package mockpsp

import "testing"

func TestLockHashFrozenDomainsAndSignedOIDBits(t *testing.T) {
	const env = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const target = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	// Independently computed with Python hashlib and big-endian signed unpack.
	for _, tt := range []struct {
		kind, target string
		want         int32
	}{
		{"command", target, 1996767468},
		{"query", target, 1958553154},
		{"merchant", "mps1.bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.cccccccc-cccc-4ccc-8ccc-cccccccccccc", -1025910691},
	} {
		if got := lockHash(env, tt.kind, tt.target); got != tt.want {
			t.Fatalf("%s domain got %d want %d", tt.kind, got, tt.want)
		}
	}
	seen := map[int32]bool{}
	for _, namespace := range []int32{lockCommand, lockQuery, lockCallback, lockControl, lockMerchant} {
		if seen[namespace] || lockKind(namespace) == "" {
			t.Fatal("namespace collision or missing kind")
		}
		seen[namespace] = true
	}
	if lockKind(0) != "" {
		t.Fatal("unknown namespace accepted")
	}
}
