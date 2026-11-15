package billing

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

const CoinDisplayConfirmation = "CONFIRM_COIN_DISPLAY_CHANGE"

const coinDisplayColumns = `operation_id,version,display_name,fiat_currency,rate_num,rate_den,actor,reason,created_at`

type CoinDisplaySetting struct {
	OperationID  string    `json:"operation_id"`
	Version      int64     `json:"version,string"`
	DisplayName  string    `json:"display_name"`
	FiatCurrency *string   `json:"fiat_currency"`
	RateNum      *string   `json:"rate_num"`
	RateDen      *string   `json:"rate_den"`
	Actor        *string   `json:"actor"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"created_at"`
}

type CoinDisplayPublic struct {
	Version      int64   `json:"version,string"`
	DisplayName  string  `json:"display_name"`
	FiatCurrency *string `json:"fiat_currency"`
	RateNum      *string `json:"rate_num"`
	RateDen      *string `json:"rate_den"`
}

type CoinDisplaySettingsView struct {
	Current  CoinDisplaySetting   `json:"current"`
	Versions []CoinDisplaySetting `json:"versions"`
}

type CoinDisplayInput struct {
	OperationID      string
	ActorID          string
	ExpectedRevision int64
	DisplayName      string
	FiatCurrency     string
	RateNum          int64
	RateDen          int64
	Reason           string
	Confirmation     string
}

func (s CoinDisplaySetting) Public() CoinDisplayPublic {
	return CoinDisplayPublic{Version: s.Version, DisplayName: s.DisplayName, FiatCurrency: s.FiatCurrency, RateNum: s.RateNum, RateDen: s.RateDen}
}

func scanCoinDisplay(row operatorSettingScanner) (CoinDisplaySetting, error) {
	var setting CoinDisplaySetting
	var rateNum, rateDen *int64
	err := row.Scan(&setting.OperationID, &setting.Version, &setting.DisplayName, &setting.FiatCurrency, &rateNum, &rateDen, &setting.Actor, &setting.Reason, &setting.CreatedAt)
	if rateNum != nil {
		value := strconv.FormatInt(*rateNum, 10)
		setting.RateNum = &value
	}
	if rateDen != nil {
		value := strconv.FormatInt(*rateDen, 10)
		setting.RateDen = &value
	}
	return setting, err
}

func validCoinDisplayName(name string) bool {
	return validWalletOperatorReason(name) && len(name) <= 32
}

func validCoinDisplayRate(currency string, numerator, denominator int64) bool {
	if currency == "" {
		return numerator == 0 && denominator == 0
	}
	if currency != "CNY" && currency != "USD" {
		return false
	}
	return numerator > 0 && denominator > 0
}

func nullableCoinDisplayRate(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func coinDisplayIntentMatches(setting CoinDisplaySetting, in CoinDisplayInput) bool {
	currency, numerator, denominator := "", int64(0), int64(0)
	if setting.FiatCurrency != nil {
		currency = *setting.FiatCurrency
	}
	if setting.RateNum != nil {
		numerator, _ = strconv.ParseInt(*setting.RateNum, 10, 64)
	}
	if setting.RateDen != nil {
		denominator, _ = strconv.ParseInt(*setting.RateDen, 10, 64)
	}
	return setting.Actor != nil && *setting.Actor == in.ActorID && setting.Version-1 == in.ExpectedRevision && setting.DisplayName == in.DisplayName && currency == in.FiatCurrency && numerator == in.RateNum && denominator == in.RateDen && setting.Reason == in.Reason
}

func (s *Store) CoinDisplayCurrent(ctx context.Context) (CoinDisplayPublic, error) {
	setting, err := scanCoinDisplay(s.pool.QueryRow(ctx, `SELECT `+coinDisplayColumns+` FROM coin_display_versions ORDER BY version DESC LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return CoinDisplayPublic{}, fail("coin_display_unavailable", "coin display genesis is missing")
	}
	return setting.Public(), err
}

func (s *Store) CoinDisplaySettings(ctx context.Context, actor string) (CoinDisplaySettingsView, error) {
	var view CoinDisplaySettingsView
	view.Versions = []CoinDisplaySetting{}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if err := role(ctx, tx, actor, "admin"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+coinDisplayColumns+` FROM coin_display_versions ORDER BY version DESC LIMIT 50`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			setting, err := scanCoinDisplay(rows)
			if err != nil {
				return err
			}
			view.Versions = append(view.Versions, setting)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(view.Versions) == 0 {
			return fail("coin_display_unavailable", "coin display genesis is missing")
		}
		view.Current = view.Versions[0]
		return nil
	})
	return view, err
}

func (s *Store) CoinDisplayOperation(ctx context.Context, actor, operation string) (CoinDisplaySetting, error) {
	var result CoinDisplaySetting
	if !validWalletOperatorOperation(operation) {
		return result, fail("invalid_coin_display", "invalid coin display operation")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if err := role(ctx, tx, actor, "admin"); err != nil {
			return err
		}
		setting, err := scanCoinDisplay(tx.QueryRow(ctx, `SELECT `+coinDisplayColumns+` FROM coin_display_versions WHERE operation_id=$1`, operation))
		if errors.Is(err, pgx.ErrNoRows) {
			return fail("not_found", "coin display operation not found")
		}
		result = setting
		return err
	})
	return result, err
}

func (s *Store) UpdateCoinDisplay(ctx context.Context, in CoinDisplayInput) (CoinDisplaySetting, error) {
	var result CoinDisplaySetting
	if !validWalletOperatorOperation(in.OperationID) || !validWalletOperatorOperation(in.ActorID) || in.ExpectedRevision <= 0 || !validCoinDisplayName(in.DisplayName) || !validCoinDisplayRate(in.FiatCurrency, in.RateNum, in.RateDen) || !validWalletOperatorReason(in.Reason) || in.Confirmation != CoinDisplayConfirmation {
		return result, fail("invalid_coin_display", "invalid coin display command")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if err := role(ctx, tx, in.ActorID, "admin"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(782193404)`); err != nil {
			return err
		}
		existing, err := scanCoinDisplay(tx.QueryRow(ctx, `SELECT `+coinDisplayColumns+` FROM coin_display_versions WHERE operation_id=$1`, in.OperationID))
		if err == nil {
			if !coinDisplayIntentMatches(existing, in) {
				return fail("idempotency_conflict", "coin display operation was already used")
			}
			result = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		current, err := scanCoinDisplay(tx.QueryRow(ctx, `SELECT `+coinDisplayColumns+` FROM coin_display_versions ORDER BY version DESC LIMIT 1`))
		if err != nil {
			return err
		}
		if current.Version != in.ExpectedRevision {
			return fail("revision_conflict", "coin display changed; review the current version")
		}
		result, err = scanCoinDisplay(tx.QueryRow(ctx, `INSERT INTO coin_display_versions(operation_id,version,display_name,fiat_currency,rate_num,rate_den,actor,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+coinDisplayColumns, in.OperationID, current.Version+1, in.DisplayName, nullableText(in.FiatCurrency), nullableCoinDisplayRate(in.RateNum), nullableCoinDisplayRate(in.RateDen), in.ActorID, in.Reason))
		if err != nil {
			return err
		}
		return audit(ctx, tx, "", in.ActorID, "coin_display.updated", in.OperationID, in.OperationID, map[string]any{
			"before": current.Public(), "after": result.Public(), "version": result.Version, "reason": in.Reason,
		})
	})
	return result, err
}
