import { useEffect, useMemo, useRef, useState } from "react"
import { useParams, useSearchParams } from "react-router"
import { Eye } from "lucide-react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@workspace/ui/components/dialog"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { useAllowed } from "../../lib/permissions"
import { spawnApi, spawnFormInput, type SpawnGrant, type SpawnGrantInput, type SpawnMember } from "./spawn-api"

export default function SpawnGrantsPage() {
  const {tenantID = ""} = useParams()
  return <SpawnGrants key={tenantID} tenant={tenantID} />
}

function SpawnGrants({tenant}: {tenant: string}) {
  const t = useTranslations("iam"), locale = useLocale(), allowed = useAllowed()
  const api = useMemo(() => spawnApi(tenant), [tenant])
  const [search, setSearch] = useSearchParams()
  const selectedID = search.get("spawn_grant_id") ?? "", state = search.get("state") ?? ""
  const parsed = Number(search.get("offset") ?? "0"), offset = Number.isSafeInteger(parsed) && parsed >= 0 ? parsed : 0
  const memberParsed = Number(search.get("member_offset") ?? "0"), memberOffset = Number.isSafeInteger(memberParsed) && memberParsed >= 0 ? memberParsed : 0
  const [page, setPage] = useState<{items: SpawnGrant[]; total: number} | null>(null)
  const [selected, setSelected] = useState<SpawnGrant | null>(null)
  const [members, setMembers] = useState<SpawnMember[]>([])
  const [memberTotal, setMemberTotal] = useState(0)
  const [editing, setEditing] = useState<SpawnGrant | "new" | "replace" | null>(null)
  const [revoke, setRevoke] = useState(false)
  const revokeKey = useRef(crypto.randomUUID())
  const [proof, setProof] = useState<{input: SpawnGrantInput; previous: SpawnGrant | null; key: string} | null>(null)
  const [busy, setBusy] = useState(false), pending = useRef(false)
  const [loading, setLoading] = useState(true), [error, setError] = useState(""), [notice, setNotice] = useState("")
  const [refresh, setRefresh] = useState(0)
  const read = allowed("policy_read"), manage = allowed("spawn_grant_manage")
  const reload = () => {setLoading(true); setRefresh(value => value + 1)}
  useEffect(() => {
    if (!read) return
    const controller = new AbortController()
    void Promise.all([api.list(state, offset, controller.signal), selectedID ? api.get(selectedID, controller.signal) : Promise.resolve(null), selectedID ? api.members(selectedID, memberOffset, controller.signal) : Promise.resolve({items: [], total: 0})]).then(([list, detail, children]) => {
      if (controller.signal.aborted) return
      setPage(list); setSelected(detail); setMembers(children.items); setMemberTotal(children.total); setLoading(false); setError("")
    }, cause => {if (!controller.signal.aborted) {setPage(null); setSelected(null); setMembers([]); setLoading(false); setError(cause instanceof Error ? cause.message : t("govUnavailable"))}})
    return () => controller.abort()
  }, [api, state, offset, memberOffset, selectedID, refresh, read, t])
  const run = async (command: () => Promise<SpawnGrant>) => {
    if (pending.current || !manage) return
    pending.current = true; setBusy(true); setError(""); setNotice("")
    try {const value = await command(); setProof(null); setEditing(null); setRevoke(false); setNotice(t("govSaved")); setSearch(previous => {const next = new URLSearchParams(previous); next.set("spawn_grant_id", value.id); return next}); reload()} catch (cause) {setError(cause instanceof Error ? cause.message : t("govUnavailable")); throw cause} finally {pending.current = false; setBusy(false)}
  }
  const date = (value: string) => new Date(value).toLocaleString(locale)
  const scope = selected?.project_ids.length ? selected.project_ids.join(", ") : t("spawnTeam")
  return <div className="flex min-h-0 flex-1 flex-col gap-5">
    <PageHeader title={t("spawnTitle")} description={t("spawnHint")} action={<div className="flex gap-2"><Button variant="outline" disabled={busy} onClick={reload}>{t("refresh")}</Button>{manage && <Button disabled={busy} onClick={() => setEditing("new")}>{t("spawnGrant")}</Button>}</div>} />
    {error && <Alert>{error}</Alert>}{notice && <p role="status">{notice}</p>}
    {!read ? <Alert>{t("govPermissionRequired")}</Alert> : <>
      <AutoForm mode="edit" initialData={{state}} fields={{state: {name: t("status"), type: "select", config: {options: [{value: "", label: t("all")}, ...["ACTIVE", "EXHAUSTED", "EXPIRED", "REVOKED"].map(value => ({value, label: t(`spawnState_${value}`)}))]}}}} onSubmit={values => {setLoading(true); setSearch(previous => {const next = new URLSearchParams(previous); next.delete("offset"); const state = String(values.state ?? ""); if (state) next.set("state", state); else next.delete("state"); return next})}}><Button type="submit">{t("delegationFilter")}</Button></AutoForm>
      {loading ? <p role="status">{t("loading")}</p> : page && <AutoTable data={page.items} rowKey="id" pagination={false} exportable={false} columns={[{field: "id", name: t("spawnRecord"), width: "200px"}, {field: "grantee_agent_id", name: t("spawnSupervisor"), width: "180px"}, {field: "effective_state", name: t("status"), width: "140px", format: value => t(`spawnState_${value}`)}, {field: "active_count", name: t("spawnActive"), width: "140px", format: (_, row) => `${row.active_count} / ${row.max_active_agents}`}, {field: "spawned_today", name: t("spawnDaily"), width: "140px", format: (_, row) => `${row.spawned_today} / ${row.max_spawns_per_day}`}, {field: "ends_at", name: t("delegationEnds"), width: "220px", format: value => date(String(value))}]} actions={[{name: t("view"), icon: Eye, onclick: row => {setLoading(true); setSearch(previous => {const next = new URLSearchParams(previous); next.set("spawn_grant_id", row.id); return next})}}]} />}
      <div className="flex gap-2"><Button variant="outline" disabled={!offset || loading} onClick={() => {setLoading(true); setSearch(previous => {const next = new URLSearchParams(previous); next.set("offset", String(Math.max(0, offset - 50))); return next})}}>{t("previousPage")}</Button><Button variant="outline" disabled={!page || offset + page.items.length >= page.total || loading} onClick={() => {setLoading(true); setSearch(previous => {const next = new URLSearchParams(previous); next.set("offset", String(offset + 50)); return next})}}>{t("nextPage")}</Button></div>
      {selected && <section className="grid gap-8 border-t pt-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]" aria-label={t("spawnRecord")}>
        <div className="min-w-0 space-y-6"><h2 className="break-all font-semibold">{t("spawnRecord")} {selected.id}</h2><dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-3 text-sm"><dt>{t("spawnSupervisor")}</dt><dd className="break-all">{selected.grantee_agent_id}</dd><dt>{t("spawnScope")}</dt><dd className="break-all">{scope}</dd><dt>{t("status")}</dt><dd>{t(`spawnState_${selected.effective_state}`)}</dd><dt>{t("spawnActive")}</dt><dd>{selected.active_count} / {selected.max_active_agents}</dd><dt>{t("spawnDaily")}</dt><dd>{selected.spawned_today} / {selected.max_spawns_per_day}</dd><dt>{t("spawnBudget")}</dt><dd className="break-all">{selected.spent_micros} / {selected.budget_cap_micros}</dd><dt>{t("delegationEnds")}</dt><dd>{date(selected.ends_at)}</dd></dl></div>
        <div className="min-w-0 space-y-6"><h2 className="font-semibold">{t("spawnBounds")}</h2><dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-3 text-sm"><dt>{t("spawnRoles")}</dt><dd>{selected.allowed_roles.join(", ")}</dd><dt>{t("spawnMachines")}</dt><dd className="break-all">{selected.allowed_machine_ids.join(", ")}</dd><dt>{t("spawnModels")}</dt><dd className="break-all">{selected.allowed_models.map(value => `${value.runtime} · ${value.model}`).join(", ")}</dd><dt>{t("spawnEffort")}</dt><dd>{selected.max_effort}</dd><dt>{t("spawnPerAgent")}</dt><dd>{selected.per_agent_cap_micros}</dd><dt>{t("spawnTTL")}</dt><dd>{selected.agent_ttl_hours}</dd><dt>{t("spawnIdle")}</dt><dd>{selected.idle_retire_hours}</dd><dt>{t("spawnApproval")}</dt><dd>{t(`spawnApproval_${selected.effective_approval_mode}`)}</dd><dt>{t("govReason")}</dt><dd className="whitespace-pre-wrap break-words">{selected.reason}</dd></dl>
          <h3 className="font-semibold">{t("spawnMembers")}</h3><AutoTable data={members} rowKey="agent_id" pagination={false} exportable={false} emptyContent={t("spawnNoMembers")} columns={[{field: "agent_id", name: t("spawnAgent"), width: "200px"}, {field: "state", name: t("status"), width: "150px", format: value => t(`spawnMember_${value}`)}, {field: "spent_micros", name: t("spawnBudget"), width: "200px", format: (_, row) => `${row.spent_micros} / ${row.per_agent_cap_micros}`}]} />
          <div className="flex gap-2" aria-label={t("spawnMembers")}><Button variant="outline" disabled={memberOffset === 0 || loading} onClick={() => {setLoading(true); setSearch(previous => {const next = new URLSearchParams(previous); next.set("member_offset", String(Math.max(0, memberOffset - 50))); return next})}}>{t("previousPage")}</Button><Button variant="outline" disabled={memberOffset + members.length >= memberTotal || loading} onClick={() => {setLoading(true); setSearch(previous => {const next = new URLSearchParams(previous); next.set("member_offset", String(memberOffset + 50)); return next})}}>{t("nextPage")}</Button></div>
          {manage && ["ACTIVE", "EXHAUSTED"].includes(selected.effective_state) && <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={busy} onClick={() => setEditing(selected)}>{t("spawnEdit")}</Button><Button variant="outline" disabled={busy} onClick={() => setEditing("replace")}>{t("spawnReplace")}</Button><Button variant="destructive" disabled={busy} onClick={() => {revokeKey.current = crypto.randomUUID(); setRevoke(true)}}>{t("spawnRevoke")}</Button></div>}
        </div>
      </section>}
    </>}
    {editing && <SpawnForm initial={editing === "new" ? null : editing === "replace" ? selected : editing} replacement={editing === "replace"} busy={busy} onClose={() => setEditing(null)} onSubmit={input => setProof({input: editing === "replace" && selected ? {...input, replaces_grant_id: selected.id} : input, previous: typeof editing === "object" ? editing : null, key: crypto.randomUUID()})} />}
    {proof && <ReauthenticationDialog purpose="spawn_grant_manage" target={`agent:${proof.input.grantee_agent_id}`} onClose={() => setProof(null)} onVerified={reauth_proof => run(() => proof.previous ? api.update(proof.previous, {...proof.input, reauth_proof}) : api.grant({...proof.input, reauth_proof}, proof.key))} />}
    {revoke && selected && <SpawnRevoke busy={busy} onClose={() => setRevoke(false)} onSubmit={reason => run(() => api.revoke(selected, reason, revokeKey.current))} />}
  </div>
}

