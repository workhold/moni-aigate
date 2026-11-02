package fundinglotinit_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	p "github.com/moni-ai/moni-aigate/internal/fundinglotinit"
	v "github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

const goldenEpoch = "01234567-89ab-4cde-8fab-0123456789ab"

func externalPtr(s string) *string { return &s }
func externalAt() v.WalletLotsTime {
	return v.WalletLotsTime{Valid: true, Value: time.Date(2026, 11, 3, 4, 5, 6, 123456000, time.UTC)}
}

// This fixture attaches identities to independently supplied financial tables.
// It never allocates, derives opening amounts, or invokes the planner/checker.
func externalMixed() p.Input {
	in := p.Input{EpochID: goldenEpoch, EpochAt: externalAt(), Mode: "sandbox"}
	in.Rows.Tenants = []v.LegacySeedTenantRow{{ID: "tenant/A"}, {ID: "租户/乙"}, {ID: ""}}
	in.Rows.Accounts = []v.LedgerAccountRow{
		{ID: "account/a/available", TenantID: externalPtr("tenant/A"), Kind: "AVAILABLE", Balance: "165"},
		{ID: "account/a/held", TenantID: externalPtr("tenant/A"), Kind: "HELD", Balance: "40"},
		{ID: "account/b/available", TenantID: externalPtr("租户/乙"), Kind: "AVAILABLE", Balance: "9223372036854775657"},
		{ID: "account/b/held", TenantID: externalPtr("租户/乙"), Kind: "HELD", Balance: "9223372036854775787"},
		{ID: "platform/funding", Kind: "FUNDING_CLEARING", Balance: "-9223372036854775807"},
		{ID: "platform/grant", Kind: "OPERATING_GRANT_CLEARING", Balance: "-9223372036854775807"},
		{ID: "platform/sandbox", Kind: "SANDBOX_FUNDING_CLEARING", Balance: "-50"},
		{ID: "platform/revenue", Kind: "REVENUE", Balance: "15"},
	}
	type line struct{ account, amount string }
	journal := func(id, tenant, operation, kind, reference, actor string, lines ...line) {
		in.Rows.Journals = append(in.Rows.Journals, v.WalletLotsJournalRow{WalletSequenceJournalRow: v.WalletSequenceJournalRow{ID: id, TenantID: tenant, OperationID: operation, Kind: kind, Currency: "CNY", CreatedTXID: "101"}, Reference: reference, Actor: actor, CreatedAt: externalAt()})
		for i, row := range lines {
			in.Rows.Lines = append(in.Rows.Lines, v.LedgerLineRow{ID: id + "/line/" + string(rune('a'+i)), JournalID: id, AccountID: row.account, SignedDelta: row.amount})
		}
	}
	for _, r := range []struct{ id, tenant, kind, source, operation, amount, available, clearing, reference string }{
		{"receipt/a/manual", "tenant/A", "manual_credit", "credit/a", "credit/a", "100", "account/a/available", "platform/funding", "credit/a"},
		{"receipt/a/voucher", "tenant/A", "voucher_grant", "voucher/a", "redemption/a", "70", "account/a/available", "platform/grant", "voucher/a"},
		{"receipt/a/sandbox", "tenant/A", "sandbox_capture", "capture/a", "capture/a", "50", "account/a/available", "platform/sandbox", "payment/order/a"},
		{"receipt/b/manual", "租户/乙", "manual_credit", "credit/b", "credit/b", "9223372036854775707", "account/b/available", "platform/funding", "credit/b"},
		{"receipt/b/voucher", "租户/乙", "voucher_grant", "voucher/b", "redemption/b", "9223372036854775737", "account/b/available", "platform/grant", "voucher/b"},
	} {
		j := "journal/" + strings.TrimPrefix(r.id, "receipt/")
		kind := r.kind
		if kind == "manual_credit" {
			kind = "credit"
		}
		journal(j, r.tenant, r.operation, kind, r.reference, "finance", line{r.available, r.amount}, line{r.clearing, "-" + r.amount})
		in.Rows.Receipts = append(in.Rows.Receipts, v.WalletLotsReceiptRow{ID: r.id, TenantID: r.tenant, SourceKind: r.kind, SourceID: r.source, Amount: r.amount, Currency: "CNY", JournalID: j, CreatedAt: externalAt()})
	}
	for _, r := range []struct{ suffix, tenant, amount, negative, status, available, held string }{
		{"a/terminal", "tenant/A", "60", "-60", "settled", "account/a/available", "account/a/held"},
		{"a/hold", "tenant/A", "40", "-40", "reserved", "account/a/available", "account/a/held"},
		{"a/zero", "tenant/A", "0", "0", "pending_reconciliation", "account/a/available", "account/a/held"},
		{"b/hold", "租户/乙", "9223372036854775787", "-9223372036854775787", "reserved", "account/b/available", "account/b/held"},
	} {
		request, reservation := "request/"+r.suffix, "reserve/"+r.suffix
		in.Rows.Requests = append(in.Rows.Requests, v.WalletLotsRequestRow{ID: request, TenantID: r.tenant, OwnerID: "customer", BillingStatus: r.status})
		in.Rows.Reservations = append(in.Rows.Reservations, v.WalletLotsReservationRow{ID: reservation, TenantID: r.tenant, RequestID: request, Amount: r.amount, Status: r.status})
		journal("journal/reserve/"+r.suffix, r.tenant, reservation, "reserve", request, "customer", line{r.available, r.negative}, line{r.held, r.amount})
	}
	journal("journal/a/finish", "tenant/A", "settlement/a/terminal", "settled", "request/a/terminal", "system", line{"account/a/available", "30"}, line{"account/a/held", "-60"}, line{"platform/revenue", "30"})
	in.Rows.Settlements = []v.WalletLotsSettlementRow{{ID: "settlement/a/terminal", TenantID: "tenant/A", RequestID: "request/a/terminal", ChargedAmount: "30", RefundedAmount: "15", Status: "settled", Reason: "usage_known", JournalID: "journal/a/finish"}}
	for _, amount := range []string{"10", "5"} {
		id, j := "refund/a/"+amount, "journal/a/refund/"+amount
		journal(j, "tenant/A", id, "refund", "settlement/a/terminal", "checker", line{"account/a/available", amount}, line{"platform/revenue", "-" + amount})
		in.Rows.Refunds = append(in.Rows.Refunds, v.WalletLotsRefundRow{ID: id, TenantID: "tenant/A", SettlementID: "settlement/a/terminal", Amount: amount, State: "approved", Requester: "maker", Approver: externalPtr("checker"), JournalID: externalPtr(j)})
	}
	in.Rows.Refunds = append(in.Rows.Refunds, v.WalletLotsRefundRow{ID: "refund/a/pending", TenantID: "tenant/A", SettlementID: "settlement/a/terminal", Amount: "7", State: "pending", Requester: "maker"})
	return in
}

