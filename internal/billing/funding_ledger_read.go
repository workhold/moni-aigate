package billing

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/migrations"
)

var (
	fundingLedgerReadInput     = errors.New("funding_ledger_read_input")
	fundingLedgerReadResource  = errors.New("funding_ledger_read_resource")
	fundingLedgerReadCanceled  = errors.New("funding_ledger_read_canceled")
	fundingLedgerReadExecution = errors.New("funding_ledger_read_execution")
)

type fundingLedgerJournalFacts struct {
	ID, TenantID, OperationID, Kind, Reference, Actor, Currency, CreatedTXID string
	CreatedAt                                                                time.Time
}
type fundingLedgerSnapshot struct {
	rows     fundinglotverify.LedgerRows
	journals []fundingLedgerJournalFacts
	check    fundinglotverify.LedgerRowsCheck
}
type fundingLedgerIdentity struct {
	tx                                          pgx.Tx
	conn                                        *pgx.Conn
	pgconn                                      *pgconn.PgConn
	database, schema, role, session, xid        string
	isolation, access, authenticated            string
	databaseOID, schemaOID, roleOID, backendPID uint32
}

const fundingLedgerIdentitySQL = `SELECT pg_catalog.current_database()::pg_catalog.text,d.oid,
 pg_catalog.current_schema()::pg_catalog.text,n.oid,pg_catalog.pg_backend_pid(),
 pg_catalog.pg_current_xact_id()::pg_catalog.text,current_user::pg_catalog.text,
 session_user::pg_catalog.text,r.oid,r.rolcanlogin,
 pg_catalog.current_setting('transaction_isolation'),pg_catalog.current_setting('transaction_read_only')
 FROM pg_catalog.pg_database d JOIN pg_catalog.pg_namespace n
 ON n.nspname OPERATOR(pg_catalog.=) pg_catalog.current_schema()
 JOIN pg_catalog.pg_roles r ON r.rolname OPERATOR(pg_catalog.=) current_user
 WHERE d.datname OPERATOR(pg_catalog.=) pg_catalog.current_database()`

func fundingLedgerFailure(ctx context.Context) error {
	if ctx.Err() != nil {
		return fundingLedgerReadCanceled
	}
	return fundingLedgerReadExecution
}
func fundingLedgerTxValid(tx pgx.Tx) bool {
	if tx == nil {
		return false
	}
	v := reflect.ValueOf(tx)
	if !v.Type().Comparable() {
		return false
	}
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if v.IsNil() {
			return false
		}
	}
	return tx.Conn() != nil && tx.Conn().PgConn() != nil && tx.Conn().PgConn().TxStatus() == 'T'
}
func fundingLedgerIdentityTx(ctx context.Context, tx pgx.Tx) (fundingLedgerIdentity, error) {
	if !fundingLedgerTxValid(tx) {
		return fundingLedgerIdentity{}, fundingLedgerReadInput
	}
	conn := tx.Conn()
	id := fundingLedgerIdentity{tx: tx, conn: conn, pgconn: conn.PgConn(), authenticated: conn.Config().User}
	var login bool
	if err := tx.QueryRow(ctx, fundingLedgerIdentitySQL).Scan(&id.database, &id.databaseOID, &id.schema, &id.schemaOID,
		&id.backendPID, &id.xid, &id.role, &id.session, &id.roleOID, &login, &id.isolation, &id.access); err != nil {
		return fundingLedgerIdentity{}, fundingLedgerFailure(ctx)
	}
	xid, err := strconv.ParseUint(id.xid, 10, 64)
	if !login || id.role == "" || id.role != id.session || id.session != id.authenticated ||
		id.isolation != "repeatable read" || id.access != "on" && id.access != "off" ||
		id.databaseOID == 0 || id.schemaOID == 0 || id.roleOID == 0 || id.backendPID == 0 || id.backendPID != conn.PgConn().PID() ||
		id.schema == "" || id.schema == "pg_catalog" || id.schema == "information_schema" || strings.HasPrefix(id.schema, "pg_") ||
		err != nil || xid == 0 || strconv.FormatUint(xid, 10) != id.xid {
		return fundingLedgerIdentity{}, fundingLedgerReadInput
	}
	return id, nil
}

