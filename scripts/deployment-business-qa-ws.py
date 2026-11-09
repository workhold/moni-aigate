#!/usr/bin/env python3
"""Small dependency-free WebSocket smoke client for deployment-business-qa."""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import secrets
import socket
import ssl
import sys
import time
from urllib.parse import urlsplit


EXIT_FAIL = 1
EXIT_SKIP = 3
MAX_FRAME = 1 << 20


class ProtocolFailure(Exception):
    pass


class BufferedSocket:
    def __init__(self, sock: socket.socket, prefix: bytes = b"") -> None:
        self.sock = sock
        self.prefix = bytearray(prefix)

    def recv(self, length: int) -> bytes:
        if self.prefix:
            result = bytes(self.prefix[:length])
            del self.prefix[:length]
            return result
        return self.sock.recv(length)

    def sendall(self, payload: bytes) -> None:
        self.sock.sendall(payload)

    def settimeout(self, timeout: float) -> None:
        self.sock.settimeout(timeout)

    def close(self) -> None:
        self.sock.close()


def fail(message: str, code: int = EXIT_FAIL) -> int:
    print(f"Realtime WebSocket: {message}", file=sys.stderr)
    return code


def receive_exact(sock: socket.socket, length: int) -> bytes:
    data = bytearray()
    while len(data) < length:
        chunk = sock.recv(length - len(data))
        if not chunk:
            raise ProtocolFailure("connection closed before a complete frame")
        data.extend(chunk)
    return bytes(data)


def send_frame(sock: socket.socket, opcode: int, payload: bytes) -> None:
    if len(payload) > MAX_FRAME:
        raise ProtocolFailure("outgoing frame is too large")
    mask = secrets.token_bytes(4)
    masked = bytes(value ^ mask[index % 4] for index, value in enumerate(payload))
    length = len(masked)
    if length < 126:
        header = bytes((0x80 | opcode, 0x80 | length))
    elif length <= 0xFFFF:
        header = bytes((0x80 | opcode, 0x80 | 126)) + length.to_bytes(2, "big")
    else:
        header = bytes((0x80 | opcode, 0x80 | 127)) + length.to_bytes(8, "big")
    sock.sendall(header + mask + masked)


def receive_frame(sock: socket.socket) -> tuple[int, bytes]:
    first, second = receive_exact(sock, 2)
    opcode = first & 0x0F
    length = second & 0x7F
    if length == 126:
        length = int.from_bytes(receive_exact(sock, 2), "big")
    elif length == 127:
        length = int.from_bytes(receive_exact(sock, 8), "big")
    if length > MAX_FRAME:
        raise ProtocolFailure("incoming frame is too large")
    masked = bool(second & 0x80)
    mask = receive_exact(sock, 4) if masked else b""
    payload = bytearray(receive_exact(sock, length))
    if masked:
        for index in range(length):
            payload[index] ^= mask[index % 4]
    return opcode, bytes(payload)


def receive_event(sock: socket.socket) -> dict:
    while True:
        opcode, payload = receive_frame(sock)
        if opcode == 0x1:
            try:
                value = json.loads(payload.decode("utf-8"))
            except (UnicodeDecodeError, json.JSONDecodeError) as error:
                raise ProtocolFailure(f"server sent invalid JSON: {error}") from error
            if not isinstance(value, dict) or not isinstance(value.get("type"), str):
                raise ProtocolFailure("server sent an invalid event object")
            return value
        if opcode == 0x9:
            send_frame(sock, 0xA, payload)
            continue
        if opcode == 0x8:
            raise ProtocolFailure("server closed the WebSocket")
        if opcode != 0xA:
            raise ProtocolFailure(f"unsupported server frame opcode {opcode}")


def expect_type(sock: socket.socket, expected: str) -> dict:
    event = receive_event(sock)
    actual = event.get("type")
    if actual == "error":
        data = event.get("data")
        code = data.get("code") if isinstance(data, dict) else ""
        if code in {"realtime_unavailable", "provider_error", "provider_unavailable", "unavailable"}:
            raise OSError(f"provider unavailable: {code}")
        raise ProtocolFailure(f"server error: {code or 'unknown'}")
    if actual != expected:
        raise ProtocolFailure(f"expected {expected}, received {actual}")
    return event


