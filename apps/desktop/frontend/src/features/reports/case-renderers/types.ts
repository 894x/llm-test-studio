import type { ComponentType } from "react"

import type { ReportCaseDetail, ReportSuiteDetail } from "../data"

export interface CaseRendererContext {
  suite: Readonly<Omit<ReportSuiteDetail, "cases">>
  caseReport: ReportCaseDetail
  dataVersion: number
  requestLimit: number
  totalRequestCount: number
  hasMore: boolean
}

export interface CaseRendererComponentProps {
  context: CaseRendererContext
  payload: unknown
}

export type CaseRendererMatch =
  | { caseType: string }
  | { caseTypeFamily: string }
  | { generic: true }

export interface CaseRendererDefinition {
  match: CaseRendererMatch
  caseTypeVersions?: readonly number[]
  dataVersions?: readonly number[]
  parse: (context: CaseRendererContext) => unknown
  Component: ComponentType<CaseRendererComponentProps>
}

export interface CaseRendererRegistry {
  candidates: (
    caseType: string | undefined,
    caseTypeVersion: number | undefined,
    dataVersion: number,
  ) => CaseRendererDefinition[]
  resolve: (
    caseType: string | undefined,
    caseTypeVersion: number | undefined,
    dataVersion: number,
  ) => CaseRendererDefinition
  generic: CaseRendererDefinition
}

export interface CaseRendererModule {
  default: CaseRendererDefinition
}
