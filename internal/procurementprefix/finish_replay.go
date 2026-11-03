package procurementprefix

// Reconciliation/finish is an independent private leaf. It validates recorded
// local observations and their actual outputs, never today's balance as a past
// before value. The unresolved cross-history cuts below keep it incomplete.
import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"unicode"
)

type reconciliationFinishResult struct {
	Stats         AttemptCost
	Complete      bool
	Unimplemented []string
}

type finishReplay struct {
	w                     *replayWork
	rows                  Rows
	err                   error
	hits                  [4]keySet
	sources               map[string]ReconciliationSourceRow
	outcomes              map[string]CustomerFinishOutcomeRow
	requests              map[string]RequestRecordRow
	reservations          map[string]ReservationRow
	reservationRequests   map[string]ReservationRow
	cases                 map[string]ReconciliationCaseRow
	finals                map[string]FinalizationRecordRow
	meters                map[string]MeteringEventRow
	incoming              map[string]IncomingSourceRow
	audits                map[string]AuditEventRow
	outbox                map[string]ProjectionOutboxRow
	settlements           map[string]SettlementRow
	journals              map[string]JournalRow
	lines                 map[string]JournalLineRow
	accounts              map[string]LedgerAccountRow
	accountKinds          map[string]LedgerAccountRow
	controls              map[string]PeriodicBudgetControlRow
	allocations           map[string]PeriodicBudgetAllocationRow
	events                map[string]PeriodicBudgetEventRow
	windows               map[string]PeriodicBudgetWindowRow
	blocks                map[string]BillingProfileBlockRow
	linesFor              map[string][]JournalLineRow
	ledgerFor             map[string][]CustomerFinishLedgerEffectRow
	pbFor                 map[string][]CustomerFinishPBEffectRow
	eventsFor             map[string][]PeriodicBudgetEventRow
	allocatedControls     keySet
	usedLines, usedEvents keySet
}

func (r *finishReplay) tick() bool {
	if r.err == nil {
		r.err = r.w.tick()
	}
	return r.err == nil
}
func (r *finishReplay) mark(kind int, table string, parts ...string) {
	r.hits[kind][tupleKey(table)+tupleKey(parts...)] = struct{}{}
}
func finishIndex[T any](r *finishReplay, rows []T, key func(T) string) map[string]T {
	if r.err != nil {
		return nil
	}
	v, err := requestIndex(r.w, rows, key)
	r.err = err
	return v
}

func checkReconciliationFinish(w *replayWork, rows Rows) (reconciliationFinishResult, error) {
	if err := w.tick(); err != nil {
		return reconciliationFinishResult{}, err
	}
	r := &finishReplay{w: w, rows: rows, linesFor: map[string][]JournalLineRow{}, ledgerFor: map[string][]CustomerFinishLedgerEffectRow{}, pbFor: map[string][]CustomerFinishPBEffectRow{}, eventsFor: map[string][]PeriodicBudgetEventRow{}, allocatedControls: make(keySet), usedLines: make(keySet), usedEvents: make(keySet)}
	for i := range r.hits {
		r.hits[i] = make(keySet)
	}
	r.index()
	for _, s := range rows.PrefixRows.ReconciliationSources {
		if !r.tick() {
			break
		}
		r.reconciliation(s)
	}
	for _, o := range rows.PrefixRows.CustomerFinishOutcomes {
		if !r.tick() {
			break
		}
		r.finish(o)
	}
	r.reverseEffects()
	if r.err != nil {
		return reconciliationFinishResult{}, r.err
	}
	if err := w.b.check(); err != nil {
		return reconciliationFinishResult{}, err
	}
	return reconciliationFinishResult{
		Stats: AttemptCost{ReconciliationRows: uint64(len(r.hits[0])), CustomerFinishRows: uint64(len(r.hits[1])), CustomerLedgerRows: uint64(len(r.hits[2])), CustomerPBRows: uint64(len(r.hits[3]))},
		Unimplemented: []string{
			"request historical latest/attempt-delivery cut for reconciliation and no-latest settlement absence",
			"mutable reconciliation last-source total order including intervening automatic customer flows",
			"historical customer-control absence and complete PB configuration/window closure",
		},
	}, nil
}

