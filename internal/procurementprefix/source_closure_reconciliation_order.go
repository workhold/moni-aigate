package procurementprefix

import (
	"encoding/json"
	"strconv"
)

// This result describes only missing manual history. It is neither the complete
// reconciliation gap set nor a source qualification or caller-supplied proof.
type sourceReconciliationOrderResult struct{ LegacyCases keySet }

type sourceManualGroup struct{ actions [3][]sourceRawRow }

type sourceManualProposal struct {
	usage            [6]*string
	evidence, reason *string
}

func sourceManualRank(action string) int {
	switch action {
	case "propose":
		return 0
	case "approve":
		return 1
	case "release":
		return 2
	}
	return -1
}
func sourceManualCaseProposal(r ReconciliationSourceRow) sourceManualProposal {
	return sourceManualProposal{[6]*string{r.CaseUsageStatus, r.CaseInputTokens, r.CaseOutputTokens, r.CaseTotalTokens, r.CaseCachedTokens, r.CaseReasoningTokens}, r.CaseEvidenceRef, r.CaseProposalReason}
}
func sourceManualInputProposal(r ReconciliationSourceRow) sourceManualProposal {
	return sourceManualProposal{[6]*string{r.InputUsageStatus, r.InputInputTokens, r.InputOutputTokens, r.InputTotalTokens, r.InputCachedTokens, r.InputReasoningTokens}, r.InputEvidenceRef, r.InputReason}
}
func sourceManualSameProposal(a, b sourceManualProposal) bool {
	for i := range a.usage {
		if !sourceSameOptional(a.usage[i], b.usage[i]) {
			return false
		}
	}
	return sourceSameOptional(a.evidence, b.evidence) && sourceSameOptional(a.reason, b.reason)
}
func sourceManualLocal(r ReconciliationSourceRow) bool {
	if r.Actor == "" || r.ActorStatus != "active" {
		return false
	}
	switch r.Action {
	case "propose":
		return r.ActorRole == "finance_operator" && r.BeforeCaseState == "open" && r.AfterCaseState == "proposed" && r.BeforeReservationStatus == "pending_reconciliation" && sourceSameOptional(r.AfterRequester, &r.Actor) && r.AfterApprover == nil && r.RequesterRole == nil && r.RequesterStatus == nil
	case "approve":
		return r.ActorRole == "finance_approver" && r.BeforeCaseState == "proposed" && r.AfterCaseState == "approved" && r.BeforeRequester != nil && *r.BeforeRequester != r.Actor && sourceSameOptional(r.RequesterRole, sourceManualString("finance_operator")) && sourceSameOptional(r.RequesterStatus, sourceManualString("active")) && sourceSameOptional(r.AfterRequester, r.BeforeRequester) && sourceSameOptional(r.AfterApprover, &r.Actor)
	case "release":
		state := "closed"
		if r.BeforeCaseState == "approved" {
			state = "approved"
		}
		return r.ActorRole == "finance_approver" && sourceOneOf(r.BeforeCaseState, "open", "proposed", "approved", "closed") && r.AfterCaseState == state && r.BeforeReservationStatus == "pending_reconciliation" && sourceSameOptional(r.AfterRequester, r.BeforeRequester) && sourceSameOptional(r.AfterApprover, r.BeforeApprover) && r.RequesterRole == nil && r.RequesterStatus == nil
	}
	return false
}
func sourceManualString(v string) *string { return &v }
func sourceManualLegacyPointer(c ReconciliationCaseRow) bool {
	return c.PPLastRootID == nil && c.PPLastRootStep == nil && c.PPLastSourceKind == nil && c.PPLastSourceID == nil && c.PPLastSourceRole == nil && c.PPLastCreatedXID == nil
}
func sourceManualCurrentState(c ReconciliationCaseRow, last ReconciliationSourceRow) bool {
	switch last.Action {
	case "propose":
		// Ordinary finish can close a non-approved case after the manual action.
		return c.State == "proposed" || c.State == "closed"
	case "approve":
		return c.State == "approved"
	case "release":
		if last.BeforeCaseState == "approved" {
			return c.State == "approved"
		}
		return c.State == "closed"
	}
	return false
}

