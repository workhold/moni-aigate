package fundinglotscan

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefix"
)

func prQuery(t *testing.T, wallet bool) *readerQuery {
	t.Helper()
	q := newReaderQuery(t, true)
	tables, _, err := prefixRegistry(wallet)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		q.specs[table.spec.name] = table.spec
	}
	return q
}
func prUpgrade(t *testing.T, q *readerQuery) {
	t.Helper()
	tables, _, err := prefixRegistry(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		q.specs[table.spec.name] = table.spec
	}
}
func prEOF(t *testing.T, q *readerQuery, count int) {
	t.Helper()
	seen := map[string]bool{}
	for _, r := range q.opened {
		if !r.eof || !r.errRead || !r.closed {
			t.Fatalf("incomplete physical lifecycle: %s", r.table)
		}
		seen[r.table] = true
	}
	if len(seen) != count {
		t.Fatalf("physical EOF count=%d want=%d", len(seen), count)
	}
}
func prSource(t *testing.T, q *readerQuery) PrefixFacts {
	t.Helper()
	f, err := ReadPrefix(context.Background(), q, "reader_fixture", ModeOrdinary)
	if err != nil {
		t.Fatal(err)
	}
	prEOF(t, q, 71)
	return f
}
func prWallet(t *testing.T, q *readerQuery) PrefixWalletFacts {
	t.Helper()
	f, err := ReadWalletPrefix(context.Background(), q, "reader_fixture", ModeOrdinary)
	if err != nil {
		t.Fatal(err)
	}
	prEOF(t, q, 87)
	return f
}

func TestPrefixReadOneUnionAndOldProfilesUnchanged(t *testing.T) {
	q := prQuery(t, false)
	f := prSource(t, q)
	r := f.Summary()
	if r.Scope != "complete_business_source_prefix_graphs_v1" || r.ScannedTables != 71 || r.ScannedColumns != 635 || r.Status != "consistent" || !r.CoverageComplete || r.PeriodicPrefix.Base != r.Periodic {
		t.Fatalf("wrong source report: %+v", r)
	}
	for i, state := range r.Algorithms {
		want := "executed"
		if i == 6 {
			want = "not_applicable"
		}
		if state != want {
			t.Fatalf("source algorithm %d=%s", i, state)
		}
	}
	wq := prQuery(t, true)
	wrEpoch(wq)
	w := prWallet(t, wq).Summary()
	if w.Scope != "complete_source_wallet_prefix_graphs_v1" || w.ScannedTables != 87 || w.ScannedColumns != 804 || w.Status != "consistent" || !w.CoverageComplete || w.PeriodicPrefix.Base != w.Periodic {
		t.Fatalf("wrong wallet report: %+v", w)
	}
	for i, state := range w.Algorithms {
		want := "executed"
		if i == 6 {
			want = "not_applicable"
		}
		if state != want {
			t.Fatalf("wallet algorithm %d=%s", i, state)
		}
	}
	counts := map[string]int{}
	var rows uint64
	for _, c := range wq.calls {
		counts[c.table]++
		rows += uint64(c.count)
	}
	for table, n := range counts {
		if n != 1 {
			t.Fatalf("shared physical table %s read %d times", table, n)
		}
	}
	if rows != w.SourceRows {
		t.Fatal("rows were counted per projection")
	}
	oldQ := newReaderQuery(t, true)
	old, err := Read(context.Background(), oldQ, "reader_fixture", ModeOrdinary)
	if err != nil || old.Summary().ScannedTables != 68 || old.Summary().Scope != "complete_business_source_graphs_v1" || len(old.Summary().Algorithms) != 7 {
		t.Fatal("old source profile changed", err)
	}
	oldWQ := wrNew(t)
	wrEpoch(oldWQ)
	oldW, err := ReadWallet(context.Background(), oldWQ, "reader_fixture", ModeOrdinary)
	if err != nil || oldW.Summary().ScannedTables != 84 || oldW.Summary().Scope != "complete_source_wallet_graphs_v1" || len(oldW.Summary().Algorithms) != 8 {
		t.Fatal("old wallet profile changed", err)
	}
}

