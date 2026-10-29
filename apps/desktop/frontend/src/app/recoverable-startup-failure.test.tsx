import { act, render, screen, within, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { TransientOverlayProvider } from "@/components/ui/transient-overlays"
import { RecoverableStartupFailure } from "./recoverable-startup-failure"
import { READY_STARTUP } from "./startup-progress"
import type { DesktopClient } from "./desktop-client"
import type { DatabaseRecoveryResult } from "./database-recovery-data"
import { parseDatabaseRecoveryResult } from "./database-recovery-data"
import { DesktopDataError } from "./data-error"

const progress = { ...READY_STARTUP, status: "failed" as const, stage: "opening" as const, failure_code: "database_unsupported" as const, recovery_available: true }
function setup() {
  const client = {
    backupAndCreateDatabase: vi.fn<DesktopClient["backupAndCreateDatabase"]>(),
    openDatabaseRecoveryDirectory: vi.fn(async () => undefined),
  }
  const onContinue = vi.fn()
  render(<TransientOverlayProvider><RecoverableStartupFailure progress={progress} client={client} actions={<button>打开日志文件夹</button>} onContinue={onContinue} /></TransientOverlayProvider>)
  return { client, onContinue }
}

describe("incompatible database recovery", () => {
  it("requires confirmation, blocks duplicates, and offers the original backup to the administrator", async () => {
    const user = userEvent.setup()
    const { client, onContinue } = setup()
    let finish!: (result: DatabaseRecoveryResult) => void
    client.backupAndCreateDatabase.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    await user.click(screen.getByRole("button", { name: "备份并创建新数据库" }))
    let dialog = screen.getByRole("alertdialog")
    expect(dialog).toHaveTextContent("原数据库及其日志文件会保留")
    expect(dialog).toHaveTextContent("不会自动带入原有历史记录")
    expect(client.backupAndCreateDatabase).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole("button", { name: "取消" }))
    expect(client.backupAndCreateDatabase).not.toHaveBeenCalled()
    await user.click(screen.getByRole("button", { name: "备份并创建新数据库" }))
    dialog = screen.getByRole("alertdialog")
    await user.click(within(dialog).getByRole("button", { name: "确认备份并创建" }))
    expect(screen.getByRole("button", { name: "正在备份并准备新工作区…" })).toBeDisabled()
    expect(client.backupAndCreateDatabase).toHaveBeenCalledTimes(1)
    await act(async () => finish({ schema_version: 1, status: "completed", workspace_ready: true }))
    expect(screen.getByRole("heading", { name: "原数据库已备份，新数据库已创建" })).toBeVisible()
    expect(screen.getByRole("alert")).toHaveTextContent("original.sqlite 和 backup.json 发给管理员")
    await user.click(screen.getByRole("button", { name: "打开备份文件夹" }))
    await waitFor(() => expect(client.openDatabaseRecoveryDirectory).toHaveBeenCalledTimes(1))
    await user.click(screen.getByRole("button", { name: "进入新工作区" }))
    expect(onContinue).toHaveBeenCalledTimes(1)
  })

  it("shows a bounded storage failure and permits a deliberate retry", async () => {
    const user = userEvent.setup()
    const { client } = setup()
    client.backupAndCreateDatabase.mockResolvedValueOnce({ schema_version: 1, status: "failed", workspace_ready: false, failure_code: "disk_full" })
    await user.click(screen.getByRole("button", { name: "备份并创建新数据库" }))
    await user.click(screen.getByRole("button", { name: "确认备份并创建" }))
    expect(await screen.findByRole("status")).toHaveTextContent("可用磁盘空间不足")
    expect(screen.getByRole("button", { name: "备份并创建新数据库" })).toBeEnabled()
    expect(screen.queryByRole("button", { name: "进入新工作区" })).not.toBeInTheDocument()
  })

  it("does not expose raw transport errors or claim an unconfirmed outcome", async () => {
    const user = userEvent.setup()
    const { client } = setup()
    client.backupAndCreateDatabase.mockRejectedValue(new Error("private/path sk-secret"))
    await user.click(screen.getByRole("button", { name: "备份并创建新数据库" }))
    await user.click(screen.getByRole("button", { name: "确认备份并创建" }))
    const status = await screen.findByRole("status")
    expect(status).toHaveTextContent("未能确认恢复操作的结果")
    expect(status).not.toHaveTextContent("sk-secret")
  })

  it("validates only bounded recovery results", () => {
    expect(parseDatabaseRecoveryResult({ schema_version: 1, status: "completed", workspace_ready: false }).workspace_ready).toBe(false)
    for (const value of [
      { schema_version: 1, status: "failed", workspace_ready: true, failure_code: "disk_full" },
      { schema_version: 1, status: "completed", workspace_ready: true, path: "private" },
      { schema_version: 1, status: "failed", workspace_ready: false, failure_code: "raw error" },
    ]) expect(() => parseDatabaseRecoveryResult(value)).toThrow(DesktopDataError)
  })

  it("requires reopening after uncertain activation instead of offering another reset", async () => {
    const user = userEvent.setup()
    const { client } = setup()
    client.backupAndCreateDatabase.mockResolvedValueOnce({ schema_version: 1, status: "failed", workspace_ready: false, failure_code: "unknown", reopen_required: true })
    await user.click(screen.getByRole("button", { name: "备份并创建新数据库" }))
    await user.click(screen.getByRole("button", { name: "确认备份并创建" }))
    expect(await screen.findByRole("status")).toHaveTextContent("不要重复创建新库")
    expect(screen.queryByRole("button", { name: "备份并创建新数据库" })).not.toBeInTheDocument()
  })
})
