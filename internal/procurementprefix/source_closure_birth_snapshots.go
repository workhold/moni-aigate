package procurementprefix

import "reflect"

type sourceFrozenColumn struct{ source, original string }

var sourceRequestFrozen = []sourceFrozenColumn{
	{"request_id", "id"}, {"tenant_id", "tenant_id"}, {"key_id", "key_id"}, {"owner_id", "owner_id"}, {"model_id", "model_id"},
	{"price_version_id", "price_version_id"}, {"input_rate", "input_rate"}, {"output_rate", "output_rate"}, {"input_upper_bound", "input_upper_bound"}, {"output_limit", "output_limit"},
	{"profile_version", "profile_version"}, {"policy_version", "policy_version"}, {"request_hmac", "request_hmac"}, {"route_revision", "route_revision"},
	{"request_deadline", "request_deadline"}, {"instance_id", "instance_id"}, {"streaming", "streaming"}, {"client_ip", "client_ip"},
	{"pricing_group_id", "pricing_group_id"}, {"tenant_pricing_revision", "tenant_pricing_revision"}, {"retail_priced_at", "retail_priced_at"}, {"request_created_at", "created_at"},
	{"pb_created_xid", "pb_created_xid"}, {"pb_tenant_control", "pb_tenant_control"}, {"pb_key_control", "pb_key_control"}, {"pb_admitted_at", "pb_admitted_at"},
}
var sourceAttemptFrozen = []sourceFrozenColumn{
	{"attempt_id", "id"}, {"tenant_id", "tenant_id"}, {"request_id", "request_id"}, {"sequence", "sequence"},
	{"channel_id", "channel_id"}, {"channel_version_id", "channel_version_id"}, {"credential_id", "credential_id"}, {"upstream_model", "upstream_model"},
	{"cost_version_id", "cost_version_id"}, {"cost_input_rate", "cost_input_rate"}, {"cost_output_rate", "cost_output_rate"}, {"cost_basis", "cost_basis"},
	{"quote_selected_at", "pp_quote_selected_at"}, {"attempt_created_at", "created_at"}, {"pb_created_xid", "pb_created_xid"},
}

func sourceFrozenEqual(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() {
		return false
	}
	if a.Type() == timeType && b.Type() == timeType {
		x, y := a.Interface().(Time), b.Interface().(Time)
		return x.Valid == y.Valid && x.InfinityModifier == y.InfinityModifier && x.Time.Equal(y.Time)
	}
	if a.Kind() == reflect.Pointer {
		if a.IsNil() {
			return b.Kind() == reflect.Pointer && b.IsNil()
		}
		a = a.Elem()
	}
	if b.Kind() == reflect.Pointer {
		if b.IsNil() {
			return false
		}
		b = b.Elem()
	}
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case reflect.String:
		return a.String() == b.String()
	case reflect.Bool:
		return a.Bool() == b.Bool()
	}
	return false
}

func (s *sourceClosureState) birthSnapshots() error {
	for _, rule := range []struct {
		source, original, id string
		columns              []sourceFrozenColumn
	}{
		{"pp_request_origins", "request_records", "request_id", sourceRequestFrozen},
		{"pp_attempt_origins", "upstream_attempts", "attempt_id", sourceAttemptFrozen},
	} {
		sources := make(map[string][]sourceRawRow)
		originals := make(map[string][]sourceRawRow)
		for _, raw := range s.tables[rule.source] {
			if err := s.w.tick(); err != nil {
				return err
			}
			key := tupleKey(sourceText(raw.value, rule.id))
			sources[key] = append(sources[key], raw)
		}
		for _, raw := range s.tables[rule.original] {
			if err := s.w.tick(); err != nil {
				return err
			}
			originals[raw.key] = append(originals[raw.key], raw)
		}
		for key, collection := range sources {
			for _, source := range collection {
				if err := s.w.tick(); err != nil {
					return err
				}
				targets := originals[key]
				valid := len(collection) == 1 && len(targets) == 1
				if valid {
					target := targets[0]
					for _, column := range rule.columns {
						if err := s.w.tick(); err != nil {
							return err
						}
						if !sourceFrozenEqual(sourceField(source.value, column.source), sourceField(target.value, column.original)) {
							valid = false
						}
					}
					if rule.source == "pp_attempt_origins" {
						a, av, err := sourceParseRoute(s.w, sourceText(source.value, "route_decisions"), sourceText(source.value, "channel_id"), sourceText(source.value, "channel_version_id"))
						if err != nil {
							return err
						}
						b, bv, err := sourceParseRoute(s.w, sourceText(target.value, "route_decisions"), sourceText(target.value, "channel_id"), sourceText(target.value, "channel_version_id"))
						if err != nil {
							return err
						}
						valid = valid && av && bv && a == b
					}
				}
				if !valid {
					s.mark("pointer", rule.source, source.key)
					if len(targets) == 1 {
						s.mark("pointer", rule.original, targets[0].key)
					}
				}
			}
		}
		// A tracked original cannot be omitted from the source enumeration. True
		// all-NULL legacy births remain uncovered instead of acquiring a snapshot.
		for _, original := range s.tables[rule.original] {
			if err := s.w.tick(); err != nil {
				return err
			}
			if sourcePresent(original.value, "pp_origin_root_id") && len(sources[original.key]) != 1 {
				s.mark("pointer", rule.original, original.key)
			}
		}
	}
	return nil
}
