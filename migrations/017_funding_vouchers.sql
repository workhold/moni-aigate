-- Refuse invalid legacy facts before creating any new source or projection.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM credit_orders c WHERE
  (c.state='pending' AND (c.approver IS NOT NULL OR c.journal_id IS NOT NULL
   OR EXISTS(SELECT 1 FROM journals j WHERE j.operation_id=c.id AND j.kind='credit')
   OR EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=c.id AND o.kind='credited')))
  OR (c.state='approved' AND (c.approver IS NULL OR c.approver=c.requester OR NOT EXISTS(
   SELECT 1 FROM journals j WHERE j.id=c.journal_id AND j.tenant_id=c.tenant_id AND j.operation_id=c.id
    AND j.kind='credit' AND j.reference=c.id AND j.actor=c.approver AND j.currency='CNY'
    AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
    AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE' AND a.tenant_id=c.tenant_id AND l.signed_delta=c.amount)
    AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='FUNDING_CLEARING' AND a.tenant_id IS NULL AND l.signed_delta=-c.amount)
    AND (SELECT count(*) FROM projection_outbox o WHERE o.operation_id=c.id AND o.kind='credited')=1
    AND EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=c.id AND o.kind='credited' AND o.tenant_id=c.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=c.amount::text)
  )))) OR EXISTS(SELECT 1 FROM journals j WHERE j.kind='voucher_grant' OR (j.kind='credit' AND NOT EXISTS(SELECT 1 FROM credit_orders c WHERE c.state='approved' AND c.journal_id=j.id)))
 OR EXISTS(SELECT 1 FROM projection_outbox o WHERE o.kind='credited' AND NOT EXISTS(SELECT 1 FROM credit_orders c WHERE c.state='approved' AND c.id=o.operation_id))
 OR EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id JOIN journals j ON j.id=l.journal_id WHERE a.kind='FUNDING_CLEARING' AND (j.kind<>'credit' OR NOT EXISTS(SELECT 1 FROM credit_orders c WHERE c.state='approved' AND c.journal_id=j.id)))
 THEN RAISE EXCEPTION 'funding legacy source verification failed'; END IF;
END $$;

ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_kind_check;
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check;
ALTER TABLE ledger_accounts ADD CHECK(kind IN ('AVAILABLE','HELD','FUNDING_CLEARING','REVENUE','OPERATING_GRANT_CLEARING'));
ALTER TABLE ledger_accounts ADD CHECK((tenant_id IS NOT NULL AND kind IN ('AVAILABLE','HELD') AND balance>=0) OR
 (tenant_id IS NULL AND kind IN ('FUNDING_CLEARING','REVENUE')) OR
 (tenant_id IS NULL AND kind='OPERATING_GRANT_CLEARING' AND balance<=0));
ALTER TABLE journals ADD UNIQUE(tenant_id,id);
ALTER TABLE credit_orders ADD UNIQUE(journal_id);
ALTER TABLE credit_orders ADD FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id);

CREATE FUNCTION funding_uuid(value TEXT) RETURNS BOOLEAN LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT value ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND value<>'00000000-0000-0000-0000-000000000000'
$$;
CREATE TABLE funding_receipts (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 source_kind TEXT NOT NULL CHECK(source_kind IN ('manual_credit','voucher_grant')),
 source_id TEXT NOT NULL,
 amount BIGINT NOT NULL CHECK(amount>0), currency TEXT NOT NULL DEFAULT 'CNY' CHECK(currency='CNY'),
 journal_id TEXT NOT NULL UNIQUE, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(source_kind,source_id), UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id)
);
INSERT INTO funding_receipts(tenant_id,source_kind,source_id,amount,journal_id,created_at)
 SELECT c.tenant_id,'manual_credit',c.id,c.amount,c.journal_id,j.created_at FROM credit_orders c JOIN journals j ON j.id=c.journal_id WHERE c.state='approved';

