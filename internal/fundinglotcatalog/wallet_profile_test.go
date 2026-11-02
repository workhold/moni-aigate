package fundinglotcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestWalletProfileStaticRootsAndExactDelta(t *testing.T) {
	var roots map[string]struct {
		Base     string                                    `json:"base_sha256"`
		Expected string                                    `json:"expected_sha256"`
		Variants map[string]struct{ Schema, Roles string } `json:"variants"`
	}
	b, err := os.ReadFile("../../docs/verification/runtime-read-profile-roots.json")
	if err != nil || json.Unmarshal(b, &roots) != nil {
		t.Fatal("independent static roots artifact")
	}
	for _, mode := range []Mode{Ordinary, Sandbox} {
		name := "ordinary"
		if mode == Sandbox {
			name = "sandbox"
		}
		base, baseRoot, err := loadExpected(Pins{Mode: mode})
		if err != nil || baseRoot != roots[name].Base {
			t.Fatal("frozen base root")
		}
		current, root, err := loadExpectedProfile(Pins{Mode: mode}, WalletReadV2)
		if err != nil || root != roots[name].Expected || current == base {
			t.Fatal("explicit current profile unavailable")
		}
		if len(base.Variants) != len(current.Variants) {
			t.Fatal("variant set changed")
		}
		for i, v := range current.Variants {
			old := base.Variants[i]
			expected := roots[name].Variants[v.Name]
			if digest(v.schemaCanonical) != expected.Schema || digest(v.rolesCanonical) != expected.Roles {
				t.Fatal("variant roots differ from independent fixed artifact")
			}
			if !bytes.Equal(old.schemaCanonical, v.schemaCanonical) || !same(old.Schema, v.Schema) {
				t.Fatal("schema changed in ACL-only profile")
			}
			// Compare by record identity, excluding only the twelve reviewed changed or
			// added app records. Peers permit only four mirrored dependency records.
			a := old.Roles["application"].(object)
			b := v.Roles["application"].(object)
			changes := map[string]int{}
			for category, value := range a {
				if category != "column_acl" && category != "effective_privileges" && category != "acl_dependencies" {
					if !same(value, b[category]) {
						t.Fatal("unreviewed application role change")
					}
					continue
				}
				before := map[string]any{}
				for _, x := range value.([]any) {
					id, e := recordIdentity(x)
					if e != nil {
						t.Fatal(e)
					}
					before[id] = x
				}
				for _, x := range b[category].([]any) {
					id, e := recordIdentity(x)
					if e != nil {
						t.Fatal(e)
					}
					old, exists := before[id]
					delete(before, id)
					if exists && same(old, x) {
						continue
					}
					r := x.(object)
					obj := r["object"].(object)
					if obj["scope"] != "application" || obj["name"] != "payment_query_conflicts" {
						t.Fatal("delta escaped target relation")
					}
					col, _ := obj["column"].(string)
					sub, ok := number(obj["sub_id"])
					if !ok || sub < 1 || sub > 4 || []string{"id", "verified_query_id", "conflicting_digest", "received_at"}[sub-1] != col {
						t.Fatal("delta escaped four physical columns")
					}
					if category == "column_acl" {
						entries := r["entries"].([]any)
						if !exists || r["acl_is_null"] != false || len(entries) != 1 || !same(entries[0], object{"grantor": object{"class": "role", "scope": "role", "name": "application_owner"}, "grantee": object{"class": "role", "scope": "role", "name": "application"}, "privilege": "SELECT", "grant_option": false}) || !same(r["default_entries"], []any{}) {
							t.Fatal("column ACL differs from reviewed delta")
						}
					} else if category == "effective_privileges" {
						if !exists || !same(r["role"], object{"class": "role", "scope": "role", "name": "application"}) || r["kind"] != "column" || !same(r["privileges"], []bool{true, false, false, false}) || !same(r["grant_options"], []bool{false, false, false, false}) {
							t.Fatal("effective privilege escaped SELECT")
						}
					} else {
						if exists || r["catalog"] != "pg_shdepend" || r["kind"] != "a" || !same(r["role"], object{"class": "role", "scope": "role", "name": "application"}) || !same(r["multiplicity"], 1) {
							t.Fatal("ACL dependency changed")
						}
					}
					changes[category]++
				}
				if len(before) != 0 {
					t.Fatal("profile removed old records")
				}
			}
			if changes["column_acl"] != 4 || changes["effective_privileges"] != 4 || changes["acl_dependencies"] != 4 {
				t.Fatal("profile delta incomplete")
			}
			for peer, value := range old.Roles {
				if peer == "application" {
					continue
				}
				before := value.(object)
				after := v.Roles[peer].(object)
				for category, value := range before {
					if category != "acl_dependencies" {
						if !same(value, after[category]) {
							t.Fatal("peer content permissions or objects changed")
						}
						continue
					}
					oldDependencies := map[string]any{}
					for _, dep := range value.([]any) {
						id, _ := recordIdentity(dep)
						oldDependencies[id] = dep
					}
					added := 0
					for _, dep := range after[category].([]any) {
						id, _ := recordIdentity(dep)
						if old, ok := oldDependencies[id]; ok {
							if !same(old, dep) {
								t.Fatal("peer old dependency changed")
							}
							delete(oldDependencies, id)
							continue
						}
						// All three scope queries close over the same eight roles:
						// peer additions must be exact mirrors of app additions.
						matched := false
						for sub, column := range []string{"id", "verified_query_id", "conflicting_digest", "received_at"} {
							wanted := object{"catalog": "pg_shdepend", "kind": "a", "multiplicity": 1,
								"object": object{"class": "relation", "scope": "application", "name": "payment_query_conflicts", "column": column, "sub_id": sub + 1},
								"role":   object{"class": "role", "scope": "role", "name": "application"}}
							if same(dep, wanted) {
								matched = true
							}
						}
						if !matched {
							t.Fatal("peer dependency is not an exact application mirror")
						}
						added++
					}
					if added != 4 || len(oldDependencies) != 0 {
						t.Fatal("peer dependency mirrors incomplete")
					}
				}
			}
		}
		again, againRoot, err := loadExpectedProfile(Pins{Mode: mode}, WalletReadV2)
		if err != nil || again != current || againRoot != root {
			t.Fatal("immutable profile cache unstable")
		}
	}
	if digest(ordinaryExpectedJSON) != ordinaryExpectedSHA || digest(sandboxExpectedJSON) != sandboxExpectedSHA {
		t.Fatal("legacy bytes changed")
	}
}

