"""SQLite persistence for compatibility and Seedance engine runs."""

from __future__ import annotations

import json
import os
import sqlite3
from collections.abc import Iterable, Mapping
from datetime import datetime
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit, urlunsplit

from .storage import DB_PATH, init_db


PROJECT_DIR = Path(__file__).resolve().parent.parent
_FORBIDDEN_SECRET_KEYS = frozenset({"apikey", "authorization", "xapikey"})


def _connect(db_path: str | os.PathLike[str]) -> sqlite3.Connection:
    connection = sqlite3.connect(str(db_path))
    connection.row_factory = sqlite3.Row
    connection.execute("PRAGMA foreign_keys = ON")
    return connection


def _init_audit_db(db_path: str | os.PathLike[str]) -> None:
    """Create audit tables alongside the existing performance-run tables."""

    init_db(db_path)
    connection = _connect(db_path)
    try:
        connection.execute("""
            CREATE TABLE IF NOT EXISTS audit_runs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                ts TEXT NOT NULL,
                suite TEXT NOT NULL,
                base_url TEXT NOT NULL,
                model TEXT NOT NULL,
                total INTEGER NOT NULL,
                overall TEXT NOT NULL,
                verdict TEXT NOT NULL,
                summary_json TEXT NOT NULL,
                config_json TEXT NOT NULL,
                report_dir TEXT NOT NULL,
                report_json TEXT NOT NULL,
                report_html TEXT NOT NULL
            )
        """)
        connection.execute("""
            CREATE TABLE IF NOT EXISTS audit_case_results (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                audit_run_id INTEGER NOT NULL,
                sequence INTEGER NOT NULL,
                case_id TEXT NOT NULL,
                name TEXT NOT NULL,
                dimension TEXT NOT NULL,
                protocol TEXT NOT NULL,
                model TEXT NOT NULL,
                status TEXT NOT NULL,
                severity TEXT NOT NULL,
                elapsed_ms INTEGER NOT NULL,
                evidence TEXT NOT NULL,
                http_status INTEGER NOT NULL,
                metrics_json TEXT NOT NULL,
                result_json TEXT NOT NULL,
                artifact_dir TEXT NOT NULL,
                FOREIGN KEY(audit_run_id) REFERENCES audit_runs(id) ON DELETE CASCADE,
                UNIQUE(audit_run_id, sequence)
            )
        """)
        connection.execute(
            "CREATE INDEX IF NOT EXISTS idx_audit_runs_ts ON audit_runs(ts DESC)"
        )
        connection.execute(
            "CREATE INDEX IF NOT EXISTS idx_audit_case_results_run_id "
            "ON audit_case_results(audit_run_id)"
        )
        connection.commit()
    finally:
        connection.close()


def _normalized_key(key: object) -> str:
    return str(key).lower().replace("-", "").replace("_", "")


def _reject_secret_fields(value: object, path: str = "events") -> None:
    if isinstance(value, Mapping):
        for key, item in value.items():
            current_path = f"{path}.{key}"
            normalized_key = _normalized_key(key)
            if (
                normalized_key in _FORBIDDEN_SECRET_KEYS
                or normalized_key.endswith("apikey")
            ):
                raise ValueError(f"API key fields cannot be persisted: {current_path}")
            _reject_secret_fields(item, current_path)
    elif isinstance(value, (list, tuple)):
        for index, item in enumerate(value):
            _reject_secret_fields(item, f"{path}[{index}]")


def _safe_base_url(base_url: str) -> str:
    parsed = urlsplit(base_url)
    netloc = parsed.netloc.rsplit("@", 1)[-1]
    return urlunsplit((parsed.scheme, netloc, parsed.path, "", ""))


def _relative_report_path(value: object) -> str:
    if not value:
        return ""
    path = Path(str(value))
    if not path.is_absolute():
        return path.as_posix()
    return Path(os.path.relpath(path, PROJECT_DIR)).as_posix()


def _json_object(value: object, field: str) -> dict[str, Any]:
    if value is None:
        return {}
    if not isinstance(value, Mapping):
        raise ValueError(f"{field} must be a JSON object")
    return dict(value)


def _event_by_type(events: list[dict[str, Any]], event_type: str) -> dict[str, Any]:
    matches = [event for event in events if event.get("type") == event_type]
    if not matches:
        raise ValueError(f"engine events must contain a {event_type} event")
    if event_type in {"plan", "final"} and len(matches) != 1:
        raise ValueError(f"engine events must contain exactly one {event_type} event")
    return matches[-1]


