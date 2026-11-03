package procurementprefix

import (
	"strconv"
)

type requestReplayResult struct {
	Stats         RequestIncoming
	Candidates    propositionKeys
	Complete      bool
	Unimplemented []string
}

type requestReplay struct {
	w            *replayWork
	rows         Rows
	hits         [7]keySet
	candidates   propositionKeys
	requests     map[string]RequestRecordRow
	attempts     map[string]UpstreamAttemptRow
	origins      map[string]RequestOriginRow
	heads        map[string]RequestHeadRow
	attemptBirth map[string]AttemptOriginRow
	incoming     map[string]IncomingSourceRow
	choices      map[string]MeterChoiceRow
	choiceFor    map[string]MeterChoiceRow
	meters       map[string]MeteringEventRow
	meterKeys    map[string]MeteringEventRow
	meterOrigins map[string]MeterOriginRow
	conflicts    map[string]MeteringEventConflictRow
	conflictSrc  map[string]ConflictSourceRow
	finals       map[string]FinalizationRecordRow
	decisions    map[string]RequestDecisionRow
	decisionFor  map[string]RequestDecisionRow
	nodes        map[string]AttemptNodeRow
	nodeSteps    map[string]AttemptNodeRow
	identities   map[string]ProcurementEvidenceIdentityRow
	evidence     map[string]EvidenceChoiceRow
	members      map[string]EvidenceMemberRow
	usage        map[string]requestUsage
	meterUsage   map[string]requestUsage
	incomingOK   keySet
	meterOK      keySet
	choiceOK     keySet
	requestOK    keySet
	inputGap     keySet
	meterGap     keySet
	choiceGap    keySet
}

const (
	requestDigestHit = iota
	requestMeterHit
	requestChoiceHit
	requestConflictHit
	requestStickyHit
	requestFinalHit
	requestLatestHit
)

// A counter may cover several physical row kinds. Keep that kind in the key;
// two different rows may legally have identical ID text in different tables.
func (r *requestReplay) mark(kind int, table string, parts ...string) {
	r.hits[kind][tupleKey(table)+tupleKey(parts...)] = struct{}{}
}

func checkRequestIncoming(w *replayWork, rows Rows) (requestReplayResult, error) {
	if err := w.tick(); err != nil {
		return requestReplayResult{}, err
	}
	r := requestReplay{w: w, rows: rows, candidates: newPropositionKeys(), usage: make(map[string]requestUsage), meterUsage: make(map[string]requestUsage), incomingOK: make(keySet), meterOK: make(keySet), choiceOK: make(keySet), requestOK: make(keySet)}
	for i := range r.hits {
		r.hits[i] = make(keySet)
	}
	r.inputGap, r.meterGap, r.choiceGap = make(keySet), make(keySet), make(keySet)
	for _, phase := range []func() error{r.index, r.inputs, r.meterBirths, r.meterChoices, r.requestChains, r.identityBindings} {
		if err := phase(); err != nil {
			return requestReplayResult{}, err
		}
	}
	if err := w.b.check(); err != nil {
		return requestReplayResult{}, err
	}
	return requestReplayResult{
		Stats:      RequestIncoming{uint64(len(r.hits[0])), uint64(len(r.hits[1])), uint64(len(r.hits[2])), uint64(len(r.hits[3])), uint64(len(r.hits[4])), uint64(len(r.hits[5])), uint64(len(r.hits[6]))},
		Candidates: r.candidates, Complete: true,
	}, nil
}

