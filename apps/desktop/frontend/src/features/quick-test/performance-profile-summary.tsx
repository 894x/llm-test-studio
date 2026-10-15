import { useTranslation } from "react-i18next"
import type { QuickPerformanceProfile } from "./data"

export function PerformanceProfileSummary({ profile, compact = false }: {
  profile: QuickPerformanceProfile; compact?: boolean
}) {
  const { t } = useTranslation("quickTest")
  const load = profile.load_mode === "open_loop"
    ? `${profile.rate_per_second} RPS`
    : t("performance.concurrencySummary", { count: profile.concurrency })
  const target = [
    profile.request_count > 0 ? t("performance.requestsSummary", { count: profile.request_count }) : "",
    profile.duration_ms > 0 ? `${profile.duration_ms / 1_000} s` : "",
  ].filter(Boolean).join(" / ")
  if (compact) return <span className="[overflow-wrap:anywhere]">{target} · {load}</span>
  const rows = [
    [t("performance.load"), `${target} · ${load}`],
    [t("performance.tokenTargets"), `${profile.input_tokens} / ${profile.output_tokens}`],
    [t("performance.timeout"), `${profile.timeout_ms / 1_000} s`],
    [t("performance.phases"), t("performance.phasesSummary", {
      warmup: profile.warmup_requests ?? 0, ramp: (profile.ramp_duration_ms ?? 0) / 1_000,
    })],
    [t("performance.workload"), t(profile.random_input ? "performance.randomWorkload" : "performance.sharedWorkload")],
    ...(profile.capacity_enabled ? [[t("performance.capacity"), `${profile.capacity_start} → ${profile.load_mode === "open_loop" ? profile.rate_per_second : profile.concurrency} (+${profile.capacity_step})`]] : []),
  ]
  return <dl aria-label={t("performance.configuration")} className="grid min-w-0 grid-cols-[repeat(auto-fit,minmax(min(100%,12rem),1fr))] gap-3 text-xs">
    {rows.map(([label, value]) => <div key={label} className="min-w-0 space-y-1 [overflow-wrap:anywhere]">
      <dt className="text-muted-foreground">{label}</dt><dd className="tabular-nums">{value}</dd>
    </div>)}
  </dl>
}
