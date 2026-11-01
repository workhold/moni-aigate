package periodicbudget

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// These constants are the independently calculated public vector from the
// frozen cursor contract. They are not runtime secrets or configuration.
const (
	pbCursorTestKeyDomain  = "moni-aigate:periodic-budget:cursor-key:v1"
	pbCursorTestSignDomain = "moni-aigate:periodic-budget:cursor-signature:v1"
	pbCursorTestScopeID    = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	pbCursorTestTenantID   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	pbCursorTestLastID     = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	pbCursorTestOtherID    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	pbCursorTestPayload    = `{"v":1,"route":"key_allocations","scope":"key","scope_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","identity_scope":"tenant:bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","order":"created_at_desc_id_c_desc","last_at":"2026-11-01T00:00:00.000000Z","last_id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd"}`
	pbCursorTestGolden     = "pbc1.eyJ2IjoxLCJyb3V0ZSI6ImtleV9hbGxvY2F0aW9ucyIsInNjb3BlIjoia2V5Iiwic2NvcGVfaWQiOiJjY2NjY2NjYy1jY2NjLTRjY2MtOGNjYy1jY2NjY2NjY2NjY2MiLCJpZGVudGl0eV9zY29wZSI6InRlbmFudDpiYmJiYmJiYi1iYmJiLTRiYmItOGJiYi1iYmJiYmJiYmJiYmIiLCJvcmRlciI6ImNyZWF0ZWRfYXRfZGVzY19pZF9jX2Rlc2MiLCJsYXN0X2F0IjoiMjAyNi0xMS0wMVQwMDowMDowMC4wMDAwMDBaIiwibGFzdF9pZCI6ImRkZGRkZGRkLWRkZGQtNGRkZC04ZGRkLWRkZGRkZGRkZGRkZCJ9.BJ7rtF6Tw9yk-pd6COVZ8wwuyPZIz83evcX8XkEJ_Oo"
)

func pbCursorTestPepper() []byte {
	p := make([]byte, 32)
	for i := range p {
		p[i] = byte(i)
	}
	return p
}

func pbCursorTestBinding() CursorBinding {
	return CursorBinding{Route: "key_allocations", Scope: "key", ScopeID: pbCursorTestScopeID,
		IdentityScope: "tenant:" + pbCursorTestTenantID, Order: "created_at_desc_id_c_desc"}
}

func pbCursorTestPosition() CursorPosition {
	return CursorPosition{At: "2026-11-01T00:00:00.000000Z", ID: pbCursorTestLastID}
}

func pbCursorTestCodec(t *testing.T) *CursorCodec {
	t.Helper()
	c, err := NewCursorCodec(pbCursorTestPepper())
	if err != nil || c == nil {
		t.Fatal("public fixture Pepper was rejected")
	}
	return c
}

func pbCursorTestInvalid(t *testing.T, err error) {
	t.Helper()
	if err == nil || ErrorCode(err) != "invalid_cursor" {
		t.Fatalf("cursor error code = %q, want invalid_cursor", ErrorCode(err))
	}
}

// This oracle uses only the standard cryptographic primitives and contract
// constants. It can sign deliberately malformed payloads without relying on
// the production encoder, so strict parsing is tested after a valid MAC.
func pbCursorTestKey(pepper []byte) []byte {
	m := hmac.New(sha256.New, pepper)
	_, _ = m.Write([]byte(pbCursorTestKeyDomain))
	return m.Sum(nil)
}

func pbCursorTestMAC(key []byte, domain, prefix string, payload []byte) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(domain))
	_, _ = m.Write([]byte{0})
	_, _ = m.Write([]byte(prefix))
	_, _ = m.Write([]byte{0})
	_, _ = m.Write(payload)
	return m.Sum(nil)
}

func pbCursorTestToken(payload []byte) string {
	mac := pbCursorTestMAC(pbCursorTestKey(pbCursorTestPepper()), pbCursorTestSignDomain, "pbc1", payload)
	return pbCursorTestAssemble("pbc1", payload, mac)
}

func pbCursorTestAssemble(prefix string, payload, mac []byte) string {
	return prefix + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac)
}