func TestPrefixFactsHaveNoConvertibleOrOriginalOutlet(t *testing.T) {
	source, wallet := reflect.TypeFor[PrefixFacts](), reflect.TypeFor[PrefixWalletFacts]()
	if source.ConvertibleTo(wallet) || wallet.ConvertibleTo(source) {
		t.Fatal("source and wallet facts are convertible")
	}
	for _, typ := range []reflect.Type{source, wallet} {
		if typ.NumField() != 1 || typ.Field(0).IsExported() || typ.NumMethod() != 4 {
			t.Fatal("facts expose constructor/state/original outlet")
		}
	}
	for _, v := range []any{PrefixFacts{}, PrefixWalletFacts{}} {
		b, err := json.Marshal(v)
		if err != nil || !strings.Contains(string(b), "redacted") {
			t.Fatal("facts not redacted")
		}
	}
	if (PrefixFacts{}).Summary() != (PrefixReport{}) || (PrefixWalletFacts{}).Summary() != (PrefixWalletReport{}) {
		t.Fatal("zero facts not zero")
	}
}

func prObservation(q *readerQuery, id, kind string) readerRecord {
	r := wrRecord(q, "periodic_budget_observation_journal", "id", id, "control_id", wrID(1), "ordinal", "1", "kind", kind, "source_id", wrID(2), "event_seq", "0", "configuration_revision", "1", "configuration_operation_id", wrID(2), "created_xid", "10")
	for _, key := range []string{"previous_id", "last_event_id", "window_id", "window_revision", "window_last_event_id", "command_source_id", "window_source_id"} {
		r[key] = wrNull()
	}
	if kind == "command" {
		r["command_source_id"] = readerText(wrID(2))
	}
	if kind == "window_birth" {
		r["window_source_id"] = readerText(wrID(2))
		r["observed_at"] = cell{kind: timeKind}
	}
	return r
}

func TestPrefixGeneratedSourceDiscriminatorsCountEachField(t *testing.T) {
	tests := []struct {
		name, kind string
		change     func(readerRecord)
		want       uint64
	}{
		{"command_ok", "command", func(r readerRecord) {}, 0},
		{"command_missing", "command", func(r readerRecord) { r["command_source_id"] = wrNull() }, 1},
		{"command_wrong", "command", func(r readerRecord) { r["command_source_id"] = readerText(wrID(3)) }, 1},
		{"command_both_wrong", "command", func(r readerRecord) { r["command_source_id"] = wrNull(); r["window_source_id"] = readerText(wrID(2)) }, 2},
		{"birth_ok", "window_birth", func(r readerRecord) {}, 0},
		{"birth_missing", "window_birth", func(r readerRecord) { r["window_source_id"] = wrNull() }, 1},
		{"birth_both_wrong", "window_birth", func(r readerRecord) {
			r["window_source_id"] = readerText(wrID(3))
			r["command_source_id"] = readerText(wrID(2))
		}, 2},
		{"invalid_kind", "invalid", func(r readerRecord) {}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := prQuery(t, true)
			wrEpoch(q)
			row := prObservation(q, wrID(300), tc.kind)
			tc.change(row)
			r := prWallet(t, q).Summary()
			if r.ObservationSourceMismatches != tc.want || r.InputMismatchFields != 0 || r.Status != "mismatch" || r.Algorithms[8] != "executed" || r.Algorithms[7] != "executed" {
				t.Fatalf("discriminator incorrectly classified: %+v", r)
			}
			if !r.Wallet.RowsConsistent {
				t.Fatal("prefix mismatch suppressed wallet execution")
			}
		})
	}
}

