#!/usr/bin/env python3
"""Validate and write current reference-only scenario Suite documents."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import tempfile
import uuid
from pathlib import Path
from typing import Any


SAFE_SEGMENT = re.compile(r"^[A-Za-z0-9._-]+$")

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


def load_cases(root: Path, protocol: str) -> dict[str, dict[str, Any]]:
    if not root.is_dir():
        raise SuiteBuildError(f"case root is not a directory: {root}")
    cases = {}
    namespace = uuid.UUID("7680782d-7ae8-558b-9f32-17d13f31a66b")
    for path in sorted(root.rglob("case.json")):
        document = load_object(path, "case")
        if document.get("schema_version") != 1 or "model_targets" in document:
            raise SuiteBuildError(f"case {path} must use current schema_version 1")
        if document.get("protocol") != protocol:
            raise SuiteBuildError(f"case {path} protocol differs from manifest")
        key = required_string(document.get("key"), f"case {path} key", safe=True)
        definition = document.get("definition", {})
        spec = definition.get("spec", {})
        if definition.get("schema_version") != 1 or definition.get("type") != protocol or definition.get("type_version") != 1:
            raise SuiteBuildError(f"case {path} must use the current protocol definition")
        if not isinstance(spec.get("inputs"), dict) or not isinstance(spec.get("assertions"), list) or "kind" in spec:
            raise SuiteBuildError(f"case {path} must declare inputs and assertions")
        case_id = str(uuid.uuid5(namespace, f"builtin.cases/v2/{protocol}/{key}"))
        if case_id in cases:
            raise SuiteBuildError(f"duplicate case key {key!r}")
        cases[case_id] = document
    if not cases:
        raise SuiteBuildError("case root contains no current cases")
    return cases


def build_documents(cases_root: Path, manifest_path: Path) -> list[tuple[str, dict[str, Any]]]:
    manifest = load_object(manifest_path, "manifest")
    if manifest.get("schema_version") != 1 or set(manifest) != {"schema_version", "protocol", "profiles"}:
        raise SuiteBuildError("manifest must contain only schema_version 1, protocol and profiles")
    protocol = required_string(manifest.get("protocol"), "manifest protocol", safe=True)
    cases = load_cases(cases_root, protocol)
    profiles = manifest.get("profiles")
    if not isinstance(profiles, list) or not profiles:
        raise SuiteBuildError("manifest profiles must be a non-empty array")
    documents = []
    directories, keys = set(), set()
    fields = {"schema_version", "key", "name", "protocol", "description", "cases", "inputs"}
    for profile in profiles:
        if not isinstance(profile, dict) or set(profile) != fields | {"directory"}:
            raise SuiteBuildError("profile requires directory and the current Suite document fields")
        directory = required_string(profile["directory"], "profile directory", safe=True)
        if directory in {".", ".."} or directory in directories:
            raise SuiteBuildError("duplicate or unsafe suite directory")
        key = required_string(profile["key"], "suite key", safe=True)
        required_string(profile["name"], "suite name")
        if key in keys or profile["schema_version"] != 1 or profile["protocol"] != protocol:
            raise SuiteBuildError("duplicate key or unsupported Suite protocol/format")
        if not isinstance(profile["description"], str) or not isinstance(profile["cases"], list) or not profile["cases"] or not isinstance(profile["inputs"], list):
            raise SuiteBuildError("Suite description, cases and inputs have invalid shapes")
        members = set()
        for ref in profile["cases"]:
            if not isinstance(ref, dict) or set(ref) != {"case_id"} or ref["case_id"] not in cases or ref["case_id"] in members:
                raise SuiteBuildError("Suite requires unique, existing Case ID references")
            members.add(ref["case_id"])
        for item in profile["inputs"]:
            if not isinstance(item, dict) or not isinstance(item.get("bindings"), list):
                raise SuiteBuildError("Suite input requires explicit bindings")
            for binding in item["bindings"]:
                if not isinstance(binding, dict) or set(binding) != {"case_id", "input"} or binding["case_id"] not in members:
                    raise SuiteBuildError("input binding must reference a Suite member")
                spec = cases[binding["case_id"]]["definition"]["spec"]
                if binding["input"] not in spec["inputs"]:
                    raise SuiteBuildError("binding references an undeclared Case input")
        directories.add(directory)
        keys.add(key)
        documents.append((directory, {key: value for key, value in profile.items() if key != "directory"}))
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
        print(f"{directory}: {len(document['cases'])} cases")


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
