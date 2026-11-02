package fundinglotscan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

type readerRecord map[string]cell

type readerCall struct {
	table string
	args  []any
	count int
}

// The fake resolves the actual SQL projection, not a DTO's declaration order.
// It supplies independent physical records and implements ordered pages with
// arbitrary-precision revision comparison rather than reader cursor helpers.
type readerQuery struct {
	t        *testing.T
	specs    map[string]tableSpec
	data     map[string][]readerRecord
	calls    []readerCall
	opened   []*readerRows
	fail     string
	failAt   string
	timeWire []byte
	onClose  func(string)
	prepare  bool
}

var readerTablePattern = regexp.MustCompile(`FROM ONLY "reader_fixture"\."([a-z_]+)" AS s`)
var readerColumnPattern = regexp.MustCompile(`CASE WHEN p\.__qualified THEN p\."([a-z_][a-z_0-9]*)" ELSE NULL END`)
var errReaderPrivateCause = errors.New("private_driver_fixture_cause")

func newReaderQuery(t *testing.T, ordinary bool) *readerQuery {
	t.Helper()
	tables, _, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings())
	if err != nil || len(tables) != 68 {
		t.Fatalf("fixed business registry: tables=%d err=%v", len(tables), err)
	}
	q := &readerQuery{t: t, specs: map[string]tableSpec{}, data: map[string][]readerRecord{}}
	for _, table := range tables {
		q.specs[table.spec.name] = table.spec
	}
	if ordinary {
		q.data["payment_policy"] = []readerRecord{{
			"singleton": {kind: boolKind, valid: true, boolean: true},
			"enabled":   {kind: boolKind, valid: true},
			"revision":  {kind: textKind, valid: true, text: "1"},
		}}
	}
	return q
}

func readerText(value string) cell { return cell{kind: textKind, valid: true, text: value} }

func (q *readerQuery) record(table string, values readerRecord) readerRecord {
	q.t.Helper()
	spec, ok := q.specs[table]
	if !ok {
		q.t.Fatalf("unknown physical fixture table %s", table)
	}
	r := readerRecord{}
	for _, column := range spec.columns {
		c := cell{kind: column.kind, valid: true}
		if column.kind == timeKind {
			c.instant = pgtype.Timestamptz{Valid: true, Time: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}
		}
		r[column.name] = c
	}
	for name, value := range values {
		if _, ok := spec.index[name]; !ok {
			q.t.Fatalf("unknown fixture column %s.%s", table, name)
		}
		r[name] = value
	}
	q.data[table] = append(q.data[table], r)
	return r
}

func readerKeyText(c cell) string {
	if c.kind == boolKind {
		return strconv.FormatBool(c.boolean)
	}
	return c.text
}

func readerCompare(a readerRecord, cursor []any, keys []key) int {
	for i, k := range keys {
		value := readerKeyText(a[k.name])
		if k.numeric {
			left, ok := new(big.Int).SetString(value, 10)
			if !ok {
				panic("invalid test numeric key")
			}
			right, ok := new(big.Int).SetString(fmt.Sprint(cursor[i]), 10)
			if !ok {
				panic("invalid test numeric cursor")
			}
			if cmp := left.Cmp(right); cmp != 0 {
				return cmp
			}
		} else if cmp := strings.Compare(value, fmt.Sprint(cursor[i])); cmp != 0 {
			return cmp
		}
	}
	return 0
}

func readerRecordKey(row readerRecord, keys []key) []any {
	result := make([]any, len(keys))
	for i, k := range keys {
		result[i] = readerKeyText(row[k.name])
	}
	return result
}

