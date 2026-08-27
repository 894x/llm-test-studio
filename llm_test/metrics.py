"""Domain metrics shared by the dashboard and saved-run reports."""

from loadtest import LoadTestProgress


def percentile(sorted_arr, q):
    if not sorted_arr:
        return 0
    position = (len(sorted_arr) - 1) * q
    lower = int(position)
    upper = min(lower + 1, len(sorted_arr) - 1)
    weight = position - lower
    return sorted_arr[lower] * (1 - weight) + sorted_arr[upper] * weight


def status_label(result):
    if result.status:
        return str(result.status)
    if result.error == "timeout":
        return "Timeout"
    return "Client error"


def compute_metrics(progress: LoadTestProgress):
    results = [r for r in progress.results if r.status == 200]
    completed = len(progress.results)
    ttfts = sorted([r.ttft_ms for r in results if r.ttft_ms > 0])
    tpots = sorted([r.tpot_ms for r in results if r.tpot_ms > 0])
    e2es = sorted([r.e2e_ms for r in results])
    queues = sorted([r.queue_ms for r in progress.results])
    prompt_total = sum(r.prompt_tokens for r in results)
    completion_total = sum(r.completion_tokens for r in results)
    cached_total = sum(r.cached_tokens for r in results)
    elapsed = max(progress.elapsed, 0.001)
    failures = completed - progress.success_count

    return {
        "success": progress.success_count,
        "total": completed,
        "failures": failures,
        "timeouts": sum(1 for r in progress.results if r.error == "timeout"),
        "success_rate": progress.success_count / max(completed, 1) * 100,
        "elapsed": progress.elapsed,
        "in_flight": progress.in_flight,
        "peak_in_flight": progress.peak_in_flight,
        "pending": max(progress.total - progress.done - progress.in_flight, 0),
        "request_qps": completed / elapsed,
        "rpm": completed / elapsed * 60,
        "input_tpm": prompt_total / elapsed * 60,
        "output_tpm": completion_total / elapsed * 60,
        "total_tpm": (prompt_total + completion_total) / elapsed * 60,
        "generation_tps": completion_total / elapsed,
        "ttft_p50": percentile(ttfts, 0.5),
        "ttft_p90": percentile(ttfts, 0.9),
        "ttft_p95": percentile(ttfts, 0.95),
        "ttft_p99": percentile(ttfts, 0.99),
        "ttft_avg": sum(ttfts) / max(len(ttfts), 1),
        "tpot_p50": percentile(tpots, 0.5),
        "tpot_p90": percentile(tpots, 0.9),
        "tpot_p95": percentile(tpots, 0.95),
        "tpot_p99": percentile(tpots, 0.99),
        "tpot_avg": sum(tpots) / max(len(tpots), 1),
        "e2e_p50": percentile(e2es, 0.5),
        "e2e_p90": percentile(e2es, 0.9),
        "e2e_p95": percentile(e2es, 0.95),
        "e2e_p99": percentile(e2es, 0.99),
        "e2e_avg": sum(e2es) / max(len(e2es), 1),
        "queue_p50": percentile(queues, 0.5),
        "queue_p95": percentile(queues, 0.95),
        "queue_avg": sum(queues) / max(len(queues), 1),
        "cached": cached_total,
        "prompt_total": prompt_total,
        "completion_total": completion_total,
        "cache_rate": cached_total / max(prompt_total, 1) * 100,
    }


__all__ = ["compute_metrics", "percentile", "status_label"]