func (r *requestReplay) index() (err error) {
	o, p := r.rows.OriginalRows, r.rows.PrefixRows
	if r.requests, err = requestIndex(r.w, o.RequestRecords, func(v RequestRecordRow) string { return tupleKey(v.ID) }); err != nil {
		return err
	}
	if r.attempts, err = requestIndex(r.w, o.UpstreamAttempts, func(v UpstreamAttemptRow) string { return tupleKey(v.ID) }); err != nil {
		return err
	}
	if r.origins, err = requestIndex(r.w, p.RequestOrigins, func(v RequestOriginRow) string { return tupleKey(v.RequestID) }); err != nil {
		return err
	}
	if r.heads, err = requestIndex(r.w, p.RequestHeads, func(v RequestHeadRow) string { return tupleKey(v.RequestID) }); err != nil {
		return err
	}
	if r.attemptBirth, err = requestIndex(r.w, p.AttemptOrigins, func(v AttemptOriginRow) string { return tupleKey(v.AttemptID) }); err != nil {
		return err
	}
	if r.incoming, err = requestIndex(r.w, p.IncomingSources, func(v IncomingSourceRow) string { return tupleKey(v.ID) }); err != nil {
		return err
	}
	if r.choices, err = requestIndex(r.w, p.MeterChoices, func(v MeterChoiceRow) string { return tupleKey(v.ID) }); err != nil {
		return err
	}
	if r.choiceFor, err = requestIndex(r.w, p.MeterChoices, func(v MeterChoiceRow) string { return tupleKey(v.IncomingSourceID) }); err != nil {
		return err
	}
	if r.meters, err = requestIndex(r.w, o.MeteringEvents, func(v MeteringEventRow) string { return tupleKey(v.ID) }); err != nil {
		return err
	}
	if r.meterKeys, err = requestIndex(r.w, o.MeteringEvents, func(v MeteringEventRow) string { return tupleKey(v.TenantID, v.Source, v.SourceEventID) }); err != nil {
		return err
	}
	if r.meterOrigins, err = requestIndex(r.w, p.MeterOrigins, func(v MeterOriginRow) string { return tupleKey(v.MeterEventID) }); err != nil {
		return err
	}
	if r.conflicts, err = requestIndex(r.w, o.MeteringEventConflicts, func(v MeteringEventConflictRow) string { return tupleKey(v.ID) }); err != nil {
		return err
	}
	if r.conflictSrc, err = requestIndex(r.w, p.ConflictSources, func(v ConflictSourceRow) string { return tupleKey(v.ConflictID) }); err != nil {
		return err
	}
	if r.finals, err = requestIndex(r.w, o.FinalizationRecords, func(v FinalizationRecordRow) string { return tupleKey(v.ID) }); err != nil {
		return err
	}
	if r.decisions, err = requestIndex(r.w, p.RequestDecisions, func(v RequestDecisionRow) string { return tupleKey(v.ID) }); err != nil {
		return err
	}
	if r.decisionFor, err = requestIndex(r.w, p.RequestDecisions, func(v RequestDecisionRow) string { return tupleKey(v.IncomingSourceID) }); err != nil {
		return err
	}
	if r.nodes, err = requestIndex(r.w, p.AttemptNodes, func(v AttemptNodeRow) string { return tupleKey(v.ID) }); err != nil {
		return err
	}
	if r.nodeSteps, err = requestIndex(r.w, p.AttemptNodes, func(v AttemptNodeRow) string { return tupleKey(v.RootID, v.StepNo) }); err != nil {
		return err
	}
	if r.identities, err = requestIndex(r.w, o.ProcurementEvidenceIdentities, func(v ProcurementEvidenceIdentityRow) string { return tupleKey(v.AttemptID, v.EvidenceDigest) }); err != nil {
		return err
	}
	if r.evidence, err = requestIndex(r.w, p.EvidenceChoices, func(v EvidenceChoiceRow) string { return tupleKey(v.ID) }); err != nil {
		return err
	}
	if r.members, err = requestIndex(r.w, p.EvidenceMembers, func(v EvidenceMemberRow) string { return tupleKey(v.AttemptID, v.IdentityDigest) }); err != nil {
		return err
	}
	return nil
}

func (r *requestReplay) inputs() error {
	for _, in := range r.rows.PrefixRows.IncomingSources {
		if err := r.w.tick(); err != nil {
			return err
		}
		u, valid := requestIncomingValid(in)
		if !valid || in.InputDigest != requestInputDigest(in, u) {
			r.mark(requestDigestHit, "pp_incoming_sources", in.ID)
			continue
		}
		request, present := r.requests[tupleKey(in.RequestID)]
		birth, tracked := r.origins[tupleKey(in.RequestID)]
		gap := present && !tracked && request.PPOriginRootID == nil
		valid = present && request.TenantID == in.TenantID && (tracked && birth.TenantID == in.TenantID || gap)
		if in.AttemptID != nil {
			a, exists := r.attempts[tupleKey(*in.AttemptID)]
			origin, hasOrigin := r.attemptBirth[tupleKey(*in.AttemptID)]
			legacy := exists && !hasOrigin && a.PPOriginRootID == nil
			gap = gap || legacy
			valid = valid && exists && a.TenantID == in.TenantID && a.RequestID == in.RequestID && (hasOrigin && origin.TenantID == in.TenantID && origin.RequestID == in.RequestID || legacy)
		}
		choice, hasChoice := r.choiceFor[tupleKey(in.ID)]
		decision, hasDecision := r.decisionFor[tupleKey(in.ID)]
		valid = valid && hasChoice && hasDecision && choice.RootID == in.RootID && choice.CreatedXID == in.CreatedXID && decision.RootID == in.RootID && decision.CreatedXID == in.CreatedXID && decision.MeterChoiceID == choice.ID
		if !valid {
			r.mark(requestChoiceHit, "pp_incoming_sources", in.ID)
			continue
		}
		if _, unambiguous := r.incoming[tupleKey(in.ID)]; unambiguous {
			r.usage[tupleKey(in.ID)] = u
			if gap {
				r.inputGap.add(in.ID)
			} else {
				r.incomingOK.add(in.ID)
			}
		}
	}
	return nil
}

func requestOriginalPointer(root, step, kind, source, role, xid *string, wantRoot, wantStep, wantKind, wantSource, wantRole, wantXID string) bool {
	return requestPointer(root, wantRoot) && requestPointer(step, wantStep) && requestPointer(kind, wantKind) && requestPointer(source, wantSource) && requestPointer(role, wantRole) && requestPointer(xid, wantXID)
}

