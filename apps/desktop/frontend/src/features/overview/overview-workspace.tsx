import { useMemo } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
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
  onOpenReport,
}: {
  workspace: WorkspaceSnapshot
  catalog: CatalogSnapshot
  reports: ReportSnapshot
  onOpenReport: (reportID: string) => void
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
      <ScrollArea contentWidth="viewport" className="min-h-0 flex-1">
        <div className="min-w-0">
          <section aria-label={t("summary.aria")} className="grid grid-cols-6 gap-2 px-4">
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
              <Table aria-labelledby="recent-reports-heading" className="mt-2 table-fixed">
                <TableHeader>
                  <TableRow>
                    <TableHead className="h-8 w-24 text-[11px]">{t("reports:columns.verdict")}</TableHead>
                    <TableHead className="h-8 text-[11px]">{t("reports:columns.reportPlan")}</TableHead>
                    <TableHead className="h-8 w-[32%] text-[11px]">{t("reports:columns.target")}</TableHead>
                    <TableHead className="h-8 w-32 text-right text-[11px]">{t("reports:columns.cases")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {recentReports.map((report) => (
                    <TableRow key={report.id} className="cursor-pointer" onClick={() => onOpenReport(report.id)}>
                      <TableCell className="text-xs">
                        <span className={report.passed ? "inline-flex items-center gap-1.5 text-success-strong" : "inline-flex items-center gap-1.5 text-destructive"}>
                          <span className="status-dot" aria-hidden="true" />
                          {t(report.passed ? "reports.passed" : "reports.failed")}
                        </span>
                      </TableCell>
                      <TableCell className="text-xs font-medium">
                        <Button variant="link" size="sm" className="h-auto w-full justify-start whitespace-normal p-0 text-left text-xs text-foreground [overflow-wrap:anywhere]"
                          aria-label={t("reports:viewAria", { name: reportVerdictTranslationKey(report) ? t(`reports:${reportVerdictTranslationKey(report)}`) : report.verdict })}
                          onClick={(event) => { event.stopPropagation(); onOpenReport(report.id) }}>
                          {reportVerdictTranslationKey(report) ? t(`reports:${reportVerdictTranslationKey(report)}`) : report.verdict}
                        </Button>
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">{report.model_name} · {report.channel_name}</TableCell>
                      <TableCell className="text-right text-xs tabular-nums text-muted-foreground">{t("reports.failureCount", { failed: report.failed_case_count, total: report.case_count })}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </section>
        </div>
      </ScrollArea>
    </PageFrame>
  )
}

function SummaryCell({ value, label }: { value: string; label: string }) {
  return (
    <div className="min-w-0 rounded-lg px-4 py-3 transition-colors duration-200 hover:bg-surface-hover [overflow-wrap:anywhere]">
      <div className="text-sm font-semibold tabular-nums">{value}</div>
      <div className="mt-1 text-[10px] text-muted-foreground">{label}</div>
    </div>
  )
}
