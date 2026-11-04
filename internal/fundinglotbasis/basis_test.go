package fundinglotbasis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/migrations"
)

func ptr(s string) *string { return &s }
func baseEnvironment() environment {
	return environment{DatabaseOID: 17, SchemaOID: 23, ServerVersionNum: 160015, DatabaseEncoding: 6, ServerEncoding: "UTF8", ClientEncoding: "UTF8", DatabaseEncodingName: "UTF8", Provider: "c", Collate: "C", Ctype: "C"}
}

type mockRows struct {
	data                               [][]any
	index                              int
	nextErr, scanErr, endErr, closeErr error
	closed                             int
	onClose                            func()
	onNext                             func(int)
}

func (r *mockRows) Next() (bool, error) {
	if r.onNext != nil {
		r.onNext(r.index)
	}
	if r.nextErr != nil {
		return false, r.nextErr
	}
	if r.index >= len(r.data) {
		return false, nil
	}
	r.index++
	return true, nil
}
func (r *mockRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.index < 1 || r.index > len(r.data) || len(dest) != len(r.data[r.index-1]) {
		return errors.New("test scan")
	}
	for i, v := range r.data[r.index-1] {
		d := reflect.ValueOf(dest[i]).Elem()
		if v == nil {
			d.SetZero()
		} else {
			d.Set(reflect.ValueOf(v))
		}
	}
	return nil
}
func (r *mockRows) Err() error { return r.endErr }
func (r *mockRows) Close() error {
	r.closed++
	if r.onClose != nil {
		r.onClose()
	}
	return r.closeErr
}

type wireRow struct {
	key, row  []byte
	qualified bool
}
type mockQuery struct {
	m        *materials
	env      environment
	meta     map[string][][]any
	data     map[string][]wireRow
	counts   map[string]int64
	log      []string
	pages    map[string]int
	hook     func(string, []any, *mockRows) (Rows, error)
	queryErr bool
}

