ALTER TABLE route_policy_versions
 ADD COLUMN cost_mode TEXT NOT NULL DEFAULT 'manual' CHECK(cost_mode IN ('manual','cost_first')),
 ADD COLUMN unknown_cost_policy TEXT NOT NULL DEFAULT 'allow' CHECK(unknown_cost_policy IN ('allow','deny')),
 ADD CONSTRAINT cost_first_requires_known_cost CHECK(cost_mode<>'cost_first' OR unknown_cost_policy='deny');
ALTER TABLE model_bindings
 ADD COLUMN max_input_cost_rate BIGINT CHECK(max_input_cost_rate>=0),
 ADD COLUMN max_output_cost_rate BIGINT CHECK(max_output_cost_rate>=0);

-- Cost publishers synchronize with billing's channel SHARE locks. A raw INSERT
-- cannot bypass this barrier or the channel's existing management CAS revision.
-- Existing duplicate effective instants remain readable in deterministic order.
CREATE FUNCTION synchronize_cost_version_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE channels SET id=id WHERE id=NEW.channel_id;
 IF EXISTS(SELECT 1 FROM cost_versions WHERE channel_id=NEW.channel_id AND upstream_model=NEW.upstream_model AND effective_at=NEW.effective_at) THEN
  RAISE EXCEPTION 'cost version effective instant already exists' USING ERRCODE='23505',CONSTRAINT='cost_version_effective_instant';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER synchronize_cost_version BEFORE INSERT ON cost_versions FOR EACH ROW EXECUTE FUNCTION synchronize_cost_version_insert();
CREATE INDEX cost_versions_current_quote ON cost_versions(channel_id,upstream_model,effective_at DESC,created_at DESC,id DESC);
