package fundinglotverify

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"
)

func pbJSONUnicode(raw []byte) bool {
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
		v, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
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
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}
func pbObject(raw []byte, keys ...string) (map[string]json.RawMessage, bool) {
	if !pbJSONUnicode(raw) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return nil, false
	}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		tok, e = d.Token()
		key, ok := tok.(string)
		if e != nil || !ok || !allowed[key] {
			return nil, false
		}
		if _, dup := out[key]; dup {
			return nil, false
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return nil, false
		}
		out[key] = v
	}
	tok, e = d.Token()
	if e != nil || tok != json.Delim('}') || len(out) != len(keys) || d.Decode(new(any)) != io.EOF {
		return nil, false
	}
	return out, true
}
func pbJSONString(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}
func pbJSONBool(raw json.RawMessage) (bool, bool) {
	var b bool
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &b) != nil {
		return false, false
	}
	return b, true
}

type pbGraphInput struct {
	expected, period, limit string
	enabled                 bool
}

func pbDecodeInput(raw []byte) (pbGraphInput, bool) {
	f, ok := pbObject(raw, "expected_revision", "period_kind", "enabled", "limit_amount")
	if !ok {
		return pbGraphInput{}, false
	}
	var in pbGraphInput
	var a, b, c, d bool
	in.expected, a = pbJSONString(f["expected_revision"])
	in.period, b = pbJSONString(f["period_kind"])
	in.enabled, c = pbJSONBool(f["enabled"])
	in.limit, d = pbJSONString(f["limit_amount"])
	_, rev := pbCanonical(in.expected, false, 19, "9223372036854775807")
	_, amt := pbCanonical(in.limit, false, 19, "9223372036854775807")
	return in, a && b && c && d && rev && amt && pbPeriod(in.period)
}
func pbFingerprint(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(p)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func pbCommandTarget(c PeriodicBudgetControlRow) (kind, scope, target string, ok bool) {
	id, ok := pbTarget(c)
	if !ok {
		return "", "", "", false
	}
	scope = "platform"
	switch c.Scope {
	case "tenant":
		kind = "tenant_periodic_budget.configure"
		target = "/api/platform/tenants/" + id + "/periodic-budget"
	case "key":
		kind = "key_periodic_budget.configure"
		scope = *c.TenantID
		target = "/api/keys/" + id + "/periodic-budget"
	case "procurement":
		kind = "procurement_periodic_budget.configure"
		target = "/api/platform/procurement-accounts/" + id + "/periodic-budget"
	}
	return kind, scope, target, true
}

const pbGraphUTC6 = "2006-01-02T15:04:05.000000Z"

func pbParseTime(s string) (time.Time, bool) {
	if len(s) != 27 || s[:4] == "0000" {
		return time.Time{}, false
	}
	t, e := time.Parse(pbGraphUTC6, s)
	return t, e == nil && t.Year() >= 1 && t.Year() <= 9999 && t.Format(pbGraphUTC6) == s
}
func (w *pbGraphWork) commands() {
	m := &w.out.Mismatches
	groups := map[string][]PeriodicBudgetVersionRow{}
	for key, v := range w.versions {
		if !w.tick() {
			return
		}
		if w.active(pbGraphObject{"versions", key.a, key.b}) {
			groups[v.ControlID] = append(groups[v.ControlID], v)
		}
	}
	for cid, versions := range groups {
		sort.Slice(versions, func(i, j int) bool {
			w.tick()
			a, ao := pbCanonical(versions[i].Revision, false, 19, "9223372036854775807")
			b, bo := pbCanonical(versions[j].Revision, false, 19, "9223372036854775807")
			if ao && bo && a.Cmp(b) != 0 {
				return a.Cmp(b) < 0
			}
			if ao != bo {
				return ao
			}
			return versions[i].Revision < versions[j].Revision
		})
		if w.err != nil {
			return
		}
		groups[cid] = versions
		c, cok := w.controls[cid]
		for i, v := range versions {
			if !w.tick() {
				return
			}
			o := pbGraphObject{"versions", cid, v.Revision}
			op, ok := w.ops[v.OperationID]
			_, uok := w.users[v.Actor]
			bad := !cok || !ok || !uok || v.Revision != strconv.Itoa(i+1) || v.PeriodKind != c.PeriodKind || op.ResourceID != cid || op.Revision != v.Revision || op.Actor != v.Actor || op.CreatedXID != v.CreatedXID || !pbTimeEq(op.CreatedAt, v.CreatedAt) || i == 0 && v.CreatedXID != c.CreatedXID
			if i > 0 && v.Enabled == versions[i-1].Enabled && v.LimitAmount == versions[i-1].LimitAmount {
				bad = true
			}
			if rev, ok := pbCanonical(v.Revision, false, 19, "9223372036854775807"); ok {
				if current, cok := pbCanonical(c.Revision, false, 19, "9223372036854775807"); cok && rev.Cmp(current) > 0 {
					bad = true
				}
			}
			if bad {
				w.mark(&m.VersionMismatches, o)
			}
		}
	}
	for id, c := range w.controls {
		if !w.tick() {
			return
		}
		o := pbObj("controls", id)
		if !w.active(o) {
			continue
		}
		target, shape := pbTarget(c)
		bad := !shape || !pbPeriod(c.PeriodKind)
		if shape {
			switch c.Scope {
			case "tenant":
				_, ok := w.tenants[target]
				bad = bad || !ok
			case "key":
				k, ok := w.keys[target]
				bad = bad || !ok || !pbString(c.TenantID, k.TenantID)
			case "procurement":
				_, ok := w.accounts[target]
				bad = bad || !ok
			}
		}
		if !pbFinite(c.TrackingStartedAt) || !pbFinite(c.EffectiveFrom) {
			bad = true
		} else if pbPeriod(c.PeriodKind) && !c.EffectiveFrom.Value.Equal(pbEnd(c.PeriodKind, pbStart(c.PeriodKind, c.TrackingStartedAt.Value))) {
			bad = true
		}
		vs := groups[id]
		if len(vs) == 0 {
			bad = true
		} else {
			v := vs[len(vs)-1]
			if v.Revision != c.Revision || v.PeriodKind != c.PeriodKind || v.Enabled != c.Enabled || v.LimitAmount != c.LimitAmount || v.OperationID != c.ConfigurationOperationID {
				bad = true
			}
		}
		if bad {
			w.mark(&m.ConfigurationMismatches, o)
		}
	}
	zeroHeld := map[string]bool{}
	for id := range w.controls {
		zeroHeld[id] = true
	}
	for id, e := range w.events {
		if !w.tick() {
			return
		}
		if !w.valid(pbObj("events", id)) || e.HeldDelta != "0" {
			zeroHeld[e.ControlID] = false
		}
	}
	for id, sum := range w.controlReplay {
		if !sum.valid {
			zeroHeld[id] = false
		}
	}
	for id, op := range w.ops {
		if !w.tick() {
			return
		}
		o := pbObj("operations", id)
		c, cok := w.controls[op.ResourceID]
		v, vok := w.versions[pbGraphPair{op.ResourceID, op.Revision}]
		_, uok := w.users[op.Actor]
		in, iok := pbDecodeInput(op.SafeInput)
		kind, scope, target, tok := pbCommandTarget(c)
		changedKey, changed := w.versionOps[id]
		changed = changed && w.active(pbGraphObject{"versions", changedKey.a, changedKey.b})
		bad := !cok || !vok || !uok || !iok || !tok || len(op.Target) > 200 || op.Kind != kind || op.Scope != scope || op.Target != target || in.period != v.PeriodKind || in.enabled != v.Enabled || in.limit != v.LimitAmount
		if rev, ok := pbCanonical(op.Revision, false, 19, "9223372036854775807"); ok {
			if changed {
				rev.Sub(rev, big.NewInt(1))
			}
			if in.expected != rev.String() {
				bad = true
			}
		} else {
			bad = true
		}
		if changed && (changedKey.a != op.ResourceID || changedKey.b != op.Revision) {
			bad = true
		}
		if iok && pbFingerprint(op.Kind, op.Actor, op.Scope, op.Target, in.expected, in.period, strconv.FormatBool(in.enabled), in.limit) != op.InputFingerprint {
			bad = true
		}
		if bad {
			w.mark(&m.OperationMismatches, o)
		}
		if !w.snapshot(op, c, v, cok && vok, zeroHeld[c.ID]) {
			w.mark(&m.SnapshotMismatches, o)
		}
		if aid, ok := w.auditTargets[id]; !ok || !w.active(pbObj("audits", aid)) {
			w.mark(&m.AuditMismatches, pbObj("missing_audit", id))
		}
	}
	for id, a := range w.audits {
		if !w.tick() {
			return
		}
		if a.Action != "periodic_budget.configured" || !w.active(pbObj("audits", id)) {
			continue
		}
		op, ok := w.ops[a.Target]
		c, cok := w.controls[op.ResourceID]
		target, shape := pbTarget(c)
		vkey, changed := w.versionOps[op.OperationID]
		changed = changed && w.active(pbGraphObject{"versions", vkey.a, vkey.b})
		diff, dok := pbObject(a.SanitizedDiff, "control_id", "revision", "scope", "scope_id", "changed")
		expect := map[string]string{"control_id": c.ID, "revision": op.Revision, "scope": c.Scope, "scope_id": target}
		bad := !ok || !cok || !shape || !dok || a.Actor != op.Actor || a.RequestID != "" || !pbTimeEq(a.CreatedAt, op.CreatedAt)
		if c.Scope == "key" {
			bad = bad || !pbSame(a.TenantScope, c.TenantID)
		} else {
			bad = bad || a.TenantScope != nil
		}
		if dok {
			for key, value := range expect {
				got, valid := pbJSONString(diff[key])
				if !valid || got != value {
					bad = true
				}
			}
			got, valid := pbJSONBool(diff["changed"])
			if !valid || got != changed {
				bad = true
			}
		}
		if bad {
			w.mark(&m.AuditMismatches, pbObj("audits", id))
		}
	}
}

func (w *pbGraphWork) snapshot(op PeriodicBudgetOperationRow, c PeriodicBudgetControlRow, v PeriodicBudgetVersionRow, refs, zero bool) bool {
	if len(op.ResultSnapshot) > 8192 {
		return false
	}
	f, ok := pbObject(op.ResultSnapshot, "scope", "scope_id", "revision", "period_kind", "enabled", "limit_amount", "state", "effective_from", "tracking_started_at", "current_window", "carry_held", "as_of")
	if !ok || !refs || !pbFinite(op.CreatedAt) || !pbFinite(c.TrackingStartedAt) || !pbFinite(c.EffectiveFrom) || !pbPeriod(c.PeriodKind) {
		return false
	}
	target, shape := pbTarget(c)
	if !shape {
		return false
	}
	if n, valid := pbCanonical(op.Revision, false, 19, "9223372036854775807"); !valid || n.Sign() == 0 {
		return false
	}
	if _, valid := pbCanonical(v.LimitAmount, false, 19, "9223372036854775807"); !valid || !pbPeriod(v.PeriodKind) {
		return false
	}
	stringsExpected := map[string]string{"scope": c.Scope, "scope_id": target, "revision": op.Revision, "period_kind": v.PeriodKind, "limit_amount": v.LimitAmount, "effective_from": c.EffectiveFrom.Value.UTC().Format(pbGraphUTC6), "tracking_started_at": c.TrackingStartedAt.Value.UTC().Format(pbGraphUTC6), "as_of": op.CreatedAt.Value.UTC().Format(pbGraphUTC6)}
	for key, want := range stringsExpected {
		got, valid := pbJSONString(f[key])
		if !valid || got != want {
			return false
		}
		if pbEnum(key, "effective_from", "tracking_started_at", "as_of") {
			if _, valid = pbParseTime(got); !valid {
				return false
			}
		}
	}
	enabled, ok := pbJSONBool(f["enabled"])
	if !ok || enabled != v.Enabled {
		return false
	}
	state := "active"
	if !v.Enabled {
		state = "disabled"
	} else if op.CreatedAt.Value.Before(c.EffectiveFrom.Value) {
		state = "scheduled"
	}
	got, ok := pbJSONString(f["state"])
	if !ok || got != state {
		return false
	}
	carry, ok := pbJSONString(f["carry_held"])
	if !ok {
		return false
	}
	if _, ok = pbCanonical(carry, false, 100, ""); !ok {
		return false
	}
	start := pbStart(c.PeriodKind, op.CreatedAt.Value)
	if start.Before(pbStart(c.PeriodKind, c.TrackingStartedAt.Value)) {
		return false
	}
	wid, exists := w.windowStarts[pbGraphWindowKey{c.ID, start}]
	exists = exists && w.active(pbObj("windows", wid))
	null := bytes.Equal(bytes.TrimSpace(f["current_window"]), []byte("null"))
	if !null {
		wf, valid := pbObject(f["current_window"], "id", "start_at", "end_at", "spent_amount", "held_amount")
		if !valid || !exists {
			return false
		}
		win := w.windows[wid]
		if !pbFinite(win.StartAt) || !pbFinite(win.EndAt) || win.ControlID != c.ID || !pbUUID(wid) || !pbContains(win, op.CreatedAt) {
			return false
		}
		expect := map[string]string{"id": wid, "start_at": win.StartAt.Value.UTC().Format(pbGraphUTC6), "end_at": win.EndAt.Value.UTC().Format(pbGraphUTC6)}
		if !pbFinite(win.StartAt) || !pbFinite(win.EndAt) || win.ControlID != c.ID {
			return false
		}
		for key, want := range expect {
			s, valid := pbJSONString(wf[key])
			if !valid || s != want {
				return false
			}
			if key != "id" {
				if _, valid = pbParseTime(s); !valid {
					return false
				}
			}
		}
		for _, key := range []string{"spent_amount", "held_amount"} {
			s, valid := pbJSONString(wf[key])
			if !valid {
				return false
			}
			if _, valid = pbCanonical(s, false, 100, ""); !valid {
				return false
			}
		}
	}
	if !exists && zero {
		if !null || carry != "0" {
			return false
		}
		return true
	}
	w.mark(&w.out.Unproven.OperationSnapshotPrefixUnproven, pbObj("operations", op.OperationID))
	return true
}
