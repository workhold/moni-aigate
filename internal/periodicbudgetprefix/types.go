// Package periodicbudgetprefix checks untrusted periodic-budget observations.
// It issues no database, transaction, or financial authority.
package periodicbudgetprefix

import (
	"errors"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

var (
	ErrInput    = errors.New("periodic_budget_prefix_input")
	ErrResource = errors.New("periodic_budget_prefix_resource")
	ErrCanceled = errors.New("periodic_budget_prefix_canceled")
	ErrTimeout  = errors.New("periodic_budget_prefix_timeout")
)

type Time = fundinglotverify.PeriodicBudgetGraphTime

type Origin struct {
	ControlID, Version, CreatedXID string
	TrackingStartedAt              Time
}

type Head struct {
	ControlID, Ordinal string
	LastObservationID  *string
	UpdatedXID         string
}

type Observation struct {
	ID, ControlID, Ordinal                          string
	PreviousID                                      *string
	Kind, SourceID, EventSeq                        string
	LastEventID                                     *string
	ConfigurationRevision, ConfigurationOperationID string
	WindowID, WindowRevision, WindowLastEventID     *string
	CreatedXID                                      string
	ObservedAt                                      Time
}

type OperationBinding struct {
	OperationID   string
	ObservationID *string
}

type WindowBinding struct {
	WindowID                             string
	ObservationID, ObservationCreatedXID *string
}

type Rows struct {
	Base              fundinglotverify.PeriodicBudgetGraphRows
	Origins           []Origin
	Heads             []Head
	Observations      []Observation
	OperationBindings []OperationBinding
	WindowBindings    []WindowBinding
}

type Scan struct {
	Origins           uint64 `json:"origins"`
	Heads             uint64 `json:"heads"`
	Observations      uint64 `json:"observations"`
	OperationBindings uint64 `json:"operation_bindings"`
	WindowBindings    uint64 `json:"window_bindings"`
	TotalRows         uint64 `json:"total_rows"`
	LogicalBytes      uint64 `json:"logical_bytes"`
}

type Mismatches struct {
	StructuralMismatches uint64 `json:"structural_mismatches"`
	OriginMismatches     uint64 `json:"origin_mismatches"`
	BindingMismatches    uint64 `json:"binding_mismatches"`
	ChainMismatches      uint64 `json:"chain_mismatches"`
	CutMismatches        uint64 `json:"cut_mismatches"`
	SnapshotMismatches   uint64 `json:"snapshot_mismatches"`
}

type Gaps struct {
	UncoveredControls               uint64 `json:"uncovered_controls"`
	LegacyMembershipUnproven        uint64 `json:"legacy_membership_unproven"`
	LegacyEffectBoundaryUnproven    uint64 `json:"legacy_effect_boundary_unproven"`
	OperationSnapshotPrefixUnproven uint64 `json:"operation_snapshot_prefix_unproven"`
}

type Report struct {
	Scope             string                                    `json:"scope"`
	Status            string                                    `json:"status"`
	Balanced          bool                                      `json:"balanced"`
	CoverageComplete  bool                                      `json:"coverage_complete"`
	Base              fundinglotverify.PeriodicBudgetGraphCheck `json:"base"`
	Scanned           Scan                                      `json:"scanned"`
	Mismatches        Mismatches                                `json:"mismatches"`
	Unproven          Gaps                                      `json:"unproven"`
	CertifiedCommands uint64                                    `json:"certified_commands"`
}
