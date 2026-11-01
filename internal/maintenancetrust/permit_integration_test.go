package maintenancetrust

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/migrations"
)

func authorityInstalled(t *testing.T, f *snapshotFixture, init bool) (*Gate, *Snapshot, *gateWire, observedBackend) {
	t.Helper()
	g, w, b := f.acquire(t, "")
	s := f.begin(t, g, init, context.Background())
	proof, err := s.VerifyInstallation(context.Background())
	if err != nil || proof == nil {
		t.Fatal("authority original installation stage")
	}
	*proof = fundinglotcatalog.InstallationProof{}
	return g, s, w, b
}

func authorityRuntime(t *testing.T, f *snapshotFixture, role string) *pgx.Conn {
	t.Helper()
	cfg := f.cfg.Copy()
	cfg.User = role
	c, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal("authority actual runtime login")
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func TestAuthorityPostgresOrdinaryPlanInitAndTerminalRevocation(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, init := range []bool{false, true} {
		for _, method := range []string{"commit", "rollback", "close"} {
			t.Run(fmt.Sprintf("init=%v/%s", init, method), func(t *testing.T) {
				g, s, w, b := authorityInstalled(t, f, init)
				p, err := Attest(context.Background(), s)
				if err != nil || p == nil {
					t.Fatalf("real ordinary authority: %v", err)
				}
				copyP := *p
				if p.Verify(context.Background()) != nil || copyP.Verify(context.Background()) != nil {
					t.Fatal("issued permit cannot verify")
				}
				a := p.state.authority
				if a.active || a.manifest == nil || a.manifest.digest != authorityManifestSHA || a.predecessor == nil || a.predecessor.v1SHA256 != "" || (a.accounts != nil) != init {
					t.Fatal("incomplete fixed authority facts")
				}
				queries, _ := w.recorded()
				before := len(queries)
				if next, err := Attest(context.Background(), s); next != nil || err != ErrTransaction {
					t.Fatal("second authority issued")
				}
				after, _ := w.recorded()
				if len(after) != before || p.Verify(context.Background()) != nil {
					t.Fatal("duplicate attest queried or revoked original success")
				}
				switch method {
				case "commit":
					if s.Commit(context.Background()) != nil || s.Outcome() != OutcomeCommitted {
						t.Fatal("authority commit")
					}
				case "rollback":
					if s.Rollback(context.Background()) != nil || s.Outcome() != OutcomeRolledBack {
						t.Fatal("authority rollback")
					}
				case "close":
					if g.Close(nil) != ErrCleanup || s.Outcome() != OutcomeAbandoned {
						t.Fatal("authority abandoned close")
					}
				}
				if p.Verify(context.Background()) == nil || copyP.Verify(context.Background()) == nil || !p.state.revoked {
					t.Fatal("terminal retained authority")
				}
				if method != "close" && g.Close(nil) != nil {
					t.Fatal("authority terminal cleanup")
				}
				f.gone(t, b)
			})
		}
	}
}

func TestAuthorityPostgresFreshRuntimeSampling(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, role := range []string{f.app, f.projector} {
		t.Run(role[len(f.tag):]+"/new-after-T1", func(t *testing.T) {
			g, s, _, b := authorityInstalled(t, f, false)
			c := authorityRuntime(t, f, role)
			if p, err := Attest(context.Background(), s); p != nil || err != ErrIdentity {
				t.Fatalf("new runtime escaped cached activity: %v", err)
			}
			if c.Close(context.Background()) != nil {
				t.Fatal("runtime close")
			}
			_ = g.Close(nil)
			f.gone(t, b)
		})
		t.Run(role[len(f.tag):]+"/disconnect-after-T1", func(t *testing.T) {
			c := authorityRuntime(t, f, role)
			runtime := observedBackend{pid: c.PgConn().PID()}
			if f.admin.QueryRow(context.Background(), `SELECT backend_start FROM pg_catalog.pg_stat_activity WHERE pid=$1`, runtime.pid).Scan(&runtime.start) != nil {
				t.Fatal("runtime identity")
			}
			g, s, _, b := authorityInstalled(t, f, false)
			if c.Close(context.Background()) != nil {
				t.Fatal("runtime disconnect")
			}
			f.gone(t, runtime)
			p, err := Attest(context.Background(), s)
			if err != nil || p == nil {
				t.Fatalf("stale activity rejected departed runtime: %v", err)
			}
			if s.Rollback(context.Background()) != nil || g.Close(nil) != nil {
				t.Fatal("fresh sampling cleanup")
			}
			f.gone(t, b)
		})
	}
	t.Run("verify-new-runtime", func(t *testing.T) {
		g, s, _, b := authorityInstalled(t, f, false)
		p, err := Attest(context.Background(), s)
		if err != nil || p == nil {
			t.Fatal("verify runtime authority")
		}
		copyP := *p
		c := authorityRuntime(t, f, f.app)
		if p.Verify(context.Background()) != ErrIdentity || copyP.Verify(context.Background()) != ErrTransaction {
			t.Fatal("new runtime did not revoke permit copies")
		}
		_ = c.Close(context.Background())
		_ = g.Close(nil)
		f.gone(t, b)
	})
}

func TestAuthorityPostgresHistoryGuardAndSameSnapshot(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, tc := range []struct{ name, sql string }{
		{"missing", `DELETE FROM %s.schema_migrations WHERE name=(SELECT min(name) FROM %s.schema_migrations)`},
		{"bad-checksum", `UPDATE %s.schema_migrations SET checksum=repeat('0',64)`},
		{"large-name", `UPDATE %s.schema_migrations SET name=repeat('x',2048) WHERE name=(SELECT min(name) FROM %s.schema_migrations)`},
		{"large-checksum", `UPDATE %s.schema_migrations SET checksum=repeat('x',1000000)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A controlled same-RR write is test-only. It proves T2 actually reads
			// history through its own raw transaction, beyond the T1 static proof.
			g, s, w, b := authorityInstalled(t, f, true)
			q := strings.ReplaceAll(tc.sql, "%s", pgx.Identifier{f.schema}.Sanitize())
			if _, err := s.state.rawTx.Exec(context.Background(), q); err != nil {
				t.Fatal("history controlled fixture")
			}
			before, _ := w.recorded()
			if p, err := Attest(context.Background(), s); p != nil || err != ErrSchema {
				t.Fatalf("invalid full history accepted: %v", err)
			}
			after, _ := w.recorded()
			if len(after) <= len(before) {
				t.Fatal("history not queried")
			}
			if strings.HasPrefix(tc.name, "large-") || tc.name == "missing" {
				for _, q := range after[len(before):] {
					if strings.HasPrefix(q, "SELECT name,checksum FROM schema_migrations") {
						t.Fatal("unbounded helper ran before scalar qualification")
					}
				}
			}
			_ = g.Close(nil)
			f.gone(t, b)
		})
	}
	t.Run("concurrent-new-history-invisible", func(t *testing.T) {
		g, s, _, b := authorityInstalled(t, f, false)
		c := f.connect(t)
		f.exec(t, c, f.snapshotSQL(`UPDATE %s.schema_migrations SET checksum=repeat('0',64)`))
		p, err := Attest(context.Background(), s)
		if p == nil || err != nil {
			t.Fatalf("T2 left original RR: %v", err)
		}
		if s.Rollback(context.Background()) != nil || g.Close(nil) != nil {
			t.Fatal("same snapshot rollback")
		}
		f.gone(t, b)
		// Restore from the original embed only for this isolated fixture.
		for _, entry := range authorityFixtureManifest(t) {
			if _, err := c.Exec(context.Background(), f.snapshotSQL(`UPDATE %s.schema_migrations SET checksum=$1 WHERE name=$2`), entry.SHA256, entry.Name); err != nil {
				t.Fatal("history fixture restoration")
			}
		}
	})
}

func authorityFixtureManifest(t *testing.T) []authorityManifestEntry {
	t.Helper()
	b, _, err := authorityManifestCanonical(migrations.Files)
	var entries []authorityManifestEntry
	if err != nil || json.Unmarshal(b, &entries) != nil {
		t.Fatal("fixture manifest")
	}
	return entries
}

func TestAuthorityPostgresAllAccountLocksAndPlanNoLocks(t *testing.T) {
	// Original TEXT IDs are not restricted to UUID or 64 bytes.
	ids := []string{"", "A", "a", strings.Repeat("z", 4096)}
	f := newSnapshotFixturePrepared(t, func(f *snapshotFixture, pool *pgxpool.Pool) {
		for i, id := range ids {
			tenant := fmt.Sprintf("authority-tenant-%d", i)
			if _, err := pool.Exec(context.Background(), f.snapshotSQL(`INSERT INTO %s.tenants(id,name) VALUES($1,$1)`), tenant); err != nil {
				t.Fatal("account tenant fixture")
			}
			if _, err := pool.Exec(context.Background(), f.snapshotSQL(`INSERT INTO %s.ledger_accounts(id,tenant_id,kind) VALUES($1,$2,'AVAILABLE')`), id, tenant); err != nil {
				t.Fatal("account fixture")
			}
		}
	})
	owner := f.connect(t)
	for _, init := range []bool{false, true} {
		t.Run(fmt.Sprint(init), func(t *testing.T) {
			g, s, w, b := authorityInstalled(t, f, init)
			p, err := Attest(context.Background(), s)
			if p == nil || err != nil {
				t.Fatalf("account authority: %v", err)
			}
			queries, _ := w.recorded()
			var locks []string
			for _, q := range queries {
				if strings.Contains(q, `ORDER BY id COLLATE pg_catalog."C" FOR UPDATE`) {
					locks = append(locks, q)
				}
			}
			if !init {
				if len(locks) != 0 || p.state.authority.accounts != nil {
					t.Fatal("plan acquired account locks")
				}
				var id string
				if err := owner.QueryRow(context.Background(), f.snapshotSQL(`SELECT id FROM %s.ledger_accounts WHERE id='A' FOR UPDATE NOWAIT`)).Scan(&id); err != nil || id != "A" {
					t.Fatal("plan held account row lock")
				}
			} else {
				if len(locks) != 1 || locks[0] != ("SELECT id FROM "+pgx.Identifier{f.schema, "ledger_accounts"}.Sanitize()+` ORDER BY id COLLATE pg_catalog."C" FOR UPDATE`) {
					t.Fatal("account SQL not exact single full lock")
				}
				// Independently encode a fresh preexisting set rather than invoke the
				// production digest implementation as its own oracle.
				rows, err := f.admin.Query(context.Background(), f.snapshotSQL(`SELECT id FROM %s.ledger_accounts`))
				if err != nil {
					t.Fatal("account oracle")
				}
				var want []string
				for rows.Next() {
					var id string
					if rows.Scan(&id) != nil {
						t.Fatal("account oracle scan")
					}
					want = append(want, id)
				}
				rows.Close()
				if rows.Err() != nil {
					t.Fatal("account oracle EOF")
				}
				sort.Strings(want)
				h := sha256.New()
				for _, id := range want {
					var b [8]byte
					binary.BigEndian.PutUint64(b[:], uint64(len(id)))
					h.Write(b[:])
					h.Write([]byte(id))
				}
				e := p.state.authority.accounts
				if e.count != uint64(len(want)) || !reflect.DeepEqual(e.digest[:], h.Sum(nil)) {
					t.Fatal("account complete digest mismatch")
				}
				// Observe actual tuple conflicts without a writer's table lock hiding
				// which individual row was locked. RowShare is compatible with SRE.
				for _, id := range want {
					tx, err := owner.Begin(context.Background())
					if err != nil {
						t.Fatal("account lock observer")
					}
					var observed string
					err = tx.QueryRow(context.Background(), f.snapshotSQL(`SELECT id FROM %s.ledger_accounts WHERE id=$1 FOR UPDATE NOWAIT`), id).Scan(&observed)
					if err == nil {
						t.Fatal("account row was not locked")
					}
					if tx.Rollback(context.Background()) != nil {
						t.Fatal("account observer rollback")
					}
				}
			}
			if s.Rollback(context.Background()) != nil || g.Close(nil) != nil {
				t.Fatal("account authority cleanup")
			}
			f.gone(t, b)
		})
	}
}

func TestAuthorityPostgresBusyCancellationAndExitRuntime(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, mode := range []string{"cancel", "exit-runtime", "sub-budget"} {
		t.Run(mode, func(t *testing.T) {
			g, s, w, b := authorityInstalled(t, f, false)
			// The full ordinary reader reaches this bounded policy projection after
			// entry runtime sampling. Hold its physical reply, not a fake Tx.
			snapshotArm(w, predecessorProjectionSQL(f.schema, predecessorPolicyTable))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				p   *Permit
				err error
			}
			done := make(chan result, 1)
			go func() { p, err := Attest(ctx, s); done <- result{p, err} }()
			select {
			case <-w.reply:
			case <-time.After(10 * time.Second):
				t.Fatal("authority physical policy barrier")
			}
			before, _ := w.recorded()
			snapshotAssertNoAddedSQL(t, w, len(before), func() error { _, e := Attest(context.Background(), s); return e })
			snapshotAssertNoAddedSQL(t, w, len(before), func() error { return s.Commit(context.Background()) })
			snapshotAssertNoAddedSQL(t, w, len(before), func() error { return s.Rollback(context.Background()) })
			snapshotAssertNoAddedSQL(t, w, len(before), func() error { return g.Close(nil) })
			want := ErrTimeout
			if mode == "cancel" {
				cancel()
			} else if mode == "exit-runtime" {
				c := authorityRuntime(t, f, f.projector)
				defer c.Close(context.Background())
				close(w.release)
				want = ErrIdentity
			}
			select {
			case r := <-done:
				if r.p != nil || r.err != want {
					t.Fatalf("authority exit should reject: %v", r.err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("authority cancellation/exit hung")
			}
			_ = g.Close(nil)
			f.gone(t, b)
		})
	}
}
