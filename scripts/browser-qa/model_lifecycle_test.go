package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/procurement"
)

// Only fixed codes, counts, money and booleans can enter this safe evidence.
// Identifiers, request/response bodies, credentials and driver errors stay private.
type modelProcessEvidence struct {
	Window                        string `json:"window"`
	BinarySHA256                  string `json:"binary_sha256"`
	PhysicalPOSTBarrier           bool   `json:"physical_post_barrier"`
	ClientReadContent             bool   `json:"client_read_content"`
	FinalEvidenceCommitted        bool   `json:"final_evidence_committed"`
	PhysicalDatabaseWaiter        bool   `json:"physical_database_waiter"`
	WaitAgeMS                     int64  `json:"database_wait_age_ms"`
	ReleaseToKillMS               int64  `json:"release_to_kill_exit_upper_ms"`
	SIGKILLConfirmed              bool   `json:"sigkill_confirmed"`
	ReplacementReady              bool   `json:"replacement_ready"`
	Original300SecondWindow       bool   `json:"original_300_second_window"`
	OriginalDeadlinePreserved     bool   `json:"original_deadline_preserved"`
	ReadyAfterPredecessorDeadline bool   `json:"ready_after_predecessor_deadline"`
	CustomerStatus                string `json:"customer_status"`
	CustomerCharged               int64  `json:"customer_charged"`
	CustomerHeld                  int64  `json:"customer_held"`
	ProcurementStatus             string `json:"procurement_status"`
	ProcurementHeld               int64  `json:"procurement_held"`
	ProcurementSpent              int64  `json:"procurement_spent"`
	Posts                         uint64 `json:"posts"`
	Attempts                      int    `json:"attempts"`
	Finalizations                 int    `json:"finalizations"`
	Settlements                   int    `json:"settlements"`
	ReplayNoNewFacts              bool   `json:"replay_no_new_facts"`
	GracefulFinalShutdown         bool   `json:"graceful_final_shutdown"`
	NoRolesSchemasOrConnections   bool   `json:"no_roles_schemas_or_connections"`
	ListenersClosed               bool   `json:"listeners_closed"`
	PrivateFilesRemoved           bool   `json:"private_files_removed"`
	ElapsedMS                     int64  `json:"elapsed_ms"`
}

func modelLifecycleDatabase(raw string) (*pgxpool.Config, error) {
	parsed, err := pgxpool.ParseConfig(raw)
	if raw == "" || err != nil || parsed.ConnConfig.Host != "127.0.0.1" || parsed.ConnConfig.Port != 55439 || parsed.ConnConfig.Database != "moni_test" {
		return nil, stage("model_lifecycle_exact_test_database_required")
	}
	for _, fallback := range parsed.ConnConfig.Fallbacks {
		if fallback.Host != "127.0.0.1" || fallback.Port != 55439 {
			return nil, stage("model_lifecycle_exact_test_database_required")
		}
	}
	return testDatabase(raw)
}

func TestModelLifecycleRejectsOtherDatabasesBeforeIO(t *testing.T) {
	for _, raw := range []string{"", "invalid", "postgres://qa:private-marker@localhost:55439/moni_test", "postgres://qa:private-marker@127.0.0.1:5432/moni_test", "postgres://qa:private-marker@[::1]:55439/moni_test", "postgres://qa:private-marker@127.0.0.1:55439/moni", "postgres://qa:private-marker@127.0.0.1:55439/moni_test?host=127.0.0.1,localhost"} {
		if _, err := modelLifecycleDatabase(raw); err == nil || strings.Contains(err.Error(), "private-marker") || strings.Contains(err.Error(), "postgres://") {
			t.Fatal("model lifecycle accepted unsafe database or reflected secret")
		}
	}
	if _, err := modelLifecycleDatabase("postgres://qa@127.0.0.1:55439/moni_test?sslmode=disable"); err != nil {
		t.Fatal(err)
	}
}

type modelBusiness struct{ tenant, key, token, account string }

