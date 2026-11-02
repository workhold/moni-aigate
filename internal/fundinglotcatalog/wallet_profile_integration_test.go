package fundinglotcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/operations"
)

const walletProfilePermissionsSHA = "97abd87df7e5d0e7a03adae4158470e76ab94d58c9e9d8b92ae792aeb84867c2"
const walletProfileReadsetSHA = "b4f66f3d15b9d5ecf4f670392a2029282dc2ab965b5d2e4412d7171f446f9a46"

// These tests use the original, source-pinned offline catalog fixtures. They do
// not provision a genuine payment installation or issue a full RuntimeProof.
type walletProfileFixture struct {
	ctx                                  context.Context
	pins                                 Pins
	admin, owner, app, worker, projector *pgxpool.Pool
	root                                 string
}

func walletProfileOrdinary(t *testing.T, kind ordinaryVariantKind) walletProfileFixture {
	t.Helper()
	f := newOrdinaryVariantFixture(t, kind)
	return walletProfileFixture{ctx: f.ctx, pins: f.pins, admin: catalogTestAudit(t, f), owner: f.owner, app: f.app, projector: f.projector, root: f.root}
}

func walletProfileSandbox(t *testing.T) walletProfileFixture {
	t.Helper()
	f := newCatalogPeerFixture(t)
	return walletProfileFixture{ctx: f.ctx, pins: f.pins, admin: f.admin, owner: f.appOwner, app: f.app, worker: f.worker, projector: f.projector, root: ordinaryVariantRoot(t)}
}

func walletProfileInstall(t *testing.T, f walletProfileFixture) {
	t.Helper()
	raw := ordinaryVariantSource(t, f.root, "specs/022-self-service-payments/c0-runtime-read-permissions.sql", walletProfilePermissionsSHA)
	text := string(raw)
	if strings.Count(text, "{{schema}}") != 1 || strings.Count(text, "{{app}}") != 1 {
		t.Fatal("wallet profile fixed identifier placeholders")
	}
	text = strings.ReplaceAll(text, "{{schema}}", pgx.Identifier{f.pins.AppSchema.Name}.Sanitize())
	text = strings.ReplaceAll(text, "{{app}}", pgx.Identifier{f.pins.App.Name}.Sanitize())
	if strings.Contains(text, "{{") || strings.Contains(text, "}}") {
		t.Fatal("wallet profile unresolved identifier placeholder")
	}
	// Only the fixture owner executes this fixed offline ACL candidate. Issuing
	// APIs are exercised below on actual LOGINs and never install privileges.
	if _, err := f.owner.Exec(f.ctx, text); err != nil {
		t.Fatal("wallet profile offline owner ACL installation")
	}
}

func walletProfileRollback(t *testing.T, tx pgx.Tx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if tx.Rollback(ctx) != nil {
		t.Fatal("wallet profile caller rollback")
	}
}

func walletProfileLegacyBaseline(t *testing.T, f walletProfileFixture) *CatalogProof {
	t.Helper()
	tx := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	p, err := VerifyRuntimeTx(f.ctx, tx, f.pins, Application)
	if err != nil || p == nil || p.Profile() != LegacyV1 || p.VerifyTx(f.ctx, tx) != nil {
		t.Fatal("wallet profile original runtime baseline")
	}
	if next, err := VerifySnapshotProfileTx(f.ctx, tx, f.pins, WalletReadV2); next != nil || err != ErrCatalog {
		t.Fatal("wallet profile accepted the original four-column gap")
	}
	if tx.Conn().PgConn().TxStatus() != 'T' {
		t.Fatal("wallet profile rejection ended the caller transaction")
	}
	walletProfileRollback(t, tx)
	return p
}

