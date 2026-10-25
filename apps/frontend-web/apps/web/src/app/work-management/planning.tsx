import { useEffect, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { AutoForm, type FieldConfig } from "@workspace/ui/components/auto-form"
import type { OperationRequestContract } from "@workspace/ui/components/auto-form/contract"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import { read, write, type Page } from "./api"
import { planningLabels } from "./planning-labels"

type Kind = "iterations" | "versions" | "milestones"
type Entity = {
  id: string
  name: string
  state: string
  version: number
  created_by: string
  achieved_at?: string
  goal?: string
  goal_id?: string
  starts_at?: string
  ends_at?: string
  due_at?: string
  planned_release_at?: string
}
type Assignment = {
  work_item_id: string
  version: number
  iteration_id?: string | null
  version_id?: string | null
  milestone_id?: string | null
}
const css = "rounded-md border bg-background px-3 py-2 text-sm"
function usePlanningLabels() {
  return planningLabels[useLocale()] ?? planningLabels["en-US"]!
}

export function PlanningContent({
  base,
  permissions,
  principalID,
}: {
  base: string
  permissions?: Set<string>
  principalID?: string
}) {
  const l = usePlanningLabels()
  const [kind, setKind] = useState<Kind>("iterations")
  const [page, setPage] = useState<Page<Entity>>({ items: [], has_more: false })
  const [cursor, setCursor] = useState("")
  const [history, setHistory] = useState<string[]>([])
  const [revision, setRevision] = useState(0)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [editing, setEditing] = useState<Entity | "new" | null>(null)
  const [reason, setReason] = useState("")
  const [target, setTarget] = useState("")
  const manage = permissions?.has("workflow_manage") ?? false
  const reload = () => {
    setLoading(true)
    setRevision((n) => n + 1)
  }
  useEffect(() => {
    const c = new AbortController()
    void read<Page<Entity>>(
      `${base}/${kind}?limit=25${cursor ? `&cursor=${cursor}` : ""}`,
      c.signal
    ).then(
      (v) => {
        if (!c.signal.aborted) {
          setPage(v)
          setLoading(false)
          setError("")
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
  }, [base, kind, cursor, revision])
  async function action(row: Entity, state: string) {
    setBusy(true)
    setError("")
    try {
      await write(
        `${base}/${kind}/${row.id}/state`,
        "PUT",
        {
          state,
          reason,
          ...(state === "CLOSED" && target
            ? { target_iteration_id: target }
            : {}),
        },
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
    <section className="space-y-4">
      <div className="flex flex-wrap gap-2">
        {(["iterations", "versions", "milestones"] as Kind[]).map((k) => (
          <Button
            key={k}
            variant={kind === k ? "default" : "outline"}
            onClick={() => {
              setKind(k)
              setCursor("")
              setHistory([])
              setEditing(null)
              setLoading(true)
            }}
          >
            {l[k]}
          </Button>
        ))}
        {manage && (
          <Button disabled={busy} onClick={() => setEditing("new")}>
            {l.create}
          </Button>
        )}
      </div>
      {error && <Alert>{error}</Alert>}
      {loading && <p role="status">{l.loading}</p>}
      {editing && (
        <EntityForm
          key={`${kind}:${editing === "new" ? "new" : editing.id}`}
          kind={kind}
          base={base}
          row={editing === "new" ? null : editing}
          onDone={() => {
            setEditing(null)
            reload()
          }}
          onCancel={() => setEditing(null)}
        />
      )}
      {kind === "versions" && (
        <label className="flex flex-col gap-1">
          {l.reason}
          <input
            className={css}
            value={reason}
            maxLength={500}
            onChange={(e) => setReason(e.target.value)}
          />
        </label>
      )}
      {kind === "iterations" && (
        <label className="flex flex-col gap-1">
          {l.target}
          <input
            className={css}
            value={target}
            inputMode="numeric"
            pattern="[1-9][0-9]*"
            onChange={(e) => setTarget(e.target.value)}
          />
        </label>
      )}
      {!loading && page.items.length === 0 && <p>{l.empty}</p>}
      <div className="divide-y rounded-md border">
        {page.items.map((row) => (
          <article
            key={row.id}
            className="flex flex-wrap items-center justify-between gap-3 p-4"
          >
            <div>
              <h3 className="font-medium">{row.name}</h3>
              <p className="text-sm text-muted-foreground">
                {l.state}: {row.achieved_at ? "ACHIEVED" : row.state} · {row.id}
              </p>
              <p className="text-sm">
                {row.starts_at} {row.ends_at} {row.due_at}{" "}
                {row.planned_release_at}
              </p>
            </div>
            <div className="flex flex-wrap gap-2">
              {manage && (
                <Button
                  variant="outline"
                  disabled={
                    busy || row.state === "LOCKED" || row.state === "CLOSED"
                  }
                  onClick={() => setEditing(row)}
                >
                  {l.edit}
                </Button>
              )}
              {stateActions(kind, row).map(
                (state) =>
                  (manage ||
                    (kind === "versions" &&
                      state === "PLANNING" &&
                      row.created_by === principalID)) && (
                    <Button
                      key={state}
                      variant="outline"
                      disabled={
                        busy ||
                        loading ||
                        (state === "PLANNING" && !reason.trim())
                      }
                      onClick={() => void action(row, state)}
                    >
                      {l[state as keyof typeof l]}
                    </Button>
                  )
              )}
            </div>
          </article>
        ))}
      </div>
      <div className="flex gap-2">
        <Button
          variant="outline"
          disabled={!history.length || loading}
          onClick={() => {
            setCursor(history.at(-1) ?? "")
            setHistory((v) => v.slice(0, -1))
            setLoading(true)
          }}
        >
          {l.previous}
        </Button>
        <Button
          variant="outline"
          disabled={!page.has_more || loading}
          onClick={() => {
            setHistory((v) => [...v, cursor])
            setCursor(page.next_cursor ?? "")
            setLoading(true)
          }}
        >
          {l.next}
        </Button>
      </div>
      {permissions?.has("work_item_write") && <AssignmentForm base={base} />}
    </section>
  )
}
function stateActions(kind: Kind, row: Entity) {
  if (kind === "iterations")
    return row.state === "CLOSED"
      ? []
      : row.state === "ACTIVE"
        ? ["CLOSED"]
        : ["ACTIVE", "CLOSED"]
  if (kind === "milestones") return row.achieved_at ? [] : ["ACHIEVED"]
  return row.state === "PLANNING"
    ? ["LOCKED", "RELEASED"]
    : row.state === "LOCKED"
      ? ["PLANNING", "RELEASED"]
      : row.state === "RELEASED"
        ? ["ARCHIVED"]
        : []
}
function EntityForm({
  base,
  kind,
  row,
  onDone,
  onCancel,
}: {
  base: string
  kind: Kind
  row: Entity | null
  onDone: () => void
  onCancel: () => void
}) {
  const l = usePlanningLabels()
  const [contract, setContract] = useState<OperationRequestContract | null>(
    null
  )
  const [error, setError] = useState("")
  const [attempt, setAttempt] = useState<{ body: string; key: string } | null>(
    null
  )
  const url = `${base}/${kind}${row ? `/${row.id}` : ""}`
  const method = row ? "PATCH" : "POST"
  useEffect(() => {
    const c = new AbortController()
    void iamApi
      .deliveryResultRequest<OperationRequestContract>(
        { method: "OPTIONS", url: `${url}?method=${method}` },
        { signal: c.signal }
      )
      .then(
        (v) => {
          if (!c.signal.aborted) setContract(v)
        },
        (e) => {
          if (!c.signal.aborted) setError(String(e.message ?? e))
        }
      )
    return () => c.abort()
  }, [url, method])
  const fields: Record<string, FieldConfig> = {
    name: { name: l.name, type: "text" },
    goal: {
      name: l.goal,
      type: "text",
      show: kind === "iterations",
      submit: kind === "iterations",
    },
    goal_id: {
      name: l.goalID,
      type: "text",
      show: kind !== "versions",
      submit: kind !== "versions",
    },
    starts_at: {
      name: l.starts_at,
      type: "text",
      show: kind === "iterations",
      submit: kind === "iterations",
    },
    ends_at: {
      name: l.ends_at,
      type: "text",
      show: kind === "iterations",
      submit: kind === "iterations",
    },
    due_at: {
      name: l.due_at,
      type: "text",
      show: kind === "milestones",
      submit: kind === "milestones",
    },
    planned_release_at: {
      name: l.planned_release_at,
      type: "text",
      show: kind === "versions",
      submit: kind === "versions",
    },
  }
  return (
    <section className="space-y-3 border p-4">
      {error && <Alert>{error}</Alert>}
      <AutoForm
        contract={contract}
        fields={fields}
        initialData={row ?? { name: "" }}
        mode={row ? "edit" : "add"}
        onSubmit={async (values) => {
          const body = Object.fromEntries(
            Object.entries(values).map(([name, value]) => [
              name,
              value === "" && name !== "name" && name !== "goal" ? null : value,
            ])
          )
          const serialized = JSON.stringify(body)
          const key =
            attempt?.body === serialized ? attempt.key : crypto.randomUUID()
          setAttempt({ body: serialized, key })
          try {
            await write(url, method, body, key, row?.version)
            onDone()
          } catch (e) {
            setError(String(e instanceof Error ? e.message : e))
            throw e
          }
        }}
      >
        <Button type="submit" disabled={!contract}>
          {l.save}
        </Button>
        <Button type="button" variant="outline" onClick={onCancel}>
          {l.cancel}
        </Button>
      </AutoForm>
    </section>
  )
}
function AssignmentForm({ base }: { base: string }) {
  const l = usePlanningLabels()
  const [work, setWork] = useState("")
  const [loaded, setLoaded] = useState("")
  const [row, setRow] = useState<Assignment | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [key, setKey] = useState(() => crypto.randomUUID())
  const fields = ["iteration_id", "version_id", "milestone_id"] as const
  async function load() {
    setBusy(true)
    setError("")
    setRow(null)
    try {
      setRow(
        await read<Assignment>(
          `${base}/work-items/${encodeURIComponent(work)}/planning`
        )
      )
      setLoaded(work)
      setKey(crypto.randomUUID())
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="space-y-3 border-t pt-4">
      <h2 className="font-medium">{l.assignment}</h2>
      {error && <Alert>{error}</Alert>}
      <label className="flex flex-col gap-1">
        {l.work}
        <input
          className={css}
          value={work}
          inputMode="numeric"
          onChange={(e) => {
            setWork(e.target.value)
            setRow(null)
          }}
        />
      </label>
      <Button
        disabled={busy || !/^[1-9][0-9]*$/.test(work)}
        onClick={() => void load()}
      >
        {busy ? l.loading : l.load}
      </Button>
      {row && (
        <form
          className="space-y-3"
          onSubmit={async (e) => {
            e.preventDefault()
            setBusy(true)
            setError("")
            try {
              const next = await write<Assignment>(
                `${base}/work-items/${loaded}/planning`,
                "PUT",
                {
                  iteration_id: row.iteration_id || null,
                  version_id: row.version_id || null,
                  milestone_id: row.milestone_id || null,
                },
                key,
                row.version
              )
              setRow(next)
              setKey(crypto.randomUUID())
            } catch (e) {
              setError(String(e instanceof Error ? e.message : e))
            } finally {
              setBusy(false)
            }
          }}
        >
          {fields.map((field, i) => (
            <label key={field} className="flex flex-col gap-1">
              {[l.iterations, l.versions, l.milestones][i]}
              <input
                className={css}
                value={row[field] ?? ""}
                inputMode="numeric"
                placeholder={l.backlog}
                onChange={(e) => {
                  setRow({ ...row, [field]: e.target.value || null })
                  setKey(crypto.randomUUID())
                }}
              />
            </label>
          ))}
          <Button type="submit" disabled={busy}>
            {l.save}
          </Button>
        </form>
      )}
    </section>
  )
}