func setupModelBusiness(ctx context.Context, live *privateModelLifecycle) (modelBusiness, error) {
	var b modelBusiness
	var admin, operator, approver string
	if live.owner.QueryRow(ctx, `SELECT (SELECT id FROM users WHERE platform_role='admin'),(SELECT id FROM users WHERE platform_role='finance_operator'),(SELECT id FROM users WHERE platform_role='finance_approver')`).Scan(&admin, &operator, &approver) != nil {
		return b, stage("model_setup_actors_failed")
	}
	c := catalog.New(live.owner, live.secrets)
	model, err := c.CreateModel(ctx, admin, catalog.ModelInput{PublicName: "lifecycle-model", ContextLimit: 120, InputUpperBound: 100, OutputLimit: 20, DefaultOutputLimit: 16})
	if err != nil {
		return b, stage("model_setup_profile_failed")
	}
	i := identity.New(live.owner, live.secrets)
	password, err := randomPassword()
	if err != nil {
		return b, err
	}
	b.tenant, err = i.CreateTenant(ctx, admin, "Synthetic model lifecycle", uuid.NewString()+"@model-lifecycle.invalid", password, []string{model})
	if err != nil {
		return b, stage("model_setup_tenant_failed")
	}
	var user string
	if live.owner.QueryRow(ctx, `SELECT user_id FROM memberships WHERE tenant_id=$1 AND role='owner'`, b.tenant).Scan(&user) != nil {
		return b, stage("model_setup_owner_failed")
	}
	limit := "1000"
	key, err := i.CreateKey(ctx, &identity.Session{User: identity.User{ID: user}, Tenant: &identity.Tenant{ID: b.tenant, Role: "owner"}}, identity.KeyInput{Name: "Lifecycle only", Scopes: []string{model}, Limit: &limit})
	if err != nil {
		return b, stage("model_setup_key_failed")
	}
	b.key, _ = key["id"].(string)
	b.token, _ = key["key"].(string)
	if b.key == "" || b.token == "" {
		return b, stage("model_setup_key_empty")
	}
	var schemaID, schemaDigest string
	if err = live.owner.QueryRow(ctx, `SELECT c.schema_id,encode(c.schema_digest,'hex') FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id WHERE m.id=$1`, model).Scan(&schemaID, &schemaDigest); err != nil {
		return b, stage("model_setup_schema_failed")
	}
	if _, err = c.PublishDefaultPrice(ctx, catalog.PricingActor{UserID: admin}, catalog.DefaultPriceCommand{OperationID: uuid.NewString(), ModelID: model, SchemaID: schemaID, SchemaDigest: schemaDigest, VariantKey: "default", InputRate: 1000000, OutputRate: 1000000, EffectiveAt: time.Now().UTC().Truncate(time.Microsecond)}); err != nil {
		return b, stage("model_setup_price_failed")
	}
	channel, err := c.SaveChannel(ctx, admin, "", catalog.ChannelInput{Name: "Lifecycle loopback", BaseURL: live.provider.BaseURL, Secret: live.provider.Secret, Region: "global", AllowedCIDRs: []string{"127.0.0.1/32"}})
	if err != nil {
		return b, stage("model_setup_channel_failed")
	}
	var credential, revision string
	if live.owner.QueryRow(ctx, `SELECT v.credential_id,c.management_revision::text FROM channels c JOIN channel_versions v ON v.id=c.current_version WHERE c.id=$1`, channel).Scan(&credential, &revision) != nil {
		return b, stage("model_setup_credential_failed")
	}
	p := procurement.New(live.owner)
	account, err := p.CreateAccount(ctx, admin, procurement.CreateInput{OperationID: uuid.NewString(), Label: "Lifecycle synthetic purchase", Provider: "local-fixture", State: "active", LimitAmount: "1000"})
	if err != nil || account.Status != "applied" {
		return b, stage("model_setup_procurement_failed")
	}
	b.account = account.TargetID
	if receipt, err := p.BindCredential(ctx, admin, procurement.BindingInput{OperationID: uuid.NewString(), ChannelID: channel, CredentialID: credential, AccountID: b.account, ExpectedChannelRevision: revision}); err != nil || receipt.Status != "applied" {
		return b, stage("model_setup_binding_failed")
	}
	if live.owner.QueryRow(ctx, `SELECT management_revision::text FROM channels WHERE id=$1`, channel).Scan(&revision) != nil {
		return b, stage("model_setup_revision_failed")
	}
	var expected int64
	if _, err = fmt.Sscan(revision, &expected); err != nil {
		return b, stage("model_setup_revision_parse_failed")
	}
	inputRate, outputRate := int64(1000000), int64(1000000)
	if _, err = c.PublishCostPrice(ctx, catalog.PricingActor{UserID: admin}, catalog.CostPriceCommand{OperationID: uuid.NewString(), ChannelID: channel, UpstreamModel: "qa-chat", SchemaID: schemaID, SchemaDigest: schemaDigest, VariantKey: "default", Basis: "contract", InputRate: &inputRate, OutputRate: &outputRate, EffectiveAt: time.Now().UTC().Truncate(time.Microsecond), ExpectedChannelRevision: expected}); err != nil {
		return b, stage("model_setup_cost_failed")
	}
	if _, err = c.Publish(ctx, admin, model, 0, []catalog.BindingInput{{ChannelID: channel, UpstreamModel: "qa-chat", Weight: 1, MaxInflight: 1}}); err != nil {
		return b, stage("model_setup_publish_failed")
	}
	store := billing.New(live.owner)
	credit, err := store.CreateCredit(ctx, billing.CreditInput{TenantID: b.tenant, ActorID: operator, Amount: 1000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 1000, BusinessRef: uuid.NewString(), ProofRef: "fixture:model-process", IdempotencyKey: uuid.NewString(), RequestHMAC: "model-process-credit"})
	if err != nil {
		return b, stage("model_setup_credit_failed")
	}
	if _, err = store.ApproveCredit(ctx, billing.ApprovalInput{TenantID: b.tenant, ActorID: approver, OrderID: credit.ID}); err != nil {
		return b, stage("model_setup_credit_approval_failed")
	}
	return b, nil
}

