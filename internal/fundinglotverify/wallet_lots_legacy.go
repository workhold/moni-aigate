package fundinglotverify

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
)

func wlWalletDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := range s {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}
func (w *wlWork) legacySource(kind, id string) *wlSource {
	if s := w.sources[wlKey{kind, id}]; s != nil {
		return s
	}
	if kind == "consumption_refund" {
		return w.sources[wlKey{"legacy_consumption_refund", id}]
	}
	return nil
}
func (w *wlWork) legacyTenant(tenant string, ref wlRef) bool {
	t, _, ok := wlGet[WalletSequenceTenantRow](w, "Tenants", tenant)
	if !ok || t.CreatedXID != nil {
		w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
		w.taint(tenant)
		return false
	}
	return true
}
func (w *wlWork) legacyEpoch(epoch string, ref wlRef) bool {
	if !w.epochKnown || epoch != w.epoch.ID {
		w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
		return false
	}
	return true
}
func (w *wlWork) checkWalletLegacy() {
	w.checkWalletEpoch()
	for _, s := range w.sources {
		if !w.tick() {
			return
		}
		s.legacy = len(w.tables["LegacySources"].primary[wlKey{wlWalletLegacyKind(s.kind), s.id}]) > 0
	}
	for i, r := range w.in.LegacySources {
		if !w.tick() {
			return
		}
		ref := wlRef{"LegacySources", i}
		w.use(ref)
		ok := w.legacyTenant(r.TenantID, ref) && w.legacyEpoch(r.EpochID, ref)
		s := w.legacySource(r.Kind, r.SourceID)
		if s == nil || !s.valid {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
			w.taint(r.TenantID)
			if s == nil {
				w.need("business_source:"+r.Kind, r.SourceID)
				w.mark(&w.out.Mismatches.OrphanFacts, ref)
			}
			continue
		}
		amount, aok := w.amount(r.Amount, wlWalletSourceKind(r.Kind) || r.Kind == "consumption_refund", ref)
		if !aok || amount.Cmp(&s.amount) != 0 {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
		}
		if r.TenantID != s.tenant || r.JournalID != s.journalID || r.JournalOperationID != s.journalOperationID {
			ok = false
		}
		roots := r.RequestID == nil && r.ReservationID == nil && r.SettlementID == nil
		switch r.Kind {
		case "manual_credit", "voucher_grant", "sandbox_capture":
		case "reserve":
			roots = wlWalletTextPointer(r.RequestID, s.requestID) && wlWalletTextPointer(r.ReservationID, s.reservationID) && r.SettlementID == nil
		case "finish", "consumption_refund":
			roots = wlWalletTextPointer(r.RequestID, s.requestID) && wlWalletTextPointer(r.ReservationID, s.reservationID) && wlWalletTextPointer(r.SettlementID, s.settlementID)
		default:
			roots = false
		}
		if !roots {
			ok = false
		}
		if !ok {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
			w.taint(r.TenantID)
			w.taint(s.tenant)
		}
	}
	for i, r := range w.in.LegacyReceipts {
		if !w.tick() {
			return
		}
		ref := wlRef{"LegacyReceipts", i}
		w.use(ref)
		w.legacyTenant(r.TenantID, ref)
		w.legacyEpoch(r.EpochID, ref)
		f, _, exists := wlGet[WalletLotsReceiptRow](w, "Receipts", r.ReceiptID)
		s := w.sources[wlKey{r.SourceKind, r.ReceiptID}]
		amount, aok := w.amount(r.Amount, true, ref)
		good := exists && s != nil && s.legacy && s.valid && f.TenantID == r.TenantID && f.SourceKind == r.SourceKind && f.SourceID == r.SourceID && f.JournalID == r.JournalID
		if !aok || s != nil && amount.Cmp(&s.amount) != 0 {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
		}
		if !good {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
			w.taint(r.TenantID)
			if s != nil {
				w.taint(s.tenant)
			}
			if !exists {
				w.need("receipt", r.ReceiptID)
				w.mark(&w.out.Mismatches.OrphanFacts, ref)
			}
		}
		if r.SourceKind == "sandbox_capture" && len(w.by("LegacySandboxReceipts", "ReceiptID", r.ReceiptID)) != 1 {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
			w.need("legacy_sandbox_receipt", r.ReceiptID)
			w.taint(r.TenantID)
		}
	}
	for i, r := range w.in.LegacySandboxReceipts {
		if !w.tick() {
			return
		}
		ref := wlRef{"LegacySandboxReceipts", i}
		w.use(ref)
		w.legacyTenant(r.TenantID, ref)
		w.legacyEpoch(r.EpochID, ref)
		f, _, exists := wlGet[WalletLotsLegacyReceiptRow](w, "LegacyReceipts", r.ReceiptID)
		n, nok := w.amount(r.Amount, true, ref)
		good := exists && f.SourceKind == "sandbox_capture" && f.EpochID == r.EpochID && f.TenantID == r.TenantID && f.SourceID == r.CaptureID && f.JournalID == r.JournalID
		source := w.sources[wlKey{"sandbox_capture", r.ReceiptID}]
		if !nok || source != nil && n.Cmp(&source.amount) != 0 {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
		}
		if !good {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
			w.taint(r.TenantID)
			if !exists {
				w.need("legacy_receipt", r.ReceiptID)
				w.mark(&w.out.Mismatches.OrphanFacts, ref)
			}
		}
	}
	// Validate all old approved members before using their ORIGINAL business
	// amount in the cutover baseline. Membership's amount is only a declaration.
	for i, r := range w.in.LegacyApprovedRefunds {
		if !w.tick() {
			return
		}
		ref := wlRef{"LegacyApprovedRefunds", i}
		w.use(ref)
		w.legacyTenant(r.TenantID, ref)
		w.legacyEpoch(r.EpochID, ref)
		f, _, known := wlGet[WalletLotsRefundRow](w, "Refunds", r.RefundOrderID)
		s := w.legacySource("consumption_refund", r.RefundOrderID)
		n, nok := w.amount(r.Amount, true, ref)
		good := known && s != nil && s.legacy && s.valid && f.State == "approved" && f.TenantID == r.TenantID && f.SettlementID == r.SettlementID && wlWalletTextPointer(f.JournalID, r.JournalID)
		if !nok || s != nil && n.Cmp(&s.amount) != 0 {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
		}
		if len(w.by("LegacySettlements", "SettlementID", r.SettlementID)) != 1 {
			good = false
			w.need("legacy_settlement", r.SettlementID)
		}
		if len(w.by("RefundLots", "RefundOrderID", r.RefundOrderID)) > 0 || len(w.by("Lots", "LegacyRefundOrderID", r.RefundOrderID)) > 0 {
			good = false
		}
		if !good {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
			w.taint(r.TenantID)
			if s != nil {
				w.taint(s.tenant)
			}
			if !known {
				w.need("refund", r.RefundOrderID)
				w.mark(&w.out.Mismatches.OrphanFacts, ref)
			}
		}
	}
	for i, r := range w.in.LegacySettlements {
		if !w.tick() {
			return
		}
		ref := wlRef{"LegacySettlements", i}
		w.use(ref)
		if len(w.tables["LegacySettlements"].primary[wlKey{id: r.SettlementID}]) != 1 {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
			w.taint(r.TenantID)
			continue
		}
		w.legacyTenant(r.TenantID, ref)
		w.legacyEpoch(r.EpochID, ref)
		s := w.sources[wlKey{"finish", r.SettlementID}]
		charge, cok := w.amount(r.ChargedAmount, false, ref)
		baseline, bok := w.amount(r.RefundedAmountAtCutover, false, ref)
		good := s != nil && s.valid && s.legacy && s.tenant == r.TenantID && s.requestID == r.RequestID && s.reservationID == r.ReservationID && s.journalID == r.JournalID
		if !cok || s != nil && charge.Cmp(&s.amount) != 0 {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
		}
		if len(w.tables["LegacySources"].primary[wlKey{"reserve", r.ReservationID}]) != 1 {
			good = false
			w.need("legacy_reserve", r.ReservationID)
		}
		sum := new(big.Int)
		complete := true
		for _, fr := range w.by("LegacyApprovedRefunds", "SettlementID", r.SettlementID) {
			if !w.tick() {
				return
			}
			f, _ := wlAt[WalletLotsLegacyApprovedRefundRow](w, fr)
			actual, ar, exists := wlGet[WalletLotsRefundRow](w, "Refunds", f.RefundOrderID)
			if !exists || actual.State != "approved" || actual.TenantID != r.TenantID || actual.SettlementID != r.SettlementID || len(w.tables["LegacyApprovedRefunds"].primary[wlKey{id: f.RefundOrderID}]) != 1 {
				complete = false
				continue
			}
			n, nok := w.amount(actual.Amount, true, ar)
			if !nok {
				complete = false
				continue
			}
			sum.Add(sum, n)
		}
		if !complete || s == nil || !s.valid {
			good = false
			w.taint(r.TenantID)
		}
		if !bok || complete && baseline.Cmp(sum) != 0 {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
		}
		if s != nil && complete && sum.Cmp(&s.amount) > 0 {
			good = false
		}
		if len(w.by("ReservationLots", "ReservationID", r.ReservationID)) > 0 || len(w.by("SettlementLots", "SettlementID", r.SettlementID)) > 0 {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
		}
		if !good {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
			w.taint(r.TenantID)
			if s != nil {
				w.taint(s.tenant)
			}
		}
		// A valid unique origin is required; cached cutover amounts never seed the
		// expected baseline. Other declaration errors remain visible as mismatches.
		if s != nil && s.valid && complete && sum.Cmp(&s.amount) <= 0 && len(w.tables["LegacySettlements"].primary[wlKey{id: r.SettlementID}]) == 1 {
			plan := wlLegacyTerminalPlan{ref: ref, row: r}
			plan.charged.Set(&s.amount)
			plan.refunded.Set(sum)
			w.legacyTerminal[r.SettlementID] = plan
		}
	}
	// Exact reverse members: no old wallet source can disappear from the
	// corresponding receipt/terminal/refund/held-opening set.
	for _, s := range w.sources {
		if !w.tick() {
			return
		}
		if !s.legacy {
			continue
		}
		switch wlWalletLegacyKind(s.kind) {
		case "manual_credit", "voucher_grant", "sandbox_capture":
			if len(w.by("LegacyReceipts", "ReceiptID", s.id)) != 1 {
				w.mark(&w.out.Mismatches.LegacyBoundaryRows, s.ref)
				w.need("legacy_receipt", s.id)
				w.taint(s.tenant)
			}
		case "finish":
			if len(w.by("LegacySettlements", "SettlementID", s.id)) != 1 {
				w.mark(&w.out.Mismatches.LegacyBoundaryRows, s.ref)
				w.need("legacy_settlement", s.id)
				w.taint(s.tenant)
			}
		case "consumption_refund":
			if len(w.by("LegacyApprovedRefunds", "RefundOrderID", s.id)) != 1 {
				w.mark(&w.out.Mismatches.LegacyBoundaryRows, s.ref)
				w.need("legacy_approved_refund", s.id)
				w.taint(s.tenant)
			}
		case "reserve":
			terminals := w.by("LegacySettlements", "ReservationID", s.id)
			openings := w.by("LegacyOpenings", "ReservationID", s.id)
			if len(terminals) == 1 && len(openings) != 0 || len(terminals) == 0 && len(openings) != 1 || len(terminals) > 1 {
				w.mark(&w.out.Mismatches.LegacyBoundaryRows, s.ref)
				w.taint(s.tenant)
				if len(terminals) == 0 && len(openings) == 0 {
					w.need("legacy_reservation_opening", s.id)
				}
			}
		}
	}
	w.checkWalletOpenings()
}