func (r *requestReplay) meterBirths() error {
	for _, meter := range r.rows.OriginalRows.MeteringEvents {
		if err := r.w.tick(); err != nil {
			return err
		}
		u, valid, parseErr := r.parseUsage(meter.NormalizedUsage)
		if parseErr != nil {
			return parseErr
		}
		if valid {
			r.meterUsage[tupleKey(meter.ID)] = u
		}
		origin, tracked := r.meterOrigins[tupleKey(meter.ID)]
		if !tracked {
			if meter.PPRootID != nil || meter.PPSourceID != nil {
				r.mark(requestMeterHit, "metering_events", meter.ID)
			} else {
				r.meterGap.add(meter.ID)
			}
			continue
		}
		choice, hasChoice := r.choices[tupleKey(origin.SourceID)]
		in, hasInput := r.incoming[tupleKey(origin.IncomingSourceID)]
		_, inputOK := r.incomingOK[tupleKey(in.ID)]
		_, inputGap := r.inputGap[tupleKey(in.ID)]
		actual, hasID := r.meters[tupleKey(meter.ID)]
		keyed, hasKey := r.meterKeys[tupleKey(meter.TenantID, meter.Source, meter.SourceEventID)]
		valid = valid && hasID && actual.ID == meter.ID && hasKey && keyed.ID == meter.ID && hasChoice && hasInput && (inputOK || inputGap) && choice.Disposition == "created" && choice.MeterEventID == meter.ID && choice.MeterOriginID == meter.ID && choice.IncomingSourceID == in.ID && origin.RootID == choice.RootID && origin.StepNo == choice.StepNo && origin.StepKind == "meter_choice" && origin.CreatedXID == choice.CreatedXID && choice.RootID == in.RootID && meter.TenantID == in.TenantID && meter.RequestID == in.RequestID && requestSameOptional(meter.AttemptID, in.AttemptID) && meter.Source == in.Source && meter.SourceEventID == in.SourceEventID && u == r.usage[tupleKey(in.ID)] && meter.SchemaVersion == "1" && finite(meter.HappenedAt) && finite(meter.ReceivedAt) && meter.HappenedAt.Time.Equal(in.EffectiveObservedAt.Time) && requestOriginalPointer(meter.PPRootID, meter.PPRootStep, meter.PPSourceKind, meter.PPSourceID, meter.PPSourceRole, meter.PPCreatedXID, choice.RootID, choice.StepNo, "meter_choice", choice.ID, "meter_origin", choice.CreatedXID)
		if !valid {
			r.mark(requestMeterHit, "metering_events", meter.ID)
		} else if inputGap {
			r.meterGap.add(meter.ID)
		} else {
			r.meterOK.add(meter.ID)
		}
	}
	for _, origin := range r.rows.PrefixRows.MeterOrigins {
		if err := r.w.tick(); err != nil {
			return err
		}
		if _, exists := r.meters[tupleKey(origin.MeterEventID)]; !exists {
			r.mark(requestMeterHit, "pp_meter_origins", origin.MeterEventID)
		}
	}
	return nil
}

type requestMeterMember struct {
	meter   MeteringEventRow
	ordinal int64
}
type requestChoicePosition struct {
	choice  MeterChoiceRow
	ordinal int64
}

