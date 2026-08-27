"""Subprocess boundary for the standalone Go compatibility engine.

Credentials are injected only into the child environment.  The client never
places an API key in argv and does not write it to project storage.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
from typing import Any, Callable, Iterable, Iterator, Mapping, Sequence


ENGINE_API_KEY_ENV = "LLM_TEST_ENGINE_API_KEY"


class EngineError(RuntimeError):
    """Raised when the engine cannot build or emit a valid result stream."""


class EngineClient:
    """Build, inspect, and run the bundled compatibility engine."""

    def __init__(
        self,
        root: str | os.PathLike[str] | None = None,
        *,
        binary_path: str | os.PathLike[str] | None = None,
        go_executable: str | os.PathLike[str] | None = None,
    ) -> None:
        self.root = (
            Path(root) if root is not None else Path(__file__).resolve().parents[1]
        ).resolve()
        self.engine_dir = self.root / "engine"
        self._managed_binary = binary_path is None
        self.binary_path = (
            Path(binary_path)
            if binary_path is not None
            else self.engine_dir / "bin" / "llm-compat-engine"
        ).resolve()
        self.go_executable = (
            Path(go_executable).resolve() if go_executable is not None else None
        )

    def build(self, *, force: bool = False) -> Path:
        """Build the Go engine if its managed binary is absent or stale."""

        if not self._managed_binary:
            if not self.binary_path.is_file():
                raise EngineError(f"engine binary does not exist: {self.binary_path}")
            return self.binary_path
        if not self.engine_dir.joinpath("go.mod").is_file():
            raise EngineError(f"engine module is missing: {self.engine_dir}")
        if not force and self._binary_is_current():
            return self.binary_path

        go = self._resolve_go()
        self.binary_path.parent.mkdir(parents=True, exist_ok=True)
        environment = os.environ.copy()
        environment.pop(ENGINE_API_KEY_ENV, None)
        environment["GOWORK"] = "off"
        completed = subprocess.run(
            [str(go), "build", "-o", str(self.binary_path), "./cmd/llm-compat-engine"],
            cwd=self.engine_dir,
            env=environment,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
        if completed.returncode != 0:
            message = completed.stderr.strip() or completed.stdout.strip()
            raise EngineError(f"engine build failed: {message}")
        return self.binary_path

    def list_cases(
        self,
        suite: str,
        *,
        cases_root: str | os.PathLike[str] | None = None,
    ) -> list[dict[str, Any]]:
        """Return case definitions accepted by the Go engine for ``suite``."""

        root = Path(cases_root) if cases_root is not None else self.root / "cases"
        events = self._collect_events(
            ["list", "--suite", suite, "--cases-root", str(root), "--jsonl"]
        )
        return [event["case"] for event in events if event.get("type") == "case"]

    def list(
        self,
        suite: str,
        *,
        cases_root: str | os.PathLike[str] | None = None,
    ) -> list[dict[str, Any]]:
        """Short alias for :meth:`list_cases`."""

        return self.list_cases(suite, cases_root=cases_root)

    def stream_run(
        self,
        *,
        suite: str,
        base_url: str,
        model: str = "",
        api_key: str | None = None,
        cases_root: str | os.PathLike[str] | None = None,
        case_ids: Iterable[str] = (),
        all_cases: bool = False,
        all_models: bool = False,
        dry_run: bool = False,
        confirm_paid_suite: bool = False,
        no_wait: bool = False,
        output: str | os.PathLike[str] | None = None,
        poll_interval: str | int | float | None = None,
        timeout: str | int | float | None = None,
        concurrency: int = 1,
    ) -> Iterator[dict[str, Any]]:
        """Yield ``plan``, ``progress``, and ``final`` JSONL events.

        A compatibility verdict may use process exit code 1 and still has a
        valid final event. Configuration/protocol failures raise EngineError.
        """

        root = Path(cases_root) if cases_root is not None else self.root / "cases"
        arguments = [
            "run",
            "--suite",
            suite,
            "--cases-root",
            str(root),
            "--base-url",
            base_url,
            "--concurrency",
            str(concurrency),
            "--api-key-env",
            ENGINE_API_KEY_ENV,
            "--jsonl",
        ]
        if model:
            arguments.extend(["--model", model])
        for case_id in case_ids:
            arguments.extend(["--case", str(case_id)])
        if all_cases:
            arguments.append("--all-cases")
        if all_models:
            arguments.append("--all-models")
        if dry_run:
            arguments.append("--dry-run")
        if confirm_paid_suite:
            arguments.append("--confirm-paid-suite")
        if no_wait:
            arguments.append("--no-wait")
        if output is not None:
            arguments.extend(["--output", str(output)])
        if poll_interval is not None:
            arguments.extend(["--poll-interval", _duration_argument(poll_interval)])
        if timeout is not None:
            arguments.extend(["--timeout", _duration_argument(timeout)])

        environment = os.environ.copy()
        environment.pop(ENGINE_API_KEY_ENV, None)
        if api_key is not None:
            environment[ENGINE_API_KEY_ENV] = api_key
        yield from self._stream_events(arguments, environment=environment, secret=api_key)

    def run(
        self,
        *,
        on_event: Callable[[Mapping[str, Any]], None] | None = None,
        **options: Any,
    ) -> list[dict[str, Any]]:
        """Run the engine and collect all events, optionally invoking a callback."""

        events: list[dict[str, Any]] = []
        for event in self.stream_run(**options):
            events.append(event)
            if on_event is not None:
                on_event(event)
        return events

    @staticmethod
    def final_event(events: Sequence[Mapping[str, Any]]) -> Mapping[str, Any]:
        """Return the terminal event from a completed list or run."""

        for event in reversed(events):
            if event.get("type") == "final":
                return event
        raise EngineError("engine stream did not contain a final event")

    def _collect_events(self, arguments: list[str]) -> list[dict[str, Any]]:
        environment = os.environ.copy()
        environment.pop(ENGINE_API_KEY_ENV, None)
        return list(self._stream_events(arguments, environment=environment))

    def _stream_events(
        self,
        arguments: list[str],
        *,
        environment: Mapping[str, str],
        secret: str | None = None,
    ) -> Iterator[dict[str, Any]]:
        binary = self.build()
        command = [str(binary), *arguments]
        saw_final = False
        with tempfile.TemporaryFile(mode="w+t", encoding="utf-8") as stderr_file:
            process = subprocess.Popen(
                command,
                cwd=self.root,
                env=dict(environment),
                text=True,
                stdout=subprocess.PIPE,
                stderr=stderr_file,
            )
            assert process.stdout is not None
            try:
                for line in process.stdout:
                    if not line.strip():
                        continue
                    try:
                        event = json.loads(line)
                    except json.JSONDecodeError as exc:
                        process.terminate()
                        raise EngineError(f"invalid engine JSONL event: {exc}") from exc
                    if not isinstance(event, dict) or not isinstance(event.get("type"), str):
                        process.terminate()
                        raise EngineError("invalid engine event envelope")
                    saw_final = saw_final or event["type"] == "final"
                    yield event
            finally:
                close_stdout = getattr(process.stdout, "close", None)
                if close_stdout is not None:
                    close_stdout()
                return_code = process.wait()
            stderr_file.seek(0)
            error_text = stderr_file.read().strip()
        if return_code != 0 and not (return_code == 1 and saw_final):
            if secret:
                error_text = error_text.replace(secret, "[REDACTED]")
            raise EngineError(error_text or f"engine exited with status {return_code}")
        if not saw_final:
            raise EngineError("engine exited without a final event")

    def _binary_is_current(self) -> bool:
        if not self.binary_path.is_file():
            return False
        binary_time = self.binary_path.stat().st_mtime_ns
        source_paths = [self.engine_dir / "go.mod", *self.engine_dir.rglob("*.go")]
        return all(path.stat().st_mtime_ns <= binary_time for path in source_paths)

    def _resolve_go(self) -> Path:
        if self.go_executable is not None:
            if not self.go_executable.is_file():
                raise EngineError(f"Go executable does not exist: {self.go_executable}")
            return self.go_executable
        discovered = shutil.which("go")
        if discovered:
            return Path(discovered)
        for candidate in (Path("/opt/homebrew/bin/go"), Path("/usr/local/go/bin/go")):
            if candidate.is_file():
                return candidate
        raise EngineError("Go executable was not found")


def _duration_argument(value: str | int | float) -> str:
    if isinstance(value, (int, float)):
        return f"{value}s"
    return str(value)
