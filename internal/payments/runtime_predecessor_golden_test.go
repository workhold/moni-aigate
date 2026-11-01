package payments

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Test-only bridge: the production codec remains private and unmodified.
func TestRuntimePredecessorIndependentGolden(t *testing.T) {
	raw, err := os.ReadFile("../maintenancetrust/testdata/predecessor_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := os.ReadFile("../maintenancetrust/testdata/predecessor_v1.sha256")
	if err != nil {
		t.Fatal(err)
	}
	var p runtimeProof
	if runtimeStrictDecode(raw, &p) != nil || runtimeValidateProof(p) != nil {
		t.Fatal("independent predecessor vector rejected by original v1")
	}
	canonical, err := runtimeCanonicalProof(p)
	if err != nil || !bytes.Equal(raw, canonical) || runtimeSHA(raw) != strings.TrimSpace(string(digest)) {
		t.Fatal("original v1 bytes/digest diverged from independent golden")
	}
}
