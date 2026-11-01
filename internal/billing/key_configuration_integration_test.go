package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func keyConfigurationTightening(kind string) (string, string) {
	switch kind {
	case "scope":
		return "UPDATE api_keys SET scopes=ARRAY[gen_random_uuid()::text] WHERE id=$1", "forbidden"
	case "region":
		return "UPDATE api_keys SET allowed_regions=ARRAY['another-region'] WHERE id=$1", "forbidden"
	case "expiry":
		return "UPDATE api_keys SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "invalid_key"
	}
	panic("unknown test policy")
}

func keyConfigurationRelease(t *testing.T, f *fixture, r Reservation) {
	t.Helper()
	if _, err := f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "authorization_revoked"}); err != nil {
		t.Fatal("configuration blocked financial cleanup", err)
	}
	var held, spent int64
	if err := f.pool.QueryRow(f.ctx, "SELECT held,spent FROM api_keys WHERE id=$1", f.key).Scan(&held, &spent); err != nil || held != 0 || spent != 0 {
		t.Fatal("configuration left Key funds", held, spent, err)
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresKeyConfigurationWaitersSeeCommittedTightening(t *testing.T) {
	for _, boundary := range []string{"reserve", "attempt", "dispatch"} {
		for _, kind := range []string{"scope", "region", "expiry"} {
			// Reserve has no selected channel. Its current regions are carried
			// forward and enforced against that channel at attempt/dispatch.
			if boundary == "reserve" && kind == "region" {
				continue
			}
			t.Run(boundary+"/"+kind, func(t *testing.T) {
				f := integration(t)
				dispatchBatchSingleConnectionStore(t, f)
				var r Reservation
				var a Attempt
				if boundary != "reserve" {
					r = f.reserve(t)
					defer f.store.EndRequest(r.RequestID)
				}
				if boundary == "dispatch" {
					a = dispatchBatchPrepared(t, f, r)
				}
				blocker, err := f.pool.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer blocker.Rollback(context.Background())
				var pid int
				if err = blocker.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					t.Fatal(err)
				}
				sql, want := keyConfigurationTightening(kind)
				if _, err = blocker.Exec(f.ctx, sql, f.key); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					switch boundary {
					case "reserve":
						_, err := f.store.Reserve(ctx, f.input())
						done <- err
					case "attempt":
						_, err := f.store.RecordAttempt(ctx, dispatchBatchAttemptInput(f, r))
						done <- err
					case "dispatch":
						done <- f.store.PrepareDispatch(ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID})
					}
				}()
				waitForBatchBlocker(t, f, pid)
				if err = blocker.Commit(f.ctx); err != nil {
					t.Fatal(err)
				}
				assertCode(t, dispatchBatchAwait(t, done), want)
				var requests, attempts, sending, settlements int
				if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM request_records),(SELECT count(*) FROM upstream_attempts),(SELECT count(*) FROM upstream_attempts WHERE delivery_state='sending'),(SELECT count(*) FROM settlements)`).Scan(&requests, &attempts, &sending, &settlements); err != nil || sending != 0 || settlements != 0 {
					t.Fatal("tightening allowed dispatch or charge", err)
				}
				if boundary == "reserve" {
					var reservations, idempotency, journals int
					if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM reservations),(SELECT count(*) FROM request_idempotency),(SELECT count(*) FROM journals WHERE kind='reserve')`).Scan(&reservations, &idempotency, &journals); err != nil || requests+attempts+reservations+idempotency+journals != 0 {
						t.Fatal("denied reservation left facts", err)
					}
					f.wallet(t, 1000, 0)
					f.check(t)
				} else {
					if requests != 1 || boundary == "attempt" && attempts != 0 || boundary == "dispatch" && attempts != 1 {
						t.Fatal("unexpected attempt facts", requests, attempts)
					}
					keyConfigurationRelease(t, f, r)
				}
			})
		}
	}
}

