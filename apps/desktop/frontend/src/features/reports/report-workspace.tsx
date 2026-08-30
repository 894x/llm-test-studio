import { useState } from "react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Separator } from "@/components/ui/separator"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  EmptyInspector,
  InspectorHeader,
  InspectorRow,
  PageFrame,
} from "@/features/shell/page-frame"

import type { ReportSnapshot, ReportSummary } from "./data"

export function ReportWorkspace({ snapshot }: { snapshot: ReportSnapshot }) {
  const [selectedID, setSelectedID] = useState("")
  const selected =
    snapshot.reports.find((report) => report.id === selectedID) ?? snapshot.reports[0]

  return (
    <PageFrame
      title="测试报告"
      description="查看由 Go Core 生成的权威结论与指标"
      count={`${snapshot.reports.length} 份报告`}
      inspector={
        selected ? (
          <ReportInspector report={selected} />
        ) : (
          <EmptyInspector label="尚未选择报告" />
        )
      }
      inspectorLabel="报告详情"
    >
      {snapshot.reports.length === 0 ? (
        <ScrollArea className="min-h-0 flex-1 border-t">
          <Empty>
            <EmptyTitle>还没有测试报告</EmptyTitle>
            <EmptyDescription>
              运行完成并封存结论后，报告会出现在这里。
            </EmptyDescription>
          </Empty>
        </ScrollArea>
      ) : (
        <ScrollArea className="min-h-0 flex-1 border-t">
          <Table aria-label="测试报告目录" className="min-w-[760px]">
            <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
              <TableRow className="hover:bg-transparent">
                <TableHead className="h-8 w-[88px] pl-4 text-[11px]">结论</TableHead>
                <TableHead className="h-8 text-[11px]">报告 / 计划</TableHead>
                <TableHead className="h-8 text-[11px]">目标</TableHead>
                <TableHead className="h-8 text-[11px]">用例</TableHead>
                <TableHead className="h-8 text-[11px]">生成时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {snapshot.reports.map((report) => (
                <TableRow
                  key={report.id}
                  data-state={report.id === selected?.id ? "selected" : undefined}
                  aria-selected={report.id === selected?.id}
                  onClick={() => setSelectedID(report.id)}
                  className="dense-table-row h-11"
                >
                  <TableCell className="py-1 pl-4">
                    <ConclusionBadge passed={report.passed} />
                  </TableCell>
                  <TableCell className="py-1">
                    <Button
                      variant="link"
                      size="sm"
                      className="h-auto max-w-[240px] justify-start p-0 text-xs no-underline hover:no-underline"
                      aria-label={`查看报告 ${report.verdict}`}
                    >
                      <span className="truncate">{report.verdict}</span>
                    </Button>
                    <div className="mt-0.5 truncate text-[10px] text-muted-foreground">
                      {report.plan_name}
                    </div>
                  </TableCell>
                  <TableCell className="py-1">
                    <div className="truncate text-xs">{report.model_name}</div>
                    <div className="mt-0.5 truncate text-[10px] text-muted-foreground">
                      {report.channel_name}
                    </div>
                  </TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">
                    {report.case_count - report.failed_case_count}/{report.case_count}
                  </TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">
                    {formatTimestamp(report.generated_at)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </ScrollArea>
      )}
    </PageFrame>
  )
}

function ReportInspector({ report }: { report: ReportSummary }) {
  return (
    <>
      <InspectorHeader
        title={report.verdict}
        subtitle={report.id}
        trailing={<ConclusionBadge passed={report.passed} />}
      />
      <Separator />
      <dl className="space-y-1 px-4 py-2">
        <InspectorRow label="运行" value={`${report.run_status} · ${report.run_id}`} />
        <InspectorRow label="测试计划" value={report.plan_name} />
        <InspectorRow label="模型与渠道" value={`${report.model_name} · ${report.channel_name}`} />
        <InspectorRow
          label="用例结论"
          value={`${report.case_count - report.failed_case_count}/${report.case_count} 通过 · ${report.failed_case_count} 失败`}
        />
        <InspectorRow label="问题" value={`${report.issue_count} 项`} />
        <InspectorRow label="附件" value={`${report.attachment_count} 个已封存`} />
        <InspectorRow label="生成时间" value={formatTimestamp(report.generated_at)} />
      </dl>
    </>
  )
}

function ConclusionBadge({ passed }: { passed: boolean }) {
  return (
    <Badge
      variant="outline"
      className={
        passed
          ? "border-success/25 bg-success-soft text-success-strong"
          : "border-destructive/25 bg-destructive-soft text-destructive"
      }
    >
      {passed ? "通过" : "未通过"}
    </Badge>
  )
}

function formatTimestamp(value: string): string {
  const date = new Date(value)
  const datePart = new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(date)
  return datePart.replaceAll("/", "-")
}
