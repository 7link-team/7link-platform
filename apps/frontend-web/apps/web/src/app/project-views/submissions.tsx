import { useEffect, useId, useRef, useState } from "react"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Textarea } from "@workspace/ui/components/textarea"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { submissionsApi, type AnswerAttachment, type SubmissionDetail, type SubmissionPage, type SubmissionQuestion } from "./submissions-api"

export function MySubmissions({ tenant, project }: { tenant: string; project: string }) {
  const t = useTranslations("iam")
  const [cursor, setCursor] = useState("")
  const [history, setHistory] = useState<string[]>([])
  const [selected, setSelected] = useState("")
  const [revision, setRevision] = useState(0)
  const [page, setPage] = useState<SubmissionPage | null>(null)
  const [detail, setDetail] = useState<SubmissionDetail | null>(null)
  const [listError, setListError] = useState("")
  const [detailError, setDetailError] = useState("")
  const [loadedPage, setLoadedPage] = useState("")
  const [loadedDetail, setLoadedDetail] = useState("")
  const pageKey = `${tenant}:${project}:${cursor}:${revision}`
  const detailKey = `${tenant}:${project}:${selected}:${revision}`
  useEffect(() => {
    const controller = new AbortController()
    void submissionsApi.list(tenant, project, cursor, controller.signal).then(value => {
      if (!controller.signal.aborted) { setPage(value); setListError(""); setLoadedPage(pageKey) }
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) { setPage(null); setListError(cause instanceof Error ? cause.message : t("clientSaveFailed")); setLoadedPage(pageKey) }
    })
    return () => controller.abort()
  }, [tenant, project, cursor, revision, pageKey, t])
  useEffect(() => {
    if (!selected) return
    const controller = new AbortController()
    void submissionsApi.get(tenant, project, selected, controller.signal).then(value => {
      if (!controller.signal.aborted) { setDetail(value); setDetailError(""); setLoadedDetail(detailKey) }
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) { setDetail(null); setDetailError(cause instanceof Error ? cause.message : t("clientSaveFailed")); setLoadedDetail(detailKey) }
    })
    return () => controller.abort()
  }, [tenant, project, selected, revision, detailKey, t])
  const loading = loadedPage !== pageKey
  const currentPage = loading ? null : page
  const current = selected && loadedDetail === detailKey ? detail : null
  const move = (next: string) => { setCursor(next); setSelected("") }
  return <section className="min-w-0 space-y-4" aria-label={t("intakeMySubmissions")}>
    <h2 className="font-medium">{t("intakeMySubmissions")}</h2>
    {!loading && listError && <Alert>{listError}</Alert>}
    <AutoTable data={currentPage?.items ?? []} loading={loading} rowKey="id" pagination={false} exportable={false} emptyContent={t("intakeSubmissionsEmpty")} onRefresh={() => setRevision(value => value + 1)} columns={[
      { field: "ref_no", name: t("workNumber"), render: (_, row) => <button type="button" className="underline underline-offset-4" onClick={() => { if (row.id) setSelected(row.id) }}>{row.ref_no}</button> },
      { field: "status", name: t("status") },
      { field: "open_questions_count", name: t("intakeOpenQuestions") },
      { field: "updated_at", name: t("intakeSubmissionUpdated") },
    ]} footer={<div className="flex justify-end gap-2"><Button variant="outline" disabled={loading || !cursor} onClick={() => { move(history.at(-1) ?? ""); setHistory(values => values.slice(0, -1)) }}>{t("fleetPrevious")}</Button><Button variant="outline" disabled={loading || !currentPage?.has_more || !currentPage.next_cursor} onClick={() => { setHistory(values => [...values, cursor]); move(currentPage!.next_cursor!) }}>{t("fleetNext")}</Button></div>} />
    {selected && <div className="space-y-3 border-t pt-4" aria-busy={loadedDetail !== detailKey}>
      <Button variant="outline" onClick={() => setSelected("")}>{t("personalWorkClose")}</Button>
      {loadedDetail !== detailKey && <p role="status">{t("loading")}</p>}
      {loadedDetail === detailKey && detailError && <><Alert>{detailError}</Alert><Button variant="outline" onClick={() => setRevision(value => value + 1)}>{t("intakeRead")}</Button></>}
      {current && <>
        <p>{current.status}</p>
        {current.client_message && <p className="whitespace-pre-wrap break-words">{current.client_message}</p>}
        {(current.questions ?? []).map(question => <div key={`${question.round_id}:${question.number}`} className="space-y-2 border-t py-3">
          <p className="whitespace-pre-wrap break-words">{question.text}</p><p className="text-sm text-muted-foreground">{question.state}</p>
          {question.answer ? <p className="whitespace-pre-wrap break-words">{question.answer}</p> : current.id && ["SENT", "EXPIRED"].includes(question.state) && <SubmissionAnswer key={`${question.round_id}:${question.round_version}:${question.number}`} tenant={tenant} project={project} id={current.id} question={question} onSaved={() => setRevision(value => value + 1)} />}
          {current.id && (question.attachments ?? []).map(attachment => <SubmissionAttachment key={attachment.id} tenant={tenant} id={current.id!} question={question} attachment={attachment} />)}
        </div>)}
        {(current.deliveries ?? []).map(value => <div key={value.id} className="space-y-1 border-t py-3"><p>{value.id} · {value.state}</p>{(value.items ?? []).map(item => <p key={item.item_id} className="break-words">{item.artifact.name}</p>)}</div>)}
        {!current.downloads_available && <p className="text-sm text-muted-foreground">{t("intakeSubmissionDownloadUnavailable")}</p>}
        {current.kind === "CHANGE" && !current.change_view_available && <p className="text-sm text-muted-foreground">{t("intakeSubmissionChangeUnavailable")}</p>}
      </>}
    </div>}
  </section>
}

