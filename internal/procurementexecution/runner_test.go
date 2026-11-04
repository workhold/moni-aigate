package procurementexecution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	runnerActor   = "20000000-0000-0000-0000-000000000001"
	runnerOp      = "20000000-0000-0000-0000-000000000002"
	runnerAccount = "20000000-0000-0000-0000-000000000003"
	runnerRoot    = "20000000-0000-0000-0000-000000000004"
	runnerAttempt = "20000000-0000-0000-0000-000000000005"
	runnerJournal = "20000000-0000-0000-0000-000000000006"
)

type runnerTestRows struct {
	columns       []column
	values        []any
	read, closed  bool
	err, closeErr error
}

func (r *runnerTestRows) FieldDescriptions() []pgconn.FieldDescription {
	out := make([]pgconn.FieldDescription, len(r.columns))
	for n, c := range r.columns {
		out[n] = pgconn.FieldDescription{Name: c.name, DataTypeOID: c.oid}
	}
	return out
}
func (r *runnerTestRows) Next() bool {
	if r.read || r.err != nil {
		return false
	}
	r.read = true
	return true
}
func (r *runnerTestRows) Scan(dest ...any) error {
	if len(dest) != len(r.values) {
		r.err = errors.New("test row length mismatch")
		return r.err
	}
	m := pgtype.NewMap()
	for n, value := range r.values {
		encoded, err := m.Encode(r.columns[n].oid, pgtype.TextFormatCode, value, make([]byte, 0, 32))
		if err == nil {
			err = m.Scan(r.columns[n].oid, pgtype.TextFormatCode, encoded, dest[n])
		}
		if err != nil {
			r.err = err
			return err
		}
	}
	return nil
}
func (r *runnerTestRows) Err() error { return r.err }
func (r *runnerTestRows) Close()     { r.closed = true; r.err = combine(r.err, r.closeErr) }

func runnerAttemptRows(id string) *runnerTestRows {
	return &runnerTestRows{columns: attemptColumns, values: []any{"applied", runnerRoot, id, id, "request-private", int32(1), "prepared", "unknown"}}
}
func runnerAccountRows(configure bool) *runnerTestRows {
	at := time.Date(2026, 11, 4, 9, 0, 1, 1000, time.UTC)
	snapshot, _ := json.Marshal(map[string]string{"id": runnerAccount, "label": "label-private", "provider": "provider-private", "state": "active", "limit_amount": "1000", "held_amount": "0", "spent_amount": "0", "revision": "1", "created_at": at.Format(time.RFC3339Nano)})
	kind := "account_create"
	before := []byte("null")
	if configure {
		kind = "account_configure"
		before = snapshot
	}
	return &runnerTestRows{columns: accountColumns, values: []any{"applied", runnerOp, runnerActor, kind, runnerAccount, "applied", "", before, snapshot, at, runnerRoot, runnerJournal}}
}

type runnerFakePool struct {
	connections []*runnerFakeConn
	acquires    int
	err         error
	panicValue  any
	contexts    []context.Context
}

func (p *runnerFakePool) acquire(ctx context.Context) (runnerConn, error) {
	p.acquires++
	p.contexts = append(p.contexts, ctx)
	if p.panicValue != nil {
		panic(p.panicValue)
	}
	if p.err != nil {
		return nil, p.err
	}
	if len(p.connections) < p.acquires {
		return nil, errors.New("unexpected extra acquire")
	}
	return p.connections[p.acquires-1], nil
}

type runnerFakeConn struct {
	tx                                     *runnerFakeTx
	idleValue                              bool
	begins, releases, destroys             int
	beginErr, destroyErr                   error
	beginPanic, releasePanic, destroyPanic any
	beginContext, destroyContext           context.Context
	options                                pgx.TxOptions
}

