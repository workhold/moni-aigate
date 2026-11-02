package fundinglotverify

import (
	"context"
	"errors"
	"reflect"
	"time"
)

var (
	ErrProcurementEvidenceGraphRowsInput     = errors.New("funding_lot_procurement_evidence_graph_rows_input")
	ErrProcurementEvidenceGraphRowsResource  = errors.New("funding_lot_procurement_evidence_graph_rows_resource")
	ErrProcurementEvidenceGraphRowsCanceled  = errors.New("funding_lot_procurement_evidence_graph_rows_canceled")
	ErrProcurementEvidenceGraphRowsTimeout   = errors.New("funding_lot_procurement_evidence_graph_rows_timeout")
	ErrProcurementEvidenceGraphRowsExecution = errors.New("funding_lot_procurement_evidence_graph_rows_execution")
)

type ProcurementEvidenceRequestRow struct {
	ID, TenantID, InputUpperBound, OutputLimit, PolicyVersion string
	LatestFinalizationID                                      *string
	RequestDeadline                                           time.Time
}
type ProcurementEvidenceAccountRow struct {
	ID, Label, Provider, State, LimitAmount, HeldAmount, SpentAmount, Revision string
	CreatedAt                                                                  time.Time
}
type ProcurementEvidenceReservationRow struct {
	AttemptID, AccountID, BindingID, InputBound, OutputBound, UpperAmount, HeldAmount, SpentAmount, State, Revision string
	Anomaly                                                                                                         bool
	CreatedAt, UpdatedAt                                                                                            time.Time
}
type ProcurementEvidenceBudgetEventRow struct {
	ID, EventKey, AccountID, AttemptID, HeldDelta, SpentDelta, Type, EvidenceRef string
	CreatedAt                                                                    time.Time
}
type ProcurementEvidenceIdentityRow struct {
	AttemptID, Digest, EventID string
	CreatedAt                  time.Time
}
type ProcurementEvidenceMeterRow struct {
	ID, TenantID, RequestID, Source, SourceEventID, UsageJSON string
	AttemptID                                                 *string
	SchemaVersion                                             int32
	HappenedAt, ReceivedAt                                    time.Time
}
type ProcurementEvidenceConflictRow struct {
	ID, EventID, UsageJSON string
	CreatedAt              time.Time
}
type ProcurementEvidenceFinalizationRow struct {
	ID, TenantID, RequestID, EventID, InputDigest, ProtocolOutcome, TransportEndReason, DeliveryStage, FinishReason, PolicyVersion string
	ContentWritten                                                                                                                 bool
	CreatedAt                                                                                                                      time.Time
}

