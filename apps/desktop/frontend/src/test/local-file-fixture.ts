import type { CatalogSnapshot, CatalogSuite, CatalogTestCase, FileSuiteInput } from "@/features/catalog/data"
import type { LocalFileSelection } from "@/app/desktop-client"
import { FIXTURE_CATALOG } from "@/features/runs/fixtures"

export const FILE_INPUT: FileSuiteInput = {
  key: "image", label: "测试图片", type: "file", required: true,
  file: { encoding: "image_data_url", media_types: ["image/png", "image/jpeg"], max_bytes: 1048576 },
  bindings: [{ case_id: FIXTURE_CATALOG.test_cases[0].id, input: "image" }],
}
export const FILE_CASE: CatalogTestCase = {
  ...FIXTURE_CATALOG.test_cases[0],
  definitions: { "openai-chat": {
    inputs: { image: { type: "file", required: true, file: FILE_INPUT.file } },
    request: { body: { messages: [{ role: "user", content: [{ type: "image_url", image_url: { url: { $input: "image" } } }] }] } },
    assertions: [{ id: "http-status", source: "http.status", operator: "equals", value: 200 }],
  } },
}
export const FILE_SUITE: CatalogSuite = {
  ...FIXTURE_CATALOG.suites[0], inputs: [FILE_INPUT], case_count: 1, cases: [{ case_id: FILE_CASE.id }],
}
export const FILE_CATALOG: CatalogSnapshot = {
  ...FIXTURE_CATALOG, test_cases: [FILE_CASE, ...FIXTURE_CATALOG.test_cases.slice(1)],
  suites: [FILE_SUITE, FIXTURE_CATALOG.suites[1]], plans: [],
}
export const FILE_SELECTION: LocalFileSelection = {
  selection_id: "a1000000-0000-4000-8000-000000000001", sha256: "a".repeat(64), size: 3, media_type: "image/png",
}
