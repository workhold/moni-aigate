package fundinglotverify

import (
	"context"
	"fmt"
	"testing"
)

// These are historical TEXT identities, not new online API admission inputs.
func legacyTenantManual(tenant string) FundingSourceGraphRows {
	r := fundingGraphTestManual()
	r.Tenants[0].ID = tenant
	r.Accounts[0].TenantID = fundingGraphTestPtr(tenant)
	r.Journals[0].TenantID = tenant
	r.Receipts[0].TenantID = tenant
	r.Credits[0].TenantID = tenant
	for i := range r.Audits {
		r.Audits[i].TenantScope = fundingGraphTestPtr(tenant)
	}
	r.Outbox[0].TenantID = tenant
	return r
}

func legacyTenantLinks(r FundingSourceGraphRows) FundingSourceRows {
	return FundingSourceRows{
		Receipts: []FundingSourceReceiptRow{{ID: r.Receipts[0].ID, TenantID: r.Receipts[0].TenantID, SourceKind: r.Receipts[0].SourceKind, SourceID: r.Receipts[0].SourceID, Amount: r.Receipts[0].Amount, Currency: r.Receipts[0].Currency, JournalID: r.Receipts[0].JournalID}},
		Credits:  []FundingSourceCreditRow{{ID: r.Credits[0].ID, TenantID: r.Credits[0].TenantID, Amount: r.Credits[0].Amount, State: r.Credits[0].State, Requester: r.Credits[0].Requester, Approver: r.Credits[0].Approver, JournalID: r.Credits[0].JournalID}},
	}
}

func TestLegacyTenantManualFundingAndLedger(t *testing.T) {
	for _, tenant := range []string{"", "历史租户/α"} {
		t.Run(fmt.Sprintf("tenant=%q", tenant), func(t *testing.T) {
			r := legacyTenantManual(tenant)
			fundingGraphTestGood(t, r)
			links, err := CheckFundingSourceLinks(context.Background(), legacyTenantLinks(r))
			if err != nil || !links.ScopedLinksConsistent || links.Mismatches != (FundingSourceLinkMismatches{}) {
				t.Fatalf("historical source links rejected: %+v %v", links, err)
			}
			r.Accounts[0].Balance, r.Accounts[1].Balance = "53", "-53"
			ledger, err := CheckLedgerRows(context.Background(), LedgerRows{Accounts: r.Accounts, Journals: []LedgerJournalRow{{ID: r.Journals[0].ID, TenantID: tenant, OperationID: r.Journals[0].OperationID, Currency: "CNY"}}, Lines: r.Lines})
			if err != nil || !ledger.RowsConsistent {
				t.Fatalf("historical ledger rejected: %+v %v", ledger, err)
			}
		})
	}
	for name, mutate := range map[string]func(*FundingSourceGraphRows){
		"duplicate empty tenant": func(r *FundingSourceGraphRows) { r.Tenants = append(r.Tenants, r.Tenants[0]) },
		"missing tenant":         func(r *FundingSourceGraphRows) { r.Tenants = nil },
		"different tenant":       func(r *FundingSourceGraphRows) { r.Tenants[0].ID = "other" },
		"null available tenant":  func(r *FundingSourceGraphRows) { r.Accounts[0].TenantID = nil },
		"wrong receipt tenant":   func(r *FundingSourceGraphRows) { r.Receipts[0].TenantID = "other" },
		"null audit tenant":      func(r *FundingSourceGraphRows) { r.Audits[0].TenantScope = nil },
		"empty own account id":   func(r *FundingSourceGraphRows) { r.Accounts[0].ID = "" },
		"empty own source id":    func(r *FundingSourceGraphRows) { r.Credits[0].ID = "" },
		"empty business ref":     func(r *FundingSourceGraphRows) { r.Credits[0].BusinessRef = "" },
		"empty journal ref":      func(r *FundingSourceGraphRows) { r.Journals[0].Reference = "" },
		"invalid tenant utf8":    func(r *FundingSourceGraphRows) { r.Tenants[0].ID = string([]byte{0xff}) },
		"tenant nul":             func(r *FundingSourceGraphRows) { r.Tenants[0].ID = "\x00" },
	} {
		t.Run(name, func(t *testing.T) {
			r := legacyTenantManual("")
			mutate(&r)
			got, err := CheckFundingSourceGraph(context.Background(), r)
			if err == nil && got.ScopedGraphConsistent {
				t.Fatalf("broken historical relation accepted: %+v", got)
			}
		})
	}
	for _, tenant := range []string{"\x00", string([]byte{0xff})} {
		if _, err := CheckFundingSourceLinks(context.Background(), legacyTenantLinks(legacyTenantManual(tenant))); err == nil {
			t.Fatal("invalid historical tenant lexical value accepted by links")
		}
	}
}

