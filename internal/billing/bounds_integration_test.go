package billing

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresIndependentContextBoundsReserveAndSettle(t *testing.T) {
	f := integration(t)
	// A separate fixture profile leaves every old model/version untouched.
	f.model = uuid.NewString()
	version := uuid.NewString()
	profile := `{"version":"` + version + `","model":"full-context","protocol":"openai_chat","input_token_upper_bound":120,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"SELECT meter_insert_text_model($1,'full-context',$2::jsonb,$3)", []any{f.model, profile, version}},
		{"INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", []any{f.tenant, f.model}},
		{"UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE id=$1", []any{f.key, f.model}},
	} {
		if _, err := f.pool.Exec(f.ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	billingPoolPublish(t, f, f.version, 1)
	publishBillingDefault(t, f, f.model, 1000000, 1000000, time.Now().UTC().Add(-time.Hour))
	publishBillingCost(t, f, f.channel, "upstream", "contract", billingRate(2000000), billingRate(2000000), time.Time{})
	in := f.input()
	in.ModelID, in.ProfileVersion, in.RouteRevision = f.model, version, 1
	in.InputUpperBound = 120
	r, err := f.store.Reserve(f.ctx, in)
	if err != nil || r.Amount != 140 {
		t.Fatal("reservation did not cover independent bounds", r, err)
	}
	defer f.store.EndRequest(r.RequestID)
	a, err := f.store.RecordAttempt(f.ctx, AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.RecordFinalization(f.ctx, FinalizationInput{TenantID: f.tenant, RequestID: r.RequestID, AttemptID: a.ID, Source: "adapter", SourceEventID: a.ID + ":final", Usage: Usage{Status: "confirmed_final", InputTokens: 115, OutputTokens: 3, TotalTokens: 118}, ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none", FinishReason: "stop"})
	if err != nil {
		t.Fatal(err)
	}
	settlement, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "live"})
	if err != nil || settlement.Status != "settled" || settlement.ChargedAmount != 118 {
		t.Fatal(settlement, err)
	}
	wallet, err := f.store.Wallet(f.ctx, Query{TenantID: f.tenant})
	if err != nil || wallet.Available != 882 || wallet.Held != 0 {
		t.Fatal(wallet, err)
	}
	ledger, err := f.store.VerifyLedger(f.ctx, Query{TenantID: f.tenant})
	if err != nil || !ledger.Balanced {
		t.Fatal(ledger, err)
	}
}
