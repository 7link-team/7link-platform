import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@workspace/ui/components/tabs"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { teamCanSave, teamCanSign, studioSource, studiosApi, type StudioContent, type StudioRecord, type TeamBoardPage, type TeamEdit, type TeamTaskView } from "./api"

const tabs = ["tasks", "risks", "drafts", "signoff"] as const
const emptyContent = (): StudioContent => ({title: "", text: "", source: "", duration_ms: 0, provider: "", model: "", seed: "", team: {work_item_id: "", signer_id: "", checklist: [{text: "", done: false, evidence_index: 0}], evidence: [], risks: [], sign_offs: []}})
const cloneContent = (value: StudioContent) => structuredClone(value)
const localTime = (value?: string) => value && Number.isFinite(Date.parse(value)) ? new Date(Date.parse(value) - new Date(value).getTimezoneOffset() * 60000).toISOString().slice(0, 16) : ""
const isoTime = (value: string) => value ? new Date(value).toISOString() : ""
export default function TeamBoard({tenant, project}: {tenant: string; project: string}) {
 const t = useTranslations("iam"), allowed = useAllowed()
 const read = allowed("template_read") && allowed("work_item_read")
 const manage = read && allowed("project_manage")
 const [params, setParams] = useSearchParams()
 const tab = tabs.find(value => value === params.get("team_tab")) ?? "tasks"
 const cursor = params.get("team_cursor") ?? ""
 const [page, setPage] = useState<TeamBoardPage>({items: [], drafts: [], has_more: false})
 const [loading, setLoading] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState("")
 const [reload, setReload] = useState(0), [selected, setSelected] = useState<StudioRecord | null>(null)
 const [content, setContent] = useState<StudioContent>(emptyContent), [reason, setReason] = useState("")
 const [draftEdit, setDraftEdit] = useState(false), [draft, setDraft] = useState<StudioContent>(() => ({...emptyContent(), team: undefined}))
 const pending = useRef<{fingerprint: string; key: string} | null>(null), mutation = useRef<AbortController | null>(null)
 useEffect(() => () => mutation.current?.abort(), [])
 useEffect(() => {
  if (!read) return
  const abort = new AbortController()
  queueMicrotask(() => {if (!abort.signal.aborted) {setLoading(true); setError("")}})
  studiosApi.team(tenant, project, cursor, abort.signal).then(value => {if (!abort.signal.aborted) setPage(value)}).catch(failure => {if (!abort.signal.aborted) setError(String(failure))}).finally(() => {if (!abort.signal.aborted) setLoading(false)})
  return () => abort.abort()
 }, [tenant, project, cursor, read, reload])
 const navigate = (key: string, value: string) => setParams(old => {const next = new URLSearchParams(old); if (value) next.set(key, value); else next.delete(key); return next})
 const run = async (fingerprint: string, operation: (key: string, signal: AbortSignal) => Promise<unknown>) => {
  if (!manage || mutation.current) return
  if (pending.current?.fingerprint !== fingerprint) pending.current = {fingerprint, key: crypto.randomUUID()}
  const abort = new AbortController(); mutation.current = abort; setBusy(true); setError("")
  try {await operation(pending.current.key, abort.signal); if (!abort.signal.aborted) {pending.current = null; setReload(value => value + 1); setSelected(null); setContent(emptyContent()); setDraftEdit(false)}} catch (failure) {if (!abort.signal.aborted) setError(String(failure))} finally {if (!abort.signal.aborted) {mutation.current = null; setBusy(false)}}
 }
 const command = (record: StudioRecord | null, action: TeamEdit["action"], value = content) => {
  const body: TeamEdit = {version: record?.version ?? 0, action, reason, content: value.team ? {...value, team: {...value.team, work_item_id: value.team.work_item_id || "0"}} : value}
  void run(JSON.stringify({id: record?.id, body}), (key, signal) => studiosApi.teamEdit(tenant, project, record?.id ?? "", body, key, signal))
 }
 const select = (record: StudioRecord, editDraft = false) => {
  setSelected(record); setDraftEdit(editDraft)
  setContent({...cloneContent(record.content), team: record.content.team ? cloneContent(record.content).team : emptyContent().team})
  navigate("team_tab", editDraft ? "drafts" : "tasks")
 }
 const change = (field: keyof StudioContent, value: string) => setContent(old => ({...old, [field]: value}))
 const team = content.team!
 const changeTeam = (update: Partial<typeof team>) => setContent(old => ({...old, team: {...old.team!, ...update}}))
 const taskFacts = (task: TeamTaskView) => <article key={task.record.id} className="space-y-3 border-b py-4">
  <h2 className="font-medium">{task.work.title} <span className="text-sm text-muted-foreground">#{task.work.id}</span></h2>
  <dl className="grid gap-2 text-sm sm:grid-cols-2 xl:grid-cols-4">
   <div><dt>{t("teamCanonicalState")}</dt><dd>{t.has(`workState_${task.work.state}`) ? t(`workState_${task.work.state}`) : task.work.state} · {t(`workLevel_${task.work.risk}`)}</dd></div>
   <div><dt>{t("studioOwner")}</dt><dd>{task.work.owner_id}</dd></div>
   <div><dt>{t("teamChecklist")}</dt><dd>{task.complete} / {task.record.content.team!.checklist.length}<progress className="block w-full" value={task.complete} max={task.record.content.team!.checklist.length} aria-label={t("teamChecklist")}/></dd></div>
   <div><dt>{t("teamSignoff")}</dt><dd>{t(task.signed ? "teamSigned" : "teamUnsigned")}</dd></div>
  </dl>
  <p className="text-sm">{t("studioDue")}: {task.record.content.due_at} · {t("teamWorkVersion")}: {task.work.version} · {t("teamStarted")}: {task.work.started_at}</p>
  <ul className="space-y-1 text-sm">{task.record.content.team!.checklist.map((item, index) => <li key={index}>{item.done ? "✓" : "○"} {item.text} · {t("teamEvidenceIndex")} {item.evidence_index + 1}</li>)}</ul>
  <ul className="space-y-2 text-sm">{task.record.content.team!.evidence.map((evidence, index) => <li key={index} className="break-all">{index + 1}. {studioSource(evidence.url) ? <a href={studioSource(evidence.url)!} target="_blank" rel="noopener noreferrer" className="underline">{evidence.url}</a> : evidence.url} · {evidence.version} · {evidence.modified_at}</li>)}</ul>
  <p className="text-sm text-muted-foreground">{task.record.content.team!.validation && task.record.content.team!.validation.work_version === task.work.version ? `${t("teamObservedAt")}: ${task.record.content.team!.validation.at}` : t("teamUnvalidated")}</p>
  {manage && <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={busy} onClick={() => select(task.record)}>{t("teamEdit")}</Button><Button variant="outline" disabled={busy || !reason.trim() || !task.record.content.team!.evidence.length} onClick={() => command(task.record, "CHECK_EVIDENCE", task.record.content)}>{t("teamValidate")}</Button></div>}
 </article>
 return <div className="flex min-w-0 flex-1 flex-col gap-4 overflow-auto">
  <PageHeader title={t("studioTEAM_TASKS")}/>
  {!read ? <p role="status">{t("projectSurfaceAccessUnavailable")}</p> : <>
   <p className="text-sm text-muted-foreground">{t("teamNotice")}</p>
   {error && <p role="alert" className="rounded-md border border-destructive p-3">{error}</p>}
   <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={busy || loading} onClick={() => setReload(value => value + 1)}>{t("templateRefresh")}</Button><Button variant="outline" disabled={busy || loading || !cursor} onClick={() => navigate("team_cursor", "")}>{t("templateBack")}</Button><Button variant="outline" disabled={busy || loading || !page.has_more} onClick={() => navigate("team_cursor", page.next_cursor ?? "")}>{t("templateNext")}</Button></div>
   {manage && <label className="block max-w-2xl text-sm">{t("teamScopeReason")}<Textarea value={reason} maxLength={2000} onChange={event => setReason(event.target.value)}/></label>}
   {loading ? <p role="status">{t("loading")}</p> : <Tabs value={tab} onValueChange={value => navigate("team_tab", value)}>
    <TabsList className="h-auto flex-wrap" aria-label={t("studioTEAM_TASKS")}>{tabs.map(value => <TabsTrigger className="text-foreground" key={value} value={value}>{t(`teamTab_${value}`)}</TabsTrigger>)}</TabsList>
    <TabsContent value="tasks">
     {!page.items.length && <p className="py-4">{t("studioEmpty")}</p>}{page.items.map(taskFacts)}
    </TabsContent>
    <TabsContent value="risks"><div className="divide-y">{page.items.flatMap(task => task.record.content.team!.risks.map((risk, index) => <article key={`${task.record.id}:${index}`} className="space-y-2 py-4"><h2 className="font-medium">{risk.title}</h2><p>{task.work.title} · {t("studioOwner")}: {risk.owner_id} · {t("studioDue")}: {risk.due_at}</p><p>{t(risk.resolved ? "teamRiskResolved" : "teamRiskOpen")} · {risk.disposition || t("teamNoDisposition")}</p>{manage && <Button variant="outline" onClick={() => select(task.record)}>{t("teamEdit")}</Button>}</article>))}{!page.items.some(task => task.record.content.team!.risks.length) && <p className="py-4">{t("teamNoRisks")}</p>}</div></TabsContent>
    <TabsContent value="drafts"><div className="space-y-4">
     <p className="text-sm text-muted-foreground">{t("teamDraftNotice")}</p>
     {page.drafts.map(record => <article key={record.id} className="space-y-2 border-b py-4"><h2 className="font-medium">{record.content.title}</h2><p className="whitespace-pre-wrap">{record.content.text}</p><p>{record.content.owner_id} · {record.content.due_at}</p>{studioSource(record.content.source) && <a className="underline" href={studioSource(record.content.source)!} target="_blank" rel="noopener noreferrer">{t("studioSource")}</a>}{manage && record.kind === "TASK" && <Button variant="outline" onClick={() => select(record)}>{t("teamEdit")}</Button>}{manage && record.kind === "ASSISTANT_DRAFT" && record.state === "OPEN" && <div className="flex flex-wrap gap-2"><Button onClick={() => select(record)} disabled={busy}>{t("teamAdoptPrepare")}</Button><Button variant="outline" onClick={() => select(record, true)} disabled={busy}>{t("teamEditDraft")}</Button><Button variant="outline" disabled={busy || !reason.trim()} onClick={() => void run(JSON.stringify({reject: record.id, version: record.version, reason}), (key, signal) => studiosApi.decide(tenant, project, record, "REJECT", reason, key, signal))}>{t("studioReject")}</Button></div>}</article>)}
     {manage && <fieldset disabled={busy} className="max-w-2xl space-y-3"><legend>{t("teamRecordDraft")}</legend><label className="block text-sm">{t("teamDraftType")}<select className="block w-full rounded-md border bg-background p-2" value={draft.draft_type ?? "ACTION"} onChange={event => setDraft(old => ({...old, draft_type: event.target.value as StudioContent["draft_type"]}))}>{["ACTION", "TRIAGE", "STATUS"].map(value => <option value={value} key={value}>{t(`teamDraft_${value}`)}</option>)}</select></label>{draft.draft_type === "STATUS" && <label className="block text-sm">{t("teamReportedState")}<Input value={draft.reported_state ?? ""} maxLength={32} onChange={event => setDraft(old => ({...old, reported_state: event.target.value}))}/></label>}{(["title", "text", "source"] as const).map(field => <label key={field} className="block text-sm">{t({title: "studioTitle", text: "studioText", source: "studioSource"}[field])}<Input value={draft[field]} maxLength={field === "text" ? 20000 : field === "source" ? 2000 : 200} onChange={event => setDraft(old => ({...old, [field]: event.target.value}))}/></label>)}<Button disabled={!draft.title.trim() || !draft.text.trim() || !draft.source.trim()} onClick={() => void run(JSON.stringify({draft}), (key, signal) => studiosApi.create(tenant, project, {domain: "TEAM_TASKS", kind: "ASSISTANT_DRAFT", content: draft}, key, signal))}>{t("teamRecordDraft")}</Button></fieldset>}
    </div></TabsContent>
    <TabsContent value="signoff"><p className="py-3 text-sm text-muted-foreground">{t("teamSignoffNotice")}</p>{page.items.map(task => <section key={task.record.id} className="border-b pb-4">{taskFacts(task)}{task.record.content.team!.sign_offs?.map((sign, index) => <p className="break-all py-2 text-sm" key={index}>{t("teamSigner")}: {sign.actor_id} · {sign.at} · {sign.scope} · {sign.content_hash} · {t("teamWorkVersion")}: {sign.work_version}</p>)}{manage && <Button disabled={busy || !reason.trim() || !teamCanSign(task)} onClick={() => command(task.record, "SIGN_OFF", task.record.content)}>{t("teamSignoff")}</Button>}</section>)}</TabsContent>
   </Tabs>}
   {manage && (tab === "tasks" || (tab === "drafts" && draftEdit)) && <fieldset disabled={busy} className="mt-4 space-y-4 border-t pt-5"><legend className="font-medium">{t(selected ? (draftEdit ? "teamEditDraft" : "teamEdit") : "teamCreate")}</legend>
    <div className="grid gap-3 sm:grid-cols-2"><label className="text-sm">{t("studioTitle")}<Input value={content.title} maxLength={200} onChange={event => change("title", event.target.value)}/></label><label className="text-sm">{t("studioText")}<Textarea value={content.text} maxLength={20000} onChange={event => change("text", event.target.value)}/></label></div>
    {(content.draft_type === "STATUS") && <label className="block text-sm">{t("teamReportedState")}<Input value={content.reported_state ?? ""} maxLength={32} onChange={event => change("reported_state", event.target.value)}/></label>}
    {draftEdit && <div className="grid gap-3 sm:grid-cols-2"><label className="text-sm">{t("studioOwner")}<Input value={content.owner_id ?? ""} inputMode="numeric" onChange={event => change("owner_id", event.target.value)}/></label><label className="text-sm">{t("studioDue")}<Input type="datetime-local" value={localTime(content.due_at)} onChange={event => change("due_at", isoTime(event.target.value))}/></label></div>}
    {!draftEdit && <>
     <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4"><label className="text-sm">{t("teamWorkID")}<Input value={team.work_item_id} inputMode="numeric" disabled={selected?.kind === "TASK" && !!selected.content.team} onChange={event => changeTeam({work_item_id: event.target.value})}/></label><label className="text-sm">{t("studioOwner")}<Input value={content.owner_id ?? ""} inputMode="numeric" onChange={event => change("owner_id", event.target.value)}/></label><label className="text-sm">{t("teamSigner")}<Input value={team.signer_id} inputMode="numeric" onChange={event => changeTeam({signer_id: event.target.value})}/></label><label className="text-sm">{t("studioDue")}<Input type="datetime-local" value={localTime(content.due_at)} onChange={event => change("due_at", isoTime(event.target.value))}/></label></div>
     <section className="space-y-2"><h3 className="font-medium">{t("teamChecklist")}</h3>{team.checklist.map((item, index) => <div key={index} className="flex flex-wrap items-end gap-2"><label className="min-w-48 flex-1 text-sm">{t("teamCriterion")} {index + 1}<Input value={item.text} maxLength={500} onChange={event => changeTeam({checklist: team.checklist.map((old, i) => i === index ? {...old, text: event.target.value} : old)})}/></label><label className="text-sm">{t("teamEvidenceIndex")}<Input type="number" className="w-24" min={1} max={10} value={item.evidence_index + 1} onChange={event => changeTeam({checklist: team.checklist.map((old, i) => i === index ? {...old, evidence_index: Number(event.target.value) - 1} : old)})}/></label><label className="flex min-h-11 items-center gap-2 text-sm"><input type="checkbox" checked={item.done} onChange={event => changeTeam({checklist: team.checklist.map((old, i) => i === index ? {...old, done: event.target.checked} : old)})}/>{t("teamComplete")}</label><Button variant="outline" onClick={() => changeTeam({checklist: team.checklist.filter((_, i) => i !== index)})}>{t("teamRemove")}</Button></div>)}<Button variant="outline" disabled={team.checklist.length >= 50} onClick={() => changeTeam({checklist: [...team.checklist, {text: "", done: false, evidence_index: 0}]})}>{t("teamAddCriterion")}</Button></section>
     <section className="space-y-3"><h3 className="font-medium">{t("teamEvidence")}</h3><p className="text-sm text-muted-foreground">{t("teamEvidenceNotice")}</p>{team.evidence.map((item, index) => <div key={index} className="grid gap-2 sm:grid-cols-3"><label className="text-sm">{index + 1}. {t("studioSource")}<Input value={item.url} maxLength={2000} onChange={event => changeTeam({evidence: team.evidence.map((old, i) => i === index ? {...old, url: event.target.value} : old)})}/></label><label className="text-sm">{t("teamEvidenceVersion")}<Input value={item.version} maxLength={200} onChange={event => changeTeam({evidence: team.evidence.map((old, i) => i === index ? {...old, version: event.target.value} : old)})}/></label><label className="text-sm">{t("teamModified")}<Input value={item.modified_at} onChange={event => changeTeam({evidence: team.evidence.map((old, i) => i === index ? {...old, modified_at: event.target.value} : old)})}/></label><Button variant="outline" onClick={() => changeTeam({evidence: team.evidence.filter((_, i) => i !== index)})}>{t("teamRemove")}</Button></div>)}<Button variant="outline" disabled={team.evidence.length >= 10} onClick={() => changeTeam({evidence: [...team.evidence, {url: "", version: "", modified_at: ""}]})}>{t("teamAddEvidence")}</Button></section>
     <section className="space-y-3"><h3 className="font-medium">{t("teamTab_risks")}</h3>{team.risks.map((risk, index) => <div key={index} className="grid gap-2 border-t pt-3 sm:grid-cols-2">{(["title", "owner_id", "due_at", "disposition"] as const).map(field => <label key={field} className="text-sm">{t({title: "teamRiskTitle", owner_id: "studioOwner", due_at: "studioDue", disposition: "teamDisposition"}[field])}<Input value={field === "due_at" ? localTime(risk[field]) : risk[field]} type={field === "due_at" ? "datetime-local" : "text"} onChange={event => changeTeam({risks: team.risks.map((old, i) => i === index ? {...old, [field]: field === "due_at" ? isoTime(event.target.value) : event.target.value} : old)})}/></label>)}<label className="flex min-h-11 items-center gap-2 text-sm"><input type="checkbox" checked={risk.resolved} onChange={event => changeTeam({risks: team.risks.map((old, i) => i === index ? {...old, resolved: event.target.checked} : old)})}/>{t("teamRiskResolved")}</label><Button variant="outline" onClick={() => changeTeam({risks: team.risks.filter((_, i) => i !== index)})}>{t("teamRemove")}</Button></div>)}<Button variant="outline" disabled={team.risks.length >= 50} onClick={() => changeTeam({risks: [...team.risks, {title: "", owner_id: "", due_at: "", disposition: "", resolved: false}]})}>{t("teamAddRisk")}</Button></section>
    </>}
    <div className="flex flex-wrap gap-2"><Button disabled={!reason.trim() || (draftEdit ? !content.title.trim() || !content.text.trim() : !teamCanSave(content))} onClick={() => command(selected, draftEdit ? "EDIT_DRAFT" : selected?.kind === "ASSISTANT_DRAFT" ? "ADOPT_DRAFT" : "SAVE", draftEdit ? {...content, team: undefined} : content)}>{t(draftEdit ? "teamEditDraft" : selected?.kind === "ASSISTANT_DRAFT" ? "studioConfirm" : "teamSave")}</Button><Button variant="outline" onClick={() => {setSelected(null); setContent(emptyContent()); setDraftEdit(false)}}>{t("teamClear")}</Button></div>
   </fieldset>}
  </>}
 </div>
}
