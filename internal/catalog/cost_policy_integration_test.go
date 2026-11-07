package catalog_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/routecost"
	"github.com/moni-ai/moni-aigate/internal/routing"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
	"github.com/moni-ai/moni-aigate/migrations"
)

func quoteRatesEqual(q routecost.Quote, input, output int64) bool {
	actual, err := q.Rates()
	if err != nil {
		return false
	}
	expected, err := metering.LegacyTokenRates(q.Schema(), metering.Supplier, input, output)
	if err != nil {
		return false
	}
	a, _ := actual.Digest()
	b, _ := expected.Digest()
	return a == b
}
func costText(value string) *string { return &value }

func costRate(value string) *int64 {
	v, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		panic(err)
	}
	return &v
}

func costChannelRevision(t *testing.T, f *credentialPoolFixture) string {
	t.Helper()
	var revision string
	if err := f.owner.QueryRow(f.ctx, `SELECT management_revision::text FROM channels WHERE id=$1`, f.channel).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func costInput(t *testing.T, f *credentialPoolFixture, model string, effective *time.Time) catalog.CostPriceCommand {
	t.Helper()
	at := time.Now().UTC().Truncate(time.Microsecond)
	if effective != nil {
		at = effective.UTC().Truncate(time.Microsecond)
	}
	revision, err := strconv.ParseInt(costChannelRevision(t, f), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	in := catalog.CostPriceCommand{OperationID: uuid.NewString(), ChannelID: f.channel, UpstreamModel: model, VariantKey: "default", InputRate: costRate("1000001"), OutputRate: costRate("2000003"), Basis: "contract", EffectiveAt: at, ExpectedChannelRevision: revision}
	if err := f.owner.QueryRow(f.ctx, `SELECT schema_id,encode(schema_digest,'hex') FROM model_capability_versions WHERE model_id=$1`, f.model).Scan(&in.SchemaID, &in.SchemaDigest); err != nil {
		t.Fatal(err)
	}
	return in
}

func costCandidate(t *testing.T, f *credentialPoolFixture, revision int64) catalog.Candidate {
	t.Helper()
	rows, err := f.store.CandidateSnapshot(f.ctx, f.model, revision)
	if err != nil || len(rows) != 1 {
		t.Fatalf("candidate snapshot len=%d err=%v", len(rows), err)
	}
	return rows[0]
}

func TestCostRoutePolicyRevisionCapsAndRollback(t *testing.T) {
	f := credentialPoolDB(t, true)
	binding := catalog.BindingInput{ChannelID: f.channel, UpstreamModel: "upstream", Priority: 3, Weight: 7, MaxInflight: 9, MaxInputCostRate: costText("0"), MaxOutputCostRate: costText("9223372036854775807")}
	policy := routing.CostPolicy{Mode: "cost_first", UnknownCostPolicy: "deny"}
	revision, err := f.store.PublishWithCostPolicy(f.ctx, "pool-admin", f.model, 0, []catalog.BindingInput{binding}, policy)
	if err != nil || revision != 1 {
		t.Fatal("complete cost policy publish", revision, err)
	}
	first := costCandidate(t, f, 1)
	if first.CostPolicy != policy || !reflect.DeepEqual(first.CostLimits.Entries(), []metering.Rate{{Dimension: "text_input_token", Unit: "token", Numerator: 0, Denominator: 1}, {Dimension: "text_output_token", Unit: "token", Numerator: 9223372036854775807, Denominator: 1000000}}) || first.CostQuote.Known() || first.CostQuote.VersionID() != "" {
		t.Fatalf("cost policy/caps/unknown quote not frozen: %+v", first)
	}
	before := f.state(t)
	if _, err = f.store.PublishWithCostPolicy(f.ctx, "pool-admin", f.model, 0, []catalog.BindingInput{binding}, policy); !errors.Is(err, catalog.ErrConflict) || f.state(t) != before {
		t.Fatal("stale route CAS changed state", err)
	}

	invalid := []struct {
		name   string
		policy routing.CostPolicy
		rate   *string
	}{
		{"mode", routing.CostPolicy{Mode: "cheapest", UnknownCostPolicy: "deny"}, costText("0")},
		{"unknown", routing.CostPolicy{Mode: "manual", UnknownCostPolicy: "zero"}, costText("0")},
		{"cost_first_allow", routing.CostPolicy{Mode: "cost_first", UnknownCostPolicy: "allow"}, costText("0")},
	}
	for _, rate := range []string{"-1", "01", "+1", " 1", "1.0", "9223372036854775808"} {
		invalid = append(invalid, struct {
			name   string
			policy routing.CostPolicy
			rate   *string
		}{rate, policy, costText(rate)})
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			bad := binding
			bad.MaxInputCostRate = tc.rate
			if _, e := f.store.PublishWithCostPolicy(f.ctx, "pool-admin", f.model, 1, []catalog.BindingInput{bad}, tc.policy); !errors.Is(e, catalog.ErrInvalid) || f.state(t) != before {
				t.Fatal("invalid route left revision/binding/audit", e)
			}
		})
	}
	binding.MaxInputCostRate = nil
	binding.MaxOutputCostRate = nil
	if _, err = f.store.Publish(f.ctx, "pool-admin", f.model, 1, []catalog.BindingInput{binding}); err != nil {
		t.Fatal(err)
	}
	second := costCandidate(t, f, 2)
	if second.CostPolicy != routing.DefaultCostPolicy() || !second.CostLimits.Empty() {
		t.Fatal("legacy publisher did not preserve manual/allow", second)
	}
	retained := costCandidate(t, f, 1)
	if retained.CostPolicy != policy || !retained.CostLimits.Equal(first.CostLimits) {
		t.Fatal("new revision mutated old cost policy", retained)
	}
	for _, mutation := range []struct{ statement, target string }{{`UPDATE route_policy_versions SET cost_mode='manual' WHERE model_id=$1`, f.model}, {`UPDATE model_bindings SET max_input_cost_rate=1 WHERE channel_id=$1`, f.channel}} {
		if _, e := f.owner.Exec(f.ctx, mutation.statement, mutation.target); e == nil {
			t.Fatal("owner mutated immutable cost route", mutation.statement)
		}
	}
}

