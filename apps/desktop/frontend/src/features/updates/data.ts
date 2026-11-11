import { DesktopDataError } from "@/app/data-error"

export const UPDATE_STATES = ["idle", "checking", "available", "up_to_date", "skipped", "downloading", "ready", "installing", "error", "unsupported"] as const
export const UPDATE_ERRORS = ["", "network", "integrity", "storage", "install_failed", "tasks_active", "tasks_unknown", "unavailable"] as const
export type DesktopUpdate = {
  schema_version: 1
  current_version: string
  latest_version: string
  status: typeof UPDATE_STATES[number]
  error_code: typeof UPDATE_ERRORS[number]
  notes: string
  downloaded: number
  total: number
  auto_check: boolean
  skipped_version: string
  install_kind: "restart" | "manual" | "unavailable"
}
export interface DesktopUpdateClient {
  getDesktopUpdate(): Promise<DesktopUpdate>
  checkDesktopUpdate(): Promise<DesktopUpdate>
  downloadDesktopUpdate(): Promise<DesktopUpdate>
  cancelDesktopUpdate(): Promise<DesktopUpdate>
  setDesktopUpdatePreferences(autoCheck: boolean, skip: boolean): Promise<DesktopUpdate>
  installDesktopUpdate(): Promise<DesktopUpdate>
  openDesktopUpdateRelease(): Promise<void>
}
export const UNAVAILABLE_UPDATE: DesktopUpdate = {
  schema_version: 1, current_version: "dev", latest_version: "", status: "unsupported", error_code: "",
  notes: "", downloaded: 0, total: 0, auto_check: false, skipped_version: "", install_kind: "unavailable",
}

export const UNAVAILABLE_UPDATE_CLIENT: DesktopUpdateClient = {
  getDesktopUpdate: async () => ({ ...UNAVAILABLE_UPDATE }),
  checkDesktopUpdate: async () => ({ ...UNAVAILABLE_UPDATE }),
  downloadDesktopUpdate: async () => ({ ...UNAVAILABLE_UPDATE }),
  cancelDesktopUpdate: async () => ({ ...UNAVAILABLE_UPDATE }),
  setDesktopUpdatePreferences: async () => ({ ...UNAVAILABLE_UPDATE }),
  installDesktopUpdate: async () => ({ ...UNAVAILABLE_UPDATE }),
  openDesktopUpdateRelease: async () => {},
}

export function parseDesktopUpdate(value: unknown): DesktopUpdate {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new DesktopDataError("Invalid update state")
  const data = value as Record<string, unknown>
  const keys = Object.keys(UNAVAILABLE_UPDATE)
  const validKeys = Object.keys(data).length === keys.length && keys.every(key => Object.hasOwn(data, key))
  const strings = ["current_version", "latest_version", "notes", "skipped_version"].every(key => typeof data[key] === "string")
  const counts = ["downloaded", "total"].every(key => Number.isSafeInteger(data[key]) && (data[key] as number) >= 0 && (data[key] as number) <= 512 * 1024 * 1024)
  if (!validKeys || data.schema_version !== 1 || !strings || !counts || typeof data.auto_check !== "boolean" ||
    !(UPDATE_STATES as readonly unknown[]).includes(data.status) || !(UPDATE_ERRORS as readonly unknown[]).includes(data.error_code) ||
    !["restart", "manual", "unavailable"].includes(data.install_kind as string) || (data.notes as string).length > 32768 ||
    (data.current_version as string).length > 128 || (data.latest_version as string).length > 128 ||
    (data.skipped_version as string).length > 128) throw new DesktopDataError("Invalid update state")
  return { ...data } as DesktopUpdate
}
