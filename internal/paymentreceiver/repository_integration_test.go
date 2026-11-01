package paymentreceiver

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/paymentruntime"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
)

func TestPostgresReceiverDurableSignedReceipts(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PG required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	root, _ := filepath.Abs(t.TempDir())
	env, err := paymentsandboxqa.Create(ctx, dsn, root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := env.Close(c); err != nil {
			t.Error(err)
		}
	}()
	cfg, err := paymentruntime.Load(ctx, env.ReceiverManifestPath, paymentruntime.Receiver)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := paymentruntime.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(context.Background())
	repo, err := newRepository(rt)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := env.Keyring()
	if err != nil {
		t.Fatal(err)
	}
	transaction := "txn:" + uuid.NewString()
	n := paymentproto.Notification{SchemaVersion: 1, EventID: "evt:" + uuid.NewString(), EventType: "capture.confirmed", EnvironmentID: env.Protocol.ID, MerchantID: env.Protocol.MerchantID, OrderID: uuid.NewString(), ProviderTransactionID: &transaction, Amount: "1000", Currency: "CNY", OccurredAt: time.Now().UTC().Add(-time.Second).Format(utc6), ProviderOrderRevision: "2"}
	raw, err := paymentproto.CanonicalNotification(n)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(raw []byte) paymentproto.SignedHeaders {
		t.Helper()
		h, err := keys.SignNotification(env.Protocol, paymentproto.SignedHeaders{DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(time.Now().Unix(), 10)}, raw)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	headers := sign(raw)
	const total = 50
	var wg sync.WaitGroup
	outputs := make(chan ReceiveOutcome, total)
	failures := make(chan error, total)
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := repo.Receive(ctx, headers, raw)
			if err != nil {
				failures <- err
				return
			}
			outputs <- out
		}()
	}
	wg.Wait()
	close(outputs)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	var first ReceiptData
	newCount, count := 0, 0
	for out := range outputs {
		count++
		if first.ReceiptID == "" {
			first = out.Receipt
		}
		if out.Receipt != first {
			t.Error("receipt changed across duplicate")
		}
		if out.Status == ReceiveNew {
			newCount++
		}
	}
	if count != total || newCount != 1 {
		t.Fatalf("outcomes %d new %d", count, newCount)
	}
	// A different exact wire representation must be independently signed. Its
	// verified canonical business identity still yields the original receipt.
	spaced := []byte(" \n" + string(raw) + "\n")
	dup, err := repo.Receive(ctx, sign(spaced), spaced)
	if err != nil || dup.Status != ReceiveDuplicate || dup.Receipt != first {
		t.Fatal("canonical duplicate receipt failed", ErrorCode(err))
	}
	n.Amount = "1001"
	other, err := paymentproto.CanonicalNotification(n)
	if err != nil {
		t.Fatal(err)
	}
	conflictHeaders := sign(other)
	for i := 0; i < 2; i++ {
		out, err := repo.Receive(ctx, conflictHeaders, other)
		if err != nil || out.Status != ReceiveConflict {
			t.Fatal("conflict not committed", ErrorCode(err))
		}
	}
	bad := headers
	bad.Signature = strings.Repeat("0", 64)
	if _, err = repo.Receive(ctx, bad, raw); ErrorCode(err) != "payment_signature_invalid" {
		t.Fatal("signature failure code", ErrorCode(err))
	}
	var inbox, receipts, conflicts int
	err = rt.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM receiver_inbox),(SELECT count(*) FROM receiver_receipts),(SELECT count(*) FROM receiver_conflicts)`).Scan(&inbox, &receipts, &conflicts)
	if err != nil || inbox != 1 || receipts != 1 || conflicts != 1 {
		t.Fatalf("stored counts %d %d %d queryOK=%v", inbox, receipts, conflicts, err == nil)
	}
	if err = env.VerifyFundsUnchanged(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestReceiverLockDomainGolden(t *testing.T) {
	// Frozen UTF-8/NUL/domain/hash contract, independently computed with Python.
	const environment = "11111111-1111-4111-8111-111111111111"
	const target = "merchant:test\x00evt:test"
	if got := receiverLockKey(environment, target); got != 1594334755 {
		t.Fatal("lock domain differs from independent golden")
	}
}
