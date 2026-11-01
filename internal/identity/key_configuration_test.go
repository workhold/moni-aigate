package identity

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func keyConfigurationString(s string) *string { return &s }

func TestKeyConfigurationInputRequiresFullReplacement(t *testing.T) {
	valid := `{"expected_revision":"1","name":"key","scopes":["*"],"allowed_regions":["global"],"lifetime_limit":null,"expires_at":null}`
	var in KeyConfigurationInput
	if err := json.Unmarshal([]byte(valid), &in); err != nil || in.ExpectedRevision != "1" || in.LifetimeLimit != nil || in.ExpiresAt != nil {
		t.Fatalf("full nullable replacement rejected: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(valid), &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"expected_revision", "name", "scopes", "allowed_regions", "lifetime_limit", "expires_at"} {
		t.Run(name, func(t *testing.T) {
			removed := make(map[string]json.RawMessage)
			for k, v := range fields {
				removed[k] = v
			}
			delete(removed, name)
			encoded, _ := json.Marshal(removed)
			if err := json.Unmarshal(encoded, &in); !errors.Is(err, ErrKeyConfigurationInvalid) {
				t.Fatalf("missing field accepted: %v", err)
			}
			if name != "lifetime_limit" && name != "expires_at" {
				removed[name] = json.RawMessage("null")
				encoded, _ = json.Marshal(removed)
				if err := json.Unmarshal(encoded, &in); !errors.Is(err, ErrKeyConfigurationInvalid) {
					t.Fatalf("null field accepted: %v", err)
				}
			}
		})
	}
	for _, bad := range []string{
		strings.Replace(valid, `"expected_revision"`, `"Expected_Revision"`, 1),
		strings.Replace(valid, `"lifetime_limit":null`, `"lifetime_limit":0`, 1),
		strings.Replace(valid, `"expires_at":null`, `"expires_at":""`, 1),
		strings.Replace(valid, `"name":"key"`, `"name":1`, 1),
		strings.Replace(valid, `"scopes":["*"]`, `"scopes":"*"`, 1),
		strings.Replace(valid, `"scopes":["*"]`, `"scopes":[null]`, 1),
		strings.Replace(valid, `"scopes":["*"]`, `"scopes":["*",null]`, 1),
		strings.Replace(valid, `"allowed_regions":["global"]`, `"allowed_regions":[null]`, 1),
		strings.Replace(valid, `"name":"key"`, `"unknown":true,"name":"key"`, 1),
	} {
		if err := json.Unmarshal([]byte(bad), &in); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("malformed field accepted: %v", err)
		}
	}
}

func TestKeyConfigurationLegacyEmptyStringsDoNotAcceptJSONNullElements(t *testing.T) {
	legacy := KeyConfigurationView{Name: "legacy", Scopes: []string{""}, AllowedRegions: []string{""}}
	valid := `{"expected_revision":"1","name":"legacy","scopes":[""],"allowed_regions":[""],"lifetime_limit":null,"expires_at":null}`
	var in KeyConfigurationInput
	if err := json.Unmarshal([]byte(valid), &in); err != nil {
		t.Fatal(err)
	}
	if got, err := normalizeKeyConfiguration(legacy, in); err != nil || !sameKeyConfiguration(got, legacy) {
		t.Fatalf("true legacy string could not round-trip: %+v %v", got, err)
	}
	for _, field := range []string{"scopes", "allowed_regions"} {
		invalid := strings.Replace(valid, `"`+field+`":[""]`, `"`+field+`":[null]`, 1)
		if err := json.Unmarshal([]byte(invalid), &in); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("null accepted as historical empty %s: %v", field, err)
		}
	}
}

func TestKeyConfigurationChangedFieldsNormalizeAndLegacyFieldsRemain(t *testing.T) {
	a, b := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	oldExpiry := time.Date(2020, 1, 2, 3, 4, 5, 123456000, time.UTC)
	legacy := KeyConfigurationView{Name: "\nold name\t", Scopes: []string{"old-scope", ""}, AllowedRegions: []string{}, ExpiresAt: &oldExpiry}
	in := KeyConfigurationInput{Name: legacy.Name, Scopes: legacy.Scopes, AllowedRegions: legacy.AllowedRegions, ExpiresAt: &oldExpiry}
	got, err := normalizeKeyConfiguration(legacy, in)
	if err != nil || !sameKeyConfiguration(legacy, got) {
		t.Fatalf("legacy no-op rejected or cleaned: %+v %v", got, err)
	}
	in.Name, in.Scopes, in.AllowedRegions = "  new key  ", []string{b, a, b}, []string{" us-east ", "global", "us-east"}
	got, err = normalizeKeyConfiguration(legacy, in)
	if err != nil || got.Name != "new key" || !reflect.DeepEqual(got.Scopes, []string{a, b}) || !reflect.DeepEqual(got.AllowedRegions, []string{"global", "us-east"}) {
		t.Fatalf("normalization=%+v err=%v", got, err)
	}
	for _, name := range []string{"", " \t ", "with\ncontrol", "with\x7fcontrol", string([]byte{0xff}), strings.Repeat("中", 34)} {
		bad := in
		bad.Name = name
		if _, err = normalizeKeyConfiguration(legacy, bad); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("invalid name accepted: %v", err)
		}
	}
	for _, scopes := range [][]string{nil, {}, {""}, {"*", a}, {"*", "*"}, {"not-a-uuid"}, {strings.ReplaceAll(a, "-", "")}, {strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")}} {
		bad := in
		bad.Scopes = scopes
		if _, err = normalizeKeyConfiguration(legacy, bad); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("invalid scopes accepted: %v", err)
		}
	}
	for _, regions := range [][]string{{""}, {" \t"}, {"us\nwest"}, {"*", "global"}, {strings.Repeat("r", 81)}, make([]string, 33)} {
		bad := in
		bad.AllowedRegions = regions
		if _, err = normalizeKeyConfiguration(legacy, bad); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("invalid regions accepted: %v", err)
		}
	}
	in.Scopes, in.AllowedRegions = []string{"*"}, []string{" * "}
	if got, err = normalizeKeyConfiguration(legacy, in); err != nil || !reflect.DeepEqual(got.AllowedRegions, []string{"*"}) {
		t.Fatalf("wildcard rejected: %+v %v", got, err)
	}
}

