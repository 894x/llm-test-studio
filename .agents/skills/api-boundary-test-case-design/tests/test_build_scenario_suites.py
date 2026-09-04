from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any


SCRIPT = Path(__file__).parents[1] / "scripts" / "build_scenario_suites.py"


class BuildScenarioSuitesTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.cases_root = self.root / "cases" / "demo-api"
        self.suites_root = self.root / "suites" / "demo-api"
        self.manifest = self.root / "demo-v1.suite-profiles.json"

        self.write_case("C001-smoke", "demo.smoke", "automatic", "demo_success")
        self.write_case("C002-auth", "demo.auth", "automatic", "demo_auth_rejected")
        self.write_case("C003-invalid", "demo.invalid", "automatic", "demo_rejected")
        self.write_case("C004-media", "demo.media", "manual", "demo_success")
        self.write_case(
            "C005-disabled", "demo.disabled", "manual", "demo_success", enabled=False
        )
        self.write_case(
            "C006-other-model",
            "demo.other",
            "automatic",
            "demo_success",
            model_targets=["demo-v2"],
        )
        self.write_manifest()

    def write_case(
        self,
        directory: str,
        key: str,
        execution_mode: str,
        kind: str,
        *,
        enabled: bool = True,
        model_targets: list[str] | None = None,
    ) -> None:
        target = self.cases_root / directory / "case.json"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(
            json.dumps(
                {
                    "schema_version": 2,
                    "key": key,
                    "name": key,
                    "dimension": "parameters",
                    "protocol": "demo-api",
                    "model_targets": model_targets or ["demo-v1"],
                    "enabled": enabled,
                    "default": False,
                    "severity": "critical",
                    "execution_mode": execution_mode,
                    "definition": {
                        "schema_version": 2,
                        "type": "legacy.apiaudit",
                        "type_version": 1,
                        "spec": {"kind": kind, "request": {}, "options": {}},
                    },
                },
                ensure_ascii=False,
                indent=2,
            ),
            encoding="utf-8",
        )

    def write_manifest(self, *, basic_keys: list[str] | None = None) -> None:
        self.manifest.write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "protocol": "demo-api",
                    "model_target": "demo-v1",
                    "profiles": [
                        {
                            "directory": "demo-v1-connectivity",
                            "key": "demo-api.demo-v1.connectivity",
                            "name": "Demo V1 connectivity",
                            "case_keys": ["demo.smoke", "demo.auth"],
                        },
                        {
                            "directory": "demo-v1-basic",
                            "key": "demo-api.demo-v1.basic",
                            "name": "Demo V1 basic",
                            "case_keys": basic_keys or ["demo.smoke", "demo.media"],
                        },
                        {
                            "directory": "demo-v1-parameter-rejection",
                            "key": "demo-api.demo-v1.parameter-rejection",
                            "name": "Demo V1 parameter rejection",
                            "selector": {
                                "enabled": True,
                                "execution_modes": ["automatic"],
                                "kinds": ["demo_rejected"],
                            },
                        },
                        {
                            "directory": "demo-v1-automatic",
                            "key": "demo-api.demo-v1.automatic",
                            "name": "Demo V1 automatic",
                            "selector": {
                                "enabled": True,
                                "execution_modes": ["automatic"],
                            },
                        },
                        {
                            "directory": "demo-v1-complete",
                            "key": "demo-api.demo-v1.complete",
                            "name": "Demo V1 complete",
                            "selector": {},
                        },
                    ],
                },
                ensure_ascii=False,
                indent=2,
            ),
            encoding="utf-8",
        )

    def run_script(self, mode: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                sys.executable,
                str(SCRIPT),
                "--cases-root",
                str(self.cases_root),
                "--suites-root",
                str(self.suites_root),
                "--manifest",
                str(self.manifest),
                mode,
            ],
            text=True,
            capture_output=True,
            check=False,
        )

    def read_suite(self, directory: str) -> dict[str, Any]:
        return json.loads(
            (self.suites_root / directory / "suite.json").read_text(encoding="utf-8")
        )

    def test_write_builds_explicit_and_selector_profiles(self) -> None:
        result = self.run_script("--write")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            self.read_suite("demo-v1-connectivity"),
            {
                "schema_version": 1,
                "key": "demo-api.demo-v1.connectivity",
                "name": "Demo V1 connectivity",
                "protocol": "demo-api",
                "model_target": "demo-v1",
                "case_keys": ["demo.smoke", "demo.auth"],
            },
        )
        self.assertEqual(
            self.read_suite("demo-v1-basic")["case_keys"],
            ["demo.smoke", "demo.media"],
        )
        self.assertEqual(
            self.read_suite("demo-v1-parameter-rejection")["case_keys"],
            ["demo.invalid"],
        )
        self.assertEqual(
            self.read_suite("demo-v1-automatic")["case_keys"],
            ["demo.smoke", "demo.auth", "demo.invalid"],
        )
        self.assertEqual(
            self.read_suite("demo-v1-complete")["case_keys"],
            [
                "demo.smoke",
                "demo.auth",
                "demo.invalid",
                "demo.media",
                "demo.disabled",
            ],
        )

    def test_check_detects_suite_membership_drift(self) -> None:
        written = self.run_script("--write")
        self.assertEqual(written.returncode, 0, written.stderr)
        self.assertEqual(self.run_script("--check").returncode, 0)

        suite = self.read_suite("demo-v1-automatic")
        suite["case_keys"] = ["demo.smoke"]
        target = self.suites_root / "demo-v1-automatic" / "suite.json"
        target.write_text(json.dumps(suite, indent=2), encoding="utf-8")

        checked = self.run_script("--check")
        self.assertNotEqual(checked.returncode, 0)
        self.assertIn("out of date", checked.stderr)

    def test_explicit_profile_rejects_unknown_or_inapplicable_keys(self) -> None:
        for invalid_key in ("demo.missing", "demo.other"):
            with self.subTest(invalid_key=invalid_key):
                self.write_manifest(basic_keys=["demo.smoke", invalid_key])
                result = self.run_script("--write")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(invalid_key, result.stderr)


if __name__ == "__main__":
    unittest.main()
