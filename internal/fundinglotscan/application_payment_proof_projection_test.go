package fundinglotscan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// The independently committed historical wire supplies descriptive rows only.
// This fake 84-table read is not a catalog, file, or PostgreSQL attestation.
func applicationPaymentProjectionQuery(t *testing.T) (*readerQuery, ApplicationPaymentProofRows) {
	t.Helper()
	raw, err := os.ReadFile("../maintenancetrust/testdata/predecessor_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	type role struct {
		Name string `json:"name"`
		OID  uint32 `json:"oid"`
	}
	var proof struct {
		EnvironmentID     string `json:"environment_id"`
		MerchantID        string `json:"merchant_id"`
		PayeeLabel        string `json:"payee_label"`
		AppNonceDigest    string `json:"app_nonce_digest"`
		WorkerNonceDigest string `json:"worker_nonce_digest"`
		Database          struct {
			OID uint32 `json:"oid"`
		} `json:"database"`
		App struct {
			OID     uint32 `json:"oid"`
			Owner   role   `json:"owner"`
			Runtime role   `json:"runtime"`
		} `json:"app"`
		Worker    role `json:"worker"`
		Projector role `json:"projector"`
	}
	if json.Unmarshal(raw, &proof) != nil || wrSHA(raw) != "1a6c1815c400ddf7b051c0c12f0fe203ff43484251aabd34d8f9361ab704b67c" {
		t.Fatal("independent historical wire changed")
	}
	at := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC).In(time.Local)
	decimal := func(value uint32) string { return fmt.Sprint(value) }
	want := ApplicationPaymentProofRows{
		Environment: &fundinglotverify.SandboxGraphEnvironmentRow{
			Singleton: true, ID: proof.EnvironmentID, MerchantID: proof.MerchantID, PayeeLabel: proof.PayeeLabel,
			DatabaseOID: decimal(proof.Database.OID), SchemaOID: decimal(proof.App.OID), NonceDigest: proof.AppNonceDigest,
			AppRole: proof.App.Runtime.Name, WorkerRole: proof.Worker.Name, ProjectorRole: proof.Projector.Name,
			ProtocolVersion: "1", CreatedAt: at,
		},
		Attestation: &fundinglotverify.SandboxGraphAttestationRow{
			Singleton: true, EnvironmentID: proof.EnvironmentID, DatabaseOID: decimal(proof.Database.OID), SchemaOID: decimal(proof.App.OID),
			SchemaOwnerOID: decimal(proof.App.Owner.OID), AppRoleOID: decimal(proof.App.Runtime.OID), WorkerRoleOID: decimal(proof.Worker.OID),
			ProjectorRoleOID: decimal(proof.Projector.OID), AppNonceDigest: proof.AppNonceDigest, WorkerNonceDigest: proof.WorkerNonceDigest,
			ProofVersion: "1", ProofCanonical: raw, ProofSHA256: wrSHA(raw), CreatedAt: at,
		},
	}
	q := wrNew(t)
	wrEpoch(q)
	e := want.Environment
	r := wrRecord(q, "payment_sandbox_environment", "id", e.ID, "merchant_id", e.MerchantID, "payee_label", e.PayeeLabel,
		"database_oid", e.DatabaseOID, "schema_oid", e.SchemaOID, "nonce_digest", e.NonceDigest, "app_role", e.AppRole,
		"worker_role", e.WorkerRole, "projector_role", e.ProjectorRole, "protocol_version", e.ProtocolVersion)
	r["singleton"] = cell{kind: boolKind, valid: true, boolean: true}
	a := want.Attestation
	r = wrRecord(q, "payment_runtime_attestations", "environment_id", a.EnvironmentID, "database_oid", a.DatabaseOID,
		"schema_oid", a.SchemaOID, "schema_owner_oid", a.SchemaOwnerOID, "app_role_oid", a.AppRoleOID,
		"worker_role_oid", a.WorkerRoleOID, "projector_role_oid", a.ProjectorRoleOID, "app_nonce_digest", a.AppNonceDigest,
		"worker_nonce_digest", a.WorkerNonceDigest, "proof_version", a.ProofVersion, "proof_sha256", a.ProofSHA256)
	r["singleton"] = cell{kind: boolKind, valid: true, boolean: true}
	r["proof_canonical"] = cell{kind: bytesKind, valid: true, data: raw}
	r = q.data["funding_lot_runtime_attestations"][0]
	r["mode"], r["worker_role_oid"], r["predecessor_payment_digest"] = readerText("sandbox"), readerText(a.WorkerRoleOID), readerText(a.ProofSHA256)
	return q, want
}

