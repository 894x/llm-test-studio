import type { ComponentType } from "react"
import { describe, expect, it } from "vitest"

import { caseRendererRegistry, createCaseRendererRegistry } from "./registry"
import type { CaseRendererComponentProps, CaseRendererDefinition } from "./types"

const EmptyRenderer: ComponentType<CaseRendererComponentProps> = () => null

function renderer(
  match: CaseRendererDefinition["match"],
  options: Pick<CaseRendererDefinition, "caseTypeVersions" | "dataVersions"> = {},
): CaseRendererDefinition {
  return {
    match,
    ...options,
    parse: () => ({}),
    Component: EmptyRenderer,
  }
}

describe("case renderer registry", () => {
  it("selects exact, longest family, and generic renderers in that order", () => {
    const exact = renderer({ caseType: "response.probe.deep" })
    const broadFamily = renderer({ caseTypeFamily: "response" })
    const narrowFamily = renderer({ caseTypeFamily: "response.probe" })
    const generic = renderer({ generic: true })
    const registry = createCaseRendererRegistry([generic, broadFamily, exact, narrowFamily])

    expect(registry.candidates("response.probe.deep", 1, 1)).toEqual([
      exact,
      narrowFamily,
      broadFamily,
      generic,
    ])
    expect(registry.resolve("response.probe.deep", 1, 1)).toBe(exact)
    expect(registry.resolve("response.probe.other", 1, 1)).toBe(narrowFamily)
    expect(registry.resolve("response.other", 1, 1)).toBe(broadFamily)
    expect(registry.resolve("embedding.similarity", 1, 1)).toBe(generic)
  })

  it("falls through when a renderer does not support the case type or data version", () => {
    const exact = renderer(
      { caseType: "response.probe" },
      { caseTypeVersions: [2], dataVersions: [2] },
    )
    const family = renderer(
      { caseTypeFamily: "response" },
      { caseTypeVersions: [1], dataVersions: [1] },
    )
    const generic = renderer({ generic: true })
    const registry = createCaseRendererRegistry([exact, family, generic])

    expect(registry.resolve("response.probe", 1, 1)).toBe(family)
    expect(registry.resolve("response.probe", 2, 1)).toBe(generic)
    expect(registry.resolve("response.probe", 2, 2)).toBe(exact)
  })

  it("automatically discovers the built-in response probe and generic files", () => {
    expect(caseRendererRegistry.resolve("response.probe", 1, 1).match).toEqual({ caseType: "response.probe" })
    expect(caseRendererRegistry.resolve("new.case.type", 1, 1).match).toEqual({ generic: true })
  })

  it.each([
    [{ caseType: "duplicate.type" }, { caseType: "duplicate.type" }],
    [{ caseTypeFamily: "duplicate" }, { caseTypeFamily: "duplicate" }],
  ] as const)("rejects duplicate exact or family matches", (first, second) => {
    expect(() => createCaseRendererRegistry([
      renderer(first),
      renderer(second),
      renderer({ generic: true }),
    ])).toThrow(/duplicate/i)
  })
})
