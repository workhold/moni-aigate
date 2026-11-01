package maildeliver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/config"
)

type smtpFixture struct {
	listener           net.Listener
	wg                 sync.WaitGroup
	mu                 sync.Mutex
	bodies             []string
	commands           []string
	final, rcpt        string
	implicit, offerTLS bool
	certificate        *tls.Config
	silent             bool
}

func newSMTPFixture(t *testing.T, mode string, final, rcpt string, silent bool) (*smtpFixture, *SMTP) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &smtpFixture{listener: l, final: final, rcpt: rcpt, silent: silent, implicit: mode == "implicit", offerTLS: mode == "starttls"}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "loopback-fixture"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	f.certificate = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	cfg := config.MailConfig{Transport: "smtp", Host: "127.0.0.1", Port: l.Addr().(*net.TCPAddr).Port, From: "noreply@fixture.invalid", TLSMode: mode}
	if mode == "no_starttls" {
		cfg.TLSMode = "starttls"
	}
	s, err := NewSMTP(cfg, "https://portal.fixture.invalid")
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	s.roots = x509.NewCertPool()
	s.roots.AddCert(root)
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		conn, e := l.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		if f.silent {
			var b [1]byte
			_, _ = conn.Read(b[:])
			return
		}
		var wire net.Conn = conn
		if f.implicit {
			tc := tls.Server(conn, f.certificate)
			if tc.Handshake() != nil {
				return
			}
			wire = tc
		}
		text := textproto.NewConn(wire)
		_ = text.PrintfLine("220 loopback fixture")
		for {
			line, e := text.ReadLine()
			if e != nil {
				return
			}
			f.mu.Lock()
			f.commands = append(f.commands, line)
			f.mu.Unlock()
			switch {
			case strings.HasPrefix(line, "EHLO "):
				if f.offerTLS {
					_ = text.PrintfLine("250-loopback fixture")
					_ = text.PrintfLine("250 STARTTLS")
				} else {
					_ = text.PrintfLine("250 loopback fixture")
				}
			case line == "STARTTLS":
				_ = text.PrintfLine("220 begin TLS")
				tc := tls.Server(conn, f.certificate)
				if tc.Handshake() != nil {
					return
				}
				text = textproto.NewConn(tc)
				f.offerTLS = false
			case strings.HasPrefix(line, "MAIL FROM:"):
				_ = text.PrintfLine("250 ok")
			case strings.HasPrefix(line, "RCPT TO:"):
				reply := f.rcpt
				if reply == "" {
					reply = "250 ok"
				}
				_ = text.PrintfLine("%s", reply)
			case line == "DATA":
				_ = text.PrintfLine("354 send data")
				body, e := text.ReadDotBytes()
				if e != nil {
					return
				}
				f.mu.Lock()
				f.bodies = append(f.bodies, string(body))
				f.mu.Unlock()
				reply := f.final
				if reply == "" {
					reply = "250 accepted"
				}
				_ = text.PrintfLine("%s", reply)
				return
			default:
				_ = text.PrintfLine("500 invalid command")
			}
		}
	}()
	t.Cleanup(func() { l.Close(); f.wg.Wait() })
	return f, s
}

func fixtureMessage() Message {
	return Message{ID: uuid.NewString(), To: "customer@fixture.invalid", Purpose: "registration", Link: "https://portal.fixture.invalid/register/complete#token=ic1_" + strings.Repeat("A", 43)}
}

func TestSMTPAcceptsFixedMessageWithVerifiedTransport(t *testing.T) {
	for _, mode := range []string{"plain_loopback", "starttls", "implicit"} {
		t.Run(mode, func(t *testing.T) {
			f, s := newSMTPFixture(t, mode, "", "", false)
			m := fixtureMessage()
			if err := s.Send(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			f.wg.Wait()
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.bodies) != 1 || !strings.Contains(f.bodies[0], "Message-ID: <"+m.ID+"@moni-aigate.local>") || !strings.Contains(f.bodies[0], m.Link) {
				t.Fatal("accepted DATA missing stable identity or fixed proof link")
			}
			for _, cmd := range f.commands {
				if cmd == "QUIT" {
					t.Fatal("post-accept QUIT should not control delivery outcome")
				}
			}
		})
	}
}

func TestSMTPFailuresNeverReflectServerText(t *testing.T) {
	for _, tc := range []struct {
		code  string
		retry bool
	}{{"451 synthetic-secret-token", true}, {"550 synthetic-secret-token", false}} {
		t.Run(tc.code[:3], func(t *testing.T) {
			_, s := newSMTPFixture(t, "plain_loopback", tc.code, "", false)
			err := s.Send(context.Background(), fixtureMessage())
			var failure *Failure
			if !errors.As(err, &failure) || failure.Retryable != tc.retry || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("unsafe failure: %v", err)
			}
		})
	}
}

func TestSMTPDoesNotDowngradeTLSOrAcceptUntrustedCertificate(t *testing.T) {
	_, s := newSMTPFixture(t, "no_starttls", "", "", false)
	err := s.Send(context.Background(), fixtureMessage())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != "mail_tls_required" || failure.Retryable {
		t.Fatalf("TLS downgrade: %v", err)
	}
	_, s = newSMTPFixture(t, "implicit", "", "", false)
	s.roots = x509.NewCertPool()
	err = s.Send(context.Background(), fixtureMessage())
	if !errors.As(err, &failure) || failure.Code != "mail_tls" || failure.Retryable {
		t.Fatalf("untrusted certificate accepted: %v", err)
	}
}

func TestSMTPCancellationClosesGreetingRead(t *testing.T) {
	_, s := newSMTPFixture(t, "plain_loopback", "", "", true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if s.Send(ctx, fixtureMessage()) == nil || time.Since(started) > time.Second {
		t.Fatal("cancel did not bound SMTP greeting")
	}
}

func TestSMTPRejectsPayloadBeforeNetwork(t *testing.T) {
	s, err := NewSMTP(config.MailConfig{Transport: "smtp", Host: "127.0.0.1", Port: 1, From: "noreply@fixture.invalid", TLSMode: "plain_loopback"}, "https://portal.fixture.invalid")
	if err != nil {
		t.Fatal(err)
	}
	for i, mutate := range []func(*Message){func(m *Message) { m.To += "\r\nBcc: attacker@fixture.invalid" }, func(m *Message) { m.ID = "bad\r\n" }, func(m *Message) { m.Purpose = "admin" }, func(m *Message) { m.Link = strings.ReplaceAll(m.Link, "portal.fixture.invalid", "attacker.invalid") }, func(m *Message) { m.Link += "&extra=secret" }} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			m := fixtureMessage()
			mutate(&m)
			err := s.Send(context.Background(), m)
			var f *Failure
			if !errors.As(err, &f) || f.Code != "mail_payload_invalid" {
				t.Fatalf("payload reached network: %v", err)
			}
		})
	}
}
