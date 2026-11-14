-- 034.4 cash-refund workflow, exact amount/actor guards, and pending alert kind.
-- Append-only. No external transfer, background recovery writer, or rate-adjustment API.

ALTER TABLE cash_refund_orders ADD COLUMN approved_at timestamptz;
ALTER TABLE cash_refund_orders DISABLE TRIGGER cash_refund_order_fact;
UPDATE cash_refund_orders r SET approved_at=(SELECT j.created_at FROM journals j WHERE j.id=r.hold_journal_id) WHERE r.hold_journal_id IS NOT NULL;
ALTER TABLE cash_refund_orders ENABLE TRIGGER cash_refund_order_fact;

ALTER TABLE financial_idempotency
 DROP CONSTRAINT financial_idempotency_operation_check,
 ADD CONSTRAINT financial_idempotency_operation_check CHECK(operation IN ('credit','refund','wallet_request','wallet_request_close','cash_refund_create','cash_refund_approve','cash_refund_execute','cash_refund_complete','cash_refund_cancel','cash_refund_fail'));

CREATE FUNCTION wallet_reference_valid(v text,max_bytes integer) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT max_bytes>0 AND max_bytes<=1024 AND v IS NOT NULL AND octet_length(v) BETWEEN 1 AND max_bytes
 AND position('://' IN v)=0 AND v !~* '^(sk-|mg_|bearer)' AND v ~ '^[[:alnum:]:_./-]+$'
$$;

ALTER TABLE cash_refund_orders
 ADD CONSTRAINT cash_refund_business_reference_check CHECK(wallet_reference_valid(business_ref,200)),
 ADD CONSTRAINT cash_refund_reason_check CHECK(octet_length(reason) BETWEEN 1 AND 200 AND btrim(reason)=reason AND reason !~ '[[:cntrl:]]'),
 ADD CONSTRAINT cash_refund_coin_amount_check CHECK(coin_amount IS NULL OR coin_amount>0),
 ADD CONSTRAINT cash_refund_fiat_amount_check CHECK(fiat_amount IS NULL OR fiat_amount>=0),
 ADD CONSTRAINT cash_refund_paid_fiat_amount_check CHECK(paid_fiat_amount IS NULL OR paid_fiat_amount>=0),
 ADD CONSTRAINT cash_refund_transfer_order_reference_check CHECK(transfer_order_ref IS NULL OR wallet_reference_valid(transfer_order_ref,256)),
 ADD CONSTRAINT cash_refund_transfer_proof_reference_check CHECK(transfer_proof_ref IS NULL OR wallet_reference_valid(transfer_proof_ref,256)),
 ADD CONSTRAINT cash_refund_approved_at_check CHECK(approved_at IS NULL OR isfinite(approved_at));
CREATE UNIQUE INDEX cash_refund_one_open_per_lot ON cash_refund_orders(lot_id) WHERE state IN ('requested','approved','transfer_executed');
CREATE INDEX cash_refund_pending_alert ON cash_refund_orders(tenant_id,approved_at,id COLLATE "C") WHERE state IN ('approved','transfer_executed');

CREATE FUNCTION wallet_cash_refund_actor_valid(actor text,required text,single_snapshot boolean) RETURNS boolean LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM users u WHERE u.id=actor AND u.status='active' AND required IN ('finance_operator','finance_approver')
 AND (u.platform_role=required OR (u.platform_role='admin' AND single_snapshot)))
$$;

CREATE FUNCTION wallet_cash_refund_approval_valid(maker text,approver text,requested_by text,single_snapshot boolean) RETURNS boolean LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT wallet_cash_refund_actor_valid(maker,'finance_operator',single_snapshot)
 AND wallet_cash_refund_actor_valid(approver,'finance_approver',single_snapshot)
 AND ((approver<>maker AND approver IS DISTINCT FROM requested_by)
  OR (single_snapshot AND approver=maker AND requested_by=maker
   AND EXISTS(SELECT 1 FROM users a JOIN users m ON m.id=maker AND m.platform_role='admin' AND m.status='active' WHERE a.id=approver AND a.platform_role='admin' AND a.status='active')))
