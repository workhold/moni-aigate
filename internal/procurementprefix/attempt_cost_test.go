package procurementprefix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Expected prices/statuses are supplied explicitly by tests; fixture construction
// never calls the checker, SQL, a billing price helper, or a writer state machine.
type historyFixture struct {
	panomaly                                                 bool
	managed                                                  bool
	account, binding, pstate, plast, lastIncoming, lastMeter string
	ph, ps, pr, accountOrdinal, memberCount                  int
	memberTail                                               *string

	rows                                                                          Rows
	seq, node, step                                                               int
	root, lastNode, attempt, request, tenant, channel, version, credential, quote string
	at                                                                            Time
	status, delivery                                                              string
	amount, lastMutation, lastDelivery, permit                                    *string
}

func (f *historyFixture) id() string {
	f.seq++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", f.seq)
}
func historyJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
func historyPtr(v string) *string { return &v }
func newHistoryFixture(quote bool) *historyFixture {
	f := &historyFixture{at: Time{Valid: true, Time: time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC)}, status: "unknown", delivery: "prepared", tenant: "tenant-a"}
	f.attempt, f.request, f.channel, f.version, f.credential, f.root = f.id(), f.id(), f.id(), f.id(), f.id(), f.id()
	origin := AttemptOriginRow{AttemptID: f.attempt, RootID: f.root, StepNo: "1", StepKind: "attempt_birth", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, Sequence: "1", ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "model", RouteDecisions: "[]", CostBasis: "unknown", QuoteSelectedAt: f.at, AttemptCreatedAt: f.at, PBCreatedXID: "7", InitialDeliveryState: "prepared", InitialCostStatus: "unknown", ManagementMode: "unmanaged"}
	if quote {
		f.quote = f.id()
		origin.CostVersionID = historyPtr(f.quote)
		origin.CostInputRate = historyPtr("1000000")
		origin.CostOutputRate = historyPtr("1000000")
		origin.CostBasis = "contract"
		f.rows.OriginalRows.CostVersions = []CostVersionRow{{ID: f.quote, ChannelID: f.channel, UpstreamModel: "model", InputRate: historyPtr("1000000"), OutputRate: historyPtr("1000000"), Basis: "contract", EffectiveAt: f.at, CreatedAt: f.at}}
	}
	f.rows.PrefixRows.AttemptOrigins = []AttemptOriginRow{origin}
	f.rows.PrefixRows.RequestOrigins = []RequestOriginRow{{RequestID: f.request, TenantID: f.tenant, InputUpperBound: "70", OutputLimit: "30", RequestDeadline: Time{Valid: true, Time: f.at.Time.Add(time.Hour)}}}
	f.rows.OriginalRows.UpstreamAttempts = []UpstreamAttemptRow{{ID: f.attempt, TenantID: f.tenant, RequestID: f.request, Sequence: "1", ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "model", CostVersionID: origin.CostVersionID, CostInputRate: origin.CostInputRate, CostOutputRate: origin.CostOutputRate, CostBasis: origin.CostBasis, CreatedAt: f.at, RouteDecisions: "[]", PBCreatedXID: historyPtr("7"), PPOriginRootID: historyPtr(f.root), PPOriginRootStep: historyPtr("1"), PPOriginXID: historyPtr("7"), PPQuoteSelectedAt: f.at}}
	f.sync()
	return f
}
func (f *historyFixture) sync() {
	a := &f.rows.OriginalRows.UpstreamAttempts[0]
	a.CostStatus, a.CostAmount, a.DeliveryState, a.PPLastCostMutationID, a.PPLastDeliverySourceID = f.status, f.amount, f.delivery, f.lastMutation, f.lastDelivery
	var last *string
	if f.lastNode != "" {
		last = historyPtr(f.lastNode)
	}
	f.rows.PrefixRows.AttemptHeads = []AttemptHeadRow{{AttemptID: f.attempt, Ordinal: strconv.Itoa(f.node), LastNodeID: last, UpdatedXID: "7"}}
	if f.managed && len(f.rows.OriginalRows.ProcurementReservations) > 0 {
		p := &f.rows.OriginalRows.ProcurementReservations[0]
		p.State, p.Revision, p.HeldAmount, p.SpentAmount, p.Anomaly = f.pstate, strconv.Itoa(f.pr), strconv.Itoa(f.ph), strconv.Itoa(f.ps), f.panomaly
	}
}
func (f *historyFixture) start() { f.root, f.step = f.id(), 0 }
func (f *historyFixture) next(kind, id string) string {
	if id == "" {
		id = f.id()
	}
	f.step++
	f.node++
	var prev *string
	if f.lastNode != "" {
		prev = historyPtr(f.lastNode)
	}
	f.lastNode = f.id()
	f.rows.PrefixRows.AttemptNodes = append(f.rows.PrefixRows.AttemptNodes, AttemptNodeRow{ID: f.lastNode, AttemptID: f.attempt, Ordinal: strconv.Itoa(f.node), PreviousOrdinal: strconv.Itoa(f.node - 1), PreviousNodeID: prev, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: kind, SourceID: id, CreatedXID: "7"})
	return id
}
func (f *historyFixture) mutate(id, kind, cause, status string, amount *string) {
	f.next("cost_mutation", id)
	f.rows.PrefixRows.CostMutations = append(f.rows.PrefixRows.CostMutations, CostMutationRow{ID: id, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "cost_mutation", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, CauseKind: kind, CauseSourceID: cause, BeforeStatus: f.status, BeforeAmount: f.amount, AfterStatus: status, AfterAmount: amount})
	f.status, f.amount, f.lastMutation = status, amount, historyPtr(id)
}
func (f *historyFixture) emit(id, kind, cause, operation, outKind, amount, basis string) {
	link := f.next("cost_outbox", "")
	step := strconv.Itoa(f.step)
	f.rows.PrefixRows.CostOutboxLinks = append(f.rows.PrefixRows.CostOutboxLinks, CostOutboxLinkRow{ID: link, RootID: f.root, StepNo: step, StepKind: "cost_outbox", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, CauseKind: kind, CauseSourceID: cause, OutboxID: id})
	f.rows.OriginalRows.ProjectionOutbox = append(f.rows.OriginalRows.ProjectionOutbox, ProjectionOutboxRow{EventID: id, TenantID: f.tenant, OperationID: operation, Kind: outKind, SchemaVersion: "1", SanitizedPayload: historyJSON(map[string]string{"attempt_id": f.attempt, "amount": amount, "basis": basis}), CreatedAt: f.at, PPRootID: historyPtr(f.root), PPRootStep: historyPtr(step), PPSourceKind: historyPtr("cost_outbox"), PPSourceID: historyPtr(link), PPSourceRole: historyPtr("cost"), PPCreatedXID: historyPtr("7")})
}
func (f *historyFixture) dispatch() {
	f.start()
	id := f.next("attempt_delivery", "")
	step := strconv.Itoa(f.step)
	f.rows.PrefixRows.AttemptDeliverySources = append(f.rows.PrefixRows.AttemptDeliverySources, AttemptDeliverySourceRow{ID: id, RootID: f.root, StepNo: step, StepKind: "attempt_delivery", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, Mode: "dispatch_permit", BeforeDeliveryState: f.delivery, AfterDeliveryState: "sending", PBDispatchCheckedAt: f.at, DispatchXID: historyPtr("7")})
	f.delivery, f.lastDelivery, f.permit = "sending", historyPtr(id), historyPtr(id)
	a := &f.rows.OriginalRows.UpstreamAttempts[0]
	a.PPDispatchSourceID, a.PPDispatchRootID, a.PPDispatchRootStep, a.PPDispatchXID, a.PBDispatchCheckedAt = historyPtr(id), historyPtr(f.root), historyPtr(step), historyPtr("7"), f.at
	f.sync()
}
func (f *historyFixture) normal(reason, usage, decision, amount, basis string, input, output int, write bool) {
	f.start()
	incoming := f.next("incoming", "")
	f.rows.PrefixRows.IncomingSources = append(f.rows.PrefixRows.IncomingSources, IncomingSourceRow{ID: incoming, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "incoming", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: historyPtr(f.attempt), UsageStatus: usage, InputTokens: strconv.Itoa(input), OutputTokens: strconv.Itoa(output), TotalTokens: strconv.Itoa(input + output), CachedTokens: "0", ReasoningTokens: "0", TransportEndReason: reason, ProtocolOutcome: "completed", FinishReason: "stop"})
	meter := f.next("meter_choice", "")
	meterID := f.id()
	f.rows.PrefixRows.MeterChoices = append(f.rows.PrefixRows.MeterChoices, MeterChoiceRow{ID: meter, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "meter_choice", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: historyPtr(f.attempt), IncomingSourceID: incoming, MeterEventID: meterID, CollisionKind: "none"})
	requestDecision := f.next("request_decision", "")
	final := f.id()
	f.rows.PrefixRows.RequestDecisions = append(f.rows.PrefixRows.RequestDecisions, RequestDecisionRow{ID: requestDecision, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "request_decision", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, IncomingSourceID: incoming, MeterChoiceID: meter, Branch: "normal", FinalizationID: historyPtr(final)})
	f.rows.OriginalRows.FinalizationRecords = append(f.rows.OriginalRows.FinalizationRecords, FinalizationRecordRow{ID: final, TenantID: f.tenant, RequestID: f.request, EventID: meterID, ProtocolOutcome: "completed", TransportEndReason: reason, FinishReason: "stop"})
	delivery := f.next("attempt_delivery", "")
	f.rows.PrefixRows.AttemptDeliverySources = append(f.rows.PrefixRows.AttemptDeliverySources, AttemptDeliverySourceRow{ID: delivery, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "attempt_delivery", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, Mode: "finalization", CauseSourceID: historyPtr(requestDecision), BeforeDeliveryState: f.delivery, AfterDeliveryState: "completed"})
	f.delivery, f.lastDelivery = "completed", historyPtr(delivery)
	f.lastIncoming, f.lastMeter = incoming, meter
	normal := f.next("normal_cost_decision", "")
	d := NormalCostDecisionRow{ID: normal, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "normal_cost_decision", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, BeforeCostStatus: f.status, BeforeCostAmount: f.amount, ManagementMode: "unmanaged", FinalizationID: final, IncomingSourceID: incoming, MeterChoiceID: meter, Decision: decision}
	if f.managed {
		d.ManagementMode = "managed"
		d.BeforeProcurementRevision = historyPtr(strconv.Itoa(f.pr))
		d.BeforeProcurementState = historyPtr(f.pstate)
		d.BeforeProcurementHeld = historyPtr(strconv.Itoa(f.ph))
		d.BeforeProcurementSpent = historyPtr(strconv.Itoa(f.ps))
		anomalyBefore := f.panomaly
		d.BeforeProcurementAnomaly = &anomalyBefore
		d.MemberCountBefore = historyPtr(strconv.Itoa(f.memberCount))
		d.MemberTailBefore = f.memberTail
		d.AccountID = historyPtr(f.account)
		d.AccountOrdinalCut = historyPtr(strconv.Itoa(f.accountOrdinal))
		d.AccountTailCut = historyPtr(f.plast)
	}
	if write {
		mutation, out := f.id(), f.id()
		d.SelectedAmount, d.SelectedBasis, d.CostMutationID, d.CostRecordedOutboxID = historyPtr(amount), historyPtr(basis), historyPtr(mutation), historyPtr(out)
		f.mutate(mutation, "normal_cost_decision", normal, basis, historyPtr(amount))
		f.emit(out, "normal_cost_decision", normal, f.attempt, "cost_recorded", amount, basis)
	}
	f.rows.PrefixRows.NormalCostDecisions = append(f.rows.PrefixRows.NormalCostDecisions, d)
	f.sync()
}
func (f *historyFixture) correct(amount, basis, actor string) {
	f.start()
	id := f.next("unmanaged_correction", "")
	step := strconv.Itoa(f.step)
	operation, correction, mutation, out, audit := f.id(), f.id(), f.id(), f.id(), f.id()
	effective := actor
	if effective == "" {
		effective = "system"
	}
	u := UnmanagedCorrectionSourceRow{ID: id, RootID: f.root, StepNo: step, StepKind: "unmanaged_correction", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, OperationID: operation, ProviderCorrectionID: correction, InputActorID: historyPtr(actor), EffectiveAuditActor: effective, Amount: amount, Basis: basis, EvidenceRef: "invoice:test", BeforeCostStatus: f.status, BeforeCostAmount: f.amount, AfterCostStatus: basis, AfterCostAmount: amount, CostMutationID: mutation, AuditID: audit, OutboxID: out}
	f.rows.PrefixRows.UnmanagedCorrectionSources = append(f.rows.PrefixRows.UnmanagedCorrectionSources, u)
	f.rows.OriginalRows.ProviderCostCorrections = append(f.rows.OriginalRows.ProviderCostCorrections, ProviderCostCorrectionRow{ID: correction, TenantID: f.tenant, AttemptID: f.attempt, OperationID: operation, Amount: amount, Basis: basis, EvidenceRef: "invoice:test", CreatedAt: f.at, PPRootID: historyPtr(f.root), PPRootStep: historyPtr(step), PPSourceKind: historyPtr("unmanaged_correction"), PPSourceID: historyPtr(id), PPSourceRole: historyPtr("correction"), PPCreatedXID: historyPtr("7")})
	f.rows.OriginalRows.AuditEvents = append(f.rows.OriginalRows.AuditEvents, AuditEventRow{ID: audit, Actor: effective, TenantScope: historyPtr(f.tenant), Action: "cost.corrected", Target: correction, RequestID: f.request, SanitizedDiff: historyJSON(map[string]string{"amount": amount, "basis": basis})})
	f.mutate(mutation, "unmanaged_correction", id, basis, historyPtr(amount))
	f.emit(out, "unmanaged_correction", id, operation, "cost_corrected", amount, basis)
	f.sync()
}
func historyRun(t *testing.T, rows Rows) attemptCostHistoryResult {
	t.Helper()
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	r, err := checkAttemptCostHistory(w, rows)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func historyClean(t *testing.T, rows Rows) {
	t.Helper()
	r := historyRun(t, rows)
	if !r.Complete || len(r.Unimplemented) != 0 || r.Stats != (AttemptCost{}) || len(r.Candidates.CurrentCostOrder) != 1 {
		t.Fatalf("history not clean: %#v", r)
	}
}
func historyClone(rows Rows) Rows {
	var out Rows
	_ = json.Unmarshal([]byte(historyJSON(rows)), &out)
	return out
}
func TestAttemptCostHistoryFrozenQuoteNormalAndCorrections(t *testing.T) {
	f := newHistoryFixture(true)
	f.dispatch()
	f.normal("done", "confirmed_final", "write_final_usage", "60", "contract", 40, 20, true)
	f.correct("60", "invoice", "")
	f.correct("60", "invoice", "actor-b")
	historyClean(t, f.rows)
	for _, mutate := range []func(*Rows){func(r *Rows) { r.PrefixRows.NormalCostDecisions[0].BeforeCostStatus = "invoice" }, func(r *Rows) { r.PrefixRows.AttemptDeliverySources[0].DispatchXID = historyPtr("8") }, func(r *Rows) { r.PrefixRows.CostMutations[1].BeforeAmount = historyPtr("59") }, func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].CostStatus = "contract" }, func(r *Rows) { r.OriginalRows.CostVersions[0].OutputRate = historyPtr("999999") }, func(r *Rows) { r.OriginalRows.ProjectionOutbox[1].PPSourceKind = historyPtr("unmanaged_correction") }, func(r *Rows) { r.PrefixRows.UnmanagedCorrectionSources[0].EffectiveAuditActor = "caller" }} {
		copy := historyClone(f.rows)
		mutate(&copy)
		r := historyRun(t, copy)
		if r.Stats == (AttemptCost{}) || len(r.Candidates.CurrentCostOrder) != 0 {
			t.Fatal("invalid cost history nominated")
		}
	}
}
func TestAttemptCostHistoryUnknownQuoteAndNormalSkips(t *testing.T) {
	f := newHistoryFixture(false)
	f.normal("eof", "missing", "skip_no_final_usage_or_rate", "", "", 0, 0, false)
	f.normal("not_sent", "missing", "write_no_consumption", "0", "contract", 0, 0, true)
	f.normal("done", "confirmed_final", "skip_already_known", "", "", 1, 0, false)
	historyClean(t, f.rows)
}
func TestAttemptCostHistoryRouteLimitsAndExactJSON(t *testing.T) {
	f := newHistoryFixture(true)
	raw := `[{"channel_id":"` + f.channel + `","version_id":"` + f.version + `","priority":-9223372036854775808,"weight":18446744073709551615,"reason":"selected"}]`
	f.rows.PrefixRows.AttemptOrigins[0].RouteDecisions = raw
	f.rows.OriginalRows.UpstreamAttempts[0].RouteDecisions = raw
	historyClean(t, f.rows)
	for _, bad := range []string{strings.Replace(raw, `"weight":18446744073709551615`, `"weight":18446744073709551616`, 1), strings.Replace(raw, `"reason":"selected"`, `"reason":"selected","reas\u006fn":"selected"`, 1), strings.Replace(raw, `"reason":"selected"`, `"reason":"\ud800"`, 1)} {
		copy := historyClone(f.rows)
		copy.PrefixRows.AttemptOrigins[0].RouteDecisions = bad
		if r := historyRun(t, copy); r.Stats.AttemptBirthRows != 1 {
			t.Fatal("bad route ABI was accepted")
		}
	}
}
func TestAttemptCostHistoryCancellationAndNaturalCounters(t *testing.T) {
	rows := Rows{OriginalRows: OriginalRows{UpstreamAttempts: make([]UpstreamAttemptRow, 3000)}}
	for i := range rows.OriginalRows.UpstreamAttempts {
		rows.OriginalRows.UpstreamAttempts[i].ID = fmt.Sprint(i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w, _, err := newReplayWork(ctx, rows)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	result, err := checkAttemptCostHistory(w, rows)
	if err != ErrCanceled || !reflect.DeepEqual(result, attemptCostHistoryResult{}) {
		t.Fatal("cancellation returned partial state")
	}
	f := newHistoryFixture(true)
	f.start()
	first, second := f.next("cost_mutation", ""), f.next("cost_mutation", "")
	_ = first
	_ = second
	f.sync()
	r := historyRun(t, f.rows)
	if r.Stats.MutationRows != 2 {
		t.Fatalf("two missing mutation objects counted %d", r.Stats.MutationRows)
	}
	f = newHistoryFixture(true)
	f.normal("done", "confirmed_final", "write_final_usage", "60", "contract", 40, 20, true)
	copy := historyClone(f.rows)
	copy.PrefixRows.CostMutations[0].BeforeStatus = "invoice"
	copy.PrefixRows.CostMutations[0].AfterStatus = "invoice"
	copy.PrefixRows.CostMutations[0].AfterAmount = historyPtr("59")
	if r := historyRun(t, copy); r.Stats.MutationRows != 1 {
		t.Fatalf("one multi-defect mutation counted %d", r.Stats.MutationRows)
	}
}

func newManagedHistoryFixture() *historyFixture {
	f := newHistoryFixture(true)
	f.managed = true
	f.account, f.binding = f.id(), f.id()
	f.pstate, f.ph, f.ps, f.pr = "prepared", 100, 0, 1
	f.accountOrdinal = 2
	o := &f.rows.PrefixRows.AttemptOrigins[0]
	o.ManagementMode = "managed"
	o.AccountID, o.BindingID = historyPtr(f.account), historyPtr(f.binding)
	transition, event, journal := f.id(), f.id(), f.id()
	o.ProcurementTransitionID, o.ReservedEventID, o.AccountJournalID, o.AdmittedAt = historyPtr(transition), historyPtr(event), historyPtr(journal), f.at
	f.root, f.step = o.RootID, 1
	f.next("procurement_transition", transition)
	f.plast = journal
	f.rows.PrefixRows.AccountJournal = []AccountJournalRow{{ID: f.id(), AccountID: f.account, Ordinal: "1", Kind: "create"}, {ID: journal, AccountID: f.account, AttemptID: historyPtr(f.attempt), Ordinal: "2", RootID: f.root, StepNo: "2", StepKind: "procurement_transition", TransitionID: historyPtr(transition), ProcurementEventID: historyPtr(event)}}
	f.rows.PrefixRows.ProcurementTransitions = []ProcurementTransitionRow{{ID: transition, RootID: f.root, StepNo: "2", StepKind: "procurement_transition", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, AccountID: f.account, BindingID: f.binding, CauseKind: "attempt_birth", CauseSourceID: f.attempt, EventID: event, JournalID: journal, EventType: "reserved", AfterRevision: "1", AfterState: "prepared", AfterHeld: "100", AfterSpent: "0"}}
	f.rows.OriginalRows.ProcurementReservations = []ProcurementReservationRow{{AttemptID: f.attempt, AccountID: f.account, BindingID: f.binding, InputBound: "70", OutputBound: "30", UpperAmount: "100", HeldAmount: "100", SpentAmount: "0", State: "prepared", Revision: "1", PPCreatedXID: historyPtr("7"), PBCreatedXID: historyPtr("7"), PPOriginJournalID: historyPtr(journal), PBAdmittedAt: f.at}}
	f.sync()
	return f
}
func historyEvidenceHash(usage string, input, output int, reason string) string {
	// Literal bytes independently spell the original Go Usage+Reason ABI.
	raw := fmt.Sprintf(`{"Usage":{"status":%s,"input_tokens":%d,"output_tokens":%d,"total_tokens":%d,"cached_tokens":0,"reasoning_tokens":0},"Reason":%s}`, historyJSON(usage), input, output, input+output, historyJSON(reason))
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func (f *historyFixture) managedEvidence(reason, usage string, input, output int, decision, target string, held, spent int, anomaly, forced bool) string {
	source := f.next("evidence_choice", "")
	step := strconv.Itoa(f.step)
	digest := historyEvidenceHash(usage, input, output, reason)
	member, meterEvent := f.id(), f.rows.PrefixRows.MeterChoices[len(f.rows.PrefixRows.MeterChoices)-1].MeterEventID
	effective := decision == "dispute"
	cause := "none"
	if forced {
		cause = "incoming_meter_conflict"
	} else if decision == "dispute" && f.pstate == "settled" {
		cause = "settled_positive_no_consumption"
	}
	choice := EvidenceChoiceRow{ID: source, RootID: f.root, StepNo: step, StepKind: "evidence_choice", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, IncomingSourceID: f.lastIncoming, MeterChoiceID: f.lastMeter, EvidenceDigest: digest, ProcurementBeforeRevision: strconv.Itoa(f.pr), ProcurementBeforeState: f.pstate, ProcurementBeforeHeld: strconv.Itoa(f.ph), ProcurementBeforeSpent: strconv.Itoa(f.ps), ProcurementBeforeAnomaly: f.panomaly, AccountID: f.account, AccountOrdinalCut: strconv.Itoa(f.accountOrdinal), AccountTailCut: f.plast, Disposition: "new_member", IdentityAttemptID: f.attempt, IdentityDigest: digest, IdentityEventID: meterEvent, MemberCountBefore: strconv.Itoa(f.memberCount), MemberTailBefore: f.memberTail, ForcedConflict: forced, EffectiveConflict: &effective, ConflictCause: historyPtr(cause), Decision: decision}
	f.memberCount++
	f.rows.PrefixRows.EvidenceMembers = append(f.rows.PrefixRows.EvidenceMembers, EvidenceMemberRow{ID: member, AttemptID: f.attempt, MemberOrdinal: strconv.Itoa(f.memberCount), PreviousMemberOrdinal: strconv.Itoa(f.memberCount - 1), PreviousMemberID: f.memberTail, IdentityDigest: digest, IdentityEventID: meterEvent, EvidenceChoiceID: source, RootID: f.root, StepNo: step, StepKind: "evidence_choice", SourceID: source, CreatedXID: "7"})
	f.memberTail = historyPtr(member)
	choice.MemberCountAfter, choice.MemberTailAfter = strconv.Itoa(f.memberCount), f.memberTail
	if strings.HasPrefix(decision, "skip_") {
		reason := strings.TrimPrefix(decision, "skip_")
		choice.SkipReason = historyPtr(reason)
		f.rows.PrefixRows.EvidenceChoices = append(f.rows.PrefixRows.EvidenceChoices, choice)
		f.sync()
		return ""
	}
	transition, event, journal := f.id(), f.id(), f.id()
	choice.ProcurementTransitionID, choice.ProcurementEventID, choice.AccountJournalID = historyPtr(transition), historyPtr(event), historyPtr(journal)
	f.rows.PrefixRows.EvidenceChoices = append(f.rows.PrefixRows.EvidenceChoices, choice)
	f.next("procurement_transition", transition)
	eventType := "cost_confirmed"
	if decision == "dispute" {
		eventType = "evidence_disputed"
	} else if decision == "mark_unknown" {
		eventType = "consumption_unknown"
	} else if decision == "release_no_consumption" {
		eventType = "released_no_consumption"
	}
	oldRevision, oldState, oldHeld, oldSpent := f.pr, f.pstate, f.ph, f.ps
	oldAnomaly := f.panomaly
	f.panomaly = anomaly
	f.pr++
	f.pstate, f.ph, f.ps = target, held, spent
	f.accountOrdinal++
	f.plast = journal
	f.rows.PrefixRows.ProcurementTransitions = append(f.rows.PrefixRows.ProcurementTransitions, ProcurementTransitionRow{ID: transition, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "procurement_transition", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, AccountID: f.account, BindingID: f.binding, CauseKind: "evidence_choice", CauseSourceID: source, EventID: event, JournalID: journal, EventType: eventType, BeforeRevision: historyPtr(strconv.Itoa(oldRevision)), BeforeState: historyPtr(oldState), BeforeHeld: historyPtr(strconv.Itoa(oldHeld)), BeforeSpent: historyPtr(strconv.Itoa(oldSpent)), BeforeAnomaly: &oldAnomaly, AfterRevision: strconv.Itoa(f.pr), AfterState: target, AfterHeld: strconv.Itoa(held), AfterSpent: strconv.Itoa(spent), AfterAnomaly: anomaly})
	f.rows.PrefixRows.AccountJournal = append(f.rows.PrefixRows.AccountJournal, AccountJournalRow{ID: journal, AccountID: f.account, AttemptID: historyPtr(f.attempt), Ordinal: strconv.Itoa(f.accountOrdinal), RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "procurement_transition", TransitionID: historyPtr(transition), ProcurementEventID: historyPtr(event)})
	if decision == "dispute" {
		f.addReset("procurement_dispute", transition, historyPtr(oldState), "applied")
	}
	f.sync()
	return transition
}
func (f *historyFixture) addReset(kind, cause string, pstate *string, policy string) {
	id := f.next("cost_reset_decision", "")
	r := CostResetDecisionRow{ID: id, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "cost_reset_decision", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, CauseKind: kind, CauseSourceID: cause, ProcurementStateBefore: pstate, Policy: policy}
	if policy == "applied" {
		mutation := f.id()
		r.CostMutationID = historyPtr(mutation)
		f.mutate(mutation, kind+"_reset", id, "unknown", nil)
	}
	f.rows.PrefixRows.CostResetDecisions = append(f.rows.PrefixRows.CostResetDecisions, r)
	f.sync()
}
func (f *historyFixture) incomingConflict() {
	f.start()
	incoming := f.next("incoming", "")
	f.lastIncoming = incoming
	f.rows.PrefixRows.IncomingSources = append(f.rows.PrefixRows.IncomingSources, IncomingSourceRow{ID: incoming, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "incoming", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: historyPtr(f.attempt), UsageStatus: "missing", InputTokens: "0", OutputTokens: "0", TotalTokens: "0", CachedTokens: "0", ReasoningTokens: "0", TransportEndReason: "provider_error", FinishReason: "stop"})
	meter := f.next("meter_choice", "")
	f.lastMeter = meter
	f.rows.PrefixRows.MeterChoices = append(f.rows.PrefixRows.MeterChoices, MeterChoiceRow{ID: meter, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "meter_choice", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: historyPtr(f.attempt), IncomingSourceID: incoming, MeterEventID: f.id(), CollisionKind: "source_payload_mismatch"})
	decision := f.next("request_decision", "")
	conflict := f.id()
	f.rows.PrefixRows.RequestDecisions = append(f.rows.PrefixRows.RequestDecisions, RequestDecisionRow{ID: decision, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "request_decision", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, IncomingSourceID: incoming, MeterChoiceID: meter, Branch: "conflict", ConflictSourceID: historyPtr(conflict)})
	f.rows.PrefixRows.ConflictSources = append(f.rows.PrefixRows.ConflictSources, ConflictSourceRow{ConflictID: conflict, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "request_decision", SourceID: decision, CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: historyPtr(f.attempt), IncomingSourceID: incoming, MeterChoiceID: meter})
	if f.managed {
		f.managedEvidence("provider_error", "missing", 0, 0, "dispute", "disputed", 100, 0, true, true)
		f.addReset("incoming_conflict", conflict, historyPtr("disputed"), "applied")
	} else {
		f.addReset("incoming_conflict", conflict, nil, "applied")
	}
	f.sync()
}
func TestAttemptCostHistoryManagedNormalAndDoubleUnknownResets(t *testing.T) {
	f := newManagedHistoryFixture()
	f.normal("done", "confirmed_final", "write_final_usage", "60", "contract", 40, 20, true)
	f.managedEvidence("done", "confirmed_final", 40, 20, "confirm_cost", "settled", 0, 60, false, false)
	historyClean(t, f.rows)
	copy := historyClone(f.rows)
	copy.PrefixRows.NormalCostDecisions[0].BeforeProcurementState = historyPtr("settled")
	if got := historyRun(t, copy); got.Stats.NormalEligibilityRows != 1 || len(got.Candidates.CostRecordedEligibility) != 0 {
		t.Fatal("managed normal observed a future p target")
	}
	f = newManagedHistoryFixture()
	f.incomingConflict()
	historyClean(t, f.rows)
	if len(f.rows.PrefixRows.CostMutations) != 2 || f.rows.PrefixRows.CostMutations[0].BeforeStatus != "unknown" || f.rows.PrefixRows.CostMutations[1].BeforeStatus != "unknown" {
		t.Fatal("fixture collapsed the two real unknown-to-unknown resets")
	}
	copy = historyClone(f.rows)
	copy.PrefixRows.CostResetDecisions[1].Policy = "suppressed_adjudicated"
	if got := historyRun(t, copy); got.Stats.ResetRows != 1 || len(got.Candidates.CurrentCostOrder) != 0 {
		t.Fatal("incoming reset was suppressed using a pre-dispute p state")
	}
	copy = historyClone(f.rows)
	copy.PrefixRows.CostMutations = copy.PrefixRows.CostMutations[:1]
	if got := historyRun(t, copy); got.Stats.MutationRows != 1 {
		t.Fatal("missing second unknown reset mutation was accepted or double-counted")
	}
}
func TestAttemptCostHistoryUnmanagedRecordedResetNormalMustRollback(t *testing.T) {
	f := newHistoryFixture(true)
	f.normal("done", "confirmed_final", "write_final_usage", "60", "contract", 40, 20, true)
	f.incomingConflict()
	historyClean(t, f.rows)
	f.normal("done", "confirmed_final", "write_final_usage", "60", "contract", 40, 20, true)
	if got := historyRun(t, f.rows); got.Stats.NormalEligibilityRows != 1 || got.Stats.OutboxRows == 0 || len(got.Candidates.CurrentCostOrder) != 0 {
		t.Fatal("unmanaged recorded/reset/normal fabricated success instead of original 23505 rollback")
	}
}

func (f *historyFixture) approveCost(amount, basis string) {
	f.start()
	id := f.next("approval_consumed", "")
	step := strconv.Itoa(f.step)
	proposal, birth, operation, correction, mutation, out, audit, transition, event, journal := f.id(), f.id(), f.id(), f.id(), f.id(), f.id(), f.id(), f.id(), f.id(), f.id()
	approver := f.id()
	a := ApprovalConsumptionRow{ID: id, RootID: f.root, StepNo: step, StepKind: "approval_consumed", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, ProposalID: proposal, ProposalBirthID: birth, ApprovalOperationID: operation, Approver: approver, BeforeCostStatus: f.status, BeforeCostAmount: f.amount, ConsumedRevision: strconv.Itoa(f.pr), ConsumedState: f.pstate, ConsumedHeld: strconv.Itoa(f.ph), ConsumedSpent: strconv.Itoa(f.ps), ConsumedAnomaly: f.panomaly, ConsumedMemberCount: strconv.Itoa(f.memberCount), ConsumedMemberTail: f.memberTail, AccountID: f.account, AccountOrdinalCut: strconv.Itoa(f.accountOrdinal), AccountTailCut: f.plast, ProcurementTransitionID: transition, ProcurementEventID: event, ProviderCorrectionID: correction, CostMutationID: mutation, OutboxID: out, AuditID: audit}
	f.rows.PrefixRows.ApprovalConsumptions = append(f.rows.PrefixRows.ApprovalConsumptions, a)
	f.rows.OriginalRows.ProcurementCostProposals = append(f.rows.OriginalRows.ProcurementCostProposals, ProcurementCostProposalRow{ID: proposal, TenantID: f.tenant, AttemptID: f.attempt, Amount: amount, Basis: basis, EvidenceRef: "invoice:test", State: "approved", Approver: historyPtr(approver), ApprovalOperationID: historyPtr(operation)})
	f.rows.OriginalRows.ProviderCostCorrections = append(f.rows.OriginalRows.ProviderCostCorrections, ProviderCostCorrectionRow{ID: correction, TenantID: f.tenant, AttemptID: f.attempt, OperationID: "procurement:" + operation, Amount: amount, Basis: basis, EvidenceRef: "invoice:test", CreatedAt: f.at, PPRootID: historyPtr(f.root), PPRootStep: historyPtr(step), PPSourceKind: historyPtr("approval_consumed"), PPSourceID: historyPtr(id), PPSourceRole: historyPtr("correction"), PPCreatedXID: historyPtr("7")})
	f.rows.OriginalRows.AuditEvents = append(f.rows.OriginalRows.AuditEvents, AuditEventRow{ID: audit, Actor: approver, TenantScope: historyPtr(f.tenant), Action: "procurement.cost_approved", Target: proposal, RequestID: f.request, SanitizedDiff: historyJSON(map[string]string{"attempt_id": f.attempt, "amount": amount, "evidence_ref": "invoice:test"})})
	f.next("procurement_transition", transition)
	afterAmount, _ := strconv.Atoi(amount)
	oldRev, oldState, oldHeld, oldSpent, oldAnomaly := f.pr, f.pstate, f.ph, f.ps, f.panomaly
	if f.pstate != "settled" || f.ph != 0 || f.ps != afterAmount || f.panomaly {
		f.pr++
	}
	f.pstate, f.ph, f.ps, f.panomaly = "settled", 0, afterAmount, false
	f.accountOrdinal++
	f.plast = journal
	f.rows.PrefixRows.ProcurementTransitions = append(f.rows.PrefixRows.ProcurementTransitions, ProcurementTransitionRow{ID: transition, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "procurement_transition", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, AccountID: f.account, BindingID: f.binding, CauseKind: "approval_consumed", CauseSourceID: id, EventID: event, JournalID: journal, EventType: "cost_corrected", BeforeRevision: historyPtr(strconv.Itoa(oldRev)), BeforeState: historyPtr(oldState), BeforeHeld: historyPtr(strconv.Itoa(oldHeld)), BeforeSpent: historyPtr(strconv.Itoa(oldSpent)), BeforeAnomaly: &oldAnomaly, AfterRevision: strconv.Itoa(f.pr), AfterState: "settled", AfterHeld: "0", AfterSpent: amount})
	f.rows.PrefixRows.AccountJournal = append(f.rows.PrefixRows.AccountJournal, AccountJournalRow{ID: journal, AccountID: f.account, AttemptID: historyPtr(f.attempt), Ordinal: strconv.Itoa(f.accountOrdinal), RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "procurement_transition", TransitionID: historyPtr(transition), ProcurementEventID: historyPtr(event)})
	f.mutate(mutation, "approval_consumed", id, basis, historyPtr(amount))
	f.emit(out, "approval_consumed", id, "procurement:"+operation, "cost_corrected", amount, basis)
	f.sync()
}
func (f *historyFixture) recoverManaged() {
	f.start()
	id := f.next("recovery", "")
	step := strconv.Itoa(f.step)
	delivery, transition, event, journal := f.id(), f.id(), f.id(), f.id()
	decision, pAfter, dAfter, held := "recovered_unknown", "unknown", "unknown", f.ph
	if f.permit == nil && f.pstate == "prepared" && f.delivery == "prepared" {
		decision, pAfter, dAfter, held = "expired_without_permit", "released", "failed", 0
	}
	f.rows.PrefixRows.RecoverySources = append(f.rows.PrefixRows.RecoverySources, RecoverySourceRow{ID: id, RootID: f.root, StepNo: step, StepKind: "recovery", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, CheckedAt: Time{Valid: true, Time: f.at.Time.Add(2 * time.Hour)}, RequestDeadline: f.rows.PrefixRows.RequestOrigins[0].RequestDeadline, ProcurementBeforeRevision: strconv.Itoa(f.pr), ProcurementBeforeState: f.pstate, ProcurementBeforeHeld: strconv.Itoa(f.ph), ProcurementBeforeSpent: strconv.Itoa(f.ps), ProcurementBeforeAnomaly: f.panomaly, DeliveryBeforeState: f.delivery, PermitSourceID: f.permit, Decision: decision, ProcurementTransitionID: transition, DeliveryMutationID: delivery, ProcurementEventID: event, AccountJournalID: journal})
	f.next("attempt_delivery", delivery)
	f.rows.PrefixRows.AttemptDeliverySources = append(f.rows.PrefixRows.AttemptDeliverySources, AttemptDeliverySourceRow{ID: delivery, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "attempt_delivery", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, Mode: "recovery", CauseSourceID: historyPtr(id), BeforeDeliveryState: f.delivery, AfterDeliveryState: dAfter})
	f.delivery, f.lastDelivery = dAfter, historyPtr(delivery)
	f.next("procurement_transition", transition)
	oldRev, oldState, oldH, oldS, oldA := f.pr, f.pstate, f.ph, f.ps, f.panomaly
	f.pr++
	f.pstate, f.ph, f.panomaly = pAfter, held, false
	f.accountOrdinal++
	f.plast = journal
	f.rows.PrefixRows.ProcurementTransitions = append(f.rows.PrefixRows.ProcurementTransitions, ProcurementTransitionRow{ID: transition, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "procurement_transition", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, AccountID: f.account, BindingID: f.binding, CauseKind: "recovery", CauseSourceID: id, EventID: event, JournalID: journal, EventType: decision, BeforeRevision: historyPtr(strconv.Itoa(oldRev)), BeforeState: historyPtr(oldState), BeforeHeld: historyPtr(strconv.Itoa(oldH)), BeforeSpent: historyPtr(strconv.Itoa(oldS)), BeforeAnomaly: &oldA, AfterRevision: strconv.Itoa(f.pr), AfterState: pAfter, AfterHeld: strconv.Itoa(held), AfterSpent: strconv.Itoa(f.ps)})
	f.rows.PrefixRows.AccountJournal = append(f.rows.PrefixRows.AccountJournal, AccountJournalRow{ID: journal, AccountID: f.account, AttemptID: historyPtr(f.attempt), Ordinal: strconv.Itoa(f.accountOrdinal), RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "procurement_transition", TransitionID: historyPtr(transition), ProcurementEventID: historyPtr(event)})
	f.sync()
}
func TestAttemptCostHistoryManagedApprovalNormalPriorityAndRecovery(t *testing.T) {
	f := newManagedHistoryFixture()
	f.normal("done", "confirmed_final", "write_final_usage", "60", "contract", 40, 20, true)
	f.managedEvidence("done", "confirmed_final", 40, 20, "confirm_cost", "settled", 0, 60, false, false)
	f.approveCost("60", "invoice")
	f.approveCost("60", "invoice")
	historyClean(t, f.rows)
	copy := historyClone(f.rows)
	copy.PrefixRows.ApprovalConsumptions[1].BeforeCostStatus = "contract"
	if got := historyRun(t, copy); got.Stats.CorrectionRows != 1 || len(got.Candidates.CurrentCostOrder) != 0 {
		t.Fatal("same-amount/basis-only approval lost true cost before")
	}
	f = newManagedHistoryFixture()
	f.incomingConflict()
	f.normal("done", "confirmed_final", "skip_disputed", "", "", 1, 0, false)
	f.managedEvidence("done", "confirmed_final", 1, 0, "skip_terminal_normal", "disputed", 100, 0, true, false)
	historyClean(t, f.rows)
	for _, permit := range []bool{false, true} {
		f = newManagedHistoryFixture()
		if permit {
			f.dispatch()
			last := &f.rows.PrefixRows.AttemptDeliverySources[0]
			journal := f.id()
			last.DispatchAccountID, last.DispatchJournalID = historyPtr(f.account), historyPtr(journal)
			f.rows.OriginalRows.UpstreamAttempts[0].PPDispatchAccountID, f.rows.OriginalRows.UpstreamAttempts[0].PPDispatchJournalID = last.DispatchAccountID, last.DispatchJournalID
			f.accountOrdinal++
			f.plast = journal
			f.pr++
			f.pstate = "sending"
			f.rows.PrefixRows.AccountJournal = append(f.rows.PrefixRows.AccountJournal, AccountJournalRow{ID: journal, AccountID: f.account, AttemptID: historyPtr(f.attempt), Ordinal: strconv.Itoa(f.accountOrdinal), Kind: "dispatch", DeliverySourceID: historyPtr(last.ID)})
		}
		f.recoverManaged()
		historyClean(t, f.rows)
		copy := historyClone(f.rows)
		copy.PrefixRows.RecoverySources[0].CheckedAt = copy.PrefixRows.RecoverySources[0].RequestDeadline
		if got := historyRun(t, copy); got.Stats.DeliveryRows == 0 || len(got.Candidates.CurrentCostOrder) != 0 {
			t.Fatal("exact-deadline recovery accepted")
		}
	}
}

func TestAttemptCostHistoryReverseObligationsAndPhysicalKeys(t *testing.T) {
	f := newHistoryFixture(true)
	f.normal("done", "confirmed_final", "write_final_usage", "60", "contract", 40, 20, true)
	for _, kind := range []string{"cost_mutation", "cost_outbox", "normal_cost_decision", "attempt_delivery"} {
		copy := historyClone(f.rows)
		for i, node := range copy.PrefixRows.AttemptNodes {
			if node.StepKind == kind {
				copy.PrefixRows.AttemptNodes = append(copy.PrefixRows.AttemptNodes[:i], copy.PrefixRows.AttemptNodes[i+1:]...)
				break
			}
		}
		if kind == "attempt_delivery" {
			// Keep the distinct current-attempt projection consistent, so this
			// vector isolates the one missing source node rather than two objects.
			copy.OriginalRows.UpstreamAttempts[0].DeliveryState = "prepared"
			copy.OriginalRows.UpstreamAttempts[0].PPLastDeliverySourceID = nil
		}
		got := historyRun(t, copy)
		var count uint64
		switch kind {
		case "cost_mutation":
			count = got.Stats.MutationRows
		case "cost_outbox":
			count = got.Stats.OutboxRows
		case "normal_cost_decision":
			count = got.Stats.NormalEligibilityRows
		case "attempt_delivery":
			count = got.Stats.DeliveryRows
		}
		if count != 1 {
			t.Fatalf("%s without node counted %d times, want its one physical object", kind, count)
		}
	}
	copy := historyClone(f.rows)
	copy.PrefixRows.NormalCostDecisions = nil
	filtered := copy.PrefixRows.AttemptNodes[:0]
	for _, node := range copy.PrefixRows.AttemptNodes {
		if node.StepKind != "normal_cost_decision" {
			filtered = append(filtered, node)
		}
	}
	copy.PrefixRows.AttemptNodes = filtered
	if got := historyRun(t, copy); got.Stats.NormalEligibilityRows != 1 || len(got.Candidates.CostRecordedEligibility) != 0 {
		t.Fatal("normal request decision with no cost source escaped reverse obligation")
	}
	copy = historyClone(f.rows)
	copy.PrefixRows.AttemptDeliverySources = nil
	filtered = copy.PrefixRows.AttemptNodes[:0]
	for _, node := range copy.PrefixRows.AttemptNodes {
		if node.StepKind != "attempt_delivery" {
			filtered = append(filtered, node)
		}
	}
	copy.PrefixRows.AttemptNodes = filtered
	if got := historyRun(t, copy); got.Stats.DeliveryRows == 0 || len(got.Candidates.CurrentCostOrder) != 0 {
		t.Fatal("normal request decision with no delivery escaped reverse obligation")
	}
	f = newManagedHistoryFixture()
	f.normal("done", "confirmed_final", "write_final_usage", "60", "contract", 40, 20, true)
	f.managedEvidence("done", "confirmed_final", 40, 20, "confirm_cost", "settled", 0, 60, false, false)
	for _, mutate := range []func(*Rows){
		func(r *Rows) { r.OriginalRows.ProcurementReservations[0].InputBound = "71" },
		func(r *Rows) { r.OriginalRows.ProcurementReservations[0].PPCreatedXID = historyPtr("8") },
		func(r *Rows) { r.OriginalRows.ProcurementReservations[0].State = "sending" },
	} {
		copy = historyClone(f.rows)
		mutate(&copy)
		got := historyRun(t, copy)
		if got.Stats.AttemptBirthRows+got.Stats.CurrentCostRows == 0 || len(got.Candidates.CurrentCostOrder) != 0 {
			t.Fatal("managed frozen/current p mismatch accepted")
		}
	}
}

func TestAttemptCostHistoryExistingIdentitySuppressesOuterReset(t *testing.T) {
	f := newManagedHistoryFixture()
	f.normal("done", "confirmed_final", "write_final_usage", "60", "contract", 40, 20, true)
	originalEvent := f.rows.PrefixRows.MeterChoices[0].MeterEventID
	f.managedEvidence("done", "confirmed_final", 40, 20, "confirm_cost", "settled", 0, 60, false, false)
	f.approveCost("60", "invoice")
	f.start()
	incoming := f.next("incoming", "")
	f.rows.PrefixRows.IncomingSources = append(f.rows.PrefixRows.IncomingSources, IncomingSourceRow{ID: incoming, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "incoming", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: historyPtr(f.attempt), UsageStatus: "confirmed_final", InputTokens: "40", OutputTokens: "20", TotalTokens: "60", CachedTokens: "0", ReasoningTokens: "0", TransportEndReason: "done"})
	meter := f.next("meter_choice", "")
	f.rows.PrefixRows.MeterChoices = append(f.rows.PrefixRows.MeterChoices, MeterChoiceRow{ID: meter, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "meter_choice", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: historyPtr(f.attempt), IncomingSourceID: incoming, MeterEventID: f.id(), CollisionKind: "source_payload_mismatch"})
	decision, conflict := f.next("request_decision", ""), f.id()
	f.rows.PrefixRows.RequestDecisions = append(f.rows.PrefixRows.RequestDecisions, RequestDecisionRow{ID: decision, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "request_decision", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, IncomingSourceID: incoming, MeterChoiceID: meter, Branch: "conflict", ConflictSourceID: historyPtr(conflict)})
	f.rows.PrefixRows.ConflictSources = append(f.rows.PrefixRows.ConflictSources, ConflictSourceRow{ConflictID: conflict, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "request_decision", SourceID: decision, CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: historyPtr(f.attempt), IncomingSourceID: incoming, MeterChoiceID: meter})
	evidence := f.next("evidence_choice", "")
	digest := historyEvidenceHash("confirmed_final", 40, 20, "done")
	f.rows.PrefixRows.EvidenceChoices = append(f.rows.PrefixRows.EvidenceChoices, EvidenceChoiceRow{ID: evidence, RootID: f.root, StepNo: strconv.Itoa(f.step), StepKind: "evidence_choice", CreatedXID: "7", TenantID: f.tenant, RequestID: f.request, AttemptID: f.attempt, IncomingSourceID: incoming, MeterChoiceID: meter, EvidenceDigest: digest, ProcurementBeforeRevision: strconv.Itoa(f.pr), ProcurementBeforeState: f.pstate, ProcurementBeforeHeld: strconv.Itoa(f.ph), ProcurementBeforeSpent: strconv.Itoa(f.ps), ProcurementBeforeAnomaly: f.panomaly, AccountID: f.account, AccountOrdinalCut: strconv.Itoa(f.accountOrdinal), AccountTailCut: f.plast, Disposition: "existing_member", IdentityAttemptID: f.attempt, IdentityDigest: digest, IdentityEventID: originalEvent, MemberCountBefore: strconv.Itoa(f.memberCount), MemberTailBefore: f.memberTail, MemberCountAfter: strconv.Itoa(f.memberCount), MemberTailAfter: f.memberTail, ForcedConflict: true, Decision: "skip_existing_identity", SkipReason: historyPtr("existing_identity")})
	f.addReset("incoming_conflict", conflict, historyPtr("settled"), "suppressed_adjudicated")
	f.sync()
	historyClean(t, f.rows)
	if f.status != "invoice" || len(f.rows.PrefixRows.CostMutations) != 2 {
		t.Fatal("suppressed reset erased invoice or fabricated mutation")
	}
	copy := historyClone(f.rows)
	copy.PrefixRows.CostResetDecisions[0].Policy = "applied"
	if got := historyRun(t, copy); got.Stats.ResetRows != 1 || len(got.Candidates.CurrentCostOrder) != 0 {
		t.Fatal("adjudicated existing evidence allowed outer reset")
	}
}
