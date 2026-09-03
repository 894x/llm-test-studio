import { readdirSync, readFileSync, statSync } from "node:fs"
import { dirname, join, relative } from "node:path"
import { fileURLToPath } from "node:url"
import { describe, expect, it } from "vitest"

import enApp from "./resources/en-US/app.json"
import enCatalog from "./resources/en-US/catalog.json"
import enCommon from "./resources/en-US/common.json"
import enComparisons from "./resources/en-US/comparisons.json"
import enOverview from "./resources/en-US/overview.json"
import enQuickTest from "./resources/en-US/quick-test.json"
import enReports from "./resources/en-US/reports.json"
import enRuns from "./resources/en-US/runs.json"
import enShell from "./resources/en-US/shell.json"
import zhApp from "./resources/zh-CN/app.json"
import zhCatalog from "./resources/zh-CN/catalog.json"
import zhCommon from "./resources/zh-CN/common.json"
import zhComparisons from "./resources/zh-CN/comparisons.json"
import zhOverview from "./resources/zh-CN/overview.json"
import zhQuickTest from "./resources/zh-CN/quick-test.json"
import zhReports from "./resources/zh-CN/reports.json"
import zhRuns from "./resources/zh-CN/runs.json"
import zhShell from "./resources/zh-CN/shell.json"

const resources = {
  app: [zhApp, enApp], catalog: [zhCatalog, enCatalog], common: [zhCommon, enCommon],
  comparisons: [zhComparisons, enComparisons], overview: [zhOverview, enOverview],
  quickTest: [zhQuickTest, enQuickTest], reports: [zhReports, enReports],
  runs: [zhRuns, enRuns], shell: [zhShell, enShell],
} as const

describe("translation resource integrity", () => {
  it.each(Object.entries(resources))("keeps %s keys and interpolation variables in parity", (_namespace, [zh, en]) => {
    const zhLeaves = flatten(zh)
    const enLeaves = flatten(en)
    expect(Object.keys(enLeaves).sort()).toEqual(Object.keys(zhLeaves).sort())
    for (const key of Object.keys(zhLeaves)) {
      expect(placeholders(enLeaves[key])).toEqual(placeholders(zhLeaves[key]))
    }
  })

  it("keeps production TSX free of hard-coded Han interface text", () => {
    const sourceRoot = join(dirname(fileURLToPath(import.meta.url)), "..")
    const violations = walk(sourceRoot)
      .filter((path) => path.endsWith(".tsx") && !path.endsWith(".test.tsx"))
      .flatMap((path) => readFileSync(path, "utf8").split(/\r?\n/).flatMap((line, index) =>
        /[\u3400-\u9fff]/u.test(line) ? [`${relative(sourceRoot, path)}:${index + 1}`] : [],
      ))
    expect(violations).toEqual([])
  })
})

function flatten(value: unknown, prefix = "", result: Record<string, string> = {}): Record<string, string> {
  if (typeof value === "string") {
    result[prefix] = value
    return result
  }
  for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
    flatten(child, prefix ? `${prefix}.${key}` : key, result)
  }
  return result
}

function placeholders(value: string): string[] {
  return [...value.matchAll(/{{\s*([^},\s]+).*?}}/g)].map((match) => match[1]).sort()
}

function walk(root: string): string[] {
  return readdirSync(root).flatMap((name) => {
    const path = join(root, name)
    return statSync(path).isDirectory() ? walk(path) : [path]
  })
}
