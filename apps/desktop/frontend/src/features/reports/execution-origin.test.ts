import { describe, expect, it } from "vitest"

import { DesktopDataError } from "@/app/data-error"
import { parseExecutionOrigin, type ExecutionOrigin } from "./execution-origin"

export function originFixture(kind: "local" | "remote" = "remote"): ExecutionOrigin {
  return { kind, node_id: "10000000-0000-4000-8000-000000000020", installation_id: "10000000-0000-4000-8000-000000000021",
    engine_build_id: "a".repeat(64), runner_version: "v-current", os: "linux", arch: "arm64" }
}

describe("execution origin boundary", () => {
  it.each(["local", "remote"] as const)("preserves an explicit %s origin without sharing the input", kind => {
    const source = originFixture(kind)
    const parsed = parseExecutionOrigin(source)
    expect(parsed).toEqual(source)
    expect(parsed).not.toBe(source)
    source.runner_version = "changed"
    expect(parsed?.runner_version).toBe("v-current")
  })

  it("accepts explicit null without inferring a local executor", () => {
    expect(parseExecutionOrigin(null)).toBeNull()
  })

  it.each([undefined, false, "local", 0, [], {}, { ...originFixture(), path: "/private" }])("rejects missing or malformed provenance %j", source => {
    expect(() => parseExecutionOrigin(source)).toThrow(DesktopDataError)
    expect(() => parseExecutionOrigin(source)).toThrow(/execution_origin/)
  })

  it.each(Object.keys(originFixture()))("rejects missing and mis-cased %s without mutating the data", key => {
    const missing: Record<string, unknown> = { ...originFixture() }
    delete missing[key]
    const before = structuredClone(missing)
    expect(() => parseExecutionOrigin(missing)).toThrow(DesktopDataError)
    expect(missing).toEqual(before)
    const miscased = { ...missing, [key.toUpperCase()]: originFixture()[key as keyof ExecutionOrigin] }
    expect(() => parseExecutionOrigin(miscased)).toThrow(DesktopDataError)
  })

  it.each([
    { kind: "LOCAL" }, { kind: "" },
    { node_id: "not-a-uuid" }, { node_id: "10000000-0000-6000-8000-000000000020" },
    { node_id: "10000000-0000-4000-0000-000000000020" },
    { installation_id: "10000000-0000-4000-8000-00000000002A" },
    { engine_build_id: "A".repeat(64) }, { engine_build_id: "a".repeat(63) }, { engine_build_id: "g".repeat(64) },
    { runner_version: "" }, { runner_version: "a".repeat(129) }, { runner_version: "é".repeat(65) },
    { runner_version: " v-current" }, { runner_version: "v-current " }, { runner_version: "v\ncurrent" },
    { runner_version: "v\u0000current" }, { runner_version: "v\u0085current" }, { runner_version: "v\ud800current" },
    { os: "" }, { os: "a".repeat(33) }, { os: " linux" }, { os: "linux\t" }, { os: "lin\rux" },
    { arch: "" }, { arch: "é".repeat(17) }, { arch: "arm64 " }, { arch: "arm\u007f64" },
  ])("rejects invalid identity or unsafe labels: %j", changes => {
    const source = { ...originFixture(), ...changes }
    const before = structuredClone(source)
    expect(() => parseExecutionOrigin(source)).toThrow(DesktopDataError)
    expect(source).toEqual(before)
  })

  it("accepts the exact UTF-8 byte boundaries and preserves labels", () => {
    const source = { ...originFixture(), runner_version: "é".repeat(64), os: "é".repeat(16), arch: "a".repeat(32) }
    expect(parseExecutionOrigin(source)).toEqual(source)
  })
})
