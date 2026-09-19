import { DesktopDataError } from "@/app/data-error"
import { parseSuite, isUUID, isJSONValue, type CatalogSuite } from "@/features/catalog/data"
import type { StartQuickTaskCommand } from "@/features/runs/data"
import { translateDesktop as tx } from "@/i18n/runtime"
import { connectionURLHint } from "./connection-url"

export const TASK_DRAFT_KEY = "llm-test-studio.quick-task-draft.v1"
export type TaskDraft = {
  task: CatalogSuite | null
  model: string
  base_url: string
  channel_id: string
  api_key: string
  seed: number
  request_timeout_ms: number
  inputs: Record<string, string | boolean>
  source_run_id?: string
  credential_run_id?: string
}

export type QuickTaskDetail = {
  schema_version: 1
  run_id: string
  suite: CatalogSuite
  model: string
  base_url: string
  channel_id?: string
  credential_run_id?: string
  seed: number
  request_timeout_ms: number
  inputs: StartQuickTaskCommand["inputs"]
}

export type RememberQuickTaskCredentialCommand = {
  run_id: string
  base_url: string
  protocol: CatalogSuite["protocol"]
  api_key: string
}

export function createTaskDraft(task: CatalogSuite | null): TaskDraft {
  return {
    task,
    model: "", seed: 1, request_timeout_ms: 60000,
    base_url: "",
    channel_id: "",
    api_key: "",
    inputs: Object.fromEntries(
      (task?.inputs ?? []).map((input) => [
        input.key,
        typeof input.default === "boolean" ? input.default : input.default === undefined ? "" : typeof input.default === "string" ? input.default : JSON.stringify(input.default),
      ]),
    ),
  }
}

export function quickTaskCommand(draft: TaskDraft): {
  command?: StartQuickTaskCommand
  errors: Record<string, string>
} {
  const errors: Record<string, string> = {}
  const task = draft.task
  if (!task) errors.task = tx("quickTest:task.choose")
  if (!draft.channel_id) {
    if (!draft.base_url.trim()) errors.base_url = tx("desktop:quick-test_enter_an_endpoint")
    else if (connectionURLHint(draft.base_url.trim(), "base_url"))
      errors.base_url = connectionURLHint(draft.base_url.trim(), "base_url")!
    if (
      !draft.credential_run_id &&
      (!draft.api_key.trim() || draft.api_key.length > 16384 || /\p{Cc}/u.test(draft.api_key))
    )
      errors.api_key = tx("desktop:quick-test_enter_an_api_key_or_select_a_channel_with_saved")
  }
  if (!draft.model.trim() || draft.model.trim().length > 256 || /\p{Cc}/u.test(draft.model))
    errors.model = tx("desktop:quick-test_enter_a_model_id")
  const inputs: StartQuickTaskCommand["inputs"] = {}
  if (!Number.isSafeInteger(draft.seed) || draft.seed < 0) errors.seed = tx("quickTest:task.numberRequired")
  if (!Number.isSafeInteger(draft.request_timeout_ms) || draft.request_timeout_ms < 1) errors.request_timeout_ms = tx("quickTest:task.numberRequired")
  for (const input of task?.inputs ?? []) {
    const value = draft.inputs[input.key]
    if (value === "" || value === undefined) {
      if (input.required && input.default === undefined) errors[`input.${input.key}`] = tx("quickTest:task.valueRequired")
      continue
    }
    if (input.type === "number" || input.type === "integer") {
      const numeric = Number(value)
      if (typeof value !== "string" || !Number.isFinite(numeric) || (input.type === "integer" && !Number.isSafeInteger(numeric))) errors[`input.${input.key}`] = tx("quickTest:task.numberRequired")
      else inputs[input.key] = numeric
    } else if ((input.type === "string" && typeof value === "string") || (input.type === "boolean" && typeof value === "boolean")) inputs[input.key] = value
    else if (input.type === "object" || input.type === "array") {
      try { const parsed: unknown = JSON.parse(String(value)); if (!isJSONValue(parsed) || (input.type === "array" ? !Array.isArray(parsed) : !record(parsed))) throw new Error(); inputs[input.key] = parsed }
      catch { errors[`input.${input.key}`] = tx("quickTest:task.valueRequired") }
    } else errors[`input.${input.key}`] = tx("quickTest:task.valueRequired")
  }
  if (!task || Object.keys(errors).length) return { errors }
  return {
    errors,
    command: {
      suite_id: task.id,
      seed: draft.seed, request_timeout_ms: draft.request_timeout_ms,
      model: draft.model.trim(),
      inputs,
      ...(draft.source_run_id ? { source_run_id: draft.source_run_id } : {}),
      ...(draft.channel_id
        ? { channel_id: draft.channel_id }
        : {
            base_url: draft.base_url.trim(),
            ...(draft.credential_run_id
              ? { credential_run_id: draft.credential_run_id }
              : { api_key: draft.api_key.trim() }),
          }),
    },
  }
}

