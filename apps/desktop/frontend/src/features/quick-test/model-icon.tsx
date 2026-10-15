import CpuIcon from "lucide-react/dist/esm/icons/cpu.mjs"
import openai from "@/assets/model-providers/openai.svg?raw"
import anthropic from "@/assets/model-providers/anthropic.svg?raw"
import claude from "@/assets/model-providers/claude.svg?raw"
import kimi from "@/assets/model-providers/kimi.svg?raw"
import gemini from "@/assets/model-providers/gemini.svg?raw"
import deepseek from "@/assets/model-providers/deepseek.svg?raw"
import qwen from "@/assets/model-providers/qwen.svg?raw"
import zhipu from "@/assets/model-providers/zhipu.svg?raw"
import minimax from "@/assets/model-providers/minimax.svg?raw"
import doubao from "@/assets/model-providers/doubao.svg?raw"
import bytedance from "@/assets/model-providers/bytedance.svg?raw"
import alibabacloud from "@/assets/model-providers/alibabacloud.svg?raw"
import type { CatalogProtocol } from "@/features/catalog/data"

const icons = { openai, anthropic, claude, kimi, gemini, deepseek, qwen, zhipu, minimax, doubao, bytedance, alibabacloud }
type Provider = keyof typeof icons

const modelProviders: [RegExp, Provider][] = [
  [/^(kimi|moonshot)/, "kimi"],
  [/^(gpt|chatgpt|o[134](?:\b|-)|dall-e|text-embedding|sora)/, "openai"],
  [/^claude/, "claude"],
  [/^gemini/, "gemini"],
  [/^deepseek/, "deepseek"],
  [/^(qwen|qwq)/, "qwen"],
  [/^(glm|chatglm)/, "zhipu"],
  [/^(minimax|abab|hailuo)/, "minimax"],
  [/^doubao/, "doubao"],
  [/^(seedance|seedream|seed-)/, "bytedance"],
  [/^wan(?:\b|[-_.\d])/, "alibabacloud"],
]

const protocolProviders: Record<CatalogProtocol, Provider> = {
  "openai-chat": "openai",
  "openai-responses": "openai",
  "anthropic-messages": "anthropic",
  seedance: "bytedance",
  "wan-video": "alibabacloud",
  "minimax-video": "minimax",
}

function ProviderIcon({ provider }: { provider?: Provider }) {
  if (!provider) return <CpuIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
  // Markup comes only from the bundled brand assets, never from the model input.
  return <span
    aria-hidden="true"
    data-provider={provider}
    className="inline-flex size-4 shrink-0 [&>svg]:size-full"
    dangerouslySetInnerHTML={{ __html: icons[provider] }}
  />
}

export function ModelIcon({ model }: { model: string }) {
  const name = model.trim().toLowerCase().split("/").at(-1) ?? ""
  return <ProviderIcon provider={modelProviders.find(([pattern]) => pattern.test(name))?.[1]} />
}

export function ProtocolIcon({ protocol }: { protocol: CatalogProtocol }) {
  return <ProviderIcon provider={protocolProviders[protocol]} />
}
