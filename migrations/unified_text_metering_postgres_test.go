package migrations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/inference"
	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func TestUnifiedTextMeteringCanonical(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "meter_u_" + uuid.NewString()[:8]
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	user := uuid.NewString()
	model := uuid.NewString()
	profileVersion := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,email,password_hash) VALUES($1,$2,'unused')`, user, user+"@test.invalid"); err != nil {
		t.Fatal(err)
	}
	profile := `{"version":"` + profileVersion + `","model":"text-model","protocol":"openai_chat","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`
	if _, err = pool.Exec(ctx, `SELECT meter_insert_text_model($1,'text-model',$2::jsonb,$3)`, model, profile, profileVersion); err != nil {
		t.Fatal(err)
	}
	for _, invalidVersion := range []string{"v1", "00000000-0000-0000-0000-000000000000", "10000000-0000-4000-8000-0000000000AB"} {
		invalidProfile := strings.ReplaceAll(strings.ReplaceAll(profile, profileVersion, invalidVersion), "text-model", "bad-model")
		if _, err = pool.Exec(ctx, `SELECT meter_insert_text_model($1,'bad-model',$2::jsonb,$3)`, uuid.NewString(), invalidProfile, invalidVersion); err == nil || !strings.Contains(err.Error(), "profile version must be a canonical uuid") {
			t.Fatal("invalid profile version accepted", invalidVersion, err)
		}
	}
	var canonical, digest []byte
	if err = pool.QueryRow(ctx, `SELECT s.canonical,s.schema_digest FROM model_capability_versions c JOIN meter_schemas s ON s.id=c.schema_id WHERE c.id=$1`, profileVersion).Scan(&canonical, &digest); err != nil {
		t.Fatal(err)
	}
	parsed, err := metering.ParseSchema(canonical)
	if err != nil {
		t.Fatalf("schema canonical rejected by metering: %v %s", err, canonical)
	}
	got, err := parsed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if string(got[:]) != string(digest) {
		t.Fatal("schema digest does not match metering")
	}
	var quantityCanonical, finalDigest []byte
	schemaID, err := parsed.ID()
	if err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT meter_side_quantity_canonical($1,'retail',10,4), meter_final_digest($2,'retail',meter_side_quantity_canonical($1,'retail',10,4))`, schemaID, digest).Scan(&quantityCanonical, &finalDigest); err != nil {
		t.Fatal(err)
	}
	quantities, err := metering.ManagementTokenQuantities(parsed, metering.Retail, 10, 4)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical, err := quantities.CanonicalJSON()
	if err != nil || string(wantCanonical) != string(quantityCanonical) {
		t.Fatalf("final canonical %s want %s (%v)", quantityCanonical, wantCanonical, err)
	}
	pure, err := quantities.Digest()
	if err != nil || bytes.Equal(finalDigest, pure[:]) || !bytes.Equal(finalDigest, inferenceFinalDigest(digest, []byte("retail"), wantCanonical)) {
		t.Fatal("final digest does not use the inference final domain")
	}
	var contract []byte
	var modelName, protocol string
	var inputMax, outputMax int64
	if err = pool.QueryRow(ctx, `SELECT contract_digest, profile->>'model', protocol, (profile->>'input_token_upper_bound')::bigint, (profile->>'output_limit')::bigint FROM model_capability_versions WHERE id=$1`, profileVersion).Scan(&contract, &modelName, &protocol, &inputMax, &outputMax); err != nil {
		t.Fatal(err)
	}
	hmac := strings.Repeat("ab", 32)
	var sqlPlan, sqlPlanDigest []byte
	if err = pool.QueryRow(ctx, `SELECT meter_request_plan_canonical($1,$2,$3,$4,20,false,$5), meter_plan_digest($4, meter_request_plan_canonical($1,$2,$3,$4,20,false,$5))`, profileVersion, model, schemaID, digest, hmac).Scan(&sqlPlan, &sqlPlanDigest); err != nil {
		t.Fatal(err)
	}
	var contractDigest [32]byte
	copy(contractDigest[:], contract)
	textProfile, err := inference.NewTextProfile(inference.TextProfileInput{Version: profileVersion, Model: modelName, VariantKey: "default", Protocol: inference.Protocol(protocol), Schema: parsed, ContractDigest: contractDigest, InputTokenUpperBound: inputMax, OutputLimit: outputMax})
	if err != nil {
		t.Fatal(err)
	}
	var hmacBytes [32]byte
	decoded, err := hex.DecodeString(hmac)
	if err != nil || copy(hmacBytes[:], decoded) != 32 {
		t.Fatal(err)
	}
	plan, err := inference.NewTextPlan(inference.TextPlanInput{Candidates: []inference.Profile{textProfile}, InputHMAC: hmacBytes, OutputLimit: 20, Stream: false})
	if err != nil {
		t.Fatal(err)
	}
	wantPlan, err := plan.CanonicalJSON()
	if err != nil || string(wantPlan) != string(sqlPlan) {
		t.Fatalf("request plan %s want %s (%v)", sqlPlan, wantPlan, err)
	}
	wantPlanDigest, err := plan.Digest()
	if err != nil || !bytes.Equal(sqlPlanDigest, wantPlanDigest[:]) {
		t.Fatal("request plan digest does not match inference")
	}
	attemptPlan, err := plan.ForAttempt(textProfile)
	if err != nil {
		t.Fatal(err)
	}
	var sqlAttempt, sqlAttemptDigest []byte
	if err = pool.QueryRow(ctx, `SELECT meter_attempt_plan_canonical($1,$2,$3,$4,20,false,$5), meter_plan_digest($4, meter_attempt_plan_canonical($1,$2,$3,$4,20,false,$5))`, profileVersion, model, schemaID, digest, hmac).Scan(&sqlAttempt, &sqlAttemptDigest); err != nil {
		t.Fatal(err)
	}
	wantAttempt, err := attemptPlan.CanonicalJSON()
	if err != nil || string(wantAttempt) != string(sqlAttempt) {
		t.Fatalf("attempt plan %s want %s (%v)", sqlAttempt, wantAttempt, err)
	}
	wantAttemptDigest, err := attemptPlan.Digest()
	if err != nil || !bytes.Equal(sqlAttemptDigest, wantAttemptDigest[:]) {
		t.Fatal("attempt plan digest does not match inference")
	}
	price := meterquote.PublishDefaultPrice(t, ctx, pool, meterquote.DefaultPrice{ModelID: model, Actor: user, InputRate: 2000000, OutputRate: 3000000})
	var rates []byte
	if err = pool.QueryRow(ctx, `SELECT rates_canonical FROM price_versions WHERE id=$1`, price).Scan(&rates); err != nil {
		t.Fatal(err)
	}
	stored, err := metering.ParseRates(parsed, metering.Retail, rates)
	if err != nil {
		t.Fatalf("rate canonical rejected: %v %s", err, rates)
	}
	want, err := metering.ManagementTokenRates(parsed, metering.Retail, 2000000, 3000000)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := stored.CanonicalJSON()
	right, _ := want.CanonicalJSON()
	if string(left) != string(right) {
		t.Fatalf("stored rates %s want %s", left, right)
	}
	bounds, err := metering.ManagementTokenBounds(parsed, metering.Retail, 100, 20)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := metering.ReservationAmount(bounds, stored)
	if err != nil || amount != 260 {
		t.Fatalf("reservation amount %d %v", amount, err)
	}
	retailBounds, err := plan.Bounds(metering.Retail)
	if err != nil {
		t.Fatal(err)
	}
	retailBoundsJSON, err := retailBounds.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	ratesJSON, err := stored.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var sqlBoundsDigest, sqlRatesDigest, sqlMeterQuoteDigest []byte
	if err = pool.QueryRow(ctx, `SELECT meter_bounds_digest($1,'retail',$2),meter_rates_digest($1,'retail',$3),meter_pure_quote_digest($1,'retail',$2,$3)`, digest, retailBoundsJSON, ratesJSON).Scan(&sqlBoundsDigest, &sqlRatesDigest, &sqlMeterQuoteDigest); err != nil {
		t.Fatal(err)
	}
	boundsDigest, err := retailBounds.Digest()
	if err != nil || !bytes.Equal(boundsDigest[:], sqlBoundsDigest) {
		t.Fatal("SQL bounds digest differs from metering", err)
	}
	ratesDigest, err := stored.Digest()
	if err != nil || !bytes.Equal(ratesDigest[:], sqlRatesDigest) {
		t.Fatal("SQL rates digest differs from metering", err)
	}
	meterQuoteDigest, err := metering.QuoteDigest(retailBounds, stored)
	if err != nil || !bytes.Equal(meterQuoteDigest[:], sqlMeterQuoteDigest) {
		t.Fatal("SQL pure quote digest differs from metering", err)
	}
	planDigest, err := plan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	quoteRaw, err := json.Marshal(quoteWire{Version: "1", Status: "known", Side: "retail", PlanDigest: hex.EncodeToString(planDigest[:]), SchemaID: schemaID, SchemaDigest: hex.EncodeToString(digest), Variant: "default", VersionID: &price, Bounds: retailBoundsJSON, Rates: ratesJSON})
	if err != nil {
		t.Fatal(err)
	}
	retailQuote, err := inference.ParseQuoteSnapshot(quoteRaw, plan, metering.Retail)
	if err != nil {
		t.Fatal(err)
	}
	var sqlQuote, sqlQuoteDigest []byte
	if err = pool.QueryRow(ctx, `SELECT meter_retail_quote_canonical($1,$2,$3,$4,100,20), meter_quote_digest($2, meter_retail_quote_canonical($1,$2,$3,$4,100,20))`, schemaID, digest, planDigest[:], price).Scan(&sqlQuote, &sqlQuoteDigest); err != nil {
		t.Fatal(err)
	}
	wantQuote, err := retailQuote.CanonicalJSON()
	if err != nil || string(wantQuote) != string(sqlQuote) {
		t.Fatalf("retail quote %s want %s (%v)", sqlQuote, wantQuote, err)
	}
	wantQuoteDigest, err := retailQuote.Digest()
	if err != nil || !bytes.Equal(sqlQuoteDigest, wantQuoteDigest[:]) {
		t.Fatal("retail quote digest does not match inference")
	}
	supplierBounds, err := attemptPlan.Bounds(metering.Supplier)
	if err != nil {
		t.Fatal(err)
	}
	supplierBoundsJSON, err := supplierBounds.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	missing := "version_missing"
	attemptDigest, err := attemptPlan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	unknownRaw, err := json.Marshal(quoteWire{Version: "1", Status: "unknown", Side: "supplier", PlanDigest: hex.EncodeToString(attemptDigest[:]), SchemaID: schemaID, SchemaDigest: hex.EncodeToString(digest), Variant: "default", UnknownReason: &missing, Bounds: supplierBoundsJSON})
	if err != nil {
		t.Fatal(err)
	}
	unknownQuote, err := inference.ParseQuoteSnapshot(unknownRaw, attemptPlan, metering.Supplier)
	if err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT meter_supplier_quote_canonical($1,$2,$3,'unknown','version_missing',NULL,NULL,100,20), meter_quote_digest($2, meter_supplier_quote_canonical($1,$2,$3,'unknown','version_missing',NULL,NULL,100,20))`, schemaID, digest, attemptDigest[:]).Scan(&sqlQuote, &sqlQuoteDigest); err != nil {
		t.Fatal(err)
	}
	wantQuote, err = unknownQuote.CanonicalJSON()
	if err != nil || string(wantQuote) != string(sqlQuote) {
		t.Fatalf("supplier quote %s want %s (%v)", sqlQuote, wantQuote, err)
	}
	wantQuoteDigest, err = unknownQuote.Digest()
	if err != nil || !bytes.Equal(sqlQuoteDigest, wantQuoteDigest[:]) {
		t.Fatal("supplier quote digest does not match inference")
	}
	var sqlAmount string
	if err = pool.QueryRow(ctx, `SELECT meter_token_amount(100,20,2,1,3,1)::text`).Scan(&sqlAmount); err != nil || sqlAmount != "260" {
		t.Fatalf("sql amount %s %v", sqlAmount, err)
	}
}

