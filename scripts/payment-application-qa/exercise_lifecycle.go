package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

const lifecycleMerchantNamespace int32 = 782193414
const lifecycleCommandNamespace int32 = 782193410

// The offline harness independently computes the already-frozen mock gate. It
// neither imports a private mock helper nor adds a production injection hook.
func lifecycleMockKey(environment, kind, target string) int32 {
	hash := sha256.Sum256([]byte(strings.Join([]string{"moni-payment-lock-v1", "mock", environment, kind, target}, "\x00")))
	return int32(binary.BigEndian.Uint32(hash[:4]))
}

type lifecycleGate struct {
	pool        *pgxpool.Pool
	conn        *pgxpool.Conn
	pid         int32
	databaseOID uint32
	key         int32
	locked      bool
}

func (s *processSession) holdLifecycleGate(order string) (*lifecycleGate, error) {
	pool, err := s.env.OpenPool(s.ctx, "mock_owner")
	if err != nil {
		return nil, failure("lifecycle_maintenance_pool_failed")
	}
	gate := &lifecycleGate{pool: pool, key: lifecycleMockKey(s.env.Protocol.ID, "merchant", "mps1."+s.env.Protocol.ID+"."+order)}
	gate.conn, err = pool.Acquire(s.ctx)
	if err != nil {
		pool.Close()
		return nil, failure("lifecycle_maintenance_connection_failed")
	}
	if gate.conn.QueryRow(s.ctx, `SELECT pg_backend_pid(),(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&gate.pid, &gate.databaseOID) != nil {
		_ = gate.close()
		return nil, failure("lifecycle_maintenance_identity_failed")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	if _, err = gate.conn.Exec(ctx, `SELECT pg_advisory_lock($1::integer,$2::integer)`, lifecycleMerchantNamespace, gate.key); err != nil {
		_ = gate.close()
		return nil, failure("lifecycle_gate_acquisition_failed")
	}
	gate.locked = true
	return gate, nil
}
func (g *lifecycleGate) close() error {
	if g == nil {
		return nil
	}
	var result error
	if g.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if g.locked {
			var unlocked bool
			if g.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1::integer,$2::integer)`, lifecycleMerchantNamespace, g.key).Scan(&unlocked) != nil || !unlocked {
				result = failure("lifecycle_gate_unlock_failed")
			}
		}
		// A session-lock holder is always removed physically, including after an
		// uncertain lock/unlock reply. It can never return a dirty session to a pool.
		raw := g.conn.Hijack()
		g.conn = nil
		g.locked = false
		if raw.Close(ctx) != nil {
			result = failure("lifecycle_maintenance_close_failed")
		}
		cancel()
	}
	if g.pool != nil {
		g.pool.Close()
		g.pool = nil
	}
	return result
}

type lifecycleMutation struct {
	command, digest            string
	canonical                  []byte
	generation                 int64
	until                      time.Time
	waiter                     int32
	barrierAt                  time.Time
	providerQueryAge           time.Duration
	providerObservationStarted time.Time
}

