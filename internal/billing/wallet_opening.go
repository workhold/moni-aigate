package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/security"
)

// WalletOpeningInput is an explicit offline noncash import, not an online
// funding command. The database role and service lock are checked again here.
type WalletOpeningInput struct {
	TenantID, ActorID, ExternalRef string
	Amount                         Money
	MaintenanceMode                bool
}

type WalletOpeningResult struct {
	OpeningID string         `json:"opening_id"`
	Receipt   FundingReceipt `json:"receipt"`
	Replayed  bool           `json:"replayed"`
}

func validWalletOpening(in WalletOpeningInput) bool {
	tenant, te := uuid.Parse(in.TenantID)
	actor, ae := uuid.Parse(in.ActorID)
	return in.MaintenanceMode && te == nil && ae == nil && tenant != uuid.Nil && actor != uuid.Nil &&
		tenant.String() == in.TenantID && actor.String() == in.ActorID && in.Amount > 0 && security.ValidReference(in.ExternalRef, 160)
}

// OpenWallet owns one connection, the same global session lock used by serve,
// and one transaction. It never retries an unknown commit or repairs history.
func (s *Store) OpenWallet(ctx context.Context, in WalletOpeningInput) (result WalletOpeningResult, err error) {
	if ctx == nil || s == nil || s.pool == nil || !validWalletOpening(in) {
		return result, fail("invalid_wallet_opening", "explicit maintenance mode and a valid noncash opening are required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return result, fail("wallet_opening_failed", "wallet opening unavailable")
	}
	var tx pgx.Tx
	locked := false
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if tx != nil {
			_ = tx.Rollback(cleanup)
		}
		if locked && !conn.Conn().IsClosed() {
			var released bool
			if e := conn.QueryRow(cleanup, `SELECT pg_advisory_unlock(782193402)`).Scan(&released); e != nil || !released {
				_ = conn.Conn().Close(cleanup)
			}
		}
		if !conn.Conn().IsClosed() && conn.Conn().PgConn().TxStatus() != 'I' {
			_ = conn.Conn().Close(cleanup)
		}
		conn.Release()
		if err != nil {
			result = WalletOpeningResult{}
		}
	}()
	var owner bool
	if err = conn.QueryRow(ctx, `SELECT current_user=session_user AND current_user::regrole::oid=c.relowner FROM pg_catalog.pg_class c WHERE c.oid='wallet_openings'::regclass`).Scan(&owner); err != nil || !owner {
		return result, fail("wallet_opening_owner_required", "wallet opening requires the migration owner connection")
	}
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(782193402)`).Scan(&locked); err != nil {
		// The server may have granted the session lock before a transport error.
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = conn.Conn().Close(cleanup)
		cancel()
		return result, fail("wallet_opening_failed", "wallet opening ownership could not be confirmed")
	}
	if !locked {
		return result, fail("wallet_opening_service_active", "stop the active service before opening a wallet")
	}
	tx, err = conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, fail("wallet_opening_failed", "wallet opening unavailable")
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('lock_timeout','3s',true),set_config('statement_timeout','5s',true)`); err != nil {
		return result, fail("wallet_opening_failed", "wallet opening unavailable")
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT status='active' FROM tenants WHERE id=$1 FOR SHARE`, in.TenantID).Scan(&active); err != nil || !active {
		return result, fail("wallet_opening_forbidden", "active tenant and administrator required")
	}
	if err = tx.QueryRow(ctx, `SELECT status='active' AND platform_role='admin' FROM users WHERE id=$1 FOR SHARE`, in.ActorID).Scan(&active); err != nil || !active {
		return result, fail("wallet_opening_forbidden", "active tenant and administrator required")
	}
	var id, tenant, actor, reference string
	var amount Money
	err = tx.QueryRow(ctx, `SELECT id,tenant_id,actor,external_ref,amount FROM wallet_openings WHERE tenant_id=$1 OR external_ref=$2`, in.TenantID, in.ExternalRef).Scan(&id, &tenant, &actor, &reference, &amount)
	if err == nil {
		if tenant != in.TenantID || actor != in.ActorID || reference != in.ExternalRef || amount != in.Amount {
			return result, fail("wallet_opening_conflict", "opening reference or wallet already has a different intent")
		}
		funded, e := s.ApplyFundingTx(ctx, tx, FundingSourceRef{Kind: FundingWalletOpening, ID: id}, FundingScope{TenantID: in.TenantID, ActorID: in.ActorID})
		if e != nil || funded.Applied {
			return result, fail("wallet_opening_integrity", "the original opening could not be verified")
		}
		return WalletOpeningResult{OpeningID: id, Receipt: funded.Receipt, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, fail("wallet_opening_failed", "wallet opening unavailable")
	}
	if _, err = lockAccounts(ctx, tx, in.TenantID, "AVAILABLE", "WALLET_OPENING_CLEARING"); err != nil {
		return result, fail("wallet_opening_failed", "wallet opening unavailable")
	}
	var empty bool
	if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM journals WHERE tenant_id=$1)
 AND NOT EXISTS(SELECT 1 FROM funding_receipts WHERE tenant_id=$1)
 AND NOT EXISTS(SELECT 1 FROM request_records WHERE tenant_id=$1)
 AND NOT EXISTS(SELECT 1 FROM reservations WHERE tenant_id=$1)
 AND NOT EXISTS(SELECT 1 FROM ledger_accounts WHERE tenant_id=$1 AND balance<>0)
 AND NOT EXISTS(SELECT 1 FROM api_keys WHERE tenant_id=$1 AND (held<>0 OR spent<>0))`, in.TenantID).Scan(&empty); err != nil {
		return result, fail("wallet_opening_failed", "wallet opening unavailable")
	}
	if !empty {
		return result, fail("wallet_opening_not_empty", "wallet opening requires an unused zero-balance wallet")
	}
	id = uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO wallet_openings(id,tenant_id,external_ref,amount,actor) VALUES($1,$2,$3,$4,$5)`, id, in.TenantID, in.ExternalRef, int64(in.Amount), in.ActorID); err != nil {
		return result, fail("wallet_opening_conflict", "opening reference or wallet already exists")
	}
	funded, e := s.ApplyFundingTx(ctx, tx, FundingSourceRef{Kind: FundingWalletOpening, ID: id}, FundingScope{TenantID: in.TenantID, ActorID: in.ActorID})
	if e != nil || !funded.Applied {
		return result, fail("wallet_opening_failed", "wallet opening could not be applied")
	}
	if err = audit(ctx, tx, in.TenantID, in.ActorID, "wallet.opened", id, "", map[string]any{"amount": in.Amount, "source_kind": "wallet_opening", "refund_class": "nonrefundable"}); err != nil {
		return result, fail("wallet_opening_failed", "wallet opening could not be applied")
	}
	if err = tx.Commit(ctx); err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) || errors.Is(err, pgx.ErrTxCommitRollback) {
			return result, fail("wallet_opening_failed", "wallet opening was not committed")
		}
		// A missing COMMIT reply leaves transaction and session ownership
		// unconfirmed. Destroy this exact connection before returning it to
		// the pool; never rely on a transport error having closed it already.
		cleanup, closeDone := context.WithTimeout(context.Background(), 2*time.Second)
		_ = conn.Conn().Close(cleanup)
		closeDone()
		return result, fail("wallet_opening_commit_unknown", "opening commit is unconfirmed; stop and verify the original external reference before retrying")
	}
	return WalletOpeningResult{OpeningID: id, Receipt: funded.Receipt}, nil
}
