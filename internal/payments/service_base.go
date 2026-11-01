package payments

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const paymentTimeLayout = "2006-01-02T15:04:05.000000Z"

func paymentID(id string) bool {
	u, e := uuid.Parse(id)
	return e == nil && u != uuid.Nil && u.String() == id
}
func paymentText(s string, max int) bool {
	if len(s) < 1 || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func paymentMeta(c CommandMeta, tenant bool) bool {
	return paymentID(c.OperationID) && paymentID(c.ActorID) && ((tenant && paymentID(c.TenantID)) || (!tenant && c.TenantID == ""))
}
func paymentSafeError(err error) error {
	if err == nil {
		return nil
	}
	var d *PaymentError
	if errors.As(err, &d) {
		return d
	}
	for _, e := range []error{ErrPaymentsConfiguration, ErrPaymentsEnvironment, ErrPaymentsLifecycleClosed, ErrPaymentsRecovery, ErrPaymentsGuard, ErrPaymentsInvalidWork, ErrPaymentsLeaseLost, ErrPaymentsIntegrity, ErrPaymentsUnavailable, ErrPaymentsOutcomeUnknown, ErrPaymentsProtocol, ErrPaymentsQueryConflict} {
		if errors.Is(err, e) {
			return e
		}
	}
	return ErrPaymentsUnavailable
}
func paymentRetryable(err error) bool {
	var e *pgconn.PgError
	return errors.As(err, &e) && (e.Code == "40P01" || e.Code == "40001")
}
func (s *Service) writeAllowed(ctx context.Context, newOrder bool) error {
	if e := s.runtime.check(ctx); e != nil {
		return e
	}
	if s.runtime.recovery() {
		return ErrPaymentsRecovery
	}
	if newOrder && (!s.runtime.hasEnvironment() || string(s.runtime.mode()) != "sandbox") {
		return paymentFail("payments_runtime_disabled")
	}
	if !s.runtime.hasEnvironment() {
		return paymentFail("payments_runtime_disabled")
	}
	return nil
}

// Only PostgreSQL's definite rollback outcomes may repeat a transaction. An
// uncertain COMMIT permanently discards its connection and its result.
func (s *Service) transact(ctx context.Context, fn func(context.Context, pgx.Tx) error) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrPaymentsUnavailable
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for n := 0; n < 3; n++ {
		e, retry := func() (error, bool) {
			if e := s.runtime.check(ctx); e != nil {
				return e, false
			}
			conn, e := s.pool.Acquire(ctx)
			if e != nil {
				return e, false
			}
			defer conn.Release()
			tx, e := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
			if e != nil {
				return e, false
			}
			defer paymentRollback(ctx, tx)
			if _, e = tx.Exec(ctx, `SELECT set_config('lock_timeout','3s',true),set_config('statement_timeout','5s',true)`); e != nil {
				return e, paymentRetryable(e)
			}
			if e = fn(ctx, tx); e != nil {
				return e, paymentRetryable(e)
			}
			if e = s.runtime.check(ctx); e != nil {
				return e, false
			}
			e = tx.Commit(ctx)
			if e == nil {
				return nil, false
			}
			var pg *pgconn.PgError
			if errors.As(e, &pg) {
				return e, paymentRetryable(e)
			}
			if errors.Is(e, pgx.ErrTxCommitRollback) {
				return e, false
			}
			paymentCloseUncertain(ctx, tx)
			return ErrPaymentsOutcomeUnknown, false
		}()
		if e == nil {
			return nil
		}
		if !retry || n == 2 || ctx.Err() != nil {
			return paymentSafeError(e)
		}
	}
	return ErrPaymentsUnavailable
}
func paymentReadAuth(ctx context.Context, tx pgx.Tx, r Reader, platform, admin bool) error {
	if !paymentID(r.ActorID) || (!platform && !paymentID(r.TenantID)) || (platform && r.TenantID != "") {
		return paymentFail("invalid_request")
	}
	var allowed bool
	var e error
	if platform {
		roles := []string{"admin", "finance_operator", "finance_approver"}
		if admin {
			roles = []string{"admin"}
		}
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active' AND platform_role=ANY($2::text[]))`, r.ActorID, roles).Scan(&allowed)
	} else {
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN memberships m ON m.user_id=u.id JOIN tenants t ON t.id=m.tenant_id WHERE u.id=$1 AND t.id=$2 AND u.status='active' AND m.status='active' AND t.status='active' AND m.role IN ('owner','billing_viewer'))`, r.ActorID, r.TenantID).Scan(&allowed)
	}
	if e != nil {
		return e
	}
	if !allowed {
		return paymentFail("forbidden")
	}
	return nil
}
func paymentAdminLock(ctx context.Context, tx pgx.Tx, actor string) error {
	var allowed bool
	e := tx.QueryRow(ctx, `SELECT status='active' AND platform_role='admin' FROM users WHERE id=$1 FOR SHARE`, actor).Scan(&allowed)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && !allowed {
		return paymentFail("forbidden")
	}
	return e
}
func paymentOwnerLock(ctx context.Context, tx pgx.Tx, c CommandMeta) error {
	for _, q := range []struct {
		sql  string
		args []any
	}{{`SELECT id FROM tenants WHERE id=$1 FOR SHARE`, []any{c.TenantID}}, {`SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR SHARE`, []any{c.TenantID, c.ActorID}}, {`SELECT id FROM users WHERE id=$1 FOR SHARE`, []any{c.ActorID}}} {
		var id string
		if e := tx.QueryRow(ctx, q.sql, q.args...).Scan(&id); errors.Is(e, pgx.ErrNoRows) {
			return paymentFail("forbidden")
		} else if e != nil {
			return e
		}
	}
	var ok bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants t JOIN memberships m ON m.tenant_id=t.id JOIN users u ON u.id=m.user_id WHERE t.id=$1 AND u.id=$2 AND t.status='active' AND m.status='active' AND u.status='active' AND m.role='owner')`, c.TenantID, c.ActorID).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return paymentFail("forbidden")
	}
	return nil
}
func (s *Service) read(ctx context.Context, r Reader, platform, admin bool, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if e := s.runtime.check(ctx); e != nil {
		return e
	}
	conn, e := s.pool.Acquire(ctx)
	if e != nil {
		return paymentSafeError(e)
	}
	defer conn.Release()
	tx, e := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return paymentSafeError(e)
	}
	defer paymentRollback(ctx, tx)
	if _, e = tx.Exec(ctx, `SELECT set_config('statement_timeout','5s',true)`); e == nil {
		e = paymentReadAuth(ctx, tx, r, platform, admin)
	}
	if e == nil {
		e = fn(ctx, tx)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return paymentFail("not_found")
	}
	if e != nil {
		return paymentSafeError(e)
	}
	if e = s.runtime.check(ctx); e != nil {
		return e
	}
	return paymentSafeError(tx.Commit(ctx))
}
func paymentAudit(ctx context.Context, tx pgx.Tx, c CommandMeta, kind, target string, payload any) error {
	var tenant any
	if c.TenantID != "" {
		tenant = c.TenantID
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff,request_id) VALUES($1,$2,$3,$4,$5,$6)`, c.ActorID, tenant, kind, target, b, c.RequestID)
	return e
}

type paymentCursor struct {
	V         int    `json:"v"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
	Filter    string `json:"filter"`
}

