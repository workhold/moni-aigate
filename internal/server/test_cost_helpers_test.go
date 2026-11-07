package server

import (
	"context"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func publishCostFixture(t *testing.T, f *gatewayFixture, channel, upstream, basis string, input, output *int64, at time.Time) string {
	t.Helper()
	var actor string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE status='active' ORDER BY id LIMIT 1`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	return meterquote.PublishCost(t, context.Background(), f.pool, meterquote.CostPrice{ChannelID: channel, UpstreamModel: upstream, ModelID: f.model, Actor: actor, Basis: basis, InputRate: input, OutputRate: output, EffectiveAt: at})
}
func costRate(v int64) *int64 { return &v }
func publishDefaultFixture(t *testing.T, f *gatewayFixture, model, actor string, input, output int64, at time.Time) string {
	t.Helper()
	return meterquote.PublishDefaultPrice(t, context.Background(), f.pool, meterquote.DefaultPrice{ModelID: model, Actor: actor, InputRate: input, OutputRate: output, EffectiveAt: at})
}