func (r *finishReplay) index() {
	p, o := r.rows.PrefixRows, r.rows.OriginalRows
	r.sources = finishIndex(r, p.ReconciliationSources, func(v ReconciliationSourceRow) string { return v.ID })
	r.outcomes = finishIndex(r, p.CustomerFinishOutcomes, func(v CustomerFinishOutcomeRow) string { return v.ID })
	r.requests = finishIndex(r, o.RequestRecords, func(v RequestRecordRow) string { return v.ID })
	r.reservations = finishIndex(r, o.Reservations, func(v ReservationRow) string { return v.ID })
	r.reservationRequests = finishIndex(r, o.Reservations, func(v ReservationRow) string { return tupleKey(v.TenantID, v.RequestID) })
	r.cases = finishIndex(r, o.ReconciliationCases, func(v ReconciliationCaseRow) string { return v.ID })
	r.finals = finishIndex(r, o.FinalizationRecords, func(v FinalizationRecordRow) string { return v.ID })
	r.meters = finishIndex(r, o.MeteringEvents, func(v MeteringEventRow) string { return v.ID })
	r.incoming = finishIndex(r, p.IncomingSources, func(v IncomingSourceRow) string { return v.ID })
	r.audits = finishIndex(r, o.AuditEvents, func(v AuditEventRow) string { return v.ID })
	r.outbox = finishIndex(r, o.ProjectionOutbox, func(v ProjectionOutboxRow) string { return v.EventID })
	r.settlements = finishIndex(r, o.Settlements, func(v SettlementRow) string { return v.ID })
	r.journals = finishIndex(r, o.Journals, func(v JournalRow) string { return v.ID })
	r.lines = finishIndex(r, o.JournalLines, func(v JournalLineRow) string { return v.ID })
	r.accounts = finishIndex(r, o.LedgerAccounts, func(v LedgerAccountRow) string { return v.ID })
	r.accountKinds = finishIndex(r, o.LedgerAccounts, func(v LedgerAccountRow) string { return finishAccountKey(v.TenantID, v.Kind) })
	r.controls = finishIndex(r, o.PeriodicBudgetControls, func(v PeriodicBudgetControlRow) string { return v.ID })
	r.allocations = finishIndex(r, o.PeriodicBudgetAllocations, func(v PeriodicBudgetAllocationRow) string { return v.ID })
	r.events = finishIndex(r, o.PeriodicBudgetEvents, func(v PeriodicBudgetEventRow) string { return v.ID })
	r.windows = finishIndex(r, o.PeriodicBudgetWindows, func(v PeriodicBudgetWindowRow) string { return v.ID })
	r.blocks = finishIndex(r, o.BillingProfileBlocks, func(v BillingProfileBlockRow) string { return tupleKey(v.ModelID, v.ProfileVersion) })
	for _, v := range o.JournalLines {
		if !r.tick() {
			return
		}
		r.linesFor[v.JournalID] = append(r.linesFor[v.JournalID], v)
	}
	for _, v := range p.CustomerFinishLedgerEffects {
		if !r.tick() {
			return
		}
		r.ledgerFor[v.FinishOutcomeID] = append(r.ledgerFor[v.FinishOutcomeID], v)
	}
	for _, v := range p.CustomerFinishPBEffects {
		if !r.tick() {
			return
		}
		r.pbFor[v.FinishOutcomeID] = append(r.pbFor[v.FinishOutcomeID], v)
	}
	for _, v := range o.PeriodicBudgetEvents {
		if !r.tick() {
			return
		}
		r.eventsFor[tupleKey(v.SourceKind, v.SourceID)] = append(r.eventsFor[tupleKey(v.SourceKind, v.SourceID)], v)
	}
	for _, v := range o.PeriodicBudgetAllocations {
		if !r.tick() {
			return
		}
		if v.SubjectType == "customer_request" {
			r.allocatedControls.add(v.SubjectID, v.ControlID)
		}
	}
}

