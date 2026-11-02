package maintenancetrust

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func fundingLotsObservationExpected() FundingLotsExpected {
	return FundingLotsExpected{EpochID: uuid.NewString(), BasisDigest: strings.Repeat("a", 64), ManifestDigest: authorityManifestSHA,
		DescriptorDigest: fundingLotsBasisDescriptor, RuntimeProofDigest: strings.Repeat("b", 64)}
}

func assertObservationReadOnly(t *testing.T, w *gateWire, rolledBack bool) {
	t.Helper()
	queries, _ := w.recorded()
	rollbacks := 0
	for _, q := range queries {
		upper := strings.ToUpper(strings.TrimSpace(q))
		words := strings.Fields(upper)
		if len(words) == 0 {
			t.Fatal("observer emitted empty SQL")
		}
		if upper == "ROLLBACK" {
			rollbacks++
		}
		if words[0] != "SELECT" && words[0] != "WITH" && words[0] != "SET" && words[0] != "BEGIN" && upper != "ROLLBACK" {
			t.Fatalf("observer sent a command outside fixed read-only infrastructure: %.80s", q)
		}
		if strings.HasPrefix(upper, "BEGIN ") && (!strings.Contains(upper, "REPEATABLE READ") || !strings.Contains(upper, "READ ONLY")) {
			t.Fatal("observer transaction was not original RR READ ONLY")
		}
	}
	if rolledBack && rollbacks != 1 || rollbacks > 1 {
		t.Fatal("observer did not retain a single read-only terminal command")
	}
}

func observeFundingLotsOrdinary(t *testing.T, f *snapshotFixture, expected FundingLotsExpected) FundingLotsObservation {
	t.Helper()
	g, w, backend := f.acquire(t, "")
	result, err := g.ObserveFundingLots(context.Background(), expected)
	if err != nil {
		t.Fatalf("independent protected observer: %v", err)
	}
	if g.Close(nil) != nil {
		t.Fatal("successful observer did not retain cleanup result")
	}
	f.gone(t, backend)
	assertObservationReadOnly(t, w, true)
	if next, err := g.ObserveFundingLots(context.Background(), expected); next != (FundingLotsObservation{}) || err != ErrTransaction {
		t.Fatal("observer gate reused its unique Begin")
	}
	return result
}

func TestFundingLotsObservationPostgresUninitializedAndMatched(t *testing.T) {
	f := newSnapshotFixturePrepared(t, func(_ *snapshotFixture, pool *pgxpool.Pool) {
		for _, id := range []string{"", "旧/tenant"} {
			if _, err := pool.Exec(context.Background(), `INSERT INTO tenants(id,name) VALUES($1,'old zero')`, id); err != nil {
				t.Fatal("observer zero legacy fixture")
			}
		}
	})
	if result := observeFundingLotsOrdinary(t, f, fundingLotsObservationExpected()); result != (FundingLotsObservation{Status: FundingLotsObservationUninitialized}) {
		t.Fatal("empty C0 was not precisely uninitialized")
	}
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	fundingLotsInitOrdinary(t, f, plan.Expected)
	for _, different := range []bool{false, true} {
		expected := plan.Expected
		status := FundingLotsObservationMatched
		if different {
			expected.EpochID, status = uuid.NewString(), FundingLotsObservationDifferentEpoch
		}
		result := observeFundingLotsOrdinary(t, f, expected)
		if result.Status != status || result.Observed != plan.Expected || result.SourceRows == 0 || result.SourceBytes == 0 {
			t.Fatal("observer lost actual persistent epoch or graph counters")
		}
	}
	for i := 0; i < 4; i++ {
		expected := plan.Expected
		fields := []*string{&expected.BasisDigest, &expected.ManifestDigest, &expected.DescriptorDigest, &expected.RuntimeProofDigest}
		*fields[i] = strings.Repeat("f", 64)
		g, w, backend := f.acquire(t, "")
		result, err := g.ObserveFundingLots(context.Background(), expected)
		if result != (FundingLotsObservation{}) || err != ErrSchema {
			t.Fatal("same epoch with a different comparison digest returned a conclusion")
		}
		f.gone(t, backend)
		assertObservationReadOnly(t, w, false)
	}
	// A nonempty observer must not weaken the original empty-installation issuer.
	g, _, backend := f.acquire(t, "")
	s := f.begin(t, g, false, context.Background())
	if proof, err := s.VerifyInstallation(context.Background()); proof != nil || err != ErrSchema {
		t.Fatal("observer weakened original empty installation requirement")
	}
	f.gone(t, backend)
}

