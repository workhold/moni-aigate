package operations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/migrations"
)

type periodicRoleFixture struct {
	ctx                             context.Context
	owner, app, projection          *pgxpool.Pool
	schema, appRole, projectionRole string
}

func periodicRoleMust(t *testing.T, step string, e error) {
	t.Helper()
	if e != nil {
		t.Fatalf("%s: %v", step, e)
	}
}
func newPeriodicRoleFixture(t *testing.T, historical bool) *periodicRoleFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for actual LOGIN periodic-budget role tests")
	}
	cfg, e := pgxpool.ParseConfig(raw)
	periodicRoleMust(t, "parse dedicated config", e)
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("periodic role tests require dedicated local moni_test port 55439")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	admin, e := pgxpool.NewWithConfig(ctx, cfg.Copy())
	periodicRoleMust(t, "open admin", e)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	f := &periodicRoleFixture{ctx: ctx, schema: "pb_roles_" + suffix, appRole: "pb_app_" + suffix, projectionRole: "pb_projection_" + suffix}
	t.Cleanup(func() {
		for _, p := range []*pgxpool.Pool{f.app, f.projection, f.owner} {
			if p != nil {
				p.Close()
			}
		}
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, e := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{f.schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error("periodic role schema cleanup failed")
		}
		for _, name := range []string{f.appRole, f.projectionRole} {
			q := pgx.Identifier{name}.Sanitize()
			if _, e := admin.Exec(cleanup, "DROP OWNED BY "+q); e != nil {
				t.Error("periodic role grant cleanup failed")
			}
			if _, e := admin.Exec(cleanup, "DROP ROLE IF EXISTS "+q); e != nil {
				t.Error("periodic LOGIN cleanup failed")
			}
		}
		admin.Close()
		cancel()
	})
	_, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{f.schema}.Sanitize())
	periodicRoleMust(t, "create isolated schema", e)
	secrets := []string{strings.ReplaceAll(uuid.NewString(), "-", ""), strings.ReplaceAll(uuid.NewString(), "-", "")}
	for i, name := range []string{f.appRole, f.projectionRole} {
		_, e = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{name}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+secrets[i]+"'")
		periodicRoleMust(t, "create isolated LOGIN", e)
	}
	cfg.MaxConns = 4
	cfg.ConnConfig.RuntimeParams["search_path"] = f.schema
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	f.owner, e = pgxpool.NewWithConfig(ctx, cfg.Copy())
	periodicRoleMust(t, "open owner", e)
	if historical {
		e = migrations.ApplyThrough(ctx, f.owner, "019_operations_alerts.sql")
	} else {
		e = migrations.Apply(ctx, f.owner)
	}
	periodicRoleMust(t, "install schema baseline", e)
	periodicRoleMust(t, "initial ProvisionFor", ProvisionFor(ctx, f.owner, f.schema, f.appRole, f.projectionRole))
	for i, name := range []string{f.appRole, f.projectionRole} {
		login := cfg.Copy()
		login.ConnConfig.User = name
		login.ConnConfig.Password = secrets[i]
		p, e := pgxpool.NewWithConfig(ctx, login)
		periodicRoleMust(t, "open runtime LOGIN", e)
		if i == 0 {
			f.app = p
		} else {
			f.projection = p
		}
		var current, session string
		periodicRoleMust(t, "attest LOGIN", p.QueryRow(ctx, "SELECT current_user,session_user").Scan(&current, &session))
		if current != name || session != name {
			t.Fatal("fixture did not authenticate as actual restricted LOGIN")
		}
	}
	return f
}
func (f *periodicRoleFixture) table(name string) string {
	return pgx.Identifier{f.schema, name}.Sanitize()
}
func (f *periodicRoleFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	_, e := f.owner.Exec(f.ctx, q, args...)
	periodicRoleMust(t, "owner fixture DDL/ACL", e)
}
func (f *periodicRoleFixture) group(t *testing.T) string {
	t.Helper()
	name := "pb_group_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	q := pgx.Identifier{name}.Sanitize()
	f.exec(t, "CREATE ROLE "+q+" NOLOGIN NOSUPERUSER NOCREATEROLE NOCREATEDB NOREPLICATION NOBYPASSRLS")
	t.Cleanup(func() { f.exec(t, "DROP OWNED BY "+q); f.exec(t, "DROP ROLE "+q) })
	return name
}
func (f *periodicRoleFixture) expectRejected(t *testing.T, role PeriodicBudgetDatabaseRole, persistent bool) {
	t.Helper()
	p := f.app
	if role == PeriodicBudgetProjectionRole {
		p = f.projection
	}
	if e := VerifyPeriodicBudgetRole(f.ctx, p, role); !errors.Is(e, ErrPeriodicBudgetRolePrivileges) {
		t.Fatalf("runtime accepted invalid periodic boundary: %v", e)
	}
	if e := VerifyPeriodicBudgetRolesFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); !errors.Is(e, ErrPeriodicBudgetRolePrivileges) {
		t.Fatalf("offline accepted invalid periodic boundary: %v", e)
	}
	// Shared owner/schema/attribute failures preserve the earlier alert module
	// sentinel; the runtime/offline periodic checks above must still reject.
	if persistent {
		if e := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); !errors.Is(e, ErrPeriodicBudgetRolePrivileges) && !errors.Is(e, ErrAlertRolePrivileges) {
			t.Fatalf("provision accepted persistent excess: %v", e)
		}
	}
}
func (f *periodicRoleFixture) expectValid(t *testing.T) {
	t.Helper()
	periodicRoleMust(t, "verify actual app", VerifyPeriodicBudgetRole(f.ctx, f.app, PeriodicBudgetApplicationRole))
	periodicRoleMust(t, "verify actual projector", VerifyPeriodicBudgetRole(f.ctx, f.projection, PeriodicBudgetProjectionRole))
	periodicRoleMust(t, "verify explicit offline roles", VerifyPeriodicBudgetRolesFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole))
}

