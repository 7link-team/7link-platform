import { useEffect, useMemo, useRef, useState } from "react"
import { useParams, useSearchParams, Link } from "react-router"
import { Eye, Pencil } from "lucide-react"
import { AutoForm, type FieldConfig } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@workspace/ui/components/dialog"
import { Tabs, TabsList, TabsTrigger } from "@workspace/ui/components/tabs"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { useAllowed } from "../../lib/permissions"
import AuditEventsPage from "../audit-events/page"
import { BusinessAudit } from "./business-audit"
import { VerifierRegistry } from "./verifier-registry"
import { verifierRegistryMessages } from "./verifier-registry-data"
import { governanceApi, ruleNeedsStepUp, type EscalationRule, type GovernanceCatalog, type Incident, type IncidentPage, type SettingsValue, type SettingsView } from "./api"

export default function GovernancePoliciesPage() {
  const {tenantID = ""} = useParams()
  return <Policies key={tenantID} tenantID={tenantID} />
}

function Policies({tenantID}: {tenantID: string}) {
  const locale = useLocale()
  const verifierLabels = verifierRegistryMessages[locale as keyof typeof verifierRegistryMessages] ?? verifierRegistryMessages["en-US"]
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const api = useMemo(() => governanceApi(tenantID), [tenantID])
  const [search, setSearch] = useSearchParams()
  const rawTab = search.get("tab") ?? "policies"
  const tab = ["policies", "incidents", "settings", "audit", "verifiers"].includes(rawTab) ? rawTab : "policies"
  const cursor = search.get("cursor") ?? ""
  const project = search.get("project_id") ?? ""
  const [catalog, setCatalog] = useState<GovernanceCatalog | null>(null)
  const [settings, setSettings] = useState<SettingsView | null>(null)
  const [rules, setRules] = useState<EscalationRule[]>([])
  const [incidents, setIncidents] = useState<IncidentPage | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [refresh, setRefresh] = useState(0)
  const [editing, setEditing] = useState<EscalationRule | null>(null)
  const [selected, setSelected] = useState<Incident | null>(null)
  const [busy, setBusy] = useState(false)
  const pending = useRef(false)
  const [stepUp, setStepUp] = useState<{purpose: string; target: string; run: (proof: string) => Promise<void>} | null>(null)
  useEffect(() => {
    if (tab === "audit" || tab === "verifiers" || !allowed("policy_read")) return
    const controller = new AbortController()
    const load = async () => {
      try {
        if (tab === "policies") {const [data, ruleData] = await Promise.all([api.catalog(controller.signal), api.rules(controller.signal, project)]); if (!controller.signal.aborted) {setCatalog(data); setRules(ruleData)}}
        if (tab === "settings") {const data = await api.settings(controller.signal); if (!controller.signal.aborted) setSettings(data)}
        if (tab === "incidents") {const data = await api.incidents(cursor, controller.signal); if (!controller.signal.aborted) setIncidents(data)}
        if (!controller.signal.aborted) {setLoading(false); setError("")}
      } catch (cause) {if (!controller.signal.aborted) {setError(cause instanceof Error ? cause.message : t("govUnavailable")); setLoading(false); setCatalog(null); setSettings(null); setRules([]); setIncidents(null)}}
    }
    void load()
    return () => controller.abort()
  }, [api, tab, cursor, project, refresh, allowed, t])
  const reload = () => {setLoading(true); setRefresh(value => value + 1)}
  const run = async (action: () => Promise<unknown>) => {
    if (pending.current) return
    if (!allowed("policy_manage")) throw new Error(t("govPermissionRequired"))
    pending.current = true; setBusy(true); setError(""); setNotice("")
    try {await action(); setStepUp(null); setEditing(null); setNotice(t("govSaved")); reload()} catch (cause) {const message = cause instanceof Error ? cause.message : t("govUnavailable"); setError(message); throw cause} finally {pending.current = false; setBusy(false)}
  }
  const saveSettings = async (values: Record<string, unknown>) => {
    if (!settings || !allowed("policy_manage")) return
    const next: SettingsValue = {delegation_max_days: Number(values.delegation_max_days), delegation_default_days: Number(values.delegation_default_days), delegation_max_cost_cap_micros: Number(values.delegation_max_cost_cap_micros), delegation_default_cost_cap_micros: Number(values.delegation_default_cost_cap_micros), default_budget_notify_pct: Number(values.default_budget_notify_pct), production_access_max_hours: Number(values.production_access_max_hours), sod_human_self_approval: String(values.sod_human_self_approval) as SettingsValue["sod_human_self_approval"], inbox_review_routing: String(values.inbox_review_routing) as SettingsValue["inbox_review_routing"]}
    const save = (proof?: string) => run(() => api.saveSettings(settings.version, next, proof))
    if (settings.sod_human_self_approval !== "ALLOW_WITH_REASON" && next.sod_human_self_approval === "ALLOW_WITH_REASON") setStepUp({purpose: "governance_sod_relax", target: `${tenantID}:governance-settings:${settings.version}`, run: save})
    else await save()
  }
  const fields: Record<string, FieldConfig> = Object.fromEntries(["delegation_max_days", "delegation_default_days", "delegation_max_cost_cap_micros", "delegation_default_cost_cap_micros", "default_budget_notify_pct", "production_access_max_hours"].map(key => [key, {name: t(`govSetting_${key}`), type: "number", required: true, disabled: busy || !allowed("policy_manage"), config: {step: 1}}]))
  fields.sod_human_self_approval = {name: t("govSetting_sod_human_self_approval"), type: "select", disabled: busy || !allowed("policy_manage"), config: {options: ["DENY", "ALLOW_WITH_REASON"].map(value => ({value, label: t(`govSOD_${value}`)}))}}
  fields.inbox_review_routing = {name: t("govSetting_inbox_review_routing"), type: "select", disabled: busy || !allowed("policy_manage"), config: {options: ["REVIEWER_ELSE_ELIGIBLE", "ALL_ELIGIBLE"].map(value => ({value, label: t(`govRouting_${value}`)}))}}
  return <div className="flex h-full min-h-0 flex-col">
    <PageHeader title={t("govPoliciesTitle")} description={t("govPoliciesHint")} action={<div className="flex flex-wrap gap-2"><Button variant="outline" asChild><Link to={`/admin/${tenantID}/governance/delegations`}>{t("delegationTitle")}</Link></Button><Button variant="outline" asChild><Link to={`/admin/${tenantID}/governance/spawn-grants`}>{t("spawnTitle")}</Link></Button><Button variant="outline" onClick={reload} disabled={loading || busy}>{t("refresh")}</Button></div>} />
    <Tabs value={tab} onValueChange={value => {setLoading(true); setError(""); setNotice(""); setEditing(null); setSearch({tab: value, ...(project ? {project_id: project} : {})})}}><TabsList className="mb-4 flex-wrap">{["policies", "incidents", "settings", "audit", "verifiers"].map(value => <TabsTrigger key={value} value={value}>{value === "verifiers" ? verifierLabels.title : t(`govTab_${value}`)}</TabsTrigger>)}</TabsList></Tabs>
    {tab === "policies" && allowed("policy_read") && <div className="mb-4 space-y-2"><AutoForm key={project} mode="edit" initialData={{project_id: project}} fields={{project_id: {name: t("govRuleProject"), type: "text", config: {placeholder: t("govRuleTenantDefaults")}}}} onSubmit={values => {const value = String(values.project_id ?? "").trim(); if (value && !/^[1-9][0-9]{0,18}$/.test(value)) {setError(t("delegationInvalid")); return} setLoading(true); setError(""); setNotice(""); setEditing(null); setStepUp(null); setRefresh(current => current+1); setSearch({tab, ...(value ? {project_id: value} : {})})}}><Button type="submit" variant="outline" disabled={busy}>{t("govRuleApplyScope")}</Button></AutoForm><p className="text-sm text-muted-foreground">{project ? t("govRuleProjectHint", {project}) : t("govRuleTenantDefaults")}</p></div>}
    {error && <Alert>{error}</Alert>}{notice && <p role="status" className="mb-3 text-sm">{notice}</p>}
    {tab === "verifiers" ? <VerifierRegistry key={tenantID} tenantID={tenantID} /> : tab === "audit" ? allowed("audit_read") ? <BusinessAudit tenantID={tenantID} /> : allowed("audit_event_view") ? <AuditEventsPage /> : <Alert>{t("govAuditPermission")}</Alert> : !allowed("policy_read") ? <Alert>{t("govPermissionRequired")}</Alert> : loading ? <p role="status">{t("loading")}</p> : <>
      {tab === "settings" && settings && <section className="min-h-0 flex-1 overflow-y-auto"><p className="mb-3 text-sm text-muted-foreground">{t("govSettingsHint")}</p><AutoForm key={settings.version} mode="edit" initialData={settings} fields={fields} className="md:grid-cols-2" onSubmit={saveSettings}>{allowed("policy_manage") && <Button type="submit" disabled={busy}>{t("save")}</Button>}</AutoForm></section>}
      {tab === "policies" && catalog && <section className="grid min-h-0 min-w-0 flex-1 gap-6 overflow-y-auto lg:grid-cols-12">
        <p className="text-sm text-muted-foreground lg:col-span-12">{t("govOverrideUnavailable")}</p>
        <div className="min-w-0 rounded-md border p-4 lg:col-span-12"><h2 className="my-3 font-medium">{t("govActionConstraints")}</h2><AutoTable data={catalog.actions} rowKey="code" pagination={false} exportable={false} columns={[{field: "code", name: t("govPermissionCodes"), width: "260px"}, {field: "human_only", name: t("govHumanOnly"), width: "150px", format: (_, row) => t(row.human_only ? "yes" : "no")}, {field: "non_delegable", name: t("govNonDelegable"), width: "150px", format: (_, row) => t(row.non_delegable ? "yes" : "no")}]} /></div>
        <div className="min-w-0 rounded-md border p-4 lg:col-span-7"><h2 className="my-3 font-medium">{t("govRegisteredPolicies")}</h2><AutoTable data={catalog.policies} rowKey="id" pagination={false} exportable={false} emptyContent={t("govNoPolicies")} columns={[{field: "id", name: t("govPolicyID"), width: "220px"}, {field: "codes", name: t("govPermissionCodes"), width: "400px", format: (_, row) => row.codes.join(", ")}]} /></div>
        <div className="min-w-0 rounded-md border p-4 lg:col-span-5"><h2 className="mb-2 font-medium">{t("govEscalationRules")}</h2><AutoTable data={rules} rowKey="kind" pagination={false} exportable={false} columns={[{field: "kind", name: t("govKind"), width: "240px"}, {field: "enabled", name: t("govEnabled"), width: "120px", format: (_, row) => t(row.enabled ? "yes" : "no")}, {field: "locked", name: t("govMandatory"), width: "150px", format: (_, row) => t(row.locked ? "yes" : "no")}, {field: "actions_autonomous", name: t("govAutonomousActions"), width: "260px", format: (_, row) => row.actions_autonomous.map(value => t(`govAction_${value}`)).join(", ")}]} actions={allowed("policy_manage") ? [{name: t("edit"), icon: Pencil, onclick: setEditing}] : []} /></div>
      </section>}
      {tab === "incidents" && <><p className="mb-3 text-sm text-muted-foreground">{t("govIncidentHint")}</p><AutoTable fill data={incidents?.items ?? []} rowKey="id" pagination={false} exportable={false} emptyContent={t("govIncidentsEmpty")} columns={[{field: "kind", name: t("govKind"), width: "220px"}, {field: "reason", name: t("govReason"), width: "320px"}, {field: "state", name: t("status"), width: "170px", format: (_, row) => t(`govState_${row.state}`)}, {field: "handled_by", name: t("govHandledBy"), width: "180px"}]} actions={[{name: t("details"), icon: Eye, onclick: setSelected}]} /><div className="mt-3 flex justify-between gap-3"><Button variant="outline" disabled={!cursor || loading} onClick={() => {setLoading(true); setSearch({tab})}}>{t("govFirstPage")}</Button><Button variant="outline" disabled={!incidents?.has_more || loading} onClick={() => {if (incidents?.next_cursor) {setLoading(true); setSearch({tab, cursor: incidents.next_cursor})}}}>{t("govNextPage")}</Button></div></>}
    </>}
    {editing && <RuleEditor rule={editing} busy={busy} onClose={() => {if (!busy) setEditing(null)}} onSave={async next => {const save = (proof?: string) => run(() => api.saveRule(next, proof, project)); if (ruleNeedsStepUp(editing, next, Boolean(project))) setStepUp({purpose: "escalation_rule_relax", target: `${tenantID}${project ? `:project:${project}` : ""}:escalation-rule:${next.kind}:${next.version}`, run: save}); else await save()}} />}
    {selected && <IncidentDialog incident={selected} canManage={allowed("policy_manage")} onClose={() => setSelected(null)} onDecide={async (action, reason) => {await api.decideIncident(selected, action, reason); setSelected(null); reload()}} />}
    {stepUp && <ReauthenticationDialog purpose={stepUp.purpose} target={stepUp.target} onClose={() => {if (!busy) setStepUp(null)}} onVerified={stepUp.run} />}
  </div>
}

