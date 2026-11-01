package alerts

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type rejected struct{ reason string }

func (e *rejected) Error() string { return e.reason }
func reject(reason string) error  { return &rejected{reason} }

type commandEffect struct {
	resource string
	revision int64
}

func actorLock(ctx context.Context, tx pgx.Tx, r Reader, admin bool) error {
	var id string
	if e := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, r.ActorID).Scan(&id); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return fail("forbidden")
		}
		return e
	}
	// This statement starts after any user-row lock wait, so old HTTP/session
	// role data and the pre-lock READ COMMITTED snapshot cannot grant a write.
	var status, role string
	if e := tx.QueryRow(ctx, `SELECT status,platform_role FROM users WHERE id=$1`, r.ActorID).Scan(&status, &role); e != nil {
		return e
	}
	if status != "active" || admin && role != "admin" || !admin && role != "admin" && role != "finance_operator" && role != "finance_approver" {
		return fail("forbidden")
	}
	return nil
}
func commandDigest(actor ID, kind CommandKind, target *ID, body any) (string, error) {
	raw, e := encode(body)
	if e != nil {
		return "", fail("invalid_request")
	}
	v := struct {
		V      int         `json:"v"`
		Actor  ID          `json:"actor"`
		Kind   CommandKind `json:"kind"`
		Target *ID         `json:"target_id"`
		Body   any         `json:"body"`
	}{1, actor, kind, target, jsonBody(raw)}
	return digest(v), nil
}

// RawMessage's field order is the already canonical, independent Go body.
type jsonBody []byte