func (q *readerQuery) Query(sql string, args ...any) (Rows, error) {
	q.t.Helper()
	match := readerTablePattern.FindStringSubmatch(sql)
	if len(match) != 2 || !strings.Contains(sql, "LIMIT 128") || !strings.Contains(sql, "AS MATERIALIZED") {
		q.t.Fatalf("query is not a fixed bounded physical SELECT")
	}
	name := match[1]
	spec, ok := q.specs[name]
	if !ok || len(args) != len(spec.keys)+1 {
		q.t.Fatalf("unexpected physical table or key arity: %s", name)
	}
	if !q.prepare {
		for table, records := range q.data {
			keys := q.specs[table].keys
			sort.SliceStable(records, func(i, j int) bool {
				return readerCompare(records[i], readerRecordKey(records[j], keys), keys) < 0
			})
		}
		q.prepare = true
	}
	if q.failAt == name && q.fail == "query" {
		q.calls = append(q.calls, readerCall{table: name, args: append([]any(nil), args...)})
		return nil, errReaderPrivateCause
	}
	if q.failAt == name && q.fail == "typed_nil_rows" {
		q.calls = append(q.calls, readerCall{table: name, args: append([]any(nil), args...)})
		var rows *readerRows
		return rows, nil
	}
	columns := readerColumnPattern.FindAllStringSubmatch(sql, -1)
	if len(columns) != len(spec.columns) {
		q.t.Fatalf("query %s did not explicitly project its whole fixed column set", name)
	}
	columnNames := make([]string, len(columns))
	for i, column := range columns {
		columnNames[i] = column[1]
	}
	records := q.data[name]
	start := 0
	hasCursor, ok := args[0].(bool)
	if !ok {
		q.t.Fatal("first-page argument lost its independent bool")
	}
	if hasCursor {
		start = sort.Search(len(records), func(i int) bool { return readerCompare(records[i], args[1:], spec.keys) > 0 })
	}
	end := start + 128
	if end > len(records) {
		end = len(records)
	}
	q.calls = append(q.calls, readerCall{table: name, args: append([]any(nil), args...), count: end - start})
	rows := &readerRows{q: q, table: name, columns: columnNames, records: records[start:end]}
	q.opened = append(q.opened, rows)
	return rows, nil
}

type readerRows struct {
	q       *readerQuery
	table   string
	columns []string
	records []readerRecord
	pos     int
	pending bool
	eof     bool
	errRead bool
	closed  bool
}

func (r *readerRows) Next() (bool, error) {
	if r.pending || r.closed {
		r.q.t.Fatal("Next called before the prior Scan or after Close")
	}
	if r.q.failAt == r.table && r.q.fail == "next" {
		return false, errReaderPrivateCause
	}
	if r.pos == len(r.records) {
		r.eof = true
		return false, nil
	}
	r.pending = true
	r.pos++
	return true, nil
}

func (r *readerRows) Scan(dest ...any) error {
	if !r.pending || len(dest) != len(r.columns)+1 {
		r.q.t.Fatal("Scan violated the physical row contract")
	}
	r.pending = false
	if r.q.failAt == r.table && r.q.fail == "scan" {
		return errReaderPrivateCause
	}
	qualified := r.q.failAt != r.table || r.q.fail != "sql_unqualified"
	*dest[0].(*bool) = qualified
	for i, name := range r.columns {
		c, exists := r.records[r.pos-1][name]
		if !exists {
			r.q.t.Fatalf("physical fixture omitted %s.%s", r.table, name)
		}
		switch value := dest[i+1].(type) {
		case *pgtype.Text:
			if c.kind != textKind {
				r.q.t.Fatalf("%s.%s unexpectedly scanned as TEXT", r.table, name)
			}
			*value = pgtype.Text{String: c.text, Valid: c.valid}
		case **bool:
			if c.kind != boolKind {
				r.q.t.Fatalf("%s.%s unexpectedly scanned as bool", r.table, name)
			}
			if c.valid {
				b := c.boolean
				*value = &b
			} else {
				*value = nil
			}
		case *[]byte:
			switch c.kind {
			case timeKind:
				if r.q.failAt == r.table && r.q.fail == "bad_time_wire" {
					*value = r.q.timeWire
					break
				}
				codec := pgtype.TimestamptzCodec{}
				plan := codec.PlanEncode(nil, pgtype.TimestamptzOID, pgx.BinaryFormatCode, c.instant)
				if plan == nil {
					r.q.t.Fatal("native timestamp fixture has no fixed binary encoder")
				}
				var err error
				*value, err = plan.Encode(c.instant, nil)
				if err != nil {
					r.q.t.Fatalf("native timestamp fixture encoding: %v", err)
				}
			case bytesKind:
				*value = c.data
			default:
				r.q.t.Fatalf("%s.%s unexpectedly scanned as bytea", r.table, name)
			}
		default:
			r.q.t.Fatalf("unexpected private scalar destination %T", value)
		}
	}
	return nil
}

