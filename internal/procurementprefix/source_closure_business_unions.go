package procurementprefix

import (
	"math/big"
	"reflect"
	"strings"
	"unicode"
)

// businessUnionRows adds independently calculated row-local rules to the
// physical and basic nullable checks. It does not authenticate historical cuts,
// replay a business domain, or grant source qualification.
func (s *sourceClosureState) businessUnionRows() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	for table, collection := range s.tables {
		if !strings.HasPrefix(table, "pp_") {
			continue
		}
		for _, raw := range collection {
			if err := s.w.tick(); err != nil {
				return err
			}
			if !sourceBusinessValues(raw.value.Interface()) {
				s.mark("scalar", table, raw.key)
			}
		}
	}
	return s.w.b.check()
}

func sourceBusinessInt(value string) (*big.Int, bool) {
	if !signedInteger(value, 64) {
		return nil, false
	}
	return numeric(value, false)
}

func sourceBusinessPositive(value string) (*big.Int, bool) {
	n, ok := sourceBusinessInt(value)
	return n, ok && n.Sign() > 0
}

func sourceBusinessUUID(value string) bool {
	return value == "00000000-0000-0000-0000-000000000000" || sourceNonzeroUUID(value)
}

func sourceBusinessDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for i := range value {
		if !(value[i] >= '0' && value[i] <= '9' || value[i] >= 'a' && value[i] <= 'f') {
			return false
		}
	}
	return true
}

func sourceBusinessUpper(input, output, inputRate, outputRate, upper string) bool {
	values := make([]*big.Int, 5)
	for i, raw := range []string{input, output, inputRate, outputRate, upper} {
		var ok bool
		values[i], ok = sourceBusinessInt(raw)
		if !ok || i == 0 && values[i].Sign() == 0 {
			return false
		}
	}
	// Round once after combining both dimensions, with no int64 intermediate.
	amount := new(big.Int).Mul(values[0], values[2])
	amount.Add(amount, new(big.Int).Mul(values[1], values[3]))
	amount.Add(amount, big.NewInt(999999))
	amount.Quo(amount, big.NewInt(1000000))
	return amount.Cmp(values[4]) == 0
}

func sourceBusinessUsage(r IncomingSourceRow) bool {
	if !sourceOneOf(r.UsageStatus, "confirmed_final", "provisional", "estimated", "missing", "conflicting") {
		return false
	}
	n := make([]*big.Int, 5)
	for i, value := range []string{r.InputTokens, r.OutputTokens, r.TotalTokens, r.CachedTokens, r.ReasoningTokens} {
		var ok bool
		n[i], ok = sourceBusinessInt(value)
		if !ok {
			return false
		}
	}
	return n[3].Cmp(n[0]) <= 0 && n[4].Cmp(n[1]) <= 0 && (r.UsageStatus != "confirmed_final" || new(big.Int).Add(n[0], n[1]).Cmp(n[2]) == 0)
}

