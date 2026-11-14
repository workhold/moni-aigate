package billing

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const WalletOperatorConfirmation = "CONFIRM_SINGLE_OPERATOR_CHANGE"

type WalletOperatorSetting struct {
	OperationID    string    `json:"operation_id"`
	Version        int64     `json:"version,string"`
	SingleOperator bool      `json:"single_operator"`
	Actor          *string   `json:"actor"`
	Reason         string    `json:"reason"`
	CreatedAt      time.Time `json:"created_at"`
}

type WalletOperatorSettingsView struct {
	Current  WalletOperatorSetting   `json:"current"`
	Versions []WalletOperatorSetting `json:"versions"`
}

type WalletOperatorSettingInput struct {
	OperationID      string
	ActorID          string
	ExpectedRevision int64
	SingleOperator   bool
	Reason           string
	Confirmation     string
}

type operatorSettingScanner interface {
	Scan(dest ...any) error
}

func scanWalletOperatorSetting(row operatorSettingScanner) (WalletOperatorSetting, error) {
	var setting WalletOperatorSetting
	err := row.Scan(&setting.OperationID, &setting.Version, &setting.SingleOperator, &setting.Actor, &setting.Reason, &setting.CreatedAt)
	return setting, err
}

func validWalletOperatorReason(reason string) bool {
	if !utf8.ValidString(reason) || len(reason) < 1 || len(reason) > 200 || strings.TrimSpace(reason) != reason {
		return false
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validWalletOperatorOperation(id string) bool {
	value, err := uuid.Parse(id)
	return err == nil && value != uuid.Nil && value.String() == id
}

func (s *Store) WalletOperatorSettings(ctx context.Context, actor string) (WalletOperatorSettingsView, error) {
	var view WalletOperatorSettingsView
	view.Versions = []WalletOperatorSetting{}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if err := role(ctx, tx, actor, "admin"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,version,single_operator,actor,reason,created_at FROM wallet_operator_settings ORDER BY version DESC LIMIT 50`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			setting, err := scanWalletOperatorSetting(rows)
			if err != nil {
				return err
			}
			view.Versions = append(view.Versions, setting)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(view.Versions) == 0 {
			return fail("operator_settings_unavailable", "wallet operator genesis is missing")
		}
		view.Current = view.Versions[0]
		return nil
	})
	return view, err
}

func (s *Store) WalletSingleOperatorEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx, `SELECT wallet_single_operator()`).Scan(&enabled)
	return enabled, err
}

func (s *Store) UpdateWalletOperatorSetting(ctx context.Context, in WalletOperatorSettingInput) (WalletOperatorSetting, error) {
	var result WalletOperatorSetting
	if !validWalletOperatorOperation(in.OperationID) || !validWalletOperatorOperation(in.ActorID) || in.ExpectedRevision <= 0 || !validWalletOperatorReason(in.Reason) || in.Confirmation != WalletOperatorConfirmation {
		return result, fail("invalid_operator_setting", "invalid wallet operator setting command")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if err := role(ctx, tx, in.ActorID, "admin"); err != nil {
			return err
		}
		// The append-only role intentionally has no UPDATE privilege. This service
		// lock serializes CAS writers without weakening the immutable table ACL.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(782193403)`); err != nil {
			return err
		}
		existing, err := scanWalletOperatorSetting(tx.QueryRow(ctx, `SELECT id,version,single_operator,actor,reason,created_at FROM wallet_operator_settings WHERE id=$1`, in.OperationID))
		if err == nil {
			if existing.Actor == nil || *existing.Actor != in.ActorID || existing.Version-1 != in.ExpectedRevision || existing.SingleOperator != in.SingleOperator || existing.Reason != in.Reason {
				return fail("idempotency_conflict", "wallet operator operation was already used")
			}
			result = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var currentVersion int64
		var before bool
		if err = tx.QueryRow(ctx, `SELECT version,single_operator FROM wallet_operator_settings ORDER BY version DESC LIMIT 1`).Scan(&currentVersion, &before); err != nil {
			return err
		}
		if currentVersion != in.ExpectedRevision {
			return fail("revision_conflict", "wallet operator setting changed; review the current version")
		}
		result, err = scanWalletOperatorSetting(tx.QueryRow(ctx, `INSERT INTO wallet_operator_settings(id,version,single_operator,actor,reason) VALUES($1,$2,$3,$4,$5) RETURNING id,version,single_operator,actor,reason,created_at`, in.OperationID, currentVersion+1, in.SingleOperator, in.ActorID, in.Reason))
		if err != nil {
			return err
		}
		return audit(ctx, tx, "", in.ActorID, "wallet.operator_settings.updated", in.OperationID, in.OperationID, map[string]any{
			"before": before, "after": in.SingleOperator, "version": result.Version, "reason": in.Reason,
		})
	})
	return result, err
}
