import {
  AutoForm,
  type UseAutoFormReturn,
  type FieldRendererProps,
} from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useMemo, useState, useRef } from "react"
import {
  Building2,
  ChevronDown,
  ChevronRight,
  KeyRound,
  Pencil,
  Plus,
  ScanSearch,
  Trash2,
} from "lucide-react"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Input } from "@workspace/ui/components/input"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import {
  iamApi,
  type AuthorizationExplanation,
  type DataConstraint,
  type Entity,
  type EntityInput,
  type EntityRoleBinding,
  type Group,
  type Member,
  type Permission,
  type Relationship,
  type RelationshipRelation,
  type RelationshipSubjectType,
  type Role,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import {
  trustedContextCondition,
  trustedContextConditionSummary,
} from "../../lib/policy-condition"

const rootEntity = "__root__"

async function loadRelationships(entityID: string) {
  return (
    await Promise.all([
      iamApi.relationships(entityID),
      iamApi.resourceRelationships(entityID),
    ])
  ).flat()
}

export default function EntitiesPage({
  scopeEntityID = "",
}: {
  scopeEntityID?: string
}) {
  const locale = useLocale()

  const t = useTranslations("iam")
  const allowed = useAllowed()

  const bindingForm = useRef<UseAutoFormReturn>(null)
  const inspectionForm = useRef<UseAutoFormReturn>(null)
  const relationshipForm = useRef<UseAutoFormReturn>(null)
  const [entities, setEntities] = useState<Entity[]>([])
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Entity | null>(null)
  const [parentID, setParentID] = useState(rootEntity)
  const [status, setStatus] = useState<EntityInput["status"]>("active")
  const [bindingEntity, setBindingEntity] = useState<Entity | null>(null)
  const [bindings, setBindings] = useState<EntityRoleBinding[]>([])
  const [relationships, setRelationships] = useState<Relationship[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const [members, setMembers] = useState<Member[]>([])
  const [groups, setGroups] = useState<Group[]>([])

  const [permissions, setPermissions] = useState<Permission[]>([])
  const [roleID, setRoleID] = useState("")
  const [principalID, setPrincipalID] = useState("")
  const [effect, setEffect] = useState<EntityRoleBinding["effect"]>("allow")
  const [expiresAt, setExpiresAt] = useState("")
  const [relationshipSubjectType, setRelationshipSubjectType] =
    useState<RelationshipSubjectType>("principal")
  const [relationshipSubjectID, setRelationshipSubjectID] = useState("")
  const [relationshipSubjectRelation, setRelationshipSubjectRelation] =
    useState<RelationshipRelation>("owner")
  const [relationshipRelation, setRelationshipRelation] =
    useState<RelationshipRelation>("viewer")
  const [relationshipResourceType, setRelationshipResourceType] = useState("")
  const [relationshipResourceID, setRelationshipResourceID] = useState("")
  const [relationshipStartsAt, setRelationshipStartsAt] = useState("")
  const [relationshipEndsAt, setRelationshipEndsAt] = useState("")
  const [relationshipMinimumAcr, setRelationshipMinimumAcr] = useState("")
  const [relationshipClientID, setRelationshipClientID] = useState("")
  const [relationshipNetworkZone, setRelationshipNetworkZone] = useState("")
  const [relationshipTimeStart, setRelationshipTimeStart] = useState("")
  const [relationshipTimeEnd, setRelationshipTimeEnd] = useState("")
  const [relationshipTimezone, setRelationshipTimezone] = useState(
    () => Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"
  )
  const [inspectionPermission, setInspectionPermission] = useState("")
  const [inspectionResourceID, setInspectionResourceID] = useState("")
  const [inspection, setInspection] = useState<{
    constraint: DataConstraint
    explanation: AuthorizationExplanation
  } | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState("")

  const refresh = useCallback(
    () =>
      iamApi.entities().then((items) => {
        setEntities(items)
        setLoaded(true)
        setLoadError("")
        window.dispatchEvent(new Event("entity-catalog-change"))
      }),
    []
  )

  useEffect(() => {
    void refresh().catch((cause: Error) => setLoadError(cause.message))
  }, [refresh])

  const entityByID = useMemo(
    () => new Map(entities.map((entity) => [entity.id, entity])),
    [entities]
  )
  const tree = useMemo(
    () =>
      scopeEntityID
        ? entities
            .filter((entity) => entity.id === scopeEntityID)
            .map((entity) => ({ entity, depth: 0 }))
        : flattenEntities(entities, collapsed),
    [collapsed, entities, scopeEntityID]
  )
  const childCounts = useMemo(() => {
    const counts = new Map<string, number>()
    for (const entity of entities) {
      if (entity.parent_id)
        counts.set(entity.parent_id, (counts.get(entity.parent_id) ?? 0) + 1)
    }
    return counts
  }, [entities])
  const parentOptions = useMemo(
    () => [
      { value: rootEntity, label: t("topEntity") },
      ...flattenEntities(entities, new Set())
        .filter(
          ({ entity }) =>
            entity.id !== editing?.id &&
            !isDescendantOf(entity, editing?.id, entityByID)
        )
        .map(({ entity, depth }) => ({
          value: entity.id,
          label: `${"　".repeat(depth)}${entity.name}`,
        })),
    ],
    [editing?.id, entities, entityByID, t]
  )
  const roleByID = useMemo(
    () => new Map(roles.map((role) => [role.id, role])),
    [roles]
  )
  const memberByID = useMemo(
    () => new Map(members.map((member) => [member.subject, member])),
    [members]
  )
  const groupByID = useMemo(
    () => new Map(groups.map((group) => [group.id, group])),
    [groups]
  )

  const resourceTypeOptions = useMemo(
    () =>
      [...new Set(permissions.map((permission) => permission.resource))].map(
        (resource) => ({ value: resource, label: resource })
      ),
    [permissions]
  )
  const relationshipSubjectOptions = useMemo(() => {
    switch (relationshipSubjectType) {
      case "group":
        return groups.map((group) => ({ value: group.id, label: group.name }))

      case "entity":
        return entities
          .filter(
            (entity) =>
              entity.status === "active" && entity.id !== bindingEntity?.id
          )
          .map((entity) => ({ value: entity.id, label: entity.name }))
      default:
        return members.map((member) => ({
          value: member.subject,
          label: member.display_name || member.subject,
        }))
    }
  }, [bindingEntity?.id, entities, groups, members, relationshipSubjectType])

  const openCreate = () => {
    setError("")
    setEditing(null)
    setParentID(rootEntity)
    setStatus("active")
    setOpen(true)
  }

  const openEdit = (entity: Entity) => {
    setError("")
    setEditing(entity)
    setParentID(entity.parent_id || rootEntity)
    setStatus(entity.status)
    setOpen(true)
  }

  const submit = async (data: Record<string, unknown>) => {
    setBusy(true)
    setError("")
    try {
      const input: EntityInput = {
        parent_id: parentID === rootEntity ? "" : parentID,
        type: String(data.type),
        name: String(data.name),
        status,
        metadata: parseMetadata(String(data.metadata || "{}")),
      }
      if (editing) await iamApi.updateEntity(editing.id, input)
      else await iamApi.createEntity(input)
      setOpen(false)
      setEditing(null)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const remove = async (entity: Entity) => {
    setBusy(true)
    setError("")
    try {
      await iamApi.deleteEntity(entity.id)
      setCollapsed((current) => {
        const next = new Set(current)
        next.delete(entity.id)
        return next
      })
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const openBindings = async (entity: Entity) => {
    setBusy(true)
    setError("")
    try {
      const [
        nextBindings,
        nextRelationships,
        nextRoles,
        nextMembers,
        nextGroups,
        nextPermissions,
      ] = await Promise.all([
        iamApi.entityRoleBindings(entity.id),
        loadRelationships(entity.id),
        allowed("role_view") ? iamApi.roles() : Promise.resolve([]),
        allowed("user_view") ? iamApi.members() : Promise.resolve([]),
        allowed("group_view") ? iamApi.groups() : Promise.resolve([]),
        allowed("role_view") ? iamApi.permissions() : Promise.resolve([]),
      ])
      const activeMembers = nextMembers.filter(
        (member) => member.status === "active"
      )
      const dataPermissions = nextPermissions.filter(
        (permission) => permission.data_scoped
      )
      setBindingEntity(entity)
      setBindings(nextBindings)
      setRelationships(nextRelationships)
      setRoles(nextRoles)
      setMembers(activeMembers)
      setGroups(nextGroups.filter((group) => group.status === "active"))

      setPermissions(dataPermissions)
      setRoleID(nextRoles[0]?.id ?? "")
      setPrincipalID(activeMembers[0]?.subject ?? "")
      setEffect("allow")
      setExpiresAt("")
      setRelationshipSubjectType("principal")
      setRelationshipSubjectID(activeMembers[0]?.subject ?? "")
      setRelationshipSubjectRelation("owner")
      setRelationshipRelation("viewer")
      setRelationshipResourceType(dataPermissions[0]?.resource ?? "")
      setRelationshipResourceID("")
      setRelationshipStartsAt("")
      setRelationshipEndsAt("")
      setRelationshipMinimumAcr("")
      setRelationshipClientID("")
      setRelationshipNetworkZone("")
      setRelationshipTimeStart("")
      setRelationshipTimeEnd("")
      setInspectionPermission(dataPermissions[0]?.code ?? "")
      setInspectionResourceID("")
      setInspection(null)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const putRelationship = async () => {
    if (!bindingEntity || !relationshipSubjectID) return
    setBusy(true)
    setError("")
    try {
      const resourceID = relationshipResourceID.trim()
      await iamApi.putRelationship({
        entity_id: resourceID ? bindingEntity.id : undefined,
        subject_type: relationshipSubjectType,
        subject_id: relationshipSubjectID,
        subject_relation:
          relationshipSubjectType === "principal"
            ? undefined
            : relationshipSubjectType === "entity"
              ? relationshipSubjectRelation
              : "member",
        relation: relationshipRelation,
        resource_type: resourceID
          ? relationshipResourceType
          : bindingEntity.type,
        resource_id: resourceID || bindingEntity.id,
        starts_at: relationshipStartsAt
          ? new Date(relationshipStartsAt).toISOString()
          : undefined,
        ends_at: relationshipEndsAt
          ? new Date(relationshipEndsAt).toISOString()
          : undefined,
        condition: trustedContextCondition(
          relationshipMinimumAcr,
          relationshipClientID,
          relationshipNetworkZone,
          relationshipTimeStart,
          relationshipTimeEnd,
          relationshipTimezone
        ),
      })
      setRelationships(await loadRelationships(bindingEntity.id))
      for (const field of [
        "starts_at",
        "ends_at",
        "minimum_acr",
        "client_id",
        "network_zone",
        "time_start",
        "time_end",
      ])
        relationshipForm.current?.setValue(field, "")
      setRelationshipStartsAt("")
      setRelationshipEndsAt("")
      setRelationshipMinimumAcr("")
      setRelationshipClientID("")
      setRelationshipNetworkZone("")
      setRelationshipTimeStart("")
      setRelationshipTimeEnd("")
      setInspection(null)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const removeRelationship = async (relationship: Relationship) => {
    if (!bindingEntity) return
    setBusy(true)
    setError("")
    try {
      await iamApi.deleteRelationship(relationship)
      setRelationships(await loadRelationships(bindingEntity.id))
      setInspection(null)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const relationshipSubjectLabel = (relationship: Relationship) => {
    switch (relationship.subject_type) {
      case "group":
        return (
          groupByID.get(relationship.subject_id)?.name ??
          relationship.subject_id
        )

      case "entity":
        return (
          entityByID.get(relationship.subject_id)?.name ??
          relationship.subject_id
        )
      default:
        return (
          memberByID.get(relationship.subject_id)?.display_name ??
          relationship.subject_id
        )
    }
  }

  const putBinding = async () => {
    if (!bindingEntity || !roleID || !principalID) return
    setBusy(true)
    setError("")
    try {
      await iamApi.putEntityRoleBinding(bindingEntity.id, roleID, principalID, {
        effect,
        expires_at: expiresAt ? new Date(expiresAt).toISOString() : undefined,
      })
      setBindings(await iamApi.entityRoleBindings(bindingEntity.id))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const removeBinding = async (binding: EntityRoleBinding) => {
    if (!bindingEntity) return
    setBusy(true)
    setError("")
    try {
      await iamApi.deleteEntityRoleBinding(
        bindingEntity.id,
        binding.role_id,
        binding.principal_id
      )
      setBindings(await iamApi.entityRoleBindings(bindingEntity.id))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const inspectAuthorization = async () => {
    if (!bindingEntity || !principalID || !inspectionPermission) return
    setBusy(true)
    setError("")
    try {
      const resourceID = inspectionResourceID.trim()
      const resourceType = permissions.find(
        (permission) => permission.code === inspectionPermission
      )?.resource
      const [constraint, explanation] = await Promise.all([
        iamApi.authorizationConstraint(inspectionPermission, principalID),
        resourceID && resourceType
          ? iamApi.explainResourceAuthorization(
              bindingEntity.id,
              resourceType,
              resourceID,
              inspectionPermission,
              principalID
            )
          : iamApi.explainEntityAuthorization(
              bindingEntity.id,
              inspectionPermission,
              principalID
            ),
      ])
      setInspection({ constraint, explanation })
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const toggleCollapsed = (id: string) => {
    setCollapsed((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      <PageHeader
        title={t(scopeEntityID ? "entitySettings" : "entityManagement")}
        description={t("entitiesDescription")}
      />
      {error && !open && !bindingEntity && <Alert>{error}</Alert>}
      {loadError ? (
        <Alert>
          <p>{t.has(loadError) ? t(loadError) : loadError}</p>
          <Button
            variant="outline"
            onClick={() =>
              void refresh().catch((cause: Error) =>
                setLoadError(cause.message)
              )
            }
          >
            {t("retryNow")}
          </Button>
        </Alert>
      ) : !loaded ? (
        <p role="status">{t("loading")}</p>
      ) : (
        <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-md border bg-card">
          <AutoTable
            fill
            onRefresh={refresh}
            searchable={({ entity }) =>
              [entity.name, entity.id, entity.type].join(" ")
            }
            toolbar={
              <div className="flex min-w-0 flex-1 flex-wrap items-end gap-3">
                {!scopeEntityID &&
                  loaded &&
                  !loadError &&
                  allowed("entity_create") && (
                    <Button onClick={openCreate}>
                      <Plus />
                      {t("createEntity")}
                    </Button>
                  )}
              </div>
            }
            footer={
              <>
                <span className="text-sm text-muted-foreground">
                  {tree.length} {t("items")}
                </span>
              </>
            }
            className="[&_table]:min-w-[900px]"
            emptyContent={
              tree.length === 0 && (
                <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
                  <span className="text-center">
                    <Building2 className="mx-auto mb-2 size-7" />
                    {t(scopeEntityID ? "entityUnavailable" : "noEntities")}
                  </span>
                </div>
              )
            }
            pagination={false}
            data={tree}
            rowKey={(row) => row.entity.id}
            columns={[
              {
                field: "entity",
                name: t("entity"),
                type: "custom" as const,
                render: (_value, { entity, depth }) => {
                  const hasChildren =
                    !scopeEntityID && (childCounts.get(entity.id) ?? 0) > 0
                  return (
                    <div>
                      <div
                        className="flex min-w-0 items-center gap-1"
                        style={{ paddingInlineStart: `${depth * 24}px` }}
                      >
                        {hasChildren ? (
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-8 shrink-0"
                            onClick={() => toggleCollapsed(entity.id)}
                            aria-label={`${collapsed.has(entity.id) ? t("expand") : t("collapseShort")}${entity.name}`}
                            title={
                              collapsed.has(entity.id)
                                ? t("expandChildEntities")
                                : t("collapseChildEntities")
                            }
                          >
                            {collapsed.has(entity.id) ? (
                              <ChevronRight />
                            ) : (
                              <ChevronDown />
                            )}
                          </Button>
                        ) : (
                          <span className="block size-8 shrink-0" />
                        )}
                        <Building2 className="size-4 shrink-0 text-primary" />
                        <strong className="truncate text-sm">
                          {entity.name}
                        </strong>
                      </div>
                    </div>
                  )
                },
              },
              {
                field: "type",
                name: t("type"),
                type: "custom" as const,
                render: (_value, { entity }) => {
                  return (
                    <div>
                      <Badge variant="outline">{entity.type}</Badge>
                    </div>
                  )
                },
              },
              {
                field: "parentEntity",
                name: t("parentEntity"),
                type: "custom" as const,
                render: (_value, { entity }) => {
                  return (
                    <div className="text-sm text-muted-foreground">
                      {entity.parent_id
                        ? (entityByID.get(entity.parent_id)?.name ??
                          entity.parent_id)
                        : "-"}
                    </div>
                  )
                },
              },
              {
                field: "status",
                name: t("status"),
                type: "custom" as const,
                render: (_value, { entity }) => {
                  return (
                    <div>
                      <Badge
                        variant={
                          entity.status === "active"
                            ? "secondary"
                            : "destructive"
                        }
                      >
                        {entity.status === "active"
                          ? t("active")
                          : t("disable")}
                      </Badge>
                    </div>
                  )
                },
              },
              {
                field: "metadata",
                name: t("metadata"),
                type: "custom" as const,
                render: (_value, { entity }) => {
                  return (
                    <div className="max-w-72 truncate font-mono text-xs text-muted-foreground">
                      {metadataSummary(entity.metadata)}
                    </div>
                  )
                },
              },
              {
                field: "actions",
                name: t("actions"),
                type: "actions" as const,
                render: (_value, { entity }) => {
                  return (
                    <div className="text-right">
                      <div className="flex justify-end gap-1">
                        {allowed("entity_manage_binding") && (
                          <Button
                            variant="ghost"
                            size="icon"
                            disabled={busy}
                            onClick={() => void openBindings(entity)}
                            aria-label={t("manageRoleBindings", {
                              name: entity.name,
                            })}
                            title={t("roleBindings")}
                          >
                            <KeyRound />
                          </Button>
                        )}
                        {allowed("entity_update") && (
                          <Button
                            variant="ghost"
                            size="icon"
                            disabled={busy}
                            onClick={() => openEdit(entity)}
                            aria-label={t("editNamed", { name: entity.name })}
                            title={t("editEntity")}
                          >
                            <Pencil />
                          </Button>
                        )}
                        {allowed("entity_delete") && (
                          <Button
                            variant="ghost"
                            size="icon"
                            disabled={busy}
                            onClick={() => void remove(entity)}
                            aria-label={t("deleteNamed", { name: entity.name })}
                            title={t("deleteEmptyEntity")}
                          >
                            <Trash2 />
                          </Button>
                        )}
                      </div>
                    </div>
                  )
                },
              },
            ]}
          />
        </div>
      )}

      <Dialog
        open={open}
        onOpenChange={(nextOpen) => {
          setOpen(nextOpen)
          if (!nextOpen) setEditing(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editing ? t("editEntity") : t("createEntity")}
            </DialogTitle>
            <DialogDescription>{t("entityFormDescription")}</DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <AutoForm
            key={editing?.id ?? "create"}
            mode={editing ? "edit" : "add"}
            id="entity-form"
            fields={{
              name: {
                name: t("name"),
                type: "text",
                value: editing?.name ?? "",
                required: true,
                config: { id: "entity-name", maxLength: 200 },
              },
              type: {
                name: t("type"),
                type: "text",
                value: editing?.type ?? "",
                required: true,
                config: {
                  id: "entity-type",
                  pattern: "[a-z][a-z0-9_-]{0,63}",
                  placeholder: "company",
                  maxLength: 64,
                },
              },
              parent_id: {
                name: t("parentEntity"),
                type: "select",
                value: parentID,
                config: { id: "entity-parent", options: parentOptions },
              },
              status: {
                name: t("status"),
                type: "select",
                value: status,
                config: {
                  id: "entity-status",
                  options: { active: t("active"), disabled: t("disable") },
                },
              },
              metadata: {
                name: t("metadataJson"),
                type: "textarea",
                value: JSON.stringify(editing?.metadata ?? {}, null, 2),
                config: {
                  id: "entity-metadata",
                  rows: 5,
                  spellCheck: false,
                  className: "font-mono text-xs",
                },
              },
            }}
            onSubmit={submit}
            onChange={(values) => {
              setStatus(values.status as EntityInput["status"])
              setParentID(String(values.parent_id))
            }}
          ></AutoForm>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {t("cancel")}
            </Button>
            <Button type="submit" form="entity-form" disabled={busy}>
              {busy ? t("saving") : editing ? t("save") : t("create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={Boolean(bindingEntity)}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) {
            setBindingEntity(null)
            setInspection(null)
          }
        }}
      >
        <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-4xl">
          <DialogHeader>
            <DialogTitle className="pr-8 break-all">
              {bindingEntity?.name} {t("entityGrantsSuffix")}
            </DialogTitle>
            <DialogDescription>
              {t("entityGrantsDescription")}
            </DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <AutoForm
            formRef={bindingForm}
            id="entity-binding-form"
            fields={{
              role_id: {
                name: t("role"),
                type: "select",
                value: roleID,
                config: {
                  id: "entity-binding-role",
                  options: roles.map((role) => ({
                    value: role.id,
                    label: role.name,
                  })),
                  placeholder: t("noAvailableRoles"),
                },
              },
              principal_id: {
                name: t("member"),
                type: "select",
                value: principalID,
                config: {
                  id: "entity-binding-principal",
                  options: members.map((member) => ({
                    value: member.subject,
                    label: member.display_name || member.subject,
                  })),
                  placeholder: t("noActiveMembers"),
                },
              },
              effect: {
                name: t("effect"),
                type: "select",
                value: effect,
                config: {
                  id: "entity-binding-effect",
                  options: { allow: t("allow"), deny: t("deny") },
                },
              },
              expires_at: {
                name: t("expiresAt"),
                type: "custom",
                component: LocalDateTimeField,
                value: expiresAt,
                col: 1,
                config: { id: "entity-binding-expiry" },
              },
            }}
            onSubmit={putBinding}
            onChange={(values) => {
              setRoleID(String(values.role_id))
              if (String(values.principal_id) !== principalID) {
                setPrincipalID(String(values.principal_id))
                inspectionForm.current?.setValue(
                  "principal_id",
                  String(values.principal_id)
                )
              }
              setEffect(values.effect as EntityRoleBinding["effect"])
              setExpiresAt(String(values.expires_at))
            }}
          ></AutoForm>
          <div className="overflow-hidden rounded-md border">
            <AutoTable
              searchable={(binding) =>
                [
                  binding.role_id,
                  binding.principal_id,
                  roles.find((role) => role.id === binding.role_id)?.name,
                  members.find(
                    (member) => member.subject === binding.principal_id
                  )?.display_name,
                  binding.effect,
                ].join(" ")
              }
              onRefresh={async () => {
                if (bindingEntity)
                  setBindings(await iamApi.entityRoleBindings(bindingEntity.id))
              }}
              sticky={{ maxHeight: "20rem" }}
              className="[&_table]:min-w-[720px]"
              emptyContent={null}
              data={bindings}
              rowKey={(binding) => `${binding.role_id}:${binding.principal_id}`}
              columns={[
                {
                  field: "role",
                  name: t("role"),
                  type: "custom",
                  render: (_value, binding) => (
                    <div>
                      {roleByID.get(binding.role_id)?.name ?? binding.role_id}
                    </div>
                  ),
                },
                {
                  field: "member",
                  name: t("member"),
                  type: "custom",
                  render: (_value, binding) => (
                    <div>
                      {memberByID.get(binding.principal_id)?.display_name ??
                        binding.principal_id}
                    </div>
                  ),
                },
                {
                  field: "effect",
                  name: t("effect"),
                  type: "custom",
                  render: (_value, binding) => (
                    <div>
                      <Badge
                        variant={
                          binding.effect === "allow"
                            ? "secondary"
                            : "destructive"
                        }
                      >
                        {binding.effect === "allow" ? t("allow") : t("deny")}
                      </Badge>
                    </div>
                  ),
                },
                {
                  field: "expiresAt",
                  name: t("expiresAt"),
                  type: "custom",
                  render: (_value, binding) => (
                    <div className="text-sm text-muted-foreground">
                      {binding.expires_at
                        ? new Date(binding.expires_at).toLocaleString(locale)
                        : t("noExpiry")}
                    </div>
                  ),
                },
                {
                  field: "actions",
                  name: t("actions"),
                  type: "actions",
                  render: (_value, binding) => (
                    <div className="text-right">
                      {allowed("entity_manage_binding") && (
                        <Button
                          variant="ghost"
                          size="icon"
                          disabled={busy}
                          onClick={() => void removeBinding(binding)}
                          aria-label={t("deleteRoleBinding")}
                          title={t("deleteRoleBinding")}
                        >
                          <Trash2 />
                        </Button>
                      )}
                    </div>
                  ),
                },
              ]}
            />
            {bindings.length === 0 && (
              <div className="grid min-h-24 place-items-center text-sm text-muted-foreground">
                {t("noDirectBindings")}
              </div>
            )}
          </div>
          <section
            className="border-t pt-5"
            aria-labelledby="relationship-title"
          >
            <h3 id="relationship-title" className="mb-3 text-sm font-semibold">
              {t("relationshipGrants")}
            </h3>
            <AutoForm
              formRef={relationshipForm}
              id="entity-relationship-form"
              fields={{
                subject_type: {
                  name: t("principalType"),
                  type: "select",
                  value: relationshipSubjectType,
                  config: {
                    id: "relationship-subject-type",
                    options: {
                      principal: t("member"),
                      group: t("groups"),
                      entity: t("entity"),
                    },
                  },
                },
                subject_id: {
                  name: t("principal"),
                  type: "select",
                  value: relationshipSubjectID,
                  config: {
                    id: "relationship-subject",
                    options: relationshipSubjectOptions,
                    placeholder: t("noPrincipalsAvailable"),
                  },
                },
                subject_relation: {
                  name: t("subjectRelation"),
                  type: "select",
                  value: relationshipSubjectRelation,
                  show: (values) => values?.subject_type === "entity",
                  config: {
                    id: "relationship-subject-relation",
                    options: {
                      owner: t("owner"),
                      editor: t("editor"),
                      viewer: t("viewer"),
                    },
                  },
                },
                direct_subject_relation: {
                  name: t("subjectRelation"),
                  type: "text",
                  value:
                    relationshipSubjectType === "principal"
                      ? t("directPrincipal")
                      : "member",
                  disabled: true,
                  show: (values) => values?.subject_type !== "entity",
                  config: {
                    placeholder:
                      relationshipSubjectType === "principal"
                        ? t("directPrincipal")
                        : "member",
                  },
                },
                relation: {
                  name: t("grantRelation"),
                  type: "select",
                  value: relationshipRelation,
                  config: {
                    id: "relationship-relation",
                    options: {
                      owner: t("owner"),
                      editor: t("editor"),
                      viewer: t("viewer"),
                    },
                  },
                },
                resource_type: {
                  name: t("businessResourceType"),
                  type: "select",
                  value: relationshipResourceType,
                  disabled: (values) => !values?.resource_id,
                  config: {
                    id: "relationship-resource-type",
                    options: resourceTypeOptions,
                    placeholder: t("selectType"),
                  },
                },
                resource_id: {
                  name: t("businessResourceId"),
                  type: "text",
                  value: relationshipResourceID,
                  config: {
                    id: "relationship-resource-id",
                    placeholder: t("blankCurrentEntity"),
                    maxLength: 255,
                  },
                },
                starts_at: {
                  name: t("effectiveAt"),
                  type: "custom",
                  component: LocalDateTimeField,
                  value: relationshipStartsAt,
                  col: 1,
                  config: { id: "relationship-starts-at" },
                },
                ends_at: {
                  name: t("expiresAt"),
                  type: "custom",
                  component: LocalDateTimeField,
                  value: relationshipEndsAt,
                  col: 1,
                  config: { id: "relationship-ends-at" },
                },
                minimum_acr: {
                  name: t("minimumAcr"),
                  type: "number",
                  value: relationshipMinimumAcr,
                  config: {
                    id: "relationship-minimum-acr",
                    min: 1,
                    max: 99,
                    placeholder: t("unrestricted"),
                  },
                },
                client_id: {
                  name: t("oauthClients"),
                  type: "text",
                  value: relationshipClientID,
                  config: {
                    id: "relationship-client-id",
                    maxLength: 255,
                    placeholder: t("unrestricted"),
                  },
                },
                network_zone: {
                  name: t("networkZone"),
                  type: "text",
                  value: relationshipNetworkZone,
                  config: {
                    id: "relationship-network-zone",
                    maxLength: 255,
                    placeholder: t("unrestricted"),
                  },
                },
                time_start: {
                  name: t("dailyStart"),
                  type: "time",
                  value: relationshipTimeStart,
                  required: (values) => Boolean(values?.time_end),
                  config: { id: "relationship-time-start" },
                },
                time_end: {
                  name: t("dailyEnd"),
                  type: "time",
                  value: relationshipTimeEnd,
                  required: (values) => Boolean(values?.time_start),
                  config: { id: "relationship-time-end" },
                },
                timezone: {
                  name: t("timezone"),
                  type: "text",
                  value: relationshipTimezone,
                  required: (values) =>
                    Boolean(values?.time_start || values?.time_end),
                  config: { id: "relationship-timezone", maxLength: 64 },
                },
              }}
              onSubmit={putRelationship}
              onChange={(values) => {
                if (values.subject_type !== relationshipSubjectType) {
                  setRelationshipSubjectType(
                    values.subject_type as RelationshipSubjectType
                  )
                  setRelationshipSubjectID("")
                  relationshipForm.current?.setValue("subject_id", "")
                  relationshipForm.current?.setValue(
                    "direct_subject_relation",
                    values.subject_type === "principal"
                      ? t("directPrincipal")
                      : "member"
                  )
                } else {
                  setRelationshipSubjectID(String(values.subject_id))
                }
                setRelationshipSubjectRelation(
                  values.subject_relation as RelationshipRelation
                )
                setRelationshipRelation(values.relation as RelationshipRelation)
                setRelationshipResourceType(String(values.resource_type))
                setRelationshipResourceID(String(values.resource_id))
                setRelationshipStartsAt(String(values.starts_at))
                setRelationshipEndsAt(String(values.ends_at))
                setRelationshipMinimumAcr(String(values.minimum_acr ?? ""))
                setRelationshipClientID(String(values.client_id))
                setRelationshipNetworkZone(String(values.network_zone))
                setRelationshipTimeStart(String(values.time_start))
                setRelationshipTimeEnd(String(values.time_end))
                setRelationshipTimezone(String(values.timezone))
              }}
            >
              <Button
                type="submit"
                className="md:col-span-2 xl:col-span-4 xl:justify-self-end"
                disabled={
                  busy ||
                  !relationshipSubjectID ||
                  (Boolean(relationshipResourceID.trim()) &&
                    !relationshipResourceType)
                }
              >
                <Plus />
                {t("grantRelation")}
              </Button>
            </AutoForm>
            <div className="mt-3 overflow-hidden rounded-md border">
              <AutoTable
                searchable={(relationship) =>
                  [
                    relationship.resource_type,
                    relationship.resource_id,
                    relationship.relation,
                    relationshipSubjectLabel(relationship),
                    relationship.subject_id,
                  ].join(" ")
                }
                onRefresh={async () => {
                  if (bindingEntity)
                    setRelationships(await loadRelationships(bindingEntity.id))
                }}
                sticky={{ maxHeight: "20rem" }}
                className="[&_table]:min-w-[1160px]"
                emptyContent={null}
                data={relationships}
                rowKey={(relationship) =>
                  `${relationship.entity_id}:${relationship.subject_type}:${relationship.subject_id}:${relationship.subject_relation}:${relationship.relation}:${relationship.resource_type}:${relationship.resource_id}`
                }
                columns={[
                  {
                    field: "subject_type",
                    name: t("principalType"),
                    type: "text",
                  },
                  {
                    field: "principal",
                    name: t("principal"),
                    type: "custom",
                    render: (_value, relationship) => (
                      <div>{relationshipSubjectLabel(relationship)}</div>
                    ),
                  },
                  {
                    field: "subjectRelation",
                    name: t("subjectRelation"),
                    type: "custom",
                    render: (_value, relationship) => (
                      <div>{relationship.subject_relation || t("direct")}</div>
                    ),
                  },
                  {
                    field: "grantRelation",
                    name: t("grantRelation"),
                    type: "custom",
                    render: (_value, relationship) => (
                      <div>
                        <Badge variant="secondary">
                          {relationship.relation}
                        </Badge>
                      </div>
                    ),
                  },
                  {
                    field: "target",
                    name: t("target"),
                    type: "custom",
                    render: (_value, relationship) => (
                      <div className="max-w-64 font-mono text-xs break-all text-muted-foreground">
                        {relationship.entity_id
                          ? `${relationship.resource_type}:${relationship.resource_id}`
                          : (entityByID.get(relationship.resource_id)?.name ??
                            `${relationship.resource_type}:${relationship.resource_id}`)}
                      </div>
                    ),
                  },
                  {
                    field: "validity",
                    name: t("validity"),
                    type: "custom",
                    render: (_value, relationship) => (
                      <div
                        className="text-xs whitespace-nowrap text-muted-foreground"
                        data-relationship-window
                      >
                        {relationship.starts_at
                          ? new Date(relationship.starts_at).toLocaleString(
                              locale
                            )
                          : t("immediately")}
                        {t("rangeTo")}
                        {relationship.ends_at
                          ? new Date(relationship.ends_at).toLocaleString(
                              locale
                            )
                          : t("noExpiry")}
                      </div>
                    ),
                  },
                  {
                    field: "condition",
                    name: t("condition"),
                    type: "custom",
                    render: (_value, relationship) => (
                      <div
                        className="max-w-72 text-xs text-muted-foreground"
                        data-relationship-condition
                      >
                        {trustedContextConditionSummary(
                          relationship.condition,
                          t("unrestricted")
                        )}
                      </div>
                    ),
                  },
                  {
                    field: "actions",
                    name: t("actions"),
                    type: "actions",
                    render: (_value, relationship) => (
                      <div className="text-right">
                        {allowed("entity_manage_binding") && (
                          <Button
                            variant="ghost"
                            size="icon"
                            disabled={busy}
                            onClick={() =>
                              void removeRelationship(relationship)
                            }
                            aria-label={t("revokeRelation")}
                            title={t("revokeRelation")}
                          >
                            <Trash2 />
                          </Button>
                        )}
                      </div>
                    ),
                  },
                ]}
              />
              {relationships.length === 0 && (
                <div className="grid min-h-20 place-items-center text-sm text-muted-foreground">
                  {t("noRelations")}
                </div>
              )}
            </div>
          </section>
          <section
            className="border-t pt-5"
            aria-labelledby="authorization-inspection-title"
          >
            <h3
              id="authorization-inspection-title"
              className="mb-3 flex items-center gap-2 text-sm font-semibold"
            >
              <ScanSearch className="size-4 text-primary" />
              {t("authorizationExplanation")}
            </h3>
            <AutoForm
              formRef={inspectionForm}
              fields={{
                principal_id: {
                  name: t("member"),
                  type: "select",
                  value: principalID,
                  config: {
                    id: "authorization-inspection-principal",
                    options: members.map((member) => ({
                      value: member.subject,
                      label: member.display_name || member.subject,
                    })),
                    placeholder: t("noActiveMembers"),
                  },
                },
                permission: {
                  name: t("dataPermission"),
                  type: "select",
                  value: inspectionPermission,
                  config: {
                    id: "authorization-inspection-permission",
                    options: permissions.map((permission) => ({
                      value: permission.code,
                      label: `${permission.code} · ${permission.summary}`,
                    })),
                    placeholder: t("noDataPermission"),
                  },
                },
                resource_id: {
                  name: t("businessResourceId"),
                  type: "text",
                  value: inspectionResourceID,
                  config: {
                    id: "authorization-inspection-resource",
                    maxLength: 255,
                    placeholder: t("blankCheckCurrentEntity"),
                  },
                },
              }}
              onChange={(values) => {
                if (
                  String(values.principal_id) !== principalID ||
                  String(values.permission) !== inspectionPermission ||
                  String(values.resource_id) !== inspectionResourceID
                ) {
                  setPrincipalID(String(values.principal_id))
                  bindingForm.current?.setValue(
                    "principal_id",
                    String(values.principal_id)
                  )
                  setInspectionPermission(String(values.permission))
                  setInspectionResourceID(String(values.resource_id))
                  setInspection(null)
                }
              }}
              onSubmit={async () => {
                if (
                  allowed("role_view") &&
                  !busy &&
                  principalID &&
                  inspectionPermission
                )
                  await inspectAuthorization()
              }}
            >
              {allowed("role_view") && (
                <Button
                  type="submit"
                  variant="outline"
                  disabled={busy || !principalID || !inspectionPermission}
                >
                  <ScanSearch />
                  {t("evaluateAuthorization")}
                </Button>
              )}
            </AutoForm>
            {inspection && (
              <div className="mt-4" data-authorization-explanation>
                <div className="mb-3 flex flex-wrap items-center gap-2 text-sm">
                  <Badge
                    variant={
                      inspection.explanation.allowed
                        ? "secondary"
                        : "destructive"
                    }
                  >
                    {inspection.explanation.allowed ? t("allow") : t("deny")}
                  </Badge>
                  <span>
                    {t(authorizationReason(inspection.explanation.reason))}
                  </span>
                  <span className="text-muted-foreground">
                    {t("policyVersion")}
                    {inspection.explanation.revision}
                  </span>
                  <span className="text-muted-foreground">
                    {inspection.constraint.allow_all
                      ? t("allTenantEntitiesExcept", {
                          denied: inspection.constraint.denied_ids.length,
                        })
                      : t("allowedDeniedEntities", {
                          allowed: inspection.constraint.resource_ids.length,
                          denied: inspection.constraint.denied_ids.length,
                        })}
                  </span>
                </div>
                <div className="overflow-hidden rounded-md border">
                  <AutoTable
                    searchable={(match) =>
                      [
                        match.permission_code,
                        match.role_id,
                        match.source_type,
                        match.source_id,
                        match.scope_type,
                        match.scope_id,
                        match.effect,
                      ].join(" ")
                    }
                    onRefresh={inspectAuthorization}
                    sticky={{ maxHeight: "20rem" }}
                    className="[&_table]:min-w-[760px]"
                    emptyContent={null}
                    data={inspection.explanation.matches.map(
                      (match, index) => ({ ...match, rowIndex: index })
                    )}
                    rowKey={(match) => String(match.rowIndex)}
                    columns={[
                      {
                        field: "permission",
                        name: t("permission"),
                        type: "custom",
                        render: (_value, match) => (
                          <div className="font-mono text-xs">
                            {match.permission_code}
                          </div>
                        ),
                      },
                      {
                        field: "role",
                        name: t("role"),
                        type: "custom",
                        render: (_value, match) => (
                          <div>
                            {roleByID.get(match.role_id)?.name ?? match.role_id}
                          </div>
                        ),
                      },
                      {
                        field: "source",
                        name: t("source"),
                        type: "custom",
                        render: (_value, match) => (
                          <div className="text-sm text-muted-foreground">
                            {match.source_type}
                          </div>
                        ),
                      },
                      {
                        field: "authorizationScope",
                        name: t("authorizationScope"),
                        type: "custom",
                        render: (_value, match) => (
                          <div className="max-w-80 text-xs break-all text-muted-foreground">
                            {match.path
                              ?.map(
                                (step) =>
                                  `${step.subject_type}:${step.subject_id}${step.subject_relation ? `#${step.subject_relation}` : ""} → ${step.relation} → ${step.resource_type}:${step.resource_id}`
                              )
                              .join(" / ") ?? "-"}
                          </div>
                        ),
                      },
                      {
                        field: "relationshipPath",
                        name: t("relationshipPath"),
                        type: "custom",
                        render: (_value, match) => (
                          <div className="text-sm text-muted-foreground">
                            {match.scope_type}:{match.scope_id}
                            {match.inherited ? t("inherited") : ""}
                          </div>
                        ),
                      },
                      {
                        field: "effect",
                        name: t("effect"),
                        type: "custom",
                        render: (_value, match) => (
                          <div>
                            <Badge
                              variant={
                                match.effect === "allow"
                                  ? "secondary"
                                  : "destructive"
                              }
                            >
                              {match.effect === "allow"
                                ? t("allow")
                                : t("deny")}
                            </Badge>
                          </div>
                        ),
                      },
                    ]}
                  />
                  {inspection.explanation.matches.length === 0 && (
                    <div className="grid min-h-20 place-items-center text-sm text-muted-foreground">
                      {t("noMatchingSources")}
                    </div>
                  )}
                </div>
              </div>
            )}
          </section>
          <DialogFooter>
            <Button variant="outline" onClick={() => setBindingEntity(null)}>
              {t("close")}
            </Button>
            <Button
              type="submit"
              form="entity-binding-form"
              disabled={busy || !roleID || !principalID}
            >
              {busy ? t("saving") : t("saveBinding")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function flattenEntities(
  entities: Entity[],
  collapsed: Set<string>
): Array<{ entity: Entity; depth: number }> {
  const children = new Map<string, Entity[]>()
  for (const entity of entities) {
    const parentID = entity.parent_id ?? ""
    const siblings = children.get(parentID) ?? []
    siblings.push(entity)
    children.set(parentID, siblings)
  }
  for (const siblings of children.values())
    siblings.sort(
      (left, right) =>
        left.type.localeCompare(right.type) ||
        left.name.localeCompare(right.name) ||
        left.id.localeCompare(right.id)
    )
  const result: Array<{ entity: Entity; depth: number }> = []
  const visit = (parentID: string, depth: number) => {
    for (const entity of children.get(parentID) ?? []) {
      result.push({ entity, depth })
      if (!collapsed.has(entity.id)) visit(entity.id, depth + 1)
    }
  }
  visit("", 0)
  return result
}

function isDescendantOf(
  candidate: Entity,
  ancestorID: string | undefined,
  entities: Map<string, Entity>
): boolean {
  if (!ancestorID) return false
  let parentID = candidate.parent_id
  while (parentID) {
    if (parentID === ancestorID) return true
    parentID = entities.get(parentID)?.parent_id
  }
  return false
}

function parseMetadata(raw: string): Record<string, unknown> {
  let value: unknown
  try {
    value = JSON.parse(raw || "{}")
  } catch {
    throw new Error("metadataInvalidJson")
  }
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("metadataMustBeObject")
  return value as Record<string, unknown>
}

function metadataSummary(metadata: Record<string, unknown>): string {
  return Object.keys(metadata).length ? JSON.stringify(metadata) : "-"
}

function authorizationReason(
  reason: AuthorizationExplanation["reason"]
): string {
  return {
    inactive_membership: "memberInactiveReason",
    inactive_tenant: "tenantInactiveReason",
    inactive_resource: "entityInactiveReason",
    explicit_deny: "explicitDenyReason",
    administrator_scope_denied: "adminScopeDenied",
    permission_grant: "permissionGrantedReason",
    administrator_grant: "adminFallbackReason",
    relationship_grant: "relationshipGrantedReason",
    no_matching_grant: "noMatchingGrant",
  }[reason]
}

// The API handlers interpret these controls as local wall-clock time before converting to an instant.
function LocalDateTimeField({
  config,
  value,
  onChange,
  disabled,
  readonly,
  labelId,
  error,
  errorId,
}: FieldRendererProps) {
  return (
    <div>
      <Input
        id={config.id}
        type="datetime-local"
        value={String(value ?? "")}
        onChange={(event) => onChange(event.target.value)}
        disabled={disabled || readonly}
        aria-labelledby={labelId}
        aria-describedby={error ? errorId : undefined}
        aria-invalid={Boolean(error)}
      />
      {error && (
        <p id={errorId} className="mt-1 text-xs text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}
