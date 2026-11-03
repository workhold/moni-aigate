package procurementprefix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func rqString(v string) *string { return &v }
func rqTime() Time              { return Time{Time: time.Date(2026, 11, 3, 4, 5, 6, 0, time.UTC), Valid: true} }

// Fixture input is encoded through a literal field map/order, independently of
// requestInputDigest and of all SQL/writer identity helpers.
func rqDigest(in IncomingSourceRow) string {
	quote := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	usage := fmt.Sprintf(`{"status":%s,"input_tokens":%s,"output_tokens":%s,"total_tokens":%s,"cached_tokens":%s,"reasoning_tokens":%s}`, quote(in.UsageStatus), in.InputTokens, in.OutputTokens, in.TotalTokens, in.CachedTokens, in.ReasoningTokens)
	values := []string{in.InputFinalizationID, in.TenantID, in.RequestID, requestOptional(in.AttemptID), in.Source, in.SourceEventID}
	names := []string{"ID", "TenantID", "RequestID", "AttemptID", "Source", "SourceEventID"}
	var fields []string
	for i, v := range values {
		fields = append(fields, quote(names[i])+":"+quote(v))
	}
	fields = append(fields, `"Usage":`+usage, `"ProtocolOutcome":`+quote(in.ProtocolOutcome), `"TransportEndReason":`+quote(in.TransportEndReason), `"DeliveryStage":`+quote(in.RawDeliveryStage), `"FinishReason":`+quote(in.FinishReason), `"ContentWritten":`+strconv.FormatBool(in.RawContentWritten), `"ObservedAt":"0001-01-01T00:00:00Z"`)
	sum := sha256.Sum256([]byte("{" + strings.Join(fields, ",") + "}"))
	return hex.EncodeToString(sum[:])
}

func rqEvidenceDigest(in IncomingSourceRow) string {
	usage := fmt.Sprintf(`{"status":%q,"input_tokens":%s,"output_tokens":%s,"total_tokens":%s,"cached_tokens":%s,"reasoning_tokens":%s}`, in.UsageStatus, in.InputTokens, in.OutputTokens, in.TotalTokens, in.CachedTokens, in.ReasoningTokens)
	reason, _ := json.Marshal(in.TransportEndReason)
	sum := sha256.Sum256([]byte(`{"Usage":` + usage + `,"Reason":` + string(reason) + `}`))
	return hex.EncodeToString(sum[:])
}

type rqFixture struct {
	rows Rows
	next int
}

func (f *rqFixture) request(id string) {
	for _, r := range f.rows.OriginalRows.RequestRecords {
		if r.ID == id {
			return
		}
	}
	f.rows.OriginalRows.RequestRecords = append(f.rows.OriginalRows.RequestRecords, RequestRecordRow{ID: id, TenantID: "tenant", PolicyVersion: "policy", Status: "accepted", BillingStatus: "reserved"})
	f.rows.PrefixRows.RequestOrigins = append(f.rows.PrefixRows.RequestOrigins, RequestOriginRow{RequestID: id, TenantID: "tenant", CreatedXID: "99", InitialStatus: "accepted", InitialBillingStatus: "reserved", PolicyVersion: "policy"})
	f.rows.PrefixRows.RequestHeads = append(f.rows.PrefixRows.RequestHeads, RequestHeadRow{RequestID: id, Ordinal: "0", UpdatedXID: "99"})
}

func (f *rqFixture) attempt(request, id string) {
	if id == "" {
		return
	}
	for _, a := range f.rows.OriginalRows.UpstreamAttempts {
		if a.ID == id {
			return
		}
	}
	f.rows.OriginalRows.UpstreamAttempts = append(f.rows.OriginalRows.UpstreamAttempts, UpstreamAttemptRow{ID: id, TenantID: "tenant", RequestID: request})
	f.rows.PrefixRows.AttemptOrigins = append(f.rows.PrefixRows.AttemptOrigins, AttemptOriginRow{AttemptID: id, TenantID: "tenant", RequestID: request, ManagementMode: "unmanaged"})
}

func (f *rqFixture) node(attempt, root, step, kind, source, xid string) *string {
	if attempt == "" {
		return nil
	}
	ordinal := 1
	var previous *string
	for _, n := range f.rows.PrefixRows.AttemptNodes {
		if n.AttemptID == attempt {
			ordinal++
			previous = rqString(n.ID)
		}
	}
	id := fmt.Sprintf("node-%s-%d", attempt, ordinal)
	f.rows.PrefixRows.AttemptNodes = append(f.rows.PrefixRows.AttemptNodes, AttemptNodeRow{ID: id, AttemptID: attempt, Ordinal: strconv.Itoa(ordinal), PreviousOrdinal: strconv.Itoa(ordinal - 1), PreviousNodeID: previous, RootID: root, StepNo: step, StepKind: kind, SourceID: source, CreatedXID: xid})
	return rqString(id)
}

