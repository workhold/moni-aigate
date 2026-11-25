package economics

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"

	"github.com/moni-ai/moni-aigate/internal/strictjson"
)

var ErrInvalidSnapshot = errors.New("invalid economics snapshot")

// EconomicsSnapshotInput is the construction/persistence wire representation.
// NewEconomicsSnapshot validates and defensively copies every mutable field.
type EconomicsSnapshotInput struct {
	TenantID                 string          `json:"tenant_id"`
	RequestID                string          `json:"request_id"`
	ReservationID            string          `json:"reservation_id"`
	OfferVersionID           string          `json:"offer_version_id"`
	EconomicsVersionID       string          `json:"economics_version_id"`
	ExecutionPolicyVersionID string          `json:"execution_policy_version_id"`
	TaskPolicyVersionID      string          `json:"task_policy_version_id"`
	QualityPolicyVersionID   string          `json:"quality_policy_version_id"`
	TaskClass                TaskClass       `json:"task_class"`
	TaskSource               TaskSource      `json:"task_source"`
	TaskCandidates           []string        `json:"task_candidates"`
	SettlementMode           string          `json:"settlement_mode"`
	MaxOuterAttempts         int64           `json:"max_outer_attempts"`
	ReservationCoinAmount    int64           `json:"reservation_coin_amount"`
	PaymentBasisMicro        MoneyVector     `json:"payment_basis_micro"`
	PaymentBasisComplete     bool            `json:"payment_basis_complete"`
	FxPolicyVersionID        string          `json:"fx_policy_version_id"`
	ValuationCurrency        Currency        `json:"valuation_currency"`
	FxSnapshot               FxPolicy        `json:"fx_snapshot"`
	MinMarginRate            int64           `json:"min_margin_rate"`
	MaxCumulativeCostMicro   *MoneyVector    `json:"max_cumulative_cost_micro"`
	ExecutionPolicy          ExecutionPolicy `json:"execution_policy"`
	CreatedAt                time.Time       `json:"created_at"`
}

// EconomicsSnapshot is immutable after construction or JSON decoding. All
// maps and slices returned by accessors are defensive copies.
type EconomicsSnapshot struct {
	input EconomicsSnapshotInput
}

func NewEconomicsSnapshot(input EconomicsSnapshotInput) (EconomicsSnapshot, error) {
	input = cloneSnapshotInput(input)
	if err := validateSnapshotInput(input); err != nil {
		return EconomicsSnapshot{}, err
	}
	// Rate order is not semantic; canonicalize it for stable snapshot digests.
	sort.Slice(input.FxSnapshot.Rates, func(i, j int) bool {
		a, b := input.FxSnapshot.Rates[i], input.FxSnapshot.Rates[j]
		if a.SourceCurrency != b.SourceCurrency {
			return a.SourceCurrency < b.SourceCurrency
		}
		return a.TargetCurrency < b.TargetCurrency
	})
	return EconomicsSnapshot{input: cloneSnapshotInput(input)}, nil
}

func validateSnapshotInput(input EconomicsSnapshotInput) error {
	ids := [...]string{
		input.TenantID, input.RequestID, input.ReservationID, input.OfferVersionID,
		input.EconomicsVersionID, input.ExecutionPolicyVersionID, input.TaskPolicyVersionID,
		input.QualityPolicyVersionID, input.FxPolicyVersionID, input.ExecutionPolicy.ID,
		input.FxSnapshot.ID,
	}
	for _, id := range ids {
		if !canonicalUUID(id) {
			return ErrInvalidSnapshot
		}
	}
	if input.TaskCandidates == nil || !sort.StringsAreSorted(input.TaskCandidates) {
		return ErrInvalidSnapshot
	}
	for i, id := range input.TaskCandidates {
		if !canonicalUUID(id) || i > 0 && input.TaskCandidates[i-1] == id {
			return ErrInvalidSnapshot
		}
	}
	if input.ExecutionPolicyVersionID != input.ExecutionPolicy.ID ||
		input.FxPolicyVersionID != input.FxSnapshot.ID ||
		input.TaskClass.Validate() != nil ||
		(input.TaskSource != TaskSourceSKU && input.TaskSource != TaskSourceHint) ||
		input.ExecutionPolicy.Validate() != nil ||
		input.SettlementMode != input.ExecutionPolicy.SettlementMode ||
		input.MaxOuterAttempts != input.ExecutionPolicy.MaxOuterAttempts ||
		input.ReservationCoinAmount < 0 || input.CreatedAt.IsZero() ||
		input.ValuationCurrency.Validate() != nil ||
		input.ValuationCurrency != input.FxSnapshot.ValuationCurrency ||
		input.FxSnapshot.Validate() != nil || input.MinMarginRate < 0 || input.MinMarginRate > 10000 {
		return ErrInvalidSnapshot
	}
	if input.PaymentBasisMicro == nil || input.PaymentBasisMicro.Validate() != nil {
		return ErrInvalidSnapshot
	}
	if input.MaxCumulativeCostMicro != nil {
		policy := EconomicsPolicy{
			ID: input.EconomicsVersionID, ValuationCurrency: input.ValuationCurrency,
			FxPolicyVersionID: input.FxPolicyVersionID, MinMarginRate: input.MinMarginRate,
			MaxCumulativeCostMicro: input.MaxCumulativeCostMicro,
			CreatedBy:              input.FxSnapshot.CreatedBy, CreatedAt: input.CreatedAt,
		}
		if policy.Validate() != nil {
			return ErrInvalidSnapshot
		}
	}
	return nil
}