func walletProfileCheckUpgraded(t *testing.T, f walletProfileFixture, legacy *CatalogProof) {
	t.Helper()
	tx := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	for name, issue := range map[string]func() (*CatalogProof, error){
		"original snapshot": func() (*CatalogProof, error) { return VerifySnapshotTx(f.ctx, tx, f.pins) },
		"original runtime":  func() (*CatalogProof, error) { return VerifyRuntimeTx(f.ctx, tx, f.pins, Application) },
		"explicit legacy":   func() (*CatalogProof, error) { return VerifySnapshotProfileTx(f.ctx, tx, f.pins, LegacyV1) },
	} {
		if p, err := issue(); p != nil || err != ErrCatalog {
			t.Fatal("wallet profile widened " + name)
		}
	}
	if p, err := VerifyInstallationTx(f.ctx, tx, f.pins); p != nil || err != ErrCatalog {
		t.Fatal("wallet profile widened original installation issuer")
	}
	p, err := VerifySnapshotProfileTx(f.ctx, tx, f.pins, WalletReadV2)
	if err != nil || p == nil || p.Profile() != WalletReadV2 || p.VerifyTx(f.ctx, tx) != nil || !p.MatchesPins(f.pins) || p.DescriptorVersion() != 1 {
		t.Fatal("wallet profile explicit snapshot issuance")
	}
	if p.SchemaSHA256() != legacy.SchemaSHA256() || p.RolesSHA256() == legacy.RolesSHA256() || p.ExpectedSHA256() == legacy.ExpectedSHA256() {
		t.Fatal("wallet profile schema or roles/root delta")
	}
	walletProfileRollback(t, tx)
	writable := beginCatalogTestTx(t, f.app, pgx.ReadWrite)
	if snapshot, err := VerifySnapshotProfileTx(f.ctx, writable, f.pins, WalletReadV2); err != nil || snapshot == nil || snapshot.Profile() != WalletReadV2 || snapshot.VerifyTx(f.ctx, writable) != nil || snapshot.SchemaSHA256() != p.SchemaSHA256() || snapshot.RolesSHA256() != p.RolesSHA256() {
		t.Fatal("wallet profile snapshot must support caller RR READ WRITE without writing")
	}
	walletProfileRollback(t, writable)
	if operations.VerifyPaymentRole(f.ctx, f.app, operations.PaymentApplicationRole) != operations.ErrPaymentRolePrivileges {
		t.Fatal("wallet profile widened old payment role checker")
	}

	runtimes := []struct {
		pool *pgxpool.Pool
		role RuntimeRole
	}{{f.app, Application}, {f.projector, Projector}}
	if f.worker != nil {
		runtimes = append(runtimes, struct {
			pool *pgxpool.Pool
			role RuntimeRole
		}{f.worker, Worker})
	}
	for _, runtime := range runtimes {
		for _, access := range []pgx.TxAccessMode{pgx.ReadOnly, pgx.ReadWrite} {
			tx := beginCatalogTestTx(t, runtime.pool, access)
			start := time.Now()
			observed := &walletProfileDeadlineTx{Tx: tx}
			proof, err := VerifyRuntimeProfileTx(f.ctx, observed, f.pins, runtime.role, WalletReadV2)
			if access == pgx.ReadWrite {
				if proof != nil || err != ErrCatalog {
					t.Fatal("wallet profile accepted runtime RR READ WRITE")
				}
				walletProfileRollback(t, tx)
				continue
			}
			if err != nil || proof == nil || proof.Profile() != WalletReadV2 || proof.SchemaSHA256() != legacy.SchemaSHA256() || proof.RolesSHA256() != p.RolesSHA256() || proof.ExpectedSHA256() != p.ExpectedSHA256() {
				t.Fatal("wallet profile actual runtime LOGIN issuance")
			}
			if observed.calls == 0 || observed.bad || observed.deadline.Before(start.Add(4900*time.Millisecond)) || observed.deadline.After(observed.firstCall.Add(catalogBudget)) {
				t.Fatal("wallet profile common five-second issuing deadline")
			}
			// The public wrapper itself is bound, together with its physical
			// connection. The embedded original Tx cannot replace that wrapper.
			if proof.VerifyTx(f.ctx, tx) != ErrCatalog {
				t.Fatal("wallet profile rebound to a different transaction wrapper")
			}
			observed.reset()
			if proof.VerifyTx(f.ctx, observed) != nil {
				t.Fatal("wallet profile lost original runtime binding")
			}
			copyOfProof := *proof
			observed.reset()
			if copyOfProof.VerifyTx(f.ctx, observed) != nil {
				t.Fatal("wallet profile copied proof lost identity")
			}
			canceled, stop := context.WithCancel(f.ctx)
			stop()
			if proof.VerifyTx(canceled, observed) != ErrCatalog {
				t.Fatal("wallet profile canceled proof verification")
			}
			if next, err := VerifyRuntimeProfileTx(canceled, observed, f.pins, runtime.role, WalletReadV2); next != nil || err != ErrCatalog {
				t.Fatal("wallet profile canceled runtime issuance")
			}
			wrong := Application
			if runtime.role == Application {
				wrong = Projector
			}
			if next, err := VerifyRuntimeProfileTx(f.ctx, observed, f.pins, wrong, WalletReadV2); next != nil || err != ErrCatalog {
				t.Fatal("wallet profile accepted a different selected LOGIN")
			}
			if f.pins.Mode == Ordinary {
				if next, err := VerifyRuntimeProfileTx(f.ctx, observed, f.pins, Worker, WalletReadV2); next != nil || err != ErrCatalog {
					t.Fatal("wallet profile ordinary worker accepted")
				}
			}
			other := beginCatalogTestTx(t, f.admin, pgx.ReadOnly)
			if proof.VerifyTx(f.ctx, other) != ErrCatalog {
				t.Fatal("wallet profile rebound to another connection")
			}
			walletProfileRollback(t, other)
			if runtime.role == Application {
				if tx.Commit(f.ctx) != nil {
					t.Fatal("wallet profile caller read-only commit")
				}
			} else {
				walletProfileRollback(t, tx)
			}
			if proof.VerifyTx(f.ctx, observed) != ErrCatalog {
				t.Fatal("wallet profile proof survived transaction end")
			}
			later := beginCatalogTestTx(t, runtime.pool, pgx.ReadOnly)
			observed.Tx = later
			observed.reset()
			if proof.VerifyTx(f.ctx, observed) != ErrCatalog {
				t.Fatal("wallet profile rebound the same wrapper to a later transaction")
			}
			walletProfileRollback(t, later)
		}
	}
	// Even a genuine owner selected with SET ROLE is not an actual LOGIN.
	impersonated := beginCatalogTestTx(t, f.admin, pgx.ReadOnly)
	if _, err := impersonated.Exec(f.ctx, "SET ROLE "+pgx.Identifier{f.pins.App.Name}.Sanitize()); err != nil {
		t.Fatal("wallet profile SET ROLE negative setup")
	}
	if proof, err := VerifyRuntimeProfileTx(f.ctx, impersonated, f.pins, Application, WalletReadV2); proof != nil || err != ErrCatalog {
		t.Fatal("wallet profile accepted SET ROLE impersonation")
	}
	walletProfileRollback(t, impersonated)
}

func TestWalletProfileOrdinaryIndependentInstallations(t *testing.T) {
	for round := 1; round <= 2; round++ {
		for _, kind := range ordinaryVariantKinds() {
			t.Run(fmt.Sprintf("round_%d/%s", round, kind), func(t *testing.T) {
				f := walletProfileOrdinary(t, kind)
				legacy := walletProfileLegacyBaseline(t, f)
				walletProfileInstall(t, f)
				walletProfileCheckUpgraded(t, f, legacy)
				walletProfileCompleteRead(t, f)
				t.Log("independent offline catalog fixture: legacy/new profiles separated; actual APP/projector, RR RO, identity, caller lifetime and common five-second deadline verified")
			})
		}
	}
}

func TestWalletProfileSandboxIndependentInstallations(t *testing.T) {
	for round := 1; round <= 2; round++ {
		t.Run(fmt.Sprintf("round_%d", round), func(t *testing.T) {
			f := walletProfileSandbox(t)
			legacy := walletProfileLegacyBaseline(t, f)
			walletProfileInstall(t, f)
			walletProfileCheckUpgraded(t, f, legacy)
			walletProfileCompleteRead(t, f)
			for _, runtime := range []*pgxpool.Pool{f.app, f.worker} {
				for _, peer := range []struct{ schema, table string }{{f.pins.Sandbox.Mock.Name, "mock_orders"}, {f.pins.Sandbox.Receiver.Name, "receiver_inbox"}} {
					_, err := runtime.Exec(f.ctx, "SELECT * FROM "+pgx.Identifier{peer.schema, peer.table}.Sanitize()+" LIMIT 0")
					pgerr, ok := err.(*pgconn.PgError)
					if !ok || pgerr.Code != "42501" {
						t.Fatal("wallet profile APP/worker peer content must remain unreadable")
					}
				}
			}
			t.Log("independent offline full-sandbox catalog fixture: actual APP/worker/projector and peer content denial verified; no genuine payment or full RuntimeProof acceptance")
		})
	}
}

