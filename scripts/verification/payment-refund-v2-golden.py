#!/usr/bin/env python3
"""Independent offline C1-P vectors; no Go imports, subprocesses, DB or network.

Default: recompute the fixed synthetic vectors and compare the committed JSON.
Generation is opt-in with --write-new and uses exclusive creation, never replace.
All keys, identities, amounts and timestamps below are conspicuously synthetic.
"""

import argparse
import calendar
import hashlib
import hmac
import json
from pathlib import Path
import struct
import sys
from datetime import datetime


ROOT = Path(__file__).resolve().parents[2]
DEFAULT_ARTIFACT = ROOT / "docs/verification/payment-refund-v2-golden.json"
V2_KEY = b"SYNTHETIC_V2_KEY_ONLY_0000000000"
V1_KEY = b"SYNTHETIC_V1_KEY_ONLY_0000000000"
KEY_ID = "synthetic_v2_key_01"
V1_KEY_ID = "synthetic_v1_key_01"
ENVIRONMENT = "11111111-1111-1111-1111-111111111111"
ORDER = "22222222-2222-2222-2222-222222222222"
MERCHANT = "商户:测试_α/01"
TRANSACTION = "交易:合成/001"
MERCHANT_ORDER = "mps1." + ENVIRONMENT + "." + ORDER
OCCURRED = "2026-01-02T03:04:05.123456Z"
COMPLETED = "2026-01-02T03:04:06.123456Z"
RECEIVED = "2026-01-02T03:04:07.000000Z"
DELIVERY_TIME = str(calendar.timegm(datetime(2026, 1, 2, 3, 4, 7).timetuple()))
DOMAINS = {
    "capture": "moni-aigate/payment-refund-capture/v2",
    "command": "moni-aigate/payment-refund-command/v2",
    "query": "moni-aigate/payment-refund-query/v2",
    "fact": "moni-aigate/payment-refund-fact/v2",
    "command_reply": "moni-aigate/payment-refund-command-reply/v2",
    "query_reply": "moni-aigate/payment-refund-query-reply/v2",
    "notification": "moni-aigate/payment-refund-notification/v2",
}


def uuid_digit(digit):
    return digit * 8 + "-" + digit * 4 + "-" + digit * 4 + "-" + digit * 4 + "-" + digit * 12


def canonical_value(value):
    """Encode our declared ordered fields, without Go code or generated DTOs."""
    if value is None:
        return "null"
    if isinstance(value, str):
        return json.dumps(value, ensure_ascii=False, separators=(",", ":"))
    if type(value) is int:
        return str(value)
    if isinstance(value, dict):
        return "{" + ",".join(
            canonical_value(k) + ":" + canonical_value(v) for k, v in value.items()
        ) + "}"
    raise ValueError("unsupported synthetic canonical value")


def wire(value):
    return canonical_value(value).encode("utf-8")


def frame(value):
    if isinstance(value, str):
        value = value.encode("utf-8")
    return struct.pack(">Q", len(value)) + value


def sha(value):
    return hashlib.sha256(value).hexdigest()


def domain_digest(domain, value):
    return sha(frame(domain) + frame(value))


def named_canonical(value, digest_domain=None):
    raw = wire(value)
    result = {"canonical": raw.decode("utf-8"), "utf8_bytes": len(raw), "raw_sha256": sha(raw)}
    if digest_domain is not None:
        result["digest_domain"] = digest_domain
        result["framed_digest"] = domain_digest(digest_domain, raw)
    return result


def request(command, refund, capture_digest):
    # This order is copied by hand from the frozen wire contract, not Go DTOs.
    return {
        "schema_version": 2,
        "environment_id": ENVIRONMENT,
        "merchant_id": MERCHANT,
        "command_kind": "refund",
        "command_id": command,
        "local_refund_id": refund,
        "merchant_order_id": MERCHANT_ORDER,
        "provider_transaction_id": TRANSACTION,
        "original_capture_amount": "1000000",
        "capture_digest": capture_digest,
        "amount": "125000",
        "currency": "CNY",
    }


def query(command, query_id):
    return {
        "schema_version": 2,
        "query_id": query_id,
        "mutation_generation": "7",
        "command": command,
        "parameter_digest": domain_digest(DOMAINS["command"], wire(command)),
    }


