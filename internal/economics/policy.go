package economics

import (
	"errors"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidExecutionPolicy = errors.New("invalid execution policy")
	ErrInvalidTaskPolicy      = errors.New("invalid task policy")
	ErrInvalidTaskClass       = errors.New("invalid task class")
	ErrTaskHintNotAllowed     = errors.New("task hint is not allowed")
	ErrUnknownTaskDenied      = errors.New("unknown task is denied")
	ErrCandidateNotPublished  = errors.New("candidate is outside the published pool")
	ErrInvalidEconomicsPolicy = errors.New("invalid economics policy")
)

const (
	SettlementFixedFullReservation = "fixed_full_reservation"
	CacheModeDisabled              = "disabled"
	CumulativeCostRequestTotalV1   = "request_total_v1"
	UnknownCostDeny                = "deny"
)

// ExecutionPolicy contains the complete immutable execution policy. P0 accepts
// exactly one combination; no client preference can enlarge these limits.
type ExecutionPolicy struct {
	ID                   string    `json:"id"`
	MaxOuterAttempts     int64     `json:"max_outer_attempts"`
	InnerMaxAttempts     int64     `json:"inner_max_attempts"`
	MaxRelayDepth        int64     `json:"max_relay_depth"`
	SettlementMode       string    `json:"settlement_mode"`
	CacheMode            string    `json:"cache_mode"`
	CumulativeCostPolicy string    `json:"cumulative_cost_policy"`
	UnknownCostPolicy    string    `json:"unknown_cost_policy"`
	CreatedBy            string    `json:"created_by"`
	CreatedAt            time.Time `json:"created_at"`
}

type ExecutionPolicyVersion = ExecutionPolicy

func P0ExecutionPolicy(id string) ExecutionPolicy {
	return ExecutionPolicy{
		ID:                   id,
		MaxOuterAttempts:     1,
		InnerMaxAttempts:     1,
		MaxRelayDepth:        0,
		SettlementMode:       SettlementFixedFullReservation,
		CacheMode:            CacheModeDisabled,
		CumulativeCostPolicy: CumulativeCostRequestTotalV1,
		UnknownCostPolicy:    UnknownCostDeny,
	}
}

func (p ExecutionPolicy) Validate() error {
	if !canonicalUUID(p.ID) || p.MaxOuterAttempts != 1 || p.InnerMaxAttempts != 1 ||
		p.MaxRelayDepth != 0 || p.SettlementMode != SettlementFixedFullReservation ||
		p.CacheMode != CacheModeDisabled || p.CumulativeCostPolicy != CumulativeCostRequestTotalV1 ||
		p.UnknownCostPolicy != UnknownCostDeny || strings.TrimSpace(p.CreatedBy) == "" || p.CreatedAt.IsZero() {
		return ErrInvalidExecutionPolicy
	}
	return nil
}

// TaskClass is the closed 041 taxonomy.
type TaskClass string

const (
	TaskCodingNonReasoning TaskClass = "coding_non_reasoning"
	TaskCodingReasoning    TaskClass = "coding_reasoning"
	TaskReasoning          TaskClass = "reasoning"
	TaskToolUse            TaskClass = "tool_use"
	TaskExtraction         TaskClass = "extraction"
	TaskSummarization      TaskClass = "summarization"
	TaskTranslation        TaskClass = "translation"
	TaskGeneralChat        TaskClass = "general_chat"
	TaskUnknown            TaskClass = "unknown"
)

var taskClasses = [...]TaskClass{
	TaskCodingNonReasoning, TaskCodingReasoning, TaskReasoning, TaskToolUse,
	TaskExtraction, TaskSummarization, TaskTranslation, TaskGeneralChat, TaskUnknown,
}

func (c TaskClass) Validate() error {
	for _, candidate := range taskClasses {
		if c == candidate {
			return nil
		}
	}
	return ErrInvalidTaskClass
}

type UnknownTaskAction string

const (
	UnknownTaskDeny       UnknownTaskAction = "deny"
	UnknownTaskUseUnknown UnknownTaskAction = "use_unknown"
)

type TaskSource string

const (
	TaskSourceSKU  TaskSource = "sku"
	TaskSourceHint TaskSource = "hint"
)

// TaskPolicy performs only deterministic SKU-default/controlled-hint mapping.
// It has no classifier, prompt input, network access, or model execution hook.
type TaskPolicy struct {
	ID                   string                 `json:"id"`
	LogicalModelID       string                 `json:"logical_model_id"`
	TaxonomyVersion      string                 `json:"taxonomy_version"`
	DefaultTaskClass     TaskClass              `json:"default_task_class"`
	AllowedHints         []TaskClass            `json:"allowed_hints"`
	ClassCandidates      map[TaskClass][]string `json:"class_candidates"`
	UnknownAction        UnknownTaskAction      `json:"unknown_action"`
	ClassifierVersion    string                 `json:"classifier_version"`
	ShapeDigestAlgorithm string                 `json:"shape_digest_algorithm"`
	CreatedBy            string                 `json:"created_by"`
	CreatedAt            time.Time              `json:"created_at"`
}

type TaskPolicyVersion = TaskPolicy

