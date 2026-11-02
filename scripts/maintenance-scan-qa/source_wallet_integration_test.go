package maintenancescanqa_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/maintenancescan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// This is an independent content inventory, not a reader completion oracle.
// No test uses the lease to write facts or fabricate a post-epoch phase.
func assertSourceWalletC0Empty(t *testing.T, f *consumerFixture, ctx context.Context) {
	t.Helper()
	for _, table := range []string{
		"funding_consumption_refund_lots", "funding_lot_balances", "funding_lot_epoch", "funding_lot_events",
		"funding_lot_legacy_approved_refunds", "funding_lot_legacy_openings", "funding_lot_legacy_receipts", "funding_lot_legacy_sandbox_receipts",
		"funding_lot_legacy_settlements", "funding_lot_runtime_attestations", "funding_lots", "funding_reservation_lots",
		"funding_settlement_lots", "funding_wallet_heads", "funding_wallet_legacy_sources", "funding_wallet_operations",
	} {
		var count int64
		if f.admin.QueryRow(ctx, "SELECT pg_catalog.count(*) FROM ONLY "+pgx.Identifier{f.env.Schemas["app"], table}.Sanitize()).Scan(&count) != nil || count != 0 {
			t.Fatal("source-wallet maintenance must leave every pre-epoch C0 table empty", table)
		}
	}
}

func sourceWalletSnapshot(t *testing.T, f *consumerFixture, ctx context.Context, initialize bool) (*maintenancetrust.Gate, *maintenancetrust.Snapshot, *maintenancetrust.Permit) {
	t.Helper()
	f.awaitOwnedGone(t)
	gate, err := maintenancetrust.Acquire(ctx, f.cfg, f.pins)
	if err != nil || gate == nil {
		t.Fatal("source-wallet original Acquire failed", err)
	}
	f.gates = append(f.gates, gate)
	f.observeMaintenance(t)
	var snapshot *maintenancetrust.Snapshot
	if initialize {
		snapshot, err = gate.BeginInit(ctx)
	} else {
		snapshot, err = gate.BeginPlan(ctx)
	}
	if err != nil || snapshot == nil {
		t.Fatal("source-wallet original snapshot failed", err)
	}
	if proof, err := snapshot.VerifyInstallation(ctx); err != nil || proof == nil {
		t.Fatal("source-wallet original empty installation failed", err)
	}
	permit, err := maintenancetrust.Attest(ctx, snapshot)
	if err != nil || permit == nil {
		t.Fatal("source-wallet original predecessor attestation failed", err)
	}
	return gate, snapshot, permit
}

