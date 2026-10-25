import { useEffect, useMemo, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { AutoForm, type FieldConfig } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAuth } from "../../components/auth"
import { iamApi } from "../../lib/iam-api"
import { apiRoute, type ApiRoute } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"
import { useAllowed } from "../../lib/permissions"
import { useRequestContract } from "../../lib/use-request-contract"
import { AgentInspection } from "./inspection"
import { AgentRegistration } from "./registration"
import { AgentDirectory } from "./directory"
import { AgentProfileEditor } from "./profile"
import { AgentProcessHistory } from "./process"
import { AgentPerformancePanel } from "./performance"
import { AgentWakeConfig } from "./wake-config"
import { AgentMachineBinding } from "./machine-binding"
import { SpawnRequests } from "../governance/spawn-requests"
import { AgentLoops } from "./loops"
import { AgentResetHistory } from "./reset-history"
import { AgentRetirement } from "./retirement"
import { AgentOperationManifest } from "./operation-manifest"
import { AgentMemories } from "./memory"
import { agentInspectionApi } from "./inspection-api"

type Live = components["schemas"]["LiveView"]
type Inbox = components["schemas"]["AgentInboxPage"]
type Reminder = components["schemas"]["ReminderView"]
type Tool = components["schemas"]["ToolView"]
type Operation = components["schemas"]["Operation"]
const tabs = ["lifecycle", "inbox", "reminders", "operations", "tools", "memory"] as const
const inspectionTabs = ["work", "direct", "workspace", "performance"] as const
type Tab = (typeof tabs)[number]

export default function AgentsPage() {
  const { tenantID = "" } = useParams()
  return <Agents key={tenantID} tenant={tenantID} />
}
function Agents({ tenant }: { tenant: string }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [registering, setRegistering] = useState(false)
  const [search, setSearch] = useSearchParams()
  const candidate = search.get("agent") ?? ""
  const agent = /^[1-9][0-9]{0,18}$/.test(candidate) ? candidate : ""
  const [privateAccess, setPrivateAccess] = useState("")
  useEffect(() => {
    if (!agent) return
    const abort = new AbortController()
    void agentInspectionApi.workspaceAccess(tenant, agent, abort.signal).then(value => { if (!abort.signal.aborted) setPrivateAccess(value ? `${tenant}:${agent}` : "") }).catch(() => { if (!abort.signal.aborted) setPrivateAccess("") })
    return () => abort.abort()
  }, [tenant, agent])
  const canInspectPrivate = privateAccess === `${tenant}:${agent}`
  const [draft, setDraft] = useState(agent)
  const rawTab = search.get("tab")
  const tab =
    [...tabs, ...inspectionTabs].find((value) => value === rawTab) ??
    "lifecycle"
  return (
    <div className="space-y-6">
      <PageHeader
        title={t("agents")}
        description={t("agentDescription")}
        action={
          allowed("agent_manage") ? (
            <Button onClick={() => setRegistering(true)}>
              {t("agentRegister")}
            </Button>
          ) : undefined
        }
      />
      {registering && (
        <AgentRegistration
          tenant={tenant}
          onClose={() => setRegistering(false)}
          onCreated={(principal) => {
            setRegistering(false)
            setDraft(principal)
            setSearch((previous) => {
              const next = new URLSearchParams(previous)
              next.set("agent", principal)
              return next
            })
          }}
        />
      )}
      {(
        <AgentDirectory
          key={tenant}
          tenant={tenant}
          onSelect={(principal) => {
            setDraft(principal)
            setSearch((previous) => {
              const next = new URLSearchParams(previous)
              next.set("agent", principal)
              return next
            })
          }}
        />
      )}
      <form
        className="flex flex-wrap items-end gap-3"
        onSubmit={(event) => {
          event.preventDefault()
          if (/^[1-9][0-9]{0,18}$/.test(draft))
            setSearch((previous) => {
              const next = new URLSearchParams(previous)
              next.set("agent", draft)
              return next
            })
        }}
      >
        <label className="grid gap-2 text-sm" htmlFor="agent-principal">
          {t("agentPrincipal")}
          <Input
            id="agent-principal"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            inputMode="numeric"
            pattern="[1-9][0-9]{0,18}"
            required
          />
        </label>
        <Button type="submit">{t("agentOpen")}</Button>
      </form>
      <nav
        aria-label={t("agentSections")}
        className="flex flex-wrap gap-2 border-b pb-3"
      >
        {[...tabs, ...inspectionTabs].filter(value => canInspectPrivate || !["workspace", "memory", "inbox", "direct"].includes(value)).map((value) => (
          <Button
            key={value}
            variant={tab === value ? "default" : "outline"}
            aria-current={tab === value ? "page" : undefined}
            onClick={() =>
              setSearch((previous) => {
                const next = new URLSearchParams(previous)
                next.set("tab", value)
                return next
              })
            }
          >
            {t(`agentTab_${value}`)}
          </Button>
        ))}
      </nav>
      {agent && tab === "lifecycle" && canInspectPrivate && <AgentProfileEditor key={`${tenant}:${agent}:profile`} tenant={tenant} agent={agent} />}
      {agent && tab === "lifecycle" && allowed("agent_read") && <AgentProcessHistory key={`${tenant}:${agent}:process`} tenant={tenant} agent={agent} />}
      {agent && !canInspectPrivate && ["workspace", "memory", "inbox", "direct"].includes(tab) ? <p role="status">{t("agentWorkspaceRestricted")}</p> : tab === "performance" ? (agent ? <AgentPerformancePanel key={`${tenant}:${agent}:performance`} tenant={tenant} agent={agent} /> : <p role="status">{t("agentOpen")}</p>) : tab === "memory" ? <AgentMemories key={`${tenant}:${agent}:memory`} tenant={tenant} agent={agent} /> : tab === "work" || tab === "direct" || tab === "workspace" ? (
        <AgentInspection
          key={`${tenant}:${agent}:${tab}`}
          tenant={tenant}
          agent={agent}
          tab={tab}
        />
      ) : (
        <AgentPanel
          key={`${tenant}:${agent}:${tab}`}
          tenant={tenant}
          agent={agent}
          tab={tab}
        />
      )}
    </div>
  )
}

