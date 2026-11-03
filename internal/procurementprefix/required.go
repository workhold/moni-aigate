package procurementprefix

// This compatibility enumerator owns its raw indexes and exact natural-object
// sets. It consumes no Base report and proves no key. Its branch conditions
// preserve the existing algorithm; manual fixtures and a differential oracle
// verify that separately executable compatibility boundary.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"io"
	"math/big"
	"strings"
	"time"
)

type requiredWork struct {
	replay      *replayWork
	keys        propositionKeys
	diagnostics fundinglotverify.ProcurementEvidenceGraphMismatches
	err         error
}

func (w *requiredWork) tick() error {
	if w.err == nil {
		w.err = w.replay.tick()
	}
	return w.err
}
func enumerateRequired(w *replayWork, rows Rows) (propositionKeys, error) {
	if w == nil || w.b.ctx == nil {
		return propositionKeys{}, ErrInput
	}
	base, err := projectBase(w.b.ctx, rows)
	if err != nil {
		return propositionKeys{}, err
	}
	return enumerateRequiredBase(w, base)
}
func enumerateRequiredBase(w *replayWork, rows fundinglotverify.ProcurementEvidenceGraphRows) (propositionKeys, error) {
	if w == nil || w.b.ctx == nil {
		return propositionKeys{}, ErrInput
	}
	if err := w.b.check(); err != nil {
		return propositionKeys{}, err
	}
	rw := &requiredWork{replay: w, keys: newPropositionKeys()}
	x, err := rw.index(rows)
	if err != nil {
		return propositionKeys{}, err
	}
	for _, run := range []func() error{x.evidence, x.identities, x.events, x.costs, x.accounts} {
		if err := run(); err != nil {
			return propositionKeys{}, err
		}
	}
	if err := rw.err; err != nil {
		return propositionKeys{}, err
	}
	if err := w.b.check(); err != nil {
		return propositionKeys{}, err
	}
	return rw.keys, nil
}

