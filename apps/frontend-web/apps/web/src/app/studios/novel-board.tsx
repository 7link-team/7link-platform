import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { novelApi, novelEvidenceText, type NovelCitation, type NovelData, type NovelEdit, type NovelWorkbench, type StudioContent, type StudioRecord } from "./api"

const tabs = ["bible", "outline", "chapters", "continuity", "foreshadow"] as const
const blank = (): StudioContent => ({title: "", text: "", source: "", provider: "", model: "", seed: "", duration_ms: 0, novel: {entries: [], characters: [], beats: []}})
export default function NovelBoard({tenant, project}: {tenant: string; project: string}) {
 const t = useTranslations("iam"), allowed = useAllowed(), [params, setParams] = useSearchParams()
 const read = allowed("template_read") && allowed("work_item_read"), manage = read && allowed("project_manage")
 const tab = tabs.find(value => value === params.get("novel_tab")) ?? "bible"
 const [board, setBoard] = useState<NovelWorkbench>({items: [], stale: false, input_sha256: ""})
 const [loading, setLoading] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState(""), [reload, setReload] = useState(0)
 const [content, setContent] = useState<StudioContent>(blank), [kind, setKind] = useState("STORY_BIBLE"), [id, setID] = useState(""), [version, setVersion] = useState(0), [reason, setReason] = useState("")
 const [reader, setReader] = useState<{record: StudioRecord; cite?: NovelCitation} | null>(null)
 const pending = useRef<{fingerprint: string; key: string} | null>(null), mutation = useRef<AbortController | null>(null)
 useEffect(() => () => mutation.current?.abort(), [])
 useEffect(() => {
  if (!read) return
  const abort = new AbortController()
  queueMicrotask(() => {if (!abort.signal.aborted) {setLoading(true); setError("")}})
  novelApi.read(tenant, project, abort.signal).then(value => {if (!abort.signal.aborted) setBoard(value)}).catch(failure => {if (!abort.signal.aborted) setError(String(failure))}).finally(() => {if (!abort.signal.aborted) setLoading(false)})
  return () => abort.abort()
 }, [tenant, project, read, reload])
 const n = content.novel!
 const change = (value: Partial<NovelData>) => setContent(old => ({...old, novel: {...old.novel!, ...value}}))
 const field = (label: string, value: string | number, onChange: (value: string) => void, multiline = false) => <label className="block min-w-0 text-sm">{t(label)}{multiline ? <Textarea value={value} onChange={e => onChange(e.target.value)}/> : <Input value={value} onChange={e => onChange(e.target.value)}/>}</label>
 const start = (record?: StudioRecord, newKind = "STORY_BIBLE") => {
  setID(record?.id ?? ""); setVersion(record?.version ?? 0); setKind(record?.kind ?? newKind)
  const next = record ? structuredClone(record.content) : blank()
  next.novel ??= {entries: [], characters: [], beats: []}
  next.novel.entries ??= []; next.novel.characters ??= []; next.novel.beats ??= []
  if ((record?.kind ?? newKind) === "CHAPTER" && !next.novel.chapter) next.novel.chapter = {number: board.items.filter(row => row.kind === "CHAPTER").length + 1, target_words: 4000, pov: "", plan: "", facts: []}
  if ((record?.kind ?? newKind) === "FORESHADOW" && !next.novel.foreshadow) next.novel.foreshadow = {planted: "", planned: "", resolved: "0", retained: false, reason: ""}
  if (next.novel.chapter) next.novel.chapter.facts ??= []
  setContent(next)
 }
 const run = async (action: "SAVE" | "RUN") => {
  if (!manage || mutation.current) return
  const input: NovelEdit = {action, version: action === "RUN" ? 0 : version, kind, reason, content}
  const recordID = action === "RUN" ? "" : id, fingerprint = JSON.stringify({recordID, input})
  if (pending.current?.fingerprint !== fingerprint) pending.current = {fingerprint, key: crypto.randomUUID()}
  const abort = new AbortController(); mutation.current = abort; setBusy(true); setError("")
  try {await novelApi.edit(tenant, project, recordID, input, pending.current.key, abort.signal); if (!abort.signal.aborted) {pending.current = null; setReload(x => x + 1); if (action === "SAVE") start()}}
  catch (failure) {if (!abort.signal.aborted) setError(String(failure))}
  finally {if (!abort.signal.aborted) {mutation.current = null; setBusy(false)}}
 }
 const report = board.report?.content.novel?.report
 const chapters = board.items.filter(row => row.kind === "CHAPTER").sort((a, b) => (a.content.novel?.chapter?.number ?? 0) - (b.content.novel?.chapter?.number ?? 0))
 const cite = (value: NovelCitation) => {const record = board.items.find(row => row.id === value.record_id); if (record) setReader({record, cite: value})}
 const evidence = reader?.cite ? novelEvidenceText(reader.record, reader.cite) : null
 const selectChapter = (label: string, value: string | null, onChange: (value: string) => void) => <label className="block text-sm">{t(label)}<select className="block w-full rounded-md border bg-background p-2" value={value ?? "0"} onChange={e => onChange(e.target.value)}><option value="0">—</option>{chapters.map(row => <option key={row.id} value={row.id}>{row.content.title}</option>)}</select></label>
 return <div className="flex min-w-0 flex-1 flex-col gap-4 overflow-auto">
  <PageHeader title={t("studioNOVEL")}/>
  {!read ? <p role="status">{t("projectSurfaceAccessUnavailable")}</p> : <>
   <p className="text-sm text-muted-foreground">{t("novelBoundary")}</p>
   {error && <p role="alert">{error}</p>}{loading && <p role="status">{t("loading")}</p>}
   <div role="tablist" aria-label={t("studioNOVEL")} className="flex flex-wrap gap-2">{tabs.map((value, index) => <button key={value} id={`novel-tab-${value}`} role="tab" aria-selected={tab === value} aria-controls="novel-panel" tabIndex={tab === value ? 0 : -1} className="min-h-11 rounded-md border px-3 text-sm aria-selected:bg-primary aria-selected:text-primary-foreground" onClick={() => setParams(old => {old.set("novel_tab", value); return old})} onKeyDown={event => {
    const next = event.key === "ArrowRight" ? (index + 1) % tabs.length : event.key === "ArrowLeft" ? (index + tabs.length - 1) % tabs.length : event.key === "Home" ? 0 : event.key === "End" ? tabs.length - 1 : -1
    if (next >= 0) {event.preventDefault(); setParams(old => {old.set("novel_tab", tabs[next]!); return old}); document.getElementById(`novel-tab-${tabs[next]}`)?.focus()}
   }}>{t(`novelTab_${value}`)}</button>)}</div>
   <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={busy || loading} onClick={() => setReload(x => x + 1)}>{t("templateRefresh")}</Button>{manage && ["STORY_BIBLE", "CHAPTER", "FORESHADOW"].map(value => <Button key={value} variant="outline" disabled={busy} onClick={() => start(undefined, value)}>{t(`novelNew_${value}`)}</Button>)}</div>
   <section role="tabpanel" id="novel-panel" aria-labelledby={`novel-tab-${tab}`} className="min-w-0 space-y-4">
    {!loading && !board.items.length && <p>{t("studioEmpty")}</p>}
    {tab === "bible" && board.items.filter(row => row.kind === "STORY_BIBLE").map(row => <article key={row.id} className="space-y-4 rounded-md border p-4"><h2>{row.content.title}</h2>{row.content.novel?.entries?.map(entry => <section key={entry.key}><h3 className="font-medium">{entry.key} · {t(`novelCategory_${entry.category}`)}</h3><p className="whitespace-pre-wrap break-words">{entry.text}</p><p className="text-sm">{entry.entity} / {entry.attribute}: {entry.value} {entry.invariant && t("novelInvariant")}</p><p className="text-xs text-muted-foreground">{t("novelMentions")}: {chapters.filter(ch => ch.content.text.includes(entry.entity)).map(ch => ch.content.title).join(" / ") || "—"}</p></section>)}{row.content.novel?.characters?.map(ch => <section key={ch.name}><h3 className="font-medium">{ch.name}</h3><p>{ch.motivation}</p><p>{ch.arc}</p></section>)}{manage && <Button variant="outline" onClick={() => start(row)}>{t("novelEdit")}</Button>}</article>)}
    {tab === "outline" && board.items.filter(row => row.kind === "STORY_BIBLE").map(row => <article key={row.id} className="space-y-3"><h2>{row.content.title}</h2><p className="whitespace-pre-wrap">{row.content.text}</p>{row.content.novel?.beats?.map((beat, i) => {
     const coverage = report?.coverage ?? [], total = coverage.reduce((sum, c) => sum + c.words, 0), index = coverage.findIndex(c => c.id === beat.chapter_id), actual = total && index >= 0 ? coverage.slice(0, index + 1).reduce((sum, c) => sum + c.words, 0) * 100 / total : null
     return <p key={i}>{beat.name} · {beat.target_percent}% → {actual === null ? "—" : `${actual.toFixed(1)}%`} {actual !== null && Math.abs(actual - beat.target_percent) > 3 && t("novelBeatDeviation")}{board.stale && t("novelStale")}</p>
    })}{manage && <Button variant="outline" onClick={() => start(row)}>{t("novelEdit")}</Button>}</article>)}
    {tab === "chapters" && chapters.map(row => <article key={row.id} className="space-y-2 rounded-md border p-4"><h2>{row.content.novel?.chapter?.number ?? "—"} · {row.content.title}</h2><p>{t("novelTarget")}: {row.content.novel?.chapter?.target_words ?? "—"} · {t("novelWords")}: {report?.coverage.find(c => c.id === row.id)?.words ?? "—"}</p><p>{row.content.novel?.chapter?.pov} · {row.content.novel?.chapter?.plan}</p><ul>{row.content.novel?.chapter?.facts?.map((f, i) => <li key={i}>{f.tick} · {f.entity} / {f.attribute}: {f.value} · <q>{f.quote}</q></li>)}</ul><div className="flex gap-2"><Button variant="outline" onClick={() => setReader({record: row})}>{t("novelRead")}</Button>{manage && <Button variant="outline" onClick={() => start(row)}>{t("novelEdit")}</Button>}</div></article>)}
    {tab === "continuity" && <>
     <p>{t("novelCoverage")}: {report?.coverage.length ?? 0} / {chapters.length} · {t("novelWords")}: {report?.words ?? 0} · CED: {report?.ced ?? "—"}</p>
     <p>{t("novelSemanticUnavailable")}</p>{board.stale && <p role="status">{t("novelStale")}</p>}
     {report && <><p className="break-all text-xs">{report.engine} · {report.at} · {report.input_sha256}</p><p>{t("novelIncomplete")}: {report.incomplete.join(", ") || "—"} · {t("novelUnclosed")}: {report.open_foreshadows.join(", ") || "—"}</p><ul>{report.coverage.map(c => <li key={c.id} className="break-all text-xs">{c.id} · v{c.version} · {c.words} · {c.sha256}</li>)}</ul>{report.issues.map((issue, i) => <article key={i} className="space-y-2 rounded-md border p-4"><h3>{t(`novelIssue_${issue.code}`)}</h3>{[issue.a, issue.b].map((c, j) => <div key={j}><p className="whitespace-pre-wrap break-words"><q>{c.quote}</q></p><Button variant="outline" onClick={() => cite(c)}>{t("novelRead")} · {c.record_id} v{c.version}</Button></div>)}</article>)}</>}
     {manage && <Button disabled={busy || !reason.trim() || !board.items.some(row => row.kind === "STORY_BIBLE")} onClick={() => void run("RUN")}>{t("novelRun")}</Button>}
    </>}
    {tab === "foreshadow" && board.items.filter(row => row.kind === "FORESHADOW").map(row => <article key={row.id} className="space-y-2 rounded-md border p-4"><h2>{row.content.title}</h2><p>{row.content.text}</p>{row.content.novel?.foreshadow ? <><p>{t("novelPlanted")}: {row.content.novel.foreshadow.planted} · {t("novelPlanned")}: {row.content.novel.foreshadow.planned} · {t("novelResolved")}: {row.content.novel.foreshadow.resolved}</p><p>{row.content.novel.foreshadow.retained ? t("novelRetained") : t("novelClosureRequired")} {row.content.novel.foreshadow.reason}</p></> : <p>{t("novelIncomplete")}</p>}{manage && <Button variant="outline" onClick={() => start(row, "FORESHADOW")}>{t("novelEdit")}</Button>}</article>)}
   </section>
   {reader && <section aria-label={t("novelRead")} className="space-y-3 border p-4"><h2>{reader.record.content.title} · v{reader.record.version}</h2>{reader.cite && !evidence && <><p role="status">{t("novelStaleEvidence")}</p><q>{reader.cite.quote}</q></>}<p className="whitespace-pre-wrap break-words">{evidence ? <>{evidence.before}<mark>{evidence.quote}</mark>{evidence.after}</> : reader.record.content.text}</p><Button variant="outline" onClick={() => setReader(null)}>{t("novelClose")}</Button></section>}
   {manage && <section className="min-w-0 space-y-3 border-t pt-4" aria-label={t("novelEditor")}><h2 className="font-medium">{t("novelEditor")} · {t(`novelNew_${kind}`)} {id && `#${id} v${version}`}</h2>
    {field("studioTitle", content.title, title => setContent(old => ({...old, title})))}
    {field("studioText", content.text, text => setContent(old => ({...old, text})), true)}
    {kind === "STORY_BIBLE" && <>
     {n.entries.map((entry, i) => <fieldset key={i} className="grid min-w-0 gap-2 border p-3 sm:grid-cols-2"><legend>{t("novelEntry")} {i + 1}</legend>{(["key", "entity", "attribute", "value", "text"] as const).map(key => <div key={key}>{field(`novelField_${key}`, entry[key], value => change({entries: n.entries.map((e, j) => j === i ? {...e, [key]: value} : e)}), key === "text")}</div>)}<label>{t("novelCategory")}<select className="block rounded-md border bg-background p-2" value={entry.category} onChange={e => change({entries: n.entries.map((x, j) => j === i ? {...x, category: e.target.value as typeof entry.category} : x)})}>{["WORLD", "LOCATION", "TERM"].map(c => <option key={c} value={c}>{t(`novelCategory_${c}`)}</option>)}</select></label><label className="flex min-h-11 items-center gap-2"><input type="checkbox" checked={entry.invariant} onChange={e => change({entries: n.entries.map((x, j) => j === i ? {...x, invariant: e.target.checked} : x)})}/>{t("novelInvariant")}</label><Button variant="outline" onClick={() => change({entries: n.entries.filter((_, j) => j !== i)})}>{t("novelRemove")}</Button></fieldset>)}
     <Button variant="outline" onClick={() => change({entries: [...n.entries, {key: "", entity: "", attribute: "", value: "", text: "", invariant: false, category: "WORLD"}]})}>{t("novelEntry")}</Button>
     {n.characters.map((ch, i) => <fieldset key={i} className="space-y-2 border p-3"><legend>{t("novelCharacter")}</legend>{(["name", "motivation", "arc"] as const).map(key => <div key={key}>{field(`novelField_${key}`, ch[key], value => change({characters: n.characters.map((x, j) => j === i ? {...x, [key]: value} : x)}))}</div>)}<Button variant="outline" onClick={() => change({characters: n.characters.filter((_, j) => j !== i)})}>{t("novelRemove")}</Button></fieldset>)}
     <Button variant="outline" onClick={() => change({characters: [...n.characters, {name: "", motivation: "", arc: ""}]})}>{t("novelCharacter")}</Button>
     {n.beats.map((b, i) => <fieldset key={i} className="space-y-2 border p-3"><legend>{t("novelBeat")}</legend>{field("novelField_name", b.name, name => change({beats: n.beats.map((x, j) => j === i ? {...x, name} : x)}))}{field("novelTargetPercent", b.target_percent, value => change({beats: n.beats.map((x, j) => j === i ? {...x, target_percent: Number(value)} : x)}))}{selectChapter("novelChapter", b.chapter_id, chapter_id => change({beats: n.beats.map((x, j) => j === i ? {...x, chapter_id} : x)}))}<Button variant="outline" onClick={() => change({beats: n.beats.filter((_, j) => j !== i)})}>{t("novelRemove")}</Button></fieldset>)}
     <Button variant="outline" onClick={() => change({beats: [...n.beats, {name: "", target_percent: 50, chapter_id: "0"}]})}>{t("novelBeat")}</Button>
    </>}
    {kind === "CHAPTER" && n.chapter && <>
     {(["number", "target_words", "pov", "plan"] as const).map(key => <div key={key}>{field(`novelField_${key}`, n.chapter![key], value => change({chapter: {...n.chapter!, [key]: ["number", "target_words"].includes(key) ? Number(value) : value}}))}</div>)}
     <p className="text-sm text-muted-foreground">{t("novelFactHint")}</p>
     {n.chapter.facts.map((f, i) => <fieldset key={i} className="grid gap-2 border p-3 sm:grid-cols-2"><legend>{t("novelFact")} {i + 1}</legend>{(["entity", "attribute", "value", "tick", "start", "quote"] as const).map(key => <div key={key}>{field(`novelField_${key}`, f[key], value => change({chapter: {...n.chapter!, facts: n.chapter!.facts.map((x, j) => j === i ? {...x, [key]: ["tick", "start"].includes(key) ? Number(value) : value} : x)}}))}</div>)}<Button variant="outline" onClick={() => change({chapter: {...n.chapter!, facts: n.chapter!.facts.filter((_, j) => j !== i)}})}>{t("novelRemove")}</Button></fieldset>)}
     <Button variant="outline" onClick={() => change({chapter: {...n.chapter!, facts: [...n.chapter!.facts, {entity: "", attribute: "", value: "", tick: 0, start: 0, quote: ""}]}})}>{t("novelFact")}</Button>
    </>}
    {kind === "FORESHADOW" && n.foreshadow && <>{(["planted", "planned", "resolved"] as const).map(key => <div key={key}>{selectChapter(`novel${key[0]!.toUpperCase()}${key.slice(1)}`, n.foreshadow![key], value => change({foreshadow: {...n.foreshadow!, [key]: value}}))}</div>)}<label className="flex min-h-11 items-center gap-2"><input type="checkbox" checked={n.foreshadow.retained} onChange={e => change({foreshadow: {...n.foreshadow!, retained: e.target.checked}})}/>{t("novelRetained")}</label>{field("templateReason", n.foreshadow.reason, value => change({foreshadow: {...n.foreshadow!, reason: value}}))}</>}
    {field("templateReason", reason, setReason, true)}
    <Button disabled={busy || !content.title.trim() || !reason.trim()} onClick={() => void run("SAVE")}>{t("novelSave")}</Button>
   </section>}
  </>}
 </div>
}
