package billing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MediaReserveInput is the current commercial boundary for non-token media.
// The rate is read from the immutable media profile in the same transaction.
type MediaReserveInput struct {
	TenantID, UserID, KeyID string
	RequestID, AttemptID    string
	Operation, ModelKey     string
	ProfileVersion          string
	IdempotencyKey          string
	RequestDigest           []byte
	QuantityBound           int64
	Deadline                time.Time
}

type MediaReservation struct {
	ID, RequestID, TenantID, Operation, ModelKey, ProfileVersion string
	AttemptID, AttemptState                                      string
	Amount                                                       Money
	RatePerMillion, SupplierRatePerMillion, QuantityBound        int64
	Deadline                                                     time.Time
	Existing                                                     bool
}

type MediaFinalizationInput struct {
	TenantID, RequestID, AttemptID, Source, SourceEventID string
	Operation, Outcome, UsageState                        string
	Quantity                                              int64
}

type MediaSettlement struct {
	ID, RequestID, TenantID, Status, Reason, JournalID string
	ChargedAmount                                      Money
	RawAmount, ReleasedAmount, SupplierCostStatus      string
	SupplierCostAmount                                 *Money
}

func validMediaOperation(value string) bool {
	return oneOf(value, "image_generation", "tts", "video_generation", "realtime_turn")
}

func mediaAmountValue(quantity, rate int64) (*big.Int, error) {
	if quantity < 0 || rate < 0 {
		return nil, fail("invalid_amount", "media quantity and rate must be nonnegative")
	}
	n := new(big.Int).Mul(big.NewInt(quantity), big.NewInt(rate))
	n.Add(n, big.NewInt(999999))
	n.Div(n, big.NewInt(1000000))
	return n, nil
}

