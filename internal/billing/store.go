package billing

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func (s *Store) transaction(ctx context.Context, fn func(pgx.Tx) error) error {
	for attempt := 0; ; attempt++ {
		err := s.transactionOnce(ctx, fn)
		var databaseError *pgconn.PgError
		if err == nil || attempt >= 2 || ctx.Err() != nil || !errors.As(err, &databaseError) || !oneOf(databaseError.Code, "40P01", "40001") {
			return err
		}
	}
}

func (s *Store) transactionOnce(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, "SELECT set_config('lock_timeout','3s',true),set_config('statement_timeout','5s',true)"); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// closeBatch preserves scan/domain errors while also surfacing any error in
// later queued statements. Closing always drains the batch before more SQL or
// transaction cleanup can use the connection.
func closeBatch(results pgx.BatchResults, prior error) error {
	closeErr := results.Close()
	if closeErr != nil {
		// pgx v5.7.6 can return a last Rows scan error before closing its
		// pipeline on the first Close. A second Close finishes that cleanup;
		// the API permits repeated closes and never reruns callbacks.
		if drainErr := results.Close(); drainErr != nil && !errors.Is(closeErr, drainErr) {
			closeErr = errors.Join(closeErr, drainErr)
		}
	}
	if prior == nil {
		return closeErr
	}
	if closeErr == nil || errors.Is(prior, closeErr) {
		return prior
	}
	return errors.Join(prior, closeErr)
}

func rawAmount(input, output int64, inputRate, outputRate Money) (*big.Int, error) {
	if input < 0 || output < 0 || inputRate < 0 || outputRate < 0 {
		return nil, fail("invalid_amount", "quantities and prices must be nonnegative")
	}
	n := new(big.Int).Mul(big.NewInt(input), big.NewInt(int64(inputRate)))
	n.Add(n, new(big.Int).Mul(big.NewInt(output), big.NewInt(int64(outputRate))))
	n.Add(n, big.NewInt(999999))
	return n.Div(n, big.NewInt(1000000)), nil
}

func Calculate(input, output int64, inputRate, outputRate Money) (Money, error) {
	n, err := rawAmount(input, output, inputRate, outputRate)
	if err != nil {
		return 0, err
	}
	if !n.IsInt64() {
		return 0, fail("amount_overflow", "amount exceeds supported range")
	}
	return Money(n.Int64()), nil
}

func checkedAdd(a, b int64) (int64, error) {
	n := new(big.Int).Add(big.NewInt(a), big.NewInt(b))
	if !n.IsInt64() {
		return 0, fail("amount_overflow", "balance exceeds supported range")
	}
	return n.Int64(), nil
}

func validateUsage(u Usage) error {
	switch u.Status {
	case "confirmed_final", "provisional", "estimated", "missing", "conflicting":
	default:
		return fail("invalid_usage", "unknown usage status")
	}
	if u.TotalTokens < 0 || u.InputTokens < 0 || u.OutputTokens < 0 || u.CachedTokens < 0 || u.ReasoningTokens < 0 || u.CachedTokens > u.InputTokens || u.ReasoningTokens > u.OutputTokens {
		return fail("invalid_usage", "invalid usage relationships")
	}
	if u.Status == "confirmed_final" && new(big.Int).Add(big.NewInt(u.InputTokens), big.NewInt(u.OutputTokens)).Cmp(big.NewInt(u.TotalTokens)) != 0 {
		return fail("invalid_usage", "final usage total must equal input plus output")
	}
	return nil
}

func validID(id string) bool               { _, err := uuid.Parse(id); return err == nil }
func boundedString(s string, max int) bool { return len(s) > 0 && len(s) <= max }
func member(a []string, value string) bool {
	for _, v := range a {
		if v == "*" || v == value {
			return true
		}
	}
	return false
}

func role(ctx context.Context, tx pgx.Tx, actor, required string) error {
	var actual, status string
	err := tx.QueryRow(ctx, "SELECT platform_role,status FROM users WHERE id=$1 FOR SHARE", actor).Scan(&actual, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return fail("forbidden", "financial role required")
	}
	if err != nil {
		return err
	}
	if status != "active" || actual != required {
		return fail("forbidden", "financial role required")
	}
	return nil
}

func walletRole(ctx context.Context, tx pgx.Tx, actor, required string) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT wallet_operator_role_valid($1,$2)`, actor, required).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fail("forbidden", "financial role required")
	}
	return nil
}

func tenantOwner(ctx context.Context, tx pgx.Tx, tenant, actor string) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants t JOIN memberships m ON m.tenant_id=t.id AND m.user_id=$2 JOIN users u ON u.id=m.user_id WHERE t.id=$1 AND t.status='active' AND m.status='active' AND m.role='owner' AND u.status='active')`, tenant, actor).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fail("forbidden", "tenant owner required")
	}
	return nil
}

