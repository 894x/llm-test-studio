import { useEffect, useState } from "react"
import { isJobTerminal, type ControlPageView, type JobView, type PerformanceResultView } from "./data"
import type { JobClient } from "./execution"

export function useJobObservation(client: JobClient, jobID: string, controlsAfter: string, refresh: number) {
  const [job, setJob] = useState<JobView | null>(null)
  const [result, setResult] = useState<PerformanceResultView | null>(null)
  const [controlState, setControls] = useState<{ after: string; page: ControlPageView } | null>(null)
  const [error, setError] = useState<unknown>(null)
  useEffect(() => {
    let active = true
    let timer: number | undefined
    const read = async () => {
      let settled = false
      try {
        const [jobRead, controlRead] = await Promise.allSettled([
          client.getJob(jobID), client.getJobControls(jobID, controlsAfter, 25),
        ])
        if (!active) return
        if (controlRead.status === "fulfilled") setControls({ after: controlsAfter, page: controlRead.value })
        if (jobRead.status === "rejected") throw jobRead.reason
        const current = jobRead.value
        setJob(current)
        const outcome = current.performance_result_ready ? await client.getJobPerformanceResult(jobID) : null
        if (!active) return
        setResult(outcome)
        if (controlRead.status === "rejected") throw controlRead.reason
        setError(null)
        settled = !!current.execution && isJobTerminal(current.execution.status) &&
          (current.kind !== "performance" || outcome !== null) &&
          !controlRead.value.commands.some(command => command.state === "pending")
      } catch (reason) {
        // Read failures preserve the last authoritative state, including a
        // receipt whose journal synchronization has not yet arrived.
        if (active) setError(reason)
      } finally {
        if (active && !settled) timer = window.setTimeout(() => void read(), 1_500)
      }
    }
    void read()
    return () => { active = false; window.clearTimeout(timer) }
  }, [client, jobID, controlsAfter, refresh])
  return { job: job?.job_id === jobID ? job : null, result: result?.job_id === jobID ? result : null, controls: controlState?.after === controlsAfter && controlState.page.job_id === jobID ? controlState.page : null, error }
}
