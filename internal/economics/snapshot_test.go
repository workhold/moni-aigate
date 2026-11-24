package economics

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestEconomicsSnapshotCanonicalRoundTripAndDigest(t *testing.T) {
	input := validSnapshotInput()
	input.FxSnapshot.Rates[0], input.FxSnapshot.Rates[1] = input.FxSnapshot.Rates[1], input.FxSnapshot.Rates[0]
	cap := MoneyVector{CurrencyCNY: 9000}
	input.MaxCumulativeCostMicro = &cap
	snapshot, err := NewEconomicsSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := snapshot.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded EconomicsSnapshot
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	raw2, _ := decoded.CanonicalJSON()
	if string(raw) != string(raw2) {
		t.Fatalf("snapshot JSON is not stable:\n%s\n%s", raw, raw2)
	}
	digest1, _ := snapshot.Digest()
	digest2, _ := decoded.Digest()
	if digest1 != digest2 {
		t.Fatal("snapshot digest changed across round trip")
	}
	if snapshot.TenantID() != input.TenantID || snapshot.RequestID() != input.RequestID ||
		snapshot.PaymentBasisComplete() != true || snapshot.MinMarginRate() != 1000 {
		t.Fatal("snapshot accessors lost data")
	}
}

func TestEconomicsSnapshotDefensiveCopies(t *testing.T) {
	input := validSnapshotInput()
	snapshot, err := NewEconomicsSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	input.PaymentBasisMicro[CurrencyCNY] = 1
	input.FxSnapshot.Rates[0].RateNum = 100
	basis := snapshot.PaymentBasisMicro()
	basis[CurrencyCNY] = 2
	fx := snapshot.FxSnapshot()
	fx.Rates[0].RateNum = 200
	dto := snapshot.DTO()
	dto.PaymentBasisMicro[CurrencyCNY] = 3
	if snapshot.PaymentBasisMicro()[CurrencyCNY] != 10000 || snapshot.FxSnapshot().Rates[1].RateNum != 7 {
		t.Fatal("snapshot mutable state escaped")
	}
	cap := MoneyVector{CurrencyCNY: 9000}
	input = validSnapshotInput()
	input.MaxCumulativeCostMicro = &cap
	snapshot, err = NewEconomicsSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	cap[CurrencyCNY] = 1
	got, ok := snapshot.MaxCumulativeCostMicro()
	if !ok || got[CurrencyCNY] != 9000 {
		t.Fatal("snapshot cap was not copied")
	}
	got[CurrencyCNY] = 2
	got, _ = snapshot.MaxCumulativeCostMicro()
	if got[CurrencyCNY] != 9000 {
		t.Fatal("snapshot cap accessor aliases state")
	}
}

func TestEconomicsSnapshotRejectsInconsistentOrUnsafeJSON(t *testing.T) {
	for name, mutate := range map[string]func(*EconomicsSnapshotInput){
		"execution id":         func(i *EconomicsSnapshotInput) { i.ExecutionPolicyVersionID = testID9 },
		"fx id":                func(i *EconomicsSnapshotInput) { i.FxPolicyVersionID = testID9 },
		"valuation":            func(i *EconomicsSnapshotInput) { i.ValuationCurrency = CurrencyUSD },
		"settlement":           func(i *EconomicsSnapshotInput) { i.SettlementMode = "charged_usage" },
		"attempts":             func(i *EconomicsSnapshotInput) { i.MaxOuterAttempts = 2 },
		"nil basis":            func(i *EconomicsSnapshotInput) { i.PaymentBasisMicro = nil },
		"negative reservation": func(i *EconomicsSnapshotInput) { i.ReservationCoinAmount = -1 },
		"margin":               func(i *EconomicsSnapshotInput) { i.MinMarginRate = 10001 },
	} {
		t.Run(name, func(t *testing.T) {
			input := validSnapshotInput()
			mutate(&input)
			if _, err := NewEconomicsSnapshot(input); !errors.Is(err, ErrInvalidSnapshot) {
				t.Fatalf("inconsistent snapshot accepted: %v", err)
			}
		})
	}
	raw, _ := json.Marshal(validSnapshotInput())
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	object["unexpected"] = json.RawMessage(`true`)
	raw, _ = json.Marshal(object)
	var snapshot EconomicsSnapshot
	if err := json.Unmarshal(raw, &snapshot); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("unknown snapshot field accepted: %v", err)
	}
	delete(object, "unexpected")
	for _, field := range []string{"min_margin_rate", "payment_basis_complete", "reservation_coin_amount", "task_candidates"} {
		raw, _ = json.Marshal(validSnapshotInput())
		_ = json.Unmarshal(raw, &object)
		object[field] = json.RawMessage(`null`)
		raw, _ = json.Marshal(object)
		if err := json.Unmarshal(raw, &snapshot); !errors.Is(err, ErrInvalidSnapshot) {
			t.Fatalf("null %s was accepted as a zero value", field)
		}
	}
}

func TestEconomicsSnapshotPreservesExplicitEmptyTaskPool(t *testing.T) {
	input := validSnapshotInput()
	input.TaskCandidates = []string{}
	snapshot, err := NewEconomicsSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TaskCandidates() == nil || len(snapshot.TaskCandidates()) != 0 {
		t.Fatal("explicit empty task pool became an unspecified pool")
	}
}

func TestSnapshotDTOIsDetached(t *testing.T) {
	snapshot := validSnapshot()
	dto := snapshot.DTO()
	if !reflect.DeepEqual(dto.PaymentBasisMicro, MoneyVector{CurrencyCNY: 10000}) {
		t.Fatal("DTO lost basis")
	}
}