func TestCursorContractGoldenAndKeyIsolation(t *testing.T) {
	key := pbCursorTestKey(pbCursorTestPepper())
	if hex.EncodeToString(key) != "45b2f9205c7c1baeb626e42d26e807b1070db901aa819def0ebcedda6da5eaf1" {
		t.Fatal("independent key derivation disagrees with public vector")
	}
	if len(pbCursorTestPayload) != 285 {
		t.Fatal("public payload byte count changed")
	}
	mac := pbCursorTestMAC(key, pbCursorTestSignDomain, "pbc1", []byte(pbCursorTestPayload))
	if hex.EncodeToString(mac) != "049eebb45e93c3dca4fa977a08e559f30c2ec8f648cfcddebdc5fc5e4109fcea" || base64.RawURLEncoding.EncodeToString(mac) != "BJ7rtF6Tw9yk-pd6COVZ8wwuyPZIz83evcX8XkEJ_Oo" {
		t.Fatal("independent signing disagrees with public vector")
	}
	if pbCursorTestToken([]byte(pbCursorTestPayload)) != pbCursorTestGolden {
		t.Fatal("public outer token changed")
	}
	pepper := pbCursorTestPepper()
	c, err := NewCursorCodec(pepper)
	if err != nil {
		t.Fatal("fixture codec construction failed")
	}
	for i := range pepper {
		pepper[i] ^= 0xff
	}
	token, err := c.Encode(pbCursorTestBinding(), pbCursorTestPosition())
	if err != nil || token != pbCursorTestGolden {
		t.Fatal("encoder differs from golden or retained caller Pepper")
	}
	position, err := c.Decode(pbCursorTestBinding(), pbCursorTestGolden)
	if err != nil || position != pbCursorTestPosition() {
		t.Fatal("decoder rejected golden position")
	}
	restarted := pbCursorTestCodec(t)
	position, err = restarted.Decode(pbCursorTestBinding(), token)
	if err != nil || position != pbCursorTestPosition() {
		t.Fatal("identical Pepper cannot decode a previous codec's cursor")
	}
	rotatedPepper := pbCursorTestPepper()
	rotatedPepper[0] ^= 1
	rotated, err := NewCursorCodec(rotatedPepper)
	if err != nil {
		t.Fatal("rotated Pepper fixture was rejected")
	}
	position, err = rotated.Decode(pbCursorTestBinding(), token)
	pbCursorTestInvalid(t, err)
	if position != (CursorPosition{}) {
		t.Fatal("rotated key failure exposed an unverified position")
	}
	for _, badPepper := range [][]byte{nil, {}, make([]byte, 31), make([]byte, 33), []byte(base64.StdEncoding.EncodeToString(pbCursorTestPepper()))} {
		c, err := NewCursorCodec(badPepper)
		if err == nil || c != nil {
			t.Fatal("constructor accepted a non-32-byte raw Pepper")
		}
	}
	var zero CursorCodec
	_, err = zero.Encode(pbCursorTestBinding(), pbCursorTestPosition())
	pbCursorTestInvalid(t, err)
	position, err = zero.Decode(pbCursorTestBinding(), pbCursorTestGolden)
	pbCursorTestInvalid(t, err)
	if position != (CursorPosition{}) {
		t.Fatal("zero codec exposed a position")
	}
}