func walletSingleOperator(ctx context.Context, tx pgx.Tx) (bool, error) {
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT wallet_single_operator()`).Scan(&enabled)
	return enabled, err
}

const insertAuditSQL = "INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff,request_id) VALUES($1,$2,$3,$4,$5,$6)"
const insertOutboxSQL = "INSERT INTO projection_outbox(tenant_id,operation_id,kind,sanitized_payload) VALUES($1,$2,$3,$4)"

func audit(ctx context.Context, tx pgx.Tx, tenant, actor, action, target, request string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, insertAuditSQL, actor, tenant, action, target, b, request)
	return err
}

func emit(ctx context.Context, tx pgx.Tx, tenant, operation, kind string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, insertOutboxSQL, tenant, operation, kind, b)
	return err
}

type lockedAccount struct {
	id, kind string
	balance  int64
}

func lockAccounts(ctx context.Context, tx pgx.Tx, tenant string, required ...string) (accounts []lockedAccount, err error) {
	if len(required) == 0 {
		required = []string{"AVAILABLE", "HELD", "REFUND_HELD", "FUNDING_CLEARING", "REVENUE", "OPERATING_GRANT_CLEARING"}
	}
	kinds := []string{}
	seen := map[string]bool{}
	for _, kind := range required {
		if !oneOf(kind, "AVAILABLE", "HELD", "REFUND_HELD", "FUNDING_CLEARING", "REVENUE", "OPERATING_GRANT_CLEARING", "SANDBOX_FUNDING_CLEARING", "WALLET_OPENING_CLEARING") {
			return nil, fail("invalid_account", "unknown account kind")
		}
		if !seen[kind] {
			seen[kind] = true
			kinds = append(kinds, kind)
		}
	}
	// Keep initialization and locking as separate ordered statements: after an
	// initialization conflict waits, READ COMMITTED gives the lock query a new
	// snapshot. Every selected account is locked in the global ID order.
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO ledger_accounts(tenant_id,kind)
 SELECT CASE WHEN k.kind IN ('AVAILABLE','HELD','REFUND_HELD') THEN $1::text ELSE NULL END,k.kind
 FROM unnest($2::text[]) AS k(kind) ON CONFLICT DO NOTHING`, tenant, kinds)
	batch.Queue("SELECT id,kind,balance FROM ledger_accounts WHERE kind=ANY($2::text[]) AND (tenant_id=$1 OR tenant_id IS NULL) ORDER BY id FOR UPDATE", tenant, kinds)
	results := tx.SendBatch(ctx, batch)
	defer func() { err = closeBatch(results, err) }()
	if _, err = results.Exec(); err != nil {
		return nil, err
	}
	rows, err := results.Query()
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a lockedAccount
		if err = rows.Scan(&a.id, &a.kind, &a.balance); err != nil {
			rows.Close()
			return nil, err
		}
		accounts = append(accounts, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(accounts) != len(kinds) {
		return nil, fail("missing_account", "ledger accounts are incomplete")
	}
	return accounts, nil
}

func post(ctx context.Context, tx pgx.Tx, tenant, operation, kind, reference, actor string, deltas map[string]int64) (string, error) {
	kinds := make([]string, 0, len(deltas))
	for kind := range deltas {
		kinds = append(kinds, kind)
	}
	accounts, err := lockAccounts(ctx, tx, tenant, kinds...)
	if err != nil {
		return "", err
	}

	return postLocked(ctx, tx, tenant, operation, kind, reference, actor, deltas, accounts)
}

func postLocked(ctx context.Context, tx pgx.Tx, tenant, operation, kind, reference, actor string, deltas map[string]int64, accounts []lockedAccount) (string, error) {
	id := uuid.NewString()
	plan, err := buildJournalPlan(id, tenant, operation, kind, reference, actor, deltas, accounts)
	if err != nil {
		return "", err
	}
	batch := &pgx.Batch{}
	batch.Queue("INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$3,$4,$5,$6)", plan.id, plan.tenant, plan.operation, plan.kind, plan.reference, plan.actor)
	for _, account := range plan.accounts {
		batch.Queue("UPDATE ledger_accounts SET balance=$2 WHERE id=$1", account.id, account.balance)
		batch.Queue("INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,$3)", plan.id, account.id, account.delta)
	}
	// Validate every locked balance before sending. PostgreSQL still executes the
	// statements in order in this transaction; Close drains every result so an
	// error in any account update or line insert aborts the whole operation.
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return "", err
	}
	return plan.id, nil
}

