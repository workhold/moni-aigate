-- 034.2 roles, payment basis, paired manual sources and deterministic FIFO.
-- No refund buckets or coin-rate versions are introduced in this slice.

CREATE TABLE wallet_operator_settings (
 id text PRIMARY KEY,
 version bigint NOT NULL UNIQUE CHECK(version>0),
 single_operator boolean NOT NULL,
 actor text REFERENCES users(id),
 reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 200),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO wallet_operator_settings(id,version,single_operator,actor,reason)
VALUES(gen_random_uuid()::text,1,true,NULL,'034 single-operator genesis');
CREATE TRIGGER immutable_wallet_operator_setting BEFORE UPDATE OR DELETE ON wallet_operator_settings FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE wallet_requests (
 id text PRIMARY KEY,
 tenant_id text NOT NULL REFERENCES tenants(id),
 kind text NOT NULL CHECK(kind IN ('topup','cash_refund')),
 requested_by text NOT NULL REFERENCES users(id),
 note text NOT NULL CHECK(octet_length(note) BETWEEN 1 AND 1000 AND btrim(note)=note AND note!~'[[:cntrl:]]'),
 state text NOT NULL DEFAULT 'open' CHECK(state IN ('open','linked','closed')),
 linked_order_id text UNIQUE,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX wallet_requests_open ON wallet_requests(tenant_id,id) WHERE state<>'closed';

ALTER TABLE financial_idempotency
 DROP CONSTRAINT financial_idempotency_operation_check,
 ADD CONSTRAINT financial_idempotency_operation_check CHECK(operation IN ('credit','refund','wallet_request','wallet_request_close'));

ALTER TABLE credit_orders
 ADD COLUMN requested_by text REFERENCES users(id),
 ADD COLUMN basis_kind text NOT NULL DEFAULT 'paid',
 ADD COLUMN fiat_currency text,
 ADD COLUMN fiat_amount bigint,
 ADD COLUMN fx_note text,
 ADD COLUMN bonus_amount bigint NOT NULL DEFAULT 0,
 ADD COLUMN transfer_ref text,
 ADD COLUMN single_operator boolean NOT NULL DEFAULT false;
DROP TRIGGER funding_credit_guard ON credit_orders;
ALTER TABLE credit_orders DISABLE TRIGGER funding_credit_fact;
UPDATE credit_orders SET requested_by=requester,basis_kind='paid',fiat_currency='CNY',fiat_amount=amount;
ALTER TABLE credit_orders
 ALTER COLUMN requested_by SET NOT NULL,
 DROP CONSTRAINT credit_orders_amount_check,
 DROP CONSTRAINT credit_orders_state_check,
 DROP CONSTRAINT credit_orders_check,
 ADD CONSTRAINT credit_orders_amount_check CHECK(amount>=0 AND amount<=1000000000000),
 ADD CONSTRAINT credit_orders_bonus_check CHECK(bonus_amount>=0 AND bonus_amount<=1000000000000),
 ADD CONSTRAINT credit_orders_total_check CHECK(amount::numeric+bonus_amount::numeric>0 AND amount::numeric+bonus_amount::numeric<=9223372036854775807),
 ADD CONSTRAINT credit_orders_basis_check CHECK(basis_kind IN ('paid','grant','unknown')),
 ADD CONSTRAINT credit_orders_currency_check CHECK(fiat_currency IS NULL OR fiat_currency IN ('CNY','USD')),
 ADD CONSTRAINT credit_orders_fiat_check CHECK((basis_kind='paid')=(fiat_currency IS NOT NULL AND fiat_amount IS NOT NULL AND fiat_amount>0)),
 ADD CONSTRAINT credit_orders_shape_check CHECK((basis_kind='paid' AND amount>0) OR (basis_kind='grant' AND amount=0 AND bonus_amount>0) OR (basis_kind='unknown' AND amount>0 AND bonus_amount=0)),
 ADD CONSTRAINT credit_orders_state_check CHECK(state IN ('pending','approved')),
 ADD CONSTRAINT credit_orders_check CHECK(approver IS NULL OR single_operator OR (approver<>requester AND approver IS DISTINCT FROM requested_by));
ALTER TABLE credit_orders ENABLE TRIGGER funding_credit_fact;

ALTER TABLE refund_orders
 ADD COLUMN requested_by text REFERENCES users(id),
 ADD COLUMN single_operator boolean NOT NULL DEFAULT false;
ALTER TABLE refund_orders DISABLE TRIGGER wallet_source_fact;
UPDATE refund_orders SET requested_by=requester;
ALTER TABLE refund_orders
 ALTER COLUMN requested_by SET NOT NULL,
 DROP CONSTRAINT refund_orders_check,
 ADD CONSTRAINT refund_orders_check CHECK(approver IS NULL OR single_operator OR (approver<>requester AND approver IS DISTINCT FROM requested_by));
ALTER TABLE refund_orders ENABLE TRIGGER wallet_source_fact;

ALTER TABLE funding_receipts
 ADD COLUMN basis_kind text,
 ADD COLUMN fiat_currency text,
 ADD COLUMN fiat_amount bigint,
 ADD COLUMN parent_ref text;
ALTER TABLE funding_receipts DISABLE TRIGGER immutable_funding_receipt;
ALTER TABLE funding_receipts DISABLE TRIGGER wallet_source_fact;
ALTER TABLE funding_lots DISABLE TRIGGER immutable_wallet_fact;
ALTER TABLE funding_lots DISABLE TRIGGER wallet_source_fact;
UPDATE funding_receipts SET
 basis_kind=CASE WHEN source_kind IN ('manual_credit','sandbox_capture') THEN 'paid' WHEN source_kind='voucher_grant' THEN 'grant' ELSE 'opening' END,
 fiat_currency=CASE WHEN source_kind IN ('manual_credit','sandbox_capture') THEN 'CNY' ELSE NULL END,
 fiat_amount=CASE WHEN source_kind IN ('manual_credit','sandbox_capture') THEN amount ELSE NULL END,
 parent_ref=source_id;
ALTER TABLE funding_receipts
 ALTER COLUMN basis_kind SET NOT NULL,
 ALTER COLUMN parent_ref SET NOT NULL,
 DROP CONSTRAINT funding_receipts_source_kind_check,
 ADD CONSTRAINT funding_receipts_source_kind_check CHECK(source_kind IN ('manual_credit','manual_bonus','manual_unknown','voucher_grant','sandbox_capture','wallet_opening')),
 ADD CONSTRAINT funding_receipts_basis_check CHECK((source_kind,basis_kind) IN (('manual_credit','paid'),('manual_bonus','grant'),('manual_unknown','unknown'),('voucher_grant','grant'),('sandbox_capture','paid'),('wallet_opening','opening'))),
 ADD CONSTRAINT funding_receipts_fiat_check CHECK((basis_kind='paid')=(fiat_currency IS NOT NULL AND fiat_amount IS NOT NULL AND fiat_amount>0)),
 ADD CONSTRAINT funding_receipts_currency_basis_check CHECK(fiat_currency IS NULL OR fiat_currency IN ('CNY','USD'));
ALTER TABLE funding_receipts ENABLE TRIGGER immutable_funding_receipt;
ALTER TABLE funding_receipts ENABLE TRIGGER wallet_source_fact;

ALTER TABLE funding_lots
 DROP CONSTRAINT funding_lots_check1,
 DROP CONSTRAINT funding_lots_refund_class_check,
 DROP CONSTRAINT funding_lots_source_kind_check;
UPDATE funding_lots SET refund_class='cash_original' WHERE source_kind='manual_credit';
ALTER TABLE funding_lots
 ADD CONSTRAINT funding_lots_refund_class_check CHECK(refund_class IN ('nonrefundable','sandbox_original','cash_original')),
 ADD CONSTRAINT funding_lots_source_kind_check CHECK(source_kind IN ('manual_credit','manual_bonus','manual_unknown','voucher_grant','sandbox_capture','wallet_opening')),
 ADD CONSTRAINT funding_lots_source_refund_check CHECK((source_kind,refund_class) IN (('manual_credit','cash_original'),('manual_bonus','nonrefundable'),('manual_unknown','nonrefundable'),('voucher_grant','nonrefundable'),('sandbox_capture','sandbox_original'),('wallet_opening','nonrefundable')));
ALTER TABLE funding_lots ENABLE TRIGGER immutable_wallet_fact;
ALTER TABLE funding_lots ENABLE TRIGGER wallet_source_fact;

CREATE FUNCTION wallet_single_operator() RETURNS boolean LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT coalesce((SELECT single_operator FROM wallet_operator_settings ORDER BY version DESC LIMIT 1),false)
$$;
CREATE FUNCTION wallet_operator_role_valid(actor text, required text) RETURNS boolean LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM users u WHERE u.id=actor AND u.status='active' AND (u.platform_role=required OR (u.platform_role='admin' AND wallet_single_operator()))
 AND required IN ('finance_operator','finance_approver'))
