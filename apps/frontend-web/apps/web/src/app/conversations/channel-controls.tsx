import { useEffect, useMemo, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { apiRoute } from "../../lib/api-route"
import { channelLifecycleApi } from "./channel-controls-api"
import { useRequestContract } from "../../lib/use-request-contract"
import type { components } from "../../lib/api-schema"

type Channel = components["schemas"]["ChannelView"]

export function ChannelControls({ channel, actor, manage, onChanged }: { channel: Channel; actor: string; manage: boolean; onChanged: () => Promise<void> }) {
  const t = useTranslations("iam")
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(channel.name)
  const [topic, setTopic] = useState(channel.topic ?? "")
  const [target, setTarget] = useState("")
  const [confirm, setConfirm] = useState<"archive" | "leave" | "remove" | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const action = useRef<AbortController | null>(null)
  const route = useMemo(() => apiRoute("patch", "/channels/{channel_id}", { path: { channel_id: channel.id! } }), [channel.id])
  const metadata = useRequestContract(route)
  useEffect(() => () => action.current?.abort(), [])
  if (channel.kind !== "CUSTOM") return null
  async function command(input?: components["schemas"]["ChannelUpdateInput"], principal?: string) {
    if (busy || !metadata.contract) return
    const controller = new AbortController(); action.current = controller; setBusy(true); setError("")
    try {
      if (input) {
        const body = JSON.stringify(input)
        if (attempt.current?.body !== body) attempt.current = { body, key: crypto.randomUUID() }
        await channelLifecycleApi.update(channel.id!, input, attempt.current.key, controller.signal)
      } else if (principal) await channelLifecycleApi.remove(channel.id!, principal, controller.signal)
      if (!controller.signal.aborted) { attempt.current = null; setConfirm(null); setOpen(false); await onChanged() }
    } catch (cause) { if (!controller.signal.aborted) setError((cause as Error).message) }
    finally { if (!controller.signal.aborted) setBusy(false) }
  }
  return <section className="space-y-3">
    <div className="flex flex-wrap gap-2">{manage && <Button variant="outline" aria-expanded={open} onClick={() => setOpen(value => !value)}>{t("conversationChannelSettings")}</Button>}{actor && !channel.archived && <Button variant="outline" disabled={busy} onClick={() => setConfirm("leave")}>{t("conversationLeave")}</Button>}</div>
    {error && <Alert>{error}</Alert>}
    {metadata.failed && <Alert>{t("selfProfileContractFailed")}<Button variant="outline" onClick={metadata.retry}>{t("refresh")}</Button></Alert>}
    {open && <form className="space-y-3" onSubmit={event => { event.preventDefault(); void command({ version: channel.version, name: name.trim(), topic }) }}>
      <label className="grid gap-2 text-sm">{t("conversationChannelName")}<Input value={name} maxLength={40} required disabled={busy || channel.archived} onChange={event => setName(event.target.value)} /></label>
      <label className="grid gap-2 text-sm">{t("conversationChannelTopic")}<Input value={topic} maxLength={200} disabled={busy || channel.archived} onChange={event => setTopic(event.target.value)} /></label>
      <div className="flex flex-wrap gap-2"><Button type="submit" disabled={busy || channel.archived || !metadata.contract || !name.trim()}>{t("save")}</Button><Button type="button" variant="outline" disabled={busy || !metadata.contract} onClick={() => setConfirm("archive")}>{t(channel.archived ? "conversationRestore" : "conversationArchive")}</Button></div>
      {!channel.archived && <label className="grid gap-2 text-sm">{t("conversationMemberPrincipal")}<Input value={target} inputMode="numeric" pattern="[1-9][0-9]*" onChange={event => setTarget(event.target.value)} /><Button type="button" variant="outline" disabled={busy || !/^[1-9][0-9]*$/.test(target)} onClick={() => setConfirm("remove")}>{t("conversationRemoveMember")}</Button></label>}
    </form>}
    {confirm && <div role="alert" className="space-y-3 rounded border p-3"><p>{t(confirm === "archive" ? "conversationArchiveConfirm" : "conversationLeaveConfirm")}</p>{confirm === "remove" && <p className="break-all">{target}</p>}<div className="flex gap-2"><Button disabled={busy || !metadata.contract} onClick={() => { void (confirm === "archive" ? command({ version: channel.version, archived: !channel.archived }) : command(undefined, confirm === "leave" ? actor : target)) }}>{t("confirm")}</Button><Button variant="outline" disabled={busy} onClick={() => setConfirm(null)}>{t("cancel")}</Button></div></div>}
  </section>
}