func TestPrefixNewTablePaginationNativeAndExecutionFailures(t *testing.T) {
	q := prQuery(t, false)
	for i := 0; i < 257; i++ {
		prObservation(q, wrID(i+500), "command")
	}
	r := prSource(t, q).Summary()
	var pages []readerCall
	for _, call := range q.calls {
		if call.table == "periodic_budget_observation_journal" {
			pages = append(pages, call)
		}
	}
	if len(pages) != 3 || pages[0].count != 128 || pages[1].count != 128 || pages[2].count != 1 || pages[0].args[0] != false || pages[1].args[0] != true || r.PeriodicPrefix.Scanned.Observations != 257 {
		t.Fatal("new table pagination not complete")
	}
	for _, wire := range []struct {
		name  string
		stamp pgtype.Timestamptz
	}{
		{"null", pgtype.Timestamptz{}}, {"infinity", pgtype.Timestamptz{Valid: true, InfinityModifier: pgtype.Infinity}}, {"wide_year", pgtype.Timestamptz{Valid: true, Time: time.Date(12000, 1, 1, 0, 0, 0, 0, time.UTC)}},
	} {
		t.Run(wire.name, func(t *testing.T) {
			q := prQuery(t, false)
			row := prObservation(q, wrID(500), "command")
			row["observed_at"] = cell{kind: timeKind, valid: wire.stamp.Valid, instant: wire.stamp}
			r := prSource(t, q).Summary()
			if r.InputMismatchFields != 0 || r.Algorithms[7] != "executed" || wire.name != "wide_year" && r.PeriodicPrefix.Mismatches.StructuralMismatches == 0 {
				t.Fatal("native shape replaced by transport sentinel", r)
			}
		})
	}
	for _, failure := range []string{"query", "typed_nil_rows", "next", "scan", "err", "close"} {
		t.Run(failure, func(t *testing.T) {
			q := prQuery(t, false)
			prObservation(q, wrID(500), "command")
			q.failAt, q.fail = "periodic_budget_observation_journal", failure
			f, err := ReadPrefix(context.Background(), q, "reader_fixture", ModeOrdinary)
			if err != ErrExecution || f.state != nil || f.Summary() != (PrefixReport{}) {
				t.Fatalf("failure not zero: %v", err)
			}
		})
	}
	q = prQuery(t, false)
	q.record("periodic_budget_observation_origins", readerRecord{"control_id": readerText(wrID(1))})
	q.timeWire = []byte{1}
	q.failAt, q.fail = "periodic_budget_observation_origins", "bad_time_wire"
	f, err := ReadPrefix(context.Background(), q, "reader_fixture", ModeOrdinary)
	if err != ErrExecution || f.state != nil {
		t.Fatal("malformed native time not zero", err)
	}
}

func TestPrefixSharedPhysicalBudgetsIncludeGenerated(t *testing.T) {
	// Fill the entire physical union to one byte above the cap. Without the
	// command discriminator, these exact old and new rows fit the shared cap.
	q := prQuery(t, false)
	row := prObservation(q, wrID(500), "command")
	for n := 0; n < 255; n++ {
		q.record("audit_events", readerRecord{"id": readerText(fmt.Sprintf("audit-%04d", n)), "sanitized_diff": readerText(strings.Repeat("a", fieldLimit))})
	}
	filler := q.record("audit_events", readerRecord{"id": readerText("filler")})
	total := 0
	for table, records := range q.data {
		for _, record := range records {
			for _, col := range q.specs[table].columns {
				c := record[col.name]
				switch c.kind {
				case timeKind:
					total += 32
				case boolKind:
					total++
				case textKind:
					if c.valid {
						total += len(c.text)
					}
				case bytesKind:
					if c.valid {
						total += len(c.data)
					}
				}
			}
		}
	}
	need := byteLimit - total + 1
	if need <= 0 || need > fieldLimit {
		t.Fatal("independent shared budget vector invalid", need)
	}
	filler["sanitized_diff"] = readerText(strings.Repeat("a", need))
	f, err := ReadPrefix(context.Background(), q, "reader_fixture", ModeOrdinary)
	if err != ErrResource || f.state != nil {
		t.Fatal("generated discriminator escaped cumulative budget", err)
	}
	row["command_source_id"] = wrNull()
	q.calls, q.opened, q.prepare = nil, nil, false
	tables, index, err := prefixRegistry(false)
	if err != nil {
		t.Fatal(err)
	}
	raw := &session{ctx: context.Background(), query: q, schema: "reader_fixture", mode: ModeOrdinary, tables: tables, index: index}
	if err = raw.readAll(); err != nil || raw.bytes != byteLimit+1-36 {
		t.Fatal("same physical vector without generated value did not fit", raw.bytes, err)
	}

	q = prQuery(t, false)
	row = prObservation(q, wrID(500), "command")
	row["command_source_id"] = readerText(strings.Repeat("g", fieldLimit+1))
	f, err = ReadPrefix(context.Background(), q, "reader_fixture", ModeOrdinary)
	if err != ErrResource || f.state != nil {
		t.Fatal("generated field limit not enforced", err)
	}
	// Exercise the shared physical row limit without paying pure-checker work.
	q = prQuery(t, false)
	for i := 0; i < 99999; i++ {
		q.record("users", readerRecord{"id": readerText(fmt.Sprintf("u%06d", i))})
	}
	q.record("periodic_budget_observation_heads", readerRecord{"control_id": readerText(wrID(1))})
	f, err = ReadPrefix(context.Background(), q, "reader_fixture", ModeOrdinary)
	if err != ErrResource || f.state != nil || q.calls[len(q.calls)-1].table != "periodic_budget_observation_heads" {
		t.Fatal("new table row escaped shared row budget", err)
	}
}

