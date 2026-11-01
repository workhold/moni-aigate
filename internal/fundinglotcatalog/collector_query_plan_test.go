package fundinglotcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// This fixture-only diagnostic verifies the semijoin rewrite preserves every
// original row, including genuine pg_depend duplicate multiplicities.
func TestCatalogDependencySemijoinsPreserveRowsAndPlan(t *testing.T) {
	f := newOrdinaryVariantFixture(t, ordinaryNamed)
	audit := catalogTestAudit(t, f)
	tx := beginCatalogTestTx(t, audit, pgx.ReadOnly)
	defer tx.Rollback(context.Background())
	profiles, _ := catalogProfiles(f.pins)
	p := profiles[0]
	r := &resolver{refs: map[refKey]object{}, types: map[uint32]object{}, attributes: map[uint32]map[uint32]string{}}
	budget := &collectorBudget{}
	if preflightProfileTx(context.Background(), tx, budget, p) != nil {
		t.Fatal("plan fixture raw preflight")
	}
	rels, e := registerProfileTx(context.Background(), tx, r, p, budget)
	if e != nil {
		t.Fatal("plan fixture relation registry")
	}
	var proposed string
	for _, q := range schemaQueries() {
		if q.name == "dependencies" {
			proposed = q.sql
		}
	}
	split := `(EXISTS(SELECT 1 FROM owned x WHERE x.classid OPERATOR(pg_catalog.=) d.classid AND x.objid OPERATOR(pg_catalog.=) d.objid) OR EXISTS(SELECT 1 FROM owned x WHERE x.classid OPERATOR(pg_catalog.=) d.refclassid AND x.objid OPERATOR(pg_catalog.=) d.refobjid))`
	old := `EXISTS(SELECT 1 FROM owned x WHERE (x.classid OPERATOR(pg_catalog.=) d.classid AND x.objid OPERATOR(pg_catalog.=) d.objid) OR (x.classid OPERATOR(pg_catalog.=) d.refclassid AND x.objid OPERATOR(pg_catalog.=) d.refobjid))`
	if strings.Count(proposed, split) != 1 {
		t.Fatal("fixed dependency semijoin registry changed")
	}
	original := strings.Replace(proposed, split, old, 1)
	var results [][]byte
	for _, item := range []struct{ name, sql string }{{"original", original}, {"split-semijoins", proposed}} {
		var plan []byte
		if tx.QueryRow(context.Background(), `EXPLAIN (ANALYZE,FORMAT JSON,TIMING OFF) `+item.sql, p.schemaOID, rels).Scan(&plan) != nil {
			t.Fatal("fixed catalog plan diagnostic failed")
		}
		var parsed []map[string]any
		if json.Unmarshal(plan, &parsed) != nil || len(parsed) != 1 {
			t.Fatal("fixed catalog plan JSON invalid")
		}
		t.Logf("%s execution_ms=%v hashed_subplan=%t", item.name, parsed[0]["Execution Time"], bytes.Contains(plan, []byte("hashed SubPlan")))
		rows, e := collectRows(context.Background(), tx, &collectorBudget{}, item.sql, 6860, p.schemaOID, rels)
		if e != nil {
			t.Fatal("fixed dependency rows unavailable")
		}
		records, e := dependencyMultiset(rows)
		if e != nil {
			t.Fatal("fixed dependency multiset invalid")
		}
		canonicalRows, e := canonical(records)
		if e != nil {
			t.Fatal("fixed dependency canonical invalid")
		}
		t.Logf("%s query_sha256=%s raw_rows=%d distinct_records=%d canonical_rows_sha256=%s", item.name, digest([]byte(item.sql)), len(rows), len(records), digest(canonicalRows))
		results = append(results, canonicalRows)
	}
	if !bytes.Equal(results[0], results[1]) {
		t.Fatal("semijoin rewrite changed original rows or genuine multiplicity")
	}
}
