package procurementattemptbirth_test

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

	"github.com/jackc/pgx/v5/pgxpool"
)

// Independent test-only copy of the reviewed A1 facility. The original helper,
// SQL, fixture and permissions remain unchanged; this file never synthesizes ACKs.
const abExecutorWireMaxFrame = 1 << 20

type abExecutorDialState struct {
	dials, startups, cancels int
	problem                  string
}

// pgx asyncClose opens a separate cancellation transport after read EOF. Count
// every socket's exact startup packet, not just DialFunc calls. SecretKey is
// compared in memory and is never part of state, diagnostics or failure text.
type abExecutorDialAudit struct {
	mu      sync.Mutex
	state   abExecutorDialState
	pid     uint32
	secret  uint32
	cleanup <-chan struct{}
}

func (a *abExecutorDialAudit) snapshot() abExecutorDialState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

type abExecutorDialConn struct {
	net.Conn
	audit      *abExecutorDialAudit
	mu         sync.Mutex
	first      []byte
	classified bool
	cancel     bool
}

func (c *abExecutorDialConn) rejected() (int, error) {
	c.audit.mu.Lock()
	c.audit.state.problem = "unrecognized startup or cancel identity"
	c.audit.mu.Unlock()
	_ = c.Conn.Close()
	return 0, errors.New("S2 wire rejected unrecognized startup or cancel identity")
}

func (c *abExecutorDialConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel && len(p) > 0 {
		return c.rejected()
	}
	if !c.classified {
		if len(c.first)+len(p) > abExecutorWireMaxFrame {
			return c.rejected()
		}
		c.first = append(c.first, p...)
		if len(c.first) >= 4 {
			size := int(binary.BigEndian.Uint32(c.first[:4]))
			if size < 8 || size > abExecutorWireMaxFrame {
				return c.rejected()
			}
			if len(c.first) >= size {
				kind := binary.BigEndian.Uint32(c.first[4:8])
				c.audit.mu.Lock()
				switch {
				case kind == 196608: // protocol 3.0 StartupMessage
					c.audit.state.startups++
				case kind == 80877102 && size == 16 && len(c.first) == 16 && c.audit.pid != 0 && binary.BigEndian.Uint32(c.first[8:12]) == c.audit.pid && binary.BigEndian.Uint32(c.first[12:16]) == c.audit.secret:
					c.audit.state.cancels++
					c.cancel = true
				default:
					c.audit.mu.Unlock()
					return c.rejected()
				}
				c.audit.mu.Unlock()
				c.classified, c.first = true, nil
			}
		}
	}
	return c.Conn.Write(p)
}

type abExecutorWireState struct {
	queries, replies, drops int
	problem                 string
}

// This test-side wrapper retains the actual socket/peer/deadlines. It never
// fabricates a PostgreSQL reply: the one fault withholds a complete real
// CommandComplete(COMMIT) before the driver can observe any of that frame.
type abExecutorWire struct {
	net.Conn
	mu             sync.Mutex
	armed, pending bool
	state          abExecutorWireState
	writeMu        sync.Mutex
	frontend       []byte
	readMu         sync.Mutex
	backend, ready []byte
	readErr        error
	closeOnce      sync.Once
	closeErr       error
	commitReady    chan struct{}
	commitRelease  chan struct{}
	gateOnce       sync.Once
	releaseOnce    sync.Once
}

// Gate only the real frontend COMMIT before its first socket byte is sent. It
// lets an independent connection prove that a fully decoded private receipt is
// still invisible. The only ACK fault remains the complete real backend frame.
func (w *abExecutorWire) gate() <-chan struct{} {
	w.commitReady, w.commitRelease = make(chan struct{}), make(chan struct{})
	return w.commitReady
}

func (w *abExecutorWire) releaseCommit() {
	w.releaseOnce.Do(func() {
		if w.commitRelease != nil {
			close(w.commitRelease)
		}
	})
}

func (w *abExecutorWire) Close() error {
	w.closeOnce.Do(func() { w.closeErr = w.Conn.Close() })
	return w.closeErr
}

