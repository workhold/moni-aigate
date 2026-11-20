package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"time"

	"github.com/moni-ai/moni-aigate/internal/testsupport/mockupstream"
)

type Distribution struct {
	SampleCount int     `json:"sample_count"`
	MinMS       float64 `json:"min_ms,omitempty"`
	P50MS       float64 `json:"p50_ms,omitempty"`
	P95MS       float64 `json:"p95_ms,omitempty"`
	P99MS       float64 `json:"p99_ms,omitempty"`
	MaxMS       float64 `json:"max_ms,omitempty"`
}

func distribution(values []float64) Distribution {
	if len(values) == 0 {
		return Distribution{}
	}
	values = append([]float64(nil), values...)
	sort.Float64s(values)
	percentile := func(f float64) float64 { return values[int(math.Ceil(f*float64(len(values))))-1] }
	return Distribution{SampleCount: len(values), MinMS: values[0], P50MS: percentile(.50), P95MS: percentile(.95), P99MS: percentile(.99), MaxMS: values[len(values)-1]}
}

func joinReceipts(samples []Sample, receipts []mockupstream.Receipt) {
	byNonce := make(map[string][]mockupstream.Receipt)
	for _, receipt := range receipts {
		if receipt.Nonce != "" {
			byNonce[receipt.Nonce] = append(byNonce[receipt.Nonce], receipt)
		}
	}
	for i := range samples {
		matches := byNonce[samples[i].Nonce]
		samples[i].MockReceiptCount = len(matches)
		samples[i].MockReceipts = append([]mockupstream.Receipt(nil), matches...)
		if len(matches) != 1 {
			samples[i].JoinError = fmt.Sprintf("mock_receipt_count=%d", len(matches))
			continue
		}
		receipt := matches[0]
		samples[i].MockReceipt = &receipt
		if receipt.FirstEventAfterUS != nil && samples[i].TTFBMS != nil && *receipt.FirstEventAfterUS >= 0 {
			wait := float64(*receipt.FirstEventAfterUS) / 1000
			extra := *samples[i].TTFBMS - wait
			samples[i].MockWaitToFirstEventMS = &wait
			samples[i].ExtraTTFBMS = &extra
		} else if samples[i].FirstByteAt != nil {
			samples[i].JoinError = "mock_first_event_duration_missing"
		}
		if receipt.FirstContentAfterUS != nil && samples[i].TTFCMS != nil && *receipt.FirstContentAfterUS >= 0 {
			wait := float64(*receipt.FirstContentAfterUS) / 1000
			extra := *samples[i].TTFCMS - wait
			samples[i].MockWaitToContentMS = &wait
			samples[i].ExtraTTFCMS = &extra
		} else if samples[i].FirstContentAt != nil {
			samples[i].JoinError = "mock_first_content_duration_missing"
		}
	}
}

func summarize(samples []Sample, wallSeconds float64) map[string]any {
	var ttfb, ttfc, full, extraTTFB, extraTTFC []float64
	var successful, networkErrors, joinErrors, upstreamPosts int
	rejections := map[string]int{}
	for _, sample := range samples {
		if sample.Successful() {
			successful++
		}
		if sample.Error != "" {
			networkErrors++
		}
		if sample.JoinError != "" {
			joinErrors++
		}
		if sample.HTTPStatus >= 400 {
			rejections[fmt.Sprintf("%d:%s", sample.HTTPStatus, sample.Rejection)]++
		}
		upstreamPosts += sample.MockReceiptCount
		if sample.TTFBMS != nil {
			ttfb = append(ttfb, *sample.TTFBMS)
		}
		if sample.TTFCMS != nil {
			ttfc = append(ttfc, *sample.TTFCMS)
		}
		if sample.ExtraTTFBMS != nil {
			extraTTFB = append(extraTTFB, *sample.ExtraTTFBMS)
		}
		if sample.ExtraTTFCMS != nil {
			extraTTFC = append(extraTTFC, *sample.ExtraTTFCMS)
		}
		full = append(full, sample.FullMS)
	}
	goodput := 0.0
	if wallSeconds > 0 {
		goodput = float64(successful) / wallSeconds
	}
	return map[string]any{
		"requests": len(samples), "successful_done": successful, "errors": networkErrors,
		"rejections": rejections, "receipt_join_errors": joinErrors, "upstream_posts": upstreamPosts,
		"completed_goodput_per_second": goodput, "wall_seconds": wallSeconds,
		"latency_ttfb": distribution(ttfb), "latency_ttfc": distribution(ttfc),
		"latency_full": distribution(full), "latency_extra_ttfb": distribution(extraTTFB),
		"latency_extra_ttfc": distribution(extraTTFC),
	}
}

