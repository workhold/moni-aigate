package procurementprefix

// Attempt history is independent of SQL writers and mutable billing helpers.
// Complete here covers only §6.3; reconciliation/finish is a separate subdomain.
import (
	"math/big"
	"sort"
	"strconv"
)

type attemptCostHistoryResult struct {
	Stats         AttemptCost
	Candidates    propositionKeys
	Complete      bool
	Unimplemented []string
}
type costHistoryPosition struct {
	origin                                     AttemptOriginRow
	delivery, status                           string
	amount, lastMutation, lastDelivery, permit *string
	p                                          costHistoryP
	memberCount                                int64
	memberTail                                 *string
	identities                                 map[string]string
	disputes                                   keySet
	recorded                                   bool
	observedAccountCut                         int64
}
type costHistoryP struct {
	present                      bool
	revision, state, held, spent string
	anomaly                      bool
}
type costHistoryMeta struct{ attempt, tenant, request, root, step, xid, kind, id string }
type costHistoryExpectedMutation struct {
	meta       costHistoryMeta
	id, status string
	amount     *string
}
type costHistoryExpectedOutbox struct {
	meta                               costHistoryMeta
	id, operation, kind, amount, basis string
}
type costHistoryAction struct {
	node, ordinal int64
	account       string
}
type costHistoryReplay struct {
	w                                                                            *replayWork
	rows                                                                         Rows
	result                                                                       attemptCostHistoryResult
	err                                                                          error
	duplicate                                                                    bool
	badAttempts, defects, consumed, seenMutations, seenOutboxes, seenCorrections keySet
	origins                                                                      map[string]AttemptOriginRow
	actual                                                                       map[string]UpstreamAttemptRow
	heads                                                                        map[string]AttemptHeadRow
	nodes                                                                        map[string][]AttemptNodeRow
	nodeOrd                                                                      map[string]int64
	meta                                                                         map[string]costHistoryMeta
	requests                                                                     map[string]RequestOriginRow
	quotes                                                                       map[string]CostVersionRow
	reservations                                                                 map[string]ProcurementReservationRow
	deliveries                                                                   map[string]AttemptDeliverySourceRow
	incoming                                                                     map[string]IncomingSourceRow
	meters                                                                       map[string]MeterChoiceRow
	decisions                                                                    map[string]RequestDecisionRow
	finals                                                                       map[string]FinalizationRecordRow
	normal                                                                       map[string]NormalCostDecisionRow
	mutations                                                                    map[string]CostMutationRow
	resets                                                                       map[string]CostResetDecisionRow
	evidence                                                                     map[string]EvidenceChoiceRow
	members                                                                      map[string]EvidenceMemberRow
	transitions                                                                  map[string]ProcurementTransitionRow
	approvals                                                                    map[string]ApprovalConsumptionRow
	births                                                                       map[string]ProposalBirthRow
	proposals                                                                    map[string]ProcurementCostProposalRow
	corrections                                                                  map[string]UnmanagedCorrectionSourceRow
	originalCorrections                                                          map[string]ProviderCostCorrectionRow
	recoveries                                                                   map[string]RecoverySourceRow
	conflicts                                                                    map[string]ConflictSourceRow
	links                                                                        map[string]CostOutboxLinkRow
	outboxes                                                                     map[string]ProjectionOutboxRow
	audits                                                                       map[string]AuditEventRow
	journals                                                                     map[string]AccountJournalRow
	journalCuts                                                                  map[string]AccountJournalRow
	actions                                                                      map[string][]costHistoryAction
	expectedMutations                                                            map[string]costHistoryExpectedMutation
	expectedOutboxes                                                             map[string]costHistoryExpectedOutbox
	evidenceDecisions                                                            map[string]string
	transitionBefore                                                             map[string]costHistoryP
	mutationForCause                                                             map[string]string
	finalDecisions                                                               map[string][]RequestDecisionRow
	finalDeliveries                                                              map[string][]AttemptDeliverySourceRow
}