function SubmissionAttachment({ tenant, id, question, attachment }: { tenant: string; id: string; question: SubmissionQuestion; attachment: AnswerAttachment }) {
  const t = useTranslations("iam")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const active = useRef(true)
  useEffect(() => { active.current = true; return () => { active.current = false } }, [])
  return <div className="space-y-1">
    <Button variant="outline" className="max-w-full whitespace-normal break-all" disabled={busy || !attachment.id} onClick={() => {
      setBusy(true); setError("")
      void (async () => {
        const blob = await submissionsApi.download(tenant, id, question, attachment.id!)
        if (!active.current) return
        const url = URL.createObjectURL(blob)
        const anchor = document.createElement("a"); anchor.href = url; anchor.download = attachment.name; anchor.click()
        window.setTimeout(() => URL.revokeObjectURL(url), 1000)
      })().catch((cause: unknown) => { if (active.current) setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) }).finally(() => { if (active.current) setBusy(false) })
    }}>{busy ? t("loading") : `${t("conversationDownloadAttachment")}: ${attachment.name}`}</Button>
    {error && <Alert>{error}</Alert>}
  </div>
}

function SubmissionAnswer({ tenant, project, id, question, onSaved }: { tenant: string; project: string; id: string; question: SubmissionQuestion; onSaved: () => void }) {
  const t = useTranslations("iam")
  const [answer, setAnswer] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [uploads, setUploads] = useState<{ key: string; file: File }[]>([])
  const uploaded = useRef(new Map<string, string>())
  const [fileError, setFileError] = useState(false)
  const attachmentHelp = useId()
  const pending = useRef<{ payload: string; key: string } | null>(null)
  const request = useRef<AbortController | null>(null)
  const locked = useRef(false)
  useEffect(() => () => request.current?.abort(), [])
  return <form className="space-y-2" onSubmit={event => {
    event.preventDefault(); if (locked.current || fileError) return
    locked.current = true
    const controller = new AbortController(); request.current = controller; setBusy(true); setError("")
    void (async () => {
    for (const entry of uploads) {
      if (controller.signal.aborted) return
      if (!uploaded.current.has(entry.key)) {
        const id = await submissionsApi.upload(project, entry.file, t("conversationUploadNoRoot"))
        if (controller.signal.aborted) return
        uploaded.current.set(entry.key, id)
      }
    }
    if (controller.signal.aborted) return
    const ids = uploads.map(entry => uploaded.current.get(entry.key)!)
    const payload = JSON.stringify({ answer, ids })
    if (pending.current?.payload !== payload) pending.current = { payload, key: crypto.randomUUID() }
    await submissionsApi.answer(tenant, id, question, answer, pending.current.key, controller.signal, ids)
    if (!controller.signal.aborted) onSaved()
  })().catch((cause: unknown) => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) }).finally(() => { locked.current = false; if (!controller.signal.aborted) setBusy(false) })
  }}>
    <label className="block space-y-1"><span>{t("intakeSubmissionAnswer")}</span><Textarea name="submission_answer" value={answer} onChange={event => setAnswer(event.target.value)} maxLength={8000} required disabled={busy} /></label>
  <label className="block space-y-1"><span>{t("conversationAttachments")}</span><Input type="file" name="submission_files" multiple disabled={busy} aria-invalid={fileError} aria-describedby={attachmentHelp} onChange={event => {
    const files = Array.from(event.currentTarget.files ?? []); event.currentTarget.value = ""
    if (uploads.length + files.length > 5 || [...uploads.map(entry => entry.file), ...files].reduce((total, file) => total + file.size, 0) > 25 * 1024 * 1024) { setFileError(true); setError(t("intakeSubmissionAttachmentLimit")); return }
    setUploads(values => [...values, ...files.map(file => ({ key: crypto.randomUUID(), file }))]); setFileError(false); setError("")
  }} /></label>
  <p id={attachmentHelp} className="text-sm text-muted-foreground">{t("intakeSubmissionAttachmentLimit")}</p>
  {fileError && <Button type="button" variant="outline" disabled={busy} onClick={() => { setUploads([]); uploaded.current.clear(); setFileError(false); setError("") }}>{t("intakeSubmissionClearAttachments")}</Button>}
  {uploads.map(entry => <div key={entry.key} className="flex min-w-0 items-center justify-between gap-2"><span className="break-all">{entry.file.name}</span><Button type="button" variant="outline" disabled={busy} onClick={() => setUploads(values => values.filter(value => value.key !== entry.key))}>{t("intakeSubmissionRemoveAttachment")}</Button></div>)}
    {error && <Alert>{error}</Alert>}
    <Button type="submit" disabled={busy || fileError || !answer.trim()}>{busy ? t("loading") : t("intakeSubmissionSendAnswer")}</Button>
  </form>
}