func walletProfileRejectACLDrift(t *testing.T, f walletProfileFixture) {
	t.Helper()
	walletProfileInstall(t, f)
	walletProfileCompareLive(t, f)
	q := func(names ...string) string { return pgx.Identifier(names).Sanitize() }
	table, app, projector := q(f.pins.AppSchema.Name, "payment_query_conflicts"), q(f.pins.App.Name), q(f.pins.Projector.Name)
	type mutation struct{ name, sql string }
	var cases []mutation
	for _, col := range []string{"id", "verified_query_id", "conflicting_digest", "received_at"} {
		cases = append(cases, mutation{"revoke_" + col, "REVOKE SELECT(" + q(col) + ") ON " + table + " FROM " + app})
	}
	cases = append(cases,
		mutation{"table_select", "GRANT SELECT ON " + table + " TO " + app},
		mutation{"table_insert", "GRANT INSERT ON " + table + " TO " + app},
		mutation{"table_update", "GRANT UPDATE ON " + table + " TO " + app},
		mutation{"column_insert", "GRANT INSERT(id) ON " + table + " TO " + app},
		mutation{"column_update", "GRANT UPDATE(id) ON " + table + " TO " + app},
		mutation{"column_references", "GRANT REFERENCES(id) ON " + table + " TO " + app},
		mutation{"column_grant_option", "GRANT SELECT(id) ON " + table + " TO " + app + " WITH GRANT OPTION"},
		mutation{"public_column", "GRANT SELECT(id) ON " + table + " TO PUBLIC"},
		mutation{"wrong_grantee", "GRANT SELECT(id) ON " + table + " TO " + projector},
		mutation{"wrong_grantor_chain", "GRANT SELECT(id) ON " + table + " TO " + projector + " WITH GRANT OPTION; SET LOCAL ROLE " + projector + "; GRANT SELECT(id) ON " + table + " TO " + app + "; RESET ROLE"},
		mutation{"membership", "GRANT " + projector + " TO " + app + " WITH ADMIN FALSE, INHERIT FALSE, SET FALSE"},
	)
	if f.pins.Mode == Sandbox {
		cases = append(cases,
			mutation{"peer_content", "GRANT SELECT ON " + q(f.pins.Sandbox.Mock.Name, "mock_orders") + " TO " + app},
			mutation{"worker_peer_content", "GRANT SELECT ON " + q(f.pins.Sandbox.Receiver.Name, "receiver_inbox") + " TO " + q(f.pins.Worker.Name)},
		)
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			tx := beginCatalogTestTx(t, f.admin, pgx.ReadWrite)
			if _, err := tx.Exec(f.ctx, item.sql); err != nil {
				t.Fatal("wallet profile controlled ACL drift setup")
			}
			if proof, err := VerifySnapshotProfileTx(f.ctx, tx, f.pins, WalletReadV2); proof != nil || err != ErrCatalog {
				t.Fatal("wallet profile accepted unintended ACL or membership")
			}
			var one int
			if tx.QueryRow(f.ctx, "SELECT 1").Scan(&one) != nil || one != 1 || tx.Conn().PgConn().TxStatus() != 'T' {
				t.Fatal("wallet profile rejection changed caller transaction")
			}
			walletProfileRollback(t, tx)
		})
	}
	tx := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	if proof, err := VerifyRuntimeProfileTx(f.ctx, tx, f.pins, Application, WalletReadV2); proof == nil || err != nil {
		t.Fatal("wallet profile drift rollback did not restore exact reviewed profile")
	}
	walletProfileRollback(t, tx)
}

func TestWalletProfileRejectsPrivilegeDrift(t *testing.T) {
	t.Run("ordinary", func(t *testing.T) { walletProfileRejectACLDrift(t, walletProfileOrdinary(t, ordinaryNamed)) })
	t.Run("sandbox", func(t *testing.T) { walletProfileRejectACLDrift(t, walletProfileSandbox(t)) })
}

