package procurementattemptbirth_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
	"github.com/moni-ai/moni-aigate/internal/testsupport/frozenattempt"
)

type abObserverCaptured struct {
	runner *procurementexecution.Runner
	pool   *pgxpool.Pool
	trace  *abObserverTrace
	wire   *abExecutorWire
	audit  *abExecutorDialAudit
	oldPID uint32
	handle *procurementexecution.ObservationHandle
	cause  error
	failed error
}

// A valid opaque handle always comes from the real Runner's complete decoder
// and uncertain COMMIT. No test writes private handle fields or manufactures
// a root/source ID. The original call returns the complete zero receipt.
func abObserverCapture(t *testing.T, f *fixture, call procurementexecution.Invocation, beforeSend bool) abObserverCaptured {
	t.Helper()
	pool, wire, pid, audit, trace := abObserverUnknownPool(t, f)
	runner := abExecutorRunner(t, pool)
	ready := wire.gate()
	must(t, "arm exact real observer unknown socket", wire.arm())
	before := f.snapshot(t)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	type result struct {
		account procurementexecution.AccountReceipt
		attempt procurementexecution.AttemptReceipt
		err     error
	}
	done := make(chan result, 1)
	go func() {
		var value result
		if call.Root() == procurementexecution.RecordAttempt {
			value.attempt, value.err = runner.RunAttempt(ctx, call)
		} else {
			value.account, value.err = runner.RunAccount(ctx, call)
		}
		done <- value
	}()
	taken := false
	defer func() {
		wire.releaseCommit()
		cancel()
		if !taken {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("observer capture did not join the original Runner goroutine")
			}
		}
	}()
	select {
	case <-ready:
	case early := <-done:
		taken = true
		t.Fatalf("real observer capture did not reach decoded COMMIT boundary: %v", early.err)
	case <-ctx.Done():
		t.Fatal("real observer capture exhausted original bounded context")
	}
	var private bool
	must(t, "prove original backend still private before any COMMIT byte", f.owner.QueryRow(f.ctx, "SELECT state='idle in transaction' AND wait_event_type='Client' FROM pg_catalog.pg_stat_activity WHERE pid=$1", pid).Scan(&private))
	if !private || f.snapshot(t) != before || wire.observed() != (abExecutorWireState{queries: 1}) {
		t.Fatal("COMMIT gate did not preserve exact original precommit state")
	}
	if beforeSend {
		// Write has not forwarded any frontend COMMIT bytes. Disconnecting
		// here forces actual server rollback; decoded IDs stay only in handle.
		must(t, "close real socket before forwarding any COMMIT bytes", wire.Close())
	}
	wire.releaseCommit()
	var got result
	select {
	case got = <-done:
		taken = true
	case <-ctx.Done():
		t.Fatal("actual unknown did not return before original context deadline")
	}
	if ctx.Err() != nil || !reflect.DeepEqual(got.account, procurementexecution.AccountReceipt{}) || !reflect.DeepEqual(got.attempt, procurementexecution.AttemptReceipt{}) {
		t.Fatal("unknown capture published a private receipt or depended on context expiry")
	}
	unknown := abExecutorUnknownError(t, got.err)
	select {
	case <-audit.cleanup:
	case <-time.After(2 * time.Second):
		t.Fatal("original unknown driver cleanup did not finish")
	}
	abExecutorWaitBackendGone(t, f, pid)
	state, transports := wire.observed(), audit.snapshot()
	if state.problem != "" || state.queries != 1 || transports.problem != "" || transports.startups != 1 || transports.cancels > 1 || transports.dials != 1+transports.cancels {
		t.Fatalf("capture retried a business connection or failed exact cancellation authentication: wire=%+v transports=%+v", state, transports)
	}
	if beforeSend {
		if state.replies != 0 || state.drops != 0 || f.snapshot(t) != before {
			t.Fatal("pre-send negative COMMIT leaked to server or retained partial facts")
		}
	} else if state.replies != 1 || state.drops != 1 || transports.cancels != 1 {
		t.Fatal("committed observer control did not withhold one complete actual ACK")
	}
	return abObserverCaptured{runner: runner, pool: pool, trace: trace, wire: wire, audit: audit, oldPID: pid, handle: unknown.ObservationHandle(), cause: unknown.Unwrap(), failed: got.err}
}

