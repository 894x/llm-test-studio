import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
import unittest
from unittest.mock import patch

from llm_test.engine_client import ENGINE_API_KEY_ENV, EngineClient


PROJECT_ROOT = Path(__file__).resolve().parent


def write_case(root: Path, *, suite: str = "openai-chat") -> None:
    case_dir = root / "cases" / suite / "T001-example"
    case_dir.mkdir(parents=True)
    case = {
        "id": "T001",
        "name": "Example compatibility case",
        "dimension": "protocol",
        "protocol": suite,
        "kind": "chat_sync",
        "default": True,
        "severity": "normal",
        "request": {
            "method": "POST",
            "path": "/v1/chat/completions",
            "body": {"messages": [{"role": "user", "content": "hello"}]},
        },
    }
    (case_dir / "case.json").write_text(json.dumps(case), encoding="utf-8")


class EngineClientIntegrationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary_directory = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temporary_directory.name)
        shutil.copy2(PROJECT_ROOT / "go.mod", cls.root / "go.mod")
        shutil.copytree(PROJECT_ROOT / "engine", cls.root / "engine")
        shutil.rmtree(cls.root / "engine" / "bin", ignore_errors=True)
        write_case(cls.root)
        cls.client = EngineClient(cls.root)

    @classmethod
    def tearDownClass(cls):
        cls.temporary_directory.cleanup()

    def test_build_creates_standalone_binary(self):
        binary = self.client.build(force=True)

        self.assertTrue(binary.is_file())
        if os.name != "nt":
            self.assertTrue(binary.stat().st_mode & 0o100)

    def test_list_returns_engine_validated_cases(self):
        cases = self.client.list_cases("openai-chat")

        self.assertEqual(["T001"], [case["id"] for case in cases])

    def test_dry_run_emits_plan_progress_final_and_report(self):
        output = self.root / "reports" / "dry-run"
        events = self.client.run(
            suite="openai-chat",
            base_url="https://gateway.example",
            model="example-model",
            dry_run=True,
            output=output,
        )

        self.assertEqual(["plan", "progress", "final"], [event["type"] for event in events])
        self.assertEqual("unknown", events[1]["result"]["status"])
        self.assertTrue((output / "report.json").is_file())
        self.assertTrue((output / "report.html").is_file())


class EngineClientCredentialTests(unittest.TestCase):
    def test_api_key_is_only_in_child_environment_not_argv(self):
        secret = "sk-super-secret-value"
        observed = {}

        class FakeProcess:
            def __init__(self, command, **kwargs):
                observed["command"] = command
                observed["environment"] = kwargs["env"]
                self.stdout = iter(
                    [json.dumps({"type": "final", "command": "run", "overall": "qualified"}) + "\n"]
                )

            def wait(self):
                return 0

            def terminate(self):
                return None

        client = EngineClient(PROJECT_ROOT, binary_path=sys.executable)
        with patch("llm_test.engine_client.subprocess.Popen", FakeProcess):
            events = client.run(
                suite="openai-chat",
                base_url="https://gateway.example",
                model="example-model",
                api_key=secret,
                all_cases=True,
            )

        self.assertEqual("final", events[-1]["type"])
        self.assertNotIn(secret, observed["command"])
        self.assertEqual(secret, observed["environment"][ENGINE_API_KEY_ENV])
        self.assertIn("--api-key-env", observed["command"])


if __name__ == "__main__":
    unittest.main()
