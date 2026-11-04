package procurementexecution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	observeAccountActor   = "40000000-0000-0000-0000-000000000001"
	observeAccountOp      = "40000000-0000-0000-0000-000000000002"
	observeAccountID      = "40000000-0000-0000-0000-000000000003"
	observeAccountRoot    = "40000000-0000-0000-0000-000000000004"
	observeAccountSource  = "40000000-0000-0000-0000-000000000005"
	observeAccountJournal = "40000000-0000-0000-0000-000000000006"
	observeAccountAudit   = "40000000-0000-0000-0000-000000000007"
	observeAccountOldOp   = "40000000-0000-0000-0000-000000000008"
	observeAccountOldRoot = "40000000-0000-0000-0000-000000000009"
	observeAccountOldSrc  = "40000000-0000-0000-0000-000000000010"
	observeAccountOldJ    = "40000000-0000-0000-0000-000000000011"
	observeAccountXID     = "18446744073709551614"
)

func TestObserverAccountDigestOriginalJSONBytes(t *testing.T) {
	in := AccountInput{ActorID: observeAccountActor, OperationID: observeAccountOp, Label: "a<>&\u2028\u2029\"\\\n", Provider: "p\t", State: "active", LimitAmount: math.MaxInt64}
	create, _ := NewCreateAccount("schema", in)
	configure, _ := NewConfigureAccount("schema", ConfigurationInput{AccountInput: in, AccountID: observeAccountID, ExpectedRevision: math.MaxInt64})
	for _, tc := range []struct {
		name string
		call Invocation
		json string
	}{
		{"create", create, `{"Kind":"account_create","Target":"","Payload":{"operation_id":"40000000-0000-0000-0000-000000000002","label":"a\u003c\u003e\u0026\u2028\u2029\"\\\n","provider":"p\t","state":"active","limit_amount":"9223372036854775807"}}`},
		{"configure", configure, `{"Kind":"account_configure","Target":"40000000-0000-0000-0000-000000000003","Payload":{"operation_id":"40000000-0000-0000-0000-000000000002","expected_revision":"9223372036854775807","label":"a\u003c\u003e\u0026\u2028\u2029\"\\\n","provider":"p\t","state":"active","limit_amount":"9223372036854775807"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sum := sha256.Sum256([]byte(tc.json))
			got, err := observerCommandDigest(tc.call)
			if err != nil || got != hex.EncodeToString(sum[:]) {
				t.Fatal("digest differs from original fixed JSON bytes", err)
			}
			changedActor := tc.call
			changedActor.account.ActorID = observeAccountOldOp
			other, err := observerCommandDigest(changedActor)
			if err != nil || other != got {
				t.Fatal("actor was incorrectly included in original digest")
			}
			changedOperation := tc.call
			changedOperation.account.OperationID = observeAccountOldOp
			other, err = observerCommandDigest(changedOperation)
			if err != nil || other == got {
				t.Fatal("operation identity was dropped from original digest")
			}
		})
	}
	if _, err := observerCommandDigest(Invocation{}); err == nil {
		t.Fatal("unknown root received an account digest")
	}
}

func TestObserverAccountProjectionContract(t *testing.T) {
	for _, tc := range []struct {
		query observerQuery
		names string
		oids  []uint32
	}{
		{observerCommandQuery, "operation_id,actor,kind,target_id,input_digest,status,reason,before_state,after_state,created_at,pp_source_id,pp_root_id,pp_root_step,pp_created_xid,pp_account_journal_id", []uint32{25, 25, 25, 25, 25, 25, 25, 3802, 3802, 1184, 25, 25, 20, 25, 25}},
		{observerCommandSourceQuery, "id,root_id,step_no,step_kind,created_xid,created_at,operation_id,actor_id,actor_role,actor_status,kind,input_target_id,target_id,input_label,input_provider,input_state,input_limit_amount,input_digest,command_status,command_reason,command_created_at,audit_id,input_expected_revision,journal_id", []uint32{25, 25, 20, 25, 25, 1184, 25, 25, 25, 25, 25, 25, 25, 25, 25, 25, 20, 25, 25, 25, 1184, 25, 20, 25}},
		{observerCommandAuditQuery, "id,actor,tenant_scope,action,target,sanitized_diff,request_id,created_at,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_source_role,pp_created_xid", []uint32{25, 25, 25, 25, 25, 3802, 25, 1184, 25, 20, 25, 25, 25, 25}},
		{observerAccountOriginQuery, "account_id,version,create_operation_id,command_source_id,root_id,step_no,step_kind,journal_id,created_xid,created_at", []uint32{25, 21, 25, 25, 25, 20, 25, 25, 25, 1184}},
	} {
		t.Run(tc.query.table, func(t *testing.T) {
			names := strings.Split(tc.names, ",")
			if len(tc.query.fields) != len(names) || len(names) != len(tc.oids) {
				t.Fatal("fixed account projection size changed")
			}
			for n, field := range tc.query.fields {
				if field.name != names[n] || field.oid != tc.oids[n] {
					t.Fatal("fixed account projection identity/type/order changed")
				}
				if strings.HasSuffix(field.name, "xid") && field.cast != "text" {
					t.Fatal("xid8 lost its unsigned-preserving TEXT projection")
				}
			}
		})
	}
}

func observeAccountSnapshotJSON(at, held, spent, revision, label, limit string) []byte {
	raw, _ := json.Marshal(map[string]string{"id": observeAccountID, "label": label, "provider": "provider", "state": "active", "limit_amount": limit, "held_amount": held, "spent_amount": spent, "revision": revision, "created_at": at})
	return raw
}

func TestObserverAccountSnapshotOriginalInstantAndStrictShape(t *testing.T) {
	const utc = "2026-11-04T01:02:03.123456Z"
	large := strings.Repeat("9", 100)
	want, null, err := observerSnapshot(observeAccountSnapshotJSON(utc, large, "0", "1", "label", "0"))
	if err != nil || null || want.HeldAmount != large {
		t.Fatal("NUMERIC100 snapshot was narrowed", err)
	}
	for _, at := range []string{utc, "2026-11-04T09:02:03.123456+08:00", "2026-11-03T20:02:03.123456-05:00", "2026-11-04T01:02:03.123456000+00:00"} {
		got, null, err := observerSnapshot(observeAccountSnapshotJSON(at, large, "0", "1", "label", "0"))
		if err != nil || null || got != want {
			t.Fatalf("historic zone spelling changed same instant: %s %v", at, err)
		}
	}
	if _, null, err := observerSnapshot([]byte(" null \n")); err != nil || !null {
		t.Fatal("JSON null was not preserved")
	}
	base := string(observeAccountSnapshotJSON(utc, large, "0", "1", "label", "0"))
	for name, raw := range map[string][]byte{
		"sql_null":           nil,
		"empty_object":       []byte("{}"),
		"extra":              []byte(strings.TrimSuffix(base, "}") + `,"extra":"x"}`),
		"duplicate":          []byte(strings.TrimSuffix(base, "}") + `,"id":"` + observeAccountID + `"}`),
		"number":             []byte(strings.Replace(base, `"revision":"1"`, `"revision":1`, 1)),
		"noncanonical_money": observeAccountSnapshotJSON(utc, "01", "0", "1", "label", "0"),
		"overflow_money":     observeAccountSnapshotJSON(utc, large+"9", "0", "1", "label", "0"),
		"submicrosecond":     observeAccountSnapshotJSON("2026-11-04T01:02:03.123456001Z", large, "0", "1", "label", "0"),
		"comma_fraction":     observeAccountSnapshotJSON("2026-11-04T01:02:03,123456Z", large, "0", "1", "label", "0"),
		"infinity":           observeAccountSnapshotJSON("infinity", large, "0", "1", "label", "0"),
		"bad_utf8":           append([]byte(base), 0xff),
		"trailing":           []byte(base + " null"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := observerSnapshot(raw); err == nil {
				t.Fatal("invalid immutable snapshot accepted")
			}
		})
	}
}

type observeAccountRows struct {
	fields    []observerField
	data      [][]any
	index     int
	err, late error
	closed    bool
}

func (r *observeAccountRows) FieldDescriptions() []pgconn.FieldDescription {
	out := make([]pgconn.FieldDescription, len(r.fields))
	for n, f := range r.fields {
		out[n] = pgconn.FieldDescription{Name: f.name, DataTypeOID: f.oid}
	}
	return out
}
func (r *observeAccountRows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	r.index++
	if r.index >= len(r.data) {
		r.err = r.late
		return false
	}
	return true
}
func (r *observeAccountRows) Scan(dest ...any) error {
	if len(dest) != len(r.fields) || len(r.data[r.index]) != len(dest) {
		r.err = errors.New("observer test scan count")
		return r.err
	}
	m := pgtype.NewMap()
	for n, field := range r.fields {
		encoded, err := m.Encode(field.oid, pgtype.TextFormatCode, r.data[r.index][n], make([]byte, 0, 64))
		if err == nil {
			err = m.Scan(field.oid, pgtype.TextFormatCode, encoded, dest[n])
		}
		if err != nil {
			r.err = err
			return err
		}
	}
	return nil
}
func (r *observeAccountRows) Err() error { return r.err }
func (r *observeAccountRows) Close()     { r.closed = true; r.err = combine(r.err, r.late) }

type observeAccountResult struct {
	name             string
	args             []any
	fields           []observerField
	row              observerRow
	err              error
	empty, duplicate bool
	rows             *observeAccountRows
}
type observeAccountTx struct {
	t       *testing.T
	schema  string
	results []*observeAccountResult
	at      int
}

func (t *observeAccountTx) exec(context.Context, string) error {
	t.t.Fatal("account probe executed a non-query statement")
	return nil
}
func (t *observeAccountTx) commit(context.Context) error {
	t.t.Fatal("account probe committed")
	return nil
}
func (t *observeAccountTx) rollback(context.Context) error {
	t.t.Fatal("account probe owns no lifecycle")
	return nil
}
func (t *observeAccountTx) query(_ context.Context, sql string, args ...any) (Rows, error) {
	t.t.Helper()
	if t.at >= len(t.results) {
		t.t.Fatalf("unexpected account probe query %s", sql)
	}
	r := t.results[t.at]
	t.at++
	if !strings.Contains(sql, pgx.Identifier{t.schema, r.name}.Sanitize()) || !reflect.DeepEqual(args, r.args) {
		t.t.Fatalf("account query changed fixed target/arguments: step=%d target=%s args=%v", t.at, r.name, args)
	}
	if r.err != nil {
		return nil, r.err
	}
	var data [][]any
	if !r.empty {
		values := make([]any, len(r.fields))
		for n, f := range r.fields {
			values[n] = r.row[f.name]
		}
		data = append(data, values)
		if r.duplicate {
			data = append(data, append([]any(nil), values...))
		}
	}
	r.rows = &observeAccountRows{fields: r.fields, data: data, index: -1}
	return r.rows, nil
}

// Journal metadata is written independently from the production projection;
// all 58 original columns remain present, including forbidden NULL branches.
func observeAccountJournalFields() []observerField {
	fields := []observerField{obsText("id", false), obsText("account_id", false), obsInt8("ordinal", false), obsText("previous_id", true), obsInt8("previous_ordinal", false), obsText("root_id", false), obsInt8("step_no", false), obsText("step_kind", false), obsXID("created_xid", false), obsTime("created_at", false), obsText("kind", false), obsText("source_kind", false), obsText("source_id", false)}
	for _, prefix := range []string{"before_", "after_"} {
		nullable := prefix == "before_"
		fields = append(fields, obsText(prefix+"label", nullable), obsText(prefix+"provider", nullable), obsText(prefix+"state", nullable), obsInt8(prefix+"limit_amount", nullable), obsInt8(prefix+"configuration_revision", nullable), obsMoney(prefix+"held_amount", nullable), obsMoney(prefix+"spent_amount", nullable), obsInt8(prefix+"blocking_count", nullable))
	}
	fields = append(fields, obsText("attempt_id", true), obsText("binding_id", true))
	for _, prefix := range []string{"before_", "after_"} {
		fields = append(fields, obsInt8(prefix+"reservation_revision", true), obsText(prefix+"reservation_state", true), obsMoney(prefix+"reservation_held_amount", true), obsMoney(prefix+"reservation_spent_amount", true), obsBool(prefix+"reservation_anomaly", true))
	}
	return append(fields, obsInt8("input_bound", true), obsInt8("output_bound", true), obsInt8("upper_amount", true), obsText("cost_version_id", true), obsInt8("cost_input_rate", true), obsInt8("cost_output_rate", true), obsText("cost_basis", true), obsInt8("gate_extra", true), obsTime("gate_checked_at", true), obsTime("pb_decision_at", true), obsText("pause_event_id", true), obsText("command_source_id", true), obsText("transition_id", true), obsText("delivery_source_id", true), obsText("command_operation_id", true), obsText("procurement_event_id", true), obsText("dispatch_attempt_id", true))
}

func observeAccountFixture(t *testing.T, branch string) (*ObservationHandle, *observeAccountTx) {
	t.Helper()
	at := time.Date(2026, 11, 4, 1, 2, 3, 123456000, time.UTC)
	commandAt := at.Add(time.Hour)
	in := AccountInput{ActorID: observeAccountActor, OperationID: observeAccountOp, Label: "label", Provider: "provider", State: "active", LimitAmount: 1000}
	call, _ := NewCreateAccount(`schema.with"quote`, in)
	before := []byte("null")
	after := observeAccountSnapshotJSON(at.Format(time.RFC3339Nano), "0", "0", "1", "label", "1000")
	receipt := AccountReceipt{Outcome: "applied", OperationID: in.OperationID, Actor: in.ActorID, Kind: "account_create", TargetID: observeAccountID, Status: "applied", Before: before, After: after, CreatedAt: commandAt, RootID: observeAccountRoot, AccountJournalID: new(string)}
	*receipt.AccountJournalID = observeAccountJournal
	if branch != "create" {
		call, _ = NewConfigureAccount(`schema.with"quote`, ConfigurationInput{AccountInput: in, AccountID: observeAccountID, ExpectedRevision: 1})
		receipt.Kind = "account_configure"
		receipt.Before = observeAccountSnapshotJSON(at.Format(time.RFC3339Nano), "123", "45", "1", "old-label", "900")
		receipt.After = observeAccountSnapshotJSON(at.Format(time.RFC3339Nano), "123", "45", "2", "label", "1000")
		if branch == "noop" {
			call.account.Label = "old-label"
			call.account.LimitAmount = 900
			receipt.After = cloneBytes(receipt.Before)
		}
		if branch == "conflict" {
			call.expected = 2
			receipt.Status = "rejected"
			receipt.Reason = "revision_conflict"
			receipt.After = []byte("null")
		}
		if branch == "not_found" {
			receipt.Status = "rejected"
			receipt.Reason = "not_found"
			receipt.Before = []byte("null")
			receipt.After = []byte("null")
			receipt.AccountJournalID = nil
		}
		call, _ = NewConfigureAccount(`schema.with"quote`, ConfigurationInput{AccountInput: call.account, AccountID: observeAccountID, ExpectedRevision: call.expected})
	}
	digest, err := observerCommandDigest(call)
	if err != nil {
		t.Fatal(err)
	}
	journalID := observerOptionalText(receipt.AccountJournalID)
	command := observerRow{"operation_id": receipt.OperationID, "actor": receipt.Actor, "kind": receipt.Kind, "target_id": receipt.TargetID, "input_digest": digest, "status": receipt.Status, "reason": receipt.Reason, "before_state": cloneBytes(receipt.Before), "after_state": cloneBytes(receipt.After), "created_at": commandAt, "pp_source_id": observeAccountSource, "pp_root_id": observeAccountRoot, "pp_root_step": int64(1), "pp_created_xid": observeAccountXID, "pp_account_journal_id": journalID}
	source := observerRow{"id": observeAccountSource, "root_id": observeAccountRoot, "step_no": int64(1), "step_kind": "account_command", "created_xid": observeAccountXID, "created_at": commandAt.Add(time.Second), "operation_id": receipt.OperationID, "actor_id": receipt.Actor, "actor_role": "admin", "actor_status": "active", "kind": receipt.Kind, "input_target_id": call.target, "target_id": receipt.TargetID, "input_label": call.account.Label, "input_provider": call.account.Provider, "input_state": call.account.State, "input_limit_amount": call.account.LimitAmount, "input_digest": digest, "command_status": receipt.Status, "command_reason": receipt.Reason, "command_created_at": commandAt, "audit_id": observeAccountAudit, "input_expected_revision": nil, "journal_id": journalID}
	if branch != "create" {
		source["input_expected_revision"] = call.expected
	}
	root := observerRow{"id": observeAccountRoot, "entry_kind": receipt.Kind, "scope_kind": "account_command", "tenant_id": nil, "request_id": nil, "attempt_id": nil, "account_target_id": observeAccountID, "created_xid": observeAccountXID, "session_role_oid": int64(7000), "step_count": int64(1), "semantic_result": receipt.Status}
	step := observerRow{"root_id": observeAccountRoot, "step_no": int64(1), "kind": "account_command", "source_id": observeAccountSource, "created_xid": observeAccountXID, "d_account_command": observeAccountSource, "d_attempt_birth": nil, "d_procurement_transition": nil}
	diff, _ := json.Marshal(map[string]string{"kind": receipt.Kind, "status": receipt.Status})
	audit := observerRow{"id": observeAccountAudit, "actor": receipt.Actor, "tenant_scope": nil, "action": "procurement.command." + receipt.Status, "target": receipt.OperationID, "sanitized_diff": diff, "request_id": "", "created_at": commandAt.Add(2 * time.Second), "pp_root_id": observeAccountRoot, "pp_root_step": int64(1), "pp_source_kind": "account_command", "pp_source_id": observeAccountSource, "pp_source_role": "account_command", "pp_created_xid": observeAccountXID}
	tx := &observeAccountTx{t: t, schema: `schema.with"quote`}
	tx.results = []*observeAccountResult{
		{name: "procurement_commands", args: []any{receipt.OperationID}, fields: observerCommandQuery.fields, row: command},
		{name: "pp_account_command_sources", args: []any{observeAccountSource}, fields: observerCommandSourceQuery.fields, row: source},
		{name: "pp_operation_roots", args: []any{observeAccountRoot}, fields: []observerField{obsText("id", false), obsText("entry_kind", false), obsText("scope_kind", false), obsText("tenant_id", true), obsText("request_id", true), obsText("attempt_id", true), obsText("account_target_id", true), obsXID("created_xid", false), obsInt8("session_role_oid", false), obsInt8("step_count", false), obsText("semantic_result", false)}, row: root},
		{name: "pp_root_steps", args: []any{observeAccountRoot}, fields: []observerField{obsText("root_id", false), obsInt8("step_no", false), obsText("kind", false), obsText("source_id", false), obsXID("created_xid", false), obsText("d_account_command", true), obsText("d_attempt_birth", true), obsText("d_procurement_transition", true)}, row: step},
		{name: "audit_events", args: []any{observeAccountAudit}, fields: observerCommandAuditQuery.fields, row: audit},
	}
	if branch != "not_found" {
		journal := observerRow{"id": observeAccountJournal, "account_id": observeAccountID, "ordinal": int64(1), "previous_id": nil, "previous_ordinal": int64(0), "root_id": observeAccountRoot, "step_no": int64(1), "step_kind": "account_command", "created_xid": observeAccountXID, "created_at": commandAt, "kind": "create", "source_kind": "command", "source_id": receipt.OperationID, "after_label": "label", "after_provider": "provider", "after_state": "active", "after_limit_amount": int64(1000), "after_configuration_revision": int64(1), "after_held_amount": "0", "after_spent_amount": "0", "after_blocking_count": int64(0), "command_source_id": observeAccountSource, "command_operation_id": receipt.OperationID}
		origin := observerRow{"account_id": observeAccountID, "version": int16(1), "create_operation_id": receipt.OperationID, "command_source_id": observeAccountSource, "root_id": observeAccountRoot, "step_no": int64(1), "step_kind": "account_command", "journal_id": observeAccountJournal, "created_xid": observeAccountXID, "created_at": at}
		if branch != "create" {
			journal["ordinal"] = int64(2)
			journal["previous_id"] = observeAccountOldJ
			journal["previous_ordinal"] = int64(1)
			journal["kind"] = "configure"
			for _, prefix := range []string{"before_", "after_"} {
				journal[prefix+"label"] = "old-label"
				journal[prefix+"provider"] = "provider"
				journal[prefix+"state"] = "active"
				journal[prefix+"limit_amount"] = int64(900)
				journal[prefix+"configuration_revision"] = int64(1)
				journal[prefix+"held_amount"] = "123"
				journal[prefix+"spent_amount"] = "45"
				journal[prefix+"blocking_count"] = int64(0)
			}
			if branch == "changed" {
				journal["after_label"] = "label"
				journal["after_limit_amount"] = int64(1000)
				journal["after_configuration_revision"] = int64(2)
			}
			if branch == "conflict" {
				journal["kind"] = "configure_rejected"
			}
			origin["create_operation_id"] = observeAccountOldOp
			origin["command_source_id"] = observeAccountOldSrc
			origin["root_id"] = observeAccountOldRoot
			origin["journal_id"] = observeAccountOldJ
			origin["created_xid"] = "99"
		}
		tx.results = append(tx.results, &observeAccountResult{name: "pp_account_journal", args: []any{observeAccountJournal}, fields: observeAccountJournalFields(), row: journal}, &observeAccountResult{name: "pp_account_origins", args: []any{observeAccountID}, fields: observerAccountOriginQuery.fields, row: origin})
	}
	for _, v := range []struct {
		name string
		args []any
	}{{"pp_validate_original", []any{"procurement_command", receipt.OperationID, nil}}, {"pp_validate_root", []any{observeAccountRoot}}} {
		tx.results = append(tx.results, &observeAccountResult{name: v.name, args: v.args, fields: []observerField{{name: v.name, oid: observerVoidOID}}, row: observerRow{v.name: nil}})
	}
	return &ObservationHandle{schema: tx.schema, invocation: call, account: receipt}, tx
}

