package fundinglotverify

import (
	"encoding/json"
	"math/big"
)

func procurementEvidenceAmount(a ProcurementAttemptRow, u procurementEvidenceUsage) (*big.Int, bool) {
	if a.CostInputRate == nil || a.CostOutputRate == nil || !procurementAttemptStatus(a.CostBasis, "contract", "invoice") {
		return nil, false
	}
	// Validate the bounded signed-int64 protocol before arbitrary-precision math.
	if len(*a.CostInputRate) > 19 || len(*a.CostOutputRate) > 19 {
		return nil, false
	}
	in, ok := procurementEvidenceInt64(*a.CostInputRate)
	if !ok {
		return nil, false
	}
	out, ok := procurementEvidenceInt64(*a.CostOutputRate)
	if !ok {
		return nil, false
	}
	n := new(big.Int).Mul(big.NewInt(u.InputTokens), in)
	n.Add(n, new(big.Int).Mul(big.NewInt(u.OutputTokens), out))
	n.Add(n, big.NewInt(999999))
	n.Quo(n, big.NewInt(1000000))
	return n, true
}
func procurementEvidenceNormalCost(a ProcurementAttemptRow, u procurementEvidenceUsage, reason string) (procurementEvidenceCost, bool) {
	if procurementEvidenceNoConsumption(reason) {
		return procurementEvidenceCost{"0", "contract"}, true
	}
	if u.Status != "confirmed_final" {
		return procurementEvidenceCost{}, false
	}
	n, ok := procurementEvidenceAmount(a, u)
	if !ok {
		return procurementEvidenceCost{}, false
	}
	return procurementEvidenceCost{n.String(), a.CostBasis}, true
}
func (x *procurementEvidenceIndex) normalEvent(r ProcurementEvidenceBudgetEventRow, predicate func(procurementEvidenceUsage, string) bool) bool {
	m, ok := x.meters[r.EvidenceRef]
	if !ok || !m.valid || m.row.AttemptID == nil || *m.row.AttemptID != r.AttemptID {
		return false
	}
	a, ok := x.attempts[r.AttemptID]
	if !ok || a.TenantID != m.row.TenantID || a.RequestID != m.row.RequestID {
		return false
	}
	for digest, semantic := range x.normalSemantics[procurementEvidencePair{r.AttemptID, r.EvidenceRef}] {
		identity, exists := x.identityRows[procurementEvidencePair{r.AttemptID, digest}]
		if exists && identity.EventID == r.EvidenceRef && predicate(semantic.usage, semantic.reason) {
			return true
		}
	}
	return false
}
func (x *procurementEvidenceIndex) disputedSource(r ProcurementEvidenceBudgetEventRow) (bool, bool) {
	a, ok := x.attempts[r.AttemptID]
	if !ok {
		return false, false
	}
	m, ok := x.meters[r.EvidenceRef]
	if !ok || m.row.TenantID != a.TenantID {
		return false, false
	}
	ids := x.identityByEvent[procurementEvidencePair{r.AttemptID, r.EvidenceRef}]
	normal, conflict := false, false
	for _, id := range ids {
		if x.conflictCandidates[r.EvidenceRef][id.Digest] {
			conflict = true
		}
		if m.valid && m.row.AttemptID != nil && *m.row.AttemptID == r.AttemptID && m.row.RequestID == a.RequestID {
			semantic, ok := x.normalSemantics[procurementEvidencePair{r.AttemptID, r.EvidenceRef}][id.Digest]
			if ok && (procurementEvidenceNoConsumption(semantic.reason) || m.usage.Status == "confirmed_final" && (m.usage.InputTokens > 0 || m.usage.OutputTokens > 0)) {
				normal = true
			}
		}
	}
	return normal || conflict, conflict
}
func procurementEvidenceDesiredHeld(upper, spent *big.Int) *big.Int {
	n := new(big.Int).Sub(upper, spent)
	if n.Sign() < 0 {
		n.SetInt64(0)
	}
	return n
}