func (r *readerRows) Err() error {
	if !r.eof || r.closed {
		r.q.t.Fatal("Err called without real EOF or after Close")
	}
	r.errRead = true
	if r.q.failAt == r.table && r.q.fail == "err" {
		return errReaderPrivateCause
	}
	return nil
}

func (r *readerRows) Close() error {
	if r.closed {
		r.q.t.Fatal("Rows closed more than once")
	}
	r.closed = true
	if r.q.onClose != nil {
		r.q.onClose(r.table)
	}
	if r.q.failAt == r.table && r.q.fail == "close" {
		return errReaderPrivateCause
	}
	return nil
}

func assertReaderEOF(t *testing.T, q *readerQuery) {
	t.Helper()
	seen := map[string]bool{}
	for _, rows := range q.opened {
		if !rows.eof || !rows.errRead || !rows.closed {
			t.Fatalf("%s page did not complete real EOF/Err/Close", rows.table)
		}
		seen[rows.table] = true
	}
	if len(seen) != 68 {
		t.Fatalf("physical EOF tables = %d, want 68", len(seen))
	}
}

func assertReaderZero(t *testing.T, facts Facts, err, want error) {
	t.Helper()
	if err != want || facts.state != nil || facts.Summary() != (Report{}) {
		t.Fatalf("error must return fixed sentinel and zero facts: err=%v want=%v", err, want)
	}
}

func TestReadCompleteOrdinaryAndSandboxModes(t *testing.T) {
	t.Run("ordinary", func(t *testing.T) {
		q := newReaderQuery(t, true)
		facts, err := Read(context.Background(), q, "reader_fixture", ModeOrdinary)
		if err != nil {
			t.Fatal(err)
		}
		v := facts.Summary()
		if v.Scope != "complete_business_source_graphs_v1" || v.Mode != ModeOrdinary || v.Status != "consistent" || !v.PhysicalEOFComplete || !v.CoverageComplete || v.ScannedTables != 68 || v.SourceRows != 1 || v.SourceBytes != 3 || v.InputMismatchFields != 0 || v.Algorithms != ([7]string{"executed", "executed", "executed", "executed", "executed", "executed", "not_applicable"}) {
			t.Fatalf("ordinary empty graph/policy report: %+v", v)
		}
		if v.Sandbox != (fundinglotverify.SandboxSourceGraphCheck{}) {
			t.Fatal("ordinary N/A fabricated a sandbox core report")
		}
		assertReaderEOF(t, q)
		encoded, err := json.Marshal(facts)
		if err != nil || string(encoded) != `{"funding_source_union_facts":"redacted"}` {
			t.Fatal("raw facts were exported by JSON")
		}
	})
	t.Run("sandbox_empty_graph_is_data_mismatch", func(t *testing.T) {
		q := newReaderQuery(t, false)
		facts, err := Read(context.Background(), q, "reader_fixture", ModeSandbox)
		if err != nil {
			t.Fatal(err)
		}
		v := facts.Summary()
		if !v.PhysicalEOFComplete || v.Status != "mismatch" || v.Algorithms[6] != "executed" || v.Sandbox.Scope != "sandbox_stored_verified_source_graph_v1" || v.Sandbox.Status != "mismatch" {
			t.Fatalf("sandbox empty graph was skipped or mistaken for execution failure: %+v", v)
		}
		assertReaderEOF(t, q)
	})
	t.Run("ordinary_does_not_skip_query_conflicts", func(t *testing.T) {
		q := newReaderQuery(t, true)
		q.record("payment_query_conflicts", readerRecord{"id": readerText("orphan")})
		facts, err := Read(context.Background(), q, "reader_fixture", ModeOrdinary)
		if err != nil {
			t.Fatal(err)
		}
		v := facts.Summary()
		if v.Status != "mismatch" || v.CoverageComplete || v.OrdinaryBoundaryMismatches != 1 || v.Algorithms[6] != "input_mismatch" || v.Sandbox != (fundinglotverify.SandboxSourceGraphCheck{}) {
			t.Fatal("ordinary mode silently skipped stored payment history")
		}
		assertReaderEOF(t, q)
	})
}