type modelState struct {
	request, attempt, customer, delivery, purchase                                                             string
	deadline                                                                                                   time.Time
	amount, available, held, keyHeld, keySpent, purchaseHeld, purchaseSpent, accountHeld, accountSpent, charge int64
	requests, attempts, finals, metering, settlements, journals, events                                        int
	finalConfirmedContent                                                                                      bool
}

func readModelState(ctx context.Context, live *privateModelLifecycle, b modelBusiness) (modelState, error) {
	var s modelState
	err := live.owner.QueryRow(ctx, `SELECT r.id,a.id,r.request_deadline,v.status,v.amount,a.delivery_state,p.state,p.held_amount::bigint,p.spent_amount::bigint,
 ac.held_amount::bigint,ac.spent_amount::bigint,k.held,k.spent,
 (SELECT balance FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE'),(SELECT balance FROM ledger_accounts WHERE tenant_id=$1 AND kind='HELD'),
 (SELECT count(*) FROM request_records),(SELECT count(*) FROM upstream_attempts),(SELECT count(*) FROM finalization_records),(SELECT count(*) FROM metering_events),
 (SELECT count(*) FROM settlements),(SELECT coalesce(sum(charged_amount),0)::bigint FROM settlements),(SELECT count(*) FROM journals),(SELECT count(*) FROM procurement_budget_events),
 EXISTS(SELECT 1 FROM finalization_records f JOIN metering_events e ON e.id=f.event_id WHERE f.request_id=r.id AND f.content_written AND e.normalized_usage->>'status'='confirmed_final')
 FROM request_records r JOIN reservations v ON v.request_id=r.id JOIN upstream_attempts a ON a.request_id=r.id
 JOIN procurement_reservations p ON p.attempt_id=a.id JOIN procurement_accounts ac ON ac.id=p.account_id JOIN api_keys k ON k.id=r.key_id
 WHERE r.tenant_id=$1 AND k.id=$2 AND ac.id=$3`, b.tenant, b.key, b.account).Scan(&s.request, &s.attempt, &s.deadline, &s.customer, &s.amount, &s.delivery, &s.purchase, &s.purchaseHeld, &s.purchaseSpent, &s.accountHeld, &s.accountSpent, &s.keyHeld, &s.keySpent, &s.available, &s.held, &s.requests, &s.attempts, &s.finals, &s.metering, &s.settlements, &s.charge, &s.journals, &s.events, &s.finalConfirmedContent)
	if err != nil {
		return s, stage("model_state_read_failed")
	}
	return s, nil
}

