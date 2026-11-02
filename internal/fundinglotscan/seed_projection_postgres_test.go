package fundinglotscan_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotinit"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
	"github.com/moni-ai/moni-aigate/migrations"
)

// These tests use an untrusted owner RR only to read original source rows.
// No maintenance permit, C0 schema, epoch, proof, or funds writer is created.
type seedPGQuery struct {
	ctx   context.Context
	tx    pgx.Tx
	calls int
}

func (q *seedPGQuery) Query(sql string, args ...any) (fundinglotscan.Rows, error) {
	q.calls++
	rows, err := q.tx.Query(q.ctx, sql, append([]any{pgx.QueryExecModeSimpleProtocol}, args...)...)
	if err != nil {
		return nil, err
	}
	return seedPGRows{rows}, nil
}

type seedPGRows struct{ pgx.Rows }

func (r seedPGRows) Next() (bool, error) { return r.Rows.Next(), nil }
func (r seedPGRows) Close() error        { r.Rows.Close(); return nil }

func seedPGConfig(t *testing.T) (string, *pgxpool.Config) {
	t.Helper()
	raw := os.Getenv("MONI_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("MONI_TEST_DATABASE_URL required for dedicated legacy seed PostgreSQL tests")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" || cfg.ConnConfig.TLSConfig != nil || len(cfg.ConnConfig.Fallbacks) != 0 {
		t.Fatal("legacy seed PostgreSQL tests require the dedicated local moni_test database")
	}
	cfg.MaxConns, cfg.MinConns = 2, 0
	cfg.ConnConfig.RuntimeParams = map[string]string{"application_name": "moni-legacy-seed-test", "search_path": "pg_catalog,pg_temp", "timezone": "UTC"}
	return raw, cfg
}

func seedPGReadAndProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string, mode fundinglotscan.Mode) fundinglotscan.LegacySeedRows {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("begin legacy seed RR")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error("rollback legacy seed RR")
		}
	}()
	readCtx, cancel := context.WithCancel(ctx)
	q := &seedPGQuery{ctx: readCtx, tx: tx}
	facts, err := fundinglotscan.Read(readCtx, q, schema, mode)
	cancel()
	if err != nil {
		t.Fatal("read original real 68-table source union")
	}
	r := facts.Summary()
	if r.Status != "consistent" || !r.CoverageComplete || r.ScannedTables != 68 || !r.PhysicalEOFComplete {
		t.Fatalf("source graph did not qualify: mode=%d status=%s coverage=%t", mode, r.Status, r.CoverageComplete)
	}
	calls := q.calls
	out, err := fundinglotscan.ProjectLegacySeed(ctx, facts)
	if err != nil {
		t.Fatalf("project original real source graph: %v", err)
	}
	seedPGPlan(t, ctx, mode, out)
	if q.calls != calls || facts.Summary() != r {
		t.Fatal("projection or pure planning queried or changed real source facts")
	}
	return out
}

func seedPGPlan(t *testing.T, ctx context.Context, mode fundinglotscan.Mode, rows fundinglotscan.LegacySeedRows) {
	t.Helper()
	var planMode string
	switch mode {
	case fundinglotscan.ModeOrdinary:
		planMode = "ordinary"
	case fundinglotscan.ModeSandbox:
		planMode = "sandbox"
	default:
		t.Fatal("unknown legacy seed test mode")
	}
	input := fundinglotinit.Input{
		EpochID: "65377449-dc7c-5a94-a135-a6c8c182065b",
		EpochAt: fundinglotverify.WalletLotsTime{Valid: true, Value: time.Date(2030, 1, 1, 0, 0, 0, 123000, time.UTC)},
		Mode:    planMode,
		Rows:    rows,
	}
	plan, err := fundinglotinit.Build(ctx, input)
	if err != nil {
		t.Fatalf("pure planning of original projected source rows failed: %v", err)
	}
	var approved, unfinished, sandbox uint64
	for _, refund := range rows.Refunds {
		if refund.State == "approved" {
			approved++
		}
	}
	for _, reservation := range rows.Reservations {
		if reservation.Status == "reserved" || reservation.Status == "pending_reconciliation" {
			unfinished++
		}
	}
	for _, receipt := range rows.Receipts {
		if receipt.SourceKind == "sandbox_capture" {
			sandbox++
		}
	}
	m := plan.Metadata
	if m.EpochID != input.EpochID || m.EpochAt != input.EpochAt ||
		m.TenantCount != uint64(len(rows.Tenants)) || m.ReceiptCount != uint64(len(rows.Receipts)) ||
		m.SettlementCount != uint64(len(rows.Settlements)) || m.ApprovedRefundCount != approved ||
		m.OpeningCount != uint64(len(rows.Tenants))+unfinished ||
		m.LegacySourceCount != uint64(len(rows.Receipts)+len(rows.Reservations)+len(rows.Settlements))+approved ||
		len(plan.LegacyReceipts) != len(rows.Receipts) || uint64(len(plan.LegacySandboxReceipts)) != sandbox ||
		len(plan.LegacySettlements) != len(rows.Settlements) || uint64(len(plan.LegacyApprovedRefunds)) != approved ||
		len(plan.Operations) != len(rows.Tenants) || len(plan.Heads) != len(rows.Tenants) ||
		len(plan.SettlementLots) != 0 || len(plan.RefundLots) != 0 {
		t.Fatal("pure plan metadata or source memberships differ from original source counts")
	}
}