type costDBState struct {
	revision      string
	costs, audits int64
}

func costState(t *testing.T, f *credentialPoolFixture) costDBState {
	t.Helper()
	var state costDBState
	if err := f.owner.QueryRow(f.ctx, `SELECT management_revision::text,(SELECT count(*) FROM cost_versions),(SELECT count(*) FROM audit_events) FROM channels WHERE id=$1`, f.channel).Scan(&state.revision, &state.costs, &state.audits); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestCostPublishCASCanonicalMoneyAndAuditAtomicity(t *testing.T) {
	f := credentialPoolDB(t, true)
	effective := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("fixture", 8*3600))
	in := costInput(t, f, "upstream", &effective)
	before := costState(t, f)
	result, err := f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, in)
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := strconv.ParseInt(before.revision, 10, 64)
	if result.Result.ID == "" || result.Result.ChannelRevision != strconv.FormatInt(initial+1, 10) {
		t.Fatal("cost insert did not advance channel CAS exactly once", result)
	}
	var at time.Time
	var canonical []byte
	var basis string
	var audits int
	if err = f.owner.QueryRow(f.ctx, `SELECT effective_at,rates_canonical,basis,(SELECT count(*) FROM audit_events WHERE action='pricing.cost.publish' AND target='/api/platform/cost-versions') FROM cost_versions WHERE id=$1`, result.Result.ID).Scan(&at, &canonical, &basis, &audits); err != nil || !at.Equal(effective.UTC().Truncate(time.Microsecond)) || !strings.Contains(string(canonical), `"side":"supplier"`) || !strings.Contains(string(canonical), `"1000001"`) || !strings.Contains(string(canonical), `"2000003"`) || basis != "contract" || audits != 1 {
		t.Fatal("cost quote normalization or audit", at, string(canonical), basis, audits, err)
	}
	after := costState(t, f)
	replay, err := f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, in)
	if err != nil || !replay.Replayed || replay.Result.ID != result.Result.ID || costState(t, f) != after {
		t.Fatal("same operation did not replay its original cost receipt", replay, err)
	}
	stale := in
	stale.OperationID = uuid.NewString()
	if _, err = f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, stale); err == nil || costState(t, f) != after {
		t.Fatal("stale cost CAS changed state", err)
	}
	collision := in
	collision.OperationID = uuid.NewString()
	collision.ExpectedChannelRevision, _ = strconv.ParseInt(after.revision, 10, 64)
	if _, err = f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, collision); err == nil || costState(t, f) != after {
		t.Fatal("same effective instant changed state", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*catalog.CostPriceCommand)
	}{
		{"expected_zero", func(v *catalog.CostPriceCommand) { v.ExpectedChannelRevision = 0 }},
		{"schema_missing", func(v *catalog.CostPriceCommand) { v.SchemaID = "" }},
		{"schema_nil", func(v *catalog.CostPriceCommand) { v.SchemaID = "00000000-0000-0000-0000-000000000000" }},
		{"schema_unpublished", func(v *catalog.CostPriceCommand) { v.SchemaID = uuid.NewString() }},
		{"schema_digest_missing", func(v *catalog.CostPriceCommand) { v.SchemaDigest = "" }},
		{"schema_uppercase", func(v *catalog.CostPriceCommand) { v.SchemaID = "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA" }},
		{"schema_digest_uppercase", func(v *catalog.CostPriceCommand) {
			v.SchemaDigest = "ABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABAB"
		}},
		{"schema_digest_wrong", func(v *catalog.CostPriceCommand) {
			v.SchemaDigest = "abababababababababababababababababababababababababababababababab"
		}},
		{"variant_missing", func(v *catalog.CostPriceCommand) { v.VariantKey = "" }},
		{"variant_other", func(v *catalog.CostPriceCommand) { v.VariantKey = "other" }},
		{"missing_input", func(v *catalog.CostPriceCommand) { v.InputRate = nil }},
		{"missing_output", func(v *catalog.CostPriceCommand) { v.OutputRate = nil }},
		{"unknown_with_rate", func(v *catalog.CostPriceCommand) { v.Basis = "unknown" }},
		{"basis", func(v *catalog.CostPriceCommand) { v.Basis = "estimate" }},
		{"negative", func(v *catalog.CostPriceCommand) { v.InputRate = costRate("-1") }},
		{"empty_model", func(v *catalog.CostPriceCommand) { v.UpstreamModel = "" }},
		{"uppercase_channel", func(v *catalog.CostPriceCommand) { v.ChannelID = "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA" }},
		{"utc_year_overflow", func(v *catalog.CostPriceCommand) {
			at := time.Date(9999, 12, 31, 23, 0, 0, 0, time.FixedZone("minus", -3600))
			v.EffectiveAt = at
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := in
			tc.edit(&bad)
			if _, e := f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, bad); e == nil || costState(t, f) != after {
				t.Fatal("invalid cost changed state", e)
			}
		})
	}
	if _, err = f.owner.Exec(f.ctx, "REVOKE INSERT ON audit_events FROM "+pgx.Identifier{f.appRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	newInput := costInput(t, f, "other-model", nil)
	if _, err = f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, newInput); err == nil || costState(t, f) != after {
		t.Fatal("audit failure left cost quote/channel revision", err)
	}
	if _, err = f.owner.Exec(f.ctx, "GRANT INSERT ON audit_events TO "+pgx.Identifier{f.appRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	unknown := costInput(t, f, "unknown-model", nil)
	unknown.Basis = "unknown"
	unknown.InputRate = nil
	unknown.OutputRate = nil
	if _, err = f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, unknown); err != nil {
		t.Fatal("explicit unknown quote rejected", err)
	}
	zero := costInput(t, f, "free-model", nil)
	zero.InputRate = costRate("0")
	zero.OutputRate = costRate("0")
	zero.Basis = "invoice"
	if _, err = f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, zero); err != nil {
		t.Fatal("known free quote rejected", err)
	}
}