def fact(command, fact_id, outcome, revision):
    return {
        "schema_version": 2,
        "fact_id": fact_id,
        "environment_id": ENVIRONMENT,
        "merchant_id": MERCHANT,
        "local_refund_id": command["local_refund_id"],
        "command_id": command["command_id"],
        "parameter_digest": domain_digest(DOMAINS["command"], wire(command)),
        "merchant_order_id": MERCHANT_ORDER,
        "provider_transaction_id": TRANSACTION,
        "original_capture_amount": "1000000",
        "capture_digest": command["capture_digest"],
        "amount": command["amount"],
        "currency": "CNY",
        "outcome": outcome,
        "provider_refund_reference": "synthetic:refund/0001" if outcome == "confirmed" else None,
        "failure_code": None if outcome == "confirmed" else "refund_policy_rejected",
        "capture_refund_revision": revision,
        "completed_at": COMPLETED,
    }


def command_reply(command, result):
    return {
        "schema_version": 2,
        "command": command,
        "parameter_digest": domain_digest(DOMAINS["command"], wire(command)),
        "command_state": "completed" if result is not None else "in_progress",
        "result": result,
    }


def query_reply(query_request, result, state="completed"):
    return {
        "schema_version": 2,
        "query_id": query_request["query_id"],
        "mutation_generation": query_request["mutation_generation"],
        "command": query_request["command"],
        "parameter_digest": query_request["parameter_digest"],
        "command_state": state,
        "result": result,
    }


def notification(result, event_id):
    return {
        "schema_version": 2,
        "event_id": event_id,
        "event_type": "refund." + result["outcome"],
        "fact": result,
    }


def signature_vector(name, kind, body, context_digest, delivery_id, query_name=None, command_name=None, raw=None):
    if raw is None:
        raw = wire(body)
    pieces = (
        DOMAINS[kind], KEY_ID, ENVIRONMENT, MERCHANT, context_digest,
        delivery_id, DELIVERY_TIME, sha(raw),
    )
    signing_input = b"".join(frame(piece) for piece in pieces)
    result = {
        "name": name,
        "kind": kind,
        "command_vector": command_name,
        "query_vector": query_name,
        "canonical": wire(body).decode("utf-8"),
        "raw_body": raw.decode("utf-8"),
        "raw_utf8_bytes": len(raw),
        "raw_sha256": sha(raw),
        "context_digest": context_digest,
        "signature_domain": DOMAINS[kind],
        "framed_signing_input_hex": signing_input.hex(),
        "headers": {
            "key_id": KEY_ID,
            "delivery_id": delivery_id,
            "delivery_time": DELIVERY_TIME,
            "signature": hmac.new(V2_KEY, signing_input, hashlib.sha256).hexdigest(),
        },
        "expected_fact_digest": None,
    }
    nested_fact = body.get("fact") if kind == "notification" else body["result"]
    if nested_fact is not None:
        result["expected_fact_digest"] = domain_digest(DOMAINS["fact"], wire(nested_fact))
    return result


def v1_headers(domain, raw, delivery_id, query_request=None):
    # v1 uses its existing newline format; the new v2 frame never changes v1.
    parts = [domain, ENVIRONMENT, MERCHANT]
    if query_request is not None:
        parts += [query_request["query_id"], query_request["command_id"]]
    parts += [delivery_id, DELIVERY_TIME, sha(raw)]
    signing_input = "\n".join(parts).encode("utf-8")
    return {
        "key_id": V1_KEY_ID,
        "delivery_id": delivery_id,
        "delivery_time": DELIVERY_TIME,
        "signature": hmac.new(V1_KEY, signing_input, hashlib.sha256).hexdigest(),
    }


def primitive_checks():
    # Independently published constants and hand-counted frames, not self output.
    assert sha(b"") == "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
    assert hmac.new(b"\x0b" * 20, b"Hi There", hashlib.sha256).hexdigest() == (
        "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"
    )
    assert frame("a").hex() == "000000000000000161"
    assert frame("α").hex() == "0000000000000002ceb1"
    assert len(V1_KEY) == len(V2_KEY) == 32 and V1_KEY != V2_KEY
    assert wire({"schema_version": 2, "value": None}) == b'{"schema_version":2,"value":null}'


