import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { intakeChangeApi } from "./intake-api"

export function IntakeTriageRequest({ project, intakeID, onSaved }: { project: string; intakeID?: string; onSaved?: () => void }) {
  const t = useTranslations("iam")
  const [id, setID] = useState(intakeID ?? "")
  const [note, setNote] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [saved, setSaved] = useState("")
  const pending = useRef<{ identity: string; key: string } | null>(null)
  const request = useRef<AbortController | null>(null)
  useEffect(() => () => request.current?.abort(), [])
  return <form className="space-y-3 border-t pt-4" aria-label={t("intakeRequestTriage")} onSubmit={event => {
    event.preventDefault()
    if (busy) return
    const identity = JSON.stringify({ project, id, note })
    if (pending.current?.identity !== identity) pending.current = { identity, key: crypto.randomUUID() }
    const controller = new AbortController()
    request.current = controller
    setBusy(true); setError(""); setSaved("")
    void intakeChangeApi.requestTriage(project, id, note, pending.current.key, controller.signal).then(receipt => {
      if (controller.signal.aborted) return
      setSaved(t(receipt.coalesced ? "intakeRequestCoalesced" : "intakeRequestQueued"))
      pending.current = null
      onSaved?.()
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("clientSaveFailed"))
    }).finally(() => { if (!controller.signal.aborted) setBusy(false) })
  }}>
    <h3 className="font-medium">{t("intakeRequestTriage")}</h3>
    <p className="text-sm text-muted-foreground">{t("intakeRequestHint")}</p>
    <fieldset className="space-y-3" disabled={busy}>
      {!intakeID && <label className="block space-y-1"><span>{t("intakeRequestID")}</span><Input name="intake_id" value={id} onChange={event => { setID(event.target.value); setSaved("") }} pattern="[1-9][0-9]{0,18}" maxLength={19} required /></label>}
      <label className="block space-y-1"><span>{t("intakeRequestNote")}</span><Textarea name="note" value={note} onChange={event => { setNote(event.target.value); setSaved("") }} maxLength={2000} rows={2} /></label>
    </fieldset>
    {error && <Alert>{error} {t("intakeTriageRetry")}</Alert>}
    {saved && <p role="status">{saved}</p>}
    <Button type="submit" disabled={busy || !/^[1-9][0-9]{0,18}$/.test(id)}>{busy ? t("loading") : t("intakeRequestTriage")}</Button>
  </form>
}
