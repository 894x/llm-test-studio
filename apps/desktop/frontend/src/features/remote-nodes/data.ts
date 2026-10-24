import { DesktopDataError } from "@/app/data-error"

export type ExecutionLocation = { kind: "local" | "remote"; configuration_id: string }
export type RemoteHostKey = { type: string; fingerprint: string }
export type RemoteRunner = {
  node_id: string; installation_id: string; version: string; build_id: string
  os: string; arch: string; ready: boolean; healthy: boolean
  active: number; queued: number; pending: number; max_active_jobs: number; max_concurrency: number
}
export const REMOTE_UPGRADE_STAGES = [
  "uploading", "checking", "pausing", "stopping", "stopped", "backing_up", "backed_up", "cutover", "verifying",
  "resume_attempted", "completed", "rolling_back", "rolled_back",
] as const
export type RemoteUpgradeStage = typeof REMOTE_UPGRADE_STAGES[number]
export const REMOTE_UPGRADE_ERRORS = [
  "runner_release_unavailable", "invalid_runner_release", "runner_release_upgrade_required", "runner_release_integrity_failed",
  "remote_connection_failed", "remote_file_integrity_failed", "installation_root_required", "unsupported_installation_platform",
  "installation_busy", "installation_operation_conflict", "installation_path_not_owned", "node_pairing_repair_required",
  "runner_upgrade_required", "installation_integrity_failed", "invalid_installation_request", "runner_installation_failed",
] as const
export type RemoteNodeUpgradeView = {
  configuration_id: string; node_id: string; installation_id: string; operation_id: string
  stage: RemoteUpgradeStage; error_code: "" | typeof REMOTE_UPGRADE_ERRORS[number]
  expected_build_id: string; target_build_id: string; actual_runner: RemoteRunner | null
  resume_attempted: boolean; restorable: boolean; confirmed: boolean; changed_at: string
}
export type StartRemoteNodeUpgradeCommand = RemoteNodeAction & { operation_id: string; expected_build_id: string }
export type RemoteNodeUpgradeAction = RemoteNodeAction & { operation_id: string }
export const REMOTE_NODE_STATUSES = [
  "unchecked", "disabled", "checking", "host_unverified", "host_changed", "authentication_failed", "offline",
  "unsupported", "installing", "ready", "maintenance", "unhealthy", "pairing_required", "artifact_required",
  "identity_changed", "credential_unavailable", "failed",
] as const
export type RemoteNodeStatus = typeof REMOTE_NODE_STATUSES[number]
export type RemoteNode = {
  id: string; revision: number; name: string; region: string; host: string; port: number; user: string
  enabled: boolean; authentication_kind: "password" | "private_key"
  has_authentication: boolean; has_passphrase: boolean; host_key: RemoteHostKey | null
  proposed_host_key: RemoteHostKey | null; status: RemoteNodeStatus; eligible: boolean; checking: boolean
  checked_at: string | null; runner: RemoteRunner | null; installation_stage: string; installation_error: string
  upgrade: RemoteNodeUpgradeView | null
}
export type RemoteNodesSnapshot = {
  schema_version: 1; default_revision: number; default: ExecutionLocation; nodes: RemoteNode[]
}
export type RemoteNodeDraft = {
  id: string; expected_revision: number; name: string; region: string; host: string; port: number; user: string
  enabled: boolean; authentication: "password" | "private_key"; password: string; private_key: string
  passphrase: string; remove_passphrase: boolean
}
export type RemoteNodeAction = { id: string; expected_revision: number }
export type RemoteNodeTaskConcurrencyCommand = RemoteNodeAction & { expected_max_active_jobs: number; max_active_jobs: number }
export type HostKeyConfirmation = RemoteNodeAction & { key: RemoteHostKey }
export type DefaultExecutionCommand = { expected_revision: number; location: ExecutionLocation }

export const EMPTY_REMOTE_NODES: RemoteNodesSnapshot = {
  schema_version: 1, default_revision: 1, default: { kind: "local", configuration_id: "" }, nodes: [],
}

