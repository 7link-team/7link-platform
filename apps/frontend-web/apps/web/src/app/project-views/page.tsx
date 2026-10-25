import { Link, useParams } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { projectSurface, type ProjectSurface } from "../../lib/project-surfaces"
import { PipelineView } from "./pipeline"
import { ProjectWorkView } from "./work"
import DispatchWorkspace from "./dispatch"
import ToleranceWorkspace from "./tolerances"
import ClientPending from "./pending"
import ClientWorkflowPage, { ClientProjectSettings } from "./client"
import HeavyPage from "../heavy/page"
import StudioPage from "../studios/page"
import IntakeWorkbench from "./intake-workbench"
import { AutomationRules } from "./automation"
import { TemplateBinding } from "../templates/binding"

export default function ProjectSurfacePage({ surface }: { surface: ProjectSurface }) {
  const { tenantID = "", entityID = "" } = useParams()
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const entry = projectSurface(surface)!
  if (surface === "intake") return <IntakeWorkbench key={`${tenantID}:${entityID}`} />
  if (surface === "heavy") return <HeavyPage />
  if (surface === "video" || surface === "novel" || surface === "team-tasks") return <StudioPage domain={surface === "video" ? "VIDEO" : surface === "novel" ? "NOVEL" : "TEAM_TASKS"} />
  if (surface === "todo") return <ClientPending key={`${tenantID}:${entityID}`} />
  if (["baseline", "changes", "feedback", "deliveries", "test-cases", "previews"].includes(surface)) return <ClientWorkflowPage key={`${tenantID}:${entityID}:${surface}`} surface={surface} />
  if (surface === "dispatch") return <DispatchWorkspace key={`${tenantID}:${entityID}`} />
  if (surface === "exceptions") return <ToleranceWorkspace key={`${tenantID}:${entityID}`} />
  if (surface === "project-settings") return <div className="space-y-4 overflow-auto"><ClientProjectSettings key={`${tenantID}:${entityID}`} /><TemplateBinding key={`binding:${tenantID}:${entityID}`} tenant={tenantID} project={entityID} /><AutomationRules key={`automation:${tenantID}:${entityID}`} tenant={tenantID} project={entityID} /></div>
  const base = `/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(entityID)}`
  // A client lacking internal permissions never falls through to an internal
  // requirements/work page. A future public projection must be a server fact.
  if (!allowed(entry.permission)) return <div className="space-y-3 p-4"><PageHeader title={t(entry.label)} /><p role="status">{t("projectSurfaceAccessUnavailable")}</p></div>
  const links = [
    ...(allowed("requirement_read") && ["baseline", "changes"].includes(surface) ? [{ path: "requirements", label: "requirements" }] : []),
    ...(allowed("channel_read") && ["baseline", "feedback"].includes(surface) ? [{ path: "conversations", label: "conversations" }] : []),
    ...(allowed("work_item_read") && ["review", "defects", "deliveries"].includes(surface) ? [{ path: `work-items?${new URLSearchParams(surface === "defects" ? { type: "BUG" } : surface === "review" ? { state: "NEEDS_HUMAN_REVIEW" } : { state: "ACCEPTED" })}`, label: "workItems" }] : []),
    ...(allowed("execution_read") && ["pipelines", "previews"].includes(surface) ? [{ path: "executions", label: "executions" }] : []),
  ]
  return <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
    <PageHeader title={t(entry.label)} />
    {surface !== "pipelines" && <p className="shrink-0 text-sm text-muted-foreground" role="status">{t(`projectSurfaceUnavailable_${surface}`)}</p>}
    {!!links.length && <nav aria-label={t("projectSurfaceRelated")} className="flex shrink-0 flex-wrap gap-3">
      {links.map((link) => <Link key={link.path} className="underline underline-offset-4" to={`${base}/${link.path}`}>{t(link.label)}</Link>)}
    </nav>}
    {surface === "pipelines" && <PipelineView key={`${tenantID}:${entityID}`} tenant={tenantID} project={entityID} />}
    {(["review", "defects", "deliveries"] as const).map((view) => surface === view && <ProjectWorkView key={`${tenantID}:${entityID}:${view}`} project={entityID} view={view} />)}
  </div>
}