func TestCostPublishConcurrentCASHasSingleWinner(t *testing.T) {
	f := credentialPoolDB(t, true)
	in := costInput(t, f, "upstream", nil)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			candidate := in
			candidate.OperationID = uuid.NewString()
			_, err := f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, candidate)
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if strings.Contains(err.Error(), "pricing_revision_conflict") {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	state := costState(t, f)
	expected := in.ExpectedChannelRevision
	if wins != 1 || conflicts != 1 || state.costs != 1 || state.revision != strconv.FormatInt(expected+1, 10) {
		t.Fatal("CAS did not give one winner", wins, conflicts, state)
	}
}

func TestCostCandidateExactModelFutureActivationAndPoolQuote(t *testing.T) {
	f := credentialPoolDB(t, true)
	f.publish(t, 0)
	initial := costCandidate(t, f, 1)
	if initial.CostQuote.Known() || initial.CostQuote.VersionID() != "" || initial.CostQuote.Basis() != "unknown" {
		t.Fatal("missing quote became zero", initial.CostQuote)
	}
	past := time.Now().Add(-time.Hour)
	wrong := costInput(t, f, "Upstream", &past)
	if _, err := f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, wrong); err != nil {
		t.Fatal(err)
	}
	if v := costCandidate(t, f, 1); v.CostQuote.VersionID() != "" {
		t.Fatal("quote matched case-insensitive upstream name", v.CostQuote)
	}
	current := costInput(t, f, "upstream", &past)
	published, err := f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, current)
	if err != nil {
		t.Fatal(err)
	}
	if v := costCandidate(t, f, 1); v.CostQuote.VersionID() != published.Result.ID || !v.CostQuote.Known() || !quoteRatesEqual(v.CostQuote, 1000001, 2000003) {
		t.Fatal("exact active quote missing", v.CostQuote)
	}
	var future time.Time
	if err = f.owner.QueryRow(f.ctx, `SELECT clock_timestamp()+interval '400 milliseconds'`).Scan(&future); err != nil {
		t.Fatal(err)
	}
	scheduled := costInput(t, f, "upstream", &future)
	scheduled.InputRate = costRate("0")
	scheduled.OutputRate = costRate("0")
	scheduled.Basis = "invoice"
	next, err := f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, scheduled)
	if err != nil {
		t.Fatal(err)
	}
	if v := costCandidate(t, f, 1); v.CostQuote.VersionID() != published.Result.ID {
		t.Fatal("future quote applied early", v.CostQuote)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var reached bool
		if err = f.owner.QueryRow(f.ctx, `SELECT clock_timestamp()>=$1`, future).Scan(&reached); err != nil {
			t.Fatal(err)
		}
		if reached {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("database clock did not pass scheduled quote")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if v := costCandidate(t, f, 1); v.CostQuote.VersionID() != next.Result.ID || !v.CostQuote.Known() || !quoteRatesEqual(v.CostQuote, 0, 0) {
		t.Fatal("scheduled known free quote did not activate", v.CostQuote)
	}
	f.addSecondary(t)
	f.publish(t, 1)
	rows, err := f.store.CandidateSnapshot(f.ctx, f.model, 2)
	if err != nil || len(rows) != 2 || !rows[0].CostQuote.Equal(rows[1].CostQuote) || rows[0].CostQuote.VersionID() != next.Result.ID {
		t.Fatal("pool members received different procurement quote", rows, err)
	}
}

func TestCostRawInsertSynchronizesShareLockAndRejectsConcurrentDuplicate(t *testing.T) {
	f := credentialPoolDB(t, true)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	holder, err := f.app.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(f.ctx)
	var holderPID int32
	if err = holder.QueryRow(ctx, `SELECT pg_backend_pid() FROM channels WHERE id=$1 FOR SHARE`, f.channel).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	writer, err := f.app.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Release()
	var writerPID int32
	if err = writer.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&writerPID); err != nil {
		t.Fatal(err)
	}
	effective := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	before := costState(t, f)
	done := make(chan error, 1)
	go func() {
		_, e := meterquote.TryPublishCost(ctx, writer, meterquote.CostPrice{ChannelID: f.channel, UpstreamModel: "raw", ModelID: f.model, Actor: poolPricingActorID, Basis: "contract", InputRate: costRate("0"), OutputRate: costRate("0"), EffectiveAt: effective})
		done <- e
	}()
	blocked := false
	for !blocked {
		if err = f.owner.QueryRow(ctx, `SELECT $1=ANY(pg_blocking_pids($2))`, holderPID, writerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		select {
		case e := <-done:
			t.Fatal("raw insert bypassed channel SHARE barrier", e)
		default:
		}
		if !blocked {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if state := costState(t, f); state != before {
		t.Fatal("blocked INSERT changed channel or costs", state)
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	state := costState(t, f)
	old, _ := strconv.ParseInt(before.revision, 10, 64)
	if state.costs != before.costs+1 || state.revision != strconv.FormatInt(old+1, 10) {
		t.Fatal("raw insert did not increment channel revision", state)
	}

	effective = effective.Add(time.Second)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, e := meterquote.TryPublishCost(ctx, f.app, meterquote.CostPrice{ChannelID: f.channel, UpstreamModel: "duplicate", ModelID: f.model, Actor: poolPricingActorID, Basis: "invoice", InputRate: costRate("1"), OutputRate: costRate("2"), EffectiveAt: effective})
			results <- e
		}()
	}
	close(start)
	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		e := <-results
		if e == nil {
			wins++
		} else {
			var pg *pgconn.PgError
			if !errors.As(e, &pg) || pg.Code != "23505" {
				t.Fatal("unexpected concurrent duplicate error", e)
			}
			conflicts++
		}
	}
	after := costState(t, f)
	if wins != 1 || conflicts != 1 || after.costs != state.costs+1 || after.revision != strconv.FormatInt(old+2, 10) {
		t.Fatal("concurrent raw duplicate not atomic", wins, conflicts, after)
	}
}

func TestCostProjectionCannotReadProcurementPolicy(t *testing.T) {
	f := credentialPoolDB(t, true)
	cfg := f.owner.Config()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{f.projector}.Sanitize())
		return err
	}
	projector, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer projector.Close()
	for _, statement := range []string{`SELECT cost_mode,unknown_cost_policy FROM route_policy_versions`, `SELECT variant_key FROM model_bindings`, `SELECT rates_canonical,rates_digest FROM cost_versions`, `INSERT INTO cost_versions(channel_id,upstream_model) VALUES('forbidden','forbidden')`} {
		_, e := projector.Exec(f.ctx, statement)
		var pg *pgconn.PgError
		if !errors.As(e, &pg) || pg.Code != "42501" {
			t.Fatal("projector accessed procurement state", statement, e)
		}
	}
}

