package maintenancetrust

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/migrations"
)

const authorityManifestCount = 22
const authorityManifestBytes = 2459
const authorityManifestSHA = "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de"

type authorityManifestEntry struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type manifestEvidence struct {
	authority *authorityStage
	nonce     *authorityNonce
	canonical []byte
	digest    string
}

// This pure encoder has no authority constructor. The actual issuer below uses
// only the embedded migrations.Files, followed by the original same-tx helper.
func authorityManifestCanonical(files fs.FS) ([]byte, int, error) {
	if files == nil {
		return nil, 0, ErrSchema
	}
	names, err := fs.Glob(files, "*.sql")
	if err != nil || len(names) != authorityManifestCount {
		return nil, 0, ErrSchema
	}
	sort.Strings(names)
	entries := make([]authorityManifestEntry, 0, len(names))
	maxName := 0
	for _, name := range names {
		body, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, 0, ErrSchema
		}
		digest := sha256.Sum256(body)
		entries = append(entries, authorityManifestEntry{name, hex.EncodeToString(digest[:])})
		if len(name) > maxName {
			maxName = len(name)
		}
	}
	canonical, err := json.Marshal(entries)
	if err != nil || !authorityManifestValid(canonical, authorityManifestSHA) {
		return nil, 0, ErrSchema
	}
	return canonical, maxName, nil
}

func (manifestEvidence) String() string               { return "maintenance_manifest_redacted" }
func (manifestEvidence) GoString() string             { return "maintenance_manifest_redacted" }
func (manifestEvidence) MarshalJSON() ([]byte, error) { return []byte(`{"manifest":"redacted"}`), nil }

func authorityManifestValid(canonical []byte, digest string) bool {
	if len(canonical) != authorityManifestBytes || digest != authorityManifestSHA {
		return false
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]) == authorityManifestSHA
}

func (a *authorityStage) attestManifest(ctx context.Context) (*manifestEvidence, error) {
	if err := a.verify(ctx); err != nil {
		return nil, err
	}
	canonical, maxName, err := authorityManifestCanonical(migrations.Files)
	if err != nil {
		return nil, authorityLeafFailure(ctx, ErrSchema)
	}
	q := `SELECT pg_catalog.count(*) OPERATOR(pg_catalog.=) 22::pg_catalog.int8
 AND COALESCE(pg_catalog.bool_and(name IS NOT NULL AND checksum IS NOT NULL
 AND pg_catalog.octet_length(name) OPERATOR(pg_catalog.>) 0
 AND pg_catalog.octet_length(name) OPERATOR(pg_catalog.<=) $1::pg_catalog.int4
 AND pg_catalog.octet_length(checksum) OPERATOR(pg_catalog.=) 64),false) FROM ` +
		pgx.Identifier{a.snapshot.gate.pins.AppSchema.Name, "schema_migrations"}.Sanitize()
	var qualified bool
	if a.snapshot.rawTx.QueryRow(ctx, q, pgx.QueryExecModeSimpleProtocol, maxName).Scan(&qualified) != nil || !qualified {
		return nil, authorityLeafFailure(ctx, ErrSchema)
	}
	if migrations.VerifySnapshotTx(ctx, a.snapshot.rawTx) != nil {
		return nil, authorityLeafFailure(ctx, ErrSchema)
	}
	if ctx.Err() != nil {
		return nil, ErrTimeout
	}
	return &manifestEvidence{authority: a, nonce: a.nonce, canonical: bytes.Clone(canonical), digest: authorityManifestSHA}, nil
}

func (m *manifestEvidence) verifyAuthority(a *authorityStage) error {
	if m == nil || a == nil || a.nonce == nil || m.authority != a || m.nonce != a.nonce || !authorityManifestValid(m.canonical, m.digest) {
		return ErrTransaction
	}
	return nil
}
