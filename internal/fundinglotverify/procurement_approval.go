package fundinglotverify

import (
	"context"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrProcurementCostApprovalRowsInput    = errors.New("funding_lot_procurement_cost_approval_rows_input")
	ErrProcurementCostApprovalRowsResource = errors.New("funding_lot_procurement_cost_approval_rows_resource")
	ErrProcurementCostApprovalRowsCanceled = errors.New("funding_lot_procurement_cost_approval_rows_canceled")
	ErrProcurementCostApprovalRowsTimeout  = errors.New("funding_lot_procurement_cost_approval_rows_timeout")
)

type ProcurementApprovalUserRow struct{ ID string }

type ProcurementCostCorrectionRow struct {
	ID, TenantID, AttemptID, OperationID, Amount, Basis, EvidenceRef string
	CreatedAt                                                        time.Time
}

type ProcurementCostProposalRow struct {
	ID, TenantID, AttemptID, Amount, Basis, EvidenceRef, Reason, Requester string
	ExpectedRevision, ExpectedEvidenceCount, State                         string
	Approver, ApprovalOperationID                                          *string
	CreatedAt                                                              time.Time
	ApprovedAt                                                             *time.Time
}

type ProcurementApprovalEventRow struct{ ID, EventKey, AccountID, AttemptID, Type, EvidenceRef string }

// ProcurementCostApprovalRows contains raw rows from one procurement snapshot.
// The fixed reader owns completeness; these DTOs grant no maintenance authority.
type ProcurementCostApprovalRows struct {
	Users        []ProcurementApprovalUserRow
	Attempts     []ProcurementAttemptRow
	Reservations []ProcurementAttemptReservationRow
	Events       []ProcurementApprovalEventRow
	Corrections  []ProcurementCostCorrectionRow
	Proposals    []ProcurementCostProposalRow
}

type ProcurementCostApprovalCounts struct {
	Users        uint64 `json:"users"`
	Attempts     uint64 `json:"attempts"`
	Reservations uint64 `json:"reservations"`
	Events       uint64 `json:"events"`
	Corrections  uint64 `json:"corrections"`
	Proposals    uint64 `json:"proposals"`
}

type ProcurementCostApprovalMismatches struct {
	DuplicateUserRows                uint64 `json:"duplicate_user_rows"`
	DuplicateAttemptRows             uint64 `json:"duplicate_attempt_rows"`
	DuplicateReservationRows         uint64 `json:"duplicate_reservation_rows"`
	DuplicateEventRows               uint64 `json:"duplicate_event_rows"`
	DuplicateEventKeyRows            uint64 `json:"duplicate_event_key_rows"`
	DuplicateCorrectionRows          uint64 `json:"duplicate_correction_rows"`
	DuplicateCorrectionOperationRows uint64 `json:"duplicate_correction_operation_rows"`
	DuplicateProposalRows            uint64 `json:"duplicate_proposal_rows"`
	DuplicateApprovalOperationRows   uint64 `json:"duplicate_approval_operation_rows"`
	InvalidTextRows                  uint64 `json:"invalid_text_rows"`
	InvalidAmountRows                uint64 `json:"invalid_amount_rows"`
	InvalidEnumRows                  uint64 `json:"invalid_enum_rows"`
	ProposalShapeRows                uint64 `json:"proposal_shape_rows"`
	ProposalIdentityRows             uint64 `json:"proposal_identity_rows"`
	CorrectionIdentityRows           uint64 `json:"correction_identity_rows"`
	ApprovedCorrectionRows           uint64 `json:"approved_correction_rows"`
	ApprovedEventRows                uint64 `json:"approved_event_rows"`
	ManagedCorrectionApprovalRows    uint64 `json:"managed_correction_approval_rows"`
	CorrectionEventApprovalRows      uint64 `json:"correction_event_approval_rows"`
}

// ProcurementCostApprovalCheck describes only the supplied historical approval
// graph. It does not prove supplier charges or complete procurement coverage.
type ProcurementCostApprovalCheck struct {
	Scope      string                            `json:"scope"`
	Scanned    ProcurementCostApprovalCounts     `json:"scanned"`
	Balanced   bool                              `json:"balanced"`
	Mismatches ProcurementCostApprovalMismatches `json:"mismatches"`
}

