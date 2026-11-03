package procurementprefix

import (
	"reflect"
	"sort"
	"strconv"
)

// These checks join independently ordered histories. They do not compute an
// evidence digest, price, financial decision, or source qualification.
type sourceContinuityP struct {
	values  [4]*string
	anomaly *bool
}

var sourceContinuityTransitionBefore = [5]string{"before_revision", "before_state", "before_held", "before_spent", "before_anomaly"}
var sourceContinuityTransitionAfter = [5]string{"after_revision", "after_state", "after_held", "after_spent", "after_anomaly"}
var sourceContinuityJournalBefore = [5]string{"before_reservation_revision", "before_reservation_state", "before_reservation_held_amount", "before_reservation_spent_amount", "before_reservation_anomaly"}
var sourceContinuityJournalAfter = [5]string{"after_reservation_revision", "after_reservation_state", "after_reservation_held_amount", "after_reservation_spent_amount", "after_reservation_anomaly"}

func sourceContinuityReadP(r reflect.Value, fields [5]string) sourceContinuityP {
	var p sourceContinuityP
	for i, field := range fields[:4] {
		p.values[i] = sourceOptionalPointer(r, field)
	}
	v := sourceField(r, fields[4])
	if v.IsValid() {
		if v.Kind() == reflect.Pointer {
			if !v.IsNil() {
				p.anomaly = v.Interface().(*bool)
			}
		} else {
			value := v.Bool()
			p.anomaly = &value
		}
	}
	return p
}

func (p sourceContinuityP) same(q sourceContinuityP) bool {
	for i := range p.values {
		if !sourceSameOptional(p.values[i], q.values[i]) {
			return false
		}
	}
	return p.anomaly == nil && q.anomaly == nil || p.anomaly != nil && q.anomaly != nil && *p.anomaly == *q.anomaly
}

func (p sourceContinuityP) present() bool {
	return p.values[0] != nil && p.values[1] != nil && p.values[2] != nil && p.values[3] != nil && p.anomaly != nil
}

func sourceContinuityText(r reflect.Value, field string) string {
	value := sourceOptionalPointer(r, field)
	if value == nil {
		return ""
	}
	return *value
}

func sourceContinuityNodeMatches(n AttemptNodeRow, source sourceCanonicalRow) bool {
	r := source.raw.value
	if n.SourceID != source.id || !sourceCommon(reflect.ValueOf(n), r) {
		return false
	}
	// RequestDecision belongs to an attempt through its root and incoming; it
	// intentionally has no attempt_id column. Nullable incoming/meter IDs do.
	return !sourceField(r, "attempt_id").IsValid() || sourceContinuityText(r, "attempt_id") == n.AttemptID
}

type sourceContinuityAction struct {
	raw           sourceRawRow
	account       string
	ordinal, node int64
}

type sourceContinuity struct {
	s           *sourceClosureState
	index       map[string]map[string][]sourceRawRow
	sources     map[string][]sourceCanonicalRow
	nodes       map[string][]sourceRawRow
	attempts    map[string][]sourceRawRow
	accounts    map[string][]sourceRawRow
	members     map[string][]sourceRawRow
	actions     map[string][]sourceContinuityAction
	badActions  keySet
	memberBirth map[string][]sourceRawRow
}

type sourceContinuityPosition struct {
	origin                  AttemptOriginRow
	delivery, cost          string
	amount, permit          *string
	p                       sourceContinuityP
	pKnown, deliveryKnown   bool
	costKnown, membersKnown bool
	memberCount             int
	memberTail              *string
	identities              map[string]EvidenceMemberRow
	usedMembers             keySet
	lastAccountCut          int64
	transitionBefore        map[string]sourceContinuityP
}

