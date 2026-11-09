package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestCustomerRequestDetailExposesTaskReferenceFacts(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	response := f.request(t, context.Background(), false, false)
	requestID := response.Header.Get("X-Request-ID")
	if response.StatusCode != http.StatusOK || requestID == "" {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("text request failed: status=%d id=%q body=%s", response.StatusCode, requestID, body)
	}
	_ = response.Body.Close()

	var owner string
	if err := f.pool.QueryRow(context.Background(), "SELECT owner_id FROM api_keys WHERE tenant_id=$1 AND id=$2", f.tenant, f.key).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	session, csrf := authSession(t, f.server, f.pool, owner, f.tenant, false, true)
	detail := authHTTP(t, f.server, http.MethodGet, "/api/requests/"+requestID, session, csrf, f.server.Config.Origin, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("request detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var row map[string]any
	if err := json.Unmarshal(authData(t, detail), &row); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"profile_version", "reservation_id", "attempt_id"} {
		value, ok := row[field].(string)
		if !ok || value == "" {
			t.Fatalf("request detail omitted task reference %s: %v", field, row)
		}
	}
	funding := authHTTP(t, f.server, http.MethodGet, "/api/requests/"+requestID+"/funding", session, csrf, f.server.Config.Origin, nil)
	if funding.Code != http.StatusOK {
		t.Fatalf("request funding status=%d body=%s", funding.Code, funding.Body.String())
	}
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(funding.Body.Bytes(), &envelope); err != nil || len(envelope.Data) == 0 {
		t.Fatalf("request funding omitted source facts: %s", funding.Body.String())
	}
	if sourceID, ok := envelope.Data[0]["lot_id"].(string); !ok || sourceID == "" {
		t.Fatalf("request funding omitted source lot identity: %v", envelope.Data[0])
	}
}
