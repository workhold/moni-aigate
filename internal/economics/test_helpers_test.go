package economics

import "time"

const (
	testID0 = "00000000-0000-4000-8000-000000000000"
	testID1 = "00000000-0000-4000-8000-000000000001"
	testID2 = "00000000-0000-4000-8000-000000000002"
	testID3 = "00000000-0000-4000-8000-000000000003"
	testID4 = "00000000-0000-4000-8000-000000000004"
	testID5 = "00000000-0000-4000-8000-000000000005"
	testID6 = "00000000-0000-4000-8000-000000000006"
	testID7 = "00000000-0000-4000-8000-000000000007"
	testID8 = "00000000-0000-4000-8000-000000000008"
	testID9 = "00000000-0000-4000-8000-000000000009"
)

var testNow = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func validFx(target Currency) FxPolicy {
	return FxPolicy{
		ID: testID0, ValuationCurrency: target, Source: "test-fixture",
		QuotedAt: testNow.Add(-time.Hour), ValidUntil: testNow.Add(time.Hour),
		CreatedBy: "tester", CreatedAt: testNow.Add(-2 * time.Hour),
		Rates: []FxRate{
			{SourceCurrency: CurrencyUSD, TargetCurrency: CurrencyCNY, RateNum: 7, RateDen: 1},
			{SourceCurrency: CurrencyCNY, TargetCurrency: CurrencyUSD, RateNum: 1, RateDen: 7},
		},
	}
}

func validExecution() ExecutionPolicy {
	policy := P0ExecutionPolicy(testID1)
	policy.CreatedBy = "tester"
	policy.CreatedAt = testNow.Add(-time.Hour)
	return policy
}

func validTask() TaskPolicy {
	return TaskPolicy{
		ID: testID2, LogicalModelID: testID3, TaxonomyVersion: "041-v1",
		DefaultTaskClass: TaskCodingNonReasoning,
		AllowedHints:     []TaskClass{TaskReasoning, TaskUnknown},
		ClassCandidates: map[TaskClass][]string{
			TaskCodingNonReasoning: {testID4},
			TaskReasoning:          {testID4, testID5},
			TaskUnknown:            {},
		},
		UnknownAction: UnknownTaskDeny, ClassifierVersion: "none",
		ShapeDigestAlgorithm: "none", CreatedBy: "tester", CreatedAt: testNow.Add(-time.Hour),
	}
}

func validSnapshotInput() EconomicsSnapshotInput {
	fx := validFx(CurrencyCNY)
	execution := validExecution()
	basis := MoneyVector{CurrencyCNY: 10000}
	return EconomicsSnapshotInput{
		TenantID: testID0, RequestID: testID2, ReservationID: testID3,
		OfferVersionID: testID4, EconomicsVersionID: testID5,
		ExecutionPolicyVersionID: execution.ID, TaskPolicyVersionID: testID6,
		QualityPolicyVersionID: testID7, TaskClass: TaskCodingNonReasoning,
		TaskSource: TaskSourceSKU, TaskCandidates: []string{testID4}, SettlementMode: execution.SettlementMode,
		MaxOuterAttempts: execution.MaxOuterAttempts, ReservationCoinAmount: 10000,
		PaymentBasisMicro: basis, PaymentBasisComplete: true,
		FxPolicyVersionID: fx.ID, ValuationCurrency: CurrencyCNY, FxSnapshot: fx,
		MinMarginRate: 1000, ExecutionPolicy: execution, CreatedAt: testNow,
	}
}

func validSnapshot() EconomicsSnapshot {
	snapshot, err := NewEconomicsSnapshot(validSnapshotInput())
	if err != nil {
		panic(err)
	}
	return snapshot
}
