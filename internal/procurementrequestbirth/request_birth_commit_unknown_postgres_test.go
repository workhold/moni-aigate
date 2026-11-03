package procurementrequestbirth_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

const a1CommitWireMaxFrame = 1 << 20

type a1CommitWireState struct {
	queries, replies, drops int
	problem                 string
}

// This test-side wrapper retains the actual socket/peer/deadlines. It never
// fabricates a PostgreSQL reply: the one fault withholds a complete real
// CommandComplete(COMMIT) before the driver can observe any of that frame.
type a1CommitWire struct {
	net.Conn
	mu             sync.Mutex
	armed, pending bool
	state          a1CommitWireState
	writeMu        sync.Mutex
	frontend       []byte
	readMu         sync.Mutex
	backend, ready []byte
	readErr        error
	closeOnce      sync.Once
	closeErr       error
}

func (w *a1CommitWire) Close() error {
	w.closeOnce.Do(func() { w.closeErr = w.Conn.Close() })
	return w.closeErr
}

func (w *a1CommitWire) arm() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.armed || w.pending || w.state.queries != 0 || w.state.drops != 0 || w.state.problem != "" {
		return errors.New("A1 wire observer cannot be rearmed")
	}
	w.armed = true
	return nil
}

func (w *a1CommitWire) observed() a1CommitWireState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

func (w *a1CommitWire) fail(problem string) error {
	w.mu.Lock()
	if w.state.problem == "" {
		w.state.problem = problem
	}
	w.mu.Unlock()
	_ = w.Close()
	return errors.New("A1 wire observer rejected " + problem)
}

func (w *a1CommitWire) observeFrontend(p []byte) error {
	w.mu.Lock()
	armed := w.armed
	w.mu.Unlock()
	if !armed {
		// Startup is untyped. Arming happens only after the real APP identity
		// query has completed; reconnects are never armed.
		return nil
	}
	if len(w.frontend)+len(p) > 2*a1CommitWireMaxFrame {
		return w.fail("frontend buffer bound")
	}
	w.frontend = append(w.frontend, p...)
	for len(w.frontend) >= 5 {
		size := binary.BigEndian.Uint32(w.frontend[1:5])
		if size < 4 || size > a1CommitWireMaxFrame {
			return w.fail("frontend frame length")
		}
		total := int(size) + 1
		if len(w.frontend) < total {
			return nil
		}
		frame := w.frontend[:total]
		if frame[0] == 'Q' {
			body := frame[5:]
			if len(body) == 0 || body[len(body)-1] != 0 || bytes.IndexByte(body[:len(body)-1], 0) >= 0 {
				return w.fail("simple-query terminator")
			}
			if strings.EqualFold(strings.TrimSpace(string(body[:len(body)-1])), "commit") {
				w.mu.Lock()
				w.state.queries++
				repeated := w.pending || w.state.queries != 1
				w.pending = true
				w.mu.Unlock()
				if repeated {
					return w.fail("repeated target commit")
				}
			}
		}
		w.frontend = w.frontend[total:]
	}
	return nil
}

func (w *a1CommitWire) Write(p []byte) (int, error) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	// Record only the intent before the real write so an eager reader cannot
	// outrun it. A Q by itself is never considered committed evidence.
	if err := w.observeFrontend(p); err != nil {
		return 0, err
	}
	return w.Conn.Write(p)
}

func (w *a1CommitWire) dropCommit(frame []byte) bool {
	if frame[0] != 'C' || !bytes.Equal(frame[5:], []byte("COMMIT\x00")) {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.armed || !w.pending || w.state.drops != 0 {
		return false
	}
	w.state.replies++
	w.state.drops++
	w.pending, w.armed = false, false
	return true
}

func (w *a1CommitWire) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.readMu.Lock()
	defer w.readMu.Unlock()
	for {
		if len(w.ready) > 0 {
			n := copy(p, w.ready)
			w.ready = w.ready[n:]
			return n, nil
		}
		if len(w.backend) >= 5 {
			size := binary.BigEndian.Uint32(w.backend[1:5])
			if size < 4 || size > a1CommitWireMaxFrame {
				return 0, w.fail("backend frame length")
			}
			total := int(size) + 1
			if len(w.backend) >= total {
				frame := w.backend[:total]
				if w.dropCommit(frame) {
					w.backend = nil
					w.readErr = io.EOF
					_ = w.Close()
					return 0, io.EOF
				}
				w.ready = frame
				w.backend = w.backend[total:]
				continue
			}
		}
		if w.readErr != nil {
			err := w.readErr
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				// A later cleared deadline must be able to resume this same
				// partial frame; the timeout is not a permanent socket EOF.
				w.readErr = nil
			}
			return 0, err
		}
		var raw [16 << 10]byte
		n, err := w.Conn.Read(raw[:])
		if n > 0 {
			if len(w.backend)+n > 2*a1CommitWireMaxFrame {
				return 0, w.fail("backend buffer bound")
			}
			w.backend = append(w.backend, raw[:n]...)
		}
		if err != nil {
			// Retain partial framing even across timeout/EOF; never discard
			// consumed bytes then pretend a later byte begins a new message.
			w.readErr = err
		}
		if n == 0 && err == nil {
			return 0, io.ErrNoProgress
		}
	}
}

