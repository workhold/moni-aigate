package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Only the isolated migration-owner fixture compresses a 24h financial
// deadline. Production APP cannot alter it or disable its identity guard.
func setFixtureReservationDeadline(t *testing.T, f *fixture, request, offset string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal("begin isolated deadline fixture")
	}
	defer tx.Rollback(ctx)
	var owner bool
	if err = tx.QueryRow(ctx, `SELECT current_user=pg_get_userbyid(relowner) AND current_schema() LIKE 'billing_test_%' FROM pg_class WHERE oid='reservations'::regclass`).Scan(&owner); err != nil || !owner {
		t.Fatal("deadline fixture requires the isolated schema owner")
	}
	for _, query := range []string{
		`ALTER TABLE reservations DISABLE TRIGGER pb_customer_reservation_identity`,
		`UPDATE reservations SET deadline=clock_timestamp()+$2::text::interval WHERE request_id=$1`,
		`SET CONSTRAINTS ALL IMMEDIATE`,
		`ALTER TABLE reservations ENABLE TRIGGER pb_customer_reservation_identity`,
	} {
		if query[0:6] == "UPDATE" {
			_, err = tx.Exec(ctx, query, request, offset)
		} else {
			_, err = tx.Exec(ctx, query)
		}
		if err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) {
				t.Fatalf("isolated deadline fixture failed (SQLSTATE %s)", databaseError.Code)
			}
			t.Fatal("isolated deadline fixture update failed")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("commit isolated deadline fixture")
	}
}
