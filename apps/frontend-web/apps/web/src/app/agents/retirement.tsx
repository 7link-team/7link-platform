import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { iamApi } from "../../lib/iam-api"
import { retirementApi, type HandoverDocument, type Retirement, type RetirementPatch, type RetirementStart } from "./retirement-api"

const steps = ["PRECHECK", "HANDOVER", "SUCCESSOR", "REASSIGN", "CLEANUP", "REVOKE", "DONE"]
export function RetirementProgress({ value }: { value: Retirement }) {
  const t = useTranslations("iam")
  const progress = value.precheck.progress
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (value.state !== "IN_PROGRESS" || value.step !== "HANDOVER" || !value.step_started_at) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [value.state, value.step, value.step_started_at])
  const handoverSeconds = value.step_started_at ? Math.max(0, Math.ceil((Date.parse(value.step_started_at) + 600000 - now) / 1000)) : NaN
  return <div className="space-y-4" aria-live="polite">
    <ol className="flex flex-wrap gap-3" aria-label={t("agentRetirementSteps")}>{steps.map(step => <li key={step} aria-current={value.step === step ? "step" : undefined} className={value.step === step ? "font-semibold underline" : "text-muted-foreground"}>{t(`agentRetirementStep_${step}`)}</li>)}</ol>
    <p>{t(`agentRetirementState_${value.state}`)} · {value.reason}</p>
    {value.mode === "FORCED" && <Alert>{t("agentRetirementForcedWarning")}</Alert>}
    {value.state === "IN_PROGRESS" && value.step === "HANDOVER" && Number.isFinite(handoverSeconds) && <p role="timer" aria-live="off">{t("agentRetirementHandoverRemaining", { seconds: handoverSeconds })}</p>}
    {progress?.handover_error_code && <Alert>{t("agentRetirementSummaryUnavailable")}</Alert>}
    {value.state === "COMPLETED" && <p>{t("agentRetirementReconciliation")}: {t(progress?.reconciliation_status === "COMPLETE" ? "agentRetirementReconciliation_COMPLETE" : "agentRetirementReconciliation_PENDING")}</p>}
    <dl className="grid gap-2 sm:grid-cols-2">
      {value.precheck.machine && <div><dt>{t("agentRetirementMachine")}</dt><dd>{[value.precheck.machine.machine_id, value.precheck.machine.machine_state, value.precheck.machine.process_status].filter(Boolean).join(" · ")}</dd></div>}
      <div><dt>{t("agentRetirementWork")}</dt><dd>{value.precheck.work_items.length}</dd></div>
      <div><dt>{t("agentRetirementExecutions")}</dt><dd>{value.precheck.executions.length}</dd></div>
      <div><dt>{t("agentRetirementHandover")}</dt><dd>{progress?.handover_artifact_id ?? t("agentRetirementPending")}{progress?.handover_source && ` · ${t(`agentRetirementSource_${progress.handover_source}`)}`}</dd></div>
      <div><dt>{t("agentRetirementHandoverExecution")}</dt><dd>{progress?.handover_execution_id ?? t("agentRetirementPending")}</dd></div>
      <div><dt>{t("agentRetirementCleanup")}</dt><dd>{progress?.cleanup_status ? (t.has(`agentRetirementCleanup_${progress.cleanup_status}`) ? t(`agentRetirementCleanup_${progress.cleanup_status}`) : progress.cleanup_status) : t("agentRetirementPending")}</dd></div>
      <div><dt>{t("agentRetirementSuccessor")}</dt><dd>{progress?.successor_kind ? t(`agentRetirementSuccessor_${progress.successor_kind}`) : t("agentRetirementPending")} {progress?.successor_principal_id}</dd></div>
    </dl>
    {!!value.precheck.lead_project_ids?.length && <p>{t("agentRetirementLeadWarning")} {value.precheck.lead_project_ids.join(", ")}</p>}
    {value.precheck.lost && <p>{t("agentRetirementLoss", { work: value.precheck.lost.open_items, executions: value.precheck.lost.running_executions })}</p>}
    {!!value.precheck.work_items.length && <ul className="list-disc pl-5">{value.precheck.work_items.map(item => <li key={item.assignment_id}>{item.title} · {item.work_item_id} · {item.state}</li>)}</ul>}
    {!!progress?.reassign_result?.length && <div><h4 className="font-medium">{t("agentRetirementResults")}</h4><ul>{progress.reassign_result.map((item, index) => <li key={index}>{Object.entries(item).filter(([, fact]) => typeof fact === "string" || typeof fact === "number").map(([, fact]) => String(fact)).join(" · ")}</li>)}</ul></div>}
  </div>
}