$$;
CREATE FUNCTION wallet_approval_snapshot_valid(requester text, approver text, requested_by text) RETURNS boolean LANGUAGE plpgsql STABLE SECURITY INVOKER AS $$
BEGIN
 IF wallet_operator_role_valid(approver,'finance_approver') AND wallet_operator_role_valid(requester,'finance_operator') AND approver<>requester AND (requested_by IS NULL OR requested_by<>approver) THEN
  RETURN true;
 END IF;
 IF wallet_single_operator() AND requester=approver AND requested_by=approver
  AND EXISTS(SELECT 1 FROM users a JOIN users m ON m.id=requester AND m.platform_role='admin' AND m.status='active' WHERE a.id=approver AND a.platform_role='admin' AND a.status='active')
  AND wallet_operator_role_valid(approver,'finance_approver') AND wallet_operator_role_valid(requester,'finance_operator') THEN
  RETURN true;
 END IF;
 RETURN false;
END $$;
CREATE FUNCTION wallet_credit_pair_valid(credit_ref text) RETURNS boolean LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM credit_orders c WHERE c.id=credit_ref AND c.state='approved'
 AND CASE c.basis_kind
  WHEN 'paid' THEN
   (SELECT count(*) FROM funding_receipts f WHERE f.parent_ref=c.id AND f.source_kind='manual_credit')=1
   AND (SELECT count(*) FROM funding_receipts f WHERE f.parent_ref=c.id AND f.source_kind='manual_bonus')=CASE WHEN c.bonus_amount>0 THEN 1 ELSE 0 END
   AND (c.bonus_amount=0 OR (SELECT min(created_at) FROM funding_receipts f WHERE f.parent_ref=c.id AND f.source_kind='manual_bonus')>(SELECT max(created_at) FROM funding_receipts f WHERE f.parent_ref=c.id AND f.source_kind='manual_credit'))
  WHEN 'grant' THEN
   (SELECT count(*) FROM funding_receipts f WHERE f.parent_ref=c.id AND f.source_kind='manual_bonus')=1
   AND (SELECT count(*) FROM funding_receipts f WHERE f.parent_ref=c.id AND f.source_kind IN ('manual_credit','manual_unknown'))=0
  ELSE
   (SELECT count(*) FROM funding_receipts f WHERE f.parent_ref=c.id AND f.source_kind='manual_unknown')=1
   AND (SELECT count(*) FROM funding_receipts f WHERE f.parent_ref=c.id AND f.source_kind IN ('manual_credit','manual_bonus'))=0
 END
 AND NOT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.parent_ref=c.id AND (
  f.basis_kind<>CASE WHEN f.source_kind='manual_bonus' THEN 'grant' ELSE c.basis_kind END
  OR f.amount<>CASE WHEN f.source_kind='manual_bonus' THEN c.bonus_amount ELSE c.amount END
  OR (f.source_kind='manual_credit' AND (f.fiat_currency,f.fiat_amount) IS DISTINCT FROM (c.fiat_currency,c.fiat_amount))
  OR (f.source_kind<>'manual_credit' AND (f.fiat_currency IS NOT NULL OR f.fiat_amount IS NOT NULL))))
 AND ((c.basis_kind='paid' AND EXISTS(SELECT 1 FROM funding_receipts f WHERE f.parent_ref=c.id AND f.source_kind='manual_credit' AND f.journal_id=c.journal_id)
   OR c.basis_kind='grant' AND EXISTS(SELECT 1 FROM funding_receipts f WHERE f.parent_ref=c.id AND f.source_kind='manual_bonus' AND f.journal_id=c.journal_id)
   OR c.basis_kind='unknown' AND EXISTS(SELECT 1 FROM funding_receipts f WHERE f.parent_ref=c.id AND f.source_kind='manual_unknown' AND f.journal_id=c.journal_id))))