type fundingLedgerColumn struct {
	name    string
	typeOID uint32
	notNull bool
}
type fundingLedgerTable struct {
	name    string
	columns []fundingLedgerColumn
}

const fundingLedgerRelationSQL = `SELECT c.oid,c.relowner,c.relkind::pg_catalog.text,
 c.relpersistence::pg_catalog.text,c.relam,c.relrowsecurity,c.relforcerowsecurity,c.relispartition,
 EXISTS(SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhrelid OPERATOR(pg_catalog.=) c.oid OR i.inhparent OPERATOR(pg_catalog.=) c.oid)
 FROM pg_catalog.pg_class c WHERE c.relnamespace OPERATOR(pg_catalog.=) $1::pg_catalog.oid
 AND c.relname OPERATOR(pg_catalog.=) $2::pg_catalog.name`

const fundingLedgerColumnsSQL = `SELECT attnum,attname::pg_catalog.text,atttypid,attnotnull,
 attisdropped,attgenerated::pg_catalog.text,attidentity::pg_catalog.text FROM pg_catalog.pg_attribute
 WHERE attrelid OPERATOR(pg_catalog.=) $1::pg_catalog.oid AND attnum OPERATOR(pg_catalog.=) ANY($2::pg_catalog.int2[])
 ORDER BY attnum USING OPERATOR(pg_catalog.<)`

const fundingLedgerPrimarySQL = `SELECT k.conkey,k.condeferrable,k.condeferred,k.convalidated,
 i.indisprimary,i.indisunique,i.indisvalid,i.indisready,i.indislive,i.indimmediate,
 i.indnatts,i.indnkeyatts,i.indkey::pg_catalog.text,
 i.indexprs IS NULL,i.indpred IS NULL,ic.relkind::pg_catalog.text,ic.relpersistence::pg_catalog.text,
 ic.relnamespace,ic.relowner,am.amname::pg_catalog.text,ns.nspname::pg_catalog.text,op.opcname::pg_catalog.text,op.opcintype
 FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_index i ON i.indexrelid OPERATOR(pg_catalog.=) k.conindid
 JOIN pg_catalog.pg_class ic ON ic.oid OPERATOR(pg_catalog.=) i.indexrelid
 JOIN pg_catalog.pg_am am ON am.oid OPERATOR(pg_catalog.=) ic.relam
 JOIN pg_catalog.pg_opclass op ON op.oid OPERATOR(pg_catalog.=) i.indclass[0]
 JOIN pg_catalog.pg_namespace ns ON ns.oid OPERATOR(pg_catalog.=) op.opcnamespace
 WHERE k.conrelid OPERATOR(pg_catalog.=) $1::pg_catalog.oid
 AND i.indrelid OPERATOR(pg_catalog.=) k.conrelid
 AND k.connamespace OPERATOR(pg_catalog.=) $2::pg_catalog.oid
 AND k.contype OPERATOR(pg_catalog.=) 'p'::pg_catalog."char"`