func (r *requestReplay) meterChoices() error {
	members := make(map[string][]requestMeterMember)
	incomplete := make(keySet)
	for _, meter := range r.rows.OriginalRows.MeteringEvents {
		if err := r.w.tick(); err != nil {
			return err
		}
		if meter.AttemptID == nil {
			continue
		}
		key := tupleKey(*meter.AttemptID)
		origin, originExists := r.meterOrigins[tupleKey(meter.ID)]
		node, nodeExists := r.nodeSteps[tupleKey(origin.RootID, origin.StepNo)]
		_, valid := r.meterOK[tupleKey(meter.ID)]
		ordinal, err := strconv.ParseInt(node.Ordinal, 10, 64)
		if !originExists || !nodeExists || !valid || err != nil || ordinal < 1 || node.AttemptID != *meter.AttemptID || node.SourceID != origin.SourceID || node.StepKind != "meter_choice" {
			incomplete[key] = struct{}{}
			continue
		}
		members[key] = append(members[key], requestMeterMember{meter, ordinal})
	}
	positions := make(map[string][]requestChoicePosition)
	for _, choice := range r.rows.PrefixRows.MeterChoices {
		if err := r.w.tick(); err != nil {
			return err
		}
		in, exists := r.incoming[tupleKey(choice.IncomingSourceID)]
		_, inputOK := r.incomingOK[tupleKey(in.ID)]
		meter, meterExists := r.meters[tupleKey(choice.MeterEventID)]
		_, inputGap := r.inputGap[tupleKey(in.ID)]
		_, meterGap := r.meterGap[tupleKey(meter.ID)]
		keyed, keyExists := r.meterKeys[tupleKey(in.TenantID, in.Source, in.SourceEventID)]
		_, birthOK := r.meterOK[tupleKey(meter.ID)]
		origin, hasOrigin := r.meterOrigins[tupleKey(meter.ID)]
		valid := exists && (inputOK || inputGap) && meterExists && keyExists && keyed.ID == meter.ID && choice.TenantID == in.TenantID && choice.RequestID == in.RequestID && requestSameOptional(choice.AttemptID, in.AttemptID) && choice.MeterOriginID == meter.ID && choice.RootID == in.RootID && choice.CreatedXID == in.CreatedXID && requestOneOf(choice.Disposition, "created", "reused")
		if valid && choice.Disposition == "created" {
			valid = hasOrigin && origin.SourceID == choice.ID && origin.IncomingSourceID == in.ID
		}
		if valid && choice.Disposition == "reused" && hasOrigin {
			valid = origin.SourceID != choice.ID && origin.IncomingSourceID != in.ID
		}
		if !valid {
			r.mark(requestChoiceHit, "pp_meter_choices", choice.ID)
			continue
		}
		if inputGap || meterGap {
			// Missing history is not evidence for a false source-key match.
			// Payload mismatch has priority and is decidable from these raw
			// rows even when the old meter's birth cannot be reconstructed.
			mismatch := meter.RequestID != in.RequestID || !requestSameOptional(meter.AttemptID, in.AttemptID) || r.meterUsage[tupleKey(meter.ID)] != r.usage[tupleKey(in.ID)]
			if mismatch || in.AttemptID == nil || in.UsageStatus != "confirmed_final" {
				if !r.choiceCollision(choice, in, meter, false) {
					continue
				}
			} else if !requestOneOf(choice.CollisionKind, "none", "other_confirmed_usage") || choice.CollisionKind == "none" && choice.CompetingMeterID != nil || choice.CollisionKind == "other_confirmed_usage" && choice.CompetingMeterID == nil {
				r.mark(requestChoiceHit, "pp_meter_choices", choice.ID)
				continue
			}
			r.choiceGap.add(choice.ID)
			continue
		}
		if !birthOK {
			continue
		} // A real legacy meter remains an unproven boundary.
		// Request ordinal is the cross-attempt/no-attempt order. A reused
		// source in this request must already have been born; an attempt cut
		// alone cannot reject a fabricated future no-attempt source.
		if choice.Disposition == "reused" && meter.RequestID == in.RequestID {
			creator := r.decisionFor[tupleKey(origin.IncomingSourceID)]
			current := r.decisionFor[tupleKey(in.ID)]
			bornAt, bornErr := strconv.ParseInt(creator.Ordinal, 10, 64)
			selectedAt, selectedErr := strconv.ParseInt(current.Ordinal, 10, 64)
			if bornErr != nil || selectedErr != nil || bornAt < 1 || bornAt >= selectedAt || creator.RequestID != in.RequestID {
				r.mark(requestChoiceHit, "pp_meter_choices", choice.ID)
				continue
			}
		}
		if choice.AttemptID == nil {
			if choice.AttemptCutBeforeID != nil || choice.AttemptCutAfterMeterID != nil {
				r.mark(requestChoiceHit, "pp_meter_choices", choice.ID)
				continue
			}
			if r.choiceCollision(choice, in, meter, false) {
				r.choiceOK.add(choice.ID)
			}
			continue
		}
		node, nodeExists := r.nodeSteps[tupleKey(choice.RootID, choice.StepNo)]
		ordinal, err := strconv.ParseInt(node.Ordinal, 10, 64)
		if !nodeExists || err != nil || ordinal < 1 || node.AttemptID != *choice.AttemptID || node.StepKind != "meter_choice" || node.SourceID != choice.ID || !requestSameOptional(choice.AttemptCutBeforeID, node.PreviousNodeID) || !requestPointer(choice.AttemptCutAfterMeterID, node.ID) {
			r.mark(requestChoiceHit, "pp_meter_choices", choice.ID)
			continue
		}
		positions[tupleKey(*choice.AttemptID)] = append(positions[tupleKey(*choice.AttemptID)], requestChoicePosition{choice, ordinal})
	}
	for attempt, choices := range positions {
		if err := r.w.tick(); err != nil {
			return err
		}
		if _, gap := incomplete[attempt]; gap {
			for _, item := range choices {
				r.choiceGap.add(item.choice.ID)
			}
			continue
		}
		births := members[attempt]
		if err := replaySort(r.w, births, func(a, b requestMeterMember) bool { return a.ordinal < b.ordinal }); err != nil {
			return err
		}
		if err := replaySort(r.w, choices, func(a, b requestChoicePosition) bool { return a.ordinal < b.ordinal }); err != nil {
			return err
		}
		confirmed := make(map[requestUsage]int)
		count, next := 0, 0
		for _, positioned := range choices {
			if err := r.w.tick(); err != nil {
				return err
			}
			for next < len(births) && births[next].ordinal <= positioned.ordinal {
				if err := r.w.tick(); err != nil {
					return err
				}
				u := r.meterUsage[tupleKey(births[next].meter.ID)]
				if u.Status == "confirmed_final" {
					confirmed[u]++
					count++
				}
				next++
			}
			choice := positioned.choice
			in := r.incoming[tupleKey(choice.IncomingSourceID)]
			meter := r.meters[tupleKey(choice.MeterEventID)]
			u := r.usage[tupleKey(in.ID)]
			hasOther := u.Status == "confirmed_final" && count > confirmed[u]
			// Reuse in the same attempt must refer to an already born member.
			if choice.Disposition == "reused" && requestSameOptional(meter.AttemptID, in.AttemptID) && meter.RequestID == in.RequestID {
				origin := r.meterOrigins[tupleKey(meter.ID)]
				node := r.nodeSteps[tupleKey(origin.RootID, origin.StepNo)]
				originOrdinal, _ := strconv.ParseInt(node.Ordinal, 10, 64)
				if originOrdinal >= positioned.ordinal {
					r.mark(requestChoiceHit, "pp_meter_choices", choice.ID)
					continue
				}
			}
			if choice.CollisionKind == "other_confirmed_usage" {
				other, found := r.meters[tupleKey(requestOptional(choice.CompetingMeterID))]
				origin, born := r.meterOrigins[tupleKey(other.ID)]
				node, linked := r.nodeSteps[tupleKey(origin.RootID, origin.StepNo)]
				ordinal, err := strconv.ParseInt(node.Ordinal, 10, 64)
				ou, usageFound := r.meterUsage[tupleKey(other.ID)]
				if choice.CompetingMeterID == nil || !found || !born || !linked || err != nil || ordinal > positioned.ordinal || node.AttemptID != *in.AttemptID || other.TenantID != in.TenantID || other.ID == meter.ID || !requestSameOptional(other.AttemptID, in.AttemptID) || !usageFound || ou.Status != "confirmed_final" || ou == u {
					r.mark(requestChoiceHit, "pp_meter_choices", choice.ID)
					continue
				}
			}
			if r.choiceCollision(choice, in, meter, hasOther) {
				r.choiceOK.add(choice.ID)
			}
		}
	}
	return nil
}