// Rows are raw projections, not authority or a caller-supplied coverage claim.
// The fixed reader owns complete EOF in one read lease; this core is pure.
type ProcurementEvidenceGraphRows struct {
	Channels        []ProcurementAttemptChannelRow
	Credentials     []ProcurementAttemptCredentialRow
	Versions        []ProcurementAttemptVersionRow
	PoolMemberships []ProcurementAttemptPoolMemberRow
	Tenants         []ProcurementAttemptTenantRow
	Requests        []ProcurementEvidenceRequestRow
	CostVersions    []ProcurementAttemptCostVersionRow
	Accounts        []ProcurementEvidenceAccountRow
	Bindings        []ProcurementAttemptBindingRow
	Attempts        []ProcurementAttemptRow
	Reservations    []ProcurementEvidenceReservationRow
	BudgetEvents    []ProcurementEvidenceBudgetEventRow
	Users           []ProcurementApprovalUserRow
	Corrections     []ProcurementCostCorrectionRow
	Proposals       []ProcurementCostProposalRow
	Commands        []ProcurementCommandRow
	Observations    []ProcurementObservationRow
	Audits          []ProcurementAuditRow
	Outbox          []ProcurementOutboxRow
	Identities      []ProcurementEvidenceIdentityRow
	Metering        []ProcurementEvidenceMeterRow
	Conflicts       []ProcurementEvidenceConflictRow
	Finalizations   []ProcurementEvidenceFinalizationRow
}
type ProcurementEvidenceGraphCounts struct {
	Channels        uint64 `json:"channels"`
	Credentials     uint64 `json:"credentials"`
	Versions        uint64 `json:"versions"`
	PoolMemberships uint64 `json:"pool_memberships"`
	Tenants         uint64 `json:"tenants"`
	Requests        uint64 `json:"requests"`
	CostVersions    uint64 `json:"cost_versions"`
	Accounts        uint64 `json:"accounts"`
	Bindings        uint64 `json:"bindings"`
	Attempts        uint64 `json:"attempts"`
	Reservations    uint64 `json:"reservations"`
	BudgetEvents    uint64 `json:"budget_events"`
	Users           uint64 `json:"users"`
	Corrections     uint64 `json:"corrections"`
	Proposals       uint64 `json:"proposals"`
	Commands        uint64 `json:"commands"`
	Observations    uint64 `json:"observations"`
	Audits          uint64 `json:"audits"`
	Outbox          uint64 `json:"outbox"`
	Identities      uint64 `json:"identities"`
	Metering        uint64 `json:"metering"`
	Conflicts       uint64 `json:"conflicts"`
	Finalizations   uint64 `json:"finalizations"`
}
type ProcurementEvidenceGraphStructural struct {
	Budget      ProcurementBudgetProjectionMismatches `json:"budget"`
	Binding     ProcurementAttemptBindingMismatches   `json:"binding"`
	Approval    ProcurementCostApprovalMismatches     `json:"approval"`
	Command     ProcurementCommandMismatches          `json:"command"`
	CostJournal ProcurementCostJournalMismatches      `json:"cost_journal"`
}
type ProcurementEvidenceGraphMismatches struct {
	DuplicateIdentityRows       uint64 `json:"duplicate_identity_rows"`
	DuplicateMeterRows          uint64 `json:"duplicate_meter_rows"`
	DuplicateMeterSourceRows    uint64 `json:"duplicate_meter_source_rows"`
	DuplicateConflictRows       uint64 `json:"duplicate_conflict_rows"`
	DuplicateFinalizationRows   uint64 `json:"duplicate_finalization_rows"`
	InvalidTextRows             uint64 `json:"invalid_text_rows"`
	InvalidTimeRows             uint64 `json:"invalid_time_rows"`
	InvalidUsageRows            uint64 `json:"invalid_usage_rows"`
	InvalidEnumRows             uint64 `json:"invalid_enum_rows"`
	MeterIdentityRows           uint64 `json:"meter_identity_rows"`
	MeterBirthRows              uint64 `json:"meter_birth_rows"`
	ConflictIdentityRows        uint64 `json:"conflict_identity_rows"`
	FinalizationIdentityRows    uint64 `json:"finalization_identity_rows"`
	FinalizationPolicyRows      uint64 `json:"finalization_policy_rows"`
	FinalizationInputDigestRows uint64 `json:"finalization_input_digest_rows"`
	FinalizationStickyRows      uint64 `json:"finalization_sticky_rows"`
	LatestFinalizationRows      uint64 `json:"latest_finalization_rows"`
	IdentitySourceRows          uint64 `json:"identity_source_rows"`
	IdentityDigestRows          uint64 `json:"identity_digest_rows"`
	NormalIdentityMissingRows   uint64 `json:"normal_identity_missing_rows"`
	BudgetEventShapeRows        uint64 `json:"budget_event_shape_rows"`
	BudgetEventSourceRows       uint64 `json:"budget_event_source_rows"`
	ReservedEventRows           uint64 `json:"reserved_event_rows"`
	StateEventRows              uint64 `json:"state_event_rows"`
	EventDeltaRows              uint64 `json:"event_delta_rows"`
	CostRecordedJSONRows        uint64 `json:"cost_recorded_json_rows"`
	CostRecordedSourceRows      uint64 `json:"cost_recorded_source_rows"`
	CostRecordedMissingRows     uint64 `json:"cost_recorded_missing_rows"`
	CurrentCostRows             uint64 `json:"current_cost_rows"`
	ProposalBarrierRows         uint64 `json:"proposal_barrier_rows"`
	AccountCreationRows         uint64 `json:"account_creation_rows"`
	AccountConfigurationRows    uint64 `json:"account_configuration_rows"`
	AccountAdmissionRows        uint64 `json:"account_admission_rows"`
}
type ProcurementEvidenceGraphUnproven struct {
	OriginalDeliveryPredecessorUnproven uint64 `json:"original_delivery_predecessor_unproven"`
	FinalizationOrderUnproven           uint64 `json:"finalization_order_unproven"`
	ConflictIncomingUnproven            uint64 `json:"conflict_incoming_unproven"`
	ConflictReasonUnproven              uint64 `json:"conflict_reason_unproven"`
	IncomingBindingUnproven             uint64 `json:"incoming_binding_unproven"`
	ProcurementEventOrderUnproven       uint64 `json:"procurement_event_order_unproven"`
	CostRecordedEligibilityUnproven     uint64 `json:"cost_recorded_eligibility_unproven"`
	CurrentCostOrderUnproven            uint64 `json:"current_cost_order_unproven"`
	ProposalBarrierUnproven             uint64 `json:"proposal_barrier_unproven"`
	AccountPauseOrderingUnproven        uint64 `json:"account_pause_ordering_unproven"`
	AccountAdmissionOrderUnproven       uint64 `json:"account_admission_order_unproven"`
}
type ProcurementEvidenceGraphCheck struct {
	Scope            string                             `json:"scope"`
	Scanned          ProcurementEvidenceGraphCounts     `json:"scanned"`
	Status           string                             `json:"status"`
	Balanced         bool                               `json:"balanced"`
	CoverageComplete bool                               `json:"coverage_complete"`
	Structural       ProcurementEvidenceGraphStructural `json:"structural"`
	Mismatches       ProcurementEvidenceGraphMismatches `json:"mismatches"`
	Unproven         ProcurementEvidenceGraphUnproven   `json:"unproven"`
}

