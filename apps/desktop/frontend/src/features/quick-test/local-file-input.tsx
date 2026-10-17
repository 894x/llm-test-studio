import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { DesktopClientError, MAX_LOCAL_FILE_BYTES, publicDesktopErrorMessage, type DesktopClient, type LocalFileSelection } from "@/app/desktop-client"
import { Button } from "@/components/ui/button"
import { FieldError } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import type { FileInput, FileMediaType } from "@/features/catalog/data"

export type LocalFileActions = Pick<DesktopClient, "createLocalFileSelection" | "releaseLocalFileSelection">
export type FileImportCoordinator = { begin: () => boolean; end: () => void }

type PendingRelease = { inFlight: Promise<void> | null }
// Failed releases belong to the current client session, including after a field
// unmounts. Core's eight selection slots bound this map; no background loop runs.
const pendingReleases = new WeakMap<LocalFileActions, Map<string, PendingRelease>>()

function releasesFor(actions: LocalFileActions): Map<string, PendingRelease> {
  let releases = pendingReleases.get(actions)
  if (!releases) {
    releases = new Map()
    pendingReleases.set(actions, releases)
  }
  return releases
}

function releaseSelection(actions: LocalFileActions, id: string): Promise<void> {
  const releases = releasesFor(actions)
  const entry = releases.get(id) ?? { inFlight: null }
  if (entry.inFlight) return entry.inFlight
  releases.set(id, entry)
  entry.inFlight = Promise.resolve().then(() => actions.releaseLocalFileSelection(id)).then(() => {
    releases.delete(id)
  }, () => {
    entry.inFlight = null
    throw new DesktopClientError("file_input_unavailable")
  })
  return entry.inFlight
}

async function retryPendingReleases(actions: LocalFileActions): Promise<void> {
  const releases = releasesFor(actions)
  const results = await Promise.allSettled([...releases].map(async ([id, entry]) => {
    if (entry.inFlight) {
      try { await entry.inFlight; return }
      catch { /* Retry this release once after the existing attempt settles. */ }
    }
    await releaseSelection(actions, id)
  }))
  if (results.some(result => result.status === "rejected") || releases.size > 0)
    throw new DesktopClientError("file_input_unavailable")
}

