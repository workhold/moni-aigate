package procurementprefix

// This domain is deliberately independent of the SQL writer and billing's
// mutable state/price helpers. SourceClosure still owns physical graph closure.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type accountReplayResult struct {
	Stats         AccountPrefix
	Candidates    propositionKeys
	Complete      bool
	Unimplemented []string
}

type acConfig struct{ label, provider, state, limit, revision string }
type acPosition struct {
	ordinal     int64
	journal     string
	config      acConfig
	held, spent string
	blocking    int64
}
type acHold struct {
	account, attempt, binding, input, output, upper, version, inputRate, outputRate, basis string
	held, spent, state, revision, journal                                                  string
	anomaly                                                                                bool
}
type acVersion struct {
	ordinal int64
	hold    acHold
}
type acAction struct {
	ordinal, node int64
	account       string
}
type acEvidence struct {
	good        bool
	ordinal     int64
	memberCount int64
	memberTail  *string
	incoming    IncomingSourceRow
	meter       MeterChoiceRow
}
type acReplay struct {
	w               *replayWork
	rows            Rows
	result          accountReplayResult
	err             error
	duplicate       bool
	defects         keySet
	badAccounts     keySet
	badProposals    keySet
	accounts        map[string]ProcurementAccountRow
	origins         map[string]AccountOriginRow
	heads           map[string]AccountHeadRow
	journals        map[string]AccountJournalRow
	accountJournals map[string][]AccountJournalRow
	cuts            map[string][]acPosition
	holds           map[string][]acVersion
	actions         map[string][]acAction
	badActionOrder  keySet
	commands        map[string]ProcurementCommandRow
	sources         map[string]AccountCommandSourceRow
	attempts        map[string]AttemptOriginRow
	upstream        map[string]UpstreamAttemptRow
	requests        map[string]RequestOriginRow
	costs           map[string]CostVersionRow
	bindings        map[string]CredentialProcurementBindingRow
	reservations    map[string]ProcurementReservationRow
	transitions     map[string]ProcurementTransitionRow
	events          map[string]ProcurementBudgetEventRow
	eventKeys       map[string]ProcurementBudgetEventRow
	eventOrd        map[string]int64
	deliveries      map[string]AttemptDeliverySourceRow
	incoming        map[string]IncomingSourceRow
	meters          map[string]MeterChoiceRow
	evidence        map[string]EvidenceChoiceRow
	evaluated       map[string]acEvidence
	members         map[string]EvidenceMemberRow
	memberChains    map[string][]EvidenceMemberRow
	nodeOrd         map[string]int64
	memberNodeOrd   map[string][]int64
	proposals       map[string]ProcurementCostProposalRow
	births          map[string]ProposalBirthRow
	approvals       map[string]ApprovalConsumptionRow
	recoveries      map[string]RecoverySourceRow
	audit           map[string]AuditEventRow
	corrections     map[string]ProviderCostCorrectionRow
	pbEvents        map[string][]PeriodicBudgetEventRow
	pbAllocations   map[string]PeriodicBudgetAllocationRow
	pbControls      map[string]PeriodicBudgetControlRow
	pbWindows       map[string]PeriodicBudgetWindowRow
}

