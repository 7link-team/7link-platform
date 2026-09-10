import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type FormEvent,
} from "react"
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
import { Label } from "@workspace/ui/components/label"
import { SimpleSelect } from "@workspace/ui/components/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
import { Textarea } from "@workspace/ui/components/textarea"
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
  type Position,
  type Relationship,
  type RelationshipRelation,
  type RelationshipSubjectType,
  type Role,
} from "../../lib/iam-api"
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
  const [positions, setPositions] = useState<Position[]>([])
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
  const positionByID = useMemo(
    () => new Map(positions.map((position) => [position.id, position])),
    [positions]
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
      case "position":
        return positions.map((position) => ({
          value: position.id,
          label: position.name,
        }))
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
  }, [
    bindingEntity?.id,
    entities,
    groups,
    members,
    positions,
    relationshipSubjectType,
  ])

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

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    try {
      const input: EntityInput = {
        parent_id: parentID === rootEntity ? "" : parentID,
        type: String(data.get("type")),
        name: String(data.get("name")),
        status,
        metadata: parseMetadata(String(data.get("metadata") || "{}")),
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
        nextPositions,
        nextPermissions,
      ] = await Promise.all([
        iamApi.entityRoleBindings(entity.id),
        loadRelationships(entity.id),
        iamApi.roles(),
        iamApi.members(),
        iamApi.groups(),
        iamApi.positions(),
        iamApi.permissions(),
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
      setPositions(
        nextPositions.filter((position) => position.status === "active")
      )
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

  const putRelationship = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
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
      case "position":
        return (
          positionByID.get(relationship.subject_id)?.name ??
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

  const putBinding = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
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
    <>
      <PageHeader
        title={t(scopeEntityID ? "entitySettings" : "entityManagement")}
        description={t("entitiesDescription")}
        action={
          !scopeEntityID &&
          loaded &&
          !loadError && (
            <Button onClick={openCreate}>
              <Plus />
              {t("createEntity")}
            </Button>
          )
        }
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
        <div className="overflow-x-auto rounded-md border bg-card">
          <Table className="min-w-[900px]">
            <TableHeader>
              <TableRow>
                <TableHead>{t("entity")}</TableHead>
                <TableHead>{t("type")}</TableHead>
                <TableHead>{t("parentEntity")}</TableHead>
                <TableHead>{t("status")}</TableHead>
                <TableHead>{t("metadata")}</TableHead>
                <TableHead className="w-32 text-right">
                  {t("actions")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tree.map(({ entity, depth }) => {
                const hasChildren =
                  !scopeEntityID && (childCounts.get(entity.id) ?? 0) > 0
                return (
                  <TableRow key={entity.id}>
                    <TableCell>
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
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline">{entity.type}</Badge>
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {entity.parent_id
                        ? (entityByID.get(entity.parent_id)?.name ??
                          entity.parent_id)
                        : "-"}
                    </TableCell>
                    <TableCell>
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
                    </TableCell>
                    <TableCell className="max-w-72 truncate font-mono text-xs text-muted-foreground">
                      {metadataSummary(entity.metadata)}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
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
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
          {tree.length === 0 && (
            <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
              <span className="text-center">
                <Building2 className="mx-auto mb-2 size-7" />
                {t(scopeEntityID ? "entityUnavailable" : "noEntities")}
              </span>
            </div>
          )}
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
          <form
            key={editing?.id ?? "create"}
            id="entity-form"
            className="grid gap-4"
            onSubmit={submit}
          >
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="grid gap-2">
                <Label htmlFor="entity-name">{t("name")}</Label>
                <Input
                  id="entity-name"
                  name="name"
                  defaultValue={editing?.name}
                  required
                  maxLength={200}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="entity-type">{t("type")}</Label>
                <Input
                  id="entity-type"
                  name="type"
                  defaultValue={editing?.type}
                  pattern="[a-z][a-z0-9_-]{0,63}"
                  placeholder="company"
                  required
                  maxLength={64}
                />
              </div>
            </div>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="grid gap-2">
                <Label htmlFor="entity-parent">{t("parentEntity")}</Label>
                <SimpleSelect
                  id="entity-parent"
                  options={parentOptions}
                  value={parentID}
                  onValueChange={setParentID}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="entity-status">{t("status")}</Label>
                <SimpleSelect
                  id="entity-status"
                  options={{ active: t("active"), disabled: t("disable") }}
                  value={status}
                  onValueChange={(value) =>
                    setStatus(value as EntityInput["status"])
                  }
                />
              </div>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="entity-metadata">{t("metadataJson")}</Label>
              <Textarea
                id="entity-metadata"
                name="metadata"
                className="font-mono text-xs"
                defaultValue={JSON.stringify(editing?.metadata ?? {}, null, 2)}
                rows={5}
                spellCheck={false}
              />
            </div>
          </form>
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
          <form
            id="entity-binding-form"
            className="grid grid-cols-1 gap-3 md:grid-cols-4"
            onSubmit={putBinding}
          >
            <div className="grid gap-2">
              <Label htmlFor="entity-binding-role">{t("role")}</Label>
              <SimpleSelect
                id="entity-binding-role"
                options={roles.map((role) => ({
                  value: role.id,
                  label: role.name,
                }))}
                value={roleID}
                onValueChange={setRoleID}
                placeholder={t("noAvailableRoles")}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="entity-binding-principal">{t("member")}</Label>
              <SimpleSelect
                id="entity-binding-principal"
                options={members.map((member) => ({
                  value: member.subject,
                  label: member.display_name || member.subject,
                }))}
                value={principalID}
                onValueChange={setPrincipalID}
                placeholder={t("noActiveMembers")}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="entity-binding-effect">{t("effect")}</Label>
              <SimpleSelect
                id="entity-binding-effect"
                options={{ allow: t("allow"), deny: t("deny") }}
                value={effect}
                onValueChange={(value) =>
                  setEffect(value as EntityRoleBinding["effect"])
                }
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="entity-binding-expiry">{t("expiresAt")}</Label>
              <Input
                id="entity-binding-expiry"
                type="datetime-local"
                value={expiresAt}
                onChange={(event) => setExpiresAt(event.target.value)}
              />
            </div>
          </form>
          <div className="overflow-x-auto rounded-md border">
            <Table className="min-w-[720px]">
              <TableHeader>
                <TableRow>
                  <TableHead>{t("role")}</TableHead>
                  <TableHead>{t("member")}</TableHead>
                  <TableHead>{t("effect")}</TableHead>
                  <TableHead>{t("expiresAt")}</TableHead>
                  <TableHead className="w-16 text-right">
                    {t("actions")}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {bindings.map((binding) => (
                  <TableRow key={`${binding.role_id}:${binding.principal_id}`}>
                    <TableCell>
                      {roleByID.get(binding.role_id)?.name ?? binding.role_id}
                    </TableCell>
                    <TableCell>
                      {memberByID.get(binding.principal_id)?.display_name ??
                        binding.principal_id}
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={
                          binding.effect === "allow"
                            ? "secondary"
                            : "destructive"
                        }
                      >
                        {binding.effect === "allow" ? t("allow") : t("deny")}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {binding.expires_at
                        ? new Date(binding.expires_at).toLocaleString(locale)
                        : t("noExpiry")}
                    </TableCell>
                    <TableCell className="text-right">
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
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
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
            <form
              id="entity-relationship-form"
              className="grid grid-cols-1 items-end gap-3 md:grid-cols-2 xl:grid-cols-4"
              onSubmit={putRelationship}
            >
              <div className="grid gap-2">
                <Label htmlFor="relationship-subject-type">
                  {t("principalType")}
                </Label>
                <SimpleSelect
                  id="relationship-subject-type"
                  options={{
                    principal: t("member"),
                    group: t("groups"),
                    position: t("positions"),
                    entity: t("entity"),
                  }}
                  value={relationshipSubjectType}
                  onValueChange={(value) => {
                    const type = value as RelationshipSubjectType
                    setRelationshipSubjectType(type)
                    setRelationshipSubjectID("")
                  }}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-subject">{t("principal")}</Label>
                <SimpleSelect
                  id="relationship-subject"
                  options={relationshipSubjectOptions}
                  value={relationshipSubjectID}
                  onValueChange={setRelationshipSubjectID}
                  placeholder={t("noPrincipalsAvailable")}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-subject-relation">
                  {t("subjectRelation")}
                </Label>
                {relationshipSubjectType === "entity" ? (
                  <SimpleSelect
                    id="relationship-subject-relation"
                    options={{
                      owner: t("owner"),
                      editor: t("editor"),
                      viewer: t("viewer"),
                    }}
                    value={relationshipSubjectRelation}
                    onValueChange={(value) =>
                      setRelationshipSubjectRelation(
                        value as RelationshipRelation
                      )
                    }
                  />
                ) : (
                  <Input
                    id="relationship-subject-relation"
                    value={
                      relationshipSubjectType === "principal"
                        ? t("directPrincipal")
                        : "member"
                    }
                    disabled
                  />
                )}
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-relation">
                  {t("grantRelation")}
                </Label>
                <SimpleSelect
                  id="relationship-relation"
                  options={{
                    owner: t("owner"),
                    editor: t("editor"),
                    viewer: t("viewer"),
                  }}
                  value={relationshipRelation}
                  onValueChange={(value) =>
                    setRelationshipRelation(value as RelationshipRelation)
                  }
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-resource-type">
                  {t("businessResourceType")}
                </Label>
                <SimpleSelect
                  id="relationship-resource-type"
                  options={resourceTypeOptions}
                  value={relationshipResourceType}
                  onValueChange={setRelationshipResourceType}
                  placeholder={t("selectType")}
                  disabled={!relationshipResourceID}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-resource-id">
                  {t("businessResourceId")}
                </Label>
                <Input
                  id="relationship-resource-id"
                  value={relationshipResourceID}
                  onChange={(event) =>
                    setRelationshipResourceID(event.target.value)
                  }
                  placeholder={t("blankCurrentEntity")}
                  maxLength={255}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-starts-at">
                  {t("effectiveAt")}
                </Label>
                <Input
                  id="relationship-starts-at"
                  type="datetime-local"
                  value={relationshipStartsAt}
                  onChange={(event) =>
                    setRelationshipStartsAt(event.target.value)
                  }
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-ends-at">{t("expiresAt")}</Label>
                <Input
                  id="relationship-ends-at"
                  type="datetime-local"
                  value={relationshipEndsAt}
                  onChange={(event) =>
                    setRelationshipEndsAt(event.target.value)
                  }
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-minimum-acr">
                  {t("minimumAcr")}
                </Label>
                <Input
                  id="relationship-minimum-acr"
                  type="number"
                  min="1"
                  max="99"
                  value={relationshipMinimumAcr}
                  onChange={(event) =>
                    setRelationshipMinimumAcr(event.target.value)
                  }
                  placeholder={t("unrestricted")}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-client-id">
                  {t("oauthClients")}
                </Label>
                <Input
                  id="relationship-client-id"
                  value={relationshipClientID}
                  onChange={(event) =>
                    setRelationshipClientID(event.target.value)
                  }
                  placeholder={t("unrestricted")}
                  maxLength={255}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-network-zone">
                  {t("networkZone")}
                </Label>
                <Input
                  id="relationship-network-zone"
                  value={relationshipNetworkZone}
                  onChange={(event) =>
                    setRelationshipNetworkZone(event.target.value)
                  }
                  placeholder={t("unrestricted")}
                  maxLength={255}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-time-start">
                  {t("dailyStart")}
                </Label>
                <Input
                  id="relationship-time-start"
                  type="time"
                  value={relationshipTimeStart}
                  onChange={(event) =>
                    setRelationshipTimeStart(event.target.value)
                  }
                  required={Boolean(relationshipTimeEnd)}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-time-end">{t("dailyEnd")}</Label>
                <Input
                  id="relationship-time-end"
                  type="time"
                  value={relationshipTimeEnd}
                  onChange={(event) =>
                    setRelationshipTimeEnd(event.target.value)
                  }
                  required={Boolean(relationshipTimeStart)}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="relationship-timezone">{t("timezone")}</Label>
                <Input
                  id="relationship-timezone"
                  value={relationshipTimezone}
                  onChange={(event) =>
                    setRelationshipTimezone(event.target.value)
                  }
                  required={Boolean(
                    relationshipTimeStart || relationshipTimeEnd
                  )}
                  maxLength={64}
                />
              </div>
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
            </form>
            <div className="mt-3 overflow-x-auto rounded-md border">
              <Table className="min-w-[1160px]">
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("principalType")}</TableHead>
                    <TableHead>{t("principal")}</TableHead>
                    <TableHead>{t("subjectRelation")}</TableHead>
                    <TableHead>{t("grantRelation")}</TableHead>
                    <TableHead>{t("target")}</TableHead>
                    <TableHead>{t("validity")}</TableHead>
                    <TableHead>{t("condition")}</TableHead>
                    <TableHead className="w-16 text-right">
                      {t("actions")}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {relationships.map((relationship) => (
                    <TableRow
                      key={`${relationship.entity_id}:${relationship.subject_type}:${relationship.subject_id}:${relationship.subject_relation}:${relationship.relation}:${relationship.resource_type}:${relationship.resource_id}`}
                    >
                      <TableCell>{relationship.subject_type}</TableCell>
                      <TableCell>
                        {relationshipSubjectLabel(relationship)}
                      </TableCell>
                      <TableCell>
                        {relationship.subject_relation || t("direct")}
                      </TableCell>
                      <TableCell>
                        <Badge variant="secondary">
                          {relationship.relation}
                        </Badge>
                      </TableCell>
                      <TableCell className="max-w-64 font-mono text-xs break-all text-muted-foreground">
                        {relationship.entity_id
                          ? `${relationship.resource_type}:${relationship.resource_id}`
                          : (entityByID.get(relationship.resource_id)?.name ??
                            `${relationship.resource_type}:${relationship.resource_id}`)}
                      </TableCell>
                      <TableCell
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
                      </TableCell>
                      <TableCell
                        className="max-w-72 text-xs text-muted-foreground"
                        data-relationship-condition
                      >
                        {trustedContextConditionSummary(
                          relationship.condition,
                          t("unrestricted")
                        )}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="ghost"
                          size="icon"
                          disabled={busy}
                          onClick={() => void removeRelationship(relationship)}
                          aria-label={t("revokeRelation")}
                          title={t("revokeRelation")}
                        >
                          <Trash2 />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
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
            <div className="grid grid-cols-1 items-end gap-3 md:grid-cols-2 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto]">
              <div className="grid gap-2">
                <Label htmlFor="authorization-inspection-principal">
                  {t("member")}
                </Label>
                <SimpleSelect
                  id="authorization-inspection-principal"
                  options={members.map((member) => ({
                    value: member.subject,
                    label: member.display_name || member.subject,
                  }))}
                  value={principalID}
                  onValueChange={(value) => {
                    setPrincipalID(value)
                    setInspection(null)
                  }}
                  placeholder={t("noActiveMembers")}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="authorization-inspection-permission">
                  {t("dataPermission")}
                </Label>
                <SimpleSelect
                  id="authorization-inspection-permission"
                  options={permissions.map((permission) => ({
                    value: permission.code,
                    label: `${permission.code} · ${permission.summary}`,
                  }))}
                  value={inspectionPermission}
                  onValueChange={(value) => {
                    setInspectionPermission(value)
                    setInspection(null)
                  }}
                  placeholder={t("noDataPermission")}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="authorization-inspection-resource">
                  {t("businessResourceId")}
                </Label>
                <Input
                  id="authorization-inspection-resource"
                  value={inspectionResourceID}
                  onChange={(event) => {
                    setInspectionResourceID(event.target.value)
                    setInspection(null)
                  }}
                  placeholder={t("blankCheckCurrentEntity")}
                  maxLength={255}
                />
              </div>
              <Button
                type="button"
                variant="outline"
                disabled={busy || !principalID || !inspectionPermission}
                onClick={() => void inspectAuthorization()}
              >
                <ScanSearch />
                {t("evaluateAuthorization")}
              </Button>
            </div>
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
                <div className="overflow-x-auto rounded-md border">
                  <Table className="min-w-[760px]">
                    <TableHeader>
                      <TableRow>
                        <TableHead>{t("permission")}</TableHead>
                        <TableHead>{t("role")}</TableHead>
                        <TableHead>{t("source")}</TableHead>
                        <TableHead>{t("authorizationScope")}</TableHead>
                        <TableHead>{t("relationshipPath")}</TableHead>
                        <TableHead>{t("effect")}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {inspection.explanation.matches.map((match, index) => (
                        <TableRow
                          key={`${match.role_id}:${match.scope_type}:${match.scope_id}:${match.effect}:${index}`}
                        >
                          <TableCell className="font-mono text-xs">
                            {match.permission_code}
                          </TableCell>
                          <TableCell>
                            {roleByID.get(match.role_id)?.name ?? match.role_id}
                          </TableCell>
                          <TableCell className="text-sm text-muted-foreground">
                            {match.source_type}
                          </TableCell>
                          <TableCell className="max-w-80 text-xs break-all text-muted-foreground">
                            {match.path
                              ?.map(
                                (step) =>
                                  `${step.subject_type}:${step.subject_id}${step.subject_relation ? `#${step.subject_relation}` : ""} → ${step.relation} → ${step.resource_type}:${step.resource_id}`
                              )
                              .join(" / ") ?? "-"}
                          </TableCell>
                          <TableCell className="text-sm text-muted-foreground">
                            {match.scope_type}:{match.scope_id}
                            {match.inherited ? t("inherited") : ""}
                          </TableCell>
                          <TableCell>
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
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
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
    </>
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
