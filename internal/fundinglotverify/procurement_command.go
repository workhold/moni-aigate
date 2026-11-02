package fundinglotverify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrProcurementCommandRowsInput    = errors.New("funding_lot_procurement_command_rows_input")
	ErrProcurementCommandRowsResource = errors.New("funding_lot_procurement_command_rows_resource")
	ErrProcurementCommandRowsCanceled = errors.New("funding_lot_procurement_command_rows_canceled")
	ErrProcurementCommandRowsTimeout  = errors.New("funding_lot_procurement_command_rows_timeout")
)

type ProcurementAuditRow struct {
	ID, Actor, Action, Target, Diff, RequestID string
	TenantScope                                *string
	CreatedAt                                  time.Time
}

type ProcurementCommandRow struct {
	OperationID, Actor, Kind, TargetID, InputDigest, Status, Reason, Before, After string
	CreatedAt                                                                      time.Time
}

type ProcurementObservationRow struct {
	ID, AccountID, OperationID, Actor, Status string
	Amount                                    *string
	Currency, Source, Semantic                string
	ObservedAt                                time.Time
	ProviderAsOf                              *time.Time
	EvidenceRef                               string
	CreatedAt                                 time.Time
}

type ProcurementCommandRows struct {
	Users        []ProcurementApprovalUserRow
	Accounts     []ProcurementBudgetAccountRow
	Bindings     []ProcurementAttemptBindingRow
	Commands     []ProcurementCommandRow
	Observations []ProcurementObservationRow
	Audits       []ProcurementAuditRow
}

type ProcurementCommandCounts struct {
	Users        uint64 `json:"users"`
	Accounts     uint64 `json:"accounts"`
	Bindings     uint64 `json:"bindings"`
	Commands     uint64 `json:"commands"`
	Observations uint64 `json:"observations"`
	Audits       uint64 `json:"audits"`
}

type ProcurementCommandMismatches struct {
	DuplicateUserRows                 uint64 `json:"duplicate_user_rows"`
	DuplicateAccountRows              uint64 `json:"duplicate_account_rows"`
	DuplicateBindingRows              uint64 `json:"duplicate_binding_rows"`
	DuplicateBindingOperationRows     uint64 `json:"duplicate_binding_operation_rows"`
	DuplicateCommandRows              uint64 `json:"duplicate_command_rows"`
	DuplicateObservationRows          uint64 `json:"duplicate_observation_rows"`
	DuplicateObservationOperationRows uint64 `json:"duplicate_observation_operation_rows"`
	DuplicateAuditRows                uint64 `json:"duplicate_audit_rows"`
	DuplicateCommandAuditTargetRows   uint64 `json:"duplicate_command_audit_target_rows"`
	InvalidTextRows                   uint64 `json:"invalid_text_rows"`
	InvalidAmountRows                 uint64 `json:"invalid_amount_rows"`
	InvalidEnumRows                   uint64 `json:"invalid_enum_rows"`
	CommandShapeRows                  uint64 `json:"command_shape_rows"`
	SnapshotRows                      uint64 `json:"snapshot_rows"`
	CommandActorRows                  uint64 `json:"command_actor_rows"`
	CommandTargetRows                 uint64 `json:"command_target_rows"`
	AppliedBindingRows                uint64 `json:"applied_binding_rows"`
	AppliedObservationRows            uint64 `json:"applied_observation_rows"`
	BindingCommandRows                uint64 `json:"binding_command_rows"`
	ObservationCommandRows            uint64 `json:"observation_command_rows"`
	CommandAuditRows                  uint64 `json:"command_audit_rows"`
	AuditCommandRows                  uint64 `json:"audit_command_rows"`
}

type ProcurementCommandCheck struct {
	Scope      string                       `json:"scope"`
	Scanned    ProcurementCommandCounts     `json:"scanned"`
	Balanced   bool                         `json:"balanced"`
	Mismatches ProcurementCommandMismatches `json:"mismatches"`
}

