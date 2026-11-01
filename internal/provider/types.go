// Package provider implements bounded generation and retrieval contracts.
// It never retries a generation or writes financial state.
package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"time"
)

const (
	MaxRequestBytes    = 1 << 20
	MaxEventBytes      = 1 << 20
	MaxResponseBytes   = 8 << 20
	ProtocolChat       = "openai_chat"
	ProtocolResponses  = "openai_responses"
	ProtocolMessages   = "anthropic_messages"
	ProtocolEmbeddings = "openai_embeddings"
	ProtocolRerank     = "jina_rerank"
	ProtocolGemini     = "gemini_generate_content"
)

type Profile struct {
	Protocol                  string   `json:"protocol,omitempty"`
	Version                   string   `json:"version"`
	Model                     string   `json:"model"`
	InputTokenUpperBound      int64    `json:"input_token_upper_bound"`
	ContextLimit              int64    `json:"context_limit"`
	OutputLimit               int64    `json:"output_limit"`
	DefaultOutputLimit        int64    `json:"default_output_limit"`
	OutputLimitField          string   `json:"output_limit_field"`
	SupportsTools             bool     `json:"supports_tools"`
	SupportsParallelToolCalls bool     `json:"supports_parallel_tool_calls"`
	AllowedParameters         []string `json:"allowed_parameters,omitempty"`
	NoConsumptionStatuses     []int    `json:"no_consumption_statuses,omitempty"`
	UsageSemantics            string   `json:"usage_semantics,omitempty"`
}

func (p Profile) ProtocolName() string {
	if p.Protocol == "" {
		return ProtocolChat
	}
	return p.Protocol
}

// InputOnly identifies the explicit protocols whose published token bound
// covers the entire input batch and which never reserve generation output.
func (p Profile) InputOnly() bool {
	return p.ProtocolName() == ProtocolEmbeddings || p.ProtocolName() == ProtocolRerank
}

func (p Profile) Validate() error {
	if p.Version == "" || p.Model == "" || p.InputTokenUpperBound <= 0 || p.ContextLimit <= 0 || p.InputTokenUpperBound > p.ContextLimit {
		return failure("invalid_profile", 503, ClassConfiguration, ConsumptionUnknown)
	}
	protocol := p.ProtocolName()
	if protocol != ProtocolChat && protocol != ProtocolResponses && protocol != ProtocolMessages && protocol != ProtocolGemini && !p.InputOnly() {
		return failure("invalid_profile", 503, ClassConfiguration, ConsumptionUnknown)
	}
	if p.InputOnly() {
		if p.OutputLimit != 0 || p.DefaultOutputLimit != 0 || p.OutputLimitField != "" || p.SupportsTools || p.SupportsParallelToolCalls {
			return failure("invalid_profile", 503, ClassConfiguration, ConsumptionUnknown)
		}
	} else if p.OutputLimit <= 0 || p.OutputLimit > p.ContextLimit || p.DefaultOutputLimit <= 0 || p.DefaultOutputLimit > p.OutputLimit {
		return failure("invalid_profile", 503, ClassConfiguration, ConsumptionUnknown)
	}
	validOutput := p.OutputLimitField == ""
	switch protocol {
	case ProtocolChat:
		validOutput = validOutput || p.OutputLimitField == "max_tokens" || p.OutputLimitField == "max_completion_tokens"
	case ProtocolResponses:
		validOutput = validOutput || p.OutputLimitField == "max_output_tokens"
	case ProtocolMessages:
		validOutput = validOutput || p.OutputLimitField == "max_tokens"
	case ProtocolGemini:
		validOutput = validOutput || p.OutputLimitField == "maxOutputTokens"
		if !ValidGeminiModel(p.Model) || p.SupportsTools || p.SupportsParallelToolCalls || (p.UsageSemantics != "" && p.UsageSemantics != "cumulative") {
			return failure("invalid_profile", 503, ClassConfiguration, ConsumptionUnknown)
		}
	}
	if !validOutput {
		return failure("invalid_profile", 503, ClassConfiguration, ConsumptionUnknown)
	}
	if p.UsageSemantics != "" && p.UsageSemantics != "final" && p.UsageSemantics != "cumulative" {
		return failure("invalid_profile", 503, ClassConfiguration, ConsumptionUnknown)
	}
	known := map[string]bool{"temperature": true, "top_p": true, "seed": true, "stop": true, "presence_penalty": true, "frequency_penalty": true, "tools": true, "tool_choice": true, "parallel_tool_calls": true}
	if protocol == ProtocolResponses {
		known = map[string]bool{"temperature": true, "top_p": true, "tools": true, "tool_choice": true, "parallel_tool_calls": true}
	}
	if protocol == ProtocolMessages {
		known = map[string]bool{"temperature": true, "top_p": true, "top_k": true, "stop_sequences": true, "tools": true, "tool_choice": true}
	}
	if protocol == ProtocolEmbeddings {
		known = map[string]bool{"encoding_format": true, "dimensions": true, "user": true}
	}
	if protocol == ProtocolRerank {
		known = map[string]bool{"top_n": true, "return_documents": true}
	}
	if protocol == ProtocolGemini {
		known = map[string]bool{"temperature": true, "topP": true, "topK": true, "stopSequences": true}
	}
	for _, parameter := range p.AllowedParameters {
		if !known[parameter] {
			return failure("invalid_profile", 503, ClassConfiguration, ConsumptionUnknown)
		}
	}
	for _, status := range p.NoConsumptionStatuses {
		if status != 400 && status != 401 && status != 403 && status != 404 && status != 429 {
			return failure("invalid_profile", 503, ClassConfiguration, ConsumptionUnknown)
		}
	}
	return nil
}

