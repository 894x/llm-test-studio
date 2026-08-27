#!/usr/bin/env python3
"""Run kimi-k3 multimodal image-url / video-url test cases."""

import asyncio
import json
import os
import sys
from pathlib import Path

sys.path.insert(0, os.path.dirname(__file__))
from loadtest import LoadTestConfig, LoadTestProgress, run_load_test


PROJECT_DIR = Path(__file__).resolve().parent
CASES_DIR = PROJECT_DIR / "cases" / "kimi-k3"


def build_cases():
    """Build test cases from case.json definitions."""
    cases = []

    # F004 - image_url object form
    with (CASES_DIR / "F004-image-url-object" / "case.json").open(encoding="utf-8") as f:
        c = json.load(f)
    cases.append({
        "id": c["id"], "name": c["name"], "body": c["request"]["body"],
        "expect": "success", "check": lambda r: "error" not in r.get("choices", [{}])[0].get("delta", {}).get("content", "").lower() if r.get("choices") else True,
    })

    # F005 - image_url string form
    with (CASES_DIR / "F005-image-url-string" / "case.json").open(encoding="utf-8") as f:
        c = json.load(f)
    cases.append({
        "id": c["id"], "name": c["name"], "body": c["request"]["body"],
        "expect": "success", "check": lambda r: True,
    })

    # F023 - video_url object form
    with (CASES_DIR / "F023-video-url-object" / "case.json").open(encoding="utf-8") as f:
        c = json.load(f)
    cases.append({
        "id": c["id"], "name": c["name"], "body": c["request"]["body"],
        "expect": "success", "check": lambda r: True,
    })

    # F024 - video_url string form
    with (CASES_DIR / "F024-video-url-string" / "case.json").open(encoding="utf-8") as f:
        c = json.load(f)
    cases.append({
        "id": c["id"], "name": c["name"], "body": c["request"]["body"],
        "expect": "success", "check": lambda r: True,
    })

    # F028 - type=image_url without image_url field (expect 400)
    with (CASES_DIR / "F028-multimodal-image-url-required" / "case.json").open(encoding="utf-8") as f:
        c = json.load(f)
    cases.append({
        "id": c["id"], "name": c["name"], "body": c["request"]["body"],
        "expect": "400", "check": None,
    })

    # F029 - type=video_url without video_url field (expect 400)
    with (CASES_DIR / "F029-multimodal-video-url-required" / "case.json").open(encoding="utf-8") as f:
        c = json.load(f)
    cases.append({
        "id": c["id"], "name": c["name"], "body": c["request"]["body"],
        "expect": "400", "check": None,
    })

    # F030 - image_url object without url field (expect 400)
    with (CASES_DIR / "F030-image-url-object-url-required" / "case.json").open(encoding="utf-8") as f:
        c = json.load(f)
    cases.append({
        "id": c["id"], "name": c["name"], "body": c["request"]["body"],
        "expect": "400", "check": None,
    })

    # F031 - video_url object without url field (expect 400)
    with (CASES_DIR / "F031-video-url-object-url-required" / "case.json").open(encoding="utf-8") as f:
        c = json.load(f)
    cases.append({
        "id": c["id"], "name": c["name"], "body": c["request"]["body"],
        "expect": "400", "check": None,
    })

    return cases


async def send(url, key, body, model="kimi-k3"):
    """Send a single request and return (status, response_body, error)."""
    import aiohttp, time
    payload = {**body, "model": model, "temperature": 1.0}
    headers = {"Content-Type": "application/json", "Authorization": f"Bearer {key}"}
    start = time.time()
    try:
        async with aiohttp.ClientSession() as session:
            async with session.post(url, json=payload, headers=headers,
                                     timeout=aiohttp.ClientTimeout(total=60)) as resp:
                status = resp.status
                raw = await resp.text()
                e2e = (time.time() - start) * 1000
                try:
                    rbody = json.loads(raw)
                except:
                    rbody = {"raw": raw[:500]}
                return status, rbody, "", e2e
    except Exception as e:
        return 0, {}, str(e), (time.time() - start) * 1000


async def main():
    key = os.getenv("LOADTEST_API_KEY")
    if not key:
        raise SystemExit("LOADTEST_API_KEY is required")
    url = os.getenv(
        "LOADTEST_URL",
        "https://one2api.xunlitec.com/v1/chat/completions",
    )
    model = os.getenv("LOADTEST_MODEL", "kimi-k3")

    cases = build_cases()
    print(f"=== Kimi-K3 Multimodal Test: {len(cases)} cases ===\n")

    results = []
    for c in cases:
        print(f"[{c['id']}] {c['name']}...", end=" ", flush=True)
        status, body, err, e2e = await send(url, key, c["body"], model)

        if c["expect"] == "success":
            passed = status == 200
            detail = f"HTTP {status} ({e2e:.0f}ms)"
            if status == 200:
                content = ""
                if body.get("choices"):
                    for ch in body["choices"]:
                        d = ch.get("delta", {})
                        content += d.get("content", "")
                detail += f" | content: {content[:100]}"
            elif err:
                detail += f" | {err[:80]}"
        else:
            passed = str(status) == c["expect"]
            detail = f"HTTP {status} ({e2e:.0f}ms)"
            if status >= 400:
                err_msg = body.get("error", {}).get("message", "")
                detail += f" | {err_msg[:100]}"

        icon = "✅" if passed else "❌"
        print(f"{icon} {detail}")
        results.append({"id": c["id"], "name": c["name"], "passed": passed,
                         "status": status, "e2e_ms": round(e2e, 0),
                         "detail": detail, "response": body})

    passed_count = sum(1 for r in results if r["passed"])
    print(f"\n{'='*60}")
    print(f"Result: {passed_count}/{len(results)} passed")
    for r in results:
        if not r["passed"]:
            print(f"  ❌ {r['id']}: {r['detail']}")
    print(f"{'='*60}")

    output_path = PROJECT_DIR / "multimodal_results.json"
    with output_path.open("w", encoding="utf-8") as f:
        json.dump(results, f, indent=2, ensure_ascii=False, default=str)
    print(f"Saved to {output_path}")


if __name__ == "__main__":
    asyncio.run(main())
