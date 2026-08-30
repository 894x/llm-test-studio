import json
from pathlib import Path
import tempfile
import unittest

from llm_studio.catalog import (
    Catalog,
    CatalogNotFoundError,
    CatalogValidationError,
    ModelProfile,
)


def example_case(**overrides):
    case = {
        "id": "T001",
        "name": "Example case",
        "dimension": "protocol",
        "protocol": "openai-chat",
        "kind": "chat_sync",
        "request": {
            "method": "POST",
            "path": "/v1/chat/completions",
            "body": {"messages": [{"role": "user", "content": "hello"}]},
        },
    }
    case.update(overrides)
    return case


def example_profile(**overrides):
    profile = {
        "schema_version": 1,
        "id": "local-chat",
        "display_name": "Local Chat",
        "model": "local-chat",
        "protocol": "openai-chat",
        "endpoint": "",
        "suites": ["openai-chat"],
        "capabilities": ["chat"],
        "enabled": True,
    }
    profile.update(overrides)
    return profile


class CatalogCaseTests(unittest.TestCase):
    def setUp(self):
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary_directory.name)
        self.case_path = self.root / "cases/openai-chat/T001-example/case.json"
        self.case_path.parent.mkdir(parents=True)
        self.case_path.write_text(
            json.dumps(example_case(), ensure_ascii=False), encoding="utf-8"
        )
        self.catalog = Catalog(self.root)

    def tearDown(self):
        self.temporary_directory.cleanup()

    def test_list_filter_and_read_case(self):
        disabled_path = self.root / "cases/openai-chat/T002-disabled/case.json"
        disabled_path.parent.mkdir(parents=True)
        disabled_path.write_text(
            json.dumps(example_case(id="T002", disabled=True)), encoding="utf-8"
        )

        self.assertEqual(2, len(self.catalog.list_cases(protocol="openai-chat")))
        self.assertEqual(["T001"], [case["id"] for case in self.catalog.list_cases(enabled=True)])
        self.assertEqual("T001", self.catalog.read_case("openai-chat", "T001")["id"])
        self.assertEqual("T001", self.catalog.read_case("T001")["id"])
        self.assertEqual(
            "T001",
            self.catalog.read_case("openai-chat/T001-example/case.json")["id"],
        )

    def test_save_existing_case_preserves_its_directory(self):
        case = self.catalog.read_case("T001")
        case["name"] = "Updated"

        saved_path = self.catalog.save_case(case)

        self.assertEqual(self.case_path.resolve(), saved_path)
        self.assertEqual("Updated", json.loads(self.case_path.read_text())["name"])
        self.assertEqual([self.case_path], list(self.root.glob("cases/**/case.json")))
        self.assertEqual([], list(self.case_path.parent.glob(".case.json.*.tmp")))

    def test_save_new_case_requires_and_preserves_protocol_directory(self):
        case = example_case(id="T003")
        saved_path = self.catalog.save_case(
            case, "openai-chat/T003-new-case/case.json"
        )

        self.assertEqual(
            (self.root / "cases/openai-chat/T003-new-case/case.json").resolve(),
            saved_path,
        )
        with self.assertRaises(CatalogValidationError):
            self.catalog.save_case(case, "seedance/T003-new-case/case.json")

    def test_case_validation_rejects_missing_wrong_and_unsafe_values(self):
        for field in ("id", "name", "dimension", "protocol", "kind", "request"):
            invalid = example_case()
            del invalid[field]
            with self.subTest(field=field), self.assertRaises(CatalogValidationError):
                self.catalog.validate_case(invalid)

        with self.assertRaises(CatalogValidationError):
            self.catalog.validate_case(example_case(request=[]))
        with self.assertRaises(CatalogValidationError):
            self.catalog.validate_case(example_case(protocol="unknown"))
        with self.assertRaises(CatalogValidationError):
            self.catalog.read_case("../outside/case.json")

    def test_missing_case_has_specific_error(self):
        with self.assertRaises(CatalogNotFoundError):
            self.catalog.read_case("missing")


class CatalogModelTests(unittest.TestCase):
    def setUp(self):
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary_directory.name)
        self.catalog = Catalog(self.root)

    def tearDown(self):
        self.temporary_directory.cleanup()

    def test_model_profile_round_trip_and_filters(self):
        path = self.catalog.save_model(example_profile(note="safe metadata"))

        self.assertEqual(
            (self.root / "definitions/models/local-chat.json").resolve(), path
        )
        loaded = self.catalog.read_model("local-chat")
        self.assertIsInstance(loaded, ModelProfile)
        self.assertEqual(("openai-chat",), loaded.suites)
        self.assertEqual("safe metadata", loaded.extra["note"])
        self.assertEqual([loaded], self.catalog.list_models(enabled=True))
        self.assertEqual([], self.catalog.list_models(protocol="seedance"))

    def test_model_save_strictly_rejects_api_keys_at_any_depth(self):
        for key in ("api_key", "API-Key", "apiKey"):
            profile = example_profile(metadata={key: "secret"})
            with self.subTest(key=key), self.assertRaises(CatalogValidationError):
                self.catalog.save_model(profile)
        self.assertFalse((self.root / "definitions/models/local-chat.json").exists())

    def test_model_filename_is_derived_from_safe_id(self):
        with self.assertRaises(CatalogValidationError):
            self.catalog.save_model(example_profile(id="../escape"))


class BundledCatalogTests(unittest.TestCase):
    def test_existing_cases_and_initial_models_are_valid(self):
        catalog = Catalog(Path(__file__).resolve().parent)

        self.assertEqual(89, len(catalog.list_cases()))
        self.assertEqual(40, len(catalog.list_cases(protocol="kimi-k3")))
        self.assertEqual(43, len(catalog.list_cases(protocol="openai-chat")))
        self.assertEqual(6, len(catalog.list_cases(protocol="seedance")))
        profiles = catalog.list_models()
        self.assertEqual(
            {"kimi-k3", "glm-5.2", "glm-5.3", "ds-v4-flash", "ds-v4-pro"},
            {profile.id for profile in profiles},
        )
        self.assertTrue(all(profile.endpoint == "" for profile in profiles))
        self.assertTrue(all("api_key" not in profile.to_dict() for profile in profiles))


if __name__ == "__main__":
    unittest.main()