func TestPrefixInputContextAndMismatchPrecedence(t *testing.T) {
	for _, wallet := range []bool{false, true} {
		q := prQuery(t, wallet)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if wallet {
			f, err := ReadWalletPrefix(ctx, q, "reader_fixture", ModeOrdinary)
			if err != ErrCanceled || f.state != nil {
				t.Fatal(err)
			}
			f, err = ReadWalletPrefix(nil, q, "reader_fixture", ModeOrdinary)
			if err != ErrInput || f.state != nil {
				t.Fatal(err)
			}
		} else {
			f, err := ReadPrefix(ctx, q, "reader_fixture", ModeOrdinary)
			if err != ErrCanceled || f.state != nil {
				t.Fatal(err)
			}
			f, err = ReadPrefix(nil, q, "reader_fixture", ModeOrdinary)
			if err != ErrInput || f.state != nil {
				t.Fatal(err)
			}
		}
	}
	q := prQuery(t, true)
	wrFunded(q)
	r := prWallet(t, q).Summary()
	if r.Status != "mismatch" || !r.Wallet.RowsConsistent || r.Algorithms[8] != "executed" {
		t.Fatal("unrelated source mismatch was relaxed", r)
	}
	// Independent aggregate proof: only the stronger prefix scope can discharge
	// the old PB snapshot gap; it leaves all old child fields untouched.
	q = prQuery(t, false)
	f := prSource(t, q)
	s := f.state.raw
	s.report.Periodic.Status = "indeterminate"
	s.report.Periodic.Balanced = false
	s.report.Periodic.CoverageComplete = false
	s.report.Periodic.Unproven.OperationSnapshotPrefixUnproven = 1
	p := f.Summary().PeriodicPrefix
	p.Base = s.report.Periodic
	p.CertifiedCommands = 1
	got := makePrefixReport(s, p, "executed", 0)
	if got.Status != "consistent" || !got.CoverageComplete || got.Periodic.Unproven.OperationSnapshotPrefixUnproven != 1 {
		t.Fatal("old aggregate leaked into new qualification", got)
	}
	p.Unproven.LegacyMembershipUnproven = 1
	p.Balanced = false
	p.CoverageComplete = false
	p.Status = "indeterminate"
	got = makePrefixReport(s, p, "executed", 0)
	if got.Status != "indeterminate" || got.CoverageComplete {
		t.Fatal("legacy gap was cleared")
	}
	p.Mismatches = periodicbudgetprefix.Mismatches{CutMismatches: 1}
	p.Status = "mismatch"
	got = makePrefixReport(s, p, "executed", 0)
	if got.Status != "mismatch" {
		t.Fatal("indeterminate hid mismatch")
	}
}

