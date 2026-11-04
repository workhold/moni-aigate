// Package procurementexecution contains the prospective three-root protocol.
// It does not acquire connections, run transactions, install SQL, or select a
// serving profile. An Invocation is data for a separately reviewed executor.
package procurementexecution

import (
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type Root string

const (
	CreateAccount    Root = "pp_create_account"
	ConfigureAccount Root = "pp_configure_account"
	RecordAttempt    Root = "pp_record_attempt"
)

type AccountInput struct {
	ActorID, OperationID, Label, Provider, State string
	LimitAmount                                  int64
}

type ConfigurationInput struct {
	AccountInput
	AccountID        string
	ExpectedRevision int64
}

// CostQuote keeps strings as uninterpreted bytes on the SQL boundary. The
// original billing validator, not this protocol package, owns business checks.
type CostQuote struct {
	VersionID, Basis      string
	InputRate, OutputRate *int64
}

type AttemptInput struct {
	TenantID, RequestID, ChannelID, ChannelVersionID, CredentialID, UpstreamModel string
	Sequence                                                                      int32
	RouteDecisions                                                                []byte
	CostQuote                                                                     *CostQuote
}

// Invocation holds a private snapshot. Accessors never lend mutable argument
// storage; callers cannot change a later retry by modifying returned bytes.
type Invocation struct {
	root     Root
	query    string
	args     []any
	account  AccountInput
	target   string
	expected int64
	request  string
	sequence int32
}

func validSchema(schema string) bool {
	return len(schema) > 0 && len(schema) <= 63 && utf8.ValidString(schema) && !strings.ContainsRune(schema, 0)
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func (i Invocation) Root() Root    { return i.root }
func (i Invocation) Query() string { return i.query }
func (i Invocation) Arguments() []any {
	out := append([]any(nil), i.args...)
	for n, value := range out {
		if b, ok := value.([]byte); ok {
			out[n] = cloneBytes(b)
		}
	}
	return out
}

const accountProjection = "outcome,operation_id,actor,kind,target_id,status,reason,before_state,after_state,created_at,root_id,account_journal_id"
const attemptProjection = "outcome,root_id,source_id,attempt_id,request_id,sequence,delivery_state,cost_status"

func NewCreateAccount(schema string, in AccountInput) (Invocation, error) {
	if !validSchema(schema) {
		return Invocation{}, ProtocolError(nil)
	}
	return Invocation{
		root:    CreateAccount,
		query:   "SELECT " + accountProjection + " FROM " + pgx.Identifier{schema, string(CreateAccount)}.Sanitize() + "($1::pg_catalog.text,$2::pg_catalog.text,$3::pg_catalog.text,$4::pg_catalog.text,$5::pg_catalog.text,$6::pg_catalog.int8)",
		args:    []any{in.ActorID, in.OperationID, in.Label, in.Provider, in.State, in.LimitAmount},
		account: in,
	}, nil
}

func NewConfigureAccount(schema string, in ConfigurationInput) (Invocation, error) {
	if !validSchema(schema) {
		return Invocation{}, ProtocolError(nil)
	}
	return Invocation{
		root:    ConfigureAccount,
		query:   "SELECT " + accountProjection + " FROM " + pgx.Identifier{schema, string(ConfigureAccount)}.Sanitize() + "($1::pg_catalog.text,$2::pg_catalog.text,$3::pg_catalog.text,$4::pg_catalog.int8,$5::pg_catalog.text,$6::pg_catalog.text,$7::pg_catalog.text,$8::pg_catalog.int8)",
		args:    []any{in.ActorID, in.OperationID, in.AccountID, in.ExpectedRevision, in.Label, in.Provider, in.State, in.LimitAmount},
		account: in.AccountInput, target: in.AccountID, expected: in.ExpectedRevision,
	}, nil
}

func NewRecordAttempt(schema string, in AttemptInput) (Invocation, error) {
	if !validSchema(schema) {
		return Invocation{}, ProtocolError(nil)
	}
	args := []any{in.TenantID, in.RequestID, in.Sequence, in.ChannelID, in.ChannelVersionID, in.CredentialID, in.UpstreamModel, cloneBytes(in.RouteDecisions), false, nil, nil, nil, nil}
	if in.CostQuote != nil {
		q := in.CostQuote
		args[8] = true
		// make preserves non-NULL empty bytes even for an empty Go string.
		args[9] = make([]byte, len(q.VersionID))
		copy(args[9].([]byte), q.VersionID)
		if q.InputRate != nil {
			args[10] = *q.InputRate
		}
		if q.OutputRate != nil {
			args[11] = *q.OutputRate
		}
		args[12] = make([]byte, len(q.Basis))
		copy(args[12].([]byte), q.Basis)
	}
	return Invocation{
		root:  RecordAttempt,
		query: "SELECT " + attemptProjection + " FROM " + pgx.Identifier{schema, string(RecordAttempt)}.Sanitize() + "($1::pg_catalog.text,$2::pg_catalog.text,$3::pg_catalog.int4,$4::pg_catalog.text,$5::pg_catalog.text,$6::pg_catalog.text,$7::pg_catalog.text,$8::pg_catalog.jsonb,$9::pg_catalog.bool,$10::pg_catalog.bytea,$11::pg_catalog.int8,$12::pg_catalog.int8,$13::pg_catalog.bytea)",
		args:  args, request: in.RequestID, sequence: in.Sequence,
	}, nil
}