type walletProfileReadTable struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

type walletProfileReadset struct {
	FormatVersion int                      `json:"format_version"`
	Tables        []walletProfileReadTable `json:"tables"`
	History       walletProfileReadTable   `json:"history"`
	Counts        struct {
		Tables             int `json:"tables"`
		Columns            int `json:"columns"`
		ColumnsWithHistory int `json:"columns_with_history"`
	} `json:"counts"`
	SourceSHA256 map[string]string `json:"source_sha256"`
}

func walletProfileCompleteRead(t *testing.T, f walletProfileFixture) {
	t.Helper()
	raw := ordinaryVariantSource(t, f.root, "specs/022-self-service-payments/c0-wallet-readset.json", walletProfileReadsetSHA)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var readset walletProfileReadset
	if decoder.Decode(&readset) != nil || decoder.Decode(new(any)) != io.EOF || readset.FormatVersion != 1 || readset.Counts.Tables != 84 || readset.Counts.Columns != 776 || readset.Counts.ColumnsWithHistory != 778 || len(readset.Tables) != 84 || readset.History.Name != "schema_migrations" || !same(readset.History.Columns, []string{"name", "checksum"}) {
		t.Fatal("wallet profile fixed full readset")
	}
	for name, sha := range readset.SourceSHA256 {
		ordinaryVariantSource(t, f.root, name, sha)
	}
	tx := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	proof, err := VerifyRuntimeProfileTx(f.ctx, tx, f.pins, Application, WalletReadV2)
	if err != nil || proof == nil {
		t.Fatal("wallet profile actual APP full-read proof")
	}
	var current, session string
	if tx.QueryRow(f.ctx, "SELECT current_user,session_user").Scan(&current, &session) != nil || current != f.pins.App.Name || session != current {
		t.Fatal("wallet profile full read must use actual APP LOGIN")
	}
	columns := 0
	all := append(append([]walletProfileReadTable(nil), readset.Tables...), readset.History)
	for _, table := range all {
		rows, err := tx.Query(f.ctx, `SELECT a.attname,pg_catalog.has_column_privilege(current_user,c.oid,a.attnum,'SELECT')
 FROM pg_catalog.pg_namespace n JOIN pg_catalog.pg_class c ON c.relnamespace=n.oid
 JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid
 WHERE n.nspname=$1 AND c.relname=$2 AND a.attnum>0 AND NOT a.attisdropped AND a.attname=ANY($3::text[])
 ORDER BY a.attname`, f.pins.AppSchema.Name, table.Name, table.Columns)
		if err != nil {
			t.Fatal("wallet profile actual APP column privilege query")
		}
		seen := map[string]bool{}
		for rows.Next() {
			var column string
			var allowed bool
			if rows.Scan(&column, &allowed) != nil || !allowed || seen[column] {
				rows.Close()
				t.Fatalf("wallet profile actual APP cannot read reviewed column %s.%s", table.Name, column)
			}
			seen[column] = true
		}
		rows.Close()
		if rows.Err() != nil || len(seen) != len(table.Columns) {
			t.Fatal("wallet profile column privilege rows incomplete")
		}
		projection := make([]string, len(table.Columns))
		for i, column := range table.Columns {
			if !seen[column] {
				t.Fatal("wallet profile reviewed physical column missing")
			}
			projection[i] = pgx.Identifier{column}.Sanitize()
			columns++
		}
		// No LIMIT 0: consume actual rows through EOF using the complete,
		// explicit physical projection, including migration history separately.
		rows, err = tx.Query(f.ctx, "SELECT "+strings.Join(projection, ",")+" FROM ONLY "+pgx.Identifier{f.pins.AppSchema.Name, table.Name}.Sanitize())
		if err != nil {
			t.Fatalf("wallet profile actual APP projection failed for %s", table.Name)
		}
		fields := rows.FieldDescriptions()
		if len(fields) != len(table.Columns) {
			rows.Close()
			t.Fatal("wallet profile explicit projection field count")
		}
		for i, field := range fields {
			if field.Name != table.Columns[i] {
				rows.Close()
				t.Fatal("wallet profile explicit projection field identity")
			}
		}
		for rows.Next() {
			if _, err := rows.Values(); err != nil {
				rows.Close()
				t.Fatal("wallet profile actual projection row decoding")
			}
		}
		rows.Close()
		if rows.Err() != nil {
			t.Fatal("wallet profile actual projection EOF")
		}
	}
	if columns != 778 {
		t.Fatal("wallet profile omitted a reviewed physical column")
	}
	var tableSelect bool
	if tx.QueryRow(f.ctx, `SELECT pg_catalog.has_table_privilege(current_user,c.oid,'SELECT') FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='payment_query_conflicts'`, f.pins.AppSchema.Name).Scan(&tableSelect) != nil || tableSelect {
		t.Fatal("wallet profile four column reads must not grant table SELECT")
	}
	query := &walletProfileSourceQuery{ctx: f.ctx, tx: tx, schema: f.pins.AppSchema.Name, calls: map[string]int{}, tables: map[string]bool{}}
	for _, table := range readset.Tables {
		query.tables[table.Name] = true
	}
	mode := fundinglotscan.ModeOrdinary
	if f.pins.Mode == Sandbox {
		mode = fundinglotscan.ModeSandbox
	}
	facts, err := fundinglotscan.ReadWallet(f.ctx, query, f.pins.AppSchema.Name, mode)
	if err != nil || !facts.Summary().PhysicalEOFComplete || facts.Summary().ScannedTables != 84 || len(query.calls) != 84 || proof.VerifyTx(f.ctx, tx) != nil {
		t.Fatal("wallet profile actual APP fixed scanner did not complete all 84 physical EOFs")
	}
	walletProfileRollback(t, tx)
	t.Log("actual APP RR: all 84 tables/776 columns plus two history columns readable; every explicit projection and real ReadWallet reached EOF; no table-level query-conflict SELECT; graph/epoch authenticity is outside this catalog fixture")
}

