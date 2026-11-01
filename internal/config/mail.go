package config

import (
	"errors"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// MailConfig is private startup configuration, never a public capability DTO.
// It must not be formatted in logs: Username and Password are credentials.
type MailConfig struct {
	Transport, Host, From, Username, Password, TLSMode string
	Port                                               int
}

func loadMail(origin string) (MailConfig, error) {
	c := MailConfig{Transport: env("MAIL_TRANSPORT", "disabled"), Host: os.Getenv("SMTP_HOST"), From: os.Getenv("SMTP_FROM"), Username: os.Getenv("SMTP_USERNAME"), Password: os.Getenv("SMTP_PASSWORD"), TLSMode: env("SMTP_TLS_MODE", "starttls")}
	if c.Transport == "disabled" {
		// Keep no unused credentials in the live configuration.
		return MailConfig{Transport: "disabled"}, nil
	}
	c.Port, _ = strconv.Atoi(os.Getenv("SMTP_PORT"))
	if err := c.Validate(origin); err != nil {
		return MailConfig{}, err
	}
	return c, nil
}

func (c MailConfig) Configured() bool { return c.Transport == "smtp" }

func (c MailConfig) Validate(origin string) error {
	invalid := errors.New("invalid mail configuration")
	if c.Transport == "disabled" {
		return nil
	}
	if c.Transport != "smtp" || c.Port < 1 || c.Port > 65535 || !mailHost(c.Host) {
		return invalid
	}
	if len(c.From) > 254 || strings.IndexFunc(c.From, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return invalid
	}
	a, err := mail.ParseAddress(c.From)
	if err != nil || a.Name != "" || a.Address != c.From || !ascii(c.From) {
		return invalid
	}
	if (c.Username == "") != (c.Password == "") || len(c.Username) > 512 || len(c.Password) > 4096 || strings.ContainsAny(c.Username+c.Password, "\r\n\x00") {
		return invalid
	}
	if c.TLSMode != "starttls" && c.TLSMode != "implicit" && c.TLSMode != "plain_loopback" {
		return invalid
	}
	if c.TLSMode == "plain_loopback" && !literalMailLoopback(c.Host) {
		return invalid
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Opaque != "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Scheme+"://"+u.Host != origin {
		return invalid
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && literalMailLoopback(u.Hostname()) && c.TLSMode == "plain_loopback") {
		return invalid
	}
	if u.Port() != "" {
		n, e := strconv.Atoi(u.Port())
		if e != nil || n < 1 || n > 65535 {
			return invalid
		}
	}
	return nil
}

func mailHost(host string) bool {
	if host == "" || len(host) > 253 || strings.TrimSpace(host) != host || !ascii(host) {
		return false
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return a.Zone() == "" && !a.Is4In6()
	}
	if strings.ContainsAny(host, "/:@?#[]\\") || strings.IndexFunc(host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func literalMailLoopback(host string) bool { return host == "127.0.0.1" || host == "::1" }
func ascii(s string) bool {
	for _, r := range s {
		if r < 33 || r > 126 {
			return false
		}
	}
	return s != ""
}
func (c MailConfig) Address() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }
