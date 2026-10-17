import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it } from "vitest"

import { createAppI18n } from "@/i18n/i18n"
import en from "@/i18n/resources/en-US/execution-origin.json"
import zh from "@/i18n/resources/zh-CN/execution-origin.json"
import { ExecutionOriginDetails } from "./execution-origin-details"
import type { ExecutionOrigin } from "./execution-origin"

const origin: ExecutionOrigin = { kind: "remote", node_id: "10000000-0000-4000-8000-000000000020",
  installation_id: "10000000-0000-4000-8000-000000000021", engine_build_id: "a".repeat(64),
  runner_version: "v".repeat(128), os: "linux", arch: "arm64" }

function show(value: ExecutionOrigin | null, locale: "zh-CN" | "en-US" = "zh-CN") {
  const i18n = createAppI18n(locale)
  i18n.addResourceBundle("zh-CN", "executionOrigin", zh)
  i18n.addResourceBundle("en-US", "executionOrigin", en)
  return render(<I18nextProvider i18n={i18n}><div className="w-[288px]"><ExecutionOriginDetails origin={value} /></div></I18nextProvider>)
}

describe("ExecutionOriginDetails", () => {
  it("shows no recorded host for null and does not claim a local runner", () => {
    show(null)
    expect(screen.getByText("未记录执行宿主来源。")).toBeVisible()
    expect(screen.queryByText("本地 Runner")).not.toBeInTheDocument()
    expect(screen.queryByRole("button")).not.toBeInTheDocument()
  })

  it("exposes the full executor identity by keyboard and allows long values to wrap", async () => {
    const user = userEvent.setup()
    show(origin)
    const region = screen.getByRole("region", { name: "实际执行来源" })
    expect(within(region).getByText("远程 Runner")).toBeVisible()
    expect(within(region).getByText("linux/arm64")).toBeVisible()
    const version = within(region).getByText(origin.runner_version)
    expect(version).toHaveAttribute("title", origin.runner_version)
    expect(version).toHaveClass("[overflow-wrap:anywhere]")
    const identity = screen.getByRole("button", { name: "执行身份" })
    expect(identity).toHaveAttribute("aria-expanded", "false")
    expect(within(region).getByText(origin.engine_build_id)).not.toBeVisible()
    await user.tab()
    expect(identity).toHaveFocus()
    await user.keyboard("{Enter}")
    expect(identity).toHaveAttribute("aria-expanded", "true")
    expect(within(region).getByText(origin.node_id)).toBeVisible()
    expect(within(region).getByText(origin.installation_id)).toBeVisible()
    expect(within(region).getByText(origin.engine_build_id)).toBeVisible()
    expect(within(region).getByText(origin.engine_build_id)).toHaveClass("[overflow-wrap:anywhere]")
    await user.keyboard(" ")
    expect(identity).toHaveAttribute("aria-expanded", "false")
    expect(identity).toHaveFocus()
  })

  it("renders the local origin and unknown provenance in English", () => {
    const view = show({ ...origin, kind: "local" }, "en-US")
    expect(screen.getByText("Local runner")).toBeVisible()
    expect(screen.getByRole("heading", { name: "Execution origin" })).toBeVisible()
    view.unmount()
    show(null, "en-US")
    expect(screen.getByText("No execution host origin was recorded.")).toBeVisible()
  })

  it("keeps English and Chinese resource keys in parity", () => {
    expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort())
  })
})
