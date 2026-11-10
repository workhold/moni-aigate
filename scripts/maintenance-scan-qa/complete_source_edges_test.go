//go:build historical

package maintenancescanqa_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/maintenancescan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

type completeSourceEdgeState struct {
	funding, legacy string
	procurement     []byte
}

func completeSourceEdgeSnapshot(t *testing.T, f *consumerFixture, ctx context.Context) completeSourceEdgeState {
	t.Helper()
	var out completeSourceEdgeState
	var err error
	out.funding, err = f.env.FundingSnapshot(ctx)
	if err != nil || out.funding == "" {
		t.Fatal("complete-source edge funding snapshot failed")
	}
	out.legacy, err = f.env.LegacyFundingSnapshot(ctx)
	if err != nil || out.legacy == "" {
		t.Fatal("complete-source edge legacy funding snapshot failed")
	}
	out.procurement, err = f.procurementAttemptSnapshot(ctx)
	if err != nil || len(out.procurement) == 0 {
		t.Fatal("complete-source edge procurement snapshot failed")
	}
	f.awaitOwnedGone(t)
	return out
}

func assertCompleteSourceEdgeUnchanged(t *testing.T, f *consumerFixture, ctx context.Context, before completeSourceEdgeState) {
	t.Helper()
	after := completeSourceEdgeSnapshot(t, f, ctx)
	if after.funding != before.funding || after.legacy != before.legacy || !bytes.Equal(after.procurement, before.procurement) {
		t.Fatal("complete-source edge scan changed funded, legacy, procurement or setup audit facts")
	}
}

func completeSourceEdgePlan(t *testing.T, f *consumerFixture, ctx context.Context) (*maintenancetrust.Gate, *maintenancetrust.Snapshot, *maintenancetrust.Permit) {
	t.Helper()
	f.awaitOwnedGone(t)
	gate, err := maintenancetrust.Acquire(ctx, f.cfg, f.pins)
	if err != nil || gate == nil {
		t.Fatal("complete-source edge original Acquire failed")
	}
	f.gates = append(f.gates, gate)
	f.observeMaintenance(t)
	snapshot, err := gate.BeginPlan(ctx)
	if err != nil || snapshot == nil {
		t.Fatal("complete-source edge original Plan failed")
	}
	installation, err := snapshot.VerifyInstallation(ctx)
	if err != nil || installation == nil {
		t.Fatal("complete-source edge genuine installation proof failed")
	}
	permit, err := maintenancetrust.Attest(ctx, snapshot)
	if err != nil || permit == nil {
		t.Fatal("complete-source edge genuine predecessor attestation failed")
	}
	return gate, snapshot, permit
}

// The original DDL permits this generic audit. Its actor is one byte over the
// reader's scalar resource limit; no history, ACL, trigger or instance setting
// is changed. The first resource error must survive early Rows.Close/Abort.
func TestMaintenanceCompleteSourceEdgesPostgresResource(t *testing.T) {
	f := newConsumerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner := openPeriodicResourceOwner(t, f, ctx)
	_, err := owner.Exec(ctx, `INSERT INTO audit_events(id,actor,tenant_scope,action,target,sanitized_diff,request_id,created_at)
 VALUES('complete_source_resource_probe',pg_catalog.repeat('x',65537),NULL,'qa.complete_source_resource','','{}'::jsonb,'',$1)`, periodicResourceTime)
	if err != nil {
		t.Fatal("complete-source legal oversized generic audit insertion failed")
	}
	var actorBytes int
	if owner.QueryRow(ctx, `SELECT pg_catalog.octet_length(actor) FROM audit_events WHERE id='complete_source_resource_probe'`).Scan(&actorBytes) != nil || actorBytes != 65537 {
		t.Fatal("complete-source resource fixture missed the exact scalar boundary")
	}
	owner.Close()
	before := completeSourceEdgeSnapshot(t, f, ctx)
	gate, _, permit := completeSourceEdgePlan(t, f, ctx)
	lease, err := permit.BeginScan(ctx, maintenancetrust.ScanCompleteSources)
	if err != nil || lease == nil {
		t.Fatal("complete-source resource original lease failed")
	}
	result, scanErr := maintenancescan.VerifyCompleteSourcesMaintenance(lease)
	if result != nil || !errors.Is(scanErr, fundinglotscan.ErrResource) {
		t.Fatalf("complete-source over-limit scan lost resource cause or returned a result: error=%v", scanErr)
	}
	if permit.Verify(ctx) == nil {
		t.Fatal("complete-source resource Abort left the original Permit valid")
	}
	// Observe physical socket/backend and advisory-lock release before the
	// explicit Gate.Close, so cleanup cannot disguise a missing wrapper Abort.
	f.awaitOwnedGone(t)
	if !errors.Is(gate.Close(ctx), maintenancetrust.ErrCleanup) {
		t.Fatal("complete-source resource abandoned outcome was not preserved")
	}
	f.awaitOwnedGone(t)
	assertCompleteSourceEdgeUnchanged(t, f, ctx, before)
}

