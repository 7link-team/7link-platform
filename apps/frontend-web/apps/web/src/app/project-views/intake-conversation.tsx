import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { collaborationID } from "../../lib/clientwork-api"
import { iamApi } from "../../lib/iam-api"
import type { IntakeLedgerSource, TriageDraft } from "./intake-api"
import { intakeThreadReply } from "./intake-api"

type Suggestion = { id: string; actor_kind: "HUMAN" | "AGENT"; actor_id: string; intake_version: number; suggested_project_id?: string; recommendation: "NEW_REQUIREMENT" | "NEW_PROJECT" | "REWORK" | "NEEDS_CLARIFICATION"; reason: string; assessment: TriageDraft }

export function IntakeConversation({ project, source }: { project: string; source: IntakeLedgerSource }) {
  const t = useTranslations("iam")
  const [channel, setChannel] = useState("")
  const [root, setRoot] = useState("")
  const [recipient, setRecipient] = useState("")
  const [body, setBody] = useState("")
  const [work, setWork] = useState("")
  const [workVersion, setWorkVersion] = useState("")
  const [message, setMessage] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [saved, setSaved] = useState(false)
  const pending = useRef<{ signature: string; key: string } | null>(null)
  const merged = source.state === "MERGED"
  const send = async (link: boolean, forward = false) => {
    if (busy) return
    setBusy(true); setError(""); setSaved(false)
    try {
      const path = `/entities/${collaborationID(project)}/intakes/${collaborationID(source.id)}` + (forward ? "/forwarded-replies" : "/conversation")
      const payload: Record<string, unknown> = link ? { channel_id: collaborationID(channel), root_message_id: collaborationID(root), recipient_id: collaborationID(recipient) } : intakeThreadReply(body, work, workVersion)
      if (forward) { delete payload.body; payload.message_id = collaborationID(message) }
      const signature = JSON.stringify([path, link, source.version, payload])
      if (pending.current?.signature !== signature) pending.current = { signature, key: crypto.randomUUID() }
      await iamApi.deliveryResultRequest({ method: "POST", url: path + (link || forward ? "" : "/messages") }, { headers: { "Idempotency-Key": pending.current.key, "If-Match": `"${source.version}"` }, body: JSON.stringify(payload) })
      pending.current = null; setSaved(true); if (!link) setBody(""); if (forward) setMessage("")
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) }
    finally { setBusy(false) }
  }
  return <details className="space-y-3 border-t pt-4">
    <summary className="min-h-11 cursor-pointer py-2 font-medium">{t("intakeConversationTitle")}</summary>
    <p className="text-sm text-muted-foreground">{t("intakeConversationHint")}</p>
    {error && <Alert>{error}</Alert>}{saved && <p role="status">{t("intakeConversationSaved")}</p>}
    <form className="grid gap-3 sm:grid-cols-3" onSubmit={event => { event.preventDefault(); void send(true) }}>
      {[["intakeConversationChannel", channel, setChannel], ["intakeConversationRoot", root, setRoot], ["intakeConversationRecipient", recipient, setRecipient]].map(([label, value, setter]) => <label key={String(label)} className="grid gap-1 text-sm">{t(String(label))}<Input value={String(value)} onChange={event => (setter as (value: string) => void)(event.target.value)} inputMode="numeric" pattern="[1-9][0-9]{0,18}" required disabled={busy} /></label>)}
      <Button type="submit" variant="outline" disabled={busy}>{t("intakeConversationLink")}</Button>
    </form>
    <form className="grid gap-3" onSubmit={event => { event.preventDefault(); void send(false, merged) }}>
      {(source.requirement_id || merged) && <fieldset className="grid gap-3 sm:grid-cols-2" disabled={busy}>
        <legend className="mb-2 text-sm">{t("intakeConversationWorkHint")}</legend>
        <label className="grid gap-1 text-sm">{t("intakeConversationWork")}<Input value={work} onChange={event => setWork(event.target.value)} inputMode="numeric" pattern="[1-9][0-9]{0,18}" required={!!workVersion} /></label>
        <label className="grid gap-1 text-sm">{t("intakeConversationWorkVersion")}<Input value={workVersion} onChange={event => setWorkVersion(event.target.value)} inputMode="numeric" pattern="[1-9][0-9]*" required={!!work} /></label>
      </fieldset>}
      {merged ? <>
        <p className="text-sm text-muted-foreground">{t("intakeConversationForwardHint")}</p>
        <label className="grid gap-1 text-sm">{t("intakeConversationMessage")}<Input value={message} onChange={event => setMessage(event.target.value)} inputMode="numeric" pattern="[1-9][0-9]{0,18}" required disabled={busy} /></label>
      </> : <label className="grid gap-1 text-sm">{t("intakeConversationReply")}<Textarea value={body} onChange={event => setBody(event.target.value)} required maxLength={2000} disabled={busy} /></label>}
      <Button type="submit" variant="outline" className="justify-self-start" disabled={busy || !(merged ? message.trim() : body.trim())}>{t(merged ? "intakeConversationForward" : "intakeConversationSend")}</Button>
    </form>
  </details>
}

export function IntakeSuggestions({ project, source }: { project: string; source: IntakeLedgerSource }) {
  const t = useTranslations("iam")
  const [rows, setRows] = useState<Suggestion[]>([])
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    const controller = new AbortController()
    void iamApi.deliveryResultRequest<Suggestion[]>({ method: "GET", url: `/entities/${collaborationID(project)}/intakes/${collaborationID(source.id)}/suggestions` }, { signal: controller.signal }).then(value => {
      if (!controller.signal.aborted) { setRows(value); setLoading(false) }
    }).catch((cause: unknown) => { if (!controller.signal.aborted) { setError(cause instanceof Error ? cause.message : t("clientSaveFailed")); setLoading(false) } })
    return () => controller.abort()
  }, [project, source.id, t])
  return <details className="space-y-3 border-t pt-4">
    <summary className="min-h-11 cursor-pointer py-2 font-medium">{t("intakeSuggestionsTitle")}</summary>
    <p className="text-sm text-muted-foreground">{t("intakeSuggestionsHint")}</p>
    {loading && <p role="status">{t("loading")}</p>}{error && <Alert>{error}</Alert>}
    {!loading && !error && !rows.length && <p>{t("intakeSuggestionsEmpty")}</p>}
    {rows.map(row => <article key={row.id} className="space-y-2 border-t py-3">
      <p className="break-all text-sm">{row.actor_kind} · {row.actor_id} · {t("clientVersion")} {row.intake_version}</p>
      <p>{t(`intakeSuggestion_${row.recommendation}`)}</p>
      {row.suggested_project_id && <p className="break-all">{t("intakeSuggestionProject")}: {row.suggested_project_id}</p>}
      <p className="whitespace-pre-wrap">{row.reason}</p>
      <p className="whitespace-pre-wrap">{row.assessment.summary}</p>
      <p className="whitespace-pre-wrap">{row.assessment.requirement_draft}</p>
      <ul className="list-disc pl-5">{row.assessment.criteria?.map((criterion, i) => <li key={i}>{criterion}</li>)}</ul>
    </article>)}
  </details>
}
