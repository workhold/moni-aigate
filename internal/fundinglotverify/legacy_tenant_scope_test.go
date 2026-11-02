package fundinglotverify

import (
	"fmt"
	"testing"
)

func legacyTenantPeriodicScope(tenant string, key bool) PeriodicBudgetGraphRows {
	r := pbgBase()
	r.Tenants[0].ID = tenant
	r.Keys[0].TenantID = tenant
	r.Controls[0].TenantID = pbgPtr(tenant)
	if key {
		r.Controls[0].Scope = "key"
		r.Controls[0].KeyID = pbgPtr(r.Keys[0].ID)
	}
	r.Operations, r.Versions, r.Audits = nil, nil, nil
	pbgCommand(&r, "0", true, r.Controls[0].TrackingStartedAt)
	return r
}

func TestLegacyTenantPeriodicScope(t *testing.T) {
	for _, tenant := range []string{"", "租户:旧身份/α"} {
		for _, key := range []bool{false, true} {
			t.Run(fmt.Sprintf("tenant=%q/key=%t", tenant, key), func(t *testing.T) {
				r := legacyTenantPeriodicScope(tenant, key)
				got := pbgClean(t, r)
				if got.Status != "balanced" || !got.Balanced || !got.CoverageComplete {
					t.Fatalf("legacy tenant configuration not fully proven: %+v", got)
				}
				if key && r.Operations[0].Scope != tenant {
					t.Fatal("key configure lost exact tenant scope")
				}
			})
		}
	}

	for name, mutate := range map[string]func(*PeriodicBudgetGraphRows){
		"missing tenant": func(r *PeriodicBudgetGraphRows) { r.Tenants = nil },
		"wrong tenant":   func(r *PeriodicBudgetGraphRows) { r.Tenants[0].ID = "different" },
		"duplicate empty tenant": func(r *PeriodicBudgetGraphRows) {
			r.Tenants = append(r.Tenants, r.Tenants[0])
		},
		"nil is not empty tenant": func(r *PeriodicBudgetGraphRows) { r.Controls[0].TenantID = nil },
		"empty control UUID":      func(r *PeriodicBudgetGraphRows) { r.Controls[0].ID = "" },
		"empty operation UUID":    func(r *PeriodicBudgetGraphRows) { r.Operations[0].OperationID = "" },
		"wrong key tenant":        func(r *PeriodicBudgetGraphRows) { r.Keys[0].TenantID = "different" },
		"platform is not empty key scope": func(r *PeriodicBudgetGraphRows) {
			r.Operations[0].Scope = "platform"
		},
		"empty key target UUID": func(r *PeriodicBudgetGraphRows) {
			r.Keys[0].ID = ""
			r.Controls[0].KeyID = pbgPtr("")
			r.Operations, r.Versions, r.Audits = nil, nil, nil
			pbgCommand(r, "0", true, r.Controls[0].TrackingStartedAt)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := legacyTenantPeriodicScope("", true)
			mutate(&r)
			got := pbgCheck(t, r)
			if got.Mismatches == (PeriodicBudgetGraphMismatches{}) || got.Balanced {
				t.Fatalf("invalid legacy tenant relation accepted: %+v", got)
			}
		})
	}
	t.Run("empty procurement target UUID", func(t *testing.T) {
		r := pbgSupplier()
		r.Controls[0].AccountID = pbgPtr("")
		r.Accounts[0].ID = ""
		r.Operations, r.Versions, r.Audits = nil, nil, nil
		pbgCommand(&r, "0", true, r.Controls[0].TrackingStartedAt)
		if got := pbgCheck(t, r); got.Mismatches.InvalidUUIDRows == 0 || got.Balanced {
			t.Fatalf("empty procurement target lost UUID restriction: %+v", got)
		}
	})
}

func legacyTenantSandboxScope(t *testing.T, tenant string) SandboxSourceGraphRows {
	t.Helper()
	r := sgtCapture(t)
	r.Tenants[0].ID = tenant
	r.Orders[0].TenantID = tenant
	r.Identities[0].TenantID = tenant
	r.Captures[0].TenantID = tenant
	r.Receipts[0].TenantID = tenant
	r.Journals[0].TenantID = tenant
	r.Accounts[0].TenantID = sgtPtr(tenant)
	r.Outbox[0].TenantID = tenant
	for i := range r.Audits {
		if r.Audits[i].TenantScope != nil {
			r.Audits[i].TenantScope = sgtPtr(tenant)
		}
	}
	legacyTenantSandboxCommandScope(&r, 2, "tenant:"+tenant)
	return r
}

