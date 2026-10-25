import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAuth } from "../../components/auth"
import { iamApi, type MachineRemoteAccess, type RemotePreviewSession } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { previewAccessURL, previewPort } from "../../lib/remote-preview"

export function RemotePreview({machineID, executionID, online, policy, onPolicy}: {
  machineID: string; executionID: string; online: boolean
  policy: MachineRemoteAccess
  onPolicy: (policy: MachineRemoteAccess) => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const {session: identity} = useAuth()
  const [port, setPort] = useState("")
  const [sessions, setSessions] = useState<RemotePreviewSession[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  const [accessURL, setAccessURL] = useState("")
  const active = useRef(true)
  const canRead = allowed("execution_read")
  useEffect(() => {
    active.current = true
    if (!executionID || !canRead) return () => {active.current = false}
    const controller = new AbortController()
    async function load() {
      try {
        const rows = await iamApi.previewSessions(executionID, controller.signal)
        if (!controller.signal.aborted) {
          setSessions((previous) => rows.map((row) => {
            const current = previous.find((candidate) => candidate.id === row.id)
            return current && current.version > row.version ? current : row
          }))
          setError("")
        }
      } catch (cause) {if (!controller.signal.aborted) setError((cause as Error).message)}
      finally {if (!controller.signal.aborted) setLoading(false)}
    }
    queueMicrotask(() => {if (!controller.signal.aborted) {setLoading(true); void load()}})
    const timer = setInterval(() => void load(), 5000)
    return () => {active.current = false; controller.abort(); clearInterval(timer)}
  }, [executionID, canRead])
  return <section className="flex min-w-0 flex-col gap-3 border-t pt-4" aria-label={t("remotePreviewTitle")}>
    <h3 className="font-medium">{t("remotePreviewTitle")}</h3>
    <p className="text-sm text-muted-foreground">{t("remotePreviewHint")}</p>
    {error && <Alert>{error}</Alert>}
    <div className="flex flex-wrap items-center gap-3">
      <span className="text-sm">{t(policy.allow_remote_preview ? "remotePreviewEnabled" : "remotePreviewDisabled")}</span>
      {allowed("machine_access_manage") && <Button variant="outline" className="min-h-11" disabled={busy} onClick={async () => {
        setBusy(true); setError("")
        try {
          const next = await iamApi.setMachinePreviewAccess(machineID, policy.version, policy.allow_remote_terminal, !policy.allow_remote_preview)
          if (active.current) onPolicy(next)
        } catch (cause) {if (active.current) setError((cause as Error).message)}
        finally {if (active.current) setBusy(false)}
      }}>{t(policy.allow_remote_preview ? "remotePreviewDisable" : "remotePreviewEnable")}</Button>}
    </div>
    {allowed("execution_start") && <form className="flex flex-wrap items-end gap-2" onSubmit={async (event) => {
      event.preventDefault()
      const parsed = previewPort(port)
      if (busy || parsed === null || !executionID || !online || !policy.allow_remote_preview || !policy.tunnel_available) return
      setBusy(true); setError(""); setAccessURL("")
      try {
        const created = await iamApi.createPreviewSession(executionID, parsed)
        if (active.current) setSessions((rows) => [created, ...rows.filter((row) => row.id !== created.id)])
      } catch (cause) {if (active.current) setError((cause as Error).message)}
      finally {if (active.current) setBusy(false)}
    }}>
      <label className="flex flex-col gap-2 text-sm">{t("remotePreviewPort")}
        <input className="min-h-11 w-40 rounded-md border bg-background px-3" type="number" min={1} max={65535} step={1} required inputMode="numeric" value={port} onChange={(event) => setPort(event.target.value)} disabled={busy} />
      </label>
      <Button className="min-h-11" type="submit" disabled={busy || !executionID || !online || !policy.allow_remote_preview || !policy.tunnel_available || previewPort(port) === null}>{t("remotePreviewCreate")}</Button>
    </form>}
    {loading && <p role="status" className="text-sm">{t("loading")}</p>}
    {!loading && canRead && !sessions.length && <p role="status" className="text-sm text-muted-foreground">{t("remotePreviewEmpty")}</p>}
    {accessURL && <a className="min-h-11 break-all py-3 text-sm underline" href={accessURL} target="_blank" rel="noopener noreferrer">{t("remotePreviewContinue")}</a>}
    <ul className="flex flex-col gap-3">
      {sessions.map((session) => <li key={session.id} className="flex flex-wrap items-center justify-between gap-3 rounded-md border p-3">
        <div className="min-w-0 text-sm">
          <p>{t("remotePreviewPort")}: {session.port} · {t(session.state === "READY" ? "remotePreviewReady" : "remotePreviewStopped")}</p>
          <p className="break-words text-muted-foreground">{t("remotePreviewExpires", {expires: session.expires_at})}</p>
          {session.stop_reason && <p className="break-words">{session.stop_reason}</p>}
          {session.path_mode_warning && <p role="note" className="mt-2 text-amber-800 dark:text-amber-300">{t("remotePreviewPathWarning")}</p>}
        </div>
        {session.state === "READY" && <div className="flex flex-wrap gap-2">
          {canRead && <Button variant="outline" className="min-h-11" disabled={busy || !session.id || !online || !policy.allow_remote_preview || !policy.tunnel_available} onClick={async () => {
            if (!session.id) return
            const tab = window.open("about:blank", "_blank")
            if (tab) tab.opener = null
            setBusy(true); setError(""); setAccessURL("")
            try {
              const grant = await iamApi.previewSessionAccess(session.id)
              const url = previewAccessURL(grant.url, window.location.origin)
              if (!active.current) {tab?.close(); return}
              if (tab) tab.location.replace(url)
              else setAccessURL(url)
            } catch (cause) {tab?.close(); if (active.current) setError((cause as Error).message)}
            finally {if (active.current) setBusy(false)}
          }}>{t("remotePreviewOpen")}</Button>}
          {(allowed("execution_stop") || canRead && !!identity?.subject && session.started_by === identity.subject) && <Button variant="destructive" className="min-h-11" disabled={busy || !session.id} onClick={async () => {
            if (!session.id) return
            setBusy(true); setError(""); setAccessURL("")
            try {
              const stopped = await iamApi.stopPreviewSession(session.id)
              if (active.current) setSessions((rows) => rows.map((row) => row.id === stopped.id ? stopped : row))
            } catch (cause) {if (active.current) setError((cause as Error).message)}
            finally {if (active.current) setBusy(false)}
          }}>{t("remotePreviewStop")}</Button>}
        </div>}
      </li>)}
    </ul>
  </section>
}
