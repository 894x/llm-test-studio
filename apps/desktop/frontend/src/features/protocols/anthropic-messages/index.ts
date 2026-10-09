import { ChatObservationDetails } from "../observation-details"
import { openAIChatPresentation } from "../openai-chat"
import type { ProtocolPresentation } from "../types"

export const anthropicMessagesPresentation: ProtocolPresentation = {
  protocol: "anthropic-messages", label: "Anthropic Messages",
  operations: [], runSettings: [], ObservationDetails: ChatObservationDetails,
  columns: openAIChatPresentation.columns,
  requestExample: { messages: [{ role: "user", content: "Hello" }], max_tokens: 128 },
}
