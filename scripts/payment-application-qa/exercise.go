package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/mockpsp"
)

func (s *processSession) setup() error {
	var err error
	s.admin, err = s.login("admin")
	if err != nil {
		return err
	}
	s.owner, err = s.login("owner")
	if err != nil {
		return err
	}
	s.report.pass("real_cookie_csrf_admin_owner_optional_mfa")
	s.sku = uuid.NewString()
	res, err := s.api(http.MethodPost, "/api/platform/payment-skus", map[string]any{"operation_id": uuid.NewString(), "sku_id": s.sku, "expected_revision": "0", "display_name": "Process QA Sandbox Credit", "amount": "250"}, s.admin)
	var receipt struct {
		Status   string  `json:"status"`
		Resource *string `json:"resource_id"`
	}
	if err != nil || res.status != 201 || responseData(res, &receipt) != nil || receipt.Status != "accepted" || receipt.Resource == nil {
		return failure("real_sku_publish_failed")
	}
	s.version = *receipt.Resource
	res, err = s.api(http.MethodPut, "/api/platform/payment-skus/"+s.sku+"/active", map[string]any{"operation_id": uuid.NewString(), "expected_revision": "1", "version_id": s.version, "enabled": true}, s.admin)
	if err != nil || res.status != 200 || responseData(res, &receipt) != nil || receipt.Status != "accepted" {
		return failure("real_sku_activate_failed")
	}
	res, err = s.api(http.MethodPut, "/api/platform/payment-policy", map[string]any{"expected_revision": "1", "enabled": true}, s.admin)
	var policy struct {
		Effective bool `json:"effective_enabled"`
	}
	if err != nil || res.status != 200 || responseData(res, &policy) != nil || !policy.Effective {
		return failure("real_payment_policy_failed")
	}
	s.report.pass("http_publish_activate_sku_and_policy")
	return nil
}
func (s *processSession) exercise() error {
	if err := s.startMock(); err != nil {
		return err
	}
	if err := s.startApp("sandbox", false); err != nil {
		return err
	}
	s.report.pass("real_main_mock_http_readiness")
	duplicate, err := s.start("app", "sandbox", false, true)
	if err != nil {
		return err
	}
	if err = s.refused(duplicate, "another active instance holds the service lock"); err != nil {
		return err
	}
	s.report.pass("second_main_refused_by_instance_lock")
	if err = s.setup(); err != nil {
		return err
	}
	first, err := s.createOrder()
	if err != nil {
		return err
	}
	if err = s.waitReady(first); err != nil {
		return err
	}
	if err = s.capture(first); err != nil {
		return err
	}
	if err = s.waitFunded(first); err != nil {
		return err
	}
	if err = s.assertUniqueFunds(1); err != nil {
		return err
	}
	if err = s.assertWallet("250"); err != nil {
		return err
	}
	if err = s.duplicateNotification(first); err != nil {
		return err
	}
	replay, err := s.api(http.MethodPost, "/api/wallet/payment-orders", map[string]any{"operation_id": first, "sku_version_id": s.version}, s.owner)
	var receipt struct {
		Status   string  `json:"status"`
		Resource *string `json:"resource_id"`
	}
	if err != nil || replay.status != 200 || responseData(replay, &receipt) != nil || receipt.Status != "accepted" || receipt.Resource == nil || *receipt.Resource != first {
		return failure("original_order_operation_replay_failed")
	}
	if err = s.assertUniqueFunds(1); err != nil {
		return err
	}
	s.report.pass("real_callback_worker_unique_funding_duplicate_ack_operation_replay")

	// Keep one genuine ready order for Recovery's queued-evidence boundary.
	recoveryOrder, err := s.createOrder()
	if err != nil {
		return err
	}
	if err = s.waitReady(recoveryOrder); err != nil {
		return err
	}
	if err = stopProcess(s.mock, syscall.SIGTERM, true); err != nil {
		return err
	}
	unknown, err := s.createOrder()
	if err != nil {
		return err
	}
	var command string
	if err = waitStage(s.ctx, 20*time.Second, "network_unknown_not_recorded", func(ctx context.Context) (bool, error) {
		var state string
		if s.appDB.QueryRow(ctx, `SELECT state,external_command_id FROM payment_command_outbox WHERE order_id=$1 AND kind='checkout'`, unknown).Scan(&state, &command) != nil {
			return false, failure("unknown_observation_failed")
		}
		return state == "submission_unknown", nil
	}); err != nil {
		return err
	}
	if err = s.stopApp(syscall.SIGTERM, true); err != nil {
		return err
	}
	if err = s.startMock(); err != nil {
		return err
	}
	plan := mockpsp.ControlInput{OperationID: uuid.NewString(), Action: "arm_fault", Scenario: "after_command_commit_drop_reply_once", CommandID: &command}
	if _, err = s.control(plan); err != nil {
		return err
	}
	if err = s.capture(recoveryOrder); err != nil {
		return err
	}
	if err = s.startApp("sandbox", true); err != nil {
		return err
	}
	if err = waitStage(s.ctx, 30*time.Second, "recovery_query_and_evidence_not_observed", func(ctx context.Context) (bool, error) {
		var queries, evidence int
		if s.appDB.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_verified_queries v JOIN payment_query_attempts a ON a.query_id=v.query_id JOIN payment_command_outbox q ON q.id=a.supervisor_id WHERE q.order_id=$1),(SELECT count(*) FROM payment_verified_evidence WHERE order_id=$2 AND kind='capture')`, unknown, recoveryOrder).Scan(&queries, &evidence) != nil {
			return false, failure("recovery_observation_failed")
		}
		return queries > 0 && evidence > 0, nil
	}); err != nil {
		return err
	}
	if err = s.observeRecoveryQuiet(unknown, recoveryOrder); err != nil {
		return err
	}
	denied, err := s.api(http.MethodPost, "/api/wallet/payment-orders", map[string]any{"operation_id": uuid.NewString(), "sku_version_id": s.version}, s.owner)
	if err != nil || denied.status != 503 || errorCode(denied) != "recovery_mode" {
		return failure("recovery_http_write_not_refused")
	}
	if err = s.assertUniqueFunds(1); err != nil {
		return err
	}
	s.report.pass("recovery_real_query_only_no_mutation_no_evidence_funding")
	if err = s.stopApp(syscall.SIGTERM, true); err != nil {
		return err
	}
	if err = s.startApp("sandbox", false); err != nil {
		return err
	}
	if err = s.waitReady(unknown); err != nil {
		return err
	}
	if err = s.waitFunded(recoveryOrder); err != nil {
		return err
	}
	var remaining, commands, orders, transitions, queries int
	merchant := "mps1." + s.env.Protocol.ID + "." + unknown
	if s.mockDB.QueryRow(s.ctx, `SELECT
 (SELECT remaining FROM mock_fault_plans WHERE control_operation_id=$1),
 (SELECT count(*) FROM mock_commands WHERE command_id=$2),
 (SELECT count(*) FROM mock_orders WHERE merchant_order_id=$3),
 (SELECT count(*) FROM mock_order_transitions WHERE merchant_order_id=$3),
 (SELECT count(*) FROM mock_queries WHERE command_id=$2)`, plan.OperationID, command, merchant).Scan(&remaining, &commands, &orders, &transitions, &queries) != nil || remaining != 0 || commands != 1 || orders != 1 || transitions != 1 || queries < 2 {
		return failure("unknown_command_query_closure_not_proven")
	}
	if err = s.replayFrozenQuery(unknown); err != nil {
		return err
	}
	if err = s.capture(unknown); err != nil {
		return err
	}
	if err = s.waitFunded(unknown); err != nil {
		return err
	}
	if err = s.assertUniqueFunds(3); err != nil {
		return err
	}
	if err = s.assertWallet("750"); err != nil {
		return err
	}
	s.report.pass("provider_commit_http_reply_drop_original_command_query_recovery")
	s.report.pass("actual_worker_query_frozen_replay_and_original_a_unchanged")

	offOrder, err := s.createOrder()
	if err != nil {
		return err
	}
	if err = s.waitReady(offOrder); err != nil {
		return err
	}
	if err = s.stopApp(syscall.SIGTERM, true); err != nil {
		return err
	}
	if err = s.capture(offOrder); err != nil {
		return err
	}
	if err = s.startApp("off", false); err != nil {
		return err
	}
	if err = s.waitFunded(offOrder); err != nil {
		return err
	}
	denied, err = s.api(http.MethodPost, "/api/wallet/payment-orders", map[string]any{"operation_id": uuid.NewString(), "sku_version_id": s.version}, s.owner)
	if err != nil || denied.status != 409 || errorCode(denied) != "payments_runtime_disabled" {
		return failure("attested_off_new_order_not_refused")
	}
	if err = s.assertUniqueFunds(4); err != nil {
		return err
	}
	if err = s.assertWallet("1000"); err != nil {
		return err
	}
	before, err := s.fundingIdentity()
	if err != nil {
		return err
	}
	if err = s.stopApp(syscall.SIGTERM, true); err != nil {
		return err
	}
	if err = s.startApp("off", false); err != nil {
		return err
	}
	after, err := s.fundingIdentity()
	if err != nil || after != before {
		return failure("sigterm_restart_funding_changed")
	}
	if err = s.stopApp(syscall.SIGKILL, false); err != nil {
		return err
	}
	if err = s.startApp("off", false); err != nil {
		return err
	}
	after, err = s.fundingIdentity()
	if err != nil || after != before {
		return failure("sigkill_confirmed_commit_funding_changed")
	}
	if err = s.assertUniqueFunds(4); err != nil {
		return err
	}
	if err = s.assertWallet("1000"); err != nil {
		return err
	}
	s.report.pass("attested_off_historical_capture_closed_new_orders_refused")
	s.report.pass("sigterm_drain_sigkill_after_confirmed_commit_same_funding_identity")
	if err = s.stopApp(syscall.SIGTERM, true); err != nil {
		return err
	}
	ordinary, err := s.start("app", "off", false, false)
	if err != nil {
		return err
	}
	if err = s.refused(ordinary, "payment service initialization failed"); err != nil {
		return err
	}
	after, err = s.fundingIdentity()
	if err != nil || after != before {
		return failure("ordinary_off_refusal_changed_funding")
	}
	s.report.pass("ordinary_off_with_sandbox_history_refused_before_listener_worker")
	if err = s.startApp("sandbox", false); err != nil {
		return err
	}
	if err = s.exerciseLifecycle(); err != nil {
		return err
	}
	if err = s.env.VerifyLegacyFundingUnchanged(s.ctx); err != nil {
		return failure("retained_a_funding_changed")
	}
	s.report.skip("database_commit_reply_loss_no_wire_fault_facility")
	return nil
}

func (s *processSession) observeRecoveryQuiet(unknown, pending string) error {
	until := time.Now().Add(3 * time.Second)
	return waitStage(s.ctx, 5*time.Second, "recovery_quiet_window_failed", func(ctx context.Context) (bool, error) {
		if !childRunning(s.app) {
			return false, failure("recovery_process_stopped")
		}
		var captures, pspCommands int
		if s.appDB.QueryRow(ctx, `SELECT count(*) FROM payment_captures WHERE order_id=$1`, pending).Scan(&captures) != nil || s.mockDB.QueryRow(ctx, `SELECT count(*) FROM mock_commands WHERE merchant_order_id=$1`, "mps1."+s.env.Protocol.ID+"."+unknown).Scan(&pspCommands) != nil {
			return false, failure("recovery_quiet_observation_failed")
		}
		if captures != 0 || pspCommands != 0 {
			return false, failure("recovery_dispatched_or_funded")
		}
		return !time.Now().Before(until), nil
	})
}
func (s *processSession) fundingIdentity() (string, error) {
	var hash string
	if s.appDB.QueryRow(s.ctx, `SELECT encode(sha256(convert_to(coalesce(jsonb_agg(jsonb_build_array(c.order_id,c.id,f.id,f.journal_id) ORDER BY c.order_id),'[]'::jsonb)::text,'UTF8')),'hex') FROM payment_captures c JOIN funding_receipts f ON f.source_kind='sandbox_capture' AND f.source_id=c.id WHERE c.tenant_id=$1`, s.env.ApplicationTenantID).Scan(&hash) != nil {
		return "", failure("funding_identity_observation_failed")
	}
	return hash, nil
}

func (s *processSession) browserHold() error {
	type credential struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	state := struct {
		Origin      string                `json:"origin"`
		TenantID    string                `json:"tenant_id"`
		SKUID       string                `json:"sku_id"`
		VersionID   string                `json:"sku_version_id"`
		OrderIDs    []string              `json:"order_ids"`
		Credentials map[string]credential `json:"credentials"`
	}{Origin: s.env.ReceiverOrigin, TenantID: s.env.ApplicationTenantID, SKUID: s.sku, VersionID: s.version, OrderIDs: s.orders, Credentials: map[string]credential{}}
	for _, actor := range []string{"admin", "owner", "billing_viewer", "member", "finance_operator", "finance_approver"} {
		email, password, err := s.env.Credentials(actor)
		if err != nil {
			return failure("browser_actor_failed")
		}
		state.Credentials[actor] = credential{email, password}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return failure("browser_state_encoding_failed")
	}
	path, stop := filepath.Join(s.root, "browser-state.json"), filepath.Join(s.root, "browser-stop")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return failure("browser_state_file_failed")
	}
	_, err = file.Write(raw)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return failure("browser_state_file_failed")
	}
	_, _ = fmt.Fprintln(s.report.out, "browser_origin", state.Origin)
	_, _ = fmt.Fprintln(s.report.out, "browser_state_file", path)
	_, _ = fmt.Fprintln(s.report.out, "browser_stop_file", stop)
	s.report.pass("browser_window_ready")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case <-ticker.C:
			if _, err = os.Stat(stop); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return failure("browser_stop_observation_failed")
			}
			if !childRunning(s.app) || !childRunning(s.mock) {
				return failure("browser_window_process_stopped")
			}
		}
	}
}
