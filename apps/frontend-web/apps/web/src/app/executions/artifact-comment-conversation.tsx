import { useContext, useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { AuthContext } from "../../components/auth"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import { canEditArtifactComment } from "./artifact-comment-permissions"
import type { ArtifactThread } from "./artifact-comments"

export type CommentMessage = { id: string; thread_id: string; seq: number; body: string | null; created_by: string; created_at: string; edit_count: number; edited_at?: string | null; retracted_at?: string | null }
type Messages = { items: CommentMessage[]; last_seq: number; has_more: boolean }


export function ArtifactCommentConversation({ tenantID, thread, write, manage, evidence, onChanged }: { tenantID: string; thread: ArtifactThread; write: boolean; manage: boolean; evidence: { id: string; title?: string }[]; onChanged: () => Promise<void> }) {
  const t = useTranslations("iam"), auth = useContext(AuthContext)
  const actor = auth?.session?.subject ?? ""
  const [messages, setMessages] = useState<Messages | null>(null)
  const [expanded, setExpanded] = useState(false)
  const [busy, setBusy] = useState(false), [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [body, setBody] = useState(""), [editing, setEditing] = useState<CommentMessage | null>(null)
  const [targetEvidence, setTargetEvidence] = useState("")
  const [withdrawal, setWithdrawal] = useState<CommentMessage | null>(null)
  const lifetime = useRef(new AbortController())
  useEffect(() => { const controller = new AbortController(); lifetime.current = controller; return () => controller.abort() }, [])
  const pending = useRef(false), attempt = useRef<{ identity: string; key: string } | null>(null)
  const base = `/tenants/${encodeURIComponent(tenantID)}`
  const path = `${base}/comment-threads/${encodeURIComponent(thread.id)}`
  const load = async (after = 0, append = false) => {
    const page = await iamApi.deliveryResultRequest<Messages>({ method: "GET", url: `${path}/comments?limit=100&after=${after}` }, { signal: lifetime.current.signal })
    if (lifetime.current.signal.aborted) return
    setMessages(previous => append && previous ? { ...page, items: [...previous.items, ...page.items] } : page)
  }
  const mutate = async (action: "REPLY" | "EDIT" | "WITHDRAW" | "STATE" | "REANCHOR", message?: CommentMessage) => {
    if (pending.current) return
    pending.current = true; setBusy(true); setError(""); setNotice("")
    const seq = messages?.last_seq ?? thread.last_seq
    try {
      const url = action === "REPLY" ? `${path}/comments` : action === "STATE" ? path : action === "REANCHOR" ? `${path}/reanchor` : `${base}/comments/${encodeURIComponent(message!.id)}`
      const method = action === "WITHDRAW" ? "DELETE" : action === "EDIT" || action === "STATE" ? "PATCH" : "POST"
      const payload = { seen_seq: seq, ...(action === "STATE" ? { state: thread.state === "OPEN" ? "RESOLVED" : "OPEN" } : action === "REANCHOR" ? { evidence_id: targetEvidence } : action === "WITHDRAW" ? {} : { body }) }
      const encoded = JSON.stringify(payload), identity = JSON.stringify({ url, method, payload, seq })
      if (attempt.current?.identity !== identity) attempt.current = { identity, key: crypto.randomUUID() }
      await iamApi.deliveryResultRequest({ method, url }, { signal: lifetime.current.signal, body: encoded, headers: { "Idempotency-Key": attempt.current.key, ...(action !== "REPLY" ? { "If-Match": `"${seq}"` } : {}) } })
      if (lifetime.current.signal.aborted) return
      attempt.current = null; setBody(""); setEditing(null); setWithdrawal(null); setNotice(t("artifactCommentSaved"))
      await load(); await onChanged()
    } catch (cause) { if (!lifetime.current.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause)) }
    finally { pending.current = false; if (!lifetime.current.signal.aborted) setBusy(false) }
  }
  return <div className="grid min-w-0 gap-3">
    <Button type="button" variant="outline" className="min-h-11 w-fit" aria-expanded={expanded} disabled={busy} onClick={() => { setExpanded(!expanded); if (!expanded) void load().catch(cause => setError(String(cause))) }}>{t("artifactCommentConversation")}</Button>
    {error && <Alert>{error}</Alert>}{notice && <p role="status">{notice}</p>}
    {expanded && <>
      {!messages ? <p role="status">{t("loading")}</p> : <ol className="grid gap-4">{messages.items.map(message => <li key={message.id} className="grid min-w-0 gap-2 border-l-2 pl-3">
        <p className="text-sm text-muted-foreground">{message.created_by} · {new Date(message.created_at).toLocaleString()}{message.edited_at ? ` · ${t("artifactCommentEdited")}` : ""}</p>
        <p className="whitespace-pre-wrap break-words">{message.body ?? t("artifactCommentWithdrawn")}</p>
        <div className="flex flex-wrap gap-2">
          {canEditArtifactComment(message, actor) && <Button type="button" variant="outline" disabled={busy} onClick={() => { setEditing(message); setBody(message.body ?? "") }}>{t("edit")}</Button>}
          {message.body !== null && (message.created_by === actor || manage) && <Button type="button" variant="outline" disabled={busy} onClick={() => setWithdrawal(message)}>{t("artifactCommentWithdraw")}</Button>}
        </div>
      </li>)}</ol>}
      {messages?.has_more && <Button type="button" variant="outline" disabled={busy} onClick={() => void load(messages.items.at(-1)?.seq ?? 0, true).catch(cause => setError(String(cause)))}>{t("artifactCommentsMore")}</Button>}
      {withdrawal && <div role="alertdialog" aria-label={t("artifactCommentWithdraw")} className="grid gap-2 border p-3"><p>{t("artifactCommentWithdrawConfirm")}</p><div className="flex gap-2"><Button disabled={busy} onClick={() => void mutate("WITHDRAW", withdrawal)}>{t("artifactCommentWithdraw")}</Button><Button variant="outline" disabled={busy} onClick={() => setWithdrawal(null)}>{t("cancel")}</Button></div></div>}
      {(write || editing !== null) && thread.visibility === "INTERNAL" && <form className="grid gap-2" onSubmit={event => { event.preventDefault(); void mutate(editing ? "EDIT" : "REPLY", editing ?? undefined) }}><label className="grid gap-1 text-sm">{t(editing ? "artifactCommentEdit" : "artifactCommentReply")}<Textarea required maxLength={8000} disabled={busy} value={body} onChange={event => setBody(event.target.value)} /></label><p className="text-sm text-muted-foreground">{t("artifactCommentEditWindow")}</p><div className="flex flex-wrap gap-2"><Button type="submit" disabled={busy || !messages || !body.trim()}>{t(editing ? "save" : "artifactCommentReply")}</Button>{editing && <Button type="button" variant="outline" disabled={busy} onClick={() => { setEditing(null); setBody("") }}>{t("cancel")}</Button>}</div></form>}
      {(manage || thread.created_by === actor || thread.executor_id === actor) && <Button type="button" variant="outline" className="w-fit" disabled={busy || !messages} onClick={() => void mutate("STATE")}>{t(thread.state === "OPEN" ? "artifactCommentResolve" : "artifactCommentReopen")}</Button>}
      <form className="grid gap-2" onSubmit={event => { event.preventDefault(); void mutate("REANCHOR") }}><label className="grid gap-1 text-sm">{t("artifactCommentCheckVersion")}<select required className="min-h-11 rounded border bg-background p-2" disabled={busy} value={targetEvidence} onChange={event => setTargetEvidence(event.target.value)}><option value="">{t("artifactCommentSelectFile")}</option>{evidence.map(item => <option key={item.id} value={item.id}>{item.title || item.id}</option>)}</select></label><Button type="submit" variant="outline" className="w-fit" disabled={busy || !messages || !targetEvidence}>{t("artifactCommentReanchor")}</Button></form>
    </>}
  </div>
}