// Native infinity is legal in the original audit DDL. All physical rows must
// finish, PB must receive its native envelope, and time.Time-only domains must
// remain explicitly unexecuted with no synthesized scope or partial summary.
func TestMaintenanceCompleteSourceEdgesPostgresNativeInfinity(t *testing.T) {
	f := newConsumerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner := openPeriodicResourceOwner(t, f, ctx)
	_, err := owner.Exec(ctx, `INSERT INTO audit_events(id,actor,tenant_scope,action,target,sanitized_diff,request_id,created_at)
 VALUES('complete_source_native_probe','qa',NULL,'qa.complete_source_native','','{}'::jsonb,'','infinity'::timestamptz)`)
	if err != nil {
		t.Fatal("complete-source legal infinite generic audit insertion failed")
	}
	var native pgtype.Timestamptz
	if owner.QueryRow(ctx, `SELECT created_at FROM audit_events WHERE id='complete_source_native_probe'`).Scan(&native) != nil || !native.Valid || native.InfinityModifier != pgtype.Infinity {
		t.Fatal("complete-source original native infinity fixture changed")
	}
	owner.Close()
	before := completeSourceEdgeSnapshot(t, f, ctx)
	gate, snapshot, permit := completeSourceEdgePlan(t, f, ctx)
	lease, err := permit.BeginScan(ctx, maintenancetrust.ScanCompleteSources)
	if err != nil || lease == nil {
		t.Fatal("complete-source native original lease failed")
	}
	result, err := maintenancescan.VerifyCompleteSourcesMaintenance(lease)
	if err != nil || result == nil || result.Verify(permit) != nil {
		t.Fatal("complete-source native scan did not return an original bound result")
	}
	report, err := result.Summary(permit)
	if err != nil || report.Scope != "complete_business_source_graphs_v1" || report.Mode != fundinglotscan.ModeSandbox || !report.PhysicalEOFComplete || report.ScannedTables != 68 || report.SourceRows == 0 || report.InputMismatchFields != 1 || report.Status != "mismatch" || report.CoverageComplete {
		t.Fatal("complete-source native scan lost EOF or misrepresented its input qualification")
	}
	if report.Algorithms != [7]string{"executed", "executed", "input_mismatch", "executed", "input_mismatch", "executed", "input_mismatch"} {
		t.Fatal("complete-source native scan skipped independent cores or invoked an unrepresentable core")
	}
	if !reflect.ValueOf(report.Funding).IsZero() || !reflect.ValueOf(report.Procurement).IsZero() || !reflect.ValueOf(report.Sandbox).IsZero() {
		t.Fatal("complete-source native scan synthesized damaged-domain scope or partial summaries")
	}
	if report.Ledger.Scope != "supplied_ledger_rows" || report.Links.Scope != "manual_voucher_receipt_links" || report.Customer.Scope != "customer_key_financial_graph_v1" || report.Periodic.Scope != "periodic_budget_graph_v1" || report.Periodic.Mismatches.InvalidTimeRows != 1 {
		t.Fatal("complete-source native scan lost independent scopes or PB native validation")
	}
	// The wrapper has already consumed the sole normal Finish. Rejecting a
	// second call must not revoke the successfully published original proof.
	if proof, finishErr := lease.Finish(); proof != nil || finishErr == nil || result.Verify(permit) != nil {
		t.Fatal("complete-source native result did not retain unique Finish binding")
	}
	if result.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("complete-source native result accepted a different Permit")
	}
	copyResult := *result
	if snapshot.Rollback(ctx) != nil || permit.Verify(ctx) == nil || result.Verify(permit) == nil || copyResult.Verify(permit) == nil {
		t.Fatal("complete-source native rollback did not revoke original and copied results")
	}
	if after, summaryErr := result.Summary(permit); summaryErr == nil || !reflect.DeepEqual(after, fundinglotscan.Report{}) {
		t.Fatal("complete-source native revoked result returned a partial summary")
	}
	if gate.Close(ctx) != nil {
		t.Fatal("complete-source native confirmed rollback failed to close")
	}
	f.awaitOwnedGone(t)
	assertCompleteSourceEdgeUnchanged(t, f, ctx, before)
}

