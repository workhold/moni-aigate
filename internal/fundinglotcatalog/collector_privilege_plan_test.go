package fundinglotcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func legacyCatalogPrivilegeSQL(current string) (string, error) {
	for _, kind := range []struct {
		fn, args   string
		privileges []string
	}{
		{"has_table_privilege", "r.oid,c.oid", []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"}},
		{"has_column_privilege", "r.oid,c.oid,a.attnum", []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"}},
	} {
		for _, grant := range []bool{false, true} {
			var calls, literals []string
			arg := "p"
			if grant {
				arg = "p OPERATOR(pg_catalog.||) ' WITH GRANT OPTION'"
			}
			for _, privilege := range kind.privileges {
				literals = append(literals, "'"+privilege+"'")
				if grant {
					privilege += " WITH GRANT OPTION"
				}
				calls = append(calls, "pg_catalog."+kind.fn+"("+kind.args+",'"+privilege+"')")
			}
			expanded := "pg_catalog.to_jsonb(ARRAY[" + strings.Join(calls, ",") + "])"
			legacy := "pg_catalog.to_jsonb(ARRAY(SELECT pg_catalog." + kind.fn + "(" + kind.args + "," + arg + ") FROM pg_catalog.unnest(ARRAY[" + strings.Join(literals, ",") + "]) p))"
			if strings.Count(current, expanded) != 1 {
				return "", ErrCatalog
			}
			current = strings.Replace(current, expanded, legacy, 1)
		}
	}
	return current, nil
}

func TestCatalogPrivilegeArraysPreserveFullPeerVectors(t *testing.T) {
	f := newCatalogPeerFixture(t)
	tx, e := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		t.Fatal("privilege plan caller RR")
	}
	defer tx.Rollback(context.Background())
	profiles, _ := catalogProfiles(f.pins)
	r := &resolver{refs: map[refKey]object{}, types: map[uint32]object{}, attributes: map[uint32]map[uint32]string{}}
	var proposed string
	for _, q := range roleQueries() {
		if q.name == "effective_privileges" {
			proposed = q.sql
		}
	}
	legacy, e := legacyCatalogPrivilegeSQL(proposed)
	if e != nil {
		t.Fatal("fixed explicit privilege array registry changed")
	}
	reviewed, _, e := loadExpected(f.pins)
	if e != nil {
		t.Fatal("fixed privilege bounds unavailable")
	}
	for _, p := range profiles {
		budget := &collectorBudget{}
		if preflightProfileTx(f.ctx, tx, budget, p) != nil {
			t.Fatal("privilege plan raw preflight")
		}
		rels, e := registerProfileTx(f.ctx, tx, r, p, budget)
		if e != nil {
			t.Fatal("privilege plan relation registry")
		}
		bound, e := reviewed.categoryBound(p.scope, "roles", "effective_privileges")
		if e != nil {
			t.Fatal("privilege static row bound")
		}
		var results [][]byte
		for _, item := range []struct{ name, sql string }{{"legacy", legacy}, {"explicit-array", proposed}} {
			var plan []byte
			if tx.QueryRow(f.ctx, `EXPLAIN (ANALYZE,FORMAT JSON,TIMING OFF) `+item.sql, p.schemaOID, rels, p.roleOIDs).Scan(&plan) != nil {
				t.Fatal("privilege plan query")
			}
			var parsed []map[string]any
			if json.Unmarshal(plan, &parsed) != nil || len(parsed) != 1 {
				t.Fatal("privilege plan JSON")
			}
			values, e := collectRows(f.ctx, tx, &collectorBudget{}, item.sql, bound.raw, p.schemaOID, rels, p.roleOIDs)
			if e != nil {
				t.Fatal("privilege vector rows")
			}
			if sortRecords(values) != nil {
				t.Fatal("privilege row identity")
			}
			canonicalRows, e := canonical(values)
			if e != nil {
				t.Fatal("privilege row canonical")
			}
			results = append(results, canonicalRows)
			t.Logf("%s/%s execution_ms=%v query_sha256=%s rows=%d canonical_rows_sha256=%s", p.scope, item.name, parsed[0]["Execution Time"], digest([]byte(item.sql)), len(values), digest(canonicalRows))
		}
		if !bytes.Equal(results[0], results[1]) {
			t.Fatal("explicit vectors changed any real peer role or table/column/function/type/schema bit")
		}
	}
}
