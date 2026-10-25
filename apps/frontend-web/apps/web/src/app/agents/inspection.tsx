import { useEffect, useState } from "react"
import { Link, useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import type { components } from "../../lib/api-schema"
import { ConversationMarkdown } from "../conversations/message"
import { agentInspectionApi, type AgentDirectChannel, type AgentDirectMessage, type WorkspacePage } from "./inspection-api"
import { Input } from "@workspace/ui/components/input"

export function AgentInspection({ tenant, agent, tab }: { tenant: string; agent: string; tab: "work" | "direct" | "workspace" }) {
  const t = useTranslations("iam")
  if (!agent) return <p className="p-6 text-muted-foreground">{t("agentChoose")}</p>
  if (tab === "workspace") return <AgentWorkspace key={`${tenant}:${agent}`} tenant={tenant} agent={agent} />
  return tab === "work" ? <AgentWork tenant={tenant} agent={agent} /> : <AgentDirect tenant={tenant} agent={agent} />
}

function AgentWorkspace({ tenant, agent }: { tenant: string; agent: string }) {
  const t = useTranslations("iam")
  const [path, setPath] = useState("")
  const [file, setFile] = useState("")
  const [content, setContent] = useState("")
  const [page, setPage] = useState<WorkspacePage | null>(null)
  const [offset, setOffset] = useState(0)
  const [search, setSearch] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    const controller = new AbortController()
    void agentInspectionApi.workspace(tenant, agent, path, offset, search, controller.signal).then(value => {
      if (!controller.signal.aborted) { setPage(value); setError("") }
    }).catch((cause: unknown) => { if (!controller.signal.aborted) { setPage(null); setError(cause instanceof Error ? cause.message : t("agentDirectoryUnavailable")) } }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [tenant, agent, path, offset, search, t])
  useEffect(() => {
    if (!file) return
    const controller = new AbortController()
    void agentInspectionApi.content(tenant, agent, file, controller.signal).then(value => {
      if (!controller.signal.aborted) { setContent(value.content); setError("") }
    }).catch((cause: unknown) => { if (!controller.signal.aborted) { setContent(""); setError(cause instanceof Error ? cause.message : t("agentDirectoryUnavailable")) } })
    return () => controller.abort()
  }, [tenant, agent, file, t])
  const navigate = (next: string) => { setLoading(true); setPath(next); setOffset(0); setFile(""); setContent("") }
  return <section className="space-y-4 min-w-0" aria-label={t("agentTab_workspace")} aria-busy={loading}>
    <p className="text-sm text-muted-foreground">{t("agentWorkspaceReadOnly")}</p>
    <nav className="flex flex-wrap gap-2" aria-label={t("agentWorkspacePath")}>
      <Button variant="outline" onClick={() => navigate("")}>/</Button>
      {path.split("/").filter(Boolean).map((part, index, parts) => <Button key={index} variant="outline" onClick={() => navigate(parts.slice(0, index + 1).join("/"))}>{part}</Button>)}
    </nav>
    <Input aria-label={t("search")} value={search} maxLength={200} onChange={event => { setLoading(true); setOffset(0); setSearch(event.target.value) }} />
    {error && <Alert>{error}</Alert>}
    {loading ? <p role="status">{t("loading")}</p> : <ul className="divide-y border-y">{page?.items.map(entry => <li key={entry.name} className="flex items-center gap-3 py-2 min-w-0">
      <Button className="min-w-0 max-w-full truncate" variant="ghost" onClick={() => { const next = [path, entry.name].filter(Boolean).join("/"); if (entry.kind === "DIRECTORY") navigate(next); else { setContent(""); setFile(next) } }}>{entry.kind === "DIRECTORY" ? "▸ " : ""}{entry.name}</Button><span className="text-xs text-muted-foreground">{entry.size} B</span>
    </li>)}</ul>}
    <div className="flex gap-2">{offset > 0 && <Button variant="outline" onClick={() => { setLoading(true); setOffset(Math.max(0, offset - 50)) }}>{t("agentFirstPage")}</Button>}{page?.has_more && <Button variant="outline" onClick={() => { setLoading(true); setOffset(offset + 50) }}>{t("agentMore")}</Button>}</div>
    {file && <div className="min-w-0"><h3 className="break-all font-medium">{file}</h3><pre className="overflow-auto max-h-96 border p-4 text-sm whitespace-pre-wrap break-all">{content}</pre></div>}
  </section>
}

function AgentWork({ tenant, agent }: { tenant: string; agent: string }) {
  const t = useTranslations("iam")
  const [page, setPage] = useState<components["schemas"]["PersonalWorkPage"] | null>(null)
  const [cursor, setCursor] = useState("")
  const [revision, setRevision] = useState(0)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    const controller = new AbortController()
    void agentInspectionApi.work(tenant, agent, cursor, controller.signal).then(next => {
      if (!controller.signal.aborted) { setPage(next); setError("") }
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) { setPage(null); setError(cause instanceof Error ? cause.message : t("workUnavailable")) }
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [tenant, agent, cursor, revision, t])
  return <section className="space-y-4" aria-label={t("agentTab_work")} aria-busy={loading}>
    <Button variant="outline" disabled={loading} onClick={() => { setLoading(true); setRevision(value => value + 1) }}>{t("agentRefresh")}</Button>
    {error && <Alert>{error}</Alert>}
    {loading ? <p role="status">{t("loading")}</p> : <>
      {!error && !page?.items?.length && <p>{t("workEmpty")}</p>}
      <ul className="divide-y border-y">{page?.items?.map(work => <li key={work.id} className="space-y-2 py-4">
        <strong>#{work.number} {work.title}</strong>
        <p className="text-sm">{t.has(`workState_${work.state}`) ? t(`workState_${work.state}`) : work.state} · {t(`personalWork_${work.responsibility}`)}</p>
        {work.project_id && work.id && <Link className="text-sm underline" to={`/admin/${tenant}/entities/${work.project_id}/work-items?work=${encodeURIComponent(work.id)}`}>{t("agentOpenProjectWork")}</Link>}
      </li>)}</ul>
      <div className="flex gap-2">
        {cursor && <Button variant="outline" onClick={() => { setLoading(true); setCursor("") }}>{t("agentFirstPage")}</Button>}
        {page?.next_cursor && <Button variant="outline" onClick={() => { setLoading(true); setCursor(page.next_cursor!) }}>{t("agentMore")}</Button>}
      </div>
    </>}
  </section>
}

function AgentDirect({ tenant, agent }: { tenant: string; agent: string }) {
  const t = useTranslations("iam")
  const [params, setParams] = useSearchParams()
  const channel = params.get("direct") ?? ""
  const [channels, setChannels] = useState<AgentDirectChannel[]>([])
  const [messages, setMessages] = useState<AgentDirectMessage[]>([])
  const [channelCursor, setChannelCursor] = useState("")
  const [nextChannel, setNextChannel] = useState("")
  const [cursor, setCursor] = useState("")
  const [nextMessage, setNextMessage] = useState("")
  const [revision, setRevision] = useState(0)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const identity = `${channel}:${cursor}:${revision}`
  const [loaded, setLoaded] = useState("")
  useEffect(() => {
    const controller = new AbortController()
    void agentInspectionApi.channels(tenant, agent, channelCursor, controller.signal).then(page => {
      if (!controller.signal.aborted) { setChannels(page.items ?? []); setNextChannel(page.next_cursor ?? ""); setError("") }
    }).catch((cause: unknown) => { if (!controller.signal.aborted) { setChannels([]); setError(cause instanceof Error ? cause.message : t("workUnavailable")) } }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [tenant, agent, channelCursor, revision, t])
  useEffect(() => {
    if (!channel) return
    const controller = new AbortController()
    void agentInspectionApi.messages(tenant, agent, channel, cursor, controller.signal).then(page => {
      if (!controller.signal.aborted) { setMessages(page.items ?? []); setNextMessage(page.next_cursor ?? ""); setError(""); setLoaded(identity) }
    }).catch((cause: unknown) => { if (!controller.signal.aborted) { setMessages([]); setError(cause instanceof Error ? cause.message : t("workUnavailable")); setLoaded(identity) } })
    return () => controller.abort()
  }, [tenant, agent, channel, cursor, revision, identity, t])
  return <section className="space-y-4" aria-label={t("agentTab_direct")}>
    <p className="text-sm text-muted-foreground">{t("agentDirectReadOnly")}</p>
    <Button variant="outline" onClick={() => { setLoading(true); setRevision(value => value + 1) }}>{t("agentRefresh")}</Button>
    {error && <Alert>{error}</Alert>}
    {loading ? <p role="status">{t("loading")}</p> : <nav className="flex flex-wrap gap-2" aria-label={t("agentTab_direct")}>
      {!error && !channels.length && <p>{t("conversationEmpty")}</p>}
      {channels.map(item => item.id && <Button key={item.id} variant={channel === item.id ? "default" : "outline"} onClick={() => { setCursor(""); setParams(old => { const next = new URLSearchParams(old); next.set("direct", item.id!); return next }) }}>{item.name}</Button>)}
      {channelCursor && <Button variant="outline" onClick={() => { setLoading(true); setChannelCursor("") }}>{t("agentFirstPage")}</Button>}
      {nextChannel && <Button variant="outline" onClick={() => { setLoading(true); setChannelCursor(nextChannel) }}>{t("agentMore")}</Button>}
    </nav>}
    {channel && (loaded !== identity ? <p role="status">{t("loading")}</p> : <>
      {!error && !messages.length && <p>{t("conversationEmpty")}</p>}
      <ol className="divide-y border-y">{[...messages].reverse().map(message => <li key={message.id} className="space-y-2 py-4">
        <div className="flex flex-wrap gap-3 text-xs text-muted-foreground"><span>{message.author_id}</span><time dateTime={message.created_at}>{new Date(message.created_at).toLocaleString()}</time></div>
        <ConversationMarkdown body={message.body} />
      </li>)}</ol>
      {cursor && <Button variant="outline" onClick={() => setCursor("")}>{t("agentFirstPage")}</Button>}
      {nextMessage && <Button variant="outline" onClick={() => setCursor(nextMessage)}>{t("conversationOlder")}</Button>}
    </>)}
  </section>
}
