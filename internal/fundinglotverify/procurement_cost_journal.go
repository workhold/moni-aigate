package fundinglotverify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrProcurementCostJournalRowsInput    = errors.New("funding_lot_procurement_cost_journal_rows_input")
	ErrProcurementCostJournalRowsResource = errors.New("funding_lot_procurement_cost_journal_rows_resource")
	ErrProcurementCostJournalRowsCanceled = errors.New("funding_lot_procurement_cost_journal_rows_canceled")
	ErrProcurementCostJournalRowsTimeout  = errors.New("funding_lot_procurement_cost_journal_rows_timeout")
)

// ProcurementOutboxRow is the fixed raw projection shared by maintenance checks.
// It contains no authority to consume, publish or recreate an outbox event.
type ProcurementOutboxRow struct {
	ID, TenantID, OperationID, Kind, Payload string
	SchemaVersion                            int32
	CreatedAt                                time.Time
}

type ProcurementCostJournalRows struct {
	Users        []ProcurementApprovalUserRow
	Attempts     []ProcurementAttemptRow
	Reservations []ProcurementAttemptReservationRow
	Corrections  []ProcurementCostCorrectionRow
	Proposals    []ProcurementCostProposalRow
	Audits       []ProcurementAuditRow
	Outbox       []ProcurementOutboxRow
}

type ProcurementCostJournalCounts struct {
	Users        uint64 `json:"users"`
	Attempts     uint64 `json:"attempts"`
	Reservations uint64 `json:"reservations"`
	Corrections  uint64 `json:"corrections"`
	Proposals    uint64 `json:"proposals"`
	Audits       uint64 `json:"audits"`
	Outbox       uint64 `json:"outbox"`
}

type ProcurementCostJournalMismatches struct {
	DuplicateUserRows                uint64 `json:"duplicate_user_rows"`
	DuplicateAttemptRows             uint64 `json:"duplicate_attempt_rows"`
	DuplicateReservationRows         uint64 `json:"duplicate_reservation_rows"`
	DuplicateCorrectionRows          uint64 `json:"duplicate_correction_rows"`
	DuplicateCorrectionOperationRows uint64 `json:"duplicate_correction_operation_rows"`
	DuplicateProposalRows            uint64 `json:"duplicate_proposal_rows"`
	DuplicateApprovalOperationRows   uint64 `json:"duplicate_approval_operation_rows"`
	DuplicateAuditRows               uint64 `json:"duplicate_audit_rows"`
	DuplicateAuditEffectRows         uint64 `json:"duplicate_audit_effect_rows"`
	DuplicateOutboxRows              uint64 `json:"duplicate_outbox_rows"`
	DuplicateOutboxOperationKindRows uint64 `json:"duplicate_outbox_operation_kind_rows"`
	InvalidTextRows                  uint64 `json:"invalid_text_rows"`
	InvalidAmountRows                uint64 `json:"invalid_amount_rows"`
	InvalidEnumRows                  uint64 `json:"invalid_enum_rows"`
	InvalidJSONRows                  uint64 `json:"invalid_json_rows"`
	ProposalIdentityRows             uint64 `json:"proposal_identity_rows"`
	ProposalShapeRows                uint64 `json:"proposal_shape_rows"`
	CorrectionIdentityRows           uint64 `json:"correction_identity_rows"`
	ManagedCorrectionProposalRows    uint64 `json:"managed_correction_proposal_rows"`
	ProposedAuditRows                uint64 `json:"proposed_audit_rows"`
	ApprovedAuditRows                uint64 `json:"approved_audit_rows"`
	LegacyAuditRows                  uint64 `json:"legacy_audit_rows"`
	CorrectionOutboxRows             uint64 `json:"correction_outbox_rows"`
	AuditSourceRows                  uint64 `json:"audit_source_rows"`
	OutboxSourceRows                 uint64 `json:"outbox_source_rows"`
}

// ProcurementCostJournalCheck covers only the supplied audit/outbox graph.
// It does not establish financial journals, supplier evidence or full coverage.
type ProcurementCostJournalCheck struct {
	Scope      string                           `json:"scope"`
	Scanned    ProcurementCostJournalCounts     `json:"scanned"`
	Balanced   bool                             `json:"balanced"`
	Mismatches ProcurementCostJournalMismatches `json:"mismatches"`
}

func procurementCostJournalError(err error) error {
	switch {
	case errors.Is(err, ErrProcurementCostApprovalRowsResource):
		return ErrProcurementCostJournalRowsResource
	case errors.Is(err, ErrProcurementCostApprovalRowsTimeout), errors.Is(err, context.DeadlineExceeded):
		return ErrProcurementCostJournalRowsTimeout
	default:
		return ErrProcurementCostJournalRowsCanceled
	}
}

