from __future__ import annotations

import importlib.util
import json
import tempfile
import unittest
import uuid
from pathlib import Path

SCRIPT = Path(__file__).parents[1] / "scripts" / "build_scenario_suites.py"
module_spec = importlib.util.spec_from_file_location("suite_builder", SCRIPT)
builder = importlib.util.module_from_spec(module_spec)
module_spec.loader.exec_module(builder)


class BuildScenarioSuitesTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.cases = self.root / "cases"
        self.cases.mkdir()
        self.case_path = self.cases / "case.json"
        self.case_id = str(uuid.uuid5(uuid.UUID("7680782d-7ae8-558b-9f32-17d13f31a66b"), "builtin.cases/v2/openai-chat/demo.smoke"))
        self.case = {
            "schema_version": 2, "id": self.case_id, "key": "demo.smoke", "name": "Smoke",
            "definitions": {"openai-chat": {"inputs": {"prompt": {"type": "string"}},
                "request": {"body": {"messages": [{"role": "user", "content": {"$input": "prompt"}}]}}, "assertions": []}}
        }
        self.profile = {"directory": "smoke", "schema_version": 1, "key": "demo.smoke",
            "name": "Smoke", "protocol": "openai-chat", "description": "Current Suite",
            "cases": [{"case_id": self.case_id}], "inputs": [{"name": "prompt", "type": "string",
                "bindings": [{"case_id": self.case_id, "input": "prompt"}]}]}
        self.manifest = {"schema_version": 1, "protocol": "openai-chat", "profiles": [self.profile]}
        self.manifest_path = self.root / "profiles.json"
        self.save()

    def save(self):
        self.case_path.write_text(json.dumps(self.case), encoding="utf-8")
        self.manifest_path.write_text(json.dumps(self.manifest), encoding="utf-8")

    def build(self):
        return builder.build_documents(self.cases, self.manifest_path)

    def test_current_references_and_bindings_round_trip(self):
        documents = self.build()
        target = self.root / "suites" / "smoke" / "suite.json"
        builder.write_document(target, documents[0][1])
        self.assertEqual(builder.check_documents(self.root / "suites", documents), [])
        self.assertEqual(json.loads(target.read_text())["cases"], [{"case_id": self.case_id}])
        self.assertNotIn("directory", documents[0][1])
        target.write_text("{}")
        self.assertIn("out of date", builder.check_documents(self.root / "suites", documents)[0])

    def test_obsolete_formats_are_rejected_without_mutation(self):
        self.case["schema_version"] = 1
        self.save()
        before = self.case_path.read_bytes()
        with self.assertRaisesRegex(builder.SuiteBuildError, "schema_version 2"):
            self.build()
        self.assertEqual(before, self.case_path.read_bytes())
        self.assertFalse((self.root / "suites").exists())

    def test_invalid_members_and_bindings(self):
        for field, value in [("cases", [{"case_id": str(uuid.uuid4())}]),
                             ("cases", [{"case_id": self.case_id}] * 2),
                             ("inputs", [{"bindings": [{"case_id": self.case_id, "input": "absent"}]}]),
                             ("inputs", [{"bindings": [{"case_id": str(uuid.uuid4()), "input": "prompt"}]}]),
                             ("protocol", "seedance"), ("directory", "..")]:
            with self.subTest(field=field, value=value):
                original = self.profile[field]
                self.profile[field] = value
                self.save()
                with self.assertRaises(builder.SuiteBuildError):
                    self.build()
                self.profile[field] = original

    def test_old_selectors_and_model_routing_are_rejected(self):
        self.manifest["model_target"] = "superseded"
        self.save()
        with self.assertRaises(builder.SuiteBuildError):
            self.build()
        del self.manifest["model_target"]
        self.profile["selector"] = {"kinds": ["old_success"]}
        self.save()
        with self.assertRaises(builder.SuiteBuildError):
            self.build()

    def test_all_profiles_are_validated_before_writing(self):
        self.manifest["profiles"].append({**self.profile, "directory": "other"})
        self.save()
        with self.assertRaises(builder.SuiteBuildError):
            self.build()
        self.assertFalse((self.root / "suites").exists())


if __name__ == "__main__":
    unittest.main()
