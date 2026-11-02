package fundinglotverify

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sgtID(n int) string      { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
func sgtPtr(s string) *string { return &s }
func sgtHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func sgtFrame(parts ...string) string {
	var b []byte
	for _, s := range parts {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		b = append(b, n[:]...)
		b = append(b, s...)
	}
	return sgtHash(b)
}
func sgtAt() time.Time          { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
func sgtUTC(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000Z") }
func sgtCommand(r *SandboxSourceGraphRows, op, kind, scope, target, resource, body string) {
	at := sgtAt()
	envelope := fmt.Sprintf(`{"v":1,"actor":"operator","scope":%q,"kind":%q,"target_id":%q,"body":%s}`, scope, kind, target, body)
	r.Commands = append(r.Commands, FundingGraphCommerceCommandRow{OperationID: op, Actor: "operator", Scope: scope, Kind: kind, RequestDigest: sgtHash([]byte(envelope)), CreatedAt: at})
	r.CommerceReceipts = append(r.CommerceReceipts, FundingGraphCommerceReceiptRow{OperationID: op, Kind: kind, Scope: scope, Actor: "operator", Status: "accepted", Reason: "accepted", ResourceID: sgtPtr(resource), CreatedAt: at})
	var tenant *string
	if strings.HasPrefix(scope, "tenant:") {
		tenant = sgtPtr(strings.TrimPrefix(scope, "tenant:"))
	}
	r.Audits = append(r.Audits, ProcurementAuditRow{ID: fmt.Sprintf("audit-%d", len(r.Audits)), Actor: "operator", TenantScope: tenant, Action: kind, Target: op, Diff: "{}", CreatedAt: at})
}
func sgtBase(t *testing.T) SandboxSourceGraphRows {
	t.Helper()
	proof, e := os.ReadFile("../maintenancetrust/testdata/predecessor_v1.json")
	if e != nil {
		t.Fatal(e)
	}
	at := sgtAt()
	env := "12345678-1234-4234-8234-123456789abc"
	merchant := "merchant:测试"
	payee := "测试 <A&B> \u2028 line"
	r := SandboxSourceGraphRows{
		Environments: []SandboxGraphEnvironmentRow{{Singleton: true, ID: env, MerchantID: merchant, PayeeLabel: payee, DatabaseOID: "100", SchemaOID: "101", NonceDigest: strings.Repeat("1", 64), AppRole: "golden_202_runtime", WorkerRole: "golden_worker", ProjectorRole: "golden_projector", ProtocolVersion: "1", CreatedAt: at}},
		Attestations: []SandboxGraphAttestationRow{{Singleton: true, EnvironmentID: env, DatabaseOID: "100", SchemaOID: "101", SchemaOwnerOID: "201", AppRoleOID: "202", WorkerRoleOID: "207", ProjectorRoleOID: "208", AppNonceDigest: strings.Repeat("1", 64), WorkerNonceDigest: strings.Repeat("2", 64), ProofVersion: "1", ProofCanonical: proof, ProofSHA256: sgtHash(proof), CreatedAt: at}},
		Policies:     []SandboxGraphPolicyRow{{Singleton: true, Revision: "1", Enabled: false}}, Users: []ProcurementApprovalUserRow{{ID: "operator"}}, Tenants: []ProcurementAttemptTenantRow{{ID: "tenant-a"}},
	}
	sku, version, publish, activate, order := sgtID(1), sgtID(2), sgtID(3), sgtID(4), sgtID(5)
	r.SKUs = []SandboxGraphSKURow{{ID: sku, Revision: "2", LastVersion: "1", Enabled: true, CurrentVersionID: sgtPtr(version), CreatedBy: "operator", CreatedAt: at, LastChangeOperationID: sgtPtr(activate)}}
	r.SKUVersions = []SandboxGraphSKUVersionRow{{ID: version, SKUID: sku, Version: "1", DisplayName: "测试 SKU", Amount: "100", WalletCreditAmount: "100", Currency: "CNY", EnvironmentID: env, MerchantID: merchant, PayeeLabel: payee, CheckoutTTLSeconds: "900", PublishOperationID: publish, CreatedBy: "operator", CreatedAt: at}}
	r.SKUChanges = []SandboxGraphSKUChangeRow{{OperationID: publish, SKUID: sku, Kind: "payment.sku.publish", BeforeRevision: "0", AfterRevision: "1", PublishedVersionID: sgtPtr(version), CreatedAt: at, CreatedXID: "8"}, {OperationID: activate, SKUID: sku, Kind: "payment.sku.activate", BeforeRevision: "1", AfterRevision: "2", AfterVersionID: sgtPtr(version), AfterEnabled: true, CreatedAt: at, BeforeOperationID: sgtPtr(publish), CreatedXID: "9"}}
	sgtCommand(&r, publish, "payment.sku.publish", "platform", sku, version, `{"expected_revision":"0","display_name":"测试 SKU","amount":"100"}`)
	sgtCommand(&r, activate, "payment.sku.activate", "platform", sku, sku, fmt.Sprintf(`{"expected_revision":"1","version_id":%q,"enabled":true}`, version))
	sgtCommand(&r, order, "payment.order.create", "tenant:tenant-a", version, order, fmt.Sprintf(`{"sku_version_id":%q}`, version))
	r.Orders = []SandboxGraphOrderRow{{ID: order, TenantID: "tenant-a", Creator: "operator", SKUVersionID: version, DisplayName: "测试 SKU", Amount: "100", WalletCreditAmount: "100", Currency: "CNY", EnvironmentID: env, MerchantID: merchant, PayeeLabel: payee, MerchantOrderID: "mps1." + env + "." + order, CreatedAt: at, ExpiresAt: at.Add(900 * time.Second)}}
	command := sgtID(6)
	raw := []byte(fmt.Sprintf(`{"schema_version":1,"environment_id":%q,"merchant_id":%q,"command_id":%q,"merchant_order_id":%q,"amount":"100","currency":"CNY","created_at":%q,"expires_at":%q}`, env, merchant, command, r.Orders[0].MerchantOrderID, sgtUTC(at), sgtUTC(at.Add(900*time.Second))))
	r.CommandOutbox = []SandboxGraphCommandOutboxRow{{ID: sgtID(7), OrderID: order, Kind: "checkout", ExternalCommandID: &command, CanonicalParameters: &raw, ParameterDigest: sgtPtr(sgtHash(raw)), State: "pending", Generation: "0", AttemptCount: "0", NextAttemptAt: at, CreatedAt: at}}
	return r
}
func sgtNotification(r *SandboxSourceGraphRows, event, kind, transaction, amount, revision string, happened time.Time) string {
	n := len(r.Inbox)
	id, eid := sgtID(100+n*2), sgtID(101+n*2)
	o := r.Orders[0]
	var tx *string
	if transaction != "" {
		tx = sgtPtr(transaction)
	}
	txJSON := "null"
	if tx != nil {
		b, _ := json.Marshal(*tx)
		txJSON = string(b)
	}
	raw := fmt.Sprintf(`{"schema_version":1,"event_id":%q,"event_type":%q,"environment_id":%q,"merchant_id":%q,"order_id":%q,"provider_transaction_id":%s,"amount":%q,"currency":"CNY","occurred_at":%q,"provider_order_revision":%q}`, event, kind, o.EnvironmentID, o.MerchantID, o.ID, txJSON, amount, sgtUTC(happened), revision)
	factKind := "capture"
	if kind == "checkout.failed" {
		factKind = "closed"
		raw = strings.TrimSuffix(raw, "}") + `,"failure_code":"order_closed"}`
	} else if kind == "refund.confirmed" {
		factKind = "unsupported"
		raw = strings.TrimSuffix(raw, "}") + fmt.Sprintf(`,"refund_id":%q}`, sgtID(999))
	}
	received := sgtAt().Add(time.Hour)
	r.Inbox = append(r.Inbox, SandboxGraphInboxRow{ID: id, EnvironmentID: o.EnvironmentID, MerchantID: o.MerchantID, EventID: event, CanonicalBody: []byte(raw), BusinessDigest: sgtHash([]byte(raw)), VerifiedKeyID: "A-key", ReceivedAt: received})
	digest := sgtFrame(o.EnvironmentID, o.MerchantID, o.ID, factKind, event)
	if factKind == "capture" {
		digest = sgtFrame(o.EnvironmentID, o.MerchantID, o.ID, factKind, transaction, amount, "CNY", sgtUTC(happened), revision)
	} else if factKind == "closed" {
		digest = sgtFrame(o.EnvironmentID, o.MerchantID, o.ID, factKind, sgtUTC(happened), revision)
	}
	r.Evidence = append(r.Evidence, SandboxGraphEvidenceRow{ID: eid, InboxID: &id, EnvironmentID: o.EnvironmentID, MerchantID: o.MerchantID, OrderID: o.ID, Kind: factKind, ProviderTransactionID: tx, Amount: sgtPtr(amount), Currency: sgtPtr("CNY"), HappenedAt: &happened, ProviderOrderRevision: sgtPtr(revision), FactDigest: digest, VerifiedAt: received})
	r.Processing = append(r.Processing, SandboxGraphProcessingRow{EvidenceID: eid, State: "pending", Generation: "0", AttemptCount: "0", NextAttemptAt: received})
	return eid
}
func sgtCapture(t *testing.T) SandboxSourceGraphRows {
	r := sgtBase(t)
	eid := sgtNotification(&r, "event:capture", "capture.confirmed", "transaction:1", "100", "1", sgtAt().Add(time.Second))
	e := r.Evidence[0]
	o := r.Orders[0]
	at := sgtAt().Add(2 * time.Hour)
	r.Processing[0].State = "processed"
	r.Processing[0].Generation = "1"
	r.Processing[0].AttemptCount = "1"
	r.ProviderRevisions = []SandboxGraphProviderRevisionRow{{EnvironmentID: o.EnvironmentID, MerchantID: o.MerchantID, OrderID: o.ID, ProviderOrderRevision: "1", Kind: "capture", FactDigest: e.FactDigest, EvidenceID: eid}}
	r.Identities = []SandboxGraphIdentityRow{{ID: sgtID(20), EnvironmentID: o.EnvironmentID, MerchantID: o.MerchantID, ProviderTransactionID: "transaction:1", OrderID: o.ID, TenantID: o.TenantID, Amount: "100", Currency: "CNY", CapturedAt: *e.HappenedAt, ProviderOrderRevision: "1", EvidenceID: eid, CreatedAt: at, ProcessingGeneration: "1"}}
	r.Selections = []SandboxGraphSelectionRow{{OrderID: o.ID, IdentityID: sgtID(20), FirstEvidenceID: eid, SelectedAt: at}}
	r.Captures = []SandboxGraphCaptureRow{{ID: sgtID(21), OrderID: o.ID, IdentityID: sgtID(20), TenantID: o.TenantID, EnvironmentID: o.EnvironmentID, MerchantID: o.MerchantID, ProviderTransactionID: "transaction:1", Amount: "100", WalletCreditAmount: "100", Currency: "CNY", CapturedAt: *e.HappenedAt, ProviderOrderRevision: "1", VerifiedEvidenceID: eid, CreatedAt: at, ProcessingGeneration: "1"}}
	r.Effects = []SandboxGraphEffectRow{{CaptureID: sgtID(21), IdentityID: sgtID(20), EnvironmentID: o.EnvironmentID, MerchantID: o.MerchantID, ProviderTransactionID: "transaction:1", EffectKind: "capture", EffectID: "capture", CreatedAt: at}}
	r.Receipts = []FundingGraphReceiptRow{{ID: "receipt", TenantID: o.TenantID, SourceKind: "sandbox_capture", SourceID: sgtID(21), Amount: "100", Currency: "CNY", JournalID: "journal", CreatedAt: at}}
	r.Journals = []FundingGraphJournalRow{{ID: "journal", TenantID: o.TenantID, OperationID: sgtID(21), Kind: "sandbox_capture", Reference: o.ID, Actor: "payment-sandbox-worker", Currency: "CNY", CreatedTXID: "11", CreatedAt: at}}
	r.Accounts = []LedgerAccountRow{{ID: "available", TenantID: &o.TenantID, Kind: "AVAILABLE", Balance: "100"}, {ID: "clearing", Kind: "SANDBOX_FUNDING_CLEARING", Balance: "-100"}}
	r.Lines = []LedgerLineRow{{ID: "line-a", JournalID: "journal", AccountID: "available", SignedDelta: "100"}, {ID: "line-b", JournalID: "journal", AccountID: "clearing", SignedDelta: "-100"}}
	r.Outbox = []ProcurementOutboxRow{{ID: "credited", TenantID: o.TenantID, OperationID: sgtID(21), Kind: "credited", SchemaVersion: 1, Payload: `{"amount":"100"}`, CreatedAt: at}}
	r.Audits = append(r.Audits, ProcurementAuditRow{ID: "capture-audit", Actor: "payment-sandbox-worker", TenantScope: &o.TenantID, Action: "payment.capture.credited", Target: sgtID(21), Diff: `{"amount":"100"}`, CreatedAt: at})
	return r
}
func sgtCheck(t *testing.T, r SandboxSourceGraphRows) SandboxSourceGraphCheck {
	t.Helper()
	got, e := CheckSandboxSourceGraph(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	return got
}
func sgtValid(t *testing.T, r SandboxSourceGraphRows) {
	t.Helper()
	got := sgtCheck(t, r)
	if got.Status != "consistent" || !got.ScopedGraphConsistent {
		t.Fatalf("want consistent, got %+v", got)
	}
}
func TestSandboxSourceGraphIndependentCaptureAndPending(t *testing.T) {
	for _, capture := range []bool{false, true} {
		r := sgtBase(t)
		if capture {
			r = sgtCapture(t)
		}
		sgtValid(t, r)
		before, _ := json.Marshal(r)
		sgtCheck(t, r)
		after, _ := json.Marshal(r)
		if string(before) != string(after) {
			t.Fatal("checker mutated raw input")
		}
	}
	r := sgtCapture(t)
	raw, _ := json.Marshal(sgtCheck(t, r))
	for _, private := range []string{"operator", "tenant-a", "transaction:1", "merchant", "proof_canonical", "A-key", "100\""} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("report disclosed %q: %s", private, raw)
		}
	}
}
func TestSandboxSourceGraphStateAndHistoryVariants(t *testing.T) {
	for _, state := range []string{"pending", "leased", "retry_wait", "processing_review"} {
		t.Run(state, func(t *testing.T) {
			r := sgtBase(t)
			sgtNotification(&r, "pending-event", "capture.confirmed", "tx:1", "100", "1", sgtAt())
			p := &r.Processing[0]
			p.State = state
			if state != "pending" {
				p.Generation = "1"
				p.AttemptCount = "1"
			}
			if state == "leased" {
				at := sgtAt()
				p.LeaseUntil = &at
			}
			if state == "retry_wait" {
				p.LastErrorCode = sgtPtr("temporary")
			}
			if state == "processing_review" {
				p.Generation = "20"
				p.AttemptCount = "20"
				p.LastErrorCode = sgtPtr("attempts_exhausted")
			}
			sgtValid(t, r)
		})
	}
	r := sgtCapture(t)
	sgtNotification(&r, "same-fact", "capture.confirmed", "transaction:1", "100", "1", *r.Evidence[0].HappenedAt)
	r.Processing[1].State = "processed"
	r.Processing[1].Generation = "2"
	r.Processing[1].AttemptCount = "2"
	sgtValid(t, r)
	// The identity and capture may be backed by distinct same-fact originals.
	r.Captures[0].VerifiedEvidenceID = r.Evidence[1].ID
	r.Captures[0].ProcessingGeneration = "2"
	sgtValid(t, r)
	// A later order cannot invalidate an already committed unknown_order review.
	r = sgtBase(t)
	eid := sgtNotification(&r, "unknown-before-create", "capture.confirmed", "tx:unknown", "100", "1", sgtAt())
	r.Processing[0].State = "processing_review"
	r.Processing[0].Generation = "1"
	r.Processing[0].AttemptCount = "1"
	r.Processing[0].LastErrorCode = sgtPtr("permanent")
	r.Reviews = []SandboxGraphReviewRow{{ID: sgtID(300), OrderID: r.Orders[0].ID, EvidenceID: eid, Reason: "unknown_order", CreatedAt: sgtAt()}}
	sgtValid(t, r)
}
func TestSandboxSourceGraphOverflowAndReservedReview(t *testing.T) {
	r := sgtCapture(t)
	r.Captures = nil
	r.Effects = nil
	r.Receipts = nil
	r.Journals = nil
	r.Lines = nil
	r.Accounts = nil
	r.Outbox = nil
	r.Audits = r.Audits[:3]
	r.Processing[0].State = "processing_review"
	r.Processing[0].LastErrorCode = sgtPtr("permanent")
	r.Reviews = []SandboxGraphReviewRow{{ID: sgtID(301), OrderID: r.Orders[0].ID, EvidenceID: r.Evidence[0].ID, IdentityID: sgtPtr(r.Identities[0].ID), SelectionOrderID: sgtPtr(r.Orders[0].ID), Reason: "funding_amount_overflow", CreatedAt: sgtAt()}}
	sgtValid(t, r)
	for _, reason := range []string{"processing_attempts_exhausted", "command_attempts_exhausted", "query_attempts_exhausted"} {
		r = sgtBase(t)
		eid := sgtNotification(&r, "reserved", "capture.confirmed", "tx:r", "100", "1", sgtAt())
		r.Processing[0].State = "processing_review"
		r.Processing[0].Generation = "1"
		r.Processing[0].AttemptCount = "1"
		r.Processing[0].LastErrorCode = sgtPtr("permanent")
		r.Reviews = []SandboxGraphReviewRow{{ID: sgtID(302), OrderID: r.Orders[0].ID, EvidenceID: eid, Reason: reason, CreatedAt: sgtAt()}}
		got := sgtCheck(t, r)
		if got.Status != "indeterminate" || got.Unproven.ReservedReviewSemanticsUnproven != 1 {
			t.Fatalf("reserved reason silently accepted or rejected: %+v", got)
		}
	}
}

func TestSandboxSourceGraphOneTerminalReviewPerEvidence(t *testing.T) {
	r := sgtBase(t)
	eid := sgtNotification(&r, "two-reviews", "capture.confirmed", "tx:two", "101", "1", sgtAt())
	r.Processing[0].State = "processing_review"
	r.Processing[0].Generation = "1"
	r.Processing[0].AttemptCount = "1"
	r.Processing[0].LastErrorCode = sgtPtr("permanent")
	r.Reviews = []SandboxGraphReviewRow{{ID: sgtID(303), OrderID: r.Orders[0].ID, EvidenceID: eid, Reason: "unknown_order", CreatedAt: sgtAt()}, {ID: sgtID(304), OrderID: r.Orders[0].ID, EvidenceID: eid, Reason: "amount_mismatch", CreatedAt: sgtAt()}}
	got := sgtCheck(t, r)
	if got.Status != "mismatch" || got.Mismatches.ProcessingOutcomeRows != 1 || got.Mismatches.ReviewRows != 0 {
		t.Fatalf("separately plausible reasons bypassed one terminal: %+v", got)
	}
}
func TestSandboxSourceGraphDetectsEveryClaimedEffectDirection(t *testing.T) {
	cases := map[string]func(*SandboxSourceGraphRows){
		"environment": func(r *SandboxSourceGraphRows) { r.Environments = nil }, "attestation": func(r *SandboxSourceGraphRows) { r.Attestations = nil }, "policy": func(r *SandboxSourceGraphRows) { r.Policies = nil }, "command": func(r *SandboxSourceGraphRows) { r.Commands = r.Commands[1:] }, "receipt_terminal": func(r *SandboxSourceGraphRows) { r.CommerceReceipts = r.CommerceReceipts[1:] }, "human_audit": func(r *SandboxSourceGraphRows) { r.Audits = r.Audits[1:] }, "chain": func(r *SandboxSourceGraphRows) { r.SKUChanges = r.SKUChanges[1:] }, "version": func(r *SandboxSourceGraphRows) { r.SKUVersions = nil }, "order": func(r *SandboxSourceGraphRows) { r.Orders = nil }, "checkout": func(r *SandboxSourceGraphRows) { r.CommandOutbox = nil }, "original": func(r *SandboxSourceGraphRows) { r.Inbox = nil }, "evidence": func(r *SandboxSourceGraphRows) { r.Evidence = nil }, "processing": func(r *SandboxSourceGraphRows) { r.Processing = nil }, "revision": func(r *SandboxSourceGraphRows) { r.ProviderRevisions = nil }, "identity": func(r *SandboxSourceGraphRows) { r.Identities = nil }, "selection": func(r *SandboxSourceGraphRows) { r.Selections = nil }, "capture": func(r *SandboxSourceGraphRows) { r.Captures = nil }, "effect": func(r *SandboxSourceGraphRows) { r.Effects = nil }, "receipt": func(r *SandboxSourceGraphRows) { r.Receipts = nil }, "journal": func(r *SandboxSourceGraphRows) { r.Journals = nil }, "line": func(r *SandboxSourceGraphRows) { r.Lines = r.Lines[:1] }, "credited": func(r *SandboxSourceGraphRows) { r.Outbox = nil }, "capture_audit": func(r *SandboxSourceGraphRows) { r.Audits = r.Audits[:3] }, "wrong_tenant": func(r *SandboxSourceGraphRows) { r.Captures[0].TenantID = "other" }, "same_generation": func(r *SandboxSourceGraphRows) { r.Identities[0].ProcessingGeneration = "2" }, "wrong_reference": func(r *SandboxSourceGraphRows) { r.Journals[0].Reference = "other" }, "wrong_actor": func(r *SandboxSourceGraphRows) { r.Audits[3].Actor = "operator" }, "extra_zero_line": func(r *SandboxSourceGraphRows) {
			r.Accounts = append(r.Accounts, LedgerAccountRow{ID: "extra", Kind: "OTHER", Balance: "0"})
			r.Lines = append(r.Lines, LedgerLineRow{ID: "extra", JournalID: "journal", AccountID: "extra", SignedDelta: "0"})
		}, "extra_projection": func(r *SandboxSourceGraphRows) {
			r.Outbox = append(r.Outbox, ProcurementOutboxRow{ID: "extra", TenantID: "tenant-a", OperationID: r.Captures[0].ID, Kind: "other", Payload: "{}", CreatedAt: sgtAt()})
		}, "bad_amount": func(r *SandboxSourceGraphRows) { r.Receipts[0].Amount = "0100" }, "bad_uuid": func(r *SandboxSourceGraphRows) { r.Inbox[0].ID = "bad"; r.Evidence[0].InboxID = sgtPtr("bad") }, "bad_time": func(r *SandboxSourceGraphRows) {
			r.Captures[0].CreatedAt = r.Captures[0].CreatedAt.Add(time.Nanosecond)
		}, "unknown_kind": func(r *SandboxSourceGraphRows) { r.Receipts[0].SourceKind = "new_money" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := sgtCapture(t)
			mutate(&r)
			if got := sgtCheck(t, r); got.Status != "mismatch" {
				t.Fatalf("missing directional corruption: %+v", got)
			}
		})
	}
}
func TestSandboxSourceGraphStrictOriginalsAndProofGolden(t *testing.T) {
	for name, mutate := range map[string]func([]byte) []byte{"duplicate": func(b []byte) []byte {
		return []byte(strings.Replace(string(b), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1))
	}, "unknown": func(b []byte) []byte { return append([]byte(`{"extra":true,`), b[1:]...) }, "missing": func(b []byte) []byte { return []byte(strings.Replace(string(b), `"schema_version":1,`, "", 1)) }, "null": func(b []byte) []byte {
		return []byte(strings.Replace(string(b), `"schema_version":1`, `"schema_version":null`, 1))
	}, "number": func(b []byte) []byte {
		return []byte(strings.Replace(string(b), `"schema_version":1`, `"schema_version":1.0`, 1))
	}, "utf16": func(b []byte) []byte { return []byte(strings.Replace(string(b), `"event:capture"`, `"\ud800"`, 1)) }, "trailing": func(b []byte) []byte { return append(b, []byte(` {}`)...) }, "space": func(b []byte) []byte { return append([]byte(" "), b...) }, "too_large": func(b []byte) []byte { return []byte(strings.Repeat("x", 65537)) }} {
		t.Run(name, func(t *testing.T) {
			r := sgtCapture(t)
			r.Inbox[0].CanonicalBody = mutate(r.Inbox[0].CanonicalBody)
			r.Inbox[0].BusinessDigest = sgtHash(r.Inbox[0].CanonicalBody)
			got := sgtCheck(t, r)
			if got.Status != "mismatch" || got.Mismatches.InvalidCanonicalRows == 0 {
				t.Fatalf("bad original escaped strict checking: %+v", got)
			}
		})
	}
	r := sgtBase(t)
	sgtValid(t, r)
	// Original v1 pins identify versions by distinct IDs; rotating a label while
	// retaining the same key digest is descriptive, valid historical material.
	sameKey := sgtBase(t)
	sameKey.Attestations[0].ProofCanonical = []byte(strings.Replace(string(sameKey.Attestations[0].ProofCanonical), strings.Repeat("5", 64), strings.Repeat("4", 64), 1))
	sameKey.Attestations[0].ProofSHA256 = sgtHash(sameKey.Attestations[0].ProofCanonical)
	sgtValid(t, sameKey)
	r.Attestations[0].ProofCanonical = []byte(strings.Replace(string(r.Attestations[0].ProofCanonical), "<A&B>", `\u003cA\u0026B\u003e`, 1))
	r.Attestations[0].ProofSHA256 = sgtHash(r.Attestations[0].ProofCanonical)
	if got := sgtCheck(t, r); got.Mismatches.InvalidCanonicalRows == 0 {
		t.Fatal("v1 original HTML escaping drift accepted")
	}
}
func TestSandboxSourceGraphTypedDuplicateAndInputBoundaries(t *testing.T) {
	r := sgtCapture(t)
	r.Inbox = append(r.Inbox, r.Inbox[0])
	got := sgtCheck(t, r)
	if got.Mismatches.DuplicatePrimaryRows != 1 || got.Mismatches.EvidenceOriginalRows != 0 || got.Scanned.Inbox != 2 {
		t.Fatalf("duplicate amplified graph: %+v", got)
	}
	r = sgtCapture(t)
	extra := r.Inbox[0]
	extra.ID = sgtID(400)
	r.Inbox = append(r.Inbox, extra)
	got = sgtCheck(t, r)
	if got.Mismatches.DuplicateUniqueRows != 1 || got.Mismatches.EvidenceOriginalRows != 0 {
		t.Fatalf("unique duplicate amplified graph: %+v", got)
	}
	// SQL NULL tenant and the empty text tenant remain distinct typed keys.
	r = sgtBase(t)
	r.Accounts = []LedgerAccountRow{{ID: "null", Kind: "OTHER", Balance: "0"}, {ID: "empty", TenantID: sgtPtr(""), Kind: "OTHER", Balance: "0"}}
	sgtValid(t, r)
	for _, n := range []int{100000, 100001} {
		rows := SandboxSourceGraphRows{Users: make([]ProcurementApprovalUserRow, n)}
		_, e := CheckSandboxSourceGraph(context.Background(), rows)
		if (n == 100001) != errors.Is(e, ErrSandboxSourceGraphRowsResource) {
			t.Fatalf("row boundary %d: %v", n, e)
		}
	}
	for _, n := range []int{16 << 20, (16 << 20) + 1} {
		rows := SandboxSourceGraphRows{Users: []ProcurementApprovalUserRow{{ID: strings.Repeat("x", n)}}}
		_, e := CheckSandboxSourceGraph(context.Background(), rows)
		if (n > 16<<20) != errors.Is(e, ErrSandboxSourceGraphRowsResource) {
			t.Fatalf("byte boundary %d: %v", n, e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx  context.Context
		want error
	}{{nil, ErrSandboxSourceGraphRowsInput}, {ctx, ErrSandboxSourceGraphRowsCanceled}} {
		out, e := CheckSandboxSourceGraph(tc.ctx, SandboxSourceGraphRows{})
		if !errors.Is(e, tc.want) || !reflect.DeepEqual(out, SandboxSourceGraphCheck{}) {
			t.Fatalf("error result leaked partial data: %+v %v", out, e)
		}
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	out, e := CheckSandboxSourceGraph(ctx, SandboxSourceGraphRows{})
	if !errors.Is(e, ErrSandboxSourceGraphRowsTimeout) || out != (SandboxSourceGraphCheck{}) {
		t.Fatalf("timeout: %+v %v", out, e)
	}
}

// This fixture spells the query wire independently; no writer or parser helper
// is used to manufacture the expected canonical bytes or funding fingerprint.
func sgtQuery(r *SandboxSourceGraphRows, result string, state string, snapshot bool) {
	o, b := r.Orders[0], &r.CommandOutbox[0]
	at := sgtAt()
	b.Generation = "1"
	b.AttemptCount = "1"
	b.State = "submission_unknown"
	b.LastErrorCode = sgtPtr("timeout")
	b.LastSubmissionUnknownAt = &at
	super, query, verified, eid := sgtID(500), sgtID(501), sgtID(502), sgtID(503)
	q := []byte(fmt.Sprintf(`{"schema_version":1,"environment_id":%q,"merchant_id":%q,"query_id":%q,"command_id":%q,"merchant_order_id":%q,"command_kind":"checkout","parameter_digest":%q}`, o.EnvironmentID, o.MerchantID, query, *b.ExternalCommandID, o.MerchantOrderID, *b.ParameterDigest))
	s := SandboxGraphCommandOutboxRow{ID: super, OrderID: o.ID, Kind: "query", OriginalOutboxID: sgtPtr(b.ID), State: state, Generation: "2", AttemptCount: "2", CurrentQueryID: &query, NextAttemptAt: at, CreatedAt: at}
	if state == "leased" {
		s.LeaseUntil = &at
	}
	if state == "review_required" {
		s.Generation = "20"
		s.AttemptCount = "20"
		s.LastErrorCode = sgtPtr("attempts_exhausted")
	}
	r.CommandOutbox = append(r.CommandOutbox, s)
	r.QueryAttempts = append(r.QueryAttempts, SandboxGraphQueryAttemptRow{QueryID: query, SupervisorID: super, CreatedGeneration: "1", CanonicalRequest: q, RequestDigest: sgtHash(q), CreatedAt: at, MutationGeneration: "1"})
	if !snapshot {
		if state == "retry_wait" {
			r.CommandOutbox[len(r.CommandOutbox)-1].LastErrorCode = sgtPtr("timeout")
		}
		return
	}
	commandState, revision, resultJSON := "completed", `"1"`, result
	if result == "not_seen" || result == "in_progress" {
		commandState = result
		revision = "null"
		resultJSON = "null"
	}
	raw := []byte(fmt.Sprintf(`{"schema_version":1,"query_id":%q,"environment_id":%q,"merchant_id":%q,"merchant_order_id":%q,"command_id":%q,"command_kind":"checkout","parameter_digest":%q,"command_state":%q,"provider_order_revision":%s,"result":%s}`, query, o.EnvironmentID, o.MerchantID, o.MerchantOrderID, *b.ExternalCommandID, *b.ParameterDigest, commandState, revision, resultJSON))
	received := at.Add(time.Hour)
	r.VerifiedQueries = append(r.VerifiedQueries, SandboxGraphVerifiedQueryRow{ID: verified, QueryID: query, CanonicalBody: raw, SnapshotDigest: sgtHash(raw), VerifiedKeyID: "A-key", ReceivedAt: received})
	e := SandboxGraphEvidenceRow{ID: eid, VerifiedQueryID: &verified, EnvironmentID: o.EnvironmentID, MerchantID: o.MerchantID, OrderID: o.ID, Kind: "observation", FactDigest: sgtFrame(o.EnvironmentID, o.MerchantID, o.ID, "observation", query), VerifiedAt: received}
	if strings.Contains(resultJSON, `"status":"captured"`) {
		h := at.Add(time.Second)
		e.Kind = "capture"
		e.ProviderTransactionID = sgtPtr("transaction:1")
		e.Amount = sgtPtr("100")
		e.Currency = sgtPtr("CNY")
		e.HappenedAt = &h
		e.ProviderOrderRevision = sgtPtr("1")
		e.FactDigest = sgtFrame(o.EnvironmentID, o.MerchantID, o.ID, "capture", "transaction:1", "100", "CNY", sgtUTC(h), "1")
	}
	if strings.Contains(resultJSON, `"status":"closed"`) {
		h := at.Add(2 * time.Second)
		e.Kind = "closed"
		e.HappenedAt = &h
		e.ProviderOrderRevision = sgtPtr("1")
		e.FactDigest = sgtFrame(o.EnvironmentID, o.MerchantID, o.ID, "closed", sgtUTC(h), "1")
	}
	r.Evidence = append(r.Evidence, e)
	r.Processing = append(r.Processing, SandboxGraphProcessingRow{EvidenceID: eid, State: "pending", Generation: "0", AttemptCount: "0", NextAttemptAt: received})
}
func TestSandboxSourceGraphQueryOriginalsAndLateSnapshots(t *testing.T) {
	ready := fmt.Sprintf(`{"status":"ready","checkout_ref":%q,"failure_code":null,"capture":null,"closed_at":null}`, "checkout:"+sgtID(888))
	for _, result := range []string{"not_seen", "in_progress", ready, `{"status":"rejected","checkout_ref":null,"failure_code":"order_expired","capture":null,"closed_at":null}`, `{"status":"closed","checkout_ref":null,"failure_code":null,"capture":null,"closed_at":"2026-01-01T00:00:02.000000Z"}`} {
		r := sgtBase(t)
		sgtQuery(&r, result, "retry_wait", true)
		sgtValid(t, r)
	}
	// The same unresolved immutable child may survive later supervisor claims.
	r := sgtBase(t)
	sgtQuery(&r, "in_progress", "retry_wait", false)
	sgtValid(t, r)
	for _, state := range []string{"retry_wait", "review_required"} {
		r = sgtBase(t)
		sgtQuery(&r, ready, state, true)
		sgtValid(t, r)
	}
	// Query-origin capture uses exactly the same funding effects as a notification.
	r = sgtCapture(t)
	r.Inbox = nil
	r.Evidence = nil
	r.Processing = nil
	capture := `{"status":"captured","checkout_ref":null,"failure_code":null,"capture":{"provider_transaction_id":"transaction:1","amount":"100","currency":"CNY","occurred_at":"2026-01-01T00:00:01.000000Z","provider_order_revision":"1"},"closed_at":null}`
	sgtQuery(&r, capture, "completed", true)
	eid := r.Evidence[0].ID
	r.Processing[0].State = "processed"
	r.Processing[0].Generation = "1"
	r.Processing[0].AttemptCount = "1"
	r.ProviderRevisions[0].EvidenceID = eid
	r.Identities[0].EvidenceID = eid
	r.Selections[0].FirstEvidenceID = eid
	r.Captures[0].VerifiedEvidenceID = eid
	sgtValid(t, r)
	r.CommandOutbox[0].State = "completed"
	r.CommandOutbox[0].LastErrorCode = nil
	sgtValid(t, r)
	// Child creation can follow a newer mutation generation but cannot later
	// return to an older one; child order is explicit generation, not row order.
	children := sgtBase(t)
	sgtQuery(&children, ready, "retry_wait", true)
	children.CommandOutbox[0].Generation = "2"
	children.CommandOutbox[0].AttemptCount = "2"
	child := children.QueryAttempts[0]
	child.QueryID = sgtID(510)
	child.CreatedGeneration = "2"
	child.MutationGeneration = "2"
	child.CanonicalRequest = []byte(strings.Replace(string(child.CanonicalRequest), children.QueryAttempts[0].QueryID, child.QueryID, 1))
	child.RequestDigest = sgtHash(child.CanonicalRequest)
	children.QueryAttempts = append(children.QueryAttempts, child)
	children.CommandOutbox[1].CurrentQueryID = &child.QueryID
	children.CommandOutbox[1].LastErrorCode = sgtPtr("timeout")
	sgtValid(t, children)
	children.QueryAttempts[0].MutationGeneration = "2"
	children.QueryAttempts[1].MutationGeneration = "1"
	if got := sgtCheck(t, children); got.Mismatches.QueryAttemptRows != 1 {
		t.Fatalf("child mutation generation regressed: %+v", got)
	}
	for name, mutate := range map[string]func(*SandboxSourceGraphRows){"echo": func(r *SandboxSourceGraphRows) {
		r.VerifiedQueries[0].CanonicalBody = []byte(strings.Replace(string(r.VerifiedQueries[0].CanonicalBody), `"command_kind":"checkout"`, `"command_kind":"cancel"`, 1))
		r.VerifiedQueries[0].SnapshotDigest = sgtHash(r.VerifiedQueries[0].CanonicalBody)
	}, "key": func(r *SandboxSourceGraphRows) { r.VerifiedQueries[0].VerifiedKeyID = "unknown" }, "pointer": func(r *SandboxSourceGraphRows) { r.CommandOutbox[1].CurrentQueryID = nil }, "future_generation": func(r *SandboxSourceGraphRows) { r.QueryAttempts[0].CreatedGeneration = "3" }, "source": func(r *SandboxSourceGraphRows) { r.QueryAttempts = nil }} {
		t.Run(name, func(t *testing.T) {
			rows := sgtBase(t)
			sgtQuery(&rows, ready, "completed", true)
			mutate(&rows)
			if got := sgtCheck(t, rows); got.Status != "mismatch" {
				t.Fatalf("query corruption escaped: %+v", got)
			}
		})
	}
}

func TestSandboxSourceGraphCancelDoesNotRejectHistoricalCapture(t *testing.T) {
	r := sgtCapture(t)
	o := r.Orders[0]
	op, command, outbox := sgtID(700), sgtID(701), sgtID(702)
	sgtCommand(&r, op, "payment.order.cancel", "tenant:tenant-a", o.ID, o.ID, `{}`)
	r.CancelIntents = []SandboxGraphCancelIntentRow{{OrderID: o.ID, OperationID: op, Actor: "operator", CreatedAt: sgtAt()}}
	raw := []byte(fmt.Sprintf(`{"schema_version":1,"environment_id":%q,"merchant_id":%q,"command_id":%q,"merchant_order_id":%q}`, o.EnvironmentID, o.MerchantID, command, o.MerchantOrderID))
	r.CommandOutbox = append(r.CommandOutbox, SandboxGraphCommandOutboxRow{ID: outbox, OrderID: o.ID, Kind: "cancel", ExternalCommandID: &command, CanonicalParameters: &raw, ParameterDigest: sgtPtr(sgtHash(raw)), State: "pending", Generation: "0", AttemptCount: "0", NextAttemptAt: sgtAt(), CreatedAt: sgtAt()})
	sgtValid(t, r)
}

func TestSandboxSourceGraphOutboxStateErrorReachability(t *testing.T) {
	failures := []string{"network", "timeout", "auth", "protocol", "environment", "temporary"}
	check := func(t *testing.T, r SandboxSourceGraphRows, valid bool) {
		t.Helper()
		got := sgtCheck(t, r)
		if valid && got.Status != "consistent" || !valid && (got.Status != "mismatch" || got.Mismatches.CommandStateRows == 0) {
			t.Fatalf("state/error reachability valid=%t: %+v", valid, got)
		}
	}
	for _, code := range append(append([]string{}, failures...), "permanent", "attempts_exhausted", "") {
		t.Run("mutation_unknown_"+code, func(t *testing.T) {
			r := sgtBase(t)
			sgtQuery(&r, "in_progress", "retry_wait", false)
			r.CommandOutbox[0].LastErrorCode = nil
			if code != "" {
				r.CommandOutbox[0].LastErrorCode = sgtPtr(code)
			}
			check(t, r, code != "" && code != "permanent" && code != "attempts_exhausted")
		})
	}
	for _, code := range []string{"attempts_exhausted", "timeout", "permanent"} {
		t.Run("mutation_tenth_"+code, func(t *testing.T) {
			r := sgtBase(t)
			sgtQuery(&r, "in_progress", "retry_wait", false)
			r.CommandOutbox[0].Generation = "10"
			r.CommandOutbox[0].AttemptCount = "10"
			r.CommandOutbox[0].LastErrorCode = sgtPtr(code)
			check(t, r, code == "attempts_exhausted")
		})
	}
	for _, child := range []bool{false, true} {
		for _, code := range append(append([]string{}, failures...), "permanent", "attempts_exhausted", "") {
			t.Run(fmt.Sprintf("query_retry_child_%t_%s", child, code), func(t *testing.T) {
				r := sgtBase(t)
				sgtQuery(&r, "in_progress", "retry_wait", false)
				q := &r.CommandOutbox[1]
				if !child {
					r.QueryAttempts = nil
					q.CurrentQueryID = nil
				}
				q.LastErrorCode = nil
				if code != "" {
					q.LastErrorCode = sgtPtr(code)
				}
				valid := code == "timeout"
				if child {
					valid = code != "" && code != "permanent" && code != "attempts_exhausted"
				}
				check(t, r, valid)
			})
		}
	}
	for _, snapshot := range []string{"in_progress", fmt.Sprintf(`{"status":"ready","checkout_ref":%q,"failure_code":null,"capture":null,"closed_at":null}`, "checkout:"+sgtID(888))} {
		r := sgtBase(t)
		sgtQuery(&r, snapshot, "retry_wait", true)
		check(t, r, true)
		r.CommandOutbox[1].LastErrorCode = sgtPtr("permanent")
		check(t, r, false)
	}
	for _, tc := range []struct {
		kind, n, code string
		valid         bool
	}{{"query", "19", "permanent", true}, {"query", "19", "attempts_exhausted", false}, {"query", "20", "permanent", false}, {"query", "20", "attempts_exhausted", true}, {"mutation", "10", "permanent", true}, {"mutation", "10", "attempts_exhausted", false}} {
		t.Run("review_"+tc.kind+tc.n+tc.code, func(t *testing.T) {
			r := sgtBase(t)
			sgtQuery(&r, "in_progress", "review_required", false)
			i := 0
			if tc.kind == "query" {
				i = 1
			}
			b := &r.CommandOutbox[i]
			b.State = "review_required"
			b.Generation = tc.n
			b.AttemptCount = tc.n
			b.LastErrorCode = sgtPtr(tc.code)
			check(t, r, tc.valid)
		})
	}
}

func TestSandboxSourceGraphNativeAndProtocolMinimumTime(t *testing.T) {
	r := sgtCapture(t)
	zero := time.Time{}
	r.Environments[0].CreatedAt = zero
	r.Attestations[0].CreatedAt = zero
	r.SKUs[0].CreatedAt = zero
	r.SKUVersions[0].CreatedAt = zero
	r.SKUChanges[0].CreatedAt = zero
	r.Audits[0].CreatedAt = zero
	r.Journals[0].CreatedAt = zero
	r.Outbox[0].CreatedAt = zero
	sgtValid(t, r)
	// P1 business instant() accepts year one; a pending verified fact is not
	// silently rejected merely because Go represents that valid time as zero.
	r = sgtBase(t)
	sgtNotification(&r, "minimum-time", "capture.confirmed", "tx:min", "100", "1", zero)
	sgtValid(t, r)
	// Original v1 key-window validation has a different, explicitly nonzero ABI.
	r = sgtBase(t)
	r.Attestations[0].ProofCanonical = []byte(strings.Replace(string(r.Attestations[0].ProofCanonical), "0001-01-01T00:00:00.000001Z", "0001-01-01T00:00:00.000000Z", 1))
	r.Attestations[0].ProofSHA256 = sgtHash(r.Attestations[0].ProofCanonical)
	if got := sgtCheck(t, r); got.Status != "mismatch" || got.Mismatches.AttestationRows != 1 {
		t.Fatalf("proof key zero window accepted: %+v", got)
	}
}
func TestSandboxSourceGraphSKUHistoryAndRejectedCommands(t *testing.T) {
	r := sgtBase(t)
	sku, version := r.SKUs[0].ID, r.SKUVersions[0].ID
	noop := sgtID(600)
	sgtCommand(&r, noop, "payment.sku.activate", "platform", sku, sku, fmt.Sprintf(`{"expected_revision":"2","version_id":%q,"enabled":true}`, version))
	r.SKUChanges = append(r.SKUChanges, SandboxGraphSKUChangeRow{OperationID: noop, SKUID: sku, Kind: "payment.sku.activate", BeforeRevision: "2", AfterRevision: "2", BeforeVersionID: &version, AfterVersionID: &version, BeforeEnabled: true, AfterEnabled: true, BeforeOperationID: sgtPtr(sgtID(4)), CreatedXID: "12", CreatedAt: sgtAt()})
	r.SKUs[0].LastChangeOperationID = &noop
	sgtValid(t, r)
	// Publish version two, switch to it, then legally return to version one.
	publish, v2, switchOp, back := sgtID(601), sgtID(602), sgtID(603), sgtID(604)
	v := r.SKUVersions[0]
	v.ID = v2
	v.Version = "2"
	v.PublishOperationID = publish
	r.SKUVersions = append(r.SKUVersions, v)
	sgtCommand(&r, publish, "payment.sku.publish", "platform", sku, v2, `{"expected_revision":"2","display_name":"测试 SKU","amount":"100"}`)
	r.SKUChanges = append(r.SKUChanges, SandboxGraphSKUChangeRow{OperationID: publish, SKUID: sku, Kind: "payment.sku.publish", BeforeRevision: "2", AfterRevision: "3", BeforeVersionID: &version, AfterVersionID: &version, PublishedVersionID: &v2, BeforeEnabled: true, AfterEnabled: true, BeforeOperationID: &noop, CreatedXID: "13", CreatedAt: sgtAt()})
	for i, op := range []string{switchOp, back} {
		before, after, previous, beforeRevision, afterRevision := version, v2, publish, "3", "4"
		if i == 1 {
			before, after, previous, beforeRevision, afterRevision = v2, version, switchOp, "4", "5"
		}
		sgtCommand(&r, op, "payment.sku.activate", "platform", sku, sku, fmt.Sprintf(`{"expected_revision":%q,"version_id":%q,"enabled":true}`, beforeRevision, after))
		r.SKUChanges = append(r.SKUChanges, SandboxGraphSKUChangeRow{OperationID: op, SKUID: sku, Kind: "payment.sku.activate", BeforeRevision: beforeRevision, AfterRevision: afterRevision, BeforeVersionID: sgtPtr(before), AfterVersionID: sgtPtr(after), BeforeEnabled: true, AfterEnabled: true, BeforeOperationID: sgtPtr(previous), CreatedXID: "14", CreatedAt: sgtAt()})
	}
	r.SKUs[0].Revision = "5"
	r.SKUs[0].LastVersion = "2"
	r.SKUs[0].LastChangeOperationID = &back
	sgtValid(t, r)
	for _, bad := range []string{"cycle", "missing", "digest", "unconsumed"} {
		t.Run(bad, func(t *testing.T) {
			rows := sgtBase(t)
			switch bad {
			case "cycle":
				rows.SKUChanges[0].BeforeOperationID = sgtPtr(rows.SKUChanges[1].OperationID)
			case "missing":
				rows.SKUs[0].LastChangeOperationID = sgtPtr(sgtID(777))
			case "digest":
				rows.Commands[0].RequestDigest = strings.Repeat("a", 64)
			case "unconsumed":
				v := rows.SKUVersions[0]
				v.ID = sgtID(778)
				v.Version = "2"
				v.PublishOperationID = sgtID(779)
				rows.SKUVersions = append(rows.SKUVersions, v)
			}
			if got := sgtCheck(t, rows); got.Status != "mismatch" {
				t.Fatalf("SKU corruption escaped: %+v", got)
			}
		})
	}
	rows := sgtBase(t)
	op := sgtID(605)
	rows.Commands = append(rows.Commands, FundingGraphCommerceCommandRow{OperationID: op, Actor: "operator", Scope: "tenant:tenant-a", Kind: "payment.order.create", RequestDigest: strings.Repeat("a", 64), CreatedAt: sgtAt()})
	rows.CommerceReceipts = append(rows.CommerceReceipts, FundingGraphCommerceReceiptRow{OperationID: op, Actor: "operator", Scope: "tenant:tenant-a", Kind: "payment.order.create", Status: "rejected", Reason: "payments_disabled", CreatedAt: sgtAt()})
	sgtValid(t, rows)
}
