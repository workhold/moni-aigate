// Package tasks contains the pure domain state machine for 028-A asynchronous
// tasks. It deliberately has no persistence, transport, billing, wallet, or
// provider dependencies.
package tasks

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	maxIDLength             = 36
	maxIdempotencyKeyLength = 128
	maxReferenceLength      = 200
	maxFailureCodeLength    = 80
	maxMediaTypeLength      = 127
	maxEventBytes           = 16 * 1024
	maxDownloadGrantTTL     = 15 * time.Minute
	maxArtifactBytes        = int64(1 << 30)
)

var (
	ErrInvalidTask         = errors.New("task_invalid")
	ErrInvalidID           = errors.New("task_invalid_id")
	ErrInvalidDigest       = errors.New("task_invalid_digest")
	ErrInvalidInput        = errors.New("task_invalid_input")
	ErrInvalidEvent        = errors.New("task_invalid_event")
	ErrInvalidTransition   = errors.New("task_invalid_transition")
	ErrNoResubmit          = errors.New("task_no_resubmit")
	ErrIdempotencyConflict = errors.New("task_idempotency_conflict")
	ErrEventConflict       = errors.New("task_event_conflict")
	ErrStaleEvent          = errors.New("task_stale_event")
	ErrAlreadyTerminal     = errors.New("task_already_terminal")
	ErrTerminalConflict    = errors.New("task_terminal_conflict")
	ErrExpired             = errors.New("task_expired")
	ErrArtifactExpired     = errors.New("task_artifact_expired")
	ErrArtifactNotReady    = errors.New("task_artifact_not_ready")
	ErrArtifactDigest      = errors.New("task_artifact_digest_mismatch")
	ErrArtifactTooLarge    = errors.New("task_artifact_too_large")
	ErrForbidden           = errors.New("task_forbidden")
	ErrNotFound            = errors.New("task_not_found")
	ErrGrantExpired        = errors.New("task_download_grant_expired")
	ErrCancelConflict      = errors.New("task_cancel_conflict")
)

// ID is a canonical, non-zero RFC 4122 UUID rendered in lowercase.
type ID string

func (id ID) String() string { return string(id) }

func (id ID) Valid() bool {
	_, err := ParseID(string(id))
	return err == nil
}

func ParseID(raw string) (ID, error) {
	if len(raw) != maxIDLength || raw != strings.ToLower(raw) {
		return "", ErrInvalidID
	}
	for i := 0; i < len(raw); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if raw[i] != '-' {
				return "", ErrInvalidID
			}
			continue
		}
		if !isLowerHex(raw[i]) {
			return "", ErrInvalidID
		}
	}
	if raw[14] < '1' || raw[14] > '8' || !strings.ContainsRune("89ab", rune(raw[19])) {
		return "", ErrInvalidID
	}
	allZero := true
	for i := 0; i < len(raw); i++ {
		if raw[i] != '-' && raw[i] != '0' {
			allZero = false
			break
		}
	}
	if allZero {
		return "", ErrInvalidID
	}
	return ID(raw), nil
}

func isLowerHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
}

// Digest is a 32-byte SHA-256 value. The zero value is never accepted where a
// digest is part of a task identity.
type Digest [32]byte

func (d Digest) IsZero() bool { return d == (Digest{}) }

func (d Digest) String() string { return hex.EncodeToString(d[:]) }

func ParseDigest(raw string) (Digest, error) {
	var d Digest
	if len(raw) != sha256.Size*2 || raw != strings.ToLower(raw) {
		return d, ErrInvalidDigest
	}
	b, err := hex.DecodeString(raw)
	if err != nil {
		return d, ErrInvalidDigest
	}
	copy(d[:], b)
	if d.IsZero() {
		return Digest{}, ErrInvalidDigest
	}
	return d, nil
}

