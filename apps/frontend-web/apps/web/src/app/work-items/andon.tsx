import { useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi, type WorkItemDetail } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

type AndonReceipt = {
  runner_pause: string
  executions?: { execution_id: string | null; state: string; control: string }[] | null
}

// These are immutable command-time facts, including on an idempotent replay.
// Do not present a saved response as a live observation of a runner process.
export function WorkAndonReceipt({ receipt }: { receipt: AndonReceipt }) {
  const t = useTranslations("iam")
  const labels: Record<string, string> = {
    PAUSE_REQUESTED: t("workAndonPauseRequested"),
    PAUSED: t("workAndonPauseAcknowledged"),
    RESUME_REQUESTED: t("workAndonResumeRequested"),
    NOT_RUNNING: t("workAndonNotRunning"),
  }
  return <div role="status" aria-atomic="true" className="space-y-2 text-sm">
    <p>{t("workAndonSaved")}</p>
    <p>{t("workAndonReceiptSnapshot")}</p>
    {receipt.runner_pause === "UNAVAILABLE" && <p>{t("workAndonRuntimeUnavailable")}</p>}
    {receipt.executions?.map((fact, index) => <p key={`${fact.execution_id ?? "unknown"}:${index}`}>
      {fact.execution_id}: {labels[fact.control] ?? t("workAndonRuntimeUnknown")}
    </p>)}
  </div>
}

export function WorkAndon({ entityID, detail, onUpdated }: {
  entityID: string
  detail: WorkItemDetail
  onUpdated: (work: WorkItemDetail) => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [reason, setReason] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [saved, setSaved] = useState<{ workID: string; receipt: AndonReceipt } | null>(null)
  const pending = useRef(false)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  const workID = detail.id
  const clear = detail.andon_active
  const enabled = !["ACCEPTED", "CANCELLED"].includes(detail.state) &&
    allowed(clear ? "andon_clear" : "andon_pull") &&
    (clear || detail.paused_at === null)
  if (!workID || (!enabled && !clear)) return null
  const submit = async () => {
    if (pending.current || !enabled || reason.trim().length < 5) return
    pending.current = true
    setBusy(true)
    setError("")
    const body = JSON.stringify({ version: detail.version, reason: reason.trim() })
    const url = `/entities/${encodeURIComponent(entityID)}/work-items/${encodeURIComponent(workID)}/andon${clear ? "/clear" : ""}`
    const identity = `${url}:${body}`
    if (attempt.current?.identity !== identity) attempt.current = { identity, key: crypto.randomUUID() }
    try {
      const receipt = await iamApi.deliveryResultRequest<AndonReceipt>({ method: "POST", url }, {
        headers: { "Idempotency-Key": attempt.current.key }, body,
      })
      attempt.current = null
      setReason("")
      setSaved({ workID, receipt })
      onUpdated(await iamApi.workItem(entityID, workID))
    } catch (e) {
      setError(e instanceof Error ? e.message : t("govUnavailable"))
    } finally {
      pending.current = false
      setBusy(false)
    }
  }
  return <section className="space-y-3 border-t pt-4">
    <h3 className="font-medium">{t("workAndonTitle")}</h3>
    <p className="text-sm text-muted-foreground">{t("workAndonHint")}</p>
    {clear && <Alert>{t("workAndonActive")}</Alert>}
    {detail.current_execution_id && <Alert>{t("workAndonRuntimePending")}</Alert>}
    {error && <Alert>{error}</Alert>}
    {saved?.workID === workID && <WorkAndonReceipt receipt={saved.receipt} />}
    {enabled && <form className="space-y-3" onSubmit={e => { e.preventDefault(); void submit() }}>
      <label className="block space-y-2"><span>{t("workAndonReason")}</span>
        <Textarea required minLength={5} maxLength={2000} value={reason} disabled={busy} onChange={e => setReason(e.target.value)} />
      </label>
      <Button type="submit" variant={clear ? "outline" : "destructive"} disabled={busy || reason.trim().length < 5}>
        {busy ? t("loading") : t(clear ? "workAndonClear" : "workAndonPull")}
      </Button>
    </form>}
  </section>
}