func externalClone[T any](t *testing.T, in T) T {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func normalizedPlan(in p.Plan) p.Plan {
	r := reflect.ValueOf(&in).Elem()
	for i := 0; i < r.NumField(); i++ {
		f := r.Field(i)
		if f.Kind() == reflect.Slice && f.Len() == 0 {
			f.Set(reflect.Zero(f.Type()))
		}
	}
	return in
}

func externalMustBuild(t *testing.T, in p.Input) p.Plan {
	t.Helper()
	out, err := p.Build(context.Background(), in)
	if err != nil {
		t.Fatalf("independent valid seed refused: %v", err)
	}
	return out
}

func TestExternalHandCalculatedMixedGolden(t *testing.T) {
	in := externalMixed()
	before := externalClone(t, in)
	got := externalMustBuild(t, in)
	raw, err := os.ReadFile("testdata/mixed_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var want p.Plan
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalizedPlan(got), normalizedPlan(want)) {
		actual, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("hand-calculated golden mismatch; actual:\n%s", actual)
	}
	if !reflect.DeepEqual(in, before) {
		t.Fatal("Build changed original supplied rows")
	}
	// Four positive openings, including two independent BIGINT-sized buckets.
	if got.Metadata.GeneratedRows != 49 || got.Metadata.DataRows != 47 || len(got.Lots) != 4 || len(got.ReservationLots) != 2 {
		t.Fatal("hand-counted G=49/data=47 changed")
	}
	if got.Metadata.AvailableTotal != "9223372036854775822" || got.Metadata.HeldTotal != "9223372036854775827" {
		t.Fatal("NUMERIC totals were narrowed")
	}
}

func TestExternalFixedUUIDFrames(t *testing.T) {
	got := externalMustBuild(t, externalMixed())
	openingIDs := map[string]string{}
	for _, o := range got.LegacyOpenings {
		if o.Kind == "available" {
			openingIDs[o.TenantID] = o.ID
		}
		if o.ReservationID != nil && *o.ReservationID == "reserve/a/hold" && o.ID != "1f99edec-4650-50b5-a9ff-e3e55e3aeba6" {
			t.Fatal("held-opening fixed frame changed")
		}
	}
	for tenant, want := range map[string]string{"tenant/A": "5997bcd3-f549-546c-81fa-3341a082787f", "租户/乙": "b0706b9c-f89c-5fb0-96b6-ad4df9269fbe", "": "8eba0f9b-0ede-5d89-b0a5-6ded6e29b4c1"} {
		if openingIDs[tenant] != want {
			t.Fatalf("fixed UUID frame changed for %q", tenant)
		}
	}
	for _, op := range got.Operations {
		if op.TenantID == "tenant/A" && op.ID != "eb41333a-0f46-556e-af32-01c732c0ed20" {
			t.Fatal("epoch-operation independent fixed vector changed")
		}
	}
	// Changing the observed clock must leave every generated identity unchanged.
	in := externalMixed()
	in.EpochAt.Value = in.EpochAt.Value.Add(time.Second)
	later := externalMustBuild(t, in)
	for i := range got.LegacyOpenings {
		if got.LegacyOpenings[i].ID != later.LegacyOpenings[i].ID {
			t.Fatal("clock entered identity frame")
		}
	}
	for i := range got.Operations {
		if got.Operations[i].ID != later.Operations[i].ID {
			t.Fatal("clock entered operation frame")
		}
	}
	for i := range got.Lots {
		if got.Lots[i].ID != later.Lots[i].ID {
			t.Fatal("clock entered lot frame")
		}
	}
	for i := range got.Events {
		if got.Events[i].ID != later.Events[i].ID {
			t.Fatal("clock entered event frame")
		}
	}
}

func TestExternalUUIDLengthFramesDoNotConcatenate(t *testing.T) {
	in := p.Input{EpochID: goldenEpoch, EpochAt: externalAt(), Mode: "ordinary"}
	for _, row := range []struct{ tenant, reservation, request string }{{"ab", "c", "request/one"}, {"a", "bc", "request/two"}} {
		in.Rows.Tenants = append(in.Rows.Tenants, v.LegacySeedTenantRow{ID: row.tenant})
		for _, kind := range []string{"AVAILABLE", "HELD"} {
			in.Rows.Accounts = append(in.Rows.Accounts, v.LedgerAccountRow{ID: row.tenant + "/" + kind, TenantID: externalPtr(row.tenant), Kind: kind, Balance: "0"})
		}
		in.Rows.Requests = append(in.Rows.Requests, v.WalletLotsRequestRow{ID: row.request, TenantID: row.tenant, OwnerID: "owner", BillingStatus: "reserved"})
		in.Rows.Reservations = append(in.Rows.Reservations, v.WalletLotsReservationRow{ID: row.reservation, TenantID: row.tenant, RequestID: row.request, Amount: "0", Status: "reserved"})
		j := "journal/" + row.request
		in.Rows.Journals = append(in.Rows.Journals, v.WalletLotsJournalRow{WalletSequenceJournalRow: v.WalletSequenceJournalRow{ID: j, TenantID: row.tenant, OperationID: row.reservation, Kind: "reserve", Currency: "CNY", CreatedTXID: "101"}, Reference: row.request, Actor: "owner", CreatedAt: externalAt()})
		for _, kind := range []string{"AVAILABLE", "HELD"} {
			in.Rows.Lines = append(in.Rows.Lines, v.LedgerLineRow{ID: j + "/" + kind, JournalID: j, AccountID: row.tenant + "/" + kind, SignedDelta: "0"})
		}
	}
	got := externalMustBuild(t, in)
	want := map[string]string{"c": "ee59c627-a1dd-540d-a69e-4b5867118241", "bc": "c9199863-d806-5936-a2c8-a0ce96774612"}
	for _, row := range got.LegacyOpenings {
		if row.ReservationID != nil && row.ID != want[*row.ReservationID] {
			t.Fatalf("independent ambiguity frame vector changed: %q", *row.ReservationID)
		}
	}
	if len(got.LegacyOpenings) != 4 || len(got.Lots) != 0 || len(got.LegacySources) != 2 {
		t.Fatal("zero holds were lost or issued")
	}
}

func TestExternalIDsKeepRawUnicodeAndExcludeAmounts(t *testing.T) {
	in := p.Input{EpochID: goldenEpoch, EpochAt: externalAt(), Mode: "ordinary", Rows: v.LegacySeedRows{Tenants: []v.LegacySeedTenantRow{{ID: "é"}, {ID: "e\u0301"}}}}
	got := externalMustBuild(t, in)
	want := map[string]string{"é": "4c24aa1f-037a-5fa2-b407-c44476e0287f", "e\u0301": "71e28e16-20e4-54b0-93d4-0df83355f6f8"}
	for _, row := range got.LegacyOpenings {
		if row.ID != want[row.TenantID] {
			t.Fatal("UUID normalized Unicode or used rune counts")
		}
	}
	base := externalMixed()
	first := externalMustBuild(t, base)
	// All changes are a single extra historical credit unit. The shared funding
	// clearing reaches signed BIGINT minimum, which is legal for this kind.
	base.Rows.Accounts[0].Balance = "166"
	base.Rows.Accounts[4].Balance = "-9223372036854775808"
	base.Rows.Receipts[0].Amount = "101"
	base.Rows.Lines[0].SignedDelta = "101"
	base.Rows.Lines[1].SignedDelta = "-101"
	second := externalMustBuild(t, base)
	for i := range first.LegacyOpenings {
		if first.LegacyOpenings[i].ID != second.LegacyOpenings[i].ID {
			t.Fatal("amount entered opening identity")
		}
	}
	for i := range first.Operations {
		if first.Operations[i].ID != second.Operations[i].ID {
			t.Fatal("amount entered operation identity")
		}
	}
	for i := range first.Lots {
		if first.Lots[i].ID != second.Lots[i].ID {
			t.Fatal("amount entered lot identity")
		}
	}
	for i := range first.Events {
		if first.Events[i].ID != second.Events[i].ID {
			t.Fatal("amount entered event identity")
		}
	}
	for i := range first.ReservationLots {
		if first.ReservationLots[i].ID != second.ReservationLots[i].ID {
			t.Fatal("amount entered allocation identity")
		}
	}
	if second.Metadata.AvailableTotal != "9223372036854775823" {
		t.Fatal("changed financial input was not actually reflected")
	}
}

func TestExternalInputPermutationDeterminism(t *testing.T) {
	want := externalMustBuild(t, externalMixed())
	for seed := int64(0); seed < 16; seed++ {
		in := externalMixed()
		rng := rand.New(rand.NewSource(seed))
		rows := reflect.ValueOf(&in.Rows).Elem()
		for i := 0; i < rows.NumField(); i++ {
			f := rows.Field(i)
			swap := reflect.Swapper(f.Interface())
			rng.Shuffle(f.Len(), swap)
		}
		before := externalClone(t, in)
		got := externalMustBuild(t, in)
		if !reflect.DeepEqual(normalizedPlan(got), normalizedPlan(want)) {
			t.Fatalf("input permutation %d changed output", seed)
		}
		if !reflect.DeepEqual(in, before) {
			t.Fatalf("input permutation %d mutated", seed)
		}
	}
}

func TestExternalDraftOwnsPointersAndEpochNamespacing(t *testing.T) {
	in := externalMixed()
	before := externalClone(t, in)
	got := externalMustBuild(t, in)
	for i := range got.LegacyOpenings {
		if got.LegacyOpenings[i].AccountID != nil {
			*got.LegacyOpenings[i].AccountID = "caller changed account"
		}
		if got.LegacyOpenings[i].ReservationID != nil {
			*got.LegacyOpenings[i].ReservationID = "caller changed reserve"
		}
	}
	for i := range got.LegacySources {
		for _, value := range []*string{got.LegacySources[i].RequestID, got.LegacySources[i].ReservationID, got.LegacySources[i].SettlementID} {
			if value != nil {
				*value = "caller changed root"
			}
		}
	}
	if !reflect.DeepEqual(in, before) {
		t.Fatal("mutable draft retained caller-owned pointer")
	}
	first := externalMustBuild(t, in)
	in.EpochID = "89abcdef-0123-4567-89ab-cdef01234567"
	second := externalMustBuild(t, in)
	ids := func(plan p.Plan) map[string]bool {
		out := map[string]bool{}
		for _, r := range plan.LegacyOpenings {
			out[r.ID] = true
		}
		for _, r := range plan.Operations {
			out[r.ID] = true
		}
		for _, r := range plan.Lots {
			out[r.ID] = true
		}
		for _, r := range plan.Events {
			out[r.ID] = true
		}
		for _, r := range plan.ReservationLots {
			out[r.ID] = true
		}
		return out
	}
	oldIDs, newIDs := ids(first), ids(second)
	if len(oldIDs) != 19 || len(newIDs) != 19 {
		t.Fatal("generated entities collided")
	}
	for id := range oldIDs {
		if newIDs[id] {
			t.Fatal("generated identity escaped epoch namespace")
		}
	}
}

func TestExternalEmptyAndZeroWallet(t *testing.T) {
	for _, tenantCount := range []int{0, 1} {
		in := p.Input{EpochID: goldenEpoch, EpochAt: externalAt(), Mode: "ordinary"}
		if tenantCount == 1 {
			in.Rows.Tenants = []v.LegacySeedTenantRow{{ID: ""}}
		}
		got := externalMustBuild(t, in)
		if len(got.LegacyOpenings) != tenantCount || len(got.Operations) != tenantCount || len(got.Heads) != tenantCount || len(got.Lots)+len(got.Events)+len(got.ReservationLots) != 0 {
			t.Fatal("zero tenant was omitted or fabricated positive rows")
		}
		if got.Metadata.GeneratedRows != uint64(3*tenantCount+2) || got.Metadata.DataRows != uint64(3*tenantCount) || got.Metadata.AvailableTotal != "0" || got.Metadata.HeldTotal != "0" {
			t.Fatal("empty hand-counted metadata")
		}
		if tenantCount == 1 && (got.LegacyOpenings[0].AccountID != nil || got.LegacyOpenings[0].Amount != "0" || got.Operations[0].EventCount != "0") {
			t.Fatal("empty wallet needs a nullable zero opening and empty seq0")
		}
	}
}

func TestExternalZeroTerminalStillHasTwoZeroLineJournals(t *testing.T) {
	in := p.Input{EpochID: goldenEpoch, EpochAt: externalAt(), Mode: "ordinary"}
	in.Rows.Tenants = []v.LegacySeedTenantRow{{ID: "zero"}}
	in.Rows.Accounts = []v.LedgerAccountRow{{ID: "zero/a", TenantID: externalPtr("zero"), Kind: "AVAILABLE", Balance: "0"}, {ID: "zero/h", TenantID: externalPtr("zero"), Kind: "HELD", Balance: "0"}}
	in.Rows.Requests = []v.WalletLotsRequestRow{{ID: "zero/request", TenantID: "zero", OwnerID: "customer", BillingStatus: "released"}}
	in.Rows.Reservations = []v.WalletLotsReservationRow{{ID: "zero/reserve", TenantID: "zero", RequestID: "zero/request", Amount: "0", Status: "released"}}
	in.Rows.Settlements = []v.WalletLotsSettlementRow{{ID: "zero/settlement", TenantID: "zero", RequestID: "zero/request", ChargedAmount: "0", RefundedAmount: "0", Status: "released", Reason: "upstream_rejected", JournalID: "zero/finish-journal"}}
	for _, row := range []struct{ id, operation, kind, actor string }{{"zero/reserve-journal", "zero/reserve", "reserve", "customer"}, {"zero/finish-journal", "zero/settlement", "released", "system"}} {
		in.Rows.Journals = append(in.Rows.Journals, v.WalletLotsJournalRow{WalletSequenceJournalRow: v.WalletSequenceJournalRow{ID: row.id, TenantID: "zero", OperationID: row.operation, Kind: row.kind, Currency: "CNY", CreatedTXID: "101"}, Reference: "zero/request", Actor: row.actor, CreatedAt: externalAt()})
		for _, account := range []string{"zero/a", "zero/h"} {
			in.Rows.Lines = append(in.Rows.Lines, v.LedgerLineRow{ID: row.id + account, JournalID: row.id, AccountID: account, SignedDelta: "0"})
		}
	}
	got := externalMustBuild(t, in)
	if got.Metadata.GeneratedRows != 8 || got.Metadata.DataRows != 6 || len(got.LegacySources) != 2 || len(got.LegacySettlements) != 1 || len(got.LegacyOpenings) != 1 {
		t.Fatal("zero terminal hand-counted membership/G")
	}
	if got.LegacyOpenings[0].AccountID == nil || *got.LegacyOpenings[0].AccountID != "zero/a" || len(got.Lots)+len(got.Events)+len(got.ReservationLots) != 0 {
		t.Fatal("zero terminal fabricated allocation or lost available binding")
	}
	for _, source := range got.LegacySources {
		if source.Amount != "0" {
			t.Fatal("zero source changed")
		}
	}
	withoutLines := externalClone(t, in)
	withoutLines.Rows.Lines = nil
	out, err := p.Build(context.Background(), withoutLines)
	if !errors.Is(err, p.ErrInput) || !reflect.DeepEqual(out, p.Plan{}) {
		t.Fatal("zero operation without its two zero lines accepted")
	}
}

func TestExternalContractRefusalsAreZero(t *testing.T) {
	cases := []struct {
		name   string
		change func(*p.Input)
	}{
		{"noncanonical_epoch", func(in *p.Input) { in.EpochID = strings.ToUpper(goldenEpoch) }},
		{"nil_epoch", func(in *p.Input) { in.EpochID = "00000000-0000-0000-0000-000000000000" }},
		{"unknown_mode", func(in *p.Input) { in.Mode = "anything" }},
		{"ordinary_sandbox", func(in *p.Input) { in.Mode = "ordinary" }},
		{"invalid_epoch_time", func(in *p.Input) { in.EpochAt.Valid = false }},
		{"submicrosecond", func(in *p.Input) { in.EpochAt.Value = in.EpochAt.Value.Add(time.Nanosecond) }},
		{"duplicate_tenant", func(in *p.Input) { in.Rows.Tenants = append(in.Rows.Tenants, in.Rows.Tenants[0]) }},
		{"unknown_tenant", func(in *p.Input) { in.Rows.Receipts[0].TenantID = "orphan" }},
		{"nul_old_text", func(in *p.Input) { in.Rows.Tenants[0].ID += "\x00" }},
		{"invalid_utf8", func(in *p.Input) { in.Rows.Tenants[0].ID = string([]byte{0xff}) }},
		{"duplicate_account", func(in *p.Input) { in.Rows.Accounts = append(in.Rows.Accounts, in.Rows.Accounts[0]) }},
		{"duplicate_account_kind", func(in *p.Input) {
			r := in.Rows.Accounts[0]
			r.ID = "second-available"
			in.Rows.Accounts = append(in.Rows.Accounts, r)
		}},
		{"cache_differs", func(in *p.Input) { in.Rows.Accounts[0].Balance = "166" }},
		{"missing_account", func(in *p.Input) { in.Rows.Accounts = in.Rows.Accounts[1:] }},
		{"account_cross_tenant", func(in *p.Input) { in.Rows.Accounts[0].TenantID = externalPtr("租户/乙") }},
		{"duplicate_receipt", func(in *p.Input) { in.Rows.Receipts = append(in.Rows.Receipts, in.Rows.Receipts[0]) }},
		{"unknown_receipt_kind", func(in *p.Input) { in.Rows.Receipts[0].SourceKind = "live_cash" }},
		{"bad_receipt_currency", func(in *p.Input) { in.Rows.Receipts[0].Currency = "USD" }},
		{"zero_receipt", func(in *p.Input) { in.Rows.Receipts[0].Amount = "0" }},
		{"noncanonical_amount", func(in *p.Input) { in.Rows.Receipts[0].Amount = "0100" }},
		{"amount_overflow", func(in *p.Input) { in.Rows.Receipts[0].Amount = "9223372036854775808" }},
		{"duplicate_request", func(in *p.Input) { in.Rows.Requests = append(in.Rows.Requests, in.Rows.Requests[0]) }},
		{"missing_reservation", func(in *p.Input) { in.Rows.Reservations = in.Rows.Reservations[1:] }},
		{"unknown_state", func(in *p.Input) { in.Rows.Reservations[1].Status = "unknown" }},
		{"cross_request", func(in *p.Input) { in.Rows.Reservations[0].RequestID = in.Rows.Requests[3].ID }},
		{"missing_terminal", func(in *p.Input) { in.Rows.Settlements = nil }},
		{"duplicate_settlement", func(in *p.Input) { in.Rows.Settlements = append(in.Rows.Settlements, in.Rows.Settlements[0]) }},
		{"refund_sum_differs", func(in *p.Input) { in.Rows.Settlements[0].RefundedAmount = "14" }},
		{"charge_exceeds_reserved", func(in *p.Input) { in.Rows.Settlements[0].ChargedAmount = "61" }},
		{"released_with_charge", func(in *p.Input) { in.Rows.Settlements[0].Status = "released" }},
		{"approved_without_approver", func(in *p.Input) { in.Rows.Refunds[0].Approver = nil }},
		{"self_approved", func(in *p.Input) { in.Rows.Refunds[0].Approver = externalPtr("maker") }},
		{"pending_with_journal", func(in *p.Input) { in.Rows.Refunds[2].JournalID = externalPtr("journal/a/refund/10") }},
		{"duplicate_refund", func(in *p.Input) { in.Rows.Refunds = append(in.Rows.Refunds, in.Rows.Refunds[0]) }},
		{"missing_source_journal", func(in *p.Input) { in.Rows.Receipts[0].JournalID = "absent" }},
		{"unexplained_zero_wallet_journal", func(in *p.Input) {
			in.Rows.Journals = append(in.Rows.Journals, v.WalletLotsJournalRow{WalletSequenceJournalRow: v.WalletSequenceJournalRow{ID: "orphan/journal", TenantID: "tenant/A", OperationID: "orphan/reserve", Kind: "reserve", Currency: "CNY", CreatedTXID: "101"}, Reference: "orphan/request", Actor: "customer", CreatedAt: externalAt()})
			in.Rows.Lines = append(in.Rows.Lines, v.LedgerLineRow{ID: "orphan/a", JournalID: "orphan/journal", AccountID: "account/a/available", SignedDelta: "0"}, v.LedgerLineRow{ID: "orphan/h", JournalID: "orphan/journal", AccountID: "account/a/held", SignedDelta: "0"})
		}},
		{"duplicate_journal_operation", func(in *p.Input) { in.Rows.Journals[1].OperationID = in.Rows.Journals[0].OperationID }},
		{"journal_reference", func(in *p.Input) { in.Rows.Journals[0].Reference = "other" }},
		{"reserve_actor", func(in *p.Input) { in.Rows.Journals[5].Actor = "other" }},
		{"unbalanced_journal", func(in *p.Input) { in.Rows.Lines[0].SignedDelta = "99" }},
		{"missing_zero_line", func(in *p.Input) {
			for i, r := range in.Rows.Lines {
				if r.JournalID == "journal/reserve/a/zero" {
					in.Rows.Lines = append(in.Rows.Lines[:i], in.Rows.Lines[i+1:]...)
					break
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := externalMixed()
			tc.change(&in)
			out, err := p.Build(context.Background(), in)
			if !errors.Is(err, p.ErrInput) || !reflect.DeepEqual(out, p.Plan{}) {
				t.Fatalf("bad seed did not return input error and zero plan: %v", err)
			}
		})
	}
}

func TestExternalContextAndResource(t *testing.T) {
	check := func(ctx context.Context, in p.Input, want error) {
		t.Helper()
		out, err := p.Build(ctx, in)
		if !errors.Is(err, want) || !reflect.DeepEqual(out, p.Plan{}) {
			t.Fatalf("expected %v and zero plan; got %v", want, err)
		}
	}
	check(nil, externalMixed(), p.ErrInput)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	check(canceled, externalMixed(), p.ErrCanceled)
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	check(deadline, externalMixed(), p.ErrTimeout)
	oversized := externalMixed()
	oversized.Rows.Tenants[0].ID = strings.Repeat("x", 65537)
	check(context.Background(), oversized, p.ErrResource)
	tooMany := p.Input{EpochID: goldenEpoch, EpochAt: externalAt(), Mode: "ordinary"}
	tooMany.Rows.Tenants = make([]v.LegacySeedTenantRow, 100001)
	check(context.Background(), tooMany, p.ErrResource)
	// 25,000 zero tenants generate 75,002 reserved rows: input+G=100,002.
	generated := p.Input{EpochID: goldenEpoch, EpochAt: externalAt(), Mode: "ordinary"}
	for n := 0; n < 25000; n++ {
		generated.Rows.Tenants = append(generated.Rows.Tenants, v.LegacySeedTenantRow{ID: time.Unix(int64(n), 0).UTC().Format(time.RFC3339)})
	}
	check(context.Background(), generated, p.ErrResource)
	projected := p.Input{EpochID: goldenEpoch, EpochAt: externalAt(), Mode: "ordinary"}
	for n := 0; n < 60; n++ {
		id := strings.Repeat("x", 65530) + time.Unix(int64(n), 0).UTC().Format("150405")
		projected.Rows.Tenants = append(projected.Rows.Tenants, v.LegacySeedTenantRow{ID: id})
	}
	check(context.Background(), projected, p.ErrResource)
}