func (w *wlWork) checkWalletEpoch() {
	if len(w.in.Epochs) != 1 {
		w.mark(&w.out.Mismatches.EpochRows, wlRef{"Epochs", -1})
		if len(w.in.Epochs) == 0 {
			w.need("epoch", "singleton")
		}
		for _, t := range w.in.Tenants {
			if !w.tick() {
				return
			}
			w.taint(t.ID)
		}
		return
	}
	e := w.in.Epochs[0]
	ref := wlRef{"Epochs", 0}
	w.use(ref)
	w.epoch = e
	w.epochKnown = wsUUID(e.ID) && w.time(e.CreatedAt, ref)
	good := e.Singleton && e.SchemaVersion == "1" && e.Actor == "offline-maintenance" && wlWalletDigest(e.BasisDigest) && wlWalletDigest(e.ManifestDigest) && wlWalletDigest(e.DescriptorDigest) && wlWalletDigest(e.RuntimeProofDigest) && w.epochKnown
	for _, s := range []string{e.DatabaseOID, e.SchemaOID} {
		n, ok := w.unsigned(s, true, false, ref)
		if !ok || n > 4294967295 {
			good = false
		}
	}
	if !good {
		w.mark(&w.out.Mismatches.EpochRows, ref)
	}
	if !w.epochKnown {
		for _, t := range w.in.Tenants {
			if !w.tick() {
				return
			}
			w.taint(t.ID)
		}
	}
	if len(w.in.Attestations) != 1 {
		w.mark(&w.out.Mismatches.EpochRows, wlRef{"Attestations", -1})
		if len(w.in.Attestations) == 0 {
			w.need("attestation", e.ID)
		}
		return
	}
	a := w.in.Attestations[0]
	ar := wlRef{"Attestations", 0}
	w.use(ar)
	hash := sha256.Sum256(a.ProofCanonical)
	good = a.Singleton && a.EpochID == e.ID && a.ProofVersion == "2" && a.DatabaseOID == e.DatabaseOID && a.SchemaOID == e.SchemaOID && a.ManifestDigest == e.ManifestDigest && a.DescriptorDigest == e.DescriptorDigest && a.ProofSHA256 == e.RuntimeProofDigest && wlWalletDigest(a.ProofSHA256) && len(a.ProofCanonical) > 0 && len(a.ProofCanonical) <= 262144 && hex.EncodeToString(hash[:]) == a.ProofSHA256 && w.time(a.CreatedAt, ar)
	roles := map[uint64]bool{}
	for _, s := range []string{a.SchemaOwnerOID, a.AppRoleOID, a.ProjectorRoleOID} {
		n, ok := w.unsigned(s, true, false, ar)
		if !ok || n > 4294967295 || roles[n] {
			good = false
		}
		roles[n] = true
	}
	switch a.Mode {
	case "ordinary":
		if a.WorkerRoleOID != nil || a.PredecessorPaymentDigest != nil {
			good = false
		}
		if len(w.in.LegacySandboxReceipts) > 0 {
			good = false
		}
		for _, r := range w.in.Receipts {
			if !w.tick() {
				return
			}
			if r.SourceKind == "sandbox_capture" {
				good = false
			}
		}
	case "sandbox":
		if a.WorkerRoleOID == nil || a.PredecessorPaymentDigest == nil {
			good = false
		} else {
			n, ok := w.unsigned(*a.WorkerRoleOID, true, false, ar)
			if !ok || n > 4294967295 || roles[n] || !wlWalletDigest(*a.PredecessorPaymentDigest) {
				good = false
			}
		}
	default:
		good = false
	}
	if !good {
		w.mark(&w.out.Mismatches.EpochRows, ar)
	}
}

