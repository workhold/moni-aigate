-- 022-C0 PRODUCTION DDL CANDIDATE, NOT A MIGRATION. Root review required.
-- Apply only to an isolated schema after real migrations 001--022 while
-- reviewing. No migration number, automatic backfill, repair, role creation,
-- network IO, SECURITY DEFINER or modification of applied SQL is authorized.
-- Owner installs empty tables/guards; offline init issues one epoch later.

CREATE FUNCTION fl_uuid(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT v ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND v<>'00000000-0000-0000-0000-000000000000'
$$;
CREATE TYPE fl_source_proof AS (
 tenant_id text,journal_id text,journal_operation_id text,amount bigint,
 available_delta numeric,held_delta numeric,consumed_delta numeric,issued_delta numeric,
 fifo_at timestamptz,refund_class text
);

-- NULL is the exact existing tenant set, including empty wallets. New rows
-- receive a protected top-xid INSERT stamp; no timestamp/xmin inference.
ALTER TABLE tenants ADD COLUMN fl_created_xid xid8;
CREATE FUNCTION fl_tenant_birth_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='INSERT' THEN NEW.fl_created_xid:=pg_catalog.pg_current_xact_id();
 ELSIF NEW.fl_created_xid IS DISTINCT FROM OLD.fl_created_xid THEN RAISE EXCEPTION 'funding_lot_tenant_birth_immutable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER a_fl_tenant_birth BEFORE INSERT OR UPDATE ON tenants FOR EACH ROW EXECUTE FUNCTION fl_tenant_birth_guard();

CREATE TABLE funding_lot_epoch(
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),id text NOT NULL UNIQUE CHECK(fl_uuid(id)),
 schema_version integer NOT NULL CHECK(schema_version=1),database_oid oid NOT NULL CHECK(database_oid<>0),schema_oid oid NOT NULL CHECK(schema_oid<>0),
 basis_digest text NOT NULL CHECK(basis_digest ~ '^[0-9a-f]{64}$'),manifest_digest text NOT NULL CHECK(manifest_digest ~ '^[0-9a-f]{64}$'),
 descriptor_digest text NOT NULL CHECK(descriptor_digest ~ '^[0-9a-f]{64}$'),runtime_proof_digest text NOT NULL CHECK(runtime_proof_digest ~ '^[0-9a-f]{64}$'),
 tenant_count bigint NOT NULL CHECK(tenant_count>=0),receipt_count bigint NOT NULL CHECK(receipt_count>=0),settlement_count bigint NOT NULL CHECK(settlement_count>=0),
 approved_refund_count bigint NOT NULL CHECK(approved_refund_count>=0),opening_count bigint NOT NULL CHECK(opening_count>=0),legacy_source_count bigint NOT NULL CHECK(legacy_source_count>=0),
 available_total numeric NOT NULL CHECK(available_total>=0),held_total numeric NOT NULL CHECK(held_total>=0),
 actor text NOT NULL CHECK(actor='offline-maintenance'),created_at timestamptz(6) NOT NULL CHECK(isfinite(created_at)),
 UNIQUE(id,created_at)
);
-- Version 2 is appended; immutable 022 payment_runtime_attestations stays v1.
CREATE TABLE funding_lot_runtime_attestations(
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),epoch_id text NOT NULL UNIQUE,
 proof_version integer NOT NULL CHECK(proof_version=2),mode text NOT NULL CHECK(mode IN ('ordinary','sandbox')),
 database_oid oid NOT NULL,schema_oid oid NOT NULL,schema_owner_oid oid NOT NULL,app_role_oid oid NOT NULL,worker_role_oid oid,projector_role_oid oid NOT NULL,
 predecessor_payment_digest text CHECK(predecessor_payment_digest ~ '^[0-9a-f]{64}$'),
 proof_canonical bytea NOT NULL CHECK(octet_length(proof_canonical) BETWEEN 1 AND 262144),
 proof_sha256 text NOT NULL CHECK(proof_sha256 ~ '^[0-9a-f]{64}$' AND proof_sha256=encode(sha256(proof_canonical),'hex')),
 manifest_digest text NOT NULL CHECK(manifest_digest ~ '^[0-9a-f]{64}$'),descriptor_digest text NOT NULL CHECK(descriptor_digest ~ '^[0-9a-f]{64}$'),
 created_at timestamptz(6) NOT NULL CHECK(isfinite(created_at)),
 CHECK(app_role_oid<>projector_role_oid AND app_role_oid<>schema_owner_oid AND projector_role_oid<>schema_owner_oid),
 CHECK((mode='sandbox')=(worker_role_oid IS NOT NULL AND predecessor_payment_digest IS NOT NULL)),
 CHECK(worker_role_oid IS NULL OR (worker_role_oid<>app_role_oid AND worker_role_oid<>projector_role_oid AND worker_role_oid<>schema_owner_oid)),
 FOREIGN KEY(epoch_id) REFERENCES funding_lot_epoch(id) DEFERRABLE INITIALLY DEFERRED,
 UNIQUE(epoch_id,proof_sha256)
);
ALTER TABLE funding_lot_epoch ADD FOREIGN KEY(id,runtime_proof_digest) REFERENCES funding_lot_runtime_attestations(epoch_id,proof_sha256) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE funding_lot_legacy_receipts(
 receipt_id text PRIMARY KEY,epoch_id text NOT NULL REFERENCES funding_lot_epoch(id) DEFERRABLE INITIALLY DEFERRED,
 tenant_id text NOT NULL,source_kind text NOT NULL CHECK(source_kind IN ('manual_credit','voucher_grant','sandbox_capture')),source_id text NOT NULL,
 amount bigint NOT NULL CHECK(amount>0),journal_id text NOT NULL UNIQUE,
 FOREIGN KEY(tenant_id,receipt_id) REFERENCES funding_receipts(tenant_id,id),FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id)
);
-- Narrow duplicate membership, not duplicate principal. Only sandbox rows;
-- allows a worker to prove legacy/new without SELECT on A/request metadata.
CREATE TABLE funding_lot_legacy_sandbox_receipts(
 receipt_id text PRIMARY KEY,epoch_id text NOT NULL REFERENCES funding_lot_epoch(id) DEFERRABLE INITIALLY DEFERRED,
 tenant_id text NOT NULL,capture_id text NOT NULL UNIQUE,amount bigint NOT NULL CHECK(amount>0),journal_id text NOT NULL UNIQUE,
 FOREIGN KEY(tenant_id,receipt_id) REFERENCES funding_receipts(tenant_id,id),FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id)
);
CREATE TABLE funding_lot_legacy_settlements(
 settlement_id text PRIMARY KEY,epoch_id text NOT NULL REFERENCES funding_lot_epoch(id) DEFERRABLE INITIALLY DEFERRED,
 tenant_id text NOT NULL,reservation_id text NOT NULL UNIQUE,request_id text NOT NULL UNIQUE,
 charged_amount bigint NOT NULL CHECK(charged_amount>=0),refunded_amount_at_cutover bigint NOT NULL CHECK(refunded_amount_at_cutover>=0 AND refunded_amount_at_cutover<=charged_amount),journal_id text NOT NULL UNIQUE,
 FOREIGN KEY(tenant_id,settlement_id) REFERENCES settlements(tenant_id,id),FOREIGN KEY(tenant_id,reservation_id) REFERENCES reservations(tenant_id,id),FOREIGN KEY(tenant_id,request_id) REFERENCES request_records(tenant_id,id),FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id),
 UNIQUE(tenant_id,settlement_id)
);
CREATE TABLE funding_lot_legacy_approved_refunds(
 refund_order_id text PRIMARY KEY REFERENCES refund_orders(id),epoch_id text NOT NULL REFERENCES funding_lot_epoch(id) DEFERRABLE INITIALLY DEFERRED,
 tenant_id text NOT NULL,settlement_id text NOT NULL,amount bigint NOT NULL CHECK(amount>0),journal_id text NOT NULL UNIQUE,
 FOREIGN KEY(tenant_id,settlement_id) REFERENCES settlements(tenant_id,id),FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id)
);
CREATE TABLE funding_lot_legacy_openings(
 id text PRIMARY KEY CHECK(fl_uuid(id)),epoch_id text NOT NULL REFERENCES funding_lot_epoch(id) DEFERRABLE INITIALLY DEFERRED,tenant_id text NOT NULL REFERENCES tenants(id),
 kind text NOT NULL CHECK(kind IN ('available','reservation')),account_id text REFERENCES ledger_accounts(id),reservation_id text,
 amount bigint NOT NULL CHECK(amount>=0),observed_at timestamptz(6) NOT NULL CHECK(isfinite(observed_at)),
 CHECK((kind='reservation')=(reservation_id IS NOT NULL)),CHECK(account_id IS NOT NULL OR (kind='available' AND amount=0)),
 FOREIGN KEY(tenant_id,reservation_id) REFERENCES reservations(tenant_id,id),UNIQUE(tenant_id,id),UNIQUE(reservation_id)
);
CREATE UNIQUE INDEX funding_lot_one_available_opening ON funding_lot_legacy_openings(tenant_id) WHERE kind='available';
CREATE TABLE funding_wallet_legacy_sources(
 epoch_id text NOT NULL REFERENCES funding_lot_epoch(id) DEFERRABLE INITIALLY DEFERRED,tenant_id text NOT NULL REFERENCES tenants(id),
 kind text NOT NULL CHECK(kind IN ('manual_credit','voucher_grant','sandbox_capture','reserve','finish','consumption_refund')),source_id text NOT NULL,
 journal_id text NOT NULL UNIQUE,journal_operation_id text NOT NULL UNIQUE,amount bigint NOT NULL CHECK(amount>=0),
 request_id text,reservation_id text,settlement_id text,
 PRIMARY KEY(kind,source_id),FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id),UNIQUE(tenant_id,kind,source_id)
);
CREATE TABLE funding_wallet_operations(
 id text PRIMARY KEY CHECK(fl_uuid(id)),tenant_id text NOT NULL REFERENCES tenants(id),epoch_id text NOT NULL REFERENCES funding_lot_epoch(id) DEFERRABLE INITIALLY DEFERRED,
 seq bigint NOT NULL CHECK(seq>=0),previous_seq bigint,previous_id text UNIQUE,
 kind text NOT NULL CHECK(kind IN ('epoch_opening','tenant_genesis','manual_credit','voucher_grant','sandbox_capture','reserve','finish','consumption_refund','legacy_consumption_refund')),source_id text NOT NULL,
 journal_id text UNIQUE,journal_operation_id text,event_count bigint NOT NULL CHECK(event_count>=0),
 available_delta numeric NOT NULL,held_delta numeric NOT NULL,consumed_delta numeric NOT NULL,issued_delta numeric NOT NULL,
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 CHECK((seq=0 AND previous_seq IS NULL AND previous_id IS NULL AND kind IN ('epoch_opening','tenant_genesis')) OR (seq>0 AND previous_seq IS NOT NULL AND previous_id IS NOT NULL AND previous_seq::numeric=seq::numeric-1 AND kind NOT IN ('epoch_opening','tenant_genesis'))),
 CHECK((seq=0)=(journal_id IS NULL AND journal_operation_id IS NULL)),CHECK((journal_id IS NULL)=(journal_operation_id IS NULL)),
 UNIQUE(tenant_id,seq),UNIQUE(tenant_id,id),UNIQUE(kind,source_id),UNIQUE(tenant_id,epoch_id,seq,id),
 FOREIGN KEY(tenant_id,epoch_id,previous_seq,previous_id) REFERENCES funding_wallet_operations(tenant_id,epoch_id,seq,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id)
);
CREATE TABLE funding_wallet_heads(
 tenant_id text PRIMARY KEY REFERENCES tenants(id),epoch_id text NOT NULL,tail_seq bigint NOT NULL CHECK(tail_seq>=0),tail_operation_id text NOT NULL,
 FOREIGN KEY(tenant_id,epoch_id,tail_seq,tail_operation_id) REFERENCES funding_wallet_operations(tenant_id,epoch_id,seq,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_lots(
 id text PRIMARY KEY CHECK(fl_uuid(id)),tenant_id text NOT NULL REFERENCES tenants(id),origin_kind text NOT NULL CHECK(origin_kind IN ('funding_receipt','legacy_available','legacy_reservation','legacy_consumption_refund')),
 funding_receipt_id text UNIQUE,legacy_opening_id text UNIQUE,legacy_refund_order_id text UNIQUE REFERENCES refund_orders(id),
 issued_amount bigint NOT NULL CHECK(issued_amount>0),currency text NOT NULL CHECK(currency='CNY'),refund_class text NOT NULL CHECK(refund_class IN ('nonrefundable','sandbox_original')),
 fifo_at timestamptz(6) NOT NULL CHECK(isfinite(fifo_at)),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 CHECK((origin_kind='funding_receipt' AND funding_receipt_id IS NOT NULL AND legacy_opening_id IS NULL AND legacy_refund_order_id IS NULL) OR (origin_kind IN ('legacy_available','legacy_reservation') AND funding_receipt_id IS NULL AND legacy_opening_id IS NOT NULL AND legacy_refund_order_id IS NULL) OR (origin_kind='legacy_consumption_refund' AND funding_receipt_id IS NULL AND legacy_opening_id IS NULL AND legacy_refund_order_id IS NOT NULL)),
 FOREIGN KEY(tenant_id,funding_receipt_id) REFERENCES funding_receipts(tenant_id,id),FOREIGN KEY(tenant_id,legacy_opening_id) REFERENCES funding_lot_legacy_openings(tenant_id,id),UNIQUE(tenant_id,id)
);
CREATE INDEX funding_lot_fifo ON funding_lots(tenant_id,fifo_at,id COLLATE "C");
CREATE TABLE funding_lot_events(
 id text PRIMARY KEY CHECK(fl_uuid(id)),tenant_id text NOT NULL,lot_id text NOT NULL,wallet_operation_id text NOT NULL,wallet_seq bigint NOT NULL CHECK(wallet_seq>=0),
 kind text NOT NULL CHECK(kind IN ('funding','legacy_opening','reserve','finish','consumption_refund','legacy_consumption_refund')),operation_id text,journal_id text,
 previous_event_id text UNIQUE,before_revision bigint NOT NULL CHECK(before_revision>=0),after_revision bigint NOT NULL CHECK(after_revision>0 AND after_revision::numeric=before_revision::numeric+1),
 before_available bigint NOT NULL CHECK(before_available>=0),before_held bigint NOT NULL CHECK(before_held>=0),before_consumed bigint NOT NULL CHECK(before_consumed>=0),
 after_available bigint NOT NULL CHECK(after_available>=0),after_held bigint NOT NULL CHECK(after_held>=0),after_consumed bigint NOT NULL CHECK(after_consumed>=0),
 available_delta numeric NOT NULL,inference_held_delta numeric NOT NULL,consumed_delta numeric NOT NULL,issued_delta numeric NOT NULL CHECK(issued_delta>=0),
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 CHECK(after_available::numeric=before_available::numeric+available_delta AND after_held::numeric=before_held::numeric+inference_held_delta AND after_consumed::numeric=before_consumed::numeric+consumed_delta),
 CHECK(available_delta+inference_held_delta+consumed_delta=issued_delta),CHECK((before_revision=0)=(previous_event_id IS NULL)),
 CHECK((kind='legacy_opening')=(journal_id IS NULL AND operation_id IS NULL)),CHECK((journal_id IS NULL)=(operation_id IS NULL)),
 UNIQUE(lot_id,after_revision),UNIQUE(wallet_operation_id,lot_id),UNIQUE(lot_id,id),UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id),FOREIGN KEY(tenant_id,wallet_operation_id) REFERENCES funding_wallet_operations(tenant_id,id),
 FOREIGN KEY(lot_id,previous_event_id) REFERENCES funding_lot_events(lot_id,id) DEFERRABLE INITIALLY DEFERRED,FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id)
);
CREATE TABLE funding_lot_balances(
 lot_id text PRIMARY KEY,tenant_id text NOT NULL,available_amount bigint NOT NULL CHECK(available_amount>=0),inference_held_amount bigint NOT NULL CHECK(inference_held_amount>=0),consumed_amount bigint NOT NULL CHECK(consumed_amount>=0),
 revision bigint NOT NULL CHECK(revision>0),last_event_id text NOT NULL,
 FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id),FOREIGN KEY(lot_id,last_event_id) REFERENCES funding_lot_events(lot_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE funding_reservation_lots(
 id text PRIMARY KEY CHECK(fl_uuid(id)),tenant_id text NOT NULL,reservation_id text NOT NULL,lot_id text NOT NULL,allocation_index bigint NOT NULL CHECK(allocation_index>0),amount bigint NOT NULL CHECK(amount>0),
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 UNIQUE(reservation_id,lot_id),UNIQUE(reservation_id,allocation_index),FOREIGN KEY(tenant_id,reservation_id) REFERENCES reservations(tenant_id,id),FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id),UNIQUE(tenant_id,id)
);
CREATE TABLE funding_settlement_lots(
 id text PRIMARY KEY CHECK(fl_uuid(id)),tenant_id text NOT NULL,settlement_id text NOT NULL,reservation_lot_id text NOT NULL UNIQUE,
 charged_amount bigint NOT NULL CHECK(charged_amount>=0),released_amount bigint NOT NULL CHECK(released_amount>=0),
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 FOREIGN KEY(tenant_id,settlement_id) REFERENCES settlements(tenant_id,id),FOREIGN KEY(tenant_id,reservation_lot_id) REFERENCES funding_reservation_lots(tenant_id,id),UNIQUE(tenant_id,id)
);
CREATE TABLE funding_consumption_refund_lots(
 id text PRIMARY KEY CHECK(fl_uuid(id)),tenant_id text NOT NULL,refund_order_id text NOT NULL REFERENCES refund_orders(id),lot_id text NOT NULL,settlement_lot_id text,
 allocation_index bigint NOT NULL CHECK(allocation_index>0),amount bigint NOT NULL CHECK(amount>0),origin text NOT NULL CHECK(origin IN ('allocated','legacy_settlement')),
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),created_at timestamptz(6) NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 CHECK((origin='allocated')=(settlement_lot_id IS NOT NULL)),UNIQUE(refund_order_id,lot_id),UNIQUE(refund_order_id,allocation_index),
 FOREIGN KEY(tenant_id,lot_id) REFERENCES funding_lots(tenant_id,id),FOREIGN KEY(tenant_id,settlement_lot_id) REFERENCES funding_settlement_lots(tenant_id,id)
);