$$;

CREATE FUNCTION wallet_cash_refund_fiat_amount(k bigint,f bigint,c bigint,bn bigint,bd bigint,cn bigint,cd bigint) RETURNS bigint LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN k<=0 OR f<=0 OR c<=0 OR k>c OR bn<=0 OR bd<=0 OR cn<=0 OR cd<=0 THEN NULL
 WHEN cn::numeric*bd::numeric>=cd::numeric*bn::numeric THEN floor(k::numeric*f::numeric/c::numeric)
 ELSE floor(k::numeric*f::numeric*cn::numeric*bd::numeric/(c::numeric*cd::numeric*bn::numeric)) END::bigint
$$;

CREATE FUNCTION wallet_cash_refund_order_transition_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'cash refund orders cannot be deleted'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'requested' OR NEW.approved_at IS NOT NULL OR NEW.coin_amount IS NOT NULL OR NEW.approver IS NOT NULL OR NEW.transfer_executor IS NOT NULL
   OR NEW.fiat_currency IS NOT NULL OR NEW.fiat_amount IS NOT NULL OR NEW.buy_rate_version_id IS NOT NULL OR NEW.refund_rate_version_id IS NOT NULL
   OR NEW.transfer_order_ref IS NOT NULL OR NEW.transfer_proof_ref IS NOT NULL OR NEW.paid_fiat_amount IS NOT NULL
   OR NEW.hold_journal_id IS NOT NULL OR NEW.retire_journal_id IS NOT NULL OR NEW.release_journal_id IS NOT NULL OR NEW.single_operator THEN
   RAISE EXCEPTION 'cash refund requested field shape';
  END IF;
  IF NOT wallet_cash_refund_actor_valid(NEW.maker,'finance_operator',wallet_single_operator()) THEN RAISE EXCEPTION 'cash refund maker role'; END IF;
  IF NEW.requested_by IS NULL THEN RAISE EXCEPTION 'cash refund requester required'; END IF;
  IF NEW.requested_by<>NEW.maker AND NOT EXISTS(SELECT 1 FROM tenants t JOIN memberships m ON m.tenant_id=t.id AND m.user_id=NEW.requested_by JOIN users u ON u.id=m.user_id
   WHERE t.id=NEW.tenant_id AND t.status='active' AND m.status='active' AND m.role='owner' AND u.status='active') THEN RAISE EXCEPTION 'cash refund requester owner'; END IF;
  RETURN NEW;
 END IF;
 IF OLD.state IN ('completed','cancelled','failed') THEN
  IF (NEW.id,NEW.tenant_id,NEW.lot_id,NEW.business_ref,NEW.coin_amount,NEW.reason,NEW.state,NEW.requested_by,NEW.maker,NEW.approver,NEW.transfer_executor,NEW.fiat_currency,NEW.fiat_amount,NEW.buy_rate_version_id,NEW.refund_rate_version_id,NEW.single_operator,NEW.transfer_order_ref,NEW.transfer_proof_ref,NEW.paid_fiat_amount,NEW.approved_at,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.lot_id,OLD.business_ref,OLD.coin_amount,OLD.reason,OLD.state,OLD.requested_by,OLD.maker,OLD.approver,OLD.transfer_executor,OLD.fiat_currency,OLD.fiat_amount,OLD.buy_rate_version_id,OLD.refund_rate_version_id,OLD.single_operator,OLD.transfer_order_ref,OLD.transfer_proof_ref,OLD.paid_fiat_amount,OLD.approved_at,OLD.created_at) THEN
   RAISE EXCEPTION 'cash refund is terminal';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.state=OLD.state THEN
  IF OLD.state<>'approved' OR (NEW.approver,NEW.single_operator,NEW.approved_at,NEW.transfer_executor,NEW.transfer_order_ref,NEW.transfer_proof_ref,NEW.paid_fiat_amount,NEW.retire_journal_id,NEW.release_journal_id)
   IS DISTINCT FROM (OLD.approver,OLD.single_operator,OLD.approved_at,OLD.transfer_executor,OLD.transfer_order_ref,OLD.transfer_proof_ref,OLD.paid_fiat_amount,OLD.retire_journal_id,OLD.release_journal_id) THEN
   RAISE EXCEPTION 'cash refund same-state mutation';
  END IF;
  RETURN NEW;
 END IF;
 IF OLD.state<>'requested' AND (NEW.approver,NEW.single_operator,NEW.approved_at,NEW.hold_journal_id) IS DISTINCT FROM (OLD.approver,OLD.single_operator,OLD.approved_at,OLD.hold_journal_id) THEN RAISE EXCEPTION 'cash refund approval identity'; END IF;
 IF OLD.state IN ('approved','transfer_executed') AND NEW.state<>OLD.state
  AND (NEW.coin_amount,NEW.fiat_currency,NEW.fiat_amount,NEW.buy_rate_version_id,NEW.refund_rate_version_id) IS DISTINCT FROM (OLD.coin_amount,OLD.fiat_currency,OLD.fiat_amount,OLD.buy_rate_version_id,OLD.refund_rate_version_id) THEN RAISE EXCEPTION 'cash refund frozen amount'; END IF;
 IF OLD.state='requested' AND NEW.state='approved' THEN
  NEW.approved_at:=clock_timestamp();
  IF NEW.approver IS NULL OR NEW.single_operator IS DISTINCT FROM wallet_single_operator() OR NOT wallet_cash_refund_approval_valid(NEW.maker,NEW.approver,NEW.requested_by,NEW.single_operator) THEN RAISE EXCEPTION 'cash refund approval roles'; END IF;
  RETURN NEW;
 END IF;
 IF OLD.state='requested' AND NEW.state='cancelled' THEN RETURN NEW; END IF;
 IF OLD.state='approved' AND NEW.state='transfer_executed' THEN
  IF NEW.transfer_executor IS NULL OR NEW.transfer_order_ref IS NULL OR NOT wallet_cash_refund_actor_valid(NEW.transfer_executor,'finance_operator',NEW.single_operator) THEN RAISE EXCEPTION 'cash refund transfer roles'; END IF;
  IF NOT NEW.single_operator AND NEW.transfer_executor=NEW.approver THEN RAISE EXCEPTION 'cash refund transfer separation'; END IF;
  IF NEW.single_operator AND NEW.transfer_executor=NEW.approver AND NOT EXISTS(SELECT 1 FROM users a JOIN users e ON e.id=NEW.transfer_executor AND e.platform_role='admin' AND e.status='active' WHERE a.id=NEW.approver AND a.platform_role='admin' AND a.status='active') THEN RAISE EXCEPTION 'cash refund single operator admin'; END IF;
  RETURN NEW;
 END IF;
 IF OLD.state='approved' AND NEW.state IN ('cancelled','failed') THEN RETURN NEW; END IF;
 IF OLD.state='transfer_executed' AND NEW.state IN ('completed','failed') THEN
  IF (NEW.transfer_executor,NEW.transfer_order_ref) IS DISTINCT FROM (OLD.transfer_executor,OLD.transfer_order_ref) THEN RAISE EXCEPTION 'cash refund transfer identity'; END IF;
  RETURN NEW;
 END IF;
 RAISE EXCEPTION 'cash refund state transition';