func fundingLedgerPrimaryTx(ctx context.Context, tx pgx.Tx, tableOID, schemaOID, ownerOID uint32) error {
	rows, err := tx.Query(ctx, fundingLedgerPrimarySQL, tableOID, schemaOID)
	if err != nil {
		return fundingLedgerFailure(ctx)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
		var keys []int16
		var deferrable, deferred, validated, primary, unique, valid, ready, live, immediate, noExpression, noPredicate bool
		var attributes, keyAttributes int16
		var key, kind, persistence, method, namespace, opclass string
		var indexSchema, indexOwner, opType uint32
		if err := rows.Scan(&keys, &deferrable, &deferred, &validated, &primary, &unique, &valid, &ready, &live, &immediate,
			&attributes, &keyAttributes, &key, &noExpression, &noPredicate, &kind, &persistence, &indexSchema, &indexOwner,
			&method, &namespace, &opclass, &opType); err != nil {
			return fundingLedgerFailure(ctx)
		}
		if n != 1 || len(keys) != 1 || keys[0] != 1 || deferrable || deferred || !validated ||
			!primary || !unique || !valid || !ready || !live || !immediate || attributes != 1 || keyAttributes != 1 || key != "1" ||
			!noExpression || !noPredicate || kind != "i" || persistence != "p" || indexSchema != schemaOID || indexOwner != ownerOID ||
			method != "btree" || namespace != "pg_catalog" || opclass != "text_ops" || opType != 25 {
			return fundingLedgerReadExecution
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return fundingLedgerFailure(ctx)
	}
	if n != 1 {
		return fundingLedgerReadExecution
	}
	return nil
}

func fundingLedgerShapeTx(ctx context.Context, tx pgx.Tx, id fundingLedgerIdentity) error {
	tables := []fundingLedgerTable{
		{"schema_migrations", []fundingLedgerColumn{{"name", 25, true}, {"checksum", 25, true}, {"applied_at", 1184, true}}},
		{"ledger_accounts", []fundingLedgerColumn{{"id", 25, true}, {"tenant_id", 25, false}, {"kind", 25, true}, {"balance", 20, true}}},
		{"journals", []fundingLedgerColumn{{"id", 25, true}, {"tenant_id", 25, true}, {"operation_id", 25, true}, {"kind", 25, true}, {"reference", 25, true}, {"actor", 25, true}, {"currency", 25, true}, {"created_txid", 20, true}, {"created_at", 1184, true}}},
		{"journal_lines", []fundingLedgerColumn{{"id", 25, true}, {"journal_id", 25, true}, {"account_id", 25, true}, {"signed_delta", 20, true}}},
	}
	var commonOwner uint32
	for _, table := range tables {
		var oid, owner, am uint32
		var kind, persistence string
		var rls, forced, partition, inherited bool
		if err := tx.QueryRow(ctx, fundingLedgerRelationSQL, id.schemaOID, table.name).Scan(&oid, &owner, &kind, &persistence, &am, &rls, &forced, &partition, &inherited); err != nil {
			return fundingLedgerFailure(ctx)
		}
		if oid == 0 || owner == 0 || kind != "r" || persistence != "p" || am != 2 || rls || forced || partition || inherited || commonOwner != 0 && owner != commonOwner {
			return fundingLedgerReadExecution
		}
		commonOwner = owner
		positions := make([]int16, len(table.columns))
		for i := range positions {
			positions[i] = int16(i + 1)
		}
		rows, err := tx.Query(ctx, fundingLedgerColumnsSQL, oid, positions)
		if err != nil {
			return fundingLedgerFailure(ctx)
		}
		position := 0
		for rows.Next() {
			var number int16
			var name, generated, identity string
			var typeOID uint32
			var notNull, dropped bool
			if err := rows.Scan(&number, &name, &typeOID, &notNull, &dropped, &generated, &identity); err != nil {
				rows.Close()
				return fundingLedgerFailure(ctx)
			}
			if position >= len(table.columns) || number != int16(position+1) || name != table.columns[position].name ||
				typeOID != table.columns[position].typeOID || notNull != table.columns[position].notNull || dropped || generated != "" || identity != "" {
				rows.Close()
				return fundingLedgerReadExecution
			}
			position++
		}
		rows.Close()
		if rows.Err() != nil {
			return fundingLedgerFailure(ctx)
		}
		if position != len(table.columns) {
			return fundingLedgerReadExecution
		}
		if err := fundingLedgerPrimaryTx(ctx, tx, oid, id.schemaOID, owner); err != nil {
			return err
		}
		if table.name == "schema_migrations" {
			var resolved uint32
			if err := tx.QueryRow(ctx, `SELECT pg_catalog.to_regclass('schema_migrations')::pg_catalog.oid`).Scan(&resolved); err != nil {
				return fundingLedgerFailure(ctx)
			}
			if resolved != oid {
				return fundingLedgerReadExecution
			}
		}
	}
	return nil
}

// The unchanged history helper contains two unqualified builtin calls. Reject
// path shadows before invoking it; resolving regprocedure never calls a target.
const fundingLedgerHistoryFunctionsSQL = `SELECT
 pg_catalog.to_regprocedure('current_setting(pg_catalog.text)')::pg_catalog.oid
 OPERATOR(pg_catalog.=) pg_catalog.to_regprocedure('pg_catalog.current_setting(pg_catalog.text)')::pg_catalog.oid,
 pg_catalog.to_regprocedure('to_regclass(pg_catalog.text)')::pg_catalog.oid
 OPERATOR(pg_catalog.=) pg_catalog.to_regprocedure('pg_catalog.to_regclass(pg_catalog.text)')::pg_catalog.oid,
 NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n
 ON n.oid OPERATOR(pg_catalog.=) p.pronamespace
 WHERE n.nspname OPERATOR(pg_catalog.=) ANY(pg_catalog.current_schemas(true))
 AND n.nspname OPERATOR(pg_catalog.<>) 'pg_catalog'::pg_catalog.name
 AND p.proname OPERATOR(pg_catalog.=) ANY(ARRAY['current_setting','to_regclass']::pg_catalog.name[]))`

const fundingLedgerCursorSQL = ` WHERE ($1::pg_catalog.bool OR
 id COLLATE pg_catalog."C" OPERATOR(pg_catalog.>) $2::pg_catalog.text COLLATE pg_catalog."C")
 ORDER BY id COLLATE pg_catalog."C" USING OPERATOR(pg_catalog.<) LIMIT 128`

func fundingLedgerPageSQL(schema, table string) string {
	var projection string
	switch table {
	case "ledger_accounts":
		projection = `id,tenant_id,kind,balance::pg_catalog.text`
	case "journals":
		projection = `id,tenant_id,operation_id,kind,reference,actor,currency,created_txid::pg_catalog.text,created_at`
	case "journal_lines":
		projection = `id,journal_id,account_id,signed_delta::pg_catalog.text`
	default:
		return ""
	}
	return "SELECT " + projection + " FROM " + pgx.Identifier{schema, table}.Sanitize() + fundingLedgerCursorSQL
}

type fundingLedgerBudget struct{ rows, bytes int }

func (b *fundingLedgerBudget) add(fields ...string) error {
	if b.rows >= 100000 {
		return fundingLedgerReadResource
	}
	for _, field := range fields {
		if len(field) > (16<<20)-b.bytes {
			return fundingLedgerReadResource
		}
		b.bytes += len(field)
		if !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
			return fundingLedgerReadExecution
		}
	}
	b.rows++
	return nil
}