func (x *procurementEvidenceIndex) events() error {
	w := x.w
	for _, r := range x.eventRows {
		if err := w.tick(); err != nil {
			return err
		}
		p, exists := x.reservations[r.AttemptID]
		shape := exists && r.AccountID == p.AccountID
		source := true
		h, hOK := procurementBudgetSigned(r.HeldDelta)
		s, sOK := procurementBudgetSigned(r.SpentDelta)
		u, uOK := procurementBudgetUnsigned(p.UpperAmount)
		if !exists || !hOK || !sOK || !uOK {
			if !exists {
				w.out.Mismatches.BudgetEventSourceRows++
			}
			continue
		}
		zero := s.Sign() == 0
		delta := true
		switch r.Type {
		case "reserved":
			shape = shape && r.EventKey == "reserve:"+r.AttemptID && r.EvidenceRef == ""
			delta = h.Cmp(u) == 0 && zero
		case "released_no_consumption":
			shape = shape && r.EventKey == "release:"+r.AttemptID && r.EvidenceRef != ""
			delta = h.Cmp(new(big.Int).Neg(u)) == 0 && zero
			source = x.normalEvent(r, func(_ procurementEvidenceUsage, reason string) bool { return procurementEvidenceNoConsumption(reason) })
		case "consumption_unknown":
			shape = shape && r.EventKey == "unknown:"+r.AttemptID && r.EvidenceRef != ""
			delta = h.Sign() == 0 && zero
			source = x.normalEvent(r, func(usage procurementEvidenceUsage, reason string) bool {
				return usage.Status != "confirmed_final" && !procurementEvidenceNoConsumption(reason)
			})
		case "cost_confirmed":
			shape = shape && r.EventKey == "settle:"+r.AttemptID && r.EvidenceRef != ""
			delta = h.Cmp(new(big.Int).Neg(u)) == 0 && s.Sign() >= 0
			source = x.normalEvent(r, func(usage procurementEvidenceUsage, reason string) bool {
				if usage.Status != "confirmed_final" || procurementEvidenceNoConsumption(reason) {
					return false
				}
				n, ok := procurementEvidenceAmount(x.attempts[r.AttemptID], usage)
				return ok && s.Cmp(n) == 0
			})
			if source {
				m := x.meters[r.EvidenceRef]
				in, inOK := procurementEvidenceInt64(p.InputBound)
				out, outOK := procurementEvidenceInt64(p.OutputBound)
				if s.Cmp(u) > 0 || inOK && m.usage.InputTokens > in.Int64() || outOK && m.usage.OutputTokens > out.Int64() {
					x.anomalyAccounts[p.AccountID] = true
				}
			}
		case "evidence_disputed":
			shape = shape && r.EventKey == "conflict:"+r.AttemptID+":"+r.EvidenceRef && r.EvidenceRef != ""
			delta = h.Sign() >= 0 && h.Cmp(u) <= 0 && zero
			source, _ = x.disputedSource(r)
			if source {
				x.anomalyAccounts[p.AccountID] = true
			}
		case "expired_without_permit":
			shape = shape && r.EventKey == "release:"+r.AttemptID && r.EvidenceRef == ""
			delta = h.Cmp(new(big.Int).Neg(u)) == 0 && zero
		case "recovered_unknown":
			shape = shape && r.EventKey == "unknown:"+r.AttemptID && r.EvidenceRef == ""
			delta = h.Sign() == 0 && zero
		case "cost_corrected":
			source = false
			if proposal, exists := x.proposalEvent[r.EventKey]; exists {
				n, ok := procurementEvidenceInt64(proposal.Amount)
				if ok && proposal.AttemptID == r.AttemptID && proposal.EvidenceRef == r.EvidenceRef {
					source = true
					beforeSpent := new(big.Int).Sub(n, s)
					beforeHeld := new(big.Int).Neg(h)
					delta = beforeSpent.Sign() >= 0 && beforeHeld.Sign() >= 0
					if n.Cmp(beforeSpent) < 0 && proposal.Basis != "invoice" {
						delta = false
					}
					if n.Cmp(u) > 0 {
						x.anomalyAccounts[p.AccountID] = true
					}
				}
			}
			// The lookup key is compared as an entire identity, not parsed by prefix.
			shape = shape && source
		default:
			w.out.Mismatches.InvalidEnumRows++
			shape = false
			source = false
		}
		if !shape {
			w.out.Mismatches.BudgetEventShapeRows++
		}
		if !source {
			w.out.Mismatches.BudgetEventSourceRows++
		}
		if !delta {
			w.out.Mismatches.EventDeltaRows++
		}
	}
	for attempt, p := range x.reservations {
		if err := w.tick(); err != nil {
			return err
		}
		u, uOK := procurementBudgetUnsigned(p.UpperAmount)
		held, hOK := procurementBudgetUnsigned(p.HeldAmount)
		spent, sOK := procurementBudgetUnsigned(p.SpentAmount)
		if !uOK || !hOK || !sOK {
			continue
		}
		if _, ok := procurementEvidencePositiveInt64(p.Revision); !ok {
			w.out.Mismatches.InvalidEnumRows++
		}
		reserved, unknown, terminal, complex, disputes, corrections := 0, 0, 0, 0, 0, 0
		var main, change ProcurementEvidenceBudgetEventRow
		for _, e := range x.eventsByAttempt[attempt] {
			switch e.Type {
			case "reserved":
				reserved++
			case "consumption_unknown", "recovered_unknown":
				unknown++
			case "released_no_consumption", "expired_without_permit", "cost_confirmed":
				terminal++
				main = e
			case "evidence_disputed":
				complex++
				disputes++
				change = e
			case "cost_corrected":
				complex++
				corrections++
				change = e
			}
		}
		if reserved != 1 {
			w.out.Mismatches.ReservedEventRows++
		}
		stateOK := unknown <= 1 && terminal <= 1 && disputes <= corrections+1
		if complex > 1 {
			w.out.Unproven.ProcurementEventOrderUnproven++
		}
		switch p.State {
		case "prepared", "sending":
			stateOK = stateOK && unknown == 0 && terminal == 0 && complex == 0 && x.identityCount[attempt] == 0 && len(x.finalsByAttempt[attempt]) == 0
		case "unknown":
			stateOK = stateOK && unknown == 1 && terminal == 0 && complex == 0 && !p.Anomaly
		case "released":
			stateOK = stateOK && terminal == 1 && main.Type != "cost_confirmed" && complex == 0 && !p.Anomaly
		case "settled":
			stateOK = stateOK && (main.Type == "cost_confirmed" || corrections > 0)
		case "disputed":
			stateOK = stateOK && disputes > 0 && p.Anomaly
		default:
			stateOK = false
		}
		if complex == 0 {
			if p.State == "settled" && main.Type == "cost_confirmed" {
				m := x.meters[main.EvidenceRef]
				expectedAnomaly := spent.Cmp(u) > 0
				if m != nil && m.valid {
					in, okI := procurementEvidenceInt64(p.InputBound)
					out, okO := procurementEvidenceInt64(p.OutputBound)
					expectedAnomaly = expectedAnomaly || okI && m.usage.InputTokens > in.Int64() || okO && m.usage.OutputTokens > out.Int64()
				}
				stateOK = stateOK && p.Anomaly == expectedAnomaly
			}
		} else if complex == 1 {
			beforeHeld := new(big.Int).Set(u)
			beforeSpent := new(big.Int)
			beforeState := "open"
			if unknown == 1 {
				beforeState = "unknown"
			}
			if terminal == 1 {
				beforeHeld.SetInt64(0)
				if main.Type == "cost_confirmed" {
					beforeState = "settled"
					if n, ok := procurementBudgetUnsigned(main.SpentDelta); ok {
						beforeSpent.Set(n)
					}
				} else {
					beforeState = "released"
				}
			}
			dh, okH := procurementBudgetSigned(change.HeldDelta)
			ds, okS := procurementBudgetSigned(change.SpentDelta)
			if okH && okS {
				targetHeld := new(big.Int)
				targetSpent := new(big.Int).Set(beforeSpent)
				targetState := "settled"
				anomaly := false
				legal := true
				if change.Type == "evidence_disputed" {
					targetHeld = procurementEvidenceDesiredHeld(u, beforeSpent)
					targetState = "disputed"
					anomaly = true
					_, hasConflict := x.disputedSource(change)
					if !hasConflict {
						legal = x.normalEvent(change, func(usage procurementEvidenceUsage, reason string) bool {
							return beforeState == "released" && usage.Status == "confirmed_final" && (usage.InputTokens > 0 || usage.OutputTokens > 0) && !procurementEvidenceNoConsumption(reason) || beforeState == "settled" && beforeSpent.Sign() > 0 && procurementEvidenceNoConsumption(reason)
						})
					}
				} else {
					legal = beforeState != "open"
					if proposal, exists := x.proposalEvent[change.EventKey]; exists {
						if n, ok := procurementEvidenceInt64(proposal.Amount); ok {
							targetSpent.Set(n)
						}
					}
					anomaly = targetSpent.Cmp(u) > 0
				}
				expectedH := new(big.Int).Sub(targetHeld, beforeHeld)
				expectedS := new(big.Int).Sub(targetSpent, beforeSpent)
				if dh.Cmp(expectedH) != 0 || ds.Cmp(expectedS) != 0 {
					w.out.Mismatches.EventDeltaRows++
				}
				stateOK = stateOK && legal && p.State == targetState && held.Cmp(targetHeld) == 0 && spent.Cmp(targetSpent) == 0 && p.Anomaly == anomaly
			}
		} else {
			stateOK = stateOK && (p.State == "settled" || p.State == "disputed")
		}
		if !stateOK {
			w.out.Mismatches.StateEventRows++
		}
	}
	return nil
}

