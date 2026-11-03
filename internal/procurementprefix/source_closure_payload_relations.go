package procurementprefix

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"reflect"
	"time"
)

// Payload relations consume raw sources and originals, never another domain's
// expected values or proof keys. Passing this leaf does not qualify a source.
type sourcePayloadRef struct {
	table string
	raw   sourceRawRow
}

type sourcePayloadSlot struct {
	owner                 sourcePayloadRef
	table, id, business   string
	present, valid        bool
	actor, action, target string
	tenant                *string
	request, operation    string
	kind                  string
	pointer               *[6]string
	value                 sourcePayloadValue
}

type sourcePayloadRelations struct {
	s        *sourceClosureState
	err      error
	index    map[string]map[string][]sourceRawRow
	commands map[string][]sourceRawRow
	links    map[string][]sourceRawRow
	journals map[string][]sourceRawRow
	audits   map[string][]sourceRawRow
	outboxes map[string][]sourceRawRow
	slots    []sourcePayloadSlot
}

func (s *sourceClosureState) payloadRelationRows() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	p := &sourcePayloadRelations{s: s, index: make(map[string]map[string][]sourceRawRow), commands: make(map[string][]sourceRawRow), links: make(map[string][]sourceRawRow), journals: make(map[string][]sourceRawRow), audits: make(map[string][]sourceRawRow), outboxes: make(map[string][]sourceRawRow)}
	if !p.tick() {
		return p.err
	}
	p.prepare()
	p.trackedCommands()
	p.originalCommands()
	p.bindingObservationReverse()
	p.sourceAudits()
	p.finishOutputs()
	p.costLinks()
	p.checkSlots()
	if p.err != nil {
		return p.err
	}
	return s.w.b.check()
}

func (p *sourcePayloadRelations) tick() bool {
	if p.err == nil {
		p.err = p.s.w.tick()
	}
	return p.err == nil
}

func (p *sourcePayloadRelations) mark(ref sourcePayloadRef) {
	if ref.table != "" && p.tick() {
		p.s.mark("pointer", ref.table, ref.raw.key)
	}
}

func (p *sourcePayloadRelations) bad(owner sourcePayloadRef, table string, rows []sourceRawRow) {
	p.mark(owner)
	for _, row := range rows {
		if p.err != nil {
			return
		}
		p.mark(sourcePayloadRef{table, row})
	}
}

func (p *sourcePayloadRelations) one(table, key string) (sourceRawRow, bool) {
	rows := p.index[table][tupleKey(key)]
	if len(rows) != 1 || !p.tick() {
		return sourceRawRow{}, false
	}
	return rows[0], true
}

func (p *sourcePayloadRelations) prepare() {
	for table, rows := range p.s.tables {
		p.index[table] = make(map[string][]sourceRawRow)
		for _, raw := range rows {
			if !p.tick() {
				return
			}
			p.index[table][raw.key] = append(p.index[table][raw.key], raw)
			switch table {
			case "pp_account_command_sources":
				key := tupleKey(sourceText(raw.value, "operation_id"))
				p.commands[key] = append(p.commands[key], raw)
			case "pp_account_journal":
				if sourceText(raw.value, "source_kind") == "command" {
					for _, key := range []string{tupleKey("source", sourceContinuityText(raw.value, "command_source_id")), tupleKey("root", sourceText(raw.value, "root_id")), tupleKey("operation", sourceText(raw.value, "source_id"))} {
						p.journals[key] = append(p.journals[key], raw)
					}
				}
			case "pp_cost_outbox_links":
				key := tupleKey(sourceText(raw.value, "cause_kind"), sourceText(raw.value, "cause_source_id"))
				p.links[key] = append(p.links[key], raw)
			case "audit_events":
				a := raw.value.Interface().(AuditEventRow)
				key := sourcePayloadAuditBusiness(a.Action, a.Target, a.TenantScope, a.RequestID)
				p.audits[key] = append(p.audits[key], raw)
			case "projection_outbox":
				o := raw.value.Interface().(ProjectionOutboxRow)
				key := tupleKey(o.OperationID, o.Kind)
				p.outboxes[key] = append(p.outboxes[key], raw)
			}
		}
	}
}

func sourcePayloadOptionalKey(v *string) string {
	if v == nil {
		return tupleKey("null")
	}
	return tupleKey("text", *v)
}

func sourcePayloadAuditBusiness(action, target string, tenant *string, request string) string {
	if sourceOneOf(action, "procurement.command.applied", "procurement.command.rejected") {
		return tupleKey("command", target)
	}
	if action == "billing.profile_blocked" {
		return tupleKey(action, sourcePayloadOptionalKey(tenant), request, target)
	}
	return tupleKey(action, target)
}

func sourcePayloadStringValue(v string) sourcePayloadValue {
	return sourcePayloadValue{kind: 's', text: v}
}

func sourcePayloadMap(fields map[string]string) sourcePayloadValue {
	v := sourcePayloadValue{kind: '{', object: make(map[string]sourcePayloadValue, len(fields))}
	for name, value := range fields {
		v.object[name] = sourcePayloadStringValue(value)
	}
	return v
}

func sourcePayloadRelationPointer(raw sourceRawRow, kind, id, role string) *[6]string {
	return &[6]string{sourceText(raw.value, "root_id"), sourceText(raw.value, "step_no"), kind, id, role, sourceText(raw.value, "created_xid")}
}

