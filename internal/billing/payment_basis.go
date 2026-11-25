package billing

import (
	"context"

	"github.com/moni-ai/moni-aigate/internal/routing"
)

// ReservationCNYBasis is the conservative paid-CNY lower bound of one reservation.
// It reads only the frozen allocation and receipt facts (amounts, basis_kind,
// fiat currency and amount) that 027/034 froze at funding time, so request paths
// stay isolated from coin-rate lookups. The per-lot share floor(amount*fiat/issued)
// matches the Reserve economics snapshot writer. Gift, opening, unknown, USD or
// an inconsistent lot leaves the whole basis uncovered. The result is only an
// admission input for minimum margin. It does not change the COIN reservation.
func (s *Store) ReservationCNYBasis(ctx context.Context, tenant, reservation string, amount int64) (int64, bool, error) {
	if s == nil || s.pool == nil {
		return 0, false, fail("payment_basis_unavailable", "payment basis is unavailable")
	}
	rows, err := s.pool.Query(ctx, `SELECT a.amount, l.issued_amount, coalesce(f.fiat_amount,0), f.basis_kind, coalesce(f.fiat_currency,'')
FROM funding_reservation_lots a
JOIN funding_lots l ON l.tenant_id=a.tenant_id AND l.id=a.lot_id
JOIN funding_receipts f ON f.tenant_id=l.tenant_id AND f.id=l.funding_receipt_id
WHERE a.tenant_id=$1 AND a.reservation_id=$2
ORDER BY a.allocation_index`, tenant, reservation)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	var lots []routing.PaidLot
	for rows.Next() {
		var lot routing.PaidLot
		if err = rows.Scan(&lot.Coin, &lot.Issued, &lot.Fiat, &lot.Basis, &lot.FiatCurrency); err != nil {
			return 0, false, err
		}
		lots = append(lots, lot)
	}
	if err = rows.Err(); err != nil {
		return 0, false, err
	}
	basis, covered := routing.ConservativeCNYPayment(amount, lots)
	return basis, covered, nil
}
