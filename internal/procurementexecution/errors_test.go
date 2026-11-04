package procurementexecution_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
)

func TestFixedAttemptPPClassificationKeepsCauseAndMessage(t *testing.T) {
	// This table is the reviewed public contract, independently enumerated so
	// an accidental state omission, text forwarding or cross-root map fails.
	for _, tc := range []struct{ state, code, message string }{
		{"PP101", "invalid_attempt", "credential is not a member of the frozen channel version"},
		{"PP102", "key_ip_forbidden", "request source is not authorized"},
		{"PP103", "invalid_key", "key is not active"},
		{"PP105", "binding_unavailable", "published model binding is quarantined"},
		{"PP106", "request_finished", "request is not eligible for a new attempt"},
		{"PP107", "unpublished_binding", "attempt is not part of the request route snapshot"},
		{"PP108", "request_deadline", "request is no longer eligible for dispatch"},
		{"PP109", "unsafe_retry", "prior attempt has no trustworthy non-consumption evidence"},
		{"PP110", "cost_unknown", "controlled procurement requires a complete known quote"},
		{"PP111", "cost_quote_stale", "configured procurement quote changed before dispatch"},
		{"PP112", "cost_limit_exceeded", "configured procurement rate exceeds route limit"},
		{"PP113", "procurement_bound_invalid", "procurement hard upper bound cannot be represented"},
		{"PP114", "procurement_paused", "supplier procurement account is paused"},
		{"PP115", "procurement_budget_exceeded", "shared supplier procurement budget is exhausted"},
		{"PP116", "idempotency_conflict", "attempt sequence already used"},
		{"PP117", "channel_unavailable", "channel is not available"},
		{"PP406", "forbidden", "key model scope is not authorized"},
		{"PP407", "forbidden", "key channel region is not authorized"},
		{"PP408", "request_deadline", "request deadline expired"},
		{"PP409", "request_deadline", "request deadline expired while waiting for procurement"},
		{"PP410", "unsafe_retry", "prior attempt has no terminal evidence"},
		{"PP411", "cost_quote_stale", "procurement quote changed before attempt"},
		{"PP412", "cost_quote_stale", "configured procurement quote changed before reservation"},
		{"PP413", "cost_unknown", "controlled procurement requires a complete quote"},
		{"PP414", "cost_unknown", "controlled procurement requires both known cost dimensions"},
		{"PP415", "cost_unknown", "route requires a known procurement quote"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			pg := &pgconn.PgError{Code: tc.state, Message: "private wrong message", Detail: "private detail", Where: "private context"}
			cause := fmt.Errorf("driver wrapper: %w", pg)
			err := procurementexecution.ClassifyPP(procurementexecution.RecordAttempt, cause)
			var f *procurementexecution.Failure
			var actual *pgconn.PgError
			if !errors.As(err, &f) || f.Domain() != procurementexecution.DomainBilling || f.SQLState() != tc.state || f.PublicCode() != tc.code || f.PublicMessage() != tc.message || !errors.As(err, &actual) || actual != pg || !errors.Is(err, cause) || strings.Contains(err.Error(), "private") {
				t.Fatalf("wrong fixed classification/cause for %s: %v", tc.state, err)
			}
			if again := procurementexecution.ClassifyPP(procurementexecution.RecordAttempt, err); again != err {
				t.Fatal("classified error was wrapped again")
			}
			if wrong := procurementexecution.ClassifyPP(procurementexecution.CreateAccount, cause); wrong != cause {
				t.Fatal("attempt state leaked into account domain")
			}
		})
	}
}