// Proposal and actor fields survive all ordinary markPending/finish writes.
// Reason, request/billing status and all other financial projections do not.
func sourceManualCurrentProposal(w *replayWork, c ReconciliationCaseRow, p sourceManualProposal) (bool, error) {
	if !sourceSameOptional(c.EvidenceRef, p.evidence) || !sourceSameOptional(c.ProposalReason, p.reason) {
		return false, nil
	}
	absent := true
	for _, field := range p.usage {
		if field != nil {
			absent = false
		}
	}
	if absent {
		return c.ProposedUsage == nil, nil
	}
	if c.ProposedUsage == nil || p.usage[0] == nil {
		return false, nil
	}
	valid, err := sourceParseUsagePayload(w, *c.ProposedUsage, false)
	if err != nil || !valid {
		return false, err
	}
	var u struct {
		Status    string `json:"status"`
		Input     int64  `json:"input_tokens"`
		Output    int64  `json:"output_tokens"`
		Total     int64  `json:"total_tokens"`
		Cached    int64  `json:"cached_tokens"`
		Reasoning int64  `json:"reasoning_tokens"`
	}
	decoder := json.NewDecoder(&sourceJSONReader{w: w, raw: *c.ProposedUsage})
	if err := decoder.Decode(&u); err != nil {
		if budgetErr := w.b.check(); budgetErr != nil {
			return false, budgetErr
		}
		return false, nil
	}
	if u.Status != *p.usage[0] {
		return false, nil
	}
	got := [5]int64{u.Input, u.Output, u.Total, u.Cached, u.Reasoning}
	for i, field := range p.usage[1:] {
		if field == nil || !signedInteger(*field, 64) {
			return false, nil
		}
		want, _ := strconv.ParseInt(*field, 10, 64)
		if got[i] != want {
			return false, nil
		}
	}
	return true, w.b.check()
}