func abObserverCheck(t *testing.T, f *fixture, capture abObserverCaptured, reason string, pgstate string) {
	t.Helper()
	before, wireBefore := f.snapshot(t), capture.wire.observed()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	started := time.Now()
	capture.trace.start()
	got, err := capture.runner.Observe(ctx, capture.handle)
	trace := capture.trace.finish()
	abObserverTraceCheck(t, trace, capture.oldPID, started, deadline, reason == "")
	if f.snapshot(t) != before || capture.wire.observed() != wireBefore {
		t.Fatal("Observe mutated any of 34 tables or replayed old wire operation")
	}
	if fields := reflect.TypeOf(got); fields.NumField() != 2 || fields.Field(0).Name != "State" || fields.Field(1).Name != "Reason" {
		t.Fatal("Observation exposes mutation data, identity or authorization")
	}
	if reason == "" {
		if string(got.State) != "receipt_present" || string(got.Reason) != "" || err != nil {
			t.Fatalf("exact committed receipt not observed: state=%s reason=%s error=%v", got.State, got.Reason, err)
		}
	} else {
		var safe interface{ PublicCode() string }
		if string(got.State) != "unresolved" || string(got.Reason) != reason || !errors.As(err, &safe) || safe.PublicCode() != "procurement_observation_unresolved" || err.Error() != "procurement_observation_unresolved" || !errors.Is(err, capture.cause) {
			t.Fatalf("unresolved observer lost exact reason/original COMMIT cause: state=%s reason=%s error=%v", got.State, got.Reason, err)
		}
		if pgstate != "" {
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != pgstate {
				t.Fatal("observation failure lost original probe PostgreSQL cause")
			}
		}
	}
	// Original unknown state is unchanged even after explicit successful read.
	var unknown *procurementexecution.UnknownOutcome
	if !errors.As(capture.failed, &unknown) || unknown.PublicCode() != "procurement_outcome_unknown" || unknown.ObservationHandle() != capture.handle {
		t.Fatal("observation mutated original unknown into a business result")
	}
	if capture.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("observer leaked its actual APP pool acquisition")
	}
	var healthy bool
	must(t, "prove read-only rollback released healthy APP pool", capture.pool.QueryRow(f.ctx, "SELECT current_user=session_user AND current_user=$1 AND current_setting('transaction_read_only')='off'", f.b.App).Scan(&healthy))
	if !healthy {
		t.Fatal("observer left pool with wrong identity or read-only transaction")
	}
}

func abObserverCreateCall(t *testing.T, f *fixture, in procurementexecution.AccountInput) procurementexecution.Invocation {
	t.Helper()
	call, err := procurementexecution.NewCreateAccount(f.b.Schema, in)
	must(t, "construct original create observer call", err)
	return call
}

func abObserverConfigureCall(t *testing.T, f *fixture, target string, revision int64, in procurementexecution.AccountInput) procurementexecution.Invocation {
	t.Helper()
	call, err := procurementexecution.NewConfigureAccount(f.b.Schema, procurementexecution.ConfigurationInput{AccountInput: in, AccountID: target, ExpectedRevision: revision})
	must(t, "construct original configure observer call", err)
	return call
}

func TestPostgresObserverAccountCommittedUnknownBranchesAndReplay(t *testing.T) {
	f := newFixture(t)
	input := abExecutorAccountInput(f)
	call := abObserverCreateCall(t, f, input)
	created := abObserverCapture(t, f, call, false)
	abObserverCheck(t, f, created, "", "")
	var account string
	must(t, "read independently committed create target", f.app.QueryRow(f.ctx, "SELECT target_id FROM procurement_commands WHERE operation_id=$1", input.OperationID).Scan(&account))
	t.Run("create_replay", func(t *testing.T) {
		replay := abObserverCapture(t, f, call, false)
		abObserverCheck(t, f, replay, "", "")
	})
	for _, tc := range []struct {
		name, target string
		revision     int64
		limit        int64
	}{
		{"changed", account, 1, 6000},
		{"noop", account, 2, 6000},
		{"revision_conflict", account, 1, 7000},
		{"not_found", uuid.NewString(), 1, 7000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := abExecutorAccountInput(f)
			in.LimitAmount = tc.limit
			configure := abObserverConfigureCall(t, f, tc.target, tc.revision, in)
			captured := abObserverCapture(t, f, configure, false)
			abObserverCheck(t, f, captured, "", "")
			replayed := abObserverCapture(t, f, configure, false)
			abObserverCheck(t, f, replayed, "", "")
		})
	}
	// The captured create remains an immutable old receipt after genuine later
	// configuration and managed procurement holds; never read today's account
	// into its old Command.After or construct a replacement observation handle.
	f.bind(t, account)
	r := f.reserve(t)
	_, err := f.attempt(r.RequestID)
	must(t, "commit genuine supported later managed reserve", err)
	t.Run("old_create_after_configure_and_reserve", func(t *testing.T) {
		abObserverCheck(t, f, created, "", "")
	})
}

