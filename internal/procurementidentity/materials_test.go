package procurementidentity_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/procurementidentity"
)

func TestFrozenIdentityMaterial(t *testing.T) {
	// This independent pin must change only after a new reviewed material
	// contract. Loading a self-declared SHA alone does not authenticate bytes.
	const want = "05f4476b25158624ed08d1e6497ac41580bb3c50550ff7164f7ea08c8be3eacc"
	sum := sha256.Sum256([]byte(procurementidentity.SQLTemplate()))
	if procurementidentity.Version != 1 || procurementidentity.MaterialSHA256 != want || hex.EncodeToString(sum[:]) != want {
		t.Fatal("frozen identity SQL material or protocol changed")
	}
}