func TestWalletProfileRejectsUnknownAndInvalidTransactions(t *testing.T) {
	for _, profile := range []Profile{0, 3, 255} {
		if d, r, e := loadExpectedProfile(Pins{Mode: Ordinary}, profile); d != nil || r != "" || e != ErrCatalog {
			t.Fatal("unknown profile accepted")
		}
		if p, e := VerifySnapshotProfileTx(context.Background(), nil, Pins{}, profile); p != nil || e != ErrCatalog {
			t.Fatal("invalid snapshot accepted")
		}
		if p, e := VerifyRuntimeProfileTx(context.Background(), nil, Pins{}, Application, profile); p != nil || e != ErrCatalog {
			t.Fatal("invalid runtime accepted")
		}
	}
	for _, profile := range []Profile{LegacyV1, WalletReadV2} {
		if p, e := VerifySnapshotProfileTx(nil, nil, Pins{}, profile); p != nil || e != ErrCatalog {
			t.Fatal("nil context accepted")
		}
		if p, e := VerifyRuntimeProfileTx(context.Background(), nil, Pins{}, 0, profile); p != nil || e != ErrCatalog {
			t.Fatal("invalid role accepted")
		}
	}
	if (*CatalogProof)(nil).Profile() != 0 || (&CatalogProof{}).Profile() != 0 {
		t.Fatal("zero proof profile")
	}
}