func (c *runnerFakeConn) begin(ctx context.Context, options pgx.TxOptions) (runnerTx, error) {
	c.begins++
	c.beginContext = ctx
	c.options = options
	if c.beginPanic != nil {
		panic(c.beginPanic)
	}
	if c.beginErr != nil {
		return nil, c.beginErr
	}
	c.idleValue = false
	if c.tx == nil {
		return nil, nil
	}
	c.tx.conn = c
	return c.tx, nil
}
func (c *runnerFakeConn) idle() bool { return c.idleValue }
func (c *runnerFakeConn) release() {
	c.releases++
	if c.releasePanic != nil {
		panic(c.releasePanic)
	}
}
func (c *runnerFakeConn) destroy(ctx context.Context) error {
	c.destroys++
	c.destroyContext = ctx
	if c.destroyPanic != nil {
		panic(c.destroyPanic)
	}
	return c.destroyErr
}

type runnerFakeTx struct {
	conn                                                  *runnerFakeConn
	rows                                                  Rows
	settingsErr, queryErr, commitErr, rollbackErr         error
	settingsPanic, queryPanic, commitPanic, rollbackPanic any
	commitIdle                                            bool
	settings, queries, commits, rollbacks                 int
	settingsSQL, querySQL                                 string
	queryArgs                                             []any
	contexts                                              []context.Context
	rollbackContext                                       context.Context
	onQuery                                               func()
	onCommit                                              func()
	onRollback                                            func(context.Context)
}

func (t *runnerFakeTx) exec(ctx context.Context, sql string) error {
	t.settings++
	t.contexts = append(t.contexts, ctx)
	t.settingsSQL = sql
	if t.settingsPanic != nil {
		panic(t.settingsPanic)
	}
	return t.settingsErr
}
func (t *runnerFakeTx) query(ctx context.Context, sql string, args ...any) (Rows, error) {
	t.queries++
	t.contexts = append(t.contexts, ctx)
	t.querySQL = sql
	t.queryArgs = args
	if t.onQuery != nil {
		t.onQuery()
	}
	if t.queryPanic != nil {
		panic(t.queryPanic)
	}
	return t.rows, t.queryErr
}
func (t *runnerFakeTx) commit(ctx context.Context) error {
	t.commits++
	t.contexts = append(t.contexts, ctx)
	if t.onCommit != nil {
		t.onCommit()
	}
	if t.commitPanic != nil {
		panic(t.commitPanic)
	}
	t.conn.idleValue = t.commitIdle
	return t.commitErr
}
func (t *runnerFakeTx) rollback(ctx context.Context) error {
	t.rollbacks++
	t.rollbackContext = ctx
	if t.onRollback != nil {
		t.onRollback(ctx)
	}
	if t.rollbackPanic != nil {
		panic(t.rollbackPanic)
	}
	if t.rollbackErr == nil {
		t.conn.idleValue = true
	}
	return t.rollbackErr
}

