package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func alertRoleLogin(t *testing.T, f *operationFixture, role string) *pgxpool.Pool {
	t.Helper()
	// The ephemeral hex password cannot contain SQL delimiters. It exists only
	// in this isolated role and connection config, never in test output.
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := f.owner.Exec(f.ctx, `ALTER ROLE `+pgx.Identifier{role}.Sanitize()+` LOGIN PASSWORD '`+password+`'`); err != nil {
		t.Fatal("cannot enable isolated role login")
	}
	cfg := f.owner.Config()
	cfg.AfterConnect = nil
	cfg.ConnConfig.User, cfg.ConnConfig.Password = role, password
	p, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal("cannot construct isolated login")
	}
	t.Cleanup(p.Close)
	if err = p.Ping(f.ctx); err != nil {
		t.Fatal("isolated role login failed")
	}
	var current, session string
	if err = p.QueryRow(f.ctx, `SELECT current_user,session_user`).Scan(&current, &session); err != nil || current != role || session != role {
		t.Fatal("role qualification must use an actual restricted LOGIN")
	}
	return p
}

func alertRoleExec(t *testing.T, f *operationFixture, sql string, args ...any) {
	t.Helper()
	if _, err := f.owner.Exec(f.ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
}

func requireAlertRolesRejected(t *testing.T, f *operationFixture, app, projection *pgxpool.Pool, role AlertDatabaseRole) {
	t.Helper()
	if err := VerifyAlertRolesFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); !errors.Is(err, ErrAlertRolePrivileges) {
		t.Fatalf("offline role verification accepted invalid boundary: %v", err)
	}
	pool := app
	if role == AlertProjectionRole {
		pool = projection
	}
	if err := VerifyAlertRole(f.ctx, pool, role); !errors.Is(err, ErrAlertRolePrivileges) {
		t.Fatalf("startup role verification accepted invalid boundary: %v", err)
	}
	if err := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); !errors.Is(err, ErrAlertRolePrivileges) {
		t.Fatalf("provision accepted persistent effective privilege excess: %v", err)
	}
}

func TestPostgresAlertRoleVerificationAllowsActualLoginsAndPermittedGroups(t *testing.T) {
	f := operationDB(t)
	app := alertRoleLogin(t, f, f.appRole)
	projection := alertRoleLogin(t, f, f.projectionRole)
	for pass := 0; pass < 2; pass++ {
		if err := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); err != nil {
			t.Fatal(err)
		}
		if err := VerifyAlertRolesFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); err != nil {
			t.Fatal(err)
		}
		if err := VerifyAlertRole(f.ctx, app, AlertApplicationRole); err != nil {
			t.Fatal(err)
		}
		if err := VerifyAlertRole(f.ctx, projection, AlertProjectionRole); err != nil {
			t.Fatal(err)
		}
	}
	group := "alert_low_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	qg := pgx.Identifier{group}.Sanitize()
	alertRoleExec(t, f, `CREATE ROLE `+qg+` NOLOGIN`)
	t.Cleanup(func() {
		alertRoleExec(t, f, `DROP OWNED BY `+qg)
		alertRoleExec(t, f, `DROP ROLE `+qg)
	})
	alertRoleExec(t, f, `GRANT SELECT ON `+pgx.Identifier{f.schema, "alert_rules"}.Sanitize()+`,`+pgx.Identifier{f.schema, "procurement_accounts"}.Sanitize()+` TO `+qg)
	alertRoleExec(t, f, `GRANT `+qg+` TO `+pgx.Identifier{f.appRole}.Sanitize()+` WITH INHERIT TRUE`)
	if err := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); err != nil {
		t.Fatal("allowed inherited subset rejected", err)
	}
	if err := VerifyAlertRole(f.ctx, app, AlertApplicationRole); err != nil {
		t.Fatal("allowed SET ROLE subset rejected", err)
	}
	// SET ROLE domain-test pools still work for domain operations, but an owner
	// session cannot qualify as a restricted production connection.
	if err := VerifyAlertRole(f.ctx, f.app, AlertApplicationRole); !errors.Is(err, ErrAlertRolePrivileges) {
		t.Fatal("owner session disguised by SET ROLE qualified for startup", err)
	}
	if err := VerifyAlertRole(f.ctx, f.projection, AlertProjectionRole); !errors.Is(err, ErrAlertRolePrivileges) {
		t.Fatal("owner projection session disguised by SET ROLE qualified", err)
	}
}

