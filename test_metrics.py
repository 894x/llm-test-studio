import unittest

from loadtest import LoadTestProgress, RequestResult
from llm_test.metrics import compute_metrics, percentile, status_label


class PercentileTest(unittest.TestCase):
    def test_empty_and_interpolated_values_match_dashboard_contract(self):
        self.assertEqual(percentile([], 0.5), 0)
        self.assertEqual(percentile([10], 0.99), 10)
        self.assertEqual(percentile([10, 20, 30, 40], 0.5), 25)
        self.assertEqual(percentile([10, 20, 30, 40], 0.9), 37)


class StatusLabelTest(unittest.TestCase):
    def test_status_timeout_and_client_error_labels(self):
        self.assertEqual(status_label(RequestResult(id=1, status=429)), "429")
        self.assertEqual(
            status_label(RequestResult(id=2, error="timeout")), "Timeout"
        )
        self.assertEqual(
            status_label(RequestResult(id=3, error="connection reset")),
            "Client error",
        )


class ComputeMetricsTest(unittest.TestCase):
    def test_metrics_keep_success_filtering_and_progress_contract(self):
        progress = LoadTestProgress(
            done=3,
            total=5,
            results=[
                RequestResult(
                    id=0,
                    status=200,
                    e2e_ms=100,
                    ttft_ms=20,
                    tpot_ms=4,
                    queue_ms=3,
                    prompt_tokens=100,
                    completion_tokens=20,
                    cached_tokens=40,
                ),
                RequestResult(
                    id=1,
                    status=200,
                    e2e_ms=300,
                    ttft_ms=60,
                    tpot_ms=8,
                    queue_ms=1,
                    prompt_tokens=300,
                    completion_tokens=60,
                    cached_tokens=80,
                ),
                RequestResult(
                    id=2,
                    status=0,
                    e2e_ms=500,
                    ttft_ms=400,
                    tpot_ms=40,
                    queue_ms=5,
                    prompt_tokens=999,
                    completion_tokens=999,
                    cached_tokens=999,
                    error="timeout",
                ),
            ],
            start_time=10,
            end_time=12,
            in_flight=1,
            peak_in_flight=2,
        )

        metrics = compute_metrics(progress)

        self.assertEqual(metrics["success"], 2)
        self.assertEqual(metrics["total"], 3)
        self.assertEqual(metrics["failures"], 1)
        self.assertEqual(metrics["timeouts"], 1)
        self.assertAlmostEqual(metrics["success_rate"], 200 / 3)
        self.assertEqual(metrics["elapsed"], 2)
        self.assertEqual(metrics["in_flight"], 1)
        self.assertEqual(metrics["peak_in_flight"], 2)
        self.assertEqual(metrics["pending"], 1)
        self.assertEqual(metrics["request_qps"], 1.5)
        self.assertEqual(metrics["rpm"], 90)
        self.assertEqual(metrics["input_tpm"], 12_000)
        self.assertEqual(metrics["output_tpm"], 2_400)
        self.assertEqual(metrics["total_tpm"], 14_400)
        self.assertEqual(metrics["generation_tps"], 40)
        self.assertEqual(metrics["ttft_p50"], 40)
        self.assertEqual(metrics["tpot_p50"], 6)
        self.assertEqual(metrics["e2e_p50"], 200)
        self.assertEqual(metrics["queue_p50"], 3)
        self.assertEqual(metrics["prompt_total"], 400)
        self.assertEqual(metrics["completion_total"], 80)
        self.assertEqual(metrics["cached"], 120)
        self.assertEqual(metrics["cache_rate"], 30)

    def test_empty_progress_uses_elapsed_floor(self):
        metrics = compute_metrics(LoadTestProgress(total=4, done=0))

        self.assertEqual(metrics["total"], 0)
        self.assertEqual(metrics["request_qps"], 0)
        self.assertEqual(metrics["success_rate"], 0)
        self.assertEqual(metrics["pending"], 4)


if __name__ == "__main__":
    unittest.main()
