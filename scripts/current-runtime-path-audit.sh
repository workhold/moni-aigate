#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/moni-aigate-runtime-audit.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

section() {
  printf '\n== %s ==\n' "$1"
}

section 'worktree'
git status --short --branch

section 'default dependency graph'
go list -deps ./cmd/moni-aigate >"$tmp/deps"
printf 'package_count=%s\n' "$(wc -l <"$tmp/deps" | tr -d ' ')"
printf '%s\n' 'legacy-shaped packages in the default graph:'
rg -n 'legacy|original|historical|compat|descriptor|fundinglot(init|prefixcompat|runtime)' "$tmp/deps" || true

section 'default entry assertions'
rg -n 'command :=|migrations\.(ApplyCurrent|VerifyCurrent)\(|command == "(migrate|provision|serve|worker)"' \
  cmd/moni-aigate/main.go
if rg -n 'migrations\.(ApplyThrough|VerifyThrough|Original22Manifest|VerifyOriginal22SnapshotTx)' cmd/moni-aigate; then
  echo 'ERROR: historical migration API reached the default command' >&2
  exit 1
fi
if ! rg -n 'migrations\.ApplyCurrent\(' cmd/moni-aigate/main.go >/dev/null; then
  echo 'ERROR: default migrate command no longer calls migrations.ApplyCurrent' >&2
  exit 1
fi
if ! rg -n 'migrations\.VerifyCurrent\(' cmd/moni-aigate/main.go >/dev/null; then
  echo 'ERROR: default serve/worker path no longer calls migrations.VerifyCurrent' >&2
  exit 1
fi

section 'compose and image entry assertions'
rg -n 'command: \[(migrate|provision|worker)\]|CMD \["serve"\]|ENTRYPOINT|go build .*cmd/moni-aigate' \
  compose.yaml Dockerfile Makefile

section 'historical and compatibility call sites'
rg -n 'ApplyThrough|VerifyThrough|Original22Manifest|VerifyOriginal22SnapshotTx|LegacyV1|WalletReadV2|funding-lot-descriptor' \
  migrations internal scripts --glob '*.go' --glob '!**/*_test.go' || true
printf '%s\n' 'test/fixture-only historical call sites:'
rg -l 'ApplyThrough|VerifyThrough|Original22Manifest|VerifyOriginal22SnapshotTx|funding-lot-descriptor' \
  migrations internal scripts --glob '*_test.go' | sort || true

section 'compile-only and vet'
go test -run '^$' ./cmd/moni-aigate
go vet ./...

printf '\nAUDIT RESULT: PASS (default entry is current-only migration/verify path; historical paths are reported for isolation review)\n'