type requiredUsage struct {
	Status          string `json:"status"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
}
type requiredPair struct{ first, second string }
type requiredSource struct{ tenant, source, event string }
type requiredMeter struct {
	row   fundinglotverify.ProcurementEvidenceMeterRow
	usage requiredUsage
	valid bool
}
type requiredConflict struct {
	row   fundinglotverify.ProcurementEvidenceConflictRow
	usage requiredUsage
	valid bool
}
type requiredFinal struct {
	row                  fundinglotverify.ProcurementEvidenceFinalizationRow
	valid, matched, self bool
}
type requiredCost struct{ amount, basis string }
type requiredSemantic struct {
	usage  requiredUsage
	reason string
}
type requiredIndex struct {
	w                    *requiredWork
	requests             map[string]fundinglotverify.ProcurementEvidenceRequestRow
	attempts             map[string]fundinglotverify.ProcurementAttemptRow
	reservations         map[string]fundinglotverify.ProcurementEvidenceReservationRow
	accountRows          map[string]fundinglotverify.ProcurementEvidenceAccountRow
	meters               map[string]*requiredMeter
	conflicts            map[string]*requiredConflict
	finals               map[string]*requiredFinal
	identityRows         map[requiredPair]fundinglotverify.ProcurementEvidenceIdentityRow
	identityByEvent      map[requiredPair][]fundinglotverify.ProcurementEvidenceIdentityRow
	identityCount        map[string]int64
	conflictCandidates   map[string]map[string]bool
	finalsByEvent        map[string][]*requiredFinal
	finalsByAttempt      map[string][]*requiredFinal
	finalsByRequest      map[string][]*requiredFinal
	conflictsByEvent     map[string][]*requiredConflict
	conflictTenant       map[string]bool
	eventsByAttempt      map[string][]fundinglotverify.ProcurementEvidenceBudgetEventRow
	eventRows            map[string]fundinglotverify.ProcurementEvidenceBudgetEventRow
	correctionsByAttempt map[string][]fundinglotverify.ProcurementCostCorrectionRow
	proposals            map[string]fundinglotverify.ProcurementCostProposalRow
	proposalApproval     map[string]fundinglotverify.ProcurementCostProposalRow
	proposalEvent        map[string]fundinglotverify.ProcurementCostProposalRow
	normalSemantics      map[requiredPair]map[string]requiredSemantic
	outbox               []fundinglotverify.ProcurementOutboxRow
	commands             []fundinglotverify.ProcurementCommandRow
	costsByAttempt       map[string]map[requiredCost]bool
	recordedByAttempt    map[string]requiredCost
	anomalyAccounts      map[string]bool
}

// Decode tokens, rather than a map/struct, so duplicate decoded keys are errors.
func (w *requiredWork) object(raw string, keys []string) (map[string]json.RawMessage, bool) {
	if !validText(raw) || !w.jsonUnicode(raw) {
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
func (w *requiredWork) usageParse(raw string) (requiredUsage, bool) {
	var u requiredUsage
	keys := []string{"status", "input_tokens", "output_tokens", "total_tokens", "cached_tokens", "reasoning_tokens"}
	obj, ok := w.object(raw, keys)
	if !ok {
		return u, false
	}
	if json.Unmarshal(obj["status"], &u.Status) != nil || !requiredOneOf(u.Status, "confirmed_final", "provisional", "estimated", "missing", "conflicting") {
		return u, false
	}
	values := []*int64{&u.InputTokens, &u.OutputTokens, &u.TotalTokens, &u.CachedTokens, &u.ReasoningTokens}
	for i, k := range keys[1:] {
		s := strings.TrimSpace(string(obj[k]))
		if len(s) > 19 {
			return u, false
		}
		n, valid := requiredInt64(s)
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
func requiredHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func requiredDigest(u requiredUsage, reason string) string {
	return requiredHash(struct {
		Usage  requiredUsage
		Reason string
	}{u, reason})
}

var requiredReasons = []string{"done", "eof", "provider_error", "platform_error", "platform_timeout", "client_cancelled", "connection_error", "unknown", "not_sent", "rejected_no_consumption"}

func requiredNoConsumption(reason string) bool {
	return reason == "not_sent" || reason == "rejected_no_consumption"
}
func requiredRank(stage string) int {
	if stage == "content" {
		return 2
	}
	if stage == "committed" {
		return 1
	}
	return 0
}
func requiredOwnStage(stage string, content bool) string {
	if content || stage == "content" {
		return "content"
	}
	return stage
}
func requiredCanOutput(original string, content bool, stored string) bool {
	own := requiredOwnStage(original, content)
	if own == stored {
		return true
	}
	if stored == "content" {
		return true
	}
	return stored == "committed" && (own == "none" || own == "unknown")
}

func requiredFirst[T any](w *requiredWork, rows []T, key func(T) string) (map[string]T, error) {
	out := make(map[string]T, len(rows))
	for _, r := range rows {
		if err := w.tick(); err != nil {
			return nil, err
		}
		k := key(r)
		if !requiredText(k) {
			continue
		}
		if _, ok := out[k]; !ok {
			out[k] = r
		}
	}
	return out, nil
}
func (w *requiredWork) index(in fundinglotverify.ProcurementEvidenceGraphRows) (*requiredIndex, error) {
	x := &requiredIndex{w: w, meters: make(map[string]*requiredMeter), conflicts: make(map[string]*requiredConflict), finals: make(map[string]*requiredFinal), identityRows: make(map[requiredPair]fundinglotverify.ProcurementEvidenceIdentityRow), identityByEvent: make(map[requiredPair][]fundinglotverify.ProcurementEvidenceIdentityRow), identityCount: make(map[string]int64), conflictCandidates: make(map[string]map[string]bool), finalsByEvent: make(map[string][]*requiredFinal), finalsByAttempt: make(map[string][]*requiredFinal), finalsByRequest: make(map[string][]*requiredFinal), conflictsByEvent: make(map[string][]*requiredConflict), conflictTenant: make(map[string]bool), eventsByAttempt: make(map[string][]fundinglotverify.ProcurementEvidenceBudgetEventRow), correctionsByAttempt: make(map[string][]fundinglotverify.ProcurementCostCorrectionRow), proposalApproval: make(map[string]fundinglotverify.ProcurementCostProposalRow), proposalEvent: make(map[string]fundinglotverify.ProcurementCostProposalRow), normalSemantics: make(map[requiredPair]map[string]requiredSemantic), costsByAttempt: make(map[string]map[requiredCost]bool), recordedByAttempt: make(map[string]requiredCost), anomalyAccounts: make(map[string]bool)}
	var err error
	if x.requests, err = requiredFirst(w, in.Requests, func(r fundinglotverify.ProcurementEvidenceRequestRow) string { return r.ID }); err != nil {
		return nil, err
	}
	if x.attempts, err = requiredFirst(w, in.Attempts, func(r fundinglotverify.ProcurementAttemptRow) string { return r.ID }); err != nil {
		return nil, err
	}
	if x.reservations, err = requiredFirst(w, in.Reservations, func(r fundinglotverify.ProcurementEvidenceReservationRow) string { return r.AttemptID }); err != nil {
		return nil, err
	}
	if x.accountRows, err = requiredFirst(w, in.Accounts, func(r fundinglotverify.ProcurementEvidenceAccountRow) string { return r.ID }); err != nil {
		return nil, err
	}
	if x.eventRows, err = requiredFirst(w, in.BudgetEvents, func(r fundinglotverify.ProcurementEvidenceBudgetEventRow) string { return r.ID }); err != nil {
		return nil, err
	}
	if x.proposals, err = requiredFirst(w, in.Proposals, func(r fundinglotverify.ProcurementCostProposalRow) string { return r.ID }); err != nil {
		return nil, err
	}
	commandMap, e := requiredFirst(w, in.Commands, func(r fundinglotverify.ProcurementCommandRow) string { return r.OperationID })
	if e != nil {
		return nil, e
	}
	for _, r := range commandMap {
		if err := w.tick(); err != nil {
			return nil, err
		}
		x.commands = append(x.commands, r)
	}
	outboxMap, e := requiredFirst(w, in.Outbox, func(r fundinglotverify.ProcurementOutboxRow) string { return r.ID })
	if e != nil {
		return nil, e
	}
	for _, r := range outboxMap {
		if err := w.tick(); err != nil {
			return nil, err
		}
		x.outbox = append(x.outbox, r)
	}
	correctionMap, e := requiredFirst(w, in.Corrections, func(r fundinglotverify.ProcurementCostCorrectionRow) string { return r.ID })
	if e != nil {
		return nil, e
	}
	for _, r := range correctionMap {
		if err := w.tick(); err != nil {
			return nil, err
		}
		x.correctionsByAttempt[r.AttemptID] = append(x.correctionsByAttempt[r.AttemptID], r)
	}
	for _, r := range x.proposals {
		if err := w.tick(); err != nil {
			return nil, err
		}
		if r.State == "approved" && r.ApprovalOperationID != nil {
			x.proposalApproval[*r.ApprovalOperationID] = r
			x.proposalEvent["correction:"+*r.ApprovalOperationID] = r
		}
	}
	seenSources := make(map[requiredSource]bool)
	seenMeterIDs := make(map[string]bool)
	seenConflictUsage := make(map[struct {
		event string
		usage requiredUsage
	}]bool)
	for _, r := range in.Metering {
		if err = w.tick(); err != nil {
			return nil, err
		}
		if r.SchemaVersion != 1 {
			return nil, ErrInput
		}
		if !requiredText(r.ID) || !validText(r.TenantID) || !requiredText(r.RequestID) || len(r.Source) == 0 || len(r.Source) > 80 || len(r.SourceEventID) == 0 || len(r.SourceEventID) > 200 || r.AttemptID != nil && !requiredText(*r.AttemptID) {
			w.diagnostics.InvalidTextRows++
		}
		u, valid := w.usageParse(r.UsageJSON)
		if !valid {
			w.diagnostics.InvalidUsageRows++
		}
		if !requiredText(r.ID) {
			continue
		}
		if seenMeterIDs[r.ID] {
			w.diagnostics.DuplicateMeterRows++
			continue
		}
		seenMeterIDs[r.ID] = true
		key := requiredSource{r.TenantID, r.Source, r.SourceEventID}
		dupSource := seenSources[key]
		seenSources[key] = true
		if dupSource {
			w.diagnostics.DuplicateMeterSourceRows++
		}
		if dupSource {
			continue
		}
		x.meters[r.ID] = &requiredMeter{r, u, valid}
	}
	for _, r := range in.Conflicts {
		if err = w.tick(); err != nil {
			return nil, err
		}
		if !requiredText(r.ID) || !requiredText(r.EventID) {
			w.diagnostics.InvalidTextRows++
		}
		u, valid := w.usageParse(r.UsageJSON)
		if !valid {
			w.diagnostics.InvalidUsageRows++
		}
		if !requiredText(r.ID) {
			continue
		}
		if _, dup := x.conflicts[r.ID]; dup {
			w.diagnostics.DuplicateConflictRows++
			continue
		}
		c := &requiredConflict{r, u, valid}
		x.conflicts[r.ID] = c
		x.conflictsByEvent[r.EventID] = append(x.conflictsByEvent[r.EventID], c)
		if m, ok := x.meters[r.EventID]; ok {
			x.conflictTenant[m.row.TenantID] = true
		}
		usageKey := struct {
			event string
			usage requiredUsage
		}{r.EventID, u}
		if valid && !seenConflictUsage[usageKey] {
			seenConflictUsage[usageKey] = true
			if x.conflictCandidates[r.EventID] == nil {
				x.conflictCandidates[r.EventID] = make(map[string]bool)
			}
			for _, reason := range requiredReasons {
				if err := w.tick(); err != nil {
					return nil, err
				}
				d := requiredDigest(u, reason)
				x.conflictCandidates[r.EventID][d] = true
			}
		}
	}
	for _, r := range in.Finalizations {
		if err = w.tick(); err != nil {
			return nil, err
		}
		_, uuidErr := uuid.Parse(r.ID)
		if uuidErr != nil || !validText(r.TenantID) || !requiredText(r.RequestID) || !requiredText(r.EventID) || !requiredHex(r.InputDigest) || len(r.FinishReason) > 80 || !requiredText(r.PolicyVersion) {
			w.diagnostics.InvalidTextRows++
		}
		valid := requiredOneOf(r.ProtocolOutcome, "completed", "failed", "incomplete", "cancelled", "unknown") && requiredOneOf(r.TransportEndReason, requiredReasons...) && requiredOneOf(r.DeliveryStage, "none", "committed", "content", "unknown") && r.ContentWritten == (r.DeliveryStage == "content")
		if !valid {
			w.diagnostics.InvalidEnumRows++
		}
		if !requiredText(r.ID) {
			continue
		}
		if _, dup := x.finals[r.ID]; dup {
			w.diagnostics.DuplicateFinalizationRows++
			continue
		}
		f := &requiredFinal{row: r, valid: valid}
		x.finals[r.ID] = f
		x.finalsByEvent[r.EventID] = append(x.finalsByEvent[r.EventID], f)
		x.finalsByRequest[r.RequestID] = append(x.finalsByRequest[r.RequestID], f)
		if m, ok := x.meters[r.EventID]; ok && m.row.AttemptID != nil {
			x.finalsByAttempt[*m.row.AttemptID] = append(x.finalsByAttempt[*m.row.AttemptID], f)
			if m.valid && f.valid {
				k := requiredPair{*m.row.AttemptID, r.EventID}
				if x.normalSemantics[k] == nil {
					x.normalSemantics[k] = make(map[string]requiredSemantic)
				}
				x.normalSemantics[k][requiredDigest(m.usage, r.TransportEndReason)] = requiredSemantic{m.usage, r.TransportEndReason}
			}
		}
	}
	for _, r := range in.Identities {
		if err = w.tick(); err != nil {
			return nil, err
		}
		if !requiredText(r.AttemptID) || !requiredHex(r.Digest) || !requiredUUID(r.EventID) {
			w.diagnostics.InvalidTextRows++
		}
		k := requiredPair{r.AttemptID, r.Digest}
		if _, dup := x.identityRows[k]; dup {
			w.diagnostics.DuplicateIdentityRows++
			continue
		}
		x.identityRows[k] = r
		x.identityCount[r.AttemptID]++
		e := requiredPair{r.AttemptID, r.EventID}
		x.identityByEvent[e] = append(x.identityByEvent[e], r)
	}
	for _, r := range x.eventRows {
		if err := w.tick(); err != nil {
			return nil, err
		}
		x.eventsByAttempt[r.AttemptID] = append(x.eventsByAttempt[r.AttemptID], r)
	}
	return x, nil
}

func (x *requiredIndex) evidence() error {
	w := x.w
	type cohort struct {
		values map[requiredUsage]bool
		anchor bool
	}
	cohorts := make(map[requiredPair]*cohort)
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
			w.diagnostics.MeterIdentityRows++
		}
		if m.valid && m.usage.Status == "confirmed_final" && r.AttemptID != nil {
			k := requiredPair{r.TenantID, *r.AttemptID}
			c := cohorts[k]
			if c == nil {
				c = &cohort{values: make(map[requiredUsage]bool)}
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
		w.keys.ConflictIncoming.add(c.row.ID)
		if _, ok := x.meters[c.row.EventID]; !ok {
			w.diagnostics.ConflictIdentityRows++
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
			c := cohorts[requiredPair{m.row.TenantID, *m.row.AttemptID}]
			if c != nil && c.anchor && len(c.values) > 1 {
				for _, conflict := range x.conflictsByEvent[m.row.ID] {
					if err := w.tick(); err != nil {
						return err
					}
					if conflict.valid && conflict.usage == m.usage {
						born = true
						break
					}
				}
			}
		}
		if !born {
			w.diagnostics.MeterBirthRows++
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
			w.diagnostics.FinalizationIdentityRows++
		}
		if requestOK && r.PolicyVersion != request.PolicyVersion {
			w.diagnostics.FinalizationPolicyRows++
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
				if err := w.tick(); err != nil {
					return err
				}
				if !requiredCanOutput(stage, content, r.DeliveryStage) {
					continue
				}
				d := requiredHash(struct {
					ID, TenantID, RequestID, AttemptID, Source, SourceEventID        string
					Usage                                                            requiredUsage
					ProtocolOutcome, TransportEndReason, DeliveryStage, FinishReason string
					ContentWritten                                                   bool
					ObservedAt                                                       time.Time
				}{r.ID, r.TenantID, r.RequestID, attempt, m.row.Source, m.row.SourceEventID, m.usage, r.ProtocolOutcome, r.TransportEndReason, stage, r.FinishReason, content, time.Time{}})
				if d == r.InputDigest {
					f.matched = true
					f.self = f.self || requiredOwnStage(stage, content) == r.DeliveryStage
				}
			}
		}
		if !f.matched {
			w.diagnostics.FinalizationInputDigestRows++
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
			if err := w.tick(); err != nil {
				return err
			}
			if !f.valid {
				continue
			}
			rank := requiredRank(f.row.DeliveryStage)
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
			if latestOK && requiredRank(f.row.DeliveryStage) < highest {
				w.diagnostics.FinalizationStickyRows++
			}
		}
		if !latestOK {
			w.diagnostics.LatestFinalizationRows++
		}
		for _, f := range fs {
			if err := w.tick(); err != nil {
				return err
			}
			if f.matched && !f.self {
				w.keys.OriginalDeliveryPredecessor.add(f.row.ID)
				if !anchors[requiredRank(f.row.DeliveryStage)] {
					w.diagnostics.FinalizationStickyRows++
				}
			}
		}
		if levels != 0 && levels&(levels-1) != 0 || need {
			w.keys.FinalizationOrder.add(id)
		}
	}
	return nil
}

func (x *requiredIndex) identities() error {
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
		if _, ok := x.identityRows[requiredPair{attempt, requiredDigest(m.usage, f.row.TransportEndReason)}]; !ok {
			w.diagnostics.NormalIdentityMissingRows++
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
			w.diagnostics.IdentitySourceRows++
		}
		matched := false
		if meterOK && m.valid && m.row.AttemptID != nil && *m.row.AttemptID == r.AttemptID && m.row.RequestID == a.RequestID {
			_, matched = x.normalSemantics[requiredPair{r.AttemptID, r.EventID}][r.Digest]
		}
		conflictMatch := x.conflictCandidates[r.EventID][r.Digest]
		if !matched {
			if conflictMatch {
				w.keys.ConflictReason.add(r.AttemptID, r.Digest)
			} else {
				w.diagnostics.IdentityDigestRows++
			}
		}
		if meterOK && (m.row.AttemptID == nil || *m.row.AttemptID != r.AttemptID || m.row.RequestID != a.RequestID) {
			if !conflictMatch {
				w.diagnostics.IdentitySourceRows++
			}
			w.keys.IncomingBinding.add(r.AttemptID, r.Digest)
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
		rev, revOK := requiredPositiveInt64(r.Revision)
		expected, expectedOK := requiredPositiveInt64(p.ExpectedRevision)
		count, countOK := requiredInt64(p.ExpectedEvidenceCount)
		if !revOK || !expectedOK || !countOK {
			continue
		}
		actual := big.NewInt(x.identityCount[p.AttemptID])
		if expected.Cmp(rev) > 0 || count.Cmp(actual) > 0 {
			w.diagnostics.ProposalBarrierRows++
		}
		if p.State == "approved" && (expected.Cmp(rev) < 0 || count.Cmp(actual) < 0) {
			w.keys.ProposalBarrier.add(p.ID)
		}
	}
	return nil
}

func requiredAmount(a fundinglotverify.ProcurementAttemptRow, u requiredUsage) (*big.Int, bool) {
	if a.CostInputRate == nil || a.CostOutputRate == nil || !requiredOneOf(a.CostBasis, "contract", "invoice") {
		return nil, false
	}
	// Validate the bounded signed-int64 protocol before arbitrary-precision math.
	if len(*a.CostInputRate) > 19 || len(*a.CostOutputRate) > 19 {
		return nil, false
	}
	in, ok := requiredInt64(*a.CostInputRate)
	if !ok {
		return nil, false
	}
	out, ok := requiredInt64(*a.CostOutputRate)
	if !ok {
		return nil, false
	}
	n := new(big.Int).Mul(big.NewInt(u.InputTokens), in)
	n.Add(n, new(big.Int).Mul(big.NewInt(u.OutputTokens), out))
	n.Add(n, big.NewInt(999999))
	n.Quo(n, big.NewInt(1000000))
	return n, true
}
func requiredNormalCost(a fundinglotverify.ProcurementAttemptRow, u requiredUsage, reason string) (requiredCost, bool) {
	if requiredNoConsumption(reason) {
		return requiredCost{"0", "contract"}, true
	}
	if u.Status != "confirmed_final" {
		return requiredCost{}, false
	}
	n, ok := requiredAmount(a, u)
	if !ok {
		return requiredCost{}, false
	}
	return requiredCost{n.String(), a.CostBasis}, true
}
func (x *requiredIndex) normalEvent(r fundinglotverify.ProcurementEvidenceBudgetEventRow, predicate func(requiredUsage, string) bool) bool {
	w := x.w
	m, ok := x.meters[r.EvidenceRef]
	if !ok || !m.valid || m.row.AttemptID == nil || *m.row.AttemptID != r.AttemptID {
		return false
	}
	a, ok := x.attempts[r.AttemptID]
	if !ok || a.TenantID != m.row.TenantID || a.RequestID != m.row.RequestID {
		return false
	}
	for digest, semantic := range x.normalSemantics[requiredPair{r.AttemptID, r.EvidenceRef}] {
		if err := w.tick(); err != nil {
			return false
		}
		identity, exists := x.identityRows[requiredPair{r.AttemptID, digest}]
		if exists && identity.EventID == r.EvidenceRef && predicate(semantic.usage, semantic.reason) {
			return true
		}
	}
	return false
}
func (x *requiredIndex) disputedSource(r fundinglotverify.ProcurementEvidenceBudgetEventRow) (bool, bool) {
	w := x.w
	a, ok := x.attempts[r.AttemptID]
	if !ok {
		return false, false
	}
	m, ok := x.meters[r.EvidenceRef]
	if !ok || m.row.TenantID != a.TenantID {
		return false, false
	}
	ids := x.identityByEvent[requiredPair{r.AttemptID, r.EvidenceRef}]
	normal, conflict := false, false
	for _, id := range ids {
		if err := w.tick(); err != nil {
			return false, false
		}
		if x.conflictCandidates[r.EvidenceRef][id.Digest] {
			conflict = true
		}
		if m.valid && m.row.AttemptID != nil && *m.row.AttemptID == r.AttemptID && m.row.RequestID == a.RequestID {
			semantic, ok := x.normalSemantics[requiredPair{r.AttemptID, r.EvidenceRef}][id.Digest]
			if ok && (requiredNoConsumption(semantic.reason) || m.usage.Status == "confirmed_final" && (m.usage.InputTokens > 0 || m.usage.OutputTokens > 0)) {
				normal = true
			}
		}
	}
	return normal || conflict, conflict
}
func requiredDesiredHeld(upper, spent *big.Int) *big.Int {
	n := new(big.Int).Sub(upper, spent)
	if n.Sign() < 0 {
		n.SetInt64(0)
	}
	return n
}

func (x *requiredIndex) events() error {
	w := x.w
	for _, r := range x.eventRows {
		if err := w.tick(); err != nil {
			return err
		}
		p, exists := x.reservations[r.AttemptID]
		shape := exists && r.AccountID == p.AccountID
		source := true
		h, hOK := requiredSigned(r.HeldDelta)
		s, sOK := requiredSigned(r.SpentDelta)
		u, uOK := requiredUnsigned(p.UpperAmount)
		if !exists || !hOK || !sOK || !uOK {
			if !exists {
				w.diagnostics.BudgetEventSourceRows++
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
			source = x.normalEvent(r, func(_ requiredUsage, reason string) bool { return requiredNoConsumption(reason) })
		case "consumption_unknown":
			shape = shape && r.EventKey == "unknown:"+r.AttemptID && r.EvidenceRef != ""
			delta = h.Sign() == 0 && zero
			source = x.normalEvent(r, func(usage requiredUsage, reason string) bool {
				return usage.Status != "confirmed_final" && !requiredNoConsumption(reason)
			})
		case "cost_confirmed":
			shape = shape && r.EventKey == "settle:"+r.AttemptID && r.EvidenceRef != ""
			delta = h.Cmp(new(big.Int).Neg(u)) == 0 && s.Sign() >= 0
			source = x.normalEvent(r, func(usage requiredUsage, reason string) bool {
				if usage.Status != "confirmed_final" || requiredNoConsumption(reason) {
					return false
				}
				n, ok := requiredAmount(x.attempts[r.AttemptID], usage)
				return ok && s.Cmp(n) == 0
			})
			if source {
				m := x.meters[r.EvidenceRef]
				in, inOK := requiredInt64(p.InputBound)
				out, outOK := requiredInt64(p.OutputBound)
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
				n, ok := requiredInt64(proposal.Amount)
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
			w.diagnostics.InvalidEnumRows++
			shape = false
			source = false
		}
		if !shape {
			w.diagnostics.BudgetEventShapeRows++
		}
		if !source {
			w.diagnostics.BudgetEventSourceRows++
		}
		if !delta {
			w.diagnostics.EventDeltaRows++
		}
	}
	for attempt, p := range x.reservations {
		if err := w.tick(); err != nil {
			return err
		}
		u, uOK := requiredUnsigned(p.UpperAmount)
		held, hOK := requiredUnsigned(p.HeldAmount)
		spent, sOK := requiredUnsigned(p.SpentAmount)
		if !uOK || !hOK || !sOK {
			continue
		}
		if _, ok := requiredPositiveInt64(p.Revision); !ok {
			w.diagnostics.InvalidEnumRows++
		}
		reserved, unknown, terminal, complex, disputes, corrections := 0, 0, 0, 0, 0, 0
		var main, change fundinglotverify.ProcurementEvidenceBudgetEventRow
		for _, e := range x.eventsByAttempt[attempt] {
			if err := w.tick(); err != nil {
				return err
			}
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
			w.diagnostics.ReservedEventRows++
		}
		stateOK := unknown <= 1 && terminal <= 1 && disputes <= corrections+1
		if complex > 1 {
			w.keys.ProcurementEventOrder.add(attempt)
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
					in, okI := requiredInt64(p.InputBound)
					out, okO := requiredInt64(p.OutputBound)
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
					if n, ok := requiredUnsigned(main.SpentDelta); ok {
						beforeSpent.Set(n)
					}
				} else {
					beforeState = "released"
				}
			}
			dh, okH := requiredSigned(change.HeldDelta)
			ds, okS := requiredSigned(change.SpentDelta)
			if okH && okS {
				targetHeld := new(big.Int)
				targetSpent := new(big.Int).Set(beforeSpent)
				targetState := "settled"
				anomaly := false
				legal := true
				if change.Type == "evidence_disputed" {
					targetHeld = requiredDesiredHeld(u, beforeSpent)
					targetState = "disputed"
					anomaly = true
					_, hasConflict := x.disputedSource(change)
					if !hasConflict {
						legal = x.normalEvent(change, func(usage requiredUsage, reason string) bool {
							return beforeState == "released" && usage.Status == "confirmed_final" && (usage.InputTokens > 0 || usage.OutputTokens > 0) && !requiredNoConsumption(reason) || beforeState == "settled" && beforeSpent.Sign() > 0 && requiredNoConsumption(reason)
						})
					}
				} else {
					legal = beforeState != "open"
					if proposal, exists := x.proposalEvent[change.EventKey]; exists {
						if n, ok := requiredInt64(proposal.Amount); ok {
							targetSpent.Set(n)
						}
					}
					anomaly = targetSpent.Cmp(u) > 0
				}
				expectedH := new(big.Int).Sub(targetHeld, beforeHeld)
				expectedS := new(big.Int).Sub(targetSpent, beforeSpent)
				if dh.Cmp(expectedH) != 0 || ds.Cmp(expectedS) != 0 {
					w.diagnostics.EventDeltaRows++
				}
				stateOK = stateOK && legal && p.State == targetState && held.Cmp(targetHeld) == 0 && spent.Cmp(targetSpent) == 0 && p.Anomaly == anomaly
			}
		} else {
			stateOK = stateOK && (p.State == "settled" || p.State == "disputed")
		}
		if !stateOK {
			w.diagnostics.StateEventRows++
		}
	}
	return nil
}

func (w *requiredWork) recorded(raw string) (string, requiredCost, bool) {
	obj, ok := w.object(raw, []string{"attempt_id", "amount", "basis"})
	if !ok {
		return "", requiredCost{}, false
	}
	var attempt, amount, basis string
	if json.Unmarshal(obj["attempt_id"], &attempt) != nil || json.Unmarshal(obj["amount"], &amount) != nil || json.Unmarshal(obj["basis"], &basis) != nil || !requiredText(attempt) || !requiredOneOf(basis, "contract", "invoice") {
		return "", requiredCost{}, false
	}
	if _, ok := requiredUnsigned(amount); !ok {
		return "", requiredCost{}, false
	}
	return attempt, requiredCost{amount, basis}, true
}
func (x *requiredIndex) costs() error {
	w := x.w
	for attempt, fs := range x.finalsByAttempt {
		if err := w.tick(); err != nil {
			return err
		}
		a, exists := x.attempts[attempt]
		if !exists {
			continue
		}
		candidates := make(map[requiredCost]bool)
		for _, f := range fs {
			if err := w.tick(); err != nil {
				return err
			}
			m := x.meters[f.row.EventID]
			if !f.valid || m == nil || !m.valid || m.row.TenantID != a.TenantID || m.row.RequestID != a.RequestID {
				continue
			}
			if c, ok := requiredNormalCost(a, m.usage, f.row.TransportEndReason); ok {
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
		attempt, c, valid := w.recorded(r.Payload)
		if !valid {
			w.diagnostics.CostRecordedJSONRows++
			continue
		}
		a, ok := x.attempts[attempt]
		if !ok || r.OperationID != attempt || r.TenantID != a.TenantID || r.SchemaVersion != 1 || !x.costsByAttempt[attempt][c] {
			w.diagnostics.CostRecordedSourceRows++
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
		expected := make(map[requiredCost]bool)
		knownCorrections := make(map[requiredCost]bool)
		for _, c := range corrections {
			if err := w.tick(); err != nil {
				return err
			}
			if c.TenantID == a.TenantID {
				key := requiredCost{c.Amount, c.Basis}
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
			if err := w.tick(); err != nil {
				return err
			}
			switch e.Type {
			case "cost_corrected", "evidence_disputed":
				eligibleGap = true
			case "cost_confirmed":
				requiredRecord = true
				n, ok := requiredUnsigned(e.SpentDelta)
				requiredMatches = requiredMatches && ok && hasRecord && record == (requiredCost{n.String(), a.CostBasis})
			case "released_no_consumption":
				requiredRecord = true
				requiredMatches = requiredMatches && hasRecord && record == (requiredCost{"0", "contract"})
			}
		}
		if requiredRecord && (!hasRecord || !requiredMatches) {
			w.diagnostics.CostRecordedMissingRows++
		}
		if len(x.costsByAttempt[attempt]) > 0 && eligibleGap {
			w.keys.CostRecordedEligibility.add(attempt)
		}
		if len(x.costsByAttempt[attempt]) > 0 && !hasRecord && !eligibleGap && !requiredRecord {
			w.diagnostics.CostRecordedMissingRows++
		}
		known := requiredOneOf(a.CostStatus, "contract", "invoice")
		valid := true
		orderGap := false
		if known {
			if a.CostAmount == nil {
				valid = false
			} else {
				c := requiredCost{*a.CostAmount, a.CostStatus}
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
			w.diagnostics.CurrentCostRows++
		}
		if orderGap {
			w.keys.CurrentCostOrder.add(attempt)
		}
	}
	return nil
}

type requiredConfig struct {
	id, label, provider, state, limit string
	created                           time.Time
	revision                          int64
}

func requiredConfigEqual(a, b requiredConfig) bool {
	return a.id == b.id && a.label == b.label && a.provider == b.provider && a.state == b.state && a.limit == b.limit && a.created.Equal(b.created)
}
func requiredConfigSnapshot(s requiredSnapshot) (requiredConfig, bool) {
	if !s.valid || s.null {
		return requiredConfig{}, false
	}
	v := func(k string) string { return requiredSnapshotValue(s, k) }
	rev, ok := requiredInt64(v("revision"))
	if !ok || rev.Sign() == 0 {
		return requiredConfig{}, false
	}
	at, ok := requiredTime(v("created_at"))
	if !ok {
		return requiredConfig{}, false
	}
	return requiredConfig{v("id"), v("label"), v("provider"), v("state"), v("limit_amount"), at, rev.Int64()}, true
}
func requiredInt64(s string) (*big.Int, bool) {
	if len(s) > 19 {
		return nil, false
	}
	return requiredUnsignedInt64(s)
}
func requiredPositiveInt64(s string) (*big.Int, bool) {
	n, ok := requiredInt64(s)
	return n, ok && n.Sign() > 0
}

type requiredAccountHistory struct {
	versions                  map[int64]requiredConfig
	edges                     map[int64]uint64
	creates                   []requiredConfig
	configurationBad, changed bool
}

func (x *requiredIndex) accounts() error {
	w := x.w
	histories := make(map[string]*requiredAccountHistory, len(x.accountRows))
	for id, r := range x.accountRows {
		if err := w.tick(); err != nil {
			return err
		}
		h := &requiredAccountHistory{versions: make(map[int64]requiredConfig), edges: make(map[int64]uint64)}
		histories[id] = h
		rev, revOK := requiredInt64(r.Revision)
		limit, limitOK := requiredInt64(r.LimitAmount)
		if !revOK || rev.Sign() == 0 || !limitOK || limit.Sign() < 0 || !w.metadata(r.Label, 100, true) || !w.metadata(r.Provider, 100, true) || !requiredOneOf(r.State, "active", "paused") {
			h.configurationBad = true
			continue
		}
		h.versions[rev.Int64()] = requiredConfig{id, r.Label, r.Provider, r.State, r.LimitAmount, r.CreatedAt, rev.Int64()}
	}
	add := func(h *requiredAccountHistory, c requiredConfig, current fundinglotverify.ProcurementEvidenceAccountRow) {
		rev, ok := requiredInt64(current.Revision)
		if !ok || c.revision > rev.Int64() || c.id != current.ID || !c.created.Equal(current.CreatedAt) {
			h.configurationBad = true
		}
		if old, exists := h.versions[c.revision]; exists && !requiredConfigEqual(old, c) {
			h.configurationBad = true
		} else {
			h.versions[c.revision] = c
		}
	}
	for _, command := range x.commands {
		if err := w.tick(); err != nil {
			return err
		}
		if command.Kind != "account_create" && command.Kind != "account_configure" {
			continue
		}
		current, exists := x.accountRows[command.TargetID]
		if !exists {
			continue
		}
		h := histories[current.ID]
		before, beforeOK := requiredConfigSnapshot(w.snapshotDecode(command.Kind, command.Before))
		after, afterOK := requiredConfigSnapshot(w.snapshotDecode(command.Kind, command.After))
		if command.Kind == "account_create" && command.Status == "applied" {
			if afterOK {
				h.creates = append(h.creates, after)
				add(h, after, current)
			} else {
				h.configurationBad = true
			}
		} else if command.Kind == "account_configure" && command.Status == "applied" {
			if beforeOK {
				add(h, before, current)
			}
			if afterOK {
				add(h, after, current)
			}
			if !beforeOK || !afterOK {
				h.configurationBad = true
				continue
			}
			if !requiredConfigEqual(before, after) {
				h.changed = true
				if before.revision == after.revision-1 {
					h.edges[before.revision]++
					if h.edges[before.revision] > 1 {
						h.configurationBad = true
					}
				} else {
					h.configurationBad = true
				}
			}
		} else if command.Kind == "account_configure" && command.Status == "rejected" && command.Reason == "revision_conflict" && beforeOK {
			add(h, before, current)
		}
	}
	sourceCount := make(map[string]int)
	upperTotal := make(map[string]*big.Int)
	allReserved := make(map[string]bool)
	upperValid := make(map[string]bool)
	for _, p := range x.reservations {
		if err := w.tick(); err != nil {
			return err
		}
		sourceCount[p.AccountID]++
		if sourceCount[p.AccountID] == 1 {
			upperTotal[p.AccountID] = new(big.Int)
			allReserved[p.AccountID] = true
			upperValid[p.AccountID] = true
		}
		if n, ok := requiredUnsigned(p.UpperAmount); ok {
			upperTotal[p.AccountID].Add(upperTotal[p.AccountID], n)
		} else {
			allReserved[p.AccountID] = false
			upperValid[p.AccountID] = false
		}
		events := x.eventsByAttempt[p.AttemptID]
		if len(events) != 1 || events[0].Type != "reserved" {
			allReserved[p.AccountID] = false
		}
	}
	for id, current := range x.accountRows {
		if err := w.tick(); err != nil {
			return err
		}
		h := histories[id]
		createOK := len(h.creates) == 1
		var creation requiredConfig
		if createOK {
			creation = h.creates[0]
			createOK = creation.revision == 1 && creation.id == id && creation.created.Equal(current.CreatedAt)
		}
		if !createOK {
			w.diagnostics.AccountCreationRows++
		}
		revisions := make([]int64, 0, len(h.versions))
		for revision := range h.versions {
			if err := w.tick(); err != nil {
				return err
			}
			revisions = append(revisions, revision)
		}
		if err := w.sortRevisions(revisions); err != nil {
			return err
		}
		pauseGap := false
		if len(revisions) == 0 || revisions[0] != 1 {
			h.configurationBad = true
		}
		for i := 1; i < len(revisions); i++ {
			if err := w.tick(); err != nil {
				return err
			}
			before, after := h.versions[revisions[i-1]], h.versions[revisions[i]]
			if after.revision == before.revision+1 && h.edges[before.revision] == 1 {
				continue
			}
			automatic := after.revision == before.revision+1 && before.state == "active" && after.state == "paused" && before.label == after.label && before.provider == after.provider && before.limit == after.limit && before.id == after.id && before.created.Equal(after.created) && x.anomalyAccounts[id]
			if automatic {
				pauseGap = true
				h.changed = true
			} else {
				h.configurationBad = true
			}
		}
		if pauseGap {
			w.keys.AccountPauseOrdering.add(id)
		}
		if h.configurationBad {
			w.diagnostics.AccountConfigurationRows++
		}
		count := sourceCount[id]
		if count == 0 || !upperValid[id] {
			continue
		}
		constant := createOK && !h.configurationBad && !h.changed && !pauseGap && len(revisions) == 1 && creation.state == "active"
		provable := constant && (count == 1 || allReserved[id])
		if provable {
			limit, ok := requiredInt64(creation.limit)
			if !ok || upperTotal[id].Cmp(limit) > 0 {
				w.diagnostics.AccountAdmissionRows++
			}
		} else {
			w.keys.AccountAdmissionOrder.add(id)
		}
	}
	return nil
}
