// Run with: TEST_DATABASE_URL=... go run ./scripts/backup-restore-drill.go
// This drill creates and drops only databases named moni_drill_<random>_*.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/internal/server"
	"github.com/moni-ai/moni-aigate/migrations"
)

type businessFacts struct {
	CreditApproved    bool  `json:"credit_approved"`
	FundingReceipt    bool  `json:"funding_receipt"`
	Request           bool  `json:"request"`
	Attempt           bool  `json:"attempt"`
	Finalization      bool  `json:"finalization"`
	Settlement        bool  `json:"settlement"`
	RefundApproved    bool  `json:"refund_approved"`
	TenantAuditEvents int64 `json:"tenant_audit_events"`
}

func main() {
	if err := drill(); err != nil {
		fmt.Fprintln(os.Stderr, "backup/restore drill failed:", err)
		os.Exit(1)
	}
}
func drill() error {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		return fmt.Errorf("TEST_DATABASE_URL required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid test database URL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		return err
	}
	defer admin.Close()
	prefix := "moni_drill_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	sourceDB, targetDB := prefix+"_source", prefix+"_restore"
	for _, db := range []string{sourceDB, targetDB} {
		if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()); err != nil {
			return err
		}
		defer func(name string) {
			_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		}(db)
	}
	connection := func(db string) (*pgxpool.Pool, error) {
		v := *u
		v.Path = "/" + db
		return pgxpool.New(ctx, v.String())
	}
	source, err := connection(sourceDB)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := connection(targetDB)
	if err != nil {
		return err
	}
	defer target.Close()
	if err = migrations.ApplyCurrent(ctx, source); err != nil {
		return err
	}
	if err = migrations.VerifyCurrent(ctx, source); err != nil {
		return fmt.Errorf("source current baseline verification failed: %w", err)
	}
	keys := &security.Secrets{Keys: map[string][]byte{"old": bytes.Repeat([]byte{1}, 32), "new": bytes.Repeat([]byte{2}, 32)}, Active: "old", Pepper: bytes.Repeat([]byte{3}, 32)}
	tenant, user, key, operator, approver, catalogAdmin := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	execSQL := func(sql string, args ...any) error { _, err := source.Exec(ctx, sql, args...); return err }
	if err = execSQL("INSERT INTO tenants(id,name) VALUES($1,'recovery-drill')", tenant); err != nil {
		return err
	}
	for _, v := range []struct{ id, role string }{{user, "none"}, {operator, "finance_operator"}, {approver, "finance_approver"}, {catalogAdmin, "admin"}} {
		mfa, err := keys.SealFor("JBSWY3DPEHPK3PXP", "mfa:user", v.id)
		if err != nil {
			return err
		}
		if err = execSQL("INSERT INTO users(id,email,password_hash,platform_role,mfa_enabled,mfa_secret) VALUES($1,$2,'fixture',$3,true,$4)", v.id, v.id+"@drill.invalid", v.role, mfa); err != nil {
			return err
		}
	}
	if err = execSQL("INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", tenant, user); err != nil {
		return err
	}
	c := catalog.New(source, keys)
	model, err := c.CreateModel(ctx, catalogAdmin, catalog.ModelInput{PublicName: "drill-model", ContextLimit: 120, InputUpperBound: 100, OutputLimit: 20, DefaultOutputLimit: 20})
	if err != nil {
		return err
	}
	channel, err := c.SaveChannel(ctx, catalogAdmin, "", catalog.ChannelInput{Name: "drill-channel", BaseURL: "https://example.com", Region: "global", Secret: "fake-drill-provider-secret"})
	if err != nil {
		return err
	}
	if _, err = c.Publish(ctx, catalogAdmin, model, 0, []catalog.BindingInput{{ChannelID: channel, UpstreamModel: "upstream", Weight: 1, MaxInflight: 10}}); err != nil {
		return err
	}
	var schemaID, schemaDigest string
	if err = source.QueryRow(ctx, `SELECT c.schema_id,encode(c.schema_digest,'hex') FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id WHERE m.id=$1`, model).Scan(&schemaID, &schemaDigest); err != nil {
		return err
	}
	if _, err = c.PublishDefaultPrice(ctx, catalog.PricingActor{UserID: catalogAdmin}, catalog.DefaultPriceCommand{OperationID: uuid.NewString(), ModelID: model, SchemaID: schemaID, SchemaDigest: schemaDigest, VariantKey: "default", InputRate: 1000000, OutputRate: 1000000, EffectiveAt: time.Now().UTC().Truncate(time.Microsecond)}); err != nil {
		return err
	}
	if err = execSQL("INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", tenant, model); err != nil {
		return err
	}
	apiKey := "mg_live_" + security.Random(32)
	if err = execSQL("INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'drill','mg_live_',$4,$5,1000)", key, tenant, user, keys.Digest(apiKey), []string{model}); err != nil {
		return err
	}
	s := billing.New(source)
	fund := func(ref string, amount billing.Money) (billing.CreditOrder, error) {
		o, err := s.CreateCredit(ctx, billing.CreditInput{TenantID: tenant, ActorID: operator, Amount: amount, BusinessRef: ref, ProofRef: "drill-proof", IdempotencyKey: ref, RequestHMAC: ref})
		if err != nil {
			return billing.CreditOrder{}, err
		}
		_, err = s.ApproveCredit(ctx, billing.ApprovalInput{TenantID: tenant, OrderID: o.ID, ActorID: approver})
		return o, err
	}
	credit, err := fund("before-backup", 1000)
	if err != nil {
		return err
	}
	var profile string
	if err = source.QueryRow(ctx, "SELECT profile_version FROM models WHERE id=$1", model).Scan(&profile); err != nil {
		return err
	}
	reserve := func() (billing.Reservation, error) {
		return s.Reserve(ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: tenant, UserID: user, KeyID: key}, ModelID: model, ProfileVersion: profile, RouteRevision: 1, RequestHMAC: "drill-inference", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().Add(300 * time.Second)})
	}
	r, err := reserve()
	if err != nil {
		return err
	}
	routeCandidates, err := c.Candidates(ctx, model, 1)
	if err != nil || len(routeCandidates) != 1 {
		return fmt.Errorf("current route candidate unavailable: %v", err)
	}
	attempt, err := s.RecordAttempt(ctx, billing.AttemptInput{
		TenantID: tenant, RequestID: r.RequestID, ChannelID: routeCandidates[0].ChannelID,
		ChannelVersionID: routeCandidates[0].VersionID, CredentialID: routeCandidates[0].CredentialID,
		UpstreamModel: routeCandidates[0].UpstreamModel, Sequence: 1,
	})
	if err != nil {
		return err
	}
	if err = s.PrepareDispatch(ctx, billing.DispatchInput{TenantID: tenant, AttemptID: attempt.ID}); err != nil {
		return err
	}
	if _, err = s.RecordFinalization(ctx, billing.FinalizationInput{TenantID: tenant, RequestID: r.RequestID, AttemptID: attempt.ID, Source: "adapter", SourceEventID: "first-final", Usage: billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true}); err != nil {
		return err
	}
	settled, err := s.Settle(ctx, billing.SettleInput{TenantID: tenant, RequestID: r.RequestID})
	if err != nil {
		return err
	}
	refund, err := s.CreateRefund(ctx, billing.RefundInput{TenantID: tenant, ActorID: operator, SettlementID: settled.ID, Amount: 10, BusinessRef: "drill-refund", Reason: "drill", IdempotencyKey: "drill-refund", RequestHMAC: "drill-refund"})
	if err != nil {
		return err
	}
	if _, err = s.ApproveRefund(ctx, billing.ApprovalInput{TenantID: tenant, OrderID: refund.ID, ActorID: approver}); err != nil {
		return err
	}
	held, err := reserve()
	if err != nil {
		return err
	}
	s.EndRequest(held.RequestID)
	checkpoint, err := s.Wallet(ctx, billing.Query{TenantID: tenant})
	if err != nil {
		return err
	}
	checkpointFacts, err := readBusinessFacts(ctx, source, tenant, credit.ID, r.RequestID, attempt.ID, settled.ID, refund.ID)
	if err != nil {
		return fmt.Errorf("source business facts: %w", err)
	}
	var checkpointAt time.Time
	if err = source.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&checkpointAt); err != nil {
		return err
	}
	keys.Active = "new"
	work, err := os.MkdirTemp("", "moni-backup-drill-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	jsonKeys, _ := json.Marshal(map[string]string{"old": base64.StdEncoding.EncodeToString(keys.Keys["old"]), "new": base64.StdEncoding.EncodeToString(keys.Keys["new"])})
	material := []byte("MASTER_KEY_ID=new\nMASTER_KEYS_JSON=" + string(jsonKeys) + "\nAUTH_SECRET=" + base64.StdEncoding.EncodeToString(keys.Pepper) + "\n")
	materialFile := filepath.Join(work, "fake-secret-material.env")
	if err = os.WriteFile(materialFile, material, 0600); err != nil {
		return err
	}
	backupDir := filepath.Join(work, "backup")
	recoveredFile := filepath.Join(work, "recovered.env")
	run := func(database, script string, args ...string) error {
		cmd := exec.CommandContext(ctx, "bash", append([]string{script}, args...)...)
		env := append([]string{}, os.Environ()...)
		env = append(env, "PGHOST="+u.Hostname(), "PGPORT="+u.Port(), "PGUSER="+u.User.Username(), "PGDATABASE="+database, "MAINTENANCE_MODE=true", "CONFIRM_RESTORE_DATABASE="+database, "SECRET_MATERIAL_FILE="+materialFile)
		if password, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+password)
		}
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s failed: %s", filepath.Base(script), out)
		}
		return nil
	}
	blocker, err := source.Acquire(ctx)
	if err != nil {
		return err
	}
	if _, err = blocker.Exec(ctx, "SELECT pg_advisory_lock(782193402)"); err != nil {
		blocker.Release()
		return err
	}
	deniedBackup := run(sourceDB, "scripts/backup.sh", filepath.Join(work, "must-not-backup-active"))
	_, _ = blocker.Exec(ctx, "SELECT pg_advisory_unlock(782193402)")
	blocker.Release()
	if deniedBackup == nil {
		return fmt.Errorf("backup bypassed an active service lock")
	}
	binary := filepath.Join(work, "moni-aigate")
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/moni-aigate").CombinedOutput(); err != nil {
		return fmt.Errorf("build current app: %s", out)
	}
	backupStart := time.Now()
	if err = run(sourceDB, "scripts/backup.sh", backupDir); err != nil {
		return err
	}
	backupDuration := time.Since(backupStart)
	// Deliberately commit one independently referenced credit after the snapshot.
	// Restoring it must NOT pretend this later fact was included in the backup.
	if _, err = fund("after-backup-external-receipt", 100); err != nil {
		return err
	}
	restoreStart := time.Now()
	if err = run(targetDB, "scripts/restore.sh", backupDir, recoveredFile); err != nil {
		return err
	}
	restoredMaterial, err := os.ReadFile(recoveredFile)
	if err != nil || !bytes.Equal(material, restoredMaterial) {
		return fmt.Errorf("key material did not survive backup/restore")
	}
	recoveryOverride, err := os.ReadFile(recoveredFile + ".recovery.env")
	if err != nil || string(recoveryOverride) != "RECOVERY_MODE=true\n" {
		return fmt.Errorf("recovery override missing")
	}
	restoredKeys, err := loadMaterial(restoredMaterial)
	if err != nil {
		return err
	}
	if err = migrations.VerifyCurrent(ctx, target); err != nil {
		return err
	}
	restoredFacts, err := readBusinessFacts(ctx, target, tenant, credit.ID, r.RequestID, attempt.ID, settled.ID, refund.ID)
	if err != nil {
		return fmt.Errorf("restored business facts: %w", err)
	}
	if !reflect.DeepEqual(checkpointFacts, restoredFacts) {
		return fmt.Errorf("restored audit or business facts differ: source=%+v restored=%+v", checkpointFacts, restoredFacts)
	}
	var recoveryAuditCount int64
	if err = target.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor='offline-restore' AND action='recovery.latched'`).Scan(&recoveryAuditCount); err != nil {
		return err
	}
	if recoveryAuditCount != 1 {
		return fmt.Errorf("restored recovery audit count=%d want=1", recoveryAuditCount)
	}
	restored := billing.New(target)
	wallet, err := restored.Wallet(ctx, billing.Query{TenantID: tenant})
	if err != nil || wallet.Available != checkpoint.Available || wallet.Held != checkpoint.Held {
		return fmt.Errorf("restored wallet differs from checkpoint: %v", err)
	}
	ledger, err := restored.VerifyLedger(ctx, billing.Query{})
	if err != nil || !ledger.Balanced {
		return fmt.Errorf("restored ledger invalid: %v", err)
	}
	candidates, err := catalog.New(target, restoredKeys).Candidates(ctx, model, 1)
	if err != nil || len(candidates) != 1 || candidates[0].Secret != "fake-drill-provider-secret" {
		return fmt.Errorf("restored credential cannot be decrypted")
	}
	withoutOld := &security.Secrets{Keys: map[string][]byte{"new": restoredKeys.Keys["new"]}, Active: "new", Pepper: restoredKeys.Pepper}
	if _, err = catalog.New(target, withoutOld).Candidates(ctx, model, 1); err == nil {
		return fmt.Errorf("missing archived old key unexpectedly accepted")
	}
	required, err := operations.RecoveryRequired(ctx, target)
	if err != nil || !required {
		return fmt.Errorf("persistent restored recovery gate is missing: %v", err)
	}
	// Match main startup: a false environment flag cannot defeat the restored DB latch.
	environmentRecoveryMode := false
	effectiveRecoveryMode := environmentRecoveryMode || required
	gin.SetMode(gin.ReleaseMode)
	app := server.New(config.Config{Origin: "http://recovery.test", RecoveryMode: effectiveRecoveryMode, Secrets: restoredKeys}, target, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("recovery drill")}}, nil)
	app.Ready(true)
	handler := app.Engine()
	makeSession := func(id string) (string, string, error) {
		rawToken, csrf := security.Random(32), security.Random(24)
		family := uuid.NewString()
		if _, err := target.Exec(ctx, "INSERT INTO session_families(id,user_id) VALUES($1,$2)", family, id); err != nil {
			return "", "", err
		}
		_, err := target.Exec(ctx, "INSERT INTO sessions(digest,user_id,tenant_id,csrf_token,mfa_verified,reauthenticated_at,expires_at,family_id) VALUES($1,$2,$3,$4,true,clock_timestamp(),clock_timestamp()+interval '1 hour',$5)", restoredKeys.Digest(rawToken), id, tenant, csrf, family)
		return rawToken, csrf, err
	}
	ownerToken, ownerCSRF, err := makeSession(user)
	if err != nil {
		return err
	}
	operatorToken, operatorCSRF, err := makeSession(operator)
	if err != nil {
		return err
	}
	approverToken, approverCSRF, err := makeSession(approver)
	if err != nil {
		return err
	}
	request := func(method, path, token, csrf string, body string, api bool) int {
		q := httptest.NewRequest(method, "http://recovery.test"+path, strings.NewReader(body))
		q.Header.Set("Content-Type", "application/json")
		if api {
			q.Header.Set("Authorization", "Bearer "+apiKey)
		} else {
			q.AddCookie(&http.Cookie{Name: "moni_session", Value: token})
			q.Header.Set("X-CSRF-Token", csrf)
			q.Header.Set("Origin", "http://recovery.test")
			q.Header.Set("Idempotency-Key", "restore-gate-drill")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, q)
		return rec.Code
	}
	statuses := map[string]int{
		"paid_api":       request("POST", "/v1/chat/completions", "", "", `{"model":"drill-model","messages":[{"role":"user","content":"fixture"}]}`, true),
		"create_credit":  request("POST", "/api/platform/credits", operatorToken, operatorCSRF, `{}`, false),
		"approve_credit": request("POST", "/api/platform/credits/nonexistent/approve", approverToken, approverCSRF, `{}`, false),
		"create_refund":  request("POST", "/api/platform/refunds", operatorToken, operatorCSRF, `{}`, false),
		"approve_refund": request("POST", "/api/platform/refunds/nonexistent/approve", approverToken, approverCSRF, `{}`, false),
		"wallet_read":    request("GET", "/api/wallet", ownerToken, ownerCSRF, "", false),
	}
	for name, code := range statuses {
		want := 503
		if name == "wallet_read" {
			want = 200
		}
		if code != want {
			return fmt.Errorf("recovery gate %s status=%d want=%d", name, code, want)
		}
	}
	afterGates, err := restored.Wallet(ctx, billing.Query{TenantID: tenant})
	if err != nil || afterGates.Available != checkpoint.Available || afterGates.Held != checkpoint.Held {
		return fmt.Errorf("blocked recovery operations changed customer funds")
	}
	appRole, projectionRole := prefix+"_app", prefix+"_projection"
	rolePassword := security.Random(32)
	for _, role := range []string{appRole, projectionRole} {
		if _, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER PASSWORD '"+rolePassword+"'"); err != nil {
			return err
		}
	}
	defer func() {
		for _, role := range []string{appRole, projectionRole} {
			_, _ = target.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
			_, _ = admin.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize())
		}
	}()
	if err = operations.ProvisionFor(ctx, target, "public", appRole, projectionRole); err != nil {
		return err
	}
	mainStatuses, graceful, err := checkMainRecovery(binary, work, *u, targetDB, appRole, projectionRole, rolePassword, restoredKeys, operatorToken, operatorCSRF, apiKey)
	if err != nil {
		return err
	}
	restoreDuration := time.Since(restoreStart)
	if err = run(targetDB, "scripts/restore.sh", backupDir, filepath.Join(work, "must-not-create.env")); err == nil {
		return fmt.Errorf("restore overwrote nonempty database")
	}
	tampered := filepath.Join(work, "tampered")
	if err = os.Mkdir(tampered, 0700); err != nil {
		return err
	}
	for _, name := range []string{"SHA256SUMS", "database.dump", "manifest.txt", "key-material.env"} {
		b, err := os.ReadFile(filepath.Join(backupDir, name))
		if err != nil {
			return err
		}
		if name == "database.dump" {
			b = append(b, 1)
		}
		if err = os.WriteFile(filepath.Join(tampered, name), b, 0600); err != nil {
			return err
		}
	}
	if err = run(targetDB, "scripts/restore.sh", tampered, filepath.Join(work, "tamper-must-not-create.env")); err == nil {
		return fmt.Errorf("restore accepted altered backup")
	}
	report := map[string]any{"date": time.Now().UTC().Format(time.RFC3339), "postgresql": "16.15", "backup_seconds": backupDuration.Seconds(), "restore_to_verified_recovery_http_seconds": restoreDuration.Seconds(), "checkpoint_at": checkpointAt.UTC(), "checkpoint_wallet": checkpoint, "restored_wallet": wallet, "ledger": ledger, "business_facts": checkpointFacts, "recovery_audit_count": recoveryAuditCount, "recovery_http_status": statuses, "post_snapshot_credit_excluded_micro": "100", "recovered_point": "quiesced snapshot before deliberately omitted post-backup credit", "source_current_verified": true, "restored_current_verified": true, "old_master_key_required_and_verified": true, "nonempty_target_rejected": true, "active_service_backup_rejected": true, "tampered_bundle_rejected": true, "writes_during_recovery_blocked": true, "persistent_recovery_required": required, "env_recovery_mode": environmentRecoveryMode, "effective_recovery_mode": effectiveRecoveryMode, "container_deployment_executed": false}
	report["actual_main_process_recovery_http"] = mainStatuses
	report["main_process_graceful_shutdown"] = graceful
	b, _ := json.MarshalIndent(report, "", "  ")
	if err = os.WriteFile("docs/verification/backup-restore-local.json", append(b, '\n'), 0644); err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func readBusinessFacts(ctx context.Context, pool *pgxpool.Pool, tenant, creditID, requestID, attemptID, settlementID, refundID string) (businessFacts, error) {
	var facts businessFacts
	err := pool.QueryRow(ctx, `
SELECT
 EXISTS(SELECT 1 FROM credit_orders WHERE tenant_id=$1 AND id=$2 AND state='approved'),
 EXISTS(SELECT 1 FROM funding_receipts WHERE tenant_id=$1 AND source_kind='manual_credit' AND source_id=$2),
 EXISTS(SELECT 1 FROM request_records WHERE tenant_id=$1 AND id=$3),
 EXISTS(SELECT 1 FROM upstream_attempts WHERE tenant_id=$1 AND id=$4 AND request_id=$3),
 EXISTS(SELECT 1 FROM finalization_records WHERE tenant_id=$1 AND request_id=$3 AND attempt_id=$4 AND retail_usage_state='confirmed_final'),
 EXISTS(SELECT 1 FROM settlements WHERE tenant_id=$1 AND id=$5 AND request_id=$3 AND status='settled'),
 EXISTS(SELECT 1 FROM refund_orders WHERE tenant_id=$1 AND id=$6 AND state='approved'),
 (SELECT count(*) FROM audit_events WHERE tenant_scope=$1)`, tenant, creditID, requestID, attemptID, settlementID, refundID).
		Scan(&facts.CreditApproved, &facts.FundingReceipt, &facts.Request, &facts.Attempt, &facts.Finalization, &facts.Settlement, &facts.RefundApproved, &facts.TenantAuditEvents)
	return facts, err
}

func loadMaterial(raw []byte) (*security.Secrets, error) {
	env := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	values := map[string]string{}
	if err := json.Unmarshal([]byte(env["MASTER_KEYS_JSON"]), &values); err != nil {
		return nil, err
	}
	s := &security.Secrets{Active: env["MASTER_KEY_ID"], Keys: map[string][]byte{}}
	for id, value := range values {
		b, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, err
		}
		s.Keys[id] = b
	}
	var err error
	s.Pepper, err = base64.StdEncoding.DecodeString(env["AUTH_SECRET"])
	if err != nil {
		return nil, err
	}
	return s, s.Validate()
}

func checkMainRecovery(binary, work string, base url.URL, database, appRole, projectionRole, password string, keys *security.Secrets, operatorToken, csrf, apiKey string) (map[string]int, bool, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, false, err
	}
	address := listener.Addr().String()
	_ = listener.Close()
	appURL, projectionURL := base, base
	appURL.Path = "/" + database
	projectionURL.Path = "/" + database
	appURL.User = url.UserPassword(appRole, password)
	projectionURL.User = url.UserPassword(projectionRole, password)
	encoded := map[string]string{}
	for id, value := range keys.Keys {
		encoded[id] = base64.StdEncoding.EncodeToString(value)
	}
	keyJSON, _ := json.Marshal(encoded)
	log, err := os.OpenFile(filepath.Join(work, "main-process.log"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return nil, false, err
	}
	defer log.Close()
	cmd := exec.Command(binary, "serve")
	cmd.Env = append(os.Environ(), "DATABASE_URL="+appURL.String(), "PROJECTION_DATABASE_URL="+projectionURL.String(), "MASTER_KEY_ID="+keys.Active, "MASTER_KEYS_JSON="+string(keyJSON), "AUTH_SECRET="+base64.StdEncoding.EncodeToString(keys.Pepper), "RECOVERY_MODE=false", "LISTEN_ADDR="+address, "PUBLIC_ORIGIN=http://recovery.test", "COOKIE_SECURE=false", "GIN_MODE=release")
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		return nil, false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stopped := false
	var stopErr error
	stop := func() bool {
		if stopped {
			return stopErr == nil
		}
		stopped = true
		started := time.Now()
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				stopErr = fmt.Errorf("main SIGTERM exit after %s: %w", time.Since(started), err)
			}
			return err == nil
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			err := <-done
			stopErr = fmt.Errorf("main SIGTERM exceeded test cleanup limit after %s; killed, wait: %v", time.Since(started), err)
			return false
		}
	}
	defer func() {
		if !stopped {
			stop()
		}
	}()
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: time.Second, Transport: transport}
	baseHTTP := "http://" + address
	ready := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(baseHTTP + "/health/ready")
		if err == nil {
			var value struct {
				RecoveryMode bool `json:"recovery_mode"`
			}
			_ = json.NewDecoder(response.Body).Decode(&value)
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == 200 {
				if !value.RecoveryMode {
					return nil, false, fmt.Errorf("actual main ignored persistent recovery gate with env false")
				}
				ready = true
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		stop()
		diagnostic, _ := os.ReadFile(filepath.Join(work, "main-process.log"))
		if len(diagnostic) > 4096 {
			diagnostic = diagnostic[len(diagnostic)-4096:]
		}
		return nil, false, fmt.Errorf("actual main did not become ready in recovery mode: %s", diagnostic)
	}
	statuses := map[string]int{}
	for _, v := range []struct {
		name, path string
		api        bool
	}{{"paid_api", "/v1/chat/completions", true}, {"create_credit", "/api/platform/credits", false}} {
		q, _ := http.NewRequest("POST", baseHTTP+v.path, strings.NewReader(`{}`))
		q.Header.Set("Content-Type", "application/json")
		if v.api {
			q.Header.Set("Authorization", "Bearer "+apiKey)
		} else {
			q.AddCookie(&http.Cookie{Name: "moni_session", Value: operatorToken})
			q.Header.Set("X-CSRF-Token", csrf)
			q.Header.Set("Origin", "http://recovery.test")
			q.Header.Set("Idempotency-Key", "main-recovery-test")
		}
		response, err := client.Do(q)
		if err != nil {
			return nil, false, err
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		statuses[v.name] = response.StatusCode
		if response.StatusCode != 503 {
			return nil, false, fmt.Errorf("actual main recovery endpoint %s status %d", v.name, response.StatusCode)
		}
	}
	graceful := stop()
	return statuses, graceful, stopErr
}
