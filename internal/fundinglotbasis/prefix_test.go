package fundinglotbasis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
)

func newPrefixMock(t *testing.T) *mockQuery {
	t.Helper()
	q := newMock(t)
	m, err := loadPrefixMaterials()
	if err != nil {
		t.Fatal(err)
	}
	q.m = m
	for _, kind := range []string{"relations", "attributes", "primary_keys"} {
		q.meta[kind] = nil
	}
	for _, tb := range m.tables {
		q.meta["relations"] = append(q.meta["relations"], metadataRow(relationMetadata{Name: tb.name, Kind: "r", Persistence: "p", AccessMethodOID: 2}))
		for _, c := range tb.columns {
			q.meta["attributes"] = append(q.meta["attributes"], metadataRow(attributeMetadata{Table: tb.name, Ordinal: c.ordinal, Name: c.name, OID: c.oid, Typmod: c.typmod, NotNull: c.notNull, CollationOID: c.collation}))
		}
		q.meta["primary_keys"] = append(q.meta["primary_keys"], metadataRow(primaryKeyMetadata{Table: tb.name, KeyCount: int32(len(tb.primaryKey)), Unique: true, Valid: true, Ready: true, Live: true, Immediate: true, Plain: true, Keys: tb.primaryKey}))
	}
	return q
}

func prefixFailure(t *testing.T, q Query, want error) {
	t.Helper()
	f, err := ReadPrefix(context.Background(), q, "test_schema")
	if err != want || f.Summary() != (Report{}) {
		t.Fatalf("error=%v want=%v zero=%t", err, want, f.Summary() == (Report{}))
	}
}

