package catalog

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"

	"github.com/moni-ai/moni-aigate/internal/routing"
)

type RoutePublishInput struct {
	Expected   int64               `json:"expected_revision"`
	Bindings   []BindingInput      `json:"bindings"`
	CostPolicy *routing.CostPolicy `json:"cost_policy"`
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
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	if value.CostPolicy != nil && routing.ValidateCostPolicy(*value.CostPolicy) != nil {
		return ErrInvalid
	}
	*in = RoutePublishInput(value)
	return nil
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
