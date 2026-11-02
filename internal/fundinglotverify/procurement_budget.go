package fundinglotverify

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"unicode/utf8"
)

var (
	ErrProcurementBudgetRowsInput    = errors.New("funding_lot_procurement_budget_rows_input")
	ErrProcurementBudgetRowsResource = errors.New("funding_lot_procurement_budget_rows_resource")
	ErrProcurementBudgetRowsCanceled = errors.New("funding_lot_procurement_budget_rows_canceled")
	ErrProcurementBudgetRowsTimeout  = errors.New("funding_lot_procurement_budget_rows_timeout")
)

// ProcurementBudgetAccountRow is the minimal account balance projection.
// Numeric values remain decimal text to preserve PostgreSQL NUMERIC exactly.
type ProcurementBudgetAccountRow struct{ ID, HeldAmount, SpentAmount string }

// ProcurementBudgetReservationRow contains only the fields needed to check
// the procurement account projection and its local state/bucket invariants.
type ProcurementBudgetReservationRow struct {
	AttemptID, AccountID, UpperAmount, HeldAmount, SpentAmount, State string
	Anomaly                                                           bool
}

// ProcurementBudgetEventRow is one immutable budget delta fact.
type ProcurementBudgetEventRow struct{ ID, EventKey, AccountID, AttemptID, HeldDelta, SpentDelta string }

// ProcurementBudgetRows is the complete caller-supplied projection for the
// fixed local scope. The fixed database reader, not this DTO, owns EOF proof.
type ProcurementBudgetRows struct {
	Accounts     []ProcurementBudgetAccountRow
	Reservations []ProcurementBudgetReservationRow
	Events       []ProcurementBudgetEventRow
}

type ProcurementBudgetRowCounts struct {
	Accounts     uint64 `json:"accounts"`
	Reservations uint64 `json:"reservations"`
	Events       uint64 `json:"events"`
}

type ProcurementBudgetProjectionMismatches struct {
	DuplicateAccounts                 uint64 `json:"duplicate_accounts"`
	DuplicateReservations             uint64 `json:"duplicate_reservations"`
	DuplicateEvents                   uint64 `json:"duplicate_events"`
	DuplicateEventKeys                uint64 `json:"duplicate_event_keys"`
	InvalidTextRows                   uint64 `json:"invalid_text_rows"`
	InvalidAmountRows                 uint64 `json:"invalid_amount_rows"`
	OrphanReservations                uint64 `json:"orphan_reservations"`
	OrphanEvents                      uint64 `json:"orphan_events"`
	ReservationStateMismatches        uint64 `json:"reservation_state_mismatches"`
	ReservationBucketMismatches       uint64 `json:"reservation_bucket_mismatches"`
	AccountHeldReservationMismatches  uint64 `json:"account_held_reservation_mismatches"`
	AccountSpentReservationMismatches uint64 `json:"account_spent_reservation_mismatches"`
	AccountHeldEventMismatches        uint64 `json:"account_held_event_mismatches"`
	AccountSpentEventMismatches       uint64 `json:"account_spent_event_mismatches"`
}

// ProcurementBudgetProjectionCheck is descriptive only. Balanced applies to
// this three-table projection and says nothing about supplier invoice truth.
type ProcurementBudgetProjectionCheck struct {
	Scope      string                                `json:"scope"`
	Scanned    ProcurementBudgetRowCounts            `json:"scanned"`
	Balanced   bool                                  `json:"balanced"`
	Mismatches ProcurementBudgetProjectionMismatches `json:"mismatches"`
}

type procurementBudgetAccount struct {
	held, spent                                 *big.Int
	heldValid, spentValid                       bool
	reservationHeld, reservationSpent           big.Int
	eventHeld, eventSpent                       big.Int
	reservationHeldValid, reservationSpentValid bool
	eventHeldValid, eventSpentValid             bool
}

type procurementBudgetWork struct {
	ctx      context.Context
	steps    uint16
	bytes    int
	resource bool
}

func (w *procurementBudgetWork) tick() error {
	w.steps++
	if w.steps&255 == 0 && w.ctx.Err() != nil {
		return procurementBudgetContextError(w.ctx)
	}
	return nil
}

func procurementBudgetContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrProcurementBudgetRowsTimeout
	}
	return ErrProcurementBudgetRowsCanceled
}

func (w *procurementBudgetWork) text(fields ...string) bool {
	valid := true
	for _, value := range fields {
		if len(value) > fundingSourceByteLimit-w.bytes {
			w.resource = true
			return false
		}
		w.bytes += len(value)
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || value == "" {
			valid = false
		}
	}
	return valid
}