$$;

CREATE OR REPLACE FUNCTION funding_fact_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE jid TEXT; credit_state TEXT;
BEGIN
 IF TG_TABLE_NAME='credit_orders' THEN
  SELECT state,journal_id INTO credit_state,jid FROM credit_orders WHERE id=NEW.id;
  IF credit_state='pending' THEN
   IF EXISTS(SELECT 1 FROM journals WHERE kind='credit' AND operation_id=NEW.id) OR EXISTS(SELECT 1 FROM projection_outbox WHERE kind='credited' AND operation_id=NEW.id) THEN RAISE EXCEPTION 'pending credit has funding'; END IF;
   RETURN NULL;
  END IF;
 ELSIF TG_TABLE_NAME='journals' THEN
  IF NEW.kind NOT IN ('credit','credit_bonus','voucher_grant','sandbox_capture') THEN
   IF EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=NEW.id AND a.kind IN ('FUNDING_CLEARING','OPERATING_GRANT_CLEARING')) THEN RAISE EXCEPTION 'funding clearing requires source';END IF;
   RETURN NULL;
  END IF;jid:=NEW.id;
 ELSIF TG_TABLE_NAME='projection_outbox' THEN IF NEW.kind<>'credited' THEN RETURN NULL; END IF;SELECT id INTO jid FROM journals WHERE operation_id=NEW.operation_id;
 ELSE jid:=NEW.journal_id; END IF;
 IF jid IS NULL OR NOT funding_journal_valid(jid) THEN RAISE EXCEPTION 'funding source integrity failed'; END IF;
 RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION wallet_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM journals j WHERE j.id=journal_ref
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE')
   =(SELECT coalesce(sum(e.delta_available::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='HELD')
   =(SELECT coalesce(sum(e.delta_held::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='REVENUE')
   =(SELECT coalesce(sum(e.delta_consumed::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.journal_id=j.id AND NOT wallet_event_valid(e.id)))
$$;

CREATE OR REPLACE FUNCTION payment_voucher_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id
 JOIN vouchers v ON f.source_kind='voucher_grant' AND v.id=f.source_id
 JOIN voucher_batches b ON b.id=v.batch_id
 JOIN voucher_redemptions r ON r.voucher_id=v.id AND r.funding_receipt_id=f.id
 JOIN commerce_commands cmd ON cmd.operation_id=r.operation_id
 JOIN commerce_operation_receipts terminal ON terminal.operation_id=r.operation_id
 WHERE j.id=journal_ref AND j.tenant_id=f.tenant_id AND j.currency='COIN' AND f.currency='COIN' AND f.amount>0
 AND f.basis_kind='grant' AND f.fiat_currency IS NULL AND f.fiat_amount IS NULL AND f.parent_ref=v.id
 AND b.state IN ('active','revoked') AND r.id=j.operation_id AND j.kind='voucher_grant' AND j.reference=v.id AND j.actor=r.actor
 AND r.tenant_id=f.tenant_id AND r.journal_id=j.id AND r.amount=f.amount AND b.face_amount=f.amount AND r.batch_id=b.id
 AND r.created_at>=b.activated_at AND r.created_at<b.expires_at AND (b.terminal_at IS NULL OR r.created_at<=b.terminal_at)
 AND cmd.kind='voucher.redeem' AND cmd.actor=r.actor AND cmd.scope='tenant:'||r.tenant_id
 AND terminal.status='accepted' AND terminal.reason='accepted' AND terminal.resource_id=r.id AND terminal.amount=f.amount AND terminal.funding_receipt_id=f.id AND terminal.journal_id=j.id
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE' AND a.tenant_id=f.tenant_id AND l.signed_delta=f.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind='OPERATING_GRANT_CLEARING' AND l.signed_delta=-f.amount)
 AND (SELECT count(*) FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited' AND o.tenant_id=j.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=f.amount::text))
$$;

CREATE OR REPLACE FUNCTION funding_journal_valid(journal_ref text) RETURNS boolean LANGUAGE plpgsql STABLE AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM journals WHERE id=journal_ref AND kind='sandbox_capture') THEN RETURN payment_sandbox_journal_valid(journal_ref); END IF;
 IF EXISTS(SELECT 1 FROM journals WHERE id=journal_ref AND kind='wallet_opening') THEN RETURN wallet_opening_journal_valid(journal_ref); END IF;
 IF EXISTS(SELECT 1 FROM journals WHERE id=journal_ref AND kind='voucher_grant') THEN RETURN payment_voucher_journal_valid(journal_ref); END IF;
 RETURN payment_funding_journal_valid(journal_ref);
END
$$;

CREATE OR REPLACE FUNCTION payment_funding_journal_valid(journal_ref text) RETURNS boolean LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id
 JOIN credit_orders c ON c.id=f.source_id AND c.tenant_id=f.tenant_id AND c.id=f.parent_ref
 WHERE j.id=journal_ref AND wallet_credit_pair_valid(c.id)
 AND f.basis_kind=CASE WHEN f.source_kind='manual_bonus' THEN 'grant' ELSE c.basis_kind END AND j.currency='COIN' AND f.currency='COIN' AND f.amount>0
 AND ((f.source_kind='manual_credit' AND c.basis_kind='paid' AND (f.fiat_currency,f.fiat_amount)=(c.fiat_currency,c.fiat_amount))
  OR (f.source_kind='manual_bonus' AND c.basis_kind IN ('paid','grant') AND f.fiat_currency IS NULL AND f.fiat_amount IS NULL)
  OR (f.source_kind='manual_unknown' AND c.basis_kind='unknown' AND f.fiat_currency IS NULL AND f.fiat_amount IS NULL))
 AND ((f.source_kind='manual_credit' AND j.kind='credit' AND j.operation_id=c.id AND j.reference=c.id AND f.amount=c.amount)
  OR (f.source_kind='manual_bonus' AND j.kind='credit_bonus' AND j.operation_id<>c.id AND j.reference=c.id AND f.amount=c.bonus_amount)
  OR (f.source_kind='manual_unknown' AND j.kind='credit' AND j.operation_id=c.id AND j.reference=c.id AND f.amount=c.amount))
 AND j.actor=c.approver
 AND ((c.approver<>c.requester AND c.approver IS DISTINCT FROM c.requested_by)
  OR (c.single_operator AND c.approver=c.requester AND c.requested_by=c.approver
   AND EXISTS(SELECT 1 FROM users a JOIN users m ON m.id=c.requester AND m.platform_role='admin' AND m.status='active' WHERE a.id=c.approver AND a.platform_role='admin' AND a.status='active')))
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE' AND a.tenant_id=f.tenant_id AND l.signed_delta=f.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL
  AND a.kind=CASE WHEN f.source_kind='manual_credit' THEN 'FUNDING_CLEARING' ELSE 'OPERATING_GRANT_CLEARING' END AND l.signed_delta=-f.amount)
 AND (SELECT count(*) FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited' AND o.tenant_id=j.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=f.amount::text))
$$;

CREATE OR REPLACE FUNCTION funding_credit_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable funding source'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.approver IS NOT NULL OR NEW.journal_id IS NOT NULL OR NEW.single_operator THEN RAISE EXCEPTION 'credit initial state'; END IF;
  RETURN NEW;
 END IF;
 IF (NEW.id,NEW.tenant_id,NEW.business_ref,NEW.created_at,NEW.requested_by) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.business_ref,OLD.created_at,OLD.requested_by) THEN RAISE EXCEPTION 'immutable funding source'; END IF;
 IF OLD.state='pending' AND NEW.state='approved' THEN
  IF (NEW.amount,NEW.bonus_amount,NEW.basis_kind,NEW.fiat_currency,NEW.fiat_amount,NEW.fx_note,NEW.transfer_ref,NEW.proof_ref,NEW.requester) IS DISTINCT FROM (OLD.amount,OLD.bonus_amount,OLD.basis_kind,OLD.fiat_currency,OLD.fiat_amount,OLD.fx_note,OLD.transfer_ref,OLD.proof_ref,OLD.requester) THEN RAISE EXCEPTION 'immutable funding source'; END IF;
  NEW.single_operator:=wallet_single_operator();
  IF NOT wallet_approval_snapshot_valid(NEW.requester,NEW.approver,NEW.requested_by) THEN RAISE EXCEPTION 'credit approval roles'; END IF;
  RETURN NEW;
 END IF;
 RAISE EXCEPTION 'credit state transition';
END $$;
DROP TRIGGER IF EXISTS funding_credit_guard ON credit_orders;
CREATE TRIGGER funding_credit_guard BEFORE INSERT OR UPDATE OR DELETE ON credit_orders FOR EACH ROW EXECUTE FUNCTION funding_credit_guard();

CREATE FUNCTION wallet_request_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
  IF NEW.kind<>'topup' THEN RAISE EXCEPTION 'request_link_unavailable'; END IF;
  IF NOT EXISTS(SELECT 1 FROM credit_orders c WHERE c.id=NEW.linked_order_id AND c.tenant_id=NEW.tenant_id AND c.requested_by=NEW.requested_by AND c.state='pending' AND wallet_operator_role_valid(c.requester,'finance_operator')) THEN RAISE EXCEPTION 'wallet request link mismatch'; END IF;
  RETURN NEW;
 END IF;
 IF OLD.state='open' AND NEW.state='closed' AND OLD.linked_order_id IS NULL AND NEW.linked_order_id IS NULL THEN RETURN NEW; END IF;
 IF OLD.state='linked' AND NEW.state='closed' AND NEW.linked_order_id=OLD.linked_order_id THEN
  IF NEW.kind='topup' AND EXISTS(SELECT 1 FROM credit_orders c WHERE c.id=NEW.linked_order_id AND c.tenant_id=NEW.tenant_id AND c.requested_by=NEW.requested_by AND c.state='approved' AND wallet_credit_pair_valid(c.id)) THEN RETURN NEW; END IF;
 END IF;
 RAISE EXCEPTION 'wallet request state transition';
END $$;
CREATE TRIGGER wallet_request_guard BEFORE INSERT OR UPDATE OR DELETE ON wallet_requests FOR EACH ROW EXECUTE FUNCTION wallet_request_guard();

CREATE FUNCTION refund_order_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable refund source'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.approver IS NOT NULL OR NEW.journal_id IS NOT NULL OR NEW.single_operator THEN RAISE EXCEPTION 'refund initial state'; END IF;
  RETURN NEW;
 END IF;
 IF (NEW.id,NEW.tenant_id,NEW.settlement_id,NEW.business_ref,NEW.amount,NEW.reason,NEW.requester,NEW.requested_by,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.settlement_id,OLD.business_ref,OLD.amount,OLD.reason,OLD.requester,OLD.requested_by,OLD.created_at) OR OLD.state<>'pending' OR NEW.state<>'approved' THEN RAISE EXCEPTION 'immutable refund source'; END IF;
 NEW.single_operator:=wallet_single_operator();
 IF NOT wallet_approval_snapshot_valid(NEW.requester,NEW.approver,NEW.requested_by) THEN RAISE EXCEPTION 'refund approval roles'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER refund_order_guard BEFORE INSERT OR UPDATE OR DELETE ON refund_orders FOR EACH ROW EXECUTE FUNCTION refund_order_guard();

CREATE OR REPLACE FUNCTION wallet_refund_valid(ref_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM refund_orders r WHERE r.id=ref_ref
 AND ((r.state<>'approved' AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)) OR
  (r.state='approved' AND (SELECT coalesce(sum(a.amount::numeric),0) FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)=r.amount::numeric
   AND (SELECT min(refund_index)=1 AND max(refund_index)=count(*) FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)
   AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id AND (a.settlement_id<>r.settlement_id OR a.tenant_id<>r.tenant_id
     OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.operation_id=r.id AND e.lot_id=a.lot_id AND e.kind='refund' AND wallet_event_valid(e.id))))
   AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a JOIN funding_consumption_refund_lots p ON p.refund_order_id=a.refund_order_id AND p.refund_index<a.refund_index
    WHERE a.refund_order_id=r.id AND p.allocation_index<=a.allocation_index)
   AND wallet_settlement_valid(r.settlement_id)))
 AND (r.state<>'approved' OR (r.approver<>r.requester AND r.approver IS DISTINCT FROM r.requested_by)
  OR (r.single_operator AND r.approver=r.requester AND r.requested_by=r.approver
   AND EXISTS(SELECT 1 FROM users a JOIN users m ON m.id=r.requester AND m.platform_role='admin' AND m.status='active' WHERE a.id=r.approver AND a.platform_role='admin' AND a.status='active'))))
