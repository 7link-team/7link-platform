import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { channelMediaApi, type ChannelConnector, type ConnectorKind, type SearchCard } from "./media-api"

export function ChannelSearchCard({ card }: { card: SearchCard }) {
  return <section className="min-w-0 space-y-3 rounded border p-3"><p className="break-words font-medium">{card.query}</p><ol className="list-decimal space-y-2 ps-5">{card.sources.map((source, index) => <li key={index}><a href={source.url} target="_blank" rel="noopener noreferrer" className="break-all text-primary underline">{source.title}</a><p className="break-words text-sm">{source.summary}</p></li>)}</ol>{card.conclusions.map((conclusion, index) => <p key={index} className="break-words">{conclusion.text} {conclusion.sources.map(source => <a key={source} href={card.sources[source - 1]?.url} target="_blank" rel="noopener noreferrer" className="text-primary underline">[{source}]</a>)}</p>)}</section>
}
export function ChannelMediaTools({ channel }: { channel: string }) {
  const t = useTranslations("iam")
  const [open, setOpen] = useState(false)
  const [connectors, setConnectors] = useState<ChannelConnector[]>([])
  const [kind, setKind] = useState<ConnectorKind>("HOOK")
  const [endpoint, setEndpoint] = useState("")
  const [secret, setSecret] = useState("")
  const [enabled, setEnabled] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [query, setQuery] = useState("")
  const [approved, setApproved] = useState(false)
  const [card, setCard] = useState<SearchCard | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const action = useRef<AbortController | null>(null)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const searchAttempt = useRef<{ body: string; key: string } | null>(null)
  useEffect(() => () => action.current?.abort(), [])
  const current = connectors.find(c => c.kind === kind)
  function select(next: ConnectorKind, rows = connectors) { const c = rows.find(c => c.kind === next); setKind(next); setEndpoint(c?.endpoint ?? ""); setEnabled(c?.enabled ?? false); setSecret("") }
  async function load() {
    const controller = new AbortController(); action.current?.abort(); action.current = controller; setBusy(true); setError("")
    try { const page = await channelMediaApi.list(channel, controller.signal); if (!controller.signal.aborted) { setConnectors(page.items); select(kind, page.items); setLoaded(true) } }
    catch (cause) { if (!controller.signal.aborted) setError((cause as Error).message) }
    finally { if (!controller.signal.aborted) setBusy(false) }
  }
  async function save() {
    if (busy || !loaded) return
    const body = { version: current?.version ?? 0, endpoint, enabled, ...(secret ? { secret } : {}) }
    const serialized = JSON.stringify({ kind, ...body }); if (attempt.current?.body !== serialized) attempt.current = { body: serialized, key: crypto.randomUUID() }
    const controller = new AbortController(); action.current = controller; setBusy(true); setError("")
    try { const row = await channelMediaApi.configure(channel, kind, body, attempt.current.key, controller.signal); if (!controller.signal.aborted) { setConnectors(rows => [...rows.filter(c => c.kind !== kind), row]); setSecret(""); attempt.current = null } }
    catch (cause) { if (!controller.signal.aborted) setError((cause as Error).message) }
    finally { if (!controller.signal.aborted) setBusy(false) }
  }
  async function search() {
    if (busy || !approved || !query.trim()) return
    const body = JSON.stringify({ channel, query, approved })
    if (searchAttempt.current?.body !== body) searchAttempt.current = { body, key: crypto.randomUUID() }
    const controller = new AbortController(); action.current = controller; setBusy(true); setError(""); setCard(null)
    try { const result = await channelMediaApi.search(channel, query, approved, searchAttempt.current.key, controller.signal); if (!controller.signal.aborted) { setCard(result); searchAttempt.current = null } }
    catch (cause) { if (!controller.signal.aborted) setError((cause as Error).message) }
    finally { if (!controller.signal.aborted) setBusy(false) }
  }
  return <section className="space-y-3"><Button variant="outline" aria-expanded={open} onClick={() => { setOpen(value => !value); if (!open) void load() }}>{t("conversationConnectorSettings")}</Button>{open && <div className="space-y-3 rounded border p-3">
    <p className="text-sm text-muted-foreground">{t("conversationConnectorBoundary")}</p>
    {error && <Alert>{error}</Alert>}
    <form className="space-y-3" onSubmit={event => { event.preventDefault(); void save() }}>
      <label className="grid gap-2 text-sm">{t("conversationConnectorKind")}<select className="rounded border bg-background p-2" value={kind} disabled={busy} onChange={event => select(event.target.value as ConnectorKind)}>{(["HOOK", "HTTP_BRIDGE", "TRANSCRIPTION", "SEARCH"] as const).map(value => <option key={value} value={value}>{t(`conversationConnector_${value}`)}</option>)}</select></label>
      <label className="grid gap-2 text-sm">{t("conversationConnectorEndpoint")}<Input type="url" value={endpoint} required maxLength={512} disabled={busy} onChange={event => setEndpoint(event.target.value)} /></label>
      <label className="grid gap-2 text-sm">{t("conversationConnectorSecret")}<Input type="password" autoComplete="new-password" value={secret} required={!current} minLength={32} maxLength={512} disabled={busy} onChange={event => setSecret(event.target.value)} /></label>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={enabled} disabled={busy} onChange={event => setEnabled(event.target.checked)} />{t("conversationConnectorEnabled")}</label>
      <Button type="submit" disabled={busy || !loaded}>{t("save")}</Button><Button type="button" variant="outline" disabled={busy} onClick={() => void load()}>{t("refresh")}</Button>
    </form>
    {connectors.some(c => c.kind === "SEARCH" && c.enabled) && <form className="space-y-3 border-t pt-3" onSubmit={event => { event.preventDefault(); void search() }}>
      <label className="grid gap-2 text-sm">{t("conversationSearchQuery")}<Input value={query} maxLength={200} required disabled={busy} onChange={event => { setQuery(event.target.value); setApproved(false); setCard(null) }} /></label>
      <label className="flex items-start gap-2 text-sm"><input type="checkbox" checked={approved} disabled={busy} onChange={event => setApproved(event.target.checked)} />{t("conversationSearchApproval")}</label><Button disabled={busy || !approved || !query.trim()}>{t("search")}</Button>
    </form>}{busy && <p role="status">{t("loading")}</p>}{card && <ChannelSearchCard card={card} />}
  </div>}</section>
}
