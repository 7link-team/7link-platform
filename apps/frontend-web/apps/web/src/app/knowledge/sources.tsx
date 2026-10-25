import { useEffect, useRef, useState } from "react"
import { Link, useNavigate, useParams, useSearchParams } from "react-router"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { knowledgeSources, type Source, type SourceQuery } from "./source-api"

export default function KnowledgeSourcesPage() {
  const {tenantID = "", entityID = ""} = useParams()
  const [search] = useSearchParams()
  return <Sources key={`${tenantID}:${entityID}:${search.get("state") ?? ""}`} tenant={tenantID} project={entityID} />
}
function Sources({tenant, project}: {tenant: string; project: string}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const navigate = useNavigate()
  const [search, setSearch] = useSearchParams()
  const state = search.get("state") ?? ""
  const [items, setItems] = useState<Source[]>([])
  const [cursor, setCursor] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [files, setFiles] = useState<File[]>([])
  const uploadAttempt = useRef<string | null>(null)
  const [revision, setRevision] = useState(0)
  const paging = useRef<AbortController | null>(null)
  const base = `/admin/${encodeURIComponent(tenant)}${project ? `/entities/${encodeURIComponent(project)}` : ""}/knowledge`
  const query: SourceQuery = {scope: project ? "PROJECT" : "TEAM", project_id: project || undefined, archived: state === "ARCHIVED", state: ["PENDING", "INDEXING", "INDEXED", "FAILED", "INVALIDATED"].includes(state) ? state as SourceQuery["state"] : undefined, limit: 50}
  const queryKey = JSON.stringify(query)
  useEffect(() => {
    paging.current?.abort()
    paging.current = null
    const controller = new AbortController()
    queueMicrotask(() => {if (!controller.signal.aborted) {setLoading(true); setItems([]); setCursor("")}})
    void knowledgeSources.list(tenant, JSON.parse(queryKey) as SourceQuery, controller.signal).then(page => {if (!controller.signal.aborted) {setItems(page.items ?? []); setCursor(page.next_cursor ?? ""); setError("")}}, cause => {if (!controller.signal.aborted) setError(cause.message)}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
    return () => {controller.abort(); paging.current?.abort(); paging.current = null}
  }, [tenant, queryKey, revision])
  const refresh = () => {
    paging.current?.abort()
    paging.current = null
    setRevision(value => value + 1)
  }
  const more = async () => {
    if (paging.current || loading || !cursor) return
    const controller = new AbortController()
    paging.current = controller
    setLoading(true); setError("")
    try {
      const page = await knowledgeSources.list(tenant, {...query, cursor}, controller.signal)
      if (!controller.signal.aborted && paging.current === controller) {
        setItems(old => [...old, ...(page.items ?? [])])
        setCursor(page.next_cursor ?? "")
      }
    } catch (cause) {
      if (!controller.signal.aborted && paging.current === controller) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))
    } finally {
      if (paging.current === controller) {paging.current = null; if (!controller.signal.aborted) setLoading(false)}
    }
  }
  return <div className="flex min-w-0 flex-col gap-5">
    <PageHeader title={t("knowledgeSources")} description={t("knowledgeSourcesHint")} />
    <div className="flex flex-wrap gap-3"><Link className="inline-flex min-h-11 items-center underline" to={base}>{t("knowledge")}</Link>{allowed("knowledge_manage") && <Link className="inline-flex min-h-11 items-center underline" to={`/admin/${encodeURIComponent(tenant)}/knowledge/settings`}>{t("knowledgeSettings")}</Link>}</div>
    {error && <Alert>{error}</Alert>}
    {allowed("knowledge_write") && <form className="space-y-3 rounded border p-4" onSubmit={event => {
      event.preventDefault()
      if (busy || !files.length) return
      if (files.length > 10 || files.some(file => file.size > 10 * 1024 * 1024)) {setError(t("knowledgeUploadHint")); return}
      setBusy(true); setError("")
      uploadAttempt.current ??= crypto.randomUUID()
      void knowledgeSources.upload(tenant, project, files, uploadAttempt.current).then(() => {uploadAttempt.current = null; setFiles([]); refresh()}).catch(cause => setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))).finally(() => setBusy(false))
    }}><label className="grid gap-2 text-sm">{t("knowledgeUploadDocuments")}<input type="file" multiple disabled={busy} accept=".md,.markdown,.txt,.go,.js,.ts,.tsx,.py,.rs,.java,.css,.html,.json,.yaml,.yml,.sql,.sh" onChange={event => {setFiles(Array.from(event.target.files ?? [])); uploadAttempt.current = null}} /></label><p className="text-sm text-muted-foreground">{t("knowledgeUploadHint")}</p><Button type="submit" disabled={busy || !files.length}>{busy ? t("loading") : t("knowledgeUploadDocuments")}</Button></form>}
    <label className="grid gap-2 text-sm">{t("fleetState")}<select className="min-h-11 w-full max-w-sm rounded-md border bg-background px-3" value={state} onChange={event => {paging.current?.abort(); paging.current = null; setSearch(event.target.value ? {state: event.target.value} : {})}}><option value="">{t("runtimeAllStates")}</option>{["PENDING", "INDEXING", "INDEXED", "FAILED", "INVALIDATED", "ARCHIVED"].map(value => <option key={value} value={value}>{value === "ARCHIVED" ? t("knowledgeSourceArchived") : t(`knowledgeSourceState_${value}`)}</option>)}</select></label>
    <AutoTable data={items.map(row => ({...row, state_label: t(`knowledgeSourceState_${row.state}`)}))} rowKey="id" loading={loading} pagination={false} exportable={false} emptyContent={t("agentEmpty")} onRefresh={refresh} columns={[{field: "title", name: t("name"), format: (_, row) => <Link className="underline" to={`${base}/sources/${row.id}`}>{row.title}</Link>}, {field: "source_kind", name: t("agentField_kind")}, {field: "state_label", name: t("fleetState")}, {field: "chunk_count", name: t("knowledgeChunkCount")}, {field: "updated_at", name: t("fleetCreatedAt")}]} />
    {cursor && <Button disabled={loading} className="self-start" variant="outline" onClick={() => void more()}>{t("agentMore")}</Button>}
    {allowed("knowledge_write") && <details className="rounded-md border p-4"><summary className="min-h-11 cursor-pointer font-medium">{t("knowledgeBindSource")}</summary><p className="my-3 text-sm text-muted-foreground">{t("knowledgeSourceUploadHint")}</p>
      <AutoForm mode="edit" initialData={{source_kind: "UPLOAD", title: "", locator: "", format: "MARKDOWN", content: ""}} fields={{source_kind: {name: t("agentField_kind"), type: "select", config: {options: ["UPLOAD", "URL", "REPOSITORY"].map(value => ({value, label: t(`knowledgeSourceKind_${value}`)}))}}, title: {name: t("name"), type: "text", required: true}, locator: {name: t("knowledgeSourceLocator"), type: "text", required: true}, format: {name: t("knowledgeSourceFormat"), type: "select", config: {options: ["MARKDOWN", "TEXT", "CODE", "PDF"].map(value => ({value, label: value}))}}, content: {name: t("knowledgeSourceContent"), type: "textarea"}}} onSubmit={async values => {
        if (busy) return
        setBusy(true); setError("")
        try {const row = await knowledgeSources.create(tenant, {scope: project ? "PROJECT" : "TEAM", project_id: project || undefined, source_kind: String(values.source_kind) as "UPLOAD", title: String(values.title), locator: String(values.locator), format: String(values.format) as "MARKDOWN", content: String(values.content ?? "")}, crypto.randomUUID()); navigate(`${base}/sources/${row.id}`)}
        catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
        finally {setBusy(false)}
      }}><Button type="submit" disabled={busy}>{t("knowledgeBindSource")}</Button></AutoForm>
    </details>}
  </div>
}
