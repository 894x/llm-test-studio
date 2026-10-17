import { useId, useState } from "react"
import { useTranslation } from "react-i18next"
import ChevronDownIcon from "lucide-react/dist/esm/icons/chevron-down.mjs"
import ChevronRightIcon from "lucide-react/dist/esm/icons/chevron-right.mjs"
import MonitorIcon from "lucide-react/dist/esm/icons/monitor.mjs"
import ServerIcon from "lucide-react/dist/esm/icons/server.mjs"

import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import type { ExecutionOrigin } from "./execution-origin"

export function ExecutionOriginDetails({ origin }: { origin: ExecutionOrigin | null }) {
  const { t } = useTranslation("executionOrigin")
  const detailsID = useId()
  const [expanded, setExpanded] = useState(false)
  const HostIcon = origin?.kind === "remote" ? ServerIcon : MonitorIcon
  return <section aria-label={t("title")} className="min-w-0 space-y-3">
    <h4 className="text-[11px] font-medium text-muted-foreground">{t("title")}</h4>
    {origin === null ? <p className="text-[11px] text-muted-foreground">{t("unrecorded")}</p> : <>
      <div className="min-w-0 space-y-3 text-xs">
        <dl className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
          <div className="min-w-0">
            <dt className="sr-only">{t("location")}</dt>
            <dd className="flex items-center gap-2 text-sm font-medium">
              <HostIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
              <span className="[overflow-wrap:anywhere]">{t(origin.kind)}</span>
            </dd>
          </div>
          <div className="min-w-0">
            <dt className="sr-only">{t("platform")}</dt>
            <dd className="rounded-sm bg-surface-subtle px-1.5 py-0.5 font-mono text-[11px] text-text-secondary [overflow-wrap:anywhere]">
              {`${origin.os}/${origin.arch}`}
            </dd>
          </div>
        </dl>
        <dl><OriginProperty label={t("runnerVersion")} value={origin.runner_version} /></dl>
      </div>
      <div className="min-w-0">
        <Separator className="mb-1" />
        <Button variant="ghost" size="sm" className="w-full justify-between px-1 text-[11px] font-normal text-muted-foreground" aria-expanded={expanded} aria-controls={detailsID}
          onClick={() => setExpanded(value => !value)}>
          {t("identity")}{expanded ? <ChevronDownIcon aria-hidden="true" /> : <ChevronRightIcon aria-hidden="true" />}
        </Button>
        <dl id={detailsID} hidden={!expanded} className="min-w-0 space-y-3 pt-3 text-xs">
          <OriginProperty label={t("nodeID")} value={origin.node_id} mono />
          <OriginProperty label={t("installationID")} value={origin.installation_id} mono />
          <OriginProperty label={t("buildID")} value={origin.engine_build_id} mono />
        </dl>
      </div>
    </>}
  </section>
}

function OriginProperty({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div className="min-w-0">
    <dt className="text-[11px] text-muted-foreground">{label}</dt>
    <dd className={`mt-1 leading-relaxed text-text-secondary [overflow-wrap:anywhere] ${mono ? "select-text font-mono text-[11px]" : ""}`} title={value}>{value}</dd>
  </div>
}
