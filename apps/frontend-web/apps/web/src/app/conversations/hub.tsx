import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { Popover, PopoverContent, PopoverTrigger } from "@workspace/ui/components/popover"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useRequestContract } from "../../lib/use-request-contract"
import { useAuth } from "../../components/auth"
import { useAllowed } from "../../lib/permissions"
import { MessageActions } from "./message-actions"
import { ConversationMarkdown } from "./message"
import { HubActivityCollection, HubExecutionCollection } from "./hub-collections"
import { conversationHubApi as api, directCreateRoute, directSendRoute, type HubChannel, type HumanContact, type DirectMessage } from "./hub-api"

export default function ConversationHubPage() {
  const { tenantID = "" } = useParams()
  return <ConversationHub key={tenantID} tenant={tenantID} />
}

function ConversationHub({ tenant }: { tenant: string }) {
  const t = useTranslations("iam")
  const [params, setParams] = useSearchParams()
  const channelID = params.get("direct") ?? ""
  const view = params.get("view") ?? "CHANNELS"
  const [channels, setChannels] = useState<HubChannel[]>([])
  const [cursor, setCursor] = useState("")
  const [next, setNext] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [revision, setRevision] = useState(0)
  const [opening, setOpening] = useState(false)
  const [pickerOpen, setPickerOpen] = useState(false)
  const projectIDs = [...new Set(channels.filter(channel => channel.kind !== "DIRECT" && channel.project_id).map(channel => channel.project_id!))]
  const attempt = useRef<{ peer: string; key: string } | null>(null)
  const action = useRef<AbortController | null>(null)
  const route = useMemo(() => directCreateRoute(tenant), [tenant])
  const metadata = useRequestContract(route)
  useEffect(() => () => action.current?.abort(), [])
  useEffect(() => {
    const controller = new AbortController()
    void api.list(tenant, cursor, controller.signal).then(page => {
      if (controller.signal.aborted) return
      setChannels(page.items ?? []); setNext(page.next_cursor ?? ""); setError(""); setLoading(false)
    }).catch((cause: Error) => {
      if (!controller.signal.aborted) { setChannels([]); setNext(""); setError(cause.message); setLoading(false) }
    })
    return () => controller.abort()
  }, [tenant, cursor, revision])
  const refresh = () => { setLoading(true); setRevision(value => value + 1) }
  const read = useCallback(() => setRevision(value => value + 1), [])
  async function open(peer: HumanContact) {
    if (opening || !metadata.contract) return
    setOpening(true); setError("")
    if (attempt.current?.peer !== peer.id) attempt.current = { peer: peer.id, key: crypto.randomUUID() }
    const controller = new AbortController(); action.current = controller
    try {
      const channel = await api.open(tenant, peer.id, attempt.current.key, controller.signal)
      if (controller.signal.aborted) return
      attempt.current = null; setPickerOpen(false); setParams({ direct: channel.id }); setCursor(""); refresh()
    } catch (cause) { if (!controller.signal.aborted) setError((cause as Error).message) }
    finally { if (!controller.signal.aborted) setOpening(false) }
  }
  return <main>
    <PageHeader title={t("conversations")} description={t("conversationHubHint")} action={<Button variant="outline" onClick={refresh}>{t("refresh")}</Button>} />
    {error && <Alert>{error}</Alert>}
    <nav className="mb-5 flex flex-wrap gap-2" aria-label={t("conversations")}>{["CHANNELS", "MENTIONS", "REPLIES", "EXECUTIONS"].map(value => <Button key={value} variant={view === value ? "default" : "outline"} aria-current={view === value ? "page" : undefined} onClick={() => setParams({view: value})}>{t(`conversationHub_${value}`)}</Button>)}</nav>
    {view === "MENTIONS" || view === "REPLIES" ? <HubActivityCollection key={`${tenant}:${view}`} tenant={tenant} kind={view} /> : view === "EXECUTIONS" ? <HubExecutionCollection key={tenant} tenant={tenant} /> : <>
    {metadata.failed && <Alert>{t("selfProfileContractFailed")} <Button variant="outline" onClick={metadata.retry}>{t("refresh")}</Button></Alert>}
    <div className="grid gap-6 lg:grid-cols-[minmax(15rem,20rem)_minmax(0,1fr)]">
      <aside className="min-w-0 space-y-6">
        <Popover open={pickerOpen} onOpenChange={setPickerOpen}>
          <PopoverTrigger disabled={opening || !metadata.contract} className="flex min-h-10 w-full items-center justify-between rounded-md border px-3 text-sm">{opening ? t("loading") : t("conversationStartDirect")} <span aria-hidden="true">⌄</span></PopoverTrigger>
          <PopoverContent align="start" className="w-[min(22rem,calc(100vw-2rem))] p-2">
            {pickerOpen && <HumanContactPicker tenant={tenant} busy={opening} onSelect={open} />}
          </PopoverContent>
        </Popover>
        {loading && <p role="status">{t("loading")}</p>}
        {(["PROJECT", "DIRECT"] as const).map(group => <section key={group} aria-label={t(group === "DIRECT" ? "conversationDirectGroup" : "conversationProjectGroup")}>
          <h2 className="mb-2 text-sm font-semibold">{t(group === "DIRECT" ? "conversationDirectGroup" : "conversationProjectGroup")}</h2>
          {group === "DIRECT" ? <ul className="space-y-1">{channels.filter(channel => channel.kind === "DIRECT").map(channel => <li key={channel.id}>
            <Button variant={channelID === channel.id ? "secondary" : "ghost"} className="h-auto min-h-11 w-full justify-between whitespace-normal text-left" onClick={() => setParams({ direct: channel.id })}><span className="break-all">{channel.name}</span><span>{channel.unread || ""}</span></Button>
          </li>)}</ul> : <ul className="space-y-4">{projectIDs.map(project => <li key={project}>
            <h3 className="mb-1 break-all px-3 text-xs text-muted-foreground">{t("conversationProjectGroup")} · {project}</h3>
            <ul className="space-y-1">{channels.filter(channel => channel.kind !== "DIRECT" && channel.project_id === project).map(channel => <li key={channel.id}>
              <Link className="flex min-h-11 items-center justify-between gap-2 rounded-md px-3 text-sm hover:bg-muted" to={`/admin/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(project)}/conversations?channel=${encodeURIComponent(channel.id)}`}><span className="break-all">{channel.name}</span><span>{channel.unread || ""}</span></Link>
            </li>)}</ul>
          </li>)}</ul>}
          {!loading && !channels.some(channel => (channel.kind === "DIRECT") === (group === "DIRECT")) && <p className="text-sm text-muted-foreground">{t("conversationNoChannels")}</p>}
        </section>)}
        <div className="flex gap-2"><Button variant="outline" disabled={!cursor || loading} onClick={() => { setLoading(true); setCursor("") }}>{t("conversationHubFirstPage")}</Button><Button variant="outline" disabled={!next || loading} onClick={() => { setLoading(true); setCursor(next) }}>{t("next")}</Button></div>
      </aside>
      {channelID ? <DirectConversation key={`${tenant}:${channelID}`} tenant={tenant} channel={channelID} onRead={read} /> : <p className="py-6 text-sm text-muted-foreground">{t("conversationChooseDirect")}</p>}
    </div>
    </>}
  </main>
}

