package procurementexecution_test

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
)

func protocol(t *testing.T, err error) {
	t.Helper()
	var failure *procurementexecution.Failure
	if !errors.As(err, &failure) || failure.SQLState() != "PP903" || failure.PublicCode() != "procurement_execution_protocol" || failure.Domain() != procurementexecution.DomainExecution {
		t.Fatalf("want local protocol classification, got %v", err)
	}
}

func TestFixedQueriesBindAllBusinessInput(t *testing.T) {
	account := procurementexecution.AccountInput{ActorID: "actor", OperationID: "operation", Label: "label", Provider: "provider", State: "active", LimitAmount: math.MaxInt64}
	create, err := procurementexecution.NewCreateAccount(`s"; SELECT $$`, account)
	if err != nil {
		t.Fatal(err)
	}
	configure, err := procurementexecution.NewConfigureAccount("s", procurementexecution.ConfigurationInput{AccountInput: account, AccountID: "target", ExpectedRevision: 7})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := procurementexecution.NewRecordAttempt("s", procurementexecution.AttemptInput{TenantID: "tenant", RequestID: "request", Sequence: 2, ChannelID: "channel", ChannelVersionID: "version", CredentialID: "credential", UpstreamModel: "model", RouteDecisions: []byte("[]")})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		call  procurementexecution.Invocation
		root  procurementexecution.Root
		query string
		args  []any
	}{
		{create, procurementexecution.CreateAccount, `SELECT outcome,operation_id,actor,kind,target_id,status,reason,before_state,after_state,created_at,root_id,account_journal_id FROM "s""; SELECT $$"."pp_create_account"($1::pg_catalog.text,$2::pg_catalog.text,$3::pg_catalog.text,$4::pg_catalog.text,$5::pg_catalog.text,$6::pg_catalog.int8)`, []any{"actor", "operation", "label", "provider", "active", int64(math.MaxInt64)}},
		{configure, procurementexecution.ConfigureAccount, `SELECT outcome,operation_id,actor,kind,target_id,status,reason,before_state,after_state,created_at,root_id,account_journal_id FROM "s"."pp_configure_account"($1::pg_catalog.text,$2::pg_catalog.text,$3::pg_catalog.text,$4::pg_catalog.int8,$5::pg_catalog.text,$6::pg_catalog.text,$7::pg_catalog.text,$8::pg_catalog.int8)`, []any{"actor", "operation", "target", int64(7), "label", "provider", "active", int64(math.MaxInt64)}},
		{attempt, procurementexecution.RecordAttempt, `SELECT outcome,root_id,source_id,attempt_id,request_id,sequence,delivery_state,cost_status FROM "s"."pp_record_attempt"($1::pg_catalog.text,$2::pg_catalog.text,$3::pg_catalog.int4,$4::pg_catalog.text,$5::pg_catalog.text,$6::pg_catalog.text,$7::pg_catalog.text,$8::pg_catalog.jsonb,$9::pg_catalog.bool,$10::pg_catalog.bytea,$11::pg_catalog.int8,$12::pg_catalog.int8,$13::pg_catalog.bytea)`, []any{"tenant", "request", int32(2), "channel", "version", "credential", "model", []byte("[]"), false, nil, nil, nil, nil}},
	} {
		if tc.call.Root() != tc.root || tc.call.Query() != tc.query || !reflect.DeepEqual(tc.call.Arguments(), tc.args) {
			t.Fatalf("wrong frozen ABI for %s", tc.root)
		}
	}
}

func TestSchemaValidationIsLocalAndByteBounded(t *testing.T) {
	for _, schema := range []string{"", strings.Repeat("x", 64), "x\x00y", string([]byte{255}), strings.Repeat("中", 22)} {
		_, err := procurementexecution.NewCreateAccount(schema, procurementexecution.AccountInput{})
		protocol(t, err)
		_, err = procurementexecution.NewConfigureAccount(schema, procurementexecution.ConfigurationInput{})
		protocol(t, err)
		_, err = procurementexecution.NewRecordAttempt(schema, procurementexecution.AttemptInput{})
		protocol(t, err)
	}
	if _, err := procurementexecution.NewCreateAccount(strings.Repeat("中", 21), procurementexecution.AccountInput{}); err != nil {
		t.Fatal("63-byte identifier was rejected", err)
	}
}

func TestIgnoredQuoteAndRouteBytesArePrivateSnapshots(t *testing.T) {
	route := []byte(`[{"channel_id":"x","version_id":"y","priority":-9223372036854775808,"weight":18446744073709551615,"reason":"selected"}]`)
	wantRoute := append([]byte(nil), route...)
	version := string([]byte{'v', 0, 255, 254})
	basis := "未知\x00" + string([]byte{128})
	input, output := int64(math.MinInt64), int64(math.MaxInt64)
	quote := &procurementexecution.CostQuote{VersionID: version, Basis: basis, InputRate: &input, OutputRate: &output}
	call, err := procurementexecution.NewRecordAttempt("s", procurementexecution.AttemptInput{RequestID: "RAW request", Sequence: 1, RouteDecisions: route, CostQuote: quote})
	if err != nil {
		t.Fatal(err)
	}
	route[0] = 'X'
	input, output = 0, 0
	quote.VersionID, quote.Basis = "new", "new"
	args := call.Arguments()
	if !bytes.Equal(args[7].([]byte), wantRoute) || args[8] != true || !bytes.Equal(args[9].([]byte), []byte(version)) || args[10] != int64(math.MinInt64) || args[11] != int64(math.MaxInt64) || !bytes.Equal(args[12].([]byte), []byte(basis)) {
		t.Fatal("raw ignored bytes/rates or full uint64 JSON changed")
	}
	args[7].([]byte)[0] = 'Y'
	args[9].([]byte)[0] = 'Y'
	args[12].([]byte)[0] = 'Y'
	args[10] = int64(77)
	again := call.Arguments()
	if !bytes.Equal(again[7].([]byte), wantRoute) || !bytes.Equal(again[9].([]byte), []byte(version)) || !bytes.Equal(again[12].([]byte), []byte(basis)) || again[10] != int64(math.MinInt64) {
		t.Fatal("argument accessor lent mutable snapshot storage")
	}
}

func TestQuoteNullUnionDoesNotFoldEmptyOrNullableRates(t *testing.T) {
	for _, q := range []*procurementexecution.CostQuote{nil, {}, {VersionID: "v", Basis: "contract"}} {
		call, err := procurementexecution.NewRecordAttempt("s", procurementexecution.AttemptInput{CostQuote: q})
		if err != nil {
			t.Fatal(err)
		}
		a := call.Arguments()
		if q == nil {
			if a[8] != false || a[9] != nil || a[10] != nil || a[11] != nil || a[12] != nil {
				t.Fatal("nil quote was not four SQL NULLs")
			}
		} else {
			v, vok := a[9].([]byte)
			b, bok := a[12].([]byte)
			if a[8] != true || !vok || !bok || v == nil || b == nil || string(v) != q.VersionID || string(b) != q.Basis || a[10] != nil || a[11] != nil {
				t.Fatal("empty quote bytes or nullable rates changed")
			}
		}
	}
}