func applicationPaymentProjectionRead(t *testing.T, q *readerQuery) WalletFacts {
	t.Helper()
	f, err := ReadWallet(context.Background(), q, "reader_fixture", ModeSandbox)
	if err != nil || f.Summary().Status != "consistent" || !f.Summary().CoverageComplete || !epochProofReportQualified(f.state) {
		t.Fatalf("independent sandbox source graph did not qualify: %+v %v", f.Summary(), err)
	}
	wrEOF(t, q)
	return f
}

func applicationPaymentProjectionFacts(t *testing.T) (WalletFacts, *readerQuery, ApplicationPaymentProofRows) {
	t.Helper()
	q, want := applicationPaymentProjectionQuery(t)
	return applicationPaymentProjectionRead(t, q), q, want
}

func applicationPaymentProjectionZero(t *testing.T, f WalletFacts, want error) {
	t.Helper()
	out, err := ProjectApplicationPaymentProof(context.Background(), f)
	if err != want || !reflect.DeepEqual(out, ApplicationPaymentProofRows{}) {
		t.Fatalf("want zero/%v, got populated=%t/%v", want, out.Environment != nil || out.Attestation != nil, err)
	}
}

// White-box transport edits isolate the projector's validation from the public
// graph algorithms. Public callers cannot edit or manufacture WalletFacts.
func applicationPaymentProjectionRecount(t *testing.T, f WalletFacts) {
	t.Helper()
	budget := &session{}
	for _, table := range f.state.raw.tables {
		for _, row := range table.rows {
			if err := budget.addTableRow(table.spec, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.state.raw.rows, f.state.raw.bytes = budget.rows, budget.bytes
	f.state.report.SourceRows, f.state.report.SourceBytes = uint64(budget.rows), uint64(budget.bytes)
	counts := &f.state.raw.report.Sandbox.Scanned
	counts.Environments = uint64(len(f.state.raw.tables[f.state.raw.index["payment_sandbox_environment"]].rows))
	counts.Attestations = uint64(len(f.state.raw.tables[f.state.raw.index["payment_runtime_attestations"]].rows))
	if f.state.raw.mode == ModeSandbox {
		f.state.report.Sandbox.Scanned = *counts
	}
}

func TestProjectApplicationPaymentProofOriginalAndOwned(t *testing.T) {
	f, q, want := applicationPaymentProjectionFacts(t)
	before, calls := f.Summary(), len(q.calls)
	out, err := ProjectApplicationPaymentProof(context.Background(), f)
	if err != nil || !reflect.DeepEqual(out, want) || len(q.calls) != calls || f.Summary() != before || len(f.state.raw.rejected) != 0 {
		t.Fatal("projection queried, changed originals, or lost fields", err)
	}
	for _, group := range []struct {
		table string
		row   any
	}{
		{"payment_sandbox_environment", out.Environment}, {"payment_runtime_attestations", out.Attestation},
	} {
		v := reflect.ValueOf(group.row).Elem()
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).Kind() != reflect.String {
				continue
			}
			column := sandboxProjectionColumn(group.table, v.Type().Field(i).Name)
			if unsafe.StringData(v.Field(i).String()) == unsafe.StringData(epochProjectionCell(f, group.table, column).text) {
				t.Fatalf("output string aliases original %s.%s", group.table, column)
			}
		}
	}
	out.Environment.ID, out.Attestation.EnvironmentID = "changed", "changed"
	out.Attestation.ProofCanonical[0] ^= 1
	again, err := ProjectApplicationPaymentProof(context.Background(), f)
	if err != nil || !reflect.DeepEqual(again, want) || again.Environment == out.Environment || again.Attestation == out.Attestation {
		t.Fatal("output mutation changed facts or another projection", err)
	}
	old, cancel := context.WithCancel(context.Background())
	cancel()
	f.state.raw.ctx, f.state.raw.query = old, nil
	if got, err := ProjectApplicationPaymentProof(context.Background(), f); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("projection retained original context or Query", err)
	}
}

