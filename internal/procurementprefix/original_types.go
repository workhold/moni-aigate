// Original DTOs contain the complete fixed safe core projection. No credentials,
// passwords, provider keys, MFA material, SQL, or caller reports are accepted.
package procurementprefix

type OriginalRows struct {
	Channels                            []ChannelRow                            `json:"channels"`
	ChannelCredentials                  []ChannelCredentialRow                  `json:"channel_credentials"`
	ChannelVersions                     []ChannelVersionRow                     `json:"channel_versions"`
	ChannelVersionCredentials           []ChannelVersionCredentialRow           `json:"channel_version_credentials"`
	Tenants                             []TenantRow                             `json:"tenants"`
	RequestRecords                      []RequestRecordRow                      `json:"request_records"`
	CostVersions                        []CostVersionRow                        `json:"cost_versions"`
	ProcurementAccounts                 []ProcurementAccountRow                 `json:"procurement_accounts"`
	CredentialProcurementBindings       []CredentialProcurementBindingRow       `json:"credential_procurement_bindings"`
	UpstreamAttempts                    []UpstreamAttemptRow                    `json:"upstream_attempts"`
	ProcurementReservations             []ProcurementReservationRow             `json:"procurement_reservations"`
	ProcurementBudgetEvents             []ProcurementBudgetEventRow             `json:"procurement_budget_events"`
	Users                               []UserRow                               `json:"users"`
	ProviderCostCorrections             []ProviderCostCorrectionRow             `json:"provider_cost_corrections"`
	ProcurementCostProposals            []ProcurementCostProposalRow            `json:"procurement_cost_proposals"`
	ProcurementCommands                 []ProcurementCommandRow                 `json:"procurement_commands"`
	ProcurementBalanceObservations      []ProcurementBalanceObservationRow      `json:"procurement_balance_observations"`
	AuditEvents                         []AuditEventRow                         `json:"audit_events"`
	ProjectionOutbox                    []ProjectionOutboxRow                   `json:"projection_outbox"`
	ProcurementEvidenceIdentities       []ProcurementEvidenceIdentityRow        `json:"procurement_evidence_identities"`
	MeteringEvents                      []MeteringEventRow                      `json:"metering_events"`
	MeteringEventConflicts              []MeteringEventConflictRow              `json:"metering_event_conflicts"`
	FinalizationRecords                 []FinalizationRecordRow                 `json:"finalization_records"`
	Reservations                        []ReservationRow                        `json:"reservations"`
	Settlements                         []SettlementRow                         `json:"settlements"`
	ReconciliationCases                 []ReconciliationCaseRow                 `json:"reconciliation_cases"`
	BillingProfileBlocks                []BillingProfileBlockRow                `json:"billing_profile_blocks"`
	LedgerAccounts                      []LedgerAccountRow                      `json:"ledger_accounts"`
	Journals                            []JournalRow                            `json:"journals"`
	JournalLines                        []JournalLineRow                        `json:"journal_lines"`
	APIKeys                             []APIKeyRow                             `json:"api_keys"`
	PeriodicBudgetControls              []PeriodicBudgetControlRow              `json:"periodic_budget_controls"`
	PeriodicBudgetWindows               []PeriodicBudgetWindowRow               `json:"periodic_budget_windows"`
	PeriodicBudgetAllocations           []PeriodicBudgetAllocationRow           `json:"periodic_budget_allocations"`
	PeriodicBudgetEvents                []PeriodicBudgetEventRow                `json:"periodic_budget_events"`
	PeriodicBudgetOperations            []PeriodicBudgetOperationRow            `json:"periodic_budget_operations"`
	PeriodicBudgetConfigurationVersions []PeriodicBudgetConfigurationVersionRow `json:"periodic_budget_configuration_versions"`
	PeriodicBudgetObservationOrigins    []PeriodicBudgetObservationOriginRow    `json:"periodic_budget_observation_origins"`
	PeriodicBudgetObservationHeads      []PeriodicBudgetObservationHeadRow      `json:"periodic_budget_observation_heads"`
	PeriodicBudgetObservationJournal    []PeriodicBudgetObservationJournalRow   `json:"periodic_budget_observation_journal"`
}

