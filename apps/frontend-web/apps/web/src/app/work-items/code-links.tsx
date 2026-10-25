import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

export type CodeLink = { id: string; kind: string; repository: string; ref: string; url: string; provenance: string; content_sha256: string }
function validCodeReference(kind: string, ref: string) {
  return ref.trim().length > 0 && ref.length <= 200 && !/[\r\n\0]/.test(ref) && (kind !== "COMMIT" || /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(ref))
}
export function WorkCodeLinks({ entityID, detail, targetKind = "work_item" }: { entityID: string; detail: { id?: string | null; version?: number }; targetKind?: "work_item" | "requirement" }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [rows, setRows] = useState<CodeLink[]>([])
  const [kind, setKind] = useState("COMMIT")
  const [repository, setRepository] = useState("")
  const [ref, setRef] = useState("")
  const [url, setURL] = useState("")
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [evidence, setEvidence] = useState<{ snapshot: string; sha256: string } | null>(null)
  const pending = useRef(false)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const path = `/entities/${encodeURIComponent(entityID)}/${targetKind === "requirement" ? "requirements" : "work-items"}/${encodeURIComponent(detail.id!)}/code-links`
  useEffect(() => {
    const controller = new AbortController()
    void iamApi.deliveryResultRequest<CodeLink[]>({ method: "GET", url: path }, { signal: controller.signal })
      .then(value => { if (!controller.signal.aborted) setRows(value) })
      .catch(e => { if (!controller.signal.aborted) setError(e instanceof Error ? e.message : String(e)) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [path, detail.version])
  const add = async () => {
    if (pending.current) return
    pending.current = true; setBusy(true); setError("")
    const body = JSON.stringify({ kind, repository_id: repository, ref, url })
    if (attempt.current?.body !== body) attempt.current = { body, key: crypto.randomUUID() }
    try {
      await iamApi.deliveryResultRequest({ method: "POST", url: path }, { body, headers: { "Idempotency-Key": attempt.current.key } })
      attempt.current = null
      setRows(await iamApi.deliveryResultRequest<CodeLink[]>({ method: "GET", url: path }))
      setRef(""); setURL("")
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) }
    finally { pending.current = false; setBusy(false) }
  }
  const inspect = async (id: string) => {
    if (pending.current) return
    pending.current = true; setBusy(true); setError(""); setEvidence(null)
    try { setEvidence(await iamApi.deliveryResultRequest<{ snapshot: string; sha256: string }>({ method: "GET", url: `${path}/${encodeURIComponent(id)}/evidence` })) }
    catch (e) { setError(e instanceof Error ? e.message : String(e)) }
    finally { pending.current = false; setBusy(false) }
  }
  return <section className="space-y-3 border-t pt-4" aria-label={t("codeLinksTitle")}>
    <h3 className="font-medium">{t("codeLinksTitle")}</h3>
    <p className="text-sm text-muted-foreground">{t("codeLinksHint")}</p>
    {error && <Alert>{error}</Alert>}
    {loading ? <p>{t("loading")}</p> : rows.length === 0 ? <p>{t("codeLinksEmpty")}</p> : <ul className="space-y-3">
      {rows.map(row => <li key={row.id} className="space-y-1 break-all border-b pb-3">
        <p>{row.kind} · {row.repository}</p>
        {row.url && /^https:\/\//i.test(row.url) ? <a className="underline" href={row.url} target="_blank" rel="noopener noreferrer">{row.ref}</a> : <code>{row.ref}</code>}
        <p className="text-sm">{t(row.provenance === "LOCAL_GIT_OBSERVED" ? "codeLinksObserved" : "codeLinksManual")}</p>
        <Button variant="outline" disabled={busy} onClick={() => void inspect(row.id)}>{t("codeLinksEvidence")}</Button>
      </li>)}
    </ul>}
    {evidence && <div className="space-y-2"><code className="block break-all text-xs">SHA-256: {evidence.sha256}</code><pre className="whitespace-pre-wrap break-all text-xs">{evidence.snapshot}</pre></div>}
    {allowed(targetKind === "requirement" ? "requirement_write" : "work_item_write") && <form className="flex flex-wrap items-end gap-2" onSubmit={event => { event.preventDefault(); void add() }}>
      <label className="grid gap-1"><span>{t("codeLinksKind")}</span><select className="h-9 rounded-md border bg-background px-3" disabled={busy} value={kind} onChange={event => setKind(event.target.value)}><option value="COMMIT">Commit</option><option value="MERGE_REQUEST">Merge request</option><option value="BRANCH">Branch</option></select></label>
      <label><span>{t("codeLinksRepository")}</span><Input required inputMode="numeric" pattern="[1-9][0-9]*" disabled={busy} value={repository} onChange={event => setRepository(event.target.value)} /></label>
      <label><span>{t("codeLinksRef")}</span><Input required maxLength={200} disabled={busy} value={ref} onChange={event => setRef(event.target.value)} /></label>
      <label><span>{t("codeLinksURL")}</span><Input type="url" pattern="https://.*" maxLength={500} disabled={busy} value={url} onChange={event => setURL(event.target.value)} /></label>
      <Button type="submit" disabled={busy || loading || !/^[1-9][0-9]*$/.test(repository) || !validCodeReference(kind, ref)}>{t("codeLinksAdd")}</Button>
    </form>}
  </section>
}
