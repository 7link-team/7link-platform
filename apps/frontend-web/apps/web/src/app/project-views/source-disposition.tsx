import { useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import type { IntakeLedgerSource } from "./intake-api"

export function SourceDisposition({ project, source, onSaved }: { project: string; source: IntakeLedgerSource; onSaved: () => void }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [action, setAction] = useState("")
  const [reason, setReason] = useState("")
  const [code, setCode] = useState("OTHER")
  const [target, setTarget] = useState("")
  const [message, setMessage] = useState("")
  const [notify, setNotify] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [receipt, setReceipt] = useState(source.disposition_delivery)
  const pending = useRef(false)
  const attempt = useRef<{ signature: string; key: string } | null>(null)
  const sourceID = source.id
  if (sourceID && ["MERGED", "DECLINED"].includes(source.state) && (source.merged_into_intake_id || source.decline_reason || source.client_message)) return <section className="space-y-3 border-t pt-4">
    <h3 className="font-medium">{t("sourceDispositionTitle")}</h3>
    <dl className="space-y-2 text-sm">
      {source.merged_into_intake_id && <div><dt>{t("sourceDispositionTarget")}</dt><dd>{source.merged_into_intake_id}</dd></div>}
      {source.decline_reason_code && <div><dt>{t("sourceDispositionCode")}</dt><dd>{t(`sourceDispositionCode_${source.decline_reason_code}`)}</dd></div>}
      {source.decline_reason && <div><dt>{t("sourceDispositionReason")}</dt><dd className="whitespace-pre-wrap break-words">{source.decline_reason}</dd></div>}
      {source.client_message && <div><dt>{t("sourceDispositionMessage")}</dt><dd className="whitespace-pre-wrap break-words">{source.client_message}</dd></div>}
    </dl>
    {receipt && <div className="space-y-2 text-sm" aria-live="polite" aria-busy={busy}>
      <p>{t("sourceDispositionReceipt")}: {t(`sourceDispositionReceipt_${receipt.state}`)}</p>
      {receipt.message_id && <p>{t("sourceDispositionReceiptMessage")}: {receipt.message_id}</p>}
      {allowed("scope_change") && receipt.state === "POSTED" && <Button variant="outline" className="min-h-11" disabled={busy} onClick={async () => {
        if (pending.current) return
        pending.current = true; setBusy(true); setError("")
        try { setReceipt(await iamApi.deliveryResultRequest<NonNullable<IntakeLedgerSource["disposition_delivery"]>>({method: "GET", url: `/entities/${encodeURIComponent(project)}/intakes/${encodeURIComponent(sourceID)}/disposition-receipt`})) }
        catch (cause) { setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) }
        finally { pending.current = false; setBusy(false) }
      }}>{busy ? t("clientSaving") : t("sourceDispositionReceiptRefresh")}</Button>}
    </div>}
    {error && <Alert>{error}</Alert>}
    <p className="text-sm text-muted-foreground">{t("sourceDispositionHint")}</p>
  </section>
  if (!sourceID || source.kind === "REWORK" || !["RECEIVED", "TRIAGED", "CLARIFYING"].includes(source.state)) return null
  const canMerge = allowed("intake_convert"), canDecline = allowed("intake_decline")
  if (!canMerge && !canDecline) return null
  const enabled = (action === "MERGE" && canMerge && /^[1-9][0-9]{0,18}$/.test(target) && target !== source.id) || (action === "DECLINE" && canDecline)
  return <section className="space-y-3 border-t pt-4">
    <h3 className="font-medium">{t("sourceDispositionTitle")}</h3>
    <p className="text-sm text-muted-foreground">{t("sourceDispositionHint")}</p>
    {error && <Alert>{error}</Alert>}
    <form className="space-y-3" onSubmit={async e => {
      e.preventDefault()
      if (pending.current || !enabled || !reason.trim()) return
      pending.current = true; setBusy(true); setError("")
      const body = JSON.stringify({ action, reason: reason.trim(), client_message: message.trim(), notify_client: notify && (action !== "DECLINE" || code !== "SPAM"), ...(action === "MERGE" ? { target_id: target } : { reason_code: code }) })
      const signature = `${source.id}:${source.version}:${body}`
      if (attempt.current?.signature !== signature) attempt.current = { signature, key: crypto.randomUUID() }
      try {
        await iamApi.deliveryResultRequest({ method: "POST", url: `/entities/${encodeURIComponent(project)}/intakes/${encodeURIComponent(sourceID)}/${action === "MERGE" ? "merge" : "decline"}` }, { headers: { "If-Match": `"${source.version}"`, "Idempotency-Key": attempt.current.key }, body })
        attempt.current = null; onSaved()
      } catch (cause) { setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) }
      finally { pending.current = false; setBusy(false) }
    }}>
      <label className="block space-y-2"><span>{t("sourceDispositionAction")}</span><select className="block min-h-11 w-full rounded-md border bg-background p-2" value={action} disabled={busy} required onChange={e => setAction(e.target.value)}>
        <option value="">{t("sourceDispositionAction")}</option>{canMerge && <option value="MERGE">{t("sourceDispositionMerge")}</option>}{canDecline && <option value="DECLINE">{t("sourceDispositionDecline")}</option>}
      </select></label>
      {action === "MERGE" && <label className="block space-y-2"><span>{t("sourceDispositionTarget")}</span><Input required inputMode="numeric" pattern="[1-9][0-9]{0,18}" value={target} disabled={busy} onChange={e => setTarget(e.target.value)} /></label>}
      {action === "DECLINE" && <label className="block space-y-2"><span>{t("sourceDispositionCode")}</span><select className="block min-h-11 w-full rounded-md border bg-background p-2" value={code} disabled={busy} onChange={e => setCode(e.target.value)}>{["OUT_OF_SCOPE", "INSUFFICIENT_INFO", "CAPACITY", "SPAM", "OTHER"].map(v => <option key={v} value={v}>{t(`sourceDispositionCode_${v}`)}</option>)}</select></label>}
      <label className="block space-y-2"><span>{t("sourceDispositionReason")}</span><Textarea required maxLength={2000} value={reason} disabled={busy} onChange={e => setReason(e.target.value)} /></label>
      <label className="block space-y-2"><span>{t("sourceDispositionMessage")}</span><Textarea maxLength={2000} value={message} disabled={busy} onChange={e => setMessage(e.target.value)} /></label>
      <label className="flex min-h-11 items-center gap-2"><input type="checkbox" checked={notify} disabled={busy || (action === "DECLINE" && code === "SPAM")} onChange={e => setNotify(e.target.checked)} />{t("sourceDispositionNotify")}</label>
      <Button type="submit" variant="outline" disabled={busy || !enabled || !reason.trim() || (notify && !message.trim())}>{busy ? t("clientSaving") : t("clientSave")}</Button>
    </form>
  </section>
}
