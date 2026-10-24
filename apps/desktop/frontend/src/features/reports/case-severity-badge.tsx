import { useTranslation } from "react-i18next"

import { Badge } from "@/components/ui/badge"
import type { CatalogCaseSeverity } from "@/features/catalog/data"

export function CaseSeverityBadge({ severity }: { severity: CatalogCaseSeverity }) {
  const { t } = useTranslation("reports")
  const critical = severity === "critical"
  return (
    <Badge
      variant="outline"
      className={critical
        ? "border-destructive/25 bg-destructive-soft text-destructive"
        : "border-info/25 bg-info-soft text-info-strong"}
      title={t("protocolDesign.severityHint")}
    >
      {t(`protocolDesign.severityLevel.${severity}`)}
    </Badge>
  )
}
