package metering

import (
	"crypto/sha256"
	"encoding/binary"
)

func frameDigest(frames ...[]byte) [32]byte {
	h := sha256.New()
	for _, frame := range frames {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(frame)))
		_, _ = h.Write(length[:])
		_, _ = h.Write(frame)
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

func canonicalCopy(valid bool, raw []byte) ([]byte, error) {
	if !valid {
		return nil, ErrInvalidValue
	}
	return append([]byte(nil), raw...), nil
}

func (s Schema) CanonicalJSON() ([]byte, error) {
	return canonicalCopy(s.valid, s.canonical)
}
func (b Bounds) CanonicalJSON() ([]byte, error) {
	return canonicalCopy(b.vector.valid, b.vector.canonical)
}
func (q Quantities) CanonicalJSON() ([]byte, error) {
	return canonicalCopy(q.vector.valid, q.vector.canonical)
}
func (r Rates) CanonicalJSON() ([]byte, error) {
	return canonicalCopy(r.valid, r.canonical)
}
func (s Schema) Digest() ([32]byte, error) {
	if !s.valid {
		return [32]byte{}, ErrInvalidValue
	}
	return s.digest, nil
}
func vectorDigest(domain string, b binding, raw []byte) ([32]byte, error) {
	if !b.valid {
		return [32]byte{}, ErrInvalidValue
	}
	return frameDigest([]byte(domain), b.digest[:], []byte(b.side), raw), nil
}
func (b Bounds) Digest() ([32]byte, error) {
	return vectorDigest("moni-aigate.metering.bounds.v1", b.vector.binding, b.vector.canonical)
}
func (q Quantities) Digest() ([32]byte, error) {
	return vectorDigest("moni-aigate.metering.quantities.v1", q.vector.binding, q.vector.canonical)
}
func (r Rates) Digest() ([32]byte, error) {
	return vectorDigest("moni-aigate.metering.rates.v1", r.binding, r.canonical)
}
func QuoteDigest(bounds Bounds, rates Rates) ([32]byte, error) {
	b := bounds.vector.binding
	if err := checkBinding(b, rates.binding); err != nil {
		return [32]byte{}, err
	}
	return frameDigest([]byte("moni-aigate.metering.quote.v1"), b.digest[:], []byte(b.side), bounds.vector.canonical, rates.canonical), nil
}
