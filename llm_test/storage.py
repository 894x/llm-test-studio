"""SQLite persistence for load-test summaries and per-request results."""

import json
import sqlite3
from datetime import datetime
from pathlib import Path

import pandas as pd

from loadtest import LoadTestConfig, LoadTestProgress, RequestResult

from .metrics import compute_metrics, percentile


PROJECT_DIR = Path(__file__).resolve().parent.parent
DB_PATH = PROJECT_DIR / "loadtest_history.db"

DEFAULT_CAPTURE_POLICY = "errors_and_sample"
DEFAULT_CAPTURE_SAMPLE_LIMIT = 10
CAPTURE_POLICIES = frozenset({"all", "errors_and_sample", "errors_only", "none"})
REDACTED = "[REDACTED]"


def _connect(db_path):
    return sqlite3.connect(str(db_path))


def _redact_api_key(value, api_key):
    """Remove credentials from any value that is about to be persisted."""
    if isinstance(value, dict):
        redacted = {}
        for key, item in value.items():
            normalized_key = str(key).lower().replace("-", "").replace("_", "")
            if normalized_key in {"apikey", "authorization", "xapikey"}:
                redacted[key] = REDACTED
            else:
                redacted[key] = _redact_api_key(item, api_key)
        return redacted
    if isinstance(value, list):
        return [_redact_api_key(item, api_key) for item in value]
    if isinstance(value, tuple):
        return tuple(_redact_api_key(item, api_key) for item in value)
    if isinstance(value, str) and api_key:
        return value.replace(api_key, REDACTED)
    return value


def init_db(db_path=DB_PATH):
    conn = _connect(db_path)
    try:
        conn.execute("""
            CREATE TABLE IF NOT EXISTS runs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                ts TEXT, url TEXT, model TEXT, requests INTEGER,
                duration INTEGER, max_tokens INTEGER, input_tokens INTEGER,
                stream INTEGER, random INTEGER,
                success INTEGER, total INTEGER, ttft_p50 REAL, ttft_p90 REAL,
                tpot_p50 REAL, tpot_p90 REAL, e2e_p50 REAL, e2e_p90 REAL,
                cached INTEGER, prompt_total INTEGER, config TEXT
            )
        """)
        existing_columns = {
            row[1] for row in conn.execute("PRAGMA table_info(runs)").fetchall()
        }
        added_columns = {
            "concurrency": "INTEGER",
            "timeout": "INTEGER",
            "failures": "INTEGER",
            "elapsed": "REAL",
            "qps": "REAL",
            "total_tpm": "REAL",
            "peak_in_flight": "INTEGER",
            "request_template": "TEXT",
            "capture_policy": "TEXT",
            "capture_sample_limit": "INTEGER",
        }
        for column, column_type in added_columns.items():
            if column not in existing_columns:
                conn.execute(f"ALTER TABLE runs ADD COLUMN {column} {column_type}")
        conn.execute("""
            CREATE TABLE IF NOT EXISTS run_results (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                run_id INTEGER NOT NULL,
                request_id INTEGER NOT NULL,
                status INTEGER,
                e2e_ms REAL,
                ttft_ms REAL,
                tpot_ms REAL,
                queue_ms REAL,
                started_at_s REAL,
                completed_at_s REAL,
                prompt_tokens INTEGER,
                completion_tokens INTEGER,
                cached_tokens INTEGER,
                chunks INTEGER,
                error TEXT,
                request_body TEXT,
                response_body TEXT,
                response_text TEXT,
                FOREIGN KEY(run_id) REFERENCES runs(id)
            )
        """)
        conn.execute(
            "CREATE INDEX IF NOT EXISTS idx_run_results_run_id ON run_results(run_id)"
        )
        conn.commit()
    finally:
        conn.close()


def _captured_request_ids(results, capture_policy, capture_sample_limit):
    if capture_policy == "all":
        return {result.id for result in results}
    captured = set()
    if capture_policy == "errors_and_sample":
        captured.update(
            result.id
            for result in sorted(results, key=lambda result: result.id)[
                :capture_sample_limit
            ]
        )
    if capture_policy in {"errors_and_sample", "errors_only"}:
        captured.update(result.id for result in results if result.status != 200)
    return captured


