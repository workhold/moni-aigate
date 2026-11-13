package catalog_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func TestPhase1GroupMultiplierCompilesImmutablePrices(t *testing.T) {
	f := publicDB(t)
	baseID := f.price(t, f.model, 1_000_000, 2_000_000, -time.Hour)
	group, err := f.store.CreatePriceGroup(f.ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupCreateCommand{
		OperationID: uuid.NewString(), Label: "phase1-compile",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := catalog.GroupPriceCompileCommand{
		OperationID: uuid.NewString(), GroupID: group.Result.ID, Multiplier: "0.85",
		ModelIDs: []string{f.model}, EffectiveAt: time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond),
		ExpectedBaseVersionIDs: map[string]string{f.model: baseID},
	}
	first, err := f.store.CompileGroupPrices(f.ctx, catalog.PricingActor{UserID: f.admin}, command)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || len(first.Result.Items) != 1 {
		t.Fatalf("unexpected first compile receipt: %+v", first)
	}
	item := first.Result.Items[0]
	if item.BasePriceVersionID != baseID || item.BaseInputRate != "1000000" || item.BaseOutputRate != "2000000" || item.InputRate != "850000" || item.OutputRate != "1700000" || item.PriceVersionID == nil {
		t.Fatalf("compiled vector mismatch: %+v", item)
	}
	var kind string
	var priceCount int
	if err = f.owner.QueryRow(f.ctx, `SELECT kind FROM pricing_operations WHERE operation_id=$1`, command.OperationID).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM price_versions WHERE pricing_operation_id=$1`, command.OperationID).Scan(&priceCount); err != nil {
		t.Fatal(err)
	}
	if kind != "group_price.compile" || priceCount != 1 {
		t.Fatalf("compile operation did not publish one immutable price: kind=%s count=%d", kind, priceCount)
	}
	replayed, err := f.store.CompileGroupPrices(f.ctx, catalog.PricingActor{UserID: f.admin}, command)
	if err != nil || !replayed.Replayed {
		t.Fatalf("compile replay failed: %+v/%v", replayed, err)
	}
	if got, want := replayed.Result.Items[0].PriceVersionID, item.PriceVersionID; got == nil || want == nil || *got != *want {
		t.Fatalf("compile replay changed price identity: got=%v want=%v", got, want)
	}
	previewCommand := command
	previewCommand.OperationID = uuid.NewString()
	preview, err := f.store.PreviewGroupPriceCompile(f.ctx, f.admin, previewCommand)
	if err != nil || len(preview.Items) != 1 || preview.Items[0].PriceVersionID != nil {
		t.Fatalf("preview unexpectedly published or failed: %+v/%v", preview, err)
	}
	var safe json.RawMessage
	if err = f.owner.QueryRow(f.ctx, `SELECT safe_input FROM pricing_operations WHERE operation_id=$1`, command.OperationID).Scan(&safe); err != nil {
		t.Fatal(err)
	}
	if string(safe) == "" || string(safe) == "null" {
		t.Fatal("compile safe input was not persisted")
	}
}

func TestPhase1ModelAliasOperationLifecycle(t *testing.T) {
	f := publicDB(t)
	f.publish(t, 0)
	alias := "phase1-alias-" + uuid.NewString()[:8]
	create := catalog.ModelAliasCreateInput{OperationID: uuid.NewString(), Alias: alias}
	created, err := f.store.CreateModelAlias(f.ctx, f.admin, f.model, create)
	if err != nil {
		t.Fatal(err)
	}
	if created.Alias != alias || created.ModelID != f.model || created.State != "active" || created.Revision != "1" {
		t.Fatalf("unexpected alias create result: %+v", created)
	}
	replay, err := f.store.CreateModelAlias(f.ctx, f.admin, f.model, create)
	if err != nil || replay.OperationID != created.OperationID || replay.CreatedAt != created.CreatedAt {
		t.Fatalf("alias create replay failed: %+v/%v", replay, err)
	}
	aliases, err := f.store.ModelAliases(f.ctx, f.model)
	if err != nil || len(aliases) != 1 || aliases[0].Alias != alias {
		t.Fatalf("alias listing failed: %+v/%v", aliases, err)
	}
	retire := catalog.ModelAliasRetireInput{OperationID: uuid.NewString(), Alias: alias, ExpectedRevision: 1}
	retired, err := f.store.RetireModelAlias(f.ctx, f.admin, retire)
	if err != nil {
		t.Fatal(err)
	}
	if retired.State != "retired" || retired.Revision != "2" || retired.RetiredAt == nil {
		t.Fatalf("unexpected alias retire result: %+v", retired)
	}
	retiredReplay, err := f.store.RetireModelAlias(f.ctx, f.admin, retire)
	if err != nil || retiredReplay.Revision != retired.Revision || retiredReplay.RetiredAt == nil {
		t.Fatalf("alias retire replay failed: %+v/%v", retiredReplay, err)
	}
	active, err := f.store.ActiveModelAliases(f.ctx, []string{f.model})
	if err != nil || len(active[f.model]) != 0 {
		t.Fatalf("retired alias remained active: %+v/%v", active, err)
	}
	var createAudit, retireAudit int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FILTER (WHERE action='model_alias.create'),count(*) FILTER (WHERE action='model_alias.retire') FROM audit_events WHERE target=$1 OR target=$2`, alias, alias).Scan(&createAudit, &retireAudit); err != nil {
		t.Fatal(err)
	}
	if createAudit != 1 || retireAudit != 1 {
		t.Fatalf("alias audit was not unique: create=%d retire=%d", createAudit, retireAudit)
	}
}