CREATE TABLE voucher_policy(singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton),enabled BOOLEAN NOT NULL DEFAULT false,revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0));
INSERT INTO voucher_policy DEFAULT VALUES;
CREATE TABLE commerce_commands (
 operation_id TEXT PRIMARY KEY CHECK(funding_uuid(operation_id)),actor TEXT NOT NULL REFERENCES users(id),
 scope TEXT NOT NULL CHECK(scope='platform' OR (left(scope,7)='tenant:' AND funding_uuid(substr(scope,8)))),
 kind TEXT NOT NULL CHECK(kind IN ('voucher.batch.create','voucher.batch.approve','voucher.batch.void','voucher.revocation.create','voucher.revocation.approve','voucher.redeem')),
 request_digest TEXT NOT NULL CHECK(request_digest ~ '^[0-9a-f]{64}$'),
 fingerprint_key_version TEXT CHECK(fingerprint_key_version ~ '^[A-Za-z0-9_-]{1,32}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(operation_id,kind,scope,actor),
 CHECK((kind='voucher.redeem')=(fingerprint_key_version IS NOT NULL)),
 CHECK((kind='voucher.redeem')=(scope<>'platform'))
);
CREATE TABLE commerce_operation_receipts (
 operation_id TEXT PRIMARY KEY,kind TEXT NOT NULL,scope TEXT NOT NULL,actor TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('accepted','rejected')),
 reason TEXT NOT NULL CHECK(reason IN ('accepted','voucher_unavailable','vouchers_disabled','voucher_revision_conflict','voucher_evidence_stale','voucher_state_conflict','funding_amount_overflow')),
 resource_id TEXT CHECK(funding_uuid(resource_id)),amount BIGINT CHECK(amount>0),
 funding_receipt_id TEXT REFERENCES funding_receipts(id),journal_id TEXT REFERENCES journals(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(operation_id,kind,scope,actor) REFERENCES commerce_commands(operation_id,kind,scope,actor),
 CHECK((status='accepted')=(reason='accepted')),
 CHECK((status='accepted' AND resource_id IS NOT NULL AND
  ((kind='voucher.redeem' AND amount IS NOT NULL AND funding_receipt_id IS NOT NULL AND journal_id IS NOT NULL) OR
   (kind<>'voucher.redeem' AND amount IS NULL AND funding_receipt_id IS NULL AND journal_id IS NULL))) OR
  (status='rejected' AND resource_id IS NULL AND amount IS NULL AND funding_receipt_id IS NULL AND journal_id IS NULL))
);
CREATE TABLE voucher_batches (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),
 state TEXT NOT NULL DEFAULT 'proposed' CHECK(state IN ('proposed','active','voided','revoked')),
 face_amount BIGINT NOT NULL CHECK(face_amount>0),count INTEGER NOT NULL CHECK(count BETWEEN 1 AND 100),
 expires_at TIMESTAMPTZ NOT NULL,purpose_ref TEXT NOT NULL CHECK(octet_length(purpose_ref) BETWEEN 1 AND 256),
 reason TEXT NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 200),maker TEXT NOT NULL REFERENCES users(id),
 approver TEXT REFERENCES users(id),activated_at TIMESTAMPTZ,terminal_actor TEXT REFERENCES users(id),terminal_at TIMESTAMPTZ,
 revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0),redeemed_count BIGINT NOT NULL DEFAULT 0 CHECK(redeemed_count>=0 AND redeemed_count<=count),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK(face_amount::numeric*count<=9223372036854775807),
 CHECK(expires_at>=created_at+interval '1 hour' AND expires_at<=created_at+interval '365 days'),
 CHECK((state='proposed' AND approver IS NULL AND activated_at IS NULL AND terminal_actor IS NULL AND terminal_at IS NULL) OR
  (state='active' AND approver IS NOT NULL AND approver<>maker AND activated_at IS NOT NULL AND terminal_actor IS NULL AND terminal_at IS NULL) OR
  (state='voided' AND approver IS NULL AND activated_at IS NULL AND terminal_actor IS NOT NULL AND terminal_actor=maker AND terminal_at IS NOT NULL) OR
  (state='revoked' AND approver IS NOT NULL AND approver<>maker AND activated_at IS NOT NULL AND terminal_actor IS NOT NULL AND terminal_at IS NOT NULL))
);
CREATE TABLE vouchers (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),batch_id TEXT NOT NULL REFERENCES voucher_batches(id),
 code_digest TEXT NOT NULL CHECK(code_digest ~ '^[0-9a-f]{64}$'),digest_key_version TEXT NOT NULL CHECK(digest_key_version ~ '^[A-Za-z0-9_-]{1,32}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),UNIQUE(id,batch_id)
);
CREATE TABLE voucher_revocations (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),batch_id TEXT NOT NULL REFERENCES voucher_batches(id),
 operation_id TEXT NOT NULL UNIQUE REFERENCES commerce_commands(operation_id),maker TEXT NOT NULL REFERENCES users(id),
 expected_revision BIGINT NOT NULL CHECK(expected_revision>0),expected_redeemed_count BIGINT NOT NULL CHECK(expected_redeemed_count>=0),
 reason TEXT NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 200),created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),UNIQUE(id,batch_id)
);
CREATE TABLE voucher_revocation_decisions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),proposal_id TEXT NOT NULL UNIQUE,batch_id TEXT NOT NULL,
 operation_id TEXT NOT NULL UNIQUE REFERENCES commerce_commands(operation_id),approver TEXT NOT NULL REFERENCES users(id),
 status TEXT NOT NULL CHECK(status IN ('accepted','rejected')),reason TEXT NOT NULL,
 decided_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(proposal_id,batch_id) REFERENCES voucher_revocations(id,batch_id),
 CHECK((status='accepted' AND reason='accepted') OR (status='rejected' AND reason='voucher_evidence_stale'))
);
CREATE TABLE voucher_redemptions (
 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text CHECK(funding_uuid(id)),tenant_id TEXT NOT NULL REFERENCES tenants(id),
 voucher_id TEXT NOT NULL UNIQUE,batch_id TEXT NOT NULL,
 operation_id TEXT NOT NULL UNIQUE REFERENCES commerce_commands(operation_id),actor TEXT NOT NULL REFERENCES users(id),
 amount BIGINT NOT NULL CHECK(amount>0),journal_id TEXT NOT NULL UNIQUE,funding_receipt_id TEXT NOT NULL UNIQUE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(voucher_id,batch_id) REFERENCES vouchers(id,batch_id),
 FOREIGN KEY(tenant_id,journal_id) REFERENCES journals(tenant_id,id),
 FOREIGN KEY(tenant_id,funding_receipt_id) REFERENCES funding_receipts(tenant_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX funding_receipts_page ON funding_receipts(tenant_id,created_at DESC,id COLLATE "C" DESC);
CREATE INDEX voucher_batches_page ON voucher_batches(created_at DESC,id COLLATE "C" DESC);
CREATE INDEX voucher_revocations_page ON voucher_revocations(batch_id,created_at DESC,id COLLATE "C" DESC);
CREATE INDEX voucher_redemptions_page ON voucher_redemptions(tenant_id,created_at DESC,id COLLATE "C" DESC);
CREATE INDEX vouchers_batch ON vouchers(batch_id,id);
CREATE INDEX voucher_redemptions_batch ON voucher_redemptions(batch_id,id);
CREATE UNIQUE INDEX commerce_accepted_resource ON commerce_operation_receipts(kind,resource_id) WHERE status='accepted' AND kind<>'voucher.redeem';

CREATE FUNCTION funding_credit_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable funding source'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.approver IS NOT NULL OR NEW.journal_id IS NOT NULL THEN RAISE EXCEPTION 'credit initial state'; END IF;
 ELSE
  IF (NEW.id,NEW.tenant_id,NEW.business_ref,NEW.amount,NEW.requester,NEW.proof_ref,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.business_ref,OLD.amount,OLD.requester,OLD.proof_ref,OLD.created_at) OR OLD.state<>'pending' THEN RAISE EXCEPTION 'immutable funding source'; END IF;
 END IF;
 IF (NEW.state='pending' AND (NEW.approver IS NOT NULL OR NEW.journal_id IS NOT NULL)) OR (NEW.state='approved' AND (NEW.approver IS NULL OR NEW.approver=NEW.requester OR NEW.journal_id IS NULL)) THEN RAISE EXCEPTION 'credit approval incomplete'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER funding_credit_guard BEFORE INSERT OR UPDATE OR DELETE ON credit_orders FOR EACH ROW EXECUTE FUNCTION funding_credit_guard();
CREATE FUNCTION voucher_policy_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable voucher policy'; END IF;
 IF TG_OP='INSERT' THEN NEW.revision:=1; ELSE NEW.revision:=OLD.revision+CASE WHEN NEW.enabled IS DISTINCT FROM OLD.enabled THEN 1 ELSE 0 END; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER voucher_policy_guard BEFORE INSERT OR UPDATE OR DELETE ON voucher_policy FOR EACH ROW EXECUTE FUNCTION voucher_policy_guard();
CREATE FUNCTION voucher_batch_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable voucher batch'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'proposed' OR NEW.redeemed_count<>0 THEN RAISE EXCEPTION 'voucher batch initial state'; END IF;NEW.revision:=1;RETURN NEW;
 END IF;
 IF (NEW.id,NEW.face_amount,NEW.count,NEW.expires_at,NEW.purpose_ref,NEW.reason,NEW.maker,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.face_amount,OLD.count,OLD.expires_at,OLD.purpose_ref,OLD.reason,OLD.maker,OLD.created_at) THEN RAISE EXCEPTION 'immutable voucher batch'; END IF;
 IF NEW.state=OLD.state THEN
  IF (NEW.approver,NEW.activated_at,NEW.terminal_actor,NEW.terminal_at) IS DISTINCT FROM (OLD.approver,OLD.activated_at,OLD.terminal_actor,OLD.terminal_at) THEN RAISE EXCEPTION 'immutable voucher approval'; END IF;
  IF NEW.redeemed_count<>OLD.redeemed_count AND (OLD.state<>'active' OR NEW.redeemed_count<>OLD.redeemed_count+1 OR NEW.redeemed_count<>(SELECT count(*) FROM voucher_redemptions WHERE batch_id=NEW.id)) THEN RAISE EXCEPTION 'voucher batch counter'; END IF;
  NEW.revision:=OLD.revision;
 ELSE
  IF NOT ((OLD.state='proposed' AND NEW.state IN ('active','voided')) OR (OLD.state='active' AND NEW.state='revoked')) OR NEW.redeemed_count<>OLD.redeemed_count THEN RAISE EXCEPTION 'voucher batch transition'; END IF;
  IF OLD.state='active' AND (NEW.approver,NEW.activated_at) IS DISTINCT FROM (OLD.approver,OLD.activated_at) THEN RAISE EXCEPTION 'immutable voucher approval'; END IF;
  NEW.revision:=OLD.revision+1;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER voucher_batch_guard BEFORE INSERT OR UPDATE OR DELETE ON voucher_batches FOR EACH ROW EXECUTE FUNCTION voucher_batch_guard();
CREATE FUNCTION voucher_redemption_count() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 UPDATE voucher_batches SET redeemed_count=redeemed_count+1 WHERE id=NEW.batch_id;RETURN NEW;
END $$;
CREATE TRIGGER voucher_redemption_count AFTER INSERT ON voucher_redemptions FOR EACH ROW EXECUTE FUNCTION voucher_redemption_count();

CREATE FUNCTION funding_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM journals j JOIN funding_receipts f ON f.journal_id=j.id
 LEFT JOIN credit_orders c ON f.source_kind='manual_credit' AND c.id=f.source_id
 LEFT JOIN vouchers v ON f.source_kind='voucher_grant' AND v.id=f.source_id
 LEFT JOIN voucher_batches b ON b.id=v.batch_id
 LEFT JOIN voucher_redemptions r ON r.voucher_id=v.id AND r.funding_receipt_id=f.id
 LEFT JOIN commerce_commands cmd ON cmd.operation_id=r.operation_id
 LEFT JOIN commerce_operation_receipts terminal ON terminal.operation_id=r.operation_id
 WHERE j.id=journal_ref AND j.tenant_id=f.tenant_id AND j.currency='CNY' AND f.currency='CNY' AND f.amount>0
 AND ((f.source_kind='manual_credit' AND c.state='approved' AND c.journal_id=j.id AND c.tenant_id=f.tenant_id AND c.amount=f.amount AND c.approver<>c.requester AND j.kind='credit' AND j.operation_id=c.id AND j.reference=c.id AND j.actor=c.approver)
 OR (f.source_kind='voucher_grant' AND b.state IN ('active','revoked') AND r.id=j.operation_id AND j.kind='voucher_grant' AND j.reference=v.id AND j.actor=r.actor
  AND r.tenant_id=f.tenant_id AND r.journal_id=j.id AND r.amount=f.amount AND b.face_amount=f.amount AND r.batch_id=b.id
  AND r.created_at>=b.activated_at AND r.created_at<b.expires_at AND (b.terminal_at IS NULL OR r.created_at<=b.terminal_at)
  AND cmd.kind='voucher.redeem' AND cmd.actor=r.actor AND cmd.scope='tenant:'||r.tenant_id
  AND terminal.status='accepted' AND terminal.reason='accepted' AND terminal.resource_id=r.id AND terminal.amount=f.amount AND terminal.funding_receipt_id=f.id AND terminal.journal_id=j.id))
 AND (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)=2
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='AVAILABLE' AND a.tenant_id=f.tenant_id AND l.signed_delta=f.amount)
 AND EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.tenant_id IS NULL AND a.kind=CASE WHEN f.source_kind='manual_credit' THEN 'FUNDING_CLEARING' ELSE 'OPERATING_GRANT_CLEARING' END AND l.signed_delta=-f.amount)
 AND (SELECT count(*) FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited')=1
 AND EXISTS(SELECT 1 FROM projection_outbox o WHERE o.operation_id=j.operation_id AND o.kind='credited' AND o.tenant_id=j.tenant_id AND o.schema_version=1 AND jsonb_typeof(o.sanitized_payload->'amount')='string' AND o.sanitized_payload->>'amount'=f.amount::text))
