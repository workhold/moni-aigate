package procurement

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
)

type prospectiveAccountStub struct {
	called     int
	invocation procurementexecution.Invocation
	receipt    procurementexecution.AccountReceipt
	err        error
}

func (r *prospectiveAccountStub) RunAccount(_ context.Context, in procurementexecution.Invocation) (procurementexecution.AccountReceipt, error) {
	r.called++
	r.invocation = in
	return r.receipt, r.err
}

func TestProspectiveProcurementConstructionDoesNotConnect(t *testing.T) {
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
		if err != nil || s.prospective == nil || s.prospective.schema != schema || s.Pool != pool {
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

func TestProspectiveProcurementValidationAndImmutableDTO(t *testing.T) {
	const id = "00000000-0000-0000-0000-000000000000" // original Go API accepts nil UUID
	stub := &prospectiveAccountStub{receipt: procurementexecution.AccountReceipt{
		OperationID: id, Actor: id, Kind: "account_create", TargetID: id, Status: "applied", Before: []byte("null"), After: []byte(`{"old":"snapshot"}`), CreatedAt: time.Unix(0, 0).UTC(), RootID: "private-root",
	}}
	s := &Store{prospective: &prospectiveAccount{schema: `a.b"c`, runner: stub}}
	for _, in := range []CreateInput{
		{OperationID: id, Label: "x", Provider: "p", State: "active", LimitAmount: "01"},
		{OperationID: "bad", Label: "x", Provider: "p", State: "active", LimitAmount: "0"},
	} {
		if got, err := s.CreateAccount(nil, id, in); !reflect.DeepEqual(got, Command{}) || !errors.Is(err, ErrInvalid) || stub.called != 0 {
			t.Fatal("invalid input reached runner", err)
		}
	}
	in := CreateInput{OperationID: id, Label: "supplier", Provider: "aggregate", State: "active", LimitAmount: "9223372036854775807"}
	got, err := s.CreateAccount(context.Background(), id, in)
	if err != nil || stub.called != 1 || got.OperationID != id || got.Kind != "account_create" || got.CreatedAt != stub.receipt.CreatedAt {
		t.Fatal(got, err)
	}
	args := stub.invocation.Arguments()
	if stub.invocation.Root() != procurementexecution.CreateAccount || !strings.Contains(stub.invocation.Query(), `"a.b""c"."pp_create_account"`) || args[5] != int64(math.MaxInt64) {
		t.Fatal("create root or amount narrowed")
	}
	stub.receipt.After[2] = 'X'
	if string(got.After) != `{"old":"snapshot"}` {
		t.Fatal("business DTO aliases private receipt")
	}
	config := ConfigurationInput{OperationID: id, ExpectedRevision: "9223372036854775807", Label: "supplier", Provider: "aggregate", State: "paused", LimitAmount: "0"}
	_, err = s.ConfigureAccount(context.Background(), id, id, config)
	args = stub.invocation.Arguments()
	if err != nil || stub.invocation.Root() != procurementexecution.ConfigureAccount || args[3] != int64(math.MaxInt64) || args[7] != int64(0) {
		t.Fatal("configure root or amount narrowed", err)
	}
	for _, bad := range []string{"0", "01", "+1", "9223372036854775808"} {
		config.ExpectedRevision = bad
		if _, err = s.ConfigureAccount(nil, id, id, config); !errors.Is(err, ErrInvalid) || stub.called != 2 {
			t.Fatal("invalid revision reached runner")
		}
	}
}

func TestProspectiveProcurementErrorsPreserveSentinelCauseAndZeroResult(t *testing.T) {
	for _, state := range []string{"PP001", "PP002", "PP003", "PP901", "PP902", "PP903", "PP904", "PP905", "PP906", "23514", "42501", "PP999"} {
		t.Run(state, func(t *testing.T) {
			cause := &pgconn.PgError{Code: state, Message: "PRIVATE_DATABASE_TEXT"}
			classified := procurementexecution.ClassifyPP(procurementexecution.CreateAccount, cause)
			stub := &prospectiveAccountStub{receipt: procurementexecution.AccountReceipt{TargetID: "must not escape"}, err: classified}
			s := &Store{prospective: &prospectiveAccount{schema: "public", runner: stub}}
			got, err := s.CreateAccount(context.Background(), "00000000-0000-0000-0000-000000000000", CreateInput{OperationID: "00000000-0000-0000-0000-000000000000", Label: "s", Provider: "p", State: "active", LimitAmount: "0"})
			var pg *pgconn.PgError
			if !reflect.DeepEqual(got, Command{}) || !errors.As(err, &pg) || pg != cause {
				t.Fatal("partial result or lost cause")
			}
			sentinel := map[string]error{"PP001": ErrInvalid, "PP002": ErrConflict, "PP003": ErrForbidden}[state]
			if sentinel != nil {
				if !errors.Is(err, sentinel) || strings.Contains(err.Error(), "PRIVATE") {
					t.Fatal("public sentinel lost or unsafe", err)
				}
			} else if err != classified {
				t.Fatal("non-procurement error reclassified")
			}
		})
	}
}

func TestProspectiveProcurementUnknownPrecedesReachableBusinessFailure(t *testing.T) {
	unknown := &procurementexecution.UnknownOutcome{}
	classified := procurementexecution.ClassifyPP(procurementexecution.CreateAccount, &pgconn.PgError{Code: "PP002", Message: "private cause"})
	chain := errors.Join(unknown, classified)
	if got := prospectiveError(chain); got != chain || errors.Is(got, ErrConflict) {
		t.Fatal("unknown was downgraded to reachable procurement sentinel")
	}
}