func TestFundingLotsObservationPostgresBusyCancellationAndCleanup(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, fault := range []string{"cancel", "rollback_reply", "cleanup_reply", "hold_success"} {
		t.Run(fault, func(t *testing.T) {
			arm := clearMaintenanceActivitySQL
			if fault == "rollback_reply" {
				arm = "rollback"
			} else if fault == "cleanup_reply" || fault == "hold_success" {
				arm = unlockMigrationSQL
			}
			g, w, backend := f.acquire(t, arm)
			alias := *g
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type reply struct {
				result FundingLotsObservation
				err    error
			}
			done := make(chan reply, 1)
			go func() {
				result, err := g.ObserveFundingLots(ctx, fundingLotsObservationExpected())
				done <- reply{result, err}
			}()
			fundingLotsAwaitReply(t, w)
			g.state.mu.Lock()
			bounded := g.state.snapshot != nil && time.Until(g.state.snapshot.deadline) <= fundingLotsBudget
			g.state.mu.Unlock()
			if !bounded {
				t.Fatal("observer extended the fixed cumulative budget")
			}
			select {
			case <-done:
				t.Fatal("observer published before its original cleanup completed")
			default:
			}
			if r, err := alias.ObserveFundingLots(context.Background(), fundingLotsObservationExpected()); r != (FundingLotsObservation{}) || err != ErrGateBusy {
				t.Fatal("copied gate escaped observation busy slot")
			}
			if s, err := alias.BeginInit(context.Background()); s != nil || err != ErrGateBusy || alias.Close(nil) != ErrGateBusy {
				t.Fatal("public Begin/Close entered an active observer")
			}
			if fault == "cancel" {
				cancel()
			} else if fault == "hold_success" {
				close(w.release)
			} else {
				close(w.drop)
			}
			select {
			case reply := <-done:
				if fault == "hold_success" {
					if reply.err != nil || reply.result != (FundingLotsObservation{Status: FundingLotsObservationUninitialized}) || alias.Close(nil) != nil {
						t.Fatal("successful observer failed bounded cleanup")
					}
				} else {
					if reply.err == nil || reply.result != (FundingLotsObservation{}) {
						t.Fatal("failed observer returned a partial conclusion")
					}
					if fault == "cancel" && reply.err != ErrTimeout || fault == "cleanup_reply" && reply.err != ErrCleanup {
						t.Fatal("observer failed to preserve safe failure category")
					}
				}
			case <-time.After(6 * time.Second):
				t.Fatal("observer did not release after physical reply fault")
			}
			f.gone(t, backend)
			assertObservationReadOnly(t, w, fault == "hold_success" || fault == "cleanup_reply")
		})
	}
}

func TestFundingLotsObservationPostgresMixedNumericHistory(t *testing.T) {
	f := newSnapshotFixturePrepared(t, func(f *snapshotFixture, pool *pgxpool.Pool) { fundingLotsMixedHistory(t, f, pool) })
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	if plan.Metadata.AvailableTotal != "9223372036854775872" || plan.Metadata.HeldTotal != "9223372036854775727" {
		t.Fatal("observer mixed NUMERIC fixture lost independent totals")
	}
	fundingLotsInitOrdinary(t, f, plan.Expected)
	result := observeFundingLotsOrdinary(t, f, plan.Expected)
	if result.Status != FundingLotsObservationMatched || result.Observed != plan.Expected {
		t.Fatal("observer rejected real funded mixed legacy history")
	}
}

