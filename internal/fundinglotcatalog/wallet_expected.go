package fundinglotcatalog

import "maps"

// Reviewed base + fixed delta. These roots were independently derived from the
// immutable legacy bytes, not captured from a live installation. See R1-A.
const walletOrdinaryExpectedSHA = "c9466842df4a55490f53b7c0ba9694ee61b5fd5e011912cd2083f2ec191cdf20"
const walletSandboxExpectedSHA = "42a63f7178748ea5dd4ce6964cbe9cd77d0131ca8dad795fbd61a4735b206706"

var walletExpectedCache [3]expectedCacheEntry

func loadExpectedProfile(p Pins, profile Profile) (*descriptorSet, string, error) {
	if profile == LegacyV1 {
		return loadExpected(p)
	}
	if profile != WalletReadV2 || p.Mode != Ordinary && p.Mode != Sandbox {
		return nil, "", ErrCatalog
	}
	c := &walletExpectedCache[p.Mode]
	c.once.Do(func() {
		base, _, err := loadExpected(p)
		if err != nil {
			c.err = ErrCatalog
			return
		}
		c.root = walletOrdinaryExpectedSHA
		if p.Mode == Sandbox {
			c.root = walletSandboxExpectedSHA
		}
		c.value, c.err = deriveWalletExpected(base, c.root)
	})
	if c.err != nil || c.value == nil {
		return nil, "", ErrCatalog
	}
	return c.value, c.root, nil
}

// Only known target records are copied and changed; shared base nodes remain
// immutable. No caller, database value, or external descriptor enters this path.
func deriveWalletExpected(base *descriptorSet, root string) (*descriptorSet, error) {
	if base == nil || len(base.Variants) == 0 || len(root) != 64 {
		return nil, ErrCatalog
	}
	out := &descriptorSet{FormatVersion: base.FormatVersion, PostgresMajor: base.PostgresMajor, Mode: base.Mode}
	variants := make([]any, 0, len(base.Variants))
	for _, v := range base.Variants {
		roles, err := walletRoleDelta(v.Roles)
		if err != nil || validateExpectedDocuments(v.Schema, roles) != nil {
			return nil, ErrCatalog
		}
		raw, err := canonical(roles)
		if err != nil {
			return nil, ErrCatalog
		}
		// Own the exact capacity, like the authenticated legacy canonical spans.
		raw = append([]byte(nil), raw...)
		raw = raw[:len(raw):len(raw)]
		out.Variants = append(out.Variants, descriptorVariant{
			Name: v.Name, Schema: v.Schema, Roles: roles,
			schemaCanonical: v.schemaCanonical, rolesCanonical: raw,
		})
		variants = append(variants, object{"name": v.Name, "schema": v.Schema, "roles": roles})
	}
	raw, err := canonical(object{"format_version": out.FormatVersion, "postgres_major": out.PostgresMajor, "mode": out.Mode, "variants": variants})
	if err != nil || digest(raw) != root {
		return nil, ErrCatalog
	}
	out.bounds, err = deriveCategoryBounds(out)
	if err != nil {
		return nil, ErrCatalog
	}
	return out, nil
}

