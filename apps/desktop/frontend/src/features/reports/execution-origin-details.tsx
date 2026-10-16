import { useId, useState } from "react"
import { useTranslation } from "react-i18next"
import ChevronDownIcon from "lucide-react/dist/esm/icons/chevron-down.mjs"
import ChevronRightIcon from "lucide-react/dist/esm/icons/chevron-right.mjs"

import { Button } from "@/components/ui/button"
import type { ExecutionOrigin } from "./execution-origin"

export function ExecutionOriginDetails({ origin }: { origin: ExecutionOrigin | null }) {
  const { t } = useTranslation("executionOrigin")
  const detailsID = useId()
  const [expanded, setExpanded] = useState(false)
  return <section aria-label={t("title")} className="min-w-0 space-y-2">
    <h4 className="text-xs font-semibold">{t("title")}</h4>
    {origin === null ? <p className="text-[11px] text-muted-foreground">{t("unrecorded")}</p> : <>
      <dl className="min-w-0 space-y-2 text-xs">
        <OriginProperty label={t("location")} value={t(origin.kind)} />
        <OriginProperty label={t("runnerVersion")} value={origin.runner_version} />
        <OriginProperty label={t("platform")} value={`${origin.os}/${origin.arch}`} />
      </dl>
      <Button variant="ghost" size="sm" className="max-w-full px-0 text-xs" aria-expanded={expanded} aria-controls={detailsID}
        onClick={() => setExpanded(value => !value)}>
        {expanded ? <ChevronDownIcon /> : <ChevronRightIcon />}{t("identity")}
      </Button>
      <dl id={detailsID} hidden={!expanded} className="min-w-0 space-y-2 text-xs">
        <OriginProperty label={t("nodeID")} value={origin.node_id} mono />
        <OriginProperty label={t("installationID")} value={origin.installation_id} mono />
        <OriginProperty label={t("buildID")} value={origin.engine_build_id} mono />
      </dl>
    </>}
  </section>
}

function OriginProperty({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div className="min-w-0">
    <dt className="text-[11px] text-muted-foreground">{label}</dt>
    <dd className={`mt-0.5 [overflow-wrap:anywhere] ${mono ? "font-mono" : ""}`} title={value}>{value}</dd>
  </div>
}
