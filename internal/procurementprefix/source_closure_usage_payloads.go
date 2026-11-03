package procurementprefix

import (
	"encoding/json"
	"io"
	"math/big"
	"strconv"
)

// Only the original persisted Usage shape is accepted. In particular, this is
// not a generic JSON oracle and does not call billing, a writer, or another
// replay domain to decide the expected usage relationships.
type sourceUsagePayload struct {
	status string
	counts [5]int64 // input, output, total, cached input, reasoning output
}

func sourceUsageField(name string) int {
	switch name {
	case "status":
		return 0
	case "input_tokens":
		return 1
	case "output_tokens":
		return 2
	case "total_tokens":
		return 3
	case "cached_tokens":
		return 4
	case "reasoning_tokens":
		return 5
	}
	return -1
}

func sourceReadUsagePayload(w *replayWork, raw string, confirmedOnly bool) bool {
	d := json.NewDecoder(&sourceJSONReader{w: w, raw: raw})
	d.UseNumber()
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	var fields uint8
	u := sourceUsagePayload{}
	for d.More() {
		if w.tick() != nil {
			return false
		}
		token, err = d.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return false
		}
		field := sourceUsageField(name)
		if field < 0 || fields&(1<<field) != 0 {
			return false
		}
		fields |= 1 << field
		token, err = d.Token()
		if err != nil {
			return false
		}
		if field == 0 {
			status, ok := token.(string)
			if !ok || !sourceOneOf(status, "confirmed_final", "provisional", "estimated", "missing", "conflicting") {
				return false
			}
			u.status = status
			continue
		}
		number, ok := token.(json.Number)
		if !ok || !unsignedInteger(string(number), 63, false) {
			return false
		}
		u.counts[field-1], _ = strconv.ParseInt(string(number), 10, 64)
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') || fields != 63 {
		return false
	}
	// Token reads the trailing boundary without materializing an arbitrary JSON
	// value supplied after the closed six-field object.
	if _, err = d.Token(); err != io.EOF {
		return false
	}
	if confirmedOnly && u.status != "confirmed_final" || u.counts[3] > u.counts[0] || u.counts[4] > u.counts[1] {
		return false
	}
	// Provisional/estimated/missing/conflicting totals keep their original
	// acceptance: only confirmed_final requires an exact sum. Big integers make
	// a sum above MaxInt64 a mismatch rather than allowing integer wraparound.
	return u.status != "confirmed_final" || new(big.Int).Add(big.NewInt(u.counts[0]), big.NewInt(u.counts[1])).Cmp(big.NewInt(u.counts[2])) == 0
}

func sourceParseUsagePayload(w *replayWork, raw string, confirmedOnly bool) (bool, error) {
	if w == nil || w.b.ctx == nil {
		return false, ErrInput
	}
	if err := w.b.check(); err != nil {
		return false, err
	}
	if len(raw) > int(fieldLimit) {
		return false, ErrResource
	}
	// Every accepted key and string value belongs to a closed ASCII vocabulary.
	// A malformed UTF16 escape cannot be repaired into an accepted member by the
	// decoder: its replacement rune fails that vocabulary. Invalid raw UTF8 and
	// NUL are rejected before decoding; decoded NUL also cannot match any member.
	valid := validText(raw) && sourceReadUsagePayload(w, raw, confirmedOnly)
	if err := w.b.check(); err != nil {
		return false, err
	}
	return valid, nil
}

func (s *sourceClosureState) usagePayloadRows() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	for _, rule := range []struct {
		table, column string
		nullable      bool
		confirmedOnly bool
	}{
		{table: "metering_events", column: "normalized_usage"},
		{table: "metering_event_conflicts", column: "normalized_usage"},
		{table: "reconciliation_cases", column: "proposed_usage", nullable: true, confirmedOnly: true},
	} {
		for _, raw := range s.tables[rule.table] {
			if err := s.w.tick(); err != nil {
				return err
			}
			payload := sourceText(raw.value, rule.column)
			if rule.nullable {
				value := sourceOptional(raw.value, rule.column)
				if value == nil {
					continue // SQL NULL is retained; JSON literal null is not a Usage.
				}
				payload = *value
			}
			valid, err := sourceParseUsagePayload(s.w, payload, rule.confirmedOnly)
			if err != nil {
				return err
			}
			if !valid {
				s.mark("scalar", rule.table, raw.key)
			}
		}
	}
	return s.w.b.check()
}
