-- C0 ISOLATED MECHANISM PROTOTYPE. NOT A MIGRATION; NEVER LOAD IN AN APP SCHEMA.
-- Caller creates a random schema owned by an actual isolated owner LOGIN and
-- sets search_path to that schema,pg_catalog. No production object is read.
-- IDs are short fixture text, not the production canonical UUID contract.
-- c0p_sources/journals/openings are TEST WITNESSES, not real source validators.
-- This prototype proves sequence/CAS/effect/replay mechanisms only.

CREATE TABLE c0p_epoch(id text PRIMARY KEY, singleton boolean NOT NULL UNIQUE DEFAULT true CHECK(singleton));
CREATE TABLE c0p_tenants(id text PRIMARY KEY);
CREATE TABLE c0p_sources(
 id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES c0p_tenants,
 kind text NOT NULL CHECK(kind IN ('epoch_opening','tenant_genesis','manual_credit','voucher_grant','sandbox_capture','reserve','finish','consumption_refund','legacy_consumption_refund')),
 amount bigint NOT NULL CHECK(amount>=0),parent_id text REFERENCES c0p_sources,
 journal_id text UNIQUE,fifo_at timestamptz NOT NULL,
 CHECK((kind IN ('epoch_opening','tenant_genesis'))=(journal_id IS NULL)),
 CHECK((kind IN ('finish','consumption_refund'))=(parent_id IS NOT NULL)),
 CHECK(kind NOT IN ('manual_credit','voucher_grant','sandbox_capture','consumption_refund','legacy_consumption_refund') OR amount>0),
 UNIQUE(tenant_id,id)
);
CREATE UNIQUE INDEX c0p_one_finish ON c0p_sources(parent_id) WHERE kind='finish';
CREATE TABLE c0p_journals(
 id text PRIMARY KEY,tenant_id text NOT NULL,source_id text NOT NULL UNIQUE,
 available_delta numeric NOT NULL,held_delta numeric NOT NULL,revenue_delta numeric NOT NULL,clearing_delta numeric NOT NULL,
 CHECK(available_delta+held_delta+revenue_delta+clearing_delta=0),
 FOREIGN KEY(tenant_id,source_id) REFERENCES c0p_sources(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
ALTER TABLE c0p_sources ADD FOREIGN KEY(journal_id) REFERENCES c0p_journals DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE c0p_operations(
 id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES c0p_tenants,epoch_id text NOT NULL REFERENCES c0p_epoch,
 seq bigint NOT NULL CHECK(seq>=0),previous_id text UNIQUE,previous_seq bigint,source_id text NOT NULL UNIQUE,
 event_count bigint NOT NULL CHECK(event_count>=0),
 available_delta numeric NOT NULL,held_delta numeric NOT NULL,consumed_delta numeric NOT NULL,issued_delta numeric NOT NULL,
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),
 UNIQUE(tenant_id,seq),UNIQUE(tenant_id,epoch_id,seq,id),UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,source_id) REFERENCES c0p_sources(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 CHECK((seq=0 AND previous_id IS NULL AND previous_seq IS NULL) OR (seq>0 AND previous_id IS NOT NULL AND previous_seq IS NOT NULL AND previous_seq::numeric=seq::numeric-1))
);
ALTER TABLE c0p_operations ADD FOREIGN KEY(tenant_id,epoch_id,previous_seq,previous_id) REFERENCES c0p_operations(tenant_id,epoch_id,seq,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE c0p_heads(
 tenant_id text PRIMARY KEY REFERENCES c0p_tenants,epoch_id text NOT NULL REFERENCES c0p_epoch,
 tail_seq bigint NOT NULL CHECK(tail_seq>=0),tail_id text NOT NULL,
 FOREIGN KEY(tenant_id,epoch_id,tail_seq,tail_id) REFERENCES c0p_operations(tenant_id,epoch_id,seq,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE c0p_openings(
 id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES c0p_tenants,source_id text NOT NULL REFERENCES c0p_sources,
 amount bigint NOT NULL CHECK(amount>=0),kind text NOT NULL CHECK(kind IN ('available','reservation')),reservation_id text,
 CHECK((kind='reservation')=(reservation_id IS NOT NULL)),
 UNIQUE(tenant_id,id)
);
CREATE UNIQUE INDEX c0p_one_available_opening ON c0p_openings(tenant_id) WHERE kind='available';
CREATE UNIQUE INDEX c0p_one_held_opening ON c0p_openings(reservation_id) WHERE kind='reservation';
CREATE TABLE c0p_lots(
 id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES c0p_tenants,origin_source_id text NOT NULL REFERENCES c0p_sources,
 opening_id text UNIQUE REFERENCES c0p_openings,issued bigint NOT NULL CHECK(issued>0),
 fifo_at timestamptz NOT NULL,refund_class text NOT NULL CHECK(refund_class IN ('nonrefundable','sandbox_original')),
 UNIQUE(tenant_id,id)
);
CREATE UNIQUE INDEX c0p_one_lot_per_new_source ON c0p_lots(origin_source_id) WHERE opening_id IS NULL;
CREATE TABLE c0p_events(
 id text PRIMARY KEY,tenant_id text NOT NULL,wallet_operation_id text NOT NULL,lot_id text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),previous_id text UNIQUE,
 before_a bigint NOT NULL CHECK(before_a>=0),before_h bigint NOT NULL CHECK(before_h>=0),before_s bigint NOT NULL CHECK(before_s>=0),
 after_a bigint NOT NULL CHECK(after_a>=0),after_h bigint NOT NULL CHECK(after_h>=0),after_s bigint NOT NULL CHECK(after_s>=0),
 a_delta numeric NOT NULL,h_delta numeric NOT NULL,s_delta numeric NOT NULL,issued_delta numeric NOT NULL CHECK(issued_delta>=0),
 CHECK(after_a::numeric=before_a::numeric+a_delta AND after_h::numeric=before_h::numeric+h_delta AND after_s::numeric=before_s::numeric+s_delta),
 CHECK(a_delta+h_delta+s_delta=issued_delta),CHECK((revision=1)=(previous_id IS NULL)),
 UNIQUE(lot_id,revision),UNIQUE(wallet_operation_id,lot_id),UNIQUE(lot_id,id),
 FOREIGN KEY(tenant_id,wallet_operation_id) REFERENCES c0p_operations(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(tenant_id,lot_id) REFERENCES c0p_lots(tenant_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(lot_id,previous_id) REFERENCES c0p_events(lot_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE c0p_balances(
 lot_id text PRIMARY KEY REFERENCES c0p_lots,tenant_id text NOT NULL,
 a bigint NOT NULL CHECK(a>=0),h bigint NOT NULL CHECK(h>=0),s bigint NOT NULL CHECK(s>=0),revision bigint NOT NULL CHECK(revision>0),last_event_id text NOT NULL,
 FOREIGN KEY(tenant_id,lot_id) REFERENCES c0p_lots(tenant_id,id),
 FOREIGN KEY(lot_id,last_event_id) REFERENCES c0p_events(lot_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE c0p_reservation_lots(
 id text PRIMARY KEY,tenant_id text NOT NULL,reservation_id text NOT NULL,lot_id text NOT NULL,allocation_index bigint NOT NULL CHECK(allocation_index>0),amount bigint NOT NULL CHECK(amount>0),
 UNIQUE(reservation_id,lot_id),UNIQUE(reservation_id,allocation_index),
 FOREIGN KEY(tenant_id,reservation_id) REFERENCES c0p_sources(tenant_id,id),FOREIGN KEY(tenant_id,lot_id) REFERENCES c0p_lots(tenant_id,id)
);
CREATE TABLE c0p_settlement_lots(
 id text PRIMARY KEY,tenant_id text NOT NULL,source_id text NOT NULL,reservation_lot_id text NOT NULL UNIQUE REFERENCES c0p_reservation_lots,
 charged bigint NOT NULL CHECK(charged>=0),released bigint NOT NULL CHECK(released>=0),
 FOREIGN KEY(tenant_id,source_id) REFERENCES c0p_sources(tenant_id,id)
);
CREATE TABLE c0p_refund_lots(
 id text PRIMARY KEY,tenant_id text NOT NULL,source_id text NOT NULL,settlement_lot_id text NOT NULL REFERENCES c0p_settlement_lots,
 allocation_index bigint NOT NULL CHECK(allocation_index>0),amount bigint NOT NULL CHECK(amount>0),
 UNIQUE(source_id,settlement_lot_id),UNIQUE(source_id,allocation_index),FOREIGN KEY(tenant_id,source_id) REFERENCES c0p_sources(tenant_id,id)
);

-- Edge guards have no worker dependency: worker has no INSERT on these tables.
CREATE FUNCTION c0p_guard_allocation() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE s c0p_sources; p text; t text; BEGIN
 IF TG_TABLE_NAME='c0p_reservation_lots' THEN
  SELECT * INTO STRICT s FROM c0p_sources WHERE id=NEW.reservation_id;
  IF s.kind<>'reserve' OR s.tenant_id<>NEW.tenant_id THEN RAISE EXCEPTION 'c0p reserve edge source' USING ERRCODE='23514'; END IF;
 ELSIF TG_TABLE_NAME='c0p_settlement_lots' THEN
  SELECT * INTO STRICT s FROM c0p_sources WHERE id=NEW.source_id;
  SELECT reservation_id,tenant_id INTO p,t FROM c0p_reservation_lots WHERE id=NEW.reservation_lot_id;
  IF s.kind<>'finish' OR s.parent_id<>p OR s.tenant_id<>NEW.tenant_id OR t<>NEW.tenant_id THEN RAISE EXCEPTION 'c0p finish edge source' USING ERRCODE='23514'; END IF;
 ELSE
  SELECT * INTO STRICT s FROM c0p_sources WHERE id=NEW.source_id;
  SELECT source_id,tenant_id INTO p,t FROM c0p_settlement_lots WHERE id=NEW.settlement_lot_id;
  IF s.kind<>'consumption_refund' OR s.parent_id<>p OR s.tenant_id<>NEW.tenant_id OR t<>NEW.tenant_id THEN RAISE EXCEPTION 'c0p refund edge source' USING ERRCODE='23514'; END IF;
 END IF; RETURN NEW;
END $$;
CREATE TRIGGER c0p_guard_allocation BEFORE INSERT ON c0p_reservation_lots FOR EACH ROW EXECUTE FUNCTION c0p_guard_allocation();
CREATE TRIGGER c0p_guard_allocation BEFORE INSERT ON c0p_settlement_lots FOR EACH ROW EXECUTE FUNCTION c0p_guard_allocation();
CREATE TRIGGER c0p_guard_allocation BEFORE INSERT ON c0p_refund_lots FOR EACH ROW EXECUTE FUNCTION c0p_guard_allocation();

CREATE FUNCTION c0p_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 RAISE EXCEPTION 'c0p immutable' USING ERRCODE='23514'; END $$;
CREATE FUNCTION c0p_operation_stamp() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 NEW.created_xid:=pg_catalog.pg_current_xact_id(); RETURN NEW; END $$;
CREATE TRIGGER c0p_operation_stamp BEFORE INSERT ON c0p_operations FOR EACH ROW EXECUTE FUNCTION c0p_operation_stamp();
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['c0p_epoch','c0p_tenants','c0p_sources','c0p_journals','c0p_operations','c0p_openings','c0p_lots','c0p_events','c0p_reservation_lots','c0p_settlement_lots','c0p_refund_lots'] LOOP
  EXECUTE format('CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION c0p_immutable()',t);
 END LOOP;
END $$;

CREATE FUNCTION c0p_guard_head() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE o c0p_operations; BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'c0p head delete' USING ERRCODE='23514'; END IF;
 SELECT * INTO o FROM c0p_operations WHERE id=NEW.tail_id;
 IF NOT FOUND OR o.tenant_id<>NEW.tenant_id OR o.epoch_id<>NEW.epoch_id OR o.seq<>NEW.tail_seq OR o.created_xid<>pg_catalog.pg_current_xact_id()
 THEN RAISE EXCEPTION 'c0p head operation' USING ERRCODE='23514'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.tail_seq<>0 OR o.previous_id IS NOT NULL THEN RAISE EXCEPTION 'c0p head genesis' USING ERRCODE='23514'; END IF;
 ELSE
  IF NEW.tenant_id<>OLD.tenant_id OR NEW.epoch_id<>OLD.epoch_id OR OLD.tail_seq=9223372036854775807
    OR NEW.tail_seq::numeric<>OLD.tail_seq::numeric+1 OR o.previous_id<>OLD.tail_id
  THEN RAISE EXCEPTION 'c0p head CAS' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER c0p_guard_head BEFORE INSERT OR UPDATE OR DELETE ON c0p_heads FOR EACH ROW EXECUTE FUNCTION c0p_guard_head();
CREATE FUNCTION c0p_guard_balance() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE e c0p_events; o c0p_operations; BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'c0p cache delete' USING ERRCODE='23514'; END IF;
 SELECT * INTO e FROM c0p_events WHERE id=NEW.last_event_id;
 SELECT * INTO o FROM c0p_operations WHERE id=e.wallet_operation_id;
 IF e.id IS NULL OR o.created_xid<>pg_catalog.pg_current_xact_id() OR NEW.tenant_id<>e.tenant_id OR NEW.lot_id<>e.lot_id OR NEW.revision<>e.revision
   OR NEW.a<>e.after_a OR NEW.h<>e.after_h OR NEW.s<>e.after_s THEN RAISE EXCEPTION 'c0p cache effect' USING ERRCODE='23514'; END IF;
 IF TG_OP='INSERT' THEN
  IF e.revision<>1 OR e.previous_id IS NOT NULL OR e.before_a<>0 OR e.before_h<>0 OR e.before_s<>0 THEN RAISE EXCEPTION 'c0p cache initial' USING ERRCODE='23514'; END IF;
 ELSE
  IF NEW.lot_id<>OLD.lot_id OR NEW.tenant_id<>OLD.tenant_id OR e.revision::numeric<>OLD.revision::numeric+1 OR e.previous_id<>OLD.last_event_id
   OR e.before_a<>OLD.a OR e.before_h<>OLD.h OR e.before_s<>OLD.s THEN RAISE EXCEPTION 'c0p cache predecessor' USING ERRCODE='23514'; END IF;
 END IF; RETURN NEW;
END $$;
CREATE TRIGGER c0p_guard_balance BEFORE INSERT OR UPDATE OR DELETE ON c0p_balances FOR EACH ROW EXECUTE FUNCTION c0p_guard_balance();

CREATE FUNCTION c0p_check_head(t text) RETURNS void LANGUAGE plpgsql AS $$ DECLARE h c0p_heads; n numeric; lo bigint; hi bigint; BEGIN
 SELECT * INTO h FROM c0p_heads WHERE tenant_id=t;
 SELECT count(*)::numeric,min(seq),max(seq) INTO n,lo,hi FROM c0p_operations WHERE tenant_id=t;
 IF h.tenant_id IS NULL OR n<>h.tail_seq::numeric+1 OR lo<>0 OR hi<>h.tail_seq OR NOT EXISTS(SELECT 1 FROM c0p_operations o WHERE o.id=h.tail_id AND o.seq=hi AND o.tenant_id=t)
  OR EXISTS(SELECT 1 FROM c0p_operations o LEFT JOIN c0p_operations p ON p.id=o.previous_id WHERE o.tenant_id=t AND o.seq>0 AND (p.id IS NULL OR p.tenant_id<>t OR p.epoch_id<>o.epoch_id OR p.seq::numeric<>o.seq::numeric-1))
 THEN RAISE EXCEPTION 'c0p incomplete wallet prefix' USING ERRCODE='23514'; END IF;
END $$;

-- Independent before-state: sums persisted effects in earlier wallet seq,
-- never trusts cache or an allocation's own selected lot order.
CREATE FUNCTION c0p_validate_reserve(sid text) RETURNS void LANGUAGE plpgsql AS $$ DECLARE src c0p_sources; op c0p_operations; BEGIN
 SELECT * INTO STRICT src FROM c0p_sources WHERE id=sid;
 SELECT * INTO STRICT op FROM c0p_operations WHERE source_id=sid;
 IF src.kind<>'reserve' THEN RAISE EXCEPTION 'c0p reserve source' USING ERRCODE='23514'; END IF;
 IF EXISTS(
  WITH before_lots AS (
   SELECT l.id,l.fifo_at,coalesce(sum(e.a_delta) FILTER(WHERE o.seq<op.seq),0) a
   FROM c0p_lots l LEFT JOIN c0p_events e ON e.lot_id=l.id LEFT JOIN c0p_operations o ON o.id=e.wallet_operation_id
   WHERE l.tenant_id=src.tenant_id GROUP BY l.id,l.fifo_at
  ), candidates AS (
   SELECT *,coalesce(sum(a) OVER(ORDER BY fifo_at,id COLLATE "C" ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) prior
   FROM before_lots WHERE a>0
  ), expected0 AS (SELECT id,greatest(least(a,src.amount::numeric-prior),0) q,fifo_at FROM candidates),
  expected AS (SELECT id,q,row_number() OVER(ORDER BY fifo_at,id COLLATE "C") idx FROM expected0 WHERE q>0),
  actual AS (SELECT lot_id,amount,allocation_index,tenant_id FROM c0p_reservation_lots WHERE reservation_id=sid)
  SELECT 1 FROM expected e FULL JOIN actual a ON a.lot_id=e.id
   WHERE e.id IS NULL OR a.lot_id IS NULL OR a.amount::numeric<>e.q OR a.allocation_index<>e.idx OR a.tenant_id<>src.tenant_id
 ) OR (SELECT coalesce(sum(amount::numeric),0) FROM c0p_reservation_lots WHERE reservation_id=sid)<>src.amount
 OR EXISTS(
  SELECT 1 FROM c0p_reservation_lots a FULL JOIN (SELECT * FROM c0p_events WHERE wallet_operation_id=op.id) e ON e.lot_id=a.lot_id
   AND a.reservation_id=sid WHERE (a.reservation_id=sid OR e.id IS NOT NULL)
   AND (a.id IS NULL OR e.id IS NULL OR e.a_delta<>-a.amount::numeric OR e.h_delta<>a.amount::numeric OR e.s_delta<>0 OR e.issued_delta<>0)
 ) THEN RAISE EXCEPTION 'c0p FIFO reserve' USING ERRCODE='23514'; END IF;
END $$;

CREATE FUNCTION c0p_validate_finish(sid text) RETURNS void LANGUAGE plpgsql AS $$ DECLARE src c0p_sources; parent c0p_sources; op c0p_operations; BEGIN
 SELECT * INTO STRICT src FROM c0p_sources WHERE id=sid;
 SELECT * INTO STRICT parent FROM c0p_sources WHERE id=src.parent_id;
 SELECT * INTO STRICT op FROM c0p_operations WHERE source_id=sid;
 IF src.kind<>'finish' OR parent.kind<>'reserve' OR parent.tenant_id<>src.tenant_id OR src.amount>parent.amount
  OR NOT EXISTS(SELECT 1 FROM c0p_operations p WHERE p.source_id=parent.id AND p.seq<op.seq AND p.tenant_id=op.tenant_id)
 THEN RAISE EXCEPTION 'c0p finish source' USING ERRCODE='23514'; END IF;
 IF EXISTS(
  WITH alloc AS (SELECT a.*,coalesce(sum(amount::numeric) OVER(ORDER BY allocation_index ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) prior FROM c0p_reservation_lots a WHERE reservation_id=parent.id),
  expected AS (SELECT *,greatest(least(amount::numeric,src.amount::numeric-prior),0) c FROM alloc),
  actual AS (SELECT * FROM c0p_settlement_lots WHERE source_id=sid)
  SELECT 1 FROM expected a FULL JOIN actual b ON b.reservation_lot_id=a.id
   WHERE a.id IS NULL OR b.id IS NULL OR b.tenant_id<>src.tenant_id OR b.charged::numeric<>a.c OR b.released::numeric<>a.amount::numeric-a.c
 ) OR EXISTS(
  WITH x AS (SELECT a.lot_id,a.amount,b.charged,b.released FROM c0p_settlement_lots b JOIN c0p_reservation_lots a ON a.id=b.reservation_lot_id WHERE b.source_id=sid),
  e AS (SELECT * FROM c0p_events WHERE wallet_operation_id=op.id)
  SELECT 1 FROM x FULL JOIN e ON e.lot_id=x.lot_id WHERE x.lot_id IS NULL OR e.id IS NULL OR e.a_delta<>x.released::numeric OR e.h_delta<>-x.amount::numeric OR e.s_delta<>x.charged::numeric OR e.issued_delta<>0
 ) THEN RAISE EXCEPTION 'c0p finish prefix' USING ERRCODE='23514'; END IF;
END $$;

CREATE FUNCTION c0p_validate_refund(sid text) RETURNS void LANGUAGE plpgsql AS $$ DECLARE src c0p_sources; parent c0p_sources; op c0p_operations; BEGIN
 SELECT * INTO STRICT src FROM c0p_sources WHERE id=sid;
 SELECT * INTO STRICT parent FROM c0p_sources WHERE id=src.parent_id;
 SELECT * INTO STRICT op FROM c0p_operations WHERE source_id=sid;
 IF src.kind<>'consumption_refund' OR parent.kind<>'finish' OR parent.tenant_id<>src.tenant_id OR NOT EXISTS(SELECT 1 FROM c0p_operations p WHERE p.source_id=parent.id AND p.seq<op.seq AND p.tenant_id=op.tenant_id)
 THEN RAISE EXCEPTION 'c0p refund source' USING ERRCODE='23514'; END IF;
 IF EXISTS(
  WITH edges AS (
   SELECT b.id,a.allocation_index,b.charged::numeric-coalesce((SELECT sum(r.amount::numeric) FROM c0p_refund_lots r JOIN c0p_operations po ON po.source_id=r.source_id WHERE r.settlement_lot_id=b.id AND po.seq<op.seq AND po.tenant_id=op.tenant_id),0) remain
   FROM c0p_settlement_lots b JOIN c0p_reservation_lots a ON a.id=b.reservation_lot_id WHERE b.source_id=parent.id
  ), candidates AS (SELECT *,coalesce(sum(remain) OVER(ORDER BY allocation_index DESC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) prior FROM edges WHERE remain>0),
  expected0 AS (SELECT *,greatest(least(remain,src.amount::numeric-prior),0) q FROM candidates),
  expected AS (SELECT *,row_number() OVER(ORDER BY allocation_index DESC) idx FROM expected0 WHERE q>0),
  actual AS (SELECT * FROM c0p_refund_lots WHERE source_id=sid)
  SELECT 1 FROM expected e FULL JOIN actual a ON a.settlement_lot_id=e.id WHERE e.id IS NULL OR a.id IS NULL OR a.tenant_id<>src.tenant_id OR a.amount::numeric<>e.q OR a.allocation_index<>e.idx
 ) OR (SELECT coalesce(sum(amount::numeric),0) FROM c0p_refund_lots WHERE source_id=sid)<>src.amount
 OR EXISTS(
  WITH x AS (SELECT a.lot_id,r.amount FROM c0p_refund_lots r JOIN c0p_settlement_lots b ON b.id=r.settlement_lot_id JOIN c0p_reservation_lots a ON a.id=b.reservation_lot_id WHERE r.source_id=sid),
  e AS (SELECT * FROM c0p_events WHERE wallet_operation_id=op.id)
  SELECT 1 FROM x FULL JOIN e ON e.lot_id=x.lot_id WHERE x.lot_id IS NULL OR e.id IS NULL OR e.a_delta<>x.amount::numeric OR e.h_delta<>0 OR e.s_delta<>-x.amount::numeric OR e.issued_delta<>0
 ) THEN RAISE EXCEPTION 'c0p reverse refund prefix' USING ERRCODE='23514'; END IF;
END $$;

CREATE FUNCTION c0p_validate_initial(sid text, sandbox_only boolean) RETURNS void LANGUAGE plpgsql AS $$ DECLARE src c0p_sources; op c0p_operations; BEGIN
 SELECT * INTO STRICT src FROM c0p_sources WHERE id=sid;
 SELECT * INTO STRICT op FROM c0p_operations WHERE source_id=sid;
 IF sandbox_only AND src.kind<>'sandbox_capture' THEN RAISE EXCEPTION 'c0p sandbox kind' USING ERRCODE='23514'; END IF;
 IF sandbox_only THEN
  IF op.seq=0 OR (SELECT count(*) FROM c0p_lots WHERE origin_source_id=sid)<>1 OR EXISTS(
   WITH x AS (SELECT * FROM c0p_lots WHERE origin_source_id=sid),e AS (SELECT * FROM c0p_events WHERE wallet_operation_id=op.id)
   SELECT 1 FROM x FULL JOIN e ON e.lot_id=x.id WHERE x.id IS NULL OR e.id IS NULL OR x.tenant_id<>src.tenant_id OR x.fifo_at<>src.fifo_at
    OR x.refund_class<>'sandbox_original' OR x.issued<>src.amount OR x.opening_id IS NOT NULL
    OR e.revision<>1 OR e.before_a<>0 OR e.before_h<>0 OR e.before_s<>0 OR e.a_delta<>src.amount::numeric OR e.h_delta<>0 OR e.s_delta<>0 OR e.issued_delta<>src.amount::numeric
  ) THEN RAISE EXCEPTION 'c0p sandbox initial mapping' USING ERRCODE='23514'; END IF;
  RETURN;
 END IF;
 IF src.kind IN ('epoch_opening','tenant_genesis') THEN
  IF op.seq<>0 OR src.amount<>0 OR src.parent_id IS NOT NULL THEN RAISE EXCEPTION 'c0p genesis source' USING ERRCODE='23514'; END IF;
  IF src.kind='tenant_genesis' AND (EXISTS(SELECT 1 FROM c0p_openings WHERE tenant_id=src.tenant_id) OR EXISTS(SELECT 1 FROM c0p_lots WHERE origin_source_id=sid)) THEN RAISE EXCEPTION 'c0p empty tenant genesis' USING ERRCODE='23514'; END IF;
  IF src.kind='epoch_opening' AND (NOT EXISTS(SELECT 1 FROM c0p_openings WHERE tenant_id=src.tenant_id AND source_id=sid AND kind='available') OR EXISTS(
   SELECT 1 FROM c0p_openings a FULL JOIN c0p_lots l ON l.opening_id=a.id WHERE (a.source_id=sid OR l.origin_source_id=sid)
   AND (a.id IS NULL OR a.source_id<>sid OR (a.amount>0 AND (l.id IS NULL OR l.issued<>a.amount)) OR (a.amount=0 AND l.id IS NOT NULL))
  )) THEN RAISE EXCEPTION 'c0p opening completeness' USING ERRCODE='23514'; END IF;
 ELSE
  IF src.kind NOT IN ('manual_credit','voucher_grant','sandbox_capture','legacy_consumption_refund') OR op.seq=0 OR (SELECT count(*) FROM c0p_lots WHERE origin_source_id=sid)<>1 THEN RAISE EXCEPTION 'c0p funding source' USING ERRCODE='23514'; END IF;
 END IF;
 IF EXISTS(
  WITH x AS (
   SELECT l.*,a.kind opening_kind FROM c0p_lots l LEFT JOIN c0p_openings a ON a.id=l.opening_id WHERE l.origin_source_id=sid
  ), e AS (SELECT * FROM c0p_events WHERE wallet_operation_id=op.id)
  SELECT 1 FROM x FULL JOIN e ON e.lot_id=x.id WHERE x.id IS NULL OR e.id IS NULL OR x.tenant_id<>src.tenant_id OR x.fifo_at<>src.fifo_at
   OR x.refund_class<>CASE WHEN src.kind='sandbox_capture' THEN 'sandbox_original' ELSE 'nonrefundable' END
   OR (src.kind NOT IN ('epoch_opening','tenant_genesis') AND (x.issued<>src.amount OR x.opening_id IS NOT NULL))
   OR e.revision<>1 OR e.before_a<>0 OR e.before_h<>0 OR e.before_s<>0 OR e.s_delta<>0 OR e.issued_delta<>x.issued::numeric
   OR e.a_delta<>CASE WHEN x.opening_kind='reservation' THEN 0 ELSE x.issued::numeric END
   OR e.h_delta<>CASE WHEN x.opening_kind='reservation' THEN x.issued::numeric ELSE 0 END
 ) THEN RAISE EXCEPTION 'c0p initial lot mapping' USING ERRCODE='23514'; END IF;
END $$;
CREATE FUNCTION c0p_validate_sandbox(sid text) RETURNS void LANGUAGE plpgsql AS $$ BEGIN PERFORM c0p_validate_initial(sid,true); END $$;
CREATE FUNCTION c0p_validate_app_initial(sid text) RETURNS void LANGUAGE plpgsql AS $$ BEGIN PERFORM c0p_validate_initial(sid,false); END $$;

CREATE FUNCTION c0p_check_operation(oid text) RETURNS void LANGUAGE plpgsql AS $$ DECLARE op c0p_operations; src c0p_sources; j c0p_journals; r numeric; da numeric; dh numeric; ds numeric; di numeric; BEGIN
 SELECT * INTO STRICT op FROM c0p_operations WHERE id=oid;
 SELECT * INTO STRICT src FROM c0p_sources WHERE id=op.source_id;
 IF op.tenant_id<>src.tenant_id OR (op.seq=0)<>(src.kind IN ('epoch_opening','tenant_genesis')) THEN RAISE EXCEPTION 'c0p op/source' USING ERRCODE='23514'; END IF;
 IF src.kind='sandbox_capture' THEN PERFORM c0p_validate_sandbox(src.id);
 ELSIF src.kind='reserve' THEN PERFORM c0p_validate_reserve(src.id);
 ELSIF src.kind='finish' THEN PERFORM c0p_validate_finish(src.id);
 ELSIF src.kind='consumption_refund' THEN PERFORM c0p_validate_refund(src.id);
 ELSE PERFORM c0p_validate_app_initial(src.id); END IF;
 SELECT coalesce(sum(a_delta),0),coalesce(sum(h_delta),0),coalesce(sum(s_delta),0),coalesce(sum(issued_delta),0) INTO da,dh,ds,di FROM c0p_events WHERE wallet_operation_id=op.id;
 IF op.event_count<>(SELECT count(*) FROM c0p_events WHERE wallet_operation_id=op.id) OR op.available_delta<>da OR op.held_delta<>dh OR op.consumed_delta<>ds OR op.issued_delta<>di THEN RAISE EXCEPTION 'c0p missing operation effects' USING ERRCODE='23514'; END IF;
 IF src.journal_id IS NOT NULL THEN
  SELECT * INTO STRICT j FROM c0p_journals WHERE id=src.journal_id;
  IF src.kind='finish' THEN SELECT amount::numeric INTO r FROM c0p_sources WHERE id=src.parent_id; ELSE r:=src.amount; END IF;
  IF j.source_id<>src.id OR j.tenant_id<>src.tenant_id OR j.available_delta<>da OR j.held_delta<>dh
   OR j.revenue_delta<>(CASE WHEN src.kind='finish' THEN src.amount::numeric WHEN src.kind='consumption_refund' THEN -src.amount::numeric WHEN src.kind='legacy_consumption_refund' THEN -src.amount::numeric ELSE 0 END)
   OR j.clearing_delta<>(CASE WHEN src.kind IN ('manual_credit','voucher_grant','sandbox_capture') THEN -src.amount::numeric ELSE 0 END)
   OR (src.kind='reserve' AND (da<>-r OR dh<>r OR ds<>0 OR di<>0))
   OR (src.kind='finish' AND (da<>r-src.amount::numeric OR dh<>-r OR ds<>src.amount::numeric OR di<>0))
   OR (src.kind='consumption_refund' AND (da<>r OR dh<>0 OR ds<>-r OR di<>0))
   OR (src.kind IN ('manual_credit','voucher_grant','sandbox_capture','legacy_consumption_refund') AND (da<>r OR dh<>0 OR ds<>0 OR di<>r))
  THEN RAISE EXCEPTION 'c0p journal/source/effects' USING ERRCODE='23514'; END IF;
 END IF;
 PERFORM c0p_check_head(op.tenant_id);
END $$;

CREATE FUNCTION c0p_check_lot(lid text) RETURNS void LANGUAGE plpgsql AS $$ DECLARE l c0p_lots; b c0p_balances; last c0p_events; BEGIN
 SELECT * INTO STRICT l FROM c0p_lots WHERE id=lid;
 IF EXISTS(
  WITH e AS (
   SELECT x.*,o.seq,row_number() OVER(ORDER BY o.seq) rn,
    lag(x.id) OVER(ORDER BY o.seq) prev,
    coalesce(sum(a_delta) OVER(ORDER BY o.seq ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) ba,
    coalesce(sum(h_delta) OVER(ORDER BY o.seq ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) bh,
    coalesce(sum(s_delta) OVER(ORDER BY o.seq ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) bs
   FROM c0p_events x JOIN c0p_operations o ON o.id=x.wallet_operation_id WHERE x.lot_id=lid
  ) SELECT 1 FROM e WHERE revision<>rn OR previous_id IS DISTINCT FROM prev OR before_a::numeric<>ba OR before_h::numeric<>bh OR before_s::numeric<>bs
   OR after_a::numeric+after_h::numeric+after_s::numeric<>l.issued::numeric OR issued_delta<>CASE WHEN rn=1 THEN l.issued::numeric ELSE 0 END
 ) THEN RAISE EXCEPTION 'c0p independent lot chain' USING ERRCODE='23514'; END IF;
 SELECT * INTO last FROM c0p_events WHERE lot_id=lid ORDER BY revision DESC LIMIT 1;
 SELECT * INTO b FROM c0p_balances WHERE lot_id=lid;
 IF last.id IS NULL OR b.lot_id IS NULL OR b.tenant_id<>l.tenant_id OR b.last_event_id<>last.id OR b.revision<>last.revision OR b.a<>last.after_a OR b.h<>last.after_h OR b.s<>last.after_s
 THEN RAISE EXCEPTION 'c0p independent cache' USING ERRCODE='23514'; END IF;
END $$;

CREATE FUNCTION c0p_deferred() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE v jsonb:=to_jsonb(NEW); sid text; oid text; lid text; BEGIN
 IF TG_TABLE_NAME='c0p_tenants' THEN PERFORM c0p_check_head(NEW.id); RETURN NULL; END IF;
 IF TG_TABLE_NAME='c0p_heads' THEN PERFORM c0p_check_head(NEW.tenant_id); RETURN NULL; END IF;
 IF TG_TABLE_NAME='c0p_sources' THEN sid:=NEW.id;
 ELSIF TG_TABLE_NAME='c0p_operations' THEN oid:=NEW.id;
 ELSIF TG_TABLE_NAME='c0p_events' THEN oid:=NEW.wallet_operation_id; lid:=NEW.lot_id;
 ELSIF TG_TABLE_NAME='c0p_lots' THEN sid:=NEW.origin_source_id; lid:=NEW.id;
 ELSIF TG_TABLE_NAME='c0p_balances' THEN lid:=NEW.lot_id;
 ELSIF TG_TABLE_NAME='c0p_reservation_lots' THEN sid:=NEW.reservation_id; lid:=NEW.lot_id;
 ELSE sid:=NEW.source_id; END IF;
 IF sid IS NOT NULL THEN SELECT id INTO oid FROM c0p_operations WHERE source_id=sid; IF oid IS NULL THEN RAISE EXCEPTION 'c0p source has no wallet witness' USING ERRCODE='23514'; END IF; END IF;
 IF oid IS NOT NULL THEN PERFORM c0p_check_operation(oid); END IF;
 IF lid IS NOT NULL THEN PERFORM c0p_check_lot(lid); END IF;
 RETURN NULL;
END $$;
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['c0p_tenants','c0p_sources','c0p_journals','c0p_operations','c0p_openings','c0p_lots','c0p_events','c0p_reservation_lots','c0p_settlement_lots','c0p_refund_lots'] LOOP
  EXECUTE format('CREATE CONSTRAINT TRIGGER proof AFTER INSERT ON %I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION c0p_deferred()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['c0p_heads','c0p_balances'] LOOP
  EXECUTE format('CREATE CONSTRAINT TRIGGER proof AFTER INSERT OR UPDATE ON %I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION c0p_deferred()',t);
 END LOOP;
END $$;

-- No repair and no writes: independent full-prefix scan (fixture only).
CREATE FUNCTION c0p_verify() RETURNS void LANGUAGE plpgsql AS $$ DECLARE rec record; BEGIN
 FOR rec IN SELECT id FROM c0p_tenants ORDER BY id COLLATE "C" LOOP PERFORM c0p_check_head(rec.id); END LOOP;
 IF EXISTS(SELECT 1 FROM c0p_sources s LEFT JOIN c0p_operations o ON o.source_id=s.id WHERE o.id IS NULL)
  OR EXISTS(SELECT 1 FROM c0p_journals j LEFT JOIN c0p_sources s ON s.journal_id=j.id WHERE s.id IS NULL OR s.id<>j.source_id)
 THEN RAISE EXCEPTION 'c0p orphan source/journal' USING ERRCODE='23514'; END IF;
 IF EXISTS(SELECT 1 FROM c0p_reservation_lots a LEFT JOIN c0p_sources s ON s.id=a.reservation_id LEFT JOIN c0p_lots l ON l.id=a.lot_id WHERE s.id IS NULL OR l.id IS NULL OR s.kind<>'reserve' OR s.tenant_id<>a.tenant_id OR l.tenant_id<>a.tenant_id)
  OR EXISTS(SELECT 1 FROM c0p_settlement_lots b LEFT JOIN c0p_sources s ON s.id=b.source_id LEFT JOIN c0p_reservation_lots a ON a.id=b.reservation_lot_id WHERE s.id IS NULL OR a.id IS NULL OR s.kind<>'finish' OR s.parent_id<>a.reservation_id OR s.tenant_id<>b.tenant_id OR a.tenant_id<>b.tenant_id)
  OR EXISTS(SELECT 1 FROM c0p_refund_lots r LEFT JOIN c0p_sources s ON s.id=r.source_id LEFT JOIN c0p_settlement_lots b ON b.id=r.settlement_lot_id WHERE s.id IS NULL OR b.id IS NULL OR s.kind<>'consumption_refund' OR s.parent_id<>b.source_id OR s.tenant_id<>r.tenant_id OR b.tenant_id<>r.tenant_id)
  OR EXISTS(SELECT 1 FROM c0p_events e LEFT JOIN c0p_operations o ON o.id=e.wallet_operation_id LEFT JOIN c0p_lots l ON l.id=e.lot_id WHERE o.id IS NULL OR l.id IS NULL OR e.tenant_id<>o.tenant_id OR e.tenant_id<>l.tenant_id)
  OR EXISTS(SELECT 1 FROM c0p_balances b LEFT JOIN c0p_lots l ON l.id=b.lot_id WHERE l.id IS NULL OR l.tenant_id<>b.tenant_id)
  OR EXISTS(SELECT 1 FROM c0p_lots l LEFT JOIN c0p_sources s ON s.id=l.origin_source_id WHERE s.id IS NULL OR s.tenant_id<>l.tenant_id)
  OR EXISTS(SELECT 1 FROM c0p_sources s LEFT JOIN c0p_tenants t ON t.id=s.tenant_id WHERE t.id IS NULL)
  OR EXISTS(SELECT 1 FROM c0p_heads h LEFT JOIN c0p_tenants t ON t.id=h.tenant_id WHERE t.id IS NULL)
  OR EXISTS(SELECT 1 FROM c0p_openings a LEFT JOIN c0p_sources s ON s.id=a.source_id WHERE s.id IS NULL OR s.kind<>'epoch_opening' OR s.tenant_id<>a.tenant_id)
 THEN RAISE EXCEPTION 'c0p orphan or misbound facts' USING ERRCODE='23514'; END IF;
 FOR rec IN SELECT id FROM c0p_operations ORDER BY tenant_id COLLATE "C",seq LOOP PERFORM c0p_check_operation(rec.id); END LOOP;
 FOR rec IN SELECT id FROM c0p_lots ORDER BY id COLLATE "C" LOOP PERFORM c0p_check_lot(rec.id); END LOOP;
END $$;
