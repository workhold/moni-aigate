package billing

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

type budgetRejectionCarrier interface {
	BudgetRejection() (BudgetRejection, bool)
}

func TestBudgetRejectionValidation(t *testing.T) {
	valid := BudgetRejection{Scope: "key", Reason: "limit_exceeded", WindowUsed: "120", LimitAmount: "119", WindowResetAt: "2026-11-21T00:00:00.000000Z"}
	if !valid.Valid() {
		t.Fatal("valid safe rejection was rejected")
	}
	for _, change := range []func(*BudgetRejection){
		func(v *BudgetRejection) { v.Scope = "private-sql-marker" },
		func(v *BudgetRejection) { v.Reason = "private-provider-error" },
		func(v *BudgetRejection) { v.WindowUsed = "01" },
		func(v *BudgetRejection) { v.LimitAmount = "9223372036854775808" },
		func(v *BudgetRejection) { v.WindowResetAt = "2026-11-21T00:00:00Z" },
	} {
		v := valid
		change(&v)
		if v.Valid() {
			t.Fatalf("unsafe or noncanonical rejection accepted: %+v", v)
		}
	}
}

func setBudgetFixtureClock(t *testing.T, f *fixture, at time.Time) {
	t.Helper()
	var schema string
	if err := f.pool.QueryRow(f.ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	query := "CREATE OR REPLACE FUNCTION " + pgx.Identifier{schema, "pb_now"}.Sanitize() + "() RETURNS timestamptz LANGUAGE sql VOLATILE SET search_path TO " + pgx.Identifier{schema}.Sanitize() + ",pg_catalog,pg_temp AS $$ SELECT '" + at.UTC().Format("2006-01-02T15:04:05.000000Z") + "'::timestamptz $$"
	if _, err := f.pool.Exec(f.ctx, query); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresPeriodicBudgetRejectionMetadataAndRollback(t *testing.T) {
	for _, scope := range []string{"tenant", "key"} {
		for _, existingHold := range []bool{false, true} {
			name := scope
			if existingHold {
				name += "/held"
			} else {
				name += "/first_window"
			}
			t.Run(name, func(t *testing.T) {
				f := integration(t)
				limit := 119
				if existingHold {
					limit = 120
				}
				now := time.Now().UTC().Truncate(time.Microsecond)
				setBudgetFixtureClock(t, f, now.AddDate(0, 0, -2))
				commander, err := periodicbudget.NewCommander(f.pool)
				if err != nil {
					t.Fatal(err)
				}
				input := periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_day", Enabled: true, LimitAmount: periodicbudget.Int64AmountString(strconv.Itoa(limit))}
				if scope == "tenant" {
					_, err = commander.ConfigurePlatformTenant(f.ctx, f.admin, f.tenant, input)
				} else {
					_, err = commander.ConfigureKey(f.ctx, f.user, f.tenant, f.key, input)
				}
				if err != nil {
					t.Fatal(err)
				}
				setBudgetFixtureClock(t, f, now.AddDate(0, 0, -1))
				var held Reservation
				if existingHold {
					held = f.reserve(t)
					defer f.store.EndRequest(held.RequestID)
				}
				before := vectorReserveFacts(t, f)
				in := f.input()
				r, err := f.store.Reserve(f.ctx, in)
				assertCode(t, err, "periodic_budget_exceeded")
				if r != (Reservation{}) {
					t.Fatalf("rejected reservation returned a receipt: %+v", r)
				}
				var carrier budgetRejectionCarrier
				if !errors.As(err, &carrier) {
					t.Fatalf("rejection lost its safe metadata carrier: %T", err)
				}
				detail, ok := carrier.BudgetRejection()
				wantLimit := "119"
				if existingHold {
					wantLimit = "120"
				}
				if !ok || !detail.Valid() || detail.Scope != scope || detail.Reason != "limit_exceeded" || detail.LimitAmount != wantLimit {
					t.Fatalf("unsafe or wrong rejection metadata: %+v, ok=%v", detail, ok)
				}
				wantUsed := "0"
				if existingHold {
					wantUsed = "120"
				}
				if detail.WindowUsed != wantUsed {
					t.Fatalf("window_used=%s want=%s", detail.WindowUsed, wantUsed)
				}
				var reset time.Time
				if err = f.pool.QueryRow(f.ctx, `SELECT pb_end('utc_day',pb_start('utc_day',pb_now()))`).Scan(&reset); err != nil {
					t.Fatal(err)
				}
				if want := reset.UTC().Format("2006-01-02T15:04:05.000000Z"); detail.WindowResetAt != want {
					t.Fatalf("window_reset_at=%s want=%s", detail.WindowResetAt, want)
				}
				after := vectorReserveFacts(t, f)
				for table, rows := range after {
					if rows != before[table] {
						t.Errorf("rejected admission or metadata read changed %s", table)
					}
				}
				if !existingHold {
					retrievalBillingNoRequest(t, f, in)
				}
				if existingHold {
					if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: held.RequestID, Reason: "not_sent"}); err != nil {
						t.Fatal(err)
					}
				}
				f.check(t)
			})
		}
	}
}
