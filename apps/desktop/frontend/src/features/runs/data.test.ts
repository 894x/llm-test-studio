import { expect, it } from "vitest"

import { FIXTURE_WORKSPACE } from "./fixtures"
import { presentWorkspace } from "./data"

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