type procurementApprovalWork struct {
	ctx   context.Context
	steps uint16
	bytes int
}

func procurementApprovalContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrProcurementCostApprovalRowsTimeout
	}
	return ErrProcurementCostApprovalRowsCanceled
}

func (w *procurementApprovalWork) tick() error {
	w.steps++
	if w.steps&255 == 0 && w.ctx.Err() != nil {
		return procurementApprovalContextError(w.ctx)
	}
	return nil
}

// text accounts for every returned text value, including the reused attempt
// and reservation rows. Required fields cannot be empty; optional allows empty
// strings only for event evidence, never for nullable proposal/quote fields.
func (w *procurementApprovalWork) text(required, optional []string) (bool, error) {
	valid := true
	for _, fields := range [][]string{required, optional} {
		for _, value := range fields {
			if len(value) > fundingSourceByteLimit-w.bytes {
				return false, ErrProcurementCostApprovalRowsResource
			}
			w.bytes += len(value)
			if !utf8.ValidString(value) || procurementApprovalContainsNUL(value) {
				valid = false
			}
		}
	}
	for _, value := range required {
		if value == "" {
			valid = false
		}
	}
	return valid, nil
}

func procurementApprovalContainsNUL(value string) bool {
	for i := range value {
		if value[i] == 0 {
			return true
		}
	}
	return false
}

func procurementApprovalUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
		} else if !(value[i] >= '0' && value[i] <= '9' || value[i] >= 'a' && value[i] <= 'f') {
			return false
		}
	}
	return true
}