// normalized stage/content and branch are hand-selected by each scenario; the
// fixture producer never calls the replay's sticky or collision implementation.
func (f *rqFixture) add(request, attempt, rawStage string, rawContent bool, normalized string, content bool, branch, reuse, collision string, input int64) int {
	f.request(request)
	f.attempt(request, attempt)
	f.next++
	n := f.next
	root := fmt.Sprintf("root-%d", n)
	xid := strconv.Itoa(100 + n)
	inID, choiceID, decisionID := fmt.Sprintf("in-%d", n), fmt.Sprintf("choice-%d", n), fmt.Sprintf("decision-%d", n)
	var attemptID *string
	if attempt != "" {
		attemptID = rqString(attempt)
	}
	in := IncomingSourceRow{ID: inID, RootID: root, StepNo: "1", StepKind: "incoming", CreatedXID: xid, TenantID: "tenant", RequestID: request, AttemptID: attemptID, InputFinalizationID: fmt.Sprintf("00000000-0000-0000-0000-%012d", n), Source: "provider", SourceEventID: fmt.Sprintf("event-%d", n), UsageStatus: "confirmed_final", InputTokens: strconv.FormatInt(input, 10), OutputTokens: "0", TotalTokens: strconv.FormatInt(input, 10), CachedTokens: "0", ReasoningTokens: "0", ProtocolOutcome: "completed", TransportEndReason: "done", RawDeliveryStage: rawStage, RawContentWritten: rawContent, EffectiveObservedAt: rqTime()}
	meterID := fmt.Sprintf("meter-%d", n)
	if reuse != "" {
		meterID = reuse
		for _, m := range f.rows.OriginalRows.MeteringEvents {
			if m.ID == reuse {
				in.Source = m.Source
				in.SourceEventID = m.SourceEventID
			}
		}
	}
	in.InputDigest = rqDigest(in)
	f.rows.PrefixRows.IncomingSources = append(f.rows.PrefixRows.IncomingSources, in)
	beforeCut := f.node(attempt, root, "1", "incoming", inID, xid)
	afterCut := f.node(attempt, root, "2", "meter_choice", choiceID, xid)
	choice := MeterChoiceRow{ID: choiceID, RootID: root, StepNo: "2", StepKind: "meter_choice", CreatedXID: xid, TenantID: "tenant", RequestID: request, AttemptID: attemptID, IncomingSourceID: inID, MeterEventID: meterID, MeterOriginID: meterID, Disposition: "created", AttemptCutBeforeID: beforeCut, AttemptCutAfterMeterID: afterCut, CollisionKind: collision}
	if reuse != "" {
		choice.Disposition = "reused"
	}
	if collision == "other_confirmed_usage" {
		for _, m := range f.rows.OriginalRows.MeteringEvents {
			if requestSameOptional(m.AttemptID, attemptID) {
				choice.CompetingMeterID = rqString(m.ID)
				break
			}
		}
	}
	f.rows.PrefixRows.MeterChoices = append(f.rows.PrefixRows.MeterChoices, choice)
	usage := fmt.Sprintf(`{"status":"confirmed_final","input_tokens":%d,"output_tokens":0,"total_tokens":%d,"cached_tokens":0,"reasoning_tokens":0}`, input, input)
	if reuse == "" {
		f.rows.OriginalRows.MeteringEvents = append(f.rows.OriginalRows.MeteringEvents, MeteringEventRow{ID: meterID, TenantID: "tenant", RequestID: request, AttemptID: attemptID, Source: in.Source, SourceEventID: in.SourceEventID, SchemaVersion: "1", NormalizedUsage: usage, HappenedAt: rqTime(), ReceivedAt: rqTime(), PPRootID: rqString(root), PPRootStep: rqString("2"), PPSourceKind: rqString("meter_choice"), PPSourceID: rqString(choiceID), PPSourceRole: rqString("meter_origin"), PPCreatedXID: rqString(xid)})
		f.rows.PrefixRows.MeterOrigins = append(f.rows.PrefixRows.MeterOrigins, MeterOriginRow{MeterEventID: meterID, IncomingSourceID: inID, RootID: root, StepNo: "2", StepKind: "meter_choice", SourceID: choiceID, CreatedXID: xid})
	}
	var latest, previous *string
	ordinal := 1
	for _, d := range f.rows.PrefixRows.RequestDecisions {
		if d.RequestID == request {
			ordinal++
			latest = d.AfterLatestID
			previous = rqString(d.ID)
		}
	}
	d := RequestDecisionRow{ID: decisionID, RootID: root, StepNo: "3", StepKind: "request_decision", CreatedXID: xid, TenantID: "tenant", RequestID: request, Ordinal: strconv.Itoa(ordinal), PreviousOrdinal: strconv.Itoa(ordinal - 1), PreviousDecisionID: previous, IncomingSourceID: inID, MeterChoiceID: choiceID, BeforeLatestID: latest, AfterLatestID: latest, BeforeStatus: "accepted", AfterStatus: "accepted", NormalizedDeliveryStage: normalized, NormalizedContentWritten: content, Branch: branch}
	if branch == "normal" {
		d.FinalizationID = rqString(in.InputFinalizationID)
		d.AfterLatestID = rqString(in.InputFinalizationID)
		d.AfterStatus = "succeeded"
		f.rows.OriginalRows.FinalizationRecords = append(f.rows.OriginalRows.FinalizationRecords, FinalizationRecordRow{ID: in.InputFinalizationID, TenantID: "tenant", RequestID: request, EventID: meterID, InputDigest: in.InputDigest, ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: normalized, ContentWritten: content, PolicyVersion: "policy", CreatedAt: rqTime(), PPRootID: rqString(root), PPRootStep: rqString("3"), PPSourceKind: rqString("request_decision"), PPSourceID: rqString(decisionID), PPSourceRole: rqString("finalization"), PPCreatedXID: rqString(xid)})
	} else {
		id := fmt.Sprintf("conflict-%d", n)
		d.ConflictSourceID = rqString(id)
		f.rows.OriginalRows.MeteringEventConflicts = append(f.rows.OriginalRows.MeteringEventConflicts, MeteringEventConflictRow{ID: id, EventID: meterID, NormalizedUsage: usage, CreatedAt: rqTime(), PPRootID: rqString(root), PPRootStep: rqString("2"), PPSourceKind: rqString("meter_choice"), PPSourceID: rqString(choiceID), PPSourceRole: rqString("conflict"), PPCreatedXID: rqString(xid)})
		f.rows.PrefixRows.ConflictSources = append(f.rows.PrefixRows.ConflictSources, ConflictSourceRow{ConflictID: id, RootID: root, StepNo: "2", StepKind: "meter_choice", SourceID: choiceID, CreatedXID: xid, TenantID: "tenant", RequestID: request, AttemptID: attemptID, IncomingSourceID: inID, MeterChoiceID: choiceID})
	}
	f.rows.PrefixRows.RequestDecisions = append(f.rows.PrefixRows.RequestDecisions, d)
	f.node(attempt, root, "3", "request_decision", decisionID, xid)
	for i := range f.rows.OriginalRows.RequestRecords {
		if f.rows.OriginalRows.RequestRecords[i].ID == request {
			f.rows.OriginalRows.RequestRecords[i].LatestFinalizationID = d.AfterLatestID
			f.rows.OriginalRows.RequestRecords[i].PPLastDecisionID = rqString(d.ID)
		}
	}
	for i := range f.rows.PrefixRows.RequestHeads {
		if f.rows.PrefixRows.RequestHeads[i].RequestID == request {
			f.rows.PrefixRows.RequestHeads[i] = RequestHeadRow{RequestID: request, Ordinal: d.Ordinal, LastDecisionID: rqString(d.ID), UpdatedXID: xid}
		}
	}
	return n - 1
}

