package operations

import (
	"testing"

	"github.com/moni-ai/moni-aigate/internal/pricing"
)

func TestPricingProvisionRemainsColumnScoped(t *testing.T) {
	f := operationDB(t)
	for pass := 0; pass < 2; pass++ {
		if err := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); err != nil {
			t.Fatal(err)
		}
		for _, sql := range []string{
			`UPDATE pricing_operations SET kind=kind WHERE false`,
			`DELETE FROM pricing_operations WHERE false`,
			`UPDATE tenant_pricing_assignments SET actor=actor WHERE false`,
			`DELETE FROM tenant_pricing_assignments WHERE false`,
			`UPDATE price_groups SET id=id WHERE false`,
			`UPDATE price_groups SET created_at=created_at WHERE false`,
			`DELETE FROM price_groups WHERE false`,
			`UPDATE tenants SET id=id WHERE false`,
			`UPDATE tenants SET created_at=created_at WHERE false`,
			`DELETE FROM tenants WHERE false`,
			`UPDATE price_versions SET price_group_id=price_group_id WHERE false`,
			`UPDATE request_records SET tenant_id=tenant_id WHERE false`,
			`UPDATE request_records SET key_id=key_id WHERE false`,
			`UPDATE request_records SET owner_id=owner_id WHERE false`,
			`UPDATE request_records SET model_id=model_id WHERE false`,
			`UPDATE request_records SET profile_version=profile_version WHERE false`,
			`UPDATE request_records SET protocol=protocol,execution_kind=execution_kind,result_kind=result_kind WHERE false`,
			`UPDATE request_meter_snapshots SET price_version_id=price_version_id WHERE false`,
			`UPDATE request_meter_snapshots SET pricing_group_id=pricing_group_id,tenant_pricing_revision=tenant_pricing_revision,retail_priced_at=retail_priced_at WHERE false`,
			`UPDATE request_records SET client_ip=client_ip WHERE false`,
		} {
			_, err := f.app.Exec(f.ctx, sql)
			requirePoolSQLState(t, err, "42501")
		}
		for _, sql := range []string{
			`UPDATE price_groups SET label=label,state=state,revision=revision,last_operation_id=last_operation_id WHERE false`,
			`UPDATE tenants SET name=name,status=status,allowed_regions=allowed_regions WHERE false`,
			`UPDATE tenants SET price_group_id=price_group_id,pricing_revision=pricing_revision,pricing_operation_id=pricing_operation_id WHERE false`,
			`UPDATE request_records SET status=status,billing_status=billing_status,latest_finalization_id=latest_finalization_id WHERE false`,
			`SELECT id FROM price_groups WHERE false FOR SHARE`,
			`SELECT id FROM price_groups WHERE false FOR NO KEY UPDATE`,
			`SELECT id FROM tenants WHERE false FOR NO KEY UPDATE`,
		} {
			if _, err := f.app.Exec(f.ctx, sql); err != nil {
				t.Fatalf("required column/lock privilege missing (pass %d): %s: %v", pass, sql, err)
			}
		}
		for _, table := range []string{"price_groups", "pricing_operations", "tenant_pricing_assignments"} {
			for _, sql := range []string{`SELECT * FROM ` + table, `DELETE FROM ` + table + ` WHERE false`} {
				_, err := f.projection.Exec(f.ctx, sql)
				requirePoolSQLState(t, err, "42501")
			}
		}
	}
	// A default-domain helper does not require any group UPDATE permission and
	// observes the same public default version with the actual serving role.
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	domain := pricing.Domain{TenantRevision: 1}
	if err = pricing.LockAssignedGroupTx(f.ctx, tx, domain); err != nil {
		t.Fatal(err)
	}
	selected, observed, err := pricing.SelectEffectiveTx(f.ctx, tx, f.model, domain)
	if err != nil || selected == nil || observed.IsZero() {
		t.Fatalf("serving role cannot select effective default price: %+v %v", selected, err)
	}
}
