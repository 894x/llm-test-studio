import { createInstance, type i18n } from "i18next"
import { initReactI18next } from "react-i18next"

import { SUPPORTED_LOCALES, type SupportedLocale } from "./locale"
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
  "zh-CN": { app: zhApp, catalog: zhCatalog, common: zhCommon, comparisons: zhComparisons, overview: zhOverview, quickTest: zhQuickTest, reports: zhReports, runs: zhRuns, shell: zhShell },
  "en-US": { app: enApp, catalog: enCatalog, common: enCommon, comparisons: enComparisons, overview: enOverview, quickTest: enQuickTest, reports: enReports, runs: enRuns, shell: enShell },
} as const

export function createAppI18n(locale: SupportedLocale): i18n {
  const instance = createInstance()
  void instance.use(initReactI18next).init({
    lng: locale,
    fallbackLng: "zh-CN",
    supportedLngs: [...SUPPORTED_LOCALES],
    resources,
    defaultNS: "shell",
    interpolation: { escapeValue: false },
    react: { useSuspense: false },
    returnNull: false,
    initAsync: false,
  })
  return instance
}