function invalid(): never { throw new DesktopDataError("Invalid remote node data") }
function record(value: unknown, keys: string[]): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return invalid()
  const result = value as Record<string, unknown>
  if (Object.keys(result).length !== keys.length || keys.some(key => !Object.hasOwn(result, key))) return invalid()
  return result
}
function text(value: unknown, max: number, empty = false): string {
  if (typeof value !== "string" || (!empty && !value) || value.trim() !== value ||
      new TextEncoder().encode(value).length > max || /[\p{Cc}\p{Cs}]/u.test(value)) return invalid()
  return value
}
function integer(value: unknown, min = 0, max = Number.MAX_SAFE_INTEGER): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < min || value > max) return invalid()
  return value
}
function bool(value: unknown): boolean { return typeof value === "boolean" ? value : invalid() }
function uuid(value: unknown): string {
  const result = text(value, 36)
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(result) ? result : invalid()
}
function nullable<T>(value: unknown, parse: (value: unknown) => T): T | null {
  return value === null ? null : parse(value)
}
export function parseExecutionLocation(value: unknown): ExecutionLocation {
  const item = record(value, ["kind", "configuration_id"])
  if (item.kind === "local" && item.configuration_id === "") return { kind: "local", configuration_id: "" }
  if (item.kind === "remote") return { kind: "remote", configuration_id: uuid(item.configuration_id) }
  return invalid()
}
function hostKey(value: unknown): RemoteHostKey {
  const item = record(value, ["type", "fingerprint"])
  const fingerprint = text(item.fingerprint, 50)
  if (!/^SHA256:[A-Za-z0-9+/]{43}$/.test(fingerprint)) return invalid()
  return { type: text(item.type, 64), fingerprint }
}
function runner(value: unknown): RemoteRunner {
  const item = record(value, ["node_id", "installation_id", "version", "build_id", "os", "arch", "ready", "healthy",
    "active", "queued", "pending", "max_active_jobs", "max_concurrency"])
  const buildID = text(item.build_id, 64)
  if (!/^[0-9a-f]{64}$/.test(buildID)) return invalid()
  return { node_id: uuid(item.node_id), installation_id: uuid(item.installation_id),
    version: text(item.version, 128), build_id: buildID, os: text(item.os, 32), arch: text(item.arch, 32),
    ready: bool(item.ready), healthy: bool(item.healthy), active: integer(item.active, 0, 0xffff_ffff),
    queued: integer(item.queued, 0, 0xffff_ffff), pending: integer(item.pending, 0, 0xffff_ffff),
    max_active_jobs: integer(item.max_active_jobs, 1, 0xffff_ffff),
    max_concurrency: integer(item.max_concurrency, 1, 0xffff_ffff) }
}
export function parseRemoteNodeUpgrade(value: unknown): RemoteNodeUpgradeView {
  const item = record(value, ["configuration_id", "node_id", "installation_id", "operation_id", "stage", "error_code",
    "expected_build_id", "target_build_id", "actual_runner", "resume_attempted", "restorable", "confirmed", "changed_at"])
  if (!REMOTE_UPGRADE_STAGES.includes(item.stage as RemoteUpgradeStage) ||
    (item.error_code !== "" && !REMOTE_UPGRADE_ERRORS.includes(item.error_code as typeof REMOTE_UPGRADE_ERRORS[number]))) return invalid()
  const build = (value: unknown) => {
    const result = text(value, 64)
    return /^[0-9a-f]{64}$/.test(result) ? result : invalid()
  }
  const changedAt = text(item.changed_at, 40)
  const milliseconds = Date.parse(changedAt)
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(changedAt) || !Number.isFinite(milliseconds) ||
    new Date(milliseconds).toISOString().slice(0, 19) !== changedAt.slice(0, 19)) return invalid()
  const actual = nullable(item.actual_runner, runner)
  const nodeID = uuid(item.node_id)
  const installationID = uuid(item.installation_id)
  if (actual && (actual.node_id !== nodeID || actual.installation_id !== installationID)) return invalid()
  return { configuration_id: uuid(item.configuration_id), node_id: nodeID, installation_id: installationID,
    operation_id: uuid(item.operation_id), stage: item.stage as RemoteUpgradeStage,
    error_code: item.error_code as RemoteNodeUpgradeView["error_code"], expected_build_id: build(item.expected_build_id),
    target_build_id: build(item.target_build_id), actual_runner: actual, resume_attempted: bool(item.resume_attempted),
    restorable: bool(item.restorable), confirmed: bool(item.confirmed), changed_at: changedAt }
}
export function parseNullableRemoteNodeUpgrade(value: unknown): RemoteNodeUpgradeView | null {
  return nullable(value, parseRemoteNodeUpgrade)
}
function node(value: unknown): RemoteNode {
  const item = record(value, ["id", "revision", "name", "region", "host", "port", "user", "enabled",
    "authentication_kind", "has_authentication", "has_passphrase", "host_key", "proposed_host_key", "status",
    "eligible", "checking", "checked_at", "runner", "installation_stage", "installation_error", "upgrade"])
  if (item.authentication_kind !== "password" && item.authentication_kind !== "private_key") return invalid()
  if (!REMOTE_NODE_STATUSES.includes(item.status as RemoteNodeStatus)) return invalid()
  const checkedAt = nullable(item.checked_at, value => text(value, 40))
  if (checkedAt !== null && !Number.isFinite(Date.parse(checkedAt))) return invalid()
  const id = uuid(item.id)
  const upgrade = parseNullableRemoteNodeUpgrade(item.upgrade)
  if (upgrade && upgrade.configuration_id !== id) return invalid()
  return { id, revision: integer(item.revision, 1), name: text(item.name, 256),
    region: text(item.region, 128, true), host: text(item.host, 253), port: integer(item.port, 1, 65535),
    user: text(item.user, 64), enabled: bool(item.enabled), authentication_kind: item.authentication_kind,
    has_authentication: bool(item.has_authentication), has_passphrase: bool(item.has_passphrase),
    host_key: nullable(item.host_key, hostKey), proposed_host_key: nullable(item.proposed_host_key, hostKey),
    status: item.status as RemoteNodeStatus, eligible: bool(item.eligible), checking: bool(item.checking),
    checked_at: checkedAt, runner: nullable(item.runner, runner),
    installation_stage: text(item.installation_stage, 64, true), installation_error: text(item.installation_error, 128, true), upgrade }
}
export function parseRemoteNodes(value: unknown): RemoteNodesSnapshot {
  const item = record(value, ["schema_version", "default_revision", "default", "nodes"])
  if (item.schema_version !== 1 || !Array.isArray(item.nodes) || item.nodes.length > 64) return invalid()
  const nodes = item.nodes.map(node)
  if (new Set(nodes.map(node => node.id)).size !== nodes.length) return invalid()
  const location = parseExecutionLocation(item.default)
  if (location.kind === "remote" && !nodes.some(node => node.id === location.configuration_id)) return invalid()
  return { schema_version: 1, default_revision: integer(item.default_revision, 1), default: location, nodes }
}
