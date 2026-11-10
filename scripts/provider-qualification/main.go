package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
)

const (
	defaultInputLimit  = int64(4096)
	defaultContext     = int64(8192)
	defaultOutputLimit = int64(128)
)

type result struct {
	Status          string                `json:"status"`
	Protocol        string                `json:"protocol"`
	Model           string                `json:"model"`
	HTTPStatus      int                   `json:"http_status,omitempty"`
	FinishReason    string                `json:"finish_reason,omitempty"`
	ProtocolOutcome string                `json:"protocol_outcome,omitempty"`
	TransportEnd    string                `json:"transport_end_reason,omitempty"`
	Usage           provider.Usage        `json:"usage,omitempty"`
	ResponseSHA256  string                `json:"response_sha256,omitempty"`
	ErrorCode       string                `json:"error_code,omitempty"`
	ErrorClass      provider.FailureClass `json:"error_class,omitempty"`
	Consumption     provider.Consumption  `json:"consumption,omitempty"`
	ErrorMessage    string                `json:"error_message,omitempty"`
}

func main() {
	baseURL := flag.String("base-url", getenv("PROVIDER_QUALIFICATION_BASE_URL", ""), "OpenAI-compatible base URL, for example https://gateway.example/v1")
	apiKey := flag.String("api-key", getenv("PROVIDER_QUALIFICATION_API_KEY", ""), "provider API key, preferably supplied through PROVIDER_QUALIFICATION_API_KEY")
	model := flag.String("model", getenv("PROVIDER_QUALIFICATION_MODEL", ""), "provider model ID")
	maxOutput := flag.Int64("max-output-tokens", getenvInt64("PROVIDER_QUALIFICATION_MAX_OUTPUT_TOKENS", 8), "small output bound for the qualification request")
	timeout := flag.Duration("timeout", getenvDuration("PROVIDER_QUALIFICATION_TIMEOUT", 90*time.Second), "whole request deadline")
	flag.Parse()

	if err := validateInputs(*baseURL, *apiKey, *model, *maxOutput, *timeout); err != nil {
		printResult(result{Status: "invalid_configuration", ErrorMessage: err.Error()})
		os.Exit(2)
	}

	profile := provider.Profile{
		Protocol:             provider.ProtocolChat,
		Version:              "live-qualification-openai-chat-v1",
		Model:                *model,
		InputTokenUpperBound: defaultInputLimit,
		ContextLimit:         defaultContext,
		OutputLimit:          defaultOutputLimit,
		DefaultOutputLimit:   *maxOutput,
		OutputLimitField:     "max_tokens",
		UsageSemantics:       "final",
	}
	requestBody := []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Reply with exactly OK."}],"max_tokens":%d,"stream":false}`, *model, *maxOutput))
	request, err := provider.ParseChat(requestBody, profile)
	if err != nil {
		printResult(result{Status: "invalid_request", Protocol: profile.ProtocolName(), Model: *model, ErrorMessage: safeError(err)})
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client := provider.NewClient(provider.DefaultTimeoutConfig().ClientOptions())
	response, err := client.Do(ctx, provider.Endpoint{
		URL:     chatEndpoint(*baseURL),
		APIKey:  *apiKey,
		Model:   *model,
		Network: provider.NetworkPolicy{AllowHTTP: false},
	}, request)
	if err != nil {
		printResult(errorResult(profile.ProtocolName(), *model, err))
		os.Exit(1)
	}
	decoded, err := response.DecodeJSON()
	if err != nil {
		printResult(errorResult(profile.ProtocolName(), *model, err))
		os.Exit(1)
	}
	if response.Usage.Status != provider.UsageConfirmed {
		printResult(result{
			Status:          "usage_unconfirmed",
			Protocol:        profile.ProtocolName(),
			Model:           *model,
			FinishReason:    response.FinishReason,
			ProtocolOutcome: response.ProtocolOutcome,
			TransportEnd:    response.TransportEndReason,
			Usage:           response.Usage,
			ResponseSHA256:  digest(decoded),
			ErrorMessage:    "provider response did not contain confirmed final usage",
		})
		os.Exit(1)
	}
	printResult(result{
		Status:          "passed",
		Protocol:        profile.ProtocolName(),
		Model:           *model,
		FinishReason:    response.FinishReason,
		ProtocolOutcome: response.ProtocolOutcome,
		TransportEnd:    response.TransportEndReason,
		Usage:           response.Usage,
		ResponseSHA256:  digest(decoded),
	})
}

func validateInputs(baseURL, apiKey, model string, maxOutput int64, timeout time.Duration) error {
	if strings.TrimSpace(apiKey) == "" {
		return errors.New("PROVIDER_QUALIFICATION_API_KEY is required")
	}
	if strings.TrimSpace(model) == "" {
		return errors.New("PROVIDER_QUALIFICATION_MODEL is required")
	}
	if maxOutput <= 0 || maxOutput > defaultOutputLimit {
		return fmt.Errorf("max output tokens must be between 1 and %d", defaultOutputLimit)
	}
	if timeout <= 0 || timeout > 10*time.Minute {
		return errors.New("timeout must be between 1s and 10m")
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("base URL must be an HTTPS URL without query or fragment")
	}
	return nil
}

func chatEndpoint(base string) string {
	return strings.TrimRight(base, "/") + "/chat/completions"
}

func errorResult(protocol, model string, err error) result {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return result{
			Status:      "failed",
			Protocol:    protocol,
			Model:       model,
			HTTPStatus:  providerErr.HTTPStatus,
			ErrorCode:   providerErr.Code,
			ErrorClass:  providerErr.Class,
			Consumption: providerErr.Consumption,
		}
	}
	return result{Status: "failed", Protocol: protocol, Model: model, ErrorMessage: safeError(err)}
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	if providerErr, ok := err.(*provider.Error); ok {
		return providerErr.Code
	}
	return "qualification_failed"
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func printResult(value result) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvInt64(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return -1
	}
	return parsed
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return -1
	}
	return parsed
}
