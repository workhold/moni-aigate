package routing

import (
	"encoding/hex"
	"errors"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

const DynamicAlgorithmVersion = "dynamic-v1"

type ScoringPolicy struct {
	Auto             bool `json:"auto"`
	AllowDegradation bool `json:"allow_degradation"`
}

type StickinessPolicy struct {
	Enabled bool     `json:"enabled"`
	MaxAgeS int64    `json:"max_age_s"`
	BreakOn []string `json:"break_on"`
}

type DynamicPolicy struct {
	CostPolicy
	QualityFloor  string            `json:"quality_floor,omitempty"`
	MinMarginRate *int64            `json:"min_margin_rate,omitempty"`
	Scoring       ScoringPolicy     `json:"scoring"`
	Stickiness    *StickinessPolicy `json:"stickiness,omitempty"`
}

func DefaultDynamicPolicy() DynamicPolicy { return DynamicPolicy{CostPolicy: DefaultCostPolicy()} }

func (p DynamicPolicy) Validate() error {
	if ValidateCostPolicy(p.CostPolicy) != nil || qualityRank(p.QualityFloor) < 0 && p.QualityFloor != "" {
		return ErrInvalidPolicy
	}
	if p.MinMarginRate != nil && (*p.MinMarginRate < 0 || *p.MinMarginRate > 10000) {
		return ErrInvalidPolicy
	}
	if p.Scoring.AllowDegradation && !p.Scoring.Auto {
		return ErrInvalidPolicy
	}
	if p.Stickiness != nil {
		if p.Stickiness.MaxAgeS < 1 || p.Stickiness.MaxAgeS > 86400 {
			return ErrInvalidPolicy
		}
		seen := map[string]bool{}
		for _, reason := range p.Stickiness.BreakOn {
			if reason != "quality" && reason != "cost" && reason != "health" || seen[reason] || !p.Stickiness.Enabled {
				return ErrInvalidPolicy
			}
			seen[reason] = true
		}
	}
	return nil
}

func qualityRank(tier string) int {
	switch tier {
	case "economy":
		return 1
	case "standard":
		return 2
	case "premium":
		return 3
	default:
		return -1
	}
}

type PhysicalCapability struct {
	ContextLimit              int64    `json:"context_limit"`
	InputUpperBound           int64    `json:"input_upper_bound"`
	OutputLimit               int64    `json:"output_limit"`
	SupportsTools             bool     `json:"supports_tools"`
	SupportsParallelToolCalls bool     `json:"supports_parallel_tool_calls"`
	UsageSemantics            string   `json:"usage_semantics"`
	Modalities                []string `json:"modalities"`
	QualityTier               string   `json:"quality_tier"`
	QualityScore              *float64 `json:"quality_score"`
	MaxConcurrency            int      `json:"max_concurrency"`
	SupportedParameters       []string `json:"supported_parameters"`
}

func (c PhysicalCapability) Validate() error {
	if c.ContextLimit <= 0 || c.InputUpperBound <= 0 || c.InputUpperBound > c.ContextLimit || c.OutputLimit < 0 || c.OutputLimit > c.ContextLimit || c.MaxConcurrency <= 0 || c.MaxConcurrency > 1000 || c.UsageSemantics != "final" && c.UsageSemantics != "cumulative" || len(c.Modalities) != 1 || c.Modalities[0] != "text" || qualityRank(c.QualityTier) < 0 || c.QualityScore != nil && (math.IsNaN(*c.QualityScore) || math.IsInf(*c.QualityScore, 0) || *c.QualityScore < 0 || *c.QualityScore > 1) {
		return ErrInvalidPolicy
	}
	seen := map[string]bool{}
	for _, parameter := range c.SupportedParameters {
		if parameter == "" || len(parameter) > 80 || seen[parameter] {
			return ErrInvalidPolicy
		}
		seen[parameter] = true
	}
	return nil
}

type MaxPrice struct {
	Input  *string `json:"input,omitempty"`
	Output *string `json:"output,omitempty"`
}

type RoutePreference struct {
	Order             []string  `json:"order,omitempty"`
	Only              []string  `json:"only,omitempty"`
	Ignore            []string  `json:"ignore,omitempty"`
	AllowFallbacks    bool      `json:"allow_fallbacks"`
	RequireParameters bool      `json:"require_parameters"`
	Sort              string    `json:"sort,omitempty"`
	MaxPrice          *MaxPrice `json:"max_price,omitempty"`
	DataCollection    string    `json:"data_collection,omitempty"`
}

type StickyTarget struct {
	PhysicalModelID string `json:"physical_model_id"`
	ChannelID       string `json:"channel_id"`
}

type RequestShape struct {
	InputUpperBound int64           `json:"input_upper_bound"`
	OutputLimit     int64           `json:"output_limit"`
	RetailAmount    int64           `json:"retail_amount"`
	UsageSemantics  string          `json:"usage_semantics,omitempty"`
	Tools           bool            `json:"tools"`
	ParallelTools   bool            `json:"parallel_tools"`
	Parameters      []string        `json:"parameters"`
	Preference      RoutePreference `json:"preference"`
	SessionID       string          `json:"session_id,omitempty"`
	Sticky          *StickyTarget   `json:"sticky,omitempty"`
	StatsWindowS    int64           `json:"stats_window_s"`
	StatsMinSamples int64           `json:"stats_min_samples"`
}

func (r RequestShape) Validate() error {
	if r.InputUpperBound < 0 || r.OutputLimit < 0 || r.StatsWindowS <= 0 || r.StatsMinSamples < 1 || r.UsageSemantics != "" && r.UsageSemantics != "final" && r.UsageSemantics != "cumulative" {
		return ErrInvalidPolicy
	}
	if r.Preference.Sort != "" && r.Preference.Sort != "price" && r.Preference.Sort != "latency" && r.Preference.Sort != "throughput" || r.Preference.DataCollection != "" && r.Preference.DataCollection != "allow" && r.Preference.DataCollection != "deny" {
		return ErrInvalidPolicy
	}
	for _, values := range [][]string{r.Preference.Order, r.Preference.Only, r.Preference.Ignore} {
		if len(values) > 32 {
			return ErrInvalidPolicy
		}
		seen := map[string]bool{}
		for _, value := range values {
			if value == "" || len(value) > 200 || strings.TrimSpace(value) != value || seen[value] {
				return ErrInvalidPolicy
			}
			seen[value] = true
		}
	}
	if r.Preference.MaxPrice != nil {
		if r.Preference.MaxPrice.Input == nil && r.Preference.MaxPrice.Output == nil {
			return ErrInvalidPolicy
		}
		for _, value := range []*string{r.Preference.MaxPrice.Input, r.Preference.MaxPrice.Output} {
			if value != nil && !canonicalNonnegative(*value) {
				return ErrInvalidPolicy
			}
		}
	}
	if r.SessionID != "" && !ValidSessionID(r.SessionID) {
		return ErrInvalidPolicy
	}
	seen := map[string]bool{}
	for _, parameter := range r.Parameters {
		if parameter == "" || seen[parameter] {
			return ErrInvalidPolicy
		}
		seen[parameter] = true
	}
	return nil
}

func ValidSessionID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.:-", r)) {
			return false
		}
	}
	return true
}

