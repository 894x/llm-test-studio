"""Versioned, bounded Inspect adapter. Only JSON protocol events go to stdout."""

import asyncio
import contextlib
import hashlib
import importlib
import importlib.metadata
import json
import math
from pathlib import Path
import sys

PROTOCOL = sys.stdout
MAX_TEXT = 12000
REQUEST_LIMIT = 65536
EXPECTED_VERSIONS = {"inspect-ai": "0.3.287", "inspect-evals": "0.27.0"}


def versions():
    found = {name: importlib.metadata.version(name) for name in EXPECTED_VERSIONS}
    if found != EXPECTED_VERSIONS:
        raise ValueError("runtime_version")
    return found


class Reporter:
    def __init__(self, request):
        self.request = request
        self.sequence = 0
        self.secret = request.get("api_key", "")

    def text(self, value, limit=MAX_TEXT):
        value = str(value or "")
        if self.secret:
            value = value.replace(self.secret, "[redacted]")
        return "".join(c for c in value if c in "\n\t" or ord(c) >= 32)[:limit]

    def emit(self, kind, **data):
        self.sequence += 1
        event = dict(schema_version=1, run_id=self.request["run_id"],
                     report_id=self.request["report_id"], sequence=self.sequence,
                     kind=kind, **data)
        PROTOCOL.write(json.dumps(event, ensure_ascii=True, allow_nan=False) + "\n")
        PROTOCOL.flush()


def text_input(value):
    if isinstance(value, str):
        return value
    parts = []
    for message in value or []:
        content = getattr(message, "content", "")
        if isinstance(content, str):
            parts.append(content)
        else:
            parts.extend(getattr(item, "text", "[image]") for item in content)
    return "\n".join(parts)


def sample_projection(sample, reporter):
    scores = []
    for name, score in list((sample.scores or {}).items())[:32]:
        value = score.value
        if isinstance(value, (dict, list)):
            value = json.dumps(value, ensure_ascii=True, allow_nan=False)
        scores.append(dict(name=reporter.text(name, 256), value=reporter.text(value, 1024),
                           answer=reporter.text(score.answer),
                           explanation=reporter.text(score.explanation)))
    usage = list((sample.model_usage or {}).values())
    return dict(id=reporter.text(sample.id, 256), epoch=sample.epoch,
                status="error" if sample.error else "completed", scores=scores,
                input=reporter.text(text_input(sample.input)),
                target=reporter.text(sample.target if isinstance(sample.target, str)
                                     else "\n".join(sample.target)),
                output=reporter.text(sample.output.completion if sample.output else ""),
                input_tokens=sum(item.input_tokens for item in usage),
                output_tokens=sum(item.output_tokens for item in usage),
                duration_ms=round(max(0, sample.total_time or 0) * 1000),
                error_code="sample_failed" if sample.error else "")


def metrics_projection(log, reporter):
    metrics = []
    for scorer in (log.results.scores if log.results else []) or []:
        for name, metric in scorer.metrics.items():
            value = metric.value
            if isinstance(value, (int, float)) and math.isfinite(value):
                metrics.append(dict(scorer=reporter.text(scorer.name, 256),
                                    name=reporter.text(name, 256), value=float(value)))
    return metrics[:128]


def load_task(dataset):
    manifest = json.loads(Path(__file__).with_name("datasets.json").read_text("utf-8"))
    if manifest["schema_version"] != 1:
        raise ValueError("manifest_version")
    item = next(item for item in manifest["datasets"] if item["id"] == dataset)
    return getattr(importlib.import_module(item["module"]), item["factory"])()