func TestPrefixDescriptorMatchesFrozenSQLDeltaAndLegacy(t *testing.T) {
	legacy, err := loadMaterials()
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := loadPrefixMaterials()
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixDescriptorCanonical) != 40655 || sha(prefixDescriptorCanonical) != "104914e6176e3d02a289e4e0acf0d476a69dcfb2e8772cf29336b520a7ec82bc" || sha(prefixDescriptorCanonical) != PeriodicPrefixDescriptorSHA256 || prefixDescriptorCanonical[len(prefixDescriptorCanonical)-1] == '\n' {
		t.Fatal("prefix descriptor bytes or digest drift")
	}
	if len(descriptorCanonical) != 39415 || sha(descriptorCanonical) != "54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5" || len(manifestCanonical) != 2459 || sha(manifestCanonical) != "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de" || len(sendCanonical) != 12360 || sha(sendCanonical) != "1bd800f0014a159d1528e716275263c7b6c97e6fb91a037d034e1fbbe028684b" {
		t.Fatal("legacy fixed originals changed")
	}
	if !reflect.DeepEqual(legacy.manifest, prefix.manifest) || !reflect.DeepEqual(legacy.send, prefix.send) {
		t.Fatal("prefix must retain original history and send metadata")
	}

	// Derive the descriptor delta independently from the SHA-authenticated SQL,
	// never from a database export or the new descriptor being checked.
	sql, err := periodicbudgetprefixschema.SQL()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]table{}
	for _, tb := range legacy.tables {
		tb.columns = append([]column(nil), tb.columns...)
		expected[tb.name] = tb
	}
	oids := map[string]uint32{"text": 25, "integer": 23, "bigint": 20, "xid8": 5069, "timestamptz": 1184}
	makeColumn := func(ordinal int, name, typ, modifiers string) column {
		oid := oids[typ]
		if oid == 0 {
			t.Fatal("unreviewed SQL type")
		}
		collation := uint32(0)
		if oid == 25 {
			collation = 100
		}
		return column{int32(ordinal), name, oid, -1, strings.Contains(modifiers, "NOT NULL") || strings.Contains(modifiers, "PRIMARY KEY"), collation}
	}
	creates := regexp.MustCompile(`(?s)CREATE TABLE ([a-z_]+) \((.*?)\n\);`).FindAllStringSubmatch(sql, -1)
	if len(creates) != 3 {
		t.Fatal("expected exactly three candidate source tables")
	}
	declarations := regexp.MustCompile(`(?m)^ ([a-z_]+) (text|integer|bigint|xid8|timestamptz)([^\n]*)$`)
	for _, create := range creates {
		tb := table{name: create[1]}
		if _, exists := expected[tb.name]; exists {
			t.Fatal("candidate table shadows legacy material")
		}
		for _, field := range declarations.FindAllStringSubmatch(create[2], -1) {
			c := makeColumn(len(tb.columns)+1, field[1], field[2], field[3])
			tb.columns = append(tb.columns, c)
			if strings.Contains(field[3], "PRIMARY KEY") {
				tb.primaryKey = append(tb.primaryKey, c.name)
			}
		}
		expected[tb.name] = tb
	}
	adds := regexp.MustCompile(`(?m)^ALTER TABLE ([a-z_]+) ADD COLUMN ([a-z_]+) (text|xid8);$`).FindAllStringSubmatch(sql, -1)
	if len(adds) != 3 {
		t.Fatal("expected three added source columns")
	}
	for _, add := range adds {
		tb, ok := expected[add[1]]
		if !ok {
			t.Fatal("ALTER targets unknown source table")
		}
		tb.columns = append(tb.columns, makeColumn(len(tb.columns)+1, add[2], add[3], ""))
		expected[tb.name] = tb
	}
	columns, keys := 0, 0
	if len(expected) != 112 || len(prefix.tables) != len(expected) {
		t.Fatal("prefix table inventory differs from original plus frozen SQL")
	}
	for _, tb := range prefix.tables {
		want, ok := expected[tb.name]
		if !ok || !reflect.DeepEqual(tb.columns, want.columns) || !reflect.DeepEqual(tb.primaryKey, want.primaryKey) {
			t.Fatalf("descriptor differs from frozen SQL and legacy projection: %s", tb.name)
		}
		columns += len(tb.columns)
		keys += len(tb.pkColumns)
		delete(expected, tb.name)
	}
	if len(expected) != 0 || columns != 1004 || keys != 131 {
		t.Fatal("prefix descriptor complete physical counts")
	}
	for _, tb := range prefix.tables {
		if tb.name == "tenants" && len(tb.columns) != 8 {
			t.Fatal("tenant birth must not enter basis content")
		}
	}
}

func TestPrefixReadAll112EmptyIndependentGolden(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*environment)
		gold string
	}{
		{"default", func(*environment) {}, "35c84b6442010484b7a590217f94aa3100c3bf80f2a87ae627d3eccf80b77331"},
		{"NULL versus empty", func(e *environment) { e.ICURules = ptr("") }, "c73855155175c9015cbfe9647f414e6f00babc9a7dd8aaa26ad953e9bbd0bb72"},
		{"other minor", func(e *environment) { e.ServerVersionNum = 160016 }, "991194a6760b53b4cbc3481bb3496c0e7638625dec749a31db32d53250aa65ba"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newPrefixMock(t)
			tc.edit(&q.env)
			f, err := ReadPrefix(context.Background(), q, "test_schema")
			if err != nil {
				t.Fatal(err)
			}
			r := f.Summary()
			if r.BasisSHA256 != tc.gold || r.Scope != "fixed_112_table_prefix_basis_v1" || !r.PhysicalEOFComplete || r.ScannedTables != 112 || r.ScannedColumns != 1004 || r.SourceRows != 0 || r.SourceBytes != 0 || r.DescriptorSHA256 != PeriodicPrefixDescriptorSHA256 || r.ManifestSHA256 != manifestSHA || r.SendMetadataSHA256 != sendSHA {
				t.Fatalf("bad prefix safe summary: %+v", r)
			}
			if len(q.pages) != 112 || len(q.log) != 231 {
				t.Fatalf("incomplete physical traversal: pages=%d calls=%d", len(q.pages), len(q.log))
			}
			for _, n := range q.pages {
				if n != 1 {
					t.Fatal("empty source requires physical EOF")
				}
			}
		})
	}
}

