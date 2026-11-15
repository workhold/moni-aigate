-- 034.3 refund-held/retired buckets and static guard-only cash-refund facts.
-- No coin-rate relation, application cash-refund writer, or 034.4 state machine.

CREATE TABLE cash_refund_orders (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
 tenant_id text NOT NULL REFERENCES tenants(id),
 lot_id text NOT NULL,
 business_ref text NOT NULL UNIQUE,
 coin_amount bigint,
 reason text NOT NULL,
 state text NOT NULL DEFAULT 'requested' CHECK(state IN ('requested','approved','transfer_executed','completed','cancelled','failed')),
 requested_by text REFERENCES users(id),
 maker text NOT NULL REFERENCES users(id),
 approver text REFERENCES users(id),
 transfer_executor text REFERENCES users(id),
 fiat_currency text,
 fiat_amount bigint,
 buy_rate_version_id text,
 refund_rate_version_id text,
 single_operator boolean NOT NULL DEFAULT false,
 transfer_order_ref text,
 transfer_proof_ref text,
 paid_fiat_amount bigint,
 hold_journal_id text,
 retire_journal_id text,
 release_journal_id text,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,id),
 CHECK(approver IS NULL OR single_operator OR (approver<>maker AND approver IS DISTINCT FROM requested_by)),
 CHECK(transfer_executor IS NULL OR single_operator OR transfer_executor<>approver),
 FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id)
);

ALTER TABLE funding_lot_balances
 ADD COLUMN refund_held_amount bigint NOT NULL DEFAULT 0 CHECK(refund_held_amount>=0),
 ADD COLUMN retired_amount bigint NOT NULL DEFAULT 0 CHECK(retired_amount>=0);

ALTER TABLE funding_lot_events
 ADD COLUMN delta_refund_held bigint NOT NULL DEFAULT 0,
 ADD COLUMN delta_retired bigint NOT NULL DEFAULT 0,
 ADD COLUMN refund_order_id text;
ALTER TABLE funding_lot_events
 DROP CONSTRAINT funding_lot_events_kind_check,
 ADD CONSTRAINT funding_lot_events_kind_check CHECK(kind IN ('funding','reserve','finish','refund','refund_hold','refund_retire','refund_release')),
 ADD CONSTRAINT funding_lot_events_refund_order_check CHECK((kind IN ('refund_hold','refund_retire','refund_release'))=(refund_order_id IS NOT NULL)),
 ADD CONSTRAINT funding_lot_events_refund_order_fk FOREIGN KEY(tenant_id,refund_order_id) REFERENCES cash_refund_orders(tenant_id,id),
 ADD CONSTRAINT funding_lot_events_refund_order_kind_key UNIQUE(refund_order_id,kind);

ALTER TABLE ledger_accounts
 DROP CONSTRAINT ledger_accounts_check,
 DROP CONSTRAINT ledger_accounts_kind_check,
 ADD CONSTRAINT ledger_accounts_check CHECK(((tenant_id IS NOT NULL AND kind IN ('AVAILABLE','HELD','REFUND_HELD')) AND balance>=0) OR (tenant_id IS NULL AND kind IN ('FUNDING_CLEARING','REVENUE')) OR (tenant_id IS NULL AND kind IN ('OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING','WALLET_OPENING_CLEARING') AND balance<=0)),
 ADD CONSTRAINT ledger_accounts_kind_check CHECK(kind IN ('AVAILABLE','HELD','REFUND_HELD','FUNDING_CLEARING','REVENUE','OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING','WALLET_OPENING_CLEARING'));

