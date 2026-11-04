// Package reporting reads current operating facts in one bounded read-only
// snapshot. Reports are informational and never authorize model or money work.
package reporting

import (
	"errors"
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

func (f Filter) Validate() error {
	if f.From.IsZero() || f.To.IsZero() || f.From.Year() < 1 || f.From.Year() > 9999 || f.To.Year() < 1 || f.To.Year() > 9999 ||
		!f.From.Before(f.To) || f.To.Sub(f.From) > 31*24*time.Hour {
		return ErrInvalidFilter
	}
	for _, id := range []string{f.TenantID, f.ModelID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return ErrInvalidFilter
			}
		}
	}
	if f.Protocol != "" && !reportProtocol(f.Protocol) || f.ExecutionKind != "" && f.ExecutionKind != "synchronous" {
		return ErrInvalidFilter
	}
	return nil
}

func reportProtocol(value string) bool {
	switch value {
	case "openai_chat", "openai_responses", "anthropic_messages", "openai_embeddings", "jina_rerank", "gemini_generate_content":
		return true
	}
	return false
}

type Totals struct {
	RequestCount         int64   `json:"request_count"`
	UnsettledRequests    int64   `json:"unsettled_requests"`
	AttemptCount         int64   `json:"attempt_count"`
	UnknownProviderCosts int64   `json:"unknown_provider_costs"`
	Charged              string  `json:"charged"`
	Refunded             string  `json:"refunded"`
	NetRevenue           string  `json:"net_revenue"`
	KnownProviderCost    string  `json:"known_provider_cost"`
	GrossMargin          *string `json:"gross_margin"`
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
	Protocol       string    `json:"protocol"`
	ExecutionKind  string    `json:"execution_kind"`
	ProfileVersion string    `json:"profile_version"`
	SchemaID       string    `json:"schema_id"`
	SchemaDigest   string    `json:"schema_digest"`
	VariantKey     string    `json:"variant_key"`
	CreatedAt      time.Time `json:"created_at"`
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
}
