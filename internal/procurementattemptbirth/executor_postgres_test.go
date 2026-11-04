package procurementattemptbirth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/procurement"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
)

// These are actual APP/PG tests of the new runner. They leave the original A2
// fixture, SQL/materials, A1 wire and ordinary Store constructors unchanged.
func abExecutorRunner(t *testing.T, pool *pgxpool.Pool) *procurementexecution.Runner {
	t.Helper()
	runner, err := procurementexecution.NewRunner(pool)
	must(t, "construct explicit runner", err)
	return runner
}

func abExecutorAccountInput(f *fixture) procurementexecution.AccountInput {
	return procurementexecution.AccountInput{ActorID: f.admin, OperationID: uuid.NewString(), Label: "supplier", Provider: "fixture", State: "active", LimitAmount: 5000}
}

func abExecutorAttemptInput(f *fixture, request string) procurementexecution.AttemptInput {
	rate := int64(2000000)
	return procurementexecution.AttemptInput{
		TenantID: f.tenant, RequestID: request, Sequence: 1, ChannelID: f.channel,
		ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", RouteDecisions: []byte("[]"),
		CostQuote: &procurementexecution.CostQuote{VersionID: f.cost, InputRate: &rate, OutputRate: &rate, Basis: "contract"},
	}
}

func abExecutorInvocation(t *testing.T, f *fixture, in procurementexecution.AttemptInput) procurementexecution.Invocation {
	t.Helper()
	i, err := procurementexecution.NewRecordAttempt(f.b.Schema, in)
	must(t, "snapshot fixed attempt ABI", err)
	return i
}

func abExecutorJSONEqual(t *testing.T, left, right []byte) bool {
	t.Helper()
	var a, b any
	must(t, "decode actual immutable JSON", json.Unmarshal(left, &a))
	must(t, "decode expected immutable JSON", json.Unmarshal(right, &b))
	return reflect.DeepEqual(a, b)
}

func abExecutorAccountOracle(t *testing.T, f *fixture, got procurementexecution.AccountReceipt) {
	t.Helper()
	// The old public reader is the immutable command oracle; it cannot replace
	// an old snapshot with today's mutable account state.
	old, err := procurement.New(f.app).Command(f.ctx, got.OperationID)
	must(t, "read original immutable command DTO", err)
	if got.Actor != old.Actor || got.Kind != old.Kind || got.TargetID != old.TargetID || got.Status != old.Status || got.Reason != old.Reason || !got.CreatedAt.Equal(old.CreatedAt) || !abExecutorJSONEqual(t, got.Before, old.Before) || !abExecutorJSONEqual(t, got.After, old.After) {
		t.Fatal("runner receipt diverges from the original immutable command")
	}
	var root string
	var journal *string
	must(t, "read exact committed command root/journal", f.app.QueryRow(f.ctx, "SELECT pp_root_id,pp_account_journal_id FROM procurement_commands WHERE operation_id=$1", got.OperationID).Scan(&root, &journal))
	if got.RootID != root || !reflect.DeepEqual(got.AccountJournalID, journal) {
		t.Fatal("runner command root/journal differs from committed original")
	}
	_, err = f.app.Exec(f.ctx, "SELECT pp_validate_original('procurement_command',$1,NULL),pp_validate_root($2)", got.OperationID, root)
	must(t, "actual APP validates immutable command and root", err)
}