function HumanContactPicker({ tenant, busy, onSelect }: { tenant: string; busy: boolean; onSelect: (contact: HumanContact) => Promise<void> }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [kind, setKind] = useState<"HUMAN" | "AGENT">("HUMAN")
  const id = useId()
  const [search, setSearch] = useState("")
  const [items, setItems] = useState<HumanContact[]>([])
  const [after, setAfter] = useState("")
  const [next, setNext] = useState("")
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [active, setActive] = useState(0)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    const timer = setTimeout(() => { void api.contacts(tenant, search, after, controller.signal, kind).then(page => {
      if (!controller.signal.aborted) { setItems(page.items ?? []); setNext(page.next_after ?? ""); setActive(0); setError(""); setLoading(false) }
    }).catch((cause: Error) => { if (!controller.signal.aborted) { setError(cause.message); setItems([]); setNext(""); setLoading(false) } }) }, 200)
    return () => { clearTimeout(timer); controller.abort() }
  }, [tenant, search, after, revision, kind])
  return <div className="space-y-2">
    {allowed("agent_read") && <select aria-label={t("conversationContactKind")} className="min-h-11 w-full rounded border bg-background px-3" value={kind} disabled={busy} onChange={event => { setKind(event.target.value as "HUMAN" | "AGENT"); setAfter(""); setItems([]); setLoading(true) }}><option value="HUMAN">{t("conversationContactHumans")}</option><option value="AGENT">{t("conversationContactAgents")}</option></select>}
    <Input role="combobox" aria-label={t("conversationContactSearch")} aria-controls={id} aria-expanded="true" aria-autocomplete="list" aria-activedescendant={!loading && items[active] ? `${id}-${active}` : undefined} value={search} placeholder={t("conversationContactSearch")} disabled={busy} onChange={event => { setSearch(event.target.value); setAfter(""); setLoading(true); setItems([]) }} onKeyDown={event => {
      if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); setActive(value => Math.max(0, Math.min(items.length - 1, value + (event.key === "ArrowDown" ? 1 : -1)))) }
      if (event.key === "Enter" && !loading && items[active] && !busy) { event.preventDefault(); void onSelect(items[active]) }
    }} />
    {loading && <p role="status" className="px-2 text-sm">{t("loading")}</p>}
    {error && <Alert>{error}<Button variant="outline" onClick={() => { setLoading(true); setRevision(value => value + 1) }}>{t("refresh")}</Button></Alert>}
    <ul id={id} role="listbox" aria-label={t("conversationContactSearch")} className="max-h-64 overflow-y-auto">{!loading && items.map((contact, index) => <li role="option" aria-selected={active === index} id={`${id}-${index}`} key={contact.id}><button type="button" className={`min-h-11 w-full rounded px-3 text-left text-sm break-all hover:bg-muted ${active === index ? "bg-muted" : ""}`} disabled={busy} onClick={() => void onSelect(contact)}>{contact.display_name} <span className="text-xs text-muted-foreground">{contact.id}</span></button></li>)}</ul>
    {!loading && !error && !items.length && <p className="px-2 text-sm text-muted-foreground">{t("conversationContactEmpty")}</p>}
    {(next || after) && <div className="flex gap-2"><Button variant="ghost" disabled={!after || loading || busy} onClick={() => { setAfter(""); setLoading(true); setItems([]) }}>{t("conversationHubFirstPage")}</Button><Button variant="ghost" disabled={!next || loading || busy} onClick={() => { setAfter(next); setLoading(true); setItems([]) }}>{t("next")}</Button></div>}
  </div>
}

