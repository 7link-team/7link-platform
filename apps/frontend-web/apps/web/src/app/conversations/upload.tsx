import { useEffect, useRef, useState } from "react"
import { Mic, Square } from "lucide-react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { iamApi } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"
import { Alert } from "../../components/alert"

export function ComposerUpload({
  entityID,
  channelID,
  disabled,
  remaining,
  onBusy,
  onAttached,
}: {
  entityID: string
  channelID: string
  disabled: boolean
  remaining: number
  onBusy: (busy: boolean) => void
  onAttached: (candidate: components["schemas"]["AttachmentCandidate"]) => void
}) {
  const t = useTranslations("iam")
  const [busy, setBusy] = useState(false)
  const [recording, setRecording] = useState(false)
  const [error, setError] = useState("")
  const recorder = useRef<MediaRecorder | null>(null)
  const stream = useRef<MediaStream | null>(null)
  const active = useRef(true)
  const locked = useRef(false)
  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
      if (recorder.current?.state === "recording") recorder.current.stop()
      stream.current?.getTracks().forEach((track) => track.stop())
      onBusy(false)
    }
  }, [onBusy])
  const upload = async (files: File[]) => {
    if (locked.current || !files.length || !active.current) return
    if (files.length > remaining) {
      setError(t("conversationAttachmentLimit"))
      return
    }
    locked.current = true
    setBusy(true)
    onBusy(true)
    setError("")
    try {
      // Use the existing project root and Files authorization; the candidate
      // endpoint resolves and pins the exact uploaded revision for the message.
      const roots = await iamApi.files(entityID)
      const root = roots.items?.find(
        (entry) => entry.kind === "directory" && entry.id
      )
      if (!root?.id) throw new Error(t("conversationUploadNoRoot"))
      for (const file of files) {
        if (!active.current) return
        const uploaded = await iamApi.uploadFile(entityID, root.id, file)
        if (!uploaded.id) throw new Error(t("conversationUploadFailed"))
        const candidate = await iamApi.conversationAttachmentCandidate(
          channelID,
          uploaded.id
        )
        if (active.current) onAttached(candidate)
      }
    } catch (cause) {
      if (active.current)
        setError(
          cause instanceof Error ? cause.message : t("conversationUploadFailed")
        )
    } finally {
      locked.current = false
      if (active.current) {
        setBusy(false)
        onBusy(false)
      }
    }
  }
  const record = async () => {
    if (disabled || locked.current || recording) return
    setError("")
    locked.current = true
    setBusy(true)
    onBusy(true)
    try {
      if (
        !navigator.mediaDevices?.getUserMedia ||
        typeof MediaRecorder === "undefined"
      )
        throw new Error(t("conversationVoiceUnavailable"))
      const media = await navigator.mediaDevices.getUserMedia({ audio: true })
      if (!active.current) {
        media.getTracks().forEach((track) => track.stop())
        return
      }
      stream.current = media
      const next = new MediaRecorder(media)
      const chunks: Blob[] = []
      next.ondataavailable = (event) => {
        if (event.data.size) chunks.push(event.data)
      }
      next.onstop = () => {
        media.getTracks().forEach((track) => track.stop())
        locked.current = false
        if (!active.current) return
        setRecording(false)
        onBusy(false)
        const mime = next.mimeType || "audio/webm"
        const extension = mime.includes("mp4")
          ? "m4a"
          : mime.includes("ogg")
            ? "ogg"
            : "webm"
        if (chunks.length)
          void upload([
            new File(chunks, `voice-${crypto.randomUUID()}.${extension}`, {
              type: mime,
            }),
          ])
      }
      recorder.current = next
      next.start()
      setBusy(false)
      setRecording(true)
    } catch (cause) {
      stream.current?.getTracks().forEach((track) => track.stop())
      locked.current = false
      if (active.current) {
        setBusy(false)
        onBusy(false)
        setError(
          cause instanceof Error
            ? cause.message
            : t("conversationVoiceUnavailable")
        )
      }
    }
  }
  return (
    <div className="space-y-2">
      <label className="grid gap-2 text-sm font-medium">
        {t("conversationUpload")}
        <Input
          type="file"
          multiple
          accept="image/*,video/*,audio/*,.pdf,.docx,.md,text/markdown,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document"
          disabled={disabled || busy || recording}
          onChange={(event) => {
            const files = Array.from(event.target.files ?? [])
            event.target.value = ""
            void upload(files)
          }}
        />
      </label>
      <Button
        type="button"
        variant="outline"
        disabled={disabled || busy}
        aria-pressed={recording}
        onClick={() => {
          if (recording) {
            if (recorder.current?.state === "recording") recorder.current.stop()
          } else void record()
        }}
      >
        {recording ? (
          <Square className="size-4" aria-hidden="true" />
        ) : (
          <Mic className="size-4" aria-hidden="true" />
        )}
        {recording ? t("conversationVoiceStop") : t("conversationVoiceRecord")}
      </Button>
      {(busy || recording) && (
        <p role="status" className="text-sm text-muted-foreground">
          {recording
            ? t("conversationVoiceRecording")
            : t("conversationUploading")}
        </p>
      )}
      {error && <Alert>{error}</Alert>}
    </div>
  )
}
