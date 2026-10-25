import { useEffect, useRef, useState, type FormEvent } from "react"
import { Link } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAllowed } from "../../lib/permissions"
import { memoryApi as api, type Memory, type Memories, type MemoryFilter, type MemoryHistory } from "./memory-api"

export function MemoryFacts({ memory, tenant }: { memory: Memory; tenant?: string }) {
 const t = useTranslations("iam")
 return <div className="space-y-1 text-sm text-muted-foreground"><p>{memory.kind} ? {memory.state} ? {memory.origin}</p><p>{t("agentMemoryUse", { count: memory.use_count })} ? {t("agentMemoryLastUsed")}: {memory.last_used_at ?? t("agentMemoryNever")} ? {t("agentMemoryExpiry")}: {memory.expires_at ?? t("agentMemoryNoExpiry")}</p>{!!memory.risk_flags?.length && <p className="text-destructive">{memory.risk_flags.join(" ? ")}</p>}{memory.source_execution_id && <p>{t("agentMemorySource")}: {tenant && memory.project_id ? <Link className="underline" to={`/admin/${tenant}/entities/${memory.project_id}/executions/${memory.source_execution_id}`}>{memory.source_execution_id}</Link> : memory.source_execution_id}</p>}{memory.supersedes_id && <p>{t("agentMemorySupersedes")}: {memory.supersedes_id}</p>}</div>
}
function History({ tenant, memory }: { tenant: string; memory: string }) {
 const t = useTranslations("iam")
 const [offset, setOffset] = useState(0)
 const [result, setResult] = useState<{ identity: string; value?: MemoryHistory; error?: string } | null>(null)
 const identity = `${tenant}:${memory}:${offset}`
 useEffect(() => { const controller = new AbortController(); void api.history(tenant, memory, offset, controller.signal).then(value => { if (!controller.signal.aborted) setResult({ identity, value }) }).catch((cause: Error) => { if (!controller.signal.aborted) setResult({ identity, error: cause.message }) }); return () => controller.abort() }, [tenant, memory, offset, identity])
 const current = result?.identity === identity ? result : null
 return <section aria-label={t("agentMemoryHistory")} className="space-y-3 border-t pt-3">{!current && <p role="status">{t("loading")}</p>}{current?.error && <Alert>{current.error}</Alert>}<ol className="space-y-2">{current?.value?.items?.map(row => <li key={row.id} className="text-sm"><p>{row.created_at} ? {row.action} ? {row.from_state} ? {row.to_state}</p><p>{row.reason} ? {row.actor_id}</p></li>)}</ol><div className="flex gap-2"><Button variant="outline" disabled={offset === 0} onClick={() => setOffset(value => Math.max(0, value - 50))}>{t("fleetPrevious")}</Button><Button variant="outline" disabled={!current?.value || offset + 50 >= current.value.total} onClick={() => setOffset(value => value + 50)}>{t("fleetNext")}</Button></div></section>
}
type EditorState = { mode: "CREATE" | "EDIT" | "PROMOTE" | "REVOKE" | "APPROVED" | "REJECTED"; memory?: Memory }
export function AgentMemories({ tenant, agent }: { tenant: string; agent: string }) {
 const t = useTranslations("iam")
 const allowed = useAllowed()
 const [filter, setFilter] = useState<MemoryFilter>({ offset: 0, limit: 50 })
 const [revision, setRevision] = useState(0)
 const [result, setResult] = useState<{ identity: string; page?: Memories; error?: string } | null>(null)
 const [history, setHistory] = useState("")
 const [editor, setEditor] = useState<EditorState | null>(null)
 const [busy, setBusy] = useState(false)
 const [error, setError] = useState("")
 const [success, setSuccess] = useState(false)
 const [affected, setAffected] = useState<string[]>([])
 const [bodyCount,setBodyCount]=useState(0)
 const attempt = useRef<{ body: string; key: string } | null>(null)
 const identity = `${tenant}:${agent}:${JSON.stringify(filter)}:${revision}`
 useEffect(() => { const controller = new AbortController(); void api.list(tenant, agent, filter, controller.signal).then(page => { if (!controller.signal.aborted) setResult({ identity, page }) }).catch((cause: Error) => { if (!controller.signal.aborted) setResult({ identity, error: cause.message }) }); return () => controller.abort() }, [tenant, agent, filter, revision, identity])
 const current = result?.identity === identity ? result : null
 const page = current?.page
 const updateFilter = (next: Partial<MemoryFilter>) => setFilter(value => ({ ...value, ...next, offset: 0 }))
 const open = (next: EditorState) => { setEditor(next); setError(""); setSuccess(false); setAffected([]);setBodyCount(Array.from(next.memory?.body ?? "").length); attempt.current = null }
 const submit = async (event: FormEvent<HTMLFormElement>) => {
  event.preventDefault(); if (!editor || busy) return
  const form = new FormData(event.currentTarget)
  const title = String(form.get("title") ?? "").trim(), body = String(form.get("body") ?? "").trim(), reason = String(form.get("reason") ?? "").trim()
  const ttl = Number(form.get("ttl_days") ?? 180)
  const payload = JSON.stringify({ editor, title, body, reason, ttl, kind: form.get("kind"), project: filter.project_id, conflicts: form.get("conflicts") === "on" })
  if (attempt.current?.body !== payload) attempt.current = { body: payload, key: crypto.randomUUID() }
  const key = attempt.current.key
  setBusy(true); setError("")
  try {
   if (editor.mode === "CREATE") await api.create(tenant, agent, { title, body, kind: form.get("kind") as "EXPERIENCE" | "RULE", project_id: filter.project_id, ttl_days: ttl }, key)
   else {
    const memory = editor.memory
    if (!memory?.id) return
    if (editor.mode === "EDIT") await api.edit(tenant, memory.id, { title, body, ttl_days: ttl, version: memory.version, reason }, key)
    else if (editor.mode === "PROMOTE") await api.promote(tenant, memory.id, memory.version, reason, key)
    else { const changed = await api.review(tenant, memory.id, memory.version, editor.mode === "REVOKE" ? "REVOKED" : editor.mode, reason, key, form.get("conflicts") === "on");if (editor.mode === "REVOKE") setAffected((changed.affected_execution_ids ?? []).filter((id): id is string => typeof id === "string")) }
   }
   attempt.current = null; setEditor(null); setSuccess(true); setRevision(value => value + 1)
  } catch (cause) { setError((cause as Error).message) } finally { setBusy(false) }
 }
 return <section aria-label={t("agentMemories")} className="space-y-4">
  <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="font-medium">{t("agentMemories")}</h2><div className="flex gap-2"><Button variant="outline" onClick={() => setRevision(value => value + 1)}>{t("refresh")}</Button>{allowed("knowledge_write") && allowed("agent_read") && (!!filter.project_id || allowed("memory_approve")) && <Button onClick={() => open({ mode: "CREATE" })}>{t("agentMemoryWrite")}</Button>}</div></div>
  <p className="text-sm text-muted-foreground">{t("agentMemoryHint")}</p>
  {page && <div className="grid gap-2 text-sm sm:grid-cols-4"><p>{t("agentMemoryExperience")}: {page.stats.active_experience}/{page.stats.experience_limit}</p><p>{t("agentMemoryRules")}: {page.stats.active_rules}/{page.stats.rule_limit}</p><p>{t("agentMemoryPending")}: {page.stats.pending}/{page.stats.pending_limit}</p><p>{t("agentMemoryToday")}: {page.stats.today_writes}/{page.stats.daily_limit}</p></div>}
  <div className="grid gap-3 sm:grid-cols-4"><label className="space-y-1 text-sm">{t("agentMemorySearch")}<Input value={filter.search ?? ""} onChange={event => updateFilter({ search: event.target.value })} maxLength={200}/></label><label className="space-y-1 text-sm">{t("agentMemoryKind")}<select className="h-9 w-full rounded-md border bg-background px-2" value={filter.kind ?? ""} onChange={event => updateFilter({ kind: event.target.value as MemoryFilter["kind"] || undefined })}><option value="">{t("agentMemoryAll")}</option><option value="EXPERIENCE">{t("agentMemoryExperience")}</option><option value="RULE">{t("agentMemoryRules")}</option></select></label><label className="space-y-1 text-sm">{t("agentMemoryState")}<select className="h-9 w-full rounded-md border bg-background px-2" value={filter.state ?? ""} onChange={event => updateFilter({ state: event.target.value as MemoryFilter["state"] || undefined })}><option value="">{t("agentMemoryAll")}</option>{["ACTIVE", "PENDING_REVIEW", "REJECTED", "REVOKED", "EXPIRED", "SUPERSEDED"].map(value => <option key={value}>{value}</option>)}</select></label><label className="space-y-1 text-sm">{t("agentMemoryProject")}<Input value={filter.project_id ?? ""} inputMode="numeric" onChange={event => updateFilter({ project_id: event.target.value || undefined })} /></label></div>
  {!current && <p role="status">{t("loading")}</p>}{current?.error && <Alert>{current.error}</Alert>}{error && <Alert>{error}</Alert>}{success && <p role="status">{t("agentMemorySaved")}</p>}{!!affected.length && <section aria-label={t("agentMemoryAffected")}><h3 className="text-sm font-medium">{t("agentMemoryAffected")}</h3><ul className="text-sm">{affected.map(id => <li key={id}>{id}</li>)}</ul></section>}
  {editor && <form key={`${editor.mode}:${editor.memory?.id ?? "new"}`} onSubmit={submit} className="space-y-3 rounded-md border p-4" aria-label={t("agentMemoryWrite")}>
   {(editor.mode === "CREATE" || editor.mode === "EDIT") && <><label className="block space-y-1 text-sm">{t("agentMemoryTitle")}<Input name="title" required maxLength={120} defaultValue={editor.memory?.title}/></label><label className="block space-y-1 text-sm">{t("agentMemoryBody")}<textarea className="min-h-28 w-full rounded-md border bg-background p-2" name="body" required minLength={10} maxLength={1000} defaultValue={editor.memory?.body} onInput={event => setBodyCount(Array.from(event.currentTarget.value).length)}/><span className="text-xs text-muted-foreground">{bodyCount}/1000</span></label>{editor.mode === "CREATE" && <label className="block text-sm">{t("agentMemoryKind")}<select name="kind" className="ml-3 rounded-md border bg-background p-2"><option value="EXPERIENCE">{t("agentMemoryExperience")}</option><option value="RULE">{t("agentMemoryRules")}</option></select></label>}<label className="block space-y-1 text-sm">{t("agentMemoryTTL")}<Input name="ttl_days" type="number" required min={7} max={730} defaultValue={editor.memory?.ttl_days || 180}/></label></>}
   {editor.mode !== "CREATE" && <label className="block space-y-1 text-sm">{t("agentMemoryReason")}<textarea className="min-h-20 w-full rounded-md border bg-background p-2" name="reason" required minLength={5} maxLength={2000}/></label>}{editor.mode === "APPROVED" && !!editor.memory?.conflict_memory_ids?.length && <label className="flex gap-2 text-sm"><input name="conflicts" type="checkbox" required/>{t("agentMemoryAcknowledge")}: {editor.memory.conflict_memory_ids.join(", ")}</label>}{editor.mode === "REVOKE" && <p className="text-sm text-destructive">{t("agentMemoryRevokeConfirm")}</p>}
   <div className="flex gap-2"><Button disabled={busy} type="submit">{busy ? t("loading") : t("agentMemoryConfirm")}</Button><Button disabled={busy} type="button" variant="outline" onClick={() => setEditor(null)}>{t("cancel")}</Button></div>
  </form>}
  <ol className="space-y-4">{page?.items?.map(memory => { const id = memory.id; return id ? <li key={id} className="space-y-2 border-b pb-4"><h3 className="break-words font-medium">{memory.title}</h3><p className="whitespace-pre-wrap break-words text-sm">{memory.body}</p><MemoryFacts memory={memory} tenant={tenant}/><p className="text-sm text-muted-foreground">{memory.provenance}</p><div className="flex flex-wrap gap-2"><Button variant="outline" onClick={() => setHistory(value => value === id ? "" : id)}>{t("agentMemoryHistory")}</Button>{memory.state === "ACTIVE" && allowed("knowledge_write") && <Button variant="outline" onClick={() => open({ mode: "EDIT", memory })}>{t("agentMemoryEdit")}</Button>}{memory.state === "ACTIVE" && allowed("knowledge_manage") && <Button variant="outline" onClick={() => open({ mode: "REVOKE", memory })}>{t("agentMemoryRevoke")}</Button>}{memory.state === "ACTIVE" && memory.kind === "RULE" && allowed("memory_approve") && <Button variant="outline" onClick={() => open({ mode: "PROMOTE", memory })}>{t("agentMemoryPromote")}</Button>}{memory.state === "PENDING_REVIEW" && allowed("memory_approve") && <><Button variant="outline" onClick={() => open({ mode: "APPROVED", memory })}>{t("agentMemoryApprove")}</Button><Button variant="outline" onClick={() => open({ mode: "REJECTED", memory })}>{t("agentMemoryReject")}</Button></>}</div>{history === id && <History tenant={tenant} memory={id}/>}</li> : null })}</ol>
  {page && !page.items?.length && <p>{t("agentMemoryEmpty")}</p>}<div className="flex gap-2"><Button variant="outline" disabled={!page || filter.offset === 0} onClick={() => setFilter(value => ({ ...value, offset: Math.max(0, value.offset - value.limit) }))}>{t("fleetPrevious")}</Button><Button variant="outline" disabled={!page || filter.offset + filter.limit >= page.total} onClick={() => setFilter(value => ({ ...value, offset: value.offset + value.limit }))}>{t("fleetNext")}</Button></div>
 </section>
}