func abExecutorAttemptOracle(t *testing.T, f *fixture, got procurementexecution.AttemptReceipt, in procurementexecution.AttemptInput, managed bool) {
	t.Helper()
	if got.Outcome != "applied" || got.RootID == nil || got.SourceID == nil || *got.SourceID != got.AttemptID || got.RequestID != in.RequestID || got.Sequence != in.Sequence || got.DeliveryState != "prepared" || got.CostStatus != "unknown" {
		t.Fatal("runner did not publish a complete committed birth receipt")
	}
	var exact bool
	must(t, "independently authenticate exact attempt and birth identities", f.app.QueryRow(f.ctx, `SELECT
 a.tenant_id=$2 AND a.request_id=$3 AND a.sequence=$4 AND a.channel_id=$5 AND a.channel_version_id=$6 AND a.credential_id=$7 AND a.upstream_model=$8
 AND a.delivery_state='prepared' AND a.cost_status='unknown' AND a.cost_version_id=$10 AND a.cost_input_rate=2000000 AND a.cost_output_rate=2000000 AND a.cost_basis='contract'
 AND o.root_id=$9 AND o.attempt_id=a.id AND a.pp_origin_root_id=o.root_id AND o.tenant_id=a.tenant_id AND o.request_id=a.request_id AND o.sequence=a.sequence
 AND o.channel_id=a.channel_id AND o.channel_version_id=a.channel_version_id AND o.credential_id=a.credential_id AND o.upstream_model=a.upstream_model
 AND ((o.management_mode='managed')=$11) AND r.tenant_id=a.tenant_id AND r.status='reserved' AND r.amount=120
 AND (SELECT count(*) FROM upstream_attempts WHERE tenant_id=$2 AND request_id=$3 AND sequence=$4)=1
 AND (SELECT count(*) FROM pp_root_steps WHERE root_id=o.root_id AND kind='attempt_birth' AND source_id=a.id)=1
 AND (SELECT count(*) FROM procurement_reservations WHERE attempt_id=a.id)=CASE WHEN $11 THEN 1 ELSE 0 END
 AND (SELECT count(*) FROM procurement_budget_events WHERE attempt_id=a.id AND type='reserved' AND held_delta=240 AND spent_delta=0)=CASE WHEN $11 THEN 1 ELSE 0 END
 FROM upstream_attempts a JOIN pp_attempt_origins o ON o.attempt_id=a.id JOIN reservations r ON r.request_id=a.request_id WHERE a.id=$1`, got.AttemptID,
		in.TenantID, in.RequestID, in.Sequence, in.ChannelID, in.ChannelVersionID, in.CredentialID, in.UpstreamModel, *got.RootID, f.cost, managed).Scan(&exact))
	if !exact {
		t.Fatal("committed birth identity/unique financial effects mismatch")
	}
	_, err := f.app.Exec(f.ctx, "SELECT pp_validate_request($1),pp_validate_attempt($2),pp_validate_root($3)", in.RequestID, got.AttemptID, *got.RootID)
	must(t, "actual APP validates runner birth prefix", err)
}

