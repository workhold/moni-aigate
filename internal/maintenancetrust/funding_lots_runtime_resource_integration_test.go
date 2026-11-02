package maintenancetrust

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotruntime"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

func TestFundingLotsRuntimePostgresActualTransportBounds(t *testing.T) {
	f := newSnapshotFixture(t)
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	fundingLotsInitOrdinary(t, f, plan.Expected)
	owner := f.connect(t)
	expected := runtimeFixtureExpected(t, owner, f.schema, plan.Expected)
	runtimeFixtureGrant(t, owner, f.pins)
	relation := pgx.Identifier{f.schema, "audit_events"}.Sanitize()
	for _, tc := range []struct {
		name        string
		rows, field int
	}{{"single_field", 1, 65537}, {"shared_16MiB", 350, 50000}} {
		t.Run(tc.name, func(t *testing.T) {
			// Unknown audit actions are ordinary raw data, not any financial authority.
			// The fixed scanner must reject transport limits before semantic projection.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, e := owner.Exec(ctx, "INSERT INTO "+relation+"(actor,action,target,sanitized_diff) SELECT 'resource-fixture','runtime.resource.test',g::text,to_jsonb(repeat('x',$1::integer)) FROM generate_series(1,$2::integer) g", tc.field, tc.rows); e != nil {
				t.Fatal("bounded transport fixture insertion")
			}
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
				defer stop()
				tx, e := f.admin.Begin(cleanup)
				if e != nil {
					t.Fatal("transport fixture cleanup transaction")
				}
				defer tx.Rollback(cleanup)
				if _, e = tx.Exec(cleanup, "SET LOCAL session_replication_role=replica"); e != nil {
					t.Fatal("transport fixture immutable cleanup")
				}
				if _, e = tx.Exec(cleanup, "DELETE FROM "+relation+" WHERE action='runtime.resource.test'"); e != nil || tx.Commit(cleanup) != nil {
					t.Fatal("transport fixture removal")
				}
			}()
			c, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			facts, e := fundinglotscan.ReadWallet(ctx, fundingLotsObserver{ctx, r.Tx}, f.schema, fundinglotscan.ModeOrdinary)
			if e != fundinglotscan.ErrResource || facts.Summary().ScannedTables != 0 {
				t.Fatalf("actual transport bound was not enforced by original scanner: %v", e)
			}
			runtimeFixtureCleanup(t, c, r.Tx)
			c, r = runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			if proof, e := fundinglotruntime.VerifyOrdinaryApplicationTx(context.Background(), r, f.pins, expected); proof != nil || e != fundinglotruntime.ErrRuntime {
				t.Fatal("actual source transport overflow issued proof")
			}
			reached := false
			for _, sql := range r.sql {
				if strings.HasPrefix(sql, "WITH page AS MATERIALIZED") && strings.Contains(sql, " FROM ONLY "+relation+" AS s") {
					reached = true
				}
			}
			if !reached {
				t.Fatal("resource rejection must exercise actual scanner query")
			}
			runtimeAssertReadOnly(t, r, false)
			runtimeFixtureCleanup(t, c, r.Tx)
		})
	}
	_, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	runtimeIssueOrdinary(t, r, f.pins, expected)
}