function SpawnForm({initial, replacement, busy, onClose, onSubmit}: {initial: SpawnGrant | null; replacement: boolean; busy: boolean; onClose: () => void; onSubmit: (value: SpawnGrantInput) => void}) {
  const t = useTranslations("iam"), [error, setError] = useState("")
  return <Dialog open onOpenChange={value => {if (!value && !busy) onClose()}}><DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-3xl"><DialogHeader><DialogTitle>{t(replacement ? "spawnReplace" : initial ? "spawnEdit" : "spawnGrant")}</DialogTitle><DialogDescription>{t("spawnFormHint")}</DialogDescription></DialogHeader>{error && <Alert>{error}</Alert>}
    <AutoForm mode="edit" initialData={{supervisor: initial?.grantee_agent_id ?? "", projects: initial?.project_ids.join(", ") ?? "", machines: initial?.allowed_machine_ids.join(", ") ?? "", codex_models: initial?.allowed_models.filter(value => value.runtime === "CODEX").map(value => value.model).join(", ") ?? "", claude_models: initial?.allowed_models.filter(value => value.runtime === "CLAUDE_CODE").map(value => value.model).join(", ") ?? "", roles: initial?.allowed_roles ?? ["EXECUTOR"], active: initial?.max_active_agents ?? 3, daily: initial?.max_spawns_per_day ?? 5, budget: initial?.budget_cap_micros ?? 50000000, per_agent: initial?.per_agent_cap_micros ?? 15000000, effort: initial?.max_effort ?? "medium", ttl: initial?.agent_ttl_hours ?? 168, idle: initial?.idle_retire_hours ?? 48, approval: initial?.approval_mode ?? "REQUIRE_APPROVAL", duration: 168, reason: "", team_confirm: false}} fields={{supervisor: {name: t("spawnSupervisor"), type: "text", required: true, disabled: Boolean(initial)}, projects: {name: t("spawnProjectIDs"), type: "text"}, team_confirm: {name: t("spawnTeamConfirm"), type: "checkbox"}, roles: {name: t("spawnRoles"), type: "checkbox-group", required: true, config: {options: ["EXECUTOR", "COORDINATOR"].map(value => ({value, label: value}))}}, machines: {name: t("spawnMachines"), type: "text", required: true}, codex_models: {name: t("spawnCodexModels"), type: "text"}, claude_models: {name: t("spawnClaudeModels"), type: "text"}, active: {name: t("spawnActive"), type: "number", required: true, config: {min: 1, max: 10}}, daily: {name: t("spawnDaily"), type: "number", required: true, config: {min: 1, max: 20}}, budget: {name: t("spawnBudget"), type: "number", required: true, config: {min: 1000000, step: 1}}, per_agent: {name: t("spawnPerAgent"), type: "number", required: true, config: {min: 1, step: 1}}, effort: {name: t("spawnEffort"), type: "select", config: {options: ["minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent"].map(value => ({value, label: value}))}}, ttl: {name: t("spawnTTL"), type: "number", required: true, config: {min: 24, max: 720}}, idle: {name: t("spawnIdle"), type: "number", required: true, config: {min: 4, max: 336}}, approval: {name: t("spawnApproval"), type: "select", required: true, config: {options: ["REQUIRE_APPROVAL", "AUTO"].map(value => ({value, label: t(`spawnApproval_${value}`)}))}}, duration: {name: t("spawnDuration"), type: "number", required: true, show: replacement || !initial, config: {min: 2, max: 2160}}, reason: {name: t("govReason"), type: "textarea", required: true, config: {minLength: 10, maxLength: 2000}}}} onSubmit={values => {try {const input = spawnFormInput(values); setError(""); onSubmit(input)} catch {setError(t("spawnInvalid"))}}} className="md:grid-cols-2"><Button type="submit" disabled={busy}>{t("continue")}</Button></AutoForm>
  </DialogContent></Dialog>
}

function SpawnRevoke({busy, onClose, onSubmit}: {busy: boolean; onClose: () => void; onSubmit: (reason: string) => Promise<void>}) {
  const t = useTranslations("iam"), [error, setError] = useState("")
  return <Dialog open onOpenChange={value => {if (!value && !busy) onClose()}}><DialogContent><DialogHeader><DialogTitle>{t("spawnRevoke")}</DialogTitle><DialogDescription>{t("spawnRevokeHint")}</DialogDescription></DialogHeader>{error && <Alert>{error}</Alert>}<AutoForm mode="edit" initialData={{reason: ""}} fields={{reason: {name: t("govReason"), type: "textarea", required: true, config: {minLength: 10, maxLength: 2000}}}} onSubmit={async values => {try {await onSubmit(String(values.reason))} catch (cause) {setError(cause instanceof Error ? cause.message : t("govUnavailable"))}}}><Button disabled={busy} type="submit" variant="destructive">{t("spawnRevoke")}</Button></AutoForm></DialogContent></Dialog>
}
