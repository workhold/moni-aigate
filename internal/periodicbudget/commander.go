package periodicbudget

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Commander configures periodic controls using current database authority and
// immutable original-operation receipts. It owns no provider or HTTP client.
type Commander struct{ pool *pgxpool.Pool }

func NewCommander(pool *pgxpool.Pool) (*Commander, error) {
	if pool == nil {
		return nil, ErrUnavailable
	}
	return &Commander{pool}, nil
}
func (c *Commander) ConfigurePlatformTenant(ctx context.Context, actorID, tenantID string, in PutPeriodicBudget) (ConfigureResult, error) {
	return c.configure(ctx, actorID, commanderTarget{scope: "tenant", id: tenantID}, in)
}
func (c *Commander) ConfigureKey(ctx context.Context, actorID, sessionTenantID, keyID string, in PutPeriodicBudget) (ConfigureResult, error) {
	return c.configure(ctx, actorID, commanderTarget{scope: "key", id: keyID, tenant: sessionTenantID}, in)
}
func (c *Commander) ConfigureProcurement(ctx context.Context, actorID, accountID string, in PutPeriodicBudget) (ConfigureResult, error) {
	return c.configure(ctx, actorID, commanderTarget{scope: "procurement", id: accountID}, in)
}
func (c *Commander) configure(parent context.Context, actor string, target commanderTarget, in PutPeriodicBudget) (ConfigureResult, error) {
	if !ValidUUID(actor) || !target.valid() || in.Validate() != nil {
		return ConfigureResult{}, ErrInvalidRequest
	}
	if c == nil || c.pool == nil || parent == nil {
		return ConfigureResult{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		out, retry, err := c.commanderAttempt(ctx, actor, target, in)
		if err == nil {
			return out, nil
		}
		if !retry || attempt == 2 || ctx.Err() != nil {
			return ConfigureResult{}, err
		}
	}
	return ConfigureResult{}, ErrUnavailable
}

func commanderSQL(err error) error {
	if err == nil {
		return nil
	}
	if ErrorCode(err) != "" {
		return err
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.Code == "23514" && pg.Message == "periodic clock regression" {
			return txCause("periodic_clock_regression", err)
		}
		if pg.Code == "23514" || pg.Code == "23503" || pg.Code == "22003" || pg.Code == "22008" {
			return txCause("periodic_budget_invariant", err)
		}
	}
	return txCause("periodic_budget_unavailable", err)
}
func commanderRetryable(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && (pg.Code == "40P01" || pg.Code == "40001")
}
func commanderIdle(conn *pgxpool.Conn) bool {
	return !conn.Conn().IsClosed() && conn.Conn().PgConn().TxStatus() == 'I'
}
func commanderDestroy(conn *pgxpool.Conn) {
	raw := conn.Hijack()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_ = raw.Close(ctx)
}

// Acquire is explicit: unlike a pgxpool.Tx, this transaction cannot return the
// connection before rollback/COMMIT classification has inspected its state.
func (c *Commander) commanderAttempt(ctx context.Context, actor string, target commanderTarget, in PutPeriodicBudget) (ConfigureResult, bool, error) {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return ConfigureResult{}, false, commanderSQL(err)
	}
	destroy := false
	defer func() {
		if destroy {
			commanderDestroy(conn)
		} else {
			conn.Release()
		}
	}()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		destroy = !commanderIdle(conn)
		return ConfigureResult{}, false, commanderSQL(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('lock_timeout','3s',true),set_config('statement_timeout','5s',true)`); err == nil {
		_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(23023024,hashtext(current_schema()||':'||$1::text))`, in.OperationID)
	}
	var out ConfigureResult
	if err == nil {
		out, err = commanderExecute(ctx, tx, actor, target, in)
	}
	if err != nil {
		clean, stop := context.WithTimeout(context.Background(), 5*time.Second)
		rollback := tx.Rollback(clean)
		stop()
		idle := commanderIdle(conn)
		if rollback != nil || !idle {
			destroy = true
		}
		return ConfigureResult{}, rollback == nil && idle && commanderRetryable(err), commanderSQL(err)
	}
	if err = tx.Commit(ctx); err == nil {
		// A confirmed COMMIT owns the receipt even if this connection should no
		// longer be reused. No connection state is read after Release.
		destroy = !commanderIdle(conn)
		return out, false, nil
	}
	var pg *pgconn.PgError
	knownRollback := commanderIdle(conn) && (errors.As(err, &pg) || errors.Is(err, pgx.ErrTxCommitRollback))
	if knownRollback {
		return ConfigureResult{}, commanderRetryable(err), commanderSQL(err)
	}
	// A timeout, cancellation or transport failure while committing cannot
	// prove whether the server committed. Never replay it or expose a receipt.
	destroy = true
	return ConfigureResult{}, false, txCause("periodic_budget_outcome_unknown", err)
}
