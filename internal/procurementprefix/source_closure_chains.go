package procurementprefix

import (
	"reflect"
	"strconv"
)

// sourceChain orders only by the contract's explicit ordinal. Neither timestamps
// nor UUIDs take part in predecessor selection. Duplicate slots remain errors.
func (s *sourceClosureState) sourceChain(table, kind, groupField, ordinalField, previousField string) (map[string][]sourceRawRow, error) {
	groups := make(map[string][]sourceRawRow)
	for _, raw := range s.tables[table] {
		if err := s.w.tick(); err != nil {
			return nil, err
		}
		key := tupleKey(sourceText(raw.value, groupField))
		groups[key] = append(groups[key], raw)
	}
	ordered := make(map[string][]sourceRawRow)
	for key, group := range groups {
		sequence := make([]sourceRawRow, len(group))
		good := true
		for _, raw := range group {
			if err := s.w.tick(); err != nil {
				return nil, err
			}
			text := sourceText(raw.value, ordinalField)
			n, err := strconv.ParseInt(text, 10, 64)
			if err != nil || !signedInteger(text, 64) || n < 1 || n > int64(len(group)) {
				s.mark(kind, table, raw.key)
				good = false
				continue
			}
			if sequence[n-1].value.IsValid() {
				s.mark(kind, table, raw.key)
				s.mark(kind, table, sequence[n-1].key)
				good = false
				continue
			}
			sequence[n-1] = raw
		}
		for i, raw := range sequence {
			if err := s.w.tick(); err != nil {
				return nil, err
			}
			if !raw.value.IsValid() {
				good = false
				continue
			}
			prev := sourceOptional(raw.value, previousField)
			valid := i == 0 && prev == nil
			if i > 0 && sequence[i-1].value.IsValid() {
				valid = prev != nil && *prev == sourceText(sequence[i-1].value, "id")
			}
			if !valid {
				s.mark(kind, table, raw.key)
				good = false
			}
		}
		if good {
			ordered[key] = sequence
		}
	}
	return ordered, nil
}

func (s *sourceClosureState) chains(rows PrefixRows) error {
	requests, err := s.sourceChain("pp_request_decisions", "request_chain", "request_id", "ordinal", "previous_decision_id")
	if err != nil {
		return err
	}
	attempts, err := s.sourceChain("pp_attempt_nodes", "attempt_chain", "attempt_id", "ordinal", "previous_node_id")
	if err != nil {
		return err
	}
	accounts, err := s.sourceChain("pp_account_journal", "account_chain", "account_id", "ordinal", "previous_id")
	if err != nil {
		return err
	}
	members, err := s.sourceChain("pp_evidence_members", "evidence_chain", "attempt_id", "member_ordinal", "previous_member_id")
	if err != nil {
		return err
	}
	if err = s.requestChain(rows, requests); err != nil {
		return err
	}
	if err = s.attemptChain(rows, attempts, members); err != nil {
		return err
	}
	return s.accountChain(rows, accounts)
}

func (s *sourceClosureState) requestChain(rows PrefixRows, chains map[string][]sourceRawRow) error {
	origins := make(map[string][]RequestOriginRow)
	heads := make(map[string][]RequestHeadRow)
	for _, r := range rows.RequestOrigins {
		if err := s.w.tick(); err != nil {
			return err
		}
		k := tupleKey(r.RequestID)
		origins[k] = append(origins[k], r)
	}
	for _, r := range rows.RequestHeads {
		if err := s.w.tick(); err != nil {
			return err
		}
		k := tupleKey(r.RequestID)
		heads[k] = append(heads[k], r)
		if len(origins[k]) != 1 {
			s.mark("request_chain", "pp_request_heads", k)
		}
	}
	for _, r := range rows.RequestDecisions {
		if err := s.w.tick(); err != nil {
			return err
		}
		if len(origins[tupleKey(r.RequestID)]) != 1 {
			s.mark("request_chain", "pp_request_decisions", tupleKey(r.ID))
		}
	}
	for k, os := range origins {
		for _, origin := range os {
			if err := s.w.tick(); err != nil {
				return err
			}
			sequence := chains[k]
			latest := origin.InitialLatestID
			valid := len(os) == 1 && len(heads[k]) == 1
			for _, raw := range sequence {
				if err := s.w.tick(); err != nil {
					return err
				}
				r := raw.value.Interface().(RequestDecisionRow)
				if r.TenantID != origin.TenantID || !sourceSameOptional(r.BeforeLatestID, latest) {
					s.mark("request_chain", "pp_request_decisions", raw.key)
				}
				latest = r.AfterLatestID
			}
			if len(heads[k]) == 1 {
				h := heads[k][0]
				xid := origin.CreatedXID
				var tail *string
				if len(sequence) > 0 {
					last := sequence[len(sequence)-1].value.Interface().(RequestDecisionRow)
					xid = last.CreatedXID
					tail = &last.ID
				}
				if h.Ordinal != strconv.Itoa(len(sequence)) || !sourceSameOptional(h.LastDecisionID, tail) || h.UpdatedXID != xid {
					valid = false
				}
			}
			if !valid {
				s.mark("request_chain", "pp_request_origins", k)
			}
		}
	}
	return nil
}

