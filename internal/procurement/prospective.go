package procurement

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
)

// ProspectiveOptions is trusted assembly configuration. It does not activate
// the candidate in ordinary New, main, migrations, or provision.
type ProspectiveOptions struct{ Schema string }

type accountRunner interface {
	RunAccount(context.Context, procurementexecution.Invocation) (procurementexecution.AccountReceipt, error)
}

type prospectiveAccount struct {
	schema string
	runner accountRunner
}

func NewProspective(pool *pgxpool.Pool, options ProspectiveOptions) (*Store, error) {
	if _, err := procurementexecution.NewCreateAccount(options.Schema, procurementexecution.AccountInput{}); err != nil {
		return nil, err
	}
	runner, err := procurementexecution.NewRunner(pool)
	if err != nil {
		return nil, err
	}
	s := New(pool)
	s.prospective = &prospectiveAccount{schema: options.Schema, runner: runner}
	return s, nil
}

type prospectiveFailure struct {
	sentinel error
	cause    error
}

func (e *prospectiveFailure) Error() string        { return e.sentinel.Error() }
func (e *prospectiveFailure) Unwrap() error        { return e.cause }
func (e *prospectiveFailure) Is(target error) bool { return target == e.sentinel }

func prospectiveError(err error) error {
	var unknown *procurementexecution.UnknownOutcome
	if errors.As(err, &unknown) {
		return err
	}
	var failure *procurementexecution.Failure
	if !errors.As(err, &failure) || failure.Domain() != procurementexecution.DomainProcurement {
		return err
	}
	var sentinel error
	switch failure.SQLState() {
	case "PP001":
		sentinel = ErrInvalid
	case "PP002":
		sentinel = ErrConflict
	case "PP003":
		sentinel = ErrForbidden
	default:
		return err
	}
	return &prospectiveFailure{sentinel: sentinel, cause: err}
}

func (p *prospectiveAccount) run(ctx context.Context, invocation procurementexecution.Invocation, err error) (Command, error) {
	if err != nil {
		return Command{}, prospectiveError(err)
	}
	receipt, err := p.runner.RunAccount(ctx, invocation)
	if err != nil {
		return Command{}, prospectiveError(err)
	}
	return Command{
		OperationID: receipt.OperationID, Actor: receipt.Actor, Kind: receipt.Kind,
		TargetID: receipt.TargetID, Status: receipt.Status, Reason: receipt.Reason,
		Before: append([]byte(nil), receipt.Before...), After: append([]byte(nil), receipt.After...), CreatedAt: receipt.CreatedAt,
	}, nil
}

func (p *prospectiveAccount) create(ctx context.Context, actor string, in CreateInput) (Command, error) {
	if !canonicalID(actor) || !canonicalID(in.OperationID) {
		return Command{}, ErrInvalid
	}
	limit, _ := strconv.ParseInt(in.LimitAmount, 10, 64) // original configuration validator already succeeded
	invocation, err := procurementexecution.NewCreateAccount(p.schema, procurementexecution.AccountInput{
		ActorID: actor, OperationID: in.OperationID, Label: in.Label, Provider: in.Provider, State: in.State, LimitAmount: limit,
	})
	return p.run(ctx, invocation, err)
}

func (p *prospectiveAccount) configure(ctx context.Context, actor, id string, in ConfigurationInput) (Command, error) {
	if !canonicalID(actor) || !canonicalID(in.OperationID) {
		return Command{}, ErrInvalid
	}
	limit, _ := strconv.ParseInt(in.LimitAmount, 10, 64)
	expected, _ := strconv.ParseInt(in.ExpectedRevision, 10, 64)
	invocation, err := procurementexecution.NewConfigureAccount(p.schema, procurementexecution.ConfigurationInput{
		AccountInput: procurementexecution.AccountInput{ActorID: actor, OperationID: in.OperationID, Label: in.Label, Provider: in.Provider, State: in.State, LimitAmount: limit},
		AccountID:    id, ExpectedRevision: expected,
	})
	return p.run(ctx, invocation, err)
}