func (s *Store) ReserveMedia(ctx context.Context, in MediaReserveInput) (MediaReservation, error) {
	var result MediaReservation
	if !validID(in.TenantID) || !validID(in.UserID) || !validID(in.KeyID) || !validID(in.RequestID) || !validID(in.AttemptID) || !validMediaOperation(in.Operation) || in.ModelKey == "" || len(in.IdempotencyKey) == 0 || len(in.IdempotencyKey) > 200 || len(in.RequestDigest) != sha256.Size || in.QuantityBound <= 0 || in.Deadline.IsZero() {
		return result, fail("invalid_request", "invalid media reservation input")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var profileVersion string
		var rate, supplierRate, maximum int64
		// The profile is immutable and the application role is intentionally
		// SELECT-only on this table. PostgreSQL requires UPDATE for a FOR SHARE
		// lock, so a locking read would violate the role boundary without adding
		// protection to an immutable row.
		if err := tx.QueryRow(ctx, `SELECT profile_version,rate_per_million,supplier_rate_per_million,max_quantity FROM media_model_profiles WHERE model_key=$1 AND operation=$2 AND enabled`, in.ModelKey, in.Operation).Scan(&profileVersion, &rate, &supplierRate, &maximum); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fail("media_profile_unavailable", "media profile is not published")
			}
			return err
		}
		if in.ProfileVersion != "" && in.ProfileVersion != profileVersion || in.QuantityBound > maximum {
			return fail("invalid_bounds", "media quantity exceeds the published profile")
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM api_keys WHERE tenant_id=$1 AND id=$2 AND revoked_at IS NULL FOR UPDATE`, in.TenantID, in.KeyID).Scan(new(string)); err != nil {
			return err
		}
		amount, err := mediaAmountValue(in.QuantityBound, rate)
		if err != nil || !amount.IsInt64() {
			return fail("amount_overflow", "media reservation exceeds supported range")
		}
		var existingID string
		err = tx.QueryRow(ctx, `INSERT INTO media_requests(tenant_id,id,key_id,owner_id,model_key,operation,profile_version,idempotency_key,request_digest,quantity_bound,deadline)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (tenant_id,operation,idempotency_key) DO NOTHING RETURNING id`, in.TenantID, in.RequestID, in.KeyID, in.UserID, in.ModelKey, in.Operation, profileVersion, in.IdempotencyKey, in.RequestDigest, in.QuantityBound, in.Deadline).Scan(&existingID)
		if errors.Is(err, pgx.ErrNoRows) {
			var oldDigest []byte
			if err = tx.QueryRow(ctx, `SELECT id,request_digest FROM media_requests WHERE tenant_id=$1 AND operation=$2 AND idempotency_key=$3`, in.TenantID, in.Operation, in.IdempotencyKey).Scan(&existingID, &oldDigest); err != nil {
				return err
			}
			if !bytes.Equal(oldDigest, in.RequestDigest) {
				return fail("idempotency_conflict", "media idempotency key already used")
			}
			result.Existing = true
			return tx.QueryRow(ctx, `SELECT r.id,r.request_id,r.tenant_id,q.operation,q.model_key,q.profile_version,r.amount,r.rate_per_million,a.supplier_rate_per_million,r.quantity_bound,r.deadline,a.id,a.delivery_state FROM media_reservations r JOIN media_requests q ON q.tenant_id=r.tenant_id AND q.id=r.request_id JOIN media_attempts a ON a.tenant_id=q.tenant_id AND a.request_id=q.id WHERE r.tenant_id=$1 AND r.request_id=$2`, in.TenantID, existingID).Scan(&result.ID, &result.RequestID, &result.TenantID, &result.Operation, &result.ModelKey, &result.ProfileVersion, &result.Amount, &result.RatePerMillion, &result.SupplierRatePerMillion, &result.QuantityBound, &result.Deadline, &result.AttemptID, &result.AttemptState)
		}
		if err != nil {
			return err
		}
		reservationID := uuid.NewString()
		result = MediaReservation{ID: reservationID, RequestID: in.RequestID, TenantID: in.TenantID, Operation: in.Operation, ModelKey: in.ModelKey, ProfileVersion: profileVersion, AttemptID: in.AttemptID, AttemptState: "prepared", Amount: Money(amount.Int64()), RatePerMillion: rate, SupplierRatePerMillion: supplierRate, QuantityBound: in.QuantityBound, Deadline: in.Deadline}
		accounts, err := lockAccounts(ctx, tx, in.TenantID, "AVAILABLE", "HELD")
		if err != nil {
			return err
		}
		journal, err := postLocked(ctx, tx, in.TenantID, reservationID, "media.reserve", in.RequestID, in.UserID, map[string]int64{"AVAILABLE": -int64(result.Amount), "HELD": int64(result.Amount)}, accounts)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO media_reservations(tenant_id,id,request_id,amount,rate_per_million,quantity_bound,deadline,journal_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, in.TenantID, reservationID, in.RequestID, int64(result.Amount), rate, in.QuantityBound, in.Deadline, journal); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO media_attempts(tenant_id,id,request_id,supplier_rate_per_million) VALUES($1,$2,$3,$4)`, in.TenantID, in.AttemptID, in.RequestID, supplierRate); err != nil {
			return err
		}
		if err = applyMediaReserveLots(ctx, tx, in.TenantID, reservationID, result.Amount, journal); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE api_keys SET held=held+$3 WHERE tenant_id=$1 AND id=$2`, in.TenantID, in.KeyID, int64(result.Amount)); err != nil {
			return err
		}
		return audit(ctx, tx, in.TenantID, in.UserID, "billing.media.reserve", in.RequestID, in.RequestID, map[string]any{"operation": in.Operation, "amount": result.Amount, "quantity_bound": in.QuantityBound})
	})
	return result, err
}

func applyMediaReserveLots(ctx context.Context, tx pgx.Tx, tenant, reservation string, amount Money, journal string) error {
	if amount == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT b.lot_id,l.fifo_at,b.available_amount,b.held_amount,b.consumed_amount,b.refund_held_amount,b.retired_amount,b.revision FROM funding_lot_balances b JOIN funding_lots l ON l.tenant_id=b.tenant_id AND l.id=b.lot_id WHERE b.tenant_id=$1 AND b.available_amount>0 ORDER BY l.fifo_at,l.id COLLATE "C" FOR UPDATE OF b`, tenant)
	if err != nil {
		return err
	}
	defer rows.Close()
	type candidate struct {
		id                                             string
		available, held, consumed, refundHeld, retired int64
		revision                                       int64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		var fifo time.Time
		if err = rows.Scan(&c.id, &fifo, &c.available, &c.held, &c.consumed, &c.refundHeld, &c.retired, &c.revision); err != nil {
			return err
		}
		candidates = append(candidates, c)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	remaining := int64(amount)
	for index, c := range candidates {
		if remaining == 0 {
			break
		}
		part := c.available
		if part > remaining {
			part = remaining
		}
		next, err := projectFundingLotChange(&fundingLotBalance{id: c.id, available: c.available, held: c.held, consumed: c.consumed, refundHeld: c.refundHeld, retired: c.retired, revision: c.revision}, -part, part, 0, 0, 0)
		if err != nil {
			return err
		}
		allocation := index + 1
		if _, err = tx.Exec(ctx, `INSERT INTO media_reservation_lots(tenant_id,reservation_id,allocation_index,lot_id,amount) VALUES($1,$2,$3,$4,$5)`, tenant, reservation, allocation, c.id, part); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed,delta_refund_held,delta_retired) VALUES($1,$2,$3,$4,$5,'reserve',$6,$7,$8,0,0,0)`, uuid.NewString(), tenant, c.id, reservation, journal, next.revision, -part, part); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE funding_lot_balances SET available_amount=$3,held_amount=$4,consumed_amount=$5,revision=$6 WHERE tenant_id=$1 AND lot_id=$2 AND revision=$7`, tenant, c.id, next.available, next.held, next.consumed, next.revision, c.revision); err != nil {
			return err
		}
		remaining -= part
	}
	if remaining != 0 {
		return fail("insufficient_balance", "funding lots cannot cover media reservation")
	}
	return nil
}

