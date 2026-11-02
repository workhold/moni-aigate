package fundinglotscan

import (
	"bytes"
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// These physical sets follow the frozen DDL and business contracts, not the
// binding or reflection order. In particular, the attestation DDL places the
// nullable worker before the projector, and the DTO does the reverse.
func TestWalletBindingsFrozenPhysicalSets(t *testing.T) {
	want := map[string]struct{ keys, columns string }{
		"tenants":                             {"id", "id fl_created_xid"},
		"ledger_accounts":                     {"id", "id tenant_id kind balance"},
		"journals":                            {"id", "id tenant_id operation_id kind reference actor currency created_txid created_at"},
		"journal_lines":                       {"id", "id journal_id account_id signed_delta"},
		"funding_receipts":                    {"id", "id tenant_id source_kind source_id amount currency journal_id created_at"},
		"request_records":                     {"id", "id tenant_id owner_id billing_status"},
		"reservations":                        {"id", "id tenant_id request_id amount status"},
		"settlements":                         {"id", "id tenant_id request_id charged_amount refunded_amount status reason journal_id"},
		"refund_orders":                       {"id", "id tenant_id settlement_id amount state requester approver journal_id"},
		"funding_lot_epoch":                   {"singleton", "singleton id schema_version database_oid schema_oid basis_digest manifest_digest descriptor_digest runtime_proof_digest tenant_count receipt_count settlement_count approved_refund_count opening_count legacy_source_count available_total held_total actor created_at"},
		"funding_lot_runtime_attestations":    {"singleton", "singleton epoch_id proof_version mode database_oid schema_oid schema_owner_oid app_role_oid worker_role_oid projector_role_oid predecessor_payment_digest proof_canonical proof_sha256 manifest_digest descriptor_digest created_at"},
		"funding_lot_legacy_receipts":         {"receipt_id", "receipt_id epoch_id tenant_id source_kind source_id amount journal_id"},
		"funding_lot_legacy_sandbox_receipts": {"receipt_id", "receipt_id epoch_id tenant_id capture_id amount journal_id"},
		"funding_lot_legacy_settlements":      {"settlement_id", "settlement_id epoch_id tenant_id reservation_id request_id charged_amount refunded_amount_at_cutover journal_id"},
		"funding_lot_legacy_approved_refunds": {"refund_order_id", "refund_order_id epoch_id tenant_id settlement_id amount journal_id"},
		"funding_lot_legacy_openings":         {"id", "id epoch_id tenant_id kind account_id reservation_id amount observed_at"},
		"funding_wallet_legacy_sources":       {"kind source_id", "epoch_id tenant_id kind source_id journal_id journal_operation_id amount request_id reservation_id settlement_id"},
		"funding_wallet_operations":           {"id", "id tenant_id epoch_id seq previous_seq previous_id kind source_id journal_id journal_operation_id event_count available_delta held_delta consumed_delta issued_delta created_xid created_at"},
		"funding_wallet_heads":                {"tenant_id", "tenant_id epoch_id tail_seq tail_operation_id"},
		"funding_lots":                        {"id", "id tenant_id origin_kind funding_receipt_id legacy_opening_id legacy_refund_order_id issued_amount currency refund_class fifo_at created_at"},
		"funding_lot_events":                  {"id", "id tenant_id lot_id wallet_operation_id wallet_seq kind operation_id journal_id previous_event_id before_revision after_revision before_available before_held before_consumed after_available after_held after_consumed available_delta inference_held_delta consumed_delta issued_delta created_xid created_at"},
		"funding_lot_balances":                {"lot_id", "lot_id tenant_id available_amount inference_held_amount consumed_amount revision last_event_id"},
		"funding_reservation_lots":            {"id", "id tenant_id reservation_id lot_id allocation_index amount created_xid created_at"},
		"funding_settlement_lots":             {"id", "id tenant_id settlement_id reservation_lot_id charged_amount released_amount created_xid created_at"},
		"funding_consumption_refund_lots":     {"id", "id tenant_id refund_order_id lot_id settlement_lot_id allocation_index amount origin created_xid created_at"},
	}
	b := walletBindings()
	if len(b) != len(want) {
		t.Fatalf("wallet input groups=%d, want %d", len(b), len(want))
	}
	seen := map[string]bool{}
	for _, binding := range b {
		contract, ok := want[binding.table]
		if !ok || seen[binding.table] || !binding.valid {
			t.Fatalf("unexpected, duplicate or invalid binding: %s", binding.table)
		}
		seen[binding.table] = true
		var keys, names []string
		for _, k := range binding.keys {
			if k.numeric {
				t.Fatalf("wallet pagination must use physical text/bool PK: %s", binding.table)
			}
			keys = append(keys, k.name)
		}
		if strings.Join(keys, " ") != contract.keys {
			t.Errorf("%s physical PK=%v, want %s", binding.table, keys, contract.keys)
		}
		for _, c := range binding.columns {
			names = append(names, c.name)
			limit := fieldLimit
			if binding.table == "funding_lot_runtime_attestations" && c.name == "proof_canonical" {
				limit = 262144
				if c.kind != bytesKind {
					t.Fatal("wallet attestation original bytes were converted to text")
				}
			}
			if columnLimit(c) != limit {
				t.Errorf("unexpected fixed field budget for %s.%s", binding.table, c.name)
			}
		}
		expected := strings.Fields(contract.columns)
		sort.Strings(names)
		sort.Strings(expected)
		if !reflect.DeepEqual(names, expected) {
			t.Errorf("%s physical columns=%v, want %v", binding.table, names, expected)
		}
	}
	old, oldIndex, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings())
	if err != nil || len(old) != 68 {
		t.Fatalf("old source registry changed: count=%d err=%v", len(old), err)
	}
	if _, exists := old[oldIndex["tenants"]].spec.index["fl_created_xid"]; exists {
		t.Fatal("wallet birth column escaped into the old 68-table reader")
	}
	combined, index, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings(), b)
	if err != nil || len(combined) != 84 {
		t.Fatalf("combined physical registry: count=%d err=%v", len(combined), err)
	}
	for _, table := range old {
		shared := combined[index[table.spec.name]].spec
		additional := len(shared.columns) - len(table.spec.columns)
		wantAdditional := 0
		if table.spec.name == "tenants" {
			wantAdditional = 1
		}
		if additional != wantAdditional {
			t.Errorf("%s added shared columns=%d, want %d", table.spec.name, additional, wantAdditional)
		}
	}
}

