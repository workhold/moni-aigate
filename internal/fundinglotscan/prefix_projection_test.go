package fundinglotscan

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func prSourceFacts(t *testing.T) (PrefixFacts, *readerQuery) {
	t.Helper()
	q := prQuery(t, false)
	prEmptyWindowHistory(q)
	for _, kind := range []string{"AVAILABLE", "HELD"} {
		wrRecord(q, "ledger_accounts", "id", "account-"+kind, "tenant_id", wrID(2), "kind", kind, "balance", "0")
	}
	f := prSource(t, q)
	if f.Summary().Status != "consistent" {
		t.Fatalf("source projection input invalid: %+v", f.Summary())
	}
	return f, q
}
func prWalletFacts(t *testing.T) (PrefixWalletFacts, *readerQuery) {
	t.Helper()
	q := prQuery(t, true)
	wrEpoch(q)
	f := prWallet(t, q)
	if f.Summary().Status != "consistent" {
		t.Fatalf("wallet projection input invalid: %+v", f.Summary())
	}
	return f, q
}
func prCell(s *session, table, column string) *cell {
	t := &s.tables[s.index[table]]
	return &t.rows[0][t.spec.index[column]]
}

func TestPrefixThreeProjectionsNoQueryOwnedDataAndOriginalContext(t *testing.T) {
	f, q := prSourceFacts(t)
	before, calls := f.Summary(), len(q.calls)
	out, err := ProjectLegacySeedPrefix(context.Background(), f)
	if err != nil || len(out.Tenants) != 1 || len(out.Accounts) != 2 || out.Accounts[0].TenantID == nil {
		t.Fatal("source DTO rejected", err)
	}
	if unsafe.StringData(out.Tenants[0].ID) == unsafe.StringData(prCell(f.state.raw, "tenants", "id").text) || unsafe.StringData(*out.Accounts[0].TenantID) == unsafe.StringData(prCell(f.state.raw, "ledger_accounts", "tenant_id").text) {
		t.Fatal("seed string storage aliases private raw")
	}
	second, err := ProjectLegacySeedPrefix(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if &out.Tenants[0] == &second.Tenants[0] || out.Accounts[0].TenantID == second.Accounts[0].TenantID {
		t.Fatal("separate seed calls share slices/pointers")
	}
	out.Tenants[0].ID, *out.Accounts[0].TenantID = "mutated", "mutated"
	third, err := ProjectLegacySeedPrefix(context.Background(), f)
	if err != nil || !reflect.DeepEqual(second, third) || len(q.calls) != calls || f.Summary() != before || len(f.state.raw.rejected) != 0 {
		t.Fatal("seed mutation escaped DTO", err)
	}

	w, wq := prWalletFacts(t)
	wb, wcalls := w.Summary(), len(wq.calls)
	epoch, err := ProjectEpochProofPrefix(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if unsafe.StringData(epoch.Epoch.ID) == unsafe.StringData(prCell(w.state.raw, "funding_lot_epoch", "id").text) {
		t.Fatal("epoch string aliases raw")
	}
	epoch.Attestation.ProofCanonical[0] ^= 1
	again, err := ProjectEpochProofPrefix(context.Background(), w)
	if err != nil || string(again.Attestation.ProofCanonical) != "independent wallet reader fixture" || again.Epoch.ID != wrID(1) {
		t.Fatal("epoch bytes alias raw", err)
	}
	ordinary, err := ProjectApplicationPaymentProofPrefix(context.Background(), w)
	if err != nil || ordinary != (ApplicationPaymentProofRows{}) {
		t.Fatal("ordinary fabricated payment originals", err)
	}
	if len(wq.calls) != wcalls || w.Summary() != wb || len(w.state.raw.rejected) != 0 {
		t.Fatal("wallet projections mutated/query raw")
	}

	old, cancel := context.WithCancel(context.Background())
	cancel()
	f.state.raw.ctx, f.state.raw.query = old, nil
	w.state.raw.ctx, w.state.raw.query = old, nil
	if _, err = ProjectLegacySeedPrefix(context.Background(), f); err != nil {
		t.Fatal("source projection inherited old context/query", err)
	}
	if _, err = ProjectEpochProofPrefix(context.Background(), w); err != nil {
		t.Fatal("epoch projection inherited old context/query", err)
	}
	if _, err = ProjectApplicationPaymentProofPrefix(context.Background(), w); err != nil {
		t.Fatal("APP projection inherited old context/query", err)
	}
}

func TestPrefixSandboxPaymentAndEpochNullableDeepCopy(t *testing.T) {
	q, want := applicationPaymentProjectionQuery(t)
	prUpgrade(t, q)
	f, err := ReadWalletPrefix(context.Background(), q, "reader_fixture", ModeSandbox)
	if err != nil || f.Summary().Status != "consistent" {
		t.Fatalf("sandbox prefix input invalid: %+v / %v", f.Summary(), err)
	}
	prEOF(t, q, 87)
	calls, before := len(q.calls), f.Summary()
	out, err := ProjectApplicationPaymentProofPrefix(context.Background(), f)
	if err != nil || !reflect.DeepEqual(out, want) {
		t.Fatal("sandbox original copy differs", err)
	}
	if unsafe.StringData(out.Environment.ID) == unsafe.StringData(prCell(f.state.raw, "payment_sandbox_environment", "id").text) {
		t.Fatal("sandbox string aliases raw")
	}
	out.Environment.ID = "mutated"
	out.Attestation.ProofCanonical[0] ^= 1
	second, err := ProjectApplicationPaymentProofPrefix(context.Background(), f)
	if err != nil || !reflect.DeepEqual(second, want) {
		t.Fatal("sandbox caller mutation escaped DTO", err)
	}
	epoch, err := ProjectEpochProofPrefix(context.Background(), f)
	if err != nil || epoch.Attestation.WorkerRoleOID == nil || epoch.Attestation.PredecessorPaymentDigest == nil {
		t.Fatal("sandbox epoch lost nullable originals", err)
	}
	worker, predecessor := *epoch.Attestation.WorkerRoleOID, *epoch.Attestation.PredecessorPaymentDigest
	*epoch.Attestation.WorkerRoleOID, *epoch.Attestation.PredecessorPaymentDigest = "mutated", "mutated"
	secondEpoch, err := ProjectEpochProofPrefix(context.Background(), f)
	if err != nil || *secondEpoch.Attestation.WorkerRoleOID != worker || *secondEpoch.Attestation.PredecessorPaymentDigest != predecessor || epoch.Attestation.WorkerRoleOID == secondEpoch.Attestation.WorkerRoleOID {
		t.Fatal("epoch nullable originals alias raw", err)
	}
	if len(q.calls) != calls || f.Summary() != before {
		t.Fatal("sandbox projections queried/mutated raw")
	}
}

func TestPrefixProjectionReportAndRawTamperingRejected(t *testing.T) {
	sourceCases := map[string]func(*prefixFactsState){
		"scope":        func(f *prefixFactsState) { f.report.Scope = "complete_business_source_graphs_v1" },
		"columns":      func(f *prefixFactsState) { f.report.ScannedColumns-- },
		"tables":       func(f *prefixFactsState) { f.report.ScannedTables-- },
		"row_count":    func(f *prefixFactsState) { f.report.SourceRows++ },
		"byte_count":   func(f *prefixFactsState) { f.report.SourceBytes++ },
		"core_count":   func(f *prefixFactsState) { f.report.Customer.Scanned.Tenants++ },
		"prefix_count": func(f *prefixFactsState) { f.report.PeriodicPrefix.Scanned.Observations++ },
		"prefix_bytes": func(f *prefixFactsState) { f.report.PeriodicPrefix.Scanned.LogicalBytes++ },
		"prefix_base":  func(f *prefixFactsState) { f.report.PeriodicPrefix.Base.Unproven.OperationSnapshotPrefixUnproven = 0 },
		"old_raw_gap":  func(f *prefixFactsState) { f.raw.report.Periodic.Unproven.OperationSnapshotPrefixUnproven = 0 },
		"generated": func(f *prefixFactsState) {
			*prCell(f.raw, "periodic_budget_observation_journal", "command_source_id") = readerText(wrID(5))
		},
		"observation_null": func(f *prefixFactsState) { *prCell(f.raw, "periodic_budget_operations", "observation_id") = wrNull() },
		"origin_null": func(f *prefixFactsState) {
			*prCell(f.raw, "periodic_budget_observation_origins", "tracking_started_at") = cell{kind: timeKind}
		},
		"head_xid": func(f *prefixFactsState) {
			*prCell(f.raw, "periodic_budget_observation_heads", "updated_xid") = readerText("21")
		},
		"rejected": func(f *prefixFactsState) { f.raw.rejected[cellRef{"tenants", 0, 0}] = true },
		"eof":      func(f *prefixFactsState) { f.raw.tables[f.raw.index["periodic_budget_observation_heads"]].eof = false },
	}
	for name, damage := range sourceCases {
		t.Run("source/"+name, func(t *testing.T) {
			f, _ := prSourceFacts(t)
			damage(f.state)
			out, err := ProjectLegacySeedPrefix(context.Background(), f)
			if err == nil || !reflect.DeepEqual(out, LegacySeedRows{}) {
				t.Fatal("tampered source accepted", err)
			}
		})
	}
	walletCases := map[string]func(*prefixWalletFactsState){
		"scope":        func(f *prefixWalletFactsState) { f.report.Scope = "complete_source_wallet_graphs_v1" },
		"columns":      func(f *prefixWalletFactsState) { f.report.ScannedColumns-- },
		"wallet_count": func(f *prefixWalletFactsState) { f.report.Wallet.Scanned.Epochs++ },
		"prefix_count": func(f *prefixWalletFactsState) { f.report.PeriodicPrefix.Scanned.TotalRows++ },
		"prefix_state": func(f *prefixWalletFactsState) { f.report.Algorithms[8] = "not_applicable" },
		"raw_counts":   func(f *prefixWalletFactsState) { f.raw.rows++ },
		"raw_registry": func(f *prefixWalletFactsState) {
			f.raw.tables[f.raw.index["periodic_budget_observation_heads"]].spec.columns[0].name = "unreviewed"
		},
		"eof": func(f *prefixWalletFactsState) {
			f.raw.tables[f.raw.index["periodic_budget_observation_heads"]].eof = false
		},
		"raw_original": func(f *prefixWalletFactsState) { prCell(f.raw, "funding_lot_epoch", "actor").text = "changed" },
	}
	for name, damage := range walletCases {
		t.Run("wallet/"+name, func(t *testing.T) {
			f, _ := prWalletFacts(t)
			damage(f.state)
			out, err := ProjectEpochProofPrefix(context.Background(), f)
			if err == nil || !reflect.DeepEqual(out, EpochProofRows{}) {
				t.Fatal("tampered wallet accepted epoch", err)
			}
			app, err := ProjectApplicationPaymentProofPrefix(context.Background(), f)
			if err == nil || app != (ApplicationPaymentProofRows{}) {
				t.Fatal("tampered wallet accepted APP", err)
			}
		})
	}
}

func TestPrefixProjectionNullInputContextAndConcurrentCalls(t *testing.T) {
	source, _ := prSourceFacts(t)
	wallet, _ := prWalletFacts(t)
	for _, ctx := range []context.Context{nil, func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }(), func() context.Context {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		cancel()
		return ctx
	}()} {
		want := ErrInput
		if ctx != nil {
			want = contextFailure(ctx)
		}
		out, err := ProjectLegacySeedPrefix(ctx, source)
		if err != want || !reflect.DeepEqual(out, LegacySeedRows{}) {
			t.Fatal("source context result", err)
		}
		epoch, err := ProjectEpochProofPrefix(ctx, wallet)
		if err != want || !reflect.DeepEqual(epoch, EpochProofRows{}) {
			t.Fatal("epoch context result", err)
		}
		app, err := ProjectApplicationPaymentProofPrefix(ctx, wallet)
		if err != want || app != (ApplicationPaymentProofRows{}) {
			t.Fatal("APP context result", err)
		}
	}
	if out, err := ProjectLegacySeedPrefix(context.Background(), PrefixFacts{}); err != ErrInput || !reflect.DeepEqual(out, LegacySeedRows{}) {
		t.Fatal("zero source accepted")
	}
	if out, err := ProjectEpochProofPrefix(context.Background(), PrefixWalletFacts{}); err != ErrInput || !reflect.DeepEqual(out, EpochProofRows{}) {
		t.Fatal("zero wallet accepted epoch")
	}
	if out, err := ProjectApplicationPaymentProofPrefix(context.Background(), PrefixWalletFacts{}); err != ErrInput || out != (ApplicationPaymentProofRows{}) {
		t.Fatal("zero wallet accepted APP")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seed, err := ProjectLegacySeedPrefix(context.Background(), source)
			if err != nil {
				errs <- err
			} else {
				*seed.Accounts[0].TenantID = strings.Repeat("x", 8)
			}
			epoch, err := ProjectEpochProofPrefix(context.Background(), wallet)
			if err != nil {
				errs <- err
			} else {
				epoch.Attestation.ProofCanonical[0] ^= 1
			}
			_, err = ProjectApplicationPaymentProofPrefix(context.Background(), wallet)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal("concurrent independent projection", err)
	}
	if source.Summary().Periodic.Unproven != (fundinglotverify.PeriodicBudgetGraphGaps{OperationSnapshotPrefixUnproven: 1}) {
		t.Fatal("parallel projection rewrote old gap")
	}
}
