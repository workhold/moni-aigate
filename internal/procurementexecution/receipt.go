package procurementexecution

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Rows is the part of pgx.Rows needed by the decoder, allowing protocol tests
// without a database. FieldDescriptions authenticates the ABI before codecs
// can convert a wrong PostgreSQL column type. Close's late errors use Err.
type Rows interface {
	FieldDescriptions() []pgconn.FieldDescription
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}

type column struct {
	name string
	oid  uint32
}

var attemptColumns = []column{
	{"outcome", pgtype.TextOID}, {"root_id", pgtype.TextOID},
	{"source_id", pgtype.TextOID}, {"attempt_id", pgtype.TextOID},
	{"request_id", pgtype.TextOID}, {"sequence", pgtype.Int4OID},
	{"delivery_state", pgtype.TextOID}, {"cost_status", pgtype.TextOID},
}

var accountColumns = []column{
	{"outcome", pgtype.TextOID}, {"operation_id", pgtype.TextOID},
	{"actor", pgtype.TextOID}, {"kind", pgtype.TextOID},
	{"target_id", pgtype.TextOID}, {"status", pgtype.TextOID},
	{"reason", pgtype.TextOID}, {"before_state", pgtype.JSONBOID},
	{"after_state", pgtype.JSONBOID}, {"created_at", pgtype.TimestamptzOID},
	{"root_id", pgtype.TextOID}, {"account_journal_id", pgtype.TextOID},
}

func fieldsMatch(rows Rows, expected []column) bool {
	actual := rows.FieldDescriptions()
	if len(actual) != len(expected) {
		return false
	}
	for n, want := range expected {
		if actual[n].Name != want.name || actual[n].DataTypeOID != want.oid {
			return false
		}
	}
	return true
}

type AttemptReceipt struct {
	Outcome, AttemptID, RequestID, DeliveryState, CostStatus string
	RootID, SourceID                                         *string
	Sequence                                                 int32
}

type AccountReceipt struct {
	Outcome, OperationID, Actor, Kind, TargetID, Status, Reason string
	Before, After                                               json.RawMessage
	CreatedAt                                                   time.Time
	RootID                                                      string
	AccountJournalID                                            *string
}

func combine(a, b error) error {
	if a == nil {
		return b
	}
	if b == nil || errors.Is(a, b) {
		return a
	}
	return errors.Join(a, b)
}

func finishRows(rows Rows) error {
	before := rows.Err()
	rows.Close()
	return combine(before, rows.Err())
}

// The server/stream error wins over a local shape problem. In particular a
// deferred error discovered when draining must not become a zero-row PP903.
func exactlyOne[T any](rows Rows, columns []column, scan func(Rows) (T, error)) (T, error) {
	var zero T
	if rows == nil {
		return zero, ProtocolError(nil)
	}
	if !fieldsMatch(rows, columns) {
		if err := finishRows(rows); err != nil {
			return zero, err
		}
		return zero, ProtocolError(nil)
	}
	if !rows.Next() {
		if err := finishRows(rows); err != nil {
			return zero, err
		}
		return zero, ProtocolError(nil)
	}
	value, scanErr := scan(rows)
	extra := false
	if scanErr == nil {
		extra = rows.Next()
	}
	if err := finishRows(rows); err != nil && (scanErr == nil || !errors.Is(err, scanErr) || !errors.Is(scanErr, err)) {
		return zero, combine(err, scanErr)
	}
	if scanErr != nil {
		var databaseError *pgconn.PgError
		if errors.As(scanErr, &databaseError) {
			return zero, scanErr
		}
		return zero, ProtocolError(scanErr)
	}
	if extra {
		return zero, ProtocolError(nil)
	}
	return value, nil
}

