package fundinglotproof

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxV2Bytes = 262144
const maxV2Nodes = 8192

var migrationName = regexp.MustCompile(`^[0-9]{3}_[a-z0-9_]+\.sql$`)

// EncodeV2 validates all supplied values before encoding, including UTF-8,
// rather than allowing encoding/json to replace or repair invalid input.
func EncodeV2(input V2Input) (*CanonicalV2, error) {
	if !validateV2(input) {
		return nil, ErrCanonical
	}
	raw, err := encodeCanonical(input, maxV2Bytes)
	if err != nil {
		return nil, ErrCanonical
	}
	return DecodeV2(raw)
}

// DecodeV2 proves format validity only. All OIDs and digests remain claims that
// a future issuer must compare to independently authenticated database facts.
func DecodeV2(raw []byte) (*CanonicalV2, error) {
	if len(raw) == 0 || len(raw) > maxV2Bytes || !utf8.Valid(raw) || !v1ValidEscapes(raw) {
		return nil, ErrCanonical
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	remaining := maxV2Nodes
	v, ok := readV2Value(d, 1, &remaining)
	if !ok {
		return nil, ErrCanonical
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrCanonical
	}
	if !exactV2Shape(v, reflect.TypeOf(V2Input{})) {
		return nil, ErrCanonical
	}
	var input V2Input
	if json.Unmarshal(raw, &input) != nil || !validateV2(input) {
		return nil, ErrCanonical
	}
	canonical, err := encodeCanonical(input, maxV2Bytes)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, ErrCanonical
	}
	return &CanonicalV2{state: &canonicalState{raw: canonical, sha: digest(canonical), bindings: copyBindings(input)}}, nil
}

func encodeCanonical(v any, limit int) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if e.Encode(v) != nil {
		return nil, ErrCanonical
	}
	raw := b.Bytes()
	if len(raw) == 0 || raw[len(raw)-1] != '\n' || len(raw)-1 > limit {
		return nil, ErrCanonical
	}
	return bytes.Clone(raw[:len(raw)-1]), nil
}
func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	nonzero := false
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
		if c != '0' {
			nonzero = true
		}
	}
	return nonzero
}
func validName(s string) bool {
	return len(s) > 0 && len(s) <= 63 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func validRole(p Role) bool {
	return validName(p.Name) && p.OID != 0 && !strings.HasPrefix(p.Name, "pg_")
}
func validSchema(p Schema) bool {
	if !validName(p.Name) || p.OID == 0 || strings.HasPrefix(p.Name, "pg_") || p.Name == "information_schema" || !validRole(p.ObjectOwner) {
		return false
	}
	return p.NamespaceOwner == p.ObjectOwner || p.Name == "public" && p.NamespaceOwner == (Role{Name: "pg_database_owner", OID: 6171})
}
func validateV2(p V2Input) bool {
	if p.Domain != DomainV2 || p.ProofVersion != 2 || p.BasisVersion != 1 || p.CatalogPostgresMajor != 16 || p.CatalogDescriptorVersion == 0 || !validUUID(p.EpochID) {
		return false
	}
	for _, d := range []string{p.BasisDigest, p.BasisDescriptorDigest, p.ManifestDigest, p.CatalogExpectedDigest, p.CatalogSchemaDigest, p.CatalogRolesDigest} {
		if !validDigest(d) {
			return false
		}
	}
	if len(p.Database.Host) == 0 || len(p.Database.Host) > 253 || !utf8.ValidString(p.Database.Host) || strings.ContainsRune(p.Database.Host, 0) || strings.HasPrefix(p.Database.Host, "/") || p.Database.Port == 0 || !validName(p.Database.Name) || p.Database.OID == 0 || !validSchema(p.AppSchema) || !validRole(p.AppRole) || !validRole(p.ProjectorRole) {
		return false
	}
	if len(p.MigrationManifest) < 1 || len(p.MigrationManifest) > 256 {
		return false
	}
	prev := ""
	for _, m := range p.MigrationManifest {
		if len(m.Name) > 255 || !migrationName.MatchString(m.Name) || m.Name <= prev || !validDigest(m.SHA256) {
			return false
		}
		prev = m.Name
	}
	manifest, err := encodeCanonical(p.MigrationManifest, maxV2Bytes)
	if err != nil || digest(manifest) != p.ManifestDigest {
		return false
	}
	roles := []Role{p.AppSchema.ObjectOwner, p.AppRole, p.ProjectorRole}
	schemas := []Schema{p.AppSchema}
	switch p.Mode {
	case "ordinary":
		if p.Sandbox != nil || p.PredecessorPaymentDigest != nil || p.PredecessorPaymentCanonicalBase64URL != nil {
			return false
		}
	case "sandbox":
		if p.Sandbox == nil || p.PredecessorPaymentDigest == nil || p.PredecessorPaymentCanonicalBase64URL == nil || !validDigest(*p.PredecessorPaymentDigest) {
			return false
		}
		s := p.Sandbox
		if !validSchema(s.MockSchema) || !validSchema(s.ReceiverSchema) || !validRole(s.MockRole) || !validRole(s.ReceiverRole) || !validRole(s.WorkerRole) {
			return false
		}
		roles = append(roles, s.MockSchema.ObjectOwner, s.MockRole, s.ReceiverSchema.ObjectOwner, s.ReceiverRole, s.WorkerRole)
		schemas = append(schemas, s.MockSchema, s.ReceiverSchema)
		if !matchesV1(p) {
			return false
		}
	default:
		return false
	}
	for i, r := range roles {
		for j := 0; j < i; j++ {
			if r.Name == roles[j].Name || r.OID == roles[j].OID {
				return false
			}
		}
	}
	for i, s := range schemas {
		if p.Mode == "sandbox" && s.NamespaceOwner != s.ObjectOwner {
			return false
		}
		for j := 0; j < i; j++ {
			if s.Name == schemas[j].Name || s.OID == schemas[j].OID {
				return false
			}
		}
	}
	return true
}

// Every value, including null and each container, consumes one node. Names and
// closing delimiters do not. The root container is depth one.
func readV2Value(d *json.Decoder, depth int, remaining *int) (any, bool) {
	if depth > 12 || *remaining == 0 {
		return nil, false
	}
	*remaining = *remaining - 1
	v, err := d.Token()
	if err != nil {
		return nil, false
	}
	switch t := v.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := map[string]any{}
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok {
					return nil, false
				}
				if _, exists := m[name]; exists {
					return nil, false
				}
				value, ok := readV2Value(d, depth+1, remaining)
				if !ok {
					return nil, false
				}
				m[name] = value
			}
			end, err := d.Token()
			return m, err == nil && end == json.Delim('}')
		case '[':
			a := []any{}
			for d.More() {
				value, ok := readV2Value(d, depth+1, remaining)
				if !ok {
					return nil, false
				}
				a = append(a, value)
			}
			end, err := d.Token()
			return a, err == nil && end == json.Delim(']')
		}
		return nil, false
	default:
		return t, true
	}
}