func TestFundingLotsObservationPostgresSandbox(t *testing.T) {
	f := newAuthoritySandboxFixture(t)
	var v1 []byte
	var digest string
	if f.admin.QueryRow(context.Background(), "SELECT proof_canonical,proof_sha256 FROM "+pgx.Identifier{f.pins.AppSchema.Name, "payment_runtime_attestations"}.Sanitize()).Scan(&v1, &digest) != nil {
		t.Fatal("observer genuine predecessor witness")
	}
	epoch := uuid.NewString()
	g, s, _, backend := f.installed(t, false)
	p, err := Attest(context.Background(), s)
	if err != nil {
		t.Fatal("observer sandbox plan authority")
	}
	plan, err := p.PlanFundingLots(context.Background(), epoch)
	if err != nil || g.Close(nil) != nil {
		t.Fatal("observer sandbox plan")
	}
	f.gone(t, backend)
	g, s, _, backend = f.installed(t, true)
	p, err = Attest(context.Background(), s)
	if err != nil {
		t.Fatal("observer sandbox init authority")
	}
	if _, err = p.InitializeFundingLots(context.Background(), plan.Expected); err != nil || g.Close(nil) != nil {
		t.Fatal("observer sandbox init")
	}
	f.gone(t, backend)
	g, err = Acquire(context.Background(), f.cfg, f.pins)
	if err != nil {
		t.Fatal("observer genuine sandbox independent gate")
	}
	result, err := g.ObserveFundingLots(context.Background(), plan.Expected)
	if err != nil || result.Status != FundingLotsObservationMatched || result.Observed != plan.Expected || g.Close(nil) != nil {
		t.Fatalf("observer genuine sandbox: %v", err)
	}
	var after []byte
	var afterDigest string
	if f.admin.QueryRow(context.Background(), "SELECT proof_canonical,proof_sha256 FROM "+pgx.Identifier{f.pins.AppSchema.Name, "payment_runtime_attestations"}.Sanitize()).Scan(&after, &afterDigest) != nil || !reflect.DeepEqual(v1, after) || digest != afterDigest {
		t.Fatal("observer changed original immutable predecessor")
	}
	if f.env.VerifyLegacyFundingUnchanged(context.Background()) != nil {
		t.Fatal("observer changed genuine sandbox legacy funds")
	}
}

func TestFundingLotsObservationPostgresLiveRuntimeAndHistory(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, role := range []string{f.app, f.projector} {
		c := authorityRuntime(t, f, role)
		g, _, backend := f.acquire(t, "")
		result, err := g.ObserveFundingLots(context.Background(), fundingLotsObservationExpected())
		if result != (FundingLotsObservation{}) || err != ErrIdentity {
			t.Fatal("observer accepted an actual live runtime")
		}
		if c.Close(context.Background()) != nil {
			t.Fatal("observer runtime fixture close")
		}
		f.gone(t, backend)
	}
	c := f.connect(t)
	if _, err := c.Exec(context.Background(), `UPDATE schema_migrations SET checksum=repeat('f',64) WHERE name='001_core.sql'`); err != nil {
		t.Fatal("observer history drift fixture")
	}
	g, _, backend := f.acquire(t, "")
	result, err := g.ObserveFundingLots(context.Background(), fundingLotsObservationExpected())
	if result != (FundingLotsObservation{}) || err != ErrSchema {
		t.Fatal("observer accepted changed complete migration history")
	}
	f.gone(t, backend)
}

func TestFundingLotsObservationPostgresUnknownAndRollback(t *testing.T) {
	f := newSnapshotFixture(t)
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	// A genuinely rolled back original top-level transaction releases its locks;
	// the new observer sees all 16 tables empty and makes no replay attempt.
	g, s, _, backend := authorityInstalled(t, f, true)
	if _, err := Attest(context.Background(), s); err != nil || s.Rollback(nil) != nil || g.Close(nil) != nil {
		t.Fatal("observer original known rollback")
	}
	f.gone(t, backend)
	if r := observeFundingLotsOrdinary(t, f, plan.Expected); r.Status != FundingLotsObservationUninitialized {
		t.Fatal("known rollback did not produce independent empty observation")
	}
	g, s, w, backend := authorityInstalled(t, f, true)
	p, err := Attest(context.Background(), s)
	if err != nil {
		t.Fatal("observer unknown permit")
	}
	w.mu.Lock()
	w.arm = "commit"
	w.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		result, err := p.InitializeFundingLots(context.Background(), plan.Expected)
		if !reflect.DeepEqual(result, FundingLotsInitResult{}) {
			done <- ErrTransaction
			return
		}
		done <- err
	}()
	fundingLotsAwaitReply(t, w)
	// COMMIT already reached PostgreSQL, but the old physical session still owns
	// both session gates. An independent attempt cannot start an early snapshot.
	blockedCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	blocked, blockedErr := Acquire(blockedCtx, f.cfg, f.pins)
	cancel()
	if blocked != nil || blockedErr != ErrTimeout {
		t.Fatal("observer acquired while unknown owner's session gates remained")
	}
	close(w.drop)
	if err := <-done; err != ErrCommitUnknown || s.Outcome() != OutcomeUnknown {
		t.Fatal("original lost commit reply was not retained as unknown")
	}
	f.gone(t, backend)
	result := observeFundingLotsOrdinary(t, f, plan.Expected)
	if result.Status != FundingLotsObservationMatched || result.Observed != plan.Expected || s.Outcome() != OutcomeUnknown {
		t.Fatal("independent durable observation changed original unknown result")
	}
}

