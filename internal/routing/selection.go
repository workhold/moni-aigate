package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

type selectionGroup struct {
	candidate   Candidate
	members     []int
	weight      uint64
	explanation CandidateDecision
}

func healthBreak(reason string) bool {
	return reason == "channel_quarantined" || reason == "quarantined" || reason == "circuit_open" || reason == "half_open_busy" || reason == "credential_cooldown"
}

func costBreak(reason string) bool {
	return reason == "cost_unknown" || reason == "cost_limit_exceeded" || reason == "margin_below_floor" || reason == "margin_basis_unavailable" || reason == "cumulative_cost_unavailable" || reason == "procurement_paused" || reason == "procurement_budget_exceeded" || reason == "procurement_bound_invalid" || reason == "procurement_budget_unavailable"
}

func stickyBreak(policy DynamicPolicy, kind string) bool {
	if policy.Stickiness == nil || !policy.Stickiness.Enabled {
		return false
	}
	for _, value := range policy.Stickiness.BreakOn {
		if value == kind {
			return true
		}
	}
	return false
}

func (e *Engine) acquirePlan(p *Plan, sequence int) (Candidate, uint64, *RouteDecision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.config.Now()
	record := &RouteDecision{Sequence: sequence, Draw: "0", Degradation: "none", AlgorithmVersion: p.algorithm, Policy: p.policy, Request: p.request, Candidates: []CandidateDecision{}}
	if len(p.candidates) > 0 {
		record.PolicyVersionID = p.candidates[0].PolicyVersionID
	}
	groups := []selectionGroup{}
	indexes := map[string]int{}
	for i, candidate := range p.candidates {
		key := candidateKey(candidate)
		index, exists := indexes[key]
		if !exists {
			index = len(groups)
			indexes[key] = index
			explanation, err := staticCandidateDecision(candidate, p.policy, p.request)
			if err != nil {
				return Candidate{}, 0, nil, err
			}
			groups = append(groups, selectionGroup{candidate: candidate, explanation: explanation})
		}
		groups[index].members = append(groups[index].members, i)
	}
	pool := []int{}
	eligibleAll := []int{}
	highest := math.MinInt
	var weight uint64
	stickyState := ""
	for i := range groups {
		group := &groups[i]
		eligible := []int{}
		for _, member := range group.members {
			candidate := p.candidates[member]
			explanation, err := staticCandidateDecision(candidate, p.policy, p.request)
			if err != nil {
				return Candidate{}, 0, nil, err
			}
			reason := explanation.FilterReason
			if reason == "" && p.tried[candidateKey(candidate)] {
				reason = "already_tried"
			}
			if reason == "" && (e.inflight[candidate.ChannelID] >= candidate.MaxInflight || e.credentialInflight[credentialKey(candidate)] >= credentialCapacity(candidate)) {
				reason = "capacity_full"
			}
			if reason == "" {
				h := e.get(key(candidate))
				switch {
				case h.state == Quarantined:
					reason = "quarantined"
				case h.state == Open && now.Before(h.until):
					reason = "circuit_open"
				default:
					if h.state == Open {
						h.state = HalfOpen
						h.probes = 0
						h.generation++
					}
					if h.state == HalfOpen && h.probes >= e.breaker(candidate).HalfOpenTokens {
						reason = "half_open_busy"
					}
				}
			}
			// 043: a rate-limited credential cools down alone; its pool
			// siblings stay eligible for the same channel+model.
			if reason == "" && e.credentialCooling(candidate.ChannelID, candidate.Model, candidate.CredentialID, now) {
				reason = "credential_cooldown"
			}
			procurementReason := ProcurementExclusion(candidate)
			if reason == "" && procurementReason != "" {
				reason = procurementReason
			}
			if reason == "" {
				costReason, err := CostExclusion(candidate.CostPolicy, candidate.CostQuote, candidate.CostLimits)
				if err != nil {
					return Candidate{}, 0, nil, err
				}
				reason = costReason
			}
			if reason == "" {
				if p.request.Preference.MaxPrice != nil && explanation.CostSnapshot.Basis == "unknown" {
					reason = "cost_unknown"
				} else {
					exceeded, err := maxPriceExceeded(explanation.CostSnapshot, p.request.Preference.MaxPrice)
					if err != nil {
						return Candidate{}, 0, nil, err
					}
					if exceeded {
						reason = "cost_limit_exceeded"
					}
				}
			}
			if reason == "" {
				reason = marginExclusionForRequest(explanation.CostSnapshot, candidate.RetailAmount, p.policy.MinMarginRate, p.request, p.algorithm)
			}
			if reason != "" {
				if group.explanation.FilterReason == "" {
					group.explanation.FilterReason = reason
				}
				continue
			}
			memberWeight := credentialWeight(candidate)
			if math.MaxUint64-group.weight < memberWeight {
				return Candidate{}, 0, nil, ErrInvalidPolicy
			}
			group.weight += memberWeight
			eligible = append(eligible, member)
		}
		group.members = eligible
		record.Candidates = append(record.Candidates, group.explanation)
		if p.request.Sticky != nil && group.candidate.ChannelID == p.request.Sticky.ChannelID && group.candidate.PhysicalModelID == p.request.Sticky.PhysicalModelID && len(eligible) == 0 {
			reason := group.explanation.FilterReason
			switch {
			case stickyBreak(p.policy, "quality") && (reason == "quality_below_floor" || reason == "capability_missing"):
				stickyState = "break_quality"
			case stickyBreak(p.policy, "cost") && costBreak(reason):
				stickyState = "break_cost"
			case stickyBreak(p.policy, "health") && healthBreak(reason):
				stickyState = "break_health"
			}
		}
		if len(eligible) == 0 {
			continue
		}
		// Credential-specific exclusions must not contaminate an eligible binding.
		group.explanation.FilterReason = ""
		eligibleAll = append(eligibleAll, i)
		group.explanation.Reason = "lower_priority"
		if group.candidate.Priority > highest {
			highest = group.candidate.Priority
			pool = pool[:0]
			weight = 0
		}
		if group.candidate.Priority == highest {
			if math.MaxUint64-weight < group.candidate.Weight {
				return Candidate{}, 0, nil, ErrInvalidPolicy
			}
			pool = append(pool, i)
			weight += group.candidate.Weight
		}
	}
	if len(pool) == 0 {
		record.Sticky = stickyState
		for i := range record.Candidates {
			record.Candidates[i].Reason = record.Candidates[i].FilterReason
		}
		finalizeRouteDecision(record)
		return Candidate{}, 0, record, ErrUnavailable
	}

	highestTier := 0
	for i := range groups {
		// already_tried remains part of the route's quality ceiling on a safe
		// retry; otherwise a retry could silently redefine the allowed tier.
		if reason := groups[i].explanation.FilterReason; reason != "" && reason != "already_tried" {
			continue
		}
		if rank := qualityRank(groups[i].candidate.Physical.QualityTier); rank > highestTier {
			highestTier = rank
		}
	}
	if !p.policy.Scoring.Auto {
		for _, i := range eligibleAll {
			if highestTier > 0 && qualityRank(groups[i].candidate.Physical.QualityTier) != highestTier {
				return Candidate{}, 0, nil, ErrInvalidPolicy
			}
		}
	} else if !p.policy.Scoring.AllowDegradation || !p.request.Preference.AllowFallbacks {
		for _, i := range eligibleAll {
			if qualityRank(groups[i].candidate.Physical.QualityTier) != highestTier {
				groups[i].explanation.FilterReason = "quality_below_floor"
				groups[i].explanation.Reason = "quality_below_floor"
				record.Candidates[i].FilterReason = "quality_below_floor"
				record.Candidates[i].Reason = "quality_below_floor"
			}
		}
		if p.request.Sticky != nil && stickyBreak(p.policy, "quality") {
			for _, i := range eligibleAll {
				candidate := groups[i].candidate
				if candidate.ChannelID == p.request.Sticky.ChannelID && candidate.PhysicalModelID == p.request.Sticky.PhysicalModelID && groups[i].explanation.FilterReason == "quality_below_floor" {
					stickyState = "break_quality"
				}
			}
		}
	}
	// Tier policy applies across the complete eligible set, then the ordinary
	// highest-priority pool is rebuilt for this attempt (including safe retries).
	pool = pool[:0]
	weight = 0
	highest = math.MinInt
	for _, i := range eligibleAll {
		if groups[i].explanation.FilterReason != "" {
			continue
		}
		candidate := groups[i].candidate
		if candidate.Priority > highest {
			highest = candidate.Priority
			pool = pool[:0]
			weight = 0
		}
		if candidate.Priority == highest {
			if math.MaxUint64-weight < candidate.Weight {
				return Candidate{}, 0, nil, ErrInvalidPolicy
			}
			pool = append(pool, i)
			weight += candidate.Weight
		}
	}
	if len(pool) == 0 {
		record.Sticky = stickyState
		for i := range record.Candidates {
			record.Candidates[i].Reason = record.Candidates[i].FilterReason
		}
		finalizeRouteDecision(record)
		return Candidate{}, 0, record, ErrUnavailable
	}

	stickyIndex := -1
	if p.policy.Stickiness != nil && p.policy.Stickiness.Enabled && p.request.Sticky != nil && stickyState == "" {
		for _, i := range eligibleAll {
			candidate := groups[i].candidate
			if groups[i].explanation.FilterReason != "" {
				continue
			}
			if candidate.ChannelID == p.request.Sticky.ChannelID && candidate.PhysicalModelID == p.request.Sticky.PhysicalModelID {
				stickyIndex = i
				break
			}
		}
	}
	if stickyIndex >= 0 {
		pool = []int{stickyIndex}
		weight = groups[stickyIndex].candidate.Weight
		stickyState = "inherit"
	} else if len(p.request.Preference.Order) > 0 {
		bestOrder := len(p.request.Preference.Order) + 1
		for _, i := range pool {
			for position, selector := range p.request.Preference.Order {
				if selectorMatches(groups[i].candidate, selector) && position < bestOrder {
					bestOrder = position
				}
			}
		}
		kept := pool[:0]
		if bestOrder < len(p.request.Preference.Order) {
			for _, i := range pool {
				matched := false
				if selectorMatches(groups[i].candidate, p.request.Preference.Order[bestOrder]) {
					matched = true
				}
				if matched {
					kept = append(kept, i)
				} else {
					groups[i].explanation.Reason = "lower_score"
				}
			}
			pool = kept
			weight = sumGroupWeights(groups, pool)
		}
	} else if groups[pool[0]].candidate.CostPolicy.Mode == "cost_first" {
		cheapest, err := cheapestGroups(groups, pool, p.request)
		if err != nil {
			return Candidate{}, 0, nil, err
		}
		for _, i := range pool {
			groups[i].explanation.Reason = "higher_cost"
		}
		pool = cheapest
		weight = sumGroupWeights(groups, pool)
	}
	if stickyIndex < 0 && len(p.request.Preference.Order) == 0 {
		var err error
		pool, err = sortPreference(groups, pool, p.request.Preference.Sort, p.policy.CostPolicy.Mode == "cost_first", p.request)
		if err != nil {
			return Candidate{}, 0, nil, err
		}
		weight = sumGroupWeights(groups, pool)
	}
	for _, i := range pool {
		groups[i].explanation.Reason = "weighted_not_selected"
	}

	roll := uint64(0)
	if stickyIndex < 0 {
		if weight == 0 {
			return Candidate{}, 0, record, ErrInvalidPolicy
		}
		roll = e.config.RandN(weight)
		if roll >= weight {
			return Candidate{}, 0, nil, ErrInvalidPolicy
		}
	}
	record.Draw = strconv.FormatUint(roll, 10)
	selectedGroup := pool[0]
	remaining := roll
	for _, i := range pool {
		if remaining < groups[i].candidate.Weight {
			selectedGroup = i
			break
		}
		remaining -= groups[i].candidate.Weight
	}
	group := &groups[selectedGroup]
	selected := group.members[0]
	if len(group.members) > 1 {
		memberRoll := e.config.RandN(group.weight)
		if memberRoll >= group.weight {
			return Candidate{}, 0, nil, ErrInvalidPolicy
		}
		text := strconv.FormatUint(memberRoll, 10)
		group.explanation.Score.CredentialDraw = &text
		for _, member := range group.members {
			memberWeight := credentialWeight(p.candidates[member])
			if memberRoll < memberWeight {
				selected = member
				break
			}
			memberRoll -= memberWeight
		}
	}
	candidate := p.candidates[selected]
	e.inflight[candidate.ChannelID]++
	e.credentialInflight[credentialKey(candidate)]++
	h := e.get(key(candidate))
	if h.state == HalfOpen {
		h.probes++
	}
	group.explanation.Reason = "selected"
	if stickyIndex >= 0 {
		group.explanation.Reason = "sticky_override"
	} else if p.policy.Stickiness != nil && p.policy.Stickiness.Enabled && p.request.SessionID != "" && stickyState == "" {
		stickyState = "establish"
	}
	record.Sticky = stickyState
	record.SelectedBindingID = group.candidate.BindingID
	record.SelectedPhysicalModelID = group.candidate.PhysicalModelID
	if p.policy.Scoring.Auto && highestTier > 0 && qualityRank(group.candidate.Physical.QualityTier) < highestTier {
		record.Degradation = "tier_changed"
	}
	for i := range groups {
		record.Candidates[i] = groups[i].explanation
		if record.Candidates[i].Reason == "" {
			record.Candidates[i].Reason = record.Candidates[i].FilterReason
		}
	}
	legacy := make([]Decision, len(groups))
	for i := range groups {
		legacy[i] = Decision{ChannelID: groups[i].candidate.ChannelID, VersionID: groups[i].candidate.VersionID, Priority: groups[i].candidate.Priority, Weight: groups[i].candidate.Weight, Reason: record.Candidates[i].Reason}
	}
	candidate.Decisions = legacy
	finalizeRouteDecision(record)
	candidate.RouteDecision = cloneDecision(record)
	return candidate, h.generation, record, nil
}

