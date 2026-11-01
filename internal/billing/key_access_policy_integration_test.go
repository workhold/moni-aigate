package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func keyPolicySet(t *testing.T, f *fixture, values []string) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, "UPDATE api_keys SET allowed_client_cidrs=$2 WHERE id=$1", f.key, values); err != nil {
		t.Fatal(err)
	}
}

func keyPolicyInput(f *fixture) ReserveInput {
	in := f.input()
	in.Principal.ClientIP = "198.51.100.8"
	return in
}

func TestPostgresKeyAccessPolicyReservationRejectsWithoutFinancialFacts(t *testing.T) {
	f := integration(t)
	keyPolicySet(t, f, []string{"198.51.100.0/24"})
	for _, source := range []string{"", "203.0.113.4", "2001:db8::1", "198.51.100.8:1234", "fe80::1%en0"} {
		in := f.input()
		in.Principal.ClientIP = source
		_, err := f.store.Reserve(f.ctx, in)
		want := "key_ip_forbidden"
		if source == "198.51.100.8:1234" || source == "fe80::1%en0" {
			want = "invalid_request"
		}
		assertCode(t, err, want)
	}
	var requests, reservations, idempotency, attempts int
	if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM request_records),(SELECT count(*) FROM reservations),(SELECT count(*) FROM request_idempotency),(SELECT count(*) FROM upstream_attempts)`).Scan(&requests, &reservations, &idempotency, &attempts); err != nil || requests+reservations+idempotency+attempts != 0 {
		t.Fatal("denied source left financial/admission facts", requests, reservations, idempotency, attempts, err)
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresKeyAccessPolicyWaitersSeeCommittedTightening(t *testing.T) {
	for _, boundary := range []string{"reserve", "attempt", "dispatch"} {
		t.Run(boundary, func(t *testing.T) {
			f := integration(t)
			dispatchBatchSingleConnectionStore(t, f)
			keyPolicySet(t, f, []string{"198.51.100.0/24"})
			in := keyPolicyInput(f)
			var r Reservation
			var a Attempt
			var err error
			if boundary != "reserve" {
				r, err = f.store.Reserve(f.ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				defer f.store.EndRequest(r.RequestID)
			}
			if boundary == "dispatch" {
				a = dispatchBatchPrepared(t, f, r)
			}
			blocker, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(f.ctx)
			var blockerPID int
			if err = blocker.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&blockerPID); err != nil {
				t.Fatal(err)
			}
			if _, err = blocker.Exec(f.ctx, "UPDATE api_keys SET allowed_client_cidrs=$2 WHERE id=$1", f.key, []string{"203.0.113.0/24"}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				switch boundary {
				case "reserve":
					_, err := f.store.Reserve(ctx, in)
					done <- err
				case "attempt":
					_, err := f.store.RecordAttempt(ctx, dispatchBatchAttemptInput(f, r))
					done <- err
				case "dispatch":
					done <- f.store.PrepareDispatch(ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID})
				}
			}()
			waitForBatchBlocker(t, f, blockerPID)
			if err = blocker.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			assertCode(t, dispatchBatchAwait(t, done), "key_ip_forbidden")
			var requests, attempts, sending int
			if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM request_records),(SELECT count(*) FROM upstream_attempts),(SELECT count(*) FROM upstream_attempts WHERE delivery_state='sending')`).Scan(&requests, &attempts, &sending); err != nil || sending != 0 {
				t.Fatal("policy tightening allowed dispatch", requests, attempts, sending, err)
			}
			if boundary == "reserve" {
				if requests != 0 || attempts != 0 {
					t.Fatal("denied reservation left request", requests, attempts)
				}
				f.wallet(t, 1000, 0)
				// The aborted idempotency insertion and batched reads must have
				// drained. Reuse the exact input and one-connection Store.
				keyPolicySet(t, f, []string{})
				r, err = f.store.Reserve(f.ctx, in)
				if err != nil {
					t.Fatal("cleared policy could not reuse original admission", err)
				}
				defer f.store.EndRequest(r.RequestID)
			} else {
				if requests != 1 || boundary == "attempt" && attempts != 0 || boundary == "dispatch" && attempts != 1 {
					t.Fatal("tightening created extra facts", requests, attempts)
				}
				f.wallet(t, 880, 120)
			}
			// A source change must not prevent no-delivery release or leave
			// either wallet or Key held funds stranded.
			released, err := f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "authorization_revoked"})
			if err != nil || released.Status != "released" || released.ChargedAmount != 0 {
				t.Fatal("policy denied cleanup", released, err)
			}
			var held, spent int64
			if err = f.pool.QueryRow(f.ctx, "SELECT held,spent FROM api_keys WHERE id=$1", f.key).Scan(&held, &spent); err != nil || held != 0 || spent != 0 {
				t.Fatal("policy left Key funds", held, spent, err)
			}
			f.wallet(t, 1000, 0)
			f.check(t)
		})
	}
}

