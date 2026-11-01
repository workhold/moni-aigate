package catalog

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPublicCatalogInputBoundsAndCanonicalIDs(t *testing.T) {
	for _, input := range []PublicCatalogFilter{
		{Prefix: strings.Repeat("x", 101)}, {Prefix: "a\x00b"}, {Prefix: string([]byte{0xff})},
		{Protocol: "OPENAI_CHAT"}, {AfterName: "model"}, {AfterID: uuid.NewString()},
		{AfterName: "a\u00a0b", AfterID: uuid.NewString()}, {AfterName: "model", AfterID: uuid.Nil.String()},
		{AfterName: "model", AfterID: strings.ToUpper("11111111-1111-4111-8111-aaaaaaaaaaaa")},
	} {
		if _, err := normalizePublicFilter(input); !errors.Is(err, ErrPublicCatalogInvalid) {
			t.Fatal("invalid storage filter accepted", input, err)
		}
	}
	valid, err := normalizePublicFilter(PublicCatalogFilter{Prefix: "  a b  ", AfterName: "模型", AfterID: uuid.NewString()})
	if err != nil || valid.Prefix != "a b" {
		t.Fatal("literal prefix normalization changed contract", valid, err)
	}
	for _, name := range []string{"", " ", "a\nb", "a\u2003b", "a\x7fb", string([]byte{0xff}), strings.Repeat("界", 34)} {
		if validPublicName(name) {
			t.Fatal("unsafe public name accepted", name)
		}
	}
	for _, name := range []string{"gpt-4.1", "Alpha_1%\\", "模型"} {
		if !validPublicName(name) {
			t.Fatal("valid logical name rejected", name)
		}
	}
	if literalPublicPrefix(`a_%\`) != `a\_\%\\%` {
		t.Fatal("literal prefix escaping lost wildcard or backslash", literalPublicPrefix(`a_%\`))
	}
}

func TestPublicCatalogUnavailablePriceHasExplicitNulls(t *testing.T) {
	body, err := json.Marshal(PublicPricing{Status: "unavailable", Currency: "CNY", Unit: "micro_cny_per_million_tokens"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(body, &fields); err != nil || len(fields) != 7 {
		t.Fatal(string(body), err)
	}
	for _, key := range []string{"price_version_id", "input_rate", "output_rate", "effective_at"} {
		if string(fields[key]) != "null" {
			t.Fatal("missing pricing field is not explicit null", key, string(body))
		}
	}
}
