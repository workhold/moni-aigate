package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/billing"
)

func TestAssemblyListenerMatchesProofAndDomainIntegrityFencesRunner(t *testing.T) {
	o := Options{valid: true, component: applicationComponent, proof: runtimeProof{AppListenOrigin: "http://127.0.0.1:59222"}}
	for _, address := range []string{"localhost:59222", "0.0.0.0:59222", "127.0.0.1:59223"} {
		if !errors.Is(ValidateApplicationAddress(o, address), ErrPaymentsConfiguration) {
			t.Fatal("listener escaped proof")
		}
	}
	if ValidateApplicationAddress(o, "127.0.0.1:59222") != nil {
		t.Fatal("pinned listener rejected")
	}
	if ValidateApplicationAddress(DisabledOptions(), "0.0.0.0:8080") != nil {
		t.Fatal("ordinary off acquired sandbox restriction")
	}
	for _, code := range []string{"funding_integrity", "invalid_funding_source"} {
		err := processorError(context.Background(), &billing.Error{Code: code})
		if !errors.Is(err, ErrPaymentsIntegrity) || !runnerFatal(err) {
			t.Fatal("funding graph contradiction treated as transient", err)
		}
	}
	if err := processorError(context.Background(), &billing.Error{Code: "amount_overflow"}); !errors.Is(err, ErrPaymentsUnavailable) {
		t.Fatal("unhandled billing error gained integrity or overflow authority", err)
	}
}