def build_vectors():
    primitive_checks()
    capture = {
        "capture_protocol_version": 1,
        "environment_id": ENVIRONMENT,
        "merchant_id": MERCHANT,
        "merchant_order_id": MERCHANT_ORDER,
        "provider_transaction_id": TRANSACTION,
        "amount": "1000000",
        "currency": "CNY",
        "occurred_at": OCCURRED,
        "provider_order_revision": "3",
    }
    capture_digest = domain_digest(DOMAINS["capture"], wire(capture))
    capture_fact_v1 = {
        "provider_transaction_id": TRANSACTION, "amount": "1000000", "currency": "CNY",
        "occurred_at": OCCURRED, "provider_order_revision": "3",
    }
    checkout = {
        "schema_version": 1, "environment_id": ENVIRONMENT, "merchant_id": MERCHANT,
        "command_id": "00000001-0001-0001-0001-000000000001",
        "merchant_order_id": MERCHANT_ORDER, "amount": "1000000", "currency": "CNY",
        "created_at": "2026-01-02T03:00:00.000000Z", "expires_at": "2026-01-02T03:15:00.000000Z",
    }
    v1_query = {
        "schema_version": 1, "environment_id": ENVIRONMENT, "merchant_id": MERCHANT,
        "query_id": "00000002-0002-0002-0002-000000000002",
        "command_id": checkout["command_id"], "merchant_order_id": MERCHANT_ORDER,
        "command_kind": "checkout", "parameter_digest": sha(wire(checkout)),
    }
    v1_notification = {
        "schema_version": 1, "event_id": "synthetic:capture/0001", "event_type": "capture.confirmed",
        "environment_id": ENVIRONMENT, "merchant_id": MERCHANT, "order_id": ORDER,
        "provider_transaction_id": TRANSACTION, "amount": "1000000", "currency": "CNY",
        "occurred_at": OCCURRED, "provider_order_revision": "3",
    }
    v1_query_reply = {
        "schema_version": 1, "query_id": v1_query["query_id"], "environment_id": ENVIRONMENT,
        "merchant_id": MERCHANT, "merchant_order_id": MERCHANT_ORDER,
        "command_id": checkout["command_id"], "command_kind": "checkout",
        "parameter_digest": v1_query["parameter_digest"], "command_state": "completed",
        "provider_order_revision": "3",
        "result": {"status": "captured", "checkout_ref": None, "failure_code": None,
                   "capture": capture_fact_v1, "closed_at": None},
    }
    first = request(uuid_digit("3"), uuid_digit("4"), capture_digest)
    second = request(uuid_digit("9"), uuid_digit("a"), capture_digest)
    first_query = query(first, uuid_digit("5"))
    second_query = query(second, uuid_digit("b"))
    confirmed = fact(first, uuid_digit("6"), "confirmed", "1")
    no_effect = fact(second, uuid_digit("c"), "final_no_effect", "2")
    notices = {"confirmed": notification(confirmed, uuid_digit("7")),
               "final_no_effect": notification(no_effect, uuid_digit("d"))}
    vectors = []
    for name, command, query_request, refund_fact, delivery in (
        ("confirmed", first, first_query, confirmed, uuid_digit("8")),
        ("final_no_effect", second, second_query, no_effect, uuid_digit("e")),
    ):
        parameter = domain_digest(DOMAINS["command"], wire(command))
        query_digest = domain_digest(DOMAINS["query"], wire(query_request))
        vectors += [
            signature_vector("command_" + name, "command_reply", command_reply(command, refund_fact),
                             parameter, delivery, command_name=name),
            signature_vector("query_" + name, "query_reply", query_reply(query_request, refund_fact),
                             query_digest, delivery, query_name=name, command_name=name),
            signature_vector("notification_" + name, "notification", notices[name], "", delivery),
        ]
    vectors += [
        signature_vector("command_in_progress", "command_reply", command_reply(first, None),
                         domain_digest(DOMAINS["command"], wire(first)), uuid_digit("8"), command_name="confirmed"),
        signature_vector("query_not_seen", "query_reply", query_reply(first_query, None, "not_seen"),
                         domain_digest(DOMAINS["query"], wire(first_query)), uuid_digit("8"),
                         query_name="confirmed", command_name="confirmed"),
        signature_vector("notification_confirmed_pretty_redelivery", "notification", notices["confirmed"],
                         "", uuid_digit("f"), raw=(json.dumps(notices["confirmed"], ensure_ascii=True, indent=2) + "\n").encode("utf-8")),
    ]
    # The same terminal fact is retained in three wrappers and a raw redelivery.
    confirmed_digests = [v["expected_fact_digest"] for v in vectors if v["name"] in (
        "command_confirmed", "query_confirmed", "notification_confirmed", "notification_confirmed_pretty_redelivery")]
    no_effect_digests = [v["expected_fact_digest"] for v in vectors if v["name"] in (
        "command_final_no_effect", "query_final_no_effect", "notification_final_no_effect")]
    assert len(set(confirmed_digests)) == len(set(no_effect_digests)) == 1
    assert vectors[2]["raw_sha256"] != vectors[-1]["raw_sha256"]
    return {
        "artifact_schema_version": 1,
        "scope": "C1-P pure wire/digest/signature synthetic vectors; no DB/network/funding authority",
        "generator": "scripts/verification/payment-refund-v2-golden.py",
        "generator_version": 1,
        "synthetic_only": True,
        "keys": {"v1_ascii": V1_KEY.decode("ascii"), "v2_ascii": V2_KEY.decode("ascii"),
                 "v1_key_id": V1_KEY_ID, "v2_key_id": KEY_ID,
                 "not_before": "2025-01-01T00:00:00.000000Z", "not_after": "2030-01-01T00:00:00.000000Z"},
        "environment": {"id": ENVIRONMENT, "merchant_id": MERCHANT},
        "received_at": RECEIVED,
        "v1_bridge": {
            "checkout": named_canonical(checkout), "query_request": named_canonical(v1_query),
            "notification": {**named_canonical(v1_notification), "headers": v1_headers(
                "moni-payment-sandbox-v1", wire(v1_notification), uuid_digit("8"))},
            "query_reply": {**named_canonical(v1_query_reply), "headers": v1_headers(
                "moni-payment-sandbox-query-v1", wire(v1_query_reply), uuid_digit("8"), v1_query)},
            "expected_capture": named_canonical(capture, DOMAINS["capture"]),
            "notification_and_query_capture_digest_equal": True,
        },
        "commands": {"confirmed": named_canonical(first, DOMAINS["command"]),
                     "final_no_effect": named_canonical(second, DOMAINS["command"])},
        "queries": {"confirmed": named_canonical(first_query, DOMAINS["query"]),
                    "final_no_effect": named_canonical(second_query, DOMAINS["query"])},
        "facts": {"confirmed": named_canonical(confirmed, DOMAINS["fact"]),
                  "final_no_effect": named_canonical(no_effect, DOMAINS["fact"])},
        "signed_vectors": vectors,
        "fact_equivalence": {"confirmed_wrapper_count": len(confirmed_digests),
                             "confirmed_digest": confirmed_digests[0],
                             "final_no_effect_wrapper_count": len(no_effect_digests),
                             "final_no_effect_digest": no_effect_digests[0]},
    }


