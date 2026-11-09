package routing

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
)

func candidate(id string, priority int, weight uint64) Candidate {
	return Candidate{CostPolicy: DefaultCostPolicy(), CostQuote: testCostQuote(id, "m", "", "unknown", 0, 0), ChannelID: id, VersionID: "v1", CredentialID: id + "-credential", Model: "m", Priority: priority, Weight: weight, MaxInflight: 10, Enabled: true}
}
func admit(context.Context, Candidate, int) error { return nil }
func plan(t *testing.T, c ...Candidate) *Plan {
	t.Helper()
	p, e := NewPlan(c, 2)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestPriorityWeightsAndNoRepeatedChannel(t *testing.T) {
	candidates := []Candidate{candidate("low", 0, 100), candidate("a", 10, 1), candidate("b", 10, 3)}
	for _, last := range []bool{false, true} {
		t.Run(strconv.FormatBool(last), func(t *testing.T) {
			engine := NewEngine(Config{RandN: func(n uint64) uint64 {
				if last {
					return n - 1
				}
				return 0
			}})
			p := plan(t, candidates...)
			a, e := p.Start(context.Background(), engine, admit)
			if e != nil {
				t.Fatal(e)
			}
			want := "a"
			if last {
				want = "b"
			}
			if a.Candidate.ChannelID != want {
				t.Fatalf("selected %s want %s", a.Candidate.ChannelID, want)
			}
			if _, e = p.Start(context.Background(), engine, admit); e != ErrAttemptInFlight {
				t.Fatal("overlapping attempts allowed", e)
			}
			a.Finish(Neutral)
			b, e := p.Start(context.Background(), engine, admit)
			if e != nil || b.Candidate.Priority != 10 || b.Candidate.ChannelID == want {
				t.Fatalf("did not use untried main-pool candidate: %+v %v", b, e)
			}
			b.Finish(Neutral)
			if _, e = p.Start(context.Background(), engine, admit); e != ErrAttemptBudget {
				t.Fatal("budget bypass", e)
			}
		})
	}
	engine := NewEngine(Config{})
	a := candidate("main", 10, 1)
	a.Enabled = false
	p := plan(t, a, candidate("backup", 0, 1))
	attempt, e := p.Start(context.Background(), engine, admit)
	if e != nil || attempt.Candidate.ChannelID != "backup" {
		t.Fatal(attempt, e)
	}
	attempt.Finish(Success)
}

func TestAdmissionFailureAndCommittedResponseCannotDispatch(t *testing.T) {
	engine := NewEngine(Config{})
	p := plan(t, candidate("a", 1, 1))
	denied := errors.New("revoked_at_dispatch_barrier")
	if _, e := p.Start(context.Background(), engine, func(context.Context, Candidate, int) error { return denied }); !errors.Is(e, denied) || p.Attempts() != 0 {
		t.Fatal(e, p.Attempts())
	}
	if engine.Snapshot("a", "a").Inflight != 0 {
		t.Fatal("failed admission leaked capacity")
	}
	a, e := p.Start(context.Background(), engine, admit)
	if e != nil {
		t.Fatal(e)
	}
	a.Finish(Neutral)
	p.CommitDownstream()
	if p.CanRetry(context.Background(), &provider.Error{Consumption: provider.ConsumptionNotSent, Class: provider.ClassNetwork}) {
		t.Fatal("retry after downstream commit")
	}
	if _, e = p.Start(context.Background(), engine, admit); e != ErrCommitted {
		t.Fatal(e)
	}
}

func TestAdmissionPanicReturnsUntransferredChannelAndHalfOpenLease(t *testing.T) {
	for _, halfOpen := range []bool{false, true} {
		t.Run(strconv.FormatBool(halfOpen), func(t *testing.T) {
			now := time.Unix(1, 0)
			engine := NewEngine(Config{Now: func() time.Time { return now }, FailureThreshold: 1, OpenDuration: 30 * time.Second})
			c := candidate("admission-panic", 1, 1)
			c.MaxInflight = 1
			if halfOpen {
				failed, err := plan(t, c).Start(context.Background(), engine, admit)
				if err != nil {
					t.Fatal(err)
				}
				failed.Finish(Failure)
				now = now.Add(31 * time.Second)
			}
			p := plan(t, c)
			marker := errors.New("panic after admission lease acquisition")
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				_, _ = p.Start(context.Background(), engine, func(context.Context, Candidate, int) error {
					h := engine.Snapshot(c.ChannelID, c.ChannelID)
					if h.Inflight != 1 || (halfOpen && (!h.ProbeInFlight || h.State != HalfOpen)) {
						t.Fatalf("panic did not occur while holding the intended lease: %+v", h)
					}
					panic(marker)
				})
			}()
			if recovered != marker {
				t.Fatalf("admission panic was swallowed or changed: %v", recovered)
			}
			h := engine.Snapshot(c.ChannelID, c.ChannelID)
			wantState, wantFailures := Closed, 0
			if halfOpen {
				wantState, wantFailures = HalfOpen, 1
			}
			if h.Inflight != 0 || h.ProbeInFlight || h.State != wantState || h.ConsecutiveFailures != wantFailures || p.Attempts() != 0 {
				t.Fatalf("admission panic leaked capacity, changed health or consumed an attempt: %+v attempts=%d", h, p.Attempts())
			}
			// The same plan has not tried this channel or consumed its attempt.
			// A fresh admission must be able to acquire the sole channel slot
			// and, when half-open, the sole probe token.
			next, err := p.Start(context.Background(), engine, admit)
			if err != nil || next.Sequence != 1 {
				t.Fatalf("successor could not admit after recovered panic: %+v err=%v", next, err)
			}
			if _, err = plan(t, c).Start(context.Background(), engine, admit); !errors.Is(err, ErrUnavailable) {
				t.Fatal("successfully transferred lease was prematurely released", err)
			}
			next.Finish(Success)
			next.Finish(Failure)
			h = engine.Snapshot(c.ChannelID, c.ChannelID)
			if h.Inflight != 0 || h.ProbeInFlight || h.State != Closed || h.ConsecutiveFailures != 0 || p.Attempts() != 1 {
				t.Fatalf("normal transfer/finish semantics changed: %+v attempts=%d", h, p.Attempts())
			}
		})
	}
}

