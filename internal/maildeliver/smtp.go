// Package maildeliver sends already-created identity proofs. It has no database
// access and cannot create accounts, tokens, money, or model requests.
package maildeliver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/config"
)

type Message struct{ ID, To, Purpose, Link string }

// Failure contains only a fixed code; SMTP replies and recipients must not be
// included even when an upstream server reflects the original request.
type Failure struct {
	Code      string
	Retryable bool
}

func (e *Failure) Error() string { return e.Code }

type SMTP struct {
	configuration config.MailConfig
	origin        string
	roots         *x509.CertPool // Test trust roots; production uses the system pool.
}

func NewSMTP(c config.MailConfig, origin string) (*SMTP, error) {
	if c.Transport != "smtp" || c.Validate(origin) != nil {
		return nil, &Failure{Code: "mail_configuration"}
	}
	return &SMTP{configuration: c, origin: origin}, nil
}

var tokenFormat = regexp.MustCompile(`^ic1_[A-Za-z0-9_-]{43}$`)

func canonicalToken(value string) bool {
	if !tokenFormat.MatchString(value) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "ic1_"))
	return err == nil && len(raw) == 32 && "ic1_"+base64.RawURLEncoding.EncodeToString(raw) == value
}

func (s *SMTP) body(m Message) (string, error) {
	id, err := uuid.Parse(m.ID)
	if err != nil || id == uuid.Nil || id.String() != m.ID || len(m.To) > 254 || strings.ContainsAny(m.To, "\r\n\x00") {
		return "", &Failure{Code: "mail_payload_invalid"}
	}
	a, err := mail.ParseAddress(m.To)
	if err != nil || a.Name != "" || a.Address != m.To {
		return "", &Failure{Code: "mail_payload_invalid"}
	}
	for _, r := range m.To {
		if r < 33 || r > 126 {
			return "", &Failure{Code: "mail_payload_invalid"}
		}
	}
	path, subject := "", ""
	switch m.Purpose {
	case "registration":
		path = "/register/complete"
		subject = "Confirm your moni-aigate registration"
	case "password_reset":
		path = "/password/reset"
		subject = "Reset your moni-aigate password"
	default:
		return "", &Failure{Code: "mail_payload_invalid"}
	}
	u, err := url.Parse(m.Link)
	base, baseErr := url.Parse(s.origin)
	if err != nil || baseErr != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || u.Path != path || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || !strings.HasPrefix(u.Fragment, "token=") || !canonicalToken(strings.TrimPrefix(u.Fragment, "token=")) || u.RawFragment != "" {
		return "", &Failure{Code: "mail_payload_invalid"}
	}
	// Links are ASCII, template text is fixed, and all header values are checked.
	return fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%s@moni-aigate.local>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nOpen this link and explicitly confirm your request:\r\n%s\r\n\r\nIf you did not request this, ignore this email. The link expires shortly.\r\n", s.configuration.From, m.To, subject, m.ID, m.Link), nil
}

// deadlineConn bounds each IO as well as the total operation. Context cancel
// closes the underlying connection, including while smtp.NewClient reads 220.
type deadlineConn struct {
	net.Conn
	until time.Time
}

func (c *deadlineConn) next() time.Time {
	t := time.Now().Add(10 * time.Second)
	if c.until.Before(t) {
		return c.until
	}
	return t
}
func (c *deadlineConn) Read(p []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(c.next()); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}
func (c *deadlineConn) Write(p []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(c.next()); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

func mailFailure(err error) error {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		if reply.Code >= 500 && reply.Code < 600 {
			return &Failure{Code: "mail_rejected", Retryable: false}
		}
		if reply.Code >= 400 && reply.Code < 500 {
			return &Failure{Code: "mail_temporary", Retryable: true}
		}
	}
	return &Failure{Code: "mail_network", Retryable: true}
}

func mailTLSFailure(err error) error {
	var invalid x509.CertificateInvalidError
	var host x509.HostnameError
	var unknown x509.UnknownAuthorityError
	if errors.As(err, &invalid) || errors.As(err, &host) || errors.As(err, &unknown) {
		return &Failure{Code: "mail_tls"}
	}
	var reply *textproto.Error
	if errors.As(err, &reply) {
		return mailFailure(err)
	}
	var network net.Error
	if errors.As(err, &network) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
		return &Failure{Code: "mail_tls", Retryable: true}
	}
	return &Failure{Code: "mail_tls"}
}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	body, err := s.body(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	until, _ := ctx.Deadline()
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", s.configuration.Address())
	if err != nil {
		return mailFailure(err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	bounded := &deadlineConn{Conn: conn, until: until}
	var wire net.Conn = bounded
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: s.configuration.Host, RootCAs: s.roots}
	if s.configuration.TLSMode == "implicit" {
		t := tls.Client(bounded, tlsConfig)
		if err = t.HandshakeContext(ctx); err != nil {
			return mailTLSFailure(err)
		}
		wire = t
	}
	client, err := smtp.NewClient(wire, s.configuration.Host)
	if err != nil {
		return mailFailure(err)
	}
	defer client.Close()
	if err = client.Hello("moni-aigate.local"); err != nil {
		return mailFailure(err)
	}
	if s.configuration.TLSMode == "starttls" {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return &Failure{Code: "mail_tls_required"}
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return mailTLSFailure(err)
		}
	}
	if s.configuration.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", s.configuration.Username, s.configuration.Password, s.configuration.Host)); err != nil {
			return mailFailure(err)
		}
	}
	if err = client.Mail(s.configuration.From); err != nil {
		return mailFailure(err)
	}
	if err = client.Rcpt(m.To); err != nil {
		return mailFailure(err)
	}
	data, err := client.Data()
	if err != nil {
		return mailFailure(err)
	}
	if _, err = io.WriteString(data, body); err != nil {
		return mailFailure(err)
	}
	if err = data.Close(); err != nil {
		return mailFailure(err)
	}
	// DATA final 250 is the acceptance point. QUIT failure cannot turn an
	// accepted message into a retry; close locally without another round trip.
	return nil
}
