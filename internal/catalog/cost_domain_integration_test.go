package catalog_test

import (
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func TestCostQuoteSchemaDomainIsolation(t *testing.T) {
	f := credentialPoolDB(t, true)
	f.publish(t, 0)
	at := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	first := costInput(t, f, "upstream", &at)
	want, err := f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, first)
	if err != nil {
		t.Fatal(err)
	}
	other := *f
	other.model, err = f.store.CreateModel(f.ctx, "pool-admin", catalog.ModelInput{PublicName: "other-schema", ContextLimit: 120, InputUpperBound: 90, OutputLimit: 30, DefaultOutputLimit: 30})
	if err != nil {
		t.Fatal(err)
	}
	other.publish(t, 0)
	if got := costCandidate(t, &other, 1).CostQuote; got.Known() || got.VersionID() != "" {
		t.Fatal("another schema filled a missing quote", got)
	}
	second := costInput(t, &other, "upstream", &at)
	second.InputRate, second.OutputRate = costRate("0"), costRate("0")
	zero, err := f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, second)
	if err != nil {
		t.Fatal("same instant in a different schema must be independent", err)
	}
	if first.SchemaID == second.SchemaID || zero.Result.SchemaID != second.SchemaID || zero.Result.SchemaDigest != second.SchemaDigest || zero.Result.VariantKey != "default" {
		t.Fatal("explicit schema publication identity changed", first, second, zero)
	}
	if got := costCandidate(t, &other, 1).CostQuote; !got.Known() || got.VersionID() != zero.Result.ID || !quoteRatesEqual(got, 0, 0) {
		t.Fatal("known zero quote was not retained in its own domain", got)
	}
	now := time.Now().Add(-time.Minute)
	unknown := costInput(t, &other, "upstream", &now)
	unknown.Basis, unknown.InputRate, unknown.OutputRate = "unknown", nil, nil
	missing, err := f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, unknown)
	if err != nil {
		t.Fatal(err)
	}
	if got := costCandidate(t, &other, 1).CostQuote; got.Known() || got.VersionID() != missing.Result.ID || got.Basis() != "unknown" {
		t.Fatal("explicit unknown lost its original identity", got)
	}
	if got := costCandidate(t, f, 1).CostQuote; !got.Known() || got.VersionID() != want.Result.ID || !quoteRatesEqual(got, 1000001, 2000003) {
		t.Fatal("latest foreign capability/zero/unknown replaced the original schema quote", got)
	}
	before := costState(t, f)
	if _, err = f.app.Exec(f.ctx, `INSERT INTO route_policy_versions(model_id,revision,profile_version) SELECT $1,2,profile_version FROM models WHERE id=$2`, f.model, other.model); err == nil || !strings.Contains(err.Error(), "route profile does not match model") || costState(t, f) != before {
		t.Fatal("route accepted another model's profile", err)
	}
	if _, err = f.app.Exec(f.ctx, `UPDATE models SET active_revision=999 WHERE id=$1`, f.model); err == nil || !strings.Contains(err.Error(), "active route does not match model profile") {
		t.Fatal("model accepted a non-existent active route", err)
	}
}

func TestCostQuoteRejectsUnpublishedSchemaAndInputOnlyOutput(t *testing.T) {
	f := credentialPoolDB(t, true)
	other := *f
	var err error
	other.model, err = f.store.CreateModel(f.ctx, "pool-admin", catalog.ModelInput{PublicName: "input-only", Protocol: "openai_embeddings", ContextLimit: 100, InputUpperBound: 100})
	if err != nil {
		t.Fatal(err)
	}
	in := costInput(t, &other, "upstream", nil)
	before := costState(t, f)
	_, err = f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, in)
	if err == nil || !strings.Contains(err.Error(), "invalid_price") || costState(t, f) != before {
		t.Fatal("input-only nonzero output publication changed state", err)
	}
	_, err = meterquote.TryPublishCost(f.ctx, f.app, meterquote.CostPrice{ChannelID: f.channel, UpstreamModel: "raw", ModelID: other.model, SchemaID: in.SchemaID, Actor: poolPricingActorID, Basis: "contract", InputRate: costRateInt(1), OutputRate: costRateInt(1)})
	if err == nil || !strings.Contains(err.Error(), "input-only cost requires zero output rate") || costState(t, f) != before {
		t.Fatal("raw input-only quote bypassed output-zero guard", err)
	}
	in.OutputRate = costRate("0")
	if _, err = f.store.PublishCostPrice(f.ctx, catalog.PricingActor{UserID: poolPricingActorID}, in); err != nil {
		t.Fatal("complete input-only quote rejected", err)
	}
	var sid string
	if err = f.app.QueryRow(f.ctx, `SELECT meter_text_schema(77,13)`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	before = costState(t, f)
	_, err = meterquote.TryPublishCost(f.ctx, f.app, meterquote.CostPrice{ChannelID: f.channel, UpstreamModel: "raw", ModelID: f.model, SchemaID: sid, Actor: poolPricingActorID, Basis: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "published text schema") {
		t.Fatal("unpublished schema accepted", err)
	}
	if costState(t, f) != before {
		t.Fatal("rejected quote left channel revision, cost or audit")
	}
}

func costRateInt(value int64) *int64 { return &value }