func TestAccountAndClosureErrorsStayInTheirDomains(t *testing.T) {
	for _, root := range []procurementexecution.Root{procurementexecution.CreateAccount, procurementexecution.ConfigureAccount} {
		for _, tc := range []struct{ state, code string }{{"PP001", "invalid_procurement_configuration"}, {"PP002", "procurement_operation_conflict"}, {"PP003", "procurement_forbidden"}} {
			pg := &pgconn.PgError{Code: tc.state, Message: "private"}
			err := procurementexecution.ClassifyPP(root, pg)
			var f *procurementexecution.Failure
			if !errors.As(err, &f) || f.Domain() != procurementexecution.DomainProcurement || f.PublicCode() != tc.code || f.PublicMessage() != tc.code || !errors.Is(err, pg) {
				t.Fatal("wrong account classification", err)
			}
			if procurementexecution.ClassifyPP(procurementexecution.RecordAttempt, pg) != pg {
				t.Fatal("account state leaked into attempt domain")
			}
		}
	}
	for _, root := range []procurementexecution.Root{procurementexecution.CreateAccount, procurementexecution.ConfigureAccount, procurementexecution.RecordAttempt} {
		for _, tc := range []struct{ state, code string }{{"PP901", "procurement_execution_forbidden"}, {"PP902", "procurement_history_invariant"}, {"PP903", "procurement_execution_protocol"}, {"PP904", "procurement_history_resource"}, {"PP905", "procurement_history_exhausted"}, {"PP906", "procurement_prefix_untracked_domain"}} {
			pg := &pgconn.PgError{Code: tc.state, Message: "private"}
			err := procurementexecution.ClassifyPP(root, pg)
			var f *procurementexecution.Failure
			if !errors.As(err, &f) || f.Domain() != procurementexecution.DomainExecution || f.PublicCode() != tc.code || f.PublicMessage() != tc.code || f.SQLState() != tc.state || !errors.Is(err, pg) {
				t.Fatal("wrong closure classification", err)
			}
		}
	}
}

func TestLookupIsNoRowsWithoutLosingPgError(t *testing.T) {
	pg := &pgconn.PgError{Code: "PP190", Message: "private lookup"}
	err := procurementexecution.ClassifyPP(procurementexecution.RecordAttempt, pg)
	var f *procurementexecution.Failure
	var actual *pgconn.PgError
	if !errors.Is(err, pgx.ErrNoRows) || !errors.As(err, &actual) || actual != pg || !errors.As(err, &f) || f.Domain() != procurementexecution.DomainLookup {
		t.Fatal("lookup identity lost", err)
	}
	for _, root := range []procurementexecution.Root{procurementexecution.CreateAccount, procurementexecution.ConfigureAccount} {
		if procurementexecution.ClassifyPP(root, pg) != pg {
			t.Fatal("lookup mapped outside attempt domain")
		}
	}
}

func TestUnknownPPAndAllNonPPErrorsAreTransparent(t *testing.T) {
	for _, state := range []string{"PP000", "PP104", "PP118", "PP120", "PP143", "PP401", "PP416", "PP999", "42501", "23503", "23505", "23514", "P0002", "22003", "40P01", "40001", "55P03", "57014"} {
		pg := &pgconn.PgError{Code: state, Message: "periodic budget exceeded", ConstraintName: "pb_budget_procurement", Where: "trusted-looking PB context"}
		for _, root := range []procurementexecution.Root{procurementexecution.CreateAccount, procurementexecution.ConfigureAccount, procurementexecution.RecordAttempt} {
			if procurementexecution.ClassifyPP(root, pg) != pg {
				t.Fatalf("unexpected mapping of %s", state)
			}
		}
	}
	for _, err := range []error{nil, context.Canceled, context.DeadlineExceeded, errors.New("transport failed"), pgx.ErrNoRows, pgx.ErrTxCommitRollback} {
		if procurementexecution.ClassifyPP(procurementexecution.RecordAttempt, err) != err {
			t.Fatal("non-PG error changed")
		}
	}
	pg := &pgconn.PgError{Code: "PP101"}
	if procurementexecution.ClassifyPP(procurementexecution.Root("caller_sql"), pg) != pg {
		t.Fatal("unknown root classified")
	}
	local := procurementexecution.ProtocolError(errors.New("private scan detail"))
	if strings.Contains(local.Error(), "private") {
		t.Fatal("protocol text leaked cause")
	}
	var synthetic *pgconn.PgError
	if errors.As(local, &synthetic) {
		t.Fatal("local protocol failure forged PgError")
	}
}