func finishBounded(s string, limit int) bool { return validText(s) && len(s) > 0 && len(s) <= limit }
func (r *finishReplay) reference(s string) bool {
	if !finishBounded(s, 256) || strings.Contains(s, "://") {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "mg_") || strings.HasPrefix(lower, "bearer") {
		return false
	}
	for _, c := range s {
		if !r.tick() {
			return false
		}
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune(":_./-", c) {
			return false
		}
	}
	return true
}
func finishUsage(fields ...*string) (requestUsage, bool) {
	if len(fields) != 6 {
		return requestUsage{}, false
	}
	for _, v := range fields {
		if v == nil {
			return requestUsage{}, false
		}
	}
	return requestIncomingUsage(IncomingSourceRow{UsageStatus: *fields[0], InputTokens: *fields[1], OutputTokens: *fields[2], TotalTokens: *fields[3], CachedTokens: *fields[4], ReasoningTokens: *fields[5]})
}
func finishInput(s ReconciliationSourceRow) (requestUsage, bool) {
	return finishUsage(s.InputUsageStatus, s.InputInputTokens, s.InputOutputTokens, s.InputTotalTokens, s.InputCachedTokens, s.InputReasoningTokens)
}
func finishCase(s ReconciliationSourceRow) (requestUsage, bool) {
	return finishUsage(s.CaseUsageStatus, s.CaseInputTokens, s.CaseOutputTokens, s.CaseTotalTokens, s.CaseCachedTokens, s.CaseReasoningTokens)
}
func finishNil(fields ...*string) bool {
	for _, v := range fields {
		if v != nil {
			return false
		}
	}
	return true
}
func finishTimeSame(a, b Time) bool {
	return nullTime(a) && nullTime(b) || finite(a) && finite(b) && a.Time.Equal(b.Time)
}
func finishPosition(root, step, xid string, otherRoot, otherStep, kind, source, otherXID string, wantSource string) bool {
	return root == otherRoot && step == otherStep && xid == otherXID && kind == "reconciliation" && source == wantSource
}
func finishPointer(root, step, source, xid, role string, pr, ps, pk, pi, px, prole *string) bool {
	return requestPointer(pr, root) && requestPointer(ps, step) && requestPointer(pk, "reconciliation") && requestPointer(pi, source) && requestPointer(px, xid) && requestPointer(prole, role)
}

