package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/moni-ai/moni-aigate/internal/testsupport/mockupstream"
)

func WriteReport(output string, cfg Config, mockCfg mockupstream.Config, samples []Sample, timings []RepeatTiming, pages []mockReceiptPage) error {
	if err := os.Mkdir(output, 0750); err != nil {
		return fmt.Errorf("create exclusive report directory: %w", err)
	}
	rawSHA, err := writeSamples(filepath.Join(output, "samples.jsonl"), samples)
	if err != nil {
		return err
	}
	if err := writeExclusiveJSON(filepath.Join(output, "input.json"), cfg); err != nil {
		return err
	}
	resources := map[string]any{}
	for name, resource := range map[string]ResourceConfig{"application": cfg.Resources.Application, "postgresql": cfg.Resources.PostgreSQL, "load_generator": cfg.Resources.LoadGenerator, "mock": cfg.Resources.Mock} {
		summary, err := resourceSummary(resource.SamplesJSONL)
		if err != nil {
			return err
		}
		resources[name] = map[string]any{"quota": resource, "observed": summary}
	}
	correctness, err := correctnessReport(cfg.Correctness.EvidencePath)
	if err != nil {
		return err
	}
	if evidence, ok := correctness["evidence"].(CorrectnessEvidence); ok {
		correctness["passed"] = evidence.Passed(len(samples))
	}
	var sampled, warmup []Sample
	for _, sample := range samples {
		if sample.Phase == "sample" {
			sampled = append(sampled, sample)
		} else {
			warmup = append(warmup, sample)
		}
	}
	repeats := make([]map[string]any, 0, cfg.Load.Repeats)
	allActive := true
	allSampleSizes := true
	allTransport := true
	for repeat := 1; repeat <= cfg.Load.Repeats; repeat++ {
		var repeatSamples []Sample
		for _, sample := range sampled {
			if sample.Repeat == repeat {
				repeatSamples = append(repeatSamples, sample)
				if sample.HTTPVersion != cfg.Connection.HTTPVersion || sample.TLS != cfg.Connection.TLS || sample.RequestID == "" {
					allTransport = false
				}
			}
		}
		peak, incomplete := peakActiveStreams(repeatSamples)
		allActive = allActive && peak >= cfg.Load.ExpectedActiveStreams && incomplete == 0
		summary := summarize(repeatSamples, timings[repeat-1].SampleWallSeconds)
		ttfb := summary["latency_extra_ttfb"].(Distribution)
		ttfc := summary["latency_extra_ttfc"].(Distribution)
		allSampleSizes = allSampleSizes && ttfb.SampleCount >= 100 && ttfc.SampleCount >= 100
		repeats = append(repeats, map[string]any{
			"repeat": repeat, "target": timings[repeat-1].Target, "timing": timings[repeat-1],
			"results": summary, "peak_upstream_active_streams": peak,
			"incomplete_active_stream_receipts": incomplete,
		})
	}
	allResultsClean := true
	mockBehaviorMatched := true
	for _, sample := range samples {
		if !sample.Successful() || sample.JoinError != "" {
			allResultsClean = false
		}
		if len(sample.MockReceipts) == 0 {
			mockBehaviorMatched = false
		}
		for _, receipt := range sample.MockReceipts {
			if receipt.Fault != cfg.Load.UpstreamFault || receipt.Protocol != "chat" || !receipt.Stream {
				mockBehaviorMatched = false
			}
		}
	}
	resourcesComplete := true
	for _, raw := range resources {
		observed := raw.(map[string]any)["observed"].(map[string]any)
		if observed["status"] != "collected" || observed["sample_count"].(int) < 2 {
			resourcesComplete = false
		}
	}
	correctnessPassed, _ := correctness["passed"].(bool)
	gates := map[string]bool{
		"clean_exact_source":              cfg.Source.TreeClean,
		"fixed_application_4vcpu_8gib":    cfg.Resources.Application.Verified && cfg.Resources.Application.CPU == "4" && cfg.Resources.Application.Memory == "8GiB",
		"fixed_database_quota":            cfg.Resources.PostgreSQL.Verified,
		"all_runtime_quotas_verified":     cfg.Resources.Application.Verified && cfg.Resources.PostgreSQL.Verified && cfg.Resources.LoadGenerator.Verified && cfg.Resources.Mock.Verified,
		"three_independent_repeats":       cfg.Load.Repeats >= 3 && len(timings) == cfg.Load.Repeats,
		"minimum_warmup_and_sample":       minimumDurations(timings, 60, 300),
		"actual_active_streams":           allActive,
		"raw_samples_complete":            allResultsClean,
		"mock_behavior_matched":           mockBehaviorMatched,
		"stable_percentile_sample_sizes":  allSampleSizes,
		"transport_and_request_identity":  allTransport,
		"mock_client_clock_verified":      cfg.Timing.SkewVerified,
		"cpu_rss_observed":                resourcesComplete,
		"commercial_correctness_evidence": correctnessPassed,
	}
	accepted := true
	for _, passed := range gates {
		accepted = accepted && passed
	}
	totalWall := 0.0
	for _, timing := range timings {
		totalWall += timing.SampleWallSeconds
	}
	report := map[string]any{
		"measurement_version": measurementVersion, "phase2_baseline_version": phase2BaselineVersion,
		"run_id": cfg.RunID, "report_generated_at_utc": time.Now().UTC().Format(time.RFC3339Nano),
		"acceptance_status":      map[bool]string{true: "evidence_complete_result_not_adjudicated", false: "not_accepted"}[accepted],
		"performance_conclusion": "not_adjudicated", "competitor_comparison": "not_compared",
		"source": cfg.Source, "resources": resources, "mock": mockSummary(mockCfg, cfg.Load.UpstreamFault),
		"load": cfg.Load, "connection": cfg.Connection, "timing": cfg.Timing, "commercial_chain": cfg.CommercialChain,
		"metric_definitions": map[string]string{
			"ttfb":       "client request start to first response body byte; headers are excluded",
			"ttfc":       "client request start to first complete useful chat SSE event; role-only, heartbeat, empty delta, usage-only, tool id/index and DONE are excluded; this is not tokenizer TTFT",
			"extra_ttfb": "per-request client TTFB minus that request's mock receive-to-first-event interval, then nearest-rank aggregation",
			"extra_ttfc": "per-request client TTFC minus that request's mock receive-to-first-content interval, then nearest-rank aggregation",
			"goodput":    "only HTTP 200 with valid content and complete DONE divided by measured sample wall time",
		},
		"raw_samples":   map[string]any{"file": "samples.jsonl", "sha256": rawSHA, "requests": len(samples), "sampled_requests": len(sampled), "warmup_requests": len(warmup)},
		"receipt_pages": pages, "repeats": repeats,
		"summary": summarize(sampled, totalWall), "warmup": summarize(warmup, 0),
		"correctness": correctness, "acceptance_gates": gates,
		"threshold_adjudication": map[string]any{"extra_ttfb_p95_max_ms": 50, "status": "not_adjudicated"},
		"unexecuted_boundaries":  []string{"Bifrost and Rust LiteLLM were not run or compared", "real supplier behavior and production performance are not certified", "TLS variants, cold/hot connection matrix, tools, long streams and slow clients require separate runs", "resource and correctness files are evidence inputs; template values alone are not execution"},
	}
	return writeExclusiveJSON(filepath.Join(output, "report.json"), report)
}

func minimumDurations(timings []RepeatTiming, warmup, sample float64) bool {
	for _, timing := range timings {
		if timing.WarmupWallSeconds < warmup || timing.SampleWallSeconds < sample {
			return false
		}
	}
	return len(timings) >= 3
}

func writeSamples(path string, samples []Sample) (string, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	writer := bufio.NewWriter(io.MultiWriter(file, hash))
	encoder := json.NewEncoder(writer)
	for _, sample := range samples {
		if err := encoder.Encode(sample); err != nil {
			_ = file.Close()
			return "", err
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeExclusiveJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func correctnessReport(path string) (map[string]any, error) {
	if path == "" {
		return map[string]any{"status": "not_run", "passed": false}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var evidence CorrectnessEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return nil, fmt.Errorf("decode correctness evidence: %w", err)
	}
	digest := sha256.Sum256(raw)
	return map[string]any{"status": evidence.Status, "evidence": evidence, "evidence_sha256": hex.EncodeToString(digest[:])}, nil
}
