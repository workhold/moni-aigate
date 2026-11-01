package alerts

import (
	"context"
	"encoding/json"
	"time"
)

// internal/alerts/types.go; wire字符串在构造边界严格验证，不能靠别名绕过。
type ID = string
type Decimal = string // 规范十进制，按字段适用正int64/非负/有符号大整数界
type Kind string

const (
	ChannelQuarantined         Kind = "channel_quarantined"
	ProcurementBudgetLow       Kind = "procurement_budget_low"
	CustomerReconciliationAged Kind = "customer_reconciliation_aged"
	ProcurementUnresolvedAged  Kind = "procurement_unresolved_aged"
)

type ScopeType string         // channel | procurement_account | tenant
type RuleState string         // enabled | disabled | retired
type Freshness string         // disabled | retired | paused | never_evaluated | fresh | stale
type CommandKind string       // rule_create | rule_configure | rule_retire | acknowledge
type EventType string         // opened | acknowledged | recovered | rule_changed | rule_disabled | rule_retired
type Time struct{ time.Time } // MarshalJSON固定UTC 2006-01-02T15:04:05.000000Z

// 封闭联合：只允许本包构造；MarshalJSON输出各kind精确字段。
// 对外不输出omitempty。数据库JSON独立验证后再decode为这些类型。
type Parameters interface {
	alertParameters()
	json.Marshaler
}
type EmptyParameters struct{} // JSON {}
type BudgetParameters struct {
	ThresholdAmount Decimal `json:"threshold_amount"`
	RecoveryAmount  Decimal `json:"recovery_amount"`
}
type AgingParameters struct {
	MinimumAgeSeconds Decimal `json:"minimum_age_seconds"`
	TriggerCount      Decimal `json:"trigger_count"`
	RecoveryCount     Decimal `json:"recovery_count"`
}
type Sample interface {
	alertSample()
	json.Marshaler
}
type ChannelSample struct {
	Kind        Kind `json:"kind"`
	AsOf        Time `json:"as_of"`
	Enabled     bool `json:"enabled"`
	Quarantined bool `json:"quarantined"`
}
type BudgetSample struct {
	Kind            Kind    `json:"kind"`
	AsOf            Time    `json:"as_of"`
	AccountState    string  `json:"account_state"`
	LimitAmount     Decimal `json:"limit_amount"`
	HeldAmount      Decimal `json:"held_amount"`
	SpentAmount     Decimal `json:"spent_amount"`
	AvailableAmount Decimal `json:"available_amount"`
	Currency        string  `json:"currency"` // CNY
}
type AgingSample struct {
	Kind              Kind    `json:"kind"`
	AsOf              Time    `json:"as_of"`
	MinimumAgeSeconds Decimal `json:"minimum_age_seconds"`
	ObservedCount     Decimal `json:"observed_count"`
	CountIsLowerBound bool    `json:"count_is_lower_bound"`
	OldestCreatedAt   *Time   `json:"oldest_created_at"`
	OldestReferenceID *ID     `json:"oldest_reference_id"`
}
type RuleSnapshot struct {
	Kind       Kind       `json:"kind"`
	ScopeType  ScopeType  `json:"scope_type"`
	ScopeID    ID         `json:"scope_id"`
	Label      string     `json:"label"`
	Parameters Parameters `json:"parameters"`
}
type Capabilities struct {
	DeploymentEnabled         bool    `json:"deployment_enabled"`
	RecoveryMode              bool    `json:"recovery_mode"`
	EvaluationAvailable       bool    `json:"evaluation_available"`
	Reason                    string  `json:"reason"`
	MaxEnabledRules           Decimal `json:"max_enabled_rules"`
	EvaluationIntervalSeconds Decimal `json:"evaluation_interval_seconds"`
	StaleAfterSeconds         Decimal `json:"stale_after_seconds"`
	ObservedAt                Time    `json:"observed_at"`
}
type RuntimeView struct {
	NextEvaluationAt  *Time     `json:"next_evaluation_at"`
	LastAttemptAt     *Time     `json:"last_attempt_at"`
	LastSuccessAt     *Time     `json:"last_success_at"`
	EvaluatedRevision *Decimal  `json:"evaluated_revision"`
	LastSample        Sample    `json:"last_sample"` // nil编码null
	LastErrorCode     *string   `json:"last_error_code"`
	CurrentEpisodeID  *ID       `json:"current_episode_id"`
	Freshness         Freshness `json:"freshness"`
	ObservedAt        Time      `json:"observed_at"`
}
type RuleView struct {
	ID                    ID          `json:"id"`
	Kind                  Kind        `json:"kind"`
	ScopeType             ScopeType   `json:"scope_type"`
	ScopeID               ID          `json:"scope_id"`
	Label                 string      `json:"label"`
	State                 RuleState   `json:"state"`
	Parameters            Parameters  `json:"parameters"`
	ConfigurationRevision Decimal     `json:"configuration_revision"`
	CreatedBy             ID          `json:"created_by"`
	UpdatedBy             ID          `json:"updated_by"`
	CreatedAt             Time        `json:"created_at"`
	UpdatedAt             Time        `json:"updated_at"`
	RetiredAt             *Time       `json:"retired_at"`
	Runtime               RuntimeView `json:"runtime"`
}
type EpisodeView struct {
	ID                  ID           `json:"id"`
	RuleID              ID           `json:"rule_id"`
	Generation          Decimal      `json:"generation"`
	RuleRevision        Decimal      `json:"rule_revision"`
	RuleSnapshot        RuleSnapshot `json:"rule_snapshot"`
	Status              string       `json:"status"`
	ActionRevision      Decimal      `json:"action_revision"`
	OpenedAt            Time         `json:"opened_at"`
	InitialSample       Sample       `json:"initial_sample"`
	LastSeenAt          Time         `json:"last_seen_at"`
	LatestSample        Sample       `json:"latest_sample"`
	AcknowledgedBy      *ID          `json:"acknowledged_by"`
	AcknowledgedAt      *Time        `json:"acknowledged_at"`
	ResolvedAt          *Time        `json:"resolved_at"`
	ResolveReason       *string      `json:"resolve_reason"`
	EvaluationFreshness Freshness    `json:"evaluation_freshness"`
	ObservedAt          Time         `json:"observed_at"`
}
type EventView struct {
	ID             ID        `json:"id"`
	EpisodeID      ID        `json:"episode_id"`
	ActionRevision Decimal   `json:"action_revision"`
	Type           EventType `json:"type"`
	Actor          string    `json:"actor"`
	ObservedAt     Time      `json:"observed_at"`
	SafeSample     Sample    `json:"safe_sample"`
	CreatedAt      Time      `json:"created_at"`
}
type CommandReceipt struct {
	OperationID    ID          `json:"operation_id"`
	Kind           CommandKind `json:"kind"`
	Status         string      `json:"status"`
	Reason         string      `json:"reason"`
	ResourceID     *ID         `json:"resource_id"`
	ResultRevision *Decimal    `json:"result_revision"`
	CreatedAt      Time        `json:"created_at"`
}
type Page[T any] struct {
	Data       []T
	NextCursor *string
} // HTTP另包request_id，Data永不nil
type Cursor struct {
	V            int    `json:"v"`
	T            Time   `json:"t"`
	ID           ID     `json:"id"`
	FilterDigest string `json:"filter_digest"`
}
type RuleFilter struct {
	State   *RuleState
	Kind    *Kind
	ScopeID *ID
	Cursor  *Cursor
}
type EpisodeFilter struct {
	Status *string
	RuleID *ID
	Kind   *Kind
	Cursor *Cursor
}
type EventFilter struct{ Cursor *Cursor }
type Reader struct{ ActorID ID } // 无客户端role或tenant授权字段
type CreateRule struct {
	OperationID ID
	Kind        Kind
	ScopeType   ScopeType
	ScopeID     ID
	Label       string
	State       RuleState
	Parameters  Parameters
}
type ConfigureRule struct {
	OperationID      ID
	ExpectedRevision Decimal
	Label            string
	State            RuleState
	Parameters       Parameters
}
type RetireRule struct {
	OperationID      ID
	ExpectedRevision Decimal
}
type Acknowledge struct {
	OperationID            ID
	ExpectedActionRevision Decimal
}
type RuntimeOptions struct {
	DeploymentEnabled bool
	RecoveryMode      bool
} // 启动冻结，无热改

