package fundinglotverify

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const sandboxGraphTimeLayout = "2006-01-02T15:04:05.000000Z"

type sandboxGraphRequest struct {
	SchemaVersion   int    `json:"schema_version"`
	EnvironmentID   string `json:"environment_id"`
	MerchantID      string `json:"merchant_id"`
	QueryID         string `json:"query_id,omitempty"`
	CommandID       string `json:"command_id"`
	MerchantOrderID string `json:"merchant_order_id"`
	CommandKind     string `json:"command_kind,omitempty"`
	ParameterDigest string `json:"parameter_digest,omitempty"`
	Amount          string `json:"amount,omitempty"`
	Currency        string `json:"currency,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
}

// Separate wire structures fix the original key order and required field sets.
type sandboxGraphCheckoutWire struct {
	SchemaVersion   int    `json:"schema_version"`
	EnvironmentID   string `json:"environment_id"`
	MerchantID      string `json:"merchant_id"`
	CommandID       string `json:"command_id"`
	MerchantOrderID string `json:"merchant_order_id"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	CreatedAt       string `json:"created_at"`
	ExpiresAt       string `json:"expires_at"`
}
type sandboxGraphCancelWire struct {
	SchemaVersion   int    `json:"schema_version"`
	EnvironmentID   string `json:"environment_id"`
	MerchantID      string `json:"merchant_id"`
	CommandID       string `json:"command_id"`
	MerchantOrderID string `json:"merchant_order_id"`
}
type sandboxGraphQueryWire struct {
	SchemaVersion   int    `json:"schema_version"`
	EnvironmentID   string `json:"environment_id"`
	MerchantID      string `json:"merchant_id"`
	QueryID         string `json:"query_id"`
	CommandID       string `json:"command_id"`
	MerchantOrderID string `json:"merchant_order_id"`
	CommandKind     string `json:"command_kind"`
	ParameterDigest string `json:"parameter_digest"`
}
type sandboxGraphCaptureWire struct {
	ProviderTransactionID string `json:"provider_transaction_id"`
	Amount                string `json:"amount"`
	Currency              string `json:"currency"`
	OccurredAt            string `json:"occurred_at"`
	ProviderOrderRevision string `json:"provider_order_revision"`
}
type sandboxGraphResult struct {
	Status      string                   `json:"status"`
	CheckoutRef *string                  `json:"checkout_ref"`
	FailureCode *string                  `json:"failure_code"`
	Capture     *sandboxGraphCaptureWire `json:"capture"`
	ClosedAt    *string                  `json:"closed_at"`
}
type sandboxGraphReply struct {
	SchemaVersion         int                 `json:"schema_version"`
	QueryID               string              `json:"query_id"`
	EnvironmentID         string              `json:"environment_id"`
	MerchantID            string              `json:"merchant_id"`
	MerchantOrderID       string              `json:"merchant_order_id"`
	CommandID             string              `json:"command_id"`
	CommandKind           string              `json:"command_kind"`
	ParameterDigest       string              `json:"parameter_digest"`
	CommandState          string              `json:"command_state"`
	ProviderOrderRevision *string             `json:"provider_order_revision"`
	Result                *sandboxGraphResult `json:"result"`
}
type sandboxGraphCommandReplyWire struct {
	SchemaVersion         int                 `json:"schema_version"`
	EnvironmentID         string              `json:"environment_id"`
	MerchantID            string              `json:"merchant_id"`
	MerchantOrderID       string              `json:"merchant_order_id"`
	CommandID             string              `json:"command_id"`
	CommandKind           string              `json:"command_kind"`
	ParameterDigest       string              `json:"parameter_digest"`
	CommandState          string              `json:"command_state"`
	ProviderOrderRevision *string             `json:"provider_order_revision"`
	Result                *sandboxGraphResult `json:"result"`
}
type sandboxGraphNotificationCommon struct {
	SchemaVersion         int     `json:"schema_version"`
	EventID               string  `json:"event_id"`
	EventType             string  `json:"event_type"`
	EnvironmentID         string  `json:"environment_id"`
	MerchantID            string  `json:"merchant_id"`
	OrderID               string  `json:"order_id"`
	ProviderTransactionID *string `json:"provider_transaction_id"`
	Amount                string  `json:"amount"`
	Currency              string  `json:"currency"`
	OccurredAt            string  `json:"occurred_at"`
	ProviderOrderRevision string  `json:"provider_order_revision"`
}
type sandboxGraphNotificationFailure struct {
	sandboxGraphNotificationCommon
	FailureCode string `json:"failure_code"`
}
type sandboxGraphNotificationRefund struct {
	sandboxGraphNotificationCommon
	RefundID string `json:"refund_id"`
}
type sandboxGraphNotificationCase struct {
	sandboxGraphNotificationCommon
	CaseID string `json:"case_id"`
}
type sandboxGraphRoleWire struct {
	Name  string `json:"name"`
	OID   uint32 `json:"oid"`
	Login bool   `json:"login"`
}
type sandboxGraphSchemaWire struct {
	Name    string               `json:"name"`
	OID     uint32               `json:"oid"`
	Owner   sandboxGraphRoleWire `json:"owner"`
	Runtime sandboxGraphRoleWire `json:"runtime"`
}
type sandboxGraphDatabaseWire struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
	Name string `json:"name"`
	OID  uint32 `json:"oid"`
}
type sandboxGraphKeyWire struct {
	ID        string `json:"id"`
	Digest    string `json:"digest"`
	NotBefore string `json:"not_before"`
	NotAfter  string `json:"not_after"`
}
type sandboxGraphProofWire struct {
	ProofVersion      int                      `json:"proof_version"`
	ProtocolVersion   int                      `json:"protocol_version"`
	EnvironmentID     string                   `json:"environment_id"`
	MerchantID        string                   `json:"merchant_id"`
	PayeeLabel        string                   `json:"payee_label"`
	Database          sandboxGraphDatabaseWire `json:"database"`
	App               sandboxGraphSchemaWire   `json:"app"`
	Mock              sandboxGraphSchemaWire   `json:"mock"`
	Receiver          sandboxGraphSchemaWire   `json:"receiver"`
	Worker            sandboxGraphRoleWire     `json:"worker"`
	Projector         sandboxGraphRoleWire     `json:"projector"`
	AppNonceDigest    string                   `json:"app_nonce_digest"`
	WorkerNonceDigest string                   `json:"worker_nonce_digest"`
	APITokenDigest    string                   `json:"api_token_digest"`
	APIOrigin         string                   `json:"api_origin"`
	ControlOrigin     string                   `json:"control_origin"`
	AppListenOrigin   string                   `json:"app_listen_origin"`
	CallbackURL       string                   `json:"callback_url"`
	Keys              []sandboxGraphKeyWire    `json:"keys"`
}
type sandboxGraphFact struct {
	env, merchant, order, kind, original    string
	transaction, amount, currency, revision *string
	happened                                *time.Time
	received                                time.Time
	valid                                   bool
}