def strict_pairs(pairs):
    result = {}
    for name, value in pairs:
        if name in result:
            raise ValueError("duplicate artifact field")
        result[name] = value
    return result


def first_difference(expected, actual, prefix="$"):
    if type(expected) is not type(actual):
        return prefix
    if isinstance(expected, dict):
        if expected.keys() != actual.keys():
            return prefix + ".fields"
        for name in expected:
            difference = first_difference(expected[name], actual[name], prefix + "." + name)
            if difference:
                return difference
    elif isinstance(expected, list):
        if len(expected) != len(actual):
            return prefix + ".length"
        for index, value in enumerate(expected):
            difference = first_difference(value, actual[index], prefix + "[" + str(index) + "]")
            if difference:
                return difference
    elif expected != actual:
        return prefix
    return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--check", type=Path, help="check this JSON instead of the committed artifact")
    group.add_argument("--write-new", type=Path, help="exclusively create a new JSON; never overwrite")
    args = parser.parse_args()
    expected = build_vectors()
    if args.write_new is not None:
        try:
            with args.write_new.open("x", encoding="utf-8", newline="\n") as output:
                json.dump(expected, output, ensure_ascii=False, indent=2)
                output.write("\n")
        except OSError:
            print("golden output must be an absent path in an existing directory", file=sys.stderr)
            return 2
        print("payment refund v2 synthetic golden created at new path")
        return 0
    try:
        raw = (args.check or DEFAULT_ARTIFACT).read_text(encoding="utf-8")
        actual = json.loads(raw, object_pairs_hook=strict_pairs)
    except (OSError, UnicodeError, ValueError):
        print("golden artifact cannot be read as strict JSON", file=sys.stderr)
        return 2
    difference = first_difference(expected, actual)
    if difference:
        print("golden mismatch at " + difference, file=sys.stderr)
        return 1
    print("payment refund v2 golden PASS: 2 v1 bridges, 4 digest domains, 9 signed vectors")
    return 0


if __name__ == "__main__":
    sys.exit(main())
