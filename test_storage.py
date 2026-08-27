import json
import sqlite3
import tempfile
import unittest
from pathlib import Path

from loadtest import LoadTestConfig, LoadTestProgress, RequestResult
from llm_test.storage import (
    DEFAULT_CAPTURE_POLICY,
    count_runs,
    decode_json,
    init_db,
    load_history,
    load_saved_run,
    save_run,
)


class StorageTest(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.temp_dir.name) / "history.db"
        self.secret = "sk-never-persist-this"
        self.config = LoadTestConfig(
            url=f"https://example.test/v1/chat/completions?key={self.secret}",
            key=self.secret,
            model="test-model",
            total_requests=3,
            duration_s=1,
            max_tokens=32,
            input_tokens=10,
            max_connections=3,
            request_timeout_s=15,
        )

    def tearDown(self):
        self.temp_dir.cleanup()

    def make_progress(self):
        template = {
            "model": "test-model",
            "messages": [{"role": "user", "content": "hello"}],
            "max_tokens": 32,
            "authorization": f"Bearer {self.secret}",
        }
        return LoadTestProgress(
            done=3,
            total=3,
            results=[
                RequestResult(
                    id=0,
                    status=200,
                    e2e_ms=100,
                    ttft_ms=20,
                    tpot_ms=4,
                    queue_ms=1,
                    prompt_tokens=10,
                    completion_tokens=5,
                    request_body=template.copy(),
                    response_body={"content": "sample zero"},
                    response_text="raw sample zero",
                    started_at_s=0,
                    completed_at_s=0.1,
                ),
                RequestResult(
                    id=1,
                    status=200,
                    e2e_ms=120,
                    ttft_ms=30,
                    tpot_ms=5,
                    queue_ms=2,
                    prompt_tokens=11,
                    completion_tokens=6,
                    request_body={**template, "max_tokens": 64},
                    response_body={"content": "uncaptured"},
                    response_text="raw uncaptured",
                    started_at_s=0.1,
                    completed_at_s=0.22,
                ),
                RequestResult(
                    id=2,
                    status=500,
                    e2e_ms=80,
                    queue_ms=3,
                    error=f"upstream echoed {self.secret}",
                    request_body={**template, "temperature": 0},
                    response_body={"error": f"bad credential {self.secret}"},
                    response_text=f"raw failure {self.secret}",
                    started_at_s=0.2,
                    completed_at_s=0.28,
                ),
            ],
            start_time=1,
            end_time=2,
            peak_in_flight=3,
        )

    def test_init_db_migrates_legacy_runs_table(self):
        conn = sqlite3.connect(self.db_path)
        conn.execute("""
            CREATE TABLE runs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                ts TEXT, url TEXT, model TEXT, requests INTEGER,
                duration INTEGER, max_tokens INTEGER, input_tokens INTEGER,
                stream INTEGER, random INTEGER,
                success INTEGER, total INTEGER, ttft_p50 REAL, ttft_p90 REAL,
                tpot_p50 REAL, tpot_p90 REAL, e2e_p50 REAL, e2e_p90 REAL,
                cached INTEGER, prompt_total INTEGER, config TEXT
            )
        """)
        conn.execute(
            "INSERT INTO runs (ts, model, requests, success, total) "
            "VALUES ('old', 'legacy', 1, 1, 1)"
        )
        conn.commit()
        conn.close()

        init_db(self.db_path)

        conn = sqlite3.connect(self.db_path)
        columns = {
            row[1] for row in conn.execute("PRAGMA table_info(runs)").fetchall()
        }
        old_row = conn.execute("SELECT model FROM runs WHERE id = 1").fetchone()
        conn.close()
        self.assertIn("request_template", columns)
        self.assertIn("capture_policy", columns)
        self.assertIn("capture_sample_limit", columns)
        self.assertEqual(old_row[0], "legacy")

    def test_default_policy_saves_template_deltas_samples_and_errors(self):
        progress = self.make_progress()

        run_id = save_run(
            self.config,
            progress,
            db_path=self.db_path,
            capture_sample_limit=1,
        )

        conn = sqlite3.connect(self.db_path)
        conn.row_factory = sqlite3.Row
        run = conn.execute("SELECT * FROM runs WHERE id = ?", (run_id,)).fetchone()
        rows = conn.execute(
            "SELECT * FROM run_results WHERE run_id = ? ORDER BY request_id",
            (run_id,),
        ).fetchall()
        conn.close()

        request_template = json.loads(run["request_template"])
        self.assertEqual(run["capture_policy"], DEFAULT_CAPTURE_POLICY)
        self.assertEqual(run["capture_sample_limit"], 1)
        self.assertEqual(request_template["max_tokens"], 32)
        self.assertEqual(request_template["authorization"], "[REDACTED]")
        self.assertEqual(json.loads(rows[0]["request_body"]), {})
        self.assertEqual(json.loads(rows[1]["request_body"]), {"max_tokens": 64})
        self.assertEqual(
            json.loads(rows[2]["request_body"]), {"temperature": 0}
        )

        self.assertEqual(
            json.loads(rows[0]["response_body"]), {"content": "sample zero"}
        )
        self.assertEqual(rows[0]["response_text"], "raw sample zero")
        self.assertEqual(json.loads(rows[1]["response_body"]), {})
        self.assertEqual(rows[1]["response_text"], "")
        self.assertEqual(
            json.loads(rows[2]["response_body"])["error"],
            "bad credential [REDACTED]",
        )
        self.assertEqual(rows[2]["response_text"], "raw failure [REDACTED]")
        self.assertEqual(rows[2]["status"], 500)
        self.assertEqual(rows[2]["e2e_ms"], 80)

        persisted_bytes = self.db_path.read_bytes()
        self.assertNotIn(self.secret.encode(), persisted_bytes)

    def test_load_saved_run_rebuilds_template_plus_top_level_delta(self):
        run_id = save_run(
            self.config,
            self.make_progress(),
            db_path=self.db_path,
            capture_sample_limit=1,
        )

        summary, saved_progress, export_data = load_saved_run(
            run_id, db_path=self.db_path
        )

        self.assertEqual(summary["id"], run_id)
        self.assertEqual([result.id for result in saved_progress.results], [0, 1, 2])
        self.assertEqual(saved_progress.results[0].request_body["max_tokens"], 32)
        self.assertEqual(saved_progress.results[1].request_body["max_tokens"], 64)
        self.assertEqual(saved_progress.results[2].request_body["temperature"], 0)
        self.assertEqual(saved_progress.results[1].response_body, {})
        self.assertEqual(saved_progress.results[1].response_text, "")
        self.assertEqual(
            saved_progress.results[2].response_body["error"],
            "bad credential [REDACTED]",
        )
        self.assertIsInstance(export_data["run"]["config"], dict)
        self.assertEqual(
            export_data["results"][1]["request_body"]["max_tokens"], 64
        )

    def test_load_saved_run_accepts_legacy_full_request_rows(self):
        init_db(self.db_path)
        conn = sqlite3.connect(self.db_path)
        cursor = conn.execute(
            """INSERT INTO runs (
                   ts, model, requests, success, total, elapsed, request_template
               ) VALUES (?, ?, ?, ?, ?, ?, ?)""",
            ("old", "legacy", 1, 1, 1, 0.5, None),
        )
        run_id = cursor.lastrowid
        conn.execute(
            """INSERT INTO run_results (
                   run_id, request_id, status, e2e_ms, ttft_ms, tpot_ms,
                   queue_ms, started_at_s, completed_at_s, prompt_tokens,
                   completion_tokens, cached_tokens, chunks, error,
                   request_body, response_body, response_text
               ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                run_id,
                7,
                200,
                50,
                10,
                2,
                1,
                0,
                0.05,
                4,
                2,
                0,
                1,
                "",
                json.dumps({"model": "legacy", "max_tokens": 8}),
                json.dumps({"content": "ok"}),
                "",
            ),
        )
        conn.commit()
        conn.close()

        _, saved_progress, export_data = load_saved_run(
            run_id, db_path=self.db_path
        )

        self.assertEqual(
            saved_progress.results[0].request_body,
            {"model": "legacy", "max_tokens": 8},
        )
        self.assertEqual(export_data["run"]["request_template"], {})

    def test_history_and_missing_run_use_injected_database(self):
        first_id = save_run(
            self.config, self.make_progress(), db_path=self.db_path
        )
        second_id = save_run(
            self.config, self.make_progress(), db_path=self.db_path
        )

        history = load_history(self.db_path)

        self.assertEqual(history["id"].tolist()[:2], [second_id, first_id])
        self.assertEqual(count_runs(db_path=self.db_path), 2)
        self.assertEqual(
            load_saved_run(999_999, db_path=self.db_path), (None, None, None)
        )

    def test_decode_json_and_capture_validation(self):
        self.assertEqual(decode_json(None), {})
        self.assertEqual(decode_json("not json"), {})
        self.assertEqual(decode_json('{"ok": true}'), {"ok": True})

        with self.assertRaises(ValueError):
            save_run(
                self.config,
                self.make_progress(),
                db_path=self.db_path,
                capture_policy="unexpected",
            )
        with self.assertRaises(ValueError):
            save_run(
                self.config,
                self.make_progress(),
                db_path=self.db_path,
                capture_sample_limit=-1,
            )


if __name__ == "__main__":
    unittest.main()
