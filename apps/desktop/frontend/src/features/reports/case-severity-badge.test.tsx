import { render, screen } from "@testing-library/react"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it } from "vitest"

import { createAppI18n } from "@/i18n/i18n"
import type { CatalogCaseSeverity } from "@/features/catalog/data"
import { CaseSeverityBadge } from "./case-severity-badge"

describe("CaseSeverityBadge", () => {
  it.each([
    { severity: "critical", label: "必过项", background: "bg-destructive-soft", foreground: "text-destructive" },
    { severity: "normal", label: "容忍项", background: "bg-info-soft", foreground: "text-info-strong" },
  ] satisfies { severity: CatalogCaseSeverity; label: string; background: string; foreground: string }[])(
    "renders $severity with text and an independent semantic surface",
    ({ severity, label, background, foreground }) => {
      render(<I18nextProvider i18n={createAppI18n("zh-CN")}><CaseSeverityBadge severity={severity} /></I18nextProvider>)
      expect(screen.getByText(label)).toHaveClass(background, foreground)
      expect(screen.getByTitle("用例重要性等级，不代替本次验证结果。")).toHaveTextContent(label)
    },
  )
})
