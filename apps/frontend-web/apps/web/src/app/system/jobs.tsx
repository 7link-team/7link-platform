import { useEffect, useRef, useState } from "react"
import { Link, useSearchParams } from "react-router"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable, TableFilterBar } from "@workspace/ui/components/auto-table"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import { Checkbox } from "@workspace/ui/components/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { ActivityLabel } from "../../components/activity-label"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import {
  jobCanCancel,
  jobCanRetry,
  jobFailures,
  platformJobsApi,
  type JobFilters,
  type JobSummary,
  type PlatformJob,
  type PlatformSchedule,
  type CorrelatedOperation,
} from "../../lib/platform-jobs-api"

function JobFanoutContext({ job }: { job: PlatformJob }) {
  const t = useTranslations("iam")
  if (!job.fanout_kind) return null
  return (
    <dl className="mt-3 space-y-2 text-sm whitespace-normal">
      <div>
        <dt className="text-muted-foreground">{t("jobsFanoutSchedule")}</dt>
        <dd className="break-all">{job.fanout_schedule_name || "—"}</dd>
      </div>
      <div>
        <dt className="text-muted-foreground">{t("jobsFanoutTarget")}</dt>
        <dd>
          <ActivityLabel
            code={job.fanout_kind}
            display_name={job.fanout_display_name}
            description={job.fanout_description}
          />
        </dd>
      </div>
      <div>
        <dt className="text-muted-foreground">{t("jobsFanoutFireAt")}</dt>
        <dd className="break-all">{job.fanout_fire_at || "—"}</dd>
      </div>
    </dl>
  )
}

