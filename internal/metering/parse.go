package metering

import "strconv"

func ParseSchema(raw []byte) (Schema, error) {
	value, err := parseJSON(raw, 16_384)
	if err != nil {
		return Schema{}, err
	}
	m, ok := exactObject(value, "schema_id", "version", "dimensions", "retail_dimensions", "supplier_dimensions")
	if !ok {
		return Schema{}, ErrInvalidSchema
	}
	id, _ := stringValue(m["schema_id"])
	version, _ := stringValue(m["version"])
	dimensions, ok := m["dimensions"].([]any)
	if !validUUID(id) || version != "1" || !ok || len(dimensions) == 0 || len(dimensions) > 8 {
		return Schema{}, ErrInvalidSchema
	}
	wire := schemaWire{SchemaID: id, Version: version}
	maximum := make([]int64, len(dimensions))
	declared := make(map[string]bool, len(dimensions))
	for i, value := range dimensions {
		d, ok := exactObject(value, "dimension", "unit", "maximum_quantity")
		if !ok {
			return Schema{}, ErrInvalidSchema
		}
		name, _ := stringValue(d["dimension"])
		unit, _ := stringValue(d["unit"])
		q, ok := decimal(d["maximum_quantity"])
		if registryUnit(name) == "" || unit != registryUnit(name) || !ok || i > 0 && name <= wire.Dimensions[i-1].Dimension {
			return Schema{}, ErrInvalidSchema
		}
		wire.Dimensions = append(wire.Dimensions, dimensionWire{name, unit, strconv.FormatInt(q, 10)})
		maximum[i] = q
		declared[name] = false
	}
	if wire.RetailDimensions, ok = sortedStrings(m["retail_dimensions"]); !ok {
		return Schema{}, ErrInvalidSchema
	}
	if wire.SupplierDimensions, ok = sortedStrings(m["supplier_dimensions"]); !ok {
		return Schema{}, ErrInvalidSchema
	}
	for _, mask := range [][]string{wire.RetailDimensions, wire.SupplierDimensions} {
		for _, name := range mask {
			if _, exists := declared[name]; !exists {
				return Schema{}, ErrInvalidSchema
			}
			declared[name] = true
		}
	}
	for _, included := range declared {
		if !included {
			return Schema{}, ErrInvalidSchema
		}
	}
	s := Schema{valid: true, wire: wire, maximum: maximum, canonical: canonical(wire)}
	s.digest = frameDigest([]byte("moni-aigate.metering.schema.v1"), s.canonical)
	return s, nil
}

type parsedEntry struct {
	dimension, unit string
	quantity        int64
	numerator       int64
	denominator     int64
}

