package maintenancetrust

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestAuthoritySandboxPostgresPlanInitAndOriginalV1(t *testing.T) {
	f := newAuthoritySandboxFixture(t)
	var original []byte
	var originalSHA string
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if f.admin.QueryRow(ctx, "SELECT proof_canonical,proof_sha256 FROM "+pgx.Identifier{f.env.Schemas["app"], "payment_runtime_attestations"}.Sanitize()).Scan(&original, &originalSHA) != nil {
		t.Fatal("sandbox authority original v1 bytes")
	}
	sum := sha256.Sum256(original)
	if len(original) == 0 || hex.EncodeToString(sum[:]) != originalSHA {
		t.Fatal("sandbox authority original v1 SHA mismatch")
	}
	count, digest := f.accountWitness(t)
	if count == 0 {
		t.Fatal("genuine sandbox must retain seeded funding accounts")
	}
	for _, init := range []bool{false, true} {
		t.Run(fmt.Sprintf("init=%v", init), func(t *testing.T) {
			g, s, _, b := f.installed(t, init)
			p, err := Attest(context.Background(), s)
			if err != nil || p == nil {
				t.Fatalf("genuine sandbox authority: %v", err)
			}
			a := p.state.authority
			if a.active || a.predecessor == nil || a.predecessor.v1SHA256 != originalSHA || !bytes.Equal(a.predecessor.canonical, original) || a.manifest == nil || a.manifest.digest != authorityManifestSHA || a.identity == nil || a.identity.loginName != "moni_test" {
				t.Fatal("sandbox authority lost original v1 or issuer facts")
			}
			if init {
				if a.accounts == nil || !a.accounts.complete || a.accounts.count != count || a.accounts.digest != digest {
					t.Fatal("sandbox Init did not cover all real funding accounts")
				}
			} else if a.accounts != nil {
				t.Fatal("sandbox Plan acquired Init account authority")
			}
			copyP := *p
			if p.Verify(context.Background()) != nil || copyP.Verify(context.Background()) != nil {
				t.Fatal("genuine sandbox permit verification")
			}
			if s.Rollback(context.Background()) != nil || s.Outcome() != OutcomeRolledBack || g.Close(nil) != nil {
				t.Fatal("sandbox authority rollback and exact Close")
			}
			f.gone(t, b)
			if p.Verify(context.Background()) != ErrTransaction || copyP.Verify(context.Background()) != ErrTransaction {
				t.Fatal("sandbox terminal permit copy retained authority")
			}
			if f.env.VerifyLegacyFundingUnchanged(context.Background()) != nil {
				t.Fatal("sandbox maintenance changed legacy funds")
			}
			f.runtimesGone(t)
		})
	}
}

func (f *authoritySandboxFixture) accountWitness(t *testing.T) (uint64, [32]byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	rows, err := f.admin.Query(ctx, "SELECT id FROM "+pgx.Identifier{f.env.Schemas["app"], "ledger_accounts"}.Sanitize()+` ORDER BY id COLLATE pg_catalog."C"`)
	if err != nil {
		t.Fatal("sandbox account witness query")
	}
	defer rows.Close()
	var count uint64
	h := sha256.New()
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			t.Fatal("sandbox account witness scan")
		}
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(id)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(id))
		count++
	}
	if rows.Err() != nil {
		t.Fatal("sandbox account witness EOF")
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return count, digest
}

func TestAuthoritySandboxPostgresFiveFreshRuntimeRoles(t *testing.T) {
	f := newAuthoritySandboxFixture(t)
	for _, r := range []struct{ component, role string }{
		{"app", "app_runtime"}, {"worker", "app_worker"}, {"projector", "app_projector"}, {"mock", "mock_runtime"}, {"receiver", "receiver_runtime"},
	} {
		t.Run(r.component+"/connect-after-T1", func(t *testing.T) {
			// T1's original catalog calls have already populated the activity
			// snapshot. Each runtime now connects under its own real LOGIN.
			g, s, _, b := f.installed(t, false)
			runtime, rb := f.runtime(t, r.component, r.role)
			p, err := Attest(context.Background(), s)
			if p != nil || err != ErrIdentity {
				t.Fatalf("sandbox live runtime escaped fresh stop sample: %v", err)
			}
			// Rejection must physically close the exact maintenance socket.
			f.gone(t, b)
			runtime.Close()
			f.gone(t, rb)
			_ = g.Close(nil)
			f.runtimesGone(t)
		})
		t.Run(r.component+"/disconnect-after-T1", func(t *testing.T) {
			runtime, rb := f.runtime(t, r.component, r.role)
			g, s, _, b := f.installed(t, false)
			runtime.Close()
			f.gone(t, rb)
			p, err := Attest(context.Background(), s)
			if p == nil || err != nil {
				t.Fatalf("sandbox departed runtime remained in cached sample: %v", err)
			}
			if s.Rollback(context.Background()) != nil || g.Close(nil) != nil {
				t.Fatal("sandbox departed runtime cleanup")
			}
			f.gone(t, b)
		})
	}
	for _, r := range []struct{ component, role string }{{"worker", "app_worker"}, {"mock", "mock_runtime"}, {"receiver", "receiver_runtime"}} {
		t.Run(r.component+"/connect-after-Permit", func(t *testing.T) {
			g, s, _, b := f.installed(t, false)
			p, err := Attest(context.Background(), s)
			if err != nil || p == nil {
				t.Fatal("sandbox verify runtime initial authority")
			}
			copyP := *p
			runtime, rb := f.runtime(t, r.component, r.role)
			if p.Verify(context.Background()) != ErrIdentity || copyP.Verify(context.Background()) != ErrTransaction {
				t.Fatal("sandbox new runtime did not revoke permit copies")
			}
			f.gone(t, b)
			runtime.Close()
			f.gone(t, rb)
			_ = g.Close(nil)
		})
	}
}

