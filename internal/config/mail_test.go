package config

import (
	"strings"
	"testing"
)

func TestMailConfigurationBoundaries(t *testing.T) {
	valid := MailConfig{Transport: "smtp", Host: "smtp.example.test", Port: 587, From: "noreply@example.test", TLSMode: "starttls"}
	if err := valid.Validate("https://portal.example.test"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, origin string
		change       func(*MailConfig)
	}{
		{"bad_transport", "https://portal.example.test", func(c *MailConfig) { c.Transport = "http" }},
		{"no_port", "https://portal.example.test", func(c *MailConfig) { c.Port = 0 }},
		{"url_host", "https://portal.example.test", func(c *MailConfig) { c.Host = "https://smtp.example.test" }},
		{"host_injection", "https://portal.example.test", func(c *MailConfig) { c.Host = "smtp.example.test\r\nsecret" }},
		{"display_sender", "https://portal.example.test", func(c *MailConfig) { c.From = "Admin <noreply@example.test>" }},
		{"sender_injection", "https://portal.example.test", func(c *MailConfig) { c.From = "noreply@example.test\r\nBcc: secret" }},
		{"half_auth", "https://portal.example.test", func(c *MailConfig) { c.Username = "secret-user" }},
		{"weak_tls", "https://portal.example.test", func(c *MailConfig) { c.TLSMode = "none" }},
		{"dns_loopback", "https://portal.example.test", func(c *MailConfig) { c.TLSMode = "plain_loopback"; c.Host = "localhost" }},
		{"remote_plain", "https://portal.example.test", func(c *MailConfig) { c.TLSMode = "plain_loopback" }},
		{"origin_http", "http://portal.example.test", func(c *MailConfig) {}},
		{"origin_path", "https://portal.example.test/path", func(c *MailConfig) {}},
		{"origin_credentials", "https://secret@portal.example.test", func(c *MailConfig) {}},
		{"origin_query", "https://portal.example.test?secret=x", func(c *MailConfig) {}},
		{"origin_fragment", "https://portal.example.test#secret", func(c *MailConfig) {}},
		{"origin_empty_fragment", "https://portal.example.test#", func(c *MailConfig) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.change(&c)
			err := c.Validate(tc.origin)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid configuration or diagnostic boundary: %v", err)
			}
		})
	}
	for _, host := range []string{"127.0.0.1", "::1"} {
		c := valid
		c.Host = host
		c.TLSMode = "plain_loopback"
		if err := c.Validate("http://127.0.0.1:12345"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDisabledMailDropsUnusedCredentials(t *testing.T) {
	t.Setenv("MAIL_TRANSPORT", "disabled")
	t.Setenv("SMTP_USERNAME", "synthetic-user")
	t.Setenv("SMTP_PASSWORD", "synthetic-secret")
	c, err := loadMail("http://localhost:8080")
	if err != nil || c.Configured() || c.Username != "" || c.Password != "" {
		t.Fatal("disabled mail retained credentials or became available")
	}
}