func (r *requestReplay) choiceCollision(choice MeterChoiceRow, in IncomingSourceRow, meter MeteringEventRow, hasOther bool) bool {
	want := "none"
	if meter.RequestID != in.RequestID || !requestSameOptional(meter.AttemptID, in.AttemptID) || r.meterUsage[tupleKey(meter.ID)] != r.usage[tupleKey(in.ID)] {
		want = "source_payload_mismatch"
	} else if in.AttemptID != nil && in.UsageStatus == "confirmed_final" && hasOther {
		want = "other_confirmed_usage"
	}
	if choice.CollisionKind != want || (want != "other_confirmed_usage" && choice.CompetingMeterID != nil) {
		r.mark(requestChoiceHit, "pp_meter_choices", choice.ID)
		return false
	}
	return true
}

func (r *requestReplay) requestChains() error {
	chains := make(map[string][]RequestDecisionRow)
	for _, decision := range r.decisions {
		if err := r.w.tick(); err != nil {
			return err
		}
		chains[tupleKey(decision.RequestID)] = append(chains[tupleKey(decision.RequestID)], decision)
	}
	usedFinals, usedConflicts := make(keySet), make(keySet)
	for _, origin := range r.origins {
		if err := r.w.tick(); err != nil {
			return err
		}
		request, present := r.requests[tupleKey(origin.RequestID)]
		head, headPresent := r.heads[tupleKey(origin.RequestID)]
		chain := chains[tupleKey(origin.RequestID)]
		if err := replaySort(r.w, chain, func(left, right RequestDecisionRow) bool {
			a, _ := strconv.ParseInt(left.Ordinal, 10, 64)
			b, _ := strconv.ParseInt(right.Ordinal, 10, 64)
			if a == b {
				return left.ID < right.ID
			}
			return a < b
		}); err != nil {
			return err
		}
		valid := present && headPresent && request.TenantID == origin.TenantID && origin.InitialLatestID == nil && origin.InitialStatus == "accepted" && origin.InitialBillingStatus == "reserved"
		var latest, previous *string
		lastXID := origin.CreatedXID
		var finalCandidates, conflictCandidates []string
		for i, decision := range chain {
			if err := r.w.tick(); err != nil {
				return err
			}
			ordinal, err := strconv.ParseInt(decision.Ordinal, 10, 64)
			if err != nil || ordinal != int64(i+1) || decision.PreviousOrdinal != strconv.Itoa(i) || !requestSameOptional(previous, decision.PreviousDecisionID) || !requestSameOptional(latest, decision.BeforeLatestID) || decision.TenantID != origin.TenantID {
				r.mark(requestStickyHit, "pp_request_decisions", decision.ID)
				valid = false
			}
			in, hasInput := r.incoming[tupleKey(decision.IncomingSourceID)]
			choice, hasChoice := r.choices[tupleKey(decision.MeterChoiceID)]
			_, inputOK := r.incomingOK[tupleKey(in.ID)]
			_, choiceOK := r.choiceOK[tupleKey(choice.ID)]
			_, inputGap := r.inputGap[tupleKey(in.ID)]
			_, choiceGap := r.choiceGap[tupleKey(choice.ID)]
			linked := hasInput && hasChoice && (inputOK || inputGap) && (choiceOK || choiceGap) && in.RequestID == origin.RequestID && in.TenantID == origin.TenantID && choice.IncomingSourceID == in.ID && decision.RootID == in.RootID && decision.CreatedXID == in.CreatedXID
			if inputGap || choiceGap {
				valid = false
			}
			stage, content := in.RawDeliveryStage, in.RawContentWritten
			if content || stage == "content" {
				stage, content = "content", true
			}
			if latest != nil {
				prior, exists := r.finals[tupleKey(*latest)]
				if !exists {
					linked = false
				} else if prior.ContentWritten || prior.DeliveryStage == "content" {
					stage, content = "content", true
				} else if prior.DeliveryStage == "committed" && requestOneOf(stage, "none", "unknown") {
					stage = "committed"
				}
			}
			if !linked || stage != decision.NormalizedDeliveryStage || content != decision.NormalizedContentWritten {
				r.mark(requestStickyHit, "pp_request_decisions", decision.ID)
				valid = false
			}
			if decision.Branch == "normal" {
				finalID := requestOptional(decision.FinalizationID)
				f, exists := r.finals[tupleKey(finalID)]
				wantStatus := "failed"
				if in.TransportEndReason == "client_cancelled" {
					wantStatus = "cancelled"
				} else if in.ProtocolOutcome == "completed" {
					wantStatus = "succeeded"
				}
				_, already := usedFinals[tupleKey(finalID)]
				okay := linked && decision.FinalizationID != nil && decision.ConflictSourceID == nil && choice.CollisionKind == "none" && !already && exists && f.ID == in.InputFinalizationID && f.TenantID == in.TenantID && f.RequestID == in.RequestID && f.EventID == choice.MeterEventID && f.InputDigest == in.InputDigest && f.ProtocolOutcome == in.ProtocolOutcome && f.TransportEndReason == in.TransportEndReason && f.DeliveryStage == stage && f.ContentWritten == content && f.FinishReason == in.FinishReason && f.PolicyVersion == origin.PolicyVersion && finite(f.CreatedAt) && requestPointer(decision.AfterLatestID, f.ID) && decision.AfterStatus == wantStatus && requestOriginalPointer(f.PPRootID, f.PPRootStep, f.PPSourceKind, f.PPSourceID, f.PPSourceRole, f.PPCreatedXID, decision.RootID, decision.StepNo, "request_decision", decision.ID, "finalization", decision.CreatedXID)
				if !okay {
					if decision.FinalizationID == nil {
						r.mark(requestFinalHit, "pp_request_decisions", decision.ID)
					} else {
						r.mark(requestFinalHit, "finalization_records", finalID)
					}
					valid = false
				} else {
					finalCandidates = append(finalCandidates, f.ID)
				}
				usedFinals.add(finalID)
				latest = cloneString(decision.AfterLatestID)
			} else if decision.Branch == "conflict" {
				id := requestOptional(decision.ConflictSourceID)
				c, exists := r.conflicts[tupleKey(id)]
				source, hasSource := r.conflictSrc[tupleKey(id)]
				u, usageOK, parseErr := r.parseUsage(c.NormalizedUsage)
				if parseErr != nil {
					return parseErr
				}
				_, already := usedConflicts[tupleKey(id)]
				prior, priorExists := r.finals[tupleKey(in.InputFinalizationID)]
				// Matching an existing normal input would have returned before
				// metering. Repeated conflict-only input IDs remain allowed.
				fastReturn := priorExists && prior.InputDigest == in.InputDigest
				okay := linked && !fastReturn && decision.FinalizationID == nil && decision.ConflictSourceID != nil && choice.CollisionKind != "none" && exists && hasSource && !already && usageOK && u == r.usage[tupleKey(in.ID)] && c.EventID == choice.MeterEventID && finite(c.CreatedAt) && source.TenantID == in.TenantID && source.RequestID == in.RequestID && requestSameOptional(source.AttemptID, in.AttemptID) && source.IncomingSourceID == in.ID && source.MeterChoiceID == choice.ID && source.SourceID == choice.ID && source.RootID == choice.RootID && source.StepNo == choice.StepNo && source.CreatedXID == choice.CreatedXID && requestSameOptional(decision.AfterLatestID, latest) && decision.AfterStatus == decision.BeforeStatus && requestOriginalPointer(c.PPRootID, c.PPRootStep, c.PPSourceKind, c.PPSourceID, c.PPSourceRole, c.PPCreatedXID, choice.RootID, choice.StepNo, "meter_choice", choice.ID, "conflict", choice.CreatedXID)
				if !okay {
					if decision.ConflictSourceID == nil {
						r.mark(requestConflictHit, "pp_request_decisions", decision.ID)
					} else {
						r.mark(requestConflictHit, "metering_event_conflicts", id)
					}
					valid = false
				} else {
					conflictCandidates = append(conflictCandidates, id)
				}
				usedConflicts.add(id)
			} else {
				r.mark(requestStickyHit, "pp_request_decisions", decision.ID)
				valid = false
			}
			id := decision.ID
			previous = &id
			lastXID = decision.CreatedXID
		}
		if !headPresent || head.Ordinal != strconv.Itoa(len(chain)) || !requestSameOptional(head.LastDecisionID, previous) || head.UpdatedXID != lastXID || !requestSameOptional(request.LatestFinalizationID, latest) || !requestSameOptional(request.PPLastDecisionID, previous) {
			r.mark(requestLatestHit, "request_records", origin.RequestID)
			valid = false
		}
		if valid {
			r.requestOK.add(origin.RequestID)
			r.candidates.FinalizationOrder.add(origin.RequestID)
			for _, id := range finalCandidates {
				r.candidates.OriginalDeliveryPredecessor.add(id)
			}
			for _, id := range conflictCandidates {
				r.candidates.ConflictIncoming.add(id)
			}
		}
	}
	for _, decision := range r.rows.PrefixRows.RequestDecisions {
		if err := r.w.tick(); err != nil {
			return err
		}
		if _, exists := r.origins[tupleKey(decision.RequestID)]; !exists {
			original, present := r.requests[tupleKey(decision.RequestID)]
			if !present || original.PPOriginRootID != nil {
				r.mark(requestStickyHit, "pp_request_decisions", decision.ID)
			}
		}
	}
	for _, f := range r.rows.OriginalRows.FinalizationRecords {
		if err := r.w.tick(); err != nil {
			return err
		}
		if f.PPRootID != nil {
			if _, used := usedFinals[tupleKey(f.ID)]; !used {
				r.mark(requestFinalHit, "finalization_records", f.ID)
			}
		}
	}
	for _, c := range r.rows.OriginalRows.MeteringEventConflicts {
		if err := r.w.tick(); err != nil {
			return err
		}
		if c.PPRootID != nil {
			if _, used := usedConflicts[tupleKey(c.ID)]; !used {
				r.mark(requestConflictHit, "metering_event_conflicts", c.ID)
			}
		}
	}
	// A valid-looking chain is insufficient if the same request has an
	// omitted original or an extra unconsumed source. Incomplete legacy history
	// withdraws candidates without inventing a mismatch or guessing its order.
	for _, in := range r.rows.PrefixRows.IncomingSources {
		if err := r.w.tick(); err != nil {
			return err
		}
		_, inputOK := r.incomingOK[tupleKey(in.ID)]
		choice, hasChoice := r.choiceFor[tupleKey(in.ID)]
		_, choiceOK := r.choiceOK[tupleKey(choice.ID)]
		if !inputOK || !hasChoice || !choiceOK {
			delete(r.requestOK, tupleKey(in.RequestID))
		}
	}
	for _, meter := range r.rows.OriginalRows.MeteringEvents {
		if err := r.w.tick(); err != nil {
			return err
		}
		if _, okay := r.meterOK[tupleKey(meter.ID)]; !okay {
			delete(r.requestOK, tupleKey(meter.RequestID))
		}
	}
	for _, f := range r.rows.OriginalRows.FinalizationRecords {
		if err := r.w.tick(); err != nil {
			return err
		}
		if _, consumed := usedFinals[tupleKey(f.ID)]; !consumed {
			delete(r.requestOK, tupleKey(f.RequestID))
		}
	}
	for _, source := range r.rows.PrefixRows.ConflictSources {
		if err := r.w.tick(); err != nil {
			return err
		}
		if _, consumed := usedConflicts[tupleKey(source.ConflictID)]; !consumed {
			delete(r.requestOK, tupleKey(source.RequestID))
		}
	}
	for _, c := range r.rows.OriginalRows.MeteringEventConflicts {
		if err := r.w.tick(); err != nil {
			return err
		}
		if _, consumed := usedConflicts[tupleKey(c.ID)]; !consumed && c.PPSourceID != nil {
			if choice, found := r.choices[tupleKey(*c.PPSourceID)]; found {
				delete(r.requestOK, tupleKey(choice.RequestID))
			}
		}
	}
	for key := range r.candidates.FinalizationOrder {
		if _, okay := r.requestOK[key]; !okay {
			delete(r.candidates.FinalizationOrder, key)
		}
	}
	for _, f := range r.rows.OriginalRows.FinalizationRecords {
		if err := r.w.tick(); err != nil {
			return err
		}
		if _, okay := r.requestOK[tupleKey(f.RequestID)]; !okay {
			delete(r.candidates.OriginalDeliveryPredecessor, tupleKey(f.ID))
		}
	}
	for _, source := range r.rows.PrefixRows.ConflictSources {
		if err := r.w.tick(); err != nil {
			return err
		}
		if _, okay := r.requestOK[tupleKey(source.RequestID)]; !okay {
			delete(r.candidates.ConflictIncoming, tupleKey(source.ConflictID))
		}
	}
	return nil
}

