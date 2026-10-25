import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

type SpawnRequest = {
  id: string
  requester_id: string
  supervisor_id: string
  agent_id?: string
  state: string
  reason: string
  version: number
  expires_at: string
}

export function SpawnRequests({ tenant }: { tenant: string }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()("agent_manage")
  const [search, setSearch] = useSearchParams()
  const [rows, setRows] = useState<SpawnRequest[] | null>(null)
  const [reason, setReason] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const [offset, setOffset] = useState(0)
  const replay = useRef<{ body: string; key: string } | null>(null)
  const root = `/tenants/${encodeURIComponent(tenant)}/agent-spawn-requests`
  const selected = rows?.find(row => row.id === search.get("request_id"))
  useEffect(() => {
    if (!allowed) return
    const controller = new AbortController()
    iamApi.deliveryResultRequest<SpawnRequest[]>(
      { method: "GET", url: `${root}?limit=50&offset=${offset}` },
      { signal: controller.signal }
    ).then(value => { setRows(value); setError("") }).catch(e => {
      if (!controller.signal.aborted) setError(e.message)
    })
    return () => controller.abort()
  }, [allowed, root, offset, revision])
  async function change(action: "ADOPT" | "REJECT" | "ORPHAN" | "CREATE") {
    if (!selected || busy || reason.trim().length < 10) return
    const body = JSON.stringify({ version: selected.version, action, reason })
    const identity = `${selected.id}:${body}`
    if (replay.current?.body !== identity) replay.current = { body: identity, key: crypto.randomUUID() }
    setBusy(true)
    setError("")
    try {
      await iamApi.deliveryResultRequest<SpawnRequest>(
        { method: "PUT", url: `${root}/${selected.id}/state` },
        { headers: { "Idempotency-Key": replay.current.key }, body }
      )
      replay.current = null
      setReason("")
      setRevision(value => value + 1)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally { setBusy(false) }
  }
  if (!allowed) return null
  return <section className="space-y-4" aria-labelledby="spawn-requests-title">
    <h2 className="text-lg font-semibold" id="spawn-requests-title">{t("spawnRequestsTitle")}</h2>
    <p className="text-muted-foreground text-sm">{t("spawnRequestsDescription")}</p>
    {error && <Alert>{error}</Alert>}
    {!rows && !error && <p role="status">{t("loading")}</p>}
    {rows && <>
      {rows.length === 0 && <p>{t("spawnRequestsEmpty")}</p>}
      <AutoTable data={rows} pagination={false} rowKey="id" columns={[
        { field: "id", name: t("spawnRequestID") },
        { field: "requester_id", name: t("agentPrincipal") },
        { field: "state", name: t("status"), render: (_, row) => t(`spawnRequestState_${row.state}`) },
        { field: "reason", name: t("reason") },
      ]} actions={[{ name: t("view"), disabled: () => busy, onclick: row => {
          setSearch(previous => { const next = new URLSearchParams(previous); next.set("request_id", row.id); return next })
          setReason("")
        } }]} />
      <div className="flex gap-2">
        <Button variant="outline" disabled={busy || offset === 0} onClick={() => setOffset(value => Math.max(0, value - 50))}>{t("previous")}</Button>
        <Button variant="outline" disabled={busy || rows.length < 50} onClick={() => setOffset(value => value + 50)}>{t("next")}</Button>
      </div>
    </>}
    {selected && <div className="space-y-3 border-t pt-4">
      <p className="font-medium">{t("spawnRequestID")}: {selected.id} · {t(`spawnRequestState_${selected.state}`)}</p>
      <p className="text-sm">{selected.reason}</p>
      {selected.agent_id && <p>{t("agentPrincipal")}: {selected.agent_id}</p>}
      {["REQUESTED", "UNAVAILABLE"].includes(selected.state) && <>
        <label className="grid gap-2 text-sm" htmlFor="spawn-request-reason">{t("spawnRequestReason")}
          <textarea id="spawn-request-reason" className="border-input min-h-24 rounded-md border bg-transparent p-3" value={reason} disabled={busy} minLength={10} maxLength={2000} onChange={event => setReason(event.target.value)} />
        </label>
        <div className="flex flex-wrap gap-2">
          {(["ADOPT", "REJECT", "ORPHAN", "CREATE"] as const).map(action => <Button key={action} variant={action === "ADOPT" ? "default" : "outline"} disabled={busy || reason.trim().length < 10} onClick={() => void change(action)}>{t(`spawnRequestAction_${action}`)}</Button>)}
        </div>
      </>}
    </div>}
  </section>
}