func (r *finishReplay) reconciliation(s ReconciliationSourceRow) {
	bad := func() { r.mark(0, "source", s.ID) }
	c, ok := r.cases[s.CaseID]
	q, qok := r.requests[s.RequestID]
	v, vok := r.reservationRequests[tupleKey(s.TenantID, s.RequestID)]
	if !ok || !qok || c.TenantID != s.TenantID || c.RequestID != s.RequestID || q.TenantID != s.TenantID || !finishTimeSame(c.CreatedAt, s.CaseCreatedAt) || s.StepKind != "reconciliation" || s.ActorStatus != "active" || !finishBounded(s.Actor, 65536) || !finite(s.ReservationDeadline) {
		bad()
	}
	if !vok || !finishTimeSame(v.Deadline, s.ReservationDeadline) {
		bad()
	}
	if s.PriorLatestID != nil {
		f, found := r.finals[*s.PriorLatestID]
		if !found || f.TenantID != s.TenantID || f.RequestID != s.RequestID {
			bad()
		}
	}
	if !requestOneOf(s.BeforeCaseState, "open", "proposed", "approved", "closed") {
		bad()
	}
	u, uok := finishInput(s)
	cu, cok := finishCase(s)
	inputAbsent := finishNil(s.InputUsageStatus, s.InputInputTokens, s.InputOutputTokens, s.InputTotalTokens, s.InputCachedTokens, s.InputReasoningTokens, s.InputEvidenceRef)
	caseAbsent := finishNil(s.CaseUsageStatus, s.CaseInputTokens, s.CaseOutputTokens, s.CaseTotalTokens, s.CaseCachedTokens, s.CaseReasoningTokens, s.CaseEvidenceRef, s.CaseProposalReason)
	if !caseAbsent && (!cok || cu.Status != "confirmed_final" || s.CaseEvidenceRef == nil || !r.reference(requestOptional(s.CaseEvidenceRef)) || s.CaseProposalReason == nil || !finishBounded(requestOptional(s.CaseProposalReason), 200)) {
		bad()
	}
	var payload any
	switch s.Action {
	case "propose":
		if s.ActorRole != "finance_operator" || s.BeforeCaseState != "open" || s.BeforeReservationStatus != "pending_reconciliation" || !uok || u.Status != "confirmed_final" || s.InputEvidenceRef == nil || !r.reference(requestOptional(s.InputEvidenceRef)) || s.InputReason == nil || !finishBounded(requestOptional(s.InputReason), 200) || s.AfterCaseState != "proposed" || !requestPointer(s.AfterRequester, s.Actor) || s.AfterApprover != nil || !nullTime(s.ApprovalCheckedAt) || !finishNil(s.DerivedIncomingSourceID, s.FinalizationID, s.CustomerFinishOutcomeID) || s.RequesterRole != nil || s.RequesterStatus != nil {
			bad()
		}
		payload = map[string]any{"usage": u, "evidence_ref": requestOptional(s.InputEvidenceRef), "reason": requestOptional(s.InputReason)}
	case "approve":
		if s.ActorRole != "finance_approver" || s.BeforeCaseState != "proposed" || s.BeforeRequester == nil || *s.BeforeRequester == s.Actor || !requestPointer(s.RequesterRole, "finance_operator") || !requestPointer(s.RequesterStatus, "active") || !cok || cu.Status != "confirmed_final" || caseAbsent || !requestSameOptional(s.AfterRequester, s.BeforeRequester) || !requestPointer(s.AfterApprover, s.Actor) || s.AfterCaseState != "approved" || !inputAbsent || s.InputReason != nil || !finite(s.ApprovalCheckedAt) {
			bad()
		}
		if finite(s.ApprovalCheckedAt) && finite(s.ReservationDeadline) {
			if s.ApprovalCheckedAt.Time.Before(s.ReservationDeadline.Time) {
				r.approvedIncoming(s, cu)
			} else if !finishNil(s.DerivedIncomingSourceID, s.FinalizationID) {
				bad()
			}
		}
		o, found := r.outcomes[requestOptional(s.CustomerFinishOutcomeID)]
		if !found {
			r.mark(1, "source_obligation", s.ID)
		}
		payload = map[string]any{"outcome": o.ResultStatus}
	case "release":
		if s.ActorRole != "finance_approver" || s.BeforeReservationStatus != "pending_reconciliation" || !inputAbsent || s.InputReason == nil || !finishBounded(requestOptional(s.InputReason), 200) || !nullTime(s.ApprovalCheckedAt) || !finishNil(s.DerivedIncomingSourceID, s.FinalizationID) || !requestSameOptional(s.AfterRequester, s.BeforeRequester) || !requestSameOptional(s.AfterApprover, s.BeforeApprover) || s.RequesterRole != nil || s.RequesterStatus != nil {
			bad()
		}
		want := "closed"
		if s.BeforeCaseState == "approved" {
			want = "approved"
		}
		if s.AfterCaseState != want {
			bad()
		}
		if _, found := r.outcomes[requestOptional(s.CustomerFinishOutcomeID)]; !found {
			r.mark(1, "source_obligation", s.ID)
		}
		payload = map[string]any{"reason": requestOptional(s.InputReason)}
	default:
		bad()
	}
	a, found := r.audits[s.AuditID]
	action := "reconciliation." + s.Action
	if s.Action == "release" {
		action = "reconciliation.released"
	}
	if s.Action == "propose" {
		action = "reconciliation.proposed"
	}
	if s.Action == "approve" {
		action = "reconciliation.approved"
	}
	if !found || a.Actor != s.Actor || !requestPointer(a.TenantScope, s.TenantID) || a.Action != action || a.Target != s.CaseID || a.RequestID != s.RequestID || !finishPointer(s.RootID, s.StepNo, s.ID, s.CreatedXID, "reconciliation", a.PPRootID, a.PPRootStep, a.PPSourceKind, a.PPSourceID, a.PPCreatedXID, a.PPSourceRole) || !r.jsonMatches(a.SanitizedDiff, payload) {
		bad()
	}
}

func (r *finishReplay) approvedIncoming(s ReconciliationSourceRow, u requestUsage) {
	bad := func() { r.mark(0, "source", s.ID) }
	in, ok := r.incoming[requestOptional(s.DerivedIncomingSourceID)]
	got, uok := requestIncomingUsage(in)
	f := FinalizationRecordRow{ProtocolOutcome: "unknown", TransportEndReason: "unknown", DeliveryStage: "unknown"}
	if s.PriorLatestID != nil {
		var found bool
		f, found = r.finals[*s.PriorLatestID]
		if !found || f.TenantID != s.TenantID || f.RequestID != s.RequestID {
			bad()
		}
	}
	if !ok || !uok || got != u || in.AttemptID != nil || in.Source != "approved_reconciliation" || in.SourceEventID != s.CaseID || in.TenantID != s.TenantID || in.RequestID != s.RequestID || in.RootID != s.RootID || in.CreatedXID != s.CreatedXID || !requestPointer(in.ReconcileSourceID, s.ID) || !requestPointer(s.FinalizationID, in.InputFinalizationID) || in.ProtocolOutcome != f.ProtocolOutcome || in.TransportEndReason != f.TransportEndReason || in.RawDeliveryStage != f.DeliveryStage || in.RawContentWritten != f.ContentWritten || in.FinishReason != f.FinishReason {
		bad()
	}
}

