import { AutoTable } from "@workspace/ui/components/auto-table"
import { useEffect, useState } from "react"
import { Link, Navigate, useNavigate, useParams } from "react-router"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { Tabs, TabsList, TabsTrigger } from "@workspace/ui/components/tabs"
import {
  iamApi,
  type Entity,
  type EntityRoleBinding,
  type Relationship,
  type Role,
  type Member,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import EntitiesPage from "./page"

export default function EntityWorkspace() {
  const { tenantID = "", entityID = "", view = "overview" } = useParams()
  const base = `/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(entityID)}`
  if (!["overview", "roles", "resources", "manage"].includes(view))
    return <Navigate to={`${base}/overview`} replace />
  if (view === "manage") return <EntitiesPage scopeEntityID={entityID} />
  return (
    <EntityDetails
      key={`${entityID}:${view}`}
      entityID={entityID}
      view={view}
      tenantID={tenantID}
    />
  )
}

function EntityDetails({
  tenantID,
  entityID,
  view,
}: {
  tenantID: string
  entityID: string
  view: string
}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const navigate = useNavigate()
  const base = `/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(entityID)}`
  const allowed = useAllowed()
  const [data, setData] = useState<{
    entity: Entity
    bindings: EntityRoleBinding[]
    relationships: Relationship[]
    roles: Role[]
    members: Member[]
  } | null>(null)
  const [error, setError] = useState("")
  const [reload, setReload] = useState(0)
  const [readLoading, setReadLoading] = useState(true)
  useEffect(() => {
    let active = true
    void Promise.all([
      iamApi.entity(entityID),
      view === "roles"
        ? iamApi.entityRoleBindings(entityID)
        : Promise.resolve([]),
      view === "resources"
        ? iamApi.resourceRelationships(entityID)
        : Promise.resolve([]),
      // Role and member directories belong to other permissions; without them
      // the entity workspace still renders its own bindings.
      view === "roles" && allowed("role_view")
        ? iamApi.roles()
        : Promise.resolve([]),
      view === "roles" && allowed("user_view")
        ? iamApi.members()
        : Promise.resolve([]),
    ])
      .then(
        ([entity, bindings, relationships, roles, members]) => {
          if (active)
            setData({ entity, bindings, relationships, roles, members })
        },
        (cause: Error) => {
          if (active) setError(cause.message)
        }
      )
      .finally(() => {
        if (active) setReadLoading(false)
      })
    return () => {
      active = false
    }
  }, [entityID, view, allowed, reload])
  if (error) return <Alert>{error}</Alert>
  if (!data) return <p role="status">{t("loading")}</p>
  const accessControl = view === "roles" || view === "resources"
  const title = accessControl ? "entityAccessControl" : "entityOverview"
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      <PageHeader
        title={t(title)}
        description={data.entity.name}
        action={
          <Link
            className="inline-flex min-h-11 items-center rounded-md border px-3 text-sm hover:bg-accent"
            to={`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(entityID)}/manage`}
          >
            {t("entitySettings")}
          </Link>
        }
      />
      <p className="mb-4 text-sm text-muted-foreground">
        {t("tenantDefinitionOwnership")}
      </p>
      {accessControl && (
        <Tabs
          value={view}
          onValueChange={(next) => navigate(`${base}/${next}`)}
        >
          <TabsList
            variant="line"
            className="mb-4"
            aria-label={t("entityAccessControl")}
          >
            <TabsTrigger variant="line" value="roles">
              {t("entityRoles")}
            </TabsTrigger>
            <TabsTrigger variant="line" value="resources">
              {t("entityResources")}
            </TabsTrigger>
          </TabsList>
        </Tabs>
      )}
      {accessControl && (
        <p className="mb-4 text-sm text-muted-foreground">
          {t("entityBindingScopeExplanation")}
        </p>
      )}
      {view === "overview" && (
        <dl className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-x-4 overflow-auto text-sm [&>dd]:border-b [&>dd]:py-4 [&>dd]:break-all [&>dt]:border-b [&>dt]:py-4 [&>dt]:text-muted-foreground">
          {data.entity.type === "project" && allowed("project_read") && (
            <>
              <dt>{t("projectDetails")}</dt>
              <dd>
                <Link className="text-primary underline" to={`${base}/project`}>
                  {t("projectDetails")}
                </Link>
              </dd>
            </>
          )}
          <dt>{t("entity")}</dt>
          <dd>{data.entity.name}</dd>
          <dt>{t("id")}</dt>
          <dd>{data.entity.id}</dd>
          <dt>{t("type")}</dt>
          <dd>{data.entity.type}</dd>
          <dt>{t("status")}</dt>
          <dd>{t(data.entity.status === "active" ? "active" : "disable")}</dd>
          <dt>{t("parentEntity")}</dt>
          <dd>{data.entity.parent_id || "-"}</dd>
          <dt>{t("metadata")}</dt>
          <dd>
            <pre className="break-all whitespace-pre-wrap">
              {JSON.stringify(data.entity.metadata, null, 2)}
            </pre>
          </dd>
        </dl>
      )}
      {view === "roles" && (
        <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden border-y">
          <AutoTable
            fill
            loading={readLoading}
            onRefresh={() => {
              setReadLoading(true)
              setReload((value) => value + 1)
            }}
            searchable={(binding) =>
              [
                binding.role_id,
                binding.principal_id,
                data.roles.find((role) => role.id === binding.role_id)?.name,
                data.members.find(
                  (member) => member.subject === binding.principal_id
                )?.display_name,
                binding.effect,
              ].join(" ")
            }
            emptyContent={t("noEntityRoles")}

            data={data.bindings}
            rowKey={(binding) => `${binding.role_id}:${binding.principal_id}`}
            columns={[
              {
                field: "roleName",
                name: t("roleName"),
                type: "custom",
                render: (_value, binding) => (
                  <div>
                    {data.roles.find((role) => role.id === binding.role_id)
                      ?.name ?? binding.role_id}
                  </div>
                ),
              },
              {
                field: "principal",
                name: t("principal"),
                type: "custom",
                render: (_value, binding) => (
                  <div>
                    {data.members.find(
                      (member) => member.subject === binding.principal_id
                    )?.display_name || binding.principal_id}
                  </div>
                ),
              },
              {
                field: "effect",
                name: t("effect"),
                type: "custom",
                render: (_value, binding) => (
                  <div>{t(binding.effect === "allow" ? "allow" : "deny")}</div>
                ),
              },
              {
                field: "expiresAt",
                name: t("expiresAt"),
                type: "custom",
                render: (_value, binding) => (
                  <div>
                    {binding.expires_at
                      ? new Date(binding.expires_at).toLocaleString(locale)
                      : t("neverExpires")}
                  </div>
                ),
              },
            ]}
          />
        </div>
      )}
      {view === "resources" && (
        <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden border-y">
          <AutoTable
            fill
            loading={readLoading}
            onRefresh={() => {
              setReadLoading(true)
              setReload((value) => value + 1)
            }}
            searchable={(relationship) =>
              [
                relationship.resource_type,
                relationship.resource_id,
                relationship.subject_id,
                relationship.relation,
              ].join(" ")
            }
            emptyContent={t("noRelations")}

            data={data.relationships}
            rowKey={(relationship) => JSON.stringify(relationship)}
            columns={[
              {
                field: "resource_type",
                name: t("resourceType"),
                type: "text",
              },
              {
                field: "resourceId",
                name: t("resourceId"),
                type: "custom",
                render: (_value, relationship) => (
                  <div className="break-all">{relationship.resource_id}</div>
                ),
              },
              {
                field: "grantRelation",
                name: t("grantRelation"),
                type: "custom",
                render: (_value, relationship) => (
                  <div>{t(relationship.relation)}</div>
                ),
              },
              {
                field: "grantRecipient",
                name: t("grantRecipient"),
                type: "custom",
                render: (_value, relationship) => (
                  <div className="break-all">{relationship.subject_id}</div>
                ),
              },
              {
                field: "type",
                name: t("type"),
                type: "custom",
                render: (_value, relationship) => (
                  <div>{t(relationship.subject_type)}</div>
                ),
              },
              {
                field: "startTime",
                name: t("startTime"),
                type: "custom",
                render: (_value, relationship) => (
                  <div>
                    {relationship.starts_at
                      ? new Date(relationship.starts_at).toLocaleString(locale)
                      : "—"}
                  </div>
                ),
              },
              {
                field: "endTime",
                name: t("endTime"),
                type: "custom",
                render: (_value, relationship) => (
                  <div>
                    {relationship.ends_at
                      ? new Date(relationship.ends_at).toLocaleString(locale)
                      : t("neverExpires")}
                  </div>
                ),
              },
            ]}
          />
        </div>
      )}
    </div>
  )
}
