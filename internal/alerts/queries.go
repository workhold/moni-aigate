package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func safeError(e error) error {
	if e == nil {
		return nil
	}
	var a *Error
	if errors.As(e, &a) {
		return a
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return fail("alert_not_found")
	}
	return fail("alert_unavailable")
}
func limits(ctx context.Context, tx pgx.Tx, statement, lock time.Duration) error {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < statement {
		statement = time.Until(deadline)
	}
	ms := statement.Milliseconds()
	if ms < 1 {
		return context.DeadlineExceeded
	}
	_, e := tx.Exec(ctx, `SELECT set_config('statement_timeout',$1,true),set_config('lock_timeout',$2,true)`, fmt.Sprintf("%dms", ms), fmt.Sprintf("%dms", lock.Milliseconds()))
	return e
}
func retryable(e error) bool {
	var p *pgconn.PgError
	return errors.As(e, &p) && (p.Code == "40P01" || p.Code == "40001")
}
func commit(tx pgx.Tx, ctx context.Context) error {
	e := tx.Commit(ctx)
	if e == nil {
		return nil
	}
	var p *pgconn.PgError
	if errors.As(e, &p) {
		return e
	}
	return &Error{Code: "alert_unavailable", CommitUnknown: true}
}
func (s *Store) read(ctx context.Context, r Reader, fn func(context.Context, pgx.Tx, time.Time) error) error {
	if !validID(r.ActorID) {
		return fail("invalid_request")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return safeError(e)
	}
	defer tx.Rollback(ctx)
	if e = limits(ctx, tx, 2*time.Second, 250*time.Millisecond); e != nil {
		return safeError(e)
	}
	var allowed bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active' AND platform_role IN ('admin','finance_operator','finance_approver'))`, r.ActorID).Scan(&allowed); e != nil {
		return safeError(e)
	}
	if !allowed {
		return fail("forbidden")
	}
	var at time.Time
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()::timestamptz(6)`).Scan(&at); e == nil {
		e = fn(ctx, tx, at)
	}
	if e != nil {
		return safeError(e)
	}
	return safeError(tx.Commit(ctx))
}
func (s *Store) Capabilities(ctx context.Context, r Reader) (Capabilities, error) {
	var out Capabilities
	e := s.read(ctx, r, func(_ context.Context, _ pgx.Tx, at time.Time) error {
		reason := "available"
		if s.options.RecoveryMode {
			reason = "recovery_mode"
		} else if !s.options.DeploymentEnabled {
			reason = "deployment_disabled"
		}
		out = Capabilities{s.options.DeploymentEnabled, s.options.RecoveryMode, s.options.DeploymentEnabled && !s.options.RecoveryMode, reason, "64", "60", "180", Time{at}}
		return nil
	})
	return out, e
}

type dbRule struct {
	id                   string
	kind                 Kind
	scope                ScopeType
	scopeID, label       string
	state                RuleState
	params               []byte
	revision             int64
	createdBy, updatedBy string
	created, updated     time.Time
	retired              *time.Time
	command              string
}

const ruleColumns = `r.id,r.kind,r.scope_type,r.scope_id,r.label,r.state,r.parameters,r.configuration_revision,r.created_by,r.updated_by,r.created_at,r.updated_at,r.retired_at,r.last_command_id`

func (r *dbRule) targets() []any {
	return []any{&r.id, &r.kind, &r.scope, &r.scopeID, &r.label, &r.state, &r.params, &r.revision, &r.createdBy, &r.updatedBy, &r.created, &r.updated, &r.retired, &r.command}
}

type dbRuntime struct {
	rule                          string
	configured, write, generation int64
	next, attempt, success        *time.Time
	evaluated                     *int64
	sample                        []byte
	errorCode, episode            *string
}

const runtimeColumns = `t.rule_id,t.configured_revision,t.write_revision,t.episode_generation,t.next_evaluation_at,t.last_attempt_at,t.last_success_at,t.evaluated_revision,t.last_sample,t.last_error_code,t.current_episode_id`

func (r *dbRuntime) targets() []any {
	return []any{&r.rule, &r.configured, &r.write, &r.generation, &r.next, &r.attempt, &r.success, &r.evaluated, &r.sample, &r.errorCode, &r.episode}
}
func timePointer(t *time.Time) *Time {
	if t == nil {
		return nil
	}
	return &Time{*t}
}
func decimalPointer(v *int64) *Decimal {
	if v == nil {
		return nil
	}
	out := strconv.FormatInt(*v, 10)
	return &out
}
func (s *Store) freshness(state RuleState, revision int64, success *time.Time, evaluated *int64, at time.Time) Freshness {
	if state == "retired" {
		return "retired"
	}
	if state == "disabled" {
		return "disabled"
	}
	if s.options.RecoveryMode || !s.options.DeploymentEnabled {
		return "paused"
	}
	if success == nil || evaluated == nil || *evaluated != revision {
		return "never_evaluated"
	}
	if at.Before(*success) || at.Sub(*success) > 180*time.Second {
		return "stale"
	}
	return "fresh"
}
func (s *Store) ruleView(r dbRule, t dbRuntime, at time.Time) (RuleView, error) {
	p, e := DecodeParameters(r.kind, r.params)
	if e != nil {
		return RuleView{}, fail("alert_unavailable")
	}
	sample, e := decodeSample(t.sample)
	if e != nil {
		return RuleView{}, fail("alert_unavailable")
	}
	runtime := RuntimeView{timePointer(t.next), timePointer(t.attempt), timePointer(t.success), decimalPointer(t.evaluated), sample, t.errorCode, t.episode, s.freshness(r.state, r.revision, t.success, t.evaluated, at), Time{at}}
	return RuleView{r.id, r.kind, r.scope, r.scopeID, r.label, r.state, p, strconv.FormatInt(r.revision, 10), r.createdBy, r.updatedBy, Time{r.created}, Time{r.updated}, timePointer(r.retired), runtime}, nil
}
func scanRuleRuntime(row pgx.Row) (dbRule, dbRuntime, error) {
	var r dbRule
	var rt dbRuntime
	args := append(r.targets(), rt.targets()...)
	e := row.Scan(args...)
	return r, rt, e
}
func nextCursor(at time.Time, id, digest string) (*string, error) {
	v, e := EncodeCursor(Cursor{1, Time{at}, id, digest})
	return &v, e
}
func boundary(c *Cursor) (any, any) {
	if c == nil {
		return nil, nil
	}
	return c.T.Time, c.ID
}
func validRuleFilter(f RuleFilter) bool {
	return (f.State == nil || *f.State == "enabled" || *f.State == "disabled" || *f.State == "retired") && (f.Kind == nil || validKind(*f.Kind)) && (f.ScopeID == nil || f.Kind != nil && validID(*f.ScopeID)) && validCursor(f.Cursor, RuleFilterDigest(f))
}
func (s *Store) Rules(ctx context.Context, reader Reader, f RuleFilter) (Page[RuleView], error) {
	out := Page[RuleView]{Data: []RuleView{}}
	if !validRuleFilter(f) {
		return out, fail("invalid_request")
	}
	e := s.read(ctx, reader, func(ctx context.Context, tx pgx.Tx, at time.Time) error {
		cut, id := boundary(f.Cursor)
		rows, e := tx.Query(ctx, `SELECT `+ruleColumns+`,`+runtimeColumns+` FROM alert_rules r JOIN alert_rule_runtime t ON t.rule_id=r.id WHERE ($1::text IS NULL OR r.state=$1) AND ($2::text IS NULL OR r.kind=$2) AND ($3::text IS NULL OR r.scope_id=$3) AND ($4::timestamptz IS NULL OR (r.created_at,r.id COLLATE "C")<($4::timestamptz,$5::text COLLATE "C")) ORDER BY r.created_at DESC,r.id COLLATE "C" DESC LIMIT 51`, f.State, f.Kind, f.ScopeID, cut, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			r, t, e := scanRuleRuntime(rows)
			if e != nil {
				return e
			}
			v, e := s.ruleView(r, t, at)
			if e != nil {
				return e
			}
			out.Data = append(out.Data, v)
		}
		return rows.Err()
	})
	if e != nil {
		return Page[RuleView]{}, e
	}
	if len(out.Data) > 50 {
		out.Data = out.Data[:50]
		last := out.Data[49]
		out.NextCursor, e = nextCursor(last.CreatedAt.Time, last.ID, RuleFilterDigest(f))
	}
	return out, e
}
func (s *Store) Rule(ctx context.Context, reader Reader, id ID) (RuleView, error) {
	var out RuleView
	if !validID(id) {
		return out, fail("invalid_request")
	}
	e := s.read(ctx, reader, func(ctx context.Context, tx pgx.Tx, at time.Time) error {
		r, t, e := scanRuleRuntime(tx.QueryRow(ctx, `SELECT `+ruleColumns+`,`+runtimeColumns+` FROM alert_rules r JOIN alert_rule_runtime t ON t.rule_id=r.id WHERE r.id=$1`, id))
		if e != nil {
			return e
		}
		out, e = s.ruleView(r, t, at)
		return e
	})
	return out, e
}

type dbEpisode struct {
	id, rule                string
	generation, revision    int64
	snapshot                []byte
	opened                  time.Time
	initial                 []byte
	seen                    time.Time
	latest                  []byte
	action                  int64
	ackBy                   *string
	ackAt                   *time.Time
	ackCommand              *string
	resolved                *time.Time
	reason, resolvedCommand *string
}

const episodeColumns = `e.id,e.rule_id,e.generation,e.rule_revision,e.rule_snapshot,e.opened_at,e.initial_sample,e.last_seen_at,e.latest_sample,e.action_revision,e.acknowledged_by,e.acknowledged_at,e.acknowledged_command_id,e.resolved_at,e.resolve_reason,e.resolved_command_id`

func (e *dbEpisode) targets() []any {
	return []any{&e.id, &e.rule, &e.generation, &e.revision, &e.snapshot, &e.opened, &e.initial, &e.seen, &e.latest, &e.action, &e.ackBy, &e.ackAt, &e.ackCommand, &e.resolved, &e.reason, &e.resolvedCommand}
}
func (s *Store) episodeView(row pgx.Row, at time.Time) (EpisodeView, error) {
	var ep dbEpisode
	var state RuleState
	var rev int64
	var success *time.Time
	var evaluated *int64
	args := append(ep.targets(), &state, &rev, &success, &evaluated)
	if e := row.Scan(args...); e != nil {
		return EpisodeView{}, e
	}
	var snapshot RuleSnapshot
	if json.Unmarshal(ep.snapshot, &snapshot) != nil {
		return EpisodeView{}, fail("alert_unavailable")
	}
	initial, e := decodeSample(ep.initial)
	if e != nil {
		return EpisodeView{}, fail("alert_unavailable")
	}
	latest, e := decodeSample(ep.latest)
	if e != nil {
		return EpisodeView{}, fail("alert_unavailable")
	}
	status := "open"
	if ep.resolved != nil {
		status = "resolved"
	} else if ep.ackBy != nil {
		status = "acknowledged"
	}
	return EpisodeView{ep.id, ep.rule, strconv.FormatInt(ep.generation, 10), strconv.FormatInt(ep.revision, 10), snapshot, status, strconv.FormatInt(ep.action, 10), Time{ep.opened}, initial, Time{ep.seen}, latest, ep.ackBy, timePointer(ep.ackAt), timePointer(ep.resolved), ep.reason, s.freshness(state, rev, success, evaluated, at), Time{at}}, nil
}

const episodeSelect = `SELECT ` + episodeColumns + `,r.state,r.configuration_revision,t.last_success_at,t.evaluated_revision FROM alert_episodes e JOIN alert_rules r ON r.id=e.rule_id JOIN alert_rule_runtime t ON t.rule_id=r.id `
const episodeStatus = `CASE WHEN e.resolved_at IS NOT NULL THEN 'resolved' WHEN e.acknowledged_at IS NOT NULL THEN 'acknowledged' ELSE 'open' END`

func (s *Store) Episodes(ctx context.Context, r Reader, f EpisodeFilter) (Page[EpisodeView], error) {
	out := Page[EpisodeView]{Data: []EpisodeView{}}
	if f.Status != nil && *f.Status != "open" && *f.Status != "acknowledged" && *f.Status != "resolved" || f.RuleID != nil && !validID(*f.RuleID) || f.Kind != nil && !validKind(*f.Kind) || !validCursor(f.Cursor, EpisodeFilterDigest(f)) {
		return out, fail("invalid_request")
	}
	e := s.read(ctx, r, func(ctx context.Context, tx pgx.Tx, at time.Time) error {
		cut, id := boundary(f.Cursor)
		rows, e := tx.Query(ctx, episodeSelect+`WHERE ($1::text IS NULL OR `+episodeStatus+`=$1) AND ($2::text IS NULL OR e.rule_id=$2) AND ($3::text IS NULL OR r.kind=$3) AND ($4::timestamptz IS NULL OR (e.opened_at,e.id COLLATE "C")<($4::timestamptz,$5::text COLLATE "C")) ORDER BY e.opened_at DESC,e.id COLLATE "C" DESC LIMIT 51`, f.Status, f.RuleID, f.Kind, cut, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v, e := s.episodeView(rows, at)
			if e != nil {
				return e
			}
			out.Data = append(out.Data, v)
		}
		return rows.Err()
	})
	if e != nil {
		return Page[EpisodeView]{}, e
	}
	if len(out.Data) > 50 {
		out.Data = out.Data[:50]
		v := out.Data[49]
		out.NextCursor, e = nextCursor(v.OpenedAt.Time, v.ID, EpisodeFilterDigest(f))
	}
	return out, e
}
func (s *Store) Episode(ctx context.Context, r Reader, id ID) (EpisodeView, error) {
	var out EpisodeView
	if !validID(id) {
		return out, fail("invalid_request")
	}
	e := s.read(ctx, r, func(ctx context.Context, tx pgx.Tx, at time.Time) error {
		var e error
		out, e = s.episodeView(tx.QueryRow(ctx, episodeSelect+`WHERE e.id=$1`, id), at)
		return e
	})
	return out, e
}
func (s *Store) Events(ctx context.Context, r Reader, id ID, f EventFilter) (Page[EventView], error) {
	out := Page[EventView]{Data: []EventView{}}
	if !validID(id) || !validCursor(f.Cursor, EventFilterDigest(id)) {
		return out, fail("invalid_request")
	}
	e := s.read(ctx, r, func(ctx context.Context, tx pgx.Tx, _ time.Time) error {
		var exists bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM alert_episodes WHERE id=$1)`, id).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return fail("alert_not_found")
		}
		cut, last := boundary(f.Cursor)
		rows, e := tx.Query(ctx, `SELECT id,episode_id,action_revision::text,type,actor,observed_at,safe_sample,created_at FROM alert_episode_events WHERE episode_id=$1 AND ($2::timestamptz IS NULL OR (created_at,id COLLATE "C")<($2::timestamptz,$3::text COLLATE "C")) ORDER BY created_at DESC,id COLLATE "C" DESC LIMIT 51`, id, cut, last)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var v EventView
			var observed, created time.Time
			var raw []byte
			if e = rows.Scan(&v.ID, &v.EpisodeID, &v.ActionRevision, &v.Type, &v.Actor, &observed, &raw, &created); e != nil {
				return e
			}
			v.ObservedAt, v.CreatedAt = Time{observed}, Time{created}
			v.SafeSample, e = decodeSample(raw)
			if e != nil {
				return fail("alert_unavailable")
			}
			out.Data = append(out.Data, v)
		}
		return rows.Err()
	})
	if e != nil {
		return Page[EventView]{}, e
	}
	if len(out.Data) > 50 {
		out.Data = out.Data[:50]
		v := out.Data[49]
		out.NextCursor, e = nextCursor(v.CreatedAt.Time, v.ID, EventFilterDigest(id))
	}
	return out, e
}

