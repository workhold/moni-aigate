-- Pre-MVP single current wallet model. Existing financial history is never
-- guessed, erased, or silently imported; use a separate fresh current database.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM journals) OR EXISTS(SELECT 1 FROM funding_receipts)
 OR EXISTS(SELECT 1 FROM reservations) OR EXISTS(SELECT 1 FROM settlements)
 OR EXISTS(SELECT 1 FROM refund_orders) OR EXISTS(SELECT 1 FROM credit_orders)
 OR EXISTS(SELECT 1 FROM ledger_accounts WHERE balance<>0)
 OR EXISTS(SELECT 1 FROM api_keys WHERE held<>0 OR spent<>0) THEN
  RAISE EXCEPTION '023 requires a fresh wallet history; preserve the old database and initialize a separate current database';
 END IF;
END $$;

ALTER TABLE journals ADD UNIQUE(tenant_id,id);
ALTER TABLE settlements ADD UNIQUE(tenant_id,id);
ALTER TABLE refund_orders ADD UNIQUE(tenant_id,id);
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_kind_check;
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check;
ALTER TABLE ledger_accounts ADD CHECK(kind IN ('AVAILABLE','HELD','FUNDING_CLEARING','REVENUE','OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING','WALLET_OPENING_CLEARING'));
ALTER TABLE ledger_accounts ADD CHECK((tenant_id IS NOT NULL AND kind IN ('AVAILABLE','HELD') AND balance>=0) OR
 (tenant_id IS NULL AND kind IN ('FUNDING_CLEARING','REVENUE')) OR
 (tenant_id IS NULL AND kind IN ('OPERATING_GRANT_CLEARING','SANDBOX_FUNDING_CLEARING','WALLET_OPENING_CLEARING') AND balance<=0));
ALTER TABLE funding_receipts DROP CONSTRAINT funding_receipts_source_kind_check;
ALTER TABLE funding_receipts ADD CHECK(source_kind IN ('manual_credit','voucher_grant','sandbox_capture','wallet_opening'));

CREATE TABLE wallet_openings (
 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL UNIQUE REFERENCES tenants(id),
 external_ref TEXT NOT NULL UNIQUE CHECK(length(external_ref) BETWEEN 1 AND 160),
 amount BIGINT NOT NULL CHECK(amount>0), actor TEXT NOT NULL REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(), UNIQUE(tenant_id,id)
);
CREATE TRIGGER immutable_wallet_opening BEFORE UPDATE OR DELETE ON wallet_openings FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();