def connect(url: str, api_key: str, origin: str, timeout: float, insecure_tls: bool) -> BufferedSocket:
    parsed = urlsplit(url)
    if parsed.scheme not in {"ws", "wss"} or not parsed.hostname or parsed.fragment:
        raise ProtocolFailure("URL must be an absolute ws:// or wss:// URL")
    port = parsed.port or (443 if parsed.scheme == "wss" else 80)
    target = parsed.path or "/"
    if parsed.query:
        target += "?" + parsed.query
    key = base64.b64encode(os.urandom(16)).decode("ascii")
    host = parsed.hostname
    if (parsed.scheme == "ws" and port != 80) or (parsed.scheme == "wss" and port != 443):
        host = f"{host}:{port}"
    sock = socket.create_connection((parsed.hostname, port), timeout=timeout)
    if parsed.scheme == "wss":
        context = ssl._create_unverified_context() if insecure_tls else ssl.create_default_context()
        sock = context.wrap_socket(sock, server_hostname=parsed.hostname)
    request = (
        f"GET {target} HTTP/1.1\r\n"
        f"Host: {host}\r\n"
        "Upgrade: websocket\r\n"
        "Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\n"
        "Sec-WebSocket-Version: 13\r\n"
        f"Origin: {origin}\r\n"
        f"Authorization: Bearer {api_key}\r\n"
        "\r\n"
    ).encode("ascii")
    sock.sendall(request)
    response = bytearray()
    while b"\r\n\r\n" not in response:
        chunk = sock.recv(4096)
        if not chunk:
            raise ProtocolFailure("connection closed during WebSocket handshake")
        response.extend(chunk)
        if len(response) > 64 * 1024:
            raise ProtocolFailure("WebSocket handshake response is too large")
    header_bytes, prefix = bytes(response).split(b"\r\n\r\n", 1)
    header_block = header_bytes.decode("iso-8859-1")
    lines = header_block.split("\r\n")
    status = int(lines[0].split(" ", 2)[1])
    if status in {502, 503, 504}:
        raise OSError(f"WebSocket endpoint unavailable with HTTP {status}")
    if status != 101:
        raise ProtocolFailure(f"WebSocket handshake returned HTTP {status}")
    headers = {}
    for line in lines[1:]:
        if ":" in line:
            name, value = line.split(":", 1)
            headers[name.lower().strip()] = value.strip()
    expected_accept = base64.b64encode(
        hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode("ascii")).digest()
    ).decode("ascii")
    if headers.get("sec-websocket-accept") != expected_accept:
        raise ProtocolFailure("WebSocket accept key did not match")
    sock.settimeout(timeout)
    return BufferedSocket(sock, prefix)


def event_id(suffix: str) -> str:
    digest = hashlib.sha256(suffix.encode("utf-8")).hexdigest()
    return f"{digest[:8]}-{digest[8:12]}-4{digest[13:16]}-8{digest[17:20]}-{digest[20:32]}"


def run(args: argparse.Namespace) -> int:
    sock = None
    try:
        sock = connect(args.url, args.api_key, args.origin, args.timeout, args.insecure_tls)
        created = expect_type(sock, "session.created")
        data = created.get("data")
        if not isinstance(data, dict) or not data.get("session_id") or not data.get("turn_id"):
            raise ProtocolFailure("session.created omitted session_id or turn_id")

        send_frame(
            sock,
            0x1,
            json.dumps(
                {
                    "event_id": event_id("deployment-business-qa-input"),
                    "type": "input.text",
                    "data": {"text": "smoke"},
                },
                separators=(",", ":"),
            ).encode("utf-8"),
        )
        expect_type(sock, "event.accepted")
        send_frame(
            sock,
            0x1,
            json.dumps(
                {
                    "event_id": event_id("deployment-business-qa-response"),
                    "type": "response.create",
                    "data": {},
                },
                separators=(",", ":"),
            ).encode("utf-8"),
        )
        expect_type(sock, "event.accepted")
        expect_type(sock, "response.created")
        expect_type(sock, "response.delta")
        expect_type(sock, "response.done")
        print(f"Realtime WebSocket passed session={data['session_id']} turn={data['turn_id']}")
        return 0
    except OSError as error:
        return fail(str(error), EXIT_SKIP)
    except (ProtocolFailure, ValueError, IndexError, socket.timeout) as error:
        return fail(str(error))
    finally:
        if sock is not None:
            try:
                sock.close()
            except OSError:
                pass


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", required=True)
    parser.add_argument("--api-key", required=True)
    parser.add_argument("--origin", required=True)
    parser.add_argument("--timeout", type=float, required=True)
    parser.add_argument("--insecure-tls", action="store_true")
    args = parser.parse_args()
    if args.timeout <= 0:
        return fail("timeout must be positive", 2)
    return run(args)


if __name__ == "__main__":
    raise SystemExit(main())