func TestPeriodicBudgetActualLoginsExactMatrixAndRepeatedProvision(t *testing.T) {
	f := newPeriodicRoleFixture(t, false)
	for n := 0; n < 3; n++ {
		periodicRoleMust(t, "repeat ProvisionFor", ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole))
		f.expectValid(t)
	}
	group := f.group(t)
	qg, qa := pgx.Identifier{group}.Sanitize(), pgx.Identifier{f.appRole}.Sanitize()
	f.exec(t, "GRANT SELECT,UPDATE(enabled) ON "+f.table("periodic_budget_controls")+" TO "+qg)
	f.exec(t, "GRANT "+qg+" TO "+qa+" WITH INHERIT TRUE, SET TRUE")
	f.expectValid(t)
	periodicRoleMust(t, "allowed reachable subset survives provision", ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole))
	if e := VerifyPeriodicBudgetRole(f.ctx, f.owner, PeriodicBudgetApplicationRole); !errors.Is(e, ErrPeriodicBudgetRolePrivileges) {
		t.Fatal("owner qualified as runtime")
	}
	cfg := f.owner.Config()
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { _, e := c.Exec(ctx, "SET ROLE "+qa); return e }
	impersonating, e := pgxpool.NewWithConfig(f.ctx, cfg)
	periodicRoleMust(t, "open privileged session wrapper", e)
	defer impersonating.Close()
	if e = VerifyPeriodicBudgetRole(f.ctx, impersonating, PeriodicBudgetApplicationRole); !errors.Is(e, ErrPeriodicBudgetRolePrivileges) {
		t.Fatal("privileged SET ROLE session qualified")
	}
	if e := VerifyPeriodicBudgetRolesFor(f.ctx, f.owner, f.schema, f.appRole, f.appRole); !errors.Is(e, ErrPeriodicBudgetRolePrivileges) {
		t.Fatal("shared app/projector role qualified")
	}
	if e := VerifyPeriodicBudgetRole(context.Background(), nil, PeriodicBudgetApplicationRole); !errors.Is(e, ErrPeriodicBudgetRolePrivileges) {
		t.Fatal("nil runtime pool qualified")
	}
	if e := VerifyPeriodicBudgetRole(f.ctx, f.app, "invalid"); !errors.Is(e, ErrPeriodicBudgetRolePrivileges) {
		t.Fatal("unknown runtime kind qualified")
	}
}

