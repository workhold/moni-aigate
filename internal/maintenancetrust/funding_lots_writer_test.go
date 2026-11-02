package maintenancetrust

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotinit"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func TestFundingLotsWriterRequiresOriginalInitWritingStage(t *testing.T) {
	ph, permit := pureFundingLotsPhase(t)
	ph.purpose, ph.snapshot.mode = snapshotInit, snapshotInit
	for _, stage := range []fundingLotsStage{fundingLotsPre, fundingLotsBasis, fundingLotsDraft, fundingLotsPost, fundingLotsReady, fundingLotsCommitting, fundingLotsDone} {
		ph.stage = stage
		// A missing raw transaction would panic if any rejected stage reached
		// SQL. Both the whole writer and individual INSERT must reject first.
		if err := ph.writeFundingLots(ph.call.ctx); err != ErrTransaction {
			t.Fatalf("writer admitted stage %d: %v", stage, err)
		}
		if err := ph.insertFundingLots(ph.call.ctx, 0); err != ErrTransaction {
			t.Fatalf("INSERT admitted stage %d: %v", stage, err)
		}
		if ph.stage != stage || ph.revoked || permit.state.revoked || !ph.snapshot.gate.busy || ph.pending != nil {
			t.Fatal("stage rejection changed the current owner")
		}
	}
	ph.stage = fundingLotsWriting
	if err := ph.requireFundingLotsWriting(ph.call.ctx); err != nil {
		t.Fatal("original writing stage rejected", err)
	}
	ph.purpose, ph.snapshot.mode = snapshotPlan, snapshotPlan
	if err := ph.writeFundingLots(ph.call.ctx); err != ErrTransaction {
		t.Fatal("Plan phase admitted writer", err)
	}
	ph.purpose, ph.snapshot.mode = snapshotInit, snapshotInit
	ph.pending = &fundingLotsRows{phase: ph}
	if err := ph.requireFundingLotsWriting(ph.call.ctx); err != ErrTransaction {
		t.Fatal("writer admitted outstanding rows", err)
	}
	ph.pending = nil
	ph.operation = true
	if err := ph.requireFundingLotsWriting(ph.call.ctx); err != ErrTransaction {
		t.Fatal("writer admitted another method owner", err)
	}
	ph.operation = false
}

func TestFundingLotsWriterFixedAllowlistAndGeneratedStamps(t *testing.T) {
	want := []struct {
		table  string
		fields int
		stamp  string
	}{
		{"funding_lot_legacy_receipts", 7, ""}, {"funding_lot_legacy_sandbox_receipts", 6, ""},
		{"funding_lot_legacy_settlements", 8, ""}, {"funding_lot_legacy_approved_refunds", 6, ""},
		{"funding_lot_legacy_openings", 8, ""}, {"funding_wallet_legacy_sources", 10, ""},
		{"funding_wallet_operations", 15, "x"}, {"funding_wallet_heads", 4, ""},
		{"funding_lots", 10, "z"}, {"funding_lot_events", 21, "x"},
		{"funding_lot_balances", 7, ""}, {"funding_reservation_lots", 6, "x"},
		{"funding_lot_runtime_attestations", 16, ""}, {"funding_lot_epoch", 19, ""},
	}
	if len(fundingLotsWriteSpecs) != len(want) {
		t.Fatal("write allowlist changed")
	}
	for i, item := range want {
		spec := fundingLotsWriteSpecs[i]
		if spec.table != item.table || len(strings.Fields(spec.columns)) != item.fields || len(spec.kinds) != item.fields || spec.stamp != item.stamp {
			t.Fatalf("write contract changed at %d", i)
		}
		sql, err := fundingLotsWriteSQL(`odd"schema`, spec)
		if err != nil || !strings.HasPrefix(sql, `INSERT INTO "odd""schema"."`+item.table+`"(`) {
			t.Fatalf("fixed identifier quoting %d: %v", i, err)
		}
		parts := strings.Split(sql, " RETURNING ")
		if len(parts) != 2 || strings.ContainsAny(sql, ";\n") || strings.Contains(sql, "ON CONFLICT") {
			t.Fatal("writer is not one fixed INSERT")
		}
		for n := 1; n <= item.fields; n++ {
			if !strings.Contains(parts[0], "$"+strconv.Itoa(n)+"::pg_catalog.") {
				t.Fatalf("missing typed argument %d/%d", i, n)
			}
		}
		if item.stamp != "" {
			if strings.Contains(parts[0], "created_at") || strings.Contains(parts[0], "created_xid") || !strings.Contains(parts[1], "pg_catalog.timestamptz_send(created_at)") {
				t.Fatal("database time stamp supplied or not observed")
			}
			if (item.stamp == "x") != strings.Contains(parts[1], "created_xid::pg_catalog.text") {
				t.Fatal("top xid stamp contract changed")
			}
		}
		for _, column := range strings.Fields(spec.columns) {
			if !strings.Contains(parts[1], `"`+column+`"`) {
				t.Fatalf("RETURNING omits actual field %s", column)
			}
		}
	}
	for _, bad := range []fundingLotsWriteSpec{{table: "x"}, {table: "x", columns: "a b", kinds: "t"}, {table: "x", columns: "a", kinds: "?"}, {table: "x", columns: "a", kinds: "t", stamp: "other"}} {
		if sql, err := fundingLotsWriteSQL("s", bad); sql != "" || err != ErrExecution {
			t.Fatal("invalid static declaration accepted")
		}
	}
}

