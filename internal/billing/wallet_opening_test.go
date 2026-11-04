package billing

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWalletOpeningRequiresExplicitOfflineInput(t *testing.T) {
	valid := WalletOpeningInput{TenantID: uuid.NewString(), ActorID: uuid.NewString(), ExternalRef: "opening:approved-001", Amount: 19, MaintenanceMode: true}
	if !validWalletOpening(valid) {
		t.Fatal("valid offline opening rejected")
	}
	for _, change := range []func(*WalletOpeningInput){
		func(in *WalletOpeningInput) { in.MaintenanceMode = false },
		func(in *WalletOpeningInput) { in.TenantID = uuid.Nil.String() },
		func(in *WalletOpeningInput) { in.ActorID = "not-a-user" },
		func(in *WalletOpeningInput) { in.ExternalRef = "https://private.invalid/proof?token=secret" },
		func(in *WalletOpeningInput) { in.ExternalRef = "sk-private-credential" },
		func(in *WalletOpeningInput) { in.Amount = 0 },
		func(in *WalletOpeningInput) { in.Amount = -1 },
	} {
		in := valid
		change(&in)
		if validWalletOpening(in) {
			t.Fatal("unsafe opening input accepted", in.ExternalRef)
		}
		out, err := New(nil).OpenWallet(context.Background(), in)
		if err == nil || out.OpeningID != "" || out.Receipt.ID != "" {
			t.Fatal("invalid opening reached a result", err)
		}
	}
	var absent *Store
	if out, err := absent.OpenWallet(context.Background(), valid); err == nil || out.OpeningID != "" {
		t.Fatal("nil store accepted")
	}
}

func TestWalletOpeningBoundsConnectionAcquisition(t *testing.T) {
	// A stalled connection constructor must not make this maintenance entry
	// point depend on the caller remembering to set a deadline. This dialer
	// never contacts PostgreSQL and is released before the pool is closed.
	cfg, err := pgxpool.ParseConfig("postgres://unused@127.0.0.1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	cfg.MinConns = 0
	cfg.MaxConns = 1
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	cfg.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return nil, errors.New("connection constructor released")
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	defer close(release)
	in := WalletOpeningInput{TenantID: uuid.NewString(), ActorID: uuid.NewString(), ExternalRef: "opening:bounded-acquire", Amount: 19, MaintenanceMode: true}
	type outcome struct {
		result WalletOpeningResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, e := New(pool).OpenWallet(context.Background(), in)
		done <- outcome{result, e}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("opening did not attempt to acquire its dedicated connection")
	}
	select {
	case out := <-done:
		var failure *Error
		if !errors.As(out.err, &failure) || failure.Code != "wallet_opening_failed" || out.result.OpeningID != "" || out.result.Receipt.ID != "" {
			t.Fatal("connection failure did not return a safe empty result", out.err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("opening waited beyond its own acquisition budget")
	}
}
