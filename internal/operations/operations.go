// Package operations contains offline, explicitly gated maintenance commands.
package operations

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/security"
	"strings"
)

func Provision(ctx context.Context, p *pgxpool.Pool) error {
	return ProvisionFor(ctx, p, "public", "moni_app", "moni_projection")
}

// ProvisionFor supports isolated deployments and integration fixtures. Every identifier is quoted.
func ProvisionFor(ctx context.Context, p *pgxpool.Pool, schema, appRole, projectionRole string) error {
	tx, e := p.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	qs := pgx.Identifier{schema}.Sanitize()
	qa := pgx.Identifier{appRole}.Sanitize()
	qp := pgx.Identifier{projectionRole}.Sanitize()
	table := func(name string) string { return pgx.Identifier{schema, name}.Sanitize() }
	immutable := []string{"journals", "journal_lines", "audit_events", "price_versions", "cost_versions", "model_capability_versions", "channel_versions", "channel_version_credentials", "route_policy_versions", "model_bindings", "metering_events", "finalization_records", "metering_event_conflicts", "projection_outbox", "provider_cost_corrections", "delivery_observations", "route_binding_isolations", "channel_model_discoveries", "channel_model_discovery_results", "channel_batch_operations", "credential_procurement_bindings", "procurement_budget_events", "procurement_evidence_identities", "procurement_commands", "procurement_balance_observations", "funding_receipts", "commerce_commands", "commerce_operation_receipts", "vouchers", "voucher_redemptions", "voucher_revocations", "voucher_revocation_decisions"}
	for i := range immutable {
		immutable[i] = table(immutable[i])
	}
	sql := fmt.Sprintf(`REVOKE CREATE ON SCHEMA %s FROM PUBLIC;
 GRANT USAGE ON SCHEMA %s TO %s,%s;
 GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA %s TO %s;
 REVOKE INSERT,UPDATE,DELETE ON %s FROM %s;
 REVOKE UPDATE,DELETE ON %s FROM %s;
 REVOKE DELETE ON %s,%s,%s FROM %s;
 REVOKE UPDATE,DELETE ON %s FROM %s;
 GRANT UPDATE(refunded_amount) ON %s TO %s;
 GRANT SELECT ON %s,%s,%s,%s TO %s;
 GRANT INSERT ON %s,%s TO %s;
 GRANT UPDATE ON %s TO %s;`, qs, qs, qa, qp, qs, qa, table("schema_migrations"), qa, strings.Join(immutable, ","), qa, table("procurement_accounts"), table("procurement_reservations"), table("procurement_cost_proposals"), qa, table("settlements"), qa, table("settlements"), qa, table("projection_outbox"), table("active_report_projection"), table("projection_receipts"), table("report_totals"), qp, table("projection_receipts"), table("report_totals"), qp, table("report_totals"), qp)
	_, e = tx.Exec(ctx, sql)
	if e != nil {
		return e
	}
	// Customer money changes only the balance, never account ownership or kind.
	// The current payment/wallet ACL pass below also clears any old column grants.
	_, e = tx.Exec(ctx, fmt.Sprintf(`REVOKE UPDATE,DELETE ON %s FROM %s;
 GRANT UPDATE(balance) ON %s TO %s;`, table("ledger_accounts"), qa, table("ledger_accounts"), qa))
	if e != nil {
		return e
	}
	// Blanket grants above must not expose immutable identity ownership or a
	// general mail-payload replacement permission to the online application.
	_, e = tx.Exec(ctx, fmt.Sprintf(`REVOKE UPDATE,DELETE ON %s FROM %s;
 REVOKE UPDATE ON %s,%s FROM %s;
 GRANT UPDATE(consumed_at,revoked_at,registration_email_ciphertext) ON %s TO %s;
 GRANT UPDATE(payload_ciphertext,state,attempt_count,next_attempt_at,claim_generation,lease_until,sent_at,last_error_code) ON %s TO %s;
 REVOKE ALL ON %s,%s,%s,%s FROM %s;`,
		table("identity_initialization"), qa, table("identity_challenges"), table("identity_mail_outbox"), qa,
		table("identity_challenges"), qa, table("identity_mail_outbox"), qa,
		table("identity_initialization"), table("identity_mail_subjects"), table("identity_challenges"), table("identity_mail_outbox"), qp))
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, fmt.Sprintf(`REVOKE INSERT,UPDATE,DELETE ON %s FROM %s;
 GRANT UPDATE(enabled) ON %s TO %s;
 REVOKE UPDATE,DELETE ON %s,%s FROM %s;
 GRANT UPDATE(state,approver,activated_at,terminal_actor,terminal_at,redeemed_count) ON %s TO %s;
 GRANT UPDATE(state,approver,journal_id) ON %s TO %s;
 REVOKE ALL ON %s FROM %s;`, table("voucher_policy"), qa, table("voucher_policy"), qa, table("voucher_batches"), table("credit_orders"), qa, table("voucher_batches"), qa, table("credit_orders"), qa,
		strings.Join([]string{table("funding_receipts"), table("voucher_policy"), table("commerce_commands"), table("commerce_operation_receipts"), table("voucher_batches"), table("vouchers"), table("voucher_revocations"), table("voucher_revocation_decisions"), table("voucher_redemptions")}, ","), qp))
	if e != nil {
		return e
	}
	// Revoke table-level UPDATE before granting the pricing/lifecycle columns.
	// Repeated provisioning must not restore broad writes from the initial grant.
	_, e = tx.Exec(ctx, fmt.Sprintf(`REVOKE UPDATE,DELETE ON %s,%s FROM %s;
 REVOKE UPDATE,DELETE ON %s FROM %s;
 GRANT UPDATE(label,state,revision,last_operation_id) ON %s TO %s;
 REVOKE UPDATE,DELETE ON %s FROM %s;
 GRANT UPDATE(name,status,allowed_regions,price_group_id,pricing_revision,pricing_operation_id) ON %s TO %s;
 REVOKE UPDATE ON %s FROM %s;
 GRANT UPDATE(status,billing_status,latest_finalization_id) ON %s TO %s;
 REVOKE ALL ON %s,%s,%s FROM %s;`,
		table("pricing_operations"), table("tenant_pricing_assignments"), qa,
		table("price_groups"), qa, table("price_groups"), qa,
		table("tenants"), qa, table("tenants"), qa,
		table("request_records"), qa, table("request_records"), qa,
		table("price_groups"), table("pricing_operations"), table("tenant_pricing_assignments"), qp))
	if e != nil {
		return e
	}
	// Alerts observe business rows with existing SELECT privileges. Their
	// protected history is writable only through the closed alert state machine.
	alertTables := strings.Join([]string{table("alert_control"), table("alert_commands"), table("alert_rules"), table("alert_rule_runtime"), table("alert_episodes"), table("alert_episode_events")}, ",")
	_, e = tx.Exec(ctx, fmt.Sprintf(`REVOKE ALL ON %s FROM %s;
 REVOKE ALL ON %s FROM %s;
 GRANT SELECT ON %s TO %s;
 GRANT UPDATE(singleton) ON %s TO %s;
 GRANT INSERT,UPDATE ON %s,%s,%s,%s TO %s;
 GRANT INSERT ON %s TO %s;`,
		alertTables, qp, alertTables, qa, alertTables, qa,
		table("alert_control"), qa,
		table("alert_commands"), table("alert_rules"), table("alert_rule_runtime"), table("alert_episodes"), qa,
		table("alert_episode_events"), qa))
	if e != nil {
		return e
	}
	if e = verifyAlertRolesFor(ctx, tx, schema, appRole, projectionRole); e != nil {
		return e
	}
	if e = provisionPeriodicBudgetRoles(ctx, tx, schema, appRole, projectionRole); e != nil {
		return e
	}
	if e = provisionPaymentRoles(ctx, tx, schema, appRole, projectionRole); e != nil {
		return e
	}
	if e = provisionMeterRoles(ctx, tx, schema, appRole, projectionRole); e != nil {
		return e
	}
	if e = provisionMediaRuntimeRoles(ctx, tx, schema, appRole, projectionRole); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func Rewrap(ctx context.Context, p *pgxpool.Pool, keys *security.Secrets) error {
	tx, e := p.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// Triggers identify the owner via current_user, including SET ROLE. Check
	// before any writes so an online app invocation cannot partially rewrap.
	var owner bool
	if e = tx.QueryRow(ctx, `SELECT count(*)=4 AND bool_and(pg_get_userbyid(relowner)=current_user) FROM pg_class WHERE oid IN ('channel_credentials'::regclass,'users'::regclass,'identity_challenges'::regclass,'identity_mail_outbox'::regclass)`).Scan(&owner); e != nil {
		return e
	}
	if !owner {
		return fmt.Errorf("rewrap requires table owner role")
	}
	rows, e := tx.Query(ctx, "SELECT id,secret_ciphertext FROM channel_credentials ORDER BY id FOR UPDATE")
	if e != nil {
		return e
	}
	type entry struct{ id, value string }
	all := []entry{}
	for rows.Next() {
		var v entry
		if e = rows.Scan(&v.id, &v.value); e != nil {
			rows.Close()
			return e
		}
		all = append(all, v)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, v := range all {
		plain, e := keys.OpenFor(v.value, "provider:platform", v.id)
		if e != nil {
			return e
		}
		encrypted, e := keys.SealFor(plain, "provider:platform", v.id)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "UPDATE channel_credentials SET secret_ciphertext=$2 WHERE id=$1", v.id, encrypted); e != nil {
			return e
		}
	}
	rows, e = tx.Query(ctx, "SELECT id,mfa_secret,mfa_pending_secret FROM users ORDER BY id FOR UPDATE")
	if e != nil {
		return e
	}
	type user struct{ id, secret, pending string }
	users := []user{}
	for rows.Next() {
		var u user
		if e = rows.Scan(&u.id, &u.secret, &u.pending); e != nil {
			rows.Close()
			return e
		}
		users = append(users, u)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, u := range users {
		seal := func(v string) (string, error) {
			if v == "" {
				return "", nil
			}
			plain, e := keys.OpenFor(v, "mfa:user", u.id)
			if e != nil {
				return "", e
			}
			return keys.SealFor(plain, "mfa:user", u.id)
		}
		a, e := seal(u.secret)
		if e != nil {
			return e
		}
		b, e := seal(u.pending)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "UPDATE users SET mfa_secret=$2,mfa_pending_secret=$3 WHERE id=$1", u.id, a, b); e != nil {
			return e
		}
	}
	// The maintenance command already owns the single-instance maintenance lock;
	// it must run without a worker. Use each record's original AAD identity.
	for _, spec := range []struct{ table, column, domain string }{
		{"identity_challenges", "registration_email_ciphertext", "identity-registration"},
		{"identity_mail_outbox", "payload_ciphertext", "identity-mail-outbox"},
	} {
		rows, e = tx.Query(ctx, fmt.Sprintf("SELECT id::text,%s FROM %s WHERE %s IS NOT NULL ORDER BY id FOR UPDATE", pgx.Identifier{spec.column}.Sanitize(), pgx.Identifier{spec.table}.Sanitize(), pgx.Identifier{spec.column}.Sanitize()))
		if e != nil {
			return e
		}
		entries := []entry{}
		for rows.Next() {
			var v entry
			if e = rows.Scan(&v.id, &v.value); e != nil {
				rows.Close()
				return e
			}
			entries = append(entries, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		for _, v := range entries {
			plain, e := keys.OpenFor(v.value, spec.domain, v.id)
			if e != nil {
				return e
			}
			sealed, e := keys.SealFor(plain, spec.domain, v.id)
			if e != nil {
				return e
			}
			verified, e := keys.OpenFor(sealed, spec.domain, v.id)
			if e != nil {
				return e
			}
			if verified != plain {
				return fmt.Errorf("rewrap plaintext mismatch")
			}
			if _, e = tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s=$2 WHERE id=$1", pgx.Identifier{spec.table}.Sanitize(), pgx.Identifier{spec.column}.Sanitize()), v.id, sealed); e != nil {
				return e
			}
		}
	}
	if _, e = tx.Exec(ctx, "INSERT INTO audit_events(actor,action,target) VALUES('offline-maintenance','encryption.rewrap','credential-mfa-and-identity-keyring')"); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
