import { afterEach, describe, expect, it } from "vitest"

import { standaloneReportHTML } from "./visual-report-export"

describe("standaloneReportHTML", () => {
  afterEach(() => {
    document.documentElement.className = ""
    delete document.documentElement.dataset.theme
  })

  it("serializes the shared report node with its charts, watermark, and active theme", () => {
    document.documentElement.className = "dark"
    document.documentElement.dataset.theme = "dark"
    const report = document.createElement("article")
    report.innerHTML = '<figure aria-label="TTFT 分布图"></figure><div data-report-watermark>team-alpha</div>'

    const exported = standaloneReportHTML(report, 'report-<unsafe>')

    expect(exported).toContain('<html lang="zh-CN" class="dark" data-theme="dark">')
    expect(exported).toContain('aria-label="TTFT 分布图"')
    expect(exported).toContain("team-alpha")
    expect(exported).toContain("LLM Test Studio Report report-&lt;unsafe&gt;")
  })
})
