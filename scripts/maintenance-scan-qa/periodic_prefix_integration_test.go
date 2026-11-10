//go:build historical

package maintenancescanqa_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/maintenancescan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
	"github.com/moni-ai/moni-aigate/migrations"
)

// This external consumer extends the existing genuine funded sandbox with the
// frozen candidate only. It is not a production installer or an epoch fixture.
// Successful wrappers prove their complete reads even when business reports
// retain the known historical procurement gap and the missing wallet epoch.
func newPeriodicPrefixConsumerFixture(t *testing.T) *consumerFixture {
	t.Helper()
	f := newConsumerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	owner := openPeriodicResourceOwner(t, f, ctx)
	tx, err := owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal("prefix consumer candidate transaction failed")
	}
	defer tx.Rollback(context.Background())
	if migrations.VerifyOriginal22SnapshotTx(ctx, tx, f.env.Schemas["app"]) != nil {
		t.Fatal("prefix consumer exact original22 history failed")
	}
	ddl, err := periodicbudgetprefixschema.SQL()
	if err != nil {
		t.Fatal("prefix consumer frozen candidate unavailable")
	}
	acl, err := periodicbudgetprefixschema.Permissions(f.env.Roles["app_runtime"], f.env.Roles["app_projector"])
	if err != nil {
		t.Fatal("prefix consumer frozen permissions unavailable")
	}
	if _, err = tx.Exec(ctx, ddl); err != nil {
		t.Fatal("prefix consumer candidate installation failed")
	}
	if _, err = tx.Exec(ctx, acl); err != nil {
		t.Fatal("prefix consumer candidate permissions failed")
	}
	// V3 inherits the independently reviewed WalletReadV2 four-column APP
	// content delta; install its exact original without broadening the grant.
	walletRaw := consumerFixedSource(t, "specs/022-self-service-payments/c0-runtime-read-permissions.sql", "97abd87df7e5d0e7a03adae4158470e76ab94d58c9e9d8b92ae792aeb84867c2")
	walletACL := strings.NewReplacer("{{schema}}", pgx.Identifier{f.env.Schemas["app"]}.Sanitize(), "{{app}}", pgx.Identifier{f.env.Roles["app_runtime"]}.Sanitize()).Replace(string(walletRaw))
	if strings.Contains(walletACL, "{{") {
		t.Fatal("prefix consumer unresolved fixed wallet-read identifier")
	}
	if _, err = tx.Exec(ctx, walletACL); err != nil || tx.Commit(ctx) != nil {
		t.Fatal("prefix consumer exact wallet-read permissions/commit failed")
	}
	owner.Close()

	// Use the fixture's restricted APP LOGIN credential only in this local
	// constructor; no DSN, password, query content or provider data is logged.
	dsn, err := f.env.RuntimeDSN("app")
	if err != nil {
		t.Fatal("prefix consumer restricted APP configuration unavailable")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("prefix consumer restricted APP configuration rejected")
	}
	cfg.ConnConfig.RuntimeParams = map[string]string{"application_name": "prefix_consumer_business", "search_path": pgx.Identifier{f.env.Schemas["app"]}.Sanitize() + ",pg_catalog,pg_temp"}
	cfg.MinConns, cfg.MaxConns = 0, 1
	app, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("prefix consumer restricted APP pool failed")
	}
	f.pools = append(f.pools, app)
	var current, session string
	if app.QueryRow(ctx, "SELECT current_user::text,session_user::text").Scan(&current, &session) != nil || current != f.env.Roles["app_runtime"] || session != current {
		t.Fatal("prefix consumer business did not use actual restricted APP LOGIN")
	}
	commander, err := periodicbudget.NewCommander(app)
	if err != nil {
		t.Fatal("prefix consumer actual APP commander failed")
	}
	for _, command := range []struct{ revision, limit string }{{"0", "1000000"}, {"1", "1000001"}, {"2", "1000001"}} {
		_, err = commander.ConfigurePlatformTenant(ctx, f.env.ActorIDs["admin"], f.env.ApplicationTenantID, periodicbudget.PutPeriodicBudget{
			OperationID: uuid.NewString(), ExpectedRevision: command.revision, PeriodKind: "utc_month", Enabled: true, LimitAmount: command.limit,
		})
		if err != nil {
			t.Fatal("prefix consumer actual APP configuration/no-op failed", err)
		}
	}
	app.Close()
	f.awaitOwnedGone(t)
	state := prefixConsumerObservationState(t, f, ctx)
	if state.origins == 0 || state.heads == 0 || state.observations == 0 {
		t.Fatal("prefix consumer requires real persisted origin/head/observation facts")
	}
	return f
}