CREATE OR REPLACE FUNCTION wallet_lot_balance_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE principal BIGINT;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'wallet balances cannot be deleted'; END IF;
 SELECT issued_amount INTO STRICT principal FROM funding_lots WHERE id=NEW.lot_id AND tenant_id=NEW.tenant_id;
 IF NEW.available_amount::numeric+NEW.held_amount::numeric+NEW.consumed_amount::numeric+NEW.refund_held_amount::numeric+NEW.retired_amount::numeric<>principal::numeric THEN RAISE EXCEPTION 'wallet lot principal mismatch'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.revision<>1 OR NEW.available_amount<>principal OR NEW.held_amount<>0 OR NEW.consumed_amount<>0 OR NEW.refund_held_amount<>0 OR NEW.retired_amount<>0 THEN RAISE EXCEPTION 'invalid wallet genesis'; END IF;
 ELSE
  IF NEW.lot_id<>OLD.lot_id OR NEW.tenant_id<>OLD.tenant_id OR NEW.revision::numeric<>OLD.revision::numeric+1
   OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.lot_id=NEW.lot_id AND e.tenant_id=NEW.tenant_id AND e.revision=NEW.revision AND e.created_xid=pg_current_xact_id()
    AND e.delta_available::numeric=NEW.available_amount::numeric-OLD.available_amount::numeric
    AND e.delta_held::numeric=NEW.held_amount::numeric-OLD.held_amount::numeric
    AND e.delta_consumed::numeric=NEW.consumed_amount::numeric-OLD.consumed_amount::numeric
    AND e.delta_refund_held::numeric=NEW.refund_held_amount::numeric-OLD.refund_held_amount::numeric
    AND e.delta_retired::numeric=NEW.retired_amount::numeric-OLD.retired_amount::numeric) THEN RAISE EXCEPTION 'wallet update requires matching event'; END IF;
 END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION wallet_event_valid(event_ref text) RETURNS boolean LANGUAGE plpgsql STABLE SECURITY INVOKER AS $$
