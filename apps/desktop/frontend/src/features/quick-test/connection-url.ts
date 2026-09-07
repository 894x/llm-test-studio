import { translateDesktop as tx } from "@/i18n/runtime"
import type { QuickTestCommand } from "./data"

export function connectionURLHint(
  value: string,
  mode: QuickTestCommand["address_mode"],
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
    if (parsed.username || parsed.password || parsed.search || parsed.hash) {
      return tx(
        "desktop:quick-test_the_endpoint_must_not_contain_credentials_query_parameters_or_fragments",
      )
    }
    if (parsed.protocol === "http:" && !isLoopbackHost(parsed.hostname)) {
      return tx("desktop:quick-test_remote_endpoints_must_use_https_http_is_only_allowed_for")
    }
    if (mode === "full_url" && !value.replace(/\/+$/, "").endsWith("/chat/completions")) {
      return tx("desktop:quick-test_a_full_url_must_end_with_chat_completions")
    }
    return undefined
  } catch {
    return tx("desktop:quick-test_enter_a_valid_url_starting_with_http_or_https")
  }
}

function isLoopbackHost(hostname: string): boolean {
  const normalized = hostname.toLowerCase().replace(/\.$/, "")
  return (
    normalized === "localhost" ||
    normalized === "::1" ||
    normalized === "[::1]" ||
    /^127(?:\.\d{1,3}){3}$/.test(normalized)
  )
}
