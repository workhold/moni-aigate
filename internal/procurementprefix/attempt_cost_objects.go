package procurementprefix

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

func (c *costHistoryReplay) approval(s *costHistoryPosition, n AttemptNodeRow) {
	a, found := c.approvals[tupleKey(n.SourceID)]
	if !found {
		c.mark(&c.result.Stats.CorrectionRows, "approval", n.SourceID, s.origin.AttemptID)
		return
	}
	p, present := c.proposals[tupleKey(a.ProposalID)]
	good := found && present && s.origin.ManagementMode == "managed" && a.BeforeCostStatus == s.status && costHistorySame(a.BeforeCostAmount, s.amount) && p.TenantID == s.origin.TenantID && p.AttemptID == s.origin.AttemptID && costHistoryPMatches(a.ConsumedRevision, a.ConsumedState, a.ConsumedHeld, a.ConsumedSpent, a.ConsumedAnomaly, s.p) && a.ConsumedMemberCount == strconv.FormatInt(s.memberCount, 10) && costHistorySame(a.ConsumedMemberTail, s.memberTail) && p.State == "approved" && (p.Basis == "contract" || p.Basis == "invoice") && costHistoryMoney(p.Amount, 19)
	good = good && c.observe(s, n, a.AccountID, a.AccountOrdinalCut, a.AccountTailCut)
	if value, ok := costHistoryInt(p.Amount); !ok || value < 0 {
		good = false
	}
	if !c.checkCorrection(a.ProviderCorrectionID, s, a.RootID, a.StepNo, a.CreatedXID, "approval_consumed", a.ID, "procurement:"+a.ApprovalOperationID, p.Amount, p.Basis, p.EvidenceRef) {
		good = false
	}
	audit, audited := c.audits[tupleKey(a.AuditID)]
	good = good && audited && audit.Actor == a.Approver && audit.Action == "procurement.cost_approved" && audit.Target == p.ID && audit.RequestID == s.origin.RequestID && costHistorySame(audit.TenantScope, costHistoryPtr(s.origin.TenantID)) && c.flatMatches(audit.SanitizedDiff, map[string]string{"attempt_id": s.origin.AttemptID, "amount": p.Amount, "evidence_ref": p.EvidenceRef})
	if !good {
		c.mark(&c.result.Stats.CorrectionRows, "approval", a.ID, s.origin.AttemptID)
	}
	meta := c.meta[tupleKey("approval_consumed", a.ID)]
	c.expectMutation(meta, a.CostMutationID, p.Basis, costHistoryPtr(p.Amount))
	c.expectOutbox(meta, a.OutboxID, "procurement:"+a.ApprovalOperationID, "cost_corrected", p.Amount, p.Basis)
}
func costHistoryReference(s string) bool {
	if len(s) == 0 || len(s) > 256 || strings.Contains(s, "://") {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "mg_") || strings.HasPrefix(lower, "bearer") {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(":_./-", r) {
			return false
		}
	}
	return true
}
func (c *costHistoryReplay) correction(s *costHistoryPosition, n AttemptNodeRow) {
	u, found := c.corrections[tupleKey(n.SourceID)]
	if !found {
		c.mark(&c.result.Stats.CorrectionRows, "unmanaged", n.SourceID, s.origin.AttemptID)
		return
	}
	amount, valid := costHistoryInt(u.Amount)
	actor := costHistoryText(u.InputActorID)
	if actor == "" {
		actor = "system"
	}
	good := found && s.origin.ManagementMode == "unmanaged" && !s.p.present && valid && amount >= 0 && (u.Basis == "contract" || u.Basis == "invoice") && len(u.OperationID) > 0 && len(u.OperationID) <= 200 && costHistoryReference(u.EvidenceRef) && u.BeforeCostStatus == s.status && costHistorySame(u.BeforeCostAmount, s.amount) && u.AfterCostStatus == u.Basis && u.AfterCostAmount == u.Amount && u.EffectiveAuditActor == actor
	good = good && c.checkCorrection(u.ProviderCorrectionID, s, u.RootID, u.StepNo, u.CreatedXID, "unmanaged_correction", u.ID, u.OperationID, u.Amount, u.Basis, u.EvidenceRef)
	audit, audited := c.audits[tupleKey(u.AuditID)]
	good = good && audited && audit.Actor == actor && audit.Action == "cost.corrected" && audit.Target == u.ProviderCorrectionID && audit.RequestID == s.origin.RequestID && costHistorySame(audit.TenantScope, costHistoryPtr(s.origin.TenantID)) && c.flatMatches(audit.SanitizedDiff, map[string]string{"amount": u.Amount, "basis": u.Basis})
	if !good {
		c.mark(&c.result.Stats.CorrectionRows, "unmanaged", u.ID, s.origin.AttemptID)
	}
	meta := c.meta[tupleKey("unmanaged_correction", u.ID)]
	c.expectMutation(meta, u.CostMutationID, u.Basis, costHistoryPtr(u.Amount))
	c.expectOutbox(meta, u.OutboxID, u.OperationID, "cost_corrected", u.Amount, u.Basis)
}
func (c *costHistoryReplay) checkCorrection(id string, s *costHistoryPosition, root, step, xid, kind, source, operation, amount, basis, evidence string) bool {
	r, ok := c.originalCorrections[tupleKey(id)]
	c.seenCorrections.add(id)
	return ok && r.TenantID == s.origin.TenantID && r.AttemptID == s.origin.AttemptID && r.OperationID == operation && r.Amount == amount && r.Basis == basis && r.EvidenceRef == evidence && costHistoryFinite(r.CreatedAt) && costHistorySame(r.PPRootID, costHistoryPtr(root)) && costHistorySame(r.PPRootStep, costHistoryPtr(step)) && costHistorySame(r.PPSourceKind, costHistoryPtr(kind)) && costHistorySame(r.PPSourceID, costHistoryPtr(source)) && costHistorySame(r.PPSourceRole, costHistoryPtr("correction")) && costHistorySame(r.PPCreatedXID, costHistoryPtr(xid))
}
func (c *costHistoryReplay) outbox(s *costHistoryPosition, n AttemptNodeRow) {
	link, found := c.links[tupleKey(n.SourceID)]
	if !found {
		c.mark(&c.result.Stats.OutboxRows, "link", n.SourceID, s.origin.AttemptID)
		return
	}
	expected, planned := c.expectedOutboxes[tupleKey(link.OutboxID)]
	out, original := c.outboxes[tupleKey(link.OutboxID)]
	good := found && planned && original && link.CauseKind == expected.meta.kind && link.CauseSourceID == expected.meta.id && link.RootID == expected.meta.root && c.metaBefore(n, link.CauseKind, link.CauseSourceID) && out.TenantID == s.origin.TenantID && out.OperationID == expected.operation && out.Kind == expected.kind && out.SchemaVersion == "1" && costHistoryFinite(out.CreatedAt) && c.flatMatches(out.SanitizedPayload, map[string]string{"attempt_id": s.origin.AttemptID, "amount": expected.amount, "basis": expected.basis}) && costHistorySame(out.PPRootID, costHistoryPtr(link.RootID)) && costHistorySame(out.PPRootStep, costHistoryPtr(link.StepNo)) && costHistorySame(out.PPSourceKind, costHistoryPtr("cost_outbox")) && costHistorySame(out.PPSourceID, costHistoryPtr(link.ID)) && costHistorySame(out.PPSourceRole, costHistoryPtr("cost")) && costHistorySame(out.PPCreatedXID, costHistoryPtr(link.CreatedXID))
	mutation := c.mutationForCause[tupleKey(link.CauseKind, link.CauseSourceID)]
	_, mutated := c.seenMutations[tupleKey(mutation)]
	good = good && mutated && c.metaBefore(n, "cost_mutation", mutation)
	if expected.kind == "cost_recorded" {
		good = good && !s.recorded
		s.recorded = true
	}
	if !good {
		c.mark(&c.result.Stats.OutboxRows, "outbox", link.OutboxID, s.origin.AttemptID)
	}
	c.seenOutboxes.add(link.OutboxID)
}
func (c *costHistoryReplay) reverse() {
	for _, m := range c.meta {
		if !c.tick() {
			return
		}
		if _, seen := c.consumed[tupleKey(m.kind, m.id)]; seen {
			continue
		}
		counter := &c.result.Stats.CurrentCostRows
		kind, id := m.kind, m.id
		switch m.kind {
		case "normal_cost_decision":
			counter, kind = &c.result.Stats.NormalEligibilityRows, "decision"
		case "cost_mutation":
			counter, kind = &c.result.Stats.MutationRows, "mutation"
		case "cost_reset_decision":
			counter, kind = &c.result.Stats.ResetRows, "decision"
		case "unmanaged_correction":
			counter, kind = &c.result.Stats.CorrectionRows, "unmanaged"
		case "approval_consumed":
			counter, kind = &c.result.Stats.CorrectionRows, "approval"
		case "attempt_delivery":
			counter, kind = &c.result.Stats.DeliveryRows, "source"
		case "recovery":
			counter, kind = &c.result.Stats.DeliveryRows, "recovery"
		case "cost_outbox":
			counter, kind = &c.result.Stats.OutboxRows, "outbox"
			id = c.links[tupleKey(m.id)].OutboxID
		}
		c.mark(counter, kind, id, m.attempt)
	}
	for _, expected := range c.expectedMutations {
		if !c.tick() {
			return
		}
		if _, seen := c.seenMutations[tupleKey(expected.id)]; !seen {
			c.mark(&c.result.Stats.MutationRows, "mutation", expected.id, expected.meta.attempt)
		}
	}
	for _, expected := range c.expectedOutboxes {
		if !c.tick() {
			return
		}
		if _, seen := c.seenOutboxes[tupleKey(expected.id)]; !seen {
			c.mark(&c.result.Stats.OutboxRows, "outbox", expected.id, expected.meta.attempt)
		}
	}
	for _, a := range c.rows.OriginalRows.UpstreamAttempts {
		if !c.tick() {
			return
		}
		if a.PPOriginRootID != nil {
			if _, found := c.origins[tupleKey(a.ID)]; !found {
				c.mark(&c.result.Stats.AttemptBirthRows, "attempt", a.ID, a.ID)
			}
		}
	}
	for _, r := range c.rows.OriginalRows.ProviderCostCorrections {
		if !c.tick() {
			return
		}
		if r.PPSourceID != nil {
			if _, seen := c.seenCorrections[tupleKey(r.ID)]; !seen {
				c.mark(&c.result.Stats.CorrectionRows, "orphan_original", r.ID, r.AttemptID)
			}
		}
	}
	operationKeys := map[string]string{}
	for _, out := range c.rows.OriginalRows.ProjectionOutbox {
		if !c.tick() {
			return
		}
		if out.Kind != "cost_recorded" && out.Kind != "cost_corrected" {
			continue
		}
		key := tupleKey(out.OperationID, out.Kind)
		if _, dup := operationKeys[key]; dup {
			c.mark(&c.result.Stats.OutboxRows, "outbox", out.EventID, "")
			c.duplicate = true
		}
		operationKeys[key] = out.EventID
		if out.PPSourceKind != nil && *out.PPSourceKind == "cost_outbox" {
			if _, seen := c.seenOutboxes[tupleKey(out.EventID)]; !seen {
				attempt := ""
				if payload, ok := c.flat(out.SanitizedPayload); ok {
					attempt, _ = payload["attempt_id"].(string)
				}
				c.mark(&c.result.Stats.OutboxRows, "outbox", out.EventID, attempt)
			}
		}
	}
	normalNodesByRoot, deliveryNodesByRoot := map[string][]string{}, map[string][]string{}
	for _, node := range c.rows.PrefixRows.AttemptNodes {
		if !c.tick() {
			return
		}
		if node.StepKind == "normal_cost_decision" {
			normalNodesByRoot[tupleKey(node.RootID)] = append(normalNodesByRoot[tupleKey(node.RootID)], node.SourceID)
		}
		if node.StepKind == "attempt_delivery" {
			deliveryNodesByRoot[tupleKey(node.RootID)] = append(deliveryNodesByRoot[tupleKey(node.RootID)], node.SourceID)
		}
	}
	normalsByIncoming := map[string][]NormalCostDecisionRow{}
	for _, normal := range c.rows.PrefixRows.NormalCostDecisions {
		if !c.tick() {
			return
		}
		key := tupleKey(normal.IncomingSourceID)
		normalsByIncoming[key] = append(normalsByIncoming[key], normal)
	}
	for _, d := range c.rows.PrefixRows.RequestDecisions {
		if !c.tick() {
			return
		}
		in, found := c.incoming[tupleKey(d.IncomingSourceID)]
		if !found || in.AttemptID == nil {
			continue
		}
		attempt := *in.AttemptID
		normals, deliveries := normalsByIncoming[tupleKey(in.ID)], c.finalDeliveries[tupleKey(d.ID)]
		if d.Branch == "normal" {
			if len(normals) == 0 {
				if nodes := normalNodesByRoot[tupleKey(d.RootID)]; len(nodes) == 1 {
					c.mark(&c.result.Stats.NormalEligibilityRows, "decision", nodes[0], attempt)
				} else {
					c.mark(&c.result.Stats.NormalEligibilityRows, "request_obligation", d.ID, attempt)
				}
			} else if len(normals) != 1 || !costHistorySame(d.FinalizationID, costHistoryPtr(normals[0].FinalizationID)) {
				for _, normal := range normals {
					if !c.tick() {
						return
					}
					c.mark(&c.result.Stats.NormalEligibilityRows, "decision", normal.ID, attempt)
				}
			}
			if len(deliveries) == 0 {
				if nodes := deliveryNodesByRoot[tupleKey(d.RootID)]; len(nodes) == 1 {
					c.mark(&c.result.Stats.DeliveryRows, "source", nodes[0], attempt)
				} else {
					c.mark(&c.result.Stats.DeliveryRows, "request_obligation", d.ID, attempt)
				}
			} else if len(deliveries) != 1 {
				for _, delivery := range deliveries {
					if !c.tick() {
						return
					}
					c.mark(&c.result.Stats.DeliveryRows, "source", delivery.ID, attempt)
				}
			}
		} else if d.Branch == "conflict" {
			for _, normal := range normals {
				if !c.tick() {
					return
				}
				c.mark(&c.result.Stats.NormalEligibilityRows, "decision", normal.ID, attempt)
			}
			for _, delivery := range deliveries {
				if !c.tick() {
					return
				}
				c.mark(&c.result.Stats.DeliveryRows, "source", delivery.ID, attempt)
			}
		}
	}
	resets := map[string]int{}
	for _, r := range c.rows.PrefixRows.CostResetDecisions {
		if !c.tick() {
			return
		}
		resets[tupleKey(r.CauseKind, r.CauseSourceID)]++
	}
	for _, t := range c.rows.PrefixRows.ProcurementTransitions {
		if !c.tick() {
			return
		}
		if t.EventType == "evidence_disputed" && resets[tupleKey("procurement_dispute", t.ID)] != 1 {
			c.mark(&c.result.Stats.ResetRows, "expected_procurement_dispute", t.ID, t.AttemptID)
		}
	}
	for _, source := range c.rows.PrefixRows.ConflictSources {
		if !c.tick() {
			return
		}
		if source.AttemptID != nil && resets[tupleKey("incoming_conflict", source.ConflictID)] != 1 {
			c.mark(&c.result.Stats.ResetRows, "expected_incoming_conflict", source.ConflictID, *source.AttemptID)
		}
	}
}

