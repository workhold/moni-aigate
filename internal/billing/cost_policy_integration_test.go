package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/routecost"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func managedCostRequest(t *testing.T, f *fixture) (Reservation, AttemptInput) {
	t.Helper()
	policy := uuid.NewString()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO route_policy_versions(id,model_id,revision,cost_mode,unknown_cost_policy) VALUES($1,$2,1,'cost_first','deny')`, []any{policy, f.model}},
		{`INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,weight,max_inflight) VALUES($1,$2,$3,'upstream',1,4)`, []any{policy, f.channel, f.version}},
		{`UPDATE models SET active_revision=1 WHERE id=$1`, []any{f.model}},
	} {
		if _, err := tx.Exec(f.ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	in := f.input()
	in.RouteRevision = 1
	r, err := f.store.Reserve(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.store.EndRequest(r.RequestID) })
	q, err := scanCostQuote(f.pool.QueryRow(f.ctx, currentAttemptCostSQL, f.channel, "upstream", r.RequestID), f.channel, "upstream")
	if err != nil {
		t.Fatal(err)
	}
	a := dispatchBatchAttemptInput(f, r)
	a.CostQuote = &q
	return r, a
}

func TestPostgresManagedAttemptRequiresExactSelectedQuote(t *testing.T) {
	for _, kind := range []string{"missing", "version", "rate", "basis", "valid"} {
		t.Run(kind, func(t *testing.T) {
			f := integration(t)
			r, in := managedCostRequest(t, f)
			switch kind {
			case "missing":
				in.CostQuote = nil
			case "version", "rate", "basis":
				var err error
				q := *in.CostQuote
				rates, _ := q.Rates()
				value := routecost.Input{ChannelID: f.channel, UpstreamModel: "upstream", VersionID: q.VersionID(), Basis: q.Basis(), VariantKey: "default", Schema: q.Schema(), Rates: rates}
				switch kind {
				case "version":
					value.VersionID = uuid.NewString()
				case "basis":
					value.Basis = "invoice"
				case "rate":
					value.Rates, err = metering.ManagementTokenRates(q.Schema(), metering.Supplier, 2000000, 1)
					if err != nil {
						t.Fatal(err)
					}
				}
				changed, e := routecost.NewQuote(value)
				if e != nil {
					t.Fatal(e)
				}
				in.CostQuote = &changed
			}
			a, err := f.store.RecordAttempt(f.ctx, in)
			if kind == "valid" {
				if err != nil || a.DeliveryState != "prepared" {
					t.Fatal(a, err)
				}
				if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
					t.Fatal(err)
				}
			} else {
				assertCode(t, err, "cost_quote_stale")
				var count int
				if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM upstream_attempts").Scan(&count); err != nil || count != 0 {
					t.Fatal("stale quote inserted attempt", count, err)
				}
				keyConfigurationRelease(t, f, r)
			}
		})
	}
}

func TestPostgresCostPublicationWaitersSeeNewQuote(t *testing.T) {
	for _, boundary := range []string{"attempt", "dispatch"} {
		t.Run(boundary, func(t *testing.T) {
			f := integration(t)
			dispatchBatchSingleConnectionStore(t, f)
			r, in := managedCostRequest(t, f)
			var a Attempt
			var err error
			if boundary == "dispatch" {
				a, err = f.store.RecordAttempt(f.ctx, in)
				if err != nil {
					t.Fatal(err)
				}
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
			// INSERT itself must hold the common channel UPDATE barrier.
			meterquote.PublishCost(t, f.ctx, blocker, meterquote.CostPrice{ChannelID: f.channel, UpstreamModel: "upstream", ModelID: f.model, Actor: f.admin, Basis: "contract", InputRate: billingRate(9000000), OutputRate: billingRate(9000000)})
			ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if boundary == "attempt" {
					_, e := f.store.RecordAttempt(ctx, in)
					done <- e
				} else {
					done <- f.store.PrepareDispatch(ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID})
				}
			}()
			waitForBatchBlocker(t, f, pid)
			if err = blocker.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			assertCode(t, dispatchBatchAwait(t, done), "cost_quote_stale")
			var sending int
			if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM upstream_attempts WHERE delivery_state<>'prepared'").Scan(&sending); err != nil || sending != 0 {
				t.Fatal("stale quote became sending", sending, err)
			}
			keyConfigurationRelease(t, f, r)
		})
	}
}

func TestPostgresScheduledCostActivationAfterAllDispatchLockWaits(t *testing.T) {
	for _, tc := range []struct{ boundary, lock string }{
		{"attempt", "channel"}, {"attempt", "reservation"},
		{"dispatch", "key"}, {"dispatch", "model"}, {"dispatch", "channel"},
		{"dispatch", "credential"}, {"dispatch", "reservation"}, {"dispatch", "attempt"},
	} {
		t.Run(tc.boundary+"/"+tc.lock, func(t *testing.T) {
			f := integration(t)
			dispatchBatchSingleConnectionStore(t, f)
			r, in := managedCostRequest(t, f)
			var a Attempt
			var err error
			if tc.boundary == "dispatch" {
				a, err = f.store.RecordAttempt(f.ctx, in)
				if err != nil {
					t.Fatal(err)
				}
			}
			var effective time.Time
			costID := uuid.NewString()
			meterquote.PublishCost(t, f.ctx, f.pool, meterquote.CostPrice{ID: costID, ChannelID: f.channel, UpstreamModel: "upstream", ModelID: f.model, Actor: f.admin, Basis: "invoice", InputRate: billingRate(3000000), OutputRate: billingRate(4000000), EffectiveAt: time.Now().UTC().Add(300 * time.Millisecond)})
			if err = f.pool.QueryRow(f.ctx, `SELECT effective_at FROM cost_versions WHERE id=$1`, costID).Scan(&effective); err != nil {
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
			sql, id := "", ""
			switch tc.lock {
			case "key":
				sql, id = "SELECT id FROM api_keys WHERE id=$1 FOR UPDATE", f.key
			case "model":
				sql, id = "SELECT id FROM models WHERE id=$1 FOR UPDATE", f.model
			case "channel":
				sql, id = "SELECT id FROM channels WHERE id=$1 FOR UPDATE", f.channel
			case "credential":
				sql, id = "SELECT id FROM channel_credentials WHERE id=$1 FOR UPDATE", f.credential
			case "reservation":
				sql, id = "SELECT id FROM reservations WHERE id=$1 FOR UPDATE", r.ID
			case "attempt":
				sql, id = "SELECT id FROM upstream_attempts WHERE id=$1 FOR UPDATE", a.ID
			}
			var ignored string
			if err = blocker.QueryRow(f.ctx, sql, id).Scan(&ignored); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if tc.boundary == "attempt" {
					_, e := f.store.RecordAttempt(ctx, in)
					done <- e
				} else {
					done <- f.store.PrepareDispatch(ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID})
				}
			}()
			waitForBatchBlocker(t, f, pid)
			for {
				var active bool
				if err = f.pool.QueryRow(ctx, "SELECT clock_timestamp()>=$1", effective).Scan(&active); err != nil {
					t.Fatal(err)
				}
				if active {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("scheduled cost did not activate")
				case <-time.After(5 * time.Millisecond):
				}
			}
			if err = blocker.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			assertCode(t, dispatchBatchAwait(t, done), "cost_quote_stale")
			keyConfigurationRelease(t, f, r)
		})
	}
}

func TestPostgresCostChangeAfterSendingKeepsFrozenCostAndSettlement(t *testing.T) {
	f := integration(t)
	r, in := managedCostRequest(t, f)
	a, err := f.store.RecordAttempt(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	meterquote.PublishCost(t, f.ctx, f.pool, meterquote.CostPrice{ChannelID: f.channel, UpstreamModel: "upstream", ModelID: f.model, Actor: f.admin, Basis: "unknown"})
	if _, err = f.store.RecordFinalization(f.ctx, FinalizationInput{TenantID: f.tenant, RequestID: r.RequestID, AttemptID: a.ID, Source: "adapter", SourceEventID: a.ID + ":final", Usage: confirmed(), ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID}); err != nil {
			t.Fatal(err)
		}
	}
	var cost, basis, version string
	var settlements int
	if err = f.pool.QueryRow(f.ctx, `SELECT a.cost_amount::text,s.cost_basis,s.cost_version_id,(SELECT count(*) FROM settlements WHERE request_id=$2) FROM upstream_attempts a JOIN attempt_meter_snapshots s ON s.attempt_id=a.id WHERE a.id=$1`, a.ID, r.RequestID).Scan(&cost, &basis, &version, &settlements); err != nil || cost != "30" || basis != "contract" || version != in.CostQuote.VersionID() || settlements != 1 {
		t.Fatal("later cost configuration changed history", cost, basis, version, settlements, err)
	}
	f.wallet(t, 985, 0)
	f.check(t)
}
