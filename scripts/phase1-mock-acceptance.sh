#!/usr/bin/env bash
set -Eeuo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
exec python3 "$root/scripts/phase1-mock-acceptance.py" "$@"