func TestLegacyTenantCustomerRequestRefund(t *testing.T) {
	for _, tenant := range []string{"", "历史租户/α"} {
		for _, state := range []string{"reserved", "pending_reconciliation", "settled", "released"} {
			t.Run(fmt.Sprintf("%q/%s", tenant, state), func(t *testing.T) {
				charge := int64(0)
				if state == "settled" {
					charge = 60
				}
				r := customerKeyGraphFixture(state, 100, charge)
				if state == "settled" {
					customerKeyGraphTestRefund(&r, "refund", "approved", 20)
				}
				r.Tenants[0].ID, r.Keys[0].TenantID = tenant, tenant
				for i := range r.Accounts {
					if r.Accounts[i].TenantID != nil {
						r.Accounts[i].TenantID = customerKeyGraphPtr(tenant)
					}
				}
				for i := range r.Requests {
					r.Requests[i].TenantID = tenant
				}
				for i := range r.Reservations {
					r.Reservations[i].TenantID = tenant
				}
				for i := range r.Settlements {
					r.Settlements[i].TenantID = tenant
				}
				for i := range r.Refunds {
					r.Refunds[i].TenantID = tenant
				}
				for i := range r.Journals {
					r.Journals[i].TenantID = tenant
				}
				customerKeyGraphTestBalanced(t, r)
				r.Accounts[0].TenantID = nil
				if customerKeyGraphTestCheck(t, r).Balanced {
					t.Fatal("NULL platform scope substituted for tenant scope")
				}
			})
		}
	}
}

func legacyTenantBinding(tenant string) ProcurementAttemptBindingRows {
	r := validProcurementAttemptRows()
	r.Tenants[0].ID, r.Requests[0].TenantID, r.Attempts[0].TenantID = tenant, tenant, tenant
	return r
}

func legacyTenantApproval(tenant string) ProcurementCostApprovalRows {
	r := validProcurementApprovalRows()
	r.Attempts[0].TenantID, r.Corrections[0].TenantID, r.Proposals[0].TenantID = tenant, tenant, tenant
	return r
}

func legacyTenantJournal(tenant string) ProcurementCostJournalRows {
	r := validProcurementCostJournalRows()
	r.Attempts[0].TenantID, r.Corrections[0].TenantID, r.Proposals[0].TenantID = tenant, tenant, tenant
	for i := range r.Audits {
		r.Audits[i].TenantScope = procurementAttemptPtr(tenant)
	}
	r.Outbox[0].TenantID = tenant
	return r
}

func legacyTenantEvidence(tenant string) ProcurementEvidenceGraphRows {
	r := evidenceTestRows()
	evidenceTestAppendNormal(&r, evidenceTestUsage, "done", "content", "content", false)
	evidenceTestRecord(&r, "22", "contract")
	evidenceTestTerminal(&r, "cost_confirmed", "22")
	r.Tenants[0].ID, r.Requests[0].TenantID, r.Attempts[0].TenantID = tenant, tenant, tenant
	r.Metering[0].TenantID, r.Finalizations[0].TenantID, r.Outbox[0].TenantID = tenant, tenant, tenant
	f, m := r.Finalizations[0], r.Metering[0]
	input := fmt.Sprintf(`{"ID":%q,"TenantID":%q,"RequestID":%q,"AttemptID":%q,"Source":%q,"SourceEventID":%q,"Usage":%s,"ProtocolOutcome":"completed","TransportEndReason":"done","DeliveryStage":"content","FinishReason":"","ContentWritten":false,"ObservedAt":"0001-01-01T00:00:00Z"}`, f.ID, tenant, f.RequestID, *m.AttemptID, m.Source, m.SourceEventID, m.UsageJSON)
	r.Finalizations[0].InputDigest = evidenceTestHash(input)
	return r
}

func TestLegacyTenantProcurementAllGraphs(t *testing.T) {
	for _, tenant := range []string{"", "历史租户/α"} {
		t.Run(fmt.Sprintf("tenant=%q", tenant), func(t *testing.T) {
			binding, err := CheckProcurementAttemptBindingGraph(context.Background(), legacyTenantBinding(tenant))
			if err != nil || !binding.Balanced {
				t.Fatalf("binding rejected: %+v %v", binding, err)
			}
			if approval := checkProcurementApproval(t, legacyTenantApproval(tenant)); !approval.Balanced {
				t.Fatalf("approval rejected: %+v", approval)
			}
			if journal := checkProcurementCostJournal(t, legacyTenantJournal(tenant)); !journal.Balanced {
				t.Fatalf("journal rejected: %+v", journal)
			}
			if evidence := evidenceTestCheck(t, legacyTenantEvidence(tenant)); !evidence.Balanced || !evidence.CoverageComplete {
				t.Fatalf("complete metering/finalization evidence rejected: %+v", evidence)
			}
		})
	}
}

