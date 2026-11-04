package procurementexecution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrRunnerUnavailable = errors.New("procurement_execution_unavailable")
	ErrRunnerPanicked    = errors.New("procurement_execution_failed")
)

const runnerSettingsSQL = "SELECT pg_catalog.set_config('lock_timeout','3s',true),pg_catalog.set_config('statement_timeout','5s',true)"

type runnerStamp struct{ issued byte }

// Runner executes only the three fixed Invocation roots. It does not install
// their database definitions, select a serving profile or call any provider.
type Runner struct {
	pool  runnerPool
	stamp *runnerStamp
}

func NewRunner(pool *pgxpool.Pool) (*Runner, error) {
	if pool == nil {
		return nil, ErrRunnerUnavailable
	}
	return &Runner{pool: poolRunner{pool}, stamp: &runnerStamp{issued: 1}}, nil
}

type runnerPool interface {
	acquire(context.Context) (runnerConn, error)
}
type runnerConn interface {
	begin(context.Context, pgx.TxOptions) (runnerTx, error)
	idle() bool
	release()
	destroy(context.Context) error
}
type runnerTx interface {
	exec(context.Context, string) error
	query(context.Context, string, ...any) (Rows, error)
	commit(context.Context) error
	rollback(context.Context) error
}

type poolRunner struct{ pool *pgxpool.Pool }
type poolConnection struct{ conn *pgxpool.Conn }
type poolTransaction struct{ tx pgx.Tx }

func (p poolRunner) acquire(ctx context.Context) (runnerConn, error) {
	c, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return poolConnection{c}, nil
}
func (c poolConnection) begin(ctx context.Context, options pgx.TxOptions) (runnerTx, error) {
	tx, err := c.conn.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return poolTransaction{tx}, nil
}
func (c poolConnection) idle() bool {
	return !c.conn.Conn().IsClosed() && c.conn.Conn().PgConn().TxStatus() == 'I'
}
func (c poolConnection) release() { c.conn.Release() }
func (c poolConnection) destroy(ctx context.Context) error {
	return c.conn.Hijack().Close(ctx)
}
func (t poolTransaction) exec(ctx context.Context, sql string) error {
	_, err := t.tx.Exec(ctx, sql)
	return err
}
func (t poolTransaction) query(ctx context.Context, sql string, args ...any) (Rows, error) {
	return t.tx.Query(ctx, sql, args...)
}
func (t poolTransaction) commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t poolTransaction) rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }

type runReceipt struct {
	attempt AttemptReceipt
	account AccountReceipt
}

// ObservationHandle is issued only for an uncertain COMMIT. Its private data
// is not a mutation result or authorization. A separate observer may consume
// it only with its exact issuing Runner and private nonzero-sized stamp.
type ObservationHandle struct {
	issuer      *Runner
	stamp       *runnerStamp
	schema      string
	invocation  Invocation
	attempt     AttemptReceipt
	account     AccountReceipt
	commitCause error
}

type UnknownOutcome struct {
	cause  error
	handle *ObservationHandle
}

func (e *UnknownOutcome) Error() string { return "procurement_outcome_unknown" }
func (e *UnknownOutcome) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
func (e *UnknownOutcome) PublicCode() string { return "procurement_outcome_unknown" }
func (e *UnknownOutcome) ObservationHandle() *ObservationHandle {
	if e == nil {
		return nil
	}
	return e.handle
}
func (UnknownOutcome) String() string   { return "procurement_outcome_unknown" }
func (UnknownOutcome) GoString() string { return "procurement_outcome_unknown" }
func (UnknownOutcome) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "procurement_outcome_unknown")
}
func (UnknownOutcome) MarshalJSON() ([]byte, error) {
	return []byte(`"procurement_outcome_unknown"`), nil
}
func (UnknownOutcome) MarshalText() ([]byte, error) {
	return []byte("procurement_outcome_unknown"), nil
}
func (ObservationHandle) String() string   { return "procurement_observation_handle" }
func (ObservationHandle) GoString() string { return "procurement_observation_handle" }
func (ObservationHandle) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "procurement_observation_handle")
}
func (ObservationHandle) MarshalJSON() ([]byte, error) {
	return []byte(`"procurement_observation_handle"`), nil
}
func (ObservationHandle) MarshalText() ([]byte, error) {
	return []byte("procurement_observation_handle"), nil
}

