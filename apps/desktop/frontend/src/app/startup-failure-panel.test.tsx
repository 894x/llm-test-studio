import { render, screen, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { StartupFailurePanel } from "./startup-failure-panel"
import { READY_STARTUP, type StartupFailureCode } from "./startup-progress"

describe("startup recovery guidance", () => {
  it.each<[StartupFailureCode, string, string]>([
    ["workspace_busy", "本地工作区正在被占用", "系统托盘"],
    ["permission_denied", "无法读写本地数据", "读写权限"],
    ["disk_full", "磁盘空间不足，无法继续启动", "释放"],
    ["storage_unavailable", "无法访问本地存储", "恢复连接"],
    ["database_unsupported", "本地数据无法兼容当前版本", "不要删除数据库或修改版本号"],
    ["database_corrupt", "本地数据库无法正常读取", "附属文件"],
    ["backup_failed", "升级备份未完成", "不要跳过备份"],
    ["migration_failed", "历史数据升级未完成", "升级前备份"],
    ["unknown", "无法打开本地工作台", "提供启动日志"],
  ])("explains %s and gives concrete steps", (code, title, guidance) => {
    render(<StartupFailurePanel progress={{ ...READY_STARTUP, status: "failed", stage: "opening", failure_code: code }} actions={<button>打开日志文件夹</button>} />)
    const alert = screen.getByRole("alert")
    expect(within(alert).getByRole("heading", { name: title })).toBeVisible()
    expect(within(alert).getAllByRole("listitem")).toHaveLength(2)
    expect(alert).toHaveTextContent(guidance)
    expect(alert).toHaveTextContent(`错误标识：${code}`)
    expect(alert).toHaveTextContent("停止于：正在打开本地工作区")
    expect(within(alert).getByRole("button", { name: "打开日志文件夹" })).toBeVisible()
  })

  it("uses honest fallback guidance when a category is unavailable", () => {
    render(<StartupFailurePanel progress={null} actions={null} />)
    expect(screen.getByRole("alert")).toHaveTextContent("目前无法确定具体原因")
    expect(screen.queryByText(/停止于/)).not.toBeInTheDocument()
  })
})
