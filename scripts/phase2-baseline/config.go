package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/moni-ai/moni-aigate/internal/testsupport/mockupstream"
)

var (
	hex40    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hex64    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	envName  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	runID    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	slowTTFB = regexp.MustCompile(`^slow_ttfb:(0|[1-9][0-9]{0,3})$`)
)

func LoadConfig(path string) (Config, mockupstream.Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, mockupstream.Config{}, err
	}
	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, mockupstream.Config{}, fmt.Errorf("decode phase2 baseline config: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, mockupstream.Config{}, fmt.Errorf("phase2 baseline config contains trailing JSON")
	}
	mockCfg, err := mockupstream.LoadConfig(cfg.Mock.ConfigPath)
	if err != nil {
		return Config{}, mockupstream.Config{}, err
	}
	if err := cfg.Validate(mockCfg); err != nil {
		return Config{}, mockupstream.Config{}, err
	}
	return cfg, mockCfg, nil
}

func (c Config) Validate(mockCfg mockupstream.Config) error {
	if c.SchemaVersion != 1 || !runID.MatchString(c.RunID) {
		return fmt.Errorf("schema_version=1 and a bounded lowercase run_id are required")
	}
	if !hex40.MatchString(c.Source.CommitSHA) || !hex64.MatchString(c.Source.TreeSHA256) || strings.TrimSpace(c.Source.BuildCommand) == "" {
		return fmt.Errorf("exact source commit/tree and build command are required")
	}
	gatewayURL, err := url.Parse(c.Gateway.BaseURL)
	if err != nil || (gatewayURL.Scheme != "http" && gatewayURL.Scheme != "https") || gatewayURL.Host == "" || gatewayURL.User != nil || gatewayURL.Path != "" || gatewayURL.RawQuery != "" || gatewayURL.Fragment != "" {
		return fmt.Errorf("gateway base_url must be a plain HTTP(S) origin")
	}
	if (gatewayURL.Scheme == "https") != c.Connection.TLS {
		return fmt.Errorf("connection.tls must match the gateway origin")
	}
	if !envName.MatchString(c.Gateway.APIKeyEnv) || !strings.HasPrefix(c.Gateway.Path, "/") || strings.ContainsAny(c.Gateway.Path, "\r\n") {
		return fmt.Errorf("gateway api_key_env and path are invalid")
	}
	var body any
	if json.Unmarshal(c.Gateway.Body, &body) != nil || bytes.Count(c.Gateway.Body, []byte("{{nonce}}")) != 1 || placeholderStringValues(body) != 1 || bytes.Contains(c.Gateway.Body, []byte("[[mock:")) {
		return fmt.Errorf("gateway body must contain exactly one string-value {{nonce}} placeholder and no preexisting mock directive")
	}
	if !streamChatBody(body) {
		return fmt.Errorf("the baseline runner currently requires an OpenAI chat stream body with model, messages and stream=true")
	}
	mockURL, err := url.Parse(c.Mock.ReceiptsURL)
	if err != nil || (mockURL.Scheme != "http" && mockURL.Scheme != "https") || mockURL.Host == "" || mockURL.User != nil || mockURL.Fragment != "" || mockURL.Path != "/__mock/receipts" || mockURL.RawQuery != "" {
		return fmt.Errorf("mock receipts_url must be the unmodified directive-gated receipts endpoint")
	}
	if len(mockCfg.Candidates) == 0 {
		return fmt.Errorf("mock config has no candidates")
	}
	if c.Load.ConcurrentClients <= 0 || c.Load.ConcurrentClients > 10000 || c.Load.ExpectedActiveStreams <= 0 || c.Load.ExpectedActiveStreams > c.Load.ConcurrentClients {
		return fmt.Errorf("invalid concurrent client or expected active stream count")
	}
	if c.Load.WarmupSeconds <= 0 || c.Load.SampleSeconds <= 0 || c.Load.Repeats < 3 || len(c.Load.RotationOrder) != c.Load.Repeats || c.Load.RequestTimeoutSeconds <= 0 {
		return fmt.Errorf("warmup/sample/timeout must be positive and at least three explicit repeats are required")
	}
	for _, target := range c.Load.RotationOrder {
		if target != "moni" {
			return fmt.Errorf("this runner only executes moni; competitor results must come from a separate equivalent run")
		}
	}
	if !slowTTFB.MatchString(c.Load.UpstreamFault) {
		return fmt.Errorf("upstream_fault must pin slow_ttfb between 0 and 10000ms")
	}
	if c.Connection.HTTPVersion != "HTTP/1.1" && c.Connection.HTTPVersion != "HTTP/2" {
		return fmt.Errorf("connection.http_version must be HTTP/1.1 or HTTP/2")
	}
	if strings.TrimSpace(c.Timing.ClockSource) == "" {
		return fmt.Errorf("timing.clock_source is required for cross-process mock joins")
	}
	if c.Connection.MaxIdleConnections < 0 || c.Connection.MaxConnsPerHost < 0 || c.Connection.ProviderRetries < 0 || c.Connection.ProviderRetries > 2 {
		return fmt.Errorf("invalid connection or provider retry metadata")
	}
	chain := c.CommercialChain
	if !chain.NormalReleaseMain || !chain.Authentication || !chain.WalletHardReservation || !chain.KeyLimits || !chain.PersistentAttempts || !chain.FinalEvidence || !chain.UniqueSettlement || !chain.Audit {
		return fmt.Errorf("the phase2 runner refuses configurations that bypass any full-commercial-chain gate")
	}
	for name, resource := range map[string]ResourceConfig{"application": c.Resources.Application, "postgresql": c.Resources.PostgreSQL, "load_generator": c.Resources.LoadGenerator, "mock": c.Resources.Mock} {
		if strings.TrimSpace(resource.CPU) == "" || strings.TrimSpace(resource.Memory) == "" {
			return fmt.Errorf("%s resource CPU and memory metadata are required", name)
		}
	}
	return nil
}

func placeholderStringValues(value any) int {
	switch typed := value.(type) {
	case string:
		return strings.Count(typed, "{{nonce}}")
	case []any:
		count := 0
		for _, item := range typed {
			count += placeholderStringValues(item)
		}
		return count
	case map[string]any:
		count := 0
		for _, item := range typed {
			count += placeholderStringValues(item)
		}
		return count
	default:
		return 0
	}
}

func streamChatBody(body any) bool {
	object, ok := body.(map[string]any)
	if !ok {
		return false
	}
	stream, _ := object["stream"].(bool)
	model, _ := object["model"].(string)
	messages, _ := object["messages"].([]any)
	return stream && model != "" && len(messages) > 0 && placeholderStringValues(messages) == 1
}

func mockSummary(cfg mockupstream.Config, fault string) map[string]any {
	candidates := make([]map[string]any, 0, len(cfg.Candidates))
	for _, name := range cfg.CandidateNames() {
		candidate := cfg.Candidates[name]
		candidates = append(candidates, map[string]any{
			"name": name, "models": append([]string(nil), candidate.Models...),
			"ttfb_ms": candidate.TTFBMS, "event_interval_ms": candidate.EventIntervalMS,
			"output_tokens": candidate.OutputTokens, "default_fault": candidate.DefaultFault,
		})
	}
	return map[string]any{"candidates": candidates, "protocols": []string{"openai_chat"}, "request_fault": fault, "secrets_included": false}
}
