package mockpsp

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type commitResultTx struct {
	pgx.Tx
	err error
}

func (t commitResultTx) Commit(context.Context) error { return t.err }

func TestCommitClassificationDoesNotReplayUnknown(t *testing.T) {
	for _, tt := range []struct {
		name           string
		err            error
		unknown, retry bool
	}{
		{"ok", nil, false, false}, {"serialization", &pgconn.PgError{Code: "40001"}, false, true}, {"deadlock", &pgconn.PgError{Code: "40P01"}, false, true},
		{"constraint", &pgconn.PgError{Code: "23514"}, false, false}, {"server_connection", &pgconn.PgError{Code: "08006"}, true, false},
		{"eof", io.EOF, true, false}, {"deadline", context.DeadlineExceeded, true, false}, {"cancelled", context.Canceled, true, false}, {"commit_rollback", pgx.ErrTxCommitRollback, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := commitTx(context.Background(), commitResultTx{err: tt.err})
			if (ErrorCode(err) == "mock_outcome_unknown") != tt.unknown || retryableTransaction(err) != tt.retry {
				t.Fatalf("classification unknown=%s retry=%v", ErrorCode(err), retryableTransaction(err))
			}
		})
	}
}

func TestNormalizeErrorKeepsOnlySafeCodes(t *testing.T) {
	raw := &pgconn.PgError{Code: "23514", Message: "secret body DSN"}
	safe := normalizeError(raw)
	if safe.Error() != "mock_unavailable" || errors.Is(safe, raw) {
		t.Fatal("raw database error escaped")
	}
	if got := normalizeError(&paymentproto.Error{Code: "payment_key_unavailable"}); got.Error() != "payment_key_unavailable" {
		t.Fatal("safe key failure lost")
	}
	for _, sentinel := range []error{ErrDisconnect, ErrDeliveryDeferred} {
		if normalizeError(sentinel) != sentinel {
			t.Fatal("internal control signal lost")
		}
	}
}

func TestRepositoryBudgetDoesNotResetParent(t *testing.T) {
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
	defer cancel()
	var r Repository
	ctx, done := r.bounded(parent)
	defer done()
	parentAt, _ := parent.Deadline()
	at, _ := ctx.Deadline()
	if !at.Equal(parentAt) {
		t.Fatal("repository replaced parent deadline")
	}
	if err := r.check(ctx); ErrorCode(err) != "mock_unavailable" {
		t.Fatal("zero repository usable")
	}
}
