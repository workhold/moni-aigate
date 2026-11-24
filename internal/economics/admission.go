package economics

import (
	"errors"
	"math/big"
	"time"
)

var (
	ErrIncompletePaymentBasis = errors.New("payment basis is incomplete")
	ErrUnknownCost            = errors.New("cost is unknown")
	ErrMarginAdmission        = errors.New("minimum margin admission failed")
	ErrCumulativeCostLimit    = errors.New("cumulative cost limit exceeded")
	ErrZeroPrice              = errors.New("zero-price request has nonzero cost or margin")
)

// CostBasis distinguishes a known empty vector from unknown cost evidence.
type CostBasis struct {
	Vector   MoneyVector `json:"vector,omitempty"`
	Complete bool        `json:"complete"`
}

type AdmissionRequest struct {
	Snapshot      EconomicsSnapshot `json:"snapshot"`
	PriorCost     CostBasis         `json:"prior_cost"`
	CandidateCost CostBasis         `json:"candidate_cost"`
}

// AdmissionDecision reports conservative values in the frozen valuation
// currency. Passing admission is not a realized-margin assertion.
type AdmissionDecision struct {
	Currency            Currency `json:"currency"`
	IncomeLower         Money    `json:"income_lower"`
	PriorCostUpper      Money    `json:"prior_cost_upper"`
	CandidateCostUpper  Money    `json:"candidate_cost_upper"`
	CumulativeCostUpper Money    `json:"cumulative_cost_upper"`
}

// EvaluateAdmission applies (B-A-U)*10000 >= B*min_margin_rate to all earlier
// and registered inner costs (A) plus the current candidate upper bound (U).
// All arithmetic and comparisons are exact big.Int operations.
func EvaluateAdmission(request AdmissionRequest, now time.Time) (AdmissionDecision, error) {
	snapshot := request.Snapshot.input
	if err := validateSnapshotInput(snapshot); err != nil {
		return AdmissionDecision{}, err
	}
	if !snapshot.PaymentBasisComplete {
		return AdmissionDecision{}, ErrIncompletePaymentBasis
	}
	if !request.PriorCost.Complete || !request.CandidateCost.Complete {
		return AdmissionDecision{}, ErrUnknownCost
	}
	if request.PriorCost.Vector == nil || request.CandidateCost.Vector == nil {
		return AdmissionDecision{}, ErrUnknownCost
	}
	fx := snapshot.FxSnapshot
	income, err := fx.IncomeLowerVector(snapshot.PaymentBasisMicro, now)
	if err != nil {
		return AdmissionDecision{}, err
	}
	prior, err := fx.CostUpperVector(request.PriorCost.Vector, now)
	if err != nil {
		return AdmissionDecision{}, err
	}
	candidate, err := fx.CostUpperVector(request.CandidateCost.Vector, now)
	if err != nil {
		return AdmissionDecision{}, err
	}
	cumulativeBig := new(big.Int).Add(big.NewInt(int64(prior)), big.NewInt(int64(candidate)))
	if !cumulativeBig.IsInt64() {
		return AdmissionDecision{}, ErrOverflow
	}
	decision := AdmissionDecision{
		Currency: snapshot.ValuationCurrency, IncomeLower: income,
		PriorCostUpper: prior, CandidateCostUpper: candidate,
		CumulativeCostUpper: Money(cumulativeBig.Int64()),
	}
	if snapshot.MaxCumulativeCostMicro != nil {
		// A converted ceiling is rounded down so FX never enlarges the frozen cap.
		limit, err := fx.IncomeLowerVector(*snapshot.MaxCumulativeCostMicro, now)
		if err != nil {
			return AdmissionDecision{}, err
		}
		if cumulativeBig.Cmp(big.NewInt(int64(limit))) > 0 {
			return AdmissionDecision{}, ErrCumulativeCostLimit
		}
	}
	if income == 0 {
		if snapshot.MinMarginRate != 0 || decision.CumulativeCostUpper != 0 {
			return AdmissionDecision{}, ErrZeroPrice
		}
		return decision, nil
	}
	left := new(big.Int).Sub(big.NewInt(int64(income)), cumulativeBig)
	left.Mul(left, big.NewInt(10000))
	right := new(big.Int).Mul(big.NewInt(int64(income)), big.NewInt(snapshot.MinMarginRate))
	if left.Cmp(right) < 0 {
		return AdmissionDecision{}, ErrMarginAdmission
	}
	return decision, nil
}
