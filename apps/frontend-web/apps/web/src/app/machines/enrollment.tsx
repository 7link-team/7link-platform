import {useEffect, useRef, useState} from "react"
import {Button} from "@workspace/ui/components/button"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {Alert} from "../../components/alert"
import {enrollmentApi, type EnrollmentSettings, type EnrollmentStatus} from "./enrollment-api"

export function Enrollment({tenantID, onChanged, initialCode = ""}: {tenantID: string; onChanged: () => Promise<void>; initialCode?: string}) {
  const t = useTranslations("iam")
  const [os, setOS] = useState("WINDOWS")
  const [settings, setSettings] = useState<EnrollmentSettings | null>(null)
  const [code, setCode] = useState(initialCode)
  const [verifiedCode, setVerifiedCode] = useState("")
  const [name, setName] = useState("")
  const [status, setStatus] = useState<EnrollmentStatus | null>(null)
  const [remaining, setRemaining] = useState(0)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [busy, setBusy] = useState(false)
  const pending = useRef<AbortController | null>(null)
  const intent = useRef(0)
  useEffect(() => {
    const controller = new AbortController()
    enrollmentApi.settings(tenantID, os, controller.signal).then(value => {if (!controller.signal.aborted) setSettings(value)}).catch(cause => {if (!controller.signal.aborted) {setSettings(null); setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}})
    return () => controller.abort()
  }, [tenantID, os, t])
  useEffect(() => () => {++intent.current; pending.current?.abort()}, [])
  useEffect(() => {
    if (!status) return
    const deadline = performance.now() + Math.max(0, Date.parse(status.expires_at) - Date.parse(status.server_time))
    const tick = () => setRemaining(Math.max(0, Math.ceil((deadline - performance.now()) / 1000)))
    tick(); const timer = setInterval(tick, 1000)
    return () => clearInterval(timer)
  }, [status])
  useEffect(() => {
    if (busy || !status || !(["PENDING", "APPROVING", "PROVISIONING", "APPROVED", "ISSUING"].includes(status.state) || (status.state === "COMPLETE" && status.machine_state !== "ONLINE" && status.machine_state !== "REVOKED"))) return
    const controller = new AbortController()
    const current = intent.current
    const timer = setInterval(() => {void enrollmentApi.status(tenantID, status.id, controller.signal).then(value => {if (!controller.signal.aborted && current === intent.current) setStatus(value)}).catch(cause => {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))})}, 5000)
    return () => {clearInterval(timer); controller.abort()}
  }, [status, tenantID, t, busy])
  const inspect = async () => {
    pending.current?.abort(); const controller = new AbortController(); pending.current = controller
    const current = ++intent.current
    setBusy(true); setError(""); setNotice(""); setVerifiedCode(""); setStatus(null)
    try {const value = await enrollmentApi.lookup(tenantID, code, controller.signal); if (current === intent.current && !controller.signal.aborted) {setStatus(value); setName(value.name); setVerifiedCode(code.trim().toUpperCase())}}
    catch (cause) {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
    finally {if (current === intent.current) setBusy(false)}
  }
  const decide = async (approve: boolean) => {
    if (!status || busy || remaining <= 0) return
    const current = ++intent.current
    setBusy(true); setError(""); setNotice("")
    try {
      await enrollmentApi.decide(tenantID, status.id, verifiedCode, approve, approve ? name.trim() : undefined)
      if (current !== intent.current) return
      const value = await enrollmentApi.status(tenantID, status.id)
      if (current !== intent.current) return
      setStatus(value); setNotice(t("fleetDecisionSaved")); await onChanged()
    }
    catch (cause) {if (current === intent.current) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
    finally {if (current === intent.current) setBusy(false)}
  }
  return <details className="shrink-0 max-h-[70vh] overflow-auto rounded-lg border p-3">
    <summary className="min-h-11 cursor-pointer font-medium">{t("fleetConfirmEnrollment")}</summary>
    <p className="my-3 text-sm text-muted-foreground">{t("fleetInstallPrerequisites")}</p>
    {error && <Alert>{error}</Alert>}{notice && <p role="status">{notice}</p>}
    <label className="flex flex-col gap-1 text-sm">{t("fleetOS")}<select className="min-h-11 rounded border bg-background px-3" value={os} onChange={event => {setSettings(null); setOS(event.target.value)}}>{["WINDOWS", "MACOS", "LINUX"].map(value => <option key={value}>{value}</option>)}</select></label>
    {settings?.available ? <div className="my-3 space-y-3">{(["install", "login", "start"] as const).map(step => <section key={step}><h3 className="text-sm font-medium">{t(`fleetInstall_${step}`)}</h3><pre className="whitespace-pre-wrap break-all rounded bg-muted p-3 text-sm">{settings[step]}</pre><Button variant="outline" onClick={async () => {try {await navigator.clipboard.writeText(settings[step]); setNotice(t("fleetCommandCopied"))} catch {setError(t("fleetCopyFailed"))}}}>{t("fleetCopyCommand")}</Button></section>)}</div> : <p role="status" className="my-3 text-sm">{settings ? t("fleetInstallerUnavailable") : t("loading")}</p>}
    <p className="my-3 text-sm">{t("fleetEnrollmentCodeHint")}</p>
    <form className="space-y-3" onSubmit={event => {event.preventDefault(); void inspect()}}>
      <label className="flex flex-col gap-1 text-sm">{t("fleetUserCode")}<input className="min-h-11 rounded border bg-background px-3" autoComplete="off" value={code} disabled={busy} required minLength={8} maxLength={16} pattern="[A-Za-z0-9]{4}-?[A-Za-z0-9]{4}" onChange={event => {++intent.current; pending.current?.abort(); setBusy(false); setCode(event.target.value); setVerifiedCode(""); setStatus(null); setNotice("")}} /></label>
      <Button type="submit" disabled={busy}>{t("fleetInspectEnrollment")}</Button>
    </form>
    {status && <section className="my-3 space-y-3" aria-label={t("fleetEnrollmentStatus")}>
      <p className="break-words">{status.name} · {status.os} · {t(`fleetEnrollment_${status.state}`)}</p>
      {status.facts ? <section className="space-y-2"><p className="text-sm text-muted-foreground">{t("fleetEnrollmentReported")}</p><dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 text-sm"><dt>{t("fleetEnrollmentHostname")}</dt><dd className="break-all">{status.facts.hostname}</dd><dt>{t("fleetEnrollmentArch")}</dt><dd>{status.facts.arch}</dd></dl><ul className="space-y-1 text-sm">{status.facts.runtimes.map(runtime => <li key={runtime.kind}>{runtime.kind} · {runtime.available ? runtime.version : t(runtime.error === "NOT_FOUND" ? "fleetEnrollmentRuntimeMissing" : "fleetEnrollmentInspectUnknown")}</li>)}</ul></section> : <p className="text-sm text-muted-foreground">{t("fleetEnrollmentInspectUnknown")}</p>}
      {status.state === "PENDING" && <><p role="timer" aria-live={remaining === 60 || remaining === 10 || remaining === 0 ? "assertive" : "off"}>{t("fleetEnrollmentRemaining", {seconds: remaining})}</p>{remaining > 0 ? <><label className="flex flex-col gap-1 text-sm">{t("fleetEnrollmentName")}<input className="min-h-11 rounded border bg-background px-3" value={name} disabled={busy} maxLength={100} onChange={event => setName(event.target.value)} /></label><div className="flex flex-wrap gap-2"><Button disabled={busy || !name.trim() || !verifiedCode} onClick={() => void decide(true)}>{t("fleetApprove")}</Button><Button variant="outline" disabled={busy || !verifiedCode} onClick={() => void decide(false)}>{t("fleetDenyEnrollment")}</Button></div></> : <p role="status">{t("fleetEnrollment_EXPIRED")}</p>}</>}
      {status.machine_id && <div role="status" className="space-y-2 text-sm"><p>{t("fleetEnrollmentConfirmed")}</p><p>{t(status.state === "COMPLETE" ? "fleetEnrollmentConnected" : "fleetEnrollmentWaiting")}</p><p>{t(status.machine_state === "ONLINE" ? "fleetEnrollmentOnline" : status.connection_delayed ? "fleetEnrollmentDelayed" : "fleetEnrollmentWaitingOnline")}</p><a className="inline-flex min-h-11 items-center underline" href={`/admin/${encodeURIComponent(tenantID)}/machines/${encodeURIComponent(status.machine_id)}`}>{t("fleetDetails")}</a></div>}
      {status.machine_id && <p>{t("fleetMachineID")}: {status.machine_id}</p>}
    </section>}
  </details>
}
