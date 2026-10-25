import { useEffect, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { knowledgeSkills, type Skill, type SkillVersion, type SkillMount, type SkillResult } from "./skills-api"

export default function KnowledgeSkillsPage() {
  const {tenantID = ""} = useParams()
  return <Skills key={tenantID} tenant={tenantID} />
}
function Skills({tenant}: {tenant: string}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [query, setQuery] = useSearchParams()
  const selected = query.get("skill") ?? ""
  const [search, setSearch] = useState("")
  const [state, setState] = useState<"" | "ACTIVE" | "ARCHIVED">("")
  const [offset, setOffset] = useState(0)
  const [items, setItems] = useState<Skill[]>([])
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    queueMicrotask(() => {if (!controller.signal.aborted) {setLoading(true); setItems([]); setError("")}})
    void knowledgeSkills.list(tenant, offset, search, state || undefined, controller.signal).then(page => {if (!controller.signal.aborted) {setItems(page.items ?? []); setHasMore(page.has_more)}}, cause => {if (!controller.signal.aborted) setError(cause.message)}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
    return () => controller.abort()
  }, [tenant, offset, search, state, revision])
  return <div className="flex min-w-0 flex-col gap-5">
    <PageHeader title={t("knowledgeSkills")} description={t("knowledgeSkillsHint")} />
    <Link className="min-h-11 self-start underline" to={`/admin/${encodeURIComponent(tenant)}/knowledge`}>{t("knowledge")}</Link>
    {error && <Alert>{error}</Alert>}
    <div className="flex flex-wrap gap-3"><label className="grid gap-2 text-sm">{t("search")}<Input value={search} maxLength={200} onChange={event => {setSearch(event.target.value); setOffset(0)}} /></label><label className="grid gap-2 text-sm">{t("fleetState")}<select className="min-h-11 rounded border bg-background px-3" value={state} onChange={event => {setState(event.target.value as typeof state); setOffset(0)}}><option value="">{t("runtimeAllStates")}</option>{["ACTIVE", "ARCHIVED"].map(value => <option key={value} value={value}>{t(`knowledgeSkillState_${value}`)}</option>)}</select></label></div>
    <AutoTable data={items} rowKey="id" loading={loading} pagination={false} exportable={false} emptyContent={t("agentEmpty")} onRefresh={() => setRevision(value => value + 1)} columns={[{field: "skill_key", name: t("name"), format: (_, row) => <Button variant="link" onClick={() => {if (row.id) setQuery({skill: row.id})}}>{row.skill_key}</Button>}, {field: "description", name: t("knowledgeSkillDescription")}, {field: "state", name: t("fleetState"), format: (_, row) => t(`knowledgeSkillState_${row.state}`)}, {field: "current_version_id", name: t("knowledgeSkillCurrent"), format: (_, row) => row.current_version_id ? t("knowledgeSkillApprovedCurrent") : t("knowledgeSkillNoApproved")}]} />
    <div className="flex gap-3"><Button variant="outline" disabled={!offset || loading} onClick={() => setOffset(value => Math.max(0, value - 50))}>{t("knowledgeSkillPrevious")}</Button><Button variant="outline" disabled={!hasMore || loading} onClick={() => setOffset(value => value + 50)}>{t("agentMore")}</Button></div>
    {allowed("knowledge_write") && <SkillEditor tenant={tenant} onSaved={result => {if (result.skill.id) setQuery({skill: result.skill.id}); setRevision(value => value + 1)}} />}
    {selected && <SkillDetail key={selected} tenant={tenant} id={selected} onChanged={() => setRevision(value => value + 1)} />}
  </div>
}
function SkillEditor({tenant, skill, onSaved}: {tenant: string; skill?: string; onSaved: (result: SkillResult) => void}) {
  const t = useTranslations("iam")
  const [content, setContent] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [warnings, setWarnings] = useState<string[]>([])
  const attempt = useRef<string | null>(null)
  return <details className="rounded-md border p-4"><summary className="min-h-11 cursor-pointer font-medium">{t(skill ? "knowledgeSkillAddVersion" : "knowledgeSkillCreate")}</summary><form className="grid gap-3 pt-3" onSubmit={event => {
    event.preventDefault(); if (busy) return
    setBusy(true); setError(""); attempt.current ??= crypto.randomUUID()
    const request = skill ? knowledgeSkills.addVersion(tenant, skill, content, attempt.current) : knowledgeSkills.create(tenant, content, attempt.current)
    void request.then(result => {setWarnings(result.warnings ?? []); setContent(""); attempt.current = null; onSaved(result)}).catch(cause => setError(cause.message)).finally(() => setBusy(false))
  }}><label className="grid gap-2 text-sm">SKILL.md<textarea className="min-h-56 w-full rounded border bg-background p-3 font-mono text-sm" required disabled={busy} value={content} aria-describedby={`skill-format-${skill ?? "new"}`} onChange={event => {setContent(event.target.value); attempt.current = null}} /></label><p id={`skill-format-${skill ?? "new"}`} className="text-sm text-muted-foreground">{t("knowledgeSkillFormat")}</p>{error && <Alert>{error}</Alert>}{warnings.length > 0 && <ul role="status" className="list-inside list-disc text-sm">{warnings.map(warning => <li key={warning}>{warning}</li>)}</ul>}<Button className="justify-self-start" type="submit" disabled={busy}>{t(busy ? "loading" : "knowledgeSkillSaveDraft")}</Button></form></details>
}
function SkillDetail({tenant, id, onChanged}: {tenant: string; id: string; onChanged: () => void}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [skill, setSkill] = useState<Skill | null>(null)
  const [versions, setVersions] = useState<SkillVersion[]>([])
  const [offset, setOffset] = useState(0)
  const [more, setMore] = useState(false)
  const [selected, setSelected] = useState<SkillVersion | null>(null)
  const [reason, setReason] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const reviewAttempt = useRef<{fingerprint: string; key: string} | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    void Promise.all([knowledgeSkills.get(tenant, id, controller.signal), knowledgeSkills.versions(tenant, id, offset, "", controller.signal)]).then(([detail, page]) => {if (!controller.signal.aborted) {setSkill(detail); setVersions(page.items ?? []); setMore(page.has_more); setError("")}}, cause => {if (!controller.signal.aborted) setError(cause.message)})
    return () => controller.abort()
  }, [tenant, id, offset, revision])
  const refresh = () => {setRevision(value => value + 1); onChanged()}
  const loadVersion = async (version: string) => {setBusy(true); setError(""); try {setSelected(await knowledgeSkills.version(tenant, version)); setReason(""); reviewAttempt.current = null} catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))} finally {setBusy(false)}}
  const review = async (decision: "APPROVED" | "REJECTED") => {
    if (!selected?.id || busy) return
    const fingerprint = JSON.stringify({id: selected.id, decision, reason})
    if (reviewAttempt.current?.fingerprint !== fingerprint) reviewAttempt.current = {fingerprint, key: crypto.randomUUID()}
    setBusy(true); setError("")
    try {setSelected(await knowledgeSkills.review(tenant, selected.id, decision, reason, reviewAttempt.current.key)); reviewAttempt.current = null; refresh()} catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))} finally {setBusy(false)}
  }
  return <section className="grid gap-4 border-t pt-5" aria-label={t("knowledgeSkillVersions")}>
    {error && <Alert>{error}</Alert>}
    {skill && <><h2 className="text-xl font-semibold">{skill.skill_key}</h2><p>{skill.description}</p><p className="text-sm text-muted-foreground">{t("knowledgeSkillCurrent")}: {skill.current_version ? `v${skill.current_version.version_no} · ${skill.current_version.content_hash.slice(0, 12)}` : t("knowledgeSkillNoApproved")}</p>
      {allowed("knowledge_write") && <Button variant="outline" className="justify-self-start" disabled={busy} onClick={() => {setBusy(true); void knowledgeSkills.state(tenant, id, skill.version, skill.state === "ARCHIVED" ? "ACTIVE" : "ARCHIVED", crypto.randomUUID()).then(() => refresh()).catch(cause => setError(cause.message)).finally(() => setBusy(false))}}>{t(skill.state === "ARCHIVED" ? "knowledgeSkillRestore" : "knowledgeSkillArchive")}</Button>}
      <AutoTable data={versions} rowKey="id" pagination={false} exportable={false} columns={[{field: "version_no", name: t("knowledgeSkillVersions"), format: (_, row) => <Button variant="link" disabled={busy} onClick={() => {if (row.id) void loadVersion(row.id)}}>v{row.version_no}</Button>}, {field: "state", name: t("fleetState"), format: (_, row) => t(`knowledgeSkillState_${row.state}`)}, {field: "content_hash", name: t("knowledgeSkillHash"), format: (_, row) => <span className="font-mono">{row.content_hash.slice(0, 12)}</span>}]} />
      <div className="flex gap-3"><Button variant="outline" disabled={!offset || busy} onClick={() => setOffset(value => Math.max(0, value - 50))}>{t("knowledgeSkillPrevious")}</Button><Button variant="outline" disabled={!more || busy} onClick={() => setOffset(value => value + 50)}>{t("agentMore")}</Button></div>
      {selected && <div className="grid gap-3"><h3 className="font-medium">v{selected.version_no} · {t(`knowledgeSkillState_${selected.state}`)}</h3>{selected.origin === "DISTILLED" && <p className="text-sm text-muted-foreground">{t("knowledgeSkillDistillSource")}: {selected.source_work_item_id} / {selected.source_execution_id}</p>}<pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded border bg-muted p-3 text-sm">{selected.content}</pre>{selected.review_reason && <p>{selected.review_reason}</p>}{allowed("knowledge_write") && selected.state === "DRAFT" && skill.state === "ACTIVE" && <><label className="grid gap-2 text-sm">{t("knowledgeSkillReviewReason")}<Input disabled={busy} value={reason} maxLength={2000} onChange={event => setReason(event.target.value)} /></label><div className="flex gap-3"><Button disabled={busy} onClick={() => void review("APPROVED")}>{t("knowledgeSkillApprove")}</Button><Button disabled={busy} variant="outline" onClick={() => void review("REJECTED")}>{t("knowledgeSkillReject")}</Button></div></>}</div>}
      {allowed("knowledge_write") && skill.state === "ACTIVE" && <SkillEditor tenant={tenant} skill={id} onSaved={result => {setSelected(result.version); refresh()}} />}
      {(allowed("agent_read") || allowed("agent_manage")) && <SkillMounts tenant={tenant} skill={skill} versions={versions} />}
    </>}
  </section>
}
function SkillMounts({tenant, skill, versions}: {tenant: string; skill: Skill; versions: SkillVersion[]}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [agent, setAgent] = useState("")
  const [openedAgent, setOpenedAgent] = useState("")
  const [pin, setPin] = useState("")
  const [mounts, setMounts] = useState<SkillMount[]>([])
  const [loadedAgent, setLoadedAgent] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const attempt = useRef<{fingerprint: string; key: string} | null>(null)
  useEffect(() => {
    if (!openedAgent) return
    const controller = new AbortController()
    void knowledgeSkills.mounts(tenant, openedAgent, 0, "", controller.signal).then(page => {if (!controller.signal.aborted) {setMounts(page.items ?? []); setLoadedAgent(openedAgent); setError("")}}, cause => {if (!controller.signal.aborted) setError(cause.message)})
    return () => controller.abort()
  }, [tenant, openedAgent, revision])
  const mounted = loadedAgent === openedAgent ? mounts.find(row => row.skill.id === skill.id) : undefined
  return <div className="grid gap-3 rounded border p-4"><h3 className="font-medium">{t("knowledgeSkillMounts")}</h3><p className="text-sm text-muted-foreground">{t("knowledgeSkillMountHint")}</p>{error && <Alert>{error}</Alert>}
    <form className="flex flex-wrap items-end gap-3" onSubmit={event => {event.preventDefault(); setOpenedAgent(agent)}}><label className="grid gap-2 text-sm">{t("agentPrincipal")}<Input value={agent} required inputMode="numeric" pattern="[1-9][0-9]{0,18}" onChange={event => setAgent(event.target.value)} /></label><Button type="submit" disabled={busy}>{t("agentOpen")}</Button></form>
    {openedAgent && <><p role="status" className="text-sm">{mounted ? `${t(mounted.archived ? "knowledgeSkillState_ARCHIVED" : mounted.pinned_version_id ? "knowledgeSkillPinned" : "knowledgeSkillFollow")} · ${mounted.resolved_version_id ?? t("knowledgeSkillNoApproved")}` : t("knowledgeSkillNotMounted")}</p>{allowed("agent_manage") && <><label className="grid gap-2 text-sm">{t("knowledgeSkillVersionPolicy")}<select className="min-h-11 rounded border bg-background px-3" value={pin} disabled={busy} onChange={event => setPin(event.target.value)}><option value="">{t("knowledgeSkillFollow")}</option>{versions.filter(version => version.state === "APPROVED").map(version => <option key={version.id} value={version.id ?? ""}>v{version.version_no} · {version.content_hash.slice(0, 12)}</option>)}</select></label><div className="flex flex-wrap gap-3"><Button disabled={busy || skill.state === "ARCHIVED"} onClick={() => {if (!skill.id) return; const fingerprint = JSON.stringify({agent: openedAgent, skill: skill.id, pin}); if (attempt.current?.fingerprint !== fingerprint) attempt.current = {fingerprint, key: crypto.randomUUID()}; setBusy(true); void knowledgeSkills.mount(tenant, openedAgent, skill.id, pin || undefined, attempt.current.key).then(() => {attempt.current = null; setRevision(value => value + 1)}).catch(cause => setError(cause.message)).finally(() => setBusy(false))}}>{t("knowledgeSkillMount")}</Button><Button variant="outline" disabled={busy || !mounted} onClick={() => {if (!skill.id) return; setBusy(true); void knowledgeSkills.unmount(tenant, openedAgent, skill.id, crypto.randomUUID()).then(() => setRevision(value => value + 1)).catch(cause => setError(cause.message)).finally(() => setBusy(false))}}>{t("knowledgeSkillUnmount")}</Button></div></>}</>}
  </div>
}
