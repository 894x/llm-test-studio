import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, it } from "vitest"

import { InspectorHeader, InspectorRow, PageFrame } from "./page-frame"

it("keeps the detail drawer current and restores focus after Escape", async () => {
  const user = userEvent.setup()
  const details = (name: string) => (
    <PageFrame title="模型" description="模型目录" inspectorLabel="模型详情"
      inspector={<><InspectorHeader title={name} subtitle="model-id" /><dl><InspectorRow label="名称" value={name} /></dl></>}>
      <p>模型列表</p>
    </PageFrame>
  )
  const { rerender } = render(details("模型 A"))
  const trigger = screen.getByRole("button", { name: "模型详情" })
  await user.click(trigger)
  expect(within(screen.getByRole("dialog", { name: "模型详情" })).getByRole("definition")).toHaveTextContent("模型 A")

  const longName = "LongUnbrokenModelName".repeat(10)
  rerender(details(longName))
  expect(within(screen.getByRole("dialog", { name: "模型详情" })).getByRole("definition")).toHaveTextContent(longName)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
  expect(trigger).toHaveFocus()
})
