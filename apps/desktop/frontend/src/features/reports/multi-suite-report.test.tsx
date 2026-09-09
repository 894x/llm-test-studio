import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it, vi } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import { protocolReportFixture, protocolReportSnapshot, reportID } from "@/test/protocol-report-fixture"
import { ReportWorkspace } from "./report-workspace"
import { EntryCaseTable } from "./entry-case-table"

describe("compact protocol reports", () => {
  it("groups cases under one entry header, displays names and snapshot parameters, and expands assertions", async () => {
    const detail = protocolReportFixture(); const user = userEvent.setup()
    render(<I18nextProvider i18n={createAppI18n("en-US")}><ReportWorkspace snapshot={protocolReportSnapshot(detail)} preferredReportID={detail.report.id} getDetail={async () => detail} exportReport={vi.fn()} saveReportExport={vi.fn()} copyReportPNG={vi.fn()} /></I18nextProvider>)
    const table = await screen.findByRole("table", { name: "Cases in execution entry" })
    expect(screen.getByText("Snapshot suite")).toBeVisible()
    expect(screen.getByText("content_length: 4000")).toBeVisible()
    expect(within(table).getAllByRole("columnheader", { name: "Case name" })).toHaveLength(1)
    expect(screen.queryByText("opaque-request-id")).not.toBeInTheDocument()
    const rowButton = within(table).getByRole("button", { name: "Reject invalid parameter" })
    await user.click(rowButton)
    expect(rowButton).toHaveAttribute("aria-expanded", "true")
    expect(screen.getByRole("table", { name: "Assertion results" })).toHaveTextContent("expected-status")
    expect(screen.getByText("opaque-request-id")).toBeVisible()
  })
  it("renders empty assertions as observed and uses backend summary counts", () => {
    const entry = protocolReportFixture("not_applicable").entries[0]
    entry.cases[0].verification.observed = 125
    render(<I18nextProvider i18n={createAppI18n("en-US")}><EntryCaseTable entry={entry} /></I18nextProvider>)
    expect(screen.getByText("Observed")).toBeVisible()
    expect(screen.getByText(/125 observed/)).toBeVisible()
    expect(screen.queryByText("Passed")).not.toBeInTheDocument()
  })
  it("bounds default rows and exposes the next page with keyboard-operable controls", async () => {
    const entry = protocolReportFixture().entries[0]; const seed = entry.cases[0]
    entry.cases = Array.from({ length: 80 }, (_, index) => ({ ...seed, case_id: reportID(100 + index), name: `Case ${index}` }))
    const user = userEvent.setup()
    render(<I18nextProvider i18n={createAppI18n("en-US")}><EntryCaseTable entry={entry} /></I18nextProvider>)
    expect(screen.queryByRole("button", { name: "Case 50" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Next" }))
    expect(screen.getByRole("button", { name: "Case 50" })).toBeVisible()
  })
})
