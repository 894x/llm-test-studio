#!/usr/bin/env python3
"""Inventory case.json request coverage without claiming contract completeness."""

from __future__ import annotations

import argparse
import json
from collections import Counter
from pathlib import Path
from typing import Any


NEGATIVE_MARKERS = ("error", "reject", "invalid", "failure")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Inventory enabled automatic case.json files and their request parameters."
    )
    parser.add_argument("root", type=Path, help="Case directory to scan recursively")
    parser.add_argument(
        "--model",
        help="Only include cases applicable to this model; empty model_targets apply to all models",
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
    return value


def applicable(case: dict[str, Any], model: str | None) -> bool:
    if model is None:
        return True
    targets = case.get("model_targets") or []
    return not targets or model in targets


def request_body(case: dict[str, Any]) -> dict[str, Any]:
    body = (
        case.get("definition", {})
        .get("spec", {})
        .get("request", {})
        .get("body", {})
    )
    return body if isinstance(body, dict) else {}


def assertion_kind(case: dict[str, Any]) -> str:
    value = case.get("definition", {}).get("spec", {}).get("kind", "")
    return value if isinstance(value, str) else ""


def inventory(args: argparse.Namespace) -> dict[str, Any]:
    if not args.root.is_dir():
        raise ValueError(f"case root is not a directory: {args.root}")

    dimensions: Counter[str] = Counter()
    parameters: Counter[str] = Counter()
    kinds: Counter[str] = Counter()
    selected: list[dict[str, Any]] = []
    all_files = sorted(args.root.rglob("case.json"))

    for path in all_files:
        case = load_case(path)
        if not args.include_disabled and (
            case.get("enabled") is not True
            or case.get("execution_mode") != "automatic"
        ):
            continue
        if not applicable(case, args.model):
            continue

        body = request_body(case)
        kind = assertion_kind(case)
        dimension = str(case.get("dimension") or "unspecified")
        dimensions[dimension] += 1
        parameters.update(body.keys())
        kinds[kind or "unspecified"] += 1
        selected.append(
            {
                "path": path.relative_to(args.root).as_posix(),
                "key": str(case.get("key") or ""),
                "dimension": dimension,
                "kind": kind,
                "parameters": sorted(body.keys()),
                "negative_kind_heuristic": any(
                    marker in kind.lower() for marker in NEGATIVE_MARKERS
                ),
            }
        )

    negative_count = sum(item["negative_kind_heuristic"] for item in selected)
    warnings: list[str] = []
    if selected and negative_count == 0:
        warnings.append("No selected assertion kind looks like a negative/error case.")
    if parameters["max_tokens"] and not parameters["max_completion_tokens"]:
        warnings.append(
            "Selected cases use max_tokens but none use max_completion_tokens; verify current provider documentation."
        )

    return {
        "root": str(args.root.resolve()),
        "model": args.model,
        "case_files": len(all_files),
        "selected_cases": len(selected),
        "negative_kind_heuristic_count": negative_count,
        "dimensions": dict(sorted(dimensions.items())),
        "request_parameters": dict(sorted(parameters.items())),
        "assertion_kinds": dict(sorted(kinds.items())),
        "warnings": warnings,
        "cases": selected,
    }


def markdown(report: dict[str, Any]) -> str:
    lines = [
        "# Case coverage inventory",
        "",
        f"- Root: `{report['root']}`",
        f"- Model filter: `{report['model'] or 'none'}`",
        f"- Case files: {report['case_files']}",
        f"- Selected cases: {report['selected_cases']}",
        f"- Negative assertion kinds (heuristic): {report['negative_kind_heuristic_count']}",
        "",
    ]
    for heading, key in (
        ("Dimensions", "dimensions"),
        ("Request parameters", "request_parameters"),
        ("Assertion kinds", "assertion_kinds"),
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
            "| Path | Key | Dimension | Kind | Parameters | Negative? |",
            "|---|---|---|---|---|---|",
        )
    )
    for case in report["cases"]:
        params = ", ".join(case["parameters"])
        negative = "yes" if case["negative_kind_heuristic"] else "no"
        lines.append(
            f"| `{case['path']}` | `{case['key']}` | `{case['dimension']}` | "
            f"`{case['kind']}` | `{params}` | {negative} |"
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
