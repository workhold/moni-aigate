package procurementattemptrules

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
)

func rulesBodies(t *testing.T) map[string]string {
	t.Helper()
	raw := SQLTemplate()
	bodies := make(map[string]string)
	pattern := regexp.MustCompile(`(?s)CREATE FUNCTION \{\{MONI_SCHEMA\}\}\.([a-z0-9_]+)\(.*?\nAS \$([a-z0-9_]+)\$\n(.*?)\n\$([a-z0-9_]+)\$;`)
	for _, match := range pattern.FindAllStringSubmatch(raw, -1) {
		if match[1] != match[2] || match[1] != match[4] || bodies[match[1]] != "" {
			t.Fatalf("function delimiters or inventory changed: %q", match[1])
		}
		bodies[match[1]] = match[3]
	}
	return bodies
}

func TestMaterialsFixedDigestAndFiveFunctionBoundary(t *testing.T) {
	if Version != 1 {
		t.Fatal("rules version changed without a new protocol")
	}
	raw := SQLTemplate()
	sum := sha256.Sum256([]byte(raw))
	if got := hex.EncodeToString(sum[:]); got != MaterialSHA256 {
		t.Fatalf("material is not its reviewed fixed bytes: got %s", got)
	}
	bodies := rulesBodies(t)
	names := []string{"pp_at_uuid_parse_v1", "pp_at_route_json_v1", "pp_at_expected_shape_v1", "pp_at_quote_equal_v1", "pp_at_current_quote_v1"}
	if len(bodies) != len(names) || strings.Count(raw, "CREATE FUNCTION ") != 5 {
		t.Fatal("unexpected SQL surface")
	}
	for _, name := range names {
		if bodies[name] == "" {
			t.Fatalf("missing frozen function %s", name)
		}
	}
	if strings.Count(raw, "LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE SECURITY INVOKER") != 2 || strings.Count(raw, "LANGUAGE sql IMMUTABLE PARALLEL SAFE SECURITY INVOKER") != 2 || strings.Count(raw, "LANGUAGE sql VOLATILE PARALLEL UNSAFE SECURITY INVOKER") != 1 {
		t.Fatal("pure rules or current quote attributes changed")
	}
	if strings.Contains(raw, " STRICT") {
		t.Fatal("NULL union checks must execute explicitly")
	}
	for _, forbidden := range []string{"SECURITY DEFINER", "CREATE TABLE", "CREATE TRIGGER", "ALTER TABLE", "DROP TABLE", "GRANT ", "INSERT INTO", "UPDATE ", "DELETE FROM", "TRUNCATE ", "EXECUTE ", "SET search_path"} {
		// REVOKE EXECUTE is the only allowed occurrence of the EXECUTE token.
		check := strings.ReplaceAll(raw, "REVOKE EXECUTE ON FUNCTION", "REVOKE ON FUNCTION")
		if strings.Contains(check, forbidden) {
			t.Fatalf("rules acquired a writer/installer surface: %s", forbidden)
		}
	}
	if strings.Count(raw, "REVOKE EXECUTE ON FUNCTION") != 1 || !strings.HasSuffix(raw, "FROM PUBLIC;\n") {
		t.Fatal("PUBLIC invocation was not revoked")
	}
	if strings.Contains(raw, "CREATE FUNCTION {{MONI_SCHEMA}}.pp_json_string_v1") {
		t.Fatal("identity dependency was redefined")
	}
}

func TestMaterialRouteAndExpectedRulesRemainByteBased(t *testing.T) {
	b := rulesBodies(t)
	route := b["pp_at_route_json_v1"]
	for _, required := range []string{"1000", "262144", "18446744073709551615", "-9223372036854775808", "9223372036854775807", "pg_catalog.octet_length(result)", "pg_catalog.convert_to(result,'UTF8')", "COLLATE pg_catalog.\"C\"", "selected route mismatch", "route needs one selected candidate"} {
		if !strings.Contains(route, required) {
			t.Fatalf("route byte/integer boundary missing %q", required)
		}
	}
	if strings.Count(route, "{{MONI_SCHEMA}}.pp_json_string_v1(") != 3 || strings.Contains(route, "weight::pg_catalog.int8") {
		t.Fatal("route text encoding or uint64 was narrowed")
	}
	last := -1
	for _, field := range []string{`'{"channel_id":'`, `',"version_id":'`, `',"priority":'`, `',"weight":'`, `',"reason":'`} {
		pos := strings.Index(route, field)
		if pos <= last {
			t.Fatal("Go Decision output order changed")
		}
		last = pos
	}
	equal := b["pp_at_quote_equal_v1"]
	for _, required := range []string{"has_expected IS TRUE", "actual_basis IS NOT NULL", "pp_at_expected_shape_v1", "pg_catalog.convert_to(coalesce(actual_version,''),'UTF8')", "pg_catalog.convert_to(actual_basis,'UTF8')", "expected_input IS NOT DISTINCT FROM actual_input", "expected_output IS NOT DISTINCT FROM actual_output"} {
		if !strings.Contains(equal, required) {
			t.Fatalf("raw quote equality lost %q", required)
		}
	}
	for _, name := range []string{"pp_at_expected_shape_v1", "pp_at_quote_equal_v1"} {
		body := b[name]
		if strings.Contains(body, "convert_from") || strings.Contains(body, "expected_version::") || strings.Contains(body, "expected_basis::") || strings.Contains(body, "octet_length") {
			t.Fatal("ignored expected bytes were decoded, serialized, or capped")
		}
	}
	uuid := b["pp_at_uuid_parse_v1"]
	if !strings.Contains(uuid, "size OPERATOR(pg_catalog.=) 38") || !strings.Contains(uuid, "core:=pg_catalog.substr(value,2,36)") || strings.Contains(uuid, "::pg_catalog.uuid") {
		t.Fatal("original Parse accepted forms were narrowed")
	}
}

func TestMaterialQuoteUsesOneActualClockAndNoquoteOuterRow(t *testing.T) {
	body := rulesBodies(t)["pp_at_current_quote_v1"]
	if strings.Count(body, "pg_catalog.clock_timestamp()") != 1 || !strings.Contains(body, "WITH instant AS MATERIALIZED") || !strings.Contains(body, "FROM instant LEFT JOIN LATERAL") {
		t.Fatal("quote did not sample one real clock for every result")
	}
	for _, required := range []string{"coalesce(c.basis,'unknown')", "FROM {{MONI_SCHEMA}}.cost_versions AS cv", "cv.channel_id OPERATOR(pg_catalog.=) $1", "cv.upstream_model OPERATOR(pg_catalog.=) $2", "cv.effective_at OPERATOR(pg_catalog.<=) instant.at", "ORDER BY cv.effective_at DESC,cv.created_at DESC,cv.id DESC LIMIT 1", ") AS c ON true"} {
		if !strings.Contains(body, required) {
			t.Fatalf("quote selection/noquote contract missing %q", required)
		}
	}
	if strings.Contains(body, "FOR UPDATE") || strings.Contains(body, "FOR SHARE") || strings.Contains(body, "COLLATE") || strings.Contains(body, ";") {
		t.Fatal("quote acquired a lock, changed legacy order, or added another statement")
	}
}