$$;
CREATE FUNCTION funding_fact_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE jid TEXT; credit_state TEXT;
BEGIN
 IF TG_TABLE_NAME='credit_orders' THEN
  SELECT state,journal_id INTO credit_state,jid FROM credit_orders WHERE id=NEW.id;
  IF credit_state='pending' THEN
   IF EXISTS(SELECT 1 FROM journals WHERE kind='credit' AND operation_id=NEW.id) OR EXISTS(SELECT 1 FROM projection_outbox WHERE kind='credited' AND operation_id=NEW.id) THEN RAISE EXCEPTION 'pending credit has funding'; END IF;
   RETURN NULL;
  END IF;
 ELSIF TG_TABLE_NAME='journals' THEN
  IF NEW.kind NOT IN ('credit','voucher_grant') THEN
   IF EXISTS(SELECT 1 FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=NEW.id AND a.kind IN ('FUNDING_CLEARING','OPERATING_GRANT_CLEARING')) THEN RAISE EXCEPTION 'funding clearing requires source';END IF;
   RETURN NULL;
  END IF;jid:=NEW.id;
 ELSIF TG_TABLE_NAME='projection_outbox' THEN IF NEW.kind<>'credited' THEN RETURN NULL; END IF;SELECT id INTO jid FROM journals WHERE operation_id=NEW.operation_id;
 ELSE jid:=NEW.journal_id; END IF;
 IF jid IS NULL OR NOT funding_journal_valid(jid) THEN RAISE EXCEPTION 'funding source integrity failed'; END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER funding_credit_fact AFTER INSERT OR UPDATE ON credit_orders DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();
