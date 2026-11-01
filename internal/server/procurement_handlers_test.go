package server

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/procurement"
)

func TestProcurementHTTPStrictDecoders(t *testing.T) {
	id := uuid.NewString()
	tests := []struct {
		name, valid, amountField string
		decode                   func(io.Reader) bool
	}{
		{"create", fmt.Sprintf(`{"operation_id":%q,"label":"supplier","provider":"aggregate","state":"active","limit_amount":"9007199254740993"}`, id), "limit_amount", func(r io.Reader) bool { return decodeProcurementCreate(r, new(procurement.CreateInput)) }},
		{"configuration", fmt.Sprintf(`{"operation_id":%q,"expected_revision":"9223372036854775807","label":"supplier","provider":"aggregate","state":"paused","limit_amount":"9007199254740993"}`, id), "limit_amount", func(r io.Reader) bool { return decodeProcurementConfiguration(r, new(procurement.ConfigurationInput)) }},
		{"binding", fmt.Sprintf(`{"operation_id":%q,"channel_id":%q,"credential_id":%q,"account_id":%q,"expected_channel_revision":"9007199254740993"}`, id, id, id, id), "expected_channel_revision", func(r io.Reader) bool { return decodeProcurementBinding(r, new(procurement.BindingInput)) }},
		{"observation", fmt.Sprintf(`{"operation_id":%q,"status":"known","amount":"9007199254740993","currency":"CNY","source":"manual_evidence","semantic":"reported_balance","observed_at":"2026-11-01T00:00:00Z","provider_as_of":null,"evidence_ref":"invoice:test"}`, id), "amount", func(r io.Reader) bool { return decodeProcurementObservation(r, new(procurement.ObservationInput)) }},
		{"proposal", fmt.Sprintf(`{"operation_id":%q,"tenant_id":%q,"attempt_id":%q,"amount":"9007199254740993","basis":"invoice","evidence_ref":"invoice:test","reason":"invoice verified"}`, id, id, id), "amount", func(r io.Reader) bool { return decodeProcurementProposal(r, new(procurementProposalBody)) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.decode(strings.NewReader(tc.valid)) {
				t.Fatal("canonical decimal lost precision or valid input rejected")
			}
			for _, raw := range []string{
				strings.Replace(tc.valid, `"operation_id":`, `"secret":"private-marker","operation_id":`, 1),
				strings.Replace(tc.valid, `"operation_id":`, `"Operation_ID":`, 1),
				strings.Replace(tc.valid, `"operation_id":`, `"operation_id":"`+id+`","operation_id":`, 1),
				strings.Replace(tc.valid, id, strings.ToUpper(id), 1),
				strings.Replace(tc.valid, id, uuid.Nil.String(), 1),
				tc.valid + `{}`, `null`, `[]`, strings.Repeat(" ", 16385) + tc.valid,
			} {
				if tc.decode(strings.NewReader(raw)) {
					t.Fatal("ambiguous JSON accepted")
				}
			}
			for _, value := range []string{`0`, `-1`, `"-1"`, `"+1"`, `"01"`, `"1.0"`, `" 1"`, `"9223372036854775808"`} {
				needle := `"` + tc.amountField + `":"9007199254740993"`
				raw := strings.Replace(tc.valid, needle, `"`+tc.amountField+`":`+value, 1)
				if tc.decode(strings.NewReader(raw)) {
					t.Fatal("noncanonical monetary amount or revision accepted", value)
				}
			}
		})
	}
	if !procurementDecimal("0", false) || !procurementDecimal("9223372036854775807", false) || procurementDecimal("0", true) {
		t.Fatal("zero and int64 boundary semantics changed")
	}
}

func TestProcurementHTTPInvalidCursorRejectedBeforeDatabase(t *testing.T) {
	s := &Server{}
	id := uuid.NewString()
	encode := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
	for _, raw := range []string{"", "invalid", encode(`{"t":"2026-11-01T00:00:00Z","id":"not-an-id"}`), encode(`{"t":"2026-11-01T00:00:00Z","id":"` + id + `","id":"` + id + `"}`), encode(`{"t":"2026-11-01T00:00:00Z","id":"` + id + `","extra":true}`), strings.Repeat("a", 513)} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("GET", "/api/platform/procurement-accounts?cursor="+raw, nil)
		s.procurementPage(c, "must not access database")
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid_cursor") {
			t.Fatal("invalid cursor reached storage", rec.Code)
		}
	}
}

func TestProcurementHTTPBillingErrorsAreSafeAndActionable(t *testing.T) {
	for code, status := range map[string]int{
		"procurement_paused": 503, "procurement_budget_exceeded": 503, "procurement_binding_changed": 503,
		"procurement_bound_invalid": 503, "procurement_invariant": 503, "procurement_not_found": 404,
		"procurement_inflight": 409, "procurement_approval_required": 409, "supplier_refund_evidence_required": 409,
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/api/platform/procurement-cost-proposals", nil)
		if !billingError(c, &Server{}, &billing.Error{Code: code, Message: "private-provider-marker"}) || rec.Code != status || strings.Contains(rec.Body.String(), "private-provider-marker") {
			t.Fatal("unsafe procurement error mapping", code, rec.Code, rec.Body.String())
		}
	}
}