func TestAuthoritySandboxPostgresPredecessorContentRefusals(t *testing.T) {
	f := newAuthoritySandboxFixture(t)
	for _, tc := range []struct {
		name, component, table, column, replacement, cast string
	}{
		{"api-token-not-v1", "mock", "mock_environment", "api_token_digest", `CASE WHEN api_token_digest<>repeat('a',64) AND control_token_digest<>repeat('a',64) THEN repeat('a',64) WHEN api_token_digest<>repeat('b',64) AND control_token_digest<>repeat('b',64) THEN repeat('b',64) ELSE repeat('c',64) END`, "text"},
		{"worker-nonce-not-v1", "app", "payment_runtime_attestations", "worker_nonce_digest", `CASE WHEN worker_nonce_digest=repeat('a',64) THEN repeat('b',64) ELSE repeat('a',64) END`, "text"},
		{"oversized-peer-name", "mock", "mock_environment", "app_schema_name", `repeat('x',2097152)`, "text"},
		{"receiver-created-infinity", "receiver", "receiver_environment", "created_at", `'infinity'::timestamptz`, "timestamptz"},
		{"mock-applied-infinity", "mock", "mock_schema_migrations", "applied_at", `'infinity'::timestamptz`, "timestamptz"},
		{"receiver-applied-infinity", "receiver", "receiver_schema_migrations", "applied_at", `'-infinity'::timestamptz`, "timestamptz"},
		{"mock-key-created-infinity", "mock", "mock_signing_keys", "created_at", `'infinity'::timestamptz`, "timestamptz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relation := pgx.Identifier{f.env.Schemas[tc.component], tc.table}.Sanitize()
			column := pgx.Identifier{tc.column}.Sanitize()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var original string
			if f.admin.QueryRow(ctx, "SELECT "+column+"::text FROM "+relation).Scan(&original) != nil {
				t.Fatal("sandbox predecessor original scalar witness")
			}
			// A genuine committed corrupt-history fixture preserves all frozen
			// relation/trigger/ACL descriptors. Disabling trigger execution is
			// strictly transaction-local to this test administrator mutation.
			f.replaceScalar(t, relation, column, tc.replacement)
			defer f.restoreScalar(t, relation, column, original, tc.cast)
			g, s, w, b := f.installed(t, false)
			before, _ := w.recorded()
			p, err := Attest(context.Background(), s)
			if p != nil || err != ErrIdentity {
				t.Fatalf("sandbox corrupt predecessor accepted: %v", err)
			}
			after, _ := w.recorded()
			queried := false
			for _, q := range after[len(before):] {
				if strings.Contains(q, "FROM ONLY "+relation+" s") && strings.Contains(q, "CASE WHEN q.qualified THEN") {
					queried = true
				}
			}
			if !queried {
				t.Fatal("predecessor refusal was masked by an earlier static check")
			}
			f.gone(t, b)
			_ = g.Close(nil)
		})
	}
	if f.env.VerifyLegacyFundingUnchanged(context.Background()) != nil {
		t.Fatal("predecessor refusal changed legacy funds")
	}
	f.runtimesGone(t)
}

func (f *authoritySandboxFixture) replaceScalar(t *testing.T, relation, column, expression string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		t.Fatal("sandbox corruption transaction")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SET LOCAL session_replication_role = 'replica'"); err != nil {
		t.Fatal("sandbox test-only immutable fixture update scope")
	}
	result, err := tx.Exec(ctx, "UPDATE "+relation+" SET "+column+"="+expression)
	if err != nil || result.RowsAffected() != 1 || tx.Commit(ctx) != nil {
		t.Fatal("sandbox corrupt predecessor fixture commit")
	}
}

func (f *authoritySandboxFixture) restoreScalar(t *testing.T, relation, column, value, cast string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		t.Error("sandbox scalar restoration transaction")
		return
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SET LOCAL session_replication_role = 'replica'"); err != nil {
		t.Error("sandbox scalar restoration scope")
		return
	}
	result, err := tx.Exec(ctx, "UPDATE "+relation+" SET "+column+"=$1::"+cast, pgx.QueryExecModeSimpleProtocol, value)
	if err != nil || result.RowsAffected() != 1 || tx.Commit(ctx) != nil {
		t.Error("sandbox scalar exact restoration")
	}
}
