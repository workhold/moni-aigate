package maintenancetrust

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type accountsEvidence struct {
	authority *authorityStage
	nonce     *authorityNonce
	count     uint64
	digest    [32]byte
	complete  bool
}

type authorityAccountDigest struct {
	count    uint64
	previous string
	hash     hash.Hash
}

func newAuthorityAccountDigest() *authorityAccountDigest {
	return &authorityAccountDigest{hash: sha256.New()}
}

func (d *authorityAccountDigest) add(id string) error {
	if d == nil || d.hash == nil || d.count == math.MaxUint64 || !utf8.ValidString(id) || d.count > 0 && strings.Compare(d.previous, id) >= 0 {
		return ErrTransaction
	}
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(id)))
	_, _ = d.hash.Write(length[:])
	_, _ = d.hash.Write([]byte(id))
	d.previous = id
	d.count++
	return nil
}

func (d *authorityAccountDigest) sum() [32]byte {
	var result [32]byte
	copy(result[:], d.hash.Sum(nil))
	return result
}

func (a *authorityStage) lockAccounts(ctx context.Context) (*accountsEvidence, error) {
	if err := a.verify(ctx); err != nil {
		return nil, err
	}
	if a.snapshot.mode != snapshotInit {
		return nil, ErrTransaction
	}
	q := "SELECT id FROM " + pgx.Identifier{a.snapshot.gate.pins.AppSchema.Name, "ledger_accounts"}.Sanitize() +
		` ORDER BY id COLLATE pg_catalog."C" FOR UPDATE`
	rows, err := a.snapshot.rawTx.Query(ctx, q, pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		return nil, authorityLeafFailure(ctx, ErrExecution)
	}
	defer rows.Close()
	digest := newAuthorityAccountDigest()
	for rows.Next() {
		if ctx.Err() != nil {
			return nil, ErrTimeout
		}
		var id string
		if rows.Scan(&id) != nil {
			return nil, authorityLeafFailure(ctx, ErrExecution)
		}
		if err = digest.add(id); err != nil {
			return nil, err
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, authorityLeafFailure(ctx, ErrExecution)
	}
	if ctx.Err() != nil {
		return nil, ErrTimeout
	}
	return &accountsEvidence{authority: a, nonce: a.nonce, count: digest.count, digest: digest.sum(), complete: true}, nil
}

func (e *accountsEvidence) verifyAuthority(a *authorityStage) error {
	if e == nil || !e.complete || a == nil || a.nonce == nil || a.snapshot == nil || a.snapshot.mode != snapshotInit || e.authority != a || e.nonce != a.nonce ||
		e.count == 0 && e.digest != sha256.Sum256(nil) {
		return ErrTransaction
	}
	return nil
}

func (accountsEvidence) String() string               { return "maintenance_accounts_redacted" }
func (accountsEvidence) GoString() string             { return "maintenance_accounts_redacted" }
func (accountsEvidence) MarshalJSON() ([]byte, error) { return []byte(`{"accounts":"redacted"}`), nil }
