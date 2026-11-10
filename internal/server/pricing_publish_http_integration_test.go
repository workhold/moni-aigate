package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

const pricingPublishTime = "2026-11-06T00:00:00.123456Z"

type pricingHTTPEnvelope struct {
	Data json.RawMessage `json:"data"`
}

func restrictedPricingFixture(t *testing.T) (*gatewayFixture, string, string, string, string, string) {
	t.Helper()
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	raw, csrf := probeAdmin(t, f)
	admin := pricingAdminID(t, f)
	schema, digest := pricingSchemaIdentity(t, f.pool, f.model)
	channel := probeChannels(t, f)[0]
	var revision string
	if err := f.pool.QueryRow(context.Background(), `SELECT management_revision::text FROM channels WHERE id=$1`, channel).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	procurementMeterApplication(t, f)
	forbidScalarQuoteReads(t, f)
	return f, admin, raw, csrf, schema, digest + ":" + channel + ":" + revision
}

func pricingAdminID(t *testing.T, f *gatewayFixture) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='admin' ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func pricingSchemaIdentity(t *testing.T, pool *pgxpool.Pool, model string) (string, string) {
	t.Helper()
	var schema, digest string
	if err := pool.QueryRow(context.Background(), `SELECT c.schema_id,encode(c.schema_digest,'hex') FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id WHERE m.id=$1`, model).Scan(&schema, &digest); err != nil {
		t.Fatal(err)
	}
	return schema, digest
}

func pricingModel(t *testing.T, f *gatewayFixture, actor, name string, profile provider.Profile) string {
	t.Helper()
	model, err := f.server.Catalog.CreateModel(context.Background(), actor, catalog.ModelInput{PublicName: name, Profile: &profile})
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func pricingHTTP(t *testing.T, s *Server, method, path, raw, csrf string, body any) *httptest.ResponseRecorder {
	t.Helper()
	resetPricingAdmissionForTest(s)
	return authHTTP(t, s, method, path, raw, csrf, s.Config.Origin, body)
}

func pricingHTTPRaw(t *testing.T, s *Server, method, path, raw, csrf, body string) *httptest.ResponseRecorder {
	t.Helper()
	resetPricingAdmissionForTest(s)
	return keyPolicyHTTP(t, s.Engine(), method, path, body, raw, csrf, s.Config.Origin, "", nil)
}

func resetPricingAdmissionForTest(s *Server) {
	s.pricing.mu.Lock()
	s.pricing.buckets = nil
	s.pricing.gates = nil
	s.pricing.mu.Unlock()
}

func pricingCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	return envelope.Error.Code
}

func requirePricingError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status || pricingCode(t, response) != code {
		t.Fatalf("want %d/%s, got %d/%s %s", status, code, response.Code, pricingCode(t, response), response.Body.String())
	}
}

func pricingData(t *testing.T, response *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var envelope pricingHTTPEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	return envelope.Data
}

func sameJSON(t *testing.T, left, right json.RawMessage) bool {
	t.Helper()
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		t.Fatal(string(left), string(right))
	}
	return reflect.DeepEqual(a, b)
}

func defaultPriceBody(operation, model, schema, digest, input, output, effective string) map[string]any {
	return map[string]any{
		"operation_id": operation, "model_id": model, "schema_id": schema,
		"schema_digest": digest, "variant_key": "default", "input_rate": input,
		"output_rate": output, "effective_at": effective,
	}
}

func costPriceBody(operation, channel, upstream, schema, digest, basis string, input, output any, effective, revision string) map[string]any {
	return map[string]any{
		"operation_id": operation, "channel_id": channel, "upstream_model": upstream,
		"schema_id": schema, "schema_digest": digest, "variant_key": "default",
		"basis": basis, "input_rate": input, "output_rate": output,
		"effective_at": effective, "expected_channel_revision": revision,
	}
}

