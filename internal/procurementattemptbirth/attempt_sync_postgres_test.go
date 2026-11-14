package procurementattemptbirth_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/procurement"
)

// Each concurrent public operation owns an actual APP backend. Knowing its PID
// before starting avoids confusing a different pooled operation with the waiter.
func abSyncApp(t *testing.T, f *fixture) (*pgxpool.Pool, int32) {
	t.Helper()
	cfg := f.app.Config().Copy()
	cfg.MaxConns = 1
	p, err := pgxpool.NewWithConfig(f.ctx, cfg)
	must(t, "open exact synchronization APP backend", err)
	t.Cleanup(p.Close)
	var pid int32
	var session, current, isolation, lockTimeout, statementTimeout string
	must(t, "identify synchronization APP and unchanged budgets", p.QueryRow(f.ctx, `SELECT pg_backend_pid(),session_user,current_user,current_setting('transaction_isolation'),current_setting('lock_timeout'),current_setting('statement_timeout')`).Scan(&pid, &session, &current, &isolation, &lockTimeout, &statementTimeout))
	if session != f.b.App || current != f.b.App || isolation != "read committed" || lockTimeout != "3s" || statementTimeout != "5s" {
		t.Fatal("synchronization requires actual APP LOGIN, RC, 3s lock and 5s statement budgets")
	}
	return p, pid
}

type abSyncCall[T any] struct {
	done chan struct{}
	out  T
	err  error
}

func abSyncStart[T any](t *testing.T, f *fixture, fn func(context.Context) (T, error)) *abSyncCall[T] {
	t.Helper()
	ctx, cancel := context.WithCancel(f.ctx)
	c := &abSyncCall[T]{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		c.out, c.err = fn(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-c.done:
		case <-time.After(6 * time.Second):
			t.Error("synchronization public call did not drain after cancellation")
		}
	})
	return c
}

func abSyncAwait[T any](t *testing.T, f *fixture, c *abSyncCall[T]) (T, error) {
	t.Helper()
	select {
	case <-c.done:
		return c.out, c.err
	case <-f.ctx.Done():
		t.Fatal("synchronization call exceeded fixture context")
		var zero T
		return zero, f.ctx.Err()
	}
}

func abSyncAttempt(t *testing.T, f *fixture, p *pgxpool.Pool, request string) *abSyncCall[attemptReceipt] {
	t.Helper()
	return abSyncStart(t, f, func(ctx context.Context) (attemptReceipt, error) {
		call := *f
		call.ctx, call.app = ctx, p
		return call.attempt(request)
	})
}