func procurementBudgetUnsigned(value string) (*big.Int, bool) {
	if value == "" || len(value) > 100 || len(value) > 1 && value[0] == '0' {
		return nil, false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(value, 10)
	return n, ok
}

func procurementBudgetSigned(value string) (*big.Int, bool) {
	if value == "" {
		return nil, false
	}
	negative := value[0] == '-'
	digits := value
	if negative {
		digits = value[1:]
		if digits == "0" {
			return nil, false
		}
	}
	n, ok := procurementBudgetUnsigned(digits)
	if !ok {
		return nil, false
	}
	if negative {
		n.Neg(n)
	}
	return n, true
}

func procurementBudgetRowsCount(rows ProcurementBudgetRows) uint64 {
	return uint64(len(rows.Accounts)) + uint64(len(rows.Reservations)) + uint64(len(rows.Events))
}

func (w *procurementBudgetWork) validateBudgets(rows ProcurementBudgetRows) (uint64, error) {
	total := procurementBudgetRowsCount(rows)
	if total > fundingSourceRowLimit {
		return 0, ErrProcurementBudgetRowsResource
	}
	var invalidText uint64
	for _, row := range rows.Accounts {
		if err := w.tick(); err != nil {
			return 0, err
		}
		if !w.text(row.ID, row.HeldAmount, row.SpentAmount) {
			invalidText++
		}
		if w.resource {
			return 0, ErrProcurementBudgetRowsResource
		}
	}
	for _, row := range rows.Reservations {
		if err := w.tick(); err != nil {
			return 0, err
		}
		if !w.text(row.AttemptID, row.AccountID, row.UpperAmount, row.HeldAmount, row.SpentAmount, row.State) {
			invalidText++
		}
		if w.resource {
			return 0, ErrProcurementBudgetRowsResource
		}
	}
	for _, row := range rows.Events {
		if err := w.tick(); err != nil {
			return 0, err
		}
		if !w.text(row.ID, row.EventKey, row.AccountID, row.AttemptID, row.HeldDelta, row.SpentDelta) {
			invalidText++
		}
		if w.resource {
			return 0, ErrProcurementBudgetRowsResource
		}
	}
	return invalidText, nil
}

// CheckProcurementBudgetProjection independently compares all supplied
// account balances with reservation totals and immutable event deltas. It
// never trusts database aggregates, triggers, state filters, or SQL joins.
func CheckProcurementBudgetProjection(ctx context.Context, rows ProcurementBudgetRows) (ProcurementBudgetProjectionCheck, error) {
	if ctx == nil {
		return ProcurementBudgetProjectionCheck{}, ErrProcurementBudgetRowsInput
	}
	if ctx.Err() != nil {
		return ProcurementBudgetProjectionCheck{}, procurementBudgetContextError(ctx)
	}
	w := procurementBudgetWork{ctx: ctx}
	invalidText, err := w.validateBudgets(rows)
	if err != nil {
		return ProcurementBudgetProjectionCheck{}, err
	}
	out := ProcurementBudgetProjectionCheck{
		Scope:      "procurement_budget_projection_v1",
		Scanned:    ProcurementBudgetRowCounts{Accounts: uint64(len(rows.Accounts)), Reservations: uint64(len(rows.Reservations)), Events: uint64(len(rows.Events))},
		Mismatches: ProcurementBudgetProjectionMismatches{InvalidTextRows: invalidText},
	}
	accounts := make(map[string]*procurementBudgetAccount, len(rows.Accounts))
	seenAccounts := make(map[string]struct{}, len(rows.Accounts))
	validText := func(fields ...string) bool {
		for _, field := range fields {
			if field == "" || !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
				return false
			}
		}
		return true
	}
	for _, row := range rows.Accounts {
		if err = w.tick(); err != nil {
			return ProcurementBudgetProjectionCheck{}, err
		}
		textOK := validText(row.ID, row.HeldAmount, row.SpentAmount)
		held, heldOK := procurementBudgetUnsigned(row.HeldAmount)
		spent, spentOK := procurementBudgetUnsigned(row.SpentAmount)
		if !heldOK || !spentOK {
			out.Mismatches.InvalidAmountRows++
		}
		if row.ID != "" && utf8.ValidString(row.ID) && !strings.ContainsRune(row.ID, 0) {
			if _, exists := seenAccounts[row.ID]; exists {
				out.Mismatches.DuplicateAccounts++
				continue
			}
			seenAccounts[row.ID] = struct{}{}
		}
		if !textOK {
			continue
		}
		accounts[row.ID] = &procurementBudgetAccount{held: held, spent: spent, heldValid: heldOK, spentValid: spentOK, reservationHeldValid: true, reservationSpentValid: true, eventHeldValid: true, eventSpentValid: true}
	}
	reservations := make(map[string]string, len(rows.Reservations))
	seenReservations := make(map[string]struct{}, len(rows.Reservations))
	for _, row := range rows.Reservations {
		if err = w.tick(); err != nil {
			return ProcurementBudgetProjectionCheck{}, err
		}
		textOK := validText(row.AttemptID, row.AccountID, row.UpperAmount, row.HeldAmount, row.SpentAmount, row.State)
		upper, upperOK := procurementBudgetUnsigned(row.UpperAmount)
		held, heldOK := procurementBudgetUnsigned(row.HeldAmount)
		spent, spentOK := procurementBudgetUnsigned(row.SpentAmount)
		if !upperOK || upper.Cmp(big.NewInt(1<<63-1)) > 0 || !heldOK || !spentOK {
			out.Mismatches.InvalidAmountRows++
		}
		stateValid := row.State == "prepared" || row.State == "sending" || row.State == "unknown" || row.State == "settled" || row.State == "released" || row.State == "disputed"
		if !stateValid {
			out.Mismatches.ReservationStateMismatches++
		}
		if upperOK && upper.Cmp(big.NewInt(1<<63-1)) <= 0 && heldOK && spentOK {
			bucketMismatch := false
			switch row.State {
			case "prepared", "sending":
				bucketMismatch = held.Cmp(upper) != 0 || spent.Sign() != 0 || row.Anomaly
			case "settled":
				bucketMismatch = held.Sign() != 0
			case "released":
				bucketMismatch = held.Sign() != 0 || spent.Sign() != 0
			}
			if bucketMismatch {
				out.Mismatches.ReservationBucketMismatches++
			}
		}
		identityValid := row.AttemptID != "" && utf8.ValidString(row.AttemptID) && !strings.ContainsRune(row.AttemptID, 0)
		duplicate := false
		if identityValid {
			if _, exists := seenReservations[row.AttemptID]; exists {
				out.Mismatches.DuplicateReservations++
				duplicate = true
			} else {
				seenReservations[row.AttemptID] = struct{}{}
			}
			if row.AccountID != "" && utf8.ValidString(row.AccountID) && !strings.ContainsRune(row.AccountID, 0) {
				if _, exists := reservations[row.AttemptID]; !exists {
					reservations[row.AttemptID] = row.AccountID
				}
			}
		}
		if !textOK {
			continue
		}
		account, exists := accounts[row.AccountID]
		if !exists {
			out.Mismatches.OrphanReservations++
		}
		if duplicate || !exists {
			continue
		}
		if !heldOK {
			account.reservationHeldValid = false
		} else {
			account.reservationHeld.Add(&account.reservationHeld, held)
		}
		if !spentOK {
			account.reservationSpentValid = false
		} else {
			account.reservationSpent.Add(&account.reservationSpent, spent)
		}
	}
	eventIDs := make(map[string]struct{}, len(rows.Events))
	eventKeys := make(map[string]struct{}, len(rows.Events))
	for _, row := range rows.Events {
		if err = w.tick(); err != nil {
			return ProcurementBudgetProjectionCheck{}, err
		}
		textOK := validText(row.ID, row.EventKey, row.AccountID, row.AttemptID, row.HeldDelta, row.SpentDelta)
		held, heldOK := procurementBudgetSigned(row.HeldDelta)
		spent, spentOK := procurementBudgetSigned(row.SpentDelta)
		if !heldOK || !spentOK {
			out.Mismatches.InvalidAmountRows++
		}
		duplicate := false
		if row.ID != "" && utf8.ValidString(row.ID) && !strings.ContainsRune(row.ID, 0) {
			if _, exists := eventIDs[row.ID]; exists {
				out.Mismatches.DuplicateEvents++
				duplicate = true
			} else {
				eventIDs[row.ID] = struct{}{}
			}
		}
		if row.EventKey != "" && utf8.ValidString(row.EventKey) && !strings.ContainsRune(row.EventKey, 0) {
			if _, exists := eventKeys[row.EventKey]; exists {
				out.Mismatches.DuplicateEventKeys++
				duplicate = true
			} else {
				eventKeys[row.EventKey] = struct{}{}
			}
		}
		if !textOK {
			continue
		}
		orphan := reservations[row.AttemptID] != row.AccountID
		account, exists := accounts[row.AccountID]
		if !exists {
			orphan = true
		}
		if orphan {
			out.Mismatches.OrphanEvents++
		}
		if duplicate {
			continue
		}
		if !exists {
			continue
		}
		if !heldOK {
			account.eventHeldValid = false
		} else {
			account.eventHeld.Add(&account.eventHeld, held)
		}
		if !spentOK {
			account.eventSpentValid = false
		} else {
			account.eventSpent.Add(&account.eventSpent, spent)
		}
	}
	for _, account := range accounts {
		if err = w.tick(); err != nil {
			return ProcurementBudgetProjectionCheck{}, err
		}
		if account.heldValid && account.reservationHeldValid && account.held.Cmp(&account.reservationHeld) != 0 {
			out.Mismatches.AccountHeldReservationMismatches++
		}
		if account.spentValid && account.reservationSpentValid && account.spent.Cmp(&account.reservationSpent) != 0 {
			out.Mismatches.AccountSpentReservationMismatches++
		}
		if account.heldValid && account.eventHeldValid && account.held.Cmp(&account.eventHeld) != 0 {
			out.Mismatches.AccountHeldEventMismatches++
		}
		if account.spentValid && account.eventSpentValid && account.spent.Cmp(&account.eventSpent) != 0 {
			out.Mismatches.AccountSpentEventMismatches++
		}
	}
	if ctx.Err() != nil {
		return ProcurementBudgetProjectionCheck{}, procurementBudgetContextError(ctx)
	}
	out.Balanced = out.Mismatches == (ProcurementBudgetProjectionMismatches{})
	return out, nil
}
