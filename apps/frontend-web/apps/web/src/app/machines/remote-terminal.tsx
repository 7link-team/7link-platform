import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { ApiError, iamApi, type MachineRemoteAccess, type RemoteTerminalSession } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { encodeTerminalLine, terminalPlainText } from "./remote-terminal-codec"
import { RemotePreview } from "./remote-preview"

export function RemoteTerminal({machineID, os, online, executionIDs}: {
  machineID: string; os: string; online: boolean; executionIDs: string[]
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [policy, setPolicy] = useState<MachineRemoteAccess | null>(null)
  const [executionID, setExecutionID] = useState("")
  const [session, setSession] = useState<RemoteTerminalSession | null>(null)
  const [output, setOutput] = useState("")
  const [command, setCommand] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [connected, setConnected] = useState(false)
  const [verification, setVerification] = useState<"enable" | "open" | null>(null)
  const seq = useRef(0)
  const decoder = useRef(new TextDecoder())
  const active = useRef(true)
  const streaming = useRef<AbortController | null>(null)
  const createIntent = useRef<{executionID: string; key: string} | null>(null)
  const selectedExecution = executionIDs.includes(executionID) ? executionID : executionIDs[0] ?? ""
  useEffect(() => {
    active.current = true
    const controller = new AbortController()
    void iamApi.machineRemoteAccess(machineID, controller.signal).then(
      (result) => {if (!controller.signal.aborted) setPolicy(result)},
      (cause: Error) => {if (!controller.signal.aborted) setError(cause.message)}
    )
    return () => {active.current = false; controller.abort(); streaming.current?.abort()}
  }, [machineID])
  const sessionID = session?.state !== "CLOSED" ? session?.id : undefined
  useEffect(() => {
    if (!sessionID) return
    const controller = new AbortController()
    const timer = setInterval(() => {
      void iamApi.terminalSession(sessionID, controller.signal).then((result) => {
        if (!controller.signal.aborted) {
          setSession(result)
          if (result.state === "CLOSED") streaming.current?.abort()
        }
      }, (cause: Error) => {if (!controller.signal.aborted) setError(cause.message)})
    }, 1500)
    return () => {clearInterval(timer); controller.abort()}
  }, [sessionID])
  const streamSessionID = session?.state === "OPEN" ? session.id : undefined
  useEffect(() => {
    if (!streamSessionID) return
    const controller = new AbortController(); streaming.current = controller
    void iamApi.streamTerminalSession(streamSessionID, (event) => {
      if (controller.signal.aborted || !active.current) return
      if (event.kind === "output") {
        const text = decoder.current.decode(Uint8Array.from(atob(event.data), (char) => char.charCodeAt(0)), {stream: true})
        setOutput((previous) => (previous + text).slice(-262144))
        setConnected(true)
      }
      if (event.kind === "closed") setSession((current) => current ? {...current, state: "CLOSED", close_reason: event.reason ?? ""} : current)
    }, controller.signal, () => {if (!controller.signal.aborted && active.current) setConnected(true)}).then(() => {
      if (!controller.signal.aborted && active.current) setConnected(false)
    }, (cause: Error) => {
      if (!controller.signal.aborted && active.current) {setError(cause.message); setConnected(false)}
    })
    return () => {controller.abort()}
  }, [streamSessionID])
  const canInteract = allowed("terminal_interact")
  const open = session?.state === "OPEN" && connected
  async function changePolicy(enabled: boolean, proof?: string) {
    if (!policy) return
    setBusy(true); setError("")
    try {
      const next = await iamApi.setMachineRemoteAccess(machineID, policy.version, enabled, proof)
      if (active.current) {setPolicy(next); setVerification(null)}
    } catch (cause) {
      if (active.current) setError((cause as Error).message)
      throw cause
    } finally {if (active.current) setBusy(false)}
  }
  return <section className="flex min-w-0 flex-col gap-4 rounded-lg border p-4">
    <h2 className="font-medium">{t("terminalTitle")}</h2>
    <p role="note" className="rounded-md border border-red-600 bg-red-50 p-3 text-sm text-red-950 dark:bg-red-950 dark:text-red-100">{t("terminalFilesystemWarning")}</p>
    <p role="note" className="rounded-md border border-amber-600 bg-amber-50 p-3 text-sm text-amber-950 dark:bg-amber-950 dark:text-amber-100">{t("terminalAgentWarning")}</p>
    <p className="text-sm text-muted-foreground">{t("terminalLineConsole")}</p>
    {error && <Alert>{error}</Alert>}
    {!policy && !error && <p role="status">{t("loading")}</p>}
    {policy && <>
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-sm">{t(policy.allow_remote_terminal ? "terminalEnabled" : "terminalDisabled")}</span>
        {allowed("machine_access_manage") && <Button className="min-h-11" variant="outline" disabled={busy} onClick={() => {
          if (!policy.allow_remote_terminal) setVerification("enable")
          else void changePolicy(false).catch(() => {})
        }}>{t(policy.allow_remote_terminal ? "terminalDisable" : "terminalEnable")}</Button>}
      </div>
      {!policy.tunnel_available && <p role="status" className="text-sm text-muted-foreground">{t("terminalTransportUnavailable")}</p>}
      <label className="flex max-w-xl flex-col gap-2 text-sm">{t("executionID")}
        <select className="min-h-11 rounded-md border bg-background px-3" value={selectedExecution} disabled={busy || !!session && session.state !== "CLOSED"} onChange={(event) => setExecutionID(event.target.value)}>
          {!executionIDs.length && <option value="">{t("terminalNoExecutions")}</option>}
          {executionIDs.map((id) => <option key={id} value={id}>{id}</option>)}
        </select>
      </label>
      {canInteract && <div className="flex flex-wrap gap-2">
        <Button className="min-h-11" disabled={busy || !online || !policy.tunnel_available || !policy.allow_remote_terminal || !selectedExecution || !!session && session.state !== "CLOSED"} onClick={() => {
          if (!createIntent.current || createIntent.current.executionID !== selectedExecution || session?.state === "CLOSED") createIntent.current = {executionID: selectedExecution, key: crypto.randomUUID()}
          setVerification("open")
        }}>{t("terminalConnect")}</Button>
        {session && session.state !== "CLOSED" && <Button className="min-h-11" variant="destructive" disabled={busy} onClick={async () => {
          if (!session.id) return
          setBusy(true); setError("")
          try {const result = await iamApi.revokeTerminalSession(session.id); if (active.current) {streaming.current?.abort(); setSession(result)}}
          catch (cause) {if (active.current) setError((cause as Error).message)}
          finally {if (active.current) setBusy(false)}
        }}>{t("terminalRevoke")}</Button>}
      </div>}
    </>}
    {session && <>
      <p role="status" className="break-words text-sm">{t("terminalState")}: {session.state} {session.close_reason}</p>
      {!connected && session.state !== "CLOSED" && <p role="status" className="text-sm">{t("terminalDisconnected")}</p>}
      <p className="break-words text-sm text-muted-foreground">{t("terminalIdleInfo", {expires: session.expires_at})}</p>
      <pre aria-label={t("terminalOutput")} tabIndex={0} className="max-h-96 min-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md border bg-muted p-3 font-mono text-sm">{terminalPlainText(output)}</pre>
      {canInteract && <form className="flex flex-wrap items-end gap-2" onSubmit={async (event) => {
        event.preventDefault()
        if (!open || busy || !session.id) return
        setBusy(true); setError("")
        const sequence = seq.current + 1
        try {
          await iamApi.terminalInput(session.id, sequence, encodeTerminalLine(command, os))
          seq.current = sequence
          if (active.current) setCommand("")
        } catch (cause) {if (active.current) setError((cause as Error).message)}
        finally {if (active.current) setBusy(false)}
      }}>
        <label className="flex min-w-0 flex-1 flex-col gap-2 text-sm">{t("terminalCommand")}<input className="min-h-11 rounded-md border bg-background px-3 font-mono" value={command} onChange={(event) => setCommand(event.target.value)} disabled={!open || busy} autoComplete="off" spellCheck={false} /></label>
        <Button className="min-h-11" type="submit" disabled={!open || busy}>{t("terminalSend")}</Button>
      </form>}
    </>}
    {policy && <RemotePreview key={`${machineID}:${selectedExecution}`} machineID={machineID} executionID={selectedExecution} online={online} policy={policy} onPolicy={setPolicy} />}
    {verification && <ReauthenticationDialog purpose={verification === "enable" ? "machine_terminal_enable" : "terminal_interactive"} target={verification === "enable" ? `machine:${machineID}` : `execution:${selectedExecution}`} onClose={() => setVerification(null)} onVerified={async (proof) => {
      if (verification === "enable") {await changePolicy(true, proof); return}
      setBusy(true); setError("")
      try {
        // The same user intent retains its key through uncertain HTTP failures
        // and renewed step-up proofs. A cached OPENING receipt is not lifecycle.
        if (!createIntent.current || createIntent.current.executionID !== selectedExecution) createIntent.current = {executionID: selectedExecution, key: crypto.randomUUID()}
        const created = await iamApi.createTerminalSession(machineID, selectedExecution, proof, createIntent.current.key)
        if (!active.current) return
        const createdID = created.id
        if (!createdID) throw new ApiError(502, "invalid_response")
        setSession(created); setVerification(null); setOutput(""); setConnected(false); seq.current = 0; decoder.current = new TextDecoder()
        streaming.current?.abort()
        const current = await iamApi.terminalSession(createdID)
        if (active.current) setSession(current)
      } catch (cause) {
        if (active.current) setError((cause as Error).message)
        throw cause
      } finally {if (active.current) setBusy(false)}
    }} />}
  </section>
}
