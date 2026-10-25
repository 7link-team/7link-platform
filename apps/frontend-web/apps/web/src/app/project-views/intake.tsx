import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { useAllowed } from "../../lib/permissions"
import { intakeChangeApi, type IntakeChangeSource } from "./intake-api"

export function IntakeChange({ project, change, canFreeze, confirmed, onUpdated }: { project: string; change: string; canFreeze: boolean; confirmed: boolean; onUpdated: () => void }) {
  const allowed = useAllowed()
  if (!allowed("scope_change")) return null
  return <IntakeChangePanel key={`${project}:${change}`} project={project} change={change} canFreeze={canFreeze} confirmed={confirmed} onUpdated={onUpdated} />
}

function IntakeChangePanel({ project, change, canFreeze, confirmed, onUpdated }: { project: string; change: string; canFreeze: boolean; confirmed: boolean; onUpdated: () => void }) {
  const t = useTranslations("iam")
  const [params, setParams] = useSearchParams()
  const selected = params.get("intake") ?? ""
  const [original, setOriginal] = useState("")
  const [draftID, setDraftID] = useState(selected)
  const [source, setSource] = useState<IntakeChangeSource | null>(null)
  const [loaded, setLoaded] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const [confirm, setConfirm] = useState(false)
  const pendingDraft = useRef<{ signature: string; key: string } | null>(null)
  const pendingConversion = useRef<{ signature: string; key: string; proof: string } | null>(null)
  const identity = `${project}:${change}:${selected}:${revision}`
  useEffect(() => {
    if (!selected) return
    const controller = new AbortController()
    void intakeChangeApi.get(project, selected, controller.signal).then(value => {
      if (controller.signal.aborted) return
      if (value.id !== selected || value.project_id !== project || value.change_id !== change) throw new Error(t("intakeWrongChange"))
      setSource(value); setLoaded(identity); setError("")
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) { setSource(null); setLoaded(identity); setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) }
    })
    return () => controller.abort()
  }, [project, change, selected, revision, identity, t])
  const current = loaded === identity ? source : null
  const select = (id: string) => setParams(old => { const next = new URLSearchParams(old); next.set("intake", id); return next })
  return <section className="grid gap-3 border-t pt-4" aria-label={t("intakeChangeTitle")}>
    <h3 className="font-medium">{t("intakeChangeTitle")}</h3>
    <p className="text-sm text-muted-foreground">{t("intakeChangeNotice")}</p>
    {error && <Alert>{error}</Alert>}
    {canFreeze && <form className="flex flex-wrap items-end gap-3" onSubmit={async event => {
      event.preventDefault()
      if (busy) return
      setBusy(true); setError("")
      const signature = `${project}:${change}:${original}`
      if (pendingDraft.current?.signature !== signature) pendingDraft.current = { signature, key: crypto.randomUUID() }
      try {
        const receipt = await intakeChangeApi.draft(project, original, change, pendingDraft.current.key)
        if (receipt.id) { pendingDraft.current = null; setDraftID(receipt.id); select(receipt.id) }
      } catch (cause) { setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) }
      finally { setBusy(false) }
    }}>
      <label className="grid gap-1 text-sm">{t("intakeOriginalID")}<Input value={original} onChange={event => setOriginal(event.target.value)} required inputMode="numeric" pattern="[1-9][0-9]{0,18}" disabled={busy}/></label>
      <Button type="submit" disabled={busy}>{t("intakeFreeze")}</Button>
    </form>}
    <form className="flex flex-wrap items-end gap-3" onSubmit={event => { event.preventDefault(); setConfirm(false); select(draftID); setRevision(value => value + 1) }}>
      <label className="grid gap-1 text-sm">{t("intakeSourceID")}<Input value={draftID} onChange={event => setDraftID(event.target.value)} required inputMode="numeric" pattern="[1-9][0-9]{0,18}" disabled={busy}/></label>
      <Button type="submit" variant="outline" disabled={busy}>{t("intakeRead")}</Button>
    </form>
    {selected && loaded !== identity && <p role="status">{t("loading")}</p>}
    {current && <>
      <dl className="grid gap-3 text-sm sm:grid-cols-2">{[
        ["intakeSourceID", current.id], ["intakeOriginalID", current.original_intake_id], ["clientRequirements", current.requirement_id], ["status", current.state], ["clientVersion", current.version], ["intakeCardRevision", current.card_revision], ["intakeProposalHash", current.proposal_hash], ...(current.result_version_no ? [["intakeResultVersion", current.result_version_no]] : []),
      ].map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{t(String(label))}</dt><dd className="break-all">{value}</dd></div>)}</dl>
      <details><summary className="cursor-pointer py-2">{t("intakeFrozenProposal")}</summary><pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words text-xs">{current.proposal}</pre><pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words text-xs">{current.impact}</pre></details>
      {current.state === "TRIAGED" && confirmed && current.id && current.requirement_id && <Button className="justify-self-start" disabled={busy} onClick={() => setConfirm(true)}>{t("intakeConvert")}</Button>}
      {current.state === "TRIAGED" && !confirmed && <p role="status">{t("intakeAwaitingConfirmation")}</p>}
    </>}
    {confirm && current && <ReauthenticationDialog purpose="scope_change" target={`requirement:${current.requirement_id}`} onClose={() => setConfirm(false)} onVerified={async proof => {
      const signature = JSON.stringify([project, current.id, current.version, current.card_revision, current.proposal_hash])
      if (pendingConversion.current?.signature !== signature) pendingConversion.current = { signature, key: crypto.randomUUID(), proof }
      setBusy(true)
      try {
        await intakeChangeApi.convert(project, current, pendingConversion.current.proof, pendingConversion.current.key)
        pendingConversion.current = null; setConfirm(false); setRevision(value => value + 1); onUpdated()
      } finally { setBusy(false) }
    }}/>}
  </section>
}
