package operations

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func TestProvisionedKeyPolicyConstraintsAndRevision(t *testing.T) {
	f := operationDB(t)
	var revision string
	// Neither financial mutations nor caller supplied revisions advance policy CAS.
	for _, sql := range []string{
		`UPDATE api_keys SET spent=2,held=3 WHERE id=$1 RETURNING access_policy_revision::text`,
		`UPDATE api_keys SET access_policy_revision=9223372036854775807 WHERE id=$1 RETURNING access_policy_revision::text`,
		`UPDATE api_keys SET access_policy_revision=0,name=name||'-renamed' WHERE id=$1 RETURNING access_policy_revision::text`,
	} {
		if err := f.app.QueryRow(f.ctx, sql, f.key).Scan(&revision); err != nil || revision != "1" {
			t.Fatalf("non-policy update changed revision=%s err=%v", revision, err)
		}
	}
	if err := f.app.QueryRow(f.ctx, `UPDATE api_keys SET allowed_client_cidrs=ARRAY['192.0.2.0/24'],access_policy_revision=700 WHERE id=$1 RETURNING access_policy_revision::text`, f.key).Scan(&revision); err != nil || revision != "2" {
		t.Fatalf("policy update did not force next revision: %s %v", revision, err)
	}
	if err := f.owner.QueryRow(f.ctx, `UPDATE api_keys SET allowed_client_cidrs=allowed_client_cidrs,access_policy_revision=0 WHERE id=$1 RETURNING access_policy_revision::text`, f.key).Scan(&revision); err != nil || revision != "2" {
		t.Fatalf("owner bypassed policy revision: %s %v", revision, err)
	}
	inserted := uuid.NewString()
	if err := f.app.QueryRow(f.ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,access_policy_revision) VALUES($1,$2,$3,'revision-fixture','fixture',$1,$4,999) RETURNING access_policy_revision::text`, inserted, f.tenant, f.user, []string{f.model}).Scan(&revision); err != nil || revision != "1" {
		t.Fatalf("insert forged initial policy revision: %s %v", revision, err)
	}
	for _, bad := range []string{
		`NULL`, `ARRAY[NULL]::text[]`, `ARRAY['']`, `ARRAY['192.0.2.1']`, `ARRAY['192.0.2.19/24']`, `ARRAY['192.0.2.0/24','192.0.2.0/24']`,
		`ARRAY['198.51.100.0/24','192.0.2.0/24']`, `ARRAY['0.0.0.0/0']`, `ARRAY['::/0']`, `ARRAY['::ffff:c000:200/120']`,
		`ARRAY['::ffff:192.0.2.0/120']`, `ARRAY['2001:DB8::/32']`, `ARRAY['2001:db8:0:0::/32']`, `ARRAY['::192.0.2.0/128']`,
		`ARRAY['example.com/24']`, `ARRAY['fe80::1%en0/128']`, `ARRAY['192.0.2.0/24 ']`, `ARRAY[['192.0.2.0/24']]`,
		`'[0:0]={192.0.2.0/24}'::text[]`, `array_fill('192.0.2.0/24'::text,ARRAY[33])`,
	} {
		t.Run("invalid_"+bad, func(t *testing.T) {
			_, err := f.app.Exec(f.ctx, `UPDATE api_keys SET allowed_client_cidrs=`+bad+` WHERE id=$1`, f.key)
			if err == nil {
				t.Fatalf("database accepted invalid allowlist: %s", bad)
			}
			var after string
			if err = f.app.QueryRow(f.ctx, `SELECT access_policy_revision::text FROM api_keys WHERE id=$1`, f.key).Scan(&after); err != nil || after != "2" {
				t.Fatalf("constraint rejection changed revision: %s %v", after, err)
			}
		})
	}
	// The database and Go agree even where PostgreSQL normally formats IPv6
	// as dotted IPv4-compatible text rather than netip's RFC 5952 representation.
	for _, source := range []string{
		"192.0.2.19/24", "128.0.0.0/1", "0.0.0.0", "255.255.255.255",
		"::", "::1", "::2", "::192.0.2.0", "::0.0.0.2", "::0.1.0.0", "::1:0:0", "8000::/1",
		"2001:DB8:1:2::5/48", "1:0:0:2:0:0:3:4", "1:2:3:4:5:6:7:8", "1:0:0:0:2:0:0:0", "1:0:2:3:4:5:6:7", "1:2:3:4:5:0:0:0",
	} {
		canonical, err := security.NormalizeClientCIDRs([]string{source})
		if err != nil {
			t.Fatal(err)
		}
		var valid bool
		if err = f.app.QueryRow(f.ctx, `SELECT valid_key_client_cidrs($1::text[])`, canonical).Scan(&valid); err != nil || !valid {
			t.Fatalf("database disagrees with netip for %q -> %v: %v", source, canonical, err)
		}
	}
	store := identity.New(f.app, f.secrets)
	view, err := store.UpdateKeyAccessPolicy(f.ctx, &identity.Session{User: identity.User{ID: f.user}, Tenant: &identity.Tenant{ID: f.tenant, Role: "owner"}}, f.key, identity.KeyAccessPolicyInput{ExpectedRevision: "2", AllowedClientCIDRs: []string{"2001:DB8::1", "192.0.2.1"}})
	if err != nil || view.AccessPolicyRevision != "3" || !reflect.DeepEqual(view.AllowedClientCIDRs, []string{"192.0.2.1/32", "2001:db8::1/128"}) {
		t.Fatalf("least-privilege application cannot edit policy: %+v %v", view, err)
	}
	for _, sql := range []string{`SELECT allowed_client_cidrs FROM api_keys LIMIT 1`, `UPDATE api_keys SET allowed_client_cidrs='{}'`, `SELECT client_ip FROM request_records LIMIT 1`} {
		_, err := f.projection.Exec(f.ctx, sql)
		requirePoolSQLState(t, err, "42501")
	}
}

func TestProvisionedRequestClientIPIsImmutableMetadata(t *testing.T) {
	f := operationDB(t)
	for i, source := range []string{"NULL", "'192.0.2.1'::inet", "'2001:db8::1'::inet"} {
		request := uuid.NewString()
		operationMetadataSource(t, f, request, fmt.Sprintf(`WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at)
 INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at)
 SELECT $1,$2,$3,$4,m.id,p.id,p.input_rate,p.output_rate,1,1,m.profile_version,'fixture','fixture',instant.at+interval '1 hour','fixture',false,%s,t.price_group_id,t.pricing_revision,instant.at
 FROM instant CROSS JOIN tenants t CROSS JOIN models m JOIN LATERAL
  (SELECT id,input_rate,output_rate FROM price_versions WHERE model_id=m.id
   AND price_group_id IS NOT DISTINCT FROM t.price_group_id AND effective_at<=instant.at
   ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1) p ON true
 WHERE t.id=$2 AND m.id=$5`, source), request, f.tenant, f.key, f.user, f.model)
		var err error
		for _, pool := range []*pgxpool.Pool{f.app, f.owner} {
			expected := "P0001"
			if pool == f.app {
				expected = "42501"
			}
			_, err = pool.Exec(f.ctx, `UPDATE request_records SET client_ip='198.51.100.1'::inet WHERE id=$1`, request)
			requirePoolSQLState(t, err, expected)
			_, err = pool.Exec(f.ctx, `UPDATE request_records SET client_ip=NULL WHERE id=$1 AND client_ip IS NOT NULL`, request)
			if i > 0 || pool == f.app {
				requirePoolSQLState(t, err, expected)
			} else if err != nil {
				t.Fatal(err)
			}
		}
		if _, err = f.app.Exec(f.ctx, `UPDATE request_records SET status='failed',billing_status='released' WHERE id=$1`, request); err != nil {
			t.Fatalf("source immutability blocked ordinary lifecycle: %v", err)
		}
		var status string
		var ip *string
		if err = f.app.QueryRow(f.ctx, `SELECT status,host(client_ip) FROM request_records WHERE id=$1`, request).Scan(&status, &ip); err != nil || status != "failed" {
			t.Fatalf("request lifecycle status=%s err=%v", status, err)
		}
		if i == 0 && ip != nil || i > 0 && (ip == nil || !strings.Contains(source, *ip)) {
			t.Fatalf("immutable source changed: source=%s actual=%v", source, ip)
		}
	}
}
