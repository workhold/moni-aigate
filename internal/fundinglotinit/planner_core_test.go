package fundinglotinit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	v "github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func coreAt() v.WalletLotsTime {
	return v.WalletLotsTime{Valid: true, Value: time.Date(2026, 11, 3, 0, 0, 0, 123456000, time.UTC)}
}

// Independently calculated: 100 funding -20 unfinished -25 charge +10 refund
// leaves AVAILABLE=65, HELD=20, REVENUE=15. Extra zero tenants exercise ordering
// and ownership without relying on a live database or production fixture helper.
func coreSeed() Input {
	in := Input{EpochID: "01234567-89ab-4cde-8fab-0123456789ab", EpochAt: coreAt(), Mode: "ordinary"}
	in.Rows.Tenants = []v.LegacySeedTenantRow{{ID: "z"}, {ID: "alpha"}, {ID: ""}, {ID: "é"}, {ID: "e\u0301"}}
	in.Rows.Accounts = []v.LedgerAccountRow{
		{ID: "available", TenantID: pointer("alpha"), Kind: "AVAILABLE", Balance: "65"},
		{ID: "held", TenantID: pointer("alpha"), Kind: "HELD", Balance: "20"},
		{ID: "funding", Kind: "FUNDING_CLEARING", Balance: "-100"},
		{ID: "revenue", Kind: "REVENUE", Balance: "15"},
	}
	type entry struct{ account, delta string }
	addJournal := func(id, operation, kind, reference, actor string, entries ...entry) {
		in.Rows.Journals = append(in.Rows.Journals, v.WalletLotsJournalRow{WalletSequenceJournalRow: v.WalletSequenceJournalRow{ID: id, TenantID: "alpha", OperationID: operation, Kind: kind, Currency: "CNY", CreatedTXID: "7"}, Reference: reference, Actor: actor, CreatedAt: coreAt()})
		for i, e := range entries {
			in.Rows.Lines = append(in.Rows.Lines, v.LedgerLineRow{ID: fmt.Sprintf("%s/%d", id, i), JournalID: id, AccountID: e.account, SignedDelta: e.delta})
		}
	}
	addJournal("fund", "credit", "credit", "credit", "finance", entry{"available", "100"}, entry{"funding", "-100"})
	addJournal("reserve-open", "open", "reserve", "request-open", "customer", entry{"available", "-20"}, entry{"held", "20"})
	addJournal("reserve-terminal", "terminal", "reserve", "request-terminal", "customer", entry{"available", "-50"}, entry{"held", "50"})
	addJournal("finish", "settlement", "settled", "request-terminal", "system", entry{"available", "25"}, entry{"held", "-50"}, entry{"revenue", "25"})
	addJournal("refund-journal", "refund", "refund", "settlement", "checker", entry{"available", "10"}, entry{"revenue", "-10"})
	in.Rows.Receipts = []v.WalletLotsReceiptRow{{ID: "receipt", TenantID: "alpha", SourceKind: "manual_credit", SourceID: "credit", Amount: "100", Currency: "CNY", JournalID: "fund", CreatedAt: coreAt()}}
	in.Rows.Requests = []v.WalletLotsRequestRow{{ID: "request-open", TenantID: "alpha", OwnerID: "customer", BillingStatus: "reserved"}, {ID: "request-terminal", TenantID: "alpha", OwnerID: "customer", BillingStatus: "settled"}}
	in.Rows.Reservations = []v.WalletLotsReservationRow{{ID: "open", TenantID: "alpha", RequestID: "request-open", Amount: "20", Status: "reserved"}, {ID: "terminal", TenantID: "alpha", RequestID: "request-terminal", Amount: "50", Status: "settled"}}
	in.Rows.Settlements = []v.WalletLotsSettlementRow{{ID: "settlement", TenantID: "alpha", RequestID: "request-terminal", ChargedAmount: "25", RefundedAmount: "10", Status: "settled", Reason: "usage_known", JournalID: "finish"}}
	in.Rows.Refunds = []v.WalletLotsRefundRow{{ID: "refund", TenantID: "alpha", SettlementID: "settlement", Amount: "10", State: "approved", Requester: "maker", Approver: pointer("checker"), JournalID: pointer("refund-journal")}, {ID: "pending", TenantID: "alpha", SettlementID: "settlement", Amount: "5", State: "pending", Requester: "maker"}}
	return in
}

