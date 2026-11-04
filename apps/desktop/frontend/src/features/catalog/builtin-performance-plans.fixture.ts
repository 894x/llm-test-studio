import { BUILTIN_CATALOG } from "./builtin-catalog.fixture"

export const FIXTURE_PERFORMANCE_PLANS = BUILTIN_CATALOG.plans.filter(plan =>
  plan.entries.length > 0 && plan.entries.every(entry => entry.target_kind === "performance"),
)