func TestPostgresExecutorAccountRootsPublishOnlyCommittedReceipts(t *testing.T) {
	f := newFixture(t)
	runner := abExecutorRunner(t, f.app)
	in := abExecutorAccountInput(f)
	create, err := procurementexecution.NewCreateAccount(f.b.Schema, in)
	must(t, "construct create ABI", err)
	created, err := runner.RunAccount(f.ctx, create)
	must(t, "runner actual APP create COMMIT", err)
	if created.Outcome != "applied" || created.Status != "applied" || created.AccountJournalID == nil {
		t.Fatal("create did not publish applied immutable command")
	}
	abExecutorAccountOracle(t, f, created)
	for _, tc := range []struct {
		name, target, status, reason string
		revision, limit              int64
		wantRevision                 string
	}{
		{"changed", created.TargetID, "applied", "", 1, 6000, "2"},
		{"noop", created.TargetID, "applied", "", 2, 6000, "2"},
		{"revision_conflict", created.TargetID, "rejected", "revision_conflict", 1, 7000, ""},
		{"not_found", uuid.NewString(), "rejected", "not_found", 1, 7000, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := in
			account.OperationID, account.LimitAmount = uuid.NewString(), tc.limit
			call, e := procurementexecution.NewConfigureAccount(f.b.Schema, procurementexecution.ConfigurationInput{AccountInput: account, AccountID: tc.target, ExpectedRevision: tc.revision})
			must(t, "construct configure ABI", e)
			got, e := runner.RunAccount(f.ctx, call)
			must(t, "runner actual APP configure COMMIT", e)
			if got.Outcome != "applied" || got.Status != tc.status || got.Reason != tc.reason || (got.AccountJournalID == nil) != (tc.reason == "not_found") {
				t.Fatal("configure branch union mismatch")
			}
			if tc.wantRevision != "" {
				var after procurement.Account
				must(t, "read typed committed snapshot", json.Unmarshal(got.After, &after))
				if after.Revision != tc.wantRevision || after.LimitAmount != strconv.FormatInt(tc.limit, 10) || after.HeldAmount != "0" || after.SpentAmount != "0" {
					t.Fatal("changed/noop configuration snapshot mismatch")
				}
			}
			abExecutorAccountOracle(t, f, got)
			before := f.snapshot(t)
			replay, e := runner.RunAccount(f.ctx, call)
			must(t, "runner configure replay explicit COMMIT", e)
			want := got
			want.Outcome = "replay"
			if !reflect.DeepEqual(replay, want) || f.snapshot(t) != before {
				t.Fatal("configure replay changed immutable receipt or any of 34 tables")
			}
		})
	}
	before := f.snapshot(t)
	replayed, err := runner.RunAccount(f.ctx, create)
	must(t, "runner original create replay after current configuration changed", err)
	want := created
	want.Outcome = "replay"
	if !reflect.DeepEqual(replayed, want) || f.snapshot(t) != before {
		t.Fatal("create replay reread today's account or wrote new effects")
	}
	abExecutorAccountOracle(t, f, replayed)
}

func TestPostgresExecutorAttemptRootsPreserveIgnoredBytesAndReplay(t *testing.T) {
	f := newFixture(t)
	runner := abExecutorRunner(t, f.app)
	negative := int64(-1)
	for _, tc := range []struct {
		name  string
		quote *procurementexecution.CostQuote
	}{
		{"absent", nil},
		{"non_null_empty", &procurementexecution.CostQuote{}},
		{"non_utf8_nul_negative_null", &procurementexecution.CostQuote{VersionID: string([]byte{0xff, 0}), Basis: string([]byte{0, 0xfe}), InputRate: &negative}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.reserve(t)
			in := abExecutorAttemptInput(f, r.RequestID)
			in.CostQuote = tc.quote
			call := abExecutorInvocation(t, f, in)
			got, err := runner.RunAttempt(f.ctx, call)
			must(t, "runner actual unmanaged ignored quote COMMIT", err)
			abExecutorAttemptOracle(t, f, got, in, false)
			before := f.snapshot(t)
			replay, err := runner.RunAttempt(f.ctx, call)
			must(t, "runner unmanaged replay COMMIT", err)
			want := got
			want.Outcome, want.RootID, want.SourceID = "replay", nil, nil
			if !reflect.DeepEqual(replay, want) || f.snapshot(t) != before {
				t.Fatal("ignored bytes replay changed receipt/source/financial graph")
			}
		})
	}
	a := f.create(t, 5000)
	f.bind(t, a.TargetID)
	r := f.reserve(t)
	in := abExecutorAttemptInput(f, r.RequestID)
	call := abExecutorInvocation(t, f, in)
	got, err := runner.RunAttempt(f.ctx, call)
	must(t, "runner actual managed birth COMMIT", err)
	abExecutorAttemptOracle(t, f, got, in, true)
	before := f.snapshot(t)
	replay, err := runner.RunAttempt(f.ctx, call)
	must(t, "runner managed replay COMMIT", err)
	want := got
	want.Outcome, want.RootID, want.SourceID = "replay", nil, nil
	if !reflect.DeepEqual(replay, want) || f.snapshot(t) != before {
		t.Fatal("managed replay created another procurement source or hold")
	}
	var held, spent string
	must(t, "independent managed upper-bound oracle", f.app.QueryRow(f.ctx, "SELECT held_amount::text,spent_amount::text FROM procurement_accounts WHERE id=$1", a.TargetID).Scan(&held, &spent))
	if held != "240" || spent != "0" {
		t.Fatal("managed original U=ceil((100+20)*2000000/1000000) mismatch")
	}
}

