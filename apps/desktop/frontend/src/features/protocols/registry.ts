import type { CatalogProtocol } from "@/features/catalog/data"
import { openAIChatPresentation } from "./openai-chat"
import { openAIResponsesPresentation } from "./openai-responses"
import { anthropicMessagesPresentation } from "./anthropic-messages"
import { seedancePresentation } from "./seedance"
import { wanVideoPresentation } from "./wan-video"
import { miniMaxVideoPresentation } from "./minimax-video"
import type { ProtocolPresentation } from "./types"

const presentations: Readonly<Record<CatalogProtocol, ProtocolPresentation>> = {
  "openai-chat": openAIChatPresentation,
  "openai-responses": openAIResponsesPresentation,
  "anthropic-messages": anthropicMessagesPresentation,
  seedance: seedancePresentation,
  "wan-video": wanVideoPresentation,
  "minimax-video": miniMaxVideoPresentation,
}
export function protocolPresentation(protocol: CatalogProtocol): ProtocolPresentation { return presentations[protocol] }