type finishDecision struct {
	kind, reason, status string
	raw                  *big.Int
	charge               int64
	known                bool
}

func finishZero(reason string) finishDecision {
	return finishDecision{kind: "new_settlement", reason: reason, status: "released", raw: new(big.Int), known: true}
}
func finishPending(reason string) finishDecision {
	return finishDecision{kind: "pending", reason: reason, status: "pending_reconciliation", raw: new(big.Int), known: true}
}
func finishAmount(usage requestUsage, inputRate, outputRate string) (*big.Int, bool) {
	i, iok := finishNonnegative(inputRate)
	o, ook := finishNonnegative(outputRate)
	if !iok || !ook {
		return nil, false
	}
	n := new(big.Int).Add(new(big.Int).Mul(big.NewInt(usage.InputTokens), i), new(big.Int).Mul(big.NewInt(usage.OutputTokens), o))
	n.Add(n, big.NewInt(999999))
	n.Quo(n, big.NewInt(1000000))
	return n, len(n.String()) <= 100
}
func finishNonnegative(s string) (*big.Int, bool) {
	if !signedInteger(s, 64) {
		return nil, false
	}
	v, ok := new(big.Int).SetString(s, 10)
	return v, ok && v.Sign() >= 0
}
func (r *finishReplay) decision(s ReconciliationSourceRow, o CustomerFinishOutcomeRow, q RequestRecordRow, R int64) finishDecision {
	if s.Action == "release" {
		return finishZero("released_reconciliation")
	}
	if !finite(o.SettlementDecisionCheckedAt) || !finite(o.ReservationDeadline) {
		return finishDecision{}
	}
	if !o.SettlementDecisionCheckedAt.Time.Before(o.ReservationDeadline.Time) {
		return finishZero("released_deadline")
	}
	if o.LatestFinalizationID == nil {
		// A current attempt state cannot prove what was visible at this historical
		// no-latest cut. The two possible original branches remain explicit.
		return finishDecision{}
	}
	f, ok := r.finals[*o.LatestFinalizationID]
	if !ok || f.TenantID != o.TenantID || f.RequestID != o.RequestID {
		return finishDecision{}
	}
	cancel := f.TransportEndReason == "client_cancelled"
	if !cancel && !f.ContentWritten && f.DeliveryStage != "unknown" {
		return finishZero("released_no_delivery")
	}
	if !cancel && !f.ContentWritten && f.DeliveryStage == "unknown" {
		return finishPending("delivery_unknown")
	}
	m, ok := r.meters[f.EventID]
	u, uok := r.parseUsage(m.NormalizedUsage)
	if !ok || m.TenantID != o.TenantID || m.RequestID != o.RequestID {
		r.mark(1, "outcome", o.ID)
	}
	if !ok || !uok || u.Status != "confirmed_final" {
		return finishPending("usage_unconfirmed")
	}
	if f.ProtocolOutcome != "completed" && !f.ContentWritten && !cancel {
		return finishPending("delivery_unknown")
	}
	raw, valid := finishAmount(u, q.InputRate, q.OutputRate)
	if !valid {
		return finishDecision{}
	}
	charge := R
	if raw.Cmp(big.NewInt(R)) < 0 {
		charge = raw.Int64()
	}
	return finishDecision{kind: "new_settlement", reason: "charged_usage", status: "settled", raw: raw, charge: charge, known: true}
}