func TestPrefixSelectedDescriptorFramesIndependentMixedGolden(t *testing.T) {
	// Independently encoded with Python struct.pack; the unchanged native mixed
	// record is framed after the new original descriptor, never its legacy hash.
	raw, err := hex.DecodeString("00000008000000190000000ae6b189e5ad97f09f9982000006a40000000c00020000000000020001092900000eda00000008017b2261223a317d000000110000000300ff01000003f10000001d00000001000000010000001900000002000000010000000178ffffffff000004a000000008000000000012d68700000019ffffffff0000001900000000")
	if err != nil {
		t.Fatal(err)
	}
	env := baseEnvironment()
	env.ICURules, env.CollationVersion, env.ActualCollationVersion = ptr(""), ptr("v1"), ptr("v1")
	f := &framer{context.Background(), sha256.New()}
	for _, err := range []error{f.headerDescriptor(env, prefixDescriptorCanonical), f.frame([]byte("golden")), f.integer(8), f.frame([]byte(`["id"]`)), f.integer(1), f.frame(raw)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := hex.EncodeToString(f.hash.Sum(nil)); got != "6ed1dd0c5397935e8114fa12d78f06307b1102f39d38e08100c629d5a1b7d5ec" {
		t.Fatalf("prefix framing golden mismatch: %s", got)
	}
}

func TestPrefixEveryPhysicalColumnAndNewSourceChangesFingerprint(t *testing.T) {
	q := newPrefixMock(t)
	for _, tb := range q.m.tables {
		q.data[tb.name] = []wireRow{makeWire(tb, nil)}
	}
	f, err := ReadPrefix(context.Background(), q, "test_schema")
	if err != nil || f.Summary().SourceRows != 112 || f.Summary().SourceBytes == 0 {
		t.Fatalf("full nonempty prefix inventory: %v", err)
	}
	for _, tc := range []struct{ table, field string }{
		{"periodic_budget_observation_origins", "tracking_started_at"},
		{"periodic_budget_observation_heads", "last_observation_id"},
		{"periodic_budget_observation_journal", "command_source_id"},
		{"periodic_budget_observation_journal", "window_source_id"},
		{"periodic_budget_operations", "observation_id"},
		{"periodic_budget_windows", "observation_id"},
		{"periodic_budget_windows", "observation_created_xid"},
	} {
		t.Run(tc.table+"/"+tc.field, func(t *testing.T) {
			original := q.data[tc.table][0]
			tb := tableByName(t, q.m, tc.table)
			value := []byte("new-observation")
			for _, c := range tb.columns {
				if c.name == tc.field && (c.oid == 1184 || c.oid == 5069) {
					value = []byte{0, 0, 0, 0, 0, 0, 0, 1}
				}
			}
			q.data[tc.table][0] = makeWire(tb, map[string][]byte{tc.field: value})
			changed, err := ReadPrefix(context.Background(), q, "test_schema")
			q.data[tc.table][0] = original
			if err != nil || changed.Summary().BasisSHA256 == f.Summary().BasisSHA256 {
				t.Fatalf("new physical column absent from basis: %v", err)
			}
		})
	}
}

func TestPrefixAndLegacyProfilesDoNotSubstitute(t *testing.T) {
	old := newMock(t)
	prefixFailure(t, old, ErrMetadata)
	for _, kind := range old.log {
		if kind == "count" || kind == "page" {
			t.Fatal("prefix must qualify full metadata before legacy content")
		}
	}
	newLayout := newPrefixMock(t)
	requireFailure(t, newLayout, ErrMetadata)
	for _, kind := range newLayout.log {
		if kind == "count" || kind == "page" {
			t.Fatal("legacy must reject new layout before business data")
		}
	}
	for _, load := range []func() (*materials, error){loadMaterials, loadPrefixMaterials} {
		m, err := load()
		if err != nil {
			t.Fatal(err)
		}
		relationLimit, attributeLimit := " LIMIT 110", " LIMIT 978"
		if m.profile.tables == 112 {
			relationLimit, attributeLimit = " LIMIT 113", " LIMIT 1006"
		}
		if !strings.Contains(relationsSQL(m), relationLimit+")") || !strings.Contains(primaryKeysSQL(m), relationLimit+")") || !strings.Contains(attributesSQL(m), attributeLimit+")") {
			t.Fatal("profile metadata sentinel was silently changed")
		}
	}
}

func TestPrefixNewSourcePaginationAndCountEOF(t *testing.T) {
	for _, n := range []int{0, 1, 127, 128, 129, 256, 257} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			q := newPrefixMock(t)
			sourceRows(t, q, "periodic_budget_observation_journal", n)
			f, err := ReadPrefix(context.Background(), q, "test_schema")
			if err != nil || f.Summary().SourceRows != uint64(n) || q.pages["periodic_budget_observation_journal"] != n/128+1 {
				t.Fatalf("new source pagination/EOF: %v", err)
			}
		})
	}
	for _, count := range []int64{0, 2} {
		q := newPrefixMock(t)
		sourceRows(t, q, "periodic_budget_observation_journal", 1)
		q.counts["periodic_budget_observation_journal"] = count
		prefixFailure(t, q, ErrExecution)
	}
}