CREATE TABLE funding_lots (
 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL REFERENCES tenants(id),
 funding_receipt_id TEXT NOT NULL UNIQUE,
 source_kind TEXT NOT NULL CHECK(source_kind IN ('manual_credit','voucher_grant','sandbox_capture','wallet_opening')),
 source_id TEXT NOT NULL, refund_class TEXT NOT NULL CHECK(refund_class IN ('nonrefundable','sandbox_original')),
 issued_amount BIGINT NOT NULL CHECK(issued_amount>0), fifo_at TIMESTAMPTZ NOT NULL,
 CHECK(id=funding_receipt_id), CHECK((source_kind='sandbox_capture')=(refund_class='sandbox_original')),
 UNIQUE(tenant_id,id), UNIQUE(source_kind,source_id),
 FOREIGN KEY(tenant_id,funding_receipt_id) REFERENCES funding_receipts(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_lot_balances (
 lot_id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL,
 available_amount BIGINT NOT NULL CHECK(available_amount>=0), held_amount BIGINT NOT NULL CHECK(held_amount>=0),
 consumed_amount BIGINT NOT NULL CHECK(consumed_amount>=0), revision BIGINT NOT NULL CHECK(revision>0),
 FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_lot_events (
 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, lot_id TEXT NOT NULL,
 operation_id TEXT NOT NULL, journal_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('funding','reserve','finish','refund')), revision BIGINT NOT NULL CHECK(revision>0),
 delta_available BIGINT NOT NULL, delta_held BIGINT NOT NULL, delta_consumed BIGINT NOT NULL,
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(), created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(lot_id,revision), UNIQUE(operation_id,lot_id),
 FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_reservation_lots (
 tenant_id TEXT NOT NULL, reservation_id TEXT NOT NULL, request_id TEXT NOT NULL, lot_id TEXT NOT NULL,
 allocation_index BIGINT NOT NULL CHECK(allocation_index>0), amount BIGINT NOT NULL CHECK(amount>0),
 PRIMARY KEY(reservation_id,allocation_index), UNIQUE(reservation_id,lot_id),
 UNIQUE(tenant_id,reservation_id,allocation_index,lot_id),
 FOREIGN KEY(tenant_id,reservation_id) REFERENCES reservations(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_settlement_lots (
 tenant_id TEXT NOT NULL, settlement_id TEXT NOT NULL, reservation_id TEXT NOT NULL,
 allocation_index BIGINT NOT NULL, lot_id TEXT NOT NULL,
 charged_amount BIGINT NOT NULL CHECK(charged_amount>=0), released_amount BIGINT NOT NULL CHECK(released_amount>=0),
 PRIMARY KEY(settlement_id,allocation_index), UNIQUE(tenant_id,settlement_id,allocation_index,lot_id),
 FOREIGN KEY(tenant_id,settlement_id) REFERENCES settlements(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,reservation_id,allocation_index,lot_id) REFERENCES funding_reservation_lots(tenant_id,reservation_id,allocation_index,lot_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_consumption_refund_lots (
 tenant_id TEXT NOT NULL, refund_order_id TEXT NOT NULL, settlement_id TEXT NOT NULL,
 allocation_index BIGINT NOT NULL, lot_id TEXT NOT NULL, refund_index BIGINT NOT NULL CHECK(refund_index>0), amount BIGINT NOT NULL CHECK(amount>0),
 PRIMARY KEY(refund_order_id,refund_index), UNIQUE(refund_order_id,allocation_index),
 FOREIGN KEY(tenant_id,refund_order_id) REFERENCES refund_orders(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,settlement_id,allocation_index,lot_id) REFERENCES funding_settlement_lots(tenant_id,settlement_id,allocation_index,lot_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX wallet_lots_fifo ON funding_lots(tenant_id,fifo_at,id COLLATE "C");
CREATE INDEX wallet_lot_events_journal ON funding_lot_events(journal_id);
CREATE INDEX wallet_lot_refunds_settlement ON funding_consumption_refund_lots(settlement_id,allocation_index);

CREATE FUNCTION wallet_opening_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id AND f.source_kind='wallet_opening'
 JOIN wallet_openings o ON o.id=f.source_id AND o.tenant_id=f.tenant_id
 WHERE j.id=journal_ref AND f.amount=o.amount AND f.currency='CNY' AND j.currency='CNY'
 AND j.tenant_id=o.tenant_id AND j.kind='wallet_opening' AND j.operation_id=o.id AND j.reference=o.id AND j.actor=o.actor
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=o.tenant_id AND a.kind='AVAILABLE' AND l.signed_delta=o.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind='WALLET_OPENING_CLEARING' AND l.signed_delta=-o.amount)
 AND (SELECT count(*) FROM projection_outbox x WHERE x.operation_id=j.operation_id AND x.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox x WHERE x.operation_id=j.operation_id AND x.kind='credited' AND x.tenant_id=o.tenant_id
  AND x.schema_version=1 AND jsonb_typeof(x.sanitized_payload->'amount')='string' AND x.sanitized_payload->>'amount'=o.amount::text))
$$;
CREATE OR REPLACE FUNCTION funding_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE plpgsql STABLE AS $$
DECLARE journal_kind TEXT;
BEGIN
 SELECT kind INTO journal_kind FROM journals WHERE id=journal_ref;
 IF journal_kind='sandbox_capture' THEN RETURN payment_sandbox_journal_valid(journal_ref);
 ELSIF journal_kind='wallet_opening' THEN RETURN wallet_opening_journal_valid(journal_ref);
 ELSE RETURN payment_legacy_funding_journal_valid(journal_ref); END IF;
END $$;

CREATE FUNCTION wallet_lot_balance_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE principal BIGINT;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'wallet balances cannot be deleted'; END IF;
 SELECT issued_amount INTO STRICT principal FROM funding_lots WHERE id=NEW.lot_id AND tenant_id=NEW.tenant_id;
 IF NEW.available_amount::numeric+NEW.held_amount::numeric+NEW.consumed_amount::numeric<>principal::numeric THEN RAISE EXCEPTION 'wallet lot principal mismatch'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.revision<>1 OR NEW.available_amount<>principal OR NEW.held_amount<>0 OR NEW.consumed_amount<>0 THEN RAISE EXCEPTION 'invalid wallet genesis'; END IF;
 ELSE
  IF NEW.lot_id<>OLD.lot_id OR NEW.tenant_id<>OLD.tenant_id OR NEW.revision::numeric<>OLD.revision::numeric+1
   OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.lot_id=NEW.lot_id AND e.tenant_id=NEW.tenant_id AND e.revision=NEW.revision AND e.created_xid=pg_current_xact_id()
    AND e.delta_available::numeric=NEW.available_amount::numeric-OLD.available_amount::numeric
    AND e.delta_held::numeric=NEW.held_amount::numeric-OLD.held_amount::numeric
    AND e.delta_consumed::numeric=NEW.consumed_amount::numeric-OLD.consumed_amount::numeric) THEN RAISE EXCEPTION 'wallet update requires matching event'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER wallet_balance_write BEFORE INSERT OR UPDATE OR DELETE ON funding_lot_balances FOR EACH ROW EXECUTE FUNCTION wallet_lot_balance_guard();
CREATE FUNCTION wallet_lot_event_stamp() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM journals j WHERE j.id=NEW.journal_id AND j.tenant_id=NEW.tenant_id AND j.operation_id=NEW.operation_id AND j.created_txid=txid_current()) THEN RAISE EXCEPTION 'wallet event requires current journal'; END IF;
 NEW.created_xid:=pg_current_xact_id(); RETURN NEW;
END $$;
CREATE TRIGGER wallet_event_stamp BEFORE INSERT ON funding_lot_events FOR EACH ROW EXECUTE FUNCTION wallet_lot_event_stamp();

CREATE FUNCTION wallet_lot_valid(lot_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
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
 AND EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.lot_id=l.id AND e.revision=1 AND e.kind='funding' AND e.journal_id=f.journal_id
  AND e.delta_available=l.issued_amount AND e.delta_held=0 AND e.delta_consumed=0))
$$;
-- Statement dispatch preserves the payment worker's narrow funding-only reads.
CREATE FUNCTION wallet_event_valid(event_ref TEXT) RETURNS BOOLEAN LANGUAGE plpgsql STABLE AS $$
DECLARE event_kind TEXT;
BEGIN
 SELECT kind INTO event_kind FROM funding_lot_events WHERE id=event_ref;
 CASE event_kind
 WHEN 'funding' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id
  JOIN funding_lots l ON l.id=e.lot_id AND l.tenant_id=e.tenant_id JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id
  WHERE e.id=event_ref AND e.revision=1 AND f.journal_id=j.id AND e.delta_available=l.issued_amount AND e.delta_held=0 AND e.delta_consumed=0 AND funding_journal_valid(j.id));
 WHEN 'reserve' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id
  JOIN funding_reservation_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN reservations r ON r.id=a.reservation_id AND r.tenant_id=a.tenant_id
  WHERE e.id=event_ref AND j.operation_id=r.id AND j.kind='reserve' AND j.reference=r.request_id AND a.request_id=r.request_id AND e.delta_available=-a.amount AND e.delta_held=a.amount AND e.delta_consumed=0);
 WHEN 'finish' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id
  JOIN funding_settlement_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN settlements s ON s.id=a.settlement_id AND s.tenant_id=a.tenant_id
  WHERE e.id=event_ref AND s.journal_id=j.id AND j.operation_id=s.id AND j.reference=s.request_id AND j.kind=s.status
  AND e.delta_available=a.released_amount AND e.delta_held::numeric=-(a.charged_amount::numeric+a.released_amount::numeric) AND e.delta_consumed=a.charged_amount);
 WHEN 'refund' THEN RETURN EXISTS(SELECT 1 FROM funding_lot_events e JOIN journals j ON j.id=e.journal_id AND j.tenant_id=e.tenant_id AND j.operation_id=e.operation_id
  JOIN funding_consumption_refund_lots a ON a.lot_id=e.lot_id AND a.tenant_id=e.tenant_id JOIN refund_orders r ON r.id=a.refund_order_id AND r.tenant_id=a.tenant_id
  WHERE e.id=event_ref AND r.state='approved' AND r.journal_id=j.id AND j.operation_id=r.id AND j.kind='refund' AND j.reference=r.settlement_id
  AND e.delta_available=a.amount AND e.delta_held=0 AND e.delta_consumed=-a.amount);
 ELSE RETURN false;
 END CASE;
END $$;

CREATE FUNCTION wallet_reservation_fifo_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_lot TEXT; available BIGINT; remaining NUMERIC; allocated NUMERIC; edge_count BIGINT;
BEGIN
 -- Every customer money mutation serializes on these existing account rows.
 PERFORM id FROM ledger_accounts WHERE tenant_id=NEW.tenant_id AND kind='AVAILABLE' FOR UPDATE;
 SELECT coalesce(sum(amount::numeric),0),count(*) INTO allocated,edge_count FROM funding_reservation_lots WHERE reservation_id=NEW.reservation_id;
 SELECT r.amount::numeric-allocated INTO STRICT remaining FROM reservations r
  WHERE r.id=NEW.reservation_id AND r.tenant_id=NEW.tenant_id AND r.request_id=NEW.request_id AND r.status='reserved';
 SELECT l.id,b.available_amount INTO next_lot,available FROM funding_lots l JOIN funding_lot_balances b ON b.lot_id=l.id AND b.tenant_id=l.tenant_id
  WHERE l.tenant_id=NEW.tenant_id AND b.available_amount>0 ORDER BY l.fifo_at,l.id COLLATE "C" LIMIT 1;
 IF NEW.allocation_index<>edge_count+1 OR NEW.lot_id IS DISTINCT FROM next_lot OR remaining<=0
  OR NEW.amount::numeric<>least(available::numeric,remaining) THEN RAISE EXCEPTION 'wallet reservation requires FIFO source allocation'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER wallet_reservation_fifo BEFORE INSERT ON funding_reservation_lots FOR EACH ROW EXECUTE FUNCTION wallet_reservation_fifo_guard();

CREATE FUNCTION wallet_reservation_valid(res_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM reservations r WHERE r.id=res_ref
 AND (SELECT coalesce(sum(a.amount::numeric),0) FROM funding_reservation_lots a WHERE a.reservation_id=r.id)=r.amount::numeric
 AND (r.amount=0 OR (SELECT min(allocation_index)=1 AND max(allocation_index)=count(*) FROM funding_reservation_lots a WHERE a.reservation_id=r.id))
 AND NOT EXISTS(SELECT 1 FROM funding_reservation_lots a WHERE a.reservation_id=r.id AND (a.tenant_id<>r.tenant_id OR a.request_id<>r.request_id OR NOT EXISTS(
  SELECT 1 FROM funding_lot_events e WHERE e.lot_id=a.lot_id AND e.operation_id=r.id AND e.kind='reserve' AND wallet_event_valid(e.id))))
 AND EXISTS(SELECT 1 FROM journals j WHERE j.operation_id=r.id AND j.tenant_id=r.tenant_id AND j.kind='reserve' AND j.reference=r.request_id))
$$;
CREATE FUNCTION wallet_settlement_valid(set_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM settlements s JOIN reservations r ON r.request_id=s.request_id AND r.tenant_id=s.tenant_id WHERE s.id=set_ref
 AND (SELECT count(*) FROM funding_settlement_lots a WHERE a.settlement_id=s.id)=(SELECT count(*) FROM funding_reservation_lots a WHERE a.reservation_id=r.id)
 AND (SELECT coalesce(sum(charged_amount::numeric),0) FROM funding_settlement_lots a WHERE a.settlement_id=s.id)=s.charged_amount::numeric
 AND (SELECT coalesce(sum(released_amount::numeric),0) FROM funding_settlement_lots a WHERE a.settlement_id=s.id)=r.amount::numeric-s.charged_amount::numeric
 AND NOT EXISTS(SELECT 1 FROM funding_settlement_lots f JOIN funding_reservation_lots a ON a.reservation_id=f.reservation_id AND a.allocation_index=f.allocation_index
  WHERE f.settlement_id=s.id AND (f.reservation_id<>r.id OR f.tenant_id<>s.tenant_id OR f.lot_id<>a.lot_id
   OR f.charged_amount::numeric+f.released_amount::numeric<>a.amount::numeric
   OR f.charged_amount::numeric<>least(a.amount::numeric,greatest(0::numeric,s.charged_amount::numeric-(SELECT coalesce(sum(p.amount::numeric),0) FROM funding_reservation_lots p WHERE p.reservation_id=r.id AND p.allocation_index<a.allocation_index)))
   OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.operation_id=s.id AND e.lot_id=f.lot_id AND e.kind='finish' AND wallet_event_valid(e.id))))
 AND (SELECT coalesce(sum(amount::numeric),0) FROM funding_consumption_refund_lots x WHERE x.settlement_id=s.id)=s.refunded_amount::numeric
 AND NOT EXISTS(SELECT 1 FROM funding_settlement_lots a WHERE a.settlement_id=s.id
  AND (SELECT coalesce(sum(x.amount::numeric),0) FROM funding_consumption_refund_lots x WHERE x.settlement_id=s.id AND x.allocation_index=a.allocation_index)
   <>least(a.charged_amount::numeric,greatest(0::numeric,s.refunded_amount::numeric-(SELECT coalesce(sum(p.charged_amount::numeric),0) FROM funding_settlement_lots p WHERE p.settlement_id=s.id AND p.allocation_index>a.allocation_index)))))