CREATE FUNCTION fl_require_epoch() RETURNS funding_lot_epoch LANGUAGE plpgsql STABLE AS $$ DECLARE e funding_lot_epoch;att funding_lot_runtime_attestations; BEGIN
 SELECT * INTO e FROM funding_lot_epoch WHERE singleton;
 IF e.id IS NULL OR e.schema_version<>1 OR e.database_oid<>(SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()) OR e.schema_oid<>current_schema()::regnamespace::oid
 THEN RAISE EXCEPTION 'funding_lot_uninitialized' USING ERRCODE='23514'; END IF;
 IF NOT EXISTS(SELECT 1 FROM funding_lot_runtime_attestations a WHERE a.epoch_id=e.id AND a.proof_sha256=e.runtime_proof_digest AND a.database_oid=e.database_oid AND a.schema_oid=e.schema_oid AND a.manifest_digest=e.manifest_digest AND a.descriptor_digest=e.descriptor_digest)
 THEN RAISE EXCEPTION 'funding_lot_invariant' USING ERRCODE='23514'; END IF;
 SELECT * INTO STRICT att FROM funding_lot_runtime_attestations WHERE epoch_id=e.id;
 IF att.mode='sandbox' THEN
  IF NOT EXISTS(SELECT 1 FROM payment_runtime_attestations p JOIN payment_sandbox_environment m ON m.id=p.environment_id
   WHERE p.proof_sha256=att.predecessor_payment_digest AND (p.database_oid,p.schema_oid,p.schema_owner_oid,p.app_role_oid,p.worker_role_oid,p.projector_role_oid)=(att.database_oid,att.schema_oid,att.schema_owner_oid,att.app_role_oid,att.worker_role_oid,att.projector_role_oid)
   AND m.database_oid=att.database_oid AND m.schema_oid=att.schema_oid AND (SELECT oid FROM pg_catalog.pg_roles WHERE rolname=m.app_role)=att.app_role_oid AND (SELECT oid FROM pg_catalog.pg_roles WHERE rolname=m.worker_role)=att.worker_role_oid AND (SELECT oid FROM pg_catalog.pg_roles WHERE rolname=m.projector_role)=att.projector_role_oid)
  THEN RAISE EXCEPTION 'funding_lot_payment_predecessor' USING ERRCODE='23514'; END IF;
 ELSIF EXISTS(SELECT 1 FROM payment_sandbox_environment) THEN RAISE EXCEPTION 'funding_lot_mode_mismatch' USING ERRCODE='23514'; END IF;
 RETURN e;
END $$;

CREATE FUNCTION fl_guard_head() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE o funding_wallet_operations; BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'funding_lot_head_delete' USING ERRCODE='23514'; END IF;
 SELECT * INTO o FROM funding_wallet_operations WHERE id=NEW.tail_operation_id;
 IF o.id IS NULL OR o.created_xid<>pg_catalog.pg_current_xact_id() OR (o.tenant_id,o.epoch_id,o.seq) IS DISTINCT FROM (NEW.tenant_id,NEW.epoch_id,NEW.tail_seq)
 THEN RAISE EXCEPTION 'funding_lot_head_operation' USING ERRCODE='23514'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.tail_seq<>0 OR o.previous_id IS NOT NULL THEN RAISE EXCEPTION 'funding_lot_head_genesis' USING ERRCODE='23514'; END IF;
 ELSE
  IF NEW.tenant_id<>OLD.tenant_id OR NEW.epoch_id<>OLD.epoch_id OR OLD.tail_seq=9223372036854775807 OR NEW.tail_seq::numeric<>OLD.tail_seq::numeric+1 OR o.previous_id<>OLD.tail_operation_id OR o.previous_seq<>OLD.tail_seq
  THEN RAISE EXCEPTION 'funding_lot_head_cas' USING ERRCODE='23514'; END IF;
 END IF; RETURN NEW;
END $$;
CREATE FUNCTION fl_guard_event() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE o funding_wallet_operations;l funding_lots;expected text; BEGIN
 SELECT * INTO STRICT o FROM funding_wallet_operations WHERE id=NEW.wallet_operation_id;SELECT * INTO STRICT l FROM funding_lots WHERE id=NEW.lot_id;
 expected:=CASE o.kind WHEN 'epoch_opening' THEN 'legacy_opening' WHEN 'manual_credit' THEN 'funding' WHEN 'voucher_grant' THEN 'funding' WHEN 'sandbox_capture' THEN 'funding' ELSE o.kind END;
 IF o.kind='tenant_genesis' OR o.created_xid<>pg_catalog.pg_current_xact_id() OR (NEW.tenant_id,NEW.wallet_seq,NEW.kind) IS DISTINCT FROM (o.tenant_id,o.seq,expected)
 OR NEW.tenant_id<>l.tenant_id OR NEW.journal_id IS DISTINCT FROM o.journal_id OR NEW.operation_id IS DISTINCT FROM o.journal_operation_id
 THEN RAISE EXCEPTION 'funding_lot_event_operation' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION fl_guard_balance() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE e funding_lot_events; BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'funding_lot_balance_delete' USING ERRCODE='23514'; END IF;
 SELECT * INTO e FROM funding_lot_events WHERE id=NEW.last_event_id;
 IF e.id IS NULL OR e.created_xid<>pg_catalog.pg_current_xact_id() OR (NEW.tenant_id,NEW.lot_id,NEW.revision,NEW.available_amount,NEW.inference_held_amount,NEW.consumed_amount) IS DISTINCT FROM (e.tenant_id,e.lot_id,e.after_revision,e.after_available,e.after_held,e.after_consumed)
 THEN RAISE EXCEPTION 'funding_lot_balance_event' USING ERRCODE='23514'; END IF;
 IF TG_OP='INSERT' THEN
  IF e.before_revision<>0 OR e.previous_event_id IS NOT NULL OR e.before_available<>0 OR e.before_held<>0 OR e.before_consumed<>0 THEN RAISE EXCEPTION 'funding_lot_balance_initial' USING ERRCODE='23514'; END IF;
 ELSE
  IF NEW.lot_id<>OLD.lot_id OR NEW.tenant_id<>OLD.tenant_id OR e.before_revision<>OLD.revision OR e.previous_event_id<>OLD.last_event_id
   OR (e.before_available,e.before_held,e.before_consumed) IS DISTINCT FROM (OLD.available_amount,OLD.inference_held_amount,OLD.consumed_amount)
  THEN RAISE EXCEPTION 'funding_lot_balance_predecessor' USING ERRCODE='23514'; END IF;
 END IF;RETURN NEW;
