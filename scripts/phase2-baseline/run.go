package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moni-ai/moni-aigate/internal/testsupport/mockupstream"
)

type mockReceipt = mockupstream.Receipt

type RepeatTiming struct {
	Repeat            int     `json:"repeat"`
	Target            string  `json:"target"`
	WarmupWallSeconds float64 `json:"warmup_wall_seconds"`
	SampleWallSeconds float64 `json:"sample_wall_seconds"`
}

type Runner struct {
	cfg      Config
	client   *http.Client
	apiKey   string
	sequence atomic.Int64
}

func NewRunner(cfg Config, apiKey string) (*Runner, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("gateway API key environment variable is empty")
	}
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: cfg.Connection.DisableKeepAlives,
		MaxIdleConns: cfg.Connection.MaxIdleConnections, MaxIdleConnsPerHost: cfg.Connection.MaxIdleConnections,
		MaxConnsPerHost:   cfg.Connection.MaxConnsPerHost,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: cfg.Connection.HTTPVersion == "HTTP/2",
	}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Runner{cfg: cfg, client: client, apiKey: apiKey}, nil
}

func (r *Runner) Close() { r.client.CloseIdleConnections() }

func (r *Runner) Execute() ([]Sample, []RepeatTiming, []mockReceiptPage, error) {
	initial, err := r.receipts(0)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read initial mock receipts: %w", err)
	}
	var since uint64
	for _, receipt := range initial {
		if receipt.Seq > since {
			since = receipt.Seq
		}
	}
	var samples []Sample
	var timings []RepeatTiming
	for repeat := 1; repeat <= r.cfg.Load.Repeats; repeat++ {
		warmup, warmupWall := r.runPhase(repeat, "warmup", r.cfg.Load.WarmupSeconds)
		samples = append(samples, warmup...)
		sampled, sampleWall := r.runPhase(repeat, "sample", r.cfg.Load.SampleSeconds)
		samples = append(samples, sampled...)
		timings = append(timings, RepeatTiming{Repeat: repeat, Target: r.cfg.Load.RotationOrder[repeat-1], WarmupWallSeconds: warmupWall, SampleWallSeconds: sampleWall})
	}
	receipts, err := r.receipts(since)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read final mock receipts: %w", err)
	}
	joinReceipts(samples, receipts)
	return samples, timings, []mockReceiptPage{{Since: since, Count: len(receipts)}}, nil
}

type mockReceiptPage struct {
	Since uint64 `json:"since"`
	Count int    `json:"count"`
}

func (r *Runner) runPhase(repeat int, phase string, seconds int) ([]Sample, float64) {
	workers := r.cfg.Load.ConcurrentClients
	start := make(chan struct{})
	ready := sync.WaitGroup{}
	ready.Add(workers)
	finished := sync.WaitGroup{}
	finished.Add(workers)
	results := make([][]Sample, workers)
	wallStart := time.Now()
	deadline := wallStart.Add(time.Duration(seconds) * time.Second)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer finished.Done()
			ready.Done()
			<-start
			sequence := 0
			for time.Now().Before(deadline) {
				sequence++
				results[worker] = append(results[worker], r.executeOne(repeat, phase, worker, sequence))
			}
		}()
	}
	ready.Wait()
	close(start)
	finished.Wait()
	wall := time.Since(wallStart).Seconds()
	var samples []Sample
	for _, worker := range results {
		samples = append(samples, worker...)
	}
	return samples, wall
}

func (r *Runner) executeOne(repeat int, phase string, worker, workerSequence int) (sample Sample) {
	globalSequence := r.sequence.Add(1)
	nonce := fmt.Sprintf("%s-r%04d-%s-w%05d-s%010d-g%010d", r.cfg.RunID, repeat, phase, worker, workerSequence, globalSequence)
	started := time.Now()
	sample = Sample{Repeat: repeat, Phase: phase, Worker: worker, Sequence: globalSequence, Nonce: nonce, StartedAt: started}
	defer func() {
		sample.EndedAt = time.Now()
		sample.FullMS = durationMS(sample.EndedAt.Sub(started))
	}()
	directive := fmt.Sprintf("[[mock:nonce=%s]] [[mock:fault=%s]]", nonce, r.cfg.Load.UpstreamFault)
	body := bytes.Replace(r.cfg.Gateway.Body, []byte("{{nonce}}"), []byte(directive), 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(r.cfg.Load.RequestTimeoutSeconds)*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.Gateway.BaseURL+r.cfg.Gateway.Path, bytes.NewReader(body))
	if err != nil {
		sample.Error = "request_construction_failed"
		return sample
	}
	request.Header.Set("Authorization", "Bearer "+r.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Idempotency-Key", nonce)
	response, err := r.client.Do(request)
	if err != nil {
		sample.Error = err.Error()
		return sample
	}
	defer response.Body.Close()
	sample.HTTPStatus = response.StatusCode
	sample.HTTPVersion = response.Proto
	sample.TLS = response.TLS != nil
	sample.RequestID = response.Header.Get("X-Request-ID")
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		sample.Rejection = rejectionCode(body)
		return sample
	}
	timingReader := &byteTimingReader{reader: io.LimitReader(response.Body, 8<<20)}
	done, streamErr := readChatStream(timingReader, func() {
		now := time.Now()
		sample.FirstContentAt = &now
	})
	if timingReader.first != nil {
		sample.FirstByteAt = timingReader.first
		value := durationMS(timingReader.first.Sub(started))
		sample.TTFBMS = &value
	}
	if sample.FirstContentAt != nil {
		value := durationMS(sample.FirstContentAt.Sub(started))
		sample.TTFCMS = &value
	}
	sample.Done = done
	if streamErr != nil {
		sample.Error = streamErr.Error()
	}
	return sample
}

func (r *Runner) receipts(since uint64) ([]mockReceipt, error) {
	parsed, err := url.Parse(r.cfg.Mock.ReceiptsURL)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()
	query.Set("since", strconv.FormatUint(since, 10))
	parsed.RawQuery = query.Encode()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mock receipts HTTP %d", response.StatusCode)
	}
	var receipts []mockReceipt
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<20))
	if err := decoder.Decode(&receipts); err != nil {
		return nil, err
	}
	return receipts, nil
}