type procurementEvidenceWork struct {
	ctx   context.Context
	steps uint16
	out   ProcurementEvidenceGraphCheck
}

func (w *procurementEvidenceWork) tick() error {
	w.steps++
	if w.steps&255 == 0 && w.ctx.Err() != nil {
		return procurementEvidenceContextError(w.ctx)
	}
	return nil
}
func procurementEvidenceContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrProcurementEvidenceGraphRowsTimeout
	}
	return ErrProcurementEvidenceGraphRowsCanceled
}
func procurementEvidenceNonzero(v any) bool {
	return procurementEvidenceValueNonzero(reflect.ValueOf(v))
}
func procurementEvidenceValueNonzero(v reflect.Value) bool {
	if v.Kind() == reflect.Struct {
		for i := 0; i < v.NumField(); i++ {
			if procurementEvidenceValueNonzero(v.Field(i)) {
				return true
			}
		}
		return false
	}
	return v.Kind() == reflect.Uint64 && v.Uint() != 0
}

// The preflight counts every original string once, before allocating indexes,
// parsing JSON, or constructing large integers. It never descends into time.Time.
func (w *procurementEvidenceWork) budget(in ProcurementEvidenceGraphRows) error {
	raw := reflect.ValueOf(in)
	counts := reflect.ValueOf(&w.out.Scanned).Elem()
	remaining := uint64(fundingSourceRowLimit)
	bytes := 0
	timeType := reflect.TypeOf(time.Time{})
	for i := 0; i < raw.NumField(); i++ {
		rows := raw.Field(i)
		count := uint64(rows.Len())
		if count > remaining {
			return ErrProcurementEvidenceGraphRowsResource
		}
		remaining -= count
		counts.Field(i).SetUint(count)
		for j := 0; j < rows.Len(); j++ {
			if err := w.tick(); err != nil {
				return err
			}
			row := rows.Index(j)
			textOK, timeOK := true, true
			for k := 0; k < row.NumField(); k++ {
				v := row.Field(k)
				if v.Kind() == reflect.Pointer {
					if v.IsNil() {
						continue
					}
					v = v.Elem()
				}
				if v.Kind() == reflect.String {
					s := v.String()
					if len(s) > fundingSourceByteLimit-bytes {
						return ErrProcurementEvidenceGraphRowsResource
					}
					bytes += len(s)
					textOK = textOK && procurementAttemptIndexableText(s)
				} else if v.Type() == timeType {
					timeOK = timeOK && procurementCommandFiniteTime(v.Interface().(time.Time))
				}
			}
			if !textOK {
				w.out.Mismatches.InvalidTextRows++
			}
			if !timeOK {
				w.out.Mismatches.InvalidTimeRows++
			}
		}
	}
	return nil
}

