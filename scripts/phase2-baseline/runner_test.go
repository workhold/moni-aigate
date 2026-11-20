package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/testsupport/mockupstream"
)

func validConfig() Config {
	return Config{
		SchemaVersion: 1, RunID: "phase2-test",
		Source:  SourceConfig{CommitSHA: strings.Repeat("a", 40), TreeSHA256: strings.Repeat("b", 64), BuildCommand: "docker build"},
		Gateway: GatewayConfig{BaseURL: "http://gateway.test", APIKeyEnv: "PHASE2_TEST_KEY", Path: "/v1/chat/completions", Body: json.RawMessage(`{"model":"qa-text","stream":true,"messages":[{"role":"user","content":"load {{nonce}}"}],"max_tokens":2}`)},
		Mock:    MockConfig{ReceiptsURL: "http://mock.test/__mock/receipts"},
		Resources: ResourcesConfig{
			Application: ResourceConfig{CPU: "4", Memory: "8GiB"}, PostgreSQL: ResourceConfig{CPU: "2", Memory: "4GiB"},
			LoadGenerator: ResourceConfig{CPU: "1", Memory: "1GiB"}, Mock: ResourceConfig{CPU: "1", Memory: "1GiB"},
		},
		Load:            LoadShape{ConcurrentClients: 100, ExpectedActiveStreams: 100, WarmupSeconds: 60, SampleSeconds: 300, Repeats: 3, RotationOrder: []string{"moni", "moni", "moni"}, RequestTimeoutSeconds: 30, UpstreamFault: "slow_ttfb:25"},
		Connection:      ConnectionConfig{HTTPVersion: "HTTP/1.1", MaxIdleConnections: 100, MaxConnsPerHost: 100, ProviderRetries: 2, Plugins: "default", Cache: "disabled"},
		Timing:          TimingConfig{ClockSource: "single-test-clock"},
		CommercialChain: CommercialChain{NormalReleaseMain: true, Authentication: true, WalletHardReservation: true, KeyLimits: true, PersistentAttempts: true, FinalEvidence: true, UniqueSettlement: true, Audit: true},
	}
}

func validMockConfig() mockupstream.Config {
	return mockupstream.Config{Candidates: map[string]mockupstream.Candidate{"a": {Secret: "not-reported", Models: []string{"qa-text"}, OutputTokens: 8, TTFBMS: 5, EventIntervalMS: 2}}}
}

func TestConfigRejectsBypassAndCompetitorSubstitution(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(validMockConfig()); err != nil {
		t.Fatal(err)
	}
	bypass := validConfig()
	bypass.CommercialChain.WalletHardReservation = false
	if err := bypass.Validate(validMockConfig()); err == nil {
		t.Fatal("commercial bypass accepted")
	}
	competitor := validConfig()
	competitor.Load.RotationOrder[1] = "bifrost"
	if err := competitor.Validate(validMockConfig()); err == nil {
		t.Fatal("competitor execution was silently substituted")
	}
	keyNonce := validConfig()
	keyNonce.Gateway.Body = json.RawMessage(`{"model":"qa-text","stream":true,"messages":[{"role":"user","content":"load"}],"{{nonce}}":"not-a-value"}`)
	if err := keyNonce.Validate(validMockConfig()); err == nil {
		t.Fatal("nonce placeholder in a JSON key was accepted")
	}
}

