import { useState } from "react"
import { useTranslation } from "react-i18next"
import { isCatalogSavedRefreshFailure, publicDesktopErrorMessage } from "@/app/desktop-client"
import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import type { SavedQuickTestTarget, SaveQuickTestTargetCommand } from "@/features/catalog/data"
import { PROTOCOL_LABELS } from "@/features/catalog/protocols"

export function SaveChannelSheet({ open, onOpenChange, connection, onSave, onSaved }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  connection: Omit<SaveQuickTestTargetCommand, "name">
  onSave: (command: SaveQuickTestTargetCommand) => Promise<SavedQuickTestTarget>
  onSaved: (saved: SavedQuickTestTarget) => void
}) {
  const { t } = useTranslation("quickTest")
  const [name, setName] = useState("")
  const [pending, setPending] = useState(false)
  const [committed, setCommitted] = useState(false)
  const [error, setError] = useState("")
  const save = async () => {
    if (pending || committed || !name.trim()) return
    setPending(true)
    setError("")
    try {
      const snapshot = await onSave({ ...connection, name: name.trim() })
      onSaved(snapshot)
      onOpenChange(false)
      setName("")
    } catch (error) {
      setCommitted(isCatalogSavedRefreshFailure(error))
      setError(publicDesktopErrorMessage(error, t("saveChannel.failed")))
    } finally {
      setPending(false)
    }
  }
  return <Sheet open={open} onOpenChange={value => {
    if (pending) return
    if (value) { setError(""); setCommitted(false) }
    onOpenChange(value)
  }}>
    <SheetContent className="sm:max-w-[420px]">
      <SheetHeader>
        <SheetTitle>{t("saveChannel.title")}</SheetTitle>
        <SheetDescription>{t("saveChannel.hint")}</SheetDescription>
      </SheetHeader>
      <form className="flex min-h-0 flex-1 flex-col" onSubmit={event => { event.preventDefault(); void save() }}>
        <div className="flex-1 space-y-4 overflow-y-auto px-4 py-3">
          <Field className="block">
            <FieldLabel htmlFor="save-channel-name">{t("saveChannel.name")}</FieldLabel>
            <Input id="save-channel-name" autoFocus value={name} onChange={event => setName(event.target.value)} disabled={pending || committed} />
          </Field>
          <dl className="space-y-2 text-xs [overflow-wrap:anywhere]">
            <div><dt className="text-muted-foreground">{t("address")}</dt><dd>{connection.base_url}</dd></div>
            <div><dt className="text-muted-foreground">{t("model.label")}</dt><dd>{connection.model_name}</dd></div>
            <div><dt className="text-muted-foreground">{t("protocol")}</dt><dd>{PROTOCOL_LABELS[connection.protocol]}</dd></div>
          </dl>
          {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
        </div>
        <SheetFooter className="flex-row justify-end border-t">
          <Button type="button" variant="outline" disabled={pending} onClick={() => onOpenChange(false)}>{t("saveChannel.cancel")}</Button>
          <Button type="submit" disabled={pending || committed || !name.trim()}>{pending ? t("saveChannel.saving") : t("saveChannel.title")}</Button>
        </SheetFooter>
      </form>
    </SheetContent>
  </Sheet>
}
