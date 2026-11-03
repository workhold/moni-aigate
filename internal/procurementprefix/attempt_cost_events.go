package procurementprefix

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

type costHistoryUsage struct {
	Status    string `json:"status"`
	Input     int64  `json:"input_tokens"`
	Output    int64  `json:"output_tokens"`
	Total     int64  `json:"total_tokens"`
	Cached    int64  `json:"cached_tokens"`
	Reasoning int64  `json:"reasoning_tokens"`
}

func costHistoryDigest(in IncomingSourceRow) (string, bool) {
	vals := []string{in.InputTokens, in.OutputTokens, in.TotalTokens, in.CachedTokens, in.ReasoningTokens}
	nums := [5]int64{}
	for i, v := range vals {
		n, ok := costHistoryInt(v)
		if !ok || n < 0 {
			return "", false
		}
		nums[i] = n
	}
	raw, _ := json.Marshal(struct {
		Usage  costHistoryUsage
		Reason string
	}{costHistoryUsage{in.UsageStatus, nums[0], nums[1], nums[2], nums[3], nums[4]}, in.TransportEndReason})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), true
}
func costHistoryPMatches(revision, state, held, spent string, anomaly bool, p costHistoryP) bool {
	return p.present && p.revision == revision && p.state == state && p.held == held && p.spent == spent && p.anomaly == anomaly
}
func costHistoryPNext(old, p costHistoryP) string {
	if !old.present {
		return "1"
	}
	rev, ok := costHistoryInt(old.revision)
	if !ok || rev < 1 {
		return "!"
	}
	if old.state != p.state || old.held != p.held || old.spent != p.spent || old.anomaly != p.anomaly {
		if rev == int64(^uint64(0)>>1) {
			return "!"
		}
		rev++
	}
	return strconv.FormatInt(rev, 10)
}
func (c *costHistoryReplay) delivery(s *costHistoryPosition, n AttemptNodeRow) {
	d, found := c.deliveries[tupleKey(n.SourceID)]
	if !found {
		c.mark(&c.result.Stats.DeliveryRows, "source", n.SourceID, s.origin.AttemptID)
		return
	}
	target := ""
	good := found && d.BeforeDeliveryState == s.delivery
	switch d.Mode {
	case "dispatch_permit":
		target = "sending"
		good = good && s.delivery == "prepared" && s.permit == nil && d.CauseSourceID == nil && costHistorySame(d.DispatchXID, costHistoryPtr(d.CreatedXID)) && costHistoryFinite(d.PBDispatchCheckedAt)
		request := c.requests[tupleKey(s.origin.RequestID)]
		good = good && costHistoryFinite(request.RequestDeadline) && d.PBDispatchCheckedAt.Time.Before(request.RequestDeadline.Time)
		a := c.actual[tupleKey(s.origin.AttemptID)]
		good = good && costHistorySame(a.PPDispatchSourceID, costHistoryPtr(d.ID)) && costHistorySame(a.PPDispatchRootID, costHistoryPtr(d.RootID)) && costHistorySame(a.PPDispatchRootStep, costHistoryPtr(d.StepNo)) && costHistorySame(a.PPDispatchXID, costHistoryPtr(d.CreatedXID)) && costHistoryTime(a.PBDispatchCheckedAt, d.PBDispatchCheckedAt) && costHistorySame(a.PPDispatchAccountID, d.DispatchAccountID) && costHistorySame(a.PPDispatchJournalID, d.DispatchJournalID)
		if s.origin.ManagementMode == "managed" {
			good = good && s.p.present && s.p.state == "prepared" && costHistorySame(d.DispatchAccountID, s.origin.AccountID) && d.DispatchJournalID != nil
			s.p.state = "sending"
			s.p.revision = costHistoryPNext(costHistoryP{present: true, revision: s.p.revision, state: "prepared", held: s.p.held, spent: s.p.spent, anomaly: s.p.anomaly}, s.p)
		} else {
			good = good && d.DispatchAccountID == nil && d.DispatchJournalID == nil
		}
		s.permit = costHistoryPtr(d.ID)
	case "finalization":
		decision, ok := c.decisions[tupleKey(costHistoryText(d.CauseSourceID))]
		f, has := c.finals[tupleKey(costHistoryText(decision.FinalizationID))]
		good = good && ok && has && decision.Branch == "normal" && c.metaBefore(n, "request_decision", decision.ID) && f.RequestID == s.origin.RequestID && f.TenantID == s.origin.TenantID
		target = "unknown"
		if f.ProtocolOutcome == "completed" {
			target = "completed"
		} else if f.ProtocolOutcome == "failed" {
			target = "failed"
		}
		good = good && d.DispatchAccountID == nil && d.DispatchJournalID == nil && d.DispatchXID == nil && !d.PBDispatchCheckedAt.Valid
	case "recovery":
		r, ok := c.recoveries[tupleKey(costHistoryText(d.CauseSourceID))]
		good = good && ok && r.DeliveryMutationID == d.ID && r.DeliveryBeforeState == s.delivery && c.metaBefore(n, "recovery", r.ID)
		target = "unknown"
		if r.Decision == "expired_without_permit" {
			target = "failed"
		}
		good = good && d.DispatchAccountID == nil && d.DispatchJournalID == nil && d.DispatchXID == nil && !d.PBDispatchCheckedAt.Valid
	default:
		good = false
	}
	if !good || d.AfterDeliveryState != target {
		c.mark(&c.result.Stats.DeliveryRows, "source", d.ID, s.origin.AttemptID)
	}
	s.delivery, s.lastDelivery = target, costHistoryPtr(d.ID)
}
func (c *costHistoryReplay) normalDecision(s *costHistoryPosition, n AttemptNodeRow) {
	d, found := c.normal[tupleKey(n.SourceID)]
	if !found {
		c.mark(&c.result.Stats.NormalEligibilityRows, "decision", n.SourceID, s.origin.AttemptID)
		return
	}
	in, inOK := c.incoming[tupleKey(d.IncomingSourceID)]
	meter, meterOK := c.meters[tupleKey(d.MeterChoiceID)]
	f, finalOK := c.finals[tupleKey(d.FinalizationID)]
	good := found && inOK && meterOK && finalOK && costHistoryText(in.AttemptID) == s.origin.AttemptID && costHistoryText(meter.AttemptID) == s.origin.AttemptID && meter.IncomingSourceID == in.ID && meter.CollisionKind == "none" && f.EventID == meter.MeterEventID && f.RequestID == s.origin.RequestID && f.TenantID == s.origin.TenantID && d.BeforeCostStatus == s.status && costHistorySame(d.BeforeCostAmount, s.amount) && d.ManagementMode == s.origin.ManagementMode && c.metaBefore(n, "incoming", in.ID) && c.metaBefore(n, "meter_choice", meter.ID)
	decisions := c.finalDecisions[tupleKey(f.ID)]
	if len(decisions) != 1 {
		good = false
	} else {
		decision := decisions[0]
		deliveries := c.finalDeliveries[tupleKey(decision.ID)]
		good = good && decision.Branch == "normal" && decision.IncomingSourceID == in.ID && decision.MeterChoiceID == meter.ID && c.metaBefore(n, "request_decision", decision.ID) && len(deliveries) == 1
		if len(deliveries) == 1 {
			good = good && c.metaBefore(n, "attempt_delivery", deliveries[0].ID)
		}
	}
	if s.origin.ManagementMode == "managed" {
		good = good && d.BeforeProcurementAnomaly != nil && costHistoryPMatches(costHistoryText(d.BeforeProcurementRevision), costHistoryText(d.BeforeProcurementState), costHistoryText(d.BeforeProcurementHeld), costHistoryText(d.BeforeProcurementSpent), d.BeforeProcurementAnomaly != nil && *d.BeforeProcurementAnomaly, s.p) && costHistorySame(d.MemberCountBefore, costHistoryPtr(strconv.FormatInt(s.memberCount, 10))) && costHistorySame(d.MemberTailBefore, s.memberTail) && c.observe(s, n, costHistoryText(d.AccountID), costHistoryText(d.AccountOrdinalCut), costHistoryText(d.AccountTailCut))
	} else {
		good = good && d.BeforeProcurementRevision == nil && d.BeforeProcurementState == nil && d.BeforeProcurementHeld == nil && d.BeforeProcurementSpent == nil && d.BeforeProcurementAnomaly == nil && d.MemberCountBefore == nil && d.MemberTailBefore == nil && d.AccountID == nil && d.AccountOrdinalCut == nil && d.AccountTailCut == nil
	}
	decision := "skip_no_final_usage_or_rate"
	var amount, basis *string
	if s.status != "unknown" {
		decision = "skip_already_known"
	} else if s.origin.ManagementMode == "managed" && s.p.state == "disputed" {
		decision = "skip_disputed"
	} else if in.TransportEndReason == "not_sent" || in.TransportEndReason == "rejected_no_consumption" {
		decision = "write_no_consumption"
		amount, basis = costHistoryPtr("0"), costHistoryPtr("contract")
	} else if in.UsageStatus == "confirmed_final" && s.origin.CostInputRate != nil && s.origin.CostOutputRate != nil && s.origin.CostBasis != "unknown" {
		decision = "write_final_usage"
		value, valid := costHistoryPrice(in.InputTokens, in.OutputTokens, *s.origin.CostInputRate, *s.origin.CostOutputRate)
		good = good && valid
		amount, basis = costHistoryPtr(value), costHistoryPtr(s.origin.CostBasis)
	}
	good = good && d.Decision == decision && costHistorySame(d.SelectedAmount, amount) && costHistorySame(d.SelectedBasis, basis)
	if amount == nil {
		good = good && d.CostMutationID == nil && d.CostRecordedOutboxID == nil
	} else {
		good = good && d.CostMutationID != nil && d.CostRecordedOutboxID != nil && !s.recorded
		meta := c.meta[tupleKey("normal_cost_decision", d.ID)]
		c.expectMutation(meta, costHistoryText(d.CostMutationID), *basis, amount)
		c.expectOutbox(meta, costHistoryText(d.CostRecordedOutboxID), s.origin.AttemptID, "cost_recorded", *amount, *basis)
	}
	if !good {
		c.mark(&c.result.Stats.NormalEligibilityRows, "decision", d.ID, s.origin.AttemptID)
	}
}
func (c *costHistoryReplay) evidenceChoice(s *costHistoryPosition, n AttemptNodeRow) {
	e, found := c.evidence[tupleKey(n.SourceID)]
	if !found {
		c.mark(&c.result.Stats.CurrentCostRows, "evidence_choice", n.SourceID, s.origin.AttemptID)
		return
	}
	in, inOK := c.incoming[tupleKey(e.IncomingSourceID)]
	meter, meterOK := c.meters[tupleKey(e.MeterChoiceID)]
	digest, valid := costHistoryDigest(in)
	good := found && inOK && meterOK && valid && s.origin.ManagementMode == "managed" && costHistoryPMatches(e.ProcurementBeforeRevision, e.ProcurementBeforeState, e.ProcurementBeforeHeld, e.ProcurementBeforeSpent, e.ProcurementBeforeAnomaly, s.p) && e.EvidenceDigest == digest && e.IdentityDigest == digest && e.MemberCountBefore == strconv.FormatInt(s.memberCount, 10) && costHistorySame(e.MemberTailBefore, s.memberTail) && c.observe(s, n, e.AccountID, e.AccountOrdinalCut, e.AccountTailCut)
	good = good && meter.IncomingSourceID == in.ID && costHistoryText(in.AttemptID) == s.origin.AttemptID && costHistoryText(meter.AttemptID) == s.origin.AttemptID && c.metaBefore(n, "incoming", in.ID) && c.metaBefore(n, "meter_choice", meter.ID)
	previous, existing := s.identities[tupleKey(digest)]
	decision := ""
	cause := "none"
	noConsumption := in.TransportEndReason == "not_sent" || in.TransportEndReason == "rejected_no_consumption"
	if existing {
		decision = "skip_existing_identity"
		good = good && e.Disposition == "existing_member" && e.IdentityEventID == previous && e.EffectiveConflict == nil && e.ConflictCause == nil
	} else {
		good = good && e.Disposition == "new_member"
		s.memberCount++
		member, memberOK := c.members[tupleKey(e.ID)]
		good = good && memberOK && member.MemberOrdinal == strconv.FormatInt(s.memberCount, 10) && member.PreviousMemberOrdinal == strconv.FormatInt(s.memberCount-1, 10) && costHistorySame(member.PreviousMemberID, s.memberTail) && member.IdentityDigest == digest && member.IdentityEventID == meter.MeterEventID
		s.memberTail = costHistoryPtr(member.ID)
		s.identities[tupleKey(digest)] = meter.MeterEventID
		if meter.CollisionKind != "none" {
			cause = "incoming_meter_conflict"
		} else if s.p.state == "released" && in.UsageStatus == "confirmed_final" && (costHistoryCmp(in.InputTokens, "0") > 0 || costHistoryCmp(in.OutputTokens, "0") > 0) && !noConsumption {
			cause = "released_positive_usage"
		} else if s.p.state == "settled" && costHistoryCmp(s.p.spent, "0") > 0 && noConsumption {
			cause = "settled_positive_no_consumption"
		}
		if cause != "none" {
			if s.p.state == "disputed" {
				decision = "skip_already_disputed"
			} else if _, seen := s.disputes[tupleKey(meter.MeterEventID)]; seen {
				decision = "skip_duplicate_dispute_event"
			} else {
				decision = "dispute"
			}
		} else if s.p.state == "settled" || s.p.state == "released" || s.p.state == "disputed" {
			decision = "skip_terminal_normal"
		} else if noConsumption {
			decision = "release_no_consumption"
		} else if in.UsageStatus != "confirmed_final" {
			if s.p.state == "unknown" {
				decision = "skip_already_unknown"
			} else {
				decision = "mark_unknown"
			}
		} else {
			decision = "confirm_cost"
		}
		good = good && e.ForcedConflict == (meter.CollisionKind != "none") && e.EffectiveConflict != nil && *e.EffectiveConflict == (cause != "none") && costHistoryText(e.ConflictCause) == cause
	}
	good = good && e.Decision == decision && e.MemberCountAfter == strconv.FormatInt(s.memberCount, 10) && costHistorySame(e.MemberTailAfter, s.memberTail)
	if !good {
		c.mark(&c.result.Stats.CurrentCostRows, "attempt", s.origin.AttemptID, s.origin.AttemptID)
	}
	c.evidenceDecisions[tupleKey(e.ID)] = decision
}
func (c *costHistoryReplay) procurement(s *costHistoryPosition, n AttemptNodeRow) {
	t, found := c.transitions[tupleKey(n.SourceID)]
	if !found {
		c.mark(&c.result.Stats.CurrentCostRows, "procurement_transition", n.SourceID, s.origin.AttemptID)
		return
	}
	old := s.p
	after := old
	good := found && s.origin.ManagementMode == "managed" && t.AccountID == costHistoryText(s.origin.AccountID) && t.BindingID == costHistoryText(s.origin.BindingID)
	request := c.requests[tupleKey(s.origin.RequestID)]
	upper, priced := costHistoryPrice(request.InputUpperBound, request.OutputLimit, costHistoryText(s.origin.CostInputRate), costHistoryText(s.origin.CostOutputRate))
	good = good && priced
	if old.present {
		good = good && costHistoryPMatches(costHistoryText(t.BeforeRevision), costHistoryText(t.BeforeState), costHistoryText(t.BeforeHeld), costHistoryText(t.BeforeSpent), t.BeforeAnomaly != nil && *t.BeforeAnomaly, old) && t.BeforeAnomaly != nil
	}
	switch t.CauseKind {
	case "attempt_birth":
		good = good && !old.present && t.CauseSourceID == s.origin.AttemptID && t.RootID == s.origin.RootID && t.CreatedXID == s.origin.CreatedXID && costHistorySame(s.origin.ProcurementTransitionID, costHistoryPtr(t.ID)) && costHistorySame(s.origin.ReservedEventID, costHistoryPtr(t.EventID)) && costHistorySame(s.origin.AccountJournalID, costHistoryPtr(t.JournalID)) && t.EventType == "reserved" && t.BeforeRevision == nil && t.BeforeState == nil && t.BeforeHeld == nil && t.BeforeSpent == nil && t.BeforeAnomaly == nil
		after = costHistoryP{present: true, state: "prepared", held: upper, spent: "0"}
		_, bound := costHistoryInt(upper)
		good = good && bound
	case "evidence_choice":
		e, ok := c.evidence[tupleKey(t.CauseSourceID)]
		in := c.incoming[tupleKey(e.IncomingSourceID)]
		meter := c.meters[tupleKey(e.MeterChoiceID)]
		good = good && ok && c.metaBefore(n, "evidence_choice", e.ID) && costHistorySame(e.ProcurementTransitionID, costHistoryPtr(t.ID))
		switch c.evidenceDecisions[tupleKey(e.ID)] {
		case "dispute":
			after.state, after.anomaly = "disputed", true
			after.held = costHistorySubtract(upper, old.spent)
			if costHistoryCmp(upper, old.spent) < 0 {
				after.held = "0"
			}
			s.disputes.add(meter.MeterEventID)
		case "release_no_consumption":
			after.state, after.held, after.anomaly = "released", "0", false
		case "mark_unknown":
			after.state, after.anomaly = "unknown", false
		case "confirm_cost":
			amount, ok := costHistoryPrice(in.InputTokens, in.OutputTokens, costHistoryText(s.origin.CostInputRate), costHistoryText(s.origin.CostOutputRate))
			good = good && ok
			after.state, after.held, after.spent = "settled", "0", amount
			after.anomaly = costHistoryCmp(amount, upper) > 0 || costHistoryCmp(in.InputTokens, request.InputUpperBound) > 0 || costHistoryCmp(in.OutputTokens, request.OutputLimit) > 0
		default:
			good = false
		}
	case "approval_consumed":
		a, ok := c.approvals[tupleKey(t.CauseSourceID)]
		p, present := c.proposals[tupleKey(a.ProposalID)]
		good = good && ok && present && c.metaBefore(n, "approval_consumed", a.ID) && a.ProcurementTransitionID == t.ID
		after.state, after.held, after.spent = "settled", "0", p.Amount
		after.anomaly = costHistoryCmp(p.Amount, upper) > 0
	case "recovery":
		r, ok := c.recoveries[tupleKey(t.CauseSourceID)]
		good = good && ok && c.metaBefore(n, "recovery", r.ID) && r.ProcurementTransitionID == t.ID
		if r.Decision == "expired_without_permit" {
			after.state, after.held = "released", "0"
		} else {
			after.state = "unknown"
		}
		after.anomaly = false
	default:
		good = false
	}
	after.revision = costHistoryPNext(old, after)
	good = good && costHistoryPMatches(t.AfterRevision, t.AfterState, t.AfterHeld, t.AfterSpent, t.AfterAnomaly, after)
	if !good {
		c.mark(&c.result.Stats.CurrentCostRows, "attempt", s.origin.AttemptID, s.origin.AttemptID)
	}
	c.transitionBefore[tupleKey(t.ID)] = old
	s.p = after
}
func (c *costHistoryReplay) reset(s *costHistoryPosition, n AttemptNodeRow) {
	r, found := c.resets[tupleKey(n.SourceID)]
	if !found {
		c.mark(&c.result.Stats.ResetRows, "decision", n.SourceID, s.origin.AttemptID)
		return
	}
	policy := "applied"
	good := found
	var pstate *string
	if r.CauseKind == "procurement_dispute" {
		t, ok := c.transitions[tupleKey(r.CauseSourceID)]
		old := c.transitionBefore[tupleKey(t.ID)]
		good = good && ok && t.EventType == "evidence_disputed" && old.present && c.metaBefore(n, "procurement_transition", t.ID)
		pstate = costHistoryPtr(old.state)
	} else if r.CauseKind == "incoming_conflict" {
		source, ok := c.conflicts[tupleKey(r.CauseSourceID)]
		step, a := costHistoryInt(source.StepNo)
		current, b := costHistoryInt(n.StepNo)
		good = good && ok && source.RootID == n.RootID && source.CreatedXID == n.CreatedXID && costHistoryText(source.AttemptID) == s.origin.AttemptID && a && b && step < current
		if s.origin.ManagementMode == "managed" {
			pstate = costHistoryPtr(s.p.state)
			if s.p.state == "settled" || s.p.state == "released" {
				policy = "suppressed_adjudicated"
			}
		}
	} else {
		good = false
	}
	good = good && r.Policy == policy && costHistorySame(r.ProcurementStateBefore, pstate)
	if policy == "applied" {
		good = good && r.CostMutationID != nil
		meta := c.meta[tupleKey("cost_reset_decision", r.ID)]
		meta.kind = r.CauseKind + "_reset"
		c.expectMutation(meta, costHistoryText(r.CostMutationID), "unknown", nil)
	} else {
		good = good && r.CostMutationID == nil
	}
	if !good {
		c.mark(&c.result.Stats.ResetRows, "decision", r.ID, s.origin.AttemptID)
	}
}
func (c *costHistoryReplay) recovery(s *costHistoryPosition, n AttemptNodeRow) {
	r, found := c.recoveries[tupleKey(n.SourceID)]
	if !found {
		c.mark(&c.result.Stats.DeliveryRows, "recovery", n.SourceID, s.origin.AttemptID)
		return
	}
	request := c.requests[tupleKey(s.origin.RequestID)]
	good := found && s.origin.ManagementMode == "managed" && costHistoryPMatches(r.ProcurementBeforeRevision, r.ProcurementBeforeState, r.ProcurementBeforeHeld, r.ProcurementBeforeSpent, r.ProcurementBeforeAnomaly, s.p) && (s.p.state == "prepared" || s.p.state == "sending") && r.DeliveryBeforeState == s.delivery && costHistorySame(r.PermitSourceID, s.permit) && costHistoryFinite(r.CheckedAt) && costHistoryTime(r.RequestDeadline, request.RequestDeadline) && r.CheckedAt.Time.After(request.RequestDeadline.Time)
	expected := "recovered_unknown"
	if s.permit == nil && s.p.state == "prepared" && s.delivery == "prepared" {
		expected = "expired_without_permit"
	}
	good = good && r.Decision == expected
	journal, journaled := c.journals[tupleKey(r.AccountJournalID)]
	cut, parsed := costHistoryInt(journal.Ordinal)
	previous := c.journalCuts[tupleKey(journal.AccountID, strconv.FormatInt(cut-1, 10))]
	good = good && journaled && parsed && c.observe(s, n, journal.AccountID, strconv.FormatInt(cut-1, 10), previous.ID)
	if !good {
		c.mark(&c.result.Stats.DeliveryRows, "recovery", r.ID, s.origin.AttemptID)
	}
}