func TestObserverAccountAllImmutableBranchesAndReplay(t *testing.T) {
	for _, branch := range []string{"create", "changed", "noop", "conflict", "not_found"} {
		for _, outcome := range []string{"applied", "replay"} {
			t.Run(branch+"/"+outcome, func(t *testing.T) {
				h, tx := observeAccountFixture(t, branch)
				h.account.Outcome = outcome
				p := &observationProbe{ctx: context.Background(), tx: tx, schema: tx.schema}
				if err := p.account(h); err != nil {
					t.Fatal("matching immutable account receipt rejected", err)
				}
				if tx.at != len(tx.results) {
					t.Fatal("account skipped required query or validator")
				}
				for _, result := range tx.results {
					if result.rows == nil || !result.rows.closed {
						t.Fatal("account left rows unclosed")
					}
				}
			})
		}
	}
}

func TestObserverAccountRejectsWrongOriginalAndIncompleteRelations(t *testing.T) {
	for _, tc := range []struct {
		name, branch, table, field string
		value                      any
		empty, duplicate           bool
	}{
		{"absent", "create", "procurement_commands", "", nil, true, false},
		{"wrong_digest", "create", "procurement_commands", "input_digest", "old-digest", false, false},
		{"wrong_actor", "create", "procurement_commands", "actor", observeAccountOldOp, false, false},
		{"wrong_root", "create", "procurement_commands", "pp_root_id", observeAccountOldRoot, false, false},
		{"missing_source_pointer", "create", "procurement_commands", "pp_source_id", nil, false, false},
		{"missing_step_pointer", "create", "procurement_commands", "pp_root_step", nil, false, false},
		{"missing_journal_pointer", "create", "procurement_commands", "pp_account_journal_id", nil, false, false},
		{"missing_source", "create", "pp_account_command_sources", "", nil, true, false},
		{"duplicate_source", "create", "pp_account_command_sources", "", nil, false, true},
		{"source_input", "create", "pp_account_command_sources", "input_label", "different", false, false},
		{"source_role", "create", "pp_account_command_sources", "actor_role", "finance_operator", false, false},
		{"source_missing_journal", "create", "pp_account_command_sources", "journal_id", nil, false, false},
		{"unexpected_expected_revision", "create", "pp_account_command_sources", "input_expected_revision", int64(1), false, false},
		{"wrong_scope", "create", "pp_operation_roots", "tenant_id", observeAccountOldOp, false, false},
		{"wrong_step_source", "create", "pp_root_steps", "source_id", observeAccountOldSrc, false, false},
		{"cross_domain_step", "create", "pp_root_steps", "d_attempt_birth", observeAccountID, false, false},
		{"wrong_audit_target", "create", "audit_events", "target", observeAccountOldOp, false, false},
		{"missing_audit", "create", "audit_events", "", nil, true, false},
		{"audit_extra", "create", "audit_events", "sanitized_diff", []byte(`{"kind":"account_create","status":"applied","extra":"x"}`), false, false},
		{"audit_duplicate", "create", "audit_events", "sanitized_diff", []byte(`{"kind":"account_create","status":"applied","kind":"account_create"}`), false, false},
		{"wrong_journal_source", "create", "pp_account_journal", "command_source_id", observeAccountOldSrc, false, false},
		{"forbidden_attempt", "create", "pp_account_journal", "attempt_id", observeAccountID, false, false},
		{"forbidden_quote", "create", "pp_account_journal", "cost_input_rate", int64(0), false, false},
		{"wrong_origin", "create", "pp_account_origins", "root_id", observeAccountOldRoot, false, false},
		{"wrong_account_time", "create", "pp_account_origins", "created_at", time.Date(2026, 11, 4, 1, 2, 4, 0, time.UTC), false, false},
		{"conflict_after_is_not_command_after", "conflict", "pp_account_journal", "after_label", "different", false, false},
		{"noop_money", "noop", "pp_account_journal", "after_held_amount", "124", false, false},
		{"not_found_journal", "not_found", "procurement_commands", "pp_account_journal_id", observeAccountJournal, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, tx := observeAccountFixture(t, tc.branch)
			for _, result := range tx.results {
				if result.name == tc.table {
					result.empty = tc.empty
					result.duplicate = tc.duplicate
					if tc.field != "" {
						result.row[tc.field] = tc.value
					}
				}
			}
			p := &observationProbe{ctx: context.Background(), tx: tx, schema: tx.schema}
			if err := p.account(h); err == nil {
				t.Fatal("unmatched/incomplete account history accepted")
			}
			for _, result := range tx.results {
				if result.rows != nil && !result.rows.closed {
					t.Fatal("failure retained rows")
				}
			}
		})
	}
}

