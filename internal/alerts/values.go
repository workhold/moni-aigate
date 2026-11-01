package alerts

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const timeLayout = "2006-01-02T15:04:05.000000Z"
const maxInt64Text = "9223372036854775807"

func validID(v string) bool {
	u, e := uuid.Parse(v)
	return e == nil && u != uuid.Nil && u.String() == v
}
func validTime(t time.Time) bool {
	return t.UTC().Year() >= 1 && t.UTC().Year() <= 9999 && t.Nanosecond()%1000 == 0
}
func (t Time) MarshalJSON() ([]byte, error) {
	if !validTime(t.Time) {
		return nil, fail("alert_unavailable")
	}
	return json.Marshal(t.UTC().Format(timeLayout))
}
func (t *Time) UnmarshalJSON(b []byte) error {
	var v string
	if json.Unmarshal(b, &v) != nil {
		return fail("invalid_request")
	}
	at, e := time.Parse(timeLayout, v)
	if e != nil || !validTime(at) || at.UTC().Format(timeLayout) != v {
		return fail("invalid_request")
	}
	t.Time = at
	return nil
}
func canonical(v string, signed bool, digits int) bool {
	if v == "" {
		return false
	}
	if signed && v[0] == '-' {
		v = v[1:]
		if v == "0" {
			return false
		}
	}
	if len(v) < 1 || len(v) > digits || len(v) > 1 && v[0] == '0' {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func uintBound(v, max string, positive bool) bool {
	if !canonical(v, false, len(max)) || positive && v == "0" {
		return false
	}
	return len(v) < len(max) || len(v) == len(max) && v <= max
}
func validLabel(v string) bool {
	if !utf8.ValidString(v) || len(v) > 160 || utf8.RuneCountInString(v) < 1 || utf8.RuneCountInString(v) > 80 || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validKind(k Kind) bool {
	return k == ChannelQuarantined || k == ProcurementBudgetLow || k == CustomerReconciliationAged || k == ProcurementUnresolvedAged
}
func scopeMatches(k Kind, s ScopeType) bool {
	return k == ChannelQuarantined && s == "channel" || (k == ProcurementBudgetLow || k == ProcurementUnresolvedAged) && s == "procurement_account" || k == CustomerReconciliationAged && s == "tenant"
}

// The decoder preserves the distinction between omitted/null fields, rejects
// duplicate keys, and never accepts encoding/json's surrogate replacement.
func strictObject(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) || !validEscapes(raw) {
		return nil, fail("invalid_request")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, fail("invalid_request")
	}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		t, e = d.Token()
		k, ok := t.(string)
		if e != nil || !ok || !allowed[k] || out[k] != nil {
			return nil, fail("invalid_request")
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return nil, fail("invalid_request")
		}
		out[k] = v
	}
	if t, e = d.Token(); e != nil || t != json.Delim('}') || len(out) != len(keys) || d.Decode(new(any)) != io.EOF {
		return nil, fail("invalid_request")
	}
	return out, nil
}
func validEscapes(raw []byte) bool {
	quoted := false
	unit := func(i int) (uint64, error) {
		if i+4 > len(raw) {
			return 0, io.ErrUnexpectedEOF
		}
		return strconv.ParseUint(string(raw[i:i+4]), 16, 16)
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		v, e := unit(i + 1)
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
		next, e := unit(i + 3)
		if e != nil || next < 0xdc00 || next > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}
func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}
func decodeClosed(raw []byte, dst any, keys ...string) error {
	obj, e := strictObject(raw, keys...)
	if e != nil {
		return e
	}
	for _, v := range obj {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fail("invalid_request")
		}
	}
	if json.Unmarshal(raw, dst) != nil {
		return fail("invalid_request")
	}
	return nil
}

