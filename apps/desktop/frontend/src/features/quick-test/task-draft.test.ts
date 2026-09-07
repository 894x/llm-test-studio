import { describe, expect, it } from "vitest"
import { FIXTURE_CATALOG } from "@/features/runs/fixtures"
import { DesktopDataError } from "@/app/data-error"
import {
  createTaskDraft,
  encodeTaskDraft,
  decodeTaskDraft,
  parseQuickTaskDetail,
  quickTaskCommand,
} from "./task-draft"

const task = {
  ...FIXTURE_CATALOG.suites[0],
  model_target: "",
  quick_test: {
    description: "Connect",
    timeout_ms: 30000,
    inputs: [
      {
        key: "prompt",
        label: "Prompt",
        type: "text" as const,
        default: "hello",
        bindings: [{ case_key: "T001", pointer: "/request/body/messages/0/content" }],
      },
      {
        key: "duration",
        label: "Duration",
        type: "number" as const,
        default: 4,
        bindings: [{ case_key: "T001", pointer: "/request/body/duration" }],
      },
      {
        key: "audio",
        label: "Audio",
        type: "boolean" as const,
        default: false,
        bindings: [{ case_key: "T001", pointer: "/request/body/audio" }],
      },
    ],
  },
}

describe("quick task drafts", () => {
  it("preserves editable fields and pins while never persisting a key", () => {
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
    expect(quickTaskCommand(draft).errors).toHaveProperty("input.duration")
    expect(
      quickTaskCommand({ ...draft, inputs: { ...draft.inputs, duration: "5" } }).command,
    ).toMatchObject({
      source_run_id: draft.source_run_id,
      inputs: { prompt: "edited", duration: 5, audio: true },
    })
  })

  it("restores safe history metadata and rejects inconsistent fields", () => {
    const payload = {
      schema_version: 1,
      run_id: "11111111-1111-4111-8111-111111111111",
      suite: task,
      model: "model",
      base_url: "https://example.test",
      inputs: { prompt: "edited", duration: 5, audio: true },
      api_key: "hidden",
    }
    expect(JSON.stringify(parseQuickTaskDetail(payload))).not.toContain("hidden")
    for (const invalid of [
      { ...payload, inputs: {} },
      { ...payload, inputs: { ...payload.inputs, duration: "5" } },
      { ...payload, base_url: "https://user:key@example.test" },
      { ...payload, run_id: "invalid" },
    ]) {
      expect(() => parseQuickTaskDetail(invalid)).toThrow(DesktopDataError)
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
