import { useCallback, useEffect, useRef, useState } from "react"
import { Link } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import { ArtifactCommentConversation } from "./artifact-comment-conversation"
import { PackageSourceViewer, type PackageSourceSelection } from "../project-views/package-source-viewer"
import { useAllowed } from "../../lib/permissions"

export type ArtifactAnchor = { kind: string; path: string; version_ref: string; file_id: string; version_id: string; sha256: string; start_line: number; end_line: number; excerpt: string; locator?: unknown }
export type ArtifactThread = { last_seq: number; created_by?: string; executor_id?: string; first_retracted?: boolean; feedback_id?: string; anchor_status?: string; relocated_anchor?: ArtifactAnchor | null; id: string; execution_id: string; work_item_id: string; evidence_id: string; visibility: string; state: string; anchor: ArtifactAnchor; first_body: string; defect_id?: string }
type ThreadPage = { items: ArtifactThread[]; has_more: boolean; next_cursor?: string }

export function FrozenArtifactAnchor({ anchor }: { anchor: ArtifactAnchor }) {
  const t = useTranslations("iam")
  return <div className="grid min-w-0 gap-2 text-sm">
    <p className="break-all font-medium">{anchor.path}{anchor.kind === "CODE_LINES" ? `:${anchor.start_line}-${anchor.end_line}` : ` ? ${anchor.kind}`}</p>
    <p className="break-all text-muted-foreground">{t("artifactCommentVersion")}: {anchor.version_ref}</p>
    <pre className="max-h-56 overflow-auto whitespace-pre-wrap break-words rounded border bg-muted/30 p-3">{anchor.excerpt}</pre>
  </div>
}

