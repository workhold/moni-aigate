# Phase 2.1 deployed baseline runner

This directory implements the 032 slice-2.1 measurement entry point. It is a
load runner and evidence serializer, not an acceptance result.

## Scope and safety

- Executes only normal authenticated `POST /v1/chat/completions` SSE requests
  through the deployed release/main gateway. Configuration that disables any
  declared commercial-chain gate is rejected.
- Uses the 031 mock and joins its directive-gated receipts by a unique nonce.
  `slow_ttfb:N` and candidate `event_interval_ms` pin upstream behavior.
- Does not implement direct-upstream, Bifrost, or Rust LiteLLM targets. Their
  rotation entries are rejected; the report always says `not_compared` until
  separate equivalent evidence exists.
- Does not provision tenants, prices, routes, wallets or API keys. Reuse the
  normal deployment/business-QA provisioning path. The API key is read only
  from the environment variable named by `api_key_env` and is never serialized.
- Without `-execute`, the command only parses and validates local files. It does
  not contact the gateway or mock.

## Inputs

Copy `config.example.json` and `mock.example.json` to a run-specific location.
Replace all placeholder source identities and deployment addresses. Set at
least three repeats; the formal target remains 100 clients/actual active
streams, at least 60 seconds warmup and 300 seconds sampling per repeat.

Resource `verified` flags and `timing.skew_verified` must reflect actual runtime
checks, not copied template values. Application acceptance requires exactly
`cpu="4"`, `memory="8GiB"`; PostgreSQL, load-generator and mock quotas must also
be fixed and recorded. The runner has no permission to change deployment
resources.

CPU/RSS observations are separate JSONL files named by `samples_jsonl` for each
component. Each row is:

```json
{"sampled_at":"2026-01-01T00:00:00Z","cpu_seconds":10,"rss_bytes":1048576}
```

`cpu_seconds` is cumulative process/container CPU time. At least two monotonic
samples per component are required for the CPU/RSS acceptance gate. Example
rows in `resource-samples.example.jsonl` are format examples, not observations.

The mock measures receive-to-first-event/content as integer microseconds inside
its own process, preserving monotonic-clock semantics across JSON transport. The
runner subtracts only those durations; it never subtracts deserialized wall
clock timestamps. Raw client/mock timestamps remain diagnostic evidence. Nonce
joins require unique request identity.

## PostgreSQL correctness evidence

`correctness.evidence_path` points to a separate JSON file generated from the
dedicated PostgreSQL/database and application checks after the run. Its schema
is shown in `correctness.example.json`. A passing file must cover every warmup
and sampled request and demonstrate:

- no excess charge, balanced ledger and unique expected settlement;
- no residual holds or runtime/admission permits;
- no unauthorized dispatch or implicit/unknown-consumption replay.

The runner does not turn an example or self-declared file into database proof.
Missing evidence, a request-count mismatch, or any failed gate makes the report
`not_accepted`. Existing phase-1 PostgreSQL assertions can be reused by the
acceptance operator, but no such acceptance is run by the local unit tests.

## Run

From the repository root:

```sh
go run ./scripts/phase2-baseline -config /path/to/phase2-baseline.json
PHASE2_BASELINE_API_KEY='...' go run ./scripts/phase2-baseline \
  -config /path/to/phase2-baseline.json \
  -output /path/to/new-report-directory -execute
```

The output directory must not exist. The runner exclusively creates:

- `input.json`: redacted structured configuration (environment variable name,
  never its secret value);
- `samples.jsonl`: every warmup and sampled request, including failures,
  client timings, receipt join and per-request extra-latency fields;
- `report.json`: `measurement_version=2`, `phase2_baseline_version=1`, source,
  build, quotas, mock summary, connections, repeats, active-stream peaks,
  percentiles, goodput, CPU/RSS, correctness evidence and raw-sample SHA-256.

No historical report is overwritten. The runner does not adjudicate the 50 ms
threshold or competitor parity; even complete evidence is labelled
`evidence_complete_result_not_adjudicated` until reviewed.

## Metric boundaries

TTFB starts at client request start and ends at the first response body byte.
TTFC ends at the first complete SSE event containing nonempty text/refusal or a
tool function name/argument increment. Role-only, heartbeat, empty delta,
usage-only, tool ID/index-only and DONE events do not count.

For each request, extra TTFB/TTFC subtracts that same request's mock
receive-to-first-event/content interval. Only then are nearest-rank
p50/p95/p99 computed. Aggregate p95 values are never subtracted. Actual active
stream peaks are derived from mock receipt receive/completion intervals;
incomplete or ambiguous receipt joins block acceptance.

Current coverage is short OpenAI-chat SSE over the configured HTTP/TLS mode.
Other text protocols, tools, long streams, slow clients, cold/hot connection
matrices, real suppliers and production performance remain separate runs.
