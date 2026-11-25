import { render, screen } from "@testing-library/react"
import { I18nextProvider } from "react-i18next"
import { expect, it } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import { protocolReportFixture } from "@/test/protocol-report-fixture"
import { EntryCaseTable } from "./entry-case-table"
import { kimiTestFocus } from "./kimi-test-focus"

it("limits review annotations to the requested models, protocols and requirements", () => {
  const item = { key: "kimi.cache.headers.write_1h", protocol: "openai-chat" as const, dimension: "cache" }
  for (const model of ["kimi-k3", "kimi-k3.1-pro", "Kimi K3.1 Pro"]) expect(kimiTestFocus(item, model)).toBe("ttl")
  for (const model of ["kimi-k3.1-pro-highspeed", "kimi-k2", "gpt-4.1", ""]) expect(kimiTestFocus(item, model)).toBeUndefined()
  expect(kimiTestFocus({ ...item, protocol: "anthropic-messages" }, "kimi-k3")).toBeUndefined()
  expect(kimiTestFocus({ ...item, key: "k31.must.video_url_object", dimension: "multimodal" }, "kimi-k3.1-pro")).toBe("video")
  expect(kimiTestFocus({ ...item, key: "k31.must.image_url_object", dimension: "multimodal" }, "kimi-k3.1-pro")).toBeUndefined()
  expect(kimiTestFocus({ ...item, key: "kvv.prompt_tokens_assistant_hello.stream", dimension: "compatibility" }, "kimi-k3")).toBe("usage")
})

it("marks result rows without changing verdicts and removes annotations in export rendering", () => {
  const detail = protocolReportFixture("failed")
  detail.entries[0].cases[0].key = "kimi.cache.headers.write_1h"
  const original = structuredClone(detail)
  const i18n = createAppI18n("zh-CN")
  const view = render(<I18nextProvider i18n={i18n}><EntryCaseTable entry={detail.entries[0]} model="kimi-k3" /></I18nextProvider>)
  const row = () => screen.getByRole("button", { name: "Reject invalid parameter" }).closest("tr")!
  expect(row()).toHaveAttribute("data-kimi-test-focus", "ttl")
  expect(row()).toHaveAttribute("title", expect.stringContaining("6 分钟"))
  expect(row()).toHaveTextContent("失败")
  view.rerender(<I18nextProvider i18n={i18n}><EntryCaseTable entry={detail.entries[0]} model="kimi-k3" exporting paginate={false} /></I18nextProvider>)
  expect(row()).not.toHaveAttribute("data-kimi-test-focus")
  expect(row()).not.toHaveAttribute("title")
  expect(detail).toEqual(original)
})
