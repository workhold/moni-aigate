package periodicbudget

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/migrations"
)

var (
	ErrVerifyInput     = errors.New("periodic_budget_verify_input")
	ErrVerifyAuthority = errors.New("periodic_budget_verify_authority")
	ErrVerifySchema    = errors.New("periodic_budget_verify_schema")
	ErrVerifyTimeout   = errors.New("periodic_budget_verify_timeout")
	ErrVerifyExecution = errors.New("periodic_budget_verify_execution")
)

type VerificationStatus string

const (
	VerificationBalanced      VerificationStatus = "balanced"
	VerificationMismatch      VerificationStatus = "mismatch"
	VerificationIndeterminate VerificationStatus = "indeterminate"
)

type VerifyOptions struct{ Timeout time.Duration }

// Verifier scans all persisted periodic budgets in one offline RR snapshot.
// It neither shares the online Reader nor takes business row/advisory locks.
type Verifier struct {
	pool    *pgxpool.Pool
	timeout time.Duration
}

type Verification struct {
	SchemaVersion    int                `json:"schema_version"`
	AsOf             UTC6               `json:"as_of"`
	Status           VerificationStatus `json:"status"`
	Balanced         bool               `json:"balanced"`
	CoverageComplete bool               `json:"coverage_complete"`
	Scanned          VerificationScan   `json:"scanned"`
	Mismatches       VerificationCounts `json:"mismatches"`
	Unproven         VerificationGaps   `json:"unproven"`
}

type VerificationScan struct {
	Controls              int64 `json:"controls,string"`
	Windows               int64 `json:"windows,string"`
	Allocations           int64 `json:"allocations,string"`
	Events                int64 `json:"events,string"`
	CustomerSources       int64 `json:"customer_sources,string"`
	SupplierSources       int64 `json:"supplier_sources,string"`
	ProcurementEvents     int64 `json:"procurement_events,string"`
	ConfigurationVersions int64 `json:"configuration_versions,string"`
	Operations            int64 `json:"operations,string"`
	Audits                int64 `json:"audits,string"`
}

type VerificationCounts struct {
	SourceIdentityMismatches       int64 `json:"source_identity_mismatches,string"`
	MissingAllocations             int64 `json:"missing_allocations,string"`
	OrphanAllocations              int64 `json:"orphan_allocations,string"`
	AllocationOriginMismatches     int64 `json:"allocation_origin_mismatches,string"`
	AllocationSourceMismatches     int64 `json:"allocation_source_mismatches,string"`
	AllocationProjectionMismatches int64 `json:"allocation_projection_mismatches,string"`
	MissingEffects                 int64 `json:"missing_effects,string"`
	OrphanEffects                  int64 `json:"orphan_effects,string"`
	EffectBindingMismatches        int64 `json:"effect_binding_mismatches,string"`
	EventChainMismatches           int64 `json:"event_chain_mismatches,string"`
	WindowMismatches               int64 `json:"window_mismatches,string"`
	ControlMismatches              int64 `json:"control_mismatches,string"`
	ProcurementSourceMismatches    int64 `json:"procurement_source_mismatches,string"`
	LegacyBaselineMismatches       int64 `json:"legacy_baseline_mismatches,string"`
	ConfigurationMismatches        int64 `json:"configuration_mismatches,string"`
	VersionMismatches              int64 `json:"version_mismatches,string"`
	OperationMismatches            int64 `json:"operation_mismatches,string"`
	AuditMismatches                int64 `json:"audit_mismatches,string"`
	SnapshotMismatches             int64 `json:"snapshot_mismatches,string"`
}

type VerificationGaps struct {
	LegacyMembershipUnproven        int64 `json:"legacy_membership_unproven,string"`
	LegacyEffectBoundaryUnproven    int64 `json:"legacy_effect_boundary_unproven,string"`
	OperationSnapshotPrefixUnproven int64 `json:"operation_snapshot_prefix_unproven,string"`
}

