package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
)

// Model a preclassified business error retaining its actual database cause.
// This is the As/Unwrap contract used by the existing PB and new adapters.
type executionHTTPBusinessError struct {
	public *billing.Error
	cause  error
}

func (e *executionHTTPBusinessError) Error() string { return e.public.Error() }
func (e *executionHTTPBusinessError) Unwrap() error { return e.cause }
func (e *executionHTTPBusinessError) As(target any) bool {
	if out, ok := target.(**billing.Error); ok {
		*out = e.public
		return true
	}
	return false
}

func TestProcurementExecutionHTTPTypedErrorsPrecedeRawConstraintCause(t *testing.T) {
	private := "PRIVATE_SQL_PARAMETERS_AND_HANDLE"
	for _, tc := range []struct {
		name, code string
		status     int
		err        error
	}{
		{"typed_pb_tenant", "periodic_budget_exceeded", 402, &executionHTTPBusinessError{&billing.Error{Code: "periodic_budget_exceeded", Message: private}, &pgconn.PgError{Code: "23514", Message: private}}},
		{"typed_pb_supplier", "procurement_periodic_budget_exceeded", 503, &executionHTTPBusinessError{&billing.Error{Code: "procurement_periodic_budget_exceeded", Message: private}, &pgconn.PgError{Code: "23514", Message: private}}},
		{"local_protocol", "procurement_execution_protocol", 503, procurementexecution.ProtocolError(&pgconn.PgError{Code: "23514", Message: private})},
		{"unknown", "procurement_outcome_unknown", 503, &procurementexecution.UnknownOutcome{}},
		{"unknown_with_reachable_lookup", "procurement_outcome_unknown", 503, errors.Join(&procurementexecution.UnknownOutcome{}, procurementexecution.ClassifyPP(procurementexecution.RecordAttempt, &pgconn.PgError{Code: "PP190", Message: private}))},
		{"unknown_with_reachable_billing", "procurement_outcome_unknown", 503, errors.Join(&procurementexecution.UnknownOutcome{}, &executionHTTPBusinessError{&billing.Error{Code: "periodic_budget_exceeded", Message: private}, &pgconn.PgError{Code: "23514", Message: private}})},
		{"raw_constraint", "conflict", 409, &pgconn.PgError{Code: "23514", Message: private}},
		{"raw_fk", "conflict", 409, &pgconn.PgError{Code: "23503", Message: private}},
		{"raw_permission", "system_unavailable", 503, &pgconn.PgError{Code: "42501", Message: private}},
		{"unknown_pp", "system_unavailable", 503, &pgconn.PgError{Code: "PP999", Message: private}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/api/platform/procurement-accounts", nil)
			c.Set("request_id", "request-test")
			(&Server{}).error(c, tc.err)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) || !strings.Contains(rec.Body.String(), "request-test") || strings.Contains(rec.Body.String(), private) || strings.Contains(rec.Body.String(), "ObservationHandle") {
				t.Fatal(rec.Code, rec.Body.String())
			}
		})
	}
}

func TestProcurementExecutionHTTPAllClosureCodesAreExplicitlyUnavailable(t *testing.T) {
	for state, code := range map[string]string{
		"PP901": "procurement_execution_forbidden", "PP902": "procurement_history_invariant", "PP903": "procurement_execution_protocol",
		"PP904": "procurement_history_resource", "PP905": "procurement_history_exhausted", "PP906": "procurement_prefix_untracked_domain",
	} {
		t.Run(state, func(t *testing.T) {
			cause := &pgconn.PgError{Code: state, Message: "private diagnostic"}
			classified := procurementexecution.ClassifyPP(procurementexecution.RecordAttempt, cause)
			var pg *pgconn.PgError
			if !errors.As(classified, &pg) || pg != cause {
				t.Fatal("original cause missing")
			}
			for _, err := range []error{classified, &billing.Error{Code: code, Message: "private diagnostic"}} {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/api/platform/procurement-accounts", nil)
				(&Server{}).error(c, err)
				if rec.Code != 503 || !strings.Contains(rec.Body.String(), code) || strings.Contains(rec.Body.String(), "private diagnostic") {
					t.Fatal(rec.Code, rec.Body.String())
				}
			}
		})
	}
}

func TestProcurementExecutionHTTPAdmissionUnknownPrecedesContextCause(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errAdmissionQueueFull} {
		for _, unknown := range []bool{false, true} {
			err := cause
			status, code := 504, "request_timeout"
			if cause == errAdmissionQueueFull {
				status, code = 429, "concurrency_limited"
			}
			if unknown {
				err = errors.Join(&procurementexecution.UnknownOutcome{}, cause)
				status, code = 503, "procurement_outcome_unknown"
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			(&Server{}).admissionError(c, err)
			if rec.Code != status || !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
				t.Fatal(rec.Code, rec.Body.String())
			}
		}
	}
}
