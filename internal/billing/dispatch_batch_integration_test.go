package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Keep the business pool at one connection so a later successful operation
// cannot hide an undrained batch by borrowing some other idle connection. The
// fixture pool remains available for the blocker and PostgreSQL observations.
func dispatchBatchSingleConnectionStore(t *testing.T, f *fixture) {
	t.Helper()
	cfg := f.pool.Config()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f.store = New(pool)
}

func dispatchBatchAttemptInput(f *fixture, r Reservation) AttemptInput {
	return AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1}
}

func dispatchBatchPrepared(t *testing.T, f *fixture, r Reservation) Attempt {
	t.Helper()
	a, err := f.store.RecordAttempt(f.ctx, dispatchBatchAttemptInput(f, r))
	if err != nil || a.DeliveryState != "prepared" {
		t.Fatal("could not prepare dispatch fixture", a, err)
	}
	return a
}

func dispatchBatchAwait(t *testing.T, done <-chan error) error {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		t.Fatal("dispatch batch did not finish")
		return nil
	}
}

func dispatchBatchFinancials(t *testing.T, f *fixture, r Reservation, requestStatus, reservationStatus string) {
	t.Helper()
	var status, reserved string
	var held, spent int64
	var settlements int
	err := f.pool.QueryRow(f.ctx, `SELECT r.status,v.status,k.held,k.spent,
 (SELECT count(*) FROM settlements s WHERE s.request_id=r.id)
 FROM request_records r JOIN reservations v ON v.request_id=r.id
 JOIN api_keys k ON (k.tenant_id,k.id)=(r.tenant_id,r.key_id)
 WHERE r.id=$1`, r.RequestID).Scan(&status, &reserved, &held, &spent, &settlements)
	if err != nil || status != requestStatus || reserved != reservationStatus || held != 120 || spent != 0 || settlements != 0 {
		t.Fatal("dispatch read changed financial facts", status, reserved, held, spent, settlements, err)
	}
	// These reads use the one-connection business pool after the failed batch.
	f.wallet(t, 880, 120)
	f.check(t)
}

func dispatchBatchState(t *testing.T, f *fixture, r Reservation, a Attempt, state, requestStatus string) {
	t.Helper()
	var actual string
	if err := f.pool.QueryRow(f.ctx, "SELECT delivery_state FROM upstream_attempts WHERE id=$1", a.ID).Scan(&actual); err != nil || actual != state {
		t.Fatal("dispatch attempt state changed", actual, state, err)
	}
	dispatchBatchFinancials(t, f, r, requestStatus, "reserved")
}

func TestPostgresDispatchReadBatchSeesCommittedChannelAndCredentialRevocation(t *testing.T) {
	for _, boundary := range []string{"channel", "credential"} {
		t.Run(boundary, func(t *testing.T) {
			f := integration(t)
			dispatchBatchSingleConnectionStore(t, f)
			r := f.reserve(t)
			defer f.store.EndRequest(r.RequestID)
			a := dispatchBatchPrepared(t, f, r)
			blocker, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(f.ctx)
			var pid int
			if err = blocker.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if boundary == "channel" {
				_, err = blocker.Exec(f.ctx, "UPDATE channels SET enabled=false WHERE id=$1", f.channel)
			} else {
				_, err = blocker.Exec(f.ctx, "UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE id=$1", f.credential)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- f.store.PrepareDispatch(ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}) }()
			waitForBatchBlocker(t, f, pid)
			if err = blocker.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			assertCode(t, dispatchBatchAwait(t, done), "channel_unavailable")
			dispatchBatchState(t, f, r, a, "prepared", "accepted")

			// Repair the mutable parent and use the same Store/pool again. The
			// rejected read batch must not leave the connection busy or sending.
			if boundary == "channel" {
				_, err = f.pool.Exec(f.ctx, "UPDATE channels SET enabled=true WHERE id=$1", f.channel)
			} else {
				_, err = f.pool.Exec(f.ctx, "UPDATE channel_credentials SET revoked_at=NULL WHERE id=$1", f.credential)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
				t.Fatal("repaired dispatch could not reuse the business pool", err)
			}
			dispatchBatchState(t, f, r, a, "sending", "dispatching")
		})
	}
}

