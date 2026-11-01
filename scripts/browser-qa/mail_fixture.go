package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type fixtureMail struct {
	Recipient string `json:"recipient"`
	Body      string `json:"body"`
}
type mailFixture struct {
	listener    net.Listener
	path        string
	mu          sync.Mutex
	accepted    int
	messages    []fixtureMail
	connections map[net.Conn]bool
	closed      bool
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	barriers    [2]*fixtureMailBarrier
	confirmed   int
}

func newMailFixture(folder string) (*mailFixture, error) {
	return newMailFixtureContext(context.Background(), folder)
}

func newMailFixtureContext(parent context.Context, folder string) (*mailFixture, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, stage("mail_fixture_listen_failed")
	}
	ctx, cancel := context.WithCancel(parent)
	f := &mailFixture{listener: listener, path: filepath.Join(folder, "mailbox-private.json"), connections: map[net.Conn]bool{}, ctx: ctx, cancel: cancel}
	if err = os.WriteFile(f.path, []byte("[]\n"), 0600); err != nil {
		listener.Close()
		cancel()
		return nil, stage("mail_fixture_private_file_failed")
	}
	f.wg.Add(1)
	go f.serve()
	return f, nil
}
func (f *mailFixture) serve() {
	defer f.wg.Done()
	for {
		c, err := f.listener.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		if f.closed || len(f.connections) >= 4 {
			f.mu.Unlock()
			c.Close()
			continue
		}
		f.connections[c] = true
		f.wg.Add(1)
		f.mu.Unlock()
		go f.connection(c)
	}
}
func (f *mailFixture) connection(c net.Conn) {
	defer func() { c.Close(); f.mu.Lock(); delete(f.connections, c); f.mu.Unlock(); f.wg.Done() }()
	c.SetDeadline(time.Now().Add(25 * time.Second))
	scan := bufio.NewScanner(c)
	scan.Buffer(make([]byte, 4096), 16384)
	out := bufio.NewWriter(c)
	reply := func(s string) bool {
		if _, err := out.WriteString(s + "\r\n"); err != nil {
			return false
		}
		return out.Flush() == nil
	}
	if !f.pauseAt(mailBeforeBanner) {
		return
	}
	if !reply("220 local-moni-fixture ESMTP") {
		return
	}
	recipient := ""
	for scan.Scan() {
		line := scan.Text()
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO "), strings.HasPrefix(upper, "HELO "):
			if !reply("250 local-moni-fixture") {
				return
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			recipient = ""
			if !reply("250 sender accepted") {
				return
			}
		case strings.HasPrefix(upper, "RCPT TO:"):
			recipient = strings.TrimSpace(line[len("RCPT TO:"):])
			if len(recipient) > 300 || !reply("250 recipient accepted") {
				return
			}
		case upper == "DATA":
			if recipient == "" || !reply("354 end with dot") {
				return
			}
			var body strings.Builder
			ended := false
			for scan.Scan() {
				part := scan.Text()
				if part == "." {
					ended = true
					break
				}
				if strings.HasPrefix(part, "..") {
					part = part[1:]
				}
				if body.Len()+len(part) > 16384 {
					return
				}
				body.WriteString(part + "\r\n")
			}
			if !ended {
				return
			}
			f.mu.Lock()
			if f.closed || len(f.messages) >= 16 {
				f.mu.Unlock()
				reply("452 fixture mailbox full")
				return
			}
			f.messages = append(f.messages, fixtureMail{Recipient: recipient, Body: body.String()})
			raw, err := json.Marshal(f.messages)
			if err == nil {
				err = os.WriteFile(f.path, append(raw, '\n'), 0600)
			}
			if err == nil {
				f.accepted++
			}
			f.mu.Unlock()
			if err != nil {
				reply("451 fixture storage failed")
				return
			}
			if !f.pauseAt(mailBeforeAcceptedReply) {
				return
			}
			if !reply("250 accepted by local fixture") {
				return
			}
			f.mu.Lock()
			f.confirmed++
			f.mu.Unlock()
		case upper == "QUIT":
			reply("221 bye")
			return
		case upper == "RSET":
			recipient = ""
			if !reply("250 reset") {
				return
			}
		case upper == "NOOP":
			if !reply("250 ok") {
				return
			}
		default:
			if !reply("502 unsupported") {
				return
			}
		}
	}
}
func (f *mailFixture) close() (int, error) {
	f.cancel()
	f.mu.Lock()
	f.closed = true
	for c := range f.connections {
		c.Close()
	}
	f.mu.Unlock()
	f.listener.Close()
	f.wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	count := f.accepted
	f.messages = nil
	return count, os.Remove(f.path)
}
