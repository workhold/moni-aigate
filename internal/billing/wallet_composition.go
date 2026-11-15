package billing

import (
	"context"
)

type WalletComposition struct {
	Available      Money `json:"available"`
	PaidAvailable  Money `json:"paid_available"`
	GrantAvailable Money `json:"grant_available"`
	OtherAvailable Money `json:"other_available"`
	Held           Money `json:"held"`
	RefundHeld     Money `json:"refund_held"`
}

func (s *Store) WalletComposition(ctx context.Context, q Query) (WalletComposition, error) {
	var result WalletComposition
	if !validID(q.TenantID) {
		return result, fail("invalid_tenant", "tenant scope required")
	}
	var accountAvailable, accountHeld, accountRefund int64
	err := s.pool.QueryRow(ctx, `WITH source AS (
 SELECT coalesce(sum(b.available_amount),0) AS available,
  coalesce(sum(b.available_amount) FILTER(WHERE f.basis_kind='paid'),0) AS paid,
  coalesce(sum(b.available_amount) FILTER(WHERE f.basis_kind='grant'),0) AS grant,
  coalesce(sum(b.available_amount) FILTER(WHERE f.basis_kind IN ('opening','unknown')),0) AS other,
  coalesce(sum(b.held_amount),0) AS held,
  coalesce(sum(b.refund_held_amount),0) AS refund_held
 FROM funding_lot_balances b JOIN funding_lots l ON l.tenant_id=b.tenant_id AND l.id=b.lot_id
 JOIN funding_receipts f ON f.tenant_id=l.tenant_id AND f.id=l.funding_receipt_id
 WHERE b.tenant_id=$1
), accounts AS (
 SELECT coalesce(sum(balance) FILTER(WHERE kind='AVAILABLE'),0) AS available,
  coalesce(sum(balance) FILTER(WHERE kind='HELD'),0) AS held,
  coalesce(sum(balance) FILTER(WHERE kind='REFUND_HELD'),0) AS refund_held
 FROM ledger_accounts WHERE tenant_id=$1
)
SELECT source.available,source.paid,source.grant,source.other,source.held,source.refund_held,accounts.available,accounts.held,accounts.refund_held FROM source,accounts`, q.TenantID).Scan(&result.Available, &result.PaidAvailable, &result.GrantAvailable, &result.OtherAvailable, &result.Held, &result.RefundHeld, &accountAvailable, &accountHeld, &accountRefund)
	if err != nil {
		return result, err
	}
	if int64(result.Available) != accountAvailable || int64(result.Held) != accountHeld || int64(result.RefundHeld) != accountRefund || int64(result.PaidAvailable)+int64(result.GrantAvailable)+int64(result.OtherAvailable) != int64(result.Available) {
		return result, fail("funding_integrity", "wallet composition differs from account balances")
	}
	return result, nil
}
