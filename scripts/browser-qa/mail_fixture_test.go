package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/maildeliver"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func TestIdentityMailFixtureAcceptsBoundedPrivateMessagesAndCleans(t *testing.T) {
	f, err := newMailFixture(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			f.close()
		}
	})
	host, port, err := net.SplitHostPort(f.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(port)
	sender, err := maildeliver.NewSMTP(config.MailConfig{Transport: "smtp", Host: host, Port: n, From: "fixture@moni.invalid", TLSMode: "plain_loopback"}, "http://127.0.0.1:31001")
	if err != nil {
		t.Fatal(err)
	}
	token := "ic1_" + security.Random(32)
	message := maildeliver.Message{ID: uuid.NewString(), To: "customer@qa.invalid", Purpose: "registration", Link: "http://127.0.0.1:31001/register/complete#token=" + token}
	// A repeated SMTP attempt deliberately keeps the same message identity; the
	// fixture records both, rather than pretending SMTP supplies exactly-once.
	for i := 0; i < 2; i++ {
		if err = sender.Send(context.Background(), message); err != nil {
			t.Fatal("local SMTP delivery failed")
		}
	}
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	var records []fixtureMail
	if json.Unmarshal(raw, &records) != nil || len(records) != 2 || !strings.Contains(records[0].Body, message.Link) || !strings.Contains(records[1].Body, "Message-ID: <"+message.ID+"@moni-aigate.local>") {
		t.Fatal("private fixture messages or stable identity missing")
	}
	info, err := os.Stat(f.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private mailbox permissions")
	}
	address := f.listener.Addr().String()
	count, err := f.close()
	closed = true
	if err != nil || count != 2 {
		t.Fatal("mail fixture cleanup/count failed")
	}
	if _, err = os.Stat(f.path); !os.IsNotExist(err) {
		t.Fatal("private mailbox survived cleanup")
	}
	c, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err == nil {
		c.Close()
		t.Fatal("mail fixture listener survived cleanup")
	}
}
func TestIdentityMailFixtureShutdownClosesActiveConnection(t *testing.T) {
	f, err := newMailFixture(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialTimeout("tcp", f.listener.Addr().String(), time.Second)
	if err != nil {
		f.close()
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "220") {
		f.close()
		t.Fatal("fixture banner not ready")
	}
	done := make(chan error, 1)
	go func() { _, err := f.close(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fixture close left active connection running")
	}
}