END $$;
CREATE TRIGGER cash_refund_order_transition BEFORE INSERT OR UPDATE ON cash_refund_orders FOR EACH ROW EXECUTE FUNCTION wallet_cash_refund_order_transition_guard();

CREATE OR REPLACE FUNCTION wallet_cash_refund_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN cash_refund_orders r ON r.id=j.reference AND r.tenant_id=j.tenant_id
 WHERE j.id=journal_ref AND j.currency='COIN' AND funding_uuid(j.operation_id) AND j.operation_id<>r.id AND j.operation_id<>r.business_ref AND r.coin_amount>0
 AND ((j.kind='cash_refund_hold' AND r.state IN ('approved','transfer_executed','completed','cancelled','failed') AND r.hold_journal_id=j.id AND j.actor=r.approver)
  OR (j.kind='cash_refund_retire' AND r.state='completed' AND r.retire_journal_id=j.id
   AND (wallet_cash_refund_actor_valid(j.actor,'finance_operator',r.single_operator) OR wallet_cash_refund_actor_valid(j.actor,'finance_approver',r.single_operator)))
  OR (j.kind='cash_refund_release' AND r.state IN ('cancelled','failed') AND r.release_journal_id=j.id
   AND ((r.state='cancelled' AND ((j.actor=r.maker AND wallet_cash_refund_actor_valid(j.actor,'finance_operator',r.single_operator)) OR (j.actor=r.approver AND wallet_cash_refund_actor_valid(j.actor,'finance_approver',r.single_operator))))
    OR (r.state='failed' AND (wallet_cash_refund_actor_valid(j.actor,'finance_operator',r.single_operator) OR wallet_cash_refund_actor_valid(j.actor,'finance_approver',r.single_operator))))))
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND CASE j.kind
  WHEN 'cash_refund_hold' THEN EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=r.tenant_id AND a.kind='AVAILABLE' AND l.signed_delta=-r.coin_amount)
   AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=r.tenant_id AND a.kind='REFUND_HELD' AND l.signed_delta=r.coin_amount)
  WHEN 'cash_refund_retire' THEN EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=r.tenant_id AND a.kind='REFUND_HELD' AND l.signed_delta=-r.coin_amount)
   AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind='FUNDING_CLEARING' AND l.signed_delta=r.coin_amount)
  ELSE EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=r.tenant_id AND a.kind='REFUND_HELD' AND l.signed_delta=-r.coin_amount)
   AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=r.tenant_id AND a.kind='AVAILABLE' AND l.signed_delta=r.coin_amount)
 END
 AND EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.journal_id=j.id AND e.refund_order_id=r.id AND e.kind=CASE j.kind WHEN 'cash_refund_hold' THEN 'refund_hold' WHEN 'cash_refund_retire' THEN 'refund_retire' ELSE 'refund_release' END AND wallet_event_valid(e.id)))
