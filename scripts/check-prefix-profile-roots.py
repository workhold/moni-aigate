"""Independently derive the fixed V3 catalog roots without a database.

Inputs are immutable LegacyV1 JSON, the exact four-column WalletReadV2 delta,
and the reviewed fixed PG16 record delta. Captured metadata is never an input.
Run: python3 scripts/check-prefix-profile-roots.py
"""

import copy
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DELTA_SHA = "2db8c8fc92e3cf56b2a5b335dd643818ed43d8b3c3e80a8bdb021a047be5bcb2"
BASE_SHA = {
    "ordinary": "a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2",
    "sandbox": "a3a7f855a5ac58ee583dce071c02707499c2a9e3d4bd2ec8b7b1b60228d55c4f",
}
APP = {"class": "role", "scope": "role", "name": "application"}
OWNER = {"class": "role", "scope": "role", "name": "application_owner"}


def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).replace("\u2028", "\\u2028").replace("\u2029", "\\u2029").encode()


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def identity(record):
    if "identity" in record:
        return canonical(record["identity"])
    keys = ["object", "referenced", "role", "member", "grantor", "grantee", "privilege", "kind", "catalog", "owner", "namespace", "object_type", "name"]
    return canonical({key: record[key] for key in keys if key in record})


def wallet(base):
    for variant in base["variants"]:
        role = variant["roles"]["application"]
        for sub, column in enumerate(["id", "verified_query_id", "conflicting_digest", "received_at"], 1):
            ref = {"class": "relation", "scope": "application", "name": "payment_query_conflicts", "sub_id": sub, "column": column}
            acl = [row for row in role["column_acl"] if row["object"] == ref]
            assert len(acl) == 1 and acl[0] == {"object": ref, "acl_is_null": True, "entries": [], "default_entries": []}
            acl[0]["acl_is_null"] = False
            acl[0]["entries"] = [{"grantor": OWNER, "grantee": APP, "privilege": "SELECT", "grant_option": False}]
            effective = [row for row in role["effective_privileges"] if row["object"] == ref and row["role"] == APP]
            assert len(effective) == 1 and effective[0]["privileges"] == [False] * 4
            effective[0]["privileges"][0] = True
            dependency = {"catalog": "pg_shdepend", "object": ref, "role": APP, "kind": "a", "multiplicity": 1}
            for scoped in variant["roles"].values():
                assert not any(identity(row) == identity(dependency) for row in scoped["acl_dependencies"])
                scoped["acl_dependencies"].append(copy.deepcopy(dependency))
        for scoped in variant["roles"].values():
            for category in ["column_acl", "effective_privileges", "acl_dependencies"]:
                scoped[category].sort(key=identity)
    return base


def main():
    raw = (ROOT / "internal/fundinglotcatalog/expected/periodic-prefix-v3-delta.json").read_bytes()
    assert sha(raw) == DELTA_SHA
    delta = json.loads(raw)
    assert canonical(delta) == raw
    assert set(delta) == {"format_version", "profile", "postgres_major", "sql_sha256", "permissions_sha256", "modes"}
    assert (delta["format_version"], delta["profile"], delta["postgres_major"]) == (1, 3, 16)
    for name, field in [("candidate-v1.sql", "sql_sha256"), ("permissions-v1.sql", "permissions_sha256")]:
        assert sha((ROOT / "internal/periodicbudgetprefixschema/materials" / name).read_bytes()) == delta[field]
    assert set(delta["modes"]) == {"ordinary", "sandbox"}
    result = {}
    for mode in ["ordinary", "sandbox"]:
        old_raw = (ROOT / "internal/fundinglotcatalog/expected" / (mode + "-v1.json")).read_bytes()
        assert sha(old_raw) == BASE_SHA[mode]
        base = json.loads(old_raw)
        assert canonical(base) == old_raw
        base = wallet(base)
        parent = sha(canonical(base))
        operations = delta["modes"][mode]
        assert set(operations) == {"base_sha256", "variants"} and operations["base_sha256"] == parent
        assert len(operations["variants"]) == len(base["variants"]) == (5 if mode == "ordinary" else 1)
        for variant, patch in zip(base["variants"], operations["variants"]):
            assert set(patch) == {"name", "changes"} and patch["name"] == variant["name"]
            seen = set()
            for change in patch["changes"]:
                assert set(change) == {"family", "scope", "category", "remove", "add"}
                path = (change["family"], change["scope"], change["category"])
                assert path not in seen
                seen.add(path)
                family, scope, category = path
                rows = variant[family][scope][category]
                assert isinstance(rows, list)
                indexed = {identity(row): row for row in rows}
                assert len(indexed) == len(rows)
                for row in change["remove"]:
                    key = identity(row)
                    assert key in indexed and indexed[key] == row
                    del indexed[key]
                for row in change["add"]:
                    key = identity(row)
                    assert key not in indexed
                    indexed[key] = copy.deepcopy(row)
                variant[family][scope][category] = [indexed[key] for key in sorted(indexed)]
            schema = variant["schema"]["application"]
            relations = schema["relations"]
            tables = [row for row in relations if row["identity"]["scope"] == "application" and row["kind"] == "r"]
            assert len(tables) == 128 and sum(len(row["attributes"]) for row in tables) == 1173
            assert len(schema["constraints"]) == 1197 and len(schema["functions"]) == 209 and len(schema["types"]) == 258
            assert sum(not row["internal"] for row in schema["triggers"]) == 274
        result[mode] = {
            "base_sha256": parent,
            "expected_sha256": sha(canonical(base)),
            "canonical_bytes": len(canonical(base)),
            "variants": {variant["name"]: {"schema": sha(canonical(variant["schema"])), "roles": sha(canonical(variant["roles"]))} for variant in base["variants"]},
        }
    reviewed = json.loads((ROOT / "docs/verification/periodic-prefix-profile-roots.json").read_bytes())
    if result != reviewed:
        raise SystemExit("static periodic-prefix roots differ from reviewed artifact")
    print("static periodic-prefix roots verified: 2 modes, 6 variants, exact parent/delta and full PG16 catalog inventory")


if __name__ == "__main__":
    main()
