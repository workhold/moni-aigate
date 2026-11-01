package periodicbudget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTxSQLClassificationRequiresExactSafeDatabaseIdentity(t *testing.T) {
	for _, tc := range []struct{ code, message, constraint, want string }{
		{"23514", "periodic budget exceeded", "pb_budget_tenant", "periodic_budget_exceeded"},
		{"23514", "periodic budget exceeded", "pb_budget_key", "periodic_budget_exceeded"},
		{"23514", "periodic budget exceeded", "pb_budget_procurement", "procurement_periodic_budget_exceeded"},
		{"23514", "periodic budget exceeded", "unknown", "periodic_budget_unavailable"},
		{"23514", "periodic budget exceeded", "", "periodic_budget_unavailable"},
		{"23514", "periodic budget exceeded ", "pb_budget_tenant", "periodic_budget_unavailable"},
		{"23505", "periodic budget exceeded", "pb_budget_tenant", "periodic_budget_unavailable"},
		{"23514", "periodic clock regression", "", "periodic_clock_regression"},
		{"23514", "periodic clock regression ", "", "periodic_budget_unavailable"},
		{"40P01", "secret deadlock details", "", "periodic_budget_unavailable"},
		{"40001", "secret serialization details", "", "periodic_budget_unavailable"},
		{"23514", "secret source invariant details", "", "periodic_budget_unavailable"},
	} {
		original := &pgconn.PgError{Code: tc.code, Message: tc.message, ConstraintName: tc.constraint, Detail: "PRIVATE_SQL_BODY_AND_PARAMETERS", SchemaName: "PRIVATE_SCHEMA"}
		safe := txSQL(original)
		if ErrorCode(safe) != tc.want || safe.Error() != tc.want || strings.Contains(fmt.Sprint(safe), "PRIVATE") || strings.Contains(fmt.Sprint(safe), tc.message) {
			t.Fatalf("unsafe or inaccurate classification: %s", ErrorCode(safe))
		}
		var cause *pgconn.PgError
		if !errors.As(safe, &cause) || cause != original || !errors.Is(safe, original) {
			t.Fatal("safe error lost exact database cause needed by caller transaction policy")
		}
	}
	for _, original := range []error{errors.New("periodic budget exceeded"), errors.New("23514 pb_budget_tenant"), context.Canceled, context.DeadlineExceeded} {
		safe := txSQL(original)
		if ErrorCode(safe) != "periodic_budget_unavailable" || safe.Error() != "periodic_budget_unavailable" || !errors.Is(safe, original) {
			t.Fatal("non-PG error falsely classified or cause lost")
		}
	}
	if txSQL(nil) != nil {
		t.Fatal("nil SQL result is not success")
	}
	if ErrorCode(ErrBudgetExceeded) != "periodic_budget_exceeded" || ErrorCode(ErrProcurementBudgetExceeded) != "procurement_periodic_budget_exceeded" {
		t.Fatal("new cap sentinel code missing")
	}
}

type pbTxZero struct{ pgx.Tx }
type pbTxUncomparable struct {
	pgx.Tx
	values []int
}

func TestTxNilZeroAndUnsupportedIdentityNeverCallsDatabase(t *testing.T) {
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	var typedNil *pgxpool.Tx
	for _, tx := range []pgx.Tx{nil, typedNil, &pgxpool.Tx{}, &pbTxZero{}, pbTxUncomparable{values: []int{1}}} {
		if comparableTx(tx) {
			t.Fatal("unsupported transaction identity accepted")
		}
		_, err := LockCustomerTx(context.Background(), tx, id, id)
		if ErrorCode(err) != "periodic_budget_invariant" {
			t.Fatal("invalid transaction object was not rejected safely")
		}
		_, err = LockSupplierTx(context.Background(), tx, "")
		if ErrorCode(err) != "periodic_budget_invariant" {
			t.Fatal("invalid transaction object was not rejected safely")
		}
	}
}
