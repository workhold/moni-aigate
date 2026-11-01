package fundinglotcatalog

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
)

// Formal roots are fixed at declaration, before eager verification in init.
// Captured live metadata never fills this map.
var reviewedExpectedRoots = map[Mode]struct {
	canonical []byte
	sha       string
}{
	Ordinary: {canonical: ordinaryExpectedJSON, sha: ordinaryExpectedSHA},
	Sandbox:  {canonical: sandboxExpectedJSON, sha: sandboxExpectedSHA},
}

func expectedMetadata(p Pins) (uint32, string, error) {
	d, root, e := loadExpected(p)
	if e != nil {
		return 0, "", ErrCatalog
	}
	return d.FormatVersion, root, nil
}

type expectedCacheEntry struct {
	once  sync.Once
	value *descriptorSet
	root  string
	err   error
}

var expectedCache = [3]expectedCacheEntry{}

func loadExpected(p Pins) (*descriptorSet, string, error) {
	if p.Mode != Ordinary && p.Mode != Sandbox {
		return nil, "", ErrCatalog
	}
	c := &expectedCache[p.Mode]
	c.once.Do(func() {
		r, ok := reviewedExpectedRoots[p.Mode]
		if !ok {
			c.err = ErrCatalog
			return
		}
		c.value, c.err = decodeExpected(r.canonical, r.sha, p.Mode)
		if c.err == nil {
			c.root = r.sha
		}
	})
	if c.err != nil || c.value == nil {
		return nil, "", ErrCatalog
	}
	return c.value, c.root, nil
}

// decodeExpected authenticates and validates immutable embedded bytes. Tests use
// this function directly; they cannot install arbitrary live expected roots.
func decodeExpected(raw []byte, sha string, mode Mode) (*descriptorSet, error) {
	if len(sha) != 64 || digest(raw) != sha {
		return nil, ErrCatalog
	}
	v, e := strictExpectedRawJSONNodes(raw, 4000000)
	if e != nil {
		return nil, ErrCatalog
	}
	m, ok := v.(object)
	if !ok || !exactKeys(m, "format_version", "postgres_major", "mode", "variants") {
		return nil, ErrCatalog
	}
	version, vok := number(m["format_version"])
	major, mok := number(m["postgres_major"])
	modeName, nok := m["mode"].(string)
	variants, aok := m["variants"].([]any)
	if !vok || !mok || !nok || !aok || version != 1 || major != 16 {
		return nil, ErrCatalog
	}
	out := descriptorSet{FormatVersion: version, PostgresMajor: major, Mode: modeName}
	for _, value := range variants {
		record, ok := value.(object)
		if !ok || !exactKeys(record, "name", "schema", "roles") {
			return nil, ErrCatalog
		}
		name, nok := record["name"].(string)
		schema, sok := record["schema"].(object)
		roles, rok := record["roles"].(object)
		if !nok || !sok || !rok {
			return nil, ErrCatalog
		}
		out.Variants = append(out.Variants, descriptorVariant{Name: name, Schema: schema, Roles: roles})
	}
	if mode == Ordinary {
		if out.Mode != "ordinary" || len(out.Variants) != 5 {
			return nil, ErrCatalog
		}
	} else if mode == Sandbox {
		if out.Mode != "sandbox" || len(out.Variants) != 1 {
			return nil, ErrCatalog
		}
	} else {
		return nil, ErrCatalog
	}
	seen := map[string]bool{}
	for _, variant := range out.Variants {
		if seen[variant.Name] || variant.Name == "" {
			return nil, ErrCatalog
		}
		seen[variant.Name] = true
		if mode == Sandbox && variant.Name != "full-sandbox" {
			return nil, ErrCatalog
		}
		if validateExpectedDocuments(variant.Schema, variant.Roles) != nil {
			return nil, ErrCatalog
		}
	}
	if mode == Ordinary && (!seen["named-isolated"] || !seen["named-minimal-login"] || !seen["named-image-login"] || !seen["public-minimal-login"] || !seen["public-image-login"]) {
		return nil, ErrCatalog
	}
	canonicalBytes, e := canonical(v)
	if e != nil || string(canonicalBytes) != string(raw) {
		return nil, ErrCatalog
	}
	if cacheAuthenticatedVariantDocuments(&out, canonicalBytes) != nil {
		return nil, ErrCatalog
	}
	out.bounds, e = deriveCategoryBounds(&out)
	if e != nil {
		return nil, ErrCatalog
	}
	return &out, nil
}

func readCatalogTx(ctx context.Context, tx pgx.Tx, p Pins) (*catalogSnapshot, error) {
	d, _, e := loadExpected(p)
	if e != nil {
		return nil, ErrCatalog
	}
	return collectTx(ctx, tx, p, d)
}

// Embedded roots are authenticated once at startup, before any caller owns a
// database transaction or starts its common five-second issuing budget.
func init() {
	for _, m := range []Mode{Ordinary, Sandbox} {
		_, _, _ = loadExpected(Pins{Mode: m})
	}
}
