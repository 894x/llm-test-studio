"""Filesystem-backed catalog for test cases and model profiles.

The catalog deliberately stores credentials nowhere.  Model profile files are
configuration that is safe to keep in the project; callers must supply API
keys at runtime.
"""

from __future__ import annotations

from dataclasses import dataclass, field
import json
import os
from pathlib import Path
import re
import tempfile
from typing import Any, Iterable, Mapping


CASE_FILENAME = "case.json"
MODEL_SCHEMA_VERSION = 1
SUPPORTED_PROTOCOLS = frozenset({"kimi-k3", "openai-chat", "seedance"})
_CASE_REQUIRED_FIELDS = ("id", "name", "dimension", "protocol", "kind", "request")
_PROFILE_REQUIRED_FIELDS = (
    "schema_version",
    "id",
    "display_name",
    "model",
    "protocol",
    "endpoint",
    "suites",
    "capabilities",
    "enabled",
)
_SAFE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")


class CatalogError(ValueError):
    """Base error for invalid catalog data or ambiguous lookups."""


class CatalogNotFoundError(CatalogError):
    """Raised when a requested case or model profile does not exist."""


class CatalogValidationError(CatalogError):
    """Raised when catalog data does not match its storage contract."""


@dataclass(frozen=True, slots=True)
class ModelProfile:
    """Serializable description of a model and its compatible case suites."""

    schema_version: int
    id: str
    display_name: str
    model: str
    protocol: str
    endpoint: str
    suites: tuple[str, ...]
    capabilities: tuple[str, ...]
    enabled: bool
    extra: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> "ModelProfile":
        data = _validate_profile_mapping(value)
        known = set(_PROFILE_REQUIRED_FIELDS)
        return cls(
            schema_version=data["schema_version"],
            id=data["id"],
            display_name=data["display_name"],
            model=data["model"],
            protocol=data["protocol"],
            endpoint=data["endpoint"],
            suites=tuple(data["suites"]),
            capabilities=tuple(data["capabilities"]),
            enabled=data["enabled"],
            extra={key: item for key, item in data.items() if key not in known},
        )

    def to_dict(self) -> dict[str, Any]:
        data = dict(self.extra)
        data.update(
            {
                "schema_version": self.schema_version,
                "id": self.id,
                "display_name": self.display_name,
                "model": self.model,
                "protocol": self.protocol,
                "endpoint": self.endpoint,
                "suites": list(self.suites),
                "capabilities": list(self.capabilities),
                "enabled": self.enabled,
            }
        )
        return _validate_profile_mapping(data)


