package fundinglotscan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func wrID(n int) string     { return fmt.Sprintf("90000000-0000-4000-8000-%012d", n) }
func wrSHA(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func wrNull() cell          { return cell{kind: textKind} }
func wrNew(t *testing.T) *readerQuery {
	t.Helper()
	q := newReaderQuery(t, true)
	tables, _, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings(), walletBindings())
	if err != nil || len(tables) != 84 {
		t.Fatalf("wallet registry: tables=%d err=%v", len(tables), err)
	}
	for _, table := range tables {
		q.specs[table.spec.name] = table.spec
	}
	return q
}
func wrRecord(q *readerQuery, table string, pairs ...string) readerRecord {
	if len(pairs)%2 != 0 {
		q.t.Fatal("invalid independent fixture pairs")
	}
	values := readerRecord{}
	for n := 0; n < len(pairs); n += 2 {
		values[pairs[n]] = readerText(pairs[n+1])
	}
	return q.record(table, values)
}
func wrEpoch(q *readerQuery) {
	proof := []byte("independent wallet reader fixture")
	r := wrRecord(q, "funding_lot_epoch", "id", wrID(1), "schema_version", "1", "database_oid", "101", "schema_oid", "102", "basis_digest", strings.Repeat("a", 64), "manifest_digest", strings.Repeat("b", 64), "descriptor_digest", strings.Repeat("c", 64), "runtime_proof_digest", wrSHA(proof), "tenant_count", "0", "receipt_count", "0", "settlement_count", "0", "approved_refund_count", "0", "opening_count", "0", "legacy_source_count", "0", "available_total", "0", "held_total", "0", "actor", "offline-maintenance")
	r["singleton"] = cell{kind: boolKind, valid: true, boolean: true}
	r = wrRecord(q, "funding_lot_runtime_attestations", "epoch_id", wrID(1), "proof_version", "2", "mode", "ordinary", "database_oid", "101", "schema_oid", "102", "schema_owner_oid", "103", "app_role_oid", "104", "projector_role_oid", "105", "proof_sha256", wrSHA(proof), "manifest_digest", strings.Repeat("b", 64), "descriptor_digest", strings.Repeat("c", 64))
	r["singleton"] = cell{kind: boolKind, valid: true, boolean: true}
	r["worker_role_oid"], r["predecessor_payment_digest"] = wrNull(), wrNull()
	r["proof_canonical"] = cell{kind: bytesKind, valid: true, data: proof}
}

