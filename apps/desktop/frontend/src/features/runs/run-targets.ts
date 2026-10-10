import type { CatalogChannel, CatalogModel, CatalogSnapshot } from "@/features/catalog/data"

export function eligibleRuntimeModels(catalog: CatalogSnapshot, planID: string): CatalogModel[] {
  const plan = catalog.plans.find(item => item.id === planID)
  if (!plan) return []
  return catalog.models.filter(model => model.protocols.includes(plan.protocol) && eligibleRuntimeChannels(catalog, planID, model.id).length > 0)
}
export function eligibleRuntimeChannels(catalog: CatalogSnapshot, planID: string, modelID: string): CatalogChannel[] {
  const plan = catalog.plans.find(item => item.id === planID)
  const model = catalog.models.find(item => item.id === modelID)
  if (!plan || !model || !model.protocols.includes(plan.protocol)) return []
  return catalog.channels.filter(channel => channel.enabled && channel.credential_configured &&
    catalog.channel_models.some(mapping => mapping.channel_id === channel.id && mapping.model_id === model.id && mapping.protocols.includes(plan.protocol)))
}