END $$;
CREATE FUNCTION fl_guard_allocation() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE t text;p text;lot text; BEGIN
 IF TG_TABLE_NAME='funding_reservation_lots' THEN
  SELECT tenant_id INTO STRICT t FROM reservations WHERE id=NEW.reservation_id;
 ELSIF TG_TABLE_NAME='funding_settlement_lots' THEN
  SELECT s.tenant_id,v.id INTO STRICT t,p FROM settlements s JOIN reservations v ON v.request_id=s.request_id WHERE s.id=NEW.settlement_id;
  IF NOT EXISTS(SELECT 1 FROM funding_reservation_lots a WHERE a.id=NEW.reservation_lot_id AND a.reservation_id=p AND a.tenant_id=t) THEN RAISE EXCEPTION 'funding_lot_finish_edge' USING ERRCODE='23514'; END IF;
 ELSE
  SELECT f.tenant_id,f.settlement_id INTO STRICT t,p FROM refund_orders f WHERE f.id=NEW.refund_order_id AND f.state='approved';
  IF NEW.origin='allocated' THEN
   SELECT a.lot_id INTO lot FROM funding_settlement_lots b JOIN funding_reservation_lots a ON a.id=b.reservation_lot_id WHERE b.id=NEW.settlement_lot_id AND b.settlement_id=p AND b.tenant_id=t;
   IF lot IS NULL OR NEW.lot_id<>lot OR EXISTS(SELECT 1 FROM funding_lot_legacy_settlements WHERE settlement_id=p) THEN RAISE EXCEPTION 'funding_lot_refund_edge' USING ERRCODE='23514'; END IF;
  ELSE
   IF NOT EXISTS(SELECT 1 FROM funding_lot_legacy_settlements WHERE settlement_id=p AND tenant_id=t) OR NOT EXISTS(SELECT 1 FROM funding_lots WHERE id=NEW.lot_id AND legacy_refund_order_id=NEW.refund_order_id AND tenant_id=t) THEN RAISE EXCEPTION 'funding_lot_legacy_refund_edge' USING ERRCODE='23514'; END IF;
  END IF;
 END IF;
 IF NEW.tenant_id<>t THEN RAISE EXCEPTION 'funding_lot_edge_tenant' USING ERRCODE='23514'; END IF;
 NEW.created_xid:=pg_catalog.pg_current_xact_id();NEW.created_at:=pg_catalog.clock_timestamp();RETURN NEW;
