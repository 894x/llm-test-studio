import { describe, expect, it } from "vitest"
import { FIXTURE_CATALOG } from "@/features/runs/fixtures"
import { DesktopDataError } from "@/app/data-error"
import { FILE_SUITE, FILE_SELECTION } from "@/test/local-file-fixture"
import {
  createTaskDraft,
  encodeTaskDraft,
  decodeTaskDraft,
  parseQuickTaskDetail,
  quickTaskCommand,
} from "./task-draft"

const task = {
  ...FIXTURE_CATALOG.suites[0],
    description: "Connect",
    inputs: [
      {
        key: "prompt",
        label: "Prompt",
        type: "string" as const,
        default: "hello",
        bindings: [{ case_id: FIXTURE_CATALOG.test_cases[0].id, input: "prompt" }],
      },
      {
        key: "duration",
        label: "Duration",
        type: "number" as const,
        default: 4,
        bindings: [{ case_id: FIXTURE_CATALOG.test_cases[0].id, input: "duration" }],
      },
      {
        key: "audio",
        label: "Audio",
        type: "boolean" as const,
        default: false,
        bindings: [{ case_id: FIXTURE_CATALOG.test_cases[0].id, input: "audio" }],
      },
    ],
}

describe("quick task drafts", () => {
  it("keeps file selections out of drafts and rejects every file-task command", () => {
    const draft = { ...createTaskDraft(FILE_SUITE), base_url: "https://example.test", api_key: "key", model: "model" }
    expect(draft.inputs).toEqual({})
    const poisoned = { ...draft, inputs: { image: FILE_SELECTION.selection_id } }
    const encoded = encodeTaskDraft(poisoned)
    expect(encoded).not.toContain(FILE_SELECTION.selection_id)
    expect(JSON.parse(encoded).inputs).toEqual({})
    expect(decodeTaskDraft(encoded)?.inputs).toEqual({})
    const checked = quickTaskCommand(poisoned)
    expect(checked.command).toBeUndefined()
    expect(checked.errors["input.image"]).toContain("暂不可运行")
    expect(decodeTaskDraft(JSON.stringify({ ...JSON.parse(encoded), inputs: poisoned.inputs }))).toBeNull()
    expect(() => parseQuickTaskDetail({ schema_version: 1, run_id: FILE_SELECTION.selection_id,
      suite: FILE_SUITE, model: "model", base_url: "https://example.test", seed: 1, request_timeout_ms: 60000,
      case_concurrency: 4, inputs: { image: { asset_ref: FILE_SELECTION.selection_id } } })).toThrow()
  })
  it("preserves editable fields and root random context while never persisting a key", () => {
    const draft = {
      ...createTaskDraft(task),
      base_url: "https://example.test",
      api_key: "private-test-key",
      model: "model",
      source_run_id: "11111111-1111-4111-8111-111111111111",
      inputs: { prompt: "edited", duration: "", audio: true },
    }
    const encoded = encodeTaskDraft(draft)
    expect(encoded).not.toContain("private-test-key")
    expect(decodeTaskDraft(encoded)).toEqual({ ...draft, api_key: "" })
    expect(quickTaskCommand(draft).command?.inputs).not.toHaveProperty("duration")
    expect(
      quickTaskCommand({ ...draft, inputs: { ...draft.inputs, duration: "5" } }).command,
    ).toMatchObject({
      source_run_id: draft.source_run_id,
      inputs: { prompt: "edited", duration: 5, audio: true },
    })
  })

  it("restores safe history metadata and rejects inconsistent fields", () => {
    const payload = {
      schema_version: 1, seed: 1, request_timeout_ms: 60000, case_concurrency: 4,
      run_id: "11111111-1111-4111-8111-111111111111",
      suite: task,
      model: "model",
      base_url: "https://example.test",
      inputs: { prompt: "edited", duration: 5, audio: true },
      api_key: "hidden",
    }
    expect(JSON.stringify(parseQuickTaskDetail(payload))).not.toContain("hidden")
    for (const invalid of [
      { ...payload, inputs: { ...payload.inputs, duration: "5" } },
      { ...payload, base_url: "https://user:key@example.test" },
      { ...payload, run_id: "invalid" },
      { ...payload, case_concurrency: undefined },
      { ...payload, case_concurrency: 0 },
      { ...payload, case_concurrency: 9 },
      { ...payload, case_concurrency: 1.5 },
      { ...payload, case_concurrency: "4" },
    ]) {
      expect(() => parseQuickTaskDetail(invalid)).toThrow(DesktopDataError)
    }
  })
  it("defaults to four concurrent cases and validates overrides", () => {
    const draft = {
      ...createTaskDraft(task),
      base_url: "https://example.test",
      api_key: "private-test-key",
      model: "model",
    }
    expect(quickTaskCommand(draft).command?.case_concurrency).toBe(4)
    for (const concurrency of [1, 2, 3, 4, 8]) {
      expect(quickTaskCommand(draft, concurrency).command?.case_concurrency).toBe(concurrency)
    }
    for (const concurrency of [0, -1, 1.5, 9, Number.NaN]) {
      expect(quickTaskCommand(draft, concurrency).command).toBeUndefined()
    }
  })
  it("never persists credentials embedded in rejected URLs", () => {
    for (const base_url of [
      "https://user:private-secret@example.test",
      "https://example.test?api_key=private-secret",
      "https://example.test#private-secret",
      "https://user:private-secret@",
      "not a url private-secret",
    ]) {
      const draft = { ...createTaskDraft(task), base_url }
      expect(encodeTaskDraft(draft)).not.toContain("private-secret")
      expect(draft.base_url).toBe(base_url)
    }
  })
  it("retains only a remembered credential reference and rejects malformed references", () => {
    const credential_run_id = "123e4567-e89b-42d3-a456-426614174099"
    const draft = {
      ...createTaskDraft(task),
      base_url: "https://example.test",
      model: "model",
      credential_run_id,
    }
    expect(decodeTaskDraft(encodeTaskDraft(draft))).toEqual(draft)
    expect(quickTaskCommand(draft).command).toMatchObject({ credential_run_id })
    expect(quickTaskCommand(draft).command).not.toHaveProperty("api_key")
    expect(
      decodeTaskDraft(
        JSON.stringify({ ...JSON.parse(encodeTaskDraft(draft)), credential_run_id: "bad" }),
      ),
    ).toBeNull()
  })
})
