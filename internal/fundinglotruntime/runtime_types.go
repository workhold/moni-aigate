// Package fundinglotruntime authenticates one complete funding graph in a
// caller-owned application RR READ ONLY transaction. It does not start a
// process, end transactions, or authorize cross-transaction financial writes.
package fundinglotruntime

import (
	"errors"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

var ErrRuntime = errors.New("funding_lot_runtime_verification_failed")

// RuntimeProof is valid only for the original still-active transaction.
// Its zero value is invalid; copies share an immutable identity.
type RuntimeProof struct{ state *runtimeProofState }

type runtimeProofState struct {
	catalog                                *fundinglotcatalog.CatalogProof
	finalCatalog                           *fundinglotcatalog.CatalogProof
	pins                                   fundinglotcatalog.Pins
	epoch, proofSHA, basisSHA, manifestSHA string
	basisDescriptorSHA, predecessorSHA     string
	historicalRoot, currentRoot            string
	compatibility                          compatibilityRecord
	report                                 fundinglotscan.WalletReport
	prefixReport                           fundinglotscan.PrefixWalletReport
}

func (RuntimeProof) String() string               { return "fundinglotruntime.RuntimeProof(redacted)" }
func (p RuntimeProof) GoString() string           { return p.String() }
func (RuntimeProof) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }
