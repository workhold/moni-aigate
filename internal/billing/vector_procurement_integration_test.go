package billing

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func vectorProcurementCost(t *testing.T, f *fixture, in ReserveInput, input, output int64, basis string) string {
	t.Helper()
	id := uuid.NewString()
	meterquote.PublishCost(t, f.ctx, f.pool, meterquote.CostPrice{ID: id, ChannelID: f.channel, UpstreamModel: "upstream", ModelID: in.ModelID, Actor: f.admin, Basis: basis, InputRate: func() *int64 {
		if basis == "unknown" {
			return nil
		}
		return &input
	}(), OutputRate: func() *int64 {
		if basis == "unknown" {
			return nil
		}
		return &output
	}()})
	return id
}

// Construct a self-consistent raw account/event projection through the APP,
// without the Go U calculator. The deferred numerical guard is the authority
// being tested, not the calling service or a deliberately broken ledger.
func vectorProcurementRaw(t *testing.T, f *fixture, r Reservation, cost *string, basis, account string, upper int64, snapshot bool, birth ...func(pgx.Tx, string) error) (pgx.Tx, string) {
	t.Helper()
	tx, err := f.store.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(f.ctx) })
	if _, err = lockRequestWithChannel(f.ctx, tx, f.tenant, r.RequestID, f.channel); err != nil {
		t.Fatal(err)
	}
	a := uuid.NewString()
	if _, err = tx.Exec(f.ctx, `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model) VALUES($1,$2,$3,1,$4,$5,$6,'upstream')`, a, f.tenant, r.RequestID, f.channel, f.version, f.credential); err != nil {
		t.Fatal(err)
	}
	if len(birth) != 0 {
		if err = birth[0](tx, a); err != nil {
			t.Fatal(err)
		}
	} else if snapshot {
		if _, err = tx.Exec(f.ctx, `SELECT meter_insert_attempt_snapshot($1,$2,$3,$4,$5)`, f.tenant, a, r.RequestID, cost, basis); err != nil {
			t.Fatal(err)
		}
	}
	if err = procurementAccountLock(f.ctx, tx, account); err != nil {
		t.Fatal(err)
	}
	locks, err := periodicbudget.LockSupplierTx(f.ctx, tx, account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO procurement_reservations(attempt_id,account_id,binding_id,upper_amount,held_amount,spent_amount,state)
 SELECT $1,$2,id,$3::numeric,$3::numeric,0,'prepared' FROM credential_procurement_bindings WHERE account_id=$2 AND credential_id=$4`, a, account, upper, f.credential); err != nil {
		t.Fatal(err)
	}
	if _, err = periodicbudget.AdmitSupplierTx(f.ctx, tx, locks, a, periodicbudget.Amount{Decimal: fmt.Sprint(upper)}); err != nil {
		t.Fatal(err)
	}
	if err = procurementEvent(f.ctx, tx, locks, account, a, "reserve:"+a, "reserved", big.NewInt(upper), big.NewInt(0), ""); err != nil {
		t.Fatal(err)
	}
	return tx, a
}

func TestPostgresVectorProcurementOriginalU(t *testing.T) {
	for _, ready := range []bool{true, false} {
		for _, tc := range []struct {
			name        string
			rate, upper int64
		}{
			{"paid", 2000000, 240}, {"zero", 0, 0}, {"single_ceil", 1, 1},
		} {
			t.Run(fmt.Sprint(ready)+"/"+tc.name, func(t *testing.T) {
				f := integration(t)
				in := f.input()
				if !ready {
					in.RequestHMAC = "unready"
				}
				vectorProcurementCost(t, f, in, tc.rate, tc.rate, "contract")
				account := procurementFixture(t, f, 1000)
				vectorReserveApplication(t, f)
				r, err := f.store.Reserve(f.ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				defer f.store.EndRequest(r.RequestID)
				// Candidate/attempt/lock-after-wait readers must not call scalar mirrors.
				forbidScalarActualReads(t, f)
				aInput := procurementAttempt(t, f, r)
				a, err := f.store.RecordAttempt(f.ctx, aInput)
				if err != nil {
					t.Fatal("attempt called the scalar echo", err)
				}
				procurementBalance(t, f, account, fmt.Sprint(tc.upper), "0")
				var upper int64
				var canonicalReady bool
				if err = f.pool.QueryRow(f.ctx, `SELECT p.upper_amount::bigint,s.supplier_bounds_canonical IS NOT NULL FROM procurement_reservations p JOIN attempt_meter_snapshots s ON s.attempt_id=p.attempt_id WHERE p.attempt_id=$1`, a.ID).Scan(&upper, &canonicalReady); err != nil || upper != tc.upper || canonicalReady != ready {
					t.Fatal("original bound/readiness changed", upper, canonicalReady, err)
				}
				forbidScalarActualReads(t, f)
				tx, err := f.store.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(f.ctx)
				got, err := storedSupplierReservationAmount(f.ctx, tx, f.tenant, r.RequestID, a.ID)
				if err != nil || got != tc.upper {
					t.Fatal("original U reader called a scalar function", got, err)
				}
				for _, wrong := range []struct{ tenant, request, attempt string }{
					{uuid.NewString(), r.RequestID, a.ID}, {f.tenant, uuid.NewString(), a.ID}, {f.tenant, r.RequestID, uuid.NewString()},
				} {
					_, err = storedSupplierReservationAmount(f.ctx, tx, wrong.tenant, wrong.request, wrong.attempt)
					assertCode(t, err, "invalid_meter_snapshot")
				}
				if err = tx.Commit(f.ctx); err != nil {
					t.Fatal(err)
				}
				// Neither customer release nor later publication changes U.
				vectorProcurementCost(t, f, in, 8000000, 8000000, "invoice")
				if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); err != nil {
					t.Fatal(err)
				}
				procurementBalance(t, f, account, fmt.Sprint(tc.upper), "0")
				f.wallet(t, 1000, 0)
				f.check(t)
			})
		}
	}
}

func TestPostgresVectorProcurementGuardSecurity(t *testing.T) {
	f := integration(t)
	var invoker, pinned, publicExec bool
	err := f.pool.QueryRow(f.ctx, `SELECT NOT p.prosecdef,
 p.proconfig=ARRAY[format('search_path=%I, pg_catalog, pg_temp',current_schema())],
 has_function_privilege('public',p.oid,'EXECUTE')
 FROM pg_proc p WHERE p.oid='meter_procurement_bound_matches()'::regprocedure`).Scan(&invoker, &pinned, &publicExec)
	if err != nil || !invoker || !pinned || publicExec {
		t.Fatal("procurement U guard security boundary changed", invoker, pinned, publicExec, err)
	}
}

func TestPostgresVectorProcurementGuardRejectsSelfConsistentForgery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		upper    int64
		rate     int64
		basis    string
		snapshot bool
		valid    bool
	}{
		{"known", 240, 2000000, "contract", true, true},
		{"zero", 0, 0, "invoice", true, true},
		{"single_ceil", 1, 1, "contract", true, true},
		{"under_reserved", 239, 2000000, "contract", true, false},
		{"over_reserved", 241, 2000000, "contract", true, false},
		{"free_forgery", 0, 2000000, "contract", true, false},
		{"rounded_per_dimension", 2, 1, "contract", true, false},
		{"unknown", 0, 0, "unknown", true, false},
		{"missing_snapshot", 240, 2000000, "contract", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := integration(t)
			in := f.input()
			cost := vectorProcurementCost(t, f, in, tc.rate, tc.rate, tc.basis)
			account := procurementFixture(t, f, 1000)
			vectorReserveApplication(t, f)
			r := f.reserve(t)
			defer f.store.EndRequest(r.RequestID)
			before := vectorProcurementFacts(t, f)
			forbidScalarActualReads(t, f)
			tx, _ := vectorProcurementRaw(t, f, r, &cost, tc.basis, account, tc.upper, tc.snapshot)
			_, err := tx.Exec(f.ctx, `SET CONSTRAINTS meter_procurement_bound_matches IMMEDIATE`)
			if !tc.valid {
				if err == nil || !strings.Contains(err.Error(), "procurement upper amount does not match original supplier vector") {
					t.Fatal("self-consistent forged U was not rejected by the numerical guard", err)
				}
				if err = tx.Rollback(f.ctx); err != nil {
					t.Fatal(err)
				}
				vectorProcurementUnchanged(t, f, before)
				procurementBalance(t, f, account, "0", "0")
				f.wallet(t, 880, 120)
				return
			}
			if err != nil {
				t.Fatal("valid original U was rejected", err)
			}
			if err = tx.Commit(f.ctx); err != nil {
				t.Fatal("valid raw birth did not pass all other original guards", err)
			}
			procurementBalance(t, f, account, fmt.Sprint(tc.upper), "0")
			// A legitimate state update must still check the original U after
			// a later cost publication, even with scalar functions forbidden.
			vectorProcurementCost(t, f, in, 8000000, 8000000, "contract")
			f.final(t, r, Usage{Status: "missing"}, "failed", "not_sent", "none", false)
			procurementBalance(t, f, account, "0", "0")
			f.check(t)
		})
	}
}

func vectorProcurementFacts(t *testing.T, f *fixture) map[string]string {
	t.Helper()
	facts := vectorReserveFacts(t, f)
	for _, table := range []string{"upstream_attempts", "attempt_meter_snapshots", "attempt_meter_bound_lines", "procurement_accounts", "procurement_reservations", "procurement_budget_events", "procurement_evidence_identities"} {
		var rows string
		if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM `+pgx.Identifier{table}.Sanitize()+` t`).Scan(&rows); err != nil {
			t.Fatal(table, err)
		}
		facts[table] = rows
	}
	return facts
}

