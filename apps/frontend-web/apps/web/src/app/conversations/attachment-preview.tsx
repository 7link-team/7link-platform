import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { iamApi } from "../../lib/iam-api"
import { channelMediaApi } from "./media-api"
import { Textarea } from "@workspace/ui/components/textarea"
import { Alert } from "../../components/alert"
import { ConversationMarkdown } from "./message"

export function AttachmentContent({ mime, source, text, label }: { mime: string; source: string; text: string; label: string }) {
  if (["image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"].includes(mime)) return <a href={source} target="_blank" rel="noopener noreferrer"><img src={source} alt={label} className="max-h-80 max-w-full rounded object-contain" /></a>
  if (mime.startsWith("audio/")) return <audio aria-label={label} src={source} controls preload="none" className="max-w-full" />
  if (mime.startsWith("video/")) return <video aria-label={label} src={source} controls preload="none" className="max-h-96 max-w-full" />
  if (mime === "text/markdown") return <ConversationMarkdown body={text} />
  if (mime.startsWith("text/")) return <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-3 text-sm">{text}</pre>
  if (mime === "application/pdf") return <iframe title={label} src={source} sandbox="" className="h-96 w-full rounded border" />
  return null
}

// Fetch only through the pinned attachment endpoint. Never use a file URL that
// could bypass message ancestry, current membership or revision authorization.
export function ConversationAttachment({ channel, message, attachment, file, transcribe = false, onDraft }: { channel: string; message: string; attachment: string; file: string; transcribe?: boolean; onDraft?: (text: string) => void }) {
  const t = useTranslations("iam")
  const [preview, setPreview] = useState<{ source: string; mime: string; text: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [transcript, setTranscript] = useState("")
  const [error, setError] = useState("")
  const active = useRef(true)
  const source = useRef("")
  const locked = useRef(false)
  useEffect(() => { active.current = true; return () => { active.current = false; if (source.current) URL.revokeObjectURL(source.current) } }, [])
  async function open(download: boolean) {
    if (locked.current) return
    locked.current = true; setBusy(true); setError("")
    try {
      const blob = await iamApi.downloadConversationAttachment(channel, message, attachment)
      if (!active.current) return
      if (download) {
        const url = URL.createObjectURL(blob); const link = document.createElement("a"); link.href = url; link.download = `attachment-${file}`; link.click(); window.setTimeout(() => URL.revokeObjectURL(url), 1000)
      } else {
        const mime = blob.type.split(";")[0].trim().toLowerCase()
        const text = mime.startsWith("text/") && blob.size <= 512 * 1024 ? await blob.text() : ""
        if (!active.current) return
        if (source.current) URL.revokeObjectURL(source.current)
        source.current = URL.createObjectURL(blob); setPreview({ source: source.current, mime, text })
      }
    } catch (cause) { if (active.current) setError((cause as Error).message) }
    finally { locked.current = false; if (active.current) setBusy(false) }
  }
  async function requestTranscript() {
    if (locked.current) return
    locked.current = true; setBusy(true); setError("")
    try { const result = await channelMediaApi.transcribe(channel, message, attachment); if (active.current) setTranscript(result.text) }
    catch (cause) { if (active.current) setError((cause as Error).message) }
    finally { locked.current = false; if (active.current) setBusy(false) }
  }
  const label = `${t("conversationDownloadAttachment")} ${file}`
  return <section className="min-w-0 space-y-2 rounded border p-3">
    <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={busy} aria-expanded={!!preview} onClick={() => { if (preview) { URL.revokeObjectURL(source.current); source.current = ""; setPreview(null) } else void open(false) }}>{t(preview ? "conversationMediaClose" : "conversationMediaPreview")}</Button><Button variant="link" disabled={busy} className="max-w-full break-all whitespace-normal" onClick={() => void open(true)}>{label}</Button></div>
    {busy && <p role="status">{t("loading")}</p>}
    {error && <Alert>{error}</Alert>}
    {preview?.mime.startsWith("audio/") && transcribe && <Button variant="outline" disabled={busy} onClick={() => void requestTranscript()}>{t("conversationTranscribe")}</Button>}
    {transcript && <div className="space-y-2"><label className="grid gap-2 text-sm">{t("conversationTranscriptReview")}<Textarea value={transcript} maxLength={8000} onChange={event => setTranscript(event.target.value)} /></label>{onDraft && <Button disabled={busy || !transcript.trim()} onClick={() => onDraft(transcript)}>{t("conversationTranscriptUse")}</Button>}</div>}
    {preview && <><AttachmentContent {...preview} label={label} /><p className="text-sm text-muted-foreground">{t("conversationMediaFallback")}</p></>}
  </section>
}
