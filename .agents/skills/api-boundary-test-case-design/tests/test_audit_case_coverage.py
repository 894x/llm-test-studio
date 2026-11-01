from __future__ import annotations

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).parents[1] / "scripts" / "audit_case_coverage.py"
module_spec = importlib.util.spec_from_file_location("case_audit", SCRIPT)
audit = importlib.util.module_from_spec(module_spec)
module_spec.loader.exec_module(audit)


class AuditCaseCoverageTest(unittest.TestCase):
    def test_case_schema_versions_preserve_description_boundary(self):
        case = {"schema_version": 2, "id": "id", "definitions": {
            "openai-chat": {"inputs": {}, "assertions": [], "request": {"body": {}}}
        }}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "case.json"
            for version, description, valid in [
                (2, None, True), (2, "legacy", False),
                (3, "current", True), (3, " ", False), (3, None, False), (4, None, False),
            ]:
                with self.subTest(version=version, description=description):
                    case["schema_version"] = version
                    case.pop("description", None)
                    if description is not None:
                        case["description"] = description
                    path.write_text(json.dumps(case), encoding="utf-8")
                    if valid:
                        self.assertEqual(audit.load_case(path)["schema_version"], version)
                    else:
                        with self.assertRaises(ValueError):
                            audit.load_case(path)


if __name__ == "__main__":
    unittest.main()
