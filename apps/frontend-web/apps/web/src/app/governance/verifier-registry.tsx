import { useCallback, useMemo, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { Eye, Pencil } from "lucide-react"
import { AutoForm, type FieldConfig } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@workspace/ui/components/dialog"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAllowed } from "../../lib/permissions"
import { useFacts } from "../runtime/use-facts"
import { verifierFormDefinition, verifierFormValues, verifierRegistryAPI, verifierRegistryMessages, verifierTypes, type Verifier } from "./verifier-registry-data"

export function VerifierRegistry({tenantID}: {tenantID: string}) {
  const t = useTranslations("iam"), locale = useLocale(), allowed = useAllowed()
  const l = verifierRegistryMessages[locale as keyof typeof verifierRegistryMessages] ?? verifierRegistryMessages["en-US"]
  const api = useMemo(() => verifierRegistryAPI(tenantID), [tenantID])
  const [search, setSearch] = useSearchParams()
  const state = search.get("verifier_state") ?? "", risk = search.get("verifier_risk") ?? "", kind = search.get("verifier_type") ?? "", project = search.get("verifier_project") ?? "", cursor = search.get("verifier_cursor") ?? ""
  const load = useCallback((signal: AbortSignal) => allowed("policy_read") ? api.list({...(state ? {state} : {}), ...(risk ? {risk_level: risk} : {}), ...(kind ? {work_item_type: kind} : {}), ...(project ? {project_id: project} : {}), ...(cursor ? {cursor} : {})}, signal) : Promise.reject(new Error(l.readonly)), [api, state, risk, kind, project, cursor, allowed, l.readonly])
  const facts = useFacts(`${tenantID}:${state}:${risk}:${kind}:${project}:${cursor}`, load)
  const [editing, setEditing] = useState<{row: Verifier | null} | null>(null)
  const [history, setHistory] = useState<Verifier | null>(null)
  const canManage = allowed("verifier_manage")
  const go = (nextCursor: string) => setSearch(previous => {const next = new URLSearchParams(previous); if (nextCursor) next.set("verifier_cursor", nextCursor); else next.delete("verifier_cursor"); return next})
  if (!allowed("policy_read")) return <Alert>{l.readonly}</Alert>
  return <section className="grid min-w-0 gap-4 overflow-y-auto" aria-label={l.title}>
    <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="text-lg font-semibold">{l.title}</h2><div className="flex flex-wrap gap-2"><Button variant="outline" disabled={facts.loading} onClick={facts.refresh}>{t("refresh")}</Button>{canManage && <Button onClick={() => setEditing({row: null})}>{l.add}</Button>}</div></div>
    <p className="text-sm text-muted-foreground">{l.hint}</p>
    <AutoForm key={`${state}:${risk}:${kind}:${project}`} mode="edit" className="md:grid-cols-2" initialData={{state: state || "all", risk: risk || "all", kind: kind || "all", project}} fields={{
      state: {name: l.state, type: "select", config: {options: [{value: "all", label: l.all}, ...(["ACTIVE", "ARCHIVED"] as const).map(value => ({value, label: l[value]}))]}},
      risk: {name: l.risks, type: "select", config: {options: [{value: "all", label: l.all}, ...["LOW", "MEDIUM", "HIGH"].map(value => ({value, label: value}))]}},
      kind: {name: l.workTypes, type: "select", config: {options: [{value: "all", label: l.all}, ...verifierTypes.map(value => ({value, label: t(`workType_${value}`)}))]}},
      project: {name: l.projects, type: "text"},
    }} onSubmit={values => setSearch(previous => {const next = new URLSearchParams(previous); for (const [key, value] of Object.entries({verifier_state: values.state, verifier_risk: values.risk, verifier_type: values.kind, verifier_project: values.project})) {const text = String(value ?? "").trim(); if (text && text !== "all") next.set(key, text); else next.delete(key)} next.delete("verifier_cursor"); return next})}><Button type="submit">{t("search")}</Button></AutoForm>
    {facts.error && <Alert>{facts.error}</Alert>}
    <AutoTable data={facts.data?.items ?? []} rowKey="id" loading={facts.loading} pagination={false} exportable={false} emptyContent={l.empty} columns={[
      {field: "name", name: t("name"), width: "220px"}, {field: "key", name: l.key, width: "180px"}, {field: "layer", name: l.layer, width: "150px", format: (_, row) => l[row.layer]}, {field: "state", name: l.state, width: "140px", format: (_, row) => l[row.state]}, {field: "required", name: l.required, width: "170px", format: (_, row) => t(row.required ? "yes" : "no")}, {field: "current_version_no", name: l.version, width: "140px"},
    ]} actions={[{name: l.history, icon: Eye, onclick: setHistory}, ...(canManage ? [{name: t("edit"), icon: Pencil, onclick: (row: Verifier) => setEditing({row})}] : [])]} />
    <div className="flex justify-between gap-3"><Button variant="outline" disabled={!cursor || facts.loading} onClick={() => go("")}>{l.first}</Button><Button variant="outline" disabled={!facts.data?.has_more || facts.loading} onClick={() => go(facts.data?.next_cursor ?? "")}>{l.more}</Button></div>
    {editing && <VerifierEditor key={editing.row?.id ?? "new"} tenantID={tenantID} row={editing.row} onClose={() => setEditing(null)} onSaved={() => {setEditing(null); facts.refresh()}} />}
    {history && <VerifierHistory tenantID={tenantID} row={history} onClose={() => setHistory(null)} />}
  </section>
}

function VerifierEditor({tenantID, row, onClose, onSaved}: {tenantID: string; row: Verifier | null; onClose: () => void; onSaved: () => void}) {
  const t = useTranslations("iam"), locale = useLocale()
  const l = verifierRegistryMessages[locale as keyof typeof verifierRegistryMessages] ?? verifierRegistryMessages["en-US"]
  const [busy, setBusy] = useState(false), [error, setError] = useState("")
  const attempt = useRef<{identity: string; key: string} | null>(null), pending = useRef(false)
  const automated = (values?: Record<string, unknown>) => values?.layer === "AUTOMATED"
  const ai = (values?: Record<string, unknown>) => values?.layer === "AI_ASSISTED"
  const human = (values?: Record<string, unknown>) => values?.layer === "HUMAN"
  const fields: Record<string, FieldConfig> = {
    key: {name: l.key, type: "text", required: true, disabled: busy || !!row}, name: {name: t("name"), type: "text", required: true, disabled: busy},
    layer: {name: l.layer, type: "select", disabled: busy || !!row, config: {options: (["AUTOMATED", "AI_ASSISTED", "HUMAN"] as const).map(value => ({value, label: l[value]}))}},
    state: {name: l.state, type: "select", disabled: busy, config: {options: (["ACTIVE", "ARCHIVED"] as const).map(value => ({value, label: l[value]}))}},
    required: {name: l.required, type: "switch", disabled: busy},
    work_item_types: {name: l.workTypes, type: "checkbox-group", disabled: busy, config: {options: verifierTypes.map(value => ({value, label: t(`workType_${value}`)}))}},
    risk_levels: {name: l.risks, type: "checkbox-group", disabled: busy, config: {options: ["LOW", "MEDIUM", "HIGH"].map(value => ({value, label: value}))}},
    project_ids: {name: l.projects, type: "text", disabled: busy}, timeout_ms: {name: l.timeout, type: "number", required: true, disabled: busy}, change_note: {name: l.note, type: "textarea", disabled: busy},
    argv: {name: l.argv, type: "textarea", show: automated, required: automated, disabled: busy}, workdir: {name: l.workdir, type: "text", show: automated, disabled: busy}, env: {name: l.env, type: "textarea", show: automated, disabled: busy},
    report_format: {name: l.report, type: "select", show: automated, disabled: busy, config: {options: ["NONE", "JUNIT_XML", "GO_TEST_JSON", "TAP"].map(value => ({value, label: value}))}}, report_globs: {name: l.globs, type: "textarea", show: automated, disabled: busy}, stdout_tail_bytes: {name: l.tail, type: "number", show: automated, disabled: busy}, tool_version_argv: {name: l.probe, type: "textarea", show: automated, disabled: busy},
    agent_id: {name: l.agent, type: "text", show: ai, required: ai, disabled: busy}, prompt_template: {name: l.prompt, type: "textarea", show: ai, required: ai, disabled: busy}, max_cost_micros: {name: l.cost, type: "number", show: ai, required: ai, disabled: busy},
    instructions: {name: l.instructions, type: "textarea", show: human, required: human, disabled: busy}, eligible_role_keys: {name: l.roles, type: "text", show: human, required: human, disabled: busy},
  }
  return <Dialog open onOpenChange={value => {if (!value && !busy) onClose()}}><DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-3xl"><DialogHeader><DialogTitle>{row ? l.editing : l.add}</DialogTitle><DialogDescription>{l.hint}</DialogDescription></DialogHeader>{error && <Alert>{error}</Alert>}<AutoForm mode="edit" initialData={verifierFormValues(row)} fields={fields} onSubmit={async values => {
    if (pending.current) return
    pending.current = true; setBusy(true); setError("")
    try {
      let definition
      try {definition = verifierFormDefinition({...verifierFormValues(row), ...values})} catch {throw new Error(l.invalid)}
      const identity = JSON.stringify({definition, id: row?.id, version: row?.version})
      if (attempt.current?.identity !== identity) attempt.current = {identity, key: crypto.randomUUID()}
      await verifierRegistryAPI(tenantID).save(definition, row, attempt.current.key)
      attempt.current = null; onSaved()
    } catch (cause) {setError(cause instanceof Error ? cause.message : l.invalid)} finally {pending.current = false; setBusy(false)}
  }}><div className="flex gap-3"><Button type="submit" disabled={busy}>{busy ? t("loading") : t("save")}</Button><Button type="button" variant="outline" disabled={busy} onClick={onClose}>{t("cancel")}</Button></div></AutoForm></DialogContent></Dialog>
}

