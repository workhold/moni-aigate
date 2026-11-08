package billing

import "testing"

func retailQuantitiesForRequest(t *testing.T, f *fixture, request string, input, output int64) []byte {
	t.Helper()
	var raw []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT meter_side_quantity_canonical(schema_id,'retail',$2,$3)
FROM request_meter_snapshots WHERE tenant_id=$1 AND request_id=$4`, f.tenant, input, output, request).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}