func sourceBusinessValues(value any) bool {
	switch r := value.(type) {
	case RequestOriginRow:
		return r.PBCreatedXID == r.CreatedXID
	case AttemptOriginRow:
		if !sourceOneOf(r.Sequence, "1", "2") || r.PBCreatedXID != r.CreatedXID || !sourceOneOf(r.CostBasis, "unknown", "contract", "invoice") {
			return false
		}
		for _, rate := range []*string{r.CostInputRate, r.CostOutputRate} {
			if rate != nil {
				if _, ok := sourceBusinessInt(*rate); !ok {
					return false
				}
			}
		}
		if r.CostVersionID == nil && (r.CostInputRate != nil || r.CostOutputRate != nil || r.CostBasis != "unknown") {
			return false
		}
		return r.ManagementMode == "unmanaged" || r.ManagementMode == "managed" && r.CostVersionID != nil && r.CostInputRate != nil && r.CostOutputRate != nil && sourceOneOf(r.CostBasis, "contract", "invoice")
	case AttemptNodeRow:
		return sourceOneOf(r.StepKind, "attempt_delivery", "incoming", "meter_choice", "request_decision", "normal_cost_decision", "cost_mutation", "cost_reset_decision", "evidence_choice", "procurement_transition", "proposal_birth", "approval_consumed", "unmanaged_correction", "recovery", "cost_outbox")
	case AccountCommandSourceRow:
		return sourceBusinessUUID(r.OperationID) && sourceBusinessUUID(r.ActorID) && sourceBusinessDigest(r.InputDigest) && (r.Kind == "account_create" || r.Kind == "account_configure" && sourceBusinessUUID(r.InputTargetID))
	case AccountJournalRow:
		return sourceBusinessJournal(r)
	case AttemptDeliverySourceRow:
		switch r.Mode {
		case "dispatch_permit":
			return r.DispatchXID != nil && *r.DispatchXID == r.CreatedXID
		case "finalization":
			return sourceOneOf(r.AfterDeliveryState, "completed", "failed", "unknown")
		case "recovery":
			return sourceOneOf(r.AfterDeliveryState, "failed", "unknown")
		}
		return false
	case IncomingSourceRow:
		return sourceBusinessUsage(r) && sourceBusinessDigest(r.InputDigest) && finite(r.EffectiveObservedAt) && (nullTime(r.ObservedAtInput) || finite(r.ObservedAtInput) && r.ObservedAtInput.Time.Equal(r.EffectiveObservedAt.Time))
	case MeterChoiceRow:
		return r.MeterOriginID == r.MeterEventID && (r.CollisionKind != "other_confirmed_usage" || r.CompetingMeterID != nil && *r.CompetingMeterID != r.MeterEventID)
	case ConflictSourceRow:
		return r.SourceID == r.MeterChoiceID
	case RequestDecisionRow:
		if !sourceOneOf(r.BeforeStatus, "accepted", "dispatching", "succeeded", "failed", "cancelled") || !sourceOneOf(r.AfterStatus, "accepted", "dispatching", "succeeded", "failed", "cancelled") {
			return false
		}
		return r.Branch == "normal" && sourceOneOf(r.AfterStatus, "succeeded", "failed", "cancelled") || r.Branch == "conflict" && r.AfterStatus == r.BeforeStatus
	case NormalCostDecisionRow:
		if r.BeforeProcurementState != nil && !sourceState(*r.BeforeProcurementState) || r.SelectedBasis != nil && !sourceOneOf(*r.SelectedBasis, "contract", "invoice") {
			return false
		}
		if r.BeforeCostStatus != "unknown" {
			return r.Decision == "skip_already_known"
		}
		if r.ManagementMode == "managed" && r.BeforeProcurementState != nil && *r.BeforeProcurementState == "disputed" {
			return r.Decision == "skip_disputed"
		}
		if r.Decision == "write_no_consumption" {
			return r.SelectedAmount != nil && *r.SelectedAmount == "0" && r.SelectedBasis != nil && *r.SelectedBasis == "contract"
		}
		return sourceOneOf(r.Decision, "write_final_usage", "skip_no_final_usage_or_rate")
	case CostMutationRow:
		if sourceOneOf(r.CauseKind, "procurement_dispute_reset", "incoming_conflict_reset") {
			return r.AfterStatus == "unknown" && r.AfterAmount == nil
		}
		return sourceOneOf(r.CauseKind, "normal_cost_decision", "approval_consumed", "unmanaged_correction") && sourceOneOf(r.AfterStatus, "contract", "invoice") && r.AfterAmount != nil
	case CostResetDecisionRow:
		if r.ProcurementStateBefore != nil && !sourceState(*r.ProcurementStateBefore) {
			return false
		}
		if r.CauseKind == "procurement_dispute" {
			return r.ProcurementStateBefore != nil && r.Policy == "applied" && r.CostMutationID != nil
		}
		if r.CauseKind != "incoming_conflict" {
			return false
		}
		suppressed := r.ProcurementStateBefore != nil && sourceOneOf(*r.ProcurementStateBefore, "settled", "released")
		return suppressed && r.Policy == "suppressed_adjudicated" && r.CostMutationID == nil || !suppressed && r.Policy == "applied" && r.CostMutationID != nil
	case EvidenceChoiceRow:
		if !sourceState(r.ProcurementBeforeState) || r.IdentityAttemptID != r.AttemptID || r.IdentityDigest != r.EvidenceDigest || !sourceBusinessDigest(r.EvidenceDigest) {
			return false
		}
		before, beforeOK := sourceBusinessInt(r.MemberCountBefore)
		after, afterOK := sourceBusinessInt(r.MemberCountAfter)
		if !beforeOK || !afterOK {
			return false
		}
		return r.Disposition == "existing_member" && before.Cmp(after) == 0 && sourceSameOptional(r.MemberTailBefore, r.MemberTailAfter) || r.Disposition == "new_member" && new(big.Int).Add(before, big.NewInt(1)).Cmp(after) == 0 && r.MemberTailAfter != nil
	case EvidenceMemberRow:
		return sourceBusinessDigest(r.IdentityDigest) && r.SourceID == r.EvidenceChoiceID
	case ProcurementTransitionRow:
		return sourceBusinessTransition(r)
	case ApprovalConsumptionRow:
		before, ok := sourceBusinessReservation(r.ConsumedRevision, r.ConsumedState, r.ConsumedHeld, r.ConsumedSpent, r.ConsumedAnomaly)
		after, afterOK := sourceBusinessReservation(r.AfterRevision, r.AfterState, r.AfterHeld, r.AfterSpent, r.AfterAnomaly)
		return ok && afterOK && sourceOneOf(r.ConsumedState, "unknown", "settled", "released", "disputed") && r.AfterState == "settled" && after.held.Sign() == 0 && sourceBusinessNextReservation(before, after) && sourceBusinessUUID(r.ProposalID) && sourceBusinessUUID(r.ApprovalOperationID) && r.ProposalID != r.ApprovalOperationID
	case UnmanagedCorrectionSourceRow:
		if len(r.OperationID) < 1 || len(r.OperationID) > 200 || !sourceFinishReference(&r.EvidenceRef) {
			return false
		}
		if r.InputActorID == nil {
			return r.EffectiveAuditActor == "system"
		}
		return len(*r.InputActorID) > 0 && r.EffectiveAuditActor == *r.InputActorID
	case RecoverySourceRow:
		if !finite(r.CheckedAt) || !finite(r.RequestDeadline) || r.CheckedAt.Time.Before(r.RequestDeadline.Time) {
			return false
		}
		expired := r.ProcurementBeforeState == "prepared" && r.DeliveryBeforeState == "prepared" && r.PermitSourceID == nil
		return expired && r.Decision == "expired_without_permit" || !expired && r.Decision == "recovered_unknown"
	case CustomerFinishLedgerEffectRow:
		before, beforeOK := numeric(r.BeforeBalance, true)
		after, afterOK := numeric(r.AfterBalance, true)
		delta, deltaOK := numeric(r.SignedDelta, true)
		if !beforeOK || !afterOK || !deltaOK || !signedInteger(r.BeforeBalance, 64) || !signedInteger(r.AfterBalance, 64) || !signedInteger(r.SignedDelta, 64) || new(big.Int).Add(before, delta).Cmp(after) != 0 {
			return false
		}
		return r.AccountKind == "REVENUE" && r.AccountTenantID == nil || sourceOneOf(r.AccountKind, "HELD", "AVAILABLE") && r.AccountTenantID != nil && *r.AccountTenantID == r.TenantID && before.Sign() >= 0 && after.Sign() >= 0
	}
	// All other tables retain their existing independent scalar/nullable,
	// canonical, generated, original, or chain checks. This is not a proof gate.
	return true
}