func canonicalNonnegative(value string) bool {
	if value == "" || len(value) > 19 || value[0] == '+' || value[0] == '-' || len(value) > 1 && value[0] == '0' {
		return false
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n >= 0 && strconv.FormatInt(n, 10) == value
}

type StatsSnapshot struct {
	SampleCount       int64  `json:"sample_count"`
	SuccessCount      int64  `json:"success_count"`
	TimeoutCount      int64  `json:"timeout_count"`
	P50FirstContentUS *int64 `json:"p50_first_content_us"`
	P95FirstContentUS *int64 `json:"p95_first_content_us"`
	ThroughputMicroS  *int64 `json:"throughput_micro_s"`
}

type CostRateSnapshot struct {
	Dimension   string `json:"dimension"`
	Unit        string `json:"unit"`
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
}

type CostSnapshot struct {
	VersionID       string             `json:"version_id,omitempty"`
	Basis           string             `json:"basis"`
	SchemaCanonical []byte             `json:"schema_canonical"`
	SchemaDigest    string             `json:"schema_digest"`
	Rates           []CostRateSnapshot `json:"rates"`
	UpperAmount     *string            `json:"upper_amount"`
}

type ScoreSnapshot struct {
	MarginRate     *int64        `json:"margin_rate"`
	Stats          StatsSnapshot `json:"stats"`
	CredentialDraw *string       `json:"credential_draw,omitempty"`
}

type CandidateDecision struct {
	BindingID        string             `json:"binding_id"`
	ChannelID        string             `json:"channel_id"`
	ChannelVersionID string             `json:"channel_version_id"`
	PhysicalModelID  string             `json:"physical_model_id"`
	UpstreamModel    string             `json:"upstream_model"`
	Priority         int                `json:"priority"`
	Weight           uint64             `json:"weight"`
	QualityTier      string             `json:"quality_tier"`
	Capability       PhysicalCapability `json:"capability"`
	FilterReason     string             `json:"filter_reason,omitempty"`
	Reason           string             `json:"reason"`
	Score            ScoreSnapshot      `json:"score"`
	CostSnapshot     CostSnapshot       `json:"cost_snapshot"`
}

type RouteDecision struct {
	ID                      string              `json:"id"`
	TenantID                string              `json:"tenant_id"`
	RequestID               string              `json:"request_id"`
	AttemptID               string              `json:"attempt_id,omitempty"`
	Sequence                int                 `json:"sequence"`
	PolicyVersionID         string              `json:"policy_version_id"`
	SelectedBindingID       string              `json:"selected_binding_id,omitempty"`
	SelectedPhysicalModelID string              `json:"selected_physical_model_id,omitempty"`
	Draw                    string              `json:"draw"`
	Sticky                  string              `json:"sticky,omitempty"`
	Degradation             string              `json:"degradation"`
	AlgorithmVersion        string              `json:"algorithm_version"`
	Policy                  DynamicPolicy       `json:"policy_snapshot"`
	Request                 RequestShape        `json:"request_shape"`
	Candidates              []CandidateDecision `json:"candidates"`
	CandidatesDigest        string              `json:"candidates_digest"`
}

func selectorMatches(c Candidate, selector string) bool {
	return selector == c.ChannelID || selector == c.PhysicalModelID || selector == c.Model
}

func selectorAllowed(c Candidate, selectors []string) bool {
	for _, selector := range selectors {
		if selectorMatches(c, selector) {
			return true
		}
	}
	return false
}

func supplierInputBound(c Candidate) int64 {
	if c.PhysicalModelID != "" {
		return c.Physical.InputUpperBound
	}
	return c.InputUpperBound
}

func supplierBounds(c Candidate) (metering.Bounds, error) {
	return metering.ManagementTokenBounds(c.CostQuote.Schema(), metering.Supplier, supplierInputBound(c), c.OutputLimit)
}

func costSnapshot(c Candidate, request RequestShape) (CostSnapshot, error) {
	snapshot := CostSnapshot{VersionID: c.CostQuote.VersionID(), Basis: c.CostQuote.Basis(), Rates: []CostRateSnapshot{}}
	schemaCanonical, err := c.CostQuote.Schema().CanonicalJSON()
	if err != nil {
		return snapshot, err
	}
	schemaDigest, err := c.CostQuote.Schema().Digest()
	if err != nil {
		return snapshot, err
	}
	snapshot.SchemaCanonical = schemaCanonical
	snapshot.SchemaDigest = hex.EncodeToString(schemaDigest[:])
	if !c.CostQuote.Known() {
		return snapshot, nil
	}
	rates, err := c.CostQuote.Rates()
	if err != nil {
		return snapshot, err
	}
	entries, err := rates.Entries()
	if err != nil {
		return snapshot, err
	}
	for _, entry := range entries {
		snapshot.Rates = append(snapshot.Rates, CostRateSnapshot{Dimension: entry.Dimension, Unit: entry.Unit, Numerator: strconv.FormatInt(entry.Numerator, 10), Denominator: strconv.FormatInt(entry.Denominator, 10)})
	}
	bounds, err := supplierBounds(c)
	if err != nil {
		return snapshot, err
	}
	amount, err := metering.ReservationAmount(bounds, rates)
	if errors.Is(err, metering.ErrAmountOverflow) {
		// Exact cost ordering still supports larger intermediates. A route with
		// a margin floor rejects this unrepresentable conservative upper amount.
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	text := strconv.FormatInt(amount, 10)
	snapshot.UpperAmount = &text
	_ = request
	return snapshot, nil
}

func marginRate(cost CostSnapshot, retail int64) *int64 {
	if cost.UpperAmount == nil {
		return nil
	}
	upper, ok := new(big.Int).SetString(*cost.UpperAmount, 10)
	if !ok {
		return nil
	}
	margin := new(big.Int).Sub(big.NewInt(retail), upper)
	margin.Mul(margin, big.NewInt(10000))
	if retail > 0 {
		margin.Quo(margin, big.NewInt(retail))
	} else if upper.Sign() != 0 {
		margin.SetInt64(-10000)
	} else {
		margin.SetInt64(0)
	}
	if !margin.IsInt64() {
		return nil
	}
	value := margin.Int64()
	return &value
}

func staticCandidateDecision(c Candidate, policy DynamicPolicy, request RequestShape) (CandidateDecision, error) {
	d := CandidateDecision{BindingID: c.BindingID, ChannelID: c.ChannelID, ChannelVersionID: c.VersionID, PhysicalModelID: c.PhysicalModelID, UpstreamModel: c.Model, Priority: c.Priority, Weight: c.Weight, QualityTier: c.Physical.QualityTier, Capability: c.Physical, Score: ScoreSnapshot{Stats: c.Stats}}
	cost, err := costSnapshot(c, request)
	if err != nil {
		return d, err
	}
	d.CostSnapshot = cost
	d.Score.MarginRate = marginRate(cost, c.RetailAmount)
	if len(request.Preference.Only) > 0 && !selectorAllowed(c, request.Preference.Only) || len(request.Preference.Ignore) > 0 && selectorAllowed(c, request.Preference.Ignore) {
		d.FilterReason = "not_authorized"
		return d, nil
	}
	if c.ExclusionReason == "region_forbidden" {
		d.FilterReason = c.ExclusionReason
		return d, nil
	}
	if c.PhysicalModelID != "" {
		if err := c.Physical.Validate(); err != nil {
			d.FilterReason = "capability_missing"
			return d, nil
		}
		switch {
		case request.UsageSemantics != "" && c.Physical.UsageSemantics != request.UsageSemantics:
			d.FilterReason = "capability_missing"
		case c.Physical.ContextLimit < c.InputUpperBound || c.Physical.InputUpperBound < c.InputUpperBound:
			d.FilterReason = "context_too_small"
		case c.Physical.OutputLimit < c.OutputLimit:
			d.FilterReason = "output_limit_too_small"
		case request.Tools && !c.Physical.SupportsTools || request.ParallelTools && !c.Physical.SupportsParallelToolCalls:
			d.FilterReason = "tools_unsupported"
		case request.Preference.RequireParameters && !parametersSupported(c.Physical.SupportedParameters, request.Parameters):
			d.FilterReason = "capability_missing"
		case qualityRank(c.Physical.QualityTier) < qualityRank(policy.QualityFloor):
			d.FilterReason = "quality_below_floor"
		}
		if d.FilterReason != "" {
			return d, nil
		}
	}
	if !c.Enabled {
		d.FilterReason = exclusionReason(c)
		return d, nil
	}
	return d, nil
}

func parametersSupported(supported, requested []string) bool {
	set := map[string]bool{}
	for _, value := range supported {
		set[value] = true
	}
	for _, value := range requested {
		if !set[value] {
			return false
		}
	}
	return true
}

func maxPriceExceeded(snapshot CostSnapshot, maximum *MaxPrice) (bool, error) {
	if maximum == nil || snapshot.Basis == "unknown" {
		return false, nil
	}
	for _, rate := range snapshot.Rates {
		var limit *string
		switch rate.Dimension {
		case "text_input_token":
			limit = maximum.Input
		case "text_output_token":
			limit = maximum.Output
		}
		if limit == nil {
			continue
		}
		numerator, nok := new(big.Int).SetString(rate.Numerator, 10)
		denominator, dok := new(big.Int).SetString(rate.Denominator, 10)
		max, mok := new(big.Int).SetString(*limit, 10)
		if !nok || !dok || !mok || denominator.Sign() <= 0 {
			return false, ErrInvalidPolicy
		}
		left := new(big.Int).Mul(numerator, big.NewInt(1_000_000))
		right := new(big.Int).Mul(max, denominator)
		if left.Cmp(right) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func marginBelowFloor(snapshot CostSnapshot, retail int64, floor *int64) bool {
	if floor == nil {
		return false
	}
	if snapshot.UpperAmount == nil {
		return true
	}
	upper, ok := new(big.Int).SetString(*snapshot.UpperAmount, 10)
	if !ok || retail < 0 {
		return true
	}
	if retail == 0 {
		return upper.Sign() != 0 || *floor > 0
	}
	left := new(big.Int).Mul(new(big.Int).Sub(big.NewInt(retail), upper), big.NewInt(10000))
	right := new(big.Int).Mul(big.NewInt(retail), big.NewInt(*floor))
	return left.Cmp(right) < 0
}

func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}

func cloneDecision(in *RouteDecision) *RouteDecision {
	if in == nil {
		return nil
	}
	out := *in
	out.Candidates = cloneSlice(in.Candidates)
	for i := range out.Candidates {
		out.Candidates[i].Capability.Modalities = cloneSlice(in.Candidates[i].Capability.Modalities)
		out.Candidates[i].Capability.SupportedParameters = cloneSlice(in.Candidates[i].Capability.SupportedParameters)
		out.Candidates[i].CostSnapshot.Rates = cloneSlice(in.Candidates[i].CostSnapshot.Rates)
	}
	out.Request.Parameters = cloneSlice(in.Request.Parameters)
	out.Request.Preference.Order = cloneSlice(in.Request.Preference.Order)
	out.Request.Preference.Only = cloneSlice(in.Request.Preference.Only)
	out.Request.Preference.Ignore = cloneSlice(in.Request.Preference.Ignore)
	if in.Policy.Stickiness != nil {
		sticky := *in.Policy.Stickiness
		sticky.BreakOn = cloneSlice(in.Policy.Stickiness.BreakOn)
		out.Policy.Stickiness = &sticky
	}
	return &out
}

func normalizeRequestShape(shape RequestShape) RequestShape {
	shape.Parameters = append([]string(nil), shape.Parameters...)
	sort.Strings(shape.Parameters)
	if shape.StatsWindowS == 0 {
		shape.StatsWindowS = 900
	}
	if shape.StatsMinSamples == 0 {
		shape.StatsMinSamples = 50
	}
	return shape
}
