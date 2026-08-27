#!/usr/bin/env python3
"""100-request test with full response recording and parameter diff."""

import asyncio
import json
import time
import sys
import os
from pathlib import Path

sys.path.insert(0, os.path.dirname(__file__))
from loadtest import LoadTestConfig, LoadTestProgress, run_load_test


PROJECT_DIR = Path(__file__).resolve().parent


async def main():
    api_key = os.getenv("LOADTEST_API_KEY") or os.getenv("KIMI_K3_API_KEY")
    if not api_key:
        raise SystemExit("LOADTEST_API_KEY is required")

    cfg = LoadTestConfig(
        url=os.getenv("LOADTEST_URL", "https://one2api.xunlitec.com/v1/chat/completions"),
        key=api_key,
        model=os.getenv("LOADTEST_MODEL", "kimi-k3"),
        total_requests=100,
        duration_s=60,
        max_tokens=10,
        input_tokens=100,
        stream=True,
        random=True,
    )

    progress = LoadTestProgress()

    def on_progress(p):
        sys.stdout.write(f"\r  {p.done}/{p.total} done, {p.elapsed:.1f}s elapsed...")
        sys.stdout.flush()

    print(f"=== 100-Request Test: {cfg.model} on {cfg.url} ===")
    print(f"Duration: {cfg.duration_s}s, Random: {cfg.random}")
    print(f"Input: ~{cfg.input_tokens} tokens, Output: {cfg.max_tokens} tokens")
    print()

    await run_load_test(cfg, progress, on_progress)
    print()

    # Save all responses
    recordings = []
    for r in progress.results:
        rec = {
            "id": r.id,
            "status": r.status,
            "e2e_ms": round(r.e2e_ms, 1),
            "ttft_ms": round(r.ttft_ms, 1),
            "tpot_ms": round(r.tpot_ms, 1),
            "prompt_tokens": r.prompt_tokens,
            "completion_tokens": r.completion_tokens,
            "cached_tokens": r.cached_tokens,
            "error": r.error,
            "chunks": r.chunks,
            "request_body": r.request_body,
            "response_body": r.response_body,
            "response_text": r.response_text[:500],
        }
        recordings.append(rec)

    output_file = PROJECT_DIR / "test_results_100req.json"
    with output_file.open("w", encoding="utf-8") as f:
        json.dump(recordings, f, indent=2, ensure_ascii=False)
    print(f"Saved {len(recordings)} recordings to {output_file}")

    # Analyze parameter structure differences
    print("\n=== Parameter Structure Analysis ===")

    # Group by status
    ok = [r for r in recordings if r["status"] == 200]
    fail = [r for r in recordings if r["status"] != 200]
    print(f"Success: {len(ok)}, Failed: {len(fail)}")

    if ok:
        # Check request body consistency
        models = set(r["request_body"].get("model") for r in ok)
        temps = set(r["request_body"].get("temperature") for r in ok)
        maxtoks = set(r["request_body"].get("max_tokens") for r in ok)
        streams = set(r["request_body"].get("stream") for r in ok)
        print(f"\nRequest fields (from {len(ok)} successful):")
        print(f"  model:       {models}")
        print(f"  temperature: {temps}")
        print(f"  max_tokens:  {maxtoks}")
        print(f"  stream:      {streams}")

        # Check response body structure
        resp_keys_sets = [set(r["response_body"].keys()) for r in ok if r["response_body"]]
        if resp_keys_sets:
            all_keys = set()
            for ks in resp_keys_sets:
                all_keys.update(ks)
            common_keys = resp_keys_sets[0]
            for ks in resp_keys_sets[1:]:
                common_keys &= ks
            print(f"\nResponse fields:")
            print(f"  Common: {sorted(common_keys)}")
            if all_keys - common_keys:
                print(f"  Varying: {sorted(all_keys - common_keys)}")

        # Check usage consistency
        usage_keys = set()
        for r in ok:
            if r["response_body"] and "usage" in r["response_body"]:
                usage_keys.update(r["response_body"]["usage"].keys())
        print(f"\nUsage fields: {sorted(usage_keys)}")

        # Latency stats
        ttfts = sorted([r["ttft_ms"] for r in ok if r["ttft_ms"] > 0])
        e2es = sorted([r["e2e_ms"] for r in ok])
        print(f"\nLatency:")
        if ttfts:
            print(f"  TTFT P50={ttfts[len(ttfts)//2]:.0f}ms P90={ttfts[int(len(ttfts)*0.9)]:.0f}ms")
        print(f"  E2E  P50={e2es[len(e2es)//2]:.0f}ms P90={e2es[int(len(e2es)*0.9)]:.0f}ms")

    if fail:
        print(f"\nFailed requests:")
        status_codes = {}
        for r in fail:
            status_codes[r["status"]] = status_codes.get(r["status"], 0) + 1
        print(f"  Status codes: {status_codes}")
        for r in fail[:3]:
            print(f"  #{r['id']}: HTTP {r['status']} - {r['error'][:100]}")


if __name__ == "__main__":
    asyncio.run(main())
