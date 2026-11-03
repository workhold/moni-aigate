-- B-P1 PERMISSION CANDIDATE, not automatic provisioning or a migration.
-- Replace every {{app}} and {{projection}} occurrence as SQL IDENTIFIERS using
-- pgx.Identifier.Sanitize (or equivalent), never as data/string interpolation.
-- Run as the isolated owner after original 001--022 provision and candidate.
-- Base source-table privileges stay unchanged. New source pointers/stamps
-- have no UPDATE grant; BEFORE INSERT overwrites even table-level INSERT input.

REVOKE ALL ON periodic_budget_observation_origins,periodic_budget_observation_heads,
 periodic_budget_observation_journal FROM {{app}},{{projection}};
REVOKE ALL ON TYPE periodic_budget_observation_origins,periodic_budget_observation_heads,
 periodic_budget_observation_journal FROM {{app}},{{projection}};
GRANT USAGE ON TYPE periodic_budget_observation_origins,periodic_budget_observation_heads,
 periodic_budget_observation_journal TO {{app}};
GRANT SELECT ON periodic_budget_observation_origins,periodic_budget_observation_heads,
 periodic_budget_observation_journal TO {{app}};
GRANT INSERT(control_id,version,tracking_started_at)
 ON periodic_budget_observation_origins TO {{app}};
GRANT INSERT(control_id,ordinal,last_observation_id)
 ON periodic_budget_observation_heads TO {{app}};
GRANT UPDATE(ordinal,last_observation_id)
 ON periodic_budget_observation_heads TO {{app}};
GRANT INSERT(id,control_id,ordinal,previous_id,kind,source_id,event_seq,last_event_id,
 configuration_revision,configuration_operation_id,window_id,window_revision,window_last_event_id,observed_at)
 ON periodic_budget_observation_journal TO {{app}};
GRANT EXECUTE ON FUNCTION pb_prefix_immutable(),pb_prefix_origin_guard(),pb_prefix_head_guard(),
 pb_prefix_journal_guard(),pb_prefix_append(text,text,text,bigint,text,bigint,text,text,bigint,text,timestamptz),
 pb_prefix_control_origin(),pb_prefix_operation_lock(),pb_prefix_operation_capture(),pb_prefix_window_capture(),
 pb_prefix_check_origin(),pb_prefix_check_journal(),pb_prefix_check_source() TO {{app}};