func TestCursorAllRoutesAndUTC6Boundaries(t *testing.T) {
	codec := pbCursorTestCodec(t)
	bindings := []CursorBinding{
		{Route: "platform_tenant_windows", Scope: "tenant", ScopeID: pbCursorTestScopeID, IdentityScope: "platform", Order: "start_at_desc_id_c_desc"},
		{Route: "platform_tenant_allocations", Scope: "tenant", ScopeID: pbCursorTestScopeID, IdentityScope: "platform", Order: "created_at_desc_id_c_desc"},
		{Route: "wallet_windows", Scope: "tenant", ScopeID: pbCursorTestScopeID, IdentityScope: "tenant:" + pbCursorTestScopeID, Order: "start_at_desc_id_c_desc"},
		{Route: "wallet_allocations", Scope: "tenant", ScopeID: pbCursorTestScopeID, IdentityScope: "tenant:" + pbCursorTestScopeID, Order: "created_at_desc_id_c_desc"},
		{Route: "key_windows", Scope: "key", ScopeID: pbCursorTestScopeID, IdentityScope: "tenant:" + pbCursorTestTenantID, Order: "start_at_desc_id_c_desc"},
		pbCursorTestBinding(),
		{Route: "procurement_windows", Scope: "procurement", ScopeID: pbCursorTestScopeID, IdentityScope: "platform", Order: "start_at_desc_id_c_desc"},
		{Route: "procurement_allocations", Scope: "procurement", ScopeID: pbCursorTestScopeID, IdentityScope: "platform", Order: "created_at_desc_id_c_desc"},
	}
	for _, binding := range bindings {
		t.Run(binding.Route, func(t *testing.T) {
			for _, at := range []string{"0001-01-01T00:00:00.000000Z", "2000-02-29T00:00:00.000001Z", "2024-02-29T23:59:59.999999Z", "9999-12-31T23:59:59.999999Z"} {
				want := CursorPosition{At: at, ID: pbCursorTestLastID}
				token, err := codec.Encode(binding, want)
				if err != nil {
					t.Fatal("valid route/time boundary rejected")
				}
				position, err := codec.Decode(binding, token)
				if err != nil || position != want {
					t.Fatal("route/time boundary did not round trip")
				}
				parts := strings.Split(token, ".")
				if len(parts) != 3 || len(token) > 1024 || len(parts[2]) != 43 {
					t.Fatal("encoder produced a noncanonical outer token")
				}
				payload, err := base64.RawURLEncoding.DecodeString(parts[1])
				if err != nil || len(payload) > 512 || token != pbCursorTestToken(payload) {
					t.Fatal("encoder signing domain/key differs from independent oracle")
				}
			}
		})
	}
}

func TestCursorRejectsCrossBinding(t *testing.T) {
	codec := pbCursorTestCodec(t)
	for name, mutate := range map[string]func(*CursorBinding){
		"route": func(b *CursorBinding) {
			b.Route = "wallet_allocations"
			b.Scope = "tenant"
			b.ScopeID = pbCursorTestTenantID
		},
		"scope":    func(b *CursorBinding) { b.Scope = "procurement" },
		"target":   func(b *CursorBinding) { b.ScopeID = pbCursorTestOtherID },
		"identity": func(b *CursorBinding) { b.IdentityScope = "tenant:" + pbCursorTestOtherID },
		"order":    func(b *CursorBinding) { b.Route = "key_windows"; b.Order = "start_at_desc_id_c_desc" },
	} {
		t.Run(name, func(t *testing.T) {
			binding := pbCursorTestBinding()
			mutate(&binding)
			position, err := codec.Decode(binding, pbCursorTestGolden)
			pbCursorTestInvalid(t, err)
			if position != (CursorPosition{}) {
				t.Fatal("cross-binding cursor exposed a position")
			}
		})
	}
}

