package billing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type mvpCrashInput struct {
	Schema, Stage, Upstream                          string
	Tenant, User, Key, Model, Channel, Version, Cred string
	ProfileVersion                                   string
	RouteRevision                                    int64
	Request, Idempotency                             string
}

// This helper is reachable only in the test binary. It calls the real billing
// services and is killed while blocked between committed public operations;
// there are no production fault hooks or timing changes to production code.
func TestMVPBillingCrashHelperProcess(t *testing.T) {
	if os.Getenv("MONI_BILLING_CRASH_HELPER") != "1" {
		t.Skip("subprocess helper only")
	}
	raw, err := base64.RawStdEncoding.DecodeString(os.Getenv("MONI_BILLING_CRASH_INPUT"))
	if err != nil {
		t.Fatal("invalid helper input")
	}
	var in mvpCrashInput
	if json.Unmarshal(raw, &in) != nil || in.Schema == "" {
		t.Fatal("invalid helper input")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = in.Schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := New(pool)
	r, err := store.Reserve(ctx, ReserveInput{Principal: Principal{TenantID: in.Tenant, UserID: in.User, KeyID: in.Key}, RequestID: in.Request, ModelID: in.Model, RouteRevision: in.RouteRevision, IdempotencyKey: in.Idempotency, RequestHMAC: "crash-test-digest", ProfileVersion: in.ProfileVersion, InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().UTC().Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if in.Stage != "reserve" {
		a, err := store.RecordAttempt(ctx, AttemptInput{TenantID: in.Tenant, RequestID: r.RequestID, ChannelID: in.Channel, ChannelVersionID: in.Version, CredentialID: in.Cred, UpstreamModel: "upstream", Sequence: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.PrepareDispatch(ctx, DispatchInput{TenantID: in.Tenant, AttemptID: a.ID}); err != nil {
			t.Fatal(err)
		}
		// A real local generation POST makes dispatch/replay observable across
		// the OS process boundary. No paid provider is involved.
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, in.Upstream, bytes.NewBufferString(`{"model":"fixture"}`))
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatal("mock generation did not complete")
		}
		if in.Stage != "dispatch" {
			stage, content := "none", false
			if in.Stage == "final_content" || in.Stage == "final_expired" {
				stage, content = "content", true
			}
			_, err = store.RecordFinalization(ctx, FinalizationInput{TenantID: in.Tenant, RequestID: r.RequestID, AttemptID: a.ID, Source: "adapter", SourceEventID: a.ID + ":final", Usage: confirmed(), ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: stage, ContentWritten: content, FinishReason: "stop"})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// Every operation above has returned after COMMIT. The parent does not
	// signal until it has read this marker, so it never guesses the boundary.
	fmt.Fprintln(os.Stdout, "MVP_BOUNDARY_COMMITTED")
	<-ctx.Done()
	t.Fatal("parent did not kill the helper")
}

func TestMVPPostgresSIGKILLRecoveryNeverReplaysGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX SIGKILL")
	}
	for _, tc := range []struct {
		stage, status       string
		posts, charge, held int64
	}{
		{stage: "reserve", status: "released"},
		{stage: "dispatch", status: "pending_reconciliation", posts: 1, held: 120},
		{stage: "final_none", status: "released", posts: 1},
		{stage: "final_content", status: "settled", posts: 1, charge: 15},
		{stage: "final_expired", status: "released", posts: 1},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			f := integration(t)
			var posts atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				posts.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = io.WriteString(w, `{"usage":{"input_tokens":10,"output_tokens":5}}`)
			}))
			defer upstream.Close()
			reserve := f.input()
			in := mvpCrashInput{Schema: f.pool.Config().ConnConfig.RuntimeParams["search_path"], Stage: tc.stage, Upstream: upstream.URL, Tenant: f.tenant, User: f.user, Key: f.key, Model: f.model, Channel: f.channel, Version: f.version, Cred: f.credential, ProfileVersion: fixtureProfileVersion, RouteRevision: fixtureRouteRevision, Request: reserve.RequestID, Idempotency: reserve.IdempotencyKey}
			encoded, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "-test.run=^TestMVPBillingCrashHelperProcess$", "-test.v")
			cmd.Env = append(os.Environ(), "MONI_BILLING_CRASH_HELPER=1", "MONI_BILLING_CRASH_INPUT="+base64.RawStdEncoding.EncodeToString(encoded))
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				if !waited {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			boundary := make(chan bool, 1)
			go func() {
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					if scanner.Text() == "MVP_BOUNDARY_COMMITTED" {
						boundary <- true
						return
					}
				}
				boundary <- false
			}()
			select {
			case ok := <-boundary:
				if !ok {
					_ = cmd.Wait()
					waited = true
					t.Fatalf("helper exited before committed boundary: %s", stderr.String())
				}
			case <-ctx.Done():
				t.Fatal("helper never reached committed boundary")
			}
			f.wallet(t, 880, 120)
			var settlements int
			if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM settlements WHERE request_id=$1", in.Request).Scan(&settlements); err != nil || settlements != 0 {
				t.Fatal("helper settled before kill", settlements, err)
			}
			var attemptsBefore, finalsBefore int
			if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM upstream_attempts WHERE request_id=$1),(SELECT count(*) FROM finalization_records WHERE request_id=$1)", in.Request).Scan(&attemptsBefore, &finalsBefore); err != nil {
				t.Fatal(err)
			}
			wantFinals := 1
			if tc.stage == "reserve" || tc.stage == "dispatch" {
				wantFinals = 0
			}
			if attemptsBefore != int(tc.posts) || finalsBefore != wantFinals {
				t.Fatalf("wrong durable kill boundary: attempts=%d finals=%d want=%d/%d", attemptsBefore, finalsBefore, tc.posts, wantFinals)
			}
			if err = cmd.Process.Signal(syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
			if err == nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatalf("helper did not exit by SIGKILL: %v %v", err, cmd.ProcessState)
			}
			if posts.Load() != tc.posts {
				t.Fatalf("POST boundary: got %d want %d", posts.Load(), tc.posts)
			}
			// Drop the notification to prove the fresh worker discovers the
			// authoritative unfinished reservation after a process crash.
			if _, err = f.pool.Exec(f.ctx, "DELETE FROM recovery_jobs WHERE request_id=$1", in.Request); err != nil {
				t.Fatal(err)
			}
			if tc.stage == "final_expired" {
				// Time-compressed financial deadline fixture: the trusted final
				// usage was committed before the kill, but recovery is after the
				// hard deadline. No 24-hour real-time wait is claimed.
				setFixtureReservationDeadline(t, f, in.Request, "-1 second")
			} else {
				for {
					var expired bool
					if err = f.pool.QueryRow(ctx, "SELECT request_deadline<clock_timestamp() FROM request_records WHERE id=$1", in.Request).Scan(&expired); err != nil {
						t.Fatal(err)
					}
					if expired {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("request execution deadline did not expire")
					case <-time.After(20 * time.Millisecond):
					}
				}
			}
			worker := New(f.pool)
			for run := 0; run < 2; run++ {
				result, err := worker.Recover(ctx, RecoverInput{Limit: 100})
				if err != nil || (run == 0 && result.Processed != 1) || (run == 1 && result.Processed != 0) {
					t.Fatalf("recovery run %d: %+v err=%v", run, result, err)
				}
			}
			var reservationStatus string
			var charged, keyHeld, spent int64
			if err = f.pool.QueryRow(ctx, "SELECT status FROM reservations WHERE request_id=$1", in.Request).Scan(&reservationStatus); err != nil || reservationStatus != tc.status {
				t.Fatalf("recovered reservation=%s want=%s err=%v", reservationStatus, tc.status, err)
			}
			if err = f.pool.QueryRow(ctx, "SELECT count(*),coalesce(sum(charged_amount),0)::bigint FROM settlements WHERE request_id=$1", in.Request).Scan(&settlements, &charged); err != nil {
				t.Fatal(err)
			}
			wantSettlements := 1
			if tc.status == "pending_reconciliation" {
				wantSettlements = 0
			}
			if settlements != wantSettlements || charged != tc.charge {
				t.Fatalf("settlements=%d charge=%d want=%d/%d", settlements, charged, wantSettlements, tc.charge)
			}
			f.wallet(t, Money(1000-tc.charge-tc.held), Money(tc.held))
			if err = f.pool.QueryRow(ctx, "SELECT held,spent FROM api_keys WHERE id=$1", f.key).Scan(&keyHeld, &spent); err != nil || keyHeld != tc.held || spent != tc.charge {
				t.Fatalf("key budget held=%d spent=%d err=%v", keyHeld, spent, err)
			}
			if posts.Load() != tc.posts {
				t.Fatalf("recovery replayed generation: got %d POSTs want %d", posts.Load(), tc.posts)
			}
			var attemptsAfter, finalsAfter int
			if err = f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM upstream_attempts WHERE request_id=$1),(SELECT count(*) FROM finalization_records WHERE request_id=$1)", in.Request).Scan(&attemptsAfter, &finalsAfter); err != nil || attemptsAfter != attemptsBefore || finalsAfter != finalsBefore {
				t.Fatalf("recovery changed attempt/evidence identity: %d/%d err=%v", attemptsAfter, finalsAfter, err)
			}
			f.check(t)
		})
	}
}