func (r *finishReplay) finish(o CustomerFinishOutcomeRow) {
	bad := func() { r.mark(1, "outcome", o.ID) }
	s, sok := r.sources[o.ReconciliationSourceID]
	q, qok := r.requests[o.RequestID]
	v, vok := r.reservations[o.CustomerReservationID]
	if !sok || !qok || !vok || s.Action == "propose" || !requestPointer(s.CustomerFinishOutcomeID, o.ID) || o.SourceID != s.ID || !finishPosition(s.RootID, s.StepNo, s.CreatedXID, o.RootID, o.StepNo, o.StepKind, o.SourceID, o.CreatedXID, s.ID) || s.CaseID != o.CaseID || s.TenantID != o.TenantID || s.RequestID != o.RequestID || q.TenantID != o.TenantID || v.TenantID != o.TenantID || v.RequestID != o.RequestID || v.Amount != o.ReservationAmount || !finishTimeSame(v.Deadline, o.ReservationDeadline) || !finishTimeSame(s.ReservationDeadline, o.ReservationDeadline) || o.BeforeReservationStatus != s.BeforeReservationStatus || o.BeforeCaseReason != s.BeforeCaseReason {
		bad()
	}
	R, Rok := finishNonnegative(o.ReservationAmount)
	raw, rawOK := numeric(o.RawAmount, false)
	waived, waivedOK := numeric(o.WaivedAmount, false)
	charge, chargeOK := finishNonnegative(o.ChargedAmount)
	if !Rok || !rawOK || !waivedOK || !chargeOK {
		bad()
		return
	}
	if charge.Cmp(R) > 0 || new(big.Int).Sub(new(big.Int).Set(raw), charge).Cmp(waived) != 0 {
		bad()
	}
	beforeCase := s.BeforeCaseState
	if s.Action == "approve" {
		beforeCase = "approved"
	}
	if o.BeforeCaseState != beforeCase {
		bad()
	}
	wantLatest := s.PriorLatestID
	if s.FinalizationID != nil {
		wantLatest = s.FinalizationID
	}
	if !requestSameOptional(o.LatestFinalizationID, wantLatest) {
		bad()
	}
	if s.Action == "approve" && o.SelectedMode != "recovery" || s.Action == "release" && o.SelectedMode != "release" {
		bad()
	}
	if finite(s.ApprovalCheckedAt) && finite(o.SettlementDecisionCheckedAt) && o.SettlementDecisionCheckedAt.Time.Before(s.ApprovalCheckedAt.Time) {
		bad()
	}
	if finite(o.FinancialCheckedAt) && finite(o.SettlementDecisionCheckedAt) && o.FinancialCheckedAt.Time.Before(o.SettlementDecisionCheckedAt.Time) {
		bad()
	}
	if s.Action == "release" && !nullTime(o.SettlementDecisionCheckedAt) {
		bad()
	}
	if o.ResultKind == "existing_settlement" {
		if s.Action == "release" {
			// Released-hold retries return before allocating a new source. A
			// pending release calls finish directly and must create a settlement.
			bad()
		}
		r.existing(o, raw, charge, waived)
		return
	}
	d := r.decision(s, o, q, R.Int64())
	if s.Action == "approve" && !finite(o.SettlementDecisionCheckedAt) {
		bad()
	}
	if !d.known {
		// Only the original no-latest alternatives are admissible while the
		// absent/sent cut is still unimplemented. No candidate is issued.
		if o.LatestFinalizationID == nil && (o.SelectedReason == "released_no_delivery" || o.SelectedReason == "delivery_unknown") {
			if o.SelectedReason == "released_no_delivery" {
				d = finishZero(o.SelectedReason)
			} else {
				d = finishPending(o.SelectedReason)
			}
		} else {
			bad()
		}
	}
	if o.ResultKind == "pending" {
		if !d.known || d.kind != "pending" || o.SelectedReason != d.reason || o.ResultStatus != "pending_reconciliation" || !nullTime(o.FinancialCheckedAt) || raw.Sign() != 0 || charge.Sign() != 0 || waived.Sign() != 0 || !finishNil(o.SettlementID, o.CustomerJournalID, o.BillingAuditID, o.BillingOutboxID, o.CustomerTenantControlID, o.CustomerKeyControlID) || !r.noProfile(o) {
			bad()
		}
		wantReservation, wantBilling := o.BeforeReservationStatus, o.BeforeBillingStatus
		reason := o.BeforeCaseReason
		if !requestOneOf(o.BeforeReservationStatus, "settled", "released") {
			wantReservation = "pending_reconciliation"
			wantBilling = "pending_reconciliation"
			if reason != "metering_conflict" {
				reason = o.SelectedReason
			}
		}
		if o.AfterReservationStatus != wantReservation || o.AfterBillingStatus != wantBilling || o.AfterRequestStatus != o.BeforeRequestStatus || o.AfterCaseState != o.BeforeCaseState || o.AfterCaseReason != reason || o.AfterKeyHeld != o.BeforeKeyHeld || o.AfterKeySpent != o.BeforeKeySpent {
			bad()
		}
		return
	}
	if o.ResultKind != "new_settlement" || !finite(o.FinancialCheckedAt) || !finite(o.ReservationDeadline) {
		bad()
		return
	}
	if requestOneOf(o.BeforeReservationStatus, "settled", "released") {
		bad()
	}
	if !o.FinancialCheckedAt.Time.Before(o.ReservationDeadline.Time) {
		d = finishZero("released_deadline")
	}
	if !d.known || d.kind != "new_settlement" || o.SelectedReason != d.reason || o.ResultStatus != d.status || raw.Cmp(d.raw) != 0 || charge.Int64() != d.charge {
		bad()
	}
	if o.AfterReservationStatus != o.ResultStatus || o.AfterBillingStatus != o.ResultStatus || o.AfterCaseReason != o.BeforeCaseReason {
		bad()
	}
	wantState := o.BeforeCaseState
	if wantState != "approved" {
		wantState = "closed"
	}
	if o.AfterCaseState != wantState {
		bad()
	}
	wantRequest := o.BeforeRequestStatus
	if o.ResultStatus == "released" && requestOneOf(wantRequest, "accepted", "dispatching") {
		wantRequest = "failed"
	}
	if o.AfterRequestStatus != wantRequest {
		bad()
	}
	bh, bhok := finishNonnegative(o.BeforeKeyHeld)
	bs, bsok := finishNonnegative(o.BeforeKeySpent)
	ah, ahok := finishNonnegative(o.AfterKeyHeld)
	as, asok := finishNonnegative(o.AfterKeySpent)
	if !bhok || !bsok || !ahok || !asok || new(big.Int).Sub(bh, R).Cmp(ah) != 0 || new(big.Int).Add(bs, charge).Cmp(as) != 0 {
		bad()
	}
	r.settlement(o, true)
	r.billingOutputs(o)
	r.profile(o, q, raw, R, waived)
	r.ledger(o, R, charge)
	r.periodic(o, q, R, charge)
}

