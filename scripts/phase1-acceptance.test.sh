#!/usr/bin/env bash
set -Eeuo pipefail
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
python3 "$root/scripts/phase1_acceptance_test.py"
python3 "$root/scripts/phase1-acceptance.py" --check