func TestPeriodicBudgetEffectiveACLAndReachableSubset(t *testing.T) {
	f := newPeriodicRoleFixture(t, false)
	for _, tc := range []struct {
		name, table, grant string
		role               PeriodicBudgetDatabaseRole
		inherit, set       bool
		grantOption        bool
	}{
		{"immutable inherited table UPDATE", "periodic_budget_events", "UPDATE", PeriodicBudgetApplicationRole, true, false, false},
		{"immutable inherited column UPDATE", "periodic_budget_operations", "UPDATE(operation_id)", PeriodicBudgetApplicationRole, true, false, false},
		{"mutable excessive immutable column", "periodic_budget_controls", "UPDATE(id)", PeriodicBudgetApplicationRole, true, false, false},
		{"mutable whole table UPDATE", "periodic_budget_controls", "UPDATE", PeriodicBudgetApplicationRole, true, false, false},
		{"DELETE", "periodic_budget_windows", "DELETE", PeriodicBudgetApplicationRole, true, false, false},
		{"TRUNCATE", "periodic_budget_allocations", "TRUNCATE", PeriodicBudgetApplicationRole, true, false, false},
		{"TRIGGER", "periodic_budget_events", "TRIGGER", PeriodicBudgetApplicationRole, true, false, false},
		{"column REFERENCES", "periodic_budget_controls", "REFERENCES(id)", PeriodicBudgetApplicationRole, true, false, false},
		{"table grant option", "periodic_budget_events", "SELECT", PeriodicBudgetApplicationRole, true, false, true},
		{"column grant option", "periodic_budget_controls", "UPDATE(enabled)", PeriodicBudgetApplicationRole, true, false, true},
		{"SET-only hidden excess", "periodic_budget_configuration_versions", "UPDATE", PeriodicBudgetApplicationRole, false, true, false},
		{"projector column SELECT", "periodic_budget_events", "SELECT(id)", PeriodicBudgetProjectionRole, true, false, false},
		{"projector table SELECT", "periodic_budget_controls", "SELECT", PeriodicBudgetProjectionRole, true, false, false},
		{"projector INSERT", "periodic_budget_operations", "INSERT", PeriodicBudgetProjectionRole, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := f.group(t)
			qg := pgx.Identifier{group}.Sanitize()
			sql := "GRANT " + tc.grant + " ON " + f.table(tc.table) + " TO " + qg
			if tc.grantOption {
				sql += " WITH GRANT OPTION"
			}
			f.exec(t, sql)
			target := f.appRole
			if tc.role == PeriodicBudgetProjectionRole {
				target = f.projectionRole
			}
			f.exec(t, fmt.Sprintf("GRANT %s TO %s WITH INHERIT %t, SET %t", qg, pgx.Identifier{target}.Sanitize(), tc.inherit, tc.set))
			f.expectRejected(t, tc.role, true)
		})
	}
	for _, tc := range []struct{ table, grant string }{{"periodic_budget_events", "SELECT(id)"}, {"periodic_budget_controls", "UPDATE(enabled)"}} {
		t.Run("PUBLIC "+tc.grant, func(t *testing.T) {
			f.exec(t, "GRANT "+tc.grant+" ON "+f.table(tc.table)+" TO PUBLIC")
			t.Cleanup(func() { f.exec(t, "REVOKE "+tc.grant+" ON "+f.table(tc.table)+" FROM PUBLIC") })
			f.expectRejected(t, PeriodicBudgetProjectionRole, true)
		})
	}
}