func (v jsonBody) MarshalJSON() ([]byte, error) { return v, nil }
func (s *Store) command(ctx context.Context, r Reader, operation ID, kind CommandKind, target *ID, body any, fn func(context.Context, pgx.Tx) (commandEffect, error)) (CommandReceipt, error) {
	if !validID(r.ActorID) || !validID(operation) || target != nil && !validID(*target) {
		return CommandReceipt{}, fail("invalid_request")
	}
	if s.options.RecoveryMode {
		return CommandReceipt{}, fail("alert_unavailable")
	}
	sum, e := commandDigest(r.ActorID, kind, target, body)
	if e != nil {
		return CommandReceipt{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		out, e := s.commandOnce(ctx, r, operation, kind, target, sum, fn)
		if e == nil || ctx.Err() != nil || !retryable(e) {
			return out, safeError(e)
		}
	}
	return CommandReceipt{}, fail("alert_unavailable")
}
func (s *Store) commandOnce(ctx context.Context, r Reader, operation ID, kind CommandKind, target *ID, sum string, fn func(context.Context, pgx.Tx) (commandEffect, error)) (CommandReceipt, error) {
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		return CommandReceipt{}, e
	}
	defer tx.Rollback(ctx)
	if e = limits(ctx, tx, 3*time.Second, 500*time.Millisecond); e != nil {
		return CommandReceipt{}, e
	}
	if e = actorLock(ctx, tx, r, kind != "acknowledge"); e != nil {
		return CommandReceipt{}, e
	}
	var inserted string
	e = tx.QueryRow(ctx, `INSERT INTO alert_commands(operation_id,actor,kind,target_id,canonical_digest,status) VALUES($1,$2,$3,$4,$5,'claimed') ON CONFLICT(operation_id) DO NOTHING RETURNING operation_id`, operation, r.ActorID, kind, target, sum).Scan(&inserted)
	if errors.Is(e, pgx.ErrNoRows) {
		var actor, oldSum string
		var oldKind CommandKind
		var oldTarget *string
		if e = tx.QueryRow(ctx, `SELECT actor,kind,target_id,canonical_digest FROM alert_commands WHERE operation_id=$1`, operation).Scan(&actor, &oldKind, &oldTarget, &oldSum); e != nil {
			return CommandReceipt{}, e
		}
		sameTarget := oldTarget == nil && target == nil || oldTarget != nil && target != nil && *oldTarget == *target
		if actor != r.ActorID || oldKind != kind || oldSum != sum || !sameTarget {
			return CommandReceipt{}, fail("alert_operation_conflict")
		}
		out, e := scanReceipt(tx.QueryRow(ctx, `SELECT `+receiptColumns+` FROM alert_commands WHERE operation_id=$1`, operation))
		if e != nil {
			return CommandReceipt{}, e
		}
		if out.Status != "applied" && out.Status != "rejected" {
			return CommandReceipt{}, fail("alert_unavailable")
		}
		if e = commit(tx, ctx); e != nil {
			return CommandReceipt{}, e
		}
		return out, nil
	}
	if e != nil {
		return CommandReceipt{}, e
	}
	if _, e = tx.Exec(ctx, `SAVEPOINT alert_effect`); e != nil {
		return CommandReceipt{}, e
	}
	effect, e := fn(ctx, tx)
	var business *rejected
	var pg *pgconn.PgError
	if errors.As(e, &pg) && pg.Code == "23505" && pg.ConstraintName == "alert_rules_live_scope" {
		e = reject("alert_rule_exists")
	}
	status, reason := "applied", ""
	var resource any = effect.resource
	var revision any = effect.revision
	if e != nil {
		if !errors.As(e, &business) {
			return CommandReceipt{}, e
		}
		if _, e = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT alert_effect`); e != nil {
			return CommandReceipt{}, e
		}
		status, reason = "rejected", business.reason
		resource, revision = target, nil
	}
	if _, e = tx.Exec(ctx, `RELEASE SAVEPOINT alert_effect`); e != nil {
		return CommandReceipt{}, e
	}
	if _, e = tx.Exec(ctx, `UPDATE alert_commands SET status=$2,reason=$3,resource_id=$4,result_revision=$5 WHERE operation_id=$1`, operation, status, reason, resource, revision); e != nil {
		return CommandReceipt{}, e
	}
	out, e := scanReceipt(tx.QueryRow(ctx, `SELECT `+receiptColumns+` FROM alert_commands WHERE operation_id=$1`, operation))
	if e != nil {
		return CommandReceipt{}, e
	}
	if e = commit(tx, ctx); e != nil {
		return CommandReceipt{}, e
	}
	return out, nil
}
func control(ctx context.Context, tx pgx.Tx) error {
	var b bool
	return tx.QueryRow(ctx, `SELECT singleton FROM alert_control WHERE singleton FOR UPDATE`).Scan(&b)
}
func lockRule(ctx context.Context, tx pgx.Tx, id ID) (dbRule, dbRuntime, error) {
	var r dbRule
	var t dbRuntime
	if e := tx.QueryRow(ctx, `SELECT `+ruleColumns+` FROM alert_rules r WHERE id=$1 FOR UPDATE`, id).Scan(r.targets()...); e != nil {
		return r, t, e
	}
	e := tx.QueryRow(ctx, `SELECT `+runtimeColumns+` FROM alert_rule_runtime t WHERE rule_id=$1 FOR UPDATE`, id).Scan(t.targets()...)
	if e == nil && t.configured != r.revision {
		e = fail("alert_unavailable")
	}
	return r, t, e
}
func lockEpisode(ctx context.Context, tx pgx.Tx, id ID) (dbEpisode, error) {
	var ep dbEpisode
	e := tx.QueryRow(ctx, `SELECT `+episodeColumns+` FROM alert_episodes e WHERE id=$1 FOR UPDATE`, id).Scan(ep.targets()...)
	return ep, e
}
func databaseTime(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var t time.Time
	e := tx.QueryRow(ctx, `SELECT clock_timestamp()::timestamptz(6)`).Scan(&t)
	return t, e
}
func sourceExists(ctx context.Context, tx pgx.Tx, kind Kind, id ID) (bool, error) {
	table := ""
	switch kind {
	case ChannelQuarantined:
		table = "channels"
	case ProcurementBudgetLow, ProcurementUnresolvedAged:
		table = "procurement_accounts"
	case CustomerReconciliationAged:
		table = "tenants"
	default:
		return false, fail("invalid_request")
	}
	var exists bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id=$1)`, id).Scan(&exists)
	return exists, e
}
func audit(ctx context.Context, tx pgx.Tx, actor, action, target string, values any) error {
	raw, e := encode(values)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES($1,$2,$3,$4)`, actor, action, target, raw)
	return e
}
func event(ctx context.Context, tx pgx.Tx, episode string, revision int64, kind EventType, actor string, at time.Time, sample []byte, operation *string) error {
	_, e := tx.Exec(ctx, `INSERT INTO alert_episode_events(id,episode_id,action_revision,type,actor,observed_at,safe_sample,created_at,command_id) VALUES($1,$2,$3,$4,$5,$6,$7,$6,$8)`, uuid.NewString(), episode, revision, kind, actor, at, sample, operation)
	return e
}
func countRules(ctx context.Context, tx pgx.Tx) (int64, int64, error) {
	var live, enabled int64
	e := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE state='enabled') FROM alert_rules WHERE state<>'retired'`).Scan(&live, &enabled)
	return live, enabled, e
}
func (s *Store) Create(ctx context.Context, r Reader, in CreateRule) (CommandReceipt, error) {
	if !validKind(in.Kind) || !scopeMatches(in.Kind, in.ScopeType) || !validID(in.ScopeID) || !validLabel(in.Label) || (in.State != "enabled" && in.State != "disabled") {
		return CommandReceipt{}, fail("invalid_request")
	}
	params, e := parameters(in.Kind, in.Parameters)
	if e != nil {
		return CommandReceipt{}, e
	}
	body := struct {
		Kind       Kind      `json:"kind"`
		ScopeType  ScopeType `json:"scope_type"`
		ScopeID    ID        `json:"scope_id"`
		Label      string    `json:"label"`
		State      RuleState `json:"state"`
		Parameters jsonBody  `json:"parameters"`
	}{in.Kind, in.ScopeType, in.ScopeID, in.Label, in.State, params}
	return s.command(ctx, r, in.OperationID, "rule_create", nil, body, func(ctx context.Context, tx pgx.Tx) (commandEffect, error) {
		if e := control(ctx, tx); e != nil {
			return commandEffect{}, e
		}
		exists, e := sourceExists(ctx, tx, in.Kind, in.ScopeID)
		if e != nil {
			return commandEffect{}, e
		}
		if !exists {
			return commandEffect{}, reject("alert_source_missing")
		}
		live, enabled, e := countRules(ctx, tx)
		if e != nil {
			return commandEffect{}, e
		}
		if live >= 256 || in.State == "enabled" && enabled >= 64 {
			return commandEffect{}, reject("alert_rule_limit")
		}
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM alert_rules WHERE kind=$1 AND scope_id=$2 AND state<>'retired')`, in.Kind, in.ScopeID).Scan(&exists); e != nil {
			return commandEffect{}, e
		}
		if exists {
			return commandEffect{}, reject("alert_rule_exists")
		}
		at, e := databaseTime(ctx, tx)
		if e != nil {
			return commandEffect{}, e
		}
		id := uuid.NewString()
		if _, e = tx.Exec(ctx, `INSERT INTO alert_rules(id,kind,scope_type,scope_id,label,state,parameters,configuration_revision,created_by,updated_by,created_at,updated_at,last_command_id) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$8,$9,$9,$10)`, id, in.Kind, in.ScopeType, in.ScopeID, in.Label, in.State, params, r.ActorID, at, in.OperationID); e != nil {
			return commandEffect{}, e
		}
		var due any
		if in.State == "enabled" {
			due = at
		}
		if _, e = tx.Exec(ctx, `INSERT INTO alert_rule_runtime(rule_id,configured_revision,next_evaluation_at) VALUES($1,1,$2)`, id, due); e != nil {
			return commandEffect{}, e
		}
		e = audit(ctx, tx, r.ActorID, "alerts.rule.create", id, map[string]any{"operation_id": in.OperationID, "configuration_revision": "1"})
		return commandEffect{id, 1}, e
	})
}
func parametersAny(p Parameters) ([]byte, error) {
	for _, k := range []Kind{ChannelQuarantined, ProcurementBudgetLow, CustomerReconciliationAged} {
		if raw, e := parameters(k, p); e == nil {
			return raw, nil
		}
	}
	return nil, fail("invalid_request")
}
func (s *Store) Configure(ctx context.Context, reader Reader, id ID, in ConfigureRule) (CommandReceipt, error) {
	if !validID(id) || !uintBound(in.ExpectedRevision, maxInt64Text, true) || !validLabel(in.Label) || (in.State != "enabled" && in.State != "disabled") {
		return CommandReceipt{}, fail("invalid_request")
	}
	p, e := parametersAny(in.Parameters)
	if e != nil {
		return CommandReceipt{}, e
	}
	body := struct {
		Revision   Decimal   `json:"expected_revision"`
		Label      string    `json:"label"`
		State      RuleState `json:"state"`
		Parameters jsonBody  `json:"parameters"`
	}{in.ExpectedRevision, in.Label, in.State, p}
	return s.command(ctx, reader, in.OperationID, "rule_configure", &id, body, func(ctx context.Context, tx pgx.Tx) (commandEffect, error) {
		return s.configure(ctx, tx, reader, id, in.OperationID, in.ExpectedRevision, in.Label, in.State, p, false)
	})
}
func (s *Store) Retire(ctx context.Context, r Reader, id ID, in RetireRule) (CommandReceipt, error) {
	if !validID(id) || !uintBound(in.ExpectedRevision, maxInt64Text, true) {
		return CommandReceipt{}, fail("invalid_request")
	}
	body := struct {
		Revision Decimal `json:"expected_revision"`
	}{in.ExpectedRevision}
	return s.command(ctx, r, in.OperationID, "rule_retire", &id, body, func(ctx context.Context, tx pgx.Tx) (commandEffect, error) {
		return s.configure(ctx, tx, r, id, in.OperationID, in.ExpectedRevision, "", "retired", nil, true)
	})
}
func (s *Store) configure(ctx context.Context, tx pgx.Tx, actor Reader, id, operation, expected, label string, state RuleState, params []byte, retire bool) (commandEffect, error) {
	if e := control(ctx, tx); e != nil {
		return commandEffect{}, e
	}
	r, t, e := lockRule(ctx, tx, id)
	if errors.Is(e, pgx.ErrNoRows) {
		return commandEffect{}, reject("alert_not_found")
	}
	if e != nil {
		return commandEffect{}, e
	}
	if r.state == "retired" {
		return commandEffect{}, reject("alert_rule_retired")
	}
	if strconv.FormatInt(r.revision, 10) != expected {
		return commandEffect{}, reject("alert_revision_stale")
	}
	if retire {
		label, params = r.label, r.params
	} else {
		normalized, e := DecodeParameters(r.kind, params)
		if e != nil {
			return commandEffect{}, fail("invalid_request")
		}
		params, e = encode(normalized)
		if e != nil {
			return commandEffect{}, e
		}
	}
	old, e := DecodeParameters(r.kind, r.params)
	if e != nil {
		return commandEffect{}, fail("alert_unavailable")
	}
	oldParams, e := encode(old)
	if e != nil {
		return commandEffect{}, e
	}
	if r.label == label && r.state == state && bytes.Equal(oldParams, params) {
		return commandEffect{id, r.revision}, nil
	}
	if r.revision == math.MaxInt64 || t.write == math.MaxInt64 {
		return commandEffect{}, fail("alert_unavailable")
	}
	if r.state != "enabled" && state == "enabled" {
		_, enabled, e := countRules(ctx, tx)
		if e != nil {
			return commandEffect{}, e
		}
		if enabled >= 64 {
			return commandEffect{}, reject("alert_rule_limit")
		}
	}
	var ep dbEpisode
	if t.episode != nil {
		ep, e = lockEpisode(ctx, tx, *t.episode)
		if e != nil {
			return commandEffect{}, e
		}
	}
	at, e := databaseTime(ctx, tx)
	if e != nil {
		return commandEffect{}, e
	}
	if at.Before(r.updated) || t.episode != nil && (at.Before(ep.seen) || ep.ackAt != nil && at.Before(*ep.ackAt)) {
		return commandEffect{}, fail("alert_unavailable")
	}
	var retired any
	if retire {
		retired = at
	}
	if _, e = tx.Exec(ctx, `UPDATE alert_rules SET label=$2,state=$3,parameters=$4,updated_by=$5,updated_at=$6,retired_at=$7,last_command_id=$8 WHERE id=$1`, id, label, state, params, actor.ActorID, at, retired, operation); e != nil {
		return commandEffect{}, e
	}
	if t.episode != nil {
		reason := EventType("rule_changed")
		if state == "disabled" {
			reason = "rule_disabled"
		}
		if state == "retired" {
			reason = "rule_retired"
		}
		if _, e = tx.Exec(ctx, `UPDATE alert_episodes SET resolved_at=$2,resolve_reason=$3,resolved_command_id=$4 WHERE id=$1`, ep.id, at, reason, operation); e != nil {
			return commandEffect{}, e
		}
		if e = event(ctx, tx, ep.id, ep.action+1, reason, actor.ActorID, at, nil, &operation); e != nil {
			return commandEffect{}, e
		}
	}
	var due any
	if state == "enabled" {
		due = at
	}
	if _, e = tx.Exec(ctx, `UPDATE alert_rule_runtime SET configured_revision=$2,next_evaluation_at=$3,last_attempt_at=NULL,last_success_at=NULL,evaluated_revision=NULL,last_sample=NULL,last_error_code=NULL,current_episode_id=NULL WHERE rule_id=$1`, id, r.revision+1, due); e != nil {
		return commandEffect{}, e
	}
	action := "alerts.rule.configure"
	if retire {
		action = "alerts.rule.retire"
	}
	e = audit(ctx, tx, actor.ActorID, action, id, map[string]any{"operation_id": operation, "configuration_revision": strconv.FormatInt(r.revision+1, 10)})
	return commandEffect{id, r.revision + 1}, e
}
func (s *Store) Acknowledge(ctx context.Context, r Reader, id ID, in Acknowledge) (CommandReceipt, error) {
	if !validID(id) || !uintBound(in.ExpectedActionRevision, maxInt64Text, true) {
		return CommandReceipt{}, fail("invalid_request")
	}
	body := struct {
		Revision Decimal `json:"expected_action_revision"`
	}{in.ExpectedActionRevision}
	return s.command(ctx, r, in.OperationID, "acknowledge", &id, body, func(ctx context.Context, tx pgx.Tx) (commandEffect, error) {
		var rule string
		if e := tx.QueryRow(ctx, `SELECT rule_id FROM alert_episodes WHERE id=$1`, id).Scan(&rule); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return commandEffect{}, reject("alert_not_found")
			}
			return commandEffect{}, e
		}
		_, runtime, e := lockRule(ctx, tx, rule)
		if e != nil {
			return commandEffect{}, e
		}
		ep, e := lockEpisode(ctx, tx, id)
		if e != nil {
			return commandEffect{}, e
		}
		if ep.rule != rule {
			return commandEffect{}, fail("alert_unavailable")
		}
		if ep.resolved != nil || runtime.episode == nil || *runtime.episode != id {
			return commandEffect{}, reject("alert_not_open")
		}
		if ep.ackBy != nil {
			return commandEffect{}, reject("alert_already_acknowledged")
		}
		if strconv.FormatInt(ep.action, 10) != in.ExpectedActionRevision {
			return commandEffect{}, reject("alert_revision_stale")
		}
		at, e := databaseTime(ctx, tx)
		if e != nil {
			return commandEffect{}, e
		}
		if at.Before(ep.opened) {
			return commandEffect{}, fail("alert_unavailable")
		}
		if _, e = tx.Exec(ctx, `UPDATE alert_episodes SET acknowledged_by=$2,acknowledged_at=$3,acknowledged_command_id=$4 WHERE id=$1`, id, r.ActorID, at, in.OperationID); e != nil {
			return commandEffect{}, e
		}
		if e = event(ctx, tx, id, ep.action+1, "acknowledged", r.ActorID, at, nil, &in.OperationID); e != nil {
			return commandEffect{}, e
		}
		e = audit(ctx, tx, r.ActorID, "alerts.episode.acknowledge", id, map[string]any{"operation_id": in.OperationID, "action_revision": "2"})
		return commandEffect{id, 2}, e
	})
}

var _ Commands = (*Store)(nil)
