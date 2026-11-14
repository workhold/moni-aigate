package billing

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type WalletRequest struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	Kind          string    `json:"kind"`
	RequestedBy   string    `json:"requested_by"`
	Note          string    `json:"note"`
	State         string    `json:"state"`
	LinkedOrderID *string   `json:"linked_order_id"`
	CreatedAt     time.Time `json:"created_at"`
}

type WalletRequestInput struct {
	IdempotencyKey string
	RequestHMAC    string
	TenantID       string
	ActorID        string
	Kind           string
	Note           string
}

type WalletRequestCloseInput struct {
	IdempotencyKey string
	RequestHMAC    string
	TenantID       string
	RequestID      string
	ActorID        string
	Reason         string
	Platform       bool
}

func validWalletRequestNote(note string) bool {
	if !utf8.ValidString(note) || len(note) < 1 || len(note) > 1000 || strings.TrimSpace(note) != note {
		return false
	}
	for _, r := range note {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func scanWalletRequest(row pgx.Row) (WalletRequest, error) {
	var request WalletRequest
	err := row.Scan(&request.ID, &request.TenantID, &request.Kind, &request.RequestedBy, &request.Note, &request.State, &request.LinkedOrderID, &request.CreatedAt)
	return request, err
}

func walletRequestRow(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (WalletRequest, error) {
	query := `SELECT id,tenant_id,kind,requested_by,note,state,linked_order_id,created_at FROM wallet_requests WHERE tenant_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	return scanWalletRequest(tx.QueryRow(ctx, query, tenant, id))
}

func (s *Store) CreateWalletRequest(ctx context.Context, in WalletRequestInput) (WalletRequest, error) {
	var result WalletRequest
	if !validWalletOperatorOperation(in.TenantID) || !validWalletOperatorOperation(in.ActorID) || (in.Kind != "topup" && in.Kind != "cash_refund") || !validWalletRequestNote(in.Note) || !boundedString(in.IdempotencyKey, 200) || !boundedString(in.RequestHMAC, 256) {
		return result, fail("invalid_wallet_request", "invalid wallet request")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if err := tenantOwner(ctx, tx, in.TenantID, in.ActorID); err != nil {
			return err
		}
		id := uuid.NewString()
		original, err := claimFinancial(ctx, tx, in.TenantID, "wallet_request", in.IdempotencyKey, in.RequestHMAC, id)
		if err != nil {
			return err
		}
		if original != "" {
			result, err = walletRequestRow(ctx, tx, in.TenantID, original, false)
			if err == nil && (result.Kind != in.Kind || result.Note != in.Note || result.RequestedBy != in.ActorID) {
				return fail("idempotency_conflict", "wallet request identity changed")
			}
			return err
		}
		result, err = scanWalletRequest(tx.QueryRow(ctx, `INSERT INTO wallet_requests(id,tenant_id,kind,requested_by,note) VALUES($1,$2,$3,$4,$5) RETURNING id,tenant_id,kind,requested_by,note,state,linked_order_id,created_at`, id, in.TenantID, in.Kind, in.ActorID, in.Note))
		if err != nil {
			return err
		}
		return audit(ctx, tx, in.TenantID, in.ActorID, "wallet.request.created", id, "", map[string]any{"kind": in.Kind})
	})
	return result, err
}

func (s *Store) CloseWalletRequest(ctx context.Context, in WalletRequestCloseInput) (WalletRequest, error) {
	var result WalletRequest
	if !validWalletOperatorOperation(in.TenantID) || !validWalletOperatorOperation(in.ActorID) || !validWalletOperatorOperation(in.RequestID) || !validWalletOperatorReason(in.Reason) || !boundedString(in.IdempotencyKey, 200) || !boundedString(in.RequestHMAC, 256) {
		return result, fail("invalid_wallet_request", "invalid wallet request close")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if in.Platform {
			if err := walletRole(ctx, tx, in.ActorID, "finance_operator"); err != nil {
				return err
			}
		} else if err := tenantOwner(ctx, tx, in.TenantID, in.ActorID); err != nil {
			return err
		}
		original, err := claimFinancial(ctx, tx, in.TenantID, "wallet_request_close", in.IdempotencyKey, in.RequestHMAC, in.RequestID)
		if err != nil {
			return err
		}
		if original != "" {
			if original != in.RequestID {
				return fail("idempotency_conflict", "wallet request close identity changed")
			}
			result, err = walletRequestRow(ctx, tx, in.TenantID, original, false)
			return err
		}
		result, err = walletRequestRow(ctx, tx, in.TenantID, in.RequestID, true)
		if err != nil {
			return err
		}
		if result.State != "open" {
			return fail("request_state_conflict", "only an open wallet request can be closed manually")
		}
		result, err = scanWalletRequest(tx.QueryRow(ctx, `UPDATE wallet_requests SET state='closed' WHERE id=$1 AND state='open' RETURNING id,tenant_id,kind,requested_by,note,state,linked_order_id,created_at`, in.RequestID))
		if err != nil {
			return err
		}
		return audit(ctx, tx, in.TenantID, in.ActorID, "wallet.request.closed", in.RequestID, "", map[string]any{"kind": result.Kind, "reason": in.Reason})
	})
	return result, err
}