func peakActiveStreams(samples []Sample) (int, int) {
	type transition struct {
		at    time.Time
		delta int
	}
	var transitions []transition
	incomplete := 0
	for _, sample := range samples {
		if sample.MockReceipt == nil || !sample.MockReceipt.Stream {
			continue
		}
		if sample.MockReceipt.CompletedAt == nil || sample.MockReceipt.CompletedAt.Before(sample.MockReceipt.ReceivedAt) {
			incomplete++
			continue
		}
		transitions = append(transitions, transition{sample.MockReceipt.ReceivedAt, 1}, transition{*sample.MockReceipt.CompletedAt, -1})
	}
	sort.Slice(transitions, func(i, j int) bool {
		if transitions[i].at.Equal(transitions[j].at) {
			return transitions[i].delta < transitions[j].delta
		}
		return transitions[i].at.Before(transitions[j].at)
	})
	active, peak := 0, 0
	for _, transition := range transitions {
		active += transition.delta
		if active > peak {
			peak = active
		}
	}
	return peak, incomplete
}

type ResourceSample struct {
	SampledAt  time.Time `json:"sampled_at"`
	CPUSeconds float64   `json:"cpu_seconds"`
	RSSBytes   uint64    `json:"rss_bytes"`
}

func resourceSummary(path string) (map[string]any, error) {
	result := map[string]any{"status": "not_collected"}
	if path == "" {
		return result, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	_, _ = hash.Write([]byte{})
	var samples []ResourceSample
	scanner := bufio.NewScanner(io.TeeReader(file, hash))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var sample ResourceSample
		if json.Unmarshal(scanner.Bytes(), &sample) != nil || sample.SampledAt.IsZero() || sample.CPUSeconds < 0 {
			return nil, fmt.Errorf("invalid resource sample in %s", path)
		}
		samples = append(samples, sample)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	result["samples_sha256"] = hex.EncodeToString(hash.Sum(nil))
	result["sample_count"] = len(samples)
	if len(samples) == 0 {
		return result, nil
	}
	result["status"] = "collected"
	result["peak_rss_bytes"] = samples[0].RSSBytes
	var cpuTotal float64
	cpuIntervals := 0
	for i, sample := range samples {
		if sample.RSSBytes > result["peak_rss_bytes"].(uint64) {
			result["peak_rss_bytes"] = sample.RSSBytes
		}
		if i > 0 {
			elapsed := sample.SampledAt.Sub(samples[i-1].SampledAt).Seconds()
			cpu := sample.CPUSeconds - samples[i-1].CPUSeconds
			if elapsed <= 0 || cpu < 0 {
				return nil, fmt.Errorf("non-monotonic resource samples in %s", path)
			}
			percentage := 100 * cpu / elapsed
			cpuTotal += percentage
			cpuIntervals++
			if current, ok := result["peak_cpu_percent"].(float64); !ok || percentage > current {
				result["peak_cpu_percent"] = percentage
			}
		}
	}
	if cpuIntervals > 0 {
		result["mean_cpu_percent"] = cpuTotal / float64(cpuIntervals)
	}
	return result, nil
}

func durationMS(duration time.Duration) float64 { return float64(duration) / float64(time.Millisecond) }
