package periodicbudgetprefix

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"
)

type snapshotWindow struct{ id, start, end, held, spent string }
type snapshot struct {
	carry  string
	window *snapshotWindow
}

// JSON is checked before decoding because encoding/json replaces bad UTF-8
// and unpaired escaped surrogates. Duplicate keys are never last-key-wins.
func unicodeJSON(raw []byte) bool {
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
		if i+4 >= len(raw) {
			return false
		}
		v, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if v >= 0xdc00 && v <= 0xdfff {
			return false
		}
		if v < 0xd800 || v > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return false
		}
		low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if e != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func objectJSON(raw []byte, keys ...string) (map[string]json.RawMessage, bool) {
	if !unicodeJSON(raw) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, false
	}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		t, e = d.Token()
		k, ok := t.(string)
		if e != nil || !ok || !allowed[k] {
			return nil, false
		}
		if _, dup := out[k]; dup {
			return nil, false
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return nil, false
		}
		out[k] = v
	}
	t, e = d.Token()
	if e != nil || t != json.Delim('}') || len(out) != len(keys) || d.Decode(new(any)) != io.EOF {
		return nil, false
	}
	return out, true
}

func stringJSON(raw json.RawMessage) (string, bool) {
	var s string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func decodeSnapshot(raw []byte) (snapshot, bool) {
	if len(raw) > 8192 {
		return snapshot{}, false
	}
	f, ok := objectJSON(raw, "scope", "scope_id", "revision", "period_kind", "enabled", "limit_amount", "state", "effective_from", "tracking_started_at", "current_window", "carry_held", "as_of")
	if !ok {
		return snapshot{}, false
	}
	s := snapshot{}
	s.carry, ok = stringJSON(f["carry_held"])
	if !ok {
		return snapshot{}, false
	}
	if _, ok = amount(s.carry, false); !ok {
		return snapshot{}, false
	}
	if bytes.Equal(bytes.TrimSpace(f["current_window"]), []byte("null")) {
		return s, true
	}
	w, ok := objectJSON(f["current_window"], "id", "start_at", "end_at", "spent_amount", "held_amount")
	if !ok {
		return snapshot{}, false
	}
	win := &snapshotWindow{}
	for k, p := range map[string]*string{"id": &win.id, "start_at": &win.start, "end_at": &win.end, "held_amount": &win.held, "spent_amount": &win.spent} {
		*p, ok = stringJSON(w[k])
		if !ok {
			return snapshot{}, false
		}
	}
	if !uuid(win.id) {
		return snapshot{}, false
	}
	if _, ok = amount(win.held, false); !ok {
		return snapshot{}, false
	}
	if _, ok = amount(win.spent, false); !ok {
		return snapshot{}, false
	}
	s.window = win
	return s, true
}