func procurementCostJournalRelated(action string) bool {
	return action == "procurement.cost_proposed" || action == "procurement.cost_approved" || action == "cost.corrected"
}

// The typed key keeps action/target and operation/kind separate even when either
// input contains punctuation. It is not a string-prefix source classifier.
type procurementCostJournalKey struct{ first, second string }

func procurementCostJournalIndex[T any](
	w *procurementApprovalWork, rows []T, key func(T) string, valid func(string) bool,
	effect func(T) (procurementCostJournalKey, bool), duplicate, duplicateEffect *uint64,
) (map[string]T, []T, error) {
	index := make(map[string]T, len(rows))
	seen := make(map[string]struct{}, len(rows))
	effects := make(map[procurementCostJournalKey]struct{}, len(rows))
	canonical := make([]T, 0, len(rows))
	for _, row := range rows {
		if err := w.tick(); err != nil {
			return nil, nil, procurementCostJournalError(err)
		}
		id := key(row)
		if !valid(id) {
			continue
		}
		_, duplicated := seen[id]
		seen[id] = struct{}{}
		if duplicated {
			(*duplicate)++
		}
		duplicatedEffect := false
		if effect != nil {
			if e, ok := effect(row); ok {
				_, duplicatedEffect = effects[e]
				effects[e] = struct{}{}
				if duplicatedEffect {
					(*duplicateEffect)++
				}
			}
		}
		if duplicated || duplicatedEffect {
			continue
		}
		index[id], canonical = row, append(canonical, row)
	}
	return index, canonical, nil
}

func procurementCostJournalJSONKeys(action string) []string {
	switch action {
	case "procurement.cost_proposed":
		return []string{"attempt_id", "amount", "basis", "evidence_ref"}
	case "procurement.cost_approved":
		return []string{"attempt_id", "amount", "evidence_ref"}
	case "cost.corrected":
		return []string{"amount", "basis"}
	default: // cost_corrected outbox
		return []string{"attempt_id", "amount", "basis"}
	}
}

// JSONB changes object order and whitespace. Decode semantic strings strictly,
// without allowing numeric Money, unknown keys, duplicate keys or tail values.
func procurementCostJournalJSON(raw string, keys []string) (map[string]string, bool) {
	if !utf8.ValidString(raw) || !procurementCostJournalUnicode(raw) {
		return nil, false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	values := make(map[string]string, len(keys))
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok {
			return nil, false
		}
		allowed := false
		for _, k := range keys {
			allowed = allowed || name == k
		}
		if !allowed {
			return nil, false
		}
		if _, exists := values[name]; exists {
			return nil, false
		}
		var value string
		var rawValue json.RawMessage
		if d.Decode(&rawValue) != nil || len(rawValue) == 0 || rawValue[0] != '"' || json.Unmarshal(rawValue, &value) != nil {
			return nil, false
		}
		values[name] = value
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') || len(values) != len(keys) {
		return nil, false
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return nil, false
	}
	if _, ok := procurementAttemptInt64(values["amount"]); !ok {
		return nil, false
	}
	return values, true
}