type OriginalScan struct {
	Channels                            uint64 `json:"channels"`
	ChannelCredentials                  uint64 `json:"channel_credentials"`
	ChannelVersions                     uint64 `json:"channel_versions"`
	ChannelVersionCredentials           uint64 `json:"channel_version_credentials"`
	Tenants                             uint64 `json:"tenants"`
	RequestRecords                      uint64 `json:"request_records"`
	CostVersions                        uint64 `json:"cost_versions"`
	ProcurementAccounts                 uint64 `json:"procurement_accounts"`
	CredentialProcurementBindings       uint64 `json:"credential_procurement_bindings"`
	UpstreamAttempts                    uint64 `json:"upstream_attempts"`
	ProcurementReservations             uint64 `json:"procurement_reservations"`
	ProcurementBudgetEvents             uint64 `json:"procurement_budget_events"`
	Users                               uint64 `json:"users"`
	ProviderCostCorrections             uint64 `json:"provider_cost_corrections"`
	ProcurementCostProposals            uint64 `json:"procurement_cost_proposals"`
	ProcurementCommands                 uint64 `json:"procurement_commands"`
	ProcurementBalanceObservations      uint64 `json:"procurement_balance_observations"`
	AuditEvents                         uint64 `json:"audit_events"`
	ProjectionOutbox                    uint64 `json:"projection_outbox"`
	ProcurementEvidenceIdentities       uint64 `json:"procurement_evidence_identities"`
	MeteringEvents                      uint64 `json:"metering_events"`
	MeteringEventConflicts              uint64 `json:"metering_event_conflicts"`
	FinalizationRecords                 uint64 `json:"finalization_records"`
	Reservations                        uint64 `json:"reservations"`
	Settlements                         uint64 `json:"settlements"`
	ReconciliationCases                 uint64 `json:"reconciliation_cases"`
	BillingProfileBlocks                uint64 `json:"billing_profile_blocks"`
	LedgerAccounts                      uint64 `json:"ledger_accounts"`
	Journals                            uint64 `json:"journals"`
	JournalLines                        uint64 `json:"journal_lines"`
	APIKeys                             uint64 `json:"api_keys"`
	PeriodicBudgetControls              uint64 `json:"periodic_budget_controls"`
	PeriodicBudgetWindows               uint64 `json:"periodic_budget_windows"`
	PeriodicBudgetAllocations           uint64 `json:"periodic_budget_allocations"`
	PeriodicBudgetEvents                uint64 `json:"periodic_budget_events"`
	PeriodicBudgetOperations            uint64 `json:"periodic_budget_operations"`
	PeriodicBudgetConfigurationVersions uint64 `json:"periodic_budget_configuration_versions"`
	PeriodicBudgetObservationOrigins    uint64 `json:"periodic_budget_observation_origins"`
	PeriodicBudgetObservationHeads      uint64 `json:"periodic_budget_observation_heads"`
	PeriodicBudgetObservationJournal    uint64 `json:"periodic_budget_observation_journal"`
}

// ChannelRow is the channels raw physical projection.
type ChannelRow struct {
	ID string `json:"id"`
}

// ChannelCredentialRow is the channel_credentials raw physical projection.
type ChannelCredentialRow struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
}

// ChannelVersionRow is the channel_versions raw physical projection.
type ChannelVersionRow struct {
	ID           string `json:"id"`
	ChannelID    string `json:"channel_id"`
	CredentialID string `json:"credential_id"`
}

// ChannelVersionCredentialRow is the channel_version_credentials raw physical projection.
type ChannelVersionCredentialRow struct {
	ChannelVersionID string `json:"channel_version_id"`
	ChannelID        string `json:"channel_id"`
	CredentialID     string `json:"credential_id"`
	Position         string `json:"position"`
	Weight           string `json:"weight"`
	MaxInflight      string `json:"max_inflight"`
}

// TenantRow is the tenants raw physical projection.
type TenantRow struct {
	ID string `json:"id"`
}

// RequestRecordRow is the request_records raw physical projection.
type RequestRecordRow struct {
	ID                    string  `json:"id"`
	TenantID              string  `json:"tenant_id"`
	KeyID                 string  `json:"key_id"`
	OwnerID               string  `json:"owner_id"`
	ModelID               string  `json:"model_id"`
	Status                string  `json:"status"`
	BillingStatus         string  `json:"billing_status"`
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
	LatestFinalizationID  *string `json:"latest_finalization_id"`
	CreatedAt             Time    `json:"created_at"`
	ClientIP              *string `json:"client_ip"`
	PricingGroupID        *string `json:"pricing_group_id"`
	TenantPricingRevision *string `json:"tenant_pricing_revision"`
	RetailPricedAt        Time    `json:"retail_priced_at"`
	PBCreatedXID          *string `json:"pb_created_xid"`
	PBTenantControl       *string `json:"pb_tenant_control"`
	PBKeyControl          *string `json:"pb_key_control"`
	PBAdmittedAt          Time    `json:"pb_admitted_at"`
	PPOriginRootID        *string `json:"pp_origin_root_id"`
	PPOriginRootStep      *string `json:"pp_origin_root_step"`
	PPOriginXID           *string `json:"pp_origin_xid"`
	PPLastDecisionID      *string `json:"pp_last_decision_id"`
}