func metadataRow(v any) []any { b, _ := json.Marshal(v); s := string(b); return []any{true, true, &s} }
func newMock(t *testing.T) *mockQuery {
	t.Helper()
	m, e := loadMaterials()
	if e != nil {
		t.Fatal(e)
	}
	q := &mockQuery{m: m, env: baseEnvironment(), meta: map[string][][]any{}, data: map[string][]wireRow{}, counts: map[string]int64{}, pages: map[string]int{}}
	for _, tb := range m.tables {
		q.meta["relations"] = append(q.meta["relations"], metadataRow(relationMetadata{Name: tb.name, Kind: "r", Persistence: "p", AccessMethodOID: 2}))
		for _, c := range tb.columns {
			q.meta["attributes"] = append(q.meta["attributes"], metadataRow(attributeMetadata{Table: tb.name, Ordinal: c.ordinal, Name: c.name, OID: c.oid, Typmod: c.typmod, NotNull: c.notNull, CollationOID: c.collation}))
		}
		q.meta["primary_keys"] = append(q.meta["primary_keys"], metadataRow(primaryKeyMetadata{Table: tb.name, KeyCount: int32(len(tb.primaryKey)), Unique: true, Valid: true, Ready: true, Live: true, Immediate: true, Plain: true, Keys: tb.primaryKey}))
	}
	for _, v := range m.send.Types {
		q.meta["types"] = append(q.meta["types"], metadataRow(v))
	}
	for _, v := range m.send.Functions {
		q.meta["functions"] = append(q.meta["functions"], metadataRow(v))
	}
	for _, v := range m.manifest {
		q.meta["manifest"] = append(q.meta["manifest"], metadataRow(v))
	}
	return q
}
func marker(sql string) string {
	a := strings.Index(sql, "funding_basis.")
	if a < 0 {
		return ""
	}
	a += len("funding_basis.")
	b := strings.Index(sql[a:], ".v1")
	if b < 0 {
		return ""
	}
	return sql[a : a+b]
}
func (q *mockQuery) Query(sql string, args ...any) (Rows, error) {
	kind := marker(sql)
	q.log = append(q.log, kind)
	r := &mockRows{}
	switch kind {
	case "environment":
		r.data = [][]any{metadataRow(q.env)}
	case "relations", "attributes", "primary_keys", "types", "functions", "manifest":
		r.data = q.meta[kind]
	case "count", "page":
		var name string
		for _, tb := range q.m.tables {
			if strings.Contains(sql, pgx.Identifier{"test_schema", tb.name}.Sanitize()) {
				name = tb.name
				break
			}
		}
		if name == "" {
			return nil, errors.New("test missing table")
		}
		if kind == "count" {
			n := int64(len(q.data[name]))
			if v, ok := q.counts[name]; ok {
				n = v
			}
			r.data = [][]any{{n}}
		} else {
			q.pages[name]++
			cursor := args[1].([]byte)
			has := args[0].(bool)
			records := q.data[name]
			count := 0
			for _, v := range records {
				if has && bytes.Compare(v.key, cursor) <= 0 {
					continue
				}
				r.data = append(r.data, []any{v.qualified, v.key, v.row})
				count++
				if count == 128 {
					break
				}
			}
		}
	default:
		return nil, errors.New("test query marker")
	}
	if q.hook != nil {
		return q.hook(kind, args, r)
	}
	return r, nil
}
func tableByName(t *testing.T, m *materials, name string) table {
	t.Helper()
	for _, v := range m.tables {
		if v.name == name {
			return v
		}
	}
	t.Fatal("missing table")
	return table{}
}
func nativeDefault(c column) []byte {
	if !c.notNull {
		return nil
	}
	switch c.oid {
	case 16:
		return []byte{0}
	case 20, 1184, 5069:
		return make([]byte, 8)
	case 21:
		return make([]byte, 2)
	case 23, 26:
		return make([]byte, 4)
	case 2950:
		return make([]byte, 16)
	case 1700:
		return make([]byte, 8)
	case 3802:
		return []byte{1, '{', '}'}
	case 1009:
		return []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 25}
	case 869:
		return []byte{2, 32, 0, 4, 127, 0, 0, 1}
	default:
		return []byte("value")
	}
}
func recordBytes(cols []column, values [][]byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, int32(len(cols)))
	for i, c := range cols {
		_ = binary.Write(&b, binary.BigEndian, c.oid)
		n := int32(-1)
		if values[i] != nil {
			n = int32(len(values[i]))
		}
		_ = binary.Write(&b, binary.BigEndian, n)
		b.Write(values[i])
	}
	return b.Bytes()
}
func makeWire(tb table, overrides map[string][]byte) wireRow {
	values := make([][]byte, len(tb.columns))
	for i, c := range tb.columns {
		values[i] = nativeDefault(c)
		if v, ok := overrides[c.name]; ok {
			values[i] = v
		}
	}
	keys := make([][]byte, len(tb.pkIndexes))
	for i, index := range tb.pkIndexes {
		keys[i] = values[index]
	}
	return wireRow{recordBytes(tb.pkColumns, keys), recordBytes(tb.columns, values), true}
}
func sourceRows(t *testing.T, q *mockQuery, name string, n int) {
	tb := tableByName(t, q.m, name)
	for i := 0; i < n; i++ {
		q.data[name] = append(q.data[name], makeWire(tb, map[string][]byte{tb.pkColumns[0].name: []byte(fmt.Sprintf("key-%04d", i))}))
	}
	sort.Slice(q.data[name], func(i, j int) bool { return bytes.Compare(q.data[name][i].key, q.data[name][j].key) < 0 })
}
func requireFailure(t *testing.T, q *mockQuery, want error) {
	t.Helper()
	f, e := Read(context.Background(), q, "test_schema")
	if e != want || f.Summary() != (Report{}) {
		t.Fatalf("error=%v want=%v zero=%t", e, want, f.Summary() == (Report{}))
	}
}