type Queries interface {
	Capabilities(context.Context, Reader) (Capabilities, error)
	Rules(context.Context, Reader, RuleFilter) (Page[RuleView], error)
	Rule(context.Context, Reader, ID) (RuleView, error)
	Episodes(context.Context, Reader, EpisodeFilter) (Page[EpisodeView], error)
	Episode(context.Context, Reader, ID) (EpisodeView, error)
	Events(context.Context, Reader, ID, EventFilter) (Page[EventView], error)
	Operation(context.Context, Reader, ID) (CommandReceipt, error)
}
type Commands interface {
	Create(context.Context, Reader, CreateRule) (CommandReceipt, error)
	Configure(context.Context, Reader, ID, ConfigureRule) (CommandReceipt, error)
	Retire(context.Context, Reader, ID, RetireRule) (CommandReceipt, error)
	Acknowledge(context.Context, Reader, ID, Acknowledge) (CommandReceipt, error)
}
type EvaluationResult struct {
	Outcome   string // idle | evaluated | failed_recorded | stale_discarded
	ErrorCode string // 安全枚举；无源行/SQL/原始错误正文
}
type EvaluationRepository interface {
	// 自己取得一条due rule并持锁完成源查询/决策；不返回待执行claim。
	EvaluateDue(context.Context) (EvaluationResult, error)
}

type Error struct {
	Code          string
	CommitUnknown bool
}

func (e *Error) Error() string { return e.Code }
func fail(code string) error   { return &Error{Code: code} }