func TestPostgresKeyConfigurationExpiryAfterUnchangedLockWait(t *testing.T) {
	cases := []struct{ boundary, lock string }{
		{"reserve", "key"}, {"reserve", "model"}, {"reserve", "wallet"},
		{"attempt", "channel"}, {"attempt", "reservation"},
		{"dispatch", "key"}, {"dispatch", "model"}, {"dispatch", "channel"},
		{"dispatch", "credential"}, {"dispatch", "reservation"}, {"dispatch", "attempt"},
		{"dispatch_deadline", "attempt"},
	}
	for _, tc := range cases {
		t.Run(tc.boundary+"/"+tc.lock, func(t *testing.T) {
			f := integration(t)
			dispatchBatchSingleConnectionStore(t, f)
			var r Reservation
			var a Attempt
			if tc.boundary != "reserve" {
				r = f.reserve(t)
				defer f.store.EndRequest(r.RequestID)
			}
			if tc.boundary == "dispatch" || tc.boundary == "dispatch_deadline" {
				a = dispatchBatchPrepared(t, f, r)
			}
			if tc.boundary == "dispatch_deadline" {
				if _, err := f.pool.Exec(f.ctx, "UPDATE request_records SET request_deadline=clock_timestamp()+interval '500 milliseconds' WHERE id=$1", r.RequestID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.pool.Exec(f.ctx, "UPDATE api_keys SET expires_at=clock_timestamp()+interval '500 milliseconds' WHERE id=$1", f.key); err != nil {
				t.Fatal(err)
			}
			blocker, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			var pid int
			if err = blocker.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			query, id := "", ""
			switch tc.lock {
			case "key":
				query, id = "SELECT id FROM api_keys WHERE id=$1 FOR UPDATE", f.key
			case "model":
				query, id = "SELECT id FROM models WHERE id=$1 FOR UPDATE", f.model
			case "wallet":
				query, id = "SELECT id FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE' FOR UPDATE", f.tenant
			case "channel":
				query, id = "SELECT id FROM channels WHERE id=$1 FOR UPDATE", f.channel
			case "credential":
				query, id = "SELECT id FROM channel_credentials WHERE id=$1 FOR UPDATE", f.credential
			case "reservation":
				query, id = "SELECT id FROM reservations WHERE id=$1 FOR UPDATE", r.ID
			case "attempt":
				query, id = "SELECT id FROM upstream_attempts WHERE id=$1 FOR UPDATE", a.ID
			}
			var ignored string
			if err = blocker.QueryRow(f.ctx, query, id).Scan(&ignored); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				switch tc.boundary {
				case "reserve":
					_, err := f.store.Reserve(ctx, f.input())
					done <- err
				case "attempt":
					_, err := f.store.RecordAttempt(ctx, dispatchBatchAttemptInput(f, r))
					done <- err
				default:
					done <- f.store.PrepareDispatch(ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID})
				}
			}()
			waitForBatchBlocker(t, f, pid)
			// Wait for the authoritative DB clock, not a goroutine schedule.
			for {
				var expired bool
				expiryQuery, expiryID := "SELECT expires_at<=clock_timestamp() FROM api_keys WHERE id=$1", f.key
				if tc.boundary == "dispatch_deadline" {
					expiryQuery, expiryID = "SELECT request_deadline<=clock_timestamp() FROM request_records WHERE id=$1", r.RequestID
				}
				if err = f.pool.QueryRow(ctx, expiryQuery, expiryID).Scan(&expired); err != nil {
					t.Fatal(err)
				}
				if expired {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("expiry wait timed out")
				case <-time.After(5 * time.Millisecond):
				}
			}
			if err = blocker.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			want := "invalid_key"
			if tc.boundary == "dispatch_deadline" {
				want = "request_deadline"
			}
			assertCode(t, dispatchBatchAwait(t, done), want)
			if tc.boundary == "reserve" {
				var count int
				if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM request_records").Scan(&count); err != nil || count != 0 {
					t.Fatal("expired admission left uncommitted writes", count, err)
				}
				f.wallet(t, 1000, 0)
				f.check(t)
			} else {
				keyConfigurationRelease(t, f, r)
			}
		})
	}
}

