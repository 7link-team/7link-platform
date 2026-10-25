import { useEffect, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import type { components } from "../../lib/api-schema"

import {MetricsHistory} from "./metrics-history"

export type DebugQuery = components["schemas"]["RetrievalDebugQuery"]
export type DebugResult = components["schemas"]["RetrievalDebugResult"]

export default function RetrievalDebugPage() {
  const {tenantID = "", entityID = ""} = useParams()
  return <RetrievalDebug key={`${tenantID}:${entityID}`} tenant={tenantID} project={entityID}/>
}

function RetrievalDebug({tenant, project}: {tenant: string; project: string}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [search, setSearch] = useSearchParams()
  const [query, setQuery] = useState(search.get("q") || "")
  const [mode, setMode] = useState<"ALL" | "ANY">(search.get("mode") === "ANY" ? "ANY" : "ALL")
  const [scope, setScope] = useState<"TEAM" | "PROJECT" | "AGENT">(project ? "PROJECT" : "TEAM")
  const [agent, setAgent] = useState("")
  const [simulate, setSimulate] = useState("")
  const [top, setTop] = useState(10)
  const [result, setResult] = useState<DebugResult | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const pending = useRef<AbortController | null>(null)
  const retry = useRef<{body: string; key: string} | null>(null)
  useEffect(() => () => pending.current?.abort(), [])
  if (!allowed("knowledge_manage")) return <Alert>{t("retrievalDenied")}</Alert>
  const root = `/admin/${encodeURIComponent(tenant)}${project ? `/entities/${encodeURIComponent(project)}` : ""}/knowledge`
  const run = async () => {
    const body: DebugQuery = {scope, query: query.trim(), mode, top_k: top, ...(simulate && project && scope !== "TEAM" ? {simulate_agent_id: simulate} : {}), ...(scope !== "TEAM" && project ? {project_id: project} : {}), ...(scope === "AGENT" ? {agent_principal_id: agent} : {})}
    const encoded = JSON.stringify(body)
    const key = retry.current?.body === encoded ? retry.current.key : crypto.randomUUID()
    retry.current = {body: encoded, key}
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setBusy(true); setError(""); setResult(null)
    setSearch({q: body.query, mode}, {replace: true})
    try {
      const value = await iamApi.retrievalDebug(tenant, body, key, controller.signal)
      if (!controller.signal.aborted) { setResult(value); retry.current = null }
    } catch (e) {
      if (!controller.signal.aborted) setError(e instanceof Error ? e.message : String(e))
    } finally { if (!controller.signal.aborted) setBusy(false) }
  }
  const exportJSON = () => {
    if (!result) return
    const url = URL.createObjectURL(new Blob([JSON.stringify(result, null, 2)], {type: "application/json"}))
    const link = document.createElement("a")
    link.href = url; link.download = "retrieval-debug.json"; link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return <div className="min-w-0 space-y-5">
    <PageHeader title={t("retrievalTitle")} description={t("retrievalDescription")} action={<div className="flex flex-wrap gap-2"><Button variant="outline" asChild><Link to={root}>{t("back")}</Link></Button><Button variant="outline" disabled={!result || busy} onClick={exportJSON}>{t("retrievalExport")}</Button></div>}/>
    <div className="lg:hidden"><Alert>{t("retrievalDesktop")}</Alert></div>
    {error && <Alert>{error}</Alert>}
    <div className="grid min-w-0 gap-5 lg:grid-cols-[minmax(16rem,1fr)_minmax(0,2fr)]">
      <form className="hidden space-y-4 rounded-lg border p-4 lg:block" onSubmit={e => {e.preventDefault(); void run()}}>
        <label className="block space-y-1 text-sm"><span>{t("retrievalQuery")}</span><Input required maxLength={500} value={query} onChange={e => setQuery(e.target.value)}/></label>
        <label className="block space-y-1 text-sm"><span>{t("retrievalMode")}</span><select className="min-h-11 w-full rounded-md border bg-background px-3" value={mode} onChange={e => setMode(e.target.value as typeof mode)}><option value="ALL">{t("retrievalAll")}</option><option value="ANY">{t("retrievalAny")}</option></select></label>
        <label className="block space-y-1 text-sm"><span>{t("knowledgeScope")}</span><select className="min-h-11 w-full rounded-md border bg-background px-3" value={scope} onChange={e => setScope(e.target.value as typeof scope)}>{["TEAM", ...(project ? ["PROJECT"] : []), "AGENT"].map(s => <option key={s} value={s}>{t(`knowledgeScope_${s}`)}</option>)}</select></label>
        {scope === "AGENT" && <label className="block space-y-1 text-sm"><span>{t("knowledgeAgent")}</span><Input required pattern="[1-9][0-9]*" inputMode="numeric" value={agent} onChange={e => setAgent(e.target.value)}/></label>}
        <label className="block space-y-1 text-sm"><span>{t("retrievalTop")}</span><Input required type="number" min={1} max={200} step={1} value={top} onChange={e => setTop(Number(e.target.value))}/></label>
        {project && scope !== "TEAM" && <label className="block space-y-1 text-sm"><span>{t("retrievalSimulateAgent")}</span><Input pattern="[1-9][0-9]*" inputMode="numeric" value={simulate} onChange={e => setSimulate(e.target.value)}/></label>}
        <p className="text-sm text-muted-foreground">{t("retrievalPerspective")}</p>
        <Button type="submit" disabled={busy}>{t(busy ? "loading" : "retrievalRun")}</Button>
        <p className="break-words font-mono text-sm">0.55T + 0.15R + 0.15W + 0.15S</p>
        <p className="text-sm text-muted-foreground">{t("retrievalFormula")}</p>
        <p className="text-sm text-muted-foreground">{t("retrievalBackend")}</p>
      </form>
      <section aria-live="polite" aria-busy={busy} className="min-w-0 space-y-3">
        {!result && <p className="text-sm text-muted-foreground">{t(busy ? "loading" : "retrievalInitial")}</p>}
        {result && <>
          <p className="text-sm">{t("retrievalBackend")} · {result.scanned} · {new Date(result.evaluated_at).toLocaleString()}</p>
          <div className="space-y-2 rounded border p-3 text-sm">
            <p>{result.dialect} ? {result.engine} ? {t("retrievalNativeUnavailable")} ? {t("retrievalVectorUnavailable")}</p>
            <p className="break-words font-mono">{result.plan?.expression}</p>
            {result.timings && <dl className="grid grid-cols-2 gap-2 sm:grid-cols-3">{Object.entries(result.timings).map(([stage, ms]) => <div key={stage}><dt>{t(`retrievalStage_${stage}`)}</dt><dd className="font-mono">{Number(ms).toFixed(3)} ms</dd></div>)}</dl>}
          </div>
          {result.truncated && <Alert>{t("retrievalTruncated")}</Alert>}
          <div className="flex flex-wrap gap-2">{result.tokens?.map(token => <span key={token} className="max-w-full break-all rounded border px-2 py-1 text-sm">{token}</span>)}</div>
          {!result.candidates?.some(c => c.rank > 0) && <p>{t("retrievalEmpty")}</p>}
          {result.candidates?.map(c => <article key={c.entry.id} className={`min-w-0 space-y-2 rounded-lg border p-4 ${c.exclusion ? "text-muted-foreground" : ""}`}>
            <h2 className="break-words font-medium">{c.rank > 0 ? `${c.rank}. ` : ""}{c.entry.title}</h2>
            <p className="break-words text-sm">{c.entry.provenance} · {c.entry.id} · v{c.entry.version}</p>
            <p className="whitespace-pre-wrap break-words text-sm">{c.entry.body}</p>
            {c.exclusion && <p className="text-sm">{t(`retrievalExclude_${c.exclusion}`)}</p>}
            <dl className="grid grid-cols-2 gap-2 text-sm sm:grid-cols-4">{[["T", c.t], ["R", c.r], ["W", c.w], ["S", c.s], [t("retrievalRaw"), c.text_raw], [t("retrievalTextRank"), c.text_rank], [t("retrievalFinal"), c.final]].map(([label, value]) => <div key={label}><dt>{label}</dt><dd className="font-mono">{Number(value).toFixed(6)}</dd></div>)}</dl>
          </article>)}
        </>}
      </section>
    </div>
    <MetricsHistory tenant={tenant} project={project} onSelect={setResult}/>
  </div>
}
