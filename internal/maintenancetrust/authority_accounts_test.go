package maintenancetrust

import (
	"crypto/sha256"
	"math"
	"strings"
	"testing"
)

func TestAuthorityAccountDigestUsesLengthPrefixedRawUTF8(t *testing.T) {
	d := newAuthorityAccountDigest()
	for _, id := range []string{"", "a", "é"} {
		if d.add(id) != nil {
			t.Fatal("valid text ID rejected")
		}
	}
	// Independent literal framing: uint64be(0), uint64be(1)+'a', uint64be(2)+UTF8(é).
	framed := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 0, 0, 0, 0, 0, 2, 0xc3, 0xa9}
	if d.count != 3 || d.sum() != sha256.Sum256(framed) {
		t.Fatal("wrong framing, normalization, or extra domain prefix")
	}
	empty := newAuthorityAccountDigest()
	if empty.count != 0 || empty.sum() != sha256.Sum256(nil) {
		t.Fatal("empty account set")
	}
	left, right := newAuthorityAccountDigest(), newAuthorityAccountDigest()
	_ = left.add("a")
	_ = left.add("bc")
	_ = right.add("ab")
	_ = right.add("c")
	if left.sum() == right.sum() {
		t.Fatal("ambiguous concatenation")
	}
}

func TestAuthorityAccountDigestDoesNotRestrictHistoricalTextIDs(t *testing.T) {
	d := newAuthorityAccountDigest()
	for _, id := range []string{"A", strings.Repeat("a", 100000), "é", "😀"} {
		if d.add(id) != nil {
			t.Fatal("unfrozen UUID/length restriction")
		}
	}
	if d.count != 4 {
		t.Fatal("account omitted")
	}
}

func TestAuthorityAccountDigestRejectsIncompleteOrUnorderedInput(t *testing.T) {
	for _, ids := range [][]string{{"a", "a"}, {"b", "a"}, {"a", string([]byte{0xff})}} {
		d := newAuthorityAccountDigest()
		if d.add(ids[0]) != nil || d.add(ids[1]) != ErrTransaction || d.count != 1 {
			t.Fatal("bad account stream accepted")
		}
	}
	d := newAuthorityAccountDigest()
	d.count = math.MaxUint64
	if d.add("x") != ErrTransaction {
		t.Fatal("count overflow")
	}
}

func TestAuthorityAccountEvidenceBindsCompletionAndInitOnly(t *testing.T) {
	a := &authorityStage{nonce: &authorityNonce{}, snapshot: &snapshotState{mode: snapshotInit}}
	e := &accountsEvidence{authority: a, nonce: a.nonce, count: 0, digest: sha256.Sum256(nil), complete: true}
	if e.verifyAuthority(a) != nil {
		t.Fatal("complete zero account set rejected")
	}
	copied := *a
	if e.verifyAuthority(&copied) != ErrTransaction {
		t.Fatal("authority copy rebound accounts")
	}
	e.complete = false
	if e.verifyAuthority(a) != ErrTransaction {
		t.Fatal("incomplete rows issued evidence")
	}
	e.complete = true
	a.snapshot.mode = snapshotPlan
	if e.verifyAuthority(a) != ErrTransaction {
		t.Fatal("Plan acquired account evidence")
	}
	a.snapshot.mode = snapshotInit
	e.digest = [32]byte{}
	if e.verifyAuthority(a) != ErrTransaction {
		t.Fatal("empty stream wrong digest")
	}
	if (&accountsEvidence{}).verifyAuthority(a) != ErrTransaction || (*accountsEvidence)(nil).verifyAuthority(a) != ErrTransaction {
		t.Fatal("zero account evidence")
	}
}