func TestMaintenanceSourceWalletPostgresPreEpochPlanInit(t *testing.T) {
	for _, initialize := range []bool{false, true} {
		t.Run(map[bool]string{false: "plan", true: "init"}[initialize], func(t *testing.T) {
			f := newConsumerFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			before := completeSourceEdgeSnapshot(t, f, ctx)
			assertSourceWalletC0Empty(t, f, ctx)
			gate, snapshot, permit := sourceWalletSnapshot(t, f, ctx, initialize)
			lease, err := permit.BeginScan(ctx, maintenancetrust.ScanSourceWallet)
			if err != nil || lease == nil {
				t.Fatal("source-wallet original seventh-kind lease failed", err)
			}
			result, err := maintenancescan.VerifySourceWalletMaintenance(lease)
			if err != nil || result == nil || result.Verify(permit) != nil {
				t.Fatal("source-wallet fixed maintenance read failed", err)
			}
			report, err := result.Summary(permit)
			if err != nil || report.Scope != "complete_source_wallet_graphs_v1" || report.Mode != fundinglotscan.ModeSandbox || !report.PhysicalEOFComplete || report.ScannedTables != 84 || report.SourceRows == 0 || report.InputMismatchFields != 0 || report.Status != "mismatch" {
				t.Fatal("source-wallet read lost its exact physical scope or pre-epoch mismatch", err, report)
			}
			if report.Algorithms != [8]string{"executed", "executed", "executed", "executed", "executed", "executed", "executed", "executed"} ||
				report.Wallet.Scope != "supplied_wallet_lots_rows" || report.Wallet.Status != "mismatch" || report.Wallet.RowsConsistent || report.Wallet.Scanned.Epochs != 0 || report.Wallet.Scanned.Attestations != 0 {
				t.Fatal("source-wallet invented an initialized wallet or skipped an algorithm", report)
			}
			if proof, err := lease.Finish(); proof != nil || err == nil || result.Verify(permit) != nil {
				t.Fatal("source-wallet wrapper failed to retain the unique Finish")
			}
			if next, err := permit.BeginScan(ctx, maintenancetrust.ScanSourceWallet); next != nil || err == nil || result.Verify(permit) != nil {
				t.Fatal("source-wallet seventh kind repeated or disturbed the original proof")
			}
			// The old sixth-kind read remains independent, with its original
			// 68-table scope. Neither completed result is promoted into the other.
			oldLease, err := permit.BeginScan(ctx, maintenancetrust.ScanCompleteSources)
			if err != nil {
				t.Fatal("source-wallet seventh bit consumed the old sixth kind", err)
			}
			oldResult, err := maintenancescan.VerifyCompleteSourcesMaintenance(oldLease)
			if err != nil || oldResult == nil || result.Verify(permit) != nil {
				t.Fatal("old complete-source reader failed after the new kind", err)
			}
			oldReport, err := oldResult.Summary(permit)
			if err != nil || oldReport.Scope != "complete_business_source_graphs_v1" || oldReport.ScannedTables != 68 || oldReport.Ledger != report.Ledger || oldReport.Links != report.Links || oldReport.Funding != report.Funding || oldReport.Customer != report.Customer || oldReport.Procurement != report.Procurement || oldReport.Periodic != report.Periodic || oldReport.Sandbox != report.Sandbox {
				t.Fatal("source-wallet changed the old scope or shared seven raw algorithms", err)
			}
			if result.Verify(&maintenancetrust.Permit{}) == nil {
				t.Fatal("source-wallet result accepted an unrelated Permit")
			}
			copyResult := *result
			if snapshot.Rollback(ctx) != nil || result.Verify(permit) == nil || copyResult.Verify(permit) == nil || oldResult.Verify(permit) == nil {
				t.Fatal("source-wallet rollback failed to revoke all original/copy results")
			}
			if after, err := result.Summary(permit); err == nil || !reflect.DeepEqual(after, fundinglotscan.WalletReport{}) {
				t.Fatal("revoked source-wallet result returned a partial report")
			}
			if gate.Close(ctx) != nil {
				t.Fatal("source-wallet confirmed rollback cleanup failed")
			}
			f.awaitOwnedGone(t)
			assertSourceWalletC0Empty(t, f, ctx)
			assertCompleteSourceEdgeUnchanged(t, f, ctx, before)
			t.Logf("source-wallet %s: 84 tables, eight executed algorithms, missing epoch retained as mismatch; no C0 writes", map[bool]string{false: "plan", true: "init"}[initialize])
		})
	}
}

func TestMaintenanceSourceWalletPostgresResourceAborts(t *testing.T) {
	f := newConsumerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner := openPeriodicResourceOwner(t, f, ctx)
	_, err := owner.Exec(ctx, `INSERT INTO audit_events(id,actor,tenant_scope,action,target,sanitized_diff,request_id,created_at)
 VALUES('source_wallet_resource_probe',pg_catalog.repeat('x',65537),NULL,'qa.source_wallet_resource','','{}'::jsonb,'',$1)`, periodicResourceTime)
	if err != nil {
		t.Fatal("source-wallet generic over-limit audit fixture failed", err)
	}
	owner.Close()
	before := completeSourceEdgeSnapshot(t, f, ctx)
	gate, _, permit := sourceWalletSnapshot(t, f, ctx, false)
	lease, err := permit.BeginScan(ctx, maintenancetrust.ScanSourceWallet)
	if err != nil {
		t.Fatal("source-wallet resource original lease failed", err)
	}
	if result, err := maintenancescan.VerifySourceWalletMaintenance(lease); result != nil || !errors.Is(err, fundinglotscan.ErrResource) {
		t.Fatal("source-wallet resource failure was replaced or exposed a result", err)
	}
	if permit.Verify(ctx) == nil {
		t.Fatal("source-wallet failed scan left its Permit valid")
	}
	f.awaitOwnedGone(t)
	if !errors.Is(gate.Close(ctx), maintenancetrust.ErrCleanup) {
		t.Fatal("source-wallet failed scan lost its abandoned outcome")
	}
	assertSourceWalletC0Empty(t, f, ctx)
	assertCompleteSourceEdgeUnchanged(t, f, ctx, before)
}
