package periodicbudget_test

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	. "github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"testing"
)

// This wrapper deliberately keeps the same Go object and physical session
// while replacing the ended transaction, so only a full xid check catches it.
type pbTxReusableWrapper struct{ pgx.Tx }

func TestTxSameWrapperAndSessionCannotReuseEndedToken(t *testing.T) {
	f := newPBTxFixture(t)
	conn, err := f.app.Acquire(f.ctx)
	pbTxMust(t, "acquire wrapper session", err)
	defer conn.Release()
	first, err := conn.Begin(f.ctx)
	pbTxMust(t, "begin first wrapped transaction", err)
	wrapped := &pbTxReusableWrapper{first}
	locks, err := LockCustomerTx(f.ctx, wrapped, f.tenant, f.key)
	pbTxMust(t, "lock first wrapped transaction", err)
	pbTxMust(t, "end first wrapped transaction", first.Rollback(f.ctx))
	second, err := conn.Begin(f.ctx)
	pbTxMust(t, "begin second wrapped transaction", err)
	defer second.Rollback(f.ctx)
	wrapped.Tx = second
	_, err = AdmitCustomerTx(f.ctx, wrapped, locks, uuid.NewString(), Amount{"120"})
	pbTxCode(t, err, "periodic_budget_invariant")
	// No source query or hidden rollback should follow the rejected identity.
	var stillActive bool
	pbTxMust(t, "caller still owns second transaction", second.QueryRow(f.ctx, "SELECT true").Scan(&stillActive))
	if !stillActive {
		t.Fatal("helper took transaction ownership")
	}
	pbTxMust(t, "rollback caller-owned second transaction", second.Rollback(f.ctx))
}
