import { useCallback, useEffect, useRef, useState } from "react"
import { useNavigate, useParams } from "react-router"
import { Eye } from "lucide-react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { PaginationPages } from "@workspace/ui/components/pagination"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { ProjectCreateAction } from "./create"
import {
  iamApi,
  type ProjectListItem,
  type ProjectListQuery,
} from "../../lib/iam-api"

const initialFilters = {
  name: "",
  code: "",
  stage: "all",
  health: "all",
  team_mode: "all",
  archived: "false",
  client_entity_id: "",
  lead_agent_id: "",
  sort: "-created_at",
}
const stages = [
  "INTAKE",
  "DISCOVERY",
  "PLANNING",
  "ACTIVE",
  "BLOCKED",
  "ACCEPTANCE_PENDING",
  "DELIVERED",
  "CLOSED",
  "CANCELLED",
]

// Preserve all micros, including amounts beyond Number.MAX_SAFE_INTEGER.
function formatMicros(value: string, locale: string) {
  const micros = BigInt(value)
  const whole = new Intl.NumberFormat(locale).format(micros / 1_000_000n)
  const remainder = (micros % 1_000_000n)
    .toString()
    .padStart(6, "0")
    .replace(/0+$/, "")
  const decimal =
    new Intl.NumberFormat(locale)
      .formatToParts(1.1)
      .find((part) => part.type === "decimal")?.value ?? "."
  return `${whole}${remainder ? decimal + remainder : ""} USD`
}

export default function ProjectsPage() {
  const { tenantID = "" } = useParams()
  return <Projects key={tenantID} tenantID={tenantID} />
}

