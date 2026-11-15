package billing

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresWalletOperatorSettingsAppendReplayCASAndAudit(t *testing.T) {
	f := fundingFixture(t)
	view, err := f.store.WalletOperatorSettings(f.ctx, f.admin)
	if err != nil || !view.Current.SingleOperator || view.Current.Version != 1 || len(view.Versions) != 1 || view.Current.Actor != nil {
		t.Fatalf("operator genesis: %+v %v", view, err)
	}
	if _, err = f.store.WalletOperatorSettings(f.ctx, f.operator); err == nil {
		t.Fatal("non-admin read operator settings")
	}
	command := WalletOperatorSettingInput{OperationID: uuid.NewString(), ActorID: f.admin, ExpectedRevision: 1, SingleOperator: false, Reason: "restore dual control", Confirmation: WalletOperatorConfirmation}
	disabled, err := f.store.UpdateWalletOperatorSetting(f.ctx, command)
	if err != nil || disabled.Version != 2 || disabled.SingleOperator || disabled.Actor == nil || *disabled.Actor != f.admin {
		t.Fatalf("disable operator mode: %+v %v", disabled, err)
	}
	replayed, err := f.store.UpdateWalletOperatorSetting(f.ctx, command)
	if err != nil || !reflect.DeepEqual(disabled, replayed) {
		t.Fatalf("operation replay changed the result: %+v %+v %v", disabled, replayed, err)
	}
	changed := command
	changed.Reason = "different intent"
	if _, err = f.store.UpdateWalletOperatorSetting(f.ctx, changed); err == nil {
		t.Fatal("operation ID accepted different content")
	} else {
		assertCode(t, err, "idempotency_conflict")
	}
	stale := WalletOperatorSettingInput{OperationID: uuid.NewString(), ActorID: f.admin, ExpectedRevision: 1, SingleOperator: true, Reason: "stale enable", Confirmation: WalletOperatorConfirmation}
	if _, err = f.store.UpdateWalletOperatorSetting(f.ctx, stale); err == nil {
		t.Fatal("stale revision changed operator settings")
	} else {
		assertCode(t, err, "revision_conflict")
	}
	if _, err = f.store.CreateCredit(f.ctx, creditInputForTest(f.tenant, f.admin, "disabled-admin-setting", 10, 0, "paid", 10)); err == nil {
		t.Fatal("disabled switch still allowed admin finance")
	}
	enable := WalletOperatorSettingInput{OperationID: uuid.NewString(), ActorID: f.admin, ExpectedRevision: 2, SingleOperator: true, Reason: "single operator maintenance", Confirmation: WalletOperatorConfirmation}
	if _, err = f.store.UpdateWalletOperatorSetting(f.ctx, enable); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, enabled := range []bool{false, true} {
		enabled := enabled
		go func() {
			<-start
			_, err := f.store.UpdateWalletOperatorSetting(f.ctx, WalletOperatorSettingInput{OperationID: uuid.NewString(), ActorID: f.admin, ExpectedRevision: 3, SingleOperator: enabled, Reason: "concurrent operator decision", Confirmation: WalletOperatorConfirmation})
			results <- err
		}()
	}
	close(start)
	succeeded, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			succeeded++
			continue
		}
		assertCode(t, err, "revision_conflict")
		conflicts++
	}
	if succeeded != 1 || conflicts != 1 {
		t.Fatalf("concurrent CAS results: success=%d conflicts=%d", succeeded, conflicts)
	}
	view, err = f.store.WalletOperatorSettings(f.ctx, f.admin)
	if err != nil || view.Current.Version != 4 || len(view.Versions) != 4 {
		t.Fatalf("append-only versions: %+v %v", view, err)
	}
	var settings, audits int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM wallet_operator_settings),(SELECT count(*) FROM audit_events WHERE action='wallet.operator_settings.updated')`).Scan(&settings, &audits); err != nil || settings != 4 || audits != 3 {
		t.Fatalf("settings/audit count=%d/%d %v", settings, audits, err)
	}
	if _, err = f.pool.Exec(f.ctx, `UPDATE wallet_operator_settings SET reason='rewrite' WHERE version=1`); err == nil {
		t.Fatal("operator history update was accepted")
	}
	if _, err = f.pool.Exec(f.ctx, `DELETE FROM wallet_operator_settings WHERE version=1`); err == nil {
		t.Fatal("operator history delete was accepted")
	}
	bad := enable
	bad.OperationID = uuid.NewString()
	bad.ExpectedRevision = 4
	bad.Confirmation = ""
	if _, err = f.store.UpdateWalletOperatorSetting(f.ctx, bad); err == nil {
		t.Fatal("missing second confirmation changed settings")
	}
}

func TestPostgresWalletOperatorSettingsActualApplication(t *testing.T) {
	f := walletSourcesFixture(t)
	walletSourcesApplication(t, f)
	command := WalletOperatorSettingInput{OperationID: uuid.NewString(), ActorID: f.admin, ExpectedRevision: 1, SingleOperator: false, Reason: "restricted application update", Confirmation: WalletOperatorConfirmation}
	updated, err := f.store.UpdateWalletOperatorSetting(f.ctx, command)
	if err != nil || updated.Version != 2 || updated.SingleOperator {
		t.Fatalf("restricted APP operator update: %+v %v", updated, err)
	}
	view, err := f.store.WalletOperatorSettings(f.ctx, f.admin)
	if err != nil || view.Current.Version != 2 || len(view.Versions) != 2 {
		t.Fatalf("restricted APP operator history: %+v %v", view, err)
	}
	if _, err = f.store.pool.Exec(f.ctx, `UPDATE wallet_operator_settings SET reason='forbidden' WHERE version=2`); err == nil {
		t.Fatal("restricted APP updated immutable operator history")
	}
	if _, err = f.store.pool.Exec(f.ctx, `DELETE FROM wallet_operator_settings WHERE version=2`); err == nil {
		t.Fatal("restricted APP deleted immutable operator history")
	}
}
