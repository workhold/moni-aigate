package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockedWriter struct {
	entered chan struct{}
	release chan struct{}
	first   sync.Once
	resume  sync.Once
	writes  atomic.Int64
}

func (w *blockedWriter) Write(b []byte) (int, error) {
	w.first.Do(func() { close(w.entered) })
	<-w.release
	w.writes.Add(1)
	return len(b), nil
}

func (w *blockedWriter) unblock() { w.resume.Do(func() { close(w.release) }) }

func joinLogger(t *testing.T, l *Logger) {
	t.Helper()
	close(l.queue) // Tests own the logger; all producers have already joined.
	select {
	case <-l.done:
	case <-time.After(time.Second):
		t.Fatal("logger worker did not exit")
	}
}

func blockedLogger(t *testing.T) (*Logger, *blockedWriter) {
	t.Helper()
	w := &blockedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	l := New(w)
	l.Write(Record{Stage: "initial"})
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		w.unblock()
		joinLogger(t, l)
		t.Fatal("fixture did not block its writer")
	}
	t.Cleanup(func() {
		w.unblock()
		joinLogger(t, l)
		if l.queued.Load() != 0 {
			t.Error("queue byte accounting did not return to zero after drain")
		}
	})
	return l, w
}

func producerReturnsWhileBlocked(t *testing.T, w *blockedWriter, producer func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { producer(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		// Release the sink and join the finite producer before any cleanup can
		// close its queue. A blocking regression must not leak test goroutines.
		w.unblock()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("producer did not recover after unblocking the sink")
		}
		t.Fatal("Write waited for its blocked sink")
	}
}

func TestBlockedSinkDoesNotBlockProducerAndQueueCountIsBounded(t *testing.T) {
	l, sink := blockedLogger(t)
	const rejected = 19
	producerReturnsWhileBlocked(t, sink, func() {
		for range maxQueuedRecords + rejected {
			l.Write(Record{RequestID: "request", Stage: "http", Code: "200", DurationMS: 12})
		}
	})
	if sink.writes.Load() != 0 || len(l.queue) != maxQueuedRecords || l.Dropped.Load() != rejected {
		t.Fatalf("sink=%d queued=%d dropped=%d", sink.writes.Load(), len(l.queue), l.Dropped.Load())
	}
	var total int64
	buffers := make([][]byte, 0, maxQueuedRecords)
	for range maxQueuedRecords {
		b := <-l.queue
		total += int64(len(b))
		buffers = append(buffers, b)
	}
	if l.queued.Load() != total || total > maxQueuedBytes {
		t.Fatalf("inconsistent byte bound: queued=%d actual=%d", l.queued.Load(), total)
	}
	// Return records to the owned queue so normal worker accounting drains it.
	for _, b := range buffers {
		l.queue <- b
	}
}

func TestQueueByteBudgetIsAtomicUnderConcurrentProducers(t *testing.T) {
	l, sink := blockedLogger(t)
	// The current public metadata schema is smaller than 16KiB. Exercise the
	// independent byte-budget primitive with maximum-size encoded buffers so
	// its 8MiB bound is tested before the 1,024-record bound can mask it.
	payload := bytes.Repeat([]byte{'x'}, maxRecordBytes)
	const producers, perProducer = 32, 64
	producerReturnsWhileBlocked(t, sink, func() {
		var workers sync.WaitGroup
		for range producers {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for range perProducer {
					l.enqueue(payload)
				}
			}()
		}
		workers.Wait()
	})
	want := maxQueuedBytes / maxRecordBytes
	if l.queued.Load() != maxQueuedBytes || len(l.queue) != want || len(l.queue) > maxQueuedRecords || l.Dropped.Load() != uint64(producers*perProducer-want) || sink.writes.Load() != 0 {
		t.Fatalf("byte budget: bytes=%d records=%d dropped=%d writes=%d", l.queued.Load(), len(l.queue), l.Dropped.Load(), sink.writes.Load())
	}
}

func TestOversizedMetadataAndEncodedRecordsAreCountedAsDropped(t *testing.T) {
	l, sink := blockedLogger(t)
	producerReturnsWhileBlocked(t, sink, func() {
		l.Write(Record{Stage: strings.Repeat("s", maxFieldBytes+1)})
		l.Write(Record{RequestID: strings.Repeat("i", maxRecordBytes+1), Stage: "http"})
		l.enqueue(make([]byte, maxRecordBytes+1))
	})
	if l.Dropped.Load() != 3 || len(l.queue) != 0 || l.queued.Load() != 0 {
		t.Fatalf("oversized records were not dropped: dropped=%d count=%d bytes=%d", l.Dropped.Load(), len(l.queue), l.queued.Load())
	}
}

func TestRecordSchemaOnlyContainsFixedMetadata(t *testing.T) {
	allowed := map[string]bool{"request_id": true, "stage": true, "code": true, "duration_ms": true}
	typeOf := reflect.TypeFor[Record]()
	if typeOf.NumField() != len(allowed) {
		t.Fatal("Record schema changed: audit its metadata whitelist")
	}
	for i := range typeOf.NumField() {
		if !allowed[strings.Split(typeOf.Field(i).Tag.Get("json"), ",")[0]] {
			t.Fatalf("non-whitelisted record field: %s", typeOf.Field(i).Name)
		}
	}
	var r Record
	if err := json.Unmarshal([]byte(`{"request_id":"safe-id","stage":"http","code":"200","duration_ms":7,"body":"prompt-canary","secret":"secret-canary","headers":{"Authorization":"Bearer auth-canary"},"error":{"message":"upstream-canary"},"url":"https://url-canary.invalid"}`), &r); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	l := New(&output)
	l.Write(r)
	joinLogger(t, l)
	var decoded map[string]any
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || len(decoded) != len(allowed) || bytes.Contains(output.Bytes(), []byte("canary")) || bytes.Count(output.Bytes(), []byte{'\n'}) != 1 {
		t.Fatalf("metadata whitelist/JSON line boundary failed: %s %v", output.Bytes(), err)
	}
	for k := range decoded {
		if !allowed[k] {
			t.Fatalf("unexpected field %s", k)
		}
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

func TestSinkErrorsAndShortWritesIncrementDropCounter(t *testing.T) {
	for name, sink := range map[string]io.Writer{
		"error": writerFunc(func([]byte) (int, error) { return 0, errors.New("sink unavailable") }),
		"short": writerFunc(func(b []byte) (int, error) { return len(b) - 1, nil }),
	} {
		t.Run(name, func(t *testing.T) {
			l := New(sink)
			l.Write(Record{Stage: "http"})
			joinLogger(t, l)
			if l.Dropped.Load() != 1 || l.queued.Load() != 0 {
				t.Fatalf("sink failure not counted: dropped=%d bytes=%d", l.Dropped.Load(), l.queued.Load())
			}
		})
	}
}
