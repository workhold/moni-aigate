#!/usr/bin/env bash
set -Eeuo pipefail
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
export PYTHONDONTWRITEBYTECODE=1
python3 -B -I "$root/scripts/check_go_test_evidence_test.py"
python3 -B -I "$root/scripts/check_acceptance_evidence_v2_test.py"
python3 -B -I "$root/scripts/acceptance_parallel_runner_test.py"
python3 -B -I "$root/scripts/phase1_acceptance_test.py"
python3 -B -I "$root/scripts/phase1_browser_qa_test.py"
python3 -B -I "$root/scripts/phase1-acceptance.py" --check
