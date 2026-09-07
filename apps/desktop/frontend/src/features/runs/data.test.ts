import { expect, it } from "vitest"

import { FIXTURE_WORKSPACE } from "./fixtures"
import { presentWorkspace } from "./data"

it("presents Suite observations without claiming a fixed request budget", () => {
  const snapshot = structuredClone(FIXTURE_WORKSPACE)
  Object.assign(snapshot.runs[0], { source: "quick_task", planned: 0, duration_ms: 0, completed: 6, passed: 6, failed: 0 })
  expect(presentWorkspace(snapshot).runs[0]).toMatchObject({ quickTask: true, total: 0, completed: 6, loadProfile: "按 Suite 执行" })
})

it("presents the durable failure phase and error code for a failed run", () => {
  const snapshot = structuredClone(FIXTURE_WORKSPACE)
  Object.assign(snapshot.runs[0], {
    status: "failed",
    failure_phase: "execute",
    error_code: "run_execution_failed",
  })

  const presented = presentWorkspace(snapshot)

  expect(presented.runs[0].failureSummary).toBe(
    "execute · run_execution_failed",
  )
})