// parseVector preserves the documented error precedence: JSON, parameters,
// complete wire shape/values, identity binding, then set and global bound checks.
func parseVector(schema Schema, side Side, raw []byte, field string) ([]parsedEntry, error) {
	value, err := parseJSON(raw, 8_192)
	if err != nil {
		return nil, err
	}
	if !schema.valid {
		return nil, ErrInvalidValue
	}
	if !validSide(side) {
		return nil, ErrSide
	}
	fields := []string{"schema_id", "version", "side", field}
	if field == "rates" {
		fields = []string{"schema_id", "version", "side", "currency", "rates"}
	}
	m, ok := exactObject(value, fields...)
	if !ok {
		return nil, ErrInvalidVector
	}
	id, _ := stringValue(m["schema_id"])
	version, _ := stringValue(m["version"])
	wireSideString, _ := stringValue(m["side"])
	wireSide := Side(wireSideString)
	array, ok := m[field].([]any)
	if !validUUID(id) || version != "1" || !validSide(wireSide) || !ok || len(array) == 0 || len(array) > 8 {
		return nil, ErrInvalidVector
	}
	if field == "rates" && m["currency"] != "CNY" {
		return nil, ErrInvalidVector
	}
	entries := make([]parsedEntry, len(array))
	for i, value := range array {
		fields := []string{"dimension", "unit", "quantity"}
		if field == "rates" {
			fields = []string{"dimension", "unit", "numerator", "denominator"}
		}
		m, ok := exactObject(value, fields...)
		if !ok {
			return nil, ErrInvalidVector
		}
		entry := &entries[i]
		entry.dimension, _ = stringValue(m["dimension"])
		entry.unit, _ = stringValue(m["unit"])
		if registryUnit(entry.dimension) == "" || entry.unit != registryUnit(entry.dimension) {
			return nil, ErrInvalidVector
		}
		if field == "rates" {
			var nOK, dOK bool
			entry.numerator, nOK = decimal(m["numerator"])
			entry.denominator, dOK = decimal(m["denominator"])
			if !nOK || !dOK || entry.denominator < 1 || entry.denominator > 1_000_000 || gcd(entry.numerator, entry.denominator) != 1 {
				return nil, ErrInvalidVector
			}
		} else if entry.quantity, ok = decimal(m["quantity"]); !ok {
			return nil, ErrInvalidVector
		}
	}
	if id != schema.wire.SchemaID || wireSide != side {
		return nil, ErrBindingMismatch
	}
	mask := schema.mask(side)
	if len(entries) != len(mask) {
		return nil, ErrInvalidVector
	}
	for i, entry := range entries {
		if entry.dimension != mask[i] {
			return nil, ErrInvalidVector
		}
		if field == "bounds" {
			for j, dimension := range schema.wire.Dimensions {
				if dimension.Dimension == entry.dimension && entry.quantity > schema.maximum[j] {
					return nil, ErrInvalidVector
				}
			}
		}
	}
	return entries, nil
}

func parseQuantities(schema Schema, side Side, raw []byte, field string) (vector, error) {
	entries, err := parseVector(schema, side, raw, field)
	if err != nil {
		return vector{}, err
	}
	wire := make([]quantityWire, len(entries))
	result := vector{binding: schema.bind(side), dimensions: make([]string, len(entries)), units: make([]string, len(entries)), values: make([]int64, len(entries))}
	for i, entry := range entries {
		wire[i] = quantityWire{entry.dimension, entry.unit, strconv.FormatInt(entry.quantity, 10)}
		result.dimensions[i], result.units[i] = entry.dimension, entry.unit
		result.values[i] = entry.quantity
	}
	if field == "bounds" {
		result.canonical = canonical(boundsWire{schema.wire.SchemaID, "1", side, wire})
	} else {
		result.canonical = canonical(quantitiesWire{schema.wire.SchemaID, "1", side, wire})
	}
	return result, nil
}

func ParseBounds(schema Schema, side Side, raw []byte) (Bounds, error) {
	v, err := parseQuantities(schema, side, raw, "bounds")
	if err != nil {
		return Bounds{}, err
	}
	return Bounds{vector: v}, nil
}
func ParseQuantities(schema Schema, side Side, raw []byte) (Quantities, error) {
	v, err := parseQuantities(schema, side, raw, "quantities")
	if err != nil {
		return Quantities{}, err
	}
	return Quantities{vector: v}, nil
}
func ParseRates(schema Schema, side Side, raw []byte) (Rates, error) {
	entries, err := parseVector(schema, side, raw, "rates")
	if err != nil {
		return Rates{}, err
	}
	wire := make([]rateWire, len(entries))
	result := Rates{binding: schema.bind(side), dimensions: make([]string, len(entries)), numerators: make([]int64, len(entries)), denominators: make([]int64, len(entries))}
	for i, entry := range entries {
		result.dimensions[i] = entry.dimension
		wire[i] = rateWire{entry.dimension, entry.unit, strconv.FormatInt(entry.numerator, 10), strconv.FormatInt(entry.denominator, 10)}
		result.numerators[i], result.denominators[i] = entry.numerator, entry.denominator
	}
	result.canonical = canonical(ratesWire{schema.wire.SchemaID, "1", side, "CNY", wire})
	return result, nil
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
