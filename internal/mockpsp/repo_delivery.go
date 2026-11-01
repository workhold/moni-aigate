package mockpsp

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func (r *Repository) deliveryClaimValid(c DeliveryClaim) bool {
	return r != nil && r.identity != nil && c.repo == r.identity && c.envID == r.env.ID &&
		c.eventRowID != "" && c.eventID != "" && c.generation >= 1 && c.generation <= 20 &&
		c.attemptID != "" && c.deliveryID != "" && c.deliveryTime != "" && c.keyID != "" &&
		c.bodyDigest != "" && !c.claimedAt.IsZero() && c.leaseUntil.Equal(c.claimedAt.Add(time.Minute))
}

func (r *Repository) deliveryBody(raw []byte, eventID, digest string) error {
	n, err := paymentproto.ParseNotification(raw)
	if err != nil || n.EventID != eventID || n.EnvironmentID != r.env.ID || n.MerchantID != r.env.MerchantID || paymentproto.RequestDigest(raw) != digest {
		return fail("mock_unavailable")
	}
	canonical, err := paymentproto.CanonicalNotification(n)
	if err != nil || !bytes.Equal(raw, canonical) {
		return fail("mock_unavailable")
	}
	return nil
}

func (r *Repository) deliveryKey(keyID, seconds string) error {
	if r.keyring == nil || keyID == "" || r.activeKeyID != keyID || r.keyring.ActiveID() != keyID || !r.keyring.HasVersion(keyID) || r.keyWindow == nil {
		return fail("payment_key_unavailable")
	}
	n, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil || n < 0 || n > 253402300799 || strconv.FormatInt(n, 10) != seconds {
		return fail("mock_unavailable")
	}
	before, after, ok := r.keyWindow(keyID)
	at := time.Unix(n, 0).UTC()
	if !ok || at.Before(before) || at.After(after) {
		return fail("payment_key_unavailable")
	}
	return nil
}

type deliveryCandidate struct{ rowID, eventID string }

