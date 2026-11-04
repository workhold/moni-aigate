package procurementexecution_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
)

const (
	actorID     = "10000000-0000-0000-0000-000000000001"
	operationID = "10000000-0000-0000-0000-000000000002"
	accountID   = "10000000-0000-0000-0000-000000000003"
	rootID      = "10000000-0000-0000-0000-000000000004"
	journalID   = "10000000-0000-0000-0000-000000000005"
	attemptID   = "10000000-0000-0000-0000-000000000006"
)

var instant = time.Date(2026, 11, 4, 8, 1, 2, 123456000, time.UTC)

// codecRows uses pgx's actual text codecs for Scan, including **string NULL
// behavior and pgtype.Timestamptz infinity. Like pgx, Scan failures become Err.
type codecRows struct {
	oids                                []uint32
	fields                              []pgconn.FieldDescription
	data                                [][]any
	index                               int
	err, scanErr, terminalErr, closeErr error
	closed                              bool
}

func newRows(oids []uint32, data ...[]any) *codecRows {
	names := []string{"outcome", "root_id", "source_id", "attempt_id", "request_id", "sequence", "delivery_state", "cost_status"}
	if len(oids) == 12 {
		names = []string{"outcome", "operation_id", "actor", "kind", "target_id", "status", "reason", "before_state", "after_state", "created_at", "root_id", "account_journal_id"}
	}
	fields := make([]pgconn.FieldDescription, len(oids))
	for n, oid := range oids {
		name := "unexpected"
		if n < len(names) {
			name = names[n]
		}
		fields[n] = pgconn.FieldDescription{Name: name, DataTypeOID: oid}
	}
	return &codecRows{oids: append([]uint32(nil), oids...), fields: fields, data: data, index: -1}
}
func (r *codecRows) FieldDescriptions() []pgconn.FieldDescription { return r.fields }
func (r *codecRows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	r.index++
	if r.index >= len(r.data) {
		r.err = r.terminalErr
		return false
	}
	return true
}
func (r *codecRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		r.err = r.scanErr
		return r.scanErr
	}
	values := r.data[r.index]
	if len(values) != len(dest) || len(r.oids) != len(dest) {
		r.err = errors.New("scan column count mismatch")
		return r.err
	}
	m := pgtype.NewMap()
	for n, value := range values {
		// A non-nil buffer preserves TEXT '' as a zero-length value rather
		// than confusing its bytes with SQL NULL in this in-memory transport.
		encoded, err := m.Encode(r.oids[n], pgtype.TextFormatCode, value, make([]byte, 0, 64))
		if err == nil {
			err = m.Scan(r.oids[n], pgtype.TextFormatCode, encoded, dest[n])
		}
		if err != nil {
			r.err = err
			return err
		}
	}
	return nil
}
func (r *codecRows) Err() error { return r.err }
func (r *codecRows) Close() {
	r.closed = true
	if r.closeErr != nil {
		if r.err == nil {
			r.err = r.closeErr
		} else {
			r.err = errors.Join(r.err, r.closeErr)
		}
	}
}

var attemptOIDs = []uint32{pgtype.TextOID, pgtype.TextOID, pgtype.TextOID, pgtype.TextOID, pgtype.TextOID, pgtype.Int4OID, pgtype.TextOID, pgtype.TextOID}
var accountOIDs = []uint32{pgtype.TextOID, pgtype.TextOID, pgtype.TextOID, pgtype.TextOID, pgtype.TextOID, pgtype.TextOID, pgtype.TextOID, pgtype.JSONBOID, pgtype.JSONBOID, pgtype.TimestamptzOID, pgtype.TextOID, pgtype.TextOID}

