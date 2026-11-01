package payments

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func TestProcessorPrivateMutationAdapterHintCannotCredit(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	f.order(t)
	var requests atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/checkout" || r.Header.Get("Authorization") == "" {
			http.Error(w, "invalid", 400)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		checkout, err := p.ParseCheckout(raw)
		if err != nil {
			t.Error(err)
			return
		}
		revision := "2"
		reply := p.CommandReply{SchemaVersion: 1, EnvironmentID: checkout.EnvironmentID, MerchantID: checkout.MerchantID, MerchantOrderID: checkout.MerchantOrderID, CommandID: checkout.CommandID, CommandKind: "checkout", ParameterDigest: processorDigest(raw), CommandState: "completed", ProviderOrderRevision: &revision, Result: &p.CommandResult{Status: "captured", Capture: &p.CaptureFact{ProviderTransactionID: "hint:" + uuid.NewString(), Amount: checkout.Amount, Currency: "CNY", OccurredAt: processorTime(processorNow(t, f)), ProviderOrderRevision: revision}}}
		canonical, err := p.CanonicalCommandReply(reply)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(canonical)
	}))
	defer mock.Close()
	processor.runtime.options.proof.APIOrigin = mock.URL
	lease, ok, err := processor.ClaimMutation(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	work, err := processor.PrepareMutation(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	response, err := processor.sendMutation(f.ctx, work)
	if err != nil {
		t.Fatal(err)
	}
	if err = processor.RecordMutation(f.ctx, lease, response); err != nil {
		t.Fatal(err)
	}
	var supervisors, captures int
	if err = f.worker.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM payment_command_outbox WHERE kind='query'),(SELECT count(*) FROM payment_captures)`).Scan(&supervisors, &captures); err != nil || supervisors != 1 || captures != 0 || requests.Load() != 1 {
		t.Fatal(supervisors, captures, requests.Load(), err)
	}
}
func TestProcessorPrivateAdapterRejectsRedirectWithoutAlternateRequest(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	f.order(t)
	var alternate atomic.Int32
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { alternate.Add(1); w.WriteHeader(200) }))
	defer second.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL, http.StatusTemporaryRedirect)
	}))
	defer first.Close()
	processor.runtime.options.proof.APIOrigin = first.URL
	lease, ok, err := processor.ClaimMutation(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	work, err := processor.PrepareMutation(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	response, err := processor.sendMutation(f.ctx, work)
	if err != nil || response.failure != "network" {
		t.Fatal(response, err)
	}
	if err = processor.RecordMutation(f.ctx, lease, response); err != nil {
		t.Fatal(err)
	}
	if alternate.Load() != 0 {
		t.Fatal("redirect target received request")
	}
}
func TestProcessorCloseCancelsAndJoinsNetworkBeforeBorrowedPoolRelease(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	f.order(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer mock.Close()
	defer close(release)
	processor.runtime.options.proof.APIOrigin = mock.URL
	lease, ok, err := processor.ClaimMutation(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	work, err := processor.PrepareMutation(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := processor.sendMutation(f.ctx, work); result <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("network not entered")
	}
	stop, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = processor.Close(stop); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err = processor.ClaimQuery(f.ctx); !errors.Is(err, ErrPaymentsLifecycleClosed) {
		t.Fatal(err)
	}
	var one int
	if err = f.worker.QueryRow(f.ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatal("borrowed worker pool closed", err)
	}
}