class Catalog:
    """Read and atomically update cases and model profiles under a project root."""

    def __init__(self, root: str | os.PathLike[str] | None = None) -> None:
        self.root = Path(root) if root is not None else Path(__file__).resolve().parents[1]
        self.root = self.root.resolve()
        self.cases_dir = self.root / "cases"
        self.models_dir = self.root / "definitions" / "models"

    def list_cases(
        self,
        *,
        protocol: str | None = None,
        dimension: str | None = None,
        kind: str | None = None,
        enabled: bool | None = None,
        default: bool | None = None,
        search: str | None = None,
        **fields: Any,
    ) -> list[dict[str, Any]]:
        """Return validated cases, optionally filtered by metadata fields.

        ``enabled`` is the inverse of the legacy ``disabled`` case field.
        Additional keyword arguments perform exact top-level matches.
        """

        filters = {
            key: value
            for key, value in {
                "protocol": protocol,
                "dimension": dimension,
                "kind": kind,
                "default": default,
                **fields,
            }.items()
            if value is not None
        }
        needle = search.casefold() if search else None
        cases: list[dict[str, Any]] = []
        for path in self._case_paths():
            case = self._read_case_file(path)
            if enabled is not None and (not bool(case.get("disabled", False))) is not enabled:
                continue
            if any(case.get(key) != value for key, value in filters.items()):
                continue
            if needle is not None:
                haystack = f"{case['id']} {case['name']}".casefold()
                if needle not in haystack:
                    continue
            cases.append(case)
        return cases

    def filter_cases(self, **filters: Any) -> list[dict[str, Any]]:
        """Explicit alias for :meth:`list_cases` with filters."""

        return self.list_cases(**filters)

    def read_case(
        self,
        identifier: str | os.PathLike[str],
        case_id: str | None = None,
        *,
        protocol: str | None = None,
    ) -> dict[str, Any]:
        """Read a case by relative path, id, or ``(protocol, case_id)``.

        The two-positional-argument form is convenient for ids that may be
        duplicated across protocol suites: ``read_case("openai-chat", "T001")``.
        """

        if case_id is not None:
            if protocol is not None:
                raise TypeError("protocol must not be supplied in both forms")
            protocol = str(identifier)
            path = self._find_case_path(case_id, protocol=protocol)
        elif _looks_like_path(identifier):
            path = self._resolve_case_path(identifier)
            if not path.is_file():
                raise CatalogNotFoundError(f"case path does not exist: {identifier}")
        else:
            path = self._find_case_path(str(identifier), protocol=protocol)
        return self._read_case_file(path)

    def validate_case(
        self,
        case: Mapping[str, Any],
        *,
        expected_protocol: str | None = None,
    ) -> dict[str, Any]:
        """Validate and return a JSON-safe copy of a case mapping."""

        if not isinstance(case, Mapping):
            raise CatalogValidationError("case must be a mapping")
        missing = [key for key in _CASE_REQUIRED_FIELDS if key not in case]
        if missing:
            raise CatalogValidationError(f"case is missing required fields: {', '.join(missing)}")
        for key in ("id", "name", "dimension", "protocol", "kind"):
            if not isinstance(case[key], str) or not case[key].strip():
                raise CatalogValidationError(f"case.{key} must be a non-empty string")
        if case["protocol"] not in SUPPORTED_PROTOCOLS:
            raise CatalogValidationError(f"unsupported case protocol: {case['protocol']}")
        if expected_protocol is not None and case["protocol"] != expected_protocol:
            raise CatalogValidationError(
                f"case protocol {case['protocol']!r} does not match directory {expected_protocol!r}"
            )
        if not isinstance(case["request"], Mapping):
            raise CatalogValidationError("case.request must be a mapping")
        try:
            return json.loads(json.dumps(dict(case), ensure_ascii=False, allow_nan=False))
        except (TypeError, ValueError) as exc:
            raise CatalogValidationError(f"case must contain JSON-compatible values: {exc}") from exc

    def save_case(
        self,
        case: Mapping[str, Any],
        path: str | os.PathLike[str] | None = None,
    ) -> Path:
        """Atomically save a case without flattening its suite directory.

        Existing cases are located from their protocol and id when ``path`` is
        omitted.  A new case requires an explicit path such as
        ``openai-chat/T044-description/case.json``.
        """

        data = self.validate_case(case)
        if path is None:
            target = self._find_case_path(data["id"], protocol=data["protocol"])
        else:
            target = self._resolve_case_path(path)
            relative = target.relative_to(self.cases_dir)
            if len(relative.parts) < 3:
                raise CatalogValidationError(
                    "case path must preserve <protocol>/<case-directory>/case.json"
                )
            self.validate_case(data, expected_protocol=relative.parts[0])
        _atomic_write_json(target, data)
        return target

    def list_models(
        self,
        *,
        enabled: bool | None = None,
        protocol: str | None = None,
    ) -> list[ModelProfile]:
        """Return validated model profiles sorted by id."""

        profiles = []
        if not self.models_dir.exists():
            return profiles
        for path in sorted(self.models_dir.glob("*.json")):
            profile = self._read_model_file(path)
            if enabled is not None and profile.enabled is not enabled:
                continue
            if protocol is not None and profile.protocol != protocol:
                continue
            profiles.append(profile)
        return sorted(profiles, key=lambda item: item.id)

    def read_model(self, profile_id: str) -> ModelProfile:
        """Read and validate one model profile by id."""

        _validate_safe_id(profile_id, "model profile id")
        path = self.models_dir / f"{profile_id}.json"
        if not path.is_file():
            raise CatalogNotFoundError(f"model profile does not exist: {profile_id}")
        return self._read_model_file(path)

    def save_model(self, profile: ModelProfile | Mapping[str, Any]) -> Path:
        """Validate and atomically save a profile; API keys are always rejected."""

        model_profile = (
            profile if isinstance(profile, ModelProfile) else ModelProfile.from_dict(profile)
        )
        data = model_profile.to_dict()
        _validate_safe_id(model_profile.id, "model profile id")
        target = self.models_dir / f"{model_profile.id}.json"
        _atomic_write_json(target, data)
        return target

    def _case_paths(self) -> Iterable[Path]:
        if not self.cases_dir.exists():
            return ()
        return sorted(self.cases_dir.glob(f"**/{CASE_FILENAME}"))

    def _read_case_file(self, path: Path) -> dict[str, Any]:
        data = _read_json_object(path, "case")
        relative = path.relative_to(self.cases_dir)
        if len(relative.parts) < 3:
            raise CatalogValidationError(
                f"case path must preserve <protocol>/<case-directory>/case.json: {relative}"
            )
        return self.validate_case(data, expected_protocol=relative.parts[0])

    def _find_case_path(self, case_id: str, *, protocol: str | None) -> Path:
        matches = []
        for path in self._case_paths():
            data = self._read_case_file(path)
            if data["id"] == case_id and (protocol is None or data["protocol"] == protocol):
                matches.append(path)
        if not matches:
            scope = f" in protocol {protocol!r}" if protocol else ""
            raise CatalogNotFoundError(f"case id does not exist{scope}: {case_id}")
        if len(matches) > 1:
            raise CatalogError(f"case id is ambiguous; specify protocol or path: {case_id}")
        return matches[0]

    def _resolve_case_path(self, value: str | os.PathLike[str]) -> Path:
        supplied = Path(value)
        if supplied.is_absolute():
            target = supplied.resolve()
        elif supplied.parts and supplied.parts[0] == self.cases_dir.name:
            target = (self.root / supplied).resolve()
        else:
            target = (self.cases_dir / supplied).resolve()
        try:
            target.relative_to(self.cases_dir)
        except ValueError as exc:
            raise CatalogValidationError("case path must stay inside the cases directory") from exc
        if target.name != CASE_FILENAME:
            raise CatalogValidationError(f"case path must end with {CASE_FILENAME}")
        return target

    def _read_model_file(self, path: Path) -> ModelProfile:
        profile = ModelProfile.from_dict(_read_json_object(path, "model profile"))
        if path.stem != profile.id:
            raise CatalogValidationError(
                f"model profile id {profile.id!r} does not match filename {path.name!r}"
            )
        return profile