func TestPostgresKeyConfigurationMissingAttemptVersionKeepsDomainError(t *testing.T) {
	f := integration(t)
	dispatchBatchSingleConnectionStore(t, f)
	r := f.reserve(t)
	defer f.store.EndRequest(r.RequestID)
	in := dispatchBatchAttemptInput(f, r)
	in.ChannelVersionID = uuid.NewString()
	_, err := f.store.RecordAttempt(f.ctx, in)
	assertCode(t, err, "invalid_attempt")
	if _, err = f.store.RecordAttempt(f.ctx, dispatchBatchAttemptInput(f, r)); err != nil {
		t.Fatal("missing version left batch unusable", err)
	}
	keyConfigurationRelease(t, f, r)
}

func TestPostgresKeyConfigurationSafeRetryUsesCurrentRestrictions(t *testing.T) {
	for _, kind := range []string{"scope", "region", "expiry"} {
		t.Run(kind, func(t *testing.T) {
			f := integration(t)
			r := f.reserve(t)
			defer f.store.EndRequest(r.RequestID)
			a := dispatchBatchPrepared(t, f, r)
			_, err := f.store.RecordFinalization(f.ctx, FinalizationInput{TenantID: f.tenant, RequestID: r.RequestID, AttemptID: a.ID, Source: "adapter", SourceEventID: uuid.NewString(), Usage: Usage{Status: "missing"}, ProtocolOutcome: "failed", TransportEndReason: "not_sent", DeliveryStage: "none"})
			if err != nil {
				t.Fatal(err)
			}
			sql, want := keyConfigurationTightening(kind)
			if _, err = f.pool.Exec(f.ctx, sql, f.key); err != nil {
				t.Fatal(err)
			}
			in := dispatchBatchAttemptInput(f, r)
			in.Sequence = 2
			_, err = f.store.RecordAttempt(f.ctx, in)
			assertCode(t, err, want)
			var count int
			if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM upstream_attempts WHERE request_id=$1", r.RequestID).Scan(&count); err != nil || count != 1 {
				t.Fatal("restricted retry created a second attempt", count, err)
			}
			keyConfigurationRelease(t, f, r)
		})
	}
}