// ClaimDue consumes one durable attempt before its caller can send any IO.
// A lost COMMIT or unlock result returns no claim; only lease expiry can recover it.
func (r *Repository) ClaimDue(parent context.Context) (DeliveryClaim, bool, error) {
	ctx, cancel := r.bounded(parent)
	defer cancel()
	if err := r.check(ctx); err != nil {
		return DeliveryClaim{}, false, normalizeError(err)
	}
	rows, err := r.pool.Query(ctx, `SELECT d.event_id,n.event_id
 FROM mock_notification_delivery d JOIN mock_notification_events n ON n.id=d.event_id
 WHERE d.attempt_count<20 AND ((d.state IN ('pending','retry_wait') AND d.next_attempt_at<=clock_timestamp())
 OR (d.state='leased' AND d.lease_until<=clock_timestamp()))
 ORDER BY d.next_attempt_at,d.event_id COLLATE "C" LIMIT 100`)
	if err != nil {
		return DeliveryClaim{}, false, normalizeError(err)
	}
	candidates := make([]deliveryCandidate, 0, 100)
	for rows.Next() {
		var c deliveryCandidate
		if err = rows.Scan(&c.rowID, &c.eventID); err != nil {
			break
		}
		candidates = append(candidates, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return DeliveryClaim{}, false, normalizeError(err)
	}
	for _, candidate := range candidates {
		claim, found, err := r.claimCandidate(ctx, candidate)
		if err != nil || found {
			return claim, found, err
		}
	}
	return DeliveryClaim{}, false, nil
}

func (r *Repository) claimCandidate(ctx context.Context, candidate deliveryCandidate) (DeliveryClaim, bool, error) {
	c, err := r.connection(ctx)
	if err != nil {
		return DeliveryClaim{}, false, normalizeError(err)
	}
	defer c.close()
	ok, err := c.tryGate(ctx, lockCallback, candidate.eventID)
	if err != nil {
		return DeliveryClaim{}, false, normalizeError(err)
	}
	if !ok {
		if err = c.close(); err != nil {
			return DeliveryClaim{}, false, normalizeError(err)
		}
		return DeliveryClaim{}, false, nil
	}
	tx, err := c.begin(ctx)
	if err != nil {
		return DeliveryClaim{}, false, normalizeError(err)
	}
	defer rollbackTx(tx)
	var raw []byte
	var eventID, digest string
	err = tx.QueryRow(ctx, `SELECT n.event_id,n.canonical_body,n.business_digest
 FROM mock_notification_delivery d JOIN mock_notification_events n ON n.id=d.event_id
 WHERE d.event_id=$1 AND d.attempt_count<20 AND
 ((d.state IN ('pending','retry_wait') AND d.next_attempt_at<=clock_timestamp())
 OR (d.state='leased' AND d.lease_until<=clock_timestamp())) FOR UPDATE OF d SKIP LOCKED`, candidate.rowID).Scan(&eventID, &raw, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		rollbackTx(tx)
		if err = c.close(); err != nil {
			return DeliveryClaim{}, false, normalizeError(err)
		}
		return DeliveryClaim{}, false, nil
	}
	if err != nil {
		return DeliveryClaim{}, false, normalizeError(err)
	}
	if eventID != candidate.eventID {
		return DeliveryClaim{}, false, fail("mock_unavailable")
	}
	if err = r.deliveryBody(raw, eventID, digest); err != nil {
		return DeliveryClaim{}, false, err
	}
	claim := DeliveryClaim{repo: r.identity, envID: r.env.ID, eventRowID: candidate.rowID, eventID: eventID,
		attemptID: uuid.NewString(), deliveryID: uuid.NewString(), keyID: r.activeKeyID, bodyDigest: digest}
	var count int
	err = tx.QueryRow(ctx, `UPDATE mock_notification_delivery SET state='leased',generation=generation+1,attempt_count=attempt_count+1
 WHERE event_id=$1 RETURNING generation,attempt_count,lease_until`, candidate.rowID).Scan(&claim.generation, &count, &claim.leaseUntil)
	if err != nil {
		return DeliveryClaim{}, false, normalizeError(err)
	}
	claim.claimedAt = claim.leaseUntil.Add(-time.Minute).UTC()
	claim.deliveryTime = strconv.FormatInt(claim.claimedAt.Unix(), 10)
	if int64(count) != claim.generation || !r.deliveryClaimValid(claim) {
		return DeliveryClaim{}, false, fail("mock_unavailable")
	}
	if err = r.deliveryKey(claim.keyID, claim.deliveryTime); err != nil {
		return DeliveryClaim{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO mock_delivery_attempts
 (id,event_id,generation,attempt_no,delivery_id,delivery_time,signing_key_id,body_digest,claimed_at,lease_until)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, claim.attemptID, claim.eventRowID, claim.generation, count,
		claim.deliveryID, claim.claimedAt.Unix(), claim.keyID, digest, claim.claimedAt, claim.leaseUntil)
	if err != nil {
		return DeliveryClaim{}, false, normalizeError(err)
	}
	if err = commitTx(ctx, tx); err != nil {
		if ErrorCode(err) == "mock_outcome_unknown" {
			c.bad = true
		}
		return DeliveryClaim{}, false, normalizeError(err)
	}
	if err = c.close(); err != nil {
		return DeliveryClaim{}, false, normalizeError(err)
	}
	return claim, true, nil
}

func (r *Repository) Prepare(parent context.Context, claim DeliveryClaim) (PreparedDelivery, error) {
	ctx, cancel := r.bounded(parent)
	defer cancel()
	if !r.deliveryClaimValid(claim) {
		return PreparedDelivery{}, fail("invalid_request")
	}
	c, err := r.connection(ctx)
	if err != nil {
		return PreparedDelivery{}, normalizeError(err)
	}
	defer c.close()
	ok, err := c.tryGate(ctx, lockCallback, claim.eventID)
	if err != nil {
		return PreparedDelivery{}, normalizeError(err)
	}
	if !ok {
		return PreparedDelivery{}, ErrDeliveryDeferred
	}
	tx, err := c.begin(ctx)
	if err != nil {
		return PreparedDelivery{}, normalizeError(err)
	}
	defer rollbackTx(tx)
	var state, eventID, digest, attemptID, deliveryID, keyID, attemptDigest string
	var generation, seconds int64
	var lease *time.Time
	var claimedAt, attemptLease time.Time
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT d.state,d.generation,d.lease_until,n.event_id,n.canonical_body,n.business_digest,
 a.id,a.delivery_id,a.delivery_time,a.signing_key_id,a.body_digest,a.claimed_at,a.lease_until
 FROM mock_notification_delivery d JOIN mock_notification_events n ON n.id=d.event_id
 JOIN mock_delivery_attempts a ON a.event_id=d.event_id AND a.generation=$2
 WHERE d.event_id=$1 FOR UPDATE OF d`, claim.eventRowID, claim.generation).Scan(&state, &generation, &lease,
		&eventID, &raw, &digest, &attemptID, &deliveryID, &seconds, &keyID, &attemptDigest, &claimedAt, &attemptLease)
	if err != nil {
		return PreparedDelivery{}, normalizeError(err)
	}
	if eventID != claim.eventID || digest != claim.bodyDigest || attemptDigest != digest || attemptID != claim.attemptID ||
		deliveryID != claim.deliveryID || strconv.FormatInt(seconds, 10) != claim.deliveryTime || keyID != claim.keyID ||
		!claimedAt.Equal(claim.claimedAt) || !attemptLease.Equal(claim.leaseUntil) {
		return PreparedDelivery{}, fail("mock_unavailable")
	}
	now, err := dbNow(ctx, tx)
	if err != nil {
		return PreparedDelivery{}, normalizeError(err)
	}
	if state != "leased" || generation != claim.generation || lease == nil || !lease.Equal(claim.leaseUntil) || lease.Sub(now) < 15*time.Second {
		return PreparedDelivery{}, ErrDeliveryDeferred
	}
	if err = r.deliveryBody(raw, eventID, digest); err != nil {
		return PreparedDelivery{}, err
	}
	if err = r.deliveryKey(keyID, claim.deliveryTime); err != nil {
		return PreparedDelivery{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE mock_fault_plans SET remaining=0,consumed_at=clock_timestamp()
 WHERE target_kind='callback' AND target_id=$1 AND boundary='before_callback_send_once' AND remaining=1`, eventID)
	if err != nil {
		return PreparedDelivery{}, normalizeError(err)
	}
	deferred := tag.RowsAffected() != 0
	var headers paymentproto.SignedHeaders
	if !deferred {
		headers, err = r.keyring.SignNotification(r.env, paymentproto.SignedHeaders{KeyID: keyID, DeliveryID: deliveryID, DeliveryTime: claim.deliveryTime}, raw)
		if err != nil {
			return PreparedDelivery{}, normalizeError(err)
		}
	}
	if err = commitTx(ctx, tx); err != nil {
		if ErrorCode(err) == "mock_outcome_unknown" {
			c.bad = true
		}
		return PreparedDelivery{}, normalizeError(err)
	}
	if err = c.close(); err != nil {
		return PreparedDelivery{}, normalizeError(err)
	}
	if deferred {
		return PreparedDelivery{}, ErrDeliveryDeferred
	}
	return PreparedDelivery{body: append([]byte(nil), raw...), headers: headers, eventID: eventID}, nil
}

func (r *Repository) ObserveReceivedAt(parent context.Context) (time.Time, error) {
	ctx, cancel := r.bounded(parent)
	defer cancel()
	if err := r.check(ctx); err != nil {
		return time.Time{}, normalizeError(err)
	}
	var now time.Time
	if err := r.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, normalizeError(err)
	}
	return now.UTC(), nil
}

