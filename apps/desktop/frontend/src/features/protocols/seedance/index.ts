import { TaskObservationDetails } from "../observation-details"
import type { ProtocolPresentation } from "../types"
export const seedancePresentation: ProtocolPresentation = {
  protocol: "seedance", label: "Seedance",
  operations: [], runSettings: ["poll_interval_ms", "task_timeout_ms"], ObservationDetails: TaskObservationDetails,
  columns: [
    { id: "http", label: "HTTP", observation: "http_status" },
    { id: "task", label: "Task", observation: "task_status" },
    { id: "e2e", label: "E2E", metric: "e2e_ms", unit: "ms" },
    { id: "artifacts", label: "Artifacts", observation: "artifacts" },
  ],
  requestExample: { content: [{ type: "text", text: "A quiet landscape" }] },
}