func (f *rqFixture) evidence(index int, existing bool) {
	in := f.rows.PrefixRows.IncomingSources[index]
	choice := f.rows.PrefixRows.MeterChoices[index]
	attempt := *in.AttemptID
	for i := range f.rows.PrefixRows.AttemptOrigins {
		if f.rows.PrefixRows.AttemptOrigins[i].AttemptID == attempt {
			f.rows.PrefixRows.AttemptOrigins[i].ManagementMode = "managed"
		}
	}
	digest := rqEvidenceDigest(in)
	id := fmt.Sprintf("evidence-%d", index)
	e := EvidenceChoiceRow{ID: id, RootID: in.RootID, StepNo: "4", StepKind: "evidence_choice", CreatedXID: in.CreatedXID, TenantID: in.TenantID, RequestID: in.RequestID, AttemptID: attempt, IncomingSourceID: in.ID, MeterChoiceID: choice.ID, EvidenceDigest: digest, Disposition: "new_member", IdentityAttemptID: attempt, IdentityDigest: digest, IdentityEventID: choice.MeterEventID, ForcedConflict: choice.CollisionKind != "none"}
	if existing {
		e.Disposition = "existing_member"
		for _, i := range f.rows.OriginalRows.ProcurementEvidenceIdentities {
			if i.AttemptID == attempt && i.EvidenceDigest == digest {
				e.IdentityEventID = i.EventID
			}
		}
	} else {
		f.rows.OriginalRows.ProcurementEvidenceIdentities = append(f.rows.OriginalRows.ProcurementEvidenceIdentities, ProcurementEvidenceIdentityRow{AttemptID: attempt, EvidenceDigest: digest, EventID: choice.MeterEventID, CreatedAt: rqTime(), PPRootID: rqString(in.RootID), PPRootStep: rqString("4"), PPSourceKind: rqString("evidence_choice"), PPSourceID: rqString(id), PPSourceRole: rqString("evidence_identity"), PPCreatedXID: rqString(in.CreatedXID)})
		f.rows.PrefixRows.EvidenceMembers = append(f.rows.PrefixRows.EvidenceMembers, EvidenceMemberRow{ID: "member-" + id, AttemptID: attempt, IdentityDigest: digest, IdentityEventID: choice.MeterEventID, EvidenceChoiceID: id, RootID: in.RootID, StepNo: "4", StepKind: "evidence_choice", SourceID: id, CreatedXID: in.CreatedXID})
	}
	f.rows.PrefixRows.EvidenceChoices = append(f.rows.PrefixRows.EvidenceChoices, e)
	f.node(attempt, in.RootID, "4", "evidence_choice", id, in.CreatedXID)
}

