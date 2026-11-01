package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/operations"
)

type capacityTrace struct {
	id                                string
	upstream, release, contentRelease atomic.Int64
	start, first, firstContent, end   int64
	status, requestBytes              int
	responseBytes                     int
	err, rejection                    string
	success                           bool
}

type capacityObserver struct {
	samples, lockSamples, waiterSamples int64
	maxWaiters, maxPermits, peakGo      int
	maxWaitingQueryAgeMS                float64
	peakHeap, peakSys                   uint64
	peakRSS                             int64
	err                                 string
}

func capacityRSS() int64 {
	b, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n * 1024
}

func capacityDistribution(v []float64) map[string]float64 {
	if len(v) == 0 {
		return map[string]float64{}
	}
	sort.Float64s(v)
	p := func(f float64) float64 { return v[int(math.Ceil(f*float64(len(v))))-1] }
	return map[string]float64{"min_ms": v[0], "p50_ms": p(.50), "p95_ms": p(.95), "p99_ms": p(.99), "max_ms": v[len(v)-1]}
}

// TestCapacityConcurrentStreams is a reproducible local burst test, not a
// benchmark claim for the separately specified 4-vCPU/8-GB deployment target.
// Run explicitly; ordinary unit and integration runs skip this load.
func TestCapacityConcurrentStreams(t *testing.T) {
	if os.Getenv("MONI_CAPACITY_TEST") != "true" {
		t.Skip("set MONI_CAPACITY_TEST=true and TEST_DATABASE_URL to run the local capacity fixture")
	}
	const tenantCount, keysPerTenant = 5, 2
	total, dbPool := 100, 32
	for _, setting := range []struct {
		name   string
		target *int
		valid  []int
	}{
		{"MONI_CAPACITY_CONCURRENCY", &total, []int{10, 100}},
		{"MONI_CAPACITY_DB_POOL", &dbPool, []int{8, 32}},
	} {
		if raw := os.Getenv(setting.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || (value != setting.valid[0] && value != setting.valid[1]) {
				t.Fatalf("%s must be %d or %d", setting.name, setting.valid[0], setting.valid[1])
			}
			*setting.target = value
		}
	}
	perKey := total / (tenantCount * keysPerTenant)
	contentDelayMS := 0
	if raw := os.Getenv("MONI_CAPACITY_CONTENT_DELAY_MS"); raw != "" {
		var err error
		contentDelayMS, err = strconv.Atoi(raw)
		if err != nil || (contentDelayMS != 0 && contentDelayMS != 25) {
			t.Fatal("MONI_CAPACITY_CONTENT_DELAY_MS must be 0 or 25")
		}
	}
	epoch := time.Now()
	traces := make([]*capacityTrace, total)
	lookup := make(map[string]*capacityTrace, total)
	for i := range traces {
		traces[i] = &capacityTrace{id: "capacity-" + strconv.Itoa(i)}
		lookup[traces[i].id] = traces[i]
	}
	allArrived := make(chan struct{})
	releaseMock := make(chan struct{})
	var arrived, activeUpstream, peakUpstream atomic.Int32
	var badMockRequest atomic.Bool
	f := newGatewayFixtureWithPool(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Messages) != 1 || lookup[body.Messages[0].Content] == nil || body.Model != "provider-model" || !body.StreamOptions.IncludeUsage {
			badMockRequest.Store(true)
			w.WriteHeader(400)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		trace := lookup[body.Messages[0].Content]
		trace.upstream.Store(time.Since(epoch).Nanoseconds())
		current := activeUpstream.Add(1)
		defer activeUpstream.Add(-1)
		for previous := peakUpstream.Load(); current > previous && !peakUpstream.CompareAndSwap(previous, current); previous = peakUpstream.Load() {
		}
		if arrived.Add(1) == int32(total) {
			close(allArrived)
		}
		select {
		case <-releaseMock:
		case <-r.Context().Done():
			return
		}
		// The first event intentionally contains only a role. Reading this byte
		// must not satisfy the client-side first-useful-content measurement.
		trace.release.Store(time.Since(epoch).Nanoseconds())
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"local-result\",\"object\":\"chat.completion.chunk\",\"model\":\"provider-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n")
		w.(http.Flusher).Flush()
		if contentDelayMS != 0 {
			timer := time.NewTimer(time.Duration(contentDelayMS) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				return
			}
		}
		trace.contentRelease.Store(time.Since(epoch).Nanoseconds())
		_, _ = fmt.Fprintf(w, "data: %s\n\n", gatewayContent)
		w.(http.Flusher).Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", gatewayFinish, gatewayUsage)
	}}, nil, int32(dbPool))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var channel, operator, approver string
	for _, q := range []struct {
		sql  string
		dest *string
	}{
		{"SELECT id FROM channels LIMIT 1", &channel},
		{"SELECT id FROM users WHERE platform_role='finance_operator' LIMIT 1", &operator},
		{"SELECT id FROM users WHERE platform_role='finance_approver' LIMIT 1", &approver},
	} {
		if err := f.pool.QueryRow(ctx, q.sql).Scan(q.dest); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.server.Catalog.Publish(ctx, operator, f.model, 1, []catalog.BindingInput{{ChannelID: channel, UpstreamModel: "provider-model", Priority: 1, Weight: 1, MaxInflight: total}}); err != nil {
		t.Fatal(err)
	}
	// Five isolated tenants and ten keys avoid measuring expected Key/tenant
	// concurrency rejection: each Key carries at most ten and each tenant twenty.
	tokens := make([]string, 0, tenantCount*keysPerTenant)
	tenants := make([]string, 0, tenantCount)
	for i := range tenantCount {
		tenant := f.tenant
		if i > 0 {
			tenant = uuid.NewString()
			if _, err := f.pool.Exec(ctx, "INSERT INTO tenants(id,name) VALUES($1,$2)", tenant, fmt.Sprintf("capacity-%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		tenants = append(tenants, tenant)
		user := uuid.NewString()
		if _, err := f.pool.Exec(ctx, "INSERT INTO users(id,email,password_hash) VALUES($1,$2,'capacity fixture')", user, user+"@capacity.invalid"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", tenant, user); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, "INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2) ON CONFLICT DO NOTHING", tenant, f.model); err != nil {
			t.Fatal(err)
		}
		credit, err := f.server.Billing.CreateCredit(ctx, billing.CreditInput{TenantID: tenant, ActorID: operator, BusinessRef: uuid.NewString(), ProofRef: "receipt:capacity-fixture", Amount: 10000, IdempotencyKey: uuid.NewString(), RequestHMAC: "capacity-credit"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.server.Billing.ApproveCredit(ctx, billing.ApprovalInput{TenantID: tenant, OrderID: credit.ID, ActorID: approver}); err != nil {
			t.Fatal(err)
		}
		for range keysPerTenant {
			token := "mg_" + uuid.NewString()
			tokens = append(tokens, token)
			if _, err := f.pool.Exec(ctx, "INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'capacity','mg_capacity',$4,$5,10000)", uuid.NewString(), tenant, user, f.server.Config.Secrets.Digest(token), []string{f.model}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Warm and label all application DB connections. The separate
	// monitor connection only samples this run's sessions in pg_stat_activity.
	application := "moni-capacity-" + uuid.NewString()
	func() {
		for range dbPool {
			conn, err := f.pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			if _, err = conn.Exec(ctx, "SELECT set_config('application_name',$1,false)", application); err != nil {
				t.Fatal(err)
			}
		}
	}()
	if f.profile != nil {
		f.profile.warm(t, ctx, f.pool)
	}
	lockID := uuid.New()
	instance, err := operations.AcquireInstance(ctx, f.pool, int64(binary.BigEndian.Uint64(lockID[:8])))
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	f.server.Guard = instance.Check
	if f.profile != nil {
		f.server.Guard = f.profile.guard(instance.Check)
	}
	monitorConfig := f.pool.Config().ConnConfig.Copy()
	monitorConfig.RuntimeParams["application_name"] = "moni-capacity-monitor"
	monitor, err := pgx.ConnectConfig(ctx, monitorConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close(context.Background())
	var postgresVersion string
	if err = monitor.QueryRow(ctx, "SHOW server_version").Scan(&postgresVersion); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	rssBefore := capacityRSS()
	poolBefore := f.pool.Stat()
	observerDone := make(chan capacityObserver, 1)
	stopObserver := make(chan struct{})
	go func() {
		v := capacityObserver{peakHeap: before.HeapAlloc, peakSys: before.Sys, peakRSS: rssBefore, peakGo: runtime.NumGoroutine()}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopObserver:
				observerDone <- v
				return
			case <-ticker.C:
			}
			v.samples++
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			v.peakHeap = max(v.peakHeap, m.HeapAlloc)
			v.peakSys = max(v.peakSys, m.Sys)
			v.peakGo = max(v.peakGo, runtime.NumGoroutine())
			v.maxPermits = max(v.maxPermits, len(f.server.global))
			if v.samples%25 == 0 {
				v.peakRSS = max(v.peakRSS, capacityRSS())
			}
			queryCtx, queryCancel := context.WithTimeout(context.Background(), time.Second)
			var waiters int
			var queryAge float64
			err := monitor.QueryRow(queryCtx, `SELECT count(*) FILTER(WHERE wait_event_type='Lock'),coalesce(max(EXTRACT(EPOCH FROM(clock_timestamp()-query_start))*1000) FILTER(WHERE wait_event_type='Lock'),0)::float8 FROM pg_stat_activity WHERE application_name=$1`, application).Scan(&waiters, &queryAge)
			queryCancel()
			if err != nil {
				v.err = err.Error()
				continue
			}
			v.maxWaiters = max(v.maxWaiters, waiters)
			v.maxWaitingQueryAgeMS = max(v.maxWaitingQueryAgeMS, queryAge)
			v.waiterSamples += int64(waiters)
			if waiters > 0 {
				v.lockSamples++
			}
		}
	}()
	// Bound the barrier if a rejected request prevents all expected arrivals.
	barrierExpired := make(chan bool, 1)
	go func() {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		expired := false
		select {
		case <-allArrived:
		case <-timer.C:
			expired = true
		}
		close(releaseMock)
		barrierExpired <- expired
	}()
	start := make(chan struct{})
	var clients sync.WaitGroup
	client := &http.Client{Transport: &http.Transport{Proxy: nil, MaxConnsPerHost: total, MaxIdleConnsPerHost: total}, Timeout: 20 * time.Second}
	defer client.CloseIdleConnections()
	loadStart := time.Now()
	if f.profile != nil {
		f.profile.start(monitorConfig, application, func() bool { return arrived.Load() < int32(total) })
	}
	for i, trace := range traces {
		clients.Add(1)
		go func(i int, trace *capacityTrace) {
			defer clients.Done()
			<-start
			raw, _ := json.Marshal(map[string]any{"model": "retail-model", "messages": []map[string]string{{"role": "user", "content": trace.id}}, "max_tokens": 20, "stream": true, "stream_options": map[string]bool{"include_usage": false}})
			trace.requestBytes = len(raw)
			r, _ := http.NewRequest(http.MethodPost, f.http.URL+"/v1/chat/completions", bytes.NewReader(raw))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+tokens[i/perKey])
			trace.start = time.Since(epoch).Nanoseconds()
			response, err := client.Do(r)
			if err != nil {
				trace.err = err.Error()
				trace.end = time.Since(epoch).Nanoseconds()
				return
			}
			defer response.Body.Close()
			trace.status = response.StatusCode
			first := make([]byte, 1)
			n, err := io.ReadFull(response.Body, first)
			if err != nil {
				trace.err = err.Error()
				trace.end = time.Since(epoch).Nanoseconds()
				return
			}
			trace.first = time.Since(epoch).Nanoseconds()
			if response.StatusCode == 200 {
				counter := &capacityByteCounter{}
				done, readErr := capacityReadStream(io.TeeReader(io.MultiReader(bytes.NewReader(first[:n]), response.Body), counter), func() {
					trace.firstContent = time.Since(epoch).Nanoseconds()
				})
				trace.responseBytes = counter.n
				trace.success = done && trace.firstContent > 0 && readErr == nil
				if readErr != nil {
					trace.err = readErr.Error()
				}
			} else {
				tail, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
				trace.responseBytes = n + len(tail)
				if readErr != nil {
					trace.err = readErr.Error()
				}
				var v struct{ Error struct{ Code string } }
				_ = json.Unmarshal(append(first[:n], tail...), &v)
				trace.rejection = v.Error.Code
			}
			trace.end = time.Since(epoch).Nanoseconds()
		}(i, trace)
	}
	close(start)
	clients.Wait()
	wall := time.Since(loadStart)
	if f.profile != nil {
		f.profile.stop(t)
	}
	close(stopObserver)
	observed := <-observerDone
	expired := <-barrierExpired
	runtime.ReadMemStats(&after)
	rssAfter := capacityRSS()
	poolAfter := f.pool.Stat()
	var extra, admission, egress, full, barrier []float64
	var firstContent, extraContent, contentEgress, byteToContent, excludedContent []float64
	accepted, success, networkErrors, minRequest, maxRequest := 0, 0, 0, math.MaxInt, 0
	rejections := map[string]int{}
	for _, trace := range traces {
		minRequest, maxRequest = min(minRequest, trace.requestBytes), max(maxRequest, trace.requestBytes)
		if trace.status == 200 {
			accepted++
		}
		if trace.success {
			success++
		}
		if trace.err != "" {
			networkErrors++
		}
		if trace.status >= 400 {
			rejections[fmt.Sprintf("%d:%s", trace.status, trace.rejection)]++
		}
		upstream, released := trace.upstream.Load(), trace.release.Load()
		if trace.first > 0 && released > 0 {
			mockWait := released - upstream
			extra = append(extra, float64(trace.first-trace.start-mockWait)/1e6)
			admission = append(admission, float64(upstream-trace.start)/1e6)
			egress = append(egress, float64(trace.first-released)/1e6)
			barrier = append(barrier, float64(mockWait)/1e6)
			full = append(full, float64(trace.end-trace.start)/1e6)
		}
		if emitted := trace.contentRelease.Load(); trace.firstContent > 0 && emitted > 0 {
			firstContent = append(firstContent, float64(trace.firstContent-trace.start)/1e6)
			extraContent = append(extraContent, float64(trace.firstContent-trace.start-(emitted-upstream))/1e6)
			contentEgress = append(contentEgress, float64(trace.firstContent-emitted)/1e6)
			byteToContent = append(byteToContent, float64(trace.firstContent-trace.first)/1e6)
			excludedContent = append(excludedContent, float64(emitted-upstream)/1e6)
		}
	}
	var settled, charges, held, keyHeld, keySpent int64
	if err = f.pool.QueryRow(context.Background(), "SELECT count(*),coalesce(sum(charged_amount),0) FROM settlements WHERE status='settled'").Scan(&settled, &charges); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(context.Background(), "SELECT coalesce(sum(balance),0) FROM ledger_accounts WHERE kind='HELD'").Scan(&held); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(context.Background(), "SELECT coalesce(sum(held),0),coalesce(sum(spent),0) FROM api_keys").Scan(&keyHeld, &keySpent); err != nil {
		t.Fatal(err)
	}
	ledger, err := f.server.Billing.VerifyLedger(context.Background(), billing.Query{})
	if err != nil {
		t.Fatal(err)
	}
	f.server.admission.mu.Lock()
	remainingAdmissionLanes, remainingAdmissionTickets := len(f.server.admission.lanes), 0
	for _, lane := range f.server.admission.lanes {
		remainingAdmissionTickets += len(lane.tickets)
	}
	f.server.admission.mu.Unlock()
	raceBuildSetting := "not_reported"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-race" {
				raceBuildSetting = setting.Value
			}
		}
	}
	report := map[string]any{
		"measurement_version": 2,
		"metric_definitions": map[string]string{
			"extra_first_byte":    "client first body byte - client start - (mock role emission - upstream arrival); excludes barrier only",
			"extra_first_content": "client first complete useful SSE event - client start - (mock content emission - upstream arrival); excludes mock barrier and simulated generation wait",
			"first_content":       "client start to first complete SSE event with nonempty text/refusal or tool function name/arguments; role, heartbeat, usage-only and empty delta excluded; not a real model/tokenizer TTFT",
		},
		"run_at_utc":                  time.Now().UTC().Format(time.RFC3339Nano),
		"environment":                 map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "go": runtime.Version(), "logical_cpus": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0), "postgres_version": postgresVersion, "application_db_pool": dbPool, "dedicated_instance_connections": 1, "max_transaction_connections": dbPool - 1, "instance_guard": "operations.Instance.Check: dedicated session ping at API entry and before provider dispatch", "race_build_setting": raceBuildSetting, "target_environment_verified": false, "measurement_scope": "gateway+load-client+mock-upstream in one go test process; PostgreSQL is a separate local process"},
		"load":                        map[string]any{"requests": total, "concurrent_clients": total, "tenants": tenantCount, "keys_per_tenant": keysPerTenant, "concurrency_per_key": perKey, "concurrency_per_tenant": keysPerTenant * perKey, "request_bytes_min": minRequest, "request_bytes_max": maxRequest, "mock_generation_delay_ms": contentDelayMS, "mock_stream_shape": "role-only event, optional content delay, content event, finish, usage, DONE", "barrier_timeout": expired},
		"results":                     map[string]any{"accepted": accepted, "successful_done": success, "acceptance_rate": float64(accepted) / float64(total), "rejections": rejections, "network_errors": networkErrors, "upstream_posts": f.posts.Load(), "peak_upstream_concurrency": peakUpstream.Load(), "sampled_peak_gateway_permits": observed.maxPermits, "wall_ms": float64(wall) / 1e6, "burst_completions_per_second": float64(success) / wall.Seconds(), "settlements": settled, "charged_microcurrency": charges, "remaining_hold_microcurrency": held, "key_remaining_hold_microcurrency": keyHeld, "key_spent_microcurrency": keySpent, "ledger_balanced": ledger.Balanced},
		"latency_extra_first_byte":    capacityDistribution(extra),
		"latency_extra_first_content": capacityDistribution(extraContent),
		"latency_client_to_first_content_including_mock_waits": capacityDistribution(firstContent),
		"latency_mock_content_to_client_first_content":         capacityDistribution(contentEgress),
		"latency_client_first_byte_to_first_content":           capacityDistribution(byteToContent),
		"excluded_mock_wait_to_content":                        capacityDistribution(excludedContent),
		"content_observations":                                 len(firstContent),
		"latency_client_to_upstream_admission":                 capacityDistribution(admission),
		"latency_mock_ready_to_client_first_byte":              capacityDistribution(egress),
		"latency_full_including_barrier":                       capacityDistribution(full),
		"excluded_mock_barrier_wait":                           capacityDistribution(barrier),
		"database":                                             map[string]any{"pool_acquire_count": poolAfter.AcquireCount() - poolBefore.AcquireCount(), "pool_acquire_duration_ms": float64(poolAfter.AcquireDuration()-poolBefore.AcquireDuration()) / 1e6, "pool_empty_acquires": poolAfter.EmptyAcquireCount() - poolBefore.EmptyAcquireCount(), "lock_sampling_interval_ms": 10, "samples": observed.samples, "samples_with_lock_waiters": observed.lockSamples, "sum_waiters_across_samples": observed.waiterSamples, "max_simultaneous_lock_waiters": observed.maxWaiters, "max_query_age_while_waiting_ms": observed.maxWaitingQueryAgeMS, "monitor_error": observed.err},
		"memory":                                               map[string]any{"scope": "entire test process, sampled every 10ms; RSS every 250ms", "heap_before_bytes": before.HeapAlloc, "sampled_peak_heap_bytes": max(observed.peakHeap, after.HeapAlloc), "heap_after_bytes": after.HeapAlloc, "sampled_peak_go_sys_bytes": max(observed.peakSys, after.Sys), "rss_before_bytes": rssBefore, "sampled_peak_rss_bytes": max(observed.peakRSS, rssAfter), "rss_after_bytes": rssAfter, "allocated_during_load_bytes": after.TotalAlloc - before.TotalAlloc, "gc_cycles": after.NumGC - before.NumGC, "sampled_peak_goroutines": observed.peakGo},
	}
	results := report["results"].(map[string]any)
	results["remaining_admission_lanes"] = remainingAdmissionLanes
	results["remaining_admission_tickets"] = remainingAdmissionTickets
	results["remaining_runtime_permits"] = len(f.server.global)
	results["remaining_streams"] = f.server.Streams.Load()
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CAPACITY_REPORT\n%s", raw)
	if path := os.Getenv("MONI_CAPACITY_REPORT"); path != "" {
		file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if openErr != nil {
			t.Fatal(openErr)
		}
		_, err = file.Write(append(raw, '\n'))
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("write capacity report: %v / %v", err, closeErr)
		}
	}
	if observed.err != "" || badMockRequest.Load() || expired || accepted != total || success != total || len(firstContent) != total || networkErrors != 0 || int(f.posts.Load()) != total || int(peakUpstream.Load()) != total || settled != int64(total) || charges != int64(total)*20 || held != 0 || keyHeld != 0 || keySpent != charges || !ledger.Balanced || len(f.server.global) != 0 || f.server.Streams.Load() != 0 || remainingAdmissionLanes != 0 || remainingAdmissionTickets != 0 {
		t.Fatalf("capacity correctness failed; inspect CAPACITY_REPORT (no latency performance threshold is asserted)")
	}
	for _, tenant := range tenants {
		var count int
		if err = f.pool.QueryRow(context.Background(), "SELECT count(*) FROM request_records WHERE tenant_id=$1", tenant).Scan(&count); err != nil || count != keysPerTenant*perKey {
			t.Fatalf("tenant distribution count=%d err=%v", count, err)
		}
	}
}
