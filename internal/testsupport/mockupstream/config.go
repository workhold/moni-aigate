package mockupstream

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Config is immutable after the server is constructed. It is intentionally
// small: this service verifies gateway behavior, not provider billing rules.
type Config struct {
	Candidates map[string]Candidate `json:"candidates"`
}

type Candidate struct {
	Secret          string   `json:"secret"`
	Models          []string `json:"models"`
	TTFBMS          int      `json:"ttfb_ms"`
	EventIntervalMS int      `json:"event_interval_ms"`
	OutputTokens    int      `json:"output_tokens"`
	DefaultFault    string   `json:"default_fault"`
}

var candidateName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read mock upstream config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode mock upstream config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if len(c.Candidates) == 0 {
		return fmt.Errorf("mock upstream config must contain at least one candidate")
	}
	for name, candidate := range c.Candidates {
		if !candidateName.MatchString(name) {
			return fmt.Errorf("invalid mock candidate name %q", name)
		}
		if strings.TrimSpace(candidate.Secret) == "" {
			return fmt.Errorf("mock candidate %q has empty secret", name)
		}
		if len(candidate.Models) == 0 {
			return fmt.Errorf("mock candidate %q has no models", name)
		}
		seen := map[string]struct{}{}
		for _, model := range candidate.Models {
			if strings.TrimSpace(model) == "" || strings.ContainsAny(model, "/:\r\n") {
				return fmt.Errorf("mock candidate %q has invalid model", name)
			}
			if _, exists := seen[model]; exists {
				return fmt.Errorf("mock candidate %q repeats model %q", name, model)
			}
			seen[model] = struct{}{}
		}
		if candidate.TTFBMS < 0 || candidate.TTFBMS > 10*60*1000 {
			return fmt.Errorf("mock candidate %q has invalid ttfb_ms", name)
		}
		if candidate.EventIntervalMS < 0 || candidate.EventIntervalMS > 10*60*1000 {
			return fmt.Errorf("mock candidate %q has invalid event_interval_ms", name)
		}
		if candidate.OutputTokens <= 0 || candidate.OutputTokens > 100000 {
			return fmt.Errorf("mock candidate %q has invalid output_tokens", name)
		}
		if candidate.DefaultFault != "" {
			if _, err := parseFault(candidate.DefaultFault); err != nil {
				return fmt.Errorf("mock candidate %q: %w", name, err)
			}
		}
	}
	return nil
}

func (c Config) CandidateNames() []string {
	names := make([]string, 0, len(c.Candidates))
	for name := range c.Candidates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c Candidate) supportsModel(model string) bool {
	for _, candidateModel := range c.Models {
		if candidateModel == model {
			return true
		}
	}
	return false
}
