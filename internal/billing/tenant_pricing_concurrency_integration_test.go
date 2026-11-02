package billing

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/operations"
)

// A Store retry must not turn a new lock cycle into a passing barrier test.
// Observe both ordinary statements and pgx's principal-lock batches.
type tenantPricingRollbackTrace struct{ retryable atomic.Int64 }

func (tr *tenantPricingRollbackTrace) observe(err error) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "40P01" || pg.Code == "40001") {
		tr.retryable.Add(1)
	}
}
func (tr *tenantPricingRollbackTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (tr *tenantPricingRollbackTrace) TraceQueryEnd(_ context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	tr.observe(data.Err)
}
func (tr *tenantPricingRollbackTrace) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return ctx
}
func (tr *tenantPricingRollbackTrace) TraceBatchQuery(_ context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	tr.observe(data.Err)
}
func (tr *tenantPricingRollbackTrace) TraceBatchEnd(_ context.Context, _ *pgx.Conn, data pgx.TraceBatchEndData) {
	tr.observe(data.Err)
}

// Keep fixture setup/inspection on its isolated owner, but run every real
// Reserve, pricing command, and financial finish through ProvisionFor's APP.
func tenantPricingApplication(t *testing.T, f *fixture) *pgxpool.Pool {
	t.Helper()
	cfg := f.pool.Config()
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("requires the dedicated moni_test database on port 55439")
	}
	var schema string
	if err := f.pool.QueryRow(f.ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	appRole, projectorRole := "pricing_bill_app_"+suffix, "pricing_bill_proj_"+suffix
	var app *pgxpool.Pool
	trace := &tenantPricingRollbackTrace{}
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		if trace.retryable.Load() != 0 {
			t.Error("automatic retry masked a pricing lock deadlock/serialization rollback", trace.retryable.Load())
		}
		for _, role := range []string{appRole, projectorRole} {
			for _, sql := range []string{`DROP OWNED BY ` + pgx.Identifier{role}.Sanitize(), `DROP ROLE ` + pgx.Identifier{role}.Sanitize()} {
				if _, err := f.pool.Exec(context.Background(), sql); err != nil {
					t.Error(err)
				}
			}
		}
	})
	for _, role := range []string{appRole, projectorRole} {
		if _, err := f.pool.Exec(f.ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` NOLOGIN NOINHERIT NOSUPERUSER`); err != nil {
			t.Fatal(err)
		}
	}
	if err := operations.ProvisionFor(f.ctx, f.pool, schema, appRole, projectorRole); err != nil {
		t.Fatal(err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE `+pgx.Identifier{appRole}.Sanitize())
		return err
	}
	cfg.ConnConfig.Tracer = trace
	var err error
	app, err = pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var current string
	var superuser bool
	if err = app.QueryRow(f.ctx, `SELECT current_user,(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&current, &superuser); err != nil || current != appRole || superuser {
		t.Fatal("not a restricted APP", current, superuser, err)
	}
	f.store = New(app)
	return app
}

func tenantPricingGroup(t *testing.T, f *fixture, c *catalog.Store, label string, in, out int64) (string, string) {
	t.Helper()
	g, err := c.CreatePriceGroup(f.ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: label})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.PublishGroupPrice(f.ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: g.Result.ID, ModelID: f.model, InputRate: in, OutputRate: out, EffectiveAt: retailNow(t, f).Add(-time.Minute).Truncate(time.Microsecond)})
	if err != nil {
		t.Fatal(err)
	}
	return g.Result.ID, p.Result.ID
}
func tenantPricingAssign(t *testing.T, f *fixture, c *catalog.Store, group *string, rev int64) catalog.PricingOperationResult[catalog.AssignmentView] {
	t.Helper()
	v, err := c.AssignTenantPricing(f.ctx, catalog.PricingActor{UserID: f.admin}, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: f.tenant, ExpectedRevision: rev, GroupID: group})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func tenantPricingTx(t *testing.T, f *fixture) pgx.Tx {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}
func tenantPricingExec(t *testing.T, ctx context.Context, tx pgx.Tx, sql string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
}

// The observed PostgreSQL lock graph is the barrier. Ticks only poll that
// graph; elapsed time by itself never advances a test to the next phase.
func tenantPricingWaiter(t *testing.T, ctx context.Context, f *fixture, blocker uint32) (uint32, string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var pid uint32
		var query string
		err := f.pool.QueryRow(ctx, `SELECT pid,query FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)) ORDER BY pid LIMIT 1`, int32(blocker)).Scan(&pid, &query)
		if err == nil {
			return pid, query
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("expected lock edge was not observed", ctx.Err())
		case <-ticker.C:
		}
	}
}

