// Package metering validates immutable, fully known billing vectors and computes
// exact nonnegative CNY amounts. It grants no provider, financial, or actor authority.
package metering

import "errors"

type Side string

const (
	Retail   Side = "retail"
	Supplier Side = "supplier"
)

var (
	ErrTooLarge        = errors.New("meter_too_large")
	ErrInvalidJSON     = errors.New("meter_invalid_json")
	ErrInvalidSchema   = errors.New("meter_invalid_schema")
	ErrInvalidVector   = errors.New("meter_invalid_vector")
	ErrInvalidValue    = errors.New("meter_invalid_value")
	ErrSide            = errors.New("meter_invalid_side")
	ErrBindingMismatch = errors.New("meter_binding_mismatch")
	ErrAmountOverflow  = errors.New("meter_amount_overflow")
)

type dimensionWire struct {
	Dimension       string `json:"dimension"`
	Unit            string `json:"unit"`
	MaximumQuantity string `json:"maximum_quantity"`
}
type schemaWire struct {
	SchemaID           string          `json:"schema_id"`
	Version            string          `json:"version"`
	Dimensions         []dimensionWire `json:"dimensions"`
	RetailDimensions   []string        `json:"retail_dimensions"`
	SupplierDimensions []string        `json:"supplier_dimensions"`
}
type quantityWire struct {
	Dimension string `json:"dimension"`
	Unit      string `json:"unit"`
	Quantity  string `json:"quantity"`
}
type boundsWire struct {
	SchemaID string         `json:"schema_id"`
	Version  string         `json:"version"`
	Side     Side           `json:"side"`
	Bounds   []quantityWire `json:"bounds"`
}
type quantitiesWire struct {
	SchemaID   string         `json:"schema_id"`
	Version    string         `json:"version"`
	Side       Side           `json:"side"`
	Quantities []quantityWire `json:"quantities"`
}
type rateWire struct {
	Dimension   string `json:"dimension"`
	Unit        string `json:"unit"`
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
}
type ratesWire struct {
	SchemaID string     `json:"schema_id"`
	Version  string     `json:"version"`
	Side     Side       `json:"side"`
	Currency string     `json:"currency"`
	Rates    []rateWire `json:"rates"`
}

// Schema and the vector types expose no mutable members or unmarshal bypass.
// Their zero values are invalid. Copying them shares only private immutable data.
type Schema struct {
	valid     bool
	wire      schemaWire
	maximum   []int64
	canonical []byte
	digest    [32]byte
}
type binding struct {
	valid  bool
	id     string
	digest [32]byte
	side   Side
}
type vector struct {
	binding
	values    []int64
	canonical []byte
}
type Bounds struct{ vector vector }
type Quantities struct{ vector vector }
type Rates struct {
	binding
	dimensions   []string
	numerators   []int64
	denominators []int64
	canonical    []byte
}

func validSide(side Side) bool { return side == Retail || side == Supplier }

// Kept as code, not a mutable package registry or a configurable expression.
func registryUnit(dimension string) string {
	switch dimension {
	case "audio_input_millisecond", "audio_output_millisecond":
		return "millisecond"
	case "image_output_count":
		return "image"
	case "request_count":
		return "request"
	case "text_input_token", "text_output_token":
		return "token"
	case "text_input_unicode_scalar":
		return "unicode_scalar"
	case "text_input_utf8_byte":
		return "byte"
	default:
		return ""
	}
}

func (s Schema) mask(side Side) []string {
	if side == Retail {
		return s.wire.RetailDimensions
	}
	return s.wire.SupplierDimensions
}

func (s Schema) bind(side Side) binding {
	return binding{valid: true, id: s.wire.SchemaID, digest: s.digest, side: side}
}

func checkBinding(left, right binding) error {
	if !left.valid || !right.valid {
		return ErrInvalidValue
	}
	if left.id != right.id || left.digest != right.digest || left.side != right.side {
		return ErrBindingMismatch
	}
	return nil
}