func (s *sourceClosureState) continuityRows() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	c := &sourceContinuity{s: s, index: make(map[string]map[string][]sourceRawRow), sources: make(map[string][]sourceCanonicalRow), nodes: make(map[string][]sourceRawRow), actions: make(map[string][]sourceContinuityAction), badActions: make(keySet), memberBirth: make(map[string][]sourceRawRow)}
	if err := c.prepare(); err != nil {
		return err
	}
	for _, raw := range s.tables["pp_attempt_origins"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		o := raw.value.Interface().(AttemptOriginRow)
		key := tupleKey(o.AttemptID)
		if len(c.index["pp_attempt_origins"][key]) != 1 {
			s.mark("attempt_chain", "pp_attempt_origins", raw.key)
			continue
		}
		sequence, ordered := c.attempts[key]
		if !ordered && len(c.index["pp_attempt_nodes_by_attempt"][key]) != 0 {
			continue // A rejected nonempty order never becomes an empty history.
		}
		_, memberOrder := c.members[key]
		p := sourceContinuityPosition{origin: o, delivery: o.InitialDeliveryState, cost: o.InitialCostStatus, amount: o.InitialCostAmount, pKnown: true, deliveryKnown: true, costKnown: true, membersKnown: memberOrder || len(c.index["pp_evidence_members_by_attempt"][key]) == 0, identities: make(map[string]EvidenceMemberRow), usedMembers: make(keySet), transitionBefore: make(map[string]sourceContinuityP)}
		for _, node := range sequence {
			if err := s.w.tick(); err != nil {
				return err
			}
			if err := c.node(&p, node); err != nil {
				return err
			}
		}
		if o.ManagementMode == "managed" && (!p.pKnown || !p.p.present()) || o.ManagementMode == "unmanaged" && p.p.present() {
			s.mark("attempt_chain", "pp_attempt_origins", raw.key)
		}
		if p.membersKnown {
			for _, member := range c.members[key] {
				if err := s.w.tick(); err != nil {
					return err
				}
				if _, consumed := p.usedMembers[member.key]; !consumed {
					s.mark("evidence_chain", "pp_evidence_members", member.key)
				}
			}
		}
	}
	return s.w.b.check()
}

func (c *sourceContinuity) prepare() error {
	var err error
	c.attempts, err = c.s.sourceChain("pp_attempt_nodes", "attempt_chain", "attempt_id", "ordinal", "previous_node_id")
	if err != nil {
		return err
	}
	c.accounts, err = c.s.sourceChain("pp_account_journal", "account_chain", "account_id", "ordinal", "previous_id")
	if err != nil {
		return err
	}
	c.members, err = c.s.sourceChain("pp_evidence_members", "evidence_chain", "attempt_id", "member_ordinal", "previous_member_id")
	if err != nil {
		return err
	}
	for _, rule := range []struct{ table, field, name string }{
		{"pp_attempt_origins", "attempt_id", "pp_attempt_origins"},
		{"pp_attempt_nodes", "attempt_id", "pp_attempt_nodes_by_attempt"},
		{"pp_evidence_members", "attempt_id", "pp_evidence_members_by_attempt"},
		{"pp_account_journal", "id", "pp_account_journal"},
	} {
		c.index[rule.name], err = c.s.pointerIndex(rule.table, rule.field)
		if err != nil {
			return err
		}
	}
	for table, descriptor := range sourceCanonicalTables {
		for _, raw := range c.s.tables[table] {
			if err := c.s.w.tick(); err != nil {
				return err
			}
			id := sourceText(raw.value, descriptor.id)
			key := tupleKey(descriptor.kind, id)
			c.sources[key] = append(c.sources[key], sourceCanonicalRow{table: table, raw: raw, kind: descriptor.kind, id: id})
		}
	}
	for _, raw := range c.s.tables["pp_attempt_nodes"] {
		if err := c.s.w.tick(); err != nil {
			return err
		}
		n := raw.value.Interface().(AttemptNodeRow)
		key := tupleKey(n.AttemptID, n.StepKind, n.SourceID)
		c.nodes[key] = append(c.nodes[key], raw)
	}
	for _, sources := range c.sources {
		for _, source := range sources {
			if err := c.s.w.tick(); err != nil {
				return err
			}
			attempt := sourceOptionalPointer(source.raw.value, "attempt_id")
			if attempt == nil || source.kind == "attempt_birth" {
				continue // Includes all no-attempt Reconcile children.
			}
			ns := c.nodes[tupleKey(*attempt, source.kind, source.id)]
			if len(sources) != 1 || len(ns) != 1 || len(c.index["pp_attempt_origins"][tupleKey(*attempt)]) != 1 || !sourceContinuityNodeMatches(ns[0].value.Interface().(AttemptNodeRow), source) {
				c.s.mark("attempt_chain", source.table, source.raw.key)
			}
		}
	}
	for _, raw := range c.s.tables["pp_evidence_members"] {
		if err := c.s.w.tick(); err != nil {
			return err
		}
		key := tupleKey(sourceText(raw.value, "evidence_choice_id"))
		c.memberBirth[key] = append(c.memberBirth[key], raw)
	}
	return c.accountActions()
}

