import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Suspense, useEffect, useState } from "react"
import { KeyRound, MonitorSmartphone, Network, Users } from "lucide-react"
import { Await, Link, Navigate, useLoaderData, useOutletContext, useParams } from "react-router"
import { ProjectCreateAction } from "../projects/create"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi, type EffectiveMenu, type ProjectListItem } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"
import { useAllowed } from "../../lib/permissions"
import { governanceApi, inboxObjectHref, type InboxPage } from "../governance/api"
import { effectiveMenuPaths } from "../../lib/navigation"
import { getAccountView } from "../../lib/account-view"
import { WorkbenchSummary } from "./summary"
import type { tenantWorkspaceLoader } from "../../route-loaders"

interface Metrics {
  principals?: number
  roles?: number
  menus?: number
  sessions?: number
}

export default function DashboardPage() {
  const t = useTranslations("iam")
  const { workspacePath } = useLoaderData<ReturnType<typeof tenantWorkspaceLoader>>()
  return (
    <Suspense fallback={<p role="status">{t("loading")}</p>}>
      <Await resolve={workspacePath}>
        {(path: string) => path ? <Navigate to={path} replace /> : <TenantDashboard />}
      </Await>
    </Suspense>
  )
}

function TenantDashboard() {
  const { tenantID = "" } = useParams()
  const t = useTranslations("iam")
  const accountView = getAccountView()

  const { menus } = useOutletContext<{ menus: EffectiveMenu[] | null }>()
  const [metrics, setMetrics] = useState<Metrics>({
    sessions: accountView ? undefined : 0,
  })
  const [error, setError] = useState("")
  useEffect(() => {
    if (menus === null) return
    const paths = effectiveMenuPaths(menus)
    let active = true
    void Promise.all([
      paths.has("/users")
        ? iamApi.principals().then((result) => result.total)
        : undefined,
      paths.has("/roles")
        ? iamApi.roles().then((result) => result.length)
        : undefined,
      paths.has("/menus")
        ? iamApi.menus().then((result) => result.length)
        : undefined,
      accountView
        ? undefined
        : iamApi.sessions().then((result) => result.length),
    ])
      .then(([principals, roles, menuCount, sessions]) => {
        if (active) {
          setError("")
          setMetrics({ principals, roles, menus: menuCount, sessions })
        }
      })
      .catch((cause: Error) => {
        if (active) setError(cause.message)
      })
    return () => {
      active = false
    }
  }, [menus, accountView])
  return (
    <>
      <PageHeader
        title={t("workspaceArea_workbench")}
        description={t("tenantWorkbenchDescription")}
        action={<ProjectCreateAction key={tenantID} tenantID={tenantID} />}
      />
      {error && <Alert>{error}</Alert>}
      {!accountView && <WorkbenchOverview key={tenantID} tenantID={tenantID}/>}
      <div className="mt-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {metrics.principals !== undefined && (
          <Metric
            icon={<Users />}
            label={t("localPrincipals")}
            value={metrics.principals}
          />
        )}
        {metrics.roles !== undefined && (
          <Metric
            icon={<KeyRound />}
            label={t("tenantRoles")}
            value={metrics.roles}
          />
        )}
        {metrics.menus !== undefined && (
          <Metric
            icon={<Network />}
            label={t("menuNodes")}
            value={metrics.menus}
          />
        )}
        {metrics.sessions !== undefined && (
          <Metric
            icon={<MonitorSmartphone />}
            label={t("mySessions")}
            value={metrics.sessions}
          />
        )}
      </div>
    </>
  )
}

function WorkbenchOverview({tenantID}: {tenantID: string}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const canReadProjects = allowed("project_read")
  const canReadWork = allowed("work_item_read")
  const [inbox, setInbox] = useState<InboxPage | null>(null)
  const [projects, setProjects] = useState<ProjectListItem[] | null>(null)
  const [work, setWork] = useState<components["schemas"]["PersonalWorkPage"] | null>(null)
  const [errors, setErrors] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    const controller = new AbortController()
    void Promise.allSettled([
      governanceApi(tenantID).inbox({}, controller.signal),
      canReadProjects ? iamApi.projects(tenantID, {offset: 0, limit: 5, sort: "-created_at", archived: "eq:false"}, controller.signal) : Promise.resolve(null),
      canReadWork ? iamApi.personalWork(tenantID, "", "", controller.signal) : Promise.resolve(null),
    ]).then(([decisions, projectPage, workPage]) => {
      if (controller.signal.aborted) return
      if (decisions.status === "fulfilled") setInbox(decisions.value)
      if (projectPage.status === "fulfilled") setProjects(projectPage.value?.items ?? null)
      if (workPage.status === "fulfilled") setWork(workPage.value)
      setErrors([decisions, projectPage, workPage].flatMap(result => result.status === "rejected" ? [result.reason instanceof Error ? result.reason.message : String(result.reason)] : []))
      setLoading(false)
    })
    return () => controller.abort()
  }, [tenantID, canReadProjects, canReadWork])
  const root = `/admin/${encodeURIComponent(tenantID)}`
  const decisions = (
      <section className="min-w-0 rounded-md border p-5" aria-label={t("govInboxTitle")}>
        <h2 className="mb-3 font-semibold"><Link className="underline underline-offset-4" to={`${root}/governance/inbox`}>{t("govInboxTitle")}</Link></h2>
        {inbox && <><ul className="divide-y">{inbox.items.slice(0, 5).map(item => {const href = inboxObjectHref(tenantID, item);return <li key={`${item.kind}:${item.id}`} className="space-y-1 py-3"><p>{href ? <Link className="underline underline-offset-4" to={href}>{item.title}</Link> : item.title}</p><p className="text-sm text-muted-foreground">{t(`govKind_${item.kind}`)} · {t(`govPriority_${item.priority}`)}</p>{item.reason && <p className="text-sm">{item.reason}</p>}</li>})}</ul>{!inbox.items.length && <p className="text-sm text-muted-foreground">{t("govInboxEmpty")}</p>}{inbox.capabilities.some(value => value.status === "UNAVAILABLE") && <p className="mt-3 text-sm text-muted-foreground">{t("govMissingHint")}</p>}</>}
      </section>
  )
  return <div className="mt-6 space-y-6">
    <WorkbenchSummary key={tenantID} tenantID={tenantID} decisions={decisions}/>
    {errors.map((message, index) => <Alert key={`${index}:${message}`}>{message}</Alert>)}
    {loading && <p role="status">{t("loading")}</p>}
    <div className="grid gap-6 lg:grid-cols-2">

      {canReadWork && <section className="min-w-0 rounded-md border p-5" aria-label={t("personalWorkTitle")}><h2 className="mb-3 font-semibold"><Link className="underline underline-offset-4" to={`${root}/my-work`}>{t("personalWorkTitle")}</Link></h2>{work && <><ul className="divide-y">{(work.items ?? []).slice(0, 5).map(item => <li key={item.id} className="py-3"><Link className="underline underline-offset-4" to={`${root}/my-work?${new URLSearchParams({project: item.project_id ?? "", work: item.id ?? ""})}`}>{item.title}</Link><p className="text-sm text-muted-foreground">{t.has(`workState_${item.state}`) ? t(`workState_${item.state}`) : item.state} · {t(`personalWork_${item.responsibility}`)}</p></li>)}</ul>{!work.items?.length && <p className="text-sm text-muted-foreground">{t("workbenchEmpty")}</p>}</>}</section>}
    </div>
    {canReadProjects && <section className="rounded-md border p-5" aria-label={t("projects")}><h2 className="mb-3 font-semibold"><Link className="underline underline-offset-4" to={`${root}/projects`}>{t("projects")}</Link></h2>{projects && <><ul className="divide-y">{projects.map(project => <li key={project.project_id} className="flex flex-wrap items-center justify-between gap-3 py-3"><Link className="underline underline-offset-4" to={`${root}/entities/${project.project_id}/project`}>{project.name}</Link><span className="text-sm">{t("projectStage")}: {t.has(`project_stage_${project.stage}`) ? t(`project_stage_${project.stage}`) : project.stage} · {t("projectHealth")}: {t.has(`project_health_${project.health}`) ? t(`project_health_${project.health}`) : project.health}</span></li>)}</ul>{!projects.length && <p className="text-sm text-muted-foreground">{t("workbenchEmpty")}</p>}</>}</section>}
  </div>
}

function Metric({
  icon,
  label,
  value,
}: {
  icon: React.ReactNode
  label: string
  value: number
}) {
  return (
    <article className="flex min-h-28 items-center gap-4 rounded-md border bg-card p-5">
      <span className="grid size-10 place-items-center rounded-md bg-primary/10 text-primary [&>svg]:size-5">
        {icon}
      </span>
      <div>
        <small className="text-muted-foreground">{label}</small>
        <strong className="mt-1 block text-2xl">{value}</strong>
      </div>
    </article>
  )
}