export default function PlatformJobsPage() {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [params, setParams] = useSearchParams()
  const [rows, setRows] = useState<PlatformJob[]>([])
  const [summary, setSummary] = useState<JobSummary | null>(null)
  const [automatic, setAutomatic] = useState(true)
  const [offset, setOffset] = useState(0)
  const [more, setMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [refresh, setRefresh] = useState(0)
  const [selected, setSelected] = useState<PlatformJob | null>(null)
  const [operations, setOperations] = useState<{
    job: string
    rows: CorrelatedOperation[]
    offset: number
  } | null>(null)
  const [action, setAction] = useState<"retry" | "cancel" | null>(null)
  const [busy, setBusy] = useState(false)
  const [batchIDs, setBatchIDs] = useState<string[]>([])
  const [batchOpen, setBatchOpen] = useState(false)
  const [batchResult, setBatchResult] = useState("")
  const query = params.toString()
  const requestedJob = params.get("job_id")
  const canView = allowed("system_job_view")
  const errorLabel = (code: string) =>
    [
      "PANIC",
      "LEASE_EXPIRED",
      "INTERRUPTED",
      "PERMANENT",
      "TIMEOUT",
      "HANDLER_ERROR",
    ].includes(code)
      ? `${code}: ${t(`jobsError${code}`)}`
      : code
  useEffect(() => {
    let current = true
    if (!canView || !requestedJob) return
    void platformJobsApi.get(requestedJob).then(
      (job) => {
        if (current) setSelected(job)
      },
      (cause: Error) => {
        if (current) setError(cause.message)
      }
    )
    return () => {
      current = false
    }
  }, [canView, requestedJob])
  useEffect(() => {
    if (!canView || !automatic) return
    const timer = window.setInterval(() => setRefresh((n) => n + 1), 10_000)
    return () => window.clearInterval(timer)
  }, [canView, automatic])
  useEffect(() => {
    let current = true
    if (!canView) return
    const filters = new URLSearchParams(query)
    filters.delete("job_id")
    void platformJobsApi
      .list(Object.fromEntries(filters) as JobFilters, offset)
      .then(
        (page) => {
          if (!current) return
          setRows(page.items ?? [])
          setSummary(page.summary)
          setMore(page.has_more)
          setLoading(false)
          setError("")
        },
        (cause) => {
          if (current) {
            setRows([])
            setError(String(cause.message))
            setLoading(false)
          }
        }
      )
    return () => {
      current = false
    }
  }, [query, offset, refresh, canView])
  const details = async (row: PlatformJob) => {
    setBusy(true)
    setError("")
    try {
      setSelected(await platformJobsApi.get(row.id))
      setAction(null)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  if (!canView) return <Alert>{t("jobsNoAccess")}</Alert>
  return (
    <section className="space-y-4">
      <PageHeader
        title={t("jobsTitle")}
        description={t("jobsDescription")}
        action={
          <Button
            variant="outline"
            disabled={loading}
            onClick={() => {
              setLoading(true)
              setRefresh((n) => n + 1)
            }}
          >
            {t("refresh")}
          </Button>
        }
      />
      {error && <Alert>{error}</Alert>}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4" aria-live="off">
        {(
          [
            ["running", "jobsRunning"],
            ["pending", "jobsPending"],
            ["dead_24h", "jobsDead24h"],
            ["oldest_pending_age_seconds", "jobsOldestPending"],
          ] as const
        ).map(([field, label]) => (
          <div key={field} className="rounded-md border p-4">
            <p className="text-sm text-muted-foreground">{t(label)}</p>
            <p className="mt-1 text-2xl tabular-nums">
              {summary ? summary[field].toLocaleString() : "—"}
            </p>
          </div>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <Button
          variant="outline"
          aria-pressed={automatic}
          onClick={() => setAutomatic((value) => !value)}
        >
          {t(automatic ? "jobsPauseRefresh" : "jobsResumeRefresh")}
        </Button>
        {allowed("system_job_manage") && (
          <Button
            className="hidden md:inline-flex"
            disabled={busy || !batchIDs.length}
            aria-describedby="jobs-retry-selection-help"
            onClick={() => {
              setBatchResult("")
              setBatchOpen(true)
            }}
          >
            {t("jobsBatchRetry")} ({batchIDs.length}/100)
          </Button>
        )}
      </div>
      {allowed("system_job_manage") && (
        <p
          id="jobs-retry-selection-help"
          className="hidden text-sm text-muted-foreground md:block"
        >
          {t("jobsRetrySelectionHelp")}
        </p>
      )}
      <TableFilterBar
        key={query}
        defaultValue={Object.fromEntries(params)}
        onSearch={(values) => {
          setLoading(true)
          setOffset(0)
          setRefresh((value) => value + 1)
          setParams(
            Object.fromEntries(
              Object.entries(values)
                .filter(([, v]) => v)
                .map(([k, v]) => [k, String(v)])
            )
          )
        }}
        filters={[
          {
            field: "state",
            name: t("jobsState"),
            type: "select",
            conditions: [],
            options: [
              "",
              "PENDING",
              "RUNNING",
              "SUCCEEDED",
              "DEAD",
              "CANCELLED",
            ].map((value) => ({
              value,
              label: value ? t(`jobsState${value}`) : t("all"),
            })),
          },
          {
            field: "queue",
            maxLength: 64,
            name: t("jobsQueue"),
            type: "string",
            conditions: [],
          },
          {
            field: "kind",
            maxLength: 128,
            name: t("jobsKind"),
            type: "string",
            conditions: [],
          },
          {
            field: "tenant_id",
            maxLength: 24,
            name: t("jobsTenantFilter"),
            type: "string",
            conditions: ["eq"],
          },
          {
            field: "created_at",
            name: t("jobsCreatedFilter"),
            type: "datetime",
            conditions: ["gte", "lte", "gt", "lt"],
          },
        ]}
        onReset={() => {
          setParams({})
          setOffset(0)
          setLoading(true)
          setRefresh((value) => value + 1)
        }}
      />
      <div className="space-y-3 md:hidden">
        {!rows.length && (
          <p className="text-sm text-muted-foreground">
            {loading ? t("loading") : t("jobsEmpty")}
          </p>
        )}
        {rows.map((row) => (
          <button
            key={row.id}
            type="button"
            disabled={busy}
            className="w-full rounded-md border p-4 text-left"
            onClick={() => void details(row)}
          >
            <div className="flex items-center justify-between gap-2">
              <ActivityLabel {...row} code={row.kind} />
              <Badge
                variant={row.state === "DEAD" ? "destructive" : "secondary"}
              >
                {t(`jobsState${row.state}`)}
              </Badge>
            </div>
            <JobFanoutContext job={row} />
            <dl className="mt-3 grid grid-cols-2 gap-2 text-sm">
              <dt>{t("id")}</dt>
              <dd>{row.id}</dd>
              <dt>{t("jobsTenant")}</dt>
              <dd>{row.tenant_id || t("activityPlatform")}</dd>
              <dt>{t("jobsAttempts")}</dt>
              <dd>
                {row.attempts}/{row.max_attempts}
              </dd>
              <dt>{t("jobsRunAt")}</dt>
              <dd className="break-all">{row.run_at}</dd>
              <dt>{t("jobsLastError")}</dt>
              <dd className="break-all">{errorLabel(row.last_error_code)}</dd>
            </dl>
            {!row.has_handler && (
              <p className="mt-2 text-sm text-muted-foreground">
                {t("jobsMissingHandler")}
              </p>
            )}
          </button>
        ))}
      </div>
      <AutoTable
        className="hidden md:block"
        data={rows}
        loading={loading}
        rowKey="id"
        pagination={false}
        emptyContent={
          <div className="space-y-2">
            <p>{t("jobsEmpty")}</p>
            <Button
              variant="outline"
              onClick={() => {
                setParams({})
                setOffset(0)
                setLoading(true)
                setRefresh((n) => n + 1)
              }}
            >
              {t("jobsClearFilters")}
            </Button>
          </div>
        }
        columns={[
          ...(allowed("system_job_manage")
            ? [
                {
                  field: "selection",
                  name: t("jobsSelectRetry"),
                  render: (_: unknown, row: PlatformJob) => (
                    <Checkbox
                      className="hidden md:inline-flex"
                      aria-label={`${t("jobsSelectRetry")} ${row.id}`}
                      aria-describedby="jobs-retry-selection-help"
                      disabled={busy || !jobCanRetry(row)}
                      checked={!!row.id && batchIDs.includes(row.id)}
                      onCheckedChange={(checked) => {
                        if (!row.id) return
                        const id = row.id
                        setBatchIDs((ids) =>
                          checked
                            ? [...new Set([...ids, id])].slice(0, 100)
                            : ids.filter((value) => value !== id)
                        )
                      }}
                    />
                  ),
                },
              ]
            : []),
          {
            field: "id",
            name: t("id"),
            render: (_, row) => (
              <Button
                variant="link"
                disabled={busy}
                onClick={() => void details(row)}
              >
                {row.id}
              </Button>
            ),
          },
          {
            field: "kind",
            name: t("jobsKind"),
            render: (_, row) => (
              <>
                <ActivityLabel {...row} code={row.kind} />
                <JobFanoutContext job={row} />
              </>
            ),
          },
          {
            field: "tenant_id",
            name: t("jobsTenant"),
            render: (_, row) => row.tenant_id || t("activityPlatform"),
          },
          {
            field: "state",
            name: t("jobsState"),
            render: (_, row) => (
              <Badge
                variant={row.state === "DEAD" ? "destructive" : "secondary"}
              >
                {t(`jobsState${row.state}`)}
              </Badge>
            ),
          },
          { field: "priority", name: t("jobsPriority") },
          {
            field: "attempts",
            name: t("jobsAttempts"),
            render: (_, row) => `${row.attempts}/${row.max_attempts}`,
          },
          { field: "run_at", name: t("jobsRunAt") },
          {
            field: "last_error_code",
            name: t("jobsLastError"),
            render: (_, row) => errorLabel(row.last_error_code),
          },
          {
            field: "actions",
            name: t("actions"),
            type: "actions",
            render: (_, row) => (
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => void details(row)}
              >
                {t("details")}
              </Button>
            ),
          },
        ]}
      />
      {batchResult && (
        <p role="status" className="text-sm">
          {batchResult}
        </p>
      )}
      <Dialog
        open={batchOpen}
        onOpenChange={(open) => {
          if (!busy) setBatchOpen(open)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("jobsBatchRetry")}</DialogTitle>
            <DialogDescription>{t("jobsBatchDescription")}</DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <AutoForm
            fields={{
              reason: {
                name: t("jobsReason"),
                type: "textarea",
                required: true,
                config: { maxLength: 2000 },
              },
            }}
            onSubmit={async (values) => {
              if (busy || !allowed("system_job_manage") || !batchIDs.length)
                return
              setBusy(true)
              setError("")
              try {
                const result = await platformJobsApi.retryBatch(
                  batchIDs,
                  String(values.reason)
                )
                setBatchResult(
                  `${t("jobsBatchRetried")}: ${result.retried?.length ?? 0}; ${t("jobsBatchSkipped")}: ${(result.skipped ?? []).map((item) => `${item.id} (${item.code})`).join(", ") || "0"}`
                )
                setBatchIDs([])
                setBatchOpen(false)
                setRefresh((n) => n + 1)
              } catch (cause) {
                setError((cause as Error).message)
              } finally {
                setBusy(false)
              }
            }}
          >
            <Button type="submit" disabled={busy}>
              {t("confirm")}
            </Button>
          </AutoForm>
        </DialogContent>
      </Dialog>
      <div className="flex justify-end gap-2">
        <Button
          variant="outline"
          disabled={offset === 0 || loading}
          onClick={() => {
            setLoading(true)
            setOffset((n) => Math.max(0, n - 50))
          }}
        >
          {t("previous")}
        </Button>
        <Button
          variant="outline"
          disabled={!more || loading}
          onClick={() => {
            setLoading(true)
            setOffset((n) => n + 50)
          }}
        >
          {t("next")}
        </Button>
      </div>
      <Dialog
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open && !busy) {
            setSelected(null)
            setAction(null)
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("jobsDetail")}</DialogTitle>
            <DialogDescription>
              {selected?.id} · {t("jobsPayloadRedacted")}
            </DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          {selected && (
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => void details(selected)}
            >
              {t("refresh")}
            </Button>
          )}
          {selected && (
            <>
              <ActivityLabel {...selected} code={selected.kind} />
              <JobFanoutContext job={selected} />
              <dl className="grid grid-cols-2 gap-2 text-sm">
                <dt>{t("jobsTenant")}</dt>
                <dd>{selected.tenant_id || t("activityPlatform")}</dd>
                <dt>{t("jobsState")}</dt>
                <dd>{t(`jobsState${selected.state}`)}</dd>
                <dt>{t("jobsAttempts")}</dt>
                <dd>
                  {selected.attempts}/{selected.max_attempts}
                </dd>
              </dl>
              {selected.correlation_id &&
                allowed("system_audit_event_view") && (
                  <Button
                    variant="outline"
                    disabled={busy}
                    onClick={async () => {
                      setBusy(true)
                      setError("")
                      try {
                        const rows = await platformJobsApi.operations(
                          selected.correlation_id!
                        )
                        setOperations({ job: selected.id!, rows, offset: 0 })
                      } catch (cause) {
                        setError((cause as Error).message)
                      } finally {
                        setBusy(false)
                      }
                    }}
                  >
                    {t("jobsCorrelatedOperations")}
                  </Button>
                )}
              {selected.id && operations?.job === selected.id && (
                <section aria-label={t("jobsCorrelatedOperations")}>
                  <pre className="max-h-60 overflow-auto rounded-md bg-muted p-3 text-xs break-all whitespace-pre-wrap">
                    {JSON.stringify(operations.rows, null, 2)}
                  </pre>
                  <div className="flex gap-2">
                    {[-50, 50].map((delta) => (
                      <Button
                        key={delta}
                        variant="outline"
                        disabled={
                          busy ||
                          (delta < 0
                            ? operations.offset === 0
                            : operations.rows.length < 50)
                        }
                        onClick={async () => {
                          setBusy(true)
                          setError("")
                          try {
                            const offset = operations.offset + delta
                            const rows = await platformJobsApi.operations(
                              selected.correlation_id!,
                              offset
                            )
                            setOperations({ job: selected.id!, rows, offset })
                          } catch (cause) {
                            setError((cause as Error).message)
                          } finally {
                            setBusy(false)
                          }
                        }}
                      >
                        {t(delta < 0 ? "previousPage" : "nextPage")}
                      </Button>
                    ))}
                  </div>
                </section>
              )}
              {!selected.has_handler && (
                <Alert>{t("jobsMissingHandler")}</Alert>
              )}
              {jobFailures(selected.error_history).length > 0 && (
                <ol
                  aria-label={t("jobsLastError")}
                  className="max-h-48 space-y-3 overflow-auto border-l pl-4 text-sm"
                >
                  {jobFailures(selected.error_history).map((failure, index) => (
                    <li key={`${failure.at}:${failure.attempt}:${index}`}>
                      <time dateTime={failure.at}>
                        {new Date(failure.at).toLocaleString()}
                      </time>
                      <p className="break-words">
                        {t("jobsAttempts")} {failure.attempt}:{" "}
                        {errorLabel(failure.code)}
                      </p>
                      <p className="break-words text-muted-foreground">
                        {failure.message}
                      </p>
                    </li>
                  ))}
                </ol>
              )}
              <details
                open={
                  new TextEncoder().encode(JSON.stringify(selected))
                    .byteLength <=
                  16 * 1024
                }
              >
                <summary className="cursor-pointer text-sm">
                  {t("jobsPayloadAndHistory")}
                </summary>
                <pre className="mt-2 max-h-60 overflow-auto rounded-md bg-muted p-3 text-xs break-all whitespace-pre-wrap">
                  {JSON.stringify(selected, null, 2)}
                </pre>
              </details>
              {allowed("system_job_manage") && (
                <div className="flex gap-2">
                  <Button
                    disabled={busy || !jobCanRetry(selected)}
                    onClick={() => setAction("retry")}
                  >
                    {t("jobsRetry")}
                  </Button>
                  <Button
                    variant="destructive"
                    disabled={busy || !jobCanCancel(selected)}
                    onClick={() => setAction("cancel")}
                  >
                    {t("jobsCancel")}
                  </Button>
                </div>
              )}
              {action && (
                <AutoForm
                  key={`${selected.id}:${action}`}
                  fields={{
                    reason: {
                      name: t("jobsReason"),
                      type: "textarea",
                      required: true,
                      config: { maxLength: 2000 },
                    },
                  }}
                  onSubmit={async (values) => {
                    if (
                      busy ||
                      !allowed("system_job_manage") ||
                      (action === "retry"
                        ? !jobCanRetry(selected)
                        : !jobCanCancel(selected))
                    )
                      return
                    setBusy(true)
                    setError("")
                    try {
                      const result = await platformJobsApi[action](
                        selected,
                        String(values.reason)
                      )
                      setSelected(result)
                      setAction(null)
                      setRefresh((n) => n + 1)
                    } catch (cause) {
                      setError((cause as Error).message)
                    } finally {
                      setBusy(false)
                    }
                  }}
                >
                  <Button type="submit" disabled={busy}>
                    {t("confirm")}
                  </Button>
                </AutoForm>
              )}
            </>
          )}
        </DialogContent>
      </Dialog>
    </section>
  )
}

export function PlatformSchedulesPage() {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [rows, setRows] = useState<PlatformSchedule[]>([])
  const [refresh, setRefresh] = useState(0)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [selected, setSelected] = useState<{
    row: PlatformSchedule
    action: "enable" | "run"
  } | null>(null)
  const keys = useRef(new Map<string | null, string>())
  const canView = allowed("system_job_view")
  useEffect(() => {
    let current = true
    if (!canView) return
    void platformJobsApi.schedules().then(
      (data) => {
        if (current) {
          setRows(data)
          setLoading(false)
          setError("")
        }
      },
      (cause) => {
        if (current) {
          setError(cause.message)
          setLoading(false)
        }
      }
    )
    return () => {
      current = false
    }
  }, [refresh, canView])
  if (!canView) return <Alert>{t("jobsNoAccess")}</Alert>
  return (
    <section className="space-y-4">
      <PageHeader
        title={t("jobsSchedules")}
        description={t("jobsScheduleDescription")}
        action={
          <Button
            variant="outline"
            disabled={loading}
            onClick={() => {
              setLoading(true)
              setRefresh((n) => n + 1)
            }}
          >
            {t("refresh")}
          </Button>
        }
      />
      {error && <Alert>{error}</Alert>}
      <AutoTable
        data={rows}
        loading={loading}
        rowKey="id"
        pagination={false}
        emptyContent={t("jobsEmpty")}
        columns={[
          {
            field: "name",
            name: t("name"),
            render: (_, row) => <ActivityLabel {...row} code={row.name} />,
          },
          { field: "kind", name: t("jobsKind") },
          { field: "queue", name: t("jobsQueue") },
          {
            field: "enabled",
            name: t("jobsState"),
            render: (_, row) =>
              row.orphaned
                ? t("jobsOrphaned")
                : row.enabled
                  ? t("enabled")
                  : t("disabled"),
          },
          {
            field: "cron",
            name: t("jobsScheduleRule"),
            render: (_, row) =>
              `${row.cron || (row.interval_ms ? t("activityEverySeconds", { seconds: row.interval_ms / 1000 }) : "—")} · ${row.timezone}`,
          },
          {
            field: "tenant_scope",
            name: t("activityScope"),
            render: (_, row) =>
              t(
                row.tenant_scope === "PER_TENANT"
                  ? "activityEachTenant"
                  : "activityPlatform"
              ),
          },
          { field: "next_run_at", name: t("jobsNextRun") },
          {
            field: "last_job_id",
            name: t("jobsLastJob"),
            render: (_, row) =>
              row.last_job_id ? (
                <Link
                  className="underline"
                  to={`/system/jobs?job_id=${encodeURIComponent(row.last_job_id)}`}
                >
                  {row.last_job_id}
                </Link>
              ) : (
                "—"
              ),
          },
          {
            field: "id",
            name: t("actions"),
            render: (_, row) =>
              allowed("system_job_manage") && (
                <div className="flex gap-2">
                  <Button
                    variant="outline"
                    disabled={busy || row.orphaned}
                    onClick={() => setSelected({ row, action: "enable" })}
                  >
                    {row.enabled ? t("disable") : t("enable")}
                  </Button>
                  <Button
                    disabled={busy || row.orphaned}
                    onClick={() => setSelected({ row, action: "run" })}
                  >
                    {t("jobsRunNow")}
                  </Button>
                </div>
              ),
          },
        ]}
      />
      <Dialog
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setSelected(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {selected?.action === "run"
                ? t("jobsRunNow")
                : t("jobsChangeSchedule")}
            </DialogTitle>
            <DialogDescription>
              {selected?.row.name} · {t("jobsScheduleConfirm")}
            </DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <Button
            disabled={busy}
            onClick={async () => {
              if (
                !selected ||
                busy ||
                !allowed("system_job_manage") ||
                selected.row.orphaned
              )
                return
              setBusy(true)
              setError("")
              try {
                if (selected.action === "enable")
                  await platformJobsApi.enable(
                    selected.row,
                    !selected.row.enabled
                  )
                else {
                  let key = keys.current.get(selected.row.id)
                  if (!key) {
                    key = crypto.randomUUID()
                    keys.current.set(selected.row.id, key)
                  }
                  await platformJobsApi.run(selected.row, key)
                  keys.current.delete(selected.row.id)
                }
                setSelected(null)
                setRefresh((n) => n + 1)
              } catch (cause) {
                setError((cause as Error).message)
              } finally {
                setBusy(false)
              }
            }}
          >
            {t("confirm")}
          </Button>
        </DialogContent>
      </Dialog>
    </section>
  )
}
