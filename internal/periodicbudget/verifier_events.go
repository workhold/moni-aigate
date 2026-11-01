package periodicbudget

const verifierEventsSQL = `WITH expected AS (
 SELECT a.control_id,a.id allocation_id,
 CASE a.origin WHEN 'new' THEN 'reservation' ELSE 'legacy_import' END source_kind,
 CASE a.origin WHEN 'new' THEN v.id ELSE a.subject_id END source_id
 FROM @allocations@ a JOIN @requests@ r ON a.subject_type='customer_request' AND r.id=a.subject_id
 JOIN @reservations@ v ON v.request_id=r.id AND v.tenant_id=r.tenant_id
 WHERE a.origin IN ('new','legacy_import')
 UNION SELECT a.control_id,a.id,'settlement',s.id
 FROM @allocations@ a JOIN @settlements@ s ON a.subject_type='customer_request' AND s.request_id=a.subject_id
 UNION SELECT a.control_id,a.id,'legacy_import',a.subject_id FROM @allocations@ a
 JOIN @procurement@ p ON p.attempt_id=a.subject_id
 WHERE a.subject_type='supplier_attempt' AND a.origin='legacy_import'
 UNION SELECT a.control_id,a.id,'procurement_event',p.id FROM @allocations@ a
 JOIN @controls@ c ON c.id=a.control_id AND c.scope='procurement'
 JOIN @pe@ p ON p.attempt_id=a.subject_id AND p.account_id=c.account_id
 WHERE a.subject_type='supplier_attempt' AND (a.origin='new' OR (a.origin='legacy_catchup' AND p.id=a.origin_source_id))
), ordered AS (
 SELECT e.*,a.id aid,a.subject_type,a.subject_id,a.origin,a.origin_source_id,a.baseline,a.window_id,
 c.id cid,c.account_id,
 row_number() OVER(PARTITION BY e.control_id ORDER BY e.seq,e.id) control_n,
 row_number() OVER(PARTITION BY e.control_id,e.allocation_id ORDER BY e.seq,e.id) allocation_n,
 row_number() OVER(PARTITION BY e.control_id,a.window_id ORDER BY e.seq,e.id) window_n,
 lag(e.after_held,1,0::numeric) OVER(PARTITION BY e.control_id,e.allocation_id ORDER BY e.seq,e.id) prior_held,
 lag(e.after_spent,1,0::numeric) OVER(PARTITION BY e.control_id,e.allocation_id ORDER BY e.seq,e.id) prior_spent,
 lag(e.after_source_spent,1,a.baseline) OVER(PARTITION BY e.control_id,e.allocation_id ORDER BY e.seq,e.id) prior_source_spent,
 lag(e.after_state) OVER(PARTITION BY e.control_id,e.allocation_id ORDER BY e.seq,e.id) prior_state
 FROM @events@ e LEFT JOIN @allocations@ a ON a.id=e.allocation_id AND a.control_id=e.control_id
 LEFT JOIN @controls@ c ON c.id=e.control_id
), facts AS (
 SELECT e.*,v.id vid,v.request_id vrequest,v.amount reserve_amount,
 s.id sid,s.request_id srequest,s.charged_amount,s.status settlement_state,
 p.id peid,p.attempt_id pattempt,p.account_id paccount,p.type petype,p.event_key pekey,p.held_delta peheld,p.spent_delta pespent,p.pb_created_xid pexid,
 CASE p.type WHEN 'reserved' THEN 'held' WHEN 'consumption_unknown' THEN 'held' WHEN 'recovered_unknown' THEN 'held'
 WHEN 'released_no_consumption' THEN 'released' WHEN 'expired_without_permit' THEN 'released'
 WHEN 'cost_confirmed' THEN 'settled' WHEN 'cost_corrected' THEN 'settled' WHEN 'evidence_disputed' THEN 'disputed' END pe_state,
 r.id rid,pr.attempt_id prid
 FROM ordered e
 LEFT JOIN @reservations@ v ON v.id=e.source_id
 LEFT JOIN @settlements@ s ON s.id=e.source_id
 LEFT JOIN @pe@ p ON p.id=e.source_id
 LEFT JOIN @requests@ r ON r.id=e.subject_id
 LEFT JOIN @procurement@ pr ON pr.attempt_id=e.subject_id
), orphan AS (
 SELECT id FROM facts WHERE aid IS NULL OR cid IS NULL
 OR (source_kind='reservation' AND vid IS NULL) OR (source_kind='settlement' AND sid IS NULL)
 OR (source_kind='procurement_event' AND peid IS NULL)
 OR (source_kind='legacy_import' AND ((subject_type='customer_request' AND rid IS NULL) OR (subject_type='supplier_attempt' AND prid IS NULL)))
 OR source_kind NOT IN ('reservation','settlement','procurement_event','legacy_import')
), binding AS (
 SELECT id FROM facts WHERE
 (source_kind='reservation' AND (subject_type IS DISTINCT FROM 'customer_request' OR origin IS DISTINCT FROM 'new'
  OR vrequest IS DISTINCT FROM subject_id OR source_id IS DISTINCT FROM origin_source_id OR allocation_n<>1
  OR (after_held,after_spent,after_source_spent,after_state) IS DISTINCT FROM (reserve_amount::numeric,0::numeric,0::numeric,'held'::text)))
 OR (source_kind='settlement' AND (subject_type IS DISTINCT FROM 'customer_request' OR srequest IS DISTINCT FROM subject_id OR allocation_n<>2
  OR (after_held,after_spent,after_source_spent,after_state) IS DISTINCT FROM (0::numeric,charged_amount::numeric,charged_amount::numeric,settlement_state)))
 OR (source_kind='legacy_import' AND (origin IS DISTINCT FROM 'legacy_import' OR source_id IS DISTINCT FROM subject_id OR allocation_n<>1
  OR (subject_type='customer_request' AND (after_state IS DISTINCT FROM 'held' OR after_spent<>0 OR after_source_spent<>0))
  OR (subject_type='supplier_attempt' AND (after_state NOT IN ('held','disputed') OR after_spent<>0 OR after_source_spent IS DISTINCT FROM baseline))))
 OR (source_kind='procurement_event' AND (subject_type IS DISTINCT FROM 'supplier_attempt' OR pattempt IS DISTINCT FROM subject_id
  OR paccount IS DISTINCT FROM account_id OR after_state IS DISTINCT FROM pe_state OR created_xid IS DISTINCT FROM pexid
  OR (petype='reserved' AND pekey IS DISTINCT FROM 'reserve:'||subject_id)
  OR (allocation_n=1 AND origin IN ('new','legacy_catchup') AND source_id IS DISTINCT FROM origin_source_id)))
 UNION SELECT e.id FROM @events@ e JOIN (
  SELECT control_id,source_kind,source_id FROM @events@ GROUP BY control_id,source_kind,source_id HAVING count(*)<>1
 ) d ON (e.control_id,e.source_kind,e.source_id)=(d.control_id,d.source_kind,d.source_id)
), chain AS (
 SELECT id FROM facts WHERE seq<>control_n OR allocation_before_revision<>allocation_n-1
 OR (before_held,before_spent) IS DISTINCT FROM (prior_held,prior_spent)
 OR (held_delta,spent_delta) IS DISTINCT FROM (after_held-before_held,after_spent-before_spent)
 OR before_held<0 OR before_spent<0 OR after_held<0 OR after_source_spent<0 OR after_spent<0
 OR before_held::text IN ('NaN','Infinity','-Infinity') OR before_spent::text IN ('NaN','Infinity','-Infinity')
 OR after_held::text IN ('NaN','Infinity','-Infinity') OR after_spent::text IN ('NaN','Infinity','-Infinity')
 OR after_source_spent::text IN ('NaN','Infinity','-Infinity') OR held_delta::text IN ('NaN','Infinity','-Infinity') OR spent_delta::text IN ('NaN','Infinity','-Infinity')
 OR after_state IS NULL OR after_state NOT IN ('held','disputed','settled','released')
 OR after_spent IS DISTINCT FROM greatest(after_source_spent-baseline,0::numeric)
 OR (window_id IS NULL AND window_before_revision IS NOT NULL)
 OR (window_id IS NOT NULL AND window_before_revision IS DISTINCT FROM window_n-1)
 OR (source_kind='procurement_event' AND
  (after_held,after_source_spent) IS DISTINCT FROM (prior_held+peheld,prior_source_spent+pespent))
 OR (subject_type='customer_request' AND (allocation_n>2 OR (allocation_n=2 AND prior_state IS DISTINCT FROM 'held')))
 OR (subject_type='supplier_attempt' AND allocation_n>1 AND NOT (
   (prior_state='held' AND after_state IN ('held','settled','released','disputed')) OR
   (prior_state='settled' AND after_state IN ('settled','disputed')) OR
   (prior_state='released' AND after_state IN ('released','settled','disputed')) OR
   (prior_state='disputed' AND after_state IN ('disputed','settled'))))
)
SELECT (SELECT count(*) FROM expected x WHERE NOT EXISTS(SELECT 1 FROM @events@ e
 WHERE (e.control_id,e.allocation_id,e.source_kind,e.source_id)=(x.control_id,x.allocation_id,x.source_kind,x.source_id))),
 (SELECT count(DISTINCT id) FROM orphan),(SELECT count(DISTINCT id) FROM binding),(SELECT count(DISTINCT id) FROM chain)`
