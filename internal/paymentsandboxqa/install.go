package paymentsandboxqa

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxschema"
	"github.com/moni-ai/moni-aigate/migrations"
)

func (e *Environment) install(ctx context.Context) error {
	p, err := e.OpenPool(ctx, "app_owner")
	if err != nil {
		return err
	}
	defer p.Close()
	if migrations.Apply(ctx, p) != nil {
		return stage("application_migrations")
	}
	for i := 0; i < 2; i++ {
		if operations.ProvisionFor(ctx, p, e.Schemas["app"], e.Roles["app_runtime"], e.Roles["app_projector"]) != nil {
			return stage("application_provision")
		}
	}
	if operations.VerifyAlertRolesFor(ctx, p, e.Schemas["app"], e.Roles["app_runtime"], e.Roles["app_projector"]) != nil {
		return stage("application_roles")
	}
	for _, component := range []string{"mock", "receiver"} {
		if err = e.installComponent(ctx, component); err != nil {
			return err
		}
	}
	a, err := e.adminPool(ctx)
	if err != nil {
		return err
	}
	defer a.Close()
	for _, c := range []string{"app", "mock", "receiver"} {
		schema := ident(e.Schemas[c])
		for _, sql := range []string{"REVOKE ALL ON SCHEMA " + schema + " FROM PUBLIC", "REVOKE ALL ON ALL TABLES IN SCHEMA " + schema + " FROM PUBLIC", "REVOKE ALL ON ALL FUNCTIONS IN SCHEMA " + schema + " FROM PUBLIC"} {
			if _, err = a.Exec(ctx, sql); err != nil {
				return stage("revoke")
			}
		}
		if c == "app" {
			if _, err = a.Exec(ctx, "GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA "+schema+" TO "+ident(e.Roles["app_runtime"])+","+ident(e.Roles["app_projector"])); err != nil {
				return stage("application_execute")
			}
			continue
		}
		role := ident(e.Roles[c+"_runtime"])
		for _, sql := range []string{"GRANT USAGE ON SCHEMA " + schema + " TO " + role, "GRANT SELECT ON ALL TABLES IN SCHEMA " + schema + " TO " + role, "GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA " + schema + " TO " + role} {
			if _, err = a.Exec(ctx, sql); err != nil {
				return stage("grant")
			}
		}
		inventory, x := paymentsandboxschema.Inventory(paymentsandboxschema.Component(c))
		if x != nil {
			return stage("inventory")
		}
		for _, table := range inventory.Tables {
			insert, updates := paymentsandboxschema.Permissions(paymentsandboxschema.Component(c), table.Name)
			qualified := schema + "." + ident(table.Name)
			if insert {
				if _, err = a.Exec(ctx, "GRANT INSERT ON "+qualified+" TO "+role); err != nil {
					return stage("insert_grant")
				}
			}
			if len(updates) > 0 {
				cols := make([]string, len(updates))
				for i, v := range updates {
					cols[i] = ident(v)
				}
				if _, err = a.Exec(ctx, "GRANT UPDATE("+strings.Join(cols, ",")+") ON "+qualified+" TO "+role); err != nil {
					return stage("update_grant")
				}
			}
		}
	}
	return nil
}

func insert(ctx context.Context, p *pgxpool.Pool, table string, fields map[string]any) error {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	cols := make([]string, len(keys))
	params := make([]string, len(keys))
	args := make([]any, len(keys))
	for i, k := range keys {
		cols[i] = ident(k)
		params[i] = fmt.Sprintf("$%d", i+1)
		args[i] = fields[k]
	}
	_, err := p.Exec(ctx, "INSERT INTO "+ident(table)+"("+strings.Join(cols, ",")+") VALUES("+strings.Join(params, ",")+")", args...)
	return err
}

func (e *Environment) installComponent(ctx context.Context, c string) error {
	p, err := e.OpenPool(ctx, c+"_owner")
	if err != nil {
		return err
	}
	defer p.Close()
	sql, err := paymentsandboxschema.SQL(paymentsandboxschema.Component(c))
	if err != nil {
		return stage("sql")
	}
	if _, err = p.Exec(ctx, sql); err != nil {
		return stage(c + "_sql")
	}
	marker := map[string]any{"environment_id": e.Protocol.ID, "merchant_id": e.Protocol.MerchantID, "database_oid": e.dbOID, "state_directory_id": e.stateDirectoryID, "callback_url": e.ReceiverOrigin + "/api/payments/sandbox/notifications", "protocol_version": 1}
	if c == "mock" {
		marker["payee_label"] = e.Protocol.PayeeLabel
		marker["nonce_digest"] = digest("moni-mock-state-v1", e.mockNonce)
		marker["api_token_digest"] = digest("moni-mock-api-token-v1", e.api)
		marker["control_token_digest"] = digest("moni-mock-control-token-v1", e.control)
		marker["api_origin"] = e.APIOrigin
		marker["control_origin"] = e.ControlOrigin
		marker["app_projector"] = e.Roles["app_projector"]
		for _, v := range []string{"app", "mock", "receiver"} {
			marker[v+"_schema_name"] = e.Schemas[v]
			marker[v+"_schema_oid"] = e.schemaOIDs[v]
			marker[v+"_owner"] = e.Roles[v+"_owner"]
			marker[v+"_runtime"] = e.Roles[v+"_runtime"]
		}
	} else {
		marker["nonce_digest"] = digest("moni-receiver-state-v1", e.receiverNonce)
		marker["receiver_schema_name"] = e.Schemas[c]
		marker["receiver_schema_oid"] = e.schemaOIDs[c]
		marker["receiver_owner"] = e.Roles[c+"_owner"]
		marker["receiver_runtime"] = e.Roles[c+"_runtime"]
	}
	if insert(ctx, p, c+"_environment", marker) != nil {
		return stage(c + "_marker")
	}
	hash, err := paymentsandboxschema.Digest(paymentsandboxschema.Component(c))
	if err != nil {
		return stage("sql_digest")
	}
	if insert(ctx, p, c+"_schema_migrations", map[string]any{"version": 1, "sha256": hash}) != nil {
		return stage(c + "_migration_marker")
	}
	if insert(ctx, p, c+"_signing_keys", map[string]any{"key_id": "qa-key", "key_digest": digest("moni-mock-signing-key-v1", e.key), "not_before": "2020-01-01T00:00:00.000000Z", "not_after": "2030-01-01T00:00:00.000000Z"}) != nil {
		return stage(c + "_key_marker")
	}
	return nil
}
