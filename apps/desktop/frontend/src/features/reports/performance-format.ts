import { desktopLocale } from "@/i18n/runtime"

/** Round presentation only; archived measurements retain their original precision. */
export function formatPerformanceInteger(value: number, locale: string = desktopLocale()): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(value)
}
