package fundinglotbasis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type framer struct {
	ctx  context.Context
	hash hash.Hash
}

func (f *framer) write(b []byte) error {
	for len(b) > 0 {
		if f.ctx.Err() != nil {
			return contextFailure(f.ctx)
		}
		n := min(len(b), 65536)
		if _, e := f.hash.Write(b[:n]); e != nil {
			return ErrExecution
		}
		b = b[n:]
	}
	if f.ctx.Err() != nil {
		return contextFailure(f.ctx)
	}
	return nil
}
func (f *framer) integer(v uint64) error {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return f.write(b[:])
}
func (f *framer) frame(b []byte) error {
	if e := f.integer(uint64(len(b))); e != nil {
		return e
	}
	return f.write(b)
}
func (f *framer) nullable(v *string) error {
	if v == nil {
		return f.integer(0)
	}
	if e := f.integer(1); e != nil {
		return e
	}
	return f.frame([]byte(*v))
}
func (f *framer) header(e environment) error {
	return f.headerDescriptor(e, descriptorCanonical)
}
func (f *framer) headerDescriptor(e environment, descriptor []byte) error {
	if err := f.write([]byte("moni-aigate/funding-lots-basis/v1\x00")); err != nil {
		return err
	}
	for _, v := range []uint64{uint64(e.DatabaseOID), uint64(e.SchemaOID), 16, uint64(e.ServerVersionNum), 6} {
		if err := f.integer(v); err != nil {
			return err
		}
	}
	for _, v := range []string{e.ServerEncoding, e.ClientEncoding, e.DatabaseEncodingName, e.Provider, e.Collate, e.Ctype} {
		if err := f.frame([]byte(v)); err != nil {
			return err
		}
	}
	for _, v := range []*string{e.ICULocale, e.ICURules, e.CollationVersion, e.ActualCollationVersion} {
		if err := f.nullable(v); err != nil {
			return err
		}
	}
	for _, b := range [][]byte{sendCanonical, manifestCanonical, descriptor} {
		if err := f.frame(b); err != nil {
			return err
		}
	}
	return nil
}

