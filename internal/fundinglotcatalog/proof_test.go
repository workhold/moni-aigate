package fundinglotcatalog

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPins(mode Mode) Pins {
	p := Pins{
		Mode: mode, Database: DatabasePin{"127.0.0.1", 55439, "moni_test", 100},
		AppSchema: SchemaPin{"application_test", 101, RolePin{"owner_test", 102}, RolePin{"owner_test", 102}},
		App:       RolePin{"app_test", 103}, Projector: RolePin{"projector_test", 104},
	}
	if mode == Sandbox {
		p.Worker = &RolePin{"worker_test", 105}
		p.Sandbox = &SandboxPins{
			Mock:            SchemaPin{"mock_test", 106, RolePin{"mock_owner_test", 107}, RolePin{"mock_owner_test", 107}},
			MockRuntime:     RolePin{"mock_runtime_test", 108},
			Receiver:        SchemaPin{"receiver_test", 109, RolePin{"receiver_owner_test", 110}, RolePin{"receiver_owner_test", 110}},
			ReceiverRuntime: RolePin{"receiver_runtime_test", 111},
		}
	}
	return p
}

func TestPinsClosedModesAndIdentity(t *testing.T) {
	for _, mode := range []Mode{Ordinary, Sandbox} {
		if !validPins(testPins(mode)) {
			t.Fatal("valid pins rejected")
		}
	}
	cases := map[string]func(*Pins){
		"unknown mode":            func(p *Pins) { p.Mode = 7 },
		"no database":             func(p *Pins) { p.Database.Name = "" },
		"zero port":               func(p *Pins) { p.Database.Port = 0 },
		"unix endpoint":           func(p *Pins) { p.Database.Host = "/tmp" },
		"owner OID alias":         func(p *Pins) { p.App.OID = p.AppSchema.ObjectOwner.OID },
		"role name alias":         func(p *Pins) { p.Projector.Name = p.App.Name },
		"system schema":           func(p *Pins) { p.AppSchema.Name = "pg_catalog" },
		"namespace owner alias":   func(p *Pins) { p.AppSchema.NamespaceOwner.Name = "other_owner" },
		"missing worker":          func(p *Pins) { p.Worker = nil },
		"missing peers":           func(p *Pins) { p.Sandbox = nil },
		"schema OID alias":        func(p *Pins) { p.Sandbox.Mock.OID = p.AppSchema.OID },
		"schema name alias":       func(p *Pins) { p.Sandbox.Mock.Name = p.Sandbox.Receiver.Name },
		"peer role alias":         func(p *Pins) { p.Sandbox.MockRuntime = p.Sandbox.ReceiverRuntime },
		"builtin owner elsewhere": func(p *Pins) { p.Sandbox.Mock.NamespaceOwner = RolePin{"pg_database_owner", 6171} },
		"NUL name":                func(p *Pins) { p.App.Name = "app\x00test" },
		"invalid UTF8":            func(p *Pins) { p.App.Name = string([]byte{255}) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := testPins(Sandbox)
			mutate(&p)
			if validPins(p) {
				t.Fatal("invalid pins accepted")
			}
		})
	}
	p := testPins(Ordinary)
	p.Worker = &RolePin{"worker_test", 105}
	if validPins(p) {
		t.Fatal("ordinary worker accepted")
	}
	p = testPins(Ordinary)
	p.AppSchema.Name = "public"
	p.AppSchema.NamespaceOwner = RolePin{"pg_database_owner", 6171}
	if !validPins(p) {
		t.Fatal("public binding rejected")
	}
	p.AppSchema.NamespaceOwner.OID++
	if validPins(p) {
		t.Fatal("fake builtin owner accepted")
	}
}

