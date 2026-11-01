package mockpsp

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/paymentruntime"
)

type repositoryIdentity struct{ marker byte }

// Repository is only assembled from an attested mock runtime by Server.New.
// It never initializes a database or opens an application financial connection.
type Repository struct {
	pool        *pgxpool.Pool
	env         paymentproto.Environment
	keyring     *paymentproto.Keyring
	activeKeyID string
	keyWindow   func(string) (time.Time, time.Time, bool)
	guard       func(context.Context) error
	identity    *repositoryIdentity
}

func newRepository(rt *paymentruntime.Runtime) (*Repository, error) {
	if rt == nil || rt.Component() != paymentruntime.Mock || rt.Pool() == nil || rt.Keyring() == nil {
		return nil, fail("mock_configuration_invalid")
	}
	return &Repository{pool: rt.Pool(), env: rt.Environment(), keyring: rt.Keyring(),
		activeKeyID: rt.ActiveKeyID(), keyWindow: rt.KeyWindow, guard: rt.Check,
		identity: &repositoryIdentity{marker: 1}}, nil
}

func (r *Repository) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.identity == nil || r.pool == nil || r.guard == nil || r.keyring == nil {
		return fail("mock_unavailable")
	}
	if err := r.guard(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fail("mock_instance_unavailable")
	}
	return nil
}

func (*Repository) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 5*time.Second)
}

func normalizeError(err error) error {
	if err == nil || errors.Is(err, ErrDisconnect) || errors.Is(err, ErrDeliveryDeferred) {
		return err
	}
	return fail(ErrorCode(err))
}

func commitTx(ctx context.Context, tx pgx.Tx) error {
	err := tx.Commit(ctx)
	if err == nil || errors.Is(err, pgx.ErrTxCommitRollback) {
		return err
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && len(pg.Code) >= 2 && pg.Code[:2] != "08" {
		return err // A server rejection is a known rollback, not a lost COMMIT.
	}
	return fail("mock_outcome_unknown")
}

func rollbackTx(tx pgx.Tx) error {
	if tx == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return tx.Rollback(ctx)
}

func retryableTransaction(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && (pg.Code == "40P01" || pg.Code == "40001")
}

func (c *repoConnection) transact(ctx context.Context, fn func(pgx.Tx) error) error {
	for attempt := 0; attempt < 3; attempt++ {
		tx, err := c.begin(ctx)
		if err != nil {
			return normalizeError(err)
		}
		err = fn(tx)
		if err == nil {
			err = commitTx(ctx, tx)
		}
		if err == nil {
			return nil
		}
		cleanupErr := rollbackTx(tx)
		if (cleanupErr != nil && !errors.Is(cleanupErr, pgx.ErrTxClosed)) || c.conn.Conn().IsClosed() || c.conn.Conn().PgConn().TxStatus() != 'I' {
			c.bad = true
			return normalizeError(err)
		}
		if ErrorCode(err) == "mock_outcome_unknown" {
			c.bad = true
		}
		if !retryableTransaction(err) || ctx.Err() != nil || attempt == 2 {
			return normalizeError(err)
		}
	}
	return fail("mock_unavailable")
}

func dbNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now.UTC(), err
}

const repoTimeLayout = "2006-01-02T15:04:05.000000Z"

func timeText(t time.Time) string { return t.UTC().Format(repoTimeLayout) }

func sameEnvironment(a, b paymentproto.Environment) bool {
	return a.ID == b.ID && a.MerchantID == b.MerchantID && a.PayeeLabel == b.PayeeLabel
}