func TestPeriodicBudgetDirectDriftRepairAndReadOnlyRejection(t *testing.T) {
	f := newPeriodicRoleFixture(t, false)
	for _, tc := range []struct {
		name, grant string
		role        PeriodicBudgetDatabaseRole
	}{{"app extra immutable column", "UPDATE(operation_id)", PeriodicBudgetApplicationRole}, {"projector direct column SELECT", "SELECT(operation_id)", PeriodicBudgetProjectionRole}} {
		t.Run(tc.name, func(t *testing.T) {
			target := f.appRole
			if tc.role == PeriodicBudgetProjectionRole {
				target = f.projectionRole
			}
			f.exec(t, "GRANT "+tc.grant+" ON "+f.table("periodic_budget_operations")+" TO "+pgx.Identifier{target}.Sanitize())
			f.expectRejected(t, tc.role, false)
			periodicRoleMust(t, "repair exact direct ACL", ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole))
			f.expectValid(t)
		})
	}
	qa := pgx.Identifier{f.appRole}.Sanitize()
	f.exec(t, "REVOKE UPDATE(enabled) ON "+f.table("periodic_budget_controls")+" FROM "+qa)
	f.expectRejected(t, PeriodicBudgetApplicationRole, false)
	var restored bool
	periodicRoleMust(t, "read-only verifier did not repair missing column", f.owner.QueryRow(f.ctx, "SELECT has_column_privilege($1,$2,'enabled','UPDATE')", f.appRole, f.table("periodic_budget_controls")).Scan(&restored))
	if restored {
		t.Fatal("verification repaired column ACL")
	}
	group := f.group(t)
	qg := pgx.Identifier{group}.Sanitize()
	f.exec(t, "GRANT UPDATE(operation_id) ON "+f.table("periodic_budget_operations")+" TO "+qg)
	f.exec(t, "GRANT "+qg+" TO "+qa+" WITH INHERIT TRUE")
	f.expectRejected(t, PeriodicBudgetApplicationRole, true)
	periodicRoleMust(t, "rejected provision preserved prior missing UPDATE", f.owner.QueryRow(f.ctx, "SELECT has_column_privilege($1,$2,'enabled','UPDATE')", f.appRole, f.table("periodic_budget_controls")).Scan(&restored))
	if restored {
		t.Fatal("failed provision left partial grants")
	}
}

func TestPeriodicBudgetOwnersSchemaAndElevatedAttributes(t *testing.T) {
	f := newPeriodicRoleFixture(t, false)
	qa, qp, qs := pgx.Identifier{f.appRole}.Sanitize(), pgx.Identifier{f.projectionRole}.Sanitize(), pgx.Identifier{f.schema}.Sanitize()
	var owner string
	periodicRoleMust(t, "read fixture owner", f.owner.QueryRow(f.ctx, "SELECT current_user").Scan(&owner))
	qo := pgx.Identifier{owner}.Sanitize()
	t.Run("schema CREATE", func(t *testing.T) {
		f.exec(t, "GRANT CREATE ON SCHEMA "+qs+" TO "+qa)
		t.Cleanup(func() { f.exec(t, "REVOKE CREATE ON SCHEMA "+qs+" FROM "+qa) })
		f.expectRejected(t, PeriodicBudgetApplicationRole, true)
	})
	t.Run("schema grant option", func(t *testing.T) {
		f.exec(t, "GRANT USAGE ON SCHEMA "+qs+" TO "+qa+" WITH GRANT OPTION")
		t.Cleanup(func() { f.exec(t, "REVOKE GRANT OPTION FOR USAGE ON SCHEMA "+qs+" FROM "+qa) })
		f.expectRejected(t, PeriodicBudgetApplicationRole, true)
	})
	t.Run("table owner", func(t *testing.T) {
		table := f.table("periodic_budget_events")
		f.exec(t, "ALTER TABLE "+table+" OWNER TO "+qa)
		t.Cleanup(func() { f.exec(t, "ALTER TABLE "+table+" OWNER TO "+qo) })
		f.expectRejected(t, PeriodicBudgetApplicationRole, true)
	})
	t.Run("schema owner", func(t *testing.T) {
		f.exec(t, "ALTER SCHEMA "+qs+" OWNER TO "+qp)
		t.Cleanup(func() { f.exec(t, "ALTER SCHEMA "+qs+" OWNER TO "+qo) })
		f.expectRejected(t, PeriodicBudgetProjectionRole, true)
	})
	for _, flag := range []string{"SUPERUSER", "CREATEROLE", "CREATEDB", "REPLICATION", "BYPASSRLS"} {
		for _, role := range []PeriodicBudgetDatabaseRole{PeriodicBudgetApplicationRole, PeriodicBudgetProjectionRole} {
			t.Run(string(role)+" "+flag, func(t *testing.T) {
				q := qa
				if role == PeriodicBudgetProjectionRole {
					q = qp
				}
				f.exec(t, "ALTER ROLE "+q+" "+flag)
				t.Cleanup(func() { f.exec(t, "ALTER ROLE "+q+" NO"+flag) })
				f.expectRejected(t, role, true)
			})
		}
	}
	t.Run("SET descendant elevated attribute", func(t *testing.T) {
		group := f.group(t)
		qg := pgx.Identifier{group}.Sanitize()
		f.exec(t, "ALTER ROLE "+qg+" CREATEROLE")
		f.exec(t, "GRANT "+qg+" TO "+qa+" WITH INHERIT FALSE, SET TRUE")
		f.expectRejected(t, PeriodicBudgetApplicationRole, true)
	})
}

