package main

import (
	"sync"
	"time"
)

// These are the only fixture pause points. They are not CLI settings or hooks
// in production SMTP, and a barrier holds only its first physical connection.
type fixtureMailPoint uint8

const (
	mailBeforeBanner fixtureMailPoint = iota
	mailBeforeAcceptedReply
)

type fixtureMailBarrier struct {
	reached chan struct{}
	proceed chan struct{}
	claimed sync.Once
	opened  sync.Once
}

func (b *fixtureMailBarrier) release() { b.opened.Do(func() { close(b.proceed) }) }

func (f *mailFixture) barrierAt(point fixtureMailPoint) (*fixtureMailBarrier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if point > mailBeforeAcceptedReply || f.closed || f.ctx.Err() != nil || len(f.connections) != 0 || len(f.messages) != 0 || f.barriers[point] != nil {
		return nil, stage("mail_fixture_barrier_invalid")
	}
	b := &fixtureMailBarrier{reached: make(chan struct{}), proceed: make(chan struct{})}
	f.barriers[point] = b
	return b, nil
}

func (f *mailFixture) pauseAt(point fixtureMailPoint) bool {
	f.mu.Lock()
	b := f.barriers[point]
	f.mu.Unlock()
	if b == nil {
		return f.ctx.Err() == nil
	}
	first := false
	b.claimed.Do(func() { first = true; close(b.reached) })
	if !first {
		return f.ctx.Err() == nil
	}
	// No fixture mutex is held while waiting. Close cancels first, then closes
	// sockets and joins every connection, including one paused before its banner.
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case <-f.ctx.Done():
		return false
	case <-timer.C:
		return false
	case <-b.proceed:
		return f.ctx.Err() == nil
	}
}

func (f *mailFixture) snapshot() ([]fixtureMail, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fixtureMail(nil), f.messages...), f.confirmed
}