func TestWalletProfileDerivationRejectsDamagedBase(t *testing.T) {
	base, _, err := loadExpected(Pins{Mode: Ordinary})
	if err != nil {
		t.Fatal("legacy base")
	}
	variant := base.Variants[0]
	for _, category := range []string{"column_acl", "effective_privileges", "acl_dependencies"} {
		for _, damage := range []string{"missing", "duplicate", "altered"} {
			t.Run(category+"/"+damage, func(t *testing.T) {
				// A separately decoded copy allows deliberate damage without mutating the
				// process-wide immutable caches used by other tests.
				raw, e := canonical(variant.Roles)
				if e != nil {
					t.Fatal(e)
				}
				value, e := decode(raw)
				if e != nil {
					t.Fatal(e)
				}
				roles := value.(object)
				app := roles["application"].(object)
				rows := app[category].([]any)
				target := -1
				for i, x := range rows {
					r := x.(object)
					o, ok := r["object"].(object)
					if !ok || o["scope"] != "application" || o["name"] != "payment_query_conflicts" || o["column"] != "id" {
						continue
					}
					if category == "effective_privileges" && !same(r["role"], object{"class": "role", "scope": "role", "name": "application"}) {
						continue
					}
					target = i
					break
				}
				if category == "acl_dependencies" {
					// Legacy has no such APP dependency. An existing new identity, whatever
					// its multiplicity, must never be silently merged or replaced.
					dep := object{"catalog": "pg_shdepend", "object": object{"class": "relation", "scope": "application", "name": "payment_query_conflicts", "sub_id": 1, "column": "id"}, "role": object{"class": "role", "scope": "role", "name": "application"}, "kind": "a", "multiplicity": 2}
					switch damage {
					case "missing":
						app[category] = nil
					case "duplicate":
						dep["multiplicity"] = 1
						app[category] = append(rows, dep, dep)
					case "altered":
						app[category] = append(rows, dep)
					}
				} else {
					if target < 0 {
						t.Fatal("damage target absent")
					}
					switch damage {
					case "missing":
						app[category] = append(rows[:target], rows[target+1:]...)
					case "duplicate":
						app[category] = append(rows, rows[target])
					case "altered":
						rows[target].(object)["acl_is_null"] = false
					}
				}
				if _, e := walletRoleDelta(roles); e != ErrCatalog {
					t.Fatal("damaged base accepted")
				}
			})
		}
	}
	if _, e := deriveWalletExpected(base, string(bytes.Repeat([]byte{'0'}, 64))); e != ErrCatalog {
		t.Fatal("unreviewed root accepted")
	}
}

func TestWalletProfilePeerDependencyMirrorsRejectDamagedBase(t *testing.T) {
	base, _, err := loadExpected(Pins{Mode: Sandbox})
	if err != nil {
		t.Fatal("sandbox legacy base")
	}
	for _, peer := range []string{"mock", "receiver"} {
		for _, damage := range []string{"preexisting", "duplicate", "wrong_scope", "wrong_role", "multiplicity"} {
			t.Run(peer+"/"+damage, func(t *testing.T) {
				raw, err := canonical(base.Variants[0].Roles)
				if err != nil {
					t.Fatal(err)
				}
				value, err := decode(raw)
				if err != nil {
					t.Fatal(err)
				}
				roles := value.(object)
				p := roles[peer].(object)
				ref := object{"class": "relation", "scope": "application", "name": "payment_query_conflicts", "sub_id": 1, "column": "id"}
				role := object{"class": "role", "scope": "role", "name": "application"}
				dep := object{"catalog": "pg_shdepend", "object": ref, "role": role, "kind": "a", "multiplicity": 1}
				switch damage {
				case "wrong_scope":
					ref["scope"] = peer
				case "wrong_role":
					role["name"] = "projector"
				case "multiplicity":
					dep["multiplicity"] = 2
				}
				p["acl_dependencies"] = append(p["acl_dependencies"].([]any), dep)
				if damage == "duplicate" {
					p["acl_dependencies"] = append(p["acl_dependencies"].([]any), dep)
				}
				copyOfBase := *base
				copyOfBase.Variants = append([]descriptorVariant(nil), base.Variants...)
				copyOfBase.Variants[0].Roles = roles
				if _, err := deriveWalletExpected(&copyOfBase, walletSandboxExpectedSHA); err != ErrCatalog {
					t.Fatal("damaged peer dependency accepted by fixed complete root")
				}
			})
		}
	}
}