// The deterministic context makes every observed checkpoint an independently
// cancellable schedule. There are no sleeps, races, test-only production hooks,
// or assumptions about a particular implementation phase's check count.
type checkpointContext struct {
	checks, after int
	failure       error
}

func (*checkpointContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*checkpointContext) Done() <-chan struct{}       { return nil }
func (*checkpointContext) Value(any) any               { return nil }
func (c *checkpointContext) Err() error {
	c.checks++
	if c.after > 0 && c.checks >= c.after {
		return c.failure
	}
	return nil
}

func TestCoreEveryCancellationCheckpointReturnsZero(t *testing.T) {
	in := coreSeed()
	baseline := &checkpointContext{}
	plan, err := Build(baseline, in)
	if err != nil || plan.Metadata.AvailableTotal != "65" || plan.Metadata.HeldTotal != "20" {
		t.Fatalf("valid baseline: %v", err)
	}
	if baseline.checks < 100 {
		t.Fatal("fixture did not exercise substantial checks")
	}
	for cut := 1; cut <= baseline.checks; cut++ {
		ctx := &checkpointContext{after: cut, failure: context.Canceled}
		out, err := Build(ctx, in)
		if !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(out, Plan{}) {
			t.Fatalf("checkpoint %d/%d: %v, nonzero=%v", cut, baseline.checks, err, !reflect.DeepEqual(out, Plan{}))
		}
	}
	for _, cut := range []int{1, baseline.checks / 4, baseline.checks / 2, baseline.checks - 1, baseline.checks} {
		ctx := &checkpointContext{after: cut, failure: context.DeadlineExceeded}
		out, err := Build(ctx, in)
		if !errors.Is(err, ErrTimeout) || !reflect.DeepEqual(out, Plan{}) {
			t.Fatalf("deadline checkpoint %d: %v", cut, err)
		}
	}
}

func TestCoreRecognizedResourceIsNotReplacedByLaterCancellation(t *testing.T) {
	in := coreSeed()
	in.Rows.Tenants[0].ID = strings.Repeat("x", fieldLimit+1)
	ctx := &checkpointContext{after: 2, failure: context.Canceled}
	out, err := Build(ctx, in)
	if !errors.Is(err, ErrResource) || !reflect.DeepEqual(out, Plan{}) || ctx.checks != 1 {
		t.Fatalf("field resource precedence: %v, checks=%d", err, ctx.checks)
	}
	in = coreSeed()
	in.Rows.Tenants = make([]v.LegacySeedTenantRow, 100001)
	ctx = &checkpointContext{after: 2, failure: context.DeadlineExceeded}
	out, err = Build(ctx, in)
	if !errors.Is(err, ErrResource) || !reflect.DeepEqual(out, Plan{}) || ctx.checks != 1 {
		t.Fatalf("row resource precedence: %v, checks=%d", err, ctx.checks)
	}
}

func corePlatformSeed() Input {
	in := Input{EpochID: "01234567-89ab-4cde-8fab-0123456789ab", EpochAt: coreAt(), Mode: "ordinary"}
	in.Rows.Tenants = []v.LegacySeedTenantRow{{ID: ""}}
	in.Rows.Accounts = []v.LedgerAccountRow{{ID: "funding", Kind: "FUNDING_CLEARING", Balance: "-5"}, {ID: "revenue", Kind: "REVENUE", Balance: "5"}}
	in.Rows.Journals = []v.WalletLotsJournalRow{{WalletSequenceJournalRow: v.WalletSequenceJournalRow{ID: "", TenantID: "", OperationID: "", Kind: "platform-transfer", Currency: "CNY", CreatedTXID: "1"}, CreatedAt: coreAt()}}
	in.Rows.Lines = []v.LedgerLineRow{{ID: "one", JournalID: "", AccountID: "funding", SignedDelta: "-5"}, {ID: "two", JournalID: "", AccountID: "revenue", SignedDelta: "5"}}
	return in
}

