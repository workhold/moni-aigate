package paymentsandboxqa_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/paymentruntime"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
)

func TestRealAttestedEnvironment(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PG required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	root, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e, err := paymentsandboxqa.Create(ctx, dsn, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if x := e.Close(c); x != nil {
			t.Error(x)
		}
	})
	for _, c := range []paymentruntime.Component{paymentruntime.Mock, paymentruntime.Receiver} {
		path := e.MockManifestPath
		if c == paymentruntime.Receiver {
			path = e.ReceiverManifestPath
		}
		cfg, x := paymentruntime.Load(ctx, path, c)
		if x != nil {
			t.Fatalf("%s load: %v", c, x)
		}
		rt, x := paymentruntime.Open(ctx, cfg)
		if x != nil {
			t.Fatalf("%s open: %v", c, x)
		}
		if rt.Check(ctx) != nil {
			t.Error("guard failed")
		}
		if x = rt.Close(ctx); x != nil {
			t.Error(x)
		}
	}
	if err = e.VerifyFundsUnchanged(ctx); err != nil {
		t.Fatal(err)
	}
}