func TestCursorRejectsInvalidEncoderInput(t *testing.T) {
	codec := pbCursorTestCodec(t)
	for name, mutate := range map[string]func(*CursorBinding){
		"zero":          func(b *CursorBinding) { *b = CursorBinding{} },
		"unknown_route": func(b *CursorBinding) { b.Route = "key_history" },
		"case_route":    func(b *CursorBinding) { b.Route = "KEY_allocations" },
		"wrong_scope":   func(b *CursorBinding) { b.Scope = "tenant" },
		"bad_scope_id":  func(b *CursorBinding) { b.ScopeID = "00000000-0000-0000-0000-000000000000" },
		"uppercase_id":  func(b *CursorBinding) { b.ScopeID = strings.ToUpper(pbCursorTestScopeID) },
		"platform_key":  func(b *CursorBinding) { b.IdentityScope = "platform" },
		"raw_tenant":    func(b *CursorBinding) { b.IdentityScope = pbCursorTestTenantID },
		"bad_identity":  func(b *CursorBinding) { b.IdentityScope = "tenant:00000000-0000-0000-0000-000000000000" },
		"unknown_order": func(b *CursorBinding) { b.Order = "created_at_desc_id_desc" },
		"wrong_order":   func(b *CursorBinding) { b.Order = "start_at_desc_id_c_desc" },
		"wallet_target": func(b *CursorBinding) { b.Route = "wallet_allocations"; b.Scope = "tenant" },
	} {
		t.Run(name, func(t *testing.T) {
			binding := pbCursorTestBinding()
			mutate(&binding)
			token, err := codec.Encode(binding, pbCursorTestPosition())
			pbCursorTestInvalid(t, err)
			if token != "" {
				t.Fatal("invalid encoder binding produced a token")
			}
		})
	}
	for _, at := range []string{
		"", "0000-01-01T00:00:00.000000Z", "10000-01-01T00:00:00.000000Z", "1900-02-29T00:00:00.000000Z",
		"2026-02-30T00:00:00.000000Z", "2026-11-01T00:00:00Z", "2026-11-01T00:00:00.00000Z", "2026-11-01T00:00:00.0000000Z",
		"2026-11-01T00:00:00.000000+00:00", "2026-11-01T00:00:00,000000Z", "2026-11-01T00:00:60.000000Z", " 2026-11-01T00:00:00.000000Z",
	} {
		position := pbCursorTestPosition()
		position.At = at
		token, err := codec.Encode(pbCursorTestBinding(), position)
		pbCursorTestInvalid(t, err)
		if token != "" {
			t.Fatal("invalid timestamp produced a token")
		}
	}
	for _, id := range []string{"", "00000000-0000-0000-0000-000000000000", strings.ToUpper(pbCursorTestLastID), "dddddddddddd4ddd8ddddddddddddddd", " " + pbCursorTestLastID} {
		position := pbCursorTestPosition()
		position.ID = id
		token, err := codec.Encode(pbCursorTestBinding(), position)
		pbCursorTestInvalid(t, err)
		if token != "" {
			t.Fatal("invalid position ID produced a token")
		}
	}
}

func TestCursorRejectsSignedNoncanonicalPayloads(t *testing.T) {
	codec := pbCursorTestCodec(t)
	replace := func(old, next string) []byte { return []byte(strings.Replace(pbCursorTestPayload, old, next, 1)) }
	cases := map[string][]byte{
		"v_decimal":        replace(`"v":1`, `"v":1.0`),
		"v_exponent":       replace(`"v":1`, `"v":1e0`),
		"v_string":         replace(`"v":1`, `"v":"1"`),
		"v_unknown":        replace(`"v":1`, `"v":2`),
		"v_null":           replace(`"v":1`, `"v":null`),
		"missing":          replace(`"scope":"key",`, ""),
		"unknown":          []byte(strings.TrimSuffix(pbCursorTestPayload, "}") + `,"extra":null}`),
		"duplicate":        []byte(strings.TrimSuffix(pbCursorTestPayload, "}") + `,"scope":"key"}`),
		"escaped_dup":      []byte(strings.TrimSuffix(pbCursorTestPayload, "}") + `,"sc\u006fpe":"key"}`),
		"case_alias":       replace(`"scope_id"`, `"Scope_ID"`),
		"reordered":        replace(`"v":1,"route":"key_allocations"`, `"route":"key_allocations","v":1`),
		"leading_space":    []byte(" " + pbCursorTestPayload),
		"trailing_newline": []byte(pbCursorTestPayload + "\n"),
		"inner_space":      replace(`"v":1,`, `"v":1, `),
		"trailing_json":    []byte(pbCursorTestPayload + `{}`),
		"root_null":        []byte(`null`),
		"root_array":       []byte(`[]`),
		"scope_null":       replace(`"scope":"key"`, `"scope":null`),
		"scope_number":     replace(`"scope":"key"`, `"scope":1`),
		"route_unknown":    replace(`"route":"key_allocations"`, `"route":"unknown"`),
		"scope_wrong":      replace(`"scope":"key"`, `"scope":"tenant"`),
		"identity_wrong":   replace(`tenant:bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb`, `platform`),
		"order_wrong":      replace(`created_at_desc_id_c_desc`, `start_at_desc_id_c_desc`),
		"nil_uuid":         replace(pbCursorTestLastID, "00000000-0000-0000-0000-000000000000"),
		"uppercase_uuid":   replace(pbCursorTestLastID, strings.ToUpper(pbCursorTestLastID)),
		"escaped_uuid":     replace(`"last_id":"dddd`, `"last_id":"\u0064ddd`),
		"short_time":       replace(`2026-11-01T00:00:00.000000Z`, `2026-11-01T00:00:00Z`),
		"bad_calendar":     replace(`2026-11-01T00:00:00.000000Z`, `2026-02-30T00:00:00.000000Z`),
		"year_zero":        replace(`2026-11-01T00:00:00.000000Z`, `0000-01-01T00:00:00.000000Z`),
		"high_surrogate":   replace(`"scope":"key"`, `"scope":"\ud800"`),
		"low_surrogate":    replace(`"scope":"key"`, `"scope":"\udc00"`),
		"control":          replace(`"scope":"key"`, "\"scope\":\"ke\ny\""),
	}
	invalidUTF8 := []byte(pbCursorTestPayload)
	invalidUTF8[bytes.Index(invalidUTF8, []byte("key_allocations"))] = 0xff
	cases["invalid_utf8"] = invalidUTF8
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			position, err := codec.Decode(pbCursorTestBinding(), pbCursorTestToken(payload))
			pbCursorTestInvalid(t, err)
			if position != (CursorPosition{}) {
				t.Fatal("signed malformed payload exposed a position")
			}
		})
	}
}

