package periodicbudget

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Reader observes each authorized resource in a new read-only RR snapshot.
// It does not create a control, window or budget event.
type Reader struct {
	pool   *pgxpool.Pool
	cursor *CursorCodec
}

func NewReader(pool *pgxpool.Pool, pepper []byte) (*Reader, error) {
	if pool == nil {
		return nil, ErrUnavailable
	}
	c, e := NewCursorCodec(pepper)
	if e != nil {
		return nil, e
	}
	return &Reader{pool: pool, cursor: c}, nil
}
func (r *Reader) read(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	if r == nil || r.pool == nil || r.cursor == nil || ctx == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return ErrUnavailable
	}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = tx.Rollback(clean)
	}()
	if _, e = tx.Exec(ctx, `SELECT set_config('lock_timeout','3s',true),set_config('statement_timeout','5s',true)`); e != nil {
		return ErrUnavailable
	}
	if e = fn(ctx, tx); e != nil {
		return readError(e)
	}
	if tx.Commit(ctx) != nil {
		return ErrUnavailable
	}
	return nil
}
func readError(e error) error {
	if e == nil {
		return nil
	}
	if ErrorCode(e) != "" {
		return e
	}
	var pg *pgconn.PgError
	if errors.As(e, &pg) {
		if pg.Code == "23514" && pg.Message == "periodic clock regression" {
			return ErrClockRegression
		}
		if pg.Code == "23514" || pg.Code == "23503" || pg.Code == "22003" || pg.Code == "22008" {
			return ErrInvariant
		}
	}
	return ErrUnavailable
}

type readKind uint8

const (
	readPlatformTenant readKind = iota + 1
	readWallet
	readKey
	readProcurement
)

type readTarget struct {
	kind       readKind
	id, tenant string
}

func (t readTarget) scope() string {
	switch t.kind {
	case readPlatformTenant, readWallet:
		return "tenant"
	case readKey:
		return "key"
	case readProcurement:
		return "procurement"
	}
	return ""
}
func (t readTarget) identity() string {
	if t.kind == readWallet || t.kind == readKey {
		return "tenant:" + t.tenant
	}
	return "platform"
}
func (t readTarget) binding(allocations bool) CursorBinding {
	prefix := ""
	switch t.kind {
	case readPlatformTenant:
		prefix = "platform_tenant"
	case readWallet:
		prefix = "wallet"
	case readKey:
		prefix = "key"
	case readProcurement:
		prefix = "procurement"
	}
	suffix, order := "_windows", "start_at_desc_id_c_desc"
	if allocations {
		suffix, order = "_allocations", "created_at_desc_id_c_desc"
	}
	return CursorBinding{Route: prefix + suffix, Scope: t.scope(), ScopeID: t.id, IdentityScope: t.identity(), Order: order}
}
func (t readTarget) valid() bool {
	return t.scope() != "" && ValidUUID(t.id) && ((t.kind != readWallet && t.kind != readKey) || ValidUUID(t.tenant))
}
func (r *Reader) budget(ctx context.Context, actor string, target readTarget) (PeriodicBudgetView, error) {
	var out PeriodicBudgetView
	if !ValidUUID(actor) || !target.valid() {
		return out, ErrInvalidRequest
	}
	e := r.read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := readAuthorize(ctx, tx, actor, target); e != nil {
			return e
		}
		var raw []byte
		e := tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT pb_now() AS at), control AS
   (SELECT id FROM periodic_budget_controls WHERE scope=$1 AND CASE scope WHEN 'tenant' THEN tenant_id WHEN 'key' THEN key_id ELSE account_id END=$2)
   SELECT CASE WHEN c.id IS NULL THEN jsonb_build_object('scope',$1::text,'scope_id',$2::text,'revision','0','period_kind',NULL,
   'enabled',false,'limit_amount',NULL,'state','unconfigured','effective_from',NULL,'tracking_started_at',NULL,'current_window',NULL,
   'carry_held','0','as_of',pricing_time(i.at)) ELSE pb_view_at(c.id,i.at) END
   FROM instant i LEFT JOIN control c ON true`, target.scope(), target.id).Scan(&raw)
		if e != nil {
			return e
		}
		v, e := readDecodeView(raw)
		if e != nil {
			return e
		}
		if v.Scope != target.scope() || v.ScopeID != target.id {
			return ErrInvariant
		}
		out = v
		return nil
	})
	if e != nil {
		return PeriodicBudgetView{}, e
	}
	return out, nil
}
func (r *Reader) PlatformTenantBudget(c context.Context, a, id string) (PeriodicBudgetView, error) {
	return r.budget(c, a, readTarget{kind: readPlatformTenant, id: id})
}
func (r *Reader) WalletBudget(c context.Context, a, t string) (PeriodicBudgetView, error) {
	return r.budget(c, a, readTarget{kind: readWallet, id: t, tenant: t})
}
func (r *Reader) KeyBudget(c context.Context, a, t, k string) (PeriodicBudgetView, error) {
	return r.budget(c, a, readTarget{kind: readKey, id: k, tenant: t})
}
func (r *Reader) ProcurementBudget(c context.Context, a, id string) (PeriodicBudgetView, error) {
	return r.budget(c, a, readTarget{kind: readProcurement, id: id})
}
