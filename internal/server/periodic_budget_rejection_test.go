package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

type budgetRejectionTestError struct {
	private   string
	rejection billing.BudgetRejection
}

func (e *budgetRejectionTestError) Error() string { return e.private }
func (e *budgetRejectionTestError) As(target any) bool {
	if out, ok := target.(**billing.Error); ok {
		*out = &billing.Error{Code: "periodic_budget_exceeded", Message: e.private}
		return true
	}
	return false
}
func (e *budgetRejectionTestError) BudgetRejection() (billing.BudgetRejection, bool) {
	return e.rejection, true
}

func safeBudgetRejection() billing.BudgetRejection {
	return billing.BudgetRejection{Scope: "tenant", Reason: "limit_exceeded", WindowUsed: "120", LimitAmount: "119", WindowResetAt: "2026-11-21T00:00:00.000000Z"}
}

func TestPeriodicBudgetInferenceErrorSafeFieldsAllProtocols(t *testing.T) {
	private := "private-body-sql-secret-marker"
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1beta/models/model:generateContent"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(private))
			c.Set("request_id", "public-request-id")
			err := &budgetRejectionTestError{private: private, rejection: safeBudgetRejection()}
			if !billingError(c, &Server{}, err) || rec.Code != http.StatusPaymentRequired {
				t.Fatalf("budget rejection was not mapped to HTTP 402: %d", rec.Code)
			}
			body := rec.Body.String()
			if strings.Contains(body, private) || strings.Contains(body, "periodic_budget_exceeded") {
				t.Fatalf("private or internal error leaked: %s", body)
			}
			var envelope struct {
				Error map[string]any `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			for field, want := range map[string]string{
				"code": "budget_exceeded", "scope": "tenant", "reason": "limit_exceeded",
				"window_used": "120", "limit_amount": "119", "window_reset_at": "2026-11-21T00:00:00.000000Z",
			} {
				if got := envelope.Error[field]; got != want {
					t.Fatalf("safe field %s=%v want=%q; body=%s", field, got, want, body)
				}
			}
		})
	}
}

func TestPeriodicBudgetInferenceErrorWithholdsInvalidOrMissingMetadata(t *testing.T) {
	for _, rejection := range []billing.BudgetRejection{{}, func() billing.BudgetRejection {
		v := safeBudgetRejection()
		v.Scope = "private-sql-marker"
		return v
	}()} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		private := "private-provider-marker"
		if !billingError(c, &Server{}, &budgetRejectionTestError{private: private, rejection: rejection}) {
			t.Fatal("billing error was not handled")
		}
		var envelope struct {
			Error map[string]any `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Error["code"] != "budget_exceeded" || rec.Code != http.StatusPaymentRequired {
			t.Fatalf("safe public code/status changed: %s", rec.Body.String())
		}
		for _, field := range []string{"scope", "reason", "window_used", "limit_amount", "window_reset_at"} {
			if _, exists := envelope.Error[field]; exists {
				t.Fatalf("incomplete or invalid field %s was exposed: %s", field, rec.Body.String())
			}
		}
		if strings.Contains(rec.Body.String(), private) {
			t.Fatal("private cause leaked without metadata")
		}
	}
}

func TestPeriodicBudgetNonInferenceErrorKeepsInternalProtocol(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/platform/example", nil)
	if !billingError(c, &Server{}, &budgetRejectionTestError{private: "private", rejection: safeBudgetRejection()}) {
		t.Fatal("billing error was not handled")
	}
	if rec.Code != http.StatusPaymentRequired || !strings.Contains(rec.Body.String(), "periodic_budget_exceeded") || strings.Contains(rec.Body.String(), "window_used") {
		t.Fatalf("non-inference error unexpectedly changed contract: %s", rec.Body.String())
	}
}
