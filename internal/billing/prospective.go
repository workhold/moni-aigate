package billing

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
)

// ProspectiveOptions is trusted assembly configuration, never request input.
// Construction does not probe, install, or qualify the candidate SQL profile.
type ProspectiveOptions struct{ Schema string }

type attemptRunner interface {
	RunAttempt(context.Context, procurementexecution.Invocation) (procurementexecution.AttemptReceipt, error)
}

type prospectiveAttempt struct {
	schema string
	runner attemptRunner
}

func NewProspective(pool *pgxpool.Pool, options ProspectiveOptions) (*Store, error) {
	if _, err := procurementexecution.NewRecordAttempt(options.Schema, procurementexecution.AttemptInput{}); err != nil {
		return nil, err
	}
	runner, err := procurementexecution.NewRunner(pool)
	if err != nil {
		return nil, err
	}
	s := New(pool)
	s.prospective = &prospectiveAttempt{schema: options.Schema, runner: runner}
	return s, nil
}

type prospectiveFailure struct {
	public *Error
	cause  error
}

func (e *prospectiveFailure) Error() string { return e.public.Error() }
func (e *prospectiveFailure) Unwrap() error { return e.cause }
func (e *prospectiveFailure) As(target any) bool {
	if out, ok := target.(**Error); ok {
		*out = e.public
		return true
	}
	return false
}

func prospectiveError(err error) error {
	var unknown *procurementexecution.UnknownOutcome
	if errors.As(err, &unknown) {
		return err
	}
	var failure *procurementexecution.Failure
	if !errors.As(err, &failure) || failure.Domain() != procurementexecution.DomainBilling {
		return err
	}
	return &prospectiveFailure{public: &Error{Code: failure.PublicCode(), Message: failure.PublicMessage()}, cause: err}
}

// The original outer validators have already run. In particular, PP101's
// fixed credential-membership classification relies on that precondition.
func (p *prospectiveAttempt) record(ctx context.Context, in AttemptInput, decisions []byte) (Attempt, error) {
	input := procurementexecution.AttemptInput{
		TenantID: in.TenantID, RequestID: in.RequestID, Sequence: int32(in.Sequence),
		ChannelID: in.ChannelID, ChannelVersionID: in.ChannelVersionID,
		CredentialID: in.CredentialID, UpstreamModel: in.UpstreamModel, RouteDecisions: decisions,
	}
	if in.CostQuote != nil {
		q := in.CostQuote
		input.CostQuote = &procurementexecution.CostQuote{VersionID: q.VersionID, Basis: q.Basis, InputRate: q.InputRate, OutputRate: q.OutputRate}
	}
	invocation, err := procurementexecution.NewRecordAttempt(p.schema, input)
	if err != nil {
		return Attempt{}, prospectiveError(err)
	}
	receipt, err := p.runner.RunAttempt(ctx, invocation)
	if err != nil {
		return Attempt{}, prospectiveError(err)
	}
	return Attempt{ID: receipt.AttemptID, RequestID: receipt.RequestID, Sequence: int(receipt.Sequence), DeliveryState: receipt.DeliveryState, CostStatus: receipt.CostStatus}, nil
}