func (r *finishReplay) existing(o CustomerFinishOutcomeRow, raw, charge, waived *big.Int) {
	bad := func() { r.mark(1, "outcome", o.ID) }
	if !nullTime(o.SettlementDecisionCheckedAt) || !nullTime(o.FinancialCheckedAt) || !finishNil(o.CustomerJournalID, o.CustomerTenantControlID, o.CustomerKeyControlID, o.BillingAuditID, o.BillingOutboxID) || !r.noProfile(o) || o.BeforeReservationStatus != o.AfterReservationStatus || o.BeforeRequestStatus != o.AfterRequestStatus || o.BeforeBillingStatus != o.AfterBillingStatus || o.BeforeCaseState != o.AfterCaseState || o.BeforeCaseReason != o.AfterCaseReason || o.BeforeKeyHeld != o.AfterKeyHeld || o.BeforeKeySpent != o.AfterKeySpent {
		bad()
	}
	if raw.Sign() < 0 || charge.Sign() < 0 || waived.Sign() < 0 {
		bad()
	}
	r.settlement(o, false)
}
func (r *finishReplay) settlement(o CustomerFinishOutcomeRow, newRow bool) {
	s, ok := r.settlements[requestOptional(o.SettlementID)]
	if !ok || s.TenantID != o.TenantID || s.RequestID != o.RequestID || s.ChargedAmount != o.ChargedAmount || s.RawRetailAmount != o.RawAmount || s.WaivedRetailAmount != o.WaivedAmount || s.Status != o.ResultStatus || s.Reason != o.SelectedReason {
		r.mark(1, "outcome", o.ID)
		return
	}
	if newRow && (!requestSameOptional(s.FinalizationID, o.LatestFinalizationID) || !requestPointer(o.CustomerJournalID, s.JournalID) || !finishPointer(o.RootID, o.StepNo, o.SourceID, o.CreatedXID, "settlement", s.PPRootID, s.PPRootStep, s.PPSourceKind, s.PPSourceID, s.PPCreatedXID, s.PPSourceRole)) {
		r.mark(1, "outcome", o.ID)
	}
	// refunded_amount and current Key/ledger totals belong to later transactions.
}
func (r *finishReplay) noProfile(o CustomerFinishOutcomeRow) bool {
	return finishNil(o.ProfileBlockModelID, o.ProfileBlockProfileVersion, o.ProfileBlockDisposition, o.ProfileBlockAuditID)
}
func (r *finishReplay) profile(o CustomerFinishOutcomeRow, q RequestRecordRow, raw, R, waived *big.Int) {
	bad := func() { r.mark(1, "outcome", o.ID) }
	if raw.Cmp(R) <= 0 {
		if !r.noProfile(o) {
			bad()
		}
		return
	}
	b, ok := r.blocks[tupleKey(q.ModelID, q.ProfileVersion)]
	if !ok || b.Reason != "usage_exceeded_reservation" || !requestPointer(o.ProfileBlockModelID, q.ModelID) || !requestPointer(o.ProfileBlockProfileVersion, q.ProfileVersion) || o.ProfileBlockDisposition == nil || !requestOneOf(*o.ProfileBlockDisposition, "created", "reused") || *o.ProfileBlockDisposition == "created" && b.RequestID != o.RequestID {
		bad()
	}
	a, ok := r.audits[requestOptional(o.ProfileBlockAuditID)]
	if !ok || a.Actor != "system" || !requestPointer(a.TenantScope, o.TenantID) || a.Action != "billing.profile_blocked" || a.Target != q.ModelID || a.RequestID != o.RequestID || !finishPointer(o.RootID, o.StepNo, o.SourceID, o.CreatedXID, "profile_block", a.PPRootID, a.PPRootStep, a.PPSourceKind, a.PPSourceID, a.PPCreatedXID, a.PPSourceRole) || !r.jsonMatches(a.SanitizedDiff, map[string]any{"waived": waived.String()}) {
		bad()
	}
}
func (r *finishReplay) billingOutputs(o CustomerFinishOutcomeRow) {
	bad := func() { r.mark(1, "outcome", o.ID) }
	a, ok := r.audits[requestOptional(o.BillingAuditID)]
	if !ok || a.Actor != "system" || !requestPointer(a.TenantScope, o.TenantID) || a.Action != "billing."+o.ResultStatus || !requestPointer(o.SettlementID, a.Target) || a.RequestID != o.RequestID || !finishPointer(o.RootID, o.StepNo, o.SourceID, o.CreatedXID, "settlement", a.PPRootID, a.PPRootStep, a.PPSourceKind, a.PPSourceID, a.PPCreatedXID, a.PPSourceRole) || !r.jsonMatches(a.SanitizedDiff, map[string]any{"amount": o.ChargedAmount, "reason": o.SelectedReason}) {
		bad()
	}
	b, ok := r.outbox[requestOptional(o.BillingOutboxID)]
	amount, _ := new(big.Int).SetString(o.ReservationAmount, 10)
	charge, _ := new(big.Int).SetString(o.ChargedAmount, 10)
	if !ok || b.TenantID != o.TenantID || !requestPointer(o.SettlementID, b.OperationID) || b.Kind != o.ResultStatus || b.SchemaVersion != "1" || !finishPointer(o.RootID, o.StepNo, o.SourceID, o.CreatedXID, "settlement", b.PPRootID, b.PPRootStep, b.PPSourceKind, b.PPSourceID, b.PPCreatedXID, b.PPSourceRole) || !r.jsonMatches(b.SanitizedPayload, map[string]any{"amount": o.ChargedAmount, "released": new(big.Int).Sub(amount, charge).String(), "request_id": o.RequestID}) {
		bad()
	}
}

// Keep standard JSON numeric semantics for payload comparison while rejecting
// duplicate decoded keys, Unicode replacement, nested surplus and extra roots.
func (r *finishReplay) jsonMatches(raw string, expected any) bool {
	got, ok := r.strictJSON(raw)
	if !ok {
		return false
	}
	b, err := json.Marshal(expected)
	if err != nil {
		return false
	}
	want, ok := r.strictJSON(string(b))
	return ok && r.jsonEqual(got, want)
}
func (r *finishReplay) parseUsage(raw string) (requestUsage, bool) {
	v, ok := r.strictJSON(raw)
	m, mok := v.(map[string]any)
	if !ok || !mok || len(m) != 6 {
		return requestUsage{}, false
	}
	b, err := json.Marshal(m)
	if err != nil {
		return requestUsage{}, false
	}
	return requestParseUsage(string(b))
}

// strconv is retained here for bounded int64 checks used by the effect file.
func finishInt(s string) (int64, bool) {
	if !signedInteger(s, 64) {
		return 0, false
	}
	v, e := strconv.ParseInt(s, 10, 64)
	return v, e == nil
}
