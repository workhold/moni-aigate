package operations

import "testing"

// Metadata-only fixtures use a same-transaction zero reservation after 020.
// They do not post money or stand in for the separately tested Reserve API.
func operationMetadataSource(t *testing.T, f *operationFixture, request, query string, args ...any) {
	t.Helper()
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal("begin metadata fixture")
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, query, args...); err != nil {
		t.Fatal("insert metadata fixture source")
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO reservations(id,tenant_id,request_id,amount,deadline) VALUES(gen_random_uuid()::text,$1,$2,0,clock_timestamp()+interval '24 hours')`, f.tenant, request); err != nil {
		t.Fatal("insert metadata fixture zero reservation")
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal("commit metadata fixture")
	}
}
