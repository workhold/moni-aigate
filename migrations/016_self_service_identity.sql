ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN password_revision BIGINT NOT NULL DEFAULT 1 CHECK(password_revision>0);
CREATE FUNCTION identity_password_revision_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.password_revision:=1;
 ELSIF NEW.password_hash IS DISTINCT FROM OLD.password_hash THEN NEW.password_revision:=OLD.password_revision+1;
 ELSE NEW.password_revision:=OLD.password_revision; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER identity_password_revision BEFORE INSERT OR UPDATE ON users FOR EACH ROW EXECUTE FUNCTION identity_password_revision_guard();

CREATE TABLE identity_initialization (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton),
 initialized_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 initialized_by TEXT NOT NULL REFERENCES users(id)
);
CREATE FUNCTION identity_initialization_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'identity initialization immutable'; END IF;
 IF NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.initialized_by AND platform_role='admin') THEN RAISE EXCEPTION 'identity initialization requires admin'; END IF;
 IF current_user<>(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=TG_RELID) AND
   (NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.initialized_by AND status='active') OR (SELECT count(*) FROM users)<>1) THEN
   RAISE EXCEPTION 'identity initialization requires bootstrap';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER identity_initialization_guard BEFORE INSERT OR UPDATE OR DELETE ON identity_initialization FOR EACH ROW EXECUTE FUNCTION identity_initialization_guard();
INSERT INTO identity_initialization(initialized_by) SELECT id FROM users WHERE platform_role='admin' ORDER BY created_at,id LIMIT 1;

CREATE TABLE identity_mail_subjects (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid() CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 purpose TEXT NOT NULL CHECK(purpose IN ('registration','password_reset')),
 email_digest TEXT NOT NULL CHECK(email_digest ~ '^[0-9a-f]{64}$'),
 window_started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 window_count INTEGER NOT NULL DEFAULT 0 CHECK(window_count BETWEEN 0 AND 5),
 next_issue_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 last_seen_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(purpose,email_digest), UNIQUE(id,purpose)
);
CREATE TABLE identity_challenges (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid() CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 subject_id UUID NOT NULL,
 purpose TEXT NOT NULL CHECK(purpose IN ('registration','password_reset')),
 token_digest TEXT NOT NULL UNIQUE CHECK(token_digest ~ '^[0-9a-f]{64}$'),
 user_id TEXT REFERENCES users(id),
 password_revision BIGINT CHECK(password_revision>0),
 registration_email_ciphertext TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 expires_at TIMESTAMPTZ NOT NULL,
 consumed_at TIMESTAMPTZ,
 revoked_at TIMESTAMPTZ,
 FOREIGN KEY(subject_id,purpose) REFERENCES identity_mail_subjects(id,purpose),
 CHECK(expires_at>created_at), CHECK(consumed_at IS NULL OR revoked_at IS NULL),
 CHECK((purpose='registration' AND user_id IS NULL AND password_revision IS NULL) OR
       (purpose='password_reset' AND user_id IS NOT NULL AND password_revision IS NOT NULL AND registration_email_ciphertext IS NULL))
);
CREATE UNIQUE INDEX identity_challenges_active ON identity_challenges(subject_id) WHERE consumed_at IS NULL AND revoked_at IS NULL;
CREATE INDEX identity_challenges_expiry ON identity_challenges(expires_at,id);
CREATE TABLE identity_mail_outbox (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid() CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 challenge_id UUID NOT NULL UNIQUE REFERENCES identity_challenges(id),
 payload_ciphertext TEXT,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sending','sent','dead','cancelled')),
 attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 5),
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 claim_generation BIGINT NOT NULL DEFAULT 0 CHECK(claim_generation=attempt_count),
 lease_until TIMESTAMPTZ,
 sent_at TIMESTAMPTZ,
 last_error_code TEXT NOT NULL DEFAULT '' CHECK(last_error_code IN ('','mail_configuration','mail_payload_invalid','mail_rejected','mail_temporary','mail_network','mail_tls','mail_tls_required','mail_decrypt','mail_attempts_exhausted','mail_proof_inactive')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK((state IN ('pending','sending'))=(payload_ciphertext IS NOT NULL)),
 CHECK((state='sending')=(lease_until IS NOT NULL)),
 CHECK((state='sent')=(sent_at IS NOT NULL)),
 CHECK(state<>'pending' OR attempt_count<5),
 CHECK(state NOT IN ('sending','sent') OR attempt_count>0)
);
CREATE INDEX identity_mail_outbox_pending ON identity_mail_outbox(next_attempt_at,id) WHERE state='pending';
CREATE INDEX identity_mail_outbox_sending ON identity_mail_outbox(lease_until,id) WHERE state='sending';

