import unittest
from io import BytesIO
from types import SimpleNamespace

from PIL import Image

from report_export import build_report_artifacts, build_timeline_data


class ReportExportTest(unittest.TestCase):
    def setUp(self):
        self.summary = {
            "model": "model <alpha>",
            "ts": "2026-08-27T10:00:00",
            "url": "https://example.test/v1/chat/completions?a=1&b=2",
            "requests": 2,
        }
        self.metrics = {
            "success_rate": 50.0,
            "total": 2,
            "failures": 1,
            "request_qps": 2.0,
            "peak_in_flight": 2,
            "elapsed": 1.0,
            "rpm": 120.0,
            "input_tpm": 240.0,
            "output_tpm": 60.0,
            "total_tpm": 300.0,
            "generation_tps": 1.0,
            "cache_rate": 25.0,
            "ttft_avg": 100.0,
            "ttft_p50": 100.0,
            "ttft_p90": 100.0,
            "ttft_p95": 100.0,
            "ttft_p99": 100.0,
            "tpot_avg": 12.5,
            "tpot_p50": 12.5,
            "tpot_p90": 12.5,
            "tpot_p95": 12.5,
            "tpot_p99": 12.5,
            "e2e_avg": 600.0,
            "e2e_p50": 600.0,
            "e2e_p90": 600.0,
            "e2e_p95": 600.0,
            "e2e_p99": 600.0,
        }
        self.results = [
            SimpleNamespace(
                id=1, status=200, error="", ttft_ms=100.0, tpot_ms=12.5,
                e2e_ms=600.0, queue_ms=0.5, prompt_tokens=4,
                completion_tokens=1, started_at_s=0.1, completed_at_s=0.6,
            ),
            SimpleNamespace(
                id=2, status=500, error="upstream <failed>", ttft_ms=0.0,
                tpot_ms=0.0, e2e_ms=50.0, queue_ms=1.0, prompt_tokens=0,
                completion_tokens=0, started_at_s=0.2, completed_at_s=0.4,
            ),
        ]

    def test_timeline_data_reconstructs_in_flight_and_status_series(self):
        timeline = build_timeline_data(self.results)

        self.assertEqual(
            timeline["concurrency"],
            [(0.1, 1), (0.2, 2), (0.4, 1), (0.6, 0)],
        )
        self.assertEqual(timeline["statuses"], {"200": 1, "500": 1})

    def test_builds_standalone_escaped_html_and_valid_binary_exports(self):
        artifacts = build_report_artifacts(
            7,
            self.summary,
            self.metrics,
            self.results,
        )

        html = artifacts.html.decode("utf-8")
        self.assertIn("<!doctype html>", html)
        self.assertIn("API Load Test Report <span>#7</span>", html)
        self.assertIn("model &lt;alpha&gt;", html)
        self.assertIn("upstream &lt;failed&gt;", html)
        self.assertIn("<h2>Timelines</h2>", html)
        self.assertIn("In-flight requests", html)
        self.assertIn("Client queue (ms)", html)
        self.assertIn("Status codes", html)
        self.assertNotIn("model <alpha>", html)

        self.assertTrue(artifacts.png.startswith(b"\x89PNG\r\n\x1a\n"))
        image = Image.open(BytesIO(artifacts.png))
        self.assertEqual(image.size, (1600, 3000))

        self.assertTrue(artifacts.pdf.startswith(b"%PDF"))
        self.assertGreaterEqual(artifacts.pdf.count(b"/Type /Page"), 2)


if __name__ == "__main__":
    unittest.main()