func (w *abExecutorWire) arm() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.armed || w.pending || w.state.queries != 0 || w.state.drops != 0 || w.state.problem != "" {
		return errors.New("S2 wire observer cannot be rearmed")
	}
	w.armed = true
	return nil
}

func (w *abExecutorWire) observed() abExecutorWireState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

func (w *abExecutorWire) fail(problem string) error {
	w.mu.Lock()
	if w.state.problem == "" {
		w.state.problem = problem
	}
	w.mu.Unlock()
	_ = w.Close()
	return errors.New("S2 wire observer rejected " + problem)
}

func (w *abExecutorWire) observeFrontend(p []byte) error {
	w.mu.Lock()
	armed := w.armed
	w.mu.Unlock()
	if !armed {
		// Startup is untyped. Arming happens only after the real APP identity
		// query has completed; reconnects are never armed.
		return nil
	}
	if len(w.frontend)+len(p) > 2*abExecutorWireMaxFrame {
		return w.fail("frontend buffer bound")
	}
	w.frontend = append(w.frontend, p...)
	for len(w.frontend) >= 5 {
		size := binary.BigEndian.Uint32(w.frontend[1:5])
		if size < 4 || size > abExecutorWireMaxFrame {
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

func (w *abExecutorWire) Write(p []byte) (int, error) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	// Record only the intent before the real write so an eager reader cannot
	// outrun it. A Q by itself is never considered committed evidence.
	if err := w.observeFrontend(p); err != nil {
		return 0, err
	}
	w.mu.Lock()
	pending := w.pending
	w.mu.Unlock()
	if pending && w.commitReady != nil {
		w.gateOnce.Do(func() { close(w.commitReady) })
		<-w.commitRelease
	}
	return w.Conn.Write(p)
}

func (w *abExecutorWire) dropCommit(frame []byte) bool {
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

func (w *abExecutorWire) Read(p []byte) (int, error) {
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
			if size < 4 || size > abExecutorWireMaxFrame {
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
			if len(w.backend)+n > 2*abExecutorWireMaxFrame {
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

func abExecutorUnknownPool(t *testing.T, f *fixture) (*pgxpool.Pool, *abExecutorWire, uint32, *abExecutorDialAudit) {
	t.Helper()
	cfg := f.app.Config()
	c := cfg.ConnConfig
	if c.Host != "127.0.0.1" || c.Port != 55439 || c.Database != "moni_test" || c.User != f.b.App || c.TLSConfig != nil || len(c.Fallbacks) != 0 || c.DialFunc == nil {
		t.Fatal("wire test requires the original actual APP plain loopback config")
	}
	dial := c.DialFunc
	cfg.MaxConns, cfg.MinConns = 1, 0
	dials := new(abExecutorDialAudit)
	var mu sync.Mutex
	var wires []*abExecutorWire
	c.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.mu.Lock()
		dials.state.dials++
		dials.mu.Unlock()
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		peer, ok := conn.RemoteAddr().(*net.TCPAddr)
		if !ok || !peer.IP.Equal(net.ParseIP("127.0.0.1")) || peer.Port != 55439 {
			_ = conn.Close()
			return nil, errors.New("wire test peer is outside the dedicated database")
		}
		w := &abExecutorWire{Conn: &abExecutorDialConn{Conn: conn, audit: dials}}
		mu.Lock()
		wires = append(wires, w)
		mu.Unlock()
		return w, nil
	}
	pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
	must(t, "create cloned actual APP wire pool", err)
	t.Cleanup(func() {
		mu.Lock()
		all := append([]*abExecutorWire(nil), wires...)
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
	w, ok := conn.Conn().PgConn().Conn().(*abExecutorWire)
	if !ok {
		t.Fatal("actual APP socket is not the intended test wrapper")
	}
	dials.mu.Lock()
	dials.pid, dials.secret, dials.cleanup = pid, conn.Conn().PgConn().SecretKey(), conn.Conn().PgConn().CleanupDone()
	dials.mu.Unlock()
	return pool, w, pid, dials
}

func abExecutorWaitBackendGone(t *testing.T, f *fixture, pid uint32) {
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

// Memory-only controls verify that the fault facility recognizes wire framing;
// they are not PostgreSQL, Store, or commercial acceptance evidence.
type abExecutorMemoryConn struct {
	reader  io.Reader
	written bytes.Buffer
	closed  bool
}

func (m *abExecutorMemoryConn) Read(p []byte) (int, error) {
	if m.closed {
		return 0, io.EOF
	}
	return m.reader.Read(p)
}
func (m *abExecutorMemoryConn) Write(p []byte) (int, error) { return m.written.Write(p) }
func (m *abExecutorMemoryConn) Close() error                { m.closed = true; return nil }
func (m *abExecutorMemoryConn) LocalAddr() net.Addr         { return nil }
func (m *abExecutorMemoryConn) RemoteAddr() net.Addr        { return nil }
func (m *abExecutorMemoryConn) SetDeadline(time.Time) error { return nil }
func (m *abExecutorMemoryConn) SetReadDeadline(time.Time) error {
	return nil
}
func (m *abExecutorMemoryConn) SetWriteDeadline(time.Time) error { return nil }

type abExecutorChoppedReader struct {
	data []byte
	step int
}

func (r *abExecutorChoppedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), len(r.data), 1+r.step%7)
	r.step++
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

type abExecutorTimeoutReader struct {
	data  []byte
	phase int
}

func (r *abExecutorTimeoutReader) Read(p []byte) (int, error) {
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

func abExecutorFrame(kind byte, body []byte) []byte {
	frame := make([]byte, 5, 5+len(body))
	frame[0] = kind
	binary.BigEndian.PutUint32(frame[1:], uint32(4+len(body)))
	return append(frame, body...)
}

func abExecutorWriteChunks(t *testing.T, w *abExecutorWire, raw []byte) {
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

func TestExecutorCommitUnknownWireControls(t *testing.T) {
	prefix := abExecutorFrame('C', []byte("UPDATE 1\x00"))
	commit := abExecutorFrame('C', []byte("COMMIT\x00"))
	ready := abExecutorFrame('Z', []byte{'I'})
	all := bytes.Join([][]byte{prefix, commit, ready}, nil)
	for _, kind := range []string{"unarmed", "non_target", "target"} {
		t.Run(kind, func(t *testing.T) {
			m := &abExecutorMemoryConn{reader: &abExecutorChoppedReader{data: all}}
			w := &abExecutorWire{Conn: m}
			if kind != "unarmed" {
				must(t, "arm pure wire control", w.arm())
			}
			front := abExecutorFrame('Q', []byte("select 'commit'\x00"))
			front = append(front, abExecutorFrame('P', []byte("commit\x00"))...)
			if kind == "target" {
				front = append(front, abExecutorFrame('Q', []byte("  CoMmIt  \x00"))...)
			}
			abExecutorWriteChunks(t, w, front)
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
			} else if !bytes.Equal(got, all) || state != (abExecutorWireState{}) || m.closed {
				t.Fatal("unarmed/non-target traffic was modified or mistaken for COMMIT")
			}
		})
	}
	t.Run("timeout_preserves_partial_frame_and_resumes", func(t *testing.T) {
		m := &abExecutorMemoryConn{reader: &abExecutorTimeoutReader{data: all}}
		w := &abExecutorWire{Conn: m}
		var first [32]byte
		if n, err := w.Read(first[:]); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal("partial frame did not retain the original read timeout")
		}
		got, err := io.ReadAll(w)
		must(t, "resume wire frame after the original timeout is cleared", err)
		if !bytes.Equal(got, all) || w.observed() != (abExecutorWireState{}) || m.closed {
			t.Fatal("read timeout discarded partial framing or permanently poisoned the observer")
		}
	})
	for _, direction := range []string{"frontend", "backend"} {
		t.Run("invalid_"+direction+"_length", func(t *testing.T) {
			invalid := []byte{'Q', 0, 0, 0, 3}
			m := &abExecutorMemoryConn{reader: bytes.NewReader(invalid)}
			w := &abExecutorWire{Conn: m}
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
	t.Run("precommit_gate_forwards_no_commit_bytes_until_release", func(t *testing.T) {
		m := &abExecutorMemoryConn{reader: bytes.NewReader(commit)}
		w := &abExecutorWire{Conn: m}
		ready := w.gate()
		must(t, "arm exact precommit gate control", w.arm())
		front := abExecutorFrame('Q', []byte("COMMIT\x00"))
		done := make(chan error, 1)
		go func() {
			_, err := w.Write(front)
			done <- err
		}()
		defer w.releaseCommit()
		select {
		case <-ready:
		case <-time.After(time.Second):
			t.Fatal("pure wire did not reach COMMIT gate")
		}
		if m.written.Len() != 0 || w.observed().queries != 1 || w.observed().replies != 0 {
			t.Fatal("gate leaked COMMIT bytes before independent precommit observation")
		}
		w.releaseCommit()
		must(t, "release and join exact COMMIT write", <-done)
		if !bytes.Equal(m.written.Bytes(), front) {
			t.Fatal("released gate changed the real frontend frame")
		}
		got, err := io.ReadAll(w)
		must(t, "drop the complete ACK after pure release", err)
		if len(got) != 0 || w.observed().drops != 1 {
			t.Fatal("released COMMIT gate no longer suppresses exact ACK")
		}
	})
}

// Memory controls are only for the fault facility. They do not prove a real
// cancellation, server COMMIT, authentication, role or business outcome.
func TestExecutorTransportStartupAndExactCancellationControls(t *testing.T) {
	startup := make([]byte, 8)
	binary.BigEndian.PutUint32(startup[:4], 8)
	binary.BigEndian.PutUint32(startup[4:8], 196608)
	cancel := make([]byte, 16)
	binary.BigEndian.PutUint32(cancel[:4], 16)
	binary.BigEndian.PutUint32(cancel[4:8], 80877102)
	binary.BigEndian.PutUint32(cancel[8:12], 13)
	binary.BigEndian.PutUint32(cancel[12:16], 17)
	for _, tc := range []struct {
		name            string
		packet          []byte
		startup, cancel int
		bad             bool
	}{
		{"startup", startup, 1, 0, false},
		{"exact_cancel", cancel, 0, 1, false},
		{"wrong_backend", func() []byte { b := append([]byte(nil), cancel...); b[11]++; return b }(), 0, 0, true},
		{"wrong_secret", func() []byte { b := append([]byte(nil), cancel...); b[15]++; return b }(), 0, 0, true},
		{"extra_cancel_bytes", append(append([]byte(nil), cancel...), 0), 0, 0, true},
		{"unknown_protocol", func() []byte { b := append([]byte(nil), startup...); b[7]++; return b }(), 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			audit := &abExecutorDialAudit{pid: 13, secret: 17}
			memory := &abExecutorMemoryConn{}
			conn := &abExecutorDialConn{Conn: memory, audit: audit}
			_, err := conn.Write(tc.packet)
			state := audit.snapshot()
			if (err != nil) != tc.bad || state.startups != tc.startup || state.cancels != tc.cancel || (state.problem != "") != tc.bad {
				t.Fatal("transport classification mismatch; private cancellation material omitted")
			}
			if !tc.bad && !bytes.Equal(memory.written.Bytes(), tc.packet) {
				t.Fatal("authenticated startup/cancel bytes were modified")
			}
			if tc.bad && (!memory.closed || memory.written.Len() != 0) {
				t.Fatal("unrecognized cancellation was sent or socket remained open")
			}
		})
	}
	t.Run("fragmented_cancel_is_counted_once_then_rejects_any_extra_write", func(t *testing.T) {
		audit := &abExecutorDialAudit{pid: 13, secret: 17}
		memory := &abExecutorMemoryConn{}
		conn := &abExecutorDialConn{Conn: memory, audit: audit}
		for _, part := range [][]byte{cancel[:2], cancel[2:9], cancel[9:]} {
			_, err := conn.Write(part)
			must(t, "retain exact fragmented cancellation packet", err)
		}
		if audit.snapshot().cancels != 1 || !bytes.Equal(memory.written.Bytes(), cancel) {
			t.Fatal("fragmented cancellation lost identity or was double-counted")
		}
		if _, err := conn.Write([]byte{0}); err == nil || !memory.closed {
			t.Fatal("cancellation socket accepted additional frontend traffic")
		}
	})
}
