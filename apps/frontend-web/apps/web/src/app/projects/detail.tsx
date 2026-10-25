import { useCallback, useEffect, useRef, useState } from "react"
import { useParams, useSearchParams } from "react-router"
import { Tabs, TabsList, TabsTrigger } from "@workspace/ui/components/tabs"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { RefreshButton } from "@workspace/ui/components/refresh-button"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi, type ProjectDetail } from "../../lib/iam-api"
import { ProjectConstitutions } from "./constitutions"
import { ProjectStages } from "./stages"
import { ProjectModes } from "./modes"
import { ProjectBudget } from "./budget"
import { ProjectOverviewFacts, ProjectWorkAreas } from "./overview"
import { ProjectArchive } from "./archive"
import { ProjectWorkflowGraph } from "../templates/page"

export default function ProjectDetailPage() {
  const { tenantID = "", entityID = "" } = useParams()
  return (
    <ProjectDetails
      key={`${tenantID}:${entityID}`}
      tenantID={tenantID}
      entityID={entityID}
    />
  )
}

function ProjectDetails({
  tenantID,
  entityID,
}: {
  tenantID: string
  entityID: string
}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const [search, setSearch] = useSearchParams()
  const tab =
    search.get("view") === "constitution" ? "constitution" : search.get("view") === "pipeline" ? "pipeline" : "overview"
  const [project, setProject] = useState<ProjectDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const pending = useRef<AbortController | null>(null)
  const load = useCallback(() => {
    if (pending.current) return
    const controller = new AbortController()
    pending.current = controller
    return iamApi
      .project(entityID, controller.signal)
      .then((next) => {
        if (controller.signal.aborted) return
        // The resource resolves its owner on the server; keep URL context faithful.
        if (next.tenant_id !== tenantID || next.project_id !== entityID)
          throw new Error("projectUnavailable")
        setProject(next)
      })
      .catch((cause) => {
        if (controller.signal.aborted) return
        setProject(null)
        setError(cause instanceof Error ? cause.message : "projectUnavailable")
      })
      .finally(() => {
        if (pending.current === controller) pending.current = null
        if (!controller.signal.aborted) setLoading(false)
      })
  }, [tenantID, entityID])
  useEffect(() => {
    void load()
    return () => {
      pending.current?.abort()
      pending.current = null
    }
  }, [load])

  const label = (kind: string, value: string) => {
    const key = `project_${kind}_${value}`
    return t.has(key) ? t(key) : value
  }
  const dateTime = (value: string | null) =>
    value ? new Date(value).toLocaleString(locale) : ""
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      <PageHeader
        title={t("instanceOverview")}
        description={project?.name}
        action={
          tab === "overview" ? (
            <RefreshButton
              onRefresh={() => {
                setLoading(true)
                setError("")
                return load()
              }}
              loading={loading}
            />
          ) : undefined
        }
      />
      {error && <Alert>{t.has(error) ? t(error) : error}</Alert>}
      {loading && !project && <p role="status">{t("loading")}</p>}
      {project?.archived_at && <Alert>{t("archiveReadOnly")}. {t("archiveRestoreNotice")}</Alert>}
      {project && (
        <Tabs
          value={tab}
          onValueChange={(value) =>
            setSearch((previous) => {
              const next = new URLSearchParams(previous)
              next.set("view", value)
              return next
            })
          }
        >
          <TabsList>
            <TabsTrigger value="overview">{t("instanceOverview")}</TabsTrigger>
            <TabsTrigger value="constitution">
              {t("constitutionTitle")}
            </TabsTrigger>
            <TabsTrigger value="pipeline">{t("templateInstance")}</TabsTrigger>
          </TabsList>
        </Tabs>
      )}
      {project && tab === "constitution" && (
        <ProjectConstitutions
          entityID={entityID}
          readOnly={
            !!project.archived_at ||
            ["CLOSED", "CANCELLED"].includes(project.stage)
          }
          onChanged={() => {
            void load()
          }}
        />
      )}
      {project && tab === "pipeline" && (
        <ProjectWorkflowGraph tenantID={tenantID} projectID={entityID} />
      )}
      {project && tab === "overview" && (
        <section
          className="min-h-0 flex-1 overflow-auto"
          aria-label={t("projectDetails")}
          aria-busy={loading}
        >
          <ProjectWorkAreas tenantID={tenantID} projectID={entityID}/>
          <ProjectOverviewFacts project={project}/>
          <ProjectArchive project={project} onChanged={() => { void load() }}/>
          <ProjectStages
            project={project}
            onChanged={() => {
              void load()
            }}
          />
          <ProjectModes project={project} onChanged={() => { void load() }}/>
          <ProjectBudget project={project} onChanged={() => { void load() }}/>
          <AutoForm
            key={`${project.project_id}:${project.version}:${locale}`}
            mode="detail"
            initialData={{
              ...project,
              stage: label("stage", project.stage),
              team_mode: label("team", project.team_mode),
              health: label("health", project.health),
            }}
            fields={{
              name: { name: t("name"), type: "text" },
              code: { name: t("projectCode"), type: "text" },
              project_id: { name: t("id"), type: "text" },
              stage: {
                name: t("projectStage"),
                type: "text",
              },
              team_mode: {
                name: t("projectTeamMode"),
                type: "text",
              },
              health: {
                name: t("projectHealth"),
                type: "text",
              },
              client_name: {
                name: t("projectClient"),
                type: "text",
                value: project.client?.name ?? "",
              },
              lead_agent_id: { name: t("projectLeadAgent"), type: "text" },
              target_date: { name: t("projectTargetDate"), type: "date" },
              version: { name: t("projectVersion"), type: "number" },
              description: { name: t("description"), type: "textarea" },
              blocked_reason: {
                name: t("projectBlockedReason"),
                type: "textarea",
                show: !!project.blocked_reason,
              },
              created_time: {
                name: t("createdAt"),
                type: "text",
                value: dateTime(project.created_at),
              },
              updated_time: {
                name: t("updatedAt"),
                type: "text",
                value: dateTime(project.updated_at),
              },
              archived_time: {
                name: t("projectArchivedAt"),
                type: "text",
                value: dateTime(project.archived_at),
                show: !!project.archived_at,
              },
            }}
          />
        </section>
      )}
    </div>
  )
}