type walletProfileSourceQuery struct {
	ctx    context.Context
	tx     pgx.Tx
	schema string
	tables map[string]bool
	calls  map[string]int
}

func (q *walletProfileSourceQuery) Query(sql string, args ...any) (fundinglotscan.Rows, error) {
	for table := range q.tables {
		if strings.Contains(sql, " FROM ONLY "+pgx.Identifier{q.schema, table}.Sanitize()+" AS s ") {
			q.calls[table]++
			rows, err := q.tx.Query(q.ctx, sql, append([]any{pgx.QueryExecModeSimpleProtocol}, args...)...)
			if err != nil {
				return nil, err
			}
			return walletProfileSourceRows{rows}, nil
		}
	}
	return nil, fundinglotscan.ErrExecution
}

type walletProfileSourceRows struct{ pgx.Rows }

func (r walletProfileSourceRows) Next() (bool, error) {
	if r.Rows.Next() {
		return true, nil
	}
	return false, r.Rows.Err()
}
func (r walletProfileSourceRows) Close() error { r.Rows.Close(); return r.Rows.Err() }

// Independently serialize the complete live documents on one fixture per mode.
// Expected SQL templates are rendered into new owned nodes before serialization;
// neither the old root nor the derived descriptor is changed by this audit.
func walletProfileCompareLive(t *testing.T, f walletProfileFixture) {
	t.Helper()
	expected, _, err := loadExpectedProfile(f.pins, WalletReadV2)
	if err != nil {
		t.Fatal("wallet profile fixed descriptor for live audit")
	}
	tx := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	observed, err := observeCatalogProfileDocumentsTx(f.ctx, tx, f.pins, expected)
	if err != nil {
		t.Fatalf("wallet profile independent metadata observation failed (context_done=%t)", f.ctx.Err() != nil)
	}
	if observed == nil {
		t.Fatal("wallet profile independent metadata observation returned nil")
	}
	if validateCatalogDocuments(observed.observedSchema, observed.observedRoles) != nil {
		walletProfileDiagnoseClosedDocuments(t, observed.observedSchema, observed.observedRoles)
		t.Fatal("wallet profile independent metadata closed DTO audit failed at document headers")
	}
	schema, err := canonical(observed.observedSchema)
	if err != nil {
		t.Fatal("wallet profile live schema canonical audit")
	}
	roles, err := canonical(observed.observedRoles)
	if err != nil {
		t.Fatal("wallet profile live roles canonical audit")
	}
	names, err := schemaIdentifiersTx(f.ctx, tx, f.pins)
	if err != nil {
		t.Fatal("wallet profile live template identifiers")
	}
	matches := 0
	for _, variant := range expected.Variants {
		if !bytes.Equal(roles, variant.rolesCanonical) {
			continue
		}
		rendered := walletProfileRenderExpected(t, variant.Schema, names, descriptorComparePath{})
		want, err := canonical(rendered)
		if err != nil {
			t.Fatal("wallet profile static rendered canonical audit")
		}
		if bytes.Equal(schema, want) {
			matches++
		}
	}
	if matches != 1 {
		t.Fatal("wallet profile complete live bytes did not match exactly one fixed variant")
	}
	walletProfileRollback(t, tx)
}