func rqRun(t *testing.T, rows Rows) requestReplayResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w, _, err := newReplayWork(ctx, rows)
	if err != nil {
		t.Fatal(err)
	}
	result, err := checkRequestIncoming(w, rows)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || len(result.Unimplemented) != 0 {
		t.Fatal("domain implementation incomplete")
	}
	return result
}

func rqClean(t *testing.T, rows Rows) requestReplayResult {
	t.Helper()
	result := rqRun(t, rows)
	if result.Stats != (RequestIncoming{}) {
		t.Fatalf("unexpected request mismatches: %+v", result.Stats)
	}
	return result
}

func rqClone(t *testing.T, rows Rows) Rows {
	t.Helper()
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var copy Rows
	if err := json.Unmarshal(data, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

func TestRequestReplayCrossAttemptAndNoAttemptSticky(t *testing.T) {
	f := rqFixture{}
	f.add("r", "a1", "committed", false, "committed", false, "normal", "", "none", 10)
	f.add("r", "a2", "none", false, "committed", false, "normal", "", "none", 10)
	f.add("r", "", "none", true, "content", true, "normal", "", "none", 10)
	f.add("r", "a2", "unknown", false, "content", true, "normal", "", "none", 10)
	// Current customer status can be changed by another legitimate money path.
	f.rows.OriginalRows.RequestRecords[0].Status = "cancelled"
	result := rqClean(t, f.rows)
	if len(result.Candidates.FinalizationOrder) != 1 || len(result.Candidates.OriginalDeliveryPredecessor) != 4 {
		t.Fatal("full request chain did not yield exact object candidates")
	}
	shuffled := rqClone(t, f.rows)
	for _, parent := range []reflect.Value{reflect.ValueOf(&shuffled.OriginalRows).Elem(), reflect.ValueOf(&shuffled.PrefixRows).Elem()} {
		for i := 0; i < parent.NumField(); i++ {
			field := parent.Field(i)
			for a, b := 0, field.Len()-1; a < b; a, b = a+1, b-1 {
				saved := reflect.New(field.Index(a).Type()).Elem()
				saved.Set(field.Index(a))
				field.Index(a).Set(field.Index(b))
				field.Index(b).Set(saved)
			}
		}
	}
	if got := rqClean(t, shuffled); !reflect.DeepEqual(result, got) {
		t.Fatal("input permutation changed replay")
	}
}

func TestRequestReplayCompleteMeterCutsAndCollisionPriority(t *testing.T) {
	f := rqFixture{}
	f.add("r", "a", "none", false, "none", false, "normal", "", "none", 10)
	f.add("r", "a", "none", false, "none", false, "conflict", "", "other_confirmed_usage", 20)
	// The conflict-born meter is a real member for the next cut, despite never
	// creating a normal finalization. Reuse of meter1 now sees that member.
	f.add("r", "a", "none", false, "none", false, "conflict", "meter-1", "other_confirmed_usage", 10)
	f.rows.PrefixRows.MeterChoices[2].CompetingMeterID = rqString("meter-2")
	// This incoming targets another attempt and request, but reuses a source
	// key in the same tenant. It must retain its incoming ownership.
	f.add("other:r", "b", "none", false, "none", false, "conflict", "meter-1", "source_payload_mismatch", 30)
	result := rqClean(t, f.rows)
	if len(result.Candidates.ConflictIncoming) != 3 || len(result.Candidates.FinalizationOrder) != 2 {
		t.Fatal("wrong conflict/request candidate sets")
	}
	for _, mutate := range []func(*Rows){
		func(r *Rows) {
			r.PrefixRows.MeterChoices[0].CollisionKind = "other_confirmed_usage"
			r.PrefixRows.MeterChoices[0].CompetingMeterID = rqString("meter-2")
		},
		func(r *Rows) { r.PrefixRows.MeterChoices[2].CompetingMeterID = rqString("meter-1") },
		func(r *Rows) {
			r.PrefixRows.MeterChoices[3].CollisionKind = "other_confirmed_usage"
			r.PrefixRows.MeterChoices[3].CompetingMeterID = rqString("meter-2")
		},
		func(r *Rows) { r.PrefixRows.MeterChoices[1].AttemptCutBeforeID = nil },
	} {
		changed := rqClone(t, f.rows)
		mutate(&changed)
		if got := rqRun(t, changed); got.Stats.MeterChoiceRows == 0 {
			t.Fatal("invalid cut/collision/witness accepted")
		}
	}
}

func TestRequestReplayIdentityUsesIncomingReasonAndFirstEvent(t *testing.T) {
	f := rqFixture{}
	i := f.add("r", "a", "none", false, "none", false, "normal", "", "none", 10)
	f.evidence(i, false)
	i = f.add("r", "a", "none", false, "none", false, "normal", "", "none", 10)
	f.evidence(i, true)
	result := rqClean(t, f.rows)
	identity := f.rows.OriginalRows.ProcurementEvidenceIdentities[0]
	key := tupleKey(identity.AttemptID, identity.EvidenceDigest)
	if _, ok := result.Candidates.ConflictReason[key]; !ok {
		t.Fatal("reason candidate missing")
	}
	if _, ok := result.Candidates.IncomingBinding[key]; !ok {
		t.Fatal("binding candidate missing")
	}
	changed := rqClone(t, f.rows)
	changed.PrefixRows.EvidenceChoices[1].IdentityEventID = "meter-2"
	got := rqRun(t, changed)
	if got.Stats.MeterIdentityRows != 1 || len(got.Candidates.ConflictReason) != 0 || len(got.Candidates.IncomingBinding) != 0 {
		t.Fatal("reuse replaced first identity event")
	}
	// A conflict identity binds the actual incoming target and usage rather
	// than the reused meter's original request/attempt or its usage.
	i = f.add("other", "b", "none", false, "none", false, "conflict", "meter-1", "source_payload_mismatch", 33)
	f.evidence(i, false)
	if result := rqClean(t, f.rows); len(result.Candidates.ConflictReason) != 2 || len(result.Candidates.IncomingBinding) != 2 {
		t.Fatal("cross-request identity not independently explained")
	}
}

func TestRequestReplayMutationMatrixAndLegacy(t *testing.T) {
	f := rqFixture{}
	f.add("r", "a", "content", false, "content", true, "normal", "", "none", 10)
	f.add("r", "", "none", false, "content", true, "normal", "", "none", 10)
	cases := []struct {
		name   string
		mutate func(*Rows)
		count  func(RequestIncoming) uint64
	}{
		{"raw_digest", func(r *Rows) { r.PrefixRows.IncomingSources[1].InputDigest = strings.Repeat("0", 64) }, func(s RequestIncoming) uint64 { return s.InputDigestRows }},
		{"usage_duplicate_member", func(r *Rows) {
			r.OriginalRows.MeteringEvents[0].NormalizedUsage = `{"status":"confirmed_final","status":"confirmed_final","input_tokens":10,"output_tokens":0,"total_tokens":10,"cached_tokens":0,"reasoning_tokens":0}`
		}, func(s RequestIncoming) uint64 { return s.MeterIdentityRows }},
		{"before_latest", func(r *Rows) { r.PrefixRows.RequestDecisions[1].BeforeLatestID = nil }, func(s RequestIncoming) uint64 { return s.RequestStickyRows }},
		{"normalized_content", func(r *Rows) { r.PrefixRows.RequestDecisions[1].NormalizedContentWritten = false }, func(s RequestIncoming) uint64 { return s.RequestStickyRows }},
		{"finalization_reason", func(r *Rows) { r.OriginalRows.FinalizationRecords[1].TransportEndReason = "not_sent" }, func(s RequestIncoming) uint64 { return s.FinalizationRows }},
		{"latest", func(r *Rows) {
			r.OriginalRows.RequestRecords[0].LatestFinalizationID = rqString(r.OriginalRows.FinalizationRecords[0].ID)
		}, func(s RequestIncoming) uint64 { return s.LatestRows }},
		{"ordinal_gap", func(r *Rows) { r.PrefixRows.RequestDecisions[1].Ordinal = "3" }, func(s RequestIncoming) uint64 { return s.RequestStickyRows }},
		{"meter_origin", func(r *Rows) { r.PrefixRows.MeterOrigins[0].IncomingSourceID = "in-2" }, func(s RequestIncoming) uint64 { return s.MeterIdentityRows }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := rqClone(t, f.rows)
			tc.mutate(&changed)
			got := rqRun(t, changed)
			if tc.count(got.Stats) == 0 || len(got.Candidates.FinalizationOrder) != 0 {
				t.Fatalf("mutation accepted: %+v", got)
			}
		})
	}
	legacy := Rows{OriginalRows: OriginalRows{MeteringEvents: []MeteringEventRow{{ID: "legacy", TenantID: "tenant", RequestID: "r", NormalizedUsage: `{"status":"missing","input_tokens":0,"output_tokens":0,"total_tokens":0,"cached_tokens":0,"reasoning_tokens":0}`}}}}
	if got := rqClean(t, legacy); len(got.Candidates.OriginalDeliveryPredecessor) != 0 {
		t.Fatal("legacy history falsely proved")
	}
}

func TestRequestReplayInputABIAndUsage(t *testing.T) {
	f := rqFixture{}
	f.add("r", "", "none", true, "content", true, "normal", "", "none", 10)
	in := f.rows.PrefixRows.IncomingSources[0]
	// SHA-256 of the explicitly written v1 preimage, independently calculated
	// with Python hashlib; raw none/true is preserved before normalization.
	if in.InputDigest != "29f5b2f986448409cd25b9b24e28ad532b552aa721cd5ee142d271d27e1556f0" {
		t.Fatal("fixed input ABI vector changed")
	}
	in.Source = "<&>\u2028"
	in.SourceEventID = "汉字\\u003c"
	in.FinishReason = "\n\""
	u, ok := requestIncomingUsage(in)
	if !ok || requestInputDigest(in, u) != rqDigest(in) {
		t.Fatal("raw Go JSON ABI differs")
	}
	digest := requestInputDigest(in, u)
	in.ObservedAtInput = rqTime()
	in.EffectiveObservedAt = rqTime()
	if requestInputDigest(in, u) != digest {
		t.Fatal("ObservedAt entered identity")
	}
	in.RawContentWritten = false
	if requestInputDigest(in, u) == digest {
		t.Fatal("raw content was normalized before digest")
	}
	for _, raw := range []string{`null`, `{"status":"missing"}`, `{"status":"missing","input_tokens":"0","output_tokens":0,"total_tokens":0,"cached_tokens":0,"reasoning_tokens":0}`, `{"status":"confirmed_final","input_tokens":9223372036854775807,"output_tokens":1,"total_tokens":0,"cached_tokens":0,"reasoning_tokens":0}`, `{"status":"missing","input_tokens":0e0,"output_tokens":0,"total_tokens":0,"cached_tokens":0,"reasoning_tokens":0}`} {
		if _, ok := requestParseUsage(raw); ok {
			t.Fatal("invalid original usage accepted")
		}
	}
}

func TestRequestReplayCanceledAndEmpty(t *testing.T) {
	if got := rqClean(t, Rows{}); len(got.Candidates.FinalizationOrder) != 0 {
		t.Fatal("empty input produced candidates")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &replayWork{b: budget{ctx: ctx}}
	if got, err := checkRequestIncoming(w, Rows{}); !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(got, requestReplayResult{}) {
		t.Fatal("canceled replay returned data")
	}
}

func TestRequestReplayNoAttemptCannotReuseFutureMeter(t *testing.T) {
	f := rqFixture{}
	f.add("r", "", "none", false, "none", false, "normal", "", "none", 10)
	f.add("r", "", "none", false, "none", false, "normal", "meter-1", "none", 10)
	rqClean(t, f.rows)
	// Put reuse before the source's creator in an otherwise coherent request
	// chain. There are no attempt nodes to catch this fabricated ordering.
	d0, d1 := &f.rows.PrefixRows.RequestDecisions[0], &f.rows.PrefixRows.RequestDecisions[1]
	d1.Ordinal = "1"
	d1.PreviousOrdinal = "0"
	d1.PreviousDecisionID = nil
	d1.BeforeLatestID = nil
	d0.Ordinal = "2"
	d0.PreviousOrdinal = "1"
	d0.PreviousDecisionID = rqString(d1.ID)
	d0.BeforeLatestID = d1.FinalizationID
	f.rows.PrefixRows.RequestHeads[0] = RequestHeadRow{RequestID: "r", Ordinal: "2", LastDecisionID: rqString(d0.ID), UpdatedXID: d0.CreatedXID}
	f.rows.OriginalRows.RequestRecords[0].LatestFinalizationID = d0.FinalizationID
	f.rows.OriginalRows.RequestRecords[0].PPLastDecisionID = rqString(d0.ID)
	got := rqRun(t, f.rows)
	if got.Stats.MeterChoiceRows != 1 || len(got.Candidates.FinalizationOrder) != 0 {
		t.Fatal("future no-attempt meter was accepted")
	}
}

func TestRequestReplayNormalFastReturnDoesNotCreateConflict(t *testing.T) {
	f := rqFixture{}
	f.add("r", "a", "none", false, "none", false, "normal", "", "none", 10)
	f.add("r", "a", "none", false, "none", false, "conflict", "", "other_confirmed_usage", 20)
	f.add("r", "a", "none", false, "none", false, "conflict", "meter-1", "other_confirmed_usage", 10)
	f.rows.PrefixRows.MeterChoices[2].CompetingMeterID = rqString("meter-2")
	rqClean(t, f.rows)
	f.rows.PrefixRows.IncomingSources[2].InputFinalizationID = f.rows.PrefixRows.IncomingSources[0].InputFinalizationID
	f.rows.PrefixRows.IncomingSources[2].InputDigest = rqDigest(f.rows.PrefixRows.IncomingSources[2])
	got := rqRun(t, f.rows)
	if got.Stats.ConflictRows != 1 || len(got.Candidates.FinalizationOrder) != 0 {
		t.Fatal("normal idempotent replay fabricated a conflict")
	}
}

func TestRequestReplayOmittedOriginalsCannotBeBalancedByGoodChain(t *testing.T) {
	f := rqFixture{}
	f.add("r", "", "none", false, "none", false, "normal", "", "none", 10)
	for _, mutate := range []func(*Rows){
		func(r *Rows) {
			r.OriginalRows.FinalizationRecords = append(r.OriginalRows.FinalizationRecords, FinalizationRecordRow{ID: "uncovered-legacy", TenantID: "tenant", RequestID: "r"})
		},
		func(r *Rows) {
			in := r.PrefixRows.IncomingSources[0]
			in.ID = "unconsumed-input"
			r.PrefixRows.IncomingSources = append(r.PrefixRows.IncomingSources, in)
		},
		func(r *Rows) {
			r.OriginalRows.MeteringEvents = append(r.OriginalRows.MeteringEvents, MeteringEventRow{ID: "legacy-meter", TenantID: "tenant", RequestID: "r"})
		},
	} {
		rows := rqClone(t, f.rows)
		mutate(&rows)
		got := rqRun(t, rows)
		if len(got.Candidates.FinalizationOrder) != 0 || len(got.Candidates.OriginalDeliveryPredecessor) != 0 {
			t.Fatal("incomplete request originals received candidates")
		}
	}
}

func TestRequestReplayLegacyReusedMeterRemainsGap(t *testing.T) {
	f := rqFixture{}
	f.add("old", "old-a", "none", false, "none", false, "normal", "", "none", 10)
	f.add("new", "new-a", "none", false, "none", false, "conflict", "meter-1", "source_payload_mismatch", 30)
	f.rows.PrefixRows.IncomingSources = f.rows.PrefixRows.IncomingSources[1:]
	f.rows.PrefixRows.MeterChoices = f.rows.PrefixRows.MeterChoices[1:]
	f.rows.PrefixRows.RequestDecisions = f.rows.PrefixRows.RequestDecisions[1:]
	f.rows.PrefixRows.RequestOrigins = f.rows.PrefixRows.RequestOrigins[1:]
	f.rows.PrefixRows.RequestHeads = f.rows.PrefixRows.RequestHeads[1:]
	f.rows.PrefixRows.AttemptOrigins = f.rows.PrefixRows.AttemptOrigins[1:]
	f.rows.PrefixRows.AttemptNodes = f.rows.PrefixRows.AttemptNodes[3:]
	f.rows.PrefixRows.MeterOrigins = nil
	f.rows.OriginalRows.FinalizationRecords = nil
	m := &f.rows.OriginalRows.MeteringEvents[0]
	m.PPRootID, m.PPRootStep, m.PPSourceKind, m.PPSourceID, m.PPSourceRole, m.PPCreatedXID = nil, nil, nil, nil, nil, nil
	f.rows.OriginalRows.RequestRecords[0].LatestFinalizationID = nil
	f.rows.OriginalRows.RequestRecords[0].PPLastDecisionID = nil
	got := rqClean(t, f.rows)
	if len(got.Candidates.ConflictIncoming) != 0 || len(got.Candidates.FinalizationOrder) != 0 {
		t.Fatal("legacy source was reconstructed")
	}
	// An unknown origin does not excuse a directly contradictory raw conflict.
	f.rows.OriginalRows.MeteringEventConflicts[0].NormalizedUsage = `{"status":"missing","input_tokens":0,"output_tokens":0,"total_tokens":0,"cached_tokens":0,"reasoning_tokens":0}`
	if got := rqRun(t, f.rows); got.Stats.ConflictRows != 1 {
		t.Fatal("legacy gap hid a conflict contradiction")
	}
}

func TestRequestReplaySharedBudgetIncludesSortAndParse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &replayWork{b: budget{ctx: ctx}}
	values := make([]int, 100000)
	for i := range values {
		values[i] = len(values) - i
	}
	compares := 0
	err := replaySort(w, values, func(a, b int) bool {
		compares++
		if compares == 32 {
			cancel()
		}
		return a < b
	})
	if !errors.Is(err, ErrCanceled) || compares >= 1000 {
		t.Fatal("sorting escaped shared cancellation budget")
	}
	r := requestReplay{w: w}
	if u, valid, err := r.parseUsage(`{"status":"missing","input_tokens":0,"output_tokens":0,"total_tokens":0,"cached_tokens":0,"reasoning_tokens":0}`); !errors.Is(err, ErrCanceled) || valid || u != (requestUsage{}) {
		t.Fatal("canceled parse returned facts")
	}
}

func TestRequestReplayUsageAndOutcomeBranches(t *testing.T) {
	for _, status := range []string{"confirmed_final", "provisional", "estimated", "missing", "conflicting"} {
		t.Run(status, func(t *testing.T) {
			f := rqFixture{}
			f.add("r", "", "none", false, "none", false, "normal", "", "none", 10)
			in := &f.rows.PrefixRows.IncomingSources[0]
			in.UsageStatus = status
			in.InputDigest = rqDigest(*in)
			f.rows.OriginalRows.FinalizationRecords[0].InputDigest = in.InputDigest
			f.rows.OriginalRows.MeteringEvents[0].NormalizedUsage = strings.Replace(f.rows.OriginalRows.MeteringEvents[0].NormalizedUsage, "confirmed_final", status, 1)
			rqClean(t, f.rows)
		})
	}
	for _, outcome := range []string{"completed", "failed", "incomplete", "cancelled", "unknown"} {
		for _, clientCancelled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/client_cancelled_%t", outcome, clientCancelled), func(t *testing.T) {
				f := rqFixture{}
				f.add("r", "", "none", false, "none", false, "normal", "", "none", 10)
				in := &f.rows.PrefixRows.IncomingSources[0]
				in.ProtocolOutcome = outcome
				want := "failed"
				if outcome == "completed" {
					want = "succeeded"
				}
				if clientCancelled {
					in.TransportEndReason = "client_cancelled"
					want = "cancelled"
				}
				in.InputDigest = rqDigest(*in)
				final := &f.rows.OriginalRows.FinalizationRecords[0]
				final.InputDigest = in.InputDigest
				final.ProtocolOutcome = outcome
				final.TransportEndReason = in.TransportEndReason
				f.rows.PrefixRows.RequestDecisions[0].AfterStatus = want
				rqClean(t, f.rows)
			})
		}
	}
}
