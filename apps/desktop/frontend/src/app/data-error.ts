// Keep protocol-error classification independent of translated display text.
export class DesktopDataError extends Error {
  constructor(message: string) {
    super(message)
    this.name = "DesktopDataError"
  }
}
