package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/mockpsp"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func (s *session) startPair() error {
	var err error
	s.receiver, err = s.start("receiver")
	if err != nil {
		return err
	}
	if err = s.ready(s.receiver, s.env.ReceiverOrigin); err != nil {
		return err
	}
	s.mock, err = s.start("mock")
	if err != nil {
		return err
	}
	return s.ready(s.mock, s.env.APIOrigin, s.env.ControlOrigin)
}

func (s *session) short() error {
	if err := s.startPair(); err != nil {
		return err
	}
	s.report.pass("actual_mock_receiver_processes_and_three_listeners")
	for _, component := range []string{"mock", "receiver"} {
		if err := s.duplicateRefused(component); err != nil {
			return err
		}
	}
	s.report.pass("both_second_instances_refused")
	q, raw, err := s.checkoutInput()
	if err != nil {
		return err
	}
	unauthorized, err := s.post(s.env.APIOrigin, "/v1/checkout", "", raw, nil)
	if err != nil || unauthorized.status != 401 || unauthorized.dropped {
		return failure("unauthenticated_checkout_not_refused")
	}
	ready, original, err := s.checkout(raw)
	if err != nil || ready.CommandState != "completed" || ready.Result == nil || ready.Result.Status != "ready" {
		return failure("checkout_not_ready")
	}
	_, replay, err := s.checkout(raw)
	if err != nil || !bytes.Equal(original, replay) {
		return failure("checkout_replay_changed")
	}
	query, queryRaw, err := s.queryInput(q, raw)
	if err != nil {
		return err
	}
	queryReady, queryOriginal, err := s.query(query, queryRaw)
	if err != nil || queryReady.Result == nil || queryReady.Result.Status != "ready" {
		return failure("query_ready_failed")
	}
	s.report.pass("http_checkout_replay_and_independent_query_signature_binding")
	capture := mockpsp.ControlInput{OperationID: uuid.NewString(), Action: "capture_order", Scenario: "normal", MerchantOrderID: &q.MerchantOrderID}
	control, controlOriginal, err := s.control(capture)
	if err != nil {
		return err
	}
	_, controlReplay, err := s.control(capture)
	if err != nil || !bytes.Equal(controlOriginal, controlReplay) {
		return failure("capture_control_replay_changed")
	}
	var event, eventRow string
	var eventBody []byte
	if s.mockDB.QueryRow(s.ctx, `SELECT event_id,id,canonical_body FROM mock_notification_events WHERE canonical_capture_id=$1`, *control.ResourceID).Scan(&event, &eventRow, &eventBody) != nil {
		return failure("canonical_capture_event_missing")
	}
	capturedQuery, capturedRaw, err := s.queryInput(q, raw)
	if err != nil {
		return err
	}
	captured, _, err := s.query(capturedQuery, capturedRaw)
	if err != nil || captured.Result == nil || captured.Result.Status != "captured" || captured.Result.Capture == nil || captured.Result.Capture.Amount != "100" {
		return failure("captured_query_failed")
	}
	_, queryReplay, err := s.query(query, queryRaw)
	if err != nil || !bytes.Equal(queryOriginal, queryReplay) {
		return failure("old_query_snapshot_changed")
	}
	if err = s.waitDelivered(eventRow); err != nil {
		return err
	}
	if err = s.captureCounts(q.MerchantOrderID, eventRow); err != nil {
		return err
	}
	s.report.pass("capture_unique_and_frozen_query_snapshot")
	if err = s.receiptReplay(event, eventBody); err != nil {
		return err
	}
	s.report.pass("actual_callback_committed_ack_and_stable_duplicate_receipt")
	if err = s.disconnects(); err != nil {
		return err
	}
	s.report.pass("before_command_rollback_after_command_query_http_reply_drop_recovery")
	baseline, err := s.snapshot()
	if err != nil {
		return err
	}
	for _, mode := range []string{"sigterm", "sigkill"} {
		signal, graceful := syscall.SIGTERM, true
		if mode == "sigkill" {
			signal, graceful = syscall.SIGKILL, false
		}
		if err = stopChild(s.mock, signal, graceful); err != nil {
			return err
		}
		if err = stopChild(s.receiver, signal, graceful); err != nil {
			return err
		}
		if err = s.startPair(); err != nil {
			return err
		}
		_, replay, err = s.checkout(raw)
		if err != nil || !bytes.Equal(original, replay) {
			return failure("restart_command_receipt_changed")
		}
		_, queryReplay, err = s.query(query, queryRaw)
		if err != nil || !bytes.Equal(queryOriginal, queryReplay) {
			return failure("restart_query_snapshot_changed")
		}
		_, controlReplay, err = s.control(capture)
		if err != nil || !bytes.Equal(controlOriginal, controlReplay) {
			return failure("restart_capture_control_changed")
		}
		if err = s.receiptReplay(event, eventBody); err != nil {
			return err
		}
		after, err := s.snapshot()
		if err != nil || after != baseline {
			return failure("restart_persistent_facts_changed")
		}
		s.report.pass(mode + "_restart_same_manifest_after_confirmed_commits")
	}
	return nil
}