func fakeRunner(connections ...*runnerFakeConn) (*Runner, *runnerFakePool) {
	p := &runnerFakePool{connections: connections}
	return &Runner{pool: p, stamp: &runnerStamp{issued: 1}}, p
}
func fakeAttemptConn() *runnerFakeConn {
	return &runnerFakeConn{idleValue: true, tx: &runnerFakeTx{rows: runnerAttemptRows(runnerAttempt), commitIdle: true}}
}
func fakeAccountConn(configure bool) *runnerFakeConn {
	return &runnerFakeConn{idleValue: true, tx: &runnerFakeTx{rows: runnerAccountRows(configure), commitIdle: true}}
}
func testAttemptInvocation(t *testing.T, schema string) Invocation {
	t.Helper()
	i, err := NewRecordAttempt(schema, AttemptInput{TenantID: "tenant-private", RequestID: "request-private", Sequence: 1, ChannelID: "channel-private", ChannelVersionID: "version-private", CredentialID: "credential-private", UpstreamModel: "model-private", RouteDecisions: []byte("[]"), CostQuote: &CostQuote{VersionID: "quote-private", Basis: "basis-private"}})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func testAccountInvocation(t *testing.T, configure bool) Invocation {
	t.Helper()
	in := AccountInput{ActorID: runnerActor, OperationID: runnerOp, Label: "label-private", Provider: "provider-private", State: "active", LimitAmount: 1000}
	var i Invocation
	var err error
	if configure {
		i, err = NewConfigureAccount("schema", ConfigurationInput{AccountInput: in, AccountID: runnerAccount, ExpectedRevision: 1})
	} else {
		i, err = NewCreateAccount("schema", in)
	}
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func runnerProtocol(t *testing.T, err error) {
	t.Helper()
	var f *Failure
	if !errors.As(err, &f) || f.PublicCode() != "procurement_execution_protocol" {
		t.Fatalf("want PP903, got %v", err)
	}
}

func TestRunnerFixedRootsUseOneTransactionAndOriginalContext(t *testing.T) {
	for _, root := range []Root{RecordAttempt, CreateAccount, ConfigureAccount} {
		t.Run(string(root), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn := fakeAttemptConn()
			i := testAttemptInvocation(t, `schema.dot"quote`)
			if root != RecordAttempt {
				conn = fakeAccountConn(root == ConfigureAccount)
				i = testAccountInvocation(t, root == ConfigureAccount)
			}
			r, p := fakeRunner(conn)
			if root == RecordAttempt {
				out, err := r.RunAttempt(ctx, i)
				if err != nil || out.AttemptID != runnerAttempt {
					t.Fatal(out, err)
				}
			} else {
				out, err := r.RunAccount(ctx, i)
				if err != nil || out.OperationID != runnerOp {
					t.Fatal(out, err)
				}
			}
			if p.acquires != 1 || conn.begins != 1 || conn.options != (pgx.TxOptions{IsoLevel: pgx.ReadCommitted}) || conn.tx.settings != 1 || conn.tx.queries != 1 || conn.tx.commits != 1 || conn.tx.rollbacks != 0 || conn.releases != 1 || conn.destroys != 0 {
				t.Fatal("wrong transaction flow")
			}
			if conn.tx.settingsSQL != runnerSettingsSQL || conn.tx.querySQL != i.Query() || !reflect.DeepEqual(conn.tx.queryArgs, i.Arguments()) {
				t.Fatal("nonfixed settings/root/args")
			}
			for _, actual := range append(append(p.contexts, conn.beginContext), conn.tx.contexts...) {
				if actual != ctx {
					t.Fatal("business context replaced")
				}
			}
		})
	}
}

func TestRunnerRejectsInvalidInputsBeforeAcquire(t *testing.T) {
	if _, err := NewRunner(nil); err != ErrRunnerUnavailable {
		t.Fatal(err)
	}
	var absent *Runner
	if _, err := absent.RunAttempt(context.Background(), Invocation{}); err != ErrRunnerUnavailable {
		t.Fatal(err)
	}
	r, p := fakeRunner()
	_, err := r.RunAttempt(nil, testAttemptInvocation(t, "s"))
	runnerProtocol(t, err)
	_, err = r.RunAccount(nil, testAccountInvocation(t, false))
	runnerProtocol(t, err)
	_, err = r.RunAttempt(context.Background(), testAccountInvocation(t, false))
	runnerProtocol(t, err)
	_, err = r.RunAccount(context.Background(), testAttemptInvocation(t, "s"))
	runnerProtocol(t, err)
	_, err = r.RunAttempt(context.Background(), Invocation{})
	runnerProtocol(t, err)
	for _, mutate := range []func(*Invocation){func(i *Invocation) { i.query = "SELECT 1" }, func(i *Invocation) { i.args = i.args[:12] }, func(i *Invocation) { i.query = strings.Replace(i.query, `"s"`, `"s"; SELECT 1 --`, 1) }} {
		i := testAttemptInvocation(t, "s")
		mutate(&i)
		_, err = r.RunAttempt(context.Background(), i)
		runnerProtocol(t, err)
	}
	if p.acquires != 0 {
		t.Fatal("invalid invocation acquired a connection")
	}
}

func TestRunnerSchemaParsingPreservesQuotedIdentifiers(t *testing.T) {
	for _, schema := range []string{`a.b`, `quote"name`, "汉字\n", `$$ {{MONI_SCHEMA}}`} {
		i := testAttemptInvocation(t, schema)
		got, ok := invocationSchema(i)
		if !ok || got != schema {
			t.Fatalf("schema changed: %q %v", got, ok)
		}
	}
}

func TestRunnerPrecommitFailuresRollbackAndNeverPublish(t *testing.T) {
	for _, stage := range []string{"settings", "query", "decoder"} {
		t.Run(stage, func(t *testing.T) {
			cause := errors.New("private execution error")
			conn := fakeAttemptConn()
			switch stage {
			case "settings":
				conn.tx.settingsErr = cause
			case "query":
				conn.tx.queryErr = cause
			case "decoder":
				conn.tx.rows.(*runnerTestRows).values[0] = "wrong"
			}
			r, p := fakeRunner(conn)
			out, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "s"))
			if stage == "decoder" {
				runnerProtocol(t, err)
			} else if !errors.Is(err, cause) {
				t.Fatal("cause lost", err)
			}
			if !reflect.DeepEqual(out, AttemptReceipt{}) || p.acquires != 1 || conn.tx.commits != 0 || conn.tx.rollbacks != 1 || conn.releases != 1 || conn.destroys != 0 {
				t.Fatal("failed transaction leaked result/connection")
			}
		})
	}
}