func TestPostgresLegacySeedProjection(t *testing.T) {
	raw, cfg := seedPGConfig(t)
	t.Run("ordinary_original_001_022", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
		if err != nil {
			t.Fatal("connect dedicated legacy seed database")
		}
		schema := "flseed_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		var owner *pgxpool.Pool
		t.Cleanup(func() {
			if owner != nil {
				owner.Close()
			}
			cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			defer admin.Close()
			if _, err := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
				t.Error("drop precise legacy seed schema")
				return
			}
			var exists bool
			if err := admin.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1)`, schema).Scan(&exists); err != nil || exists {
				t.Error("legacy seed schema cleanup unconfirmed")
			}
		})
		var exists bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1)`, schema).Scan(&exists); err != nil || exists {
			t.Fatal("legacy seed schema collision check")
		}
		if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
			t.Fatal("create isolated legacy seed schema")
		}
		ownerCfg := cfg.Copy()
		ownerCfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{schema}.Sanitize() + ",pg_catalog,pg_temp"
		owner, err = pgxpool.NewWithConfig(ctx, ownerCfg)
		if err != nil {
			t.Fatal("connect isolated legacy seed schema")
		}
		if err := migrations.ApplyThrough(ctx, owner, "022_payment_runtime_attestation.sql"); err != nil {
			t.Fatal("install original 001-022 migrations")
		}
		// A non-UUID old tenant and user exercise the raw TEXT boundary without
		// inventing a birth stamp or any financial history.
		if _, err := owner.Exec(ctx, `INSERT INTO tenants(id,name) VALUES('旧/tenant','Seed projection test'); INSERT INTO users(id,email,password_hash) VALUES('旧/user','legacy-seed@example.invalid','unused-fixture-only')`); err != nil {
			t.Fatal("seed isolated original identities")
		}
		out := seedPGReadAndProject(t, ctx, owner, schema, fundinglotscan.ModeOrdinary)
		if len(out.Tenants) != 1 || out.Tenants[0].ID != "旧/tenant" || len(out.Receipts) != 0 {
			t.Fatal("ordinary source projection changed old identities or invented funding")
		}
	})
	t.Run("genuine_sandbox_predecessor_with_funded_history", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		env, err := paymentsandboxqa.CreateApplication(ctx, raw, t.TempDir())
		if err != nil {
			t.Fatal("create genuine isolated sandbox source fixture")
		}
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
			defer stop()
			if err := env.Close(cleanup); err != nil {
				t.Error("cleanup precise sandbox source fixture")
			}
		})
		owner, err := env.OpenPool(ctx, "app_owner")
		if err != nil {
			t.Fatal("open sandbox original owner reader")
		}
		defer owner.Close()
		out := seedPGReadAndProject(t, ctx, owner, env.Schemas["app"], fundinglotscan.ModeSandbox)
		if len(out.Tenants) == 0 || len(out.Receipts) == 0 || len(out.Accounts) == 0 || len(out.Journals) == 0 || len(out.Lines) == 0 {
			t.Fatal("genuine sandbox nonempty business history was not projected")
		}
		for _, receipt := range out.Receipts {
			if receipt.ID == receipt.SourceID || receipt.Amount == "" {
				t.Fatal("projection confused receipt and original source identities")
			}
		}
		if err := env.VerifyFundsUnchanged(ctx); err != nil {
			t.Fatal("source projection changed original sandbox funds")
		}
	})
}
