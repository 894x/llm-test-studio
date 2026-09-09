import { TaskObservationDetails } from "../observation-details"
import type { ProtocolPresentation } from "../types"
export const miniMaxVideoPresentation: ProtocolPresentation = {
  protocol: "minimax-video", label: "MiniMax Video",
  operations: [], runSettings: ["poll_interval_ms", "task_timeout_ms"], ObservationDetails: TaskObservationDetails,
  columns: [
    { id: "http", label: "HTTP", observation: "http_status" },
    { id: "task", label: "Task", observation: "task_status" },
    { id: "e2e", label: "E2E", metric: "e2e_ms", unit: "ms" },
    { id: "artifacts", label: "Artifacts", observation: "artifacts" },
  ],
  requestExample: { prompt: "A quiet landscape" },
}