func sumGroupWeights(groups []selectionGroup, pool []int) uint64 {
	var total uint64
	for _, i := range pool {
		total += groups[i].candidate.Weight
	}
	return total
}

func compareFrozenCost(left, right Candidate, request RequestShape) (int, error) {
	if request.Economics != nil {
		leftSnapshot, err := costSnapshot(left, request)
		if err != nil {
			return 0, err
		}
		rightSnapshot, err := costSnapshot(right, request)
		if err != nil {
			return 0, err
		}
		leftCost, err := economicCostUpper(request, leftSnapshot)
		if err != nil {
			return 0, err
		}
		rightCost, err := economicCostUpper(request, rightSnapshot)
		if err != nil {
			return 0, err
		}
		return int(leftCost - rightCost), nil
	}
	leftBounds, err := supplierBounds(left)
	if err != nil {
		return 0, err
	}
	rightBounds, err := supplierBounds(right)
	if err != nil {
		return 0, err
	}
	leftRates, err := left.CostQuote.Rates()
	if err != nil {
		return 0, err
	}
	rightRates, err := right.CostQuote.Rates()
	if err != nil {
		return 0, err
	}
	return metering.CompareExactCost(leftBounds, leftRates, rightBounds, rightRates)
}

func cheapestGroups(groups []selectionGroup, pool []int, request RequestShape) ([]int, error) {
	var lowest *Candidate
	cheapest := []int{}
	for _, i := range pool {
		candidate := groups[i].candidate
		comparison := -1
		if lowest != nil {
			var err error
			comparison, err = compareFrozenCost(candidate, *lowest, request)
			if err != nil {
				return nil, err
			}
		}
		if lowest == nil || comparison < 0 {
			lowest = &groups[i].candidate
			cheapest = cheapest[:0]
		}
		if comparison <= 0 {
			cheapest = append(cheapest, i)
		}
	}
	return cheapest, nil
}

