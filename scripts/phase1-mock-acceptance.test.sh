#!/usr/bin/env bash
set -Eeuo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
script="$root/scripts/phase1-mock-acceptance.py"

python3 -m py_compile "$script" "$root/scripts/phase1_mock_acceptance_test.py"
python3 -I -m unittest discover -s "$root/scripts" -p 'phase1_mock_acceptance_test.py' -v
bash -n "$root/scripts/phase1-mock-acceptance.sh"

if grep -nE 'docker-compose|docker compose (up|restart|down)|run_compose|go test ./\.\.\.' "$script"; then
  echo "phase1 harness must not start/restart Compose or run the whole repository test suite" >&2
  exit 1
fi

python3 "$script" --run-bound-go-tests

echo "phase1 mock acceptance harness unit/static and focused Go self-tests passed"