func TestConcurrentCapacityIsReleasedOnceAfterExecution(t *testing.T) {
	engine := NewEngine(Config{})
	c := candidate("a", 1, 1)
	c.MaxInflight = 3
	var admissions atomic.Int32
	var wg sync.WaitGroup
	gate := make(chan struct{})
	attempts := make(chan *Attempt, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			p, e := NewPlan([]Candidate{c}, 1)
			if e != nil {
				return
			}
			a, e := p.Start(context.Background(), engine, func(context.Context, Candidate, int) error { admissions.Add(1); return nil })
			if e == nil {
				attempts <- a
			} else if e != ErrUnavailable {
				t.Errorf("unexpected admission: %v", e)
			}
		}()
	}
	close(gate)
	wg.Wait()
	close(attempts)
	if got := engine.Snapshot("a", "a").Inflight; got != 3 || admissions.Load() != 3 {
		t.Fatalf("inflight=%d admitted=%d", got, admissions.Load())
	}
	for a := range attempts {
		a.Candidate.ChannelID = "tampered"
		a.Finish(Neutral)
		a.Finish(Failure)
	}
	if engine.Snapshot("a", "a").Inflight != 0 {
		t.Fatal("lease identity mutable or released more than once")
	}
}

func TestHalfOpenNeutralDoesNotReopenOrProveSuccess(t *testing.T) {
	now := time.Unix(1, 0)
	engine := NewEngine(Config{Now: func() time.Time { return now }, FailureThreshold: 3, OpenDuration: 30 * time.Second})
	c := candidate("a", 1, 1)
	for i := 0; i < 3; i++ {
		a, e := plan(t, c).Start(context.Background(), engine, admit)
		if e != nil {
			t.Fatal(e)
		}
		a.Finish(Failure)
	}
	if engine.Snapshot("a", "a").State != Open {
		t.Fatal("not opened")
	}
	if _, e := plan(t, c).Start(context.Background(), engine, admit); e != ErrUnavailable {
		t.Fatal(e)
	}
	now = now.Add(31 * time.Second)
	a, e := plan(t, c).Start(context.Background(), engine, admit)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := plan(t, c).Start(context.Background(), engine, admit); e != ErrUnavailable {
		t.Fatal("two half-open requests admitted", e)
	}
	a.Finish(Neutral)
	h := engine.Snapshot("a", "a")
	if h.State != HalfOpen || h.ProbeInFlight || h.ConsecutiveFailures != 3 {
		t.Fatal(h)
	}
	b, e := plan(t, c).Start(context.Background(), engine, admit)
	if e != nil {
		t.Fatal(e)
	}
	b.Finish(Success)
	if h = engine.Snapshot("a", "a"); h.State != Closed || h.ConsecutiveFailures != 0 {
		t.Fatal(h)
	}
}