type prefixConsumerState struct {
	canonical                    string
	origins, heads, observations int64
}

func prefixConsumerObservationState(t *testing.T, f *consumerFixture, ctx context.Context) prefixConsumerState {
	t.Helper()
	var state prefixConsumerState
	var parts []string
	for _, table := range []string{
		"periodic_budget_allocations", "periodic_budget_configuration_versions", "periodic_budget_controls", "periodic_budget_events",
		"periodic_budget_observation_heads", "periodic_budget_observation_journal", "periodic_budget_observation_origins", "periodic_budget_operations", "periodic_budget_windows",
	} {
		var content string
		if f.admin.QueryRow(ctx, "SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]'::jsonb)::text FROM ONLY "+pgx.Identifier{f.env.Schemas["app"], table}.Sanitize()+" AS x").Scan(&content) != nil {
			t.Fatal("prefix consumer independent content baseline failed", table)
		}
		parts = append(parts, table+"/"+content)
	}
	state.canonical = strings.Join(parts, "\n")
	for table, count := range map[string]*int64{
		"periodic_budget_observation_origins": &state.origins, "periodic_budget_observation_heads": &state.heads, "periodic_budget_observation_journal": &state.observations,
	} {
		if f.admin.QueryRow(ctx, "SELECT count(*) FROM ONLY "+pgx.Identifier{f.env.Schemas["app"], table}.Sanitize()).Scan(count) != nil {
			t.Fatal("prefix consumer independent witness count failed")
		}
	}
	return state
}

func periodicPrefixConsumerSnapshot(t *testing.T, f *consumerFixture, ctx context.Context, initialize bool) (*maintenancetrust.Gate, *maintenancetrust.Snapshot, *maintenancetrust.Permit) {
	t.Helper()
	f.awaitOwnedGone(t)
	gate, err := maintenancetrust.Acquire(ctx, f.cfg, f.pins)
	if err != nil || gate == nil {
		t.Fatal("prefix consumer original Acquire failed", err)
	}
	f.gates = append(f.gates, gate)
	f.observeMaintenance(t)
	var snapshot *maintenancetrust.Snapshot
	if initialize {
		snapshot, err = gate.BeginPeriodicPrefixInit(ctx)
	} else {
		snapshot, err = gate.BeginPeriodicPrefixPlan(ctx)
	}
	if err != nil || snapshot == nil {
		t.Fatal("prefix consumer original V3 snapshot failed", err)
	}
	installation, err := snapshot.VerifyPeriodicPrefixInstallation(ctx)
	if err != nil || installation == nil || installation.Catalog().Profile() != fundinglotcatalog.PeriodicPrefixV3 {
		t.Fatal("prefix consumer actual owned V3 installation failed", err)
	}
	*installation = fundinglotcatalog.InstallationProof{}
	permit, err := maintenancetrust.Attest(ctx, snapshot)
	if err != nil || permit == nil {
		t.Fatal("prefix consumer original sandbox maintenance authority failed", err)
	}
	return gate, snapshot, permit
}