// These explicit amounts are a separate hand calculation, not a writer/FIFO
// planner: fund40 -> R30 -> C20/release10 -> F5 => A25,H0,S15,revision4.
func wrFunded(q *readerQuery) {
	wrEpoch(q)
	wrRecord(q, "tenants", "id", "tenant", "fl_created_xid", "101")
	for _, row := range [][3]string{{"available", "AVAILABLE", "25"}, {"held", "HELD", "0"}, {"clearing", "FUNDING_CLEARING", "-40"}, {"revenue", "REVENUE", "15"}} {
		r := wrRecord(q, "ledger_accounts", "id", row[0], "tenant_id", "tenant", "kind", row[1], "balance", row[2])
		if row[0] == "clearing" || row[0] == "revenue" {
			r["tenant_id"] = wrNull()
		}
	}
	for n, row := range [][6]string{{"fund", "credit", "credit", "credit", "checker", "201"}, {"reserve", "reserve", "reservation", "request", "customer", "202"}, {"finish", "settled", "settlement", "request", "system", "203"}, {"refund", "refund", "refund", "settlement", "checker", "204"}} {
		wrRecord(q, "journals", "id", row[0], "tenant_id", "tenant", "kind", row[1], "operation_id", row[2], "reference", row[3], "actor", row[4], "currency", "CNY", "created_txid", row[5])
		for k, line := range [][][2]string{{{"available", "40"}, {"clearing", "-40"}}, {{"available", "-30"}, {"held", "30"}}, {{"available", "10"}, {"held", "-30"}, {"revenue", "20"}}, {{"available", "5"}, {"revenue", "-5"}}}[n] {
			wrRecord(q, "journal_lines", "id", fmt.Sprintf("line-%d-%d", n, k), "journal_id", row[0], "account_id", line[0], "signed_delta", line[1])
		}
	}
	wrRecord(q, "funding_receipts", "id", "receipt", "tenant_id", "tenant", "source_kind", "manual_credit", "source_id", "credit", "amount", "40", "currency", "CNY", "journal_id", "fund")
	wrRecord(q, "request_records", "id", "request", "tenant_id", "tenant", "owner_id", "customer", "billing_status", "settled", "key_id", "key", "input_rate", "1000000", "output_rate", "1000000", "input_upper_bound", "30", "output_limit", "0")
	wrRecord(q, "reservations", "id", "reservation", "tenant_id", "tenant", "request_id", "request", "amount", "30", "status", "settled")
	wrRecord(q, "settlements", "id", "settlement", "tenant_id", "tenant", "request_id", "request", "charged_amount", "20", "raw_retail_amount", "20", "waived_retail_amount", "0", "refunded_amount", "5", "status", "settled", "reason", "usage_known", "journal_id", "finish")
	wrRecord(q, "refund_orders", "id", "refund", "tenant_id", "tenant", "settlement_id", "settlement", "amount", "5", "state", "approved", "requester", "maker", "approver", "checker", "journal_id", "refund")
	for n, row := range [][8]string{{"tenant_genesis", "tenant", "", "101", "0", "0", "0", "0"}, {"manual_credit", "receipt", "fund", "201", "40", "0", "0", "40"}, {"reserve", "reservation", "reserve", "202", "-30", "30", "0", "0"}, {"finish", "settlement", "finish", "203", "10", "-30", "20", "0"}, {"consumption_refund", "refund", "refund", "204", "5", "0", "-5", "0"}} {
		r := wrRecord(q, "funding_wallet_operations", "id", wrID(100+n), "tenant_id", "tenant", "epoch_id", wrID(1), "seq", fmt.Sprint(n), "kind", row[0], "source_id", row[1], "journal_id", row[2], "journal_operation_id", []string{"", "credit", "reservation", "settlement", "refund"}[n], "event_count", "1", "available_delta", row[4], "held_delta", row[5], "consumed_delta", row[6], "issued_delta", row[7], "created_xid", row[3], "previous_seq", fmt.Sprint(n-1), "previous_id", wrID(99+n))
		if n == 0 {
			r["previous_seq"], r["previous_id"], r["journal_id"], r["journal_operation_id"] = wrNull(), wrNull(), wrNull(), wrNull()
			r["event_count"] = readerText("0")
		}
	}
	wrRecord(q, "funding_wallet_heads", "tenant_id", "tenant", "epoch_id", wrID(1), "tail_seq", "4", "tail_operation_id", wrID(104))
	r := wrRecord(q, "funding_lots", "id", wrID(200), "tenant_id", "tenant", "origin_kind", "funding_receipt", "funding_receipt_id", "receipt", "issued_amount", "40", "currency", "CNY", "refund_class", "nonrefundable")
	r["legacy_opening_id"], r["legacy_refund_order_id"] = wrNull(), wrNull()
	for n, row := range [][10]string{{"funding", "fund", "credit", "201", "0", "0", "0", "40", "0", "0"}, {"reserve", "reserve", "reservation", "202", "40", "0", "0", "10", "30", "0"}, {"finish", "finish", "settlement", "203", "10", "30", "0", "20", "0", "20"}, {"consumption_refund", "refund", "refund", "204", "20", "0", "20", "25", "0", "15"}} {
		r := wrRecord(q, "funding_lot_events", "id", wrID(300+n), "tenant_id", "tenant", "lot_id", wrID(200), "wallet_operation_id", wrID(101+n), "wallet_seq", fmt.Sprint(n+1), "kind", row[0], "journal_id", row[1], "operation_id", row[2], "created_xid", row[3], "before_revision", fmt.Sprint(n), "after_revision", fmt.Sprint(n+1), "before_available", row[4], "before_held", row[5], "before_consumed", row[6], "after_available", row[7], "after_held", row[8], "after_consumed", row[9], "available_delta", []string{"40", "-30", "10", "5"}[n], "inference_held_delta", []string{"0", "30", "-30", "0"}[n], "consumed_delta", []string{"0", "0", "20", "-5"}[n], "issued_delta", []string{"40", "0", "0", "0"}[n], "previous_event_id", wrID(299+n))
		if n == 0 {
			r["previous_event_id"] = wrNull()
		}
	}
	wrRecord(q, "funding_lot_balances", "lot_id", wrID(200), "tenant_id", "tenant", "available_amount", "25", "inference_held_amount", "0", "consumed_amount", "15", "revision", "4", "last_event_id", wrID(303))
	wrRecord(q, "funding_reservation_lots", "id", wrID(400), "tenant_id", "tenant", "reservation_id", "reservation", "lot_id", wrID(200), "allocation_index", "1", "amount", "30", "created_xid", "202")
	wrRecord(q, "funding_settlement_lots", "id", wrID(401), "tenant_id", "tenant", "settlement_id", "settlement", "reservation_lot_id", wrID(400), "charged_amount", "20", "released_amount", "10", "created_xid", "203")
	wrRecord(q, "funding_consumption_refund_lots", "id", wrID(402), "tenant_id", "tenant", "refund_order_id", "refund", "lot_id", wrID(200), "settlement_lot_id", wrID(401), "allocation_index", "1", "amount", "5", "origin", "allocated", "created_xid", "204")
}
func wrEOF(t *testing.T, q *readerQuery) {
	t.Helper()
	seen := map[string]bool{}
	for _, r := range q.opened {
		if !r.eof || !r.errRead || !r.closed {
			t.Fatalf("incomplete EOF/Err/Close: %s", r.table)
		}
		seen[r.table] = true
	}
	if len(seen) != 84 {
		t.Fatalf("physical tables=%d want84", len(seen))
	}
}
func wrRead(t *testing.T, q *readerQuery) WalletFacts {
	t.Helper()
	facts, err := ReadWallet(context.Background(), q, "reader_fixture", ModeOrdinary)
	if err != nil {
		t.Fatal(err)
	}
	r := facts.Summary()
	if r.Scope != "complete_source_wallet_graphs_v1" || !r.PhysicalEOFComplete || r.ScannedTables != 84 {
		t.Fatalf("wrong wallet read scope/count: %+v", r)
	}
	wrEOF(t, q)
	return facts
}
func wrZero(t *testing.T, f WalletFacts, err, want error) {
	t.Helper()
	if err != want || !reflect.DeepEqual(f, WalletFacts{}) || f.Summary() != (WalletReport{}) {
		t.Fatalf("expected zero/%v, got %+v %v", want, f.Summary(), err)
	}
}

