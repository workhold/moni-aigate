package fundinglotverify

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func customerKeyGraphPtr(s string) *string { return &s }

func customerKeyGraphTestJournal(in *CustomerKeyGraphRows, id, operation, kind, reference, actor string, lines map[string]string) {
	in.Journals = append(in.Journals, FundingGraphJournalRow{ID: id, TenantID: "tenant", OperationID: operation, Kind: kind, Reference: reference, Actor: actor, Currency: "CNY", CreatedTXID: "7", CreatedAt: time.Unix(0, 0)})
	ids := make([]string, 0, len(lines))
	for id := range lines {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, account := range ids {
		in.Lines = append(in.Lines, LedgerLineRow{ID: fmt.Sprintf("line-%d", len(in.Lines)), JournalID: id, AccountID: account, SignedDelta: lines[account]})
	}
}

// Each simple fixture has one monetary unit per bounded input token, keeping
// expected amounts explicit and independent of the production price helper.
func customerKeyGraphFixture(state string, reserve, charge int64) CustomerKeyGraphRows {
	in := CustomerKeyGraphRows{
		Tenants: []ProcurementAttemptTenantRow{{ID: "tenant"}},
		Users:   []ProcurementApprovalUserRow{{ID: "owner"}, {ID: "checker"}},
		Keys:    []CustomerKeyRow{{ID: "key", TenantID: "tenant", OwnerID: "owner", Held: "0", Spent: "0"}},
		Accounts: []LedgerAccountRow{
			{ID: "available", TenantID: customerKeyGraphPtr("tenant"), Kind: "AVAILABLE", Balance: "0"},
			{ID: "held", TenantID: customerKeyGraphPtr("tenant"), Kind: "HELD", Balance: "0"},
			{ID: "revenue", Kind: "REVENUE", Balance: "0"},
		},
	}
	customerKeyGraphTestRequest(&in, "", state, reserve, charge)
	return in
}
func customerKeyGraphTestRequest(in *CustomerKeyGraphRows, suffix, state string, reserve, charge int64) {
	n, c := strconv.FormatInt(reserve, 10), strconv.FormatInt(charge, 10)
	request, reservation, settlement := "request"+suffix, "reservation"+suffix, "settlement"+suffix
	in.Requests = append(in.Requests, CustomerKeyRequestRow{ID: request, TenantID: "tenant", KeyID: "key", OwnerID: "owner", BillingStatus: state, InputRate: "1000000", OutputRate: "0", InputUpperBound: n, OutputLimit: "0"})
	in.Reservations = append(in.Reservations, CustomerKeyReservationRow{ID: reservation, TenantID: "tenant", RequestID: request, Amount: n, Status: state})
	customerKeyGraphTestJournal(in, "reserve-journal"+suffix, reservation, "reserve", request, "owner", map[string]string{"available": strconv.FormatInt(-reserve, 10), "held": n})
	if reserve == 0 {
		in.Lines[len(in.Lines)-2].SignedDelta = "0"
	}
	if state == "reserved" || state == "pending_reconciliation" {
		old, _ := new(big.Int).SetString(in.Keys[0].Held, 10)
		in.Keys[0].Held = old.Add(old, big.NewInt(reserve)).String()
		return
	}
	reason := "charged_usage"
	if state == "released" {
		reason = "released_not_sent"
	}
	in.Settlements = append(in.Settlements, CustomerKeySettlementRow{ID: settlement, TenantID: "tenant", RequestID: request, ChargedAmount: c, RawRetailAmount: c, WaivedRetailAmount: "0", RefundedAmount: "0", Status: state, Reason: reason, JournalID: "finish-journal" + suffix})
	lines := map[string]string{"held": strconv.FormatInt(-reserve, 10), "available": strconv.FormatInt(reserve-charge, 10)}
	if reserve == 0 {
		lines["held"] = "0"
	}
	if charge > 0 {
		lines["revenue"] = c
	}
	customerKeyGraphTestJournal(in, "finish-journal"+suffix, settlement, state, request, "system", lines)
	old, _ := new(big.Int).SetString(in.Keys[0].Spent, 10)
	in.Keys[0].Spent = old.Add(old, big.NewInt(charge)).String()
}
func customerKeyGraphTestRefund(in *CustomerKeyGraphRows, id, state string, amount int64) {
	r := CustomerKeyRefundRow{ID: id, TenantID: "tenant", SettlementID: "settlement", BusinessRef: "business-" + id, Amount: strconv.FormatInt(amount, 10), State: state, Requester: "owner"}
	if state == "approved" {
		r.Approver, r.JournalID = customerKeyGraphPtr("checker"), customerKeyGraphPtr("journal-"+id)
		customerKeyGraphTestJournal(in, *r.JournalID, id, "refund", r.SettlementID, "checker", map[string]string{"available": r.Amount, "revenue": strconv.FormatInt(-amount, 10)})
		n, _ := new(big.Int).SetString(in.Settlements[0].RefundedAmount, 10)
		in.Settlements[0].RefundedAmount = n.Add(n, big.NewInt(amount)).String()
	}
	in.Refunds = append(in.Refunds, r)
}
func customerKeyGraphTestCheck(t *testing.T, in CustomerKeyGraphRows) CustomerKeyGraphCheck {
	t.Helper()
	got, err := CheckCustomerKeyGraph(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope != "customer_key_financial_graph_v1" {
		t.Fatalf("scope=%q", got.Scope)
	}
	return got
}
func customerKeyGraphTestBalanced(t *testing.T, in CustomerKeyGraphRows) {
	t.Helper()
	if got := customerKeyGraphTestCheck(t, in); !got.Balanced || got.Mismatches != (CustomerKeyGraphMismatches{}) {
		t.Fatalf("unexpected mismatch: %+v", got)
	}
}

func TestCheckCustomerKeyGraphValidFinancialStates(t *testing.T) {
	customerKeyGraphTestBalanced(t, CustomerKeyGraphRows{})
	for _, state := range []string{"reserved", "pending_reconciliation", "settled", "released"} {
		t.Run(state, func(t *testing.T) {
			charge := int64(60)
			if state != "settled" {
				charge = 0
			}
			in := customerKeyGraphFixture(state, 100, charge)
			in.Keys[0].LifetimeLimit = customerKeyGraphPtr("100")
			customerKeyGraphTestBalanced(t, in)
		})
	}
	for _, state := range []string{"reserved", "pending_reconciliation", "settled", "released"} {
		t.Run("zero_"+state, func(t *testing.T) { customerKeyGraphTestBalanced(t, customerKeyGraphFixture(state, 0, 0)) })
	}
	t.Run("zero_key_without_requests", func(t *testing.T) {
		in := customerKeyGraphFixture("reserved", 0, 0)
		in.Requests, in.Reservations, in.Journals, in.Lines = nil, nil, nil, nil
		customerKeyGraphTestBalanced(t, in)
	})
	for _, reason := range []string{"released_deadline", "released_no_delivery", "released_not_sent", "released_rejected_no_consumption", "released_no_capacity", "released_authorization_revoked", "released_platform_failure", "released_provider_failure", "released_reconciliation"} {
		t.Run(reason, func(t *testing.T) {
			in := customerKeyGraphFixture("released", 100, 0)
			in.Settlements[0].Reason = reason
			customerKeyGraphTestBalanced(t, in)
		})
	}
	t.Run("full_waived_numeric100", func(t *testing.T) {
		in := customerKeyGraphFixture("settled", 100, 100)
		in.Settlements[0].RawRetailAmount = "1" + strings.Repeat("0", 99)
		in.Settlements[0].WaivedRetailAmount = strings.Repeat("9", 97) + "00"
		customerKeyGraphTestBalanced(t, in)
	})
	t.Run("refunds_and_unbounded_pending_queue", func(t *testing.T) {
		in := customerKeyGraphFixture("settled", 100, 60)
		customerKeyGraphTestRefund(&in, "refund1", "approved", 35)
		customerKeyGraphTestRefund(&in, "refund2", "approved", 25)
		customerKeyGraphTestRefund(&in, "pending1", "pending", 60)
		customerKeyGraphTestRefund(&in, "pending2", "pending", 60)
		customerKeyGraphTestRequest(&in, "held", "pending_reconciliation", 40, 0)
		customerKeyGraphTestBalanced(t, in)
		in.Keys[0].Spent = "0"
		got := customerKeyGraphTestCheck(t, in)
		if got.Mismatches.KeySpentRows != 1 {
			t.Fatalf("refund must not reduce key spent: %+v", got)
		}
	})
	t.Run("arbitrary_and_empty_text_identity", func(t *testing.T) {
		in := customerKeyGraphFixture("reserved", 0, 0)
		in.Tenants[0].ID, in.Users[0].ID = "", ""
		in.Keys[0].ID, in.Keys[0].TenantID, in.Keys[0].OwnerID = "", "", ""
		in.Requests[0].ID, in.Requests[0].TenantID, in.Requests[0].KeyID, in.Requests[0].OwnerID = "", "", "", ""
		in.Reservations[0].ID, in.Reservations[0].TenantID, in.Reservations[0].RequestID = "", "", ""
		for i := 0; i < 2; i++ {
			in.Accounts[i].TenantID = customerKeyGraphPtr("")
		}
		in.Journals[0].ID, in.Journals[0].TenantID, in.Journals[0].OperationID, in.Journals[0].Reference, in.Journals[0].Actor = "", "", "", "", ""
		for i := range in.Lines {
			in.Lines[i].JournalID = ""
		}
		customerKeyGraphTestBalanced(t, in)
		in.Keys[0].ID, in.Requests[0].KeyID = "历史 key / 非 UUID "+strings.Repeat("k", 1000), "历史 key / 非 UUID "+strings.Repeat("k", 1000)
		customerKeyGraphTestBalanced(t, in)
	})
}

func TestCheckCustomerKeyGraphCeilAndLargeCumulative(t *testing.T) {
	in := customerKeyGraphFixture("reserved", 2, 0)
	in.Requests[0].InputRate, in.Requests[0].OutputRate, in.Requests[0].InputUpperBound, in.Requests[0].OutputLimit = "999999", "2", "1", "1"
	customerKeyGraphTestBalanced(t, in) // 1,000,001 / 1,000,000 rounds upward to 2.
	in.Reservations[0].Amount = "1"
	if got := customerKeyGraphTestCheck(t, in); got.Mismatches.ReservationAmountRows != 1 {
		t.Fatalf("ceil not checked: %+v", got)
	}
	in = customerKeyGraphFixture("reserved", 9223372036854775807, 0)
	customerKeyGraphTestBalanced(t, in)
	customerKeyGraphTestRequest(&in, "extra", "reserved", 1, 0)
	in.Keys[0].Held = "9223372036854775807"
	got := customerKeyGraphTestCheck(t, in)
	if got.Mismatches.KeyHeldRows != 1 || got.Mismatches.InvalidAmountRows != 0 {
		t.Fatalf("cumulative sum truncated: %+v", got)
	}
	in = customerKeyGraphFixture("settled", 9223372036854775807, 9223372036854775807)
	customerKeyGraphTestRequest(&in, "extra", "settled", 1, 1)
	in.Keys[0].Spent = "9223372036854775807"
	if got = customerKeyGraphTestCheck(t, in); got.Mismatches.KeySpentRows != 1 {
		t.Fatalf("spent cumulative truncated: %+v", got)
	}
	in = customerKeyGraphFixture("reserved", 100, 0)
	in.Requests[0].InputRate, in.Requests[0].InputUpperBound = "9223372036854775807", "9223372036854775807"
	if got = customerKeyGraphTestCheck(t, in); got.Mismatches.ReservationAmountRows != 1 {
		t.Fatalf("unrepresentable R accepted: %+v", got)
	}
}

func TestCheckCustomerKeyGraphIdentityStateAndAmountMismatches(t *testing.T) {
	cases := []struct {
		name, field string
		mutate      func(*CustomerKeyGraphRows)
	}{
		{"key_tenant", "KeyIdentityRows", func(r *CustomerKeyGraphRows) { r.Keys[0].TenantID = "absent" }},
		{"key_owner", "KeyIdentityRows", func(r *CustomerKeyGraphRows) { r.Keys[0].OwnerID = "absent" }},
		{"request_tenant", "RequestIdentityRows", func(r *CustomerKeyGraphRows) { r.Requests[0].TenantID = "absent" }},
		{"request_owner", "RequestIdentityRows", func(r *CustomerKeyGraphRows) { r.Requests[0].OwnerID = "checker" }},
		{"request_key", "RequestIdentityRows", func(r *CustomerKeyGraphRows) { r.Requests[0].KeyID = "absent" }},
		{"request_missing_reservation", "RequestReservationRows", func(r *CustomerKeyGraphRows) { r.Reservations = nil }},
		{"reservation_orphan", "ReservationIdentityRows", func(r *CustomerKeyGraphRows) { r.Reservations[0].RequestID = "absent" }},
		{"reservation_tenant", "ReservationIdentityRows", func(r *CustomerKeyGraphRows) { r.Reservations[0].TenantID = "other" }},
		{"reservation_upper", "ReservationAmountRows", func(r *CustomerKeyGraphRows) { r.Reservations[0].Amount = "101" }},
		{"billing_status", "RequestFinancialStateRows", func(r *CustomerKeyGraphRows) { r.Requests[0].BillingStatus = "reserved" }},
		{"settlement_orphan", "SettlementIdentityRows", func(r *CustomerKeyGraphRows) { r.Settlements[0].RequestID = "absent" }},
		{"settlement_tenant", "SettlementIdentityRows", func(r *CustomerKeyGraphRows) { r.Settlements[0].TenantID = "other" }},
		{"settlement_terminal", "SettlementStateRows", func(r *CustomerKeyGraphRows) { r.Reservations[0].Status = "pending_reconciliation" }},
		{"settlement_reason", "SettlementStateRows", func(r *CustomerKeyGraphRows) { r.Settlements[0].Reason = "released_deadline" }},
		{"settlement_min", "SettlementAmountRows", func(r *CustomerKeyGraphRows) {
			r.Settlements[0].RawRetailAmount = "100"
			r.Settlements[0].WaivedRetailAmount = "40"
		}},
		{"settlement_raw", "SettlementAmountRows", func(r *CustomerKeyGraphRows) { r.Settlements[0].WaivedRetailAmount = "1" }},
		{"refund_orphan", "RefundIdentityRows", func(r *CustomerKeyGraphRows) { r.Refunds[0].SettlementID = "absent" }},
		{"refund_tenant", "RefundIdentityRows", func(r *CustomerKeyGraphRows) { r.Refunds[0].TenantID = "other" }},
		{"refund_requester", "RefundIdentityRows", func(r *CustomerKeyGraphRows) { r.Refunds[0].Requester = "absent" }},
		{"refund_self", "RefundShapeRows", func(r *CustomerKeyGraphRows) { r.Refunds[0].Approver = customerKeyGraphPtr("owner") }},
		{"refund_checker", "RefundShapeRows", func(r *CustomerKeyGraphRows) { r.Refunds[0].Approver = customerKeyGraphPtr("absent") }},
		{"refund_null", "RefundShapeRows", func(r *CustomerKeyGraphRows) { r.Refunds[0].JournalID = nil }},
		{"refund_too_large", "RefundShapeRows", func(r *CustomerKeyGraphRows) { r.Refunds[0].Amount = "61" }},
		{"refund_total", "SettlementRefundedRows", func(r *CustomerKeyGraphRows) { r.Settlements[0].RefundedAmount = "59" }},
		{"key_held", "KeyHeldRows", func(r *CustomerKeyGraphRows) { r.Keys[0].Held = "1" }},
		{"key_spent", "KeySpentRows", func(r *CustomerKeyGraphRows) { r.Keys[0].Spent = "59" }},
		{"key_limit", "KeyLimitRows", func(r *CustomerKeyGraphRows) { r.Keys[0].LifetimeLimit = customerKeyGraphPtr("59") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := customerKeyGraphFixture("settled", 100, 60)
			customerKeyGraphTestRefund(&in, "refund", "approved", 60)
			tc.mutate(&in)
			got := customerKeyGraphTestCheck(t, in)
			if got.Balanced || reflect.ValueOf(got.Mismatches).FieldByName(tc.field).Uint() != 1 {
				t.Fatalf("missing %s: %+v", tc.field, got)
			}
		})
	}
	t.Run("unfinished_cannot_have_settlement", func(t *testing.T) {
		in := customerKeyGraphFixture("settled", 100, 60)
		in.Requests[0].BillingStatus, in.Reservations[0].Status = "reserved", "reserved"
		if got := customerKeyGraphTestCheck(t, in); got.Mismatches.RequestFinancialStateRows != 1 || got.Mismatches.SettlementStateRows != 1 {
			t.Fatalf("unfinished settlement: %+v", got)
		}
	})
	t.Run("pending_nullable_not_empty", func(t *testing.T) {
		in := customerKeyGraphFixture("settled", 100, 60)
		customerKeyGraphTestRefund(&in, "pending", "pending", 60)
		in.Refunds[0].JournalID = customerKeyGraphPtr("")
		if got := customerKeyGraphTestCheck(t, in); got.Mismatches.RefundShapeRows != 1 {
			t.Fatalf("empty text became SQL NULL: %+v", got)
		}
	})
	t.Run("approved_refund_sum_exceeds_charge", func(t *testing.T) {
		in := customerKeyGraphFixture("settled", 100, 60)
		customerKeyGraphTestRefund(&in, "r1", "approved", 40)
		customerKeyGraphTestRefund(&in, "r2", "approved", 40)
		if got := customerKeyGraphTestCheck(t, in); got.Mismatches.SettlementRefundedRows != 1 || got.Mismatches.SettlementAmountRows != 1 {
			t.Fatalf("refund cap: %+v", got)
		}
	})
}

func TestCheckCustomerKeyGraphJournalBothDirections(t *testing.T) {
	for _, index := range []int{0, 1, 2} {
		for _, field := range []string{"TenantID", "OperationID", "Kind", "Reference", "Actor", "Currency"} {
			t.Run(fmt.Sprintf("header_%d_%s", index, field), func(t *testing.T) {
				in := customerKeyGraphFixture("settled", 100, 60)
				customerKeyGraphTestRefund(&in, "refund", "approved", 60)
				reflect.ValueOf(&in.Journals[index]).Elem().FieldByName(field).SetString("wrong")
				got := customerKeyGraphTestCheck(t, in)
				n := []uint64{got.Mismatches.ReservationJournalRows, got.Mismatches.SettlementJournalRows, got.Mismatches.RefundJournalRows}[index]
				if n != 1 || got.Balanced {
					t.Fatalf("header effect: %+v", got)
				}
			})
		}
	}
	for _, kind := range []string{"reserve", "settled", "released", "refund"} {
		t.Run("orphan_"+kind, func(t *testing.T) {
			in := customerKeyGraphFixture("settled", 100, 60)
			customerKeyGraphTestJournal(&in, "orphan", "orphan-operation", kind, "request", "system", nil)
			got := customerKeyGraphTestCheck(t, in)
			if got.Mismatches.JournalSourceRows != 1 {
				t.Fatalf("reverse orphan: %+v", got)
			}
		})
	}
	for _, mutate := range []func(*CustomerKeyGraphRows){
		func(r *CustomerKeyGraphRows) { r.Lines = r.Lines[1:] },
		func(r *CustomerKeyGraphRows) { r.Lines[0].SignedDelta = "0" },
		func(r *CustomerKeyGraphRows) { r.Lines[0].AccountID = "absent" },
		func(r *CustomerKeyGraphRows) { r.Accounts[0].TenantID = customerKeyGraphPtr("other") },
	} {
		in := customerKeyGraphFixture("reserved", 100, 0)
		mutate(&in)
		got := customerKeyGraphTestCheck(t, in)
		if got.Mismatches.ReservationJournalRows != 1 || got.Mismatches.JournalSourceRows != 1 {
			t.Fatalf("missing/incorrect line: %+v", got)
		}
	}
	t.Run("extra_zero_line_is_not_ignored", func(t *testing.T) {
		in := customerKeyGraphFixture("reserved", 0, 0)
		in.Lines = append(in.Lines, LedgerLineRow{ID: "extra", JournalID: in.Journals[0].ID, AccountID: "revenue", SignedDelta: "0"})
		if got := customerKeyGraphTestCheck(t, in); got.Mismatches.ReservationJournalRows != 1 {
			t.Fatalf("extra zero: %+v", got)
		}
	})
	t.Run("zero_settlement_requires_exactly_two_lines", func(t *testing.T) {
		in := customerKeyGraphFixture("settled", 100, 0)
		in.Lines = append(in.Lines, LedgerLineRow{ID: "extra", JournalID: in.Settlements[0].JournalID, AccountID: "revenue", SignedDelta: "0"})
		if got := customerKeyGraphTestCheck(t, in); got.Mismatches.SettlementJournalRows != 1 {
			t.Fatalf("zero C revenue: %+v", got)
		}
	})
	t.Run("pending_effect_of_any_kind", func(t *testing.T) {
		in := customerKeyGraphFixture("settled", 100, 60)
		customerKeyGraphTestRefund(&in, "pending", "pending", 60)
		customerKeyGraphTestJournal(&in, "bad", "pending", "other", "", "", nil)
		if got := customerKeyGraphTestCheck(t, in); got.Mismatches.PendingRefundEffectRows != 1 {
			t.Fatalf("pending effect hidden by kind: %+v", got)
		}
	})
	t.Run("unrelated_journal_and_dangling_line", func(t *testing.T) {
		in := customerKeyGraphFixture("reserved", 100, 0)
		customerKeyGraphTestJournal(&in, "other", "other-op", "other-kind", "", "", nil)
		customerKeyGraphTestBalanced(t, in)
		in.Lines = append(in.Lines, LedgerLineRow{ID: "dangling", JournalID: "absent", AccountID: "available", SignedDelta: "0"})
		if got := customerKeyGraphTestCheck(t, in); got.Mismatches.LineIdentityRows != 1 {
			t.Fatalf("dangling line dropped: %+v", got)
		}
	})
}

func TestCheckCustomerKeyGraphDuplicateKeysDoNotAmplify(t *testing.T) {
	cases := []struct {
		name, field string
		mutate      func(*CustomerKeyGraphRows)
	}{
		{"tenant", "DuplicateTenantRows", func(r *CustomerKeyGraphRows) { r.Tenants = append(r.Tenants, r.Tenants[0]) }},
		{"user", "DuplicateUserRows", func(r *CustomerKeyGraphRows) { r.Users = append(r.Users, r.Users[0]) }},
		{"key", "DuplicateKeyRows", func(r *CustomerKeyGraphRows) { r.Keys = append(r.Keys, r.Keys[0]) }},
		{"request", "DuplicateRequestRows", func(r *CustomerKeyGraphRows) { r.Requests = append(r.Requests, r.Requests[0]) }},
		{"reservation", "DuplicateReservationRows", func(r *CustomerKeyGraphRows) { r.Reservations = append(r.Reservations, r.Reservations[0]) }},
		{"reservation_request", "DuplicateReservationRequestRows", func(r *CustomerKeyGraphRows) {
			v := r.Reservations[0]
			v.ID = "other"
			r.Reservations = append(r.Reservations, v)
		}},
		{"settlement", "DuplicateSettlementRows", func(r *CustomerKeyGraphRows) { r.Settlements = append(r.Settlements, r.Settlements[0]) }},
		{"settlement_request", "DuplicateSettlementRequestRows", func(r *CustomerKeyGraphRows) {
			v := r.Settlements[0]
			v.ID = "other"
			r.Settlements = append(r.Settlements, v)
		}},
		{"refund", "DuplicateRefundRows", func(r *CustomerKeyGraphRows) { r.Refunds = append(r.Refunds, r.Refunds[0]) }},
		{"refund_business", "DuplicateRefundBusinessRefRows", func(r *CustomerKeyGraphRows) { v := r.Refunds[0]; v.ID = "other"; r.Refunds = append(r.Refunds, v) }},
		{"account", "DuplicateAccountRows", func(r *CustomerKeyGraphRows) { r.Accounts = append(r.Accounts, r.Accounts[0]) }},
		{"account_kind", "DuplicateAccountKindRows", func(r *CustomerKeyGraphRows) { v := r.Accounts[0]; v.ID = "other"; r.Accounts = append(r.Accounts, v) }},
		{"journal", "DuplicateJournalRows", func(r *CustomerKeyGraphRows) { r.Journals = append(r.Journals, r.Journals[0]) }},
		{"journal_operation", "DuplicateJournalOperationRows", func(r *CustomerKeyGraphRows) { v := r.Journals[0]; v.ID = "other"; r.Journals = append(r.Journals, v) }},
		{"line", "DuplicateLineRows", func(r *CustomerKeyGraphRows) { r.Lines = append(r.Lines, r.Lines[0]) }},
		{"journal_account", "DuplicateJournalAccountRows", func(r *CustomerKeyGraphRows) { v := r.Lines[0]; v.ID = "other"; r.Lines = append(r.Lines, v) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := customerKeyGraphFixture("settled", 100, 60)
			customerKeyGraphTestRefund(&in, "refund", "approved", 60)
			tc.mutate(&in)
			got := customerKeyGraphTestCheck(t, in)
			var want CustomerKeyGraphMismatches
			reflect.ValueOf(&want).Elem().FieldByName(tc.field).SetUint(1)
			if got.Balanced || got.Mismatches != want {
				t.Fatalf("duplicate amplified graph: got=%+v want=%+v", got.Mismatches, want)
			}
		})
	}
	t.Run("unrelated_empty_operation_and_typed_pairs", func(t *testing.T) {
		in := CustomerKeyGraphRows{Journals: []FundingGraphJournalRow{{ID: "a", OperationID: "", Kind: "other"}, {ID: "b", OperationID: "", Kind: "another"}}}
		if got := customerKeyGraphTestCheck(t, in); got.Mismatches != (CustomerKeyGraphMismatches{DuplicateJournalOperationRows: 1}) {
			t.Fatalf("empty operation uniqueness: %+v", got)
		}
		in = CustomerKeyGraphRows{Accounts: []LedgerAccountRow{{ID: "a", Kind: "same", Balance: "0"}, {ID: "b", TenantID: customerKeyGraphPtr(""), Kind: "same", Balance: "0"}}}
		customerKeyGraphTestBalanced(t, in) // NULL tenant and empty TEXT tenant differ.
	})
}

func TestCheckCustomerKeyGraphLexicalAndUncomputableTotals(t *testing.T) {
	for _, value := range []string{"", "00", "01", "+1", "-0", "-1", "1.0", "1e1", " 1", "9223372036854775808"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			in := customerKeyGraphFixture("reserved", 100, 0)
			in.Reservations[0].Amount = value
			got := customerKeyGraphTestCheck(t, in)
			if got.Mismatches.InvalidAmountRows != 1 || got.Mismatches.KeyHeldRows != 0 {
				t.Fatalf("invalid R became partial sum: %+v", got)
			}
		})
	}
	t.Run("invalid_refund_does_not_become_zero", func(t *testing.T) {
		in := customerKeyGraphFixture("settled", 100, 60)
		customerKeyGraphTestRefund(&in, "r", "approved", 60)
		in.Refunds[0].Amount = "bad"
		got := customerKeyGraphTestCheck(t, in)
		if got.Mismatches.InvalidAmountRows != 1 || got.Mismatches.SettlementRefundedRows != 0 {
			t.Fatalf("invalid refund became zero: %+v", got)
		}
	})
	for _, mutate := range []func(*CustomerKeyGraphRows){
		func(r *CustomerKeyGraphRows) { r.Keys[0].OwnerID = "\xff" },
		func(r *CustomerKeyGraphRows) { r.Requests[0].KeyID = "key\x00" },
		func(r *CustomerKeyGraphRows) { r.Journals[0].CreatedTXID = "\xff" },
	} {
		in := customerKeyGraphFixture("reserved", 100, 0)
		mutate(&in)
		got := customerKeyGraphTestCheck(t, in)
		if got.Mismatches.InvalidTextRows != 1 {
			t.Fatalf("text not counted once: %+v", got)
		}
	}
	for _, mutate := range []func(*CustomerKeyGraphRows){
		func(r *CustomerKeyGraphRows) { r.Settlements[0].RawRetailAmount = strings.Repeat("9", 101) },
		func(r *CustomerKeyGraphRows) { r.Keys[0].LifetimeLimit = customerKeyGraphPtr("-1") },
		func(r *CustomerKeyGraphRows) { r.Lines[0].SignedDelta = "-0" },
		func(r *CustomerKeyGraphRows) { r.Accounts[0].Balance = "9223372036854775808" },
	} {
		in := customerKeyGraphFixture("settled", 100, 60)
		mutate(&in)
		got := customerKeyGraphTestCheck(t, in)
		if got.Mismatches.InvalidAmountRows != 1 {
			t.Fatalf("amount not counted once: %+v", got)
		}
	}
	for _, mutate := range []func(*CustomerKeyGraphRows){
		func(r *CustomerKeyGraphRows) { r.Requests[0].BillingStatus = "unknown" },
		func(r *CustomerKeyGraphRows) { r.Reservations[0].Status = "unknown" },
		func(r *CustomerKeyGraphRows) { r.Settlements[0].Status = "unknown" },
		func(r *CustomerKeyGraphRows) { r.Refunds[0].State = "unknown" },
	} {
		in := customerKeyGraphFixture("settled", 100, 60)
		customerKeyGraphTestRefund(&in, "r", "pending", 60)
		mutate(&in)
		got := customerKeyGraphTestCheck(t, in)
		if got.Mismatches.InvalidEnumRows != 1 {
			t.Fatalf("enum: %+v", got)
		}
	}
}

