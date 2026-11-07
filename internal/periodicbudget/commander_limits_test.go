package periodicbudget_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
	. "github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

// Funding and source construction use real APP transactions. The extra credit
// has the same dual-approval, immutable-source and ledger checks as production.
func pbCommandFund(t *testing.T, f *pbTxFixture) {
	t.Helper()
	operation := uuid.NewString()
	store := billing.New(f.app)
	credit, err := store.CreateCredit(f.ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, Amount: 200000, BusinessRef: "commander-limits:" + operation, ProofRef: "fixture:verified", IdempotencyKey: operation, RequestHMAC: operation})
	pbTxMust(t, "create complete extra source funding", err)
	_, err = store.ApproveCredit(f.ctx, billing.ApprovalInput{TenantID: f.tenant, ActorID: f.approver, OrderID: credit.ID})
	pbTxMust(t, "approve complete extra source funding", err)
}

// Each attempt belongs to a distinct real customer request, preserving both
// immutable request/sequence and request/channel uniqueness. Nothing is sent.
func pbCommandSupplierSources(t *testing.T, f *pbTxFixture, tx pgx.Tx, requests []string) []string {
	t.Helper()
	f.exec(t, tx, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, f.account)
	ids := make([]string, 0, len(requests))
	for _, request := range requests {
		attempt := pbTxSupplierAttempt(t, f, tx, request, 240)
		f.exec(t, tx, `INSERT INTO procurement_reservations(attempt_id,account_id,binding_id,upper_amount,held_amount,spent_amount,state) VALUES($1,$2,$3,240,240,0,'prepared')`, attempt, f.account, f.binding)
		f.exec(t, tx, `INSERT INTO procurement_budget_events(id,event_key,account_id,attempt_id,held_delta,spent_delta,type) VALUES($1,$2,$3,$4,240,0,'reserved')`, uuid.NewString(), "reserve:"+attempt, f.account, attempt)
		ids = append(ids, attempt)
	}
	f.exec(t, tx, `UPDATE procurement_accounts SET held_amount=held_amount+$2 WHERE id=$1`, f.account, 240*len(requests))
	return ids
}

