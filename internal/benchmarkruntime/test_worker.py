"""Offline acceptance: actual Inspect, deterministic local OpenAI-compatible API."""

import asyncio
import io
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import threading
import unittest
from unittest.mock import patch

import worker


class ModelHandler(BaseHTTPRequestHandler):
    calls = 0

    def log_message(self, *args):
        pass

    def do_POST(self):
        data = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        type(self).calls += 1
        assert self.path == self.server.expected_path
        assert data.get("stream", False) is False
        if self.path == "/v1/messages":
            assert self.headers["Authorization"] == "Bearer private-test-key"
            assert self.headers["anthropic-version"]
            response = dict(id="msg_test", type="message", role="assistant", model=data["model"],
                            content=[dict(type="text", text="42")], stop_reason="end_turn",
                            stop_sequence=None, usage=dict(input_tokens=5, output_tokens=1))
        elif self.path == "/v1/responses":
            assert self.headers["Authorization"] == "Bearer private-test-key"
            response = dict(id="resp_test", object="response", created_at=1, status="completed",
                            model=data["model"], output=[dict(id="msg_test", type="message",
                            status="completed", role="assistant", content=[dict(type="output_text",
                            text="42", annotations=[])])], tools=[], tool_choice="auto",
                            parallel_tool_calls=False, usage=dict(input_tokens=5, output_tokens=1,
                            total_tokens=6, input_tokens_details=dict(cached_tokens=0),
                            output_tokens_details=dict(reasoning_tokens=0)))
        else:
            assert self.path == "/v1/chat/completions"
            assert self.headers["Authorization"] == "Bearer private-test-key"
            response = dict(id="test", object="chat.completion", created=0,
                               model=data["model"], choices=[dict(index=0,
                               message=dict(role="assistant", content="42"),
                               finish_reason="stop")],
                               usage=dict(prompt_tokens=5, completion_tokens=1,
                                          total_tokens=6))
        body = json.dumps(response).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class WorkerTests(unittest.TestCase):
    def test_actual_inspect_progress_scores_and_epochs(self):
        self.run_fixture("openai-chat", 2)

    def test_actual_inspect_responses_and_anthropic_protocols(self):
        for protocol in ["openai-responses", "anthropic-messages"]:
            with self.subTest(protocol=protocol):
                self.run_fixture(protocol, 1)

    def run_fixture(self, protocol, epochs):
        from inspect_ai import Task
        from inspect_ai.dataset import Sample
        from inspect_ai.scorer import match
        from inspect_ai.solver import generate
        import tempfile

        server = ThreadingHTTPServer(("127.0.0.1", 0), ModelHandler)
        server.expected_path = {"openai-chat": "/v1/chat/completions",
                                "openai-responses": "/v1/responses",
                                "anthropic-messages": "/v1/messages"}[protocol]
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        output = io.StringIO()
        original = worker.PROTOCOL
        worker.PROTOCOL = output
        self.addCleanup(setattr, worker, "PROTOCOL", original)
        with tempfile.TemporaryDirectory() as directory:
            request = dict(schema_version=1, run_id="run", report_id="report",
                           dataset_id="fixture", protocol=protocol, model="test-model",
                           base_url=f"http://127.0.0.1:{server.server_port}" +
                                    ("" if protocol == "anthropic-messages" else "/v1"),
                           api_key="private-test-key", log_dir=directory,
                           options=dict(sample_limit=2, epochs=epochs,
                                        max_tokens=8192 if protocol == "anthropic-messages" else 16,
                                        concurrency=2, temperature=0, seed=1,
                                        timeout_seconds=5, stream=False, reasoning_effort=""))
            task = Task(dataset=[Sample(id="one", input="What is 6 times 7?", target="42"),
                                 Sample(id="two", input="What is 40 plus 2?", target="42")],
                        solver=generate(), scorer=match())
            with patch.dict("os.environ", {"ANTHROPIC_AUTH_TOKEN": "ambient-test-token"}):
                asyncio.run(worker.execute(request, task_factory=lambda _: task))
        events = [json.loads(line) for line in output.getvalue().splitlines()]
        result = events[-1]
        self.assertEqual(result["kind"], "result")
        self.assertEqual(result["status"], "completed")
        self.assertEqual(result["total"], 2 * epochs)
        self.assertEqual(result["completed"], 2 * epochs)
        self.assertEqual(result["failed"], 0)
        self.assertTrue(any(m["name"] == "accuracy" and m["value"] == 1 for m in result["metrics"]))
        self.assertEqual(len(result["provenance"]["input_sha256"]), 64)
        self.assertTrue(any(item["kind"] == "progress" and item["in_flight"] > 0 for item in events))
        samples = {(e["sample"]["id"], e["sample"]["epoch"]) for e in events if e["kind"] == "sample"}
        self.assertEqual(len(samples), 2 * epochs)
        self.assertNotIn("private-test-key", output.getvalue())

    def test_projection_redacts_and_bounds_text(self):
        reporter = worker.Reporter(dict(api_key="private-key", run_id="r", report_id="p"))
        self.assertEqual(reporter.text("private-key\x00"), "[redacted]")
        self.assertEqual(len(reporter.text("x" * 20000)), worker.MAX_TEXT)

    def test_registry_rejects_arbitrary_task_imports(self):
        with self.assertRaises(StopIteration):
            worker.load_task("os/system")

    def test_all_registered_factories_import_without_optional_dependencies(self):
        import importlib
        import inspect
        from pathlib import Path
        manifest = json.loads(Path("datasets.json").read_text("utf-8"))
        for item in manifest["datasets"]:
            with self.subTest(dataset=item["id"]):
                factory = getattr(importlib.import_module(item["module"]), item["factory"])
                self.assertTrue(callable(factory))
                inspect.signature(factory).bind()

    def test_dataset_download_prepares_samples_without_model_requests(self):
        from inspect_ai import Task
        from inspect_ai.dataset import Sample
        import tempfile
        from pathlib import Path
        output = io.StringIO()
        with tempfile.TemporaryDirectory() as directory, patch.object(worker, "PROTOCOL", output):
            Path(directory, "data.json").write_text("cached data", "utf-8")
            task = Task(dataset=[Sample(id="one", input="Question", target="42")])
            request = dict(cache_dir=directory, dataset_id="fixture")
            with patch("inspect_ai.model.get_model", side_effect=AssertionError("unexpected model request")):
                worker.download_dataset(request, task_factory=lambda _: task)
        result = json.loads(output.getvalue().splitlines()[-1])
        self.assertEqual(result["kind"], "ready")
        self.assertEqual(result["samples"], 1)
        self.assertEqual(result["bytes"], 11)
        self.assertEqual(len(result["input_sha256"]), 64)


if __name__ == "__main__":
    unittest.main()
