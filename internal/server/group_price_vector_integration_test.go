package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func TestGroupPriceVectorHTTPApplication(t *testing.T) {
	s, pool := newAuthFixture(t)
	admin, _ := authUser(t, s, pool, "admin")
	finance, _ := authUser(t, s, pool, "finance_operator")
	raw, csrf := authSession(t, s, pool, admin, "", true, true)
	fin, fc := authSession(t, s, pool, finance, "", true, true)
	model, err := s.Catalog.CreateModel(context.Background(), admin, catalog.ModelInput{PublicName: "group-vector-http", ContextLimit: 100, InputUpperBound: 80, OutputLimit: 20, DefaultOutputLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.Catalog.CreatePriceGroup(context.Background(), catalog.PricingActor{UserID: admin}, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "group-vector-http"})
	if err != nil {
		t.Fatal(err)
	}
	c := meterquote.BindGroup(t, context.Background(), pool, catalog.GroupPriceCommand{ModelID: model})
	f := &gatewayFixture{pool: pool, server: s}
	procurementMeterApplication(t, f)
	forbidScalarQuoteReads(t, f)
	s = f.server
	path := "/api/platform/price-groups/" + g.Result.ID + "/price-versions"
	body := map[string]any{"operation_id": uuid.NewString(), "model_id": model, "schema_id": c.SchemaID, "schema_digest": c.SchemaDigest, "variant_key": "default", "input_rate": "250000", "output_rate": "0", "effective_at": time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")}
	for _, patch := range []map[string]any{{"schema_id": nil}, {"schema_digest": strings.Repeat("AB", 32)}, {"variant_key": ""}, {"input_rate": 250000}, {"output_rate": "01"}, {"effective_at": "2026-11-01T00:00:00.0000001Z"}} {
		bad := map[string]any{}
		for k, v := range body {
			bad[k] = v
		}
		for k, v := range patch {
			bad[k] = v
		}
		s.pricing = pricingAdmission{}
		requireAuthStatus(t, authHTTP(t, s, "POST", path, raw, csrf, s.Config.Origin, bad), 400)
	}
	s.pricing = pricingAdmission{}
	response := authHTTP(t, s, "POST", path, raw, csrf, s.Config.Origin, body)
	requireAuthStatus(t, response, 201)
	var first struct {
		Data catalog.PricingOperationReceipt `json:"data"`
	}
	if json.Unmarshal(response.Body.Bytes(), &first) != nil {
		t.Fatal(response.Body.String())
	}
	for _, field := range []string{`"input_rate"`, `"output_rate"`} {
		if strings.Contains(response.Body.String(), field) {
			t.Fatal("scalar result leaked", response.Body.String())
		}
	}
	result, ok := first.Data.Result.(map[string]any)
	if !ok || len(result) != 10 || result["schema_id"] != c.SchemaID || result["rates"] == nil {
		t.Fatal(first)
	}
	// Neither HTTP replay nor finance reads need the current model/group state.
	authExec(t, pool, `UPDATE models SET enabled=false WHERE id=$1`, model)
	_, err = s.Catalog.UpdatePriceGroup(context.Background(), catalog.PricingActor{UserID: admin}, catalog.GroupUpdateCommand{OperationID: uuid.NewString(), GroupID: g.Result.ID, ExpectedRevision: 1, Label: "retired", State: "retired"})
	if err != nil {
		t.Fatal(err)
	}
	s.pricing = pricingAdmission{}
	response = authHTTP(t, s, "POST", path, raw, csrf, s.Config.Origin, body)
	requireAuthStatus(t, response, 200)
	var second struct {
		Data catalog.PricingOperationReceipt `json:"data"`
	}
	if json.Unmarshal(response.Body.Bytes(), &second) != nil || !reflect.DeepEqual(first.Data, second.Data) {
		t.Fatal("not original HTTP receipt", response.Body.String())
	}
	for _, read := range []string{path, "/api/platform/pricing-operations/" + body["operation_id"].(string)} {
		s.pricing = pricingAdmission{}
		response = authHTTP(t, s, "GET", read, fin, fc, "", nil)
		requireAuthStatus(t, response, 200)
		if !strings.Contains(response.Body.String(), `"rates"`) || strings.Contains(response.Body.String(), `"input_rate"`) || strings.Contains(response.Body.String(), `"output_rate"`) {
			t.Fatal(response.Body.String())
		}
	}
	body["input_rate"] = "250001"
	s.pricing = pricingAdmission{}
	requireAuthStatus(t, authHTTP(t, s, "POST", path, raw, csrf, s.Config.Origin, body), 409)
}