func digestFrame(domain string, body []byte) Digest {
	h := sha256.New()
	var length [4]byte
	putUint32(length[:], uint32(len(domain)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(domain))
	putUint32(length[:], uint32(len(body)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(body)
	var out Digest
	copy(out[:], h.Sum(nil))
	return out
}

func putUint32(dst []byte, n uint32) {
	dst[0] = byte(n >> 24)
	dst[1] = byte(n >> 16)
	dst[2] = byte(n >> 8)
	dst[3] = byte(n)
}

func validIdempotencyKey(raw string) bool {
	if len(raw) == 0 || len(raw) > maxIdempotencyKeyLength {
		return false
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", rune(c)) {
			continue
		}
		return false
	}
	return true
}

func validSafeReference(raw string, maximum int) bool {
	if len(raw) == 0 || len(raw) > maximum || raw[0] < 'A' || raw[0] > 'z' || raw[0] >= '[' && raw[0] <= '`' {
		return false
	}
	if strings.Contains(raw, "://") || strings.ContainsAny(raw, "?#\\\t\r\n") {
		return false
	}
	for _, part := range strings.Split(raw, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("._:/-", rune(c)) {
			continue
		}
		return false
	}
	return true
}

func validFailureCode(raw string) bool {
	if len(raw) == 0 || len(raw) > maxFailureCodeLength {
		return false
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", rune(c)) {
			continue
		}
		return false
	}
	return true
}

func validUTC(t time.Time) bool {
	return !t.IsZero() && t.Location() == time.UTC && t == t.Round(0)
}

func validateID(id ID) error {
	if _, err := ParseID(string(id)); err != nil {
		return ErrInvalidID
	}
	return nil
}

func validateDigest(d Digest) error {
	if d.IsZero() {
		return ErrInvalidDigest
	}
	return nil
}

// Owner is the immutable task ownership scope.
type Owner struct {
	TenantID  ID
	SubjectID ID
}

func (o Owner) valid() error {
	if err := validateID(o.TenantID); err != nil {
		return err
	}
	if err := validateID(o.SubjectID); err != nil {
		return err
	}
	return nil
}

// Permission is supplied by the outer authentication layer. The pure package
// only checks the explicit capability and task scope; it does not inspect roles
// or query a database.
type Permission uint32

const (
	PermissionTaskRead Permission = 1 << iota
	PermissionTaskObserve
	PermissionTaskCancel
	PermissionResultDownload
	PermissionTenantRead
)

// Actor is an already-authenticated caller proof for a pure authorization
// check. SubjectID must never be left implicit.
type Actor struct {
	TenantID   ID
	SubjectID  ID
	Permission Permission
}

func (a Actor) valid() error {
	if err := validateID(a.TenantID); err != nil {
		return err
	}
	if err := validateID(a.SubjectID); err != nil {
		return err
	}
	return nil
}

func (a Actor) has(p Permission) bool { return a.Permission&p == p }

// ProtocolState is the task's provider protocol state. It is intentionally
// separate from observation freshness and artifact availability.
type ProtocolState string

const (
	StatePrepared          ProtocolState = "prepared"
	StateSubmitting        ProtocolState = "submitting"
	StateQueued            ProtocolState = "queued"
	StateRunning           ProtocolState = "running"
	StateSubmissionUnknown ProtocolState = "submission_unknown"
	StateCancelRequested   ProtocolState = "cancel_requested"
	StateSucceeded         ProtocolState = "succeeded"
	StateFailed            ProtocolState = "failed"
	StateCancelled         ProtocolState = "cancelled"
)

type ObservationState string

const (
	ObservationUnobserved ObservationState = "unobserved"
	ObservationFresh      ObservationState = "fresh"
	ObservationBlocked    ObservationState = "blocked"
	ObservationExpired    ObservationState = "expired"
)

type CancelState string

const (
	CancelNone      CancelState = "none"
	CancelRequested CancelState = "requested"
	CancelAccepted  CancelState = "accepted"
	CancelRejected  CancelState = "rejected"
)

type ArtifactState string

const (
	ArtifactNone      ArtifactState = "none"
	ArtifactAvailable ArtifactState = "available"
	ArtifactExpired   ArtifactState = "expired"
)

// Artifact is a safe reference to the original result. It never contains the
// result bytes or a URL.
type Artifact struct {
	Reference string
	MediaType string
	Size      int64
	SHA256    Digest
	ExpiresAt time.Time
}

func (a Artifact) valid() error {
	if !validSafeReference(a.Reference, maxReferenceLength) || strings.Contains(a.Reference, "://") {
		return ErrInvalidInput
	}
	if !validMediaType(a.MediaType) {
		return ErrInvalidInput
	}
	if a.Size <= 0 || a.Size > maxArtifactBytes {
		return ErrArtifactTooLarge
	}
	if err := validateDigest(a.SHA256); err != nil {
		return err
	}
	if !validUTC(a.ExpiresAt) {
		return ErrInvalidInput
	}
	return nil
}

func validMediaType(raw string) bool {
	if len(raw) == 0 || len(raw) > maxMediaTypeLength || raw != strings.ToLower(raw) {
		return false
	}
	parts := strings.Split(raw, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		for i := 0; i < len(part); i++ {
			c := part[i]
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$&^_.+-", rune(c)) {
				continue
			}
			return false
		}
	}
	return true
}

// DownloadGrant is a short-lived, task-bound authorization proof created by
// the outer authorization layer or IssueDownloadGrant. It is not a URL.
type DownloadGrant struct {
	GrantID        ID
	TaskID         ID
	TenantID       ID
	SubjectID      ID
	ArtifactRef    string
	ArtifactSHA256 Digest
	MaxBytes       int64
	ExpiresAt      time.Time
}

func (g DownloadGrant) valid() error {
	for _, id := range []ID{g.GrantID, g.TaskID, g.TenantID, g.SubjectID} {
		if err := validateID(id); err != nil {
			return err
		}
	}
	if !validSafeReference(g.ArtifactRef, maxReferenceLength) {
		return ErrInvalidInput
	}
	if err := validateDigest(g.ArtifactSHA256); err != nil {
		return err
	}
	if g.MaxBytes <= 0 || g.MaxBytes > maxArtifactBytes {
		return ErrArtifactTooLarge
	}
	if !validUTC(g.ExpiresAt) {
		return ErrInvalidInput
	}
	return nil
}

func wrapInvalid(err error) error {
	if err == nil {
		return ErrInvalidInput
	}
	return fmt.Errorf("%w: %v", ErrInvalidInput, err)
}