func TestRunnerQueryErrorClosesRowsAndKeepsLateCause(t *testing.T) {
	primary := &pgconn.PgError{Code: "42501", Message: "private primary"}
	late := errors.New("late stream")
	conn := fakeAttemptConn()
	rows := conn.tx.rows.(*runnerTestRows)
	rows.closeErr = late
	conn.tx.queryErr = primary
	r, _ := fakeRunner(conn)
	_, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "s"))
	if !errors.Is(err, primary) || !errors.Is(err, late) || !rows.closed || conn.tx.commits != 0 {
		t.Fatal("query/drain cause lost", err)
	}
}

func TestRunnerCanceledBeforeCommitRollsBackWithoutSendingCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	conn := fakeAttemptConn()
	conn.tx.onQuery = cancel
	r, _ := fakeRunner(conn)
	out, err := r.RunAttempt(ctx, testAttemptInvocation(t, "s"))
	if err != context.Canceled || !reflect.DeepEqual(out, AttemptReceipt{}) || conn.tx.commits != 0 || conn.tx.rollbacks != 1 || conn.releases != 1 {
		t.Fatal("precommit cancellation crossed COMMIT", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	r, p := fakeRunner()
	_, err = r.RunAttempt(ctx, testAttemptInvocation(t, "s"))
	if err != context.Canceled || p.acquires != 0 {
		t.Fatal("canceled caller acquired")
	}
}

func TestRunnerAttemptRetriesOnlyConfirmedDeadlockOrSerialization(t *testing.T) {
	for _, state := range []string{"40P01", "40001"} {
		for _, stage := range []string{"query", "commit"} {
			t.Run(state+"/"+stage, func(t *testing.T) {
				connections := []*runnerFakeConn{fakeAttemptConn(), fakeAttemptConn(), fakeAttemptConn()}
				for _, c := range connections[:2] {
					if stage == "query" {
						c.tx.queryErr = &pgconn.PgError{Code: state}
					} else {
						c.tx.commitErr = &pgconn.PgError{Code: state}
						c.tx.commitIdle = true
					}
				}
				r, p := fakeRunner(connections...)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				out, err := r.RunAttempt(ctx, testAttemptInvocation(t, "s"))
				if err != nil || out.AttemptID != runnerAttempt || p.acquires != 3 {
					t.Fatal("exact retry budget failed", out, err)
				}
				for _, c := range connections {
					if c.releases != 1 || c.destroys != 0 {
						t.Fatal("retry connection dirty")
					}
					for _, actual := range c.tx.contexts {
						if actual != ctx {
							t.Fatal("retry reset caller context")
						}
					}
				}
			})
		}
	}
}

func TestRunnerAttemptStopsAtThreeAndAccountNeverRetries(t *testing.T) {
	for _, root := range []Root{RecordAttempt, CreateAccount, ConfigureAccount} {
		connections := []*runnerFakeConn{fakeAttemptConn(), fakeAttemptConn(), fakeAttemptConn()}
		cause := &pgconn.PgError{Code: "40P01"}
		for _, c := range connections {
			c.tx.queryErr = cause
		}
		r, p := fakeRunner(connections...)
		if root == RecordAttempt {
			_, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "s"))
			if !errors.Is(err, cause) || p.acquires != 3 {
				t.Fatal("attempt exceeded three", err)
			}
		} else {
			out, err := r.RunAccount(context.Background(), testAccountInvocation(t, root == ConfigureAccount))
			if !errors.Is(err, cause) || p.acquires != 1 || !reflect.DeepEqual(out, AccountReceipt{}) {
				t.Fatal("management retried/published", err)
			}
		}
	}
}