func attemptCall(t *testing.T) procurementexecution.Invocation {
	t.Helper()
	call, err := procurementexecution.NewRecordAttempt("schema", procurementexecution.AttemptInput{RequestID: "raw request", Sequence: 2})
	if err != nil {
		t.Fatal(err)
	}
	return call
}
func attemptApplied() []any {
	return []any{"applied", rootID, attemptID, attemptID, "raw request", int32(2), "prepared", "unknown"}
}
func accountInput() procurementexecution.AccountInput {
	return procurementexecution.AccountInput{ActorID: actorID, OperationID: operationID, Label: "label", Provider: "provider", State: "active", LimitAmount: 900}
}
func accountCall(t *testing.T, configure bool, revision int64) procurementexecution.Invocation {
	t.Helper()
	var call procurementexecution.Invocation
	var err error
	if configure {
		call, err = procurementexecution.NewConfigureAccount("schema", procurementexecution.ConfigurationInput{AccountInput: accountInput(), AccountID: accountID, ExpectedRevision: revision})
	} else {
		call, err = procurementexecution.NewCreateAccount("schema", accountInput())
	}
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func snapshot(held, spent, revision, label string) []byte {
	b, _ := json.Marshal(map[string]string{"id": accountID, "label": label, "provider": "provider", "state": "active", "limit_amount": "900", "held_amount": held, "spent_amount": spent, "revision": revision, "created_at": instant.Format(time.RFC3339Nano)})
	return b
}
func accountApplied() []any {
	return []any{"applied", operationID, actorID, "account_create", accountID, "applied", "", []byte("null"), snapshot("0", "0", "1", "label"), instant, rootID, journalID}
}

func TestAttemptAppliedAndOriginalReplayText(t *testing.T) {
	call := attemptCall(t)
	r := newRows(attemptOIDs, attemptApplied())
	out, err := call.DecodeAttempt(r)
	if err != nil || !r.closed || out.RootID == nil || out.SourceID == nil || out.AttemptID != attemptID || out.Sequence != 2 || out.CostStatus != "unknown" {
		t.Fatalf("new receipt: %#v %v", out, err)
	}
	for _, delivery := range []string{"prepared", "sending", "completed", "failed", "unknown"} {
		for _, cost := range []string{"unknown", "contract", "invoice", "", "legacy custom 成本 text"} {
			r := newRows(attemptOIDs, []any{"replay", nil, nil, "legacy non-UUID", "raw request", int32(2), delivery, cost})
			out, err := call.DecodeAttempt(r)
			if err != nil || out.RootID != nil || out.SourceID != nil || out.AttemptID != "legacy non-UUID" || out.CostStatus != cost || out.DeliveryState != delivery {
				t.Fatalf("legacy replay changed: %#v %v", out, err)
			}
		}
	}
}

func TestAttemptNullAndIdentityUnionRejectsZeroResult(t *testing.T) {
	for n := range attemptApplied() {
		t.Run(fmt.Sprintf("null_%d", n), func(t *testing.T) {
			row := attemptApplied()
			row[n] = nil
			out, err := attemptCall(t).DecodeAttempt(newRows(attemptOIDs, row))
			protocol(t, err)
			if !reflect.DeepEqual(out, procurementexecution.AttemptReceipt{}) {
				t.Fatal("published invalid receipt")
			}
		})
	}
	for _, tc := range []struct {
		n     int
		value any
	}{
		{0, "noop"}, {1, ""}, {1, "00000000-0000-0000-0000-000000000000"}, {2, rootID}, {3, "legacy"}, {4, "wrong tenant request"}, {5, int32(1)}, {6, "completed"}, {7, "invoice"},
	} {
		row := attemptApplied()
		row[tc.n] = tc.value
		_, err := attemptCall(t).DecodeAttempt(newRows(attemptOIDs, row))
		protocol(t, err)
	}
	for _, ids := range [][2]any{{rootID, nil}, {nil, attemptID}, {"", ""}} {
		row := []any{"replay", ids[0], ids[1], "old", "raw request", int32(2), "failed", "invoice"}
		_, err := attemptCall(t).DecodeAttempt(newRows(attemptOIDs, row))
		protocol(t, err)
	}
}

func TestExactlyOneAndRealScanFailureCausality(t *testing.T) {
	call := attemptCall(t)
	for _, data := range [][][]any{nil, {attemptApplied(), attemptApplied()}} {
		r := newRows(attemptOIDs, data...)
		out, err := call.DecodeAttempt(r)
		protocol(t, err)
		if !r.closed || !reflect.DeepEqual(out, procurementexecution.AttemptReceipt{}) {
			t.Fatal("row-count failure retained receipt/rows")
		}
	}
	scanCause := errors.New("codec mismatch")
	r := newRows(attemptOIDs, attemptApplied())
	r.scanErr = scanCause
	out, err := call.DecodeAttempt(r)
	protocol(t, err)
	if !errors.Is(err, scanCause) || r.Err() != scanCause || !reflect.DeepEqual(out, procurementexecution.AttemptReceipt{}) {
		t.Fatal("same Scan/Rows.Err cause was not protocol wrapped")
	}
	// A genuine pgx codec error also writes Rows.Err and must remain PP903.
	row := attemptApplied()
	row[5] = "not an integer"
	r = newRows(attemptOIDs, row)
	_, err = call.DecodeAttempt(r)
	protocol(t, err)
	if r.Err() == nil || !errors.Is(err, r.Err()) {
		t.Fatal("codec cause lost")
	}
	r = newRows(attemptOIDs, attemptApplied()[:7])
	_, err = call.DecodeAttempt(r)
	protocol(t, err)
	if !errors.Is(err, r.Err()) {
		t.Fatal("column count cause lost")
	}
}

func TestServerErrorsWinOverLocalShapeFailures(t *testing.T) {
	for _, state := range []string{"PP115", "42501", "57014"} {
		for _, where := range []string{"empty", "tail", "extra", "scan", "scan-and-close", "shape-and-close"} {
			t.Run(state+"/"+where, func(t *testing.T) {
				pg := &pgconn.PgError{Code: state, Message: "private server cause"}
				r := newRows(attemptOIDs, attemptApplied())
				switch where {
				case "empty":
					r.data = nil
					r.terminalErr = pg
				case "tail":
					r.terminalErr = pg
				case "extra":
					r.data = append(r.data, attemptApplied())
					r.closeErr = pg
				case "scan":
					r.scanErr = pg
				case "scan-and-close":
					r.scanErr = errors.New("codec mismatch")
					r.closeErr = pg
				case "shape-and-close":
					r.data[0][0] = "wrong outcome"
					r.closeErr = pg
				}
				out, err := attemptCall(t).DecodeAttempt(r)
				var got *pgconn.PgError
				var failure *procurementexecution.Failure
				if !errors.As(err, &got) || got != pg || errors.As(err, &failure) || !r.closed || !reflect.DeepEqual(out, procurementexecution.AttemptReceipt{}) {
					t.Fatalf("server cause masked: %v", err)
				}
			})
		}
	}
	transport := errors.New("stream disconnected")
	r := newRows(attemptOIDs)
	r.terminalErr = transport
	_, err := attemptCall(t).DecodeAttempt(r)
	if err != transport {
		t.Fatal("transport failure became zero-row protocol")
	}
	r = newRows(attemptOIDs, attemptApplied())
	r.scanErr = errors.New("scan argument error")
	r.closeErr = transport
	_, err = attemptCall(t).DecodeAttempt(r)
	var failure *procurementexecution.Failure
	if !errors.Is(err, transport) || errors.As(err, &failure) {
		t.Fatal("independent late transport failure became scan protocol")
	}
}

func TestAccountBranchUnionIncludingNumeric100AndNoop(t *testing.T) {
	for _, outcome := range []string{"applied", "replay"} {
		row := accountApplied()
		row[0] = outcome
		out, err := accountCall(t, false, 0).DecodeAccount(newRows(accountOIDs, row))
		if err != nil || out.AccountJournalID == nil || string(out.Before) != "null" || out.RootID != rootID || !out.CreatedAt.Equal(instant) {
			t.Fatalf("create %s: %#v %v", outcome, out, err)
		}
		big := strings.Repeat("9", 100)
		for _, branch := range []string{"noop", "changed", "not_found", "revision_conflict"} {
			row := accountApplied()
			row[0] = outcome
			row[3] = "account_configure"
			expected := int64(7)
			switch branch {
			case "noop":
				row[7] = snapshot(big, "123456789012345678901", "7", "label")
				row[8] = snapshot(big, "123456789012345678901", "7", "label")
			case "changed":
				row[7] = snapshot(big, "0", "7", "old label")
				row[8] = snapshot(big, "0", "8", "label")
			case "not_found":
				row[5] = "rejected"
				row[6] = "not_found"
				row[7] = []byte("null")
				row[8] = []byte("null")
				row[11] = nil
			case "revision_conflict":
				row[5] = "rejected"
				row[6] = "revision_conflict"
				row[7] = snapshot(big, "0", "9", "label")
				row[8] = []byte("null")
			}
			out, err := accountCall(t, true, expected).DecodeAccount(newRows(accountOIDs, row))
			if err != nil || out.Status != row[5] || out.Reason != row[6] || (branch == "not_found") != (out.AccountJournalID == nil) {
				t.Fatalf("%s/%s: %#v %v", outcome, branch, out, err)
			}
		}
	}
}

func TestAccountRejectsSQLNullWrongIdentityAndIllegalUnion(t *testing.T) {
	for n := range accountApplied() {
		row := accountApplied()
		row[n] = nil
		out, err := accountCall(t, false, 0).DecodeAccount(newRows(accountOIDs, row))
		protocol(t, err)
		if !reflect.DeepEqual(out, procurementexecution.AccountReceipt{}) {
			t.Fatal("invalid account result escaped")
		}
	}
	for _, tc := range []struct {
		n     int
		value any
	}{
		{0, "semantic_conflict"}, {1, actorID}, {2, operationID}, {3, "credential_bind"}, {4, "legacy"}, {5, "rejected"}, {6, "revision_conflict"}, {7, snapshot("0", "0", "1", "label")}, {8, []byte("null")}, {10, ""}, {11, ""}, {9, pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true}},
	} {
		row := accountApplied()
		row[tc.n] = tc.value
		_, err := accountCall(t, false, 0).DecodeAccount(newRows(accountOIDs, row))
		protocol(t, err)
	}
	// not_found cannot smuggle an existing journal or object snapshot.
	for _, n := range []int{7, 8, 11} {
		row := accountApplied()
		row[3] = "account_configure"
		row[5] = "rejected"
		row[6] = "not_found"
		row[7] = []byte("null")
		row[8] = []byte("null")
		row[11] = nil
		if n == 11 {
			row[n] = journalID
		} else {
			row[n] = snapshot("0", "0", "7", "label")
		}
		_, err := accountCall(t, true, 7).DecodeAccount(newRows(accountOIDs, row))
		protocol(t, err)
	}
}