CREATE CONSTRAINT TRIGGER funding_receipt_fact AFTER INSERT ON funding_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();
CREATE CONSTRAINT TRIGGER funding_journal_fact AFTER INSERT ON journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();
CREATE CONSTRAINT TRIGGER funding_redemption_fact AFTER INSERT ON voucher_redemptions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();
CREATE CONSTRAINT TRIGGER funding_outbox_fact AFTER INSERT ON projection_outbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION funding_fact_guard();

CREATE FUNCTION voucher_batch_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE batch_ref TEXT; b voucher_batches%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='voucher_batches' THEN batch_ref:=NEW.id;ELSE batch_ref:=NEW.batch_id;END IF;
 SELECT * INTO b FROM voucher_batches WHERE id=batch_ref;
 IF b.id IS NULL OR b.count<>(SELECT count(*) FROM vouchers WHERE batch_id=batch_ref) OR b.redeemed_count<>(SELECT count(*) FROM voucher_redemptions WHERE batch_id=batch_ref) THEN RAISE EXCEPTION 'voucher batch facts incomplete'; END IF;
 IF NOT EXISTS(SELECT 1 FROM commerce_operation_receipts terminal WHERE terminal.kind='voucher.batch.create' AND terminal.status='accepted' AND terminal.resource_id=b.id AND terminal.actor=b.maker) THEN RAISE EXCEPTION 'voucher batch creation incomplete';END IF;
 IF b.state IN ('active','revoked') AND NOT EXISTS(SELECT 1 FROM commerce_operation_receipts terminal WHERE terminal.kind='voucher.batch.approve' AND terminal.status='accepted' AND terminal.resource_id=b.id AND terminal.actor=b.approver) THEN RAISE EXCEPTION 'voucher batch approval incomplete';END IF;
 IF b.state='voided' AND NOT EXISTS(SELECT 1 FROM commerce_operation_receipts terminal WHERE terminal.kind='voucher.batch.void' AND terminal.status='accepted' AND terminal.resource_id=b.id AND terminal.actor=b.maker) THEN RAISE EXCEPTION 'voucher batch void incomplete';END IF;
 IF b.state='revoked' AND NOT EXISTS(SELECT 1 FROM voucher_revocations p JOIN voucher_revocation_decisions d ON d.proposal_id=p.id
  JOIN commerce_commands cmd ON cmd.operation_id=d.operation_id JOIN commerce_operation_receipts terminal ON terminal.operation_id=cmd.operation_id
  WHERE p.batch_id=b.id AND d.batch_id=b.id AND d.status='accepted' AND d.reason='accepted' AND d.approver<>p.maker
  AND b.terminal_actor=d.approver AND b.revision=p.expected_revision+1 AND b.redeemed_count=p.expected_redeemed_count
  AND cmd.kind='voucher.revocation.approve' AND cmd.scope='platform' AND cmd.actor=d.approver
  AND terminal.status='accepted' AND terminal.resource_id=p.id) THEN RAISE EXCEPTION 'voucher revocation decision incomplete';END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER voucher_batch_complete AFTER INSERT OR UPDATE ON voucher_batches DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_batch_complete();