func costHistoryPtr(s string) *string { return &s }
func costHistoryText(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func costHistorySame(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func costHistoryInt(s string) (int64, bool) {
	if !signedInteger(s, 64) {
		return 0, false
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n, true
}
func costHistoryMoney(s string, max int) bool { return decimalDigits(s, false, max) }
func costHistoryPrice(input, output, ri, ro string) (string, bool) {
	if !costHistoryMoney(input, 19) || !costHistoryMoney(output, 19) || !costHistoryMoney(ri, 19) || !costHistoryMoney(ro, 19) {
		return "", false
	}
	a, ok := costHistoryInt(input)
	b, bok := costHistoryInt(output)
	r, rok := costHistoryInt(ri)
	s, sok := costHistoryInt(ro)
	if !ok || !bok || !rok || !sok || a < 0 || b < 0 || r < 0 || s < 0 {
		return "", false
	}
	x := new(big.Int).Mul(big.NewInt(a), big.NewInt(r))
	x.Add(x, new(big.Int).Mul(big.NewInt(b), big.NewInt(s)))
	x.Add(x, big.NewInt(999999))
	x.Quo(x, big.NewInt(1000000))
	return x.String(), len(x.String()) <= 40
}
func costHistoryCmp(a, b string) int {
	x, ok := numeric(a, false)
	y, yes := numeric(b, false)
	if !ok || !yes {
		return 2
	}
	return x.Cmp(y)
}
func costHistorySubtract(a, b string) string {
	x, ok := numeric(a, false)
	y, yes := numeric(b, false)
	if !ok || !yes {
		return "!"
	}
	return x.Sub(x, y).String()
}
func costHistoryFinite(t Time) bool { return finite(t) }
func costHistoryTime(a, b Time) bool {
	return a.Valid == b.Valid && a.InfinityModifier == b.InfinityModifier && (!a.Valid || a.Time.Equal(b.Time))
}
func (c *costHistoryReplay) tick() bool {
	if c.err != nil {
		return false
	}
	c.err = c.w.tick()
	return c.err == nil
}
func (c *costHistoryReplay) mark(counter *uint64, kind, id, attempt string) {
	name := "current"
	switch counter {
	case &c.result.Stats.AttemptBirthRows:
		name = "birth"
	case &c.result.Stats.QuoteBindingRows:
		name = "quote"
	case &c.result.Stats.DeliveryRows:
		name = "delivery"
	case &c.result.Stats.NormalEligibilityRows:
		name = "normal"
	case &c.result.Stats.ResetRows:
		name = "reset"
	case &c.result.Stats.MutationRows:
		name = "mutation"
	case &c.result.Stats.CorrectionRows:
		name = "correction"
	case &c.result.Stats.OutboxRows:
		name = "outbox"
	}
	key := tupleKey(name, kind, id)
	if _, seen := c.defects[key]; !seen {
		c.defects[key] = struct{}{}
		*counter++
	}
	if attempt != "" {
		c.badAttempts.add(attempt)
	}
}
func costHistoryIndex[T any](c *costHistoryReplay, rows []T, key func(T) string) map[string]T {
	out := make(map[string]T, len(rows))
	for _, r := range rows {
		if !c.tick() {
			break
		}
		k := tupleKey(key(r))
		if _, dup := out[k]; dup {
			c.duplicate = true
		}
		out[k] = r
	}
	return out
}
func checkAttemptCostHistory(w *replayWork, rows Rows) (attemptCostHistoryResult, error) {
	c := &costHistoryReplay{w: w, rows: rows, result: attemptCostHistoryResult{Complete: true, Candidates: newPropositionKeys()}, badAttempts: make(keySet), defects: make(keySet), consumed: make(keySet), seenMutations: make(keySet), seenOutboxes: make(keySet), seenCorrections: make(keySet), nodes: map[string][]AttemptNodeRow{}, nodeOrd: map[string]int64{}, meta: map[string]costHistoryMeta{}, journalCuts: map[string]AccountJournalRow{}, actions: map[string][]costHistoryAction{}, expectedMutations: map[string]costHistoryExpectedMutation{}, expectedOutboxes: map[string]costHistoryExpectedOutbox{}, evidenceDecisions: map[string]string{}, transitionBefore: map[string]costHistoryP{}, mutationForCause: map[string]string{}, finalDecisions: map[string][]RequestDecisionRow{}, finalDeliveries: map[string][]AttemptDeliverySourceRow{}}
	if !c.tick() {
		return attemptCostHistoryResult{}, c.err
	}
	c.index()
	c.replay()
	c.reverse()
	if c.err != nil {
		return attemptCostHistoryResult{}, c.err
	}
	for _, o := range rows.PrefixRows.AttemptOrigins {
		if !c.tick() {
			return attemptCostHistoryResult{}, c.err
		}
		if _, bad := c.badAttempts[tupleKey(o.AttemptID)]; !bad && !c.duplicate {
			c.result.Candidates.CostRecordedEligibility.add(o.AttemptID)
			c.result.Candidates.CurrentCostOrder.add(o.AttemptID)
			c.result.Candidates.ProcurementEventOrder.add(o.AttemptID)
		}
	}
	if err := w.b.check(); err != nil {
		return attemptCostHistoryResult{}, err
	}
	return c.result, nil
}
func (c *costHistoryReplay) addMeta(m costHistoryMeta) {
	k := tupleKey(m.kind, m.id)
	if _, dup := c.meta[k]; dup {
		c.duplicate = true
	}
	c.meta[k] = m
}
func (c *costHistoryReplay) index() {
	p, o := c.rows.PrefixRows, c.rows.OriginalRows
	c.origins = costHistoryIndex(c, p.AttemptOrigins, func(r AttemptOriginRow) string { return r.AttemptID })
	c.actual = costHistoryIndex(c, o.UpstreamAttempts, func(r UpstreamAttemptRow) string { return r.ID })
	c.heads = costHistoryIndex(c, p.AttemptHeads, func(r AttemptHeadRow) string { return r.AttemptID })
	c.requests = costHistoryIndex(c, p.RequestOrigins, func(r RequestOriginRow) string { return r.RequestID })
	c.quotes = costHistoryIndex(c, o.CostVersions, func(r CostVersionRow) string { return r.ID })
	c.reservations = costHistoryIndex(c, o.ProcurementReservations, func(r ProcurementReservationRow) string { return r.AttemptID })
	c.deliveries = costHistoryIndex(c, p.AttemptDeliverySources, func(r AttemptDeliverySourceRow) string { return r.ID })
	c.incoming = costHistoryIndex(c, p.IncomingSources, func(r IncomingSourceRow) string { return r.ID })
	c.meters = costHistoryIndex(c, p.MeterChoices, func(r MeterChoiceRow) string { return r.ID })
	c.decisions = costHistoryIndex(c, p.RequestDecisions, func(r RequestDecisionRow) string { return r.ID })
	c.finals = costHistoryIndex(c, o.FinalizationRecords, func(r FinalizationRecordRow) string { return r.ID })
	c.normal = costHistoryIndex(c, p.NormalCostDecisions, func(r NormalCostDecisionRow) string { return r.ID })
	c.mutations = costHistoryIndex(c, p.CostMutations, func(r CostMutationRow) string { return r.ID })
	c.resets = costHistoryIndex(c, p.CostResetDecisions, func(r CostResetDecisionRow) string { return r.ID })
	c.evidence = costHistoryIndex(c, p.EvidenceChoices, func(r EvidenceChoiceRow) string { return r.ID })
	c.members = costHistoryIndex(c, p.EvidenceMembers, func(r EvidenceMemberRow) string { return r.EvidenceChoiceID })
	c.transitions = costHistoryIndex(c, p.ProcurementTransitions, func(r ProcurementTransitionRow) string { return r.ID })
	c.births = costHistoryIndex(c, p.ProposalBirths, func(r ProposalBirthRow) string { return r.ID })
	c.approvals = costHistoryIndex(c, p.ApprovalConsumptions, func(r ApprovalConsumptionRow) string { return r.ID })
	c.proposals = costHistoryIndex(c, o.ProcurementCostProposals, func(r ProcurementCostProposalRow) string { return r.ID })
	c.corrections = costHistoryIndex(c, p.UnmanagedCorrectionSources, func(r UnmanagedCorrectionSourceRow) string { return r.ID })
	c.originalCorrections = costHistoryIndex(c, o.ProviderCostCorrections, func(r ProviderCostCorrectionRow) string { return r.ID })
	c.recoveries = costHistoryIndex(c, p.RecoverySources, func(r RecoverySourceRow) string { return r.ID })
	c.conflicts = costHistoryIndex(c, p.ConflictSources, func(r ConflictSourceRow) string { return r.ConflictID })
	c.links = costHistoryIndex(c, p.CostOutboxLinks, func(r CostOutboxLinkRow) string { return r.ID })
	c.outboxes = costHistoryIndex(c, o.ProjectionOutbox, func(r ProjectionOutboxRow) string { return r.EventID })
	c.audits = costHistoryIndex(c, o.AuditEvents, func(r AuditEventRow) string { return r.ID })
	c.journals = costHistoryIndex(c, p.AccountJournal, func(r AccountJournalRow) string { return r.ID })
	for _, d := range p.RequestDecisions {
		if !c.tick() {
			return
		}
		if d.FinalizationID != nil {
			key := tupleKey(*d.FinalizationID)
			c.finalDecisions[key] = append(c.finalDecisions[key], d)
		}
	}
	for _, d := range p.AttemptDeliverySources {
		if !c.tick() {
			return
		}
		if d.Mode == "finalization" && d.CauseSourceID != nil {
			key := tupleKey(*d.CauseSourceID)
			c.finalDeliveries[key] = append(c.finalDeliveries[key], d)
		}
	}
	for _, n := range p.AttemptNodes {
		if !c.tick() {
			return
		}
		k := tupleKey(n.AttemptID)
		c.nodes[k] = append(c.nodes[k], n)
		source := tupleKey(n.StepKind, n.SourceID)
		v, ok := costHistoryInt(n.Ordinal)
		if _, dup := c.nodeOrd[source]; dup {
			c.duplicate = true
		}
		if !ok {
			v = 0
		}
		c.nodeOrd[source] = v
	}
	for key, list := range c.nodes {
		if err := replaySort(c.w, list, func(a, b AttemptNodeRow) bool {
			x, _ := costHistoryInt(a.Ordinal)
			y, _ := costHistoryInt(b.Ordinal)
			return x < y
		}); err != nil {
			c.err = err
			return
		}
		c.nodes[key] = list
	}
	for _, j := range p.AccountJournal {
		if !c.tick() {
			return
		}
		k := tupleKey(j.AccountID, j.Ordinal)
		if _, dup := c.journalCuts[k]; dup {
			c.duplicate = true
		}
		c.journalCuts[k] = j
		if j.AttemptID == nil {
			continue
		}
		kind, id := "procurement_transition", costHistoryText(j.TransitionID)
		if j.Kind == "dispatch" {
			kind, id = "attempt_delivery", costHistoryText(j.DeliverySourceID)
		}
		ordinal, ok := costHistoryInt(j.Ordinal)
		if !ok {
			ordinal = 0
		}
		key := tupleKey(*j.AttemptID)
		c.actions[key] = append(c.actions[key], costHistoryAction{c.nodeOrd[tupleKey(kind, id)], ordinal, j.AccountID})
	}
	for key, list := range c.actions {
		if err := replaySort(c.w, list, func(a, b costHistoryAction) bool { return a.node < b.node }); err != nil {
			c.err = err
			return
		}
		c.actions[key] = list
		for i, a := range list {
			if !c.tick() {
				return
			}
			if a.node <= 0 || a.ordinal <= 0 || i > 0 && (a.node <= list[i-1].node || a.ordinal <= list[i-1].ordinal || a.account != list[i-1].account) {
				attempt := c.origins[key].AttemptID
				c.mark(&c.result.Stats.CurrentCostRows, "attempt", attempt, attempt)
			}
		}
	}
	// Explicit typed metadata, never a reflection-based choice of business union.
	for _, r := range p.AttemptDeliverySources {
		if !c.tick() {
			return
		}
		attempt := r.AttemptID
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "attempt_delivery", r.ID})
		}
	}
	for _, r := range p.IncomingSources {
		if !c.tick() {
			return
		}
		attempt := costHistoryText(r.AttemptID)
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "incoming", r.ID})
		}
	}
	for _, r := range p.MeterChoices {
		if !c.tick() {
			return
		}
		attempt := costHistoryText(r.AttemptID)
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "meter_choice", r.ID})
		}
	}
	for _, r := range p.RequestDecisions {
		if !c.tick() {
			return
		}
		attempt := costHistoryText(c.incoming[tupleKey(r.IncomingSourceID)].AttemptID)
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "request_decision", r.ID})
		}
	}
	for _, r := range p.NormalCostDecisions {
		if !c.tick() {
			return
		}
		attempt := r.AttemptID
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "normal_cost_decision", r.ID})
		}
	}
	for _, r := range p.CostMutations {
		if !c.tick() {
			return
		}
		attempt := r.AttemptID
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "cost_mutation", r.ID})
		}
	}
	for _, r := range p.CostResetDecisions {
		if !c.tick() {
			return
		}
		attempt := r.AttemptID
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "cost_reset_decision", r.ID})
		}
	}
	for _, r := range p.EvidenceChoices {
		if !c.tick() {
			return
		}
		attempt := r.AttemptID
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "evidence_choice", r.ID})
		}
	}
	for _, r := range p.ProcurementTransitions {
		if !c.tick() {
			return
		}
		attempt := r.AttemptID
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "procurement_transition", r.ID})
		}
	}
	for _, r := range p.ProposalBirths {
		if !c.tick() {
			return
		}
		attempt := r.AttemptID
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "proposal_birth", r.ID})
		}
	}
	for _, r := range p.ApprovalConsumptions {
		if !c.tick() {
			return
		}
		attempt := r.AttemptID
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "approval_consumed", r.ID})
		}
	}
	for _, r := range p.UnmanagedCorrectionSources {
		if !c.tick() {
			return
		}
		attempt := r.AttemptID
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "unmanaged_correction", r.ID})
		}
	}
	for _, r := range p.RecoverySources {
		if !c.tick() {
			return
		}
		attempt := r.AttemptID
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "recovery", r.ID})
		}
	}
	for _, r := range p.CostOutboxLinks {
		if !c.tick() {
			return
		}
		attempt := r.AttemptID
		if attempt != "" {
			c.addMeta(costHistoryMeta{attempt, r.TenantID, r.RequestID, r.RootID, r.StepNo, r.CreatedXID, "cost_outbox", r.ID})
		}
	}
}
func (c *costHistoryReplay) replay() {
	for _, o := range c.rows.PrefixRows.AttemptOrigins {
		if !c.tick() {
			return
		}
		key := tupleKey(o.AttemptID)
		a, found := c.actual[key]
		request, requested := c.requests[tupleKey(o.RequestID)]
		born := found && requested && request.TenantID == o.TenantID && o.InitialDeliveryState == "prepared" && o.InitialCostStatus == "unknown" && o.InitialCostAmount == nil && o.Sequence == a.Sequence && (o.Sequence == "1" || o.Sequence == "2") && o.TenantID == a.TenantID && o.RequestID == a.RequestID && o.ChannelID == a.ChannelID && o.ChannelVersionID == a.ChannelVersionID && o.CredentialID == a.CredentialID && o.UpstreamModel == a.UpstreamModel && len(o.UpstreamModel) > 0 && len(o.UpstreamModel) <= 200 && costHistoryFinite(o.AttemptCreatedAt) && costHistoryTime(o.AttemptCreatedAt, a.CreatedAt) && costHistorySame(a.PPOriginRootID, costHistoryPtr(o.RootID)) && costHistorySame(a.PPOriginRootStep, costHistoryPtr(o.StepNo)) && costHistorySame(a.PPOriginXID, costHistoryPtr(o.CreatedXID)) && costHistorySame(a.PBCreatedXID, costHistoryPtr(o.PBCreatedXID)) && c.routes(o.RouteDecisions, o.ChannelID, o.ChannelVersionID) && c.equalRoutes(o.RouteDecisions, a.RouteDecisions)
		if !born {
			c.mark(&c.result.Stats.AttemptBirthRows, "attempt", o.AttemptID, o.AttemptID)
		}
		quote := costHistoryFinite(o.QuoteSelectedAt) && costHistoryTime(o.QuoteSelectedAt, a.PPQuoteSelectedAt) && costHistorySame(o.CostVersionID, a.CostVersionID) && costHistorySame(o.CostInputRate, a.CostInputRate) && costHistorySame(o.CostOutputRate, a.CostOutputRate) && o.CostBasis == a.CostBasis
		if o.CostVersionID == nil {
			quote = quote && o.CostInputRate == nil && o.CostOutputRate == nil && o.CostBasis == "unknown"
		} else {
			q, ok := c.quotes[tupleKey(*o.CostVersionID)]
			quote = quote && ok && q.ChannelID == o.ChannelID && q.UpstreamModel == o.UpstreamModel && q.Basis == o.CostBasis && costHistorySame(q.InputRate, o.CostInputRate) && costHistorySame(q.OutputRate, o.CostOutputRate) && costHistoryFinite(q.EffectiveAt) && !q.EffectiveAt.Time.After(o.QuoteSelectedAt.Time)
		}
		managed := o.ManagementMode == "managed"
		if managed {
			quote = quote && o.CostVersionID != nil && o.CostInputRate != nil && o.CostOutputRate != nil && (o.CostBasis == "contract" || o.CostBasis == "invoice")
		} else if o.ManagementMode != "unmanaged" {
			quote = false
		}
		if !quote {
			c.mark(&c.result.Stats.QuoteBindingRows, "attempt", o.AttemptID, o.AttemptID)
		}
		p, hasReservation := c.reservations[key]
		if managed {
			upper, priced := costHistoryPrice(request.InputUpperBound, request.OutputLimit, costHistoryText(o.CostInputRate), costHistoryText(o.CostOutputRate))
			_, bounded := costHistoryInt(upper)
			if !hasReservation || !priced || !bounded || p.AccountID != costHistoryText(o.AccountID) || p.BindingID != costHistoryText(o.BindingID) || p.InputBound != request.InputUpperBound || p.OutputBound != request.OutputLimit || p.UpperAmount != upper || !costHistorySame(p.PPCreatedXID, costHistoryPtr(o.CreatedXID)) || !costHistorySame(p.PBCreatedXID, costHistoryPtr(o.PBCreatedXID)) || !costHistorySame(p.PPOriginJournalID, o.AccountJournalID) || !costHistoryFinite(o.AdmittedAt) || !costHistoryTime(p.PBAdmittedAt, o.AdmittedAt) {
				c.mark(&c.result.Stats.AttemptBirthRows, "attempt", o.AttemptID, o.AttemptID)
			}
		} else if hasReservation || o.AccountID != nil || o.BindingID != nil || o.ProcurementTransitionID != nil || o.ReservedEventID != nil || o.AccountJournalID != nil || o.AdmittedAt.Valid {
			c.mark(&c.result.Stats.AttemptBirthRows, "attempt", o.AttemptID, o.AttemptID)
		}
		state := costHistoryPosition{origin: o, delivery: "prepared", status: "unknown", identities: map[string]string{}, disputes: make(keySet)}
		list := c.nodes[key]
		var prev *string
		for i, n := range list {
			if !c.tick() {
				return
			}
			m, exists := c.meta[tupleKey(n.StepKind, n.SourceID)]
			ordinal, valid := costHistoryInt(n.Ordinal)
			if !exists || m.attempt != o.AttemptID || m.tenant != o.TenantID || m.request != o.RequestID || m.root != n.RootID || m.step != n.StepNo || m.xid != n.CreatedXID || !valid || ordinal != int64(i+1) || n.PreviousOrdinal != strconv.Itoa(i) || !costHistorySame(n.PreviousNodeID, prev) {
				c.mark(&c.result.Stats.CurrentCostRows, "node", n.ID, o.AttemptID)
			}
			c.consumed.add(n.StepKind, n.SourceID)
			prev = costHistoryPtr(n.ID)
			switch n.StepKind {
			case "attempt_delivery":
				c.delivery(&state, n)
			case "normal_cost_decision":
				c.normalDecision(&state, n)
			case "cost_reset_decision":
				c.reset(&state, n)
			case "cost_mutation":
				c.mutate(&state, n)
			case "evidence_choice":
				c.evidenceChoice(&state, n)
			case "procurement_transition":
				c.procurement(&state, n)
			case "approval_consumed":
				c.approval(&state, n)
			case "unmanaged_correction":
				c.correction(&state, n)
			case "recovery":
				c.recovery(&state, n)
			case "cost_outbox":
				c.outbox(&state, n)
			case "proposal_birth":
				// Proposal's financial/member barrier is certified by AccountPrefix;
				// this domain also enforces its temporal cut against other cost observations.
				if source, ok := c.births[tupleKey(n.SourceID)]; !ok || !c.observe(&state, n, source.AccountID, source.AccountOrdinalCut, source.AccountTailCut) {
					c.mark(&c.result.Stats.CurrentCostRows, "attempt", o.AttemptID, o.AttemptID)
				}
			case "incoming", "meter_choice", "request_decision":
			default:
				c.mark(&c.result.Stats.CurrentCostRows, "node", n.ID, o.AttemptID)
			}
		}
		head, headed := c.heads[key]
		if !headed || head.Ordinal != strconv.Itoa(len(list)) || !costHistorySame(head.LastNodeID, prev) {
			c.mark(&c.result.Stats.CurrentCostRows, "head", o.AttemptID, o.AttemptID)
		}
		if managed && !state.p.present || !managed && state.p.present {
			c.mark(&c.result.Stats.AttemptBirthRows, "attempt", o.AttemptID, o.AttemptID)
		}
		if managed && !costHistoryPMatches(p.Revision, p.State, p.HeldAmount, p.SpentAmount, p.Anomaly, state.p) {
			c.mark(&c.result.Stats.CurrentCostRows, "attempt", o.AttemptID, o.AttemptID)
		}
		if a.CostStatus != state.status || !costHistorySame(a.CostAmount, state.amount) || !costHistorySame(a.PPLastCostMutationID, state.lastMutation) {
			c.mark(&c.result.Stats.CurrentCostRows, "attempt", o.AttemptID, o.AttemptID)
		}
		if a.DeliveryState != state.delivery || !costHistorySame(a.PPLastDeliverySourceID, state.lastDelivery) || !costHistorySame(a.PPDispatchSourceID, state.permit) {
			c.mark(&c.result.Stats.DeliveryRows, "current_attempt", o.AttemptID, o.AttemptID)
		}
		if state.permit == nil && (a.PPDispatchRootID != nil || a.PPDispatchRootStep != nil || a.PPDispatchXID != nil || a.PPDispatchAccountID != nil || a.PPDispatchJournalID != nil || a.PBDispatchCheckedAt.Valid) {
			c.mark(&c.result.Stats.DeliveryRows, "current_attempt", o.AttemptID, o.AttemptID)
		}
	}
}
func (c *costHistoryReplay) observe(s *costHistoryPosition, n AttemptNodeRow, account, ordinal, tail string) bool {
	cut, ok := costHistoryInt(ordinal)
	j, present := c.journalCuts[tupleKey(account, ordinal)]
	node, _ := costHistoryInt(n.Ordinal)
	list := c.actions[tupleKey(s.origin.AttemptID)]
	i := sort.Search(len(list), func(i int) bool { return list[i].node >= node })
	good := ok && present && j.ID == tail && account == costHistoryText(s.origin.AccountID) && cut >= s.observedAccountCut && i > 0 && list[i-1].account == account && list[i-1].ordinal <= cut && (i == len(list) || list[i].account == account && cut < list[i].ordinal && list[i].node > node)
	if good {
		s.observedAccountCut = cut
	}
	return good
}
func (c *costHistoryReplay) metaBefore(n AttemptNodeRow, kind, id string) bool {
	m, ok := c.meta[tupleKey(kind, id)]
	step, a := costHistoryInt(m.step)
	current, b := costHistoryInt(n.StepNo)
	return ok && a && b && m.root == n.RootID && m.attempt == n.AttemptID && m.xid == n.CreatedXID && step < current && c.nodeOrd[tupleKey(kind, id)] < c.nodeOrd[tupleKey(n.StepKind, n.SourceID)]
}
func (c *costHistoryReplay) expectMutation(m costHistoryMeta, id, status string, amount *string) {
	if id == "" {
		c.mark(&c.result.Stats.MutationRows, "cause_obligation", tupleKey(m.kind, m.id), m.attempt)
		return
	}
	key := tupleKey(id)
	if _, dup := c.expectedMutations[key]; dup {
		c.mark(&c.result.Stats.MutationRows, "mutation", id, m.attempt)
	}
	c.expectedMutations[key] = costHistoryExpectedMutation{m, id, status, amount}
	c.mutationForCause[tupleKey(m.kind, m.id)] = id
}
func (c *costHistoryReplay) expectOutbox(m costHistoryMeta, id, operation, kind, amount, basis string) {
	if id == "" {
		c.mark(&c.result.Stats.OutboxRows, "cause_obligation", tupleKey(m.kind, m.id), m.attempt)
		return
	}
	key := tupleKey(id)
	if _, dup := c.expectedOutboxes[key]; dup {
		c.mark(&c.result.Stats.OutboxRows, "outbox", id, m.attempt)
	}
	c.expectedOutboxes[key] = costHistoryExpectedOutbox{m, id, operation, kind, amount, basis}
}
func (c *costHistoryReplay) mutate(s *costHistoryPosition, n AttemptNodeRow) {
	m, found := c.mutations[tupleKey(n.SourceID)]
	if !found {
		c.mark(&c.result.Stats.MutationRows, "mutation", n.SourceID, s.origin.AttemptID)
		return
	}
	expected, planned := c.expectedMutations[tupleKey(m.ID)]
	good := found && planned && m.CauseKind == expected.meta.kind && m.CauseSourceID == expected.meta.id && m.RootID == expected.meta.root && m.BeforeStatus == s.status && costHistorySame(m.BeforeAmount, s.amount) && m.AfterStatus == expected.status && costHistorySame(m.AfterAmount, expected.amount)
	causeKind := m.CauseKind
	if causeKind == "procurement_dispute_reset" || causeKind == "incoming_conflict_reset" {
		causeKind = "cost_reset_decision"
	}
	good = good && c.metaBefore(n, causeKind, m.CauseSourceID)
	if !good {
		c.mark(&c.result.Stats.MutationRows, "mutation", m.ID, s.origin.AttemptID)
	}
	s.status, s.amount, s.lastMutation = expected.status, expected.amount, costHistoryPtr(m.ID)
	if !planned {
		s.status, s.amount = "unknown", nil
	}
	c.seenMutations.add(m.ID)
}