func TestAccountSnapshotStrictTypesAndDecimalBounds(t *testing.T) {
	base := snapshot("0", "0", "1", "label")
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"extra", "x"}, {"id", "not UUID"}, {"label", " label"}, {"provider", "x\x01"}, {"state", "closed"}, {"limit_amount", "9223372036854775808"}, {"held_amount", strings.Repeat("9", 101)}, {"spent_amount", "01"}, {"revision", "0"}, {"revision", "9223372036854775808"}, {"created_at", "2026-11-04T08:01:02.123456789Z"}, {"created_at", "2026-11-04T08:01:02+00:00"}, {"limit_amount", 900}, {"held_amount", nil},
	} {
		var fields map[string]any
		if err := json.Unmarshal(base, &fields); err != nil {
			t.Fatal(err)
		}
		fields[tc.key] = tc.value
		bad, _ := json.Marshal(fields)
		row := accountApplied()
		row[8] = bad
		_, err := accountCall(t, false, 0).DecodeAccount(newRows(accountOIDs, row))
		protocol(t, err)
	}
	duplicate := append(append([]byte(nil), base[:len(base)-1]...), []byte(`,"id":"`+accountID+`"}`)...)
	for _, bad := range [][]byte{[]byte("[]"), []byte("{}"), duplicate, append(append([]byte(nil), base...), []byte(" null")...)} {
		row := accountApplied()
		row[8] = bad
		_, err := accountCall(t, false, 0).DecodeAccount(newRows(accountOIDs, row))
		protocol(t, err)
	}
}