func sourcePayloadKind(table string) string {
	return map[string]string{"pp_account_command_sources": "account_command", "pp_proposal_births": "proposal_birth", "pp_approval_consumptions": "approval_consumed", "pp_unmanaged_correction_sources": "unmanaged_correction", "pp_reconciliation_sources": "reconciliation", "pp_customer_finish_outcomes": "reconciliation", "pp_cost_outbox_links": "cost_outbox"}[table]
}

func sourcePayloadStepBefore(a, b string) bool {
	if !unsignedInteger(a, 63, true) || !unsignedInteger(b, 63, true) {
		return false
	}
	x, _ := new(big.Int).SetString(a, 10)
	y, _ := new(big.Int).SetString(b, 10)
	return x.Cmp(y) < 0
}

func sourcePayloadPointerEqual(r reflect.Value, expected *[6]string) bool {
	for i, name := range [6]string{"pp_root_id", "pp_root_step", "pp_source_kind", "pp_source_id", "pp_source_role", "pp_created_xid"} {
		v := sourceOptional(r, name)
		if expected == nil {
			if v != nil {
				return false
			}
		} else if v == nil || *v != expected[i] {
			return false
		}
	}
	return true
}

func (p *sourcePayloadRelations) parse(raw string) (sourcePayloadValue, bool) {
	if !p.tick() {
		return sourcePayloadValue{}, false
	}
	v, ok, err := sourceParsePayload(p.s.w, raw)
	if err != nil {
		p.err = err
	}
	return v, ok && err == nil
}

func (p *sourcePayloadRelations) equal(a, b sourcePayloadValue) bool {
	if !p.tick() || a.kind != b.kind || a.text != b.text || len(a.object) != len(b.object) {
		return false
	}
	for key, value := range b.object {
		got, found := a.object[key]
		if !found || !p.equal(got, value) {
			return false
		}
	}
	return true
}

func (p *sourcePayloadRelations) sameTime(v sourcePayloadValue, name string, at Time) bool {
	if !p.tick() || !finite(at) {
		return false
	}
	text, ok := sourcePayloadString(v, name)
	t, err := time.Parse(time.RFC3339Nano, text)
	return ok && err == nil && sourcePayloadTimestamp(text) && t.Equal(at.Time)
}

func (p *sourcePayloadRelations) auditSlot(owner sourcePayloadRef, id string, actor string, tenant *string, action, target, request, role, sourceID string, value sourcePayloadValue, valid bool) {
	kind := sourcePayloadKind(owner.table)
	p.slots = append(p.slots, sourcePayloadSlot{owner: owner, table: "audit_events", id: id, present: true, business: sourcePayloadAuditBusiness(action, target, tenant, request), actor: actor, tenant: tenant, action: action, target: target, request: request, pointer: sourcePayloadRelationPointer(owner.raw, kind, sourceID, role), value: value, valid: valid && len(p.index[owner.table][owner.raw.key]) == 1 && sourceText(owner.raw.value, "step_kind") == kind})
}

func (p *sourcePayloadRelations) outboxSlot(owner sourcePayloadRef, id *string, operation, kind, tenant, sourceID, role string, value sourcePayloadValue, valid bool) {
	sourceKind := sourcePayloadKind(owner.table)
	slot := sourcePayloadSlot{owner: owner, table: "projection_outbox", operation: operation, kind: kind, tenant: &tenant, business: tupleKey(operation, kind), pointer: sourcePayloadRelationPointer(owner.raw, sourceKind, sourceID, role), value: value, valid: valid && len(p.index[owner.table][owner.raw.key]) == 1 && sourceText(owner.raw.value, "step_kind") == sourceKind, present: id != nil}
	if id != nil {
		slot.id = *id
	}
	p.slots = append(p.slots, slot)
}

func (p *sourcePayloadRelations) trackedCommands() {
	for _, raw := range p.s.tables["pp_account_command_sources"] {
		if !p.tick() {
			return
		}
		s := raw.value.Interface().(AccountCommandSourceRow)
		owner := sourcePayloadRef{"pp_account_command_sources", raw}
		slotIndex := len(p.slots)
		p.auditSlot(owner, s.AuditID, s.ActorID, nil, "procurement.command."+s.CommandStatus, s.OperationID, "", "account_command", s.ID, sourcePayloadMap(map[string]string{"kind": s.Kind, "status": s.CommandStatus}), len(p.commands[tupleKey(s.OperationID)]) == 1)
		originals := p.index["procurement_commands"][tupleKey(s.OperationID)]
		valid := len(originals) == 1 && len(p.commands[tupleKey(s.OperationID)]) == 1 && len(p.index[owner.table][raw.key]) == 1
		if valid {
			c := originals[0].value.Interface().(ProcurementCommandRow)
			digest, ok := p.commandDigest(s)
			valid = ok && digest == s.InputDigest && digest == c.InputDigest && c.Actor == s.ActorID && c.Kind == s.Kind && c.TargetID == s.TargetID && c.Status == s.CommandStatus && c.Reason == s.CommandReason && sourceFrozenEqual(reflect.ValueOf(c.CreatedAt), reflect.ValueOf(s.CommandCreatedAt)) && finite(c.CreatedAt) && sourceSameOptional(c.PPSourceID, &s.ID) && sourceSameOptional(c.PPRootID, &s.RootID) && sourceSameOptional(c.PPRootStep, &s.StepNo) && sourceSameOptional(c.PPCreatedXID, &s.CreatedXID) && sourceSameOptional(c.PPAccountJournalID, s.JournalID)
			valid = p.trackedSnapshots(s, c) && valid
		}
		if !valid {
			p.bad(owner, "procurement_commands", originals)
		}
		p.slots[slotIndex].valid = p.slots[slotIndex].valid && valid
	}
}