func TestRunnerNoRetryForOtherStatesOrProtocolFailure(t *testing.T) {
	for _, cause := range []error{&pgconn.PgError{Code: "55P03"}, &pgconn.PgError{Code: "57014"}, &pgconn.PgError{Code: "23514"}, &pgconn.PgError{Code: "PP115"}, io.EOF, context.DeadlineExceeded, ProtocolError(&pgconn.PgError{Code: "40P01"})} {
		conn := fakeAttemptConn()
		conn.tx.queryErr = cause
		r, p := fakeRunner(conn)
		out, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "s"))
		if err == nil || p.acquires != 1 || !reflect.DeepEqual(out, AttemptReceipt{}) {
			t.Fatal("nonretry error retried/published")
		}
		var pg *pgconn.PgError
		if errors.As(cause, &pg) && pg.Code == "PP115" {
			var f *Failure
			if !errors.As(err, &f) || f.PublicCode() != "procurement_budget_exceeded" {
				t.Fatal("PP mapping omitted", err)
			}
		} else if !errors.Is(err, cause) {
			t.Fatal("execution cause lost", err)
		}
	}
}

func TestRunnerCleanupSharesOneIndependentAbsoluteBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	primary := &pgconn.PgError{Code: "40P01"}
	cleanupError := errors.New("rollback failed")
	conn := fakeAttemptConn()
	conn.tx.queryErr = primary
	conn.tx.rollbackErr = cleanupError
	conn.tx.onRollback = func(clean context.Context) {
		cancel()
		if clean.Err() != nil {
			t.Fatal("cleanup inherited caller cancellation")
		}
	}
	r, p := fakeRunner(conn)
	_, err := r.RunAttempt(ctx, testAttemptInvocation(t, "s"))
	if !errors.Is(err, primary) || !errors.Is(err, cleanupError) || p.acquires != 1 || conn.releases != 0 || conn.destroys != 1 {
		t.Fatal("failed cleanup retried/released", err)
	}
	if conn.tx.rollbackContext != conn.destroyContext {
		t.Fatal("rollback and close got separate budgets")
	}
	deadline, ok := conn.destroyContext.Deadline()
	if !ok || deadline.After(time.Now().Add(5*time.Second)) {
		t.Fatal("unbounded cleanup")
	}
	// A cleanup-only deadlock must not promote an ordinary execution failure.
	conn = fakeAttemptConn()
	conn.tx.queryErr = io.EOF
	conn.tx.rollbackErr = &pgconn.PgError{Code: "40001"}
	r, p = fakeRunner(conn)
	_, _ = r.RunAttempt(context.Background(), testAttemptInvocation(t, "s"))
	if p.acquires != 1 {
		t.Fatal("cleanup error drove retry")
	}
}

