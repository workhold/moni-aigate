package billing

import (
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func publishBillingCost(t *testing.T, f *fixture, channel, upstream, basis string, input, output *int64, at time.Time) string {
	t.Helper()
	return meterquote.PublishCost(t, f.ctx, f.pool, meterquote.CostPrice{ChannelID: channel, UpstreamModel: upstream, ModelID: f.model, Actor: f.admin, Basis: basis, InputRate: input, OutputRate: output, EffectiveAt: at})
}
func publishBillingDefault(t *testing.T, f *fixture, model string, input, output int64, at time.Time) string {
	t.Helper()
	return meterquote.PublishDefaultPrice(t, f.ctx, f.pool, meterquote.DefaultPrice{ModelID: model, Actor: f.operator, InputRate: input, OutputRate: output, EffectiveAt: at})
}
func billingRate(v int64) *int64 { return &v }