func TestCostMigrationPreservesLegacyDuplicatesAndDeterministicQuotes(t *testing.T) {
	f := credentialPoolDB(t, false)
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		if name >= "013_cost_aware_routing.sql" {
			break
		}
		body, e := migrations.Files.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.owner.Exec(f.ctx, string(body)); e != nil {
			t.Fatal(name, e)
		}
	}
	secrets := &security.Secrets{Keys: map[string][]byte{"pool": bytes.Repeat([]byte{0x63}, 32)}, Active: "pool", Pepper: bytes.Repeat([]byte{0x77}, 32)}
	f.store = catalog.New(f.owner, secrets)
	if f.channel, err = f.store.SaveChannel(f.ctx, "pool-admin", "", catalog.ChannelInput{Name: "legacy-cost", BaseURL: "https://example.com/v1", Secret: "legacy-cost-secret"}); err != nil {
		t.Fatal(err)
	}
	f.model = uuid.NewString()
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,'legacy-cost-model',$2,'legacy')`, f.model, `{"version":"legacy","model":"legacy-cost-model","context_limit":100,"input_token_upper_bound":80,"output_limit":20,"default_output_limit":20}`); err != nil {
		t.Fatal(err)
	}
	policy := uuid.NewString()
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO route_policy_versions(id,model_id,revision) VALUES($1,$2,1)`, policy, f.model); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,weight,max_inflight) SELECT $1,id,current_version,'legacy-upstream',1,1 FROM channels WHERE id=$2`, policy, f.channel); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE models SET active_revision=1 WHERE id=$1`, f.model); err != nil {
		t.Fatal(err)
	}
	effective := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	created := effective.Add(time.Minute)
	ids := []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"}
	for i, id := range ids {
		at := created
		if i == 0 {
			at = at.Add(-time.Second)
		}
		if _, err = f.owner.Exec(f.ctx, `INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at,created_at) VALUES($1,$2,'legacy-upstream',$3,0,'contract',$4,$5)`, id, f.channel, int64(i), effective, at); err != nil {
			t.Fatal(err)
		}
	}
	body, err := migrations.Files.ReadFile("013_cost_aware_routing.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, string(body)); err != nil {
		t.Fatal("013 upgrade rejected old duplicate instants", err)
	}
	// Inspect the original 013 contract directly. Current catalog readers
	// require 024 and must not grow a legacy-schema execution branch.
	var mode, unknown, selected string
	var inputCap, outputCap *int64
	var selectedRate int64
	if err = f.owner.QueryRow(f.ctx, `SELECT p.cost_mode,p.unknown_cost_policy,b.max_input_cost_rate,b.max_output_cost_rate,c.id,c.input_rate
 FROM route_policy_versions p JOIN model_bindings b ON b.route_policy_version_id=p.id
 JOIN LATERAL (SELECT id,input_rate FROM cost_versions WHERE channel_id=b.channel_id AND upstream_model=b.upstream_model AND effective_at<=clock_timestamp() ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1) c ON true WHERE p.id=$1`, policy).Scan(&mode, &unknown, &inputCap, &outputCap, &selected, &selectedRate); err != nil || mode != "manual" || unknown != "allow" || inputCap != nil || outputCap != nil || selected != ids[2] || selectedRate != 2 {
		t.Fatal("legacy defaults or tie order changed", mode, unknown, selected, selectedRate, err)
	}
	before := costState(t, f)
	_, err = f.owner.Exec(f.ctx, `INSERT INTO cost_versions(channel_id,upstream_model,input_rate,output_rate,basis,effective_at) VALUES($1,'legacy-upstream',9,0,'contract',$2)`, f.channel, effective)
	var conflict *pgconn.PgError
	if !errors.As(err, &conflict) || conflict.Code != "23505" || conflict.ConstraintName != "cost_version_effective_instant" || costState(t, f) != before {
		t.Fatal("new duplicate of legacy instant allowed", err)
	}
	var count int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM cost_versions WHERE channel_id=$1 AND effective_at=$2`, f.channel, effective).Scan(&count); err != nil || count != 3 {
		t.Fatal("legacy duplicates deleted", count, err)
	}
}