func (s *sourceClosureState) attemptChain(rows PrefixRows, chains, members map[string][]sourceRawRow) error {
	origins := make(map[string][]AttemptOriginRow)
	heads := make(map[string][]AttemptHeadRow)
	roots := make(map[string][]OperationRootRow)
	steps := make(map[string][]RootStepRow)
	nodes := make(map[string][]AttemptNodeRow)
	for _, r := range rows.AttemptOrigins {
		if err := s.w.tick(); err != nil {
			return err
		}
		k := tupleKey(r.AttemptID)
		origins[k] = append(origins[k], r)
	}
	for _, r := range rows.OperationRoots {
		if err := s.w.tick(); err != nil {
			return err
		}
		k := tupleKey(r.ID)
		roots[k] = append(roots[k], r)
	}
	for _, r := range rows.RootSteps {
		if err := s.w.tick(); err != nil {
			return err
		}
		k := tupleKey(r.RootID, r.StepNo)
		steps[k] = append(steps[k], r)
	}
	for _, r := range rows.AttemptHeads {
		if err := s.w.tick(); err != nil {
			return err
		}
		k := tupleKey(r.AttemptID)
		heads[k] = append(heads[k], r)
		if len(origins[k]) != 1 {
			s.mark("attempt_chain", "pp_attempt_heads", k)
		}
	}
	for _, r := range rows.AttemptNodes {
		if err := s.w.tick(); err != nil {
			return err
		}
		k := tupleKey(r.RootID, r.StepNo)
		nodes[k] = append(nodes[k], r)
		rs, ss := roots[tupleKey(r.RootID)], steps[k]
		valid := len(origins[tupleKey(r.AttemptID)]) == 1 && len(rs) == 1 && len(ss) == 1 && r.StepKind != "attempt_birth"
		if len(rs) == 1 {
			root := rs[0]
			valid = valid && root.ScopeKind == "request" && root.AttemptID != nil && *root.AttemptID == r.AttemptID
		}
		if len(ss) == 1 {
			step := ss[0]
			valid = valid && step.Kind == r.StepKind && step.SourceID == r.SourceID && step.CreatedXID == r.CreatedXID
		}
		if !valid {
			s.mark("attempt_chain", "pp_attempt_nodes", tupleKey(r.ID))
		}
	}
	for k, ss := range steps {
		for _, step := range ss {
			if err := s.w.tick(); err != nil {
				return err
			}
			rs := roots[tupleKey(step.RootID)]
			ns := nodes[k]
			if len(rs) != 1 {
				continue
			}
			expected := rs[0].ScopeKind == "request" && rs[0].AttemptID != nil && step.Kind != "attempt_birth"
			if expected && len(ns) != 1 || !expected && len(ns) != 0 {
				s.mark("attempt_chain", "pp_root_steps", k)
			}
		}
	}
	for _, r := range rows.EvidenceMembers {
		if err := s.w.tick(); err != nil {
			return err
		}
		os := origins[tupleKey(r.AttemptID)]
		if len(os) != 1 || len(os) == 1 && os[0].ManagementMode != "managed" {
			s.mark("evidence_chain", "pp_evidence_members", tupleKey(r.ID))
		}
	}
	for k, os := range origins {
		for _, origin := range os {
			if err := s.w.tick(); err != nil {
				return err
			}
			sequence := chains[k]
			valid := len(os) == 1 && len(heads[k]) == 1
			if len(heads[k]) == 1 {
				h := heads[k][0]
				xid := origin.CreatedXID
				var tail *string
				if len(sequence) > 0 {
					last := sequence[len(sequence)-1].value.Interface().(AttemptNodeRow)
					xid = last.CreatedXID
					tail = &last.ID
				}
				if h.Ordinal != strconv.Itoa(len(sequence)) || !sourceSameOptional(h.LastNodeID, tail) || h.UpdatedXID != xid {
					valid = false
				}
				if origin.ManagementMode == "managed" {
					ms := members[k]
					count := strconv.Itoa(len(ms))
					var memberTail *string
					if len(ms) > 0 {
						last := ms[len(ms)-1].value.Interface().(EvidenceMemberRow)
						memberTail = &last.ID
					}
					if h.MemberCount == nil || *h.MemberCount != count || !sourceSameOptional(h.MemberTailID, memberTail) {
						s.mark("evidence_chain", "pp_attempt_heads", k)
					}
				} else if h.MemberCount != nil || h.MemberTailID != nil || len(members[k]) != 0 {
					s.mark("evidence_chain", "pp_attempt_heads", k)
				}
			}
			if !valid {
				s.mark("attempt_chain", "pp_attempt_origins", k)
			}
		}
	}
	return nil
}