func TestOldCompletionsCannotUndoNewHealthGeneration(t *testing.T) {
	engine := NewEngine(Config{FailureThreshold: 1})
	c := candidate("a", 1, 1)
	a, _ := plan(t, c).Start(context.Background(), engine, admit)
	b, _ := plan(t, c).Start(context.Background(), engine, admit)
	a.Finish(Failure)
	b.Finish(Success)
	if engine.Snapshot("a", "a").State != Open {
		t.Fatal("old success closed newer breaker")
	}
	engine.SetQuarantined("a", true)
	if _, e := plan(t, c).Start(context.Background(), engine, admit); e != ErrUnavailable {
		t.Fatal("quarantine did not block", e)
	}
	engine.SetQuarantined("a", false)
	d, e := plan(t, c).Start(context.Background(), engine, admit)
	if e != nil {
		t.Fatal(e)
	}
	engine.SetQuarantined("a", true)
	d.Finish(Success)
	if h := engine.Snapshot("a", "a"); h.State != Quarantined || h.Inflight != 0 {
		t.Fatal(h)
	}
}

func TestFrozenCandidateAndSafeRetryBudgetWithRealHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limit"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	n, _ := strconv.Atoi(u.Port())
	ep := provider.Endpoint{URL: server.URL, Model: "upstream", Network: provider.NetworkPolicy{AllowHTTP: true, AllowedCIDRs: []string{"127.0.0.0/8"}, AllowedPorts: []uint16{uint16(n)}}}
	c1 := candidate("a", 2, 1)
	c2 := candidate("b", 1, 1)
	c1.Endpoint = ep
	c2.Endpoint = ep
	input := []Candidate{c1, c2}
	p, e := NewPlan(input, 2)
	if e != nil {
		t.Fatal(e)
	}
	input[0].Endpoint.URL = "http://169.254.169.254/"
	input[0].Endpoint.Network.AllowedCIDRs[0] = "0.0.0.0/0"
	profile := provider.Profile{Version: "v1", Model: "m", InputTokenUpperBound: 8, ContextLimit: 10, OutputLimit: 2, DefaultOutputLimit: 2, NoConsumptionStatuses: []int{429}}
	request, e := provider.ParseChat([]byte(`{"model":"m","messages":[{"role":"user","content":"x"}]}`), profile)
	if e != nil {
		t.Fatal(e)
	}
	engine := NewEngine(Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := provider.NewClient(provider.ClientOptions{})
	var sequences []int
	for {
		a, e := p.Start(ctx, engine, func(_ context.Context, c Candidate, n int) error {
			sequences = append(sequences, n)
			if c.Endpoint.URL != server.URL || c.Endpoint.Network.AllowedCIDRs[0] != "127.0.0.0/8" {
				return errors.New("snapshot changed")
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		response, callErr := client.Do(ctx, a.Candidate.Endpoint, request)
		if callErr != nil {
			a.Finish(Neutral)
			if !p.CanRetry(ctx, callErr) {
				t.Fatal(callErr)
			}
			continue
		}
		_, callErr = response.DecodeJSON()
		a.Finish(Success)
		if callErr != nil {
			t.Fatal(callErr)
		}
		break
	}
	if calls.Load() != 2 || p.Attempts() != 2 || len(sequences) != 2 || sequences[0] != 1 || sequences[1] != 2 {
		t.Fatal("wrong physical attempts", calls.Load(), p.Attempts(), sequences)
	}
	if p.CanRetry(ctx, &provider.Error{Class: provider.ClassNetwork, Consumption: provider.ConsumptionNotSent}) {
		t.Fatal("third attempt allowed")
	}
}

func TestInvalidPoliciesAndConsumptionUnknown(t *testing.T) {
	c := candidate("a", 1, 1)
	for _, bad := range [][]Candidate{nil, {c, c}, {func() Candidate { x := c; x.Weight = 0; return x }()}, {func() Candidate { x := c; x.MaxInflight = 0; return x }()}} {
		if _, e := NewPlan(bad, 2); e == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	if _, e := NewPlan([]Candidate{c}, 3); e == nil {
		t.Fatal("unbounded attempts accepted")
	}
	p := plan(t, c)
	if p.CanRetry(context.Background(), &provider.Error{Class: provider.ClassUpstream, Consumption: provider.ConsumptionUnknown}) {
		t.Fatal("health failure granted unsafe replay")
	}
}