func TestMaterialsPinnedAndEmbeddedMigrationCrosscheck(t *testing.T) {
	m, e := loadMaterials()
	if e != nil {
		t.Fatal(e)
	}
	if len(m.tables) != 109 || len(m.send.Types) != 15 || len(m.send.Functions) != 16 {
		t.Fatal("fixed inventories")
	}
	names, e := fs.Glob(migrations.Files, "*.sql")
	if e != nil {
		t.Fatal(e)
	}
	sort.Strings(names)
	// These research materials describe the original 001-022 schema. Later
	// current migrations must not silently change that frozen identity.
	const originalThrough = "022_payment_runtime_attestation.sql"
	last := sort.SearchStrings(names, originalThrough)
	if last == len(names) || names[last] != originalThrough || last+1 != 22 {
		t.Fatal("original 001-022 migration prefix drift")
	}
	names = names[:last+1]
	var entries []manifestEntry
	for _, name := range names {
		b, e := migrations.Files.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		entries = append(entries, manifestEntry{name, sha(b)})
	}
	b, _ := json.Marshal(entries)
	if !bytes.Equal(b, manifestCanonical) {
		t.Fatal("migration manifest drift")
	}
	columns, pks := 0, 0
	for _, tb := range m.tables {
		columns += len(tb.columns)
		pks += len(tb.pkColumns)
	}
	if columns != 976 || pks != 128 {
		t.Fatal("projection drift")
	}
	if sha(descriptorCanonical) != descriptorSHA || sha(sendCanonical) != sendSHA || sha(manifestCanonical) != manifestSHA {
		t.Fatal("material SHA")
	}
}
func TestReadAll109EmptyIndependentGolden(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*environment)
		gold string
	}{
		{"default", func(*environment) {}, "3e59b227615330063d0010225b80375ccbba024890253675e8f36f897f056001"},
		{"NULL versus empty", func(e *environment) { e.ICURules = ptr("") }, "813fd7093fdab9e4d0e5b8d1a7b1358df581d7e028d0f1e00c2a4c152ad55393"},
		{"other minor", func(e *environment) { e.ServerVersionNum = 160016 }, "903b2f817b473d70854cb1f9b1268c3e546928616427d8c7325867b128b6a56a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newMock(t)
			tc.edit(&q.env)
			f, e := Read(context.Background(), q, "test_schema")
			if e != nil {
				t.Fatal(e)
			}
			r := f.Summary()
			if r.BasisSHA256 != tc.gold || r.Scope != "fixed_109_table_basis_v1" || !r.PhysicalEOFComplete || r.ScannedTables != 109 || r.ScannedColumns != 976 || r.SourceRows != 0 || r.SourceBytes != 0 {
				t.Fatalf("bad safe summary: %+v", r)
			}
			if len(q.pages) != 109 || len(q.log) != 225 {
				t.Fatalf("incomplete physical traversal: pages=%d calls=%d", len(q.pages), len(q.log))
			}
			for _, n := range q.pages {
				if n != 1 {
					t.Fatal("empty table must have one page")
				}
			}
		})
	}
}
func TestIndependentMixedFrameGolden(t *testing.T) {
	// This literal vector and SHA were encoded independently with Python's
	// struct.pack, including NULL/empty, UTF8, native NUMERIC/JSON/bytea/array/time.
	raw, e := hex.DecodeString("00000008000000190000000ae6b189e5ad97f09f9982000006a40000000c00020000000000020001092900000eda00000008017b2261223a317d000000110000000300ff01000003f10000001d00000001000000010000001900000002000000010000000178ffffffff000004a000000008000000000012d68700000019ffffffff0000001900000000")
	if e != nil {
		t.Fatal(e)
	}
	env := baseEnvironment()
	env.ICURules = ptr("")
	env.CollationVersion = ptr("v1")
	env.ActualCollationVersion = ptr("v1")
	f := &framer{context.Background(), sha256.New()}
	for _, e := range []error{f.header(env), f.frame([]byte("golden")), f.integer(8), f.frame([]byte(`["id"]`)), f.integer(1), f.frame(raw)} {
		if e != nil {
			t.Fatal(e)
		}
	}
	if got := hex.EncodeToString(f.hash.Sum(nil)); got != "bc8faee9e7c8cbc72a1e1d4d390649269be4e02304b3e5c193432d8e6e15236d" {
		t.Fatalf("golden mismatch %s", got)
	}
}
func TestReadPaginationAndBinaryCompositeKeys(t *testing.T) {
	for _, n := range []int{0, 1, 127, 128, 129, 256, 257} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			q := newMock(t)
			sourceRows(t, q, "api_keys", n)
			f, e := Read(context.Background(), q, "test_schema")
			if e != nil {
				t.Fatal(e)
			}
			if f.Summary().SourceRows != uint64(n) || q.pages["api_keys"] != n/128+1 {
				t.Fatal("pagination/EOF")
			}
		})
	}
	q := newMock(t)
	tb := tableByName(t, q.m, "financial_idempotency")
	for _, v := range []string{"", "z", "aa", "汉字", "🙂"} {
		q.data[tb.name] = append(q.data[tb.name], makeWire(tb, map[string][]byte{"tenant_id": []byte("tenant"), "operation": []byte("op"), "key": []byte(v)}))
	}
	sort.Slice(q.data[tb.name], func(i, j int) bool { return bytes.Compare(q.data[tb.name][i].key, q.data[tb.name][j].key) < 0 })
	f, e := Read(context.Background(), q, "test_schema")
	if e != nil || f.Summary().SourceRows != 5 {
		t.Fatalf("composite %v", e)
	}
	// Native record bytes sort by length before text contents: z precedes aa.
	if bytes.Compare(q.data[tb.name][1].key, q.data[tb.name][2].key) >= 0 {
		t.Fatal("binary ordering")
	}
}
func TestReadEveryTableNonempty(t *testing.T) {
	q := newMock(t)
	for _, tb := range q.m.tables {
		q.data[tb.name] = []wireRow{makeWire(tb, nil)}
	}
	f, e := Read(context.Background(), q, "test_schema")
	if e != nil {
		t.Fatal(e)
	}
	if f.Summary().SourceRows != 109 || f.Summary().SourceBytes == 0 {
		t.Fatal("full nonempty union")
	}
}
func TestMetadataPrecedesDataAndRejectsDrift(t *testing.T) {
	for _, kind := range []string{"relations", "attributes", "primary_keys", "types", "functions", "manifest"} {
		t.Run(kind, func(t *testing.T) {
			q := newMock(t)
			q.meta[kind] = q.meta[kind][1:]
			requireFailure(t, q, ErrMetadata)
			for _, seen := range q.log {
				if seen == "count" || seen == "page" {
					t.Fatal("business read before metadata qualification")
				}
			}
		})
	}
	for _, edit := range []func(*environment){func(e *environment) { e.DatabaseOID = 0 }, func(e *environment) { e.ServerVersionNum = 170001 }, func(e *environment) { e.DatabaseEncoding = 0 }, func(e *environment) { e.ClientEncoding = "LATIN1" }, func(e *environment) { e.ServerEncoding = "LATIN1" }, func(e *environment) { e.DatabaseEncodingName = "LATIN1" }, func(e *environment) { e.Provider = "x" }} {
		q := newMock(t)
		edit(&q.env)
		requireFailure(t, q, ErrMetadata)
	}
	q := newMock(t)
	q.env.Provider = "i"
	q.env.ICULocale = ptr("und")
	q.env.Collate = "en_US"
	if _, e := Read(context.Background(), q, "test_schema"); e != nil {
		t.Fatal("actual locale should be bound, not rejected", e)
	}
}
func TestMetadataStrictNullTypeAndShape(t *testing.T) {
	q := newMock(t)
	base := *q.meta["functions"][0][2].(*string)
	for _, mutate := range []func(map[string]any){func(m map[string]any) { m["security_definer"] = nil }, func(m map[string]any) { m["variadic_oid"] = nil }, func(m map[string]any) { m["argument_type_oids"] = []any{nil} }, func(m map[string]any) { m["argument_type_oids"] = []any{"2277"} }, func(m map[string]any) { m["argument_type_oids"] = nil }, func(m map[string]any) { m["config"] = []any{} }, func(m map[string]any) { delete(m, "leakproof") }, func(m map[string]any) { m["extra"] = false }} {
		q := newMock(t)
		var m map[string]any
		_ = json.Unmarshal([]byte(base), &m)
		mutate(m)
		b, _ := json.Marshal(m)
		s := string(b)
		q.meta["functions"][0][2] = &s
		requireFailure(t, q, ErrMetadata)
	}
}
func TestMetadataTenantBirthExactDelta(t *testing.T) {
	for _, bad := range []bool{false, true} {
		q := newMock(t)
		a := attributeMetadata{Table: "tenants", Ordinal: 9, Name: "fl_created_xid", OID: 5069, Typmod: -1}
		if bad {
			a.NotNull = true
		}
		q.meta["attributes"] = append(q.meta["attributes"], metadataRow(a))
		sort.Slice(q.meta["attributes"], func(i, j int) bool {
			var a, b attributeMetadata
			_ = json.Unmarshal([]byte(*q.meta["attributes"][i][2].(*string)), &a)
			_ = json.Unmarshal([]byte(*q.meta["attributes"][j][2].(*string)), &b)
			return a.Table < b.Table || a.Table == b.Table && a.Ordinal < b.Ordinal
		})
		if bad {
			requireFailure(t, q, ErrMetadata)
		} else {
			f, e := Read(context.Background(), q, "test_schema")
			if e != nil || f.Summary().BasisSHA256 != "3e59b227615330063d0010225b80375ccbba024890253675e8f36f897f056001" {
				t.Fatal("birth delta altered old basis", e)
			}
		}
	}
}
func TestMetadataAndDataResourceRejection(t *testing.T) {
	q := newMock(t)
	q.meta["functions"][0] = []any{false, true, (*string)(nil)}
	requireFailure(t, q, ErrMetadata)
	q = newMock(t)
	q.meta["attributes"][0] = []any{true, false, (*string)(nil)}
	requireFailure(t, q, ErrResource)
	q = newMock(t)
	sourceRows(t, q, "api_keys", 1)
	q.data["api_keys"][0].qualified = false
	requireFailure(t, q, ErrResource)
	q = newMock(t)
	sourceRows(t, q, "api_keys", 1)
	q.data["api_keys"][0].key = make([]byte, keyLimit+1)
	requireFailure(t, q, ErrResource)
	q = newMock(t)
	sourceRows(t, q, "api_keys", 1)
	q.data["api_keys"][0].row = make([]byte, recordLimit+1)
	requireFailure(t, q, ErrResource)
}
func TestEnvelopeFailuresAndPKMismatch(t *testing.T) {
	q := newMock(t)
	tb := tableByName(t, q.m, "api_keys")
	good := makeWire(tb, map[string][]byte{"id": []byte("id")})
	for _, mutate := range []func(*wireRow){func(w *wireRow) { w.row = w.row[:3] }, func(w *wireRow) { w.row = append(w.row, 0) }, func(w *wireRow) { binary.BigEndian.PutUint32(w.row[:4], 0) }, func(w *wireRow) { binary.BigEndian.PutUint32(w.key[4:8], 17) }, func(w *wireRow) { binary.BigEndian.PutUint32(w.key[8:12], uint32(0xfffffffe)) }, func(w *wireRow) { binary.BigEndian.PutUint32(w.key[8:12], uint32(0xffffffff)) }, func(w *wireRow) { w.key[len(w.key)-1] = 'x' }, func(w *wireRow) { w.row = nil }} {
		q := newMock(t)
		w := wireRow{bytes.Clone(good.key), bytes.Clone(good.row), true}
		mutate(&w)
		q.data[tb.name] = []wireRow{w}
		requireFailure(t, q, ErrExecution)
	}
	for _, oid := range []uint32{16, 20, 21, 23, 26, 1184, 2950, 5069} {
		cols := []column{{oid: oid, notNull: true}}
		raw := recordBytes(cols, [][]byte{{1, 2, 3}})
		if _, e := parseRecord(context.Background(), raw, cols); e != ErrExecution {
			t.Fatal("fixed width not checked", oid, e)
		}
	}
}
func TestCountTailErrorsAndCleanup(t *testing.T) {
	for _, phase := range []string{"environment", "count", "page"} {
		for _, fault := range []string{"query", "next", "scan", "err", "close"} {
			t.Run(phase+"/"+fault, func(t *testing.T) {
				q := newMock(t)
				sourceRows(t, q, "api_keys", 1)
				var touched *mockRows
				q.hook = func(kind string, args []any, r *mockRows) (Rows, error) {
					if kind == phase && touched == nil && (phase != "page" || len(r.data) > 0) {
						touched = r
						switch fault {
						case "query":
							return r, errors.New("secret error")
						case "next":
							r.nextErr = errors.New("secret")
						case "scan":
							r.scanErr = errors.New("secret")
						case "err":
							r.endErr = errors.New("secret")
						case "close":
							r.closeErr = errors.New("secret")
						}
					}
					return r, nil
				}
				requireFailure(t, q, ErrExecution)
				if touched == nil || touched.closed == 0 {
					t.Fatal("failed Rows not closed")
				}
			})
		}
	}
	q := newMock(t)
	q.counts["api_keys"] = 1
	requireFailure(t, q, ErrExecution)
	q = newMock(t)
	sourceRows(t, q, "api_keys", 1)
	q.counts["api_keys"] = 0
	requireFailure(t, q, ErrExecution)
}
func TestResourcePreservedAcrossCleanupCancel(t *testing.T) {
	for _, phase := range []string{"count", "page", "attributes"} {
		t.Run(phase, func(t *testing.T) {
			q := newMock(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			q.counts["api_keys"] = 100001
			sourceRows(t, q, "api_keys", 1)
			if phase != "count" {
				delete(q.counts, "api_keys")
			}
			if phase == "page" {
				q.data["api_keys"][0].qualified = false
			}
			if phase == "attributes" {
				q.meta[phase][0] = []any{true, false, (*string)(nil)}
			}
			q.hook = func(kind string, args []any, r *mockRows) (Rows, error) {
				if kind == phase && (phase == "attributes" || phase == "count" && len(r.data) > 0 && r.data[0][0].(int64) > rowLimit || phase == "page" && len(r.data) > 0) {
					r.onClose = cancel
				}
				return r, nil
			}
			f, e := Read(ctx, q, "test_schema")
			if e != ErrResource || f.Summary() != (Report{}) {
				t.Fatalf("resource lost: %v", e)
			}
		})
	}
}
func TestContextInputAndRedaction(t *testing.T) {
	q := newMock(t)
	for _, schema := range []string{"", strings.Repeat("a", 64), "a\x00b", string([]byte{255})} {
		if _, e := Read(context.Background(), q, schema); e != ErrInput {
			t.Fatal(e)
		}
	}
	var typedNil *mockQuery
	if _, e := Read(context.Background(), typedNil, "x"); e != ErrInput {
		t.Fatal(e)
	}
	if _, e := Read(nil, q, "x"); e != ErrInput {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Read(ctx, q, "x"); e != ErrCanceled {
		t.Fatal(e)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, e := Read(ctx, q, "x"); e != ErrTimeout {
		t.Fatal(e)
	}
	f, e := Read(context.Background(), newMock(t), "test_schema")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{fmt.Sprint(f), fmt.Sprintf("%#v", f), func() string { b, _ := json.Marshal(f); return string(b) }()} {
		if strings.Contains(v, "test_schema") || strings.Contains(v, f.Summary().BasisSHA256) {
			t.Fatal("facts not redacted")
		}
	}
}
func TestSQLQualificationAndIdentifierBinding(t *testing.T) {
	m, _ := loadMaterials()
	tb := m.tables[0]
	sql := pageSQL("evil\";SELECT secret;--", tb)
	for _, part := range []string{`"evil"";SELECT secret;--"`, "WITH page AS MATERIALIZED", "sizes AS MATERIALIZED", "ranked AS MATERIALIZED", "ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW", "$3::pg_catalog.int8", "$4::pg_catalog.int8", "CASE WHEN q THEN k ELSE NULL END", "CASE WHEN q THEN r ELSE NULL END", "LIMIT 128", "COLLATE pg_catalog.\"C\""} {
		if !strings.Contains(sql, part) {
			t.Fatal("missing SQL guard", part)
		}
	}
	if strings.Contains(sql, "WHERE q") || strings.Contains(sql, "fl_created_xid") {
		t.Fatal("qualification filtered or birth hashed")
	}
	for _, s := range []string{typesSQL(m), functionsSQL(m), manifestSQL("s")} {
		if !strings.Contains(s, "$1::pg_catalog.text IS NOT NULL") || strings.Contains(s, " IN (") {
			t.Fatal("unused argument or unbound operator")
		}
	}
}

func TestSharedBudgetsAndStrictCursor(t *testing.T) {
	q := newMock(t)
	tb := tableByName(t, q.m, "api_keys")
	sourceRows(t, q, tb.name, 1)
	w := q.data[tb.name][0]
	for _, tc := range []struct {
		rows, bytes int
		want        error
	}{{rowLimit - 1, byteLimit - len(w.key) - len(w.row), nil}, {rowLimit, 0, ErrResource}, {0, byteLimit - len(w.key) - len(w.row) + 1, ErrResource}} {
		s := &reader{ctx: context.Background(), q: q, schema: "test_schema", materials: q.m, rows: tc.rows, bytes: tc.bytes}
		f := &framer{s.ctx, sha256.New()}
		if e := s.readTable(f, tb); e != tc.want {
			t.Fatalf("shared allowance %v != %v", e, tc.want)
		}
	}
	q = newMock(t)
	sourceRows(t, q, "api_keys", 2)
	q.data["api_keys"][1].key = bytes.Clone(q.data["api_keys"][0].key)
	q.data["api_keys"][1].row = bytes.Clone(q.data["api_keys"][0].row)
	requireFailure(t, q, ErrExecution)
}

func TestExactKeyAndRecordLimits(t *testing.T) {
	for _, size := range []int{keyLimit - 1, keyLimit, keyLimit + 1} {
		q := newMock(t)
		tb := tableByName(t, q.m, "api_keys")
		q.data[tb.name] = []wireRow{makeWire(tb, map[string][]byte{"id": bytes.Repeat([]byte("k"), size-12)})}
		f, e := Read(context.Background(), q, "test_schema")
		if size > keyLimit {
			if e != ErrResource || f.Summary() != (Report{}) {
				t.Fatal("key cap", size, e)
			}
		} else if e != nil || f.Summary().SourceRows != 1 {
			t.Fatal("key accepted boundary", size, e)
		}
	}
	for _, size := range []int{recordLimit - 1, recordLimit, recordLimit + 1} {
		q := newMock(t)
		tb := tableByName(t, q.m, "api_keys")
		field := ""
		for _, c := range tb.columns {
			if c.oid == 25 && c.name != "id" {
				field = c.name
				break
			}
		}
		if field == "" {
			t.Fatal("missing variable native field")
		}
		base := makeWire(tb, map[string][]byte{field: []byte{}})
		q.data[tb.name] = []wireRow{makeWire(tb, map[string][]byte{field: bytes.Repeat([]byte("x"), size-len(base.row))})}
		if len(q.data[tb.name][0].row) != size {
			t.Fatal("fixture size")
		}
		f, e := Read(context.Background(), q, "test_schema")
		if size > recordLimit {
			if e != ErrResource || f.Summary() != (Report{}) {
				t.Fatal("record cap", size, e)
			}
		} else if e != nil || f.Summary().SourceRows != 1 {
			t.Fatal("record accepted boundary", size, e)
		}
	}
}

func TestOverfullRowResourcePrecedesCleanup(t *testing.T) {
	q := newMock(t)
	q.meta["attributes"] = [][]any{q.meta["attributes"][0], {true, false, (*string)(nil)}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.hook = func(kind string, _ []any, r *mockRows) (Rows, error) { r.onClose = cancel; return r, nil }
	s := &reader{ctx: ctx, q: q, schema: "test_schema", materials: q.m}
	if e := s.consumeMetadata(attributesSQL(q.m), 1, func([]byte) error { return nil }); e != ErrResource {
		t.Fatal("overfull metadata resource", e)
	}
	q = newMock(t)
	sourceRows(t, q, "api_keys", 128)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	q.hook = func(kind string, _ []any, r *mockRows) (Rows, error) {
		if kind == "page" && len(r.data) == 128 {
			r.data = append(r.data, []any{false, []byte(nil), []byte(nil)})
			r.onClose = cancel
		}
		return r, nil
	}
	f, e := Read(ctx, q, "test_schema")
	if e != ErrResource || f.Summary() != (Report{}) {
		t.Fatal("overfull data resource", e)
	}
}

type cancelHash struct {
	hash.Hash
	cancel context.CancelFunc
}

func (h *cancelHash) Write(b []byte) (int, error) { n, e := h.Hash.Write(b); h.cancel(); return n, e }
func TestCPUAndDriverCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &cancelHash{sha256.New(), cancel}
	f := &framer{ctx, h}
	if e := f.frame(make([]byte, recordLimit)); e != ErrCanceled {
		t.Fatal("CPU cancellation", e)
	}
	for _, phase := range []string{"environment", "count", "page"} {
		q := newMock(t)
		sourceRows(t, q, "api_keys", 1)
		ctx, cancel := context.WithCancel(context.Background())
		q.hook = func(kind string, _ []any, r *mockRows) (Rows, error) {
			if kind == phase {
				r.onNext = func(int) { cancel() }
			}
			return r, nil
		}
		facts, e := Read(ctx, q, "test_schema")
		cancel()
		if e != ErrCanceled || facts.Summary() != (Report{}) {
			t.Fatal("driver cancellation", phase, e)
		}
	}
}

func TestDuplicateMetadataKeys(t *testing.T) {
	q := newMock(t)
	s := *q.meta["types"][0][2].(*string)
	s = strings.TrimSuffix(s, "}") + `,"by_value":true}`
	q.meta["types"][0][2] = &s
	requireFailure(t, q, ErrMetadata)
}

func FuzzRecordEnvelope(f *testing.F) {
	cols := []column{{oid: 25, notNull: true}, {oid: 20, notNull: true}}
	f.Add(recordBytes(cols, [][]byte{[]byte("Unicode🙂"), make([]byte, 8)}))
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 2})
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > recordLimit {
			t.Skip()
		}
		got, e := parseRecord(context.Background(), raw, cols)
		if e != nil {
			if e != ErrExecution {
				t.Fatal(e)
			}
			return
		}
		if len(got) != 2 || got[0].oid != 25 || got[1].oid != 20 || got[0].null || got[1].null || len(got[1].value) != 8 {
			t.Fatal("accepted invalid fixed envelope")
		}
	})
}
