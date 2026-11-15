package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func TestPricingHTTPBusinessAndCurrentDomainIsolation(t *testing.T) {
	s, pool := newAuthFixture(t)
	admin, _ := authUser(t, s, pool, "admin")
	finance, _ := authUser(t, s, pool, "finance_operator")
	owner, _ := authUser(t, s, pool, "none")
	tenant := uuid.NewString()
	authExec(t, pool, `INSERT INTO tenants(id,name) VALUES($1,'pricing')`, tenant)
	authExec(t, pool, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, tenant, owner)
	raw, csrf := authSession(t, s, pool, admin, "", true, true)
	fin, fc := authSession(t, s, pool, finance, "", true, true)
	customer, cc := authSession(t, s, pool, owner, tenant, true, true)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		s.pricing = pricingAdmission{}
		return authHTTP(t, s, method, path, raw, csrf, s.Config.Origin, body)
	}
	requirePriceTimes := func(w *httptest.ResponseRecorder) {
		var page struct {
			Data []struct {
				EffectiveAt string `json:"effective_at"`
				CreatedAt   string `json:"created_at"`
			} `json:"data"`
		}
		if json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Data) == 0 {
			t.Fatal("price list is empty or invalid", w.Body.String())
		}
		for _, row := range page.Data {
			for _, value := range []string{row.EffectiveAt, row.CreatedAt} {
				if len(value) != 27 {
					t.Fatalf("price timestamp does not use six microseconds: %q", value)
				}
				if _, err := time.Parse("2006-01-02T15:04:05.000000Z", value); err != nil {
					t.Fatalf("invalid price timestamp %q: %v", value, err)
				}
			}
		}
	}
	var receipt catalog.PricingOperationReceipt
	decode := func(w *httptest.ResponseRecorder) map[string]any {
		var v map[string]any
		if json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Body.String())
		}
		return v
	}
	created := call("POST", "/api/platform/price-groups", map[string]any{"operation_id": uuid.NewString(), "label": "commercial"})
	requireAuthStatus(t, created, 201)
	if json.Unmarshal(created.Body.Bytes(), &struct {
		Data *catalog.PricingOperationReceipt `json:"data"`
	}{Data: &receipt}) != nil {
		t.Fatal(created.Body.String())
	}
	group := receipt.Operation.ResourceID
	model, e := s.Catalog.CreateModel(context.Background(), admin, catalog.ModelInput{PublicName: "pricing-model", ContextLimit: 120, InputUpperBound: 100, OutputLimit: 20, DefaultOutputLimit: 20})
	if e != nil {
		t.Fatal(e)
	}
	authExec(t, pool, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, tenant, model)
	var schema, digest string
	if e = pool.QueryRow(context.Background(), `SELECT c.schema_id,encode(c.schema_digest,'hex') FROM models m JOIN model_capability_versions c ON c.id=m.profile_version WHERE m.id=$1`, model).Scan(&schema, &digest); e != nil {
		t.Fatal(e)
	}
	millisecond := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
	requireAuthStatus(t, call("POST", "/api/platform/price-versions", map[string]any{"operation_id": uuid.NewString(), "model_id": model, "schema_id": schema, "schema_digest": digest, "variant_key": "default", "input_rate": "900", "output_rate": "900", "effective_at": millisecond}), 201)
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")
	requireAuthStatus(t, call("POST", "/api/platform/price-groups/"+group+"/price-versions", map[string]any{"operation_id": uuid.NewString(), "model_id": model, "schema_id": schema, "schema_digest": digest, "variant_key": "default", "input_rate": "123", "output_rate": "456", "effective_at": now}), 201)
	requireAuthStatus(t, call("PUT", "/api/platform/tenants/"+tenant+"/pricing", map[string]any{"operation_id": uuid.NewString(), "expected_revision": "1", "price_group_id": group}), 200)
	for _, path := range []string{"/api/models", "/api/prices", "/api/models/" + model + "/retail-price"} {
		w := authHTTP(t, s, "GET", path, customer, cc, "", nil)
		requireAuthStatus(t, w, 200)
		if path == "/api/prices" {
			requirePriceTimes(w)
		}
		if !strings.Contains(w.Body.String(), `"123"`) || strings.Contains(w.Body.String(), `"900"`) {
			t.Fatal(path, w.Body.String())
		}
	}
	adminPrice := call("GET", "/api/prices", nil)
	requirePriceTimes(adminPrice)
	if !strings.Contains(adminPrice.Body.String(), `"numerator":"9"`) || !strings.Contains(adminPrice.Body.String(), `"denominator":"10000"`) || strings.Contains(adminPrice.Body.String(), `"numerator":"123"`) {
		t.Fatal(adminPrice.Body.String())
	}
	preview := authHTTP(t, s, "GET", "/api/platform/tenants/"+tenant+"/pricing-preview?price_group_id=default", fin, fc, "", nil)
	requireAuthStatus(t, preview, 200)
	if decode(preview)["next_cursor"] != "" {
		t.Fatal(preview.Body.String())
	}
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/platform/price-groups", customer, cc, "", nil), 403)
	authExec(t, pool, `UPDATE memberships SET role='member' WHERE tenant_id=$1 AND user_id=$2`, tenant, owner)
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/tenant/pricing", customer, cc, "", nil), 403)
	authExec(t, pool, `UPDATE users SET platform_role='none' WHERE id=$1`, finance)
	requireAuthStatus(t, authHTTP(t, s, "GET", "/api/platform/price-groups/"+group, fin, fc, "", nil), 403)
	for _, q := range []string{"?limit=50", "?cursor=x&cursor=y", "?cursor=%FF"} {
		requireAuthStatus(t, call("GET", "/api/platform/price-groups"+q, nil), 400)
	}
}