func TestCursorRejectsOuterEncodingAndMACChanges(t *testing.T) {
	codec := pbCursorTestCodec(t)
	parts := strings.Split(pbCursorTestGolden, ".")
	key := pbCursorTestKey(pbCursorTestPepper())
	payload := []byte(pbCursorTestPayload)
	mac := pbCursorTestMAC(key, pbCursorTestSignDomain, "pbc1", payload)
	badMAC := append([]byte(nil), mac...)
	badMAC[0] ^= 1
	wrongPepper := pbCursorTestPepper()
	wrongPepper[0] ^= 1
	cases := map[string]string{
		"empty":             "",
		"single":            "pbc1",
		"two_parts":         "pbc1." + parts[1],
		"extra_part":        pbCursorTestGolden + ".extra",
		"empty_payload":     "pbc1.." + parts[2],
		"empty_mac":         "pbc1." + parts[1] + ".",
		"unknown_prefix":    "pbc2." + parts[1] + "." + parts[2],
		"payload_padding":   "pbc1." + parts[1] + "=." + parts[2],
		"mac_padding":       pbCursorTestGolden + "=",
		"plus":              "pbc1.+" + parts[1][1:] + "." + parts[2],
		"slash":             "pbc1./" + parts[1][1:] + "." + parts[2],
		"space":             pbCursorTestGolden + " ",
		"newline":           pbCursorTestGolden + "\n",
		"non_ascii":         "pbc1.界." + parts[2],
		"nul":               pbCursorTestGolden + "\x00",
		"mac_31":            pbCursorTestAssemble("pbc1", payload, mac[:31]),
		"mac_33":            pbCursorTestAssemble("pbc1", payload, append(append([]byte(nil), mac...), 0)),
		"mac_wrong":         pbCursorTestAssemble("pbc1", payload, badMAC),
		"payload_unsigned":  pbCursorTestAssemble("pbc1", []byte(strings.Replace(pbCursorTestPayload, pbCursorTestScopeID, pbCursorTestOtherID, 1)), mac),
		"raw_pepper_as_key": pbCursorTestAssemble("pbc1", payload, pbCursorTestMAC(pbCursorTestPepper(), pbCursorTestSignDomain, "pbc1", payload)),
		"wrong_pepper":      pbCursorTestAssemble("pbc1", payload, pbCursorTestMAC(pbCursorTestKey(wrongPepper), pbCursorTestSignDomain, "pbc1", payload)),
		"wrong_sign_domain": pbCursorTestAssemble("pbc1", payload, pbCursorTestMAC(key, "moni-aigate:periodic-budget:cursor-key:v1", "pbc1", payload)),
		"unsigned_prefix":   pbCursorTestAssemble("pbc1", payload, pbCursorTestMAC(key, pbCursorTestSignDomain, "pbc2", payload)),
		"prefix_case":       "PBC1." + parts[1] + "." + parts[2],
	}
	// A noncanonical last base64 character can decode to the same MAC with a
	// permissive decoder. Canonical re-encoding must still reject it.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, parts[2][len(parts[2])-1])
	if last < 0 || last%4 != 0 {
		t.Fatal("public MAC lacks the expected two unused tail bits")
	}
	for tail := 1; tail <= 3; tail++ {
		badSegment := parts[2][:len(parts[2])-1] + string(alphabet[last+tail])
		decoded, err := base64.RawURLEncoding.DecodeString(badSegment)
		if err != nil || !bytes.Equal(decoded, mac) {
			t.Fatal("noncanonical MAC-tail fixture is not byte-equivalent")
		}
		cases["mac_tail_"+string(rune('0'+tail))] = "pbc1." + parts[1] + "." + badSegment
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			position, err := codec.Decode(pbCursorTestBinding(), token)
			pbCursorTestInvalid(t, err)
			if position != (CursorPosition{}) {
				t.Fatal("unverified outer token exposed a position")
			}
		})
	}
}

