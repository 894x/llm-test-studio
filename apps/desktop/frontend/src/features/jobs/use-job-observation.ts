import { useEffect, useState } from "react"
import { isJobTerminal, type ControlPageView, type ControlView, type JobView, type PerformanceResultView } from "./data"
import type { JobClient } from "./execution"

export function useJobObservation(client: JobClient, jobID: string, controlsAfter: string, refresh: number, waitingForReport = false) {
  const [job, setJob] = useState<JobView | null>(null)
  const [result, setResult] = useState<PerformanceResultView | null>(null)
  const [controlState, setControls] = useState<{ after: string; page: ControlPageView } | null>(null)
  const [controlSummary, setControlSummary] = useState<{ job_id: string; commands: ControlView[]; ready: boolean } | null>(null)
  const [error, setError] = useState<unknown>(null)
  useEffect(() => {
    let active = true
    let timer: number | undefined
    let scanCursor = ""
    let scanReady = false
    const observedControls = new Map<string, ControlView>()
    const scanControls = new Map<string, ControlView>()
    const merge = (target: Map<string, ControlView>, commands: ControlView[]) => {
      for (const command of commands) {
        const previous = target.get(command.command_id)
        if (!previous || command.delivery_revision >= previous.delivery_revision) target.set(command.command_id, command)
      }
    }
    const read = async () => {
      let settled = false
      try {
        const [jobRead, controlRead, scanRead] = await Promise.allSettled([
          client.getJob(jobID), client.getJobControls(jobID, controlsAfter, 25),
          controlsAfter !== scanCursor ? client.getJobControls(jobID, scanCursor, 25) : Promise.resolve(null),
        ])
        if (!active) return
        if (controlRead.status === "fulfilled") setControls({ after: controlsAfter, page: controlRead.value })
        if (jobRead.status === "rejected") throw jobRead.reason
        const current = jobRead.value
        setJob(current)
        const outcome = current.performance_result_ready ? await client.getJobPerformanceResult(jobID) : null
        if (!active) return
        setResult(outcome)
        const scan = controlsAfter === scanCursor ? controlRead : scanRead
        if (scan.status === "rejected") throw scan.reason
        const scanPage = scan.value!
        if (scanPage.has_more && (!scanPage.next_cursor || scanPage.next_cursor === scanCursor)) throw new Error("Invalid control continuation")
        // UUID pagination is not chronological. Visit one bounded page per poll
        // and merge revisions, including acknowledgements on the viewed page.
        if (!scanCursor) scanControls.clear()
        merge(scanControls, scanPage.commands)
        if (!scanPage.has_more) observedControls.clear()
        merge(observedControls, scanPage.has_more ? scanPage.commands : [...scanControls.values()])
        if (controlRead.status === "fulfilled") merge(observedControls, controlRead.value.commands)
        scanCursor = scanPage.has_more ? scanPage.next_cursor : ""
        scanReady ||= !scanPage.has_more
        const commands = [...observedControls.values()].sort((left, right) => Date.parse(right.created_at) - Date.parse(left.created_at) || right.command_id.localeCompare(left.command_id))
        setControlSummary({ job_id: jobID, commands, ready: scanReady })
        if (controlRead.status === "rejected") throw controlRead.reason
        setError(null)
        settled = !waitingForReport && !!current.execution && isJobTerminal(current.execution.status) &&
          (current.kind !== "performance" || outcome !== null) &&
          scanReady && scanCursor === "" && !commands.some(command => command.state === "pending")
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
  }, [client, jobID, controlsAfter, refresh, waitingForReport])
  return { job: job?.job_id === jobID ? job : null, result: result?.job_id === jobID ? result : null, controls: controlState?.after === controlsAfter && controlState.page.job_id === jobID ? controlState.page : null,
    controlSummary: controlSummary?.job_id === jobID ? controlSummary : null, error }
}