func abSyncWait(t *testing.T, f *fixture, pid, blocker int32, advisory bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 1500*time.Millisecond)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		err := f.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE pid=$1 AND state='active' AND wait_event_type='Lock'
 AND $2::integer=ANY(pg_blocking_pids(pid)) AND (NOT $3::boolean OR wait_event='advisory'))`, pid, blocker, advisory).Scan(&waiting)
		must(t, "observe exact PID blocking relation", err)
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("APP backend %d did not wait on exact blocker %d (advisory=%v)", pid, blocker, advisory)
		case <-tick.C:
		}
	}
}

func abSyncAccountBlocker(t *testing.T, f *fixture, account string) (pgx.Tx, int32) {
	t.Helper()
	tx, err := f.owner.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	must(t, "begin synchronization account blocker", err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	})
	var pid int32
	must(t, "identify exact account blocker", tx.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	_, err = tx.Exec(f.ctx, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, account)
	must(t, "hold exact account N", err)
	return tx, pid
}

type abSyncBarrier struct {
	conn     *pgxpool.Conn
	pid      int32
	schema   string
	released bool
}

func abSyncPause(t *testing.T, f *fixture, table string) *abSyncBarrier {
	t.Helper()
	if table != "upstream_attempts" && table != "procurement_reservations" && table != "periodic_budget_operations" {
		t.Fatal("unexpected synchronization barrier table")
	}
	function := pgx.Identifier{f.b.Schema, "a4_sync_pause"}.Sanitize()
	relation := pgx.Identifier{f.b.Schema, table}.Sanitize()
	_, err := f.owner.Exec(f.ctx, `CREATE FUNCTION `+function+`() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $$
 BEGIN PERFORM pg_catalog.pg_advisory_xact_lock(23023029,pg_catalog.hashtext(TG_TABLE_SCHEMA)); RETURN NEW; END $$;
 CREATE TRIGGER zzzz_a4_sync_pause BEFORE INSERT ON `+relation+` FOR EACH ROW EXECUTE FUNCTION `+function+`()`)
	must(t, "install value-preserving test-only synchronization barrier", err)
	conn, err := f.owner.Acquire(f.ctx)
	must(t, "acquire stable session advisory blocker", err)
	b := &abSyncBarrier{conn: conn, schema: f.b.Schema}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !b.released {
			_, e := b.conn.Exec(ctx, `SELECT pg_advisory_unlock(23023029,hashtext($1))`, b.schema)
			if e != nil {
				t.Errorf("release synchronization barrier: %v", e)
			}
		}
		b.conn.Release()
	})
	must(t, "identify exact advisory blocker", conn.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&b.pid))
	_, err = conn.Exec(f.ctx, `SELECT pg_advisory_lock(23023029,hashtext($1))`, b.schema)
	must(t, "hold exact test-only synchronization object", err)
	return b
}

func (b *abSyncBarrier) release(t *testing.T, f *fixture) {
	t.Helper()
	var unlocked bool
	must(t, "release observed synchronization barrier", b.conn.QueryRow(f.ctx, `SELECT pg_advisory_unlock(23023029,hashtext($1))`, b.schema).Scan(&unlocked))
	if !unlocked || b.released {
		t.Fatal("synchronization barrier was not held exactly once")
	}
	b.released = true
}

func abSyncNamespace(t *testing.T, f *fixture, pid int32, account, mode string) {
	t.Helper()
	var held bool
	must(t, "prove exact supplier namespace object and mode", f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks
 WHERE pid=$1 AND locktype='advisory' AND granted AND classid=23023023 AND objsubid=2
 AND objid::bigint=(hashtext($2||':procurement:'||$3)::bigint & 4294967295) AND mode=$4)`, pid, f.b.Schema, account, mode).Scan(&held))
	if !held {
		t.Fatalf("backend %d does not own expected supplier namespace %s", pid, mode)
	}
}

func abSyncBindInput(t *testing.T, f *fixture, account string) procurement.BindingInput {
	t.Helper()
	in := procurement.BindingInput{OperationID: uuid.NewString(), ChannelID: f.channel, CredentialID: f.credential, AccountID: account}
	must(t, "read original current channel revision", f.owner.QueryRow(f.ctx, `SELECT management_revision::text FROM channels WHERE id=$1`, f.channel).Scan(&in.ExpectedChannelRevision))
	return in
}