// pg_stat_activity.query can describe another statement in an extended batch
// while pg_blocking_pids already sees the tenant wait. Authenticate this exact
// barrier with the tuple and transaction lock tags instead of activity text.
func tenantPricingWaitTenantShare(t *testing.T, ctx context.Context, f *fixture, blocker uint32) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var pid uint32
		err := f.pool.QueryRow(ctx, `WITH locks AS MATERIALIZED (SELECT * FROM pg_locks)
 SELECT tuple_lock.pid
 FROM tenants t JOIN locks tuple_lock ON tuple_lock.relation=t.tableoid
 AND tuple_lock.database=(SELECT oid FROM pg_database WHERE datname=current_database())
 AND tuple_lock.locktype='tuple' AND tuple_lock.mode='RowShareLock' AND tuple_lock.granted
 AND tuple_lock.page::text||','||tuple_lock.tuple::text=btrim(t.ctid::text,'()')
 JOIN locks waiting ON waiting.pid=tuple_lock.pid AND waiting.locktype='transactionid'
 AND waiting.mode='ShareLock' AND NOT waiting.granted
 JOIN locks holding ON holding.pid=$1::int AND holding.locktype='transactionid'
 AND holding.mode='ExclusiveLock' AND holding.granted AND holding.transactionid=waiting.transactionid
 WHERE t.id=$2 AND $1::int=ANY(pg_blocking_pids(tuple_lock.pid))
 ORDER BY tuple_lock.pid LIMIT 1`, int32(blocker), f.tenant).Scan(&pid)
		if err == nil && pid != 0 {
			return
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("tenant SHARE lock-tag observation", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("expected tenant tuple SHARE and transaction wait edge absent", ctx.Err())
		case <-ticker.C:
		}
	}
}

type tenantPricingReservationResult struct {
	value Reservation
	err   error
}

func tenantPricingReserveAsync(ctx context.Context, f *fixture, input ReserveInput) <-chan tenantPricingReservationResult {
	done := make(chan tenantPricingReservationResult, 1)
	go func() { v, e := f.store.Reserve(ctx, input); done <- tenantPricingReservationResult{v, e} }()
	return done
}
func tenantPricingReservation(t *testing.T, ctx context.Context, f *fixture, done <-chan tenantPricingReservationResult) Reservation {
	t.Helper()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		t.Cleanup(func() { f.store.EndRequest(got.value.RequestID) })
		return got.value
	case <-ctx.Done():
		t.Fatal("Reserve did not complete", ctx.Err())
		return Reservation{}
	}
}

type tenantPricingAssignmentResult struct {
	value catalog.PricingOperationResult[catalog.AssignmentView]
	err   error
}