func TestReadWalletIndependentReplayAndSharedPhysicalRows(t *testing.T) {
	q := wrNew(t)
	wrFunded(q)
	f := wrRead(t, q)
	r := f.Summary()
	if !r.Wallet.RowsConsistent || r.Wallet.Scanned.Events != 4 || r.Wallet.Scanned.ReservationLots != 1 || r.Wallet.Scanned.SettlementLots != 1 || r.Wallet.Scanned.RefundLots != 1 {
		t.Fatalf("hand-calculated positive lot graph did not replay: %+v", r.Wallet)
	}
	for i, state := range r.Algorithms {
		want := "executed"
		if i == 6 {
			want = "not_applicable"
		}
		if state != want {
			t.Fatalf("algorithm%d=%s", i, state)
		}
	}
	counts := map[string]int{}
	var rows uint64
	for _, c := range q.calls {
		counts[c.table]++
		rows += uint64(c.count)
	}
	if rows != r.SourceRows {
		t.Fatal("physical rows charged per projection")
	}
	for table, count := range counts {
		if count != 1 {
			t.Fatalf("shared table %s read %d times", table, count)
		}
	}
	// The other source algorithms see these same incomplete supplied business
	// graphs. Their mismatches cannot suppress the independent lot algorithm.
	if r.Status != "mismatch" || r.InputMismatchFields != 0 {
		t.Fatalf("source and lot results were conflated: %+v", r)
	}
	for name, damage := range map[string]func(*readerQuery){
		"false_cache": func(q *readerQuery) {
			q.data["funding_lot_balances"][0]["available_amount"] = readerText("26")
			q.data["funding_lot_balances"][0]["consumed_amount"] = readerText("14")
		},
		"false_refund_edge": func(q *readerQuery) { q.data["funding_consumption_refund_lots"][0]["amount"] = readerText("4") },
		"missing_terminal":  func(q *readerQuery) { q.data["funding_settlement_lots"] = nil },
	} {
		t.Run(name, func(t *testing.T) {
			q := wrNew(t)
			wrFunded(q)
			damage(q)
			got := wrRead(t, q).Summary()
			if got.Algorithms[7] != "executed" || got.Wallet.RowsConsistent || got.Wallet.Status != "mismatch" {
				t.Fatalf("wallet core was skipped or trusted cached result: %+v", got.Wallet)
			}
		})
	}
}