func abSyncBinding(t *testing.T, f *fixture, in procurement.BindingInput, out procurement.Command) {
	t.Helper()
	if out.OperationID != in.OperationID || out.Kind != "credential_bind" || out.Status != "applied" || out.Reason != "" {
		t.Fatalf("original binding receipt: %+v", out)
	}
	var exact bool
	must(t, "independent original binding command and audit oracle", f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*)=1 FROM credential_procurement_bindings WHERE operation_id=$1 AND credential_id=$2 AND channel_id=$3 AND account_id=$4)
 AND (SELECT count(*)=1 FROM procurement_commands WHERE operation_id=$1 AND kind='credential_bind' AND status='applied' AND pp_source_id IS NULL)
 AND (SELECT count(*)=1 FROM audit_events WHERE target=$1 AND action='procurement.command.applied')`, in.OperationID, f.credential, f.channel, in.AccountID).Scan(&exact))
	if !exact {
		t.Fatal("public binding did not retain exact original command/audit")
	}
}

func abSyncManaged(t *testing.T, f *fixture, account, request string, got attemptReceipt) {
	t.Helper()
	if got.Outcome != "applied" || got.Root == nil || got.Source == nil || got.Request != request {
		t.Fatal("managed synchronization receipt is incomplete")
	}
	var exact bool
	must(t, "independent exact managed birth and hand-calculated hold oracle", f.owner.QueryRow(f.ctx, `SELECT
 o.tenant_id=$1 AND o.request_id=$2 AND o.management_mode='managed' AND o.account_id=$3
 AND a.tenant_id=o.tenant_id AND a.request_id=o.request_id AND a.pp_origin_root_id=o.root_id
 AND p.account_id=o.account_id AND p.binding_id=o.binding_id AND p.upper_amount=240 AND p.held_amount=240 AND p.spent_amount=0
 AND e.id=o.reserved_event_id AND e.type='reserved' AND e.event_key='reserve:'||a.id AND e.held_delta=240 AND e.spent_delta=0
 AND j.id=o.account_journal_id AND j.source_id=e.id AND j.kind='reserve' AND j.after_held_amount=240 AND j.after_spent_amount=0
 AND c.held_amount=240 AND c.spent_amount=0 AND h.held_amount=240 AND h.spent_amount=0 AND h.ordinal=2
 AND root.id=$5 AND root.step_count=2 AND root.semantic_result='managed'
 AND (SELECT count(*) FROM procurement_reservations WHERE account_id=c.id)=1
 AND (SELECT count(*) FROM procurement_budget_events WHERE account_id=c.id)=1
 AND (SELECT count(*) FROM pp_account_journal WHERE account_id=c.id AND kind='reserve')=1
 FROM pp_attempt_origins o JOIN upstream_attempts a ON a.id=o.attempt_id
 JOIN procurement_reservations p ON p.attempt_id=a.id JOIN procurement_budget_events e ON e.attempt_id=a.id
 JOIN pp_account_journal j ON j.attempt_id=a.id JOIN procurement_accounts c ON c.id=p.account_id
 JOIN pp_account_heads h ON h.account_id=c.id JOIN pp_operation_roots root ON root.id=o.root_id
 WHERE a.id=$4 AND j.kind='reserve'`, f.tenant, request, account, got.Attempt, *got.Root).Scan(&exact))
	if !exact {
		t.Fatal("managed synchronization changed exact source relations or shared money")
	}
	_, err := f.app.Exec(f.ctx, `SELECT pp_validate_attempt($1),pp_validate_account($2)`, got.Attempt, account)
	must(t, "actual APP complete synchronized prefix validation", err)
}

func TestPostgresA4BindingBeforeAttemptWaitsAndClassifiesManaged(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, 1000)
	r := f.reserve(t)
	in := abSyncBindInput(t, f, a.TargetID)
	binder, bindPID := abSyncApp(t, f)
	attemptPool, attemptPID := abSyncApp(t, f)
	blocker, ownerPID := abSyncAccountBlocker(t, f, a.TargetID)
	bind := abSyncStart(t, f, func(ctx context.Context) (procurement.Command, error) {
		return procurement.New(binder).BindCredential(ctx, f.admin, in)
	})
	abSyncWait(t, f, bindPID, ownerPID, false)
	birth := abSyncAttempt(t, f, attemptPool, r.RequestID)
	abSyncWait(t, f, attemptPID, bindPID, false)
	must(t, "release account after exact binding/channel wait chain", blocker.Commit(f.ctx))
	bound, err := abSyncAwait(t, f, bind)
	must(t, "original binding commits before waiting birth", err)
	abSyncBinding(t, f, in, bound)
	got, err := abSyncAwait(t, f, birth)
	must(t, "waiting birth sees newly committed binding", err)
	abSyncManaged(t, f, a.TargetID, r.RequestID, got)
}

func TestPostgresA4AttemptBeforeBindingKeepsUnmanagedHistory(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, 1000)
	r := f.reserve(t)
	in := abSyncBindInput(t, f, a.TargetID)
	attemptPool, attemptPID := abSyncApp(t, f)
	binder, bindPID := abSyncApp(t, f)
	barrier := abSyncPause(t, f, "upstream_attempts")
	birth := abSyncAttempt(t, f, attemptPool, r.RequestID)
	abSyncWait(t, f, attemptPID, barrier.pid, true)
	bind := abSyncStart(t, f, func(ctx context.Context) (procurement.Command, error) {
		return procurement.New(binder).BindCredential(ctx, f.admin, in)
	})
	abSyncWait(t, f, bindPID, attemptPID, false)
	barrier.release(t, f)
	got, err := abSyncAwait(t, f, birth)
	must(t, "earlier unbound birth commits", err)
	bound, err := abSyncAwait(t, f, bind)
	must(t, "binding follows committed unmanaged birth", err)
	abSyncBinding(t, f, in, bound)
	var exact bool
	must(t, "unmanaged history has no supplier backfill", f.owner.QueryRow(f.ctx, `SELECT
 o.management_mode='unmanaged' AND o.account_id IS NULL AND o.binding_id IS NULL AND root.step_count=1 AND root.semantic_result='unmanaged'
 AND c.held_amount=0 AND c.spent_amount=0 AND h.ordinal=1
 AND NOT EXISTS(SELECT 1 FROM procurement_reservations WHERE attempt_id=o.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM procurement_budget_events WHERE attempt_id=o.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_id=o.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM pp_account_journal WHERE attempt_id=o.attempt_id)
 FROM pp_attempt_origins o JOIN pp_operation_roots root ON root.id=o.root_id
 CROSS JOIN procurement_accounts c JOIN pp_account_heads h ON h.account_id=c.id
 WHERE o.attempt_id=$1 AND c.id=$2`, got.Attempt, a.TargetID).Scan(&exact))
	if !exact || got.Outcome != "applied" {
		t.Fatal("later binding changed the committed unmanaged branch")
	}
	before := f.snapshot(t)
	replay, err := f.attempt(r.RequestID)
	must(t, "replay unmanaged attempt after binding", err)
	if replay.Outcome != "replay" || replay.Attempt != got.Attempt || before != f.snapshot(t) {
		t.Fatal("binding caused old attempt replay to create supplier effects")
	}
	next := f.reserve(t)
	managed, err := f.attempt(next.RequestID)
	must(t, "new request follows new committed binding", err)
	abSyncManaged(t, f, a.TargetID, next.RequestID, managed)
}

func abSyncPBInput() periodicbudget.PutPeriodicBudget {
	return periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "1000"}
}

func abSyncPB(t *testing.T, f *fixture, account string, got attemptReceipt, configured periodicbudget.ConfigureResult, origin string) {
	t.Helper()
	if !configured.Created {
		t.Fatal("first supplier budget configuration was not committed as creation")
	}
	var exact bool
	must(t, "independent real PB new/import source and hold oracle", f.owner.QueryRow(f.ctx, `SELECT
 c.id=$2 AND c.scope='procurement' AND c.account_id=$3 AND c.held_amount=240 AND c.spent_amount=0 AND c.event_seq=1
 AND a.subject_type='supplier_attempt' AND a.origin=$4 AND a.upper_amount=240 AND a.baseline=0
 AND a.held_amount=240 AND a.spent_amount=0 AND a.source_held=240 AND a.source_spent=0 AND a.revision=1
 AND e.allocation_before_revision=0 AND e.before_held=0 AND e.before_spent=0 AND e.after_held=240 AND e.after_spent=0
 AND a.last_event_id=e.id AND c.last_event_id=e.id
 AND CASE WHEN $4='new' THEN
   p.pb_control=c.id AND a.origin_source_id=pe.id AND a.admitted_at=p.pb_admitted_at AND a.window_id IS NOT NULL
   AND e.source_kind='procurement_event' AND e.source_id=pe.id
   AND EXISTS(SELECT 1 FROM periodic_budget_windows w WHERE w.id=a.window_id AND w.control_id=c.id AND w.held_amount=240 AND w.spent_amount=0)
 ELSE p.pb_control IS NULL AND a.origin_source_id=p.attempt_id AND a.admitted_at IS NULL AND a.window_id IS NULL
   AND e.source_kind='legacy_import' AND e.source_id=p.attempt_id END
 AND (SELECT count(*) FROM periodic_budget_allocations WHERE control_id=c.id)=1
 AND (SELECT count(*) FROM periodic_budget_events WHERE control_id=c.id)=1
 AND (SELECT count(*) FROM periodic_budget_operations WHERE resource_id=c.id)=1
 FROM procurement_reservations p JOIN procurement_budget_events pe ON pe.attempt_id=p.attempt_id
 JOIN periodic_budget_allocations a ON a.subject_id=p.attempt_id
 JOIN periodic_budget_controls c ON c.id=a.control_id JOIN periodic_budget_events e ON e.allocation_id=a.id
 WHERE p.attempt_id=$1`, got.Attempt, string(configured.Receipt.Operation.ResourceID), account, origin).Scan(&exact))
	if !exact {
		t.Fatal("first-configuration race lost or duplicated actual supplier source")
	}
	_, err := f.app.Exec(f.ctx, `SELECT pp_validate_attempt($1),pp_validate_account($2)`, got.Attempt, account)
	must(t, "actual APP synchronized PB prefix validation", err)
}

func TestPostgresA4SupplierFirstConfigurationBeforeBirth(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, 1000)
	in := abSyncBindInput(t, f, a.TargetID)
	bound, err := procurement.New(f.app).BindCredential(f.ctx, f.admin, in)
	must(t, "original public binding before PB race", err)
	abSyncBinding(t, f, in, bound)
	r := f.reserve(t)
	commandPool, commandPID := abSyncApp(t, f)
	attemptPool, attemptPID := abSyncApp(t, f)
	commander, err := periodicbudget.NewCommander(commandPool)
	must(t, "construct actual APP supplier Commander", err)
	barrier := abSyncPause(t, f, "periodic_budget_operations")
	configure := abSyncStart(t, f, func(ctx context.Context) (periodicbudget.ConfigureResult, error) {
		return commander.ConfigureProcurement(ctx, f.admin, a.TargetID, abSyncPBInput())
	})
	abSyncWait(t, f, commandPID, barrier.pid, true)
	abSyncNamespace(t, f, commandPID, a.TargetID, "ExclusiveLock")
	birth := abSyncAttempt(t, f, attemptPool, r.RequestID)
	abSyncWait(t, f, attemptPID, commandPID, false)
	barrier.release(t, f)
	configured, err := abSyncAwait(t, f, configure)
	must(t, "first supplier configuration commits before birth", err)
	got, err := abSyncAwait(t, f, birth)
	must(t, "birth rechecks newly committed supplier control", err)
	abSyncManaged(t, f, a.TargetID, r.RequestID, got)
	abSyncPB(t, f, a.TargetID, got, configured, "new")
}

func TestPostgresA4BirthBeforeSupplierFirstConfiguration(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, 1000)
	in := abSyncBindInput(t, f, a.TargetID)
	bound, err := procurement.New(f.app).BindCredential(f.ctx, f.admin, in)
	must(t, "original public binding before birth/import race", err)
	abSyncBinding(t, f, in, bound)
	r := f.reserve(t)
	attemptPool, attemptPID := abSyncApp(t, f)
	commandPool, commandPID := abSyncApp(t, f)
	commander, err := periodicbudget.NewCommander(commandPool)
	must(t, "construct actual APP import Commander", err)
	barrier := abSyncPause(t, f, "procurement_reservations")
	birth := abSyncAttempt(t, f, attemptPool, r.RequestID)
	abSyncWait(t, f, attemptPID, barrier.pid, true)
	abSyncNamespace(t, f, attemptPID, a.TargetID, "ShareLock")
	configure := abSyncStart(t, f, func(ctx context.Context) (periodicbudget.ConfigureResult, error) {
		return commander.ConfigureProcurement(ctx, f.admin, a.TargetID, abSyncPBInput())
	})
	abSyncWait(t, f, commandPID, attemptPID, false)
	barrier.release(t, f)
	got, err := abSyncAwait(t, f, birth)
	must(t, "unconfigured managed birth commits before import", err)
	configured, err := abSyncAwait(t, f, configure)
	must(t, "first configuration imports just-committed real hold", err)
	abSyncManaged(t, f, a.TargetID, r.RequestID, got)
	abSyncPB(t, f, a.TargetID, got, configured, "legacy_import")
}

// Normalize only the independently verified successful configure delta. Every
// other row of all 34 snapshot tables must remain byte-for-byte equal.
func abSyncOnlyConfigureDelta(t *testing.T, f *fixture, before, account, operation string, configured receipt) {
	t.Helper()
	if configured.Outcome != "applied" || configured.Status != "applied" || configured.Reason != "" || configured.AccountJournalID == nil || configured.TargetID != account || configured.OperationID != operation {
		t.Fatal("queued configure did not commit a complete applied receipt")
	}
	var exact bool
	must(t, "independent committed pause projection oracle", f.owner.QueryRow(f.ctx, `SELECT
 a.state='paused' AND a.revision=2 AND a.limit_amount=1000 AND a.held_amount=0 AND a.spent_amount=0
 AND h.state='paused' AND h.configuration_revision=2 AND h.limit_amount=1000 AND h.held_amount=0 AND h.spent_amount=0
 AND h.ordinal=2 AND h.journal_id=$2 AND h.blocking_count=0 AND h.updated_xid=s.created_xid
 AND s.kind='account_configure' AND s.operation_id=$4 AND s.actor_id=$5 AND s.actor_role='admin' AND s.actor_status='active'
 AND s.input_target_id=$1 AND s.target_id=$1 AND s.input_label='supplier' AND s.input_provider='fixture'
 AND s.input_state='paused' AND s.input_limit_amount=1000 AND s.input_expected_revision=1
 AND s.command_status='applied' AND s.command_reason='' AND s.journal_id=j.id AND s.step_no=1 AND s.created_xid<>a.pp_created_xid
 AND j.root_id=$3 AND j.kind='configure' AND j.source_kind='command' AND j.source_id=$4 AND j.command_source_id=s.id
 AND j.created_xid=s.created_xid AND j.after_state='paused' AND j.after_configuration_revision=2 AND j.after_held_amount=0 AND j.after_spent_amount=0
 AND root.entry_kind='account_configure' AND root.scope_kind='account_command' AND root.account_target_id=$1
 AND root.created_xid=s.created_xid AND root.session_role_oid=$6 AND root.step_count=1 AND root.semantic_result='applied'
 FROM procurement_accounts a JOIN pp_account_heads h ON h.account_id=a.id
 JOIN pp_account_command_sources s ON s.root_id=$3 AND s.target_id=a.id
 JOIN pp_account_journal j ON j.id=h.journal_id JOIN pp_operation_roots root ON root.id=s.root_id
 WHERE a.id=$1`, account, *configured.AccountJournalID, configured.RootID, operation, f.admin, f.b.AppOID).Scan(&exact))
	if !exact {
		t.Fatal("failed waiter changed the independently expected configure/account result")
	}
	var old, now map[string][]json.RawMessage
	must(t, "decode complete before snapshot", json.Unmarshal([]byte(before), &old))
	must(t, "decode complete post-race snapshot", json.Unmarshal([]byte(f.snapshot(t)), &now))
	if len(old) != 34 || len(now) != 34 {
		t.Fatal("synchronization rollback oracle lost full 34-table coverage")
	}
	for table, selector := range map[string]struct{ field, value string }{
		"pp_operation_roots":         {"id", configured.RootID},
		"pp_root_steps":              {"root_id", configured.RootID},
		"pp_account_command_sources": {"root_id", configured.RootID},
		"pp_account_journal":         {"root_id", configured.RootID},
		"procurement_commands":       {"operation_id", configured.OperationID},
		"audit_events":               {"target", configured.OperationID},
	} {
		kept := make([]json.RawMessage, 0, len(now[table]))
		removed := 0
		for _, raw := range now[table] {
			var row map[string]json.RawMessage
			must(t, "decode configure delta row", json.Unmarshal(raw, &row))
			var id string
			if v := row[selector.field]; v != nil && string(v) != "null" {
				must(t, "decode configure delta identity", json.Unmarshal(v, &id))
			}
			if id == selector.value {
				removed++
			} else {
				kept = append(kept, raw)
			}
		}
		if removed != 1 {
			t.Fatalf("legal configure delta in %s has %d rows instead of one", table, removed)
		}
		now[table] = kept
	}
	var baselineAccount map[string]json.RawMessage
	for _, projection := range []struct {
		table, field string
		allowed      []string
	}{
		{"procurement_accounts", "id", []string{"state", "revision"}},
		{"pp_account_heads", "account_id", []string{"state", "configuration_revision", "ordinal", "journal_id", "updated_xid"}},
	} {
		table, field := projection.table, projection.field
		var original json.RawMessage
		var originalRow map[string]json.RawMessage
		originalCount := 0
		for _, raw := range old[table] {
			var row map[string]json.RawMessage
			must(t, "decode original account projection", json.Unmarshal(raw, &row))
			var id string
			must(t, "decode original account projection identity", json.Unmarshal(row[field], &id))
			if id == configured.TargetID {
				original = raw
				originalRow = row
				originalCount++
			}
		}
		if originalCount != 1 {
			t.Fatal("expected exactly one original configured account projection")
		}
		// These pre-race values are independent of the writer under test. In
		// particular the original head points to the original creation source.
		expectedOld := map[string]json.RawMessage{"state": json.RawMessage(`"active"`)}
		if table == "procurement_accounts" {
			baselineAccount = originalRow
			expectedOld["revision"] = json.RawMessage(`1`)
		} else {
			expectedOld["configuration_revision"] = json.RawMessage(`1`)
			expectedOld["ordinal"] = json.RawMessage(`1`)
			expectedOld["journal_id"] = baselineAccount["pp_origin_journal_id"]
			expectedOld["updated_xid"] = baselineAccount["pp_created_xid"]
		}
		for name, want := range expectedOld {
			if len(want) == 0 || string(want) == "null" || !reflect.DeepEqual(originalRow[name], want) {
				t.Fatalf("unexpected original %s.%s before queued configure", table, name)
			}
		}
		restored := 0
		for i, raw := range now[table] {
			var row map[string]json.RawMessage
			must(t, "decode current account projection", json.Unmarshal(raw, &row))
			var id string
			must(t, "decode current account projection identity", json.Unmarshal(row[field], &id))
			if id == configured.TargetID {
				// Only these exact fields may differ; all identities, labels,
				// provider, timestamps and other fields must equal the full old row.
				unchanged := make(map[string]json.RawMessage, len(originalRow))
				for name, value := range originalRow {
					unchanged[name] = value
				}
				for _, name := range projection.allowed {
					delete(unchanged, name)
					delete(row, name)
				}
				if !reflect.DeepEqual(unchanged, row) {
					t.Fatalf("configure changed forbidden fields in complete %s row", table)
				}
				now[table][i] = original
				restored++
			}
		}
		if original == nil || restored != 1 {
			t.Fatal("expected exactly one before/current configured account projection")
		}
	}
	if !reflect.DeepEqual(old, now) {
		for table := range old {
			if !reflect.DeepEqual(old[table], now[table]) {
				t.Errorf("failed waiter retained unexpected changes in full snapshot table %s", table)
			}
		}
		t.Fatal("queued pause plus failed birth changed more than the genuine configure delta")
	}
}

func TestPostgresA4AccountWaitRechecksCommittedPause(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, 1000)
	in := abSyncBindInput(t, f, a.TargetID)
	bound, err := procurement.New(f.app).BindCredential(f.ctx, f.admin, in)
	must(t, "public binding before account invalidation", err)
	abSyncBinding(t, f, in, bound)
	r := f.reserve(t)
	before := f.snapshot(t)
	configurePool, configurePID := abSyncApp(t, f)
	attemptPool, attemptPID := abSyncApp(t, f)
	blocker, ownerPID := abSyncAccountBlocker(t, f, a.TargetID)
	operation := uuid.NewString()
	configure := abSyncStart(t, f, func(ctx context.Context) (receipt, error) {
		var raw []byte
		out := receipt{}
		err := configurePool.QueryRow(ctx, `SELECT to_jsonb(x) FROM pp_configure_account($1,$2,$3,1,'supplier','fixture','paused',1000) x`, f.admin, operation, a.TargetID).Scan(&raw)
		if err == nil {
			err = json.Unmarshal(raw, &out)
		}
		return out, err
	})
	abSyncWait(t, f, configurePID, ownerPID, false)
	birth := abSyncAttempt(t, f, attemptPool, r.RequestID)
	// The queued configure is also a real soft blocker of this later waiter.
	abSyncWait(t, f, attemptPID, configurePID, false)
	must(t, "release FIFO account queue after exact PID proof", blocker.Commit(f.ctx))
	configured, err := abSyncAwait(t, f, configure)
	must(t, "queued public configure commits paused state", err)
	_, err = abSyncAwait(t, f, birth)
	sqlstate(t, err, "PP114")
	abSyncOnlyConfigureDelta(t, f, before, a.TargetID, operation, configured)
	_, err = f.app.Exec(f.ctx, `SELECT pp_validate_account($1)`, a.TargetID)
	must(t, "actual APP validates surviving legal configure prefix", err)
}

func abSyncOtherTenant(t *testing.T, f *fixture) *fixture {
	t.Helper()
	other := *f
	other.tenant, other.user, other.key = uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err := f.owner.Begin(f.ctx)
	must(t, "begin original second-tenant management setup", err)
	defer tx.Rollback(f.ctx)
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants(id,name) VALUES($1,'A4 second tenant')`, []any{other.tenant}},
		{`INSERT INTO users(id,email,password_hash) VALUES($1,$2,'fixture-disabled')`, []any{other.user, other.user + "@example.invalid"}},
		{`INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, []any{other.tenant, other.user}},
		{`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, []any{other.tenant, other.model}},
		{`INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'A4 second tenant','a4-second-key',$5,$4,100000)`, []any{other.key, other.tenant, other.user, []string{other.model}, "fixture-disabled:" + other.key}},
	} {
		_, err = tx.Exec(f.ctx, row.sql, row.args...)
		must(t, "seed only original second-tenant management parents", err)
	}
	must(t, "commit second-tenant management parents", tx.Commit(f.ctx))
	store := billing.New(f.app)
	credit, err := store.CreateCredit(f.ctx, billing.CreditInput{TenantID: other.tenant, ActorID: f.operator, BusinessRef: uuid.NewString(), ProofRef: "fixture:verified", Amount: 10000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 10000, IdempotencyKey: uuid.NewString(), RequestHMAC: "a4:second-tenant-credit"})
	must(t, "actual APP original second-tenant credit proposal", err)
	_, err = store.ApproveCredit(f.ctx, billing.ApprovalInput{TenantID: other.tenant, ActorID: f.approver, OrderID: credit.ID})
	must(t, "actual APP original second-tenant distinct approver funding", err)
	return &other
}