func TestChatStreamContentBoundary(t *testing.T) {
	prefix := ": heartbeat\n\ndata: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"completion_tokens\":1}}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call\"}]}}]}\n\n"
	for _, tc := range []struct {
		name, delta string
		content     bool
	}{
		{"none", "", false},
		{"text", `{"content":"hello"}`, true},
		{"refusal", `{"refusal":"cannot"}`, true},
		{"tool name", `{"tool_calls":[{"function":{"name":"lookup"}}]}`, true},
		{"tool arguments", `{"tool_calls":[{"function":{"arguments":"{}"}}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := prefix
			if tc.delta != "" {
				body += fmt.Sprintf("data: {\"choices\":[{\"delta\":%s}]}\n\n", tc.delta)
			}
			body += "data: [DONE]\n\n"
			calls := 0
			done, err := readChatStream(strings.NewReader(body), func() { calls++ })
			if tc.content {
				if err != nil || !done || calls != 1 {
					t.Fatalf("done=%v calls=%d err=%v", done, calls, err)
				}
			} else if err == nil || done {
				t.Fatalf("content-free stream was accepted: done=%v err=%v", done, err)
			}
		})
	}
}

func TestJoinReceiptsSubtractsWithinEachSample(t *testing.T) {
	ttfb1, ttfb2 := 100.0, 200.0
	samples := []Sample{{Nonce: "one", TTFBMS: &ttfb1}, {Nonce: "two", TTFBMS: &ttfb2}}
	first1, first2 := int64(90000), int64(10000)
	receipts := []mockupstream.Receipt{
		{Nonce: "one", FirstEventAfterUS: &first1},
		{Nonce: "two", FirstEventAfterUS: &first2},
	}
	joinReceipts(samples, receipts)
	if samples[0].ExtraTTFBMS == nil || *samples[0].ExtraTTFBMS != 10 || *samples[1].ExtraTTFBMS != 190 {
		t.Fatalf("wrong per-request extras: %+v", samples)
	}
	summary := summarize(samples, 1)
	extra := summary["latency_extra_ttfb"].(Distribution)
	if extra.P95MS != 190 {
		t.Fatalf("p95=%v; subtracting aggregate p95 would incorrectly produce 110", extra.P95MS)
	}
}

func TestDuplicateReceiptsRemainVisibleInUpstreamPostCount(t *testing.T) {
	samples := []Sample{{Nonce: "retry"}}
	receipts := []mockupstream.Receipt{{Nonce: "retry", Seq: 1}, {Nonce: "retry", Seq: 2}}
	joinReceipts(samples, receipts)
	if samples[0].JoinError == "" || samples[0].MockReceiptCount != 2 || len(samples[0].MockReceipts) != 2 {
		t.Fatalf("duplicate physical attempts were hidden: %+v", samples[0])
	}
	if got := summarize(samples, 1)["upstream_posts"]; got != 2 {
		t.Fatalf("upstream_posts=%v want=2", got)
	}
}

func TestPeakActiveStreamsUsesMockIntervals(t *testing.T) {
	start := time.Unix(1700000000, 0)
	makeSample := func(received, completed time.Time) Sample {
		return Sample{MockReceipt: &mockupstream.Receipt{Stream: true, ReceivedAt: received, CompletedAt: &completed}}
	}
	samples := []Sample{
		makeSample(start, start.Add(10*time.Second)),
		makeSample(start.Add(2*time.Second), start.Add(5*time.Second)),
		makeSample(start.Add(8*time.Second), start.Add(12*time.Second)),
		{MockReceipt: &mockupstream.Receipt{Stream: true, ReceivedAt: start}},
	}
	peak, incomplete := peakActiveStreams(samples)
	if peak != 2 || incomplete != 1 {
		t.Fatalf("peak=%d incomplete=%d", peak, incomplete)
	}
}

func TestExecuteOneRecordsClientTimingWithoutAcceptanceClaim(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-ID", "request-test")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	cfg := validConfig()
	cfg.Gateway.BaseURL = upstream.URL
	runner, err := NewRunner(cfg, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	sample := runner.executeOne(1, "sample", 0, 1)
	if !sample.Successful() || sample.TTFBMS == nil || sample.TTFCMS == nil || sample.FullMS < 0 || sample.RequestID != "request-test" || sample.EndedAt.IsZero() {
		t.Fatalf("invalid client sample: %+v", sample)
	}
}

func TestWriteReportIsExclusiveAndNotAcceptedWithoutEvidence(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "new-report")
	cfg := validConfig()
	start := time.Unix(1700000000, 0)
	timings := []RepeatTiming{{Repeat: 1, Target: "moni", WarmupWallSeconds: 60, SampleWallSeconds: 300}, {Repeat: 2, Target: "moni", WarmupWallSeconds: 60, SampleWallSeconds: 300}, {Repeat: 3, Target: "moni", WarmupWallSeconds: 60, SampleWallSeconds: 300}}
	var samples []Sample
	for repeat := 1; repeat <= 3; repeat++ {
		samples = append(samples, Sample{Repeat: repeat, Phase: "sample", Nonce: fmt.Sprintf("n-%d", repeat), StartedAt: start, EndedAt: start.Add(time.Second), HTTPStatus: 200, HTTPVersion: "HTTP/1.1", RequestID: fmt.Sprintf("r-%d", repeat), Done: true})
	}
	if err := WriteReport(output, cfg, validMockConfig(), samples, timings, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(output, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if json.Unmarshal(raw, &report) != nil || report["acceptance_status"] != "not_accepted" || report["competitor_comparison"] != "not_compared" || report["measurement_version"] != float64(2) || report["phase2_baseline_version"] != float64(1) {
		t.Fatalf("unexpected report: %s", raw)
	}
	samplesRaw, err := os.ReadFile(filepath.Join(output, "samples.jsonl"))
	if err != nil || !strings.Contains(string(samplesRaw), "n-1") || strings.Contains(string(samplesRaw), "not-reported") {
		t.Fatalf("raw samples missing or leaked mock secret: %s", samplesRaw)
	}
	if err := WriteReport(output, cfg, validMockConfig(), samples, timings, nil); err == nil {
		t.Fatal("historical report directory was overwritten")
	}
}

func TestResourceSummaryAndCorrectnessEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.jsonl")
	start := time.Unix(1700000000, 0)
	raw := fmt.Sprintf("{\"sampled_at\":%q,\"cpu_seconds\":1,\"rss_bytes\":100}\n{\"sampled_at\":%q,\"cpu_seconds\":3,\"rss_bytes\":200}\n", start.Format(time.RFC3339Nano), start.Add(2*time.Second).Format(time.RFC3339Nano))
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	summary, err := resourceSummary(path)
	if err != nil || summary["status"] != "collected" || summary["mean_cpu_percent"] != 100.0 || summary["peak_rss_bytes"] != uint64(200) {
		t.Fatalf("resource summary=%v err=%v", summary, err)
	}
	evidence := CorrectnessEvidence{Status: "passed", RequestsChecked: 2, LedgerBalanced: true}
	if !evidence.Passed(2) || evidence.Passed(1) {
		t.Fatal("correctness evidence request scope was not enforced")
	}
}