func (c *sourceContinuity) journalSource(j AccountJournalRow) (sourceCanonicalRow, AttemptNodeRow, bool) {
	kind, id := "", ""
	if j.SourceKind == "budget_event" && j.TransitionID != nil {
		kind, id = "procurement_transition", *j.TransitionID
	} else if j.SourceKind == "dispatch" && j.DeliverySourceID != nil {
		kind, id = "attempt_delivery", *j.DeliverySourceID
	}
	if j.AttemptID == nil || kind == "" {
		return sourceCanonicalRow{}, AttemptNodeRow{}, false
	}
	ss, ns := c.sources[tupleKey(kind, id)], c.nodes[tupleKey(*j.AttemptID, kind, id)]
	if len(ss) != 1 || len(ns) != 1 {
		return sourceCanonicalRow{}, AttemptNodeRow{}, false
	}
	source, n := ss[0], ns[0].value.Interface().(AttemptNodeRow)
	valid := sourceContinuityNodeMatches(n, source) && sourceCommon(reflect.ValueOf(j), source.raw.value) && sourceScope(reflect.ValueOf(j), source.raw.value)
	if kind == "procurement_transition" {
		t := source.raw.value.Interface().(ProcurementTransitionRow)
		valid = valid && t.JournalID == j.ID && t.AccountID == j.AccountID && t.EventID == j.SourceID
	} else {
		d := source.raw.value.Interface().(AttemptDeliverySourceRow)
		valid = valid && d.Mode == "dispatch_permit" && sourceSameOptional(d.DispatchJournalID, &j.ID) && sourceSameOptional(d.DispatchAccountID, &j.AccountID) && j.SourceID == d.AttemptID
	}
	return source, n, valid
}

func (c *sourceContinuity) accountActions() error {
	for _, raw := range c.s.tables["pp_account_journal"] {
		if err := c.s.w.tick(); err != nil {
			return err
		}
		j := raw.value.Interface().(AccountJournalRow)
		if j.AttemptID != nil {
			if _, ordered := c.accounts[tupleKey(j.AccountID)]; !ordered {
				c.s.mark("account_chain", "pp_account_journal", raw.key)
				c.badActions.add(*j.AttemptID)
			}
		}
	}
	for _, sequence := range c.accounts {
		last := make(map[string]sourceContinuityP)
		for i, raw := range sequence {
			if err := c.s.w.tick(); err != nil {
				return err
			}
			j := raw.value.Interface().(AccountJournalRow)
			if j.AttemptID == nil {
				continue
			}
			key := tupleKey(*j.AttemptID)
			os := c.index["pp_attempt_origins"][key]
			_, n, valid := c.journalSource(j)
			node, parseErr := strconv.ParseInt(n.Ordinal, 10, 64)
			valid = valid && parseErr == nil && node > 0 && len(os) == 1
			if len(os) == 1 {
				o := os[0].value.Interface().(AttemptOriginRow)
				valid = valid && o.ManagementMode == "managed" && sourceSameOptional(o.AccountID, &j.AccountID) && sourceSameOptional(o.BindingID, j.BindingID)
			}
			// Only the origin's unique account/source bridge can enter this
			// attempt's action order. An unrelated account must not seed its
			// predecessor: account maps have no cross-account ordering.
			if !valid {
				c.s.mark("account_chain", "pp_account_journal", raw.key)
				c.badActions.add(*j.AttemptID)
				continue
			}
			orderedBridge := valid
			before := sourceContinuityReadP(raw.value, sourceContinuityJournalBefore)
			previous, exists := last[key]
			if !exists {
				valid = valid && j.Kind == "reserve" && before.same(sourceContinuityP{})
			} else {
				valid = valid && before.same(previous)
			}
			list := c.actions[key]
			if len(list) > 0 {
				prior := list[len(list)-1]
				monotone := prior.account == j.AccountID && prior.node < node
				valid, orderedBridge = valid && monotone, orderedBridge && monotone
			}
			after := sourceContinuityReadP(raw.value, sourceContinuityJournalAfter)
			valid = valid && after.present()
			if !valid {
				c.s.mark("account_chain", "pp_account_journal", raw.key)
			}
			if !orderedBridge {
				c.badActions.add(*j.AttemptID)
			}
			c.actions[key] = append(list, sourceContinuityAction{raw: raw, account: j.AccountID, ordinal: int64(i + 1), node: node})
			last[key] = after
		}
	}
	return nil
}