func TestCommanderLimits1001RejectsThenExactly1000ImportsAllScopes(t *testing.T) {
	f := newPBTxFixture(t)
	pbCommandFund(t, f)
	// Legacy sources exist before the first configuration, at real time.
	f.realClock(t)
	requests := make([]string, 0, 1001)
	for i := 0; i < 1001; i++ {
		requests = append(requests, f.reserveCustomer(t))
	}
	f.setClock(t, time.Date(2026, 11, 10, 12, 0, 0, 123456000, time.UTC))
	seed := f.begin(t)
	attempts := pbCommandSupplierSources(t, f, seed, requests)
	pbTxMust(t, "commit exactly 1001 complete unresolved sources", seed.Commit(f.ctx))
	pbTxAssertSupplierConserved(t, f)
	commander := pbCommander(t, f)
	before := pbCommandSnapshot(t, f) + pbTxSupplierSnapshot(t, f)
	for _, scope := range []string{"tenant", "key", "procurement"} {
		got, err := pbCommandCallFor(commander, f, scope)(f.ctx, pbCommandInput("0", "0"))
		if ErrorCode(err) != "periodic_legacy_import_requires_maintenance" || got != (ConfigureResult{}) {
			t.Fatalf("scope %s did not reject 1001 sources with maintenance and zero result (safe code %q)", scope, ErrorCode(err))
		}
		if before != pbCommandSnapshot(t, f)+pbTxSupplierSnapshot(t, f) {
			t.Fatal("1001-source rejection retained configuration/import facts or changed actual sources")
		}
	}
	// Close one source of each kind through complete money/event paths. One
	// remaining supplier is disputed with pre-configuration spent=70; import
	// must freeze that baseline rather than charge it to a synthetic old window.
	_, err := billing.New(f.app).Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: requests[1000], Reason: "not_sent"})
	pbTxMust(t, "release one genuine customer source", err)
	finish := f.begin(t)
	f.exec(t, finish, `SELECT id FROM upstream_attempts WHERE id=$1 FOR UPDATE`, attempts[1000])
	f.exec(t, finish, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, f.account)
	pbTxChangeSupplier(t, f, finish, attempts[1000], 0, 0, "released")
	pbTxChangeSupplier(t, f, finish, attempts[0], 100, 70, "disputed")
	pbTxMust(t, "commit complete source closures and historical supplier baseline", finish.Commit(f.ctx))
	for _, scope := range []string{"tenant", "key", "procurement"} {
		in := pbCommandInput("0", "0")
		got, err := pbCommandCallFor(commander, f, scope)(f.ctx, in)
		pbTxMust(t, "import exactly 1000 through Commander", err)
		pbCommandReceipt(t, f, got, in, scope, "1", true)
		wantHeld, upper := "120000", 120
		if scope == "procurement" {
			wantHeld, upper = "239860", 240
		}
		if got.Receipt.Result.CarryHeld != wantHeld || got.Receipt.Result.CurrentWindow != nil {
			t.Fatal("legacy import lost exact carry-held amount or invented a period window")
		}
		var exact bool
		pbTxMust(t, "verify all 1000 exact null-window initial allocations and events", f.app.QueryRow(f.ctx, `SELECT
 (SELECT count(*)=1000 AND bool_and(origin='legacy_import' AND window_id IS NULL AND admitted_at IS NULL
  AND revision=1 AND upper_amount=$2 AND spent_amount=0 AND source_spent=baseline AND last_event_id IS NOT NULL)
  AND sum(held_amount)=$3::numeric FROM periodic_budget_allocations WHERE control_id=$1)
 AND (SELECT count(*)=1000 AND bool_and(source_kind='legacy_import' AND allocation_before_revision=0 AND window_before_revision IS NULL)
  FROM periodic_budget_events WHERE control_id=$1)
 AND NOT EXISTS(SELECT 1 FROM periodic_budget_windows WHERE control_id=$1)`, got.Receipt.Operation.ResourceID, upper, wantHeld).Scan(&exact))
		if !exact {
			t.Fatal("first configuration imported an incomplete source set or altered legacy source semantics")
		}
		if scope == "procurement" {
			pbTxMust(t, "verify exact disputed source baseline", f.app.QueryRow(f.ctx, `SELECT baseline=70 AND source_spent=70 AND held_amount=100 AND spent_amount=0 AND state='disputed' FROM periodic_budget_allocations WHERE control_id=$1 AND subject_id=$2`, got.Receipt.Operation.ResourceID, attempts[0]).Scan(&exact))
			if !exact {
				t.Fatal("legacy procurement import did not preserve its frozen baseline and source state")
			}
		}
	}
	pbTxAssertSupplierConserved(t, f)
}

func TestCommanderLimitsImportDoesNotLockBusinessSources(t *testing.T) {
	f := newPBTxFixture(t)
	f.realClock(t)
	request := f.reserveCustomer(t)
	f.setClock(t, time.Date(2026, 11, 10, 12, 0, 0, 123456000, time.UTC))
	seed := f.begin(t)
	attempt := pbCommandSupplierSources(t, f, seed, []string{request})[0]
	pbTxMust(t, "commit real legacy sources for reverse-lock check", seed.Commit(f.ctx))
	blocker := f.begin(t)
	// These business locks deliberately omit all ancestor roots. A correct
	// first configuration reads the already committed sources without waiting
	// back on request/reservation/attempt locks held later in the global order.
	f.exec(t, blocker, `SELECT id FROM request_records WHERE id=$1 FOR UPDATE`, request)
	f.exec(t, blocker, `SELECT id FROM reservations WHERE request_id=$1 FOR UPDATE`, request)
	f.exec(t, blocker, `SELECT id FROM upstream_attempts WHERE id=$1 FOR UPDATE`, attempt)
	f.exec(t, blocker, `SELECT attempt_id FROM procurement_reservations WHERE attempt_id=$1 FOR UPDATE`, attempt)
	commander := pbCommander(t, f)
	for _, scope := range []string{"tenant", "key", "procurement"} {
		ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
		got, err := pbCommandCallFor(commander, f, scope)(ctx, pbCommandInput("0", "0"))
		cancel()
		pbTxMust(t, "import while later-order business sources stay row-locked", err)
		if !got.Created || got.Receipt.Result.CarryHeld == "0" {
			t.Fatal("source-lock check did not import an actual nonzero unresolved source")
		}
	}
	pbTxMust(t, "release unchanged business source locks", blocker.Rollback(f.ctx))
	pbTxAssertSupplierConserved(t, f)
}

