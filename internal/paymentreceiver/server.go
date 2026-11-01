package paymentreceiver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/moni-ai/moni-aigate/internal/paymentruntime"
)

type RuntimeConfig struct {
	config paymentruntime.Config
	valid  bool
}

func (RuntimeConfig) String() string   { return "paymentreceiver.RuntimeConfig" }
func (RuntimeConfig) GoString() string { return "paymentreceiver.RuntimeConfig" }
func (RuntimeConfig) MarshalJSON() ([]byte, error) {
	return []byte(`"paymentreceiver.RuntimeConfig"`), nil
}
func LoadRuntimeConfig(ctx context.Context, path string) (RuntimeConfig, error) {
	c, e := paymentruntime.Load(ctx, path, paymentruntime.Receiver)
	if e != nil {
		return RuntimeConfig{}, failure("receiver_unavailable")
	}
	return RuntimeConfig{config: c, valid: true}, nil
}

type Server struct {
	runtime         *paymentruntime.Runtime
	handler         *receiverHTTP
	mu              sync.Mutex
	started, closed bool
	cancel          context.CancelFunc
	done            chan struct{}
	requests        sync.WaitGroup
}

func New(ctx context.Context, c RuntimeConfig) (*Server, error) {
	if !c.valid {
		return nil, failure("receiver_unavailable")
	}
	rt, e := paymentruntime.Open(ctx, c.config)
	if e != nil {
		return nil, failure("receiver_unavailable")
	}
	r, e := newRepository(rt)
	if e != nil {
		_ = rt.Close(ctx)
		return nil, e
	}
	return &Server{runtime: rt, handler: newReceiverHTTP(r), done: make(chan struct{})}, nil
}
func (s *Server) Run(ctx context.Context) error {
	if s == nil || s.runtime == nil {
		return failure("receiver_unavailable")
	}
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return failure("receiver_unavailable")
	}
	s.started = true
	life, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()
	defer close(s.done)
	defer cancel()
	if e := s.runtime.Check(life); e != nil {
		_ = s.runtime.Close(context.Background())
		return failure("mock_instance_unavailable")
	}
	origin, ok := s.runtime.Origin(paymentruntime.ReceiverHTTP)
	u, e := url.Parse(origin)
	if !ok || e != nil {
		_ = s.runtime.Close(context.Background())
		return failure("receiver_unavailable")
	}
	l, e := net.Listen("tcp4", u.Host)
	if e != nil {
		_ = s.runtime.Close(context.Background())
		return failure("receiver_unavailable")
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			httpError(w, 503, "receiver_unavailable")
			return
		}
		s.requests.Add(1)
		s.mu.Unlock()
		defer s.requests.Done()
		s.handler.ServeHTTP(w, r)
	})
	h := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 6 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return life }}
	served := make(chan error, 1)
	go func() { served <- h.Serve(l) }()
	var result error
	select {
	case <-life.Done():
	case <-s.runtime.Done():
		result = failure("mock_instance_unavailable")
	case err := <-served:
		if !errors.Is(err, http.ErrServerClosed) {
			result = failure("receiver_unavailable")
		}
		served = nil
	}
	cancel()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	if e = h.Shutdown(cleanup); e != nil {
		_ = h.Close()
	}
	if served != nil {
		<-served
	}
	// A cancelled BaseContext stops every in-flight transaction before releasing
	// ownership. Forced HTTP closure is followed by joining the handler work.
	joined := make(chan struct{})
	go func() { s.requests.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-cleanup.Done():
		_ = h.Close()
		<-joined
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	if e = s.runtime.Close(closeCtx); e != nil && result == nil {
		result = failure("receiver_unavailable")
	}
	return result
}
func (s *Server) Close(ctx context.Context) error {
	if s == nil || s.runtime == nil {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	started, cancel := s.started, s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !started {
		return s.runtime.Close(ctx)
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return failure("receiver_unavailable")
	}
}