def _validate_profile_mapping(value: Mapping[str, Any]) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        raise CatalogValidationError("model profile must be a mapping")
    _reject_api_key(value)
    missing = [key for key in _PROFILE_REQUIRED_FIELDS if key not in value]
    if missing:
        raise CatalogValidationError(
            f"model profile is missing required fields: {', '.join(missing)}"
        )
    if isinstance(value["schema_version"], bool) or not isinstance(value["schema_version"], int):
        raise CatalogValidationError("model profile schema_version must be an integer")
    if value["schema_version"] < 1:
        raise CatalogValidationError("model profile schema_version must be positive")
    for key in ("id", "display_name", "model", "protocol"):
        if not isinstance(value[key], str) or not value[key].strip():
            raise CatalogValidationError(f"model profile {key} must be a non-empty string")
    _validate_safe_id(value["id"], "model profile id")
    if value["protocol"] not in SUPPORTED_PROTOCOLS:
        raise CatalogValidationError(f"unsupported model protocol: {value['protocol']}")
    if not isinstance(value["endpoint"], str):
        raise CatalogValidationError("model profile endpoint must be a string")
    for key in ("suites", "capabilities"):
        items = value[key]
        if not isinstance(items, (list, tuple)) or isinstance(items, (str, bytes)):
            raise CatalogValidationError(f"model profile {key} must be a list of strings")
        if any(not isinstance(item, str) or not item.strip() for item in items):
            raise CatalogValidationError(
                f"model profile {key} must contain only non-empty strings"
            )
    if not isinstance(value["enabled"], bool):
        raise CatalogValidationError("model profile enabled must be a boolean")
    try:
        return json.loads(json.dumps(dict(value), ensure_ascii=False, allow_nan=False))
    except (TypeError, ValueError) as exc:
        raise CatalogValidationError(
            f"model profile must contain JSON-compatible values: {exc}"
        ) from exc


def _reject_api_key(value: Any) -> None:
    if isinstance(value, Mapping):
        for key, item in value.items():
            if isinstance(key, str):
                snake_key = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", key)
                normalized = re.sub(r"[^a-z0-9]+", "_", snake_key.lower()).strip("_")
                if normalized in {"api_key", "apikey"}:
                    raise CatalogValidationError("model profiles must never contain api_key")
            _reject_api_key(item)
    elif isinstance(value, (list, tuple)):
        for item in value:
            _reject_api_key(item)


def _validate_safe_id(value: str, label: str) -> None:
    if not isinstance(value, str) or not _SAFE_ID.fullmatch(value):
        raise CatalogValidationError(
            f"{label} must contain only letters, numbers, dots, underscores, or hyphens"
        )


def _looks_like_path(value: str | os.PathLike[str]) -> bool:
    if isinstance(value, os.PathLike):
        return True
    return "/" in value or "\\" in value or value == CASE_FILENAME or value.endswith(".json")


def _read_json_object(path: Path, label: str) -> dict[str, Any]:
    try:
        with path.open("r", encoding="utf-8") as handle:
            value = json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        raise CatalogValidationError(f"unable to read {label} {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise CatalogValidationError(f"{label} must be a JSON object: {path}")
    return value


def _atomic_write_json(path: Path, value: Mapping[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    mode = (path.stat().st_mode & 0o777) if path.exists() else 0o644
    descriptor, temporary_name = tempfile.mkstemp(
        prefix=f".{path.name}.", suffix=".tmp", dir=path.parent
    )
    temporary_path = Path(temporary_name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            json.dump(value, handle, ensure_ascii=False, indent=2, allow_nan=False)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary_path, mode)
        os.replace(temporary_path, path)
    except Exception:
        temporary_path.unlink(missing_ok=True)
        raise