func paymentFilter(scope, kind, filter string) string {
	b, _ := json.Marshal([]string{scope, kind, filter})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func decodePaymentCursor(raw, filter string) (*time.Time, string, error) {
	if raw == "" {
		return nil, "", nil
	}
	bad := func() (*time.Time, string, error) { return nil, "", paymentFail("invalid_cursor") }
	if len(raw) > 1024 {
		return bad()
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil || len(b) > 768 || !utf8.Valid(b) || base64.RawURLEncoding.EncodeToString(b) != raw {
		return bad()
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return bad()
	}
	seen := map[string]bool{}
	for d.More() {
		k, e := d.Token()
		s, ok := k.(string)
		if e != nil || !ok || seen[s] || (s != "v" && s != "created_at" && s != "id" && s != "filter") {
			return bad()
		}
		seen[s] = true
		var v json.RawMessage
		if d.Decode(&v) != nil || string(v) == "null" {
			return bad()
		}
	}
	if t, e = d.Token(); e != nil || t != json.Delim('}') || len(seen) != 4 {
		return bad()
	}
	if _, e = d.Token(); e != io.EOF {
		return bad()
	}
	var c paymentCursor
	if json.Unmarshal(b, &c) != nil || c.V != 1 || c.Filter != filter || !paymentID(c.ID) {
		return bad()
	}
	at, e := time.Parse(paymentTimeLayout, c.CreatedAt)
	if e != nil || at.Year() < 1 || at.Year() > 9999 || at.Format(paymentTimeLayout) != c.CreatedAt {
		return bad()
	}
	return &at, c.ID, nil
}
func encodePaymentCursor(at time.Time, id, filter string) *string {
	b, _ := json.Marshal(paymentCursor{1, at.UTC().Format(paymentTimeLayout), id, filter})
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func paymentRollback(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if e := tx.Rollback(cleanup); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
		_ = tx.Conn().Close(cleanup)
	}
}
func paymentCloseUncertain(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = tx.Conn().Close(cleanup)
}