func vectorProcurementUnchanged(t *testing.T, f *fixture, before map[string]string) {
	t.Helper()
	for table, rows := range vectorProcurementFacts(t, f) {
		if rows != before[table] {
			t.Errorf("rejected procurement changed %s", table)
		}
	}
}

func TestPostgresVectorProcurementOverflowRollsBackNewAttempt(t *testing.T) {
	f := integration(t)
	in := vectorReserveModel(t, f, 1000001)
	retailPrice(t, f, 1, 0, retailNow(t, f))
	cost := vectorProcurementCost(t, f, in, math.MaxInt64, math.MaxInt64, "contract")
	account := procurementFixture(t, f, 1000)
	vectorReserveApplication(t, f)
	r, err := f.store.Reserve(f.ctx, in)
	if err != nil || r.Amount != 2 {
		t.Fatal(r, err)
	}
	defer f.store.EndRequest(r.RequestID)
	before := vectorProcurementFacts(t, f)
	aInput := procurementAttempt(t, f, r)
	a, err := f.store.RecordAttempt(f.ctx, aInput)
	assertCode(t, err, "procurement_bound_invalid")
	var attempts int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM upstream_attempts WHERE request_id=$1`, r.RequestID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatal("overflow left a partially born attempt", a, attempts, err)
	}
	vectorProcurementUnchanged(t, f, before)
	// Even a raw self-consistent U=1 cannot hide the original true overflow.
	tx, _ := vectorProcurementRaw(t, f, r, &cost, "contract", account, 1, true)
	if _, err = tx.Exec(f.ctx, `SET CONSTRAINTS meter_procurement_bound_matches IMMEDIATE`); err == nil || !strings.Contains(err.Error(), "procurement upper amount") {
		t.Fatal("raw overflow was truncated into a valid U", err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	vectorProcurementUnchanged(t, f, before)
	procurementBalance(t, f, account, "0", "0")
	f.wallet(t, 998, 2)
	f.check(t)
}

func TestPostgresVectorProcurementInputOnlyU(t *testing.T) {
	for _, protocol := range []string{"openai_embeddings", "jina_rerank"} {
		t.Run(protocol, func(t *testing.T) {
			f := integration(t)
			in := retrievalBillingInput(t, f, protocol, 0, nil)
			vectorProcurementCost(t, f, in, 1, 0, "invoice")
			account := procurementFixture(t, f, 1000)
			vectorReserveApplication(t, f)
			r, err := f.store.Reserve(f.ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			defer f.store.EndRequest(r.RequestID)
			a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
			if err != nil {
				t.Fatal(err)
			}
			procurementBalance(t, f, account, "1", "0")
			forbidScalarActualReads(t, f)
			tx, err := f.store.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			upper, err := storedSupplierReservationAmount(f.ctx, tx, f.tenant, r.RequestID, a.ID)
			if err != nil || upper != 1 {
				t.Fatal("input-only U did not include the explicit zero output dimension", upper, err)
			}
			if err = tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			f.final(t, r, Usage{Status: "missing"}, "failed", "not_sent", "none", false)
			procurementBalance(t, f, account, "0", "0")
			f.check(t)
		})
	}
}

func TestPostgresVectorProcurementGuardRejectsIncompleteOriginal(t *testing.T) {
	for _, kind := range []string{"missing_input_bound", "missing_output_bound", "missing_input_rate", "missing_output_rate", "wrong_rate_unit", "extra_rate"} {
		t.Run(kind, func(t *testing.T) {
			f := integration(t)
			in := f.input()
			in.RequestHMAC = "unready"
			cost := vectorProcurementCost(t, f, in, 2000000, 2000000, "contract")
			account := procurementFixture(t, f, 1000)
			vectorReserveApplication(t, f)
			r, err := f.store.Reserve(f.ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			defer f.store.EndRequest(r.RequestID)
			before := vectorProcurementFacts(t, f)
			if strings.Contains(kind, "rate") || kind == "wrong_rate_unit" || kind == "extra_rate" {
				// A cost root is itself an operation-owned immutable fact. A
				// raw malformed root must therefore stop at that earlier guard;
				// it must not be used to manufacture an incomplete U snapshot.
				tx, txErr := f.store.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
				if txErr != nil {
					t.Fatal(txErr)
				}
				badCost := uuid.NewString()
				_, txErr = tx.Exec(f.ctx, `INSERT INTO cost_versions(id,channel_id,upstream_model,basis,effective_at,schema_id,schema_digest,variant_key,rates_canonical,rates_digest)
 SELECT $1,channel_id,upstream_model,basis,clock_timestamp(),schema_id,schema_digest,variant_key,rates_canonical,rates_digest FROM cost_versions WHERE id=$2`, badCost, cost)
				if txErr == nil || !strings.Contains(txErr.Error(), "cost price operation mismatch") {
					t.Fatalf("malformed cost root crossed its operation guard: %v", txErr)
				}
				if rbErr := tx.Rollback(f.ctx); rbErr != nil {
					t.Fatal(rbErr)
				}
				vectorProcurementUnchanged(t, f, before)
				return
			}

			tx, attempt := vectorProcurementRaw(t, f, r, &cost, "contract", account, 240, false, func(tx pgx.Tx, attempt string) error {
				if _, err := tx.Exec(f.ctx, `INSERT INTO attempt_meter_snapshots(tenant_id,attempt_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,cost_version_id,quote_status,unknown_reason,cost_basis,input_bound,output_bound)
 SELECT tenant_id,$1,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,$3,'known',NULL,'contract',input_bound,output_bound FROM request_meter_snapshots WHERE request_id=$2`, attempt, r.RequestID, cost); err != nil {
					return err
				}
				_, err := tx.Exec(f.ctx, `INSERT INTO attempt_meter_bound_lines(tenant_id,attempt_id,side,dimension,unit,quantity)
				SELECT tenant_id,$1,side,dimension,unit,quantity FROM request_meter_bound_lines WHERE request_id=$2
 AND NOT ($3='missing_input_bound' AND side='supplier' AND dimension='text_input_token') AND NOT ($3='missing_output_bound' AND side='supplier' AND dimension='text_output_token')`, attempt, r.RequestID, kind)
				return err
			})
			// The Go reader and the deferred SQL U guard must both reject an
			// original snapshot whose supplier bounds are incomplete.
			_, err = storedSupplierReservationAmount(f.ctx, tx, f.tenant, r.RequestID, attempt)
			assertCode(t, err, "invalid_meter_snapshot")
			if _, err = tx.Exec(f.ctx, `SET CONSTRAINTS meter_procurement_bound_matches IMMEDIATE`); err == nil || !strings.Contains(err.Error(), "procurement upper amount") {
				t.Fatal("incomplete original vector passed the U guard", err)
			}
			if err = tx.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			vectorProcurementUnchanged(t, f, before)
		})
	}
}

func TestPostgresVectorProcurementBoundShapeRejectsInvalidRows(t *testing.T) {
	for _, tc := range []struct {
		name, dimension, unit, constraint string
	}{
		{"wrong_bound_unit", "text_input_token", "byte", "attempt_meter_bound_lines_unit_check"},
		{"extra_bound", "request_count", "request", "attempt_meter_bound_lines_dimension_check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := integration(t)
			vectorReserveApplication(t, f)
			r := f.reserve(t)
			defer f.store.EndRequest(r.RequestID)
			before := vectorProcurementFacts(t, f)
			tx, err := f.store.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			if _, err = lockRequestWithChannel(f.ctx, tx, f.tenant, r.RequestID, f.channel); err != nil {
				t.Fatal(err)
			}
			attempt := uuid.NewString()
			if _, err = tx.Exec(f.ctx, `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model) VALUES($1,$2,$3,1,$4,$5,$6,'upstream')`, attempt, f.tenant, r.RequestID, f.channel, f.version, f.credential); err != nil {
				t.Fatal(err)
			}
			// These rows are rejected before any deferred U guard can run.
			// Keep the original CHECKs enabled and prove the exact boundary.
			_, err = tx.Exec(f.ctx, `INSERT INTO attempt_meter_bound_lines(tenant_id,attempt_id,side,dimension,unit,quantity) VALUES($1,$2,'supplier',$3,$4,0)`, f.tenant, attempt, tc.dimension, tc.unit)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != tc.constraint {
				t.Fatal("invalid supplier bound was not rejected by the original shape CHECK", err)
			}
			if err = tx.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			vectorProcurementUnchanged(t, f, before)
		})
	}
}