func TestReadCompletePhysicalPagination(t *testing.T) {
	for _, count := range []int{0, 1, 127, 128, 129, 256, 257} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			q := newReaderQuery(t, true)
			for i := 0; i < count; i++ {
				value := ""
				if i > 0 {
					value = fmt.Sprintf("用户/%06d", i)
				}
				q.record("users", readerRecord{"id": readerText(value)})
			}
			facts, err := Read(context.Background(), q, "reader_fixture", ModeOrdinary)
			if err != nil || facts.Summary().SourceRows != uint64(count+1) {
				t.Fatalf("physical page count=%d err=%v", count, err)
			}
			var calls []readerCall
			for _, call := range q.calls {
				if call.table == "users" {
					calls = append(calls, call)
				}
			}
			if len(calls) != count/128+1 || calls[0].args[0] != false || calls[0].args[1] != "" {
				t.Fatal("first empty key or full-page continuation was lost")
			}
			for i := 1; i < len(calls); i++ {
				if calls[i].args[0] != true || calls[i].args[1] != fmt.Sprintf("用户/%06d", i*128-1) {
					t.Fatal("TEXT cursor did not retain the last actual physical key")
				}
			}
			assertReaderEOF(t, q)
		})
	}
}

func TestReadCompleteTupleAndFalseSingletonKeys(t *testing.T) {
	q := newReaderQuery(t, false)
	for i := 1; i <= 129; i++ {
		q.record("channel_version_credentials", readerRecord{"channel_version_id": readerText(""), "credential_id": readerText(fmt.Sprintf("凭据/%03d", i)), "position": readerText("1"), "weight": readerText("1"), "max_inflight": readerText("1")})
		q.record("procurement_evidence_identities", readerRecord{"attempt_id": readerText(""), "evidence_digest": readerText(fmt.Sprintf("%064d", i))})
		q.record("periodic_budget_configuration_versions", readerRecord{"control_id": readerText(""), "revision": readerText(strconv.Itoa(i))})
		q.record("payment_provider_revisions", readerRecord{"environment_id": readerText(""), "merchant_id": readerText("商户"), "order_id": readerText(""), "provider_order_revision": readerText(strconv.Itoa(i))})
	}
	q.record("payment_policy", readerRecord{"singleton": {kind: boolKind, valid: true}, "revision": readerText("1")})
	q.record("payment_policy", readerRecord{"singleton": {kind: boolKind, valid: true, boolean: true}, "revision": readerText("1")})
	facts, err := Read(context.Background(), q, "reader_fixture", ModeSandbox)
	if err != nil || facts.Summary().SourceRows != 518 {
		t.Fatalf("tuple/singleton physical enumeration: rows=%d err=%v", facts.Summary().SourceRows, err)
	}
	var continuations = map[string][]any{}
	for _, call := range q.calls {
		if call.args[0] == true {
			continuations[call.table] = call.args[1:]
		}
	}
	for table, want := range map[string][]any{
		"channel_version_credentials":            {"", "凭据/128"},
		"procurement_evidence_identities":        {"", fmt.Sprintf("%064d", 128)},
		"periodic_budget_configuration_versions": {"", int64(128)},
		"payment_provider_revisions":             {"", "商户", "", int64(128)},
	} {
		if !reflect.DeepEqual(continuations[table], want) {
			t.Fatalf("%s tuple cursor=%v want=%v", table, continuations[table], want)
		}
	}
	policy := facts.state.tables[facts.state.index["payment_policy"]]
	if len(policy.rows) != 2 || policy.rows[0][policy.spec.index["singleton"]].boolean || !policy.rows[1][policy.spec.index["singleton"]].boolean {
		t.Fatal("singleton false was filtered or boolean C ordering changed")
	}
	assertReaderEOF(t, q)
}