def save_audit_run(
    suite: str,
    base_url: str,
    model: str,
    events: Iterable[Mapping[str, Any]],
    db_path: str | os.PathLike[str] = DB_PATH,
) -> int:
    """Persist one completed EngineClient event stream and return its run ID."""

    event_list: list[dict[str, Any]] = []
    for index, event in enumerate(events):
        if not isinstance(event, Mapping):
            raise TypeError(f"events[{index}] must be a mapping")
        event_list.append(dict(event))

    _reject_secret_fields(event_list)
    plan = _event_by_type(event_list, "plan")
    final = _event_by_type(event_list, "final")
    progress_events = [event for event in event_list if event.get("type") == "progress"]
    if final.get("command") not in {None, "run"}:
        raise ValueError("final event is not a run result")

    results = []
    for index, event in enumerate(progress_events):
        result = event.get("result")
        if not isinstance(result, Mapping):
            raise ValueError(f"progress event {index} has no result object")
        results.append(dict(result))

    summary = _json_object(final.get("summary"), "final.summary")
    safe_base_url = _safe_base_url(base_url)
    config_snapshot = {
        "suite": suite,
        "base_url": safe_base_url,
        "model": model,
        "plan": plan,
    }
    total = plan.get("total", len(results))
    if isinstance(total, bool) or not isinstance(total, int) or total < 0:
        raise ValueError("plan.total must be a non-negative integer")

    report_dir = _relative_report_path(final.get("report_dir"))
    report_json = _relative_report_path(final.get("report_json"))
    report_html = _relative_report_path(final.get("report_html"))

    _init_audit_db(db_path)
    connection = _connect(db_path)
    try:
        cursor = connection.execute(
            """INSERT INTO audit_runs (
                   ts, suite, base_url, model, total, overall, verdict,
                   summary_json, config_json, report_dir, report_json, report_html
               ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                datetime.now().isoformat(),
                suite,
                safe_base_url,
                model,
                total,
                str(final.get("overall") or ""),
                str(final.get("verdict") or ""),
                json.dumps(summary, ensure_ascii=False),
                json.dumps(config_snapshot, ensure_ascii=False),
                report_dir,
                report_json,
                report_html,
            ),
        )
        audit_run_id = int(cursor.lastrowid)
        rows = []
        for sequence, result in enumerate(results):
            metrics = _json_object(result.get("metrics"), "result.metrics")
            stored_result = result.copy()
            if stored_result.get("artifact_dir"):
                stored_result["artifact_dir"] = _relative_report_path(
                    stored_result["artifact_dir"]
                )
            rows.append(
                (
                    audit_run_id,
                    sequence,
                    str(result.get("id") or ""),
                    str(result.get("name") or ""),
                    str(result.get("dimension") or ""),
                    str(result.get("protocol") or ""),
                    str(result.get("model") or model),
                    str(result.get("status") or ""),
                    str(result.get("severity") or ""),
                    int(result.get("elapsed_ms") or 0),
                    str(result.get("evidence") or ""),
                    int(result.get("http_status") or 0),
                    json.dumps(metrics, ensure_ascii=False),
                    json.dumps(stored_result, ensure_ascii=False),
                    _relative_report_path(stored_result.get("artifact_dir")),
                )
            )
        connection.executemany(
            """INSERT INTO audit_case_results (
                   audit_run_id, sequence, case_id, name, dimension, protocol,
                   model, status, severity, elapsed_ms, evidence, http_status,
                   metrics_json, result_json, artifact_dir
               ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            rows,
        )
        connection.commit()
    finally:
        connection.close()
    return audit_run_id


def _decode_json_object(value: object) -> dict[str, Any]:
    if not value:
        return {}
    try:
        decoded = json.loads(str(value))
    except (TypeError, json.JSONDecodeError):
        return {}
    return decoded if isinstance(decoded, dict) else {}


def _decode_run(row: sqlite3.Row) -> dict[str, Any]:
    run = dict(row)
    run["summary"] = _decode_json_object(run.pop("summary_json", ""))
    run["config"] = _decode_json_object(run.pop("config_json", ""))
    return run


def load_audit_history(
    limit: int = 50,
    db_path: str | os.PathLike[str] = DB_PATH,
) -> list[dict[str, Any]]:
    """Return newest audit-run summaries, without per-case result payloads."""

    if isinstance(limit, bool) or not isinstance(limit, int):
        raise TypeError("limit must be an integer")
    if limit < 1:
        raise ValueError("limit must be at least 1")
    _init_audit_db(db_path)
    connection = _connect(db_path)
    try:
        rows = connection.execute(
            "SELECT * FROM audit_runs ORDER BY id DESC LIMIT ?", (limit,)
        ).fetchall()
    finally:
        connection.close()
    return [_decode_run(row) for row in rows]


def count_audit_runs(db_path: str | os.PathLike[str] = DB_PATH) -> int:
    """Return the total number of stored compatibility and Seedance audits."""
    _init_audit_db(db_path)
    connection = _connect(db_path)
    try:
        return int(connection.execute("SELECT COUNT(*) FROM audit_runs").fetchone()[0])
    finally:
        connection.close()


def load_audit_run(
    audit_run_id: int,
    db_path: str | os.PathLike[str] = DB_PATH,
) -> dict[str, Any] | None:
    """Load one audit summary with its ordered, original case-result objects."""

    _init_audit_db(db_path)
    connection = _connect(db_path)
    try:
        run_row = connection.execute(
            "SELECT * FROM audit_runs WHERE id = ?", (audit_run_id,)
        ).fetchone()
        if run_row is None:
            return None
        result_rows = connection.execute(
            """SELECT result_json FROM audit_case_results
               WHERE audit_run_id = ? ORDER BY sequence""",
            (audit_run_id,),
        ).fetchall()
    finally:
        connection.close()

    run = _decode_run(run_row)
    run["results"] = [
        _decode_json_object(row["result_json"]) for row in result_rows
    ]
    return run


__all__ = [
    "count_audit_runs",
    "load_audit_history",
    "load_audit_run",
    "save_audit_run",
]