func TestPostgresAlertRoleVerificationRejectsEffectiveGroupAndColumnExcess(t *testing.T) {
	f := operationDB(t)
	app := alertRoleLogin(t, f, f.appRole)
	projection := alertRoleLogin(t, f, f.projectionRole)
	table := func(name string) string { return pgx.Identifier{f.schema, name}.Sanitize() }
	for _, tc := range []struct {
		name, grant, privilege, table string
		role                          AlertDatabaseRole
		inherit                       bool
		column                        bool
	}{
		{"app inherited event update", "UPDATE", "UPDATE", "alert_episode_events", AlertApplicationRole, true, false},
		{"app inherited event column update", "UPDATE(actor)", "UPDATE", "alert_episode_events", AlertApplicationRole, true, true},
		{"projector inherited column select", "SELECT(actor)", "SELECT", "alert_episode_events", AlertProjectionRole, true, true},
		{"app inherited grant option", "SELECT", "SELECT WITH GRANT OPTION", "alert_rules", AlertApplicationRole, true, false},
		{"app noinherit set role event update", "UPDATE", "UPDATE", "alert_episode_events", AlertApplicationRole, false, false},
		{"app inherited delete", "DELETE", "DELETE", "alert_commands", AlertApplicationRole, true, false},
		{"projector inherited table select", "SELECT", "SELECT", "alert_rules", AlertProjectionRole, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := "alert_excess_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			qg := pgx.Identifier{group}.Sanitize()
			alertRoleExec(t, f, `CREATE ROLE `+qg+` NOLOGIN`)
			t.Cleanup(func() {
				alertRoleExec(t, f, `DROP OWNED BY `+qg)
				alertRoleExec(t, f, `DROP ROLE `+qg)
			})
			sql := `GRANT ` + tc.grant + ` ON ` + table(tc.table) + ` TO ` + qg
			if strings.Contains(tc.privilege, "GRANT OPTION") {
				sql += ` WITH GRANT OPTION`
			}
			alertRoleExec(t, f, sql)
			target := f.appRole
			if tc.role == AlertProjectionRole {
				target = f.projectionRole
			}
			alertRoleExec(t, f, `GRANT `+qg+` TO `+pgx.Identifier{target}.Sanitize()+` WITH INHERIT `+strings.ToUpper(fmt.Sprint(tc.inherit)))
			requireAlertRolesRejected(t, f, app, projection, tc.role)
			fn := "has_table_privilege"
			if tc.column {
				fn = "has_any_column_privilege"
			}
			var retained bool
			if err := f.owner.QueryRow(f.ctx, `SELECT pg_catalog.`+fn+`($1,$2,$3)`, group, table(tc.table), tc.privilege).Scan(&retained); err != nil || !retained {
				t.Fatal("rejected provision changed external group privileges", retained, err)
			}
		})
	}
	for _, role := range []AlertDatabaseRole{AlertApplicationRole, AlertProjectionRole} {
		t.Run(string(role)+" startup rejects direct column drift and provision repairs it", func(t *testing.T) {
			target, grant := f.appRole, "UPDATE(actor)"
			if role == AlertProjectionRole {
				target, grant = f.projectionRole, "SELECT(actor)"
			}
			q := pgx.Identifier{target}.Sanitize()
			alertRoleExec(t, f, `GRANT `+grant+` ON `+table("alert_episode_events")+` TO `+q)
			t.Cleanup(func() { alertRoleExec(t, f, `REVOKE `+grant+` ON `+table("alert_episode_events")+` FROM `+q) })
			pool := app
			if role == AlertProjectionRole {
				pool = projection
			}
			if err := VerifyAlertRole(f.ctx, pool, role); !errors.Is(err, ErrAlertRolePrivileges) {
				t.Fatal("startup accepted direct column drift", err)
			}
			// PostgreSQL 16 REVOKE ALL ON TABLE also clears these direct column
			// grants. Offline provisioning may repair a directly granted ACL.
			if err := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); err != nil {
				t.Fatal("repairable direct grant rejected", err)
			}
			if err := VerifyAlertRole(f.ctx, pool, role); err != nil {
				t.Fatal("repaired role rejected", err)
			}
		})
	}
	t.Run("PUBLIC column grant remains effective for projector", func(t *testing.T) {
		alertRoleExec(t, f, `GRANT SELECT(actor) ON `+table("alert_episode_events")+` TO PUBLIC`)
		t.Cleanup(func() { alertRoleExec(t, f, `REVOKE SELECT(actor) ON `+table("alert_episode_events")+` FROM PUBLIC`) })
		requireAlertRolesRejected(t, f, app, projection, AlertProjectionRole)
		var retained bool
		if err := f.owner.QueryRow(f.ctx, `SELECT pg_catalog.has_any_column_privilege($1,$2,'SELECT')`, f.projectionRole, table("alert_episode_events")).Scan(&retained); err != nil || !retained {
			t.Fatal("rejected provision changed PUBLIC privileges", retained, err)
		}
	})
}

