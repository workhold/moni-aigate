package main

import "testing"

func TestLifecycleIndependentFrozenMerchantAndCommandKeys(t *testing.T) {
	environment := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	for _, test := range []struct {
		kind, target string
		want         int32
	}{
		{"command", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", 1996767468},
		{"merchant", "mps1.bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.cccccccc-cccc-4ccc-8ccc-cccccccccccc", -1025910691},
	} {
		if got := lifecycleMockKey(environment, test.kind, test.target); got != test.want {
			t.Fatal("offline gate wire domain changed")
		}
	}
	key := lifecycleMockKey(environment, "merchant", "mps1.bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	if uint32(key) != 3269056605 {
		t.Fatal("signed key must preserve PostgreSQL OID bit pattern")
	}
	if key == lifecycleMockKey("dddddddd-dddd-4ddd-8ddd-dddddddddddd", "merchant", "mps1.bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.cccccccc-cccc-4ccc-8ccc-cccccccccccc") {
		t.Fatal("cross-environment gate collision in fixture")
	}
}