func TestObserverAccountMissingIsNotUnsupportedOrInvariant(t *testing.T) {
	for _, tc := range []struct {
		name, table, field string
		reason             ObservationReason
	}{
		{"missing_command", "procurement_commands", "", ReasonReceiptAbsent},
		{"missing_source", "pp_account_command_sources", "", ReasonReceiptIncomplete},
		{"partial_command", "procurement_commands", "pp_root_id", ReasonReceiptIncomplete},
		{"partial_journal_pointer", "procurement_commands", "pp_account_journal_id", ReasonReceiptIncomplete},
		{"partial_source", "pp_account_command_sources", "journal_id", ReasonReceiptIncomplete},
		{"missing_audit", "audit_events", "", ReasonReceiptIncomplete},
		{"missing_journal", "pp_account_journal", "", ReasonReceiptIncomplete},
		{"missing_origin", "pp_account_origins", "", ReasonReceiptIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, tx := observeAccountFixture(t, "create")
			for _, result := range tx.results {
				if result.name == tc.table {
					if tc.field == "" {
						result.empty = true
					} else {
						result.row[tc.field] = nil
					}
				}
			}
			p := &observationProbe{ctx: context.Background(), tx: tx, schema: tx.schema}
			err := p.account(h)
			var pg *pgconn.PgError
			if err == nil || observationFailureReason(err) != tc.reason || errors.As(err, &pg) {
				t.Fatal("missing evidence manufactured a database legacy/invariant cause", err)
			}
		})
	}
}