CREATE FUNCTION identity_subject_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE db_now TIMESTAMPTZ:=clock_timestamp();
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.window_count NOT IN (0,1) OR NEW.window_started_at>db_now OR NEW.last_seen_at>db_now THEN RAISE EXCEPTION 'identity subject initial state'; END IF;
  NEW.window_started_at:=db_now;
  IF NEW.window_count=1 THEN NEW.last_seen_at:=db_now; NEW.next_issue_at:=db_now+interval '60 seconds'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.last_seen_at>clock_timestamp()-interval '24 hours' OR EXISTS(SELECT 1 FROM identity_challenges WHERE subject_id=OLD.id) THEN RAISE EXCEPTION 'identity subject retention'; END IF;
  RETURN OLD;
 END IF;
 IF (NEW.id,NEW.purpose,NEW.email_digest) IS DISTINCT FROM (OLD.id,OLD.purpose,OLD.email_digest) OR NEW.last_seen_at<OLD.last_seen_at OR NEW.last_seen_at>db_now OR NEW.next_issue_at<OLD.next_issue_at THEN RAISE EXCEPTION 'identity subject immutable'; END IF;
 IF NEW.window_started_at=OLD.window_started_at THEN
  IF NEW.window_count<OLD.window_count OR NEW.window_count>OLD.window_count+1 THEN RAISE EXCEPTION 'identity subject counter'; END IF;
 ELSIF NEW.window_started_at<OLD.window_started_at+interval '1 hour' OR NEW.window_started_at>db_now OR db_now<OLD.window_started_at+interval '1 hour' OR NEW.window_count<>1 THEN RAISE EXCEPTION 'identity subject window'; END IF;
 IF NEW.window_count>OLD.window_count OR NEW.window_started_at<>OLD.window_started_at THEN
  IF OLD.next_issue_at>db_now THEN RAISE EXCEPTION 'identity subject cooldown'; END IF;
  IF NEW.window_started_at<>OLD.window_started_at THEN NEW.window_started_at:=db_now; END IF;
  NEW.last_seen_at:=db_now; NEW.next_issue_at:=db_now+interval '60 seconds';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER identity_subject_guard BEFORE INSERT OR UPDATE OR DELETE ON identity_mail_subjects FOR EACH ROW EXECUTE FUNCTION identity_subject_guard();