// CostVersionRow is the cost_versions raw physical projection.
type CostVersionRow struct {
	ID            string  `json:"id"`
	ChannelID     string  `json:"channel_id"`
	UpstreamModel string  `json:"upstream_model"`
	InputRate     *string `json:"input_rate"`
	OutputRate    *string `json:"output_rate"`
	Basis         string  `json:"basis"`
	EffectiveAt   Time    `json:"effective_at"`
	CreatedAt     Time    `json:"created_at"`
}

// ProcurementAccountRow is the procurement_accounts raw physical projection.
type ProcurementAccountRow struct {
	ID                string  `json:"id"`
	Label             string  `json:"label"`
	Provider          string  `json:"provider"`
	State             string  `json:"state"`
	LimitAmount       string  `json:"limit_amount"`
	HeldAmount        string  `json:"held_amount"`
	SpentAmount       string  `json:"spent_amount"`
	Revision          string  `json:"revision"`
	CreatedAt         Time    `json:"created_at"`
	PPVersion         *string `json:"pp_version"`
	PPCreatedXID      *string `json:"pp_created_xid"`
	PPOriginJournalID *string `json:"pp_origin_journal_id"`
}

// CredentialProcurementBindingRow is the credential_procurement_bindings raw physical projection.
type CredentialProcurementBindingRow struct {
	ID           string `json:"id"`
	CredentialID string `json:"credential_id"`
	ChannelID    string `json:"channel_id"`
	AccountID    string `json:"account_id"`
	OperationID  string `json:"operation_id"`
	BoundAt      Time   `json:"bound_at"`
}

// UpstreamAttemptRow is the upstream_attempts raw physical projection.
type UpstreamAttemptRow struct {
	ID                     string  `json:"id"`
	TenantID               string  `json:"tenant_id"`
	RequestID              string  `json:"request_id"`
	Sequence               string  `json:"sequence"`
	ChannelID              string  `json:"channel_id"`
	ChannelVersionID       string  `json:"channel_version_id"`
	CredentialID           string  `json:"credential_id"`
	UpstreamModel          string  `json:"upstream_model"`
	DeliveryState          string  `json:"delivery_state"`
	CostVersionID          *string `json:"cost_version_id"`
	CostInputRate          *string `json:"cost_input_rate"`
	CostOutputRate         *string `json:"cost_output_rate"`
	CostBasis              string  `json:"cost_basis"`
	CostStatus             string  `json:"cost_status"`
	CostAmount             *string `json:"cost_amount"`
	CreatedAt              Time    `json:"created_at"`
	RouteDecisions         string  `json:"route_decisions"`
	PBCreatedXID           *string `json:"pb_created_xid"`
	PBDispatchCheckedAt    Time    `json:"pb_dispatch_checked_at"`
	PPOriginRootID         *string `json:"pp_origin_root_id"`
	PPOriginRootStep       *string `json:"pp_origin_root_step"`
	PPOriginXID            *string `json:"pp_origin_xid"`
	PPQuoteSelectedAt      Time    `json:"pp_quote_selected_at"`
	PPLastCostMutationID   *string `json:"pp_last_cost_mutation_id"`
	PPLastDeliverySourceID *string `json:"pp_last_delivery_source_id"`
	PPDispatchSourceID     *string `json:"pp_dispatch_source_id"`
	PPDispatchRootID       *string `json:"pp_dispatch_root_id"`
	PPDispatchRootStep     *string `json:"pp_dispatch_root_step"`
	PPDispatchXID          *string `json:"pp_dispatch_xid"`
	PPDispatchAccountID    *string `json:"pp_dispatch_account_id"`
	PPDispatchJournalID    *string `json:"pp_dispatch_journal_id"`
}

