package periodicbudget

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestVerifierConstructionAndProtocol(t *testing.T) {
	if _, e := NewVerifier(nil, VerifyOptions{}); !errors.Is(e, ErrVerifyInput) {
		t.Fatal("nil pool accepted")
	}
	for _, timeout := range []time.Duration{-1, time.Second - 1, 10*time.Minute + 1} {
		if _, e := NewVerifier(new(pgxpool.Pool), VerifyOptions{timeout}); !errors.Is(e, ErrVerifyInput) {
			t.Fatal("invalid timeout accepted")
		}
	}
	var v *Verifier
	if got, e := v.Verify(context.Background()); !errors.Is(e, ErrVerifyInput) || got != (Verification{}) {
		t.Fatal("nil verifier not rejected before DB")
	}
	v, _ = NewVerifier(new(pgxpool.Pool), VerifyOptions{})
	if _, e := v.Verify(nil); !errors.Is(e, ErrVerifyInput) {
		t.Fatal("nil context accepted")
	}
	raw, e := json.Marshal(Verification{SchemaVersion: 1, AsOf: "2026-11-01T00:00:00.000000Z", Status: VerificationBalanced, Balanced: true, CoverageComplete: true})
	if e != nil || !strings.Contains(string(raw), `"schema_version":1`) || !strings.Contains(string(raw), `"source_identity_mismatches":"0"`) || !strings.Contains(string(raw), `"customer_sources":"0"`) {
		t.Fatal("report lost canonical count protocol")
	}
}

func TestVerifierOperationStaticProofAndMalformedReceipts(t *testing.T) {
	p := func(s string) *string { return &s }
	enabled := true
	o := verifierOperation{id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", kind: "tenant_periodic_budget.configure", actor: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", scope: "platform", target: "/api/platform/tenants/cccccccc-cccc-4ccc-8ccc-cccccccccccc/periodic-budget", resource: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", revision: "1", at: "2026-11-01T00:00:00.000000Z", control: p("dddddddd-dddd-4ddd-8ddd-dddddddddddd"), controlScope: p("tenant"), scopeID: p("cccccccc-cccc-4ccc-8ccc-cccccccccccc"), period: p("utc_month"), tracking: p("2026-11-01T00:00:00.000000Z"), effective: p("2026-12-01T00:00:00.000000Z"), version: p("1"), versionPeriod: p("utc_month"), versionLimit: p("100"), versionEnabled: &enabled, changed: true, actorExists: true, zeroHeld: true}
	o.input = []byte(`{"expected_revision":"0","period_kind":"utc_month","enabled":true,"limit_amount":"100"}`)
	o.fingerprint, _ = Fingerprint(o.kind, o.actor, o.scope, o.target, PutPeriodicBudget{o.id, "0", "utc_month", true, "100"})
	o.snapshot = []byte(`{"scope":"tenant","scope_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","revision":"1","period_kind":"utc_month","enabled":true,"limit_amount":"100","state":"scheduled","tracking_started_at":"2026-11-01T00:00:00.000000Z","effective_from":"2026-12-01T00:00:00.000000Z","current_window":null,"carry_held":"0","as_of":"2026-11-01T00:00:00.000000Z"}`)
	if op, snapshot, gap := o.check(); op || snapshot || gap {
		t.Fatal("statically provable empty snapshot rejected")
	}
	o.zeroHeld = false
	if op, snapshot, gap := o.check(); op || snapshot || !gap {
		t.Fatal("unwitnessed dynamic prefix called proved")
	}
	o.zeroHeld = true
	o.snapshot = []byte(`{"carry_held":"private-invalid-number"}`)
	if op, snapshot, gap := o.check(); op || !snapshot || gap {
		t.Fatal("malformed result did not become safe finding")
	}
	o.input = []byte(`{"expected_revision":"private-invalid-number","period_kind":"utc_month","enabled":true,"limit_amount":"100"}`)
	if op, snapshot, gap := o.check(); !op || !snapshot || gap {
		t.Fatal("malformed safe input did not become safe finding")
	}
}
