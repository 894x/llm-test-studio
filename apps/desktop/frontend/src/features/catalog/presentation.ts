import { translateDesktop } from "@/i18n/runtime"

const typeKeys: Record<string, string> = {
  "request.single": "catalog_type_request_single",
  "response.probe": "catalog_type_response_probe",
  "legacy.apiaudit": "catalog_type_legacy_apiaudit",
  "latency.input_ladder": "catalog_type_latency_input_ladder",
}

export function caseTypeLabel(type: string, fallback = type): string {
  return typeKeys[type] ? translateDesktop(`desktop:${typeKeys[type]}`) : fallback
}