// ProcurementReservationRow is the procurement_reservations raw physical projection.
type ProcurementReservationRow struct {
	AttemptID         string  `json:"attempt_id"`
	AccountID         string  `json:"account_id"`
	BindingID         string  `json:"binding_id"`
	InputBound        string  `json:"input_bound"`
	OutputBound       string  `json:"output_bound"`
	UpperAmount       string  `json:"upper_amount"`
	HeldAmount        string  `json:"held_amount"`
	SpentAmount       string  `json:"spent_amount"`
	State             string  `json:"state"`
	Anomaly           bool    `json:"anomaly"`
	Revision          string  `json:"revision"`
	CreatedAt         Time    `json:"created_at"`
	UpdatedAt         Time    `json:"updated_at"`
	PBCreatedXID      *string `json:"pb_created_xid"`
	PBControl         *string `json:"pb_control"`
	PBAdmittedAt      Time    `json:"pb_admitted_at"`
	PPCreatedXID      *string `json:"pp_created_xid"`
	PPOriginJournalID *string `json:"pp_origin_journal_id"`
	PPLastJournalID   *string `json:"pp_last_journal_id"`
}

// ProcurementBudgetEventRow is the procurement_budget_events raw physical projection.
type ProcurementBudgetEventRow struct {
	ID             string  `json:"id"`
	EventKey       string  `json:"event_key"`
	AccountID      string  `json:"account_id"`
	AttemptID      string  `json:"attempt_id"`
	HeldDelta      string  `json:"held_delta"`
	SpentDelta     string  `json:"spent_delta"`
	Type           string  `json:"type"`
	EvidenceRef    string  `json:"evidence_ref"`
	CreatedAt      Time    `json:"created_at"`
	PBCreatedXID   *string `json:"pb_created_xid"`
	PPRootID       *string `json:"pp_root_id"`
	PPRootStep     *string `json:"pp_root_step"`
	PPTransitionID *string `json:"pp_transition_id"`
	PPJournalID    *string `json:"pp_journal_id"`
	PPCreatedXID   *string `json:"pp_created_xid"`
}

// UserRow is the users raw physical projection.
type UserRow struct {
	ID string `json:"id"`
}

// ProviderCostCorrectionRow is the provider_cost_corrections raw physical projection.
type ProviderCostCorrectionRow struct {
	ID           string  `json:"id"`
	TenantID     string  `json:"tenant_id"`
	AttemptID    string  `json:"attempt_id"`
	OperationID  string  `json:"operation_id"`
	Amount       string  `json:"amount"`
	Basis        string  `json:"basis"`
	EvidenceRef  string  `json:"evidence_ref"`
	CreatedAt    Time    `json:"created_at"`
	PPRootID     *string `json:"pp_root_id"`
	PPRootStep   *string `json:"pp_root_step"`
	PPSourceKind *string `json:"pp_source_kind"`
	PPSourceID   *string `json:"pp_source_id"`
	PPSourceRole *string `json:"pp_source_role"`
	PPCreatedXID *string `json:"pp_created_xid"`
}

// ProcurementCostProposalRow is the procurement_cost_proposals raw physical projection.
type ProcurementCostProposalRow struct {
	ID                          string  `json:"id"`
	TenantID                    string  `json:"tenant_id"`
	AttemptID                   string  `json:"attempt_id"`
	Amount                      string  `json:"amount"`
	Basis                       string  `json:"basis"`
	EvidenceRef                 string  `json:"evidence_ref"`
	Reason                      string  `json:"reason"`
	Requester                   string  `json:"requester"`
	ExpectedReservationRevision string  `json:"expected_reservation_revision"`
	ExpectedEvidenceCount       string  `json:"expected_evidence_count"`
	State                       string  `json:"state"`
	Approver                    *string `json:"approver"`
	ApprovalOperationID         *string `json:"approval_operation_id"`
	CreatedAt                   Time    `json:"created_at"`
	ApprovedAt                  Time    `json:"approved_at"`
	PPRootID                    *string `json:"pp_root_id"`
	PPRootStep                  *string `json:"pp_root_step"`
	PPSourceKind                *string `json:"pp_source_kind"`
	PPSourceID                  *string `json:"pp_source_id"`
	PPSourceRole                *string `json:"pp_source_role"`
	PPCreatedXID                *string `json:"pp_created_xid"`
	PPApprovalRootID            *string `json:"pp_approval_root_id"`
	PPApprovalRootStep          *string `json:"pp_approval_root_step"`
	PPApprovalSourceKind        *string `json:"pp_approval_source_kind"`
	PPApprovalSourceID          *string `json:"pp_approval_source_id"`
	PPApprovalSourceRole        *string `json:"pp_approval_source_role"`
	PPApprovalCreatedXID        *string `json:"pp_approval_created_xid"`
}

