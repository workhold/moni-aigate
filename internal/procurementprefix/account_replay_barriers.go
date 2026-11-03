package procurementprefix

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

func acReference(s string) bool {
	if len(s) < 1 || len(s) > 256 || strings.Contains(s, "://") {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "mg_") || strings.HasPrefix(lower, "bearer") {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(":_./-", r) {
			return false
		}
	}
	return true
}
func (e *acReplay) proposalBad(id, account string) {
	e.bad(&e.result.Stats.ProposalRows, "proposal", id, account)
	e.badProposals.add(id)
}
func (e *acReplay) approvalBad(id, proposal, account string) {
	e.bad(&e.result.Stats.ApprovalRows, "approval", id, account)
	e.badProposals.add(proposal)
}
func (e *acReplay) checkProposals() {
	birthsByProposal := map[string]ProposalBirthRow{}
	approvalsByProposal := map[string][]ApprovalConsumptionRow{}
	for _, a := range e.rows.PrefixRows.ApprovalConsumptions {
		if !e.tick() {
			return
		}
		k := tupleKey(a.ProposalID)
		approvalsByProposal[k] = append(approvalsByProposal[k], a)
	}
	for _, birth := range e.rows.PrefixRows.ProposalBirths {
		if !e.tick() {
			return
		}
		proposal, found := e.proposals[tupleKey(birth.ProposalID)]
		_, duplicate := birthsByProposal[tupleKey(birth.ProposalID)]
		birthsByProposal[tupleKey(birth.ProposalID)] = birth
		cut, valid := e.cut(birth.AccountID, birth.AccountOrdinalCut, birth.AccountTailCut)
		p, held := e.holdAt(birth.AttemptID, cut.ordinal)
		node := e.node(birth.RootID, birth.StepNo, birth.StepKind, birth.ID, birth.AttemptID)
		count, tail, members := e.membersBefore(birth.AttemptID, node)
		amount, money := acInt(proposal.Amount)
		good := found && !duplicate && valid && held && members && e.observes(birth.AttemptID, birth.AccountID, cut.ordinal, node) && money && amount >= 0 && (proposal.Basis == "contract" || proposal.Basis == "invoice") && acReference(proposal.EvidenceRef) && len(proposal.Reason) >= 1 && len(proposal.Reason) <= 200 && birth.RequesterRole == "finance_operator" && birth.RequesterStatus == "active" && proposal.Requester == birth.Requester && proposal.TenantID == birth.TenantID && proposal.AttemptID == birth.AttemptID && p.account == birth.AccountID && p.state != "prepared" && p.state != "sending" && birth.ReservationRevision == p.revision && birth.ReservationState == p.state && birth.ReservationHeld == p.held && birth.ReservationSpent == p.spent && birth.ReservationAnomaly == p.anomaly && birth.MemberCount == strconv.FormatInt(count, 10) && acSamePtr(birth.MemberTail, tail) && proposal.ExpectedReservationRevision == p.revision && proposal.ExpectedEvidenceCount == birth.MemberCount && (acCmp(proposal.Amount, p.spent) >= 0 || proposal.Basis == "invoice")
		audit, audited := e.audit[tupleKey(birth.AuditID)]
		good = good && audited && audit.Actor == birth.Requester && audit.Action == "procurement.cost_proposed" && audit.Target == proposal.ID && acPtrEq(audit.TenantScope, birth.TenantID) && audit.RequestID == birth.RequestID && e.jsonMatches(audit.SanitizedDiff, map[string]any{"attempt_id": birth.AttemptID, "amount": json.Number(proposal.Amount), "basis": proposal.Basis, "evidence_ref": proposal.EvidenceRef})
		if !good {
			e.proposalBad(birth.ProposalID, birth.AccountID)
		}
		approvals := approvalsByProposal[tupleKey(birth.ProposalID)]
		if proposal.State == "approved" {
			if len(approvals) != 1 {
				e.bad(&e.result.Stats.ApprovalRows, "approval_obligation", birth.ProposalID, birth.AccountID)
				e.badProposals.add(birth.ProposalID)
			}
		} else if proposal.State != "proposed" || len(approvals) != 0 || proposal.Approver != nil || proposal.ApprovalOperationID != nil || proposal.ApprovedAt.Valid {
			e.proposalBad(birth.ProposalID, birth.AccountID)
		}
	}
	for _, approval := range e.rows.PrefixRows.ApprovalConsumptions {
		if !e.tick() {
			return
		}
		proposal, found := e.proposals[tupleKey(approval.ProposalID)]
		birth, born := e.births[tupleKey(approval.ProposalBirthID)]
		cut, valid := e.cut(approval.AccountID, approval.AccountOrdinalCut, approval.AccountTailCut)
		p, held := e.holdAt(approval.AttemptID, cut.ordinal)
		node := e.node(approval.RootID, approval.StepNo, approval.StepKind, approval.ID, approval.AttemptID)
		count, tail, members := e.membersBefore(approval.AttemptID, node)
		good := found && born && valid && held && members && e.observes(approval.AttemptID, approval.AccountID, cut.ordinal, node) && birth.ProposalID == approval.ProposalID && birth.AttemptID == approval.AttemptID && birth.AccountID == approval.AccountID && approval.Requester == birth.Requester && approval.Requester == proposal.Requester && approval.RequesterRole == "finance_operator" && approval.RequesterStatus == "active" && approval.ApproverRole == "finance_approver" && approval.ApproverStatus == "active" && approval.Requester != approval.Approver && proposal.State == "approved" && acPtrEq(proposal.Approver, approval.Approver) && acPtrEq(proposal.ApprovalOperationID, approval.ApprovalOperationID) && acFinite(proposal.ApprovedAt) && p.state != "prepared" && p.state != "sending" && approval.ConsumedRevision == p.revision && approval.ConsumedState == p.state && approval.ConsumedHeld == p.held && approval.ConsumedSpent == p.spent && approval.ConsumedAnomaly == p.anomaly && approval.ConsumedMemberCount == strconv.FormatInt(count, 10) && acSamePtr(approval.ConsumedMemberTail, tail) && approval.ConsumedRevision == birth.ReservationRevision && approval.ConsumedMemberCount == birth.MemberCount && acSamePtr(approval.ConsumedMemberTail, birth.MemberTail) && (acCmp(proposal.Amount, p.spent) >= 0 || proposal.Basis == "invoice")
		transition, exists := e.transitions[tupleKey(approval.ProcurementTransitionID)]
		correction, corrected := e.corrections[tupleKey(approval.ProviderCorrectionID)]
		good = good && exists && transition.CauseKind == "approval_consumed" && transition.CauseSourceID == approval.ID && transition.RootID == approval.RootID && transition.EventID == approval.ProcurementEventID && transition.EventType == "cost_corrected" && transition.AfterRevision == approval.AfterRevision && transition.AfterState == approval.AfterState && transition.AfterHeld == approval.AfterHeld && transition.AfterSpent == approval.AfterSpent && transition.AfterAnomaly == approval.AfterAnomaly && corrected && correction.TenantID == approval.TenantID && correction.AttemptID == approval.AttemptID && correction.OperationID == "procurement:"+approval.ApprovalOperationID && correction.Amount == proposal.Amount && correction.Basis == proposal.Basis && correction.EvidenceRef == proposal.EvidenceRef
		journal, present := e.journals[tupleKey(transition.JournalID)]
		good = good && present && journal.Ordinal == strconv.FormatInt(cut.ordinal+1, 10)
		audit, audited := e.audit[tupleKey(approval.AuditID)]
		good = good && audited && audit.Actor == approval.Approver && audit.Action == "procurement.cost_approved" && audit.Target == approval.ProposalID && acPtrEq(audit.TenantScope, approval.TenantID) && audit.RequestID == approval.RequestID && e.jsonMatches(audit.SanitizedDiff, map[string]any{"attempt_id": approval.AttemptID, "amount": json.Number(proposal.Amount), "evidence_ref": proposal.EvidenceRef})
		if !good {
			e.approvalBad(approval.ID, approval.ProposalID, approval.AccountID)
		}
	}
	for _, proposal := range e.rows.OriginalRows.ProcurementCostProposals {
		if !e.tick() {
			return
		}
		if proposal.PPSourceID != nil {
			if _, found := birthsByProposal[tupleKey(proposal.ID)]; !found {
				origin := e.attempts[tupleKey(proposal.AttemptID)]
				e.proposalBad(proposal.ID, acPtr(origin.AccountID))
			}
		}
	}
}

