import { useCallback, useEffect, useMemo, useState, useRef, type KeyboardEvent } from "react"
import { useNavigate, useParams, useSearchParams } from "react-router"
import { InboxPreview } from "./inbox-preview"
import { Eye } from "lucide-react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useRealtime } from "@workspace/ui/hooks/use-realtime"
import { PageHeader } from "../../components/page-header"
import { Alert } from "../../components/alert"
import { governanceApi, inboxKinds, inboxObjectHref, type InboxItem, type InboxPage } from "./api"

export default function GovernanceInboxPage() {
  const {tenantID = ""} = useParams()
  return <Inbox key={tenantID} tenantID={tenantID} />
}

function Inbox({tenantID}: {tenantID: string}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const navigate = useNavigate()
  const api = useMemo(() => governanceApi(tenantID), [tenantID])
  const [search, setSearch] = useSearchParams()
  const kind = search.get("kind") ?? ""
  const project = search.get("project_id") ?? ""
  const cursor = search.get("cursor") ?? ""
  const selectedKey = search.get("item") ?? ""
  const [page, setPage] = useState<InboxPage | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [refresh, setRefresh] = useState(0)
  const selected = Math.max(0, page?.items.findIndex(item => `${item.kind}:${item.id}` === selectedKey) ?? 0)
  const [armed,setArmed] = useState<"A"|"R"|null>(null)
  const preview=useRef<HTMLDivElement>(null)
  const [counts,setCounts]=useState<Awaited<ReturnType<typeof api.counts>>|null>(null)
  useEffect(()=>{const c=new AbortController();void api.counts({project_id:project},c.signal).then(v=>{if(!c.signal.aborted)setCounts(v)},()=>{if(!c.signal.aborted)setCounts(null)});return()=>c.abort()},[api,project,refresh])
  const select=(index:number)=>{setArmed(null);const item=page?.items[index];if(item)setSearch(previous=>{const next=new URLSearchParams(previous);next.set("item",`${item.kind}:${item.id}`);return next},{replace:true})}
  useEffect(() => {
    const controller = new AbortController()
    void api.inbox({kind, project_id: project, cursor}, controller.signal).then(value => {
      if (controller.signal.aborted) return
      setPage(value); setLoading(false); setError(""); setArmed(null)
    }, cause => {if (!controller.signal.aborted) {setPage(null); setLoading(false); setError(cause instanceof Error ? cause.message : t("govUnavailable"))}})
    return () => controller.abort()
  }, [api, kind, project, cursor, refresh, t])
  const reload = useCallback(() => {setLoading(true); setRefresh(value => value + 1)}, [])
  // Hints carry no decision facts. Re-fetch the same authorized, filtered page
  // so another device's action removes stale decisions without navigating away.
  const reconcile = useCallback(() => setRefresh(value => value + 1), [])
  useRealtime(`tenant/${tenantID}/governance_inbox`, {onEvent: reconcile, onResync: reconcile, poll: reconcile, pollIntervalMs: 10_000})
  const open = (row: InboxItem) => {const href = inboxObjectHref(tenantID, row); if (href) navigate(href)}
  const onKeyDown = (event: KeyboardEvent) => {
    const target = event.target as HTMLElement
    if (event.ctrlKey || event.metaKey || event.altKey || target.closest("input,textarea,select,[contenteditable=true]")) return
    if(event.key==="Enter"&&target.closest("button,a"))return
    const rows = page?.items ?? []
    if (!rows.length || loading) return
    if (event.key.toLowerCase() === "j") {event.preventDefault(); select(Math.min(rows.length - 1, selected + 1))}
    if (event.key.toLowerCase() === "k") {event.preventDefault(); select(Math.max(0, selected - 1))}
    if(event.key==="Escape"){event.preventDefault();setArmed(null);return}
    if (["a","r"].includes(event.key.toLowerCase())) {event.preventDefault();(event.currentTarget as HTMLElement).focus();setArmed(event.key.toLowerCase()==="a"?"A":"R");return}
    if(event.key==="Enter"&&rows[selected]){event.preventDefault();if(armed){const button=preview.current?.querySelector<HTMLButtonElement>(armed==="A"?"[data-inbox-primary]":"[data-inbox-secondary]");if(button&&!button.disabled){button.click()}else{setError(t("govActionUnavailable"))}setArmed(null)}else open(rows[selected])}
  }
  const unavailable = page?.capabilities.filter(value => value.status === "UNAVAILABLE") ?? []
  return <div className="flex h-full min-h-0 flex-col focus-visible:outline-2 focus-visible:outline-ring" tabIndex={0} aria-label={t("govInboxTitle")} onKeyDown={onKeyDown}>
    <PageHeader title={t("govInboxTitle")} description={t("govInboxHint")} action={<Button variant="outline" onClick={reload} disabled={loading}>{t("refresh")}</Button>} />
    <div className="mb-3 flex shrink-0 flex-wrap gap-2" role="group" aria-label={t("govKind")}>
      {["", "WORK_ITEM_REVIEW", "INTERACTION", "INTAKE_TRIAGE", "DISPATCH_PROPOSAL", "AGENT_SPAWN_APPROVAL", "ESCALATION_ALERT", "DELIVERY_CONFIRM", "MEMORY_RULE_APPROVAL", "HUMAN_VERIFICATION"].map(value => <Button key={value} variant={kind === value ? "default" : "outline"} aria-pressed={kind === value} disabled={loading} onClick={() => {setLoading(true); setSearch(previous => {const next = new URLSearchParams(previous); next.delete("cursor"); if (value) next.set("kind", value); else next.delete("kind"); return next})}}>{value ? t(`govKind_${value}`) : t("all")}{counts && (value ? counts.by_kind[value as keyof typeof counts.by_kind] : counts.total)!==undefined && <span className="ml-2 tabular-nums">{value ? counts.by_kind[value as keyof typeof counts.by_kind] : counts.total}</span>}</Button>)}
    </div>
    <AutoForm key={`${kind}:${project}`} mode="edit" initialData={{kind: kind || "all", project_id: project}} className="shrink-0 md:grid-cols-2" fields={{
      kind: {name: t("govKind"), type: "select", config: {options: [{value: "all", label: t("all")}, ...inboxKinds.map(value => ({value, label: t(`govKind_${value}`)}))]}},
      project_id: {name: t("govProjectID"), type: "text"},
    }} onSubmit={values => {const next = new URLSearchParams(); const nextKind = String(values.kind ?? "all"); const nextProject = String(values.project_id ?? "").trim(); if (nextKind !== "all") next.set("kind", nextKind); if (nextProject) next.set("project_id", nextProject); setLoading(true); setSearch(next)}}><Button type="submit" disabled={loading}>{t("search")}</Button></AutoForm>
    {error && <Alert>{error}</Alert>}
    {!!unavailable.length && <details className="mb-3 shrink-0 rounded-md border p-3 text-sm"><summary>{t("govMissingSources", {count: unavailable.length})}</summary><p className="mt-2 text-muted-foreground">{t("govMissingHint")}</p><ul className="mt-2 grid gap-1 sm:grid-cols-2">{unavailable.map(value => <li key={value.kind}>{t(`govKind_${value.kind}`)}</li>)}</ul></details>}
    <p className="mb-2 text-sm text-muted-foreground" role="status">{loading ? t("loading") : t("govSelection", {index: page?.items.length ? selected + 1 : 0, count: page?.items.length ?? 0})}</p>
    {armed&&<div className="mb-3 rounded-md border p-3 text-sm" role="alert">{t("govConfirmShortcut")} <Button variant="outline" onClick={()=>setArmed(null)}>{t("cancel")}</Button></div>}
    <div className="grid min-h-0 flex-1 gap-4 overflow-auto lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]"><div className="min-h-64 min-w-0">
    <AutoTable fill data={loading ? [] : page?.items ?? []} rowKey={row => `${row.kind}:${row.id}`} loading={loading} pagination={false} exportable={false} emptyContent={t("govInboxEmpty")} columns={[
      {field: "title", name: t("govSubject"), width: "260px"},
      {field: "kind", name: t("govKind"), width: "210px", format: (_, row) => t(`govKind_${row.kind}`)},
      {field: "priority", name: t("govPriority"), width: "120px", format: (_, row) => t(`govPriority_${row.priority}`)},
      {field: "reason", name: t("govReason"), width: "260px"},
      {field: "created_at", name: t("createdAt"), width: "190px", format: (_, row) => new Date(row.created_at).toLocaleString(locale)},
    ]} actions={[{name: t("govPreview"), icon: Eye, onclick: row=>select(page?.items.findIndex(item=>item.kind===row.kind&&item.id===row.id)??0)}]} />
    </div><div ref={preview} className="min-w-0">{!loading&&page?.items[selected]&&<InboxPreview key={`${page.items[selected].kind}:${page.items[selected].id}`} tenant={tenantID} item={page.items[selected]} onChanged={reload} onOpen={()=>open(page.items[selected])} revision={refresh}/>}</div></div>
    <div className="mt-3 flex shrink-0 justify-between gap-3"><Button variant="outline" disabled={loading || !cursor} onClick={() => {setLoading(true); setSearch(previous => {const next = new URLSearchParams(previous); next.delete("cursor"); return next})}}>{t("govFirstPage")}</Button><Button variant="outline" disabled={loading || !page?.has_more || !page.next_cursor} onClick={() => {if (!page?.next_cursor) return; setLoading(true); setSearch(previous => {const next = new URLSearchParams(previous); next.set("cursor", page.next_cursor!); return next})}}>{t("govNextPage")}</Button></div>
  </div>
}