function Projects({ tenantID }: { tenantID: string }) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const navigate = useNavigate()
  const [filters, setFilters] = useState<ProjectListQuery>({
    sort: "-created_at",
  })
  const [offset, setOffset] = useState(0)
  const [limit, setLimit] = useState(20)
  const [rows, setRows] = useState<ProjectListItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const pending = useRef<AbortController | null>(null)
  const load = useCallback(async () => {
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setLoading(true)
    setError("")
    try {
      const result = await iamApi.projects(
        tenantID,
        { ...filters, offset, limit },
        controller.signal
      )
      if (controller.signal.aborted) return
      // Deletions or permission changes can make the current last page disappear.
      if (offset > 0 && offset >= result.total) {
        setOffset(Math.max(0, Math.ceil(result.total / limit) - 1) * limit)
        return
      }
      setRows(result.items)
      setTotal(result.total)
    } catch (cause) {
      if (controller.signal.aborted) return
      setRows([])
      setError(cause instanceof Error ? cause.message : t("projectUnavailable"))
    } finally {
      if (pending.current === controller) pending.current = null
      if (!controller.signal.aborted) setLoading(false)
    }
  }, [tenantID, filters, offset, limit, t])
  useEffect(() => {
    let active = true
    queueMicrotask(() => {
      if (active) void load()
    })
    return () => {
      active = false
      pending.current?.abort()
      pending.current = null
    }
  }, [load])

  const label = (kind: string, value: string) => t(`project_${kind}_${value}`)
  const options = (kind: string, values: string[]) => [
    { value: "all", label: t("all") },
    ...values.map((value) => ({ value, label: label(kind, value) })),
  ]
  const open = (row: ProjectListItem) =>
    navigate(
      `/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(row.project_id ?? "")}/project`
    )
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      <PageHeader
        title={t("projects")}
        action={<ProjectCreateAction tenantID={tenantID} />}
      />
      {error && <Alert>{error}</Alert>}
      <AutoTable
        fill
        data={loading ? [] : rows}
        loading={loading}
        rowKey="project_id"
        rowOffset={offset}
        pagination={false}
        onRefresh={load}
        exportable={!error}
        emptyContent={error ? t("projectListRetry") : t("projectListEmpty")}
        toolbar={
          <AutoForm
            mode="edit"
            initialData={initialFilters}
            className="max-h-[28dvh] flex-1 overflow-y-auto md:grid-cols-3 xl:grid-cols-5"
            fields={{
              name: {
                name: t("name"),
                type: "text",
                config: { type: "search" },
              },
              stage: {
                name: t("projectStage"),
                type: "select",
                config: { options: options("stage", stages) },
              },
              health: {
                name: t("projectHealth"),
                type: "select",
                config: {
                  options: options("health", ["GREEN", "AMBER", "RED", "NONE"]),
                },
              },
              team_mode: {
                name: t("projectTeamMode"),
                type: "select",
                config: {
                  options: options("team", [
                    "HUMAN_ONLY",
                    "HYBRID",
                    "AUTONOMOUS",
                  ]),
                },
              },
              code: { name: t("projectCode"), type: "text" },
              client_entity_id: { name: t("projectClientID"), type: "text" },
              lead_agent_id: { name: t("projectLeadAgent"), type: "text" },
              archived: {
                name: t("projectArchivedFilter"),
                type: "select",
                config: {
                  options: [
                    { value: "false", label: t("projectActiveRecords") },
                    { value: "true", label: t("projectArchivedRecords") },
                    { value: "all", label: t("archiveAll") },
                  ],
                },
              },
              sort: {
                name: t("projectSort"),
                type: "select",
                config: {
                  options: [
                    { value: "-created_at", label: t("projectNewest") },
                    { value: "name", label: t("name") },
                    {
                      value: "-stage_changed_at",
                      label: t("projectStageChanged"),
                    },
                    { value: "health", label: t("projectHealth") },
                    { value: "target_date", label: t("projectTargetDate") },
                  ],
                },
              },
            }}
            onSubmit={(values) => {
              const next: ProjectListQuery = {
                sort: String(values.sort),
                archived: values.archived === "all" ? "in:true,false" : `eq:${values.archived}`,
              }
              for (const field of [
                "name",
                "code",
                "stage",
                "health",
                "team_mode",
                "client_entity_id",
                "lead_agent_id",
              ] as const) {
                const value = String(values[field] ?? "").trim()
                const isAll =
                  ["stage", "health", "team_mode"].includes(field) &&
                  value === "all"
                if (value && !isAll)
                  next[field] = `${field === "name" ? "like" : "eq"}:${value}`
              }
              setOffset(0)
              setFilters(next)
            }}
          >
            <Button type="submit" size="sm">
              {t("search")}
            </Button>
          </AutoForm>
        }
        columns={[
          { field: "code", name: t("projectCode"), width: "140px" },
          { field: "name", name: t("name"), width: "220px" },
          {
            field: "client",
            name: t("projectClient"),
            width: "170px",
            format: (_, row) => row.client?.name ?? "—",
          },
          {
            field: "stage",
            name: t("projectStage"),
            width: "170px",
            format: (_, row) => label("stage", row.stage),
          },
          {
            field: "team_mode",
            name: t("projectTeamMode"),
            width: "160px",
            format: (_, row) => label("team", row.team_mode),
          },
          {
            field: "health",
            name: t("projectHealth"),
            width: "120px",
            format: (_, row) => label("health", row.health),
          },
          {
            field: "budget",
            name: t("projectBudget"),
            width: "290px",
            format: (_, row) =>
              `${formatMicros(row.budget.spent, locale)} / ${row.budget.cap === null ? t("projectNoCap") : formatMicros(row.budget.cap, locale)}${row.budget.pct === null ? "" : ` (${new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(row.budget.pct)}%)`}`,
          },
          {
            field: "open_work_items",
            name: t("projectOpenWorkItems"),
            width: "130px",
            format: (_, row) => row.counts.open_work_items,
          },
          {
            field: "needs_review",
            name: t("projectNeedsReview"),
            width: "130px",
            format: (_, row) => row.counts.needs_review,
          },
          {
            field: "waiting_deliveries",
            name: t("projectWaitingDeliveries"),
            width: "130px",
            format: (_, row) => row.counts.waiting_deliveries,
          },
          {
            field: "target_date",
            name: t("projectTargetDate"),
            width: "130px",
            format: (_, row) => row.target_date ?? "—",
          },
          {
            field: "updated_at",
            name: t("updatedAt"),
            width: "190px",
            format: (_, row) => new Date(row.updated_at).toLocaleString(locale),
          },
        ]}
        actions={[{ name: t("projectDetails"), icon: Eye, onclick: open }]}
        footer={
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
                value={limit}
                disabled={loading}
                className="rounded-md border bg-background p-1"
                onChange={(event) => {
                  setOffset(0)
                  setLimit(Number(event.target.value))
                }}
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
              onPageChange={(page) => setOffset((page - 1) * limit)}
            />
          </div>
        }
      />
    </div>
  )
}