func (r *requestReplay) identityBindings() error {
	for _, identity := range r.rows.OriginalRows.ProcurementEvidenceIdentities {
		if err := r.w.tick(); err != nil {
			return err
		}
		key := tupleKey(identity.AttemptID, identity.EvidenceDigest)
		member, hasMember := r.members[key]
		if !hasMember {
			if identity.PPRootID != nil {
				r.mark(requestMeterHit, "procurement_evidence_identities", identity.AttemptID, identity.EvidenceDigest)
			}
			continue
		}
		creator, hasCreator := r.evidence[tupleKey(member.EvidenceChoiceID)]
		in, hasInput := r.incoming[tupleKey(creator.IncomingSourceID)]
		choice, hasChoice := r.choices[tupleKey(creator.MeterChoiceID)]
		_, inputOK := r.incomingOK[tupleKey(in.ID)]
		_, choiceOK := r.choiceOK[tupleKey(choice.ID)]
		_, requestOK := r.requestOK[tupleKey(in.RequestID)]
		_, inputGap := r.inputGap[tupleKey(in.ID)]
		_, choiceGap := r.choiceGap[tupleKey(choice.ID)]
		attempt, hasAttempt := r.attempts[tupleKey(identity.AttemptID)]
		birth, hasBirth := r.attemptBirth[tupleKey(identity.AttemptID)]
		digest := requestEvidenceDigest(r.usage[tupleKey(in.ID)], in.TransportEndReason)
		valid := hasCreator && hasInput && hasChoice && (inputOK || inputGap) && (choiceOK || choiceGap) && hasAttempt && hasBirth && birth.ManagementMode == "managed" && in.AttemptID != nil && *in.AttemptID == identity.AttemptID && attempt.TenantID == in.TenantID && attempt.RequestID == in.RequestID && creator.TenantID == in.TenantID && creator.RequestID == in.RequestID && creator.AttemptID == identity.AttemptID && creator.RootID == in.RootID && choice.IncomingSourceID == in.ID && creator.Disposition == "new_member" && creator.EvidenceDigest == digest && creator.IdentityDigest == digest && identity.EvidenceDigest == digest && creator.IdentityAttemptID == identity.AttemptID && creator.IdentityEventID == identity.EventID && choice.MeterEventID == identity.EventID && creator.ForcedConflict == (choice.CollisionKind != "none") && member.IdentityEventID == identity.EventID && member.SourceID == creator.ID && member.RootID == creator.RootID && member.StepNo == creator.StepNo && member.CreatedXID == creator.CreatedXID && requestOriginalPointer(identity.PPRootID, identity.PPRootStep, identity.PPSourceKind, identity.PPSourceID, identity.PPSourceRole, identity.PPCreatedXID, creator.RootID, creator.StepNo, "evidence_choice", creator.ID, "evidence_identity", creator.CreatedXID)
		if !valid {
			r.mark(requestMeterHit, "procurement_evidence_identities", identity.AttemptID, identity.EvidenceDigest)
			continue
		}
		if !inputOK || !choiceOK || !requestOK {
			continue
		}
		r.candidates.ConflictReason.add(identity.AttemptID, identity.EvidenceDigest)
		r.candidates.IncomingBinding.add(identity.AttemptID, identity.EvidenceDigest)
	}
	// Reuse can have a different current meter; the immutable first identity's
	// event remains authoritative. Every observed choice still binds its own raw
	// incoming reason and target; one good creator cannot hide a forged reuse.
	for _, e := range r.rows.PrefixRows.EvidenceChoices {
		if err := r.w.tick(); err != nil {
			return err
		}
		in, exists := r.incoming[tupleKey(e.IncomingSourceID)]
		choice, hasChoice := r.choices[tupleKey(e.MeterChoiceID)]
		key := tupleKey(e.AttemptID, e.EvidenceDigest)
		identity, hasIdentity := r.identities[key]
		_, inputOK := r.incomingOK[tupleKey(in.ID)]
		_, choiceOK := r.choiceOK[tupleKey(choice.ID)]
		_, inputGap := r.inputGap[tupleKey(in.ID)]
		_, choiceGap := r.choiceGap[tupleKey(choice.ID)]
		okay := exists && hasChoice && hasIdentity && (inputOK || inputGap) && (choiceOK || choiceGap) && in.AttemptID != nil && *in.AttemptID == e.AttemptID && e.TenantID == in.TenantID && e.RequestID == in.RequestID && e.RootID == in.RootID && choice.IncomingSourceID == in.ID && e.EvidenceDigest == requestEvidenceDigest(r.usage[tupleKey(in.ID)], in.TransportEndReason) && e.IdentityAttemptID == identity.AttemptID && e.IdentityDigest == identity.EvidenceDigest && e.IdentityEventID == identity.EventID && e.ForcedConflict == (choice.CollisionKind != "none") && requestOneOf(e.Disposition, "new_member", "existing_member")
		if !okay {
			r.mark(requestMeterHit, "pp_evidence_choices", e.ID)
			delete(r.candidates.ConflictReason, key)
			delete(r.candidates.IncomingBinding, key)
		}
	}
	return nil
}
