package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func retailPrice(t *testing.T, f *fixture, input, output int64, effective time.Time) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by)
 VALUES($1,$2,$3,$4,$5,$6)`, id, f.model, input, output, effective, f.operator); err != nil {
		t.Fatal(err)
	}
	return id
}

func retailNow(t *testing.T, f *fixture) time.Time {
	t.Helper()
	var now time.Time
	if err := f.pool.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	return now
}

func retailAssertSnapshot(t *testing.T, f *fixture, reservation Reservation, price string, input, output Money) {
	t.Helper()
	if reservation.PriceVersionID != price || reservation.InputRate != input || reservation.OutputRate != output {
		t.Fatalf("reservation has incorrect retail snapshot: %+v; want %s/%d/%d", reservation, price, input, output)
	}
	var storedPrice string
	var storedInput, storedOutput Money
	if err := f.pool.QueryRow(f.ctx, `SELECT price_version_id,input_rate,output_rate FROM request_records WHERE id=$1`, reservation.RequestID).Scan(&storedPrice, &storedInput, &storedOutput); err != nil {
		t.Fatal(err)
	}
	if storedPrice != price || storedInput != input || storedOutput != output {
		t.Fatalf("request snapshot changed: %s/%d/%d; want %s/%d/%d", storedPrice, storedInput, storedOutput, price, input, output)
	}
}

func TestPostgresRetailPriceLatestEffectiveAndKnownZero(t *testing.T) {
	f := integration(t)
	now := retailNow(t, f)
	// Insertion order and created_at cannot override the latest effective_at.
	latest := uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_at,created_by)
 VALUES($1,$2,2000000,3000000,$3,$4,$5)`, latest, f.model, now.Add(-time.Minute), now.Add(-3*time.Hour), f.operator); err != nil {
		t.Fatal(err)
	}
	retailPrice(t, f, 4000000, 4000000, now.Add(-2*time.Minute))
	retailPrice(t, f, 9000000, 9000000, now.Add(time.Hour))
	r := f.reserve(t)
	defer f.store.EndRequest(r.RequestID)
	retailAssertSnapshot(t, f, r, latest, 2000000, 3000000)
	if r.Amount != 260 {
		t.Fatal("reservation did not use both selected retail rates", r)
	}
	// The reservation deadline is based on the selected statement's DB cutoff,
	// rather than an application clock. A future version must not precede it.
	cutoff := r.Deadline.Add(-24 * time.Hour)
	if cutoff.Before(now.Add(-time.Minute)) || !cutoff.Before(now.Add(time.Hour)) {
		t.Fatal("retail snapshot and returned database cutoff disagree", r, cutoff)
	}
	if _, err := f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); err != nil {
		t.Fatal(err)
	}
	zero := retailPrice(t, f, 0, 0, retailNow(t, f).Add(-time.Second))
	free := f.reserve(t)
	defer f.store.EndRequest(free.RequestID)
	retailAssertSnapshot(t, f, free, zero, 0, 0)
	if free.Amount != 0 || free.PriceVersionID == "" {
		t.Fatal("known zero price was confused with an unavailable price", free)
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresRetailFutureOnlyPriceIsUnavailable(t *testing.T) {
	f := integration(t)
	f.model = uuid.NewString()
	for _, command := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,'future-only',
 '{"version":"v1","model":"future-only","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}','v1')`, []any{f.model}},
		{"INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", []any{f.tenant, f.model}},
		{"UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE id=$1", []any{f.key, f.model}},
	} {
		if _, err := f.pool.Exec(f.ctx, command.sql, command.args...); err != nil {
			t.Fatal(err)
		}
	}
	retailPrice(t, f, 1000000, 1000000, retailNow(t, f).Add(time.Hour))
	var journalsBefore int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM journals").Scan(&journalsBefore); err != nil {
		t.Fatal(err)
	}
	in := f.input()
	_, err := f.store.Reserve(f.ctx, in)
	assertCode(t, err, "price_unavailable")
	var requests, reservations, journals int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM request_records WHERE id=$1),
 (SELECT count(*) FROM reservations WHERE request_id=$1),(SELECT count(*) FROM journals)`, in.RequestID).Scan(&requests, &reservations, &journals); err != nil {
		t.Fatal(err)
	}
	if requests != 0 || reservations != 0 || journals != journalsBefore {
		t.Fatal("future-only retail price created partial financial facts", requests, reservations, journals, journalsBefore)
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func retailWaitForBlocker(t *testing.T, ctx context.Context, f *fixture, blocker pgx.Tx) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a
 WHERE $1::int=ANY(pg_blocking_pids(a.pid)))`, int32(blocker.Conn().PgConn().PID())).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("reservation did not reach its test lock", ctx.Err())
		case <-deadline.C:
			t.Fatal("reservation did not wait on its isolated blocker")
		case <-ticker.C:
		}
	}
}

func TestPostgresRetailPriceAfterModelLockUsesFreshDBCutoff(t *testing.T) {
	f := integration(t)
	effective := retailNow(t, f).Add(time.Second)
	next := retailPrice(t, f, 2000000, 3000000, effective)
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	if _, err = blocker.Exec(f.ctx, "SELECT id FROM models WHERE id=$1 FOR UPDATE", f.model); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	type outcome struct {
		reservation Reservation
		err         error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := f.store.Reserve(ctx, f.input())
		done <- outcome{r, err}
	}()
	retailWaitForBlocker(t, ctx, f, blocker)
	if !retailNow(t, f).Before(effective) {
		t.Fatal("test did not observe the model wait before price activation")
	}
	if _, err = f.pool.Exec(ctx, "SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM ($1::timestamptz-clock_timestamp())))+0.02)", effective); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer f.store.EndRequest(got.reservation.RequestID)
	retailAssertSnapshot(t, f, got.reservation, next, 2000000, 3000000)
	if got.reservation.Deadline.Add(-24 * time.Hour).Before(effective) {
		t.Fatal("retail cutoff was sampled before the model lock wait", got.reservation, effective)
	}
	f.wallet(t, 740, 260)
	f.check(t)
}

func TestPostgresRetailWalletWaitKeepsAlreadyFrozenPrice(t *testing.T) {
	f := integration(t)
	var original string
	if err := f.pool.QueryRow(f.ctx, "SELECT id FROM price_versions WHERE model_id=$1", f.model).Scan(&original); err != nil {
		t.Fatal(err)
	}
	effective := retailNow(t, f).Add(time.Second)
	retailPrice(t, f, 2000000, 3000000, effective)
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	if _, err = blocker.Exec(f.ctx, "SELECT id FROM ledger_accounts WHERE tenant_id=$1 ORDER BY id FOR UPDATE", f.tenant); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	type outcome struct {
		reservation Reservation
		err         error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := f.store.Reserve(ctx, f.input())
		done <- outcome{r, err}
	}()
	retailWaitForBlocker(t, ctx, f, blocker)
	if !retailNow(t, f).Before(effective) {
		t.Fatal("test did not observe the wallet wait before price activation")
	}
	if _, err = f.pool.Exec(ctx, "SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM ($1::timestamptz-clock_timestamp())))+0.02)", effective); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer f.store.EndRequest(got.reservation.RequestID)
	retailAssertSnapshot(t, f, got.reservation, original, 1000000, 1000000)
	if !got.reservation.Deadline.Add(-24 * time.Hour).Before(effective) {
		t.Fatal("test did not freeze the original price before the wallet wait", got.reservation, effective)
	}
	f.wallet(t, 880, 120)
	f.check(t)
}

func TestPostgresRetailPriceAndPublicationDoNotRewriteAcceptedRequest(t *testing.T) {
	f := integration(t)
	accepted := f.reserve(t)
	defer f.store.EndRequest(accepted.RequestID)
	next := retailPrice(t, f, 2000000, 3000000, retailNow(t, f).Add(-time.Second))
	for _, visible := range []bool{true, false} {
		if _, err := f.pool.Exec(f.ctx, "UPDATE models SET public_visible=$2 WHERE id=$1", f.model, visible); err != nil {
			t.Fatal(err)
		}
	}
	retailAssertSnapshot(t, f, accepted, accepted.PriceVersionID, 1000000, 1000000)
	later := f.reserve(t)
	defer f.store.EndRequest(later.RequestID)
	retailAssertSnapshot(t, f, later, next, 2000000, 3000000)
	for _, tc := range []struct {
		reservation Reservation
		charge      Money
	}{{accepted, 15}, {later, 35}} {
		f.final(t, tc.reservation, confirmed(), "completed", "done", "none", false)
		settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: tc.reservation.RequestID, Mode: "live"})
		if err != nil || settled.ChargedAmount != tc.charge {
			t.Fatal("settlement recalculated a frozen retail price", settled, err, tc.charge)
		}
	}
	retailAssertSnapshot(t, f, accepted, accepted.PriceVersionID, 1000000, 1000000)
	f.wallet(t, 950, 0)
	f.check(t)
}

func TestPostgresRetailSameEffectivePriceRemainsUnique(t *testing.T) {
	f := integration(t)
	var effective time.Time
	var original string
	if err := f.pool.QueryRow(f.ctx, "SELECT id,effective_at FROM price_versions WHERE model_id=$1", f.model).Scan(&original, &effective); err != nil {
		t.Fatal(err)
	}
	_, err := f.pool.Exec(f.ctx, `INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by)
 VALUES($1,$2,2000000,3000000,$3,$4)`, uuid.NewString(), f.model, effective, f.operator)
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "23505" {
		t.Fatal("same-model same-effective_at retail version was not rejected by the original uniqueness constraint", err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM price_versions WHERE model_id=$1", f.model).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate price attempt changed immutable price history", count, err)
	}
	r := f.reserve(t)
	defer f.store.EndRequest(r.RequestID)
	retailAssertSnapshot(t, f, r, original, 1000000, 1000000)
	f.check(t)
}