type sourceBusinessP struct {
	revision, held, spent *big.Int
	state                 string
	anomaly               bool
}

func sourceBusinessReservation(revision, state, held, spent string, anomaly bool) (sourceBusinessP, bool) {
	rev, revOK := sourceBusinessPositive(revision)
	h, hOK := numeric(held, false)
	s, sOK := numeric(spent, false)
	return sourceBusinessP{rev, h, s, state, anomaly}, revOK && hOK && sOK && sourceState(state)
}

func sourceBusinessNextReservation(before, after sourceBusinessP) bool {
	change := before.state != after.state || before.held.Cmp(after.held) != 0 || before.spent.Cmp(after.spent) != 0 || before.anomaly != after.anomaly
	expected := new(big.Int).Set(before.revision)
	if change {
		expected.Add(expected, big.NewInt(1))
	}
	return after.revision.Cmp(expected) == 0
}

func sourceBusinessPInvariant(p sourceBusinessP, upper *big.Int) bool {
	if sourceOneOf(p.state, "prepared", "sending") && (p.held.Cmp(upper) != 0 || p.spent.Sign() != 0 || p.anomaly) {
		return false
	}
	return (!sourceOneOf(p.state, "settled", "released") || p.held.Sign() == 0) && (p.state != "released" || p.spent.Sign() == 0)
}

