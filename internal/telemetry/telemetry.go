package telemetry

import (
	"encoding/json"
	"io"
	"sync/atomic"
	"time"
)

const (
	maxRecordBytes   = 16 << 10
	maxQueuedRecords = 1024
	maxQueuedBytes   = 8 << 20
	maxFieldBytes    = 100
)

// Logger accepts only operational metadata; it never accepts a request or raw error.
type Record struct {
	RequestID  string `json:"request_id,omitempty"`
	Stage      string `json:"stage"`
	Code       string `json:"code,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}
type Logger struct {
	queue   chan []byte
	queued  atomic.Int64
	Dropped atomic.Uint64
	done    chan struct{}
}

func New(w io.Writer) *Logger {
	l := &Logger{queue: make(chan []byte, maxQueuedRecords), done: make(chan struct{})}
	go func() {
		defer close(l.done)
		for b := range l.queue {
			l.queued.Add(-int64(len(b)))
			if v, ok := w.(interface{ SetWriteDeadline(time.Time) error }); ok {
				v.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
			}
			if n, err := w.Write(b); err != nil || n != len(b) {
				l.Dropped.Add(1)
			}
		}
	}()
	return l
}
func (l *Logger) Write(v Record) {
	if len(v.RequestID) > maxFieldBytes || len(v.Stage) > maxFieldBytes || len(v.Code) > maxFieldBytes {
		l.Dropped.Add(1)
		return
	}
	b, e := json.Marshal(v)
	if e != nil || len(b)+1 > maxRecordBytes {
		l.Dropped.Add(1)
		return
	}
	l.enqueue(append(b, '\n'))
}

func (l *Logger) enqueue(b []byte) {
	if len(b) == 0 || len(b) > maxRecordBytes {
		l.Dropped.Add(1)
		return
	}
	n := int64(len(b))
	for {
		previous := l.queued.Load()
		if n > maxQueuedBytes-previous {
			l.Dropped.Add(1)
			return
		}
		if l.queued.CompareAndSwap(previous, previous+n) {
			break
		}
	}
	select {
	case l.queue <- b:
	default:
		l.queued.Add(-int64(len(b)))
		l.Dropped.Add(1)
	}
}
