// Package funding019 verifies the explicitly historical 001–019 financial
// fixture. It is test/QA support, never a serving-process version fallback.
package funding019

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/migrations"
)

const Through = "019_operations_alerts.sql"

var (
	ErrBaseline = errors.New("funding019 historical baseline invalid")
	ErrInput    = errors.New("funding019 input invalid")
	ErrVerify   = errors.New("funding019 verification unavailable")
)

type Check struct {
	Balanced                                          bool
	MissingReceipts, OrphanReceipts, SourceMismatches int64
	JournalMismatches, OutboxMismatches               int64
	RedemptionMismatches, BatchCountMismatches        int64
	ClearingMismatches, AccountMismatches             int64
	UnbalancedJournals                                int64
}

// Verify confirms the exact historical manifest, catalog boundary and all A
// funding/ledger directions in one RR READ ONLY snapshot. It returns a result
// only after COMMIT confirmation. It never installs, repairs or locks sources.
func Verify(ctx context.Context, pool *pgxpool.Pool, tenant string) (Check, error) {
	if ctx == nil || pool == nil || !validScope(tenant) {
		return Check{}, ErrInput
	}
	if ctx.Err() != nil {
		return Check{}, ErrVerify
	}
	expected, body, err := baseline()
	if err != nil {
		return Check{}, ErrBaseline
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Check{}, ErrVerify
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var schema string
	if err = tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil || schema == "" || schema == "pg_catalog" || schema == "information_schema" || strings.HasPrefix(schema, "pg_temp_") {
		return Check{}, ErrBaseline
	}
	qualify := func(sql string) string {
		return strings.ReplaceAll(sql, "@", pgx.Identifier{schema}.Sanitize()+".")
	}
	if err = verifyManifest(ctx, tx, qualify(`SELECT name,checksum FROM @schema_migrations ORDER BY name`), expected); err != nil {
		return Check{}, err
	}
	if err = verifyCatalog(ctx, tx, schema, body); err != nil {
		return Check{}, err
	}
	var out Check
	err = tx.QueryRow(ctx, qualify(fundingSQL), tenant).Scan(&out.MissingReceipts, &out.OrphanReceipts, &out.SourceMismatches, &out.JournalMismatches, &out.OutboxMismatches, &out.RedemptionMismatches, &out.BatchCountMismatches, &out.ClearingMismatches, &out.AccountMismatches, &out.UnbalancedJournals)
	if err != nil {
		return Check{}, ErrVerify
	}
	out.Balanced = out.MissingReceipts == 0 && out.OrphanReceipts == 0 && out.SourceMismatches == 0 && out.JournalMismatches == 0 && out.OutboxMismatches == 0 && out.RedemptionMismatches == 0 && out.BatchCountMismatches == 0 && out.ClearingMismatches == 0 && out.AccountMismatches == 0 && out.UnbalancedJournals == 0
	if tx.Commit(ctx) != nil {
		return Check{}, ErrVerify
	}
	return out, nil
}

func validScope(tenant string) bool {
	if tenant == "" {
		return true
	}
	id, err := uuid.Parse(tenant)
	return err == nil && id != uuid.Nil && id.String() == tenant
}

func baseline() (map[string]string, string, error) {
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return nil, "", err
	}
	sort.Strings(names)
	expected := make(map[string]string, 19)
	var original string
	for _, name := range names {
		if name > Through {
			continue
		}
		if len(expected) >= 19 || !strings.HasPrefix(name, fmt.Sprintf("%03d_", len(expected)+1)) {
			return nil, "", ErrBaseline
		}
		body, e := migrations.Files.ReadFile(name)
		if e != nil {
			return nil, "", e
		}
		expected[name] = fmt.Sprintf("%x", sha256.Sum256(body))
		if name == "017_funding_vouchers.sql" {
			_, tail, found := strings.Cut(string(body), "CREATE FUNCTION funding_journal_valid(journal_ref TEXT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$")
			var closed bool
			original, _, closed = strings.Cut(tail, "$$;")
			if !found || !closed || original == "" {
				return nil, "", ErrBaseline
			}
		}
	}
	if len(expected) != 19 || expected[Through] == "" || original == "" {
		return nil, "", ErrBaseline
	}
	return expected, original, nil
}

func verifyManifest(ctx context.Context, tx pgx.Tx, sql string, expected map[string]string) error {
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return ErrBaseline
	}
	defer rows.Close()
	seen := make(map[string]bool, len(expected))
	for rows.Next() {
		var name, sum string
		if rows.Scan(&name, &sum) != nil || expected[name] == "" || expected[name] != sum || seen[name] {
			return ErrBaseline
		}
		seen[name] = true
	}
	if rows.Err() != nil || len(seen) != len(expected) {
		return ErrBaseline
	}
	return nil
}

func verifyCatalog(ctx context.Context, tx pgx.Tx, schema, expectedBody string) error {
	var clean bool
	if tx.QueryRow(ctx, `SELECT
 NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND left(c.relname,8)='payment_')
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND left(p.proname,8)='payment_')`, schema).Scan(&clean) != nil || !clean {
		return ErrBaseline
	}
	rows, err := tx.Query(ctx, `SELECT p.prosrc,
 p.proargtypes='25'::pg_catalog.oidvector AND p.prorettype=16 AND p.proargnames=ARRAY['journal_ref']::text[]
 AND l.lanname='sql' AND p.provolatile='s' AND p.prokind='f' AND NOT p.prosecdef AND NOT p.proisstrict
 AND NOT p.proleakproof AND NOT p.proretset AND p.proparallel='u' AND p.pronargs=1 AND p.pronargdefaults=0
 AND p.provariadic=0 AND p.proargmodes IS NULL AND p.proallargtypes IS NULL AND p.proargdefaults IS NULL
 AND p.probin IS NULL AND p.prosqlbody IS NULL AND p.prosupport=0 AND p.protrftypes IS NULL AND p.proconfig IS NULL
 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace JOIN pg_catalog.pg_language l ON l.oid=p.prolang
 WHERE n.nspname=$1 AND p.proname='funding_journal_valid'`, schema)
	if err != nil {
		return ErrBaseline
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var body string
		var valid bool
		if rows.Scan(&body, &valid) != nil || !valid || sha256.Sum256([]byte(body)) != sha256.Sum256([]byte(expectedBody)) {
			return ErrBaseline
		}
		count++
	}
	if rows.Err() != nil || count != 1 {
		return ErrBaseline
	}
	return nil
}