func sortPreference(groups []selectionGroup, pool []int, preference string, costFirst bool, request RequestShape) ([]int, error) {
	if preference == "" || preference == "price" && costFirst || len(pool) < 2 {
		return pool, nil
	}
	best := pool[:0]
	for _, i := range pool {
		if len(best) == 0 {
			best = append(best, i)
			continue
		}
		comparison, known, err := comparePreference(groups[i].candidate, groups[best[0]].candidate, preference, request)
		if err != nil {
			return nil, err
		}
		if !known {
			continue
		}
		if comparison < 0 {
			for _, j := range best {
				groups[j].explanation.Reason = preferenceExclusion(preference)
			}
			best = best[:0]
		}
		if comparison <= 0 {
			best = append(best, i)
		} else {
			groups[i].explanation.Reason = preferenceExclusion(preference)
		}
	}
	for _, i := range pool {
		found := false
		for _, j := range best {
			if i == j {
				found = true
			}
		}
		if !found && groups[i].explanation.Reason == "weighted_not_selected" || !found && groups[i].explanation.Reason == "lower_priority" {
			groups[i].explanation.Reason = preferenceExclusion(preference)
		}
	}
	return best, nil
}

func preferenceExclusion(preference string) string {
	if preference == "price" {
		return "higher_cost"
	}
	return "lower_score"
}