// ProcurementCommandRow is the procurement_commands raw physical projection.
type ProcurementCommandRow struct {
	OperationID        string  `json:"operation_id"`
	Actor              string  `json:"actor"`
	Kind               string  `json:"kind"`
	TargetID           string  `json:"target_id"`
	InputDigest        string  `json:"input_digest"`
	Status             string  `json:"status"`
	Reason             string  `json:"reason"`
	BeforeState        string  `json:"before_state"`
	AfterState         string  `json:"after_state"`
	CreatedAt          Time    `json:"created_at"`
	PPSourceID         *string `json:"pp_source_id"`
	PPRootID           *string `json:"pp_root_id"`
	PPRootStep         *string `json:"pp_root_step"`
	PPCreatedXID       *string `json:"pp_created_xid"`
	PPAccountJournalID *string `json:"pp_account_journal_id"`
}

// ProcurementBalanceObservationRow is the procurement_balance_observations raw physical projection.
type ProcurementBalanceObservationRow struct {
	ID           string  `json:"id"`
	AccountID    string  `json:"account_id"`
	OperationID  string  `json:"operation_id"`
	Actor        string  `json:"actor"`
	Status       string  `json:"status"`
	Amount       *string `json:"amount"`
	Currency     string  `json:"currency"`
	Source       string  `json:"source"`
	Semantic     string  `json:"semantic"`
	ObservedAt   Time    `json:"observed_at"`
	ProviderAsOf Time    `json:"provider_as_of"`
	EvidenceRef  string  `json:"evidence_ref"`
	CreatedAt    Time    `json:"created_at"`
}

// AuditEventRow is the audit_events raw physical projection.
type AuditEventRow struct {
	ID            string  `json:"id"`
	Actor         string  `json:"actor"`
	TenantScope   *string `json:"tenant_scope"`
	Action        string  `json:"action"`
	Target        string  `json:"target"`
	SanitizedDiff string  `json:"sanitized_diff"`
	RequestID     string  `json:"request_id"`
	CreatedAt     Time    `json:"created_at"`
	PPRootID      *string `json:"pp_root_id"`
	PPRootStep    *string `json:"pp_root_step"`
	PPSourceKind  *string `json:"pp_source_kind"`
	PPSourceID    *string `json:"pp_source_id"`
	PPSourceRole  *string `json:"pp_source_role"`
	PPCreatedXID  *string `json:"pp_created_xid"`
}

// ProjectionOutboxRow is the projection_outbox raw physical projection.
type ProjectionOutboxRow struct {
	EventID          string  `json:"event_id"`
	TenantID         string  `json:"tenant_id"`
	OperationID      string  `json:"operation_id"`
	Kind             string  `json:"kind"`
	SchemaVersion    string  `json:"schema_version"`
	SanitizedPayload string  `json:"sanitized_payload"`
	CreatedAt        Time    `json:"created_at"`
	PPRootID         *string `json:"pp_root_id"`
	PPRootStep       *string `json:"pp_root_step"`
	PPSourceKind     *string `json:"pp_source_kind"`
	PPSourceID       *string `json:"pp_source_id"`
	PPSourceRole     *string `json:"pp_source_role"`
	PPCreatedXID     *string `json:"pp_created_xid"`
}

// ProcurementEvidenceIdentityRow is the procurement_evidence_identities raw physical projection.
type ProcurementEvidenceIdentityRow struct {
	AttemptID      string  `json:"attempt_id"`
	EvidenceDigest string  `json:"evidence_digest"`
	EventID        string  `json:"event_id"`
	CreatedAt      Time    `json:"created_at"`
	PPRootID       *string `json:"pp_root_id"`
	PPRootStep     *string `json:"pp_root_step"`
	PPSourceKind   *string `json:"pp_source_kind"`
	PPSourceID     *string `json:"pp_source_id"`
	PPSourceRole   *string `json:"pp_source_role"`
	PPCreatedXID   *string `json:"pp_created_xid"`
}

// MeteringEventRow is the metering_events raw physical projection.
type MeteringEventRow struct {
	ID              string  `json:"id"`
	TenantID        string  `json:"tenant_id"`
	RequestID       string  `json:"request_id"`
	AttemptID       *string `json:"attempt_id"`
	Source          string  `json:"source"`
	SourceEventID   string  `json:"source_event_id"`
	SchemaVersion   string  `json:"schema_version"`
	NormalizedUsage string  `json:"normalized_usage"`
	HappenedAt      Time    `json:"happened_at"`
	ReceivedAt      Time    `json:"received_at"`
	PPRootID        *string `json:"pp_root_id"`
	PPRootStep      *string `json:"pp_root_step"`
	PPSourceKind    *string `json:"pp_source_kind"`
	PPSourceID      *string `json:"pp_source_id"`
	PPSourceRole    *string `json:"pp_source_role"`
	PPCreatedXID    *string `json:"pp_created_xid"`
}