function DirectConversation({ tenant, channel, onRead }: { tenant: string; channel: string; onRead: () => void }) {
  const { session } = useAuth()
  const allowed = useAllowed()
  const t = useTranslations("iam")
  const [messages, setMessages] = useState<DirectMessage[]>([])
  const [cursor, setCursor] = useState("")
  const [next, setNext] = useState("")
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [draft, setDraft] = useState("")
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const action = useRef<AbortController | null>(null)
  const route = useMemo(() => directSendRoute(tenant, channel), [tenant, channel])
  const metadata = useRequestContract(route)
  useEffect(() => () => action.current?.abort(), [])
  useEffect(() => {
    const controller = new AbortController()
    void api.messages(tenant, channel, cursor, controller.signal).then(async page => {
      if (!controller.signal.aborted) {
        setMessages(page.items ?? []); setNext(page.next_cursor ?? ""); setLoading(false); setError("")
        const latest = page.items?.[0]?.seq
        if (!cursor && latest) {
          await api.markRead(tenant, channel, latest, controller.signal)
          if (!controller.signal.aborted) onRead()
        }
      }
    }).catch((cause: Error) => { if (!controller.signal.aborted) { setMessages([]); setNext(""); setLoading(false); setError(cause.message) } })
    return () => controller.abort()
  }, [tenant, channel, cursor, revision, onRead])
  return <section className="min-w-0 space-y-4" aria-label={t("conversationDirectGroup")}>
    <div className="flex items-center justify-between gap-2"><h2 className="break-all font-semibold">{t("conversationDirectGroup")} · {channel}</h2><Button variant="outline" onClick={() => { setLoading(true); setRevision(value => value + 1) }}>{t("refresh")}</Button></div>
    <p className="text-sm text-muted-foreground">{t("conversationDirectHint")}</p>
    {error && <Alert>{error}</Alert>}
    {loading ? <p role="status">{t("loading")}</p> : <ol className="space-y-4" aria-live="polite">{[...messages].reverse().map(message => <li key={message.id} className="border-b pb-3"><p className="mb-1 text-xs text-muted-foreground">{message.author_id} · {message.created_at}</p><div className="break-words">{!message.retracted && <ConversationMarkdown body={message.body} />}<MessageActions tenant={tenant} message={message} actor={session?.subject} audit={allowed("audit_read")} onChanged={() => { setLoading(true); setRevision(value => value + 1) }} /></div></li>)}</ol>}
    {!loading && !error && !messages.length && <p>{t("conversationEmpty")}</p>}
    <div className="flex gap-2"><Button variant="outline" disabled={!cursor || loading} onClick={() => { setCursor(""); setLoading(true) }}>{t("conversationHubLatest")}</Button><Button variant="outline" disabled={!next || loading} onClick={() => { setCursor(next); setLoading(true) }}>{t("conversationOlder")}</Button></div>
    {metadata.failed && <Alert>{t("selfProfileContractFailed")}<Button variant="outline" onClick={metadata.retry}>{t("refresh")}</Button></Alert>}
    <form className="space-y-2" onSubmit={event => {
      event.preventDefault()
      if (busy || loading || error || !metadata.contract || !draft.trim()) return
      const body = draft.trim()
      if (attempt.current?.body !== body) attempt.current = { body, key: crypto.randomUUID() }
      const controller = new AbortController(); action.current = controller; setBusy(true)
      void api.send(tenant, channel, body, attempt.current.key, controller.signal).then(() => {
        if (!controller.signal.aborted) { attempt.current = null; setDraft(""); setCursor(""); setLoading(true); setRevision(value => value + 1) }
      }).catch((cause: Error) => { if (!controller.signal.aborted) setError(cause.message) }).finally(() => { if (!controller.signal.aborted) setBusy(false) })
    }}><Textarea aria-label={t("conversationSend")} maxLength={8000} value={draft} onChange={event => setDraft(event.target.value)} disabled={busy || !metadata.contract} /><Button type="submit" disabled={busy || loading || !!error || !metadata.contract || !draft.trim()}>{busy ? t("loading") : t("conversationSend")}</Button></form>
  </section>
}
