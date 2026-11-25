// Package reporting reads current operating facts in one bounded read-only
// snapshot. Reports are informational and never authorize model or money work.
package reporting

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const MaxRequests = 5000

var (
	ErrInvalidFilter = errors.New("invalid_report_filter")
	ErrRangeTooLarge = errors.New("report_range_too_large")
)

type Filter struct {
	From, To                time.Time
	TenantID, ModelID       string
	Protocol, ExecutionKind string
}

var publicProtocols = map[string]struct{}{
	"openai_chat": {}, "openai_responses": {}, "anthropic_messages": {},
	"openai_embeddings": {}, "jina_rerank": {}, "gemini_generate_content": {},
	"image_generation": {}, "tts": {}, "video_generation": {}, "realtime_turn": {},
}

var currentMediaModelKeys = map[string]struct{}{
	"mock-image-generation": {},
	"mock-tts":              {},
	"mock-video-generation": {},
	"mock-realtime":         {},
}

func validProtocol(value string) bool {
	_, ok := publicProtocols[value]
	return ok
}

func validModelSelector(value string) bool {
	if _, err := uuid.Parse(value); err == nil {
		return true
	}
	_, ok := currentMediaModelKeys[value]
	return ok && !strings.ContainsAny(value, " \t\r\n")
}

func (f Filter) Validate() error {
	if f.From.IsZero() || f.To.IsZero() || f.From.Year() < 1 || f.From.Year() > 9999 || f.To.Year() < 1 || f.To.Year() > 9999 ||
		!f.From.Before(f.To) || f.To.Sub(f.From) > 31*24*time.Hour {
		return ErrInvalidFilter
	}
	if f.TenantID != "" {
		if _, err := uuid.Parse(f.TenantID); err != nil {
			return ErrInvalidFilter
		}
	}
	if f.ModelID != "" && !validModelSelector(f.ModelID) {
		return ErrInvalidFilter
	}
	if f.Protocol != "" && !validProtocol(f.Protocol) {
		return ErrInvalidFilter
	}
	if f.ExecutionKind != "" && f.ExecutionKind != "synchronous" && f.ExecutionKind != "asynchronous" {
		return ErrInvalidFilter
	}
	return nil
}

type Totals struct {
	RequestCount         int64             `json:"request_count"`
	UnsettledRequests    int64             `json:"unsettled_requests"`
	AttemptCount         int64             `json:"attempt_count"`
	UnknownProviderCosts int64             `json:"unknown_provider_costs"`
	Charged              string            `json:"charged"`
	Refunded             string            `json:"refunded"`
	NetRevenue           string            `json:"net_revenue"`
	KnownProviderCost    string            `json:"known_provider_cost"`
	PaymentBasis         map[string]string `json:"payment_basis"`
	CashRefunded         map[string]string `json:"cash_refunded"`
	GrossMargin          *string           `json:"gross_margin"`

	// P0 keeps the legacy scalar fields above while exposing the exact original
	// currency vector and the evidence state of both contribution margins.
	ProviderCost             map[string]string `json:"provider_cost"`
	ProvisionalGrossMargin   *string           `json:"provisional_gross_margin"`
	BillConfirmedGrossMargin *string           `json:"bill_confirmed_gross_margin"`
	GrossMarginCurrency      *string           `json:"gross_margin_currency"`
	MarginValuation          *string           `json:"margin_valuation"`
	CostAsOf                 *time.Time        `json:"cost_as_of"`
	CostRevision             string            `json:"cost_revision"`
	FxPolicyVersions         []string          `json:"fx_policy_versions"`
}

type ModelReport struct {
	ModelID   string `json:"model_id"`
	ModelName string `json:"model_name"`
	Totals
}

type ChannelReport struct {
	ChannelID            string `json:"channel_id"`
	ChannelName          string `json:"channel_name"`
	RequestCount         int64  `json:"request_count"`
	AttemptCount         int64  `json:"attempt_count"`
	UnknownProviderCosts int64  `json:"unknown_provider_costs"`
	KnownProviderCost    string `json:"known_provider_cost"`
}

type RequestRow struct {
	RequestID      string    `json:"request_id"`
	TenantID       string    `json:"tenant_id"`
	TenantName     string    `json:"tenant_name"`
	ModelID        string    `json:"model_id"`
	ModelName      string    `json:"model_name"`
	Status         string    `json:"status"`
	BillingStatus  string    `json:"billing_status"`
	CreatedAt      time.Time `json:"created_at"`
	Protocol       string    `json:"protocol"`
	ExecutionKind  string    `json:"execution_kind"`
	ProfileVersion string    `json:"profile_version"`
	SchemaID       string    `json:"schema_id"`
	SchemaDigest   string    `json:"schema_digest"`
	VariantKey     string    `json:"variant_key"`
	Totals
	OfferVersionID string `json:"offer_version_id"`
	TaskClass      string `json:"task_class"`
}

type OfferReport struct {
	OfferVersionID string `json:"offer_version_id"`
	TaskClass      string `json:"task_class"`
	Totals
}

type Report struct {
	From       time.Time       `json:"from"`
	To         time.Time       `json:"to"`
	AsOf       time.Time       `json:"as_of"`
	Currency   string          `json:"currency"`
	AmountUnit string          `json:"amount_unit"`
	Scope      string          `json:"scope"`
	Summary    Totals          `json:"summary"`
	Models     []ModelReport   `json:"models"`
	Channels   []ChannelReport `json:"channels"`
	Requests   []RequestRow    `json:"requests"`
	// OfferReports contains only requests bound to an immutable P0 offer. Legacy
	// and media requests remain visible in the other dimensions.
	OfferReports []OfferReport `json:"offer_reports"`
}
