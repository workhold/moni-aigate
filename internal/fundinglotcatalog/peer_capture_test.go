package fundinglotcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxschema"
)

type peerTemplatePosition struct {
	Path   string `json:"path"`
	Part   int    `json:"part"`
	Schema string `json:"schema"`
}

// This opt-in capture only produces candidate review material. Production has
// no input that can load these bytes and this function cannot issue a proof.
func TestCatalogPeerCaptureReviewMetadata(t *testing.T) {
	if os.Getenv("C0_CATALOG_CAPTURE_PEER") != "1" {
		t.Skip("offline candidate metadata capture is explicitly opt-in")
	}
	f := newCatalogPeerFixture(t)
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("peer candidate caller RR RO")
	}
	defer tx.Rollback(context.Background())
	if verifyPeerRoleClosureTx(f.ctx, tx, f.pins) != nil {
		t.Fatal("peer candidate role closure")
	}
	snapshot, err := observeCatalogTx(f.ctx, tx, f.pins)
	if err != nil || snapshot == nil {
		t.Fatal("peer candidate metadata observation")
	}
	var schema, roles document
	for _, d := range []struct {
		raw []byte
		out *document
	}{{snapshot.schemaCanonical, &schema}, {snapshot.rolesCanonical, &roles}} {
		decoder := json.NewDecoder(bytes.NewReader(d.raw))
		decoder.UseNumber()
		if decoder.Decode(d.out) != nil {
			t.Fatal("peer candidate metadata JSON")
		}
	}
	identifiers := map[string]string{}
	for _, p := range []struct{ name, scope string }{{f.schemas[0], "application"}, {f.schemas[1], "mock"}, {f.schemas[2], "receiver"}} {
		var identifier string
		if tx.QueryRow(f.ctx, `SELECT pg_catalog.quote_ident($1::pg_catalog.text)`, p.name).Scan(&identifier) != nil {
			t.Fatal("peer candidate schema identifier")
		}
		identifiers[identifier] = p.scope
	}
	var positions []peerTemplatePosition
	converted := capturePeerTemplates(t, schema, "/schema", "", identifiers, &positions).(document)
	// Role metadata contains no SQL deparse field and may not carry a live name.
	for identifier := range identifiers {
		if strings.Contains(string(snapshot.rolesCanonical), identifier) {
			t.Fatal("peer candidate role bytes contain live schema name")
		}
	}
	candidate := descriptorSet{FormatVersion: 1, PostgresMajor: 16, Mode: "sandbox", Schema: converted, Roles: roles}
	b, err := canonical(candidate)
	if err != nil {
		t.Fatal("peer candidate canonical descriptor")
	}
	for identifier := range identifiers {
		if strings.Contains(string(b), identifier) {
			t.Fatal("peer candidate contains unresolved schema name")
		}
	}
	auditPeerCandidateSources(t, b)
	audit, err := canonical(positions)
	if err != nil {
		t.Fatal("peer candidate position audit")
	}
	if tx.Commit(f.ctx) != nil {
		t.Fatal("peer candidate caller commit")
	}
	// Close and audit the exact owned database objects before publishing files.
	f.close(t)
	if t.Failed() {
		return
	}
	dir, err := os.MkdirTemp("/tmp", "moni-catalog-peer-review-")
	if err != nil {
		t.Fatal("peer candidate review directory")
	}
	if os.WriteFile(filepath.Join(dir, "sandbox-candidate.json"), b, 0600) != nil || os.WriteFile(filepath.Join(dir, "sandbox-template-positions.json"), audit, 0600) != nil {
		t.Fatal("peer candidate review output")
	}
	t.Logf("candidate review only; not a trust root: %s (descriptor=%d bytes, template_positions=%d)", dir, len(b), len(positions))
}