DECLARE event_kind TEXT;
BEGIN
 SELECT kind INTO event_kind FROM funding_lot_events WHERE id=event_ref;
 CASE event_kind
 WHEN 'funding' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_lots l ON l.id=e.lot_id AND l.tenant_id=e.tenant_id JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id WHERE e.id=event_ref AND e.revision=1 AND f.journal_id=j.id AND e.delta_available=l.issued_amount AND e.delta_held=0 AND e.delta_consumed=0 AND e.delta_refund_held=0 AND e.delta_retired=0 AND funding_journal_valid(j.id));
 WHEN 'reserve' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_reservation_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN reservations r ON r.id=a.reservation_id AND r.tenant_id=a.tenant_id WHERE e.id=event_ref AND j.operation_id=r.id AND j.kind='reserve' AND j.reference=r.request_id AND a.request_id=r.request_id AND e.delta_available=-a.amount AND e.delta_held=a.amount AND e.delta_consumed=0 AND e.delta_refund_held=0 AND e.delta_retired=0)
   OR EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN media_reservation_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN media_reservations r ON r.id=a.reservation_id AND r.tenant_id=a.tenant_id WHERE e.id=event_ref AND j.operation_id=r.id AND j.kind='media.reserve' AND j.reference=r.request_id AND e.delta_available=-a.amount AND e.delta_held=a.amount AND e.delta_consumed=0 AND e.delta_refund_held=0 AND e.delta_retired=0);
 WHEN 'finish' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_settlement_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN settlements s ON s.id=a.settlement_id AND s.tenant_id=a.tenant_id WHERE e.id=event_ref AND s.journal_id=j.id AND j.operation_id=s.id AND j.reference=s.request_id AND j.kind=s.status AND e.delta_available=a.released_amount AND e.delta_held::numeric=-(a.charged_amount::numeric+a.released_amount::numeric) AND e.delta_consumed=a.charged_amount AND e.delta_refund_held=0 AND e.delta_retired=0)
   OR EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN media_settlement_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN media_settlements s ON s.id=a.settlement_id AND s.tenant_id=a.tenant_id WHERE e.id=event_ref AND s.journal_id=j.id AND j.operation_id=s.id AND j.kind='media.settle' AND j.reference=s.request_id AND e.delta_available=a.released_amount AND e.delta_held::numeric=-(a.charged_amount::numeric+a.released_amount::numeric) AND e.delta_consumed=a.charged_amount AND e.delta_refund_held=0 AND e.delta_retired=0);
 WHEN 'refund' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN funding_consumption_refund_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN refund_orders r ON r.id=a.refund_order_id AND r.tenant_id=a.tenant_id WHERE e.id=event_ref AND r.state='approved' AND r.journal_id=j.id AND j.operation_id=r.id AND j.kind='refund' AND j.reference=r.settlement_id AND e.delta_available=a.amount AND e.delta_held=0 AND e.delta_consumed=-a.amount AND e.delta_refund_held=0 AND e.delta_retired=0);
 WHEN 'refund_hold' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN cash_refund_orders r ON r.id=e.refund_order_id AND r.tenant_id=e.tenant_id JOIN funding_lots l ON l.id=e.lot_id AND l.tenant_id=e.tenant_id AND l.refund_class='cash_original' JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id AND f.basis_kind='paid' WHERE e.id=event_ref AND j.kind='cash_refund_hold' AND j.reference=r.id AND funding_uuid(j.operation_id) AND j.operation_id<>r.id AND j.operation_id<>r.business_ref AND r.hold_journal_id=j.id AND r.state IN ('approved','transfer_executed','completed','cancelled','failed') AND r.lot_id=e.lot_id AND r.coin_amount>0 AND e.revision>1 AND e.delta_available=-r.coin_amount AND e.delta_held=0 AND e.delta_consumed=0 AND e.delta_refund_held=r.coin_amount AND e.delta_retired=0);
 WHEN 'refund_retire' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN cash_refund_orders r ON r.id=e.refund_order_id AND r.tenant_id=e.tenant_id JOIN funding_lots l ON l.id=e.lot_id AND l.tenant_id=e.tenant_id AND l.refund_class='cash_original' JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id AND f.basis_kind='paid' WHERE e.id=event_ref AND j.kind='cash_refund_retire' AND j.reference=r.id AND funding_uuid(j.operation_id) AND j.operation_id<>r.id AND j.operation_id<>r.business_ref AND r.retire_journal_id=j.id AND r.state='completed' AND r.lot_id=e.lot_id AND r.coin_amount>0 AND e.revision>1 AND e.delta_available=0 AND e.delta_held=0 AND e.delta_consumed=0 AND e.delta_refund_held=-r.coin_amount AND e.delta_retired=r.coin_amount AND EXISTS(SELECT 1 FROM funding_lot_events h WHERE h.refund_order_id=r.id AND h.lot_id=e.lot_id AND h.kind='refund_hold' AND h.revision<e.revision AND h.delta_available=-r.coin_amount AND h.delta_held=0 AND h.delta_consumed=0 AND h.delta_refund_held=r.coin_amount AND h.delta_retired=0));
 WHEN 'refund_release' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id JOIN cash_refund_orders r ON r.id=e.refund_order_id AND r.tenant_id=e.tenant_id JOIN funding_lots l ON l.id=e.lot_id AND l.tenant_id=e.tenant_id AND l.refund_class='cash_original' JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id AND f.basis_kind='paid' WHERE e.id=event_ref AND j.kind='cash_refund_release' AND j.reference=r.id AND funding_uuid(j.operation_id) AND j.operation_id<>r.id AND j.operation_id<>r.business_ref AND r.release_journal_id=j.id AND r.state IN ('cancelled','failed') AND r.lot_id=e.lot_id AND r.coin_amount>0 AND e.revision>1 AND e.delta_available=r.coin_amount AND e.delta_held=0 AND e.delta_consumed=0 AND e.delta_refund_held=-r.coin_amount AND e.delta_retired=0 AND EXISTS(SELECT 1 FROM funding_lot_events h WHERE h.refund_order_id=r.id AND h.lot_id=e.lot_id AND h.kind='refund_hold' AND h.revision<e.revision AND h.delta_available=-r.coin_amount AND h.delta_held=0 AND h.delta_consumed=0 AND h.delta_refund_held=r.coin_amount AND h.delta_retired=0));
 ELSE RETURN false;
 END CASE;
END $$;