func (c *sourceContinuity) observe(p *sourceContinuityPosition, row sourceCanonicalRow, account, ordinal, tail string, node int64) {
	bad := func() { c.s.mark("attempt_chain", row.table, row.raw.key) }
	cut, err := strconv.ParseInt(ordinal, 10, 64)
	sequence, ordered := c.accounts[tupleKey(account)]
	if err != nil || !signedInteger(ordinal, 64) || cut < 1 || cut > int64(len(sequence)) || !ordered || sourceText(sequence[cut-1].value, "id") != tail || !sourceSameOptional(p.origin.AccountID, &account) {
		bad()
		return
	}
	if _, invalid := c.badActions[tupleKey(p.origin.AttemptID)]; invalid {
		bad()
		return
	}
	list := c.actions[tupleKey(p.origin.AttemptID)]
	next := sort.Search(len(list), func(i int) bool { return list[i].node >= node })
	if cut < p.lastAccountCut || next == 0 || list[next-1].account != account || list[next-1].ordinal > cut || next < len(list) && (list[next].account != account || list[next].node <= node || cut >= list[next].ordinal) {
		bad()
		return
	}
	p.lastAccountCut = cut
}

func (c *sourceContinuity) memberCut(p *sourceContinuityPosition, row sourceCanonicalRow, count *string, tail *string) {
	if !p.membersKnown {
		return // Its nonempty broken member chain is already an error, never zero.
	}
	if p.origin.ManagementMode != "managed" || count == nil || *count != strconv.Itoa(p.memberCount) || !sourceSameOptional(tail, p.memberTail) {
		c.s.mark("evidence_chain", row.table, row.raw.key)
	}
}

func (c *sourceContinuity) snapshotP(p *sourceContinuityPosition, row sourceCanonicalRow, fields [5]string) {
	if !p.pKnown || p.origin.ManagementMode != "managed" || !p.p.present() || !sourceContinuityReadP(row.raw.value, fields).same(p.p) {
		c.s.mark("attempt_chain", row.table, row.raw.key)
	}
}

