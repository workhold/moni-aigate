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

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/procurement"
)

// The fixture stores plain integer targets supplied by each test. It never calls
// replay or writer arithmetic/state helpers to manufacture expected values.
type acFixture struct {
	rows                                Rows
	seq                                 int
	account, actor                      string
	at                                  Time
	label, provider, state              string
	limit, revision, held, spent, block int
	last                                string
	ordinal                             int
	attempt, request, binding, cost     string
	node, count                         int
	memberTail                          *string
	ph, ps, pr                          int
	pstate                              string
	anomaly                             bool
	upper, input, output                int
	inputRate, outputRate               int
	permit                              *string
	pLast                               string
}

func acTestPtr[T any](v T) *T   { return &v }
func (f *acFixture) id() string { f.seq++; return fmt.Sprintf("00000000-0000-4000-8000-%012d", f.seq) }
func acTestJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
func (f *acFixture) snapshot() string {
	return acTestJSON(map[string]string{"id": f.account, "label": f.label, "provider": f.provider, "state": f.state, "limit_amount": strconv.Itoa(f.limit), "held_amount": strconv.Itoa(f.held), "spent_amount": strconv.Itoa(f.spent), "revision": strconv.Itoa(f.revision), "created_at": f.at.Time.UTC().Format(time.RFC3339Nano)})
}
func acTestDigest(kind, target string, payload any) string {
	raw := acTestJSON(struct {
		Kind, Target string
		Payload      any
	}{kind, target, payload})
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func newACFixture() *acFixture {
	f := &acFixture{at: Time{Valid: true, Time: time.Date(2026, 11, 3, 2, 3, 4, 120000000, time.UTC)}, label: "团队<&>\u2028A", provider: "provider", state: "active", limit: 1000, revision: 1}
	f.account = f.id()
	f.actor = f.id()
	root, source, operation, journal, audit := f.id(), f.id(), f.id(), f.id(), f.id()
	f.ordinal = 1
	f.last = journal
	s := AccountCommandSourceRow{ID: source, RootID: root, StepNo: "1", StepKind: "account_command", CreatedXID: "7", CreatedAt: f.at, OperationID: operation, ActorID: f.actor, ActorRole: "admin", ActorStatus: "active", Kind: "account_create", TargetID: f.account, InputLabel: f.label, InputProvider: f.provider, InputState: f.state, InputLimitAmount: "1000", CommandStatus: "applied", CommandCreatedAt: f.at, AuditID: audit, JournalID: &journal}
	s.InputDigest = acTestDigest("account_create", "", procurement.CreateInput{OperationID: operation, Label: f.label, Provider: f.provider, State: f.state, LimitAmount: "1000"})
	f.rows.PrefixRows.AccountCommandSources = []AccountCommandSourceRow{s}
	f.rows.PrefixRows.AccountOrigins = []AccountOriginRow{{AccountID: f.account, Version: "1", CreateOperationID: operation, CommandSourceID: source, RootID: root, StepNo: "1", StepKind: "account_command", JournalID: journal, CreatedXID: "7", CreatedAt: f.at}}
	f.rows.PrefixRows.AccountJournal = []AccountJournalRow{{ID: journal, AccountID: f.account, Ordinal: "1", PreviousOrdinal: "0", RootID: root, StepNo: "1", StepKind: "account_command", CreatedXID: "7", CreatedAt: f.at, Kind: "create", SourceKind: "command", SourceID: operation, AfterLabel: f.label, AfterProvider: f.provider, AfterState: f.state, AfterLimitAmount: "1000", AfterConfigurationRevision: "1", AfterHeldAmount: "0", AfterSpentAmount: "0", AfterBlockingCount: "0", CommandSourceID: &source, CommandOperationID: &operation}}
	f.rows.OriginalRows.ProcurementCommands = []ProcurementCommandRow{{OperationID: operation, Actor: f.actor, Kind: "account_create", TargetID: f.account, InputDigest: s.InputDigest, Status: "applied", BeforeState: "null", AfterState: f.snapshot(), CreatedAt: f.at, PPSourceID: &source, PPRootID: &root, PPRootStep: acTestPtr("1"), PPCreatedXID: acTestPtr("7"), PPAccountJournalID: &journal}}
	f.rows.OriginalRows.AuditEvents = []AuditEventRow{{ID: audit, Actor: f.actor, Action: "procurement.command.applied", Target: operation, SanitizedDiff: `{"kind":"account_create","status":"applied"}`, CreatedAt: f.at}}
	f.sync()
	return f
}
func (f *acFixture) sync() {
	origin := f.rows.PrefixRows.AccountOrigins[0]
	f.rows.OriginalRows.ProcurementAccounts = []ProcurementAccountRow{{ID: f.account, Label: f.label, Provider: f.provider, State: f.state, LimitAmount: strconv.Itoa(f.limit), HeldAmount: strconv.Itoa(f.held), SpentAmount: strconv.Itoa(f.spent), Revision: strconv.Itoa(f.revision), CreatedAt: f.at, PPVersion: acTestPtr("1"), PPCreatedXID: acTestPtr("7"), PPOriginJournalID: &origin.JournalID}}
	f.rows.PrefixRows.AccountHeads = []AccountHeadRow{{AccountID: f.account, Ordinal: strconv.Itoa(f.ordinal), JournalID: f.last, UpdatedXID: "7", Label: f.label, Provider: f.provider, State: f.state, LimitAmount: strconv.Itoa(f.limit), ConfigurationRevision: strconv.Itoa(f.revision), HeldAmount: strconv.Itoa(f.held), SpentAmount: strconv.Itoa(f.spent), BlockingCount: strconv.Itoa(f.block)}}
	if f.attempt != "" {
		for i := range f.rows.OriginalRows.ProcurementReservations {
			p := &f.rows.OriginalRows.ProcurementReservations[i]
			if p.AttemptID == f.attempt {
				p.HeldAmount = strconv.Itoa(f.ph)
				p.SpentAmount = strconv.Itoa(f.ps)
				p.State = f.pstate
				p.Anomaly = f.anomaly
				p.Revision = strconv.Itoa(f.pr)
				p.PPLastJournalID = acTestPtr(f.pLast)
			}
		}
		for i := range f.rows.PrefixRows.AttemptHeads {
			h := &f.rows.PrefixRows.AttemptHeads[i]
			if h.AttemptID == f.attempt {
				h.Ordinal = strconv.Itoa(f.node)
				h.MemberCount = acTestPtr(strconv.Itoa(f.count))
				h.MemberTailID = f.memberTail
			}
		}
	}
}

// Switch only the fixture's active attempt cursor. The account's global bucket
// totals and journal cursor are kept, matching the shared supplier budget.
func (f *acFixture) selectAttempt(index int) {
	origin := f.rows.PrefixRows.AttemptOrigins[index]
	f.attempt, f.request, f.binding, f.cost = origin.AttemptID, origin.RequestID, *origin.BindingID, *origin.CostVersionID
	f.inputRate, _ = strconv.Atoi(*origin.CostInputRate)
	f.outputRate, _ = strconv.Atoi(*origin.CostOutputRate)
	for _, p := range f.rows.OriginalRows.ProcurementReservations {
		if p.AttemptID == f.attempt {
			f.input, _ = strconv.Atoi(p.InputBound)
			f.output, _ = strconv.Atoi(p.OutputBound)
			f.upper, _ = strconv.Atoi(p.UpperAmount)
			f.ph, _ = strconv.Atoi(p.HeldAmount)
			f.ps, _ = strconv.Atoi(p.SpentAmount)
			f.pr, _ = strconv.Atoi(p.Revision)
			f.pstate, f.anomaly, f.pLast = p.State, p.Anomaly, *p.PPLastJournalID
		}
	}
	for _, head := range f.rows.PrefixRows.AttemptHeads {
		if head.AttemptID == f.attempt {
			f.node, _ = strconv.Atoi(head.Ordinal)
			f.count, _ = strconv.Atoi(*head.MemberCount)
			f.memberTail = head.MemberTailID
		}
	}
	f.permit = nil
	for _, u := range f.rows.OriginalRows.UpstreamAttempts {
		if u.ID == f.attempt {
			f.permit = u.PPDispatchSourceID
		}
	}
}
func (f *acFixture) journal(root, source, step, kind, sourceKind, sourceID string) AccountJournalRow {
	return AccountJournalRow{ID: f.id(), AccountID: f.account, Ordinal: strconv.Itoa(f.ordinal + 1), PreviousOrdinal: strconv.Itoa(f.ordinal), PreviousID: acTestPtr(f.last), RootID: root, StepNo: step, StepKind: kind, CreatedXID: "7", CreatedAt: f.at, Kind: source, SourceKind: sourceKind, SourceID: sourceID, BeforeLabel: acTestPtr(f.label), BeforeProvider: acTestPtr(f.provider), BeforeState: acTestPtr(f.state), BeforeLimitAmount: acTestPtr(strconv.Itoa(f.limit)), BeforeConfigurationRevision: acTestPtr(strconv.Itoa(f.revision)), BeforeHeldAmount: acTestPtr(strconv.Itoa(f.held)), BeforeSpentAmount: acTestPtr(strconv.Itoa(f.spent)), BeforeBlockingCount: acTestPtr(strconv.Itoa(f.block))}
}
func (f *acFixture) finishJournal(j AccountJournalRow) {
	j.AfterLabel = f.label
	j.AfterProvider = f.provider
	j.AfterState = f.state
	j.AfterLimitAmount = strconv.Itoa(f.limit)
	j.AfterConfigurationRevision = strconv.Itoa(f.revision)
	j.AfterHeldAmount = strconv.Itoa(f.held)
	j.AfterSpentAmount = strconv.Itoa(f.spent)
	j.AfterBlockingCount = strconv.Itoa(f.block)
	f.ordinal++
	f.last = j.ID
	if j.AttemptID != nil {
		f.pLast = j.ID
	}
	f.rows.PrefixRows.AccountJournal = append(f.rows.PrefixRows.AccountJournal, j)
	f.sync()
}
func (f *acFixture) configure(label, state string, limit, expected int, rejected bool) {
	root, source, operation, audit := f.id(), f.id(), f.id(), f.id()
	j := f.journal(root, "configure", "1", "account_command", "command", operation)
	before := f.snapshot()
	status, reason := "applied", ""
	if rejected {
		status = "rejected"
		reason = "revision_conflict"
		j.Kind = "configure_rejected"
	} else {
		if f.label != label || f.state != state || f.limit != limit {
			f.revision++
		}
		f.label = label
		f.state = state
		f.limit = limit
	}
	j.CommandSourceID = &source
	j.CommandOperationID = &operation
	s := AccountCommandSourceRow{ID: source, RootID: root, StepNo: "1", StepKind: "account_command", CreatedXID: "7", CreatedAt: f.at, OperationID: operation, ActorID: f.actor, ActorRole: "admin", ActorStatus: "active", Kind: "account_configure", InputTargetID: f.account, TargetID: f.account, InputLabel: label, InputProvider: f.provider, InputState: state, InputLimitAmount: strconv.Itoa(limit), InputExpectedRevision: acTestPtr(strconv.Itoa(expected)), CommandStatus: status, CommandReason: reason, CommandCreatedAt: f.at, AuditID: audit, JournalID: &j.ID}
	s.InputDigest = acTestDigest(s.Kind, s.InputTargetID, procurement.ConfigurationInput{OperationID: operation, ExpectedRevision: strconv.Itoa(expected), Label: label, Provider: f.provider, State: state, LimitAmount: strconv.Itoa(limit)})
	after := "null"
	if !rejected {
		after = f.snapshot()
	}
	f.rows.PrefixRows.AccountCommandSources = append(f.rows.PrefixRows.AccountCommandSources, s)
	f.rows.OriginalRows.ProcurementCommands = append(f.rows.OriginalRows.ProcurementCommands, ProcurementCommandRow{OperationID: operation, Actor: f.actor, Kind: s.Kind, TargetID: f.account, InputDigest: s.InputDigest, Status: status, Reason: reason, BeforeState: before, AfterState: after, CreatedAt: f.at})
	f.rows.OriginalRows.AuditEvents = append(f.rows.OriginalRows.AuditEvents, AuditEventRow{ID: audit, Actor: f.actor, Action: "procurement.command." + status, Target: operation, SanitizedDiff: acTestJSON(map[string]string{"kind": s.Kind, "status": status})})
	f.finishJournal(j)
}
func (f *acFixture) addNode(root, step, kind, source string) {
	f.node++
	f.rows.PrefixRows.AttemptNodes = append(f.rows.PrefixRows.AttemptNodes, AttemptNodeRow{ID: f.id(), AttemptID: f.attempt, Ordinal: strconv.Itoa(f.node), RootID: root, StepNo: step, StepKind: kind, SourceID: source, CreatedXID: "7"})
}
func (f *acFixture) reserve(input, output int) {
	f.reserveAtPrice(input, output, input+output, 1000000, 1000000)
}
func (f *acFixture) reserveAtPrice(input, output, upper, inputRate, outputRate int) {
	f.attempt = f.id()
	f.request = f.id()
	f.binding = f.id()
	f.cost = f.id()
	f.input = input
	f.output = output
	f.upper, f.inputRate, f.outputRate = upper, inputRate, outputRate
	f.node, f.count, f.memberTail, f.permit, f.pLast, f.anomaly = 0, 0, nil, nil, "", false
	root, transition, event := f.id(), f.id(), f.id()
	j := f.journal(root, "reserve", "2", "procurement_transition", "budget_event", event)
	f.rows.PrefixRows.RequestOrigins = append(f.rows.PrefixRows.RequestOrigins, RequestOriginRow{RequestID: f.request, TenantID: f.actor, InputUpperBound: strconv.Itoa(input), OutputLimit: strconv.Itoa(output), RequestDeadline: Time{Valid: true, Time: f.at.Time.Add(time.Hour)}})
	f.rows.OriginalRows.CostVersions = append(f.rows.OriginalRows.CostVersions, CostVersionRow{ID: f.cost, ChannelID: f.actor, UpstreamModel: "model", InputRate: acTestPtr(strconv.Itoa(f.inputRate)), OutputRate: acTestPtr(strconv.Itoa(f.outputRate)), Basis: "contract"})
	f.rows.OriginalRows.CredentialProcurementBindings = append(f.rows.OriginalRows.CredentialProcurementBindings, CredentialProcurementBindingRow{ID: f.binding, AccountID: f.account, CredentialID: f.actor, ChannelID: f.actor})
	f.rows.PrefixRows.AttemptOrigins = append(f.rows.PrefixRows.AttemptOrigins, AttemptOriginRow{AttemptID: f.attempt, RootID: root, StepNo: "1", StepKind: "attempt_birth", CreatedXID: "7", TenantID: f.actor, RequestID: f.request, ChannelID: f.actor, CredentialID: f.actor, UpstreamModel: "model", CostVersionID: acTestPtr(f.cost), CostInputRate: acTestPtr(strconv.Itoa(f.inputRate)), CostOutputRate: acTestPtr(strconv.Itoa(f.outputRate)), CostBasis: "contract", ManagementMode: "managed", AccountID: acTestPtr(f.account), BindingID: acTestPtr(f.binding), ProcurementTransitionID: &transition, ReservedEventID: &event, AccountJournalID: &j.ID, AdmittedAt: f.at})
	f.rows.PrefixRows.AttemptHeads = append(f.rows.PrefixRows.AttemptHeads, AttemptHeadRow{AttemptID: f.attempt, MemberCount: acTestPtr("0")})
	f.rows.OriginalRows.UpstreamAttempts = append(f.rows.OriginalRows.UpstreamAttempts, UpstreamAttemptRow{ID: f.attempt, TenantID: f.actor, RequestID: f.request, DeliveryState: "prepared"})
	f.rows.OriginalRows.ProcurementReservations = append(f.rows.OriginalRows.ProcurementReservations, ProcurementReservationRow{AttemptID: f.attempt, AccountID: f.account, BindingID: f.binding, InputBound: strconv.Itoa(input), OutputBound: strconv.Itoa(output), UpperAmount: strconv.Itoa(f.upper), PPCreatedXID: acTestPtr("7"), PPOriginJournalID: &j.ID, PBAdmittedAt: f.at})
	f.ph = f.upper
	f.ps = 0
	f.pr = 1
	f.pstate = "prepared"
	f.held += f.upper
	j.AttemptID = acTestPtr(f.attempt)
	j.BindingID = acTestPtr(f.binding)
	j.InputBound = acTestPtr(strconv.Itoa(input))
	j.OutputBound = acTestPtr(strconv.Itoa(output))
	j.UpperAmount = acTestPtr(strconv.Itoa(f.upper))
	j.CostVersionID = acTestPtr(f.cost)
	j.CostInputRate = acTestPtr(strconv.Itoa(f.inputRate))
	j.CostOutputRate = acTestPtr(strconv.Itoa(f.outputRate))
	j.CostBasis = acTestPtr("contract")
	j.GateExtra = acTestPtr(strconv.Itoa(f.upper))
	j.GateCheckedAt = f.at
	j.PBDecisionAt = f.at
	j.TransitionID = &transition
	j.ProcurementEventID = &event
	f.fillAfterHold(&j)
	t := ProcurementTransitionRow{ID: transition, RootID: root, StepNo: "2", StepKind: "procurement_transition", CreatedXID: "7", CreatedAt: f.at, TenantID: f.actor, RequestID: f.request, AttemptID: f.attempt, AccountID: f.account, BindingID: f.binding, CauseKind: "attempt_birth", CauseSourceID: f.attempt, EventID: event, JournalID: j.ID, EventType: "reserved", EventKey: "reserve:" + f.attempt, HeldDelta: strconv.Itoa(f.upper), SpentDelta: "0", AfterRevision: "1", AfterState: "prepared", AfterHeld: strconv.Itoa(f.upper), AfterSpent: "0", InputBound: strconv.Itoa(input), OutputBound: strconv.Itoa(output), UpperAmount: strconv.Itoa(f.upper), CostVersionID: f.cost, CostInputRate: strconv.Itoa(f.inputRate), CostOutputRate: strconv.Itoa(f.outputRate), CostBasis: "contract"}
	f.rows.PrefixRows.ProcurementTransitions = append(f.rows.PrefixRows.ProcurementTransitions, t)
	f.originalPE(t)
	f.addNode(root, "2", "procurement_transition", transition)
	f.finishJournal(j)
}
func (f *acFixture) fillAfterHold(j *AccountJournalRow) {
	j.AfterReservationRevision = acTestPtr(strconv.Itoa(f.pr))
	j.AfterReservationState = acTestPtr(f.pstate)
	j.AfterReservationHeldAmount = acTestPtr(strconv.Itoa(f.ph))
	j.AfterReservationSpentAmount = acTestPtr(strconv.Itoa(f.ps))
	j.AfterReservationAnomaly = acTestPtr(f.anomaly)
}
func (f *acFixture) originalPE(t ProcurementTransitionRow) {
	f.rows.OriginalRows.ProcurementBudgetEvents = append(f.rows.OriginalRows.ProcurementBudgetEvents, ProcurementBudgetEventRow{ID: t.EventID, EventKey: t.EventKey, AccountID: t.AccountID, AttemptID: t.AttemptID, Type: t.EventType, EvidenceRef: t.EvidenceRef, HeldDelta: t.HeldDelta, SpentDelta: t.SpentDelta, PPTransitionID: &t.ID, PPJournalID: &t.JournalID, PPRootID: &t.RootID, PPRootStep: &t.StepNo, PPCreatedXID: &t.CreatedXID})
}
func acRun(t *testing.T, rows Rows) accountReplayResult {
	t.Helper()
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	r, err := checkAccountPrefix(w, rows)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func acClean(t *testing.T, rows Rows) {
	t.Helper()
	r := acRun(t, rows)
	if !r.Complete || len(r.Unimplemented) > 0 || r.Stats != (AccountPrefix{}) {
		t.Fatalf("account replay not clean: %#v", r)
	}
}
func TestAccountReplayCommands(t *testing.T) {
	f := newACFixture()
	f.configure("new", "paused", 700, 1, false)
	f.configure("new", "paused", 700, 2, false)
	f.configure("stale", "active", 900, 1, true)
	acClean(t, f.rows)
	r := acRun(t, f.rows)
	if len(r.Candidates.AccountPauseOrdering) != 1 || len(r.Candidates.AccountAdmissionOrder) != 1 {
		t.Fatal("complete local account was not nominated")
	}
	badCut := acClone(f.rows)
	badCut.PrefixRows.AccountJournal[1].Ordinal = "9223372036854775807"
	if got := acRun(t, badCut); got.Stats.CommandRows == 0 || len(got.Candidates.AccountPauseOrdering) != 0 {
		t.Fatal("out-of-range command cut was accepted")
	}
	for _, mutate := range []func(*Rows){func(r *Rows) { r.PrefixRows.AccountJournal[2].AfterConfigurationRevision = "3" }, func(r *Rows) { r.OriginalRows.ProcurementCommands[1].InputDigest = string(make([]byte, 64)) }, func(r *Rows) {
		r.OriginalRows.ProcurementCommands[3].AfterState = r.OriginalRows.ProcurementCommands[0].AfterState
	}, func(r *Rows) { r.PrefixRows.AccountHeads[0].SpentAmount = "1" }} {
		copy := acClone(f.rows)
		mutate(&copy)
		got := acRun(t, copy)
		if got.Stats == (AccountPrefix{}) || len(got.Candidates.AccountPauseOrdering) != 0 {
			t.Fatal("corrupt command/configuration was certified")
		}
	}
	// JSON snapshots compare the old process timezone by instant.
	copy := acClone(f.rows)
	var snapshot map[string]string
	json.Unmarshal([]byte(copy.OriginalRows.ProcurementCommands[0].AfterState), &snapshot)
	snapshot["created_at"] = f.at.Time.In(time.FixedZone("fixture", 8*3600)).Format(time.RFC3339Nano)
	copy.OriginalRows.ProcurementCommands[0].AfterState = acTestJSON(snapshot)
	acClean(t, copy)
}
func acClone(rows Rows) Rows {
	b, _ := json.Marshal(rows)
	var out Rows
	json.Unmarshal(b, &out)
	return out
}
func TestAccountReplayReserveAndGate(t *testing.T) {
	f := newACFixture()
	f.reserve(70, 30)
	acClean(t, f.rows)
	for _, mutation := range []func(*Rows){func(r *Rows) { r.PrefixRows.AccountJournal[1].GateExtra = acTestPtr("99") }, func(r *Rows) { r.PrefixRows.ProcurementTransitions[0].AfterHeld = "99" }, func(r *Rows) { r.OriginalRows.CostVersions[0].OutputRate = acTestPtr("999999") }, func(r *Rows) { r.PrefixRows.AccountJournal[1].BeforeBlockingCount = acTestPtr("1") }} {
		copy := acClone(f.rows)
		mutation(&copy)
		got := acRun(t, copy)
		if got.Stats == (AccountPrefix{}) || len(got.Candidates.AccountAdmissionOrder) != 0 {
			t.Fatal("invalid reserve/gate was nominated")
		}
	}
}

func TestAccountReplayTwoChannelsShareOneAccountPrefix(t *testing.T) {
	f := newACFixture()
	f.reserve(70, 30)  // A independently holds 100.
	f.reserve(140, 60) // B independently holds 200 in the same account.
	secondChannel, secondCredential := f.id(), f.id()
	f.rows.PrefixRows.AttemptOrigins[1].ChannelID = secondChannel
	f.rows.PrefixRows.AttemptOrigins[1].CredentialID = secondCredential
	f.rows.OriginalRows.CostVersions[1].ChannelID = secondChannel
	f.rows.OriginalRows.CredentialProcurementBindings[1].ChannelID = secondChannel
	f.rows.OriginalRows.CredentialProcurementBindings[1].CredentialID = secondCredential
	checkBuckets := func(held, spent, blocking int) {
		t.Helper()
		if f.held != held || f.spent != spent || f.block != blocking {
			t.Fatalf("fixture buckets got %d/%d/%d want %d/%d/%d", f.held, f.spent, f.block, held, spent, blocking)
		}
		acClean(t, f.rows) // Independently replay every historical before/after.
	}
	checkBuckets(300, 0, 0)
	f.selectAttempt(0)
	f.dispatch()
	f.evidence("done", "confirmed_final", 40, 20, "confirm_cost", "settled", 0, 60, false, "none")
	checkBuckets(200, 60, 0)
	firstTerminal := f.rows.OriginalRows.ProcurementReservations[0]
	// The same B dispatch must fail when H(A+B)+S exceeds a new limit, or
	// when the account is paused. A's settled 60 must remain in the gate sum.
	for _, gate := range []struct {
		state string
		limit int
	}{{"active", 250}, {"paused", 1000}} {
		branch := *f
		branch.rows = acClone(f.rows)
		branch.configure("gate-change", gate.state, gate.limit, 1, false)
		branch.selectAttempt(1)
		branch.dispatch()
		if got := acRun(t, branch.rows); got.Stats.GateRows != 1 || len(got.Candidates.AccountAdmissionOrder) != 0 {
			t.Fatalf("B dispatch ignored shared account gate %+v: %#v", gate, got)
		}
	}
	f.configure("maintenance", "paused", 10, 1, false)
	f.selectAttempt(1)
	f.evidence("eof", "partial", 10, 0, "mark_unknown", "unknown", 200, 0, false, "none")
	checkBuckets(200, 60, 0)
	f.evidence("not_sent", "none", 0, 0, "release_no_consumption", "released", 0, 0, false, "none")
	checkBuckets(0, 60, 0) // Favorable terminal effects still work while paused/over limit.
	if !reflect.DeepEqual(f.rows.OriginalRows.ProcurementReservations[0], firstTerminal) {
		t.Fatal("B evidence changed A's terminal position or last journal")
	}
	f.selectAttempt(0)
	f.evidence("not_sent", "none", 0, 0, "dispute", "disputed", 40, 60, true, "settled_positive_no_consumption")
	checkBuckets(40, 60, 1)
	f.selectAttempt(1)
	f.evidence("done", "confirmed_final", 1, 0, "dispute", "disputed", 200, 0, true, "released_positive_usage")
	checkBuckets(240, 60, 2)
	f.selectAttempt(0)
	f.approve(60, "invoice")
	checkBuckets(200, 60, 1)
	f.selectAttempt(1)
	f.approve(25, "invoice")
	checkBuckets(0, 85, 0)
	if f.state != "paused" || f.revision != 2 {
		t.Fatal("already-paused anomalies advanced configuration or clearing blockers reactivated account")
	}
	first, second := f.rows.OriginalRows.ProcurementReservations[0], f.rows.OriginalRows.ProcurementReservations[1]
	if first.State != "settled" || first.SpentAmount != "60" || first.HeldAmount != "0" || first.Revision != "5" || second.State != "settled" || second.SpentAmount != "25" || second.HeldAmount != "0" || second.Revision != "5" || *first.PPLastJournalID == *second.PPLastJournalID || *first.PPLastJournalID == f.last {
		t.Fatal("shared account mixed per-attempt states or last pointers")
	}
	copy := acClone(f.rows)
	copy.OriginalRows.ProcurementReservations[0].PPLastJournalID = copy.OriginalRows.ProcurementReservations[1].PPLastJournalID
	if got := acRun(t, copy); got.Stats.CurrentProjectionRows == 0 || len(got.Candidates.AccountPauseOrdering) != 0 {
		t.Fatal("A accepted B's journal as its own last position")
	}
}

func TestAccountReplayZeroRateStillEnforcesTokenBounds(t *testing.T) {
	for _, usage := range []struct {
		name          string
		input, output int
	}{{"input", 71, 0}, {"output", 0, 31}} {
		t.Run(usage.name, func(t *testing.T) {
			f := newACFixture()
			f.reserveAtPrice(70, 30, 0, 0, 0) // Independently supplied U=0.
			f.dispatch()
			f.evidence("done", "confirmed_final", usage.input, usage.output, "confirm_cost", "settled", 0, 0, true, "none")
			acClean(t, f.rows)
			p := f.rows.OriginalRows.ProcurementReservations[0]
			if p.UpperAmount != "0" || p.SpentAmount != "0" || !p.Anomaly || f.held != 0 || f.spent != 0 || f.block != 0 || f.state != "paused" || f.revision != 2 {
				t.Fatal("zero price concealed an input/output token-bound anomaly")
			}
			// Keep the forged target internally consistent across the last p,
			// transition, journal, account and head. Only the independent token
			// comparison can establish the missing anomaly/pause.
			copy := acClone(f.rows)
			copy.PrefixRows.ProcurementTransitions[1].AfterAnomaly = false
			last := &copy.PrefixRows.AccountJournal[len(copy.PrefixRows.AccountJournal)-1]
			last.AfterReservationAnomaly = acTestPtr(false)
			last.AfterState, last.AfterConfigurationRevision, last.PauseEventID = "active", "1", nil
			copy.OriginalRows.ProcurementReservations[0].Anomaly = false
			copy.OriginalRows.ProcurementAccounts[0].State, copy.OriginalRows.ProcurementAccounts[0].Revision = "active", "1"
			copy.PrefixRows.AccountHeads[0].State, copy.PrefixRows.AccountHeads[0].ConfigurationRevision = "active", "1"
			if got := acRun(t, copy); got.Stats == (AccountPrefix{}) || len(got.Candidates.AccountAdmissionOrder) != 0 || len(got.Candidates.AccountPauseOrdering) != 0 {
				t.Fatal("consistent zero-cost projections suppressed independent token-bound enforcement")
			}
		})
	}
}

// Account mismatch identities (the counter namespace is part of every key):
// birth=account; command=operation or orphan-journal; journal/gate/blocking/
// pause=journal; evidence=choice, orphan-member, (attempt,digest) identity, or
// attempt-head; transition=transition or orphan-journal/orphan-budget-event;
// proposal=proposal; approval=approval or proposal approval-obligation;
// recovery=source; current=account or attempt reservation. Cause is never a key.
func TestAccountReplayMismatchCountersUseNaturalObjects(t *testing.T) {
	type defect struct{ category, id string }
	newCounterReplay := func() *acReplay {
		return &acReplay{
			defects: make(keySet), badAccounts: make(keySet),
			journals:    map[string]AccountJournalRow{tupleKey("journal"): {ID: "journal", SourceKind: "command", SourceID: "operation", CommandSourceID: acTestPtr("source"), TransitionID: acTestPtr("transition")}},
			sources:     map[string]AccountCommandSourceRow{tupleKey("source"): {ID: "source", OperationID: "operation"}},
			commands:    map[string]ProcurementCommandRow{tupleKey("operation"): {OperationID: "operation"}},
			transitions: map[string]ProcurementTransitionRow{tupleKey("transition"): {ID: "transition"}},
			events:      map[string]ProcurementBudgetEventRow{tupleKey("event"): {ID: "event", PPTransitionID: acTestPtr("transition")}},
		}
	}
	for _, test := range []struct {
		name    string
		counter func(*acReplay) *uint64
		defects []defect
	}{
		{"birth", func(e *acReplay) *uint64 { return &e.result.Stats.AccountBirthRows }, []defect{{"birth", "account"}, {"create", "journal"}}},
		{"command", func(e *acReplay) *uint64 { return &e.result.Stats.CommandRows }, []defect{{"configure", "journal"}, {"revision", "journal"}, {"rejection", "journal"}, {"command", "operation"}}},
		{"journal", func(e *acReplay) *uint64 { return &e.result.Stats.JournalRows }, []defect{{"before", "journal"}, {"after", "journal"}, {"hold", "journal"}, {"attempt_action_order", "journal"}}},
		{"gate", func(e *acReplay) *uint64 { return &e.result.Stats.GateRows }, []defect{{"dispatch", "journal"}, {"gate", "journal"}}},
		{"blocking", func(e *acReplay) *uint64 { return &e.result.Stats.BlockingRows }, []defect{{"blocking_before", "journal"}, {"blocking_after", "journal"}, {"blocking", "journal"}}},
		{"evidence", func(e *acReplay) *uint64 { return &e.result.Stats.EvidenceRows }, []defect{{"member", "choice"}, {"choice", "choice"}}},
		{"transition", func(e *acReplay) *uint64 { return &e.result.Stats.TransitionRows }, []defect{{"journal_transition", "journal"}, {"target", "transition"}, {"delta", "transition"}, {"snapshot", "transition"}, {"original_pe", "transition"}, {"pb_required", "transition"}, {"pb_target", "transition"}, {"unreplayed", "transition"}, {"unreplayed_original", "event"}}},
		{"pause", func(e *acReplay) *uint64 { return &e.result.Stats.PauseRows }, []defect{{"pause", "journal"}, {"unexpected_pause", "journal"}}},
		{"proposal", func(e *acReplay) *uint64 { return &e.result.Stats.ProposalRows }, []defect{{"proposal", "proposal"}, {"proposal", "proposal"}}},
		{"approval", func(e *acReplay) *uint64 { return &e.result.Stats.ApprovalRows }, []defect{{"approval", "approval"}, {"approval", "approval"}}},
		{"recovery", func(e *acReplay) *uint64 { return &e.result.Stats.RecoveryRows }, []defect{{"recovery", "recovery"}, {"recovery", "recovery"}}},
		{"current", func(e *acReplay) *uint64 { return &e.result.Stats.CurrentProjectionRows }, []defect{{"missing_original_p", "attempt"}, {"missing_p", "attempt"}, {"p_current", "attempt"}, {"missing_managed", "attempt"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := newCounterReplay()
			counter := test.counter(e)
			for _, d := range test.defects {
				e.bad(counter, d.category, d.id, "account")
			}
			if *counter != 1 {
				t.Fatalf("one natural %s object counted %d times", test.name, *counter)
			}
		})
	}
	e := newCounterReplay()
	for _, category := range []string{"member", "choice", "orphan_member", "member_head", "identity"} {
		e.bad(&e.result.Stats.EvidenceRows, category, "same-id", "account")
	}
	if e.result.Stats.EvidenceRows != 4 {
		t.Fatal("different evidence object types with the same ID suppressed each other")
	}
	e.bad(&e.result.Stats.CurrentProjectionRows, "account_current", "same-id", "account")
	e.bad(&e.result.Stats.CurrentProjectionRows, "p_current", "same-id", "account")
	e.bad(&e.result.Stats.CurrentProjectionRows, "missing_original_p", "same-id", "account")
	if e.result.Stats.CurrentProjectionRows != 2 {
		t.Fatal("account and attempt reservation identities collided")
	}
	e.bad(&e.result.Stats.ApprovalRows, "approval", "same-id", "account")
	e.bad(&e.result.Stats.ApprovalRows, "approval_obligation", "same-id", "account")
	if e.result.Stats.ApprovalRows != 2 {
		t.Fatal("approval and missing proposal-approval obligation collided")
	}
	// Different physical tables may legitimately carry identical UUID text. An
	// unrelated journal/event must never reinterpret a known transition ID.
	e.transitions[tupleKey("same-id")] = ProcurementTransitionRow{ID: "same-id"}
	e.journals[tupleKey("same-id")] = AccountJournalRow{ID: "same-id", SourceKind: "command", SourceID: "operation"}
	e.events[tupleKey("same-id")] = ProcurementBudgetEventRow{ID: "same-id", PPTransitionID: acTestPtr("missing")}
	e.bad(&e.result.Stats.TransitionRows, "target", "same-id", "account")
	e.bad(&e.result.Stats.TransitionRows, "delta", "same-id", "account")
	e.bad(&e.result.Stats.TransitionRows, "journal_transition", "same-id", "account")
	e.bad(&e.result.Stats.TransitionRows, "unreplayed_original", "same-id", "account")
	if e.result.Stats.TransitionRows != 3 {
		t.Fatal("known transition and same-ID orphan journal/PE collided")
	}
	e.bad(&e.result.Stats.CommandRows, "command", "same-id", "account")
	e.bad(&e.result.Stats.CommandRows, "configure", "same-id", "account")
	if e.result.Stats.CommandRows != 2 {
		t.Fatal("a same-ID journal redirected a direct command operation identity")
	}
	e.journals[tupleKey("same-id")] = AccountJournalRow{ID: "same-id"}
	e.bad(&e.result.Stats.CommandRows, "configure", "same-id", "account")
	if e.result.Stats.CommandRows != 3 {
		t.Fatal("orphan command journal collided with a same-ID operation")
	}
	// End-to-end: one transition has both a forged target snapshot and bad
	// delta/original PE metadata. Each check fires, but TransitionRows is one.
	f := newACFixture()
	f.reserve(70, 30)
	f.rows.PrefixRows.ProcurementTransitions[0].AfterHeld = "99"
	f.rows.PrefixRows.ProcurementTransitions[0].HeldDelta = "99"
	f.rows.OriginalRows.ProcurementBudgetEvents[0].HeldDelta = "98"
	if got := acRun(t, f.rows); got.Stats.TransitionRows != 1 {
		t.Fatalf("multi-defect transition counted %d times", got.Stats.TransitionRows)
	}
	f = newACFixture()
	f.reserve(70, 30)
	f.rows.PrefixRows.AccountJournal[1].BeforeHeldAmount = acTestPtr("1")
	f.rows.PrefixRows.AccountJournal[1].AfterHeldAmount = "99"
	f.rows.PrefixRows.AccountJournal[1].AfterReservationHeldAmount = acTestPtr("98")
	if got := acRun(t, f.rows); got.Stats.JournalRows != 1 {
		t.Fatalf("before/after/p snapshot journal counted %d times", got.Stats.JournalRows)
	}
	f = newACFixture()
	f.reserve(70, 30)
	f.evidence("eof", "partial", 1, 0, "mark_unknown", "unknown", 100, 0, false, "none")
	f.rows.PrefixRows.EvidenceChoices[0].MemberCountBefore = "999"
	f.rows.PrefixRows.EvidenceChoices[0].ProcurementBeforeHeld = "99"
	if got := acRun(t, f.rows); got.Stats.EvidenceRows != 1 {
		t.Fatalf("member and choice checks counted one evidence choice %d times", got.Stats.EvidenceRows)
	}
}
func TestAccountReplayIsOrderIndependentAndCanceled(t *testing.T) {
	f := newACFixture()
	f.configure("other", "active", 500, 1, false)
	original := acRun(t, f.rows)
	rows := acClone(f.rows)
	for i, j := 0, len(rows.PrefixRows.AccountJournal)-1; i < j; i, j = i+1, j-1 {
		rows.PrefixRows.AccountJournal[i], rows.PrefixRows.AccountJournal[j] = rows.PrefixRows.AccountJournal[j], rows.PrefixRows.AccountJournal[i]
	}
	if got := acRun(t, rows); !reflect.DeepEqual(got, original) {
		t.Fatal("input array order changed local replay")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := newReplayWork(ctx, rows); err != ErrCanceled {
		t.Fatalf("cancellation got %v", err)
	}
}

func TestAccountReplayCancellationAfterPreflight(t *testing.T) {
	for _, phase := range []string{"final_check", "index"} {
		t.Run(phase, func(t *testing.T) {
			rows := Rows{}
			if phase == "index" {
				for i := 0; i < 3000; i++ {
					rows.OriginalRows.ProcurementAccounts = append(rows.OriginalRows.ProcurementAccounts, ProcurementAccountRow{ID: fmt.Sprint(i)})
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			w, _, err := newReplayWork(ctx, rows)
			if err != nil {
				t.Fatal(err)
			}
			cancel()
			result, err := checkAccountPrefix(w, rows)
			if err != ErrCanceled || !reflect.DeepEqual(result, accountReplayResult{}) {
				t.Fatalf("cancelled %s returned partial result: %#v %v", phase, result, err)
			}
			if phase == "index" && w.b.steps != 256 || phase == "final_check" && w.b.steps >= 256 {
				t.Fatalf("%s did not exercise its intended budget boundary: %d", phase, w.b.steps)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	w, _, err := newReplayWork(ctx, Rows{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	e := &acReplay{w: w}
	if data, ok := e.flatJSON(`{"label":"` + strings.Repeat("x", 60000) + `"}`); ok || data != nil || e.err != ErrCanceled || w.b.steps != 256 {
		t.Fatal("Unicode scan did not stop at the shared cancellation boundary")
	}
	w, _, err = newReplayWork(context.Background(), Rows{})
	if err != nil {
		t.Fatal(err)
	}
	w.b.deadline = time.Now().Add(-time.Second)
	if result, err := checkAccountPrefix(w, Rows{}); err != ErrTimeout || !reflect.DeepEqual(result, accountReplayResult{}) {
		t.Fatal("final account check restarted the original deadline or returned partial state")
	}
}

func (f *acFixture) holdBefore(j *AccountJournalRow) {
	j.AttemptID = acTestPtr(f.attempt)
	j.BindingID = acTestPtr(f.binding)
	j.InputBound = acTestPtr(strconv.Itoa(f.input))
	j.OutputBound = acTestPtr(strconv.Itoa(f.output))
	j.UpperAmount = acTestPtr(strconv.Itoa(f.upper))
	j.CostVersionID = acTestPtr(f.cost)
	j.CostInputRate = acTestPtr(strconv.Itoa(f.inputRate))
	j.CostOutputRate = acTestPtr(strconv.Itoa(f.outputRate))
	j.CostBasis = acTestPtr("contract")
	j.BeforeReservationRevision = acTestPtr(strconv.Itoa(f.pr))
	j.BeforeReservationState = acTestPtr(f.pstate)
	j.BeforeReservationHeldAmount = acTestPtr(strconv.Itoa(f.ph))
	j.BeforeReservationSpentAmount = acTestPtr(strconv.Itoa(f.ps))
	j.BeforeReservationAnomaly = acTestPtr(f.anomaly)
}
func (f *acFixture) dispatch() {
	root, id := f.id(), f.id()
	j := f.journal(root, "dispatch", "1", "attempt_delivery", "dispatch", f.attempt)
	f.holdBefore(&j)
	f.pr++
	f.pstate = "sending"
	j.DeliverySourceID = &id
	j.DispatchAttemptID = acTestPtr(f.attempt)
	j.GateExtra = acTestPtr("0")
	j.GateCheckedAt = f.at
	j.PBDecisionAt = f.at
	f.fillAfterHold(&j)
	f.permit = &id
	f.rows.PrefixRows.AttemptDeliverySources = append(f.rows.PrefixRows.AttemptDeliverySources, AttemptDeliverySourceRow{ID: id, RootID: root, StepNo: "1", StepKind: "attempt_delivery", CreatedXID: "7", TenantID: f.actor, RequestID: f.request, AttemptID: f.attempt, Mode: "dispatch_permit", BeforeDeliveryState: "prepared", AfterDeliveryState: "sending", DispatchAccountID: acTestPtr(f.account), DispatchJournalID: &j.ID, DispatchXID: acTestPtr("7"), PBDispatchCheckedAt: f.at})
	f.addNode(root, "1", "attempt_delivery", id)
	for i := range f.rows.OriginalRows.UpstreamAttempts {
		u := &f.rows.OriginalRows.UpstreamAttempts[i]
		if u.ID == f.attempt {
			u.DeliveryState = "sending"
			u.PPDispatchSourceID = &id
			u.PPDispatchAccountID = acTestPtr(f.account)
			u.PPDispatchJournalID = &j.ID
			u.PBDispatchCheckedAt = f.at
		}
	}
	f.finishJournal(j)
}
func (f *acFixture) evidence(reason, status string, input, output int, decision, targetState string, held, spent int, anomaly bool, conflictCause string) {
	root, source, incoming, meterChoice, meterID := f.id(), f.id(), f.id(), f.id(), f.id()
	usage := billing.Usage{Status: status, InputTokens: int64(input), OutputTokens: int64(output), TotalTokens: int64(input + output)}
	digest := acTestDigestEvidence(usage, reason)
	effective := conflictCause != "none"
	forced := conflictCause == "incoming_meter_conflict"
	collision := "none"
	if forced {
		collision = "source_payload_mismatch"
	}
	c := EvidenceChoiceRow{ID: source, RootID: root, StepNo: "1", StepKind: "evidence_choice", CreatedXID: "7", TenantID: f.actor, RequestID: f.request, AttemptID: f.attempt, IncomingSourceID: incoming, MeterChoiceID: meterChoice, EvidenceDigest: digest, ProcurementBeforeRevision: strconv.Itoa(f.pr), ProcurementBeforeState: f.pstate, ProcurementBeforeHeld: strconv.Itoa(f.ph), ProcurementBeforeSpent: strconv.Itoa(f.ps), ProcurementBeforeAnomaly: f.anomaly, AccountID: f.account, AccountOrdinalCut: strconv.Itoa(f.ordinal), AccountTailCut: f.last, Disposition: "new_member", IdentityAttemptID: f.attempt, IdentityDigest: digest, IdentityEventID: meterID, MemberCountBefore: strconv.Itoa(f.count), MemberTailBefore: f.memberTail, ForcedConflict: forced, EffectiveConflict: &effective, ConflictCause: &conflictCause, Decision: decision}
	f.rows.PrefixRows.IncomingSources = append(f.rows.PrefixRows.IncomingSources, IncomingSourceRow{ID: incoming, AttemptID: acTestPtr(f.attempt), UsageStatus: status, InputTokens: strconv.Itoa(input), OutputTokens: strconv.Itoa(output), TotalTokens: strconv.Itoa(input + output), CachedTokens: "0", ReasoningTokens: "0", FinishReason: "stop", TransportEndReason: reason})
	f.rows.PrefixRows.MeterChoices = append(f.rows.PrefixRows.MeterChoices, MeterChoiceRow{ID: meterChoice, IncomingSourceID: incoming, AttemptID: acTestPtr(f.attempt), MeterEventID: meterID, CollisionKind: collision})
	memberID := f.id()
	member := EvidenceMemberRow{ID: memberID, AttemptID: f.attempt, MemberOrdinal: strconv.Itoa(f.count + 1), PreviousMemberOrdinal: strconv.Itoa(f.count), PreviousMemberID: f.memberTail, IdentityDigest: digest, IdentityEventID: meterID, EvidenceChoiceID: source, RootID: root, StepNo: "1", StepKind: "evidence_choice", SourceID: source, CreatedXID: "7"}
	f.count++
	f.memberTail = &memberID
	c.MemberCountAfter = strconv.Itoa(f.count)
	c.MemberTailAfter = f.memberTail
	f.rows.PrefixRows.EvidenceMembers = append(f.rows.PrefixRows.EvidenceMembers, member)
	f.rows.OriginalRows.ProcurementEvidenceIdentities = append(f.rows.OriginalRows.ProcurementEvidenceIdentities, ProcurementEvidenceIdentityRow{AttemptID: f.attempt, EvidenceDigest: digest, EventID: meterID, PPSourceID: &source})
	f.addNode(root, "1", "evidence_choice", source)
	skips := map[string]string{"skip_terminal_normal": "terminal_normal", "skip_already_unknown": "already_unknown", "skip_already_disputed": "already_disputed", "skip_duplicate_dispute_event": "duplicate_dispute_event"}
	if skip, ok := skips[decision]; ok {
		c.SkipReason = &skip
		f.rows.PrefixRows.EvidenceChoices = append(f.rows.PrefixRows.EvidenceChoices, c)
		f.sync()
		return
	}
	transition, event := f.id(), f.id()
	eventType, eventKey := "", ""
	switch decision {
	case "confirm_cost":
		eventType = "cost_confirmed"
		eventKey = "settle:" + f.attempt
	case "release_no_consumption":
		eventType = "released_no_consumption"
		eventKey = "release:" + f.attempt
	case "mark_unknown":
		eventType = "consumption_unknown"
		eventKey = "unknown:" + f.attempt
	case "dispute":
		eventType = "evidence_disputed"
		eventKey = "conflict:" + f.attempt + ":" + meterID
	}
	j := f.journal(root, eventType, "2", "procurement_transition", "budget_event", event)
	f.holdBefore(&j)
	oldH, oldS, oldState, oldAnomaly := f.ph, f.ps, f.pstate, f.anomaly
	oldRev := f.pr
	oldBlock := 0
	if oldAnomaly && (oldState == "unknown" || oldState == "disputed") {
		oldBlock = 1
	}
	newBlock := 0
	if anomaly && (targetState == "unknown" || targetState == "disputed") {
		newBlock = 1
	}
	f.block += newBlock - oldBlock
	f.held += held - oldH
	f.spent += spent - oldS
	f.ph = held
	f.ps = spent
	f.pstate = targetState
	f.anomaly = anomaly
	if oldH != held || oldS != spent || oldState != targetState || oldAnomaly != anomaly {
		f.pr++
	}
	if anomaly {
		if f.state != "paused" {
			f.revision++
		}
		f.state = "paused"
		j.PauseEventID = &event
	}
	j.TransitionID = &transition
	j.ProcurementEventID = &event
	f.fillAfterHold(&j)
	t := ProcurementTransitionRow{ID: transition, RootID: root, StepNo: "2", StepKind: "procurement_transition", CreatedXID: "7", CreatedAt: f.at, TenantID: f.actor, RequestID: f.request, AttemptID: f.attempt, AccountID: f.account, BindingID: f.binding, CauseKind: "evidence_choice", CauseSourceID: source, EventID: event, JournalID: j.ID, EventType: eventType, EventKey: eventKey, EvidenceRef: meterID, HeldDelta: strconv.Itoa(held - oldH), SpentDelta: strconv.Itoa(spent - oldS), BeforeRevision: acTestPtr(strconv.Itoa(oldRev)), BeforeState: &oldState, BeforeHeld: acTestPtr(strconv.Itoa(oldH)), BeforeSpent: acTestPtr(strconv.Itoa(oldS)), BeforeAnomaly: &oldAnomaly, AfterRevision: strconv.Itoa(f.pr), AfterState: targetState, AfterHeld: strconv.Itoa(held), AfterSpent: strconv.Itoa(spent), AfterAnomaly: anomaly, InputBound: strconv.Itoa(f.input), OutputBound: strconv.Itoa(f.output), UpperAmount: strconv.Itoa(f.upper), CostVersionID: f.cost, CostInputRate: strconv.Itoa(f.inputRate), CostOutputRate: strconv.Itoa(f.outputRate), CostBasis: "contract"}
	c.ProcurementTransitionID = &transition
	c.ProcurementEventID = &event
	c.AccountJournalID = &j.ID
	f.rows.PrefixRows.EvidenceChoices = append(f.rows.PrefixRows.EvidenceChoices, c)
	f.rows.PrefixRows.ProcurementTransitions = append(f.rows.PrefixRows.ProcurementTransitions, t)
	f.originalPE(t)
	f.addNode(root, "2", "procurement_transition", transition)
	f.finishJournal(j)
}
func acTestDigestEvidence(usage billing.Usage, reason string) string {
	raw := acTestJSON(struct {
		Usage  billing.Usage
		Reason string
	}{usage, reason})
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func TestAccountReplayDispatchEvidenceAndPause(t *testing.T) {
	f := newACFixture()
	f.reserve(70, 30)
	f.dispatch()
	f.evidence("done", "confirmed_final", 40, 20, "confirm_cost", "settled", 0, 60, false, "none")
	f.evidence("not_sent", "none", 0, 0, "dispute", "disputed", 40, 60, true, "settled_positive_no_consumption")
	f.configure("reopened", "active", 1000, 2, false)
	f.evidence("eof", "none", 0, 0, "skip_terminal_normal", "disputed", 40, 60, true, "none")
	acClean(t, f.rows)
	for _, mutation := range []func(*Rows){func(r *Rows) { r.PrefixRows.EvidenceChoices[0].EvidenceDigest = "bad" }, func(r *Rows) { r.PrefixRows.EvidenceMembers[1].PreviousMemberID = nil }, func(r *Rows) { r.PrefixRows.AccountJournal[4].AfterConfigurationRevision = "9" }, func(r *Rows) { r.PrefixRows.EvidenceChoices[1].ConflictCause = acTestPtr("incoming_meter_conflict") }, func(r *Rows) { r.PrefixRows.EvidenceChoices[2].Decision = "confirm_cost" }} {
		copy := acClone(f.rows)
		mutation(&copy)
		got := acRun(t, copy)
		if got.Stats == (AccountPrefix{}) || len(got.Candidates.AccountPauseOrdering) != 0 {
			t.Fatal("corrupted evidence/member/pause certified")
		}
	}
}
func TestAccountReplayUnknownAndTerminalSkip(t *testing.T) {
	f := newACFixture()
	f.reserve(70, 30)
	f.evidence("eof", "partial", 1, 0, "mark_unknown", "unknown", 100, 0, false, "none")
	f.evidence("connection_error", "partial", 2, 0, "skip_already_unknown", "unknown", 100, 0, false, "none")
	f.evidence("not_sent", "none", 0, 0, "release_no_consumption", "released", 0, 0, false, "none")
	f.evidence("done", "confirmed_final", 1, 0, "dispute", "disputed", 100, 0, true, "released_positive_usage")
	f.evidence("provider_error", "none", 0, 0, "skip_already_disputed", "disputed", 100, 0, true, "incoming_meter_conflict")
	acClean(t, f.rows)
}

func TestAccountReplayExistingIdentityAndDuplicateDisputeSkip(t *testing.T) {
	f := newACFixture()
	f.reserve(70, 30)
	f.evidence("provider_error", "partial", 1, 0, "dispute", "disputed", 100, 0, true, "incoming_meter_conflict")
	first := f.rows.PrefixRows.EvidenceChoices[0]
	firstMeter := f.rows.PrefixRows.MeterChoices[0].MeterEventID
	firstEvent := f.rows.OriginalRows.ProcurementBudgetEvents[1].ID
	f.approve(30, "invoice")
	f.evidence("connection_error", "partial", 1, 0, "skip_duplicate_dispute_event", "settled", 0, 30, false, "incoming_meter_conflict")
	// A new immutable evidence identity may encounter the same meter-event
	// conflict key after correction reopened the attempt's financial state.
	i := len(f.rows.PrefixRows.EvidenceChoices) - 1
	f.rows.PrefixRows.EvidenceChoices[i].ExistingDisputeEventID = &firstEvent
	f.rows.PrefixRows.EvidenceChoices[i].IdentityEventID = firstMeter
	f.rows.PrefixRows.MeterChoices[i].MeterEventID = firstMeter
	f.rows.PrefixRows.EvidenceMembers[i].IdentityEventID = firstMeter
	f.rows.OriginalRows.ProcurementEvidenceIdentities[i].EventID = firstMeter
	acClean(t, f.rows)
	copy := acClone(f.rows)
	copy.PrefixRows.EvidenceChoices[i].ExistingDisputeEventID = acTestPtr("wrong-prior-event")
	if got := acRun(t, copy); got.Stats.EvidenceRows == 0 || len(got.Candidates.AccountPauseOrdering) != 0 {
		t.Fatal("duplicate dispute was accepted without its actual prior event")
	}
	// Repeating the exact first Usage+Reason identity must return before the
	// conflict branches, add no member, and leave effect/cause observation NULL.
	root, source, incoming, meterChoice := f.id(), f.id(), f.id(), f.id()
	c := first
	c.ID, c.RootID, c.IncomingSourceID, c.MeterChoiceID = source, root, incoming, meterChoice
	c.ProcurementBeforeRevision, c.ProcurementBeforeState, c.ProcurementBeforeHeld, c.ProcurementBeforeSpent, c.ProcurementBeforeAnomaly = strconv.Itoa(f.pr), f.pstate, strconv.Itoa(f.ph), strconv.Itoa(f.ps), f.anomaly
	c.AccountOrdinalCut, c.AccountTailCut = strconv.Itoa(f.ordinal), f.last
	c.Disposition, c.Decision = "existing_member", "skip_existing_identity"
	c.MemberCountBefore, c.MemberCountAfter = strconv.Itoa(f.count), strconv.Itoa(f.count)
	c.MemberTailBefore, c.MemberTailAfter = f.memberTail, f.memberTail
	c.EffectiveConflict, c.ConflictCause, c.ExistingDisputeEventID = nil, nil, nil
	c.ProcurementTransitionID, c.ProcurementEventID, c.AccountJournalID = nil, nil, nil
	c.SkipReason = acTestPtr("existing_identity")
	in := f.rows.PrefixRows.IncomingSources[0]
	in.ID = incoming
	meter := f.rows.PrefixRows.MeterChoices[0]
	meter.ID, meter.IncomingSourceID = meterChoice, incoming
	f.rows.PrefixRows.IncomingSources = append(f.rows.PrefixRows.IncomingSources, in)
	f.rows.PrefixRows.MeterChoices = append(f.rows.PrefixRows.MeterChoices, meter)
	f.rows.PrefixRows.EvidenceChoices = append(f.rows.PrefixRows.EvidenceChoices, c)
	f.addNode(root, "1", "evidence_choice", source)
	f.sync()
	acClean(t, f.rows)
	copy = acClone(f.rows)
	copy.PrefixRows.EvidenceChoices[len(copy.PrefixRows.EvidenceChoices)-1].EffectiveConflict = acTestPtr(false)
	if got := acRun(t, copy); got.Stats.EvidenceRows == 0 || len(got.Candidates.AccountAdmissionOrder) != 0 {
		t.Fatal("existing identity accepted non-NULL conflict observation")
	}
}

func TestAccountReplayTransportReasonDigestAndNoConsumption(t *testing.T) {
	// Literal Go-ABI byte golden, computed from the sealed Usage+Reason shape:
	// {"Usage":{"status":"confirmed_final","input_tokens":1,"output_tokens":0,"total_tokens":1,"cached_tokens":0,"reasoning_tokens":0},"Reason":"done"}
	in := IncomingSourceRow{UsageStatus: "confirmed_final", InputTokens: "1", OutputTokens: "0", TotalTokens: "1", CachedTokens: "0", ReasoningTokens: "0", TransportEndReason: "done", FinishReason: "stop"}
	const golden = "58c1b5053d66f55ba98c823651d95b9ba01d6ede0135ad3e0ea02d942ed6447e"
	if digest, ok := acEvidenceDigest(in); !ok || digest != golden {
		t.Fatalf("transport reason ABI digest changed: %s %v", digest, ok)
	}
	in.FinishReason = "not_sent"
	if digest, ok := acEvidenceDigest(in); !ok || digest != golden {
		t.Fatal("provider finish reason changed procurement evidence identity")
	}
	f := newACFixture()
	f.reserve(70, 30)
	f.evidence("done", "confirmed_final", 1, 0, "confirm_cost", "settled", 0, 1, false, "none")
	f.rows.PrefixRows.IncomingSources[0].FinishReason = "not_sent"
	acClean(t, f.rows)
	// In the opposite direction, an actual transport not_sent releases even
	// when the independent provider finish reason says stop.
	f = newACFixture()
	f.reserve(70, 30)
	f.evidence("not_sent", "none", 0, 0, "release_no_consumption", "released", 0, 0, false, "none")
	acClean(t, f.rows)
}

func TestAccountReplayNotFoundAndStrictJSON(t *testing.T) {
	f := newACFixture()
	operation, source, root, audit, target := f.id(), f.id(), f.id(), f.id(), f.id()
	s := AccountCommandSourceRow{ID: source, RootID: root, StepNo: "1", CreatedXID: "7", OperationID: operation, ActorID: f.actor, ActorRole: "admin", ActorStatus: "active", Kind: "account_configure", InputTargetID: target, TargetID: target, InputExpectedRevision: acTestPtr("1"), InputLabel: "missing", InputProvider: "provider", InputState: "active", InputLimitAmount: "10", CommandStatus: "rejected", CommandReason: "not_found", CommandCreatedAt: f.at, AuditID: audit}
	s.InputDigest = acTestDigest(s.Kind, target, procurement.ConfigurationInput{OperationID: operation, ExpectedRevision: "1", Label: "missing", Provider: "provider", State: "active", LimitAmount: "10"})
	f.rows.PrefixRows.AccountCommandSources = append(f.rows.PrefixRows.AccountCommandSources, s)
	f.rows.OriginalRows.ProcurementCommands = append(f.rows.OriginalRows.ProcurementCommands, ProcurementCommandRow{OperationID: operation, Actor: f.actor, Kind: s.Kind, TargetID: target, InputDigest: s.InputDigest, Status: "rejected", Reason: "not_found", BeforeState: "null", AfterState: "null", CreatedAt: f.at})
	f.rows.OriginalRows.AuditEvents = append(f.rows.OriginalRows.AuditEvents, AuditEventRow{ID: audit, Actor: f.actor, Action: "procurement.command.rejected", Target: operation, SanitizedDiff: `{"kind":"account_configure","status":"rejected"}`})
	acClean(t, f.rows)
	bad := []string{`{"kind":"account_create","status":"applied","\u0073tatus":"applied"}`, `{"kind":"account_create","status":"applied","ignored":"x"}`, `{"kind":"account_create","status":"applied"} {}`, `{"kind":"account_create","status":"\ud800"}`, `{"kind":"account_create","status":"\udc00"}`}
	for _, raw := range bad {
		copy := acClone(f.rows)
		copy.OriginalRows.AuditEvents[0].SanitizedDiff = raw
		r := acRun(t, copy)
		if r.Stats.CommandRows != 1 {
			t.Fatalf("strict JSON mutation was accepted: %s %#v", raw, r.Stats)
		}
	}
	e := &acReplay{w: &replayWork{b: budget{ctx: context.Background()}}}
	if _, ok := e.flatJSON(`{"value":"\ud83d\ude80"}`); !ok {
		t.Fatal("valid UTF16 pair rejected")
	}
}
func TestAccountReplaySupplierPBSlotAndWindow(t *testing.T) {
	f := newACFixture()
	f.reserve(70, 30)
	control, allocation, window, event := f.id(), f.id(), f.id(), f.id()
	pe := f.rows.OriginalRows.ProcurementBudgetEvents[0]
	f.rows.OriginalRows.ProcurementReservations[0].PBControl = &control
	f.rows.OriginalRows.PeriodicBudgetControls = []PeriodicBudgetControlRow{{ID: control, Scope: "procurement", AccountID: acTestPtr(f.account)}}
	f.rows.OriginalRows.PeriodicBudgetWindows = []PeriodicBudgetWindowRow{{ID: window, ControlID: control, StartAt: Time{Valid: true, Time: f.at.Time.Add(-time.Hour)}, EndAt: Time{Valid: true, Time: f.at.Time.Add(time.Hour)}}}
	f.rows.OriginalRows.PeriodicBudgetAllocations = []PeriodicBudgetAllocationRow{{ID: allocation, ControlID: control, SubjectType: "supplier_attempt", SubjectID: f.attempt, Origin: "new", OriginSourceID: pe.ID, WindowID: &window, AdmittedAt: f.at, UpperAmount: "100", Baseline: "0"}}
	f.rows.OriginalRows.PeriodicBudgetEvents = []PeriodicBudgetEventRow{{ID: event, ControlID: control, AllocationID: allocation, SourceKind: "procurement_event", SourceID: pe.ID, Seq: "1", AllocationBeforeRevision: "1", WindowBeforeRevision: acTestPtr("1"), BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "100", AfterSpent: "0", AfterSourceSpent: "0", AfterState: "held", HeldDelta: "100", SpentDelta: "0", CreatedXID: "7"}}
	acClean(t, f.rows)
	for _, mutation := range []func(*Rows){func(r *Rows) { r.OriginalRows.PeriodicBudgetWindows[0].ControlID = "wrong" }, func(r *Rows) {
		r.OriginalRows.PeriodicBudgetAllocations[0].AdmittedAt.Time = r.OriginalRows.PeriodicBudgetWindows[0].EndAt.Time
	}, func(r *Rows) { r.OriginalRows.PeriodicBudgetEvents[0].CreatedXID = "8" }, func(r *Rows) { r.OriginalRows.PeriodicBudgetEvents = nil }} {
		copy := acClone(f.rows)
		mutation(&copy)
		if got := acRun(t, copy); got.Stats.TransitionRows == 0 || len(got.Candidates.AccountAdmissionOrder) != 0 {
			t.Fatal("PB slot/window mutation certified")
		}
	}
}

func (f *acFixture) approve(amount int, basis string) {
	birthID, proposalID, root, proposalAudit := f.id(), f.id(), f.id(), f.id()
	birth := ProposalBirthRow{ID: birthID, RootID: root, StepNo: "1", StepKind: "proposal_birth", CreatedXID: "7", TenantID: f.actor, RequestID: f.request, AttemptID: f.attempt, ProposalID: proposalID, Requester: f.actor, RequesterRole: "finance_operator", RequesterStatus: "active", ReservationRevision: strconv.Itoa(f.pr), ReservationState: f.pstate, ReservationHeld: strconv.Itoa(f.ph), ReservationSpent: strconv.Itoa(f.ps), ReservationAnomaly: f.anomaly, MemberCount: strconv.Itoa(f.count), MemberTail: f.memberTail, AccountID: f.account, AccountOrdinalCut: strconv.Itoa(f.ordinal), AccountTailCut: f.last, AuditID: proposalAudit}
	f.rows.PrefixRows.ProposalBirths = append(f.rows.PrefixRows.ProposalBirths, birth)
	proposal := ProcurementCostProposalRow{ID: proposalID, TenantID: f.actor, AttemptID: f.attempt, Amount: strconv.Itoa(amount), Basis: basis, EvidenceRef: "invoice:fixture", Reason: "supplier invoice", Requester: f.actor, ExpectedReservationRevision: strconv.Itoa(f.pr), ExpectedEvidenceCount: strconv.Itoa(f.count), State: "approved", CreatedAt: f.at, ApprovedAt: f.at}
	f.rows.OriginalRows.AuditEvents = append(f.rows.OriginalRows.AuditEvents, AuditEventRow{ID: proposalAudit, Actor: f.actor, TenantScope: acTestPtr(f.actor), Action: "procurement.cost_proposed", Target: proposalID, RequestID: f.request, SanitizedDiff: acTestJSON(map[string]any{"attempt_id": f.attempt, "amount": amount, "basis": basis, "evidence_ref": proposal.EvidenceRef})})
	f.addNode(root, "1", "proposal_birth", birthID)
	approvalID, approvalRoot, operation, approver, transition, event, correction, audit := f.id(), f.id(), f.id(), f.id(), f.id(), f.id(), f.id(), f.id()
	j := f.journal(approvalRoot, "cost_corrected", "2", "procurement_transition", "budget_event", event)
	f.holdBefore(&j)
	beforeRevision, beforeState, beforeHeld, beforeSpent, beforeAnomaly := f.pr, f.pstate, f.ph, f.ps, f.anomaly
	afterAnomaly := amount > f.upper
	oldBlock := 0
	if beforeAnomaly && (beforeState == "unknown" || beforeState == "disputed") {
		oldBlock = 1
	}
	f.block -= oldBlock
	f.held -= beforeHeld
	f.spent += amount - beforeSpent
	f.ph = 0
	f.ps = amount
	f.pstate = "settled"
	f.anomaly = afterAnomaly
	if beforeState != "settled" || beforeHeld != 0 || beforeSpent != amount || beforeAnomaly != afterAnomaly {
		f.pr++
	}
	if afterAnomaly {
		if f.state != "paused" {
			f.revision++
		}
		f.state = "paused"
		j.PauseEventID = &event
	}
	f.fillAfterHold(&j)
	j.TransitionID = &transition
	j.ProcurementEventID = &event
	approval := ApprovalConsumptionRow{ID: approvalID, RootID: approvalRoot, StepNo: "1", StepKind: "approval_consumed", CreatedXID: "7", TenantID: f.actor, RequestID: f.request, AttemptID: f.attempt, ProposalID: proposalID, ProposalBirthID: birthID, ApprovalOperationID: operation, Requester: f.actor, RequesterRole: "finance_operator", RequesterStatus: "active", Approver: approver, ApproverRole: "finance_approver", ApproverStatus: "active", ConsumedRevision: strconv.Itoa(beforeRevision), ConsumedState: beforeState, ConsumedHeld: strconv.Itoa(beforeHeld), ConsumedSpent: strconv.Itoa(beforeSpent), ConsumedAnomaly: beforeAnomaly, ConsumedMemberCount: strconv.Itoa(f.count), ConsumedMemberTail: f.memberTail, AccountID: f.account, AccountOrdinalCut: strconv.Itoa(f.ordinal), AccountTailCut: f.last, ProcurementTransitionID: transition, ProcurementEventID: event, ProviderCorrectionID: correction, AuditID: audit, AfterRevision: strconv.Itoa(f.pr), AfterState: f.pstate, AfterHeld: "0", AfterSpent: strconv.Itoa(amount), AfterAnomaly: afterAnomaly}
	proposal.Approver = &approver
	proposal.ApprovalOperationID = &operation
	f.rows.PrefixRows.ApprovalConsumptions = append(f.rows.PrefixRows.ApprovalConsumptions, approval)
	f.rows.OriginalRows.ProcurementCostProposals = append(f.rows.OriginalRows.ProcurementCostProposals, proposal)
	f.rows.OriginalRows.ProviderCostCorrections = append(f.rows.OriginalRows.ProviderCostCorrections, ProviderCostCorrectionRow{ID: correction, TenantID: f.actor, AttemptID: f.attempt, OperationID: "procurement:" + operation, Amount: strconv.Itoa(amount), Basis: basis, EvidenceRef: proposal.EvidenceRef})
	f.rows.OriginalRows.AuditEvents = append(f.rows.OriginalRows.AuditEvents, AuditEventRow{ID: audit, Actor: approver, TenantScope: acTestPtr(f.actor), Action: "procurement.cost_approved", Target: proposalID, RequestID: f.request, SanitizedDiff: acTestJSON(map[string]any{"attempt_id": f.attempt, "amount": amount, "evidence_ref": proposal.EvidenceRef})})
	f.addNode(approvalRoot, "1", "approval_consumed", approvalID)
	t := ProcurementTransitionRow{ID: transition, RootID: approvalRoot, StepNo: "2", StepKind: "procurement_transition", CreatedXID: "7", CreatedAt: f.at, TenantID: f.actor, RequestID: f.request, AttemptID: f.attempt, AccountID: f.account, BindingID: f.binding, CauseKind: "approval_consumed", CauseSourceID: approvalID, EventID: event, JournalID: j.ID, EventType: "cost_corrected", EventKey: "correction:" + operation, EvidenceRef: proposal.EvidenceRef, HeldDelta: strconv.Itoa(-beforeHeld), SpentDelta: strconv.Itoa(amount - beforeSpent), BeforeRevision: acTestPtr(strconv.Itoa(beforeRevision)), BeforeState: &beforeState, BeforeHeld: acTestPtr(strconv.Itoa(beforeHeld)), BeforeSpent: acTestPtr(strconv.Itoa(beforeSpent)), BeforeAnomaly: &beforeAnomaly, AfterRevision: strconv.Itoa(f.pr), AfterState: f.pstate, AfterHeld: "0", AfterSpent: strconv.Itoa(amount), AfterAnomaly: afterAnomaly, InputBound: strconv.Itoa(f.input), OutputBound: strconv.Itoa(f.output), UpperAmount: strconv.Itoa(f.upper), CostVersionID: f.cost, CostInputRate: strconv.Itoa(f.inputRate), CostOutputRate: strconv.Itoa(f.outputRate), CostBasis: "contract"}
	f.rows.PrefixRows.ProcurementTransitions = append(f.rows.PrefixRows.ProcurementTransitions, t)
	f.originalPE(t)
	f.addNode(approvalRoot, "2", "procurement_transition", transition)
	f.finishJournal(j)
}
func TestAccountReplayProposalApprovalMemberBarrier(t *testing.T) {
	f := newACFixture()
	f.reserve(70, 30)
	f.evidence("done", "confirmed_final", 40, 20, "confirm_cost", "settled", 0, 60, false, "none")
	f.approve(25, "invoice")
	acClean(t, f.rows)
	if r := acRun(t, f.rows); len(r.Candidates.ProposalBarrier) != 1 {
		t.Fatal("valid full proposal barrier not nominated")
	}
	for _, mutation := range []func(*Rows){func(r *Rows) {
		r.PrefixRows.ApprovalConsumptions[0].ConsumedMemberTail = acTestPtr("other-member-same-count")
	}, func(r *Rows) {
		r.PrefixRows.ApprovalConsumptions[0].Approver = r.PrefixRows.ApprovalConsumptions[0].Requester
	}, func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].Basis = "contract" }, func(r *Rows) {
		r.OriginalRows.AuditEvents[len(r.OriginalRows.AuditEvents)-1].SanitizedDiff = `{"attempt_id":"wrong","amount":25,"evidence_ref":"invoice:fixture"}`
	}} {
		copy := acClone(f.rows)
		mutation(&copy)
		got := acRun(t, copy)
		if got.Stats == (AccountPrefix{}) || len(got.Candidates.ProposalBarrier) != 0 {
			t.Fatal("bad proposal/approval barrier nominated")
		}
	}
	same := newACFixture()
	same.reserve(70, 30)
	same.evidence("done", "confirmed_final", 40, 20, "confirm_cost", "settled", 0, 60, false, "none")
	same.approve(60, "invoice")
	acClean(t, same.rows)
	if same.pr != 2 {
		t.Fatal("same amount correction fixture advanced p revision")
	}
}

func TestAccountReplayAccountAndAttemptOrdersCannotDiverge(t *testing.T) {
	f := newACFixture()
	f.reserve(70, 30)
	f.evidence("done", "confirmed_final", 40, 20, "confirm_cost", "settled", 0, 60, false, "none")
	f.approve(60, "invoice")
	f.approve(60, "invoice")
	acClean(t, f.rows)
	if f.pr != 2 {
		t.Fatal("fixture needs equal-valued corrections that preserve p revision")
	}
	copy := acClone(f.rows)
	journals := copy.PrefixRows.AccountJournal
	last := len(journals) - 1
	journals[last-1], journals[last] = journals[last], journals[last-1]
	for i := 1; i < len(journals); i++ {
		journals[i].Ordinal, journals[i].PreviousOrdinal, journals[i].PreviousID = strconv.Itoa(i+1), strconv.Itoa(i), acTestPtr(journals[i-1].ID)
	}
	copy.PrefixRows.AccountHeads[0].JournalID = journals[last].ID
	copy.OriginalRows.ProcurementReservations[0].PPLastJournalID = acTestPtr(journals[last].ID)
	for i := range copy.PrefixRows.ApprovalConsumptions {
		approval := &copy.PrefixRows.ApprovalConsumptions[i]
		for _, journal := range journals {
			if journal.TransitionID != nil && *journal.TransitionID == approval.ProcurementTransitionID {
				approval.AccountOrdinalCut, approval.AccountTailCut = journal.PreviousOrdinal, *journal.PreviousID
			}
		}
		for j := range copy.PrefixRows.ProposalBirths {
			birth := &copy.PrefixRows.ProposalBirths[j]
			if birth.ID == approval.ProposalBirthID {
				birth.AccountOrdinalCut, birth.AccountTailCut = approval.AccountOrdinalCut, approval.AccountTailCut
			}
		}
	}
	if got := acRun(t, copy); got.Stats.JournalRows == 0 || len(got.Candidates.AccountAdmissionOrder) != 0 || len(got.Candidates.AccountPauseOrdering) != 0 || len(got.Candidates.ProposalBarrier) != 0 {
		t.Fatal("same-valued corrections reordered the account history against immutable attempt nodes")
	}
	// Without reordering any action, a proposal cannot read a later equal-valued
	// p state, or roll its account observation back across an earlier action.
	for _, boundary := range []struct {
		birth, journal int
	}{{0, last}, {1, 2}} {
		copy := acClone(f.rows)
		birth := &copy.PrefixRows.ProposalBirths[boundary.birth]
		journal := copy.PrefixRows.AccountJournal[boundary.journal]
		birth.AccountOrdinalCut, birth.AccountTailCut = journal.Ordinal, journal.ID
		if got := acRun(t, copy); got.Stats.ProposalRows == 0 || len(got.Candidates.ProposalBarrier) != 0 {
			t.Fatalf("proposal %d consumed future/past equal-state account cut", boundary.birth)
		}
	}
	// A no-effect evidence choice also needs the bridge: it must observe the
	// last correction already preceding its node, even with an unchanged p.
	f.evidence("eof", "none", 0, 0, "skip_terminal_normal", "settled", 0, 60, false, "none")
	acClean(t, f.rows)
	copy = acClone(f.rows)
	choice := &copy.PrefixRows.EvidenceChoices[len(copy.PrefixRows.EvidenceChoices)-1]
	choice.AccountOrdinalCut, choice.AccountTailCut = "3", copy.PrefixRows.AccountJournal[2].ID
	if got := acRun(t, copy); got.Stats.EvidenceRows == 0 || len(got.Candidates.AccountAdmissionOrder) != 0 {
		t.Fatal("no-effect evidence rolled its cut back across equal-valued corrections")
	}
	// Two no-effect observations cannot move backward across an account-only
	// configuration entry while staying in the same pair of attempt actions.
	f.configure("new-label", "active", 1000, 1, false)
	f.evidence("connection_error", "none", 0, 0, "skip_terminal_normal", "settled", 0, 60, false, "none")
	f.evidence("provider_error", "none", 0, 0, "skip_terminal_normal", "settled", 0, 60, false, "none")
	acClean(t, f.rows)
	copy = acClone(f.rows)
	choice = &copy.PrefixRows.EvidenceChoices[len(copy.PrefixRows.EvidenceChoices)-1]
	choice.AccountOrdinalCut, choice.AccountTailCut = "5", copy.PrefixRows.AccountJournal[4].ID
	if got := acRun(t, copy); got.Stats.EvidenceRows == 0 || len(got.Candidates.AccountPauseOrdering) != 0 {
		t.Fatal("successive no-effect observations moved backward inside one action interval")
	}
}
func (f *acFixture) recover() {
	root, source, delivery, transition, event := f.id(), f.id(), f.id(), f.id(), f.id()
	decision, pAfter, dAfter, eventKey := "recovered_unknown", "unknown", "unknown", "unknown:"+f.attempt
	newHeld := f.ph
	if f.permit == nil && f.pstate == "prepared" {
		decision = "expired_without_permit"
		pAfter = "released"
		dAfter = "failed"
		eventKey = "release:" + f.attempt
		newHeld = 0
	}
	j := f.journal(root, decision, "3", "procurement_transition", "budget_event", event)
	f.holdBefore(&j)
	beforeRevision, beforeState, beforeHeld, beforeAnomaly := f.pr, f.pstate, f.ph, f.anomaly
	deliveryBefore := "prepared"
	if f.permit != nil {
		deliveryBefore = "sending"
	}
	sourceRow := RecoverySourceRow{ID: source, RootID: root, StepNo: "1", StepKind: "recovery", CreatedXID: "7", TenantID: f.actor, RequestID: f.request, AttemptID: f.attempt, CheckedAt: Time{Valid: true, Time: f.at.Time.Add(2 * time.Hour)}, RequestDeadline: f.rows.PrefixRows.RequestOrigins[0].RequestDeadline, ProcurementBeforeRevision: strconv.Itoa(f.pr), ProcurementBeforeState: f.pstate, ProcurementBeforeHeld: strconv.Itoa(f.ph), ProcurementBeforeSpent: strconv.Itoa(f.ps), ProcurementBeforeAnomaly: f.anomaly, DeliveryBeforeState: deliveryBefore, PermitSourceID: f.permit, Decision: decision, ProcurementTransitionID: transition, DeliveryMutationID: delivery, ProcurementEventID: event, AccountJournalID: j.ID}
	f.rows.PrefixRows.RecoverySources = append(f.rows.PrefixRows.RecoverySources, sourceRow)
	f.addNode(root, "1", "recovery", source)
	f.rows.PrefixRows.AttemptDeliverySources = append(f.rows.PrefixRows.AttemptDeliverySources, AttemptDeliverySourceRow{ID: delivery, RootID: root, StepNo: "2", StepKind: "attempt_delivery", CreatedXID: "7", TenantID: f.actor, RequestID: f.request, AttemptID: f.attempt, Mode: "recovery", CauseSourceID: &source, BeforeDeliveryState: deliveryBefore, AfterDeliveryState: dAfter})
	f.addNode(root, "2", "attempt_delivery", delivery)
	f.held += newHeld - f.ph
	f.ph = newHeld
	f.pstate = pAfter
	f.pr++
	f.anomaly = false
	f.fillAfterHold(&j)
	j.TransitionID = &transition
	j.ProcurementEventID = &event
	t := ProcurementTransitionRow{ID: transition, RootID: root, StepNo: "3", StepKind: "procurement_transition", CreatedXID: "7", CreatedAt: f.at, TenantID: f.actor, RequestID: f.request, AttemptID: f.attempt, AccountID: f.account, BindingID: f.binding, CauseKind: "recovery", CauseSourceID: source, EventID: event, JournalID: j.ID, EventType: decision, EventKey: eventKey, HeldDelta: strconv.Itoa(newHeld - beforeHeld), SpentDelta: "0", BeforeRevision: acTestPtr(strconv.Itoa(beforeRevision)), BeforeState: &beforeState, BeforeHeld: acTestPtr(strconv.Itoa(beforeHeld)), BeforeSpent: acTestPtr("0"), BeforeAnomaly: &beforeAnomaly, AfterRevision: strconv.Itoa(f.pr), AfterState: f.pstate, AfterHeld: strconv.Itoa(f.ph), AfterSpent: "0", InputBound: strconv.Itoa(f.input), OutputBound: strconv.Itoa(f.output), UpperAmount: strconv.Itoa(f.upper), CostVersionID: f.cost, CostInputRate: strconv.Itoa(f.inputRate), CostOutputRate: strconv.Itoa(f.outputRate), CostBasis: "contract"}
	f.rows.PrefixRows.ProcurementTransitions = append(f.rows.PrefixRows.ProcurementTransitions, t)
	f.originalPE(t)
	f.addNode(root, "3", "procurement_transition", transition)
	f.finishJournal(j)
}
func TestAccountReplayRecoveryPermitAndDeadline(t *testing.T) {
	for _, permit := range []bool{false, true} {
		f := newACFixture()
		f.reserve(70, 30)
		if permit {
			f.dispatch()
		}
		f.recover()
		acClean(t, f.rows)
		for _, checkedAt := range []Time{f.at, f.rows.PrefixRows.RecoverySources[0].RequestDeadline} {
			copy := acClone(f.rows)
			copy.PrefixRows.RecoverySources[0].CheckedAt = checkedAt
			if got := acRun(t, copy); got.Stats.TransitionRows == 0 || len(got.Candidates.AccountAdmissionOrder) != 0 {
				t.Fatal("nonexpired recovery or exact-deadline recovery accepted")
			}
		}
		if permit {
			copy := acClone(f.rows)
			copy.PrefixRows.RecoverySources[0].PermitSourceID = nil
			if got := acRun(t, copy); got.Stats.RecoveryRows == 0 {
				t.Fatal("real sending permit was forgotten")
			}
		}
	}
}
