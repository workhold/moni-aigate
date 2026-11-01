CREATE TABLE recovery_gate (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton),
 required BOOLEAN NOT NULL DEFAULT false,
 epoch BIGINT NOT NULL DEFAULT 0 CHECK(epoch>=0),
 proposal_id TEXT, requester TEXT REFERENCES users(id), approver TEXT REFERENCES users(id),
 evidence_ref TEXT, reason TEXT, proposed_at TIMESTAMPTZ, approved_at TIMESTAMPTZ,
 latched_at TIMESTAMPTZ,
 CHECK((proposal_id IS NULL)=(requester IS NULL)),
 CHECK(approver IS NULL OR (requester IS NOT NULL AND approver<>requester)),
 CHECK(approved_at IS NULL OR approver IS NOT NULL)
);
INSERT INTO recovery_gate(singleton,required,epoch) VALUES(true,false,0);
CREATE FUNCTION guard_recovery_gate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'recovery singleton cannot be deleted'; END IF;
 IF NEW.epoch<>OLD.epoch THEN
  IF NEW.epoch<>OLD.epoch+1 OR NOT NEW.required OR NEW.proposal_id IS NOT NULL OR NEW.approver IS NOT NULL THEN
   RAISE EXCEPTION 'invalid recovery epoch transition';
  END IF;
 ELSE
  IF NOT OLD.required AND NEW.required THEN RAISE EXCEPTION 'recovery latch requires a new epoch'; END IF;
  IF OLD.proposal_id IS NOT NULL AND
   (NEW.proposal_id,NEW.requester,NEW.evidence_ref,NEW.reason,NEW.proposed_at) IS DISTINCT FROM
   (OLD.proposal_id,OLD.requester,OLD.evidence_ref,OLD.reason,OLD.proposed_at) THEN
   RAISE EXCEPTION 'immutable recovery proposal';
  END IF;
  IF OLD.required AND NOT NEW.required AND (NEW.proposal_id IS NULL OR NEW.approver IS NULL OR NEW.approved_at IS NULL) THEN
   RAISE EXCEPTION 'recovery checker approval required';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER recovery_gate_guard BEFORE UPDATE OR DELETE ON recovery_gate FOR EACH ROW EXECUTE FUNCTION guard_recovery_gate();