export function AgentOperationsPage() {
  const { tenantID = "" } = useParams()
  const t = useTranslations("iam")
  return (
    <div className="space-y-6">
      <PageHeader
        title={t("agentTab_operations")}
        description={t("agentDescription")}
      />
      <SpawnRequests key={`spawn:${tenantID}`} tenant={tenantID} />
      <AgentPanel key={tenantID} tenant={tenantID} agent="" tab="operations" />
    </div>
  )
}

export function AgentPanel({
  tenant,
  agent,
  tab,
  toolKind,
}: {
  tenant: string
  agent: string
  tab: Tab
  toolKind?: "MCP_SERVER" | "SKILL"
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const { session } = useAuth()
  const [live, setLive] = useState<Live | null>(null)
  const [inbox, setInbox] = useState<Inbox | null>(null)
  const [inboxKind, setInboxKind] = useState<
    | ""
    | "MESSAGE"
    | "MENTION"
    | "REMINDER"
    | "STATE"
    | "EXECUTION"
    | "INTERACTION"
    | "DISPATCH"
    | "REVIEW_FEEDBACK"
  >("")
  const [inboxSince, setInboxSince] = useState<number | undefined>()
  const [reminders, setReminders] = useState<Reminder[]>([])
  const [tools, setTools] = useState<Tool[]>([])
  const [operations, setOperations] = useState<Operation[]>([])
  const [operationFilter, setOperationFilter] = useState<
    "all" | "sideEffects" | "human"
  >("all")
  const visibleOperations = operations.filter(
    (operation) =>
      operationFilter === "all" ||
      (operationFilter === "human"
        ? operation.human_only
        : Boolean(
            operation.side_effects?.some((effect) => effect !== "READ_ONLY")
          ))
  )
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  const [revision, setRevision] = useState(0)
  const [cursor, setCursor] = useState("")
  const [showForm, setShowForm] = useState(false)
  const [mutating, setMutating] = useState(false)
  const [notice, setNotice] = useState("")
  const actionKey = useRef<{ identity: string; key: string } | null>(null)
  const refresh = () => {
    setShowForm(false)
    setRevision((value) => value + 1)
  }
  const catalog = tab === "tools" && allowed("tool_catalog_manage")
  useEffect(() => {
    if (!agent && tab !== "operations" && !catalog) return
    const controller = new AbortController()
    const load = async () => {
      setLoading(true)
      try {
        if (tab === "lifecycle")
          setLive(await iamApi.agentLive(tenant, agent, controller.signal))
        if (tab === "inbox") {
          const state = await iamApi.agentLive(tenant, agent, controller.signal)
          setLive(state)
          setInbox(
            await iamApi.agentInbox(
              tenant,
              agent,
              inboxSince,
              controller.signal,
              inboxKind || undefined
            )
          )
        }
        if (tab === "reminders") {
          const page = await iamApi.agentReminders(
            tenant,
            agent,
            undefined,
            controller.signal
          )
          setReminders(page.items ?? [])
          setCursor(page.next_cursor || "")
        }
        if (tab === "operations")
          setOperations(
            (await iamApi.agentOperations(tenant, controller.signal))
              .operations ?? []
          )
        if (tab === "tools") {
          const page = await iamApi.agentTools(
            tenant,
            catalog ? undefined : agent,
            undefined,
            controller.signal
          )
          setTools(page.items ?? [])
          setCursor(page.next_cursor || "")
        }
        setError("")
      } catch (e) {
        if (!controller.signal.aborted) {
          setLive(null)
          setInbox(null)
          setReminders([])
          setTools([])
          setOperations([])
          setCursor("")
          setError((e as Error).message)
        }
      } finally {
        if (!controller.signal.aborted) setLoading(false)
      }
    }
    void load()
    const poll =
      tab === "lifecycle" || tab === "inbox"
        ? setInterval(() => void load(), 30000)
        : undefined
    return () => {
      controller.abort()
      if (poll) clearInterval(poll)
    }
  }, [tenant, agent, tab, catalog, revision, inboxKind, inboxSince])
  async function action(
    identity: string,
    perform: (key: string) => Promise<unknown>
  ) {
    const key =
      actionKey.current?.identity === identity
        ? actionKey.current.key
        : crypto.randomUUID()
    actionKey.current = { identity, key }
    setMutating(true)
    setNotice("")
    try {
      await perform(key)
      actionKey.current = null
      setNotice(t("agentSaved"))
      refresh()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setMutating(false)
    }
  }
  async function more() {
    setLoading(true)
    try {
      if (tab === "tools") {
        const page = await iamApi.agentTools(
          tenant,
          catalog ? undefined : agent,
          cursor
        )
        setTools((old) => [...old, ...(page.items ?? [])])
        setCursor(page.next_cursor || "")
      } else {
        const page = await iamApi.agentReminders(tenant, agent, cursor)
        setReminders((old) => [...old, ...(page.items ?? [])])
        setCursor(page.next_cursor || "")
      }
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }
  if (!agent && tab !== "operations" && !catalog)
    return <p className="p-6 text-muted-foreground">{t("agentChoose")}</p>
  return (
    <section
      aria-label={t(`agentTab_${tab}`)}
      className="space-y-4"
      aria-busy={loading}
    >
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-lg font-semibold">{t(`agentTab_${tab}`)}</h2>
        <Button variant="outline" onClick={refresh} disabled={loading}>
          {t("agentRefresh")}
        </Button>
      </div>
      {error && <Alert>{error}</Alert>}
      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}
      {loading && (
        <p role="status" className="text-sm text-muted-foreground">
          {t("agentLoading")}
        </p>
      )}
      {tab === "lifecycle" && live && (
        <>
          <dl className="grid gap-5 border-y py-5 sm:grid-cols-3">
            {[
              ["agentName", live.name],
              ...(live.runtime
                ? [
                    [
                      "agentMachine",
                      live.runtime.machine_id ?? t("agentUnreported"),
                    ],
                    ["agentMachineConnectivity", live.runtime.machine_state],
                    ["agentRuntimeKind", live.runtime.runtime_kind],
                    ["agentProcessStatus", live.runtime.process_status],
                  ]
                : []),
              ["agentIdentity", live.identity_state],
              ["agentActivity", t(`agentActivity_${live.activity}`)],
              ["agentSession", String(live.session_epoch)],
              ["agentWorkspace", String(live.workspace_epoch)],
              [
                "agentUpdated",
                live.updated_at
                  ? new Date(live.updated_at).toLocaleString()
                  : t("agentUnreported"),
              ],
            ].map(([label, value]) => (
              <div key={label}>
                <dt className="text-sm text-muted-foreground">{t(label!)}</dt>
                <dd className="mt-2 font-medium break-words">{value}</dd>
              </div>
            ))}
          </dl>
          <p className="text-sm text-muted-foreground">
            {t("agentResetNotice")}
          </p>
          {allowed("agent_manage") && (
            <>
              <Button variant="outline" onClick={() => setShowForm(!showForm)}>
                {t("agentReset")}
              </Button>
              {showForm && (
                <ResetForm tenant={tenant} agent={agent} onDone={refresh} />
              )}
            </>
          )}
        </>
      )}
      {tab === "lifecycle" && agent && allowed("agent_read") && (
        <AgentMachineBinding
          key={`${tenant}:${agent}`}
          tenant={tenant}
          agent={agent}
        />
      )}
      {tab === "lifecycle" && agent && live && (
        <AgentResetHistory
          key={`${tenant}:${agent}`}
          tenant={tenant}
          agent={agent}
          revision={revision}
        />
      )}
      {tab === "lifecycle" && agent && allowed("agent_read") && (
        <AgentRetirement
          key={`${tenant}:${agent}`}
          tenant={tenant}
          agent={agent}
          canRetire={allowed("agent_retire")}
        />
      )}
      {tab === "inbox" && agent && allowed("agent_manage") && (
        <AgentWakeConfig
          key={`${tenant}:${agent}`}
          tenant={tenant}
          agent={agent}
        />
      )}
      {tab === "inbox" && (
        <label className="flex flex-wrap items-center gap-2 text-sm">
          <span>{t("agentInboxKind")}</span>
          <select
            className="min-h-11 rounded border bg-background px-3"
            value={inboxKind}
            onChange={(e) => {
              setInboxKind(e.target.value as typeof inboxKind)
              setInboxSince(undefined)
            }}
          >
            <option value="">{t("agentInboxAllKinds")}</option>
            {(
              [
                "MESSAGE",
                "MENTION",
                "REMINDER",
                "STATE",
                "EXECUTION",
                "INTERACTION",
                "DISPATCH",
                "REVIEW_FEEDBACK",
              ] as const
            ).map((kind) => (
              <option key={kind} value={kind}>
                {t(`agentInboxKind_${kind}`)}
              </option>
            ))}
          </select>
        </label>
      )}
      {tab === "inbox" && inbox && (
        <>
          <p>
            {t("agentCursor")}: {inbox.ack} / {inbox.head}
          </p>
          <p className="text-sm text-muted-foreground">
            {t("agentWakeNotice")}
          </p>
          {inboxKind && (
            <p className="text-sm text-muted-foreground">
              {t("agentInboxFilterReadOnly")}
            </p>
          )}
          {(inbox.items ?? []).length === 0 && (
            <p className="p-5 text-muted-foreground">{t("agentEmpty")}</p>
          )}
          <ol className="divide-y border-y">
            {(inbox.items ?? []).map((event) => (
              <li
                key={event.id}
                className="grid gap-2 py-4 sm:grid-cols-[5rem_1fr_auto]"
              >
                <span className="font-mono text-sm">#{event.seq}</span>
                <div>
                  <strong>{event.kind}</strong>
                  <p className="text-sm break-all text-muted-foreground">
                    {event.stream_key} ? {event.subject_id}
                  </p>
                </div>
                <time className="text-sm text-muted-foreground">
                  {new Date(event.created_at).toLocaleString()}
                </time>
              </li>
            ))}
          </ol>
          {session?.subject === agent && !inboxKind && (
            <Button
              disabled={!(inbox.items ?? []).length || mutating}
              onClick={() => {
                const since = (inbox.items ?? []).at(-1)?.seq
                if (since !== undefined)
                  void action(`ack:${since}`, (key) =>
                    iamApi.agentAcknowledge(tenant, agent, since, key)
                  )
              }}
            >
              {t("agentAck")}
            </Button>
          )}
          {inbox.has_more && (
            <Button
              variant="outline"
              disabled={loading}
              onClick={() => setInboxSince(inbox.items?.at(-1)?.seq)}
            >
              {t("nextPage")}
            </Button>
          )}
          {inboxSince !== undefined && (
            <Button
              variant="outline"
              disabled={loading}
              onClick={() => setInboxSince(undefined)}
            >
              {t("agentInboxResume")}
            </Button>
          )}
        </>
      )}
      {tab === "reminders" && (
        <>
          {agent && (
            <AgentLoops
              key={`${tenant}:${agent}`}
              tenant={tenant}
              agent={agent}
              canManage={allowed("agent_manage")}
            />
          )}
          <p className="text-sm text-muted-foreground">
            {t("agentLoopNotice")}
          </p>
          <Button variant="outline" onClick={() => setShowForm(!showForm)}>
            {t("agentReminderCreate")}
          </Button>
          {showForm && (
            <ReminderForm tenant={tenant} agent={agent} onDone={refresh} />
          )}
          {!loading && !reminders.length && !error && (
            <p className="p-5 text-muted-foreground">{t("agentEmpty")}</p>
          )}
          <div className="divide-y border-y">
            {reminders.map((reminder) => (
              <article key={reminder.id} className="space-y-3 py-5">
                <div className="flex flex-wrap justify-between gap-2">
                  <h3 className="font-semibold">{reminder.title}</h3>
                  <span className="text-sm">
                    {t(`agentReminderState_${reminder.state}`)}
                  </span>
                </div>
                <p className="text-sm break-all">
                  {reminder.anchor_kind === "WORK_ITEM" ? (
                    <Link
                      className="underline underline-offset-4"
                      to={`/admin/${tenant}/entities/${reminder.project_id}/work-items?work=${reminder.work_item_id}`}
                    >
                      {t("agentField_work_item_id")}: {reminder.work_item_id}
                    </Link>
                  ) : (
                    `${t("agentField_thread_id")}: ${reminder.thread_id}`
                  )}
                </p>
                <p className="text-sm whitespace-pre-wrap">{reminder.note}</p>
                <dl className="grid gap-3 text-sm sm:grid-cols-2">
                  {[
                    ["agentField_schedule_kind", reminder.schedule_kind],
                    ["agentField_period_kind", reminder.period_kind],
                    ["agentField_timezone", reminder.timezone],
                    ["agentField_due_at", reminder.due_at],
                    ["agentField_checks", reminder.checks],
                    ["agentField_budget_minor", String(reminder.budget_minor)],
                    ["agentField_escalation", reminder.escalation],
                    ["agentField_output_format", reminder.output_format],
                  ].map(([label, value]) => (
                    <div key={label}>
                      <dt className="text-muted-foreground">{t(label!)}</dt>
                      <dd className="break-words">{value || "?"}</dd>
                    </div>
                  ))}
                </dl>
                {["ACTIVE", "PAUSED"].includes(reminder.state) && (
                  <div className="flex flex-wrap gap-2">
                    <Button
                      variant="outline"
                      disabled={mutating}
                      onClick={() =>
                        void action(
                          `reminder:${reminder.id}:${reminder.version}`,
                          (key) =>
                            iamApi.changeAgentReminder(
                              tenant,
                              agent,
                              String(reminder.id),
                              {
                                version: reminder.version,
                                state:
                                  reminder.state === "ACTIVE"
                                    ? "PAUSED"
                                    : "ACTIVE",
                              },
                              key
                            )
                        )
                      }
                    >
                      {t(
                        reminder.state === "ACTIVE"
                          ? "agentPause"
                          : "agentResume"
                      )}
                    </Button>
                    <Button
                      variant="outline"
                      disabled={mutating}
                      onClick={() =>
                        void action(
                          `cancel:${reminder.id}:${reminder.version}`,
                          (key) =>
                            iamApi.changeAgentReminder(
                              tenant,
                              agent,
                              String(reminder.id),
                              { version: reminder.version, state: "CANCELLED" },
                              key
                            )
                        )
                      }
                    >
                      {t("agentCancel")}
                    </Button>
                  </div>
                )}
              </article>
            ))}
          </div>
        </>
      )}
      {tab === "operations" && (
        <>
          <details className="rounded-md border p-4">
            <summary className="min-h-11 cursor-pointer font-medium">
              {t("agentManifestTitle")}
            </summary>
            <AgentOperationManifest
              key={`${tenant}:${agent}`}
              tenant={tenant}
              agent={agent}
            />
          </details>
          <p className="text-sm text-muted-foreground">
            {t("agentManifestNotice")}
          </p>
          <div
            className="flex flex-wrap gap-2"
            role="group"
            aria-label={t("agentOperationFilters")}
          >
            {(["all", "sideEffects", "human"] as const).map((value) => (
              <Button
                key={value}
                variant={operationFilter === value ? "default" : "outline"}
                aria-pressed={operationFilter === value}
                onClick={() => setOperationFilter(value)}
              >
                {t(
                  value === "all"
                    ? "all"
                    : value === "human"
                      ? "agentHumanOnly"
                      : "agentWithSideEffects"
                )}
              </Button>
            ))}
          </div>
          {!loading && !error && !visibleOperations.length && (
            <p className="text-sm text-muted-foreground">{t("agentEmpty")}</p>
          )}
          <AutoTable
            data={visibleOperations}
            rowKey="name"
            pagination={false}
            exportable={false}
            columns={[
              {
                field: "name",
                name: t("agentTab_operations"),
                width: "260px",
                format: (_, operation) => (
                  <div className="space-y-2">
                    <h3 className="font-mono font-semibold break-all">
                      {operation.name}
                    </h3>
                    <p>{operation.summary}</p>
                    <p className="font-mono text-xs break-all">
                      {operation.method} {operation.path}
                    </p>
                  </div>
                ),
              },
              {
                field: "input_schema",
                name: t("agentSchema"),
                width: "280px",
                format: (_, operation) => (
                  <details>
                    <summary className="min-h-11 cursor-pointer text-sm">
                      {t("agentSchema")}
                    </summary>
                    <pre className="max-h-96 max-w-72 overflow-auto bg-muted p-3 text-xs">
                      {JSON.stringify(
                        {
                          parameters: operation.parameters,
                          input: operation.input_schema,
                          output: operation.output_schema,
                        },
                        null,
                        2
                      )}
                    </pre>
                  </details>
                ),
              },
              {
                field: "side_effects",
                name: t("agentWithSideEffects"),
                width: "180px",
                format: (_, operation) => (
                  <div className="space-y-2">
                    <p>{(operation.side_effects ?? []).join(", ")}</p>
                    {operation.human_only && <p>{t("agentHumanOnly")}</p>}
                  </div>
                ),
              },
              {
                field: "key_rule",
                name: "Idempotency-Key",
                width: "240px",
                format: (_, operation) =>
                  operation.idempotency_required ? (
                    <p className="text-sm break-words">
                      {operation.key_rule || t("agentKeyRule")}
                    </p>
                  ) : (
                    "—"
                  ),
              },
              {
                field: "required_permission",
                name: t("permissions"),
                width: "220px",
                format: (_, operation) => (
                  <div className="space-y-2">
                    <p className="font-mono text-xs break-all">
                      {operation.required_permission || "SELF"}
                    </p>
                    <p>
                      {t(
                        operation.delegable
                          ? "agentDelegable"
                          : "agentNotDelegable"
                      )}
                    </p>
                    <p className="text-xs text-muted-foreground">
                      {operation.via}
                    </p>
                  </div>
                ),
              },
            ]}
          />
        </>
      )}
      {tab === "tools" && (
        <>
          <p className="text-sm text-muted-foreground">
            {t("agentToolNotice")}
          </p>
          {catalog && (
            <>
              <Button variant="outline" onClick={() => setShowForm(!showForm)}>
                {t("agentToolCreate")}
              </Button>
              {showForm && <ToolForm tenant={tenant} onDone={refresh} />}
            </>
          )}
          {!loading && !tools.length && !error && (
            <p className="p-5 text-muted-foreground">{t("agentEmpty")}</p>
          )}
          <div className="divide-y border-y">
            {tools
              .filter((tool) => !toolKind || tool.kind === toolKind)
              .map((tool) => (
                <article key={tool.id} className="space-y-3 py-5">
                  <h3 className="font-semibold">{tool.title}</h3>
                  <p className="font-mono text-sm break-all">
                    {tool.entry_key} ? {tool.kind} ? {tool.transport}
                  </p>
                  <p className="text-sm break-all">
                    {tool.endpoint || tool.content_hash}
                  </p>
                  {tool.skill_body && (
                    <pre className="text-sm break-words whitespace-pre-wrap">
                      {tool.skill_body}
                    </pre>
                  )}
                  <p className="text-sm text-muted-foreground">
                    {t(
                      tool.has_credential
                        ? "agentCredentialSealed"
                        : "agentCredentialNone"
                    )}
                  </p>
                  {catalog && agent && (
                    <div className="flex flex-wrap gap-2">
                      <Button
                        variant="outline"
                        disabled={mutating}
                        onClick={() =>
                          void action(`grant:${tool.id}`, (key) =>
                            iamApi.grantAgentTool(
                              tenant,
                              String(tool.id),
                              { agent_principal_id: agent, state: "ACTIVE" },
                              key
                            )
                          )
                        }
                      >
                        {t("agentGrant")}
                      </Button>
                      <Button
                        variant="outline"
                        disabled={mutating}
                        onClick={() =>
                          void action(`revoke:${tool.id}`, (key) =>
                            iamApi.grantAgentTool(
                              tenant,
                              String(tool.id),
                              { agent_principal_id: agent, state: "REVOKED" },
                              key
                            )
                          )
                        }
                      >
                        {t("agentRevoke")}
                      </Button>
                    </div>
                  )}
                </article>
              ))}
          </div>
        </>
      )}
      {cursor && (
        <Button
          variant="outline"
          disabled={loading}
          onClick={() => void more()}
        >
          {t("agentMore")}
        </Button>
      )}
    </section>
  )
}
function ContractForm({
  route,
  fields,
  onSubmit,
  onDone,
  reauthentication,
}: {
  route: ApiRoute
  reauthentication?: (
    values: Record<string, unknown>
  ) => { purpose: string; target: string } | null
  fields: Record<string, FieldConfig>
  onSubmit: (values: Record<string, unknown>, key: string) => Promise<unknown>
  onDone: () => void
}) {
  const t = useTranslations("iam")
  const [pending, setPending] = useState<{
    values: Record<string, unknown>
    key: string
    purpose: string
    target: string
  } | null>(null)
  const metadata = useRequestContract(route)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const [error, setError] = useState("")
  return (
    <div className="space-y-3 border p-4">
      {error && <Alert>{error}</Alert>}
      {metadata.failed && (
        <Button variant="outline" onClick={metadata.retry}>
          {t("agentRefresh")}
        </Button>
      )}
      <AutoForm
        contract={metadata.contract}
        fields={fields}
        onSubmit={async (values) => {
          const identity = JSON.stringify(values)
          const key =
            attempt.current?.body === identity
              ? attempt.current.key
              : crypto.randomUUID()
          attempt.current = { body: identity, key }
          const binding = reauthentication?.(values)
          if (binding) {
            setPending({ values: { ...values }, key, ...binding })
            return
          }
          try {
            await onSubmit(values, key)
            attempt.current = null
            onDone()
          } catch (e) {
            setError((e as Error).message)
            throw e
          }
        }}
      >
        <Button type="submit" disabled={!metadata.contract}>
          {t("agentSave")}
        </Button>
      </AutoForm>
      {pending && (
        <ReauthenticationDialog
          purpose={pending.purpose}
          target={pending.target}
          onClose={() => setPending(null)}
          onVerified={async (proof) => {
            await onSubmit(
              { ...pending.values, reauth_proof: proof },
              pending.key
            )
            attempt.current = null
            setPending(null)
            onDone()
          }}
        />
      )}
    </div>
  )
}
function ReminderForm({
  tenant,
  agent,
  onDone,
}: {
  tenant: string
  agent: string
  onDone: () => void
}) {
  const t = useTranslations("iam")
  const route = useMemo(
    () =>
      apiRoute(
        "post",
        "/tenants/{tenant_id}/agents/{agent_principal_id}/reminders",
        { path: { tenant_id: tenant, agent_principal_id: agent } }
      ),
    [tenant, agent]
  )
  const fields: Record<string, FieldConfig> = {}
  for (const name of [
    "project_id",
    "anchor_kind",
    "thread_id",
    "work_item_id",
    "title",
    "note",
    "schedule_kind",
    "period_kind",
    "due_at",
    "interval_minutes",
    "max_fires",
    "checks",
    "budget_minor",
    "escalation",
    "output_format",
  ])
    fields[name] = {
      name: t(`agentField_${name}`),
      type: ["note", "checks", "escalation", "output_format"].includes(name)
        ? "textarea"
        : "text",
      value:
        name === "max_fires"
          ? 1
          : ["interval_minutes", "budget_minor"].includes(name)
            ? 0
            : "",
      ...(["interval_minutes", "max_fires", "budget_minor"].includes(name)
        ? { config: { type: "number" } }
        : {}),
    }
  fields.anchor_kind = {
    name: t("agentField_anchor_kind"),
    type: "select",
    value: "THREAD",
    config: {
      options: ["THREAD", "WORK_ITEM"].map((value) => ({
        value,
        label: value,
      })),
    },
  }
  fields.schedule_kind = {
    name: t("agentField_schedule_kind"),
    type: "select",
    value: "ONCE",
    config: {
      options: ["ONCE", "INTERVAL", "PERIOD"].map((value) => ({
        value,
        label: value,
      })),
    },
  }
  fields.period_kind = {
    name: t("agentField_period_kind"),
    type: "select",
    value: "DAY",
    show: (values) => values?.schedule_kind === "PERIOD",
    config: {
      options: ["DAY", "WEEK", "MONTH", "QUARTER", "YEAR"].map((value) => ({
        value,
        label: value,
      })),
    },
  }
  fields.thread_id.show = (values) => values?.anchor_kind !== "WORK_ITEM"
  fields.work_item_id.show = (values) => values?.anchor_kind === "WORK_ITEM"
  fields.due_at.show = (values) => values?.schedule_kind !== "PERIOD"
  fields.interval_minutes.show = (values) =>
    values?.schedule_kind === "INTERVAL"
  return (
    <ContractForm
      route={route}
      fields={fields}
      onDone={onDone}
      onSubmit={(values, key) => {
        const period = values.schedule_kind === "PERIOD"
        const date = period ? undefined : new Date(String(values.due_at))
        return iamApi.createAgentReminder(
          tenant,
          agent,
          {
            ...values,
            project_id: String(values.project_id),
            anchor_kind: String(values.anchor_kind) as NonNullable<
              components["schemas"]["ReminderInput"]["anchor_kind"]
            >,
            work_item_id:
              values.anchor_kind === "WORK_ITEM"
                ? String(values.work_item_id)
                : undefined,
            thread_id:
              values.anchor_kind === "WORK_ITEM"
                ? undefined
                : String(values.thread_id),
            schedule_kind: String(values.schedule_kind) as NonNullable<
              components["schemas"]["ReminderInput"]["schedule_kind"]
            >,
            period_kind: period
              ? (String(values.period_kind) as NonNullable<
                  components["schemas"]["ReminderInput"]["period_kind"]
                >)
              : undefined,
            title: String(values.title),
            note: String(values.note),
            due_at: date?.toISOString(),
            interval_minutes:
              values.schedule_kind === "INTERVAL"
                ? Number(values.interval_minutes)
                : 0,
            max_fires: Number(values.max_fires),
            checks: String(values.checks),
            budget_minor: Number(values.budget_minor),
            escalation: String(values.escalation),
            output_format: String(values.output_format),
          },
          key
        )
      }}
    />
  )
}
function ResetForm({
  tenant,
  agent,
  onDone,
}: {
  tenant: string
  agent: string
  onDone: () => void
}) {
  const t = useTranslations("iam")
  const route = useMemo(
    () =>
      apiRoute(
        "post",
        "/tenants/{tenant_id}/agents/{agent_principal_id}/resets",
        { path: { tenant_id: tenant, agent_principal_id: agent } }
      ),
    [tenant, agent]
  )
  return (
    <ContractForm
      route={route}
      fields={{
        kind: {
          name: t("agentField_kind"),
          type: "select",
          value: "SESSION",
          config: {
            options: ["RESTART", "SESSION", "FULL"].map((value) => ({
              value,
              label: t(`agentReset_${value}`),
            })),
          },
        },
        reason: { name: t("agentField_reason"), type: "textarea", value: "" },
        confirm_name: {
          name: t("agentField_confirm_name"),
          type: "text",
          value: "",
        },
      }}
      reauthentication={(values) =>
        values.kind === "FULL"
          ? {
              purpose: "agent_full_reset",
              target: `tenant:${tenant}:agent:${agent}`,
            }
          : null
      }
      onDone={onDone}
      onSubmit={(values, key) =>
        iamApi.agentReset(
          tenant,
          agent,
          values as components["schemas"]["ResetInput"],
          key
        )
      }
    />
  )
}
function ToolForm({ tenant, onDone }: { tenant: string; onDone: () => void }) {
  const t = useTranslations("iam")
  const route = useMemo(
    () =>
      apiRoute("post", "/tenants/{tenant_id}/agent-tools", {
        path: { tenant_id: tenant },
      }),
    [tenant]
  )
  const fields: Record<string, FieldConfig> = {}
  for (const name of [
    "entry_key",
    "title",
    "endpoint",
    "content_hash",
    "skill_body",
  ])
    fields[name] = {
      name: t(`agentField_${name}`),
      type: name === "skill_body" ? "textarea" : "text",
      value: "",
    }
  fields.kind = {
    name: t("agentField_kind"),
    type: "select",
    value: "MCP_SERVER",
    config: {
      options: ["MCP_SERVER", "SKILL"].map((value) => ({
        value,
        label: t(`agentToolKind_${value}`),
      })),
    },
  }
  fields.transport = {
    name: t("agentField_transport"),
    type: "select",
    value: "STREAMABLE_HTTP",
    config: {
      options: ["STREAMABLE_HTTP", "STDIO_PACKAGE", ""].map((value) => ({
        value,
        label: value || t("agentNoTransport"),
      })),
    },
  }
  fields.credential = {
    name: t("agentField_credential"),
    type: "text",
    value: "",
    config: { type: "password", autoComplete: "new-password" },
  }
  return (
    <ContractForm
      route={route}
      fields={fields}
      reauthentication={(values) =>
        values.credential
          ? {
              purpose: "agent_tool_credential_write",
              target: `tenant:${tenant}:tool:${String(values.entry_key)}`,
            }
          : null
      }
      onDone={onDone}
      onSubmit={(values, key) =>
        iamApi.createAgentTool(
          tenant,
          {
            ...values,
          } as components["schemas"]["ToolInput"],
          key
        )
      }
    />
  )
}