func sourceBusinessTransition(r ProcurementTransitionRow) bool {
	if !sourceOneOf(r.CostBasis, "contract", "invoice") || !sourceBusinessUpper(r.InputBound, r.OutputBound, r.CostInputRate, r.CostOutputRate, r.UpperAmount) || len(r.EvidenceRef) > 256 {
		return false
	}
	for _, c := range r.EvidenceRef {
		if unicode.IsControl(c) {
			return false
		}
	}
	upper, _ := sourceBusinessInt(r.UpperAmount)
	after, ok := sourceBusinessReservation(r.AfterRevision, r.AfterState, r.AfterHeld, r.AfterSpent, r.AfterAnomaly)
	heldDelta, heldOK := numeric(r.HeldDelta, true)
	spentDelta, spentOK := numeric(r.SpentDelta, true)
	if !ok || !heldOK || !spentOK || !sourceBusinessPInvariant(after, upper) {
		return false
	}
	if r.EventType == "reserved" {
		return r.CauseKind == "attempt_birth" && r.CauseSourceID == r.AttemptID && sourceFinishAll(false, r.BeforeRevision, r.BeforeState, r.BeforeHeld, r.BeforeSpent) && r.BeforeAnomaly == nil && r.AfterRevision == "1" && r.AfterState == "prepared" && r.EventKey == "reserve:"+r.AttemptID && r.EvidenceRef == "" && after.held.Cmp(upper) == 0 && after.spent.Sign() == 0 && !after.anomaly && heldDelta.Cmp(after.held) == 0 && spentDelta.Sign() == 0
	}
	if !sourceFinishAll(true, r.BeforeRevision, r.BeforeState, r.BeforeHeld, r.BeforeSpent) || r.BeforeAnomaly == nil {
		return false
	}
	before, beforeOK := sourceBusinessReservation(*r.BeforeRevision, *r.BeforeState, *r.BeforeHeld, *r.BeforeSpent, *r.BeforeAnomaly)
	if !beforeOK || !sourceBusinessPInvariant(before, upper) || !sourceBusinessNextReservation(before, after) || new(big.Int).Add(before.held, heldDelta).Cmp(after.held) != 0 || new(big.Int).Add(before.spent, spentDelta).Cmp(after.spent) != 0 {
		return false
	}
	switch r.EventType {
	case "released_no_consumption", "expired_without_permit":
		cause := r.CauseKind == "evidence_choice" && sourceOneOf(before.state, "prepared", "sending", "unknown")
		if r.EventType == "expired_without_permit" {
			cause = r.CauseKind == "recovery" && before.state == "prepared" && r.EvidenceRef == ""
		}
		return cause && after.state == "released" && after.held.Sign() == 0 && after.spent.Cmp(before.spent) == 0 && after.spent.Sign() == 0 && !after.anomaly && r.EventKey == "release:"+r.AttemptID
	case "consumption_unknown", "recovered_unknown":
		cause := r.CauseKind == "evidence_choice" && sourceOneOf(before.state, "prepared", "sending")
		if r.EventType == "recovered_unknown" {
			cause = r.CauseKind == "recovery" && sourceOneOf(before.state, "prepared", "sending") && r.EvidenceRef == ""
		}
		return cause && after.state == "unknown" && after.held.Cmp(before.held) == 0 && after.spent.Cmp(before.spent) == 0 && !after.anomaly && r.EventKey == "unknown:"+r.AttemptID
	case "cost_confirmed":
		// Token bounds can make a below-upper cost anomalous too. This row
		// proves only the amount implication; the joined usage proves the rest.
		return r.CauseKind == "evidence_choice" && sourceOneOf(before.state, "prepared", "sending", "unknown") && after.state == "settled" && after.held.Sign() == 0 && (after.spent.Cmp(upper) <= 0 || after.anomaly) && r.EventKey == "settle:"+r.AttemptID
	case "cost_corrected":
		return r.CauseKind == "approval_consumed" && sourceOneOf(before.state, "unknown", "settled", "released", "disputed") && after.state == "settled" && after.held.Sign() == 0 && after.anomaly == (after.spent.Cmp(upper) > 0) && strings.HasPrefix(r.EventKey, "correction:")
	case "evidence_disputed":
		expected := new(big.Int).Sub(upper, before.spent)
		if expected.Sign() < 0 {
			expected.SetInt64(0)
		}
		return r.CauseKind == "evidence_choice" && before.state != "disputed" && after.state == "disputed" && after.held.Cmp(expected) == 0 && after.spent.Cmp(before.spent) == 0 && after.anomaly && r.EventKey == "conflict:"+r.AttemptID+":"+r.EvidenceRef
	}
	return false
}

