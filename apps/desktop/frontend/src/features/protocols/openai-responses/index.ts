import { ChatObservationDetails } from "../observation-details"
import { openAIChatPresentation } from "../openai-chat"
import type { ProtocolPresentation } from "../types"

export const openAIResponsesPresentation: ProtocolPresentation = {
  protocol: "openai-responses", label: "OpenAI Responses",
  operations: [], runSettings: [], ObservationDetails: ChatObservationDetails,
  columns: openAIChatPresentation.columns,
  requestExample: { input: "Hello", max_output_tokens: 128 },
}