func (r *Repository) ExpireFinalClaims(parent context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, fail("invalid_request")
	}
	ctx, cancel := r.bounded(parent)
	defer cancel()
	c, err := r.connection(ctx)
	if err != nil {
		return 0, normalizeError(err)
	}
	defer c.close()
	tx, err := c.begin(ctx)
	if err != nil {
		return 0, normalizeError(err)
	}
	defer rollbackTx(tx)
	tag, err := tx.Exec(ctx, `WITH expired AS (
 SELECT event_id FROM mock_notification_delivery WHERE state='leased' AND attempt_count=20 AND lease_until<=clock_timestamp()
 ORDER BY lease_until,event_id COLLATE "C" LIMIT $1 FOR UPDATE SKIP LOCKED)
 UPDATE mock_notification_delivery d SET state='review_required',last_error_code='attempts_exhausted'
 FROM expired e WHERE d.event_id=e.event_id AND d.state='leased' AND d.attempt_count=20 AND d.lease_until<=clock_timestamp()`, limit)
	if err != nil {
		return 0, normalizeError(err)
	}
	if err = commitTx(ctx, tx); err != nil {
		if ErrorCode(err) == "mock_outcome_unknown" {
			c.bad = true
		}
		return 0, normalizeError(err)
	}
	if err = c.close(); err != nil {
		return 0, normalizeError(err)
	}
	return int(tag.RowsAffected()), nil
}
