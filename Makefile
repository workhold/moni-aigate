.PHONY: test console build check verify-mvp architecture-check architecture-refresh
architecture-check:
	go run ./scripts/check-architecture
architecture-refresh: export ARCHITECTURE_REVIEW_REASON = $(REASON)
architecture-refresh:
	@test -n "$$ARCHITECTURE_REVIEW_REASON" || { echo 'REASON must describe the change and its architectural impact' >&2; exit 1; }
	go run ./scripts/check-architecture --refresh --reviewed --reason "$$ARCHITECTURE_REVIEW_REASON"
console:
	cd web && npm ci && npm run build
build: console
	go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate
test:
	go test -race ./...
	cd web && npm test
check: architecture-check
	go vet ./...
	cd web && npm run build
verify-mvp:
	@test -n "$$TEST_DATABASE_URL" || { echo 'TEST_DATABASE_URL must point to a dedicated PostgreSQL test database' >&2; exit 1; }
	go run ./scripts/check-architecture
	npm --prefix web test
	npm --prefix web run build
	go test -race -p 1 ./... -count=1 -timeout 600s
	go vet ./...
	go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate
