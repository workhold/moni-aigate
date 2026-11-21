package billing

import "testing"

func TestPostgresCacheValidatorSearchPathsArePinned(t *testing.T) {
	f := integration(t)
	names := []string{"text_response_cache_complete", "text_response_cache_mutation", "cache_request_digest_immutable", "cache_hit_no_attempt", "cache_finalization_complete", "meter_finalization_complete", "meter_settlement_amount"}
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND p.proname=ANY($1) AND array_to_string(p.proconfig,'|')='search_path='||current_schema()||', pg_catalog, pg_temp'`, names).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(names) {
		rows, err := f.pool.Query(f.ctx, `SELECT p.proname,coalesce(array_to_string(p.proconfig,'|'),'<null>') FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND p.proname=ANY($1) ORDER BY p.proname`, names)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var name, config string
			if err = rows.Scan(&name, &config); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %s", name, config)
		}
		t.Fatalf("pinned validator count=%d want=%d", count, len(names))
	}
}