func TestReadWalletEmptyEpochAndOldReadScope(t *testing.T) {
	q := wrNew(t)
	wrEpoch(q)
	r := wrRead(t, q).Summary()
	if r.Status != "consistent" || !r.CoverageComplete || !r.Wallet.RowsConsistent || r.SourceRows != 3 {
		t.Fatalf("valid supplied empty epoch: %+v", r)
	}
	q = wrNew(t)
	r = wrRead(t, q).Summary()
	if r.Status != "mismatch" || r.Algorithms[7] != "executed" || r.Wallet.Mismatches.EpochRows == 0 {
		t.Fatal("missing epoch became N/A or execution error")
	}
	old := newReaderQuery(t, true)
	f, err := Read(context.Background(), old, "reader_fixture", ModeOrdinary)
	if err != nil || f.Summary().ScannedTables != 68 || f.Summary().Scope != "complete_business_source_graphs_v1" {
		t.Fatal("old68 API changed", err)
	}
	assertReaderEOF(t, old)
	if _, ok := old.specs["tenants"].index["fl_created_xid"]; ok {
		t.Fatal("old68 registry gained protected tenant column")
	}
}

func TestReadWalletC0PaginationAndSingletons(t *testing.T) {
	for _, count := range []int{0, 1, 127, 128, 129, 256, 257} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			q := wrNew(t)
			for n := 0; n < count; n++ {
				wrRecord(q, "funding_wallet_legacy_sources", "kind", []string{"manual_credit", "reserve"}[n%2], "source_id", fmt.Sprintf("%04d", n/2))
				wrRecord(q, "funding_lots", "id", wrID(1000+n))
			}
			f := wrRead(t, q)
			calls := map[string]int{}
			for _, c := range q.calls {
				calls[c.table]++
			}
			for _, table := range []string{"funding_wallet_legacy_sources", "funding_lots"} {
				if calls[table] != count/128+1 {
					t.Fatalf("%s pages=%d", table, calls[table])
				}
			}
			if f.Summary().Wallet.Scanned.LegacySources != uint64(count) || f.Summary().Wallet.Scanned.Lots != uint64(count) {
				t.Fatal("physical page truncated")
			}
		})
	}
	q := wrNew(t)
	wrEpoch(q)
	r := wrRecord(q, "funding_lot_epoch", "id", wrID(2))
	r["singleton"] = cell{kind: boolKind, valid: true}
	r = wrRecord(q, "funding_lot_runtime_attestations", "epoch_id", wrID(2))
	r["singleton"] = cell{kind: boolKind, valid: true}
	r["proof_canonical"] = cell{kind: bytesKind}
	f := wrRead(t, q)
	if f.Summary().Wallet.Scanned.Epochs != 2 || f.Summary().Wallet.Scanned.Attestations != 2 {
		t.Fatal("false singleton hidden")
	}
}

