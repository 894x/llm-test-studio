import { describe, expect, it } from "vitest"
import { connectionURLHint, resolveConnectionURL, taskConnectionURLs } from "./connection-url"
import { FIXTURE_CATALOG } from "@/features/runs/fixtures"

describe("connectionURLHint", () => {
  it("accepts remote HTTP base and full URLs", () => {
    expect(connectionURLHint("http://api.example.test/v1", "base_url")).toBeUndefined()
    expect(
      connectionURLHint(
        "http://api.example.test/v1/chat/completions",
        "full_url",
      ),
    ).toBeUndefined()
  })

  it("continues to reject unsupported URL schemes", () => {
    expect(connectionURLHint("ftp://api.example.test/v1", "base_url")).toBeDefined()
  })
})

describe("connection path completion", () => {
  it.each([
    ["", "/v1/chat/completions"],
    ["/", "v1/chat/completions"],
    ["/v1", "/chat/completions"],
    ["/v1/", "chat/completions"],
    ["/v1/ch", "at/completions"],
    ["/v1/chat/completions", ""],
    ["/v1/chat/completions/", ""],
  ])("completes %s without duplicating the entered route", (path, suffix) => {
    const resolved = resolveConnectionURL(`https://example.test${path}`, "/v1/chat/completions")
    expect(resolved).toEqual({ endpoint: "https://example.test/v1/chat/completions", suffix })
  })

  it("preserves a gateway prefix and a complete versionless endpoint", () => {
    expect(resolveConnectionURL("https://example.test/proxy/v1/", "/v1/chat/completions"))
      .toEqual({ endpoint: "https://example.test/proxy/v1/chat/completions", suffix: "chat/completions" })
    expect(resolveConnectionURL("https://example.test/proxy/chat/completions", "/v1/chat/completions"))
      .toEqual({ endpoint: "https://example.test/proxy/chat/completions", suffix: "" })
  })

  it("previews each distinct operation of the selected Suite", () => {
    const cases = FIXTURE_CATALOG.test_cases.slice(0, 2).map((testCase, index) => ({
      ...testCase, definitions: { "openai-chat": { ...testCase.definitions["openai-chat"], operation: index ? "models.list" : "" } },
    }))
    const catalog = { ...FIXTURE_CATALOG, test_cases: cases }
    const task = { ...catalog.suites[0], protocol: "openai-chat" as const, cases: cases.map(({ id }) => ({ case_id: id })) }
    expect(taskConnectionURLs("https://example.test/v1", task, catalog).map(({ endpoint }) => endpoint))
      .toEqual(["https://example.test/v1/chat/completions", "https://example.test/v1/models"])
  })

  it("does not display a completion for unsafe addresses", () => {
    for (const address of ["", "ftp://example.test", "https://user:key@example.test", "https://example.test?", "https://example.test#"]) {
      expect(resolveConnectionURL(address, "/v1/chat/completions")).toBeUndefined()
    }
  })
})