CREATE CONSTRAINT TRIGGER voucher_batch_vouchers_complete AFTER INSERT ON vouchers DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_batch_complete();
CREATE CONSTRAINT TRIGGER voucher_batch_redemptions_complete AFTER INSERT ON voucher_redemptions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_batch_complete();
CREATE FUNCTION commerce_command_complete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM commerce_commands c JOIN commerce_operation_receipts r USING(operation_id) WHERE c.operation_id=NEW.operation_id AND (c.kind,c.scope,c.actor)=(r.kind,r.scope,r.actor)) THEN RAISE EXCEPTION 'commerce command incomplete'; END IF;
 IF TG_TABLE_NAME='commerce_operation_receipts' THEN
  IF NEW.status='accepted' AND NEW.kind='voucher.redeem' AND NOT EXISTS(SELECT 1 FROM voucher_redemptions r WHERE r.operation_id=NEW.operation_id AND r.id=NEW.resource_id AND r.actor=NEW.actor AND 'tenant:'||r.tenant_id=NEW.scope AND r.amount=NEW.amount AND r.funding_receipt_id=NEW.funding_receipt_id AND r.journal_id=NEW.journal_id) THEN RAISE EXCEPTION 'commerce redemption incomplete';END IF;
  IF NEW.status='accepted' AND (
   (NEW.kind='voucher.batch.create' AND NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.resource_id AND b.maker=NEW.actor)) OR
   (NEW.kind='voucher.batch.approve' AND NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.resource_id AND b.state IN ('active','revoked') AND b.approver=NEW.actor)) OR
   (NEW.kind='voucher.batch.void' AND NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.resource_id AND b.state='voided' AND b.terminal_actor=NEW.actor AND b.maker=NEW.actor)) OR
   (NEW.kind='voucher.revocation.create' AND NOT EXISTS(SELECT 1 FROM voucher_revocations p WHERE p.id=NEW.resource_id AND p.operation_id=NEW.operation_id AND p.maker=NEW.actor)) OR
   (NEW.kind='voucher.revocation.approve' AND NOT EXISTS(SELECT 1 FROM voucher_revocation_decisions d WHERE d.proposal_id=NEW.resource_id AND d.operation_id=NEW.operation_id AND d.approver=NEW.actor AND d.status='accepted'))
  ) THEN RAISE EXCEPTION 'commerce resource incomplete';END IF;
  IF NEW.status='rejected' AND NEW.kind='voucher.revocation.approve' AND NEW.reason='voucher_evidence_stale' AND NOT EXISTS(SELECT 1 FROM voucher_revocation_decisions d WHERE d.operation_id=NEW.operation_id AND d.approver=NEW.actor AND d.status='rejected' AND d.reason=NEW.reason) THEN RAISE EXCEPTION 'commerce stale decision incomplete';END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER commerce_command_complete AFTER INSERT ON commerce_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION commerce_command_complete();