func (p *sourcePayloadRelations) commandDigest(s AccountCommandSourceRow) (string, bool) {
	type create struct {
		Operation string `json:"operation_id"`
		Label     string `json:"label"`
		Provider  string `json:"provider"`
		State     string `json:"state"`
		Limit     string `json:"limit_amount"`
	}
	type configure struct {
		Operation string `json:"operation_id"`
		Expected  string `json:"expected_revision"`
		Label     string `json:"label"`
		Provider  string `json:"provider"`
		State     string `json:"state"`
		Limit     string `json:"limit_amount"`
	}
	var payload any
	if s.Kind == "account_create" && s.InputTargetID == "" && s.InputExpectedRevision == nil {
		payload = create{s.OperationID, s.InputLabel, s.InputProvider, s.InputState, s.InputLimitAmount}
	} else if s.Kind == "account_configure" && s.InputExpectedRevision != nil && s.InputTargetID == s.TargetID {
		payload = configure{s.OperationID, *s.InputExpectedRevision, s.InputLabel, s.InputProvider, s.InputState, s.InputLimitAmount}
	} else {
		return "", false
	}
	if !p.tick() {
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

func (p *sourcePayloadRelations) snapshots(c ProcurementCommandRow) (sourcePayloadValue, sourcePayloadValue, bool) {
	b, bok := p.parse(c.BeforeState)
	a, aok := p.parse(c.AfterState)
	return b, a, bok && aok && sourcePayloadSnapshot(c.Kind, b) && sourcePayloadSnapshot(c.Kind, a) && sourcePayloadCommandShape(c.Kind, c.Status, c.Reason, b, a)
}

func (p *sourcePayloadRelations) accountSnapshot(v sourcePayloadValue, journal sourceRawRow, prefix, id string, at Time) bool {
	if !sourcePayloadSnapshot("account_create", v) || v.kind != '{' {
		return false
	}
	fields := map[string]string{"id": id}
	for _, pair := range [][2]string{{"label", "label"}, {"provider", "provider"}, {"state", "state"}, {"limit_amount", "limit_amount"}, {"held_amount", "held_amount"}, {"spent_amount", "spent_amount"}, {"revision", "configuration_revision"}} {
		if !p.tick() {
			return false
		}
		value := sourceOptionalPointer(journal.value, prefix+pair[1])
		if value == nil {
			return false
		}
		fields[pair[0]] = *value
	}
	for name, value := range fields {
		if !p.tick() || v.object[name].kind != 's' || v.object[name].text != value {
			return false
		}
	}
	return p.sameTime(v, "created_at", at)
}

func (p *sourcePayloadRelations) trackedSnapshots(s AccountCommandSourceRow, c ProcurementCommandRow) bool {
	b, a, valid := p.snapshots(c)
	if !valid {
		return false
	}
	if s.JournalID == nil {
		return s.Kind == "account_configure" && s.CommandStatus == "rejected" && s.CommandReason == "not_found" && s.InputTargetID == s.TargetID && s.InputExpectedRevision != nil && b.kind == '0' && a.kind == '0' && len(p.journals[tupleKey("source", s.ID)]) == 0 && len(p.journals[tupleKey("root", s.RootID)]) == 0
	}
	jraw, jok := p.one("pp_account_journal", *s.JournalID)
	oraw, ook := p.one("pp_account_origins", s.TargetID)
	araw, aok := p.one("procurement_accounts", s.TargetID)
	if !jok || !ook || !aok {
		return false
	}
	j, o, account := jraw.value.Interface().(AccountJournalRow), oraw.value.Interface().(AccountOriginRow), araw.value.Interface().(ProcurementAccountRow)
	if j.AccountID != s.TargetID || j.SourceKind != "command" || j.SourceID != s.OperationID || !sourceSameOptional(j.CommandSourceID, &s.ID) || !sourceCommon(jraw.value, reflect.ValueOf(s)) || !sourceFrozenEqual(reflect.ValueOf(o.CreatedAt), reflect.ValueOf(account.CreatedAt)) || !finite(o.CreatedAt) {
		return false
	}
	if s.Kind == "account_create" {
		valid = j.Kind == "create" && j.Ordinal == "1" && s.CommandStatus == "applied" && s.CommandReason == "" && b.kind == '0' && p.accountSnapshot(a, jraw, "after_", s.TargetID, o.CreatedAt)
	} else if s.Kind == "account_configure" && s.InputExpectedRevision != nil {
		valid = p.accountSnapshot(b, jraw, "before_", s.TargetID, o.CreatedAt)
		if s.CommandStatus == "applied" {
			valid = valid && s.CommandReason == "" && j.Kind == "configure" && sourceSameOptional(j.BeforeConfigurationRevision, s.InputExpectedRevision) && p.accountSnapshot(a, jraw, "after_", s.TargetID, o.CreatedAt)
		} else {
			valid = valid && s.CommandStatus == "rejected" && s.CommandReason == "revision_conflict" && j.Kind == "configure_rejected" && j.BeforeConfigurationRevision != nil && *j.BeforeConfigurationRevision != *s.InputExpectedRevision && a.kind == '0'
			for _, name := range []string{"label", "provider", "state", "limit_amount", "configuration_revision", "held_amount", "spent_amount", "blocking_count"} {
				if !p.tick() || !sourceSameOptional(sourceOptionalPointer(jraw.value, "before_"+name), sourceOptionalPointer(jraw.value, "after_"+name)) {
					valid = false
				}
			}
		}
	} else {
		return false
	}
	if s.CommandStatus == "applied" {
		valid = valid && j.AfterLabel == s.InputLabel && j.AfterProvider == s.InputProvider && j.AfterState == s.InputState && j.AfterLimitAmount == s.InputLimitAmount
	}
	return valid
}

func (p *sourcePayloadRelations) originalCommands() {
	for _, raw := range p.s.tables["procurement_commands"] {
		if !p.tick() {
			return
		}
		c := raw.value.Interface().(ProcurementCommandRow)
		owner := sourcePayloadRef{"procurement_commands", raw}
		if len(p.commands[tupleKey(c.OperationID)]) != 0 {
			continue // The source direction checked every claimant and original.
		}
		tracked := c.PPSourceID != nil || c.PPRootID != nil || c.PPRootStep != nil || c.PPCreatedXID != nil || c.PPAccountJournalID != nil || len(p.journals[tupleKey("operation", c.OperationID)]) != 0
		b, a, valid := p.snapshots(c)
		valid = valid && !tracked
		switch c.Kind {
		case "account_create", "account_configure":
			valid = p.legacyAccount(c, b, a) && valid
		case "credential_bind":
			if c.Status == "applied" || c.Reason == "already_bound" {
				v := a
				if c.Status != "applied" {
					v = b
				}
				id := v.object["id"].text
				rows := p.index["credential_procurement_bindings"][tupleKey(id)]
				if len(rows) != 1 || !p.binding(c, v, rows[0]) {
					p.bad(owner, "credential_procurement_bindings", rows)
					valid = false
				}
			}
		case "balance_observe":
			if c.Status == "applied" {
				rows := p.index["procurement_balance_observations"][tupleKey(a.object["id"].text)]
				if len(rows) != 1 || !p.observation(c, a, rows[0]) {
					p.bad(owner, "procurement_balance_observations", rows)
					valid = false
				}
			}
		default:
			valid = false
		}
		if !valid {
			p.mark(owner)
		}
		key := sourcePayloadAuditBusiness("procurement.command."+c.Status, c.OperationID, nil, "")
		as := p.audits[key]
		id := ""
		if len(as) == 1 {
			id = sourceText(as[0].value, "id")
		}
		p.slots = append(p.slots, sourcePayloadSlot{owner: owner, table: "audit_events", id: id, present: len(as) == 1, business: key, actor: c.Actor, action: "procurement.command." + c.Status, target: c.OperationID, value: sourcePayloadMap(map[string]string{"kind": c.Kind, "status": c.Status}), valid: !tracked})
	}
}

func (p *sourcePayloadRelations) legacyAccount(c ProcurementCommandRow, b, a sourcePayloadValue) bool {
	if c.Reason == "not_found" {
		return b.kind == '0' && a.kind == '0'
	}
	raw, ok := p.one("procurement_accounts", c.TargetID)
	if !ok {
		return false
	}
	at := raw.value.Interface().(ProcurementAccountRow).CreatedAt
	for _, v := range []sourcePayloadValue{b, a} {
		if !p.tick() {
			return false
		}
		if v.kind != '0' && (v.object["id"].text != c.TargetID || !p.sameTime(v, "created_at", at)) {
			return false
		}
	}
	if c.Kind == "account_create" {
		return b.kind == '0' && a.kind == '{' && a.object["held_amount"].text == "0" && a.object["spent_amount"].text == "0" && a.object["revision"].text == "1"
	}
	if c.Status != "applied" {
		return c.Reason == "revision_conflict" && b.kind == '{' && a.kind == '0'
	}
	if b.kind != '{' || a.kind != '{' || b.object["held_amount"].text != a.object["held_amount"].text || b.object["spent_amount"].text != a.object["spent_amount"].text {
		return false
	}
	before, bok := numeric(b.object["revision"].text, false)
	after, aok := numeric(a.object["revision"].text, false)
	if !bok || !aok {
		return false
	}
	for _, field := range []string{"label", "provider", "state", "limit_amount"} {
		if !p.tick() {
			return false
		}
		if b.object[field].text != a.object[field].text {
			before.Add(before, big.NewInt(1))
			break
		}
	}
	return before.Cmp(after) == 0
}

func (p *sourcePayloadRelations) binding(c ProcurementCommandRow, v sourcePayloadValue, raw sourceRawRow) bool {
	b := raw.value.Interface().(CredentialProcurementBindingRow)
	fields := map[string]string{"id": b.ID, "credential_id": b.CredentialID, "channel_id": b.ChannelID, "account_id": b.AccountID, "operation_id": b.OperationID}
	if !sourcePayloadSnapshot("credential_bind", v) || v.kind != '{' || c.TargetID != b.CredentialID || c.Status == "applied" && c.OperationID != b.OperationID {
		return false
	}
	for field, expected := range fields {
		if !p.tick() || v.object[field].kind != 's' || v.object[field].text != expected {
			return false
		}
	}
	// channel_revision is an unprojected historical JOIN value, not current state.
	return p.sameTime(v, "bound_at", b.BoundAt)
}

func (p *sourcePayloadRelations) observation(c ProcurementCommandRow, v sourcePayloadValue, raw sourceRawRow) bool {
	o := raw.value.Interface().(ProcurementBalanceObservationRow)
	if !sourcePayloadSnapshot("balance_observe", v) || v.kind != '{' || c.TargetID != o.AccountID || c.Actor != o.Actor || c.OperationID != o.OperationID {
		return false
	}
	for field, expected := range map[string]string{"id": o.ID, "account_id": o.AccountID, "operation_id": o.OperationID, "actor": o.Actor, "status": o.Status, "currency": o.Currency, "source": o.Source, "semantic": o.Semantic, "evidence_ref": o.EvidenceRef} {
		if !p.tick() || v.object[field].kind != 's' || v.object[field].text != expected {
			return false
		}
	}
	amount := v.object["amount"]
	if o.Amount == nil && amount.kind != '0' || o.Amount != nil && (amount.kind != 's' || amount.text != *o.Amount) {
		return false
	}
	provider := v.object["provider_as_of"]
	return p.sameTime(v, "observed_at", o.ObservedAt) && p.sameTime(v, "created_at", o.CreatedAt) && (nullTime(o.ProviderAsOf) && provider.kind == '0' || !nullTime(o.ProviderAsOf) && p.sameTime(v, "provider_as_of", o.ProviderAsOf))
}

func (p *sourcePayloadRelations) bindingObservationReverse() {
	for _, table := range []string{"credential_procurement_bindings", "procurement_balance_observations"} {
		for _, raw := range p.s.tables[table] {
			if !p.tick() {
				return
			}
			owner := sourcePayloadRef{table, raw}
			cs := p.index["procurement_commands"][tupleKey(sourceText(raw.value, "operation_id"))]
			valid := len(cs) == 1
			if valid {
				c := cs[0].value.Interface().(ProcurementCommandRow)
				_, a, shape := p.snapshots(c)
				valid = shape && c.Status == "applied"
				if table == "credential_procurement_bindings" {
					valid = valid && c.Kind == "credential_bind" && p.binding(c, a, raw)
				} else {
					valid = valid && c.Kind == "balance_observe" && p.observation(c, a, raw)
				}
			}
			if !valid {
				p.bad(owner, "procurement_commands", cs)
			}
		}
	}
}

func (p *sourcePayloadRelations) proposal(raw sourceRawRow) (ProcurementCostProposalRow, bool) {
	id := sourceText(raw.value, "proposal_id")
	pr, ok := p.one("procurement_cost_proposals", id)
	if !ok {
		return ProcurementCostProposalRow{}, false
	}
	proposal := pr.value.Interface().(ProcurementCostProposalRow)
	attemptRaw, found := p.one("upstream_attempts", proposal.AttemptID)
	valid := found && proposal.TenantID == sourceText(raw.value, "tenant_id") && proposal.AttemptID == sourceText(raw.value, "attempt_id")
	if found {
		// Attempt scope is immutable. It bridges the proposal (which has no
		// request column) without reading today's mutable cost/delivery head.
		attempt := attemptRaw.value.Interface().(UpstreamAttemptRow)
		valid = valid && attempt.TenantID == proposal.TenantID && attempt.RequestID == sourceText(raw.value, "request_id")
	}
	if raw.value.Type() == reflect.TypeOf(ProposalBirthRow{}) {
		b := raw.value.Interface().(ProposalBirthRow)
		valid = valid && proposal.Requester == b.Requester && proposal.ExpectedReservationRevision == b.ReservationRevision && proposal.ExpectedEvidenceCount == b.MemberCount
	} else {
		a := raw.value.Interface().(ApprovalConsumptionRow)
		birth, found := p.one("pp_proposal_births", a.ProposalBirthID)
		valid = valid && found && proposal.State == "approved" && proposal.Requester == a.Requester && sourceSameOptional(proposal.Approver, &a.Approver) && sourceSameOptional(proposal.ApprovalOperationID, &a.ApprovalOperationID)
		if found {
			b := birth.value.Interface().(ProposalBirthRow)
			valid = valid && b.ProposalID == a.ProposalID && b.TenantID == a.TenantID && b.RequestID == a.RequestID && b.AttemptID == a.AttemptID && b.Requester == a.Requester
		}
	}
	return proposal, valid
}

func (p *sourcePayloadRelations) finish(source ReconciliationSourceRow) (CustomerFinishOutcomeRow, bool) {
	if source.CustomerFinishOutcomeID == nil {
		return CustomerFinishOutcomeRow{}, false
	}
	raw, ok := p.one("pp_customer_finish_outcomes", *source.CustomerFinishOutcomeID)
	if !ok {
		return CustomerFinishOutcomeRow{}, false
	}
	o := raw.value.Interface().(CustomerFinishOutcomeRow)
	valid := o.ReconciliationSourceID == source.ID && o.SourceID == source.ID && o.CaseID == source.CaseID && o.TenantID == source.TenantID && o.RequestID == source.RequestID && sourceCommon(raw.value, reflect.ValueOf(source))
	return o, valid
}

func (p *sourcePayloadRelations) sourceAudits() {
	for _, table := range []string{"pp_proposal_births", "pp_approval_consumptions", "pp_unmanaged_correction_sources", "pp_reconciliation_sources"} {
		for _, raw := range p.s.tables[table] {
			if !p.tick() {
				return
			}
			owner := sourcePayloadRef{table, raw}
			id, sourceID := sourceText(raw.value, "audit_id"), sourceText(raw.value, "id")
			tenant, request := sourceText(raw.value, "tenant_id"), sourceText(raw.value, "request_id")
			actor, action, target, role := "", "", "", ""
			value, valid := sourcePayloadValue{}, true
			switch table {
			case "pp_proposal_births", "pp_approval_consumptions":
				proposal, found := p.proposal(raw)
				valid = found
				fields := map[string]string{"attempt_id": sourceText(raw.value, "attempt_id"), "amount": proposal.Amount, "evidence_ref": proposal.EvidenceRef}
				target = sourceText(raw.value, "proposal_id")
				if table == "pp_proposal_births" {
					actor, action, role = sourceText(raw.value, "requester"), "procurement.cost_proposed", "proposal"
					fields["basis"] = proposal.Basis
				} else {
					actor, action, role = sourceText(raw.value, "approver"), "procurement.cost_approved", "approval"
				}
				value = sourcePayloadMap(fields)
			case "pp_unmanaged_correction_sources":
				u := raw.value.Interface().(UnmanagedCorrectionSourceRow)
				actor = "system"
				if u.InputActorID != nil {
					actor = *u.InputActorID
				}
				valid = actor == u.EffectiveAuditActor
				action, target, role = "cost.corrected", u.ProviderCorrectionID, "unmanaged_correction"
				value = sourcePayloadMap(map[string]string{"amount": u.Amount, "basis": u.Basis})
			case "pp_reconciliation_sources":
				s := raw.value.Interface().(ReconciliationSourceRow)
				actor, target, role = s.Actor, s.CaseID, "reconciliation"
				switch s.Action {
				case "propose":
					action = "reconciliation.proposed"
					value = sourcePayloadMap(map[string]string{"evidence_ref": sourceRelationOptional(s.InputEvidenceRef), "reason": sourceRelationOptional(s.InputReason)})
					u := sourcePayloadMap(map[string]string{"status": sourceRelationOptional(s.InputUsageStatus)})
					for i, field := range []string{"input_tokens", "output_tokens", "total_tokens", "cached_tokens", "reasoning_tokens"} {
						ptr := []*string{s.InputInputTokens, s.InputOutputTokens, s.InputTotalTokens, s.InputCachedTokens, s.InputReasoningTokens}[i]
						u.object[field] = sourcePayloadValue{kind: 'n', text: sourceRelationOptional(ptr)}
						valid = valid && ptr != nil && p.tick()
					}
					value.object["usage"] = u
					valid = valid && s.InputEvidenceRef != nil && s.InputReason != nil && s.InputUsageStatus != nil && sourcePayloadUsage(u) && s.CustomerFinishOutcomeID == nil
				case "approve":
					action = "reconciliation.approved"
					o, found := p.finish(s)
					valid = found
					value = sourcePayloadMap(map[string]string{"outcome": o.ResultStatus})
				case "release":
					action = "reconciliation.released"
					_, found := p.finish(s)
					valid = found && s.InputReason != nil
					value = sourcePayloadMap(map[string]string{"reason": sourceRelationOptional(s.InputReason)})
				default:
					valid = false
				}
			}
			p.auditSlot(owner, id, actor, &tenant, action, target, request, role, sourceID, value, valid)
		}
	}
}

func sourceRelationOptional(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func (p *sourcePayloadRelations) finishOutputs() {
	for _, raw := range p.s.tables["pp_customer_finish_outcomes"] {
		if !p.tick() {
			return
		}
		o := raw.value.Interface().(CustomerFinishOutcomeRow)
		owner := sourcePayloadRef{"pp_customer_finish_outcomes", raw}
		sraw, found := p.one("pp_reconciliation_sources", o.ReconciliationSourceID)
		valid := found
		if found {
			s := sraw.value.Interface().(ReconciliationSourceRow)
			_, valid = p.finish(s)
			valid = valid && sourceSameOptional(s.CustomerFinishOutcomeID, &o.ID) && s.Action != "propose"
		}
		if !valid {
			p.mark(owner)
		}
		if o.ResultKind != "new_settlement" {
			for _, claim := range []struct {
				table string
				id    *string
			}{{"audit_events", o.BillingAuditID}, {"projection_outbox", o.BillingOutboxID}, {"audit_events", o.ProfileBlockAuditID}} {
				if !p.tick() {
					return
				}
				if claim.id != nil {
					p.bad(owner, claim.table, p.index[claim.table][tupleKey(*claim.id)])
				}
			}
			continue
		}
		auditID := sourceRelationOptional(o.BillingAuditID)
		p.auditSlot(owner, auditID, "system", &o.TenantID, "billing."+o.ResultStatus, sourceRelationOptional(o.SettlementID), o.RequestID, "settlement", o.SourceID, sourcePayloadMap(map[string]string{"amount": o.ChargedAmount, "reason": o.SelectedReason}), valid && o.BillingAuditID != nil && o.SettlementID != nil)
		p.slots[len(p.slots)-1].present = o.BillingAuditID != nil
		R, rok := numeric(o.ReservationAmount, false)
		charge, cok := numeric(o.ChargedAmount, false)
		released := ""
		if rok && cok && unsignedInteger(o.ReservationAmount, 63, false) && unsignedInteger(o.ChargedAmount, 63, false) && R.Cmp(charge) >= 0 {
			released = new(big.Int).Sub(R, charge).String()
		}
		p.outboxSlot(owner, o.BillingOutboxID, sourceRelationOptional(o.SettlementID), o.ResultStatus, o.TenantID, o.SourceID, "settlement", sourcePayloadMap(map[string]string{"amount": o.ChargedAmount, "released": released, "request_id": o.RequestID}), valid && o.SettlementID != nil && released != "")
		if o.ProfileBlockAuditID != nil || o.ProfileBlockModelID != nil || o.ProfileBlockProfileVersion != nil || o.ProfileBlockDisposition != nil {
			profileOK := valid && o.ProfileBlockAuditID != nil && o.ProfileBlockModelID != nil && o.ProfileBlockProfileVersion != nil && o.ProfileBlockDisposition != nil
			if profileOK {
				qr, qok := p.one("request_records", o.RequestID)
				blocks := p.index["billing_profile_blocks"][tupleKey(*o.ProfileBlockModelID, *o.ProfileBlockProfileVersion)]
				profileOK = qok && len(blocks) == 1
				if profileOK {
					q, b := qr.value.Interface().(RequestRecordRow), blocks[0].value.Interface().(BillingProfileBlockRow)
					profileOK = q.TenantID == o.TenantID && q.ModelID == *o.ProfileBlockModelID && q.ProfileVersion == *o.ProfileBlockProfileVersion && b.Reason == "usage_exceeded_reservation" && sourceOneOf(*o.ProfileBlockDisposition, "created", "reused") && (*o.ProfileBlockDisposition != "created" || b.RequestID == o.RequestID)
				}
			}
			p.auditSlot(owner, sourceRelationOptional(o.ProfileBlockAuditID), "system", &o.TenantID, "billing.profile_blocked", sourceRelationOptional(o.ProfileBlockModelID), o.RequestID, "profile_block", o.SourceID, sourcePayloadMap(map[string]string{"waived": o.WaivedAmount}), profileOK)
			p.slots[len(p.slots)-1].present = o.ProfileBlockAuditID != nil
		}
	}
}

func (p *sourcePayloadRelations) costLinks() {
	for _, table := range []string{"pp_normal_cost_decisions", "pp_approval_consumptions", "pp_unmanaged_correction_sources"} {
		for _, raw := range p.s.tables[table] {
			if !p.tick() {
				return
			}
			owner := sourcePayloadRef{table, raw}
			kind := map[string]string{"pp_normal_cost_decisions": "normal_cost_decision", "pp_approval_consumptions": "approval_consumed", "pp_unmanaged_correction_sources": "unmanaged_correction"}[table]
			id := sourceText(raw.value, "id")
			ls := p.links[tupleKey(kind, id)]
			operation, outKind, amount, basis, mutationID, outID := "", "", "", "", (*string)(nil), (*string)(nil)
			valid, needed := len(p.index[table][tupleKey(id)]) == 1 && sourceText(raw.value, "step_kind") == kind, true
			switch table {
			case "pp_normal_cost_decisions":
				d := raw.value.Interface().(NormalCostDecisionRow)
				needed = sourceOneOf(d.Decision, "write_no_consumption", "write_final_usage")
				valid = valid && d.SelectedAmount != nil && d.SelectedBasis != nil
				operation, outKind, amount, basis, mutationID, outID = d.AttemptID, "cost_recorded", sourceRelationOptional(d.SelectedAmount), sourceRelationOptional(d.SelectedBasis), d.CostMutationID, d.CostRecordedOutboxID
			case "pp_approval_consumptions":
				a := raw.value.Interface().(ApprovalConsumptionRow)
				proposal, found := p.proposal(raw)
				valid = valid && found
				operation, outKind, amount, basis, mutationID, outID = "procurement:"+a.ApprovalOperationID, "cost_corrected", proposal.Amount, proposal.Basis, &a.CostMutationID, &a.OutboxID
			case "pp_unmanaged_correction_sources":
				u := raw.value.Interface().(UnmanagedCorrectionSourceRow)
				operation, outKind, amount, basis, mutationID, outID = u.OperationID, "cost_corrected", u.Amount, u.Basis, &u.CostMutationID, &u.OutboxID
			}
			if !needed {
				if len(ls) != 0 || outID != nil || mutationID != nil {
					p.bad(owner, "pp_cost_outbox_links", ls)
					if outID != nil {
						p.bad(owner, "projection_outbox", p.index["projection_outbox"][tupleKey(*outID)])
					}
				}
				continue
			}
			if len(ls) != 1 {
				p.bad(owner, "pp_cost_outbox_links", ls)
			}
			if len(ls) == 0 {
				// The cause still establishes its business output identity. Keep
				// an invalid slot so a second NULL-pointer original cannot hide
				// behind the missing link as if it were unrelated legacy output.
				p.outboxSlot(owner, outID, operation, outKind, sourceText(raw.value, "tenant_id"), id, "cost", sourcePayloadMap(map[string]string{"attempt_id": sourceText(raw.value, "attempt_id"), "amount": amount, "basis": basis}), false)
				continue
			}
			for _, linkRaw := range ls {
				if !p.tick() {
					return
				}
				link := linkRaw.value.Interface().(CostOutboxLinkRow)
				good := valid && len(ls) == 1 && len(p.index["pp_cost_outbox_links"][linkRaw.key]) == 1 && outID != nil && mutationID != nil && *outID == link.OutboxID && sourceScope(raw.value, linkRaw.value) && sourceText(raw.value, "root_id") == link.RootID && sourceText(raw.value, "created_xid") == link.CreatedXID && sourcePayloadStepBefore(sourceText(raw.value, "step_no"), link.StepNo)
				mr, mok := p.one("pp_cost_mutations", sourceRelationOptional(mutationID))
				if mok {
					m := mr.value.Interface().(CostMutationRow)
					good = good && m.CauseKind == kind && m.CauseSourceID == id && sourceScope(raw.value, mr.value) && m.RootID == link.RootID && m.CreatedXID == link.CreatedXID && m.AfterStatus == basis && sourceSameOptional(m.AfterAmount, &amount) && sourcePayloadStepBefore(sourceText(raw.value, "step_no"), m.StepNo) && sourcePayloadStepBefore(m.StepNo, link.StepNo)
				} else {
					good = false
				}
				if !good {
					p.bad(owner, "pp_cost_outbox_links", []sourceRawRow{linkRaw})
					if outID != nil {
						p.bad(owner, "projection_outbox", p.index["projection_outbox"][tupleKey(*outID)])
					}
				}
				p.outboxSlot(sourcePayloadRef{"pp_cost_outbox_links", linkRaw}, &link.OutboxID, operation, outKind, link.TenantID, link.ID, "cost", sourcePayloadMap(map[string]string{"attempt_id": link.AttemptID, "amount": amount, "basis": basis}), good)
			}
		}
	}
	for _, raw := range p.s.tables["pp_cost_outbox_links"] {
		if !p.tick() {
			return
		}
		l := raw.value.Interface().(CostOutboxLinkRow)
		table := map[string]string{"normal_cost_decision": "pp_normal_cost_decisions", "approval_consumed": "pp_approval_consumptions", "unmanaged_correction": "pp_unmanaged_correction_sources"}[l.CauseKind]
		if table == "" || len(p.index[table][tupleKey(l.CauseSourceID)]) != 1 {
			p.bad(sourcePayloadRef{"pp_cost_outbox_links", raw}, "projection_outbox", p.index["projection_outbox"][tupleKey(l.OutboxID)])
		}
	}
}

func (p *sourcePayloadRelations) checkSlots() {
	claimed := make(map[string][]int)
	business := make(map[string][]int)
	for i, slot := range p.slots {
		if !p.tick() {
			return
		}
		if slot.present {
			key := tupleKey(slot.table, slot.id)
			claimed[key] = append(claimed[key], i)
		}
		key := tupleKey(slot.table, slot.business)
		business[key] = append(business[key], i)
	}
	for _, slot := range p.slots {
		if !p.tick() {
			return
		}
		originals := []sourceRawRow(nil)
		if slot.present {
			originals = p.index[slot.table][tupleKey(slot.id)]
		}
		valid := slot.valid && slot.present && len(originals) == 1 && len(claimed[tupleKey(slot.table, slot.id)]) == 1
		var peers []sourceRawRow
		if slot.table == "audit_events" {
			peers = p.audits[slot.business]
		} else {
			peers = p.outboxes[slot.business]
		}
		valid = valid && len(peers) == 1 && len(business[tupleKey(slot.table, slot.business)]) == 1 && peers[0].key == tupleKey(slot.id)
		if len(originals) == 1 {
			raw := originals[0]
			payload := ""
			if slot.table == "audit_events" {
				a := raw.value.Interface().(AuditEventRow)
				valid = valid && a.Actor == slot.actor && sourceSameOptional(a.TenantScope, slot.tenant) && a.Action == slot.action && a.Target == slot.target && a.RequestID == slot.request && finite(a.CreatedAt)
				payload = a.SanitizedDiff
			} else {
				o := raw.value.Interface().(ProjectionOutboxRow)
				valid = valid && slot.tenant != nil && o.TenantID == *slot.tenant && o.OperationID == slot.operation && o.Kind == slot.kind && o.SchemaVersion == "1" && finite(o.CreatedAt)
				payload = o.SanitizedPayload
			}
			got, parsed := p.parse(payload)
			valid = p.equal(got, slot.value) && parsed && sourcePayloadPointerEqual(raw.value, slot.pointer) && valid
		}
		if !valid {
			p.bad(slot.owner, slot.table, originals)
			p.bad(slot.owner, slot.table, peers)
		}
	}
	for _, table := range []string{"audit_events", "projection_outbox"} {
		for _, raw := range p.s.tables[table] {
			if !p.tick() {
				return
			}
			key := tupleKey(table, sourceText(raw.value, sourcePhysicalLayouts[table].pk))
			cs := claimed[key]
			tracked := !sourceAll(raw.value, false, "pp_root_id", "pp_root_step", "pp_source_kind", "pp_source_id", "pp_source_role", "pp_created_xid")
			command := table == "audit_events" && sourceOneOf(sourceText(raw.value, "action"), "procurement.command.applied", "procurement.command.rejected")
			if (tracked || command) && len(cs) != 1 {
				p.mark(sourcePayloadRef{table, raw})
				for _, i := range cs {
					p.mark(p.slots[i].owner)
				}
			}
		}
	}
}
