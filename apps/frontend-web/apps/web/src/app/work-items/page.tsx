import { WorkWorkflowTransition } from "./workflow-transition"
import { WorkCalendar, WorkSchedule } from "./schedule"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useParams, useSearchParams } from "react-router"
import { Eye, Plus, Trash2 } from "lucide-react"
import {
  AutoForm,
  type FieldRendererProps,
} from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Textarea } from "@workspace/ui/components/textarea"
import { PaginationPages } from "@workspace/ui/components/pagination"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAuth } from "../../components/auth"
import { apiRoute } from "../../lib/api-route"
import {
  iamApi,
  type WorkItem,
  type WorkItemDetail,
  type WorkItemInput,
  type Requirement,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { useRequestContract } from "../../lib/use-request-contract"
import { WorkPreparation } from "./preparation"
import { WorkAssignmentControls } from "./assignment"
import { WorkDependencies } from "./dependencies"
import { WorkCodeLinks } from "./code-links"
import { WorkCommentSource } from "./comment-source"
import { WorkDefectReport, DefectCreateFields } from "./defect"
import { WorkEffort } from "./effort"
import { defectMessages } from "./defect-data"
import { WorkResults } from "./results"
import { WorkRecovery } from "./recovery"
import { WorkAndon } from "./andon"
import { WorkBoard } from "./board"

const workTypes = [
  "TASK",
  "FEATURE",
  "BUG",
  "CHORE",
  "DOC",
  "RESEARCH",
  "DESIGN",
  "TEST",
  "RISK",
  "INCIDENT",
  "CONTENT",
] as const
const isDefect = (values?: Record<string, unknown>) =>
  values?.type === "BUG" || values?.type === "INCIDENT"

const workStates = [
  "DRAFT",
  "READY",
  "BLOCKED",
  "CLAIMED",
  "IN_PROGRESS",
  "AGENT_CLAIMED_DONE",
  "VERIFYING",
  "NEEDS_HUMAN_REVIEW",
  "ACCEPTED",
  "REWORK",
  "EXECUTION_FAILED",
  "PLAN_REVIEW",
  "INTERNAL_QA",
  "STOPPED",
  "CANCELLED",
] as const

type Clause = { text: string; verification_method: string }

// Reuse AutoForm's custom renderer for a structured array; criteria stay data,
// with separate verification methods, rather than newline-delimited fake IDs.
function CriteriaField({
  value,
  onChange,
  disabled,
  readonly,
  labelId,
  describedBy,
  error,
  errorId,
}: FieldRendererProps) {
  const t = useTranslations("iam")
  const rows = (Array.isArray(value) ? value : []) as Clause[]
  const update = (index: number, field: keyof Clause, text: string) =>
    onChange(
      rows.map((row, i) => (i === index ? { ...row, [field]: text } : row))
    )
  return (
    <div
      className="space-y-3"
      role="group"
      aria-labelledby={labelId}
      aria-describedby={describedBy}
    >
      {rows.map((row, i) => (
        <fieldset
          key={i}
          className="space-y-2 rounded-md border p-3"
          disabled={disabled}
        >
          <legend className="px-1 text-sm">
            {t("workClauseNumber", { number: i + 1 })}
          </legend>
          <label className="flex flex-col gap-1 text-sm">
            {t("workCriterionText")}
            <Textarea
              value={row.text}
              readOnly={readonly}
              aria-invalid={Boolean(error)}
              aria-describedby={describedBy}
              required={!readonly}
              onChange={(e) => update(i, "text", e.target.value)}
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            {t("workVerificationMethod")}
            <Textarea
              value={row.verification_method}
              readOnly={readonly}
              aria-invalid={Boolean(error)}
              aria-describedby={describedBy}
              required={!readonly}
              onChange={(e) => update(i, "verification_method", e.target.value)}
            />
          </label>
          {!readonly && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={disabled || rows.length <= 1}
              onClick={() => onChange(rows.filter((_, index) => index !== i))}
            >
              <Trash2 />
              {t("workRemoveClause")}
            </Button>
          )}
        </fieldset>
      ))}
      {error && (
        <p id={errorId} role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      {!readonly && (
        <Button
          type="button"
          variant="outline"
          disabled={disabled || rows.length >= 50}
          onClick={() =>
            onChange([...rows, { text: "", verification_method: "" }])
          }
        >
          <Plus />
          {t("workAddClause")}
        </Button>
      )}
    </div>
  )
}

export default function WorkItemsPage() {
  const { tenantID = "", entityID = "" } = useParams()
  return <WorkItems key={`${tenantID}:${entityID}`} entityID={entityID} />
}

function WorkItems({ entityID }: { entityID: string }) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const allowed = useAllowed()
  const [search, setSearch] = useSearchParams()
  const [rows, setRows] = useState<WorkItem[]>([])
 const [boardRevision,setBoardRevision]=useState(0)
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [creating, setCreating] = useState(false)
  const [detail, setDetail] = useState<WorkItemDetail | null>(null)
  const [reading, setReading] = useState(false)
  const pending = useRef<AbortController | null>(null)
  const detailPending = useRef<AbortController | null>(null)
  const rawOffset = Number(search.get("offset") ?? 0)
  const offset =
    Number.isSafeInteger(rawOffset) && rawOffset >= 0 ? rawOffset : 0
  const rawLimit = Number(search.get("limit") ?? 20)
  const limit = [10, 20, 50, 100].includes(rawLimit) ? rawLimit : 20
  const title = search.get("title") ?? ""
  const type = search.get("type") ?? "all"
  const sort = search.get("sort") ?? "-number"
  const view = search.get("view") === "board" ? "board" : search.get("view") === "calendar" ? "calendar" : "list"
  const state = search.get("state") ?? "all"
  const severity = search.get("severity") ?? "all"
  const accountable = search.get("accountable_principal_id") ?? ""
  const createdBy = search.get("created_by") ?? ""
  const workCandidate = search.get("work") ?? ""
  const selectedWork = /^[1-9][0-9]{0,18}$/.test(workCandidate) ? workCandidate : ""
  const filters = useMemo(
    () => ({ title, type, sort, state, severity, accountable_principal_id: accountable, created_by: createdBy }),
    [title, type, sort, state, severity, accountable, createdBy]
  )
  const boardQuery = useMemo(() => ({
    sort, title: title ? `like:${title}` : undefined, type: type !== "all" ? `eq:${type}` : undefined,
    severity: severity !== "all" ? `eq:${severity}` : undefined,
    accountable_principal_id: accountable ? `eq:${accountable}` : undefined,
    created_by: createdBy ? `eq:${createdBy}` : undefined,
  }), [sort,title,type,severity,accountable,createdBy])
  const load = useCallback(async () => {
    pending.current?.abort()
    if (view !== "list") { setLoading(false); setError(""); setBoardRevision(v=>v+1); return }
    const controller = new AbortController()
    pending.current = controller
    setLoading(true)
    setError("")
    try {
      const query = {
          offset,
          limit,
          sort,
          title: title ? `like:${title}` : undefined,
          type: type !== "all" ? `eq:${type}` : undefined,
          state: state !== "all" ? `eq:${state}` : undefined,
          severity: severity !== "all" ? `eq:${severity}` : undefined,
          accountable_principal_id: accountable ? `eq:${accountable}` : undefined,
          created_by: createdBy ? `eq:${createdBy}` : undefined,
      }
      const page = await iamApi.workItems(
        entityID,
        query,
        controller.signal
      )
      if (controller.signal.aborted) return
      if (offset > 0 && offset >= page.total) {
        setSearch(
          (previous) => {
            const next = new URLSearchParams(previous)
            next.set(
              "offset",
              String(Math.max(0, Math.ceil(page.total / limit) - 1) * limit)
            )
            return next
          },
          { replace: true }
        )
        return
      }
      setRows(page.items)
      setTotal(page.total)
    } catch (cause) {
      if (!controller.signal.aborted) {
        setRows([])
        setTotal(0)
        setError(cause instanceof Error ? cause.message : t("workUnavailable"))
      }
    } finally {
      if (!controller.signal.aborted) setLoading(false)
    }
  }, [entityID, offset, limit, title, type, sort, state, severity, accountable, createdBy, setSearch, t, view])
  useEffect(() => {
    let active = true
    queueMicrotask(() => {
      if (active) void load()
    })
    return () => {
      active = false
      pending.current?.abort()
    }
  }, [load])
  useEffect(() => () => detailPending.current?.abort(), [])
  const open = (row: WorkItem) => {
    if (!row.id) return
    setSearch(previous => { const next = new URLSearchParams(previous); next.set("work", row.id!); return next })
  }
  useEffect(() => {
    if (!selectedWork) return
    const controller = new AbortController()
    detailPending.current = controller
    void Promise.resolve().then(async () => {
      if (controller.signal.aborted) return
      setError(""); setReading(true)
      try {
        const result = await iamApi.workItem(entityID, selectedWork, controller.signal)
        if (!controller.signal.aborted) setDetail(result)
      } catch (cause) {
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("workUnavailable"))
      } finally { if (!controller.signal.aborted) setReading(false) }
    })
    return () => controller.abort()
  }, [entityID, selectedWork, t])
  const filterForm = (
    <AutoForm
      key={JSON.stringify(filters)}
      mode="edit"
      initialData={filters}
      className="shrink-0 md:grid-cols-4"
      fields={{
        title: {
          name: t("workTitle"),
          type: "text",
          config: { type: "search" },
        },
        type: {
          name: t("workType"),
          type: "select",
          config: {
            options: [
              { value: "all", label: t("all") },
              ...workTypes.map((value) => ({
                value,
                label: t(`workType_${value}`),
              })),
            ],
          },
        },
        state: {
          name: t("status"),
          type: "select",
          config: {
            options: [
              { value: "all", label: t("all") },
              ...workStates.map((value) => ({
                value,
                label: t(`workState_${value}`),
              })),
            ],
          },
        },
        severity: {
          name: t("workSeverity"),
          type: "select",
          config: { options: [{ value: "all", label: t("all") }, ...["FATAL", "CRITICAL", "NORMAL", "MINOR"].map(value => ({ value, label: value }))] },
        },
        accountable_principal_id: {
          name: t("workFilterAccountable"),
          type: "text",
        },
        created_by: {
          name: t("workFilterSubmitter"),
          type: "text",
        },
        sort: {
          name: t("projectSort"),
          type: "select",
          config: {
            options: [
              { value: "-number", label: t("projectNewest") },
              { value: "number", label: t("workNumber") },
              { value: "title", label: t("workTitle") },
              { value: "-updated_at", label: t("updatedAt") },
            ],
          },
        },
      }}
      onSubmit={(values) => {
        const next = new URLSearchParams()
        next.set("limit", String(limit))
        next.set("view", view)
        for (const key of ["title", "type", "sort", "state", "severity", "accountable_principal_id", "created_by"]) {
          const value = String(values[key] ?? "").trim()
          if (value) next.set(key, value)
        }
        setSearch(next)
      }}
    >
      <Button type="submit" size="sm">
        {t("search")}
      </Button>
    </AutoForm>
  )
  const paginationFooter = (
    <div className="flex w-full flex-wrap items-center justify-between gap-3">
      <span className="text-sm text-muted-foreground">
        {t("projectPageSummary", {
          start: rows.length && !loading ? offset + 1 : 0,
          end: rows.length && !loading ? offset + rows.length : 0,
          total,
        })}
      </span>
      <label className="flex items-center gap-2 text-sm">
        {t("projectPageSize")}
        <select
          className="rounded-md border bg-background p-1"
          value={limit}
          disabled={loading}
          onChange={(e) =>
            setSearch((previous) => {
              const next = new URLSearchParams(previous)
              next.set("limit", e.target.value)
              next.delete("offset")
              return next
            })
          }
        >
          {[10, 20, 50, 100].map((size) => (
            <option key={size} value={size}>
              {size}
            </option>
          ))}
        </select>
      </label>
      <PaginationPages
        page={Math.floor(offset / limit) + 1}
        totalPages={Math.max(1, Math.ceil(total / limit))}
        disabled={loading}
        onPageChange={(page) =>
          setSearch((previous) => {
            const next = new URLSearchParams(previous)
            next.set("offset", String((page - 1) * limit))
            return next
          })
        }
      />
    </div>
  )
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      <PageHeader
        title={t("workItems")}
        action={
          allowed("work_item_write") ? (
            <Button onClick={() => setCreating(true)}>
              <Plus />
              {t("workCreate")}
            </Button>
          ) : undefined
        }
      />
      {error && <Alert>{error}</Alert>}
      {reading && selectedWork && (
        <p role="status" className="text-sm">
          {t("loading")}
        </p>
      )}
      <div className="flex gap-2" role="group" aria-label={t("workView")}>
        {(["list", "board", "calendar"] as const).map((value) => (
          <Button
            key={value}
            size="sm"
            variant={view === value ? "default" : "outline"}
            aria-pressed={view === value}
            onClick={() =>
              setSearch((previous) => {
                const next = new URLSearchParams(previous)
                next.set("view", value)
                return next
              })
            }
          >
            {t(`workView_${value}`)}
          </Button>
        ))}
      </div>
      {filterForm}
      {view === "calendar" ? <WorkCalendar entityID={entityID} onOpen={open} revision={boardRevision} /> : view === "board" ? (
        <WorkBoard project={entityID} states={state === "all" ? workStates : workStates.filter(s=>s===state)} query={boardQuery} revision={boardRevision} onOpen={open} onChanged={()=>{void load()}} />
      ) : (
        <AutoTable
          fill
          data={loading ? [] : rows}
          loading={loading}
          rowKey="id"
          rowOffset={offset}
          pagination={false}
          onRefresh={load}
          exportable={!error}
          emptyContent={t("workEmpty")}
          columns={[
            { field: "number", name: t("workNumber"), width: "110px" },
            { field: "title", name: t("workTitle"), width: "280px" },
            {
              field: "type",
              name: t("workType"),
              width: "140px",
              format: (_, row) => t(`workType_${row.type}`),
            },
            {
              field: "state",
              name: t("status"),
              width: "170px",
              format: (_, row) => t(`workState_${row.state}`),
            },
            {
              field: "objective",
              name: t("workObjective"),
              width: "320px",
              format: (_, row) => row.objective ?? "—",
            },
            {
              field: "accountable_principal_id",
              name: t("workOwner"),
              width: "190px",
            },
            {
              field: "requirement_id",
              name: t("workRequirement"),
              width: "190px",
              format: (_, row) => row.requirement_id ?? t("workIndependent"),
            },
            {
              field: "updated_at",
              name: t("updatedAt"),
              width: "190px",
              format: (_, row) =>
                new Date(row.updated_at).toLocaleString(locale),
            },
          ]}
          actions={[{ name: t("workDetails"), icon: Eye, onclick: open }]}
        />
      )}
      {view === "list" && paginationFooter}
      {creating && (
        <CreateWork
          entityID={entityID}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false)
            void load()
          }}
        />
      )}
      {detail && detail.id === selectedWork && detail.project_id === entityID && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open) { setDetail(null); setReading(false); setSearch(previous => { const next = new URLSearchParams(previous); next.delete("work"); return next }) }
          }}
        >
          <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-2xl">
            <DialogHeader>
              <DialogTitle>{detail.title}</DialogTitle>
              <DialogDescription>
                {detail.contract
                  ? t("workContractVersion", {
                      version: detail.contract.version_no,
                    })
                  : t("workLegacyContract")}
              </DialogDescription>
            </DialogHeader>
            <AutoForm
              mode="detail"
              initialData={{
                objective: detail.objective,
                description: detail.description,
                criteria: detail.contract?.snapshot.criteria ?? detail.criteria,
                severity: detail.severity,
                priority: detail.priority,
                reproducibility: detail.reproducibility,
                found_version: detail.found_version,
                defect_stage: detail.defect_stage ? (t.has(`workDefectStage_${detail.defect_stage}`) ? t(`workDefectStage_${detail.defect_stage}`) : detail.defect_stage) : "—",
              }}
              fields={{
                severity: {
                  name: t("workSeverity"),
                  type: "text",
                  show: Boolean(detail.severity),
                },
                priority: {
                  name: t("workPriority"),
                  type: "text",
                  show: Boolean(detail.priority),
                },
                reproducibility: {
                  name: t("workReproducibility"),
                  type: "text",
                  show: Boolean(detail.reproducibility),
                },
                found_version: {
                  name: t("workFoundVersion"),
                  type: "text",
                  show: Boolean(detail.found_version),
                },
                defect_stage: {
                  name: t("workDefectStage"),
                  type: "text",
                  show: ["BUG", "INCIDENT"].includes(detail.type),
                },
                objective: { name: t("workObjective"), type: "textarea" },
                description: { name: t("description"), type: "textarea" },
                criteria: {
                  name: t("workCriteria"),
                  type: "custom",
                  component: CriteriaField,
                },
              }}
            />
            {detail.contract && (
              <p className="text-xs break-all text-muted-foreground">
                SHA-256 {detail.contract.content_hash}
              </p>
            )}
            <WorkAssignmentControls
              entityID={entityID}
              detail={detail}
              onUpdated={(current) => {
                setDetail(current)
                void load()
              }}
            />
            <WorkResults
              key={`results:${detail.id}`}
              entityID={entityID}
              detail={detail}
              onUpdated={(current) => {
                setDetail(current)
                void load()
              }}
            />
            <WorkRecovery
              key={`recovery:${detail.id}`}
              entityID={entityID}
              detail={detail}
              onUpdated={(current) => {
                setDetail(current)
                void load()
              }}
            />
            {["TASK", "BUG", "RISK", "INCIDENT"].includes(detail.type ?? "") && <WorkWorkflowTransition key={`workflow:${detail.id}`} entityID={entityID} detail={detail} onUpdated={(current) => { setDetail(current); void load() }} />}
            <WorkCodeLinks key={`code-links:${detail.id}`} entityID={entityID} detail={detail} />
            <WorkEffort key={`effort:${detail.id}`} workID={detail.id!} />
            {(detail.type === "BUG" || detail.type === "INCIDENT") && <WorkDefectReport key={`defect:${detail.id}`} workID={detail.id!} version={detail.version} onUpdated={() => { void iamApi.workItem(entityID, detail.id!).then(setDetail).catch(cause => setError(cause instanceof Error ? cause.message : String(cause))); void load() }} />}
            {(detail.type === "BUG" || detail.type === "INCIDENT") && <WorkCommentSource key={`comment-source:${detail.id}`} entityID={entityID} workID={detail.id} />}
            <WorkDependencies
              key={detail.id}
              entityID={entityID}
              detail={detail}
              onUpdated={(current) => {
                setDetail(current)
                void load()
              }}
            />
            <WorkAndon entityID={entityID} detail={detail} onUpdated={(current) => { setDetail(current); void load() }} />