func (s *sourceClosureState) accountChain(rows PrefixRows, chains map[string][]sourceRawRow) error {
	origins := make(map[string][]AccountOriginRow)
	heads := make(map[string][]AccountHeadRow)
	for _, r := range rows.AccountOrigins {
		if err := s.w.tick(); err != nil {
			return err
		}
		k := tupleKey(r.AccountID)
		origins[k] = append(origins[k], r)
	}
	for _, r := range rows.AccountHeads {
		if err := s.w.tick(); err != nil {
			return err
		}
		k := tupleKey(r.AccountID)
		heads[k] = append(heads[k], r)
		if len(origins[k]) != 1 {
			s.mark("account_chain", "pp_account_heads", k)
		}
	}
	for _, r := range rows.AccountJournal {
		if err := s.w.tick(); err != nil {
			return err
		}
		if len(origins[tupleKey(r.AccountID)]) != 1 {
			s.mark("account_chain", "pp_account_journal", tupleKey(r.ID))
		}
	}
	fields := []string{"label", "provider", "state", "limit_amount", "configuration_revision", "held_amount", "spent_amount", "blocking_count"}
	for k, os := range origins {
		for _, origin := range os {
			if err := s.w.tick(); err != nil {
				return err
			}
			sequence := chains[k]
			valid := len(os) == 1 && len(heads[k]) == 1 && len(sequence) > 0
			for i, raw := range sequence {
				if err := s.w.tick(); err != nil {
					return err
				}
				r := raw.value.Interface().(AccountJournalRow)
				if i == 0 {
					if r.Kind != "create" || r.ID != origin.JournalID || r.SourceKind != "command" || r.SourceID != origin.CreateOperationID || r.CommandSourceID == nil || *r.CommandSourceID != origin.CommandSourceID || r.RootID != origin.RootID || r.StepNo != origin.StepNo || r.CreatedXID != origin.CreatedXID {
						s.mark("account_chain", "pp_account_journal", raw.key)
					}
				} else if r.Kind == "create" {
					s.mark("account_chain", "pp_account_journal", raw.key)
				}
				for _, field := range fields {
					if err := s.w.tick(); err != nil {
						return err
					}
					before := sourceOptional(raw.value, "before_"+field)
					if i == 0 {
						if before != nil {
							s.mark("account_chain", "pp_account_journal", raw.key)
						}
					} else {
						expected := sourceText(sequence[i-1].value, "after_"+field)
						if before == nil || *before != expected {
							s.mark("account_chain", "pp_account_journal", raw.key)
						}
					}
				}
			}
			if len(heads[k]) == 1 && len(sequence) > 0 {
				h := heads[k][0]
				last := sequence[len(sequence)-1]
				r := last.value.Interface().(AccountJournalRow)
				if h.Ordinal != strconv.Itoa(len(sequence)) || h.JournalID != r.ID || h.UpdatedXID != r.CreatedXID {
					valid = false
				}
				hv := reflect.ValueOf(h)
				for _, field := range fields {
					if err := s.w.tick(); err != nil {
						return err
					}
					if sourceText(hv, field) != sourceText(last.value, "after_"+field) {
						valid = false
					}
				}
			}
			if !valid {
				s.mark("account_chain", "pp_account_origins", k)
			}
		}
	}
	return nil
}
