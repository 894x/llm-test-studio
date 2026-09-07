export const DESKTOP_PAGES = [
  { id: "overview", labelKey: "navigation.overview" },
  { id: "quick-test", labelKey: "navigation.quickTest" },
  { id: "catalog", labelKey: "navigation.catalog" },
  { id: "cases", labelKey: "navigation.cases" },
  { id: "plans", labelKey: "navigation.plans" },
  { id: "runs", labelKey: "navigation.runs" },
  { id: "reports", labelKey: "navigation.reports" },
] as const

export type DesktopPage = (typeof DESKTOP_PAGES)[number]["id"]

export function desktopPageFromHash(hash: string): DesktopPage {
  const candidate = hash.replace(/^#\/?/, "")
  return DESKTOP_PAGES.some((page) => page.id === candidate)
    ? (candidate as DesktopPage)
    : "runs"
}