func a1CommitUnknownPool(t *testing.T, f *fixture) (*pgxpool.Pool, *a1CommitWire, uint32) {
	t.Helper()
	cfg := f.app.Config()
	c := cfg.ConnConfig
	if c.Host != "127.0.0.1" || c.Port != 55439 || c.Database != "moni_test" || c.User != f.b.App || c.TLSConfig != nil || len(c.Fallbacks) != 0 || c.DialFunc == nil {
		t.Fatal("wire test requires the original actual APP plain loopback config")
	}
	dial := c.DialFunc
	var mu sync.Mutex
	var wires []*a1CommitWire
	c.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		peer, ok := conn.RemoteAddr().(*net.TCPAddr)
		if !ok || !peer.IP.Equal(net.ParseIP("127.0.0.1")) || peer.Port != 55439 {
			_ = conn.Close()
			return nil, errors.New("wire test peer is outside the dedicated database")
		}
		w := &a1CommitWire{Conn: conn}
		mu.Lock()
		wires = append(wires, w)
		mu.Unlock()
		return w, nil
	}
	pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
	must(t, "create cloned actual APP wire pool", err)
	t.Cleanup(func() {
		mu.Lock()
		all := append([]*a1CommitWire(nil), wires...)
		mu.Unlock()
		for _, w := range all {
			_ = w.Close()
		}
		pool.Close()
	})
	conn, err := pool.Acquire(f.ctx)
	must(t, "acquire real wire-wrapped APP connection", err)
	defer conn.Release()
	var session, current string
	var pid uint32
	must(t, "confirm wire pool actual APP LOGIN", conn.QueryRow(f.ctx, "SELECT session_user,current_user,pg_backend_pid()").Scan(&session, &current, &pid))
	if session != f.b.App || current != f.b.App || pid == 0 {
		t.Fatal("wire pool does not use the actual APP identity")
	}
	w, ok := conn.Conn().PgConn().Conn().(*a1CommitWire)
	if !ok {
		t.Fatal("actual APP socket is not the intended test wrapper")
	}
	return pool, w, pid
}

func a1CommitWaitBackendGone(t *testing.T, f *fixture, pid uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var gone bool
		must(t, "observe the target lost-reply backend cleanup", f.owner.QueryRow(ctx, "SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1)", pid).Scan(&gone))
		if gone {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("lost-reply socket left its original backend alive")
		case <-tick.C:
		}
	}
}