func (r *Runner) available() bool {
	return r != nil && r.pool != nil && r.stamp != nil
}

// invocationSchema accepts the exact S1 query template with one canonical
// quoted schema identifier. It never splits on dots inside quoted names.
func invocationSchema(i Invocation) (string, bool) {
	var prototype Invocation
	var count int
	switch i.root {
	case CreateAccount:
		prototype, _ = NewCreateAccount("_", i.account)
		count = 6
	case ConfigureAccount:
		prototype, _ = NewConfigureAccount("_", ConfigurationInput{AccountInput: i.account, AccountID: i.target, ExpectedRevision: i.expected})
		count = 8
	case RecordAttempt:
		prototype, _ = NewRecordAttempt("_", AttemptInput{})
		count = 13
	default:
		return "", false
	}
	parts := strings.SplitN(prototype.query, `"_"`, 2)
	if len(parts) != 2 || len(i.args) != count || !strings.HasPrefix(i.query, parts[0]) || !strings.HasSuffix(i.query, parts[1]) || len(i.query) < len(parts[0])+len(parts[1])+2 {
		return "", false
	}
	quoted := i.query[len(parts[0]) : len(i.query)-len(parts[1])]
	if quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
		return "", false
	}
	schema := strings.ReplaceAll(quoted[1:len(quoted)-1], `""`, `"`)
	if !validSchema(schema) || (pgx.Identifier{schema}).Sanitize() != quoted {
		return "", false
	}
	return schema, true
}

func (r *Runner) RunAttempt(ctx context.Context, i Invocation) (AttemptReceipt, error) {
	if !r.available() {
		return AttemptReceipt{}, ErrRunnerUnavailable
	}
	if ctx == nil || i.root != RecordAttempt {
		return AttemptReceipt{}, ProtocolError(nil)
	}
	out, err := r.run(ctx, i)
	if err != nil {
		return AttemptReceipt{}, err
	}
	return out.attempt, nil
}

func (r *Runner) RunAccount(ctx context.Context, i Invocation) (AccountReceipt, error) {
	if !r.available() {
		return AccountReceipt{}, ErrRunnerUnavailable
	}
	if ctx == nil || (i.root != CreateAccount && i.root != ConfigureAccount) {
		return AccountReceipt{}, ProtocolError(nil)
	}
	out, err := r.run(ctx, i)
	if err != nil {
		return AccountReceipt{}, err
	}
	return out.account, nil
}

func (r *Runner) run(ctx context.Context, i Invocation) (runReceipt, error) {
	schema, ok := invocationSchema(i)
	if !ok {
		return runReceipt{}, ProtocolError(nil)
	}
	limit := 1
	if i.root == RecordAttempt {
		limit = 3
	}
	for attempt := 0; attempt < limit; attempt++ {
		if err := ctx.Err(); err != nil {
			return runReceipt{}, err
		}
		out, retry, err := r.runOnce(ctx, i, schema)
		if err == nil {
			return out, nil
		}
		if !retry || attempt+1 == limit || ctx.Err() != nil {
			var unknown *UnknownOutcome
			if errors.As(err, &unknown) {
				return runReceipt{}, err
			}
			return runReceipt{}, ClassifyPP(i.root, err)
		}
	}
	return runReceipt{}, ErrRunnerUnavailable
}

func retryableExecution(err error) bool {
	var local *Failure
	if errors.As(err, &local) {
		return false
	}
	var pg *pgconn.PgError
	return errors.As(err, &pg) && (pg.Code == "40P01" || pg.Code == "40001")
}

func definiteCommitRollback(conn runnerConn, err error) bool {
	if !safeRunnerIdle(conn) {
		return false
	}
	var pg *pgconn.PgError
	return errors.As(err, &pg) || errors.Is(err, pgx.ErrTxCommitRollback)
}

func safeRunnerIdle(conn runnerConn) (idle bool) {
	defer func() {
		if recover() != nil {
			idle = false
		}
	}()
	return conn.idle()
}

func safeRunnerRollback(tx runnerTx, ctx context.Context) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrRunnerPanicked
		}
	}()
	return tx.rollback(ctx)
}