func TestProofCopiesPinsAndRejectsZero(t *testing.T) {
	pins := testPins(Sandbox)
	original := copyPins(pins)
	p := &CatalogProof{identity: &proofIdentity{pins: copyPins(pins), version: 1, expected: "root", schema: "schema", roles: "roles"}}
	copyOfProof := *p
	if !p.MatchesPins(original) || !copyOfProof.MatchesPins(original) {
		t.Fatal("initial pins mismatch")
	}
	pins.Worker.OID++
	pins.Sandbox.Mock.Name = "changed"
	if p.MatchesPins(pins) || !p.MatchesPins(original) || !copyOfProof.MatchesPins(original) {
		t.Fatal("caller mutation changed proof")
	}
	if p.DescriptorVersion() != 1 || p.ExpectedSHA256() != "root" || p.SchemaSHA256() != "schema" || p.RolesSHA256() != "roles" {
		t.Fatal("metadata lost")
	}
	for _, invalid := range []*CatalogProof{nil, {}} {
		if invalid.MatchesPins(original) || invalid.DescriptorVersion() != 0 || invalid.ExpectedSHA256() != "" || invalid.SchemaSHA256() != "" || invalid.RolesSHA256() != "" || invalid.VerifyTx(context.Background(), nil) != ErrCatalog {
			t.Fatal("zero proof accepted")
		}
	}
	for _, invalid := range []*InstallationProof{nil, {}} {
		if invalid.Catalog() != nil || invalid.VerifyTx(context.Background(), nil) != ErrCatalog {
			t.Fatal("zero installation accepted")
		}
	}
	installed := &InstallationProof{identity: p.identity}
	exposed := installed.Catalog()
	if exposed.identity != p.identity {
		t.Fatal("installation changed original identity")
	}
	*exposed = CatalogProof{identity: &proofIdentity{expected: "other"}}
	*p = CatalogProof{}
	if installed.Catalog().ExpectedSHA256() != "root" || !installed.Catalog().MatchesPins(original) {
		t.Fatal("caller replaced private empty-at-issue identity")
	}
}

// Embedding an interface provides methods while leaving a non-comparable
// wrapper. It must be rejected before any promoted (nil) method is called.
type nonComparableTx struct {
	pgx.Tx
	values []string
}

func TestPublicIssuersRejectInvalidWithoutDatabase(t *testing.T) {
	var typedNil *pgxpool.Tx
	for _, tx := range []pgx.Tx{nil, typedNil, nonComparableTx{}} {
		if validTx(tx) {
			t.Fatal("invalid transaction accepted")
		}
		if p, err := VerifySnapshotTx(context.Background(), tx, testPins(Ordinary)); p != nil || err != ErrCatalog {
			t.Fatal("invalid snapshot issued")
		}
		if p, err := VerifyInstallationTx(context.Background(), tx, testPins(Ordinary)); p != nil || err != ErrCatalog {
			t.Fatal("invalid installation issued")
		}
		for _, role := range []RuntimeRole{0, Application, Worker, Projector, 77} {
			if p, err := VerifyRuntimeTx(context.Background(), tx, testPins(Ordinary), role); p != nil || err != ErrCatalog {
				t.Fatal("invalid runtime issued")
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, invalidCtx := range []context.Context{nil, ctx} {
		if p, err := VerifySnapshotTx(invalidCtx, nil, testPins(Ordinary)); p != nil || err != ErrCatalog {
			t.Fatal("invalid context accepted")
		}
	}
}

func TestBudgetIsOneBoundedParentContext(t *testing.T) {
	start := time.Now()
	ctx, cancel, err := boundedContext(context.Background())
	if err != nil {
		t.Fatal("budget failed")
	}
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Before(start.Add(4*time.Second)) || deadline.After(time.Now().Add(catalogBudget)) {
		t.Fatal("unbounded context")
	}
	parent, stop := context.WithDeadline(context.Background(), start.Add(time.Second))
	defer stop()
	child, done, err := boundedContext(parent)
	if err != nil {
		t.Fatal("short parent failed")
	}
	defer done()
	childDeadline, _ := child.Deadline()
	if !childDeadline.Equal(start.Add(time.Second)) {
		t.Fatal("parent deadline extended")
	}
}

func TestSearchPathStrictIdentifierList(t *testing.T) {
	for input, want := range map[string][]string{
		`app, pg_catalog, pg_temp`:            {"app", "pg_catalog", "pg_temp"},
		`"Case Sensitive",pg_catalog,pg_temp`: {"Case Sensitive", "pg_catalog", "pg_temp"},
		`"quote""name",pg_catalog,pg_temp`:    {`quote"name`, "pg_catalog", "pg_temp"},
	} {
		got, err := identityParsePath(input)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("valid identifier list rejected")
		}
	}
	for _, input := range []string{"", `""`, `"missing`, "app,", "app,,pg_catalog", "app;SELECT 1", "$user,public", "app,pg_catalog,pg_temp,extra", "app /*x*/,pg_catalog"} {
		if _, err := identityParsePath(input); err != ErrCatalog {
			t.Fatal("invalid identifier list accepted")
		}
	}
	seen := map[string]bool{}
	for _, table := range installationTables {
		if seen[table] {
			t.Fatal("duplicate empty-at-issue table")
		}
		seen[table] = true
	}
	if len(seen) != 16 {
		t.Fatal("incomplete empty-at-issue set")
	}
}
