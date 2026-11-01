package alerts

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
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
func alertCommitLossPool(t *testing.T, f *fixture) (*pgxpool.Pool, *atomic.Bool, *atomic.Int32) {
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

func TestPostgresAlertLostCommitReplyHidesReceiptAndNeverRecordsReverseFailure(t *testing.T) {
	f := integration(t)
	f.exec(`UPDATE channels SET quarantined=true WHERE id=$1`, f.channel)
	p, armed, lost := alertCommitLossPool(t, f)
	via, err := NewStore(p, RuntimeOptions{DeploymentEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	in := CreateRule{uuid.NewString(), ChannelQuarantined, "channel", f.channel, "commit loss", "enabled", EmptyParameters{}}
	armed.Store(true)
	out, err := via.Create(f.ctx, f.reader(), in)
	var ae *Error
	if !errors.As(err, &ae) || !ae.CommitUnknown || !reflect.DeepEqual(out, CommandReceipt{}) || lost.Load() != 1 {
		t.Fatal("unconfirmed command result escaped", out, err, lost.Load())
	}
	receipt, err := f.store.Operation(f.ctx, f.reader(), in.OperationID)
	if err != nil || receipt.Status != "applied" {
		t.Fatal(receipt, err)
	}
	replay, err := via.Create(f.ctx, f.reader(), in)
	if err != nil || !reflect.DeepEqual(replay, receipt) {
		t.Fatal(replay, receipt, err)
	}
	armed.Store(true)
	result, err := via.EvaluateDue(f.ctx)
	if !errors.As(err, &ae) || !ae.CommitUnknown || result.Outcome != "" || lost.Load() != 2 {
		t.Fatal("worker unknown commit was retried or reversed", result, err, lost.Load())
	}
	var commands, rules, episodes, events int
	var write int64
	var failure *string
	if err = f.app.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM alert_commands),(SELECT count(*) FROM alert_rules),(SELECT count(*) FROM alert_episodes),(SELECT count(*) FROM alert_episode_events),write_revision,last_error_code FROM alert_rule_runtime WHERE rule_id=$1`, *receipt.ResourceID).Scan(&commands, &rules, &episodes, &events, &write, &failure); err != nil || commands != 1 || rules != 1 || episodes != 1 || events != 1 || write != 2 || failure != nil {
		t.Fatal(commands, rules, episodes, events, write, failure, err)
	}
	result, err = via.EvaluateDue(f.ctx)
	if err != nil || result.Outcome != "idle" {
		t.Fatal("committed worker success repeated", result, err)
	}
}

func TestPostgresAlertKnownCommitRejectionRetryBound(t *testing.T) {
	for _, code := range []string{"40001", "40P01", "23514"} {
		t.Run(code, func(t *testing.T) {
			f := integration(t)
			f.exec(`CREATE SEQUENCE alert_test_retry; GRANT USAGE,SELECT ON alert_test_retry TO ` + pgx.Identifier{f.appRole}.Sanitize() + `; CREATE FUNCTION alert_test_commit_reject() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF nextval('alert_test_retry')<3 THEN RAISE EXCEPTION 'synthetic private fault' USING ERRCODE='` + code + `'; END IF; RETURN NULL; END $$; CREATE CONSTRAINT TRIGGER zz_alert_test_commit AFTER INSERT ON alert_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_test_commit_reject()`)
			r, err := f.store.Create(f.ctx, f.reader(), CreateRule{uuid.NewString(), ChannelQuarantined, "channel", f.channel, "retry", "enabled", EmptyParameters{}})
			var attempts, rules, commands int
			if e := f.owner.QueryRow(f.ctx, `SELECT (SELECT last_value FROM alert_test_retry),(SELECT count(*) FROM alert_rules),(SELECT count(*) FROM alert_commands)`).Scan(&attempts, &rules, &commands); e != nil {
				t.Fatal(e)
			}
			if code == "23514" {
				requireCode(t, err, "alert_unavailable")
				if attempts != 1 || rules != 0 || commands != 0 || !reflect.DeepEqual(r, CommandReceipt{}) {
					t.Fatal(attempts, rules, commands, r)
				}
				return
			}
			if err != nil || r.Status != "applied" || attempts != 3 || rules != 1 || commands != 1 {
				t.Fatal(attempts, rules, commands, r, err)
			}
			f.exec(`DROP TRIGGER zz_alert_test_commit ON alert_commands; DROP FUNCTION alert_test_commit_reject(); ALTER SEQUENCE alert_test_retry RESTART WITH 1; CREATE FUNCTION alert_test_commit_reject() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM nextval('alert_test_retry'); RAISE EXCEPTION 'bounded private fault' USING ERRCODE='` + code + `'; END $$; CREATE CONSTRAINT TRIGGER zz_alert_test_commit AFTER INSERT ON alert_commands DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION alert_test_commit_reject()`)
			r, err = f.store.Configure(f.ctx, f.reader(), *r.ResourceID, ConfigureRule{uuid.NewString(), "1", "changed", "enabled", EmptyParameters{}})
			requireCode(t, err, "alert_unavailable")
			if !reflect.DeepEqual(r, CommandReceipt{}) {
				t.Fatal("failed retry exposed receipt", r)
			}
			if e := f.owner.QueryRow(f.ctx, `SELECT (SELECT last_value FROM alert_test_retry),(SELECT count(*) FROM alert_rules),(SELECT count(*) FROM alert_commands)`).Scan(&attempts, &rules, &commands); e != nil || attempts != 3 || rules != 1 || commands != 1 {
				t.Fatal(attempts, rules, commands, e)
			}
		})
	}
}

func alertSlowSource(t *testing.T, f *fixture) {
	t.Helper()
	f.exec(`ALTER TABLE channels RENAME TO alert_test_channels; CREATE VIEW channels AS SELECT c.id,c.enabled,c.quarantined FROM alert_test_channels c CROSS JOIN LATERAL (SELECT pg_sleep(2)) s; GRANT SELECT ON channels TO ` + pgx.Identifier{f.appRole}.Sanitize())
}

func TestPostgresAlertSourceTimeoutPreservesSuccessAndCancellationWritesNothing(t *testing.T) {
	for _, cancelSource := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelSource), func(t *testing.T) {
			f := integration(t)
			rule, episode := alertOpen(t, f)
			f.due(rule)
			before, err := f.store.Rule(f.ctx, f.reader(), rule)
			if err != nil {
				t.Fatal(err)
			}
			var beforeWrite int64
			if err = f.app.QueryRow(f.ctx, `SELECT write_revision FROM alert_rule_runtime WHERE rule_id=$1`, rule).Scan(&beforeWrite); err != nil {
				t.Fatal(err)
			}
			alertSlowSource(t, f)
			cfg := f.app.Config()
			name := "alert_fault_" + uuid.NewString()
			cfg.ConnConfig.RuntimeParams["application_name"] = name
			p, err := pgxpool.NewWithConfig(f.ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			via, err := NewStore(p, RuntimeOptions{DeploymentEnabled: true})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			done := make(chan struct {
				r EvaluationResult
				e error
			}, 1)
			go func() {
				r, e := via.EvaluateDue(ctx)
				done <- struct {
					r EvaluationResult
					e error
				}{r, e}
			}()
			if cancelSource {
				deadline := time.After(time.Second)
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					var sleeping bool
					if err = f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND state='active' AND wait_event='PgSleep')`, name).Scan(&sleeping); err != nil {
						t.Fatal(err)
					}
					if sleeping {
						break
					}
					select {
					case <-deadline:
						t.Fatal("source SQL never reached actual server PgSleep")
					case <-ticker.C:
					}
				}
				cancel()
			}
			select {
			case out := <-done:
				if cancelSource {
					requireCode(t, out.e, "alert_unavailable")
					if out.r.Outcome != "" {
						t.Fatal(out)
					}
				} else if out.e != nil || out.r.Outcome != "failed_recorded" || out.r.ErrorCode != "source_query_timeout" {
					t.Fatal(out)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("source deadline did not finish")
			}
			after, err := f.store.Rule(f.ctx, f.reader(), rule)
			if err != nil || after.Runtime.CurrentEpisodeID == nil || *after.Runtime.CurrentEpisodeID != episode || !after.Runtime.LastSuccessAt.Equal(before.Runtime.LastSuccessAt.Time) || !reflect.DeepEqual(after.Runtime.LastSample, before.Runtime.LastSample) {
				t.Fatal("failure lost last success", after, err)
			}
			var write int64
			var interval float64
			if err = f.app.QueryRow(f.ctx, `SELECT write_revision,extract(epoch FROM next_evaluation_at-last_attempt_at)::float8 FROM alert_rule_runtime WHERE rule_id=$1`, rule).Scan(&write, &interval); err != nil {
				t.Fatal(err)
			}
			if cancelSource {
				if write != beforeWrite || after.Runtime.LastErrorCode != nil {
					t.Fatal("canceled source recorded a failure", write, beforeWrite, after)
				}
			} else if write != beforeWrite+1 || interval != 30 || after.Runtime.LastErrorCode == nil || *after.Runtime.LastErrorCode != "source_query_timeout" {
				t.Fatal(write, beforeWrite, interval, after)
			}
		})
	}
}