func TestCommanderLimitsRevisionEndpoint(t *testing.T) {
	for _, scope := range []string{"tenant", "key", "procurement"} {
		t.Run(scope, func(t *testing.T) {
			f := newPBTxFixture(t)
			call := pbCommandCallFor(pbCommander(t, f), f, scope)
			first, err := call(f.ctx, pbCommandInput("0", "500"))
			pbTxMust(t, "create endpoint fixture through Commander", err)
			// This is an explicit owner-only endpoint fixture, not a claim that
			// MaxInt64 historical changes occurred. Keep the original receipt,
			// safe-input fingerprint, version and current pointer consistent.
			tx, err := f.owner.Begin(f.ctx)
			pbTxMust(t, "begin isolated owner revision endpoint fixture", err)
			defer tx.Rollback(f.ctx)
			for _, table := range []string{"periodic_budget_controls", "periodic_budget_operations", "periodic_budget_configuration_versions"} {
				_, err = tx.Exec(f.ctx, "ALTER TABLE "+pgx.Identifier{table}.Sanitize()+" DISABLE TRIGGER USER")
				pbTxMust(t, "disable guards only for isolated revision endpoint fixture", err)
			}
			_, err = tx.Exec(f.ctx, `UPDATE periodic_budget_controls SET revision=9223372036854775807 WHERE id=$1`, first.Receipt.Operation.ResourceID)
			pbTxMust(t, "set isolated revision endpoint", err)
			_, err = tx.Exec(f.ctx, `UPDATE periodic_budget_configuration_versions SET revision=9223372036854775807 WHERE control_id=$1`, first.Receipt.Operation.ResourceID)
			pbTxMust(t, "align isolated endpoint version", err)
			_, err = tx.Exec(f.ctx, `UPDATE periodic_budget_operations SET revision=9223372036854775807,
 safe_input=jsonb_set(safe_input,'{expected_revision}',to_jsonb('9223372036854775806'::text)),
 result_snapshot=jsonb_set(result_snapshot,'{revision}',to_jsonb('9223372036854775807'::text)),
 input_fingerprint=encode(sha256(pb_frame(ARRAY[kind,actor,scope,target,'9223372036854775806',safe_input->>'period_kind',safe_input->>'enabled',safe_input->>'limit_amount'])),'hex') WHERE operation_id=$1`, first.Receipt.Operation.OperationID)
			pbTxMust(t, "align canonical isolated endpoint receipt", err)
			for _, table := range []string{"periodic_budget_controls", "periodic_budget_operations", "periodic_budget_configuration_versions"} {
				_, err = tx.Exec(f.ctx, "ALTER TABLE "+pgx.Identifier{table}.Sanitize()+" ENABLE TRIGGER USER")
				pbTxMust(t, "restore all revision endpoint guards", err)
			}
			pbTxMust(t, "commit explicit owner endpoint fixture", tx.Commit(f.ctx))
			var oldTuple string
			pbTxMust(t, "remember endpoint control tuple", f.app.QueryRow(f.ctx, `SELECT ctid::text FROM periodic_budget_controls WHERE id=$1`, first.Receipt.Operation.ResourceID).Scan(&oldTuple))
			noopInput := pbCommandInput("9223372036854775807", "500")
			noop, err := call(f.ctx, noopInput)
			pbTxMust(t, "no-op remains usable at revision endpoint", err)
			pbCommandReceipt(t, f, noop, noopInput, scope, "9223372036854775807", false)
			var newTuple string
			pbTxMust(t, "verify endpoint no-op never UPDATEs control", f.app.QueryRow(f.ctx, `SELECT ctid::text FROM periodic_budget_controls WHERE id=$1`, first.Receipt.Operation.ResourceID).Scan(&newTuple))
			if oldTuple != newTuple {
				t.Fatal("revision-endpoint no-op rewrote control")
			}
			pbCommandFailure(t, f, call, pbCommandInput("9223372036854775807", "501"), "periodic_revision_exhausted")
		})
	}
}