func sandboxGraphSHA(raw []byte) string { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }
func sandboxGraphFrame(parts ...string) string {
	var raw []byte
	for _, s := range parts {
		raw = binary.BigEndian.AppendUint32(raw, uint32(len(s)))
		raw = append(raw, []byte(s)...)
	}
	return sandboxGraphSHA(raw)
}
func sandboxGraphRef(s string) bool {
	if len(s) < 1 || len(s) > 100 || !utf8.ValidString(s) || strings.Contains(s, "://") {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "mg_") || strings.HasPrefix(lower, "bearer") {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(":_./-", r) {
			return false
		}
	}
	return true
}
func sandboxGraphLabel(s string) bool {
	if len(s) < 1 || len(s) > 100 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func sandboxGraphKeyVersion(s string) bool {
	if len(s) < 1 || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func sandboxGraphInstant(s string) (time.Time, bool) {
	t, e := time.Parse(sandboxGraphTimeLayout, s)
	return t, e == nil && sandboxGraphTime(t) && t.Format(sandboxGraphTimeLayout) == s
}
func sandboxGraphProofInstant(s string) (time.Time, bool) {
	t, ok := sandboxGraphInstant(s)
	return t, ok && !t.IsZero()
}
func sandboxGraphMerchant(s string) (string, string, bool) {
	if len(s) != 78 || !strings.HasPrefix(s, "mps1.") || s[41] != '.' {
		return "", "", false
	}
	return s[5:41], s[42:], fundingGraphUUID(s[5:41]) && fundingGraphUUID(s[42:])
}
func sandboxGraphWireIdentity(schema int, env, merchant, command, order string) bool {
	e, _, ok := sandboxGraphMerchant(order)
	return schema == 1 && fundingGraphUUID(env) && sandboxGraphRef(merchant) && fundingGraphUUID(command) && ok && e == env
}

// Shape checking uses a separate token tree, preserving exact nullability and
// rejecting duplicate keys before typed JSON decoding can merge them.
func (g *sandboxGraphData) strict(raw []byte, dest any, max, depthLimit, nodeLimit int) bool {
	if len(raw) == 0 || len(raw) > max || !utf8.Valid(raw) {
		return false
	}
	for at := 0; at < len(raw); at += 256 {
		if !g.tick() {
			return false
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	nodes := 0
	var value func(int) (any, bool)
	value = func(depth int) (any, bool) {
		if !g.tick() {
			return nil, false
		}
		nodes++
		if depth > depthLimit || nodes > nodeLimit {
			return nil, false
		}
		tok, e := d.Token()
		if e != nil {
			return nil, false
		}
		delim, compound := tok.(json.Delim)
		if !compound {
			return tok, true
		}
		if delim == '{' {
			m := map[string]any{}
			for d.More() {
				key, e := d.Token()
				s, ok := key.(string)
				if e != nil || !ok {
					return nil, false
				}
				if _, ok = m[s]; ok {
					return nil, false
				}
				v, ok := value(depth + 1)
				if !ok {
					return nil, false
				}
				m[s] = v
			}
			end, e := d.Token()
			return m, e == nil && end == json.Delim('}')
		}
		if delim == '[' {
			a := []any{}
			for d.More() {
				v, ok := value(depth + 1)
				if !ok {
					return nil, false
				}
				a = append(a, v)
			}
			end, e := d.Token()
			return a, e == nil && end == json.Delim(']')
		}
		return nil, false
	}
	v, ok := value(0)
	if !ok {
		return false
	}
	if _, e := d.Token(); e != io.EOF {
		return false
	}
	var shape func(any, reflect.Type) bool
	var fields func(reflect.Type) map[string]reflect.Type
	fields = func(t reflect.Type) map[string]reflect.Type {
		out := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.Anonymous {
				for k, v := range fields(f.Type) {
					out[k] = v
				}
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			out[tag] = f.Type
		}
		return out
	}
	shape = func(v any, t reflect.Type) bool {
		if t.Kind() == reflect.Pointer {
			if v == nil {
				return true
			}
			return shape(v, t.Elem())
		}
		if v == nil {
			return false
		}
		switch t.Kind() {
		case reflect.Struct:
			m, ok := v.(map[string]any)
			if !ok {
				return false
			}
			fs := fields(t)
			if len(m) != len(fs) {
				return false
			}
			for k, ft := range fs {
				v, ok := m[k]
				if !ok || !shape(v, ft) {
					return false
				}
			}
			return true
		case reflect.Slice:
			a, ok := v.([]any)
			if !ok {
				return false
			}
			for _, v := range a {
				if !shape(v, t.Elem()) {
					return false
				}
			}
			return true
		case reflect.String:
			_, ok := v.(string)
			return ok
		case reflect.Bool:
			_, ok := v.(bool)
			return ok
		default:
			_, ok := v.(json.Number)
			return ok
		}
	}
	if !shape(v, reflect.TypeOf(dest).Elem()) || json.Unmarshal(raw, dest) != nil {
		return false
	}
	canonical, e := json.Marshal(dest)
	if depthLimit == 12 {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		e = enc.Encode(dest)
		canonical = bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
	}
	return e == nil && bytes.Equal(raw, canonical)
}
func (g *sandboxGraphData) request(raw []byte, kind string) (sandboxGraphRequest, bool) {
	var q sandboxGraphRequest
	valid := false
	switch kind {
	case "checkout":
		var r sandboxGraphCheckoutWire
		if g.strict(raw, &r, 8192, 8, 128) {
			q = sandboxGraphRequest{SchemaVersion: r.SchemaVersion, EnvironmentID: r.EnvironmentID, MerchantID: r.MerchantID, CommandID: r.CommandID, MerchantOrderID: r.MerchantOrderID, Amount: r.Amount, Currency: r.Currency, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}
			a, ok := sandboxGraphInstant(r.CreatedAt)
			b, bok := sandboxGraphInstant(r.ExpiresAt)
			valid = ok && bok && b.Sub(a) == 900*time.Second && sandboxGraphMoney(r.Amount) && r.Currency == "CNY"
		}
	case "cancel":
		var r sandboxGraphCancelWire
		if g.strict(raw, &r, 8192, 8, 128) {
			q = sandboxGraphRequest{SchemaVersion: r.SchemaVersion, EnvironmentID: r.EnvironmentID, MerchantID: r.MerchantID, CommandID: r.CommandID, MerchantOrderID: r.MerchantOrderID}
			valid = true
		}
	case "query":
		var r sandboxGraphQueryWire
		if g.strict(raw, &r, 8192, 8, 128) {
			q = sandboxGraphRequest{SchemaVersion: r.SchemaVersion, EnvironmentID: r.EnvironmentID, MerchantID: r.MerchantID, QueryID: r.QueryID, CommandID: r.CommandID, MerchantOrderID: r.MerchantOrderID, CommandKind: r.CommandKind, ParameterDigest: r.ParameterDigest}
			valid = fundingGraphUUID(r.QueryID) && sandboxGraphHas(r.CommandKind, "checkout", "cancel") && sandboxGraphHex(r.ParameterDigest)
		}
	}
	return q, valid && sandboxGraphWireIdentity(q.SchemaVersion, q.EnvironmentID, q.MerchantID, q.CommandID, q.MerchantOrderID)
}
func sandboxGraphReplyValid(r sandboxGraphReply, query bool) bool {
	if !sandboxGraphWireIdentity(r.SchemaVersion, r.EnvironmentID, r.MerchantID, r.CommandID, r.MerchantOrderID) || !sandboxGraphHas(r.CommandKind, "checkout", "cancel") || !sandboxGraphHex(r.ParameterDigest) || query && !fundingGraphUUID(r.QueryID) {
		return false
	}
	if r.CommandState == "in_progress" || query && r.CommandState == "not_seen" {
		return r.ProviderOrderRevision == nil && r.Result == nil
	}
	if r.CommandState != "completed" || r.ProviderOrderRevision == nil || !sandboxGraphMoney(*r.ProviderOrderRevision) || r.Result == nil {
		return false
	}
	v := r.Result
	if r.CommandKind == "cancel" && !sandboxGraphHas(v.Status, "closed", "captured") {
		return false
	}
	switch v.Status {
	case "ready":
		return v.CheckoutRef != nil && strings.HasPrefix(*v.CheckoutRef, "checkout:") && fundingGraphUUID(strings.TrimPrefix(*v.CheckoutRef, "checkout:")) && v.FailureCode == nil && v.Capture == nil && v.ClosedAt == nil
	case "rejected":
		return v.CheckoutRef == nil && v.FailureCode != nil && sandboxGraphHas(*v.FailureCode, "checkout_rejected", "order_closed", "order_expired") && v.Capture == nil && v.ClosedAt == nil
	case "closed":
		if v.CheckoutRef != nil || v.FailureCode != nil || v.Capture != nil || v.ClosedAt == nil {
			return false
		}
		_, ok := sandboxGraphInstant(*v.ClosedAt)
		return ok
	case "captured":
		if v.CheckoutRef != nil || v.FailureCode != nil || v.Capture == nil || v.ClosedAt != nil {
			return false
		}
		c := v.Capture
		_, ok := sandboxGraphInstant(c.OccurredAt)
		return sandboxGraphRef(c.ProviderTransactionID) && sandboxGraphMoney(c.Amount) && c.Currency == "CNY" && c.ProviderOrderRevision == *r.ProviderOrderRevision && ok
	}
	return false
}
func (g *sandboxGraphData) reply(raw []byte, query bool) (sandboxGraphReply, bool) {
	var r sandboxGraphReply
	if query {
		if !g.strict(raw, &r, 65536, 8, 128) {
			return r, false
		}
	} else {
		var v sandboxGraphCommandReplyWire
		if !g.strict(raw, &v, 65536, 8, 128) {
			return r, false
		}
		r = sandboxGraphReply{SchemaVersion: v.SchemaVersion, EnvironmentID: v.EnvironmentID, MerchantID: v.MerchantID, MerchantOrderID: v.MerchantOrderID, CommandID: v.CommandID, CommandKind: v.CommandKind, ParameterDigest: v.ParameterDigest, CommandState: v.CommandState, ProviderOrderRevision: v.ProviderOrderRevision, Result: v.Result}
	}
	return r, sandboxGraphReplyValid(r, query)
}
func (g *sandboxGraphData) notification(raw []byte) (sandboxGraphNotificationCommon, bool) {
	var peek struct {
		EventType string `json:"event_type"`
	}
	if len(raw) > 65536 || json.Unmarshal(raw, &peek) != nil {
		return sandboxGraphNotificationCommon{}, false
	}
	var n sandboxGraphNotificationCommon
	valid := false
	switch peek.EventType {
	case "capture.confirmed":
		valid = g.strict(raw, &n, 65536, 8, 128)
	case "checkout.failed":
		var r sandboxGraphNotificationFailure
		valid = g.strict(raw, &r, 65536, 8, 128) && sandboxGraphHas(r.FailureCode, "checkout_rejected", "order_closed", "order_expired")
		n = r.sandboxGraphNotificationCommon
	case "refund.confirmed", "refund.failed":
		var r sandboxGraphNotificationRefund
		valid = g.strict(raw, &r, 65536, 8, 128) && fundingGraphUUID(r.RefundID)
		n = r.sandboxGraphNotificationCommon
	case "dispute.opened", "chargeback.debited", "chargeback.reversed":
		var r sandboxGraphNotificationCase
		valid = g.strict(raw, &r, 65536, 8, 128) && sandboxGraphRef(r.CaseID)
		n = r.sandboxGraphNotificationCommon
	}
	_, ok := sandboxGraphInstant(n.OccurredAt)
	txOK := n.ProviderTransactionID != nil && sandboxGraphRef(*n.ProviderTransactionID)
	if n.EventType == "checkout.failed" {
		txOK = n.ProviderTransactionID == nil
	}
	return n, valid && n.SchemaVersion == 1 && fundingGraphUUID(n.EnvironmentID) && sandboxGraphRef(n.MerchantID) && fundingGraphUUID(n.OrderID) && sandboxGraphRef(n.EventID) && sandboxGraphMoney(n.Amount) && n.Currency == "CNY" && sandboxGraphMoney(n.ProviderOrderRevision) && ok && txOK
}
func sandboxGraphFactDigest(f sandboxGraphFact) string {
	if f.kind == "capture" {
		if f.transaction == nil || f.amount == nil || f.currency == nil || f.happened == nil || f.revision == nil {
			return ""
		}
		return sandboxGraphFrame(f.env, f.merchant, f.order, f.kind, *f.transaction, *f.amount, *f.currency, f.happened.UTC().Format(sandboxGraphTimeLayout), *f.revision)
	}
	if f.kind == "closed" {
		if f.happened == nil || f.revision == nil {
			return ""
		}
		return sandboxGraphFrame(f.env, f.merchant, f.order, f.kind, f.happened.UTC().Format(sandboxGraphTimeLayout), *f.revision)
	}
	return sandboxGraphFrame(f.env, f.merchant, f.order, f.kind, f.original)
}
