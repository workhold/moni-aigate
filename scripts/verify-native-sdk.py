#!/usr/bin/env python3
"""Verify native SDK text aggregation against provider-exported local fixtures."""

from __future__ import annotations

import argparse
import importlib.metadata
import json
import logging
import os
from pathlib import Path
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


VERSIONS = {"openai": "3.41.0", "anthropic": "2.3.0"}
MAX_FIXTURE_BYTES = 8 << 20


class SmokeFailure(Exception):
    """Contains a fixed diagnostic code, never request or response data."""


class SafeArgumentParser(argparse.ArgumentParser):
    def error(self, message: str) -> None:
        self.exit(2, "FAIL invalid_arguments\n")


def require(condition: bool, code: str) -> None:
    if not condition:
        raise SmokeFailure(code)


def text_output(value: dict, protocol: str) -> str:
    if protocol == "openai_responses":
        blocks = [
            part
            for item in value["output"]
            if item["type"] == "message"
            for part in item["content"]
        ]
        require(all(block["type"] == "output_text" for block in blocks), "fixtures")
    else:
        blocks = value["content"]
        require(all(block["type"] == "text" for block in blocks), "fixtures")
    text = "".join(block["text"] for block in blocks)
    require(bool(text), "fixtures")
    return text


def run(directory: Path) -> None:
    # SDK logging can otherwise be enabled by inherited developer environment.
    logging.disable(logging.CRITICAL)
    os.environ.pop("OPENAI_LOG", None)
    os.environ.pop("ANTHROPIC_LOG", None)

    # No API URL or real credential is accepted. This guard also rejects any
    # unexpected DNS lookup, proxy, redirect or telemetry connection.
    allowed_port = [None]

    def local_network_only(event: str, arguments: tuple) -> None:
        if event == "socket.connect":
            address = arguments[1]
            if (
                not isinstance(address, tuple)
                or len(address) < 2
                or address[0] != "127.0.0.1"
                or address[1] != allowed_port[0]
            ):
                raise SmokeFailure("external_network_disabled")
        elif event == "socket.getaddrinfo":
            if arguments[0] != "127.0.0.1" or arguments[1] != allowed_port[0]:
                raise SmokeFailure("external_network_disabled")
        elif event == "socket.sendto":
            raise SmokeFailure("external_network_disabled")

    sys.addaudithook(local_network_only)
    for name, expected in VERSIONS.items():
        require(importlib.metadata.version(name) == expected, "dependency_versions")

    import anthropic
    import openai

    fixtures: dict[tuple[str, bool], bytes] = {}
    expected: dict[str, dict] = {}
    expected_text: dict[str, str] = {}
    for protocol in ("openai_responses", "anthropic_messages"):
        for stream, extension in ((False, ".json"), (True, ".sse")):
            path = directory / (protocol + extension)
            with path.open("rb") as source:
                body = source.read(MAX_FIXTURE_BYTES + 1)
            require(0 < len(body) <= MAX_FIXTURE_BYTES, "fixtures")
            fixtures[(protocol, stream)] = body
        expected[protocol] = json.loads(fixtures[(protocol, False)])
        expected_text[protocol] = text_output(expected[protocol], protocol)

    class FixtureHandler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *arguments) -> None:
            pass

        def setup(self) -> None:
            super().setup()
            self.connection.settimeout(5)

        def do_POST(self) -> None:
            routes = {
                "/v1/responses": "openai_responses",
                "/v1/messages": "anthropic_messages",
            }
            try:
                size = int(self.headers.get("Content-Length", "0"))
                if self.path not in routes or not 0 < size <= 65536:
                    raise ValueError
                payload = json.loads(self.rfile.read(size))
                stream = payload.get("stream", False)
                if not isinstance(stream, bool):
                    raise ValueError
                body = fixtures[(routes[self.path], stream)]
            except Exception:
                self.send_response(400)
                self.send_header("Content-Length", "0")
                self.send_header("Connection", "close")
                self.end_headers()
                self.close_connection = True
                return
            self.send_response(200)
            self.send_header(
                "Content-Type", "text/event-stream" if stream else "application/json"
            )
            self.send_header("Content-Length", str(len(body)))
            self.send_header("x-request-id", "local-sdk-fixture")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()

    class QuietFixtureServer(ThreadingHTTPServer):
        daemon_threads = True

        def handle_error(self, request, client_address) -> None:
            pass

    server = QuietFixtureServer(("127.0.0.1", 0), FixtureHandler)
    allowed_port[0] = server.server_port
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    base = "http://127.0.0.1:" + str(server.server_port)
    try:
        with openai.OpenAI(
            api_key="local-fixture-only",
            base_url=base + "/v1",
            max_retries=0,
            timeout=5,
            http_client=openai.DefaultHttpxClient(trust_env=False),
        ) as client:
            with client.responses.stream(
                model="public-model", input="SDK fixture", store=False
            ) as stream:
                chunks = [
                    event.delta
                    for event in stream
                    if event.type == "response.output_text.delta"
                ]
                final = stream.get_final_response()
            wanted = expected_text["openai_responses"]
            require("".join(chunks) == wanted, "responses_stream_text")
            require(final.output_text == wanted, "responses_stream_final")
            require(
                final.usage.total_tokens
                == expected["openai_responses"]["usage"]["total_tokens"],
                "responses_stream_usage",
            )
            response = client.responses.create(
                model="public-model", input="SDK fixture", store=False
            )
            require(response.output_text == wanted, "responses_json")
        print("PASS openai=" + openai.__version__ + " responses_stream responses_json")

        with anthropic.Anthropic(
            api_key="local-fixture-only",
            base_url=base,
            max_retries=0,
            timeout=5,
            http_client=anthropic.DefaultHttpxClient(trust_env=False),
        ) as client:
            request = {
                "model": "public-model",
                "messages": [{"role": "user", "content": "SDK fixture"}],
                "max_tokens": 32,
            }
            with client.messages.stream(**request) as stream:
                chunks = list(stream.text_stream)
                final = stream.get_final_message()
            wanted = expected_text["anthropic_messages"]
            require("".join(chunks) == wanted, "messages_stream_text")
            require(
                "".join(block.text for block in final.content) == wanted,
                "messages_stream_final",
            )
            usage = expected["anthropic_messages"]["usage"]
            require(
                final.usage.input_tokens == usage["input_tokens"]
                and final.usage.output_tokens == usage["output_tokens"],
                "messages_stream_usage",
            )
            message = client.messages.create(**request)
            require(
                "".join(block.text for block in message.content) == wanted,
                "messages_json",
            )
        print("PASS anthropic=" + anthropic.__version__ + " messages_stream messages_json")
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=2)


def main() -> int:
    parser = SafeArgumentParser(description=__doc__)
    parser.add_argument("--fixtures", required=True, type=Path, help="Go exporter output directory")
    arguments = parser.parse_args()
    try:
        run(arguments.fixtures)
    except SmokeFailure as failure:
        print("FAIL " + str(failure), file=sys.stderr)
        return 1
    except Exception:
        # Errors can contain fixture content, request details, or file paths.
        print("FAIL native_sdk_smoke", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