$$;

CREATE OR REPLACE FUNCTION wallet_opening_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id AND f.source_kind='wallet_opening'
 JOIN wallet_openings o ON o.id=f.source_id AND o.tenant_id=f.tenant_id
 WHERE j.id=journal_ref AND f.amount=o.amount AND f.currency='COIN' AND j.currency='COIN'
 AND f.basis_kind='opening' AND f.fiat_currency IS NULL AND f.fiat_amount IS NULL AND f.parent_ref=o.id
 AND j.tenant_id=o.tenant_id AND j.kind='wallet_opening' AND j.operation_id=o.id AND j.reference=o.id AND j.actor=o.actor
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=o.tenant_id AND a.kind='AVAILABLE' AND l.signed_delta=o.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind='WALLET_OPENING_CLEARING' AND l.signed_delta=-o.amount)
 AND (SELECT count(*) FROM projection_outbox x WHERE x.operation_id=j.operation_id AND x.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox x WHERE x.operation_id=j.operation_id AND x.kind='credited' AND x.tenant_id=o.tenant_id
  AND x.schema_version=1 AND jsonb_typeof(x.sanitized_payload->'amount')='string' AND x.sanitized_payload->>'amount'=o.amount::text))
$$;

CREATE OR REPLACE FUNCTION payment_sandbox_journal_valid(journal_ref text) RETURNS boolean LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id AND f.source_kind='sandbox_capture'
 JOIN payment_captures c ON c.id=f.source_id JOIN payment_effects x ON x.capture_id=c.id
 WHERE j.id=journal_ref AND payment_capture_source_valid(c.id)
 AND f.basis_kind='paid' AND f.fiat_currency=c.currency AND f.fiat_amount=c.amount AND f.parent_ref=c.id
 AND EXISTS(SELECT 1 FROM payment_transaction_identities i JOIN payment_evidence_processing p ON p.evidence_id=i.evidence_id
  WHERE i.id=c.identity_id AND p.generation=i.processing_generation AND p.state IN ('processed','processing_review'))
 AND EXISTS(SELECT 1 FROM payment_evidence_processing p WHERE p.evidence_id=c.verified_evidence_id AND p.state='processed' AND p.generation=c.processing_generation)
 AND EXISTS(SELECT 1 FROM audit_events a WHERE a.action='payment.capture.credited' AND a.target=c.id AND a.actor='payment-sandbox-worker' AND a.tenant_scope=c.tenant_id AND a.sanitized_diff=jsonb_build_object('amount',c.wallet_credit_amount::text) AND a.request_id='')
 AND (j.kind,j.operation_id,j.reference,j.actor,j.currency)=('sandbox_capture',c.id,c.order_id,'payment-sandbox-worker','COIN')
 AND (f.tenant_id,f.amount,f.currency,j.tenant_id)=(c.tenant_id,c.wallet_credit_amount,'COIN',c.tenant_id)
 AND (x.identity_id,x.environment_id,x.merchant_id,x.provider_transaction_id,x.effect_kind,x.effect_id)=(c.identity_id,c.environment_id,c.merchant_id,c.provider_transaction_id,'capture','capture')
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=c.tenant_id AND a.kind='AVAILABLE' AND l.signed_delta=c.wallet_credit_amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind='SANDBOX_FUNDING_CLEARING' AND l.signed_delta=-c.wallet_credit_amount)
 AND (SELECT count(*) FROM projection_outbox b WHERE b.operation_id=c.id AND b.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox b WHERE b.operation_id=c.id AND b.kind='credited' AND b.tenant_id=c.tenant_id AND b.schema_version=1 AND b.sanitized_payload=jsonb_build_object('amount',c.wallet_credit_amount::text)))
$$;

DO $$ DECLARE f record; BEGIN
 FOR f IN SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND p.proname IN ('funding_fact_guard','funding_journal_valid','wallet_journal_valid','payment_voucher_journal_valid','payment_funding_journal_valid','wallet_opening_journal_valid','payment_sandbox_journal_valid','wallet_refund_valid','wallet_single_operator','wallet_operator_role_valid','wallet_approval_snapshot_valid','wallet_credit_pair_valid','wallet_request_guard','refund_order_guard') LOOP
  EXECUTE format('ALTER FUNCTION %s SET search_path TO %I,pg_catalog,pg_temp',f.oid::regprocedure::text,current_schema());
  EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC',f.oid::regprocedure::text);
 END LOOP;
END $$;
