import { useMemo, useState } from "react"
import GitCompareArrowsIcon from "lucide-react/dist/esm/icons/git-compare-arrows.mjs"

import { publicDesktopErrorMessage } from "@/app/desktop-client"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldLabel } from "@/components/ui/field"
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { CatalogSnapshot } from "@/features/catalog/data"

import type { ComparisonSnapshot, StartComparisonCommand } from "./data"

export function NewComparisonSheet({
  catalog,
  pending,
  onStart,
}: {
  catalog: CatalogSnapshot
  pending: boolean
  onStart: (command: StartComparisonCommand) => Promise<void>
}) {
  const eligiblePlans = useMemo(
    () => catalog.plans.filter((plan) => plan.model_ids.length > 0 && plan.channel_ids.length >= 2),
    [catalog.plans],
  )
  const [open, setOpen] = useState(false)
  const [planID, setPlanID] = useState(eligiblePlans[0]?.id ?? "")
  const plan = eligiblePlans.find((item) => item.id === planID) ?? eligiblePlans[0]
  const models = catalog.models.filter((model) => plan?.model_ids.includes(model.id))
  const [modelID, setModelID] = useState(models[0]?.id ?? "")
  const effectiveModelID = models.some((model) => model.id === modelID) ? modelID : (models[0]?.id ?? "")
  const channels = catalog.channels.filter((channel) =>
    plan?.channel_ids.includes(channel.id) && channel.enabled && channel.credential_configured &&
    catalog.channel_models.some((mapping) => mapping.channel_id === channel.id && mapping.model_id === effectiveModelID),
  )
  const [selected, setSelected] = useState<string[]>([])
  const effectiveSelected = selected.filter((id) => channels.some((channel) => channel.id === id))
  const [error, setError] = useState("")

  const changePlan = (next: string) => {
    setPlanID(next)
    const nextPlan = eligiblePlans.find((item) => item.id === next)
    setModelID(nextPlan?.model_ids[0] ?? "")
    setSelected([])
  }
  const changeModel = (next: string) => {
    setModelID(next)
    setSelected([])
  }
  const start = async () => {
    if (!plan || !effectiveModelID || effectiveSelected.length < 2) return
    setError("")
    try {
      await onStart({ plan_id: plan.id, model_id: effectiveModelID, channel_ids: effectiveSelected })
      setOpen(false)
    } catch (caught) {
      setError(publicDesktopErrorMessage(caught, "无法启动渠道对比，请检查本地日志"))
    }
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button size="sm" variant="outline">
          <GitCompareArrowsIcon data-icon="inline-start" /> 渠道对比
        </Button>
      </SheetTrigger>
      <SheetContent className="sm:max-w-[440px]">
        <SheetHeader>
          <SheetTitle>同模型渠道对比</SheetTitle>
          <SheetDescription>固定一个模型，同时在至少两个渠道上独立运行同一测试计划。</SheetDescription>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4">
          {eligiblePlans.length === 0 ? (
            <p className="text-xs text-muted-foreground">需要先创建一个包含同一模型至少两个渠道的计划。</p>
          ) : (
            <>
              <SelectField label="测试计划" value={plan?.id ?? ""} options={eligiblePlans.map((item) => [item.id, item.name])} onChange={changePlan} />
              <SelectField label="逻辑模型" value={effectiveModelID} options={models.map((item) => [item.id, item.name])} onChange={changeModel} />
              <div>
                <div className="text-xs font-semibold">选择渠道</div>
                <div className="mt-2 space-y-2">
                  {channels.map((channel) => (
                    <Field key={channel.id} className="rounded-md border px-3 py-2">
                      <Checkbox
                        id={`compare-${channel.id}`}
                        checked={effectiveSelected.includes(channel.id)}
                        onCheckedChange={(checked) => setSelected((current) => checked === true ? [...current, channel.id] : current.filter((id) => id !== channel.id))}
                      />
                      <FieldLabel htmlFor={`compare-${channel.id}`} className="min-w-0 flex-1">
                        <span className="block truncate">{channel.name}</span>
                        <span className="block truncate text-[10px] font-normal text-muted-foreground">{channel.base_url}</span>
                      </FieldLabel>
                    </Field>
                  ))}
                  {channels.length < 2 ? <p className="text-xs text-destructive">当前模型没有至少两个已启用、已配置密钥的可用渠道。</p> : null}
                </div>
              </div>
            </>
          )}
          {error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
        </div>
        <SheetFooter className="flex-row justify-end border-t">
          <SheetClose asChild><Button variant="outline">取消</Button></SheetClose>
          <Button disabled={pending || effectiveSelected.length < 2} onClick={() => void start()}>
            {pending ? "正在启动…" : `对比 ${effectiveSelected.length} 个渠道`}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

export function ComparisonPanel({ snapshot }: { snapshot: ComparisonSnapshot }) {
  const comparison = snapshot.comparisons[0]
  if (!comparison) return null
  return (
    <section aria-labelledby="comparison-heading" className="mx-4 mb-3 rounded-md border bg-card">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <div className="min-w-0">
          <h2 id="comparison-heading" className="truncate text-xs font-semibold">最近渠道对比 · {comparison.model_name}</h2>
          <p className="mt-0.5 truncate text-[10px] text-muted-foreground">{comparison.plan_name} · {comparison.channels.length} 个独立运行</p>
        </div>
        <Badge variant="outline">{comparison.status === "running" ? "运行中" : comparison.status === "completed" ? "已完成" : comparison.status === "failed" ? "失败" : "已取消"}</Badge>
      </div>
      <Table aria-label="渠道对比结果">
        <TableHeader><TableRow>
          <TableHead className="h-8 text-[11px]">渠道</TableHead>
          <TableHead className="h-8 text-[11px]">结论</TableHead>
          <TableHead className="h-8 text-[11px]">成功率</TableHead>
          <TableHead className="h-8 text-[11px]">平均 E2E</TableHead>
          <TableHead className="h-8 text-[11px]">平均 TTFT</TableHead>
        </TableRow></TableHeader>
        <TableBody>{comparison.channels.map((channel) => (
          <TableRow key={channel.run_id} className="h-9">
            <TableCell className="py-1 text-xs font-medium">{channel.channel_name}</TableCell>
            <TableCell className="py-1 text-xs">{channel.report_ready ? (channel.passed ? "通过" : channel.verdict || "未通过") : channel.run_status}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatMetric(channel.metrics.success_rate, true)}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatMetric(channel.metrics.e2e_ms)}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatMetric(channel.metrics.ttft_ms)}</TableCell>
          </TableRow>
        ))}</TableBody>
      </Table>
    </section>
  )
}

function SelectField({ label, value, options, onChange }: { label: string; value: string; options: [string, string][]; onChange: (value: string) => void }) {
  return <Field className="block"><FieldLabel>{label}</FieldLabel><Select value={value} onValueChange={onChange}><SelectTrigger aria-label={label} className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{options.map(([id, text]) => <SelectItem key={id} value={id}>{text}</SelectItem>)}</SelectGroup></SelectContent></Select></Field>
}

function formatMetric(metric: { value: number; unit: string } | undefined, ratio = false): string {
  if (!metric) return "—"
  if (ratio || metric.unit === "ratio") return `${(metric.value * 100).toFixed(1)}%`
  if (metric.unit === "ms") return `${metric.value.toFixed(0)} ms`
  return metric.value.toFixed(2)
}
