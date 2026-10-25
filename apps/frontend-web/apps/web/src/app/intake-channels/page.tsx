import { useEffect, useRef, useState } from "react"
import { useParams } from "react-router"
import { AutoForm, type FieldConfig } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { channelSettings, intakeChannelsApi, type IntakeChannel } from "./api"
import { intakeChannelMessages } from "./messages"
const api = intakeChannelsApi()
export default function IntakeChannelsPage() {
  const { tenantID = "", entityID = "" } = useParams()
  return (
    <Channels
      key={`${tenantID}:${entityID}`}
      tenant={tenantID}
      project={entityID}
    />
  )
}
function Channels({ tenant, project }: { tenant: string; project: string }) {
  const locale = useLocale(),
    l =
      intakeChannelMessages[locale as keyof typeof intakeChannelMessages] ??
      intakeChannelMessages["en-US"],
    allowed = useAllowed(),
    read = allowed("intake_read"),
    write = allowed("intake_channel_manage")
  const [channels, setChannels] = useState<IntakeChannel[]>([]),
    [loading, setLoading] = useState(true),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [revision, setRevision] = useState(0),
    [editing, setEditing] = useState<string | null>(null),
    [busy, setBusy] = useState(false)
  const pending = useRef<{ signature: string; key: string } | null>(null)
  const rotation = useRef<{ version: number; key: string } | null>(null)
  const [credential, setCredential] = useState("")
  useEffect(() => {
    if (!read) return
    const c = new AbortController()
    void api
      .list(tenant, project, c.signal)
      .then((v) => {
        if (!c.signal.aborted) {
          setChannels(v)
          setLoading(false)
          setError("")
        }
      })
      .catch((e) => {
        if (!c.signal.aborted) {
          setChannels([])
          setLoading(false)
          setError(e instanceof Error ? e.message : l.error)
        }
      })
    return () => c.abort()
  }, [tenant, project, read, revision, l.error])
  const current = channels.find((c) => c.kind === editing),
    label = (kind: string) =>
      kind === "PORTAL" ? l.portal : kind === "EMAIL" ? l.email : l.api
  const fields: Record<string, FieldConfig> = {
	...(editing === "API" ? { callback_subscription_id: {name:l.callback, type:"text", config:{inputMode:"numeric", maxLength:19, pattern:"[1-9][0-9]{0,18}"}} as FieldConfig } : {}),
    triage_agent_id: { name: l.triageAgent, type: "text" },
    notify_principal_ids: {name: l.notifyTargets, type: "text"},
    enabled: { name: l.enabled, type: "switch" },
    display_name: {
      name: l.display_name,
      type: "text",
      required: true,
      config: { maxLength: 200 },
    },
    support_email: {
      name: l.support_email,
      type: "email",
      config: { maxLength: 254 },
    },
    language: {
      name: l.language,
      type: "select",
      required: true,
      value: locale,
      config: {
        options: [
          { value: "en-US", label: "English" },
          { value: "zh-CN", label: l.localeLanguageZh },
          { value: "ms-MY", label: "Bahasa Melayu" },
        ],
      },
    },
    daily_limit: {
      name: l.daily_limit,
      type: "number",
      required: true,
      value: 20,
    },
    receipt_enabled: { name: l.receipt_enabled, type: "switch" },
    reminder_hours: {
      name: l.reminder_hours,
      type: "number",
      required: true,
      value: 48,
    },
    timeout_hours: {
      name: l.timeout_hours,
      type: "number",
      required: true,
      value: 168,
    },
  }
  return (
    <main className="min-w-0 space-y-5">
      <PageHeader title={l.title} description={l.description} />
      {!read ? (
        <Alert>{l.denied}</Alert>
      ) : (
        <>
          <Alert>{l.unavailable}</Alert>
          <p className="text-sm text-muted-foreground">{l.existing}</p>
          {error && <Alert>{error}</Alert>}
          {notice && (
            <p role="status" className="text-sm">
              {notice}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              disabled={loading || busy}
              onClick={() => {
                setLoading(true)
                setRevision((v) => v + 1)
              }}
            >
              {l.refresh}
            </Button>
          </div>
          <div className="grid gap-3 md:grid-cols-3">
            {["PORTAL", "EMAIL", "API"].map((kind) => (
              <section key={kind} className="space-y-3 rounded-lg border p-4">
                <h2 className="font-semibold">{label(kind)}</h2>
                <p className="text-sm text-muted-foreground">
                  {l.availability}: {channels.find(c => c.kind === kind)?.availability ?? "UNAVAILABLE"}
                </p>
                <p className="text-sm">
                  {l.version}:{" "}
                  {channels.find((c) => c.kind === kind)?.version ?? "-"}
                </p>
                {write && (
                  <Button
                    variant="outline"
                    disabled={loading || busy}
                    onClick={() => {
                      setNotice("")
                      setEditing(kind)
                    }}
                  >
                    {channels.some((c) => c.kind === kind)
                      ? l.edit
                      : l.configure}
                  </Button>
                )}
                {kind === "API" && write && channels.some(c => c.kind === "API") && <Button variant="outline" disabled={loading || busy} onClick={async () => {
                  const channel = channels.find(c => c.kind === "API")
                  if (!channel || busy) return
                  if (rotation.current?.version !== channel.version) rotation.current = { version: channel.version, key: crypto.randomUUID() }
                  setBusy(true); setError(""); setCredential("")
                  try {
                    const result = await api.rotateCredential(tenant, project, channel.version, rotation.current.key)
                    rotation.current = null
                    setCredential(result.credential ?? "")
                    setNotice(result.credential ? l.credentialOnce : l.credentialReplay)
                    setChannels(rows => rows.map(row => row.kind === "API" ? result.channel : row))
                  } catch (e) { setError(e instanceof Error ? e.message : l.error) }
                  finally { setBusy(false) }
                }}>{l.rotateCredential}</Button>}
              </section>
            ))}
          </div>
          {credential && <section className="min-w-0 space-y-2 border p-4" aria-label={l.credential}>
            <label className="grid gap-2 text-sm">{l.credential}<Input readOnly value={credential} autoComplete="off" spellCheck={false} /></label>
            <p className="text-sm text-muted-foreground">{l.credentialOnce}</p>
            <Button variant="outline" onClick={() => setCredential("")}>{l.dismissCredential}</Button>
          </section>}
          <section className="min-w-0 space-y-2 border-t pt-4">
            <h2 className="font-medium">{l.receivingEndpoint}</h2>
            <code className="block break-all text-sm">POST /tenants/{tenant}/projects/{project}/intake-channels/API/submissions</code>
            <p className="text-sm text-muted-foreground">{l.receivingHelp}</p>
			<p className="text-sm text-muted-foreground">{l.callbackDisclosure}</p>
			{channels.find(c => c.kind === "API")?.settings.callback_subscription_id && <p className="break-all text-sm">{l.callback}: {channels.find(c => c.kind === "API")?.settings.callback_subscription_id}</p>}
          </section>
          <AutoTable
            data={channels}
            loading={loading}
            rowKey="id"
            pagination={false}
            exportable={false}
            emptyContent={error ? l.error : l.empty}
            columns={[
              {
                field: "kind",
                name: l.kind,
                render: (_, row) => label(row.kind),
              },
              { field: "version", name: l.version },
              {
                field: "settings",
                name: l.display_name,
                render: (_, row) => row.settings.display_name,
              },
              { field: "availability", name: l.availability },
            ]}
          />
          {editing && write && (
            <section className="space-y-3 border-t pt-5">
              <h2 className="font-semibold">
                {l.edit} / {label(editing)}
              </h2>
			{editing === "API" && <p className="text-sm text-muted-foreground">{l.callbackHelp} {l.callbackDisclosure}</p>}
              <AutoForm
                key={`${editing}:${current?.version ?? 0}`}
                fields={fields}
                mode="edit"
                initialData={
                  current
                    ? { ...current.settings }
                    : {
                        enabled: false,
                        display_name: "",
                        support_email: "",
                        language: locale,
                        daily_limit: 20,
                        receipt_enabled: false,
                        reminder_hours: 48,
                        timeout_hours: 168,
                      }
                }
                onError={(e) =>
                  setError(e instanceof Error ? e.message : l.error)
                }
                onSubmit={async (values) => {
                  if (busy) return
                  const settings = channelSettings(values, l.callbackError),
                    signature = JSON.stringify([
                      editing,
                      current?.version ?? 0,
                      settings,
                    ])
                  if (pending.current?.signature !== signature)
                    pending.current = { signature, key: crypto.randomUUID() }
                  setBusy(true)
                  setError("")
                  try {
                    await api.save(
                      tenant,
                      project,
                      editing,
                      current?.version ?? 0,
                      settings,
                      pending.current.key
                    )
                    pending.current = null
                    setEditing(null)
                    setNotice(l.saved)
                    setLoading(true)
                    setRevision((v) => v + 1)
                  } catch (e) {
                    setError(e instanceof Error ? e.message : l.error)
                    throw e
                  } finally {
                    setBusy(false)
                  }
                }}
              >
                <div className="flex flex-wrap gap-2">
                  <Button type="submit" disabled={busy}>
                    {l.save}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    disabled={busy}
                    onClick={() => setEditing(null)}
                  >
                    {l.cancel}
                  </Button>
                </div>
              </AutoForm>
            </section>
          )}
          <section className="space-y-2 border-t pt-4">
            <h2 className="font-medium">{l.im}</h2>
            <p className="text-sm text-muted-foreground">{l.imHint}</p>
          </section>
        </>
      )}
    </main>
  )
}