END $$;
CREATE FUNCTION fl_check_wallet_head(t text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE h funding_wallet_heads;n numeric;lo bigint;hi bigint; BEGIN
 SELECT * INTO h FROM funding_wallet_heads WHERE tenant_id=t;SELECT count(*)::numeric,min(seq),max(seq) INTO n,lo,hi FROM funding_wallet_operations WHERE tenant_id=t;
 IF h.tenant_id IS NULL OR n<>h.tail_seq::numeric+1 OR lo<>0 OR hi<>h.tail_seq
 OR NOT EXISTS(SELECT 1 FROM funding_wallet_operations o WHERE (o.tenant_id,o.epoch_id,o.seq,o.id)=(t,h.epoch_id,hi,h.tail_operation_id))
 OR EXISTS(SELECT 1 FROM funding_wallet_operations o LEFT JOIN funding_wallet_operations p ON p.id=o.previous_id WHERE o.tenant_id=t AND o.seq>0 AND (p.id IS NULL OR p.tenant_id<>t OR p.epoch_id<>o.epoch_id OR p.seq<>o.previous_seq OR p.seq::numeric<>o.seq::numeric-1))
 THEN RAISE EXCEPTION 'funding_lot_wallet_prefix' USING ERRCODE='23514'; END IF;
END $$;
CREATE FUNCTION fl_check_wallet_account(t text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE a numeric;h numeric;la numeric;lh numeric; BEGIN
 SELECT coalesce(sum(balance::numeric) FILTER(WHERE kind='AVAILABLE'),0),coalesce(sum(balance::numeric) FILTER(WHERE kind='HELD'),0) INTO a,h FROM ledger_accounts WHERE tenant_id=t;
 SELECT coalesce(sum(available_amount::numeric),0),coalesce(sum(inference_held_amount::numeric),0) INTO la,lh FROM funding_lot_balances WHERE tenant_id=t;
 IF a<>la OR h<>lh THEN RAISE EXCEPTION 'funding_lot_wallet_balance' USING ERRCODE='23514'; END IF;
END $$;
CREATE FUNCTION fl_check_lot(lid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE l funding_lots;b funding_lot_balances;last funding_lot_events; BEGIN
 SELECT * INTO STRICT l FROM funding_lots WHERE id=lid;
 IF EXISTS(
  WITH es AS (
   SELECT x.*,o.seq,row_number() OVER(ORDER BY o.seq) rn,lag(x.id) OVER(ORDER BY o.seq) prev,
    coalesce(sum(x.available_delta) OVER(ORDER BY o.seq ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) ba,
    coalesce(sum(x.inference_held_delta) OVER(ORDER BY o.seq ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) bh,
    coalesce(sum(x.consumed_delta) OVER(ORDER BY o.seq ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) bs
   FROM funding_lot_events x JOIN funding_wallet_operations o ON o.id=x.wallet_operation_id WHERE x.lot_id=lid
  ) SELECT 1 FROM es WHERE after_revision<>rn OR before_revision::numeric<>rn-1 OR previous_event_id IS DISTINCT FROM prev OR before_available::numeric<>ba OR before_held::numeric<>bh OR before_consumed::numeric<>bs
   OR after_available::numeric+after_held::numeric+after_consumed::numeric<>l.issued_amount::numeric OR issued_delta<>(CASE WHEN rn=1 THEN l.issued_amount::numeric ELSE 0 END)
 ) THEN RAISE EXCEPTION 'funding_lot_event_chain' USING ERRCODE='23514'; END IF;
 SELECT * INTO last FROM funding_lot_events WHERE lot_id=lid ORDER BY after_revision DESC LIMIT 1;SELECT * INTO b FROM funding_lot_balances WHERE lot_id=lid;
 IF last.id IS NULL OR b.lot_id IS NULL OR (b.tenant_id,b.revision,b.last_event_id,b.available_amount,b.inference_held_amount,b.consumed_amount) IS DISTINCT FROM (l.tenant_id,last.after_revision,last.id,last.after_available,last.after_held,last.after_consumed)
 THEN RAISE EXCEPTION 'funding_lot_balance_chain' USING ERRCODE='23514'; END IF;
 PERFORM fl_check_wallet_account(l.tenant_id);
END $$;

CREATE FUNCTION fl_validate_new_funding(oid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE o funding_wallet_operations;p fl_source_proof; BEGIN
 SELECT * INTO STRICT o FROM funding_wallet_operations WHERE id=oid;p:=fl_read_source(o.kind,o.source_id);
 IF (SELECT count(*) FROM funding_lots WHERE funding_receipt_id=o.source_id)<>1
 OR EXISTS(
  WITH ls AS (SELECT * FROM funding_lots WHERE funding_receipt_id=o.source_id),es AS (SELECT * FROM funding_lot_events WHERE wallet_operation_id=o.id)
  SELECT 1 FROM ls FULL JOIN es ON es.lot_id=ls.id WHERE ls.id IS NULL OR es.id IS NULL OR ls.tenant_id<>p.tenant_id OR ls.origin_kind<>'funding_receipt' OR ls.issued_amount<>p.amount OR ls.fifo_at<>p.fifo_at OR ls.refund_class<>p.refund_class OR es.kind<>'funding'
   OR es.before_revision<>0 OR es.before_available<>0 OR es.before_held<>0 OR es.before_consumed<>0 OR es.available_delta<>p.amount::numeric OR es.inference_held_delta<>0 OR es.consumed_delta<>0 OR es.issued_delta<>p.amount::numeric
 ) THEN RAISE EXCEPTION 'funding_lot_funding_mapping' USING ERRCODE='23514'; END IF;
END $$;
CREATE FUNCTION fl_validate_funding(oid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE o funding_wallet_operations; BEGIN
 SELECT * INTO STRICT o FROM funding_wallet_operations WHERE id=oid;
 IF o.kind NOT IN ('manual_credit','voucher_grant') OR EXISTS(SELECT 1 FROM funding_lot_legacy_receipts WHERE receipt_id=o.source_id) THEN RAISE EXCEPTION 'funding_lot_legacy_receipt_boundary' USING ERRCODE='23514'; END IF;
 PERFORM fl_validate_new_funding(oid);
END $$;
CREATE FUNCTION fl_validate_sandbox_funding(oid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE o funding_wallet_operations; BEGIN
 SELECT * INTO STRICT o FROM funding_wallet_operations WHERE id=oid;
 IF o.kind<>'sandbox_capture' OR EXISTS(SELECT 1 FROM funding_lot_legacy_sandbox_receipts WHERE receipt_id=o.source_id) THEN RAISE EXCEPTION 'funding_lot_sandbox_legacy_boundary' USING ERRCODE='23514'; END IF;
 PERFORM fl_validate_new_funding(oid);
END $$;
CREATE FUNCTION fl_validate_reserve(oid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE o funding_wallet_operations;v reservations; BEGIN
 SELECT * INTO STRICT o FROM funding_wallet_operations WHERE id=oid;SELECT * INTO STRICT v FROM reservations WHERE id=o.source_id;
 IF EXISTS(
  WITH before_lots AS (
   SELECT l.id,l.fifo_at,coalesce(sum(e.available_delta) FILTER(WHERE op.seq<o.seq),0) a FROM funding_lots l LEFT JOIN funding_lot_events e ON e.lot_id=l.id LEFT JOIN funding_wallet_operations op ON op.id=e.wallet_operation_id
   WHERE l.tenant_id=o.tenant_id GROUP BY l.id,l.fifo_at
  ),candidates AS (SELECT *,coalesce(sum(a) OVER(ORDER BY fifo_at,id COLLATE "C" ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) prior FROM before_lots WHERE a>0),
  expected0 AS (SELECT *,greatest(least(a,v.amount::numeric-prior),0) q FROM candidates),
  expected AS (SELECT id,q,row_number() OVER(ORDER BY fifo_at,id COLLATE "C") idx FROM expected0 WHERE q>0),
  actual AS (SELECT * FROM funding_reservation_lots WHERE reservation_id=v.id)
  SELECT 1 FROM expected x FULL JOIN actual a ON a.lot_id=x.id WHERE x.id IS NULL OR a.id IS NULL OR a.tenant_id<>o.tenant_id OR a.amount::numeric<>x.q OR a.allocation_index<>x.idx
 ) OR (SELECT coalesce(sum(amount::numeric),0) FROM funding_reservation_lots WHERE reservation_id=v.id)<>v.amount::numeric
 OR EXISTS(
  WITH xs AS (SELECT * FROM funding_reservation_lots WHERE reservation_id=v.id),es AS (SELECT * FROM funding_lot_events WHERE wallet_operation_id=o.id)
  SELECT 1 FROM xs FULL JOIN es ON es.lot_id=xs.lot_id WHERE xs.id IS NULL OR es.id IS NULL OR es.kind<>'reserve' OR es.available_delta<>-xs.amount::numeric OR es.inference_held_delta<>xs.amount::numeric OR es.consumed_delta<>0 OR es.issued_delta<>0
 ) THEN RAISE EXCEPTION 'funding_lot_fifo_allocation' USING ERRCODE='23514'; END IF;
END $$;
CREATE FUNCTION fl_validate_finish(oid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE o funding_wallet_operations;s settlements;v reservations; BEGIN
 SELECT * INTO STRICT o FROM funding_wallet_operations WHERE id=oid;SELECT * INTO STRICT s FROM settlements WHERE id=o.source_id;SELECT * INTO STRICT v FROM reservations WHERE request_id=s.request_id;
 IF EXISTS(SELECT 1 FROM funding_lot_legacy_settlements WHERE settlement_id=s.id) OR NOT EXISTS(SELECT 1 FROM funding_wallet_operations p WHERE p.tenant_id=o.tenant_id AND p.kind='reserve' AND p.source_id=v.id AND p.seq<o.seq)
  AND NOT EXISTS(SELECT 1 FROM funding_wallet_legacy_sources WHERE tenant_id=o.tenant_id AND kind='reserve' AND source_id=v.id)
 THEN RAISE EXCEPTION 'funding_lot_finish_boundary' USING ERRCODE='23514'; END IF;
 IF EXISTS(
  WITH alloc AS (SELECT a.*,coalesce(sum(amount::numeric) OVER(ORDER BY allocation_index ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) prior FROM funding_reservation_lots a WHERE reservation_id=v.id),
  expected AS (SELECT *,greatest(least(amount::numeric,s.charged_amount::numeric-prior),0) c FROM alloc),actual AS (SELECT * FROM funding_settlement_lots WHERE settlement_id=s.id)
  SELECT 1 FROM expected a FULL JOIN actual b ON b.reservation_lot_id=a.id WHERE a.id IS NULL OR b.id IS NULL OR b.tenant_id<>o.tenant_id OR b.charged_amount::numeric<>a.c OR b.released_amount::numeric<>a.amount::numeric-a.c
 ) OR (SELECT coalesce(sum(amount::numeric),0) FROM funding_reservation_lots WHERE reservation_id=v.id)<>v.amount::numeric
 OR EXISTS(
  WITH xs AS (SELECT a.lot_id,a.amount,b.charged_amount,b.released_amount FROM funding_settlement_lots b JOIN funding_reservation_lots a ON a.id=b.reservation_lot_id WHERE b.settlement_id=s.id),es AS (SELECT * FROM funding_lot_events WHERE wallet_operation_id=o.id)
  SELECT 1 FROM xs FULL JOIN es ON es.lot_id=xs.lot_id WHERE xs.lot_id IS NULL OR es.id IS NULL OR es.kind<>'finish' OR es.available_delta<>xs.released_amount::numeric OR es.inference_held_delta<>-xs.amount::numeric OR es.consumed_delta<>xs.charged_amount::numeric OR es.issued_delta<>0
 ) THEN RAISE EXCEPTION 'funding_lot_finish_allocation' USING ERRCODE='23514'; END IF;
END $$;
CREATE FUNCTION fl_validate_refund(oid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE o funding_wallet_operations;f refund_orders; BEGIN
 SELECT * INTO STRICT o FROM funding_wallet_operations WHERE id=oid;SELECT * INTO STRICT f FROM refund_orders WHERE id=o.source_id AND state='approved';
 IF EXISTS(SELECT 1 FROM funding_lot_legacy_settlements WHERE settlement_id=f.settlement_id) OR EXISTS(SELECT 1 FROM funding_lot_legacy_approved_refunds WHERE refund_order_id=f.id)
 OR NOT EXISTS(SELECT 1 FROM funding_wallet_operations p WHERE p.tenant_id=o.tenant_id AND p.kind='finish' AND p.source_id=f.settlement_id AND p.seq<o.seq)
 THEN RAISE EXCEPTION 'funding_lot_refund_boundary' USING ERRCODE='23514'; END IF;
 IF EXISTS(
  WITH edges AS (
   SELECT b.id,a.allocation_index,b.charged_amount::numeric-coalesce((SELECT sum(r.amount::numeric) FROM funding_consumption_refund_lots r JOIN funding_wallet_operations p ON p.kind='consumption_refund' AND p.source_id=r.refund_order_id WHERE r.settlement_lot_id=b.id AND p.tenant_id=o.tenant_id AND p.seq<o.seq),0) remain
   FROM funding_settlement_lots b JOIN funding_reservation_lots a ON a.id=b.reservation_lot_id WHERE b.settlement_id=f.settlement_id
  ),candidates AS (SELECT *,coalesce(sum(remain) OVER(ORDER BY allocation_index DESC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) prior FROM edges WHERE remain>0),
  expected0 AS (SELECT *,greatest(least(remain,f.amount::numeric-prior),0) q FROM candidates),expected AS (SELECT *,row_number() OVER(ORDER BY allocation_index DESC) idx FROM expected0 WHERE q>0),actual AS (SELECT * FROM funding_consumption_refund_lots WHERE refund_order_id=f.id)
  SELECT 1 FROM expected x FULL JOIN actual a ON a.settlement_lot_id=x.id WHERE x.id IS NULL OR a.id IS NULL OR a.tenant_id<>o.tenant_id OR a.origin<>'allocated' OR a.amount::numeric<>x.q OR a.allocation_index<>x.idx
 ) OR (SELECT coalesce(sum(amount::numeric),0) FROM funding_consumption_refund_lots WHERE refund_order_id=f.id)<>f.amount::numeric
 OR EXISTS(
  WITH xs AS (SELECT r.lot_id,r.amount FROM funding_consumption_refund_lots r WHERE r.refund_order_id=f.id),es AS (SELECT * FROM funding_lot_events WHERE wallet_operation_id=o.id)
  SELECT 1 FROM xs FULL JOIN es ON es.lot_id=xs.lot_id WHERE xs.lot_id IS NULL OR es.id IS NULL OR es.kind<>'consumption_refund' OR es.available_delta<>xs.amount::numeric OR es.inference_held_delta<>0 OR es.consumed_delta<>-xs.amount::numeric OR es.issued_delta<>0
 ) THEN RAISE EXCEPTION 'funding_lot_reverse_refund' USING ERRCODE='23514'; END IF;
END $$;
CREATE FUNCTION fl_validate_legacy_refund(oid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE o funding_wallet_operations;f refund_orders; BEGIN
 SELECT * INTO STRICT o FROM funding_wallet_operations WHERE id=oid;SELECT * INTO STRICT f FROM refund_orders WHERE id=o.source_id AND state='approved';
 IF NOT EXISTS(SELECT 1 FROM funding_lot_legacy_settlements WHERE settlement_id=f.settlement_id AND tenant_id=f.tenant_id) OR EXISTS(SELECT 1 FROM funding_lot_legacy_approved_refunds WHERE refund_order_id=f.id)
 OR (SELECT count(*) FROM funding_lots WHERE legacy_refund_order_id=f.id)<>1 OR (SELECT count(*) FROM funding_consumption_refund_lots WHERE refund_order_id=f.id)<>1
 OR NOT EXISTS(
  SELECT 1 FROM funding_lots l JOIN funding_consumption_refund_lots r ON r.lot_id=l.id JOIN funding_lot_events e ON e.lot_id=l.id AND e.wallet_operation_id=o.id
  WHERE l.legacy_refund_order_id=f.id AND l.tenant_id=f.tenant_id AND l.origin_kind='legacy_consumption_refund' AND l.issued_amount=f.amount AND l.refund_class='nonrefundable' AND l.fifo_at=o.created_at
  AND r.refund_order_id=f.id AND r.tenant_id=f.tenant_id AND r.origin='legacy_settlement' AND r.settlement_lot_id IS NULL AND r.allocation_index=1 AND r.amount=f.amount
  AND e.kind='legacy_consumption_refund' AND e.before_revision=0 AND e.before_available=0 AND e.before_held=0 AND e.before_consumed=0 AND e.available_delta=f.amount::numeric AND e.inference_held_delta=0 AND e.consumed_delta=0 AND e.issued_delta=f.amount::numeric
 ) THEN RAISE EXCEPTION 'funding_lot_legacy_refund' USING ERRCODE='23514'; END IF;
END $$;
CREATE FUNCTION fl_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'funding_lot_immutable' USING ERRCODE='23514'; END $$;
CREATE FUNCTION fl_stamp() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 NEW.created_xid:=pg_catalog.pg_current_xact_id();NEW.created_at:=pg_catalog.clock_timestamp();RETURN NEW;
END $$;

-- Existing funding validators retain the original 017/021 contracts, including
-- source, journal, exact lines, terminal commerce/capture audit and credited.
CREATE FUNCTION fl_source_manual(sid text) RETURNS fl_source_proof LANGUAGE plpgsql STABLE AS $$ DECLARE f funding_receipts;j journals; BEGIN
 SELECT * INTO STRICT f FROM funding_receipts WHERE id=sid AND source_kind='manual_credit';
 SELECT * INTO STRICT j FROM journals WHERE id=f.journal_id;
 IF NOT payment_legacy_funding_journal_valid(j.id) OR NOT EXISTS(SELECT 1 FROM credit_orders c JOIN audit_events a ON a.target=c.id AND a.action='credit.approved' WHERE c.id=f.source_id AND a.tenant_scope=f.tenant_id AND a.actor=c.approver AND a.request_id='' AND a.sanitized_diff=jsonb_build_object('amount',f.amount::text))
 THEN RAISE EXCEPTION 'funding_lot_manual_source' USING ERRCODE='23514'; END IF;
 RETURN (f.tenant_id,j.id,j.operation_id,f.amount,f.amount::numeric,0,0,f.amount::numeric,f.created_at,'nonrefundable')::fl_source_proof;
END $$;
CREATE FUNCTION fl_source_voucher(sid text) RETURNS fl_source_proof LANGUAGE plpgsql STABLE AS $$ DECLARE f funding_receipts;j journals; BEGIN
 SELECT * INTO STRICT f FROM funding_receipts WHERE id=sid AND source_kind='voucher_grant';SELECT * INTO STRICT j FROM journals WHERE id=f.journal_id;
 IF NOT payment_legacy_funding_journal_valid(j.id) OR NOT EXISTS(SELECT 1 FROM voucher_redemptions r JOIN audit_events a ON a.target=r.id AND a.action='voucher.redeemed' WHERE r.funding_receipt_id=f.id AND a.tenant_scope=f.tenant_id AND a.actor=r.actor AND a.sanitized_diff=jsonb_build_object('amount',f.amount::text,'funding_receipt_id',f.id))
 THEN RAISE EXCEPTION 'funding_lot_voucher_source' USING ERRCODE='23514'; END IF;
 RETURN (f.tenant_id,j.id,j.operation_id,f.amount,f.amount::numeric,0,0,f.amount::numeric,f.created_at,'nonrefundable')::fl_source_proof;
END $$;
CREATE FUNCTION fl_source_sandbox(sid text) RETURNS fl_source_proof LANGUAGE plpgsql STABLE AS $$ DECLARE f funding_receipts;j journals; BEGIN
 SELECT * INTO STRICT f FROM funding_receipts WHERE id=sid AND source_kind='sandbox_capture';SELECT * INTO STRICT j FROM journals WHERE id=f.journal_id;
 IF NOT payment_sandbox_journal_valid(j.id) THEN RAISE EXCEPTION 'funding_lot_sandbox_source' USING ERRCODE='23514'; END IF;
 RETURN (f.tenant_id,j.id,j.operation_id,f.amount,f.amount::numeric,0,0,f.amount::numeric,f.created_at,'sandbox_original')::fl_source_proof;
END $$;

CREATE FUNCTION fl_journal_lines(jid text,t text,da numeric,dh numeric,dr numeric,include_revenue boolean) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT (SELECT count(*) FROM journal_lines WHERE journal_id=jid)=CASE WHEN include_revenue THEN 3 ELSE 2 END
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=jid AND a.tenant_id=t AND a.kind='AVAILABLE' AND l.signed_delta::numeric=da)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=jid AND a.tenant_id=t AND a.kind='HELD' AND l.signed_delta::numeric=dh)
 AND (NOT include_revenue OR EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=jid AND a.tenant_id IS NULL AND a.kind='REVENUE' AND l.signed_delta::numeric=dr))
$$;
CREATE FUNCTION fl_source_reserve(sid text) RETURNS fl_source_proof LANGUAGE plpgsql STABLE AS $$ DECLARE v reservations;r request_records;j journals; BEGIN
 SELECT * INTO STRICT v FROM reservations WHERE id=sid;SELECT * INTO STRICT r FROM request_records WHERE id=v.request_id;
 SELECT * INTO STRICT j FROM journals WHERE operation_id=v.id;
 IF v.tenant_id<>r.tenant_id OR (j.tenant_id,j.kind,j.reference,j.actor,j.currency) IS DISTINCT FROM (v.tenant_id,'reserve',r.id,r.owner_id,'CNY') OR NOT fl_journal_lines(j.id,v.tenant_id,-v.amount::numeric,v.amount::numeric,0,false)
 OR NOT EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=v.id AND o.kind='reserved' AND o.tenant_id=v.tenant_id AND o.schema_version=1 AND o.sanitized_payload=jsonb_build_object('amount',v.amount::text,'request_id',r.id))
 OR NOT EXISTS(SELECT 1 FROM audit_events a WHERE a.action='billing.reserve' AND a.target=j.id AND a.actor=r.owner_id AND a.tenant_scope=v.tenant_id AND a.request_id=r.id AND a.sanitized_diff=jsonb_build_object('amount',v.amount::text))
 THEN RAISE EXCEPTION 'funding_lot_reserve_source' USING ERRCODE='23514'; END IF;
 RETURN (v.tenant_id,j.id,j.operation_id,v.amount,-v.amount::numeric,v.amount::numeric,0,0,NULL,NULL)::fl_source_proof;
END $$;
CREATE FUNCTION fl_source_finish(sid text) RETURNS fl_source_proof LANGUAGE plpgsql STABLE AS $$ DECLARE s settlements;v reservations;j journals; BEGIN
 SELECT * INTO STRICT s FROM settlements WHERE id=sid;SELECT * INTO STRICT v FROM reservations WHERE request_id=s.request_id;SELECT * INTO STRICT j FROM journals WHERE id=s.journal_id;
 IF s.tenant_id<>v.tenant_id OR s.charged_amount>v.amount OR s.refunded_amount::numeric<>coalesce((SELECT sum(f.amount::numeric) FROM refund_orders f WHERE f.settlement_id=s.id AND f.state='approved'),0)
 OR v.status<>s.status OR (j.tenant_id,j.operation_id,j.kind,j.reference,j.actor,j.currency) IS DISTINCT FROM (s.tenant_id,s.id,s.status,s.request_id,'system','CNY')
 OR NOT fl_journal_lines(j.id,s.tenant_id,v.amount::numeric-s.charged_amount::numeric,-v.amount::numeric,s.charged_amount::numeric,s.charged_amount>0)
 OR NOT EXISTS(SELECT 1 FROM request_records r WHERE r.id=s.request_id AND r.tenant_id=s.tenant_id AND r.billing_status=s.status)
 OR NOT EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=s.id AND o.kind=s.status AND o.tenant_id=s.tenant_id AND o.schema_version=1 AND o.sanitized_payload=jsonb_build_object('amount',s.charged_amount::text,'released',(v.amount::numeric-s.charged_amount::numeric)::text,'request_id',s.request_id))
 OR NOT EXISTS(SELECT 1 FROM audit_events a WHERE a.action='billing.'||s.status AND a.target=s.id AND a.actor='system' AND a.tenant_scope=s.tenant_id AND a.request_id=s.request_id AND a.sanitized_diff=jsonb_build_object('amount',s.charged_amount::text,'reason',s.reason))
 THEN RAISE EXCEPTION 'funding_lot_finish_source' USING ERRCODE='23514'; END IF;
 RETURN (s.tenant_id,j.id,j.operation_id,s.charged_amount,v.amount::numeric-s.charged_amount::numeric,-v.amount::numeric,s.charged_amount::numeric,0,NULL,NULL)::fl_source_proof;
END $$;
CREATE FUNCTION fl_source_refund(sid text,legacy boolean) RETURNS fl_source_proof LANGUAGE plpgsql STABLE AS $$ DECLARE f refund_orders;s settlements;j journals; BEGIN
 SELECT * INTO STRICT f FROM refund_orders WHERE id=sid AND state='approved';SELECT * INTO STRICT s FROM settlements WHERE id=f.settlement_id;SELECT * INTO STRICT j FROM journals WHERE id=f.journal_id;
 IF s.refunded_amount::numeric<>coalesce((SELECT sum(x.amount::numeric) FROM refund_orders x WHERE x.settlement_id=s.id AND x.state='approved'),0) OR s.refunded_amount>s.charged_amount
 THEN RAISE EXCEPTION 'funding_lot_refund_cumulative' USING ERRCODE='23514'; END IF;
 IF legacy AND NOT EXISTS(
  SELECT 1 FROM funding_lot_legacy_settlements l WHERE l.settlement_id=s.id
   AND l.refunded_amount_at_cutover::numeric=coalesce((SELECT sum(x.amount::numeric) FROM funding_lot_legacy_approved_refunds x WHERE x.settlement_id=s.id),0)
   AND s.refunded_amount::numeric=l.refunded_amount_at_cutover::numeric+coalesce((SELECT sum(x.amount::numeric) FROM refund_orders x WHERE x.settlement_id=s.id AND x.state='approved' AND NOT EXISTS(SELECT 1 FROM funding_lot_legacy_approved_refunds h WHERE h.refund_order_id=x.id)),0)
 ) THEN RAISE EXCEPTION 'funding_lot_legacy_refund_cumulative' USING ERRCODE='23514'; END IF;
 IF f.tenant_id<>s.tenant_id OR f.approver IS NULL OR f.approver=f.requester OR f.amount>s.charged_amount
 OR (j.tenant_id,j.operation_id,j.kind,j.reference,j.actor,j.currency) IS DISTINCT FROM (f.tenant_id,f.id,'refund',s.id,f.approver,'CNY')
 OR (SELECT count(*) FROM journal_lines WHERE journal_id=j.id)<>2
 OR NOT EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id=f.tenant_id AND a.kind='AVAILABLE' AND l.signed_delta=f.amount)
 OR NOT EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind='REVENUE' AND l.signed_delta=-f.amount)
 OR NOT EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=f.id AND o.kind='refunded' AND o.tenant_id=f.tenant_id AND o.schema_version=1 AND o.sanitized_payload=jsonb_build_object('amount',f.amount::text))
 OR NOT EXISTS(SELECT 1 FROM audit_events a WHERE a.action='refund.approved' AND a.target=f.id AND a.actor=f.approver AND a.tenant_scope=f.tenant_id AND a.request_id=s.request_id AND a.sanitized_diff=jsonb_build_object('amount',f.amount::text))
 THEN RAISE EXCEPTION 'funding_lot_refund_source' USING ERRCODE='23514'; END IF;
 RETURN (f.tenant_id,j.id,j.operation_id,f.amount,f.amount::numeric,0,CASE WHEN legacy THEN 0 ELSE -f.amount::numeric END,CASE WHEN legacy THEN f.amount::numeric ELSE 0 END,NULL,'nonrefundable')::fl_source_proof;
END $$;
CREATE FUNCTION fl_read_source(k text,sid text) RETURNS fl_source_proof LANGUAGE plpgsql STABLE AS $$ BEGIN
 IF k='manual_credit' THEN RETURN fl_source_manual(sid);
 ELSIF k='voucher_grant' THEN RETURN fl_source_voucher(sid);
 ELSIF k='sandbox_capture' THEN RETURN fl_source_sandbox(sid);
 ELSIF k='reserve' THEN RETURN fl_source_reserve(sid);
 ELSIF k='finish' THEN RETURN fl_source_finish(sid);
 ELSIF k='consumption_refund' THEN RETURN fl_source_refund(sid,false);
 ELSIF k='legacy_consumption_refund' THEN RETURN fl_source_refund(sid,true);
 ELSE RAISE EXCEPTION 'funding_lot_source_kind' USING ERRCODE='23514'; END IF;
END $$;

CREATE FUNCTION fl_validate_genesis(oid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE o funding_wallet_operations;e funding_lot_epoch;birth xid8; BEGIN
 SELECT * INTO STRICT o FROM funding_wallet_operations WHERE id=oid;e:=fl_require_epoch();
 SELECT fl_created_xid INTO STRICT birth FROM tenants WHERE id=o.tenant_id;
 IF o.seq<>0 OR o.source_id<>o.tenant_id OR o.epoch_id<>e.id OR o.journal_id IS NOT NULL OR o.journal_operation_id IS NOT NULL THEN RAISE EXCEPTION 'funding_lot_genesis_identity' USING ERRCODE='23514'; END IF;
 IF o.kind='tenant_genesis' THEN
  IF birth IS NULL OR birth IS DISTINCT FROM o.created_xid OR EXISTS(SELECT 1 FROM funding_lot_legacy_openings WHERE tenant_id=o.tenant_id) OR o.event_count<>0 OR o.available_delta<>0 OR o.held_delta<>0 OR o.consumed_delta<>0 OR o.issued_delta<>0 OR EXISTS(SELECT 1 FROM funding_lot_events WHERE wallet_operation_id=o.id)
  THEN RAISE EXCEPTION 'funding_lot_tenant_genesis' USING ERRCODE='23514'; END IF;
 ELSIF o.kind='epoch_opening' THEN
  IF birth IS NOT NULL OR NOT EXISTS(SELECT 1 FROM funding_lot_legacy_openings WHERE tenant_id=o.tenant_id AND kind='available')
  OR EXISTS(
   WITH xs AS (SELECT * FROM funding_lot_legacy_openings WHERE tenant_id=o.tenant_id),ls AS (SELECT * FROM funding_lots WHERE tenant_id=o.tenant_id AND legacy_opening_id IS NOT NULL)
   SELECT 1 FROM xs FULL JOIN ls ON ls.legacy_opening_id=xs.id WHERE xs.id IS NULL OR (xs.amount=0 AND ls.id IS NOT NULL) OR (xs.amount>0 AND (ls.id IS NULL OR ls.issued_amount<>xs.amount OR ls.origin_kind<>'legacy_'||xs.kind)) OR xs.epoch_id<>e.id OR xs.observed_at<>e.created_at
  ) OR EXISTS(
   WITH xs AS (SELECT l.*,a.kind opening_kind FROM funding_lots l JOIN funding_lot_legacy_openings a ON a.id=l.legacy_opening_id WHERE a.tenant_id=o.tenant_id),es AS (SELECT * FROM funding_lot_events WHERE wallet_operation_id=o.id)
   SELECT 1 FROM xs FULL JOIN es ON es.lot_id=xs.id WHERE xs.id IS NULL OR es.id IS NULL OR xs.fifo_at<>e.created_at OR xs.refund_class<>'nonrefundable' OR es.kind<>'legacy_opening' OR es.before_revision<>0 OR es.before_available<>0 OR es.before_held<>0 OR es.before_consumed<>0 OR es.consumed_delta<>0 OR es.issued_delta<>xs.issued_amount::numeric
    OR es.available_delta<>(CASE WHEN xs.opening_kind='available' THEN xs.issued_amount::numeric ELSE 0 END) OR es.inference_held_delta<>(CASE WHEN xs.opening_kind='reservation' THEN xs.issued_amount::numeric ELSE 0 END)
  ) OR EXISTS(
   WITH xs AS (SELECT a.*,l.id lot_id FROM funding_lot_legacy_openings a LEFT JOIN funding_lots l ON l.legacy_opening_id=a.id WHERE a.tenant_id=o.tenant_id AND a.kind='reservation'),rs AS (SELECT r.* FROM funding_reservation_lots r JOIN funding_wallet_legacy_sources s ON s.kind='reserve' AND s.source_id=r.reservation_id WHERE s.tenant_id=o.tenant_id)
   SELECT 1 FROM xs FULL JOIN rs ON rs.reservation_id=xs.reservation_id WHERE xs.id IS NULL OR (xs.amount=0 AND rs.id IS NOT NULL) OR (xs.amount>0 AND (rs.id IS NULL OR rs.lot_id<>xs.lot_id OR rs.amount<>xs.amount OR rs.allocation_index<>1 OR rs.tenant_id<>o.tenant_id))
  ) THEN RAISE EXCEPTION 'funding_lot_epoch_genesis' USING ERRCODE='23514'; END IF;
 ELSE RAISE EXCEPTION 'funding_lot_genesis_kind' USING ERRCODE='23514'; END IF;
END $$;

CREATE FUNCTION fl_check_wallet_operation(oid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE o funding_wallet_operations;p fl_source_proof;e funding_lot_epoch;da numeric;dh numeric;ds numeric;di numeric; BEGIN
 SELECT * INTO STRICT o FROM funding_wallet_operations WHERE id=oid;e:=fl_require_epoch();
 IF o.epoch_id<>e.id THEN RAISE EXCEPTION 'funding_lot_operation_epoch' USING ERRCODE='23514'; END IF;
 IF o.kind IN ('epoch_opening','tenant_genesis') THEN PERFORM fl_validate_genesis(o.id);
 ELSE
  p:=fl_read_source(o.kind,o.source_id);
  IF (o.tenant_id,o.journal_id,o.journal_operation_id,o.available_delta,o.held_delta,o.consumed_delta,o.issued_delta) IS DISTINCT FROM (p.tenant_id,p.journal_id,p.journal_operation_id,p.available_delta,p.held_delta,p.consumed_delta,p.issued_delta)
  THEN RAISE EXCEPTION 'funding_lot_operation_source' USING ERRCODE='23514'; END IF;
  IF NOT EXISTS(SELECT 1 FROM journals j WHERE j.id=o.journal_id AND j.tenant_id=o.tenant_id AND j.created_txid::numeric=o.created_xid::text::numeric)
  THEN RAISE EXCEPTION 'funding_lot_operation_transaction' USING ERRCODE='23514'; END IF;
  IF o.kind='sandbox_capture' THEN PERFORM fl_validate_sandbox_funding(o.id);
  ELSIF o.kind IN ('manual_credit','voucher_grant') THEN PERFORM fl_validate_funding(o.id);
  ELSE
   IF EXISTS(SELECT 1 FROM funding_wallet_legacy_sources WHERE kind=CASE WHEN o.kind='legacy_consumption_refund' THEN 'consumption_refund' ELSE o.kind END AND source_id=o.source_id) THEN RAISE EXCEPTION 'funding_lot_operation_legacy_overlap' USING ERRCODE='23514'; END IF;
   IF o.kind='reserve' THEN PERFORM fl_validate_reserve(o.id);
   ELSIF o.kind='finish' THEN PERFORM fl_validate_finish(o.id);
   ELSIF o.kind='consumption_refund' THEN PERFORM fl_validate_refund(o.id);
   ELSIF o.kind='legacy_consumption_refund' THEN PERFORM fl_validate_legacy_refund(o.id);
   ELSE RAISE EXCEPTION 'funding_lot_operation_kind' USING ERRCODE='23514'; END IF;
  END IF;
 END IF;
 SELECT coalesce(sum(available_delta),0),coalesce(sum(inference_held_delta),0),coalesce(sum(consumed_delta),0),coalesce(sum(issued_delta),0) INTO da,dh,ds,di FROM funding_lot_events WHERE wallet_operation_id=o.id;
 IF o.event_count<>(SELECT count(*) FROM funding_lot_events WHERE wallet_operation_id=o.id) OR (o.available_delta,o.held_delta,o.consumed_delta,o.issued_delta) IS DISTINCT FROM (da,dh,ds,di)
 OR EXISTS(SELECT 1 FROM funding_lot_events x WHERE x.wallet_operation_id=o.id AND (x.tenant_id<>o.tenant_id OR x.wallet_seq<>o.seq OR x.journal_id IS DISTINCT FROM o.journal_id OR x.operation_id IS DISTINCT FROM o.journal_operation_id))
 THEN RAISE EXCEPTION 'funding_lot_operation_effects' USING ERRCODE='23514'; END IF;
 PERFORM fl_check_wallet_head(o.tenant_id);PERFORM fl_check_wallet_account(o.tenant_id);
END $$;

CREATE FUNCTION fl_check_legacy_source(k text,sid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE l funding_wallet_legacy_sources;p fl_source_proof;r text;v text;s text; BEGIN
 SELECT * INTO STRICT l FROM funding_wallet_legacy_sources WHERE kind=k AND source_id=sid;p:=fl_read_source(k,sid);
 IF (l.tenant_id,l.journal_id,l.journal_operation_id,l.amount) IS DISTINCT FROM (p.tenant_id,p.journal_id,p.journal_operation_id,p.amount) OR EXISTS(SELECT 1 FROM funding_wallet_operations WHERE kind IN (k,CASE WHEN k='consumption_refund' THEN 'legacy_consumption_refund' ELSE k END) AND source_id=sid)
 THEN RAISE EXCEPTION 'funding_lot_legacy_source' USING ERRCODE='23514'; END IF;
 IF k='reserve' THEN SELECT request_id,id INTO r,v FROM reservations WHERE id=sid;
 ELSIF k='finish' THEN SELECT x.request_id,a.id,x.id INTO r,v,s FROM settlements x JOIN reservations a ON a.request_id=x.request_id WHERE x.id=sid;
 ELSIF k='consumption_refund' THEN SELECT x.request_id,a.id,x.id INTO r,v,s FROM refund_orders f JOIN settlements x ON x.id=f.settlement_id JOIN reservations a ON a.request_id=x.request_id WHERE f.id=sid;
 END IF;
 IF l.request_id IS DISTINCT FROM r OR l.reservation_id IS DISTINCT FROM v OR l.settlement_id IS DISTINCT FROM s THEN RAISE EXCEPTION 'funding_lot_legacy_source_roots' USING ERRCODE='23514'; END IF;
END $$;

CREATE FUNCTION fl_check_sandbox_receipt(rid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE f funding_receipts;l funding_lot_legacy_sandbox_receipts;o text; BEGIN
 SELECT * INTO STRICT f FROM funding_receipts WHERE id=rid AND source_kind='sandbox_capture';SELECT * INTO l FROM funding_lot_legacy_sandbox_receipts WHERE receipt_id=rid;
 IF l.receipt_id IS NOT NULL THEN
  IF (l.tenant_id,l.capture_id,l.amount,l.journal_id) IS DISTINCT FROM (f.tenant_id,f.source_id,f.amount,f.journal_id) OR EXISTS(SELECT 1 FROM funding_wallet_operations WHERE kind='sandbox_capture' AND source_id=rid) OR EXISTS(SELECT 1 FROM funding_lots WHERE funding_receipt_id=rid) OR NOT payment_sandbox_journal_valid(f.journal_id)
  THEN RAISE EXCEPTION 'funding_lot_sandbox_legacy_receipt' USING ERRCODE='23514'; END IF;
 ELSE
  SELECT id INTO o FROM funding_wallet_operations WHERE kind='sandbox_capture' AND source_id=rid;
  IF o IS NULL THEN RAISE EXCEPTION 'funding_lot_missing_sandbox_operation' USING ERRCODE='23514'; END IF;
  PERFORM fl_check_wallet_operation(o);
 END IF;
END $$;
CREATE FUNCTION fl_check_receipt(rid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE f funding_receipts;l funding_lot_legacy_receipts;o text; BEGIN
 SELECT * INTO STRICT f FROM funding_receipts WHERE id=rid;
 IF f.source_kind='sandbox_capture' THEN PERFORM fl_check_sandbox_receipt(rid);RETURN; END IF;
 SELECT * INTO l FROM funding_lot_legacy_receipts WHERE receipt_id=rid;
 IF l.receipt_id IS NOT NULL THEN
  IF (l.tenant_id,l.source_kind,l.source_id,l.amount,l.journal_id) IS DISTINCT FROM (f.tenant_id,f.source_kind,f.source_id,f.amount,f.journal_id) OR EXISTS(SELECT 1 FROM funding_wallet_operations WHERE kind=f.source_kind AND source_id=rid) OR EXISTS(SELECT 1 FROM funding_lots WHERE funding_receipt_id=rid)
  THEN RAISE EXCEPTION 'funding_lot_legacy_receipt' USING ERRCODE='23514'; END IF;
  PERFORM fl_check_legacy_source(f.source_kind,rid);
 ELSE
  SELECT id INTO o FROM funding_wallet_operations WHERE kind=f.source_kind AND source_id=rid;
  IF o IS NULL THEN RAISE EXCEPTION 'funding_lot_missing_funding_operation' USING ERRCODE='23514'; END IF;
  PERFORM fl_check_wallet_operation(o);
 END IF;
END $$;
CREATE FUNCTION fl_check_reservation(rid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE v reservations;o text;legacy boolean; BEGIN
 SELECT * INTO STRICT v FROM reservations WHERE id=rid;legacy:=EXISTS(SELECT 1 FROM funding_wallet_legacy_sources WHERE kind='reserve' AND source_id=rid);
 IF NOT EXISTS(SELECT 1 FROM request_records r WHERE r.id=v.request_id AND r.tenant_id=v.tenant_id AND r.billing_status=v.status) THEN RAISE EXCEPTION 'funding_lot_reservation_request_state' USING ERRCODE='23514'; END IF;
 IF legacy THEN
  PERFORM fl_check_legacy_source('reserve',rid);
  IF EXISTS(SELECT 1 FROM funding_lot_legacy_settlements WHERE reservation_id=rid) THEN
   IF EXISTS(SELECT 1 FROM funding_reservation_lots WHERE reservation_id=rid) THEN RAISE EXCEPTION 'funding_lot_historical_terminal_allocation' USING ERRCODE='23514'; END IF;
  ELSE
   SELECT id INTO o FROM funding_wallet_operations WHERE kind='epoch_opening' AND tenant_id=v.tenant_id;
   IF o IS NULL THEN RAISE EXCEPTION 'funding_lot_missing_opening_operation' USING ERRCODE='23514'; END IF;PERFORM fl_validate_genesis(o);
  END IF;
 ELSE
  SELECT id INTO o FROM funding_wallet_operations WHERE kind='reserve' AND source_id=rid;
  IF o IS NULL THEN RAISE EXCEPTION 'funding_lot_missing_reserve_operation' USING ERRCODE='23514'; END IF;PERFORM fl_check_wallet_operation(o);
 END IF;
 IF v.status IN ('reserved','pending_reconciliation') THEN
  IF EXISTS(SELECT 1 FROM settlements WHERE request_id=v.request_id) OR EXISTS(SELECT 1 FROM funding_settlement_lots b JOIN funding_reservation_lots a ON a.id=b.reservation_lot_id WHERE a.reservation_id=rid)
  THEN RAISE EXCEPTION 'funding_lot_unfinished_terminal_edges' USING ERRCODE='23514'; END IF;
 ELSE
  SELECT id INTO o FROM settlements WHERE request_id=v.request_id;
  IF o IS NULL THEN RAISE EXCEPTION 'funding_lot_missing_settlement_source' USING ERRCODE='23514'; END IF;
  IF NOT EXISTS(SELECT 1 FROM funding_lot_legacy_settlements WHERE settlement_id=o) THEN
   SELECT id INTO o FROM funding_wallet_operations WHERE kind='finish' AND source_id=o;
   IF o IS NULL THEN RAISE EXCEPTION 'funding_lot_missing_finish_operation' USING ERRCODE='23514'; END IF;PERFORM fl_check_wallet_operation(o);
  END IF;
 END IF;
END $$;
CREATE FUNCTION fl_check_settlement(sid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE s settlements;l funding_lot_legacy_settlements;o text; BEGIN
 SELECT * INTO STRICT s FROM settlements WHERE id=sid;SELECT * INTO l FROM funding_lot_legacy_settlements WHERE settlement_id=sid;
 IF l.settlement_id IS NOT NULL THEN
  IF (l.tenant_id,l.request_id,l.charged_amount,l.journal_id) IS DISTINCT FROM (s.tenant_id,s.request_id,s.charged_amount,s.journal_id) OR s.refunded_amount<l.refunded_amount_at_cutover OR EXISTS(SELECT 1 FROM funding_settlement_lots WHERE settlement_id=sid)
  THEN RAISE EXCEPTION 'funding_lot_legacy_settlement' USING ERRCODE='23514'; END IF;
  PERFORM fl_check_legacy_source('finish',sid);
 ELSE
  SELECT id INTO o FROM funding_wallet_operations WHERE kind='finish' AND source_id=sid;
  IF o IS NULL THEN RAISE EXCEPTION 'funding_lot_missing_finish_operation' USING ERRCODE='23514'; END IF;PERFORM fl_check_wallet_operation(o);
 END IF;
END $$;
CREATE FUNCTION fl_check_refund(fid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE f refund_orders;l funding_lot_legacy_approved_refunds;o text;k text; BEGIN
 SELECT * INTO STRICT f FROM refund_orders WHERE id=fid;
 IF f.state='pending' THEN
  IF f.journal_id IS NOT NULL OR f.approver IS NOT NULL OR EXISTS(SELECT 1 FROM funding_wallet_operations WHERE kind IN ('consumption_refund','legacy_consumption_refund') AND source_id=fid) OR EXISTS(SELECT 1 FROM funding_consumption_refund_lots WHERE refund_order_id=fid)
  THEN RAISE EXCEPTION 'funding_lot_pending_refund_effects' USING ERRCODE='23514'; END IF;RETURN;
 END IF;
 SELECT * INTO l FROM funding_lot_legacy_approved_refunds WHERE refund_order_id=fid;
 IF l.refund_order_id IS NOT NULL THEN
  IF (l.tenant_id,l.settlement_id,l.amount,l.journal_id) IS DISTINCT FROM (f.tenant_id,f.settlement_id,f.amount,f.journal_id) OR EXISTS(SELECT 1 FROM funding_consumption_refund_lots WHERE refund_order_id=fid) OR EXISTS(SELECT 1 FROM funding_lots WHERE legacy_refund_order_id=fid)
  THEN RAISE EXCEPTION 'funding_lot_legacy_approved_refund' USING ERRCODE='23514'; END IF;PERFORM fl_check_legacy_source('consumption_refund',fid);
 ELSE
  k:=CASE WHEN EXISTS(SELECT 1 FROM funding_lot_legacy_settlements WHERE settlement_id=f.settlement_id) THEN 'legacy_consumption_refund' ELSE 'consumption_refund' END;
  SELECT id INTO o FROM funding_wallet_operations WHERE kind=k AND source_id=fid;
  IF o IS NULL THEN RAISE EXCEPTION 'funding_lot_missing_refund_operation' USING ERRCODE='23514'; END IF;PERFORM fl_check_wallet_operation(o);
 END IF;
END $$;

CREATE FUNCTION fl_check_wallet_journal(jid text) RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE j journals;r text;o text; BEGIN
 SELECT * INTO STRICT j FROM journals WHERE id=jid;
 IF NOT EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=jid AND a.kind IN ('AVAILABLE','HELD')) THEN RETURN; END IF;
 IF j.kind IN ('credit','voucher_grant','sandbox_capture') THEN
  SELECT id INTO r FROM funding_receipts WHERE journal_id=jid;
  IF r IS NULL THEN RAISE EXCEPTION 'funding_lot_orphan_funding_journal' USING ERRCODE='23514'; END IF;PERFORM fl_check_receipt(r);
 ELSIF j.kind='reserve' THEN
  SELECT id INTO r FROM reservations WHERE id=j.operation_id;
  IF r IS NULL THEN RAISE EXCEPTION 'funding_lot_orphan_reserve_journal' USING ERRCODE='23514'; END IF;PERFORM fl_check_reservation(r);
 ELSIF j.kind IN ('settled','released') THEN
  SELECT id INTO r FROM settlements WHERE journal_id=jid AND id=j.operation_id;
  IF r IS NULL THEN RAISE EXCEPTION 'funding_lot_orphan_finish_journal' USING ERRCODE='23514'; END IF;PERFORM fl_check_settlement(r);
 ELSIF j.kind='refund' THEN
  SELECT id INTO r FROM refund_orders WHERE journal_id=jid AND id=j.operation_id AND state='approved';
  IF r IS NULL THEN RAISE EXCEPTION 'funding_lot_orphan_refund_journal' USING ERRCODE='23514'; END IF;PERFORM fl_check_refund(r);
 ELSE RAISE EXCEPTION 'funding_lot_unknown_wallet_journal' USING ERRCODE='23514'; END IF;
END $$;

CREATE FUNCTION fl_check_epoch() RETURNS void LANGUAGE plpgsql STABLE AS $$ DECLARE e funding_lot_epoch;rec record; BEGIN
 e:=fl_require_epoch();
 IF e.tenant_count<>(SELECT count(*) FROM funding_lot_legacy_openings WHERE kind='available') OR e.receipt_count<>(SELECT count(*) FROM funding_lot_legacy_receipts)
 OR e.settlement_count<>(SELECT count(*) FROM funding_lot_legacy_settlements) OR e.approved_refund_count<>(SELECT count(*) FROM funding_lot_legacy_approved_refunds)
 OR e.opening_count<>(SELECT count(*) FROM funding_lot_legacy_openings) OR e.legacy_source_count<>(SELECT count(*) FROM funding_wallet_legacy_sources)
 OR e.available_total<>coalesce((SELECT sum(amount::numeric) FROM funding_lot_legacy_openings WHERE kind='available'),0) OR e.held_total<>coalesce((SELECT sum(amount::numeric) FROM funding_lot_legacy_openings WHERE kind='reservation'),0)
 OR e.tenant_count<>(SELECT count(*) FROM funding_wallet_operations WHERE kind='epoch_opening')
 OR EXISTS(
  WITH old_tenants AS (SELECT id FROM tenants WHERE fl_created_xid IS NULL),openings AS (SELECT tenant_id FROM funding_lot_legacy_openings WHERE kind='available')
  SELECT 1 FROM old_tenants t FULL JOIN openings a ON a.tenant_id=t.id WHERE t.id IS NULL OR a.tenant_id IS NULL
 )
 THEN RAISE EXCEPTION 'funding_lot_epoch_counts' USING ERRCODE='23514'; END IF;
 IF EXISTS(
  SELECT 1 FROM funding_lot_legacy_receipts l JOIN funding_receipts f ON f.id=l.receipt_id
  WHERE (l.tenant_id,l.source_kind,l.source_id,l.amount,l.journal_id) IS DISTINCT FROM (f.tenant_id,f.source_kind,f.source_id,f.amount,f.journal_id)
   OR NOT EXISTS(SELECT 1 FROM funding_wallet_legacy_sources s WHERE s.kind=l.source_kind AND s.source_id=l.receipt_id AND s.tenant_id=l.tenant_id AND s.journal_id=l.journal_id)
 ) OR EXISTS(
  WITH all_sandbox AS (SELECT * FROM funding_lot_legacy_receipts WHERE source_kind='sandbox_capture')
  SELECT 1 FROM all_sandbox a FULL JOIN funding_lot_legacy_sandbox_receipts b ON b.receipt_id=a.receipt_id WHERE a.receipt_id IS NULL OR b.receipt_id IS NULL OR (a.epoch_id,a.tenant_id,a.source_id,a.amount,a.journal_id) IS DISTINCT FROM (b.epoch_id,b.tenant_id,b.capture_id,b.amount,b.journal_id)
 ) OR EXISTS(
  SELECT 1 FROM funding_lot_legacy_settlements l JOIN settlements s ON s.id=l.settlement_id JOIN reservations v ON v.request_id=s.request_id
  WHERE (l.tenant_id,l.reservation_id,l.request_id,l.charged_amount,l.journal_id) IS DISTINCT FROM (s.tenant_id,v.id,s.request_id,s.charged_amount,s.journal_id)
   OR l.refunded_amount_at_cutover<>coalesce((SELECT sum(f.amount::numeric) FROM funding_lot_legacy_approved_refunds f WHERE f.settlement_id=s.id),0)
   OR NOT EXISTS(SELECT 1 FROM funding_wallet_legacy_sources x WHERE x.kind='finish' AND x.source_id=s.id)
 ) OR EXISTS(
  SELECT 1 FROM funding_lot_legacy_approved_refunds l JOIN refund_orders f ON f.id=l.refund_order_id
  WHERE f.state<>'approved' OR (l.tenant_id,l.settlement_id,l.amount,l.journal_id) IS DISTINCT FROM (f.tenant_id,f.settlement_id,f.amount,f.journal_id)
   OR NOT EXISTS(SELECT 1 FROM funding_lot_legacy_settlements WHERE settlement_id=f.settlement_id)
   OR NOT EXISTS(SELECT 1 FROM funding_wallet_legacy_sources WHERE kind='consumption_refund' AND source_id=f.id)
 ) THEN RAISE EXCEPTION 'funding_lot_epoch_membership' USING ERRCODE='23514'; END IF;
 IF EXISTS(
  WITH expected AS (
   SELECT v.* FROM funding_wallet_legacy_sources x JOIN reservations v ON x.kind='reserve' AND x.source_id=v.id
   WHERE NOT EXISTS(SELECT 1 FROM funding_lot_legacy_settlements s WHERE s.reservation_id=v.id)
  ),actual AS (SELECT * FROM funding_lot_legacy_openings WHERE kind='reservation')
  SELECT 1 FROM expected v FULL JOIN actual a ON a.reservation_id=v.id WHERE v.id IS NULL OR a.id IS NULL OR (a.tenant_id,a.amount) IS DISTINCT FROM (v.tenant_id,v.amount)
 ) OR EXISTS(
  SELECT 1 FROM funding_lot_legacy_openings a LEFT JOIN ledger_accounts c ON c.id=a.account_id
  WHERE a.observed_at<>e.created_at OR (a.account_id IS NOT NULL AND (c.id IS NULL OR c.tenant_id<>a.tenant_id OR c.kind<>(CASE WHEN a.kind='available' THEN 'AVAILABLE' ELSE 'HELD' END)))
 ) OR EXISTS(
  SELECT 1 FROM funding_lot_legacy_openings a WHERE a.kind='available' AND a.amount::numeric<>coalesce((SELECT sum(l.signed_delta::numeric) FROM funding_wallet_legacy_sources s JOIN journal_lines l ON l.journal_id=s.journal_id JOIN ledger_accounts c ON c.id=l.account_id WHERE s.tenant_id=a.tenant_id AND c.tenant_id=a.tenant_id AND c.kind='AVAILABLE'),0)
 ) OR EXISTS(
  SELECT 1 FROM funding_lot_legacy_openings a WHERE a.kind='available' AND coalesce((SELECT sum(h.amount::numeric) FROM funding_lot_legacy_openings h WHERE h.tenant_id=a.tenant_id AND h.kind='reservation'),0)<>coalesce((SELECT sum(l.signed_delta::numeric) FROM funding_wallet_legacy_sources s JOIN journal_lines l ON l.journal_id=s.journal_id JOIN ledger_accounts c ON c.id=l.account_id WHERE s.tenant_id=a.tenant_id AND c.tenant_id=a.tenant_id AND c.kind='HELD'),0)
 ) THEN RAISE EXCEPTION 'funding_lot_epoch_opening_basis' USING ERRCODE='23514'; END IF;
 FOR rec IN SELECT kind,source_id FROM funding_wallet_legacy_sources ORDER BY kind COLLATE "C",source_id COLLATE "C" LOOP PERFORM fl_check_legacy_source(rec.kind,rec.source_id); END LOOP;
 -- Reverse membership: a legacy source cannot omit its corresponding snapshot.
 IF EXISTS(SELECT 1 FROM funding_wallet_legacy_sources s WHERE
  (s.kind IN ('manual_credit','voucher_grant','sandbox_capture') AND NOT EXISTS(SELECT 1 FROM funding_lot_legacy_receipts WHERE receipt_id=s.source_id AND source_kind=s.kind))
  OR (s.kind='finish' AND NOT EXISTS(SELECT 1 FROM funding_lot_legacy_settlements WHERE settlement_id=s.source_id))
  OR (s.kind='consumption_refund' AND NOT EXISTS(SELECT 1 FROM funding_lot_legacy_approved_refunds WHERE refund_order_id=s.source_id)))
 THEN RAISE EXCEPTION 'funding_lot_epoch_reverse_membership' USING ERRCODE='23514'; END IF;
 FOR rec IN SELECT id FROM tenants ORDER BY id COLLATE "C" LOOP PERFORM fl_check_wallet_head(rec.id);PERFORM fl_check_wallet_account(rec.id); END LOOP;
 FOR rec IN SELECT id FROM funding_receipts ORDER BY id COLLATE "C" LOOP PERFORM fl_check_receipt(rec.id); END LOOP;
 FOR rec IN SELECT id FROM reservations ORDER BY id COLLATE "C" LOOP PERFORM fl_check_reservation(rec.id); END LOOP;
 FOR rec IN SELECT id FROM settlements ORDER BY id COLLATE "C" LOOP PERFORM fl_check_settlement(rec.id); END LOOP;
 FOR rec IN SELECT id FROM refund_orders ORDER BY id COLLATE "C" LOOP PERFORM fl_check_refund(rec.id); END LOOP;
 FOR rec IN SELECT id FROM funding_wallet_operations ORDER BY tenant_id COLLATE "C",seq LOOP PERFORM fl_check_wallet_operation(rec.id); END LOOP;
 FOR rec IN SELECT id FROM funding_lots ORDER BY id COLLATE "C" LOOP PERFORM fl_check_lot(rec.id); END LOOP;
 FOR rec IN SELECT j.id FROM journals j WHERE EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind IN ('AVAILABLE','HELD')) ORDER BY j.id COLLATE "C" LOOP PERFORM fl_check_wallet_journal(rec.id); END LOOP;
END $$;

CREATE FUNCTION fl_maintenance_insert_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF EXISTS(SELECT 1 FROM funding_lot_epoch WHERE singleton) THEN RAISE EXCEPTION 'funding_lot_epoch_already_issued' USING ERRCODE='23514'; END IF;RETURN NEW;
END $$;
CREATE FUNCTION fl_new_tenant() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE e funding_lot_epoch;oid text; BEGIN
 e:=fl_require_epoch();oid:=pg_catalog.gen_random_uuid()::text;
 INSERT INTO funding_wallet_operations(id,tenant_id,epoch_id,seq,kind,source_id,event_count,available_delta,held_delta,consumed_delta,issued_delta)
  VALUES(oid,NEW.id,e.id,0,'tenant_genesis',NEW.id,0,0,0,0,0);
 INSERT INTO funding_wallet_heads(tenant_id,epoch_id,tail_seq,tail_operation_id) VALUES(NEW.id,e.id,0,oid);
 RETURN NEW;
END $$;

CREATE FUNCTION fl_new_fact_constraint() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE oid text;lid text;sid text;k text; BEGIN
 IF TG_TABLE_NAME='funding_lot_epoch' THEN PERFORM fl_check_epoch();RETURN NULL; END IF;
 IF TG_TABLE_NAME='funding_wallet_heads' THEN PERFORM fl_check_wallet_head(NEW.tenant_id);RETURN NULL; END IF;
 IF TG_TABLE_NAME='funding_wallet_operations' THEN oid:=NEW.id;
 ELSIF TG_TABLE_NAME='funding_lot_events' THEN oid:=NEW.wallet_operation_id;lid:=NEW.lot_id;
 ELSIF TG_TABLE_NAME='funding_lot_balances' THEN lid:=NEW.lot_id;
 ELSIF TG_TABLE_NAME='funding_lots' THEN
  lid:=NEW.id;
  IF NEW.funding_receipt_id IS NOT NULL THEN PERFORM fl_check_receipt(NEW.funding_receipt_id);
  ELSIF NEW.legacy_refund_order_id IS NOT NULL THEN PERFORM fl_check_refund(NEW.legacy_refund_order_id);
  ELSE SELECT id INTO oid FROM funding_wallet_operations WHERE tenant_id=NEW.tenant_id AND kind='epoch_opening'; END IF;
 ELSIF TG_TABLE_NAME='funding_reservation_lots' THEN PERFORM fl_check_reservation(NEW.reservation_id);lid:=NEW.lot_id;
 ELSIF TG_TABLE_NAME='funding_settlement_lots' THEN PERFORM fl_check_settlement(NEW.settlement_id);SELECT lot_id INTO lid FROM funding_reservation_lots WHERE id=NEW.reservation_lot_id;
 ELSIF TG_TABLE_NAME='funding_consumption_refund_lots' THEN PERFORM fl_check_refund(NEW.refund_order_id);lid:=NEW.lot_id;
 END IF;
 IF oid IS NOT NULL THEN PERFORM fl_check_wallet_operation(oid); END IF;
 IF lid IS NOT NULL THEN PERFORM fl_check_lot(lid); END IF;
 RETURN NULL;
END $$;
CREATE FUNCTION fl_source_constraint() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE rid text;jid text; BEGIN
 PERFORM fl_require_epoch();
 IF TG_TABLE_NAME='funding_receipts' THEN PERFORM fl_check_receipt(NEW.id);
 ELSIF TG_TABLE_NAME='request_records' THEN SELECT id INTO rid FROM reservations WHERE request_id=NEW.id;IF rid IS NULL THEN RAISE EXCEPTION 'funding_lot_request_without_reservation' USING ERRCODE='23514'; END IF;PERFORM fl_check_reservation(rid);
 ELSIF TG_TABLE_NAME='reservations' THEN PERFORM fl_check_reservation(NEW.id);
 ELSIF TG_TABLE_NAME='settlements' THEN PERFORM fl_check_settlement(NEW.id);
 ELSIF TG_TABLE_NAME='refund_orders' THEN PERFORM fl_check_refund(NEW.id);
 ELSIF TG_TABLE_NAME='journals' THEN PERFORM fl_check_wallet_journal(NEW.id);
 ELSIF TG_TABLE_NAME='journal_lines' THEN PERFORM fl_check_wallet_journal(NEW.journal_id);
 ELSIF TG_TABLE_NAME='ledger_accounts' THEN IF NEW.tenant_id IS NOT NULL AND NEW.kind IN ('AVAILABLE','HELD') THEN PERFORM fl_check_wallet_account(NEW.tenant_id); END IF;
 ELSIF TG_TABLE_NAME='projection_outbox' THEN
  IF NEW.kind IN ('credited','reserved','settled','released','refunded') THEN SELECT id INTO jid FROM journals WHERE operation_id=NEW.operation_id;IF jid IS NULL THEN RAISE EXCEPTION 'funding_lot_orphan_wallet_outbox' USING ERRCODE='23514'; END IF;PERFORM fl_check_wallet_journal(jid); END IF;
 END IF;RETURN NULL;
END $$;

-- Fixed installation inventory; runtime role/schema checker pins every body,
-- owner, argument/result type, search_path, trigger and dependency separately.
DO $install$ DECLARE t text;BEGIN
 FOREACH t IN ARRAY ARRAY['funding_lot_epoch','funding_lot_runtime_attestations','funding_lot_legacy_receipts','funding_lot_legacy_sandbox_receipts','funding_lot_legacy_settlements','funding_lot_legacy_approved_refunds','funding_lot_legacy_openings','funding_wallet_legacy_sources','funding_wallet_operations','funding_lots','funding_lot_events','funding_reservation_lots','funding_settlement_lots','funding_consumption_refund_lots'] LOOP
  EXECUTE format('CREATE TRIGGER fl_immutable BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION fl_immutable()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['funding_lot_runtime_attestations','funding_lot_legacy_receipts','funding_lot_legacy_sandbox_receipts','funding_lot_legacy_settlements','funding_lot_legacy_approved_refunds','funding_lot_legacy_openings','funding_wallet_legacy_sources'] LOOP
  EXECUTE format('CREATE TRIGGER fl_initial_epoch_only BEFORE INSERT ON %I FOR EACH ROW EXECUTE FUNCTION fl_maintenance_insert_guard()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['funding_wallet_operations','funding_lot_events'] LOOP
  EXECUTE format('CREATE TRIGGER a_fl_stamp BEFORE INSERT ON %I FOR EACH ROW EXECUTE FUNCTION fl_stamp()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['funding_reservation_lots','funding_settlement_lots','funding_consumption_refund_lots'] LOOP
  EXECUTE format('CREATE TRIGGER fl_allocation BEFORE INSERT ON %I FOR EACH ROW EXECUTE FUNCTION fl_guard_allocation()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['funding_lot_epoch','funding_wallet_operations','funding_lots','funding_lot_events','funding_reservation_lots','funding_settlement_lots','funding_consumption_refund_lots'] LOOP
  EXECUTE format('CREATE CONSTRAINT TRIGGER fl_complete AFTER INSERT ON %I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fl_new_fact_constraint()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['funding_wallet_heads','funding_lot_balances'] LOOP
  EXECUTE format('CREATE CONSTRAINT TRIGGER fl_complete AFTER INSERT OR UPDATE ON %I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fl_new_fact_constraint()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['funding_receipts','journals','journal_lines','projection_outbox'] LOOP
  EXECUTE format('CREATE CONSTRAINT TRIGGER fl_wallet_source AFTER INSERT ON %I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fl_source_constraint()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['request_records','reservations','settlements','refund_orders','ledger_accounts'] LOOP
  EXECUTE format('CREATE CONSTRAINT TRIGGER fl_wallet_source AFTER INSERT OR UPDATE ON %I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fl_source_constraint()',t);
 END LOOP;
END $install$;
CREATE TRIGGER fl_head_guard BEFORE INSERT OR UPDATE OR DELETE ON funding_wallet_heads FOR EACH ROW EXECUTE FUNCTION fl_guard_head();
CREATE TRIGGER b_fl_event_guard BEFORE INSERT ON funding_lot_events FOR EACH ROW EXECUTE FUNCTION fl_guard_event();
CREATE TRIGGER fl_balance_guard BEFORE INSERT OR UPDATE OR DELETE ON funding_lot_balances FOR EACH ROW EXECUTE FUNCTION fl_guard_balance();
CREATE TRIGGER fl_tenant_genesis AFTER INSERT ON tenants FOR EACH ROW EXECUTE FUNCTION fl_new_tenant();

-- Search_path fixed on a static function signature list, not PUBLIC resolution.
DO $paths$ DECLARE sig text;schema_name text:=current_schema();BEGIN
 FOREACH sig IN ARRAY ARRAY[
 'fl_uuid(text)','fl_tenant_birth_guard()','fl_require_epoch()','fl_immutable()','fl_stamp()','fl_guard_head()','fl_guard_event()','fl_guard_balance()','fl_guard_allocation()',
 'fl_check_wallet_head(text)','fl_check_wallet_account(text)','fl_check_lot(text)','fl_validate_new_funding(text)','fl_validate_funding(text)','fl_validate_sandbox_funding(text)',
 'fl_validate_reserve(text)','fl_validate_finish(text)','fl_validate_refund(text)','fl_validate_legacy_refund(text)','fl_source_manual(text)','fl_source_voucher(text)','fl_source_sandbox(text)',
 'fl_journal_lines(text,text,numeric,numeric,numeric,boolean)','fl_source_reserve(text)','fl_source_finish(text)','fl_source_refund(text,boolean)','fl_read_source(text,text)',
 'fl_validate_genesis(text)','fl_check_wallet_operation(text)','fl_check_legacy_source(text,text)','fl_check_sandbox_receipt(text)','fl_check_receipt(text)','fl_check_reservation(text)',
 'fl_check_settlement(text)','fl_check_refund(text)','fl_check_wallet_journal(text)','fl_check_epoch()','fl_maintenance_insert_guard()','fl_new_tenant()','fl_new_fact_constraint()','fl_source_constraint()'
 ] LOOP
  EXECUTE format('ALTER FUNCTION %I.%s SET search_path TO %I,pg_catalog,pg_temp',schema_name,sig,schema_name);
  EXECUTE format('REVOKE ALL ON FUNCTION %I.%s FROM PUBLIC',schema_name,sig);
 END LOOP;
END $paths$;