// comparePreference compares only frozen, sufficiently sampled statistics.
func comparePreference(left, right Candidate, preference string, request RequestShape) (comparison int, known bool, err error) {
	if preference == "price" {
		comparison, err = compareFrozenCost(left, right, request)
		return comparison, true, err
	}
	var leftValue, rightValue *int64
	if preference == "latency" {
		leftValue, rightValue = left.Stats.P50FirstContentUS, right.Stats.P50FirstContentUS
	} else {
		leftValue, rightValue = left.Stats.ThroughputMicroS, right.Stats.ThroughputMicroS
	}
	if leftValue == nil {
		if rightValue == nil {
			return 0, true, nil
		}
		return 1, true, nil
	}
	if rightValue == nil {
		return -1, true, nil
	}
	if *leftValue == *rightValue {
		if preference == "latency" && left.Stats.P95FirstContentUS != nil && right.Stats.P95FirstContentUS != nil {
			leftValue, rightValue = left.Stats.P95FirstContentUS, right.Stats.P95FirstContentUS
		} else {
			return 0, true, nil
		}
	}
	if preference == "throughput" {
		if *leftValue > *rightValue {
			return -1, true, nil
		}
		if *leftValue < *rightValue {
			return 1, true, nil
		}
		return 0, true, nil
	}
	if *leftValue < *rightValue {
		return -1, true, nil
	}
	if *leftValue > *rightValue {
		return 1, true, nil
	}
	return 0, true, nil
}

func finalizeRouteDecision(record *RouteDecision) {
	raw, _ := json.Marshal(record.Candidates)
	digest := sha256.Sum256(raw)
	record.CandidatesDigest = hex.EncodeToString(digest[:])
}