func (s *session) waitDelivered(eventRow string) error {
	ctx, stop := context.WithTimeout(s.ctx, 15*time.Second)
	defer stop()
	return eventually(ctx, 50*time.Millisecond, func() (bool, error) {
		var state string
		if s.mockDB.QueryRow(ctx, `SELECT state FROM mock_notification_delivery WHERE event_id=$1`, eventRow).Scan(&state) != nil {
			return false, failure("delivery_observation_failed")
		}
		if state == "review_required" {
			return false, failure("normal_callback_reached_review")
		}
		return state == "delivered", nil
	})
}

func (s *session) captureCounts(order, eventRow string) error {
	var captures, events, transitions, attempts, observations, acks int
	if s.mockDB.QueryRow(s.ctx, `SELECT
 (SELECT count(*) FROM mock_captures WHERE merchant_order_id=$1),
 (SELECT count(*) FROM mock_notification_events WHERE merchant_order_id=$1),
 (SELECT count(*) FROM mock_order_transitions WHERE merchant_order_id=$1),
 (SELECT count(*) FROM mock_delivery_attempts WHERE event_id=$2),
 (SELECT count(*) FROM mock_delivery_observations WHERE event_id=$2),
 (SELECT count(*) FROM mock_delivery_observations WHERE event_id=$2 AND outcome='ack' AND http_status IN(200,202))`, order, eventRow).Scan(&captures, &events, &transitions, &attempts, &observations, &acks) != nil || captures != 1 || events != 1 || transitions != 2 || attempts != 1 || observations != 1 || acks != 1 {
		return failure("capture_callback_facts_not_unique")
	}
	return nil
}

func (s *session) receiptReplay(event string, body []byte) error {
	var retained []byte
	var inboxes, receipts int
	if s.receiverDB.QueryRow(s.ctx, `SELECT r.receipt_data,(SELECT count(*) FROM receiver_inbox WHERE event_id=$1),(SELECT count(*) FROM receiver_receipts x JOIN receiver_inbox i ON i.id=x.inbox_id WHERE i.event_id=$1) FROM receiver_receipts r JOIN receiver_inbox i ON i.id=r.inbox_id WHERE i.event_id=$1`, event).Scan(&retained, &inboxes, &receipts) != nil || inboxes != 1 || receipts != 1 {
		return failure("receiver_committed_receipt_missing")
	}
	priorRequest := ""
	for i := 0; i < 2; i++ {
		h, err := s.signedNotification(body)
		if err != nil {
			return err
		}
		res, err := s.post(s.env.ReceiverOrigin, "/api/payments/sandbox/notifications", "", body, &h)
		if err != nil || res.status != 200 || res.dropped {
			return failure("duplicate_notification_http_failed")
		}
		var envelope mockpsp.ReceiptEnvelope
		if json.Unmarshal(res.body, &envelope) != nil || envelope.RequestID == priorRequest {
			return failure("receipt_request_identity_invalid")
		}
		var now time.Time
		if s.receiverDB.QueryRow(s.ctx, "SELECT clock_timestamp()").Scan(&now) != nil {
			return failure("database_clock_failed")
		}
		ack, err := mockpsp.ParseACK(res.status, res.body, event, now.UTC())
		if err != nil {
			return failure("duplicate_receipt_invalid")
		}
		data, err := ack.DataBytes()
		if err != nil || !bytes.Equal(data, retained) {
			return failure("duplicate_receipt_changed")
		}
		priorRequest = envelope.RequestID
	}
	return nil
}

