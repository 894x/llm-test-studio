#!/usr/bin/env python3
"""Build deterministic model-scoped suite.json files from case metadata."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import tempfile
from pathlib import Path
from typing import Any


SAFE_SEGMENT = re.compile(r"^[A-Za-z0-9._-]+$")
SELECTOR_FIELDS = {"enabled", "execution_modes", "kinds", "dimensions", "severities"}


class SuiteBuildError(ValueError):
    """Raised when cases or the suite profile manifest are invalid."""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Build scenario suite.json files from repository-owned case.json files."
    )
    parser.add_argument("--cases-root", required=True, type=Path)
    parser.add_argument("--suites-root", required=True, type=Path)
    parser.add_argument("--manifest", required=True, type=Path)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--write", action="store_true", help="Write generated suites")
    mode.add_argument(
        "--check", action="store_true", help="Fail when generated suites are missing or stale"
    )
    return parser.parse_args()


def load_object(path: Path, label: str) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise SuiteBuildError(f"{label} {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise SuiteBuildError(f"{label} {path}: top-level JSON value must be an object")
    return value


def required_string(value: Any, label: str, *, safe: bool = False) -> str:
    if not isinstance(value, str) or not value.strip() or value != value.strip():
        raise SuiteBuildError(f"{label} must be a non-empty trimmed string")
    if safe and not SAFE_SEGMENT.fullmatch(value):
        raise SuiteBuildError(f"{label} must contain only letters, digits, dot, underscore, or hyphen")
    return value


def string_list(value: Any, label: str, *, allow_empty: bool = False) -> list[str]:
    if not isinstance(value, list) or (not value and not allow_empty):
        raise SuiteBuildError(f"{label} must be a non-empty string array")
    result: list[str] = []
    for index, item in enumerate(value):
        result.append(required_string(item, f"{label}[{index}]"))
    if len(set(result)) != len(result):
        raise SuiteBuildError(f"{label} must not contain duplicate values")
    return result


def load_cases(
    root: Path, protocol: str, model_target: str
) -> tuple[dict[str, dict[str, Any]], list[dict[str, Any]]]:
    if not root.is_dir():
        raise SuiteBuildError(f"case root is not a directory: {root}")
    all_cases: dict[str, dict[str, Any]] = {}
    applicable: list[dict[str, Any]] = []
    paths = sorted(root.rglob("case.json"), key=lambda path: path.relative_to(root).as_posix())
    if not paths:
        raise SuiteBuildError(f"case root contains no case.json files: {root}")

    for path in paths:
        document = load_object(path, "case")
        key = required_string(document.get("key"), f"case {path} key")
        if key in all_cases:
            raise SuiteBuildError(f"duplicate case key {key!r}: {path}")
        if document.get("protocol") != protocol:
            raise SuiteBuildError(
                f"case {path} protocol {document.get('protocol')!r} does not match {protocol!r}"
            )
        targets = document.get("model_targets") or []
        if not isinstance(targets, list) or any(not isinstance(item, str) for item in targets):
            raise SuiteBuildError(f"case {path} model_targets must be a string array")
        case = {
            "key": key,
            "enabled": document.get("enabled"),
            "execution_mode": document.get("execution_mode"),
            "dimension": document.get("dimension"),
            "severity": document.get("severity"),
            "kind": document.get("definition", {}).get("spec", {}).get("kind"),
            "model_targets": targets,
        }
        all_cases[key] = case
        if not targets or model_target in targets:
            applicable.append(case)
    return all_cases, applicable


def matches_selector(case: dict[str, Any], selector: dict[str, Any], label: str) -> bool:
    unknown = set(selector) - SELECTOR_FIELDS
    if unknown:
        raise SuiteBuildError(f"{label} has unknown selector fields: {', '.join(sorted(unknown))}")
    if "enabled" in selector:
        if not isinstance(selector["enabled"], bool):
            raise SuiteBuildError(f"{label}.enabled must be a boolean")
        if case["enabled"] is not selector["enabled"]:
            return False
    for field, case_field in (
        ("execution_modes", "execution_mode"),
        ("kinds", "kind"),
        ("dimensions", "dimension"),
        ("severities", "severity"),
    ):
        if field in selector:
            accepted = string_list(selector[field], f"{label}.{field}")
            if case[case_field] not in accepted:
                return False
    return True


def select_case_keys(
    profile: dict[str, Any],
    profile_label: str,
    all_cases: dict[str, dict[str, Any]],
    applicable: list[dict[str, Any]],
    model_target: str,
) -> list[str]:
    has_explicit = "case_keys" in profile
    has_selector = "selector" in profile
    if has_explicit == has_selector:
        raise SuiteBuildError(
            f"{profile_label} must define exactly one of case_keys or selector"
        )
    applicable_keys = {case["key"] for case in applicable}
    if has_explicit:
        keys = string_list(profile["case_keys"], f"{profile_label}.case_keys")
        for key in keys:
            if key not in all_cases:
                raise SuiteBuildError(f"{profile_label} references unknown case key {key!r}")
            if key not in applicable_keys:
                raise SuiteBuildError(
                    f"{profile_label} case key {key!r} does not apply to model {model_target!r}"
                )
        return keys

    selector = profile["selector"]
    if not isinstance(selector, dict):
        raise SuiteBuildError(f"{profile_label}.selector must be an object")
    keys = [
        case["key"]
        for case in applicable
        if matches_selector(case, selector, f"{profile_label}.selector")
    ]
    if not keys:
        raise SuiteBuildError(f"{profile_label}.selector matched no cases")
    return keys


def build_documents(
    cases_root: Path, manifest_path: Path
) -> list[tuple[str, dict[str, Any]]]:
    manifest = load_object(manifest_path, "manifest")
    if manifest.get("schema_version") != 1:
        raise SuiteBuildError("manifest schema_version must be 1")
    protocol = required_string(manifest.get("protocol"), "manifest protocol", safe=True)
    model_target = required_string(manifest.get("model_target"), "manifest model_target")
    profiles = manifest.get("profiles")
    if not isinstance(profiles, list) or not profiles:
        raise SuiteBuildError("manifest profiles must be a non-empty array")

    all_cases, applicable = load_cases(cases_root, protocol, model_target)
    documents: list[tuple[str, dict[str, Any]]] = []
    seen_directories: set[str] = set()
    seen_suite_keys: set[str] = set()
    for index, profile in enumerate(profiles):
        label = f"manifest profiles[{index}]"
        if not isinstance(profile, dict):
            raise SuiteBuildError(f"{label} must be an object")
        directory = required_string(profile.get("directory"), f"{label}.directory", safe=True)
        suite_key = required_string(profile.get("key"), f"{label}.key", safe=True)
        name = required_string(profile.get("name"), f"{label}.name")
        if directory in seen_directories:
            raise SuiteBuildError(f"duplicate suite directory {directory!r}")
        if suite_key in seen_suite_keys:
            raise SuiteBuildError(f"duplicate suite key {suite_key!r}")
        seen_directories.add(directory)
        seen_suite_keys.add(suite_key)
        case_keys = select_case_keys(profile, label, all_cases, applicable, model_target)
        documents.append(
            (
                directory,
                {
                    "schema_version": 1,
                    "key": suite_key,
                    "name": name,
                    "protocol": protocol,
                    "model_target": model_target,
                    "case_keys": case_keys,
                },
            )
        )
    return documents


def write_document(path: Path, document: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = json.dumps(document, ensure_ascii=False, indent=2) + "\n"
    with tempfile.NamedTemporaryFile(
        mode="w", encoding="utf-8", dir=path.parent, delete=False, prefix=".suite-", suffix=".tmp"
    ) as handle:
        temporary = Path(handle.name)
        handle.write(payload)
        handle.flush()
        os.fsync(handle.fileno())
    try:
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def check_documents(
    suites_root: Path, documents: list[tuple[str, dict[str, Any]]]
) -> list[str]:
    errors: list[str] = []
    for directory, expected in documents:
        target = suites_root / directory / "suite.json"
        try:
            actual = load_object(target, "suite")
        except SuiteBuildError as exc:
            errors.append(str(exc))
            continue
        if actual != expected:
            errors.append(f"suite {target} is out of date")
    return errors


def print_summary(mode: str, documents: list[tuple[str, dict[str, Any]]]) -> None:
    print(f"mode={mode} suites={len(documents)}")
    for directory, document in documents:
        print(f"{directory}: {len(document['case_keys'])} cases")


def main() -> int:
    args = parse_args()
    try:
        documents = build_documents(args.cases_root, args.manifest)
        if args.check:
            errors = check_documents(args.suites_root, documents)
            if errors:
                print("\n".join(errors), file=sys.stderr)
                return 1
            print_summary("check", documents)
            return 0
        if args.write:
            for directory, document in documents:
                write_document(args.suites_root / directory / "suite.json", document)
            print_summary("write", documents)
            return 0
        print_summary("preview", documents)
        return 0
    except SuiteBuildError as exc:
        print(str(exc), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
