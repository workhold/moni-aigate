package frozenattempt

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

func TestCurrentBoundaryIsNotAHistoricalBusinessOracle(t *testing.T) {
	n := int64(0)
	q := procurementexecution.CostQuote{VersionID: "original-text-version", Basis: "contract", InputRate: &n, OutputRate: &n}
	in := Input{TenantID: "tenant", RequestID: "request", ChannelID: "channel", ChannelVersionID: "version", CredentialID: "credential", UpstreamModel: "upstream", Sequence: 1, CostQuote: &q,
		RouteDecisions: []routing.Decision{{ChannelID: "channel", VersionID: "version", Reason: "selected"}}}
	before := in
	before.RouteDecisions = append([]routing.Decision(nil), in.RouteDecisions...)
	// A nil store proves this compile boundary never contacts current billing
	// or fabricates a successful historical write to keep fixtures green.
	got, err := Reject(context.Background(), nil, in)
	var business *billing.Error
	if !errors.Is(err, ErrCurrentBoundary) || errors.As(err, &business) || got != (billing.Attempt{}) || !reflect.DeepEqual(in, before) || q.VersionID != "original-text-version" || n != 0 {
		t.Fatal("compile isolation fabricated business acceptance or modified original input", got, err)
	}
}
