// Package procurementidentity contains the frozen SQL leaf for the original
// procurement identity ABI. It neither installs SQL nor accesses database facts.
package procurementidentity

import _ "embed"

// Version identifies the fixed identity encoding protocol.
const Version = 1

// MaterialSHA256 identifies the exact, unrendered SQL template bytes.
const MaterialSHA256 = "05f4476b25158624ed08d1e6497ac41580bb3c50550ff7164f7ea08c8be3eacc"

//go:embed materials/identity-v1.sql
var sqlTemplate string

// SQLTemplate returns the static material without rendering or executing it.
func SQLTemplate() string { return sqlTemplate }