func (e *acReplay) checkRecoveryCuts() {
	deliveries := map[string][]AttemptDeliverySourceRow{}
	for _, d := range e.rows.PrefixRows.AttemptDeliverySources {
		if !e.tick() {
			return
		}
		k := tupleKey(d.AttemptID)
		deliveries[k] = append(deliveries[k], d)
	}
	type deliveryCut struct {
		node   int64
		state  string
		permit *string
		good   bool
	}
	prefixes := map[string][]deliveryCut{}
	for key, list := range deliveries {
		if err := replaySort(e.w, list, func(left, right AttemptDeliverySourceRow) bool {
			return e.node(left.RootID, left.StepNo, left.StepKind, left.ID, left.AttemptID) < e.node(right.RootID, right.StepNo, right.StepKind, right.ID, right.AttemptID)
		}); err != nil {
			e.err = err
			return
		}
		// Keep the full immutable delivery prefix once per attempt. Each recovery
		// then observes the cut before its node in logarithmic time, rather than
		// scanning the same history for every recovery source.
		cut := deliveryCut{state: "prepared", good: true}
		prefix := make([]deliveryCut, 0, len(list))
		for _, d := range list {
			if !e.tick() {
				return
			}
			node := e.node(d.RootID, d.StepNo, d.StepKind, d.ID, d.AttemptID)
			if node <= cut.node || d.BeforeDeliveryState != cut.state {
				cut.good = false
			}
			cut.node, cut.state = node, d.AfterDeliveryState
			if d.Mode == "dispatch_permit" {
				if cut.permit != nil {
					cut.good = false
				}
				id := d.ID
				cut.permit = &id
			}
			prefix = append(prefix, cut)
		}
		prefixes[key] = prefix
	}
	for _, source := range e.rows.PrefixRows.RecoverySources {
		if !e.tick() {
			return
		}
		transition, exists := e.transitions[tupleKey(source.ProcurementTransitionID)]
		journal, journalExists := e.journals[tupleKey(source.AccountJournalID)]
		ordinal, ordOK := acInt(journal.Ordinal)
		p, held := e.holdAt(source.AttemptID, ordinal-1)
		sourceNode := e.node(source.RootID, source.StepNo, source.StepKind, source.ID, source.AttemptID)
		cut := deliveryCut{state: "prepared", good: true}
		prefix := prefixes[tupleKey(source.AttemptID)]
		index := sort.Search(len(prefix), func(i int) bool { return prefix[i].node >= sourceNode })
		if index > 0 {
			cut = prefix[index-1]
		}
		state, permit := cut.state, cut.permit
		good := exists && journalExists && ordOK && held && sourceNode > 0 && cut.good && e.observes(source.AttemptID, transition.AccountID, ordinal-1, sourceNode)
		good = good && source.DeliveryBeforeState == state && acSamePtr(source.PermitSourceID, permit) && source.ProcurementBeforeRevision == p.revision && source.ProcurementBeforeState == p.state && source.ProcurementBeforeHeld == p.held && source.ProcurementBeforeSpent == p.spent && source.ProcurementBeforeAnomaly == p.anomaly && transition.CauseKind == "recovery" && transition.CauseSourceID == source.ID && transition.JournalID == source.AccountJournalID && transition.EventID == source.ProcurementEventID
		mutation, found := e.deliveries[tupleKey(source.DeliveryMutationID)]
		expected := "unknown"
		decision := "recovered_unknown"
		if permit == nil && p.state == "prepared" && state == "prepared" {
			expected = "failed"
			decision = "expired_without_permit"
		}
		good = good && found && mutation.Mode == "recovery" && acPtrEq(mutation.CauseSourceID, source.ID) && mutation.RootID == source.RootID && mutation.AttemptID == source.AttemptID && mutation.BeforeDeliveryState == state && mutation.AfterDeliveryState == expected && source.Decision == decision
		if !good {
			e.bad(&e.result.Stats.RecoveryRows, "recovery", source.ID, transition.AccountID)
		}
	}
}