func TestMaintenancePeriodicPrefixPostgresPreEpochPlanInit(t *testing.T) {
	for _, initialize := range []bool{false, true} {
		t.Run(map[bool]string{false: "plan", true: "init"}[initialize], func(t *testing.T) {
			f := newPeriodicPrefixConsumerFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			before := completeSourceEdgeSnapshot(t, f, ctx)
			prefixBefore := prefixConsumerObservationState(t, f, ctx)
			assertSourceWalletC0Empty(t, f, ctx)
			gate, snapshot, permit := periodicPrefixConsumerSnapshot(t, f, ctx, initialize)
			sourceLease, err := permit.BeginScan(ctx, maintenancetrust.ScanCompleteSources)
			if err != nil {
				t.Fatal("prefix consumer original kind6 lease failed", err)
			}
			sources, err := maintenancescan.VerifyPrefixSourcesMaintenance(sourceLease)
			if err != nil || sources == nil || sources.Verify(permit) != nil {
				t.Fatal("prefix consumer complete source wrapper failed", err)
			}
			source, err := sources.Summary(permit)
			if err != nil || source.Scope != "complete_business_source_prefix_graphs_v1" || source.Mode != fundinglotscan.ModeSandbox || !source.PhysicalEOFComplete || source.ScannedTables != 71 || source.ScannedColumns != 635 || source.SourceRows == 0 || source.InputMismatchFields != 0 || source.ObservationSourceMismatches != 0 || source.PeriodicPrefix.Base != source.Periodic || source.PeriodicPrefix.Scanned.Origins != uint64(prefixBefore.origins) || source.PeriodicPrefix.Scanned.Observations != uint64(prefixBefore.observations) {
				t.Fatal("prefix source wrapper lost exact scope or actual witnesses", err)
			}
			if source.Algorithms != [8]string{"executed", "executed", "executed", "executed", "executed", "executed", "executed", "executed"} || source.Procurement.Unproven.ProposalBarrierUnproven != 1 || source.CoverageComplete || source.Status != "indeterminate" {
				t.Fatal("prefix source wrapper erased the genuine historical procurement gap")
			}
			if proof, err := sourceLease.Finish(); proof != nil || err == nil || sources.Verify(permit) != nil {
				t.Fatal("prefix source wrapper lost sole Finish binding")
			}
			if old, err := maintenancescan.VerifyCompleteSourcesMaintenance(sourceLease); old != nil || err == nil || sources.Verify(permit) != nil {
				t.Fatal("legacy source wrapper reused the completed sixth-kind lease")
			}
			if lease, err := permit.BeginScan(ctx, maintenancetrust.ScanCompleteSources); lease != nil || err == nil || sources.Verify(permit) != nil {
				t.Fatal("prefix source wrapper acquired another sixth-kind opportunity")
			}

			walletLease, err := permit.BeginScan(ctx, maintenancetrust.ScanSourceWallet)
			if err != nil {
				t.Fatal("prefix source kind6 consumed independent kind7", err)
			}
			wallet, err := maintenancescan.VerifyPrefixWalletMaintenance(walletLease)
			if err != nil || wallet == nil || wallet.Verify(permit) != nil || sources.Verify(permit) != nil {
				t.Fatal("prefix consumer complete wallet wrapper failed", err)
			}
			post, err := wallet.Summary(permit)
			if err != nil || post.Scope != "complete_source_wallet_prefix_graphs_v1" || post.Mode != fundinglotscan.ModeSandbox || !post.PhysicalEOFComplete || post.ScannedTables != 87 || post.ScannedColumns != 804 || post.SourceRows == 0 || post.InputMismatchFields != 0 || post.ObservationSourceMismatches != 0 || post.PeriodicPrefix.Base != post.Periodic || post.PeriodicPrefix != source.PeriodicPrefix || post.Periodic != source.Periodic || post.Procurement != source.Procurement {
				t.Fatal("prefix wallet wrapper changed shared original facts or scope", err)
			}
			if post.Algorithms != [9]string{"executed", "executed", "executed", "executed", "executed", "executed", "executed", "executed", "executed"} || post.Status != "mismatch" || post.Wallet.Scanned.Epochs != 0 || post.Wallet.Scanned.Attestations != 0 || post.Wallet.RowsConsistent {
				t.Fatal("prefix wallet wrapper invented a wallet epoch or skipped a core")
			}
			if proof, err := walletLease.Finish(); proof != nil || err == nil || wallet.Verify(permit) != nil {
				t.Fatal("prefix wallet wrapper lost sole Finish binding")
			}
			if old, err := maintenancescan.VerifySourceWalletMaintenance(walletLease); old != nil || err == nil || wallet.Verify(permit) != nil {
				t.Fatal("legacy wallet wrapper reused the completed seventh-kind lease")
			}
			if lease, err := permit.BeginScan(ctx, maintenancetrust.ScanSourceWallet); lease != nil || err == nil || wallet.Verify(permit) != nil || sources.Verify(permit) != nil {
				t.Fatal("prefix wallet wrapper acquired another seventh-kind opportunity")
			}
			if sources.Verify(&maintenancetrust.Permit{}) == nil || wallet.Verify(&maintenancetrust.Permit{}) == nil {
				t.Fatal("prefix result accepted an unrelated Permit")
			}
			sourceCopy, walletCopy := *sources, *wallet
			if snapshot.Rollback(ctx) != nil || sources.Verify(permit) == nil || sourceCopy.Verify(permit) == nil || wallet.Verify(permit) == nil || walletCopy.Verify(permit) == nil {
				t.Fatal("prefix rollback did not revoke both original/copy results")
			}
			if summary, err := sources.Summary(permit); err == nil || !reflect.DeepEqual(summary, fundinglotscan.PrefixReport{}) {
				t.Fatal("revoked prefix source returned a partial report")
			}
			if summary, err := wallet.Summary(permit); err == nil || !reflect.DeepEqual(summary, fundinglotscan.PrefixWalletReport{}) {
				t.Fatal("revoked prefix wallet returned a partial report")
			}
			if gate.Close(ctx) != nil {
				t.Fatal("prefix consumer confirmed rollback cleanup failed")
			}
			f.awaitOwnedGone(t)
			assertSourceWalletC0Empty(t, f, ctx)
			assertCompleteSourceEdgeUnchanged(t, f, ctx, before)
			if after := prefixConsumerObservationState(t, f, ctx); after != prefixBefore {
				t.Fatal("prefix wrappers changed actual business or observation originals")
			}
			t.Log("actual V3 source71/635 and wallet87/804 wrappers retained nonempty observations, unique kind6/7 completion and rollback revocation; pre-epoch business gaps remain descriptive")
		})
	}
}