export function encodeTaskDraft(draft: TaskDraft): string {
  // Deliberately project fields instead of serializing the form with its key.
  const baseURL = connectionURLHint(draft.base_url.trim(), "base_url") ? "" : draft.base_url.trim()
  return JSON.stringify({
    schema_version: 1,
    task: draft.task,
    model: draft.model,
    base_url: baseURL,
    channel_id: draft.channel_id,
    inputs: draft.inputs, seed: draft.seed, request_timeout_ms: draft.request_timeout_ms,
    ...(draft.credential_run_id && baseURL ? { credential_run_id: draft.credential_run_id } : {}),
    ...(draft.source_run_id ? { source_run_id: draft.source_run_id } : {}),
  })
}

export function decodeTaskDraft(raw: string | null): TaskDraft | null {
  try {
    if (!raw || raw.length > 1_000_000) return null
    const value: unknown = JSON.parse(raw)
    if (
      !record(value) ||
      value.schema_version !== 1 ||
      typeof value.model !== "string" ||
      typeof value.base_url !== "string" ||
      typeof value.channel_id !== "string" ||
      (value.channel_id && !isUUID(value.channel_id)) ||
      (value.source_run_id !== undefined && !isUUID(value.source_run_id)) ||
      (value.credential_run_id !== undefined &&
        (!isUUID(value.credential_run_id) || !!value.channel_id))
    )
      return null
    if (!Number.isSafeInteger(value.seed) || (value.seed as number) < 0 || !Number.isSafeInteger(value.request_timeout_ms) || (value.request_timeout_ms as number) < 1) return null
    const task = value.task === null ? null : parseSuite(value.task)
    if (
      !record(value.inputs) ||
      Object.keys(value.inputs).length !== (task?.inputs.length ?? 0)
    )
      return null
    const inputs: TaskDraft["inputs"] = {}
    for (const input of task?.inputs ?? []) {
      const item = value.inputs[input.key]
      if (typeof item !== (input.type === "boolean" ? "boolean" : "string")) return null
      inputs[input.key] = item as string | boolean
    }
    return {
      task,
      model: value.model,
      base_url: value.base_url,
      channel_id: value.channel_id,
      api_key: "", seed: value.seed as number, request_timeout_ms: value.request_timeout_ms as number,
      inputs,
      ...(typeof value.source_run_id === "string" ? { source_run_id: value.source_run_id } : {}),
      ...(typeof value.credential_run_id === "string"
        ? { credential_run_id: value.credential_run_id }
        : {}),
    }
  } catch {
    return null
  }
}

export function parseQuickTaskDetail(value: unknown): QuickTaskDetail {
  const invalid = () => new DesktopDataError(tx("quickTest:task.invalidHistory"))
  if (
    !record(value) ||
    value.schema_version !== 1 ||
    !isUUID(value.run_id) ||
    typeof value.model !== "string" ||
    !value.model.trim() ||
    typeof value.base_url !== "string" ||
    connectionURLHint(value.base_url, "base_url") ||
    (value.channel_id !== undefined && !isUUID(value.channel_id)) ||
    (value.credential_run_id !== undefined &&
      (!isUUID(value.credential_run_id) || !!value.channel_id)) ||
    !record(value.inputs)
  )
    throw invalid()
  const suite = parseSuite(value.suite)
  if (!Number.isSafeInteger(value.seed) || (value.seed as number) < 0 || !Number.isSafeInteger(value.request_timeout_ms) || (value.request_timeout_ms as number) < 1) throw invalid()
  const inputs: QuickTaskDetail["inputs"] = {}
  for (const [key, item] of Object.entries(value.inputs)) {
    const definition = suite.inputs.find(input => input.key === key)
    if (!definition || !isJSONValue(item) ||
      (definition.type === "string" && typeof item !== "string") || (definition.type === "boolean" && typeof item !== "boolean") ||
      ((definition.type === "integer" || definition.type === "number") && typeof item !== "number") ||
      (definition.type === "integer" && !Number.isSafeInteger(item)) ||
      (definition.type === "array" && !Array.isArray(item)) || (definition.type === "object" && !record(item))) throw invalid()
    inputs[key] = item
  }
  return {
    schema_version: 1,
    run_id: value.run_id,
    suite,
    model: value.model, seed: value.seed as number, request_timeout_ms: value.request_timeout_ms as number,
    base_url: value.base_url,
    inputs,
    ...(typeof value.channel_id === "string" ? { channel_id: value.channel_id } : {}),
    ...(typeof value.credential_run_id === "string"
      ? { credential_run_id: value.credential_run_id }
      : {}),
  }
}

export function restoreTaskDraft(detail: QuickTaskDetail, previous: TaskDraft): TaskDraft {
  return {
    task: detail.suite,
    model: detail.model, seed: detail.seed, request_timeout_ms: detail.request_timeout_ms,
    base_url: detail.base_url,
    channel_id: detail.channel_id ?? "",
    source_run_id: detail.run_id,
    ...(detail.credential_run_id ? { credential_run_id: detail.credential_run_id } : {}),
    api_key:
      !detail.channel_id &&
      !detail.credential_run_id &&
      previous.task?.protocol === detail.suite.protocol &&
      previous.base_url === detail.base_url
        ? previous.api_key
        : "",
    inputs: Object.fromEntries(
      Object.entries(detail.inputs).map(([key, value]) => [
        key,
        typeof value === "boolean" ? value : typeof value === "string" ? value : JSON.stringify(value),
      ]),
    ),
  }
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
