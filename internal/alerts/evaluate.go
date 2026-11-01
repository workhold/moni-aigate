package alerts

import (
	"context"
	"errors"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type evaluationFailure struct{ code string }

func (e *evaluationFailure) Error() string { return e.code }

type evaluationPreimage struct {
	id              string
	revision, write int64
}

func observeSource(ctx context.Context, tx pgx.Tx, r dbRule) (Sample, time.Time, error) {
	var exists bool
	var at time.Time
	var sample Sample
	switch r.kind {
	case ChannelQuarantined:
		var enabled, quarantined *bool
		e := tx.QueryRow(ctx, channelSourceSQL, r.scopeID).Scan(&exists, &at, &enabled, &quarantined)
		if e != nil {
			return nil, at, e
		}
		if exists {
			if enabled == nil || quarantined == nil {
				return nil, at, &evaluationFailure{"evaluation_failed"}
			}
			sample = ChannelSample{r.kind, Time{at}, *enabled, *quarantined}
		}
	case ProcurementBudgetLow:
		var state, limit, held, spent, available *string
		e := tx.QueryRow(ctx, budgetSourceSQL, r.scopeID).Scan(&exists, &at, &state, &limit, &held, &spent, &available)
		if e != nil {
			return nil, at, e
		}
		if exists {
			if state == nil || limit == nil || held == nil || spent == nil || available == nil {
				return nil, at, &evaluationFailure{"evaluation_failed"}
			}
			sample = BudgetSample{r.kind, Time{at}, *state, *limit, *held, *spent, *available, "CNY"}
		}
	case CustomerReconciliationAged, ProcurementUnresolvedAged:
		p, e := DecodeParameters(r.kind, r.params)
		if e != nil {
			return nil, at, e
		}
		aging := p.(AgingParameters)
		age, _ := strconv.ParseInt(aging.MinimumAgeSeconds, 10, 64)
		sql := customerSourceSQL
		if r.kind == ProcurementUnresolvedAged {
			sql = procurementSourceSQL
		}
		var count string
		var lower bool
		var oldest *time.Time
		var reference *string
		if e = tx.QueryRow(ctx, sql, r.scopeID, age).Scan(&exists, &at, &count, &lower, &oldest, &reference); e != nil {
			return nil, at, e
		}
		if exists {
			sample = AgingSample{r.kind, Time{at}, aging.MinimumAgeSeconds, count, lower, timePointer(oldest), reference}
		}
	default:
		return nil, at, &evaluationFailure{"evaluation_failed"}
	}
	if !exists {
		return nil, at, &evaluationFailure{"source_missing"}
	}
	if _, e := sample.MarshalJSON(); e != nil {
		return nil, at, &evaluationFailure{"evaluation_failed"}
	}
	return sample, at, nil
}
func decide(r dbRule, s Sample, open bool) (string, error) {
	p, e := DecodeParameters(r.kind, r.params)
	if e != nil {
		return "", e
	}
	switch v := s.(type) {
	case ChannelSample:
		if v.Quarantined {
			return "breach", nil
		}
		return "clear", nil
	case BudgetSample:
		params := p.(BudgetParameters)
		value, _ := new(big.Int).SetString(v.AvailableAmount, 10)
		threshold, _ := new(big.Int).SetString(params.ThresholdAmount, 10)
		recovery, _ := new(big.Int).SetString(params.RecoveryAmount, 10)
		if open {
			if value.Cmp(recovery) > 0 {
				return "clear", nil
			}
			return "hold", nil
		}
		if value.Cmp(threshold) <= 0 {
			return "breach", nil
		}
		return "hold", nil
	case AgingSample:
		params := p.(AgingParameters)
		count, _ := strconv.ParseInt(v.ObservedCount, 10, 64)
		trigger, _ := strconv.ParseInt(params.TriggerCount, 10, 64)
		recovery, _ := strconv.ParseInt(params.RecoveryCount, 10, 64)
		if open {
			if count <= recovery {
				return "clear", nil
			}
			return "hold", nil
		}
		if count >= trigger {
			return "breach", nil
		}
		return "hold", nil
	}
	return "", fail("alert_unavailable")
}
func classifyFailure(e error) string {
	var known *evaluationFailure
	if errors.As(e, &known) {
		return known.code
	}
	var pg *pgconn.PgError
	if errors.As(e, &pg) {
		if pg.Code == "57014" {
			return "source_query_timeout"
		}
		if strings.HasPrefix(pg.Code, "08") {
			return "source_unavailable"
		}
	}
	var connect *pgconn.ConnectError
	if errors.As(e, &connect) || errors.Is(e, io.EOF) || errors.Is(e, io.ErrUnexpectedEOF) {
		return "source_unavailable"
	}
	return "evaluation_failed"
}
func (s *Store) EvaluateDue(ctx context.Context) (EvaluationResult, error) {
	if !s.options.DeploymentEnabled || s.options.RecoveryMode {
		return EvaluationResult{Outcome: "idle"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		result, pre, e := s.evaluateOnce(ctx)
		if e == nil {
			return result, nil
		}
		var unknown *Error
		if errors.As(e, &unknown) && unknown.CommitUnknown {
			return EvaluationResult{}, unknown
		}
		if ctx.Err() != nil {
			return EvaluationResult{}, fail("alert_unavailable")
		}
		if retryable(e) && attempt < 2 {
			continue
		}
		if pre == nil {
			return EvaluationResult{}, safeError(e)
		}
		return s.recordFailure(ctx, *pre, classifyFailure(e))
	}
	return EvaluationResult{}, fail("alert_unavailable")
}
func (s *Store) evaluateOnce(ctx context.Context) (EvaluationResult, *evaluationPreimage, error) {
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		return EvaluationResult{}, nil, e
	}
	defer tx.Rollback(ctx)
	if e = limits(ctx, tx, 2*time.Second, 250*time.Millisecond); e != nil {
		return EvaluationResult{}, nil, e
	}
	var id string
	e = tx.QueryRow(ctx, `SELECT r.id FROM alert_rules r JOIN alert_rule_runtime t ON t.rule_id=r.id WHERE r.state='enabled' AND t.next_evaluation_at<=clock_timestamp() ORDER BY t.next_evaluation_at ASC,r.id COLLATE "C" ASC FOR UPDATE OF r SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		e = commit(tx, ctx)
		return EvaluationResult{Outcome: "idle"}, nil, e
	}
	if e != nil {
		return EvaluationResult{}, nil, e
	}
	r, runtime, e := lockRule(ctx, tx, id)
	if e != nil {
		return EvaluationResult{}, nil, e
	}
	var due bool
	if e = tx.QueryRow(ctx, `SELECT r.state='enabled' AND r.configuration_revision=t.configured_revision AND t.next_evaluation_at<=clock_timestamp() FROM alert_rules r JOIN alert_rule_runtime t ON t.rule_id=r.id WHERE r.id=$1`, id).Scan(&due); e != nil {
		return EvaluationResult{}, nil, e
	}
	if !due {
		e = commit(tx, ctx)
		return EvaluationResult{Outcome: "idle"}, nil, e
	}
	pre := &evaluationPreimage{id, r.revision, runtime.write}
	if runtime.write == math.MaxInt64 {
		return EvaluationResult{}, pre, &evaluationFailure{"counter_exhausted"}
	}
	sourceBudget := 1500 * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline)-250*time.Millisecond < sourceBudget {
		sourceBudget = time.Until(deadline) - 250*time.Millisecond
	}
	if sourceBudget < time.Millisecond {
		return EvaluationResult{}, nil, fail("alert_unavailable")
	}
	if e = limits(ctx, tx, sourceBudget, 250*time.Millisecond); e != nil {
		return EvaluationResult{}, pre, e
	}
	sample, at, e := observeSource(ctx, tx, r)
	if e != nil {
		return EvaluationResult{}, pre, e
	}
	if runtime.success != nil && at.Before(*runtime.success) {
		return EvaluationResult{}, pre, &evaluationFailure{"clock_regression"}
	}
	if e = limits(ctx, tx, 2*time.Second, 250*time.Millisecond); e != nil {
		return EvaluationResult{}, pre, e
	}
	decision, e := decide(r, sample, runtime.episode != nil)
	if e != nil {
		return EvaluationResult{}, pre, e
	}
	raw, e := encode(sample)
	if e != nil {
		return EvaluationResult{}, pre, e
	}
	generation := runtime.generation
	var current any = runtime.episode
	if runtime.episode == nil && decision == "breach" {
		if generation == math.MaxInt64 {
			return EvaluationResult{}, pre, &evaluationFailure{"counter_exhausted"}
		}
		generation++
		episode := uuid.NewString()
		p, e := DecodeParameters(r.kind, r.params)
		if e != nil {
			return EvaluationResult{}, pre, e
		}
		snapshot, e := encode(RuleSnapshot{r.kind, r.scope, r.scopeID, r.label, p})
		if e != nil {
			return EvaluationResult{}, pre, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO alert_episodes(id,rule_id,generation,rule_revision,rule_snapshot,opened_at,initial_sample,last_seen_at,latest_sample,action_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$6,$7,1)`, episode, r.id, generation, r.revision, snapshot, at, raw); e != nil {
			return EvaluationResult{}, pre, e
		}
		if e = event(ctx, tx, episode, 1, "opened", "system", at, raw, nil); e != nil {
			return EvaluationResult{}, pre, e
		}
		if e = audit(ctx, tx, "system", "alerts.episode.opened", episode, map[string]any{"rule_id": r.id, "generation": strconv.FormatInt(generation, 10)}); e != nil {
			return EvaluationResult{}, pre, e
		}
		current = episode
	} else if runtime.episode != nil {
		ep, e := lockEpisode(ctx, tx, *runtime.episode)
		if e != nil {
			return EvaluationResult{}, pre, e
		}
		if ep.rule != r.id || ep.resolved != nil || ep.revision != r.revision {
			return EvaluationResult{}, pre, fail("alert_unavailable")
		}
		if at.Before(ep.seen) || ep.ackAt != nil && at.Before(*ep.ackAt) {
			return EvaluationResult{}, pre, &evaluationFailure{"clock_regression"}
		}
		if decision == "clear" {
			if _, e = tx.Exec(ctx, `UPDATE alert_episodes SET last_seen_at=$2,latest_sample=$3,resolved_at=$2,resolve_reason='recovered' WHERE id=$1`, ep.id, at, raw); e != nil {
				return EvaluationResult{}, pre, e
			}
			if e = event(ctx, tx, ep.id, ep.action+1, "recovered", "system", at, raw, nil); e != nil {
				return EvaluationResult{}, pre, e
			}
			if e = audit(ctx, tx, "system", "alerts.episode.recovered", ep.id, map[string]any{"rule_id": r.id}); e != nil {
				return EvaluationResult{}, pre, e
			}
			current = nil
		} else {
			if _, e = tx.Exec(ctx, `UPDATE alert_episodes SET last_seen_at=$2,latest_sample=$3 WHERE id=$1`, ep.id, at, raw); e != nil {
				return EvaluationResult{}, pre, e
			}
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE alert_rule_runtime SET episode_generation=$2,current_episode_id=$3,last_attempt_at=$4,last_success_at=$4,evaluated_revision=$5,last_sample=$6,last_error_code=NULL,next_evaluation_at=$4::timestamptz+interval '60 seconds' WHERE rule_id=$1`, r.id, generation, current, at, r.revision, raw); e != nil {
		return EvaluationResult{}, pre, e
	}
	e = commit(tx, ctx)
	return EvaluationResult{Outcome: "evaluated"}, pre, e
}
func (s *Store) recordFailure(ctx context.Context, pre evaluationPreimage, code string) (EvaluationResult, error) {
	if ctx.Err() != nil {
		return EvaluationResult{}, fail("alert_unavailable")
	}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		return EvaluationResult{}, safeError(e)
	}
	defer tx.Rollback(ctx)
	if e = limits(ctx, tx, 2*time.Second, 250*time.Millisecond); e != nil {
		return EvaluationResult{}, safeError(e)
	}
	r, runtime, e := lockRule(ctx, tx, pre.id)
	if e != nil {
		return EvaluationResult{}, safeError(e)
	}
	if r.state != "enabled" || r.revision != pre.revision || runtime.configured != pre.revision || runtime.write != pre.write {
		if e = commit(tx, ctx); e != nil {
			return EvaluationResult{}, safeError(e)
		}
		return EvaluationResult{Outcome: "stale_discarded"}, nil
	}
	if runtime.write == math.MaxInt64 {
		return EvaluationResult{}, fail("alert_unavailable")
	}
	at, e := databaseTime(ctx, tx)
	if e != nil {
		return EvaluationResult{}, safeError(e)
	}
	if runtime.attempt != nil && at.Before(*runtime.attempt) {
		code = "clock_regression"
	}
	if _, e = tx.Exec(ctx, `UPDATE alert_rule_runtime SET last_attempt_at=$2,last_error_code=$3,next_evaluation_at=$2::timestamptz+interval '30 seconds' WHERE rule_id=$1`, r.id, at, code); e != nil {
		return EvaluationResult{}, safeError(e)
	}
	if e = commit(tx, ctx); e != nil {
		return EvaluationResult{}, safeError(e)
	}
	return EvaluationResult{Outcome: "failed_recorded", ErrorCode: code}, nil
}

var _ EvaluationRepository = (*Store)(nil)
