import { useMemo } from "react"
import { useTranslation } from "react-i18next"

import { Badge } from "@/components/ui/badge"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import type { CatalogSnapshot } from "@/features/catalog/data"
import { reportVerdictTranslationKey, type ReportSnapshot } from "@/features/reports/data"
import {
  presentWorkspace,
  type WorkspaceSnapshot,
} from "@/features/runs/data"
import { PageFrame } from "@/features/shell/page-frame"

export function OverviewWorkspace({
  workspace,
  catalog,
  reports,
}: {
  workspace: WorkspaceSnapshot
  catalog: CatalogSnapshot
  reports: ReportSnapshot
}) {
  const { t } = useTranslation(["overview", "common", "reports"])
  const presentation = useMemo(() => presentWorkspace(workspace), [workspace])
  const activeRun = workspace.active_run_id
    ? presentation.runs.find((run) => run.id === workspace.active_run_id)
    : undefined
  const recentReports = [...reports.reports]
    .sort((left, right) =>
      right.generated_at.localeCompare(left.generated_at) || right.id.localeCompare(left.id),
    )
    .slice(0, 4)
  const enabledChannels = catalog.channels.filter((channel) => channel.enabled).length
  const configuredChannels = catalog.channels.filter(
    (channel) => channel.credential_configured,
  ).length

  return (
    <PageFrame
      title={t("title")}
      description={t("description")}
    >
      <ScrollArea className="min-h-0 flex-1">
        <div className="min-w-[760px]">
          <section aria-label={t("summary.aria")} className="grid grid-cols-6">
            <SummaryCell value={t("summary.models", { count: catalog.models.length })} label={t("summary.logicalModels")} />
            <SummaryCell value={t("summary.channels", { count: catalog.channels.length })} label={t("summary.enabled", { count: enabledChannels })} />
            <SummaryCell value={t("summary.cases", { count: catalog.test_cases.length })} label={t("summary.suites", { count: catalog.suites.length })} />
            <SummaryCell value={t("summary.plans", { count: catalog.plans.length })} label={t("summary.fixedVersions")} />
            <SummaryCell value={t("summary.runs", { count: presentation.runs.length })} label={t(activeRun ? "summary.activeRun" : "summary.noActiveRun")} />
            <SummaryCell value={t("summary.reports", { count: reports.reports.length })} label={t("summary.archivedConclusions")} />
          </section>

          <div className="grid grid-cols-2">
            <section aria-labelledby="active-run-heading" className="min-h-52 min-w-0 px-4 py-3">
              <div className="flex items-center justify-between">
                <h2 id="active-run-heading" className="text-sm font-semibold">{t("activeRun.title")}</h2>
                {activeRun ? <Badge variant="outline">{t(`common:status.${activeRun.status}`)}</Badge> : null}
              </div>
              {activeRun ? (
                <dl className="mt-3 grid grid-cols-[112px_minmax(0,1fr)] [overflow-wrap:anywhere] gap-x-3 gap-y-2 text-xs">
                  <dt className="text-muted-foreground">{t("activeRun.plan")}</dt><dd className="font-medium">{activeRun.title}</dd>
                  <dt className="text-muted-foreground">{t("activeRun.modelChannel")}</dt><dd>{activeRun.model} · {activeRun.channel}</dd>
                  <dt className="text-muted-foreground">{t("activeRun.load")}</dt><dd>{activeRun.loadProfile}</dd>
                  <dt className="text-muted-foreground">{t("activeRun.completed")}</dt><dd className="tabular-nums">{activeRun.quickTask ? activeRun.completed : `${activeRun.completed}/${activeRun.total || t("activeRun.timed")}`}</dd>
                  <dt className="text-muted-foreground">{t("activeRun.coreStatus")}</dt><dd>{activeRun.coreStatus}</dd>
                </dl>
              ) : (
                <Empty className="min-h-40 border-0 p-0">
                  <EmptyTitle>{t("activeRun.emptyTitle")}</EmptyTitle>
                  <EmptyDescription>{t("activeRun.emptyDescription")}</EmptyDescription>
                </Empty>
              )}
            </section>

            <section aria-labelledby="readiness-heading" className="min-h-52 min-w-0 px-4 py-3">
              <h2 id="readiness-heading" className="text-sm font-semibold">{t("readiness.title")}</h2>
              <dl className="mt-3 grid grid-cols-[128px_minmax(0,1fr)] [overflow-wrap:anywhere] gap-x-3 gap-y-2 text-xs">
                <dt className="text-muted-foreground">{t("readiness.enabledChannels")}</dt><dd>{enabledChannels}/{catalog.channels.length}</dd>
                <dt className="text-muted-foreground">{t("readiness.configuredCredentials")}</dt><dd>{configuredChannels}/{catalog.channels.length}</dd>
                <dt className="text-muted-foreground">{t("readiness.modelMappings")}</dt><dd>{t("readiness.mappingCount", { count: catalog.channel_models.length })}</dd>
                <dt className="text-muted-foreground">{t("readiness.reusableSuites")}</dt><dd>{t("readiness.suiteCount", { count: catalog.suites.length })}</dd>
                <dt className="text-muted-foreground">{t("readiness.localSecrets")}</dt><dd>{t("readiness.localSecretsValue")}</dd>
              </dl>
            </section>
          </div>

          <section aria-labelledby="recent-reports-heading" className="px-4 py-3">
            <div className="flex items-center justify-between">
              <h2 id="recent-reports-heading" className="text-sm font-semibold">{t("reports.title")}</h2>
              <span className="text-[11px] text-muted-foreground">{t("reports.source")}</span>
            </div>
            {recentReports.length === 0 ? (
              <p className="mt-3 text-xs text-muted-foreground">{t("reports.empty")}</p>
            ) : (
              <div className="mt-2 divide-y divide-divider">
                {recentReports.map((report) => (
                  <div key={report.id} className="grid grid-cols-[84px_minmax(0,1fr)_180px_110px] items-center gap-3 py-2 text-xs">
                    <Badge
                      variant="outline"
                      className={report.passed ? "border-success/25 bg-success-soft text-success-strong" : "border-destructive/25 bg-destructive-soft text-destructive"}
                    >
                      {t(report.passed ? "reports.passed" : "reports.failed")}
                    </Badge>
                    <span className="font-medium [overflow-wrap:anywhere]">{reportVerdictTranslationKey(report) ? t(`reports:${reportVerdictTranslationKey(report)}`) : report.verdict}</span>
                    <span className="text-muted-foreground [overflow-wrap:anywhere]">{report.model_name} · {report.channel_name}</span>
                    <span className="text-right tabular-nums text-muted-foreground">{t("reports.failureCount", { failed: report.failed_case_count, total: report.case_count })}</span>
                  </div>
                ))}
              </div>
            )}
          </section>
        </div>
      </ScrollArea>
    </PageFrame>
  )
}

function SummaryCell({ value, label }: { value: string; label: string }) {
  return (
    <div className="min-w-0 px-4 py-3 [overflow-wrap:anywhere]">
      <div className="text-sm font-semibold tabular-nums">{value}</div>
      <div className="mt-1 text-[10px] text-muted-foreground">{label}</div>
    </div>
  )
}
