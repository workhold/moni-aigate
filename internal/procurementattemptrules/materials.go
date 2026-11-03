// Package procurementattemptrules contains isolated original-attempt rules.
// It returns fixed SQL material and never installs or enables a business root.
package procurementattemptrules

import _ "embed"

// Version identifies the frozen original-attempt rules protocol.
const Version = 1

// MaterialSHA256 identifies the exact, unrendered SQL template bytes.
const MaterialSHA256 = "2ef4058cfe04ded0df1db4f571c3f46fed74e426430f517c1f5f09e37c134fdb"

//go:embed materials/attempt-rules-v1.sql
var sqlTemplate string

// SQLTemplate returns static material without rendering or executing it.
func SQLTemplate() string { return sqlTemplate }