func (e *acReplay) checkObservationOrder() {
	type observation struct {
		node, cut         int64
		kind, id, account string
	}
	groups := map[string][]observation{}
	add := func(attempt, account, kind, id, root, step, stepKind, ordinal string) {
		node := e.node(root, step, stepKind, id, attempt)
		cut, valid := acInt(ordinal)
		if node > 0 && valid && cut > 0 {
			key := tupleKey(attempt)
			groups[key] = append(groups[key], observation{node: node, cut: cut, kind: kind, id: id, account: account})
		}
	}
	for _, source := range e.rows.PrefixRows.EvidenceChoices {
		if !e.tick() {
			return
		}
		add(source.AttemptID, source.AccountID, "evidence", source.ID, source.RootID, source.StepNo, source.StepKind, source.AccountOrdinalCut)
	}
	for _, source := range e.rows.PrefixRows.ProposalBirths {
		if !e.tick() {
			return
		}
		add(source.AttemptID, source.AccountID, "proposal", source.ID, source.RootID, source.StepNo, source.StepKind, source.AccountOrdinalCut)
	}
	for _, source := range e.rows.PrefixRows.ApprovalConsumptions {
		if !e.tick() {
			return
		}
		add(source.AttemptID, source.AccountID, "approval", source.ID, source.RootID, source.StepNo, source.StepKind, source.AccountOrdinalCut)
	}
	for _, source := range e.rows.PrefixRows.RecoverySources {
		if !e.tick() {
			return
		}
		transition, found := e.transitions[tupleKey(source.ProcurementTransitionID)]
		journal, present := e.journals[tupleKey(source.AccountJournalID)]
		ordinal, valid := acInt(journal.Ordinal)
		if found && present && valid && ordinal > 1 {
			add(source.AttemptID, transition.AccountID, "recovery", source.ID, source.RootID, source.StepNo, source.StepKind, strconv.FormatInt(ordinal-1, 10))
		}
	}
	for _, list := range groups {
		if err := replaySort(e.w, list, func(left, right observation) bool { return left.node < right.node }); err != nil {
			e.err = err
			return
		}
		for index, source := range list {
			if !e.tick() {
				return
			}
			if index == 0 {
				continue
			}
			previous := list[index-1]
			if source.node > previous.node && source.cut >= previous.cut && source.account == previous.account {
				continue
			}
			e.badAccounts.add(previous.account)
			switch source.kind {
			case "evidence":
				e.bad(&e.result.Stats.EvidenceRows, "choice", source.id, source.account)
			case "proposal":
				birth := e.births[tupleKey(source.id)]
				e.proposalBad(birth.ProposalID, source.account)
			case "approval":
				approval := e.approvals[tupleKey(source.id)]
				e.approvalBad(source.id, approval.ProposalID, source.account)
			case "recovery":
				e.bad(&e.result.Stats.RecoveryRows, "recovery", source.id, source.account)
			}
		}
	}
}