func TestPeriodicBudgetAdministrativeClosureAndActualEscalation(t *testing.T) {
	f := newPeriodicRoleFixture(t, false)
	for _, tc := range []struct {
		name              string
		role              PeriodicBudgetDatabaseRole
		chain, adminChain bool
	}{{"app direct ADMIN", PeriodicBudgetApplicationRole, false, false}, {"projector direct ADMIN", PeriodicBudgetProjectionRole, false, false}, {"ADMIN then SET", PeriodicBudgetApplicationRole, true, false}, {"ADMIN then ADMIN", PeriodicBudgetApplicationRole, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			group, descendant := f.group(t), f.group(t)
			qg, qd := pgx.Identifier{group}.Sanitize(), pgx.Identifier{descendant}.Sanitize()
			target, pool, grant := f.appRole, f.app, "UPDATE(operation_id)"
			if tc.role == PeriodicBudgetProjectionRole {
				target, pool, grant = f.projectionRole, f.projection, "SELECT(operation_id)"
			}
			qt := pgx.Identifier{target}.Sanitize()
			grantee := qg
			if tc.chain {
				grantee = qd
				if tc.adminChain {
					f.exec(t, "GRANT "+qd+" TO "+qg+" WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
				} else {
					f.exec(t, "GRANT "+qd+" TO "+qg+" WITH INHERIT FALSE, SET TRUE")
				}
			}
			f.exec(t, "GRANT "+grant+" ON "+f.table("periodic_budget_operations")+" TO "+grantee)
			f.exec(t, "GRANT "+qg+" TO "+qt+" WITH INHERIT FALSE, SET FALSE")
			f.expectValid(t)
			f.exec(t, "GRANT "+qg+" TO "+qt+" WITH ADMIN TRUE")
			f.expectRejected(t, tc.role, true)
			_, e := pool.Exec(f.ctx, "GRANT "+qg+" TO "+qt+" WITH SET TRUE")
			periodicRoleMust(t, "actual LOGIN can exercise ADMIN escalation", e)
			var canSet bool
			periodicRoleMust(t, "actual self-grant enabled SET", pool.QueryRow(f.ctx, "SELECT pg_has_role(current_user,$1,'SET')", group).Scan(&canSet))
			if !canSet {
				t.Fatal("actual ADMIN escalation did not enable SET")
			}
			f.expectRejected(t, tc.role, true)
		})
	}
}

func TestPeriodicBudgetHistorical019OnlySkipsFullyAbsentSet(t *testing.T) {
	f := newPeriodicRoleFixture(t, true)
	for n := 0; n < 2; n++ {
		periodicRoleMust(t, "repeat historical019 Provision", ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole))
	}
	periodicRoleMust(t, "exact019 remains intact", migrations.VerifyThrough(f.ctx, f.owner, "019_operations_alerts.sql"))
	if e := VerifyPeriodicBudgetRole(f.ctx, f.app, PeriodicBudgetApplicationRole); !errors.Is(e, ErrPeriodicBudgetRolePrivileges) {
		t.Fatal("runtime verifier accepted historical missing table set")
	}
	f.exec(t, "CREATE TABLE "+f.table("periodic_budget_controls")+"(dummy integer)")
	qa := pgx.Identifier{f.appRole}.Sanitize()
	f.exec(t, "REVOKE INSERT ON "+f.table("tenants")+" FROM "+qa)
	if e := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); !errors.Is(e, ErrPeriodicBudgetRolePrivileges) {
		t.Fatalf("partial periodic set accepted: %v", e)
	}
	var insert bool
	periodicRoleMust(t, "read historical rollback ACL", f.owner.QueryRow(f.ctx, "SELECT has_table_privilege($1,$2,'INSERT')", f.appRole, f.table("tenants")).Scan(&insert))
	if insert {
		t.Fatal("partial-set rejection left blanket grant changes")
	}
}