func (c *sourceContinuity) node(p *sourceContinuityPosition, raw sourceRawRow) error {
	n := raw.value.Interface().(AttemptNodeRow)
	ss := c.sources[tupleKey(n.StepKind, n.SourceID)]
	if len(ss) != 1 || len(c.nodes[tupleKey(n.AttemptID, n.StepKind, n.SourceID)]) != 1 || !sourceContinuityNodeMatches(n, ss[0]) || !sourceScope(ss[0].raw.value, reflect.ValueOf(p.origin)) {
		c.s.mark("attempt_chain", "pp_attempt_nodes", raw.key)
		switch n.StepKind {
		case "attempt_delivery":
			p.deliveryKnown, p.pKnown = false, false
		case "cost_mutation":
			p.costKnown = false
		case "procurement_transition":
			p.pKnown = false
		case "evidence_choice":
			p.membersKnown = false
		}
		return nil
	}
	r := ss[0]
	bad := func() { c.s.mark("attempt_chain", r.table, r.raw.key) }
	value := r.raw.value
	ordinal, _ := strconv.ParseInt(n.Ordinal, 10, 64)
	switch n.StepKind {
	case "attempt_delivery":
		d := value.Interface().(AttemptDeliverySourceRow)
		if !p.deliveryKnown || d.BeforeDeliveryState != p.delivery {
			bad()
		}
		p.delivery, p.deliveryKnown = d.AfterDeliveryState, true
		if d.Mode == "dispatch_permit" {
			if p.permit != nil {
				bad()
			}
			p.permit = &d.ID
			if p.origin.ManagementMode == "managed" {
				c.advanceP(p, r, n, d.DispatchJournalID, nil)
			}
		}
	case "cost_mutation":
		if !p.costKnown || sourceText(value, "before_status") != p.cost || !sourceSameOptional(sourceOptional(value, "before_amount"), p.amount) {
			bad()
		}
		p.cost, p.amount, p.costKnown = sourceText(value, "after_status"), sourceOptional(value, "after_amount"), true
	case "normal_cost_decision", "approval_consumed", "unmanaged_correction":
		if !p.costKnown || sourceText(value, "before_cost_status") != p.cost || !sourceSameOptional(sourceOptional(value, "before_cost_amount"), p.amount) {
			bad()
		}
	}
	if n.StepKind == "procurement_transition" {
		t := value.Interface().(ProcurementTransitionRow)
		before := sourceContinuityReadP(value, sourceContinuityTransitionBefore)
		p.transitionBefore[tupleKey(t.ID)] = before
		birth := t.CauseKind == "attempt_birth"
		if !p.pKnown || p.origin.ManagementMode != "managed" || !before.same(p.p) || birth && (ordinal != 1 || p.p.present() || t.RootID != p.origin.RootID || t.CreatedXID != p.origin.CreatedXID || !sourceSameOptional(p.origin.ProcurementTransitionID, &t.ID)) || !birth && !p.p.present() {
			bad()
		}
		c.advanceP(p, r, n, &t.JournalID, &before)
		p.p = sourceContinuityReadP(value, sourceContinuityTransitionAfter)
		// Continue comparisons against the declared after, without qualifying
		// this row or hiding any missing/bad companion already recorded above.
		p.pKnown = p.p.present()
	}
	switch n.StepKind {
	case "normal_cost_decision":
		if p.origin.ManagementMode == "managed" {
			c.snapshotP(p, r, [5]string{"before_procurement_revision", "before_procurement_state", "before_procurement_held", "before_procurement_spent", "before_procurement_anomaly"})
			c.memberCut(p, r, sourceOptional(value, "member_count_before"), sourceOptional(value, "member_tail_before"))
			c.observe(p, r, sourceContinuityText(value, "account_id"), sourceContinuityText(value, "account_ordinal_cut"), sourceContinuityText(value, "account_tail_cut"), ordinal)
		}
	case "evidence_choice":
		c.snapshotP(p, r, [5]string{"procurement_before_revision", "procurement_before_state", "procurement_before_held", "procurement_before_spent", "procurement_before_anomaly"})
		c.observe(p, r, sourceText(value, "account_id"), sourceText(value, "account_ordinal_cut"), sourceText(value, "account_tail_cut"), ordinal)
		return c.evidence(p, r)
	case "proposal_birth":
		c.snapshotP(p, r, [5]string{"reservation_revision", "reservation_state", "reservation_held", "reservation_spent", "reservation_anomaly"})
		c.memberCut(p, r, sourceOptionalPointer(value, "member_count"), sourceOptional(value, "member_tail"))
		c.observe(p, r, sourceText(value, "account_id"), sourceText(value, "account_ordinal_cut"), sourceText(value, "account_tail_cut"), ordinal)
	case "approval_consumed":
		c.snapshotP(p, r, [5]string{"consumed_revision", "consumed_state", "consumed_held", "consumed_spent", "consumed_anomaly"})
		c.memberCut(p, r, sourceOptionalPointer(value, "consumed_member_count"), sourceOptional(value, "consumed_member_tail"))
		c.observe(p, r, sourceText(value, "account_id"), sourceText(value, "account_ordinal_cut"), sourceText(value, "account_tail_cut"), ordinal)
	case "recovery":
		recovery := value.Interface().(RecoverySourceRow)
		c.snapshotP(p, r, [5]string{"procurement_before_revision", "procurement_before_state", "procurement_before_held", "procurement_before_spent", "procurement_before_anomaly"})
		if !p.deliveryKnown || sourceText(value, "delivery_before_state") != p.delivery || !sourceSameOptional(sourceOptional(value, "permit_source_id"), p.permit) {
			bad()
		}
		js := c.index["pp_account_journal"][tupleKey(sourceText(value, "account_journal_id"))]
		if len(js) != 1 {
			bad()
		} else {
			j := js[0].value.Interface().(AccountJournalRow)
			effect, effectNode, bridge := c.journalSource(j)
			if !bridge || effect.kind != "procurement_transition" {
				bad()
			} else {
				t := effect.raw.value.Interface().(ProcurementTransitionRow)
				if t.ID != recovery.ProcurementTransitionID || t.CauseKind != "recovery" || t.CauseSourceID != recovery.ID || t.EventID != recovery.ProcurementEventID || !c.before(effectNode, "recovery", recovery.ID) {
					bad()
				}
			}
			cut, err := strconv.ParseInt(j.Ordinal, 10, 64)
			if err != nil || cut <= 1 || j.PreviousID == nil {
				bad()
			} else {
				// Recovery has no caller-supplied account cut: its exact effect
				// journal makes the preceding account ordinal observable.
				c.observe(p, r, j.AccountID, strconv.FormatInt(cut-1, 10), *j.PreviousID, ordinal)
			}
		}
	case "cost_reset_decision":
		var expected *string
		if sourceText(value, "cause_kind") == "procurement_dispute" {
			prior, exists := p.transitionBefore[tupleKey(sourceText(value, "cause_source_id"))]
			if !exists || !c.before(n, "procurement_transition", sourceText(value, "cause_source_id")) {
				bad()
			} else {
				expected = prior.values[1]
			}
		} else if p.origin.ManagementMode == "managed" {
			if !p.pKnown || !p.p.present() {
				bad()
			}
			expected = p.p.values[1]
		}
		if !sourceSameOptional(sourceOptional(value, "procurement_state_before"), expected) {
			bad()
		}
	}
	return nil
}