func auditPeerCandidateSources(t *testing.T, raw []byte) {
	t.Helper()
	decoded, err := strictJSON(raw)
	if err != nil {
		t.Fatal("peer candidate source audit JSON")
	}
	schemas := decoded.(object)["schema"].(object)
	bodyPattern := regexp.MustCompile(`(?s)CREATE FUNCTION ([a-z0-9_]+)\([^;]*? AS \$\$(.*?)\$\$;`)
	for _, component := range []paymentsandboxschema.Component{paymentsandboxschema.Mock, paymentsandboxschema.Receiver} {
		scope := string(component)
		sql, err := paymentsandboxschema.SQL(component)
		if err != nil {
			t.Fatal("peer candidate source audit SQL")
		}
		inventory, err := paymentsandboxschema.Inventory(component)
		if err != nil {
			t.Fatal("peer candidate source audit inventory")
		}
		bodies := map[string]string{}
		for _, match := range bodyPattern.FindAllStringSubmatch(sql, -1) {
			if _, dup := bodies[match[1]]; dup {
				t.Fatal("peer candidate duplicate frozen function name")
			}
			bodies[match[1]] = match[2]
		}
		if len(bodies) != len(inventory.Functions) {
			t.Fatal("peer candidate incomplete frozen function extraction")
		}
		functions := schemas[scope].(object)["functions"].([]any)
		if len(functions) != len(bodies) {
			t.Fatal("peer candidate function set differs from frozen SQL")
		}
		wantHashes := map[string]string{}
		for _, f := range inventory.Functions {
			wantHashes[f.Name] = f.SourceSHA256
		}
		for _, item := range functions {
			f := item.(object)
			name := f["identity"].(object)["name"].(string)
			body, ok := bodies[name]
			if !ok || f["prosrc"] != body || f["source_sha256"] != digest([]byte(body)) || f["source_sha256"] != wantHashes[name] {
				t.Fatal("peer candidate raw function body differs from frozen SQL/inventory")
			}
		}
		constraints := map[string]string{}
		for _, c := range inventory.Constraints {
			constraints[c.Table+"/"+c.Name] = c.Definition
		}
		checks := 0
		for _, item := range schemas[scope].(object)["constraints"].([]any) {
			c := item.(object)
			if c["expression"] == nil {
				continue
			}
			checks++
			expression, ok := c["expression"].(string)
			if !ok {
				m, ok := c["expression"].(object)
				if !ok || !exactKeys(m, "parts") {
					t.Fatal("peer candidate CHECK template shape")
				}
				parts, ok := m["parts"].([]any)
				if !ok {
					t.Fatal("peer candidate CHECK template parts")
				}
				var out strings.Builder
				for i, raw := range parts {
					p, ok := raw.(object)
					if !ok {
						t.Fatal("peer candidate CHECK part")
					}
					if symbol, yes := p["schema"]; yes {
						if !exactKeys(p, "schema") || symbol != scope || i+1 >= len(parts) {
							t.Fatal("peer candidate CHECK cross-schema token")
						}
						continue
					}
					literal, ok := p["literal"].(string)
					if !ok || !exactKeys(p, "literal") {
						t.Fatal("peer candidate CHECK literal")
					}
					if i > 0 {
						if _, schema := parts[i-1].(object)["schema"]; schema {
							if !strings.HasPrefix(literal, ".") {
								t.Fatal("peer candidate CHECK schema token is not a qualified function")
							}
							literal = literal[1:]
						}
					}
					out.WriteString(literal)
				}
				expression = out.String()
			}
			name := c["identity"].(object)["name"].(string)
			if constraints[name] != "CHECK ("+expression+")" {
				t.Fatal("peer candidate CHECK expression differs from frozen own-schema inventory")
			}
		}
		wantChecks := 0
		for _, c := range inventory.Constraints {
			if c.Type == "c" {
				wantChecks++
			}
		}
		if checks != wantChecks {
			t.Fatal("peer candidate CHECK inventory incomplete")
		}
	}
}

func capturePeerTemplates(t *testing.T, v any, path, field string, identifiers map[string]string, positions *[]peerTemplatePosition) any {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		out := document{}
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out[key] = capturePeerTemplates(t, x[key], path+"/"+key, key, identifiers, positions)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = capturePeerTemplates(t, item, path+"/"+strconv.Itoa(i), field, identifiers, positions)
		}
		return out
	case string:
		contains := false
		for identifier := range identifiers {
			contains = contains || strings.Contains(x, identifier)
		}
		if !contains {
			return x
		}
		if !isDeparseField(field) {
			t.Fatal("peer candidate live schema in an unregistered non-deparse field")
		}
		parts := capturePeerSQLParts(t, x, identifiers)
		for i, part := range parts {
			if part.Schema != "" {
				*positions = append(*positions, peerTemplatePosition{Path: path, Part: i, Schema: part.Schema})
			}
		}
		return sqlTemplate{Parts: parts}
	default:
		return v
	}
}

// Candidate generation recognizes schema identifier tokens followed by a dot;
// quoted literals remain literal. Actual runtime SQL is never rewritten.
func capturePeerSQLParts(t *testing.T, sql string, identifiers map[string]string) []sqlTemplatePart {
	t.Helper()
	parts := []sqlTemplatePart{}
	start := 0
	for i := 0; i < len(sql); {
		if sql[i] == '\'' {
			i++
			for i < len(sql) {
				if sql[i] == '\'' {
					i++
					if i < len(sql) && sql[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
			continue
		}
		begin := i
		if sql[i] == '"' {
			i++
			for i < len(sql) {
				if sql[i] == '"' {
					i++
					if i < len(sql) && sql[i] == '"' {
						i++
						continue
					}
					break
				}
				i++
			}
		} else if sql[i] == '_' || sql[i] >= 'a' && sql[i] <= 'z' || sql[i] >= 'A' && sql[i] <= 'Z' {
			i++
			for i < len(sql) && (sql[i] == '_' || sql[i] == '$' || sql[i] >= 'a' && sql[i] <= 'z' || sql[i] >= 'A' && sql[i] <= 'Z' || sql[i] >= '0' && sql[i] <= '9') {
				i++
			}
		} else {
			i++
			continue
		}
		if scope, ok := identifiers[sql[begin:i]]; ok && i < len(sql) && sql[i] == '.' {
			if begin > start {
				literal := sql[start:begin]
				parts = append(parts, sqlTemplatePart{Literal: &literal})
			}
			parts = append(parts, sqlTemplatePart{Schema: scope})
			start = i
		}
	}
	if start == 0 {
		t.Fatal("peer candidate schema substring was not a registered identifier token")
	}
	if start < len(sql) {
		literal := sql[start:]
		parts = append(parts, sqlTemplatePart{Literal: &literal})
	}
	return parts
}
