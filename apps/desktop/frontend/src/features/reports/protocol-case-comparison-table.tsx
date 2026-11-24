import { Fragment, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import ChevronDownIcon from "lucide-react/dist/esm/icons/chevron-down.mjs"
import ChevronRightIcon from "lucide-react/dist/esm/icons/chevron-right.mjs"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { ProtocolComparisonColumns, ProtocolComparisonHeader } from "./comparison-channel-header"
import type { ReportCaseDetail } from "./data"
import { VerificationBadges } from "./entry-case-table"

type Lane = { id: string; label: string; baseline?: boolean }
type Cell = { item?: ReportCaseDetail; empty?: string; selected?: boolean; onOpen?: () => void }
type Row = { key: string; name: string; detailID: string; detail?: ReactNode; cells: Cell[]; onToggle?: () => void }

export function ProtocolCaseComparisonTable({ lanes, rows, exporting = false }: {
  lanes: Lane[]
  rows: Row[]
  exporting?: boolean
}) {
  const { t } = useTranslation("reports")
  return <Table aria-label={t("compare.table")} className="table-fixed text-xs">
    <ProtocolComparisonColumns />
    <TableHeader><TableRow>
      <TableHead scope="col" className="h-8">{t("protocolDesign.caseName")}</TableHead>
      {lanes.map(lane => <ProtocolComparisonHeader key={lane.id} label={lane.label} baseline={lane.baseline} />)}
    </TableRow></TableHeader>
    <TableBody>{rows.map(row => <Fragment key={row.key}>
      <TableRow data-state={row.detail ? "selected" : undefined}>
        <TableCell>
          {exporting || !row.onToggle ? <span className="font-medium">{row.name}</span> :
            <Button variant="ghost" size="sm" className="h-auto min-h-7 w-full justify-start whitespace-normal px-0 text-left"
              onClick={row.onToggle} aria-expanded={Boolean(row.detail)} aria-controls={row.detailID}>
              {row.detail ? <ChevronDownIcon aria-hidden="true" /> : <ChevronRightIcon aria-hidden="true" />}
              <span className="min-w-0 [overflow-wrap:anywhere]">{row.name}</span>
            </Button>}
        </TableCell>
        {lanes.map((lane, index) => {
          const cell = row.cells[index]
          const item = cell?.item
          if (!item) return <TableCell key={lane.id} className="text-muted-foreground">{cell?.empty}</TableCell>
          const verdict = <VerificationBadges status={item.verification.status} checks={item.verification_checks} summaryOnly />
          return <TableCell key={lane.id}>
            {exporting || !cell.onOpen ? verdict :
              <Button variant="ghost" size="sm" className="h-auto min-h-7 w-full justify-start whitespace-normal px-0 py-1 text-left"
                aria-pressed={Boolean(cell.selected)} aria-controls={row.detailID}
                aria-label={t("comparison.caseDetails", { channel: lane.label, name: row.name })} onClick={cell.onOpen}>{verdict}</Button>}
          </TableCell>
        })}
      </TableRow>
      {row.detail ? <TableRow id={row.detailID}><TableCell colSpan={lanes.length + 1} className="py-3">
        {row.detail}
      </TableCell></TableRow> : null}
    </Fragment>)}</TableBody>
  </Table>
}