func TestPostgresA4TwoTenantsCompeteForSharedProcurementAccount(t *testing.T) {
	f := newFixture(t)
	other := abSyncOtherTenant(t, f)
	a := f.create(t, 300)
	in := abSyncBindInput(t, f, a.TargetID)
	bound, err := procurement.New(f.app).BindCredential(f.ctx, f.admin, in)
	must(t, "public shared credential/account binding", err)
	abSyncBinding(t, f, in, bound)
	first, second := f.reserve(t), other.reserve(t)
	firstPool, firstPID := abSyncApp(t, f)
	secondPool, secondPID := abSyncApp(t, f)
	barrier := abSyncPause(t, f, "procurement_reservations")
	winner := abSyncAttempt(t, f, firstPool, first.RequestID)
	abSyncWait(t, f, firstPID, barrier.pid, true)
	loser := abSyncAttempt(t, other, secondPool, second.RequestID)
	abSyncWait(t, f, secondPID, firstPID, false)
	barrier.release(t, f)
	got, err := abSyncAwait(t, f, winner)
	must(t, "first tenant owns the affordable shared hold", err)
	_, err = abSyncAwait(t, f, loser)
	sqlstate(t, err, "PP115")
	abSyncManaged(t, f, a.TargetID, first.RequestID, got)
	var exact bool
	must(t, "independent cross-tenant winning/losing source oracle", f.owner.QueryRow(f.ctx, `SELECT
 $1::text<>$2::text AND $3::text<>$4::text
 AND (SELECT count(*)=1 AND bool_and(tenant_id=$1 AND request_id=$3) FROM upstream_attempts)
 AND (SELECT count(*)=1 AND bool_and(tenant_id=$1 AND request_id=$3 AND account_id=$5) FROM pp_attempt_origins)
 AND NOT EXISTS(SELECT 1 FROM upstream_attempts WHERE tenant_id=$2 OR request_id=$4)
 AND NOT EXISTS(SELECT 1 FROM pp_attempt_origins WHERE tenant_id=$2 OR request_id=$4)
 AND NOT EXISTS(SELECT 1 FROM pp_procurement_transitions WHERE tenant_id=$2 OR request_id=$4)
 AND NOT EXISTS(SELECT 1 FROM pp_operation_roots WHERE entry_kind='attempt_birth' AND (tenant_id=$2 OR request_id=$4))
 AND (SELECT count(*)=2 AND bool_and(status='reserved' AND amount=120) FROM reservations WHERE request_id IN ($3,$4))`, f.tenant, other.tenant, first.RequestID, second.RequestID, a.TargetID).Scan(&exact))
	if !exact {
		t.Fatal("shared-account competition crossed tenant sources or left a partial losing birth")
	}
}
