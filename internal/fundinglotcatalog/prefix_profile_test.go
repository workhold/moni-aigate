package fundinglotcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
)

func TestPeriodicPrefixStaticRootsAndInventory(t *testing.T) {
	var roots map[string]struct {
		Base     string                                    `json:"base_sha256"`
		Expected string                                    `json:"expected_sha256"`
		Variants map[string]struct{ Schema, Roles string } `json:"variants"`
	}
	raw, err := os.ReadFile("../../docs/verification/periodic-prefix-profile-roots.json")
	if err != nil || json.Unmarshal(raw, &roots) != nil {
		t.Fatal("independent periodic-prefix roots artifact")
	}
	for _, mode := range []Mode{Ordinary, Sandbox} {
		base, baseRoot, err := loadExpectedProfile(Pins{Mode: mode}, WalletReadV2)
		if err != nil {
			t.Fatal("original wallet profile unavailable")
		}
		name := base.Mode
		current, root, err := loadPeriodicPrefixExpected(Pins{Mode: mode})
		if err != nil || current == nil || current == base || root != roots[name].Expected || baseRoot != roots[name].Base {
			t.Fatalf("fixed V3 descriptor unavailable for %s", name)
		}
		if len(current.Variants) != len(base.Variants) || current.FormatVersion != 1 || current.PostgresMajor != 16 {
			t.Fatal("descriptor envelope changed")
		}
		for i, variant := range current.Variants {
			oldVariant := base.Variants[i]
			oldSchema, err := canonical(oldVariant.Schema)
			if err != nil || !bytes.Equal(oldSchema, oldVariant.schemaCanonical) {
				t.Fatal("V3 changed immutable parent schema nodes")
			}
			oldRoles, err := canonical(oldVariant.Roles)
			if err != nil || !bytes.Equal(oldRoles, oldVariant.rolesCanonical) {
				t.Fatal("V3 changed immutable parent role nodes")
			}
			if variant.Name != base.Variants[i].Name || digest(variant.schemaCanonical) != roots[name].Variants[variant.Name].Schema || digest(variant.rolesCanonical) != roots[name].Variants[variant.Name].Roles {
				t.Fatal("V3 variant tuple differs from independent artifact")
			}
			old, next := base.Variants[i].Schema["application"].(object), variant.Schema["application"].(object)
			for category, added := range map[string]int{"relations": 21, "types": 6, "functions": 12, "constraints": 55, "indexes": 15, "triggers": 71} {
				if len(next[category].([]any))-len(old[category].([]any)) != added {
					t.Fatalf("%s complete catalog inventory", category)
				}
			}
			tables, columns, indexes, users := 0, 0, 0, 0
			for _, value := range next["relations"].([]any) {
				r := value.(object)
				if r["identity"].(object)["scope"] != "application" {
					continue
				}
				if r["kind"] == "r" {
					tables++
					columns += len(r["attributes"].([]any))
				} else if r["kind"] == "i" {
					indexes++
				}
			}
			for _, value := range next["triggers"].([]any) {
				if value.(object)["internal"] == false {
					users++
				}
			}
			if tables != 128 || columns != 1173 || indexes != 343 || users != 274 || len(next["constraints"].([]any)) != 1197 || len(next["functions"].([]any)) != 209 || len(next["types"].([]any)) != 258 {
				t.Fatal("full application counts differ from static candidate SQL")
			}
			for scope, oldSchema := range base.Variants[i].Schema {
				if scope != "application" && !same(oldSchema, variant.Schema[scope]) {
					t.Fatal("V3 changed peer schema")
				}
			}
		}
		again, againRoot, err := loadPeriodicPrefixExpected(Pins{Mode: mode})
		if err != nil || again != current || againRoot != root {
			t.Fatal("V3 immutable cache unstable")
		}
	}
	if digest(ordinaryExpectedJSON) != ordinaryExpectedSHA || digest(sandboxExpectedJSON) != sandboxExpectedSHA {
		t.Fatal("historical catalog material changed")
	}
}

