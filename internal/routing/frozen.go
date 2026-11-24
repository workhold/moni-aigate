package routing

import (
	"context"
	"time"

	"github.com/moni-ai/moni-aigate/internal/economics"
)

// FrozenExecution is the safe execution value carried by an admitted attempt.
// It contains references, not credentials, wallet authority, or mutable policy
// maps. The provider still receives only the existing endpoint/request values.
type FrozenExecution struct {
	RequestID         string                    `json:"request_id"`
	OuterAttemptID    string                    `json:"outer_attempt_id,omitempty"`
	Sequence          int                       `json:"sequence"`
	PolicyVersionID   string                    `json:"policy_version_id"`
	BindingID         string                    `json:"binding_id"`
	ChannelID         string                    `json:"channel_id"`
	ChannelVersionID  string                    `json:"channel_version_id"`
	CredentialID      string                    `json:"credential_id"`
	PhysicalModelID   string                    `json:"physical_model_id"`
	CostVersionID     string                    `json:"cost_version_id"`
	QualityEvidenceID string                    `json:"quality_evidence_id"`
	InnerPoolVersion  string                    `json:"inner_pool_version,omitempty"`
	InnerPoolDigest   string                    `json:"inner_pool_digest,omitempty"`
	RemainingDeadline time.Time                 `json:"remaining_deadline"`
	ExecutionPolicy   economics.ExecutionPolicy `json:"execution_policy"`
}

func frozenExecution(ctx context.Context, plan *Plan, candidate Candidate, sequence int) *FrozenExecution {
	if plan.policy.Execution == nil {
		return nil
	}
	frozen := &FrozenExecution{
		RequestID: plan.request.RequestID, Sequence: sequence,
		PolicyVersionID: candidate.PolicyVersionID, BindingID: candidate.BindingID,
		ChannelID: candidate.ChannelID, ChannelVersionID: candidate.VersionID,
		CredentialID: candidate.CredentialID, PhysicalModelID: candidate.PhysicalModelID,
		CostVersionID: candidate.CostQuote.VersionID(), QualityEvidenceID: candidate.Quality.EvidenceID,
		ExecutionPolicy: *plan.policy.Execution,
	}
	if deadline, ok := ctx.Deadline(); ok {
		frozen.RemainingDeadline = deadline
	}
	return frozen
}