func TestRestrictedAPPDefaultPricePublishHTTP(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	raw, csrf := probeAdmin(t, f)
	admin := pricingAdminID(t, f)
	schema, digest := pricingSchemaIdentity(t, f.pool, f.model)
	procurementMeterApplication(t, f)
	forbidScalarQuoteReads(t, f)

	at := pricingPublishTime
	op := uuid.NewString()
	body := defaultPriceBody(op, f.model, schema, digest, "9007199254740993", "0", at)
	first := pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, body)
	if first.Code != 201 || !strings.Contains(first.Body.String(), `"kind":"default_price.publish"`) || !strings.Contains(first.Body.String(), `"price_group_id":null`) || !strings.Contains(first.Body.String(), `"rates"`) {
		t.Fatal(first.Code, first.Body.String())
	}
	firstData := pricingData(t, first)

	replay := pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, body)
	if replay.Code != 200 || !sameJSON(t, firstData, pricingData(t, replay)) {
		t.Fatal("default publication did not return its original receipt", replay.Code, replay.Body.String())
	}
	get := pricingHTTP(t, f.server, "GET", "/api/platform/pricing-operations/"+op, raw, csrf, nil)
	if get.Code != 200 || !sameJSON(t, firstData, pricingData(t, get)) {
		t.Fatal("default operation GET changed the original receipt", get.Code, get.Body.String())
	}

	mismatch := defaultPriceBody(op, f.model, schema, digest, "9007199254740992", "0", at)
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, mismatch), 409, "policy_operation_conflict")
	conflict := defaultPriceBody(uuid.NewString(), f.model, schema, digest, "1", "0", at)
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, conflict), 409, "price_effective_conflict")

	invalid := defaultPriceBody(uuid.NewString(), f.model, schema, strings.Repeat("0", 64), "1", "0", at)
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, invalid), 400, "invalid_price")
	invalid = defaultPriceBody(uuid.NewString(), f.model, schema, digest, "1", "0", "2026-11-06T00:00:00.1234567Z")
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, invalid), 400, "invalid_request")
	invalid = defaultPriceBody(uuid.NewString(), f.model, schema, digest, "1", "0", at)
	invalid["variant_key"] = "other"
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, invalid), 400, "invalid_request")
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, defaultPriceBody(uuid.NewString(), uuid.NewString(), schema, digest, "1", "0", at)), 404, "not_found")
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/price-versions?x=1", raw, csrf, body), 400, "invalid_request")

	inputOnly := pricingModel(t, f, admin, "app-http-input-only", provider.Profile{Protocol: provider.ProtocolEmbeddings, InputTokenUpperBound: 100, ContextLimit: 100, OutputLimit: 0, DefaultOutputLimit: 0})
	inputSchema, inputDigest := pricingSchemaIdentity(t, f.pool, inputOnly)
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, defaultPriceBody(uuid.NewString(), inputOnly, inputSchema, inputDigest, "1", "1", "2026-11-06T01:00:00.123456Z")), 400, "invalid_price")
	large := pricingModel(t, f, admin, "app-http-large", provider.Profile{Protocol: provider.ProtocolChat, InputTokenUpperBound: 2000000, ContextLimit: 4000000, OutputLimit: 2000000, DefaultOutputLimit: 2000000})
	largeSchema, largeDigest := pricingSchemaIdentity(t, f.pool, large)
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, defaultPriceBody(uuid.NewString(), large, largeSchema, largeDigest, "9223372036854775807", "9223372036854775807", "2026-11-06T02:00:00.123456Z")), 400, "invalid_price")

	rollbackOp := uuid.NewString()
	authExec(t, f.pool, `CREATE FUNCTION http_default_price_audit_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='pricing.price.publish' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER http_default_price_audit_fail BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION http_default_price_audit_fail()`)
	rollbackResponse := pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, defaultPriceBody(rollbackOp, f.model, schema, digest, "2", "0", "2026-11-06T03:00:00.123456Z"))
	if rollbackResponse.Code != 503 {
		t.Fatal("audit failure did not surface as an uncertain write", rollbackResponse.Code, rollbackResponse.Body.String())
	}
	authExec(t, f.pool, `DROP TRIGGER http_default_price_audit_fail ON audit_events`)
	var operationRows, priceRows, auditRows int
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM pricing_operations WHERE operation_id=$1),(SELECT count(*) FROM price_versions WHERE pricing_operation_id=$1),(SELECT count(*) FROM audit_events WHERE sanitized_diff->>'operation_id'=$1)`, rollbackOp).Scan(&operationRows, &priceRows, &auditRows); err != nil || operationRows != 0 || priceRows != 0 || auditRows != 0 {
		t.Fatal("default audit failure leaked rows", operationRows, priceRows, auditRows, err)
	}

	concurrentOp := uuid.NewString()
	concurrentBody := defaultPriceBody(concurrentOp, f.model, schema, digest, "3", "0", "2026-11-06T04:00:00.123456Z")
	responses := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, concurrentBody).Code
		}()
	}
	wg.Wait()
	close(responses)
	counts := map[int]int{}
	for status := range responses {
		counts[status]++
	}
	if counts[201] != 1 || counts[200]+counts[429] != 1 {
		t.Fatal("unexpected concurrent HTTP publication statuses", counts)
	}
	if retry := pricingHTTP(t, f.server, "POST", "/api/platform/price-versions", raw, csrf, concurrentBody); retry.Code != 200 {
		t.Fatal("concurrent publication did not leave a replayable receipt", retry.Code, retry.Body.String())
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM price_versions WHERE pricing_operation_id=$1),(SELECT count(*) FROM audit_events WHERE sanitized_diff->>'operation_id'=$1)`, concurrentOp).Scan(&priceRows, &auditRows); err != nil || priceRows != 1 || auditRows != 1 {
		t.Fatal("concurrent default publication was not unique", priceRows, auditRows, err)
	}
}

