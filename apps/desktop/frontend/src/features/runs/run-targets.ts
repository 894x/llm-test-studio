import type {
  CatalogChannel,
  CatalogModel,
  CatalogSnapshot,
} from "@/features/catalog/data"
import { PROTOCOLS } from "@/features/catalog/protocols.generated"

export function paidRuntimeProtocol(catalog: CatalogSnapshot, planID: string, modelID: string, channelID: string) {
  const plan = catalog.plans.find((item) => item.id === planID)
  const model = catalog.models.find((item) => item.id === modelID)
  const protocol = PROTOCOLS.find((item) => item.id === model?.protocol)
  if (!plan || !protocol) return undefined
  const mapping = catalog.channel_models.find((item) => item.model_id === modelID && item.channel_id === channelID)
  const count = plan.cases.filter((ref) => {
    const testCase = catalog.test_cases.find((item) => item.id === ref.case_id && item.revision === ref.revision)
    // Current catalog DTOs may not contain a pinned historical member/target.
    // Keep unknown members in the acknowledgement count; the backend resolves
    // and validates the exact revisions before starting the run.
    return !testCase || !mapping || testCase.model_targets.length === 0 || testCase.model_targets.includes(mapping.upstream_model_name)
  }).length
  return count > 0 && (protocol.alwaysConfirmPaid || (protocol.confirmPaidBatch && count > 1)) ? protocol : undefined
}

function planProtocol(catalog: CatalogSnapshot, planID: string) {
  const plan = catalog.plans.find((item) => item.id === planID)
  if (!plan || plan.cases.length === 0) return undefined
  const protocols = new Set(
    plan.cases.map(
      (ref) =>
        catalog.test_cases.find((item) => item.id === ref.case_id)?.protocol,
    ),
  )
  return protocols.size === 1 && !protocols.has(undefined)
    ? [...protocols][0]
    : undefined
}

export function eligibleRuntimeModels(
  catalog: CatalogSnapshot,
  planID: string,
): CatalogModel[] {
  const plan = catalog.plans.find((item) => item.id === planID)
  const protocol = planProtocol(catalog, planID)
  if (!plan || !protocol) return []
	if (plan.model_ids.length > 0) {
		return catalog.models.filter((model) => plan.model_ids.includes(model.id))
	}
  return catalog.models.filter(
    (model) =>
      model.protocol === protocol &&
      (plan.model_ids.length === 0 || plan.model_ids.includes(model.id)) &&
      eligibleRuntimeChannels(catalog, planID, model.id).length > 0,
  )
}

export function eligibleRuntimeChannels(
  catalog: CatalogSnapshot,
  planID: string,
  modelID: string,
): CatalogChannel[] {
  const plan = catalog.plans.find((item) => item.id === planID)
  const model = catalog.models.find((item) => item.id === modelID)
  const protocol = planProtocol(catalog, planID)
  if (
    !plan ||
    !model ||
    !protocol ||
    model.protocol !== protocol ||
    (plan.model_ids.length > 0 && !plan.model_ids.includes(model.id))
  ) {
    return []
  }
	if (plan.channel_ids.length > 0) {
		return catalog.channels.filter((channel) => plan.channel_ids.includes(channel.id))
	}
  return catalog.channels.filter(
    (channel) =>
      channel.protocol === protocol &&
      channel.enabled &&
      channel.credential_configured &&
      (plan.channel_ids.length === 0 ||
        plan.channel_ids.includes(channel.id)) &&
      catalog.channel_models.some(
        (mapping) =>
          mapping.channel_id === channel.id && mapping.model_id === model.id,
      ),
  )
}