func (s *processSession) lifecycleCreateOrder(order string) error {
	res, err := s.api(http.MethodPost, "/api/wallet/payment-orders", map[string]any{"operation_id": order, "sku_version_id": s.version}, s.owner)
	var receipt struct {
		Status   string  `json:"status"`
		Resource *string `json:"resource_id"`
	}
	if err != nil || res.status != 202 || responseData(res, &receipt) != nil || receipt.Status != "accepted" || receipt.Resource == nil || *receipt.Resource != order {
		return failure("lifecycle_order_create_failed")
	}
	s.orders = append(s.orders, order)
	return nil
}
func (s *processSession) lifecyclePhysicalBarrier(g *lifecycleGate, order string) (lifecycleMutation, error) {
	var mutation lifecycleMutation
	err := waitStage(s.ctx, 10*time.Second, "lifecycle_physical_barrier_timeout", func(ctx context.Context) (bool, error) {
		var state string
		var until *time.Time
		if s.appDB.QueryRow(ctx, `SELECT state,external_command_id,canonical_parameters,parameter_digest,generation,lease_until FROM payment_command_outbox WHERE order_id=$1 AND kind='checkout'`, order).Scan(&state, &mutation.command, &mutation.canonical, &mutation.digest, &mutation.generation, &until) != nil {
			return false, failure("lifecycle_outbox_observation_failed")
		}
		if state != "leased" {
			return false, nil
		}
		if until == nil || mutation.generation < 1 {
			return false, failure("lifecycle_outbox_binding_invalid")
		}
		mutation.until = *until
		hash := sha256.Sum256(mutation.canonical)
		checkout, err := paymentproto.ParseCheckout(mutation.canonical)
		if err != nil || hex.EncodeToString(hash[:]) != mutation.digest || checkout.CommandID != mutation.command || checkout.MerchantOrderID != "mps1."+s.env.Protocol.ID+"."+order || checkout.EnvironmentID != s.env.Protocol.ID || checkout.MerchantID != s.env.Protocol.MerchantID {
			return false, failure("lifecycle_outbox_binding_invalid")
		}
		commandKey := lifecycleMockKey(s.env.Protocol.ID, "command", mutation.command)
		// The actual mock LOGIN may observe its own runtime sessions' detailed
		// pg_stat_activity fields without granting either owner/runtime new ACLs.
		observationStarted := time.Now()
		rows, err := s.mockDB.Query(ctx, `SELECT wait.pid,(extract(epoch FROM (clock_timestamp()-a.query_start))*1000)::bigint FROM pg_locks wait JOIN pg_stat_activity a ON a.pid=wait.pid
 WHERE wait.locktype='advisory' AND wait.database=$1::oid AND wait.classid=$2::oid AND wait.objid=$3::oid AND wait.objsubid=2 AND NOT wait.granted
 AND a.usename=$4 AND a.datid=$1::oid AND a.wait_event_type='Lock' AND a.wait_event='advisory' AND $5::integer=ANY(pg_blocking_pids(wait.pid))
 AND EXISTS(SELECT 1 FROM pg_locks own WHERE own.pid=wait.pid AND own.locktype='advisory' AND own.database=$1::oid AND own.classid=$6::oid AND own.objid=$7::oid AND own.objsubid=2 AND own.granted)`, g.databaseOID, uint32(lifecycleMerchantNamespace), uint32(g.key), s.env.Roles["mock_runtime"], g.pid, uint32(lifecycleCommandNamespace), uint32(commandKey))
		if err != nil {
			return false, failure("lifecycle_lock_observation_failed")
		}
		var count int
		var waited int64
		for rows.Next() {
			var interval int64
			if rows.Scan(&mutation.waiter, &interval) != nil {
				rows.Close()
				return false, failure("lifecycle_lock_observation_failed")
			}
			waited = interval
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil || count > 1 {
			return false, failure("lifecycle_lock_observation_failed")
		}
		if count == 0 {
			return false, nil
		}
		if mutation.waiter == g.pid || waited < 0 || waited >= 2000 {
			return false, failure("lifecycle_barrier_not_before_provider_timeout")
		}
		mutation.providerQueryAge = time.Duration(waited) * time.Millisecond
		mutation.providerObservationStarted = observationStarted
		if err = s.lifecycleNoProviderEffect(order, mutation.command); err != nil {
			return false, err
		}
		mutation.barrierAt = time.Now()
		return true, nil
	})
	return mutation, err
}

func (s *processSession) lifecycleNoProviderEffect(order, command string) error {
	var commands, orders, transitions, captures int
	merchant := "mps1." + s.env.Protocol.ID + "." + order
	if s.mockDB.QueryRow(s.ctx, `SELECT (SELECT count(*) FROM mock_commands WHERE command_id=$1),(SELECT count(*) FROM mock_orders WHERE merchant_order_id=$2),(SELECT count(*) FROM mock_order_transitions WHERE merchant_order_id=$2),(SELECT count(*) FROM mock_captures WHERE merchant_order_id=$2)`, command, merchant).Scan(&commands, &orders, &transitions, &captures) != nil || commands+orders+transitions+captures != 0 {
		return failure("lifecycle_provider_effect_not_zero")
	}
	return nil
}
func (s *processSession) lifecycleProviderCancelled(g *lifecycleGate, m lifecycleMutation) error {
	commandKey := lifecycleMockKey(s.env.Protocol.ID, "command", m.command)
	return waitStage(s.ctx, 2*time.Second, "lifecycle_provider_cancellation_not_proven", func(ctx context.Context) (bool, error) {
		var n int
		if g.conn.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND database=$2::oid AND objsubid=2 AND ((classid=$3::oid AND objid=$4::oid) OR (classid=$5::oid AND objid=$6::oid))`, m.waiter, g.databaseOID, uint32(lifecycleMerchantNamespace), uint32(g.key), uint32(lifecycleCommandNamespace), uint32(commandKey)).Scan(&n) != nil {
			return false, failure("lifecycle_cancellation_observation_failed")
		}
		return n == 0, nil
	})
}
func (s *processSession) lifecycleNoApplicationConnections(g *lifecycleGate) error {
	if s.appDB != nil {
		s.appDB.Close()
		s.appDB = nil
	}
	return waitStage(s.ctx, 5*time.Second, "lifecycle_application_connections_not_zero", func(ctx context.Context) (bool, error) {
		var n int
		if g.conn.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datid=$1::oid AND usename=ANY($2::text[])`, g.databaseOID, []string{s.env.Roles["app_runtime"], s.env.Roles["app_worker"], s.env.Roles["app_projector"]}).Scan(&n) != nil {
			return false, failure("lifecycle_connection_observation_failed")
		}
		return n == 0, nil
	})
}