func tenantPricingAssignment(t *testing.T, ctx context.Context, done <-chan tenantPricingAssignmentResult) catalog.PricingOperationResult[catalog.AssignmentView] {
	t.Helper()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		return got.value
	case <-ctx.Done():
		t.Fatal("assignment did not complete", ctx.Err())
		return catalog.PricingOperationResult[catalog.AssignmentView]{}
	}
}
func tenantPricingSnapshot(t *testing.T, f *fixture, r Reservation, group, price string, revision int64, in, out Money) {
	t.Helper()
	retailAssertSnapshot(t, f, r, price, in, out)
	var storedGroup *string
	var storedRevision int64
	var storedAt time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT pricing_group_id,tenant_pricing_revision,retail_priced_at FROM request_records WHERE id=$1`, r.RequestID).Scan(&storedGroup, &storedRevision, &storedAt); err != nil {
		t.Fatal(err)
	}
	if r.PricingGroupID == nil || *r.PricingGroupID != group || r.TenantPricingRevision != revision || r.RetailPricedAt.IsZero() || storedGroup == nil || *storedGroup != group || storedRevision != revision || !storedAt.Equal(r.RetailPricedAt) {
		t.Fatal("mixed pricing domain/revision/time snapshot", r, storedGroup, storedRevision, storedAt)
	}
}

func TestPostgresTenantPricingWalletBarrierFreezesBackdatedGroupPrice(t *testing.T) {
	f := integration(t)
	app := tenantPricingApplication(t, f)
	c := catalog.New(app, nil)
	group, original := tenantPricingGroup(t, f, c, "wallet snapshot", 2000000, 3000000)
	tenantPricingAssign(t, f, c, &group, 1)
	blocker := tenantPricingTx(t, f)
	tenantPricingExec(t, f.ctx, blocker, `SELECT id FROM ledger_accounts WHERE tenant_id=$1 ORDER BY id FOR UPDATE`, f.tenant)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	done := tenantPricingReserveAsync(ctx, f, f.input())
	tenantPricingWaiter(t, ctx, f, blocker.Conn().PgConn().PID())
	// The newer effective time is already in the past, but its row did not exist
	// at this request's observed cutoff. Rechecking latest would change the bill.
	published, err := c.PublishGroupPrice(ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: group, ModelID: f.model, InputRate: 4000000, OutputRate: 5000000, EffectiveAt: retailNow(t, f).Add(-time.Second).Truncate(time.Microsecond)})
	if err != nil {
		t.Fatal("publication must coexist with held group SHARE", err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := tenantPricingReservation(t, ctx, f, done)
	tenantPricingSnapshot(t, f, r, group, original, 2, 2000000, 3000000)
	if r.Amount != 260 {
		t.Fatal(r)
	}
	later := f.reserve(t)
	defer f.store.EndRequest(later.RequestID)
	tenantPricingSnapshot(t, f, later, group, published.Result.ID, 2, 4000000, 5000000)
	if later.Amount != 500 {
		t.Fatal(later)
	}
	for _, item := range []struct {
		r      Reservation
		charge Money
	}{{r, 35}, {later, 65}} {
		f.final(t, item.r, confirmed(), "completed", "done", "none", false)
		settled, e := f.store.Settle(ctx, SettleInput{TenantID: f.tenant, RequestID: item.r.RequestID})
		if e != nil || settled.ChargedAmount != item.charge {
			t.Fatal("frozen group price was repriced at finish", settled, e)
		}
	}
	f.wallet(t, 900, 0)
	f.check(t)
}

func TestPostgresTenantPricingReserveFirstAssignmentWaitsTenant(t *testing.T) {
	f := integration(t)
	app := tenantPricingApplication(t, f)
	c := catalog.New(app, nil)
	a, pa := tenantPricingGroup(t, f, c, "before", 2000000, 3000000)
	b, pb := tenantPricingGroup(t, f, c, "after", 4000000, 5000000)
	tenantPricingAssign(t, f, c, &a, 1)
	blocker := tenantPricingTx(t, f)
	tenantPricingExec(t, f.ctx, blocker, `SELECT id FROM ledger_accounts WHERE tenant_id=$1 ORDER BY id FOR UPDATE`, f.tenant)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	reserved := tenantPricingReserveAsync(ctx, f, f.input())
	reservePID, _ := tenantPricingWaiter(t, ctx, f, blocker.Conn().PgConn().PID())
	command := catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: f.tenant, ExpectedRevision: 2, GroupID: &b}
	assigned := make(chan tenantPricingAssignmentResult, 1)
	go func() {
		v, e := c.AssignTenantPricing(ctx, catalog.PricingActor{UserID: f.admin}, command)
		assigned <- tenantPricingAssignmentResult{v, e}
	}()
	_, query := tenantPricingWaiter(t, ctx, f, reservePID)
	if !strings.Contains(query, "FROM tenants") || !strings.Contains(query, "FOR NO KEY UPDATE") {
		t.Fatal("assignment did not first wait on tenant NO KEY UPDATE", query)
	}
	// A target-group SHARE taken before the tenant would conflict with this
	// immediate probe. No NOWAIT retry or sleep can hide that reversed order.
	probe, err := app.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = probe.Exec(ctx, `SELECT id FROM price_groups WHERE id=$1 FOR NO KEY UPDATE NOWAIT`, b); err != nil {
		_ = probe.Rollback(ctx)
		t.Fatal("waiting assignment already locked its target group", err)
	}
	if err = probe.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := tenantPricingReservation(t, ctx, f, reserved)
	result := tenantPricingAssignment(t, ctx, assigned)
	tenantPricingSnapshot(t, f, r, a, pa, 2, 2000000, 3000000)
	if result.Result.PriceGroupID == nil || *result.Result.PriceGroupID != b || result.Result.PricingRevision != "3" {
		t.Fatal("assignment returned a mixed result", result)
	}
	later := f.reserve(t)
	defer f.store.EndRequest(later.RequestID)
	tenantPricingSnapshot(t, f, later, b, pb, 3, 4000000, 5000000)
	for _, r := range []Reservation{r, later} {
		if _, err = f.store.Release(ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); err != nil {
			t.Fatal(err)
		}
	}
	tenantPricingAssign(t, f, c, nil, 3)
	receipt, err := c.GetPricingOperation(ctx, f.admin, command.OperationID)
	if err != nil || !reflect.DeepEqual(receipt.Result, &result.Result) || receipt.Operation.InputFingerprint != result.Operation.InputFingerprint {
		t.Fatal("concurrent operation receipt lost its original result", receipt, err)
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresTenantPricingAssignmentFirstReserveSeesCommittedDomain(t *testing.T) {
	f := integration(t)
	app := tenantPricingApplication(t, f)
	c := catalog.New(app, nil)
	a, _ := tenantPricingGroup(t, f, c, "before", 2000000, 3000000)
	b, pb := tenantPricingGroup(t, f, c, "after", 4000000, 5000000)
	tenantPricingAssign(t, f, c, &a, 1)
	actorBlock := tenantPricingTx(t, f)
	tenantPricingExec(t, f.ctx, actorBlock, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.admin)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	assigned := make(chan tenantPricingAssignmentResult, 1)
	go func() {
		v, e := c.AssignTenantPricing(ctx, catalog.PricingActor{UserID: f.admin}, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: f.tenant, ExpectedRevision: 2, GroupID: &b})
		assigned <- tenantPricingAssignmentResult{v, e}
	}()
	assignmentPID, _ := tenantPricingWaiter(t, ctx, f, actorBlock.Conn().PgConn().PID())
	input := f.input()
	// Avoid an earlier idempotency INSERT's FK KEY SHARE: PostgreSQL may
	// skip the heavyweight tuple tag when upgrading its own weaker lock.
	// This case isolates the actual principal SHARE barrier.
	input.IdempotencyKey = ""
	reserved := tenantPricingReserveAsync(ctx, f, input)
	tenantPricingWaitTenantShare(t, ctx, f, assignmentPID)
	if err := actorBlock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := tenantPricingAssignment(t, ctx, assigned)
	r := tenantPricingReservation(t, ctx, f, reserved)
	if result.Result.PricingRevision != "3" {
		t.Fatal(result)
	}
	tenantPricingSnapshot(t, f, r, b, pb, 3, 4000000, 5000000)
	if _, err := f.store.Release(ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); err != nil {
		t.Fatal(err)
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresTenantPricingRetirementDoesNotReturnToWaitingTenant(t *testing.T) {
	f := integration(t)
	app := tenantPricingApplication(t, f)
	c := catalog.New(app, nil)
	group, price := tenantPricingGroup(t, f, c, "retirement", 2000000, 3000000)
	tenantPricingAssign(t, f, c, &group, 1)
	// The actual catalog retirement holds its group NO KEY UPDATE before this
	// disposable trigger pauses it. Reserve then holds tenant SHARE while waiting
	// on that group. Returning from retirement to the tenant would form a cycle.
	if _, err := f.pool.Exec(f.ctx, `CREATE FUNCTION pause_tenant_pricing_retirement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.state='active' AND NEW.state='retired' THEN PERFORM pg_advisory_xact_lock(23072,1); END IF; RETURN NEW; END $$; CREATE TRIGGER zz_pause_tenant_pricing_retirement BEFORE UPDATE ON price_groups FOR EACH ROW EXECUTE FUNCTION pause_tenant_pricing_retirement()`); err != nil {
		t.Fatal(err)
	}
	gate := tenantPricingTx(t, f)
	tenantPricingExec(t, f.ctx, gate, `SELECT pg_advisory_xact_lock(23072,1)`)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	retired := make(chan error, 1)
	go func() {
		_, e := c.UpdatePriceGroup(ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupUpdateCommand{OperationID: uuid.NewString(), GroupID: group, ExpectedRevision: 1, Label: "retired", State: "retired"})
		retired <- e
	}()
	retirementPID, _ := tenantPricingWaiter(t, ctx, f, gate.Conn().PgConn().PID())
	reserved := tenantPricingReserveAsync(ctx, f, f.input())
	_, query := tenantPricingWaiter(t, ctx, f, retirementPID)
	if !strings.Contains(query, "FROM price_groups") || !strings.Contains(query, "FOR SHARE") {
		t.Fatal("Reserve did not reach its assigned-group SHARE", query)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-retired:
		if err != nil {
			t.Fatal("retirement acquired a reverse tenant dependency", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	r := tenantPricingReservation(t, ctx, f, reserved)
	tenantPricingSnapshot(t, f, r, group, price, 2, 2000000, 3000000)
	// Existing assignments remain usable on subsequent calls after retirement.
	later := f.reserve(t)
	defer f.store.EndRequest(later.RequestID)
	tenantPricingSnapshot(t, f, later, group, price, 2, 2000000, 3000000)
	for _, r := range []Reservation{r, later} {
		if _, err := f.store.Release(ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); err != nil {
			t.Fatal(err)
		}
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresTenantPricingHistoricalFinishIgnoresBothGroupLocks(t *testing.T) {
	f := integration(t)
	app := tenantPricingApplication(t, f)
	c := catalog.New(app, nil)
	a, pa := tenantPricingGroup(t, f, c, "original", 2000000, 3000000)
	b, _ := tenantPricingGroup(t, f, c, "current", 4000000, 5000000)
	tenantPricingAssign(t, f, c, &a, 1)
	charged, released := f.reserve(t), f.reserve(t)
	defer f.store.EndRequest(charged.RequestID)
	defer f.store.EndRequest(released.RequestID)
	in := f.input()
	in.RequestDeadline = retailNow(t, f).Add(time.Second)
	recovered, err := f.store.Reserve(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	f.store.EndRequest(recovered.RequestID)
	f.final(t, charged, confirmed(), "completed", "done", "none", false)
	tenantPricingAssign(t, f, c, &b, 2)
	if _, err = c.UpdatePriceGroup(f.ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupUpdateCommand{OperationID: uuid.NewString(), GroupID: a, ExpectedRevision: 1, Label: "retired", State: "retired"}); err != nil {
		t.Fatal(err)
	}
	if _, err = app.Exec(f.ctx, `UPDATE tenants SET status='suspended' WHERE id=$1`, f.tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = app.Exec(f.ctx, `UPDATE memberships SET status='suspended' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.user); err != nil {
		t.Fatal(err)
	}
	groups := tenantPricingTx(t, f)
	tenantPricingExec(t, f.ctx, groups, `SELECT id FROM price_groups WHERE id=ANY($1) ORDER BY id FOR UPDATE`, []string{a, b})
	// This control proves either a legacy or current group SHARE would really
	// wait. Financial lifecycle paths must finish while both blockers stay held.
	for _, group := range []string{a, b} {
		probe, e := app.Begin(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		_, e = probe.Exec(f.ctx, `SELECT id FROM price_groups WHERE id=$1 FOR SHARE NOWAIT`, group)
		var pg *pgconn.PgError
		if !errors.As(e, &pg) || pg.Code != "55P03" {
			_ = probe.Rollback(f.ctx)
			t.Fatal("group blocker did not conflict with SHARE", e)
		}
		_ = probe.Rollback(f.ctx)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 4*time.Second)
	defer cancel()
	settlement, err := f.store.Settle(ctx, SettleInput{TenantID: f.tenant, RequestID: charged.RequestID})
	if err != nil || settlement.ChargedAmount != 35 {
		t.Fatal("settlement consulted a current/retired group", settlement, err)
	}
	replay, err := f.store.Settle(ctx, SettleInput{TenantID: f.tenant, RequestID: charged.RequestID})
	if err != nil || replay.ID != settlement.ID {
		t.Fatal("settlement repeated", replay, err)
	}
	if _, err = f.store.Release(ctx, ReleaseInput{TenantID: f.tenant, RequestID: released.RequestID, Reason: "not_sent"}); err != nil {
		t.Fatal("release consulted a group", err)
	}
	key := uuid.NewString()
	refund, err := f.store.CreateRefund(ctx, RefundInput{TenantID: f.tenant, SettlementID: settlement.ID, ActorID: f.operator, Amount: 5, BusinessRef: key, Reason: "pricing snapshot", IdempotencyKey: key, RequestHMAC: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveRefund(ctx, ApprovalInput{TenantID: f.tenant, OrderID: refund.ID, ActorID: f.approver}); err != nil {
		t.Fatal("refund consulted a group", err)
	}
	// Wait for this exact persisted deadline, never an assumed sleep interval.
	if _, err = f.pool.Exec(ctx, `SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM ($1::timestamptz-clock_timestamp())))+0.001)`, in.RequestDeadline); err != nil {
		t.Fatal(err)
	}
	result, err := f.store.Recover(ctx, RecoverInput{Limit: 10})
	if err != nil || result.Processed != 1 || result.Released != 1 {
		t.Fatal("recovery consulted a group", result, err)
	}
	for _, r := range []Reservation{charged, released, recovered} {
		tenantPricingSnapshot(t, f, r, a, pa, 2, 2000000, 3000000)
	}
	var settlements int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM settlements WHERE request_id=ANY($1)`, []string{charged.RequestID, released.RequestID, recovered.RequestID}).Scan(&settlements); err != nil || settlements != 3 {
		t.Fatal("finish duplicated/lost settlement", settlements, err)
	}
	f.wallet(t, 970, 0)
	f.check(t)
}