type authState struct {
	RouteRevision             int64
	TenantRegions, KeyRegions []string
	Spent, Held               int64
	Limit                     *int64
	KeyExpiresAt              *time.Time
	ProfileVersion            string
	Profile                   json.RawMessage
	PriceGroupID              *string
	PricingRevision           int64
}

func lockPrincipal(ctx context.Context, tx pgx.Tx, p Principal, model string, keyWrite bool) (authState, error) {
	var state authState
	var all bool
	var scopes []string
	lock := "FOR SHARE"
	if keyWrite {
		lock = "FOR UPDATE"
	}
	// These remain separate locking statements in the original global order.
	// The closure drains the complete batch even when an early parent denies
	// access; authorization below is sent only after every parent lock is held.
	err := func() (err error) {
		batch := &pgx.Batch{}
		batch.Queue("SELECT status,allowed_regions,price_group_id,pricing_revision FROM tenants WHERE id=$1 FOR SHARE", p.TenantID)
		batch.Queue("SELECT status,allow_all_models FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR SHARE", p.TenantID, p.UserID)
		batch.Queue("SELECT status FROM users WHERE id=$1 FOR SHARE", p.UserID)
		batch.Queue("SELECT owner_id,scopes,allowed_regions,spent,held,lifetime_limit,revoked_at IS NOT NULL,expires_at,allowed_client_cidrs FROM api_keys WHERE tenant_id=$1 AND id=$2 "+lock, p.TenantID, p.KeyID)
		batch.Queue("SELECT enabled,profile_version,meter_model_profile(id),active_revision FROM models WHERE id=$1 FOR SHARE", model)
		results := tx.SendBatch(ctx, batch)
		defer func() { err = closeBatch(results, err) }()
		var status string
		if err = results.QueryRow().Scan(&status, &state.TenantRegions, &state.PriceGroupID, &state.PricingRevision); err != nil {
			return err
		}
		if status != "active" {
			return fail("forbidden", "tenant is not active")
		}
		if err = results.QueryRow().Scan(&status, &all); err != nil {
			return err
		}
		if status != "active" {
			return fail("forbidden", "membership is not active")
		}
		if err = results.QueryRow().Scan(&status); err != nil {
			return err
		}
		if status != "active" {
			return fail("forbidden", "user is not active")
		}
		var owner string
		var revoked bool
		var clientCIDRs []string
		if err = results.QueryRow().Scan(&owner, &scopes, &state.KeyRegions, &state.Spent, &state.Held, &state.Limit, &revoked, &state.KeyExpiresAt, &clientCIDRs); err != nil {
			return err
		}
		if owner != p.UserID || revoked {
			return fail("invalid_key", "key is not active")
		}
		if !security.ClientIPAllowed(p.ClientIP, clientCIDRs) {
			return fail("key_ip_forbidden", "request source is not authorized")
		}
		var enabled bool
		if err = results.QueryRow().Scan(&enabled, &state.ProfileVersion, &state.Profile, &state.RouteRevision); err != nil {
			return err
		}
		if !enabled || !member(scopes, model) {
			return fail("forbidden", "model is not authorized")
		}
		return nil
	}()
	if err != nil {
		return state, err
	}
	var tenantGrant, memberGrant, blocked bool
	var now time.Time
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM tenant_model_grants WHERE tenant_id=$1 AND model_id=$3),EXISTS(SELECT 1 FROM member_model_grants WHERE tenant_id=$1 AND user_id=$2 AND model_id=$3),EXISTS(SELECT 1 FROM billing_profile_blocks WHERE model_id=$3 AND profile_version=$4),clock_timestamp()", p.TenantID, p.UserID, model, state.ProfileVersion).Scan(&tenantGrant, &memberGrant, &blocked, &now)
	if err != nil {
		return state, err
	}
	// The Key or model locks may have waited across an unchanged expiry.
	// Evaluate the database clock in a new statement after those waits.
	if state.KeyExpiresAt != nil && !state.KeyExpiresAt.After(now) {
		return state, fail("invalid_key", "key is not active")
	}
	if !tenantGrant || (!all && !memberGrant) {
		return state, fail("forbidden", "model is not authorized")
	}
	if blocked {
		return state, fail("profile_blocked", "model billing profile is paused")
	}
	return state, nil
}