func TestCursorRejectsNoncanonicalPayloadBase64Tail(t *testing.T) {
	codec := pbCursorTestCodec(t)
	binding := CursorBinding{Route: "platform_tenant_allocations", Scope: "tenant", ScopeID: pbCursorTestScopeID,
		IdentityScope: "platform", Order: "created_at_desc_id_c_desc"}
	payload := strings.Replace(pbCursorTestPayload, `"route":"key_allocations"`, `"route":"platform_tenant_allocations"`, 1)
	payload = strings.Replace(payload, `"scope":"key"`, `"scope":"tenant"`, 1)
	payload = strings.Replace(payload, `"identity_scope":"tenant:`+pbCursorTestTenantID+`"`, `"identity_scope":"platform"`, 1)
	token := pbCursorTestToken([]byte(payload))
	position, err := codec.Decode(binding, token)
	if err != nil || position != pbCursorTestPosition() {
		t.Fatal("independent alternate-route cursor fixture was rejected")
	}
	parts := strings.Split(token, ".")
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, parts[1][len(parts[1])-1])
	if len(payload)%3 == 0 || last < 0 || last%4 != 0 {
		t.Fatal("alternate-route payload lacks unused base64 tail bits")
	}
	badSegment := parts[1][:len(parts[1])-1] + string(alphabet[last+1])
	decoded, err := base64.RawURLEncoding.DecodeString(badSegment)
	if err != nil || !bytes.Equal(decoded, []byte(payload)) {
		t.Fatal("noncanonical payload-tail fixture is not byte-equivalent")
	}
	position, err = codec.Decode(binding, "pbc1."+badSegment+"."+parts[2])
	pbCursorTestInvalid(t, err)
	if position != (CursorPosition{}) {
		t.Fatal("noncanonical payload encoding exposed a position")
	}
}

func TestCursorRejectsSizeBoundaryInputs(t *testing.T) {
	codec := pbCursorTestCodec(t)
	for _, size := range []int{512, 513} {
		// Valid MACs ensure these inputs reach the payload size/canonical gate.
		// No exact valid DTO can grow to this length; padding is also forbidden.
		payload := append([]byte(pbCursorTestPayload), bytes.Repeat([]byte(" "), size-len(pbCursorTestPayload))...)
		position, err := codec.Decode(pbCursorTestBinding(), pbCursorTestToken(payload))
		pbCursorTestInvalid(t, err)
		if position != (CursorPosition{}) {
			t.Fatal("oversized/noncanonical payload exposed a position")
		}
	}
	for _, size := range []int{1024, 1025} {
		token := "pbc1." + strings.Repeat("A", size-6-43) + "." + strings.Repeat("A", 43)
		if len(token) != size {
			t.Fatal("outer token size fixture is incorrect")
		}
		position, err := codec.Decode(pbCursorTestBinding(), token)
		pbCursorTestInvalid(t, err)
		if position != (CursorPosition{}) {
			t.Fatal("oversized/noncanonical token exposed a position")
		}
	}
}