// The only destructive QA operation revalidates database, actual administrator,
// random APP LOGIN, backend_start and the exact granted single-key lock twice.
// This private method accepts no caller-supplied target PID.
func (s *processSession) lifecycleTerminateOwnedInstance(g *lifecycleGate, m lifecycleMutation, signalAt *time.Time) (result error) {
	if signalAt == nil {
		return failure("lifecycle_signal_clock_missing")
	}
	pool, err := s.env.OpenPool(s.ctx, "app_owner")
	if err != nil {
		return failure("lifecycle_admin_pool_failed")
	}
	defer pool.Close()
	conn, err := pool.Acquire(s.ctx)
	if err != nil {
		return failure("lifecycle_admin_connection_failed")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if conn.Hijack().Close(ctx) != nil {
			result = failure("lifecycle_admin_physical_close_failed")
		}
	}()
	config := conn.Conn().Config()
	if config.Host != "127.0.0.1" || config.Port != 55439 || config.Database != "moni_test" || config.User != "moni_test" {
		return failure("lifecycle_admin_target_invalid")
	}
	if _, err = conn.Exec(s.ctx, `RESET ROLE`); err != nil {
		return failure("lifecycle_admin_identity_failed")
	}
	var current, session, database, address string
	var port int
	var super bool
	var oid uint32
	if conn.QueryRow(s.ctx, `SELECT current_user,session_user,current_database(),host(inet_server_addr()),inet_server_port(),(SELECT rolsuper FROM pg_roles WHERE rolname=current_user),(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&current, &session, &database, &address, &port, &super, &oid) != nil || current != "moni_test" || session != current || database != "moni_test" || address != "127.0.0.1" || port != 55439 || !super || oid != g.databaseOID {
		return failure("lifecycle_admin_identity_failed")
	}
	rows, err := conn.Query(s.ctx, `SELECT a.pid,a.backend_start FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid WHERE a.datid=$1::oid AND a.usename=$2 AND l.locktype='advisory' AND l.database=$1::oid AND l.classid=0 AND l.objid=782193402 AND l.objsubid=1 AND l.granted`, oid, s.env.Roles["app_runtime"])
	if err != nil {
		return failure("lifecycle_instance_target_observation_failed")
	}
	var pid int32
	var started time.Time
	count := 0
	for rows.Next() {
		if rows.Scan(&pid, &started) != nil {
			rows.Close()
			return failure("lifecycle_instance_target_observation_failed")
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil || count != 1 || pid <= 0 || uint32(pid) == conn.Conn().PgConn().PID() || pid == g.pid || pid == m.waiter || started.IsZero() {
		return failure("lifecycle_instance_target_not_unique")
	}
	if time.Since(m.barrierAt) >= 2*time.Second {
		return failure("lifecycle_barrier_expired_before_signal")
	}
	var terminated bool
	*signalAt = time.Now()
	if conn.QueryRow(s.ctx, `SELECT pg_terminate_backend(a.pid) FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid WHERE a.pid=$1 AND a.backend_start=$2 AND a.datid=$3::oid AND a.usename=$4 AND l.locktype='advisory' AND l.database=$3::oid AND l.classid=0 AND l.objid=782193402 AND l.objsubid=1 AND l.granted`, pid, started, oid, s.env.Roles["app_runtime"]).Scan(&terminated) != nil || !terminated {
		return failure("lifecycle_exact_instance_termination_failed")
	}
	return nil
}

func (s *processSession) lifecycleWaitGuardExit() error {
	// A fresh ready request after exact ownership loss cannot share an earlier
	// successful Ping batch. Failure may close the listener before a JSON reply.
	res, err := s.request(s.ctx, http.MethodGet, s.env.ReceiverOrigin+"/health/ready", nil, nil, "", nil)
	if err == nil && res.status == 200 {
		return failure("lifecycle_guard_loss_still_ready")
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-s.app.done:
		s.app.expected = true
		if s.app.err != nil {
			return failure("lifecycle_guard_loss_exit_failed")
		}
		return nil
	case <-s.ctx.Done():
		return failure("qa_cancelled")
	case <-timer.C:
		return failure("lifecycle_guard_loss_process_not_joined")
	}
}

func (s *processSession) lifecycleRecoverOriginal(order string, m lifecycleMutation) error {
	var err error
	s.appDB, err = s.env.OpenPool(s.ctx, "app")
	if err != nil {
		return failure("lifecycle_application_observer_reopen_failed")
	}
	if err = s.lifecycleNoProviderEffect(order, m.command); err != nil {
		return err
	}
	var funded int
	if s.appDB.QueryRow(s.ctx, `SELECT count(*) FROM payment_captures WHERE order_id=$1`, order).Scan(&funded) != nil || funded != 0 {
		return failure("lifecycle_application_effect_not_zero")
	}
	if err = s.startApp("sandbox", false); err != nil {
		return err
	}
	if err = waitStage(s.ctx, 100*time.Second, "lifecycle_natural_lease_recovery_timeout", func(ctx context.Context) (bool, error) {
		var state, command, digest string
		var raw []byte
		var generation int64
		var now time.Time
		if s.appDB.QueryRow(ctx, `SELECT state,external_command_id,parameter_digest,canonical_parameters,generation,clock_timestamp() FROM payment_command_outbox WHERE order_id=$1 AND kind='checkout'`, order).Scan(&state, &command, &digest, &raw, &generation, &now) != nil {
			return false, failure("lifecycle_recovery_observation_failed")
		}
		if command != m.command || digest != m.digest || !bytes.Equal(raw, m.canonical) {
			return false, failure("lifecycle_original_command_changed")
		}
		if state != "completed" {
			if state == "review_required" {
				return false, failure("lifecycle_recovery_required_review")
			}
			return false, nil
		}
		if generation <= m.generation || now.Before(m.until) {
			return false, failure("lifecycle_lease_did_not_expire_naturally")
		}
		return true, nil
	}); err != nil {
		return err
	}
	if err = s.waitReady(order); err != nil {
		return err
	}
	var commands, orders, transitions, captures int
	merchant := "mps1." + s.env.Protocol.ID + "." + order
	if s.mockDB.QueryRow(s.ctx, `SELECT (SELECT count(*) FROM mock_commands WHERE command_id=$1),(SELECT count(*) FROM mock_orders WHERE merchant_order_id=$2),(SELECT count(*) FROM mock_order_transitions WHERE merchant_order_id=$2),(SELECT count(*) FROM mock_captures WHERE merchant_order_id=$2)`, m.command, merchant).Scan(&commands, &orders, &transitions, &captures) != nil || commands != 1 || orders != 1 || transitions != 1 || captures != 0 {
		return failure("lifecycle_recovery_provider_graph_not_unique")
	}
	var notSeen int
	if s.appDB.QueryRow(s.ctx, `SELECT count(*) FROM payment_verified_queries v JOIN payment_query_attempts a ON a.query_id=v.query_id JOIN payment_command_outbox q ON q.id=a.supervisor_id WHERE q.order_id=$1 AND convert_from(v.canonical_body,'UTF8')::jsonb->>'command_state'='not_seen' AND a.mutation_generation=$2`, order, m.generation).Scan(&notSeen) != nil || notSeen < 1 {
		return failure("lifecycle_original_query_not_seen_missing")
	}
	if s.appDB.QueryRow(s.ctx, `SELECT count(*) FROM payment_captures WHERE order_id=$1`, order).Scan(&captures) != nil || captures != 0 {
		return failure("lifecycle_recovery_created_funding")
	}
	return nil
}

func (s *processSession) exerciseLifecycleCase(kind string) (result error) {
	before, err := s.fundingIdentity()
	if err != nil {
		return err
	}
	order := uuid.NewString()
	gate, err := s.holdLifecycleGate(order)
	if err != nil {
		return err
	}
	defer func() {
		if result != nil && childRunning(s.app) {
			_ = stopProcess(s.app, syscall.SIGTERM, true)
		}
		if closeErr := gate.close(); closeErr != nil {
			result = closeErr
		}
	}()
	if err = s.lifecycleCreateOrder(order); err != nil {
		return err
	}
	mutation, err := s.lifecyclePhysicalBarrier(gate, order)
	if err != nil {
		return err
	}
	s.report.pass("lifecycle_" + kind + "_physical_network_wait_observed")
	if time.Since(mutation.barrierAt) >= 2*time.Second {
		return failure("lifecycle_barrier_expired_before_signal")
	}
	triggerAt := time.Now()
	if kind == "sigterm" {
		if err = stopProcess(s.app, syscall.SIGTERM, true); err != nil {
			return err
		}
	} else if kind == "guard_loss" {
		if err = s.lifecycleTerminateOwnedInstance(gate, mutation, &triggerAt); err != nil {
			return err
		}
		if err = s.lifecycleWaitGuardExit(); err != nil {
			return err
		}
	} else {
		return failure("lifecycle_scenario_invalid")
	}
	if err = s.lifecycleProviderCancelled(gate, mutation); err != nil {
		return err
	}
	cancelledMs := time.Since(triggerAt).Milliseconds()
	if cancelledMs >= 2000 {
		return failure("lifecycle_provider_cancellation_too_late")
	}
	if err = s.lifecycleNoApplicationConnections(gate); err != nil {
		return err
	}
	if err = s.lifecycleNoProviderEffect(order, mutation.command); err != nil {
		return err
	}
	// Start before the pg_locks round trip, then include all later zero-effect,
	// administrative, exit and cancellation checks. Independent per-step bounds
	// alone could sum past the provider's existing five-second timeout.
	if mutation.providerObservationStarted.IsZero() || mutation.providerQueryAge < 0 {
		return failure("lifecycle_provider_clock_missing")
	}
	providerTotalAge := mutation.providerQueryAge + time.Since(mutation.providerObservationStarted)
	if providerTotalAge >= 4500*time.Millisecond {
		return failure("lifecycle_provider_timeout_cannot_be_excluded")
	}
	_, _ = fmt.Fprintln(s.report.out, "lifecycle_"+kind+"_barrier_to_trigger_ms", triggerAt.Sub(mutation.barrierAt).Milliseconds())
	_, _ = fmt.Fprintln(s.report.out, "lifecycle_"+kind+"_provider_cancel_ms", cancelledMs)
	_, _ = fmt.Fprintln(s.report.out, "lifecycle_"+kind+"_provider_total_age_ms", providerTotalAge.Milliseconds())
	s.report.pass("lifecycle_" + kind + "_app_join_zero_connections_provider_cancel_before_timeout")
	if err = gate.close(); err != nil {
		return err
	}
	recoveryAt := time.Now()
	if err = s.lifecycleRecoverOriginal(order, mutation); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(s.report.out, "lifecycle_"+kind+"_natural_lease_recovery_ms", time.Since(recoveryAt).Milliseconds())
	s.report.pass("lifecycle_" + kind + "_natural_lease_original_command_query_recovery")
	after, err := s.fundingIdentity()
	if err != nil || after != before {
		return failure("lifecycle_confirmed_funding_identity_changed")
	}
	if err = s.assertUniqueFunds(4); err != nil {
		return err
	}
	if err = s.env.VerifyLegacyFundingUnchanged(s.ctx); err != nil {
		return failure("lifecycle_retained_a_changed")
	}
	s.report.pass("lifecycle_" + kind + "_original_funding_unchanged")
	return nil
}
func (s *processSession) exerciseLifecycle() error {
	for _, kind := range []string{"sigterm", "guard_loss"} {
		if err := s.exerciseLifecycleCase(kind); err != nil {
			return err
		}
	}
	return nil
}