async def execute(request, task_factory=None, reporter=None):
    from inspect_ai import eval_async
    from inspect_ai.hooks import Hooks, hooks
    from inspect_ai.model import GenerateConfig, get_model

    reporter = reporter or Reporter(request)
    package_versions = versions()
    reporter.emit("loading")
    task = (task_factory or load_task)(request["dataset_id"])
    options = request["options"]
    selected = list(task.dataset)
    limit = options["sample_limit"]
    if limit:
        selected = selected[:limit]
    digest = hashlib.sha256()
    for sample in selected:
        digest.update(sample.model_dump_json(exclude_none=True).encode("utf-8"))
        digest.update(b"\n")
    total = len(selected) * options["epochs"]
    reporter.emit("dataset", total=total)
    active = set()
    finished = set()
    failed = 0

    @hooks(name="llm_test_progress", description="LLM Test Studio progress")
    class Progress(Hooks):
        def needs_full_sample(self):
            return False

        async def on_sample_start(self, data):
            active.add(data.sample_id)
            reporter.emit("progress", total=total, completed=len(finished),
                          failed=failed, in_flight=len(active))

        async def on_sample_end(self, data):
            nonlocal failed
            if data.sample_id in finished:
                return
            active.discard(data.sample_id)
            finished.add(data.sample_id)
            failed += int(data.sample.error is not None)
            reporter.emit("sample", sample=sample_projection(data.sample, reporter))
            reporter.emit("progress", total=total, completed=len(finished),
                          failed=failed, in_flight=len(active))

    protocol = request["protocol"]
    provider = "anthropic" if protocol == "anthropic-messages" else "openai-api/benchmark"
    model_args = {} if provider == "anthropic" else {
        "responses_api": protocol == "openai-responses", "stream": options["stream"]}
    config = GenerateConfig(max_tokens=options["max_tokens"],
                            temperature=options["temperature"],
                            max_connections=options["concurrency"],
                            timeout=options["timeout_seconds"], max_retries=2,
                            seed=options["seed"])
    if options["reasoning_effort"]:
        config.reasoning_effort = options["reasoning_effort"]
    model = get_model(f"{provider}/{request['model']}", api_key=request["api_key"],
                      base_url=request["base_url"], config=config, **model_args)
    logs = await eval_async(task, model=model, log_dir=request["log_dir"],
                            limit=limit or None, epochs=options["epochs"],
                            max_connections=options["concurrency"],
                            max_samples=options["concurrency"],
                            fail_on_error=False, retry_on_error=0,
                            log_images=False, log_realtime=False, log_model_api=False,
                            ctl_server=False, log_buffer=1)
    if len(logs) != 1:
        raise ValueError("unexpected_task_count")
    log = logs[0]
    # Re-read samples with the public API; hooks can be interrupted or fail.
    from inspect_ai.log import read_eval_log
    final = read_eval_log(log.location) if log.location else log
    samples = final.samples or []
    for sample in samples:
        reporter.emit("sample", sample=sample_projection(sample, reporter))
    spec = final.eval
    provenance = dict(inspect_ai=package_versions["inspect-ai"],
                      inspect_evals=package_versions["inspect-evals"],
                      task=reporter.text(spec.task, 256),
                      task_version=reporter.text(spec.task_version, 256),
                      dataset_name=reporter.text(spec.dataset.name, 512),
                      dataset_location=reporter.text(spec.dataset.location, 2048),
                      input_sha256=digest.hexdigest())
    failed_samples = sum(sample.error is not None for sample in samples)
    status = "completed" if final.status == "success" and not failed_samples else "cancelled" if final.status == "cancelled" else "failed"
    reporter.emit("result", status=status, total=total, completed=len(samples),
                  failed=failed_samples,
                  metrics=metrics_projection(final, reporter), provenance=provenance)


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "check":
        with contextlib.redirect_stdout(sys.stderr):
            found = versions()
            import inspect_ai  # noqa: F401
            import inspect_evals  # noqa: F401
        PROTOCOL.write(json.dumps(dict(schema_version=1, versions=found)) + "\n")
        return
    request = json.loads(sys.stdin.readline(REQUEST_LIMIT + 1))
    if request.get("schema_version") != 1:
        raise ValueError("protocol_version")
    # Dependency loading can print diagnostics; they are never protocol events.
    with contextlib.redirect_stdout(sys.stderr):
        reporter = Reporter(request)
        try:
            asyncio.run(execute(request, reporter=reporter))
        except BaseException:
            reporter.emit("error", code="evaluation_failed")
            raise SystemExit(1) from None


if __name__ == "__main__":
    main()
