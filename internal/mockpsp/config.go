package mockpsp

import (
	"context"
	"github.com/moni-ai/moni-aigate/internal/paymentruntime"
)

type RuntimeConfig struct {
	config paymentruntime.Config
	valid  bool
}

func (RuntimeConfig) String() string               { return "mockpsp.RuntimeConfig" }
func (RuntimeConfig) GoString() string             { return "mockpsp.RuntimeConfig" }
func (RuntimeConfig) MarshalJSON() ([]byte, error) { return []byte(`"mockpsp.RuntimeConfig"`), nil }
func LoadRuntimeConfig(ctx context.Context, path string) (RuntimeConfig, error) {
	c, e := paymentruntime.Load(ctx, path, paymentruntime.Mock)
	if e != nil {
		return RuntimeConfig{}, serverFailure("mock_unavailable")
	}
	return RuntimeConfig{config: c, valid: true}, nil
}