func abExecutorNoUnknown(t *testing.T, err error, state, message string) {
	t.Helper()
	var pgerr *pgconn.PgError
	var unknown *procurementexecution.UnknownOutcome
	var classified *procurementexecution.Failure
	if !errors.As(err, &pgerr) || pgerr.Code != state || (message != "" && pgerr.Message != message) || errors.As(err, &unknown) || errors.As(err, &classified) {
		t.Fatalf("known native COMMIT rollback lost exact cause or was reclassified: %v", err)
	}
}

// The control decodes the exact real root inside an explicit APP transaction
// before the original server COMMIT failure. The runner call must discard that
// same private receipt; no mock can establish this stage boundary.
func abExecutorCommitFailureControl(t *testing.T, f *fixture, call procurementexecution.Invocation, state, message string) {
	t.Helper()
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	must(t, "begin actual APP COMMIT-stage control", err)
	defer tx.Rollback(f.ctx)
	_, err = tx.Exec(f.ctx, "SELECT pg_catalog.set_config('lock_timeout','3s',true),pg_catalog.set_config('statement_timeout','5s',true)")
	must(t, "retain original root timeouts", err)
	rows, err := tx.Query(f.ctx, call.Query(), call.Arguments()...)
	must(t, "real root succeeds before deferred COMMIT", err)
	if call.Root() == procurementexecution.RecordAttempt {
		r, e := call.DecodeAttempt(rows)
		must(t, "decode private actual attempt before failed COMMIT", e)
		if r.Outcome != "applied" || r.RootID == nil {
			t.Fatal("COMMIT control never reached complete private receipt")
		}
	} else {
		r, e := call.DecodeAccount(rows)
		must(t, "decode private actual account before failed COMMIT", e)
		if r.Outcome != "applied" || r.RootID == "" {
			t.Fatal("COMMIT control never reached complete private receipt")
		}
	}
	abExecutorNoUnknown(t, tx.Commit(f.ctx), state, message)
}

func TestPostgresExecutorDeferredPermissionFailurePublishesNoReceipt(t *testing.T) {
	s := abDeadlineBaseline(t)
	f := s.f
	r := f.reserve(t)
	call := abExecutorInvocation(t, f, abExecutorAttemptInput(f, r.RequestID))
	before := f.snapshot(t)
	// Frozen necessary APP deferred grant, not W root SELECT. Restore checks
	// every grantee ACL, not just this effective permission.
	g := abGrantItem{role: "APP", privilege: "SELECT", table: "pp_operation_roots", column: "id", call: "attempt"}
	restore := abGrantRevoke(t, f, g)
	abExecutorCommitFailureControl(t, f, call, "42501", "")
	got, err := abExecutorRunner(t, f.app).RunAttempt(f.ctx, call)
	abExecutorNoUnknown(t, err, "42501", "")
	if !reflect.DeepEqual(got, procurementexecution.AttemptReceipt{}) || f.snapshot(t) != before {
		t.Fatal("deferred APP denial published a receipt or retained partial effects")
	}
	must(t, "restore exact APP ACL", restore())
	s.validateOld(t, "240", 2)
	fresh := f.reserve(t)
	next := abExecutorAttemptInput(f, fresh.RequestID)
	restored, err := abExecutorRunner(t, f.app).RunAttempt(f.ctx, abExecutorInvocation(t, f, next))
	must(t, "new genuine request after APP ACL restoration", err)
	abExecutorAttemptOracle(t, f, restored, next, true)
	s.validateOld(t, "480", 3)
}

