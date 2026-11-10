# Current MVP browser smoke sidecar

`current-mvp-smoke.sh` is the current HTTP/WS equivalent inner smoke entry for an already deployed disposable environment. It is intentionally separate from the Go `browser-qa` package: that package retains private identity and model lifecycle fixtures for their own tests and is not evidence for the current MVP deployment path.

The sidecar requires this exact local test database declaration before it will run:

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'
```

The sidecar does not connect to that database. The value is a safety gate so a smoke command cannot silently drift to port 5432, another database, or a historical fixture. The target deployment is checked through HTTP and Realtime WebSocket using an existing current API key, model, and Realtime URL. The sidecar never calls `deployment-business-provision.sh` and does not create identities, tenants, keys, catalog, routes, prices, wallet credit, tasks, or provider facts.

Run it with an existing disposable deployment:

```sh
TEST_DATABASE_URL='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable' \
BROWSER_QA_BASE_URL='http://127.0.0.1:18080' \
BROWSER_QA_API_KEY="$API_KEY" \
BROWSER_QA_MODEL="$MODEL" \
BROWSER_QA_REALTIME_URL='ws://127.0.0.1:18080/v1/realtime' \
scripts/browser-qa/current-mvp-smoke.sh
```

The required assertions are `PASS text` and `PASS Realtime WebSocket`. Image, TTS, and async task checks can be enabled with the documented `BROWSER_QA_*` inputs; omitted optional checks remain explicit `SKIP` results. A successful sidecar run therefore proves only the current HTTP/WS inner smoke against the supplied deployment and references. It does not prove a real provider, browser automation, production deployment, or the full commercial guard matrix.

The existing deployment business runner remains the implementation of the protocol assertions. Its provisioning path is deliberately outside this sidecar; use `scripts/deployment-qa.sh` with `DEPLOY_QA_BUSINESS=true` only when a disposable Compose environment and fresh business fixture are explicitly required.
