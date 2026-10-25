import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { videoApi, type StudioContent, type StudioRecord, type VideoEdit, type VideoWorkbench } from "./api"

const tabs = ["shots", "references", "qc", "master", "providers"] as const
const blank = (): StudioContent => ({title: "", text: "", source: "", provider: "", model: "", seed: "", duration_ms: 3000, video: {role: "SHOT", character: "", framing: "", execution_id: "0", generation_id: "0", evidence_id: "0", output_index: 0}})
function Media({tenant, project, record}: {tenant: string; project: string; record: StudioRecord}) {
 const t = useTranslations("iam"), [url, setURL] = useState(""), [type, setType] = useState(""), [error, setError] = useState("")
 useEffect(() => {
  const abort = new AbortController(); let objectURL = ""
  videoApi.content(tenant, project, record.id, abort.signal).then(blob => {if (!abort.signal.aborted) {objectURL = URL.createObjectURL(blob); setType(blob.type); setURL(objectURL)}}).catch(e => {if (!abort.signal.aborted) setError(String(e))})
  return () => {abort.abort(); if (objectURL) URL.revokeObjectURL(objectURL)}
 }, [tenant, project, record.id, record.version])
 return <section aria-label={t("videoMedia")} className="space-y-2">{error && <p role="alert">{error}</p>}{!url && !error && <p role="status">{t("loading")}</p>}{url && (type.startsWith("image/") ? <img src={url} alt={record.content.title} className="max-h-96 max-w-full object-contain"/> : type.startsWith("audio/") ? <audio controls src={url} aria-label={record.content.title}/> : <video controls preload="metadata" src={url} aria-label={record.content.title} className="max-h-96 max-w-full"/>)}<p className="break-all text-xs">{record.content.video?.artifact?.sha256}</p><p className="text-xs text-muted-foreground">{t("videoMediaBoundary")}</p></section>
}
export default function VideoBoard({tenant, project}: {tenant: string; project: string}) {
 const t = useTranslations("iam"), allowed = useAllowed(), [params, setParams] = useSearchParams()
 const read = allowed("template_read") && allowed("work_item_read"), manage = read && allowed("project_manage")
 const tab = tabs.find(value => value === params.get("video_tab")) ?? "shots"
 const [board, setBoard] = useState<VideoWorkbench>({items: [], executions: [], generations: [], providers: [], bindings: {version: 0, providers: []}, availability: "UNAVAILABLE"})
 const [content, setContent] = useState<StudioContent>(blank), [id, setID] = useState(""), [version, setVersion] = useState(0), [parent, setParent] = useState("0"), [reason, setReason] = useState(""), [provider, setProvider] = useState("")
 const [error, setError] = useState(""), [busy, setBusy] = useState(false), [loading, setLoading] = useState(false), [reload, setReload] = useState(0), [media, setMedia] = useState<StudioRecord | null>(null)
 const pending = useRef<{fingerprint: string; key: string} | null>(null), mutation = useRef<AbortController | null>(null)
 useEffect(() => () => mutation.current?.abort(), [])
 useEffect(() => {
  if (!read) return
  const abort = new AbortController(); queueMicrotask(() => {if (!abort.signal.aborted) {setLoading(true); setError("")}})
  videoApi.read(tenant, project, abort.signal).then(value => {if (!abort.signal.aborted) setBoard(value)}).catch(failure => {if (!abort.signal.aborted) setError(String(failure))}).finally(() => {if (!abort.signal.aborted) setLoading(false)})
  return () => abort.abort()
 }, [tenant, project, read, reload])
 const run = async (fingerprint: string, action: (key: string, signal: AbortSignal) => Promise<unknown>) => {
  if (!manage || mutation.current) return
  if (pending.current?.fingerprint !== fingerprint) pending.current = {fingerprint, key: crypto.randomUUID()}
  const abort = new AbortController(); mutation.current = abort; setBusy(true); setError("")
  try {await action(pending.current.key, abort.signal); if (!abort.signal.aborted) {pending.current = null; setReload(x => x + 1)}} catch (failure) {if (!abort.signal.aborted) setError(String(failure))} finally {if (!abort.signal.aborted) {mutation.current = null; setBusy(false)}}
 }
 const send = (action: VideoEdit["action"], record?: StudioRecord) => {
  const input: VideoEdit = {action, version: record?.version ?? version, parent_id: parent, reason, content: record?.content ?? content}
  // Stored server provenance is never accepted as client-authored input.
  if (action !== "SELECT" && input.content.video) input.content = {...input.content, video: {...input.content.video, artifact: undefined, generation: undefined}}
  const recordID = record?.id ?? id
  void run(JSON.stringify({recordID, input}), (key, signal) => videoApi.edit(tenant, project, recordID, input, key, signal))
 }
 const change = (key: string, value: string | number) => setContent(old => ({...old, video: {...old.video!, [key]: value}}))
 const field = (label: string, value: string | number | null, onChange: (value: string) => void, multiline = false) => <label className="block text-sm">{t(label)}{multiline ? <Textarea value={value ?? ""} onChange={e => onChange(e.target.value)}/> : <Input value={value ?? ""} onChange={e => onChange(e.target.value)}/>}</label>
 const edit = (record?: StudioRecord) => {setID(record?.id ?? ""); setVersion(record?.version ?? 0); const next = record ? structuredClone(record.content) : blank(); next.video ??= blank().video; setContent(next)}
 const shots = board.items.filter(row => row.kind === "SHOT" && (!row.content.video || row.content.video.role === "SHOT"))
 const card = (record: StudioRecord) => <article key={record.id} className="min-w-0 space-y-2 rounded-md border p-4"><h3 className="font-medium">{record.content.title} · v{record.version}</h3><p className="whitespace-pre-wrap break-words">{record.content.text}</p><p>{record.content.video?.character} · {record.content.video?.framing} · {record.content.duration_ms} ms</p><p className="break-all text-xs">{record.id} · {record.content.video?.artifact?.file_id} · {record.content.video?.artifact?.version_id}</p>{record.content.video?.generation ? <p className="break-all text-xs">{record.content.video.generation.adapter_key} · v{record.content.video.generation.provider_version} · {record.content.model} · {record.content.seed} · {record.content.video.generation.request_sha256} · {record.content.video.generation.output.reported_cost} {record.content.video.generation.output.currency}</p> : <p className="text-xs text-muted-foreground">{t("videoReported")}</p>}<div className="flex flex-wrap gap-2">{record.content.video?.artifact && <Button variant="outline" onClick={() => setMedia(record)}>{t("videoPlay")}</Button>}{manage && record.kind === "SHOT" && <Button variant="outline" onClick={() => edit(record)}>{t("novelEdit")}</Button>}{manage && record.kind === "CANDIDATE" && <Button disabled={busy || !reason.trim()} onClick={() => send("SELECT", record)}>{t("studioSelect")}</Button>}</div></article>
 return <div className="flex min-w-0 flex-1 flex-col gap-4 overflow-auto"><PageHeader title={t("studioVIDEO")}/>
  {!read ? <p role="status">{t("projectSurfaceAccessUnavailable")}</p> : <>
   <p className="text-sm text-muted-foreground">{t("videoBoundary")}</p>{error && <p role="alert">{error}</p>}{loading && <p role="status">{t("loading")}</p>}
   <div role="tablist" aria-label={t("studioVIDEO")} className="flex flex-wrap gap-2">{tabs.map((value, index) => <button key={value} id={`video-tab-${value}`} role="tab" aria-selected={tab === value} aria-controls="video-panel" tabIndex={tab === value ? 0 : -1} className="min-h-11 rounded-md border px-3 text-sm aria-selected:bg-primary aria-selected:text-primary-foreground" onClick={() => setParams(old => {old.set("video_tab", value); return old})} onKeyDown={event => {const next = event.key === "ArrowRight" ? (index + 1) % tabs.length : event.key === "ArrowLeft" ? (index + tabs.length - 1) % tabs.length : event.key === "Home" ? 0 : event.key === "End" ? tabs.length - 1 : -1; if (next >= 0) {event.preventDefault(); setParams(old => {old.set("video_tab", tabs[next]!); return old}); document.getElementById(`video-tab-${tabs[next]}`)?.focus()}}}>{t(`videoTab_${value}`)}</button>)}</div>
   <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={busy || loading} onClick={() => setReload(x => x + 1)}>{t("templateRefresh")}</Button>{manage && <Button variant="outline" disabled={busy} onClick={() => edit()}>{t("videoNew")}</Button>}</div>
   <section role="tabpanel" id="video-panel" aria-labelledby={`video-tab-${tab}`} className="min-w-0 space-y-4">
    {tab === "shots" && <>{!shots.length && <p>{t("studioEmpty")}</p>}{shots.map(shot => <section key={shot.id} className="space-y-3">{card(shot)}<p>{t("studioSelected")}: {shot.selected_id ?? "—"}</p><div className="grid min-w-0 gap-3 lg:grid-cols-2">{board.items.filter(row => row.kind === "CANDIDATE" && row.parent_id === shot.id).map(card)}</div></section>)}<h2>{t("videoJobs")}</h2>{board.generations.map(g => <article key={g.id} className="space-y-2 rounded-md border p-3"><p>{g.id} · {g.state} · {g.availability} · {g.adapter_key} v{g.provider_version}</p><p className="break-all text-xs">{g.request_sha256} · {g.output_sha256}</p>{g.output.outputs?.map((output, i) => <p key={i} className="break-all text-sm">{i} · {output.file_ref} · {output.model} / {output.model_version} · {output.sha256}</p>)}</article>)}</>}
    {tab === "references" && <>{board.items.filter(row => row.content.video?.role === "REFERENCE").map(card)}<p>{t("videoReferenceHint")}</p></>}
    {tab === "master" && <>{board.items.filter(row => row.content.video?.role === "MASTER").map(card)}<p>{t("videoMasterHint")}</p></>}
    {tab === "qc" && <><p>{t("videoQCNotice")}</p>{!board.executions.some(e => e.qc.length) && <p role="status">{t("videoQCUnavailable")}</p>}{board.executions.map(execution => <article key={execution.execution_id} className="space-y-2 border p-4"><h2>{t("videoExecution")} {execution.execution_id} · {execution.state}</h2>{execution.qc.map(qc => <section key={qc.task_id} className="space-y-1 break-all text-sm"><h3>{qc.verifier} · {qc.verdict}</h3><p>{qc.method} · {qc.stage} · {qc.reason}</p><p>{t("videoResult")}: {qc.result_id} v{qc.result_version} · {qc.head_commit}</p><p>{qc.task_id} · {qc.receipt_sha256}</p></section>)}</article>)}</>}
    {tab === "providers" && <><p role="status">{t("videoAvailability")}: {board.availability}</p><p>{t("videoProviderHint")}</p>{board.providers.map(p => <article key={p.id} className="space-y-2 border p-4"><h2>{p.display_name} · {p.adapter_key} · v{p.version}</h2><p>{p.state} · {p.health_checked_at ?? "—"}</p><p>{p.limitations}</p><p>{p.license_notice}</p><p>{p.region_notice}</p></article>)}{manage && <><label className="block text-sm">{t("videoProvider")}<select className="block w-full rounded-md border bg-background p-2" value={provider} onChange={e => setProvider(e.target.value)}><option value="">—</option>{board.providers.filter(p => p.state === "ENABLED").map(p => <option key={p.id} value={p.id}>{p.display_name} · v{p.version}</option>)}</select></label><Button disabled={busy || !provider} onClick={() => void run(JSON.stringify({provider, version: board.bindings.version}), (key, signal) => videoApi.bind(tenant, project, board.bindings.version, provider, key, signal))}>{t("videoBind")}</Button></>}<ul>{board.bindings.providers.map(p => <li key={p.kind}>{p.kind}: {p.provider_id} v{p.provider_version}</li>)}</ul></>}
   </section>
   {media && <section className="space-y-3 border p-4"><h2>{media.content.title}</h2><Media key={`${media.id}:${media.version}`} tenant={tenant} project={project} record={media}/><Button variant="outline" onClick={() => setMedia(null)}>{t("novelClose")}</Button></section>}
   {manage && <section aria-label={t("videoEditor")} className="space-y-3 border-t pt-4"><h2>{t("videoEditor")} {id && `#${id} v${version}`}</h2>
    {field("studioTitle", content.title, title => setContent(old => ({...old, title})))}{field("studioText", content.text, text => setContent(old => ({...old, text})), true)}
    <label className="block text-sm">{t("videoRole")}<select className="block w-full rounded-md border bg-background p-2" value={content.video!.role} onChange={e => change("role", e.target.value)}>{["SHOT", "REFERENCE", "MASTER"].map(value => <option key={value} value={value}>{t(`videoRole_${value}`)}</option>)}</select></label>
    {field("videoCharacter", content.video!.character, value => change("character", value))}{field("videoFraming", content.video!.framing, value => change("framing", value))}{field("studioDuration", content.duration_ms, value => setContent(old => ({...old, duration_ms: Number(value)})))}{field("studioFieldseed", content.seed, seed => setContent(old => ({...old, seed})))}
    {field("videoExecution", content.video!.execution_id, value => change("execution_id", value))}{field("videoEvidence", content.video!.evidence_id, value => change("evidence_id", value))}
    <label className="block text-sm">{t("studioParent")}<select className="block w-full rounded-md border bg-background p-2" value={parent} onChange={e => setParent(e.target.value)}><option value="0">—</option>{shots.map(row => <option key={row.id} value={row.id}>{row.content.title}</option>)}</select></label>
    {field("videoGeneration", content.video!.generation_id, value => change("generation_id", value))}{field("videoOutput", content.video!.output_index, value => change("output_index", Number(value)))}{field("templateReason", reason, setReason, true)}
    <div className="flex flex-wrap gap-2"><Button disabled={busy || !reason.trim() || !content.title.trim()} onClick={() => send("SAVE")}>{t("novelSave")}</Button><Button variant="outline" disabled={busy || !!id || !reason.trim() || parent === "0" || !content.text.trim() || content.video!.execution_id === "0" || board.availability === "UNAVAILABLE"} onClick={() => send("GENERATE")}>{t("videoGenerate")}</Button><Button variant="outline" disabled={busy || !!id || !reason.trim() || parent === "0" || content.video!.generation_id === "0"} onClick={() => send("IMPORT")}>{t("videoImport")}</Button></div>
   </section>}
  </>}
 </div>
}
