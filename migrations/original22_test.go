package migrations

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
)

type original22RecordingFS struct {
	fs.FS
	opened []string
}

func (r *original22RecordingFS) Open(name string) (fs.File, error) {
	r.opened = append(r.opened, name)
	return r.FS.Open(name)
}

func original22TestFiles(t *testing.T) (fstest.MapFS, []original22Entry) {
	t.Helper()
	var entries []original22Entry
	if json.Unmarshal(original22Canonical, &entries) != nil || len(entries) != 22 {
		t.Fatal("invalid historical test material")
	}
	files := fstest.MapFS{}
	for _, entry := range entries {
		body, err := Files.ReadFile(entry.Name)
		if err != nil {
			t.Fatal("missing original embedded file")
		}
		files[entry.Name] = &fstest.MapFile{Data: bytes.Clone(body)}
	}
	return files, entries
}

func TestOriginal22ManifestFixedOwnedMaterial(t *testing.T) {
	first, err := Original22Manifest()
	if err != nil || len(first) != 2459 || original22Digest(first) != "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de" {
		t.Fatal("original canonical material changed")
	}
	second, err := Original22Manifest()
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("historical material is not repeatable")
	}
	first[0] = '!'
	third, err := Original22Manifest()
	if err != nil || !bytes.Equal(second, third) || bytes.Equal(first, third) {
		t.Fatal("caller mutation changed historical material")
	}
}

func TestOriginal22ManifestFakeFSIgnoresFutureFiles(t *testing.T) {
	files, entries := original22TestFiles(t)
	// Future names before, within, and after the old range never participate in
	// this historical inventory, even when their bodies are deliberately invalid.
	for _, name := range []string{"000_future.sql", "001_additional.sql", "023_future.sql", "999_future.sql"} {
		files[name] = &fstest.MapFile{Data: []byte("not an original migration")}
	}
	recording := &original22RecordingFS{FS: files}
	got, err := original22Material(recording, original22Canonical)
	if err != nil || !reflect.DeepEqual(got, entries) {
		t.Fatal("future files changed historical material")
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if !reflect.DeepEqual(recording.opened, names) {
		t.Fatal("historical material enumerated or opened nonhistorical files")
	}
}

func TestOriginal22ManifestFakeFSRejectsChangedMissingOrUntrustedMaterial(t *testing.T) {
	for _, mode := range []string{"changed_first", "changed_last", "missing_middle", "nil_fs", "altered_manifest", "trailing_manifest", "empty_manifest"} {
		t.Run(mode, func(t *testing.T) {
			files, entries := original22TestFiles(t)
			var source fs.FS = files
			material := bytes.Clone(original22Canonical)
			switch mode {
			case "changed_first":
				files[entries[0].Name].Data = append(files[entries[0].Name].Data, '\n')
			case "changed_last":
				files[entries[21].Name].Data = []byte("changed original")
			case "missing_middle":
				delete(files, entries[10].Name)
			case "nil_fs":
				source = nil
			case "altered_manifest":
				material[100] ^= 1
			case "trailing_manifest":
				material = append(material, '\n')
			case "empty_manifest":
				material = nil
			}
			if got, err := original22Material(source, material); got != nil || err != ErrOriginal22Manifest {
				t.Fatal("invalid original material accepted or exposed diagnostics")
			}
		})
	}
}

// Embedded methods panic if the verifier reaches a transaction for input that
// must be rejected before any driver operation.
type original22UnusedTx struct{ pgx.Tx }

func TestOriginal22SnapshotRejectsNilContextAndTransactionBeforeDriver(t *testing.T) {
	if VerifyOriginal22SnapshotTx(context.Background(), nil, "target") != ErrVerifyOriginal22Snapshot {
		t.Fatal("nil transaction accepted")
	}
	var typedNil *original22UnusedTx
	if VerifyOriginal22SnapshotTx(context.Background(), typedNil, "target") != ErrVerifyOriginal22Snapshot {
		t.Fatal("typed-nil transaction accepted")
	}
	tx := &original22UnusedTx{}
	if VerifyOriginal22SnapshotTx(nil, tx, "target") != ErrVerifyOriginal22Snapshot {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if VerifyOriginal22SnapshotTx(ctx, tx, "target") != ErrVerifyOriginal22Snapshot {
		t.Fatal("canceled context accepted")
	}
	for _, schema := range []string{"", strings.Repeat("a", 64), "nul\x00schema", string([]byte{0xff})} {
		if VerifyOriginal22SnapshotTx(context.Background(), tx, schema) != ErrVerifyOriginal22Snapshot {
			t.Fatal("invalid schema accepted")
		}
	}
}
