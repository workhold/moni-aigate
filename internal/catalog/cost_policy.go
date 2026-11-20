package catalog

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"time"

	"github.com/moni-ai/moni-aigate/internal/routing"
)

type RoutePublishInput struct {
	Expected         int64               `json:"expected_revision"`
	Bindings         []BindingInput      `json:"bindings"`
	CostPolicy       *routing.CostPolicy `json:"cost_policy"`
	ReleaseIntent    string              `json:"release_intent,omitempty"`
	FailureThreshold *int                `json:"failure_threshold,omitempty"`
	OpenMS           *int                `json:"open_ms,omitempty"`
	HalfOpenTokens   *int                `json:"half_open_tokens,omitempty"`
	CacheTTLSeconds  *int                `json:"cache_ttl_seconds,omitempty"`
}

func (in *RoutePublishInput) UnmarshalJSON(data []byte) error {
	type plain RoutePublishInput
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return ErrInvalid
	}
	if raw, exists := fields["cost_policy"]; exists && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ErrInvalid
	}
	for _, name := range []string{"failure_threshold", "open_ms", "half_open_tokens", "cache_ttl_seconds"} {
		if raw, exists := fields[name]; exists && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return ErrInvalid
		}
	}
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	if value.CostPolicy != nil && routing.ValidateCostPolicy(*value.CostPolicy) != nil {
		return ErrInvalid
	}
	if value.ReleaseIntent == "" {
		value.ReleaseIntent = ReleaseTestOnly
	}
	if value.ReleaseIntent != ReleaseTestOnly && value.ReleaseIntent != ReleaseCommercial {
		return ErrInvalid
	}
	if _, err := RoutePublishInput(value).BreakerConfig(); err != nil {
		return err
	}
	if value.CacheTTLSeconds != nil && (*value.CacheTTLSeconds != 0 && (*value.CacheTTLSeconds < 60 || *value.CacheTTLSeconds > 86400)) {
		return ErrInvalid
	}
	*in = RoutePublishInput(value)
	return nil
}

func (in RoutePublishInput) BreakerConfig() (routing.BreakerConfig, error) {
	config := routing.DefaultBreakerConfig()
	if in.FailureThreshold != nil {
		config.FailureThreshold = *in.FailureThreshold
	}
	if in.OpenMS != nil {
		if *in.OpenMS < 1000 || *in.OpenMS > 600000 {
			return config, ErrInvalid
		}
		config.OpenDuration = time.Duration(*in.OpenMS) * time.Millisecond
	}
	if in.HalfOpenTokens != nil {
		config.HalfOpenTokens = *in.HalfOpenTokens
	}
	if routing.ValidateBreakerConfig(config) != nil {
		return config, ErrInvalid
	}
	return config, nil
}

func (in RoutePublishInput) CacheTTL() int {
	if in.CacheTTLSeconds == nil {
		return 0
	}
	return *in.CacheTTLSeconds
}

func parseCostRate(value *string) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	n, err := strconv.ParseInt(*value, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != *value {
		return nil, ErrInvalid
	}
	return &n, nil
}
