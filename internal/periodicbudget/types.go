// Package periodicbudget defines the lossless management protocol for periodic
// budgets and transaction-bound helpers for their caller-owned PostgreSQL
// transactions. Helpers do not authenticate users or own business transactions.
package periodicbudget

import (
	"encoding/json"
	"strings"
	"time"
)

type BudgetWindowView struct {
	ID          UUID       `json:"id"`
	StartAt     UTC6       `json:"start_at"`
	EndAt       UTC6       `json:"end_at"`
	SpentAmount Numeric100 `json:"spent_amount"`
	HeldAmount  Numeric100 `json:"held_amount"`
}

type BudgetAllocationView struct {
	ID          UUID              `json:"id"`
	SubjectKind string            `json:"subject_kind"`
	SubjectID   UUID              `json:"subject_id"`
	WindowID    *UUID             `json:"window_id"`
	UpperAmount Int64AmountString `json:"upper_amount"`
	HeldAmount  Numeric100        `json:"held_amount"`
	SpentAmount Numeric100        `json:"spent_amount"`
	State       string            `json:"state"`
	CreatedAt   UTC6              `json:"created_at"`
}

type PeriodicBudgetView struct {
	Scope             string             `json:"scope"`
	ScopeID           UUID               `json:"scope_id"`
	Revision          Revision           `json:"revision"`
	PeriodKind        *string            `json:"period_kind"`
	Enabled           bool               `json:"enabled"`
	LimitAmount       *Int64AmountString `json:"limit_amount"`
	State             string             `json:"state"`
	EffectiveFrom     *UTC6              `json:"effective_from"`
	TrackingStartedAt *UTC6              `json:"tracking_started_at"`
	CurrentWindow     *BudgetWindowView  `json:"current_window"`
	CarryHeld         Numeric100         `json:"carry_held"`
	AsOf              UTC6               `json:"as_of"`
}

type PutPeriodicBudget struct {
	OperationID      UUID              `json:"operation_id"`
	ExpectedRevision Revision          `json:"expected_revision"`
	PeriodKind       string            `json:"period_kind"`
	Enabled          bool              `json:"enabled"`
	LimitAmount      Int64AmountString `json:"limit_amount"`
}

type PeriodicBudgetOperationView struct {
	OperationID      UUID             `json:"operation_id"`
	Kind             string           `json:"kind"`
	Scope            string           `json:"scope"`
	Actor            UUID             `json:"actor"`
	Target           string           `json:"target"`
	InputFingerprint LowerHex64       `json:"input_fingerprint"`
	ResourceID       UUID             `json:"resource_id"`
	Revision         PositiveRevision `json:"revision"`
	CreatedAt        UTC6             `json:"created_at"`
}

type CommandReceipt struct {
	Operation PeriodicBudgetOperationView `json:"operation"`
	Result    PeriodicBudgetView          `json:"result"`
}

type ConfigureResult struct {
	Receipt CommandReceipt `json:"receipt"`
	Created bool           `json:"-"`
}

type WindowPage struct {
	Data       []BudgetWindowView `json:"data"`
	NextCursor string             `json:"next_cursor"`
}

type AllocationPage struct {
	Data       []BudgetAllocationView `json:"data"`
	NextCursor string                 `json:"next_cursor"`
}

type SingleResponse[T any] struct {
	Data      T      `json:"data"`
	RequestID string `json:"request_id"`
}

