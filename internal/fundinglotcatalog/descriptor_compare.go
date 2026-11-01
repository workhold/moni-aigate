package fundinglotcatalog

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

func templatePath(path string) bool {
	for _, suffix := range []string{".partition_bound", ".partition_key", ".default_expression", ".argument_defaults", ".expression", ".expressions", ".predicate", ".when"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return strings.HasSuffix(path, ".default") && strings.Contains(path, ".attributes.")
}

func renderSQLTemplate(v object, names map[string]string, path string) (string, error) {
	return renderSQLTemplateAllowed(v, names, templatePath(path))
}

func renderSQLTemplateAllowed(v object, names map[string]string, allowed bool) (string, error) {
	if !allowed || !exactKeys(v, "parts") {
		return "", ErrCatalog
	}
	parts, ok := v["parts"].([]any)
	if !ok || len(parts) == 0 || len(parts) > 1024 {
		return "", ErrCatalog
	}
	var b strings.Builder
	schemas := 0
	for _, part := range parts {
		m, ok := part.(object)
		if !ok {
			return "", ErrCatalog
		}
		if exactKeys(m, "literal") {
			s, ok := m["literal"].(string)
			if !ok {
				return "", ErrCatalog
			}
			b.WriteString(s)
		} else if exactKeys(m, "schema") {
			s, ok := m["schema"].(string)
			if !ok || names[s] == "" {
				return "", ErrCatalog
			}
			b.WriteString(names[s])
			schemas++
		} else {
			return "", ErrCatalog
		}
		if b.Len() > collectorRecordLimit {
			return "", ErrCatalog
		}
	}
	if schemas == 0 {
		return "", ErrCatalog
	}
	return b.String(), nil
}

// Closed descriptor fields need only the current field and the attributes
// ancestor bit. Do not allocate a full path for every scalar in the catalog.
// The string-path entry remains available for the template contract tests.
type descriptorComparePath struct {
	field        string
	inAttributes bool
}

func (p descriptorComparePath) child(field string) descriptorComparePath {
	return descriptorComparePath{field: field, inAttributes: p.inAttributes || p.field == "attributes"}
}

func (p descriptorComparePath) permitsTemplate() bool {
	switch p.field {
	case "partition_bound", "partition_key", "default_expression", "argument_defaults", "expression", "expressions", "predicate", "when":
		return true
	case "default":
		return p.inAttributes
	}
	return false
}

func compareDescriptorValue(actual, expected any, names map[string]string, path string) error {
	p := descriptorComparePath{inAttributes: strings.Contains(path, ".attributes.")}
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		p.field = path[i+1:]
	}
	return compareClosedDescriptorValue(actual, expected, names, p)
}

func compareClosedDescriptorValue(actual, expected any, names map[string]string, path descriptorComparePath) error {
	if e, ok := expected.(object); ok {
		if _, yes := e["parts"]; yes {
			rendered, err := renderSQLTemplateAllowed(e, names, path.permitsTemplate())
			a, ok := actual.(string)
			if err != nil || !ok || a != rendered {
				return ErrCatalog
			}
			return nil
		}
		a, ok := actual.(object)
		if !ok || len(a) != len(e) {
			return ErrCatalog
		}
		for k, v := range e {
			av, ok := a[k]
			if !ok || compareClosedDescriptorValue(av, v, names, path.child(k)) != nil {
				return ErrCatalog
			}
		}
		return nil
	}
	if e, ok := expected.([]any); ok {
		a, ok := actual.([]any)
		if !ok || len(a) != len(e) {
			return ErrCatalog
		}
		for i, v := range e {
			if compareClosedDescriptorValue(a[i], v, names, path.child("")) != nil {
				return ErrCatalog
			}
		}
		return nil
	}
	if !sameCatalogScalar(actual, expected) {
		return ErrCatalog
	}
	return nil
}

func schemaIdentifiersTx(ctx context.Context, tx pgx.Tx, p Pins) (map[string]string, error) {
	profiles, _ := catalogProfiles(p)
	names := map[string]string{}
	for _, s := range profiles {
		var q string
		if tx.QueryRow(ctx, `SELECT pg_catalog.quote_ident($1::pg_catalog.text)`, s.schema).Scan(&q) != nil || q == "" {
			return nil, ErrCatalog
		}
		names[s.scope] = q
	}
	return names, nil
}

// Normalized Go counters/OIDs and parsed expected JSON share canonical number
// text, without remarshal allocations at each of the catalog's scalar leaves.
func sameCatalogScalar(a, b any) bool {
	switch x := b.(type) {
	case nil:
		return a == nil
	case string:
		y, ok := a.(string)
		return ok && y == x
	case bool:
		y, ok := a.(bool)
		return ok && y == x
	}
	x, xok := canonicalNumberText(a)
	y, yok := canonicalNumberText(b)
	return xok && yok && validCanonicalNumber(x) && validCanonicalNumber(y) && x == y
}
