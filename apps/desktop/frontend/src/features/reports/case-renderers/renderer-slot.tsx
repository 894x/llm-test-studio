import { Component, type ReactNode } from "react"
import { useTranslation } from "react-i18next"

import type { ReportCaseDetail, ReportSuiteDetail } from "../data"
import { CaseReportSection } from "./generic-case.renderer"
import { caseRendererRegistry } from "./registry"
import type {
  CaseRendererContext,
  CaseRendererDefinition,
  CaseRendererRegistry,
} from "./types"

const DEFAULT_REQUEST_LIMIT = 1_000

export function CaseRendererSlot({
  suite,
  caseReport,
  registry = caseRendererRegistry,
  requestLimit = DEFAULT_REQUEST_LIMIT,
}: {
  suite: ReportSuiteDetail
  caseReport: ReportCaseDetail
  registry?: CaseRendererRegistry
  requestLimit?: number
}) {
  const dataVersion = caseDataVersion(suite, caseReport.case_id)
  const totalRequestCount = caseReport.request_results.length
  const boundedCaseReport = totalRequestCount > requestLimit
    ? { ...caseReport, request_results: caseReport.request_results.slice(0, requestLimit) }
    : caseReport
  const { cases: _cases, ...suiteMetadata } = suite
  const context: CaseRendererContext = {
    suite: suiteMetadata,
    caseReport: boundedCaseReport,
    dataVersion,
    requestLimit,
    totalRequestCount,
    hasMore: totalRequestCount > boundedCaseReport.request_results.length,
  }
  const candidates = registry.candidates(caseReport.case_type, caseReport.case_type_version, dataVersion)
  return (
    <RendererCandidateChain
      key={`${suite.suite_entry_id ?? suite.suite_key}:${caseReport.case_id}`}
      candidates={candidates}
      context={context}
      resetToken={caseReport}
    />
  )
}

function RendererCandidateChain({
  candidates,
  context,
  index = 0,
  resetToken,
}: {
  candidates: CaseRendererDefinition[]
  context: CaseRendererContext
  index?: number
  resetToken: ReportCaseDetail
}) {
  const current = candidates[index]
  if (!current) return <TerminalCaseRendererFallback context={context} />
  return (
    <CaseRendererErrorBoundary resetToken={resetToken} fallback={(
      <RendererCandidateChain candidates={candidates} context={context} index={index + 1} resetToken={resetToken} />
    )}>
      <ResolvedCaseRenderer definition={current} context={context} />
    </CaseRendererErrorBoundary>
  )
}

function ResolvedCaseRenderer({
  definition,
  context,
}: {
  definition: CaseRendererDefinition
  context: CaseRendererContext
}) {
  const payload = definition.parse(context)
  return <definition.Component context={context} payload={payload} />
}

function TerminalCaseRendererFallback({ context }: { context: CaseRendererContext }) {
  const { t } = useTranslation("reports")
  return (
    <CaseReportSection context={context}>
      <p role="alert" className="px-3 py-4 text-xs text-destructive">
        {t("hierarchy.rendererUnavailable")}
      </p>
    </CaseReportSection>
  )
}

function caseDataVersion(suite: ReportSuiteDetail, caseID: string): number {
  const versions = new Set<number>()
  for (const distribution of suite.distributions) {
    if (distribution.case_id !== caseID || distribution.schema_version === undefined) continue
    if (!Number.isSafeInteger(distribution.schema_version) || (distribution.schema_version as number) <= 0) return 0
    versions.add(distribution.schema_version as number)
  }
  if (versions.size > 1) return 0
  return versions.values().next().value ?? 1
}

class CaseRendererErrorBoundary extends Component<{
  children: ReactNode
  fallback: ReactNode
  resetToken: unknown
}, { failed: boolean; resetToken: unknown }> {
  state = { failed: false, resetToken: this.props.resetToken }

  static getDerivedStateFromProps(
    props: { resetToken: unknown },
    state: { failed: boolean; resetToken: unknown },
  ) {
    return props.resetToken === state.resetToken
      ? null
      : { failed: false, resetToken: props.resetToken }
  }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  render() {
    return this.state.failed ? this.props.fallback : this.props.children
  }
}