$$;
CREATE FUNCTION wallet_refund_valid(ref_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM refund_orders r WHERE r.id=ref_ref AND
  ((r.state<>'approved' AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)) OR
   (r.state='approved' AND (SELECT coalesce(sum(a.amount::numeric),0) FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)=r.amount::numeric
    AND (SELECT min(refund_index)=1 AND max(refund_index)=count(*) FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id)
    AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a WHERE a.refund_order_id=r.id AND (a.settlement_id<>r.settlement_id OR a.tenant_id<>r.tenant_id
      OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.operation_id=r.id AND e.lot_id=a.lot_id AND e.kind='refund' AND wallet_event_valid(e.id))))
    AND NOT EXISTS(SELECT 1 FROM funding_consumption_refund_lots a JOIN funding_consumption_refund_lots p ON p.refund_order_id=a.refund_order_id AND p.refund_index<a.refund_index
     WHERE a.refund_order_id=r.id AND p.allocation_index<=a.allocation_index)
    AND wallet_settlement_valid(r.settlement_id))))
$$;
CREATE FUNCTION wallet_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM journals j WHERE j.id=journal_ref
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE')
   =(SELECT coalesce(sum(e.delta_available::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='HELD')
   =(SELECT coalesce(sum(e.delta_held::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='REVENUE')
   =(SELECT coalesce(sum(e.delta_consumed::numeric),0) FROM funding_lot_events e WHERE e.journal_id=j.id)
 AND NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.journal_id=j.id AND NOT wallet_event_valid(e.id)))
$$;
CREATE FUNCTION wallet_account_valid(account_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM ledger_accounts a WHERE a.id=account_ref
 AND a.balance::numeric=(SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l WHERE l.account_id=a.id)
 AND (a.tenant_id IS NULL OR (a.kind='AVAILABLE' AND a.balance::numeric=(SELECT coalesce(sum(b.available_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=a.tenant_id))
  OR (a.kind='HELD' AND a.balance::numeric=(SELECT coalesce(sum(b.held_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=a.tenant_id))))
$$;
CREATE FUNCTION wallet_source_fact_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ok BOOLEAN; target_lot TEXT;
BEGIN
 CASE TG_TABLE_NAME
 WHEN 'wallet_openings' THEN SELECT EXISTS(SELECT 1 FROM funding_receipts f WHERE f.source_kind='wallet_opening' AND f.source_id=NEW.id AND wallet_opening_journal_valid(f.journal_id)) INTO ok;
 WHEN 'funding_receipts' THEN SELECT wallet_lot_valid(NEW.id) INTO ok;
 WHEN 'funding_lots' THEN SELECT wallet_lot_valid(NEW.id) INTO ok;
 WHEN 'funding_lot_balances' THEN SELECT wallet_lot_valid(NEW.lot_id) INTO ok;
 WHEN 'funding_lot_events' THEN SELECT wallet_event_valid(NEW.id) AND wallet_lot_valid(NEW.lot_id) INTO ok;
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
DO $$ DECLARE t TEXT; BEGIN
 FOREACH t IN ARRAY ARRAY['funding_lots','funding_lot_events','funding_reservation_lots','funding_settlement_lots','funding_consumption_refund_lots'] LOOP
  EXECUTE format('CREATE TRIGGER immutable_wallet_fact BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION reject_history_mutation()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['wallet_openings','funding_receipts','funding_lots','funding_lot_balances','funding_lot_events','reservations','funding_reservation_lots','settlements','funding_settlement_lots','refund_orders','funding_consumption_refund_lots','journals','ledger_accounts'] LOOP
  EXECUTE format('CREATE CONSTRAINT TRIGGER wallet_source_fact AFTER INSERT OR UPDATE ON %I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION wallet_source_fact_guard()',t);
 END LOOP;
END $$;

-- All current wallet validators execute as the caller within the fixed schema.
DO $$ DECLARE f RECORD; BEGIN
 FOR f IN SELECT p.proname,pg_get_function_identity_arguments(p.oid) args FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=current_schema() AND (left(p.proname,7)='wallet_' OR p.proname='funding_journal_valid') LOOP
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SECURITY INVOKER',current_schema(),f.proname,f.args);
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SET search_path TO %I,pg_catalog,pg_temp',current_schema(),f.proname,f.args,current_schema());
  EXECUTE format('REVOKE ALL ON FUNCTION %I.%I(%s) FROM PUBLIC',current_schema(),f.proname,f.args);
 END LOOP;
END $$;