$$;

CREATE OR REPLACE FUNCTION wallet_cash_refund_order_valid(order_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM cash_refund_orders r
 JOIN funding_lots l ON l.id=r.lot_id AND l.tenant_id=r.tenant_id AND l.refund_class='cash_original'
 JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id AND f.basis_kind='paid'
 LEFT JOIN coin_rate_versions b ON b.id=f.buy_rate_version_id AND b.fiat_currency=f.fiat_currency
 LEFT JOIN coin_rate_versions cur ON cur.id=r.refund_rate_version_id AND cur.fiat_currency=f.fiat_currency
 WHERE r.id=order_ref AND r.requested_by IS NOT NULL
 AND (r.requested_by=r.maker OR EXISTS(SELECT 1 FROM wallet_requests w WHERE w.linked_order_id=r.id AND w.tenant_id=r.tenant_id AND w.kind='cash_refund' AND w.requested_by=r.requested_by AND w.state IN ('linked','closed')))
 AND (CASE WHEN r.state='requested' OR (r.state='cancelled' AND r.hold_journal_id IS NULL) THEN
   r.approved_at IS NULL AND r.coin_amount IS NULL AND r.approver IS NULL AND r.transfer_executor IS NULL AND r.fiat_currency IS NULL AND r.fiat_amount IS NULL
   AND r.buy_rate_version_id IS NULL AND r.refund_rate_version_id IS NULL AND r.transfer_order_ref IS NULL AND r.transfer_proof_ref IS NULL AND r.paid_fiat_amount IS NULL
   AND r.retire_journal_id IS NULL AND r.release_journal_id IS NULL AND NOT r.single_operator
  ELSE
   r.approved_at IS NOT NULL AND r.approved_at>=r.created_at AND r.coin_amount>0 AND r.coin_amount<=l.issued_amount
   AND r.fiat_currency=f.fiat_currency AND r.buy_rate_version_id=f.buy_rate_version_id AND b.id IS NOT NULL AND cur.id IS NOT NULL
   AND r.refund_rate_version_id=(SELECT x.id FROM coin_rate_versions x WHERE x.fiat_currency=f.fiat_currency AND x.effective_at<=r.approved_at ORDER BY x.effective_at DESC,x.id DESC LIMIT 1)
   AND r.fiat_amount=wallet_cash_refund_fiat_amount(r.coin_amount,f.fiat_amount,l.issued_amount,b.rate_num,b.rate_den,cur.rate_num,cur.rate_den)
   AND r.single_operator=coalesce((SELECT s.single_operator FROM wallet_operator_settings s WHERE s.created_at<=r.approved_at ORDER BY s.version DESC LIMIT 1),false)
   AND wallet_cash_refund_approval_valid(r.maker,r.approver,r.requested_by,r.single_operator)
  END)
 AND CASE r.state
  WHEN 'requested' THEN r.hold_journal_id IS NULL AND r.release_journal_id IS NULL
  WHEN 'approved' THEN r.transfer_executor IS NULL AND r.transfer_order_ref IS NULL AND r.transfer_proof_ref IS NULL AND r.paid_fiat_amount IS NULL AND r.retire_journal_id IS NULL AND r.release_journal_id IS NULL
  WHEN 'transfer_executed' THEN r.transfer_executor IS NOT NULL AND r.transfer_order_ref IS NOT NULL AND r.transfer_proof_ref IS NULL AND r.paid_fiat_amount IS NULL AND r.retire_journal_id IS NULL AND r.release_journal_id IS NULL
  WHEN 'completed' THEN r.transfer_executor IS NOT NULL AND r.transfer_order_ref IS NOT NULL AND r.transfer_proof_ref IS NOT NULL AND r.paid_fiat_amount=r.fiat_amount AND r.retire_journal_id IS NOT NULL AND r.release_journal_id IS NULL
  WHEN 'cancelled' THEN r.transfer_executor IS NULL AND r.transfer_order_ref IS NULL AND r.transfer_proof_ref IS NULL AND r.paid_fiat_amount IS NULL AND r.retire_journal_id IS NULL
  ELSE r.transfer_proof_ref IS NULL AND r.paid_fiat_amount IS NULL AND r.retire_journal_id IS NULL AND r.release_journal_id IS NOT NULL
 END
 AND NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.refund_order_id=r.id AND NOT wallet_event_valid(e.id))
 AND CASE WHEN r.state='requested' THEN r.hold_journal_id IS NULL AND r.retire_journal_id IS NULL AND r.release_journal_id IS NULL
   AND (SELECT count(*) FROM funding_lot_events e WHERE e.refund_order_id=r.id)=0
   AND (SELECT count(*) FROM journals j WHERE j.reference=r.id AND j.kind IN ('cash_refund_hold','cash_refund_retire','cash_refund_release'))=0
  WHEN r.state IN ('approved','transfer_executed') THEN r.hold_journal_id IS NOT NULL AND r.retire_journal_id IS NULL AND r.release_journal_id IS NULL
   AND wallet_cash_refund_journal_valid(r.hold_journal_id)
   AND (SELECT count(*) FROM funding_lot_events e WHERE e.refund_order_id=r.id)=1
   AND (SELECT count(*) FROM journals j WHERE j.reference=r.id AND j.kind IN ('cash_refund_hold','cash_refund_retire','cash_refund_release'))=1
  WHEN r.state='completed' THEN r.hold_journal_id IS NOT NULL AND r.retire_journal_id IS NOT NULL AND r.release_journal_id IS NULL
   AND wallet_cash_refund_journal_valid(r.hold_journal_id) AND wallet_cash_refund_journal_valid(r.retire_journal_id)
   AND (SELECT count(*) FROM funding_lot_events e WHERE e.refund_order_id=r.id)=2
   AND (SELECT count(*) FROM journals j WHERE j.reference=r.id AND j.kind IN ('cash_refund_hold','cash_refund_retire','cash_refund_release'))=2
  WHEN r.state='cancelled' THEN
   (r.hold_journal_id IS NULL AND r.retire_journal_id IS NULL AND r.release_journal_id IS NULL
    AND (SELECT count(*) FROM funding_lot_events e WHERE e.refund_order_id=r.id)=0
    AND (SELECT count(*) FROM journals j WHERE j.reference=r.id AND j.kind IN ('cash_refund_hold','cash_refund_retire','cash_refund_release'))=0)
   OR (r.hold_journal_id IS NOT NULL AND r.release_journal_id IS NOT NULL AND r.retire_journal_id IS NULL
    AND wallet_cash_refund_journal_valid(r.hold_journal_id) AND wallet_cash_refund_journal_valid(r.release_journal_id)
    AND (SELECT count(*) FROM funding_lot_events e WHERE e.refund_order_id=r.id)=2
    AND (SELECT count(*) FROM journals j WHERE j.reference=r.id AND j.kind IN ('cash_refund_hold','cash_refund_retire','cash_refund_release'))=2)
  ELSE r.hold_journal_id IS NOT NULL AND r.release_journal_id IS NOT NULL AND r.retire_journal_id IS NULL
   AND wallet_cash_refund_journal_valid(r.hold_journal_id) AND wallet_cash_refund_journal_valid(r.release_journal_id)
   AND (SELECT count(*) FROM funding_lot_events e WHERE e.refund_order_id=r.id)=2
   AND (SELECT count(*) FROM journals j WHERE j.reference=r.id AND j.kind IN ('cash_refund_hold','cash_refund_retire','cash_refund_release'))=2
 END)