func fundingLedgerPages(ctx context.Context, tx pgx.Tx, query string, scan func(pgx.Rows) (string, error)) error {
	hasCursor, cursor := false, ""
	for {
		rows, err := tx.Query(ctx, query, !hasCursor, cursor)
		if err != nil {
			return fundingLedgerFailure(ctx)
		}
		count, last := 0, cursor
		for rows.Next() {
			key, err := scan(rows)
			if err != nil {
				rows.Close()
				return err
			}
			if count >= 128 || (hasCursor || count > 0) && key <= last {
				rows.Close()
				return fundingLedgerReadExecution
			}
			last = key
			count++
		}
		rows.Close()
		if rows.Err() != nil {
			return fundingLedgerFailure(ctx)
		}
		if count < 128 {
			return nil
		}
		hasCursor, cursor = true, last
	}
}

// This private reader preserves the caller's RR and returns raw facts, not a
// full-source report or proof. The future composer must authorize its own tx.
func readFundingLedgerSnapshotTx(ctx context.Context, tx pgx.Tx) (fundingLedgerSnapshot, error) {
	if ctx == nil {
		return fundingLedgerSnapshot{}, fundingLedgerReadInput
	}
	if ctx.Err() != nil {
		return fundingLedgerSnapshot{}, fundingLedgerReadCanceled
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	id, err := fundingLedgerIdentityTx(ctx, tx)
	if err != nil {
		return fundingLedgerSnapshot{}, err
	}
	if err = fundingLedgerShapeTx(ctx, tx, id); err != nil {
		return fundingLedgerSnapshot{}, err
	}
	var currentSettingBuiltin, toRegclassBuiltin, noOverloads bool
	if err = tx.QueryRow(ctx, fundingLedgerHistoryFunctionsSQL).Scan(&currentSettingBuiltin, &toRegclassBuiltin, &noOverloads); err != nil || !currentSettingBuiltin || !toRegclassBuiltin || !noOverloads {
		return fundingLedgerSnapshot{}, fundingLedgerFailure(ctx)
	}
	if err = migrations.VerifySnapshotTx(ctx, tx); err != nil {
		return fundingLedgerSnapshot{}, fundingLedgerFailure(ctx)
	}
	var out fundingLedgerSnapshot
	var budget fundingLedgerBudget
	err = fundingLedgerPages(ctx, tx, fundingLedgerPageSQL(id.schema, "ledger_accounts"), func(rows pgx.Rows) (string, error) {
		var row fundinglotverify.LedgerAccountRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.Kind, &row.Balance); err != nil {
			return "", fundingLedgerFailure(ctx)
		}
		fields := []string{row.ID, row.Kind, row.Balance}
		if row.TenantID != nil {
			fields = append(fields, *row.TenantID)
		}
		if err := budget.add(fields...); err != nil {
			return "", err
		}
		out.rows.Accounts = append(out.rows.Accounts, row)
		return row.ID, nil
	})
	if err != nil {
		return fundingLedgerSnapshot{}, err
	}
	err = fundingLedgerPages(ctx, tx, fundingLedgerPageSQL(id.schema, "journals"), func(rows pgx.Rows) (string, error) {
		var row fundingLedgerJournalFacts
		if err := rows.Scan(&row.ID, &row.TenantID, &row.OperationID, &row.Kind, &row.Reference, &row.Actor, &row.Currency, &row.CreatedTXID, &row.CreatedAt); err != nil {
			return "", fundingLedgerFailure(ctx)
		}
		if err := budget.add(row.ID, row.TenantID, row.OperationID, row.Kind, row.Reference, row.Actor, row.Currency, row.CreatedTXID); err != nil {
			return "", err
		}
		xid, err := strconv.ParseInt(row.CreatedTXID, 10, 64)
		row.CreatedAt = row.CreatedAt.UTC()
		if err != nil || xid <= 0 || strconv.FormatInt(xid, 10) != row.CreatedTXID || row.CreatedAt.IsZero() ||
			row.CreatedAt.Year() < 1 || row.CreatedAt.Year() > 9999 || row.CreatedAt.Nanosecond()%1000 != 0 {
			return "", fundingLedgerReadExecution
		}
		out.journals = append(out.journals, row)
		out.rows.Journals = append(out.rows.Journals, fundinglotverify.LedgerJournalRow{ID: row.ID, TenantID: row.TenantID, OperationID: row.OperationID, Currency: row.Currency})
		return row.ID, nil
	})
	if err != nil {
		return fundingLedgerSnapshot{}, err
	}
	err = fundingLedgerPages(ctx, tx, fundingLedgerPageSQL(id.schema, "journal_lines"), func(rows pgx.Rows) (string, error) {
		var row fundinglotverify.LedgerLineRow
		if err := rows.Scan(&row.ID, &row.JournalID, &row.AccountID, &row.SignedDelta); err != nil {
			return "", fundingLedgerFailure(ctx)
		}
		if err := budget.add(row.ID, row.JournalID, row.AccountID, row.SignedDelta); err != nil {
			return "", err
		}
		out.rows.Lines = append(out.rows.Lines, row)
		return row.ID, nil
	})
	if err != nil {
		return fundingLedgerSnapshot{}, err
	}
	out.check, err = fundinglotverify.CheckLedgerRows(ctx, out.rows)
	if err != nil {
		switch err {
		case fundinglotverify.ErrLedgerRowsResource:
			return fundingLedgerSnapshot{}, fundingLedgerReadResource
		case fundinglotverify.ErrLedgerRowsCanceled:
			return fundingLedgerSnapshot{}, fundingLedgerReadCanceled
		default:
			return fundingLedgerSnapshot{}, fundingLedgerReadExecution
		}
	}
	after, err := fundingLedgerIdentityTx(ctx, tx)
	if err != nil {
		return fundingLedgerSnapshot{}, err
	}
	if after != id {
		return fundingLedgerSnapshot{}, fundingLedgerReadInput
	}
	if ctx.Err() != nil {
		return fundingLedgerSnapshot{}, fundingLedgerReadCanceled
	}
	return out, nil
}