function ArtifactOriginalSource({ tenantID, thread }: { tenantID: string; thread: ArtifactThread }) {
  const t = useTranslations("iam")
  const [text, setText] = useState<string | null>(null), [error, setError] = useState("")
  const controller = useRef<AbortController | null>(null)
  useEffect(() => () => controller.current?.abort(), [])
  return <details onToggle={event => {
    if (!event.currentTarget.open || text !== null || controller.current) return
    controller.current = new AbortController()
    const signal = controller.current.signal
    void iamApi.deliveryResultRequest<{ text: string }>({ method: "GET", url: `/tenants/${encodeURIComponent(tenantID)}/executions/${encodeURIComponent(thread.execution_id)}/artifact-source?evidence_id=${encodeURIComponent(thread.evidence_id)}` }, { signal }).then(value => { if (!signal.aborted) setText(value.text) }).catch(cause => { if (!signal.aborted) { setError(String(cause)); controller.current = null } })
  }}><summary className="min-h-11 cursor-pointer py-2 text-sm">{t("artifactCommentOriginal")}</summary>{error && <Alert>{error}</Alert>}{text === null ? <p role="status">{t("loading")}</p> : <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words border p-3 text-sm">{text}</pre>}</details>
}

export function ArtifactCommentThreads({ tenantID, projectID, executionID, evidence }: { tenantID: string; projectID: string; executionID: string; evidence: { id?: string | null; kind?: string; title?: string }[] }) {
  const t = useTranslations("iam"), allowed = useAllowed()
  const canRead = allowed("execution_read"), canWrite = allowed("artifact_comment_write")
  const canManage = allowed("work_item_write")
  const [page, setPage] = useState<ThreadPage | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState("")
  const [evidenceID, setEvidenceID] = useState("")
  const [start, setStart] = useState("1"), [end, setEnd] = useState("1")
  const [body, setBody] = useState("")
  const [anchorKind, setAnchorKind] = useState<"CODE_LINES" | "TABLE_ROW" | "TEXT_PARAGRAPH" | "HTML_REGION">("CODE_LINES")
  const [selection, setSelection] = useState<PackageSourceSelection>({})
  const [source, setSource] = useState<{ name: string; text: string; version_id: string; sha256: string; identity: string } | null>(null)
  const [selected, setSelected] = useState<ArtifactThread | null>(null)
  const [severity, setSeverity] = useState("NORMAL")
  const [priority, setPriority] = useState("MEDIUM")
  const [expected, setExpected] = useState("")
  const pending = useRef(false)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  const path = `/tenants/${encodeURIComponent(tenantID)}/executions/${encodeURIComponent(executionID)}/artifact-comments`
  const sourceIdentity = `${tenantID}:${executionID}:${evidenceID}`
  const files = evidence.filter((item): item is { id: string; kind: string; title?: string } => item.kind === "FILE" && typeof item.id === "string")
  const load = useCallback(async (signal?: AbortSignal, cursor = "") => {
    const result = await iamApi.deliveryResultRequest<ThreadPage>({ method: "GET", url: `${path}?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}` }, { signal })
    if (!signal?.aborted) setPage(result)
  }, [path])
  useEffect(() => {
    const controller = new AbortController()
    queueMicrotask(() => { if (canRead && !controller.signal.aborted) void load(controller.signal).catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause)) }) })
    return () => controller.abort()
  }, [load, canRead])
  useEffect(() => {
    const controller = new AbortController()
    if (evidenceID) void iamApi.deliveryResultRequest<{ name: string; text: string; version_id: string; sha256: string }>({ method: "GET", url: `/tenants/${encodeURIComponent(tenantID)}/executions/${encodeURIComponent(executionID)}/artifact-source?evidence_id=${encodeURIComponent(evidenceID)}` }, { signal: controller.signal }).then(value => { if (!controller.signal.aborted) { setSource({ ...value, identity: `${tenantID}:${executionID}:${evidenceID}` }); setSelection({}) } }).catch(cause => { if (!controller.signal.aborted) { setSource(null); setError(String(cause)) } })
    return () => controller.abort()
  }, [tenantID, executionID, evidenceID])
  const perform = async (kind: "COMMENT" | "DEFECT") => {
    if (pending.current || (kind === "DEFECT" && !selected) || (kind === "COMMENT" && (!source || source.identity !== sourceIdentity))) return
    pending.current = true; setBusy(true); setError(""); setNotice("")
    try {
      const requestBody = kind === "COMMENT" ? { evidence_id: evidenceID, anchor_kind: anchorKind, ...(anchorKind === "CODE_LINES" ? { start_line: Number(start), end_line: Number(end) } : selection), body } : { severity, priority, expected }
      const url = kind === "COMMENT" ? path : `/tenants/${encodeURIComponent(tenantID)}/comment-threads/${encodeURIComponent(selected!.id)}/defects`
      const encoded = JSON.stringify(requestBody), identity = `${url}:${encoded}`
      if (attempt.current?.identity !== identity) attempt.current = { identity, key: crypto.randomUUID() }
      await iamApi.deliveryResultRequest({ method: "POST", url }, { body: encoded, headers: { "Idempotency-Key": attempt.current.key } })
      attempt.current = null
      if (kind === "COMMENT") setBody("")
      else { setSelected(null); setExpected("") }
      setNotice(t(kind === "COMMENT" ? "artifactCommentSaved" : "artifactCommentConverted"))
      await load()
    } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) }
    finally { pending.current = false; setBusy(false) }
  }
  if (!canRead) return null
  return <section className="grid min-w-0 gap-4 border-t pt-4" aria-label={t("artifactCommentsTitle")}>
    <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="font-semibold">{t("artifactCommentsTitle")}</h2><Button variant="outline" disabled={busy} onClick={() => void load().catch(cause => setError(String(cause)))}>{t("refresh")}</Button></div>
    <p className="text-sm text-muted-foreground">{t("artifactCommentsHint")}</p>
    {error && <Alert>{error}</Alert>}{notice && <p role="status">{notice}</p>}
    {!page ? <p role="status">{t("loading")}</p> : page.items.length === 0 ? <p>{t("artifactCommentsEmpty")}</p> : <ul className="grid gap-5">{page.items.map(thread => <li key={thread.id} id={`artifact-comment-${thread.id}`} className="grid min-w-0 gap-3 border-b pb-4">
      <FrozenArtifactAnchor anchor={thread.anchor} />
      <p className="whitespace-pre-wrap break-words">{thread.first_retracted ? t("artifactCommentWithdrawn") : thread.first_body}</p>
      <p className="text-sm text-muted-foreground">{t(`artifactCommentAnchor${thread.anchor_status || "CURRENT"}`)}</p>
      {thread.relocated_anchor && <FrozenArtifactAnchor anchor={thread.relocated_anchor} />}
      <ArtifactOriginalSource key={`${tenantID}:${thread.execution_id}:${thread.evidence_id}`} tenantID={tenantID} thread={thread} />
      <ArtifactCommentConversation key={`${tenantID}:${thread.id}`} tenantID={tenantID} thread={thread} write={allowed("artifact_comment_write")} manage={canManage} evidence={files} onChanged={load} />
      {!thread.feedback_id && canWrite && thread.work_item_id !== "0" && <Link className="min-h-11 py-2 text-sm underline" to={`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(projectID)}/work-items?work=${encodeURIComponent(thread.work_item_id)}&comment_thread=${encodeURIComponent(thread.id)}`}>{t("artifactCommentFeedback")}</Link>}
      <p className="text-sm text-muted-foreground">{thread.state === "RESOLVED" ? t("artifactCommentResolved") : t("artifactCommentOpen")}</p>
      {thread.defect_id ? <Link className="min-h-11 py-2 text-sm underline" to={`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(projectID)}/work-items?work=${encodeURIComponent(thread.defect_id)}`}>{t("artifactCommentOpenDefect")}</Link> : canWrite && <Button variant="outline" className="w-fit" disabled={busy} onClick={() => { setSelected(thread); setExpected(""); setError("") }}>{t("artifactCommentConvert")}</Button>}
    </li>)}</ul>}
    {page?.has_more && <Button variant="outline" disabled={busy} onClick={() => void load(undefined, page.next_cursor).catch(cause => setError(String(cause)))}>{t("artifactCommentsMore")}</Button>}
    {selected && <form className="grid gap-3 rounded border p-4" onSubmit={event => { event.preventDefault(); void perform("DEFECT") }}>
      <h3 className="font-medium">{t("artifactCommentConvert")}</h3><FrozenArtifactAnchor anchor={selected.anchor} /><p className="whitespace-pre-wrap">{selected.first_body}</p>
      <div className="grid gap-3 sm:grid-cols-2"><label className="grid gap-1 text-sm">{t("artifactCommentSeverity")}<select className="min-h-11 rounded border bg-background p-2" value={severity} disabled={busy} onChange={event => setSeverity(event.target.value)}>{["FATAL", "CRITICAL", "NORMAL", "MINOR"].map(value => <option key={value} value={value}>{t(`artifactCommentSeverity_${value}`)}</option>)}</select></label>
      <label className="grid gap-1 text-sm">{t("artifactCommentPriority")}<select className="min-h-11 rounded border bg-background p-2" value={priority} disabled={busy} onChange={event => setPriority(event.target.value)}>{["URGENT", "HIGH", "MEDIUM", "LOW"].map(value => <option key={value} value={value}>{t(`artifactCommentPriority_${value}`)}</option>)}</select></label></div>
      <label className="grid gap-1 text-sm">{t("artifactCommentExpected")}<Textarea required maxLength={2000} disabled={busy} value={expected} onChange={event => setExpected(event.target.value)} /></label>
      <p className="text-sm text-muted-foreground">{t("artifactCommentConversionHint")}</p>
      <div className="flex flex-wrap gap-2"><Button type="submit" disabled={busy || !expected.trim()}>{t("artifactCommentConvert")}</Button><Button type="button" variant="outline" disabled={busy} onClick={() => setSelected(null)}>{t("cancel")}</Button></div>
    </form>}
    {canWrite && (files.length === 0 ? <p>{t("artifactCommentsNoFiles")}</p> : <form className="grid gap-3" onSubmit={event => { event.preventDefault(); void perform("COMMENT") }}>
      <label className="grid gap-1 text-sm">{t("artifactCommentFile")}<select required className="min-h-11 rounded border bg-background p-2" disabled={busy} value={evidenceID} onChange={event => { setSource(null); setSelection({}); setStart("1"); setEnd("1"); setEvidenceID(event.target.value) }}><option value="">{t("artifactCommentSelectFile")}</option>{files.map(file => <option key={file.id} value={file.id}>{file.title || file.id}</option>)}</select></label>
      <label className="grid gap-1 text-sm">{t("artifactCommentAnchorKind")}<select className="min-h-11 rounded border bg-background p-2" value={anchorKind} disabled={busy} onChange={event => { setAnchorKind(event.target.value as typeof anchorKind); setSelection({}) }}>{(["CODE_LINES", "TABLE_ROW", "TEXT_PARAGRAPH", "HTML_REGION"] as const).map(value => <option key={value} value={value}>{t(`packageCommentKind${value}`)}</option>)}</select></label>
      {source?.identity === sourceIdentity && <PackageSourceViewer key={`${evidenceID}:${anchorKind}`} text={source.text} name={source.name} kind={anchorKind} onPick={value => { setSelection(value); if (value.start_line) setStart(String(value.start_line)); if (value.end_line) setEnd(String(value.end_line)) }} />}
      {anchorKind === "CODE_LINES" && <div className="grid gap-3 sm:grid-cols-2"><label className="grid gap-1 text-sm">{t("artifactCommentStart")}<Input required type="number" min={1} step={1} value={start} disabled={busy} onChange={event => setStart(event.target.value)} /></label><label className="grid gap-1 text-sm">{t("artifactCommentEnd")}<Input required type="number" min={Number(start) || 1} max={(Number(start) || 1) + 99} step={1} value={end} disabled={busy} onChange={event => setEnd(event.target.value)} /></label></div>}
      <label className="grid gap-1 text-sm">{t("artifactCommentBody")}<Textarea required maxLength={8000} value={body} disabled={busy} onChange={event => setBody(event.target.value)} /></label>
      <Button type="submit" className="w-fit" disabled={busy || source?.identity !== sourceIdentity || !evidenceID || !body.trim() || (anchorKind === "CODE_LINES" ? Number(end) < Number(start) : !selection.anchor_kind)}>{t("artifactCommentAdd")}</Button>
    </form>)}
  </section>
}