CREATE FUNCTION identity_challenge_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.consumed_at IS NOT NULL OR NEW.revoked_at IS NOT NULL OR (NEW.purpose='registration' AND NEW.registration_email_ciphertext IS NULL) THEN RAISE EXCEPTION 'identity challenge initial state'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.created_at>clock_timestamp()-interval '7 days' OR (OLD.consumed_at IS NULL AND OLD.revoked_at IS NULL AND OLD.expires_at>clock_timestamp()) THEN RAISE EXCEPTION 'identity challenge retention'; END IF;
  RETURN OLD;
 END IF;
 IF current_user=(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=TG_RELID) AND OLD.registration_email_ciphertext IS NOT NULL AND NEW.registration_email_ciphertext IS NOT NULL AND
  (to_jsonb(NEW)-'registration_email_ciphertext')=(to_jsonb(OLD)-'registration_email_ciphertext') THEN RETURN NEW; END IF;
 IF (NEW.id,NEW.subject_id,NEW.purpose,NEW.token_digest,NEW.user_id,NEW.password_revision,NEW.created_at,NEW.expires_at) IS DISTINCT FROM
    (OLD.id,OLD.subject_id,OLD.purpose,OLD.token_digest,OLD.user_id,OLD.password_revision,OLD.created_at,OLD.expires_at) THEN RAISE EXCEPTION 'identity challenge immutable'; END IF;
 IF (OLD.consumed_at IS NOT NULL AND NEW.consumed_at IS DISTINCT FROM OLD.consumed_at) OR (OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at) THEN RAISE EXCEPTION 'identity challenge terminal'; END IF;
 IF NEW.registration_email_ciphertext IS DISTINCT FROM OLD.registration_email_ciphertext AND
   (NEW.registration_email_ciphertext IS NOT NULL OR (NEW.consumed_at IS NULL AND NEW.revoked_at IS NULL AND NEW.expires_at>clock_timestamp())) THEN RAISE EXCEPTION 'identity challenge secret immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER identity_challenge_guard BEFORE INSERT OR UPDATE OR DELETE ON identity_challenges FOR EACH ROW EXECUTE FUNCTION identity_challenge_guard();

CREATE FUNCTION identity_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.attempt_count<>0 OR NEW.claim_generation<>0 OR NEW.lease_until IS NOT NULL OR NEW.sent_at IS NOT NULL OR NEW.payload_ciphertext IS NULL OR NEW.last_error_code<>'' THEN RAISE EXCEPTION 'identity outbox initial state'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.state IN ('pending','sending') OR OLD.created_at>clock_timestamp()-interval '7 days' THEN RAISE EXCEPTION 'identity outbox retention'; END IF;
  RETURN OLD;
 END IF;
 IF current_user=(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid=TG_RELID) AND OLD.payload_ciphertext IS NOT NULL AND NEW.payload_ciphertext IS NOT NULL AND
  (to_jsonb(NEW)-'payload_ciphertext')=(to_jsonb(OLD)-'payload_ciphertext') THEN RETURN NEW; END IF;
 IF (NEW.id,NEW.challenge_id,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.challenge_id,OLD.created_at) THEN RAISE EXCEPTION 'identity outbox immutable'; END IF;
 IF OLD.state IN ('sent','dead','cancelled') THEN
  IF NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'identity outbox terminal'; END IF;
  RETURN NEW;
 END IF;
 IF NEW.state='sending' AND (OLD.state='pending' OR (OLD.state='sending' AND OLD.lease_until<=clock_timestamp())) THEN
  IF NEW.attempt_count<>OLD.attempt_count+1 OR NEW.claim_generation<>OLD.claim_generation+1 OR OLD.attempt_count>=5 OR NEW.lease_until<=clock_timestamp() OR NEW.lease_until>clock_timestamp()+interval '60 seconds' OR NEW.payload_ciphertext IS DISTINCT FROM OLD.payload_ciphertext THEN RAISE EXCEPTION 'identity outbox claim'; END IF;
 ELSIF OLD.state='sending' AND NEW.state IN ('pending','sent','dead') OR NEW.state='cancelled' THEN
  IF (NEW.attempt_count,NEW.claim_generation) IS DISTINCT FROM (OLD.attempt_count,OLD.claim_generation) THEN RAISE EXCEPTION 'identity outbox counter'; END IF;
  IF NEW.state='pending' AND (NEW.payload_ciphertext IS DISTINCT FROM OLD.payload_ciphertext OR NEW.next_attempt_at<clock_timestamp()) THEN RAISE EXCEPTION 'identity outbox retry'; END IF;
  IF NEW.state='sent' AND NEW.last_error_code<>'' THEN RAISE EXCEPTION 'identity outbox sent error'; END IF;
 ELSE RAISE EXCEPTION 'identity outbox transition'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER identity_outbox_guard BEFORE INSERT OR UPDATE OR DELETE ON identity_mail_outbox FOR EACH ROW EXECUTE FUNCTION identity_outbox_guard();