type customerKeyGraphCancelContext struct {
	context.Context
	calls, after int
	cause        error
}

func (c *customerKeyGraphCancelContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		return c.cause
	}
	return nil
}

func TestCheckCustomerKeyGraphResourceAndContextZeroResults(t *testing.T) {
	checkError := func(ctx context.Context, rows CustomerKeyGraphRows, want error) {
		t.Helper()
		got, err := CheckCustomerKeyGraph(ctx, rows)
		if !errors.Is(err, want) || got != (CustomerKeyGraphCheck{}) {
			t.Fatalf("expected zero + %v, got %+v / %v", want, got, err)
		}
	}
	checkError(nil, CustomerKeyGraphRows{}, ErrCustomerKeyGraphRowsInput)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	checkError(canceled, customerKeyGraphFixture("reserved", 100, 0), ErrCustomerKeyGraphRowsCanceled)
	expired, end := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer end()
	checkError(expired, CustomerKeyGraphRows{}, ErrCustomerKeyGraphRowsTimeout)
	checkError(context.Background(), CustomerKeyGraphRows{Tenants: make([]ProcurementAttemptTenantRow, 50000), Users: make([]ProcurementApprovalUserRow, 50001)}, ErrCustomerKeyGraphRowsResource)
	_, err := CheckCustomerKeyGraph(context.Background(), CustomerKeyGraphRows{Tenants: make([]ProcurementAttemptTenantRow, 100000)})
	if err != nil {
		t.Fatalf("row boundary: %v", err)
	}
	rows := CustomerKeyGraphRows{Tenants: []ProcurementAttemptTenantRow{{ID: strings.Repeat("a", 8<<20)}}, Users: []ProcurementApprovalUserRow{{ID: strings.Repeat("b", 8<<20)}}}
	customerKeyGraphTestBalanced(t, rows)
	rows.Users[0].ID += "x"
	checkError(context.Background(), rows, ErrCustomerKeyGraphRowsResource)
	rows = CustomerKeyGraphRows{Tenants: make([]ProcurementAttemptTenantRow, 1000)}
	for i := range rows.Tenants {
		rows.Tenants[i].ID = strconv.Itoa(i)
	}
	for _, after := range []int{2, 5, 9} {
		checkError(&customerKeyGraphCancelContext{Context: context.Background(), after: after, cause: context.Canceled}, rows, ErrCustomerKeyGraphRowsCanceled)
		checkError(&customerKeyGraphCancelContext{Context: context.Background(), after: after, cause: context.DeadlineExceeded}, rows, ErrCustomerKeyGraphRowsTimeout)
	}
}
