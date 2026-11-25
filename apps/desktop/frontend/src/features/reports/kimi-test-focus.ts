import type { ReportCaseDetail } from "./data"

export type KimiTestFocus = "cache" | "ttl" | "usage" | "protocol" | "video"

// Presentation only: never attach this annotation to stored reports or exports.
export function kimiTestFocus(item: Pick<ReportCaseDetail, "key" | "protocol" | "dimension">, model: string): KimiTestFocus | undefined {
  const normalizedModel = model.toLowerCase().replace(/[\s._-]/g, "")
  if (!["kimik3", "kimik31pro"].includes(normalizedModel) ||
    !["openai-chat", "openai-responses"].includes(item.protocol)) return undefined

  const key = item.key.toLowerCase()
  if (key.includes("video_url") || key === "k3.responses.input_video_unsupported") return "video"
  if (key.includes("cache") && (key.includes("ttl") || key.includes("1h") || key.includes("5m"))) return "ttl"
  if (item.dimension === "cache" || key.startsWith("kimi.cache.") || key.startsWith("k3.responses.cache_")) return "cache"
  if (key.includes("usage") || key.startsWith("kvv.prompt_tokens")) return "usage"
  if (key.startsWith("kimi.protocol.") ||
    ["must.stream_integrity", "k31.must.stream_integrity", "k31.stream_explicit_false", "k3.responses.stream_event_envelope"].includes(key)) return "protocol"
  return undefined
}
