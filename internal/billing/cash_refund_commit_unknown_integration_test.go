package billing

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// cashRefundCommitLossPool suppresses the real successful COMMIT reply. It never
// fabricates a server error or guesses whether the financial facts committed.
func cashRefundCommitLossPool(t *testing.T, f *fixture) (*pgxpool.Pool, *atomic.Bool, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	armed := &atomic.Bool{}
	lost := &atomic.Int32{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	track := func(c net.Conn) { mu.Lock(); connections[c] = true; mu.Unlock() }
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			track(client)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer client.Close()
				cfg := f.pool.Config().ConnConfig
				upstream, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), time.Second)
				if err != nil {
					return
				}
				track(upstream)
				defer upstream.Close()
				go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
				for {
					header := make([]byte, 5)
					if _, err = io.ReadFull(upstream, header); err != nil {
						return
					}
					length := binary.BigEndian.Uint32(header[1:])
					if length < 4 || length > 16<<20 {
						return
					}
					body := make([]byte, int(length)-4)
					if _, err = io.ReadFull(upstream, body); err != nil {
						return
					}
					if header[0] == 'C' && string(body) == "COMMIT\x00" && armed.CompareAndSwap(true, false) {
						lost.Add(1)
						return
					}
					if _, err = client.Write(append(header, body...)); err != nil {
						return
					}
				}
			}()
		}
	}()
	cfg := f.pool.Config()
	addr := listener.Addr().(*net.TCPAddr)
	cfg.ConnConfig.Host = "127.0.0.1"
	cfg.ConnConfig.Port = uint16(addr.Port)
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_ = listener.Close()
		mu.Lock()
		for connection := range connections {
			_ = connection.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return pool, armed, lost
}

func TestPostgresCashRefundLostCommitReplyReadsBackWithoutReplay(t *testing.T) {
	t.Run("approval", func(t *testing.T) {
		f := fundingFixture(t)
		order := createCashRefundForTest(t, f, refundBucketOnlyLot(t, f), "cash-refund:unknown-approve")
		pool, armed, lost := cashRefundCommitLossPool(t, f)
		action := cashRefundActionForTest(order, f.approver)
		armed.Store(true)
		result, err := New(pool).ApproveCashRefund(f.ctx, action)
		assertCode(t, err, "cash_refund_commit_unknown")
		if result.ID != "" || lost.Load() != 1 {
			t.Fatalf("unknown approval returned success or suppressed the wrong reply: %+v lost=%d", result, lost.Load())
		}
		committed, err := f.store.CashRefund(f.ctx, order.TenantID, order.ID)
		if err != nil || committed.State != "approved" || committed.HoldJournalID == "" {
			t.Fatalf("committed approval was not readable: %+v %v", committed, err)
		}
		replayed := approveCashRefundForTest(t, f, order)
		if replayed.HoldJournalID != committed.HoldJournalID {
			t.Fatalf("readback/replay changed hold: %+v", replayed)
		}
		var events int
		if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_lot_events WHERE refund_order_id=$1`, order.ID).Scan(&events); err != nil || events != 1 {
			t.Fatalf("unknown approval replay wrote events: %d %v", events, err)
		}
	})
	t.Run("completion", func(t *testing.T) {
		f := fundingFixture(t)
		order := createCashRefundForTest(t, f, refundBucketOnlyLot(t, f), "cash-refund:unknown-complete")
		approved := approveCashRefundForTest(t, f, order)
		execute := cashRefundActionForTest(approved, f.operator)
		execute.TransferOrderRef = "transfer:unknown-complete"
		executed, err := f.store.ExecuteCashRefund(f.ctx, execute)
		if err != nil {
			t.Fatal(err)
		}
		pool, armed, lost := cashRefundCommitLossPool(t, f)
		action := cashRefundActionForTest(executed, f.approver)
		action.TransferProofRef = "transfer:unknown-proof"
		paid := FiatMoney(1000)
		action.PaidFiatAmount = &paid
		armed.Store(true)
		result, err := New(pool).CompleteCashRefund(f.ctx, action)
		assertCode(t, err, "cash_refund_commit_unknown")
		if result.ID != "" || lost.Load() != 1 {
			t.Fatalf("unknown completion returned success or suppressed the wrong reply: %+v lost=%d", result, lost.Load())
		}
		committed, err := f.store.CashRefundByBusinessRef(f.ctx, order.BusinessRef)
		if err != nil || committed.State != "completed" || committed.RetireJournalID == "" {
			t.Fatalf("committed completion was not readable: %+v %v", committed, err)
		}
		replayed, err := f.store.CompleteCashRefund(f.ctx, action)
		if err != nil || replayed.RetireJournalID != committed.RetireJournalID {
			t.Fatalf("readback/replay changed retire: %+v %v", replayed, err)
		}
		var events int
		if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_lot_events WHERE refund_order_id=$1`, order.ID).Scan(&events); err != nil || events != 2 {
			t.Fatalf("unknown completion replay wrote events: %d %v", events, err)
		}
		refundBucketBalanced(t, f)
	})
}