function VerifierHistory({tenantID, row, onClose}: {tenantID: string; row: Verifier; onClose: () => void}) {
  const t = useTranslations("iam"), locale = useLocale()
  const l = verifierRegistryMessages[locale as keyof typeof verifierRegistryMessages] ?? verifierRegistryMessages["en-US"]
  const [cursor, setCursor] = useState("")
  const load = useCallback((signal: AbortSignal) => verifierRegistryAPI(tenantID).versions(row.id, cursor, signal), [tenantID, row.id, cursor])
  const facts = useFacts(`${tenantID}:${row.id}:${cursor}`, load)
  return <Dialog open onOpenChange={value => {if (!value) onClose()}}><DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-3xl"><DialogHeader><DialogTitle>{l.history}: {row.name}</DialogTitle><DialogDescription>{l.historyHint}</DialogDescription></DialogHeader>{facts.error && <Alert>{facts.error}</Alert>}{facts.loading && <p role="status">{t("loading")}</p>}<ol className="grid gap-4">{facts.data?.items.map(version => <li key={version.id} className="grid min-w-0 gap-2 border-b pb-4"><h3 className="font-medium">{l.version} {version.version_no}</h3><p className="break-words text-sm">{version.change_note}</p><p className="text-sm">{l.timeout}: {version.timeout_ms} · {new Date(version.created_at).toLocaleString(locale)}</p><pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded border p-3 text-sm">{JSON.stringify(version.spec, null, 2)}</pre></li>)}</ol><div className="flex justify-between gap-3"><Button variant="outline" disabled={!cursor || facts.loading} onClick={() => setCursor("")}>{l.first}</Button><Button variant="outline" disabled={!facts.data?.has_more || facts.loading} onClick={() => setCursor(facts.data?.next_cursor ?? "")}>{l.more}</Button></div></DialogContent></Dialog>
}
