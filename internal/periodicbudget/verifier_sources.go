package periodicbudget

// Every relation starts at the real source, including source rows which have no
// allocation. UNION keys are typed JSON arrays and never leave this process.
const verifierSourcesSQL = `WITH
customer_scopes AS (
 SELECT c.id control_id,c.scope,r.id subject_id,v.id reservation_id,v.status,
 CASE c.scope WHEN 'tenant' THEN r.pb_tenant_control ELSE r.pb_key_control END marker
 FROM @requests@ r JOIN @reservations@ v ON v.request_id=r.id AND v.tenant_id=r.tenant_id
 JOIN @controls@ c ON (c.scope='tenant' AND c.tenant_id=r.tenant_id)
 OR (c.scope='key' AND c.tenant_id=r.tenant_id AND c.key_id=r.key_id)
), supplier_scopes AS (
 SELECT c.id control_id,p.attempt_id subject_id,p.pb_control marker,p.state
 FROM @procurement@ p JOIN @controls@ c ON c.scope='procurement' AND c.account_id=p.account_id
), required AS (
 SELECT control_id,'customer_request'::text subject_type,subject_id FROM customer_scopes
 WHERE marker=control_id OR status IN ('reserved','pending_reconciliation')
 UNION ALL SELECT control_id,'supplier_attempt',subject_id FROM supplier_scopes
 WHERE marker=control_id OR state IN ('prepared','sending','unknown','disputed')
), membership AS (
 SELECT control_id,'customer_request'::text subject_type,subject_id FROM customer_scopes WHERE marker IS NULL AND status IN ('settled','released')
 UNION ALL SELECT control_id,'supplier_attempt',subject_id FROM supplier_scopes WHERE marker IS NULL AND state IN ('settled','released')
), identity_bad AS (
 SELECT 'request' kind,r.id pk FROM @requests@ r
 LEFT JOIN @tenants@ t ON t.id=r.tenant_id
 LEFT JOIN @keys@ k ON k.id=r.key_id AND k.tenant_id=r.tenant_id
 LEFT JOIN @reservations@ v ON v.request_id=r.id AND v.tenant_id=r.tenant_id
 LEFT JOIN @controls@ tc ON tc.id=r.pb_tenant_control AND tc.scope='tenant' AND tc.tenant_id=r.tenant_id
 LEFT JOIN @controls@ kc ON kc.id=r.pb_key_control AND kc.scope='key' AND kc.tenant_id=r.tenant_id AND kc.key_id=r.key_id
 WHERE t.id IS NULL OR k.id IS NULL
 OR (r.pb_tenant_control IS NOT NULL AND tc.id IS NULL) OR (r.pb_key_control IS NOT NULL AND kc.id IS NULL)
 OR ((r.pb_created_xid IS NULL) IS DISTINCT FROM (r.pb_admitted_at IS NULL))
 OR (r.pb_admitted_at IS NOT NULL AND NOT isfinite(r.pb_admitted_at))
 OR (r.pb_created_xid IS NULL AND (r.pb_tenant_control IS NOT NULL OR r.pb_key_control IS NOT NULL))
 OR (v.id IS NULL AND (r.pb_created_xid IS NOT NULL OR r.pb_tenant_control IS NOT NULL OR r.pb_key_control IS NOT NULL
     OR EXISTS(SELECT 1 FROM @allocations@ a WHERE a.subject_type='customer_request' AND a.subject_id=r.id)))
 UNION SELECT 'reservation',v.id FROM @reservations@ v
 LEFT JOIN @requests@ r ON r.id=v.request_id AND r.tenant_id=v.tenant_id
 LEFT JOIN @settlements@ s ON s.request_id=v.request_id AND s.tenant_id=v.tenant_id
 WHERE r.id IS NULL OR v.amount<0 OR v.status IS NULL OR v.status NOT IN ('reserved','pending_reconciliation','settled','released') OR (v.status IN ('settled','released') AND (s.id IS NULL OR s.status IS DISTINCT FROM v.status))
 OR (v.status IN ('reserved','pending_reconciliation') AND s.id IS NOT NULL)
 UNION SELECT 'settlement',s.id FROM @settlements@ s
 LEFT JOIN @requests@ r ON r.id=s.request_id AND r.tenant_id=s.tenant_id
 LEFT JOIN @reservations@ v ON v.request_id=s.request_id AND v.tenant_id=s.tenant_id
 WHERE r.id IS NULL OR v.id IS NULL OR v.status IS DISTINCT FROM s.status OR s.status IS NULL OR s.status NOT IN ('settled','released') OR s.charged_amount<0 OR (s.status='released' AND s.charged_amount<>0)
 UNION SELECT 'supplier',p.attempt_id FROM @procurement@ p
 LEFT JOIN @attempts@ u ON u.id=p.attempt_id
 LEFT JOIN @requests@ r ON r.id=u.request_id AND r.tenant_id=u.tenant_id
 LEFT JOIN @bindings@ b ON b.id=p.binding_id AND b.account_id=p.account_id
 LEFT JOIN @accounts@ q ON q.id=p.account_id
 LEFT JOIN @controls@ c ON c.id=p.pb_control AND c.scope='procurement' AND c.account_id=p.account_id
 WHERE u.id IS NULL OR r.id IS NULL OR b.id IS NULL OR q.id IS NULL OR p.state IS NULL OR p.state NOT IN ('prepared','sending','unknown','disputed','settled','released')
 OR (u.credential_id,u.channel_id) IS DISTINCT FROM (b.credential_id,b.channel_id)
 OR (p.pb_control IS NOT NULL AND c.id IS NULL)
 OR ((p.pb_created_xid IS NULL) IS DISTINCT FROM (p.pb_admitted_at IS NULL))
 OR (p.pb_admitted_at IS NOT NULL AND NOT isfinite(p.pb_admitted_at))
 OR (p.pb_created_xid IS NULL AND p.pb_control IS NOT NULL)
 OR p.pb_created_xid IS DISTINCT FROM u.pb_created_xid
 UNION SELECT 'procurement_event',p.id FROM @pe@ p
 LEFT JOIN @procurement@ r ON r.attempt_id=p.attempt_id AND r.account_id=p.account_id WHERE r.attempt_id IS NULL
), first_event AS (
 SELECT DISTINCT ON (allocation_id) * FROM @events@ ORDER BY allocation_id,seq,id
), facts AS (
 SELECT a.*,c.scope,c.tenant_id ctenant,c.key_id ckey,c.account_id caccount,c.created_xid control_xid,
 r.id rid,r.tenant_id rtenant,r.key_id rkey,r.pb_created_xid request_xid,r.pb_admitted_at request_at,
 CASE c.scope WHEN 'tenant' THEN r.pb_tenant_control ELSE r.pb_key_control END customer_marker,
 v.id reservation_id,v.amount reservation_upper,v.status reservation_status,
 s.id settlement_id,s.charged_amount,s.status settlement_status,
 p.attempt_id pid,p.account_id paccount,p.upper_amount procurement_upper,p.held_amount procurement_held,p.spent_amount procurement_spent,p.state procurement_state,p.pb_control supplier_marker,p.pb_admitted_at procurement_at,p.pb_created_xid procurement_xid,
 u.pb_created_xid attempt_xid,
 f.id first_id,f.source_kind first_kind,f.source_id first_source,f.created_xid first_xid,
 f.before_held first_before_held,f.before_spent first_before_spent,f.after_held first_held,f.after_spent first_spent,f.after_source_spent first_source_spent,f.after_state first_state,f.allocation_before_revision first_revision,
 pe.id origin_pe_id,pe.attempt_id origin_pe_attempt,pe.account_id origin_pe_account,pe.type origin_pe_type,pe.event_key origin_pe_key,pe.held_delta origin_pe_held,pe.spent_delta origin_pe_spent,pe.pb_created_xid origin_pe_xid,
 w.id wid,w.control_id wcontrol,w.start_at wstart,w.end_at wend
 FROM @allocations@ a LEFT JOIN @controls@ c ON c.id=a.control_id
 LEFT JOIN @requests@ r ON a.subject_type='customer_request' AND r.id=a.subject_id
 LEFT JOIN @reservations@ v ON v.request_id=r.id AND v.tenant_id=r.tenant_id
 LEFT JOIN @settlements@ s ON s.request_id=r.id AND s.tenant_id=r.tenant_id
 LEFT JOIN @procurement@ p ON a.subject_type='supplier_attempt' AND p.attempt_id=a.subject_id
 LEFT JOIN @attempts@ u ON u.id=p.attempt_id
 LEFT JOIN first_event f ON f.allocation_id=a.id
 LEFT JOIN @pe@ pe ON pe.id=a.origin_source_id
 LEFT JOIN @windows@ w ON w.id=a.window_id
), orphan AS (
 SELECT id FROM facts WHERE scope IS NULL OR
 (subject_type='customer_request' AND (rid IS NULL OR reservation_id IS NULL OR NOT
   ((scope='tenant' AND ctenant=rtenant) OR (scope='key' AND ctenant=rtenant AND ckey=rkey))))
 OR (subject_type='supplier_attempt' AND (pid IS NULL OR scope<>'procurement' OR caccount IS DISTINCT FROM paccount))
 OR subject_type NOT IN ('customer_request','supplier_attempt')
), origin_bad AS (
 SELECT id FROM facts WHERE first_id IS NULL OR first_revision IS DISTINCT FROM 0::bigint OR upper_amount<0 OR baseline<0 OR baseline::text IN ('NaN','Infinity','-Infinity')
 OR first_xid IS DISTINCT FROM created_xid OR first_before_held IS DISTINCT FROM 0::numeric OR first_before_spent IS DISTINCT FROM 0::numeric
 OR (subject_type='customer_request' AND (upper_amount IS DISTINCT FROM reservation_upper OR baseline<>0))
 OR (subject_type='supplier_attempt' AND upper_amount::numeric IS DISTINCT FROM procurement_upper)
 OR (origin='new' AND (baseline<>0 OR wid IS NULL OR wcontrol IS DISTINCT FROM control_id
    OR admitted_at IS NULL OR NOT isfinite(admitted_at) OR admitted_at<wstart OR admitted_at>=wend
    OR (subject_type='customer_request' AND (customer_marker IS DISTINCT FROM control_id OR admitted_at IS DISTINCT FROM request_at
      OR created_xid IS DISTINCT FROM request_xid OR origin_source_id IS DISTINCT FROM reservation_id
      OR first_kind IS DISTINCT FROM 'reservation' OR first_source IS DISTINCT FROM reservation_id
      OR (first_held,first_spent,first_source_spent,first_state) IS DISTINCT FROM (reservation_upper::numeric,0::numeric,0::numeric,'held'::text)))
    OR (subject_type='supplier_attempt' AND (supplier_marker IS DISTINCT FROM control_id OR admitted_at IS DISTINCT FROM procurement_at
      OR created_xid IS DISTINCT FROM procurement_xid OR created_xid IS DISTINCT FROM attempt_xid OR created_xid IS DISTINCT FROM origin_pe_xid
      OR origin_pe_id IS NULL OR origin_pe_attempt IS DISTINCT FROM subject_id OR origin_pe_account IS DISTINCT FROM caccount
      OR (origin_pe_type,origin_pe_key,origin_pe_held,origin_pe_spent) IS DISTINCT FROM ('reserved'::text,'reserve:'||subject_id,upper_amount::numeric,0::numeric)
      OR first_kind IS DISTINCT FROM 'procurement_event' OR first_source IS DISTINCT FROM origin_source_id
      OR (first_held,first_spent,first_source_spent,first_state) IS DISTINCT FROM (upper_amount::numeric,0::numeric,0::numeric,'held'::text)))))
 OR (origin IN ('legacy_import','legacy_catchup') AND (window_id IS NOT NULL OR admitted_at IS NOT NULL
    OR (subject_type='customer_request' AND customer_marker IS NOT NULL) OR (subject_type='supplier_attempt' AND supplier_marker IS NOT NULL)))
 OR (origin='legacy_import' AND (created_xid IS DISTINCT FROM control_xid OR origin_source_id IS DISTINCT FROM subject_id
    OR first_kind IS DISTINCT FROM 'legacy_import' OR first_source IS DISTINCT FROM subject_id
    OR first_spent IS DISTINCT FROM 0::numeric OR first_source_spent IS DISTINCT FROM baseline
    OR (subject_type='customer_request' AND (first_held,first_state) IS DISTINCT FROM (reservation_upper::numeric,'held'::text))
    OR (subject_type='supplier_attempt' AND first_state NOT IN ('held','disputed'))))
 OR (origin='legacy_catchup' AND (subject_type<>'supplier_attempt' OR origin_pe_id IS NULL
    OR origin_pe_attempt IS DISTINCT FROM subject_id OR origin_pe_account IS DISTINCT FROM caccount
    OR created_xid IS DISTINCT FROM origin_pe_xid OR first_kind IS DISTINCT FROM 'procurement_event' OR first_source IS DISTINCT FROM origin_source_id))
 OR origin NOT IN ('new','legacy_import','legacy_catchup')
), source_bad AS (
 SELECT id FROM facts WHERE held_amount<0 OR spent_amount<0 OR source_held<0 OR source_spent<0
 OR held_amount::text IN ('NaN','Infinity','-Infinity') OR spent_amount::text IN ('NaN','Infinity','-Infinity')
 OR source_held::text IN ('NaN','Infinity','-Infinity') OR source_spent::text IN ('NaN','Infinity','-Infinity') OR state IS NULL OR state NOT IN ('held','disputed','settled','released') OR
 (subject_type='customer_request' AND (
   (reservation_status IN ('reserved','pending_reconciliation') AND
     (held_amount,spent_amount,source_held,source_spent,state) IS DISTINCT FROM (reservation_upper::numeric,0::numeric,reservation_upper::numeric,0::numeric,'held'::text))
   OR (reservation_status IN ('settled','released') AND (settlement_id IS NULL OR settlement_status IS DISTINCT FROM reservation_status
     OR (held_amount,spent_amount,source_held,source_spent,state) IS DISTINCT FROM (0::numeric,charged_amount::numeric,0::numeric,charged_amount::numeric,settlement_status)))))
 OR (subject_type='supplier_attempt' AND
   (held_amount,spent_amount,source_held,source_spent,state) IS DISTINCT FROM
   (procurement_held,greatest(procurement_spent-baseline,0::numeric),procurement_held,procurement_spent,
    CASE procurement_state WHEN 'settled' THEN 'settled' WHEN 'released' THEN 'released' WHEN 'disputed' THEN 'disputed' ELSE 'held' END))
)
SELECT (SELECT count(*) FROM identity_bad),
 (SELECT count(*) FROM (SELECT DISTINCT control_id,subject_type,subject_id FROM required r WHERE NOT EXISTS(SELECT 1 FROM @allocations@ a WHERE (a.control_id,a.subject_type,a.subject_id)=(r.control_id,r.subject_type,r.subject_id))) missing),
 (SELECT count(DISTINCT id) FROM orphan),(SELECT count(DISTINCT id) FROM origin_bad),(SELECT count(DISTINCT id) FROM source_bad),
 (SELECT count(*) FROM (SELECT DISTINCT control_id,subject_type,subject_id FROM membership r WHERE NOT EXISTS(SELECT 1 FROM @allocations@ a WHERE (a.control_id,a.subject_type,a.subject_id)=(r.control_id,r.subject_type,r.subject_id))) unknown)`
