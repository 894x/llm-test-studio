import json
import os
import sqlite3
import tempfile
import unittest
from pathlib import Path

from llm_studio.audit_storage import (
    count_audit_runs,
    load_audit_history,
    load_audit_run,
    save_audit_run,
)
from llm_studio.storage import init_db


class AuditStorageTest(unittest.TestCase):
    def setUp(self):
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.db_path = Path(self.temporary_directory.name) / "history.db"

    def tearDown(self):
        self.temporary_directory.cleanup()

    def make_events(self):
        return [
            {
                "type": "plan",
                "schema_version": 1,
                "total": 2,
                "runs": [
                    {
                        "id": "S001@seedance-1-0-pro",
                        "case_id": "S001",
                        "name": "Create a video task",
                        "dimension": "task",
                        "kind": "seedance_task",
                        "model": "seedance-1-0-pro",
                    },
                    {
                        "id": "S002@seedance-1-0-pro",
                        "case_id": "S002",
                        "name": "Poll a video task",
                        "dimension": "task",
                        "kind": "seedance_task",
                        "model": "seedance-1-0-pro",
                    },
                ],
            },
            {
                "type": "progress",
                "schema_version": 1,
                "completed": 1,
                "total": 2,
                "result": {
                    "id": "S001@seedance-1-0-pro",
                    "name": "Create a video task",
                    "dimension": "task",
                    "protocol": "seedance",
                    "model": "seedance-1-0-pro",
                    "status": "pass",
                    "severity": "critical",
                    "elapsed_ms": 125,
                    "evidence": "task task-123 succeeded",
                    "http_status": 200,
                    "metrics": {"poll_count": 3, "video_host": "cdn.example"},
                    "artifact_dir": "output/api-audit/run-1/S001",
                    "exchanges": [{"method": "POST", "status_code": 200}],
                },
            },
            {
                "type": "progress",
                "schema_version": 1,
                "completed": 2,
                "total": 2,
                "result": {
                    "id": "S002@seedance-1-0-pro",
                    "name": "Poll a video task",
                    "dimension": "task",
                    "protocol": "seedance",
                    "model": "seedance-1-0-pro",
                    "status": "warning",
                    "severity": "normal",
                    "elapsed_ms": 87,
                    "evidence": "accepted; terminal status was not checked",
                    "metrics": {"poll_count": 0},
                },
            },
            {
                "type": "final",
                "schema_version": 1,
                "command": "run",
                "report_dir": "output/api-audit/run-1",
                "report_json": "output/api-audit/run-1/report.json",
                "report_html": "output/api-audit/run-1/report.html",
                "overall": "review",
                "verdict": "需复核：1 项警告",
                "summary": {"pass": 1, "warning": 1, "fail": 0, "unknown": 0},
            },
        ]

    def test_seedance_round_trip_and_existing_database_compatibility(self):
        init_db(self.db_path)
        events = self.make_events()
        report_dir = Path(self.temporary_directory.name) / "reports" / "run-1"
        events[-1]["report_dir"] = str(report_dir)
        events[-1]["report_json"] = str(report_dir / "report.json")
        events[-1]["report_html"] = str(report_dir / "report.html")

        run_id = save_audit_run(
            "seedance",
            "https://gateway.example/v1?api_key=must-not-persist#fragment",
            "seedance-1-0-pro",
            events,
            db_path=self.db_path,
        )
        loaded = load_audit_run(run_id, db_path=self.db_path)

        self.assertIsNotNone(loaded)
        self.assertEqual(loaded["suite"], "seedance")
        self.assertEqual(loaded["base_url"], "https://gateway.example/v1")
        self.assertEqual(loaded["model"], "seedance-1-0-pro")
        self.assertEqual(loaded["overall"], "review")
        self.assertEqual(loaded["verdict"], "需复核：1 项警告")
        self.assertEqual(
            loaded["summary"],
            {"pass": 1, "warning": 1, "fail": 0, "unknown": 0},
        )
        self.assertEqual(loaded["config"]["suite"], "seedance")
        self.assertEqual(loaded["config"]["plan"]["total"], 2)
        expected_report_dir = Path(
            os.path.relpath(report_dir, Path(__file__).resolve().parent)
        ).as_posix()
        self.assertEqual(loaded["report_dir"], expected_report_dir)
        self.assertEqual(
            loaded["report_json"], f"{expected_report_dir}/report.json"
        )
        self.assertEqual(
            loaded["report_html"], f"{expected_report_dir}/report.html"
        )
        self.assertEqual(
            [result["status"] for result in loaded["results"]],
            ["pass", "warning"],
        )
        self.assertEqual(loaded["results"][0]["metrics"]["poll_count"], 3)
        self.assertEqual(
            loaded["results"][0]["evidence"], "task task-123 succeeded"
        )
        self.assertNotIn(b"must-not-persist", self.db_path.read_bytes())

        connection = sqlite3.connect(self.db_path)
        table_names = {
            row[0]
            for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table'"
            ).fetchall()
        }
        case_row = connection.execute(
            """SELECT status, evidence, metrics_json, result_json
               FROM audit_case_results WHERE audit_run_id = ? AND sequence = 0""",
            (run_id,),
        ).fetchone()
        connection.close()
        self.assertTrue({"runs", "run_results", "audit_runs", "audit_case_results"} <= table_names)
        self.assertEqual(case_row[0], "pass")
        self.assertEqual(case_row[1], "task task-123 succeeded")
        self.assertEqual(json.loads(case_row[2])["video_host"], "cdn.example")
        self.assertEqual(json.loads(case_row[3])["protocol"], "seedance")

    def test_history_is_newest_first_and_obeys_limit(self):
        first_id = save_audit_run(
            "seedance",
            "https://gateway.example",
            "model-a",
            self.make_events(),
            db_path=self.db_path,
        )
        second_id = save_audit_run(
            "seedance",
            "https://gateway.example",
            "model-b",
            self.make_events(),
            db_path=self.db_path,
        )

        history = load_audit_history(limit=1, db_path=self.db_path)

        self.assertEqual([entry["id"] for entry in history], [second_id])
        self.assertEqual(count_audit_runs(db_path=self.db_path), 2)
        self.assertEqual(history[0]["summary"]["warning"], 1)
        self.assertNotEqual(first_id, second_id)
        self.assertNotIn("results", history[0])
        with self.assertRaises(ValueError):
            load_audit_history(limit=0, db_path=self.db_path)

    def test_missing_run_returns_none(self):
        self.assertIsNone(load_audit_run(999, db_path=self.db_path))

    def test_api_key_fields_are_rejected_before_database_write(self):
        events = self.make_events()
        events[1]["result"]["metrics"]["engine_api_key"] = "sk-forbidden"

        with self.assertRaisesRegex(ValueError, "API key fields cannot be persisted"):
            save_audit_run(
                "seedance",
                "https://gateway.example",
                "seedance-1-0-pro",
                events,
                db_path=self.db_path,
            )

        self.assertFalse(self.db_path.exists())

    def test_event_contract_requires_plan_final_and_result_objects(self):
        with self.assertRaisesRegex(ValueError, "plan event"):
            save_audit_run(
                "seedance",
                "https://gateway.example",
                "model",
                [{"type": "final", "command": "run"}],
                db_path=self.db_path,
            )

        events = self.make_events()
        events[1]["result"] = "not-an-object"
        with self.assertRaisesRegex(ValueError, "no result object"):
            save_audit_run(
                "seedance",
                "https://gateway.example",
                "model",
                events,
                db_path=self.db_path,
            )


if __name__ == "__main__":
    unittest.main()