func TestPostgresObserverAttemptCommittedUnknownAndTrackedReplay(t *testing.T) {
	for _, mode := range []string{"unmanaged", "managed_no_control", "managed_existing_control", "managed_later_import"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			var account string
			if mode != "unmanaged" {
				account = f.create(t, 5000).TargetID
				f.bind(t, account)
				if mode == "managed_existing_control" {
					abObserverConfigurePB(t, f, account)
				}
			}
			r := f.reserve(t)
			input := abExecutorAttemptInput(f, r.RequestID)
			call := abExecutorInvocation(t, f, input)
			captured := abObserverCapture(t, f, call, false)
			abObserverCheck(t, f, captured, "", "")
			if mode == "managed_later_import" {
				abObserverConfigurePB(t, f, account)
				var imported bool
				must(t, "independent original Commander import oracle", f.app.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM periodic_budget_allocations al JOIN upstream_attempts a ON a.id=al.subject_id WHERE a.request_id=$1 AND al.subject_type='supplier_attempt' AND al.origin='legacy_import' AND al.origin_source_id=a.id AND al.window_id IS NULL AND al.admitted_at IS NULL AND al.upper_amount=240 AND al.held_amount=240 AND al.spent_amount=0)`, input.RequestID).Scan(&imported))
				if !imported {
					t.Fatal("later PB configuration did not produce genuine original import")
				}
				abObserverCheck(t, f, captured, "", "")
			}
			// Valid route metadata is deliberately changed; it is not a fifth
			// replay key. Unmanaged expected quote bytes remain ignored.
			input.RouteDecisions = []byte(fmt.Sprintf(`[{"channel_id":%q,"version_id":%q,"priority":0,"weight":1,"reason":"selected"}]`, f.channel, f.version))
			if mode == "unmanaged" {
				negative := int64(-7)
				input.CostQuote = &procurementexecution.CostQuote{VersionID: string([]byte{0xff, 0}), Basis: string([]byte{0xfe}), InputRate: &negative}
			}
			replay := abObserverCapture(t, f, abExecutorInvocation(t, f, input), false)
			abObserverCheck(t, f, replay, "", "")
		})
	}
}

func abObserverConfigurePB(t *testing.T, f *fixture, account string) {
	t.Helper()
	commander, err := periodicbudget.NewCommander(f.app)
	must(t, "construct original supplier Commander for observer fixture", err)
	_, err = commander.ConfigureProcurement(f.ctx, f.admin, account, periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "5000"})
	must(t, "commit original supplier configuration before observing", err)
}

func TestPostgresObserverCommitNeverSentRemainsAbsent(t *testing.T) {
	for _, kind := range []string{"create", "configure", "managed_attempt"} {
		t.Run(kind, func(t *testing.T) {
			s := abDeadlineBaseline(t)
			f := s.f
			input := abExecutorAccountInput(f)
			call := abObserverCreateCall(t, f, input)
			if kind == "configure" {
				input.LimitAmount = 6000
				call = abObserverConfigureCall(t, f, s.account.TargetID, 1, input)
			} else if kind == "managed_attempt" {
				call = abExecutorInvocation(t, f, abExecutorAttemptInput(f, f.reserve(t).RequestID))
			}
			before := f.snapshot(t)
			captured := abObserverCapture(t, f, call, true)
			abObserverCheck(t, f, captured, "receipt_absent", "")
			// A second explicit read is a new RR RO acquisition, not polling or
			// permission to replay the absent mutation.
			abObserverCheck(t, f, captured, "receipt_absent", "")
			if f.snapshot(t) != before {
				t.Fatal("absent original receipt caused a replacement business operation")
			}
			s.validateOld(t, "240", 2)
		})
	}
}

func TestPostgresObserverPermissionFailureKeepsCausesAndRestores(t *testing.T) {
	for _, target := range []string{"command_column", "command_validator", "attempt_column", "attempt_validator"} {
		t.Run(target, func(t *testing.T) {
			f := newFixture(t)
			input := abExecutorAccountInput(f)
			call := abObserverCreateCall(t, f, input)
			g := abGrantItem{role: "APP", privilege: "SELECT", table: "procurement_commands", column: "operation_id"}
			if strings.HasPrefix(target, "attempt_") {
				call = abExecutorInvocation(t, f, abExecutorAttemptInput(f, f.reserve(t).RequestID))
				g.table, g.column = "upstream_attempts", "id"
			}
			if strings.HasSuffix(target, "_validator") {
				g.privilege, g.table, g.column = "EXECUTE", "", ""
				g.function = "pp_validate_original(text,text,text)"
				if target == "attempt_validator" {
					g.function = "pp_validate_attempt(text)"
				}
			}
			captured := abObserverCapture(t, f, call, false)
			restore := abGrantRevoke(t, f, g)
			abObserverCheck(t, f, captured, "observation_unavailable", "42501")
			must(t, "restore complete exact observer ACL", restore())
			abObserverCheck(t, f, captured, "", "")
		})
	}
}

func TestPostgresObserverLegacyCannotIssueHandle(t *testing.T) {
	var request, originalAttempt string
	f := newFixture(t, func(before *fixture) {
		store := billing.New(before.owner)
		reservation, err := store.Reserve(before.ctx, abCompatInput(before))
		must(t, "genuine original request before installing candidate", err)
		request = reservation.RequestID
		attempt, err := frozenattempt.Reject(before.ctx, store, frozenattempt.Input{TenantID: before.tenant, RequestID: request, Sequence: 1, ChannelID: before.channel, ChannelVersionID: before.version, CredentialID: before.credential, UpstreamModel: "upstream", CostQuote: &procurementexecution.CostQuote{VersionID: before.cost, Basis: "contract"}})
		must(t, "genuine original attempt before installing candidate", err)
		originalAttempt = attempt.ID
	})
	var legacy bool
	must(t, "prove genuine legacy has no fabricated tracked companions", f.app.QueryRow(f.ctx, `SELECT a.pp_origin_root_id IS NULL AND NOT EXISTS(SELECT 1 FROM pp_attempt_origins WHERE attempt_id=a.id) AND NOT EXISTS(SELECT 1 FROM pp_attempt_heads WHERE attempt_id=a.id) FROM upstream_attempts a WHERE a.id=$1 AND a.request_id=$2`, originalAttempt, request).Scan(&legacy))
	if !legacy {
		t.Fatal("before-install ordinary API did not produce genuine legacy")
	}
	call := abExecutorInvocation(t, f, abExecutorAttemptInput(f, request))
	pool, wire, _, audit, _ := abObserverUnknownPool(t, f)
	must(t, "arm only genuine legacy capture COMMIT intent", wire.arm())
	before := f.snapshot(t)
	got, err := abExecutorRunner(t, pool).RunAttempt(f.ctx, call)
	var pgerr *pgconn.PgError
	var unknown *procurementexecution.UnknownOutcome
	if !errors.As(err, &pgerr) || pgerr.Code != "PP906" || errors.As(err, &unknown) || !reflect.DeepEqual(got, procurementexecution.AttemptReceipt{}) || f.snapshot(t) != before || wire.observed() != (abExecutorWireState{}) || audit.snapshot() != (abExecutorDialState{dials: 1, startups: 1}) {
		t.Fatal("legacy capture crossed trusted-source gate, issued usable DTO/handle or attempted COMMIT")
	}
	// This is a real pre-capture rejection, not production Observe legacy
	// evidence. The complete legacy observation matrix remains explicitly open.
}

func TestPostgresObserverInvalidIssuerRejectsBeforeAcquire(t *testing.T) {
	f := newFixture(t)
	call := abObserverCreateCall(t, f, abExecutorAccountInput(f))
	captured := abObserverCapture(t, f, call, false)
	foreign := abExecutorRunner(t, captured.pool)
	for _, tc := range []struct {
		name   string
		runner *procurementexecution.Runner
		handle *procurementexecution.ObservationHandle
	}{
		{"nil", captured.runner, nil},
		{"zero", captured.runner, &procurementexecution.ObservationHandle{}},
		{"same_pool_new_runner", foreign, captured.handle},
		{"copied_runner", func() *procurementexecution.Runner { copy := *captured.runner; return &copy }(), captured.handle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.snapshot(t)
			captured.trace.start()
			got, err := tc.runner.Observe(context.Background(), tc.handle)
			trace := captured.trace.finish()
			if string(got.State) != "unresolved" || string(got.Reason) != "invalid_handle" || err == nil || trace.acquires != 0 || trace.releases != 0 || len(trace.queries) != 0 || f.snapshot(t) != before {
				t.Fatal("invalid process-local issuer acquired or observed database state")
			}
		})
	}
	abObserverCheck(t, f, captured, "", "")
}
