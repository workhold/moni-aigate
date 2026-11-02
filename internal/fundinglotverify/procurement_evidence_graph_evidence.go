package fundinglotverify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
)

type procurementEvidenceUsage struct {
	Status          string `json:"status"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
}
type procurementEvidencePair struct{ first, second string }
type procurementEvidenceSource struct{ tenant, source, event string }
type procurementEvidenceMeter struct {
	row   ProcurementEvidenceMeterRow
	usage procurementEvidenceUsage
	valid bool
}
type procurementEvidenceConflict struct {
	row   ProcurementEvidenceConflictRow
	usage procurementEvidenceUsage
	valid bool
}
type procurementEvidenceFinal struct {
	row                  ProcurementEvidenceFinalizationRow
	valid, matched, self bool
}
type procurementEvidenceCost struct{ amount, basis string }
type procurementEvidenceSemantic struct {
	usage  procurementEvidenceUsage
	reason string
}
type procurementEvidenceIndex struct {
	w                    *procurementEvidenceWork
	requests             map[string]ProcurementEvidenceRequestRow
	attempts             map[string]ProcurementAttemptRow
	reservations         map[string]ProcurementEvidenceReservationRow
	accountRows          map[string]ProcurementEvidenceAccountRow
	meters               map[string]*procurementEvidenceMeter
	conflicts            map[string]*procurementEvidenceConflict
	finals               map[string]*procurementEvidenceFinal
	identityRows         map[procurementEvidencePair]ProcurementEvidenceIdentityRow
	identityByEvent      map[procurementEvidencePair][]ProcurementEvidenceIdentityRow
	identityCount        map[string]int64
	conflictCandidates   map[string]map[string]bool
	finalsByEvent        map[string][]*procurementEvidenceFinal
	finalsByAttempt      map[string][]*procurementEvidenceFinal
	finalsByRequest      map[string][]*procurementEvidenceFinal
	conflictsByEvent     map[string][]*procurementEvidenceConflict
	conflictTenant       map[string]bool
	eventsByAttempt      map[string][]ProcurementEvidenceBudgetEventRow
	eventRows            map[string]ProcurementEvidenceBudgetEventRow
	correctionsByAttempt map[string][]ProcurementCostCorrectionRow
	proposals            map[string]ProcurementCostProposalRow
	proposalApproval     map[string]ProcurementCostProposalRow
	proposalEvent        map[string]ProcurementCostProposalRow
	normalSemantics      map[procurementEvidencePair]map[string]procurementEvidenceSemantic
	outbox               []ProcurementOutboxRow
	commands             []ProcurementCommandRow
	costsByAttempt       map[string]map[procurementEvidenceCost]bool
	recordedByAttempt    map[string]procurementEvidenceCost
	anomalyAccounts      map[string]bool
}

// Decode tokens, rather than a map/struct, so duplicate decoded keys are errors.
func procurementEvidenceObject(raw string, keys []string) (map[string]json.RawMessage, bool) {
	if !procurementAttemptIndexableText(raw) || !procurementCommandJSONUnicode(raw) {
		return nil, false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, false
	}
	allowed := make(map[string]bool, len(keys))
	for _, k := range keys {
		allowed[k] = true
	}
	out := make(map[string]json.RawMessage, len(keys))
	for d.More() {
		t, e := d.Token()
		k, ok := t.(string)
		if e != nil || !ok || !allowed[k] {
			return nil, false
		}
		if _, exists := out[k]; exists {
			return nil, false
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return nil, false
		}
		out[k] = v
	}
	t, err = d.Token()
	if err != nil || t != json.Delim('}') || len(out) != len(keys) {
		return nil, false
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, false
	}
	return out, true
}
func procurementEvidenceUsageParse(raw string) (procurementEvidenceUsage, bool) {
	var u procurementEvidenceUsage
	keys := []string{"status", "input_tokens", "output_tokens", "total_tokens", "cached_tokens", "reasoning_tokens"}
	obj, ok := procurementEvidenceObject(raw, keys)
	if !ok {
		return u, false
	}
	if json.Unmarshal(obj["status"], &u.Status) != nil || !procurementAttemptStatus(u.Status, "confirmed_final", "provisional", "estimated", "missing", "conflicting") {
		return u, false
	}
	values := []*int64{&u.InputTokens, &u.OutputTokens, &u.TotalTokens, &u.CachedTokens, &u.ReasoningTokens}
	for i, k := range keys[1:] {
		s := strings.TrimSpace(string(obj[k]))
		if len(s) > 19 {
			return u, false
		}
		n, valid := procurementEvidenceInt64(s)
		if !valid {
			return u, false
		}
		*values[i] = n.Int64()
	}
	if u.CachedTokens > u.InputTokens || u.ReasoningTokens > u.OutputTokens {
		return u, false
	}
	if u.Status == "confirmed_final" {
		total := new(big.Int).Add(big.NewInt(u.InputTokens), big.NewInt(u.OutputTokens))
		if total.Cmp(big.NewInt(u.TotalTokens)) != 0 {
			return u, false
		}
	}
	return u, true
}
func procurementEvidenceHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func procurementEvidenceDigest(u procurementEvidenceUsage, reason string) string {
	return procurementEvidenceHash(struct {
		Usage  procurementEvidenceUsage
		Reason string
	}{u, reason})
}

var procurementEvidenceReasons = []string{"done", "eof", "provider_error", "platform_error", "platform_timeout", "client_cancelled", "connection_error", "unknown", "not_sent", "rejected_no_consumption"}

func procurementEvidenceNoConsumption(reason string) bool {
	return reason == "not_sent" || reason == "rejected_no_consumption"
}
func procurementEvidenceRank(stage string) int {
	if stage == "content" {
		return 2
	}
	if stage == "committed" {
		return 1
	}
	return 0
}
func procurementEvidenceOwnStage(stage string, content bool) string {
	if content || stage == "content" {
		return "content"
	}
	return stage
}
func procurementEvidenceCanOutput(original string, content bool, stored string) bool {
	own := procurementEvidenceOwnStage(original, content)
	if own == stored {
		return true
	}
	if stored == "content" {
		return true
	}
	return stored == "committed" && (own == "none" || own == "unknown")
}

func procurementEvidenceFirst[T any](w *procurementEvidenceWork, rows []T, key func(T) string) (map[string]T, error) {
	out := make(map[string]T, len(rows))
	for _, r := range rows {
		if err := w.tick(); err != nil {
			return nil, err
		}
		k := key(r)
		if !procurementAttemptValidText(k) {
			continue
		}
		if _, ok := out[k]; !ok {
			out[k] = r
		}
	}
	return out, nil
}
func (w *procurementEvidenceWork) index(in ProcurementEvidenceGraphRows) (*procurementEvidenceIndex, error) {
	x := &procurementEvidenceIndex{w: w, meters: make(map[string]*procurementEvidenceMeter), conflicts: make(map[string]*procurementEvidenceConflict), finals: make(map[string]*procurementEvidenceFinal), identityRows: make(map[procurementEvidencePair]ProcurementEvidenceIdentityRow), identityByEvent: make(map[procurementEvidencePair][]ProcurementEvidenceIdentityRow), identityCount: make(map[string]int64), conflictCandidates: make(map[string]map[string]bool), finalsByEvent: make(map[string][]*procurementEvidenceFinal), finalsByAttempt: make(map[string][]*procurementEvidenceFinal), finalsByRequest: make(map[string][]*procurementEvidenceFinal), conflictsByEvent: make(map[string][]*procurementEvidenceConflict), conflictTenant: make(map[string]bool), eventsByAttempt: make(map[string][]ProcurementEvidenceBudgetEventRow), correctionsByAttempt: make(map[string][]ProcurementCostCorrectionRow), proposalApproval: make(map[string]ProcurementCostProposalRow), proposalEvent: make(map[string]ProcurementCostProposalRow), normalSemantics: make(map[procurementEvidencePair]map[string]procurementEvidenceSemantic), costsByAttempt: make(map[string]map[procurementEvidenceCost]bool), recordedByAttempt: make(map[string]procurementEvidenceCost), anomalyAccounts: make(map[string]bool)}
	var err error
	if x.requests, err = procurementEvidenceFirst(w, in.Requests, func(r ProcurementEvidenceRequestRow) string { return r.ID }); err != nil {
		return nil, err
	}
	if x.attempts, err = procurementEvidenceFirst(w, in.Attempts, func(r ProcurementAttemptRow) string { return r.ID }); err != nil {
		return nil, err
	}
	if x.reservations, err = procurementEvidenceFirst(w, in.Reservations, func(r ProcurementEvidenceReservationRow) string { return r.AttemptID }); err != nil {
		return nil, err
	}
	if x.accountRows, err = procurementEvidenceFirst(w, in.Accounts, func(r ProcurementEvidenceAccountRow) string { return r.ID }); err != nil {
		return nil, err
	}
	if x.eventRows, err = procurementEvidenceFirst(w, in.BudgetEvents, func(r ProcurementEvidenceBudgetEventRow) string { return r.ID }); err != nil {
		return nil, err
	}
	if x.proposals, err = procurementEvidenceFirst(w, in.Proposals, func(r ProcurementCostProposalRow) string { return r.ID }); err != nil {
		return nil, err
	}
	commandMap, e := procurementEvidenceFirst(w, in.Commands, func(r ProcurementCommandRow) string { return r.OperationID })
	if e != nil {
		return nil, e
	}
	for _, r := range commandMap {
		x.commands = append(x.commands, r)
	}
	outboxMap, e := procurementEvidenceFirst(w, in.Outbox, func(r ProcurementOutboxRow) string { return r.ID })
	if e != nil {
		return nil, e
	}
	for _, r := range outboxMap {
		x.outbox = append(x.outbox, r)
	}
	correctionMap, e := procurementEvidenceFirst(w, in.Corrections, func(r ProcurementCostCorrectionRow) string { return r.ID })
	if e != nil {
		return nil, e
	}
	for _, r := range correctionMap {
		x.correctionsByAttempt[r.AttemptID] = append(x.correctionsByAttempt[r.AttemptID], r)
	}
	for _, r := range x.proposals {
		if r.State == "approved" && r.ApprovalOperationID != nil {
			x.proposalApproval[*r.ApprovalOperationID] = r
			x.proposalEvent["correction:"+*r.ApprovalOperationID] = r
		}
	}
	seenSources := make(map[procurementEvidenceSource]bool)
	seenMeterIDs := make(map[string]bool)
	seenConflictUsage := make(map[struct {
		event string
		usage procurementEvidenceUsage
	}]bool)
	for _, r := range in.Metering {
		if err = w.tick(); err != nil {
			return nil, err
		}
		if r.SchemaVersion != 1 {
			return nil, ErrProcurementEvidenceGraphRowsExecution
		}
		if !procurementAttemptValidText(r.ID) || !legacyTenantText(r.TenantID) || !procurementAttemptValidText(r.RequestID) || len(r.Source) == 0 || len(r.Source) > 80 || len(r.SourceEventID) == 0 || len(r.SourceEventID) > 200 || r.AttemptID != nil && !procurementAttemptValidText(*r.AttemptID) {
			w.out.Mismatches.InvalidTextRows++
		}
		u, valid := procurementEvidenceUsageParse(r.UsageJSON)
		if !valid {
			w.out.Mismatches.InvalidUsageRows++
		}
		if !procurementAttemptValidText(r.ID) {
			continue
		}
		if seenMeterIDs[r.ID] {
			w.out.Mismatches.DuplicateMeterRows++
			continue
		}
		seenMeterIDs[r.ID] = true
		key := procurementEvidenceSource{r.TenantID, r.Source, r.SourceEventID}
		dupSource := seenSources[key]
		seenSources[key] = true
		if dupSource {
			w.out.Mismatches.DuplicateMeterSourceRows++
		}
		if dupSource {
			continue
		}
		x.meters[r.ID] = &procurementEvidenceMeter{r, u, valid}
	}
	for _, r := range in.Conflicts {
		if err = w.tick(); err != nil {
			return nil, err
		}
		if !procurementAttemptValidText(r.ID) || !procurementAttemptValidText(r.EventID) {
			w.out.Mismatches.InvalidTextRows++
		}
		u, valid := procurementEvidenceUsageParse(r.UsageJSON)
		if !valid {
			w.out.Mismatches.InvalidUsageRows++
		}
		if !procurementAttemptValidText(r.ID) {
			continue
		}
		if _, dup := x.conflicts[r.ID]; dup {
			w.out.Mismatches.DuplicateConflictRows++
			continue
		}
		c := &procurementEvidenceConflict{r, u, valid}
		x.conflicts[r.ID] = c
		x.conflictsByEvent[r.EventID] = append(x.conflictsByEvent[r.EventID], c)
		if m, ok := x.meters[r.EventID]; ok {
			x.conflictTenant[m.row.TenantID] = true
		}
		usageKey := struct {
			event string
			usage procurementEvidenceUsage
		}{r.EventID, u}
		if valid && !seenConflictUsage[usageKey] {
			seenConflictUsage[usageKey] = true
			if x.conflictCandidates[r.EventID] == nil {
				x.conflictCandidates[r.EventID] = make(map[string]bool)
			}
			for _, reason := range procurementEvidenceReasons {
				d := procurementEvidenceDigest(u, reason)
				x.conflictCandidates[r.EventID][d] = true
			}
		}
	}
	for _, r := range in.Finalizations {
		if err = w.tick(); err != nil {
			return nil, err
		}
		_, uuidErr := uuid.Parse(r.ID)
		if uuidErr != nil || !legacyTenantText(r.TenantID) || !procurementAttemptValidText(r.RequestID) || !procurementAttemptValidText(r.EventID) || !procurementCommandHex(r.InputDigest) || len(r.FinishReason) > 80 || !procurementAttemptValidText(r.PolicyVersion) {
			w.out.Mismatches.InvalidTextRows++
		}
		valid := procurementAttemptStatus(r.ProtocolOutcome, "completed", "failed", "incomplete", "cancelled", "unknown") && procurementAttemptStatus(r.TransportEndReason, procurementEvidenceReasons...) && procurementAttemptStatus(r.DeliveryStage, "none", "committed", "content", "unknown") && r.ContentWritten == (r.DeliveryStage == "content")
		if !valid {
			w.out.Mismatches.InvalidEnumRows++
		}
		if !procurementAttemptValidText(r.ID) {
			continue
		}
		if _, dup := x.finals[r.ID]; dup {
			w.out.Mismatches.DuplicateFinalizationRows++
			continue
		}
		f := &procurementEvidenceFinal{row: r, valid: valid}
		x.finals[r.ID] = f
		x.finalsByEvent[r.EventID] = append(x.finalsByEvent[r.EventID], f)
		x.finalsByRequest[r.RequestID] = append(x.finalsByRequest[r.RequestID], f)
		if m, ok := x.meters[r.EventID]; ok && m.row.AttemptID != nil {
			x.finalsByAttempt[*m.row.AttemptID] = append(x.finalsByAttempt[*m.row.AttemptID], f)
			if m.valid && f.valid {
				k := procurementEvidencePair{*m.row.AttemptID, r.EventID}
				if x.normalSemantics[k] == nil {
					x.normalSemantics[k] = make(map[string]procurementEvidenceSemantic)
				}
				x.normalSemantics[k][procurementEvidenceDigest(m.usage, r.TransportEndReason)] = procurementEvidenceSemantic{m.usage, r.TransportEndReason}
			}
		}
	}
	for _, r := range in.Identities {
		if err = w.tick(); err != nil {
			return nil, err
		}
		if !procurementAttemptValidText(r.AttemptID) || !procurementCommandHex(r.Digest) || !procurementApprovalUUID(r.EventID) {
			w.out.Mismatches.InvalidTextRows++
		}
		k := procurementEvidencePair{r.AttemptID, r.Digest}
		if _, dup := x.identityRows[k]; dup {
			w.out.Mismatches.DuplicateIdentityRows++
			continue
		}
		x.identityRows[k] = r
		x.identityCount[r.AttemptID]++
		e := procurementEvidencePair{r.AttemptID, r.EventID}
		x.identityByEvent[e] = append(x.identityByEvent[e], r)
	}
	for _, r := range x.eventRows {
		x.eventsByAttempt[r.AttemptID] = append(x.eventsByAttempt[r.AttemptID], r)
	}
	return x, nil
}

func (x *procurementEvidenceIndex) evidence() error {
	w := x.w
	type cohort struct {
		values map[procurementEvidenceUsage]bool
		anchor bool
	}
	cohorts := make(map[procurementEvidencePair]*cohort)
	for _, m := range x.meters {
		if err := w.tick(); err != nil {
			return err
		}
		r := m.row
		request, ok := x.requests[r.RequestID]
		identityOK := ok && request.TenantID == r.TenantID
		if r.AttemptID != nil {
			a, exists := x.attempts[*r.AttemptID]
			identityOK = identityOK && exists && a.RequestID == r.RequestID && a.TenantID == r.TenantID
		}
		if !identityOK {
			w.out.Mismatches.MeterIdentityRows++
		}
		if m.valid && m.usage.Status == "confirmed_final" && r.AttemptID != nil {
			k := procurementEvidencePair{r.TenantID, *r.AttemptID}
			c := cohorts[k]
			if c == nil {
				c = &cohort{values: make(map[procurementEvidenceUsage]bool)}
				cohorts[k] = c
			}
			c.values[m.usage] = true
			c.anchor = c.anchor || len(x.finalsByEvent[r.ID]) > 0
		}
	}
	for _, c := range x.conflicts {
		if err := w.tick(); err != nil {
			return err
		}
		w.out.Unproven.ConflictIncomingUnproven++
		if _, ok := x.meters[c.row.EventID]; !ok {
			w.out.Mismatches.ConflictIdentityRows++
		}
	}
	for _, m := range x.meters {
		if err := w.tick(); err != nil {
			return err
		}
		if len(x.finalsByEvent[m.row.ID]) > 0 {
			continue
		}
		born := false
		if m.valid && m.usage.Status == "confirmed_final" && m.row.AttemptID != nil {
			c := cohorts[procurementEvidencePair{m.row.TenantID, *m.row.AttemptID}]
			if c != nil && c.anchor && len(c.values) > 1 {
				for _, conflict := range x.conflictsByEvent[m.row.ID] {
					if conflict.valid && conflict.usage == m.usage {
						born = true
						break
					}
				}
			}
		}
		if !born {
			w.out.Mismatches.MeterBirthRows++
		}
	}
	for _, f := range x.finals {
		if err := w.tick(); err != nil {
			return err
		}
		r := f.row
		request, requestOK := x.requests[r.RequestID]
		m, meterOK := x.meters[r.EventID]
		if !requestOK || !meterOK || request.TenantID != r.TenantID || meterOK && (m.row.TenantID != r.TenantID || m.row.RequestID != r.RequestID) {
			w.out.Mismatches.FinalizationIdentityRows++
		}
		if requestOK && r.PolicyVersion != request.PolicyVersion {
			w.out.Mismatches.FinalizationPolicyRows++
		}
		if !meterOK || !m.valid || !f.valid {
			continue
		}
		attempt := ""
		if m.row.AttemptID != nil {
			attempt = *m.row.AttemptID
		}
		// Independent preimage protocol; declaration order and capitalization are persisted ABI.
		for _, stage := range []string{"none", "committed", "content", "unknown"} {
			for _, content := range []bool{false, true} {
				if !procurementEvidenceCanOutput(stage, content, r.DeliveryStage) {
					continue
				}
				d := procurementEvidenceHash(struct {
					ID, TenantID, RequestID, AttemptID, Source, SourceEventID        string
					Usage                                                            procurementEvidenceUsage
					ProtocolOutcome, TransportEndReason, DeliveryStage, FinishReason string
					ContentWritten                                                   bool
					ObservedAt                                                       time.Time
				}{r.ID, r.TenantID, r.RequestID, attempt, m.row.Source, m.row.SourceEventID, m.usage, r.ProtocolOutcome, r.TransportEndReason, stage, r.FinishReason, content, time.Time{}})
				if d == r.InputDigest {
					f.matched = true
					f.self = f.self || procurementEvidenceOwnStage(stage, content) == r.DeliveryStage
				}
			}
		}
		if !f.matched {
			w.out.Mismatches.FinalizationInputDigestRows++
		}
	}
	for id, request := range x.requests {
		if err := w.tick(); err != nil {
			return err
		}
		fs := x.finalsByRequest[id]
		highest := 0
		levels := uint8(0)
		anchors := [3]bool{}
		need := false
		for _, f := range fs {
			if !f.valid {
				continue
			}
			rank := procurementEvidenceRank(f.row.DeliveryStage)
			if rank > highest {
				highest = rank
			}
			levels |= 1 << rank
			if f.matched && f.self {
				anchors[rank] = true
			}
			need = need || f.matched && !f.self
		}
		latestOK := true
		if request.LatestFinalizationID == nil {
			latestOK = len(fs) == 0
		} else {
			f, ok := x.finals[*request.LatestFinalizationID]
			latestOK = ok && f.row.RequestID == id && f.row.TenantID == request.TenantID
			if latestOK && procurementEvidenceRank(f.row.DeliveryStage) < highest {
				w.out.Mismatches.FinalizationStickyRows++
			}
		}
		if !latestOK {
			w.out.Mismatches.LatestFinalizationRows++
		}
		for _, f := range fs {
			if f.matched && !f.self {
				w.out.Unproven.OriginalDeliveryPredecessorUnproven++
				if !anchors[procurementEvidenceRank(f.row.DeliveryStage)] {
					w.out.Mismatches.FinalizationStickyRows++
				}
			}
		}
		if levels != 0 && levels&(levels-1) != 0 || need {
			w.out.Unproven.FinalizationOrderUnproven++
		}
	}
	return nil
}

func (x *procurementEvidenceIndex) identities() error {
	w := x.w
	for _, f := range x.finals {
		if err := w.tick(); err != nil {
			return err
		}
		m, ok := x.meters[f.row.EventID]
		if !ok || !m.valid || m.row.AttemptID == nil {
			continue
		}
		attempt := *m.row.AttemptID
		if _, managed := x.reservations[attempt]; !managed {
			continue
		}
		if _, ok := x.identityRows[procurementEvidencePair{attempt, procurementEvidenceDigest(m.usage, f.row.TransportEndReason)}]; !ok {
			w.out.Mismatches.NormalIdentityMissingRows++
		}
	}
	for _, r := range x.identityRows {
		if err := w.tick(); err != nil {
			return err
		}
		a, attemptOK := x.attempts[r.AttemptID]
		_, managed := x.reservations[r.AttemptID]
		m, meterOK := x.meters[r.EventID]
		if !attemptOK || !managed || !meterOK || meterOK && m.row.TenantID != a.TenantID {
			w.out.Mismatches.IdentitySourceRows++
		}
		matched := false
		if meterOK && m.valid && m.row.AttemptID != nil && *m.row.AttemptID == r.AttemptID && m.row.RequestID == a.RequestID {
			_, matched = x.normalSemantics[procurementEvidencePair{r.AttemptID, r.EventID}][r.Digest]
		}
		conflictMatch := x.conflictCandidates[r.EventID][r.Digest]
		if !matched {
			if conflictMatch {
				w.out.Unproven.ConflictReasonUnproven++
			} else {
				w.out.Mismatches.IdentityDigestRows++
			}
		}
		if meterOK && (m.row.AttemptID == nil || *m.row.AttemptID != r.AttemptID || m.row.RequestID != a.RequestID) {
			if !conflictMatch {
				w.out.Mismatches.IdentitySourceRows++
			}
			w.out.Unproven.IncomingBindingUnproven++
		}
	}
	for _, p := range x.proposals {
		if err := w.tick(); err != nil {
			return err
		}
		r, ok := x.reservations[p.AttemptID]
		if !ok {
			continue
		}
		rev, revOK := procurementEvidencePositiveInt64(r.Revision)
		expected, expectedOK := procurementEvidencePositiveInt64(p.ExpectedRevision)
		count, countOK := procurementEvidenceInt64(p.ExpectedEvidenceCount)
		if !revOK || !expectedOK || !countOK {
			continue
		}
		actual := big.NewInt(x.identityCount[p.AttemptID])
		if expected.Cmp(rev) > 0 || count.Cmp(actual) > 0 {
			w.out.Mismatches.ProposalBarrierRows++
		}
		if p.State == "approved" && (expected.Cmp(rev) < 0 || count.Cmp(actual) < 0) {
			w.out.Unproven.ProposalBarrierUnproven++
		}
	}
	return nil
}
