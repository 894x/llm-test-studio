import type { CatalogSnapshot } from "@/features/catalog/data"
import type { PlanRunConnection } from "@/features/runs/data"
import { connectionURLHint } from "./connection-url"
import { translateDesktop as tx } from "@/i18n/runtime"

export function planConnectionCommand(connection: PlanRunConnection, catalog: CatalogSnapshot) {
  const errors: Record<string, string> = {}
  const channel = catalog.channels.find(item => item.id === connection.channel_id && item.enabled && item.credential_configured)
  if (connection.channel_id && !channel) errors.channel_id = tx("quickTest:task.missingChannel")
  if (!connection.channel_id) {
    const address = connection.base_url?.trim() ?? ""
    if (!address) errors.base_url = tx("desktop:quick-test_enter_an_endpoint")
    else if (connectionURLHint(address, "base_url")) errors.base_url = connectionURLHint(address, "base_url")!
    const key = connection.api_key ?? ""
    if (!connection.credential_run_id && (!key.trim() || key.length > 16384 || /\p{Cc}/u.test(key)))
      errors.api_key = tx("desktop:quick-test_enter_an_api_key_or_select_a_channel_with_saved")
  }
  const model = connection.model.trim()
  if (!model || model.length > 256 || /\p{Cc}/u.test(model)) errors.model = tx("desktop:quick-test_enter_a_model_id")
  const mappings = catalog.channel_models.filter(item => item.channel_id === connection.channel_id && item.upstream_model_name === model)
  if (mappings.length && !mappings.some(item => item.protocols.includes(connection.protocol))) errors.model = tx("quickTest:task.unsupportedModelProtocol")
  const command: PlanRunConnection = { protocol: connection.protocol, model,
    ...(connection.channel_id ? { channel_id: connection.channel_id } : {
      base_url: connection.base_url?.trim(),
      ...(connection.credential_run_id ? { credential_run_id: connection.credential_run_id } : { api_key: connection.api_key?.trim() }),
    }),
  }
  return { errors, command }
}