func TestPostgresExecutorOuterCommit23514IsNotBusinessOrUnknown(t *testing.T) {
	s := abDeadlineBaseline(t)
	f := s.f
	for _, kind := range []string{"create", "attempt"} {
		t.Run(kind, func(t *testing.T) {
			table, key := "procurement_commands", "operation_id"
			input := abExecutorAccountInput(f)
			identity := input.OperationID
			call, err := procurementexecution.NewCreateAccount(f.b.Schema, input)
			must(t, "construct create COMMIT fault call", err)
			if kind == "attempt" {
				table, key = "upstream_attempts", "request_id"
				identity = f.reserve(t).RequestID
				call = abExecutorInvocation(t, f, abExecutorAttemptInput(f, identity))
			}
			before := f.snapshot(t)
			// This constraint trigger only rejects; all genuine root/guards have
			// already completed. It has no DML, role change or generated facts.
			_, err = f.owner.Exec(f.ctx, `CREATE FUNCTION ab_executor_commit_fault() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$BEGIN RAISE EXCEPTION 'ab_executor_outer_commit_23514' USING ERRCODE='23514'; END$$`)
			must(t, "install isolated deferred rejection function", err)
			_, err = f.owner.Exec(f.ctx, "CREATE CONSTRAINT TRIGGER zz_ab_executor_commit_fault AFTER INSERT ON "+pgx.Identifier{table}.Sanitize()+" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW."+pgx.Identifier{key}.Sanitize()+"="+"'"+identity+"') EXECUTE FUNCTION ab_executor_commit_fault()")
			must(t, "install exact-identity COMMIT fault", err)
			removed := false
			remove := func() {
				if removed {
					return
				}
				_, e := f.owner.Exec(f.ctx, "DROP TRIGGER zz_ab_executor_commit_fault ON "+pgx.Identifier{table}.Sanitize())
				must(t, "remove isolated COMMIT trigger", e)
				_, e = f.owner.Exec(f.ctx, "DROP FUNCTION ab_executor_commit_fault()")
				must(t, "remove isolated COMMIT function", e)
				removed = true
			}
			defer remove()
			abExecutorCommitFailureControl(t, f, call, "23514", "ab_executor_outer_commit_23514")
			runner := abExecutorRunner(t, f.app)
			if kind == "attempt" {
				got, e := runner.RunAttempt(f.ctx, call)
				abExecutorNoUnknown(t, e, "23514", "ab_executor_outer_commit_23514")
				if !reflect.DeepEqual(got, procurementexecution.AttemptReceipt{}) {
					t.Fatal("COMMIT failure leaked decoded attempt")
				}
			} else {
				got, e := runner.RunAccount(f.ctx, call)
				abExecutorNoUnknown(t, e, "23514", "ab_executor_outer_commit_23514")
				if !reflect.DeepEqual(got, procurementexecution.AccountReceipt{}) {
					t.Fatal("COMMIT failure leaked decoded account")
				}
			}
			remove()
			if f.snapshot(t) != before {
				t.Fatal("server-confirmed COMMIT rollback changed one of 34 tables")
			}
			s.validateOld(t, "240", 2)
		})
	}
	// Genuine fresh operation after removing faults; never retry a model send.
	r := f.reserve(t)
	in := abExecutorAttemptInput(f, r.RequestID)
	got, err := abExecutorRunner(t, f.app).RunAttempt(f.ctx, abExecutorInvocation(t, f, in))
	must(t, "new Runner request succeeds after removing COMMIT fault", err)
	abExecutorAttemptOracle(t, f, got, in, true)
	s.validateOld(t, "480", 3)
}

