package billing

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func coinDisplayCommand(actor string, revision int64) CoinDisplayInput {
	return CoinDisplayInput{OperationID: uuid.NewString(), ActorID: actor, ExpectedRevision: revision, DisplayName: "点数", FiatCurrency: "CNY", RateNum: 7, RateDen: 10, Reason: "console display review", Confirmation: CoinDisplayConfirmation}
}

func TestPostgresCoinDisplayAppendReplayCASConcurrencyAndPublicView(t *testing.T) {
	f := fundingFixture(t)
	current, err := f.store.CoinDisplayCurrent(f.ctx)
	if err != nil || current.Version != 1 || current.DisplayName != "币" || current.FiatCurrency != nil || current.RateNum != nil || current.RateDen != nil {
		t.Fatalf("display genesis: %+v %v", current, err)
	}
	if _, err = f.store.CoinDisplaySettings(f.ctx, f.operator); err == nil {
		t.Fatal("non-admin read display history")
	}
	command := coinDisplayCommand(f.admin, 1)
	updated, err := f.store.UpdateCoinDisplay(f.ctx, command)
	if err != nil || updated.Version != 2 || updated.DisplayName != "点数" || updated.FiatCurrency == nil || *updated.FiatCurrency != "CNY" || updated.RateNum == nil || *updated.RateNum != "7" || updated.RateDen == nil || *updated.RateDen != "10" {
		t.Fatalf("display append: %+v %v", updated, err)
	}
	replayed, err := f.store.UpdateCoinDisplay(f.ctx, command)
	if err != nil || !reflect.DeepEqual(updated, replayed) {
		t.Fatalf("display replay changed the result: %+v %+v %v", updated, replayed, err)
	}
	operation, err := f.store.CoinDisplayOperation(f.ctx, f.admin, command.OperationID)
	if err != nil || !reflect.DeepEqual(updated, operation) {
		t.Fatalf("display operation readback: %+v %v", operation, err)
	}
	if _, err = f.store.CoinDisplayOperation(f.ctx, f.operator, command.OperationID); err == nil {
		t.Fatal("non-admin read display operation")
	}
	changed := command
	changed.Reason = "different intent"
	if _, err = f.store.UpdateCoinDisplay(f.ctx, changed); err == nil {
		t.Fatal("display operation accepted different content")
	} else {
		assertCode(t, err, "idempotency_conflict")
	}
	stale := coinDisplayCommand(f.admin, 1)
	if _, err = f.store.UpdateCoinDisplay(f.ctx, stale); err == nil {
		t.Fatal("stale display revision was accepted")
	} else {
		assertCode(t, err, "revision_conflict")
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, name := range []string{"积分", "能量"} {
		name := name
		go func() {
			<-start
			command := coinDisplayCommand(f.admin, 2)
			command.DisplayName = name
			_, err := f.store.UpdateCoinDisplay(f.ctx, command)
			results <- err
		}()
	}
	close(start)
	succeeded, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			succeeded++
		} else {
			assertCode(t, err, "revision_conflict")
			conflicts++
		}
	}
	if succeeded != 1 || conflicts != 1 {
		t.Fatalf("display concurrent CAS: success=%d conflicts=%d", succeeded, conflicts)
	}
	view, err := f.store.CoinDisplaySettings(f.ctx, f.admin)
	if err != nil || view.Current.Version != 3 || len(view.Versions) != 3 {
		t.Fatalf("display versions after CAS: %+v %v", view, err)
	}
	disable := coinDisplayCommand(f.admin, 3)
	disable.DisplayName, disable.FiatCurrency, disable.RateNum, disable.RateDen = "币", "", 0, 0
	disabled, err := f.store.UpdateCoinDisplay(f.ctx, disable)
	if err != nil || disabled.Version != 4 || disabled.FiatCurrency != nil || disabled.RateNum != nil || disabled.RateDen != nil {
		t.Fatalf("display disable: %+v %v", disabled, err)
	}
	var versions, audits int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM coin_display_versions),(SELECT count(*) FROM audit_events WHERE action='coin_display.updated')`).Scan(&versions, &audits); err != nil || versions != 4 || audits != 3 {
		t.Fatalf("display versions/audits=%d/%d %v", versions, audits, err)
	}
	for _, invalid := range []CoinDisplayInput{
		coinDisplayCommand(f.operator, 4),
		coinDisplayCommand(f.admin, 0),
	} {
		if _, err = f.store.UpdateCoinDisplay(f.ctx, invalid); err == nil {
			t.Fatal("invalid display command was accepted")
		}
	}
	bad := coinDisplayCommand(f.admin, 4)
	bad.DisplayName = ""
	if _, err = f.store.UpdateCoinDisplay(f.ctx, bad); err == nil {
		t.Fatal("blank display name was accepted")
	}
	bad = coinDisplayCommand(f.admin, 4)
	bad.RateNum = 0
	if _, err = f.store.UpdateCoinDisplay(f.ctx, bad); err == nil {
		t.Fatal("zero display rate was accepted")
	}
	bad = coinDisplayCommand(f.admin, 4)
	bad.Confirmation = "no"
	if _, err = f.store.UpdateCoinDisplay(f.ctx, bad); err == nil {
		t.Fatal("missing display confirmation was accepted")
	}
}

func TestPostgresCoinDisplayActualApplication(t *testing.T) {
	f := walletSourcesFixture(t)
	walletSourcesApplication(t, f)
	updated, err := f.store.UpdateCoinDisplay(f.ctx, coinDisplayCommand(f.admin, 1))
	if err != nil || updated.Version != 2 {
		t.Fatalf("restricted APP display append: %+v %v", updated, err)
	}
	current, err := f.store.CoinDisplayCurrent(f.ctx)
	if err != nil || current.Version != 2 || current.DisplayName != "点数" {
		t.Fatalf("restricted APP public display: %+v %v", current, err)
	}
	if _, err = f.store.pool.Exec(f.ctx, `UPDATE coin_display_versions SET reason='forbidden' WHERE version=2`); err == nil {
		t.Fatal("restricted APP updated immutable display history")
	}
	if _, err = f.store.pool.Exec(f.ctx, `DELETE FROM coin_display_versions WHERE version=2`); err == nil {
		t.Fatal("restricted APP deleted immutable display history")
	}
}
