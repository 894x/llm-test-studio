"""Load test engine — async HTTP client with streaming support."""

import asyncio
import json
import time
import random
from dataclasses import dataclass, field

import aiohttp


MAX_CONCURRENT_CONNECTIONS = 2000
DEFAULT_IMAGE_PROMPT = "识别图片中央的数字，并用“图片：<数字>”格式回答。"
DEFAULT_VIDEO_PROMPT = (
    "按照出现顺序识别视频中的三个数字，并用“视频：<数字,数字,数字>”格式回答。"
)


@dataclass
class RequestResult:
    id: int
    status: int = 0
    e2e_ms: float = 0
    ttft_ms: float = 0
    tpot_ms: float = 0
    prompt_tokens: int = 0
    completion_tokens: int = 0
    cached_tokens: int = 0
    error: str = ""
    chunks: int = 0
    request_body: dict = field(default_factory=dict)
    response_body: dict = field(default_factory=dict)
    response_text: str = ""
    queue_ms: float = 0
    started_at_s: float = 0
    completed_at_s: float = 0


@dataclass
class LoadTestConfig:
    url: str
    key: str
    model: str
    total_requests: int
    duration_s: int
    max_tokens: int
    input_tokens: int
    stream: bool = True
    random: bool = True
    max_connections: int = MAX_CONCURRENT_CONNECTIONS
    request_timeout_s: int = 120
    image_data_url: str = ""
    image_prompt: str = DEFAULT_IMAGE_PROMPT
    video_data_url: str = ""
    video_prompt: str = DEFAULT_VIDEO_PROMPT


@dataclass
class LoadTestProgress:
    done: int = 0
    total: int = 0
    results: list = field(default_factory=list)
    running: bool = False
    start_time: float = 0
    end_time: float = 0
    in_flight: int = 0
    peak_in_flight: int = 0

    @property
    def success_count(self) -> int:
        return sum(1 for r in self.results if r.status == 200)

    @property
    def elapsed(self) -> float:
        if self.start_time == 0:
            return 0
        end_time = self.end_time or time.time()
        return end_time - self.start_time


def generate_text(target_tokens: int) -> str:
    block = (
        "The quick brown fox jumps over the lazy dog. "
        "System analysis shows that network protocol optimization "
        "requires careful calibration of temperature and pressure parameters. "
        "Database server response indicates successful endpoint processing. "
        "Algorithm heuristic convergence achieved through iterative recursion. "
        "Metadata interface implementation follows standard specification. "
    )
    target_chars = target_tokens * 7
    if target_chars < len(block):
        target_chars = len(block)
    repeats = target_chars // len(block)
    remainder = target_chars % len(block)
    parts = [block] * repeats
    if remainder > 0:
        parts.append(block[:remainder])
    return "".join(parts)


def build_messages(config: LoadTestConfig) -> list:
    """Build one text-only or multimodal user message for the configured run."""
    generated_text = generate_text(config.input_tokens)
    if not config.image_data_url and not config.video_data_url:
        return [{"role": "user", "content": generated_text}]

    content = [{"type": "text", "text": generated_text}]
    if config.image_data_url:
        content.extend([
            {"type": "text", "text": config.image_prompt.strip()},
            {
                "type": "image_url",
                "image_url": {"url": config.image_data_url},
            },
        ])
    if config.video_data_url:
        content.extend([
            {"type": "text", "text": config.video_prompt.strip()},
            {
                "type": "video_url",
                "video_url": {"url": config.video_data_url},
            },
        ])
    return [{"role": "user", "content": content}]


def compact_recorded_payload(value):
    """Keep request structure while replacing large Base64 media in recordings."""
    if isinstance(value, dict):
        return {key: compact_recorded_payload(item) for key, item in value.items()}
    if isinstance(value, list):
        return [compact_recorded_payload(item) for item in value]
    if isinstance(value, str) and value.startswith("data:") and ";base64," in value:
        header, encoded = value.split(",", 1)
        return f"{header},<{len(encoded):,} base64 chars>"
    return value