func abExecutorUnknownError(t *testing.T, err error, sensitive ...string) *procurementexecution.UnknownOutcome {
	t.Helper()
	var unknown *procurementexecution.UnknownOutcome
	if !errors.As(err, &unknown) || unknown.PublicCode() != "procurement_outcome_unknown" || (!errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, net.ErrClosed)) {
		t.Fatalf("lost real ACK did not retain safe typed unknown and transport cause: %v", err)
	}
	handle := unknown.ObservationHandle()
	if handle == nil {
		t.Fatal("unknown lost its runner-issued process-local handle")
	}
	typ := reflect.TypeOf(handle).Elem()
	for n := 0; n < typ.NumField(); n++ {
		if typ.Field(n).IsExported() {
			t.Fatal("observation handle exposes a writable/exported identity field")
		}
	}
	for _, value := range []any{unknown, handle} {
		encoded, e := json.Marshal(value)
		must(t, "encode safe opaque diagnostics", e)
		formatted := fmt.Sprintf("%v|%+v|%#v|%s", value, value, value, encoded)
		for _, secret := range sensitive {
			if secret != "" && strings.Contains(formatted, secret) {
				t.Fatal("opaque unknown/handle diagnostic exposed process-local identity or input")
			}
		}
	}
	return unknown
}

func abExecutorReadOnly(t *testing.T, f *fixture, fn func(context.Context, pgx.Tx)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := f.app.Acquire(ctx)
	must(t, "acquire independent actual APP observation connection", err)
	defer conn.Release()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	must(t, "begin independent RR READ ONLY observation", err)
	defer tx.Rollback(ctx)
	var session, current, iso, ro string
	must(t, "prove actual observer LOGIN and snapshot flags", tx.QueryRow(ctx, "SELECT session_user,current_user,current_setting('transaction_isolation'),current_setting('transaction_read_only')").Scan(&session, &current, &iso, &ro))
	if session != f.b.App || current != f.b.App || iso != "repeatable read" || ro != "on" {
		t.Fatal("independent observation changed identity/isolation/read-only")
	}
	fn(ctx, tx)
	must(t, "end observation without business mutation", tx.Rollback(ctx))
}