func TestLegacyTenantProcurementRelationAndIdentityBoundaries(t *testing.T) {
	for name, mutate := range map[string]func(*ProcurementAttemptBindingRows){
		"duplicate empty tenant": func(r *ProcurementAttemptBindingRows) { r.Tenants = append(r.Tenants, r.Tenants[0]) },
		"missing tenant":         func(r *ProcurementAttemptBindingRows) { r.Tenants = nil },
		"wrong request tenant":   func(r *ProcurementAttemptBindingRows) { r.Requests[0].TenantID = "other" },
		"empty own request":      func(r *ProcurementAttemptBindingRows) { r.Requests[0].ID = "" },
		"empty own attempt":      func(r *ProcurementAttemptBindingRows) { r.Attempts[0].ID = "" },
		"empty non tenant fk":    func(r *ProcurementAttemptBindingRows) { r.Attempts[0].ChannelID = "" },
		"empty nullable quote":   func(r *ProcurementAttemptBindingRows) { r.Attempts[0].CostVersionID = procurementAttemptPtr("") },
		"invalid tenant utf8":    func(r *ProcurementAttemptBindingRows) { r.Tenants[0].ID = string([]byte{0xff}) },
		"invalid tenant nul":     func(r *ProcurementAttemptBindingRows) { r.Requests[0].TenantID = "\x00" },
	} {
		t.Run(name, func(t *testing.T) {
			r := legacyTenantBinding("")
			mutate(&r)
			got, err := CheckProcurementAttemptBindingGraph(context.Background(), r)
			if err == nil && got.Balanced {
				t.Fatalf("invalid binding accepted: %+v", got)
			}
		})
	}
	for name, mutate := range map[string]func(*ProcurementCostApprovalRows){
		"wrong proposal tenant":   func(r *ProcurementCostApprovalRows) { r.Proposals[0].TenantID = "other" },
		"wrong correction tenant": func(r *ProcurementCostApprovalRows) { r.Corrections[0].TenantID = "other" },
		"empty proposal id":       func(r *ProcurementCostApprovalRows) { r.Proposals[0].ID = "" },
		"empty evidence ref":      func(r *ProcurementCostApprovalRows) { r.Proposals[0].EvidenceRef = "" },
		"invalid tenant nul":      func(r *ProcurementCostApprovalRows) { r.Proposals[0].TenantID = "\x00" },
	} {
		t.Run(name, func(t *testing.T) {
			r := legacyTenantApproval("")
			mutate(&r)
			if got := checkProcurementApproval(t, r); got.Balanced {
				t.Fatalf("invalid approval accepted: %+v", got)
			}
		})
	}
	for name, mutate := range map[string]func(*ProcurementCostJournalRows){
		"null audit scope":   func(r *ProcurementCostJournalRows) { r.Audits[0].TenantScope = nil },
		"wrong outbox scope": func(r *ProcurementCostJournalRows) { r.Outbox[0].TenantID = "other" },
		"empty operation":    func(r *ProcurementCostJournalRows) { r.Outbox[0].OperationID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			r := legacyTenantJournal("")
			mutate(&r)
			if got := checkProcurementCostJournal(t, r); got.Balanced {
				t.Fatalf("invalid journal accepted: %+v", got)
			}
		})
	}
	for name, mutate := range map[string]func(*ProcurementEvidenceGraphRows){
		"wrong meter tenant":        func(r *ProcurementEvidenceGraphRows) { r.Metering[0].TenantID = "other" },
		"wrong finalization tenant": func(r *ProcurementEvidenceGraphRows) { r.Finalizations[0].TenantID = "other" },
		"empty meter own id":        func(r *ProcurementEvidenceGraphRows) { r.Metering[0].ID = "" },
		"invalid meter tenant nul":  func(r *ProcurementEvidenceGraphRows) { r.Metering[0].TenantID = "\x00" },
		"stale finalization digest": func(r *ProcurementEvidenceGraphRows) { r.Finalizations[0].InputDigest = evidenceTestHash("old tenant") },
	} {
		t.Run(name, func(t *testing.T) {
			r := legacyTenantEvidence("")
			mutate(&r)
			if got := evidenceTestCheck(t, r); got.Balanced {
				t.Fatalf("invalid evidence accepted: %+v", got)
			}
		})
	}
}

func TestLegacyTenantDoesNotExpandVoucherScope(t *testing.T) {
	for _, tenant := range []string{"", "历史租户/α"} {
		r := fundingGraphTestFull()
		r.Commands[2].Scope = "tenant:" + tenant
		r.CommerceReceipts[2].Scope = "tenant:" + tenant
		if got := fundingGraphTestCheck(t, r); got.ScopedGraphConsistent || got.Mismatches.CommerceShapeRows == 0 {
			t.Fatalf("new voucher scope accepted non UUID tenant: %+v", got)
		}
	}
}