async def _send_request(
    session: aiohttp.ClientSession,
    url: str,
    key: str,
    model: str,
    messages: list,
    max_tokens: int,
    stream: bool,
    req_id: int,
    timeout_s: int = 120,
    record: bool = False,
) -> RequestResult:
    r = RequestResult(id=req_id)
    payload = {
        "model": model,
        "messages": messages,
        "max_tokens": max_tokens,
        "temperature": 1.0,
    }
    if stream:
        payload["stream"] = True
        payload["stream_options"] = {"include_usage": True}

    if record:
        r.request_body = compact_recorded_payload(payload)

    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {key}",
    }

    start = time.time()
    try:
        async with session.post(
            url, json=payload, headers=headers, timeout=aiohttp.ClientTimeout(total=timeout_s)
        ) as resp:
            r.status = resp.status
            if resp.status != 200:
                body = await resp.text()
                r.error = body
                r.response_text = body
                r.e2e_ms = (time.time() - start) * 1000
                return r

            if stream:
                first_token = True
                full_content = []
                full_reasoning = []
                usage = {}
                async for raw_line in resp.content:
                    line = raw_line.decode("utf-8", errors="replace").strip()
                    if not line.startswith("data: ") or line[6:] == "[DONE]":
                        if line.startswith("data: ") and line[6:] == "[DONE]":
                            break
                        continue
                    try:
                        c = json.loads(line[6:])
                        r.chunks += 1
                        if c.get("usage"):
                            usage = c["usage"]
                            r.prompt_tokens = usage.get("prompt_tokens", 0)
                            r.completion_tokens = usage.get("completion_tokens", 0)
                            details = usage.get("prompt_tokens_details", {})
                            r.cached_tokens = details.get("cached_tokens", 0)
                        if c.get("choices"):
                            d = c["choices"][0].get("delta", {})
                            if d:
                                if d.get("content"):
                                    full_content.append(d["content"])
                                if d.get("reasoning_content"):
                                    full_reasoning.append(d["reasoning_content"])
                            if first_token and (d.get("content") or d.get("reasoning_content")):
                                r.ttft_ms = (time.time() - start) * 1000
                                first_token = False
                    except json.JSONDecodeError:
                        pass
                r.e2e_ms = (time.time() - start) * 1000
                if record:
                    r.response_text = "".join(full_content)
                    r.response_body = {
                        "content": r.response_text,
                        "reasoning_content": "".join(full_reasoning),
                        "usage": usage,
                        "chunks": r.chunks,
                    }
            else:
                body = await resp.text()
                r.e2e_ms = (time.time() - start) * 1000
                r.response_text = body
                try:
                    cr = json.loads(body)
                    u = cr.get("usage", {})
                    r.prompt_tokens = u.get("prompt_tokens", 0)
                    r.completion_tokens = u.get("completion_tokens", 0)
                    details = u.get("prompt_tokens_details", {})
                    r.cached_tokens = details.get("cached_tokens", 0)
                    if record:
                        r.response_body = cr
                except Exception:
                    pass

    except asyncio.TimeoutError:
        r.error = "timeout"
        r.e2e_ms = (time.time() - start) * 1000
    except Exception as e:
        r.error = str(e)
        r.e2e_ms = (time.time() - start) * 1000

    if r.completion_tokens > 0 and r.ttft_ms > 0 and r.e2e_ms > r.ttft_ms:
        r.tpot_ms = (r.e2e_ms - r.ttft_ms) / r.completion_tokens

    return r


async def run_load_test(
    config: LoadTestConfig,
    progress: LoadTestProgress,
    on_progress=None,
):
    """Run the load test, updating progress in-place and calling on_progress callback."""
    if config.total_requests < 1:
        raise ValueError("total_requests must be at least 1")
    if not 1 <= config.max_connections <= MAX_CONCURRENT_CONNECTIONS:
        raise ValueError(
            f"max_connections must be between 1 and {MAX_CONCURRENT_CONNECTIONS}"
        )
    if config.request_timeout_s < 1:
        raise ValueError("request_timeout_s must be at least 1")
    if config.image_data_url and not config.image_prompt.strip():
        raise ValueError("image_prompt cannot be empty when an image is included")
    if config.video_data_url and not config.video_prompt.strip():
        raise ValueError("video_prompt cannot be empty when a video is included")

    progress.running = True
    progress.start_time = time.time()
    progress.end_time = 0
    progress.done = 0
    progress.total = config.total_requests
    progress.results = []
    progress.in_flight = 0
    progress.peak_in_flight = 0

    messages = build_messages(config)

    connector = aiohttp.TCPConnector(
        limit=config.max_connections,
        limit_per_host=config.max_connections,
    )
    try:
        async with aiohttp.ClientSession(connector=connector) as session:
            semaphore = asyncio.Semaphore(
                min(config.total_requests, config.max_connections)
            )
            last_progress_update = 0.0

            async def _limited_send(req_id):
                nonlocal last_progress_update
                ready_at = time.time()
                async with semaphore:
                    started_at = time.time()
                    progress.in_flight += 1
                    progress.peak_in_flight = max(
                        progress.peak_in_flight, progress.in_flight
                    )
                    try:
                        result = await _send_request(
                            session, config.url, config.key, config.model,
                            messages, config.max_tokens, config.stream, req_id,
                            timeout_s=config.request_timeout_s,
                            record=True,
                        )
                    finally:
                        progress.in_flight -= 1

                    result.queue_ms = (started_at - ready_at) * 1000
                    result.started_at_s = started_at - progress.start_time
                    result.completed_at_s = time.time() - progress.start_time
                    progress.done += 1
                    progress.results.append(result)
                    update_time = time.monotonic()
                    if on_progress and (
                        progress.done == progress.total
                        or update_time - last_progress_update >= 0.2
                    ):
                        last_progress_update = update_time
                        on_progress(progress)
                    return result

            # All delays are relative to a fixed reference time
            t0 = time.time()
            interval = config.duration_s / config.total_requests
            tasks = []
            for i in range(config.total_requests):
                delay = i * interval
                if config.random:
                    delay += random.uniform(0, interval * 0.3)
                tasks.append(
                    asyncio.create_task(_fire_at(t0, delay, _limited_send, i))
                )

            await asyncio.gather(*tasks)
    finally:
        progress.end_time = time.time()
        progress.running = False


async def _fire_at(t0: float, delay_s: float, coro_func, *args):
    """Sleep until absolute time t0 + delay_s, then run coro_func."""
    target = t0 + delay_s
    wait = target - time.time()
    if wait > 0:
        await asyncio.sleep(wait)
    return await coro_func(*args)
