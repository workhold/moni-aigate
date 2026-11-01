package maintenancetrust

import (
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type predecessorKind uint8

const (
	predecessorText predecessorKind = iota + 1
	predecessorBytes
	predecessorBool
	predecessorOID
	predecessorInt4
	predecessorInt8
	predecessorTimestamp
)

type predecessorColumn struct {
	name     string
	kind     predecessorKind
	min, max int
}
type predecessorTable struct {
	name    string
	columns []predecessorColumn
	keys    bool
}

func predecessorTextColumn(name string, min, max int) predecessorColumn {
	return predecessorColumn{name: name, kind: predecessorText, min: min, max: max}
}
func predecessorFixedColumn(name string, kind predecessorKind) predecessorColumn {
	return predecessorColumn{name: name, kind: kind}
}

// Only these compiled column lists reach the private projection builder. No
// caller can provide a table, column, predicate, or SQL callback. Qualification
// is returned, never filtered; every unsafe scalar is withheld by CASE.
func predecessorTables() []predecessorTable {
	t := predecessorTextColumn
	f := predecessorFixedColumn
	return []predecessorTable{
		{"payment_sandbox_environment", []predecessorColumn{
			f("singleton", predecessorBool), t("id", 36, 36), t("merchant_id", 1, 100), t("payee_label", 1, 100),
			f("database_oid", predecessorOID), f("schema_oid", predecessorOID), t("nonce_digest", 64, 64),
			t("app_role", 1, 63), t("worker_role", 1, 63), t("projector_role", 1, 63),
			f("protocol_version", predecessorInt4), f("created_at", predecessorTimestamp),
		}, false},
		{"payment_runtime_attestations", []predecessorColumn{
			f("singleton", predecessorBool), t("environment_id", 36, 36), f("database_oid", predecessorOID),
			f("schema_oid", predecessorOID), f("schema_owner_oid", predecessorOID), f("app_role_oid", predecessorOID),
			f("worker_role_oid", predecessorOID), f("projector_role_oid", predecessorOID), t("app_nonce_digest", 64, 64),
			t("worker_nonce_digest", 64, 64), f("proof_version", predecessorInt4),
			{name: "proof_canonical", kind: predecessorBytes, min: 1, max: 65536}, t("proof_sha256", 64, 64),
			f("created_at", predecessorTimestamp),
		}, false},
		{"mock_environment", []predecessorColumn{
			f("singleton", predecessorBool), t("environment_id", 36, 36), t("state_directory_id", 36, 36), f("database_oid", predecessorOID),
			t("app_schema_name", 1, 63), f("app_schema_oid", predecessorOID), t("app_owner", 1, 63), t("app_runtime", 1, 63), t("app_projector", 1, 63),
			t("mock_schema_name", 1, 63), f("mock_schema_oid", predecessorOID), t("mock_owner", 1, 63), t("mock_runtime", 1, 63),
			t("receiver_schema_name", 1, 63), f("receiver_schema_oid", predecessorOID), t("receiver_owner", 1, 63), t("receiver_runtime", 1, 63),
			t("merchant_id", 1, 100), t("payee_label", 1, 100), t("nonce_digest", 64, 64), t("api_token_digest", 64, 64),
			t("control_token_digest", 64, 64), t("api_origin", 1, 128), t("control_origin", 1, 128), t("callback_url", 1, 128),
			f("protocol_version", predecessorInt4), f("created_at", predecessorTimestamp),
		}, false},
		{"receiver_environment", []predecessorColumn{
			f("singleton", predecessorBool), t("environment_id", 36, 36), t("merchant_id", 1, 100), f("database_oid", predecessorOID),
			t("receiver_schema_name", 1, 63), f("receiver_schema_oid", predecessorOID), t("receiver_owner", 1, 63), t("receiver_runtime", 1, 63),
			t("state_directory_id", 36, 36), t("nonce_digest", 64, 64), t("callback_url", 1, 128),
			f("protocol_version", predecessorInt4), f("created_at", predecessorTimestamp),
		}, false},
		{"mock_schema_migrations", []predecessorColumn{f("version", predecessorInt4), t("sha256", 64, 64), f("applied_at", predecessorTimestamp)}, false},
		{"receiver_schema_migrations", []predecessorColumn{f("version", predecessorInt4), t("sha256", 64, 64), f("applied_at", predecessorTimestamp)}, false},
		{"mock_signing_keys", []predecessorColumn{t("key_id", 1, 32), t("key_digest", 64, 64), f("not_before", predecessorTimestamp), f("not_after", predecessorTimestamp), f("created_at", predecessorTimestamp)}, true},
		{"receiver_signing_keys", []predecessorColumn{t("key_id", 1, 32), t("key_digest", 64, 64), f("not_before", predecessorTimestamp), f("not_after", predecessorTimestamp)}, true},
	}
}

var predecessorPolicyTable = predecessorTable{"payment_policy", []predecessorColumn{
	{name: "singleton", kind: predecessorBool}, {name: "enabled", kind: predecessorBool}, {name: "revision", kind: predecessorInt8},
}, false}

func predecessorProjectionSQL(schema string, table predecessorTable) string {
	conditions := make([]string, 0, len(table.columns))
	inner := make([]string, 0, len(table.columns))
	outer := []string{"q.qualified"}
	for _, column := range table.columns {
		name := pgx.Identifier{column.name}.Sanitize()
		value := "s." + name
		typeName := ""
		switch column.kind {
		case predecessorText:
			value += "::pg_catalog.text"
			typeName = "text"
		case predecessorBytes:
			typeName = "bytea"
		case predecessorBool:
			typeName = "bool"
		case predecessorOID:
			typeName = "oid"
		case predecessorInt4:
			typeName = "int4"
		case predecessorInt8:
			typeName = "int8"
		case predecessorTimestamp:
			typeName = "timestamptz"
		}
		condition := value + " IS NOT NULL"
		if column.kind == predecessorText || column.kind == predecessorBytes {
			condition += " AND pg_catalog.octet_length(" + value + ") OPERATOR(pg_catalog.>=) " + strconv.Itoa(column.min) + "::pg_catalog.int4" +
				" AND pg_catalog.octet_length(" + value + ") OPERATOR(pg_catalog.<=) " + strconv.Itoa(column.max) + "::pg_catalog.int4"
		}
		conditions = append(conditions, "("+condition+")")
		inner = append(inner, value+" AS "+name)
		outer = append(outer, "CASE WHEN q.qualified THEN q."+name+" ELSE NULL::pg_catalog."+typeName+" END AS "+name)
	}
	inner = append(inner, "COALESCE("+strings.Join(conditions, " AND ")+",false) AS qualified")
	query := "SELECT " + strings.Join(outer, ",") + " FROM (SELECT " + strings.Join(inner, ",") + " FROM ONLY " + pgx.Identifier{schema, table.name}.Sanitize() + " s) q"
	if table.keys {
		return query + " ORDER BY q.\"key_id\" COLLATE pg_catalog.\"C\" LIMIT 129"
	}
	return query + " LIMIT 2"
}

var predecessorHistoryTables = [...]string{
	"payment_sandbox_environment", "payment_runtime_attestations", "payment_skus", "payment_sku_versions", "payment_sku_changes", "payment_orders", "payment_cancel_intents",
	"payment_command_outbox", "payment_command_observations", "payment_query_attempts", "payment_inbox", "payment_event_conflicts", "payment_verified_queries", "payment_verified_evidence",
	"payment_evidence_processing", "payment_provider_revisions", "payment_transaction_identities", "payment_order_capture_selections", "payment_captures", "payment_effects", "payment_review_facts",
}

var predecessorBypasses = [...]struct{ table, predicate string }{
	{"commerce_commands", `pg_catalog.left(kind,8::pg_catalog.int4) OPERATOR(pg_catalog.=) 'payment.'::pg_catalog.text`},
	{"commerce_operation_receipts", `pg_catalog.left(kind,8::pg_catalog.int4) OPERATOR(pg_catalog.=) 'payment.'::pg_catalog.text`},
	{"audit_events", `pg_catalog.left(action,8::pg_catalog.int4) OPERATOR(pg_catalog.=) 'payment.'::pg_catalog.text`},
	{"funding_receipts", `source_kind OPERATOR(pg_catalog.=) 'sandbox_capture'::pg_catalog.text`},
	{"journals", `kind OPERATOR(pg_catalog.=) 'sandbox_capture'::pg_catalog.text`},
	{"ledger_accounts", `kind OPERATOR(pg_catalog.=) 'SANDBOX_FUNDING_CLEARING'::pg_catalog.text`},
}

const predecessorQueryConflictsSQL = `SELECT child.attnotnull AND EXISTS(
 SELECT 1 FROM pg_catalog.pg_constraint f
 WHERE f.contype OPERATOR(pg_catalog.=) 'f'::pg_catalog."char"
 AND f.conrelid OPERATOR(pg_catalog.=) c.oid AND f.confrelid OPERATOR(pg_catalog.=) p.oid
 AND f.convalidated AND NOT f.condeferrable AND NOT f.condeferred
 AND f.conkey OPERATOR(pg_catalog.=) ARRAY[child.attnum]::pg_catalog.int2[]
 AND f.confkey OPERATOR(pg_catalog.=) ARRAY[parent.attnum]::pg_catalog.int2[])
 FROM pg_catalog.pg_namespace n
 JOIN pg_catalog.pg_class c ON c.relnamespace OPERATOR(pg_catalog.=) n.oid AND c.relname OPERATOR(pg_catalog.=) 'payment_query_conflicts'::pg_catalog.name
 JOIN pg_catalog.pg_class p ON p.relnamespace OPERATOR(pg_catalog.=) n.oid AND p.relname OPERATOR(pg_catalog.=) 'payment_verified_queries'::pg_catalog.name
 JOIN pg_catalog.pg_attribute child ON child.attrelid OPERATOR(pg_catalog.=) c.oid AND child.attname OPERATOR(pg_catalog.=) 'verified_query_id'::pg_catalog.name
 AND child.attnum OPERATOR(pg_catalog.>) 0::pg_catalog.int2 AND NOT child.attisdropped
 JOIN pg_catalog.pg_attribute parent ON parent.attrelid OPERATOR(pg_catalog.=) p.oid AND parent.attname OPERATOR(pg_catalog.=) 'id'::pg_catalog.name
 AND parent.attnum OPERATOR(pg_catalog.>) 0::pg_catalog.int2 AND NOT parent.attisdropped
 WHERE n.nspname OPERATOR(pg_catalog.=) $1::pg_catalog.name`
