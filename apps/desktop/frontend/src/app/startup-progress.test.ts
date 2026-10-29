import { describe, expect, it } from "vitest"
import { DesktopDataError } from "./data-error"
import { parseStartupProgress, READY_STARTUP, STARTUP_FAILURE_CODES } from "./startup-progress"

describe("startup progress boundary", () => {
  it("accepts existing progress and bounded recovery codes", () => {
    expect(parseStartupProgress(READY_STARTUP)).toEqual(READY_STARTUP)
    const failed = { ...READY_STARTUP, status: "failed" as const, stage: "opening" as const }
    expect(parseStartupProgress(failed).failure_code).toBeUndefined()
    for (const code of STARTUP_FAILURE_CODES) {
      expect(parseStartupProgress({ ...failed, failure_code: code }).failure_code).toBe(code)
    }
    expect(parseStartupProgress({ ...failed, failure_code: "database_unsupported", recovery_available: true }).recovery_available).toBe(true)
  })

  it("rejects private details and invalid recovery data", () => {
    for (const value of [
      { ...READY_STARTUP, failure_code: "workspace_busy" },
      { ...READY_STARTUP, status: "failed", failure_code: "private provider error" },
      { ...READY_STARTUP, status: "failed", failure_code: null },
      { ...READY_STARTUP, status: "failed", detail: "sk-secret" },
      { ...READY_STARTUP, elapsed_ms: -1 },
      { ...READY_STARTUP, schema_version: 2 },
      { ...READY_STARTUP, recovery_available: true },
      { ...READY_STARTUP, status: "failed", failure_code: "disk_full", recovery_available: true },
    ]) expect(() => parseStartupProgress(value)).toThrow(DesktopDataError)
  })
})
