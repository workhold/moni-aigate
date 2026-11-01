package periodicbudget

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
)

// Fingerprint binds the canonical command identity and configuration. The
// operation UUID is validated but excluded so only command content is hashed.
func Fingerprint(kind, actor, scope, target string, in PutPeriodicBudget) (string, error) {
	if in.Validate() != nil || !ValidUUID(actor) {
		return "", ErrInvalidRequest
	}
	if _, _, ok := commandTarget(kind, scope, target); !ok {
		return "", ErrInvalidRequest
	}
	h := sha256.New()
	for _, value := range []string{kind, actor, scope, target, in.ExpectedRevision, in.PeriodKind, strconv.FormatBool(in.Enabled), in.LimitAmount} {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(value)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(value))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
