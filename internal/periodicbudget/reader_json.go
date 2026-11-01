package periodicbudget

import (
	"bytes"
	"encoding/json"
	"io"
)

func readObject(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	if !validJSONUnicode(raw) {
		return nil, ErrInvariant
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return nil, ErrInvariant
	}
	allowed := make(map[string]bool, len(keys))
	for _, k := range keys {
		allowed[k] = true
	}
	out := make(map[string]json.RawMessage, len(keys))
	for d.More() {
		tok, e = d.Token()
		if e != nil {
			return nil, ErrInvariant
		}
		key, ok := tok.(string)
		if !ok || !allowed[key] {
			return nil, ErrInvariant
		}
		if _, ok = out[key]; ok {
			return nil, ErrInvariant
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return nil, ErrInvariant
		}
		out[key] = v
	}
	tok, e = d.Token()
	if e != nil || tok != json.Delim('}') || len(out) != len(keys) || d.Decode(new(any)) != io.EOF {
		return nil, ErrInvariant
	}
	return out, nil
}
func readDecodeView(raw []byte) (PeriodicBudgetView, error) {
	var out PeriodicBudgetView
	fields, e := readObject(raw, "scope", "scope_id", "revision", "period_kind", "enabled", "limit_amount", "state", "effective_from", "tracking_started_at", "current_window", "carry_held", "as_of")
	if e != nil {
		return out, e
	}
	// NULL is legal only for the documented nullable configuration/window fields.
	for _, k := range []string{"scope", "scope_id", "revision", "enabled", "state", "carry_held", "as_of"} {
		if bytes.Equal(bytes.TrimSpace(fields[k]), []byte("null")) {
			return out, ErrInvariant
		}
	}
	if !bytes.Equal(bytes.TrimSpace(fields["current_window"]), []byte("null")) {
		if _, e = readObject(fields["current_window"], "id", "start_at", "end_at", "spent_amount", "held_amount"); e != nil {
			return out, e
		}
	}
	if json.Unmarshal(raw, &out) != nil || out.Validate() != nil {
		return PeriodicBudgetView{}, ErrInvariant
	}
	return out, nil
}
