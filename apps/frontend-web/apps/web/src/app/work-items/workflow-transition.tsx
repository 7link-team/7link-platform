import { useEffect, useRef, useState } from "react"
import { useParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi, type WorkItemDetail } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { transitionMessages, transitionTargets, transitionFields } from "./workflow-transition-data"
import { read, type Workflow } from "../work-management/api"

export function WorkWorkflowTransition({ entityID, detail, onUpdated }: { entityID: string; detail: WorkItemDetail; onUpdated: (detail: WorkItemDetail) => void }) {
  const { tenantID = "" } = useParams()
  const locale = useLocale()
  const l = transitionMessages[locale as keyof typeof transitionMessages] ?? transitionMessages["en-US"]
  const allowed = useAllowed()
  const [workflow, setWorkflow] = useState<Workflow | null>(null)
  const [target, setTarget] = useState("")
  const [comment, setComment] = useState("")
  const [estimate, setEstimate] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const pending = useRef(false)
  const attempt = useRef<{ signature: string; key: string } | null>(null)
  const stateKey = (detail as WorkItemDetail & { state_key?: string }).state_key ?? detail.state?.toLowerCase() ?? ""
  const can = allowed("work_item_write") && !detail.archived_at
  useEffect(() => {
    const controller = new AbortController()
    void read<Workflow>(`/tenants/${tenantID}/projects/${entityID}/workflow-configs/${detail.type}`, controller.signal).then(value => { if (!controller.signal.aborted) { setWorkflow(value); setError("") } }, cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause)) })
    return () => controller.abort()
  }, [tenantID, entityID, detail.type, detail.version])
  const refresh = async () => {
    if (!detail.id || pending.current) return
    pending.current = true; setBusy(true)
    try { onUpdated(await iamApi.workItem(entityID, detail.id)); setError("") }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) }
    finally { pending.current = false; setBusy(false) }
  }
  const apply = async () => {
    if (!can || !detail.id || !workflow || pending.current) return
    const selected = transitionTargets(workflow, stateKey).find(state => state.key === target)
    if (!selected) return
    pending.current = true; setBusy(true); setError("")
    try {
      const fields = transitionFields(selected.maps_to, estimate, detail.current_claim_id, comment)
      const business = { to_state_key: target, fields, comment }
      const signature = JSON.stringify([detail.id, business])
      if (attempt.current?.signature !== signature) attempt.current = { signature, key: crypto.randomUUID() }
      await iamApi.deliveryResultRequest({ method: "POST", url: `/work-items/${detail.id}/transitions` }, { headers: { "Idempotency-Key": attempt.current.key }, body: JSON.stringify({ ...business, seen_seq: detail.activity_seq }) })
      onUpdated(await iamApi.workItem(entityID, detail.id))
      attempt.current = null; setTarget(""); setComment(""); setEstimate("")
    } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) }
    finally { pending.current = false; setBusy(false) }
  }
  return <section className="grid min-w-0 gap-3 border-t pt-4" aria-label={l.title}>
    <h3 className="font-semibold">{l.title}</h3><p className="text-sm text-muted-foreground">{l.hint}</p>
    <p className="text-sm">{stateKey}</p>
    {error && <Alert>{error}</Alert>}
    <Button type="button" variant="outline" disabled={busy} onClick={() => void refresh()}>{l.refresh}</Button>
    {!workflow ? !error && <p role="status">{l.loading}</p> : <form className="grid min-w-0 gap-3" onSubmit={event => { event.preventDefault(); void apply() }}>
      <label className="grid gap-1 text-sm">{l.target}<select className="min-h-11 rounded-md border bg-background px-3" value={target} disabled={!can || busy} onChange={event => setTarget(event.target.value)}><option value="">—</option>{transitionTargets(workflow, stateKey).map(state => <option key={state.key} value={state.key}>{state.display_name || state.key}</option>)}</select></label>
      <label className="grid gap-1 text-sm">{l.estimate}<Input type="number" min={15} max={4800} value={estimate} disabled={!can || busy} onChange={event => setEstimate(event.target.value)} /></label>
      <label className="grid gap-1 text-sm">{l.comment}<Textarea maxLength={2000} value={comment} disabled={!can || busy} onChange={event => setComment(event.target.value)} /></label>
      {can && <Button type="submit" disabled={busy || !target}>{l.apply}</Button>}
    </form>}
  </section>
}