$$;

CREATE OR REPLACE FUNCTION wallet_request_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable wallet request'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'open' OR NEW.linked_order_id IS NOT NULL THEN RAISE EXCEPTION 'wallet request initial state'; END IF;
  IF NOT EXISTS(SELECT 1 FROM tenants t JOIN memberships m ON m.tenant_id=t.id AND m.user_id=NEW.requested_by JOIN users u ON u.id=m.user_id WHERE t.id=NEW.tenant_id AND t.status='active' AND m.status='active' AND m.role='owner' AND u.status='active') THEN RAISE EXCEPTION 'wallet request owner required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtext('wallet-request:'||NEW.tenant_id));
  IF (SELECT count(*) FROM wallet_requests WHERE tenant_id=NEW.tenant_id AND state<>'closed')>=20 THEN RAISE EXCEPTION 'wallet_request_limit'; END IF;
  RETURN NEW;
 END IF;
 IF (NEW.id,NEW.tenant_id,NEW.kind,NEW.requested_by,NEW.note,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.kind,OLD.requested_by,OLD.note,OLD.created_at) THEN RAISE EXCEPTION 'immutable wallet request'; END IF;
 IF OLD.state='open' AND NEW.state='linked' THEN
  IF OLD.linked_order_id IS NOT NULL OR NEW.linked_order_id IS NULL THEN RAISE EXCEPTION 'wallet request link identity'; END IF;
  IF NEW.kind='topup' AND EXISTS(SELECT 1 FROM credit_orders c WHERE c.id=NEW.linked_order_id AND c.tenant_id=NEW.tenant_id AND c.requested_by=NEW.requested_by AND c.state='pending' AND wallet_operator_role_valid(c.requester,'finance_operator')) THEN RETURN NEW; END IF;
  IF NEW.kind='cash_refund' AND EXISTS(SELECT 1 FROM cash_refund_orders r WHERE r.id=NEW.linked_order_id AND r.tenant_id=NEW.tenant_id AND r.requested_by=NEW.requested_by AND r.state='requested' AND wallet_cash_refund_actor_valid(r.maker,'finance_operator',wallet_single_operator())) THEN RETURN NEW; END IF;
  RAISE EXCEPTION 'wallet request link mismatch';
 END IF;
 IF OLD.state='open' AND NEW.state='closed' AND OLD.linked_order_id IS NULL AND NEW.linked_order_id IS NULL THEN RETURN NEW; END IF;
 IF OLD.state='linked' AND NEW.state='closed' AND NEW.linked_order_id=OLD.linked_order_id THEN
  IF NEW.kind='topup' AND EXISTS(SELECT 1 FROM credit_orders c WHERE c.id=NEW.linked_order_id AND c.tenant_id=NEW.tenant_id AND c.requested_by=NEW.requested_by AND c.state='approved' AND wallet_credit_pair_valid(c.id)) THEN RETURN NEW; END IF;
  IF NEW.kind='cash_refund' AND EXISTS(SELECT 1 FROM cash_refund_orders r WHERE r.id=NEW.linked_order_id AND r.tenant_id=NEW.tenant_id AND r.requested_by=NEW.requested_by AND r.state IN ('completed','cancelled','failed') AND wallet_cash_refund_order_valid(r.id)) THEN RETURN NEW; END IF;
 END IF;
 RAISE EXCEPTION 'wallet request state transition';
