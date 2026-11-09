package server

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

type mediaExecutionRefs struct {
	enabled       bool
	existing      bool
	state         string
	tenantID      string
	requestID     string
	reservationID string
	attemptID     string
	operation     string
}

// reserveMediaForRequest is enabled for the real Server created with a
// database pool. Pure protocol tests construct a server without a pool and
// exercise only the provider/media contract.
func (s *Server) reserveMediaForRequest(ctx context.Context, tenant, user, key, requestID, idempotency, operation, model, profile string, digest [32]byte, quantity int64) (mediaExecutionRefs, error) {
	refs := mediaExecutionRefs{tenantID: tenant, requestID: requestID, operation: operation}
	if s.Billing == nil || s.Pool == nil {
		return refs, nil
	}
	if _, err := uuid.Parse(requestID); err != nil {
		requestID = uuid.NewString()
		refs.requestID = requestID
	}
	refs.attemptID = uuid.NewString()
	refs.enabled = true
	reservation, err := s.Billing.ReserveMedia(ctx, billing.MediaReserveInput{
		TenantID: tenant, UserID: user, KeyID: key, RequestID: requestID, AttemptID: refs.attemptID,
		Operation: operation, ModelKey: model, ProfileVersion: profile, IdempotencyKey: idempotency,
		RequestDigest: digest[:], QuantityBound: quantity, Deadline: time.Now().UTC().Add(10 * time.Minute),
	})
	if err == nil {
		refs.existing = reservation.Existing
		refs.state = reservation.AttemptState
		refs.reservationID = reservation.ID
		if reservation.AttemptID != "" {
			refs.attemptID = reservation.AttemptID
		}
	}
	return refs, err
}

func (s *Server) finalizeMediaRequest(ctx context.Context, refs mediaExecutionRefs, outcome, usageState string, quantity int64) error {
	if !refs.enabled {
		return nil
	}
	digest := sha256.Sum256([]byte(refs.requestID + ":usage"))
	_, err := s.Billing.FinalizeMedia(ctx, billing.MediaFinalizationInput{
		TenantID: refs.tenantID, RequestID: refs.requestID, AttemptID: refs.attemptID,
		Source: "adapter", SourceEventID: hexDigest(digest), Operation: refs.operation,
		Outcome: outcome, UsageState: usageState, Quantity: quantity,
	})
	return err
}

func hexDigest(value [32]byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(value)*2)
	for i, b := range value {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&15]
	}
	return string(out)
}
