package metering

import "strconv"

// managementTokenSchema validates the one currently published text profile
// at the management boundary. Management still accepts integer units per
// million, while runtime facts are stored as the unified rational vector.
func managementTokenSchema(schema Schema, side Side) error {
	if !schema.valid {
		return ErrInvalidValue
	}
	if !validSide(side) {
		return ErrSide
	}
	if len(schema.wire.Dimensions) != 2 || schema.wire.Dimensions[0].Dimension != "text_input_token" || schema.wire.Dimensions[1].Dimension != "text_output_token" || len(schema.wire.RetailDimensions) != 2 || len(schema.wire.SupplierDimensions) != 2 {
		return ErrInvalidSchema
	}
	return nil
}

// ManagementTokenRates converts the management integer inputs once into the
// canonical rational rate vector used by all current billing paths.
func ManagementTokenRates(schema Schema, side Side, inputRate, outputRate int64) (Rates, error) {
	if err := managementTokenSchema(schema, side); err != nil {
		return Rates{}, err
	}
	if inputRate < 0 || outputRate < 0 {
		return Rates{}, ErrInvalidValue
	}
	rates := []rateWire{}
	for i, n := range []int64{inputRate, outputRate} {
		d := int64(1_000_000)
		factor := gcd(n, d)
		n, d = n/factor, d/factor
		rates = append(rates, rateWire{schema.wire.Dimensions[i].Dimension, "token", strconv.FormatInt(n, 10), strconv.FormatInt(d, 10)})
	}
	return ParseRates(schema, side, canonical(ratesWire{schema.wire.SchemaID, "1", side, "CNY", rates}))
}

func managementTokenQuantities(schema Schema, side Side, input, output int64, bounds bool) (vector, error) {
	if err := managementTokenSchema(schema, side); err != nil {
		return vector{}, err
	}
	if input < 0 || output < 0 {
		return vector{}, ErrInvalidValue
	}
	wire := []quantityWire{{"text_input_token", "token", strconv.FormatInt(input, 10)}, {"text_output_token", "token", strconv.FormatInt(output, 10)}}
	if bounds {
		return parseQuantities(schema, side, canonical(boundsWire{schema.wire.SchemaID, "1", side, wire}), "bounds")
	}
	return parseQuantities(schema, side, canonical(quantitiesWire{schema.wire.SchemaID, "1", side, wire}), "quantities")
}

func ManagementTokenQuantities(schema Schema, side Side, input, output int64) (Quantities, error) {
	v, err := managementTokenQuantities(schema, side, input, output, false)
	if err != nil {
		return Quantities{}, err
	}
	return Quantities{vector: v}, nil
}

func ManagementTokenBounds(schema Schema, side Side, input, output int64) (Bounds, error) {
	v, err := managementTokenQuantities(schema, side, input, output, true)
	if err != nil {
		return Bounds{}, err
	}
	return Bounds{vector: v}, nil
}
