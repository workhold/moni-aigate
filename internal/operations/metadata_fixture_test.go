package operations

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

// operationMetadataRequest births one complete current request through the
// actual APP billing path, so its snapshot, bounds, reservation and wallet
// source facts are production rows rather than hand-written metadata.
func operationMetadataRequest(t *testing.T, f *operationFixture, clientIP string) string {
	t.Helper()
	fundingApproveCredit(t, f, uuid.NewString())
	request := uuid.NewString()
	digest := sha256.Sum256([]byte(request))
	_, err := billing.New(f.app).Reserve(f.ctx, billing.ReserveInput{
		RouteRevision:   1,
		Principal:       billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key, ClientIP: clientIP},
		RequestID:       request,
		ModelID:         f.model,
		IdempotencyKey:  uuid.NewString(),
		RequestHMAC:     hex.EncodeToString(digest[:]),
		InputUpperBound: 100,
		OutputLimit:     20,
		RequestDeadline: time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal("reserve metadata fixture request:", err)
	}
	return request
}
