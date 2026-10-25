import { useEffect, useRef, useState } from "react"
import { Link } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { highlightedParts, nativeKnowledge, type SearchRequest, type SearchResult } from "./native-api"

export function NativeSearch({tenant, project}: {tenant: string; project: string}) {
  const t = useTranslations("iam")
  const [query, setQuery] = useState("")
	const [includeArchived, setIncludeArchived] = useState(false)
  const [mode, setMode] = useState<"ALL" | "ANY">("ALL")
  const [tags, setTags] = useState("")
  const [sourceKind, setSourceKind] = useState("")
  const [scope, setScope] = useState("")
  const [projects, setProjects] = useState("")
  const [agents, setAgents] = useState("")
  const [updatedAfter, setUpdatedAfter] = useState("")
  const [updatedBefore, setUpdatedBefore] = useState("")
  const [result, setResult] = useState<SearchResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const pending = useRef<AbortController | null>(null)
  const last = useRef<SearchRequest | null>(null)
  useEffect(() => () => pending.current?.abort(), [])
  async function search(offset: number, previous?: SearchRequest) {
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    const split = (value: string) => value.split(",").map(part => part.trim()).filter(Boolean)
    const body: SearchRequest = previous ? {...previous, offset} : {query: query.trim(), match_mode: mode, scopes: scope ? [scope] : project ? ["TEAM", "PROJECT"] : undefined, project_ids: project ? [project] : split(projects), agent_ids: split(agents), updated_after: updatedAfter ? new Date(updatedAfter).toISOString() : undefined, updated_before: updatedBefore ? new Date(updatedBefore).toISOString() : undefined, tags: split(tags), source_kinds: sourceKind ? [sourceKind] : [], offset, limit: 50, explain: false}
	if (!previous) body.include_archived_projects = includeArchived
    if (!body.query) return
    last.current = body
    setBusy(true); setError(""); setResult(null)
    try {const value = await nativeKnowledge.search(tenant, body, crypto.randomUUID(), controller.signal); if (!controller.signal.aborted) setResult(value)}
    catch (cause) {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
    finally {if (!controller.signal.aborted) setBusy(false)}
  }
  return <section className="min-w-0 space-y-3" aria-label={t("knowledgeNativeSearch")}>
    <form className="flex flex-wrap items-end gap-3" onSubmit={event => {event.preventDefault(); void search(0)}}>
      <label className="min-w-48 flex-1 space-y-1">{t("knowledgeNativeSearch")}<Input value={query} maxLength={500} onChange={event => setQuery(event.target.value)} /></label>
      <label className="space-y-1">{t("knowledgeNativeMatch")}<select className="block min-h-11 rounded-md border bg-background px-3" value={mode} onChange={event => setMode(event.target.value as "ALL" | "ANY")}><option value="ALL">{t("knowledgeNativeAll")}</option><option value="ANY">{t("knowledgeNativeAny")}</option></select></label>
      <label className="space-y-1">{t("knowledgeNativeTags")}<Input value={tags} maxLength={500} onChange={event => setTags(event.target.value)} /></label>
      <label className="space-y-1">{t("knowledgeNativeSource")}<select className="block min-h-11 rounded-md border bg-background px-3" value={sourceKind} onChange={event => setSourceKind(event.target.value)}><option value="">{t("knowledgeNativeAllSources")}</option>{["UPLOAD", "URL", "REPOSITORY", "DELIVERY", "REQUIREMENT", "FEEDBACK", "DECISION"].map(value => <option key={value} value={value}>{value}</option>)}</select></label>
      <label className="space-y-1">{t("knowledgeScope")}<select className="block min-h-11 rounded-md border bg-background px-3" value={scope} onChange={event => setScope(event.target.value)}><option value="">{t("knowledgeNativeAllScopes")}</option>{["TEAM", "PROJECT", "AGENT"].map(value => <option key={value} value={value}>{t(`knowledgeScope_${value}`)}</option>)}</select></label>
      {!project && <label className="space-y-1">{t("knowledgeNativeProjects")}<Input value={projects} maxLength={400} onChange={event => setProjects(event.target.value)}/></label>}
      <label className="space-y-1">{t("knowledgeAgent")}<Input value={agents} maxLength={200} onChange={event => setAgents(event.target.value)}/></label>
      <label className="space-y-1">{t("knowledgeNativeUpdatedAfter")}<Input type="datetime-local" value={updatedAfter} onChange={event => setUpdatedAfter(event.target.value)}/></label>
      <label className="space-y-1">{t("knowledgeNativeUpdatedBefore")}<Input type="datetime-local" value={updatedBefore} onChange={event => setUpdatedBefore(event.target.value)}/></label>
      <Button type="submit" disabled={busy || !query.trim()}>{t("agentBrowseApply")}</Button>
	  <label className="flex min-h-11 items-center gap-2"><input type="checkbox" checked={includeArchived} onChange={event => setIncludeArchived(event.target.checked)}/>{t("archiveSearchInclude")}</label>
    </form>
    {busy && <p role="status">{t("loading")}</p>}{error && <Alert>{error}</Alert>}
    {result && <>
      <p role="status" className="text-sm text-muted-foreground">{t("knowledgeNativeResultMode")} {result.mode} · {result.total_candidates}{result.fallback_reason && <span> · {result.fallback_reason}</span>}</p>
      <p className="text-sm text-muted-foreground">{t("knowledgeFullText")}: {t(result.native_sql_available ? "knowledgeConnectorAvailable" : "knowledgeConnectorUnavailable")}</p>
      {!result.results.length && <p>{t("knowledgeNativeEmpty")}</p>}
      <ol className="divide-y">{result.results.map(hit => <li key={hit.chunk_id} className="space-y-2 py-4">
		{hit.project_archived && <p className="text-sm font-medium">{t("archiveReadOnly")}</p>}
        <Link className="break-words font-medium underline" to={`/admin/${encodeURIComponent(tenant)}${project ? `/entities/${encodeURIComponent(project)}` : ""}/knowledge/sources/${encodeURIComponent(hit.source_id)}`}>{hit.title}</Link>
        {hit.heading_path && <p className="break-words text-sm text-muted-foreground">{hit.heading_path}</p>}
        <p className="whitespace-pre-wrap break-words">{highlightedParts(hit.snippet, hit.highlights).map((part, index) => part.marked ? <mark key={index}>{part.text}</mark> : part.text)}</p>
      </li>)}</ol>
      <nav className="flex gap-3" aria-label={t("knowledgeNativeSearch")}><Button variant="outline" disabled={busy || result.offset === 0} onClick={() => void search(Math.max(0, result.offset - 50), last.current!)}>{t("knowledgeHistoryPrevious")}</Button><Button variant="outline" disabled={busy || result.offset + result.results.length >= result.total_candidates} onClick={() => void search(result.offset + 50, last.current!)}>{t("knowledgeHistoryNext")}</Button></nav>
    </>}
  </section>
}