END $$;

CREATE OR REPLACE FUNCTION alert_parameters(k TEXT,p JSONB) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 IF p IS NULL OR octet_length(p::text)>512 THEN RETURN false; END IF;
 IF k='channel_quarantined' THEN RETURN p='{}'::jsonb; END IF;
 IF k='procurement_budget_low' THEN
  IF NOT alert_keys(p,ARRAY['threshold_amount','recovery_amount']) THEN RETURN false; END IF;
  IF jsonb_typeof(p->'threshold_amount') IS DISTINCT FROM 'string'
    OR jsonb_typeof(p->'recovery_amount') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
  IF NOT alert_uint(p->>'threshold_amount',9223372036854775807)
    OR NOT alert_uint(p->>'recovery_amount',9223372036854775807) THEN RETURN false; END IF;
  RETURN (p->>'recovery_amount')::numeric >= (p->>'threshold_amount')::numeric;
 END IF;
 IF k IN ('customer_reconciliation_aged','procurement_unresolved_aged','cash_refund_pending_aged') THEN
  IF NOT alert_keys(p,ARRAY['minimum_age_seconds','trigger_count','recovery_count']) THEN RETURN false; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_each(p) x WHERE jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
  IF NOT alert_uint(p->>'minimum_age_seconds',604800) OR NOT alert_uint(p->>'trigger_count',1000)
    OR NOT alert_uint(p->>'recovery_count',999) THEN RETURN false; END IF;
  RETURN (p->>'minimum_age_seconds')::numeric>=600 AND (p->>'trigger_count')::numeric>=1
    AND (p->>'recovery_count')::numeric<(p->>'trigger_count')::numeric;
 END IF;
 RETURN false;
