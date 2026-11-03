package procurementprefix

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// This is the persisted Go field order. It deliberately does not import the
// routing or billing writer's validator, structs, or SQL identity codec.
type sourceRouteDecision struct {
	ChannelID string `json:"channel_id"`
	VersionID string `json:"version_id"`
	Priority  int64  `json:"priority"`
	Weight    uint64 `json:"weight"`
	Reason    string `json:"reason"`
}

type sourceJSONReader struct {
	w      *replayWork
	raw    string
	offset int
}

func (r *sourceJSONReader) Read(p []byte) (int, error) {
	if err := r.w.b.check(); err != nil {
		return 0, err
	}
	if r.offset == len(r.raw) {
		return 0, io.EOF
	}
	if len(p) > 256 {
		p = p[:256]
	}
	n := copy(p, r.raw[r.offset:])
	r.offset += n
	return n, nil
}

func sourceRouteReason(reason string) bool {
	return sourceOneOf(reason, "selected", "weighted_not_selected", "lower_priority", "disabled", "already_tried", "capacity_full", "quarantined", "circuit_open", "half_open_busy", "channel_quarantined", "credential_revoked", "binding_isolated", "region_forbidden", "cost_unknown", "cost_limit_exceeded", "higher_cost", "procurement_paused", "procurement_budget_exceeded", "procurement_bound_invalid", "procurement_budget_unavailable")
}

func sourceParseRoute(w *replayWork, raw, channel, version string) (canonical string, valid bool, err error) {
	if w == nil || w.b.ctx == nil {
		return "", false, ErrInput
	}
	if err = w.b.check(); err != nil {
		return "", false, err
	}
	if !validText(raw) {
		return "", false, nil
	}
	canonical, valid = sourceReadRoute(w, raw, channel, version)
	if err = w.b.check(); err != nil {
		return "", false, err
	}
	return canonical, valid, nil
}
func sourceReadRoute(w *replayWork, raw, channel, version string) (string, bool) {
	d := json.NewDecoder(&sourceJSONReader{w: w, raw: raw})
	d.UseNumber()
	token, err := d.Token()
	if err != nil || token != json.Delim('[') {
		return "", false
	}
	var out strings.Builder
	out.WriteByte('[')
	seen := make(keySet)
	selected, count := 0, 0
	for d.More() {
		if w.tick() != nil {
			return "", false
		}
		count++
		if count > 1000 {
			return "", false
		}
		token, err = d.Token()
		if err != nil || token != json.Delim('{') {
			return "", false
		}
		fields := make(keySet)
		row := sourceRouteDecision{}
		for d.More() {
			if w.tick() != nil {
				return "", false
			}
			token, err = d.Token()
			name, ok := token.(string)
			if err != nil || !ok {
				return "", false
			}
			if !sourceOneOf(name, "channel_id", "version_id", "priority", "weight", "reason") {
				return "", false
			}
			key := tupleKey(name)
			if _, duplicate := fields[key]; duplicate {
				return "", false
			}
			fields[key] = struct{}{}
			value, e := d.Token()
			if e != nil {
				return "", false
			}
			switch name {
			case "channel_id", "version_id", "reason":
				text, ok := value.(string)
				if !ok || !validText(text) {
					return "", false
				}
				switch name {
				case "channel_id":
					row.ChannelID = text
				case "version_id":
					row.VersionID = text
				case "reason":
					row.Reason = text
				}
			case "priority":
				n, ok := value.(json.Number)
				if !ok || !signedInteger(string(n), 64) {
					return "", false
				}
				row.Priority, _ = strconv.ParseInt(string(n), 10, 64)
			case "weight":
				n, ok := value.(json.Number)
				if !ok || !unsignedInteger(string(n), 64, true) {
					return "", false
				}
				row.Weight, _ = strconv.ParseUint(string(n), 10, 64)
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim('}') || len(fields) != 5 {
			return "", false
		}
		if _, err := uuid.Parse(row.ChannelID); err != nil {
			return "", false
		}
		if _, err := uuid.Parse(row.VersionID); err != nil {
			return "", false
		}
		channelKey := tupleKey(row.ChannelID)
		if _, duplicate := seen[channelKey]; duplicate {
			return "", false
		}
		seen[channelKey] = struct{}{}
		if !sourceRouteReason(row.Reason) {
			return "", false
		}
		if row.Reason == "selected" {
			selected++
			if row.ChannelID != channel || row.VersionID != version {
				return "", false
			}
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return "", false
		}
		if count > 1 {
			out.WriteByte(',')
		}
		out.Write(encoded)
		// The closing ']' is part of the original Go business byte bound.
		if out.Len()+1 > 262144 {
			return "", false
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim(']') || count > 0 && selected != 1 {
		return "", false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return "", false
	}
	out.WriteByte(']')
	return out.String(), true
}

func (s *sourceClosureState) routeRows() error {
	for _, table := range []string{"upstream_attempts", "pp_attempt_origins"} {
		for _, raw := range s.tables[table] {
			if err := s.w.tick(); err != nil {
				return err
			}
			_, valid, err := sourceParseRoute(s.w, sourceText(raw.value, "route_decisions"), sourceText(raw.value, "channel_id"), sourceText(raw.value, "channel_version_id"))
			if err != nil {
				return err
			}
			if !valid {
				s.mark("scalar", table, raw.key)
			}
		}
	}
	return nil
}