func TestKeyConfigurationExactLimitsAndMicrosecondTime(t *testing.T) {
	for _, value := range []string{"", "-1", "+1", "01", " 1", "1.0", "9223372036854775808"} {
		if _, err := parseKeyConfigurationLimit(&value); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("noncanonical amount accepted: %v", err)
		}
	}
	for _, value := range []string{"0", "9007199254740993", "9223372036854775807"} {
		if _, err := parseKeyConfigurationLimit(&value); err != nil {
			t.Fatalf("exact amount rejected: %v", err)
		}
	}
	if !keyBudgetBelowUsage("9223372036854775807", "9223372036854775807", "1") || keyBudgetBelowUsage("40", "17", "23") || !keyBudgetBelowUsage("39", "17", "23") {
		t.Fatal("budget comparison overflowed or lost precision")
	}
	input := time.Date(2030, 1, 2, 3, 4, 5, 123456789, time.FixedZone("test", 8*3600))
	got := keyConfigurationTime(&input)
	if got.Nanosecond() != 123456000 || got.Location() != time.UTC || !input.Equal(got.Add(789*time.Nanosecond)) {
		t.Fatal("expiry did not preserve PostgreSQL microsecond instant")
	}
}

func TestKeyConfigurationRejectsNewUTCYearOverflowWithoutCleaningLegacy(t *testing.T) {
	before := KeyConfigurationView{Name: "test", Scopes: []string{"*"}, AllowedRegions: []string{"global"}}
	in := keyConfigurationReplacement(before)
	for _, value := range []string{"9999-12-31T23:59:59-23:59", "0000-01-01T00:00:00+23:59"} {
		expires, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		in.ExpiresAt = &expires
		if _, err = normalizeKeyConfiguration(before, in); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("nonserializable UTC expiry accepted: %v", err)
		}
		legacy := before
		legacy.ExpiresAt = keyConfigurationTime(&expires)
		in = keyConfigurationReplacement(legacy)
		in.Name = "other field changed"
		if _, err = normalizeKeyConfiguration(legacy, in); err != nil {
			t.Fatalf("unchanged legacy expiry was retroactively constrained: %v", err)
		}
		in = keyConfigurationReplacement(before)
	}
	for _, value := range []string{"9999-12-31T23:59:59Z", "0000-01-01T00:00:00Z"} {
		expires, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		if !serializableKeyConfigurationTime(keyConfigurationTime(&expires)) {
			t.Fatal("serializable UTC boundary rejected")
		}
	}
}

func TestKeyExpiryJSONStrictRFC3339ForCreateAndConfiguration(t *testing.T) {
	configuration := `{"expected_revision":"1","name":"test","scopes":["*"],"allowed_regions":["global"],"lifetime_limit":null,"expires_at":VALUE}`
	creation := `{"name":"test","scopes":["*"],"expires_at":VALUE}`
	for _, value := range []string{
		`"2030-01-01T00:00:00+24:00"`, `"2030-01-01T00:00:00-24:00"`,
		`"2030-01-01T00:00:00+00:60"`, `"2030-01-01T00:00:00,123Z"`,
		`"2030-01-01T1:00:00Z"`, `"2030-01-01T00:00:00.1234567890Z"`,
		`"2030-02-30T00:00:00Z"`, `"2030-01-01T00:00:60Z"`,
		`"2030-01-01T00:00:00"`, `"2030-01-01t00:00:00z"`, `""`, `true`, `123`,
	} {
		var in KeyConfigurationInput
		if err := json.Unmarshal([]byte(strings.Replace(configuration, "VALUE", value, 1)), &in); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("configuration accepted invalid RFC3339 %s: %v", value, err)
		}
		var created KeyInput
		if err := json.Unmarshal([]byte(strings.Replace(creation, "VALUE", value, 1)), &created); !errors.Is(err, ErrKeyConfigurationInvalid) {
			t.Fatalf("creation accepted invalid RFC3339 %s: %v", value, err)
		}
	}
	for _, value := range []string{`null`, `"2030-01-01T00:00:00Z"`, `"2030-01-01T00:00:00.123456789+23:59"`, `"2030-01-01T00:00:00.1-23:59"`} {
		var in KeyConfigurationInput
		if err := json.Unmarshal([]byte(strings.Replace(configuration, "VALUE", value, 1)), &in); err != nil {
			t.Fatalf("configuration rejected valid RFC3339 %s: %v", value, err)
		}
		var created KeyInput
		if err := json.Unmarshal([]byte(strings.Replace(creation, "VALUE", value, 1)), &created); err != nil {
			t.Fatalf("creation rejected valid RFC3339 %s: %v", value, err)
		}
	}
	var created KeyInput
	if err := json.Unmarshal([]byte(`{"name":"test","scopes":["*"]}`), &created); err != nil || created.ExpiresAt != nil {
		t.Fatalf("creation omitted expiry changed contract: %v", err)
	}
}
