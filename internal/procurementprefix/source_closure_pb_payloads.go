package procurementprefix

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"time"
)

const sourcePBUTC6 = "2006-01-02T15:04:05.000000Z"

// These are the original PB wire shapes, independently decoded here. Ordinary
// procurement snapshots do not contain booleans and keep their own parser.
type sourcePBValue struct {
	kind    byte
	text    string
	boolean bool
	object  map[string]sourcePBValue
}

type sourcePBInput struct {
	expected, period, limit string
	enabled                 bool
}

type sourcePBWindow struct {
	id, held, spent string
	start, end      time.Time
}

type sourcePBSnapshot struct {
	scope, scopeID, revision, period, limit, state, carry string
	enabled                                               bool
	effective, tracking, at                               time.Time
	window                                                *sourcePBWindow
}

func sourcePBReadValue(w *replayWork, d *json.Decoder, depth int, nodes *int) (sourcePBValue, bool) {
	if w.tick() != nil || depth > 2 || *nodes >= 32 {
		return sourcePBValue{}, false
	}
	*nodes++
	token, err := d.Token()
	if err != nil {
		return sourcePBValue{}, false
	}
	switch value := token.(type) {
	case nil:
		return sourcePBValue{kind: '0'}, true
	case string:
		return sourcePBValue{kind: 's', text: value}, validText(value)
	case bool:
		return sourcePBValue{kind: 'b', boolean: value}, true
	case json.Delim:
		if value != '{' || depth > 1 {
			return sourcePBValue{}, false
		}
		out := sourcePBValue{kind: '{', object: make(map[string]sourcePBValue)}
		for d.More() {
			if w.tick() != nil || len(out.object) >= 12 {
				return sourcePBValue{}, false
			}
			token, err = d.Token()
			name, ok := token.(string)
			if err != nil || !ok || !validText(name) {
				return sourcePBValue{}, false
			}
			if _, duplicate := out.object[name]; duplicate {
				return sourcePBValue{}, false
			}
			child, ok := sourcePBReadValue(w, d, depth+1, nodes)
			if !ok {
				return sourcePBValue{}, false
			}
			out.object[name] = child
		}
		if last, err := d.Token(); err != nil || last != json.Delim('}') {
			return sourcePBValue{}, false
		}
		return out, true
	default:
		// PB has no JSON number fields, arrays or other nested wire shapes.
		return sourcePBValue{}, false
	}
}

func sourcePBParse(w *replayWork, raw string, snapshot bool) (sourcePBValue, bool, error) {
	if w == nil || w.b.ctx == nil {
		return sourcePBValue{}, false, ErrInput
	}
	if err := w.b.check(); err != nil {
		return sourcePBValue{}, false, err
	}
	if len(raw) > int(fieldLimit) {
		return sourcePBValue{}, false, ErrResource
	}
	// Only result_snapshot had the original 8192-byte persisted JSON limit.
	// Do not transfer the HTTP request-body limit to old safe_input values.
	if snapshot && len(raw) > 8192 {
		return sourcePBValue{}, false, nil
	}
	if !sourcePayloadUnicode(w, raw) {
		return sourcePBValue{}, false, w.b.check()
	}
	d := json.NewDecoder(&sourceJSONReader{w: w, raw: raw})
	d.UseNumber()
	nodes := 0
	value, valid := sourcePBReadValue(w, d, 0, &nodes)
	if valid {
		_, err := d.Token()
		valid = err == io.EOF
	}
	if err := w.b.check(); err != nil {
		return sourcePBValue{}, false, err
	}
	if !valid {
		return sourcePBValue{}, false, nil
	}
	return value, true, nil
}

func sourcePBMembers(value sourcePBValue, names ...string) bool {
	if value.kind != '{' || len(value.object) != len(names) {
		return false
	}
	for _, name := range names {
		if _, exists := value.object[name]; !exists {
			return false
		}
	}
	return true
}

func sourcePBString(value sourcePBValue, name string) (string, bool) {
	field, exists := value.object[name]
	return field.text, exists && field.kind == 's'
}

