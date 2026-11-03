import { useTranslation } from "react-i18next"

import type { CatalogCaseSeverity } from "@/features/catalog/data"

export function CaseSeverityBadge({ severity }: { severity: CatalogCaseSeverity }) {
  const { t } = useTranslation("reports")
  return (
    <span
      className="text-xs text-muted-foreground"
      title={t("protocolDesign.severityHint")}
    >
      {t(`protocolDesign.severityLevel.${severity}`)}
    </span>
  )
}
