// Package procurementprefix contains raw DTO and replay foundations.
// The complete four-domain Check entry point is not implemented or exposed.
package procurementprefix

import (
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

var (
	ErrInput    = errors.New("procurement_prefix_input")
	ErrResource = errors.New("procurement_prefix_resource")
	ErrCanceled = errors.New("procurement_prefix_canceled")
	ErrTimeout  = errors.New("procurement_prefix_timeout")
)

// Time preserves physical NULL, infinity, microseconds and the binary timestamp value.
type Time = pgtype.Timestamptz

type Rows struct {
	OriginalRows OriginalRows `json:"original_rows"`
	PrefixRows   PrefixRows   `json:"prefix_rows"`
}

type PrefixRows struct {
	OperationRoots              []OperationRootRow              `json:"pp_operation_roots"`
	RootSteps                   []RootStepRow                   `json:"pp_root_steps"`
	RequestOrigins              []RequestOriginRow              `json:"pp_request_origins"`
	RequestHeads                []RequestHeadRow                `json:"pp_request_heads"`
	AttemptOrigins              []AttemptOriginRow              `json:"pp_attempt_origins"`
	AttemptHeads                []AttemptHeadRow                `json:"pp_attempt_heads"`
	AttemptNodes                []AttemptNodeRow                `json:"pp_attempt_nodes"`
	AccountOrigins              []AccountOriginRow              `json:"pp_account_origins"`
	AccountHeads                []AccountHeadRow                `json:"pp_account_heads"`
	AccountJournal              []AccountJournalRow             `json:"pp_account_journal"`
	AccountCommandSources       []AccountCommandSourceRow       `json:"pp_account_command_sources"`
	AttemptDeliverySources      []AttemptDeliverySourceRow      `json:"pp_attempt_delivery_sources"`
	IncomingSources             []IncomingSourceRow             `json:"pp_incoming_sources"`
	MeterChoices                []MeterChoiceRow                `json:"pp_meter_choices"`
	MeterOrigins                []MeterOriginRow                `json:"pp_meter_origins"`
	ConflictSources             []ConflictSourceRow             `json:"pp_conflict_sources"`
	RequestDecisions            []RequestDecisionRow            `json:"pp_request_decisions"`
	NormalCostDecisions         []NormalCostDecisionRow         `json:"pp_normal_cost_decisions"`
	CostMutations               []CostMutationRow               `json:"pp_cost_mutations"`
	CostResetDecisions          []CostResetDecisionRow          `json:"pp_cost_reset_decisions"`
	EvidenceChoices             []EvidenceChoiceRow             `json:"pp_evidence_choices"`
	EvidenceMembers             []EvidenceMemberRow             `json:"pp_evidence_members"`
	ProcurementTransitions      []ProcurementTransitionRow      `json:"pp_procurement_transitions"`
	ProposalBirths              []ProposalBirthRow              `json:"pp_proposal_births"`
	ApprovalConsumptions        []ApprovalConsumptionRow        `json:"pp_approval_consumptions"`
	UnmanagedCorrectionSources  []UnmanagedCorrectionSourceRow  `json:"pp_unmanaged_correction_sources"`
	RecoverySources             []RecoverySourceRow             `json:"pp_recovery_sources"`
	CostOutboxLinks             []CostOutboxLinkRow             `json:"pp_cost_outbox_links"`
	ReconciliationSources       []ReconciliationSourceRow       `json:"pp_reconciliation_sources"`
	CustomerFinishOutcomes      []CustomerFinishOutcomeRow      `json:"pp_customer_finish_outcomes"`
	CustomerFinishLedgerEffects []CustomerFinishLedgerEffectRow `json:"pp_customer_finish_ledger_effects"`
	CustomerFinishPBEffects     []CustomerFinishPBEffectRow     `json:"pp_customer_finish_pb_effects"`
}

type PrefixScan struct {
	OperationRoots              uint64 `json:"pp_operation_roots"`
	RootSteps                   uint64 `json:"pp_root_steps"`
	RequestOrigins              uint64 `json:"pp_request_origins"`
	RequestHeads                uint64 `json:"pp_request_heads"`
	AttemptOrigins              uint64 `json:"pp_attempt_origins"`
	AttemptHeads                uint64 `json:"pp_attempt_heads"`
	AttemptNodes                uint64 `json:"pp_attempt_nodes"`
	AccountOrigins              uint64 `json:"pp_account_origins"`
	AccountHeads                uint64 `json:"pp_account_heads"`
	AccountJournal              uint64 `json:"pp_account_journal"`
	AccountCommandSources       uint64 `json:"pp_account_command_sources"`
	AttemptDeliverySources      uint64 `json:"pp_attempt_delivery_sources"`
	IncomingSources             uint64 `json:"pp_incoming_sources"`
	MeterChoices                uint64 `json:"pp_meter_choices"`
	MeterOrigins                uint64 `json:"pp_meter_origins"`
	ConflictSources             uint64 `json:"pp_conflict_sources"`
	RequestDecisions            uint64 `json:"pp_request_decisions"`
	NormalCostDecisions         uint64 `json:"pp_normal_cost_decisions"`
	CostMutations               uint64 `json:"pp_cost_mutations"`
	CostResetDecisions          uint64 `json:"pp_cost_reset_decisions"`
	EvidenceChoices             uint64 `json:"pp_evidence_choices"`
	EvidenceMembers             uint64 `json:"pp_evidence_members"`
	ProcurementTransitions      uint64 `json:"pp_procurement_transitions"`
	ProposalBirths              uint64 `json:"pp_proposal_births"`
	ApprovalConsumptions        uint64 `json:"pp_approval_consumptions"`
	UnmanagedCorrectionSources  uint64 `json:"pp_unmanaged_correction_sources"`
	RecoverySources             uint64 `json:"pp_recovery_sources"`
	CostOutboxLinks             uint64 `json:"pp_cost_outbox_links"`
	ReconciliationSources       uint64 `json:"pp_reconciliation_sources"`
	CustomerFinishOutcomes      uint64 `json:"pp_customer_finish_outcomes"`
	CustomerFinishLedgerEffects uint64 `json:"pp_customer_finish_ledger_effects"`
	CustomerFinishPBEffects     uint64 `json:"pp_customer_finish_pb_effects"`
}

type Scan struct {
	OriginalRows OriginalScan `json:"original_rows"`
	PrefixRows   PrefixScan   `json:"prefix_rows"`
	TotalRows    uint64       `json:"total_rows"`
	LogicalBytes uint64       `json:"logical_bytes"`
}

type SourceClosure struct {
	InvalidScalarRows   uint64 `json:"invalid_scalar_rows"`
	DuplicateRows       uint64 `json:"duplicate_rows"`
	RootTemplateRows    uint64 `json:"root_template_rows"`
	CanonicalUnionRows  uint64 `json:"canonical_union_rows"`
	CompanionRows       uint64 `json:"companion_rows"`
	OriginalPointerRows uint64 `json:"original_pointer_rows"`
	GeneratedRows       uint64 `json:"generated_rows"`
	RequestChainRows    uint64 `json:"request_chain_rows"`
	AttemptChainRows    uint64 `json:"attempt_chain_rows"`
	AccountChainRows    uint64 `json:"account_chain_rows"`
	EvidenceChainRows   uint64 `json:"evidence_chain_rows"`
}

type RequestIncoming struct {
	InputDigestRows   uint64 `json:"input_digest_rows"`
	MeterIdentityRows uint64 `json:"meter_identity_rows"`
	MeterChoiceRows   uint64 `json:"meter_choice_rows"`
	ConflictRows      uint64 `json:"conflict_rows"`
	RequestStickyRows uint64 `json:"request_sticky_rows"`
	FinalizationRows  uint64 `json:"finalization_rows"`
	LatestRows        uint64 `json:"latest_rows"`
}

type AttemptCost struct {
	AttemptBirthRows      uint64 `json:"attempt_birth_rows"`
	QuoteBindingRows      uint64 `json:"quote_binding_rows"`
	DeliveryRows          uint64 `json:"delivery_rows"`
	NormalEligibilityRows uint64 `json:"normal_eligibility_rows"`
	ResetRows             uint64 `json:"reset_rows"`
	MutationRows          uint64 `json:"mutation_rows"`
	CorrectionRows        uint64 `json:"correction_rows"`
	OutboxRows            uint64 `json:"outbox_rows"`
	CurrentCostRows       uint64 `json:"current_cost_rows"`
	ReconciliationRows    uint64 `json:"reconciliation_rows"`
	CustomerFinishRows    uint64 `json:"customer_finish_rows"`
	CustomerLedgerRows    uint64 `json:"customer_ledger_rows"`
	CustomerPBRows        uint64 `json:"customer_p_b_rows"`
}

type AccountPrefix struct {
	AccountBirthRows      uint64 `json:"account_birth_rows"`
	CommandRows           uint64 `json:"command_rows"`
	JournalRows           uint64 `json:"journal_rows"`
	GateRows              uint64 `json:"gate_rows"`
	BlockingRows          uint64 `json:"blocking_rows"`
	EvidenceRows          uint64 `json:"evidence_rows"`
	TransitionRows        uint64 `json:"transition_rows"`
	PauseRows             uint64 `json:"pause_rows"`
	ProposalRows          uint64 `json:"proposal_rows"`
	ApprovalRows          uint64 `json:"approval_rows"`
	RecoveryRows          uint64 `json:"recovery_rows"`
	CurrentProjectionRows uint64 `json:"current_projection_rows"`
}

type Gaps struct {
	UncoveredRequestOrigins      uint64 `json:"uncovered_request_origins"`
	UncoveredAttemptOrigins      uint64 `json:"uncovered_attempt_origins"`
	UncoveredAccountOrigins      uint64 `json:"uncovered_account_origins"`
	UncoveredMeters              uint64 `json:"uncovered_meters"`
	UncoveredConflicts           uint64 `json:"uncovered_conflicts"`
	UncoveredFinalizations       uint64 `json:"uncovered_finalizations"`
	UncoveredProcurementEvents   uint64 `json:"uncovered_procurement_events"`
	UncoveredCostWrites          uint64 `json:"uncovered_cost_writes"`
	UncoveredProposals           uint64 `json:"uncovered_proposals"`
	UncoveredCorrections         uint64 `json:"uncovered_corrections"`
	UncoveredReconciliations     uint64 `json:"uncovered_reconciliations"`
	UncoveredCustomerFinishes    uint64 `json:"uncovered_customer_finishes"`
	UncoveredAuditOutbox         uint64 `json:"uncovered_audit_outbox"`
	LegacySourceBoundaryUnproven uint64 `json:"legacy_source_boundary_unproven"`
}

type Proposition struct {
	Required uint64 `json:"required"`
	Proven   uint64 `json:"proven"`
	Unproven uint64 `json:"unproven"`
}

type Propositions struct {
	OriginalDeliveryPredecessor Proposition `json:"original_delivery_predecessor"`
	FinalizationOrder           Proposition `json:"finalization_order"`
	ConflictIncoming            Proposition `json:"conflict_incoming"`
	ConflictReason              Proposition `json:"conflict_reason"`
	IncomingBinding             Proposition `json:"incoming_binding"`
	ProcurementEventOrder       Proposition `json:"procurement_event_order"`
	CostRecordedEligibility     Proposition `json:"cost_recorded_eligibility"`
	CurrentCostOrder            Proposition `json:"current_cost_order"`
	ProposalBarrier             Proposition `json:"proposal_barrier"`
	AccountPauseOrdering        Proposition `json:"account_pause_ordering"`
	AccountAdmissionOrder       Proposition `json:"account_admission_order"`
}

// Report is descriptive. No Check may return one until all four domains are implemented.
type Report struct {
	Scope            string                                         `json:"scope"`
	Status           string                                         `json:"status"`
	Balanced         bool                                           `json:"balanced"`
	CoverageComplete bool                                           `json:"coverage_complete"`
	Scanned          Scan                                           `json:"scanned"`
	Base             fundinglotverify.ProcurementEvidenceGraphCheck `json:"base"`
	SourceClosure    SourceClosure                                  `json:"source_closure"`
	RequestIncoming  RequestIncoming                                `json:"request_incoming"`
	AttemptCost      AttemptCost                                    `json:"attempt_cost"`
	AccountPrefix    AccountPrefix                                  `json:"account_prefix"`
	Propositions     Propositions                                   `json:"propositions"`
	Unproven         Gaps                                           `json:"unproven"`
}

// OperationRootRow is every physical column of pp_operation_roots.
type OperationRootRow struct {
	ID              string  `json:"id"`
	EntryKind       string  `json:"entry_kind"`
	ScopeKind       string  `json:"scope_kind"`
	TenantID        *string `json:"tenant_id"`
	RequestID       *string `json:"request_id"`
	AttemptID       *string `json:"attempt_id"`
	AccountTargetID *string `json:"account_target_id"`
	CreatedXID      string  `json:"created_xid"`
	SessionRoleOID  string  `json:"session_role_oid"`
	StepCount       string  `json:"step_count"`
	SemanticResult  string  `json:"semantic_result"`
}

// RootStepRow is every physical column of pp_root_steps.
type RootStepRow struct {
	RootID                 string  `json:"root_id"`
	StepNo                 string  `json:"step_no"`
	Kind                   string  `json:"kind"`
	SourceID               string  `json:"source_id"`
	CreatedXID             string  `json:"created_xid"`
	DRequestBirth          *string `json:"d_request_birth"`
	DAttemptBirth          *string `json:"d_attempt_birth"`
	DAccountCommand        *string `json:"d_account_command"`
	DAttemptDelivery       *string `json:"d_attempt_delivery"`
	DIncoming              *string `json:"d_incoming"`
	DMeterChoice           *string `json:"d_meter_choice"`
	DRequestDecision       *string `json:"d_request_decision"`
	DNormalCostDecision    *string `json:"d_normal_cost_decision"`
	DCostMutation          *string `json:"d_cost_mutation"`
	DCostResetDecision     *string `json:"d_cost_reset_decision"`
	DEvidenceChoice        *string `json:"d_evidence_choice"`
	DProcurementTransition *string `json:"d_procurement_transition"`
	DProposalBirth         *string `json:"d_proposal_birth"`
	DApprovalConsumed      *string `json:"d_approval_consumed"`
	DUnmanagedCorrection   *string `json:"d_unmanaged_correction"`
	DRecovery              *string `json:"d_recovery"`
	DCostOutbox            *string `json:"d_cost_outbox"`
	DReconciliation        *string `json:"d_reconciliation"`
}

// RequestOriginRow is every physical column of pp_request_origins.
type RequestOriginRow struct {
	RequestID             string  `json:"request_id"`
	RootID                string  `json:"root_id"`
	StepNo                string  `json:"step_no"`
	StepKind              string  `json:"step_kind"`
	CreatedXID            string  `json:"created_xid"`
	TenantID              string  `json:"tenant_id"`
	KeyID                 string  `json:"key_id"`
	OwnerID               string  `json:"owner_id"`
	ModelID               string  `json:"model_id"`
	PriceVersionID        string  `json:"price_version_id"`
	InputRate             string  `json:"input_rate"`
	OutputRate            string  `json:"output_rate"`
	InputUpperBound       string  `json:"input_upper_bound"`
	OutputLimit           string  `json:"output_limit"`
	ProfileVersion        string  `json:"profile_version"`
	PolicyVersion         string  `json:"policy_version"`
	RequestHMAC           string  `json:"request_hmac"`
	RouteRevision         string  `json:"route_revision"`
	RequestDeadline       Time    `json:"request_deadline"`
	InstanceID            string  `json:"instance_id"`
	Streaming             bool    `json:"streaming"`
	ClientIP              *string `json:"client_ip"`
	PricingGroupID        *string `json:"pricing_group_id"`
	TenantPricingRevision string  `json:"tenant_pricing_revision"`
	RetailPricedAt        Time    `json:"retail_priced_at"`
	RequestCreatedAt      Time    `json:"request_created_at"`
	PBCreatedXID          string  `json:"pb_created_xid"`
	PBTenantControl       *string `json:"pb_tenant_control"`
	PBKeyControl          *string `json:"pb_key_control"`
	PBAdmittedAt          Time    `json:"pb_admitted_at"`
	InitialLatestID       *string `json:"initial_latest_id"`
	InitialStatus         string  `json:"initial_status"`
	InitialBillingStatus  string  `json:"initial_billing_status"`
}

// RequestHeadRow is every physical column of pp_request_heads.
type RequestHeadRow struct {
	RequestID      string  `json:"request_id"`
	Ordinal        string  `json:"ordinal"`
	LastDecisionID *string `json:"last_decision_id"`
	UpdatedXID     string  `json:"updated_xid"`
}

// AttemptOriginRow is every physical column of pp_attempt_origins.
type AttemptOriginRow struct {
	AttemptID               string  `json:"attempt_id"`
	RootID                  string  `json:"root_id"`
	StepNo                  string  `json:"step_no"`
	StepKind                string  `json:"step_kind"`
	CreatedXID              string  `json:"created_xid"`
	TenantID                string  `json:"tenant_id"`
	RequestID               string  `json:"request_id"`
	Sequence                string  `json:"sequence"`
	ChannelID               string  `json:"channel_id"`
	ChannelVersionID        string  `json:"channel_version_id"`
	CredentialID            string  `json:"credential_id"`
	UpstreamModel           string  `json:"upstream_model"`
	RouteDecisions          string  `json:"route_decisions"`
	CostVersionID           *string `json:"cost_version_id"`
	CostInputRate           *string `json:"cost_input_rate"`
	CostOutputRate          *string `json:"cost_output_rate"`
	CostBasis               string  `json:"cost_basis"`
	QuoteSelectedAt         Time    `json:"quote_selected_at"`
	AttemptCreatedAt        Time    `json:"attempt_created_at"`
	PBCreatedXID            string  `json:"pb_created_xid"`
	InitialDeliveryState    string  `json:"initial_delivery_state"`
	InitialCostStatus       string  `json:"initial_cost_status"`
	InitialCostAmount       *string `json:"initial_cost_amount"`
	ManagementMode          string  `json:"management_mode"`
	AccountID               *string `json:"account_id"`
	BindingID               *string `json:"binding_id"`
	ProcurementTransitionID *string `json:"procurement_transition_id"`
	ReservedEventID         *string `json:"reserved_event_id"`
	AccountJournalID        *string `json:"account_journal_id"`
	AdmittedAt              Time    `json:"admitted_at"`
}

// AttemptHeadRow is every physical column of pp_attempt_heads.
type AttemptHeadRow struct {
	AttemptID    string  `json:"attempt_id"`
	Ordinal      string  `json:"ordinal"`
	LastNodeID   *string `json:"last_node_id"`
	MemberCount  *string `json:"member_count"`
	MemberTailID *string `json:"member_tail_id"`
	UpdatedXID   string  `json:"updated_xid"`
}

// AttemptNodeRow is every physical column of pp_attempt_nodes.
type AttemptNodeRow struct {
	ID              string  `json:"id"`
	AttemptID       string  `json:"attempt_id"`
	Ordinal         string  `json:"ordinal"`
	PreviousNodeID  *string `json:"previous_node_id"`
	RootID          string  `json:"root_id"`
	StepNo          string  `json:"step_no"`
	StepKind        string  `json:"step_kind"`
	SourceID        string  `json:"source_id"`
	CreatedXID      string  `json:"created_xid"`
	PreviousOrdinal string  `json:"previous_ordinal"`
}

// AccountOriginRow is every physical column of pp_account_origins.
type AccountOriginRow struct {
	AccountID         string `json:"account_id"`
	Version           string `json:"version"`
	CreateOperationID string `json:"create_operation_id"`
	CommandSourceID   string `json:"command_source_id"`
	RootID            string `json:"root_id"`
	StepNo            string `json:"step_no"`
	StepKind          string `json:"step_kind"`
	JournalID         string `json:"journal_id"`
	CreatedXID        string `json:"created_xid"`
	CreatedAt         Time   `json:"created_at"`
}

// AccountHeadRow is every physical column of pp_account_heads.
type AccountHeadRow struct {
	AccountID             string `json:"account_id"`
	Ordinal               string `json:"ordinal"`
	JournalID             string `json:"journal_id"`
	UpdatedXID            string `json:"updated_xid"`
	Label                 string `json:"label"`
	Provider              string `json:"provider"`
	State                 string `json:"state"`
	LimitAmount           string `json:"limit_amount"`
	ConfigurationRevision string `json:"configuration_revision"`
	HeldAmount            string `json:"held_amount"`
	SpentAmount           string `json:"spent_amount"`
	BlockingCount         string `json:"blocking_count"`
}

// AccountJournalRow is every physical column of pp_account_journal.
type AccountJournalRow struct {
	ID                           string  `json:"id"`
	AccountID                    string  `json:"account_id"`
	Ordinal                      string  `json:"ordinal"`
	PreviousID                   *string `json:"previous_id"`
	PreviousOrdinal              string  `json:"previous_ordinal"`
	RootID                       string  `json:"root_id"`
	StepNo                       string  `json:"step_no"`
	StepKind                     string  `json:"step_kind"`
	CreatedXID                   string  `json:"created_xid"`
	CreatedAt                    Time    `json:"created_at"`
	Kind                         string  `json:"kind"`
	SourceKind                   string  `json:"source_kind"`
	SourceID                     string  `json:"source_id"`
	BeforeLabel                  *string `json:"before_label"`
	BeforeProvider               *string `json:"before_provider"`
	BeforeState                  *string `json:"before_state"`
	BeforeLimitAmount            *string `json:"before_limit_amount"`
	BeforeConfigurationRevision  *string `json:"before_configuration_revision"`
	BeforeHeldAmount             *string `json:"before_held_amount"`
	BeforeSpentAmount            *string `json:"before_spent_amount"`
	BeforeBlockingCount          *string `json:"before_blocking_count"`
	AfterLabel                   string  `json:"after_label"`
	AfterProvider                string  `json:"after_provider"`
	AfterState                   string  `json:"after_state"`
	AfterLimitAmount             string  `json:"after_limit_amount"`
	AfterConfigurationRevision   string  `json:"after_configuration_revision"`
	AfterHeldAmount              string  `json:"after_held_amount"`
	AfterSpentAmount             string  `json:"after_spent_amount"`
	AfterBlockingCount           string  `json:"after_blocking_count"`
	AttemptID                    *string `json:"attempt_id"`
	BindingID                    *string `json:"binding_id"`
	BeforeReservationRevision    *string `json:"before_reservation_revision"`
	BeforeReservationState       *string `json:"before_reservation_state"`
	BeforeReservationHeldAmount  *string `json:"before_reservation_held_amount"`
	BeforeReservationSpentAmount *string `json:"before_reservation_spent_amount"`
	BeforeReservationAnomaly     *bool   `json:"before_reservation_anomaly"`
	AfterReservationRevision     *string `json:"after_reservation_revision"`
	AfterReservationState        *string `json:"after_reservation_state"`
	AfterReservationHeldAmount   *string `json:"after_reservation_held_amount"`
	AfterReservationSpentAmount  *string `json:"after_reservation_spent_amount"`
	AfterReservationAnomaly      *bool   `json:"after_reservation_anomaly"`
	InputBound                   *string `json:"input_bound"`
	OutputBound                  *string `json:"output_bound"`
	UpperAmount                  *string `json:"upper_amount"`
	CostVersionID                *string `json:"cost_version_id"`
	CostInputRate                *string `json:"cost_input_rate"`
	CostOutputRate               *string `json:"cost_output_rate"`
	CostBasis                    *string `json:"cost_basis"`
	GateExtra                    *string `json:"gate_extra"`
	GateCheckedAt                Time    `json:"gate_checked_at"`
	PBDecisionAt                 Time    `json:"pb_decision_at"`
	PauseEventID                 *string `json:"pause_event_id"`
	CommandSourceID              *string `json:"command_source_id"`
	TransitionID                 *string `json:"transition_id"`
	DeliverySourceID             *string `json:"delivery_source_id"`
	CommandOperationID           *string `json:"command_operation_id"`
	ProcurementEventID           *string `json:"procurement_event_id"`
	DispatchAttemptID            *string `json:"dispatch_attempt_id"`
}

// AccountCommandSourceRow is every physical column of pp_account_command_sources.
type AccountCommandSourceRow struct {
	ID                    string  `json:"id"`
	RootID                string  `json:"root_id"`
	StepNo                string  `json:"step_no"`
	StepKind              string  `json:"step_kind"`
	CreatedXID            string  `json:"created_xid"`
	CreatedAt             Time    `json:"created_at"`
	OperationID           string  `json:"operation_id"`
	ActorID               string  `json:"actor_id"`
	ActorRole             string  `json:"actor_role"`
	ActorStatus           string  `json:"actor_status"`
	Kind                  string  `json:"kind"`
	InputTargetID         string  `json:"input_target_id"`
	TargetID              string  `json:"target_id"`
	InputLabel            string  `json:"input_label"`
	InputProvider         string  `json:"input_provider"`
	InputState            string  `json:"input_state"`
	InputLimitAmount      string  `json:"input_limit_amount"`
	InputDigest           string  `json:"input_digest"`
	CommandStatus         string  `json:"command_status"`
	CommandReason         string  `json:"command_reason"`
	CommandCreatedAt      Time    `json:"command_created_at"`
	AuditID               string  `json:"audit_id"`
	InputExpectedRevision *string `json:"input_expected_revision"`
	JournalID             *string `json:"journal_id"`
}

// AttemptDeliverySourceRow is every physical column of pp_attempt_delivery_sources.
type AttemptDeliverySourceRow struct {
	ID                  string  `json:"id"`
	RootID              string  `json:"root_id"`
	StepNo              string  `json:"step_no"`
	StepKind            string  `json:"step_kind"`
	CreatedXID          string  `json:"created_xid"`
	TenantID            string  `json:"tenant_id"`
	RequestID           string  `json:"request_id"`
	AttemptID           string  `json:"attempt_id"`
	Mode                string  `json:"mode"`
	CauseSourceID       *string `json:"cause_source_id"`
	BeforeDeliveryState string  `json:"before_delivery_state"`
	AfterDeliveryState  string  `json:"after_delivery_state"`
	DispatchAccountID   *string `json:"dispatch_account_id"`
	DispatchJournalID   *string `json:"dispatch_journal_id"`
	DispatchXID         *string `json:"dispatch_xid"`
	PBDispatchCheckedAt Time    `json:"pb_dispatch_checked_at"`
	DFinalization       *string `json:"d_finalization"`
	DRecovery           *string `json:"d_recovery"`
}

// IncomingSourceRow is every physical column of pp_incoming_sources.
type IncomingSourceRow struct {
	ID                  string  `json:"id"`
	RootID              string  `json:"root_id"`
	StepNo              string  `json:"step_no"`
	StepKind            string  `json:"step_kind"`
	CreatedXID          string  `json:"created_xid"`
	TenantID            string  `json:"tenant_id"`
	RequestID           string  `json:"request_id"`
	AttemptID           *string `json:"attempt_id"`
	InputFinalizationID string  `json:"input_finalization_id"`
	InputDigest         string  `json:"input_digest"`
	Source              string  `json:"source"`
	SourceEventID       string  `json:"source_event_id"`
	UsageStatus         string  `json:"usage_status"`
	InputTokens         string  `json:"input_tokens"`
	OutputTokens        string  `json:"output_tokens"`
	TotalTokens         string  `json:"total_tokens"`
	CachedTokens        string  `json:"cached_tokens"`
	ReasoningTokens     string  `json:"reasoning_tokens"`
	ProtocolOutcome     string  `json:"protocol_outcome"`
	TransportEndReason  string  `json:"transport_end_reason"`
	RawDeliveryStage    string  `json:"raw_delivery_stage"`
	RawContentWritten   bool    `json:"raw_content_written"`
	FinishReason        string  `json:"finish_reason"`
	ObservedAtInput     Time    `json:"observed_at_input"`
	EffectiveObservedAt Time    `json:"effective_observed_at"`
	ReconcileSourceID   *string `json:"reconcile_source_id"`
}

// MeterChoiceRow is every physical column of pp_meter_choices.
type MeterChoiceRow struct {
	ID                     string  `json:"id"`
	RootID                 string  `json:"root_id"`
	StepNo                 string  `json:"step_no"`
	StepKind               string  `json:"step_kind"`
	CreatedXID             string  `json:"created_xid"`
	TenantID               string  `json:"tenant_id"`
	RequestID              string  `json:"request_id"`
	AttemptID              *string `json:"attempt_id"`
	IncomingSourceID       string  `json:"incoming_source_id"`
	MeterEventID           string  `json:"meter_event_id"`
	MeterOriginID          string  `json:"meter_origin_id"`
	Disposition            string  `json:"disposition"`
	AttemptCutBeforeID     *string `json:"attempt_cut_before_id"`
	AttemptCutAfterMeterID *string `json:"attempt_cut_after_meter_id"`
	CollisionKind          string  `json:"collision_kind"`
	CompetingMeterID       *string `json:"competing_meter_id"`
}

// MeterOriginRow is every physical column of pp_meter_origins.
type MeterOriginRow struct {
	MeterEventID     string `json:"meter_event_id"`
	IncomingSourceID string `json:"incoming_source_id"`
	RootID           string `json:"root_id"`
	StepNo           string `json:"step_no"`
	StepKind         string `json:"step_kind"`
	SourceID         string `json:"source_id"`
	CreatedXID       string `json:"created_xid"`
}

// ConflictSourceRow is every physical column of pp_conflict_sources.
type ConflictSourceRow struct {
	ConflictID       string  `json:"conflict_id"`
	RootID           string  `json:"root_id"`
	StepNo           string  `json:"step_no"`
	StepKind         string  `json:"step_kind"`
	SourceID         string  `json:"source_id"`
	CreatedXID       string  `json:"created_xid"`
	TenantID         string  `json:"tenant_id"`
	RequestID        string  `json:"request_id"`
	AttemptID        *string `json:"attempt_id"`
	IncomingSourceID string  `json:"incoming_source_id"`
	MeterChoiceID    string  `json:"meter_choice_id"`
}

// RequestDecisionRow is every physical column of pp_request_decisions.
type RequestDecisionRow struct {
	ID                       string  `json:"id"`
	RootID                   string  `json:"root_id"`
	StepNo                   string  `json:"step_no"`
	StepKind                 string  `json:"step_kind"`
	CreatedXID               string  `json:"created_xid"`
	TenantID                 string  `json:"tenant_id"`
	RequestID                string  `json:"request_id"`
	Ordinal                  string  `json:"ordinal"`
	PreviousDecisionID       *string `json:"previous_decision_id"`
	IncomingSourceID         string  `json:"incoming_source_id"`
	MeterChoiceID            string  `json:"meter_choice_id"`
	BeforeLatestID           *string `json:"before_latest_id"`
	AfterLatestID            *string `json:"after_latest_id"`
	BeforeStatus             string  `json:"before_status"`
	AfterStatus              string  `json:"after_status"`
	NormalizedDeliveryStage  string  `json:"normalized_delivery_stage"`
	NormalizedContentWritten bool    `json:"normalized_content_written"`
	Branch                   string  `json:"branch"`
	FinalizationID           *string `json:"finalization_id"`
	ConflictSourceID         *string `json:"conflict_source_id"`
	PreviousOrdinal          string  `json:"previous_ordinal"`
}

// NormalCostDecisionRow is every physical column of pp_normal_cost_decisions.
type NormalCostDecisionRow struct {
	ID                        string  `json:"id"`
	RootID                    string  `json:"root_id"`
	StepNo                    string  `json:"step_no"`
	StepKind                  string  `json:"step_kind"`
	CreatedXID                string  `json:"created_xid"`
	TenantID                  string  `json:"tenant_id"`
	RequestID                 string  `json:"request_id"`
	AttemptID                 string  `json:"attempt_id"`
	BeforeCostStatus          string  `json:"before_cost_status"`
	BeforeCostAmount          *string `json:"before_cost_amount"`
	ManagementMode            string  `json:"management_mode"`
	BeforeProcurementRevision *string `json:"before_procurement_revision"`
	BeforeProcurementState    *string `json:"before_procurement_state"`
	BeforeProcurementHeld     *string `json:"before_procurement_held"`
	BeforeProcurementSpent    *string `json:"before_procurement_spent"`
	BeforeProcurementAnomaly  *bool   `json:"before_procurement_anomaly"`
	MemberCountBefore         *string `json:"member_count_before"`
	MemberTailBefore          *string `json:"member_tail_before"`
	AccountID                 *string `json:"account_id"`
	AccountOrdinalCut         *string `json:"account_ordinal_cut"`
	AccountTailCut            *string `json:"account_tail_cut"`
	FinalizationID            string  `json:"finalization_id"`
	IncomingSourceID          string  `json:"incoming_source_id"`
	MeterChoiceID             string  `json:"meter_choice_id"`
	Decision                  string  `json:"decision"`
	SelectedAmount            *string `json:"selected_amount"`
	SelectedBasis             *string `json:"selected_basis"`
	CostMutationID            *string `json:"cost_mutation_id"`
	CostRecordedOutboxID      *string `json:"cost_recorded_outbox_id"`
}

// CostMutationRow is every physical column of pp_cost_mutations.
type CostMutationRow struct {
	ID                       string  `json:"id"`
	RootID                   string  `json:"root_id"`
	StepNo                   string  `json:"step_no"`
	StepKind                 string  `json:"step_kind"`
	CreatedXID               string  `json:"created_xid"`
	TenantID                 string  `json:"tenant_id"`
	RequestID                string  `json:"request_id"`
	AttemptID                string  `json:"attempt_id"`
	CauseKind                string  `json:"cause_kind"`
	CauseSourceID            string  `json:"cause_source_id"`
	BeforeStatus             string  `json:"before_status"`
	BeforeAmount             *string `json:"before_amount"`
	AfterStatus              string  `json:"after_status"`
	AfterAmount              *string `json:"after_amount"`
	DNormalCostDecision      *string `json:"d_normal_cost_decision"`
	DProcurementDisputeReset *string `json:"d_procurement_dispute_reset"`
	DIncomingConflictReset   *string `json:"d_incoming_conflict_reset"`
	DApprovalConsumed        *string `json:"d_approval_consumed"`
	DUnmanagedCorrection     *string `json:"d_unmanaged_correction"`
}

// CostResetDecisionRow is every physical column of pp_cost_reset_decisions.
type CostResetDecisionRow struct {
	ID                     string  `json:"id"`
	RootID                 string  `json:"root_id"`
	StepNo                 string  `json:"step_no"`
	StepKind               string  `json:"step_kind"`
	CreatedXID             string  `json:"created_xid"`
	TenantID               string  `json:"tenant_id"`
	RequestID              string  `json:"request_id"`
	AttemptID              string  `json:"attempt_id"`
	CauseKind              string  `json:"cause_kind"`
	CauseSourceID          string  `json:"cause_source_id"`
	ProcurementStateBefore *string `json:"procurement_state_before"`
	Policy                 string  `json:"policy"`
	CostMutationID         *string `json:"cost_mutation_id"`
	DProcurementDispute    *string `json:"d_procurement_dispute"`
	DIncomingConflict      *string `json:"d_incoming_conflict"`
	MutationCauseKind      *string `json:"mutation_cause_kind"`
}

// EvidenceChoiceRow is every physical column of pp_evidence_choices.
type EvidenceChoiceRow struct {
	ID                        string  `json:"id"`
	RootID                    string  `json:"root_id"`
	StepNo                    string  `json:"step_no"`
	StepKind                  string  `json:"step_kind"`
	CreatedXID                string  `json:"created_xid"`
	TenantID                  string  `json:"tenant_id"`
	RequestID                 string  `json:"request_id"`
	AttemptID                 string  `json:"attempt_id"`
	IncomingSourceID          string  `json:"incoming_source_id"`
	MeterChoiceID             string  `json:"meter_choice_id"`
	EvidenceDigest            string  `json:"evidence_digest"`
	ProcurementBeforeRevision string  `json:"procurement_before_revision"`
	ProcurementBeforeState    string  `json:"procurement_before_state"`
	ProcurementBeforeHeld     string  `json:"procurement_before_held"`
	ProcurementBeforeSpent    string  `json:"procurement_before_spent"`
	ProcurementBeforeAnomaly  bool    `json:"procurement_before_anomaly"`
	AccountID                 string  `json:"account_id"`
	AccountOrdinalCut         string  `json:"account_ordinal_cut"`
	AccountTailCut            string  `json:"account_tail_cut"`
	Disposition               string  `json:"disposition"`
	IdentityAttemptID         string  `json:"identity_attempt_id"`
	IdentityDigest            string  `json:"identity_digest"`
	IdentityEventID           string  `json:"identity_event_id"`
	MemberCountBefore         string  `json:"member_count_before"`
	MemberTailBefore          *string `json:"member_tail_before"`
	MemberCountAfter          string  `json:"member_count_after"`
	MemberTailAfter           *string `json:"member_tail_after"`
	ForcedConflict            bool    `json:"forced_conflict"`
	EffectiveConflict         *bool   `json:"effective_conflict"`
	ConflictCause             *string `json:"conflict_cause"`
	Decision                  string  `json:"decision"`
	ExistingDisputeEventID    *string `json:"existing_dispute_event_id"`
	ProcurementTransitionID   *string `json:"procurement_transition_id"`
	ProcurementEventID        *string `json:"procurement_event_id"`
	AccountJournalID          *string `json:"account_journal_id"`
	SkipReason                *string `json:"skip_reason"`
}

// EvidenceMemberRow is every physical column of pp_evidence_members.
type EvidenceMemberRow struct {
	ID                    string  `json:"id"`
	AttemptID             string  `json:"attempt_id"`
	MemberOrdinal         string  `json:"member_ordinal"`
	PreviousMemberID      *string `json:"previous_member_id"`
	IdentityDigest        string  `json:"identity_digest"`
	IdentityEventID       string  `json:"identity_event_id"`
	EvidenceChoiceID      string  `json:"evidence_choice_id"`
	RootID                string  `json:"root_id"`
	StepNo                string  `json:"step_no"`
	StepKind              string  `json:"step_kind"`
	SourceID              string  `json:"source_id"`
	CreatedXID            string  `json:"created_xid"`
	PreviousMemberOrdinal string  `json:"previous_member_ordinal"`
}

// ProcurementTransitionRow is every physical column of pp_procurement_transitions.
type ProcurementTransitionRow struct {
	ID                string  `json:"id"`
	RootID            string  `json:"root_id"`
	StepNo            string  `json:"step_no"`
	StepKind          string  `json:"step_kind"`
	CreatedXID        string  `json:"created_xid"`
	CreatedAt         Time    `json:"created_at"`
	TenantID          string  `json:"tenant_id"`
	RequestID         string  `json:"request_id"`
	AttemptID         string  `json:"attempt_id"`
	AccountID         string  `json:"account_id"`
	BindingID         string  `json:"binding_id"`
	CauseKind         string  `json:"cause_kind"`
	CauseSourceID     string  `json:"cause_source_id"`
	EventID           string  `json:"event_id"`
	JournalID         string  `json:"journal_id"`
	EventType         string  `json:"event_type"`
	EventKey          string  `json:"event_key"`
	EvidenceRef       string  `json:"evidence_ref"`
	HeldDelta         string  `json:"held_delta"`
	SpentDelta        string  `json:"spent_delta"`
	BeforeRevision    *string `json:"before_revision"`
	BeforeState       *string `json:"before_state"`
	BeforeHeld        *string `json:"before_held"`
	BeforeSpent       *string `json:"before_spent"`
	BeforeAnomaly     *bool   `json:"before_anomaly"`
	AfterRevision     string  `json:"after_revision"`
	AfterState        string  `json:"after_state"`
	AfterHeld         string  `json:"after_held"`
	AfterSpent        string  `json:"after_spent"`
	AfterAnomaly      bool    `json:"after_anomaly"`
	InputBound        string  `json:"input_bound"`
	OutputBound       string  `json:"output_bound"`
	UpperAmount       string  `json:"upper_amount"`
	CostVersionID     string  `json:"cost_version_id"`
	CostInputRate     string  `json:"cost_input_rate"`
	CostOutputRate    string  `json:"cost_output_rate"`
	CostBasis         string  `json:"cost_basis"`
	DAttemptBirth     *string `json:"d_attempt_birth"`
	DEvidenceChoice   *string `json:"d_evidence_choice"`
	DApprovalConsumed *string `json:"d_approval_consumed"`
	DRecovery         *string `json:"d_recovery"`
}

// ProposalBirthRow is every physical column of pp_proposal_births.
type ProposalBirthRow struct {
	ID                  string  `json:"id"`
	RootID              string  `json:"root_id"`
	StepNo              string  `json:"step_no"`
	StepKind            string  `json:"step_kind"`
	CreatedXID          string  `json:"created_xid"`
	TenantID            string  `json:"tenant_id"`
	RequestID           string  `json:"request_id"`
	AttemptID           string  `json:"attempt_id"`
	ProposalID          string  `json:"proposal_id"`
	Requester           string  `json:"requester"`
	RequesterRole       string  `json:"requester_role"`
	RequesterStatus     string  `json:"requester_status"`
	ReservationRevision string  `json:"reservation_revision"`
	ReservationState    string  `json:"reservation_state"`
	ReservationHeld     string  `json:"reservation_held"`
	ReservationSpent    string  `json:"reservation_spent"`
	ReservationAnomaly  bool    `json:"reservation_anomaly"`
	MemberCount         string  `json:"member_count"`
	MemberTail          *string `json:"member_tail"`
	AccountID           string  `json:"account_id"`
	AccountOrdinalCut   string  `json:"account_ordinal_cut"`
	AccountTailCut      string  `json:"account_tail_cut"`
	AuditID             string  `json:"audit_id"`
}

// ApprovalConsumptionRow is every physical column of pp_approval_consumptions.
type ApprovalConsumptionRow struct {
	ID                      string  `json:"id"`
	RootID                  string  `json:"root_id"`
	StepNo                  string  `json:"step_no"`
	StepKind                string  `json:"step_kind"`
	CreatedXID              string  `json:"created_xid"`
	TenantID                string  `json:"tenant_id"`
	RequestID               string  `json:"request_id"`
	AttemptID               string  `json:"attempt_id"`
	ProposalID              string  `json:"proposal_id"`
	ProposalBirthID         string  `json:"proposal_birth_id"`
	ApprovalOperationID     string  `json:"approval_operation_id"`
	Requester               string  `json:"requester"`
	RequesterRole           string  `json:"requester_role"`
	RequesterStatus         string  `json:"requester_status"`
	Approver                string  `json:"approver"`
	ApproverRole            string  `json:"approver_role"`
	ApproverStatus          string  `json:"approver_status"`
	ConsumedRevision        string  `json:"consumed_revision"`
	ConsumedState           string  `json:"consumed_state"`
	ConsumedHeld            string  `json:"consumed_held"`
	ConsumedSpent           string  `json:"consumed_spent"`
	ConsumedAnomaly         bool    `json:"consumed_anomaly"`
	ConsumedMemberCount     string  `json:"consumed_member_count"`
	ConsumedMemberTail      *string `json:"consumed_member_tail"`
	BeforeCostStatus        string  `json:"before_cost_status"`
	BeforeCostAmount        *string `json:"before_cost_amount"`
	AccountID               string  `json:"account_id"`
	AccountOrdinalCut       string  `json:"account_ordinal_cut"`
	AccountTailCut          string  `json:"account_tail_cut"`
	ProcurementTransitionID string  `json:"procurement_transition_id"`
	ProcurementEventID      string  `json:"procurement_event_id"`
	ProviderCorrectionID    string  `json:"provider_correction_id"`
	CostMutationID          string  `json:"cost_mutation_id"`
	OutboxID                string  `json:"outbox_id"`
	AuditID                 string  `json:"audit_id"`
	AfterRevision           string  `json:"after_revision"`
	AfterState              string  `json:"after_state"`
	AfterHeld               string  `json:"after_held"`
	AfterSpent              string  `json:"after_spent"`
	AfterAnomaly            bool    `json:"after_anomaly"`
}

// UnmanagedCorrectionSourceRow is every physical column of pp_unmanaged_correction_sources.
type UnmanagedCorrectionSourceRow struct {
	ID                   string  `json:"id"`
	RootID               string  `json:"root_id"`
	StepNo               string  `json:"step_no"`
	StepKind             string  `json:"step_kind"`
	CreatedXID           string  `json:"created_xid"`
	TenantID             string  `json:"tenant_id"`
	RequestID            string  `json:"request_id"`
	AttemptID            string  `json:"attempt_id"`
	OperationID          string  `json:"operation_id"`
	ProviderCorrectionID string  `json:"provider_correction_id"`
	InputActorID         *string `json:"input_actor_id"`
	EffectiveAuditActor  string  `json:"effective_audit_actor"`
	Amount               string  `json:"amount"`
	Basis                string  `json:"basis"`
	EvidenceRef          string  `json:"evidence_ref"`
	BeforeCostStatus     string  `json:"before_cost_status"`
	BeforeCostAmount     *string `json:"before_cost_amount"`
	AfterCostStatus      string  `json:"after_cost_status"`
	AfterCostAmount      string  `json:"after_cost_amount"`
	CostMutationID       string  `json:"cost_mutation_id"`
	AuditID              string  `json:"audit_id"`
	OutboxID             string  `json:"outbox_id"`
}

// RecoverySourceRow is every physical column of pp_recovery_sources.
type RecoverySourceRow struct {
	ID                        string  `json:"id"`
	RootID                    string  `json:"root_id"`
	StepNo                    string  `json:"step_no"`
	StepKind                  string  `json:"step_kind"`
	CreatedXID                string  `json:"created_xid"`
	TenantID                  string  `json:"tenant_id"`
	RequestID                 string  `json:"request_id"`
	AttemptID                 string  `json:"attempt_id"`
	CheckedAt                 Time    `json:"checked_at"`
	RequestDeadline           Time    `json:"request_deadline"`
	ProcurementBeforeRevision string  `json:"procurement_before_revision"`
	ProcurementBeforeState    string  `json:"procurement_before_state"`
	ProcurementBeforeHeld     string  `json:"procurement_before_held"`
	ProcurementBeforeSpent    string  `json:"procurement_before_spent"`
	ProcurementBeforeAnomaly  bool    `json:"procurement_before_anomaly"`
	DeliveryBeforeState       string  `json:"delivery_before_state"`
	PermitSourceID            *string `json:"permit_source_id"`
	Decision                  string  `json:"decision"`
	ProcurementTransitionID   string  `json:"procurement_transition_id"`
	DeliveryMutationID        string  `json:"delivery_mutation_id"`
	ProcurementEventID        string  `json:"procurement_event_id"`
	AccountJournalID          string  `json:"account_journal_id"`
}

// CostOutboxLinkRow is every physical column of pp_cost_outbox_links.
type CostOutboxLinkRow struct {
	ID                   string  `json:"id"`
	RootID               string  `json:"root_id"`
	StepNo               string  `json:"step_no"`
	StepKind             string  `json:"step_kind"`
	CreatedXID           string  `json:"created_xid"`
	TenantID             string  `json:"tenant_id"`
	RequestID            string  `json:"request_id"`
	AttemptID            string  `json:"attempt_id"`
	CauseKind            string  `json:"cause_kind"`
	CauseSourceID        string  `json:"cause_source_id"`
	OutboxID             string  `json:"outbox_id"`
	DNormalCostDecision  *string `json:"d_normal_cost_decision"`
	DApprovalConsumed    *string `json:"d_approval_consumed"`
	DUnmanagedCorrection *string `json:"d_unmanaged_correction"`
}

// ReconciliationSourceRow is every physical column of pp_reconciliation_sources.
type ReconciliationSourceRow struct {
	ID                      string  `json:"id"`
	RootID                  string  `json:"root_id"`
	StepNo                  string  `json:"step_no"`
	StepKind                string  `json:"step_kind"`
	CreatedXID              string  `json:"created_xid"`
	TenantID                string  `json:"tenant_id"`
	RequestID               string  `json:"request_id"`
	CaseID                  string  `json:"case_id"`
	Action                  string  `json:"action"`
	Actor                   string  `json:"actor"`
	ActorRole               string  `json:"actor_role"`
	ActorStatus             string  `json:"actor_status"`
	InputUsageStatus        *string `json:"input_usage_status"`
	InputInputTokens        *string `json:"input_input_tokens"`
	InputOutputTokens       *string `json:"input_output_tokens"`
	InputTotalTokens        *string `json:"input_total_tokens"`
	InputCachedTokens       *string `json:"input_cached_tokens"`
	InputReasoningTokens    *string `json:"input_reasoning_tokens"`
	InputEvidenceRef        *string `json:"input_evidence_ref"`
	InputReason             *string `json:"input_reason"`
	BeforeCaseReason        string  `json:"before_case_reason"`
	CaseCreatedAt           Time    `json:"case_created_at"`
	BeforeCaseState         string  `json:"before_case_state"`
	AfterCaseState          string  `json:"after_case_state"`
	BeforeReservationStatus string  `json:"before_reservation_status"`
	BeforeRequester         *string `json:"before_requester"`
	BeforeApprover          *string `json:"before_approver"`
	RequesterRole           *string `json:"requester_role"`
	RequesterStatus         *string `json:"requester_status"`
	AfterRequester          *string `json:"after_requester"`
	AfterApprover           *string `json:"after_approver"`
	CaseUsageStatus         *string `json:"case_usage_status"`
	CaseInputTokens         *string `json:"case_input_tokens"`
	CaseOutputTokens        *string `json:"case_output_tokens"`
	CaseTotalTokens         *string `json:"case_total_tokens"`
	CaseCachedTokens        *string `json:"case_cached_tokens"`
	CaseReasoningTokens     *string `json:"case_reasoning_tokens"`
	CaseEvidenceRef         *string `json:"case_evidence_ref"`
	CaseProposalReason      *string `json:"case_proposal_reason"`
	ApprovalCheckedAt       Time    `json:"approval_checked_at"`
	ReservationDeadline     Time    `json:"reservation_deadline"`
	PriorLatestID           *string `json:"prior_latest_id"`
	DerivedIncomingSourceID *string `json:"derived_incoming_source_id"`
	FinalizationID          *string `json:"finalization_id"`
	CustomerFinishOutcomeID *string `json:"customer_finish_outcome_id"`
	AuditID                 string  `json:"audit_id"`
}

// CustomerFinishOutcomeRow is every physical column of pp_customer_finish_outcomes.
type CustomerFinishOutcomeRow struct {
	ID                          string  `json:"id"`
	RootID                      string  `json:"root_id"`
	StepNo                      string  `json:"step_no"`
	StepKind                    string  `json:"step_kind"`
	SourceID                    string  `json:"source_id"`
	CreatedXID                  string  `json:"created_xid"`
	TenantID                    string  `json:"tenant_id"`
	RequestID                   string  `json:"request_id"`
	ReconciliationSourceID      string  `json:"reconciliation_source_id"`
	CaseID                      string  `json:"case_id"`
	LatestFinalizationID        *string `json:"latest_finalization_id"`
	CustomerReservationID       string  `json:"customer_reservation_id"`
	ReservationAmount           string  `json:"reservation_amount"`
	ReservationDeadline         Time    `json:"reservation_deadline"`
	ResultKind                  string  `json:"result_kind"`
	SelectedMode                string  `json:"selected_mode"`
	SelectedReason              string  `json:"selected_reason"`
	SettlementDecisionCheckedAt Time    `json:"settlement_decision_checked_at"`
	FinancialCheckedAt          Time    `json:"financial_checked_at"`
	BeforeCaseState             string  `json:"before_case_state"`
	AfterCaseState              string  `json:"after_case_state"`
	BeforeCaseReason            string  `json:"before_case_reason"`
	AfterCaseReason             string  `json:"after_case_reason"`
	BeforeReservationStatus     string  `json:"before_reservation_status"`
	AfterReservationStatus      string  `json:"after_reservation_status"`
	BeforeRequestStatus         string  `json:"before_request_status"`
	AfterRequestStatus          string  `json:"after_request_status"`
	BeforeBillingStatus         string  `json:"before_billing_status"`
	AfterBillingStatus          string  `json:"after_billing_status"`
	BeforeKeyHeld               string  `json:"before_key_held"`
	BeforeKeySpent              string  `json:"before_key_spent"`
	AfterKeyHeld                string  `json:"after_key_held"`
	AfterKeySpent               string  `json:"after_key_spent"`
	RawAmount                   string  `json:"raw_amount"`
	ChargedAmount               string  `json:"charged_amount"`
	WaivedAmount                string  `json:"waived_amount"`
	ResultStatus                string  `json:"result_status"`
	SettlementID                *string `json:"settlement_id"`
	CustomerJournalID           *string `json:"customer_journal_id"`
	CustomerTenantControlID     *string `json:"customer_tenant_control_id"`
	CustomerKeyControlID        *string `json:"customer_key_control_id"`
	BillingAuditID              *string `json:"billing_audit_id"`
	BillingOutboxID             *string `json:"billing_outbox_id"`
	ProfileBlockModelID         *string `json:"profile_block_model_id"`
	ProfileBlockProfileVersion  *string `json:"profile_block_profile_version"`
	ProfileBlockDisposition     *string `json:"profile_block_disposition"`
	ProfileBlockAuditID         *string `json:"profile_block_audit_id"`
	DNewSettlement              *string `json:"d_new_settlement"`
}

// CustomerFinishLedgerEffectRow is every physical column of pp_customer_finish_ledger_effects.
type CustomerFinishLedgerEffectRow struct {
	FinishOutcomeID string  `json:"finish_outcome_id"`
	RootID          string  `json:"root_id"`
	StepNo          string  `json:"step_no"`
	StepKind        string  `json:"step_kind"`
	SourceID        string  `json:"source_id"`
	CreatedXID      string  `json:"created_xid"`
	TenantID        string  `json:"tenant_id"`
	AccountID       string  `json:"account_id"`
	AccountTenantID *string `json:"account_tenant_id"`
	AccountKind     string  `json:"account_kind"`
	BeforeBalance   string  `json:"before_balance"`
	AfterBalance    string  `json:"after_balance"`
	SignedDelta     string  `json:"signed_delta"`
	JournalLineID   string  `json:"journal_line_id"`
}

// CustomerFinishPBEffectRow is every physical column of pp_customer_finish_pb_effects.
type CustomerFinishPBEffectRow struct {
	FinishOutcomeID    string `json:"finish_outcome_id"`
	RootID             string `json:"root_id"`
	StepNo             string `json:"step_no"`
	StepKind           string `json:"step_kind"`
	SourceID           string `json:"source_id"`
	CreatedXID         string `json:"created_xid"`
	Scope              string `json:"scope"`
	ControlID          string `json:"control_id"`
	AllocationID       string `json:"allocation_id"`
	EventID            string `json:"event_id"`
	OriginalSourceKind string `json:"original_source_kind"`
	OriginalSourceID   string `json:"original_source_id"`
}
