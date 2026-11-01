package mockpsp

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

type Server struct {
	runtime         *paymentruntime.Runtime
	repository      *Repository
	mu              sync.Mutex
	started, closed bool
	cancel          context.CancelFunc
	done            chan struct{}
	requests        sync.WaitGroup
}

func New(ctx context.Context, c RuntimeConfig) (*Server, error) {
	if !c.valid {
		return nil, serverFailure("mock_unavailable")
	}
	rt, e := paymentruntime.Open(ctx, c.config)
	if e != nil {
		return nil, serverFailure("mock_unavailable")
	}
	r, e := newRepository(rt)
	if e != nil {
		_ = rt.Close(ctx)
		return nil, e
	}
	return &Server{runtime: rt, repository: r, done: make(chan struct{})}, nil
}
func (s *Server) Run(ctx context.Context) error {
	if s == nil || s.runtime == nil {
		return serverFailure("mock_unavailable")
	}
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return serverFailure("mock_unavailable")
	}
	s.started = true
	life, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()
	defer close(s.done)
	defer cancel()
	if e := s.runtime.Check(life); e != nil {
		_ = s.runtime.Close(context.Background())
		return serverFailure("mock_instance_unavailable")
	}
	runner, e := newCallbackRunner(s)
	if e != nil {
		_ = s.runtime.Close(context.Background())
		return e
	}
	var listeners []net.Listener
	for _, kind := range []paymentruntime.Endpoint{paymentruntime.API, paymentruntime.Control} {
		origin, ok := s.runtime.Origin(kind)
		u, err := url.Parse(origin)
		if !ok || err != nil {
			e = serverFailure("mock_unavailable")
			break
		}
		l, err := net.Listen("tcp4", u.Host)
		if err != nil {
			e = serverFailure("mock_unavailable")
			break
		}
		listeners = append(listeners, l)
	}
	if e != nil {
		for _, l := range listeners {
			_ = l.Close()
		}
		_ = s.runtime.Close(context.Background())
		return e
	}
	errorsCh := make(chan error, 2)
	var servers []*http.Server
	var serving sync.WaitGroup
	for i, l := range listeners {
		inner := newMockHTTP(s.runtime, s.repository, i == 1)
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				writeError(w, 503, "mock_unavailable")
				return
			}
			s.requests.Add(1)
			s.mu.Unlock()
			defer s.requests.Done()
			inner.ServeHTTP(w, r)
		})
		h := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 6 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return life }}
		servers = append(servers, h)
		serving.Add(1)
		go func() { defer serving.Done(); errorsCh <- h.Serve(l) }()
	}
	workersDone := make(chan struct{})
	go func() { runner.run(life); close(workersDone) }()
	var result error
	select {
	case <-life.Done():
	case <-s.runtime.Done():
		result = serverFailure("mock_instance_unavailable")
	case err := <-errorsCh:
		if !errors.Is(err, http.ErrServerClosed) {
			result = serverFailure("mock_unavailable")
		}
	}
	cancel()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	var shutdown sync.WaitGroup
	for _, h := range servers {
		shutdown.Add(1)
		go func() {
			defer shutdown.Done()
			if h.Shutdown(cleanup) != nil {
				_ = h.Close()
			}
		}()
	}
	shutdown.Wait()
	serving.Wait()
	joined := make(chan struct{})
	go func() { s.requests.Wait(); <-workersDone; close(joined) }()
	select {
	case <-joined:
	case <-cleanup.Done():
		for _, h := range servers {
			_ = h.Close()
		}
		<-joined
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	if e = s.runtime.Close(closeCtx); e != nil && result == nil {
		result = serverFailure("mock_unavailable")
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
		return serverFailure("mock_unavailable")
	}
}