function RuleEditor({rule, busy, onClose, onSave}: {rule: EscalationRule; busy: boolean; onClose: () => void; onSave: (rule: EscalationRule) => Promise<void>}) {
  const t = useTranslations("iam")
  const [error, setError] = useState("")
  return <Dialog open onOpenChange={value => {if (!value && !busy) onClose()}}><DialogContent className="max-h-[90dvh] overflow-y-auto"><DialogHeader><DialogTitle>{rule.kind}</DialogTitle><DialogDescription>{t("govRuleHint")}</DialogDescription></DialogHeader>{error && <Alert>{error}</Alert>}<AutoForm mode="edit" initialData={{...rule, params: JSON.stringify(rule.params, null, 2)}} fields={{enabled: {name: t("govEnabled"), type: "switch", disabled: busy || rule.locked}, params: {name: t("govRuleParams"), type: "textarea", disabled: busy, config: {rows: 6}}, actions_autonomous: {name: t("govAutonomousActions"), type: "checkbox-group", disabled: busy, config: {options: ["NOTIFY", "HOLD_DELEGATIONS", "PROJECT_HOLD"].map(value => ({value, label: t(`govAction_${value}`)}))}}, actions_hybrid: {name: t("govHybridActions"), type: "checkbox-group", disabled: busy, config: {options: ["NOTIFY", "HOLD_DELEGATIONS", "PROJECT_HOLD"].map(value => ({value, label: t(`govAction_${value}`)}))}}}} onSubmit={async values => {try {const params = JSON.parse(String(values.params)); if (!params || typeof params !== "object" || Array.isArray(params)) throw new Error(t("govInvalidParams")); await onSave({...rule, enabled: Boolean(values.enabled), params, actions_autonomous: Array.isArray(values.actions_autonomous) ? values.actions_autonomous.map(String) : [], actions_hybrid: Array.isArray(values.actions_hybrid) ? values.actions_hybrid.map(String) : []})} catch (cause) {setError(cause instanceof Error ? cause.message : t("govUnavailable"))}}}><Button type="submit" disabled={busy}>{t("save")}</Button></AutoForm></DialogContent></Dialog>
}

