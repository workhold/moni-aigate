package operations

import "testing"

func TestProvisionedBindingIsolationIsAppendOnly(t *testing.T) {
	f := operationDB(t)
	var version string
	if err := f.app.QueryRow(f.ctx, "SELECT current_version FROM channels WHERE id=$1", f.channel).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Exec(f.ctx, "INSERT INTO route_binding_isolations(channel_version_id,upstream_model,reason) VALUES($1,'upstream','model_configuration_error')", version); err != nil {
		t.Fatalf("app role cannot append isolation: %v", err)
	}
	var count int
	if err := f.app.QueryRow(f.ctx, "SELECT count(*) FROM route_binding_isolations").Scan(&count); err != nil || count != 1 {
		t.Fatalf("app role cannot read isolation: %d %v", count, err)
	}
	for _, sql := range []string{"UPDATE route_binding_isolations SET reason='model_configuration_error'", "DELETE FROM route_binding_isolations"} {
		if _, err := f.app.Exec(f.ctx, sql); err == nil {
			t.Fatalf("app role could mutate isolation history: %s", sql)
		}
		if _, err := f.owner.Exec(f.ctx, sql); err == nil {
			t.Fatalf("immutable isolation trigger allowed history mutation: %s", sql)
		}
	}
}