type UsageStatus string

const (
	UsageConfirmed   UsageStatus = "confirmed_final"
	UsageProvisional UsageStatus = "provisional"
	UsageMissing     UsageStatus = "missing"
	UsageConflicting UsageStatus = "conflicting"
)

type Usage struct {
	Status          UsageStatus `json:"status"`
	InputTokens     int64       `json:"input_tokens"`
	OutputTokens    int64       `json:"output_tokens"`
	TotalTokens     int64       `json:"total_tokens"`
	CachedTokens    int64       `json:"cached_tokens"`
	ReasoningTokens int64       `json:"reasoning_tokens"`
}

type FailureClass string

const (
	ClassConfiguration FailureClass = "configuration"
	ClassCaller        FailureClass = "caller"
	ClassNetwork       FailureClass = "network"
	ClassUpstream      FailureClass = "upstream"
	ClassRateLimit     FailureClass = "rate_limit"
	ClassCredential    FailureClass = "credential"
	ClassProtocol      FailureClass = "protocol"
	ClassCancelled     FailureClass = "cancelled"
	ClassTimeout       FailureClass = "timeout"
	ClassModel         FailureClass = "model"
	ClassQuota         FailureClass = "quota"
	ClassPolicy        FailureClass = "policy"
	ClassRejected      FailureClass = "rejected"
)

type Consumption string

const (
	ConsumptionUnknown  Consumption = "unknown"
	ConsumptionNotSent  Consumption = "not_sent"
	ConsumptionRejected Consumption = "rejected_no_consumption"
)

// Error deliberately contains no URL, upstream body, credential or raw cause.
type Error struct {
	Code           string        `json:"code"`
	HTTPStatus     int           `json:"status"`
	Class          FailureClass  `json:"class"`
	Consumption    Consumption   `json:"consumption"`
	RetryAfter     time.Duration `json:"-"`
	UpstreamStatus int           `json:"-"`
}

func (e *Error) Error() string { return e.Code }
func failure(code string, status int, class FailureClass, consumed Consumption) *Error {
	return &Error{Code: code, HTTPStatus: status, Class: class, Consumption: consumed}
}
func SafeToRetry(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Class != ClassCancelled && e.Class != ClassTimeout && (e.Consumption == ConsumptionRejected || (e.Consumption == ConsumptionNotSent && e.Class == ClassNetwork))
}

type NetworkPolicy struct {
	AllowHTTP    bool     `json:"allow_http"`
	AllowedCIDRs []string `json:"allowed_cidrs,omitempty"`
	AllowedPorts []uint16 `json:"allowed_ports,omitempty"`
}
type Endpoint struct {
	URL     string
	APIKey  string
	Model   string
	Network NetworkPolicy
}
type IPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}
type ClientOptions struct {
	Resolver          IPResolver
	RootCAs           *x509.CertPool
	ConnectTimeout    time.Duration
	HeaderTimeout     time.Duration
	FirstEventTimeout time.Duration
	IdleTimeout       time.Duration
	ProgressTimeout   time.Duration
}

type Event struct {
	Kind         string // chunk, usage, heartbeat, done
	Type         string
	Data         []byte // validated normal JSON only; never a provider error body
	Content      bool
	Progress     bool
	FinishReason string
	Usage        Usage
}