func TestReadCompleteNativeMismatchPreservesEOFAndOtherAlgorithms(t *testing.T) {
	for _, native := range []pgtype.Timestamptz{{}, {Valid: true, InfinityModifier: pgtype.Infinity}, {Valid: true, InfinityModifier: pgtype.NegativeInfinity}} {
		t.Run(fmt.Sprintf("valid_%v_infinity_%d", native.Valid, native.InfinityModifier), func(t *testing.T) {
			q := newReaderQuery(t, false)
			q.record("audit_events", readerRecord{"id": readerText("audit"), "created_at": {kind: timeKind, valid: native.Valid, instant: native}})
			facts, err := Read(context.Background(), q, "reader_fixture", ModeSandbox)
			if err != nil {
				t.Fatal(err)
			}
			v := facts.Summary()
			if !v.PhysicalEOFComplete || v.Status != "mismatch" || v.CoverageComplete || v.InputMismatchFields != 1 || v.Algorithms != ([7]string{"executed", "executed", "input_mismatch", "executed", "input_mismatch", "executed", "input_mismatch"}) {
				t.Fatalf("shared native mismatch scope/completion: %+v", v)
			}
			if v.Funding != (fundinglotverify.FundingSourceGraphCheck{}) || v.Procurement != (fundinglotverify.ProcurementEvidenceGraphCheck{}) || v.Sandbox != (fundinglotverify.SandboxSourceGraphCheck{}) || v.Periodic.Scope != "periodic_budget_graph_v1" || v.Periodic.Mismatches.InvalidTimeRows != 1 {
				t.Fatal("unrepresentable legacy DTO fabricated a core result or PB native input was skipped")
			}
			assertReaderEOF(t, q)
		})
	}
}

