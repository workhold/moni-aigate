package maintenancetrust

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"sort"
	"testing"
	"testing/fstest"

	"github.com/moni-ai/moni-aigate/migrations"
)

func copyAuthorityOriginal22MigrationFiles(t *testing.T) fstest.MapFS {
	t.Helper()
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	// These pure fixtures retain the original 001-022 identity. Mutations
	// for negative cases are applied afterward and remain visible in full.
	sort.Strings(names)
	const originalThrough = "022_payment_runtime_attestation.sql"
	last := sort.SearchStrings(names, originalThrough)
	if last == len(names) || names[last] != originalThrough || last+1 != 22 {
		t.Fatal("original 001-022 fixture prefix drift")
	}
	names = names[:last+1]
	files := fstest.MapFS{}
	for _, name := range names {
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: bytes.Clone(body)}
	}
	return files
}

func TestAuthorityManifestUsesFixedCompleteEmbeddedBytes(t *testing.T) {
	b, maxName, err := authorityManifestCanonical(copyAuthorityOriginal22MigrationFiles(t))
	if err != nil || len(b) != 2459 {
		t.Fatal("fixed complete manifest")
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de" || bytes.HasSuffix(b, []byte{10}) {
		t.Fatal("manifest canonical provenance")
	}
	var entries []authorityManifestEntry
	if json.Unmarshal(b, &entries) != nil || len(entries) != 22 {
		t.Fatal("manifest entry set")
	}
	names := make([]string, 0, len(entries))
	wantMax := 0
	for _, e := range entries {
		names = append(names, e.Name)
		if len(e.Name) > wantMax {
			wantMax = len(e.Name)
		}
		body, err := migrations.Files.ReadFile(e.Name)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(body)
		if e.SHA256 != hex.EncodeToString(h[:]) {
			t.Fatal("manifest checksum not from original embed")
		}
	}
	if !sort.StringsAreSorted(names) || maxName != wantMax {
		t.Fatal("C order or history name bound")
	}
}

func TestAuthorityManifestRejectsChangedUnreviewedSources(t *testing.T) {
	for _, kind := range []string{"missing", "extra", "renamed", "modified"} {
		t.Run(kind, func(t *testing.T) {
			files := copyAuthorityOriginal22MigrationFiles(t)
			name := "001_core.sql"
			switch kind {
			case "missing":
				delete(files, name)
			case "extra":
				files["023_unreviewed.sql"] = &fstest.MapFile{Data: []byte("SELECT 1")}
			case "renamed":
				files["001_renamed.sql"] = files[name]
				delete(files, name)
			case "modified":
				files[name].Data = append(files[name].Data, ' ')
			}
			if b, _, err := authorityManifestCanonical(files); b != nil || err != ErrSchema {
				t.Fatal("unreviewed manifest issued")
			}
		})
	}
	if b, _, err := authorityManifestCanonical(nil); b != nil || err != ErrSchema {
		t.Fatal("nil source")
	}
}

func TestAuthorityManifestEvidenceCannotRebindOrAcceptAlteredCanonical(t *testing.T) {
	b, _, err := authorityManifestCanonical(copyAuthorityOriginal22MigrationFiles(t))
	if err != nil {
		t.Fatal(err)
	}
	a := &authorityStage{nonce: &authorityNonce{}}
	e := &manifestEvidence{authority: a, nonce: a.nonce, canonical: bytes.Clone(b), digest: authorityManifestSHA}
	if e.verifyAuthority(a) != nil {
		t.Fatal("immutable evidence unusable after active stage ended")
	}
	copied := *a
	if e.verifyAuthority(&copied) != ErrTransaction {
		t.Fatal("authority copy rebound evidence")
	}
	other := &authorityStage{nonce: &authorityNonce{}}
	if e.verifyAuthority(other) != ErrTransaction {
		t.Fatal("foreign authority")
	}
	e.canonical[0] ^= 1
	if e.verifyAuthority(a) != ErrTransaction {
		t.Fatal("canonical mutation accepted")
	}
	e.canonical = bytes.Clone(b)
	e.digest = ""
	if e.verifyAuthority(a) != ErrTransaction {
		t.Fatal("missing digest accepted")
	}
	if (&manifestEvidence{}).verifyAuthority(a) != ErrTransaction || (*manifestEvidence)(nil).verifyAuthority(a) != ErrTransaction {
		t.Fatal("zero evidence")
	}
}