func NewVerifier(pool *pgxpool.Pool, options VerifyOptions) (*Verifier, error) {
	if pool == nil {
		return nil, ErrVerifyInput
	}
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}
	if options.Timeout < time.Second || options.Timeout > 10*time.Minute {
		return nil, ErrVerifyInput
	}
	return &Verifier{pool: pool, timeout: options.Timeout}, nil
}

func verifyFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrVerifyTimeout
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "57014" || pg.Code == "55P03") {
		return ErrVerifyTimeout
	}
	return ErrVerifyExecution
}

func (v *Verifier) Verify(ctx context.Context) (Verification, error) {
	var out Verification
	if v == nil || v.pool == nil || ctx == nil || v.timeout < time.Second || v.timeout > 10*time.Minute {
		return out, ErrVerifyInput
	}
	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	conn, err := v.pool.Acquire(ctx)
	if err != nil {
		return out, verifyFailure(ctx, err)
	}
	defer conn.Release()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, verifyFailure(ctx, err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		clean, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if e := tx.Rollback(clean); e != nil && !errors.Is(e, pgx.ErrTxClosed) || conn.Conn().PgConn().TxStatus() != 'I' {
			_ = conn.Hijack().Close(clean)
		}
	}()
	var schema string
	if err = tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return Verification{}, verifyFailure(ctx, err)
	}
	if schema == "" || schema == "pg_catalog" || strings.HasPrefix(schema, "pg_temp") {
		return Verification{}, ErrVerifySchema
	}
	deadline, _ := ctx.Deadline()
	ms := time.Until(deadline).Milliseconds()
	if ms < 1 {
		return Verification{}, ErrVerifyTimeout
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('search_path',$1,true),set_config('lock_timeout',$2,true),set_config('statement_timeout',$2,true)`, pgx.Identifier{schema}.Sanitize()+",pg_catalog,pg_temp", strconv.FormatInt(ms, 10)+"ms"); err != nil {
		return Verification{}, verifyFailure(ctx, err)
	}
	if err = verifierAuthority(ctx, tx, schema, conn.Conn().Config().User); err != nil {
		if ctx.Err() != nil {
			return Verification{}, ErrVerifyTimeout
		}
		return Verification{}, err
	}
	if err = migrations.VerifyReadOnlyTx(ctx, tx); err != nil {
		if verifyFailure(ctx, err) == ErrVerifyTimeout {
			return Verification{}, ErrVerifyTimeout
		}
		return Verification{}, ErrVerifySchema
	}
	out, err = readVerification(publicVerifierQuery{tx: tx, ctx: ctx}, verifierRenderer(schema))
	if err != nil {
		return Verification{}, verifyFailure(ctx, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Verification{}, verifyFailure(ctx, err)
	}
	committed = true
	return out, nil
}

// VerificationQuery is a narrow raw-facts reading seam for the fixed periodic
// budget algorithm. Implementing it does not confer maintenance authority.
// The public Verifier retains its own transaction and authentication lifecycle.
type VerificationQuery interface {
	Query(string, ...any) (VerificationRows, error)
	QueryRow(string, ...any) VerificationRow
	Check() error
}

// VerificationRow exposes only typed scalar scanning to the fixed algorithm.
type VerificationRow interface{ Scan(...any) error }

// VerificationRows exposes only the fixed algorithm's row lifecycle.
type VerificationRows interface {
	Next() (bool, error)
	Scan(...any) error
	Err() error
	Close() error
}

// Private aliases keep the single original reading algorithm unchanged.
type verifierQuery = VerificationQuery
type verifierRow = VerificationRow
type verifierRows = VerificationRows

type publicVerifierQuery struct {
	tx  pgx.Tx
	ctx context.Context
}

func (q publicVerifierQuery) Check() error { return q.ctx.Err() }
func (q publicVerifierQuery) Query(sql string, args ...any) (verifierRows, error) {
	rows, err := q.tx.Query(q.ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return publicVerifierRows{rows: rows}, nil
}
func (q publicVerifierQuery) QueryRow(sql string, args ...any) verifierRow {
	return q.tx.QueryRow(q.ctx, sql, args...)
}

type publicVerifierRows struct{ rows pgx.Rows }

func (r publicVerifierRows) Next() (bool, error) {
	if r.rows.Next() {
		return true, nil
	}
	return false, r.rows.Err()
}
func (r publicVerifierRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r publicVerifierRows) Err() error             { return r.rows.Err() }
func (r publicVerifierRows) Close() error           { r.rows.Close(); return nil }

// readVerification shares only the independent SQL and receipt algorithm. It
// does not authenticate, begin, change settings, commit or issue authority.
func readVerification(q verifierQuery, render verifierRender) (Verification, error) {
	var out Verification
	if err := q.Check(); err != nil {
		return out, err
	}
	var at time.Time
	if err := q.QueryRow(`SELECT pg_catalog.clock_timestamp()`).Scan(&at); err != nil {
		return Verification{}, err
	}
	out.AsOf = at.UTC().Format(utc6Layout)
	if !ValidUTC6(out.AsOf) {
		return Verification{}, ErrVerifyExecution
	}
	if err := q.Check(); err != nil {
		return Verification{}, err
	}
	if err := verifierScan(q, render, &out.Scanned); err != nil {
		return Verification{}, err
	}
	m, g := &out.Mismatches, &out.Unproven
	queries := []struct {
		sql  string
		dest []any
	}{
		{verifierSourcesSQL, []any{&m.SourceIdentityMismatches, &m.MissingAllocations, &m.OrphanAllocations, &m.AllocationOriginMismatches, &m.AllocationSourceMismatches, &g.LegacyMembershipUnproven}},
		{verifierEventsSQL, []any{&m.MissingEffects, &m.OrphanEffects, &m.EffectBindingMismatches, &m.EventChainMismatches}},
		{verifierProjectionsSQL, []any{&m.AllocationProjectionMismatches, &m.WindowMismatches, &m.ControlMismatches}},
		{verifierProcurementSQL, []any{&m.ProcurementSourceMismatches, &m.LegacyBaselineMismatches, &g.LegacyEffectBoundaryUnproven}},
		{verifierVersionsSQL, []any{&m.ConfigurationMismatches, &m.VersionMismatches}},
		{verifierAuditsSQL, []any{&m.AuditMismatches}},
	}
	for _, query := range queries {
		if err := q.Check(); err != nil {
			return Verification{}, err
		}
		if err := q.QueryRow(render(query.sql)).Scan(query.dest...); err != nil {
			return Verification{}, err
		}
	}
	if err := verifierOperations(q, render, &out); err != nil {
		return Verification{}, err
	}
	out.SchemaVersion = 1
	out.CoverageComplete = g.LegacyMembershipUnproven == 0 && g.LegacyEffectBoundaryUnproven == 0 && g.OperationSnapshotPrefixUnproven == 0
	out.Status = VerificationBalanced
	if *m != (VerificationCounts{}) {
		out.Status = VerificationMismatch
	} else if !out.CoverageComplete {
		out.Status = VerificationIndeterminate
	}
	out.Balanced = out.Status == VerificationBalanced
	if err := q.Check(); err != nil {
		return Verification{}, err
	}
	return out, nil
}

type verifierRender func(string) string

func verifierRenderer(schema string) verifierRender {
	var pairs []string
	for alias, table := range verifierTables {
		pairs = append(pairs, "@"+alias+"@", pgx.Identifier{schema, table}.Sanitize())
	}
	r := strings.NewReplacer(pairs...)
	return r.Replace
}

var verifierTables = map[string]string{
	"controls": "periodic_budget_controls", "windows": "periodic_budget_windows", "allocations": "periodic_budget_allocations", "events": "periodic_budget_events", "operations": "periodic_budget_operations", "versions": "periodic_budget_configuration_versions",
	"requests": "request_records", "reservations": "reservations", "settlements": "settlements", "attempts": "upstream_attempts", "accounts": "procurement_accounts", "bindings": "credential_procurement_bindings", "procurement": "procurement_reservations", "pe": "procurement_budget_events", "tenants": "tenants", "keys": "api_keys", "users": "users", "audits": "audit_events", "migrations": "schema_migrations",
}

var verifierColumns = map[string]string{
	"controls":     "id scope tenant_id key_id account_id period_kind enabled limit_amount revision tracking_started_at effective_from max_observed_window_start held_amount spent_amount event_seq last_event_id created_xid configuration_operation_id",
	"windows":      "id control_id start_at end_at held_amount spent_amount revision last_event_id",
	"allocations":  "id control_id subject_type subject_id origin origin_source_id window_id admitted_at upper_amount baseline source_held source_spent held_amount spent_amount state revision last_event_id created_xid created_at",
	"events":       "id control_id allocation_id source_kind source_id seq allocation_before_revision window_before_revision before_held before_spent after_held after_spent after_source_spent after_state held_delta spent_delta created_xid created_at",
	"operations":   "operation_id kind actor scope target resource_id revision safe_input input_fingerprint result_snapshot created_xid created_at",
	"versions":     "control_id revision operation_id period_kind enabled limit_amount actor created_xid created_at",
	"requests":     "id tenant_id key_id pb_created_xid pb_tenant_control pb_key_control pb_admitted_at",
	"reservations": "id tenant_id request_id amount status",
	"settlements":  "id tenant_id request_id charged_amount status",
	"attempts":     "id tenant_id request_id credential_id channel_id pb_created_xid",
	"accounts":     "id held_amount spent_amount",
	"bindings":     "id account_id credential_id channel_id",
	"procurement":  "attempt_id account_id binding_id upper_amount held_amount spent_amount state pb_created_xid pb_control pb_admitted_at",
	"pe":           "id event_key account_id attempt_id held_delta spent_delta type pb_created_xid",
	"tenants":      "id", "keys": "id tenant_id", "users": "id",
	"audits":     "id actor tenant_scope action target sanitized_diff request_id created_at",
	"migrations": "name checksum",
}

func verifierAuthority(ctx context.Context, tx pgx.Tx, schema, authenticatedUser string) error {
	var login bool
	if e := tx.QueryRow(ctx, `SELECT current_user=session_user AND session_user=$1 AND coalesce((SELECT rolcanlogin FROM pg_catalog.pg_roles WHERE rolname=session_user),false)`, authenticatedUser).Scan(&login); e != nil {
		return verifyFailure(ctx, e)
	}
	if !login {
		return ErrVerifyAuthority
	}
	for alias, table := range verifierTables {
		var exists, selectOK, owns bool
		e := tx.QueryRow(ctx, `SELECT count(*)=1,coalesce(bool_and(has_table_privilege(current_user,c.oid,'SELECT')),false),coalesce(bool_and(c.relowner=(SELECT oid FROM pg_catalog.pg_roles WHERE rolname=session_user)),false) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2 AND c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity AND (SELECT count(*) FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped AND a.attname=ANY($3::text[]))=cardinality($3::text[])`, schema, table, strings.Fields(verifierColumns[alias])).Scan(&exists, &selectOK, &owns)
		if e != nil {
			return verifyFailure(ctx, e)
		}
		if !exists {
			return ErrVerifySchema
		}
		if alias == "controls" || alias == "windows" || alias == "allocations" || alias == "events" || alias == "operations" || alias == "versions" {
			if !owns {
				return ErrVerifyAuthority
			}
		}
		if !selectOK {
			return ErrVerifyAuthority
		}
	}
	return nil
}

func verifierScan(q verifierQuery, render verifierRender, s *VerificationScan) error {
	return q.QueryRow(render(`SELECT (SELECT count(*) FROM @controls@),(SELECT count(*) FROM @windows@),(SELECT count(*) FROM @allocations@),(SELECT count(*) FROM @events@),(SELECT count(*) FROM @requests@),(SELECT count(*) FROM @procurement@),(SELECT count(*) FROM @pe@),(SELECT count(*) FROM @versions@),(SELECT count(*) FROM @operations@),(SELECT count(*) FROM @audits@ WHERE action='periodic_budget.configured')`)).Scan(&s.Controls, &s.Windows, &s.Allocations, &s.Events, &s.CustomerSources, &s.SupplierSources, &s.ProcurementEvents, &s.ConfigurationVersions, &s.Operations, &s.Audits)
}