type wlLegacySums struct {
	a, h     big.Int
	complete bool
}

func (w *wlWork) checkWalletOpenings() {
	sums := map[string]*wlLegacySums{}
	for _, t := range w.in.Tenants {
		if !w.tick() {
			return
		}
		if _, ok := sums[t.ID]; !ok {
			sums[t.ID] = &wlLegacySums{complete: true}
		}
	}
	for _, s := range w.sources {
		if !w.tick() {
			return
		}
		if !s.legacy {
			continue
		}
		sum := sums[s.tenant]
		if sum == nil {
			sum = &wlLegacySums{complete: true}
			sums[s.tenant] = sum
		}
		if !s.valid {
			sum.complete = false
			continue
		}
		switch wlWalletLegacyKind(s.kind) {
		case "manual_credit", "voucher_grant", "sandbox_capture", "consumption_refund":
			sum.a.Add(&sum.a, &s.amount)
		case "reserve":
			sum.a.Sub(&sum.a, &s.amount)
			sum.h.Add(&sum.h, &s.amount)
		case "finish":
			sum.a.Add(&sum.a, new(big.Int).Sub(&s.reserved, &s.amount))
			sum.h.Sub(&sum.h, &s.reserved)
		}
	}
	totalA, totalH := new(big.Int), new(big.Int)
	totalsKnown := true
	oldTenants := map[string]bool{}
	availableCounts := map[string]int{}
	for _, opening := range w.in.LegacyOpenings {
		if !w.tick() {
			return
		}
		if opening.Kind == "available" {
			availableCounts[opening.TenantID]++
		}
	}
	for i, t := range w.in.Tenants {
		if !w.tick() {
			return
		}
		ref := wlRef{"Tenants", i}
		if t.CreatedXID != nil {
			if len(w.by("LegacyOpenings", "TenantID", t.ID)) > 0 {
				w.mark(&w.out.Mismatches.GenesisRows, ref)
				w.taint(t.ID)
			}
			continue
		}
		oldTenants[t.ID] = true
		if len(w.tables["Tenants"].primary[wlKey{id: t.ID}]) != 1 {
			w.mark(&w.out.Mismatches.GenesisRows, ref)
			w.taint(t.ID)
			totalsKnown = false
			continue
		}
		sum := sums[t.ID]
		if sum == nil || !sum.complete {
			totalsKnown = false
			w.taint(t.ID)
		} else {
			totalA.Add(totalA, &sum.a)
			totalH.Add(totalH, &sum.h)
			if sum.a.Sign() < 0 || sum.h.Sign() < 0 || !sum.a.IsInt64() {
				w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
				w.taint(t.ID)
			}
		}
		if availableCounts[t.ID] != 1 {
			w.mark(&w.out.Mismatches.GenesisRows, ref)
			w.taint(t.ID)
			if availableCounts[t.ID] == 0 {
				w.need("legacy_available_opening", t.ID)
			}
		}
	}
	openingHeld := map[string]*big.Int{}
	for i, o := range w.in.LegacyOpenings {
		if !w.tick() {
			return
		}
		ref := wlRef{"LegacyOpenings", i}
		w.use(ref)
		old := w.legacyTenant(o.TenantID, ref)
		w.legacyEpoch(o.EpochID, ref)
		good := old && w.epochKnown && wlWalletTimeEqual(o.ObservedAt, w.epoch.CreatedAt)
		declared, dok := w.amount(o.Amount, false, ref)
		if !dok {
			good = false
		}
		var expected *big.Int
		switch o.Kind {
		case "available":
			if o.ReservationID != nil {
				good = false
			}
			sum := sums[o.TenantID]
			if sum != nil && sum.complete && sum.a.Sign() >= 0 {
				expected = new(big.Int).Set(&sum.a)
			}
			if o.AccountID == nil && expected != nil && expected.Sign() > 0 {
				good = false
			}
		case "reservation":
			if o.ReservationID == nil || o.AccountID == nil {
				good = false
			} else {
				source := w.sources[wlKey{"reserve", *o.ReservationID}]
				if source != nil && source.valid && source.legacy && source.tenant == o.TenantID && len(w.by("LegacySettlements", "ReservationID", *o.ReservationID)) == 0 {
					expected = new(big.Int).Set(&source.amount)
				} else {
					good = false
					if source != nil && source.tenant != o.TenantID {
						w.taint(source.tenant)
					}
				}
			}
		default:
			good = false
		}
		if o.AccountID != nil {
			account, _, exists := wlGet[LedgerAccountRow](w, "Accounts", *o.AccountID)
			kind := "AVAILABLE"
			if o.Kind == "reservation" {
				kind = "HELD"
			}
			if !exists || account.TenantID == nil || *account.TenantID != o.TenantID || account.Kind != kind {
				good = false
				if !exists {
					w.need("opening_account", *o.AccountID)
				}
			}
		}
		if expected == nil {
			good = false
			w.taint(o.TenantID)
		} else if dok && declared.Cmp(expected) != 0 {
			good = false
		}
		if !good {
			w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
		}
		unique := len(w.tables["LegacyOpenings"].primary[wlKey{id: o.ID}]) == 1
		if o.Kind == "available" {
			unique = unique && availableCounts[o.TenantID] == 1
		}
		if o.Kind == "reservation" && o.ReservationID != nil {
			unique = unique && len(w.by("LegacyOpenings", "ReservationID", *o.ReservationID)) == 1
		}
		if expected != nil && old && unique && wsUUID(o.ID) && w.epochKnown {
			plan := wlOpeningPlan{ref: ref, row: o}
			plan.amount.Set(expected)
			w.openings[o.TenantID] = append(w.openings[o.TenantID], plan)
			if o.Kind == "reservation" {
				if openingHeld[o.TenantID] == nil {
					openingHeld[o.TenantID] = new(big.Int)
				}
				openingHeld[o.TenantID].Add(openingHeld[o.TenantID], expected)
			}
		} else if !unique {
			w.taint(o.TenantID)
		}
	}
	for tenant, sum := range sums {
		if !w.tick() {
			return
		}
		if !oldTenants[tenant] || !sum.complete {
			continue
		}
		held := openingHeld[tenant]
		if held == nil {
			held = new(big.Int)
		}
		if held.Cmp(&sum.h) != 0 {
			if _, ref, ok := wlGet[WalletSequenceTenantRow](w, "Tenants", tenant); ok {
				w.mark(&w.out.Mismatches.LegacyBoundaryRows, ref)
			}
			w.taint(tenant)
		}
	}
	if !w.epochKnown {
		return
	}
	er := wlRef{"Epochs", 0}
	e := w.epoch
	counts := []struct {
		declared string
		actual   uint64
	}{{e.TenantCount, uint64(len(oldTenants))}, {e.ReceiptCount, uint64(len(w.in.LegacyReceipts))}, {e.SettlementCount, uint64(len(w.in.LegacySettlements))}, {e.ApprovedRefundCount, uint64(len(w.in.LegacyApprovedRefunds))}, {e.OpeningCount, uint64(len(w.in.LegacyOpenings))}, {e.LegacySourceCount, uint64(len(w.in.LegacySources))}}
	for _, c := range counts {
		n, ok := w.unsigned(c.declared, false, true, er)
		if !ok || n != c.actual {
			w.mark(&w.out.Mismatches.EpochRows, er)
		}
	}
	a, aok := w.integer(e.AvailableTotal, er)
	h, hok := w.integer(e.HeldTotal, er)
	if !aok || !hok || aok && a.Sign() < 0 || hok && h.Sign() < 0 || totalsKnown && aok && a.Cmp(totalA) != 0 || totalsKnown && hok && h.Cmp(totalH) != 0 {
		w.mark(&w.out.Mismatches.EpochRows, er)
	}
}
