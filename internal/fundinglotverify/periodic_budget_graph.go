package fundinglotverify

import (
	"context"
	"errors"
	"reflect"
	"time"
)

var (
	ErrPeriodicBudgetGraphRowsInput    = errors.New("funding_lot_periodic_budget_graph_rows_input")
	ErrPeriodicBudgetGraphRowsResource = errors.New("funding_lot_periodic_budget_graph_rows_resource")
	ErrPeriodicBudgetGraphRowsCanceled = errors.New("funding_lot_periodic_budget_graph_rows_canceled")
	ErrPeriodicBudgetGraphRowsTimeout  = errors.New("funding_lot_periodic_budget_graph_rows_timeout")
)

// PeriodicBudgetGraphTime preserves native SQL NULL and infinities without pgx.
type PeriodicBudgetGraphTime struct {
	Value    time.Time
	Valid    bool
	Infinity int8
}
type PeriodicBudgetControlRow struct {
	ID, Scope                                                string
	TenantID, KeyID, AccountID                               *string
	PeriodKind                                               string
	Enabled                                                  bool
	LimitAmount, Revision                                    string
	TrackingStartedAt, EffectiveFrom, MaxObservedWindowStart PeriodicBudgetGraphTime
	HeldAmount, SpentAmount, EventSeq                        string
	LastEventID                                              *string
	CreatedXID, ConfigurationOperationID                     string
}
type PeriodicBudgetWindowRow struct {
	ID, ControlID                     string
	StartAt, EndAt                    PeriodicBudgetGraphTime
	HeldAmount, SpentAmount, Revision string
	LastEventID                       *string
}
type PeriodicBudgetAllocationRow struct {
	ID, ControlID, SubjectType, SubjectID, Origin, OriginSourceID                            string
	WindowID                                                                                 *string
	AdmittedAt                                                                               PeriodicBudgetGraphTime
	UpperAmount, Baseline, SourceHeld, SourceSpent, HeldAmount, SpentAmount, State, Revision string
	LastEventID                                                                              *string
	CreatedXID                                                                               string
	CreatedAt                                                                                PeriodicBudgetGraphTime
}
type PeriodicBudgetEventRow struct {
	ID, ControlID, AllocationID, SourceKind, SourceID, Seq, AllocationBeforeRevision                                string
	WindowBeforeRevision                                                                                            *string
	BeforeHeld, BeforeSpent, AfterHeld, AfterSpent, AfterSourceSpent, AfterState, HeldDelta, SpentDelta, CreatedXID string
	CreatedAt                                                                                                       PeriodicBudgetGraphTime
}
type PeriodicBudgetOperationRow struct {
	OperationID, Kind, Actor, Scope, Target, ResourceID, Revision string
	SafeInput, ResultSnapshot                                     []byte
	InputFingerprint, CreatedXID                                  string
	CreatedAt                                                     PeriodicBudgetGraphTime
}
type PeriodicBudgetVersionRow struct {
	ControlID, Revision, OperationID, PeriodKind string
	Enabled                                      bool
	LimitAmount, Actor, CreatedXID               string
	CreatedAt                                    PeriodicBudgetGraphTime
}
type PeriodicBudgetKeyRow struct{ ID, TenantID string }
type PeriodicBudgetRequestRow struct {
	ID, TenantID, KeyID                   string
	CreatedXID, TenantControl, KeyControl *string
	AdmittedAt                            PeriodicBudgetGraphTime
}
type PeriodicBudgetSettlementRow struct{ ID, TenantID, RequestID, ChargedAmount, Status string }
type PeriodicBudgetAttemptRow struct {
	ID, TenantID, RequestID, CredentialID, ChannelID, DeliveryState string
	CreatedXID                                                      *string
	DispatchCheckedAt                                               PeriodicBudgetGraphTime
}
type PeriodicBudgetBindingRow struct{ ID, AccountID, CredentialID, ChannelID string }
type PeriodicBudgetSupplierRow struct {
	AttemptID, AccountID, BindingID, UpperAmount, HeldAmount, SpentAmount, State string
	Anomaly                                                                      bool
	CreatedXID, ControlID                                                        *string
	AdmittedAt                                                                   PeriodicBudgetGraphTime
}
type PeriodicBudgetProcurementEventRow struct {
	ID, EventKey, AccountID, AttemptID, HeldDelta, SpentDelta, Type string
	CreatedXID                                                      *string
}
type PeriodicBudgetAuditRow struct {
	ID, Actor      string
	TenantScope    *string
	Action, Target string
	SanitizedDiff  []byte
	RequestID      string
	CreatedAt      PeriodicBudgetGraphTime
}
type PeriodicBudgetGraphRows struct {
	Controls          []PeriodicBudgetControlRow
	Windows           []PeriodicBudgetWindowRow
	Allocations       []PeriodicBudgetAllocationRow
	Events            []PeriodicBudgetEventRow
	Operations        []PeriodicBudgetOperationRow
	Versions          []PeriodicBudgetVersionRow
	Tenants           []ProcurementAttemptTenantRow
	Keys              []PeriodicBudgetKeyRow
	Users             []ProcurementApprovalUserRow
	Requests          []PeriodicBudgetRequestRow
	Reservations      []CustomerKeyReservationRow
	Settlements       []PeriodicBudgetSettlementRow
	Attempts          []PeriodicBudgetAttemptRow
	Accounts          []ProcurementBudgetAccountRow
	Bindings          []PeriodicBudgetBindingRow
	Procurement       []PeriodicBudgetSupplierRow
	ProcurementEvents []PeriodicBudgetProcurementEventRow
	Audits            []PeriodicBudgetAuditRow
}
type PeriodicBudgetGraphCounts struct {
	Controls          uint64 `json:"controls"`
	Windows           uint64 `json:"windows"`
	Allocations       uint64 `json:"allocations"`
	Events            uint64 `json:"events"`
	Operations        uint64 `json:"operations"`
	Versions          uint64 `json:"versions"`
	Tenants           uint64 `json:"tenants"`
	Keys              uint64 `json:"keys"`
	Users             uint64 `json:"users"`
	Requests          uint64 `json:"requests"`
	Reservations      uint64 `json:"reservations"`
	Settlements       uint64 `json:"settlements"`
	Attempts          uint64 `json:"attempts"`
	Accounts          uint64 `json:"accounts"`
	Bindings          uint64 `json:"bindings"`
	Procurement       uint64 `json:"procurement"`
	ProcurementEvents uint64 `json:"procurement_events"`
	Audits            uint64 `json:"audits"`
}
type PeriodicBudgetGraphMismatches struct {
	DuplicatePrimaryRows             uint64 `json:"duplicate_primary_rows"`
	DuplicateControlScopeRows        uint64 `json:"duplicate_control_scope_rows"`
	DuplicateWindowStartRows         uint64 `json:"duplicate_window_start_rows"`
	DuplicateAllocationSubjectRows   uint64 `json:"duplicate_allocation_subject_rows"`
	DuplicateEventSequenceRows       uint64 `json:"duplicate_event_sequence_rows"`
	DuplicateEffectRows              uint64 `json:"duplicate_effect_rows"`
	DuplicateVersionOperationRows    uint64 `json:"duplicate_version_operation_rows"`
	DuplicateReservationRequestRows  uint64 `json:"duplicate_reservation_request_rows"`
	DuplicateSettlementRequestRows   uint64 `json:"duplicate_settlement_request_rows"`
	DuplicateBindingCredentialRows   uint64 `json:"duplicate_binding_credential_rows"`
	DuplicateProcurementEventKeyRows uint64 `json:"duplicate_procurement_event_key_rows"`
	DuplicateAuditTargetRows         uint64 `json:"duplicate_audit_target_rows"`
	InvalidTextRows                  uint64 `json:"invalid_text_rows"`
	InvalidUUIDRows                  uint64 `json:"invalid_uuid_rows"`
	InvalidAmountRows                uint64 `json:"invalid_amount_rows"`
	InvalidRevisionRows              uint64 `json:"invalid_revision_rows"`
	InvalidXIDRows                   uint64 `json:"invalid_xid_rows"`
	InvalidTimeRows                  uint64 `json:"invalid_time_rows"`
	InvalidEnumRows                  uint64 `json:"invalid_enum_rows"`
	SourceIdentityMismatches         uint64 `json:"source_identity_mismatches"`
	MissingAllocations               uint64 `json:"missing_allocations"`
	OrphanAllocations                uint64 `json:"orphan_allocations"`
	AllocationOriginMismatches       uint64 `json:"allocation_origin_mismatches"`
	AllocationSourceMismatches       uint64 `json:"allocation_source_mismatches"`
	AllocationProjectionMismatches   uint64 `json:"allocation_projection_mismatches"`
	MissingEffects                   uint64 `json:"missing_effects"`
	OrphanEffects                    uint64 `json:"orphan_effects"`
	EffectBindingMismatches          uint64 `json:"effect_binding_mismatches"`
	EventChainMismatches             uint64 `json:"event_chain_mismatches"`
	WindowMismatches                 uint64 `json:"window_mismatches"`
	ControlMismatches                uint64 `json:"control_mismatches"`
	ProcurementSourceMismatches      uint64 `json:"procurement_source_mismatches"`
	LegacyBaselineMismatches         uint64 `json:"legacy_baseline_mismatches"`
	ConfigurationMismatches          uint64 `json:"configuration_mismatches"`
	VersionMismatches                uint64 `json:"version_mismatches"`
	OperationMismatches              uint64 `json:"operation_mismatches"`
	AuditMismatches                  uint64 `json:"audit_mismatches"`
	SnapshotMismatches               uint64 `json:"snapshot_mismatches"`
	DispatchMetadataMismatches       uint64 `json:"dispatch_metadata_mismatches"`
}
type PeriodicBudgetGraphGaps struct {
	LegacyMembershipUnproven        uint64 `json:"legacy_membership_unproven"`
	LegacyEffectBoundaryUnproven    uint64 `json:"legacy_effect_boundary_unproven"`
	OperationSnapshotPrefixUnproven uint64 `json:"operation_snapshot_prefix_unproven"`
}
type PeriodicBudgetGraphCheck struct {
	Scope            string                        `json:"scope"`
	Scanned          PeriodicBudgetGraphCounts     `json:"scanned"`
	Status           string                        `json:"status"`
	Balanced         bool                          `json:"balanced"`
	CoverageComplete bool                          `json:"coverage_complete"`
	Mismatches       PeriodicBudgetGraphMismatches `json:"mismatches"`
	Unproven         PeriodicBudgetGraphGaps       `json:"unproven"`
}