func TestRunnerUnknownCommitNeverRetriesPublishesOrRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		idle  bool
	}{
		{"eof", io.EOF, false}, {"cancel", context.Canceled, true}, {"deadline", context.DeadlineExceeded, false}, {"txclosed", pgx.ErrTxClosed, true}, {"pg_nonidle", &pgconn.PgError{Code: "40P01"}, false}, {"pp_nonidle", &pgconn.PgError{Code: "PP115"}, false}, {"commitrollback_nonidle", pgx.ErrTxCommitRollback, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := fakeAttemptConn()
			conn.tx.commitErr = tc.cause
			conn.tx.commitIdle = tc.idle
			r, p := fakeRunner(conn)
			i := testAttemptInvocation(t, `dot."name`)
			out, err := r.RunAttempt(context.Background(), i)
			var unknown *UnknownOutcome
			var failure *Failure
			if !errors.As(err, &unknown) || errors.As(err, &failure) || !errors.Is(err, tc.cause) || !reflect.DeepEqual(out, AttemptReceipt{}) || p.acquires != 1 || conn.tx.rollbacks != 0 || conn.destroys != 1 || conn.releases != 0 {
				t.Fatal("uncertain commit became usable/replayed", err)
			}
			h := unknown.ObservationHandle()
			if h == nil || h.issuer != r || h.stamp != r.stamp || h.schema != `dot."name` || h.attempt.AttemptID != runnerAttempt || h.invocation.query != i.query || h.commitCause != tc.cause {
				t.Fatal("opaque evidence incomplete")
			}
		})
	}
}

func TestRunnerKnownCommitFailuresReturnZeroAndConfirmedSuccessSurvivesDestroy(t *testing.T) {
	for _, cause := range []error{&pgconn.PgError{Code: "23514", Message: "deferred invariant"}, pgx.ErrTxCommitRollback} {
		conn := fakeAttemptConn()
		conn.tx.commitErr = cause
		conn.tx.commitIdle = true
		r, _ := fakeRunner(conn)
		out, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "s"))
		var unknown *UnknownOutcome
		if !errors.Is(err, cause) || errors.As(err, &unknown) || !reflect.DeepEqual(out, AttemptReceipt{}) || conn.releases != 1 || conn.destroys != 0 {
			t.Fatal("definite commit failure misclassified", err)
		}
	}
	conn := fakeAttemptConn()
	conn.tx.commitIdle = false
	conn.destroyErr = io.EOF
	r, _ := fakeRunner(conn)
	out, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "s"))
	if err != nil || out.AttemptID != runnerAttempt || conn.destroys != 1 || conn.releases != 0 {
		t.Fatal("confirmed success became unknown", err)
	}
}

func TestRunnerBeginAcquireAndPanicCleanup(t *testing.T) {
	r, p := fakeRunner()
	p.err = io.EOF
	if _, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "s")); err != io.EOF {
		t.Fatal(err)
	}
	for _, idle := range []bool{true, false} {
		conn := fakeAttemptConn()
		conn.beginErr = io.EOF
		conn.idleValue = idle
		r, _ := fakeRunner(conn)
		_, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "s"))
		if err != io.EOF || idle != (conn.releases == 1) || idle == (conn.destroys == 1) {
			t.Fatal("Begin connection cleanup failed")
		}
	}
	for _, stage := range []string{"acquire", "begin", "settings", "query", "commit", "rollback"} {
		t.Run(stage, func(t *testing.T) {
			conn := fakeAttemptConn()
			r, p := fakeRunner(conn)
			switch stage {
			case "acquire":
				p.panicValue = "private panic"
			case "begin":
				conn.beginPanic = "private panic"
			case "settings":
				conn.tx.settingsPanic = "private panic"
			case "query":
				conn.tx.queryPanic = "private panic"
			case "commit":
				conn.tx.commitPanic = "private panic"
			case "rollback":
				conn.tx.queryErr = io.EOF
				conn.tx.rollbackPanic = "private panic"
			}
			out, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "s"))
			if err == nil || !reflect.DeepEqual(out, AttemptReceipt{}) || strings.Contains(err.Error(), "private") {
				t.Fatal("panic leaked", err)
			}
			if stage == "commit" {
				var unknown *UnknownOutcome
				if !errors.As(err, &unknown) || conn.destroys != 1 || conn.tx.rollbacks != 0 {
					t.Fatal("commit panic not unknown", err)
				}
			} else if stage == "settings" || stage == "query" {
				if !errors.Is(err, ErrRunnerPanicked) || conn.tx.rollbacks != 1 || conn.releases != 1 {
					t.Fatal("precommit panic not cleaned", err)
				}
			}
		})
	}
}