func TestProjectApplicationPaymentProofOrdinaryAndSingletons(t *testing.T) {
	f, q := epochProjectionFacts(t)
	before, calls := f.Summary(), len(q.calls)
	out, err := ProjectApplicationPaymentProof(context.Background(), f)
	if err != nil || !reflect.DeepEqual(out, ApplicationPaymentProofRows{}) || len(q.calls) != calls || f.Summary() != before {
		t.Fatal("ordinary mode must return both nil without reading again", err)
	}
	for _, table := range []string{"payment_sandbox_environment", "payment_runtime_attestations"} {
		t.Run("ordinary_nonempty_"+table, func(t *testing.T) {
			ordinary, _ := epochProjectionFacts(t)
			sandbox, _, _ := applicationPaymentProjectionFacts(t)
			ordinary.state.raw.tables[ordinary.state.raw.index[table]].rows = sandbox.state.raw.tables[sandbox.state.raw.index[table]].rows
			applicationPaymentProjectionRecount(t, ordinary)
			// Ordinary has no sandbox algorithm/count authority.
			ordinary.state.raw.report.Sandbox = fundinglotverify.SandboxSourceGraphCheck{}
			applicationPaymentProjectionZero(t, ordinary, ErrInput)
		})
		for _, count := range []int{0, 2} {
			t.Run(fmt.Sprintf("sandbox_%s_%d", table, count), func(t *testing.T) {
				f, _, _ := applicationPaymentProjectionFacts(t)
				rows := &f.state.raw.tables[f.state.raw.index[table]].rows
				if count == 0 {
					*rows = nil
				} else {
					first := append(rawRow(nil), (*rows)[0]...)
					first[f.state.raw.tables[f.state.raw.index[table]].spec.index["singleton"]].boolean = false
					*rows = append([]rawRow{first}, (*rows)...)
				}
				applicationPaymentProjectionRecount(t, f)
				applicationPaymentProjectionZero(t, f, ErrInput)
			})
		}
	}
}

func TestProjectApplicationPaymentProofQualificationsAndPhysicalUnion(t *testing.T) {
	for name, damage := range map[string]func(WalletFacts){
		"wrong scope":                func(f WalletFacts) { f.state.report.Scope = "complete_business_source_graphs_v1" },
		"wrong mode":                 func(f WalletFacts) { f.state.report.Mode = ModeOrdinary },
		"invalid mode":               func(f WalletFacts) { f.state.raw.mode = 0; f.state.report.Mode = 0 },
		"mismatch":                   func(f WalletFacts) { f.state.report.Status = "mismatch" },
		"old 68 count":               func(f WalletFacts) { f.state.report.ScannedTables = 68 },
		"no physical EOF":            func(f WalletFacts) { f.state.report.PhysicalEOFComplete = false },
		"no coverage":                func(f WalletFacts) { f.state.report.CoverageComplete = false },
		"algorithm omitted":          func(f WalletFacts) { f.state.report.Algorithms[6] = "not_applicable" },
		"source algorithm omitted":   func(f WalletFacts) { f.state.raw.report.Algorithms[0] = "input_mismatch" },
		"source report changed":      func(f WalletFacts) { f.state.raw.report.Status = "mismatch" },
		"input mismatch":             func(f WalletFacts) { f.state.report.InputMismatchFields++ },
		"journal mismatch":           func(f WalletFacts) { f.state.report.JournalMetadataMismatches++ },
		"ordinary boundary mismatch": func(f WalletFacts) { f.state.report.OrdinaryBoundaryMismatches++ },
		"rejected raw cell":          func(f WalletFacts) { f.state.raw.rejected[cellRef{"tenants", 0, 0}] = true },
		"wallet mismatch":            func(f WalletFacts) { f.state.report.Wallet.Mismatches.EpochRows++ },
		"wallet unproven":            func(f WalletFacts) { f.state.report.Wallet.Unproven.ReplayPrefixUnproven++ },
		"copied child report":        func(f WalletFacts) { f.state.report.Sandbox.Scanned.Environments++ },
		"source sandbox mismatch": func(f WalletFacts) {
			f.state.report.Sandbox.Mismatches.EnvironmentRows++
			f.state.raw.report.Sandbox = f.state.report.Sandbox
		},
		"source sandbox unproven": func(f WalletFacts) {
			f.state.report.Sandbox.Unproven.ReservedReviewSemanticsUnproven++
			f.state.raw.report.Sandbox = f.state.report.Sandbox
		},
		"source ledger mismatch": func(f WalletFacts) {
			f.state.report.Ledger.Mismatches.UnbalancedJournals++
			f.state.raw.report.Ledger = f.state.report.Ledger
		},
		"stale wallet count": func(f WalletFacts) { f.state.report.Wallet.Scanned.Epochs++ },
		"stale source count": func(f WalletFacts) {
			f.state.raw.report.Sandbox.Scanned.Environments++
			f.state.report.Sandbox = f.state.raw.report.Sandbox
		},
		"extra reported rows":  func(f WalletFacts) { f.state.report.SourceRows = rowLimit + 1 },
		"extra reported bytes": func(f WalletFacts) { f.state.report.SourceBytes = byteLimit + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := applicationPaymentProjectionFacts(t)
			damage(f)
			applicationPaymentProjectionZero(t, f, ErrInput)
		})
	}
	for name, tc := range map[string]struct {
		damage func(WalletFacts)
		want   error
	}{
		"missing physical table":     {func(f WalletFacts) { f.state.raw.tables = f.state.raw.tables[:83] }, ErrInput},
		"unfinished unrelated table": {func(f WalletFacts) { f.state.raw.tables[0].eof = false }, ErrInput},
		"wrong registry":             {func(f WalletFacts) { f.state.raw.index["payment_runtime_attestations"] = 0 }, ErrExecution},
		"wrong spec":                 {func(f WalletFacts) { f.state.raw.tables[0].spec.columns[0].name = "wrong" }, ErrExecution},
		"wrong cell kind":            {func(f WalletFacts) { epochProjectionCell(f, "payment_sandbox_environment", "id").kind = bytesKind }, ErrExecution},
		"bad native valid bit": {func(f WalletFacts) {
			epochProjectionCell(f, "payment_sandbox_environment", "created_at").instant.Valid = false
		}, ErrExecution},
		"wrong source bytes": {func(f WalletFacts) { f.state.report.SourceBytes++ }, ErrExecution},
		"wrong raw bytes":    {func(f WalletFacts) { f.state.raw.bytes++ }, ErrExecution},
		"proof uses ordinary field bound": {func(f WalletFacts) {
			epochProjectionCell(f, "payment_runtime_attestations", "proof_canonical").data = make([]byte, fieldLimit+1)
		}, ErrResource},
		"unrelated v2 proof remains bounded": {func(f WalletFacts) {
			epochProjectionCell(f, "funding_lot_runtime_attestations", "proof_canonical").data = make([]byte, 262145)
		}, ErrResource},
		"unrelated text remains bounded": {func(f WalletFacts) {
			epochProjectionCell(f, "funding_lot_epoch", "available_total").text = strings.Repeat("1", fieldLimit+1)
		}, ErrResource},
		"invalid utf8": {func(f WalletFacts) {
			epochProjectionCell(f, "payment_sandbox_environment", "id").text = string([]byte{0xff})
		}, ErrInput},
		"nul text": {func(f WalletFacts) {
			epochProjectionCell(f, "payment_runtime_attestations", "environment_id").text = "\x00"
		}, ErrInput},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := applicationPaymentProjectionFacts(t)
			tc.damage(f)
			applicationPaymentProjectionZero(t, f, tc.want)
		})
	}
	applicationPaymentProjectionZero(t, WalletFacts{}, ErrInput)
}