func TestMeterCompletenessRejectsGaps(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "meter_gap_" + uuid.NewString()[:8]
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	user, tenant, model, profile := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,email,password_hash) VALUES($1,$2,'unused')`, user, user+"@test.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,'gap')`, tenant); err != nil {
		t.Fatal(err)
	}
	body := `{"version":"` + profile + `","model":"gap-model","protocol":"openai_chat","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`
	if _, err = pool.Exec(ctx, `SELECT meter_insert_text_model($1,'gap-model',$2::jsonb,$3)`, model, body, profile); err != nil {
		t.Fatal(err)
	}
	var schemaID string
	if err = pool.QueryRow(ctx, `SELECT schema_id FROM model_capability_versions WHERE id=$1`, profile).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	channel, version, credential := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO channels(id,name,current_version) VALUES($1,'gap',$2)`, channel, version); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unused')`, credential, channel); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, version, channel, credential); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1000)`, version, channel, credential); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO cost_versions(id,channel_id,upstream_model,basis,schema_id,schema_digest,variant_key,currency,rates_canonical,rates_digest,created_by,pricing_operation_id)
 SELECT $1,$2,'upstream','contract',id,schema_digest,'default','CNY',canonical,schema_digest,$3,$4 FROM meter_schemas WHERE id=$5`, uuid.NewString(), channel, user, uuid.NewString(), schemaID); err == nil || !strings.Contains(err.Error(), "cost price operation mismatch") {
		t.Fatal("raw cost without operation was accepted", err)
	}
	_ = tx.Rollback(ctx)
	// Current-domain requests require a published route.  Birth the route and
	// its binding together so this fixture exercises the same 024 contract as
	// the management publisher.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	channel = uuid.NewString()
	version = uuid.NewString()
	credential = uuid.NewString()
	policy := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO channels(id,name,current_version) VALUES($1,'gap-current',$2)`, channel, version); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unused')`, credential, channel); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, version, channel, credential); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1000)`, version, channel, credential); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO route_policy_versions(id,model_id,revision,cost_mode,unknown_cost_policy) VALUES($1,$2,1,'manual','allow')`, policy, model); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key) VALUES($1,$2,$3,'upstream',0,1,10,'default')`, policy, channel, version); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE models SET active_revision=1 WHERE id=$1`, model); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// A binding may omit a supplier cap, but a published cap is one complete
	// two-dimensional vector.  A half-written cap must roll back the entire
	// route publication, including the active model pointer.
	partialRoute, partialBinding := uuid.NewString(), uuid.NewString()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO route_policy_versions(id,model_id,revision,cost_mode,unknown_cost_policy) VALUES($1,$2,2,'manual','allow')`, partialRoute, model); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO model_bindings(id,route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key) VALUES($1,$2,$3,$4,'upstream',0,1,10,'default')`, partialBinding, partialRoute, channel, version); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO binding_cost_rate_limits(binding_id,dimension,unit,numerator,denominator) VALUES($1,'text_input_token','token',2,1)`, partialBinding); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE models SET active_revision=2 WHERE id=$1`, model); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "route binding cap must contain exactly two text dimensions") {
		t.Fatal("partial route cap committed", err)
	}
	_ = tx.Rollback(ctx)
	var activeRevision int64
	if err = pool.QueryRow(ctx, `SELECT active_revision FROM models WHERE id=$1`, model).Scan(&activeRevision); err != nil || activeRevision != 1 {
		t.Fatalf("partial route changed active pointer: revision=%d err=%v", activeRevision, err)
	}
	var partialBindings int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM model_bindings WHERE id=$1`, partialBinding).Scan(&partialBindings); err != nil || partialBindings != 0 {
		t.Fatalf("partial route left binding=%d err=%v", partialBindings, err)
	}
	completeRoute, completeBinding := uuid.NewString(), uuid.NewString()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO route_policy_versions(id,model_id,revision,cost_mode,unknown_cost_policy) VALUES($1,$2,2,'manual','allow')`, completeRoute, model); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO model_bindings(id,route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key) VALUES($1,$2,$3,$4,'upstream',0,1,10,'default')`, completeBinding, completeRoute, channel, version); err != nil {
		t.Fatal(err)
	}
	for _, cap := range []struct {
		dimension string
		numerator int64
	}{
		{dimension: "text_input_token", numerator: 2},
		{dimension: "text_output_token", numerator: 3},
	} {
		if _, err = tx.Exec(ctx, `INSERT INTO binding_cost_rate_limits(binding_id,dimension,unit,numerator,denominator) VALUES($1,$2,'token',$3,1)`, completeBinding, cap.dimension, cap.numerator); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE models SET active_revision=2 WHERE id=$1`, model); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("complete route cap rejected: %v", err)
	}
	// Two different bindings cannot combine one cap each into a route-wide
	// vector.  Completeness is per binding, because caps constrain one upstream
	// candidate and must not leak across candidates.
	crossRoute, crossBindingA, crossBindingB := uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO route_policy_versions(id,model_id,revision,cost_mode,unknown_cost_policy) VALUES($1,$2,5,'manual','allow')`, crossRoute, model); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []struct {
		id, upstream, dimension string
		numerator               int64
	}{
		{id: crossBindingA, upstream: "upstream-a", dimension: "text_input_token", numerator: 2},
		{id: crossBindingB, upstream: "upstream-b", dimension: "text_output_token", numerator: 3},
	} {
		if _, err = tx.Exec(ctx, `INSERT INTO model_bindings(id,route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key) VALUES($1,$2,$3,$4,$5,0,1,10,'default')`, binding.id, crossRoute, channel, version, binding.upstream); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO binding_cost_rate_limits(binding_id,dimension,unit,numerator,denominator) VALUES($1,$2,'token',$3,1)`, binding.id, binding.dimension, binding.numerator); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE models SET active_revision=5 WHERE id=$1`, model); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "route binding cap must contain exactly two text dimensions") {
		t.Fatal("cross-binding partial caps committed", err)
	}
	_ = tx.Rollback(ctx)
	if err = pool.QueryRow(ctx, `SELECT active_revision FROM models WHERE id=$1`, model).Scan(&activeRevision); err != nil || activeRevision != 2 {
		t.Fatalf("cross-binding partial caps changed active pointer: revision=%d err=%v", activeRevision, err)
	}
	emptyRoute := uuid.NewString()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO route_policy_versions(id,model_id,revision,cost_mode,unknown_cost_policy) VALUES($1,$2,4,'manual','allow')`, emptyRoute, model); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "route requires at least one binding") {
		t.Fatal("empty route committed", err)
	}
	_ = tx.Rollback(ctx)
	wrongRoute, wrongChannel, wrongVersion, wrongCredential := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO channels(id,name,current_version) VALUES($1,'wrong-route',$2)`, []any{wrongChannel, wrongVersion}},
		{`INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unused')`, []any{wrongCredential, wrongChannel}},
		{`INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, []any{wrongVersion, wrongChannel, wrongCredential}},
		{`INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1000)`, []any{wrongVersion, wrongChannel, wrongCredential}},
		{`INSERT INTO route_policy_versions(id,model_id,revision,cost_mode,unknown_cost_policy) VALUES($1,$2,3,'manual','allow')`, []any{wrongRoute, model}},
		{`INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key) VALUES($1,$2,$3,'upstream',0,1,10,'default')`, []any{wrongRoute, channel, wrongVersion}},
	} {
		if _, err = tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "route binding supplier contract is incomplete") {
		t.Fatal("detached route binding committed", err)
	}
	_ = tx.Rollback(ctx)
	other, otherProfile, price, request, key := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	otherBody := `{"version":"` + otherProfile + `","model":"other-model","protocol":"openai_chat","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`
	if _, err = pool.Exec(ctx, `SELECT meter_insert_text_model($1,'other-model',$2::jsonb,$3)`, other, otherBody, otherProfile); err != nil {
		t.Fatal(err)
	}
	meterquote.PublishDefaultPrice(t, ctx, pool, meterquote.DefaultPrice{ID: price, ModelID: other, Actor: user, InputRate: 1000000, OutputRate: 1000000})
	if _, err = pool.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')`, tenant, user); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes) VALUES($1,$2,$3,'k','k',$4,$5)`, key, tenant, user, key, []string{model}); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,profile_version,protocol,execution_kind,result_kind,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision)
 VALUES($1,$2,$3,$4,$5,$6,'openai_chat','synchronous','json','r3','hmac',clock_timestamp()+interval '1 hour','test',false,1)`, request, tenant, key, user, model, profile); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO request_meter_snapshots(tenant_id,request_id,model_id,profile_version,schema_id,schema_digest,price_version_id,tenant_pricing_revision,retail_priced_at,input_bound,output_bound)
 SELECT $1,$2,$3,$4,c.schema_id,c.schema_digest,$5,1,clock_timestamp(),100,20 FROM model_capability_versions c WHERE c.id=$4`, tenant, request, model, profile, price); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO request_meter_bound_lines(tenant_id,request_id,side,dimension,unit,quantity)
 SELECT $1,$2,side,dimension,'token', CASE dimension WHEN 'text_input_token' THEN 100 ELSE 20 END
 FROM (VALUES ('retail'),('supplier')) AS s(side)
 CROSS JOIN (VALUES ('text_input_token'),('text_output_token')) AS d(dimension)`, tenant, request); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO reservations(id,tenant_id,request_id,amount,deadline) VALUES($1,$2,$3,100,clock_timestamp()+interval '1 day')`, uuid.NewString(), tenant, request); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "request price does not match model") {
		t.Fatal("mismatched request price committed", err)
	}
	_ = tx.Rollback(ctx)
	bare := uuid.NewString()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,profile_version,protocol,execution_kind,result_kind,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision)
 VALUES($1,$2,$3,$4,$5,$6,'openai_chat','synchronous','json','r3','hmac',clock_timestamp()+interval '1 hour','test',false,1)`, bare, tenant, key, user, model, profile); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO request_meter_snapshots(tenant_id,request_id,model_id,profile_version,schema_id,schema_digest,price_version_id,tenant_pricing_revision,retail_priced_at,input_bound,output_bound)
 SELECT $1,$2,$3,$4,c.schema_id,c.schema_digest,$5,1,clock_timestamp(),100,20 FROM model_capability_versions c WHERE c.id=$4`, tenant, bare, model, profile, price); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO reservations(id,tenant_id,request_id,amount,deadline) VALUES($1,$2,$3,100,clock_timestamp()+interval '1 day')`, uuid.NewString(), tenant, bare); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil || !strings.Contains(err.Error(), "request bounds are incomplete") {
		t.Fatal("request without bound lines committed", err)
	}
	_ = tx.Rollback(ctx)
	currentPrice := uuid.NewString()
	meterquote.PublishDefaultPrice(t, ctx, pool, meterquote.DefaultPrice{ID: currentPrice, ModelID: model, Actor: user, InputRate: 1000000, OutputRate: 1000000})
	validRequest := uuid.NewString()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,profile_version,protocol,execution_kind,result_kind,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision)
 VALUES($1,$2,$3,$4,$5,$6,'openai_chat','synchronous','json','r3','hmac',clock_timestamp()+interval '1 hour','test',false,1)`, validRequest, tenant, key, user, model, profile); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO request_meter_snapshots(tenant_id,request_id,model_id,profile_version,schema_id,schema_digest,price_version_id,tenant_pricing_revision,retail_priced_at,input_bound,output_bound)
 SELECT $1,$2,$3,$4,c.schema_id,c.schema_digest,$5,1,clock_timestamp(),100,20 FROM model_capability_versions c WHERE c.id=$4`, tenant, validRequest, model, profile, currentPrice); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO request_meter_bound_lines(tenant_id,request_id,side,dimension,unit,quantity)
 SELECT $1,$2,side,dimension,'token', CASE dimension WHEN 'text_input_token' THEN 100 ELSE 20 END
 FROM (VALUES ('retail'),('supplier')) AS s(side)
 CROSS JOIN (VALUES ('text_input_token'),('text_output_token')) AS d(dimension)`, tenant, validRequest); err != nil {
		t.Fatal(err)
	}
	// Do not commit this partial business fixture: 020/023 correctly require
	// the real reservation and wallet source chain. The immediate request
	// identity guard must reject before those deferred business guards run.
	if _, err = tx.Exec(ctx, `UPDATE request_records SET streaming=true WHERE tenant_id=$1 AND id=$2`, tenant, validRequest); err == nil || !strings.Contains(err.Error(), "immutable request identity") {
		t.Fatal("request quote identity update was accepted", err)
	}
	_ = tx.Rollback(ctx)
}