CREATE CONSTRAINT TRIGGER commerce_receipt_complete AFTER INSERT ON commerce_operation_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION commerce_command_complete();

CREATE FUNCTION voucher_revocation_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p voucher_revocations%ROWTYPE; d voucher_revocation_decisions%ROWTYPE; b voucher_batches%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='voucher_revocations' THEN
  p:=NEW;
 ELSE
  d:=NEW;SELECT * INTO p FROM voucher_revocations WHERE id=d.proposal_id;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM commerce_commands c JOIN commerce_operation_receipts terminal USING(operation_id)
  WHERE c.operation_id=p.operation_id AND c.kind='voucher.revocation.create' AND c.scope='platform' AND c.actor=p.maker
   AND terminal.status='accepted' AND terminal.resource_id=p.id) THEN RAISE EXCEPTION 'voucher revocation proposal incomplete';END IF;
 IF TG_TABLE_NAME='voucher_revocation_decisions' THEN
  SELECT * INTO b FROM voucher_batches WHERE id=p.batch_id;
  IF d.approver=p.maker OR d.batch_id<>p.batch_id OR NOT EXISTS(SELECT 1 FROM commerce_commands c JOIN commerce_operation_receipts terminal USING(operation_id)
   WHERE c.operation_id=d.operation_id AND c.kind='voucher.revocation.approve' AND c.scope='platform' AND c.actor=d.approver
    AND terminal.status=d.status AND terminal.reason=d.reason AND (d.status='rejected' OR terminal.resource_id=p.id)) THEN RAISE EXCEPTION 'voucher revocation terminal incomplete';END IF;
  IF d.status='accepted' THEN
   IF b.state<>'revoked' OR b.terminal_actor<>d.approver OR b.revision<>p.expected_revision+1 OR b.redeemed_count<>p.expected_redeemed_count THEN RAISE EXCEPTION 'voucher revocation state incomplete';END IF;
  ELSIF b.state='active' AND b.revision=p.expected_revision AND b.redeemed_count=p.expected_redeemed_count THEN RAISE EXCEPTION 'voucher revocation evidence not stale';END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER voucher_revocation_complete AFTER INSERT ON voucher_revocations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_revocation_complete();
