package metering

// Dimension describes one immutable schema dimension. A description grants no
// ability to construct a schema or change the binding of an existing vector.
type Dimension struct {
	Dimension       string
	Unit            string
	MaximumQuantity int64
}

// Rate describes an already validated exact rational rate.
type Rate struct {
	Dimension   string
	Unit        string
	Numerator   int64
	Denominator int64
}

func (s Schema) ID() (string, error) {
	if !s.valid {
		return "", ErrInvalidValue
	}
	return s.wire.SchemaID, nil
}

// Dimensions returns a copy in the schema's canonical order.
func (s Schema) Dimensions() ([]Dimension, error) {
	if !s.valid {
		return nil, ErrInvalidValue
	}
	dimensions := make([]Dimension, len(s.wire.Dimensions))
	for i, d := range s.wire.Dimensions {
		dimensions[i] = Dimension{Dimension: d.Dimension, Unit: d.Unit, MaximumQuantity: s.maximum[i]}
	}
	return dimensions, nil
}

// Mask returns a copy of the side's complete ordered dimension set.
func (s Schema) Mask(side Side) ([]string, error) {
	if !s.valid {
		return nil, ErrInvalidValue
	}
	if !validSide(side) {
		return nil, ErrSide
	}
	return append([]string(nil), s.mask(side)...), nil
}

// Entries returns a copy of the rates in their complete canonical mask order.
func (r Rates) Entries() ([]Rate, error) {
	if !r.valid {
		return nil, ErrInvalidValue
	}
	entries := make([]Rate, len(r.dimensions))
	for i, dimension := range r.dimensions {
		entries[i] = Rate{Dimension: dimension, Unit: registryUnit(dimension), Numerator: r.numerators[i], Denominator: r.denominators[i]}
	}
	return entries, nil
}