func TestPostgresDispatchReadBatchChecksClockAfterReservationWait(t *testing.T) {
	for _, attemptState := range []string{"prepared", "unknown"} {
		t.Run(attemptState, func(t *testing.T) {
			f := integration(t)
			dispatchBatchSingleConnectionStore(t, f)
			in := f.input()
			// Set the immutable request deadline once, from the database clock.
			// The reservation wait stays below the production 3s lock timeout.
			if err := f.pool.QueryRow(f.ctx, "SELECT clock_timestamp()+interval '1500 milliseconds'").Scan(&in.RequestDeadline); err != nil {
				t.Fatal(err)
			}
			r, err := f.store.Reserve(f.ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			defer f.store.EndRequest(r.RequestID)
			a := dispatchBatchPrepared(t, f, r)
			if attemptState != "prepared" {
				if _, err = f.pool.Exec(f.ctx, "UPDATE upstream_attempts SET delivery_state=$2 WHERE id=$1", a.ID, attemptState); err != nil {
					t.Fatal(err)
				}
			}
			blocker, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(f.ctx)
			var pid int
			if err = blocker.QueryRow(f.ctx, "SELECT pg_backend_pid() FROM reservations WHERE request_id=$1 FOR UPDATE", r.RequestID).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- f.store.PrepareDispatch(ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}) }()
			waitForBatchBlocker(t, f, pid)
			var stillValid bool
			if err = f.pool.QueryRow(f.ctx, "SELECT clock_timestamp()<request_deadline FROM request_records WHERE id=$1", r.RequestID).Scan(&stillValid); err != nil || !stillValid {
				t.Fatal("deadline fixture expired before the observed reservation wait", stillValid, err)
			}
			clockCtx, clockCancel := context.WithTimeout(f.ctx, 2500*time.Millisecond)
			defer clockCancel()
			for {
				var expired bool
				if err = f.pool.QueryRow(clockCtx, "SELECT clock_timestamp()>request_deadline FROM request_records WHERE id=$1", r.RequestID).Scan(&expired); err != nil {
					t.Fatal("database deadline did not pass within the bounded lock wait", err)
				}
				if expired {
					break
				}
				select {
				case <-clockCtx.Done():
					t.Fatal("database request deadline did not expire")
				case <-time.After(5 * time.Millisecond):
				}
			}
			if err = blocker.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			// Deadline remains ahead of attempt-state errors even when the
			// queued attempt lock returns a state other than prepared.
			assertCode(t, dispatchBatchAwait(t, done), "request_deadline")
			dispatchBatchState(t, f, r, a, attemptState, "accepted")
			var persisted time.Time
			if err = f.pool.QueryRow(f.ctx, "SELECT request_deadline FROM request_records WHERE id=$1", r.RequestID).Scan(&persisted); err != nil || !persisted.Equal(in.RequestDeadline) {
				t.Fatal("test or dispatch changed the persisted request deadline", persisted, in.RequestDeadline, err)
			}
			retry := dispatchBatchAttemptInput(f, r)
			retry.Sequence = 2
			_, err = f.store.RecordAttempt(f.ctx, retry)
			assertCode(t, err, "request_deadline") // Before missing retry evidence.
			dispatchBatchFinancials(t, f, r, "accepted", "reserved")
		})
	}
}

func TestPostgresDispatchReadBatchPreservesDomainErrorPriority(t *testing.T) {
	for _, boundary := range []string{"channel_before_isolation", "isolation_before_credential", "credential_before_region", "region_before_attempt"} {
		t.Run(boundary, func(t *testing.T) {
			f := integration(t)
			dispatchBatchSingleConnectionStore(t, f)
			r := f.reserve(t)
			defer f.store.EndRequest(r.RequestID)
			a := dispatchBatchPrepared(t, f, r)
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			want, state := "channel_unavailable", "prepared"
			switch boundary {
			case "channel_before_isolation":
				exec("UPDATE channels SET enabled=false WHERE id=$1", f.channel)
				exec("INSERT INTO route_binding_isolations(channel_version_id,upstream_model,reason) VALUES($1,'upstream','model_configuration_error')", f.version)
			case "isolation_before_credential":
				exec("INSERT INTO route_binding_isolations(channel_version_id,upstream_model,reason) VALUES($1,'upstream','model_configuration_error')", f.version)
				exec("UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE id=$1", f.credential)
				want = "binding_unavailable"
			case "credential_before_region":
				exec("UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE id=$1", f.credential)
				exec("UPDATE tenants SET allowed_regions=ARRAY['restricted'] WHERE id=$1", f.tenant)
			case "region_before_attempt":
				exec("UPDATE tenants SET allowed_regions=ARRAY['restricted'] WHERE id=$1", f.tenant)
				exec("UPDATE upstream_attempts SET delivery_state='unknown' WHERE id=$1", a.ID)
				want, state = "forbidden", "unknown"
			}
			assertCode(t, f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}), want)
			dispatchBatchState(t, f, r, a, state, "accepted")
		})
	}
}

