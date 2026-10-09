#!/usr/bin/env python3
"""Inventory case.json request coverage without claiming contract completeness."""

from __future__ import annotations

import argparse
import json
from collections import Counter
from pathlib import Path
from typing import Any




def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Inventory enabled automatic case.json files and their request parameters."
    )
    parser.add_argument("root", type=Path, help="Case directory to scan recursively")
    parser.add_argument(
        "--key-prefix",
        help="Only include current Case keys starting with this prefix; the Run owns model binding",
    )
    parser.add_argument(
        "--include-disabled",
        action="store_true",
        help="Include disabled and non-automatic cases",
    )
    parser.add_argument(
        "--format", choices=("markdown", "json"), default="markdown"
    )
    return parser.parse_args()


def load_case(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"{path}: {exc}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"{path}: top-level JSON value must be an object")
    definitions = value.get("definitions")
    if value.get("schema_version") != 2 or "definition" in value or "protocol" in value or "model_targets" in value:
        raise ValueError(f"{path}: expected current Case schema_version 2 with id and definitions")
    if not isinstance(value.get("id"), str) or not isinstance(definitions, dict) or not definitions:
        raise ValueError(f"{path}: expected id and nonempty protocol-keyed definitions")
    for protocol, spec in definitions.items():
        if not isinstance(protocol, str) or not isinstance(spec, dict):
            raise ValueError(f"{path}: invalid protocol definition")
        if "kind" in spec or not isinstance(spec.get("inputs"), dict) or not isinstance(spec.get("assertions"), list):
            raise ValueError(f"{path}: {protocol}: expected explicit current inputs and assertions")
    return value


def request_body(case: dict[str, Any]) -> dict[str, Any]:
    result = {}
    for spec in case["definitions"].values():
        body = spec.get("request", {}).get("body", {})
        if isinstance(body, dict):
            result.update(body)
    return result



def assertions(case: dict[str, Any]) -> list[dict[str, Any]]:
    result = []
    def collect(items: list[dict[str, Any]]) -> None:
        for item in items:
            result.append(item)
            for group in ("all", "any", "each"):
                collect(item.get(group, []))
    for spec in case["definitions"].values():
        collect(spec["assertions"])
    return result


def rejects_http(items: list[dict[str, Any]]) -> bool:
    for item in items:
        if item.get("source") != "http.status":
            continue
        values = [item.get("value")] if item.get("operator") == "equals" else item.get("value", []) if item.get("operator") == "in" else []
        if isinstance(values, list) and values and all(isinstance(value, int) and 400 <= value < 500 for value in values):
            return True
    return False


def inventory(args: argparse.Namespace) -> dict[str, Any]:
    if not args.root.is_dir():
        raise ValueError(f"case root is not a directory: {args.root}")

    dimensions: Counter[str] = Counter()
    parameters: Counter[str] = Counter()
    operators: Counter[str] = Counter()
    selected: list[dict[str, Any]] = []
    all_files = sorted(args.root.rglob("case.json"))

    for path in all_files:
        case = load_case(path)
        if not args.include_disabled and (
            case.get("enabled") is not True
            or case.get("execution_mode") != "automatic"
        ):
            continue
        if args.key_prefix and not str(case.get("key", "")).startswith(args.key_prefix):
            continue

        body = request_body(case)
        rules = assertions(case)
        dimension = str(case.get("dimension") or "unspecified")
        dimensions[dimension] += 1
        parameters.update(body.keys())
        operators.update(rule["operator"] for rule in rules if "operator" in rule)
        selected.append(
            {
                "path": path.relative_to(args.root).as_posix(),
                "key": str(case.get("key") or ""),
                "dimension": dimension,
                "operators": sorted({rule["operator"] for rule in rules if "operator" in rule}),
                "parameters": sorted(body.keys()),
                "http_rejection_assertion": rejects_http(rules),
            }
        )

    negative_count = sum(item["http_rejection_assertion"] for item in selected)
    warnings: list[str] = []
    if selected and negative_count == 0:
        warnings.append("No selected case explicitly asserts a 4xx HTTP rejection; review admission and task failure coverage separately.")
    if parameters["max_tokens"] and not parameters["max_completion_tokens"]:
        warnings.append(
            "Selected cases use max_tokens but none use max_completion_tokens; verify current provider documentation."
        )

    return {
        "root": str(args.root.resolve()),
        "key_prefix": args.key_prefix,
        "case_files": len(all_files),
        "selected_cases": len(selected),
        "http_rejection_assertion_count": negative_count,
        "dimensions": dict(sorted(dimensions.items())),
        "request_parameters": dict(sorted(parameters.items())),
        "assertion_operators": dict(sorted(operators.items())),
        "warnings": warnings,
        "cases": selected,
    }


def markdown(report: dict[str, Any]) -> str:
    lines = [
        "# Case coverage inventory",
        "",
        f"- Root: `{report['root']}`",
        f"- Case key prefix: `{report['key_prefix'] or 'none'}`",
        f"- Case files: {report['case_files']}",
        f"- Selected cases: {report['selected_cases']}",
        f"- Cases asserting 4xx HTTP rejection: {report['http_rejection_assertion_count']}",
        "",
    ]
    for heading, key in (
        ("Dimensions", "dimensions"),
        ("Request parameters", "request_parameters"),
        ("Assertion operators", "assertion_operators"),
    ):
        lines.extend((f"## {heading}", ""))
        values = report[key]
        if values:
            lines.extend(f"- `{name}`: {count}" for name, count in values.items())
        else:
            lines.append("- None")
        lines.append("")

    if report["warnings"]:
        lines.extend(("## Warnings", ""))
        lines.extend(f"- {warning}" for warning in report["warnings"])
        lines.append("")

    lines.extend(
        (
            "## Selected cases",
            "",
            "| Path | Key | Dimension | Operators | Parameters | HTTP rejection? |",
            "|---|---|---|---|---|---|",
        )
    )
    for case in report["cases"]:
        params = ", ".join(case["parameters"])
        negative = "yes" if case["http_rejection_assertion"] else "no"
        lines.append(
            f"| `{case['path']}` | `{case['key']}` | `{case['dimension']}` | "
            f"`{', '.join(case['operators'])}` | `{params}` | {negative} |"
        )
    lines.extend(
        (
            "",
            "> This is a structural inventory, not proof of provider-contract coverage.",
        )
    )
    return "\n".join(lines)


def main() -> int:
    args = parse_args()
    try:
        report = inventory(args)
    except ValueError as exc:
        raise SystemExit(str(exc)) from exc
    if args.format == "json":
        print(json.dumps(report, ensure_ascii=False, indent=2))
    else:
        print(markdown(report))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
