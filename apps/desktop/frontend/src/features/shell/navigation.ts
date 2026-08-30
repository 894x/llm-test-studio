export const DESKTOP_PAGES = [
  { id: "overview", label: "总览" },
  { id: "catalog", label: "模型与渠道" },
  { id: "cases", label: "用例" },
  { id: "plans", label: "计划" },
  { id: "runs", label: "运行" },
  { id: "reports", label: "报告" },
] as const

export type DesktopPage = (typeof DESKTOP_PAGES)[number]["id"]

export function desktopPageFromHash(hash: string): DesktopPage {
  const candidate = hash.replace(/^#\/?/, "")
  return DESKTOP_PAGES.some((page) => page.id === candidate)
    ? (candidate as DesktopPage)
    : "runs"
}
