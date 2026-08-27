#!/usr/bin/env python3
"""Test kimi-k3 video capability using F024-video-url-string case."""
import asyncio
import json
import os
import sys
import time
from pathlib import Path

import aiohttp

PROJECT_DIR = Path(__file__).resolve().parent
CASES_DIR = PROJECT_DIR / "cases" / "kimi-k3"
URL = os.getenv(
    "LOADTEST_URL",
    "https://one2api.xunlitec.com/v1/chat/completions",
)
KEY = os.getenv("LOADTEST_API_KEY", "")
MODEL = os.getenv("LOADTEST_MODEL", "kimi-k3")


def load_case(case_dir):
    with (CASES_DIR / case_dir / "case.json").open(encoding="utf-8") as f:
        return json.load(f)


async def send(body):
    headers = {"Content-Type": "application/json", "Authorization": f"Bearer {KEY}"}
    start = time.time()
    async with aiohttp.ClientSession() as session:
        async with session.post(URL, json=body, headers=headers,
                                 timeout=aiohttp.ClientTimeout(total=120)) as resp:
            status = resp.status
            if status != 200:
                raw = await resp.text()
                return status, raw, (time.time() - start) * 1000

            content, reasoning = [], []
            usage = {}
            async for raw in resp.content:
                line = raw.decode("utf-8", errors="replace").strip()
                if not line.startswith("data: ") or line[6:] == "[DONE]":
                    if line.startswith("data: ") and line[6:] == "[DONE]":
                        break
                    continue
                try:
                    c = json.loads(line[6:])
                    if c.get("usage"):
                        usage = c["usage"]
                    if c.get("choices"):
                        d = c["choices"][0].get("delta", {})
                        if d.get("content"):
                            content.append(d["content"])
                        if d.get("reasoning_content"):
                            reasoning.append(d["reasoning_content"])
                except json.JSONDecodeError:
                    pass
            e2e = (time.time() - start) * 1000
            text = "".join(content)
            reason = "".join(reasoning)
            return status, {"content": text, "reasoning": reason, "usage": usage}, e2e


async def main():
    if not KEY:
        raise SystemExit("LOADTEST_API_KEY is required")

    cases = [
        ("F024-video-url-string", "video_url 字符串形式"),
        ("F023-video-url-object", "video_url 对象形式"),
    ]

    for case_id, label in cases:
        print(f"=== [{case_id}] {label} ===")
        case = load_case(case_id)
        body = case["request"]["body"]
        body["model"] = MODEL
        body["temperature"] = 1.0

        print(f"  Sending request...", flush=True)
        status, result, e2e = await send(body)

        print(f"  HTTP {status} ({e2e:.0f}ms)")
        if status == 200:
            print(f"  Content: \"{result['content']}\"")
            if result["reasoning"]:
                print(f"  Reasoning (first 200): \"{result['reasoning'][:200]}\"")
            u = result["usage"]
            print(f"  Usage: prompt={u.get('prompt_tokens',0)} "
                  f"completion={u.get('completion_tokens',0)} "
                  f"cached={u.get('cached_tokens',u.get('prompt_tokens_details',{}).get('cached_tokens',0))}")
        else:
            err = result[:300] if isinstance(result, str) else str(result)[:300]
            print(f"  Error: {err}")
        print()

    # Also test a quick image case for comparison
    print("=== [F004] image_url object (baseline) ===")
    tiny_png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="
    body = {
        "model": MODEL,
        "messages": [{"role": "user", "content": [
            {"type": "text", "text": "What color is this image? Reply one word."},
            {"type": "image_url", "image_url": {"url": f"data:image/png;base64,{tiny_png}"}}
        ]}],
        "max_tokens": 50,
        "stream": True,
        "stream_options": {"include_usage": True},
        "temperature": 1.0,
    }
    status, result, e2e = await send(body)
    print(f"  HTTP {status} ({e2e:.0f}ms)")
    if status == 200:
        print(f"  Content: \"{result['content']}\"")
    print()


if __name__ == "__main__":
    asyncio.run(main())
