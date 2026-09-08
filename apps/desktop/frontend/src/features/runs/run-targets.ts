import type {
  CatalogChannel,
  CatalogModel,
  CatalogPlan,
  CatalogSnapshot,
} from "@/features/catalog/data"

interface PlanRuntimeRequirements {
  plan: CatalogPlan
  protocol: CatalogModel["protocol"]
  modelTarget: string
}

function planRuntimeRequirements(
  catalog: CatalogSnapshot,
  planID: string,
): PlanRuntimeRequirements | undefined {
  const plan = catalog.plans.find((item) => item.id === planID)
  if (!plan || plan.suites.length === 0) return undefined
  const protocols = new Set(plan.suites.map((suite) => suite.protocol))
  const modelTargets = new Set(
    plan.suites.map((suite) => suite.model_target).filter(Boolean),
  )
  if (protocols.size !== 1 || modelTargets.size > 1) return undefined
  return {
    plan,
    protocol: [...protocols][0],
    modelTarget: [...modelTargets][0] ?? "",
  }
}

export function eligibleRuntimeModels(
  catalog: CatalogSnapshot,
  planID: string,
): CatalogModel[] {
  const requirements = planRuntimeRequirements(catalog, planID)
  if (!requirements) return []
  const { plan, protocol } = requirements
  if (plan.model_ids.length > 0) {
    return catalog.models.filter(
      (model) => plan.model_ids.includes(model.id) && model.protocol === protocol,
    )
  }
  return catalog.models.filter(
    (model) =>
      model.protocol === protocol &&
      eligibleRuntimeChannels(catalog, planID, model.id).length > 0,
  )
}

export function eligibleRuntimeChannels(
  catalog: CatalogSnapshot,
  planID: string,
  modelID: string,
): CatalogChannel[] {
  const requirements = planRuntimeRequirements(catalog, planID)
  const model = catalog.models.find((item) => item.id === modelID)
  if (
    !requirements ||
    !model ||
    model.protocol !== requirements.protocol ||
    (requirements.plan.model_ids.length > 0 &&
      !requirements.plan.model_ids.includes(model.id))
  ) {
    return []
  }
  const { plan, protocol, modelTarget } = requirements
  if (plan.channel_ids.length > 0) {
    return catalog.channels.filter(
      (channel) => plan.channel_ids.includes(channel.id) && channel.protocol === protocol,
    )
  }
  return catalog.channels.filter(
    (channel) =>
      channel.protocol === protocol &&
      channel.enabled &&
      channel.credential_configured &&
      catalog.channel_models.some(
        (mapping) =>
          mapping.channel_id === channel.id &&
          mapping.model_id === model.id &&
          (!modelTarget || mapping.upstream_model_name === modelTarget),
      ),
  )
}
