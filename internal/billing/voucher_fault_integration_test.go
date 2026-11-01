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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/security"
)

// A local wire proxy suppresses the server's actual successful COMMIT reply.
// It does not synthesize an error before COMMIT or guess whether money moved.
func voucherCommitLossPool(t *testing.T, f *fixture) (*pgxpool.Pool, *atomic.Bool, *atomic.Int32) {
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
				cfg := f.pool.Config().ConnConfig
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
	cfg := f.pool.Config()
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

func TestPostgresVoucherLostCommitReplyHidesCodesAndNeverRepeatsMoney(t *testing.T) {
	f := fundingFixture(t)
	direct := voucherTestService(t, f)
	p, armed, lost := voucherCommitLossPool(t, f)
	v, e := NewVoucherService(f.ctx, New(p), direct.options)
	if e != nil {
		t.Fatal(e)
	}
	in := voucherTestInput(f, 2, 19)
	armed.Store(true)
	issued, e := v.CreateVoucherBatch(f.ctx, in)
	voucherTestError(t, e, "commerce_outcome_unknown")
	if issued.Created || issued.SecretsAvailable || issued.Batch != nil || len(issued.Codes) != 0 || lost.Load() != 1 {
		t.Fatal("lost COMMIT reply exposed an issuance result")
	}
	receipt, e := direct.GetPlatformCommerceOperation(f.ctx, f.operator, in.Command.OperationID)
	if e != nil || receipt.Status != "accepted" || receipt.ResourceID == nil {
		t.Fatal("committed issuance unavailable through original operation", e)
	}
	replay, e := v.CreateVoucherBatch(f.ctx, in)
	if e != nil || replay.Created || replay.SecretsAvailable || len(replay.Codes) != 0 || replay.Batch == nil || replay.Batch.ID != *receipt.ResourceID {
		t.Fatal("unknown issuance replay regenerated codes", e)
	}
	// A distinct batch supplies a known code for the same actual fault in funding.
	known := voucherTestCreate(t, f, direct, 1, 7, true)
	redeem := voucherTestRedeem(f, known.Codes[0].Code)
	armed.Store(true)
	r, e := v.RedeemVoucher(f.ctx, redeem)
	voucherTestError(t, e, "commerce_outcome_unknown")
	if r.OperationID != "" || r.Amount != nil || lost.Load() != 2 {
		t.Fatal("unknown funding returned an accepted result")
	}
	recovered, e := direct.GetWalletCommerceOperation(f.ctx, VoucherReader{f.user, f.tenant}, redeem.Command.OperationID)
	if e != nil || recovered.Status != "accepted" {
		t.Fatal("committed redemption was lost", e)
	}
	for i := 0; i < 2; i++ {
		repeat, e := v.RedeemVoucher(f.ctx, redeem)
		if e != nil || repeat.Status != "accepted" || *repeat.ResourceID != *recovered.ResourceID {
			t.Fatal("original operation did not recover exact receipt", e)
		}
	}
	var batches, vouchers, journals int
	e = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM voucher_batches),(SELECT count(*) FROM vouchers),(SELECT count(*) FROM journals WHERE kind='voucher_grant')`).Scan(&batches, &vouchers, &journals)
	if e != nil || batches != 2 || vouchers != 3 || journals != 1 {
		t.Fatal("unknown response repeated issuance or funding", batches, vouchers, journals, e)
	}
	f.wallet(t, 1007, 0)
	voucherTestFunding(t, f)
}

func TestPostgresVoucherKnownAbortedCommitRetriesAreBoundedAndHideFailedCodes(t *testing.T) {
	for _, code := range []string{"40001", "40P01", "23514"} {
		t.Run(code, func(t *testing.T) {
			f := fundingFixture(t)
			v := voucherTestService(t, f)
			if _, e := f.pool.Exec(f.ctx, `CREATE SEQUENCE voucher_attempt;
CREATE FUNCTION voucher_retry_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.action='voucher.batch.created' AND nextval('voucher_attempt')<3 THEN RAISE EXCEPTION 'safe fixture detail' USING ERRCODE='`+code+`'; END IF; RETURN NULL; END $$;
CREATE CONSTRAINT TRIGGER voucher_retry_fixture AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_retry_fixture()`); e != nil {
				t.Fatal(e)
			}
			in := voucherTestInput(f, 3, 5)
			r, e := v.CreateVoucherBatch(f.ctx, in)
			var attempts, batches, vouchers, commands int
			if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT last_value FROM voucher_attempt),(SELECT count(*) FROM voucher_batches),(SELECT count(*) FROM vouchers),(SELECT count(*) FROM commerce_commands)`).Scan(&attempts, &batches, &vouchers, &commands); err != nil {
				t.Fatal(err)
			}
			if code == "23514" {
				voucherTestError(t, e, "commerce_unavailable")
				if attempts != 1 || batches != 0 || vouchers != 0 || commands != 0 || r.Created || len(r.Codes) != 0 {
					t.Fatal("known nonretryable commit rejection retained/retried facts")
				}
				return
			}
			if e != nil || !r.Created || len(r.Codes) != 3 || attempts != 3 || batches != 1 || vouchers != 3 || commands != 1 {
				t.Fatal("commit retry duplicated or lost final-attempt facts", attempts, batches, vouchers, commands, e)
			}
			if _, e = f.pool.Exec(f.ctx, `DROP TRIGGER voucher_retry_fixture ON audit_events; DROP FUNCTION voucher_retry_fixture(); ALTER SEQUENCE voucher_attempt RESTART WITH 1; CREATE FUNCTION voucher_retry_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='voucher.batch.created' THEN PERFORM nextval('voucher_attempt'); RAISE EXCEPTION 'bounded fixture' USING ERRCODE='`+code+`'; END IF; RETURN NULL; END $$; CREATE CONSTRAINT TRIGGER voucher_retry_fixture AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_retry_fixture()`); e != nil {
				t.Fatal(e)
			}
			failed, e := v.CreateVoucherBatch(f.ctx, voucherTestInput(f, 2, 1))
			voucherTestError(t, e, "commerce_unavailable")
			if failed.Created || failed.SecretsAvailable || len(failed.Codes) != 0 || failed.Batch != nil {
				t.Fatal("failed attempt secrets escaped")
			}
			if e = f.pool.QueryRow(f.ctx, `SELECT (SELECT last_value FROM voucher_attempt),(SELECT count(*) FROM voucher_batches),(SELECT count(*) FROM vouchers),(SELECT count(*) FROM commerce_commands)`).Scan(&attempts, &batches, &vouchers, &commands); e != nil || attempts != 3 || batches != 1 || vouchers != 3 || commands != 1 {
				t.Fatal("retry limit or rollback failed", attempts, batches, vouchers, commands, e)
			}
		})
	}
}