// MeteringEventConflictRow is the metering_event_conflicts raw physical projection.
type MeteringEventConflictRow struct {
	ID              string  `json:"id"`
	EventID         string  `json:"event_id"`
	NormalizedUsage string  `json:"normalized_usage"`
	CreatedAt       Time    `json:"created_at"`
	PPRootID        *string `json:"pp_root_id"`
	PPRootStep      *string `json:"pp_root_step"`
	PPSourceKind    *string `json:"pp_source_kind"`
	PPSourceID      *string `json:"pp_source_id"`
	PPSourceRole    *string `json:"pp_source_role"`
	PPCreatedXID    *string `json:"pp_created_xid"`
}

// FinalizationRecordRow is the finalization_records raw physical projection.
type FinalizationRecordRow struct {
	ID                 string  `json:"id"`
	TenantID           string  `json:"tenant_id"`
	RequestID          string  `json:"request_id"`
	EventID            string  `json:"event_id"`
	InputDigest        string  `json:"input_digest"`
	ProtocolOutcome    string  `json:"protocol_outcome"`
	TransportEndReason string  `json:"transport_end_reason"`
	DeliveryStage      string  `json:"delivery_stage"`
	ContentWritten     bool    `json:"content_written"`
	FinishReason       string  `json:"finish_reason"`
	PolicyVersion      string  `json:"policy_version"`
	CreatedAt          Time    `json:"created_at"`
	PPRootID           *string `json:"pp_root_id"`
	PPRootStep         *string `json:"pp_root_step"`
	PPSourceKind       *string `json:"pp_source_kind"`
	PPSourceID         *string `json:"pp_source_id"`
	PPSourceRole       *string `json:"pp_source_role"`
	PPCreatedXID       *string `json:"pp_created_xid"`
}

// ReservationRow is the reservations raw physical projection.
type ReservationRow struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	RequestID string `json:"request_id"`
	Amount    string `json:"amount"`
	Status    string `json:"status"`
	Deadline  Time   `json:"deadline"`
	CreatedAt Time   `json:"created_at"`
}

// SettlementRow is the settlements raw physical projection.
type SettlementRow struct {
	ID                 string  `json:"id"`
	TenantID           string  `json:"tenant_id"`
	RequestID          string  `json:"request_id"`
	FinalizationID     *string `json:"finalization_id"`
	ChargedAmount      string  `json:"charged_amount"`
	RawRetailAmount    string  `json:"raw_retail_amount"`
	WaivedRetailAmount string  `json:"waived_retail_amount"`
	RefundedAmount     string  `json:"refunded_amount"`
	Status             string  `json:"status"`
	Reason             string  `json:"reason"`
	JournalID          string  `json:"journal_id"`
	CreatedAt          Time    `json:"created_at"`
	PPRootID           *string `json:"pp_root_id"`
	PPRootStep         *string `json:"pp_root_step"`
	PPSourceKind       *string `json:"pp_source_kind"`
	PPSourceID         *string `json:"pp_source_id"`
	PPSourceRole       *string `json:"pp_source_role"`
	PPCreatedXID       *string `json:"pp_created_xid"`
}

// ReconciliationCaseRow is the reconciliation_cases raw physical projection.
type ReconciliationCaseRow struct {
	ID               string  `json:"id"`
	TenantID         string  `json:"tenant_id"`
	RequestID        string  `json:"request_id"`
	Reason           string  `json:"reason"`
	State            string  `json:"state"`
	ProposedUsage    *string `json:"proposed_usage"`
	EvidenceRef      *string `json:"evidence_ref"`
	ProposalReason   *string `json:"proposal_reason"`
	Requester        *string `json:"requester"`
	Approver         *string `json:"approver"`
	CreatedAt        Time    `json:"created_at"`
	PPLastRootID     *string `json:"pp_last_root_id"`
	PPLastRootStep   *string `json:"pp_last_root_step"`
	PPLastSourceKind *string `json:"pp_last_source_kind"`
	PPLastSourceID   *string `json:"pp_last_source_id"`
	PPLastSourceRole *string `json:"pp_last_source_role"`
	PPLastCreatedXID *string `json:"pp_last_created_xid"`
}

// BillingProfileBlockRow is the billing_profile_blocks raw physical projection.
type BillingProfileBlockRow struct {
	ModelID        string `json:"model_id"`
	ProfileVersion string `json:"profile_version"`
	Reason         string `json:"reason"`
	RequestID      string `json:"request_id"`
	CreatedAt      Time   `json:"created_at"`
}