// encoding/json replaces isolated UTF16 surrogate escapes. Reject those before
// decoding so an invalid document cannot equal a literal replacement character.
func procurementCostJournalUnicode(raw string) bool {
	quoted := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		code, ok := procurementCostJournalHex4(raw, i+1)
		if !ok {
			return false
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+2 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, ok := procurementCostJournalHex4(raw, i+3)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func procurementCostJournalHex4(raw string, at int) (uint16, bool) {
	if at+4 > len(raw) {
		return 0, false
	}
	var n uint16
	for _, c := range []byte(raw[at : at+4]) {
		n <<= 4
		switch {
		case c >= '0' && c <= '9':
			n += uint16(c - '0')
		case c >= 'a' && c <= 'f':
			n += uint16(c-'a') + 10
		case c >= 'A' && c <= 'F':
			n += uint16(c-'A') + 10
		default:
			return 0, false
		}
	}
	return n, true
}

func procurementCostJournalTenant(scope *string, tenant string) bool {
	return scope != nil && *scope == tenant && procurementAttemptValidText(tenant)
}

func procurementCostJournalProposalAudit(p ProcurementCostProposalRow, a ProcurementAttemptRow, event ProcurementAuditRow, values map[string]string, approved bool) bool {
	actor, action := p.Requester, "procurement.cost_proposed"
	if approved {
		if p.State != "approved" || !procurementApprovalShape(p) {
			return false
		}
		actor, action = *p.Approver, "procurement.cost_approved"
	}
	return values != nil && p.TenantID == a.TenantID && procurementCostJournalTenant(event.TenantScope, p.TenantID) &&
		event.Action == action && event.Target == p.ID && event.Actor == actor && event.RequestID == a.RequestID &&
		values["attempt_id"] == p.AttemptID && values["amount"] == p.Amount && values["evidence_ref"] == p.EvidenceRef &&
		(approved || values["basis"] == p.Basis)
}

func procurementCostJournalLegacyAudit(c ProcurementCostCorrectionRow, a ProcurementAttemptRow, event ProcurementAuditRow, values map[string]string) bool {
	return values != nil && c.TenantID == a.TenantID && procurementCostJournalTenant(event.TenantScope, c.TenantID) &&
		event.Action == "cost.corrected" && event.Target == c.ID && procurementAttemptValidText(event.Actor) &&
		event.RequestID == a.RequestID && values["amount"] == c.Amount && values["basis"] == c.Basis
}

func procurementCostJournalOutbox(c ProcurementCostCorrectionRow, event ProcurementOutboxRow, values map[string]string) bool {
	return values != nil && event.Kind == "cost_corrected" && event.SchemaVersion == 1 && event.OperationID == c.OperationID &&
		event.TenantID == c.TenantID && values["attempt_id"] == c.AttemptID && values["amount"] == c.Amount && values["basis"] == c.Basis
}

// CheckProcurementCostJournalGraph compares fixed source/audit/outbox effects in
// both directions. Unrelated actions/kinds are not parsed as cost operations.
func CheckProcurementCostJournalGraph(ctx context.Context, rows ProcurementCostJournalRows) (ProcurementCostJournalCheck, error) {
	if ctx == nil {
		return ProcurementCostJournalCheck{}, ErrProcurementCostJournalRowsInput
	}
	if ctx.Err() != nil {
		return ProcurementCostJournalCheck{}, procurementCostJournalError(ctx.Err())
	}
	remaining := uint64(fundingSourceRowLimit)
	for _, count := range [...]int{len(rows.Users), len(rows.Attempts), len(rows.Reservations), len(rows.Corrections), len(rows.Proposals), len(rows.Audits), len(rows.Outbox)} {
		if uint64(count) > remaining {
			return ProcurementCostJournalCheck{}, ErrProcurementCostJournalRowsResource
		}
		remaining -= uint64(count)
	}
	w := procurementApprovalWork{ctx: ctx}
	invalid, err := procurementApprovalTextPass(&w, ProcurementCostApprovalRows{Users: rows.Users, Attempts: rows.Attempts, Reservations: rows.Reservations, Corrections: rows.Corrections, Proposals: rows.Proposals})
	if err != nil {
		return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
	}
	out := ProcurementCostJournalCheck{
		Scope: "procurement_cost_journal_graph_v1",
		Scanned: ProcurementCostJournalCounts{Users: uint64(len(rows.Users)), Attempts: uint64(len(rows.Attempts)), Reservations: uint64(len(rows.Reservations)),
			Corrections: uint64(len(rows.Corrections)), Proposals: uint64(len(rows.Proposals)), Audits: uint64(len(rows.Audits)), Outbox: uint64(len(rows.Outbox))},
		Mismatches: ProcurementCostJournalMismatches{InvalidTextRows: invalid},
	}
	m := &out.Mismatches
	for _, event := range rows.Audits {
		if err := w.tick(); err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		required, optional := []string{event.ID, event.Action}, []string{event.Diff, event.RequestID}
		if event.TenantScope != nil {
			optional = append(optional, *event.TenantScope)
		}
		if procurementCostJournalRelated(event.Action) {
			required = append(required, event.Actor, event.Target)
		} else {
			optional = append(optional, event.Actor, event.Target)
		}
		valid, err := w.text(required, optional)
		if err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		if !valid {
			m.InvalidTextRows++
		}
	}
	for _, event := range rows.Outbox {
		if err := w.tick(); err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		required, optional := []string{event.ID, event.Kind}, []string{event.Payload}
		if event.Kind == "cost_corrected" {
			required = append(required, event.TenantID, event.OperationID)
		} else {
			optional = append(optional, event.TenantID, event.OperationID)
		}
		valid, err := w.text(required, optional)
		if err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		if !valid {
			m.InvalidTextRows++
		}
	}
	users, _, err := procurementCostJournalIndex(&w, rows.Users, func(r ProcurementApprovalUserRow) string { return r.ID }, procurementAttemptValidText, nil, &m.DuplicateUserRows, nil)
	if err != nil {
		return ProcurementCostJournalCheck{}, err
	}
	attempts, _, err := procurementCostJournalIndex(&w, rows.Attempts, func(r ProcurementAttemptRow) string { return r.ID }, procurementAttemptValidText, nil, &m.DuplicateAttemptRows, nil)
	if err != nil {
		return ProcurementCostJournalCheck{}, err
	}
	reservations, _, err := procurementCostJournalIndex(&w, rows.Reservations, func(r ProcurementAttemptReservationRow) string { return r.AttemptID }, procurementAttemptValidText, nil, &m.DuplicateReservationRows, nil)
	if err != nil {
		return ProcurementCostJournalCheck{}, err
	}
	correctionsByID, corrections, err := procurementCostJournalIndex(&w, rows.Corrections, func(r ProcurementCostCorrectionRow) string { return r.ID }, procurementAttemptValidText,
		func(r ProcurementCostCorrectionRow) (procurementCostJournalKey, bool) {
			return procurementCostJournalKey{r.OperationID, ""}, procurementAttemptValidText(r.OperationID)
		}, &m.DuplicateCorrectionRows, &m.DuplicateCorrectionOperationRows)
	if err != nil {
		return ProcurementCostJournalCheck{}, err
	}
	proposalsByID, proposals, err := procurementCostJournalIndex(&w, rows.Proposals, func(r ProcurementCostProposalRow) string { return r.ID }, procurementApprovalUUID,
		func(r ProcurementCostProposalRow) (procurementCostJournalKey, bool) {
			if r.ApprovalOperationID == nil {
				return procurementCostJournalKey{}, false
			}
			return procurementCostJournalKey{*r.ApprovalOperationID, ""}, procurementApprovalUUID(*r.ApprovalOperationID)
		}, &m.DuplicateProposalRows, &m.DuplicateApprovalOperationRows)
	if err != nil {
		return ProcurementCostJournalCheck{}, err
	}
	_, audits, err := procurementCostJournalIndex(&w, rows.Audits, func(r ProcurementAuditRow) string { return r.ID }, procurementAttemptValidText,
		func(r ProcurementAuditRow) (procurementCostJournalKey, bool) {
			return procurementCostJournalKey{r.Action, r.Target}, procurementCostJournalRelated(r.Action) && procurementAttemptValidText(r.Target)
		}, &m.DuplicateAuditRows, &m.DuplicateAuditEffectRows)
	if err != nil {
		return ProcurementCostJournalCheck{}, err
	}
	_, outbox, err := procurementCostJournalIndex(&w, rows.Outbox, func(r ProcurementOutboxRow) string { return r.ID }, procurementAttemptValidText,
		func(r ProcurementOutboxRow) (procurementCostJournalKey, bool) {
			return procurementCostJournalKey{r.OperationID, r.Kind}, utf8.ValidString(r.OperationID) && !procurementApprovalContainsNUL(r.OperationID) && procurementAttemptValidText(r.Kind)
		}, &m.DuplicateOutboxRows, &m.DuplicateOutboxOperationKindRows)
	if err != nil {
		return ProcurementCostJournalCheck{}, err
	}
	auditEffects := make(map[procurementCostJournalKey]ProcurementAuditRow, len(audits))
	auditJSON := make(map[string]map[string]string, len(audits))
	for _, event := range audits {
		if err := w.tick(); err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		if !procurementCostJournalRelated(event.Action) {
			continue
		}
		auditEffects[procurementCostJournalKey{event.Action, event.Target}] = event
		values, valid := procurementCostJournalJSON(event.Diff, procurementCostJournalJSONKeys(event.Action))
		if !valid {
			m.InvalidJSONRows++
		}
		auditJSON[event.ID] = values
	}
	boxEffects := make(map[string]ProcurementOutboxRow, len(outbox))
	boxJSON := make(map[string]map[string]string, len(outbox))
	for _, event := range outbox {
		if err := w.tick(); err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		if event.Kind != "cost_corrected" {
			continue
		}
		boxEffects[event.OperationID] = event
		values, valid := procurementCostJournalJSON(event.Payload, procurementCostJournalJSONKeys("cost_corrected"))
		if !valid {
			m.InvalidJSONRows++
		}
		boxJSON[event.ID] = values
	}
	approved := make(map[string]ProcurementCostProposalRow, len(proposals))
	for _, p := range proposals {
		if err := w.tick(); err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		a, exists := attempts[p.AttemptID]
		_, held := reservations[p.AttemptID]
		_, maker := users[p.Requester]
		identity := exists && held && maker && p.TenantID == a.TenantID
		if p.State == "approved" {
			if p.Approver == nil {
				identity = false
			} else if _, checker := users[*p.Approver]; !checker {
				identity = false
			}
		}
		if !identity {
			m.ProposalIdentityRows++
		}
		if !procurementApprovalShape(p) {
			m.ProposalShapeRows++
		}
		if p.State == "approved" && procurementApprovalShape(p) && procurementApprovalUUID(*p.ApprovalOperationID) {
			approved["procurement:"+*p.ApprovalOperationID] = p
		}
		event, exists := auditEffects[procurementCostJournalKey{"procurement.cost_proposed", p.ID}]
		if !exists || !procurementCostJournalProposalAudit(p, a, event, auditJSON[event.ID], false) {
			m.ProposedAuditRows++
		}
		if p.State == "approved" {
			event, exists = auditEffects[procurementCostJournalKey{"procurement.cost_approved", p.ID}]
			if !exists || !procurementCostJournalProposalAudit(p, a, event, auditJSON[event.ID], true) {
				m.ApprovedAuditRows++
			}
		}
	}
	correctionsByOperation := make(map[string]ProcurementCostCorrectionRow, len(corrections))
	for _, c := range corrections {
		if err := w.tick(); err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		if procurementAttemptValidText(c.OperationID) {
			correctionsByOperation[c.OperationID] = c
		}
		a, exists := attempts[c.AttemptID]
		if !exists || c.TenantID != a.TenantID {
			m.CorrectionIdentityRows++
		}
		if _, managed := reservations[c.AttemptID]; managed {
			p, found := approved[c.OperationID]
			if !found || !procurementApprovalCorrectionMatches(p, c) {
				m.ManagedCorrectionProposalRows++
			}
		} else {
			event, found := auditEffects[procurementCostJournalKey{"cost.corrected", c.ID}]
			if !found || !procurementCostJournalLegacyAudit(c, a, event, auditJSON[event.ID]) {
				m.LegacyAuditRows++
			}
		}
		event, found := boxEffects[c.OperationID]
		if !found || !procurementCostJournalOutbox(c, event, boxJSON[event.ID]) {
			m.CorrectionOutboxRows++
		}
	}
	for _, event := range audits {
		if err := w.tick(); err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		if !procurementCostJournalRelated(event.Action) {
			continue
		}
		match := false
		if event.Action == "cost.corrected" {
			c, exists := correctionsByID[event.Target]
			a, attempt := attempts[c.AttemptID]
			_, managed := reservations[c.AttemptID]
			match = exists && attempt && !managed && procurementCostJournalLegacyAudit(c, a, event, auditJSON[event.ID])
		} else {
			p, exists := proposalsByID[event.Target]
			a, attempt := attempts[p.AttemptID]
			match = exists && attempt && procurementCostJournalProposalAudit(p, a, event, auditJSON[event.ID], event.Action == "procurement.cost_approved")
		}
		if !match {
			m.AuditSourceRows++
		}
	}
	for _, event := range outbox {
		if err := w.tick(); err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		if event.Kind != "cost_corrected" {
			continue
		}
		c, exists := correctionsByOperation[event.OperationID]
		if !exists || !procurementCostJournalOutbox(c, event, boxJSON[event.ID]) {
			m.OutboxSourceRows++
		}
	}
	for _, c := range rows.Corrections {
		if err := w.tick(); err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		if _, ok := procurementAttemptInt64(c.Amount); !ok {
			m.InvalidAmountRows++
		}
		if !procurementAttemptStatus(c.Basis, "contract", "invoice") {
			m.InvalidEnumRows++
		}
	}
	for _, p := range rows.Proposals {
		if err := w.tick(); err != nil {
			return ProcurementCostJournalCheck{}, procurementCostJournalError(err)
		}
		_, amount := procurementAttemptInt64(p.Amount)
		_, revision := procurementAttemptPositiveInt64(p.ExpectedRevision)
		_, evidence := procurementAttemptInt64(p.ExpectedEvidenceCount)
		if !amount || !revision || !evidence {
			m.InvalidAmountRows++
		}
		if !procurementAttemptStatus(p.Basis, "contract", "invoice") || !procurementAttemptStatus(p.State, "proposed", "approved") {
			m.InvalidEnumRows++
		}
	}
	if ctx.Err() != nil {
		return ProcurementCostJournalCheck{}, procurementCostJournalError(ctx.Err())
	}
	out.Balanced = out.Mismatches == (ProcurementCostJournalMismatches{})
	return out, nil
}
