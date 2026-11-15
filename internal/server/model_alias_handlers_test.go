package server

import (
	"strings"
	"testing"
)

func TestModelAliasRequestDecodersAreCanonical(t *testing.T) {
	var create modelAliasCreateBody
	if !decodeModelAliasCreate(strings.NewReader(`{"operation_id":"11111111-1111-4111-8111-111111111111","alias":"gpt-4o"}`), &create) || create.Alias != "gpt-4o" {
		t.Fatal("valid alias create rejected")
	}
	for _, raw := range []string{
		`{"operation_id":"11111111-1111-4111-8111-111111111111","alias":"gpt-4o","extra":"private"}`,
		`{"operation_id":"11111111-1111-4111-8111-111111111111","alias":"gpt 4o"}`,
		`{"operation_id":"11111111-1111-4111-8111-111111111111","alias":"gpt-4o"}{}`,
	} {
		var input modelAliasCreateBody
		if decodeModelAliasCreate(strings.NewReader(raw), &input) {
			t.Fatal("ambiguous alias create accepted", raw)
		}
	}
	var retire modelAliasRetireBody
	if revision, ok := decodeModelAliasRetire(strings.NewReader(`{"operation_id":"22222222-2222-4222-8222-222222222222","expected_revision":"9223372036854775807"}`), &retire); !ok || revision <= 0 {
		t.Fatal("valid alias retirement rejected")
	}
	for _, raw := range []string{
		`{"operation_id":"22222222-2222-4222-8222-222222222222","expected_revision":1}`,
		`{"operation_id":"22222222-2222-4222-8222-222222222222","expected_revision":"01"}`,
		`{"operation_id":"22222222-2222-4222-8222-222222222222","expected_revision":"0"}`,
		`{"operation_id":"22222222-2222-4222-8222-222222222222","expected_revision":"1","extra":true}`,
	} {
		if revision, ok := decodeModelAliasRetire(strings.NewReader(raw), &retire); ok || revision != 0 {
			t.Fatal("invalid alias retirement accepted", raw, revision)
		}
	}
}