export function AgentRetirement({ tenant, agent, canRetire }: { tenant: string; agent: string; canRetire: boolean }) {
  const t = useTranslations("iam")
  const actions = useTranslations("actions")
  const [search, setSearch] = useSearchParams()
  const retirementID = search.get("retirement") ?? ""
  const [dismissedRetirementID, setDismissedRetirementID] = useState("")
  const [value, setValue] = useState<Retirement | null>(null)
  const [document, setDocument] = useState<HandoverDocument | null>(null)
  const [identity, setIdentity] = useState<{ name: string; version: number; state: string } | null>(null)
  const [reason, setReason] = useState("")
  const [mode, setMode] = useState<"NORMAL" | "FORCED">("NORMAL")
  const [forceEscalation, setForceEscalation] = useState(false)
  const [confirmName, setConfirmName] = useState("")
  const [successor, setSuccessor] = useState<"HUMAN" | "AGENT" | "LATER">("LATER")
  const [principal, setPrincipal] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [reauth, setReauth] = useState(false)
  const [revision, setRevision] = useState(0)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  useEffect(() => {
    const abort = new AbortController()
    const load = async () => {
      try {
        const [live, binding] = await Promise.all([iamApi.agentLive(tenant, agent, abort.signal), retirementApi.binding(tenant, agent, abort.signal)])
        if (abort.signal.aborted) return
        setIdentity({ name: live.name, version: binding.version, state: binding.state })
        const id = retirementID || (binding.retirement_id === dismissedRetirementID ? undefined : binding.retirement_id)
        if (id && /^[1-9][0-9]{0,18}$/.test(id)) {
          const current = await retirementApi.read(tenant, id, abort.signal)
          if (!abort.signal.aborted && current.agent_principal_id === agent) {
            setValue(current)
            if (canRetire && current.precheck.progress?.handover_artifact_id) {
              const document = await retirementApi.handover(tenant, id, abort.signal)
              if (!abort.signal.aborted) setDocument(document)
            } else setDocument(null)
          }
        } else {
          setValue(null)
          setDocument(null)
        }
        if (!abort.signal.aborted) setError("")
      } catch (cause) { if (!abort.signal.aborted) setError(cause instanceof Error ? cause.message : t("clientLoadFailed")) }
    }
    void load()
    const timer = setInterval(() => { void load() }, 5000)
    return () => { abort.abort(); clearInterval(timer) }
  }, [tenant, agent, retirementID, dismissedRetirementID, revision, canRetire, t])
  const run = async (body: RetirementStart | RetirementPatch) => {
    if (busy || !identity || !canRetire) return
    setBusy(true); setError("")
    const starting = "mode" in body
    const fingerprint = JSON.stringify({ body: { ...body, reauth_proof: undefined }, version: starting ? identity.version : value?.version, agentVersion: identity.version })
    if (attempt.current?.body !== fingerprint) attempt.current = { body: fingerprint, key: crypto.randomUUID() }
    try {
      const current = !starting && value ? await retirementApi.patch(tenant, value.id, value.version, body as RetirementPatch, attempt.current!.key) : await retirementApi.start(tenant, agent, identity.version, body as RetirementStart, attempt.current!.key)
      setValue(current); if (!current.precheck.progress?.handover_artifact_id) setDocument(null); attempt.current = null; setReauth(false)
      setSearch(previous => { const next = new URLSearchParams(previous); next.set("retirement", current.id); return next })
      setForceEscalation(false); setRevision(current => current + 1)
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) } finally { setBusy(false) }
  }
  const active = value?.state === "IN_PROGRESS"
  const downloadHandover = () => {
    if (!document) return
    const url = URL.createObjectURL(new Blob([document.content], { type: "text/markdown;charset=utf-8" }))
    const anchor = window.document.createElement("a")
    anchor.href = url
    anchor.download = `handover-${document.id}.md`
    anchor.click()
    URL.revokeObjectURL(url)
  }
  return <section className="space-y-4 rounded-lg border p-4" aria-label={t("agentRetirementTitle")} aria-busy={busy}>
    <h3 className="text-lg font-semibold">{t("agentRetirementTitle")}</h3>
    <p className="text-sm text-muted-foreground">{t("agentRetirementWarning")}</p>
    {error && <Alert>{error}</Alert>}
    <Button variant="outline" disabled={busy} onClick={() => setRevision(current => current + 1)}>{t("retryNow")}</Button>
    {!identity && !error && <p role="status">{t("loading")}</p>}
    {value && <RetirementProgress value={value} />}
    {canRetire && identity && identity.state !== "RETIRED" && value && ["CANCELLED", "FAILED"].includes(value.state) && <Button variant="outline" disabled={busy} onClick={() => { setDismissedRetirementID(value.id); setValue(null); setDocument(null); setSearch(previous => { const next = new URLSearchParams(previous); next.delete("retirement"); return next }) }}>{t("agentRetirementStart")}</Button>}
    {document && <details className="rounded-md border p-3"><summary className="cursor-pointer font-medium">{t("agentRetirementHandover")}</summary><pre className="mt-3 max-h-96 overflow-auto whitespace-pre-wrap break-words text-sm">{document.content}</pre><Button variant="outline" onClick={downloadHandover}>{t("agentRetirementDownload")}</Button></details>}
    {canRetire && identity && active && !forceEscalation && <Button variant="destructive" disabled={busy} onClick={() => { setMode("FORCED"); setForceEscalation(true) }}>{t("agentRetirementMode_FORCED")}</Button>}
    {canRetire && identity && (!value || forceEscalation) && identity.state !== "RETIRED" && <form className="grid gap-4" onSubmit={event => { event.preventDefault(); if (mode === "FORCED") setReauth(true); else void run({ mode, reason }) }}>
      <label className="grid gap-2">{t("agentRetirementMode")}<select className="min-h-11 rounded-md border bg-background px-3" value={mode} disabled={busy || forceEscalation} onChange={event => setMode(event.target.value as "NORMAL" | "FORCED")}>{["NORMAL", "FORCED"].map(mode => <option key={mode} value={mode}>{t(`agentRetirementMode_${mode}`)}</option>)}</select></label>
      <label className="grid gap-2">{t("agentField_reason")}<textarea className="min-h-24 rounded-md border bg-background p-3" required minLength={10} maxLength={2000} value={reason} disabled={busy} onChange={event => setReason(event.target.value)} /></label>
      {mode === "FORCED" && <><Alert>{t("agentRetirementForcedWarning")}</Alert><label className="grid gap-2">{t("agentField_confirm_name")} · {identity.name}<Input required value={confirmName} disabled={busy} onChange={event => setConfirmName(event.target.value)} /></label></>}
      <Button type="submit" variant="destructive" disabled={busy || reason.trim().length < 10 || (mode === "FORCED" && confirmName !== identity.name)}>{t("agentRetirementStart")}</Button>
      {forceEscalation && <Button type="button" variant="outline" disabled={busy} onClick={() => setForceEscalation(false)}>{actions("cancel")}</Button>}
    </form>}
    {canRetire && identity && active && value && !forceEscalation && <div className="flex flex-wrap items-end gap-3">
      {value.step === "SUCCESSOR" && <form className="flex flex-wrap items-end gap-3" onSubmit={event => { event.preventDefault(); void run({ agent_version: identity.version, successor_kind: successor, ...(successor !== "LATER" ? { successor_principal_id: principal } : {}) }) }}>
        <label className="grid gap-2">{t("agentRetirementSuccessor")}<select className="min-h-11 rounded-md border bg-background px-3" value={successor} disabled={busy} onChange={event => setSuccessor(event.target.value as typeof successor)}>{["HUMAN", "AGENT", "LATER"].map(kind => <option key={kind} value={kind}>{t(`agentRetirementSuccessor_${kind}`)}</option>)}</select></label>
        {successor !== "LATER" && <label className="grid gap-2">{t("agentPrincipal")}<Input required pattern="[1-9][0-9]{0,18}" inputMode="numeric" value={principal} disabled={busy} onChange={event => setPrincipal(event.target.value)} /></label>}
        <Button disabled={busy} type="submit">{t("agentRetirementChoose")}</Button>
      </form>}
      {value.step === "SUCCESSOR" && value.precheck.progress?.handover_source === "SYSTEM" && (value.precheck.progress?.handover_retry_count ?? 0) < 1 && <Button variant="outline" disabled={busy} onClick={() => void run({ agent_version: identity.version, handover_retry: true })}>{t("agentRetirementRetryHandover")}</Button>}
      {["HANDOVER", "REASSIGN", "CLEANUP"].includes(value.step) && <Button disabled={busy} onClick={() => void run({ agent_version: identity.version, advance: true })}>{t("agentRetirementAdvance")}</Button>}
      {["PRECHECK", "HANDOVER", "SUCCESSOR"].includes(value.step) && <Button variant="outline" disabled={busy} onClick={() => void run({ agent_version: identity.version, state: "CANCELLED" })}>{t("agentRetirementCancel")}</Button>}
    </div>}
    {reauth && <ReauthenticationDialog purpose="agent_forced_retire" target={`tenant:${tenant}:agent:${agent}`} onClose={() => setReauth(false)} onVerified={proof => run({ mode: "FORCED", reason, confirm_name: confirmName, reauth_proof: proof })} />}
  </section>
}