CREATE CONSTRAINT TRIGGER voucher_decision_complete AFTER INSERT ON voucher_revocation_decisions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_revocation_complete();
CREATE FUNCTION voucher_proposal_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM voucher_batches b WHERE b.id=NEW.batch_id AND b.state='active' AND b.revision=NEW.expected_revision AND b.redeemed_count=NEW.expected_redeemed_count) THEN RAISE EXCEPTION 'voucher proposal snapshot invalid';END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER voucher_proposal_snapshot BEFORE INSERT ON voucher_revocations FOR EACH ROW EXECUTE FUNCTION voucher_proposal_snapshot();

CREATE TRIGGER immutable_funding_receipt BEFORE UPDATE OR DELETE ON funding_receipts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_commerce_command BEFORE UPDATE OR DELETE ON commerce_commands FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_commerce_receipt BEFORE UPDATE OR DELETE ON commerce_operation_receipts FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_voucher BEFORE UPDATE OR DELETE ON vouchers FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_voucher_redemption BEFORE UPDATE OR DELETE ON voucher_redemptions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_voucher_revocation BEFORE UPDATE OR DELETE ON voucher_revocations FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
CREATE TRIGGER immutable_voucher_decision BEFORE UPDATE OR DELETE ON voucher_revocation_decisions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