func TestRestrictedAPPCostPricePublishHTTP(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	raw, csrf := probeAdmin(t, f)
	admin := pricingAdminID(t, f)
	schema, digest := pricingSchemaIdentity(t, f.pool, f.model)
	channel := probeChannels(t, f)[0]
	var revision string
	if err := f.pool.QueryRow(context.Background(), `SELECT management_revision::text FROM channels WHERE id=$1`, channel).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	procurementMeterApplication(t, f)
	forbidScalarQuoteReads(t, f)

	knownOp := uuid.NewString()
	known := costPriceBody(knownOp, channel, "provider-model", schema, digest, "contract", "9007199254740993", "0", pricingPublishTime, revision)
	first := pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, known)
	if first.Code != 201 || !strings.Contains(first.Body.String(), `"kind":"cost_price.publish"`) || !strings.Contains(first.Body.String(), `"channel_revision":"`) || !strings.Contains(first.Body.String(), `"rates"`) {
		t.Fatal(first.Code, first.Body.String())
	}
	firstData := pricingData(t, first)
	replay := pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, known)
	if replay.Code != 200 || !sameJSON(t, firstData, pricingData(t, replay)) {
		t.Fatal("cost publication did not return its original receipt", replay.Code, replay.Body.String())
	}
	get := pricingHTTP(t, f.server, "GET", "/api/platform/pricing-operations/"+knownOp, raw, csrf, nil)
	if get.Code != 200 || !sameJSON(t, firstData, pricingData(t, get)) {
		t.Fatal("cost operation GET changed the original receipt", get.Code, get.Body.String())
	}

	mismatch := costPriceBody(knownOp, channel, "provider-model", schema, digest, "contract", "1", "0", pricingPublishTime, revision)
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, mismatch), 409, "policy_operation_conflict")
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, costPriceBody(uuid.NewString(), channel, "provider-model", schema, digest, "contract", "1", "0", pricingPublishTime, "2")), 409, "price_effective_conflict")
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, costPriceBody(uuid.NewString(), channel, "provider-model", schema, digest, "contract", "1", "0", "2026-11-06T00:01:00.123456Z", revision)), 409, "pricing_revision_conflict")

	secondOp := uuid.NewString()
	second := costPriceBody(secondOp, channel, "provider-model", schema, digest, "invoice", "1", "0", "2026-11-06T00:01:00.123456Z", "2")
	if response := pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, second); response.Code != 201 {
		t.Fatal("CAS-forward cost publication failed", response.Code, response.Body.String())
	}
	unknownOp := uuid.NewString()
	unknown := costPriceBody(unknownOp, channel, "provider-model", schema, digest, "unknown", nil, nil, "2026-11-06T00:02:00.123456Z", "3")
	unknownResponse := pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, unknown)
	if unknownResponse.Code != 201 || !strings.Contains(unknownResponse.Body.String(), `"rates":null`) {
		t.Fatal("unknown cost publication did not preserve null rates", unknownResponse.Code, unknownResponse.Body.String())
	}

	invalid := costPriceBody(uuid.NewString(), channel, "provider-model", schema, strings.Repeat("0", 64), "contract", "1", "0", "2026-11-06T00:03:00.123456Z", "4")
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, invalid), 400, "invalid_price")
	invalid = costPriceBody(uuid.NewString(), channel, "provider-model", schema, digest, "unknown", "0", nil, "2026-11-06T00:03:01.123456Z", "4")
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, invalid), 400, "invalid_request")
	invalid = costPriceBody(uuid.NewString(), channel, "provider-model", schema, digest, "contract", nil, "0", "2026-11-06T00:03:02.123456Z", "4")
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, invalid), 400, "invalid_request")
	invalid = costPriceBody(uuid.NewString(), channel, "provider-model", schema, digest, "contract", "1", "0", "2026-11-06T00:03:03.123456Z", "4")
	invalid["variant_key"] = "other"
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, invalid), 400, "invalid_request")
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, costPriceBody(uuid.NewString(), uuid.NewString(), "provider-model", schema, digest, "contract", "1", "0", "2026-11-06T00:03:04.123456Z", "1")), 404, "not_found")

	inputOnly := pricingModel(t, f, admin, "app-http-cost-input-only", provider.Profile{Protocol: provider.ProtocolEmbeddings, InputTokenUpperBound: 100, ContextLimit: 100, OutputLimit: 0, DefaultOutputLimit: 0})
	inputSchema, inputDigest := pricingSchemaIdentity(t, f.pool, inputOnly)
	inputOnlyBody := costPriceBody(uuid.NewString(), channel, "provider-model", inputSchema, inputDigest, "contract", "1", "1", "2026-11-06T00:03:05.123456Z", "4")
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, inputOnlyBody), 400, "invalid_price")
	large := pricingModel(t, f, admin, "app-http-cost-large", provider.Profile{Protocol: provider.ProtocolChat, InputTokenUpperBound: 2000000, ContextLimit: 4000000, OutputLimit: 2000000, DefaultOutputLimit: 2000000})
	largeSchema, largeDigest := pricingSchemaIdentity(t, f.pool, large)
	largeBody := costPriceBody(uuid.NewString(), channel, "provider-model", largeSchema, largeDigest, "contract", "9223372036854775807", "9223372036854775807", "2026-11-06T00:03:06.123456Z", "4")
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, largeBody), 400, "invalid_price")

	rollbackOp := uuid.NewString()
	var beforeRevision string
	if err := f.pool.QueryRow(context.Background(), `SELECT management_revision::text FROM channels WHERE id=$1`, channel).Scan(&beforeRevision); err != nil {
		t.Fatal(err)
	}
	authExec(t, f.pool, `CREATE FUNCTION http_cost_price_audit_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='pricing.cost.publish' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER http_cost_price_audit_fail BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION http_cost_price_audit_fail()`)
	rollbackResponse := pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, costPriceBody(rollbackOp, channel, "provider-model", schema, digest, "contract", "2", "0", "2026-11-06T00:03:07.123456Z", beforeRevision))
	if rollbackResponse.Code != 503 {
		t.Fatal("audit failure did not surface as an uncertain cost write", rollbackResponse.Code, rollbackResponse.Body.String())
	}
	authExec(t, f.pool, `DROP TRIGGER http_cost_price_audit_fail ON audit_events`)
	var operationRows, costRows, auditRows int
	var afterRevision string
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM pricing_operations WHERE operation_id=$1),(SELECT count(*) FROM cost_versions WHERE pricing_operation_id=$1),(SELECT count(*) FROM audit_events WHERE sanitized_diff->>'operation_id'=$1),(SELECT management_revision::text FROM channels WHERE id=$2)`, rollbackOp, channel).Scan(&operationRows, &costRows, &auditRows, &afterRevision); err != nil || operationRows != 0 || costRows != 0 || auditRows != 0 || afterRevision != beforeRevision {
		t.Fatal("cost audit failure leaked rows or revision", operationRows, costRows, auditRows, beforeRevision, afterRevision, err)
	}

	tx, err := f.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `SET LOCAL session_replication_role='replica'`); err == nil {
		_, err = tx.Exec(context.Background(), `UPDATE channels SET management_revision=$2 WHERE id=$1`, channel, math.MaxInt64)
	}
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	requirePricingError(t, pricingHTTP(t, f.server, "POST", "/api/platform/cost-versions", raw, csrf, costPriceBody(uuid.NewString(), channel, "provider-model", schema, digest, "contract", "1", "0", "2026-11-06T00:04:00.123456Z", "9223372036854775807")), 409, "pricing_revision_exhausted")
}