END $$;

CREATE OR REPLACE FUNCTION alert_snapshot(v JSONB) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k TEXT; s TEXT;
BEGIN
 IF v IS NULL OR octet_length(v::text)>1024 OR NOT alert_keys(v,ARRAY['kind','scope_type','scope_id','label','parameters']) THEN RETURN false; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_each(v) x WHERE x.key<>'parameters' AND jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
 k:=v->>'kind'; s:=v->>'scope_type';
 RETURN alert_id(v->>'scope_id') AND alert_label(v->>'label') AND alert_parameters(k,v->'parameters')
  AND ((k='channel_quarantined' AND s='channel')
    OR (k IN ('procurement_budget_low','procurement_unresolved_aged') AND s='procurement_account')
    OR (k IN ('customer_reconciliation_aged','cash_refund_pending_aged') AND s='tenant'));
END $$;

CREATE OR REPLACE FUNCTION alert_sample(v JSONB,k TEXT) RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE n NUMERIC; age_seconds NUMERIC;
BEGIN
 IF v IS NULL OR octet_length(v::text)>2048 OR jsonb_typeof(v) IS DISTINCT FROM 'object'
   OR jsonb_typeof(v->'kind') IS DISTINCT FROM 'string' OR (v->>'kind') IS DISTINCT FROM k
   OR jsonb_typeof(v->'as_of') IS DISTINCT FROM 'string' OR NOT alert_time(v->>'as_of') THEN RETURN false; END IF;
 IF k='channel_quarantined' THEN
  RETURN alert_keys(v,ARRAY['kind','as_of','enabled','quarantined'])
   AND jsonb_typeof(v->'enabled')='boolean' AND jsonb_typeof(v->'quarantined')='boolean';
 ELSIF k='procurement_budget_low' THEN
  IF NOT alert_keys(v,ARRAY['kind','as_of','account_state','limit_amount','held_amount','spent_amount','available_amount','currency']) THEN RETURN false; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_each(v) x WHERE jsonb_typeof(x.value) IS DISTINCT FROM 'string') THEN RETURN false; END IF;
  IF (v->>'account_state') NOT IN ('active','paused') OR (v->>'currency') IS DISTINCT FROM 'CNY'
    OR NOT alert_uint(v->>'limit_amount',9223372036854775807)
    OR NOT alert_uint(v->>'held_amount',power(10::numeric,100)-1)
    OR NOT alert_uint(v->>'spent_amount',power(10::numeric,100)-1)
    OR NOT alert_signed(v->>'available_amount') THEN RETURN false; END IF;
  RETURN (v->>'available_amount')::numeric=(v->>'limit_amount')::numeric-(v->>'held_amount')::numeric-(v->>'spent_amount')::numeric;
 ELSIF k IN ('customer_reconciliation_aged','procurement_unresolved_aged','cash_refund_pending_aged') THEN
  IF NOT alert_keys(v,ARRAY['kind','as_of','minimum_age_seconds','observed_count','count_is_lower_bound','oldest_created_at','oldest_reference_id']) THEN RETURN false; END IF;
  IF jsonb_typeof(v->'minimum_age_seconds') IS DISTINCT FROM 'string'
    OR jsonb_typeof(v->'observed_count') IS DISTINCT FROM 'string'
    OR jsonb_typeof(v->'count_is_lower_bound') IS DISTINCT FROM 'boolean'
    OR NOT alert_uint(v->>'minimum_age_seconds',604800) OR NOT alert_uint(v->>'observed_count',1001) THEN RETURN false; END IF;
  n:=(v->>'observed_count')::numeric; age_seconds:=(v->>'minimum_age_seconds')::numeric;
  IF age_seconds<600 OR (v->>'count_is_lower_bound')::boolean IS DISTINCT FROM (n=1001) THEN RETURN false; END IF;
  IF n=0 THEN RETURN v->'oldest_created_at'='null'::jsonb AND v->'oldest_reference_id'='null'::jsonb; END IF;
  IF jsonb_typeof(v->'oldest_created_at') IS DISTINCT FROM 'string' OR jsonb_typeof(v->'oldest_reference_id') IS DISTINCT FROM 'string'
    OR NOT alert_time(v->>'oldest_created_at') OR NOT alert_id(v->>'oldest_reference_id') THEN RETURN false; END IF;
  RETURN (v->>'oldest_created_at')::timestamptz <= (v->>'as_of')::timestamptz-age_seconds*interval '1 second';
 END IF;
 RETURN false;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;

DO $$ DECLARE f record; BEGIN
 FOR f IN SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND p.proname IN ('wallet_reference_valid','wallet_cash_refund_actor_valid','wallet_cash_refund_approval_valid','wallet_cash_refund_fiat_amount','wallet_cash_refund_order_transition_guard','wallet_cash_refund_journal_valid','wallet_cash_refund_order_valid','wallet_request_guard','alert_parameters','alert_snapshot','alert_sample') LOOP
  EXECUTE format('ALTER FUNCTION %s SECURITY INVOKER',f.oid::regprocedure::text);
  EXECUTE format('ALTER FUNCTION %s SET search_path TO %I,pg_catalog,pg_temp',f.oid::regprocedure::text,current_schema());
  EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC',f.oid::regprocedure::text);
 END LOOP;
END $$;
