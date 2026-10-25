import { SystemMessageContent } from "./system-messages"
import { foldSystemMessages } from "./system-message-folding"
import { ChannelMediaTools } from "./media-tools"
import { ConversationAttachment } from "./attachment-preview"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useParams, useSearchParams } from "react-router"
import { Bookmark, Hash, MessageSquare, Plus, Users } from "lucide-react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useAuth } from "../../components/auth"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { apiRoute } from "../../lib/api-route"
import {
  iamApi,
  type ConversationChannel,
  type ConversationMember,
  type ConversationMessage,
  type ConversationMessageQuery,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { useRequestContract } from "../../lib/use-request-contract"
import { MessageActions } from "./message-actions"
import { WorkConversion } from "./work-conversion"
import { ComposerUpload } from "./upload"
import { ChannelControls } from "./channel-controls"
import { channelLifecycleApi } from "./channel-controls-api"
import { useRealtime } from "@workspace/ui/hooks/use-realtime"
import type { components } from "../../lib/api-schema"

export default function ConversationsPage() {
  const { tenantID = "", entityID = "" } = useParams()
  return (
    <Conversations
      key={`${tenantID}:${entityID}`}
      entityID={entityID}
      tenantID={tenantID}
    />
  )
}

function Conversations({
  entityID,
  tenantID,
}: {
  entityID: string
  tenantID: string
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const { session } = useAuth()
  const [params, setParams] = useSearchParams()
  const channelID = params.get("channel") ?? ""
  const threadID = params.get("thread") ?? ""
  const activity = (params.get("activity") ??
    "all") as ConversationMessageQuery["activity"]
  const [channels, setChannels] = useState<ConversationChannel[]>([])
  const [archived, setArchived] = useState(false)
  const [channelCursor, setChannelCursor] = useState("")
  const [messages, setMessages] = useState<ConversationMessage[]>([])
  const [messageCursor, setMessageCursor] = useState("")
  const [members, setMembers] = useState<ConversationMember[]>([])
  const [membersOpen, setMembersOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)
  const [draft, setDraft] = useState("")
  const [converting, setConverting] = useState("")
  const [asTask, setAsTask] = useState(false)
  const [uploading, setUploading] = useState(false)
  const realtimeRefresh = useRef<(() => Promise<void>) | null>(null)
  const [attachmentFile, setAttachmentFile] = useState("")
  const [attachments, setAttachments] = useState<
    components["schemas"]["AttachmentCandidate"][]
  >([])
  const [resolving, setResolving] = useState(false)
  const [internal, setInternal] = useState(false)
  const [mentions, setMentions] = useState<string[]>([])
  const [search, setSearch] = useState("")
  const [query, setQuery] = useState("")
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const selected = channels.find((c) => c.id === channelID)
  const selection = `${channelID}:${threadID}:${activity}:${query}`
  const selectionRef = useRef(selection)
  useEffect(() => {
    selectionRef.current = selection
  }, [selection])
  const loadChannels = useCallback(
    async (signal?: AbortSignal) => {
      const page = await channelLifecycleApi.list(
        entityID,
        archived,
        undefined,
        signal
      )
      setChannels(page.items ?? [])
      setChannelCursor(page.next_cursor ?? "")
    },
    [entityID, archived]
  )
  useEffect(() => {
    const controller = new AbortController()
    void channelLifecycleApi
      .list(entityID, archived, undefined, controller.signal)
      .then(
        (page) => {
          if (controller.signal.aborted) return
          setChannels(page.items ?? [])
          setChannelCursor(page.next_cursor ?? "")
        },
        (e: Error) => {
          if (!controller.signal.aborted) setError(e.message)
        }
      )
    return () => controller.abort()
  }, [entityID, archived])
  useEffect(() => {
    if (channelID || !channels[0]?.id) return
    setParams({ channel: channels[0].id }, { replace: true })
  }, [channelID, channels, setParams])
  useEffect(() => {
    if (!channelID) return
    const controller = new AbortController()
    let running = false
    const load = async (initial = false) => {
      if (running || document.hidden) return
      running = true
      try {
        const page = await iamApi.conversationMessages(
          channelID,
          {
            limit: 50,
            activity,
            search: query || undefined,
            reply_to_message_id: threadID || undefined,
          },
          controller.signal
        )
        if (controller.signal.aborted) return
        setMessages(page.items ?? [])
        setMessageCursor(page.next_cursor ?? "")
        setError("")
      } catch (e) {
        if (!controller.signal.aborted) {
          setError((e as Error).message)
          if (initial) setMessages([])
        }
      } finally {
        running = false
        if (!controller.signal.aborted) setLoading(false)
      }
    }
    void load(true)
    realtimeRefresh.current = () => load()
    const visibility = () => {
      if (!document.hidden) void load()
    }
    document.addEventListener("visibilitychange", visibility)
    return () => {
      controller.abort()
      realtimeRefresh.current = null
      document.removeEventListener("visibilitychange", visibility)
    }
  }, [channelID, activity, query, threadID])
  useRealtime(
    channelID
      ? [
          `tenant/${tenantID}/channel/${channelID}`,
          ...(session?.subject
            ? [`tenant/${tenantID}/inbox/${session.subject}`]
            : []),
          ...(threadID ? [`tenant/${tenantID}/thread/${threadID}`] : []),
        ]
      : null,
    {
      onEvent: () => {
        void realtimeRefresh.current?.()
        void loadChannels().catch((e: Error) => setError(e.message))
      },
      onResync: () => {
        void realtimeRefresh.current?.()
        void loadChannels().catch((e: Error) => setError(e.message))
      },
      poll: async () => {
        await realtimeRefresh.current?.()
        await loadChannels()
      },
      pollIntervalMs: 5000,
    }
  )
  useEffect(() => {
    if (!channelID) return
    const controller = new AbortController()
    void iamApi
      .conversationMembers(channelID, undefined, controller.signal)
      .then(
        (page) => {
          setMembers(page.items ?? [])
        },
        (e: Error) => {
          if (!controller.signal.aborted) setError(e.message)
        }
      )
    return () => controller.abort()
  }, [channelID])
  const navigate = (patch: Record<string, string>) => {
    const next = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v)
      else next.delete(k)
    }
    setParams(next)
  }
  const refresh = async () => {
    const current = selection
    const page = await iamApi.conversationMessages(channelID, {
      limit: 50,
      activity,
      search: query || undefined,
      reply_to_message_id: threadID || undefined,
    })
    if (selectionRef.current !== current) return
    setMessages(page.items ?? [])
    setMessageCursor(page.next_cursor ?? "")
    await loadChannels()
  }
  const send = async () => {
    if (!draft.trim() || busy || uploading) return
    const target = channelID
    const body = {
      body: draft.trim(),
      visibility: internal ? ("INTERNAL" as const) : ("CHANNEL" as const),
      reply_to_message_id: threadID || undefined,
      mention_principal_ids: mentions,
      attachments: attachments.map((a) => ({
        space_id: a.space_id,
        file_id: a.file_id,
        version_id: a.version_id,
        expected_file_version: a.expected_file_version,
      })),
    }
    const identity = JSON.stringify({ channel: target, ...body })
    const key =
      attempt.current?.body === identity
        ? attempt.current.key
        : crypto.randomUUID()
    attempt.current = { body: identity, key }
    setBusy(true)
    setError("")
    try {
      const posted = await iamApi.postConversationMessage(target, body, key)
      if (selectionRef.current !== selection) return
      setDraft("")
      setAttachments([])
      setMentions([])
      attempt.current = null
      if (asTask && posted.id) setConverting(posted.id)
      setAsTask(false)
      await refresh()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const moreMessages = async () => {
    const current = selection
    setBusy(true)
    try {
      const page = await iamApi.conversationMessages(channelID, {
        limit: 50,
        cursor: messageCursor,
        activity,
        search: query || undefined,
        reply_to_message_id: threadID || undefined,
      })
      if (selectionRef.current !== current) return
      setMessages((old) => [
        ...old,
        ...(page.items ?? []).filter((m) => !old.some((o) => o.id === m.id)),
      ])
      setMessageCursor(page.next_cursor ?? "")
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const author = (id: string | null) =>
    members.find((m) => m.principal_id === id)?.member_kind === "AGENT"
      ? `${t("conversationAgent")} ${id}`
      : id
  return (
    <div className="flex min-h-[70svh] min-w-0 flex-col gap-4">
      {converting && channelID && (
        <WorkConversion
          key={`${channelID}:${converting}`}
          channel={channelID}
          message={converting}
          tenant={tenantID}
          entity={entityID}
          onClose={() => setConverting("")}
        />
      )}
      <PageHeader
        title={t("conversations")}
        description={t("conversationDescription")}
      />
      {error && <Alert>{error}</Alert>}
      <div className="grid min-h-[60svh] min-w-0 overflow-hidden rounded-lg border md:grid-cols-[15rem_minmax(0,1fr)]">
        <aside
          className="space-y-3 border-b bg-muted/20 p-3 md:border-e md:border-b-0"
          aria-label={t("conversationChannels")}
        >
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold">
              {t("conversationChannels")}
            </h2>
            {allowed("channel_post") && (
              <Button
                variant="ghost"
                size="icon"
                aria-label={t("conversationCreate")}
                onClick={() => setCreating(!creating)}
              >
                <Plus className="size-4" />
              </Button>
            )}
          </div>
          <label className="flex min-h-11 items-center gap-2 text-sm"><input type="checkbox" checked={archived} onChange={event => { setArchived(event.target.checked); setChannels([]); setChannelCursor(""); navigate({ channel: "", thread: "" }) }} />{t("conversationArchivedChannels")}</label>
          {creating && (
            <CreateChannel
              entityID={entityID}
              onCreated={async (ch) => {
                setCreating(false)
                await loadChannels()
                if (ch.id) navigate({ channel: ch.id, thread: "" })
              }}
            />
          )}
          <nav className="flex gap-1 overflow-auto md:block md:space-y-1">
            {channels.map(
              (ch) =>
                ch.id && (
                  <button
                    key={ch.id}
                    className={`flex min-h-11 min-w-36 items-center gap-2 rounded-md px-3 py-2 text-start text-sm md:w-full ${ch.id === channelID ? "bg-primary/10 font-medium text-primary" : "hover:bg-accent"}`}
                    onClick={() => {
                      setDraft("")
                      setAttachments([])
                      setAttachmentFile("")
                      setConverting("")
                      setInternal(false)
                      setAsTask(false)
                      setMentions([])
                      navigate({ channel: ch.id!, thread: "" })
                    }}
                    aria-current={ch.id === channelID ? "page" : undefined}
                  >
                    <Hash className="size-4 shrink-0" aria-hidden="true" />
                    <span className="min-w-0 flex-1 truncate">{ch.name}</span>
                    {ch.unread > 0 && (
                      <span className="rounded bg-primary/10 px-1.5 text-xs">
                        {ch.unread}
                      </span>
                    )}
                  </button>
                )
            )}
          </nav>
          {channelCursor && (
            <Button
              variant="outline"
              disabled={busy}
              onClick={async () => {
                setBusy(true)
                try {
                  const p = await channelLifecycleApi.list(
                    entityID,
                    archived,
                    channelCursor
                  )
                  setChannels((old) => [...old, ...(p.items ?? [])])
                  setChannelCursor(p.next_cursor ?? "")
                } catch (e) {
                  setError((e as Error).message)
                } finally {
                  setBusy(false)
                }
              }}
            >
              {t("conversationMoreChannels")}
            </Button>
          )}
          {!channels.length && (
            <p className="text-sm text-muted-foreground">
              {t("conversationNoChannels")}
            </p>
          )}
        </aside>
        <section
          className="flex min-w-0 flex-col"
          aria-label={selected?.name ?? t("conversations")}
        >
          {selected && (
            <>
              <header className="space-y-3 border-b p-4">
                <ChannelControls key={`${selected.id}:${selected.version}`} channel={selected} actor={session?.subject ?? ""} manage={allowed("channel_manage") || members.some(member => member.principal_id === session?.subject && member.role === "OWNER")} onChanged={async () => { await loadChannels(); navigate({ channel: "", thread: "" }) }} />
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <h2 className="font-semibold">{selected.name}</h2>
                    <p className="text-sm text-muted-foreground">
                      {selected.topic}
                    </p>
                  </div>
                  <Button
                    variant="outline"
                    onClick={() => setMembersOpen(!membersOpen)}
                    aria-expanded={membersOpen}
                  >
                    <Users className="size-4" />
                    {t("conversationMembers")}
                  </Button>
                </div>
                {selected.visibility === "CLIENT_SHARED" && (
                  <p
                    role="note"
                    className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm font-medium"
                  >
                    {t("conversationClientPresent")}
                  </p>
                )}
                {membersOpen && (
                  <ul className="grid gap-2 text-sm sm:grid-cols-2">
                    {members.map((m) => (
                      <li
                        key={m.principal_id}
                        className="rounded border px-3 py-2"
                      >
                        {author(m.principal_id)} ·{" "}
                        {m.external ? t("conversationClient") : m.role}
                      </li>
                    ))}
                  </ul>
                )}
                {members.some(member => member.principal_id === session?.subject && member.role === "OWNER" && !member.external) && <ChannelMediaTools key={channelID} channel={channelID} />}
                <div
                  className="flex flex-wrap items-center gap-2"
                  role="group"
                  aria-label={t("conversationActivity")}
                >
                  {["all", "unread", "mentions", "saved"].map((tab) => (
                    <Button
                      key={tab}
                      variant={activity === tab ? "secondary" : "ghost"}
                      onClick={() => navigate({ activity: tab })}
                      aria-pressed={activity === tab}
                    >
                      {t(`conversationActivity_${tab}`)}
                    </Button>
                  ))}
                </div>
                <form
                  className="flex gap-2"
                  onSubmit={(e) => {
                    e.preventDefault()
                    setQuery(search)
                  }}
                >
                  <Input
                    value={search}
                    maxLength={200}
                    onChange={(e) => setSearch(e.target.value)}
                    aria-label={t("conversationSearch")}
                    placeholder={t("conversationSearch")}
                  />
                  <Button variant="outline" type="submit">
                    {t("search")}
                  </Button>
                </form>
              </header>
              {threadID && (
                <div className="flex items-center justify-between border-b bg-muted/30 px-4 py-2 text-sm">
                  <span>{t("conversationThread")}</span>
                  <Button
                    variant="ghost"
                    onClick={() => navigate({ thread: "" })}
                  >
                    {t("conversationBack")}
                  </Button>
                </div>
              )}
              <div
                className="min-h-48 flex-1 space-y-0 p-4"
                aria-busy={loading}
              >
                {loading ? (
                  <p role="status" className="text-sm text-muted-foreground">
                    {t("loading")}
                  </p>
                ) : messages.length ? (
                  foldSystemMessages([...messages].reverse()).map(({ latest: m, previous }) => (
                    <article
                      key={m.id}
                      className="min-w-0 border-b py-4 last:border-b-0"
                    >
                      <div className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
                        <span className="font-medium text-foreground">
                          {author(m.author_id)}
                        </span>
                        <time dateTime={m.created_at}>
                          {new Date(m.created_at).toLocaleString()}
                        </time>
                        {m.visibility === "INTERNAL" && (
                          <span className="rounded border px-1.5">
                            {t("conversationInternal")}
                          </span>
                        )}
                      </div>
                      {!m.retracted && <SystemMessageContent message={m} tenant={tenantID} project={entityID} />}
                      {previous.length > 0 && <details className="mt-2 text-sm"><summary>{t("conversationFoldMore")} {previous.length}</summary><div className="space-y-2 pt-2">{previous.map(item => <SystemMessageContent key={item.id} message={item} tenant={tenantID} project={entityID} />)}</div></details>}
                      {(!("kind" in m) || m.kind === "TEXT") && <MessageActions tenant={tenantID} message={m} actor={session?.subject} manage={allowed("channel_manage")} audit={allowed("audit_read")} onChanged={refresh} />}
                      {!m.retracted && (m.attachments ?? []).map(a => a.id && m.id && a.file_id && <ConversationAttachment key={a.id} channel={channelID} message={m.id} attachment={a.id} file={a.file_id} transcribe={members.some(member => member.principal_id === session?.subject && member.role === "OWNER" && !member.external)} onDraft={text => setDraft(value => `${value}${value ? "\n" : ""}${text}`)} />)}
                      {!m.retracted && allowed("work_item_write") && m.id && (
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setConverting(m.id!)}
                        >
                          {t("conversationConvert")}
                        </Button>
                      )}

                      <div className="mt-2 flex flex-wrap gap-1">
                        {!threadID && !m.reply_to_message_id && (
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => navigate({ thread: m.id ?? "" })}
                          >
                            <MessageSquare className="size-3.5" />
                            {t("conversationReply")}
                          </Button>
                        )}
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-pressed={m.saved}
                          onClick={async () => {
                            if (!m.id) return
                            try {
                              await iamApi.saveConversationMessage(
                                channelID,
                                m.id,
                                !m.saved
                              )
                              await refresh()
                            } catch (e) {
                              setError((e as Error).message)
                            }
                          }}
                        >
                          <Bookmark
                            className={`size-3.5 ${m.saved ? "fill-current" : ""}`}
                          />
                          {m.saved
                            ? t("conversationUnsave")
                            : t("conversationSave")}
                        </Button>
                      </div>
                    </article>
                  ))
                ) : (
                  <p className="py-8 text-center text-sm text-muted-foreground">
                    {t("conversationEmpty")}
                  </p>
                )}
                {messageCursor && (
                  <Button
                    variant="outline"
                    disabled={busy}
                    onClick={() => void moreMessages()}
                  >
                    {t("conversationOlder")}
                  </Button>
                )}
              </div>
              {messages.length > 0 && (
                <Button
                  className="mx-4 mb-2 self-start"
                  variant="ghost"
                  onClick={async () => {
                    try {
                      await iamApi.readConversation(
                        channelID,
                        Math.max(...messages.map((m) => m.seq))
                      )
                      await loadChannels()
                    } catch (e) {
                      setError((e as Error).message)
                    }
                  }}
                >
                  {t("conversationMarkRead")}
                </Button>
              )}
              {allowed("channel_post") && !selected.archived && (
                <form
                  className="space-y-3 border-t p-4"
                  onSubmit={(e) => {
                    e.preventDefault()
                    void send()
                  }}
                >
                  <label htmlFor="message-body" className="text-sm font-medium">
                    {threadID
                      ? t("conversationReply")
                      : t("conversationCompose")}
                  </label>
                  <Textarea
                    id="message-body"
                    value={draft}
                    maxLength={8000}
                    rows={3}
                    onChange={(e) => setDraft(e.target.value)}
                    disabled={busy}
                    placeholder={t("conversationMarkdown")}
                  />
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="flex flex-wrap gap-3 text-sm">
                      <label className="flex min-h-11 items-center gap-2">
                        <input
                          type="checkbox"
                          checked={internal}
                          disabled={busy}
                          onChange={(e) => setInternal(e.target.checked)}
                        />
                        {t("conversationInternal")}
                      </label>
                      <label className="flex items-center gap-2">
                        {t("conversationMention")}
                        <select
                          className="min-h-11 rounded border bg-background px-2"
                          value=""
                          onChange={(e) => {
                            if (e.target.value)
                              setMentions((old) => [
                                ...new Set([...old, e.target.value]),
                              ])
                          }}
                        >
                          <option value="">
                            {t("conversationChooseMember")}
                          </option>
                          {members.map(
                            (m) =>
                              m.principal_id && (
                                <option
                                  key={m.principal_id}
                                  value={m.principal_id}
                                >
                                  {author(m.principal_id)}
                                </option>
                              )
                          )}
                        </select>
                      </label>
                    </div>
                    <Button
                      type="submit"
                      disabled={busy || uploading || !draft.trim()}
                    >
                      {busy ? t("loading") : t("conversationSend")}
                    </Button>
                  </div>
                  <label className="flex min-h-11 items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={asTask}
                      disabled={busy || !allowed("work_item_write")}
                      onChange={(e) => setAsTask(e.target.checked)}
                    />
                    {t("conversationAsTask")}
                  </label>
                  <ComposerUpload
                    key={channelID}
                    entityID={entityID}
                    channelID={channelID}
                    disabled={busy || resolving || attachments.length >= 5}
                    remaining={5 - attachments.length}
                    onBusy={setUploading}
                    onAttached={(candidate) =>
                      setAttachments((old) =>
                        old.some((a) => a.file_id === candidate.file_id)
                          ? old
                          : [...old, candidate]
                      )
                    }
                  />
                  <fieldset
                    disabled={busy || resolving || uploading}
                    className="space-y-2"
                  >
                    <legend className="text-sm font-medium">
                      {t("conversationAttachments")}
                    </legend>
                    <p className="text-xs text-muted-foreground">
                      {t("conversationAttachmentsHint")}
                    </p>
                    <div className="flex flex-wrap gap-2">
                      <label className="min-w-0 flex-1 text-sm">
                        {t("conversationFileID")}
                        <Input
                          inputMode="numeric"
                          pattern="[1-9][0-9]*"
                          value={attachmentFile}
                          onChange={(e) => setAttachmentFile(e.target.value)}
                        />
                      </label>
                      <Button
                        type="button"
                        variant="outline"
                        disabled={
                          attachments.length >= 5 ||
                          !/^[1-9][0-9]{0,18}$/.test(attachmentFile)
                        }
                        onClick={async () => {
                          const target = channelID
                          setResolving(true)
                          try {
                            const candidate =
                              await iamApi.conversationAttachmentCandidate(
                                target,
                                attachmentFile
                              )
                            if (selectionRef.current !== selection) return
                            setAttachments((old) =>
                              old.some((a) => a.file_id === candidate.file_id)
                                ? old
                                : [...old, candidate]
                            )
                            setAttachmentFile("")
                          } catch (e) {
                            setError((e as Error).message)
                          } finally {
                            setResolving(false)
                          }
                        }}
                      >
                        {resolving ? t("loading") : t("conversationAttach")}
                      </Button>
                    </div>
                    {attachments.map((a) => (
                      <div
                        key={a.file_id}
                        className="flex items-center justify-between gap-2 text-sm"
                      >
                        <span className="min-w-0 break-all">
                          {a.name} · {a.mime}
                        </span>
                        <Button
                          type="button"
                          variant="ghost"
                          onClick={() =>
                            setAttachments((old) =>
                              old.filter((v) => v.file_id !== a.file_id)
                            )
                          }
                        >
                          {t("conversationRemove")}
                        </Button>
                      </div>
                    ))}
                  </fieldset>
                  {mentions.length > 0 && (
                    <div className="flex flex-wrap gap-2">
                      {mentions.map((id) => (
                        <Button
                          type="button"
                          variant="secondary"
                          size="sm"
                          key={id}
                          onClick={() =>
                            setMentions((old) => old.filter((v) => v !== id))
                          }
                        >
                          @{author(id)} ×
                        </Button>
                      ))}
                    </div>
                  )}
                </form>
              )}
            </>
          )}
        </section>
      </div>
    </div>
  )
}

function CreateChannel({
  entityID,
  onCreated,
}: {
  entityID: string
  onCreated: (channel: ConversationChannel) => Promise<void>
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const route = useMemo(
    () =>
      apiRoute("post", "/entities/{entity_id}/channels", {
        path: { entity_id: entityID },
      }),
    [entityID]
  )
  const metadata = useRequestContract(route)
  const [error, setError] = useState("")
  const attempt = useRef<{ body: string; key: string } | null>(null)
  return (
    <div className="space-y-2 border-t pt-3">
      {error && <Alert>{error}</Alert>}
      <AutoForm
        contract={metadata.contract}
        fields={{
          name: { name: t("name"), type: "text", value: "" },
          topic: { name: t("conversationTopic"), type: "text", value: "" },
          visibility: {
            name: t("conversationVisibility"),
            type: "select",
            value: "INTERNAL",
            config: {
              options: [
                { label: t("conversationInternal"), value: "INTERNAL" },
                ...(allowed("channel_manage")
                  ? [{ label: t("conversationShared"), value: "CLIENT_SHARED" }]
                  : []),
              ],
            },
          },
        }}
        onSubmit={async (values) => {
          const body = {
            name: String(values.name),
            topic: String(values.topic ?? ""),
            visibility:
              values.visibility === "CLIENT_SHARED"
                ? ("CLIENT_SHARED" as const)
                : ("INTERNAL" as const),
          }
          const identity = JSON.stringify(body)
          const key =
            attempt.current?.body === identity
              ? attempt.current.key
              : crypto.randomUUID()
          attempt.current = { body: identity, key }
          try {
            await onCreated(
              await iamApi.createConversation(entityID, body, key)
            )
          } catch (e) {
            setError((e as Error).message)
            throw e
          }
        }}
      >
        <Button type="submit" disabled={!metadata.contract}>
          {t("conversationCreate")}
        </Button>
      </AutoForm>
    </div>
  )
}