func modelRequest(ctx context.Context, client *http.Client, origin, token, operation string, stream bool) (*http.Response, error) {
	body := `{"model":"lifecycle-model","messages":[{"role":"user","content":"synthetic"}],"max_tokens":16,"stream":false}`
	if stream {
		body = strings.Replace(body, `"stream":false`, `"stream":true,"stream_options":{"include_usage":true}`, 1)
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		return nil, stage("model_request_create_failed")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Idempotency-Key", operation)
	return client.Do(r)
}

func modelPause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return stage("model_observation_timeout")
	case <-timer.C:
		return nil
	}
}

func exerciseModelWindow(ctx context.Context, live *privateModelLifecycle, point modelFixturePoint, e *modelProcessEvidence) error {
	e.BinarySHA256 = live.binaryHash
	setup, setupDone := context.WithTimeout(ctx, 20*time.Second)
	b, err := setupModelBusiness(setup, live)
	setupDone()
	if err != nil {
		return err
	}
	if live.fixture.arm(point) != nil {
		return stage("model_arm_failed")
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 330 * time.Second}
	operation := uuid.NewString()
	requestCtx, requestDone := context.WithCancel(ctx)
	defer requestDone()
	content := make(chan bool, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		r, err := modelRequest(requestCtx, client, live.origin, b.token, operation, point == modelBeforeFinal)
		if err != nil {
			return
		}
		defer r.Body.Close()
		reader := bufio.NewReader(r.Body)
		for {
			line, err := reader.ReadString('\n')
			if strings.Contains(line, `"content":"QA OK"`) {
				select {
				case content <- true:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		requestDone()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
		}
	}()
	barrier, barrierDone := context.WithTimeout(ctx, 10*time.Second)
	defer barrierDone()
	select {
	case <-live.fixture.hit:
		e.PhysicalPOSTBarrier = true
	case <-barrier.Done():
		return stage("model_post_barrier_timeout")
	}
	initial, err := readModelState(ctx, live, b)
	if err != nil {
		return err
	}
	if initial.requests != 1 || initial.attempts != 1 || initial.delivery != "sending" || initial.customer != "reserved" || initial.purchase != "sending" || initial.amount != 116 || initial.available != 884 || initial.held != 116 || initial.keyHeld != 116 || initial.keySpent != 0 || initial.purchaseHeld != 116 || initial.purchaseSpent != 0 || initial.accountHeld != 116 || initial.accountSpent != 0 || initial.finals != 0 || initial.metering != 0 || initial.settlements != 0 || live.fixture.mock.counts().ChatPOSTs != 1 {
		return stage("model_sending_boundary_mismatch")
	}
	var originalWindow bool
	if live.owner.QueryRow(ctx, `SELECT request_deadline>clock_timestamp()+interval '290 seconds' AND request_deadline<=clock_timestamp()+interval '300 seconds' FROM request_records WHERE id=$1`, initial.request).Scan(&originalWindow) != nil || !originalWindow {
		return stage("model_original_300_second_window_missing")
	}
	e.Original300SecondWindow = true
	var blocker pgx.Tx
	var finalReleased time.Time
	defer func() {
		if blocker != nil {
			cleanup, stop := context.WithTimeout(context.Background(), time.Second)
			_ = blocker.Rollback(cleanup)
			stop()
		}
	}()
	if point == modelBeforeFinal {
		select {
		case <-content:
			e.ClientReadContent = true
		case <-barrier.Done():
			return stage("model_client_content_missing")
		}
		blocker, err = live.owner.Begin(ctx)
		if err != nil {
			return stage("model_blocker_begin_failed")
		}
		var pid int
		if blocker.QueryRow(ctx, `SELECT pg_backend_pid() FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE' FOR UPDATE`, b.tenant).Scan(&pid) != nil {
			return stage("model_blocker_lock_failed")
		}
		released := time.Now()
		finalReleased = released
		live.fixture.resume()
		for {
			if time.Since(released) >= 1500*time.Millisecond {
				return stage("model_settlement_window_missed")
			}
			var waiting bool
			var age int64
			if live.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename=$1 AND state='active' AND wait_event_type='Lock' AND $2=ANY(pg_blocking_pids(pid)) AND position('ledger_accounts' in query)>0),coalesce((SELECT max(floor(extract(epoch FROM(clock_timestamp()-query_start))*1000))::bigint FROM pg_stat_activity WHERE usename=$1 AND state='active' AND wait_event_type='Lock' AND $2=ANY(pg_blocking_pids(pid)) AND position('ledger_accounts' in query)>0),0)`, live.appRole, pid).Scan(&waiting, &age) != nil {
				return stage("model_waiter_read_failed")
			}
			state, err := readModelState(ctx, live, b)
			if err != nil {
				return err
			}
			if waiting && state.finals == 1 && state.metering == 1 && state.finalConfirmedContent && state.settlements == 0 && state.purchase == "settled" && state.purchaseSpent == 6 && state.purchaseHeld == 0 {
				if age < 0 || age >= 1500 {
					return stage("model_database_window_expired")
				}
				e.FinalEvidenceCommitted, e.PhysicalDatabaseWaiter, e.WaitAgeMS = true, true, age
				break
			}
			if err = modelPause(ctx, 5*time.Millisecond); err != nil {
				return err
			}
		}
		e.ReleaseToKillMS = time.Since(released).Milliseconds()
		if e.ReleaseToKillMS >= 1500 {
			return stage("model_kill_window_expired")
		}
	}
	kill, killDone := context.WithTimeout(ctx, 5*time.Second)
	err = live.killAndWait(kill)
	killDone()
	if err != nil {
		return err
	}
	if !finalReleased.IsZero() {
		e.ReleaseToKillMS = time.Since(finalReleased).Milliseconds()
		if time.Since(finalReleased) >= 1500*time.Millisecond {
			return stage("model_kill_exit_exceeded_safe_window")
		}
	}
	e.SIGKILLConfirmed = true
	if blocker != nil {
		if blocker.Rollback(ctx) != nil {
			return stage("model_blocker_rollback_failed")
		}
		blocker = nil
	}
	live.fixture.resume()
	requestDone()
	select {
	case <-finished:
	case <-ctx.Done():
		return stage("model_customer_join_timeout")
	}
	restart, restartDone := context.WithTimeout(ctx, 325*time.Second)
	err = live.restart(restart)
	restartDone()
	if err != nil {
		return err
	}
	e.ReplacementReady = true
	var readyAfter bool
	if live.owner.QueryRow(ctx, `SELECT clock_timestamp()>=request_deadline+interval '5 seconds' AND request_deadline=$2 FROM request_records WHERE id=$1`, initial.request, initial.deadline).Scan(&readyAfter) != nil || !readyAfter {
		return stage("model_predecessor_deadline_not_preserved")
	}
	e.OriginalDeadlinePreserved, e.ReadyAfterPredecessorDeadline = true, true
	var recovered modelState
	for {
		recovered, err = readModelState(ctx, live, b)
		if err != nil {
			return err
		}
		if point == modelBeforeResponse && recovered.customer == "pending_reconciliation" && recovered.purchase == "unknown" {
			break
		}
		if point == modelBeforeFinal && recovered.customer == "settled" && recovered.purchase == "settled" {
			break
		}
		if err = modelPause(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
	if point == modelBeforeResponse {
		if recovered.available != 884 || recovered.held != 116 || recovered.keyHeld != 116 || recovered.keySpent != 0 || recovered.purchaseHeld != 116 || recovered.purchaseSpent != 0 || recovered.accountHeld != 116 || recovered.accountSpent != 0 || recovered.finals != 0 || recovered.metering != 0 || recovered.settlements != 0 || recovered.journals != initial.journals {
			return stage("model_unknown_recovery_mismatch")
		}
	} else if recovered.available != 994 || recovered.held != 0 || recovered.keyHeld != 0 || recovered.keySpent != 6 || recovered.purchaseHeld != 0 || recovered.purchaseSpent != 6 || recovered.accountHeld != 0 || recovered.accountSpent != 6 || recovered.charge != 6 || recovered.finals != 1 || recovered.metering != 1 || recovered.settlements != 1 || recovered.journals != initial.journals+1 {
		return stage("model_final_recovery_mismatch")
	}
	// Observe two complete production recovery ticks without rewriting deadlines,
	// lease values, recovery notifications or calling a second recovery Store.
	for tick := 0; tick < 2; tick++ {
		if err = modelPause(ctx, 11*time.Second); err != nil {
			return err
		}
		current, err := readModelState(ctx, live, b)
		if err != nil || !reflect.DeepEqual(current, recovered) {
			return stage("model_repeat_recovery_changed_facts")
		}
	}
	replay, replayErr := modelRequest(ctx, client, live.origin, b.token, operation, point == modelBeforeFinal)
	if replayErr != nil {
		return stage("model_replay_http_failed")
	}
	replayMatches := modelReplayMatches(replay, initial.request)
	replay.Body.Close()
	if !replayMatches {
		return stage("model_replay_unexpected_response")
	}
	final, err := readModelState(ctx, live, b)
	if err != nil || !reflect.DeepEqual(final, recovered) || live.fixture.mock.counts().ChatPOSTs != 1 || final.requests != 1 || final.attempts != 1 {
		return stage("model_replay_created_new_facts")
	}
	e.ReplayNoNewFacts = true
	e.CustomerStatus, e.CustomerCharged, e.CustomerHeld = final.customer, final.charge, final.held
	e.ProcurementStatus, e.ProcurementHeld, e.ProcurementSpent = final.purchase, final.purchaseHeld, final.purchaseSpent
	e.Posts, e.Attempts, e.Finalizations, e.Settlements = live.fixture.mock.counts().ChatPOSTs, final.attempts, final.finals, final.settlements
	return nil
}

// The exact conflict and original identity are necessary: an unavailable or
// unauthorized gateway must never satisfy the idempotency recovery oracle.
func modelReplayMatches(response *http.Response, original string) bool {
	if response == nil || response.Body == nil || response.StatusCode != http.StatusConflict || original == "" || response.Header.Get("X-Original-Request-ID") != original {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(body) > 65536 {
		return false
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(body, &envelope) == nil && envelope.Error.Code == "request_exists"
}

func TestModelReplayRequiresExactOriginalConflict(t *testing.T) {
	for _, tc := range []struct {
		status         int
		original, body string
		want           bool
	}{
		{409, "original", `{"error":{"code":"request_exists"}}`, true},
		{401, "original", `{"error":{"code":"request_exists"}}`, false},
		{503, "original", `{"error":{"code":"request_exists"}}`, false},
		{409, "other", `{"error":{"code":"request_exists"}}`, false},
		{409, "original", `{"error":{"code":"idempotency_conflict"}}`, false},
		{409, "original", `{"error":{"code":"request_exists"}}{}`, false},
		{409, "original", strings.Repeat(" ", 65536) + `{}`, false},
	} {
		response := &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}
		response.Header.Set("X-Original-Request-ID", tc.original)
		if modelReplayMatches(response, "original") != tc.want {
			t.Fatal("incorrect replay classification")
		}
		response.Body.Close()
	}
	if modelReplayMatches(nil, "original") {
		t.Fatal("nil replay accepted")
	}
}

func TestModelLifecycleRealMainRecovery(t *testing.T) {
	if os.Getenv("MODEL_LIFECYCLE_REAL_MAIN_TEST") != "1" {
		t.Skip("explicit isolated model real-main opt-in required")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX SIGKILL required")
	}
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("dedicated local PostgreSQL test database is required")
	}
	cfg, err := modelLifecycleDatabase(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("MODEL_LIFECYCLE_BINARY")
	if binary == "" {
		binary = filepath.Join("..", "..", "bin", "moni-aigate")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal("model lifecycle binary path unavailable")
	}
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		t.Skip("prebuilt real main binary is required")
	}
	admin, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal("model lifecycle owner pool unavailable")
	}
	defer admin.Close()
	preflight, finishPreflight := context.WithTimeout(context.Background(), 3*time.Second)
	var canObserve bool
	err = admin.QueryRow(preflight, `SELECT current_user=session_user AND (EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=current_user AND rolsuper) OR pg_catalog.pg_has_role(current_user,'pg_read_all_stats','USAGE'))`).Scan(&canObserve)
	finishPreflight()
	if err != nil || !canObserve {
		t.Fatal("model lifecycle cross-role observation privilege required")
	}
	for _, point := range []modelFixturePoint{modelBeforeResponse, modelBeforeFinal} {
		name := "sending_before_upstream_response"
		if point == modelBeforeFinal {
			name = "committed_final_usage_before_customer_settlement"
		}
		t.Run(name, func(t *testing.T) {
			started := time.Now()
			e := modelProcessEvidence{Window: name}
			root := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 380*time.Second)
			defer cancel()
			err := run(ctx, options{binary: binary, listen: "127.0.0.1:0", root: root, database: os.Getenv("TEST_DATABASE_URL"), modelLifecycle: func(ctx context.Context, live *privateModelLifecycle) error {
				return exerciseModelWindow(ctx, live, point, &e)
			}}, io.Discard)
			if err != nil {
				t.Fatal("model lifecycle window failed", err)
			}
			paths, _ := filepath.Glob(filepath.Join(root, ".local", "browser-qa", "run-*", "manifest.json"))
			if len(paths) != 1 {
				t.Fatal("model lifecycle cleanup manifest missing")
			}
			raw, readErr := os.ReadFile(paths[0])
			var m manifest
			if readErr != nil || json.Unmarshal(raw, &m) != nil || m.Status != "stopped" || m.ServeShutdown != "graceful" || len(m.Remaining) != 0 || !m.CounterReadValid || m.MockCounts.ChatPOSTs != 1 || m.BusinessCounts.Requests != 1 || m.BusinessCounts.Attempts != 1 || m.BusinessCounts.Settlements != int64(e.Settlements) {
				t.Fatal("model lifecycle cleanup manifest mismatch")
			}
			cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			if privateWaitRoleExit(cleanup, admin, m.AppRole, m.ProjectionRole) != nil {
				t.Fatal("model lifecycle role connections remain")
			}
			var remains bool
			if admin.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1) OR EXISTS(SELECT 1 FROM pg_roles WHERE rolname=ANY($2::text[]))`, m.Schema, []string{m.AppRole, m.ProjectionRole}).Scan(&remains) != nil || remains {
				t.Fatal("model lifecycle schema or roles remain")
			}
			for _, address := range []string{strings.TrimPrefix(m.AppOrigin, "http://"), strings.TrimPrefix(m.MockOrigin, "http://")} {
				c, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
				if err == nil {
					c.Close()
					t.Fatal("model lifecycle listener remains")
				}
			}
			if os.RemoveAll(root) != nil {
				t.Fatal("model lifecycle private files cleanup failed")
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("model lifecycle private files remain")
			}
			e.GracefulFinalShutdown, e.NoRolesSchemasOrConnections, e.ListenersClosed, e.PrivateFilesRemoved = true, true, true, true
			e.ElapsedMS = time.Since(started).Milliseconds()
			safe, _ := json.Marshal(e)
			t.Log("model_process_evidence=" + string(safe))
		})
	}
}
