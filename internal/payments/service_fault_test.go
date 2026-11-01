package payments

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A local wire proxy suppresses the server's actual successful COMMIT reply.
// It does not synthesize an error before COMMIT or guess whether money moved.
func serviceCommitLossPool(t *testing.T, f *serviceFixture) (*pgxpool.Pool, *atomic.Bool, *atomic.Int32) {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	armed := &atomic.Bool{}
	lost := &atomic.Int32{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	conns := map[net.Conn]bool{}
	track := func(c net.Conn) { mu.Lock(); conns[c] = true; mu.Unlock() }
	go func() {
		for {
			client, e := listener.Accept()
			if e != nil {
				return
			}
			track(client)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer client.Close()
				cfg := f.app.Config().ConnConfig
				upstream, e := net.DialTimeout("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), time.Second)
				if e != nil {
					return
				}
				track(upstream)
				defer upstream.Close()
				go func() { _, _ = io.Copy(upstream, client); upstream.Close() }()
				for {
					header := make([]byte, 5)
					if _, e = io.ReadFull(upstream, header); e != nil {
						return
					}
					length := binary.BigEndian.Uint32(header[1:])
					if length < 4 || length > 16<<20 {
						return
					}
					body := make([]byte, int(length)-4)
					if _, e = io.ReadFull(upstream, body); e != nil {
						return
					}
					if header[0] == 'C' && string(body) == "COMMIT\x00" && armed.CompareAndSwap(true, false) {
						lost.Add(1)
						return
					}
					if _, e = client.Write(append(header, body...)); e != nil {
						return
					}
				}
			}()
		}
	}()
	cfg := f.app.Config()
	addr := listener.Addr().(*net.TCPAddr)
	cfg.ConnConfig.Host = "127.0.0.1"
	cfg.ConnConfig.Port = uint16(addr.Port)
	cfg.MaxConns = 1
	p, e := pgxpool.NewWithConfig(f.ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		p.Close()
		listener.Close()
		mu.Lock()
		for c := range conns {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return p, armed, lost
}

func TestPostgresPaymentServiceLostCommitNeverReplaysOrAcknowledges(t *testing.T) {
	f := paymentServiceFixture(t)
	pool, armed, lost := serviceCommitLossPool(t, f)
	proxied := &Service{pool: pool, runtime: &paymentRuntime{pool: pool, options: f.appOptions}}
	in := CreateOrderInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version}
	armed.Store(true)
	result, e := proxied.CreateOrder(f.ctx, in)
	if !errors.Is(e, ErrPaymentsOutcomeUnknown) || result.Created || result.Order != nil || result.Receipt.OperationID != "" || lost.Load() != 1 {
		t.Fatal("uncertain order exposed result/retried", e, result, lost.Load())
	}
	persisted, e := f.service.GetWalletOrder(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, in.OperationID)
	if e != nil || persisted.ID != in.OperationID {
		t.Fatal("committed order disappeared", e)
	}
	replay, e := proxied.CreateOrder(f.ctx, in)
	if e != nil || replay.Created || replay.Order == nil || replay.Order.ID != in.OperationID {
		t.Fatal("original operation did not recover", e, replay)
	}
	input, _ := serviceSignedNotification(t, f, "event:lost-commit", in.OperationID, "100")
	armed.Store(true)
	ack, first, e := proxied.ReceiveNotification(f.ctx, input)
	if !errors.Is(e, ErrPaymentsOutcomeUnknown) || first || ack.ReceiptID != "" || lost.Load() != 2 {
		t.Fatal("uncertain notification ACKed/retried", e, ack, lost.Load())
	}
	recovered, first, e := f.service.ReceiveNotification(f.ctx, input)
	if e != nil || first || !paymentID(recovered.ReceiptID) {
		t.Fatal("lost ACK was not durable", e, recovered)
	}
	var orders, outboxes, inboxes, evidences, processing, funding int
	if e = f.app.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM payment_orders),(SELECT count(*) FROM payment_command_outbox WHERE kind='checkout'),(SELECT count(*) FROM payment_inbox),(SELECT count(*) FROM payment_verified_evidence),(SELECT count(*) FROM payment_evidence_processing),(SELECT count(*) FROM funding_receipts)`).Scan(&orders, &outboxes, &inboxes, &evidences, &processing, &funding); e != nil || orders != 1 || outboxes != 1 || inboxes != 1 || evidences != 1 || processing != 1 || funding != 0 {
		t.Fatal("uncertainty duplicated graph", e, orders, outboxes, inboxes, evidences, processing, funding)
	}
}
func TestPostgresPaymentServiceKnownCommitAbortRetryBoundary(t *testing.T) {
	for _, code := range []string{"40001", "40P01", "23514"} {
		t.Run(code, func(t *testing.T) {
			f := paymentServiceFixture(t)
			role := f.app.Config().ConnConfig.User
			serviceExec(t, f.owner, f.ctx, `CREATE SEQUENCE service_commit_attempt; CREATE FUNCTION service_commit_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='payment.order.create' AND nextval('service_commit_attempt')<3 THEN RAISE EXCEPTION 'private fixture detail' USING ERRCODE='`+code+`'; END IF; RETURN NULL; END $$; CREATE CONSTRAINT TRIGGER service_commit_fault AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION service_commit_fault()`)
			serviceExec(t, f.owner, f.ctx, `GRANT USAGE,SELECT ON SEQUENCE service_commit_attempt TO `+pgx.Identifier{role}.Sanitize())
			result, e := f.service.CreateOrder(f.ctx, CreateOrderInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version})
			var attempts, orders, commands int
			qe := f.app.QueryRow(f.ctx, `SELECT (SELECT last_value FROM service_commit_attempt),(SELECT count(*) FROM payment_orders),(SELECT count(*) FROM commerce_commands WHERE kind='payment.order.create')`).Scan(&attempts, &orders, &commands)
			if qe != nil {
				t.Fatal(qe)
			}
			if code == "23514" {
				if !errors.Is(e, ErrPaymentsUnavailable) || result.Created || result.Order != nil || attempts != 1 || orders != 0 || commands != 0 {
					t.Fatal("definite nonretryable abort retried/left effects", e, attempts, orders, commands)
				}
				return
			}
			if e != nil || !result.Created || attempts != 3 || orders != 1 || commands != 1 {
				t.Fatal("definite abort was not bounded exact retry", e, attempts, orders, commands)
			}
		})
	}
}