func TestPostgresAttemptReadBatchKeepsMissingCostUnknown(t *testing.T) {
	f := integration(t)
	dispatchBatchSingleConnectionStore(t, f)
	r := f.reserve(t)
	defer f.store.EndRequest(r.RequestID)
	in := dispatchBatchAttemptInput(f, r)
	in.UpstreamModel = "unpriced-upstream"
	a, err := f.store.RecordAttempt(f.ctx, in)
	if err != nil || a.DeliveryState != "prepared" || a.CostStatus != "unknown" {
		t.Fatal("missing cost prevented an attempt or invented a cost", a, err)
	}
	var id, amount *string
	var rates, ratesDigest []byte
	var input, output *int64
	var basis, status string
	err = f.pool.QueryRow(f.ctx, `SELECT s.cost_version_id,s.supplier_rates_canonical,s.supplier_rates_digest,s.cost_basis,a.cost_status,a.cost_amount::text FROM upstream_attempts a JOIN attempt_meter_snapshots s ON s.attempt_id=a.id WHERE a.id=$1`, a.ID).Scan(&id, &rates, &ratesDigest, &basis, &status, &amount)
	if err != nil || id != nil || rates != nil || ratesDigest != nil || amount != nil || basis != "unknown" || status != "unknown" {
		t.Fatal("missing cost was not stored as NULL/unknown", id, input, output, basis, status, amount, err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal("cost no-row scan left the next transaction unusable", err)
	}
	dispatchBatchState(t, f, r, a, "sending", "dispatching")
}

func TestPostgresAttemptReadBatchEarlyRefusalsDrain(t *testing.T) {
	for _, boundary := range []string{"unpublished_before_retry", "finished_before_unpublished", "isolation_before_finished", "retry_without_finalization"} {
		t.Run(boundary, func(t *testing.T) {
			f := integration(t)
			dispatchBatchSingleConnectionStore(t, f)
			publishFixtureRoute(t, f, 1, "upstream")
			reserve := f.input()
			reserve.RouteRevision = 1
			r, err := f.store.Reserve(f.ctx, reserve)
			if err != nil {
				t.Fatal(err)
			}
			defer f.store.EndRequest(r.RequestID)
			in := dispatchBatchAttemptInput(f, r)
			in.Sequence = 2
			want, reservationStatus := "unsafe_retry", "reserved"
			switch boundary {
			case "unpublished_before_retry", "finished_before_unpublished":
				in.UpstreamModel = "unpublished-and-unpriced"
				want = "unpublished_binding"
				if boundary == "finished_before_unpublished" {
					reservationStatus, want = "pending_reconciliation", "request_finished"
				}
			case "isolation_before_finished":
				reservationStatus, want = "pending_reconciliation", "binding_unavailable"
				if _, err = f.pool.Exec(f.ctx, "INSERT INTO route_binding_isolations(channel_version_id,upstream_model,reason) VALUES($1,'upstream','model_configuration_error')", f.version); err != nil {
					t.Fatal(err)
				}
			}
			if reservationStatus != "reserved" {
				if _, err = f.pool.Exec(f.ctx, "UPDATE reservations SET status=$2 WHERE request_id=$1", r.RequestID, reservationStatus); err != nil {
					t.Fatal(err)
				}
			}
			_, err = f.store.RecordAttempt(f.ctx, in)
			assertCode(t, err, want)
			var count int
			if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM upstream_attempts WHERE request_id=$1", r.RequestID).Scan(&count); err != nil || count != 0 {
				t.Fatal("refused checks inserted an attempt", count, err)
			}
			dispatchBatchFinancials(t, f, r, "accepted", reservationStatus)
		})
	}
}