// A hand-calculated nonempty history: first configure observes no current
// window; later an empty window is born at the same financial cut. The old
// graph cannot prove that ordering and must keep its one snapshot gap.
func prEmptyWindowHistory(q *readerQuery) {
	at := func(value string) cell {
		stamp, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			panic(err)
		}
		return cell{kind: timeKind, valid: true, instant: pgtype.Timestamptz{Valid: true, Time: stamp}}
	}
	first := at("2026-10-01T08:00:00Z")
	day := at("2026-10-01T00:00:00Z")
	next := at("2026-10-02T00:00:00Z")
	wrRecord(q, "users", "id", wrID(9))
	wrRecord(q, "tenants", "id", wrID(2))
	c := wrRecord(q, "periodic_budget_controls", "id", wrID(1), "scope", "tenant", "tenant_id", wrID(2), "period_kind", "utc_day", "limit_amount", "1000", "revision", "1", "held_amount", "0", "spent_amount", "0", "event_seq", "0", "created_xid", "10", "configuration_operation_id", wrID(4))
	c["key_id"], c["account_id"], c["last_event_id"] = wrNull(), wrNull(), wrNull()
	c["enabled"] = cell{kind: boolKind, valid: true, boolean: true}
	c["tracking_started_at"], c["effective_from"], c["max_observed_window_start"] = first, next, day
	op := wrRecord(q, "periodic_budget_operations", "operation_id", wrID(4), "kind", "tenant_periodic_budget.configure", "actor", wrID(9), "scope", "platform", "target", "/api/platform/tenants/"+wrID(2)+"/periodic-budget", "resource_id", wrID(1), "revision", "1", "created_xid", "10", "observation_id", wrID(101))
	op["created_at"] = first
	op["safe_input"] = readerText(`{"expected_revision":"0","period_kind":"utc_day","enabled":true,"limit_amount":"1000"}`)
	op["result_snapshot"] = readerText(fmt.Sprintf(`{"scope":"tenant","scope_id":%q,"revision":"1","period_kind":"utc_day","enabled":true,"limit_amount":"1000","state":"scheduled","effective_from":"2026-10-02T00:00:00.000000Z","tracking_started_at":"2026-10-01T08:00:00.000000Z","as_of":"2026-10-01T08:00:00.000000Z","current_window":null,"carry_held":"0"}`, wrID(2)))
	var encoded []byte
	for _, part := range []string{"tenant_periodic_budget.configure", wrID(9), "platform", "/api/platform/tenants/" + wrID(2) + "/periodic-budget", "0", "utc_day", "true", "1000"} {
		encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(part)))
		encoded = append(encoded, part...)
	}
	digest := sha256.Sum256(encoded)
	op["input_fingerprint"] = readerText(hex.EncodeToString(digest[:]))
	v := wrRecord(q, "periodic_budget_configuration_versions", "control_id", wrID(1), "revision", "1", "operation_id", wrID(4), "period_kind", "utc_day", "limit_amount", "1000", "actor", wrID(9), "created_xid", "10")
	v["enabled"] = cell{kind: boolKind, valid: true, boolean: true}
	v["created_at"] = first
	audit := wrRecord(q, "audit_events", "id", wrID(201), "actor", wrID(9), "action", "periodic_budget.configured", "target", wrID(4), "sanitized_diff", fmt.Sprintf(`{"control_id":%q,"revision":"1","scope":"tenant","scope_id":%q,"changed":true}`, wrID(1), wrID(2)))
	audit["request_id"], audit["tenant_scope"] = readerText(""), wrNull()
	audit["created_at"] = first
	origin := wrRecord(q, "periodic_budget_observation_origins", "control_id", wrID(1), "version", "1", "created_xid", "10")
	origin["tracking_started_at"] = first
	wrRecord(q, "periodic_budget_observation_heads", "control_id", wrID(1), "ordinal", "2", "last_observation_id", wrID(102), "updated_xid", "20")
	command := prObservation(q, wrID(101), "command")
	command["source_id"], command["command_source_id"], command["configuration_operation_id"] = readerText(wrID(4)), readerText(wrID(4)), readerText(wrID(4))
	command["observed_at"] = first
	birth := prObservation(q, wrID(102), "window_birth")
	birth["source_id"], birth["window_source_id"], birth["configuration_operation_id"] = readerText(wrID(14)), readerText(wrID(14)), readerText(wrID(4))
	birth["ordinal"], birth["previous_id"], birth["window_id"], birth["window_revision"], birth["created_xid"] = readerText("2"), readerText(wrID(101)), readerText(wrID(14)), readerText("0"), readerText("20")
	win := wrRecord(q, "periodic_budget_windows", "id", wrID(14), "control_id", wrID(1), "held_amount", "0", "spent_amount", "0", "revision", "0", "observation_id", wrID(102), "observation_created_xid", "20")
	win["last_event_id"], win["start_at"], win["end_at"] = wrNull(), day, next
}