// LedgerAccountRow is the ledger_accounts raw physical projection.
type LedgerAccountRow struct {
	ID       string  `json:"id"`
	TenantID *string `json:"tenant_id"`
	Kind     string  `json:"kind"`
	Balance  string  `json:"balance"`
}

// JournalRow is the journals raw physical projection.
type JournalRow struct {
	ID           string  `json:"id"`
	TenantID     string  `json:"tenant_id"`
	OperationID  string  `json:"operation_id"`
	Kind         string  `json:"kind"`
	Reference    string  `json:"reference"`
	Actor        string  `json:"actor"`
	Currency     string  `json:"currency"`
	CreatedTxid  string  `json:"created_txid"`
	CreatedAt    Time    `json:"created_at"`
	PPRootID     *string `json:"pp_root_id"`
	PPRootStep   *string `json:"pp_root_step"`
	PPSourceKind *string `json:"pp_source_kind"`
	PPSourceID   *string `json:"pp_source_id"`
	PPSourceRole *string `json:"pp_source_role"`
	PPCreatedXID *string `json:"pp_created_xid"`
}

// JournalLineRow is the journal_lines raw physical projection.
type JournalLineRow struct {
	ID           string  `json:"id"`
	JournalID    string  `json:"journal_id"`
	AccountID    string  `json:"account_id"`
	SignedDelta  string  `json:"signed_delta"`
	PPRootID     *string `json:"pp_root_id"`
	PPRootStep   *string `json:"pp_root_step"`
	PPSourceKind *string `json:"pp_source_kind"`
	PPSourceID   *string `json:"pp_source_id"`
	PPSourceRole *string `json:"pp_source_role"`
	PPCreatedXID *string `json:"pp_created_xid"`
}

// APIKeyRow is the api_keys raw physical projection.
type APIKeyRow struct {
	ID            string  `json:"id"`
	TenantID      string  `json:"tenant_id"`
	OwnerID       string  `json:"owner_id"`
	LifetimeLimit *string `json:"lifetime_limit"`
	Spent         string  `json:"spent"`
	Held          string  `json:"held"`
}

// PeriodicBudgetControlRow is the periodic_budget_controls raw physical projection.
type PeriodicBudgetControlRow struct {
	ID                       string  `json:"id"`
	Scope                    string  `json:"scope"`
	TenantID                 *string `json:"tenant_id"`
	KeyID                    *string `json:"key_id"`
	AccountID                *string `json:"account_id"`
	PeriodKind               string  `json:"period_kind"`
	Enabled                  bool    `json:"enabled"`
	LimitAmount              string  `json:"limit_amount"`
	Revision                 string  `json:"revision"`
	TrackingStartedAt        Time    `json:"tracking_started_at"`
	EffectiveFrom            Time    `json:"effective_from"`
	MaxObservedWindowStart   Time    `json:"max_observed_window_start"`
	HeldAmount               string  `json:"held_amount"`
	SpentAmount              string  `json:"spent_amount"`
	EventSeq                 string  `json:"event_seq"`
	LastEventID              *string `json:"last_event_id"`
	CreatedXID               string  `json:"created_xid"`
	ConfigurationOperationID string  `json:"configuration_operation_id"`
}

// PeriodicBudgetWindowRow is the periodic_budget_windows raw physical projection.
type PeriodicBudgetWindowRow struct {
	ID                    string  `json:"id"`
	ControlID             string  `json:"control_id"`
	StartAt               Time    `json:"start_at"`
	EndAt                 Time    `json:"end_at"`
	HeldAmount            string  `json:"held_amount"`
	SpentAmount           string  `json:"spent_amount"`
	Revision              string  `json:"revision"`
	LastEventID           *string `json:"last_event_id"`
	ObservationID         *string `json:"observation_id"`
	ObservationCreatedXID *string `json:"observation_created_xid"`
}

// PeriodicBudgetAllocationRow is the periodic_budget_allocations raw physical projection.
type PeriodicBudgetAllocationRow struct {
	ID             string  `json:"id"`
	ControlID      string  `json:"control_id"`
	SubjectType    string  `json:"subject_type"`
	SubjectID      string  `json:"subject_id"`
	Origin         string  `json:"origin"`
	OriginSourceID string  `json:"origin_source_id"`
	WindowID       *string `json:"window_id"`
	AdmittedAt     Time    `json:"admitted_at"`
	UpperAmount    string  `json:"upper_amount"`
	Baseline       string  `json:"baseline"`
	SourceHeld     string  `json:"source_held"`
	SourceSpent    string  `json:"source_spent"`
	HeldAmount     string  `json:"held_amount"`
	SpentAmount    string  `json:"spent_amount"`
	State          string  `json:"state"`
	Revision       string  `json:"revision"`
	LastEventID    *string `json:"last_event_id"`
	CreatedXID     string  `json:"created_xid"`
	CreatedAt      Time    `json:"created_at"`
}

