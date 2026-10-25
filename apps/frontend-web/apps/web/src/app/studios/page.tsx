import { useEffect, useRef, useState } from "react"
import { useParams } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { canConfirm, studioSource, studiosApi, type StudioContent, type StudioDomain, type StudioDraft, type StudioRecord } from "./api"
import TeamBoard from "./team-board"
import NovelBoard from "./novel-board"
import VideoBoard from "./video-board"

const kinds = {VIDEO: ["SHOT", "CANDIDATE", "QC_NOTE"], NOVEL: ["CHAPTER", "CONTINUITY", "FORESHADOW"], TEAM_TASKS: ["ASSISTANT_DRAFT"]} as const
const blank = (): StudioContent => ({title: "", text: "", source: "", duration_ms: 0, provider: "", model: "", seed: ""})

export default function StudioPage({ domain }: {domain: StudioDomain}) {
 const {tenantID = "", entityID = ""} = useParams()
 if (domain === "VIDEO") return <VideoBoard key={`${tenantID}:${entityID}`} tenant={tenantID} project={entityID}/>
 if (domain === "NOVEL") return <NovelBoard key={`${tenantID}:${entityID}`} tenant={tenantID} project={entityID}/>
 if (domain === "TEAM_TASKS") return <TeamBoard key={`${tenantID}:${entityID}`} tenant={tenantID} project={entityID}/>
 return <StudioContentPage key={`${tenantID}:${entityID}:${domain}`} domain={domain} tenant={tenantID} project={entityID}/>
}
function StudioContentPage({domain, tenant, project}: {domain: StudioDomain; tenant: string; project: string}) {
 const t = useTranslations("iam")
 const allowed = useAllowed()
 const read = allowed("work_item_read") && allowed("template_read")
 const manage = read && allowed("project_manage")
 const [rows, setRows] = useState<StudioRecord[]>([])
 const [next, setNext] = useState("")
 const [cursor, setCursor] = useState("")
 const [reload, setReload] = useState(0)
 const [loading, setLoading] = useState(false)
 const [busy, setBusy] = useState(false)
 const [error, setError] = useState("")
 const [kind, setKind] = useState<string>(kinds[domain][0])
 const [parent, setParent] = useState("")
 const [content, setContent] = useState<StudioContent>(blank)
 const [reason, setReason] = useState("")
 const pending = useRef<{fingerprint: string; key: string} | null>(null)
 const mutation = useRef<AbortController | null>(null)
 useEffect(() => () => mutation.current?.abort(), [])
 useEffect(() => {
  if (!read) return
  const abort = new AbortController()
  queueMicrotask(() => {if (!abort.signal.aborted) {setLoading(true);setError("")}})
  studiosApi.list(tenant, project, domain, cursor, abort.signal).then(page => {if (!abort.signal.aborted) {setRows(page.items);setNext(page.next_cursor ?? "")}}).catch(failure => {if (!abort.signal.aborted) setError(String(failure))}).finally(() => {if (!abort.signal.aborted) setLoading(false)})
  return () => abort.abort()
 }, [tenant, project, domain, cursor, reload, read])
 const change = (field: keyof StudioContent, value: string | number) => setContent(old => ({...old, [field]: value}))
 const run = async (fingerprint: string, action: (key: string, signal: AbortSignal) => Promise<unknown>) => {
  if (mutation.current || !manage) return
  if (pending.current?.fingerprint !== fingerprint) pending.current = {fingerprint, key: crypto.randomUUID()}
  const abort = new AbortController();mutation.current = abort;setBusy(true);setError("")
  try {await action(pending.current.key, abort.signal);if (!abort.signal.aborted) {pending.current = null;setReload(value => value + 1)}} catch (failure) {if (!abort.signal.aborted) setError(String(failure))} finally {if (!abort.signal.aborted) {mutation.current = null;setBusy(false)}}
 }
 const draft: StudioDraft = {domain, kind, ...(parent ? {parent_id: parent} : {}), content}
 const needsParent = ["CANDIDATE", "QC_NOTE", "CONTINUITY"].includes(kind)
 const decision = (record: StudioRecord, action: string) => void run(JSON.stringify({id: record.id, version: record.version, action, reason}), (key, signal) => studiosApi.decide(tenant, project, record, action, reason, key, signal))
 return <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-4 overflow-auto">
  <PageHeader title={t(`studio${domain}`)}/>
  {!read ? <p role="status">{t("projectSurfaceAccessUnavailable")}</p> : <>
   <p className="text-sm text-muted-foreground">{t("studioNotice")}</p>
   {error && <p role="alert" className="rounded-md border p-3">{error}</p>}
   <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={busy || loading} onClick={() => setReload(value => value + 1)}>{t("templateRefresh")}</Button>{cursor && <Button variant="outline" disabled={busy || loading} onClick={() => setCursor("")}>{t("templateBack")}</Button>}<Button variant="outline" disabled={!next || busy || loading} onClick={() => setCursor(next)}>{t("templateNext")}</Button></div>
   {loading && <p role="status">{t("loading")}</p>}
   {!loading && !error && !rows.length && <p>{t("studioEmpty")}</p>}
   <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,2fr)_minmax(20rem,1fr)]">
    <section className="min-w-0 space-y-3" aria-label={t("studioRecords")}>{rows.map(record => <article key={record.id} className="space-y-2 rounded-md border p-4">
     <div className="flex flex-wrap justify-between gap-2"><h2 className="font-medium">{record.content.title}</h2><span className="text-xs text-muted-foreground">{t(`studioKind${record.kind}`)} / {t(`studioState${record.state}`)}</span></div>
     <p className="whitespace-pre-wrap break-words text-sm">{record.content.text}</p>
     <dl className="flex flex-wrap gap-x-5 gap-y-1 break-all text-xs text-muted-foreground"><div><dt>{t("studioID")}</dt><dd>{record.id}</dd></div>{record.parent_id && <div><dt>{t("studioParent")}</dt><dd>{record.parent_id}</dd></div>}{record.selected_id && <div><dt>{t("studioSelected")}</dt><dd>{record.selected_id}</dd></div>}{!!record.content.duration_ms && <div><dt>{t("studioDuration")}</dt><dd>{record.content.duration_ms}</dd></div>}{record.content.owner_id && <div><dt>{t("studioOwner")}</dt><dd>{record.content.owner_id}</dd></div>}{record.content.due_at && <div><dt>{t("studioDue")}</dt><dd>{record.content.due_at}</dd></div>}</dl>
     {record.content.source && <p className="break-all text-sm">{studioSource(record.content.source) ? <a className="underline" href={studioSource(record.content.source)!} target="_blank" rel="noopener noreferrer">{t("studioSource")}</a> : record.content.source}</p>}
     {record.kind === "CANDIDATE" && <p className="text-xs">{[record.content.provider, record.content.model, record.content.seed].filter(Boolean).join(" / ")}</p>}
     {record.kind === "QC_NOTE" && <p className="text-xs text-muted-foreground">{t("studioAdvisory")}</p>}
     {manage && record.state === "OPEN" && <div className="flex flex-wrap gap-2">
      {record.kind === "CANDIDATE" && <Button disabled={busy || !reason.trim()} onClick={() => decision(record, "SELECT")}>{t("studioSelect")}</Button>}
      {record.kind === "ASSISTANT_DRAFT" && <><Button disabled={busy || !reason.trim() || !canConfirm(record)} onClick={() => decision(record, "CONFIRM")}>{t("studioConfirm")}</Button><Button variant="outline" disabled={busy || !reason.trim()} onClick={() => decision(record, "REJECT")}>{t("studioReject")}</Button>{!canConfirm(record) && <p className="text-xs">{t("studioMissingOwnerDue")}</p>}</>}
      {["CONTINUITY", "FORESHADOW"].includes(record.kind) && <Button disabled={busy || !reason.trim()} onClick={() => decision(record, "RESOLVE")}>{t("studioResolve")}</Button>}
     </div>}
    </article>)}</section>
    {manage && <section className="space-y-3 border-t pt-4 xl:border-l xl:border-t-0 xl:pl-5 xl:pt-0"><h2 className="font-medium">{t("studioCreate")}</h2>
     <label className="block text-sm">{t("studioKind")}<select className="mt-1 block w-full rounded-md border bg-background p-2" value={kind} onChange={event => {setKind(event.target.value);setParent("")}}>{kinds[domain].map(value => <option key={value} value={value}>{t(`studioKind${value}`)}</option>)}</select></label>
     <label className="block text-sm">{t("studioTitle")}<Input value={content.title} maxLength={200} onChange={event => change("title", event.target.value)}/></label>
     <label className="block text-sm">{t("studioText")}<Textarea value={content.text} maxLength={20000} onChange={event => change("text", event.target.value)}/></label>
     {(["CANDIDATE", "QC_NOTE", "CONTINUITY", "FORESHADOW"].includes(kind)) && <label className="block text-sm">{t("studioParent")}<Input value={parent} inputMode="numeric" onChange={event => setParent(event.target.value)}/></label>}
     <label className="block text-sm">{t("studioSource")}<Input value={content.source} maxLength={2000} onChange={event => change("source", event.target.value)}/></label>
     {kind === "SHOT" && <label className="block text-sm">{t("studioDuration")}<Input type="number" min={0} max={86400000} value={content.duration_ms} onChange={event => change("duration_ms", Number(event.target.value))}/></label>}
     {kind === "CANDIDATE" && (["provider", "model", "seed"] as const).map(field => <label key={field} className="block text-sm">{t(`studioField${field}`)}<Input value={content[field]} maxLength={200} onChange={event => change(field, event.target.value)}/></label>)}
     {kind === "ASSISTANT_DRAFT" && <><label className="block text-sm">{t("studioOwner")}<Input value={content.owner_id ?? ""} inputMode="numeric" onChange={event => setContent(old => ({...old, owner_id: event.target.value || undefined}))}/></label><label className="block text-sm">{t("studioDue")}<Input type="datetime-local" onChange={event => setContent(old => ({...old, due_at: event.target.value ? new Date(event.target.value).toISOString() : undefined}))}/></label></>}
     <Button disabled={busy || !content.title.trim() || (needsParent && !/^[1-9][0-9]*$/.test(parent))} onClick={() => void run(JSON.stringify(draft), (key, signal) => studiosApi.create(tenant, project, draft, key, signal))}>{t("studioCreate")}</Button>
     <label className="block border-t pt-3 text-sm">{t("templateReason")}<Textarea value={reason} maxLength={2000} onChange={event => setReason(event.target.value)}/></label>
    </section>}
   </div>
  </>}
 </div>
}