func TestPostgresKeyConfigurationSafeRetryKeepsSingleFrozenBudget(t *testing.T) {
	f := integration(t)
	r := f.reserve(t)
	defer f.store.EndRequest(r.RequestID)
	a := dispatchBatchPrepared(t, f, r)
	_, err := f.store.RecordFinalization(f.ctx, FinalizationInput{TenantID: f.tenant, RequestID: r.RequestID, AttemptID: a.ID, Source: "adapter", SourceEventID: uuid.NewString(), Usage: Usage{Status: "missing"}, ProtocolOutcome: "failed", TransportEndReason: "not_sent", DeliveryStage: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE api_keys SET lifetime_limit=spent+held WHERE id=$1", f.key); err != nil {
		t.Fatal(err)
	}
	in := dispatchBatchAttemptInput(f, r)
	in.Sequence = 2
	// A safe retry must use a different channel; changing credentials on the
	// original channel is deliberately forbidden by the existing contract.
	in.ChannelID, in.ChannelVersionID, in.CredentialID = uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO channels(id,name,current_version) VALUES($1,'synthetic retry',$2)", []any{in.ChannelID, in.ChannelVersionID}},
		{"INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'synthetic-only')", []any{in.CredentialID, in.ChannelID}},
		{"INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)", []any{in.ChannelVersionID, in.ChannelID, in.CredentialID}},
		{"INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1)", []any{in.ChannelVersionID, in.ChannelID, in.CredentialID}},
	} {
		if _, err = tx.Exec(f.ctx, query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	second, err := f.store.RecordAttempt(f.ctx, in)
	if err != nil {
		t.Fatal("safe retry demanded another reservation", err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: second.ID}); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.RecordFinalization(f.ctx, FinalizationInput{TenantID: f.tenant, RequestID: r.RequestID, AttemptID: second.ID, Source: "adapter", SourceEventID: uuid.NewString(), Usage: confirmed(), ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || settled.ChargedAmount != 15 {
		t.Fatal("safe retry did not settle once", settled, err)
	}
	var reservations, journals, attempts, settlements int
	var spent, held int64
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM reservations),(SELECT count(*) FROM journals WHERE kind='reserve'),(SELECT count(*) FROM upstream_attempts),(SELECT count(*) FROM settlements),spent,held FROM api_keys WHERE id=$1`, f.key).Scan(&reservations, &journals, &attempts, &settlements, &spent, &held); err != nil || reservations != 1 || journals != 1 || attempts != 2 || settlements != 1 || spent != 15 || held != 0 {
		t.Fatal("safe retry duplicated budget or charge", reservations, journals, attempts, settlements, spent, held, err)
	}
	f.wallet(t, 985, 0)
	f.check(t)
}

func TestPostgresKeyConfigurationReservedBudgetAndFinancialTail(t *testing.T) {
	for _, tightenAfterSending := range []bool{false, true} {
		name := "reserved_budget"
		if tightenAfterSending {
			name = "sending_recovery_refund"
		}
		t.Run(name, func(t *testing.T) {
			f := integration(t)
			r := f.reserve(t)
			if _, err := f.pool.Exec(f.ctx, "UPDATE api_keys SET lifetime_limit=spent+held WHERE id=$1", f.key); err != nil {
				t.Fatal(err)
			}
			a := dispatchBatchPrepared(t, f, r)
			if err := f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
				t.Fatal("budget lowering invalidated existing reservation", err)
			}
			if tightenAfterSending {
				if _, err := f.pool.Exec(f.ctx, "UPDATE api_keys SET scopes=ARRAY[gen_random_uuid()::text],allowed_regions=ARRAY['another-region'],expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", f.key); err != nil {
					t.Fatal(err)
				}
			}
			f.final(t, r, confirmed(), "completed", "done", "content", true)
			f.store.EndRequest(r.RequestID)
			if _, err := f.pool.Exec(f.ctx, "UPDATE request_records SET request_deadline=clock_timestamp()-interval '1 second' WHERE id=$1", r.RequestID); err != nil {
				t.Fatal(err)
			}
			worker := New(f.pool)
			for range 2 {
				if _, err := worker.Recover(f.ctx, RecoverInput{Limit: 10}); err != nil {
					t.Fatal("configuration blocked financial recovery", err)
				}
			}
			settled, err := worker.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
			if err != nil || settled.ChargedAmount != 15 || settled.Status != "settled" {
				t.Fatal("financial tail failed", settled, err)
			}
			ref := uuid.NewString()
			refund, err := worker.CreateRefund(f.ctx, RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: settled.ID, BusinessRef: ref, Reason: "synthetic-configuration-refund", Amount: 10, IdempotencyKey: ref, RequestHMAC: ref})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = worker.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: refund.ID, ActorID: f.approver}); err != nil {
				t.Fatal("configuration blocked refund", err)
			}
			var count int
			var spent, held int64
			if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM settlements),spent,held FROM api_keys WHERE id=$1", f.key).Scan(&count, &spent, &held); err != nil || count != 1 || spent != 15 || held != 0 {
				t.Fatal("duplicated or stranded financial tail", count, spent, held, err)
			}
			f.wallet(t, 995, 0)
			f.check(t)
		})
	}
}