// PeriodicBudgetEventRow is the periodic_budget_events raw physical projection.
type PeriodicBudgetEventRow struct {
	ID                       string  `json:"id"`
	ControlID                string  `json:"control_id"`
	AllocationID             string  `json:"allocation_id"`
	SourceKind               string  `json:"source_kind"`
	SourceID                 string  `json:"source_id"`
	Seq                      string  `json:"seq"`
	AllocationBeforeRevision string  `json:"allocation_before_revision"`
	WindowBeforeRevision     *string `json:"window_before_revision"`
	BeforeHeld               string  `json:"before_held"`
	BeforeSpent              string  `json:"before_spent"`
	AfterHeld                string  `json:"after_held"`
	AfterSpent               string  `json:"after_spent"`
	AfterSourceSpent         string  `json:"after_source_spent"`
	AfterState               string  `json:"after_state"`
	HeldDelta                string  `json:"held_delta"`
	SpentDelta               string  `json:"spent_delta"`
	CreatedXID               string  `json:"created_xid"`
	CreatedAt                Time    `json:"created_at"`
}

// PeriodicBudgetOperationRow is the periodic_budget_operations raw physical projection.
type PeriodicBudgetOperationRow struct {
	OperationID      string  `json:"operation_id"`
	Kind             string  `json:"kind"`
	Actor            string  `json:"actor"`
	Scope            string  `json:"scope"`
	Target           string  `json:"target"`
	ResourceID       string  `json:"resource_id"`
	Revision         string  `json:"revision"`
	SafeInput        string  `json:"safe_input"`
	InputFingerprint string  `json:"input_fingerprint"`
	ResultSnapshot   string  `json:"result_snapshot"`
	CreatedXID       string  `json:"created_xid"`
	CreatedAt        Time    `json:"created_at"`
	ObservationID    *string `json:"observation_id"`
}

// PeriodicBudgetConfigurationVersionRow is the periodic_budget_configuration_versions raw physical projection.
type PeriodicBudgetConfigurationVersionRow struct {
	ControlID   string `json:"control_id"`
	Revision    string `json:"revision"`
	OperationID string `json:"operation_id"`
	PeriodKind  string `json:"period_kind"`
	Enabled     bool   `json:"enabled"`
	LimitAmount string `json:"limit_amount"`
	Actor       string `json:"actor"`
	CreatedXID  string `json:"created_xid"`
	CreatedAt   Time   `json:"created_at"`
}

// PeriodicBudgetObservationOriginRow is the periodic_budget_observation_origins raw physical projection.
type PeriodicBudgetObservationOriginRow struct {
	ControlID         string `json:"control_id"`
	Version           string `json:"version"`
	CreatedXID        string `json:"created_xid"`
	TrackingStartedAt Time   `json:"tracking_started_at"`
}

// PeriodicBudgetObservationHeadRow is the periodic_budget_observation_heads raw physical projection.
type PeriodicBudgetObservationHeadRow struct {
	ControlID         string  `json:"control_id"`
	Ordinal           string  `json:"ordinal"`
	LastObservationID *string `json:"last_observation_id"`
	UpdatedXID        string  `json:"updated_xid"`
}

// PeriodicBudgetObservationJournalRow is the periodic_budget_observation_journal raw physical projection.
type PeriodicBudgetObservationJournalRow struct {
	ID                       string  `json:"id"`
	ControlID                string  `json:"control_id"`
	Ordinal                  string  `json:"ordinal"`
	PreviousID               *string `json:"previous_id"`
	Kind                     string  `json:"kind"`
	SourceID                 string  `json:"source_id"`
	EventSeq                 string  `json:"event_seq"`
	LastEventID              *string `json:"last_event_id"`
	ConfigurationRevision    string  `json:"configuration_revision"`
	ConfigurationOperationID string  `json:"configuration_operation_id"`
	WindowID                 *string `json:"window_id"`
	WindowRevision           *string `json:"window_revision"`
	WindowLastEventID        *string `json:"window_last_event_id"`
	CreatedXID               string  `json:"created_xid"`
	ObservedAt               Time    `json:"observed_at"`
	CommandSourceID          *string `json:"command_source_id"`
	WindowSourceID           *string `json:"window_source_id"`
}