func TestObserverAccountOriginalTimeZoneAndValidatorCauses(t *testing.T) {
	t.Run("immutable_zone_spelling", func(t *testing.T) {
		h, tx := observeAccountFixture(t, "changed")
		for _, name := range []string{"before_state", "after_state"} {
			raw := tx.results[0].row[name].([]byte)
			tx.results[0].row[name] = []byte(strings.ReplaceAll(string(raw), "2026-11-04T01:02:03.123456Z", "2026-11-04T09:02:03.123456+08:00"))
		}
		p := &observationProbe{ctx: context.Background(), tx: tx, schema: tx.schema}
		if err := p.account(h); err != nil {
			t.Fatal("historic original time spelling rejected", err)
		}
	})
	for _, state := range []string{"PP902", "PP906", "42501", "57014"} {
		for _, validator := range []string{"pp_validate_original", "pp_validate_root"} {
			t.Run(state+"/"+validator, func(t *testing.T) {
				h, tx := observeAccountFixture(t, "create")
				cause := &pgconn.PgError{Code: state, Message: "PRIVATE_PG_CAUSE"}
				for _, result := range tx.results {
					if result.name == validator {
						result.err = cause
					}
				}
				p := &observationProbe{ctx: context.Background(), tx: tx, schema: tx.schema}
				err := p.account(h)
				if !errors.Is(err, cause) {
					t.Fatal("observer lost real validator cause", err)
				}
				if validator == "pp_validate_original" && tx.at == len(tx.results) {
					t.Fatal("continued querying after aborted validator")
				}
			})
		}
	}
}

func TestObserverAccountFixedMetadataAndNoMutableAccountRead(t *testing.T) {
	for _, q := range []observerQuery{observerCommandQuery, observerCommandSourceQuery, observerCommandAuditQuery, observerAccountOriginQuery} {
		if q.table == "procurement_accounts" {
			t.Fatal("immutable receipt reads current account")
		}
		for n := range q.fields {
			t.Run(fmt.Sprintf("%s/%s", q.table, q.fields[n].name), func(t *testing.T) {
				h, tx := observeAccountFixture(t, "create")
				for _, result := range tx.results {
					if result.name == q.table {
						result.fields = append([]observerField(nil), result.fields...)
						result.fields[n].oid = pgtype.ByteaOID
					}
				}
				p := &observationProbe{ctx: context.Background(), tx: tx, schema: tx.schema}
				if err := p.account(h); err == nil {
					t.Fatal("wrong original column OID accepted")
				}
			})
		}
	}
}
