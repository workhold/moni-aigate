"""Independent Python format vectors; never imports or runs the Go codec.

The committed JSON bytes and SHA values are the test oracle. This script is a
reviewable source for their initial construction, not run by the Go tests.
All IDs and digest values are synthetic and grant no database authority.
"""
import base64
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent


def canonical(value):
    # Match the specified UTF-8 JSON spelling, including the two JSONP escapes.
    return json.dumps(value, ensure_ascii=False, separators=(",", ":")).replace(
        "\u2028", r"\u2028"
    ).replace("\u2029", r"\u2029").encode()


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def role(name, oid):
    return {"name": name, "oid": oid}


def schema(name, oid, owner):
    return {"name": name, "oid": oid, "namespace_owner": owner, "object_owner": owner}


manifest = [{"name": "001_core.sql", "sha256": "1" * 64}]
ordinary = {
    "domain": "moni-aigate/funding-lots-runtime/v2",
    "proof_version": 2,
    "mode": "ordinary",
    "epoch_id": "12345678-1234-4234-8234-123456789abc",
    "database": {"host": "db.example", "port": 5432, "name": "普通库", "oid": 100},
    "app_schema": schema("tenant_研发", 100, role("普通owner", 201)),
    "app_role": role("普通app", 202),
    "projector_role": role("普通projector", 203),
    "sandbox": None,
    "basis_version": 1,
    "basis_digest": "a" * 64,
    "basis_descriptor_digest": "b" * 64,
    "migration_manifest": manifest,
    "manifest_digest": sha(canonical(manifest)),
    "catalog_descriptor_version": 2,
    "catalog_postgres_major": 16,
    "catalog_expected_digest": "c" * 64,
    "catalog_schema_digest": "d" * 64,
    "catalog_roles_digest": "e" * 64,
    "predecessor_payment_digest": None,
    "predecessor_payment_canonical_base64url": None,
}


def sandbox(v1raw):
    p = json.loads(v1raw)
    v2 = dict(ordinary)
    v2["mode"] = "sandbox"
    v2["database"] = p["database"]
    for name in ("app", "mock", "receiver"):
        old = p[name]
        s = schema(old["name"], old["oid"], role(old["owner"]["name"], old["owner"]["oid"]))
        r = role(old["runtime"]["name"], old["runtime"]["oid"])
        if name == "app":
            v2["app_schema"], v2["app_role"] = s, r
        else:
            if name == "mock":
                v2["sandbox"] = {}
            v2["sandbox"][name + "_schema"] = s
            v2["sandbox"][name + "_role"] = r
    v2["sandbox"]["worker_role"] = role(p["worker"]["name"], p["worker"]["oid"])
    v2["projector_role"] = role(p["projector"]["name"], p["projector"]["oid"])
    v2["predecessor_payment_digest"] = sha(v1raw)
    v2["predecessor_payment_canonical_base64url"] = base64.urlsafe_b64encode(v1raw).rstrip(b"=").decode()
    return v2


v1 = ROOT.joinpath("predecessor_v1.json").read_bytes()
vectors = {"ordinary.json": canonical(ordinary), "sandbox.json": canonical(sandbox(v1))}
for count in (1, 128):
    p = json.loads(v1)
    # Equal times and equal key digests remain valid in the v1 format.
    p["keys"] = [
        {"id": f"key_{n:03}", "digest": "4" * 64,
         "not_before": "2001-02-03T04:05:06.123456Z",
         "not_after": "2001-02-03T04:05:06.123456Z"}
        for n in range(count)
    ]
    vectors[f"predecessor_{count}keys.json"] = canonical(p)
    vectors[f"sandbox_{count}keys.json"] = canonical(sandbox(canonical(p)))

for name, raw in vectors.items():
    ROOT.joinpath(name).write_bytes(raw)
    print(name, len(raw), sha(raw))