func procurementApprovalBoundText(value string, limit int) bool {
	if len(value) > limit || !procurementAttemptValidText(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func procurementApprovalTextPass(w *procurementApprovalWork, rows ProcurementCostApprovalRows) (uint64, error) {
	var invalid uint64
	check := func(required, optional []string, extraValid bool) error {
		if err := w.tick(); err != nil {
			return err
		}
		valid, err := w.text(required, optional)
		if err != nil {
			return err
		}
		if !valid || !extraValid {
			invalid++
		}
		return nil
	}
	for _, row := range rows.Users {
		if err := check([]string{row.ID}, nil, true); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Attempts {
		fields := []string{row.ID, row.TenantID, row.RequestID, row.ChannelID, row.ChannelVersionID, row.CredentialID, row.UpstreamModel, row.DeliveryState, row.CostBasis, row.CostStatus}
		for _, value := range []*string{row.CostVersionID, row.CostInputRate, row.CostOutputRate, row.CostAmount} {
			if value != nil {
				fields = append(fields, *value)
			}
		}
		if err := check(fields, nil, true); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Reservations {
		if err := check([]string{row.AttemptID, row.AccountID, row.BindingID, row.InputBound, row.OutputBound, row.UpperAmount, row.HeldAmount, row.SpentAmount, row.State}, nil, true); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Events {
		if err := check([]string{row.ID, row.EventKey, row.AccountID, row.AttemptID, row.Type}, []string{row.EvidenceRef}, true); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Corrections {
		if err := check([]string{row.ID, row.TenantID, row.AttemptID, row.OperationID, row.Amount, row.Basis, row.EvidenceRef}, nil, true); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Proposals {
		fields := []string{row.ID, row.TenantID, row.AttemptID, row.Amount, row.Basis, row.EvidenceRef, row.Reason, row.Requester, row.ExpectedRevision, row.ExpectedEvidenceCount, row.State}
		for _, value := range []*string{row.Approver, row.ApprovalOperationID} {
			if value != nil {
				fields = append(fields, *value)
			}
		}
		extraValid := procurementApprovalUUID(row.ID) && (row.ApprovalOperationID == nil || procurementApprovalUUID(*row.ApprovalOperationID)) &&
			procurementApprovalBoundText(row.EvidenceRef, 256) && procurementApprovalBoundText(row.Reason, 400)
		if err := check(fields, nil, extraValid); err != nil {
			return 0, err
		}
	}
	return invalid, nil
}

// seen retains the first legal key only. It also keeps malformed identities out
// of relation indexes without turning a data mismatch into an execution error.
func procurementApprovalSeen(seen map[string]struct{}, value string, valid bool) bool {
	if !valid {
		return false
	}
	if _, exists := seen[value]; exists {
		return true
	}
	seen[value] = struct{}{}
	return false
}

func procurementApprovalShape(row ProcurementCostProposalRow) bool {
	if row.State == "proposed" {
		return row.Approver == nil && row.ApprovalOperationID == nil && row.ApprovedAt == nil
	}
	return row.State == "approved" && row.Approver != nil && row.ApprovalOperationID != nil && row.ApprovedAt != nil &&
		*row.Approver != row.Requester && *row.ApprovalOperationID != row.ID
}

func procurementApprovalCorrectionMatches(proposal ProcurementCostProposalRow, correction ProcurementCostCorrectionRow) bool {
	if proposal.ApprovalOperationID == nil || correction.OperationID != "procurement:"+*proposal.ApprovalOperationID ||
		correction.TenantID != proposal.TenantID || correction.AttemptID != proposal.AttemptID ||
		correction.Basis != proposal.Basis || correction.EvidenceRef != proposal.EvidenceRef {
		return false
	}
	left, leftOK := procurementAttemptInt64(proposal.Amount)
	right, rightOK := procurementAttemptInt64(correction.Amount)
	return leftOK && rightOK && left.Cmp(right) == 0
}

func procurementApprovalEventMatches(proposal ProcurementCostProposalRow, event ProcurementApprovalEventRow, reservations map[string]ProcurementAttemptReservationRow) bool {
	reservation, exists := reservations[proposal.AttemptID]
	return exists && proposal.ApprovalOperationID != nil && event.EventKey == "correction:"+*proposal.ApprovalOperationID &&
		event.Type == "cost_corrected" && event.AttemptID == proposal.AttemptID && event.AccountID == reservation.AccountID && event.EvidenceRef == proposal.EvidenceRef
}

// CheckProcurementCostApprovalGraph independently compares both directions of
// the cost proposal/correction/event graph. Historical final costs, reservation
// revisions, evidence counts, user roles and process-clock order are not oracles.
func CheckProcurementCostApprovalGraph(ctx context.Context, rows ProcurementCostApprovalRows) (ProcurementCostApprovalCheck, error) {
	if ctx == nil {
		return ProcurementCostApprovalCheck{}, ErrProcurementCostApprovalRowsInput
	}
	if ctx.Err() != nil {
		return ProcurementCostApprovalCheck{}, procurementApprovalContextError(ctx)
	}
	remaining := uint64(fundingSourceRowLimit)
	for _, count := range [...]int{len(rows.Users), len(rows.Attempts), len(rows.Reservations), len(rows.Events), len(rows.Corrections), len(rows.Proposals)} {
		if uint64(count) > remaining {
			return ProcurementCostApprovalCheck{}, ErrProcurementCostApprovalRowsResource
		}
		remaining -= uint64(count)
	}
	w := procurementApprovalWork{ctx: ctx}
	invalidText, err := procurementApprovalTextPass(&w, rows)
	if err != nil {
		return ProcurementCostApprovalCheck{}, err
	}
	out := ProcurementCostApprovalCheck{
		Scope: "procurement_cost_approval_graph_v1",
		Scanned: ProcurementCostApprovalCounts{Users: uint64(len(rows.Users)), Attempts: uint64(len(rows.Attempts)), Reservations: uint64(len(rows.Reservations)),
			Events: uint64(len(rows.Events)), Corrections: uint64(len(rows.Corrections)), Proposals: uint64(len(rows.Proposals))},
		Mismatches: ProcurementCostApprovalMismatches{InvalidTextRows: invalidText},
	}
	for _, row := range rows.Corrections {
		if err := w.tick(); err != nil {
			return ProcurementCostApprovalCheck{}, err
		}
		if _, ok := procurementAttemptInt64(row.Amount); !ok {
			out.Mismatches.InvalidAmountRows++
		}
		if !procurementAttemptStatus(row.Basis, "contract", "invoice") {
			out.Mismatches.InvalidEnumRows++
		}
	}
	for _, row := range rows.Proposals {
		if err := w.tick(); err != nil {
			return ProcurementCostApprovalCheck{}, err
		}
		_, amountOK := procurementAttemptInt64(row.Amount)
		_, revisionOK := procurementAttemptPositiveInt64(row.ExpectedRevision)
		_, countOK := procurementAttemptInt64(row.ExpectedEvidenceCount)
		if !amountOK || !revisionOK || !countOK {
			out.Mismatches.InvalidAmountRows++
		}
		if !procurementAttemptStatus(row.Basis, "contract", "invoice") || !procurementAttemptStatus(row.State, "proposed", "approved") {
			out.Mismatches.InvalidEnumRows++
		}
	}

	users := make(map[string]struct{}, len(rows.Users))
	for _, row := range rows.Users {
		if err := w.tick(); err != nil {
			return ProcurementCostApprovalCheck{}, err
		}
		if procurementApprovalSeen(users, row.ID, procurementAttemptValidText(row.ID)) {
			out.Mismatches.DuplicateUserRows++
		}
	}
	attempts := make(map[string]ProcurementAttemptRow, len(rows.Attempts))
	for _, row := range rows.Attempts {
		if err := w.tick(); err != nil {
			return ProcurementCostApprovalCheck{}, err
		}
		if !procurementAttemptValidText(row.ID) {
			continue
		}
		if _, exists := attempts[row.ID]; exists {
			out.Mismatches.DuplicateAttemptRows++
			continue
		}
		attempts[row.ID] = row
	}
	reservations := make(map[string]ProcurementAttemptReservationRow, len(rows.Reservations))
	for _, row := range rows.Reservations {
		if err := w.tick(); err != nil {
			return ProcurementCostApprovalCheck{}, err
		}
		if !procurementAttemptValidText(row.AttemptID) {
			continue
		}
		if _, exists := reservations[row.AttemptID]; exists {
			out.Mismatches.DuplicateReservationRows++
			continue
		}
		reservations[row.AttemptID] = row
	}
	events := make(map[string]ProcurementApprovalEventRow, len(rows.Events))
	eventIDs := make(map[string]struct{}, len(rows.Events))
	eventKeys := make(map[string]struct{}, len(rows.Events))
	canonicalEvents := make([]ProcurementApprovalEventRow, 0, len(rows.Events))
	for _, row := range rows.Events {
		if err := w.tick(); err != nil {
			return ProcurementCostApprovalCheck{}, err
		}
		duplicateID := procurementApprovalSeen(eventIDs, row.ID, procurementAttemptValidText(row.ID))
		duplicateKey := procurementApprovalSeen(eventKeys, row.EventKey, procurementAttemptValidText(row.EventKey))
		if duplicateID {
			out.Mismatches.DuplicateEventRows++
		}
		if duplicateKey {
			out.Mismatches.DuplicateEventKeyRows++
		}
		if duplicateID || duplicateKey {
			continue
		}
		canonicalEvents = append(canonicalEvents, row)
		if procurementAttemptValidText(row.ID) && procurementAttemptValidText(row.EventKey) {
			events[row.EventKey] = row
		}
	}
	corrections := make(map[string]ProcurementCostCorrectionRow, len(rows.Corrections))
	correctionIDs := make(map[string]struct{}, len(rows.Corrections))
	correctionOperations := make(map[string]struct{}, len(rows.Corrections))
	canonicalCorrections := make([]ProcurementCostCorrectionRow, 0, len(rows.Corrections))
	for _, row := range rows.Corrections {
		if err := w.tick(); err != nil {
			return ProcurementCostApprovalCheck{}, err
		}
		duplicateID := procurementApprovalSeen(correctionIDs, row.ID, procurementAttemptValidText(row.ID))
		duplicateOperation := procurementApprovalSeen(correctionOperations, row.OperationID, procurementAttemptValidText(row.OperationID))
		if duplicateID {
			out.Mismatches.DuplicateCorrectionRows++
		}
		if duplicateOperation {
			out.Mismatches.DuplicateCorrectionOperationRows++
		}
		if duplicateID || duplicateOperation {
			continue
		}
		canonicalCorrections = append(canonicalCorrections, row)
		if procurementAttemptValidText(row.ID) && procurementAttemptValidText(row.OperationID) {
			corrections[row.OperationID] = row
		}
	}
	proposals := make(map[string]ProcurementCostProposalRow, len(rows.Proposals))
	proposalsByEvent := make(map[string]ProcurementCostProposalRow, len(rows.Proposals))
	proposalIDs := make(map[string]struct{}, len(rows.Proposals))
	approvalOperations := make(map[string]struct{}, len(rows.Proposals))
	canonicalProposals := make([]ProcurementCostProposalRow, 0, len(rows.Proposals))
	for _, row := range rows.Proposals {
		if err := w.tick(); err != nil {
			return ProcurementCostApprovalCheck{}, err
		}
		duplicateID := procurementApprovalSeen(proposalIDs, row.ID, procurementApprovalUUID(row.ID))
		duplicateOperation := false
		if row.ApprovalOperationID != nil {
			duplicateOperation = procurementApprovalSeen(approvalOperations, *row.ApprovalOperationID, procurementApprovalUUID(*row.ApprovalOperationID))
		}
		if duplicateID {
			out.Mismatches.DuplicateProposalRows++
		}
		if duplicateOperation {
			out.Mismatches.DuplicateApprovalOperationRows++
		}
		if duplicateID || duplicateOperation {
			continue
		}
		canonicalProposals = append(canonicalProposals, row)
		if procurementApprovalUUID(row.ID) && row.State == "approved" && procurementApprovalShape(row) && procurementApprovalUUID(*row.ApprovalOperationID) {
			proposals["procurement:"+*row.ApprovalOperationID] = row
			proposalsByEvent["correction:"+*row.ApprovalOperationID] = row
		}
	}

	for _, row := range canonicalProposals {
		if err := w.tick(); err != nil {
			return ProcurementCostApprovalCheck{}, err
		}
		shapeValid := procurementApprovalShape(row)
		if !shapeValid {
			out.Mismatches.ProposalShapeRows++
		}
		attempt, attemptOK := attempts[row.AttemptID]
		_, reservationOK := reservations[row.AttemptID]
		_, requesterOK := users[row.Requester]
		identityValid := attemptOK && reservationOK && requesterOK && procurementAttemptValidText(row.TenantID) && row.TenantID == attempt.TenantID
		if row.State == "approved" {
			if row.Approver == nil {
				identityValid = false
			} else if _, exists := users[*row.Approver]; !exists {
				identityValid = false
			}
		}
		if !identityValid {
			out.Mismatches.ProposalIdentityRows++
		}
		if row.State != "approved" || !shapeValid {
			continue
		}
		correction, exists := corrections["procurement:"+*row.ApprovalOperationID]
		if !exists || !procurementApprovalCorrectionMatches(row, correction) {
			out.Mismatches.ApprovedCorrectionRows++
		}
		event, exists := events["correction:"+*row.ApprovalOperationID]
		if !exists || !procurementApprovalEventMatches(row, event, reservations) {
			out.Mismatches.ApprovedEventRows++
		}
	}
	for _, row := range canonicalCorrections {
		if err := w.tick(); err != nil {
			return ProcurementCostApprovalCheck{}, err
		}
		attempt, exists := attempts[row.AttemptID]
		if !exists || !procurementAttemptValidText(row.TenantID) || row.TenantID != attempt.TenantID {
			out.Mismatches.CorrectionIdentityRows++
		}
		if _, managed := reservations[row.AttemptID]; !managed {
			continue
		}
		proposal, exists := proposals[row.OperationID]
		if !exists || !procurementApprovalCorrectionMatches(proposal, row) {
			out.Mismatches.ManagedCorrectionApprovalRows++
		}
	}
	for _, row := range canonicalEvents {
		if err := w.tick(); err != nil {
			return ProcurementCostApprovalCheck{}, err
		}
		if row.Type != "cost_corrected" {
			continue
		}
		// Use the exact full key, without classifying other events or legacy
		// corrections by a string prefix.
		proposal, exists := proposalsByEvent[row.EventKey]
		if !exists || !procurementApprovalEventMatches(proposal, row, reservations) {
			out.Mismatches.CorrectionEventApprovalRows++
		}
	}
	if ctx.Err() != nil {
		return ProcurementCostApprovalCheck{}, procurementApprovalContextError(ctx)
	}
	out.Balanced = out.Mismatches == (ProcurementCostApprovalMismatches{})
	return out, nil
}
