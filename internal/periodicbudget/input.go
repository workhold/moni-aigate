package periodicbudget

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

const MaxConfigureBytes = 8192

func (in PutPeriodicBudget) Validate() error {
	if !ValidUUID(in.OperationID) || !ValidRevision(in.ExpectedRevision) ||
		!validPeriod(in.PeriodKind) || !ValidInt64AmountString(in.LimitAmount) {
		return ErrInvalidRequest
	}
	return nil
}

// ParseConfigure accepts exactly the five public fields, preserving strings and
// rejecting duplicate decoded keys, nulls, aliases and malformed Unicode.
func ParseConfigure(raw []byte) (PutPeriodicBudget, error) {
	var in PutPeriodicBudget
	if len(raw) > MaxConfigureBytes {
		return in, ErrRequestTooLarge
	}
	if !validJSONUnicode(raw) {
		return in, ErrInvalidRequest
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return in, ErrInvalidRequest
	}
	seen := make(map[string]bool, 5)
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return PutPeriodicBudget{}, ErrInvalidRequest
		}
		name, ok := token.(string)
		if !ok || seen[name] {
			return PutPeriodicBudget{}, ErrInvalidRequest
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return PutPeriodicBudget{}, ErrInvalidRequest
		}
		var destination any
		switch name {
		case "operation_id":
			destination = &in.OperationID
		case "expected_revision":
			destination = &in.ExpectedRevision
		case "period_kind":
			destination = &in.PeriodKind
		case "enabled":
			destination = &in.Enabled
		case "limit_amount":
			destination = &in.LimitAmount
		default:
			return PutPeriodicBudget{}, ErrInvalidRequest
		}
		if json.Unmarshal(value, destination) != nil {
			return PutPeriodicBudget{}, ErrInvalidRequest
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || len(seen) != 5 || d.Decode(new(any)) != io.EOF {
		return PutPeriodicBudget{}, ErrInvalidRequest
	}
	if in.Validate() != nil {
		return PutPeriodicBudget{}, ErrInvalidRequest
	}
	return in, nil
}

func (in *PutPeriodicBudget) UnmarshalJSON(raw []byte) error {
	parsed, err := ParseConfigure(raw)
	if err == nil {
		*in = parsed
	}
	return err
}

// encoding/json replaces isolated UTF-16 surrogates with U+FFFD. Check them
// before decoding so a malformed field name cannot be silently repaired.
func validJSONUnicode(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	inside := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inside = !inside
			continue
		}
		if !inside || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		unit, ok := jsonHexUnit(raw, i+1)
		if !ok {
			return false
		}
		i += 4
		if unit >= 0xdc00 && unit <= 0xdfff {
			return false
		}
		if unit < 0xd800 || unit > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return false
		}
		low, ok := jsonHexUnit(raw, i+3)
		if !ok || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func jsonHexUnit(raw []byte, at int) (uint16, bool) {
	if at+4 > len(raw) {
		return 0, false
	}
	var out uint16
	for _, b := range raw[at : at+4] {
		out <<= 4
		switch {
		case b >= '0' && b <= '9':
			out |= uint16(b - '0')
		case b >= 'a' && b <= 'f':
			out |= uint16(b - 'a' + 10)
		case b >= 'A' && b <= 'F':
			out |= uint16(b - 'A' + 10)
		default:
			return 0, false
		}
	}
	return out, true
}