func TestPrefixMetadataExactBirthAndAddedFieldDrift(t *testing.T) {
	for _, birth := range []bool{false, true} {
		q := newPrefixMock(t)
		if birth {
			q.meta["attributes"] = append(q.meta["attributes"], metadataRow(attributeMetadata{Table: "tenants", Ordinal: 9, Name: "fl_created_xid", OID: 5069, Typmod: -1}))
			sort.Slice(q.meta["attributes"], func(i, j int) bool {
				var a, b attributeMetadata
				_ = json.Unmarshal([]byte(*q.meta["attributes"][i][2].(*string)), &a)
				_ = json.Unmarshal([]byte(*q.meta["attributes"][j][2].(*string)), &b)
				return a.Table < b.Table || a.Table == b.Table && a.Ordinal < b.Ordinal
			})
		}
		f, err := ReadPrefix(context.Background(), q, "test_schema")
		if err != nil || f.Summary().ScannedColumns != 1004 || f.Summary().BasisSHA256 != "35c84b6442010484b7a590217f94aa3100c3bf80f2a87ae627d3eccf80b77331" {
			t.Fatalf("accurate optional tenant birth metadata: %v", err)
		}
		if birth {
			for i, row := range q.meta["attributes"] {
				var a attributeMetadata
				_ = json.Unmarshal([]byte(*row[2].(*string)), &a)
				if a.Table == "tenants" && a.Ordinal == 9 {
					a.NotNull = true
					q.meta["attributes"][i] = metadataRow(a)
				}
			}
			prefixFailure(t, q, ErrMetadata)
		}
	}
	for _, field := range []string{"command_source_id", "window_source_id", "observation_id", "observation_created_xid"} {
		t.Run(field, func(t *testing.T) {
			q := newPrefixMock(t)
			for i, row := range q.meta["attributes"] {
				var a attributeMetadata
				_ = json.Unmarshal([]byte(*row[2].(*string)), &a)
				if a.Name == field {
					a.NotNull = !a.NotNull
					q.meta["attributes"][i] = metadataRow(a)
					break
				}
			}
			prefixFailure(t, q, ErrMetadata)
			if len(q.pages) != 0 {
				t.Fatal("drift reached business reads")
			}
		})
	}
}

