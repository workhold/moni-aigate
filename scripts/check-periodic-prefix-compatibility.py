"""Recompute the twelve fixed V3 compatibility routes from repository bytes.

Read-only. Catalog roots must first pass check-periodic-prefix-profile-roots.py.
"""
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
old_raw = (ROOT / "internal/fundinglotruntime/expected/compatibility-v1.json").read_bytes()
assert hashlib.sha256(old_raw).hexdigest() == "5add8e6ffde8f792547aff8b205b0c34b6ea6ec13ac19f2ab823902e8a987f4b"
old = json.loads(old_raw)
roots = json.loads((ROOT / "docs/verification/periodic-prefix-profile-roots.json").read_bytes())
basis = hashlib.sha256((ROOT / "internal/fundinglotbasis/materials/descriptor-prefix-v1.json").read_bytes()).hexdigest()
assert basis == "104914e6176e3d02a289e4e0acf0d476a69dcfb2e8772cf29336b520a7ec82bc"
records = []
for r in old["mappings"]:
    d = roots[r["mode"]]
    v = d["variants"][r["variant"]]
    current = dict(expected=d["expected_sha256"], schema=v["schema"], roles=v["roles"])
    for profile, descriptor, historical in [("LegacyV1", old["basis_descriptor"], r["historical"]), ("PeriodicPrefixV3", basis, current)]:
        records.append(dict(mode=r["mode"], variant=r["variant"], historical_profile=profile,
                            basis_descriptor=descriptor, historical=historical, current=current))
expected = dict(format_version=1, postgres_major=16, descriptor_version=1, basis_version=1,
                current_profile="PeriodicPrefixV3", manifest_digest=old["manifest_digest"], mappings=records)
raw = (json.dumps(expected, ensure_ascii=False, indent=2) + "\n").encode()
assert raw == (ROOT / "internal/fundinglotprefixcompat/expected/compatibility-v1.json").read_bytes()
assert len(records) == 12 and len({(r["mode"], r["variant"], r["historical_profile"]) for r in records}) == 12
print("static prefix compatibility verified: 12 exact routes; original six routes retained")
