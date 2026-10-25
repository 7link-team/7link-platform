import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAllowed } from "../../lib/permissions"
import { processApi, type ProcessPage, type RunState } from "./process-api"

export function AgentProcessHistory({ tenant, agent }: { tenant: string; agent: string }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [offset, setOffset] = useState(0)
  const [draft, setDraft] = useState("")
  const [search, setSearch] = useState("")
  const [revision, setRevision] = useState(0)
  const [busy, setBusy] = useState(false)
  const [commandError, setCommandError] = useState("")
  const [state, setState] = useState<RunState | null>(null)
  const identity = JSON.stringify([tenant, agent, offset, search, revision])
  const [result, setResult] = useState<{ identity: string; page: ProcessPage | null; error: string } | null>(null)
  const attempt = useRef<{ desired: "ACTIVE" | "STOPPED"; key: string } | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    void processApi.list(tenant, agent, offset, search, controller.signal).then(page => { if (!controller.signal.aborted) setResult({ identity, page, error: "" }) }).catch((cause: Error) => { if (!controller.signal.aborted) setResult({ identity, page: null, error: cause.message }) })
    return () => controller.abort()
  }, [tenant, agent, offset, search, identity])
  const current = result?.identity === identity ? result : null
  const change = async (desired: "ACTIVE" | "STOPPED") => {
    if (busy) return
    setBusy(true); setCommandError("")
    if (attempt.current?.desired !== desired) attempt.current = { desired, key: crypto.randomUUID() }
    try { setState(await processApi.change(tenant, agent, desired, attempt.current!.key)); attempt.current = null; setOffset(0); setRevision(value => value + 1) } catch (cause) { setCommandError(cause instanceof Error ? cause.message : t("clientSaveFailed")) } finally { setBusy(false) }
  }
  return <section className="space-y-3" aria-label={t("agentProcessHistory")}>
    <h2 className="font-medium">{t("agentProcessHistory")}</h2>
    <p className="text-sm text-muted-foreground">{t("agentRestartPolicy")}</p>
    {allowed("agent_manage") && <div className="flex gap-2"><Button type="button" disabled={busy} onClick={() => void change("ACTIVE")}>{t("agentStart")}</Button><Button type="button" variant="outline" disabled={busy} onClick={() => void change("STOPPED")}>{t("agentStop")}</Button></div>}
    {state && <p role="status">{t("agentRunRequested")} · {state.state} · {state.process_status}</p>}
    {commandError && <Alert>{commandError}</Alert>}
    <form className="flex flex-wrap items-end gap-2" onSubmit={event => { event.preventDefault(); setSearch(draft.trim()); setOffset(0); setRevision(value => value + 1) }}><label className="grid gap-2 text-sm">{t("agentProcessSearch")}<Input value={draft} maxLength={200} onChange={event => setDraft(event.target.value)} /></label><Button type="submit" variant="outline">{t("refresh")}</Button></form>
    {!current && <p role="status">{t("loading")}</p>}
    {current?.error && <Alert>{current.error}</Alert>}
    {current?.page && <><ol className="space-y-3">{current.page.items.map(event => <li key={event.id} className="border-b pb-3 text-sm"><p className="font-medium">{t(`agentProcess_${event.kind}`)}</p><p className="break-words">{event.reason}</p><p className="text-muted-foreground">{event.occurred_at} · PID {event.pid} · {t("agentRestartCount")} {event.restart_count}</p></li>)}</ol>{!current.page.items.length && <p>{t("agentProcessEmpty")}</p>}</>}
    <div className="flex gap-2"><Button type="button" variant="outline" disabled={!current || offset === 0} onClick={() => setOffset(value => Math.max(0, value - 50))}>{t("fleetPrevious")}</Button><Button type="button" variant="outline" disabled={!current?.page?.has_more} onClick={() => setOffset(value => value + 50)}>{t("fleetNext")}</Button></div>
  </section>
}
