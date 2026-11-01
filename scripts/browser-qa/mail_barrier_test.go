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

func fixtureSender(t testing.TB, f *mailFixture) (*maildeliver.SMTP, maildeliver.Message) {
	t.Helper()
	host, port, err := net.SplitHostPort(f.listener.Addr().String())
	if err != nil {
		t.Fatal("fixture address unavailable")
	}
	n, _ := strconv.Atoi(port)
	sender, err := maildeliver.NewSMTP(config.MailConfig{Transport: "smtp", Host: host, Port: n, From: "fixture@moni.invalid", TLSMode: "plain_loopback"}, "http://127.0.0.1:31001")
	if err != nil {
		t.Fatal("fixture sender unavailable")
	}
	message := maildeliver.Message{ID: uuid.NewString(), To: "barrier@qa.invalid", Purpose: "registration", Link: "http://127.0.0.1:31001/register/complete#token=ic1_" + security.Random(32)}
	return sender, message
}

func awaitMailBarrier(t testing.TB, b *fixtureMailBarrier) {
	t.Helper()
	select {
	case <-b.reached:
	case <-time.After(time.Second):
		t.Fatal("fixture physical barrier was not reached")
	}
}

func TestMailBarrierBeforeBannerAndOneShot(t *testing.T) {
	f, err := newMailFixture(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer f.close()
	b, err := f.barrierAt(mailBeforeBanner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.barrierAt(mailBeforeBanner); err == nil {
		t.Fatal("duplicate barrier accepted")
	}
	c, err := net.DialTimeout("tcp", f.listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("fixture connection failed")
	}
	defer c.Close()
	awaitMailBarrier(t, b)
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	_, err = bufio.NewReader(c).ReadString('\n')
	if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatal("fixture sent banner before release")
	}
	if messages, _ := f.snapshot(); len(messages) != 0 {
		t.Fatal("TCP accept was mistaken for a message")
	}
	b.release()
	b.release()
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "220 ") {
		t.Fatal("banner did not resume")
	}
	second, err := net.DialTimeout("tcp", f.listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("second fixture connection failed")
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	if line, err := bufio.NewReader(second).ReadString('\n'); err != nil || !strings.HasPrefix(line, "220 ") {
		t.Fatal("one-shot barrier paused another connection")
	}
}

func TestMailBarrierPersistsDataBeforeFinal250(t *testing.T) {
	f, err := newMailFixture(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer f.close()
	b, err := f.barrierAt(mailBeforeAcceptedReply)
	if err != nil {
		t.Fatal(err)
	}
	sender, message := fixtureSender(t, f)
	done := make(chan error, 1)
	go func() { done <- sender.Send(context.Background(), message) }()
	awaitMailBarrier(t, b)
	select {
	case <-done:
		t.Fatal("SMTP sender returned before the final 250")
	default:
	}
	messages, confirmed := f.snapshot()
	if len(messages) != 1 || confirmed != 0 {
		t.Fatal("DATA persistence was confused with an SMTP ACK")
	}
	raw, err := os.ReadFile(f.path)
	var persisted []fixtureMail
	if err != nil || json.Unmarshal(raw, &persisted) != nil || len(persisted) != 1 || persisted[0] != messages[0] {
		t.Fatal("DATA barrier preceded private mailbox persistence")
	}
	b.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("released SMTP did not accept")
		}
	case <-time.After(time.Second):
		t.Fatal("released SMTP did not join")
	}
	if err := sender.Send(context.Background(), message); err != nil {
		t.Fatal("one-shot DATA barrier paused a later message")
	}
	if messages, _ := f.snapshot(); len(messages) != 2 {
		t.Fatal("fixture lost physical duplicate")
	}
}

func TestMailBarriersCloseAndContextCancelJoin(t *testing.T) {
	for _, point := range []fixtureMailPoint{mailBeforeBanner, mailBeforeAcceptedReply} {
		for _, cancelOnly := range []bool{false, true} {
			name := "before_banner"
			if point == mailBeforeAcceptedReply {
				name = "before_final_250"
			}
			if cancelOnly {
				name += "_context"
			} else {
				name += "_close"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				f, err := newMailFixtureContext(ctx, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				closed := false
				defer func() {
					if !closed {
						f.close()
					}
				}()
				b, err := f.barrierAt(point)
				if err != nil {
					t.Fatal(err)
				}
				sender, message := fixtureSender(t, f)
				done := make(chan error, 1)
				go func() { done <- sender.Send(context.Background(), message) }()
				awaitMailBarrier(t, b)
				if cancelOnly {
					cancel()
				} else {
					closing := make(chan struct{})
					go func() { f.close(); close(closing) }()
					select {
					case <-closing:
						closed = true
					case <-time.After(time.Second):
						t.Fatal("fixture close did not cancel and join its barrier")
					}
				}
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("canceled barrier acknowledged SMTP")
					}
				case <-time.After(time.Second):
					t.Fatal("barrier cancellation did not close the physical connection")
				}
			})
		}
	}
}