export function LocalFileInput({ id, definition, actions, coordinator, disabled, importing, invalid }: {
  id: string
  definition: FileInput
  actions: LocalFileActions
  coordinator: FileImportCoordinator
  disabled?: boolean
  importing: boolean
  invalid?: boolean
}) {
  const { t } = useTranslation("quickTest")
  const [selection, setSelection] = useState<LocalFileSelection | null>(null)
  const [pending, setPending] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [error, setError] = useState("")
  const selected = useRef<LocalFileSelection | null>(null)
  const reader = useRef<FileReader | null>(null)
  const generation = useRef(0)
  const active = useRef(true)
  const operation = useRef<Promise<void> | null>(null)
  const removalInFlight = useRef(false)
  const focusAfterRemoval = useRef(false)
  const input = useRef<HTMLInputElement>(null)
  const release = async (value: LocalFileSelection) => {
    await releaseSelection(actions, value.selection_id)
  }

  const cancel = useCallback(() => {
    generation.current = generation.current + 1
    reader.current?.abort()
    setPending(false)
    setError("")
    if (input.current) input.current.value = ""
  }, [])

  useEffect(() => {
    active.current = true
    const control = input.current
    control?.addEventListener("cancel", cancel)
    return () => {
      control?.removeEventListener("cancel", cancel)
      active.current = false
      generation.current = generation.current + 1
      reader.current?.abort()
      if (selected.current) void releaseSelection(actions, selected.current.selection_id).catch(() => undefined)
      selected.current = null
      void retryPendingReleases(actions).catch(() => undefined)
    }
  }, [actions, cancel])

  useEffect(() => {
    if (!removing && focusAfterRemoval.current) {
      focusAfterRemoval.current = false
      input.current?.focus()
    }
  }, [removing, selection])

  const choose = async (file: File) => {
    // Reject size and media type before allocating a reader or encoding bytes.
    if (!Number.isSafeInteger(file.size) || file.size < 1) {
      setError(t("fileInput.invalid"))
      return
    }
    if (file.size > Math.min(definition.max_bytes, MAX_LOCAL_FILE_BYTES)) {
      setError(t("fileInput.tooLarge", { bytes: definition.max_bytes }))
      return
    }
    if (!definition.media_types.includes(file.type as FileMediaType)) {
      setError(t("fileInput.invalid"))
      return
    }
    const currentGeneration = ++generation.current
    reader.current?.abort()
    // Replacing a pending read waits for it to settle, so only one import runs.
    if (operation.current) await operation.current
    if (!active.current || currentGeneration !== generation.current) return
    if (!coordinator.begin()) {
      setError(t("fileInput.busy"))
      return
    }
    setPending(true)
    setError("")
    const isCurrent = () => active.current && currentGeneration === generation.current
    const execute = async () => {
      try {
        await retryPendingReleases(actions)
        if (!isCurrent()) return
        const currentReader = new FileReader()
        reader.current = currentReader
        const buffer = await new Promise<ArrayBuffer>((resolve, reject) => {
          currentReader.onload = () => currentReader.result instanceof ArrayBuffer
            ? resolve(currentReader.result) : reject(new DesktopClientError("file_input_invalid"))
          currentReader.onerror = () => reject(new DesktopClientError("file_input_invalid"))
          currentReader.onabort = () => reject(new DesktopClientError("operation_cancelled"))
          currentReader.readAsArrayBuffer(file)
        })
        reader.current = null
        if (!isCurrent()) return
        if (buffer.byteLength !== file.size || buffer.byteLength > definition.max_bytes || buffer.byteLength > MAX_LOCAL_FILE_BYTES)
          throw new DesktopClientError("file_input_invalid")
        const bytes = new Uint8Array(buffer)
        const chunks: string[] = []
        for (let offset = 0; offset < bytes.length; offset += 0x8000) {
          chunks.push(String.fromCharCode(...bytes.subarray(offset, offset + 0x8000)))
        }
        const created = await actions.createLocalFileSelection({ data_base64: btoa(chunks.join("")), media_type: file.type as FileMediaType })
        if (!isCurrent()) {
          await release(created)
          return
        }
        if (created.size !== file.size || !definition.media_types.includes(created.media_type)) {
          await release(created)
          throw new DesktopClientError("file_input_invalid")
        }
        const previous = selected.current
        selected.current = created
        setSelection(created)
        if (previous) await release(previous)
      } catch (reason) {
        if (isCurrent()) setError(publicDesktopErrorMessage(reason, t("fileInput.invalid")))
      } finally {
        reader.current = null
        coordinator.end()
        if (isCurrent()) setPending(false)
      }
    }
    operation.current = execute()
    await operation.current
    operation.current = null
  }

  const remove = async () => {
    if (removalInFlight.current || operation.current) return
    cancel()
    const previous = selected.current
    if (!previous) return
    removalInFlight.current = true
    setRemoving(true)
    try {
      const alreadyPending = releasesFor(actions).has(previous.selection_id)
      await retryPendingReleases(actions)
      if (!alreadyPending) await release(previous)
      if (selected.current !== previous || !active.current) return
      selected.current = null
      focusAfterRemoval.current = true
      setSelection(null)
    } catch (reason) {
      if (active.current) setError(publicDesktopErrorMessage(reason, t("fileInput.unavailable")))
    } finally {
      removalInFlight.current = false
      if (active.current) setRemoving(false)
    }
  }

  return (
    <div className="min-w-0 space-y-2" data-invalid={!!error || invalid || undefined}>
      <Input id={id} ref={input} type="file" accept={definition.media_types.join(",")} multiple={false}
        disabled={disabled || importing || removing} aria-invalid={!!error || invalid || undefined}
        aria-describedby={`${id}-hint${error ? ` ${id}-file-error` : ""}${invalid ? ` ${id}-error` : ""}`}
        onChange={event => {
          const files = event.currentTarget.files
          const file = files?.[0]
          const count = files?.length ?? 0
          event.currentTarget.value = ""
          if (count > 1) { setError(t("fileInput.invalid")); return }
          if (file) void choose(file)
        }} />
      <p id={`${id}-hint`} className="text-[11px] text-muted-foreground [overflow-wrap:anywhere]">
        {t("fileInput.hint", { bytes: definition.max_bytes, types: definition.media_types.join(", ") })}
      </p>
      <p role="status" className="text-xs text-muted-foreground [overflow-wrap:anywhere]">
        {pending ? <><Spinner className="mr-1 inline-block" />{t("fileInput.importing")}</> : selection
          ? t("fileInput.selected", { type: selection.media_type, bytes: selection.size }) : t("fileInput.empty")}
      </p>
      {pending || selection ? <div className="flex flex-wrap gap-2">
        {pending ? <Button type="button" variant="outline" disabled={disabled} onClick={cancel}>{t("fileInput.cancel")}</Button> : null}
        {selection ? <Button type="button" variant="outline" disabled={disabled || importing || removing} onClick={() => void remove()}>
          {removing ? <Spinner data-icon="inline-start" /> : null}{t("fileInput.remove")}
        </Button> : null}
      </div> : null}
      {error ? <FieldError id={`${id}-file-error`}>{error}</FieldError> : null}
    </div>
  )
}