func TestPostgresAlertRoleVerificationRejectsOwnerSchemaAndElevatedRoles(t *testing.T) {
	f := operationDB(t)
	app := alertRoleLogin(t, f, f.appRole)
	projection := alertRoleLogin(t, f, f.projectionRole)
	var owner string
	if err := f.owner.QueryRow(f.ctx, `SELECT current_user`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	qs, qa, qp := pgx.Identifier{f.schema}.Sanitize(), pgx.Identifier{f.appRole}.Sanitize(), pgx.Identifier{f.projectionRole}.Sanitize()
	t.Run("schema create", func(t *testing.T) {
		alertRoleExec(t, f, `GRANT CREATE ON SCHEMA `+qs+` TO `+qa)
		t.Cleanup(func() { alertRoleExec(t, f, `REVOKE CREATE ON SCHEMA `+qs+` FROM `+qa) })
		requireAlertRolesRejected(t, f, app, projection, AlertApplicationRole)
	})
	t.Run("schema grant option", func(t *testing.T) {
		alertRoleExec(t, f, `GRANT USAGE ON SCHEMA `+qs+` TO `+qa+` WITH GRANT OPTION`)
		t.Cleanup(func() { alertRoleExec(t, f, `REVOKE GRANT OPTION FOR USAGE ON SCHEMA `+qs+` FROM `+qa) })
		requireAlertRolesRejected(t, f, app, projection, AlertApplicationRole)
	})
	t.Run("table owner", func(t *testing.T) {
		table := pgx.Identifier{f.schema, "alert_episode_events"}.Sanitize()
		alertRoleExec(t, f, `ALTER TABLE `+table+` OWNER TO `+qa)
		t.Cleanup(func() { alertRoleExec(t, f, `ALTER TABLE `+table+` OWNER TO `+pgx.Identifier{owner}.Sanitize()) })
		requireAlertRolesRejected(t, f, app, projection, AlertApplicationRole)
	})
	t.Run("schema owner", func(t *testing.T) {
		alertRoleExec(t, f, `ALTER SCHEMA `+qs+` OWNER TO `+qp)
		t.Cleanup(func() { alertRoleExec(t, f, `ALTER SCHEMA `+qs+` OWNER TO `+pgx.Identifier{owner}.Sanitize()) })
		requireAlertRolesRejected(t, f, app, projection, AlertProjectionRole)
	})
	for _, flag := range []string{"SUPERUSER", "CREATEROLE", "CREATEDB", "REPLICATION", "BYPASSRLS"} {
		for _, role := range []AlertDatabaseRole{AlertApplicationRole, AlertProjectionRole} {
			t.Run(string(role)+" "+flag, func(t *testing.T) {
				q := qa
				if role == AlertProjectionRole {
					q = qp
				}
				alertRoleExec(t, f, `ALTER ROLE `+q+` `+flag)
				t.Cleanup(func() { alertRoleExec(t, f, `ALTER ROLE `+q+` NO`+flag) })
				requireAlertRolesRejected(t, f, app, projection, role)
			})
		}
	}
}

func TestPostgresAlertRoleVerificationIsReadOnlyAndProvisionRollsBackItsACL(t *testing.T) {
	f := operationDB(t)
	app := alertRoleLogin(t, f, f.appRole)
	table, qa := pgx.Identifier{f.schema, "alert_episode_events"}.Sanitize(), pgx.Identifier{f.appRole}.Sanitize()
	alertRoleExec(t, f, `REVOKE INSERT ON `+table+` FROM `+qa)
	if err := VerifyAlertRole(f.ctx, app, AlertApplicationRole); !errors.Is(err, ErrAlertRolePrivileges) {
		t.Fatal("missing required permission accepted", err)
	}
	var canInsert bool
	if err := f.owner.QueryRow(f.ctx, `SELECT pg_catalog.has_table_privilege($1,$2,'INSERT')`, f.appRole, table).Scan(&canInsert); err != nil || canInsert {
		t.Fatal("read-only check repaired the ACL", err)
	}
	// A persistent group violation rejects the final check. The earlier
	// blanket grant must also roll back instead of silently restoring INSERT.
	group := "alert_rollback_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	qg := pgx.Identifier{group}.Sanitize()
	alertRoleExec(t, f, `CREATE ROLE `+qg+` NOLOGIN`)
	t.Cleanup(func() {
		alertRoleExec(t, f, `DROP OWNED BY `+qg)
		alertRoleExec(t, f, `DROP ROLE `+qg)
	})
	alertRoleExec(t, f, `GRANT UPDATE(actor) ON `+table+` TO `+qg)
	alertRoleExec(t, f, `GRANT `+qg+` TO `+qa+` WITH INHERIT TRUE`)
	if err := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); !errors.Is(err, ErrAlertRolePrivileges) {
		t.Fatal("group violation was not rejected", err)
	}
	if err := f.owner.QueryRow(f.ctx, `SELECT pg_catalog.has_table_privilege($1,$2,'INSERT')`, f.appRole, table).Scan(&canInsert); err != nil || canInsert {
		t.Fatal("rejected provision left partial ACL changes", err)
	}
	alertRoleExec(t, f, `REVOKE UPDATE(actor) ON `+table+` FROM `+qg)
	if err := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAlertRole(f.ctx, app, AlertApplicationRole); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAlertRolesFor(f.ctx, f.owner, f.schema, f.appRole, f.appRole); !errors.Is(err, ErrAlertRolePrivileges) {
		t.Fatal("shared app/projector role accepted", err)
	}
	if err := VerifyAlertRole(context.Background(), nil, AlertApplicationRole); !errors.Is(err, ErrAlertRolePrivileges) {
		t.Fatal("nil verification pool accepted", err)
	}
}