// CheckPeriodicBudgetGraph checks untrusted local rows, conferring no authority.
func CheckPeriodicBudgetGraph(ctx context.Context, rows PeriodicBudgetGraphRows) (PeriodicBudgetGraphCheck, error) {
	if ctx == nil {
		return PeriodicBudgetGraphCheck{}, ErrPeriodicBudgetGraphRowsInput
	}
	w := newPBGraph(ctx)
	if err := w.budget(reflect.ValueOf(rows)); err != nil {
		return PeriodicBudgetGraphCheck{}, err
	}
	w.index(rows)
	if w.err == nil {
		w.sources()
	}
	if w.err == nil {
		w.allocations()
	}
	if w.err == nil {
		w.replay()
	}
	if w.err == nil {
		w.projections()
	}
	if w.err == nil {
		w.commands()
	}
	if err := w.check(); err != nil {
		return PeriodicBudgetGraphCheck{}, err
	}
	w.out.Scope = "periodic_budget_graph_v1"
	w.out.CoverageComplete = w.out.Unproven == (PeriodicBudgetGraphGaps{})
	w.out.Status = "balanced"
	if w.out.Mismatches != (PeriodicBudgetGraphMismatches{}) {
		w.out.Status = "mismatch"
	} else if !w.out.CoverageComplete {
		w.out.Status = "indeterminate"
	}
	w.out.Balanced = w.out.Status == "balanced"
	if err := w.check(); err != nil {
		return PeriodicBudgetGraphCheck{}, err
	}
	return w.out, nil
}
