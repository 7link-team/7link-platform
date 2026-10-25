import { PlanningContent } from "./planning"
import { planningLabels } from "./planning-labels"
import { useEffect, useRef, useState } from "react"
import { useOutletContext, useParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import {
  basePath,
  read,
  write,
  setCell,
  worklogPeriodQuery,
  previewWorkflow,
  workflowDefinition,
  applyCellConditions,
  type WorkflowPreview,
  type Worklog,
  type Workflow,
  type Page,
  type Summary,
} from "./api"
import { messages } from "./messages"
function useLabels() {
  const locale = useLocale()
  return messages[locale as keyof typeof messages] ?? messages["en-US"]
}
function useScope() {
  const { tenantID = "", entityID = "" } = useParams()
  const context = useOutletContext<{
    permissions?: Set<string>
    permissionsResolved?: boolean
    principalID?: string
  }>()
  return { base: basePath(tenantID, entityID), project: entityID, ...context }
}
const inputClass = "rounded-md border bg-background px-3 py-2 text-sm"
export function WorklogsPage() {
  const labels = useLabels()
  const scope = useScope()
  if (!scope.permissionsResolved) return <p role="status">{labels.loading}</p>
  if (!scope.project || !scope.permissions?.has("work_item_read"))
    return <Alert>{labels.forbidden}</Alert>
  return <WorklogContent key={scope.base} />
}
function WorklogContent() {
  const l = useLabels()
  const s = useScope()
  const [revision, setRevision] = useState(0)
  const [cursor, setCursor] = useState("")
  const [workFilter, setWorkFilter] = useState("")
  const [from, setFrom] = useState("")
  const [until, setUntil] = useState("")
  const periodQuery = worklogPeriodQuery(workFilter, from, until)
  const [history, setHistory] = useState<string[]>([])
  const [page, setPage] = useState<Page<Worklog>>({
    items: [],
    has_more: false,
  })
  const [summary, setSummary] = useState<Summary | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [reason, setReason] = useState("")
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    const c = new AbortController()
    void Promise.all([
      read<Page<Worklog>>(
        `${s.base}/worklogs?limit=25&${periodQuery}${cursor ? `&cursor=${cursor}` : ""}`,
        c.signal
      ),
      read<Summary>(`${s.base}/worklog-summary?${periodQuery}`, c.signal),
    ]).then(
      ([p, v]) => {
        if (!c.signal.aborted) {
          setPage(p)
          setSummary(v)
          setError("")
          setLoading(false)
        }
      },
      (e) => {
        if (!c.signal.aborted) {
          setError(String(e.message ?? e))
          setLoading(false)
        }
      }
    )
    return () => c.abort()
  }, [s.base, cursor, revision, periodQuery])
  const reload = () => {
    setLoading(true)
    setRevision((n) => n + 1)
  }
  async function action(row: Worklog, state: string) {
    setBusy(true)
    setError("")
    try {
      await write(
        `${s.base}/worklogs/${row.id}/state`,
        "PUT",
        { state, reason },
        crypto.randomUUID(),
        row.version
      )
      reload()
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <main className="space-y-5">
      <PageHeader title={l.worklogs} />
      <div className="grid gap-3 sm:grid-cols-3">
        {[
          {
            label: l.workItem,
            value: workFilter,
            set: setWorkFilter,
            type: "text",
          },
          { label: l.periodFrom, value: from, set: setFrom, type: "date" },
          { label: l.periodUntil, value: until, set: setUntil, type: "date" },
        ].map((filter) => (
          <label key={filter.label} className="flex flex-col gap-1 text-sm">
            {filter.label}
            <input
              className={inputClass}
              type={filter.type}
              value={filter.value}
              onChange={(e) => {
                filter.set(e.target.value)
                setCursor("")
                setHistory([])
                setLoading(true)
              }}
            />
          </label>
        ))}
      </div>
      <p className="text-sm text-muted-foreground">{l.runtime}</p>
      {error && <Alert>{error}</Alert>}
      {loading && <p role="status">{l.loading}</p>}
      {summary && (
        <p>
          {l.summary}: {summary.approved_minutes} · {l.cost}:{" "}
          {summary.known_cost_micros} · {l.unknownCost}:{" "}
          {summary.unknown_cost_count}
        </p>
      )}
      <div className="flex flex-wrap gap-3">
        <Button variant="outline" onClick={reload}>
          {l.reload}
        </Button>
      </div>
      {s.permissions?.has("worklog_approve") && (
        <label className="flex flex-col gap-1">
          {l.reason}
          <input
            className={inputClass}
            value={reason}
            maxLength={500}
            onChange={(e) => setReason(e.target.value)}
          />
        </label>
      )}
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead>
            <tr>
              {[
                l.principal,
                l.date,
                l.workItem,
                l.category,
                l.minutes,
                l.cost,
                l.state,
                l.note,
              ].map((v) => (
                <th key={v} className="p-2">
                  {v}
                </th>
              ))}
              <th />
            </tr>
          </thead>
          <tbody>
            {page.items.map((row) => (
              <tr key={row.id} className="border-t">
                <td className="p-2">
                  {row.principal_id} ({row.actor_kind})
                </td>
                <td className="p-2">{row.work_date}</td>
                <td className="p-2">{row.work_item_id ?? l.unassigned}</td>
                <td className="p-2">{row.category}</td>
                <td className="p-2">{row.minutes}</td>
                <td className="p-2">{row.cost_micros ?? l.unknown}</td>
                <td className="p-2">
                  {row.state}
                  <WorklogApproval row={row} labels={l} />
                </td>
                <td className="p-2">{row.note}</td>
                <td className="p-2">
                  <div className="flex flex-wrap gap-2">
                    {row.work_item_id && <a className="underline" href={`./work-items?work_item_id=${encodeURIComponent(row.work_item_id)}`}>{l.workItem}</a>}
                    {!row.execution_id &&
                      row.state === "DRAFT" &&
                      s.permissions?.has("worklog_submit") && (
                        <Button
                          disabled={busy}
                          onClick={() => void action(row, "SUBMITTED")}
                        >
                          {l.submit}
                        </Button>
                      )}
                    {!row.execution_id &&
                      row.state === "SUBMITTED" &&
                      s.permissions?.has("worklog_approve") && (
                        <>
                          <Button
                            disabled={busy}
                            onClick={() => void action(row, "APPROVED")}
                          >
                            {l.approve}
                          </Button>
                          <Button
                            disabled={busy || !reason.trim()}
                            variant="outline"
                            onClick={() => void action(row, "REJECTED")}
                          >
                            {l.reject}
                          </Button>
                        </>
                      )}
                    {!row.execution_id &&
                      row.state === "APPROVED" &&
                      s.permissions?.has("worklog_approve") && (
                        <Button
                          disabled={busy || !reason.trim()}
                          variant="outline"
                          onClick={() => void action(row, "SUBMITTED")}
                        >
                          {l.withdraw}
                        </Button>
                      )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!loading && !page.items.length && <p>{l.empty}</p>}
      <div className="flex gap-2">
        <Button
          variant="outline"
          disabled={!history.length || loading}
          onClick={() => {
            setCursor(history.at(-1) ?? "")
            setHistory((h) => h.slice(0, -1))
            setLoading(true)
          }}
        >
          {l.previous}
        </Button>
        <Button
          variant="outline"
          disabled={!page.has_more || loading}
          onClick={() => {
            setHistory((h) => [...h, cursor])
            setCursor(page.next_cursor ?? "")
            setLoading(true)
          }}
        >
          {l.next}
        </Button>
      </div>
    </main>
  )
}
export function WorkflowConfigPage() {
  const locale = useLocale()
  const pl = planningLabels[locale] ?? planningLabels["en-US"]!
  const [tab, setTab] = useState("workflow")
  const l = useLabels()
  const s = useScope()
  if (!s.permissionsResolved) return <p role="status">{l.loading}</p>
  if (!s.project || !s.permissions?.has("work_item_read"))
    return <Alert>{l.forbidden}</Alert>
  return (
    <main className="space-y-4">
      <div className="flex gap-2">
        <Button
          variant={tab === "workflow" ? "default" : "outline"}
          onClick={() => setTab("workflow")}
        >
          {pl.workflow}
        </Button>
        <Button
          variant={tab === "planning" ? "default" : "outline"}
          onClick={() => setTab("planning")}
        >
          {pl.planning}
        </Button>
      </div>
      {tab === "workflow" ? (
        <WorkflowContent key={s.base} />
      ) : (
        <PlanningContent
          key={s.base}
          base={s.base}
          permissions={s.permissions}
          principalID={s.principalID}
        />
      )}
    </main>
  )
}
function WorkflowContent() {
  const locale = useLocale()
  const pl = planningLabels[locale] ?? planningLabels["en-US"]!
  const l = useLabels()
  const s = useScope()
  const [kind, setKind] = useState("TASK")
  const [teamDefault, setTeamDefault] = useState(false)
  const [version, setVersion] = useState(0)
  const [doc, setDoc] = useState<Workflow>({
    version: 0,
    states: [],
    transitions: [],
  })
  const [selected, setSelected] = useState<{ from: string; to: string } | null>(
    null
  )
  const [conditions, setConditions] = useState("[]")
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)
  const pending = useRef(false)
  const attempt = useRef<{ signature: string; key: string } | null>(null)
  const [preview, setPreview] = useState<{
    signature: string
    value: WorkflowPreview
  } | null>(null)
  const can = s.permissions?.has("workflow_manage") ?? false
  const url = `${s.base}/workflow-configs/${kind}${teamDefault ? "?team_default=true" : ""}`
  const previewSignature = JSON.stringify([url, doc, selected, conditions])
  useEffect(() => {
    const c = new AbortController()
    void read<Workflow>(url, c.signal).then(
      (v) => {
        if (!c.signal.aborted) {
          setDoc(v)
          setVersion(v.version)
          setNotice("")
          setError("")
          setLoading(false)
        }
      },
      (e) => {
        if (!c.signal.aborted) {
          setError(String(e.message ?? e))
          setDoc({ version: 0, states: [], transitions: [] })
          setVersion(0)
          setLoading(false)
        }
      }
    )
    return () => c.abort()
  }, [url])
  function chooseTransition(from: string, to: string) {
    let current = doc
    if (selected) {
      try {
        current = applyCellConditions(current, selected, conditions)
      } catch {
        setError(l.conditions)
        return false
      }
    }
    setDoc(current)
    setSelected({ from, to })
    setConditions(
      JSON.stringify(
        current.transitions.find((cell) => cell.from === from && cell.to === to)
          ?.conditions ?? [],
        null,
        2
      )
    )
    setError("")
    return true
  }
  async function save(remove = false, previewOnly = false) {
    if (pending.current) return
    pending.current = true
    setBusy(true)
    setError("")
    try {
      const body = remove ? doc : applyCellConditions(doc, selected, conditions)
      if (previewOnly) {
        const value = await previewWorkflow(
          url,
          version,
          workflowDefinition(body),
          undefined,
          remove
        )
        setPreview({ signature: previewSignature, value })
        return
      }
      const definition = workflowDefinition(body)
      const signature = JSON.stringify([url, remove, version, definition])
      if (attempt.current?.signature !== signature)
        attempt.current = { signature, key: crypto.randomUUID() }
      const result = await write<Workflow>(
        url,
        remove ? "DELETE" : "PUT",
        definition,
        attempt.current.key,
        version || undefined
      )
      setDoc(result)
      setSelected(null)
      setConditions("[]")
      setVersion(result.version)
      setNotice(l.saved)
      setPreview(null)
      attempt.current = null
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e))
    } finally {
      pending.current = false
      setBusy(false)
    }
  }
  return (
    <main className="space-y-5">
      <PageHeader title={l.workflow} />
      <label className="flex flex-col gap-1">
        {pl.carry}
        <select
          className={inputClass}
          value={doc.carry_over_mode ?? "NEXT_ITERATION"}
          disabled={!can || busy || loading}
          onChange={(e) =>
            setDoc({
              ...doc,
              carry_over_mode: e.target.value as "NEXT_ITERATION" | "BACKLOG",
            })
          }
        >
          <option value="NEXT_ITERATION">{pl.nextIteration}</option>
          <option value="BACKLOG">{pl.backlog}</option>
        </select>
      </label>
      <p className="text-sm text-muted-foreground">{l.configHint}</p>
      <p className="text-sm" role="status">
        {l.effectiveSource}:{" "}
        {doc.source === "PROJECT"
          ? l.projectOverride
          : doc.source === "TEAM"
            ? l.teamDefaults
            : l.systemDefaults}{" "}
        · {l.teamVersion}: {doc.team_version ?? 0}
      </p>
      <label className="flex items-center gap-3 text-sm">
        <input
          type="checkbox"
          checked={teamDefault}
          disabled={busy || loading}
          onChange={(e) => {
            setTeamDefault(e.target.checked)
            setLoading(true)
            setSelected(null)
          }}
        />
        {l.editTeamDefaults}
      </label>
      {error && <Alert>{error}</Alert>}
      {notice && <p role="status">{notice}</p>}
      {preview?.signature === previewSignature && (
        <WorkflowPreviewResult value={preview.value} labels={l} />
      )}
      {loading && <p role="status">{l.loading}</p>}
      <label className="flex items-center gap-3">
        {l.type}
        <select
          className={inputClass}
          value={kind}
          disabled={busy || loading}
          onChange={(e) => {
            setKind(e.target.value)
            setLoading(true)
            setSelected(null)
          }}
        >
          {["TASK", "BUG", "RISK", "INCIDENT"].map((v) => (
            <option key={v}>{v}</option>
          ))}
        </select>
      </label>
      <section className="rounded-md border bg-card p-4" aria-label={l.group}>
        <h2 className="mb-4 font-medium">{l.group}</h2>
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
          {["TODO", "IN_PROGRESS", "VERIFYING", "DONE", "CANCELLED"].map(
            (group) => (
              <section key={group} className="min-w-0 space-y-2">
                <h3 className="text-xs font-semibold tracking-wide text-muted-foreground">
                  {group}
                </h3>
                {doc.states
                  .filter((state) => state.group === group)
                  .map((state) => (
                    <div
                      key={state.key}
                      className="rounded-md border bg-background px-3 py-2"
                    >
                      <p className="text-sm font-medium break-words">
                        {state.display_name || state.key}
                      </p>
                      <p className="mt-1 font-mono text-xs break-all text-muted-foreground">
                        {state.maps_to}
                      </p>
                    </div>
                  ))}
              </section>
            )
          )}
        </div>
      </section>
      <details className="rounded-md border p-4">
        <summary className="cursor-pointer font-medium">
          {l.edit} / {l.states}
        </summary>
        <div className="mt-4 space-y-3">
          {doc.states.map((state, index) => (
            <div key={index} className="grid gap-2 md:grid-cols-5">
              {(["key", "display_name", "group", "maps_to"] as const).map(
                (field, i) => (
                  <label key={field} className="flex flex-col gap-1 text-sm">
                    {[l.key, l.name, l.group, l.maps][i]}
                    <input
                      disabled={!can || busy || loading}
                      className={inputClass}
                      value={state[field]}
                      onChange={(e) => {
                        if (
                          field === "key" &&
                          (selected?.from === state.key ||
                            selected?.to === state.key)
                        ) {
                          setSelected(null)
                          setConditions("[]")
                        }
                        setDoc((d) => ({
                          ...d,
                          states: d.states.map((v, j) =>
                            j === index ? { ...v, [field]: e.target.value } : v
                          ),
                        }))
                      }}
                    />
                  </label>
                )
              )}
              <label className="flex min-h-11 items-center gap-2 text-sm">
                <input type="checkbox" checked={state.archived ?? false}
                  disabled={!can || busy || loading}
                  onChange={(e) => setDoc((d) => ({ ...d, states: d.states.map((v, j) => j === index ? { ...v, archived: e.target.checked, migrate_to: e.target.checked ? v.migrate_to : undefined } : v) }))} />
                {l.archived}
              </label>
              {state.archived && <label className="flex min-w-0 flex-col gap-1 text-sm">
                {l.migrateTo}
                <select className={inputClass} value={state.migrate_to ?? ""} disabled={!can || busy || loading}
                  onChange={(e) => setDoc((d) => ({ ...d, states: d.states.map((v, j) => j === index ? { ...v, migrate_to: e.target.value } : v) }))}>
                  <option value="">—</option>
                  {doc.states.filter((v) => v.key !== state.key && !v.archived && v.group === state.group && v.maps_to === state.maps_to).map((v) => <option key={v.key} value={v.key}>{v.display_name || v.key}</option>)}
                </select>
              </label>}

            </div>
          ))}
        </div>
        {can && (
          <Button
            variant="outline"
            disabled={busy || loading}
            onClick={() =>
              setDoc((d) => ({
                ...d,
                states: [
                  ...d.states,
                  {
                    key: `state_${d.states.length + 1}`,
                    display_name: "",
                    group: "TODO",
                    maps_to: "DRAFT",
                  },
                ],
              }))
            }
          >
            {l.addState}
          </Button>
        )}
      </details>
      <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_20rem]">
        <section className="min-w-0 rounded-md border">
          <h2 className="border-b p-4 font-medium">{l.transitions}</h2>
          <div className="max-h-[38rem] overflow-auto">
            <table className="w-full border-collapse text-sm">
              <thead>
                <tr>
                  <th />
                  {doc.states.map((v) => (
                    <th
                      key={v.key}
                      scope="col"
                      className="sticky top-0 z-10 border-b bg-card p-3"
                    >
                      {v.display_name || v.key}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {doc.states.map((from) => (
                  <tr key={from.key}>
                    <th
                      scope="row"
                      className="sticky left-0 z-10 border-b bg-card p-3 text-left"
                    >
                      {from.display_name || from.key}
                    </th>
                    {doc.states.map((to) => (
                      <td
                        key={to.key}
                        className={`border-b p-3 text-center ${selected?.from === from.key && selected.to === to.key ? "bg-muted ring-1 ring-ring ring-inset" : ""}`}
                      >
                        <input
                          type="checkbox"
                          aria-label={`${from.key} → ${to.key}`}
                          disabled={!can || busy || to.archived || from.key === to.key}
                          checked={doc.transitions.some(
                            (t) =>
                              t.from === from.key &&
                              t.to === to.key &&
                              t.enabled
                          )}
                          onChange={(e) => {
                            if (!chooseTransition(from.key, to.key)) return
                            setDoc((d) =>
                              setCell(d, from.key, to.key, e.target.checked)
                            )
                          }}
                        />
                        <button
                          className="ml-2 underline"
                          type="button"
                          disabled={busy || loading || from.key === to.key}
                          onClick={() => chooseTransition(from.key, to.key)}
                        >
                          {l.edit}
                        </button>
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
        {selected && (
          <aside className="space-y-3 rounded-md border bg-card p-4 xl:sticky xl:top-4">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                disabled={!can || busy}
                checked={
                  doc.transitions.find(
                    (t) => t.from === selected.from && t.to === selected.to
                  )?.manual_allowed ?? true
                }
                onChange={(e) =>
                  setDoc((d) => {
                    const next = setCell(
                      d,
                      selected.from,
                      selected.to,
                      d.transitions.find(
                        (t) => t.from === selected.from && t.to === selected.to
                      )?.enabled ?? false
                    )
                    return {
                      ...next,
                      transitions: next.transitions.map((t) =>
                        t.from === selected.from && t.to === selected.to
                          ? { ...t, manual_allowed: e.target.checked }
                          : t
                      ),
                    }
                  })
                }
              />
              {l.manual}
            </label>
            <label className="flex flex-col gap-2">
              {l.conditions} ({selected.from} → {selected.to})
              <textarea
                className={`${inputClass} min-h-40 font-mono`}
                value={conditions}
                disabled={!can || busy || loading}
                onChange={(e) => setConditions(e.target.value)}
              />
            </label>
          </aside>
        )}
      </div>
      {can && (
        <div className="flex flex-wrap gap-3">
          <Button
            variant="outline"
            disabled={busy || loading}
            onClick={() => void save(false, true)}
          >
            {l.preview}
          </Button>
          <Button disabled={busy || loading} onClick={() => void save()}>
            {l.save}
          </Button>
          <Button
            variant="outline"
            disabled={busy || loading || !version}
            onClick={() => void save(true, true)}
            className="h-auto min-h-9 max-w-full whitespace-normal"
          >
            {l.previewReset}
          </Button>
          <Button
            variant="outline"
            disabled={busy || loading || !version}
            onClick={() => void save(true)}
          >
            {l.remove}
          </Button>
        </div>
      )}
    </main>
  )
}

export function WorkflowPreviewResult({
  value,
  labels: l,
}: {
  value: WorkflowPreview
  labels: (typeof messages)[keyof typeof messages]
}) {
  return (
    <section
      className="space-y-2 rounded-md border p-4"
      aria-label={l.preview}
      role="status"
    >
      <p>{value.runtime_impact === "EVALUATED" ? l.impactNotice : l.previewNotice}</p>
      {value.runtime_impact === "EVALUATED" && <p>{l.projectsAffected}: {value.projects_affected ?? 0} · {l.itemsInArchivedStates}: {value.items_in_archived_states ?? 0}</p>}
      <dl className="space-y-2 break-words">
        <div>
          <dt className="font-medium">
            {l.changedStates} ({value.changed_states.length})
          </dt>
          <dd>{value.changed_states.join(", ") || "—"}</dd>
        </div>
        <div>
          <dt className="font-medium">
            {l.changedTransitions} ({value.changed_transitions.length})
          </dt>
          <dd>{value.changed_transitions.join(", ") || "—"}</dd>
        </div>
      </dl>
    </section>
  )
}

export function WorklogApproval({
  row,
  labels: l,
}: {
  row: Worklog
  labels: (typeof messages)[keyof typeof messages]
}) {
  if (!row.approved_by || !row.approved_at) return null
  return (
    <dl className="mt-1 text-xs text-muted-foreground">
      <div>
        <dt>{l.approvedBy}</dt>
        <dd>{row.approved_by}</dd>
      </div>
      <div>
        <dt>{l.approvedAt}</dt>
        <dd>
          <time dateTime={row.approved_at}>{row.approved_at}</time>
        </dd>
      </div>
    </dl>
  )
}
