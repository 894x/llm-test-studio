import { useTranslation } from "react-i18next"
import type { ProtocolObservation } from "./types"

export function ChatObservationDetails({ observation }: { observation: ProtocolObservation }) {
  const { t } = useTranslation("reports")
  return <div className="min-w-0 space-y-2">
    {observation.text !== undefined ? <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words text-xs">{observation.text.slice(0, 16000)}</pre> : null}
    {observation.stream_completed !== undefined ? <p className="text-xs text-muted-foreground">{t(observation.stream_completed ? "protocolDesign.streamCompleted" : "protocolDesign.streamIncomplete")}</p> : null}
  </div>
}

export function TaskObservationDetails({ observation }: { observation: ProtocolObservation }) {
  const { t } = useTranslation("reports")
  return <div className="min-w-0 space-y-2">
    {observation.task ? <dl className="flex flex-wrap gap-x-6 gap-y-2 text-xs"><div><dt className="text-muted-foreground">{t("protocolDesign.task")}</dt><dd className="break-all">{observation.task.id}</dd></div><div><dt className="text-muted-foreground">{t("protocolDesign.taskStatus")}</dt><dd>{observation.task.status}</dd></div></dl> : null}
    {observation.artifacts.length ? <ul className="space-y-1 text-xs">{observation.artifacts.map((artifact, i) => <li key={i}>{safeArtifactURL(artifact.url) ? <a className="break-all underline underline-offset-2" href={artifact.url} target="_blank" rel="noreferrer">{artifact.kind}</a> : <span>{artifact.kind}</span>}</li>)}</ul> : null}
  </div>
}
function safeArtifactURL(value: string) { try { const url = new URL(value); return ["https:", "http:"].includes(url.protocol) && !url.username && !url.password } catch { return false } }