// The immutable business language supplies the partial order P -> A -> R.
// No timestamp, XID or UUID is used to order manual actions. Current pointers
// are checked separately from ordinary automatic changes to case reason/state.
func (s *sourceClosureState) reconciliationOrder() (sourceReconciliationOrderResult, error) {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return sourceReconciliationOrderResult{}, ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return sourceReconciliationOrderResult{}, err
	}
	result := sourceReconciliationOrderResult{LegacyCases: make(keySet)}
	cases, err := s.pointerIndex("reconciliation_cases", "id")
	if err != nil {
		return sourceReconciliationOrderResult{}, err
	}
	outcomes, err := s.pointerIndex("pp_customer_finish_outcomes", "reconciliation_source_id")
	if err != nil {
		return sourceReconciliationOrderResult{}, err
	}
	groups := map[string]*sourceManualGroup{}
	for _, raw := range s.tables["pp_reconciliation_sources"] {
		if err := s.w.tick(); err != nil {
			return sourceReconciliationOrderResult{}, err
		}
		r := raw.value.Interface().(ReconciliationSourceRow)
		key := tupleKey(r.CaseID)
		g := groups[key]
		if g == nil {
			g = &sourceManualGroup{}
			groups[key] = g
		}
		if !sourceManualLocal(r) {
			s.mark("scalar", "pp_reconciliation_sources", raw.key)
		}
		rank := sourceManualRank(r.Action)
		if rank < 0 {
			continue
		}
		g.actions[rank] = append(g.actions[rank], raw)
		if originals := cases[key]; len(originals) != 1 {
			s.mark("pointer", "pp_reconciliation_sources", raw.key)
		} else {
			c := originals[0].value.Interface().(ReconciliationCaseRow)
			if c.TenantID != r.TenantID || c.RequestID != r.RequestID || !(finite(c.CreatedAt) && finite(r.CaseCreatedAt) && c.CreatedAt.Time.Equal(r.CaseCreatedAt.Time)) {
				s.mark("pointer", "pp_reconciliation_sources", raw.key)
			}
		}
	}
	for key, g := range groups {
		if err := s.w.tick(); err != nil {
			return sourceReconciliationOrderResult{}, err
		}
		var chosen [3]*ReconciliationSourceRow
		lastRank := -1
		for rank, collection := range g.actions {
			if len(collection) > 0 {
				lastRank = rank
			}
			if len(collection) != 1 {
				for _, raw := range collection {
					if err := s.w.tick(); err != nil {
						return sourceReconciliationOrderResult{}, err
					}
					s.mark("pointer", "pp_reconciliation_sources", raw.key)
				}
				continue
			}
			r := collection[0].value.Interface().(ReconciliationSourceRow)
			chosen[rank] = &r
		}
		p, a, release := chosen[0], chosen[1], chosen[2]
		if a != nil {
			if len(g.actions[0]) == 0 {
				if len(cases[key]) == 1 {
					result.LegacyCases[key] = struct{}{}
				}
			} else if p != nil && (!sourceManualSameProposal(sourceManualInputProposal(*p), sourceManualCaseProposal(*a)) || !sourceSameOptional(a.BeforeRequester, &p.Actor) || !sourceSameOptional(a.BeforeApprover, p.AfterApprover)) {
				s.mark("pointer", "pp_reconciliation_sources", tupleKey(a.ID))
			}
		}
		if release != nil {
			legacyProposal := release.BeforeRequester != nil || release.BeforeApprover != nil || release.CaseUsageStatus != nil || release.CaseInputTokens != nil || release.CaseOutputTokens != nil || release.CaseTotalTokens != nil || release.CaseCachedTokens != nil || release.CaseReasoningTokens != nil || release.CaseEvidenceRef != nil || release.CaseProposalReason != nil
			if len(cases[key]) == 1 && len(g.actions[0]) == 0 && len(g.actions[1]) == 0 && (release.BeforeCaseState != "open" || legacyProposal) {
				result.LegacyCases[key] = struct{}{}
			}
			if a != nil {
				if release.BeforeCaseState != "approved" || !sourceManualSameProposal(sourceManualCaseProposal(*a), sourceManualCaseProposal(*release)) || !sourceSameOptional(release.BeforeRequester, a.AfterRequester) || !sourceSameOptional(release.BeforeApprover, a.AfterApprover) {
					s.mark("pointer", "pp_reconciliation_sources", tupleKey(release.ID))
				}
				os := outcomes[tupleKey(a.ID)]
				if len(os) != 1 {
					s.mark("pointer", "pp_reconciliation_sources", tupleKey(a.ID))
				} else {
					o := os[0].value.Interface().(CustomerFinishOutcomeRow)
					if !sourceSameOptional(a.CustomerFinishOutcomeID, &o.ID) || o.RootID != a.RootID || o.StepNo != a.StepNo || o.StepKind != "reconciliation" || o.SourceID != a.ID || o.CreatedXID != a.CreatedXID || o.TenantID != a.TenantID || o.RequestID != a.RequestID || o.CaseID != a.CaseID {
						s.mark("pointer", "pp_reconciliation_sources", tupleKey(a.ID))
					} else if o.ResultKind != "pending" || o.ResultStatus != "pending_reconciliation" || o.AfterReservationStatus != "pending_reconciliation" || o.AfterCaseState != "approved" || release.BeforeReservationStatus != o.AfterReservationStatus {
						s.mark("pointer", "pp_reconciliation_sources", tupleKey(release.ID))
					}
				}
			} else if p != nil && len(g.actions[1]) == 0 {
				if release.BeforeCaseState != "proposed" || !sourceManualSameProposal(sourceManualInputProposal(*p), sourceManualCaseProposal(*release)) || !sourceSameOptional(release.BeforeRequester, p.AfterRequester) || !sourceSameOptional(release.BeforeApprover, p.AfterApprover) {
					s.mark("pointer", "pp_reconciliation_sources", tupleKey(release.ID))
				}
			}
		}
		if originals := cases[key]; len(originals) == 1 && lastRank >= 0 && chosen[lastRank] != nil {
			raw, last := originals[0], *chosen[lastRank]
			c := raw.value.Interface().(ReconciliationCaseRow)
			slot := sourcePointerSlot{prefix: "pp_last_", role: "reconciliation", id: last.ID, parent: g.actions[lastRank][0]}
			valid := sourcePointerMatches(raw.value, slot) && sourceManualCurrentState(c, last) && sourceSameOptional(c.Requester, last.AfterRequester) && sourceSameOptional(c.Approver, last.AfterApprover)
			proposal := sourceManualCaseProposal(last)
			if p != nil {
				proposal = sourceManualInputProposal(*p)
			} else if a != nil {
				proposal = sourceManualCaseProposal(*a)
			} else if last.Action == "propose" {
				proposal = sourceManualInputProposal(last)
			}
			same, err := sourceManualCurrentProposal(s.w, c, proposal)
			if err != nil {
				return sourceReconciliationOrderResult{}, err
			}
			if !valid || !same {
				s.mark("pointer", "reconciliation_cases", raw.key)
			}
		}
	}
	for key, originals := range cases {
		if err := s.w.tick(); err != nil {
			return sourceReconciliationOrderResult{}, err
		}
		if _, found := groups[key]; found {
			continue
		}
		for _, raw := range originals {
			if err := s.w.tick(); err != nil {
				return sourceReconciliationOrderResult{}, err
			}
			c := raw.value.Interface().(ReconciliationCaseRow)
			if sourceManualLegacyPointer(c) {
				result.LegacyCases[key] = struct{}{}
			} else {
				s.mark("pointer", "reconciliation_cases", raw.key)
			}
		}
	}
	if err := s.w.b.check(); err != nil {
		return sourceReconciliationOrderResult{}, err
	}
	return result, nil
}