// Read uses only the supplied Query. Its successful facts describe complete
// enumeration; they are not evidence of an authenticated transaction or role.
func Read(ctx context.Context, q Query, schema string) (Facts, error) {
	return readFixed(ctx, q, schema, loadMaterials)
}
func readFixed(ctx context.Context, q Query, schema string, load func() (*materials, error)) (Facts, error) {
	if ctx == nil || nilValue(q) || schema == "" || len(schema) > 63 || !utf8.ValidString(schema) || strings.ContainsRune(schema, 0) {
		return Facts{}, ErrInput
	}
	if ctx.Err() != nil {
		return Facts{}, contextFailure(ctx)
	}
	m, e := load()
	if e != nil {
		return Facts{}, e
	}
	s := &reader{ctx: ctx, q: q, schema: strings.Clone(schema), materials: m}
	env, e := s.metadata()
	if e != nil {
		return Facts{}, failure(ctx, e)
	}
	f := &framer{ctx: ctx, hash: sha256.New()}
	if e = f.headerDescriptor(env, m.profile.canonical); e != nil {
		return Facts{}, failure(ctx, e)
	}
	for _, t := range m.tables {
		if e = s.readTable(f, t); e != nil {
			return Facts{}, failure(ctx, e)
		}
	}
	if ctx.Err() != nil {
		return Facts{}, contextFailure(ctx)
	}
	r := Report{Scope: m.profile.scope, PhysicalEOFComplete: true, ScannedTables: uint64(m.profile.tables), ScannedColumns: uint64(m.profile.columns), SourceRows: uint64(s.rows), SourceBytes: uint64(s.bytes), BasisSHA256: hex.EncodeToString(f.hash.Sum(nil)), ManifestSHA256: manifestSHA, DescriptorSHA256: m.profile.digest, SendMetadataSHA256: sendSHA}
	return Facts{state: &factsState{report: r}}, nil
}
func (s *reader) count(t table) (int64, error) {
	if s.ctx.Err() != nil {
		return 0, contextFailure(s.ctx)
	}
	rows, e := s.q.Query("/* funding_basis.count.v1 */ SELECT pg_catalog.count(*) FROM ONLY " + pgx.Identifier{s.schema, t.name}.Sanitize())
	if e != nil || nilValue(rows) {
		if !nilValue(rows) {
			_ = rows.Close()
		}
		return 0, contextFailure(s.ctx)
	}
	fail := func(e error) (int64, error) { _ = rows.Close(); return 0, failure(s.ctx, e) }
	more, e := rows.Next()
	if e != nil || !more {
		return fail(ErrExecution)
	}
	var n int64
	if rows.Scan(&n) != nil || n < 0 {
		return fail(ErrExecution)
	}
	if n > int64(rowLimit-s.rows) {
		return fail(ErrResource)
	}
	more, e = rows.Next()
	if e != nil || more {
		return fail(ErrExecution)
	}
	e = rows.Err()
	closed := rows.Close()
	if s.ctx.Err() != nil {
		return 0, contextFailure(s.ctx)
	}
	if e != nil || closed != nil {
		return 0, ErrExecution
	}
	return n, nil
}
func (s *reader) readTable(f *framer, t table) error {
	expected, e := s.count(t)
	if e != nil {
		return e
	}
	if e = f.frame([]byte(t.name)); e != nil {
		return e
	}
	if e = f.integer(uint64(len(t.columns))); e != nil {
		return e
	}
	if e = f.frame(t.pkCanonical); e != nil {
		return e
	}
	if e = f.integer(uint64(expected)); e != nil {
		return e
	}
	var cursor []byte
	hasCursor := false
	actual := int64(0)
	sql := pageSQL(s.schema, t)
	for {
		if s.ctx.Err() != nil {
			return contextFailure(s.ctx)
		}
		rows, e := s.q.Query(sql, hasCursor, cursor, int64(rowLimit-s.rows), int64(byteLimit-s.bytes))
		if e != nil || nilValue(rows) {
			if !nilValue(rows) {
				_ = rows.Close()
			}
			return contextFailure(s.ctx)
		}
		fail := func(e error) error { _ = rows.Close(); return failure(s.ctx, e) }
		count := 0
		for {
			if s.ctx.Err() != nil {
				return fail(contextFailure(s.ctx))
			}
			more, e := rows.Next()
			if e != nil {
				return fail(ErrExecution)
			}
			if !more {
				break
			}
			var qualified bool
			var key, row []byte
			if rows.Scan(&qualified, &key, &row) != nil {
				return fail(ErrExecution)
			}
			if !qualified {
				return fail(ErrResource)
			}
			if count >= pageSize {
				return fail(ErrExecution)
			}
			if len(key) > keyLimit || len(row) > recordLimit || len(key) > byteLimit-s.bytes || len(row) > byteLimit-s.bytes-len(key) || s.rows >= rowLimit {
				return fail(ErrResource)
			}
			if key == nil || row == nil || (hasCursor || count > 0) && bytes.Compare(key, cursor) <= 0 {
				return fail(ErrExecution)
			}
			if e = validateRecord(s.ctx, t, key, row); e != nil {
				return fail(e)
			}
			if e = f.frame(row); e != nil {
				return fail(e)
			}
			s.rows++
			s.bytes += len(key) + len(row)
			actual++
			count++
			// The supplied driver may reuse its bytea buffer at the next Next.
			cursor = bytes.Clone(key)
		}
		e = rows.Err()
		closed := rows.Close()
		if s.ctx.Err() != nil {
			return contextFailure(s.ctx)
		}
		if e != nil || closed != nil {
			return ErrExecution
		}
		if count < pageSize {
			if actual != expected {
				return ErrExecution
			}
			return nil
		}
		hasCursor = true
	}
}