func TestPrefixFaultsCloseAndReturnNoPartialFacts(t *testing.T) {
	for _, phase := range []string{"attributes", "count", "page"} {
		for _, fault := range []string{"query", "next", "scan", "err", "close"} {
			t.Run(phase+"/"+fault, func(t *testing.T) {
				q := newPrefixMock(t)
				sourceRows(t, q, "periodic_budget_observation_journal", 1)
				var touched *mockRows
				q.hook = func(kind string, _ []any, r *mockRows) (Rows, error) {
					eligible := kind == phase && (phase == "attributes" || phase == "count" && r.data[0][0].(int64) == 1 || phase == "page" && len(r.data) > 0)
					if eligible && touched == nil {
						touched = r
						switch fault {
						case "query":
							return r, errors.New("secret driver error")
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
				prefixFailure(t, q, ErrExecution)
				if touched == nil || touched.closed == 0 {
					t.Fatal("failed new-source Rows was not closed")
				}
			})
		}
	}
}

func TestPrefixSharedBudgetsAndCleanupCancellation(t *testing.T) {
	for _, boundary := range []string{"rows", "bytes", "qualification"} {
		t.Run(boundary, func(t *testing.T) {
			q := newPrefixMock(t)
			tb := tableByName(t, q.m, "periodic_budget_observation_journal")
			sourceRows(t, q, tb.name, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &reader{ctx: ctx, q: q, schema: "test_schema", materials: q.m}
			switch boundary {
			case "rows":
				s.rows = rowLimit
			case "bytes":
				w := q.data[tb.name][0]
				s.bytes = byteLimit - len(w.key) - len(w.row) + 1
			case "qualification":
				q.data[tb.name][0].qualified = false
			}
			q.hook = func(kind string, _ []any, r *mockRows) (Rows, error) {
				if boundary == "rows" && kind == "count" || boundary != "rows" && kind == "page" {
					r.onClose = cancel
				}
				return r, nil
			}
			if err := s.readTable(&framer{ctx, sha256.New()}, tb); err != ErrResource {
				t.Fatalf("shared new-source budget or resource precedence lost: %v", err)
			}
		})
	}
}

func TestPrefixInputCancellationAndRedaction(t *testing.T) {
	var typedNil *mockQuery
	if f, err := ReadPrefix(nil, newPrefixMock(t), "test_schema"); err != ErrInput || f.Summary() != (Report{}) {
		t.Fatal("nil context")
	}
	if f, err := ReadPrefix(context.Background(), typedNil, "test_schema"); err != ErrInput || f.Summary() != (Report{}) {
		t.Fatal("typed nil query")
	}
	for _, schema := range []string{"", strings.Repeat("a", 64), "a\x00b", string([]byte{255})} {
		if f, err := ReadPrefix(context.Background(), newPrefixMock(t), schema); err != ErrInput || f.Summary() != (Report{}) {
			t.Fatal("invalid schema")
		}
	}
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := ErrCanceled
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = ErrTimeout
		}
		cancel()
		if f, err := ReadPrefix(ctx, newPrefixMock(t), "test_schema"); err != want || f.Summary() != (Report{}) {
			t.Fatal("context outcome")
		}
	}
	f, err := ReadPrefix(context.Background(), newPrefixMock(t), "test_schema")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f)
	for _, text := range []string{fmt.Sprint(f), fmt.Sprintf("%#v", f), string(raw)} {
		if strings.Contains(text, "test_schema") || strings.Contains(text, f.Summary().BasisSHA256) {
			t.Fatal("prefix Facts leaked its private state")
		}
	}
	for _, name := range []string{"periodic_budget_observation_origins", "periodic_budget_observation_heads", "periodic_budget_observation_journal"} {
		q := newPrefixMock(t)
		tb := tableByName(t, q.m, name)
		wire := makeWire(tb, nil)
		wire.key = bytes.Clone(wire.key)
		wire.row = bytes.Clone(wire.row)
		q.data[name] = []wireRow{wire, wire}
		prefixFailure(t, q, ErrExecution)
	}
}
