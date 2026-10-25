import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

type Rule = { id: string; project_id: string; name: string; version: number; enabled: boolean; archived: boolean; definition?: unknown; run_as?: string }
type Preview = { rule_id: string; matches: boolean; reason: string; actions: unknown[]; executed: boolean }
type Run = { id: string; rule_name: string; rule_version: number; work_item_id: string; outcome: string; error?: string; definition_snapshot: string; run_as?: string; actions_result?: string }
type Catalog = { rules: { items: Rule[]; next_cursor?: string; has_more: boolean }; previews: Preview[] }
const initialDefinition = JSON.stringify({ trigger: { event_type: "work_item.state_changed", filters: {} }, condition: true, actions: [{ kind: "NOTIFY", parameters: { recipients_rule: "OWNER" } }], max_runs_per_hour: 60 }, null, 2)
export function AutomationRules({ tenant, project }: { tenant: string; project: string }) {
  const t = useTranslations("iam"), allowed = useAllowed()
  const [catalog, setCatalog] = useState<Catalog | null>(null)
  const [selected, setSelected] = useState<Rule | null>(null)
  const [runRule, setRunRule] = useState<Rule | null>(null)
  const [name, setName] = useState("")
  const [definition, setDefinition] = useState(initialDefinition)
  const [enabled, setEnabled] = useState(true)
  const [event, setEvent] = useState("")
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [runCursor, setRunCursor] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const pending = useRef(false)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const base = `/tenants/${encodeURIComponent(tenant)}/automation-rules`
  const query = `project_id=${encodeURIComponent(project)}&limit=50`
  const canRead = allowed("work_item_read"), canManage = allowed("workflow_manage")
  useEffect(() => {
    const controller = new AbortController()
    if (canRead) void iamApi.deliveryResultRequest<Catalog>({ method: "GET", url: `${base}?${query}` }, { signal: controller.signal })
      .then(value => { if (!controller.signal.aborted) setCatalog(value) })
      .catch(e => { if (!controller.signal.aborted) setError(String(e)) })
    return () => controller.abort()
  }, [base, query, canRead])
  const perform = async (kind: "SAVE" | "ARCHIVE" | "PREVIEW" | "RUNS" | "NEXT" | "RUN_NEXT", rule = selected) => {
    if (pending.current) return
    pending.current = true; setBusy(true); setError("")
    try {
      if (kind === "PREVIEW" || kind === "NEXT") {
        const value = await iamApi.deliveryResultRequest<Catalog>({ method: "GET", url: `${base}?${query}${kind === "PREVIEW" ? `&test_event_id=${encodeURIComponent(event)}` : `&cursor=${encodeURIComponent(catalog?.rules.next_cursor ?? "")}`}` })
        setCatalog(value)
      } else if (kind === "RUNS" || kind === "RUN_NEXT") {
        if (!rule) return
        const value = await iamApi.deliveryResultRequest<{ items: Run[]; next_cursor?: string }>({ method: "GET", url: `${base}/${encodeURIComponent(rule.id)}/runs?${query}${kind === "RUN_NEXT" ? `&cursor=${encodeURIComponent(runCursor)}` : ""}` })
        setRunRule(rule); setRuns(value.items); setRunCursor(value.next_cursor ?? "")
      } else {
        if (kind === "ARCHIVE" && !rule) return
        const body = kind === "ARCHIVE" ? "" : JSON.stringify({ name, enabled, definition: JSON.parse(definition) as unknown })
        const identity = `${kind}:${rule?.id ?? "new"}:${rule?.version ?? 0}:${body}`
        if (attempt.current?.body !== identity) attempt.current = { body: identity, key: crypto.randomUUID() }
        await iamApi.deliveryResultRequest({ method: kind === "ARCHIVE" ? "DELETE" : rule ? "PATCH" : "POST", url: `${base}${rule ? `/${encodeURIComponent(rule.id)}` : ""}?${query}` }, { ...(body ? { body } : {}), headers: { "Idempotency-Key": attempt.current.key, ...(rule ? { "If-Match": `"${rule.version}"` } : {}) } })
        attempt.current = null
        setCatalog(await iamApi.deliveryResultRequest<Catalog>({ method: "GET", url: `${base}?${query}` }))
        setSelected(null); setName(""); setDefinition(initialDefinition); setEnabled(true); setRuns(null)
      }
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) }
    finally { pending.current = false; setBusy(false) }
  }
  const edit = (rule: Rule | null) => { setSelected(rule); setName(rule?.name ?? ""); setEnabled(rule?.enabled ?? true); setDefinition(rule?.definition ? JSON.stringify(rule.definition, null, 2) : initialDefinition); setRuns(null) }
  if (!canRead) return null
  return <section className="space-y-4 border-t p-4" aria-label={t("automationTitle")}>
    <h2 className="font-semibold">{t("automationTitle")}</h2>
    <p className="text-sm text-muted-foreground">{t("automationExecutorHint")}</p>
    {error && <Alert>{error}</Alert>}
    <form className="flex items-end gap-2" onSubmit={e => { e.preventDefault(); void perform("PREVIEW") }}>
      <label><span>{t("automationTestEvent")}</span><Input required inputMode="numeric" pattern="[1-9][0-9]*" value={event} disabled={busy} onChange={e => setEvent(e.target.value)} /></label>
      <Button type="submit" variant="outline" disabled={busy || !/^[1-9][0-9]*$/.test(event)}>{t("automationPreview")}</Button>
    </form>
    {catalog?.previews.map(preview => <div key={preview.rule_id} className="space-y-1 border-b pb-2"><p>{preview.rule_id}: {preview.matches ? t("automationMatches") : t("automationNoMatch")} · {preview.reason}</p><pre className="whitespace-pre-wrap break-all text-xs">{JSON.stringify(preview.actions, null, 2)}</pre><p>{t("automationNoExecution")}</p></div>)}
    {!catalog ? <p>{t("loading")}</p> : catalog.rules.items.length === 0 ? <p>{t("automationEmpty")}</p> : <ul className="space-y-2">
      {catalog.rules.items.map(rule => <li key={rule.id} className="flex flex-wrap items-center gap-2 border-b pb-2"><span>{rule.name} · v{rule.version} · {rule.archived ? t("automationArchived") : rule.enabled ? t("automationEnabled") : t("automationDisabled")}</span><Button variant="outline" disabled={busy} onClick={() => void perform("RUNS", rule)}>{t("automationRuns")}</Button>{canManage && !rule.archived && <><Button variant="outline" disabled={busy} onClick={() => edit(rule)}>{t("automationEdit")}</Button><Button variant="outline" disabled={busy} onClick={() => void perform("ARCHIVE", rule)}>{t("automationArchive")}</Button></>}</li>)}
    </ul>}
    {catalog?.rules.has_more && <Button variant="outline" disabled={busy} onClick={() => void perform("NEXT")}>{t("automationNext")}</Button>}
    {runs && <div className="space-y-2"><h3>{t("automationRuns")}</h3>{runs.length === 0 && <p>{t("automationNoRuns")}</p>}{runs.map(run => <details key={run.id}><summary>{run.rule_name} · v{run.rule_version} · {run.work_item_id} · {run.outcome} {run.error}</summary><p className="text-sm">{t("automationRunAs")}: {run.run_as || "—"}</p><h4 className="text-sm">{t("automationActionResults")}</h4><pre className="whitespace-pre-wrap break-all text-xs">{run.actions_result || "[]"}</pre><h4 className="text-sm">{t("automationDefinition")}</h4><pre className="whitespace-pre-wrap break-all text-xs">{run.definition_snapshot}</pre></details>)}{runCursor && <Button disabled={busy} variant="outline" onClick={() => void perform("RUN_NEXT", runRule)}>{t("automationNext")}</Button>}</div>}
    {canManage && <form className="space-y-3" onSubmit={e => { e.preventDefault(); void perform("SAVE") }}>
      <label className="block"><span>{t("automationName")}</span><Input required maxLength={80} value={name} disabled={busy} onChange={e => setName(e.target.value)} /></label>
      <label className="flex gap-2"><input type="checkbox" checked={enabled} disabled={busy} onChange={e => setEnabled(e.target.checked)} />{t("automationEnabled")}</label>
      <label className="block"><span>{t("automationDefinition")}</span><Textarea required maxLength={6000} rows={10} value={definition} disabled={busy} onChange={e => setDefinition(e.target.value)} /></label>
      <p className="text-sm text-muted-foreground">{t("automationLogicHint")}</p>
      <p className="text-sm text-muted-foreground">{t("automationScheduleHint")}</p>
      <Button type="submit" disabled={busy || !name.trim()}>{t("automationSave")}</Button>{selected && <Button type="button" variant="outline" disabled={busy} onClick={() => edit(null)}>{t("automationNew")}</Button>}
    </form>}
  </section>
}
