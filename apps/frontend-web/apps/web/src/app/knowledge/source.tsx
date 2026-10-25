import { useEffect, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { knowledgeSources, type Source, type Chunk, type SourceUsage } from "./source-api"

const sourceReasons = new Map([
  ["ARCHIVED", "knowledgeSourceArchived"],
  ["AGENT_RETIRED", "knowledgeSourceAgentRetired"],
  ["UNSUPPORTED_SOURCE", "knowledgeSourceReason_UNSUPPORTED_SOURCE"],
  ["PDF_EXTRACTION_UNAVAILABLE", "knowledgeSourceReason_PDF_EXTRACTION_UNAVAILABLE"],
  ["TEXT_NOT_EXTRACTABLE", "knowledgeSourceReason_TEXT_NOT_EXTRACTABLE"],
  ["TOO_LARGE", "knowledgeSourceReason_TOO_LARGE"],
  ["CODE_LINE_TOO_LONG", "knowledgeSourceReason_CODE_LINE_TOO_LONG"],
  ["TABLE_ROW_TOO_LONG", "knowledgeSourceReason_TABLE_ROW_TOO_LONG"],
  ["TOO_MANY_CHUNKS", "knowledgeSourceReason_TOO_MANY_CHUNKS"],
  ["MANUALLY_INVALIDATED", "knowledgeSourceReason_MANUALLY_INVALIDATED"],
  ["INDEX_AUTHORITY_REVOKED", "knowledgeSourceReason_INDEX_AUTHORITY_REVOKED"],
  ["INGESTION_RETRIES_EXHAUSTED", "knowledgeSourceReason_INGESTION_RETRIES_EXHAUSTED"],
])

export default function KnowledgeSourcePage() {
  const {tenantID = "", entityID = "", sourceID = ""} = useParams()
  return <SourcePanel key={`${tenantID}:${entityID}:${sourceID}`} tenant={tenantID} project={entityID} sourceID={sourceID} />
}
function SourcePanel({tenant, project, sourceID}: {tenant: string; project: string; sourceID: string}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [search, setSearch] = useSearchParams()
  const tab = search.get("tab") === "chunks" ? "chunks" : "overview"
  const [source, setSource] = useState<Source | null>(null)
  const [usage, setUsage] = useState<SourceUsage | null>(null)
  const [chunks, setChunks] = useState<Chunk[]>([])
  const [next, setNext] = useState<string | null>(null)
  const [query, setQuery] = useState("")
  const [replacement, setReplacement] = useState("")
  const [confirm, setConfirm] = useState(false)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const [pagingBusy, setPagingBusy] = useState(false)
  const paging = useRef<AbortController | null>(null)
  useEffect(() => {
    paging.current?.abort()
    paging.current = null
    const controller = new AbortController()
    queueMicrotask(() => {if (!controller.signal.aborted) {setLoading(true); setPagingBusy(false); setSource(null); setUsage(null); setChunks([]); setNext(null)}})
    void knowledgeSources.get(tenant, sourceID, controller.signal).then(async row => {
      if ((row.project_id ?? "") !== project) throw Error(t("knowledgeSourceScopeMismatch"))
      const page = await knowledgeSources.chunkPage(tenant, sourceID, "", controller.signal)
      const usageFacts = await knowledgeSources.usage(tenant, sourceID, controller.signal)
      if (!controller.signal.aborted) {setSource(row); setUsage(usageFacts); setChunks(page.items ?? []); setNext(page.has_more ? page.next_cursor ?? null : null); setError("")}
    }, cause => {throw cause}).catch(cause => {if (!controller.signal.aborted) setError(cause.message)}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
    return () => {controller.abort(); paging.current?.abort(); paging.current = null}
  }, [tenant, project, sourceID, revision, t])
  const refresh = () => {
    paging.current?.abort()
    paging.current = null
    setPagingBusy(false)
    setRevision(value => value + 1)
  }
  const more = async () => {
    if (paging.current || busy || loading || next === null) return
    const controller = new AbortController()
    paging.current = controller
    setPagingBusy(true); setError("")
    try {
      const page = await knowledgeSources.chunkPage(tenant, sourceID, next, controller.signal)
      if (!controller.signal.aborted && paging.current === controller) {
        setChunks(old => [...old, ...(page.items ?? [])])
        setNext(page.has_more ? page.next_cursor ?? null : null)
      }
    } catch (cause) {
      if (!controller.signal.aborted && paging.current === controller) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))
    } finally {
      if (paging.current === controller) {paging.current = null; if (!controller.signal.aborted) setPagingBusy(false)}
    }
  }
  const reasonKey = sourceReasons.get(source?.state_reason ?? "")
  const base = `/admin/${encodeURIComponent(tenant)}${project ? `/entities/${encodeURIComponent(project)}` : ""}/knowledge`
  const run = async (action: (row: Source) => Promise<Source>) => {
    if (!source || busy) return
    setBusy(true); setError("")
    try {await action(source); refresh(); setConfirm(false); setReplacement("")}
    catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
    finally {setBusy(false)}
  }
  return <div className="flex min-w-0 flex-col gap-5">
    <Link className="inline-flex min-h-11 items-center underline" to={`${base}/sources`}>{t("knowledgeSources")}</Link>
    <PageHeader title={source?.title ?? t("knowledgeSource")} description={t("knowledgeSourceUploadHint")} action={<Button variant="outline" disabled={loading || busy} onClick={refresh}>{t("refresh")}</Button>} />
    {error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
    {source && !loading && <>
      <nav className="flex flex-wrap gap-2 border-b pb-3" aria-label={t("knowledgeSource")}>{["overview", "chunks"].map(value => <Button key={value} className="min-h-11" aria-current={tab === value ? "page" : undefined} variant={tab === value ? "default" : "outline"} onClick={() => setSearch({tab: value})}>{t(value === "overview" ? "overview" : "knowledgeChunks")}</Button>)}</nav>
      {tab === "overview" ? <>
        {source.origin_id && <section aria-label={t("knowledgeSourceEventOrigin")} className="space-y-3 border-b pb-4">
          <h2 className="font-medium">{t("knowledgeSourceEventOrigin")}</h2>
          <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
            <div><dt className="text-muted-foreground">{t("agentField_kind")}</dt><dd>{t(`knowledgeSourceKind_${source.source_kind}`)}</dd></div>
            <div><dt className="text-muted-foreground">{t("knowledgeSourceOriginID")}</dt><dd className="break-all font-mono">{source.origin_id}</dd></div>
            <div><dt className="text-muted-foreground">{t("knowledgeSourceOriginVersion")}</dt><dd>{source.origin_version}</dd></div>
            {source.source_kind === "DELIVERY" && <div><dt className="text-muted-foreground">{t("knowledgeSourceClientAcceptance")}</dt><dd>{t(source.client_accepted ? "knowledgeSourceClientAccepted" : "knowledgeSourceClientNotAccepted")}</dd></div>}
          </dl>
          {source.work_item_id && project && <Link className="inline-flex min-h-11 items-center underline" to={`/admin/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(project)}/work-items/${encodeURIComponent(source.work_item_id)}`}>{t("clientWorkItem")} {source.work_item_id}</Link>}
        </section>}
        <AutoForm mode="detail" initialData={{...source, state: t(`knowledgeSourceState_${source.state}`)}} fields={{id: {name: t("knowledgeSource"), type: "text"}, source_kind: {name: t("agentField_kind"), type: "text"}, state: {name: t("fleetState"), type: "text"}, locator: {name: t("knowledgeSourceLocator"), type: "text"}, format: {name: t("knowledgeSourceFormat"), type: "text"}, content_hash: {name: t("agentField_content_hash"), type: "text"}, chunk_count: {name: t("knowledgeChunkCount"), type: "number"}, text_chars: {name: t("knowledgeTextChars"), type: "number"}, attempts: {name: t("knowledgeIndexAttempts"), type: "number"}, indexed_at: {name: t("knowledgeIndexedAt"), type: "text"}, version: {name: t("fleetVersion"), type: "number"}}} />
        {source.state_reason && <Alert>{t("knowledgeSourceReason")}: {reasonKey ? t(reasonKey) : source.state_reason}</Alert>}
        {usage?.available ? <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2"><div><dt>{t("knowledgeUsageInclusions")}</dt><dd>{usage.inclusions}</dd></div><div><dt>{t("knowledgeUsageCitations")}</dt><dd>{usage.citations}</dd></div></dl> : <p className="text-sm text-muted-foreground">{t("knowledgeUsageUnavailable")}</p>}
        {source.archived_at && <p role="status">{t("knowledgeSourceArchived")} {source.archived_at}</p>}
        {allowed("knowledge_write") && !source.archived_at && <section className="space-y-4 rounded-md border p-4">
          <AutoForm key={source.version} mode="edit" initialData={{title: source.title, tags: (source.tags ?? []).join(", ")}} fields={{title: {name: t("name"), type: "text", required: true}, tags: {name: t("knowledgeSourceTags"), type: "text"}}} onSubmit={values => run(row => knowledgeSources.metadata(tenant, sourceID, {version: row.version, title: String(values.title), tags: String(values.tags ?? "").split(",").map(value => value.trim()).filter(Boolean)}, crypto.randomUUID()))}><Button type="submit" disabled={busy}>{t("save")}</Button></AutoForm>
          <Button disabled={busy || source.state === "INVALIDATED" || source.source_kind !== "UPLOAD" || source.format === "PDF"} onClick={() => void run(row => knowledgeSources.ingest(tenant, sourceID, {version: row.version}, crypto.randomUUID()))}>{t("knowledgeQueueSource")}</Button>
          <div className="flex flex-wrap gap-3"><Button disabled={busy || source.source_kind !== "UPLOAD" || source.format === "PDF"} onClick={() => void run(row => knowledgeSources.sync(tenant, sourceID, {version: row.version}, crypto.randomUUID()))}>{t("knowledgeSyncSource")}</Button><Button variant="outline" disabled={busy || source.source_kind !== "UPLOAD" || source.format === "PDF"} onClick={() => void run(row => knowledgeSources.reindex(tenant, sourceID, row.version, crypto.randomUUID()))}>{t("knowledgeReindexSource")}</Button></div>
          {source.source_kind === "UPLOAD" && source.format !== "PDF" && <details><summary className="min-h-11 cursor-pointer">{t("knowledgeReplaceSource")}</summary><label className="grid gap-2 text-sm">{t("knowledgeSourceContent")}<textarea className="min-h-40 w-full rounded-md border bg-background p-3" value={replacement} onChange={event => setReplacement(event.target.value)} maxLength={2000000} /></label><Button className="mt-3" disabled={busy || !replacement.trim()} onClick={() => void run(row => knowledgeSources.sync(tenant, sourceID, {version: row.version, content: replacement}, crypto.randomUUID()))}>{t("knowledgeReplaceSource")}</Button></details>}
          <label className="flex min-h-11 items-center gap-3 text-sm"><input type="checkbox" checked={confirm} onChange={event => setConfirm(event.target.checked)} />{t("knowledgeInvalidateConfirm")}</label><Button variant="destructive" disabled={busy || !confirm || source.state === "INVALIDATED"} onClick={() => void run(row => knowledgeSources.invalidate(tenant, sourceID, row.version, crypto.randomUUID()))}>{t("knowledgeInvalidateSource")}</Button>
          <Button variant="destructive" disabled={busy || !confirm} onClick={() => void run(row => knowledgeSources.archive(tenant, sourceID, row.version, crypto.randomUUID()))}>{t("knowledgeSourceArchive")}</Button>
        </section>}
      </> : <>
        <label className="grid gap-2 text-sm">{t("knowledgeChunkSearch")}<Input value={query} onChange={event => setQuery(event.target.value)} /></label>
        <p className="text-sm text-muted-foreground">{t("knowledgeTokenEstimateHint")}</p>
        <div className="divide-y border-y">{chunks.filter(chunk => `${chunk.heading_path} ${chunk.content}`.toLocaleLowerCase().includes(query.toLocaleLowerCase())).map(chunk => <article key={chunk.id} className="min-w-0 space-y-3 py-5"><h2 className="font-medium">#{chunk.ordinal + 1} · {chunk.heading_path || chunk.kind}</h2><p className="break-all font-mono text-xs">{chunk.content_hash}</p><p className="text-sm text-muted-foreground">{t("knowledgeTextChars")}: {chunk.char_count} · {t("knowledgeTokenEstimate")}: {chunk.token_estimate}</p><pre className="whitespace-pre-wrap break-words text-sm">{chunk.content}</pre></article>)}</div>
        {!chunks.length && <p>{t("agentEmpty")}</p>}
        {next !== null && <Button variant="outline" disabled={busy || loading || pagingBusy} onClick={() => void more()}>{t("agentMore")}</Button>}
      </>}
    </>}
  </div>
}
