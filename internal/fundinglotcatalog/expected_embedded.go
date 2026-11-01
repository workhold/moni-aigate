package fundinglotcatalog

import _ "embed"

// These metadata-only roots were reviewed independently against fixed SQL,
// original descriptors and peer inventories, after two isolated installations
// per profile. Runtime never reads files or promotes captured live metadata.
// See specs/022-self-service-payments/c0-schema-check-contract.md section 10.

//go:embed expected/ordinary-v1.json
var ordinaryExpectedJSON []byte

//go:embed expected/sandbox-v1.json
var sandboxExpectedJSON []byte

const ordinaryExpectedSHA = "a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2"
const sandboxExpectedSHA = "a3a7f855a5ac58ee583dce071c02707499c2a9e3d4bd2ec8b7b1b60228d55c4f"
