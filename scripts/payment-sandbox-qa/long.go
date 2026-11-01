package main

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/mockpsp"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type failedAttempt struct {
	generation, number int
	delivery, digest   string
	claimed, lease     time.Time
	observed           *time.Time
	outcome, code      *string
	status             *int
}

func retryDelay(generation int) time.Duration {
	switch generation {
	case 1:
		return time.Second
	case 2:
		return 5 * time.Second
	case 3:
		return 30 * time.Second
	case 4:
		return 120 * time.Second
	default:
		return 300 * time.Second
	}
}

// No receiver is started in this independent environment. Each attempt really
// calls the pinned loopback endpoint and records the resulting network failure.
// No deadline, lease, attempt counter, trigger or database clock is modified.
func (s *session) longRetry() error {
	var err error
	s.mock, err = s.start("mock")
	if err != nil {
		return err
	}
	if err = s.ready(s.mock, s.env.APIOrigin, s.env.ControlOrigin); err != nil {
		return err
	}
	q, raw, err := s.checkoutInput()
	if err != nil {
		return err
	}
	if _, _, err = s.checkout(raw); err != nil {
		return err
	}
	control, _, err := s.control(mockpsp.ControlInput{OperationID: uuid.NewString(), Action: "capture_order", Scenario: "normal", MerchantOrderID: &q.MerchantOrderID})
	if err != nil {
		return err
	}
	var eventRow, digest string
	var body []byte
	if s.mockDB.QueryRow(s.ctx, `SELECT id,business_digest,canonical_body FROM mock_notification_events WHERE canonical_capture_id=$1`, *control.ResourceID).Scan(&eventRow, &digest, &body) != nil || digest != paymentproto.RequestDigest(body) {
		return failure("long_retry_event_missing")
	}
	s.report.pass("long_retry_real_process_started_receiver_deliberately_absent")
	confirmed := 0
	err = eventually(s.ctx, time.Second, func() (bool, error) {
		select {
		case <-s.mock.done:
			return false, failure("long_retry_component_exited")
		default:
		}
		attempts, state, count, last, err := s.readFailedAttempts(eventRow)
		if err != nil {
			return false, err
		}
		if err = validateFailedAttempts(attempts, digest); err != nil {
			return false, err
		}
		if len(attempts) != count {
			return false, failure("long_retry_claim_count_mismatch")
		}
		complete := 0
		for _, a := range attempts {
			if a.observed != nil {
				complete++
			}
		}
		for confirmed < complete {
			confirmed++
			s.report.pass(fmt.Sprintf("real_callback_failed_generation_%02d", confirmed))
		}
		if state == "delivered" || count > 20 {
			return false, failure("long_retry_unexpected_terminal_state")
		}
		if state != "review_required" {
			return false, nil
		}
		if count != 20 || complete != 20 || last != "attempts_exhausted" {
			return false, failure("long_retry_review_without_twenty_failures")
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	// Two runners poll every 250 ms. Observe several real post-terminal cycles.
	t := time.NewTimer(1500 * time.Millisecond)
	defer t.Stop()
	select {
	case <-s.ctx.Done():
		return failure("long_retry_cancelled")
	case <-t.C:
	}
	attempts, state, count, last, err := s.readFailedAttempts(eventRow)
	if err != nil || validateFailedAttempts(attempts, digest) != nil || len(attempts) != 20 || count != 20 || state != "review_required" || last != "attempts_exhausted" {
		return failure("long_retry_twenty_first_claim_or_terminal_change")
	}
	var inboxes int
	if s.receiverDB.QueryRow(s.ctx, "SELECT count(*) FROM receiver_inbox").Scan(&inboxes) != nil || inboxes != 0 {
		return failure("long_retry_absent_receiver_received_event")
	}
	if requireRunning(s.mock) != nil {
		return failure("long_retry_component_exited")
	}
	s.report.pass("twenty_real_attempts_original_backoff_review_and_no_twenty_first")
	return nil
}

func (s *session) readFailedAttempts(event string) ([]failedAttempt, string, int, string, error) {
	var state, last string
	var count int
	// A repeatable-read snapshot joins the mutable count and immutable attempts
	// without racing the two real process workers between separate reads.
	tx, err := s.mockDB.Begin(s.ctx)
	if err != nil {
		return nil, "", 0, "", failure("long_retry_observation_failed")
	}
	defer tx.Rollback(s.ctx)
	if _, err = tx.Exec(s.ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return nil, "", 0, "", failure("long_retry_observation_failed")
	}
	if tx.QueryRow(s.ctx, `SELECT state,attempt_count,coalesce(last_error_code,'') FROM mock_notification_delivery WHERE event_id=$1`, event).Scan(&state, &count, &last) != nil {
		return nil, "", 0, "", failure("long_retry_observation_failed")
	}
	rows, err := tx.Query(s.ctx, `SELECT a.generation,a.attempt_no,a.delivery_id,a.body_digest,a.claimed_at,a.lease_until,o.observed_at,o.outcome,o.error_code,o.http_status
 FROM mock_delivery_attempts a LEFT JOIN mock_delivery_observations o ON o.event_id=a.event_id AND o.generation=a.generation
 WHERE a.event_id=$1 ORDER BY a.generation`, event)
	if err != nil {
		return nil, "", 0, "", failure("long_retry_observation_failed")
	}
	var all []failedAttempt
	for rows.Next() {
		var a failedAttempt
		if rows.Scan(&a.generation, &a.number, &a.delivery, &a.digest, &a.claimed, &a.lease, &a.observed, &a.outcome, &a.code, &a.status) != nil {
			rows.Close()
			return nil, "", 0, "", failure("long_retry_observation_failed")
		}
		all = append(all, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || tx.Commit(s.ctx) != nil {
		return nil, "", 0, "", failure("long_retry_observation_failed")
	}
	return all, state, count, last, nil
}

func validateFailedAttempts(all []failedAttempt, digest string) error {
	if len(all) > 20 {
		return failure("long_retry_twenty_first_claim")
	}
	seen := map[string]bool{}
	for i, a := range all {
		id, err := uuid.Parse(a.delivery)
		if a.generation != i+1 || a.number != i+1 || err != nil || id == uuid.Nil || id.String() != a.delivery || seen[a.delivery] || a.digest != digest || !a.lease.Equal(a.claimed.Add(60*time.Second)) {
			return failure("long_retry_immutable_attempt_invalid")
		}
		seen[a.delivery] = true
		if a.observed != nil && (a.outcome == nil || *a.outcome != "failed" || a.code == nil || *a.code != "network" || a.status != nil || a.observed.Before(a.claimed)) {
			return failure("long_retry_not_actual_network_failure")
		}
		if i > 0 {
			prior := all[i-1]
			if prior.observed == nil || a.claimed.Before(prior.observed.Add(retryDelay(i))) {
				return failure("long_retry_claimed_before_original_backoff")
			}
		}
	}
	return nil
}
