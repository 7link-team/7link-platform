import { defectMessages, defectPayload, emptyDetails, detailFields, versionFields, taxonomyOptions, type DefectDescription, type DefectReport } from "./defect-data"
import { useEffect, useRef, useState } from "react"
import type { FieldRendererProps } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

function DefectNarrativeFields({ details, onChange, locale }: { details: DefectDescription; onChange: (details: DefectDescription) => void; locale: string }) {
  const text = defectMessages[locale as keyof typeof defectMessages] ?? defectMessages["en-US"]
  return <>{detailFields.map(field => <label key={field} className="grid min-w-0 gap-1 text-sm">{text[field]}<Textarea required={field === "steps" || field === "expected" || field === "actual"} maxLength={field === "environment" ? 200 : 20000} value={details[field]} onChange={event => onChange({ ...details, [field]: event.target.value })} /></label>)}</>
}
export function DefectCreateFields(props: FieldRendererProps) {
  const locale = useLocale()
  return <fieldset className="grid min-w-0 gap-3" disabled={props.disabled || props.readonly} aria-labelledby={props.labelId} aria-describedby={props.describedBy}>
    <DefectNarrativeFields details={(props.value as DefectDescription | undefined) ?? emptyDetails} onChange={props.onChange} locale={locale} />
  </fieldset>
}
export function DefectReportFields({ report, onChange, disabled, locale }: { report: DefectReport; onChange: (report: DefectReport) => void; disabled: boolean; locale: string }) {
  const text = defectMessages[locale as keyof typeof defectMessages] ?? defectMessages["en-US"]
  const details = report.details ?? emptyDetails
  const setDetails = (patch: Partial<DefectDescription>) => onChange({ ...report, details: { ...details, ...patch } })
  return <fieldset disabled={disabled} className="grid min-w-0 gap-3">
    {Object.entries(taxonomyOptions).map(([key, options]) => {
      const field = key as keyof typeof taxonomyOptions
      return <label key={key} className="grid min-w-0 gap-1 text-sm">{text[field]}<select required className="min-h-11 w-full rounded-md border bg-background px-3" value={report[field] ?? ""} onChange={event => onChange({ ...report, [field]: event.target.value })}><option value="">{text.none}</option>{options.map(value => <option key={value} value={value}>{value}</option>)}</select></label>
    })}
    <DefectNarrativeFields details={details} onChange={value => setDetails(value)} locale={locale} />
    {versionFields.map(field => <label key={field} className="grid min-w-0 gap-1 text-sm">{text[field]}<Input maxLength={field === "module" ? 80 : field.startsWith("introduced") ? 32 : 64} value={report[field] ?? ""} onChange={event => onChange({ ...report, [field]: event.target.value || null })} /></label>)}
    <label className="grid gap-1 text-sm">{text.resolution}<select className="min-h-11 w-full rounded-md border bg-background px-3" value={details.resolution ?? ""} onChange={event => setDetails({ resolution: event.target.value || null, duplicate_of_work_item_id: event.target.value === "DUPLICATE" ? details.duplicate_of_work_item_id : null })}><option value="">{text.none}</option>{(["FIXED", "WONT_FIX", "INVALID", "DUPLICATE", "BY_DESIGN"] as const).map(value => <option key={value} value={value}>{text[value]}</option>)}</select></label>
    {details.resolution === "DUPLICATE" && <label className="grid gap-1 text-sm">{text.duplicate_of_work_item_id}<Input required inputMode="numeric" pattern="[1-9][0-9]*" maxLength={19} value={details.duplicate_of_work_item_id ?? ""} onChange={event => setDetails({ duplicate_of_work_item_id: event.target.value || null })} /></label>}
  </fieldset>
}

export function WorkDefectReport({ workID, version, onUpdated }: { workID: string; version: number; onUpdated: () => void }) {
  const locale = useLocale(), t = useTranslations("iam"), allowed = useAllowed()
  const text = defectMessages[locale as keyof typeof defectMessages] ?? defectMessages["en-US"]
  const [report, setReport] = useState<DefectReport | null>(null)
  const [error, setError] = useState(""), [busy, setBusy] = useState(false), [saved, setSaved] = useState(false)
  const [reload, setReload] = useState(0)
  const pending = useRef(false), attempt = useRef<{ signature: string; key: string } | null>(null)
  const path = `/work-items/${encodeURIComponent(workID)}/defect`
  useEffect(() => {
    const controller = new AbortController()
    queueMicrotask(() => { if (!controller.signal.aborted) { setReport(null); setError(""); attempt.current = null } })
    void iamApi.deliveryResultRequest<DefectReport>({ method: "GET", url: path }, { signal: controller.signal }).then(value => { if (!controller.signal.aborted) setReport(value) }).catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause)) })
    return () => controller.abort()
  }, [path, version, reload])
  const save = async () => {
    if (!report || !allowed("work_item_write") || pending.current) return
    pending.current = true; setBusy(true); setSaved(false); setError("")
    const body = JSON.stringify(defectPayload(report)), signature = `${report.version}:${body}`
    if (attempt.current?.signature !== signature) attempt.current = { signature, key: crypto.randomUUID() }
    try {
      const receipt = await iamApi.deliveryResultRequest<{ work_item_id: string; version: number }>({ method: "PUT", url: path }, { body, headers: { "Idempotency-Key": attempt.current.key, "If-Match": `"${report.version}"` } })
      setReport({ ...report, version: receipt.version }); attempt.current = null; setSaved(true); onUpdated()
    } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) }
    finally { pending.current = false; setBusy(false) }
  }
  return <section className="grid min-w-0 gap-3 border-t pt-4" aria-label={text.title}>
    <h3 className="font-semibold">{text.title}</h3><p className="text-sm text-muted-foreground">{text.hint}</p>
    {error && <Alert>{error}</Alert>}{saved && <p role="status">{text.saved}</p>}
    <Button type="button" variant="outline" disabled={busy} onClick={() => setReload(value => value + 1)}>{t("refresh")}</Button>
    {!report ? !error && <p role="status">{t("loading")}</p> : <form className="grid min-w-0 gap-3" onSubmit={event => { event.preventDefault(); void save() }}>
      {!report.details && <p>{text.unknown}</p>}
      <DefectReportFields report={report} onChange={value => { setReport(value); setSaved(false) }} disabled={busy || !allowed("work_item_write")} locale={locale} />
      {allowed("work_item_write") && <Button type="submit" disabled={busy}>{busy ? t("loading") : t("save")}</Button>}
    </form>}
  </section>
}