type sourceBusinessAccount struct {
	label, provider, state       string
	limit, revision, held, spent *big.Int
	blocking                     *big.Int
}

func sourceBusinessAccountValues(r reflect.Value, prefix string) (sourceBusinessAccount, bool) {
	var out sourceBusinessAccount
	texts := []*string{}
	for _, field := range []string{"label", "provider", "state", "limit_amount", "configuration_revision", "held_amount", "spent_amount", "blocking_count"} {
		v := sourceOptionalPointer(r, prefix+field)
		if v == nil {
			return out, false
		}
		texts = append(texts, v)
	}
	out.label, out.provider, out.state = *texts[0], *texts[1], *texts[2]
	if !sourceAccountText(out.label) || !sourceAccountText(out.provider) || !sourceOneOf(out.state, "active", "paused") {
		return out, false
	}
	values := []**big.Int{&out.limit, &out.revision, &out.held, &out.spent, &out.blocking}
	for i, target := range values {
		var ok bool
		if i == 2 || i == 3 {
			*target, ok = numeric(*texts[i+3], false)
		} else {
			*target, ok = sourceBusinessInt(*texts[i+3])
		}
		if !ok || i == 1 && (*target).Sign() == 0 {
			return out, false
		}
	}
	return out, true
}

func sourceBusinessSameConfig(before, after sourceBusinessAccount) bool {
	return before.label == after.label && before.provider == after.provider && before.state == after.state && before.limit.Cmp(after.limit) == 0
}

func sourceBusinessSameBuckets(before, after sourceBusinessAccount) bool {
	return before.held.Cmp(after.held) == 0 && before.spent.Cmp(after.spent) == 0 && before.blocking.Cmp(after.blocking) == 0
}

