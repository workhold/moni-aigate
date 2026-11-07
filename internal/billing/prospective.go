package billing

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

// ProspectiveOptions is trusted assembly configuration, never request input.
// Construction does not probe, install, or qualify the candidate SQL profile.
type ProspectiveOptions struct{ Schema string }

type attemptRunner interface {
	RunAttempt(context.Context, procurementexecution.Invocation) (procurementexecution.AttemptReceipt, error)
}

// ProspectiveStore is the explicit frozen 022 adapter, not a mode of Store.
// It deliberately has no embedding or vector-input conversion/fallback.
type ProspectiveStore struct {
	pool   *pgxpool.Pool
	schema string
	runner attemptRunner
}

type FrozenAttemptInput struct {
	TenantID, RequestID, ChannelID, ChannelVersionID, CredentialID, UpstreamModel string
	Sequence                                                                      int
	RouteDecisions                                                                []routing.Decision
	CostQuote                                                                     *procurementexecution.CostQuote
}

func NewProspective(pool *pgxpool.Pool, options ProspectiveOptions) (*ProspectiveStore, error) {
	if _, err := procurementexecution.NewRecordAttempt(options.Schema, procurementexecution.AttemptInput{}); err != nil {
		return nil, err
	}
	runner, err := procurementexecution.NewRunner(pool)
	if err != nil {
		return nil, err
	}
	return &ProspectiveStore{pool: pool, schema: options.Schema, runner: runner}, nil
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
func (p *ProspectiveStore) RecordAttempt(ctx context.Context, in FrozenAttemptInput) (Attempt, error) {
	if in.Sequence < 1 || in.Sequence > 2 || !boundedString(in.UpstreamModel, 200) {
		return Attempt{}, fail("invalid_attempt", "invalid attempt input")
	}
	decisions, err := validateRouteDecisions(in.ChannelID, in.ChannelVersionID, in.RouteDecisions)
	if err != nil {
		return Attempt{}, err
	}
	input := procurementexecution.AttemptInput{
		TenantID: in.TenantID, RequestID: in.RequestID, Sequence: int32(in.Sequence),
		ChannelID: in.ChannelID, ChannelVersionID: in.ChannelVersionID,
		CredentialID: in.CredentialID, UpstreamModel: in.UpstreamModel, RouteDecisions: decisions,
	}
	if in.CostQuote != nil {
		input.CostQuote = in.CostQuote
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
