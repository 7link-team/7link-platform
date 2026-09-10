import { useEffect, useState } from "react"
import { Link, Navigate, useParams } from "react-router"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import {
  iamApi,
  type Entity,
  type EntityRoleBinding,
  type Relationship,
  type Role,
  type Member,
} from "../../lib/iam-api"
import EntitiesPage from "./page"

export default function EntityWorkspace() {
  const { entityID = "", view = "overview" } = useParams()
  if (!["overview", "roles", "resources", "manage"].includes(view))
    return (
      <Navigate
        to={`/iam/entities/${encodeURIComponent(entityID)}/overview`}
        replace
      />
    )
  if (view === "manage") return <EntitiesPage scopeEntityID={entityID} />
  return (
    <EntityDetails
      key={`${entityID}:${view}`}
      entityID={entityID}
      view={view}
    />
  )
}

function EntityDetails({ entityID, view }: { entityID: string; view: string }) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const [data, setData] = useState<{
    entity: Entity
    bindings: EntityRoleBinding[]
    relationships: Relationship[]
    roles: Role[]
    members: Member[]
  } | null>(null)
  const [error, setError] = useState("")
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
      view === "roles" ? iamApi.roles() : Promise.resolve([]),
      view === "roles" ? iamApi.members() : Promise.resolve([]),
    ]).then(
      ([entity, bindings, relationships, roles, members]) => {
        if (active) setData({ entity, bindings, relationships, roles, members })
      },
      (cause: Error) => {
        if (active) setError(cause.message)
      }
    )
    return () => {
      active = false
    }
  }, [entityID, view])
  if (error) return <Alert>{error}</Alert>
  if (!data) return <p role="status">{t("loading")}</p>
  const title =
    view === "roles"
      ? "entityRoles"
      : view === "resources"
        ? "entityResources"
        : "entityOverview"
  return (
    <>
      <PageHeader
        title={t(title)}
        description={data.entity.name}
        action={
          <Link
            className="inline-flex min-h-11 items-center rounded-md border px-3 text-sm hover:bg-accent"
            to={`/iam/entities/${encodeURIComponent(entityID)}/manage`}
          >
            {t("entitySettings")}
          </Link>
        }
      />
      <p className="mb-4 text-sm text-muted-foreground">
        {t("tenantDefinitionOwnership")}
      </p>
      {view !== "overview" && (
        <p className="mb-4 text-sm text-muted-foreground">
          {t("entityBindingScopeExplanation")}
        </p>
      )}
      {view === "overview" && (
        <dl className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-x-4 text-sm [&>dd]:border-b [&>dd]:py-4 [&>dd]:break-all [&>dt]:border-b [&>dt]:py-4 [&>dt]:text-muted-foreground">
          <dt>{t("entity")}</dt>
          <dd>{data.entity.name}</dd>
          <dt>ID</dt>
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
        <div className="overflow-x-auto border-y">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("roleName")}</TableHead>
                <TableHead>{t("principal")}</TableHead>
                <TableHead>{t("effect")}</TableHead>
                <TableHead>{t("expiresAt")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.bindings.map((binding) => (
                <TableRow key={`${binding.role_id}:${binding.principal_id}`}>
                  <TableCell>
                    {data.roles.find((role) => role.id === binding.role_id)
                      ?.name ?? binding.role_id}
                  </TableCell>
                  <TableCell>
                    {data.members.find(
                      (member) => member.subject === binding.principal_id
                    )?.display_name || binding.principal_id}
                  </TableCell>
                  <TableCell>
                    {t(binding.effect === "allow" ? "allow" : "deny")}
                  </TableCell>
                  <TableCell>
                    {binding.expires_at
                      ? new Date(binding.expires_at).toLocaleString(locale)
                      : t("neverExpires")}
                  </TableCell>
                </TableRow>
              ))}
              {data.bindings.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4}>{t("noEntityRoles")}</TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      )}
      {view === "resources" && (
        <div className="overflow-x-auto border-y">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("resourceType")}</TableHead>
                <TableHead>{t("resourceId")}</TableHead>
                <TableHead>{t("grantRelation")}</TableHead>
                <TableHead>{t("grantRecipient")}</TableHead>
                <TableHead>{t("type")}</TableHead>
                <TableHead>{t("startTime")}</TableHead>
                <TableHead>{t("endTime")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.relationships.map((relationship) => (
                <TableRow key={JSON.stringify(relationship)}>
                  <TableCell>{relationship.resource_type}</TableCell>
                  <TableCell className="break-all">
                    {relationship.resource_id}
                  </TableCell>
                  <TableCell>{t(relationship.relation)}</TableCell>
                  <TableCell className="break-all">
                    {relationship.subject_id}
                  </TableCell>
                  <TableCell>{t(relationship.subject_type)}</TableCell>
                  <TableCell>
                    {relationship.starts_at
                      ? new Date(relationship.starts_at).toLocaleString(locale)
                      : "—"}
                  </TableCell>
                  <TableCell>
                    {relationship.ends_at
                      ? new Date(relationship.ends_at).toLocaleString(locale)
                      : t("neverExpires")}
                  </TableCell>
                </TableRow>
              ))}
              {data.relationships.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7}>{t("noRelations")}</TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      )}
    </>
  )
}