func sourceBusinessJournal(r AccountJournalRow) bool {
	v := reflect.ValueOf(r)
	after, ok := sourceBusinessAccountValues(v, "after_")
	if !ok {
		return false
	}
	config := sourceOneOf(r.Kind, "create", "configure", "configure_rejected")
	step := map[string]string{"command": "account_command", "budget_event": "procurement_transition", "dispatch": "attempt_delivery"}[r.SourceKind]
	if step == "" || r.StepKind != step || r.Kind == "dispatch" && (r.AttemptID == nil || *r.AttemptID != r.SourceID) {
		return false
	}
	if r.Kind == "create" {
		return r.SourceKind == "command" && after.revision.Cmp(big.NewInt(1)) == 0 && after.held.Sign() == 0 && after.spent.Sign() == 0 && after.blocking.Sign() == 0
	}
	before, beforeOK := sourceBusinessAccountValues(v, "before_")
	if !beforeOK {
		return false
	}
	expectedRevision := new(big.Int).Set(before.revision)
	if !sourceBusinessSameConfig(before, after) {
		expectedRevision.Add(expectedRevision, big.NewInt(1))
	}
	if after.revision.Cmp(expectedRevision) != 0 {
		return false
	}
	if config {
		return r.SourceKind == "command" && sourceBusinessSameBuckets(before, after) && (r.Kind != "configure_rejected" || sourceBusinessSameConfig(before, after))
	}
	fields := []*string{r.AfterReservationRevision, r.AfterReservationState, r.AfterReservationHeldAmount, r.AfterReservationSpentAmount, r.InputBound, r.OutputBound, r.UpperAmount, r.CostInputRate, r.CostOutputRate, r.CostBasis}
	if !sourceFinishAll(true, fields...) || r.AfterReservationAnomaly == nil || !sourceOneOf(*r.CostBasis, "contract", "invoice") || !sourceBusinessUpper(*r.InputBound, *r.OutputBound, *r.CostInputRate, *r.CostOutputRate, *r.UpperAmount) {
		return false
	}
	upper, _ := sourceBusinessInt(*r.UpperAmount)
	pAfter, pOK := sourceBusinessReservation(*r.AfterReservationRevision, *r.AfterReservationState, *r.AfterReservationHeldAmount, *r.AfterReservationSpentAmount, *r.AfterReservationAnomaly)
	if !pOK || !sourceBusinessPInvariant(pAfter, upper) {
		return false
	}
	pBefore := sourceBusinessP{big.NewInt(0), big.NewInt(0), big.NewInt(0), "", false}
	if r.Kind != "reserve" {
		if !sourceFinishAll(true, r.BeforeReservationRevision, r.BeforeReservationState, r.BeforeReservationHeldAmount, r.BeforeReservationSpentAmount) || r.BeforeReservationAnomaly == nil {
			return false
		}
		pBefore, pOK = sourceBusinessReservation(*r.BeforeReservationRevision, *r.BeforeReservationState, *r.BeforeReservationHeldAmount, *r.BeforeReservationSpentAmount, *r.BeforeReservationAnomaly)
		if !pOK || !sourceBusinessPInvariant(pBefore, upper) {
			return false
		}
	} else if pAfter.revision.Cmp(big.NewInt(1)) != 0 || pAfter.state != "prepared" || pAfter.held.Cmp(upper) != 0 || pAfter.spent.Sign() != 0 || pAfter.anomaly {
		return false
	}
	if sourceOneOf(r.Kind, "reserve", "dispatch") {
		if r.GateExtra == nil || !finite(r.GateCheckedAt) || !finite(r.PBDecisionAt) || before.state != "active" || before.blocking.Sign() != 0 {
			return false
		}
		extra, extraOK := sourceBusinessInt(*r.GateExtra)
		if !extraOK || r.Kind == "reserve" && extra.Cmp(upper) != 0 || r.Kind == "dispatch" && extra.Sign() != 0 || new(big.Int).Add(new(big.Int).Add(before.held, before.spent), extra).Cmp(before.limit) > 0 {
			return false
		}
	}
	if r.Kind == "dispatch" {
		return r.SourceKind == "dispatch" && sourceBusinessSameConfig(before, after) && sourceBusinessSameBuckets(before, after) && pBefore.state == "prepared" && pAfter.state == "sending" && sourceBusinessNextReservation(pBefore, pAfter) && pBefore.held.Cmp(upper) == 0 && pAfter.held.Cmp(upper) == 0 && pBefore.spent.Sign() == 0 && pAfter.spent.Sign() == 0 && !pBefore.anomaly && !pAfter.anomaly && r.PauseEventID == nil
	}
	if r.SourceKind != "budget_event" {
		return false
	}
	if r.Kind != "reserve" && !sourceBusinessNextReservation(pBefore, pAfter) {
		return false
	}
	if pAfter.anomaly {
		if r.PauseEventID == nil || r.ProcurementEventID == nil || *r.PauseEventID != *r.ProcurementEventID || after.state != "paused" {
			return false
		}
	} else if r.PauseEventID != nil || after.state != before.state {
		return false
	}
	expectedBlocking := new(big.Int).Set(before.blocking)
	if pBefore.anomaly && sourceOneOf(pBefore.state, "unknown", "disputed") {
		expectedBlocking.Sub(expectedBlocking, big.NewInt(1))
	}
	if pAfter.anomaly && sourceOneOf(pAfter.state, "unknown", "disputed") {
		expectedBlocking.Add(expectedBlocking, big.NewInt(1))
	}
	return before.label == after.label && before.provider == after.provider && before.limit.Cmp(after.limit) == 0 && expectedBlocking.Cmp(after.blocking) == 0 && new(big.Int).Sub(after.held, before.held).Cmp(new(big.Int).Sub(pAfter.held, pBefore.held)) == 0 && new(big.Int).Sub(after.spent, before.spent).Cmp(new(big.Int).Sub(pAfter.spent, pBefore.spent)) == 0
}