func TestMaintenancePeriodicPrefixPostgresProfileRejectionSharesOnce(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		for _, kind := range []maintenancetrust.ScanKind{maintenancetrust.ScanCompleteSources, maintenancetrust.ScanSourceWallet} {
			name := map[bool]string{false: "prefix_on_legacy", true: "legacy_on_prefix"}[prefix] + "/" + map[maintenancetrust.ScanKind]string{maintenancetrust.ScanCompleteSources: "source_kind6", maintenancetrust.ScanSourceWallet: "wallet_kind7"}[kind]
			t.Run(name, func(t *testing.T) {
				var f *consumerFixture
				if prefix {
					f = newPeriodicPrefixConsumerFixture(t)
				} else {
					f = newConsumerFixture(t)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				before := completeSourceEdgeSnapshot(t, f, ctx)
				var gate *maintenancetrust.Gate
				var permit *maintenancetrust.Permit
				if prefix {
					gate, _, permit = periodicPrefixConsumerSnapshot(t, f, ctx, false)
				} else {
					gate, _, permit = sourceWalletSnapshot(t, f, ctx, false)
				}
				lease, err := permit.BeginScan(ctx, kind)
				if err != nil || lease == nil {
					t.Fatal("cross-profile wrapper original kind6/7 lease failed", err)
				}
				var scanErr error
				switch {
				case prefix && kind == maintenancetrust.ScanCompleteSources:
					result, err := maintenancescan.VerifyCompleteSourcesMaintenance(lease)
					scanErr = err
					if result != nil {
						t.Fatal("legacy source wrapper accepted actual V3 authority")
					}
				case prefix:
					result, err := maintenancescan.VerifySourceWalletMaintenance(lease)
					scanErr = err
					if result != nil {
						t.Fatal("legacy wallet wrapper accepted actual V3 authority")
					}
				case kind == maintenancetrust.ScanCompleteSources:
					result, err := maintenancescan.VerifyPrefixSourcesMaintenance(lease)
					scanErr = err
					if result != nil {
						t.Fatal("prefix source wrapper upgraded legacy authority")
					}
				default:
					result, err := maintenancescan.VerifyPrefixWalletMaintenance(lease)
					scanErr = err
					if result != nil {
						t.Fatal("prefix wallet wrapper upgraded legacy authority")
					}
				}
				if !errors.Is(scanErr, fundinglotscan.ErrExecution) || permit.Verify(ctx) == nil {
					t.Fatal("cross-profile wrapper issued result or retained the failed Permit", scanErr)
				}
				if next, err := permit.BeginScan(ctx, kind); next != nil || err == nil {
					t.Fatal("switching wrappers recovered original kind6/7 once opportunity")
				}
				if kind == maintenancetrust.ScanCompleteSources {
					if result, err := maintenancescan.VerifyPrefixSourcesMaintenance(lease); result != nil || err == nil {
						t.Fatal("failed sixth-kind lease retried through prefix wrapper")
					}
					if result, err := maintenancescan.VerifyCompleteSourcesMaintenance(lease); result != nil || err == nil {
						t.Fatal("failed sixth-kind lease retried through legacy wrapper")
					}
				} else {
					if result, err := maintenancescan.VerifyPrefixWalletMaintenance(lease); result != nil || err == nil {
						t.Fatal("failed seventh-kind lease retried through prefix wrapper")
					}
					if result, err := maintenancescan.VerifySourceWalletMaintenance(lease); result != nil || err == nil {
						t.Fatal("failed seventh-kind lease retried through legacy wrapper")
					}
				}
				f.awaitOwnedGone(t)
				if !errors.Is(gate.Close(ctx), maintenancetrust.ErrCleanup) {
					t.Fatal("cross-profile wrapper failed to preserve abandoned cleanup")
				}
				assertSourceWalletC0Empty(t, f, ctx)
				assertCompleteSourceEdgeUnchanged(t, f, ctx, before)
			})
		}
	}
}