func acIndex[T any](e *acReplay, list []T, key func(T) string) map[string]T {
	out := make(map[string]T, len(list))
	for _, r := range list {
		if !e.tick() {
			break
		}
		k := tupleKey(key(r))
		if _, ok := out[k]; ok {
			e.defects.add("duplicate", k)
			e.duplicate = true
		}
		out[k] = r
	}
	return out
}
func (e *acReplay) tick() bool {
	if e.err != nil {
		return false
	}
	e.err = e.w.tick()
	return e.err == nil
}
func (e *acReplay) bad(counter *uint64, category, id, account string) {
	counterName := "current"
	kind := "journal"
	switch counter {
	case &e.result.Stats.AccountBirthRows:
		counterName = "birth"
		kind = "account"
		id = account
	case &e.result.Stats.CommandRows:
		counterName = "command"
		kind = "operation"
		if category != "command" {
			j, found := e.journals[tupleKey(id)]
			source, sourced := e.sources[tupleKey(acPtr(j.CommandSourceID))]
			if found && sourced {
				id = source.OperationID
			} else if command, known := e.commands[tupleKey(j.SourceID)]; found && j.SourceKind == "command" && known {
				id = command.OperationID
			} else {
				kind = "orphan_journal"
			}
		}
	case &e.result.Stats.JournalRows:
		counterName = "journal"
	case &e.result.Stats.GateRows:
		counterName = "gate"
	case &e.result.Stats.BlockingRows:
		counterName = "blocking"
	case &e.result.Stats.EvidenceRows:
		counterName = "evidence"
		switch category {
		case "orphan_member":
			kind = "member"
		case "identity":
			kind = "identity"
		case "member_head":
			kind = "head"
		default:
			kind = "choice"
		}
	case &e.result.Stats.TransitionRows:
		counterName = "transition"
		kind = "transition"
		switch category {
		case "journal_transition":
			j := e.journals[tupleKey(id)]
			if transition, known := e.transitions[tupleKey(acPtr(j.TransitionID))]; known {
				id = transition.ID
			} else {
				kind = "orphan_journal"
			}
		case "unreplayed_original":
			pe := e.events[tupleKey(id)]
			if transition, known := e.transitions[tupleKey(acPtr(pe.PPTransitionID))]; known {
				id = transition.ID
			} else {
				kind = "orphan_budget_event"
			}
		}
	case &e.result.Stats.PauseRows:
		counterName = "pause"
	case &e.result.Stats.ProposalRows:
		counterName = "proposal"
		kind = "proposal"
	case &e.result.Stats.ApprovalRows:
		counterName = "approval"
		kind = "approval"
		if category == "approval_obligation" {
			kind = "proposal_obligation"
		}
	case &e.result.Stats.RecoveryRows:
		counterName = "recovery"
		kind = "source"
	case &e.result.Stats.CurrentProjectionRows:
		counterName = "current"
		if category == "account_current" {
			kind = "account"
		} else {
			kind = "reservation"
		}
	}
	k := tupleKey(counterName, kind, id)
	if _, ok := e.defects[k]; !ok {
		e.defects[k] = struct{}{}
		*counter++
	}
	if account != "" {
		e.badAccounts.add(account)
	}
}
func acPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func acPtrEq(p *string, s string) bool { return p != nil && *p == s }
func acSamePtr(a, b *string) bool      { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func acInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && strconv.FormatInt(n, 10) == s
}
func acBig(s string) (*big.Int, bool) {
	n, ok := new(big.Int).SetString(s, 10)
	return n, ok && n.String() == s && len(strings.TrimPrefix(s, "-")) <= 100
}
func acAdd(a, b string) string {
	x, ok := acBig(a)
	y, yes := acBig(b)
	if !ok || !yes {
		return "!"
	}
	return x.Add(x, y).String()
}
func acSub(a, b string) string {
	x, ok := acBig(a)
	y, yes := acBig(b)
	if !ok || !yes {
		return "!"
	}
	return x.Sub(x, y).String()
}
func acCmp(a, b string) int {
	x, ok := acBig(a)
	y, yes := acBig(b)
	if !ok || !yes {
		return 2
	}
	return x.Cmp(y)
}
func acAmount(input, output, inputRate, outputRate string) (string, bool) {
	a, ok := acBig(input)
	b, y := acBig(output)
	r, z := acBig(inputRate)
	s, v := acBig(outputRate)
	if !ok || !y || !z || !v || a.Sign() < 0 || b.Sign() < 0 || r.Sign() < 0 || s.Sign() < 0 {
		return "", false
	}
	a.Mul(a, r)
	b.Mul(b, s)
	a.Add(a, b)
	a.Add(a, big.NewInt(999999))
	a.Quo(a, big.NewInt(1000000))
	return a.String(), len(a.String()) <= 100
}
func acText(s string) bool {
	if !utf8.ValidString(s) || len(s) < 1 || len(s) > 100 || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func acFinite(t Time) bool { return t.Valid && t.InfinityModifier == 0 }
func acTimeEq(a, b Time) bool {
	return a.Valid == b.Valid && a.InfinityModifier == b.InfinityModifier && (!a.Valid || a.Time.Equal(b.Time))
}
func acConfigAfter(j AccountJournalRow) acConfig {
	return acConfig{j.AfterLabel, j.AfterProvider, j.AfterState, j.AfterLimitAmount, j.AfterConfigurationRevision}
}
func acBeforeMatches(j AccountJournalRow, p acPosition) bool {
	return acPtrEq(j.BeforeLabel, p.config.label) && acPtrEq(j.BeforeProvider, p.config.provider) && acPtrEq(j.BeforeState, p.config.state) && acPtrEq(j.BeforeLimitAmount, p.config.limit) && acPtrEq(j.BeforeConfigurationRevision, p.config.revision) && acPtrEq(j.BeforeHeldAmount, p.held) && acPtrEq(j.BeforeSpentAmount, p.spent) && acPtrEq(j.BeforeBlockingCount, strconv.FormatInt(p.blocking, 10))
}
func acAfterMatches(j AccountJournalRow, p acPosition) bool {
	return acConfigAfter(j) == p.config && j.AfterHeldAmount == p.held && j.AfterSpentAmount == p.spent && j.AfterBlockingCount == strconv.FormatInt(p.blocking, 10)
}
func acBlock(p acHold) int64 {
	if p.anomaly && (p.state == "unknown" || p.state == "disputed") {
		return 1
	}
	return 0
}
func acNextRevision(before acHold, after acHold) (string, bool) {
	r, ok := acInt(before.revision)
	if !ok || r < 1 {
		return "", false
	}
	if before.held != after.held || before.spent != after.spent || before.state != after.state || before.anomaly != after.anomaly {
		if r == int64(^uint64(0)>>1) {
			return "", false
		}
		r++
	}
	return strconv.FormatInt(r, 10), true
}
func acConfigRevision(before, after acConfig) (string, bool) {
	r, ok := acInt(before.revision)
	if !ok || r < 1 {
		return "", false
	}
	if before.label != after.label || before.provider != after.provider || before.state != after.state || before.limit != after.limit {
		if r == int64(^uint64(0)>>1) {
			return "", false
		}
		r++
	}
	return strconv.FormatInt(r, 10), true
}

func checkAccountPrefix(w *replayWork, rows Rows) (accountReplayResult, error) {
	e := &acReplay{w: w, rows: rows, result: accountReplayResult{Candidates: newPropositionKeys(), Complete: true}, defects: make(keySet), badAccounts: make(keySet), badProposals: make(keySet), accountJournals: map[string][]AccountJournalRow{}, cuts: map[string][]acPosition{}, holds: map[string][]acVersion{}, actions: map[string][]acAction{}, badActionOrder: make(keySet), evaluated: map[string]acEvidence{}, memberChains: map[string][]EvidenceMemberRow{}, nodeOrd: map[string]int64{}, memberNodeOrd: map[string][]int64{}, eventOrd: map[string]int64{}, pbEvents: map[string][]PeriodicBudgetEventRow{}}
	if !e.tick() {
		return accountReplayResult{}, e.err
	}
	e.index()
	e.prepareEvidence()
	e.replayAccounts()
	e.checkCommands()
	e.checkEvidenceCuts()
	e.checkProposals()
	e.checkRecoveryCuts()
	e.checkObservationOrder()
	e.checkCurrent()
	if e.err != nil {
		return accountReplayResult{}, e.err
	}
	// Local candidates are never public proof; integration must intersect with
	// SourceClosure and the remaining domains, including the original PB result.
	for _, a := range rows.PrefixRows.AccountOrigins {
		if !e.tick() {
			break
		}
		if _, bad := e.badAccounts[tupleKey(a.AccountID)]; !bad && !e.duplicate {
			e.result.Candidates.AccountPauseOrdering.add(a.AccountID)
			e.result.Candidates.AccountAdmissionOrder.add(a.AccountID)
		}
	}
	for _, p := range rows.PrefixRows.ProposalBirths {
		if !e.tick() {
			break
		}
		_, bad := e.badProposals[tupleKey(p.ProposalID)]
		_, accountBad := e.badAccounts[tupleKey(p.AccountID)]
		if !bad && !accountBad && !e.duplicate {
			e.result.Candidates.ProposalBarrier.add(p.ProposalID)
		}
	}
	if e.err != nil {
		return accountReplayResult{}, e.err
	}
	if err := e.w.b.check(); err != nil {
		return accountReplayResult{}, err
	}
	return e.result, nil
}

func (e *acReplay) index() {
	p, o := e.rows.PrefixRows, e.rows.OriginalRows
	e.accounts = acIndex(e, o.ProcurementAccounts, func(r ProcurementAccountRow) string { return r.ID })
	e.origins = acIndex(e, p.AccountOrigins, func(r AccountOriginRow) string { return r.AccountID })
	e.heads = acIndex(e, p.AccountHeads, func(r AccountHeadRow) string { return r.AccountID })
	e.journals = acIndex(e, p.AccountJournal, func(r AccountJournalRow) string { return r.ID })
	e.commands = acIndex(e, o.ProcurementCommands, func(r ProcurementCommandRow) string { return r.OperationID })
	e.sources = acIndex(e, p.AccountCommandSources, func(r AccountCommandSourceRow) string { return r.ID })
	e.attempts = acIndex(e, p.AttemptOrigins, func(r AttemptOriginRow) string { return r.AttemptID })
	e.upstream = acIndex(e, o.UpstreamAttempts, func(r UpstreamAttemptRow) string { return r.ID })
	e.requests = acIndex(e, p.RequestOrigins, func(r RequestOriginRow) string { return r.RequestID })
	e.costs = acIndex(e, o.CostVersions, func(r CostVersionRow) string { return r.ID })
	e.bindings = acIndex(e, o.CredentialProcurementBindings, func(r CredentialProcurementBindingRow) string { return r.ID })
	e.reservations = acIndex(e, o.ProcurementReservations, func(r ProcurementReservationRow) string { return r.AttemptID })
	e.transitions = acIndex(e, p.ProcurementTransitions, func(r ProcurementTransitionRow) string { return r.ID })
	e.events = acIndex(e, o.ProcurementBudgetEvents, func(r ProcurementBudgetEventRow) string { return r.ID })
	e.eventKeys = acIndex(e, o.ProcurementBudgetEvents, func(r ProcurementBudgetEventRow) string { return r.EventKey })
	e.deliveries = acIndex(e, p.AttemptDeliverySources, func(r AttemptDeliverySourceRow) string { return r.ID })
	e.incoming = acIndex(e, p.IncomingSources, func(r IncomingSourceRow) string { return r.ID })
	e.meters = acIndex(e, p.MeterChoices, func(r MeterChoiceRow) string { return r.ID })
	e.evidence = acIndex(e, p.EvidenceChoices, func(r EvidenceChoiceRow) string { return r.ID })
	e.members = acIndex(e, p.EvidenceMembers, func(r EvidenceMemberRow) string { return r.EvidenceChoiceID })
	e.proposals = acIndex(e, o.ProcurementCostProposals, func(r ProcurementCostProposalRow) string { return r.ID })
	e.births = acIndex(e, p.ProposalBirths, func(r ProposalBirthRow) string { return r.ID })
	e.approvals = acIndex(e, p.ApprovalConsumptions, func(r ApprovalConsumptionRow) string { return r.ID })
	e.recoveries = acIndex(e, p.RecoverySources, func(r RecoverySourceRow) string { return r.ID })
	e.audit = acIndex(e, o.AuditEvents, func(r AuditEventRow) string { return r.ID })
	e.corrections = acIndex(e, o.ProviderCostCorrections, func(r ProviderCostCorrectionRow) string { return r.ID })
	e.pbAllocations = acIndex(e, o.PeriodicBudgetAllocations, func(r PeriodicBudgetAllocationRow) string { return r.ID })
	e.pbControls = acIndex(e, o.PeriodicBudgetControls, func(r PeriodicBudgetControlRow) string { return r.ID })
	e.pbWindows = acIndex(e, o.PeriodicBudgetWindows, func(r PeriodicBudgetWindowRow) string { return r.ID })
	for _, j := range p.AccountJournal {
		if !e.tick() {
			return
		}
		k := tupleKey(j.AccountID)
		e.accountJournals[k] = append(e.accountJournals[k], j)
	}
	for k, list := range e.accountJournals {
		if err := replaySort(e.w, list, func(left, right AccountJournalRow) bool {
			a, _ := acInt(left.Ordinal)
			b, _ := acInt(right.Ordinal)
			return a < b
		}); err != nil {
			e.err = err
			return
		}
		e.accountJournals[k] = list
	}
	for _, n := range p.AttemptNodes {
		if !e.tick() {
			return
		}
		ordinal, ok := acInt(n.Ordinal)
		k := tupleKey(n.RootID, n.StepNo, n.StepKind, n.SourceID, n.AttemptID)
		if _, found := e.nodeOrd[k]; found || !ok || ordinal <= 0 {
			e.defects.add("node", k)
		}
		e.nodeOrd[k] = ordinal
	}
	for _, pe := range o.PeriodicBudgetEvents {
		if !e.tick() {
			return
		}
		if pe.SourceKind == "procurement_event" {
			k := tupleKey(pe.SourceID)
			e.pbEvents[k] = append(e.pbEvents[k], pe)
		}
	}
}
func (e *acReplay) node(root, step, kind, source, attempt string) int64 {
	return e.nodeOrd[tupleKey(root, step, kind, source, attempt)]
}
func (e *acReplay) cut(account, ordinal, tail string) (acPosition, bool) {
	n, ok := acInt(ordinal)
	list := e.cuts[tupleKey(account)]
	if !ok || n <= 0 || n > int64(len(list)) {
		return acPosition{}, false
	}
	p := list[n-1]
	return p, p.ordinal == n && p.journal == tail
}
func (e *acReplay) holdAt(attempt string, cut int64) (acHold, bool) {
	list := e.holds[tupleKey(attempt)]
	i := sort.Search(len(list), func(i int) bool { return list[i].ordinal > cut })
	if i == 0 {
		return acHold{}, false
	}
	return list[i-1].hold, true
}

func (e *acReplay) recordAction(j AccountJournalRow, ordinal, node int64) {
	key := tupleKey(acPtr(j.AttemptID))
	list := e.actions[key]
	good := node > 0 && ordinal > 0 && j.AttemptID != nil
	if len(list) > 0 {
		previous := list[len(list)-1]
		good = good && previous.account == j.AccountID && previous.ordinal < ordinal && previous.node < node
		if previous.account != j.AccountID {
			e.badAccounts.add(previous.account)
		}
	}
	if !good {
		e.badActionOrder.add(acPtr(j.AttemptID))
		e.bad(&e.result.Stats.JournalRows, "attempt_action_order", j.ID, j.AccountID)
	}
	e.actions[key] = append(list, acAction{ordinal: ordinal, node: node, account: j.AccountID})
}

// The two independent serial orders must describe the same historical p cut.
// A source observes all of its earlier attempt actions and none of its later
// actions, even when other attempts/configuration occupy account ordinals.
func (e *acReplay) observes(attempt, account string, accountCut, node int64) bool {
	if !e.tick() || node <= 0 {
		return false
	}
	if _, bad := e.badActionOrder[tupleKey(attempt)]; bad {
		return false
	}
	list := e.actions[tupleKey(attempt)]
	next := sort.Search(len(list), func(i int) bool { return list[i].node >= node })
	if next == 0 || list[next-1].account != account || list[next-1].ordinal > accountCut {
		return false
	}
	return next == len(list) || list[next].account == account && list[next].node > node && accountCut < list[next].ordinal
}

func (e *acReplay) replayAccounts() {
	for _, origin := range e.rows.PrefixRows.AccountOrigins {
		if !e.tick() {
			return
		}
		key := tupleKey(origin.AccountID)
		list := e.accountJournals[key]
		actual, exists := e.accounts[key]
		position := acPosition{held: "0", spent: "0"}
		holds := map[string]acHold{}
		if !exists || origin.Version != "1" || !acFinite(origin.CreatedAt) || !acTimeEq(origin.CreatedAt, actual.CreatedAt) || !acPtrEq(actual.PPVersion, "1") || !acPtrEq(actual.PPCreatedXID, origin.CreatedXID) || !acPtrEq(actual.PPOriginJournalID, origin.JournalID) || len(list) == 0 {
			e.bad(&e.result.Stats.AccountBirthRows, "birth", origin.AccountID, origin.AccountID)
		}
		for index, j := range list {
			if !e.tick() {
				return
			}
			ordinal, ok := acInt(j.Ordinal)
			if !ok || ordinal != int64(index)+1 || j.PreviousOrdinal != strconv.Itoa(index) || !acFinite(j.CreatedAt) || index == 0 && j.PreviousID != nil || index > 0 && !acPtrEq(j.PreviousID, position.journal) {
				e.bad(&e.result.Stats.JournalRows, "journal", j.ID, j.AccountID)
			}
			if index > 0 && !acBeforeMatches(j, position) {
				e.bad(&e.result.Stats.JournalRows, "before", j.ID, j.AccountID)
			}
			before := position
			position.ordinal = int64(index) + 1
			position.journal = j.ID
			switch j.Kind {
			case "create":
				s, yes := e.sources[tupleKey(acPtr(j.CommandSourceID))]
				if index != 0 || !yes || j.ID != origin.JournalID || s.ID != origin.CommandSourceID || s.OperationID != origin.CreateOperationID || s.RootID != origin.RootID || s.Kind != "account_create" || j.SourceKind != "command" || j.SourceID != s.OperationID || j.BeforeLabel != nil || j.BeforeProvider != nil || j.BeforeState != nil || j.BeforeLimitAmount != nil || j.BeforeConfigurationRevision != nil || j.BeforeHeldAmount != nil || j.BeforeSpentAmount != nil || j.BeforeBlockingCount != nil {
					e.bad(&e.result.Stats.AccountBirthRows, "create", j.ID, j.AccountID)
				}
				position.config = acConfig{s.InputLabel, s.InputProvider, s.InputState, s.InputLimitAmount, "1"}
			case "configure", "configure_rejected":
				s, yes := e.sources[tupleKey(acPtr(j.CommandSourceID))]
				if !yes || index == 0 || s.Kind != "account_configure" || s.TargetID != j.AccountID || j.SourceID != s.OperationID || j.SourceKind != "command" {
					e.bad(&e.result.Stats.CommandRows, "configure", j.ID, j.AccountID)
				}
				if s.CommandStatus == "applied" && acPtrEq(s.InputExpectedRevision, before.config.revision) && j.Kind == "configure" {
					position.config = acConfig{s.InputLabel, s.InputProvider, s.InputState, s.InputLimitAmount, ""}
					rev, valid := acConfigRevision(before.config, position.config)
					position.config.revision = rev
					if !valid {
						e.bad(&e.result.Stats.CommandRows, "revision", j.ID, j.AccountID)
					}
				} else if !(s.CommandStatus == "rejected" && s.CommandReason == "revision_conflict" && s.InputExpectedRevision != nil && *s.InputExpectedRevision != before.config.revision && j.Kind == "configure_rejected") {
					e.bad(&e.result.Stats.CommandRows, "rejection", j.ID, j.AccountID)
				}
			case "dispatch":
				p, yes := holds[tupleKey(acPtr(j.AttemptID))]
				d, found := e.deliveries[tupleKey(acPtr(j.DeliverySourceID))]
				e.recordAction(j, position.ordinal, e.node(d.RootID, d.StepNo, d.StepKind, d.ID, d.AttemptID))
				if !yes || !found || p.state != "prepared" || d.Mode != "dispatch_permit" || d.AttemptID != acPtr(j.AttemptID) || !acPtrEq(d.DispatchJournalID, j.ID) || !acPtrEq(d.DispatchAccountID, j.AccountID) || d.BeforeDeliveryState != "prepared" || d.AfterDeliveryState != "sending" || !acTimeEq(d.PBDispatchCheckedAt, j.PBDecisionAt) || j.SourceKind != "dispatch" || j.SourceID != d.AttemptID {
					e.bad(&e.result.Stats.GateRows, "dispatch", j.ID, j.AccountID)
				}
				e.gate(j, before, "0")
				after := p
				after.state = "sending"
				after.revision, ok = acNextRevision(p, after)
				if !ok {
					e.bad(&e.result.Stats.JournalRows, "p_revision", j.ID, j.AccountID)
				}
				e.compareHold(j, p, after, false)
				after.journal = j.ID
				holds[tupleKey(d.AttemptID)] = after
				e.holds[tupleKey(d.AttemptID)] = append(e.holds[tupleKey(d.AttemptID)], acVersion{position.ordinal, after})
			default:
				t, yes := e.transitions[tupleKey(acPtr(j.TransitionID))]
				if !yes || j.SourceKind != "budget_event" || t.JournalID != j.ID || t.EventID != j.SourceID || t.AccountID != j.AccountID || t.RootID != j.RootID || t.StepNo != j.StepNo || t.CreatedXID != j.CreatedXID || !acPtrEq(j.AttemptID, t.AttemptID) {
					e.bad(&e.result.Stats.TransitionRows, "journal_transition", j.ID, j.AccountID)
					break
				}
				old, present := holds[tupleKey(t.AttemptID)]
				e.recordAction(j, position.ordinal, e.node(t.RootID, t.StepNo, t.StepKind, t.ID, t.AttemptID))
				after, valid := e.target(t, j, before, old, present)
				if !valid {
					e.bad(&e.result.Stats.TransitionRows, "target", t.ID, j.AccountID)
				}
				e.compareHold(j, old, after, !present)
				e.compareTransition(t, old, after, !present)
				dh, ds := after.held, after.spent
				if present {
					dh = acSub(after.held, old.held)
					ds = acSub(after.spent, old.spent)
				}
				position.held = acAdd(before.held, dh)
				position.spent = acAdd(before.spent, ds)
				position.blocking = before.blocking - acBlock(old) + acBlock(after)
				if t.HeldDelta != dh || t.SpentDelta != ds {
					e.bad(&e.result.Stats.TransitionRows, "delta", t.ID, j.AccountID)
				}
				if after.anomaly {
					position.config.state = "paused"
					position.config.revision, ok = acConfigRevision(before.config, position.config)
					if !ok || !acPtrEq(j.PauseEventID, t.EventID) || j.AfterState != "paused" || j.AfterConfigurationRevision != position.config.revision {
						e.bad(&e.result.Stats.PauseRows, "pause", j.ID, j.AccountID)
					}
				} else if j.PauseEventID != nil {
					e.bad(&e.result.Stats.PauseRows, "unexpected_pause", j.ID, j.AccountID)
				}
				pe, found := e.events[tupleKey(t.EventID)]
				if !found || pe.AccountID != j.AccountID || pe.AttemptID != t.AttemptID || pe.EventKey != t.EventKey || pe.Type != t.EventType || pe.EvidenceRef != t.EvidenceRef || pe.HeldDelta != dh || pe.SpentDelta != ds || !acPtrEq(pe.PPTransitionID, t.ID) || !acPtrEq(pe.PPJournalID, j.ID) {
					e.bad(&e.result.Stats.TransitionRows, "original_pe", t.ID, j.AccountID)
				}
				e.eventOrd[tupleKey(t.EventID)] = position.ordinal
				e.checkPB(t, old, after, present)
				after.journal = j.ID
				holds[tupleKey(t.AttemptID)] = after
				e.holds[tupleKey(t.AttemptID)] = append(e.holds[tupleKey(t.AttemptID)], acVersion{position.ordinal, after})
			}
			if j.AfterBlockingCount != strconv.FormatInt(position.blocking, 10) {
				e.bad(&e.result.Stats.BlockingRows, "blocking_after", j.ID, j.AccountID)
			}
			if index > 0 && !acPtrEq(j.BeforeBlockingCount, strconv.FormatInt(before.blocking, 10)) {
				e.bad(&e.result.Stats.BlockingRows, "blocking_before", j.ID, j.AccountID)
			}
			if !acAfterMatches(j, position) {
				e.bad(&e.result.Stats.JournalRows, "after", j.ID, j.AccountID)
			}
			if position.blocking < 0 {
				e.bad(&e.result.Stats.BlockingRows, "blocking", j.ID, j.AccountID)
			}
			e.cuts[key] = append(e.cuts[key], position)
		}
		head, found := e.heads[key]
		if !found || head.Ordinal != strconv.FormatInt(position.ordinal, 10) || head.JournalID != position.journal || head.Label != position.config.label || head.Provider != position.config.provider || head.State != position.config.state || head.LimitAmount != position.config.limit || head.ConfigurationRevision != position.config.revision || head.HeldAmount != position.held || head.SpentAmount != position.spent || head.BlockingCount != strconv.FormatInt(position.blocking, 10) || actual.Label != position.config.label || actual.Provider != position.config.provider || actual.State != position.config.state || actual.LimitAmount != position.config.limit || actual.Revision != position.config.revision || actual.HeldAmount != position.held || actual.SpentAmount != position.spent {
			e.bad(&e.result.Stats.CurrentProjectionRows, "account_current", origin.AccountID, origin.AccountID)
		}
	}
}
func (e *acReplay) gate(j AccountJournalRow, before acPosition, extra string) {
	if before.config.state != "active" || before.blocking != 0 || acCmp(acAdd(acAdd(before.held, before.spent), extra), before.config.limit) > 0 || !acPtrEq(j.GateExtra, extra) || !acFinite(j.GateCheckedAt) || !acFinite(j.PBDecisionAt) {
		e.bad(&e.result.Stats.GateRows, "gate", j.ID, j.AccountID)
	}
}
func (e *acReplay) compareHold(j AccountJournalRow, before, after acHold, birth bool) {
	good := acPtrEq(j.BindingID, after.binding) && acPtrEq(j.InputBound, after.input) && acPtrEq(j.OutputBound, after.output) && acPtrEq(j.UpperAmount, after.upper) && acPtrEq(j.CostVersionID, after.version) && acPtrEq(j.CostInputRate, after.inputRate) && acPtrEq(j.CostOutputRate, after.outputRate) && acPtrEq(j.CostBasis, after.basis) && acPtrEq(j.AfterReservationRevision, after.revision) && acPtrEq(j.AfterReservationState, after.state) && acPtrEq(j.AfterReservationHeldAmount, after.held) && acPtrEq(j.AfterReservationSpentAmount, after.spent) && j.AfterReservationAnomaly != nil && *j.AfterReservationAnomaly == after.anomaly
	if birth {
		good = good && j.BeforeReservationRevision == nil && j.BeforeReservationState == nil && j.BeforeReservationHeldAmount == nil && j.BeforeReservationSpentAmount == nil && j.BeforeReservationAnomaly == nil
	} else {
		good = good && acPtrEq(j.BeforeReservationRevision, before.revision) && acPtrEq(j.BeforeReservationState, before.state) && acPtrEq(j.BeforeReservationHeldAmount, before.held) && acPtrEq(j.BeforeReservationSpentAmount, before.spent) && j.BeforeReservationAnomaly != nil && *j.BeforeReservationAnomaly == before.anomaly
	}
	if !good {
		e.bad(&e.result.Stats.JournalRows, "hold", j.ID, j.AccountID)
	}
}
func (e *acReplay) compareTransition(t ProcurementTransitionRow, before, after acHold, birth bool) {
	good := t.BindingID == after.binding && t.InputBound == after.input && t.OutputBound == after.output && t.UpperAmount == after.upper && t.CostVersionID == after.version && t.CostInputRate == after.inputRate && t.CostOutputRate == after.outputRate && t.CostBasis == after.basis && t.AfterRevision == after.revision && t.AfterState == after.state && t.AfterHeld == after.held && t.AfterSpent == after.spent && t.AfterAnomaly == after.anomaly
	if birth {
		good = good && t.BeforeRevision == nil && t.BeforeState == nil && t.BeforeHeld == nil && t.BeforeSpent == nil && t.BeforeAnomaly == nil
	} else {
		good = good && acPtrEq(t.BeforeRevision, before.revision) && acPtrEq(t.BeforeState, before.state) && acPtrEq(t.BeforeHeld, before.held) && acPtrEq(t.BeforeSpent, before.spent) && t.BeforeAnomaly != nil && *t.BeforeAnomaly == before.anomaly
	}
	if !good {
		e.bad(&e.result.Stats.TransitionRows, "snapshot", t.ID, t.AccountID)
	}
}

func (e *acReplay) target(t ProcurementTransitionRow, j AccountJournalRow, before acPosition, old acHold, present bool) (acHold, bool) {
	after := old
	valid := true
	eventType, eventKey, evidence := "", "", ""
	switch t.CauseKind {
	case "attempt_birth":
		origin, ok := e.attempts[tupleKey(t.AttemptID)]
		request, found := e.requests[tupleKey(origin.RequestID)]
		binding, bound := e.bindings[tupleKey(acPtr(origin.BindingID))]
		quote, quoted := e.costs[tupleKey(acPtr(origin.CostVersionID))]
		amount, priceOK := acAmount(request.InputUpperBound, request.OutputLimit, acPtr(origin.CostInputRate), acPtr(origin.CostOutputRate))
		upper, upperOK := acInt(amount)
		valid = ok && found && bound && quoted && priceOK && upperOK && upper >= 0 && !present && origin.ManagementMode == "managed" && t.CauseSourceID == origin.AttemptID && origin.RootID == t.RootID && origin.TenantID == t.TenantID && origin.RequestID == t.RequestID && acPtrEq(origin.AccountID, t.AccountID) && acPtrEq(origin.ProcurementTransitionID, t.ID) && acPtrEq(origin.ReservedEventID, t.EventID) && acPtrEq(origin.AccountJournalID, j.ID) && binding.AccountID == t.AccountID && binding.CredentialID == origin.CredentialID && binding.ChannelID == origin.ChannelID && quote.ChannelID == origin.ChannelID && quote.UpstreamModel == origin.UpstreamModel && acSamePtr(quote.InputRate, origin.CostInputRate) && acSamePtr(quote.OutputRate, origin.CostOutputRate) && quote.Basis == origin.CostBasis && (quote.Basis == "contract" || quote.Basis == "invoice") && acFinite(origin.AdmittedAt) && acFinite(request.RequestDeadline) && origin.AdmittedAt.Time.Before(request.RequestDeadline.Time) && acTimeEq(origin.AdmittedAt, j.PBDecisionAt)
		after = acHold{account: t.AccountID, attempt: t.AttemptID, binding: binding.ID, input: request.InputUpperBound, output: request.OutputLimit, upper: amount, version: acPtr(origin.CostVersionID), inputRate: acPtr(origin.CostInputRate), outputRate: acPtr(origin.CostOutputRate), basis: origin.CostBasis, held: amount, spent: "0", state: "prepared", revision: "1"}
		eventType, eventKey = "reserved", "reserve:"+t.AttemptID
		e.gate(j, before, amount)
	case "evidence_choice":
		choice, ok := e.evidence[tupleKey(t.CauseSourceID)]
		evaluated, found := e.evaluated[tupleKey(choice.ID)]
		decision, cause, effective, existing := e.evidenceDecision(choice, old, before.ordinal)
		valid = present && ok && found && evaluated.good && choice.AttemptID == t.AttemptID && choice.AccountID == t.AccountID && choice.RootID == t.RootID && choice.AccountOrdinalCut == strconv.FormatInt(before.ordinal, 10) && choice.AccountTailCut == before.journal && acPtrEq(choice.ProcurementTransitionID, t.ID) && acPtrEq(choice.ProcurementEventID, t.EventID) && acPtrEq(choice.AccountJournalID, j.ID) && e.choiceFields(choice, old, decision, cause, effective, existing)
		evidence = evaluated.meter.MeterEventID
		switch decision {
		case "dispute":
			after.held = acSub(old.upper, old.spent)
			if acCmp(after.held, "0") < 0 {
				after.held = "0"
			}
			after.state = "disputed"
			after.anomaly = true
			eventType = "evidence_disputed"
			eventKey = "conflict:" + t.AttemptID + ":" + evidence
		case "release_no_consumption":
			after.held = "0"
			after.state = "released"
			after.anomaly = false
			eventType = "released_no_consumption"
			eventKey = "release:" + t.AttemptID
		case "mark_unknown":
			after.state = "unknown"
			after.anomaly = false
			eventType = "consumption_unknown"
			eventKey = "unknown:" + t.AttemptID
		case "confirm_cost":
			var priced bool
			after.spent, priced = acAmount(evaluated.incoming.InputTokens, evaluated.incoming.OutputTokens, old.inputRate, old.outputRate)
			valid = valid && priced
			after.held = "0"
			after.state = "settled"
			after.anomaly = acCmp(after.spent, old.upper) > 0 || acCmp(evaluated.incoming.InputTokens, old.input) > 0 || acCmp(evaluated.incoming.OutputTokens, old.output) > 0
			eventType = "cost_confirmed"
			eventKey = "settle:" + t.AttemptID
		default:
			valid = false
		}
	case "approval_consumed":
		source, ok := e.approvals[tupleKey(t.CauseSourceID)]
		proposal, found := e.proposals[tupleKey(source.ProposalID)]
		valid = present && ok && found && source.AttemptID == t.AttemptID && source.AccountID == t.AccountID && source.RootID == t.RootID && source.AccountOrdinalCut == strconv.FormatInt(before.ordinal, 10) && source.AccountTailCut == before.journal && source.ProcurementTransitionID == t.ID && source.ProcurementEventID == t.EventID && old.state != "prepared" && old.state != "sending" && source.ConsumedRevision == old.revision && source.ConsumedState == old.state && source.ConsumedHeld == old.held && source.ConsumedSpent == old.spent && source.ConsumedAnomaly == old.anomaly && (acCmp(proposal.Amount, old.spent) >= 0 || proposal.Basis == "invoice")
		after.held = "0"
		after.spent = proposal.Amount
		after.state = "settled"
		after.anomaly = acCmp(proposal.Amount, old.upper) > 0
		eventType = "cost_corrected"
		eventKey = "correction:" + source.ApprovalOperationID
		evidence = proposal.EvidenceRef
		if source.AfterState != after.state || source.AfterHeld != after.held || source.AfterSpent != after.spent || source.AfterAnomaly != after.anomaly {
			valid = false
		}
	case "recovery":
		source, ok := e.recoveries[tupleKey(t.CauseSourceID)]
		origin, found := e.attempts[tupleKey(t.AttemptID)]
		request, known := e.requests[tupleKey(origin.RequestID)]
		valid = present && ok && found && known && source.RootID == t.RootID && source.AttemptID == t.AttemptID && source.ProcurementTransitionID == t.ID && source.ProcurementEventID == t.EventID && source.AccountJournalID == j.ID && source.ProcurementBeforeRevision == old.revision && source.ProcurementBeforeState == old.state && source.ProcurementBeforeHeld == old.held && source.ProcurementBeforeSpent == old.spent && source.ProcurementBeforeAnomaly == old.anomaly && (old.state == "prepared" || old.state == "sending") && acFinite(source.CheckedAt) && acFinite(request.RequestDeadline) && acTimeEq(source.RequestDeadline, request.RequestDeadline) && source.CheckedAt.Time.After(request.RequestDeadline.Time)
		noPermit := source.PermitSourceID == nil && old.state == "prepared" && source.DeliveryBeforeState == "prepared"
		if noPermit {
			after.held = "0"
			after.state = "released"
			eventType = "expired_without_permit"
			eventKey = "release:" + t.AttemptID
			valid = valid && source.Decision == "expired_without_permit"
		} else {
			after.state = "unknown"
			eventType = "recovered_unknown"
			eventKey = "unknown:" + t.AttemptID
			valid = valid && source.Decision == "recovered_unknown"
		}
		after.anomaly = false
	default:
		valid = false
	}
	if present {
		var ok bool
		after.revision, ok = acNextRevision(old, after)
		valid = valid && ok
	}
	if t.CauseKind == "approval_consumed" {
		source := e.approvals[tupleKey(t.CauseSourceID)]
		valid = valid && source.AfterRevision == after.revision
	}
	kind := eventType
	if kind == "reserved" {
		kind = "reserve"
	}
	valid = valid && eventType != "" && t.EventType == eventType && t.EventKey == eventKey && t.EvidenceRef == evidence && j.Kind == kind
	return after, valid
}

func (e *acReplay) checkPB(t ProcurementTransitionRow, before, after acHold, present bool) {
	// Full control/window/configuration history belongs to the existing independent
	// PB checker. Here each actual procurement slot is checked against our p target.
	events := e.pbEvents[tupleKey(t.EventID)]
	seen := make(keySet)
	reservation := e.reservations[tupleKey(t.AttemptID)]
	if reservation.PBControl != nil && len(events) != 1 {
		e.bad(&e.result.Stats.TransitionRows, "pb_required", t.ID, t.AccountID)
	}
	for _, pe := range events {
		if !e.tick() {
			return
		}
		allocation, found := e.pbAllocations[tupleKey(pe.AllocationID)]
		control, yes := e.pbControls[tupleKey(pe.ControlID)]
		key := tupleKey(pe.ControlID)
		_, duplicate := seen[key]
		seen[key] = struct{}{}
		identityOK := pe.SourceKind == "procurement_event" && pe.SourceID == t.EventID && pe.CreatedXID == t.CreatedXID && reservation.AccountID == t.AccountID
		if reservation.PBControl != nil {
			identityOK = identityOK && acPtrEq(reservation.PBControl, pe.ControlID) && allocation.Origin == "new" && acFinite(reservation.PBAdmittedAt) && acTimeEq(allocation.AdmittedAt, reservation.PBAdmittedAt) && allocation.Baseline == "0"
		} else {
			identityOK = identityOK && allocation.Origin != "new"
		}
		if allocation.Origin == "new" {
			origin, exists := e.events[tupleKey(allocation.OriginSourceID)]
			identityOK = identityOK && exists && origin.AttemptID == t.AttemptID && origin.AccountID == t.AccountID && origin.Type == "reserved" && origin.EventKey == "reserve:"+t.AttemptID && allocation.WindowID != nil
		} else if allocation.Origin == "legacy_import" {
			identityOK = identityOK && allocation.OriginSourceID == t.AttemptID && allocation.WindowID == nil && !allocation.AdmittedAt.Valid
		} else if allocation.Origin == "legacy_catchup" {
			origin, exists := e.events[tupleKey(allocation.OriginSourceID)]
			identityOK = identityOK && exists && origin.AttemptID == t.AttemptID && origin.AccountID == t.AccountID && allocation.WindowID == nil && !allocation.AdmittedAt.Valid
			if allocation.OriginSourceID == t.EventID {
				identityOK = identityOK && present && before.held == "0" && allocation.Baseline == before.spent
			}
		} else {
			identityOK = false
		}
		if allocation.WindowID != nil {
			window, exists := e.pbWindows[tupleKey(*allocation.WindowID)]
			identityOK = identityOK && exists && window.ControlID == pe.ControlID && acFinite(window.StartAt) && acFinite(window.EndAt) && acFinite(allocation.AdmittedAt) && !allocation.AdmittedAt.Time.Before(window.StartAt.Time) && allocation.AdmittedAt.Time.Before(window.EndAt.Time) && pe.WindowBeforeRevision != nil
		} else {
			identityOK = identityOK && pe.WindowBeforeRevision == nil
		}
		if !identityOK {
			e.bad(&e.result.Stats.TransitionRows, "pb_identity", t.ID, t.AccountID)
		}
		base, ok := acBig(allocation.Baseline)
		spent, valid := acBig(after.spent)
		effective := "!"
		if ok && valid {
			spent.Sub(spent, base)
			if spent.Sign() < 0 {
				spent.SetInt64(0)
			}
			effective = spent.String()
		}
		state := "held"
		if after.state == "disputed" {
			state = "disputed"
		} else if after.state == "settled" {
			state = "settled"
		} else if after.state == "released" {
			state = "released"
		}
		if !found || !yes || duplicate || allocation.ControlID != pe.ControlID || allocation.SubjectType != "supplier_attempt" || allocation.SubjectID != t.AttemptID || control.Scope != "procurement" || !acPtrEq(control.AccountID, t.AccountID) || allocation.UpperAmount != after.upper || pe.AfterHeld != after.held || pe.AfterSourceSpent != after.spent || pe.AfterSpent != effective || pe.AfterState != state || reservation.PBControl != nil && !acPtrEq(reservation.PBControl, pe.ControlID) {
			e.bad(&e.result.Stats.TransitionRows, "pb_target", t.ID, t.AccountID)
		}
		oldHeld := "0"
		oldSpent := "0"
		if present {
			oldHeld = before.held
			if b, good := acBig(before.spent); good && ok {
				b.Sub(b, base)
				if b.Sign() < 0 {
					b.SetInt64(0)
				}
				oldSpent = b.String()
			}
		}
		if pe.BeforeHeld != oldHeld || pe.BeforeSpent != oldSpent || pe.HeldDelta != acSub(after.held, oldHeld) || pe.SpentDelta != acSub(effective, oldSpent) {
			e.bad(&e.result.Stats.TransitionRows, "pb_delta", t.ID, t.AccountID)
		}
	}
}

func acCommandDigest(s AccountCommandSourceRow) (string, bool) {
	type create struct {
		OperationID string `json:"operation_id"`
		Label       string `json:"label"`
		Provider    string `json:"provider"`
		State       string `json:"state"`
		Limit       string `json:"limit_amount"`
	}
	type configure struct {
		OperationID string `json:"operation_id"`
		Expected    string `json:"expected_revision"`
		Label       string `json:"label"`
		Provider    string `json:"provider"`
		State       string `json:"state"`
		Limit       string `json:"limit_amount"`
	}
	var payload any
	if s.Kind == "account_create" {
		if s.InputTargetID != "" || s.InputExpectedRevision != nil {
			return "", false
		}
		payload = create{s.OperationID, s.InputLabel, s.InputProvider, s.InputState, s.InputLimitAmount}
	} else if s.Kind == "account_configure" && s.InputExpectedRevision != nil {
		payload = configure{s.OperationID, *s.InputExpectedRevision, s.InputLabel, s.InputProvider, s.InputState, s.InputLimitAmount}
	} else {
		return "", false
	}
	encoded, err := json.Marshal(struct {
		Kind, Target string
		Payload      any
	}{s.Kind, s.InputTargetID, payload})
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), true
}
func (e *acReplay) snapshot(raw, id string, p acPosition, at Time) bool {
	data, valid := e.flatJSON(raw)
	if !valid || len(data) != 9 || !acFinite(at) {
		return false
	}
	expected := map[string]string{"id": id, "label": p.config.label, "provider": p.config.provider, "state": p.config.state, "limit_amount": p.config.limit, "held_amount": p.held, "spent_amount": p.spent, "revision": p.config.revision}
	for k, v := range expected {
		if !e.tick() {
			return false
		}
		if data[k] != v {
			return false
		}
	}
	text, yes := data["created_at"].(string)
	if !yes {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	return err == nil && parsed.Equal(at.Time)
}
func (e *acReplay) checkCommands() {
	for _, s := range e.rows.PrefixRows.AccountCommandSources {
		if !e.tick() {
			return
		}
		c, exists := e.commands[tupleKey(s.OperationID)]
		digest, valid := acCommandDigest(s)
		limit, lok := acInt(s.InputLimitAmount)
		expected, expectedOK := acInt(acPtr(s.InputExpectedRevision))
		if s.Kind == "account_configure" {
			valid = valid && expectedOK && expected > 0
		}
		valid = valid && lok && limit >= 0 && acText(s.InputLabel) && acText(s.InputProvider) && (s.InputState == "active" || s.InputState == "paused") && s.ActorRole == "admin" && s.ActorStatus == "active" && digest == s.InputDigest && exists && c.Actor == s.ActorID && c.Kind == s.Kind && c.TargetID == s.TargetID && c.InputDigest == digest && c.Status == s.CommandStatus && c.Reason == s.CommandReason && acTimeEq(c.CreatedAt, s.CommandCreatedAt)
		if s.JournalID == nil {
			valid = valid && s.Kind == "account_configure" && s.CommandStatus == "rejected" && s.CommandReason == "not_found" && s.InputTargetID == s.TargetID && s.InputExpectedRevision != nil && strings.TrimSpace(c.BeforeState) == "null" && strings.TrimSpace(c.AfterState) == "null"
		} else {
			j, found := e.journals[tupleKey(*s.JournalID)]
			origin, born := e.origins[tupleKey(s.TargetID)]
			ordinal, _ := acInt(j.Ordinal)
			after, cut := e.cut(s.TargetID, j.Ordinal, j.ID)
			valid = valid && found && born && cut && j.CommandSourceID != nil && *j.CommandSourceID == s.ID && j.SourceID == s.OperationID && j.AccountID == s.TargetID && j.RootID == s.RootID && j.StepNo == s.StepNo && j.CreatedXID == s.CreatedXID
			if s.Kind == "account_create" {
				valid = valid && s.CommandStatus == "applied" && s.CommandReason == "" && ordinal == 1 && strings.TrimSpace(c.BeforeState) == "null" && e.snapshot(c.AfterState, s.TargetID, after, origin.CreatedAt)
			} else if cut && ordinal > 1 {
				before := e.cuts[tupleKey(s.TargetID)][ordinal-2]
				valid = valid && s.InputTargetID == s.TargetID && e.snapshot(c.BeforeState, s.TargetID, before, origin.CreatedAt)
				if s.CommandStatus == "applied" {
					valid = valid && s.CommandReason == "" && e.snapshot(c.AfterState, s.TargetID, after, origin.CreatedAt)
				} else {
					valid = valid && s.CommandReason == "revision_conflict" && strings.TrimSpace(c.AfterState) == "null"
				}
			} else {
				valid = false
			}
		}
		audit, found := e.audit[tupleKey(s.AuditID)]
		valid = valid && found && audit.Actor == s.ActorID && audit.Action == "procurement.command."+s.CommandStatus && audit.Target == s.OperationID && audit.TenantScope == nil && audit.RequestID == "" && e.jsonMatches(audit.SanitizedDiff, map[string]any{"kind": s.Kind, "status": s.CommandStatus})
		if !valid {
			e.bad(&e.result.Stats.CommandRows, "command", s.OperationID, s.TargetID)
		}
	}
}
