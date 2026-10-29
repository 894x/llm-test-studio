import { expect, it } from "vitest"
import { DesktopDataError } from "@/app/data-error"
import { parseHistoryRecyclePreview, parseRecycledHistoryPage, type RecycledHistoryEntry } from "./data"
import { historyVisibility } from "./visibility"

const entry: RecycledHistoryEntry = {
  kind: "job", id: "11111111-1111-4111-8111-111111111111", title: "Saved test", removed_at: "2026-10-30T00:00:00.000000001Z",
  job_ids: ["11111111-1111-4111-8111-111111111111"], run_ids: ["22222222-2222-4222-8222-222222222222"], report_ids: [], comparison_ids: [], remote: true,
}
it("accepts a closed recycled page and only folds authoritative affected IDs", () => {
  const page = { schema_version: 1, entries: [entry], next_cursor: "opaque", has_more: true }
  expect(parseRecycledHistoryPage(page)).toEqual(page)
  const hidden = historyVisibility([{ entry, action: "recycle" }])
  expect([...hidden.jobs]).toEqual(entry.job_ids)
  expect([...hidden.runs]).toEqual(entry.run_ids)
  expect(historyVisibility([{ entry, action: "recycle" }, { entry, action: "restore" }]).jobs.size).toBe(0)
})
it.each([
  { schema_version: 2, entries: [entry], next_cursor: "", has_more: false },
  { schema_version: 1, entries: [entry, entry], next_cursor: "", has_more: false },
  { schema_version: 1, entries: [{ ...entry, removed_at: "" }], next_cursor: "", has_more: false },
  { schema_version: 1, entries: [{ ...entry, job_ids: [] }], next_cursor: "", has_more: false },
  { schema_version: 1, entries: [{ ...entry, remote: "false" }], next_cursor: "", has_more: false },
  { schema_version: 1, entries: [{ ...entry, private_path: "hidden" }], next_cursor: "", has_more: false },
  { schema_version: 1, entries: [entry], next_cursor: "", has_more: true },
])("rejects malformed or inconsistent recycle pages", value => {
  expect(() => parseRecycledHistoryPage(value)).toThrow(DesktopDataError)
})
it("keeps preview decisions authoritative and rejects contradictory actions", () => {
  expect(parseHistoryRecyclePreview({ schema_version: 1, entry: { ...entry, removed_at: "" }, can_recycle: false, blocked_reason: "active" })).toMatchObject({ can_recycle: false, blocked_reason: "active" })
  for (const value of [
    { schema_version: 1, entry, can_recycle: true, blocked_reason: "active" },
    { schema_version: 1, entry, can_recycle: false, blocked_reason: "" },
    { schema_version: 1, entry, can_recycle: false, blocked_reason: "unknown" },
  ]) expect(() => parseHistoryRecyclePreview(value)).toThrow(DesktopDataError)
})

it("preserves supported authored titles with line breaks and long text", () => {
  const title = "A long authored name\n" + "x".repeat(3000)
  expect(parseRecycledHistoryPage({ schema_version: 1, entries: [{ ...entry, title }], next_cursor: "", has_more: false }).entries[0].title).toBe(title)
})
