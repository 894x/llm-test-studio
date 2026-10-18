import plan0 from "../../../../../../data/performanceplans/149778ef-59ad-5f2e-b121-94729933f982.json"
import plan1 from "../../../../../../data/performanceplans/1fcf92de-ecf2-57fe-856d-b74c895da305.json"
import plan2 from "../../../../../../data/performanceplans/21c23509-fe1c-5c45-a1d6-2aecb1bc0418.json"
import plan3 from "../../../../../../data/performanceplans/44c56536-b421-593a-89e4-e5a9d0c44360.json"
import plan4 from "../../../../../../data/performanceplans/45fd3d2c-71e3-54c2-95d3-60b94386d7b8.json"
import plan5 from "../../../../../../data/performanceplans/5543e378-39f7-54d9-b7eb-ec1fa6e8617e.json"
import plan6 from "../../../../../../data/performanceplans/8468e889-c685-56e5-9256-6fe96c8a6137.json"
import plan7 from "../../../../../../data/performanceplans/84c81db5-d565-523f-96fc-493efbaa9a3b.json"
import plan8 from "../../../../../../data/performanceplans/896ce007-552f-5387-ace8-f16dfaf93e4f.json"
import plan9 from "../../../../../../data/performanceplans/9435f08f-ada0-5d0f-9160-955d3c946ade.json"
import plan10 from "../../../../../../data/performanceplans/9bc2ecd5-133b-5c68-a8ed-c05b066511f6.json"
import plan11 from "../../../../../../data/performanceplans/a1a9fd6c-70e0-5595-a38f-ba1e858edf91.json"
import plan12 from "../../../../../../data/performanceplans/af6c5abc-176d-5ebe-a383-31bc2056b285.json"
import plan13 from "../../../../../../data/performanceplans/af972ed2-94fd-58b4-a0c8-a618c04688e1.json"
import plan14 from "../../../../../../data/performanceplans/b9e5ad4a-fb09-5d58-b795-12d53b0bbb63.json"
import plan15 from "../../../../../../data/performanceplans/c7810a2f-b6fb-5ca0-a866-fb7807dc4841.json"
import plan16 from "../../../../../../data/performanceplans/f56617fd-5354-5919-817f-2a2fe13a5e84.json"
import plan17 from "../../../../../../data/performanceplans/fae460fc-fe6a-549d-ab9a-199025b13232.json"
import type { CatalogPlan } from "./data"
import { parseQuickPerformanceProfile } from "@/features/quick-test/data"

export const FIXTURE_PERFORMANCE_PLANS: CatalogPlan[] = [
plan0, plan1, plan2, plan3, plan4, plan5, plan6, plan7, plan8, plan9, plan10, plan11, plan12, plan13, plan14, plan15, plan16, plan17].map(document => {
  const performance = parseQuickPerformanceProfile(document.performance)
  return {
    id: document.id, revision: document.revision, name: document.name,
    protocol: document.protocol as CatalogPlan["protocol"], seed: document.seed,
    builtin: true, entry_count: 1, case_count: 0, performance,
    entries: [{ entry_id: document.id, target_id: document.id, target_kind: "performance", target_key: "performance",
      target_name: document.name, name: document.name, on_failure: "continue", case_count: 0, performance,
      parameters: {}, settings: {}, warmup_count: 0, sla_thresholds: {}, load_mode: performance.load_mode ?? "fixed_concurrency",
      concurrency: Math.max(1, performance.concurrency), request_count: performance.request_count,
      duration_ms: performance.duration_ms ?? 0, rate_per_second: performance.rate_per_second ?? 0, request_timeout_ms: performance.timeout_ms }],
  }
})
