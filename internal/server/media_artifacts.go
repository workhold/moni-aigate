package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/identity"
)

type persistedMediaArtifact struct {
	RequestID string
	MediaType string
	Body      []byte
}

func (s *Server) storeMediaArtifact(ctx context.Context, refs mediaExecutionRefs, operation, mediaType string, body []byte) error {
	if !refs.enabled {
		return nil
	}
	if len(body) == 0 {
		return errors.New("empty media result")
	}
	digest := sha256.Sum256(body)
	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	_, err := s.Pool.Exec(ctx, `
INSERT INTO media_result_artifacts(tenant_id,request_id,attempt_id,operation,media_type,body,sha256,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (tenant_id,request_id) DO NOTHING`,
		refs.tenantID, refs.requestID, refs.attemptID, operation, mediaType, body, digest[:], expiresAt)
	if err != nil {
		return err
	}
	var storedType string
	var storedDigest []byte
	if err = s.Pool.QueryRow(ctx, `SELECT media_type,sha256 FROM media_result_artifacts WHERE tenant_id=$1 AND request_id=$2`, refs.tenantID, refs.requestID).Scan(&storedType, &storedDigest); err != nil {
		return err
	}
	if storedType != mediaType || !bytes.Equal(storedDigest, digest[:]) {
		return errors.New("media result identity conflict")
	}
	return nil
}

func (s *Server) loadMediaArtifactByRequest(ctx context.Context, p identity.Principal, requestID string) (persistedMediaArtifact, bool, error) {
	if s.Pool == nil {
		return persistedMediaArtifact{}, false, nil
	}
	var artifact persistedMediaArtifact
	var body []byte
	err := s.Pool.QueryRow(ctx, `
SELECT a.request_id,a.media_type,a.body
FROM media_result_artifacts a
JOIN media_requests r ON r.tenant_id=a.tenant_id AND r.id=a.request_id
WHERE a.tenant_id=$1 AND r.owner_id=$2 AND r.key_id=$3 AND a.request_id=$4 AND a.expires_at>clock_timestamp()`,
		p.TenantID, p.UserID, p.KeyID, requestID).Scan(&artifact.RequestID, &artifact.MediaType, &body)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return persistedMediaArtifact{}, false, nil
		}
		return persistedMediaArtifact{}, false, err
	}
	artifact.Body = append([]byte(nil), body...)
	return artifact, true, nil
}

func (s *Server) loadMediaArtifactByIdempotency(ctx context.Context, p identity.Principal, operation, idempotency string, expectedDigest [32]byte) (persistedMediaArtifact, bool, error) {
	if s.Pool == nil {
		return persistedMediaArtifact{}, false, nil
	}
	var artifact persistedMediaArtifact
	var body []byte
	var requestDigest []byte
	err := s.Pool.QueryRow(ctx, `
SELECT a.request_id,a.media_type,a.body,r.request_digest
FROM media_result_artifacts a
JOIN media_requests r ON r.tenant_id=a.tenant_id AND r.id=a.request_id
WHERE a.tenant_id=$1 AND r.owner_id=$2 AND r.key_id=$3 AND r.operation=$4 AND r.idempotency_key=$5 AND a.expires_at>clock_timestamp()`,
		p.TenantID, p.UserID, p.KeyID, operation, idempotency).Scan(&artifact.RequestID, &artifact.MediaType, &body, &requestDigest)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return persistedMediaArtifact{}, false, nil
		}
		return persistedMediaArtifact{}, false, err
	}
	if !bytes.Equal(requestDigest, expectedDigest[:]) {
		return persistedMediaArtifact{}, false, errors.New("media idempotency digest conflict")
	}
	artifact.Body = append([]byte(nil), body...)
	return artifact, true, nil
}