func TestReadCompleteResourceBoundaries(t *testing.T) {
	t.Run("single_text_exact_and_over", func(t *testing.T) {
		for _, size := range []int{65536, 65537} {
			q := newReaderQuery(t, true)
			q.record("users", readerRecord{"id": readerText(strings.Repeat("x", size))})
			facts, err := Read(context.Background(), q, "reader_fixture", ModeOrdinary)
			if size == 65537 {
				assertReaderZero(t, facts, err, ErrResource)
			} else if err != nil || facts.Summary().SourceBytes != 65539 {
				t.Fatalf("exact single field boundary: bytes=%d err=%v", facts.Summary().SourceBytes, err)
			}
		}
	})
	t.Run("single_bytea_exact_and_over", func(t *testing.T) {
		for _, size := range []int{65536, 65537} {
			q := newReaderQuery(t, false)
			q.record("payment_runtime_attestations", readerRecord{"proof_canonical": {kind: bytesKind, valid: true, data: []byte(strings.Repeat("x", size))}})
			facts, err := Read(context.Background(), q, "reader_fixture", ModeSandbox)
			if size == 65537 {
				assertReaderZero(t, facts, err, ErrResource)
			} else if err != nil || facts.Summary().SourceBytes != 65569 {
				t.Fatalf("exact bytea boundary/native accounting: bytes=%d err=%v", facts.Summary().SourceBytes, err)
			}
		}
	})
	t.Run("resource_failure_survives_cleanup_cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		q := newReaderQuery(t, true)
		q.record("users", readerRecord{"id": readerText(strings.Repeat("x", 65537))})
		q.onClose = func(table string) {
			if table == "users" {
				cancel()
			}
		}
		facts, err := Read(ctx, q, "reader_fixture", ModeOrdinary)
		assertReaderZero(t, facts, err, ErrResource)
		if ctx.Err() != context.Canceled || len(q.calls) != 1 || len(q.opened) != 1 || !q.opened[0].closed {
			t.Fatal("fixture did not cancel during early resource cleanup")
		}
	})
	t.Run("shared_physical_byte_budget", func(t *testing.T) {
		q := newReaderQuery(t, true)
		for i := 0; i < 256; i++ {
			q.record("users", readerRecord{"id": readerText(strings.Repeat("x", 65530) + fmt.Sprintf("%06d", i))})
		}
		facts, err := Read(context.Background(), q, "reader_fixture", ModeOrdinary)
		assertReaderZero(t, facts, err, ErrResource)
		if q.calls[len(q.calls)-1].table != "payment_policy" {
			t.Fatal("shared physical byte budget was charged repeatedly for domain projections or reset by table")
		}
	})
	t.Run("shared_physical_row_budget", func(t *testing.T) {
		q := newReaderQuery(t, true)
		for i := 0; i < 50000; i++ {
			q.record("users", readerRecord{"id": readerText(fmt.Sprintf("u%05d", i))})
			q.record("tenants", readerRecord{"id": readerText(fmt.Sprintf("t%05d", i))})
		}
		facts, err := Read(context.Background(), q, "reader_fixture", ModeOrdinary)
		assertReaderZero(t, facts, err, ErrResource)
		if q.calls[len(q.calls)-1].table != "payment_policy" {
			t.Fatal("100000 physical rows were rejected early or allowance reset for the next table")
		}
	})
}

func TestReadCompleteByteaNativeNullEmptyAndOwnedCopy(t *testing.T) {
	q := newReaderQuery(t, false)
	null := q.record("payment_runtime_attestations", readerRecord{"singleton": {kind: boolKind, valid: true}, "proof_canonical": {kind: bytesKind}})
	empty := q.record("payment_runtime_attestations", readerRecord{"singleton": {kind: boolKind, valid: true, boolean: true}, "proof_canonical": {kind: bytesKind, valid: true, data: []byte{}}})
	// A different bytea table also exercises non-UTF8 original bytes. The reader
	// must not apply its text interpretation to these canonical raw values.
	original := []byte{0xff, 0x00, 0x81, 'x'}
	q.record("payment_inbox", readerRecord{"id": readerText("raw"), "canonical_body": {kind: bytesKind, valid: true, data: original}})
	facts, err := Read(context.Background(), q, "reader_fixture", ModeSandbox)
	if err != nil {
		t.Fatal(err)
	}
	if null["proof_canonical"].data != nil || empty["proof_canonical"].data == nil {
		t.Fatal("test source did not preserve SQL NULL/empty bytea distinction")
	}
	attestation := facts.state.tables[facts.state.index["payment_runtime_attestations"]]
	proof := attestation.spec.index["proof_canonical"]
	if attestation.rows[0][proof].valid || attestation.rows[0][proof].data != nil || !attestation.rows[1][proof].valid || attestation.rows[1][proof].data == nil || len(attestation.rows[1][proof].data) != 0 {
		t.Fatal("native bytea NULL and non-NULL empty values were merged")
	}
	original[0] = 'a'
	inbox := facts.state.tables[facts.state.index["payment_inbox"]]
	data := inbox.rows[0][inbox.spec.index["canonical_body"]].data
	if !reflect.DeepEqual(data, []byte{0xff, 0x00, 0x81, 'x'}) {
		t.Fatal("owned source graph retained the driver's bytea buffer alias")
	}
	assertReaderEOF(t, q)
}