func (s *session) disconnects() error {
	for _, boundary := range []string{"before_command_commit_disconnect_once", "after_command_commit_drop_reply_once"} {
		q, raw, err := s.checkoutInput()
		if err != nil {
			return err
		}
		plan := mockpsp.ControlInput{OperationID: uuid.NewString(), Action: "arm_fault", Scenario: boundary, CommandID: &q.CommandID}
		if _, _, err = s.control(plan); err != nil {
			return err
		}
		res, err := s.post(s.env.APIOrigin, "/v1/checkout", s.env.APIToken(), raw, nil)
		if err != nil || !res.dropped {
			return failure("planned_command_connection_not_dropped")
		}
		before := 0
		if boundary == "after_command_commit_drop_reply_once" {
			before = 1
		}
		if err = s.commandCounts(q, plan.OperationID, before); err != nil {
			return err
		}
		out, original, err := s.checkout(raw)
		if err != nil || out.Result == nil || out.Result.Status != "ready" {
			return failure("dropped_command_original_id_recovery_failed")
		}
		_, repeated, err := s.checkout(raw)
		if err != nil || !bytes.Equal(original, repeated) {
			return failure("dropped_command_replay_changed")
		}
		if err = s.commandCounts(q, plan.OperationID, 1); err != nil {
			return err
		}
		query, queryRaw, err := s.queryInput(q, raw)
		if err != nil {
			return err
		}
		plan = mockpsp.ControlInput{OperationID: uuid.NewString(), Action: "arm_fault", Scenario: "after_query_snapshot_commit_drop_reply_once", QueryID: &query.QueryID}
		if _, _, err = s.control(plan); err != nil {
			return err
		}
		res, err = s.post(s.env.APIOrigin, "/v1/query", s.env.APIToken(), queryRaw, nil)
		if err != nil || !res.dropped {
			return failure("planned_query_connection_not_dropped")
		}
		var snapshot []byte
		var count, remaining int
		if s.mockDB.QueryRow(s.ctx, `SELECT snapshot_body,(SELECT count(*) FROM mock_queries WHERE query_id=$1),(SELECT remaining FROM mock_fault_plans WHERE control_operation_id=$2) FROM mock_queries WHERE query_id=$1`, query.QueryID, plan.OperationID).Scan(&snapshot, &count, &remaining) != nil || count != 1 || remaining != 0 {
			return failure("dropped_query_snapshot_not_committed")
		}
		_, recovered, err := s.query(query, queryRaw)
		if err != nil || !bytes.Equal(snapshot, recovered) {
			return failure("dropped_query_original_id_recovery_failed")
		}
	}
	return nil
}

func (s *session) commandCounts(q paymentproto.CheckoutRequest, plan string, expected int) error {
	var commands, orders, transitions, finals, remaining int
	var consumed bool
	if s.mockDB.QueryRow(s.ctx, `SELECT
 (SELECT count(*) FROM mock_commands WHERE command_id=$1),
 (SELECT count(*) FROM mock_orders WHERE merchant_order_id=$2),
 (SELECT count(*) FROM mock_order_transitions WHERE merchant_order_id=$2),
 (SELECT count(*) FROM mock_command_results WHERE command_id=$1),
 remaining,consumed_at IS NOT NULL FROM mock_fault_plans WHERE control_operation_id=$3`, q.CommandID, q.MerchantOrderID, plan).Scan(&commands, &orders, &transitions, &finals, &remaining, &consumed) != nil || commands != expected || orders != expected || transitions != expected || finals != expected || remaining != 0 || !consumed {
		return failure("disconnect_durable_boundary_incorrect")
	}
	return nil
}

func (s *session) snapshot() (string, error) {
	h := sha256.New()
	for _, component := range []string{"mock", "receiver"} {
		pool := s.mockDB
		if component == "receiver" {
			pool = s.receiverDB
		}
		if err := hashTables(s.ctx, pool, s.env.Schemas[component], h.Write); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashTables(ctx context.Context, pool *pgxpool.Pool, schema string, write func([]byte) (int, error)) error {
	rows, err := pool.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind='r' ORDER BY c.relname`, schema)
	if err != nil {
		return failure("snapshot_catalog_failed")
	}
	var tables []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			rows.Close()
			return failure("snapshot_catalog_failed")
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(tables) == 0 {
		return failure("snapshot_catalog_failed")
	}
	for _, table := range tables {
		var raw string
		if pool.QueryRow(ctx, `SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM `+pgx.Identifier{schema, table}.Sanitize()+` t`).Scan(&raw) != nil {
			return failure("persistent_snapshot_failed")
		}
		_, _ = write([]byte(schema + "\x00" + table + "\x00" + raw + "\x00"))
	}
	return nil
}