func TestPostgresA1CommitUnknownKeepsUniqueBirthAndFunds(t *testing.T) {
	f := newFixture(t)
	admin := birthBudgetBaseline(t, f)
	controls := birthBudgetConfigure(t, f, admin, "enabled", "disabled")
	pool, wire, pid := a1CommitUnknownPool(t, f)
	store := billing.New(pool)
	in := f.input()
	must(t, "arm only the authenticated connection's first COMMIT", wire.arm())
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	r, err := store.Reserve(ctx, in)
	state := wire.observed()
	if err == nil || (!errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, net.ErrClosed)) || ctx.Err() != nil || r.RequestID != "" {
		t.Fatalf("original Reserve did not retain the actual lost-reply transport failure: %v", err)
	}
	if state.problem != "" || state.queries != 1 || state.replies != 1 || state.drops != 1 {
		t.Fatalf("the unique real COMMIT reply was not withheld: %+v", state)
	}
	// This separate owner connection can only see committed rows. Neither a
	// frontend COMMIT nor the EOF itself establishes that the server committed.
	var exact bool
	var reservation string
	must(t, "independently see the committed original request, identity and companions", f.owner.QueryRow(f.ctx, `SELECT v.id,
 r.tenant_id=$2 AND r.key_id=$3 AND r.owner_id=$4 AND r.request_hmac=$6 AND r.status='accepted' AND r.billing_status='reserved'
 AND i.tenant_id=r.tenant_id AND i.operation='inference' AND i.key=$5 AND i.request_hmac=$6 AND i.request_id=r.id
 AND v.tenant_id=r.tenant_id AND v.status='reserved' AND v.amount=120
 AND (SELECT count(*) FROM request_idempotency WHERE tenant_id=$2 AND operation='inference' AND key=$5)=1
 AND (SELECT count(*) FROM reservations WHERE request_id=r.id)=1
 AND (SELECT count(*) FROM journals WHERE operation_id=v.id AND kind='reserve')=1
 AND (SELECT count(*) FROM journal_lines l JOIN journals j ON j.id=l.journal_id WHERE j.operation_id=v.id AND j.kind='reserve')=2
 AND (SELECT count(*) FROM audit_events WHERE request_id=r.id AND action='billing.reserve')=1
 AND (SELECT count(*) FROM projection_outbox WHERE operation_id=v.id AND kind='reserved')=1
 AND (SELECT count(*) FROM recovery_jobs WHERE request_id=r.id)=1
 FROM request_records r JOIN reservations v ON v.request_id=r.id JOIN request_idempotency i ON i.request_id=r.id
 WHERE r.id=$1`, in.RequestID, f.tenant, f.key, f.user, in.IdempotencyKey, in.RequestHMAC).Scan(&reservation, &exact))
	if !exact {
		t.Fatal("committed request did not preserve the exact original identity or unique effects")
	}
	f.checkBirth(t, in.RequestID)
	birthBudgetWallet(t, f, 9880, 120)
	birthBudgetEffects(t, f, billing.Reservation{ID: reservation, RequestID: in.RequestID, Amount: 120}, controls, false)
	a1CommitWaitBackendGone(t, f, pid)
	committed := birthBudgetSnapshot(t, f)
	for _, item := range []struct {
		name  string
		store *billing.Store
		input billing.ReserveInput
	}{
		{"same_store_same_input", store, in},
		{"new_store_same_input", billing.New(f.app), in},
		{"new_store_fresh_request_same_identity", billing.New(f.app), func() billing.ReserveInput { next := in; next.RequestID = uuid.NewString(); return next }()},
	} {
		t.Run(item.name, func(t *testing.T) {
			again, e := item.store.Reserve(f.ctx, item.input)
			var business *billing.Error
			if !errors.As(e, &business) || business.Code != "request_exists" || business.Message != "inference idempotency key already used" || business.ResourceID != in.RequestID || again.RequestID != "" {
				t.Fatalf("original idempotency observation did not point to the committed request: %v", e)
			}
			if birthBudgetSnapshot(t, f) != committed {
				t.Fatal("observing the committed identity created another birth or financial effect")
			}
			if observed := wire.observed(); observed != state {
				t.Fatal("the old connection was rearmed or another commit reply was dropped")
			}
		})
	}
}

// Memory-only controls verify that the fault facility recognizes wire framing;
// they are not PostgreSQL, Store, or commercial acceptance evidence.
type a1CommitMemoryConn struct {
	reader  io.Reader
	written bytes.Buffer
	closed  bool
}

func (m *a1CommitMemoryConn) Read(p []byte) (int, error) {
	if m.closed {
		return 0, io.EOF
	}
	return m.reader.Read(p)
}
func (m *a1CommitMemoryConn) Write(p []byte) (int, error) { return m.written.Write(p) }
func (m *a1CommitMemoryConn) Close() error                { m.closed = true; return nil }
func (m *a1CommitMemoryConn) LocalAddr() net.Addr         { return nil }
func (m *a1CommitMemoryConn) RemoteAddr() net.Addr        { return nil }
func (m *a1CommitMemoryConn) SetDeadline(time.Time) error { return nil }
func (m *a1CommitMemoryConn) SetReadDeadline(time.Time) error {
	return nil
}
func (m *a1CommitMemoryConn) SetWriteDeadline(time.Time) error { return nil }

