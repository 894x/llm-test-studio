import { ChatObservationDetails } from "../observation-details"
import type { ProtocolPresentation } from "../types"
export const openAIChatPresentation: ProtocolPresentation = {
  protocol: "openai-chat", label: "OpenAI Chat",
  operations: ["models.list"], runSettings: [], ObservationDetails: ChatObservationDetails,
  columns: [
    { id: "http", label: "HTTP", observation: "http_status" },
    { id: "e2e", label: "E2E", metric: "e2e_ms", unit: "ms" },
    { id: "ttft", label: "TTFT", metric: "ttft_ms", unit: "ms" },
    { id: "tokens", label: "Output tokens", metric: "output_tokens" },
  ],
  requestExample: { messages: [{ role: "user", content: "Hello" }] },
}