func walletRoleDelta(base document) (document, error) {
	if len(base) != 1 && len(base) != 3 {
		return nil, ErrCatalog
	}
	app, ok := base["application"].(object)
	if !ok {
		return nil, ErrCatalog
	}
	roles := maps.Clone(base)
	app = maps.Clone(app)
	roles["application"] = app
	for _, name := range []string{"column_acl", "effective_privileges", "acl_dependencies"} {
		records, ok := app[name].([]any)
		if !ok {
			return nil, ErrCatalog
		}
		app[name] = append([]any{}, records...)
	}
	appRole := object{"class": "role", "scope": "role", "name": "application"}
	ownerRole := object{"class": "role", "scope": "role", "name": "application_owner"}
	for sub, column := range []string{"id", "verified_query_id", "conflicting_digest", "received_at"} {
		ref := object{"class": "relation", "scope": "application", "name": "payment_query_conflicts", "sub_id": sub + 1, "column": column}
		oldACL := object{"object": ref, "acl_is_null": true, "entries": []any{}, "default_entries": []any{}}
		newACL := object{"object": ref, "acl_is_null": false, "default_entries": []any{}, "entries": []any{
			object{"grantor": ownerRole, "grantee": appRole, "privilege": "SELECT", "grant_option": false},
		}}
		if replaceWalletRecord(app["column_acl"].([]any), oldACL, newACL) != nil {
			return nil, ErrCatalog
		}
		oldEffective := object{"role": appRole, "object": ref, "kind": "column", "privileges": []any{false, false, false, false}, "grant_options": []any{false, false, false, false}}
		newEffective := maps.Clone(oldEffective)
		newEffective["privileges"] = []any{true, false, false, false}
		if replaceWalletRecord(app["effective_privileges"].([]any), oldEffective, newEffective) != nil {
			return nil, ErrCatalog
		}
		dep := object{"catalog": "pg_shdepend", "object": ref, "role": appRole, "kind": "a", "multiplicity": 1}
		id, err := recordIdentity(dep)
		if err != nil {
			return nil, ErrCatalog
		}
		for _, value := range app["acl_dependencies"].([]any) {
			other, err := recordIdentity(value)
			if err != nil || other == id {
				return nil, ErrCatalog
			}
		}
		app["acl_dependencies"] = append(app["acl_dependencies"].([]any), dep)
	}
	for _, name := range []string{"column_acl", "effective_privileges", "acl_dependencies"} {
		if sortRecords(app[name].([]any)) != nil {
			return nil, ErrCatalog
		}
	}
	if len(base) == 3 {
		// Each scope's dependency query closes over all eight registered roles.
		// The same four real column dependencies therefore appear in the two
		// peer metadata documents too. This grants no peer content privileges.
		for _, scope := range []string{"mock", "receiver"} {
			peer, ok := base[scope].(object)
			if !ok {
				return nil, ErrCatalog
			}
			rows, ok := peer["acl_dependencies"].([]any)
			if !ok {
				return nil, ErrCatalog
			}
			peer = maps.Clone(peer)
			rows = append([]any{}, rows...)
			for sub, column := range []string{"id", "verified_query_id", "conflicting_digest", "received_at"} {
				ref := object{"class": "relation", "scope": "application", "name": "payment_query_conflicts", "sub_id": sub + 1, "column": column}
				dep := object{"catalog": "pg_shdepend", "object": ref, "role": appRole, "kind": "a", "multiplicity": 1}
				id, _ := recordIdentity(dep)
				for _, value := range rows {
					other, err := recordIdentity(value)
					if err != nil || other == id {
						return nil, ErrCatalog
					}
				}
				rows = append(rows, dep)
			}
			if sortRecords(rows) != nil {
				return nil, ErrCatalog
			}
			peer["acl_dependencies"] = rows
			roles[scope] = peer
		}
	}
	return roles, nil
}

// Match a unique record identity and its complete old value before replacing.
func replaceWalletRecord(records []any, old, replacement object) error {
	id, err := recordIdentity(old)
	if err != nil {
		return ErrCatalog
	}
	found := -1
	for i, value := range records {
		other, err := recordIdentity(value)
		if err != nil {
			return ErrCatalog
		}
		if other == id {
			if found != -1 || !same(value, old) {
				return ErrCatalog
			}
			found = i
		}
	}
	if found == -1 {
		return ErrCatalog
	}
	records[found] = replacement
	return nil
}

func init() {
	// Do all fixed derivation before a caller starts the five-second budget.
	for _, mode := range []Mode{Ordinary, Sandbox} {
		_, _, _ = loadExpectedProfile(Pins{Mode: mode}, WalletReadV2)
	}
}
