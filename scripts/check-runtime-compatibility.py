"""Verify the six fixed R1-B compatibility mappings from repository bytes.

Run: python3 scripts/check-runtime-compatibility.py
Reads the original LegacyV1 documents, independently applies only the reviewed
four-column WalletReadV2 delta, and recomputes every historical/current tuple.
No database, generated live metadata, or Go implementation is used as an oracle.
No repository files are modified.
"""

import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MATERIAL_SHA = "5add8e6ffde8f792547aff8b205b0c34b6ea6ec13ac19f2ab823902e8a987f4b"
BASE_SHA = {
    "ordinary": "a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2",
    "sandbox": "a3a7f855a5ac58ee583dce071c02707499c2a9e3d4bd2ec8b7b1b60228d55c4f",
}
VARIANTS = {
    "ordinary": ["named-isolated", "named-minimal-login", "named-image-login",
                 "public-minimal-login", "public-image-login"],
    "sandbox": ["full-sandbox"],
}


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def compact(value, *, sort_keys=True):
    # Match the reviewed Go JSON: object keys sort in catalog documents; the
    # manifest instead preserves the struct's name,sha256 field order.
    return json.dumps(value, ensure_ascii=False, sort_keys=sort_keys,
                      separators=(",", ":")).replace("\u2028", "\\u2028").replace("\u2029", "\\u2029").encode()


def identity(record):
    if "identity" in record:
        return compact(record["identity"])
    fields = ("object", "referenced", "role", "member", "grantor", "grantee",
              "privilege", "kind", "catalog", "owner", "namespace", "object_type", "name")
    return compact({key: record[key] for key in fields if key in record})


def unique(records, predicate):
    matches = [record for record in records if predicate(record)]
    assert len(matches) == 1, "expected exactly one original record"
    return matches[0]


def derive_variant(variant, mode):
    application = {"class": "role", "scope": "role", "name": "application"}
    owner = {"class": "role", "scope": "role", "name": "application_owner"}
    role = variant["roles"]["application"]
    for index, column in enumerate(("id", "verified_query_id", "conflicting_digest", "received_at"), 1):
        obj = {"class": "relation", "scope": "application", "name": "payment_query_conflicts",
               "sub_id": index, "column": column}
        acl = unique(role["column_acl"], lambda row: row["object"] == obj)
        assert acl == {"object": obj, "acl_is_null": True, "entries": [], "default_entries": []}
        acl["acl_is_null"] = False
        acl["entries"] = [{"grantor": owner, "grantee": application, "privilege": "SELECT", "grant_option": False}]
        effective = unique(role["effective_privileges"], lambda row: row["object"] == obj and row["role"] == application)
        assert effective == {"object": obj, "role": application, "kind": "column",
                             "privileges": [False] * 4, "grant_options": [False] * 4}
        effective["privileges"][0] = True
        dependency = {"catalog": "pg_shdepend", "object": obj, "role": application, "kind": "a", "multiplicity": 1}
        assert not any(identity(row) == identity(dependency) for row in role["acl_dependencies"])
        role["acl_dependencies"].append(dependency)
        if mode == "sandbox":
            # The peer descriptors observe metadata over all eight roles. This
            # mirrors the real dependencies, without granting peer content.
            for scope in ("mock", "receiver"):
                rows = variant["roles"][scope]["acl_dependencies"]
                assert not any(identity(row) == identity(dependency) for row in rows)
                rows.append(dependency.copy())
    for key in ("column_acl", "effective_privileges", "acl_dependencies"):
        role[key].sort(key=identity)
    if mode == "sandbox":
        for scope in ("mock", "receiver"):
            variant["roles"][scope]["acl_dependencies"].sort(key=identity)


def verify():
    mappings = []
    profile_summary = {}
    for mode in ("ordinary", "sandbox"):
        raw = (ROOT / f"internal/fundinglotcatalog/expected/{mode}-v1.json").read_bytes()
        assert sha(raw) == BASE_SHA[mode], "original LegacyV1 bytes changed"
        document = json.loads(raw)
        assert compact(document) == raw, "original catalog is not canonical"
        assert document["format_version"] == 1 and document["postgres_major"] == 16 and document["mode"] == mode
        assert [row["name"] for row in document["variants"]] == VARIANTS[mode]
        historical = {}
        for variant in document["variants"]:
            historical[variant["name"]] = {"expected": sha(raw),
                "schema": sha(compact(variant["schema"])), "roles": sha(compact(variant["roles"]))}
            derive_variant(variant, mode)
        current_raw = compact(document)
        current_root = sha(current_raw)
        roots = {}
        for variant in document["variants"]:
            name = variant["name"]
            current = {"expected": current_root, "schema": sha(compact(variant["schema"])),
                       "roles": sha(compact(variant["roles"]))}
            assert current["schema"] == historical[name]["schema"], "reviewed delta changed schema"
            mappings.append({"mode": mode, "variant": name, "historical": historical[name], "current": current})
            roots[name] = {key: current[key] for key in ("schema", "roles")}
        profile_summary[mode] = {"base_sha256": sha(raw), "expected_sha256": current_root,
                                 "canonical_bytes": len(current_raw), "variants": roots}
    # This is an additional reviewed cross-check, never the source of the roots.
    assert profile_summary == json.loads((ROOT / "docs/verification/runtime-read-profile-roots.json").read_text())
    files = sorted((ROOT / "migrations").glob("*.sql"), key=lambda path: path.name)
    manifest = [{"name": path.name, "sha256": sha(path.read_bytes())} for path in files]
    manifest_raw = compact(manifest, sort_keys=False)
    assert len(manifest) == 22 and len(manifest_raw) == 2459 and max(len(row["name"].encode()) for row in manifest) == 35
    assert sha(manifest_raw) == "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de"
    basis_sha = sha((ROOT / "internal/fundinglotbasis/materials/descriptor-v1.json").read_bytes())
    assert basis_sha == "54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5"
    expected = {"format_version": 1, "postgres_major": 16, "descriptor_version": 1, "basis_version": 1,
                "basis_descriptor": basis_sha, "historical_profile": "LegacyV1", "current_profile": "WalletReadV2",
                "manifest_digest": sha(manifest_raw), "manifest_bytes": len(manifest_raw), "max_name_bytes": 35,
                "manifest": manifest, "mappings": mappings}
    material_raw = (ROOT / "internal/fundinglotruntime/expected/compatibility-v1.json").read_bytes()
    assert len(material_raw) == 7374 and sha(material_raw) == MATERIAL_SHA, "fixed compatibility artifact bytes changed"
    assert json.loads(material_raw) == expected, "complete compatibility artifact differs from independent calculation"
    print("runtime compatibility verified: six exact LegacyV1 → WalletReadV2 tuples; 22 migrations / 2459 bytes")


if __name__ == "__main__":
    verify()