func TestProjectApplicationPaymentProofCumulativeBudgetAndOldFacts(t *testing.T) {
	f, _, _ := applicationPaymentProjectionFacts(t)
	table := &f.state.raw.tables[f.state.raw.index["users"]]
	// Individually bounded values across the real union still share one 16MiB
	// transport budget; the APP projection must not budget only its two rows.
	for n := 0; n < 257; n++ {
		row := make(rawRow, len(table.spec.columns))
		for ci, column := range table.spec.columns {
			row[ci] = cell{kind: column.kind, valid: true}
		}
		id := fmt.Sprintf("%06d", n)
		row[table.spec.index["id"]] = readerText(id + strings.Repeat("a", fieldLimit-len(id)))
		table.rows = append(table.rows, row)
	}
	applicationPaymentProjectionZero(t, f, ErrResource)
	ordinary, _ := epochProjectionFacts(t)
	old, err := Read(context.Background(), newReaderQuery(t, true), "reader_fixture", ModeOrdinary)
	if err != nil {
		t.Fatal(err)
	}
	ordinary.state.raw = old.state
	applicationPaymentProjectionZero(t, ordinary, ErrInput)
}

func TestProjectApplicationPaymentProofTimeAndRequiredOriginals(t *testing.T) {
	q, want := applicationPaymentProjectionQuery(t)
	// The existing sandbox graph accepts a finite year-1 environment instant;
	// Go represents this valid original as its zero time value.
	q.data["payment_sandbox_environment"][0]["created_at"] = cell{kind: timeKind, valid: true, instant: pgtype.Timestamptz{Valid: true}}
	want.Environment.CreatedAt = time.Time{}.In(time.Local)
	f := applicationPaymentProjectionRead(t, q)
	out, err := ProjectApplicationPaymentProof(context.Background(), f)
	if err != nil || !reflect.DeepEqual(out, want) {
		t.Fatal("valid year-1 environment time was narrowed", err)
	}
	for _, year := range []int{1, 9999} {
		f, _, _ := applicationPaymentProjectionFacts(t)
		at := time.Date(year, 2, 3, 4, 5, 6, 123456000, time.FixedZone("original", 3600))
		for _, table := range []string{"payment_sandbox_environment", "payment_runtime_attestations"} {
			*epochProjectionCell(f, table, "created_at") = cell{kind: timeKind, valid: true, instant: pgtype.Timestamptz{Valid: true, Time: at}}
		}
		out, err := ProjectApplicationPaymentProof(context.Background(), f)
		if err != nil || out.Environment.CreatedAt != at || out.Attestation.CreatedAt != at {
			t.Fatal("projection changed native finite time", err)
		}
	}
	for _, table := range []string{"payment_sandbox_environment", "payment_runtime_attestations"} {
		for name, at := range map[string]pgtype.Timestamptz{
			"null": {}, "infinite": {Valid: true, InfinityModifier: pgtype.Infinity},
			"year_zero":       {Valid: true, Time: time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)},
			"year_10000":      {Valid: true, Time: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)},
			"sub_microsecond": {Valid: true, Time: time.Date(2026, 1, 1, 0, 0, 0, 1, time.UTC)},
		} {
			t.Run(table+"_"+name, func(t *testing.T) {
				f, _, _ := applicationPaymentProjectionFacts(t)
				*epochProjectionCell(f, table, "created_at") = cell{kind: timeKind, valid: at.Valid, instant: at}
				applicationPaymentProjectionZero(t, f, ErrInput)
			})
		}
	}
	for name, damage := range map[string]func(WalletFacts){
		"zero attestation time": func(f WalletFacts) {
			epochProjectionCell(f, "payment_runtime_attestations", "created_at").instant.Time = time.Time{}
		},
		"null original": func(f WalletFacts) {
			epochProjectionCell(f, "payment_runtime_attestations", "proof_canonical").valid = false
		},
		"empty original": func(f WalletFacts) {
			epochProjectionCell(f, "payment_runtime_attestations", "proof_canonical").data = nil
		},
		"null environment field": func(f WalletFacts) {
			epochProjectionCell(f, "payment_sandbox_environment", "merchant_id").valid = false
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := applicationPaymentProjectionFacts(t)
			damage(f)
			applicationPaymentProjectionRecount(t, f)
			applicationPaymentProjectionZero(t, f, ErrInput)
			if len(f.state.raw.rejected) != 0 {
				t.Fatal("failed projection changed original rejected map")
			}
		})
	}
}