func TestRunnerOpaqueHandleAndUnknownFormattingNeverExposeEvidence(t *testing.T) {
	conn := fakeAttemptConn()
	conn.tx.commitErr = errors.New("secret-cause-private")
	conn.destroyErr = errors.New("secret-close-private")
	r, _ := fakeRunner(conn)
	_, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "schema-private"))
	var unknown *UnknownOutcome
	if !errors.As(err, &unknown) {
		t.Fatal(err)
	}
	if err.Error() != "procurement_outcome_unknown" || !errors.Is(err, conn.destroyErr) {
		t.Fatal("close error escaped safe unknown classification", err)
	}
	h := unknown.ObservationHandle()
	for _, value := range []any{unknown, *unknown, h, *h, []any{unknown, h}, struct {
		Unknown *UnknownOutcome
		Handle  *ObservationHandle
	}{unknown, h}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			text := fmt.Sprintf(format, value)
			for _, secret := range []string{"private", runnerAttempt, runnerRoot} {
				if strings.Contains(text, secret) {
					t.Fatalf("format leaked %s: %s", secret, text)
				}
			}
		}
		b, e := json.Marshal(value)
		if e != nil || strings.Contains(string(b), "private") || strings.Contains(string(b), runnerAttempt) {
			t.Fatal("JSON leaked evidence", string(b), e)
		}
	}
	if h.issuer != r || h.stamp == nil || (&ObservationHandle{}).stamp != nil {
		t.Fatal("missing private issuance")
	}
}

func TestRunnerRetriesDoNotReusePriorDecodedIdentity(t *testing.T) {
	first, second := fakeAttemptConn(), fakeAttemptConn()
	first.tx.commitErr = &pgconn.PgError{Code: "40001"}
	first.tx.commitIdle = true
	secondID := "20000000-0000-0000-0000-000000000099"
	second.tx.rows = runnerAttemptRows(secondID)
	second.tx.commitErr = io.EOF
	second.tx.commitIdle = false
	r, p := fakeRunner(first, second)
	_, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "s"))
	var unknown *UnknownOutcome
	if !errors.As(err, &unknown) || p.acquires != 2 || unknown.handle.attempt.AttemptID != secondID {
		t.Fatal("failed attempt identity retained", err)
	}
}

func TestRunnerReleasePanicNeverTouchesTransferredConnection(t *testing.T) {
	for _, committed := range []bool{false, true} {
		conn := fakeAttemptConn()
		conn.releasePanic = "private external release tracer panic"
		var cause error
		if !committed {
			cause = &pgconn.PgError{Code: "40P01"}
			conn.tx.queryErr = cause
		}
		r, p := fakeRunner(conn)
		out, err := r.RunAttempt(context.Background(), testAttemptInvocation(t, "s"))
		if conn.releases != 1 || conn.destroys != 0 || p.acquires != 1 {
			t.Fatal("used or retried a transferred connection")
		}
		if committed {
			if err != nil || out.AttemptID != runnerAttempt {
				t.Fatal("acknowledged success overwritten by external callback panic", err)
			}
		} else if !errors.Is(err, cause) || !errors.Is(err, ErrRunnerPanicked) || !reflect.DeepEqual(out, AttemptReceipt{}) || strings.Contains(err.Error(), "private") {
			t.Fatal("release panic leaked/dropped prior failure", err)
		}
	}
}
