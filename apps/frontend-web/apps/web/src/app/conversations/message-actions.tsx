import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Textarea } from "@workspace/ui/components/textarea"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@workspace/ui/components/dialog"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { ConversationMarkdown } from "./message"

import { messageActionsApi as api, messageActionAvailability, type Message, type Revision } from "./message-actions-api"

export function MessageActions({ tenant, message, actor, manage = false, audit = false, onChanged }: { tenant: string; message: Message; actor: string | undefined; manage?: boolean; audit?: boolean; onChanged: () => void | Promise<void> }) {
  const t = useTranslations("iam")
  const available = messageActionAvailability(message, actor, manage)
  const [mode, setMode] = useState<"edit" | "retract" | "revisions" | null>(null)
  const [draft, setDraft] = useState(message.body ?? "")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [revisions, setRevisions] = useState<Revision[]>([])
  const action = useRef<AbortController | null>(null)
  const attempt = useRef<{ signature: string; key: string } | null>(null)
  useEffect(() => () => action.current?.abort(), [])
  function open(next: typeof mode) { setMode(next); setError(""); setDraft(message.body ?? ""); attempt.current = null }
  async function submit() {
    if (busy || !message.id || (mode !== "edit" && mode !== "retract")) return
    const body = mode === "edit" ? JSON.stringify({ body: draft.trim() }) : undefined
    const signature = `${mode}:${message.version}:${body ?? ""}`
    if (attempt.current?.signature !== signature) attempt.current = { signature, key: crypto.randomUUID() }
    const controller = new AbortController(); action.current = controller; setBusy(true); setError("")
    try {
      if (mode === "edit") await api.edit(tenant,message.id,message.version,draft.trim(),attempt.current.key,controller.signal)
      else await api.retract(tenant,message.id,message.version,attempt.current.key,controller.signal)
      if (controller.signal.aborted) return
      setMode(null); attempt.current = null; await onChanged()
    } catch (cause) { if (!controller.signal.aborted) setError((cause as Error).message) }
    finally { if (!controller.signal.aborted) setBusy(false) }
  }
  async function history() {
    open("revisions"); setRevisions([]); setBusy(true)
    const controller = new AbortController(); action.current = controller
    try {
      const rows = await api.revisions(tenant,message.id!,controller.signal)
      if (!controller.signal.aborted) setRevisions(rows)
    } catch (cause) { if (!controller.signal.aborted) setError((cause as Error).message) }
    finally { if (!controller.signal.aborted) setBusy(false) }
  }
  return <>
    <div className="flex flex-wrap items-center gap-2">
      {message.retracted ? <p className="text-sm italic text-muted-foreground">{t("conversationRetracted")}</p> : message.edited && <span className="text-xs text-muted-foreground">{t("conversationEdited")}</span>}
      {available.edit && <Button variant="ghost" size="sm" className="min-h-11" onClick={() => open("edit")}>{t("conversationEdit")}</Button>}
      {available.retract && <Button variant="ghost" size="sm" className="min-h-11" onClick={() => open("retract")}>{t("conversationRetract")}</Button>}
      {audit && message.id && <Button variant="ghost" size="sm" className="min-h-11" onClick={() => void history()}>{t("conversationRevisions")}</Button>}
    </div>
    <Dialog open={mode !== null} onOpenChange={value => { if (!value && !busy) setMode(null) }}><DialogContent><DialogHeader><DialogTitle>{t(mode === "edit" ? "conversationEdit" : mode === "retract" ? "conversationRetract" : "conversationRevisions")}</DialogTitle><DialogDescription>{t(mode === "retract" ? "conversationRetractConfirm" : "conversationEditRules")}</DialogDescription></DialogHeader>
      {error && <Alert>{error}</Alert>}
      {mode === "revisions" ? <div className="max-h-96 space-y-4 overflow-y-auto">{busy ? <p role="status">{t("loading")}</p> : revisions.length ? revisions.map(row => <article key={row.revision} className="border-b pb-3"><p className="text-xs text-muted-foreground">{t("conversationRevisionNumber", { number: row.revision })} &middot; {row.edited_by} &middot; {row.edited_at}</p><ConversationMarkdown body={row.body} /></article>) : !error && <p>{t("conversationNoRevisions")}</p>}</div> : <form className="space-y-4" onSubmit={event => { event.preventDefault(); void submit() }}>
        {mode === "edit" && <label className="grid gap-2 text-sm">{t("conversationEditBody")}<Textarea required maxLength={8000} value={draft} disabled={busy} onChange={event => setDraft(event.target.value)} /></label>}
        <div className="flex flex-wrap gap-2"><Button type="submit" variant={mode === "retract" ? "destructive" : "default"} disabled={busy || (mode === "edit" && !draft.trim())}>{busy ? t("loading") : t(mode === "retract" ? "conversationRetract" : "conversationSaveEdit")}</Button><Button type="button" variant="outline" disabled={busy} onClick={() => setMode(null)}>{t("conversationCancelEdit")}</Button></div>
      </form>}
    </DialogContent></Dialog>
  </>
}