func TestProjectApplicationPaymentProofContextAndConcurrentReaders(t *testing.T) {
	f, _, want := applicationPaymentProjectionFacts(t)
	if out, err := ProjectApplicationPaymentProof(nil, f); err != ErrInput || !reflect.DeepEqual(out, ApplicationPaymentProofRows{}) {
		t.Fatal("nil context must return zero/input")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	timed, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, tc := range []struct {
		ctx  context.Context
		want error
	}{{canceled, ErrCanceled}, {timed, ErrTimeout}} {
		out, err := ProjectApplicationPaymentProof(tc.ctx, f)
		if err != tc.want || !reflect.DeepEqual(out, ApplicationPaymentProofRows{}) {
			t.Fatal("expired caller did not return atomic fixed failure", err)
		}
	}
	probe := &seedCountingContext{Context: context.Background()}
	if _, err := ProjectApplicationPaymentProof(probe, f); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []struct{ cause, want error }{{context.Canceled, ErrCanceled}, {context.DeadlineExceeded, ErrTimeout}} {
		for _, at := range []int{1, 5, 45, probe.calls / 2, probe.calls - 3, probe.calls} {
			ctx := &seedCountingContext{Context: context.Background(), failAt: at, failure: failure.cause}
			out, err := ProjectApplicationPaymentProof(ctx, f)
			if err != failure.want || !reflect.DeepEqual(out, ApplicationPaymentProofRows{}) || len(f.state.raw.rejected) != 0 {
				t.Fatalf("interrupted projection at %d published partial rows or changed raw: %v", at, err)
			}
		}
	}
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for n := 0; n < 4; n++ {
				out, err := ProjectApplicationPaymentProof(context.Background(), f)
				if err != nil || !reflect.DeepEqual(out, want) {
					t.Error("concurrent projection changed originals", err)
				}
			}
		}()
	}
	readers.Wait()
}