func abExecutorCreateDigest(t *testing.T, in procurementexecution.AccountInput) string {
	t.Helper()
	// This exact public payload and envelope are the original Go lockCommand
	// contract, not a writer-generated expected digest.
	public := procurement.CreateInput{OperationID: in.OperationID, Label: in.Label, Provider: in.Provider, State: in.State, LimitAmount: strconv.FormatInt(in.LimitAmount, 10)}
	encoded, err := json.Marshal(struct {
		Kind, Target string
		Payload      any
	}{"account_create", "", public})
	must(t, "encode original Go command identity", err)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func TestPostgresExecutorCommitAckLostKeepsUnknownAndCommittedFacts(t *testing.T) {
	for _, kind := range []string{"create", "attempt"} {
		t.Run(kind, func(t *testing.T) {
			s := abDeadlineBaseline(t)
			f := s.f
			account := abExecutorAccountInput(f)
			call, err := procurementexecution.NewCreateAccount(f.b.Schema, account)
			must(t, "construct unknown create input", err)
			var attempt procurementexecution.AttemptInput
			if kind == "attempt" {
				attempt = abExecutorAttemptInput(f, f.reserve(t).RequestID)
				call = abExecutorInvocation(t, f, attempt)
			}
			before := f.snapshot(t)
			pool, wire, pid, dials := abExecutorUnknownPool(t, f)
			ready := wire.gate()
			runner := abExecutorRunner(t, pool)
			must(t, "arm one authenticated connection and exact COMMIT ACK", wire.arm())
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			type result struct {
				account procurementexecution.AccountReceipt
				attempt procurementexecution.AttemptReceipt
				err     error
			}
			done := make(chan result, 1)
			go func() {
				var r result
				if kind == "attempt" {
					r.attempt, r.err = runner.RunAttempt(ctx, call)
				} else {
					r.account, r.err = runner.RunAccount(ctx, call)
				}
				done <- r
			}()
			taken := false
			defer func() {
				wire.releaseCommit()
				cancel()
				if !taken {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("runner goroutine did not stop after releasing exact commit gate")
					}
				}
			}()
			select {
			case <-ready:
			case r := <-done:
				taken = true
				t.Fatalf("runner ended before actual decoded pre-COMMIT boundary: %v", r.err)
			case <-ctx.Done():
				t.Fatal("actual root never reached exact COMMIT boundary")
			}
			var private, exactContext bool
			must(t, "prove exact original backend is private before frontend COMMIT", f.owner.QueryRow(f.ctx, `SELECT state='idle in transaction',wait_event_type='Client' FROM pg_catalog.pg_stat_activity WHERE pid=$1`, pid).Scan(&private, &exactContext))
			if !private || !exactContext || f.snapshot(t) != before {
				t.Fatal("decoded uncommitted result was not private or affected visible state")
			}
			if state := wire.observed(); state.queries != 1 || state.replies != 0 || state.drops != 0 || state.problem != "" {
				t.Fatal("pre-COMMIT gate did not stop before the real ACK")
			}
			wire.releaseCommit()
			var got result
			select {
			case got = <-done:
				taken = true
			case <-ctx.Done():
				t.Fatal("lost-ACK runner did not return before original bounded context")
			}
			if ctx.Err() != nil || !reflect.DeepEqual(got.account, procurementexecution.AccountReceipt{}) || !reflect.DeepEqual(got.attempt, procurementexecution.AttemptReceipt{}) {
				t.Fatal("lost ACK leaked private DTO or relied on context expiry")
			}
			abExecutorUnknownError(t, got.err, account.OperationID, account.ActorID, account.Label, f.tenant, attempt.RequestID, f.credential, f.cost)
			// pgx read-EOF cleanup sends one exact cancellation packet over a
			// separate socket. Await its completion before classifying every
			// dial; a second StartupMessage would still be a forbidden retry.
			select {
			case <-dials.cleanup:
			case <-time.After(2 * time.Second):
				t.Fatal("original pgx cleanup did not complete after ACK loss")
			}
			state := wire.observed()
			dialState := dials.snapshot()
			if state.queries != 1 || state.replies != 1 || state.drops != 1 || state.problem != "" || dialState != (abExecutorDialState{dials: 2, startups: 1, cancels: 1}) {
				t.Fatalf("unknown COMMIT was replayed/reconnected or exact real ACK/cancellation identity not authenticated: wire=%+v transports=%+v", state, dialState)
			}
			abExecutorWaitBackendGone(t, f, pid)
			committed := f.snapshot(t)
			if committed == before {
				t.Fatal("real COMMIT ACK control did not actually commit visible facts")
			}
			abExecutorReadOnly(t, f, func(read context.Context, tx pgx.Tx) {
				var exact bool
				var root, source, id string
				if kind == "create" {
					must(t, "observe exact independent committed command/source/root/step/journal", tx.QueryRow(read, `SELECT c.target_id,c.pp_root_id,c.pp_source_id,
 c.actor=$2 AND c.kind='account_create' AND c.status='applied' AND c.reason='' AND c.input_digest=$3 AND c.before_state='null'::jsonb
 AND s.id=c.pp_source_id AND s.operation_id=c.operation_id AND s.actor_id=c.actor AND s.kind=c.kind AND s.target_id=c.target_id AND s.input_digest=c.input_digest AND s.journal_id=c.pp_account_journal_id AND s.root_id=c.pp_root_id
 AND s.input_label=$4 AND s.input_provider=$5 AND s.input_state=$6 AND s.input_limit_amount=$7
 AND a.id=c.target_id AND a.label=$4 AND a.provider=$5 AND a.state=$6 AND a.limit_amount=$7 AND a.held_amount=0 AND a.spent_amount=0 AND a.revision=1
 AND j.id=c.pp_account_journal_id AND j.account_id=a.id AND j.root_id=c.pp_root_id AND j.command_source_id=s.id AND j.command_operation_id=c.operation_id
 AND (SELECT count(*) FROM pp_root_steps WHERE root_id=c.pp_root_id AND kind='account_command' AND source_id=c.pp_source_id)=1
 AND (SELECT count(*) FROM procurement_commands WHERE operation_id=$1)=1
 FROM procurement_commands c JOIN pp_account_command_sources s ON s.id=c.pp_source_id JOIN procurement_accounts a ON a.id=c.target_id JOIN pp_account_journal j ON j.id=c.pp_account_journal_id WHERE c.operation_id=$1`, account.OperationID, account.ActorID, abExecutorCreateDigest(t, account), account.Label, account.Provider, account.State, account.LimitAmount).Scan(&id, &root, &source, &exact))
					_, e := tx.Exec(read, "SELECT pp_validate_original('procurement_command',$1,NULL),pp_validate_root($2)", account.OperationID, root)
					must(t, "read-only exact command/root validators", e)
				} else {
					must(t, "observe exact independent committed managed attempt and unique U", tx.QueryRow(read, `SELECT a.id,a.pp_origin_root_id,o.attempt_id,
 a.tenant_id=$1 AND a.request_id=$2 AND a.sequence=$3 AND a.channel_id=$4 AND a.channel_version_id=$5 AND a.credential_id=$6 AND a.upstream_model=$7
 AND a.delivery_state='prepared' AND a.cost_status='unknown' AND o.root_id=a.pp_origin_root_id AND o.management_mode='managed' AND o.account_id=$8
 AND p.attempt_id=a.id AND p.account_id=$8 AND p.upper_amount=240 AND p.held_amount=240 AND p.spent_amount=0 AND p.state='prepared'
 AND e.attempt_id=a.id AND e.account_id=p.account_id AND e.type='reserved' AND e.held_delta=240 AND e.spent_delta=0 AND e.id=o.reserved_event_id
 AND (SELECT count(*) FROM upstream_attempts WHERE tenant_id=$1 AND request_id=$2 AND sequence=$3)=1
 AND (SELECT count(*) FROM pp_root_steps WHERE root_id=o.root_id AND kind='attempt_birth' AND source_id=a.id)=1
 AND (SELECT count(*) FROM procurement_budget_events WHERE attempt_id=a.id)=1
 FROM upstream_attempts a JOIN pp_attempt_origins o ON o.attempt_id=a.id JOIN procurement_reservations p ON p.attempt_id=a.id JOIN procurement_budget_events e ON e.attempt_id=a.id
 WHERE a.tenant_id=$1 AND a.request_id=$2 AND a.sequence=$3`, attempt.TenantID, attempt.RequestID, attempt.Sequence, attempt.ChannelID, attempt.ChannelVersionID, attempt.CredentialID, attempt.UpstreamModel, s.account.TargetID).Scan(&id, &root, &source, &exact))
					_, e := tx.Exec(read, "SELECT pp_validate_request($1),pp_validate_attempt($2),pp_validate_account($3),pp_validate_root($4)", attempt.RequestID, id, s.account.TargetID, root)
					must(t, "read-only exact managed attempt/root validators", e)
				}
				if !exact || uuid.Validate(id) != nil || uuid.Validate(root) != nil || uuid.Validate(source) != nil {
					t.Fatal("independent committed facts do not match exact unknown invocation")
				}
			})
			if f.snapshot(t) != committed || wire.observed() != state || dials.snapshot() != dialState {
				t.Fatal("observation mutated/replayed business facts or used the dead runner connection")
			}
			held, ordinal := "240", int64(2)
			if kind == "attempt" {
				held, ordinal = "480", 3
			}
			s.validateOld(t, held, ordinal)
			// The original call remains unknown even after facts are visible;
			// this test never calls it again or publishes any recovered DTO.
			abExecutorUnknownError(t, got.err, account.OperationID, attempt.RequestID)
		})
	}
}
