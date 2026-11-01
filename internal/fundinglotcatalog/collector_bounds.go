package fundinglotcatalog

// Both bounds derive solely from authenticated static expected metadata. The
// dependency categories are multisets: raw rows include frozen multiplicity,
// while normalization emits one record per identity with that exact count.
type categoryBoundKey struct{ scope, family, category string }
type categoryBound struct{ raw, normalized int }

func deriveCategoryBounds(d *descriptorSet) (map[categoryBoundKey]categoryBound, error) {
	bounds := map[categoryBoundKey]categoryBound{}
	for _, variant := range d.Variants {
		for _, family := range []struct {
			name string
			doc  document
		}{{"schema", variant.Schema}, {"roles", variant.Roles}} {
			for scope, value := range family.doc {
				doc, ok := value.(object)
				if !ok {
					return nil, ErrCatalog
				}
				registry := schemaRecordFields
				if family.name == "roles" {
					registry = roleRecordFields
				}
				for category := range registry {
					records, ok := doc[category].([]any)
					if !ok || len(records) > collectorRowLimit {
						return nil, ErrCatalog
					}
					raw := len(records)
					if category == "dependencies" || category == "acl_dependencies" {
						raw = 0
						for _, value := range records {
							record, ok := value.(object)
							if !ok {
								return nil, ErrCatalog
							}
							n, ok := number(record["multiplicity"])
							if !ok || n == 0 || uint64(n) > uint64(collectorRowLimit-raw) {
								return nil, ErrCatalog
							}
							raw += int(n)
						}
					}
					key := categoryBoundKey{scope, family.name, category}
					bound := bounds[key]
					if raw > bound.raw {
						bound.raw = raw
					}
					if len(records) > bound.normalized {
						bound.normalized = len(records)
					}
					bounds[key] = bound
				}
			}
		}
	}
	return bounds, nil
}
func (d *descriptorSet) categoryBound(scope, family, category string) (categoryBound, error) {
	if d == nil || d.bounds == nil {
		return categoryBound{}, ErrCatalog
	}
	b, ok := d.bounds[categoryBoundKey{scope, family, category}]
	if !ok || b.raw < 0 || b.raw > collectorRowLimit || b.normalized < 0 || b.normalized > b.raw {
		return categoryBound{}, ErrCatalog
	}
	return b, nil
}
