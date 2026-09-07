import { DesktopDataError } from "@/app/data-error"
import { translateDesktop as tx } from "@/i18n/runtime"
import type { CoreRunStatus } from "@/features/runs/data"

export interface ComparisonMetric {
  value: number
  unit: string
  samples: number
}

export interface ComparisonChannelResult {
  channel_id: string
  channel_name: string
  run_id: string
  run_status: CoreRunStatus
  report_ready: boolean
  passed: boolean
  verdict: string
  metrics: Record<string, ComparisonMetric>
}

export interface ComparisonSummary {
  id: string
  created_at: string
  status: "running" | "completed" | "failed" | "cancelled"
  plan_id: string
  plan_name: string
  model_id: string
  model_name: string
  channels: ComparisonChannelResult[]
}

export interface ComparisonSnapshot {
  schema_version: 1
  comparisons: ComparisonSummary[]
}

export interface StartComparisonCommand {
  plan_id: string
  model_id: string
  channel_ids: string[]
}

export const EMPTY_COMPARISONS: ComparisonSnapshot = {
  schema_version: 1,
  comparisons: [],
}

export function parseComparisonSnapshot(value: unknown): ComparisonSnapshot {
  if (!isRecord(value) || value.schema_version !== 1 || !Array.isArray(value.comparisons)) {
    throw new DesktopDataError(tx("desktop:comparisons_invalid_channel_comparison_data_protocol"))
  }
  const comparisons = value.comparisons.map(parseComparison)
  if (new Set(comparisons.map((item) => item.id)).size !== comparisons.length) {
    throw new DesktopDataError(tx("desktop:comparisons_duplicate_channel_comparison_identifiers"))
  }
  return { schema_version: 1, comparisons }
}

function parseComparison(value: unknown): ComparisonSummary {
  if (
    !isRecord(value) || !isUUID(value.id) || !isUTCDate(value.created_at) ||
    !isComparisonStatus(value.status) || !isUUID(value.plan_id) || !isNonBlank(value.plan_name) ||
    !isUUID(value.model_id) || !isNonBlank(value.model_name) || !Array.isArray(value.channels) || value.channels.length < 2
  ) {
    throw new DesktopDataError(tx("desktop:comparisons_invalid_channel_comparison_summary"))
  }
  const channels = value.channels.map(parseChannel)
  if (
    new Set(channels.map((item) => item.channel_id)).size !== channels.length ||
    new Set(channels.map((item) => item.run_id)).size !== channels.length
  ) {
    throw new DesktopDataError(tx("desktop:comparisons_duplicate_channel_comparison_run_reference"))
  }
  return {
    id: value.id, created_at: value.created_at, status: value.status,
    plan_id: value.plan_id, plan_name: value.plan_name,
    model_id: value.model_id, model_name: value.model_name, channels,
  }
}

function parseChannel(value: unknown): ComparisonChannelResult {
  if (
    !isRecord(value) || !isUUID(value.channel_id) || !isNonBlank(value.channel_name) ||
    !isUUID(value.run_id) || !isRunStatus(value.run_status) ||
    typeof value.report_ready !== "boolean" || typeof value.passed !== "boolean" ||
    typeof value.verdict !== "string" || !isRecord(value.metrics)
  ) {
    throw new DesktopDataError(tx("desktop:comparisons_invalid_channel_comparison_result"))
  }
  const metrics: Record<string, ComparisonMetric> = {}
  for (const [name, metric] of Object.entries(value.metrics)) {
		if (!isNonBlank(name) || !isRecord(metric)) {
			throw new DesktopDataError(tx("desktop:comparisons_invalid_channel_comparison_metrics"))
		}
		const metricValue = metric.value
		const samples = metric.samples
		if (typeof metricValue !== "number" || !Number.isFinite(metricValue) ||
			!isNonBlank(metric.unit) || typeof samples !== "number" || !Number.isInteger(samples) || samples < 0) {
      throw new DesktopDataError(tx("desktop:comparisons_invalid_channel_comparison_metrics"))
    }
		metrics[name] = { value: metricValue, unit: metric.unit, samples }
  }
  return {
    channel_id: value.channel_id, channel_name: value.channel_name, run_id: value.run_id,
    run_status: value.run_status, report_ready: value.report_ready, passed: value.passed,
    verdict: value.verdict, metrics,
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
function isUUID(value: unknown): value is string {
  return typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value)
}
function isNonBlank(value: unknown): value is string {
  return typeof value === "string" && value.trim() === value && value.length > 0
}
function isUTCDate(value: unknown): value is string {
  return typeof value === "string" && !Number.isNaN(Date.parse(value)) && (value.endsWith("Z") || value.endsWith("+00:00"))
}
function isComparisonStatus(value: unknown): value is ComparisonSummary["status"] {
  return value === "running" || value === "completed" || value === "failed" || value === "cancelled"
}
function isRunStatus(value: unknown): value is CoreRunStatus {
  return value === "queued" || value === "starting" || value === "running" || value === "draining" || value === "completed" || value === "failed" || value === "cancelled"
}