func (c *sourceContinuity) before(current AttemptNodeRow, kind, id string) bool {
	prior := c.nodes[tupleKey(current.AttemptID, kind, id)]
	if len(prior) != 1 {
		return false
	}
	n := prior[0].value.Interface().(AttemptNodeRow)
	ordinal, a := strconv.ParseInt(n.Ordinal, 10, 64)
	currentOrdinal, b := strconv.ParseInt(current.Ordinal, 10, 64)
	return a == nil && b == nil && ordinal < currentOrdinal && n.RootID == current.RootID && n.CreatedXID == current.CreatedXID && finishStepBefore(n.StepNo, current.StepNo)
}

func (c *sourceContinuity) advanceP(p *sourceContinuityPosition, row sourceCanonicalRow, n AttemptNodeRow, journalID *string, transitionBefore *sourceContinuityP) {
	js := c.index["pp_account_journal"][tupleKey(requestOptional(journalID))]
	valid := journalID != nil && len(js) == 1
	if valid {
		j := js[0].value.Interface().(AccountJournalRow)
		_, jn, bridge := c.journalSource(j)
		before := sourceContinuityReadP(js[0].value, sourceContinuityJournalBefore)
		after := sourceContinuityReadP(js[0].value, sourceContinuityJournalAfter)
		valid = bridge && jn.ID == n.ID && p.pKnown && before.same(p.p) && after.present()
		if transitionBefore != nil {
			valid = valid && before.same(*transitionBefore) && after.same(sourceContinuityReadP(row.raw.value, sourceContinuityTransitionAfter))
		}
		if !valid {
			c.s.mark("account_chain", "pp_account_journal", js[0].key)
		}
		p.p, p.pKnown = after, after.present() && bridge && jn.ID == n.ID
	}
	if !valid {
		c.s.mark("attempt_chain", row.table, row.raw.key)
		if len(js) != 1 {
			p.pKnown = false
		}
	}
}

func (c *sourceContinuity) evidence(p *sourceContinuityPosition, row sourceCanonicalRow) error {
	if !p.membersKnown {
		return nil
	}
	e := row.raw.value.Interface().(EvidenceChoiceRow)
	c.memberCut(p, row, &e.MemberCountBefore, e.MemberTailBefore)
	bad := func() { c.s.mark("evidence_chain", row.table, row.raw.key) }
	previous, exists := p.identities[tupleKey(e.IdentityDigest)]
	births := c.memberBirth[tupleKey(e.ID)]
	if e.Disposition == "existing_member" {
		if !exists || len(births) != 0 || previous.IdentityEventID != e.IdentityEventID || e.IdentityAttemptID != p.origin.AttemptID || e.EvidenceDigest != e.IdentityDigest {
			bad()
		}
	} else if e.Disposition == "new_member" {
		sequence := c.members[tupleKey(p.origin.AttemptID)]
		if exists || len(births) != 1 || p.memberCount >= len(sequence) {
			bad()
			return nil
		}
		raw := births[0]
		m := raw.value.Interface().(EvidenceMemberRow)
		if raw.key != sequence[p.memberCount].key || m.AttemptID != p.origin.AttemptID || m.MemberOrdinal != strconv.Itoa(p.memberCount+1) || m.PreviousMemberOrdinal != strconv.Itoa(p.memberCount) || !sourceSameOptional(m.PreviousMemberID, p.memberTail) || m.IdentityDigest != e.IdentityDigest || m.IdentityEventID != e.IdentityEventID || e.IdentityAttemptID != m.AttemptID || e.EvidenceDigest != m.IdentityDigest || m.SourceID != e.ID || !sourceCommon(raw.value, row.raw.value) {
			bad()
			c.s.mark("evidence_chain", "pp_evidence_members", raw.key)
		}
		p.memberCount++
		p.memberTail = &m.ID
		p.identities[tupleKey(m.IdentityDigest)] = m
		p.usedMembers[raw.key] = struct{}{}
	} else {
		bad()
	}
	c.memberCut(p, row, &e.MemberCountAfter, e.MemberTailAfter)
	return c.s.w.tick()
}