// On a failed independent audit, report only fixed scope/category/field names
// and ordinal positions. No catalog value, SQL text, or runtime name is logged.
func walletProfileDiagnoseClosedDocuments(t *testing.T, schema, roles document) {
	t.Helper()
	for scope, raw := range schema {
		s, ok := raw.(object)
		if !ok {
			t.Fatalf("wallet profile closed DTO schema scope kind: %s", scope)
		}
		r, ok := roles[scope].(object)
		if !ok {
			t.Fatalf("wallet profile closed DTO roles scope kind: %s", scope)
		}
		for _, set := range []struct {
			family   string
			doc      object
			registry map[string][]string
		}{{"schema", s, schemaRecordFields}, {"roles", r, roleRecordFields}} {
			for category, keys := range set.registry {
				records, ok := set.doc[category].([]any)
				if !ok {
					t.Fatalf("wallet profile closed DTO category kind: %s/%s/%s", scope, set.family, category)
				}
				for i, raw := range records {
					record, ok := raw.(object)
					if !ok || !exactKeys(record, keys...) {
						t.Fatalf("wallet profile closed DTO record shape: %s/%s/%s[%d]", scope, set.family, category, i)
					}
					if validateRecordValues(record, category) != nil {
						t.Fatalf("wallet profile closed DTO record types/enums: %s/%s/%s[%d]", scope, set.family, category, i)
					}
					for key, value := range record {
						if validateNested(value, key) != nil {
							t.Fatalf("wallet profile closed DTO nested field: %s/%s/%s[%d]/%s", scope, set.family, category, i, key)
						}
					}
				}
			}
		}
	}
}

func walletProfileRenderExpected(t *testing.T, value any, names map[string]string, path descriptorComparePath) any {
	t.Helper()
	switch value := value.(type) {
	case object:
		if _, template := value["parts"]; template {
			rendered, err := renderSQLTemplateAllowed(value, names, path.permitsTemplate())
			if err != nil {
				t.Fatal("wallet profile expected template audit")
			}
			return rendered
		}
		out := object{}
		for key, item := range value {
			out[key] = walletProfileRenderExpected(t, item, names, path.child(key))
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = walletProfileRenderExpected(t, item, names, path.child(""))
		}
		return out
	default:
		return value
	}
}

// This instrumentation delegates every operation to a real PostgreSQL Tx. It
// checks that one fixed API context reaches every metadata query, rather than
// granting five new seconds to each stage of an issuing call.
type walletProfileDeadlineTx struct {
	pgx.Tx
	deadline  time.Time
	firstCall time.Time
	calls     int
	bad       bool
}

func (x *walletProfileDeadlineTx) reset() {
	x.deadline, x.firstCall, x.calls, x.bad = time.Time{}, time.Time{}, 0, false
}
func (x *walletProfileDeadlineTx) record(ctx context.Context) {
	d, ok := ctx.Deadline()
	if !ok || x.calls != 0 && !d.Equal(x.deadline) {
		x.bad = true
	}
	if x.calls == 0 {
		x.deadline = d
		x.firstCall = time.Now()
	}
	x.calls++
}
func (x *walletProfileDeadlineTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	x.record(ctx)
	return x.Tx.Query(ctx, sql, args...)
}
func (x *walletProfileDeadlineTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	x.record(ctx)
	return x.Tx.QueryRow(ctx, sql, args...)
}