const receiptColumns = `operation_id,kind,status,reason,resource_id,result_revision::text,created_at`

func scanReceipt(row pgx.Row) (CommandReceipt, error) {
	var v CommandReceipt
	var at time.Time
	e := row.Scan(&v.OperationID, &v.Kind, &v.Status, &v.Reason, &v.ResourceID, &v.ResultRevision, &at)
	v.CreatedAt = Time{at}
	return v, e
}
func (s *Store) Operation(ctx context.Context, r Reader, id ID) (CommandReceipt, error) {
	var out CommandReceipt
	if !validID(id) {
		return out, fail("invalid_request")
	}
	e := s.read(ctx, r, func(ctx context.Context, tx pgx.Tx, _ time.Time) error {
		var e error
		out, e = scanReceipt(tx.QueryRow(ctx, `SELECT `+receiptColumns+` FROM alert_commands WHERE operation_id=$1 AND actor=$2`, id, r.ActorID))
		if e != nil {
			return e
		}
		if out.Kind != "acknowledge" {
			var admin bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active' AND platform_role='admin')`, r.ActorID).Scan(&admin); e != nil {
				return e
			}
			if !admin {
				return fail("forbidden")
			}
		}
		if out.Status != "applied" && out.Status != "rejected" {
			return fail("alert_unavailable")
		}
		return nil
	})
	return out, e
}

var _ Queries = (*Store)(nil)
