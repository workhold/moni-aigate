package maintenancetrust

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

func purePins() fundinglotcatalog.Pins {
	owner := fundinglotcatalog.RolePin{Name: "owner", OID: 10}
	return fundinglotcatalog.Pins{Mode: fundinglotcatalog.Ordinary, Database: fundinglotcatalog.DatabasePin{Host: "127.0.0.1", Port: 55439, Name: "moni_test", OID: 9}, AppSchema: fundinglotcatalog.SchemaPin{Name: "target", OID: 11, NamespaceOwner: owner, ObjectOwner: owner}, App: fundinglotcatalog.RolePin{Name: "app", OID: 12}, Projector: fundinglotcatalog.RolePin{Name: "projector", OID: 13}}
}
func pureSandboxPins() fundinglotcatalog.Pins {
	p := purePins()
	p.Mode = fundinglotcatalog.Sandbox
	p.Worker = &fundinglotcatalog.RolePin{Name: "worker", OID: 14}
	mo := fundinglotcatalog.RolePin{Name: "mock_owner", OID: 15}
	ro := fundinglotcatalog.RolePin{Name: "receiver_owner", OID: 16}
	p.Sandbox = &fundinglotcatalog.SandboxPins{Mock: fundinglotcatalog.SchemaPin{Name: "mock", OID: 17, NamespaceOwner: mo, ObjectOwner: mo}, MockRuntime: fundinglotcatalog.RolePin{Name: "mock_runtime", OID: 18}, Receiver: fundinglotcatalog.SchemaPin{Name: "receiver", OID: 19, NamespaceOwner: ro, ObjectOwner: ro}, ReceiverRuntime: fundinglotcatalog.RolePin{Name: "receiver_runtime", OID: 20}}
	return p
}
func pureConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	c, e := pgx.ParseConfig("postgres://moni_test:redacted@127.0.0.1:55439/moni_test?sslmode=disable")
	if e != nil {
		t.Fatal("parse")
	}
	return c
}
func TestClosedPins(t *testing.T) {
	for _, p := range []fundinglotcatalog.Pins{purePins(), pureSandboxPins()} {
		if !validPins(p) {
			t.Fatal("valid pins")
		}
	}
	tests := []struct {
		name   string
		change func(*fundinglotcatalog.Pins)
	}{
		{"mode", func(p *fundinglotcatalog.Pins) { p.Mode = 0 }},
		{"zero db", func(p *fundinglotcatalog.Pins) { p.Database.OID = 0 }},
		{"unix", func(p *fundinglotcatalog.Pins) { p.Database.Host = "/tmp" }},
		{"null host", func(p *fundinglotcatalog.Pins) { p.Database.Host = "x\x00y" }},
		{"zero port", func(p *fundinglotcatalog.Pins) { p.Database.Port = 0 }},
		{"reserved schema", func(p *fundinglotcatalog.Pins) { p.AppSchema.Name = "pg_temp_1" }},
		{"catalog schema", func(p *fundinglotcatalog.Pins) { p.AppSchema.Name = "information_schema" }},
		{"invalid utf8", func(p *fundinglotcatalog.Pins) { p.App.Name = "\xff" }},
		{"long identifier", func(p *fundinglotcatalog.Pins) { p.App.Name = strings.Repeat("a", 64) }},
		{"same role oid", func(p *fundinglotcatalog.Pins) { p.App.OID = p.Projector.OID }},
		{"same role name", func(p *fundinglotcatalog.Pins) { p.App.Name = p.Projector.Name }},
		{"ordinary worker", func(p *fundinglotcatalog.Pins) { p.Worker = &fundinglotcatalog.RolePin{Name: "worker", OID: 14} }},
		{"different raw owner", func(p *fundinglotcatalog.Pins) { p.AppSchema.NamespaceOwner = p.App }},
		{"fake builtin", func(p *fundinglotcatalog.Pins) {
			p.AppSchema.Name = "public"
			p.AppSchema.NamespaceOwner = fundinglotcatalog.RolePin{Name: "pg_database_owner", OID: 6172}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := purePins()
			tt.change(&p)
			if validPins(p) {
				t.Fatal("accepted invalid pins")
			}
		})
	}
	p := purePins()
	p.AppSchema.Name = "public"
	p.AppSchema.NamespaceOwner = fundinglotcatalog.RolePin{Name: "pg_database_owner", OID: 6171}
	if !validPins(p) {
		t.Fatal("public builtin target")
	}
	for _, change := range []func(*fundinglotcatalog.Pins){
		func(p *fundinglotcatalog.Pins) { p.Worker = nil }, func(p *fundinglotcatalog.Pins) { p.Sandbox = nil },
		func(p *fundinglotcatalog.Pins) { p.Sandbox.Mock.OID = p.AppSchema.OID }, func(p *fundinglotcatalog.Pins) { p.Sandbox.Mock.Name = p.AppSchema.Name },
		func(p *fundinglotcatalog.Pins) { p.Sandbox.ReceiverRuntime.OID = p.App.OID }, func(p *fundinglotcatalog.Pins) { p.Sandbox.Mock.NamespaceOwner = p.AppSchema.ObjectOwner },
	} {
		p := pureSandboxPins()
		change(&p)
		if validPins(p) {
			t.Fatal("sandbox structure")
		}
	}
}
func TestPinsAreDeepCopied(t *testing.T) {
	p := pureSandboxPins()
	copy := copyPins(p)
	p.Worker.Name = "changed"
	p.Sandbox.Mock.Name = "changed"
	p.Sandbox.ReceiverRuntime.OID = 99
	if copy.Worker.Name != "worker" || copy.Sandbox.Mock.Name != "mock" || copy.Sandbox.ReceiverRuntime.OID != 20 {
		t.Fatal("aliased pins")
	}
}
func TestInvalidInputsNeverDial(t *testing.T) {
	c := pureConfig(t)
	p := purePins()
	for _, tt := range []struct {
		name string
		ctx  context.Context
		cfg  *pgx.ConnConfig
		pins fundinglotcatalog.Pins
		want error
	}{
		{"nil context", nil, c, p, ErrInput}, {"nil config", context.Background(), nil, p, ErrInput}, {"zero pins", context.Background(), c, fundinglotcatalog.Pins{}, ErrInput},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g, e := Acquire(tt.ctx, tt.cfg, tt.pins)
			if g != nil || e != tt.want {
				t.Fatal("input error")
			}
		})
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if g, e := Acquire(cancelCtx, c, p); g != nil || e != ErrTimeout {
		t.Fatal("cancelled entrance")
	}
	for _, change := range []func(*pgx.ConnConfig){
		func(c *pgx.ConnConfig) { c.Fallbacks = []*pgconn.FallbackConfig{nil} }, func(c *pgx.ConnConfig) { c.Host = "localhost" },
		func(c *pgx.ConnConfig) { c.Database = "other" }, func(c *pgx.ConnConfig) { c.DialFunc = nil }, func(c *pgx.ConnConfig) { c.LookupFunc = nil },
		func(c *pgx.ConnConfig) { c.Fallbacks = []*pgconn.FallbackConfig{{Host: c.Host, Port: c.Port + 1}} },
	} {
		cc := c.Copy()
		change(cc)
		if g, e := Acquire(context.Background(), cc, p); g != nil || e != ErrInput {
			t.Fatal("bad config")
		}
	}
	c.Fallbacks = []*pgconn.FallbackConfig{{Host: c.Host, Port: c.Port, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
	if !validConfig(c, p) {
		t.Fatal("same endpoint TLS fallback")
	}
	sp := pureSandboxPins()
	if !validConfig(c, sp) {
		t.Fatal("sandbox literal")
	}
	c.User = "other"
	if validConfig(c, sp) {
		t.Fatal("sandbox login literal")
	}
}
func TestRedactionAndCloseLifecycle(t *testing.T) {
	g := Gate{state: &gateState{pins: purePins()}}
	for _, v := range []any{g, &g, Gate{}} {
		if fmt.Sprint(v) != "maintenance_gate_redacted" || fmt.Sprintf("%#v", v) != "maintenance_gate_redacted" {
			t.Fatal("redaction")
		}
		b, e := json.Marshal(v)
		if e != nil || string(b) != `{"gate":"redacted"}` {
			t.Fatal("json redaction")
		}
	}
	var zero Gate
	if zero.Close(nil) != ErrInput || (*Gate)(nil).Close(nil) != ErrInput {
		t.Fatal("zero close")
	}
	copied := g
	g.state.mu.Lock()
	if copied.Close(nil) != ErrGateBusy {
		t.Fatal("busy")
	}
	g.state.mu.Unlock()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if g.Close(cancelled) != ErrCleanup || copied.Close(nil) != ErrCleanup || !g.state.closed {
		t.Fatal("shared invalid closure")
	}
	for _, err := range []error{ErrInput, ErrGateBusy, ErrIdentity, ErrTimeout, ErrExecution, ErrCleanup} {
		if u, ok := err.(interface{ Unwrap() error }); ok && u.Unwrap() != nil {
			t.Fatal("driver unwrap")
		}
		if !strings.HasPrefix(err.Error(), "maintenance_") {
			t.Fatal("safe errors")
		}
	}
	if acquireBudget != 30*time.Second || cleanupBudget != 5*time.Second || pollInterval != 50*time.Millisecond {
		t.Fatal("frozen budgets")
	}
	if reflect.TypeOf(Gate{}).NumField() != 1 {
		t.Fatal("private state")
	}
}

func TestSocketRegistrySealsConcurrentLateDials(t *testing.T) {
	var state gateState
	const n = 32
	clients := make([]net.Conn, n)
	peers := make([]net.Conn, n)
	for i := range clients {
		clients[i], peers[i] = net.Pipe()
		defer peers[i].Close()
	}
	var wg sync.WaitGroup
	for _, conn := range clients {
		wg.Add(1)
		go func(c net.Conn) { defer wg.Done(); state.registerSocket(c) }(conn)
	}
	state.closeSockets()
	wg.Wait()
	state.closeSockets()
	for _, peer := range peers {
		_ = peer.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		if _, err := peer.Read(b[:]); err != io.EOF {
			t.Fatal("registered socket was not physically closed at seal")
		}
	}
	late, peer := net.Pipe()
	defer peer.Close()
	if state.registerSocket(late) {
		t.Fatal("late socket registered after closure")
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := peer.Read(b[:]); err != io.EOF {
		t.Fatal("late dial not physically closed")
	}
}