func cloneSnapshotInput(input EconomicsSnapshotInput) EconomicsSnapshotInput {
	if input.TaskCandidates != nil {
		input.TaskCandidates = append([]string{}, input.TaskCandidates...)
	}
	input.PaymentBasisMicro = input.PaymentBasisMicro.Clone()
	if input.MaxCumulativeCostMicro != nil {
		cloned := input.MaxCumulativeCostMicro.Clone()
		input.MaxCumulativeCostMicro = &cloned
	}
	input.FxSnapshot = input.FxSnapshot.Clone()
	return input
}

func (s EconomicsSnapshot) TenantID() string            { return s.input.TenantID }
func (s EconomicsSnapshot) RequestID() string           { return s.input.RequestID }
func (s EconomicsSnapshot) ReservationID() string       { return s.input.ReservationID }
func (s EconomicsSnapshot) OfferVersionID() string      { return s.input.OfferVersionID }
func (s EconomicsSnapshot) EconomicsVersionID() string  { return s.input.EconomicsVersionID }
func (s EconomicsSnapshot) TaskPolicyVersionID() string { return s.input.TaskPolicyVersionID }
func (s EconomicsSnapshot) QualityPolicyVersionID() string {
	return s.input.QualityPolicyVersionID
}
func (s EconomicsSnapshot) TaskClass() TaskClass   { return s.input.TaskClass }
func (s EconomicsSnapshot) TaskSource() TaskSource { return s.input.TaskSource }
func (s EconomicsSnapshot) TaskCandidates() []string {
	return append([]string{}, s.input.TaskCandidates...)
}
func (s EconomicsSnapshot) SettlementMode() string           { return s.input.SettlementMode }
func (s EconomicsSnapshot) MaxOuterAttempts() int64          { return s.input.MaxOuterAttempts }
func (s EconomicsSnapshot) ReservationCoinAmount() int64     { return s.input.ReservationCoinAmount }
func (s EconomicsSnapshot) PaymentBasisComplete() bool       { return s.input.PaymentBasisComplete }
func (s EconomicsSnapshot) FxPolicyVersionID() string        { return s.input.FxPolicyVersionID }
func (s EconomicsSnapshot) ValuationCurrency() Currency      { return s.input.ValuationCurrency }
func (s EconomicsSnapshot) MinMarginRate() int64             { return s.input.MinMarginRate }
func (s EconomicsSnapshot) CreatedAt() time.Time             { return s.input.CreatedAt }
func (s EconomicsSnapshot) ExecutionPolicy() ExecutionPolicy { return s.input.ExecutionPolicy }
func (s EconomicsSnapshot) FxSnapshot() FxPolicy             { return s.input.FxSnapshot.Clone() }
func (s EconomicsSnapshot) PaymentBasisMicro() MoneyVector   { return s.input.PaymentBasisMicro.Clone() }
func (s EconomicsSnapshot) MaxCumulativeCostMicro() (MoneyVector, bool) {
	if s.input.MaxCumulativeCostMicro == nil {
		return nil, false
	}
	return s.input.MaxCumulativeCostMicro.Clone(), true
}

// DTO returns a fully detached persistence/route representation.
func (s EconomicsSnapshot) DTO() EconomicsSnapshotInput {
	return cloneSnapshotInput(s.input)
}

func (s EconomicsSnapshot) MarshalJSON() ([]byte, error) {
	if err := validateSnapshotInput(s.input); err != nil {
		return nil, err
	}
	return json.Marshal(s.input)
}

func (s *EconomicsSnapshot) UnmarshalJSON(b []byte) error {
	if s == nil {
		return ErrInvalidSnapshot
	}
	parsed, err := strictjson.Parse(b, 65536)
	root, ok := parsed.(map[string]any)
	if err != nil || !ok || len(root) != 23 {
		return ErrInvalidSnapshot
	}
	for key, value := range root {
		if key != "max_cumulative_cost_micro" && value == nil {
			return ErrInvalidSnapshot
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	var input EconomicsSnapshotInput
	if err := decoder.Decode(&input); err != nil {
		return ErrInvalidSnapshot
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrInvalidSnapshot
	}
	snapshot, err := NewEconomicsSnapshot(input)
	if err != nil {
		return err
	}
	*s = snapshot
	return nil
}

func (s EconomicsSnapshot) CanonicalJSON() ([]byte, error) { return s.MarshalJSON() }

func (s EconomicsSnapshot) Digest() ([sha256.Size]byte, error) {
	canonical, err := s.CanonicalJSON()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}