func walletProjectionSession(t *testing.T) *session {
	t.Helper()
	tables, index, err := registry(walletBindings())
	if err != nil {
		t.Fatal(err)
	}
	for i := range tables {
		tables[i].eof = true
	}
	return &session{ctx: context.Background(), tables: tables, index: index, rejected: map[cellRef]bool{}}
}

func walletProjectionRecord(t *testing.T, s *session, name string, override map[string]cell) rawRow {
	t.Helper()
	table := &s.tables[s.index[name]]
	row := make(rawRow, len(table.spec.columns))
	for i, c := range table.spec.columns {
		row[i] = cell{kind: c.kind, valid: true, text: c.name}
		if c.kind == timeKind {
			row[i].instant = pgtype.Timestamptz{Valid: true, Time: time.Date(10000, 1, 2, 3, 4, 5, 6000, time.UTC)}
		}
	}
	for column, value := range override {
		i, ok := table.spec.index[column]
		if !ok {
			t.Fatalf("unknown physical override: %s.%s", name, column)
		}
		row[i] = value
	}
	table.rows = append(table.rows, row)
	return row
}

func walletProjectionText(v string) cell { return cell{kind: textKind, valid: true, text: v} }

func TestProjectWalletEmbeddedPathsAndOriginalValues(t *testing.T) {
	s := walletProjectionSession(t)
	for _, table := range []string{"journals", "funding_wallet_legacy_sources", "funding_wallet_operations", "funding_lot_events"} {
		walletProjectionRecord(t, s, table, nil)
	}
	walletProjectionRecord(t, s, "tenants", map[string]cell{
		"id": walletProjectionText(""), "fl_created_xid": walletProjectionText("18446744073709551615"),
	})
	canonical := []byte{0, 0xff, 'p'}
	walletProjectionRecord(t, s, "funding_lot_runtime_attestations", map[string]cell{
		"singleton":       {kind: boolKind, valid: true, boolean: false},
		"proof_canonical": {kind: bytesKind, valid: true, data: canonical},
	})
	out, qualified, err := projectWallet(s)
	if err != nil || !qualified || len(s.rejected) != 0 {
		t.Fatalf("raw projection: qualified=%v rejected=%d err=%v", qualified, len(s.rejected), err)
	}
	stamp := fundinglotverify.WalletLotsTime{Value: time.Date(10000, 1, 2, 3, 4, 5, 6000, time.UTC), Valid: true}
	wantJournal := fundinglotverify.WalletLotsJournalRow{
		WalletSequenceJournalRow: fundinglotverify.WalletSequenceJournalRow{ID: "id", TenantID: "tenant_id", OperationID: "operation_id", Kind: "kind", Currency: "currency", CreatedTXID: "created_txid"},
		Reference:                "reference", Actor: "actor", CreatedAt: stamp,
	}
	if !reflect.DeepEqual(out.Journals[0], wantJournal) {
		t.Errorf("journal header/append fields were permuted: %#v", out.Journals[0])
	}
	ptr := func(v string) *string { return &v }
	wantLegacy := fundinglotverify.WalletLotsLegacySourceRow{
		WalletSequenceLegacySourceRow: fundinglotverify.WalletSequenceLegacySourceRow{EpochID: "epoch_id", TenantID: "tenant_id", Kind: "kind", SourceID: "source_id", JournalID: "journal_id", JournalOperationID: "journal_operation_id", Amount: "amount"},
		RequestID:                     ptr("request_id"), ReservationID: ptr("reservation_id"), SettlementID: ptr("settlement_id"),
	}
	if !reflect.DeepEqual(out.LegacySources[0], wantLegacy) {
		t.Errorf("legacy embedded/nullable fields were permuted: %#v", out.LegacySources[0])
	}
	wantOperation := fundinglotverify.WalletLotsOperationRow{
		WalletSequenceOperationRow: fundinglotverify.WalletSequenceOperationRow{
			ID: "id", TenantID: "tenant_id", EpochID: "epoch_id", Seq: "seq", PreviousSeq: ptr("previous_seq"), PreviousID: ptr("previous_id"), Kind: "kind", SourceID: "source_id", JournalID: ptr("journal_id"), JournalOperationID: ptr("journal_operation_id"), EventCount: "event_count", AvailableDelta: "available_delta", HeldDelta: "held_delta", ConsumedDelta: "consumed_delta", IssuedDelta: "issued_delta", CreatedXID: "created_xid",
		},
		CreatedAt: stamp,
	}
	if !reflect.DeepEqual(out.Operations[0], wantOperation) {
		t.Errorf("operation embedded fields were permuted: %#v", out.Operations[0])
	}
	wantEvent := fundinglotverify.WalletLotsEventRow{
		WalletSequenceEventRow: fundinglotverify.WalletSequenceEventRow{
			ID: "id", TenantID: "tenant_id", LotID: "lot_id", WalletOperationID: "wallet_operation_id", WalletSeq: "wallet_seq", Kind: "kind", OperationID: ptr("operation_id"), JournalID: ptr("journal_id"), CreatedXID: "created_xid", AvailableDelta: "available_delta", HeldDelta: "inference_held_delta", ConsumedDelta: "consumed_delta", IssuedDelta: "issued_delta",
		},
		PreviousEventID: ptr("previous_event_id"), BeforeRevision: "before_revision", AfterRevision: "after_revision", BeforeAvailable: "before_available", BeforeHeld: "before_held", BeforeConsumed: "before_consumed", AfterAvailable: "after_available", AfterHeld: "after_held", AfterConsumed: "after_consumed", CreatedAt: stamp,
	}
	if !reflect.DeepEqual(out.Events[0], wantEvent) {
		t.Errorf("event embedded fields or inference_held_delta were permuted: %#v", out.Events[0])
	}
	if out.Tenants[0].ID != "" || out.Tenants[0].CreatedXID == nil || *out.Tenants[0].CreatedXID != "18446744073709551615" {
		t.Fatal("existing empty TEXT identity or full xid8 was normalized")
	}
	a := out.Attestations[0]
	if a.Singleton || a.ProjectorRoleOID != "projector_role_oid" || a.WorkerRoleOID == nil || *a.WorkerRoleOID != "worker_role_oid" || !bytes.Equal(a.ProofCanonical, canonical) || a.CreatedAt != stamp {
		t.Fatal("attestation bool, role order, original bytes or native time changed")
	}
	canonical[0] = 42
	if a.ProofCanonical[0] != 0 {
		t.Fatal("projected proof aliases the owned raw source")
	}
}