func TestFundingLotsObservationPostgresCorruptEpochAndCatalog(t *testing.T) {
	for _, fault := range []string{"epoch_missing", "proof_changed", "historical_counts", "catalog_acl"} {
		t.Run(fault, func(t *testing.T) {
			f := newSnapshotFixture(t)
			plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
			fundingLotsInitOrdinary(t, f, plan.Expected)
			ctx := context.Background()
			if fault == "catalog_acl" {
				if _, err := f.admin.Exec(ctx, "GRANT SELECT ON "+pgx.Identifier{f.schema, "funding_lot_epoch"}.Sanitize()+" TO "+pgx.Identifier{f.projector}.Sanitize()); err != nil {
					t.Fatal("observer isolated catalog ACL drift")
				}
			} else {
				table := "funding_lot_epoch"
				query := "DELETE FROM " + pgx.Identifier{f.schema, table}.Sanitize()
				if fault == "proof_changed" {
					table = "funding_lot_runtime_attestations"
					query = "UPDATE " + pgx.Identifier{f.schema, table}.Sanitize() + " SET proof_canonical='{}'::bytea,proof_sha256=encode(sha256('{}'::bytea),'hex')"
				} else if fault == "historical_counts" {
					query = "UPDATE " + pgx.Identifier{f.schema, table}.Sanitize() + " SET tenant_count=tenant_count+1"
				}
				tx, err := f.admin.Begin(ctx)
				if err != nil {
					t.Fatal("observer isolated corruption transaction")
				}
				defer tx.Rollback(ctx)
				tables := []string{table}
				if fault == "proof_changed" {
					tables = append(tables, "funding_lot_epoch")
				}
				for _, name := range tables {
					if _, err := tx.Exec(ctx, "ALTER TABLE "+pgx.Identifier{f.schema, name}.Sanitize()+" DISABLE TRIGGER ALL"); err != nil {
						t.Fatal("observer isolated corruption trigger setup")
					}
				}
				if _, err := tx.Exec(ctx, query); err != nil {
					t.Fatal("observer isolated corruption setup")
				}
				if fault == "proof_changed" {
					if _, err := tx.Exec(ctx, "UPDATE "+pgx.Identifier{f.schema, "funding_lot_epoch"}.Sanitize()+" SET runtime_proof_digest=encode(sha256('{}'::bytea),'hex')"); err != nil {
						t.Fatal("observer isolated invalid strict proof with matching FK")
					}
				}
				for _, name := range tables {
					if _, err := tx.Exec(ctx, "ALTER TABLE "+pgx.Identifier{f.schema, name}.Sanitize()+" ENABLE TRIGGER ALL"); err != nil {
						t.Fatal("observer isolated corruption trigger restore")
					}
				}
				if tx.Commit(ctx) != nil {
					t.Fatal("observer isolated corruption commit")
				}
			}
			g, w, backend := f.acquire(t, "")
			result, err := g.ObserveFundingLots(ctx, plan.Expected)
			if result != (FundingLotsObservation{}) || err != ErrSchema {
				t.Fatalf("observer accepted corrupt or incompatible installation: %v", err)
			}
			f.gone(t, backend)
			assertObservationReadOnly(t, w, false)
		})
	}
}