func exactV2Shape(v any, t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		return v == nil || exactV2Shape(v, t.Elem())
	}
	if v == nil {
		return false
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok || len(m) != t.NumField() {
			return false
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			value, ok := m[f.Tag.Get("json")]
			if !ok || !exactV2Shape(value, f.Type) {
				return false
			}
		}
		return true
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return false
		}
		for _, value := range a {
			if !exactV2Shape(value, t.Elem()) {
				return false
			}
		}
		return true
	case reflect.String:
		_, ok := v.(string)
		return ok
	case reflect.Uint16, reflect.Uint32:
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		s := string(n)
		if len(s) == 0 || len(s) > 1 && s[0] == '0' {
			return false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		_, err := strconv.ParseUint(s, 10, t.Bits())
		return err == nil
	}
	return false
}

func matchesV1(p V2Input) bool {
	b64 := *p.PredecessorPaymentCanonicalBase64URL
	if len(b64) == 0 || len(b64) > 87382 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 || len(raw) > 65536 || base64.RawURLEncoding.EncodeToString(raw) != b64 || digest(raw) != *p.PredecessorPaymentDigest {
		return false
	}
	var old v1Proof
	if v1StrictDecode(raw, &old) != nil || v1ValidateProof(old) != nil {
		return false
	}
	canonical, err := v1CanonicalProof(old)
	if err != nil || !bytes.Equal(canonical, raw) {
		return false
	}
	role := func(a v1RolePin, b Role) bool { return a.Name == b.Name && a.OID == b.OID }
	schema := func(a v1SchemaPin, b Schema, r Role) bool {
		return a.Name == b.Name && a.OID == b.OID && b.NamespaceOwner == b.ObjectOwner && role(a.Owner, b.ObjectOwner) && role(a.Runtime, r)
	}
	s := p.Sandbox
	return old.Database.Host == p.Database.Host && old.Database.Port == p.Database.Port && old.Database.Name == p.Database.Name && old.Database.OID == p.Database.OID && schema(old.App, p.AppSchema, p.AppRole) && schema(old.Mock, s.MockSchema, s.MockRole) && schema(old.Receiver, s.ReceiverSchema, s.ReceiverRole) && role(old.Worker, s.WorkerRole) && role(old.Projector, p.ProjectorRole)
}