func (e *acReplay) checkCurrent() {
	for attempt, versions := range e.holds {
		if !e.tick() {
			return
		}
		if len(versions) > 0 {
			if _, exists := e.reservations[attempt]; !exists {
				last := versions[len(versions)-1].hold
				e.bad(&e.result.Stats.CurrentProjectionRows, "missing_original_p", last.attempt, last.account)
			}
		}
	}

	for _, p := range e.rows.OriginalRows.ProcurementReservations {
		if !e.tick() {
			return
		}
		versions := e.holds[tupleKey(p.AttemptID)]
		if len(versions) == 0 {
			if p.PPCreatedXID != nil || len(e.cuts[tupleKey(p.AccountID)]) > 0 {
				e.bad(&e.result.Stats.CurrentProjectionRows, "missing_p", p.AttemptID, p.AccountID)
			}
			continue
		}
		expected := versions[len(versions)-1].hold
		first := versions[0].hold
		if p.AccountID != expected.account || p.BindingID != expected.binding || p.InputBound != expected.input || p.OutputBound != expected.output || p.UpperAmount != expected.upper || p.HeldAmount != expected.held || p.SpentAmount != expected.spent || p.State != expected.state || p.Anomaly != expected.anomaly || p.Revision != expected.revision || !acPtrEq(p.PPLastJournalID, expected.journal) || !acPtrEq(p.PPOriginJournalID, first.journal) {
			e.bad(&e.result.Stats.CurrentProjectionRows, "p_current", p.AttemptID, p.AccountID)
		}
	}
	for _, origin := range e.rows.PrefixRows.AttemptOrigins {
		if !e.tick() {
			return
		}
		versions := e.holds[tupleKey(origin.AttemptID)]
		if origin.ManagementMode == "managed" {
			if len(versions) == 0 {
				e.bad(&e.result.Stats.CurrentProjectionRows, "missing_managed", origin.AttemptID, acPtr(origin.AccountID))
			}
		} else if len(versions) > 0 {
			e.bad(&e.result.Stats.CurrentProjectionRows, "unmanaged_p", origin.AttemptID, versions[0].hold.account)
		}
	}
	for _, t := range e.rows.PrefixRows.ProcurementTransitions {
		if !e.tick() {
			return
		}
		if _, found := e.eventOrd[tupleKey(t.EventID)]; !found {
			e.bad(&e.result.Stats.TransitionRows, "unreplayed", t.ID, t.AccountID)
		}
	}
	for _, j := range e.rows.PrefixRows.AccountJournal {
		if !e.tick() {
			return
		}
		if _, exists := e.origins[tupleKey(j.AccountID)]; !exists {
			e.bad(&e.result.Stats.JournalRows, "orphan_journal", j.ID, j.AccountID)
		}
	}
	for _, event := range e.rows.OriginalRows.ProcurementBudgetEvents {
		if !e.tick() {
			return
		}
		if event.PPTransitionID != nil {
			if _, found := e.eventOrd[tupleKey(event.ID)]; !found {
				e.bad(&e.result.Stats.TransitionRows, "unreplayed_original", event.ID, event.AccountID)
			}
		}
	}
}