func TestPostgresDispatchReadBatchCancellationDrainsAndRollsBack(t *testing.T) {
	for _, boundary := range []string{"channel", "credential", "reservation"} {
		t.Run(boundary, func(t *testing.T) {
			f := integration(t)
			dispatchBatchSingleConnectionStore(t, f)
			r := f.reserve(t)
			defer f.store.EndRequest(r.RequestID)
			a := dispatchBatchPrepared(t, f, r)
			blocker, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(f.ctx)
			var pid int
			var lockSQL, id string
			switch boundary {
			case "channel":
				lockSQL, id = "SELECT pg_backend_pid() FROM channels WHERE id=$1 FOR UPDATE", f.channel
			case "credential":
				lockSQL, id = "SELECT pg_backend_pid() FROM channel_credentials WHERE id=$1 FOR UPDATE", f.credential
			case "reservation":
				lockSQL, id = "SELECT pg_backend_pid() FROM reservations WHERE request_id=$1 FOR UPDATE", r.RequestID
			}
			if err = blocker.QueryRow(f.ctx, lockSQL, id).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- f.store.PrepareDispatch(ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}) }()
			waitForBatchBlocker(t, f, pid)
			cancel()
			if err = dispatchBatchAwait(t, done); !errors.Is(err, context.Canceled) {
				t.Fatal("blocked dispatch did not return cancellation", err)
			}
			if err = blocker.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			dispatchBatchState(t, f, r, a, "prepared", "accepted")
			if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
				t.Fatal("cancelled batch left the next transaction unusable", err)
			}
			dispatchBatchState(t, f, r, a, "sending", "dispatching")
		})
	}
}

func TestPostgresDispatchReadBatchMissingRowDrains(t *testing.T) {
	f := integration(t)
	dispatchBatchSingleConnectionStore(t, f)
	r := f.reserve(t)
	defer f.store.EndRequest(r.RequestID)
	a := dispatchBatchPrepared(t, f, r)
	err := f.store.transaction(f.ctx, func(tx pgx.Tx) error {
		_, err := readDispatchChannel(f.ctx, tx, f.channel, uuid.NewString(), f.credential, "upstream")
		return err
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("missing channel version lost the original no-row error", err)
	}
	dispatchBatchState(t, f, r, a, "prepared", "accepted")
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal("missing-row batch left the next transaction unusable", err)
	}
	dispatchBatchState(t, f, r, a, "sending", "dispatching")
}

// Append a real PostgreSQL execution error after the production read statements.
// This wrapper exists only in tests; no production fault injection is needed.
type dispatchBatchLateErrorTx struct{ pgx.Tx }

func (tx dispatchBatchLateErrorTx) SendBatch(ctx context.Context, batch *pgx.Batch) pgx.BatchResults {
	batch.Queue("SELECT 1/0")
	return tx.Tx.SendBatch(ctx, batch)
}

func TestPostgresDispatchReadBatchRetainsLateDatabaseError(t *testing.T) {
	for _, earlyRefusal := range []bool{false, true} {
		name := "successful_reads"
		if earlyRefusal {
			name = "domain_refusal"
		}
		t.Run(name, func(t *testing.T) {
			f := integration(t)
			dispatchBatchSingleConnectionStore(t, f)
			r := f.reserve(t)
			defer f.store.EndRequest(r.RequestID)
			a := dispatchBatchPrepared(t, f, r)
			if earlyRefusal {
				if _, err := f.pool.Exec(f.ctx, "UPDATE channels SET enabled=false WHERE id=$1", f.channel); err != nil {
					t.Fatal(err)
				}
			}
			err := f.store.transaction(f.ctx, func(tx pgx.Tx) error {
				_, err := readDispatchChannel(f.ctx, dispatchBatchLateErrorTx{tx}, f.channel, f.version, f.credential, "upstream")
				return err
			})
			var backend *pgconn.PgError
			if !errors.As(err, &backend) || backend.Code != "22012" {
				t.Fatal("lost database execution error while draining the read batch", err)
			}
			if earlyRefusal {
				assertCode(t, err, "channel_unavailable")
			}
			dispatchBatchState(t, f, r, a, "prepared", "accepted")
			if _, err = f.pool.Exec(f.ctx, "UPDATE channels SET enabled=true WHERE id=$1", f.channel); err != nil {
				t.Fatal(err)
			}
			if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
				t.Fatal("late SQL error left the business pool unusable", err)
			}
			dispatchBatchState(t, f, r, a, "sending", "dispatching")
		})
	}
}