func TestConfigureNoopAtMaxRevisionDoesNotOverflow(t *testing.T) {
	const maximum = int64(1<<63 - 1)
	row := accountApplied()
	row[3] = "account_configure"
	row[7] = snapshot("0", "0", "9223372036854775807", "label")
	row[8] = snapshot("0", "0", "9223372036854775807", "label")
	if _, err := accountCall(t, true, maximum).DecodeAccount(newRows(accountOIDs, row)); err != nil {
		t.Fatal("max-revision noop should retain the original receipt", err)
	}
	row[7] = snapshot("0", "0", "9223372036854775807", "old label")
	_, err := accountCall(t, true, maximum).DecodeAccount(newRows(accountOIDs, row))
	protocol(t, err)
}

func TestWrongDecoderAndNilRowsDoNotPublish(t *testing.T) {
	r := newRows(accountOIDs, accountApplied())
	_, err := attemptCall(t).DecodeAccount(r)
	protocol(t, err)
	if !r.closed {
		t.Fatal("wrong decoder did not close rows")
	}
	r = newRows(attemptOIDs, attemptApplied())
	_, err = accountCall(t, false, 0).DecodeAttempt(r)
	protocol(t, err)
	if !r.closed {
		t.Fatal("wrong decoder did not close rows")
	}
	_, err = attemptCall(t).DecodeAttempt(nil)
	protocol(t, err)
	_, err = accountCall(t, false, 0).DecodeAccount(nil)
	protocol(t, err)
}

