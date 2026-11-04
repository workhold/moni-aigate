package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

type prospectiveAttemptStub struct {
	called     int
	invocation procurementexecution.Invocation
	receipt    procurementexecution.AttemptReceipt
	err        error
}

func (r *prospectiveAttemptStub) RunAttempt(_ context.Context, in procurementexecution.Invocation) (procurementexecution.AttemptReceipt, error) {
	r.called++
	r.invocation = in
	return r.receipt, r.err
}

func TestProspectiveBillingConstructionDoesNotConnect(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if New(pool).prospective != nil {
		t.Fatal("ordinary constructor selected candidate")
	}
	for _, schema := range []string{"public", `a.b"c`, "中文", strings.Repeat("a", 63)} {
		s, err := NewProspective(pool, ProspectiveOptions{Schema: schema})
		if err != nil || s.prospective == nil || s.prospective.schema != schema || s.pool != pool {
			t.Fatalf("local construction: %v", err)
		}
	}
	for _, schema := range []string{"", strings.Repeat("a", 64), "bad\x00name", string([]byte{0xff})} {
		if s, err := NewProspective(pool, ProspectiveOptions{Schema: schema}); s != nil || err == nil {
			t.Fatal("invalid schema accepted")
		}
	}
	if s, err := NewProspective(nil, ProspectiveOptions{Schema: "public"}); s != nil || err == nil {
		t.Fatal("nil pool accepted")
	}
	if pool.Stat().TotalConns() != 0 {
		t.Fatal("constructor contacted database")
	}
}

func TestProspectiveBillingOriginalValidationAndByteEncoding(t *testing.T) {
	stub := &prospectiveAttemptStub{}
	s := &Store{prospective: &prospectiveAttempt{schema: `a.b"c`, runner: stub}}
	in := AttemptInput{Sequence: 0, UpstreamModel: "m", RouteDecisions: make([]routing.Decision, 1001)}
	_, err := s.RecordAttempt(nil, in)
	var public *Error
	if !errors.As(err, &public) || public.Message != "invalid attempt input" || stub.called != 0 {
		t.Fatal("outer validation priority changed", err)
	}
	in.Sequence = 1
	_, err = s.RecordAttempt(nil, in)
	if !errors.As(err, &public) || public.Message != "too many route decisions" || stub.called != 0 {
		t.Fatal("route validation bypassed", err)
	}
	in.ChannelID = "00000000-0000-0000-0000-000000000001"
	in.ChannelVersionID = "00000000-0000-0000-0000-000000000002"
	in.TenantID, in.RequestID, in.CredentialID = "legacy tenant", "legacy request", "legacy credential"
	in.UpstreamModel = "\x00" // original boundedString does not reject control bytes
	in.RouteDecisions = []routing.Decision{{ChannelID: in.ChannelID, VersionID: in.ChannelVersionID, Priority: math.MinInt, Weight: math.MaxUint64, Reason: "selected"}}
	negative := int64(-7)
	in.CostQuote = &routing.CostQuote{VersionID: string([]byte{0xff, 0}), Basis: "", InputRate: &negative}
	stub.receipt = procurementexecution.AttemptReceipt{Outcome: "replay", AttemptID: "old text id", RequestID: in.RequestID, Sequence: 1, DeliveryState: "completed", CostStatus: "old cost status"}
	got, err := s.RecordAttempt(context.Background(), in)
	if err != nil || got.ID != "old text id" || got.CostStatus != "old cost status" || stub.called != 1 {
		t.Fatal(got, err)
	}
	args := stub.invocation.Arguments()
	raw, _ := json.Marshal(in.RouteDecisions)
	if stub.invocation.Root() != procurementexecution.RecordAttempt || !strings.Contains(stub.invocation.Query(), `"a.b""c"."pp_record_attempt"`) || !bytes.Equal(args[7].([]byte), raw) || !bytes.Equal(args[9].([]byte), []byte{0xff, 0}) || args[10] != negative || args[11] != nil || args[12].([]byte) == nil || len(args[12].([]byte)) != 0 {
		t.Fatal("fixed root or lossless encoding changed")
	}
	in.RouteDecisions = nil
	in.CostQuote = nil
	_, err = s.RecordAttempt(context.Background(), in)
	args = stub.invocation.Arguments()
	if err != nil || string(args[7].([]byte)) != "[]" || args[8] != false || args[9] != nil || args[10] != nil || args[11] != nil || args[12] != nil {
		t.Fatal("nil unions changed")
	}
}

func TestProspectiveBillingErrorsPreserveCauseAndZeroResult(t *testing.T) {
	for _, state := range []string{"PP101", "PP102", "PP103", "PP105", "PP106", "PP107", "PP108", "PP109", "PP110", "PP111", "PP112", "PP113", "PP114", "PP115", "PP116", "PP117", "PP406", "PP407", "PP408", "PP409", "PP410", "PP411", "PP412", "PP413", "PP414", "PP415", "PP190", "PP901", "PP902", "PP903", "PP904", "PP905", "PP906", "23514", "42501", "PP999"} {
		t.Run(state, func(t *testing.T) {
			cause := &pgconn.PgError{Code: state, Message: "PRIVATE_DATABASE_TEXT"}
			classified := procurementexecution.ClassifyPP(procurementexecution.RecordAttempt, cause)
			stub := &prospectiveAttemptStub{receipt: procurementexecution.AttemptReceipt{AttemptID: "must not escape"}, err: classified}
			s := &Store{prospective: &prospectiveAttempt{schema: "public", runner: stub}}
			got, err := s.RecordAttempt(context.Background(), AttemptInput{Sequence: 1, UpstreamModel: "model"})
			var pg *pgconn.PgError
			if got != (Attempt{}) || !errors.As(err, &pg) || pg != cause {
				t.Fatal("partial result or lost original cause", got, err)
			}
			var failure *procurementexecution.Failure
			var public *Error
			if errors.As(classified, &failure) && failure.Domain() == procurementexecution.DomainBilling {
				if !errors.As(err, &public) || public.Code != failure.PublicCode() || public.Message != failure.PublicMessage() || strings.Contains(err.Error(), "PRIVATE") {
					t.Fatal("unsafe billing classification", err)
				}
			} else if !reflect.DeepEqual(err, classified) {
				t.Fatal("non-billing error reclassified")
			}
			if state == "PP190" && !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal("lookup identity lost")
			}
		})
	}
}

func TestProspectiveBillingUnknownPrecedesReachableBusinessFailure(t *testing.T) {
	unknown := &procurementexecution.UnknownOutcome{}
	classified := procurementexecution.ClassifyPP(procurementexecution.RecordAttempt, &pgconn.PgError{Code: "PP115", Message: "private cause"})
	chain := errors.Join(unknown, classified)
	stub := &prospectiveAttemptStub{receipt: procurementexecution.AttemptReceipt{AttemptID: "must not escape"}, err: chain}
	s := &Store{prospective: &prospectiveAttempt{schema: "public", runner: stub}}
	got, err := s.RecordAttempt(context.Background(), AttemptInput{Sequence: 1, UpstreamModel: "model"})
	var public *Error
	if got != (Attempt{}) || err != chain || errors.As(err, &public) {
		t.Fatal("unknown was downgraded to reachable billing failure")
	}
}
