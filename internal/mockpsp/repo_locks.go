package mockpsp

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	lockCommand  int32 = 782193410
	lockQuery    int32 = 782193411
	lockCallback int32 = 782193412
	lockControl  int32 = 782193413
	lockMerchant int32 = 782193414
)

type advisoryGate struct{ namespace, key int32 }
type repoConnection struct {
	r     *Repository
	conn  *pgxpool.Conn
	gates []advisoryGate
	bad   bool
}

func lockKind(namespace int32) string {
	switch namespace {
	case lockCommand:
		return "command"
	case lockQuery:
		return "query"
	case lockCallback:
		return "callback"
	case lockControl:
		return "control-operation"
	case lockMerchant:
		return "merchant"
	default:
		return ""
	}
}

func lockHash(environment, kind, target string) int32 {
	s := strings.Join([]string{"moni-payment-lock-v1", "mock", environment, kind, target}, "\x00")
	h := sha256.Sum256([]byte(s))
	return int32(binary.BigEndian.Uint32(h[:4]))
}

func (r *Repository) connection(ctx context.Context) (*repoConnection, error) {
	if err := r.check(ctx); err != nil {
		return nil, normalizeError(err)
	}
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, normalizeError(err)
	}
	return &repoConnection{r: r, conn: conn}, nil
}

func (r *Repository) acquire(ctx context.Context, namespace int32, target string) (*repoConnection, error) {
	c, err := r.connection(ctx)
	if err != nil {
		return nil, err
	}
	if err = c.gate(ctx, namespace, target); err != nil {
		c.close()
		return nil, normalizeError(err)
	}
	return c, nil
}

func (c *repoConnection) gate(ctx context.Context, namespace int32, target string) error {
	kind := lockKind(namespace)
	if kind == "" || target == "" {
		return fail("invalid_request")
	}
	key := lockHash(c.r.env.ID, kind, target)
	_, err := c.conn.Exec(ctx, `SELECT pg_advisory_lock($1::integer,$2::integer)`, namespace, key)
	if err != nil {
		c.bad = true
		return err
	}
	c.gates = append(c.gates, advisoryGate{namespace, key})
	return nil
}

func (c *repoConnection) tryGate(ctx context.Context, namespace int32, target string) (bool, error) {
	kind := lockKind(namespace)
	if kind == "" || target == "" {
		return false, fail("invalid_request")
	}
	key := lockHash(c.r.env.ID, kind, target)
	var ok bool
	err := c.conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1::integer,$2::integer)`, namespace, key).Scan(&ok)
	if err != nil {
		c.bad = true
		return false, err
	}
	if ok {
		c.gates = append(c.gates, advisoryGate{namespace, key})
	}
	return ok, nil
}

func (c *repoConnection) begin(ctx context.Context) (pgx.Tx, error) {
	if err := c.r.check(ctx); err != nil {
		return nil, err
	}
	return c.conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
}

func (c *repoConnection) close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c.conn.Conn().PgConn().TxStatus() != 'I' {
		c.bad = true
	}
	if !c.bad {
		for i := len(c.gates) - 1; i >= 0; i-- {
			var ok bool
			g := c.gates[i]
			if err := c.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1::integer,$2::integer)`, g.namespace, g.key).Scan(&ok); err != nil || !ok {
				c.bad = true
				break
			}
		}
	}
	if c.bad || c.conn.Conn().IsClosed() {
		_ = c.conn.Hijack().Close(ctx)
	} else {
		c.conn.Release()
	}
	c.conn = nil
	if c.bad {
		return fail("mock_unavailable")
	}
	return nil
}

func (r *Repository) merchantGate(ctx context.Context, tx pgx.Tx, order string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1::integer,$2::integer)`, lockMerchant, lockHash(r.env.ID, "merchant", order))
	return err
}