type alertRollbackSQLKey struct{}
type alertRollbackPause struct {
	ready, release chan struct{}
	armed          atomic.Bool
}

func (p *alertRollbackPause) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, alertRollbackSQLKey{}, strings.ToLower(strings.TrimSpace(d.SQL)))
}
func (p *alertRollbackPause) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	if ctx.Value(alertRollbackSQLKey{}) == "rollback" && d.Err == nil && p.armed.CompareAndSwap(true, false) {
		close(p.ready)
		<-p.release
	}
}

func TestPostgresAlertLateFailureAfterActualRollbackCannotOverwriteNewSuccess(t *testing.T) {
	f := integration(t)
	rule, episode := alertOpen(t, f)
	f.due(rule)
	f.exec(`ALTER TABLE channels RENAME TO alert_test_channels; CREATE FUNCTION alert_test_source_failure() RETURNS boolean LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic source failure' USING ERRCODE='57014'; END $$; CREATE VIEW channels AS SELECT id,enabled,alert_test_source_failure() AS quarantined FROM alert_test_channels; GRANT SELECT ON channels TO ` + pgx.Identifier{f.appRole}.Sanitize())
	pause := &alertRollbackPause{ready: make(chan struct{}), release: make(chan struct{})}
	pause.armed.Store(true)
	var release sync.Once
	unblock := func() { release.Do(func() { close(pause.release) }) }
	defer unblock()
	cfg := f.app.Config()
	cfg.ConnConfig.Tracer = pause
	p, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { unblock(); p.Close() }()
	via, err := NewStore(p, RuntimeOptions{DeploymentEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct {
		r EvaluationResult
		e error
	}, 1)
	go func() {
		r, e := via.EvaluateDue(f.ctx)
		done <- struct {
			r EvaluationResult
			e error
		}{r, e}
	}()
	select {
	case <-pause.ready:
	case <-time.After(time.Second):
		t.Fatal("first evaluator did not complete actual server ROLLBACK")
	}
	// ROLLBACK has released the first evaluator's rule/runtime locks. Restore
	// the original source, then commit a real newer same-configuration success.
	f.exec(`DROP VIEW channels; DROP FUNCTION alert_test_source_failure(); ALTER TABLE alert_test_channels RENAME TO channels`)
	if r, err := f.store.EvaluateDue(f.ctx); err != nil || r.Outcome != "evaluated" {
		t.Fatal(r, err)
	}
	before, err := f.store.Rule(f.ctx, f.reader(), rule)
	if err != nil {
		t.Fatal(err)
	}
	unblock()
	select {
	case out := <-done:
		if out.e != nil || out.r.Outcome != "stale_discarded" {
			t.Fatal(out)
		}
	case <-time.After(time.Second):
		t.Fatal("old failure did not finish")
	}
	after, err := f.store.Rule(f.ctx, f.reader(), rule)
	if err != nil || after.Runtime.LastErrorCode != nil || after.Runtime.CurrentEpisodeID == nil || *after.Runtime.CurrentEpisodeID != episode || !after.Runtime.LastSuccessAt.Equal(before.Runtime.LastSuccessAt.Time) || !reflect.DeepEqual(before.Runtime.LastSample, after.Runtime.LastSample) {
		t.Fatal("late failure overwrote newer success", after, err)
	}
}