type procurementCommandWork struct {
	ctx   context.Context
	steps uint16
	bytes int
}

func procurementCommandContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrProcurementCommandRowsTimeout
	}
	return ErrProcurementCommandRowsCanceled
}

func (w *procurementCommandWork) tick() error {
	w.steps++
	if w.steps&255 == 0 && w.ctx.Err() != nil {
		return procurementCommandContextError(w.ctx)
	}
	return nil
}

func (w *procurementCommandWork) text(required, optional []string) (bool, error) {
	valid := true
	for _, fields := range [][]string{required, optional} {
		for _, value := range fields {
			if len(value) > fundingSourceByteLimit-w.bytes {
				return false, ErrProcurementCommandRowsResource
			}
			w.bytes += len(value)
			if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
				valid = false
			}
		}
	}
	for _, value := range required {
		if value == "" {
			valid = false
		}
	}
	return valid, nil
}

func procurementCommandHex(value string) bool {
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

func procurementCommandMetadata(value string, max int, nonspace bool) bool {
	if len(value) == 0 || len(value) > max || !utf8.ValidString(value) || nonspace && strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// A snapshot is flat and closed. Token decoding detects duplicate decoded keys,
// including escaped aliases, rather than accepting map/struct last-key-wins.
func procurementCommandObject(raw string, keys []string, nullable ...string) (map[string]*string, bool, bool) {
	if len(raw) > 16384 || !utf8.ValidString(raw) || !procurementCommandJSONUnicode(raw) {
		return nil, false, false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	token, err := d.Token()
	if err != nil {
		return nil, false, false
	}
	if token == nil {
		if _, err := d.Token(); err == io.EOF {
			return nil, true, true
		}
		return nil, false, false
	}
	if token != json.Delim('{') {
		return nil, false, false
	}
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = false
	}
	for _, key := range nullable {
		allowed[key] = true
	}
	values := make(map[string]*string, len(keys))
	for d.More() {
		keyToken, err := d.Token()
		key, keyOK := keyToken.(string)
		allowNull, allowedKey := allowed[key]
		if err != nil || !keyOK || !allowedKey {
			return nil, false, false
		}
		if _, duplicate := values[key]; duplicate {
			return nil, false, false
		}
		value, err := d.Token()
		if err != nil {
			return nil, false, false
		}
		if value == nil && allowNull {
			values[key] = nil
			continue
		}
		text, ok := value.(string)
		if !ok || !procurementCommandMetadata(text, 1000, false) {
			return nil, false, false
		}
		values[key] = &text
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(values) != len(keys) {
		return nil, false, false
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, false, false
	}
	return values, false, true
}

// encoding/json replaces malformed UTF-16 escapes with U+FFFD. PostgreSQL
// JSONB rejects those escapes, so the independent DTO parser must reject them
// too rather than silently making an invalid identity match a literal U+FFFD.
func procurementCommandJSONUnicode(raw string) bool {
	unit := func(at int) (uint16, bool) {
		if at+4 > len(raw) {
			return 0, false
		}
		var value uint16
		for i := at; i < at+4; i++ {
			value <<= 4
			switch c := raw[i]; {
			case c >= '0' && c <= '9':
				value |= uint16(c - '0')
			case c >= 'a' && c <= 'f':
				value |= uint16(c-'a') + 10
			case c >= 'A' && c <= 'F':
				value |= uint16(c-'A') + 10
			default:
				return 0, false
			}
		}
		return value, true
	}
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
			continue // Consume escaped quotes and slashes without reinterpreting them.
		}
		value, ok := unit(i + 1)
		if !ok {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, lowOK := unit(i + 3)
			if !lowOK || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

var procurementCommandAccountKeys = []string{"id", "label", "provider", "state", "limit_amount", "held_amount", "spent_amount", "revision", "created_at"}
var procurementCommandBindingKeys = []string{"id", "credential_id", "channel_id", "account_id", "operation_id", "bound_at", "channel_revision"}
var procurementCommandObservationKeys = []string{"id", "account_id", "operation_id", "actor", "status", "amount", "currency", "source", "semantic", "observed_at", "provider_as_of", "evidence_ref", "created_at"}

func procurementCommandTime(value string) (time.Time, bool) {
	// time.Parse accepts a comma fraction, but Go's persisted JSON timestamp
	// grammar emits a period. Preserve the supported persisted grammar.
	if strings.ContainsRune(value, ',') {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil && procurementCommandFiniteTime(parsed)
}

func procurementCommandFiniteTime(value time.Time) bool {
	return value.Year() >= 1 && value.Year() <= 9999 && value.Nanosecond()%1000 == 0
}

func procurementCommandValue(values map[string]*string, key string) string {
	if values[key] == nil {
		return ""
	}
	return *values[key]
}

func procurementCommandObservationShape(row ProcurementObservationRow) bool {
	if !procurementAttemptStatus(row.Status, "known", "unknown", "unsupported", "error") || row.Currency != "CNY" ||
		!procurementCommandMetadata(row.Source, 100, false) || !procurementCommandMetadata(row.Semantic, 100, false) || !procurementCommandMetadata(row.EvidenceRef, 256, false) {
		return false
	}
	if row.Status == "known" {
		if row.Amount == nil {
			return false
		}
		_, ok := procurementAttemptInt64(*row.Amount)
		return ok
	}
	return row.Amount == nil
}

type procurementCommandSnapshot struct {
	values map[string]*string
	null   bool
	valid  bool
}

func procurementCommandDecode(kind, raw string) procurementCommandSnapshot {
	var keys []string
	var nullable []string
	switch kind {
	case "account_create", "account_configure":
		keys = procurementCommandAccountKeys
	case "credential_bind":
		keys = procurementCommandBindingKeys
	case "balance_observe":
		keys, nullable = procurementCommandObservationKeys, []string{"amount", "provider_as_of"}
	default:
		return procurementCommandSnapshot{}
	}
	values, null, valid := procurementCommandObject(raw, keys, nullable...)
	s := procurementCommandSnapshot{values: values, null: null, valid: valid}
	if !valid || null {
		return s
	}
	v := func(key string) string { return procurementCommandValue(values, key) }
	switch kind {
	case "account_create", "account_configure":
		_, limitOK := procurementAttemptInt64(v("limit_amount"))
		_, heldOK := procurementAttemptNumeric(v("held_amount"))
		_, spentOK := procurementAttemptNumeric(v("spent_amount"))
		_, revisionOK := procurementAttemptPositiveInt64(v("revision"))
		_, timeOK := procurementCommandTime(v("created_at"))
		s.valid = limitOK && heldOK && spentOK && revisionOK && timeOK && procurementAttemptStatus(v("state"), "active", "paused") &&
			procurementCommandMetadata(v("label"), 100, true) && procurementCommandMetadata(v("provider"), 100, true)
	case "credential_bind":
		_, revisionOK := procurementAttemptPositiveInt64(v("channel_revision"))
		_, timeOK := procurementCommandTime(v("bound_at"))
		s.valid = revisionOK && timeOK && procurementApprovalUUID(v("operation_id"))
	case "balance_observe":
		observation, ok := procurementCommandSnapshotObservation(s)
		s.valid = ok && procurementApprovalUUID(observation.OperationID) && procurementCommandObservationShape(observation)
	}
	return s
}

func procurementCommandSnapshotObservation(s procurementCommandSnapshot) (ProcurementObservationRow, bool) {
	v := func(key string) string { return procurementCommandValue(s.values, key) }
	row := ProcurementObservationRow{ID: v("id"), AccountID: v("account_id"), OperationID: v("operation_id"), Actor: v("actor"), Status: v("status"),
		Amount: s.values["amount"], Currency: v("currency"), Source: v("source"), Semantic: v("semantic"), EvidenceRef: v("evidence_ref")}
	var observedOK, createdOK bool
	row.ObservedAt, observedOK = procurementCommandTime(v("observed_at"))
	row.CreatedAt, createdOK = procurementCommandTime(v("created_at"))
	providerOK := true
	if s.values["provider_as_of"] != nil {
		var value time.Time
		value, providerOK = procurementCommandTime(v("provider_as_of"))
		row.ProviderAsOf = &value
	}
	return row, observedOK && createdOK && providerOK
}

func procurementCommandSnapshotValue(s procurementCommandSnapshot, key string) string {
	return procurementCommandValue(s.values, key)
}

func procurementCommandSnapshotTimeEqual(left, right procurementCommandSnapshot, key string) bool {
	l, leftOK := procurementCommandTime(procurementCommandSnapshotValue(left, key))
	r, rightOK := procurementCommandTime(procurementCommandSnapshotValue(right, key))
	return leftOK && rightOK && l.Equal(r)
}

func procurementCommandBindingMatches(row ProcurementCommandRow, s procurementCommandSnapshot, binding ProcurementAttemptBindingRow, applied bool) bool {
	if !s.valid || s.null {
		return false
	}
	v := func(key string) string { return procurementCommandSnapshotValue(s, key) }
	boundAt, valid := procurementCommandTime(v("bound_at"))
	return valid && row.TargetID == binding.CredentialID && v("id") == binding.ID && v("credential_id") == binding.CredentialID && v("channel_id") == binding.ChannelID &&
		v("account_id") == binding.AccountID && v("operation_id") == binding.OperationID && boundAt.Equal(binding.BoundAt) && (!applied || row.OperationID == binding.OperationID)
}

func procurementCommandObservationMatches(command ProcurementCommandRow, s procurementCommandSnapshot, row ProcurementObservationRow) bool {
	if !s.valid || s.null {
		return false
	}
	got, valid := procurementCommandSnapshotObservation(s)
	return valid && got.ID == row.ID && got.AccountID == row.AccountID && got.OperationID == row.OperationID && got.Actor == row.Actor && got.Status == row.Status &&
		procurementAttemptOptionalEqual(got.Amount, row.Amount) && got.Currency == row.Currency && got.Source == row.Source && got.Semantic == row.Semantic &&
		got.ObservedAt.Equal(row.ObservedAt) && procurementCommandOptionalTimeEqual(got.ProviderAsOf, row.ProviderAsOf) && got.EvidenceRef == row.EvidenceRef && got.CreatedAt.Equal(row.CreatedAt) &&
		command.TargetID == row.AccountID && command.Actor == row.Actor && command.OperationID == row.OperationID
}

func procurementCommandOptionalTimeEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func procurementCommandAuditAction(action string) bool {
	return action == "procurement.command.applied" || action == "procurement.command.rejected"
}

func procurementCommandAuditMatches(command ProcurementCommandRow, audit ProcurementAuditRow) bool {
	if audit.Actor != command.Actor || audit.Target != command.OperationID || audit.Action != "procurement.command."+command.Status || audit.TenantScope != nil || audit.RequestID != "" {
		return false
	}
	values, null, valid := procurementCommandObject(audit.Diff, []string{"kind", "status"})
	return valid && !null && procurementCommandValue(values, "kind") == command.Kind && procurementCommandValue(values, "status") == command.Status
}

func procurementCommandShape(row ProcurementCommandRow, before, after procurementCommandSnapshot) bool {
	// State/reason contradictions remain observable even when a snapshot is
	// separately malformed. Only the unparseable document's null shape is skipped.
	validStateReason := false
	if row.Status == "applied" {
		validStateReason = row.Reason == "" && procurementAttemptStatus(row.Kind, "account_create", "account_configure", "credential_bind", "balance_observe")
	} else if row.Status == "rejected" {
		switch row.Kind {
		case "account_configure":
			validStateReason = procurementAttemptStatus(row.Reason, "not_found", "revision_conflict")
		case "credential_bind":
			validStateReason = procurementAttemptStatus(row.Reason, "not_found", "revision_conflict", "credential_revoked", "already_bound")
		case "balance_observe":
			validStateReason = row.Reason == "not_found"
		}
	}
	if !validStateReason {
		return false
	}
	if !before.valid || !after.valid {
		return true // SnapshotRows owns malformed documents, rather than double classifying their null shape.
	}
	if row.Status == "rejected" {
		if !after.null {
			return false
		}
		switch row.Kind {
		case "account_configure":
			return row.Reason == "not_found" && before.null || row.Reason == "revision_conflict" && !before.null
		case "credential_bind":
			return procurementAttemptStatus(row.Reason, "not_found", "revision_conflict", "credential_revoked") && before.null || row.Reason == "already_bound" && !before.null
		case "balance_observe":
			return row.Reason == "not_found" && before.null
		}
		return false
	}
	if row.Status != "applied" || row.Reason != "" || after.null {
		return false
	}
	if row.Kind == "account_configure" {
		if before.null || !procurementCommandSnapshotTimeEqual(before, after, "created_at") {
			return false
		}
		for _, key := range []string{"id", "held_amount", "spent_amount"} {
			if procurementCommandSnapshotValue(before, key) != procurementCommandSnapshotValue(after, key) {
				return false
			}
		}
		beforeRevision, _ := procurementAttemptPositiveInt64(procurementCommandSnapshotValue(before, "revision"))
		afterRevision, _ := procurementAttemptPositiveInt64(procurementCommandSnapshotValue(after, "revision"))
		changed := false
		for _, key := range []string{"label", "provider", "state", "limit_amount"} {
			changed = changed || procurementCommandSnapshotValue(before, key) != procurementCommandSnapshotValue(after, key)
		}
		if changed {
			beforeRevision = new(big.Int).Add(beforeRevision, big.NewInt(1))
		}
		return beforeRevision.Cmp(afterRevision) == 0
	}
	if !before.null {
		return false
	}
	if row.Kind == "account_create" {
		return procurementCommandSnapshotValue(after, "held_amount") == "0" && procurementCommandSnapshotValue(after, "spent_amount") == "0" && procurementCommandSnapshotValue(after, "revision") == "1"
	}
	return row.Kind == "credential_bind" || row.Kind == "balance_observe"
}

type procurementDecodedCommand struct {
	row           ProcurementCommandRow
	before, after procurementCommandSnapshot
}

// CheckProcurementCommandGraph checks only the immutable management receipts,
// binding/observation backlinks and their mandatory command audits.
func CheckProcurementCommandGraph(ctx context.Context, rows ProcurementCommandRows) (ProcurementCommandCheck, error) {
	if ctx == nil {
		return ProcurementCommandCheck{}, ErrProcurementCommandRowsInput
	}
	if ctx.Err() != nil {
		return ProcurementCommandCheck{}, procurementCommandContextError(ctx)
	}
	remaining := uint64(fundingSourceRowLimit)
	for _, count := range [...]int{len(rows.Users), len(rows.Accounts), len(rows.Bindings), len(rows.Commands), len(rows.Observations), len(rows.Audits)} {
		if uint64(count) > remaining {
			return ProcurementCommandCheck{}, ErrProcurementCommandRowsResource
		}
		remaining -= uint64(count)
	}
	w := procurementCommandWork{ctx: ctx}
	out := ProcurementCommandCheck{Scope: "procurement_command_observation_graph_v1", Scanned: ProcurementCommandCounts{
		Users: uint64(len(rows.Users)), Accounts: uint64(len(rows.Accounts)), Bindings: uint64(len(rows.Bindings)), Commands: uint64(len(rows.Commands)), Observations: uint64(len(rows.Observations)), Audits: uint64(len(rows.Audits))}}
	check := func(required, optional []string, extraValid bool) error {
		if err := w.tick(); err != nil {
			return err
		}
		valid, err := w.text(required, optional)
		if err != nil {
			return err
		}
		if !valid || !extraValid {
			out.Mismatches.InvalidTextRows++
		}
		return nil
	}
	for _, row := range rows.Users {
		if err := check([]string{row.ID}, nil, true); err != nil {
			return ProcurementCommandCheck{}, err
		}
	}
	for _, row := range rows.Accounts {
		if err := check([]string{row.ID, row.HeldAmount, row.SpentAmount}, nil, true); err != nil {
			return ProcurementCommandCheck{}, err
		}
	}
	for _, row := range rows.Bindings {
		if err := check([]string{row.ID, row.CredentialID, row.ChannelID, row.AccountID, row.OperationID}, nil, true); err != nil {
			return ProcurementCommandCheck{}, err
		}
	}
	for _, row := range rows.Commands {
		if err := check([]string{row.OperationID, row.Actor, row.Kind, row.TargetID, row.InputDigest, row.Status, row.Before, row.After}, []string{row.Reason}, procurementApprovalUUID(row.OperationID) && procurementCommandHex(row.InputDigest)); err != nil {
			return ProcurementCommandCheck{}, err
		}
		if !procurementAttemptStatus(row.Kind, "account_create", "account_configure", "credential_bind", "balance_observe") || !procurementAttemptStatus(row.Status, "applied", "rejected") ||
			!procurementAttemptStatus(row.Reason, "", "not_found", "revision_conflict", "already_bound", "credential_revoked") {
			out.Mismatches.InvalidEnumRows++
		}
	}
	for _, row := range rows.Observations {
		fields := []string{row.ID, row.AccountID, row.OperationID, row.Actor, row.Status, row.Currency, row.Source, row.Semantic, row.EvidenceRef}
		if row.Amount != nil {
			fields = append(fields, *row.Amount)
		}
		if err := check(fields, nil,
			procurementApprovalUUID(row.OperationID) && procurementCommandFiniteTime(row.ObservedAt) && procurementCommandFiniteTime(row.CreatedAt) && (row.ProviderAsOf == nil || procurementCommandFiniteTime(*row.ProviderAsOf)) &&
				procurementCommandMetadata(row.Source, 100, false) && procurementCommandMetadata(row.Semantic, 100, false) && procurementCommandMetadata(row.EvidenceRef, 256, false)); err != nil {
			return ProcurementCommandCheck{}, err
		}
		amountValid := row.Status != "known" && row.Amount == nil
		if row.Status == "known" && row.Amount != nil {
			_, amountValid = procurementAttemptInt64(*row.Amount)
		}
		if !amountValid {
			out.Mismatches.InvalidAmountRows++
		}
		if !procurementAttemptStatus(row.Status, "known", "unknown", "unsupported", "error") || row.Currency != "CNY" {
			out.Mismatches.InvalidEnumRows++
		}
	}
	for _, row := range rows.Audits {
		fields := []string{row.Actor, row.Action, row.Target, row.Diff, row.RequestID}
		if row.TenantScope != nil {
			fields = append(fields, *row.TenantScope)
		}
		if err := check([]string{row.ID}, fields, true); err != nil {
			return ProcurementCommandCheck{}, err
		}
	}

	users, accounts := make(map[string]struct{}, len(rows.Users)), make(map[string]struct{}, len(rows.Accounts))
	for _, row := range rows.Users {
		if err := w.tick(); err != nil {
			return ProcurementCommandCheck{}, err
		}
		if procurementApprovalSeen(users, row.ID, procurementAttemptValidText(row.ID)) {
			out.Mismatches.DuplicateUserRows++
		}
	}
	for _, row := range rows.Accounts {
		if err := w.tick(); err != nil {
			return ProcurementCommandCheck{}, err
		}
		if procurementApprovalSeen(accounts, row.ID, procurementAttemptValidText(row.ID)) {
			out.Mismatches.DuplicateAccountRows++
		}
	}
	bindings := make(map[string]ProcurementAttemptBindingRow, len(rows.Bindings))
	bindingIDs, bindingOperations := make(map[string]struct{}), make(map[string]struct{})
	canonicalBindings := make([]ProcurementAttemptBindingRow, 0, len(rows.Bindings))
	for _, row := range rows.Bindings {
		if err := w.tick(); err != nil {
			return ProcurementCommandCheck{}, err
		}
		duplicateID := procurementApprovalSeen(bindingIDs, row.ID, procurementAttemptValidText(row.ID))
		duplicateOperation := procurementApprovalSeen(bindingOperations, row.OperationID, procurementAttemptValidText(row.OperationID))
		if duplicateID {
			out.Mismatches.DuplicateBindingRows++
		}
		if duplicateOperation {
			out.Mismatches.DuplicateBindingOperationRows++
		}
		if duplicateID || duplicateOperation {
			continue
		}
		canonicalBindings = append(canonicalBindings, row)
		if procurementAttemptValidText(row.ID) && procurementAttemptValidText(row.OperationID) {
			bindings[row.ID] = row
		}
	}
	observations := make(map[string]ProcurementObservationRow, len(rows.Observations))
	observationIDs, observationOperations := make(map[string]struct{}), make(map[string]struct{})
	canonicalObservations := make([]ProcurementObservationRow, 0, len(rows.Observations))
	for _, row := range rows.Observations {
		if err := w.tick(); err != nil {
			return ProcurementCommandCheck{}, err
		}
		duplicateID := procurementApprovalSeen(observationIDs, row.ID, procurementAttemptValidText(row.ID))
		duplicateOperation := procurementApprovalSeen(observationOperations, row.OperationID, procurementAttemptValidText(row.OperationID))
		if duplicateID {
			out.Mismatches.DuplicateObservationRows++
		}
		if duplicateOperation {
			out.Mismatches.DuplicateObservationOperationRows++
		}
		if duplicateID || duplicateOperation {
			continue
		}
		canonicalObservations = append(canonicalObservations, row)
		if procurementAttemptValidText(row.ID) && procurementAttemptValidText(row.OperationID) {
			observations[row.ID] = row
		}
	}
	commands := make(map[string]procurementDecodedCommand, len(rows.Commands))
	commandIDs := make(map[string]struct{}, len(rows.Commands))
	canonicalCommands := make([]procurementDecodedCommand, 0, len(rows.Commands))
	for _, row := range rows.Commands {
		if err := w.tick(); err != nil {
			return ProcurementCommandCheck{}, err
		}
		if procurementApprovalSeen(commandIDs, row.OperationID, procurementApprovalUUID(row.OperationID)) {
			out.Mismatches.DuplicateCommandRows++
			continue
		}
		decoded := procurementDecodedCommand{row: row, before: procurementCommandDecode(row.Kind, row.Before), after: procurementCommandDecode(row.Kind, row.After)}
		canonicalCommands = append(canonicalCommands, decoded)
		if procurementApprovalUUID(row.OperationID) {
			commands[row.OperationID] = decoded
		}
	}
	audits := make(map[string]ProcurementAuditRow, len(rows.Audits))
	auditIDs, auditTargets := make(map[string]struct{}), make(map[string]struct{})
	canonicalAudits := make([]ProcurementAuditRow, 0, len(rows.Audits))
	for _, row := range rows.Audits {
		if err := w.tick(); err != nil {
			return ProcurementCommandCheck{}, err
		}
		duplicateID := procurementApprovalSeen(auditIDs, row.ID, procurementAttemptValidText(row.ID))
		duplicateTarget := false
		if procurementCommandAuditAction(row.Action) {
			duplicateTarget = procurementApprovalSeen(auditTargets, row.Target, procurementAttemptValidText(row.Target))
		}
		if duplicateID {
			out.Mismatches.DuplicateAuditRows++
		}
		if duplicateTarget {
			out.Mismatches.DuplicateCommandAuditTargetRows++
		}
		if duplicateID || duplicateTarget || !procurementCommandAuditAction(row.Action) {
			continue
		}
		canonicalAudits = append(canonicalAudits, row)
		if procurementAttemptValidText(row.ID) && procurementAttemptValidText(row.Target) {
			audits[row.Target] = row
		}
	}
	for _, command := range canonicalCommands {
		if err := w.tick(); err != nil {
			return ProcurementCommandCheck{}, err
		}
		row, before, after := command.row, command.before, command.after
		if !before.valid || !after.valid {
			out.Mismatches.SnapshotRows++
		}
		if !procurementCommandShape(row, before, after) {
			out.Mismatches.CommandShapeRows++
		}
		if _, exists := users[row.Actor]; !exists {
			out.Mismatches.CommandActorRows++
		}
		if row.Kind == "account_create" && row.Status == "applied" || row.Kind == "account_configure" && (row.Status == "applied" || row.Status == "rejected" && row.Reason == "revision_conflict") {
			_, exists := accounts[row.TargetID]
			snapshot := after
			if row.Status == "rejected" {
				snapshot = before
			}
			if !exists || snapshot.valid && !snapshot.null && procurementCommandSnapshotValue(snapshot, "id") != row.TargetID || row.Status == "applied" && row.Kind == "account_configure" && before.valid && !before.null && procurementCommandSnapshotValue(before, "id") != row.TargetID {
				out.Mismatches.CommandTargetRows++
			}
		}
		if row.Kind == "credential_bind" && row.Status == "applied" {
			binding, exists := bindings[procurementCommandSnapshotValue(after, "id")]
			if !exists || !procurementCommandBindingMatches(row, after, binding, true) {
				out.Mismatches.AppliedBindingRows++
			}
		}
		if row.Kind == "credential_bind" && row.Status == "rejected" && row.Reason == "already_bound" && before.valid && !before.null {
			binding, exists := bindings[procurementCommandSnapshotValue(before, "id")]
			if !exists || !procurementCommandBindingMatches(row, before, binding, false) {
				out.Mismatches.CommandTargetRows++
			}
		}
		if row.Kind == "balance_observe" && row.Status == "applied" {
			observation, exists := observations[procurementCommandSnapshotValue(after, "id")]
			if !exists || !procurementCommandObservationMatches(row, after, observation) {
				out.Mismatches.AppliedObservationRows++
			}
		}
		audit, exists := audits[row.OperationID]
		if !exists || !procurementCommandAuditMatches(row, audit) {
			out.Mismatches.CommandAuditRows++
		}
	}
	for _, binding := range canonicalBindings {
		if err := w.tick(); err != nil {
			return ProcurementCommandCheck{}, err
		}
		command, exists := commands[binding.OperationID]
		if !exists || command.row.Kind != "credential_bind" || command.row.Status != "applied" || !procurementCommandBindingMatches(command.row, command.after, binding, true) {
			out.Mismatches.BindingCommandRows++
		}
	}
	for _, observation := range canonicalObservations {
		if err := w.tick(); err != nil {
			return ProcurementCommandCheck{}, err
		}
		_, accountExists := accounts[observation.AccountID]
		_, actorExists := users[observation.Actor]
		command, exists := commands[observation.OperationID]
		if !accountExists || !actorExists || !exists || command.row.Kind != "balance_observe" || command.row.Status != "applied" || !procurementCommandObservationMatches(command.row, command.after, observation) {
			out.Mismatches.ObservationCommandRows++
		}
	}
	for _, audit := range canonicalAudits {
		if err := w.tick(); err != nil {
			return ProcurementCommandCheck{}, err
		}
		command, exists := commands[audit.Target]
		if !exists || !procurementCommandAuditMatches(command.row, audit) {
			out.Mismatches.AuditCommandRows++
		}
	}
	if ctx.Err() != nil {
		return ProcurementCommandCheck{}, procurementCommandContextError(ctx)
	}
	out.Balanced = out.Mismatches == (ProcurementCommandMismatches{})
	return out, nil
}