func TestReadWalletNativeQualificationDoesNotSkipOtherCores(t *testing.T) {
	q := wrNew(t)
	wrEpoch(q)
	q.data["funding_lot_epoch"][0]["schema_version"] = wrNull()
	r := wrRead(t, q).Summary()
	if r.InputMismatchFields != 1 || r.Algorithms[7] != "input_mismatch" || r.Wallet != (fundinglotverify.WalletLotsRowsCheck{}) || r.CoverageComplete {
		t.Fatalf("required NULL fabricated bool/core: %+v", r)
	}
	for i := 0; i < 6; i++ {
		if r.Algorithms[i] != "executed" {
			t.Fatal("wallet input error skipped source algorithm")
		}
	}
	for name, stamp := range map[string]pgtype.Timestamptz{"null": {}, "infinity": {Valid: true, InfinityModifier: pgtype.Infinity}, "minus_infinity": {Valid: true, InfinityModifier: pgtype.NegativeInfinity}, "year10000": {Valid: true, Time: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}, "go_zero_finite": {Valid: true, Time: time.Time{}}} {
		t.Run(name, func(t *testing.T) {
			q := wrNew(t)
			wrEpoch(q)
			for _, table := range []string{"funding_lot_epoch", "funding_lot_runtime_attestations"} {
				q.data[table][0]["created_at"] = cell{kind: timeKind, valid: stamp.Valid, instant: stamp}
			}
			f := wrRead(t, q)
			r := f.Summary()
			if r.InputMismatchFields != 0 || r.Algorithms[7] != "executed" {
				t.Fatal("native envelope was replaced by unrepresentable DTO")
			}
			valid := stamp.Valid && stamp.InfinityModifier == 0
			if r.Wallet.RowsConsistent != valid {
				t.Fatalf("native timestamp semantics lost: %+v", r.Wallet)
			}
		})
	}
}

func TestReadWalletProofLimitsAndSharedBudget(t *testing.T) {
	for _, size := range []int{65537, 262144, 262145} {
		t.Run(fmt.Sprintf("c0_proof_%d", size), func(t *testing.T) {
			q := wrNew(t)
			wrEpoch(q)
			proof := make([]byte, size)
			q.data["funding_lot_runtime_attestations"][0]["proof_canonical"] = cell{kind: bytesKind, valid: true, data: proof}
			q.data["funding_lot_runtime_attestations"][0]["proof_sha256"] = readerText(wrSHA(proof))
			q.data["funding_lot_epoch"][0]["runtime_proof_digest"] = readerText(wrSHA(proof))
			f, err := ReadWallet(context.Background(), q, "reader_fixture", ModeOrdinary)
			if size > 262144 {
				wrZero(t, f, err, ErrResource)
			} else if err != nil || !f.Summary().Wallet.RowsConsistent {
				t.Fatalf("legal C0 proof rejected: %v %+v", err, f.Summary())
			}
		})
	}
	for _, table := range []string{"users", "payment_runtime_attestations"} {
		t.Run("ordinary_cap_"+table, func(t *testing.T) {
			q := wrNew(t)
			if table == "users" {
				wrRecord(q, table, "id", strings.Repeat("x", 65537))
			} else {
				r := q.record(table, readerRecord{})
				r["proof_canonical"] = cell{kind: bytesKind, valid: true, data: make([]byte, 65537)}
			}
			f, err := ReadWallet(context.Background(), q, "reader_fixture", ModeOrdinary)
			wrZero(t, f, err, ErrResource)
		})
	}
	t.Run("first_business_and_last_c0_share_bytes", func(t *testing.T) {
		q := wrNew(t)
		for n := 0; n < 255; n++ {
			wrRecord(q, "users", "id", strings.Repeat("x", 65530)+fmt.Sprintf("%06d", n))
		}
		wrRecord(q, "funding_consumption_refund_lots", "id", strings.Repeat("z", 65536))
		f, err := ReadWallet(context.Background(), q, "reader_fixture", ModeOrdinary)
		wrZero(t, f, err, ErrResource)
		if q.calls[len(q.calls)-1].table != "funding_consumption_refund_lots" {
			t.Fatal("shared byte allowance rejected early or reset")
		}
	})
	t.Run("business_and_c0_share_row_allowance", func(t *testing.T) {
		q := wrNew(t)
		for n := 0; n < 50000; n++ {
			wrRecord(q, "users", "id", fmt.Sprintf("u%05d", n))
			wrRecord(q, "funding_wallet_heads", "tenant_id", fmt.Sprintf("t%05d", n))
		}
		f, err := ReadWallet(context.Background(), q, "reader_fixture", ModeOrdinary)
		wrZero(t, f, err, ErrResource)
		if q.calls[len(q.calls)-1].table != "funding_wallet_heads" {
			t.Fatal("100000 row allowance was reset or charged per projection")
		}
	})
}

