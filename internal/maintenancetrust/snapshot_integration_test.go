package maintenancetrust

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

func TestSnapshotPostgresOptionsFixedOrderAndOriginalProof(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, init := range []bool{false, true} {
		t.Run(map[bool]string{false: "plan", true: "init"}[init], func(t *testing.T) {
			g, w, backend := f.acquire(t, "")
			s := f.begin(t, g, init, context.Background())
			statements := snapshotAfterBegin(t, w)
			wantBegin := "begin isolation level repeatable read read only"
			wantCount := 4
			if init {
				wantBegin = "begin isolation level repeatable read read write"
				wantCount++
			}
			if len(statements) != wantCount || strings.ToLower(statements[0]) != wantBegin {
				t.Fatal("not exactly one fixed RR BEGIN before evidence")
			}
			if !strings.HasPrefix(strings.ToLower(statements[1]), "set local search_path") || !strings.Contains(statements[1], pgx.Identifier{f.schema}.Sanitize()) {
				t.Fatal("fixed safely quoted search_path SET order")
			}
			for i, name := range []string{"lock_timeout", "statement_timeout"} {
				q := strings.ToLower(statements[2+i])
				if !snapshotSQLStarts(q, "SET", "LOCAL", name) {
					t.Fatal("timeouts not fixed utility SETs")
				}
				v := strings.TrimSpace(strings.TrimPrefix(q, "set local "+name))
				v = strings.TrimSpace(strings.TrimPrefix(v, "="))
				v = strings.TrimSpace(strings.TrimPrefix(v, "to"))
				v = strings.Trim(v, "'; ")
				v = strings.TrimSuffix(v, "ms")
				n, err := strconv.Atoi(v)
				if err != nil || n <= 0 || name == "lock_timeout" && n > 3000 || name == "statement_timeout" && n > 30000 {
					t.Fatal("unbounded timeout literal")
				}
			}
			if init {
				var targets []string
				for _, name := range snapshotExpectedTables(t) {
					targets = append(targets, "ONLY "+pgx.Identifier{f.schema, name}.Sanitize())
				}
				want := "LOCK TABLE " + strings.Join(targets, ", ") + " IN SHARE ROW EXCLUSIVE MODE"
				if strings.TrimSuffix(strings.Join(strings.Fields(statements[4]), " "), ";") != want {
					t.Fatal("Init is not one complete static ONLY125 LOCK after SET and before first SELECT")
				}
				f.expectAllTableLocks(t, backend)
			} else {
				for _, q := range statements {
					if snapshotSQLStarts(q, "LOCK", "TABLE") || snapshotSQLStarts(q, "SELECT") && strings.Contains(strings.ToLower(q), "for update") {
						t.Fatal("Plan acquired table/account write locks")
					}
				}
			}
			proof, err := s.VerifyInstallation(context.Background())
			if err != nil || proof == nil || proof.VerifyTx(context.Background(), s.state.rawTx) != nil || !proof.Catalog().MatchesPins(f.pins) {
				t.Fatal("real original wrapper installation proof")
			}
			owned := s.state.installation
			if owned == nil || owned == proof {
				t.Fatal("Snapshot did not own a distinct proof wrapper")
			}
			copySnapshot := *s
			if p, err := copySnapshot.VerifyInstallation(context.Background()); p != nil || err == nil {
				t.Fatal("copied Snapshot reissued evidence")
			}
			// A caller can zero its exported wrapper without replacing the owned
			// identity. This does not create authority or a second issuer path.
			*proof = fundinglotcatalog.InstallationProof{}
			if owned.VerifyTx(context.Background(), s.state.rawTx) != nil {
				t.Fatal("caller wrapper mutation altered owned evidence")
			}
			otherConn := f.connect(t)
			other, err := otherConn.BeginTx(context.Background(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			if err != nil || owned.VerifyTx(context.Background(), other) != fundinglotcatalog.ErrCatalog {
				t.Fatal("installation rebound to another real connection")
			}
			if other.Rollback(context.Background()) != nil {
				t.Fatal("other connection rollback")
			}
			if init {
				nested, err := s.state.rawTx.Begin(context.Background())
				if err != nil || owned.VerifyTx(context.Background(), nested) != fundinglotcatalog.ErrCatalog || nested.Rollback(context.Background()) != nil {
					t.Fatal("installation rebound to nested wrapper")
				}
			}
			if copySnapshot.Rollback(context.Background()) != nil || s.Outcome() != OutcomeRolledBack || owned.VerifyTx(context.Background(), s.state.rawTx) != fundinglotcatalog.ErrCatalog {
				t.Fatal("shared terminal state/proof invalidation")
			}
			if p, err := s.VerifyInstallation(context.Background()); p != nil || err == nil {
				t.Fatal("ended Snapshot issued proof")
			}
			if next, err := g.BeginPlan(context.Background()); next != nil || err == nil {
				t.Fatal("Gate began twice")
			}
			if g.Close(nil) != nil {
				t.Fatal("confirmed rollback gate cleanup")
			}
			f.gone(t, backend)
			wantGates := []string{tryInstanceSQL, tryMigrationSQL, unlockMigrationSQL, unlockInstanceSQL}
			if !reflect.DeepEqual(gateStatements(w), wantGates) {
				t.Fatal("normal Snapshot did not retain T0 reverse cleanup")
			}
		})
	}
}

func TestSnapshotPostgresWriterBarrierCreatesSnapshotAfterLockWait(t *testing.T) {
	f := newSnapshotFixture(t)
	g, w, backend := f.acquire(t, "")
	writer := f.connect(t)
	var prior bool
	var priorRevision int64
	if writer.QueryRow(context.Background(), f.snapshotSQL(`SELECT enabled,revision FROM %s.voucher_policy`)).Scan(&prior, &priorRevision) != nil || prior || priorRevision != 1 {
		t.Fatal("writer barrier original policy")
	}
	tx, err := writer.Begin(context.Background())
	if err != nil {
		t.Fatal("writer barrier Begin")
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err = tx.Exec(context.Background(), f.snapshotSQL(`UPDATE %s.voucher_policy SET enabled=true WHERE singleton`)); err != nil {
		t.Fatal("writer barrier legitimate uncommitted change")
	}
	type result struct {
		s *Snapshot
		e error
	}
	done := make(chan result, 1)
	go func() { s, e := g.BeginInit(context.Background()); done <- result{s, e} }()
	f.waitRelationLock(t, backend, "voucher_policy", "ShareRowExclusiveLock", false)
	statements := snapshotAfterBegin(t, w)
	if len(statements) != 5 || !strings.HasPrefix(strings.ToUpper(statements[4]), "LOCK TABLE ONLY ") {
		t.Fatal("writer barrier did not wait in the complete first LOCK")
	}
	// Classify fixed leading SQL tokens. A table identifier such as
	// payment_order_capture_selections must not be mistaken for SELECT.
	wantPrefixes := [][]string{
		{"BEGIN"}, {"SET", "LOCAL", "search_path"},
		{"SET", "LOCAL", "lock_timeout"}, {"SET", "LOCAL", "statement_timeout"},
		{"LOCK", "TABLE", "ONLY"},
	}
	for i, q := range statements {
		if !snapshotSQLStarts(q, wantPrefixes[i]...) {
			t.Fatal("data snapshot established before physical lock wait")
		}
	}
	select {
	case <-done:
		t.Fatal("Init returned before writer commit")
	default:
	}
	allQueries, _ := w.recorded()
	snapshotAssertNoAddedSQL(t, w, len(allQueries), func() error { _, e := g.BeginPlan(context.Background()); return e })
	if tx.Commit(context.Background()) != nil {
		t.Fatal("writer barrier commit")
	}
	r := <-done
	if r.e != nil || r.s == nil {
		t.Fatal("Init did not finish after writer commit within original lock budget")
	}
	var enabled bool
	var revision int64
	if r.s.state.rawTx.QueryRow(context.Background(), f.snapshotSQL(`SELECT enabled,revision FROM %s.voucher_policy`)).Scan(&enabled, &revision) != nil || !enabled || revision != 2 || enabled == prior || revision == priorRevision {
		t.Fatal("RR snapshot was taken before the physical writer lock barrier")
	}
	// This proves changed source visibility; T3 alone may attest a plan basis.
	f.expectAllTableLocks(t, backend)
	if p, err := r.s.VerifyInstallation(context.Background()); err != nil || p == nil {
		t.Fatal("post-lock-wait real evidence")
	}
	if r.s.Rollback(nil) != nil || g.Close(nil) != nil {
		t.Fatal("writer barrier cleanup")
	}
	f.gone(t, backend)
}

func TestSnapshotPostgresAllTablesStopWritesButPermitSelect(t *testing.T) {
	f := newSnapshotFixture(t)
	g, _, backend := f.acquire(t, "")
	s := f.begin(t, g, true, context.Background())
	f.expectAllTableLocks(t, backend)
	readerCfg := f.cfg.Copy()
	readerCfg.User = f.app
	reader, err := pgx.ConnectConfig(context.Background(), readerCfg)
	if err != nil {
		t.Fatal("actual APP reader LOGIN")
	}
	t.Cleanup(func() { _ = reader.Close(context.Background()) })
	var enabled bool
	if reader.QueryRow(context.Background(), f.snapshotSQL(`SELECT enabled FROM %s.voucher_policy`)).Scan(&enabled) != nil || enabled {
		t.Fatal("SRE blocked ordinary APP SELECT")
	}
	writer := f.connect(t)
	f.exec(t, writer, `SET lock_timeout='100ms'`)
	for _, sql := range []string{
		`INSERT INTO %s.voucher_policy(singleton,enabled) VALUES(true,false)`,
		`UPDATE %s.voucher_policy SET enabled=true`,
		`DELETE FROM %s.voucher_policy`,
		`TRUNCATE %s.voucher_policy`,
		`ALTER TABLE %s.voucher_policy ADD COLUMN forbidden_drift integer`,
	} {
		_, err := writer.Exec(context.Background(), f.snapshotSQL(sql))
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
			t.Fatal("SRE allowed a conflicting write or DDL")
		}
	}
	if s.Rollback(nil) != nil || g.Close(nil) != nil {
		t.Fatal("stop writes cleanup")
	}
	f.gone(t, backend)
	// Plan has no SRE locks: an independent actual owner may update the source.
	g, _, backend = f.acquire(t, "")
	s = f.begin(t, g, false, context.Background())
	if _, err := writer.Exec(context.Background(), f.snapshotSQL(`UPDATE %s.voucher_policy SET enabled=true`)); err != nil {
		t.Fatal("Plan blocked a normal writer")
	}
	if s.Rollback(nil) != nil || g.Close(nil) != nil {
		t.Fatal("Plan writer cleanup")
	}
	f.gone(t, backend)
}

func TestSnapshotPostgresMissingLateTableClosesPartialBegin(t *testing.T) {
	f := newSnapshotFixture(t)
	owner := f.connect(t)
	f.exec(t, owner, f.snapshotSQL(`ALTER TABLE %s.vouchers RENAME TO missing_vouchers`))
	g, w, backend := f.acquire(t, "")
	if s, err := g.BeginInit(context.Background()); s != nil || err == nil {
		t.Fatal("missing late lock target returned half Snapshot")
	}
	f.gone(t, backend)
	select {
	case <-w.closed:
	default:
		t.Fatal("partial LOCK failure did not close physical socket")
	}
	if s, err := g.BeginPlan(context.Background()); s != nil || err == nil {
		t.Fatal("failed Begin attempt was reusable")
	}
	if g.Close(nil) != ErrCleanup {
		t.Fatal("failed partial Begin lost cleanup failure")
	}
	for _, q := range snapshotAfterBegin(t, w) {
		if snapshotSQLStarts(q, "SELECT") || q == unlockMigrationSQL || q == unlockInstanceSQL {
			t.Fatal("partial LOCK failure continued evidence/compensation")
		}
	}
}

func TestSnapshotPostgresCatalogRejectsDriftBeforeBusinessReads(t *testing.T) {
	for _, drift := range []string{"rls", "incoming_inheritance"} {
		t.Run(drift, func(t *testing.T) {
			f := newSnapshotFixture(t)
			owner := f.connect(t)
			var descendant string
			if drift == "rls" {
				f.exec(t, owner, f.snapshotSQL(`ALTER TABLE %s.ledger_accounts ENABLE ROW LEVEL SECURITY`))
			} else {
				qa := f.sentinel(t)
				descendant = pgx.Identifier{qa, "descendant"}.Sanitize()
				f.exec(t, owner, "CREATE TABLE "+descendant+" () INHERITS ("+pgx.Identifier{f.schema, "voucher_policy"}.Sanitize()+")")
			}
			g, w, backend := f.acquire(t, "")
			s := f.begin(t, g, true, context.Background())
			if descendant != "" {
				var locked bool
				if f.admin.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND relation=$2::regclass AND mode='ShareRowExclusiveLock' AND granted)`, backend.pid, descendant).Scan(&locked) != nil || locked {
					t.Fatal("ONLY lock silently expanded to an unknown descendant")
				}
			}
			if p, err := s.VerifyInstallation(context.Background()); p != nil || err == nil || s.state.installation != nil {
				t.Fatal("unreviewed catalog returned evidence")
			}
			f.gone(t, backend)
			for _, q := range snapshotAfterBegin(t, w) {
				lower := strings.ToLower(q)
				if snapshotSQLStarts(q, "SELECT") && (strings.Contains(lower, "for update") || snapshotSQLStarts(q, "SELECT", "id", "FROM") || descendant != "" && strings.Contains(q, "FROM "+descendant)) {
					t.Fatal("bad catalog reached business rows before authentication")
				}
			}
		})
	}
}

func TestSnapshotPostgresEvidenceBusyRejectsWithoutSQL(t *testing.T) {
	f := newSnapshotFixture(t)
	g, w, backend := f.acquire(t, "snapshot_catalog_stage")
	s := f.begin(t, g, false, context.Background())
	type result struct {
		p *fundinglotcatalog.InstallationProof
		e error
	}
	done := make(chan result, 1)
	go func() { p, e := s.VerifyInstallation(context.Background()); done <- result{p, e} }()
	awaitReply(t, w)
	queries, _ := w.recorded()
	before := len(queries)
	copySnapshot, copyGate := *s, *g
	for _, call := range []func() error{
		func() error { return s.Commit(context.Background()) },
		func() error { return copySnapshot.Rollback(nil) },
		func() error { return copyGate.Close(nil) },
		func() error { _, e := copyGate.BeginInit(context.Background()); return e },
		func() error { _, e := copySnapshot.VerifyInstallation(context.Background()); return e },
	} {
		snapshotAssertNoAddedSQL(t, w, before, call)
	}
	close(w.release)
	r := <-done
	if r.e != nil || r.p == nil || s.Outcome() != OutcomeOpen {
		t.Fatal("busy rejection invalidated first legitimate evidence stage")
	}
	if s.Rollback(nil) != nil || g.Close(nil) != nil {
		t.Fatal("busy evidence cleanup")
	}
	f.gone(t, backend)
}

func TestSnapshotPostgresParentCancellationClosesIdleAndActiveSQL(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle_CPU_gap", true: "catalog_SQL_reply"}[active], func(t *testing.T) {
			f := newSnapshotFixture(t)
			arm := ""
			if active {
				arm = "snapshot_catalog_stage"
			}
			g, w, backend := f.acquire(t, arm)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			s := f.begin(t, g, false, ctx)
			var done chan error
			if active {
				done = make(chan error, 1)
				go func() {
					p, e := s.VerifyInstallation(context.Background())
					if p != nil {
						done <- ErrIdentity
					} else {
						done <- e
					}
				}()
				awaitReply(t, w)
			}
			cancel()
			select {
			case <-w.closed:
			case <-time.After(500 * time.Millisecond):
				t.Fatal("parent cancellation did not raw-close during idle/CPU/SQL gap")
			}
			if active {
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("cancelled evidence returned success")
					}
				case <-time.After(time.Second):
					t.Fatal("cancelled catalog operation retained ownership")
				}
			}
			f.gone(t, backend)
			if s.Outcome() != OutcomeAbandoned {
				t.Fatal("cancel before COMMIT guessed transaction result")
			}
			before, _ := w.recorded()
			if p, err := s.VerifyInstallation(context.Background()); p != nil || err == nil {
				t.Fatal("Background revived cancelled Snapshot")
			}
			if s.Commit(context.Background()) == nil || g.Close(nil) != ErrCleanup {
				t.Fatal("cancelled Snapshot authorized commit or healthy cleanup")
			}
			after, _ := w.recorded()
			if len(after) != len(before) {
				t.Fatal("cancelled Snapshot issued extra SQL")
			}
		})
	}
}

func TestSnapshotPostgresEarlierDeadlineAndShortBudgetReject(t *testing.T) {
	f := newSnapshotFixture(t)
	g, w, backend := f.acquire(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	started := time.Now()
	s := f.begin(t, g, false, ctx)
	later, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	select {
	case <-w.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("idle Snapshot ignored original absolute deadline")
	}
	elapsed := time.Since(started)
	if elapsed < time.Second || elapsed > 1800*time.Millisecond || s.Outcome() != OutcomeAbandoned {
		t.Fatal("Snapshot reset or shortened deadline unexpectedly")
	}
	if p, err := s.VerifyInstallation(later); p != nil || err == nil {
		t.Fatal("longer later deadline revived Snapshot")
	}
	f.gone(t, backend)
	g, w, backend = f.acquire(t, "")
	short, stopShort := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer stopShort()
	before, _ := w.recorded()
	if s, err := g.BeginPlan(short); s != nil || err == nil {
		t.Fatal("remaining budget below one second accepted")
	}
	after, _ := w.recorded()
	if len(before) != len(after) {
		t.Fatal("rejected short budget sent BEGIN")
	}
	if s, err := g.BeginInit(context.Background()); s != nil || err == nil {
		t.Fatal("rejected Begin did not consume sole attempt")
	}
	if g.Close(nil) != ErrCleanup {
		t.Fatal("rejected short Begin lost cleanup failure")
	}
	f.gone(t, backend)
}

func TestSnapshotPostgresDefaultThirtySecondIdleDeadline(t *testing.T) {
	// No installation is necessary to test an idle RO lifecycle. The fixture
	// never closes the measured socket before production's watcher fires.
	f := &snapshotFixture{gateFixture: newGateFixture(t)}
	g, w, backend := f.acquire(t, "")
	started := time.Now()
	s := f.begin(t, g, false, context.Background())
	select {
	case <-w.closed:
	case <-time.After(33 * time.Second):
		t.Fatal("default idle Snapshot did not physically close")
	}
	elapsed := time.Since(started)
	if elapsed < 29*time.Second || elapsed > 33*time.Second || s.Outcome() != OutcomeAbandoned {
		t.Fatal("default Snapshot budget not thirty seconds")
	}
	f.gone(t, backend)
	if g.Close(nil) != ErrCleanup {
		t.Fatal("expired Snapshot cleanup result")
	}
}

func TestSnapshotPostgresCommitConfirmedAndDeferredRollback(t *testing.T) {
	f := newSnapshotFixture(t)
	qa := f.sentinel(t)
	for _, kind := range []string{"committed", "explicit_rollback", "deferred_constraint", "aborted_commit"} {
		t.Run(kind, func(t *testing.T) {
			g, w, backend := f.acquire(t, "")
			s := f.begin(t, g, true, context.Background())
			if _, err := s.state.rawTx.Exec(context.Background(), "INSERT INTO "+pgx.Identifier{qa, "sentinel"}.Sanitize()+"(id) VALUES($1)", kind); err != nil {
				t.Fatal("private QA transaction write")
			}
			want := OutcomeCommitted
			if kind == "deferred_constraint" {
				if _, err := s.state.rawTx.Exec(context.Background(), "INSERT INTO "+pgx.Identifier{qa, "child"}.Sanitize()+"(id,parent) VALUES('deferred','missing')"); err != nil {
					t.Fatal("deferred QA constraint failed before COMMIT")
				}
			} else if kind == "aborted_commit" {
				if _, err := s.state.rawTx.Exec(context.Background(), "SELECT 1/0"); err == nil {
					t.Fatal("QA transaction was not aborted")
				}
			}
			var err error
			if kind == "explicit_rollback" {
				cancelled, cancel := context.WithCancel(context.Background())
				cancel()
				err = s.Rollback(cancelled)
				want = OutcomeRolledBack
			} else {
				err = s.Commit(context.Background())
				if kind != "committed" {
					want = OutcomeRolledBack
				}
			}
			if want == OutcomeCommitted && err != nil || kind == "explicit_rollback" && err != nil || kind == "deferred_constraint" && err == nil || kind == "aborted_commit" && err == nil || s.Outcome() != want {
				t.Fatal("explicit COMMIT/ROLLBACK evidence classified incorrectly")
			}
			wantCount := 0
			if want == OutcomeCommitted {
				wantCount = 1
			}
			if f.sentinelCount(t, kind) != wantCount {
				t.Fatal("independent durable QA fact differs from outcome")
			}
			before, _ := w.recorded()
			copySnapshot := *s
			_ = copySnapshot.Commit(context.Background())
			_ = s.Rollback(nil)
			after, _ := w.recorded()
			if len(before) != len(after) || copySnapshot.Outcome() != want {
				t.Fatal("terminal copy retried SQL or changed confirmed outcome")
			}
			if g.Close(nil) != nil || s.Outcome() != want {
				t.Fatal("normal cleanup lost confirmed outcome")
			}
			f.gone(t, backend)
		})
	}
}

func TestSnapshotPostgresCommitLostReplyIsUnknownWithoutRetry(t *testing.T) {
	f := newSnapshotFixture(t)
	qa := f.sentinel(t)
	g, w, backend := f.acquire(t, "commit")
	s := f.begin(t, g, true, context.Background())
	if _, err := s.state.rawTx.Exec(context.Background(), "INSERT INTO "+pgx.Identifier{qa, "sentinel"}.Sanitize()+"(id) VALUES('commit-lost-reply')"); err != nil {
		t.Fatal("private QA unknown-commit write")
	}
	done := make(chan error, 1)
	go func() { done <- s.Commit(context.Background()) }()
	awaitReply(t, w)
	// Before dropping only the driver's reply, independently prove the server
	// durably committed. EOF before execution cannot satisfy this oracle.
	if f.sentinelCount(t, "commit-lost-reply") != 1 {
		t.Fatal("server COMMIT did not precede intercepted reply")
	}
	queries, _ := w.recorded()
	snapshotAssertNoAddedSQL(t, w, len(queries), func() error { return g.Close(nil) })
	close(w.drop)
	if err := <-done; err != ErrCommitUnknown || s.Outcome() != OutcomeUnknown {
		t.Fatal("lost committed reply was not unknown")
	}
	select {
	case <-w.closed:
	default:
		t.Fatal("unknown COMMIT did not physically close socket")
	}
	f.gone(t, backend)
	before, _ := w.recorded()
	copySnapshot := *s
	_ = copySnapshot.Commit(context.Background())
	_ = s.Rollback(nil)
	_ = g.Close(nil)
	after, _ := w.recorded()
	if len(after) != len(before) || s.Outcome() != OutcomeUnknown || copySnapshot.Outcome() != OutcomeUnknown {
		t.Fatal("unknown result retried/rolled back or changed after cleanup")
	}
	commits := 0
	for _, q := range snapshotAfterBegin(t, w) {
		if strings.EqualFold(q, "commit") {
			commits++
		}
		if strings.EqualFold(q, "rollback") || q == unlockInstanceSQL || q == unlockMigrationSQL {
			t.Fatal("unknown COMMIT attempted compensation")
		}
	}
	if commits != 1 {
		t.Fatal("unknown COMMIT was not exactly once")
	}
}

func TestSnapshotPostgresRollbackLostReplyAbandons(t *testing.T) {
	f := newSnapshotFixture(t)
	qa := f.sentinel(t)
	g, w, backend := f.acquire(t, "rollback")
	s := f.begin(t, g, true, context.Background())
	if _, err := s.state.rawTx.Exec(context.Background(), "INSERT INTO "+pgx.Identifier{qa, "sentinel"}.Sanitize()+"(id) VALUES('rollback-lost-reply')"); err != nil {
		t.Fatal("private QA rollback write")
	}
	done := make(chan error, 1)
	go func() { done <- s.Rollback(nil) }()
	awaitReply(t, w)
	f.expectBackendIdle(t, backend)
	if f.sentinelCount(t, "rollback-lost-reply") != 0 {
		t.Fatal("server did not rollback before lost reply")
	}
	close(w.drop)
	if err := <-done; err != ErrCleanup || s.Outcome() != OutcomeAbandoned {
		t.Fatal("lost ROLLBACK response guessed confirmed outcome")
	}
	select {
	case <-w.closed:
	default:
		t.Fatal("failed ROLLBACK did not raw-close")
	}
	f.gone(t, backend)
	before, _ := w.recorded()
	_ = s.Rollback(nil)
	_ = g.Close(nil)
	after, _ := w.recorded()
	if len(after) != len(before) || s.Outcome() != OutcomeAbandoned {
		t.Fatal("failed ROLLBACK retried/compensated")
	}
}

func TestSnapshotPostgresCloseOpenSnapshotAbandonsWithoutRollback(t *testing.T) {
	f := newSnapshotFixture(t)
	g, w, backend := f.acquire(t, "")
	s := f.begin(t, g, false, context.Background())
	before, _ := w.recorded()
	if g.Close(nil) != ErrCleanup || s.Outcome() != OutcomeAbandoned {
		t.Fatal("Close of an open Snapshot guessed healthy cleanup")
	}
	after, _ := w.recorded()
	if len(after) != len(before) {
		t.Fatal("Gate.Close issued implicit transaction control/unlock")
	}
	f.gone(t, backend)
}

func TestSnapshotPostgresCallerCancellationCannotBeIgnored(t *testing.T) {
	f := newSnapshotFixture(t)
	g, w, backend := f.acquire(t, "snapshot_catalog_stage")
	s := f.begin(t, g, false, context.Background())
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		p, err := s.VerifyInstallation(caller)
		if p != nil {
			done <- ErrIdentity
		} else {
			done <- err
		}
	}()
	awaitReply(t, w)
	cancel()
	select {
	case err := <-done:
		if err != ErrTimeout {
			t.Fatal("caller cancellation did not invalidate evidence")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled caller SQL retained stage ownership")
	}
	select {
	case <-w.closed:
	default:
		t.Fatal("caller cancellation did not force physical close")
	}
	f.gone(t, backend)
	if p, err := s.VerifyInstallation(context.Background()); p != nil || err == nil || s.Outcome() != OutcomeAbandoned {
		t.Fatal("later Background revived caller-invalidated Snapshot")
	}
}

func TestSnapshotPostgresRollbackIndependentFiveSecondBudget(t *testing.T) {
	f := newSnapshotFixture(t)
	g, w, backend := f.acquire(t, "rollback")
	s := f.begin(t, g, true, context.Background())
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- s.Rollback(cancelled) }()
	awaitReply(t, w)
	f.expectBackendIdle(t, backend)
	queries, _ := w.recorded()
	snapshotAssertNoAddedSQL(t, w, len(queries), func() error { return s.Commit(context.Background()) })
	if err := <-done; err != ErrCleanup || s.Outcome() != OutcomeAbandoned {
		t.Fatal("unconfirmed ROLLBACK did not abandon")
	}
	elapsed := time.Since(started)
	if elapsed < 4500*time.Millisecond || elapsed > 6500*time.Millisecond {
		t.Fatal("ROLLBACK ignored independent total five-second cleanup budget")
	}
	select {
	case <-w.closed:
	default:
		t.Fatal("ROLLBACK timeout did not raw-close")
	}
	f.gone(t, backend)
	for _, q := range gateStatements(w) {
		if q == unlockMigrationSQL || q == unlockInstanceSQL {
			t.Fatal("unconfirmed ROLLBACK attempted gate compensation")
		}
	}
}

func TestSnapshotPostgresCommitCallerAlreadyCancelledSendsNothing(t *testing.T) {
	f := newSnapshotFixture(t)
	qa := f.sentinel(t)
	g, w, backend := f.acquire(t, "")
	s := f.begin(t, g, true, context.Background())
	if _, err := s.state.rawTx.Exec(context.Background(), "INSERT INTO "+pgx.Identifier{qa, "sentinel"}.Sanitize()+"(id) VALUES('cancel-before-commit')"); err != nil {
		t.Fatal("private QA cancelled-commit write")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before, _ := w.recorded()
	if err := s.Commit(ctx); err != ErrTimeout || s.Outcome() != OutcomeAbandoned {
		t.Fatal("already cancelled Commit guessed a sent or confirmed result")
	}
	after, _ := w.recorded()
	if len(after) != len(before) || f.sentinelCount(t, "cancel-before-commit") != 0 {
		t.Fatal("cancelled Commit sent SQL or persisted QA write")
	}
	f.gone(t, backend)
}

func TestSnapshotPostgresCommitReplyCancellationRetainsUnknown(t *testing.T) {
	f := newSnapshotFixture(t)
	qa := f.sentinel(t)
	g, w, backend := f.acquire(t, "commit")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := f.begin(t, g, true, ctx)
	if _, err := s.state.rawTx.Exec(context.Background(), "INSERT INTO "+pgx.Identifier{qa, "sentinel"}.Sanitize()+"(id) VALUES('cancel-after-commit')"); err != nil {
		t.Fatal("private QA post-send cancelled-commit write")
	}
	done := make(chan error, 1)
	go func() { done <- s.Commit(context.Background()) }()
	awaitReply(t, w)
	if f.sentinelCount(t, "cancel-after-commit") != 1 {
		t.Fatal("COMMIT did not persist before cancellation")
	}
	cancel()
	select {
	case err := <-done:
		if err != ErrCommitUnknown || s.Outcome() != OutcomeUnknown {
			t.Fatal("cancel after COMMIT send did not preserve unknown")
		}
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not interrupt COMMIT reply")
	}
	f.gone(t, backend)
	before, _ := w.recorded()
	if g.Close(nil) != ErrCleanup || s.Outcome() != OutcomeUnknown {
		t.Fatal("cleanup masked unknown commit")
	}
	after, _ := w.recorded()
	if len(before) != len(after) {
		t.Fatal("unknown cancellation triggered compensation")
	}
}

// Production emits fixed, uncommented statements. Compare their leading SQL
// keywords rather than searching identifier or literal substrings.
func snapshotSQLStarts(sql string, prefix ...string) bool {
	words := strings.Fields(sql)
	if len(words) < len(prefix) {
		return false
	}
	for i, want := range prefix {
		if !strings.EqualFold(words[i], want) {
			return false
		}
	}
	return true
}