func (s *Store) MarkMediaAttemptSending(ctx context.Context, tenant, attempt string) error {
	if !validID(tenant) || !validID(attempt) {
		return fail("invalid_request", "invalid media attempt")
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		var state string
		if err := tx.QueryRow(ctx, `SELECT delivery_state FROM media_attempts WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, attempt).Scan(&state); err != nil {
			return err
		}
		if state == "sending" {
			return nil
		}
		if state != "prepared" {
			return fail("media_attempt_terminal", "media attempt cannot be dispatched again")
		}
		_, err := tx.Exec(ctx, `UPDATE media_attempts SET delivery_state='sending' WHERE tenant_id=$1 AND id=$2`, tenant, attempt)
		return err
	})
}

func (s *Store) FinalizeMedia(ctx context.Context, in MediaFinalizationInput) (MediaSettlement, error) {
	var result MediaSettlement
	if !validID(in.TenantID) || !validID(in.RequestID) || !validID(in.AttemptID) || !validMediaOperation(in.Operation) || in.Source == "" || in.SourceEventID == "" || in.Quantity < 0 || !oneOf(in.UsageState, "confirmed_final", "missing", "unknown", "conflicting") || !oneOf(in.Outcome, "completed", "failed", "cancelled", "unknown") {
		return result, fail("invalid_evidence", "invalid media finalization")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var existing MediaSettlement
		var existingSupplierCost *int64
		err := tx.QueryRow(ctx, `SELECT id,request_id,tenant_id,status,reason,journal_id,charged_amount,raw_amount::text,released_amount::text,supplier_cost_status,supplier_cost_amount FROM media_settlements WHERE tenant_id=$1 AND request_id=$2`, in.TenantID, in.RequestID).Scan(&existing.ID, &existing.RequestID, &existing.TenantID, &existing.Status, &existing.Reason, &existing.JournalID, &existing.ChargedAmount, &existing.RawAmount, &existing.ReleasedAmount, &existing.SupplierCostStatus, &existingSupplierCost)
		if err == nil {
			if existingSupplierCost != nil {
				value := Money(*existingSupplierCost)
				existing.SupplierCostAmount = &value
			}
			result = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var reservationID, keyID, operation, modelKey, profile string
		var reserved, rate, supplierRate, bound int64
		var reservationStatus string
		if err = tx.QueryRow(ctx, `SELECT r.id,q.key_id,q.operation,q.model_key,q.profile_version,r.amount,r.rate_per_million,a.supplier_rate_per_million,r.quantity_bound,r.status FROM media_reservations r JOIN media_requests q ON q.tenant_id=r.tenant_id AND q.id=r.request_id JOIN media_attempts a ON a.tenant_id=r.tenant_id AND a.id=$3 AND a.request_id=r.request_id WHERE r.tenant_id=$1 AND r.request_id=$2 FOR UPDATE`, in.TenantID, in.RequestID, in.AttemptID).Scan(&reservationID, &keyID, &operation, &modelKey, &profile, &reserved, &rate, &supplierRate, &bound, &reservationStatus); err != nil {
			return err
		}
		if operation != in.Operation || reservationStatus != "reserved" {
			return fail("invalid_state", "media reservation is not settleable")
		}
		canonical, _ := json.Marshal(struct {
			Version string `json:"version"`
			Source  string `json:"source"`
			Event   string `json:"source_event_id"`
			State   string `json:"usage_state"`
			Qty     *int64 `json:"quantity,omitempty"`
		}{Version: "1", Source: in.Source, Event: in.SourceEventID, State: in.UsageState, Qty: mediaQuantityPointer(in.UsageState, in.Quantity)})
		observationID := uuid.NewString()
		observationDigest := sha256.Sum256(canonical)
		if _, err = tx.Exec(ctx, `INSERT INTO media_observations(tenant_id,id,request_id,attempt_id,source,source_event_id,usage_state,quantity,canonical,digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (tenant_id,source,source_event_id) DO NOTHING`, in.TenantID, observationID, in.RequestID, in.AttemptID, in.Source, in.SourceEventID, in.UsageState, mediaQuantityPointer(in.UsageState, in.Quantity), canonical, observationDigest[:]); err != nil {
			return err
		}
		raw, err := mediaAmountValue(in.Quantity, rate)
		if err != nil || !raw.IsInt64() || in.Quantity > bound {
			raw = big.NewInt(0)
		}
		charge := int64(0)
		reason := "released_no_delivery"
		if in.UsageState == "confirmed_final" && in.Outcome == "completed" && in.Quantity <= bound {
			charge = raw.Int64()
			reason = "charged_usage"
		}
		if charge > reserved {
			charge = reserved
		}
		supplierCostStatus := "unknown"
		var supplierCostAmount *int64
		if in.UsageState == "confirmed_final" && in.Outcome == "completed" && in.Quantity <= bound {
			supplierRaw, supplierErr := mediaAmountValue(in.Quantity, supplierRate)
			if supplierErr == nil && supplierRaw.IsInt64() {
				supplierCostStatus = "known"
				value := supplierRaw.Int64()
				supplierCostAmount = &value
			}
		}
		kinds := []string{"HELD", "AVAILABLE"}
		if charge > 0 {
			kinds = append(kinds, "REVENUE")
		}
		accounts, err := lockAccounts(ctx, tx, in.TenantID, kinds...)
		if err != nil {
			return err
		}
		settlementID := uuid.NewString()
		deltas := map[string]int64{"HELD": -reserved, "AVAILABLE": reserved - charge}
		if charge > 0 {
			deltas["REVENUE"] = charge
		}
		journal, err := postLocked(ctx, tx, in.TenantID, settlementID, "media.settle", in.RequestID, "system", deltas, accounts)
		if err != nil {
			return err
		}
		if err = applyMediaFinishLots(ctx, tx, in.TenantID, reservationID, settlementID, journal, Money(charge), Money(reserved)); err != nil {
			return err
		}
		status := "settled"
		if charge == 0 {
			status = "released"
		}
		released := new(big.Int).Sub(big.NewInt(reserved), big.NewInt(charge))
		storedRaw := raw.String()
		if charge == 0 {
			storedRaw = "0"
		}
		if _, err = tx.Exec(ctx, `INSERT INTO media_settlements(tenant_id,id,request_id,reservation_id,attempt_id,charged_amount,raw_amount,released_amount,supplier_cost_status,supplier_cost_amount,status,reason,journal_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, in.TenantID, settlementID, in.RequestID, reservationID, in.AttemptID, charge, storedRaw, released.String(), supplierCostStatus, supplierCostAmount, status, reason, journal); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE media_reservations SET status=$3 WHERE tenant_id=$1 AND id=$2`, in.TenantID, reservationID, status); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE media_requests SET state=$3 WHERE tenant_id=$1 AND id=$2`, in.TenantID, in.RequestID, status); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE api_keys SET held=held-$3,spent=spent+$4 WHERE tenant_id=$1 AND id=$2 AND held>=$3`, in.TenantID, keyID, reserved, charge); err != nil {
			return err
		}
		attemptState := mediaAttemptState(in.Outcome, in.UsageState)
		if attemptState == "completed" {
			if _, err = tx.Exec(ctx, `UPDATE media_attempts SET delivery_state='sending' WHERE tenant_id=$1 AND id=$2 AND delivery_state='prepared'`, in.TenantID, in.AttemptID); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE media_attempts SET delivery_state=$3 WHERE tenant_id=$1 AND id=$2`, in.TenantID, in.AttemptID, attemptState); err != nil {
			return err
		}
		var supplierAmount *Money
		if supplierCostAmount != nil {
			value := Money(*supplierCostAmount)
			supplierAmount = &value
		}
		result = MediaSettlement{ID: settlementID, RequestID: in.RequestID, TenantID: in.TenantID, Status: status, Reason: reason, JournalID: journal, ChargedAmount: Money(charge), RawAmount: raw.String(), ReleasedAmount: released.String(), SupplierCostStatus: supplierCostStatus, SupplierCostAmount: supplierAmount}
		return audit(ctx, tx, in.TenantID, "system", "billing.media.settle", in.RequestID, in.RequestID, map[string]any{"status": status, "charged_amount": charge, "raw_amount": raw.String(), "supplier_cost_status": supplierCostStatus, "supplier_cost_amount": supplierCostAmount})
	})
	return result, err
}

