// Package frozenattempt marks a compile-only boundary for historical research
// fixtures whose original scalar input cannot be accepted by current billing.
// This is test support, never an adapter or a source of execution authority.
package frozenattempt

import (
	"context"
	"errors"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

var ErrCurrentBoundary = errors.New("current vector billing rejects frozen scalar attempt input")

type Input struct {
	TenantID, RequestID, ChannelID, ChannelVersionID, CredentialID, UpstreamModel string
	Sequence                                                                      int
	RouteDecisions                                                                []routing.Decision
	CostQuote                                                                     *procurementexecution.CostQuote
}

// Reject does not contact the database, discard a quote, or fabricate a vector.
// Frozen business-oracle acceptance needs its original qualified source/runtime.
func Reject(context.Context, *billing.Store, Input) (billing.Attempt, error) {
	return billing.Attempt{}, ErrCurrentBoundary
}