func safeRunnerDestroy(conn runnerConn, ctx context.Context) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrRunnerPanicked
		}
	}()
	return conn.destroy(ctx)
}

func safeRunnerRelease(conn runnerConn) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrRunnerPanicked
		}
	}()
	conn.release()
	return nil
}

func (r *Runner) unknown(i Invocation, schema string, out runReceipt, cause error) error {
	return &UnknownOutcome{cause: cause, handle: &ObservationHandle{
		issuer: r, stamp: r.stamp, schema: schema, invocation: i,
		attempt: out.attempt, account: out.account, commitCause: cause,
	}}
}

func runnerCleanupCause(primary, cleanup error) error {
	if cleanup == nil {
		return primary
	}
	if unknown, ok := primary.(*UnknownOutcome); ok {
		// Keep the fixed safe top-level error text even when Close fails.
		unknown.cause = combine(unknown.cause, cleanup)
		return unknown
	}
	return combine(primary, cleanup)
}

func (r *Runner) runOnce(ctx context.Context, i Invocation, schema string) (out runReceipt, retry bool, err error) {
	// The only exported constructor binds a real pool. These private interfaces
	// make client-state-machine tests deterministic without exporting callbacks.
	var conn runnerConn
	var tx runnerTx
	var commitStarted, committed, rolledBack bool
	defer func() {
		panicked := recover() != nil
		if panicked {
			if commitStarted && !committed {
				err = r.unknown(i, schema, out, ErrRunnerPanicked)
			} else if !committed {
				err = ErrRunnerPanicked
			}
		}
		if conn == nil {
			if err != nil {
				out = runReceipt{}
			}
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		primary := err
		destroy := false
		if committed {
			destroy = !safeRunnerIdle(conn)
		} else if commitStarted {
			// An uncertain COMMIT cannot be disproved by Rollback/ErrTxClosed.
			destroy = !rolledBack
			retry = rolledBack && retryableExecution(primary) && ctx.Err() == nil
		} else if tx != nil {
			rollbackErr := safeRunnerRollback(tx, cleanup)
			clean := rollbackErr == nil && safeRunnerIdle(conn)
			err = combine(err, rollbackErr)
			destroy = !clean
			retry = clean && retryableExecution(primary) && ctx.Err() == nil
		} else {
			destroy = !safeRunnerIdle(conn)
		}
		if destroy {
			closeErr := safeRunnerDestroy(conn, cleanup)
			if !committed {
				err = runnerCleanupCause(err, closeErr)
			}
		} else {
			if releaseErr := safeRunnerRelease(conn); releaseErr != nil {
				// Release transfers ownership before external pool callbacks.
				// Even a callback panic does not permit using this Conn again.
				if !committed {
					err = combine(err, releaseErr)
					retry = false
				}
			}
		}
		if err != nil {
			out = runReceipt{}
		}
	}()
	conn, err = r.pool.acquire(ctx)
	if err != nil {
		return out, false, err
	}
	if conn == nil {
		return out, false, ErrRunnerUnavailable
	}
	tx, err = conn.begin(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return out, false, err
	}
	if tx == nil {
		return out, false, ProtocolError(nil)
	}
	if err = tx.exec(ctx, runnerSettingsSQL); err != nil {
		return out, false, err
	}
	var rows Rows
	rows, err = tx.query(ctx, i.Query(), i.Arguments()...)
	if err != nil {
		if rows != nil {
			err = combine(err, finishRows(rows))
		}
		return out, false, err
	}
	if i.root == RecordAttempt {
		out.attempt, err = i.DecodeAttempt(rows)
	} else {
		out.account, err = i.DecodeAccount(rows)
	}
	if err != nil {
		return out, false, err
	}
	if err = ctx.Err(); err != nil {
		return out, false, err
	}
	commitStarted = true
	err = tx.commit(ctx)
	if err == nil {
		committed = true
		return out, false, nil
	}
	if definiteCommitRollback(conn, err) {
		rolledBack = true
		return runReceipt{}, false, err
	}
	err = r.unknown(i, schema, out, err)
	return runReceipt{}, false, err
}

// Compile-time checks cover the safe serializers without exporting payloads.
var (
	_ json.Marshaler = (*UnknownOutcome)(nil)
	_ json.Marshaler = (*ObservationHandle)(nil)
)
