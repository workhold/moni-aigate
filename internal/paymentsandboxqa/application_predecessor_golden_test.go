package paymentsandboxqa

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Test-only bridge: compare the separately owned offline wire struct/encoder.
func TestApplicationPredecessorIndependentGolden(t *testing.T) {
	raw, err := os.ReadFile("../maintenancetrust/testdata/predecessor_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := os.ReadFile("../maintenancetrust/testdata/predecessor_v1.sha256")
	if err != nil {
		t.Fatal(err)
	}
	var p applicationProof
	if json.Unmarshal(raw, &p) != nil {
		t.Fatal("independent predecessor vector malformed")
	}
	canonical, err := applicationCanonical(p)
	if err != nil || !bytes.Equal(raw, canonical) || applicationSHA(raw) != strings.TrimSpace(string(digest)) {
		t.Fatal("offline writer bytes/digest diverged from independent golden")
	}
}