func sourcePBDecodeInput(value sourcePBValue) (sourcePBInput, bool) {
	if !sourcePBMembers(value, "expected_revision", "period_kind", "enabled", "limit_amount") || value.object["enabled"].kind != 'b' {
		return sourcePBInput{}, false
	}
	out := sourcePBInput{enabled: value.object["enabled"].boolean}
	for name, target := range map[string]*string{"expected_revision": &out.expected, "period_kind": &out.period, "limit_amount": &out.limit} {
		var valid bool
		*target, valid = sourcePBString(value, name)
		if !valid {
			return sourcePBInput{}, false
		}
	}
	return out, unsignedInteger(out.expected, 63, false) && unsignedInteger(out.limit, 63, false) && sourceOneOf(out.period, "utc_day", "utc_month")
}

func sourcePBTime(text string) (time.Time, bool) {
	if len(text) != 27 || text[:4] == "0000" {
		return time.Time{}, false
	}
	at, err := time.Parse(sourcePBUTC6, text)
	return at, err == nil && at.Year() >= 1 && at.Year() <= 9999 && at.Format(sourcePBUTC6) == text
}

func sourcePBPeriodStart(period string, at time.Time) time.Time {
	at = at.UTC()
	day := at.Day()
	if period == "utc_month" {
		day = 1
	}
	return time.Date(at.Year(), at.Month(), day, 0, 0, 0, 0, time.UTC)
}

func sourcePBPeriodEnd(period string, start time.Time) time.Time {
	if period == "utc_month" {
		return start.AddDate(0, 1, 0)
	}
	return start.AddDate(0, 0, 1)
}

func sourcePBDecodeWindow(value sourcePBValue) (*sourcePBWindow, bool) {
	if !sourcePBMembers(value, "id", "start_at", "end_at", "spent_amount", "held_amount") {
		return nil, false
	}
	var id, start, end, spent, held string
	for name, target := range map[string]*string{"id": &id, "start_at": &start, "end_at": &end, "spent_amount": &spent, "held_amount": &held} {
		var valid bool
		*target, valid = sourcePBString(value, name)
		if !valid {
			return nil, false
		}
	}
	startAt, validStart := sourcePBTime(start)
	endAt, validEnd := sourcePBTime(end)
	_, validHeld := numeric(held, false)
	_, validSpent := numeric(spent, false)
	if !sourceOriginalUUID(id, true) || !validStart || !validEnd || !validHeld || !validSpent {
		return nil, false
	}
	return &sourcePBWindow{id: id, start: startAt, end: endAt, held: held, spent: spent}, true
}

func sourcePBDecodeSnapshot(value sourcePBValue) (sourcePBSnapshot, bool) {
	if !sourcePBMembers(value, "scope", "scope_id", "revision", "period_kind", "enabled", "limit_amount", "state", "effective_from", "tracking_started_at", "current_window", "carry_held", "as_of") || value.object["enabled"].kind != 'b' {
		return sourcePBSnapshot{}, false
	}
	out := sourcePBSnapshot{enabled: value.object["enabled"].boolean}
	var effective, tracking, at string
	for name, target := range map[string]*string{"scope": &out.scope, "scope_id": &out.scopeID, "revision": &out.revision, "period_kind": &out.period, "limit_amount": &out.limit, "state": &out.state, "effective_from": &effective, "tracking_started_at": &tracking, "carry_held": &out.carry, "as_of": &at} {
		var valid bool
		*target, valid = sourcePBString(value, name)
		if !valid {
			return sourcePBSnapshot{}, false
		}
	}
	_, validCarry := numeric(out.carry, false)
	if !sourceOneOf(out.scope, "tenant", "key", "procurement") || !sourceOriginalUUID(out.scopeID, true) || !unsignedInteger(out.revision, 63, true) || !sourceOneOf(out.period, "utc_day", "utc_month") || !unsignedInteger(out.limit, 63, false) || !validCarry {
		return sourcePBSnapshot{}, false
	}
	var valid bool
	if out.effective, valid = sourcePBTime(effective); !valid {
		return sourcePBSnapshot{}, false
	}
	if out.tracking, valid = sourcePBTime(tracking); !valid {
		return sourcePBSnapshot{}, false
	}
	if out.at, valid = sourcePBTime(at); !valid {
		return sourcePBSnapshot{}, false
	}
	start := sourcePBPeriodStart(out.period, out.at)
	trackingStart := sourcePBPeriodStart(out.period, out.tracking)
	if !out.effective.Equal(sourcePBPeriodEnd(out.period, trackingStart)) || start.Before(trackingStart) {
		return sourcePBSnapshot{}, false
	}
	state := "disabled"
	if out.enabled {
		state = "active"
		if out.at.Before(out.effective) {
			state = "scheduled"
		}
	}
	if out.state != state {
		return sourcePBSnapshot{}, false
	}
	if window := value.object["current_window"]; window.kind != '0' {
		out.window, valid = sourcePBDecodeWindow(window)
		if !valid || !out.window.start.Equal(start) || !out.window.end.Equal(sourcePBPeriodEnd(out.period, start)) {
			return sourcePBSnapshot{}, false
		}
	}
	return out, true
}