<WorkSchedule key={`${detail.id}:${detail.version}`} entityID={entityID} detail={detail} onUpdated={(current) => { setDetail(current); void load() }} />
            <WorkPreparation
              key={detail.id}
              entityID={entityID}
              detail={detail}
              onUpdated={(current) => {
                setDetail(current)
                void load()
              }}
            />
          </DialogContent>
        </Dialog>
      )}
    </div>
  )
}

function CreateWork({
  entityID,
  onClose,
  onCreated,
}: {
  entityID: string
  onClose: () => void
  onCreated: () => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const { session } = useAuth()
  const locale = useLocale()
  const route = useMemo(
    () =>
      apiRoute("post", "/entities/{entity_id}/work-items", {
        path: { entity_id: entityID },
      }),
    [entityID]
  )
  const metadata = useRequestContract(route)
  const [owners, setOwners] = useState<{ value: string; label: string }[]>([])
  const [ownerError, setOwnerError] = useState("")
  const [requirements, setRequirements] = useState<Requirement[]>([])
  const [requirementError, setRequirementError] = useState("")
  const [requirementsLoading, setRequirementsLoading] = useState(false)
  const [requirementRetry, setRequirementRetry] = useState(0)
  const canReadRequirements = allowed("requirement_read")
  useEffect(() => {
    if (!canReadRequirements) return
    const controller = new AbortController()
    queueMicrotask(async () => {
      if (controller.signal.aborted) return
      setRequirementsLoading(true)
      setRequirementError("")
      try {
        const collected: Requirement[] = []
        let offset = 0
        // The domain caps active requirements at 500; traverse real pages
        // rather than silently hiding baselines after the first 100.
        while (true) {
          const page = await iamApi.requirements(
            entityID,
            { offset, limit: 100, status: "eq:BASELINED", sort: "number" },
            controller.signal
          )
          if (controller.signal.aborted) return
          collected.push(...page.items)
          offset += page.items.length
          if (offset >= page.total || !page.items.length) break
        }
        setRequirements(collected)
      } catch (cause) {
        if (!controller.signal.aborted)
          setRequirementError(
            cause instanceof Error ? cause.message : t("requirementLookupError")
          )
      } finally {
        if (!controller.signal.aborted) setRequirementsLoading(false)
      }
    })
    return () => controller.abort()
  }, [canReadRequirements, entityID, requirementRetry, t])
  const canListOwners = allowed("user_view")
  useEffect(() => {
    if (!canListOwners) return
    let active = true
    void iamApi.members().then(
      (members) => {
        if (active)
          setOwners(
            members
              .filter((member) => member.status === "active")
              .map((member) => ({
                value: member.subject,
                label: member.display_name || member.email || member.subject,
              }))
          )
      },
      (cause) => {
        if (active)
          setOwnerError(
            cause instanceof Error ? cause.message : t("workUnavailable")
          )
      }
    )
    return () => {
      active = false
    }
  }, [canListOwners, t])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const active = useRef(true)
  const pending = useRef(false)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
    }
  }, [])
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending.current) onClose()
      }}
    >
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("workCreate")}</DialogTitle>
          <DialogDescription>{t("workCreateHint")}</DialogDescription>
        </DialogHeader>
        {error && <Alert>{error}</Alert>}
        {ownerError && <Alert>{ownerError}</Alert>}
        {requirementError && (
          <Alert>
            {requirementError}{" "}
            <Button
              size="sm"
              variant="outline"
              onClick={() => setRequirementRetry((value) => value + 1)}
            >
              {t("retryNow")}
            </Button>
          </Alert>
        )}
        {metadata.failed && (
          <Alert>
            {t("projectContractUnavailable")}{" "}
            <Button variant="outline" size="sm" onClick={metadata.retry}>
              {t("retryNow")}
            </Button>
          </Alert>
        )}
        <AutoForm
          id="work-create-form"
          contract={metadata.contract}
          fields={{
            title: { name: t("workTitle"), type: "text", disabled: busy },
            type: {
              name: t("workType"),
              type: "select",
              value: "TASK",
              disabled: busy,
              config: {
                options: workTypes.map((value) => ({
                  value,
                  label: t(`workType_${value}`),
                })),
              },
            },
            severity: {
              name: t("workSeverity"),
              type: "select",
              show: isDefect,
              required: isDefect,
              disabled: busy,
              config: {
                options: ["FATAL", "CRITICAL", "NORMAL", "MINOR"].map(
                  (value) => ({ value, label: value })
                ),
              },
            },
            priority: {
              name: t("workPriority"),
              type: "select",
              show: isDefect,
              required: isDefect,
              disabled: busy,
              config: {
                options: ["URGENT", "HIGH", "MEDIUM", "LOW"].map((value) => ({
                  value,
                  label: value,
                })),
              },
            },
            reproducibility: {
              name: t("workReproducibility"),
              type: "select",
              show: isDefect,
              required: isDefect,
              disabled: busy,
              config: {
                options: ["ALWAYS", "HIGH", "MEDIUM", "LOW", "UNABLE"].map(
                  (value) => ({ value, label: value })
                ),
              },
            },
            found_version: {
              name: t("workFoundVersion"),
              type: "text",
              show: isDefect,
              disabled: busy,
            },
            defect_details: {
              name: defectMessages[locale as keyof typeof defectMessages]?.title ?? defectMessages["en-US"].title,
              type: "custom",
              component: DefectCreateFields,
              show: isDefect,
              required: isDefect,
              disabled: busy,
            },
            objective: {
              name: t("workObjective"),
              type: "textarea",
              disabled: busy,
            },
            description: {
              name: t("description"),
              type: "textarea",
              value: "",
              disabled: busy,
            },
            accountable_principal_id: {
              name: t("workOwner"),
              type: "select",
              value: session?.subject ?? "",
              disabled: busy,
              config: {
                options: [
                  {
                    value: session?.subject ?? "",
                    label:
                      session?.preferred_username ||
                      session?.email ||
                      session?.subject ||
                      "",
                  },
                  ...owners.filter((owner) => owner.value !== session?.subject),
                ],
              },
            },
            criteria: {
              name: t("workCriteria"),
              type: "custom",
              component: CriteriaField,
              value: [{ text: "", verification_method: "" }],
              disabled: busy,
            },
            ...(canReadRequirements
              ? {
                  requirement_id: {
                    name: t("requirementSelect"),
                    type: "custom" as const,
                    value: undefined,
                    disabled:
                      busy || requirementsLoading || Boolean(requirementError),
                    component: ({
                      value,
                      onChange,
                      disabled,
                      labelId,
                      describedBy,
                      error,
                      errorId,
                    }: FieldRendererProps) => (
                      <div>
                        <select
                          className="h-9 w-full rounded-md border bg-background px-3 text-sm"
                          value={typeof value === "string" ? value : "none"}
                          disabled={disabled}
                          aria-labelledby={labelId}
                          aria-describedby={describedBy}
                          aria-invalid={Boolean(error)}
                          onChange={(event) =>
                            onChange(
                              event.target.value === "none"
                                ? undefined
                                : event.target.value
                            )
                          }
                        >
                          <option value="none">{t("requirementNone")}</option>
                          {requirements.flatMap((requirement) =>
                            requirement.id
                              ? [
                                  <option
                                    key={requirement.id}
                                    value={requirement.id}
                                  >
                                    {requirement.title} · v
                                    {requirement.current_version_no}
                                  </option>,
                                ]
                              : []
                          )}
                        </select>
                        {error && (
                          <p
                            id={errorId}
                            role="alert"
                            className="text-sm text-destructive"
                          >
                            {error}
                          </p>
                        )}
                      </div>
                    ),
                  },
                }
              : {}),
          }}
          onSubmit={async (values) => {
            if (
              pending.current ||
              !allowed("work_item_write") ||
              !metadata.contract
            )
              return
            const body: WorkItemInput = {
              title: String(values.title),
              type: values.type as WorkItemInput["type"],
              objective: String(values.objective),
              description: String(values.description ?? ""),
              accountable_principal_id: String(values.accountable_principal_id),
              criteria: values.criteria as Clause[],
            }
            if (isDefect(values)) {
              body.defect_details = values.defect_details as WorkItemInput["defect_details"]
              body.severity = values.severity as WorkItemInput["severity"]
              body.priority = values.priority as WorkItemInput["priority"]
              body.reproducibility =
                values.reproducibility as WorkItemInput["reproducibility"]
              if (values.found_version)
                body.found_version = String(values.found_version)
            }
            if (values.requirement_id && values.requirement_id !== "none") {
              const selected = requirements.find(
                (requirement) => requirement.id === values.requirement_id
              )
              if (!selected?.id || requirementError || requirementsLoading) {
                setError(t("requirementLookupError"))
                return
              }
              body.requirement_id = selected.id
              body.baseline_version_no = selected.current_version_no
            }
            const identity = JSON.stringify(body)
            const key =
              attempt.current?.body === identity
                ? attempt.current.key
                : crypto.randomUUID()
            attempt.current = { body: identity, key }
            pending.current = true
            setBusy(true)
            setError("")
            try {
              await iamApi.createWorkItem(entityID, body, key)
              if (active.current) onCreated()
            } catch (cause) {
              if (active.current)
                setError(
                  cause instanceof Error ? cause.message : t("workUnavailable")
                )
            } finally {
              pending.current = false
              if (active.current) setBusy(false)
            }
          }}
          onError={(cause) =>
            setError(
              cause instanceof Error ? cause.message : t("workUnavailable")
            )
          }
        />
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            type="submit"
            form="work-create-form"
            disabled={busy || !metadata.contract || !allowed("work_item_write")}
          >
            {busy ? t("saving") : t("workConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