func TestProjectWalletNativeNullAndTimeBoundaries(t *testing.T) {
	for _, native := range []pgtype.Timestamptz{
		{},
		{Valid: true, InfinityModifier: pgtype.Infinity},
		{Valid: true, InfinityModifier: pgtype.NegativeInfinity},
		{Valid: true, Time: time.Time{}},
		{Valid: true, Time: time.Date(-4712, 2, 3, 0, 0, 0, 0, time.UTC)},
		{Valid: true, Time: time.Date(294276, 2, 3, 0, 0, 0, 0, time.UTC)},
	} {
		s := walletProjectionSession(t)
		walletProjectionRecord(t, s, "funding_receipts", map[string]cell{"created_at": {kind: timeKind, valid: native.Valid, instant: native}})
		walletProjectionRecord(t, s, "tenants", map[string]cell{"fl_created_xid": {kind: textKind}})
		walletProjectionRecord(t, s, "refund_orders", map[string]cell{"approver": {kind: textKind}, "journal_id": walletProjectionText("")})
		out, qualified, err := projectWallet(s)
		if err != nil || !qualified {
			t.Fatalf("envelope cannot be replaced with a DTO qualification failure: qualified=%v err=%v", qualified, err)
		}
		got := out.Receipts[0].CreatedAt
		if got.Valid != native.Valid || got.Infinity != int8(native.InfinityModifier) || got.Value != native.Time {
			t.Fatal("native timestamp NULL/finite/infinity changed")
		}
		if out.Tenants[0].CreatedXID != nil || out.Refunds[0].Approver != nil || out.Refunds[0].JournalID == nil || *out.Refunds[0].JournalID != "" {
			t.Fatal("nullable TEXT NULL and empty were conflated")
		}
	}
	for _, raw := range [][]byte{nil, {}} {
		s := walletProjectionSession(t)
		walletProjectionRecord(t, s, "funding_lot_runtime_attestations", map[string]cell{"proof_canonical": {kind: bytesKind, valid: raw != nil, data: raw}})
		out, qualified, err := projectWallet(s)
		if err != nil || !qualified || (out.Attestations[0].ProofCanonical == nil) != (raw == nil) {
			t.Fatalf("proof NULL and empty changed: qualified=%v err=%v", qualified, err)
		}
	}
}

func TestProjectWalletRequiredNullQualificationAndEOF(t *testing.T) {
	s := walletProjectionSession(t)
	walletProjectionRecord(t, s, "journals", map[string]cell{"actor": {kind: textKind}})
	walletProjectionRecord(t, s, "funding_lot_epoch", map[string]cell{"singleton": {kind: boolKind}})
	out, qualified, err := projectWallet(s)
	if err != nil || qualified || len(out.Journals) != 1 || len(out.Epochs) != 1 || len(s.rejected) != 2 {
		t.Fatalf("required NULL qualification: qualified=%v rejected=%d err=%v", qualified, len(s.rejected), err)
	}
	if _, qualified, err = projectWallet(s); err != nil || qualified || len(s.rejected) != 2 {
		t.Fatal("the same physical NULL was counted again on projection")
	}
	s.tables[s.index["funding_consumption_refund_lots"]].eof = false
	out, qualified, err = projectWallet(s)
	if err != ErrExecution || qualified || !reflect.DeepEqual(out, fundinglotverify.WalletLotsRows{}) {
		t.Fatal("an incomplete physical table published partial wallet input")
	}
}
