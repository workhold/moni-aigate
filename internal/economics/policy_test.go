package economics

import (
	"errors"
	"testing"
)

func TestExecutionPolicyP0ExactValues(t *testing.T) {
	policy := validExecution()
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ExecutionPolicy){
		"outer retry": func(p *ExecutionPolicy) { p.MaxOuterAttempts = 2 },
		"inner retry": func(p *ExecutionPolicy) { p.InnerMaxAttempts = 2 },
		"relay":       func(p *ExecutionPolicy) { p.MaxRelayDepth = 1 },
		"settlement":  func(p *ExecutionPolicy) { p.SettlementMode = "charged_usage" },
		"cache":       func(p *ExecutionPolicy) { p.CacheMode = "enabled" },
		"cumulative":  func(p *ExecutionPolicy) { p.CumulativeCostPolicy = "per_attempt" },
		"unknown":     func(p *ExecutionPolicy) { p.UnknownCostPolicy = "allow" },
		"id":          func(p *ExecutionPolicy) { p.ID = "not-a-uuid" },
		"created by":  func(p *ExecutionPolicy) { p.CreatedBy = "" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := policy
			mutate(&copy)
			if !errors.Is(copy.Validate(), ErrInvalidExecutionPolicy) {
				t.Fatal("non-P0 execution policy accepted")
			}
		})
	}
}

func TestTaskPolicyTaxonomyAndResolution(t *testing.T) {
	for _, class := range taskClasses {
		if class.Validate() != nil {
			t.Fatalf("fixed class rejected: %s", class)
		}
	}
	if TaskClass("other").Validate() == nil {
		t.Fatal("open task class accepted")
	}
	policy := validTask()
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	selection, err := policy.Resolve(nil)
	if err != nil || selection.Class != TaskCodingNonReasoning || selection.Source != TaskSourceSKU || selection.PolicyVersion != policy.ID {
		t.Fatalf("SKU default: %#v %v", selection, err)
	}
	hint := TaskReasoning
	selection, err = policy.Resolve(&hint)
	if err != nil || selection.Class != hint || selection.Source != TaskSourceHint {
		t.Fatalf("controlled hint: %#v %v", selection, err)
	}
	unknown := TaskUnknown
	if _, err = policy.Resolve(&unknown); !errors.Is(err, ErrUnknownTaskDenied) {
		t.Fatalf("unknown deny: %v", err)
	}
	disallowed := TaskTranslation
	if _, err = policy.Resolve(&disallowed); !errors.Is(err, ErrTaskHintNotAllowed) {
		t.Fatalf("disallowed hint: %v", err)
	}
	policy.UnknownAction = UnknownTaskUseUnknown
	policy.ClassCandidates[TaskUnknown] = []string{testID5}
	selection, err = policy.Resolve(&unknown)
	if err != nil || selection.Class != TaskUnknown {
		t.Fatalf("explicit unknown candidates: %#v %v", selection, err)
	}
}

func TestTaskPolicyRejectsInvalidMappings(t *testing.T) {
	for name, mutate := range map[string]func(*TaskPolicy){
		"classifier":              func(p *TaskPolicy) { p.ClassifierVersion = "llm" },
		"digest":                  func(p *TaskPolicy) { p.ShapeDigestAlgorithm = "sha256" },
		"missing default":         func(p *TaskPolicy) { delete(p.ClassCandidates, p.DefaultTaskClass) },
		"missing unknown":         func(p *TaskPolicy) { delete(p.ClassCandidates, TaskUnknown) },
		"deny unknown candidates": func(p *TaskPolicy) { p.ClassCandidates[TaskUnknown] = []string{testID4} },
		"unmapped hint":           func(p *TaskPolicy) { p.AllowedHints = append(p.AllowedHints, TaskTranslation) },
		"duplicate hint":          func(p *TaskPolicy) { p.AllowedHints = append(p.AllowedHints, TaskReasoning) },
		"unsorted":                func(p *TaskPolicy) { p.ClassCandidates[TaskReasoning] = []string{testID5, testID4} },
		"duplicate candidate":     func(p *TaskPolicy) { p.ClassCandidates[TaskReasoning] = []string{testID4, testID4} },
		"invalid candidate":       func(p *TaskPolicy) { p.ClassCandidates[TaskReasoning] = []string{"physical-a"} },
		"unknown action":          func(p *TaskPolicy) { p.UnknownAction = "allow" },
	} {
		t.Run(name, func(t *testing.T) {
			policy := validTask()
			mutate(&policy)
			if policy.Validate() == nil {
				t.Fatal("invalid task policy accepted")
			}
		})
	}
	policy := validTask()
	if err := policy.ValidateAgainstPool([]string{testID4, testID5}); err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidateAgainstPool([]string{testID4}); !errors.Is(err, ErrCandidateNotPublished) {
		t.Fatalf("candidate expanded published pool: %v", err)
	}
}

func TestEconomicsPolicyValidation(t *testing.T) {
	cap := MoneyVector{CurrencyCNY: 1, CurrencyUSD: 2}
	policy := EconomicsPolicy{
		ID: testID5, ValuationCurrency: CurrencyCNY, FxPolicyVersionID: testID0,
		MinMarginRate: 10000, MaxCumulativeCostMicro: &cap,
		CreatedBy: "tester", CreatedAt: testNow,
	}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	policy.MinMarginRate = 10001
	if !errors.Is(policy.Validate(), ErrInvalidEconomicsPolicy) {
		t.Fatal("margin above 10000 accepted")
	}
	policy.MinMarginRate = -1
	if policy.Validate() == nil {
		t.Fatal("negative margin accepted")
	}
	policy.MinMarginRate = 0
	zero := MoneyVector{CurrencyCNY: 0}
	policy.MaxCumulativeCostMicro = &zero
	if policy.Validate() == nil {
		t.Fatal("zero cumulative cap accepted")
	}
	empty := MoneyVector{}
	policy.MaxCumulativeCostMicro = &empty
	if policy.Validate() == nil {
		t.Fatal("empty cumulative cap accepted")
	}
	policy.MaxCumulativeCostMicro = nil
	if policy.Validate() != nil {
		t.Fatal("optional absent cap rejected")
	}
}