func sourcePBFingerprint(w *replayWork, row PeriodicBudgetOperationRow, input sourcePBInput) (string, error) {
	if w == nil || w.b.ctx == nil {
		return "", ErrInput
	}
	if err := w.b.check(); err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, value := range []string{row.Kind, row.Actor, row.Scope, row.Target, input.expected, input.period, strconv.FormatBool(input.enabled), input.limit} {
		if len(value) > int(fieldLimit) {
			return "", ErrResource
		}
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(value)))
		_, _ = hash.Write(length[:])
		for len(value) > 0 {
			chunk := value[:min(len(value), 256)]
			for range len(chunk) {
				if err := w.tick(); err != nil {
					return "", err
				}
			}
			_, _ = hash.Write([]byte(chunk))
			value = value[len(chunk):]
		}
	}
	if err := w.b.check(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func sourcePBOperation(w *replayWork, row PeriodicBudgetOperationRow) (bool, error) {
	inputValue, inputParsed, err := sourcePBParse(w, row.SafeInput, false)
	if err != nil {
		return false, err
	}
	snapshotValue, snapshotParsed, err := sourcePBParse(w, row.ResultSnapshot, true)
	if err != nil {
		return false, err
	}
	input, inputValid := sourcePBDecodeInput(inputValue)
	snapshot, snapshotValid := sourcePBDecodeSnapshot(snapshotValue)
	if !inputParsed || !snapshotParsed || !inputValid || !snapshotValid || !validText(row.Actor) || !finite(row.CreatedAt) || row.Revision != snapshot.revision || !row.CreatedAt.Time.Equal(snapshot.at) || input.period != snapshot.period || input.enabled != snapshot.enabled || input.limit != snapshot.limit {
		return false, w.b.check()
	}
	revision, _ := strconv.ParseUint(snapshot.revision, 10, 63)
	expected, _ := strconv.ParseUint(input.expected, 10, 63)
	// UINT64 arithmetic is wider than both positive BIGINT values. It cannot
	// wrap a MaxInt64 input into a seemingly valid changed receipt.
	if revision != expected && revision != expected+1 {
		return false, w.b.check()
	}
	kind, scope, target := snapshot.scope+"_periodic_budget.configure", "platform", ""
	switch snapshot.scope {
	case "tenant":
		target = "/api/platform/tenants/" + snapshot.scopeID + "/periodic-budget"
	case "procurement":
		target = "/api/platform/procurement-accounts/" + snapshot.scopeID + "/periodic-budget"
	case "key":
		target, scope = "/api/keys/"+snapshot.scopeID+"/periodic-budget", row.Scope
		if !sourceOriginalUUID(scope, true) {
			return false, w.b.check()
		}
	}
	if row.Kind != kind || row.Scope != scope || row.Target != target {
		return false, w.b.check()
	}
	// Actor is the original TEXT identity. The SQL writer frames those exact
	// bytes; public new-command UUID validation is not a historical constraint.
	fingerprint, err := sourcePBFingerprint(w, row, input)
	if err != nil {
		return false, err
	}
	return fingerprint == row.InputFingerprint, w.b.check()
}

func (s *sourceClosureState) pbPayloadRows() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	for _, raw := range s.tables["periodic_budget_operations"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		valid, err := sourcePBOperation(s.w, raw.value.Interface().(PeriodicBudgetOperationRow))
		if err != nil {
			return err
		}
		if !valid {
			s.mark("scalar", "periodic_budget_operations", raw.key)
		}
	}
	return s.w.b.check()
}