// Reframe the original command, rather than retaining a stale digest after a
// scope change. No business effect or other original is changed by this helper.
func legacyTenantSandboxCommandScope(r *SandboxSourceGraphRows, i int, scope string) {
	c := &r.Commands[i]
	c.Scope = scope
	r.CommerceReceipts[i].Scope = scope
	var target, body string
	switch c.Kind {
	case "payment.order.create":
		target = r.Orders[0].SKUVersionID
		body = fmt.Sprintf(`{"sku_version_id":%q}`, target)
	case "payment.sku.publish":
		target = r.SKUs[0].ID
		body = `{"expected_revision":"0","display_name":"测试 SKU","amount":"100"}`
	default:
		panic("unsupported fixture command")
	}
	envelope := fmt.Sprintf(`{"v":1,"actor":"operator","scope":%q,"kind":%q,"target_id":%q,"body":%s}`, scope, c.Kind, target, body)
	c.RequestDigest = sgtHash([]byte(envelope))
}

func TestLegacyTenantSandboxScope(t *testing.T) {
	for _, tenant := range []string{"", "租户:旧身份/α"} {
		t.Run(fmt.Sprintf("tenant=%q", tenant), func(t *testing.T) {
			r := legacyTenantSandboxScope(t, tenant)
			sgtValid(t, r)
			if r.Commands[0].Scope != "platform" || r.Commands[1].Scope != "platform" || r.Commands[2].Scope != "tenant:"+tenant {
				t.Fatal("platform SKU or exact tenant order scope changed")
			}
			if r.Accounts[0].TenantID == nil || *r.Accounts[0].TenantID != tenant || r.Accounts[1].TenantID != nil {
				t.Fatal("tenant wallet and platform clearing were conflated")
			}
			o := r.Orders[0]
			op, command := sgtID(700), sgtID(701)
			sgtCommand(&r, op, "payment.order.cancel", "tenant:"+tenant, o.ID, o.ID, `{}`)
			r.CancelIntents = []SandboxGraphCancelIntentRow{{OrderID: o.ID, OperationID: op, Actor: "operator", CreatedAt: sgtAt()}}
			raw := []byte(fmt.Sprintf(`{"schema_version":1,"environment_id":%q,"merchant_id":%q,"command_id":%q,"merchant_order_id":%q}`, o.EnvironmentID, o.MerchantID, command, o.MerchantOrderID))
			r.CommandOutbox = append(r.CommandOutbox, SandboxGraphCommandOutboxRow{ID: sgtID(702), OrderID: o.ID, Kind: "cancel", ExternalCommandID: &command, CanonicalParameters: &raw, ParameterDigest: sgtPtr(sgtHash(raw)), State: "pending", Generation: "0", AttemptCount: "0", NextAttemptAt: sgtAt(), CreatedAt: sgtAt()})
			sgtValid(t, r)
		})
	}
	for name, mutate := range map[string]func(*SandboxSourceGraphRows){
		"missing tenant": func(r *SandboxSourceGraphRows) { r.Tenants = nil },
		"wrong capture tenant": func(r *SandboxSourceGraphRows) {
			r.Captures[0].TenantID = "different"
		},
		"duplicate empty tenant": func(r *SandboxSourceGraphRows) {
			r.Tenants = append(r.Tenants, r.Tenants[0])
		},
		"nil is not empty wallet tenant": func(r *SandboxSourceGraphRows) { r.Accounts[0].TenantID = nil },
		"empty tenant is not platform clearing": func(r *SandboxSourceGraphRows) {
			r.Accounts[1].TenantID = sgtPtr("")
		},
		"empty tenant audit is not platform": func(r *SandboxSourceGraphRows) { r.Audits[2].TenantScope = nil },
		"platform order command": func(r *SandboxSourceGraphRows) {
			legacyTenantSandboxCommandScope(r, 2, "platform")
			r.Audits[2].TenantScope = nil
		},
		"tenant SKU command": func(r *SandboxSourceGraphRows) {
			legacyTenantSandboxCommandScope(r, 0, "tenant:")
			r.Audits[0].TenantScope = sgtPtr("")
		},
		"empty order UUID":    func(r *SandboxSourceGraphRows) { r.Orders[0].ID = "" },
		"empty identity UUID": func(r *SandboxSourceGraphRows) { r.Identities[0].ID = "" },
		"empty capture UUID":  func(r *SandboxSourceGraphRows) { r.Captures[0].ID = "" },
		"empty command UUID":  func(r *SandboxSourceGraphRows) { r.Commands[2].OperationID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			r := legacyTenantSandboxScope(t, "")
			mutate(&r)
			got := sgtCheck(t, r)
			if got.Mismatches == (SandboxSourceGraphMismatches{}) || got.ScopedGraphConsistent {
				t.Fatalf("invalid legacy tenant relation accepted: %+v", got)
			}
		})
	}
}