func TestMaintenanceCompleteSourceEdgesPostgresBusyOwner(t *testing.T) {
	f := newConsumerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	before := completeSourceEdgeSnapshot(t, f, ctx)
	gate, snapshot, permit := completeSourceEdgePlan(t, f, ctx)
	backend := f.backends[len(f.backends)-1]
	lease, err := permit.BeginScan(ctx, maintenancetrust.ScanCompleteSources)
	if err != nil || lease == nil {
		t.Fatal("complete-source busy original lease failed")
	}
	ownerDone := make(chan error, 1)
	ownerFinished := make(chan struct{})
	t.Cleanup(func() {
		// A failed assertion must still cancel and join the exact SQL owner
		// before the fixture attempts its bounded backend/role cleanup.
		_ = lease.Abort()
		select {
		case <-ownerFinished:
		case <-time.After(3 * time.Second):
			t.Error("complete-source busy SQL owner did not leave during cleanup")
		}
	})
	go func() {
		defer close(ownerFinished)
		rows, queryErr := lease.Query(`SELECT pg_catalog.pg_sleep(1)::pg_catalog.text,true`)
		if queryErr != nil {
			ownerDone <- queryErr
			return
		}
		more, nextErr := rows.Next()
		if nextErr != nil || !more {
			ownerDone <- maintenancetrust.ErrExecution
			return
		}
		var ignored string
		var marker bool
		if scanErr := rows.Scan(&ignored, &marker); scanErr != nil {
			ownerDone <- scanErr
			return
		}
		more, nextErr = rows.Next()
		if nextErr != nil || more || ignored != "" || !marker {
			ownerDone <- maintenancetrust.ErrExecution
			return
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			ownerDone <- rowsErr
			return
		}
		ownerDone <- rows.Close()
	}()
	// Observe only this original PID/backend_start. The PgSleep state is the
	// real driver-method barrier; no wall-clock guess or test hook owns it.
	observe, stopObserve := context.WithTimeout(ctx, 3*time.Second)
	defer stopObserve()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var sleeping bool
		err = f.admin.QueryRow(observe, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity
 WHERE pid=$1 AND backend_start=$2 AND datid=$3 AND state='active' AND wait_event='PgSleep')`, backend.pid, backend.start, f.pins.Database.OID).Scan(&sleeping)
		if err != nil {
			t.Fatal("complete-source busy original PgSleep observation failed")
		}
		if sleeping {
			break
		}
		select {
		case <-ownerDone:
			t.Fatal("complete-source busy owner finished before its driver barrier was observed")
		case <-observe.Done():
			t.Fatal("complete-source busy original PgSleep barrier was not reached")
		case <-ticker.C:
		}
	}
	result, err := maintenancescan.VerifyCompleteSourcesMaintenance(lease)
	if result != nil || !errors.Is(err, fundinglotscan.ErrExecution) {
		t.Fatal("complete-source wrapper did not reject the concurrent Require")
	}
	select {
	case ownerErr := <-ownerDone:
		if ownerErr != nil {
			t.Fatal("complete-source rejected wrapper disturbed the original query owner")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("complete-source original query did not complete after rejected Require")
	}
	proof, err := lease.Finish()
	if err != nil || proof == nil || proof.Verify(permit, maintenancetrust.ScanCompleteSources) != nil || permit.Verify(ctx) != nil {
		t.Fatal("complete-source rejected wrapper aborted or invalidated the original lease")
	}
	// This is the generic lease proof only; no source facts or CompleteSources
	// result were manufactured by the manual query used for the busy barrier.
	if snapshot.Rollback(ctx) != nil || proof.Verify(permit, maintenancetrust.ScanCompleteSources) == nil {
		t.Fatal("complete-source busy original proof survived rollback")
	}
	if gate.Close(ctx) != nil {
		t.Fatal("complete-source busy confirmed rollback failed to close")
	}
	f.awaitOwnedGone(t)
	assertCompleteSourceEdgeUnchanged(t, f, ctx, before)
}
