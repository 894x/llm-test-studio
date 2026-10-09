import { translateDesktop as tx } from "@/i18n/runtime"
import type { QuickTestAddressMode } from "./data"
import type { CatalogSnapshot, CatalogSuite } from "@/features/catalog/data"
import { PROTOCOLS } from "@/features/catalog/protocols"

export function connectionURLHint(
  value: string,
  mode: QuickTestAddressMode,
): string | undefined {
  if (value.trim() !== value)
    return tx("desktop:quick-test_the_endpoint_must_not_have_leading_or_trailing_spaces")
  if (value.includes("\\"))
    return tx("desktop:quick-test_the_endpoint_must_not_contain_backslashes")
  try {
    const parsed = new URL(value)
    if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || !parsed.hostname) {
      return tx("desktop:quick-test_enter_a_valid_url_starting_with_http_or_https")
    }
    if (parsed.username || parsed.password || value.includes("?") || value.includes("#")) {
      return tx(
        "desktop:quick-test_the_endpoint_must_not_contain_credentials_query_parameters_or_fragments",
      )
    }
    if (mode === "full_url" && !value.replace(/\/+$/, "").endsWith("/chat/completions")) {
      return tx("desktop:quick-test_a_full_url_must_end_with_chat_completions")
    }
    return undefined
  } catch {
    return tx("desktop:quick-test_enter_a_valid_url_starting_with_http_or_https")
  }
}

export type ResolvedConnectionURL = { endpoint: string; suffix: string }

function addressPaths(path: string): string[] {
  return path.startsWith("/v1/") ? [path, path.slice(3)] : [path]
}

// Presentation counterpart of Go protocol.ResolveAddress. Execution always
// resolves the submitted address again in Core.
export function resolveConnectionURL(
  address: string,
  requestPath: string,
  knownPaths: readonly { path: string }[] = [],
): ResolvedConnectionURL | undefined {
  if (!address || connectionURLHint(address, "base_url")) return undefined
  const inputPath = new URL(address).pathname.replace(/\/+$/, "")
  const trimmed = address.replace(/\/+$/, "")
  let endpoint = ""
  if (addressPaths(requestPath).some((path) => inputPath.endsWith(path))) endpoint = trimmed
  if (!endpoint) {
    for (const known of knownPaths) {
      const matched = addressPaths(known.path).find((path) => inputPath.endsWith(path))
      if (matched) {
        endpoint = trimmed.slice(0, -matched.length) + requestPath
        break
      }
    }
  }
  if (!endpoint) {
    let completion = requestPath
    let overlap = 0
    for (const candidate of addressPaths(requestPath)) {
      for (let size = Math.min(inputPath.length, candidate.length - 1); size > overlap; size--) {
        if (inputPath.endsWith(candidate.slice(0, size))) {
          completion = candidate.slice(size)
          overlap = size
          break
        }
      }
    }
    endpoint = trimmed + completion
  }
  return { endpoint, suffix: endpoint.startsWith(address) ? endpoint.slice(address.length) : "" }
}

export function taskConnectionURLs(address: string, task: CatalogSuite | undefined, catalog: CatalogSnapshot) {
  if (!task) return []
  const descriptor = PROTOCOLS.find((protocol) => protocol.id === task.protocol)!
  const paths = task.cases.map(({ case_id }) => {
    const testCase = catalog.test_cases.find((item) => item.id === case_id)
    const operation = testCase?.spec.operation ?? ""
    return descriptor.request_paths.find((route) => route.operation === operation)?.path
  }).filter((path): path is NonNullable<typeof path> => !!path)
  if (!paths.length) paths.push(descriptor.request_paths[0].path)
  return [...new Set(paths)].flatMap((path) => {
    const resolved = resolveConnectionURL(address, path, descriptor.request_paths)
    return resolved ? [resolved] : []
  })
}