func (p TaskPolicy) Validate() error {
	if !canonicalUUID(p.ID) || !canonicalUUID(p.LogicalModelID) ||
		strings.TrimSpace(p.TaxonomyVersion) == "" || p.DefaultTaskClass.Validate() != nil ||
		p.ClassifierVersion != "none" || p.ShapeDigestAlgorithm != "none" ||
		strings.TrimSpace(p.CreatedBy) == "" || p.CreatedAt.IsZero() {
		return ErrInvalidTaskPolicy
	}
	if p.UnknownAction != UnknownTaskDeny && p.UnknownAction != UnknownTaskUseUnknown {
		return ErrInvalidTaskPolicy
	}
	seenHints := make(map[TaskClass]struct{}, len(p.AllowedHints))
	for _, hint := range p.AllowedHints {
		if hint.Validate() != nil {
			return ErrInvalidTaskClass
		}
		if _, duplicate := seenHints[hint]; duplicate {
			return ErrInvalidTaskPolicy
		}
		seenHints[hint] = struct{}{}
		if _, ok := p.ClassCandidates[hint]; !ok {
			return ErrInvalidTaskPolicy
		}
	}
	if p.ClassCandidates == nil {
		return ErrInvalidTaskPolicy
	}
	if _, ok := p.ClassCandidates[p.DefaultTaskClass]; !ok {
		return ErrInvalidTaskPolicy
	}
	unknown, ok := p.ClassCandidates[TaskUnknown]
	if !ok || (p.UnknownAction == UnknownTaskDeny && len(unknown) != 0) {
		return ErrInvalidTaskPolicy
	}
	for class, candidates := range p.ClassCandidates {
		if class.Validate() != nil {
			return ErrInvalidTaskClass
		}
		if !sort.StringsAreSorted(candidates) {
			return ErrInvalidTaskPolicy
		}
		for i, id := range candidates {
			if !canonicalUUID(id) || (i > 0 && candidates[i-1] == id) {
				return ErrInvalidTaskPolicy
			}
		}
	}
	return nil
}

// ValidateAgainstPool additionally verifies that every candidate only narrows
// the supplied published physical-model pool.
func (p TaskPolicy) ValidateAgainstPool(published []string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	pool := make(map[string]struct{}, len(published))
	for _, id := range published {
		if !canonicalUUID(id) {
			return ErrInvalidTaskPolicy
		}
		pool[id] = struct{}{}
	}
	for _, candidates := range p.ClassCandidates {
		for _, id := range candidates {
			if _, ok := pool[id]; !ok {
				return ErrCandidateNotPublished
			}
		}
	}
	return nil
}

type TaskSelection struct {
	Class         TaskClass  `json:"class"`
	Source        TaskSource `json:"source"`
	PolicyVersion string     `json:"policy_version"`
}

// Resolve chooses only the SKU default or an explicitly allowed hint.
func (p TaskPolicy) Resolve(hint *TaskClass) (TaskSelection, error) {
	if err := p.Validate(); err != nil {
		return TaskSelection{}, err
	}
	class, source := p.DefaultTaskClass, TaskSourceSKU
	if hint != nil {
		class, source = *hint, TaskSourceHint
		allowed := false
		for _, candidate := range p.AllowedHints {
			if class == candidate {
				allowed = true
				break
			}
		}
		if !allowed {
			return TaskSelection{}, ErrTaskHintNotAllowed
		}
	}
	if class == TaskUnknown && p.UnknownAction == UnknownTaskDeny {
		return TaskSelection{}, ErrUnknownTaskDenied
	}
	if _, ok := p.ClassCandidates[class]; !ok {
		return TaskSelection{}, ErrInvalidTaskPolicy
	}
	return TaskSelection{Class: class, Source: source, PolicyVersion: p.ID}, nil
}

// EconomicsPolicy is the immutable margin and optional request-total cap DTO.
type EconomicsPolicy struct {
	ID                     string       `json:"id"`
	ValuationCurrency      Currency     `json:"valuation_currency"`
	FxPolicyVersionID      string       `json:"fx_policy_version_id"`
	MinMarginRate          int64        `json:"min_margin_rate"`
	MaxCumulativeCostMicro *MoneyVector `json:"max_cumulative_cost_micro"`
	CreatedBy              string       `json:"created_by"`
	CreatedAt              time.Time    `json:"created_at"`
}

type EconomicsPolicyVersion = EconomicsPolicy

func (p EconomicsPolicy) Validate() error {
	if !canonicalUUID(p.ID) || p.ValuationCurrency.Validate() != nil ||
		!canonicalUUID(p.FxPolicyVersionID) || p.MinMarginRate < 0 || p.MinMarginRate > 10000 ||
		strings.TrimSpace(p.CreatedBy) == "" || p.CreatedAt.IsZero() {
		return ErrInvalidEconomicsPolicy
	}
	if p.MaxCumulativeCostMicro != nil {
		if err := p.MaxCumulativeCostMicro.Validate(); err != nil {
			return ErrInvalidEconomicsPolicy
		}
		if len(*p.MaxCumulativeCostMicro) == 0 {
			return ErrInvalidEconomicsPolicy
		}
		for _, amount := range *p.MaxCumulativeCostMicro {
			if amount <= 0 {
				return ErrInvalidEconomicsPolicy
			}
		}
	}
	return nil
}
