package procurementexecution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ObservationState string
type ObservationReason string

const (
	ReceiptPresent ObservationState = "receipt_present"
	Unresolved     ObservationState = "unresolved"

	ReasonInvalidHandle            ObservationReason = "invalid_handle"
	ReasonInvalidContext           ObservationReason = "invalid_context"
	ReasonIdentityUnavailable      ObservationReason = "identity_unavailable"
	ReasonReceiptAbsent            ObservationReason = "receipt_absent"
	ReasonReceiptMismatch          ObservationReason = "receipt_mismatch"
	ReasonReceiptIncomplete        ObservationReason = "receipt_incomplete"
	ReasonHistoryInvariant         ObservationReason = "history_invariant"
	ReasonUnsupportedHistory       ObservationReason = "unsupported_history"
	ReasonObservationProtocol      ObservationReason = "observation_protocol"
	ReasonObservationTimeout       ObservationReason = "observation_timeout"
	ReasonObservationUnavailable   ObservationReason = "observation_unavailable"
	ReasonObservationCleanupFailed ObservationReason = "observation_cleanup_failed"
	ReasonObservationPanicked      ObservationReason = "observation_panicked"
)

// Observation describes presence in one snapshot. It is neither a mutation
// receipt nor permission to dispatch, retry, or issue a new operation.
type Observation struct {
	State  ObservationState
	Reason ObservationReason
}

type ObservationError struct{ cause error }

func (*ObservationError) Error() string      { return "procurement_observation_unresolved" }
func (*ObservationError) PublicCode() string { return "procurement_observation_unresolved" }
func (e *ObservationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
func (ObservationError) String() string   { return "procurement_observation_unresolved" }
func (ObservationError) GoString() string { return "procurement_observation_unresolved" }
func (ObservationError) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "procurement_observation_unresolved")
}
func (ObservationError) MarshalJSON() ([]byte, error) {
	return []byte(`"procurement_observation_unresolved"`), nil
}
func (ObservationError) MarshalText() ([]byte, error) {
	return []byte("procurement_observation_unresolved"), nil
}

type observationProblem struct {
	reason ObservationReason
	cause  error
}

func (*observationProblem) Error() string   { return "procurement_observation_unresolved" }
func (e *observationProblem) Unwrap() error { return e.cause }
func observerCheck(ok bool, reason ObservationReason) error {
	if ok {
		return nil
	}
	return &observationProblem{reason: reason}
}
func observerProtocol(cause error) error {
	return &observationProblem{reason: ReasonObservationProtocol, cause: cause}
}

// Classification uses only this probe's primary error, before the old COMMIT
// cause is appended. A historic PP902/context cannot classify a new failure.
func observationFailureReason(err error) ObservationReason {
	var local *observationProblem
	if errors.As(err, &local) {
		return local.reason
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "PP902":
			return ReasonHistoryInvariant
		case "PP906":
			return ReasonUnsupportedHistory
		case "57014":
			return ReasonObservationTimeout
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ReasonObservationTimeout
	}
	return ReasonObservationUnavailable
}

func observationUnresolved(reason ObservationReason, probe, commit error) (Observation, error) {
	return Observation{State: Unresolved, Reason: reason}, &ObservationError{cause: combine(probe, commit)}
}

func observationInvocationValid(i Invocation) bool {
	switch i.root {
	case CreateAccount:
		want, _ := NewCreateAccount("_", i.account)
		return reflect.DeepEqual(i.args, want.args)
	case ConfigureAccount:
		want, _ := NewConfigureAccount("_", ConfigurationInput{AccountInput: i.account, AccountID: i.target, ExpectedRevision: i.expected})
		return reflect.DeepEqual(i.args, want.args)
	case RecordAttempt:
		if len(i.args) != 13 {
			return false
		}
		for _, n := range []int{0, 1, 3, 4, 5, 6} {
			if _, ok := i.args[n].(string); !ok {
				return false
			}
		}
		seq, ok := i.args[2].(int32)
		if !ok || seq != i.sequence || i.args[1] != i.request {
			return false
		}
		if _, ok := i.args[7].([]byte); !ok {
			return false
		}
		has, ok := i.args[8].(bool)
		if !ok {
			return false
		}
		if !has {
			return i.args[9] == nil && i.args[10] == nil && i.args[11] == nil && i.args[12] == nil
		}
		for _, n := range []int{9, 12} {
			b, ok := i.args[n].([]byte)
			if !ok || b == nil {
				return false
			}
		}
		for _, n := range []int{10, 11} {
			if i.args[n] != nil {
				if _, ok := i.args[n].(int64); !ok {
					return false
				}
			}
		}
		return true
	}
	return false
}