func TestPeriodicPrefixFunctionSourcesAndACLBoundary(t *testing.T) {
	sql, err := periodicbudgetprefixschema.SQL()
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{}
	for _, m := range regexp.MustCompile(`(?s)CREATE FUNCTION (pb_prefix_[a-z_]+)\(.*?\)\s*RETURNS (?:trigger|text) LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS \$\$(.*?)\$\$;`).FindAllStringSubmatch(sql, -1) {
		bodies[m[1]] = m[2]
	}
	if len(bodies) != 12 {
		t.Fatal("fixed SQL function extraction")
	}
	for _, mode := range []Mode{Ordinary, Sandbox} {
		d, _, err := loadPeriodicPrefixExpected(Pins{Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range d.Variants {
			schema, roles := v.Schema["application"].(object), v.Roles["application"].(object)
			seen := 0
			for _, value := range schema["functions"].([]any) {
				f := value.(object)
				name := f["identity"].(object)["name"].(string)
				if body, ok := bodies[name]; ok {
					seen++
					if f["prosrc"] != body || f["source_sha256"] != digest([]byte(body)) || f["definer"] != false || f["volatility"] != "v" {
						t.Fatal("V3 function differs from frozen raw source/invoker contract")
					}
				}
			}
			if seen != 12 {
				t.Fatal("V3 function inventory incomplete")
			}
			for _, value := range roles["effective_privileges"].([]any) {
				r := value.(object)
				ref, who := r["object"].(object), r["role"].(object)
				name, _ := ref["name"].(string)
				newObject := strings.HasPrefix(name, "periodic_budget_observation_") || strings.HasPrefix(name, "_periodic_budget_observation_") || strings.HasPrefix(name, "pb_prefix_")
				if newObject && who["name"] != "application" && who["name"] != "application_owner" {
					for _, privilege := range r["privileges"].([]any) {
						if privilege != false {
							t.Fatal("new V3 surface grants worker, projector, or peer content")
						}
					}
				}
				if ref["column"] == "observation_id" || ref["column"] == "observation_created_xid" {
					if who["name"] == "application" && r["privileges"].([]any)[2] != false {
						t.Fatal("APP can update source observation identity")
					}
				}
			}
		}
	}
}

func TestPeriodicPrefixBuiltinSelectionAndClosedOldProfiles(t *testing.T) {
	if len(periodicPrefixBuiltinObjects) != 3 {
		t.Fatal("V3 builtin closure changed")
	}
	for _, mode := range []Mode{Ordinary, Sandbox} {
		current, _, err := loadPeriodicPrefixExpected(Pins{Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		for key, raw := range periodicPrefixBuiltinObjects {
			if _, ok := reviewedBuiltinObjects[key]; ok {
				t.Fatal("V3 builtin entered global legacy acceptance table")
			}
			if value, ok := expectedBuiltinObject(current, key); !ok || value != raw {
				t.Fatal("fixed V3 builtin unavailable")
			}
			for _, profile := range []Profile{LegacyV1, WalletReadV2} {
				old, _, err := loadExpectedProfile(Pins{Mode: mode}, profile)
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := expectedBuiltinObject(old, key); ok {
					t.Fatal("old profile accepts new dependency")
				}
				// This must reject before touching the deliberately absent tx;
				// a new allowed builtin would advance into a database query.
				resolver := &resolver{refs: map[refKey]object{}, attributes: map[uint32]map[uint32]string{}}
				ref := object{"$ref": []any{key.class, key.oid, key.sub}}
				if primeBuiltins(context.Background(), nil, resolver, &collectorBudget{}, old, ref) != ErrCatalog {
					t.Fatal("old collector accepts new dependency reference")
				}
			}
		}
		if d, r, err := loadExpectedProfile(Pins{Mode: mode}, PeriodicPrefixV3); d != nil || r != "" || err != ErrCatalog {
			t.Fatal("old generic loader accepts dedicated V3")
		}
	}
	if _, ok := expectedBuiltinObject(nil, refKey{2616, 10053, 0}); ok {
		t.Fatal("zero descriptor receives V3 builtin")
	}
}

func TestPeriodicPrefixExactDeltaReplacementAndInstallationBoundary(t *testing.T) {
	variant := descriptorVariant{Schema: document{"application": object{"relations": []any{
		object{"identity": object{"class": "relation", "scope": "application", "name": "example"}, "value": 1},
	}}}}
	old := variant.Schema["application"].(object)["relations"].([]any)[0]
	wrong := object{"identity": old.(object)["identity"], "value": 2}
	change := object{"family": "schema", "scope": "application", "category": "relations", "remove": []any{wrong}, "add": []any{}}
	if applyPeriodicPrefixChange(&variant, change, map[string]bool{}) != ErrCatalog {
		t.Fatal("identity alone replaced a nonmatching full old value")
	}
	change["remove"], change["add"] = []any{}, []any{wrong}
	if applyPeriodicPrefixChange(&variant, change, map[string]bool{}) != ErrCatalog {
		t.Fatal("duplicate record identity accepted")
	}
	if len(installationTables) != 16 {
		t.Fatal("original installation output set changed")
	}
	for _, table := range installationTables {
		if strings.HasPrefix(table, "periodic_budget_") {
			t.Fatal("business periodic evidence became an empty installation output")
		}
	}
}

func TestPeriodicPrefixDeltaRejectsWrongParentAndTampering(t *testing.T) {
	base, parent, err := loadExpectedProfile(Pins{Mode: Ordinary}, WalletReadV2)
	if err != nil {
		t.Fatal(err)
	}
	if v, e := derivePeriodicPrefixExpected(base, strings.Repeat("0", 64), periodicPrefixOrdinaryExpectedSHA, periodicPrefixDeltaJSON); v != nil || e != ErrCatalog {
		t.Fatal("wrong fixed parent root accepted")
	}
	tampered := bytes.Clone(periodicPrefixDeltaJSON)
	tampered[len(tampered)/2] ^= 1
	if v, e := derivePeriodicPrefixExpected(base, parent, periodicPrefixOrdinaryExpectedSHA, tampered); v != nil || e != ErrCatalog {
		t.Fatal("tampered delta accepted")
	}
	if v, e := derivePeriodicPrefixExpected(base, parent, strings.Repeat("0", 64), periodicPrefixDeltaJSON); v != nil || e != ErrCatalog {
		t.Fatal("wrong final root accepted")
	}
	if d, r, e := loadPeriodicPrefixExpected(Pins{}); d != nil || r != "" || e != ErrCatalog {
		t.Fatal("unknown mode accepted")
	}
	for _, ctx := range []context.Context{nil, context.Background()} {
		if p, err := VerifyPeriodicPrefixSnapshotTx(ctx, nil, Pins{}); p != nil || err != ErrCatalog {
			t.Fatal("invalid snapshot transaction accepted")
		}
		if p, err := VerifyPeriodicPrefixInstallationTx(ctx, nil, Pins{}); p != nil || err != ErrCatalog {
			t.Fatal("invalid installation transaction accepted")
		}
		if p, err := VerifyPeriodicPrefixRuntimeTx(ctx, nil, Pins{}, 0); p != nil || err != ErrCatalog {
			t.Fatal("invalid runtime role accepted")
		}
	}
}