func TestPostgresAlertRoleVerificationIncludesAdministrativeMembershipClosure(t *testing.T) {
	f := operationDB(t)
	app := alertRoleLogin(t, f, f.appRole)
	projection := alertRoleLogin(t, f, f.projectionRole)
	for _, tc := range []struct {
		name       string
		role       AlertDatabaseRole
		chained    bool
		adminChain bool
	}{
		{"app direct admin", AlertApplicationRole, false, false},
		{"projector direct admin", AlertProjectionRole, false, false},
		{"app admin then set chain", AlertApplicationRole, true, false},
		{"app admin then admin chain", AlertApplicationRole, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			group, descendant := "alert_admin_"+suffix, "alert_desc_"+suffix
			qg, qd := pgx.Identifier{group}.Sanitize(), pgx.Identifier{descendant}.Sanitize()
			alertRoleExec(t, f, `CREATE ROLE `+qg+` NOLOGIN`)
			alertRoleExec(t, f, `CREATE ROLE `+qd+` NOLOGIN`)
			t.Cleanup(func() {
				for _, q := range []string{qd, qg} {
					alertRoleExec(t, f, `DROP OWNED BY `+q)
					alertRoleExec(t, f, `DROP ROLE `+q)
				}
			})
			target, pool, privilege := f.appRole, app, "UPDATE"
			if tc.role == AlertProjectionRole {
				target, pool, privilege = f.projectionRole, projection, "SELECT"
			}
			qt := pgx.Identifier{target}.Sanitize()
			grantee := qg
			if tc.chained {
				grantee = qd
				if tc.adminChain {
					alertRoleExec(t, f, `GRANT `+qd+` TO `+qg+` WITH ADMIN TRUE`)
					alertRoleExec(t, f, `GRANT `+qd+` TO `+qg+` WITH INHERIT FALSE`)
					alertRoleExec(t, f, `GRANT `+qd+` TO `+qg+` WITH SET FALSE`)
				} else {
					alertRoleExec(t, f, `GRANT `+qd+` TO `+qg+` WITH SET TRUE`)
				}
			}
			alertRoleExec(t, f, `GRANT `+privilege+` ON `+pgx.Identifier{f.schema, "alert_episode_events"}.Sanitize()+` TO `+grantee)
			alertRoleExec(t, f, `GRANT `+qg+` TO `+qt+` WITH INHERIT FALSE`)
			alertRoleExec(t, f, `GRANT `+qg+` TO `+qt+` WITH SET FALSE`)
			// An inaccessible ordinary membership is harmless, even if the group
			// has privileges the runtime itself must never obtain.
			if err := VerifyAlertRole(f.ctx, pool, tc.role); err != nil {
				t.Fatal("inaccessible membership was rejected", err)
			}
			alertRoleExec(t, f, `GRANT `+qg+` TO `+qt+` WITH ADMIN TRUE`)
			requireAlertRolesRejected(t, f, app, projection, tc.role)
			// Prove the escalation using the actual restricted LOGIN, not the
			// fixture owner or a superuser SET ROLE session.
			if _, err := pool.Exec(f.ctx, `GRANT `+qg+` TO `+qt+` WITH SET TRUE`); err != nil {
				t.Fatal("actual LOGIN could not exercise its ADMIN membership", err)
			}
			var canSet bool
			if err := pool.QueryRow(f.ctx, `SELECT pg_catalog.pg_has_role(current_user,$1,'SET')`, group).Scan(&canSet); err != nil || !canSet {
				t.Fatal("ADMIN self-grant did not enable SET ROLE", err)
			}
			if tc.adminChain {
				tx, err := pool.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(f.ctx)
				if _, err = tx.Exec(f.ctx, `SET LOCAL ROLE `+qg); err == nil {
					_, err = tx.Exec(f.ctx, `GRANT `+qd+` TO `+qg+` WITH SET TRUE`)
				}
				if err != nil {
					t.Fatal("actual LOGIN could not exercise recursive ADMIN path", err)
				}
				if err = tx.Commit(f.ctx); err != nil {
					t.Fatal(err)
				}
			}
			requireAlertRolesRejected(t, f, app, projection, tc.role)
		})
	}
}
