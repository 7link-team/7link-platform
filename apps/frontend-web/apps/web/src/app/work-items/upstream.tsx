import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi, type WorkItemDetail } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

export type ArtifactConsumption = {
  id: string; upstream_work_item_id: string; accepted_artifact_id: string
  version_id: string; content_sha256: string; state: string; checked_at: string | null
}
export function WorkUpstreamArtifacts({ entityID, detail }: { entityID: string; detail: WorkItemDetail }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [rows, setRows] = useState<ArtifactConsumption[]>([])
  const [artifact, setArtifact] = useState("")
 const [replaces, setReplaces] = useState("")
 const [reason, setReason] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const pending = useRef(false)
  const path = `/entities/${encodeURIComponent(entityID)}/work-items/${encodeURIComponent(detail.id!)}/artifact-consumptions`
  useEffect(() => {
    const c = new AbortController()
    void iamApi.deliveryResultRequest<ArtifactConsumption[]>({ method: "GET", url: path }, { signal: c.signal })
      .then(v => { if (!c.signal.aborted) setRows(v) })
      .catch(e => { if (!c.signal.aborted) setError(e instanceof Error ? e.message : String(e)) })
      .finally(() => { if (!c.signal.aborted) setLoading(false) })
    return () => c.abort()
  }, [path, detail.version])
  const mutate = async (reconcile: boolean) => {
    if (pending.current) return
    pending.current = true; setBusy(true); setError("")
    const body = JSON.stringify({ version: detail.version, accepted_artifact_id: artifact, ...(replaces ? {replaces_id: replaces, reason} : {}) })
    if (attempt.current?.body !== body) attempt.current = { body, key: crypto.randomUUID() }
    try {
      await iamApi.deliveryResultRequest({ method: "POST", url: `${path}${reconcile ? "/reconcile" : ""}` }, reconcile ? {} : {
        body, headers: { "Idempotency-Key": attempt.current.key },
      })
      attempt.current = null
      setRows(await iamApi.deliveryResultRequest<ArtifactConsumption[]>({ method: "GET", url: path }))
      if (!reconcile) { setArtifact(""); setReplaces(""); setReason("") }
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) }
    finally { pending.current = false; setBusy(false) }
  }
  return <section className="space-y-3 border-t pt-4" aria-label={t("upstreamTitle")}>
    <h3 className="font-medium">{t("upstreamTitle")}</h3>
    <p className="text-sm text-muted-foreground">{t("upstreamHint")}</p>
    {error && <Alert>{error}</Alert>}
    {loading ? <p>{t("loading")}</p> : rows.length === 0 ? <p>{t("upstreamEmpty")}</p> : <ul className="space-y-3">
      {rows.map(row => <li key={row.id} className="break-all border-b pb-3">
        <a className="underline" href={`?view=list&work=${encodeURIComponent(row.upstream_work_item_id)}`}>{row.upstream_work_item_id}</a>
        <p>{t("upstreamConsumption")}: {row.id}</p>
 <p>{t("upstreamArtifact")}: {row.accepted_artifact_id} · {row.version_id}</p>
        <code className="text-xs">{row.content_sha256}</code>
        <p role="status">{t(`upstreamState_${row.state}`)}</p>
      </li>)}
    </ul>}
    {allowed("work_item_write") && <>
      <Button disabled={busy || loading} variant="outline" onClick={() => void mutate(true)}>{t("upstreamReconcile")}</Button>
      <form className="flex flex-wrap gap-2" onSubmit={e => { e.preventDefault(); void mutate(false) }}>
        <label><span className="text-sm">{t("upstreamArtifact")}</span><Input required inputMode="numeric" pattern="[1-9][0-9]*" value={artifact} disabled={busy} onChange={e => setArtifact(e.target.value)} /></label>
        <label><span className="text-sm">{t("upstreamReplace")}</span><Input inputMode="numeric" pattern="[1-9][0-9]*" value={replaces} disabled={busy} onChange={e => setReplaces(e.target.value)} /></label>
 {replaces && <label><span className="text-sm">{t("upstreamReplaceReason")}</span><Input required minLength={5} maxLength={2000} value={reason} disabled={busy} onChange={e => setReason(e.target.value)} /></label>}
 <Button type="submit" disabled={busy || !/^[1-9][0-9]*$/.test(artifact) || (!!replaces && reason.trim().length<5)}>{t("upstreamConsume")}</Button>
      </form>
    </>}
  </section>
}
