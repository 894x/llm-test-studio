import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import { CasesWorkspace } from "./catalog-workspaces"
import { EMPTY_CATALOG, type CatalogActions, type CatalogSnapshot } from "./data"

describe("CasesWorkspace", () => {
  it("renders human-readable case type labels with the interface font", () => {
    const catalog: CatalogSnapshot = {
      ...EMPTY_CATALOG,
      case_types: [{
        type: "legacy.apiaudit",
        type_version: 1,
        label: "内置兼容性审计",
        category: "compatibility",
        scheduling_owner: "case",
        supported_protocols: ["openai-chat"],
        creatable: false,
        default_spec: { kind: "chat_sync" },
      }],
      test_cases: [{
        id: "123e4567-e89b-42d3-a456-426614174020",
        revision: 1,
        key: "F001",
        name: "输入 usage 增长检测",
        dimension: "compatibility",
        protocol: "openai-chat",
        model_targets: [],
        enabled: true,
        default: true,
        severity: "normal",
        execution_mode: "automatic",
        definition_schema_version: 2,
        type: "legacy.apiaudit",
        type_version: 1,
        spec: { kind: "chat_sync" },
      }],
    }

    render(
      <CasesWorkspace
        catalog={catalog}
        actions={{} as CatalogActions}
        mutate={async (operation) => { await operation() }}
        mutationPending={false}
        mutationError=""
      />,
    )

    expect(screen.getByRole("cell", { name: "内置兼容性审计" })).not.toHaveClass("font-mono")
  })
})