func (i Invocation) DecodeAttempt(rows Rows) (AttemptReceipt, error) {
	if i.root != RecordAttempt {
		if rows != nil {
			if err := finishRows(rows); err != nil {
				return AttemptReceipt{}, err
			}
		}
		return AttemptReceipt{}, ProtocolError(nil)
	}
	type wire struct {
		outcome, root, source, attempt, request, delivery, cost *string
		sequence                                                *int32
	}
	w, err := exactlyOne(rows, attemptColumns, func(r Rows) (wire, error) {
		var w wire
		e := r.Scan(&w.outcome, &w.root, &w.source, &w.attempt, &w.request, &w.sequence, &w.delivery, &w.cost)
		return w, e
	})
	if err != nil {
		return AttemptReceipt{}, err
	}
	if w.outcome == nil || w.attempt == nil || w.request == nil || w.sequence == nil || w.delivery == nil || w.cost == nil || *w.request != i.request || *w.sequence != i.sequence || !oneOf(*w.delivery, "prepared", "sending", "completed", "failed", "unknown") {
		return AttemptReceipt{}, ProtocolError(nil)
	}
	switch *w.outcome {
	case "applied":
		if w.root == nil || w.source == nil || !canonicalID(*w.root, true) || !canonicalID(*w.attempt, true) || *w.source != *w.attempt || *w.delivery != "prepared" || *w.cost != "unknown" {
			return AttemptReceipt{}, ProtocolError(nil)
		}
	case "replay":
		// The old fast return scans TEXT without new ID/cost-status checks.
		if w.root != nil || w.source != nil {
			return AttemptReceipt{}, ProtocolError(nil)
		}
	default:
		return AttemptReceipt{}, ProtocolError(nil)
	}
	return AttemptReceipt{Outcome: *w.outcome, RootID: w.root, SourceID: w.source, AttemptID: *w.attempt, RequestID: *w.request, Sequence: *w.sequence, DeliveryState: *w.delivery, CostStatus: *w.cost}, nil
}

type accountWire struct {
	outcome, operation, actor, kind, target, status, reason, root, journal *string
	before, after                                                          []byte
	created                                                                pgtype.Timestamptz
}

func (i Invocation) DecodeAccount(rows Rows) (AccountReceipt, error) {
	if i.root != CreateAccount && i.root != ConfigureAccount {
		if rows != nil {
			if err := finishRows(rows); err != nil {
				return AccountReceipt{}, err
			}
		}
		return AccountReceipt{}, ProtocolError(nil)
	}
	w, err := exactlyOne(rows, accountColumns, func(r Rows) (accountWire, error) {
		var w accountWire
		e := r.Scan(&w.outcome, &w.operation, &w.actor, &w.kind, &w.target, &w.status, &w.reason, &w.before, &w.after, &w.created, &w.root, &w.journal)
		return w, e
	})
	if err != nil {
		return AccountReceipt{}, err
	}
	if w.outcome == nil || w.operation == nil || w.actor == nil || w.kind == nil || w.target == nil || w.status == nil || w.reason == nil || w.root == nil || w.before == nil || w.after == nil || !w.created.Valid || w.created.InfinityModifier != pgtype.Finite || w.created.Time.Year() < 1 || w.created.Time.Year() > 9999 || w.created.Time.Nanosecond()%1000 != 0 || !oneOf(*w.outcome, "applied", "replay") || *w.operation != i.account.OperationID || *w.actor != i.account.ActorID || !canonicalID(*w.root, true) {
		return AccountReceipt{}, ProtocolError(nil)
	}
	wantKind := "account_create"
	if i.root == ConfigureAccount {
		wantKind = "account_configure"
	}
	if *w.kind != wantKind || (i.root == CreateAccount && !canonicalID(*w.target, true)) || (i.root == ConfigureAccount && *w.target != i.target) {
		return AccountReceipt{}, ProtocolError(nil)
	}
	before, beforeNull, err := decodeSnapshot(w.before)
	if err != nil {
		return AccountReceipt{}, ProtocolError(err)
	}
	after, afterNull, err := decodeSnapshot(w.after)
	if err != nil {
		return AccountReceipt{}, ProtocolError(err)
	}
	if !beforeNull && before.ID != *w.target || !afterNull && after.ID != *w.target {
		return AccountReceipt{}, ProtocolError(nil)
	}
	if *w.status == "rejected" && *w.reason == "not_found" && i.root == ConfigureAccount {
		if !beforeNull || !afterNull || w.journal != nil {
			return AccountReceipt{}, ProtocolError(nil)
		}
	} else {
		if w.journal == nil || !canonicalID(*w.journal, true) {
			return AccountReceipt{}, ProtocolError(nil)
		}
		switch {
		case *w.status == "applied" && *w.reason == "":
			if afterNull || !after.matches(i.account) {
				return AccountReceipt{}, ProtocolError(nil)
			}
			if i.root == CreateAccount {
				if !beforeNull || after.HeldAmount != "0" || after.SpentAmount != "0" || after.Revision != "1" {
					return AccountReceipt{}, ProtocolError(nil)
				}
			} else {
				if beforeNull || before.Revision != strconv.FormatInt(i.expected, 10) || before.HeldAmount != after.HeldAmount || before.SpentAmount != after.SpentAmount || before.CreatedAt != after.CreatedAt {
					return AccountReceipt{}, ProtocolError(nil)
				}
				old, _ := strconv.ParseInt(before.Revision, 10, 64)
				newRevision, _ := strconv.ParseInt(after.Revision, 10, 64)
				changed := before.Label != after.Label || before.Provider != after.Provider || before.State != after.State || before.LimitAmount != after.LimitAmount
				if changed && (old == int64(1<<63-1) || newRevision != old+1) || !changed && newRevision != old {
					return AccountReceipt{}, ProtocolError(nil)
				}
			}
		case *w.status == "rejected" && *w.reason == "revision_conflict" && i.root == ConfigureAccount:
			if beforeNull || !afterNull || before.Revision == strconv.FormatInt(i.expected, 10) {
				return AccountReceipt{}, ProtocolError(nil)
			}
		default:
			return AccountReceipt{}, ProtocolError(nil)
		}
	}
	return AccountReceipt{Outcome: *w.outcome, OperationID: *w.operation, Actor: *w.actor, Kind: *w.kind, TargetID: *w.target, Status: *w.status, Reason: *w.reason, Before: cloneBytes(w.before), After: cloneBytes(w.after), CreatedAt: w.created.Time, RootID: *w.root, AccountJournalID: w.journal}, nil
}

