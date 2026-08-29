#!/usr/bin/env python3
"""Local OpenAI-compatible SSE server for the LLM benchmark integration tests."""

import argparse
import json
import re
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


TOKEN_PATTERN = re.compile(
    r"[\u3400-\u4dbf\u4e00-\u9fff]|[A-Za-z0-9_]+|[^\s]"
)


def parse_args():
    parser = argparse.ArgumentParser(description="Local mock streaming LLM server")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=18080)
    parser.add_argument("--api-key", default="mock-key")
    parser.add_argument(
        "--base-latency-ms",
        type=float,
        default=200,
        help="fixed request/network latency added before the first token",
    )
    parser.add_argument(
        "--prefill-tps",
        type=float,
        default=2000,
        help="simulated input/prefill throughput in tokens per second",
    )
    parser.add_argument(
        "--decode-tps",
        type=float,
        default=20,
        help="simulated output/decode throughput in tokens per second",
    )
    parser.add_argument(
        "--ttft-ms",
        type=float,
        default=None,
        help="fixed TTFT override; disables token-based TTFT calculation",
    )
    parser.add_argument(
        "--tpot-ms",
        type=float,
        default=None,
        help="fixed TPOT override; disables decode-tps for token spacing",
    )
    parser.add_argument(
        "--tokens",
        type=int,
        default=None,
        help="optional legacy cap on generated tokens; defaults to request max_tokens",
    )
    args = parser.parse_args()

    if args.port < 1 or args.port > 65535:
        parser.error("--port must be between 1 and 65535")
    if args.base_latency_ms < 0:
        parser.error("--base-latency-ms must be non-negative")
    if args.prefill_tps <= 0 or args.decode_tps <= 0:
        parser.error("--prefill-tps and --decode-tps must be positive")
    if args.ttft_ms is not None and args.ttft_ms < 0:
        parser.error("--ttft-ms must be non-negative")
    if args.tpot_ms is not None and args.tpot_ms < 0:
        parser.error("--tpot-ms must be non-negative")
    if args.tokens is not None and args.tokens < 1:
        parser.error("--tokens must be positive")
    return args


def estimate_prompt_tokens(messages):
    text_parts = []
    for message in messages:
        if not isinstance(message, dict):
            continue
        content = message.get("content", "")
        if isinstance(content, str):
            text_parts.append(content)
        elif isinstance(content, list):
            for part in content:
                if not isinstance(part, dict):
                    continue
                text = part.get("text")
                if isinstance(text, str):
                    text_parts.append(text)

    content_tokens = len(TOKEN_PATTERN.findall("\n".join(text_parts)))
    message_overhead = len(messages) * 4 + 2
    return max(1, content_tokens + message_overhead)


class MockServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address, handler, config):
        super().__init__(address, handler)
        self.config = config
        self.request_counter = 0
        self.request_counter_lock = threading.Lock()

    def next_request_number(self):
        with self.request_counter_lock:
            self.request_counter += 1
            return self.request_counter


class MockHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *_args):
        return

    def do_GET(self):
        if self.path != "/health":
            self.send_error(404)
            return
        body = json.dumps({"ok": True}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        if self.path != "/v1/chat/completions":
            self._write_json(404, {"error": {"message": "route not found"}})
            return

        expected_authorization = f"Bearer {self.server.config.api_key}"
        if self.headers.get("Authorization") != expected_authorization:
            self._write_json(401, {"error": {"message": "invalid API key"}})
            return

        try:
            content_length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(content_length))
        except (ValueError, json.JSONDecodeError):
            self._write_json(400, {"error": {"message": "invalid JSON body"}})
            return

        if payload.get("stream") is not True:
            self._write_json(400, {"error": {"message": "mock requires stream=true"}})
            return

        messages = payload.get("messages")
        if not isinstance(messages, list) or not messages:
            self._write_json(400, {"error": {"message": "messages is required"}})
            return

        prompt_tokens = estimate_prompt_tokens(messages)
        default_tokens = self.server.config.tokens or 20
        requested_tokens = payload.get("max_tokens", default_tokens)
        if not isinstance(requested_tokens, int) or requested_tokens < 1:
            self._write_json(400, {"error": {"message": "max_tokens must be positive"}})
            return

        if self.server.config.tokens is None:
            completion_tokens = requested_tokens
        else:
            completion_tokens = min(requested_tokens, self.server.config.tokens)

        if self.server.config.ttft_ms is None:
            ttft_ms = (
                self.server.config.base_latency_ms
                + prompt_tokens * 1000 / self.server.config.prefill_tps
                + 1000 / self.server.config.decode_tps
            )
        else:
            ttft_ms = self.server.config.ttft_ms

        if self.server.config.tpot_ms is None:
            tpot_ms = 1000 / self.server.config.decode_tps
        else:
            tpot_ms = self.server.config.tpot_ms

        request_number = self.server.next_request_number()
        response_id = f"chatcmpl-mock-{request_number}-{uuid.uuid4().hex[:8]}"
        model = payload.get("model", "mock-model")

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()

        try:
            self._send_sse(
                {
                    "id": response_id,
                    "model": model,
                    "choices": [
                        {
                            "index": 0,
                            "delta": {"role": "assistant"},
                            "finish_reason": None,
                        }
                    ],
                }
            )

            time.sleep(ttft_ms / 1000)
            for token_index in range(completion_tokens):
                self._send_sse(
                    {
                        "id": response_id,
                        "model": model,
                        "choices": [
                            {
                                "index": 0,
                                "delta": {"content": f"token-{token_index} "},
                                "finish_reason": None,
                            }
                        ],
                    }
                )
                if token_index + 1 < completion_tokens:
                    time.sleep(tpot_ms / 1000)

            self._send_sse(
                {
                    "id": response_id,
                    "model": model,
                    "choices": [
                        {
                            "index": 0,
                            "delta": {},
                            "finish_reason": "stop",
                        }
                    ],
                }
            )
            self._send_sse(
                {
                    "id": response_id,
                    "model": model,
                    "choices": [],
                    "usage": {
                        "prompt_tokens": prompt_tokens,
                        "completion_tokens": completion_tokens,
                        "total_tokens": prompt_tokens + completion_tokens,
                    },
                }
            )
            self._send_sse("[DONE]")
        except (BrokenPipeError, ConnectionResetError):
            pass
        finally:
            self.close_connection = True

    def _send_sse(self, data):
        if isinstance(data, str):
            encoded_data = data
        else:
            encoded_data = json.dumps(data, separators=(",", ":"))
        self.wfile.write(f"data: {encoded_data}\n\n".encode())
        self.wfile.flush()

    def _write_json(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)
        self.close_connection = True


def main():
    config = parse_args()
    server = MockServer((config.host, config.port), MockHandler, config)
    print(
        f"Mock LLM listening on http://{config.host}:{config.port}/v1",
        flush=True,
    )
    if config.ttft_ms is None:
        print(
            "Timing: TTFT = "
            f"{config.base_latency_ms:g}ms base + input/{config.prefill_tps:g} TPS "
            f"+ first output token; TPOT = output/{config.decode_tps:g} TPS",
            flush=True,
        )
    else:
        print(
            f"Timing: fixed TTFT {config.ttft_ms:g}ms; "
            f"TPOT {config.tpot_ms if config.tpot_ms is not None else 1000 / config.decode_tps:g}ms",
            flush=True,
        )
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