type fundingLotsWriterAlias string
type fundingLotsWriterValuer struct{ called *bool }

func (v fundingLotsWriterValuer) TextValue() (pgtype.Text, error) {
	*v.called = true
	return pgtype.Text{String: "bad", Valid: true}, nil
}

func TestFundingLotsWriterScalarClosureAndExactAmounts(t *testing.T) {
	for _, test := range []struct {
		kind           byte
		value          any
		argument, echo any
	}{
		{'t', "", "", ""}, {'t', "租户/乙", "租户/乙", "租户/乙"}, {'T', nil, nil, nil},
		{'i', "-9223372036854775808", int64(-9223372036854775808), "-9223372036854775808"},
		{'i', "9223372036854775807", int64(9223372036854775807), "9223372036854775807"},
		{'I', nil, nil, nil}, {'n', "18446744073709551614", "18446744073709551614", "18446744073709551614"},
		{'n', "-18446744073709551614", "-18446744073709551614", "-18446744073709551614"},
		{'o', uint32(4294967295), uint32(4294967295), "4294967295"}, {'O', nil, nil, nil},
		{'v', int32(2), int32(2), "2"}, {'b', true, true, true},
	} {
		argument, echo, err := fundingLotsWriteValue(test.kind, test.value)
		if err != nil || !reflect.DeepEqual(argument, test.argument) || !reflect.DeepEqual(echo, test.echo) {
			t.Fatalf("closed scalar %q: %v", test.kind, err)
		}
	}
	called := false
	pointer := "x"
	for _, value := range []any{fundingLotsWriterAlias("x"), fundingLotsWriterValuer{&called}, &pointer, pgx.QueryExecModeSimpleProtocol, pgx.NamedArgs{"x": 1}, float64(1), int(1), uint64(1), []string{"x"}} {
		if argument, echo, err := fundingLotsWriteValue('t', value); err != ErrExecution || argument != nil || echo != nil {
			t.Fatal("driver-capable/nonbuiltin value accepted")
		}
	}
	if called {
		t.Fatal("untrusted valuer invoked")
	}
	for _, test := range []struct {
		kind  byte
		value any
	}{
		{'t', nil}, {'t', "bad\x00text"}, {'t', string([]byte{255})}, {'i', "+1"}, {'i', "01"}, {'i', "-0"}, {'i', "9223372036854775808"},
		{'n', "01"}, {'n', "-0"}, {'n', "1.0"}, {'n', "1e3"}, {'n', "+1"}, {'o', uint32(0)}, {'v', int32(0)}, {'b', false}, {'y', []byte{}}, {'?', "x"},
	} {
		if a, e, err := fundingLotsWriteValue(test.kind, test.value); err != ErrExecution || a != nil || e != nil {
			t.Fatal("noncanonical write value accepted")
		}
	}
	for _, test := range []struct {
		kind  byte
		value any
	}{{'t', strings.Repeat("x", 65537)}, {'y', make([]byte, 262145)}} {
		if a, e, err := fundingLotsWriteValue(test.kind, test.value); err != ErrResource || a != nil || e != nil {
			t.Fatal("write resource failure lost")
		}
	}
	raw := []byte("canonical")
	a, e, err := fundingLotsWriteValue('y', raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 'X'
	a.([]byte)[1] = 'Y'
	if !bytes.Equal(e.([]byte), []byte("canonical")) {
		t.Fatal("canonical aliases caller or argument")
	}
	if fundingLotsNullable(nil) != nil || fundingLotsNullable(&pointer) != "x" {
		t.Fatal("NULL/empty pointer distinction lost")
	}
}

func TestFundingLotsWriterNativeTimestampEncoding(t *testing.T) {
	codec := pgtype.NewMap()
	for _, year := range []int{-4712, 0, 1, 10000, 294276} {
		at := time.Date(year, 1, 2, 3, 4, 5, 123456000, time.UTC)
		argument, echo, err := fundingLotsWriteValue('z', at)
		if err != nil || !fundingLotsProofTimeEqual(echo.(fundinglotverify.WalletLotsTime), fundinglotverify.WalletLotsTime{Value: at, Valid: true}) {
			t.Fatalf("native year %d", year)
		}
		// This is the exact pgx simple-protocol argument conversion. It must
		// preserve full PG finite years, including astronomical year zero.
		text, err := codec.Encode(0, pgx.TextFormatCode, argument, nil)
		if err != nil || len(text) == 0 || (year <= 0) != strings.HasSuffix(string(text), " BC") {
			t.Fatalf("pgx native parameter year %d: %v", year, err)
		}
		if !strings.Contains(string(text), "03:04:05.123456") {
			t.Fatal("microsecond timestamp truncated")
		}
	}
	for _, at := range []time.Time{time.Date(-4713, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC), time.Now().Truncate(time.Microsecond).Add(time.Nanosecond)} {
		if a, e, err := fundingLotsWriteValue('z', at); err != ErrExecution || a != nil || e != nil {
			t.Fatal("invalid native timestamp accepted")
		}
	}
}

func TestFundingLotsWriterGeneratedBudgetIndependentGolden(t *testing.T) {
	raw, err := os.ReadFile("../fundinglotinit/testdata/mixed_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var plan fundinglotinit.Plan
	if json.Unmarshal(raw, &plan) != nil {
		t.Fatal("independent golden decode")
	}
	// Hand-counted UTF8 bytes: memberships/openings/sources 3441;
	// operations/heads/lots/events/balances/allocations including stamps 3114.
	if got, err := fundingLotsGeneratedBytes(context.Background(), &plan); err != nil || got != 6555 {
		t.Fatalf("generated C0 projection bytes=%d error=%v", got, err)
	}
	plan.Metadata.AvailableTotal = strings.Repeat("x", 65537)
	if got, err := fundingLotsGeneratedBytes(context.Background(), &plan); err != nil || got != 6555 {
		t.Fatal("metadata double-counted instead of fixed reservation")
	}
	plan.LegacyOpenings[0].AccountID = nil
	if got, err := fundingLotsGeneratedBytes(context.Background(), &plan); err != nil || got != 6555-uint64(len("account/b/held")) {
		t.Fatal("NULL scalar incorrectly contributes bytes")
	}
	var empty fundinglotinit.Plan
	if got, err := fundingLotsGeneratedBytes(context.Background(), &empty); err != nil || got != 0 {
		t.Fatal("empty generated projection")
	}
}

func TestFundingLotsWriterGeneratedBudgetFailuresZero(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		ctx  context.Context
		plan *fundinglotinit.Plan
		want error
	}{
		{nil, &fundinglotinit.Plan{}, ErrInput}, {context.Background(), nil, ErrInput}, {ctx, &fundinglotinit.Plan{}, ErrTimeout},
		{context.Background(), &fundinglotinit.Plan{SettlementLots: []fundinglotverify.WalletLotsSettlementLotRow{{}}}, ErrInput},
		{context.Background(), &fundinglotinit.Plan{RefundLots: []fundinglotverify.WalletLotsRefundLotRow{{}}}, ErrInput},
		{context.Background(), &fundinglotinit.Plan{Heads: []fundinglotverify.WalletSequenceHeadRow{{TenantID: strings.Repeat("x", 65537)}}}, ErrResource},
		{context.Background(), &fundinglotinit.Plan{Heads: make([]fundinglotverify.WalletSequenceHeadRow, 100001)}, ErrResource},
	} {
		if n, err := fundingLotsGeneratedBytes(test.ctx, test.plan); n != 0 || err != test.want {
			t.Fatalf("failure amount=%d err=%v want=%v", n, err, test.want)
		}
	}
	plan := fundinglotinit.Plan{Heads: make([]fundinglotverify.WalletSequenceHeadRow, 257)}
	for i := 0; i < 256; i++ {
		plan.Heads[i].TenantID = strings.Repeat("x", 65536)
	}
	plan.Heads[256].TenantID = "x"
	if n, err := fundingLotsGeneratedBytes(context.Background(), &plan); n != 0 || err != ErrResource {
		t.Fatal("generated byte cap not enforced")
	}
	plan.Heads = plan.Heads[:256]
	if n, err := fundingLotsGeneratedBytes(context.Background(), &plan); n != 16<<20 || err != nil {
		t.Fatal("exact generated byte cap rejected")
	}
}