CREATE OR REPLACE FUNCTION wallet_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM journals j WHERE j.id=journal_ref
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE')
   =(SELECT coalesce(sum(e.delta_available::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='HELD')
   =(SELECT coalesce(sum(e.delta_held::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='REVENUE')
   =(SELECT coalesce(sum(e.delta_consumed::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='REFUND_HELD')
   =(SELECT coalesce(sum(e.delta_refund_held::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (j.kind<>'cash_refund_retire' OR (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='FUNDING_CLEARING')
   =(SELECT coalesce(sum(e.delta_retired::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id))
 AND NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.journal_id=j.id AND NOT wallet_event_valid(e.id)))
$$;

CREATE OR REPLACE FUNCTION wallet_lot_valid(lot_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM funding_lots l JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id
 JOIN funding_lot_balances b ON b.lot_id=l.id AND b.tenant_id=l.tenant_id
 WHERE l.id=lot_ref AND (l.source_kind,l.source_id,l.issued_amount,l.fifo_at)=(f.source_kind,f.source_id,f.amount,f.created_at)
 AND funding_journal_valid(f.journal_id)
 AND (SELECT count(*) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.revision
 AND (SELECT min(revision) FROM funding_lot_events e WHERE e.lot_id=l.id)=1
 AND (SELECT max(revision) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.revision
 AND (SELECT coalesce(sum(delta_available::numeric),0) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.available_amount::numeric
 AND (SELECT coalesce(sum(delta_held::numeric),0) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.held_amount::numeric
 AND (SELECT coalesce(sum(delta_consumed::numeric),0) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.consumed_amount::numeric
 AND (SELECT coalesce(sum(delta_refund_held::numeric),0) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.refund_held_amount::numeric
 AND (SELECT coalesce(sum(delta_retired::numeric),0) FROM funding_lot_events e WHERE e.lot_id=l.id)=b.retired_amount::numeric
 AND EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.lot_id=l.id AND e.revision=1 AND e.kind='funding' AND e.journal_id=f.journal_id
  AND e.delta_available=l.issued_amount AND e.delta_held=0 AND e.delta_consumed=0 AND e.delta_refund_held=0 AND e.delta_retired=0))
$$;

CREATE OR REPLACE FUNCTION wallet_account_valid(account_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM ledger_accounts a WHERE a.id=account_ref
 AND a.balance::numeric=(SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l WHERE l.account_id=a.id)
 AND (a.tenant_id IS NULL OR (a.kind='AVAILABLE' AND a.balance::numeric=(SELECT coalesce(sum(b.available_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=a.tenant_id))
  OR (a.kind='HELD' AND a.balance::numeric=(SELECT coalesce(sum(b.held_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=a.tenant_id))
  OR (a.kind='REFUND_HELD' AND a.balance::numeric=(SELECT coalesce(sum(b.refund_held_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=a.tenant_id))))
$$;

CREATE FUNCTION wallet_cash_refund_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN cash_refund_orders r ON r.id=j.reference AND r.tenant_id=j.tenant_id
 WHERE j.id=journal_ref AND j.currency='COIN' AND funding_uuid(j.operation_id) AND j.operation_id<>r.id AND j.operation_id<>r.business_ref AND r.coin_amount>0
 AND ((j.kind='cash_refund_hold' AND r.state IN ('approved','transfer_executed','completed','cancelled','failed') AND r.hold_journal_id=j.id)
  OR (j.kind='cash_refund_retire' AND r.state='completed' AND r.retire_journal_id=j.id)
  OR (j.kind='cash_refund_release' AND r.state IN ('cancelled','failed') AND r.release_journal_id=j.id))
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

CREATE FUNCTION wallet_cash_refund_order_valid(order_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT EXISTS(SELECT 1 FROM cash_refund_orders r WHERE r.id=order_ref
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

CREATE OR REPLACE FUNCTION funding_journal_valid(journal_ref text) RETURNS boolean LANGUAGE plpgsql STABLE SECURITY INVOKER AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM journals WHERE id=journal_ref AND kind IN ('cash_refund_hold','cash_refund_retire','cash_refund_release')) THEN RETURN wallet_cash_refund_journal_valid(journal_ref); END IF;
 IF EXISTS(SELECT 1 FROM journals WHERE id=journal_ref AND kind='sandbox_capture') THEN RETURN payment_sandbox_journal_valid(journal_ref); END IF;
 IF EXISTS(SELECT 1 FROM journals WHERE id=journal_ref AND kind='wallet_opening') THEN RETURN wallet_opening_journal_valid(journal_ref); END IF;
 IF EXISTS(SELECT 1 FROM journals WHERE id=journal_ref AND kind='voucher_grant') THEN RETURN payment_voucher_journal_valid(journal_ref); END IF;
 RETURN payment_funding_journal_valid(journal_ref);
END
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
  IF NEW.kind NOT IN ('credit','credit_bonus','voucher_grant','sandbox_capture','cash_refund_retire') THEN
   IF EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=NEW.id AND a.kind IN ('FUNDING_CLEARING','OPERATING_GRANT_CLEARING')) THEN RAISE EXCEPTION 'funding clearing requires source';END IF;
   RETURN NULL;
  END IF;jid:=NEW.id;
 ELSIF TG_TABLE_NAME='projection_outbox' THEN IF NEW.kind<>'credited' THEN RETURN NULL; END IF;SELECT id INTO jid FROM journals WHERE operation_id=NEW.operation_id;
 ELSE jid:=NEW.journal_id; END IF;
 IF jid IS NULL OR NOT funding_journal_valid(jid) THEN RAISE EXCEPTION 'funding source integrity failed'; END IF;
 RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION wallet_source_fact_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ok BOOLEAN; target_lot TEXT;
BEGIN
 CASE TG_TABLE_NAME
 WHEN 'wallet_openings' THEN SELECT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='wallet_opening' AND f.source_id=NEW.id AND wallet_opening_journal_valid(f.journal_id)) INTO ok;
 WHEN 'funding_receipts' THEN SELECT wallet_lot_valid(NEW.id) INTO ok;
 WHEN 'funding_lots' THEN SELECT wallet_lot_valid(NEW.id) INTO ok;
 WHEN 'funding_lot_balances' THEN SELECT wallet_lot_valid(NEW.lot_id) INTO ok;
 WHEN 'funding_lot_events' THEN SELECT wallet_event_valid(NEW.id) AND wallet_lot_valid(NEW.lot_id) INTO ok;
 WHEN 'cash_refund_orders' THEN SELECT wallet_cash_refund_order_valid(NEW.id) INTO ok;
 WHEN 'reservations' THEN SELECT wallet_reservation_valid(NEW.id) INTO ok;
 WHEN 'funding_reservation_lots' THEN SELECT wallet_reservation_valid(NEW.reservation_id) INTO ok;
 WHEN 'settlements' THEN SELECT wallet_settlement_valid(NEW.id) INTO ok;
 WHEN 'funding_settlement_lots' THEN SELECT wallet_settlement_valid(NEW.settlement_id) INTO ok;
 WHEN 'refund_orders' THEN SELECT wallet_refund_valid(NEW.id) INTO ok;
 WHEN 'funding_consumption_refund_lots' THEN SELECT wallet_refund_valid(NEW.refund_order_id) INTO ok;
 WHEN 'journals' THEN SELECT wallet_journal_valid(NEW.id) INTO ok;
 WHEN 'ledger_accounts' THEN SELECT wallet_account_valid(NEW.id) INTO ok;
 ELSE ok:=false;
 END CASE;
 IF ok IS DISTINCT FROM true THEN RAISE EXCEPTION 'wallet source facts do not match'; END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER cash_refund_order_fact AFTER INSERT OR UPDATE ON cash_refund_orders DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard();
CREATE FUNCTION cash_refund_order_identity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR (NEW.id,NEW.tenant_id,NEW.lot_id,NEW.business_ref,NEW.requested_by,NEW.maker,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.lot_id,OLD.business_ref,OLD.requested_by,OLD.maker,OLD.created_at) THEN RAISE EXCEPTION 'immutable cash refund identity'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER cash_refund_order_identity BEFORE UPDATE OR DELETE ON cash_refund_orders FOR EACH ROW EXECUTE FUNCTION cash_refund_order_identity_guard();

DO $$ DECLARE f record; BEGIN
 FOR f IN SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND p.proname IN ('wallet_lot_balance_guard','wallet_event_valid','wallet_journal_valid','wallet_lot_valid','wallet_account_valid','wallet_cash_refund_journal_valid','wallet_cash_refund_order_valid','wallet_source_fact_guard','cash_refund_order_identity_guard','funding_journal_valid','funding_fact_guard') LOOP
  EXECUTE format('ALTER FUNCTION %s SECURITY INVOKER',f.oid::regprocedure::text);
  EXECUTE format('ALTER FUNCTION %s SET search_path TO %I,pg_catalog,pg_temp',f.oid::regprocedure::text,current_schema());
  EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC',f.oid::regprocedure::text);
 END LOOP;
END $$;