func TestReadCompleteFailuresReturnZeroFacts(t *testing.T) {
	t.Run("non_null_timestamp_wire_must_be_eight_bytes", func(t *testing.T) {
		for _, size := range []int{0, 1, 7, 9} {
			t.Run(strconv.Itoa(size), func(t *testing.T) {
				q := newReaderQuery(t, true)
				q.record("audit_events", readerRecord{"id": readerText("bad-wire")})
				q.fail, q.failAt = "bad_time_wire", "audit_events"
				q.timeWire = make([]byte, size)
				facts, err := Read(context.Background(), q, "reader_fixture", ModeOrdinary)
				assertReaderZero(t, facts, err, ErrExecution)
				if len(q.opened) == 0 || q.opened[len(q.opened)-1].table != "audit_events" || !q.opened[len(q.opened)-1].closed {
					t.Fatal("malformed native timestamp wire was retained or its page not closed")
				}
			})
		}
	})
	t.Run("typed_nil_query", func(t *testing.T) {
		var query *readerQuery
		facts, err := Read(context.Background(), query, "reader_fixture", ModeOrdinary)
		assertReaderZero(t, facts, err, ErrInput)
	})
	for _, stage := range []string{"query", "typed_nil_rows", "next", "scan", "err", "close", "sql_unqualified"} {
		t.Run(stage, func(t *testing.T) {
			q := newReaderQuery(t, true)
			q.record("users", readerRecord{"id": readerText("first")})
			q.fail, q.failAt = stage, "payment_policy"
			facts, err := Read(context.Background(), q, "reader_fixture", ModeOrdinary)
			want := ErrExecution
			if stage == "sql_unqualified" {
				want = ErrResource
			}
			assertReaderZero(t, facts, err, want)
			for _, rows := range q.opened {
				if !rows.closed {
					t.Fatalf("%s error left an open physical page", stage)
				}
			}
		})
	}
	t.Run("cancel_at_last_close", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		q := newReaderQuery(t, true)
		q.onClose = func(table string) {
			if table == "payment_review_facts" {
				cancel()
			}
		}
		facts, err := Read(ctx, q, "reader_fixture", ModeOrdinary)
		assertReaderZero(t, facts, err, ErrCanceled)
	})
	t.Run("later_failure_discards_native_mismatch", func(t *testing.T) {
		q := newReaderQuery(t, false)
		q.record("audit_events", readerRecord{"id": readerText("bad"), "created_at": {kind: timeKind, valid: true, instant: pgtype.Timestamptz{Valid: true, InfinityModifier: pgtype.Infinity}}})
		q.fail, q.failAt = "close", "payment_review_facts"
		facts, err := Read(context.Background(), q, "reader_fixture", ModeSandbox)
		assertReaderZero(t, facts, err, ErrExecution)
	})
	t.Run("duplicate_physical_key", func(t *testing.T) {
		q := newReaderQuery(t, true)
		q.record("users", readerRecord{"id": readerText("same")})
		q.record("users", readerRecord{"id": readerText("same")})
		facts, err := Read(context.Background(), q, "reader_fixture", ModeOrdinary)
		assertReaderZero(t, facts, err, ErrExecution)
		if len(q.opened) != 1 || !q.opened[0].closed {
			t.Fatal("non-progressing key was accepted or page was not closed")
		}
	})
	t.Run("expired_context", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		q := newReaderQuery(t, true)
		facts, err := Read(ctx, q, "reader_fixture", ModeOrdinary)
		assertReaderZero(t, facts, err, ErrTimeout)
		if len(q.calls) != 0 {
			t.Fatal("expired context still queried a business source")
		}
	})
}
