import { useMemo } from "react"

import { Badge } from "@/components/ui/badge"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import type { CatalogSnapshot } from "@/features/catalog/data"
import type { ReportSnapshot } from "@/features/reports/data"
import {
  STATUS_LABELS,
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
      title="工作台总览"
      description="汇总本地目录、活动运行和最近报告"
    >
      <ScrollArea className="min-h-0 flex-1 border-t">
        <div className="min-w-[760px]">
          <section aria-label="本地对象摘要" className="grid grid-cols-6 border-b bg-surface-subtle">
            <SummaryCell value={`${catalog.models.length} 个模型`} label="逻辑模型" />
            <SummaryCell value={`${catalog.channels.length} 个渠道`} label={`${enabledChannels} 个启用`} />
            <SummaryCell value={`${catalog.test_cases.length} 个用例`} label={`${catalog.suites.length} 个套件`} />
            <SummaryCell value={`${catalog.plans.length} 个计划`} label="固定版本" />
            <SummaryCell value={`${presentation.runs.length} 次运行`} label={activeRun ? "1 个活动运行" : "无活动运行"} />
            <SummaryCell value={`${reports.reports.length} 份报告`} label="已封存结论" last />
          </section>

          <div className="grid grid-cols-2">
            <section aria-labelledby="active-run-heading" className="min-h-52 border-b border-r px-4 py-3">
              <div className="flex items-center justify-between">
                <h2 id="active-run-heading" className="text-sm font-semibold">当前运行</h2>
                {activeRun ? <Badge variant="outline">{STATUS_LABELS[activeRun.status]}</Badge> : null}
              </div>
              {activeRun ? (
                <dl className="mt-3 grid grid-cols-[112px_1fr] gap-x-3 gap-y-2 text-xs">
                  <dt className="text-muted-foreground">计划</dt><dd className="font-medium">{activeRun.title}</dd>
                  <dt className="text-muted-foreground">模型与渠道</dt><dd>{activeRun.model} · {activeRun.channel}</dd>
                  <dt className="text-muted-foreground">负载</dt><dd>{activeRun.loadProfile}</dd>
                  <dt className="text-muted-foreground">完成</dt><dd className="tabular-nums">{activeRun.completed}/{activeRun.total || "定时"}</dd>
                  <dt className="text-muted-foreground">Core 状态</dt><dd>{activeRun.coreStatus}</dd>
                </dl>
              ) : (
                <Empty className="min-h-40 border-0 p-0">
                  <EmptyTitle>当前没有活动运行</EmptyTitle>
                  <EmptyDescription>从测试计划创建运行后，这里会显示权威生命周期状态。</EmptyDescription>
                </Empty>
              )}
            </section>

            <section aria-labelledby="readiness-heading" className="min-h-52 border-b px-4 py-3">
              <h2 id="readiness-heading" className="text-sm font-semibold">目录就绪度</h2>
              <dl className="mt-3 grid grid-cols-[128px_1fr] gap-x-3 gap-y-2 text-xs">
                <dt className="text-muted-foreground">启用渠道</dt><dd>{enabledChannels}/{catalog.channels.length}</dd>
                <dt className="text-muted-foreground">已配置凭据</dt><dd>{configuredChannels}/{catalog.channels.length}</dd>
                <dt className="text-muted-foreground">模型映射</dt><dd>{catalog.channel_models.length} 条</dd>
                <dt className="text-muted-foreground">可复用套件</dt><dd>{catalog.suites.length} 个</dd>
                <dt className="text-muted-foreground">本地秘密</dt><dd>仅保存在操作系统密钥环</dd>
              </dl>
            </section>
          </div>

          <section aria-labelledby="recent-reports-heading" className="px-4 py-3">
            <div className="flex items-center justify-between">
              <h2 id="recent-reports-heading" className="text-sm font-semibold">最近报告</h2>
              <span className="text-[11px] text-muted-foreground">结论由 Go Core 提供</span>
            </div>
            {recentReports.length === 0 ? (
              <p className="mt-3 text-xs text-muted-foreground">尚无已封存报告。</p>
            ) : (
              <div className="mt-2 divide-y border-y">
                {recentReports.map((report) => (
                  <div key={report.id} className="grid grid-cols-[84px_1fr_180px_110px] items-center gap-3 py-2 text-xs">
                    <Badge
                      variant="outline"
                      className={report.passed ? "border-success/25 bg-success-soft text-success-strong" : "border-destructive/25 bg-destructive-soft text-destructive"}
                    >
                      {report.passed ? "通过" : "未通过"}
                    </Badge>
                    <span className="truncate font-medium">{report.verdict}</span>
                    <span className="truncate text-muted-foreground">{report.model_name} · {report.channel_name}</span>
                    <span className="text-right tabular-nums text-muted-foreground">{report.failed_case_count}/{report.case_count} 失败</span>
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

function SummaryCell({ value, label, last = false }: { value: string; label: string; last?: boolean }) {
  return (
    <div className={last ? "px-4 py-3" : "border-r px-4 py-3"}>
      <div className="text-sm font-semibold tabular-nums">{value}</div>
      <div className="mt-1 text-[10px] text-muted-foreground">{label}</div>
    </div>
  )
}