func TestPrefixNonemptyOriginalGapRetainedAndLegacyOriginClosed(t *testing.T) {
	q := prQuery(t, false)
	prEmptyWindowHistory(q)
	f := prSource(t, q)
	r := f.Summary()
	if r.Status != "consistent" || !r.CoverageComplete || r.Periodic.Status != "indeterminate" || r.Periodic.CoverageComplete || r.Periodic.Unproven.OperationSnapshotPrefixUnproven != 1 || r.PeriodicPrefix.Base != r.Periodic || !r.PeriodicPrefix.Balanced || r.PeriodicPrefix.CertifiedCommands != 1 || r.PeriodicPrefix.Unproven != (periodicbudgetprefix.Gaps{}) {
		t.Fatalf("nonempty stronger scope did not preserve old gap: %+v", r)
	}
	if out, err := ProjectLegacySeedPrefix(context.Background(), f); err != nil || len(out.Tenants) != 1 {
		t.Fatal("prefix-qualified seed rejected retained old gap", err)
	}
	// A legacy graph with no witness is indeterminate. It cannot acquire origins
	// merely because its old source totals happen to be zero.
	q = prQuery(t, false)
	prEmptyWindowHistory(q)
	q.data["periodic_budget_observation_origins"], q.data["periodic_budget_observation_heads"], q.data["periodic_budget_observation_journal"] = nil, nil, nil
	q.data["periodic_budget_operations"][0]["observation_id"] = wrNull()
	q.data["periodic_budget_windows"][0]["observation_id"], q.data["periodic_budget_windows"][0]["observation_created_xid"] = wrNull(), wrNull()
	legacy := prSource(t, q)
	r = legacy.Summary()
	if r.Status != "indeterminate" || r.CoverageComplete || r.PeriodicPrefix.Unproven.UncoveredControls != 1 || r.PeriodicPrefix.Unproven.OperationSnapshotPrefixUnproven != 1 {
		t.Fatal("legacy control silently authenticated", r)
	}
	if _, err := ProjectLegacySeedPrefix(context.Background(), legacy); err != ErrInput {
		t.Fatal("legacy data got writable seed", err)
	}
}

func TestPrefixLogicalBudgetIndependentAndInputMismatchBase(t *testing.T) {
	// These 200 windows fit the unique physical union and original graph budget.
	// Their IDs occur again in the new pure binding array, which must be charged
	// independently; a successful physical read cannot bypass its 16MiB cap.
	q := prQuery(t, false)
	for i := 0; i < 200; i++ {
		row := wrRecord(q, "periodic_budget_windows", "id", fmt.Sprintf("%04d", i)+strings.Repeat("x", fieldLimit-4), "control_id", wrID(1), "held_amount", "0", "spent_amount", "0", "revision", "0")
		row["last_event_id"], row["observation_id"], row["observation_created_xid"] = wrNull(), wrNull(), wrNull()
	}
	f, err := ReadPrefix(context.Background(), q, "reader_fixture", ModeOrdinary)
	if err != ErrResource || f.state != nil {
		t.Fatal("pure prefix logical budget was bypassed", err)
	}
	prEOF(t, q, 71)
	q = prQuery(t, false)
	origin := wrRecord(q, "periodic_budget_observation_origins", "control_id", wrID(1), "created_xid", "10")
	origin["version"] = wrNull()
	r := prSource(t, q).Summary()
	if r.InputMismatchFields != 1 || r.Algorithms[7] != "input_mismatch" || r.PeriodicPrefix.Base != r.Periodic || r.Periodic.Scope != "periodic_budget_graph_v1" || r.Status != "mismatch" {
		t.Fatal("unqualified prefix lost old base or fabricated an execution", r)
	}
	for i, state := range r.Algorithms[:7] {
		want := "executed"
		if i == 6 {
			want = "not_applicable"
		}
		if state != want {
			t.Fatal("prefix input gap suppressed another core")
		}
	}
	for _, xid := range []string{"-1", "01", "18446744073709551616"} {
		t.Run(xid, func(t *testing.T) {
			q := prQuery(t, false)
			wrRecord(q, "periodic_budget_observation_origins", "control_id", wrID(1), "version", "1", "created_xid", xid)
			report := prSource(t, q).Summary()
			if report.InputMismatchFields != 0 || report.Algorithms[7] != "executed" || report.PeriodicPrefix.Mismatches.StructuralMismatches == 0 {
				t.Fatal("xid shape misclassified", report)
			}
		})
	}
}
