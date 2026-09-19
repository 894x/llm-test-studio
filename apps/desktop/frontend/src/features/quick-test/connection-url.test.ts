import { describe, expect, it } from "vitest"
import { connectionURLHint } from "./connection-url"

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