func observationIdentityValid(h *ObservationHandle) bool {
	i := h.invocation
	if i.root == RecordAttempt {
		a := h.attempt
		if a.RequestID != i.request || a.Sequence != i.sequence || !oneOf(a.DeliveryState, "prepared", "sending", "completed", "failed", "unknown") {
			return false
		}
		if a.Outcome == "replay" {
			return a.RootID == nil && a.SourceID == nil
		}
		return a.Outcome == "applied" && a.RootID != nil && a.SourceID != nil && canonicalID(*a.RootID, true) && canonicalID(a.AttemptID, true) && *a.SourceID == a.AttemptID && a.DeliveryState == "prepared" && a.CostStatus == "unknown"
	}
	a := h.account
	kind := "account_create"
	if i.root == ConfigureAccount {
		kind = "account_configure"
	}
	if !oneOf(a.Outcome, "applied", "replay") || a.OperationID != i.account.OperationID || a.Actor != i.account.ActorID || a.Kind != kind || !canonicalID(a.RootID, true) || a.Before == nil || a.After == nil || a.CreatedAt.Year() < 1 || a.CreatedAt.Year() > 9999 || a.CreatedAt.Nanosecond()%1000 != 0 {
		return false
	}
	if i.root == CreateAccount && !canonicalID(a.TargetID, true) || i.root == ConfigureAccount && a.TargetID != i.target {
		return false
	}
	_, bn, be := decodeSnapshot(a.Before)
	_, an, ae := decodeSnapshot(a.After)
	if be != nil || ae != nil {
		return false
	}
	if a.Status == "rejected" && a.Reason == "not_found" {
		return i.root == ConfigureAccount && bn && an && a.AccountJournalID == nil
	}
	if a.AccountJournalID == nil || !canonicalID(*a.AccountJournalID, true) {
		return false
	}
	return a.Status == "applied" && a.Reason == "" && !an && (i.root != CreateAccount || bn) || i.root == ConfigureAccount && a.Status == "rejected" && a.Reason == "revision_conflict" && !bn && an
}

// Observe starts one fresh, bounded RR READ ONLY transaction. It never runs a
// mutation root, commits, polls, retries, or interprets presence as permission.
func (r *Runner) Observe(ctx context.Context, h *ObservationHandle) (Observation, error) {
	if ctx == nil {
		return observationUnresolved(ReasonInvalidContext, nil, nil)
	}
	if !r.available() || h == nil || h.issuer != r || h.stamp != r.stamp {
		return observationUnresolved(ReasonInvalidHandle, nil, nil)
	}
	schema, ok := invocationSchema(h.invocation)
	if !ok || schema != h.schema || !observationInvocationValid(h.invocation) {
		return observationUnresolved(ReasonInvalidHandle, nil, nil)
	}
	if !observationIdentityValid(h) {
		return observationUnresolved(ReasonIdentityUnavailable, nil, h.commitCause)
	}
	work, cancel := context.WithTimeout(ctx, 5*time.Second)
	return r.observe(work, cancel, h)
}

func (r *Runner) observe(work context.Context, cancel context.CancelFunc, h *ObservationHandle) (result Observation, err error) {
	var conn runnerConn
	var tx runnerTx
	var probeErr error
	var forcedReason ObservationReason
	defer func() {
		if recover() != nil {
			probeErr = ErrRunnerPanicked
			forcedReason = ReasonObservationPanicked
		}
		cancel()
		if conn != nil {
			cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			destroy := false
			if tx != nil {
				rb := safeRunnerRollback(tx, cleanup)
				if rb != nil || !safeRunnerIdle(conn) {
					probeErr = combine(probeErr, rb)
					forcedReason = ReasonObservationCleanupFailed
					destroy = true
				}
			} else {
				destroy = !safeRunnerIdle(conn)
			}
			if destroy {
				if closeErr := safeRunnerDestroy(conn, cleanup); closeErr != nil {
					probeErr = combine(probeErr, closeErr)
					forcedReason = ReasonObservationCleanupFailed
				}
			} else {
				// Release transfers ownership before user callbacks. No later
				// state read, Hijack or Close is legal even if a hook panics.
				if releaseErr := safeRunnerRelease(conn); releaseErr != nil {
					probeErr = combine(probeErr, releaseErr)
					forcedReason = ReasonObservationCleanupFailed
				}
			}
		}
		if forcedReason != "" {
			result, err = observationUnresolved(forcedReason, probeErr, h.commitCause)
			return
		}
		if probeErr != nil {
			result, err = observationUnresolved(observationFailureReason(probeErr), probeErr, h.commitCause)
			return
		}
		result = Observation{State: ReceiptPresent}
		err = nil
	}()
	if probeErr = work.Err(); probeErr != nil {
		return
	}
	conn, probeErr = r.pool.acquire(work)
	if probeErr != nil {
		return
	}
	if conn == nil {
		probeErr = ErrRunnerUnavailable
		return
	}
	tx, probeErr = conn.begin(work, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if probeErr != nil {
		return
	}
	if tx == nil {
		probeErr = observerProtocol(nil)
		return
	}
	if probeErr = tx.exec(work, runnerSettingsSQL); probeErr != nil {
		return
	}
	p := &observationProbe{ctx: work, tx: tx, schema: h.schema}
	if h.invocation.root == RecordAttempt {
		probeErr = p.attempt(h)
	} else {
		probeErr = p.account(h)
	}
	if probeErr == nil {
		probeErr = work.Err()
	}
	return
}