func TestUnifiedTextMeteringDoesNotRetainRouteRevisionBypass(t *testing.T) {
	sql, err := os.ReadFile("024_unified_text_metering.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(sql)
	if strings.Contains(text, "route_revision<>0") || strings.Contains(text, "route_revision != 0") {
		t.Fatal("024 retained a route_revision=0 compatibility bypass")
	}
	if !strings.Contains(text, "request_records_route_revision_check") || !strings.Contains(text, "CHECK(route_revision>0)") {
		t.Fatal("024 no longer enforces a positive request route revision")
	}
}

type quoteWire struct {
	Version       string          `json:"version"`
	Status        string          `json:"status"`
	Side          string          `json:"side"`
	PlanDigest    string          `json:"plan_digest"`
	SchemaID      string          `json:"schema_id"`
	SchemaDigest  string          `json:"schema_digest"`
	Variant       string          `json:"variant_key"`
	VersionID     *string         `json:"version_id"`
	UnknownReason *string         `json:"unknown_reason"`
	Bounds        json.RawMessage `json:"bounds"`
	Rates         json.RawMessage `json:"rates"`
}

func inferenceFinalDigest(frames ...[]byte) []byte {
	h := sha256.New()
	all := append([][]byte{[]byte("moni-aigate.inference.final.v1")}, frames...)
	for _, frame := range all {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(frame)))
		_, _ = h.Write(size[:])
		_, _ = h.Write(frame)
	}
	return h.Sum(nil)
}