// JSON decoding rejects decoded duplicate keys and UTF16 substitution before a
// map is built. Every byte scan shares the same cancellation/deadline budget.
func (c *costHistoryReplay) unicodeJSON(raw string) bool {
	if !utf8.ValidString(raw) {
		return false
	}
	quoted := false
	for i := 0; i < len(raw); i++ {
		if !c.tick() {
			return false
		}
		b := raw[i]
		if !quoted {
			if b == '"' {
				quoted = true
			}
			continue
		}
		if b == '"' {
			quoted = false
			continue
		}
		if b != '\\' {
			if b < 32 {
				return false
			}
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(raw[i+1:i+5], 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xD800 && n <= 0xDBFF {
			if i+6 >= len(raw) || raw[i+1:i+3] != "\\u" {
				return false
			}
			low, err := strconv.ParseUint(raw[i+3:i+7], 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		} else if n >= 0xDC00 && n <= 0xDFFF {
			return false
		}
	}
	return !quoted
}
func (c *costHistoryReplay) flat(raw string) (map[string]any, bool) {
	if !c.unicodeJSON(raw) {
		return nil, false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, false
	}
	out := map[string]any{}
	for d.More() {
		if !c.tick() || len(out) >= 9 {
			return nil, false
		}
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok {
			return nil, false
		}
		if _, dup := out[name]; dup {
			return nil, false
		}
		value, err := d.Token()
		if err != nil {
			return nil, false
		}
		switch value.(type) {
		case string, json.Number, bool, nil:
		default:
			return nil, false
		}
		out[name] = value
	}
	if last, err := d.Token(); err != nil || last != json.Delim('}') {
		return nil, false
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, false
	}
	return out, true
}
func (c *costHistoryReplay) flatMatches(raw string, want map[string]string) bool {
	got, ok := c.flat(raw)
	if !ok || len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if !c.tick() {
			return false
		}
		if got[k] != v {
			return false
		}
	}
	return true
}

type costHistoryRoute struct {
	ChannelID string `json:"channel_id"`
	VersionID string `json:"version_id"`
	Priority  int64  `json:"priority"`
	Weight    uint64 `json:"weight"`
	Reason    string `json:"reason"`
}

func (c *costHistoryReplay) routeValues(raw string) ([]costHistoryRoute, bool) {
	if !c.unicodeJSON(raw) {
		return nil, false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('[') {
		return nil, false
	}
	out := []costHistoryRoute{}
	for d.More() {
		if !c.tick() || len(out) >= 1000 {
			return nil, false
		}
		var encoded json.RawMessage
		if err := d.Decode(&encoded); err != nil {
			return nil, false
		}
		m, ok := c.flat(string(encoded))
		if !ok || len(m) != 5 {
			return nil, false
		}
		channel, a := m["channel_id"].(string)
		version, b := m["version_id"].(string)
		priority, p := m["priority"].(json.Number)
		weight, w := m["weight"].(json.Number)
		reason, r := m["reason"].(string)
		if !a || !b || !p || !w || !r || !signedInteger(string(priority), 64) || !unsignedInteger(string(weight), 64, true) {
			return nil, false
		}
		pri, _ := strconv.ParseInt(string(priority), 10, 64)
		wei, _ := strconv.ParseUint(string(weight), 10, 64)
		out = append(out, costHistoryRoute{channel, version, pri, wei, reason})
	}
	if last, err := d.Token(); err != nil || last != json.Delim(']') {
		return nil, false
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, false
	}
	canonical, err := json.Marshal(out)
	return out, err == nil && len(canonical) <= 262144
}
func (c *costHistoryReplay) routes(raw, channel, version string) bool {
	list, ok := c.routeValues(raw)
	if !ok {
		return false
	}
	if len(list) == 0 {
		return true
	}
	seen := make(keySet)
	selected := 0
	for _, r := range list {
		if !c.tick() {
			return false
		}
		if _, err := uuid.Parse(r.ChannelID); err != nil {
			return false
		}
		if _, err := uuid.Parse(r.VersionID); err != nil {
			return false
		}
		if _, dup := seen[tupleKey(r.ChannelID)]; dup {
			return false
		}
		seen.add(r.ChannelID)
		switch r.Reason {
		case "selected", "weighted_not_selected", "lower_priority", "disabled", "already_tried", "capacity_full", "quarantined", "circuit_open", "half_open_busy", "channel_quarantined", "credential_revoked", "binding_isolated", "region_forbidden", "cost_unknown", "cost_limit_exceeded", "higher_cost", "procurement_paused", "procurement_budget_exceeded", "procurement_bound_invalid", "procurement_budget_unavailable":
		default:
			return false
		}
		if r.Reason == "selected" {
			selected++
			if r.ChannelID != channel || r.VersionID != version {
				return false
			}
		}
	}
	return selected == 1
}
func (c *costHistoryReplay) equalRoutes(a, b string) bool {
	x, ok := c.routeValues(a)
	y, yes := c.routeValues(b)
	if !ok || !yes {
		return false
	}
	left, _ := json.Marshal(x)
	right, _ := json.Marshal(y)
	return bytes.Equal(left, right)
}