function IncidentDialog({incident, canManage, onClose, onDecide}: {incident: Incident; canManage: boolean; onClose: () => void; onDecide: (action: "acknowledgements" | "dismissals", reason?: string) => Promise<void>}) {
  const t = useTranslations("iam")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  return <Dialog open onOpenChange={value => {if (!value && !busy) onClose()}}><DialogContent className="max-h-[90dvh] overflow-y-auto"><DialogHeader><DialogTitle>{incident.kind}</DialogTitle><DialogDescription>{incident.reason}</DialogDescription></DialogHeader>{error && <Alert>{error}</Alert>}<dl className="grid grid-cols-[auto_1fr] gap-2 text-sm"><dt>{t("govSubject")}</dt><dd>{incident.subject_kind} {incident.subject_id}</dd><dt>{t("govSourceEvent")}</dt><dd>{incident.trigger_event_id}</dd><dt>{t("status")}</dt><dd>{t(`govState_${incident.state}`)}</dd><dt>{t("govHandledBy")}</dt><dd>{incident.handled_by ?? "—"}</dd></dl><pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md border p-3 text-xs">{JSON.stringify(incident.facts, null, 2)}</pre>{["OPEN", "ACKNOWLEDGED"].includes(incident.state) && <AutoForm mode="edit" initialData={{action: incident.state === "OPEN" ? "acknowledgements" : "dismissals", reason: ""}} fields={{action: {name: t("govDisposition"), type: "select", disabled: busy, config: {options: [...(incident.state === "OPEN" ? [{value: "acknowledgements", label: t("govAcknowledge")}] : []), ...(canManage ? [{value: "dismissals", label: t("govDismiss")}] : [])]}}, reason: {name: t("govReason"), type: "textarea", disabled: busy, show: values => values?.action === "dismissals", required: values => values?.action === "dismissals", config: {minLength: 10, maxLength: 2000}}}} onSubmit={async values => {setBusy(true); setError(""); try {await onDecide(String(values.action) === "dismissals" ? "dismissals" : "acknowledgements", String(values.reason ?? ""))} catch (cause) {setError(cause instanceof Error ? cause.message : t("govUnavailable"))} finally {setBusy(false)}}}><Button type="submit" disabled={busy || (incident.state !== "OPEN" && !canManage)}>{t("submit")}</Button></AutoForm>}</DialogContent></Dialog>
}