func TestPostgresKeyAccessPolicyPersistedSourceAndLegacyUnknown(t *testing.T) {
	for _, source := range []string{"::ffff:198.51.100.8", "2001:db8::8", ""} {
		t.Run(source, func(t *testing.T) {
			f := integration(t)
			in := f.input()
			in.Principal.ClientIP = source
			r, err := f.store.Reserve(f.ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			defer f.store.EndRequest(r.RequestID)
			var saved string
			if err = f.pool.QueryRow(f.ctx, "SELECT COALESCE(host(client_ip),'') FROM request_records WHERE id=$1", r.RequestID).Scan(&saved); err != nil {
				t.Fatal(err)
			}
			want := source
			if source == "::ffff:198.51.100.8" {
				want = "198.51.100.8"
			}
			if saved != want {
				t.Fatal("source snapshot changed identity", saved, want)
			}
			if _, err = f.pool.Exec(f.ctx, "UPDATE request_records SET client_ip='203.0.113.5' WHERE id=$1", r.RequestID); err == nil {
				t.Fatal("request source changed after insertion")
			}
			keyPolicySet(t, f, []string{"198.51.100.0/24", "2001:db8::/32"})
			a, err := f.store.RecordAttempt(f.ctx, dispatchBatchAttemptInput(f, r))
			if source == "" {
				assertCode(t, err, "key_ip_forbidden")
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
					t.Fatal("persisted source not used at dispatch", err)
				}
			}
			if source == "" {
				if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "authorization_revoked"}); err != nil {
					t.Fatal("legacy unknown source blocked release", err)
				}
				f.wallet(t, 1000, 0)
			} else {
				f.final(t, r, confirmed(), "completed", "done", "content", true)
				if _, err = f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID}); err != nil {
					t.Fatal(err)
				}
				f.wallet(t, 985, 0)
			}
			f.check(t)
		})
	}
}

func TestPostgresKeyAccessPolicyTighteningDoesNotBlockFinalizationRecoveryOrRefund(t *testing.T) {
	f := integration(t)
	keyPolicySet(t, f, []string{"198.51.100.0/24"})
	in := keyPolicyInput(f)
	r, err := f.store.Reserve(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	a := dispatchBatchPrepared(t, f, r)
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	keyPolicySet(t, f, []string{"203.0.113.0/24"})
	f.final(t, r, confirmed(), "completed", "done", "content", true)
	f.store.EndRequest(r.RequestID)
	if _, err = f.pool.Exec(f.ctx, "UPDATE request_records SET request_deadline=clock_timestamp()-interval '1 second' WHERE id=$1", r.RequestID); err != nil {
		t.Fatal(err)
	}
	worker := New(f.pool)
	for range 2 {
		if _, err = worker.Recover(f.ctx, RecoverInput{Limit: 10}); err != nil {
			t.Fatal("new source policy blocked financial recovery", err)
		}
	}
	settled, err := worker.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || settled.ChargedAmount != 15 || settled.Status != "settled" {
		t.Fatal("dispatched work did not uniquely settle", settled, err)
	}
	ref := uuid.NewString()
	refund, err := worker.CreateRefund(f.ctx, RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: settled.ID, BusinessRef: ref, Reason: "synthetic-policy-refund", Amount: 10, IdempotencyKey: ref, RequestHMAC: ref})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worker.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: refund.ID, ActorID: f.approver}); err != nil {
		t.Fatal("new source policy blocked refund", err)
	}
	var settlements, finalizations int
	var spent, held int64
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM settlements),(SELECT count(*) FROM finalization_records),spent,held FROM api_keys WHERE id=$1`, f.key).Scan(&settlements, &finalizations, &spent, &held); err != nil || settlements != 1 || finalizations != 1 || spent != 15 || held != 0 {
		t.Fatal("financial tail duplicated or stranded funds", settlements, finalizations, spent, held, err)
	}
	f.wallet(t, 995, 0)
	f.check(t)
}

func TestPostgresKeyAccessPolicyLegacyDispatchAndSafeRetry(t *testing.T) {
	for _, boundary := range []string{"legacy_prepared", "safe_retry"} {
		t.Run(boundary, func(t *testing.T) {
			f := integration(t)
			in := f.input()
			if boundary == "safe_retry" {
				in.Principal.ClientIP = "198.51.100.8"
			}
			r, err := f.store.Reserve(f.ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			defer f.store.EndRequest(r.RequestID)
			a := dispatchBatchPrepared(t, f, r)
			if boundary == "safe_retry" {
				_, err = f.store.RecordFinalization(f.ctx, FinalizationInput{TenantID: f.tenant, RequestID: r.RequestID, AttemptID: a.ID, Source: "adapter", SourceEventID: uuid.NewString(), Usage: Usage{Status: "missing"}, ProtocolOutcome: "failed", TransportEndReason: "not_sent", DeliveryStage: "none"})
				if err != nil {
					t.Fatal(err)
				}
			}
			keyPolicySet(t, f, []string{"203.0.113.0/24"})
			if boundary == "legacy_prepared" {
				assertCode(t, f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}), "key_ip_forbidden")
			} else {
				retry := dispatchBatchAttemptInput(f, r)
				retry.Sequence = 2
				_, err = f.store.RecordAttempt(f.ctx, retry)
				assertCode(t, err, "key_ip_forbidden")
			}
			var attempts, sending int
			if err = f.pool.QueryRow(f.ctx, `SELECT count(*),count(*) FILTER (WHERE delivery_state='sending') FROM upstream_attempts WHERE request_id=$1`, r.RequestID).Scan(&attempts, &sending); err != nil || attempts != 1 || sending != 0 {
				t.Fatal("tightening allowed a legacy dispatch or safe retry", attempts, sending, err)
			}
			if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "authorization_revoked"}); err != nil {
				t.Fatal("policy blocked no-delivery cleanup", err)
			}
			f.wallet(t, 1000, 0)
			f.check(t)
		})
	}
}