type a1CommitChoppedReader struct {
	data []byte
	step int
}

func (r *a1CommitChoppedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), len(r.data), 1+r.step%7)
	r.step++
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

type a1CommitTimeoutReader struct {
	data  []byte
	phase int
}

func (r *a1CommitTimeoutReader) Read(p []byte) (int, error) {
	if r.phase == 1 {
		r.phase++
		return 0, os.ErrDeadlineExceeded
	}
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), len(r.data))
	if r.phase == 0 {
		r.phase++
		n = min(n, 2)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func a1CommitFrame(kind byte, body []byte) []byte {
	frame := make([]byte, 5, 5+len(body))
	frame[0] = kind
	binary.BigEndian.PutUint32(frame[1:], uint32(4+len(body)))
	return append(frame, body...)
}

func a1CommitWriteChunks(t *testing.T, w *a1CommitWire, raw []byte) {
	t.Helper()
	for at := 0; at < len(raw); {
		next := min(len(raw), at+1+at%7)
		n, err := w.Write(raw[at:next])
		must(t, "forward fragmented original frontend bytes", err)
		if n != next-at {
			t.Fatal("fragmented frontend Write was incomplete")
		}
		at = next
	}
}

func TestA1CommitUnknownWireControls(t *testing.T) {
	prefix := a1CommitFrame('C', []byte("UPDATE 1\x00"))
	commit := a1CommitFrame('C', []byte("COMMIT\x00"))
	ready := a1CommitFrame('Z', []byte{'I'})
	all := bytes.Join([][]byte{prefix, commit, ready}, nil)
	for _, kind := range []string{"unarmed", "non_target", "target"} {
		t.Run(kind, func(t *testing.T) {
			m := &a1CommitMemoryConn{reader: &a1CommitChoppedReader{data: all}}
			w := &a1CommitWire{Conn: m}
			if kind != "unarmed" {
				must(t, "arm pure wire control", w.arm())
			}
			front := a1CommitFrame('Q', []byte("select 'commit'\x00"))
			front = append(front, a1CommitFrame('P', []byte("commit\x00"))...)
			if kind == "target" {
				front = append(front, a1CommitFrame('Q', []byte("  CoMmIt  \x00"))...)
			}
			a1CommitWriteChunks(t, w, front)
			if !bytes.Equal(m.written.Bytes(), front) {
				t.Fatal("wire observer changed the original frontend bytes")
			}
			got, err := io.ReadAll(w)
			must(t, "read pure fragmented backend control", err)
			state := w.observed()
			if kind == "target" {
				if !bytes.Equal(got, prefix) || state.queries != 1 || state.replies != 1 || state.drops != 1 || state.problem != "" || !m.closed {
					t.Fatal("target COMMIT leaked any ACK bytes or the wrong frame was discarded")
				}
			} else if !bytes.Equal(got, all) || state != (a1CommitWireState{}) || m.closed {
				t.Fatal("unarmed/non-target traffic was modified or mistaken for COMMIT")
			}
		})
	}
	t.Run("timeout_preserves_partial_frame_and_resumes", func(t *testing.T) {
		m := &a1CommitMemoryConn{reader: &a1CommitTimeoutReader{data: all}}
		w := &a1CommitWire{Conn: m}
		var first [32]byte
		if n, err := w.Read(first[:]); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal("partial frame did not retain the original read timeout")
		}
		got, err := io.ReadAll(w)
		must(t, "resume wire frame after the original timeout is cleared", err)
		if !bytes.Equal(got, all) || w.observed() != (a1CommitWireState{}) || m.closed {
			t.Fatal("read timeout discarded partial framing or permanently poisoned the observer")
		}
	})
	for _, direction := range []string{"frontend", "backend"} {
		t.Run("invalid_"+direction+"_length", func(t *testing.T) {
			invalid := []byte{'Q', 0, 0, 0, 3}
			m := &a1CommitMemoryConn{reader: bytes.NewReader(invalid)}
			w := &a1CommitWire{Conn: m}
			must(t, "arm malformed-frame pure control", w.arm())
			var err error
			if direction == "frontend" {
				_, err = w.Write(invalid)
			} else {
				_, err = io.ReadAll(w)
			}
			if err == nil || w.observed().problem == "" || !m.closed {
				t.Fatal("malformed wire frame was accepted as normal or committed traffic")
			}
		})
	}
}
