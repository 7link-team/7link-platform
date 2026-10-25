import { useEffect, useRef, useState } from "react"
import { AutoForm, type FormConfig } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { textLines } from "../../lib/clientwork-api"
import { useAllowed } from "../../lib/permissions"
import type { IntakeLedgerSource } from "./intake-api"
import { clarificationApi, type Clarification } from "./clarification-api"

function ClarificationForm({fields, submit, label}: {fields: FormConfig; submit: (values: Record<string, unknown>, key: string) => Promise<unknown>; label: string}) {
  const t = useTranslations("iam")
  const [busy, setBusy] = useState(false), [error, setError] = useState("")
  const pending = useRef<{signature: string; key: string} | null>(null)
  return <div className="grid gap-2" aria-busy={busy}>{error && <Alert>{error}</Alert>}<AutoForm fields={fields} mode="add" onSubmit={async values => {
    if (busy) return
    const signature = JSON.stringify(values)
    if (pending.current?.signature !== signature) pending.current = {signature, key: crypto.randomUUID()}
    setBusy(true); setError("")
    try { await submit(values, pending.current.key); pending.current = null }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) }
    finally { setBusy(false) }
  }}><Button type="submit" disabled={busy} className="min-h-11">{busy ? t("clientSaving") : label}</Button></AutoForm></div>
}

export function ClarificationRequestForm({project, source, onSaved}: {project: string; source: IntakeLedgerSource; onSaved: () => void}) {
  const t = useTranslations("iam")
  if (!["RECEIVED", "TRIAGED"].includes(source.state)) return null
  return <section className="grid gap-3 border-t pt-4" aria-label={t("clarificationTitle")}><h3 className="font-medium">{t("clarificationTitle")}</h3><p className="text-sm text-muted-foreground">{t("clarificationInApp")}</p><ClarificationForm fields={{recipient_id: {name: t("clientRecipient"), type: "text", required: true}, questions: {name: t("clarificationQuestions"), type: "textarea", required: true}}} label={t("clarificationRequest")} submit={async (values, key) => {
    const questions = textLines(values.questions)
    if (!questions.length || questions.length > 10) throw new Error(t("clarificationQuestionLimit"))
    await clarificationApi.request(project, {intake_id: source.id, intake_version: source.version, recipient_id: String(values.recipient_id ?? ""), questions}, key)
    onSaved()
  }}/></section>
}

export function Clarifications({project, external, onChanged}: {project: string; external: boolean; onChanged?: () => void}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [cursor, setCursor] = useState(""), [revision, setRevision] = useState(0)
  const [page, setPage] = useState<{items: Clarification[]; has_more: boolean; next_cursor?: string} | null>(null)
  const [loaded, setLoaded] = useState(""), [error, setError] = useState("")
  const identity = `${project}:${external}:${cursor}:${revision}`
  useEffect(() => {
    const controller = new AbortController()
    void clarificationApi.list(project, cursor, controller.signal).then(value => {
      if (!controller.signal.aborted) {setPage({...value, items: value.items ?? []}); setLoaded(identity); setError("")}
    }).catch((cause: unknown) => { if (!controller.signal.aborted) {setPage(null); setLoaded(identity); setError(cause instanceof Error ? cause.message : t("clientLoadFailed"))} })
    return () => controller.abort()
  }, [project, cursor, identity, t])
  const current = loaded === identity ? page : null
  return <section className="grid min-w-0 gap-3 border-t pt-4" aria-label={t("clarificationTitle")} aria-busy={loaded !== identity}>
    <h2 className="font-medium">{t("clarificationTitle")}</h2><p className="text-sm text-muted-foreground">{t("clarificationInApp")}</p>
    {error && <Alert>{error}</Alert>}<Button variant="outline" className="min-h-11 justify-self-start" onClick={() => setRevision(value => value + 1)}>{t("clientRetry")}</Button>
    {current?.items.map(round => <article key={`${round.id}:${round.version}`} className="grid min-w-0 gap-3 border-l-2 pl-3"><p className="text-sm">{t(round.state === "ANSWERED" ? "clarificationAnswered" : round.state === "CANCELLED" ? "clarificationCancelled" : "clarificationOpen")}</p>{(round.questions ?? []).map(question => <div key={question.number} className="grid gap-2"><h3 className="whitespace-pre-wrap break-words font-medium">{question.number}. {question.text}</h3>{question.answered_at ? <p className="whitespace-pre-wrap break-words">{question.answer}</p> : external && round.state === "OPEN" && <ClarificationForm fields={{answer: {name: t("clarificationAnswer"), type: "textarea", required: true}}} label={t("clarificationSubmit")} submit={async (values, key) => {await clarificationApi.answer(project, round, {question_no: question.number, answer: String(values.answer ?? "")}, key); setRevision(value => value + 1)}}/>}</div>)}{round.state === "CANCELLED" && <p className="text-sm text-muted-foreground">{t("clarificationWithdrawn")}</p>}{!external && allowed("scope_change") && round.state === "OPEN" && <ClarificationForm fields={{reason: {name: t("clientReason"), type: "textarea", required: true}}} label={t("clarificationCancel")} submit={async (values, key) => {await clarificationApi.cancel(project, round, String(values.reason ?? ""), key); setRevision(value => value + 1); onChanged?.()}}/>}</article>)}
    {current && !current.items.length && <p role="status">{t("clientEmpty")}</p>}
    <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={!cursor} onClick={() => setCursor("")}>{t("clientFirstPage")}</Button><Button variant="outline" disabled={!current?.has_more} onClick={() => setCursor(current?.next_cursor ?? "")}>{t("fleetNext")}</Button></div>
  </section>
}