def save_run(
    cfg: LoadTestConfig,
    p: LoadTestProgress,
    db_path=DB_PATH,
    capture_policy=DEFAULT_CAPTURE_POLICY,
    capture_sample_limit=DEFAULT_CAPTURE_SAMPLE_LIMIT,
):
    if capture_policy not in CAPTURE_POLICIES:
        choices = ", ".join(sorted(CAPTURE_POLICIES))
        raise ValueError(f"capture_policy must be one of: {choices}")
    if isinstance(capture_sample_limit, bool) or not isinstance(
        capture_sample_limit, int
    ):
        raise TypeError("capture_sample_limit must be an integer")
    if capture_sample_limit < 0:
        raise ValueError("capture_sample_limit must be at least 0")

    init_db(db_path)
    successful_results = [r for r in p.results if r.status == 200]
    metrics = compute_metrics(p)
    api_key = cfg.key
    request_template = _redact_api_key(
        p.results[0].request_body if p.results else {}, api_key
    )
    ttfts = sorted([r.ttft_ms for r in successful_results if r.ttft_ms > 0])
    tpots = sorted([r.tpot_ms for r in successful_results if r.tpot_ms > 0])
    e2es = sorted([r.e2e_ms for r in successful_results])
    prompt_total = sum(r.prompt_tokens for r in successful_results)
    cached_total = sum(r.cached_tokens for r in successful_results)
    captured_request_ids = _captured_request_ids(
        p.results, capture_policy, capture_sample_limit
    )

    conn = _connect(db_path)
    try:
        cursor = conn.execute(
            """INSERT INTO runs (ts, url, model, requests, duration, max_tokens,
               input_tokens, stream, random, success, total, ttft_p50, ttft_p90,
               tpot_p50, tpot_p90, e2e_p50, e2e_p90, cached, prompt_total, config,
               concurrency, timeout, failures, elapsed, qps, total_tpm,
               peak_in_flight, request_template, capture_policy,
               capture_sample_limit)
               VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            (
                datetime.now().isoformat(),
                _redact_api_key(cfg.url, api_key),
                _redact_api_key(cfg.model, api_key),
                cfg.total_requests,
                cfg.duration_s,
                cfg.max_tokens,
                cfg.input_tokens,
                int(cfg.stream),
                int(cfg.random),
                p.success_count,
                len(p.results),
                percentile(ttfts, 0.5),
                percentile(ttfts, 0.9),
                percentile(tpots, 0.5),
                percentile(tpots, 0.9),
                percentile(e2es, 0.5),
                percentile(e2es, 0.9),
                cached_total,
                prompt_total,
                json.dumps(
                    _redact_api_key(
                        {
                            "url": cfg.url,
                            "max_connections": cfg.max_connections,
                            "request_timeout_s": cfg.request_timeout_s,
                            "include_image": bool(cfg.image_data_url),
                            "image_prompt": (
                                cfg.image_prompt if cfg.image_data_url else ""
                            ),
                            "include_video": bool(cfg.video_data_url),
                            "video_prompt": (
                                cfg.video_prompt if cfg.video_data_url else ""
                            ),
                        },
                        api_key,
                    ),
                    ensure_ascii=False,
                ),
                cfg.max_connections,
                cfg.request_timeout_s,
                metrics["failures"],
                metrics["elapsed"],
                metrics["request_qps"],
                metrics["total_tpm"],
                metrics["peak_in_flight"],
                json.dumps(request_template, ensure_ascii=False),
                capture_policy,
                capture_sample_limit,
            ),
        )
        run_id = cursor.lastrowid
        result_rows = []
        for result in p.results:
            request_body = _redact_api_key(result.request_body, api_key)
            request_delta = {
                key: value
                for key, value in request_body.items()
                if key not in request_template or request_template[key] != value
            }
            capture_response = result.id in captured_request_ids
            response_body = (
                _redact_api_key(result.response_body, api_key)
                if capture_response
                else {}
            )
            response_text = (
                _redact_api_key(result.response_text, api_key)
                if capture_response
                else ""
            )
            result_rows.append(
                (
                    run_id,
                    result.id,
                    result.status,
                    result.e2e_ms,
                    result.ttft_ms,
                    result.tpot_ms,
                    result.queue_ms,
                    result.started_at_s,
                    result.completed_at_s,
                    result.prompt_tokens,
                    result.completion_tokens,
                    result.cached_tokens,
                    result.chunks,
                    _redact_api_key(result.error, api_key),
                    json.dumps(request_delta, ensure_ascii=False),
                    json.dumps(response_body, ensure_ascii=False),
                    response_text,
                )
            )
        conn.executemany(
            """INSERT INTO run_results (
                   run_id, request_id, status, e2e_ms, ttft_ms, tpot_ms, queue_ms,
                   started_at_s, completed_at_s, prompt_tokens, completion_tokens,
                   cached_tokens, chunks, error, request_body, response_body,
                   response_text
               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            result_rows,
        )
        conn.commit()
    finally:
        conn.close()
    return run_id


def load_history(db_path=DB_PATH):
    conn = _connect(db_path)
    try:
        return pd.read_sql("SELECT * FROM runs ORDER BY id DESC LIMIT 50", conn)
    finally:
        conn.close()


def count_runs(db_path=DB_PATH):
    """Return the total number of stored performance runs."""
    init_db(db_path)
    conn = _connect(db_path)
    try:
        return int(conn.execute("SELECT COUNT(*) FROM runs").fetchone()[0])
    finally:
        conn.close()


def decode_json(value):
    if not value:
        return {}
    try:
        return json.loads(value)
    except (TypeError, json.JSONDecodeError):
        return {}


def load_saved_run(run_id, db_path=DB_PATH):
    conn = _connect(db_path)
    conn.row_factory = sqlite3.Row
    try:
        run_row = conn.execute(
            "SELECT * FROM runs WHERE id = ?", (run_id,)
        ).fetchone()
        result_rows = conn.execute(
            "SELECT * FROM run_results WHERE run_id = ? ORDER BY request_id",
            (run_id,),
        ).fetchall()
    finally:
        conn.close()

    if run_row is None:
        return None, None, None

    summary = dict(run_row)
    request_template = decode_json(summary.get("request_template"))
    results = []
    export_results = []
    for row in result_rows:
        row_data = dict(row)
        request_body = request_template.copy()
        request_body.update(decode_json(row_data["request_body"]))
        response_body = decode_json(row_data["response_body"])
        result = RequestResult(
            id=row_data["request_id"],
            status=row_data["status"],
            e2e_ms=row_data["e2e_ms"],
            ttft_ms=row_data["ttft_ms"],
            tpot_ms=row_data["tpot_ms"],
            prompt_tokens=row_data["prompt_tokens"],
            completion_tokens=row_data["completion_tokens"],
            cached_tokens=row_data["cached_tokens"],
            error=row_data["error"] or "",
            chunks=row_data["chunks"],
            request_body=request_body,
            response_body=response_body,
            response_text=row_data["response_text"] or "",
            queue_ms=row_data["queue_ms"],
            started_at_s=row_data["started_at_s"],
            completed_at_s=row_data["completed_at_s"],
        )
        results.append(result)
        export_results.append(
            {
                "request_id": result.id,
                "status": result.status,
                "e2e_ms": result.e2e_ms,
                "ttft_ms": result.ttft_ms,
                "tpot_ms": result.tpot_ms,
                "queue_ms": result.queue_ms,
                "started_at_s": result.started_at_s,
                "completed_at_s": result.completed_at_s,
                "prompt_tokens": result.prompt_tokens,
                "completion_tokens": result.completion_tokens,
                "cached_tokens": result.cached_tokens,
                "chunks": result.chunks,
                "error": result.error,
                "request_body": result.request_body,
                "response_body": result.response_body,
                "response_text": result.response_text,
            }
        )

    elapsed = summary.get("elapsed") or 0
    progress = LoadTestProgress(
        done=len(results),
        total=summary.get("total") or summary.get("requests") or len(results),
        results=results,
        running=False,
        start_time=1,
        end_time=1 + elapsed,
        in_flight=0,
        peak_in_flight=summary.get("peak_in_flight") or 0,
    )
    export_data = {
        "run": {
            **summary,
            "config": decode_json(summary.get("config")),
            "request_template": request_template,
        },
        "results": export_results,
    }
    return summary, progress, export_data


__all__ = [
    "CAPTURE_POLICIES",
    "DB_PATH",
    "DEFAULT_CAPTURE_POLICY",
    "DEFAULT_CAPTURE_SAMPLE_LIMIT",
    "decode_json",
    "init_db",
    "load_history",
    "load_saved_run",
    "save_run",
]