func TestReadWalletOwnedByteaAndRedaction(t *testing.T) {
	q := wrNew(t)
	wrEpoch(q)
	proof := q.data["funding_lot_runtime_attestations"][0]["proof_canonical"].data
	q.onClose = func(table string) {
		if table == "funding_lot_runtime_attestations" {
			proof[0] ^= 0xff
		}
	}
	f := wrRead(t, q)
	if !f.Summary().Wallet.RowsConsistent {
		t.Fatal("driver-owned canonical buffer leaked into session")
	}
	encoded, err := json.Marshal(f)
	if err != nil || string(encoded) != `{"source_wallet_union_facts":"redacted"}` || !strings.Contains(fmt.Sprintf("%v %#v", f, f), "redacted") {
		t.Fatal("raw wallet facts exposed")
	}
	for _, value := range []string{wrID(1), "independent wallet reader fixture"} {
		data, _ := json.Marshal(f.Summary())
		if strings.Contains(string(data), value) {
			t.Fatal("wallet summary leaks raw identity/proof")
		}
	}
	q = wrNew(t)
	wrEpoch(q)
	a := q.data["funding_lot_runtime_attestations"][0]
	a["singleton"] = cell{kind: boolKind, valid: true}
	a["proof_canonical"] = cell{kind: bytesKind}
	r := q.record("funding_lot_runtime_attestations", readerRecord{"singleton": {kind: boolKind, valid: true, boolean: true}, "proof_canonical": {kind: bytesKind, valid: true, data: []byte{}}})
	_ = r
	f = wrRead(t, q)
	table := f.state.raw.tables[f.state.raw.index["funding_lot_runtime_attestations"]]
	ci := table.spec.index["proof_canonical"]
	if table.rows[0][ci].valid || table.rows[0][ci].data != nil || !table.rows[1][ci].valid || table.rows[1][ci].data == nil {
		t.Fatal("NULL and empty proof bytea merged")
	}
}

func TestReadWalletFailuresAreEntirelyZero(t *testing.T) {
	var nilQuery *readerQuery
	f, err := ReadWallet(context.Background(), nilQuery, "reader_fixture", ModeOrdinary)
	wrZero(t, f, err, ErrInput)
	for _, stage := range []string{"query", "typed_nil_rows", "next", "scan", "err", "close", "sql_unqualified", "bad_time_wire"} {
		t.Run(stage, func(t *testing.T) {
			q := wrNew(t)
			wrFunded(q)
			q.fail, q.failAt = stage, "funding_consumption_refund_lots"
			q.timeWire = []byte{1}
			f, err := ReadWallet(context.Background(), q, "reader_fixture", ModeOrdinary)
			want := ErrExecution
			if stage == "sql_unqualified" {
				want = ErrResource
			}
			wrZero(t, f, err, want)
			for _, rows := range q.opened {
				if !rows.closed {
					t.Fatal("failure left open page")
				}
			}
		})
	}
	t.Run("late_cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		q := wrNew(t)
		wrFunded(q)
		q.onClose = func(table string) {
			if table == "funding_consumption_refund_lots" {
				cancel()
			}
		}
		f, err := ReadWallet(ctx, q, "reader_fixture", ModeOrdinary)
		wrZero(t, f, err, ErrCanceled)
	})
	t.Run("resource_survives_cleanup_cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		q := wrNew(t)
		q.record("funding_lot_runtime_attestations", readerRecord{"proof_canonical": {kind: bytesKind, valid: true, data: make([]byte, 262145)}})
		q.onClose = func(table string) {
			if table == "funding_lot_runtime_attestations" {
				cancel()
			}
		}
		f, err := ReadWallet(ctx, q, "reader_fixture", ModeOrdinary)
		wrZero(t, f, err, ErrResource)
	})
	t.Run("timeout_before_query", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
		defer cancel()
		q := wrNew(t)
		f, err := ReadWallet(ctx, q, "reader_fixture", ModeOrdinary)
		wrZero(t, f, err, ErrTimeout)
		if len(q.calls) != 0 {
			t.Fatal("expired caller still queried")
		}
	})
}