func mediaQuantityPointer(state string, quantity int64) *int64 {
	if state == "confirmed_final" {
		return &quantity
	}
	return nil
}

func mediaAttemptState(outcome, usage string) string {
	if outcome == "completed" && usage == "confirmed_final" {
		return "completed"
	}
	if outcome == "unknown" {
		return "unknown"
	}
	return "failed"
}

func applyMediaFinishLots(ctx context.Context, tx pgx.Tx, tenant, reservation, settlement, journal string, charge, reserved Money) error {
	rows, err := tx.Query(ctx, `SELECT allocation_index,lot_id,amount FROM media_reservation_lots WHERE tenant_id=$1 AND reservation_id=$2 ORDER BY allocation_index`, tenant, reservation)
	if err != nil {
		return err
	}
	defer rows.Close()
	var allocations []fundingLotAllocation
	var ids []string
	for rows.Next() {
		var a fundingLotAllocation
		if err = rows.Scan(&a.index, &a.lotID, &a.amount); err != nil {
			return err
		}
		allocations = append(allocations, a)
		ids = append(ids, a.lotID)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	balances, err := lockFundingLotBalancesTx(ctx, tx, tenant, ids)
	if err != nil {
		return err
	}
	finish, err := planFinishFundingLots(allocations, reserved, charge)
	if err != nil {
		return err
	}
	for _, item := range finish {
		balance := balances[item.allocation.lotID]
		next, err := projectFundingLotChange(balance, int64(item.released), -int64(item.allocation.amount), int64(item.charged), 0, 0)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO media_settlement_lots(tenant_id,settlement_id,reservation_id,allocation_index,lot_id,charged_amount,released_amount) VALUES($1,$2,$3,$4,$5,$6,$7)`, tenant, settlement, reservation, item.allocation.index, item.allocation.lotID, item.charged, item.released); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed,delta_refund_held,delta_retired) VALUES($1,$2,$3,$4,$5,'finish',$6,$7,$8,$9,0,0)`, uuid.NewString(), tenant, item.allocation.lotID, settlement, journal, next.revision, item.released, -int64(item.allocation.amount), item.charged); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE funding_lot_balances SET available_amount=$3,held_amount=$4,consumed_amount=$5,revision=$6 WHERE tenant_id=$1 AND lot_id=$2 AND revision=$7`, tenant, item.allocation.lotID, next.available, next.held, next.consumed, next.revision, balance.revision); err != nil {
			return err
		}
		*balance = next
	}
	return nil
}
