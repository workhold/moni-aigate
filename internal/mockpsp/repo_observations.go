package mockpsp

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func deliveryFailureCode(code FailureCode) bool {
	switch code {
	case FailureNetwork, FailureTimeout, FailureAuth, FailureProtocol, FailureEnvironment, FailureTemporary, FailurePermanent:
		return true
	}
	return false
}

func deliveryObservationValid(claim DeliveryClaim, observation Observation) bool {
	if observation.httpStatus != nil && (*observation.httpStatus < 100 || *observation.httpStatus > 599) {
		return false
	}
	switch observation.outcome {
	case "ack":
		return observation.httpStatus != nil && (*observation.httpStatus == 200 || *observation.httpStatus == 202) &&
			observation.errorCode == "" && observation.eventID == claim.eventID && len(observation.receiptData) > 0 && len(observation.receiptData) <= 8192
	case "failed":
		return deliveryFailureCode(observation.errorCode) && len(observation.receiptData) == 0 && observation.eventID == ""
	}
	return false
}

func sameDeliveryObservation(a, b Observation) bool {
	return a.outcome == b.outcome && a.errorCode == b.errorCode && bytes.Equal(a.receiptData, b.receiptData) &&
		((a.httpStatus == nil && b.httpStatus == nil) || (a.httpStatus != nil && b.httpStatus != nil && *a.httpStatus == *b.httpStatus))
}

func deliveryLeaseRaceCandidate(err error, nextState string) bool {
	var pg *pgconn.PgError
	return (nextState == "delivered" || nextState == "retry_wait") && errors.As(err, &pg) && pg != nil && pg.Code == "P0001"
}

func deliveryLeaseExpired(state string, generation int64, lease *time.Time, now time.Time, claim DeliveryClaim) bool {
	return state == "leased" && generation == claim.generation && lease != nil &&
		lease.Equal(claim.leaseUntil) && !now.Before(*lease)
}

// The WHERE condition and the trigger each read the DB clock. If the lease
// expires between them, preserve the already inserted IO evidence, but only
// after a fresh statement proves that exact lease expired while we hold its row.
func deliveryRecordState(ctx context.Context, tx pgx.Tx, claim DeliveryClaim, nextState string, lastError any) (bool, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackTx(sp)
	tag, updateErr := sp.Exec(ctx, `UPDATE mock_notification_delivery SET state=$3,last_error_code=$4
 WHERE event_id=$1 AND generation=$2 AND state='leased' AND lease_until>clock_timestamp()`, claim.eventRowID, claim.generation, nextState, lastError)
	if updateErr == nil {
		if err = sp.Commit(ctx); err != nil {
			return false, err
		}
		return tag.RowsAffected() == 1, nil
	}
	cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
	err = sp.Rollback(cleanup)
	cancel()
	if err != nil {
		return false, err
	}
	if !deliveryLeaseRaceCandidate(updateErr, nextState) {
		return false, updateErr
	}
	var state string
	var generation int64
	var lease *time.Time
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT state,generation,lease_until,clock_timestamp()
 FROM mock_notification_delivery WHERE event_id=$1`, claim.eventRowID).Scan(&state, &generation, &lease, &now); err != nil {
		return false, err
	}
	if !deliveryLeaseExpired(state, generation, lease, now, claim) {
		return false, updateErr
	}
	return false, nil
}

func (r *Repository) RecordObservation(parent context.Context, claim DeliveryClaim, observation Observation) (RecordOutcome, error) {
	if !r.deliveryClaimValid(claim) || !deliveryObservationValid(claim, observation) {
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
	var state, eventID, attemptID string
	var generation int64
	var lease *time.Time
	err = tx.QueryRow(ctx, `SELECT d.state,d.generation,d.lease_until,n.event_id,a.id
 FROM mock_notification_delivery d JOIN mock_notification_events n ON n.id=d.event_id
 JOIN mock_delivery_attempts a ON a.event_id=d.event_id AND a.generation=$2
 WHERE d.event_id=$1 FOR UPDATE OF d`, claim.eventRowID, claim.generation).Scan(&state, &generation, &lease, &eventID, &attemptID)
	if err != nil {
		return 0, normalizeError(err)
	}
	if eventID != claim.eventID || attemptID != claim.attemptID {
		return 0, fail("mock_unavailable")
	}
	var prior Observation
	var priorError *string
	err = tx.QueryRow(ctx, `SELECT outcome,http_status,error_code,receipt_data FROM mock_delivery_observations WHERE event_id=$1 AND generation=$2`, claim.eventRowID, claim.generation).Scan(&prior.outcome, &prior.httpStatus, &priorError, &prior.receiptData)
	if err == nil {
		if priorError != nil {
			prior.errorCode = FailureCode(*priorError)
		}
		if !sameDeliveryObservation(prior, observation) {
			return 0, fail("mock_unavailable")
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
		return AlreadyRecorded, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, normalizeError(err)
	}
	now, err := dbNow(ctx, tx)
	if err != nil {
		return 0, normalizeError(err)
	}
	var failure any
	if observation.errorCode != "" {
		failure = string(observation.errorCode)
	}
	var receipt any
	if observation.receiptData != nil {
		receipt = append([]byte(nil), observation.receiptData...)
	}
	_, err = tx.Exec(ctx, `INSERT INTO mock_delivery_observations(event_id,generation,outcome,http_status,error_code,receipt_data)
 VALUES($1,$2,$3,$4,$5,$6)`, claim.eventRowID, claim.generation, observation.outcome, observation.httpStatus, failure, receipt)
	if err != nil {
		return 0, normalizeError(err)
	}
	result := StaleRecorded
	if state == "leased" && generation == claim.generation && lease != nil && lease.Equal(claim.leaseUntil) && now.Before(*lease) {
		nextState, lastError := "delivered", any(nil)
		if observation.outcome == "failed" {
			nextState, lastError = "retry_wait", string(observation.errorCode)
			if generation == 20 {
				nextState, lastError = "review_required", "attempts_exhausted"
			}
		}
		current, updateErr := deliveryRecordState(ctx, tx, claim, nextState, lastError)
		if updateErr != nil {
			return 0, normalizeError(updateErr)
		}
		if current {
			result = CurrentRecorded
		}
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
	return result, nil
}
