package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
)

func TestModelFixtureBarriersCancelAndJoin(t *testing.T) {
	for _, point := range []modelFixturePoint{modelBeforeResponse, modelBeforeFinal} {
		for _, cancelAtBarrier := range []bool{false, true} {
			f := newLifecycleModelFixture(context.Background(), &mockProvider{keys: []string{"synthetic-model-key"}})
			if f.arm(point) != nil || f.arm(point) == nil {
				t.Fatal("barrier must arm only once")
			}
			s := httptest.NewServer(f)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			t.Cleanup(func() { cancel(); f.close(); s.Close() })
			done := make(chan struct{})
			var response *http.Response
			var body []byte
			var requestErr error
			go func() {
				defer close(done)
				r, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.URL+"/v1/chat/completions", strings.NewReader(`{"model":"qa-chat","stream":true,"max_tokens":16,"messages":[{"role":"user","content":"synthetic"}]}`))
				r.Header.Set("Authorization", "Bearer synthetic-model-key")
				response, requestErr = http.DefaultClient.Do(r)
				if requestErr == nil {
					body, requestErr = io.ReadAll(response.Body)
					response.Body.Close()
				}
			}()
			select {
			case <-f.hit:
			case <-ctx.Done():
				t.Fatal("fixture never reached exact barrier")
			}
			if f.mock.counts().ChatPOSTs != 1 {
				t.Fatal("barrier did not observe one authenticated POST")
			}
			if cancelAtBarrier {
				f.close()
			} else {
				f.resume()
			}
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("fixture did not release HTTP handler")
			}
			f.close()
			s.Close()
			cancel()
			if !cancelAtBarrier {
				if requestErr != nil || response.StatusCode != 200 || !strings.Contains(string(body), "data: [DONE]") {
					t.Fatal("fixture did not return complete SSE")
				}
			}
		}
	}
}

func TestModelFixtureActualAdapterAcceptsFinalUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		f := newLifecycleModelFixture(ctx, &mockProvider{keys: []string{"synthetic-model-key"}})
		if f.arm(modelBeforeResponse) != nil {
			t.Fatal("fixture arm failed")
		}
		f.resume()
		s := httptest.NewServer(f)
		t.Cleanup(func() { cancel(); f.close(); s.Close() })
		profile := provider.Profile{Version: "test", Model: "qa-chat", Protocol: provider.ProtocolChat, ContextLimit: 120, InputTokenUpperBound: 100, OutputLimit: 20, DefaultOutputLimit: 16, OutputLimitField: "max_tokens", UsageSemantics: "final"}
		raw := `{"model":"qa-chat","messages":[{"role":"user","content":"synthetic"}],"max_tokens":16,"stream":false}`
		if stream {
			raw = strings.Replace(raw, `"stream":false`, `"stream":true`, 1)
		}
		parsed, err := provider.Parse(provider.ProtocolChat, []byte(raw), profile)
		if err != nil {
			t.Fatal(err)
		}
		_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
		n, _ := strconv.ParseUint(port, 10, 16)
		response, err := provider.NewClient(provider.ClientOptions{}).Do(ctx, provider.Endpoint{URL: s.URL + "/v1/chat/completions", APIKey: "synthetic-model-key", Model: "qa-chat", Network: provider.NetworkPolicy{AllowHTTP: true, AllowedCIDRs: []string{"127.0.0.1/32"}, AllowedPorts: []uint16{uint16(n)}}}, parsed)
		if err != nil {
			t.Fatal(err)
		}
		if stream {
			for {
				event, err := response.ReadEvent()
				if err != nil {
					t.Fatal(err)
				}
				if event.Kind == "done" {
					break
				}
			}
		} else if _, err = response.DecodeJSON(); err != nil {
			t.Fatal(err)
		}
		if response.Usage.Status != provider.UsageConfirmed || response.Usage.InputTokens != 4 || response.Usage.OutputTokens != 2 || response.Usage.TotalTokens != 6 || response.ProtocolOutcome != "completed" {
			t.Fatal("fixture usage was not accepted by actual adapter")
		}
		response.Close()
		f.close()
		s.Close()
		cancel()
	}
}

func TestModelLifecyclePrivateSeamRequiresSeparateFixture(t *testing.T) {
	for _, in := range []options{{identityFixture: true}, {voucherFixture: true}, {alertsFixture: true}} {
		in.modelLifecycle = func(context.Context, *privateModelLifecycle) error { return nil }
		var output strings.Builder
		if err := run(context.Background(), in, &output); err == nil || err.Error() != "model_lifecycle_requires_separate_fixture" || output.Len() != 0 {
			t.Fatal("model seam escaped fixture isolation")
		}
	}
	f := newLifecycleModelFixture(context.Background(), &mockProvider{keys: []string{"synthetic-model-key"}})
	defer f.close()
	if f.arm(modelFixturePoint(42)) == nil || f.arm(modelBeforeResponse) != nil {
		t.Fatal("invalid barrier accepted")
	}
	for _, raw := range []string{`invalid`, `{"model":"other","max_tokens":16,"messages":[1]}`, `{"model":"qa-chat","max_tokens":17,"messages":[1]}`} {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(raw))
		r.Header.Set("Authorization", "Bearer synthetic-model-key")
		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("fixture accepted malformed synthetic request")
		}
	}
	if f.mock.counts().ChatPOSTs != 0 {
		t.Fatal("invalid input reached physical model barrier")
	}
}