func TestFixedColumnTypesRejectCodecConvertibleWrongABI(t *testing.T) {
	for _, tc := range []struct {
		name        string
		index       int
		oid         uint32
		value       any
		convertible bool
	}{
		{"legacy_id_int8", 3, pgtype.Int8OID, int64(42), true},
		{"cost_status_bool", 7, pgtype.BoolOID, true, true},
		{"sequence_text", 5, pgtype.TextOID, "2", false},
		{"sequence_int8", 5, pgtype.Int8OID, int64(2), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := []any{"replay", nil, nil, "legacy", "raw request", int32(2), "failed", "invoice"}
			row[tc.index] = tc.value
			oids := append([]uint32(nil), attemptOIDs...)
			oids[tc.index] = tc.oid
			r := newRows(oids, row)
			// Establish that pgx can scan this wrong ABI into the original
			// decoder's destinations; accepting Scan alone is insufficient.
			var a, b, c, d, e, g, h *string
			var f *int32
			if !r.Next() {
				t.Fatal("missing codec control row")
			}
			if err := r.Scan(&a, &b, &c, &d, &e, &f, &g, &h); (err == nil) != tc.convertible {
				t.Fatalf("wrong-type codec control: convertible=%t, got %v", tc.convertible, err)
			}
			r = newRows(oids, row)
			out, err := attemptCall(t).DecodeAttempt(r)
			protocol(t, err)
			if !r.closed || r.index != -1 || !reflect.DeepEqual(out, procurementexecution.AttemptReceipt{}) {
				t.Fatal("wrong ABI was scanned or published")
			}
		})
	}
	// JSON has a compatible JSON codec too, but the ABI requires JSONB.
	oids := append([]uint32(nil), accountOIDs...)
	oids[8] = pgtype.JSONOID
	r := newRows(oids, accountApplied())
	_, err := accountCall(t, false, 0).DecodeAccount(r)
	protocol(t, err)
	if !r.closed {
		t.Fatal("account ABI failure did not close rows")
	}
}

func TestFixedColumnNamesCountAndAllTypePositions(t *testing.T) {
	for n := range attemptOIDs {
		r := newRows(attemptOIDs, attemptApplied())
		r.fields[n].DataTypeOID = pgtype.ByteaOID
		_, err := attemptCall(t).DecodeAttempt(r)
		protocol(t, err)
		r = newRows(attemptOIDs, attemptApplied())
		r.fields[n].Name = "wrong_alias"
		_, err = attemptCall(t).DecodeAttempt(r)
		protocol(t, err)
	}
	for n := range accountOIDs {
		r := newRows(accountOIDs, accountApplied())
		r.fields[n].DataTypeOID = pgtype.ByteaOID
		_, err := accountCall(t, false, 0).DecodeAccount(r)
		protocol(t, err)
		r = newRows(accountOIDs, accountApplied())
		r.fields[n].Name = "wrong_alias"
		_, err = accountCall(t, false, 0).DecodeAccount(r)
		protocol(t, err)
	}
	for _, count := range []int{0, 7, 9} {
		r := newRows(attemptOIDs, attemptApplied())
		if count > len(r.fields) {
			r.fields = append(r.fields, pgconn.FieldDescription{})
		} else {
			r.fields = r.fields[:count]
		}
		_, err := attemptCall(t).DecodeAttempt(r)
		protocol(t, err)
		if !r.closed {
			t.Fatal("bad column count was not closed")
		}
	}
	for _, count := range []int{0, 11, 13} {
		r := newRows(accountOIDs, accountApplied())
		if count > len(r.fields) {
			r.fields = append(r.fields, pgconn.FieldDescription{})
		} else {
			r.fields = r.fields[:count]
		}
		_, err := accountCall(t, false, 0).DecodeAccount(r)
		protocol(t, err)
		if !r.closed {
			t.Fatal("bad account column count was not closed")
		}
	}
}

func TestWrongABIStillDrainsAndPreservesServerOrStreamCause(t *testing.T) {
	for _, cause := range []error{&pgconn.PgError{Code: "42501", Message: "private server"}, errors.New("stream disconnected")} {
		r := newRows(attemptOIDs, attemptApplied())
		r.fields[3].DataTypeOID = pgtype.Int8OID
		r.closeErr = cause
		out, err := attemptCall(t).DecodeAttempt(r)
		var failure *procurementexecution.Failure
		if err != cause || errors.As(err, &failure) || !r.closed || !reflect.DeepEqual(out, procurementexecution.AttemptReceipt{}) {
			t.Fatalf("ABI mismatch hid drain cause: %v", err)
		}
		r = newRows(accountOIDs, accountApplied())
		r.fields = r.fields[:11]
		r.err = cause
		_, err = accountCall(t, false, 0).DecodeAccount(r)
		if err != cause || !r.closed {
			t.Fatal("ABI mismatch hid preexisting server error", err)
		}
	}
}