func (w *procurementEvidenceWork) structural(in ProcurementEvidenceGraphRows) error {
	accounts := make([]ProcurementBudgetAccountRow, 0, len(in.Accounts))
	reservations := make([]ProcurementAttemptReservationRow, 0, len(in.Reservations))
	budgetReservations := make([]ProcurementBudgetReservationRow, 0, len(in.Reservations))
	events := make([]ProcurementBudgetEventRow, 0, len(in.BudgetEvents))
	approvalEvents := make([]ProcurementApprovalEventRow, 0, len(in.BudgetEvents))
	requests := make([]ProcurementAttemptRequestRow, 0, len(in.Requests))
	for _, r := range in.Accounts {
		if err := w.tick(); err != nil {
			return err
		}
		accounts = append(accounts, ProcurementBudgetAccountRow{r.ID, r.HeldAmount, r.SpentAmount})
	}
	for _, r := range in.Reservations {
		if err := w.tick(); err != nil {
			return err
		}
		reservations = append(reservations, ProcurementAttemptReservationRow{r.AttemptID, r.AccountID, r.BindingID, r.InputBound, r.OutputBound, r.UpperAmount, r.HeldAmount, r.SpentAmount, r.State, r.Anomaly})
		budgetReservations = append(budgetReservations, ProcurementBudgetReservationRow{r.AttemptID, r.AccountID, r.UpperAmount, r.HeldAmount, r.SpentAmount, r.State, r.Anomaly})
	}
	for _, r := range in.BudgetEvents {
		if err := w.tick(); err != nil {
			return err
		}
		events = append(events, ProcurementBudgetEventRow{r.ID, r.EventKey, r.AccountID, r.AttemptID, r.HeldDelta, r.SpentDelta})
		approvalEvents = append(approvalEvents, ProcurementApprovalEventRow{r.ID, r.EventKey, r.AccountID, r.AttemptID, r.Type, r.EvidenceRef})
	}
	for _, r := range in.Requests {
		if err := w.tick(); err != nil {
			return err
		}
		requests = append(requests, ProcurementAttemptRequestRow{r.ID, r.TenantID, r.InputUpperBound, r.OutputLimit})
	}
	fail := func(err error) error {
		if w.ctx.Err() != nil {
			return procurementEvidenceContextError(w.ctx)
		}
		return ErrProcurementEvidenceGraphRowsExecution
	}
	b, err := CheckProcurementBudgetProjection(w.ctx, ProcurementBudgetRows{Accounts: accounts, Reservations: budgetReservations, Events: events})
	if err != nil {
		return fail(err)
	}
	if b.Scope != "procurement_budget_projection_v1" || b.Balanced == procurementEvidenceNonzero(b.Mismatches) {
		return ErrProcurementEvidenceGraphRowsExecution
	}
	w.out.Structural.Budget = b.Mismatches
	a, err := CheckProcurementAttemptBindingGraph(w.ctx, ProcurementAttemptBindingRows{Channels: in.Channels, Credentials: in.Credentials, Versions: in.Versions, PoolMemberships: in.PoolMemberships, Tenants: in.Tenants, Requests: requests, CostVersions: in.CostVersions, Accounts: accounts, Bindings: in.Bindings, Attempts: in.Attempts, Reservations: reservations, BudgetEvents: events})
	if err != nil {
		return fail(err)
	}
	if a.Scope != "procurement_attempt_binding_graph_v1" || a.Balanced == procurementEvidenceNonzero(a.Mismatches) {
		return ErrProcurementEvidenceGraphRowsExecution
	}
	w.out.Structural.Binding = a.Mismatches
	p, err := CheckProcurementCostApprovalGraph(w.ctx, ProcurementCostApprovalRows{Users: in.Users, Attempts: in.Attempts, Reservations: reservations, Events: approvalEvents, Corrections: in.Corrections, Proposals: in.Proposals})
	if err != nil {
		return fail(err)
	}
	if p.Scope != "procurement_cost_approval_graph_v1" || p.Balanced == procurementEvidenceNonzero(p.Mismatches) {
		return ErrProcurementEvidenceGraphRowsExecution
	}
	w.out.Structural.Approval = p.Mismatches
	c, err := CheckProcurementCommandGraph(w.ctx, ProcurementCommandRows{Users: in.Users, Accounts: accounts, Bindings: in.Bindings, Commands: in.Commands, Observations: in.Observations, Audits: in.Audits})
	if err != nil {
		return fail(err)
	}
	if c.Scope != "procurement_command_observation_graph_v1" || c.Balanced == procurementEvidenceNonzero(c.Mismatches) {
		return ErrProcurementEvidenceGraphRowsExecution
	}
	w.out.Structural.Command = c.Mismatches
	j, err := CheckProcurementCostJournalGraph(w.ctx, ProcurementCostJournalRows{Users: in.Users, Attempts: in.Attempts, Reservations: reservations, Corrections: in.Corrections, Proposals: in.Proposals, Audits: in.Audits, Outbox: in.Outbox})
	if err != nil {
		return fail(err)
	}
	if j.Scope != "procurement_cost_journal_graph_v1" || j.Balanced == procurementEvidenceNonzero(j.Mismatches) {
		return ErrProcurementEvidenceGraphRowsExecution
	}
	w.out.Structural.CostJournal = j.Mismatches
	return nil
}

