package fundinglotscan

import "github.com/moni-ai/moni-aigate/internal/periodicbudgetprefix"

// Both generated columns are transported and charged in the physical union.
// Only the original fifteen fields are passed to the independent pure checker.
type prefixObservationTransport struct {
	periodicbudgetprefix.Observation
	CommandSourceID *string
	WindowSourceID  *string
}

func prefixBindings() []binding {
	return []binding{
		bind[periodicbudgetprefix.Origin]("periodic_budget_observation_origins", "control_id", "control_id version created_xid tracking_started_at"),
		bind[periodicbudgetprefix.Head]("periodic_budget_observation_heads", "control_id", "control_id ordinal last_observation_id updated_xid"),
		bind[prefixObservationTransport]("periodic_budget_observation_journal", "id", "id control_id ordinal previous_id kind source_id event_seq last_event_id configuration_revision configuration_operation_id window_id window_revision window_last_event_id created_xid observed_at command_source_id window_source_id"),
		bind[periodicbudgetprefix.OperationBinding]("periodic_budget_operations", "operation_id", "operation_id observation_id"),
		bind[periodicbudgetprefix.WindowBinding]("periodic_budget_windows", "id", "id observation_id observation_created_xid"),
	}
}

func projectPrefix(s *session) (periodicbudgetprefix.Rows, bool, uint64, error) {
	var out periodicbudgetprefix.Rows
	base, qualified, err := projectPeriodic(s)
	if err != nil {
		return out, false, 0, err
	}
	out.Base = base
	b := prefixBindings()
	var transported []prefixObservationTransport
	for _, step := range []func() error{
		func() error { return projectInto(s, b[0], &out.Origins, &qualified) },
		func() error { return projectInto(s, b[1], &out.Heads, &qualified) },
		func() error { return projectInto(s, b[2], &transported, &qualified) },
		func() error { return projectInto(s, b[3], &out.OperationBindings, &qualified) },
		func() error { return projectInto(s, b[4], &out.WindowBindings, &qualified) },
	} {
		if err := step(); err != nil {
			return periodicbudgetprefix.Rows{}, false, 0, err
		}
	}
	out.Observations = make([]periodicbudgetprefix.Observation, 0, len(transported))
	var mismatches uint64
	t := s.tables[s.index["periodic_budget_observation_journal"]]
	for i, v := range transported {
		if s.ctx.Err() != nil {
			return periodicbudgetprefix.Rows{}, false, 0, contextFailure(s.ctx)
		}
		out.Observations = append(out.Observations, v.Observation)
		row := t.rows[i]
		kind, source, command, window := row[t.spec.index["kind"]], row[t.spec.index["source_id"]], row[t.spec.index["command_source_id"]], row[t.spec.index["window_source_id"]]
		switch {
		case kind.valid && kind.text == "command":
			if !source.valid || !command.valid || command.text != source.text {
				mismatches++
			}
			if window.valid {
				mismatches++
			}
		case kind.valid && kind.text == "window_birth":
			if command.valid {
				mismatches++
			}
			if !source.valid || !window.valid || window.text != source.text {
				mismatches++
			}
		default:
			// Neither discriminator can qualify an unknown source kind.
			mismatches += 2
		}
	}
	return out, qualified, mismatches, nil
}