type accountSnapshot struct {
	ID, Label, Provider, State, LimitAmount, HeldAmount, SpentAmount, Revision, CreatedAt string
}

func (a accountSnapshot) matches(in AccountInput) bool {
	return a.Label == in.Label && a.Provider == in.Provider && a.State == in.State && a.LimitAmount == strconv.FormatInt(in.LimitAmount, 10)
}

func decodeSnapshot(raw []byte) (accountSnapshot, bool, error) {
	var zero accountSnapshot
	if !utf8.Valid(raw) {
		return zero, false, errors.New("invalid account snapshot UTF-8")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return zero, true, nil
	}
	// Token-by-token decoding also rejects duplicate object keys. Decoding into
	// a map alone would silently accept the last duplicate value.
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return zero, false, errors.New("invalid account snapshot object")
	}
	fields := map[string]string{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return zero, false, errors.New("invalid account snapshot key")
		}
		if _, exists := fields[key]; exists {
			return zero, false, errors.New("duplicate account snapshot key")
		}
		var value any
		if err = d.Decode(&value); err != nil {
			return zero, false, err
		}
		text, ok := value.(string)
		if !ok {
			return zero, false, errors.New("account snapshot field must be a string")
		}
		fields[key] = text
	}
	if _, err = d.Token(); err != nil {
		return zero, false, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return zero, false, errors.New("trailing account snapshot JSON")
	}
	keys := []string{"id", "label", "provider", "state", "limit_amount", "held_amount", "spent_amount", "revision", "created_at"}
	if len(fields) != len(keys) {
		return zero, false, errors.New("invalid account snapshot fields")
	}
	for _, key := range keys {
		if _, exists := fields[key]; !exists {
			return zero, false, errors.New("missing account snapshot field")
		}
	}
	a := accountSnapshot{fields["id"], fields["label"], fields["provider"], fields["state"], fields["limit_amount"], fields["held_amount"], fields["spent_amount"], fields["revision"], fields["created_at"]}
	at, err := time.Parse(time.RFC3339Nano, a.CreatedAt)
	if !canonicalID(a.ID, false) || !safeText(a.Label) || !safeText(a.Provider) || !oneOf(a.State, "active", "paused") || !decimal64(a.LimitAmount, false) || !decimal64(a.Revision, true) || !decimal100(a.HeldAmount) || !decimal100(a.SpentAmount) || err != nil || at.Year() < 1 || at.Year() > 9999 || at.Nanosecond()%1000 != 0 || at.UTC().Format(time.RFC3339Nano) != a.CreatedAt {
		return zero, false, errors.New("invalid typed account snapshot")
	}
	return a, false, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func canonicalID(value string, nonzero bool) bool {
	if len(value) != 36 || nonzero && value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for n, r := range value {
		if n == 8 || n == 13 || n == 18 || n == 23 {
			if r != '-' {
				return false
			}
		} else if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func safeText(value string) bool {
	if len(value) < 1 || len(value) > 100 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func decimal64(value string, positive bool) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n >= 0 && (!positive || n > 0) && strconv.FormatInt(n, 10) == value
}

func decimal100(value string) bool {
	if value == "0" {
		return true
	}
	if len(value) < 1 || len(value) > 100 || value[0] < '1' || value[0] > '9' {
		return false
	}
	for _, r := range value[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
