package fundinglotverify

import (
	"encoding/json"
	"strconv"
	"strings"
)

func (g *sandboxGraphData) environment() {
	envs := sandboxGraphRows[SandboxGraphEnvironmentRow](g, "Environments")
	proofs := sandboxGraphRows[SandboxGraphAttestationRow](g, "Attestations")
	if len(envs) != 1 {
		g.bad("EnvironmentRows", "Environments", "singleton")
	} else {
		e := envs[0]
		g.env = e
		if !e.Singleton || !fundingGraphUUID(e.ID) || !sandboxGraphRef(e.MerchantID) || !sandboxGraphLabel(e.PayeeLabel) || !sandboxGraphUint(e.DatabaseOID, 32, true) || !sandboxGraphUint(e.SchemaOID, 32, true) || !sandboxGraphHex(e.NonceDigest) || e.ProtocolVersion != "1" || e.AppRole == "" || e.WorkerRole == "" || e.ProjectorRole == "" || e.AppRole == e.WorkerRole || e.AppRole == e.ProjectorRole || e.WorkerRole == e.ProjectorRole {
			g.bad("EnvironmentRows", "Environments", "singleton")
		}
	}
	if len(proofs) != 1 {
		g.bad("AttestationRows", "Attestations", "singleton")
	} else {
		a := proofs[0]
		var p sandboxGraphProofWire
		valid := g.strict(a.ProofCanonical, &p, 65536, 12, 4096) && sandboxGraphSHA(a.ProofCanonical) == a.ProofSHA256 && sandboxGraphHex(a.ProofSHA256) && a.Singleton && a.EnvironmentID == g.env.ID && a.DatabaseOID == g.env.DatabaseOID && a.SchemaOID == g.env.SchemaOID && a.AppNonceDigest == g.env.NonceDigest && a.ProofVersion == "1"
		for _, s := range []string{a.SchemaOwnerOID, a.AppRoleOID, a.WorkerRoleOID, a.ProjectorRoleOID} {
			valid = valid && sandboxGraphUint(s, 32, true)
		}
		valid = valid && a.AppRoleOID != a.WorkerRoleOID && a.AppRoleOID != a.ProjectorRoleOID && a.WorkerRoleOID != a.ProjectorRoleOID && a.SchemaOwnerOID != a.AppRoleOID && a.SchemaOwnerOID != a.WorkerRoleOID && a.SchemaOwnerOID != a.ProjectorRoleOID
		valid = valid && p.ProofVersion == 1 && p.ProtocolVersion == 1 && p.EnvironmentID == g.env.ID && p.MerchantID == g.env.MerchantID && p.PayeeLabel == g.env.PayeeLabel && strconv.FormatUint(uint64(p.Database.OID), 10) == a.DatabaseOID && strconv.FormatUint(uint64(p.App.OID), 10) == a.SchemaOID && strconv.FormatUint(uint64(p.App.Owner.OID), 10) == a.SchemaOwnerOID && strconv.FormatUint(uint64(p.App.Runtime.OID), 10) == a.AppRoleOID && strconv.FormatUint(uint64(p.Worker.OID), 10) == a.WorkerRoleOID && strconv.FormatUint(uint64(p.Projector.OID), 10) == a.ProjectorRoleOID && p.App.Runtime.Name == g.env.AppRole && p.Worker.Name == g.env.WorkerRole && p.Projector.Name == g.env.ProjectorRole && p.AppNonceDigest == a.AppNonceDigest && p.WorkerNonceDigest == a.WorkerNonceDigest
		valid = valid && sandboxGraphHex(p.AppNonceDigest) && sandboxGraphHex(p.WorkerNonceDigest) && sandboxGraphHex(p.APITokenDigest) && p.Database.OID > 0 && p.Database.Port > 0 && p.Database.Host != "" && p.Database.Name != "" && p.APIOrigin != "" && p.ControlOrigin != "" && p.AppListenOrigin != "" && p.CallbackURL != "" && !p.App.Owner.Login && p.App.Runtime.Login && p.Worker.Login && p.Projector.Login && len(p.Keys) >= 1 && len(p.Keys) <= 128
		seenOID := map[uint32]bool{}
		for _, r := range []sandboxGraphRoleWire{p.App.Owner, p.App.Runtime, p.Mock.Owner, p.Mock.Runtime, p.Receiver.Owner, p.Receiver.Runtime, p.Worker, p.Projector} {
			if !g.tick() {
				return
			}
			if r.Name == "" || r.OID == 0 || seenOID[r.OID] {
				valid = false
			}
			seenOID[r.OID] = true
		}
		for _, s := range []sandboxGraphSchemaWire{p.App, p.Mock, p.Receiver} {
			valid = valid && s.Name != "" && s.OID > 0 && !s.Owner.Login && s.Runtime.Login
		}
		last := ""
		for _, k := range p.Keys {
			if !g.tick() {
				return
			}
			before, bok := sandboxGraphProofInstant(k.NotBefore)
			after, aok := sandboxGraphProofInstant(k.NotAfter)
			if !sandboxGraphKeyVersion(k.ID) || !sandboxGraphHex(k.Digest) || !bok || !aok || after.Before(before) || g.keys[k.ID] || last != "" && k.ID <= last {
				valid = false
			}
			g.keys[k.ID] = true
			last = k.ID
		}
		if !valid {
			g.bad("AttestationRows", "Attestations", "singleton")
		}
	}
	policies := sandboxGraphRows[SandboxGraphPolicyRow](g, "Policies")
	if len(policies) != 1 || !policies[0].Singleton || !sandboxGraphMoney(policies[0].Revision) {
		g.bad("PolicyRows", "Policies", "singleton")
	}
}
func sandboxGraphPaymentKind(s string) bool {
	return sandboxGraphHas(s, "payment.sku.publish", "payment.sku.activate", "payment.order.create", "payment.order.cancel")
}
func sandboxGraphVoucherKind(s string) bool {
	return sandboxGraphHas(s, "voucher.batch.create", "voucher.batch.approve", "voucher.batch.void", "voucher.revocation.create", "voucher.revocation.approve", "voucher.redeem")
}
func (g *sandboxGraphData) audit(action, target string) []ProcurementAuditRow {
	return sandboxGraphRelated[ProcurementAuditRow](g, "Audits", []string{"Action", "Target"}, action, target)
}
func (g *sandboxGraphData) commerce() {
	for _, c := range sandboxGraphRows[FundingGraphCommerceCommandRow](g, "Commands") {
		if !g.tick() {
			return
		}
		r, ok := sandboxGraphGet[FundingGraphCommerceReceiptRow](g, "CommerceReceipts", c.OperationID)
		if !g.exists("Users", c.Actor) {
			g.bad("IdentityRows", "Commands", c.OperationID)
		}
		if !sandboxGraphPaymentKind(c.Kind) && !sandboxGraphVoucherKind(c.Kind) {
			g.bad("InvalidEnumRows", "Commands", c.OperationID)
		}
		if !ok || r.Kind != c.Kind || r.Actor != c.Actor || r.Scope != c.Scope || !r.CreatedAt.Equal(c.CreatedAt) {
			g.bad("CommerceTerminalRows", "Commands", c.OperationID)
		}
		if !sandboxGraphPaymentKind(c.Kind) {
			continue
		}
		platform := strings.HasPrefix(c.Kind, "payment.sku.")
		scopeOK := c.Scope == "platform"
		if !platform {
			scopeOK = strings.HasPrefix(c.Scope, "tenant:") && g.exists("Tenants", strings.TrimPrefix(c.Scope, "tenant:"))
		}
		if !fundingGraphUUID(c.OperationID) || c.FingerprintKeyVersion != nil || !sandboxGraphHex(c.RequestDigest) || !scopeOK {
			g.bad("CommerceShapeRows", "Commands", c.OperationID)
		}
		if !ok {
			continue
		}
		shape := r.Amount == nil && r.FundingReceiptID == nil && r.JournalID == nil
		if r.Status == "accepted" {
			shape = shape && r.Reason == "accepted" && r.ResourceID != nil
		} else if r.Status == "rejected" {
			shape = shape && r.ResourceID == nil
			switch c.Kind {
			case "payment.sku.publish", "payment.sku.activate":
				shape = shape && sandboxGraphHas(r.Reason, "payment_revision_conflict", "payment_state_conflict")
			case "payment.order.create":
				shape = shape && sandboxGraphHas(r.Reason, "payments_disabled", "payment_sku_unavailable")
			case "payment.order.cancel":
				shape = shape && r.Reason == "payment_state_conflict"
			}
		} else {
			shape = false
		}
		if !shape {
			g.bad("CommerceTerminalRows", "CommerceReceipts", r.OperationID)
		}
		audits := g.audit(c.Kind, c.OperationID)
		if r.Status == "accepted" {
			if len(audits) != 1 {
				g.bad("HumanAuditRows", "Commands", c.OperationID)
			}
			for _, a := range audits {
				valid := a.Actor == c.Actor && strings.TrimSpace(a.Diff) == "{}"
				if platform {
					valid = valid && a.TenantScope == nil
				} else {
					valid = valid && a.TenantScope != nil && *a.TenantScope == strings.TrimPrefix(c.Scope, "tenant:")
				}
				if !valid {
					g.bad("HumanAuditRows", "Audits", a.ID)
				}
			}
		} else if len(audits) != 0 {
			g.bad("HumanAuditRows", "Commands", c.OperationID)
		}
	}
	for _, r := range sandboxGraphRows[FundingGraphCommerceReceiptRow](g, "CommerceReceipts") {
		if !g.tick() {
			return
		}
		if !g.exists("Commands", r.OperationID) {
			g.bad("CommerceTerminalRows", "CommerceReceipts", r.OperationID)
		}
	}
}
func (g *sandboxGraphData) accepted(operation, kind, resource, actor, tenant string) bool {
	c, cok := sandboxGraphGet[FundingGraphCommerceCommandRow](g, "Commands", operation)
	r, rok := sandboxGraphGet[FundingGraphCommerceReceiptRow](g, "CommerceReceipts", operation)
	scope := "platform"
	switch kind {
	case "payment.order.create", "payment.order.cancel":
		scope = "tenant:" + tenant
	case "payment.sku.publish", "payment.sku.activate":
	default:
		return false
	}
	return cok && rok && c.Kind == kind && c.Actor == actor && c.Scope == scope && r.Status == "accepted" && r.Reason == "accepted" && r.ResourceID != nil && *r.ResourceID == resource && r.Kind == kind && r.Actor == actor && r.Scope == scope
}
func (g *sandboxGraphData) digest(c FundingGraphCommerceCommandRow, target string, body any) bool {
	raw, e := json.Marshal(struct {
		V        int     `json:"v"`
		Actor    string  `json:"actor"`
		Scope    string  `json:"scope"`
		Kind     string  `json:"kind"`
		TargetID *string `json:"target_id"`
		Body     any     `json:"body"`
	}{1, c.Actor, c.Scope, c.Kind, &target, body})
	return e == nil && sandboxGraphSHA(raw) == c.RequestDigest
}
func (g *sandboxGraphData) skusAndOrders() {
	usedChanges := map[string]bool{}
	usedVersions := map[string]bool{}
	for _, s := range sandboxGraphRows[SandboxGraphSKURow](g, "SKUs") {
		if !g.tick() {
			return
		}
		valid := fundingGraphUUID(s.ID) && sandboxGraphMoney(s.Revision) && sandboxGraphMoney(s.LastVersion) && g.exists("Users", s.CreatedBy) && s.LastChangeOperationID != nil && (!s.Enabled || s.CurrentVersionID != nil)
		var chain []SandboxGraphSKUChangeRow
		seen := map[string]bool{}
		ptr := s.LastChangeOperationID
		for ptr != nil {
			if !g.tick() {
				return
			}
			c, ok := sandboxGraphGet[SandboxGraphSKUChangeRow](g, "SKUChanges", *ptr)
			if !ok || c.SKUID != s.ID || seen[*ptr] {
				valid = false
				break
			}
			seen[*ptr] = true
			usedChanges[*ptr] = true
			chain = append(chain, c)
			ptr = c.BeforeOperationID
		}
		var version int64
		revision := "0"
		var active *string
		enabled := false
		for j := len(chain) - 1; j >= 0; j-- {
			if !g.tick() {
				return
			}
			c := chain[j]
			stepOK := c.BeforeRevision == revision && sandboxGraphSame(c.BeforeVersionID, active) && c.BeforeEnabled == enabled && sandboxGraphUint(c.CreatedXID, 64, true)
			cmd, ok := sandboxGraphGet[FundingGraphCommerceCommandRow](g, "Commands", c.OperationID)
			if !ok || !sandboxGraphHas(c.Kind, "payment.sku.publish", "payment.sku.activate") {
				stepOK = false
			}
			if c.Kind == "payment.sku.publish" {
				v, vok := sandboxGraphGet[SandboxGraphSKUVersionRow](g, "SKUVersions", sandboxGraphPtr(c.PublishedVersionID))
				version++
				want := sandboxGraphN(revision) + 1
				stepOK = stepOK && vok && v.SKUID == s.ID && v.PublishOperationID == c.OperationID && v.Version == strconv.FormatInt(version, 10) && c.AfterRevision == strconv.FormatInt(want, 10) && sandboxGraphSame(c.AfterVersionID, active) && c.AfterEnabled == enabled && g.accepted(c.OperationID, c.Kind, v.ID, v.CreatedBy, "")
				if j == len(chain)-1 {
					stepOK = stepOK && s.CreatedBy == v.CreatedBy
				}
				usedVersions[v.ID] = true
				body := struct {
					ExpectedRevision string `json:"expected_revision"`
					DisplayName      string `json:"display_name"`
					Amount           string `json:"amount"`
				}{c.BeforeRevision, v.DisplayName, v.Amount}
				stepOK = stepOK && g.digest(cmd, s.ID, body)
			} else if c.Kind == "payment.sku.activate" {
				changed := !sandboxGraphSame(c.BeforeVersionID, c.AfterVersionID) || c.BeforeEnabled != c.AfterEnabled
				want := sandboxGraphN(revision)
				if changed {
					want++
				}
				stepOK = stepOK && version > 0 && c.PublishedVersionID == nil && c.AfterRevision == strconv.FormatInt(want, 10) && (!c.AfterEnabled || c.AfterVersionID != nil) && g.accepted(c.OperationID, c.Kind, s.ID, cmd.Actor, "")
				if c.AfterVersionID != nil {
					v, ok := sandboxGraphGet[SandboxGraphSKUVersionRow](g, "SKUVersions", *c.AfterVersionID)
					stepOK = stepOK && ok && v.SKUID == s.ID
				}
				body := struct {
					ExpectedRevision string  `json:"expected_revision"`
					VersionID        *string `json:"version_id"`
					Enabled          bool    `json:"enabled"`
				}{c.BeforeRevision, c.AfterVersionID, c.AfterEnabled}
				stepOK = stepOK && g.digest(cmd, s.ID, body)
			}
			if !stepOK {
				valid = false
				g.bad("SKUChainRows", "SKUs", s.ID)
			}
			revision, active, enabled = c.AfterRevision, c.AfterVersionID, c.AfterEnabled
		}
		valid = valid && len(chain) > 0 && chain[len(chain)-1].Kind == "payment.sku.publish" && s.Revision == revision && s.LastVersion == strconv.FormatInt(version, 10) && sandboxGraphSame(s.CurrentVersionID, active) && s.Enabled == enabled
		if !valid {
			g.bad("SKURootRows", "SKUs", s.ID)
		}
	}
	for _, v := range sandboxGraphRows[SandboxGraphSKUVersionRow](g, "SKUVersions") {
		if !g.tick() {
			return
		}
		if !fundingGraphUUID(v.ID) || !g.exists("SKUs", v.SKUID) || !sandboxGraphMoney(v.Version) || !sandboxGraphLabel(v.DisplayName) || !sandboxGraphMoney(v.Amount) || v.Amount != v.WalletCreditAmount || v.Currency != "CNY" || v.CheckoutTTLSeconds != "900" || v.EnvironmentID != g.env.ID || v.MerchantID != g.env.MerchantID || v.PayeeLabel != g.env.PayeeLabel || !g.exists("Users", v.CreatedBy) || !usedVersions[v.ID] {
			g.bad("SKUVersionRows", "SKUVersions", v.ID)
		}
	}
	for _, c := range sandboxGraphRows[SandboxGraphSKUChangeRow](g, "SKUChanges") {
		if !g.tick() {
			return
		}
		if !usedChanges[c.OperationID] {
			g.bad("SKUChainRows", "SKUs", c.SKUID)
		}
	}
	for _, o := range sandboxGraphRows[SandboxGraphOrderRow](g, "Orders") {
		if !g.tick() {
			return
		}
		v, ok := sandboxGraphGet[SandboxGraphSKUVersionRow](g, "SKUVersions", o.SKUVersionID)
		c, cok := sandboxGraphGet[FundingGraphCommerceCommandRow](g, "Commands", o.ID)
		body := struct {
			SKUVersionID string `json:"sku_version_id"`
		}{o.SKUVersionID}
		valid := fundingGraphUUID(o.ID) && ok && cok && g.exists("Tenants", o.TenantID) && g.exists("Users", o.Creator) && o.DisplayName == v.DisplayName && o.Amount == v.Amount && o.WalletCreditAmount == v.WalletCreditAmount && o.Currency == v.Currency && o.EnvironmentID == v.EnvironmentID && o.MerchantID == v.MerchantID && o.PayeeLabel == v.PayeeLabel && o.MerchantOrderID == "mps1."+o.EnvironmentID+"."+o.ID && o.ExpiresAt.Equal(o.CreatedAt.Add(900000000000)) && g.accepted(o.ID, "payment.order.create", o.ID, o.Creator, o.TenantID) && g.digest(c, o.SKUVersionID, body)
		if !valid {
			g.bad("OrderRows", "Orders", o.ID)
		}
	}
	for _, x := range sandboxGraphRows[SandboxGraphCancelIntentRow](g, "CancelIntents") {
		if !g.tick() {
			return
		}
		o, ok := sandboxGraphGet[SandboxGraphOrderRow](g, "Orders", x.OrderID)
		c, cok := sandboxGraphGet[FundingGraphCommerceCommandRow](g, "Commands", x.OperationID)
		if !ok || !cok || !g.exists("Users", x.Actor) || !g.accepted(x.OperationID, "payment.order.cancel", o.ID, x.Actor, o.TenantID) || !g.digest(c, o.ID, struct{}{}) {
			g.bad("CancelIntentRows", "CancelIntents", x.OrderID)
		}
	}
	// Rejected commands have no persisted target/body. Sweep effects rather than
	// reconstructing an invented request or judging today's mutable sale state.
	for _, c := range sandboxGraphRows[FundingGraphCommerceCommandRow](g, "Commands") {
		if !g.tick() {
			return
		}
		if !sandboxGraphPaymentKind(c.Kind) {
			continue
		}
		r, ok := sandboxGraphGet[FundingGraphCommerceReceiptRow](g, "CommerceReceipts", c.OperationID)
		if !ok {
			continue
		}
		effects := 0
		switch c.Kind {
		case "payment.sku.publish", "payment.sku.activate":
			if g.exists("SKUChanges", c.OperationID) {
				effects++
			}
		case "payment.order.create":
			if g.exists("Orders", c.OperationID) {
				effects++
			}
		case "payment.order.cancel":
			effects = len(sandboxGraphRelated[SandboxGraphCancelIntentRow](g, "CancelIntents", []string{"OperationID"}, c.OperationID))
		}
		if r.Status == "accepted" && effects != 1 || r.Status == "rejected" && effects != 0 {
			g.bad("CommerceResourceRows", "Commands", c.OperationID)
		}
	}
}
