import { render, screen } from "@testing-library/react"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it } from "vitest"

import { createAppI18n } from "@/i18n/i18n"
import type { CatalogCaseSeverity } from "@/features/catalog/data"
import { CaseSeverityBadge } from "./case-severity-badge"

describe("CaseSeverityBadge", () => {
  it.each([
    { severity: "critical", label: "必过" },
    { severity: "normal", label: "容忍" },
  ] satisfies { severity: CatalogCaseSeverity; label: string }[])(
    "renders $severity as a short neutral requirement rather than an outcome",
    ({ severity, label }) => {
      render(<I18nextProvider i18n={createAppI18n("zh-CN")}><CaseSeverityBadge severity={severity} /></I18nextProvider>)
      expect(screen.getByText(label)).toHaveClass("text-muted-foreground")
      expect(screen.getByText(label)).not.toHaveAttribute("data-slot", "badge")
      expect(screen.getByTitle("用例重要性等级，不代替本次验证结果。")).toHaveTextContent(label)
    },
  )
})
