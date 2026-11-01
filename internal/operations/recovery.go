package operations

import (
	"context"
	"errors"
	"fmt"
	"github.com/moni-ai/moni-aigate/internal/security"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRecoveryConflict = errors.New("recovery_conflict")
var ErrRecoveryForbidden = errors.New("recovery_forbidden")

type RecoveryGate struct {
	Required    bool       `json:"required"`
	Epoch       int64      `json:"epoch"`
	ProposalID  string     `json:"proposal_id,omitempty"`
	Requester   string     `json:"requester,omitempty"`
	Approver    string     `json:"approver,omitempty"`
	EvidenceRef string     `json:"evidence_ref,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	ProposedAt  *time.Time `json:"proposed_at,omitempty"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
	LatchedAt   *time.Time `json:"latched_at,omitempty"`
}
type RecoveryProposal struct {
	ActorID     string `json:"-"`
	Epoch       int64  `json:"epoch"`
	EvidenceRef string `json:"evidence_ref"`
	Reason      string `json:"reason"`
}
type RecoveryApproval struct {
	ActorID    string `json:"-"`
	Epoch      int64  `json:"epoch"`
	ProposalID string `json:"proposal_id"`
}

const gateColumns = `required,epoch,coalesce(proposal_id,''),coalesce(requester,''),coalesce(approver,''),coalesce(evidence_ref,''),coalesce(reason,''),proposed_at,approved_at,latched_at`

func scanGate(row pgx.Row) (RecoveryGate, error) {
	var g RecoveryGate
	err := row.Scan(&g.Required, &g.Epoch, &g.ProposalID, &g.Requester, &g.Approver, &g.EvidenceRef, &g.Reason, &g.ProposedAt, &g.ApprovedAt, &g.LatchedAt)
	if err == nil {
		for _, stamp := range []*time.Time{g.ProposedAt, g.ApprovedAt, g.LatchedAt} {
			if stamp != nil {
				*stamp = stamp.UTC()
			}
		}
	}
	return g, err
}
func RecoveryRequired(ctx context.Context, p *pgxpool.Pool) (bool, error) {
	var required bool
	err := p.QueryRow(ctx, "SELECT required FROM recovery_gate WHERE singleton").Scan(&required)
	return required, err
}
func RecoveryState(ctx context.Context, p *pgxpool.Pool) (RecoveryGate, error) {
	return scanGate(p.QueryRow(ctx, "SELECT "+gateColumns+" FROM recovery_gate WHERE singleton"))
}
func recoveryRole(ctx context.Context, tx pgx.Tx, actor, expected string) error {
	var role, status string
	err := tx.QueryRow(ctx, "SELECT platform_role,status FROM users WHERE id=$1 FOR SHARE", actor).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRecoveryForbidden
	}
	if err != nil {
		return err
	}
	if role != expected || status != "active" {
		return ErrRecoveryForbidden
	}
	return nil
}
func gateTx(ctx context.Context, p *pgxpool.Pool, fn func(pgx.Tx) (RecoveryGate, error)) (RecoveryGate, error) {
	tx, err := p.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RecoveryGate{}, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='2s';SET LOCAL statement_timeout='5s'"); err != nil {
		return RecoveryGate{}, err
	}
	g, err := fn(tx)
	if err != nil {
		return RecoveryGate{}, err
	}
	return g, tx.Commit(ctx)
}
func ProposeRecovery(ctx context.Context, p *pgxpool.Pool, in RecoveryProposal) (RecoveryGate, error) {
	if in.Epoch <= 0 || !security.ValidReference(in.EvidenceRef, 256) || len(in.Reason) < 1 || len(in.Reason) > 200 {
		return RecoveryGate{}, fmt.Errorf("%w: bounded evidence and reason required", ErrRecoveryConflict)
	}
	return gateTx(ctx, p, func(tx pgx.Tx) (RecoveryGate, error) {
		g, err := scanGate(tx.QueryRow(ctx, "SELECT "+gateColumns+" FROM recovery_gate WHERE singleton FOR UPDATE"))
		if err != nil {
			return g, err
		}
		if err = recoveryRole(ctx, tx, in.ActorID, "finance_operator"); err != nil {
			return g, err
		}
		if !g.Required || g.Epoch != in.Epoch {
			return g, ErrRecoveryConflict
		}
		if g.ProposalID != "" {
			if g.Requester == in.ActorID && g.EvidenceRef == in.EvidenceRef && g.Reason == in.Reason {
				return g, nil
			}
			return g, fmt.Errorf("%w: proposal is frozen", ErrRecoveryConflict)
		}
		if _, err = tx.Exec(ctx, "UPDATE recovery_gate SET proposal_id=$1,requester=$2,evidence_ref=$3,reason=$4,proposed_at=clock_timestamp() WHERE singleton", uuid.NewString(), in.ActorID, in.EvidenceRef, in.Reason); err != nil {
			return g, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES($1,'recovery.proposed',$2,jsonb_build_object('epoch',$3::bigint,'evidence_ref',$4::text,'reason',$5::text))`, in.ActorID, fmt.Sprint(g.Epoch), g.Epoch, in.EvidenceRef, in.Reason); err != nil {
			return g, err
		}
		return scanGate(tx.QueryRow(ctx, "SELECT "+gateColumns+" FROM recovery_gate WHERE singleton"))
	})
}
func ApproveRecovery(ctx context.Context, p *pgxpool.Pool, in RecoveryApproval) (RecoveryGate, error) {
	if in.Epoch <= 0 || in.ProposalID == "" {
		return RecoveryGate{}, ErrRecoveryConflict
	}
	return gateTx(ctx, p, func(tx pgx.Tx) (RecoveryGate, error) {
		g, err := scanGate(tx.QueryRow(ctx, "SELECT "+gateColumns+" FROM recovery_gate WHERE singleton FOR UPDATE"))
		if err != nil {
			return g, err
		}
		if err = recoveryRole(ctx, tx, in.ActorID, "finance_approver"); err != nil {
			return g, err
		}
		if g.Epoch != in.Epoch || g.ProposalID != in.ProposalID || g.Requester == "" {
			return g, ErrRecoveryConflict
		}
		if g.Requester == in.ActorID {
			return g, ErrRecoveryForbidden
		}
		if err = recoveryRole(ctx, tx, g.Requester, "finance_operator"); err != nil {
			return g, err
		}
		if !g.Required {
			return g, nil
		}
		var bad bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ledger_accounts a WHERE a.balance::numeric<>coalesce((SELECT sum(signed_delta::numeric) FROM journal_lines l WHERE l.account_id=a.id),0)) OR EXISTS(SELECT 1 FROM journals j WHERE (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)<2 OR (SELECT coalesce(sum(signed_delta::numeric),0) FROM journal_lines l WHERE l.journal_id=j.id)<>0)`).Scan(&bad); err != nil {
			return g, err
		}
		if bad {
			return g, fmt.Errorf("%w: ledger verification failed", ErrRecoveryConflict)
		}
		if _, err = tx.Exec(ctx, "UPDATE recovery_gate SET required=false,approver=$1,approved_at=clock_timestamp() WHERE singleton", in.ActorID); err != nil {
			return g, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES($1,'recovery.approved',$2,jsonb_build_object('epoch',$3::bigint,'proposal_id',$4::text,'requester',$5::text))`, in.ActorID, in.ProposalID, g.Epoch, in.ProposalID, g.Requester); err != nil {
			return g, err
		}
		return scanGate(tx.QueryRow(ctx, "SELECT "+gateColumns+" FROM recovery_gate WHERE singleton"))
	})
}