func TestCoreNonWalletJournalsStillRequireCompleteLedgerConsistency(t *testing.T) {
	in := corePlatformSeed()
	got, err := Build(context.Background(), in)
	if err != nil || got.Metadata.AvailableTotal != "0" || got.Metadata.HeldTotal != "0" || len(got.LegacySources) != 0 || len(got.Lots) != 0 {
		t.Fatalf("valid platform journal: %v", err)
	}
	cases := []struct {
		name   string
		change func(*Input)
	}{
		{"cache_mismatch", func(in *Input) { in.Rows.Accounts[0].Balance = "-6" }},
		{"unbalanced", func(in *Input) { in.Rows.Lines[0].SignedDelta = "-6"; in.Rows.Accounts[0].Balance = "-6" }},
		{"dangling_account", func(in *Input) { in.Rows.Lines[0].AccountID = "absent" }},
		{"dangling_journal", func(in *Input) { in.Rows.Lines[0].JournalID = "absent" }},
		{"duplicate_line_id", func(in *Input) { in.Rows.Lines[1].ID = in.Rows.Lines[0].ID }},
		{"duplicate_journal_account", func(in *Input) { in.Rows.Lines[1].AccountID = in.Rows.Lines[0].AccountID }},
		{"unknown_tenant", func(in *Input) { in.Rows.Journals[0].TenantID = "missing" }},
		{"invalid_txid", func(in *Input) { in.Rows.Journals[0].CreatedTXID = "0" }},
		{"signed_noncanonical", func(in *Input) { in.Rows.Lines[0].SignedDelta = "-05" }},
		{"signed_underflow", func(in *Input) { in.Rows.Accounts[0].Balance = "-9223372036854775809" }},
		{"unknown_account_kind", func(in *Input) { in.Rows.Accounts[0].Kind = "OTHER" }},
		{"positive_grant_clearing", func(in *Input) { in.Rows.Accounts[1].Kind = "OPERATING_GRANT_CLEARING" }},
		{"platform_has_tenant", func(in *Input) { in.Rows.Accounts[0].TenantID = pointer("") }},
		{"duplicate_platform_kind", func(in *Input) { in.Rows.Accounts[1].Kind = "FUNDING_CLEARING" }},
		{"wallet_kind_without_source", func(in *Input) { in.Rows.Journals[0].Kind = "credit" }},
		{"invalid_journal_time", func(in *Input) { in.Rows.Journals[0].CreatedAt.Infinity = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := corePlatformSeed()
			tc.change(&in)
			out, err := Build(context.Background(), in)
			if !errors.Is(err, ErrInput) || !reflect.DeepEqual(out, Plan{}) {
				t.Fatalf("bad nonwallet ledger accepted: %v", err)
			}
		})
	}
}

func TestCoreSignedLedgerArithmeticDoesNotOverflowAtIntermediateSums(t *testing.T) {
	in := corePlatformSeed()
	in.Rows.Accounts[0].Balance = "0"
	in.Rows.Accounts[1].Balance = "0"
	in.Rows.Journals = nil
	in.Rows.Lines = nil
	// Each cache fits BIGINT while the first two postings' running sums do not.
	for n, delta := range []string{"9223372036854775807", "9223372036854775807", "-9223372036854775807", "-9223372036854775807"} {
		id := fmt.Sprintf("platform/%d", n)
		opposite := "-9223372036854775807"
		if strings.HasPrefix(delta, "-") {
			opposite = "9223372036854775807"
		}
		in.Rows.Journals = append(in.Rows.Journals, v.WalletLotsJournalRow{WalletSequenceJournalRow: v.WalletSequenceJournalRow{ID: id, TenantID: "", OperationID: id, Kind: "platform-transfer", Currency: "CNY", CreatedTXID: "1"}, CreatedAt: coreAt()})
		in.Rows.Lines = append(in.Rows.Lines, v.LedgerLineRow{ID: id + "/funding", JournalID: id, AccountID: "funding", SignedDelta: delta}, v.LedgerLineRow{ID: id + "/revenue", JournalID: id, AccountID: "revenue", SignedDelta: opposite})
	}
	if _, err := Build(context.Background(), in); err != nil {
		t.Fatalf("exact wide ledger rejected: %v", err)
	}
}
