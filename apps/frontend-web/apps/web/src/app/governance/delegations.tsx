import { useEffect, useMemo, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
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
import { delegationActions, delegationApi, delegationIsCurrent, delegationPageSearch, scheduledDelegationStart, type Delegation, type DelegationGrant } from "./delegation-api"

export default function DelegationsPage() {
  const {tenantID = ""} = useParams()
  return <Delegations key={tenantID} tenant={tenantID} />
}

function Delegations({tenant}: {tenant: string}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const useLabels = {title: t("delegationUsesTitle"), count: t("delegationUsesCount"), empty: t("delegationUsesEmpty"), recent: t("delegationUsesRecent"), decision: t("delegationUsesDecision")}
  const allowed = useAllowed()
  const api = useMemo(() => delegationApi(tenant), [tenant])
  const [search, setSearch] = useSearchParams()
  const selectedID = search.get("delegation_id") ?? ""
  const project = search.get("project_id") ?? ""
  const state = search.get("state") ?? ""
  const boundaries = search.getAll("before_id")
  const beforeID = boundaries.at(-1) ?? ""
  const [page, setPage] = useState<{items: Delegation[]; total: number; hasMore: boolean} | null>(null)
  const [selected, setSelected] = useState<Delegation | null>(null)
  const [editing, setEditing] = useState<Delegation | "new" | null>(null)
  const [revoking, setRevoking] = useState(false)
  const [proof, setProof] = useState<{input: DelegationGrant; key: string} | null>(null)
  const [busy, setBusy] = useState(false)
  const pending = useRef(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [refresh, setRefresh] = useState(0)
  const [now, setNow] = useState(() => Date.now())
  const read = allowed("policy_read")
  const manage = allowed("delegation_grant")
  useEffect(() => {const timer = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(timer)}, [])
  useEffect(() => {
    if (!read) return
    const controller = new AbortController()
    void Promise.all([api.list({project_id: project || undefined, state: state || undefined, before_id: beforeID || undefined}, controller.signal), selectedID ? api.get(selectedID, controller.signal) : Promise.resolve(null)]).then(([next, detail]) => {
      if (controller.signal.aborted) return
      setPage(next); setSelected(detail); setError(""); setLoading(false)
    }, cause => {if (!controller.signal.aborted) {setPage(null); setSelected(null); setError(cause instanceof Error ? cause.message : t("govUnavailable")); setLoading(false)}})
    return () => controller.abort()
  }, [api, project, state, beforeID, selectedID, refresh, read, t])
  const reload = () => {setLoading(true); setRefresh(value => value + 1)}
  const date = (value: string) => new Date(value).toLocaleString(locale)
  const run = async (action: () => Promise<Delegation>) => {
    if (pending.current || !manage) return
    pending.current = true; setBusy(true); setError(""); setNotice("")
    try {const value = await action(); setEditing(null); setProof(null); setRevoking(false); setNotice(t("govSaved")); setSearch(previous => {const next = new URLSearchParams(previous); next.set("delegation_id", value.id); return next}); reload()} catch (cause) {setError(cause instanceof Error ? cause.message : t("govUnavailable")); throw cause} finally {pending.current = false; setBusy(false)}
  }
  return <div className="flex min-h-0 flex-1 flex-col gap-4">
    <PageHeader title={t("delegationTitle")} description={t("delegationHint")} action={<div className="flex gap-2"><Button variant="outline" disabled={busy} onClick={reload}>{t("refresh")}</Button>{manage && <Button disabled={busy} onClick={() => setEditing("new")}>{t("delegationGrant")}</Button>}</div>} />
    {error && <Alert>{error}</Alert>}{notice && <p role="status">{notice}</p>}
    {!read ? <Alert>{t("govPermissionRequired")}</Alert> : <>
      <AutoForm mode="edit" initialData={{project_id: project, state}} fields={{project_id: {name: t("delegationProject"), type: "text"}, state: {name: t("status"), type: "select", config: {options: [{value: "", label: t("all")}, ...["SCHEDULED", "ACTIVE", "REVOKED", "EXPIRED"].map(value => ({value, label: t(`delegationState_${value}`)}))]}}}} onSubmit={values => {setLoading(true); setSearch(previous => {const next = new URLSearchParams(previous); next.delete("offset"); next.delete("before_id"); next.delete("delegation_id"); for (const key of ["project_id", "state"]) {const value = String(values[key] ?? ""); if (value) next.set(key, value); else next.delete(key)} return next})}} className="md:grid-cols-3"><Button type="submit">{t("delegationFilter")}</Button></AutoForm>
      {loading ? <p role="status">{t("loading")}</p> : page && <AutoTable data={page.items} rowKey="id" pagination={false} exportable={false} emptyContent={t("delegationEmpty")} columns={[{field: "id", name: t("delegationRecord"), width: "210px"}, {field: "project_id", name: t("delegationProject"), width: "180px"}, {field: "grantee_principal_id", name: t("agentPrincipal"), width: "180px"}, {field: "actions", name: t("delegationActions"), width: "240px", format: (_, row) => row.actions.join(", ")}, {field: "state", name: t("status"), width: "160px", format: (_, row) => t(`delegationState_${row.state === "ACTIVE" && !delegationIsCurrent(row, now) ? "EXPIRED" : row.state}`)}, {field: "ends_at", name: t("delegationEnds"), width: "220px", format: value => date(String(value))}]} actions={[{name: t("view"), icon: Eye, onclick: row => {setLoading(true); setSearch(previous => {const next = new URLSearchParams(previous); next.set("delegation_id", row.id); return next})}}]} />}
      <div className="flex gap-2"><Button variant="outline" disabled={!beforeID || loading} onClick={() => {setLoading(true); setSearch(previous => delegationPageSearch(previous))}}>{t("previousPage")}</Button><Button variant="outline" disabled={!page?.hasMore || loading} onClick={() => {const boundary = page?.items.at(-1)?.id; if (!boundary) return; setLoading(true); setSearch(previous => delegationPageSearch(previous, boundary))}}>{t("nextPage")}</Button></div>
      {selected && <section className="grid gap-6 border-t pt-5 lg:grid-cols-[5fr_7fr]" aria-label={t("delegationRecord")}><div className="min-w-0 rounded-md border p-5"><h2 className="mb-3 font-semibold">DELEGATION:{selected.id}</h2><dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-3 text-sm"><dt>{t("agentPrincipal")}</dt><dd className="break-all">{selected.grantee_principal_id}</dd><dt>{t("delegationGrantor")}</dt><dd>{selected.grantor_principal_id}</dd><dt>{t("delegationProject")}</dt><dd>{selected.project_id}</dd><dt>{t("delegationStarts")}</dt><dd>{date(selected.starts_at)}</dd><dt>{t("delegationEnds")}</dt><dd>{date(selected.ends_at)}</dd><dt>{t("govReason")}</dt><dd className="whitespace-pre-wrap break-words">{selected.reason}</dd>{selected.end_note && <><dt>{t("delegationEndNote")}</dt><dd>{selected.end_note}</dd></>}</dl></div><div className="min-w-0 space-y-4 rounded-md border p-5"><h2 className="font-semibold">{t("delegationConditions")}</h2><dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-3 text-sm"><dt>{t("delegationActions")}</dt><dd>{selected.actions.join(", ")}</dd><dt>{t("delegationTypes")}</dt><dd>{selected.work_item_types.join(", ") || t("all")}</dd><dt>{t("delegationRisk")}</dt><dd>{selected.max_risk}</dd><dt>{t("delegationCost")}</dt><dd>{selected.cost_cap_micros.toLocaleString(locale)}</dd><dt>{t("delegationRework")}</dt><dd>&lt; {selected.max_rework}</dd></dl><p className="text-sm text-muted-foreground">{t("delegationFixedConditions")}</p><section className="space-y-3 border-t pt-4"><h3 className="font-semibold">{useLabels.title}</h3><p className="text-sm">{useLabels.count}: {selected.use_count ?? "—"} · {useLabels.recent}</p>{selected.uses?.length ? <ul className="divide-y">{selected.uses.map(use => <li key={use.decision_id} className="space-y-1 py-3 text-sm"><time dateTime={use.at}>{date(use.at)}</time><p><Link className="underline" to={`/admin/${tenant}/my-work?${new URLSearchParams({project: selected.project_id, work: use.work_item_id})}`}>{use.work_item_id}</Link></p><p className="break-all">{useLabels.decision}: {use.decision_id}</p><p>{use.reason}</p></li>)}</ul> : <p className="text-sm text-muted-foreground">{useLabels.empty}</p>}</section>{manage && ["ACTIVE", "SCHEDULED"].includes(selected.state) && <div className="flex flex-wrap gap-2">{selected.state === "ACTIVE" && <Button variant="outline" disabled={busy} onClick={() => setEditing(selected)}>{t("delegationReplace")}</Button>}<Button variant="destructive" disabled={busy} onClick={() => setRevoking(true)}>{t(selected.state === "SCHEDULED" ? "delegationCancel" : "delegationRevoke")}</Button></div>}</div></section>}
    </>}
    {editing && <GrantDialog initial={editing === "new" ? undefined : editing} project={project} busy={busy} onClose={() => setEditing(null)} onSubmit={input => setProof({input, key: crypto.randomUUID()})} />}
    {proof && <ReauthenticationDialog purpose="delegation_grant" target={`project:${proof.input.project_id}:grantee:${proof.input.grantee_principal_id}`} onClose={() => setProof(null)} onVerified={reauth_proof => run(() => api.grant({...proof.input, reauth_proof}, proof.key))} />}
    {revoking && selected && <RevokeDialog busy={busy} onClose={() => setRevoking(false)} onSubmit={reason => run(() => api.revoke(selected.id, reason, crypto.randomUUID()))} />}
  </div>
}

function GrantDialog({initial, project, busy, onClose, onSubmit}: {initial?: Delegation; project: string; busy: boolean; onClose: () => void; onSubmit: (input: DelegationGrant) => void}) {
  const t = useTranslations("iam")
  const [error, setError] = useState("")
  const kinds = ["FEATURE", "BUG", "CHORE", "DOC", "RESEARCH", "DESIGN", "TEST", "TASK", "RISK", "INCIDENT", "CONTENT"]
  return <Dialog open onOpenChange={value => {if (!value && !busy) onClose()}}><DialogContent className="max-h-[90dvh] overflow-y-auto"><DialogHeader><DialogTitle>{t(initial ? "delegationReplace" : "delegationGrant")}</DialogTitle><DialogDescription>{t("delegationGrantHint")}</DialogDescription></DialogHeader>{error && <Alert>{error}</Alert>}{!initial && <p className="text-sm text-muted-foreground">{t("delegationScheduleHint")}</p>}<AutoForm mode="edit" initialData={{project_id: initial?.project_id ?? project, grantee_principal_id: initial?.grantee_principal_id ?? "", actions: initial?.actions ?? ["decision_approve"], work_item_types: initial?.work_item_types ?? [], max_risk: initial?.max_risk ?? "LOW", cost_cap_micros: initial?.cost_cap_micros ?? 1000000, max_rework: initial?.max_rework ?? 3, duration_minutes: 60, starts_at: "", reason: ""}} fields={{project_id: {name: t("delegationProject"), type: "text", required: true, disabled: Boolean(initial)}, grantee_principal_id: {name: t("agentPrincipal"), type: "text", required: true, disabled: Boolean(initial)}, actions: {name: t("delegationActions"), type: "checkbox-group", required: true, config: {options: delegationActions.map(value => ({value, label: value}))}}, work_item_types: {name: t("delegationTypes"), type: "checkbox-group", config: {options: kinds.map(value => ({value, label: value}))}}, max_risk: {name: t("delegationRisk"), type: "select", required: true, config: {options: ["LOW", "MEDIUM"].map(value => ({value, label: value}))}}, cost_cap_micros: {name: t("delegationCost"), type: "number", required: true, config: {min: 1, step: 1}}, max_rework: {name: t("delegationRework"), type: "number", required: true, config: {min: 1, max: 3, step: 1}}, starts_at: {name: t("delegationScheduleStart"), type: "text", disabled: Boolean(initial), config: {type: "datetime-local"}}, duration_minutes: {name: t("delegationDuration"), type: "number", required: true, config: {min: 15, max: 43200, step: 1}}, reason: {name: t("govReason"), type: "textarea", required: true, config: {minLength: 10, maxLength: 2000}}}} onSubmit={values => {try {const starts_at = scheduledDelegationStart(values.starts_at); const input = {...(starts_at ? {starts_at} : {}), project_id: String(values.project_id), grantee_principal_id: String(values.grantee_principal_id), actions: Array.isArray(values.actions) ? values.actions.map(String) : [], work_item_types: Array.isArray(values.work_item_types) ? values.work_item_types.map(String) : [], max_risk: String(values.max_risk) as DelegationGrant["max_risk"], cost_cap_micros: Number(values.cost_cap_micros), max_rework: Number(values.max_rework), duration_minutes: Number(values.duration_minutes), reason: String(values.reason), ...(initial ? {replaces_delegation_id: initial.id} : {})}; if (!/^[1-9][0-9]{0,18}$/.test(input.project_id) || !/^[1-9][0-9]{0,18}$/.test(input.grantee_principal_id) || !Number.isSafeInteger(input.cost_cap_micros) || !input.actions.length || (input.actions.includes("code_merge") && input.max_risk !== "LOW")) throw new Error(t("delegationInvalid")); setError(""); onSubmit(input)} catch (cause) {setError(cause instanceof Error && cause.message !== "Invalid future activation time" ? cause.message : t("delegationInvalid"))}}}><Button disabled={busy} type="submit">{t("continue")}</Button></AutoForm></DialogContent></Dialog>
}

function RevokeDialog({busy, onClose, onSubmit}: {busy: boolean; onClose: () => void; onSubmit: (reason: string) => Promise<void>}) {
  const t = useTranslations("iam")
  const [error, setError] = useState("")
  return <Dialog open onOpenChange={value => {if (!value && !busy) onClose()}}><DialogContent><DialogHeader><DialogTitle>{t("delegationRevoke")}</DialogTitle><DialogDescription>{t("delegationRevokeHint")}</DialogDescription></DialogHeader>{error && <Alert>{error}</Alert>}<AutoForm mode="edit" initialData={{reason: ""}} fields={{reason: {name: t("govReason"), type: "textarea", required: true, config: {minLength: 5, maxLength: 2000}}}} onSubmit={async values => {try {await onSubmit(String(values.reason))} catch (cause) {setError(cause instanceof Error ? cause.message : t("govUnavailable"))}}}><Button type="submit" variant="destructive" disabled={busy}>{t("delegationRevoke")}</Button></AutoForm></DialogContent></Dialog>
}