func CheckProcurementEvidenceGraph(ctx context.Context, in ProcurementEvidenceGraphRows) (ProcurementEvidenceGraphCheck, error) {
	if ctx == nil {
		return ProcurementEvidenceGraphCheck{}, ErrProcurementEvidenceGraphRowsInput
	}
	if ctx.Err() != nil {
		return ProcurementEvidenceGraphCheck{}, procurementEvidenceContextError(ctx)
	}
	w := procurementEvidenceWork{ctx: ctx}
	w.out.Scope = "procurement_domain_evidence_graph_v1"
	if err := w.budget(in); err != nil {
		return ProcurementEvidenceGraphCheck{}, err
	}
	if err := w.structural(in); err != nil {
		return ProcurementEvidenceGraphCheck{}, err
	}
	x, err := w.index(in)
	if err != nil {
		return ProcurementEvidenceGraphCheck{}, err
	}
	for _, f := range []func() error{x.evidence, x.identities, x.events, x.costs, x.accounts} {
		if err := f(); err != nil {
			return ProcurementEvidenceGraphCheck{}, err
		}
	}
	if ctx.Err() != nil {
		return ProcurementEvidenceGraphCheck{}, procurementEvidenceContextError(ctx)
	}
	w.out.CoverageComplete = !procurementEvidenceNonzero(w.out.Unproven)
	switch {
	case procurementEvidenceNonzero(w.out.Structural) || procurementEvidenceNonzero(w.out.Mismatches):
		w.out.Status = "mismatch"
	case !w.out.CoverageComplete:
		w.out.Status = "indeterminate"
	default:
		w.out.Status = "balanced"
		w.out.Balanced = true
	}
	return w.out, nil
}
