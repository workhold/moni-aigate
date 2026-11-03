package procurementprefix

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
)

type acUsage struct {
	Status    string `json:"status"`
	Input     int64  `json:"input_tokens"`
	Output    int64  `json:"output_tokens"`
	Total     int64  `json:"total_tokens"`
	Cached    int64  `json:"cached_tokens"`
	Reasoning int64  `json:"reasoning_tokens"`
}

func acEvidenceDigest(i IncomingSourceRow) (string, bool) {
	input, a := acInt(i.InputTokens)
	output, b := acInt(i.OutputTokens)
	total, c := acInt(i.TotalTokens)
	cached, d := acInt(i.CachedTokens)
	reasoning, f := acInt(i.ReasoningTokens)
	if !a || !b || !c || !d || !f || input < 0 || output < 0 || total < 0 || cached < 0 || reasoning < 0 {
		return "", false
	}
	raw, err := json.Marshal(struct {
		Usage  acUsage
		Reason string
	}{acUsage{i.UsageStatus, input, output, total, cached, reasoning}, i.TransportEndReason})
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), true
}
func (e *acReplay) prepareEvidence() {
	groups := map[string][]EvidenceChoiceRow{}
	identities := map[string]ProcurementEvidenceIdentityRow{}
	for _, i := range e.rows.OriginalRows.ProcurementEvidenceIdentities {
		if !e.tick() {
			return
		}
		identities[tupleKey(i.AttemptID, i.EvidenceDigest)] = i
	}
	for _, c := range e.rows.PrefixRows.EvidenceChoices {
		if !e.tick() {
			return
		}
		key := tupleKey(c.AttemptID)
		groups[key] = append(groups[key], c)
	}
	for attempt, list := range groups {
		if err := replaySort(e.w, list, func(left, right EvidenceChoiceRow) bool {
			return e.node(left.RootID, left.StepNo, left.StepKind, left.ID, left.AttemptID) < e.node(right.RootID, right.StepNo, right.StepKind, right.ID, right.AttemptID)
		}); err != nil {
			e.err = err
			return
		}
		seen := map[string]EvidenceMemberRow{}
		var tail *string
		count := int64(0)
		previousNode := int64(0)
		for _, choice := range list {
			if !e.tick() {
				return
			}
			incoming, incomingOK := e.incoming[tupleKey(choice.IncomingSourceID)]
			meter, meterOK := e.meters[tupleKey(choice.MeterChoiceID)]
			digest, valid := acEvidenceDigest(incoming)
			ordinal := e.node(choice.RootID, choice.StepNo, choice.StepKind, choice.ID, choice.AttemptID)
			good := incomingOK && meterOK && valid && ordinal > previousNode && meter.IncomingSourceID == incoming.ID && acPtrEq(incoming.AttemptID, choice.AttemptID) && acPtrEq(meter.AttemptID, choice.AttemptID) && choice.IdentityAttemptID == choice.AttemptID && choice.IdentityDigest == digest && choice.EvidenceDigest == digest && choice.ForcedConflict == (meter.CollisionKind != "none") && choice.MemberCountBefore == strconv.FormatInt(count, 10) && acSamePtr(choice.MemberTailBefore, tail)
			previousNode = ordinal
			first, existing := seen[tupleKey(digest)]
			member, hasMember := e.members[tupleKey(choice.ID)]
			if existing {
				good = good && choice.Disposition == "existing_member" && !hasMember && choice.IdentityEventID == first.IdentityEventID && choice.MemberCountAfter == choice.MemberCountBefore && acSamePtr(choice.MemberTailAfter, tail)
			} else {
				count++
				good = good && choice.Disposition == "new_member" && hasMember && member.AttemptID == choice.AttemptID && member.MemberOrdinal == strconv.FormatInt(count, 10) && member.PreviousMemberOrdinal == strconv.FormatInt(count-1, 10) && acSamePtr(member.PreviousMemberID, tail) && member.IdentityDigest == digest && member.IdentityEventID == meter.MeterEventID && choice.IdentityEventID == meter.MeterEventID && member.RootID == choice.RootID && member.StepNo == choice.StepNo && member.SourceID == choice.ID && member.CreatedXID == choice.CreatedXID && member.StepKind == "evidence_choice"
				id := member.ID
				tail = &id
				good = good && choice.MemberCountAfter == strconv.FormatInt(count, 10) && acSamePtr(choice.MemberTailAfter, tail)
				seen[tupleKey(digest)] = member
				e.memberChains[attempt] = append(e.memberChains[attempt], member)
				e.memberNodeOrd[attempt] = append(e.memberNodeOrd[attempt], ordinal)
				identity, found := identities[tupleKey(choice.AttemptID, digest)]
				good = good && found && identity.EventID == member.IdentityEventID
			}
			e.evaluated[tupleKey(choice.ID)] = acEvidence{good: good, ordinal: ordinal, memberCount: count, memberTail: tail, incoming: incoming, meter: meter}
			if !good {
				e.bad(&e.result.Stats.EvidenceRows, "member", choice.ID, choice.AccountID)
			}
		}
	}
}
func (e *acReplay) membersBefore(attempt string, node int64) (int64, *string, bool) {
	if node <= 0 {
		return 0, nil, false
	}
	list := e.memberNodeOrd[tupleKey(attempt)]
	n := sort.Search(len(list), func(i int) bool { return list[i] >= node })
	if n == 0 {
		return 0, nil, true
	}
	id := e.memberChains[tupleKey(attempt)][n-1].ID
	return int64(n), &id, true
}
func (e *acReplay) evidenceDecision(c EvidenceChoiceRow, p acHold, cut int64) (decision, cause string, effective bool, existing *string) {
	evaluated := e.evaluated[tupleKey(c.ID)]
	if c.Disposition == "existing_member" {
		return "skip_existing_identity", "", false, nil
	}
	cause = "none"
	noConsumption := evaluated.incoming.TransportEndReason == "not_sent" || evaluated.incoming.TransportEndReason == "rejected_no_consumption"
	if c.ForcedConflict {
		cause = "incoming_meter_conflict"
	} else if p.state == "released" && evaluated.incoming.UsageStatus == "confirmed_final" && (acCmp(evaluated.incoming.InputTokens, "0") > 0 || acCmp(evaluated.incoming.OutputTokens, "0") > 0) && !noConsumption {
		cause = "released_positive_usage"
	} else if p.state == "settled" && acCmp(p.spent, "0") > 0 && noConsumption {
		cause = "settled_positive_no_consumption"
	}
	effective = cause != "none"
	if effective {
		if p.state == "disputed" {
			return "skip_already_disputed", cause, true, nil
		}
		event, found := e.eventKeys[tupleKey("conflict:"+c.AttemptID+":"+evaluated.meter.MeterEventID)]
		ordinal, tracked := e.eventOrd[tupleKey(event.ID)]
		if found && tracked && ordinal <= cut {
			id := event.ID
			return "skip_duplicate_dispute_event", cause, true, &id
		}
		return "dispute", cause, true, nil
	}
	if p.state == "settled" || p.state == "released" || p.state == "disputed" {
		return "skip_terminal_normal", cause, false, nil
	}
	if noConsumption {
		return "release_no_consumption", cause, false, nil
	}
	if evaluated.incoming.UsageStatus != "confirmed_final" {
		if p.state == "unknown" {
			return "skip_already_unknown", cause, false, nil
		}
		return "mark_unknown", cause, false, nil
	}
	return "confirm_cost", cause, false, nil
}
func (e *acReplay) choiceFields(c EvidenceChoiceRow, p acHold, decision, cause string, effective bool, existing *string) bool {
	good := c.ProcurementBeforeRevision == p.revision && c.ProcurementBeforeState == p.state && c.ProcurementBeforeHeld == p.held && c.ProcurementBeforeSpent == p.spent && c.ProcurementBeforeAnomaly == p.anomaly && c.Decision == decision && acSamePtr(c.ExistingDisputeEventID, existing)
	skip := map[string]string{"skip_existing_identity": "existing_identity", "skip_terminal_normal": "terminal_normal", "skip_already_disputed": "already_disputed", "skip_duplicate_dispute_event": "duplicate_dispute_event", "skip_already_unknown": "already_unknown"}
	if decision == "skip_existing_identity" {
		good = good && c.EffectiveConflict == nil && c.ConflictCause == nil
	} else {
		good = good && c.EffectiveConflict != nil && *c.EffectiveConflict == effective && acPtrEq(c.ConflictCause, cause)
	}
	if reason, skipped := skip[decision]; skipped {
		good = good && acPtrEq(c.SkipReason, reason) && c.ProcurementTransitionID == nil && c.ProcurementEventID == nil && c.AccountJournalID == nil
	} else {
		good = good && c.SkipReason == nil && c.ProcurementTransitionID != nil && c.ProcurementEventID != nil && c.AccountJournalID != nil
	}
	return good
}
func (e *acReplay) checkEvidenceCuts() {
	for _, choice := range e.rows.PrefixRows.EvidenceChoices {
		if !e.tick() {
			return
		}
		cut, ok := e.cut(choice.AccountID, choice.AccountOrdinalCut, choice.AccountTailCut)
		hold, present := e.holdAt(choice.AttemptID, cut.ordinal)
		evaluated, found := e.evaluated[tupleKey(choice.ID)]
		decision, cause, effective, existing := e.evidenceDecision(choice, hold, cut.ordinal)
		good := ok && present && found && evaluated.good && hold.account == choice.AccountID && e.observes(choice.AttemptID, choice.AccountID, cut.ordinal, evaluated.ordinal) && e.choiceFields(choice, hold, decision, cause, effective, existing)
		if choice.ProcurementTransitionID != nil {
			transition, exists := e.transitions[tupleKey(*choice.ProcurementTransitionID)]
			journal, journalExists := e.journals[tupleKey(acPtr(choice.AccountJournalID))]
			good = good && exists && journalExists && transition.CauseKind == "evidence_choice" && transition.CauseSourceID == choice.ID && transition.RootID == choice.RootID && journal.Ordinal == strconv.FormatInt(cut.ordinal+1, 10)
		}
		if !good {
			e.bad(&e.result.Stats.EvidenceRows, "choice", choice.ID, choice.AccountID)
		}
	}
	// A complete immutable identity set cannot contain members with no observed
	// choice, or original identities omitted from the complete member prefix.
	memberIdentities := make(keySet)
	for _, m := range e.rows.PrefixRows.EvidenceMembers {
		if !e.tick() {
			return
		}
		memberIdentities.add(m.AttemptID, m.IdentityDigest)
		if _, found := e.evaluated[tupleKey(m.EvidenceChoiceID)]; !found {
			account := acPtr(e.attempts[tupleKey(m.AttemptID)].AccountID)
			e.bad(&e.result.Stats.EvidenceRows, "orphan_member", m.ID, account)
		}
	}
	for _, i := range e.rows.OriginalRows.ProcurementEvidenceIdentities {
		if !e.tick() {
			return
		}
		if _, found := memberIdentities[tupleKey(i.AttemptID, i.EvidenceDigest)]; !found && i.PPSourceID != nil {
			account := acPtr(e.attempts[tupleKey(i.AttemptID)].AccountID)
			e.bad(&e.result.Stats.EvidenceRows, "identity", tupleKey(i.AttemptID, i.EvidenceDigest), account)
		}
	}
	for _, head := range e.rows.PrefixRows.AttemptHeads {
		if !e.tick() {
			return
		}
		origin := e.attempts[tupleKey(head.AttemptID)]
		if origin.ManagementMode != "managed" {
			continue
		}
		chain := e.memberChains[tupleKey(head.AttemptID)]
		var tail *string
		if len(chain) > 0 {
			id := chain[len(chain)-1].ID
			tail = &id
		}
		if !acPtrEq(head.MemberCount, strconv.Itoa(len(chain))) || !acSamePtr(head.MemberTailID, tail) {
			e.bad(&e.result.Stats.EvidenceRows, "member_head", head.AttemptID, acPtr(origin.AccountID))
		}
	}
}