func (EmptyParameters) alertParameters()             {}
func (BudgetParameters) alertParameters()            {}
func (AgingParameters) alertParameters()             {}
func (EmptyParameters) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }
func (p BudgetParameters) MarshalJSON() ([]byte, error) {
	if !uintBound(p.ThresholdAmount, maxInt64Text, false) || !uintBound(p.RecoveryAmount, maxInt64Text, false) {
		return nil, fail("invalid_request")
	}
	a, _ := new(big.Int).SetString(p.ThresholdAmount, 10)
	b, _ := new(big.Int).SetString(p.RecoveryAmount, 10)
	if b.Cmp(a) < 0 {
		return nil, fail("invalid_request")
	}
	type wire BudgetParameters
	return encode(wire(p))
}
func (p AgingParameters) MarshalJSON() ([]byte, error) {
	if !uintBound(p.MinimumAgeSeconds, "604800", true) || !uintBound(p.TriggerCount, "1000", true) || !uintBound(p.RecoveryCount, "999", false) {
		return nil, fail("invalid_request")
	}
	age, _ := strconv.ParseInt(p.MinimumAgeSeconds, 10, 64)
	trigger, _ := strconv.ParseInt(p.TriggerCount, 10, 64)
	recover, _ := strconv.ParseInt(p.RecoveryCount, 10, 64)
	if age < 600 || recover >= trigger {
		return nil, fail("invalid_request")
	}
	type wire AgingParameters
	return encode(wire(p))
}
func DecodeParameters(k Kind, raw []byte) (Parameters, error) {
	if len(raw) > 512 {
		return nil, fail("invalid_request")
	}
	switch k {
	case ChannelQuarantined:
		if _, e := strictObject(raw); e != nil {
			return nil, e
		}
		return EmptyParameters{}, nil
	case ProcurementBudgetLow:
		var p BudgetParameters
		if e := decodeClosed(raw, &p, "threshold_amount", "recovery_amount"); e != nil {
			return nil, e
		}
		if _, e := p.MarshalJSON(); e != nil {
			return nil, e
		}
		return p, nil
	case CustomerReconciliationAged, ProcurementUnresolvedAged:
		var p AgingParameters
		if e := decodeClosed(raw, &p, "minimum_age_seconds", "trigger_count", "recovery_count"); e != nil {
			return nil, e
		}
		if _, e := p.MarshalJSON(); e != nil {
			return nil, e
		}
		return p, nil
	}
	return nil, fail("invalid_request")
}
func parameters(k Kind, p Parameters) ([]byte, error) {
	if p == nil {
		return nil, fail("invalid_request")
	}
	b, e := encode(p)
	if e != nil {
		return nil, fail("invalid_request")
	}
	decoded, e := DecodeParameters(k, b)
	if e != nil {
		return nil, e
	}
	return encode(decoded)
}
func (r RuleSnapshot) MarshalJSON() ([]byte, error) {
	if !validKind(r.Kind) || !scopeMatches(r.Kind, r.ScopeType) || !validID(r.ScopeID) || !validLabel(r.Label) {
		return nil, fail("alert_unavailable")
	}
	if _, e := parameters(r.Kind, r.Parameters); e != nil {
		return nil, e
	}
	type wire RuleSnapshot
	b, e := encode(wire(r))
	if len(b) > 1024 {
		return nil, fail("alert_unavailable")
	}
	return b, e
}
func (r *RuleSnapshot) UnmarshalJSON(raw []byte) error {
	if len(raw) > 1024 {
		return fail("alert_unavailable")
	}
	var v struct {
		Kind       Kind            `json:"kind"`
		ScopeType  ScopeType       `json:"scope_type"`
		ScopeID    ID              `json:"scope_id"`
		Label      string          `json:"label"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if e := decodeClosed(raw, &v, "kind", "scope_type", "scope_id", "label", "parameters"); e != nil {
		return e
	}
	p, e := DecodeParameters(v.Kind, v.Parameters)
	if e != nil {
		return e
	}
	*r = RuleSnapshot{v.Kind, v.ScopeType, v.ScopeID, v.Label, p}
	_, e = r.MarshalJSON()
	return e
}

func (ChannelSample) alertSample() {}
func (BudgetSample) alertSample()  {}
func (AgingSample) alertSample()   {}
func (v ChannelSample) MarshalJSON() ([]byte, error) {
	if v.Kind != ChannelQuarantined || !validTime(v.AsOf.Time) {
		return nil, fail("alert_unavailable")
	}
	type wire ChannelSample
	return encode(wire(v))
}
func (v BudgetSample) MarshalJSON() ([]byte, error) {
	if v.Kind != ProcurementBudgetLow || !validTime(v.AsOf.Time) || (v.AccountState != "active" && v.AccountState != "paused") || v.Currency != "CNY" || !uintBound(v.LimitAmount, maxInt64Text, false) || !canonical(v.HeldAmount, false, 100) || !canonical(v.SpentAmount, false, 100) || !canonical(v.AvailableAmount, true, 101) {
		return nil, fail("alert_unavailable")
	}
	limit, _ := new(big.Int).SetString(v.LimitAmount, 10)
	held, _ := new(big.Int).SetString(v.HeldAmount, 10)
	spent, _ := new(big.Int).SetString(v.SpentAmount, 10)
	available, _ := new(big.Int).SetString(v.AvailableAmount, 10)
	if limit.Sub(limit, held).Sub(limit, spent).Cmp(available) != 0 {
		return nil, fail("alert_unavailable")
	}
	type wire BudgetSample
	return encode(wire(v))
}
func (v AgingSample) MarshalJSON() ([]byte, error) {
	if (v.Kind != CustomerReconciliationAged && v.Kind != ProcurementUnresolvedAged) || !validTime(v.AsOf.Time) || !uintBound(v.MinimumAgeSeconds, "604800", true) || !uintBound(v.ObservedCount, "1001", false) {
		return nil, fail("alert_unavailable")
	}
	age, _ := strconv.ParseInt(v.MinimumAgeSeconds, 10, 64)
	if age < 600 || v.CountIsLowerBound != (v.ObservedCount == "1001") {
		return nil, fail("alert_unavailable")
	}
	if v.ObservedCount == "0" {
		if v.OldestCreatedAt != nil || v.OldestReferenceID != nil {
			return nil, fail("alert_unavailable")
		}
	} else if v.OldestCreatedAt == nil || v.OldestReferenceID == nil || !validID(*v.OldestReferenceID) || !validTime(v.OldestCreatedAt.Time) || v.OldestCreatedAt.After(v.AsOf.Add(-time.Duration(age)*time.Second)) {
		return nil, fail("alert_unavailable")
	}
	type wire AgingSample
	return encode(wire(v))
}
func decodeSample(raw []byte) (Sample, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > 2048 {
		return nil, fail("alert_unavailable")
	}
	var tag struct {
		Kind Kind `json:"kind"`
	}
	if json.Unmarshal(raw, &tag) != nil {
		return nil, fail("alert_unavailable")
	}
	var v Sample
	switch tag.Kind {
	case ChannelQuarantined:
		var x ChannelSample
		if e := decodeClosed(raw, &x, "kind", "as_of", "enabled", "quarantined"); e != nil {
			return nil, e
		}
		v = x
	case ProcurementBudgetLow:
		var x BudgetSample
		if e := decodeClosed(raw, &x, "kind", "as_of", "account_state", "limit_amount", "held_amount", "spent_amount", "available_amount", "currency"); e != nil {
			return nil, e
		}
		v = x
	case CustomerReconciliationAged, ProcurementUnresolvedAged:
		var x AgingSample
		obj, e := strictObject(raw, "kind", "as_of", "minimum_age_seconds", "observed_count", "count_is_lower_bound", "oldest_created_at", "oldest_reference_id")
		if e != nil {
			return nil, e
		}
		for k, b := range obj {
			if k != "oldest_created_at" && k != "oldest_reference_id" && bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
				return nil, fail("alert_unavailable")
			}
		}
		if json.Unmarshal(raw, &x) != nil {
			return nil, fail("alert_unavailable")
		}
		v = x
	default:
		return nil, fail("alert_unavailable")
	}
	if _, e := v.MarshalJSON(); e != nil {
		return nil, e
	}
	return v, nil
}

func digest(v any) string {
	b, e := encode(v)
	if e != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func RuleFilterDigest(f RuleFilter) string {
	return digest(struct {
		V       int        `json:"v"`
		Route   string     `json:"route"`
		State   *RuleState `json:"state"`
		Kind    *Kind      `json:"kind"`
		ScopeID *ID        `json:"scope_id"`
	}{1, "rules", f.State, f.Kind, f.ScopeID})
}
func EpisodeFilterDigest(f EpisodeFilter) string {
	return digest(struct {
		V      int     `json:"v"`
		Route  string  `json:"route"`
		Status *string `json:"status"`
		RuleID *ID     `json:"rule_id"`
		Kind   *Kind   `json:"kind"`
	}{1, "episodes", f.Status, f.RuleID, f.Kind})
}
func EventFilterDigest(id ID) string {
	return digest(struct {
		V       int    `json:"v"`
		Route   string `json:"route"`
		Episode ID     `json:"episode_id"`
	}{1, "events", id})
}
func validCursor(c *Cursor, want string) bool {
	if c == nil {
		return true
	}
	_, e := hex.DecodeString(c.FilterDigest)
	return c.V == 1 && validID(c.ID) && validTime(c.T.Time) && !c.T.IsZero() && len(c.FilterDigest) == 64 && e == nil && strings.ToLower(c.FilterDigest) == c.FilterDigest && c.FilterDigest == want
}
func EncodeCursor(c Cursor) (string, error) {
	if !validCursor(&c, c.FilterDigest) {
		return "", fail("invalid_request")
	}
	b, e := encode(c)
	if e != nil {
		return "", e
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	if len(s) > 512 {
		return "", fail("invalid_request")
	}
	return s, nil
}
func DecodeCursor(raw string) (*Cursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 512 {
		return nil, fail("invalid_request")
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil || base64.RawURLEncoding.EncodeToString(b) != raw {
		return nil, fail("invalid_request")
	}
	var c Cursor
	if e = decodeClosed(b, &c, "v", "t", "id", "filter_digest"); e != nil || !validCursor(&c, c.FilterDigest) {
		return nil, fail("invalid_request")
	}
	if _, e = hex.DecodeString(c.FilterDigest); e != nil || strings.ToLower(c.FilterDigest) != c.FilterDigest {
		return nil, fail("invalid_request")
	}
	return &c, nil
}