type PageResponse[T any] struct {
	Data       []T    `json:"data"`
	RequestID  string `json:"request_id"`
	NextCursor string `json:"next_cursor"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error     ErrorDetail `json:"error"`
	RequestID string      `json:"request_id"`
}

func (p PageResponse[T]) MarshalJSON() ([]byte, error) {
	type plain PageResponse[T]
	if p.Data == nil {
		p.Data = []T{}
	}
	return json.Marshal(plain(p))
}

func (p WindowPage) MarshalJSON() ([]byte, error) {
	type plain WindowPage
	if p.Data == nil {
		p.Data = []BudgetWindowView{}
	}
	return json.Marshal(plain(p))
}

func (p AllocationPage) MarshalJSON() ([]byte, error) {
	type plain AllocationPage
	if p.Data == nil {
		p.Data = []BudgetAllocationView{}
	}
	return json.Marshal(plain(p))
}

func (v BudgetWindowView) Validate() error {
	if !ValidUUID(v.ID) || !ValidUTC6(v.StartAt) || !ValidUTC6(v.EndAt) || v.StartAt >= v.EndAt ||
		!ValidNumeric100(v.SpentAmount) || !ValidNumeric100(v.HeldAmount) {
		return ErrInvariant
	}
	return nil
}

func (v BudgetAllocationView) Validate() error {
	if !ValidUUID(v.ID) || !ValidUUID(v.SubjectID) || !ValidInt64AmountString(v.UpperAmount) ||
		!ValidNumeric100(v.HeldAmount) || !ValidNumeric100(v.SpentAmount) || !ValidUTC6(v.CreatedAt) {
		return ErrInvariant
	}
	legacy := false
	switch v.SubjectKind {
	case "customer_request", "supplier_attempt":
	case "legacy_customer", "legacy_supplier":
		legacy = true
	default:
		return ErrInvariant
	}
	if legacy && v.WindowID != nil || !legacy && (v.WindowID == nil || !ValidUUID(*v.WindowID)) {
		return ErrInvariant
	}
	switch v.State {
	case "held", "settled", "released":
	case "disputed":
		if v.SubjectKind == "customer_request" || v.SubjectKind == "legacy_customer" {
			return ErrInvariant
		}
	default:
		return ErrInvariant
	}
	return nil
}

func (v PeriodicBudgetView) Validate() error {
	if (v.Scope != "tenant" && v.Scope != "key" && v.Scope != "procurement") || !ValidUUID(v.ScopeID) ||
		!ValidRevision(v.Revision) || !ValidNumeric100(v.CarryHeld) || !ValidUTC6(v.AsOf) {
		return ErrInvariant
	}
	if v.State == "unconfigured" {
		if v.Revision != "0" || v.Enabled || v.PeriodKind != nil || v.LimitAmount != nil ||
			v.EffectiveFrom != nil || v.TrackingStartedAt != nil || v.CurrentWindow != nil || v.CarryHeld != "0" {
			return ErrInvariant
		}
		return nil
	}
	if !ValidPositiveRevision(v.Revision) || v.PeriodKind == nil || !validPeriod(*v.PeriodKind) ||
		v.LimitAmount == nil || !ValidInt64AmountString(*v.LimitAmount) || v.EffectiveFrom == nil ||
		!ValidUTC6(*v.EffectiveFrom) || v.TrackingStartedAt == nil || !ValidUTC6(*v.TrackingStartedAt) {
		return ErrInvariant
	}
	tracking, _ := time.Parse(utc6Layout, *v.TrackingStartedAt)
	if periodEnd(*v.PeriodKind, periodStart(*v.PeriodKind, tracking)).Format(utc6Layout) != *v.EffectiveFrom {
		return ErrInvariant
	}
	state := "disabled"
	if v.Enabled {
		state = "active"
		if v.AsOf < *v.EffectiveFrom {
			state = "scheduled"
		}
	}
	if v.State != state {
		return ErrInvariant
	}
	if w := v.CurrentWindow; w != nil {
		if w.Validate() != nil {
			return ErrInvariant
		}
		at, _ := time.Parse(utc6Layout, v.AsOf)
		start := periodStart(*v.PeriodKind, at)
		if w.StartAt != start.Format(utc6Layout) || w.EndAt != periodEnd(*v.PeriodKind, start).Format(utc6Layout) {
			return ErrInvariant
		}
	}
	return nil
}

func periodStart(kind string, at time.Time) time.Time {
	day := at.Day()
	if kind == "utc_month" {
		day = 1
	}
	return time.Date(at.Year(), at.Month(), day, 0, 0, 0, 0, time.UTC)
}

func periodEnd(kind string, start time.Time) time.Time {
	if kind == "utc_month" {
		return start.AddDate(0, 1, 0)
	}
	return start.AddDate(0, 0, 1)
}

func (v PeriodicBudgetOperationView) Validate() error {
	if !ValidUUID(v.OperationID) || !ValidUUID(v.Actor) || !ValidLowerHex64(v.InputFingerprint) ||
		!ValidUUID(v.ResourceID) || !ValidPositiveRevision(v.Revision) || !ValidUTC6(v.CreatedAt) {
		return ErrInvariant
	}
	if _, _, ok := commandTarget(v.Kind, v.Scope, v.Target); !ok {
		return ErrInvariant
	}
	return nil
}

func (r CommandReceipt) Validate() error {
	if r.Operation.Validate() != nil || r.Result.Validate() != nil || r.Operation.Revision != r.Result.Revision ||
		r.Operation.CreatedAt != r.Result.AsOf {
		return ErrInvariant
	}
	scope, id, ok := commandTarget(r.Operation.Kind, r.Operation.Scope, r.Operation.Target)
	if !ok || scope != r.Result.Scope || id != r.Result.ScopeID {
		return ErrInvariant
	}
	return nil
}

// commandTarget validates only canonical protocol identity, never authorization.
// Current tenant/Key ownership must still be established by the eventual Reader.
func commandTarget(kind, identityScope, target string) (scope, id string, ok bool) {
	var prefix string
	switch kind {
	case "tenant_periodic_budget.configure":
		scope, prefix = "tenant", "/api/platform/tenants/"
		if identityScope != "platform" {
			return "", "", false
		}
	case "key_periodic_budget.configure":
		scope, prefix = "key", "/api/keys/"
		if !ValidUUID(identityScope) {
			return "", "", false
		}
	case "procurement_periodic_budget.configure":
		scope, prefix = "procurement", "/api/platform/procurement-accounts/"
		if identityScope != "platform" {
			return "", "", false
		}
	default:
		return "", "", false
	}
	const suffix = "/periodic-budget"
	if !strings.HasPrefix(target, prefix) || !strings.HasSuffix(target, suffix) {
		return "", "", false
	}
	id = strings.TrimSuffix(strings.TrimPrefix(target, prefix), suffix)
	if !ValidUUID(id) || target != prefix+id+suffix {
		return "", "", false
	}
	return scope, id, true
}