func TestPostgresVoucherAccountLockWaitRechecksDatabaseExpiry(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	batch, id := uuid.NewString(), uuid.NewString()
	secret := security.Random(32)
	digest, e := v.options.Keys.CodeDigest("v1", id, secret)
	if e != nil {
		t.Fatal(e)
	}
	var expires time.Time
	// A historical valid batch, created two hours ago and activated now. No
	// production guard is disabled or changed to manufacture the short expiry.
	tx, e := f.pool.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	if e = tx.QueryRow(f.ctx, `SELECT clock_timestamp()+interval '1200 milliseconds'`).Scan(&expires); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, `INSERT INTO voucher_batches(id,face_amount,count,expires_at,purpose_ref,reason,maker,created_at) VALUES($1,9,1,$2,'grant:historical','historical expiry fixture',$3,$2::timestamptz-interval '2 hours')`, batch, expires, f.operator); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, `INSERT INTO vouchers(id,batch_id,code_digest,digest_key_version) VALUES($1,$2,$3,'v1')`, id, batch, digest); e != nil {
		t.Fatal(e)
	}
	if e = fundingPlatformCommand(f.ctx, tx, uuid.NewString(), f.operator, "voucher.batch.create", batch); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, `UPDATE voucher_batches SET state='active',approver=$2,activated_at=clock_timestamp() WHERE id=$1`, batch, f.approver); e != nil {
		t.Fatal(e)
	}
	if e = fundingPlatformCommand(f.ctx, tx, uuid.NewString(), f.approver, "voucher.batch.approve", batch); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	blocker, e := f.pool.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Rollback(f.ctx)
	var pid int
	if e = blocker.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid); e != nil {
		t.Fatal(e)
	}
	if _, e = blocker.Exec(f.ctx, `SELECT id FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE' FOR UPDATE`, f.tenant); e != nil {
		t.Fatal(e)
	}
	type result struct {
		r VoucherOperationReceipt
		e error
	}
	done := make(chan result, 1)
	go func() {
		r, e := v.RedeemVoucher(f.ctx, voucherTestRedeem(f, "mav1."+id+"."+secret))
		done <- result{r, e}
	}()
	locked := false
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		if e = f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))`, pid).Scan(&locked); e != nil {
			t.Fatal(e)
		}
		if locked {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !locked {
		t.Fatal("redemption never waited for the actual account lock")
	}
	for {
		var expired bool
		if e = f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()>$1`, expires).Scan(&expired); e != nil {
			t.Fatal(e)
		}
		if expired {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e = blocker.Rollback(f.ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case got := <-done:
		voucherTestRejected(t, got.r, got.e, "voucher_unavailable")
	case <-time.After(time.Second):
		t.Fatal("redemption did not complete after account unlock")
	}
	f.wallet(t, 1000, 0)
	voucherTestFunding(t, f)
}