func procurementEvidenceRecorded(raw string) (string, procurementEvidenceCost, bool) {
	obj, ok := procurementEvidenceObject(raw, []string{"attempt_id", "amount", "basis"})
	if !ok {
		return "", procurementEvidenceCost{}, false
	}
	var attempt, amount, basis string
	if json.Unmarshal(obj["attempt_id"], &attempt) != nil || json.Unmarshal(obj["amount"], &amount) != nil || json.Unmarshal(obj["basis"], &basis) != nil || !procurementAttemptValidText(attempt) || !procurementAttemptStatus(basis, "contract", "invoice") {
		return "", procurementEvidenceCost{}, false
	}
	if _, ok := procurementBudgetUnsigned(amount); !ok {
		return "", procurementEvidenceCost{}, false
	}
	return attempt, procurementEvidenceCost{amount, basis}, true
}
func (x *procurementEvidenceIndex) costs() error {
	w := x.w
	for attempt, fs := range x.finalsByAttempt {
		if err := w.tick(); err != nil {
			return err
		}
		a, exists := x.attempts[attempt]
		if !exists {
			continue
		}
		candidates := make(map[procurementEvidenceCost]bool)
		for _, f := range fs {
			m := x.meters[f.row.EventID]
			if !f.valid || m == nil || !m.valid || m.row.TenantID != a.TenantID || m.row.RequestID != a.RequestID {
				continue
			}
			if c, ok := procurementEvidenceNormalCost(a, m.usage, f.row.TransportEndReason); ok {
				candidates[c] = true
			}
		}
		x.costsByAttempt[attempt] = candidates
	}
	for _, r := range x.outbox {
		if err := w.tick(); err != nil {
			return err
		}
		if r.Kind != "cost_recorded" {
			continue
		}
		attempt, c, valid := procurementEvidenceRecorded(r.Payload)
		if !valid {
			w.out.Mismatches.CostRecordedJSONRows++
			continue
		}
		a, ok := x.attempts[attempt]
		if !ok || r.OperationID != attempt || r.TenantID != a.TenantID || r.SchemaVersion != 1 || !x.costsByAttempt[attempt][c] {
			w.out.Mismatches.CostRecordedSourceRows++
		}
		if _, exists := x.recordedByAttempt[attempt]; !exists {
			x.recordedByAttempt[attempt] = c
		}
	}
	for attempt, a := range x.attempts {
		if err := w.tick(); err != nil {
			return err
		}
		p, managed := x.reservations[attempt]
		record, hasRecord := x.recordedByAttempt[attempt]
		corrections := x.correctionsByAttempt[attempt]
		eligibleGap := x.conflictTenant[a.TenantID] || !managed && len(corrections) > 0
		expected := make(map[procurementEvidenceCost]bool)
		knownCorrections := make(map[procurementEvidenceCost]bool)
		for _, c := range corrections {
			if c.TenantID == a.TenantID {
				key := procurementEvidenceCost{c.Amount, c.Basis}
				knownCorrections[key] = true
				expected[key] = true
			}
		}
		if hasRecord {
			expected[record] = true
		}
		requiredRecord := false
		requiredMatches := true
		for _, e := range x.eventsByAttempt[attempt] {
			switch e.Type {
			case "cost_corrected", "evidence_disputed":
				eligibleGap = true
			case "cost_confirmed":
				requiredRecord = true
				n, ok := procurementBudgetUnsigned(e.SpentDelta)
				requiredMatches = requiredMatches && ok && hasRecord && record == (procurementEvidenceCost{n.String(), a.CostBasis})
			case "released_no_consumption":
				requiredRecord = true
				requiredMatches = requiredMatches && hasRecord && record == (procurementEvidenceCost{"0", "contract"})
			}
		}
		if requiredRecord && (!hasRecord || !requiredMatches) {
			w.out.Mismatches.CostRecordedMissingRows++
		}
		if len(x.costsByAttempt[attempt]) > 0 && eligibleGap {
			w.out.Unproven.CostRecordedEligibilityUnproven++
		}
		if len(x.costsByAttempt[attempt]) > 0 && !hasRecord && !eligibleGap && !requiredRecord {
			w.out.Mismatches.CostRecordedMissingRows++
		}
		known := procurementAttemptStatus(a.CostStatus, "contract", "invoice")
		valid := true
		orderGap := false
		if known {
			if a.CostAmount == nil {
				valid = false
			} else {
				c := procurementEvidenceCost{*a.CostAmount, a.CostStatus}
				valid = expected[c]
				if managed {
					switch p.State {
					case "settled":
						valid = valid && *a.CostAmount == p.SpentAmount
						if len(knownCorrections) > 0 {
							valid = valid && knownCorrections[c]
							orderGap = len(knownCorrections) > 1
						}
					case "released":
						valid = valid && *a.CostAmount == "0"
					default:
						valid = false
					}
				} else {
					// Without a real conflict there is no writer that resets a legacy
					// correction's known cost; the one cost_recorded must precede it.
					if len(knownCorrections) > 0 && !x.conflictTenant[a.TenantID] {
						valid = valid && knownCorrections[c]
					}
					orderGap = len(knownCorrections) > 1 || len(expected) > 1 && x.conflictTenant[a.TenantID]
				}
			}
		} else if a.CostStatus == "unknown" {
			valid = a.CostAmount == nil
			if managed {
				valid = valid && p.State != "settled"
				if p.State == "released" && hasRecord {
					valid = false
				}
			} else if len(expected) > 0 {
				if x.conflictTenant[a.TenantID] {
					orderGap = true
				} else {
					valid = false
				}
			}
		} else {
			valid = false
		}
		if !valid {
			w.out.Mismatches.CurrentCostRows++
		}
		if orderGap {
			w.out.Unproven.CurrentCostOrderUnproven++
		}
	}
	return nil
}
