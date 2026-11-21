package main

import (
	"bufio"
	"bytes"
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

// WriteMeasurement creates immutable measurement artifacts. Correctness is a
// separate phase because its evidence must bind the exact input and measurement
// report digests, which do not exist until load execution finishes.
func WriteMeasurement(output string, cfg Config, mockCfg mockupstream.Config, samples []Sample, timings []RepeatTiming, pages []mockReceiptPage) error {
	if err := os.Mkdir(output, 0750); err != nil {
		return fmt.Errorf("create exclusive report directory: %w", err)
	}
	rawSHA, err := writeSamples(filepath.Join(output, "samples.jsonl"), samples)
	if err != nil {
		return err
	}
	inputSHA, err := writeExclusiveJSONDigest(filepath.Join(output, "input.json"), cfg)
	if err != nil {
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
		"commercial_correctness_evidence": false,
	}
	totalWall := 0.0
	for _, timing := range timings {
		totalWall += timing.SampleWallSeconds
	}
	measurement := map[string]any{
		"measurement_version": measurementVersion, "phase2_baseline_version": phase2BaselineVersion,
		"run_id": cfg.RunID, "report_generated_at_utc": time.Now().UTC().Format(time.RFC3339Nano),
		"acceptance_status":      "awaiting_correctness",
		"performance_conclusion": "not_adjudicated", "competitor_comparison": "not_compared",
		"source": cfg.Source, "resources": resources, "mock": mockSummary(mockCfg, cfg.Load.UpstreamFault),
		"load": cfg.Load, "connection": cfg.Connection, "timing": cfg.Timing, "commercial_chain": cfg.CommercialChain,
		"input_sha256": inputSHA,
		"metric_definitions": map[string]string{
			"ttfb":       "client request start to first response body byte; headers are excluded",
			"ttfc":       "client request start to first complete useful chat SSE event; role-only, heartbeat, empty delta, usage-only, tool id/index and DONE are excluded; this is not tokenizer TTFT",
			"extra_ttfb": "per-request client TTFB minus that request's mock receive-to-first-event interval; negative or ambiguous joins are errors and excluded before nearest-rank aggregation",
			"extra_ttfc": "per-request client TTFC minus that request's mock receive-to-first-content interval; negative or ambiguous joins are errors and excluded before nearest-rank aggregation",
			"goodput":    "only HTTP 200 with valid content and complete DONE divided by measured sample wall time",
		},
		"raw_samples":   map[string]any{"file": "samples.jsonl", "sha256": rawSHA, "requests": len(samples), "sampled_requests": len(sampled), "warmup_requests": len(warmup)},
		"receipt_pages": pages, "repeats": repeats,
		"summary": summarize(sampled, totalWall), "warmup": summarize(warmup, 0),
		"acceptance_gates":       gates,
		"threshold_adjudication": map[string]any{"extra_ttfb_p95_max_ms": 50, "status": "not_adjudicated"},
		"unexecuted_boundaries":  []string{"Bifrost and Rust LiteLLM were not run or compared", "real supplier behavior and production performance are not certified", "TLS variants, cold/hot connection matrix, tools, long streams and slow clients require separate runs", "resource and correctness files are evidence inputs; template values alone are not execution"},
	}
	_, err = writeExclusiveJSONDigest(filepath.Join(output, "measurement.json"), measurement)
	return err
}

// FinalizeReport verifies the correctness evidence against immutable artifacts
// and exact request identities before creating the final report.
func FinalizeReport(output string, cfg Config) error {
	inputPath := filepath.Join(output, "input.json")
	inputRaw, err := os.ReadFile(inputPath)
	if err != nil {
		return err
	}
	expectedInput, err := encodeJSON(cfg)
	if err != nil || !bytes.Equal(inputRaw, expectedInput) {
		return fmt.Errorf("redacted input does not match the finalization config")
	}
	inputSHA := sha256Hex(inputRaw)
	measurementPath := filepath.Join(output, "measurement.json")
	measurementRaw, err := os.ReadFile(measurementPath)
	if err != nil {
		return err
	}
	measurementSHA := sha256Hex(measurementRaw)
	var report map[string]any
	if json.Unmarshal(measurementRaw, &report) != nil {
		return fmt.Errorf("invalid measurement report")
	}
	if report["run_id"] != cfg.RunID || report["input_sha256"] != inputSHA {
		return fmt.Errorf("measurement report run or input identity mismatch")
	}
	source, ok := report["source"].(map[string]any)
	if !ok || source["commit_sha"] != cfg.Source.CommitSHA || source["tree_sha256"] != cfg.Source.TreeSHA256 {
		return fmt.Errorf("measurement report source identity mismatch")
	}
	samplesRaw, err := os.ReadFile(filepath.Join(output, "samples.jsonl"))
	if err != nil {
		return err
	}
	rawSamples, ok := report["raw_samples"].(map[string]any)
	if !ok || rawSamples["sha256"] != sha256Hex(samplesRaw) {
		return fmt.Errorf("measurement report raw sample digest mismatch")
	}
	samples, err := decodeSamples(samplesRaw)
	if err != nil {
		return err
	}
	requests := requestIdentities(samples)
	correctness, evidence, err := correctnessEvidence(cfg.Correctness.EvidencePath)
	if err != nil {
		return err
	}
	passed := false
	if evidence != nil {
		if err := evidence.ValidateBinding(cfg, inputSHA, measurementSHA, requests); err != nil {
			return err
		}
		passed = evidence.Passed(cfg, inputSHA, measurementSHA, requests)
	}
	correctness["passed"] = passed
	gates, ok := report["acceptance_gates"].(map[string]any)
	if !ok {
		return fmt.Errorf("measurement report acceptance gates are missing")
	}
	gates["commercial_correctness_evidence"] = passed
	accepted := true
	for _, raw := range gates {
		passedGate, ok := raw.(bool)
		accepted = accepted && ok && passedGate
	}
	report["acceptance_gates"] = gates
	report["acceptance_status"] = map[bool]string{true: "evidence_complete_result_not_adjudicated", false: "not_accepted"}[accepted]
	report["correctness"] = correctness
	report["measurement_report"] = map[string]any{"file": "measurement.json", "sha256": measurementSHA, "correctness_binding": "correctness evidence report_sha256 references this immutable measurement report"}
	report["finalized_at_utc"] = time.Now().UTC().Format(time.RFC3339Nano)
	_, err = writeExclusiveJSONDigest(filepath.Join(output, "report.json"), report)
	return err
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

func decodeSamples(raw []byte) ([]Sample, error) {
	var samples []Sample
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var sample Sample
		err := decoder.Decode(&sample)
		if err == io.EOF {
			return samples, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode raw samples: %w", err)
		}
		samples = append(samples, sample)
	}
}

func encodeJSON(value any) ([]byte, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func writeExclusiveJSONDigest(path string, value any) (string, error) {
	raw, err := encodeJSON(value)
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return sha256Hex(raw), nil
}

func sha256Hex(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func correctnessEvidence(path string) (map[string]any, *CorrectnessEvidence, error) {
	if path == "" {
		return map[string]any{"status": "not_run"}, nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var evidence CorrectnessEvidence
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return nil, nil, fmt.Errorf("decode correctness evidence: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, nil, fmt.Errorf("correctness evidence contains trailing JSON")
	}
	return map[string]any{"status": evidence.Status, "evidence": evidence, "evidence_sha256": sha256Hex(raw)}, &evidence, nil
}
