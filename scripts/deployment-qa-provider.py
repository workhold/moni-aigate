#!/usr/bin/env python3
"""Deterministic OpenAI-compatible upstream for the Compose business-qa profile."""

from __future__ import annotations

import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    server_version = "moni-aigate-qa-provider/1"

    def log_message(self, *_args: object) -> None:
        return

    def _json(self, status: int, value: object) -> None:
        raw = json.dumps(value, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self) -> None:
        if self.path == "/health":
            self._json(200, {"status": "ok"})
            return
        self._json(404, {"error": {"message": "not found", "type": "qa_provider"}})

    def do_POST(self) -> None:
        if self.path != "/v1/chat/completions":
            self._json(404, {"error": {"message": "not found", "type": "qa_provider"}})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            body = json.loads(self.rfile.read(length))
        except (ValueError, json.JSONDecodeError):
            self._json(400, {"error": {"message": "invalid json", "type": "invalid_request_error"}})
            return
        model = body.get("model")
        if not isinstance(model, str) or not model or not isinstance(body.get("messages"), list):
            self._json(400, {"error": {"message": "invalid request", "type": "invalid_request_error"}})
            return
        usage = {"prompt_tokens": 4, "completion_tokens": 2, "total_tokens": 6}
        response_id = "chatcmpl_moni_qa"
        if body.get("stream") is True:
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Connection", "close")
            self.end_headers()

            def write(value: object) -> None:
                raw = json.dumps(value, separators=(",", ":"))
                self.wfile.write((f"data: {raw}\n\n").encode())
                self.wfile.flush()

            write({
                "id": response_id,
                "object": "chat.completion.chunk",
                "model": model,
                "choices": [{"index": 0, "delta": {"role": "assistant", "content": "QA"}, "finish_reason": None}],
            })
            write({
                "id": response_id,
                "object": "chat.completion.chunk",
                "model": model,
                "choices": [{"index": 0, "delta": {"content": " OK"}, "finish_reason": None}],
            })
            write({
                "id": response_id,
                "object": "chat.completion.chunk",
                "model": model,
                "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
                "usage": usage,
            })
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
            return
        self._json(200, {
            "id": response_id,
            "object": "chat.completion",
            "model": model,
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "QA OK"}, "finish_reason": "stop"}],
            "usage": usage,
        })


if __name__ == "__main__":
    server = ThreadingHTTPServer(("0.0.0.0", 80), Handler)
    server.serve_forever()
