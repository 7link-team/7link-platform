import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { systemRoleLabel } from "../../lib/membership-presentation"
import { Link, useSearchParams, useParams } from "react-router"
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react"
import {
  CalendarClock,
  Database,
  ExternalLink,
  KeyRound,
  Pencil,
  Plus,
  SlidersHorizontal,
  Trash2,
  UsersRound,
} from "lucide-react"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import { Checkbox } from "@workspace/ui/components/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Input } from "@workspace/ui/components/input"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@workspace/ui/components/tabs"
import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import type { FieldRendererProps } from "@workspace/ui/components/auto-form/types"
import { AutoTable } from "@workspace/ui/components/auto-table/auto-table"
import { apiRoute } from "../../lib/api-route"
import { useRequestContract } from "../../lib/use-request-contract"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { operatingPlatformID } from "../../lib/organization-scope"
import {
  iamApi,
  type Group,
  type DataScope,
  type Department,
  type Member,
  type Permission,
  type Role,
  type RoleDirectoryBinding,
  type RoleMemberGrant,
  type RoleDataScope,
  type RolePermissionGrant,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import {
  trustedContextCondition,
  trustedContextConditionDraft,
  trustedContextConditionSummary,
  type TrustedContextConditionDraft,
} from "../../lib/policy-condition"

export function SystemRolesPage() {
  return <RolesPage system />
}

type WorkspaceRole = Role & { builtin?: boolean }
type WorkspaceMember = Member & { protected?: boolean }

export default function RolesPage({ system = false }: { system?: boolean }) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const scopeAllowed = useAllowed()
  const allowed = useCallback(
    (code: string) =>
      scopeAllowed(
        system
          ? code === "user_view"
            ? "system_member_view"
            : code === "dept_view"
              ? "system_department_view"
              : `system_${code}`
          : code
      ),
    [scopeAllowed, system]
  )
  const api = useMemo(
    () =>
      system
        ? {
            ...iamApi,
            roles: iamApi.systemRoles,
            permissions: iamApi.systemRolePermissionCatalog,
            createRole: iamApi.createSystemRole,
            updateRole: iamApi.updateSystemRole,
            deleteRole: iamApi.deleteSystemRole,
            rolePermissionGrants: iamApi.systemRolePermissionGrants,
            grantPermission: iamApi.grantSystemRolePermission,
            revokePermission: iamApi.revokeSystemRolePermission,
            roleMembers: iamApi.systemRoleMembers,
            addRoleMember: iamApi.addSystemRoleMember,
            removeRoleMember: iamApi.removeSystemRoleMember,
            members: iamApi.systemMembers,
            departments: iamApi.systemDepartments,
          }
        : iamApi,
    [system]
  )
  const [params, setParams] = useSearchParams()
  const requestedView = roleWorkspaceView(params.get("view"))
  const view =
    system && requestedView === "scope" ? "permissions" : requestedView

  const [roles, setRoles] = useState<WorkspaceRole[]>([])
  const [catalog, setCatalog] = useState<Permission[]>([])
  const [detailsRevision, setDetailsRevision] = useState(0)
  const selected =
    roles.find((role) => role.id === params.get("role")) ?? roles[0] ?? null
  const [details, setDetails] = useState<{
    roleID: string
    grants: RolePermissionGrant[]
    boundMembers: string[]
    memberEnds: Record<string, string | null>
    members: WorkspaceMember[]
    bindings: RoleDirectoryBinding[]
    groups: Group[]

    dataScope: RoleDataScope
    departments: Department[]
  }>({
    roleID: "",
    grants: [],
    boundMembers: [],
    memberEnds: {},
    members: [],
    bindings: [],
    groups: [],

    dataScope: { role_id: "", scope: "all", department_ids: [] },
    departments: [],
  })
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<WorkspaceRole | null>(null)
  const [busy, setBusy] = useState(false)
  const { tenantID = "", platformID = "" } = useParams()
  const roleContractRoute = useMemo(() => {
    if (!open) return null
    if (editing)
      return system
        ? apiRoute("patch", "/system/roles/{role_id}", {
            path: { role_id: editing.id },
          })
        : apiRoute("patch", "/roles/{role_id}", {
            path: { role_id: editing.id },
          })
    if (system) return apiRoute("post", "/system/roles")
    if (platformID)
      return apiRoute("post", "/operating-platforms/{platform_id}/roles", {
        path: { platform_id: platformID },
      })
    return apiRoute("post", "/tenants/{tenant_id}/roles", {
      path: { tenant_id: tenantID },
    })
  }, [open, editing, system, platformID, tenantID])
  const roleContract = useRequestContract(roleContractRoute)

  const [error, setError] = useState("")
  const [loadError, setLoadError] = useState("")
  const [scopeDraft, setScopeDraft] = useState<DataScope>("all")
  const [scopeDepartmentIDs, setScopeDepartmentIDs] = useState<string[]>([])
  const scopeDraftVersion = useRef(0)
  const scopeDirty = useRef(false)
  const scopeSource = useRef<{ scope: DataScope; department_ids: string[] }>({
    scope: "all",
    department_ids: [],
  })
  const activeRoleID = useRef(selected?.id)
  useEffect(() => {
    activeRoleID.current = selected?.id
    scopeDirty.current = false
  }, [selected?.id])
  const [scopeFormRevision, setScopeFormRevision] = useState(0)
  const scopeContractRoute = useMemo(
    () =>
      !system && selected && view === "scope" && allowed("role_update")
        ? apiRoute("put", "/roles/{role_id}/data-scope", {
            path: { role_id: selected.id },
          })
        : null,
    [system, selected, view, allowed]
  )
  const scopeContract = useRequestContract(scopeContractRoute)
  const onScopeChange = useCallback((values: Record<string, unknown>) => {
    scopeDraftVersion.current += 1
    const ids = (values.department_ids as string[] | undefined) ?? []
    scopeDirty.current =
      values.scope !== scopeSource.current.scope ||
      ids.length !== scopeSource.current.department_ids.length ||
      ids.some((id, index) => id !== scopeSource.current.department_ids[index])
    setScopeDraft(values.scope as DataScope)
    setScopeDepartmentIDs((values.department_ids as string[] | undefined) ?? [])
  }, [])

  const [conditionPermission, setConditionPermission] =
    useState<Permission | null>(null)
  const [conditionDraft, setConditionDraft] =
    useState<TrustedContextConditionDraft>(() =>
      trustedContextConditionDraft(undefined, localTimezone())
    )

  // Reading the page and writing to it fail on different channels: a refused
  // read keeps the error next to a retry, a refused write next to the form.
  const refresh = useCallback(
    () =>
      Promise.all([api.roles(), api.permissions()])
        .then(([nextRoles, nextCatalog]) => {
          setRoles(nextRoles)
          setCatalog(nextCatalog)
          setLoadError("")
        })
        .catch((cause: Error) => setLoadError(cause.message)),
    [api]
  )

  useEffect(() => {
    void refresh()
  }, [refresh, locale])

  const [refreshingDetails, setRefreshingDetails] = useState(false)
  useEffect(() => {
    if (!selected) return
    let current = true
    const scopeVersion = scopeDraftVersion.current
    // Three of these answer to permissions a role administrator need not hold:
    // the member directory to user_view, groups to their own view
    // codes, departments to dept_view. Asking for all lists at once used to make
    // any one refusal empty the whole role workspace, so the assignable lists
    // degrade on their own and the role's own data still arrives.
    void Promise.allSettled([
      api.rolePermissionGrants(selected.id),
      api.roleMembers(selected.id),
      system
        ? Promise.resolve([] as RoleMemberGrant[])
        : iamApi.roleMemberGrants(selected.id),
      allowed("user_view") ? api.members() : Promise.resolve([]),
      system ? Promise.resolve([]) : iamApi.roleDirectoryBindings(selected.id),
      !system && allowed("group_view") ? iamApi.groups() : Promise.resolve([]),

      system
        ? Promise.resolve({
            role_id: selected.id,
            scope: "all" as const,
            department_ids: [],
          })
        : iamApi.roleDataScope(selected.id),
      allowed("dept_view") ? api.departments() : Promise.resolve([]),
    ])
      .then(
        ([
          nextGrants,
          nextBoundMembers,
          nextMemberGrants,
          nextMembers,
          nextBindings,
          nextGroups,

          nextDataScope,
          nextDepartments,
        ]) => {
          if (!current) return
          const refused = [
            nextGrants,
            nextBoundMembers,
            nextBindings,
            nextDataScope,
          ].find((result) => result.status === "rejected")
          if (refused?.status === "rejected") {
            setLoadError((refused.reason as Error | undefined)?.message ?? "")
            return
          }
          if (
            nextGrants.status !== "fulfilled" ||
            nextBoundMembers.status !== "fulfilled" ||
            nextBindings.status !== "fulfilled" ||
            nextDataScope.status !== "fulfilled"
          )
            return
          setDetails({
            roleID: selected.id,
            grants: nextGrants.value,
            boundMembers: nextBoundMembers.value,
            memberEnds:
              nextMemberGrants.status === "fulfilled"
                ? Object.fromEntries(
                    nextMemberGrants.value.map((grant) => [
                      grant.principal_id,
                      grant.expires_at ?? null,
                    ])
                  )
                : {},
            members:
              nextMembers.status === "fulfilled" ? nextMembers.value : [],
            bindings: nextBindings.value,
            groups: nextGroups.status === "fulfilled" ? nextGroups.value : [],

            dataScope: nextDataScope.value,
            departments:
              nextDepartments.status === "fulfilled"
                ? nextDepartments.value
                : [],
          })
          // A read started before an edit must not reset the newer scope draft.
          if (
            scopeVersion === scopeDraftVersion.current &&
            !scopeDirty.current
          ) {
            const nextScope = nextDataScope.value
            const changed =
              nextScope.scope !== scopeSource.current.scope ||
              JSON.stringify(nextScope.department_ids) !==
                JSON.stringify(scopeSource.current.department_ids)
            scopeSource.current = nextScope
            setScopeDraft(nextScope.scope)
            setScopeDepartmentIDs(nextScope.department_ids)
            if (changed) setScopeFormRevision((value) => value + 1)
          }
        }
      )
      .finally(() => {
        if (current) setRefreshingDetails(false)
      })
    return () => {
      current = false
    }
  }, [selected, allowed, api, system, detailsRevision])

  const loadingDetails = details.roleID !== selected?.id
  const grants = loadingDetails ? [] : details.grants
  const boundMembers = loadingDetails ? [] : details.boundMembers
  const members = loadingDetails ? [] : details.members
  const memberEnds = loadingDetails ? {} : details.memberEnds
  const directoryBindings = loadingDetails ? [] : details.bindings
  const directoryGroups = loadingDetails ? [] : details.groups

  const departments = loadingDetails ? [] : details.departments
  const conditionGrant = grants.find(
    (grant) => grant.permission_code === conditionPermission?.code
  )
  const condition = trustedContextCondition(
    conditionDraft.minimumAcr,
    conditionDraft.clientID,
    conditionDraft.networkZone,
    conditionDraft.timeStart,
    conditionDraft.timeEnd,
    conditionDraft.timezone
  )

  const permissionGroups = useMemo(
    () =>
      catalog.reduce<Record<string, Permission[]>>((result, permission) => {
        const permissions = result[permission.resource] ?? []
        permissions.push(permission)
        result[permission.resource] = permissions
        return result
      }, {}),
    [catalog]
  )
  const assignmentCount = boundMembers.length + directoryBindings.length

  const selectRole = (role: Role) => {
    setConditionPermission(null)
    const next = new URLSearchParams(params)
    next.set("role", role.id)
    setParams(next)
  }

  const selectView = (nextView: string) => {
    const next = new URLSearchParams(params)
    if (nextView === "permissions") next.delete("view")
    else next.set("view", nextView)
    setParams(next)
  }

  const submit = async (values: Record<string, unknown>) => {
    setBusy(true)
    setError("")
    const body = {
      name: String(values.name ?? ""),
      description: String(values.description ?? ""),
    }
    try {
      const role = editing
        ? await api.updateRole(editing.id, body)
        : await api.createRole(body)
      setOpen(false)
      setEditing(null)
      const next = new URLSearchParams(params)
      next.set("role", role.id)
      setParams(next)
      await refresh()
    } finally {
      setBusy(false)
    }
  }

  const togglePermission = async (code: string, checked: boolean) => {
    if (!selected) return
    setBusy(true)
    setError("")
    try {
      if (checked) await api.grantPermission(selected.id, code)
      else await api.revokePermission(selected.id, code)
      const nextGrants = await api.rolePermissionGrants(selected.id)
      setDetails((current) =>
        current.roleID === selected.id
          ? { ...current, grants: nextGrants }
          : current
      )
      if (!checked && conditionPermission?.code === code)
        setConditionPermission(null)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const openPermissionCondition = (permission: Permission) => {
    const current = grants.find(
      (grant) => grant.permission_code === permission.code
    )
    setConditionDraft(
      trustedContextConditionDraft(current?.condition, localTimezone())
    )
    setConditionPermission(permission)
  }

  const savePermissionCondition = async (values: Record<string, unknown>) => {
    const condition = trustedContextCondition(
      String(values.minimumAcr ?? ""),
      String(values.clientID ?? ""),
      String(values.networkZone ?? ""),
      String(values.timeStart ?? ""),
      String(values.timeEnd ?? ""),
      String(values.timezone ?? "")
    )
    if (!selected || !conditionPermission || !condition) return
    setBusy(true)
    setError("")
    try {
      await iamApi.setRolePermissionCondition(
        selected.id,
        conditionPermission.code,
        condition
      )
      const nextGrants = await iamApi.rolePermissionGrants(selected.id)
      setDetails((current) =>
        current.roleID === selected.id
          ? { ...current, grants: nextGrants }
          : current
      )
      setConditionPermission(null)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const clearPermissionCondition = async () => {
    if (!selected || !conditionPermission) return
    setBusy(true)
    setError("")
    try {
      await iamApi.clearRolePermissionCondition(
        selected.id,
        conditionPermission.code
      )
      const nextGrants = await iamApi.rolePermissionGrants(selected.id)
      setDetails((current) =>
        current.roleID === selected.id
          ? { ...current, grants: nextGrants }
          : current
      )
      setConditionPermission(null)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  // Both listings are read back: one says who holds the role, the other until
  // when, and an edit can change either.
  const refreshMembers = async () => {
    if (!selected) return
    const nextBoundMembers = await api.roleMembers(selected.id)
    const nextEnds = system ? [] : await iamApi.roleMemberGrants(selected.id)
    setDetails((current) =>
      current.roleID === selected.id
        ? {
            ...current,
            boundMembers: nextBoundMembers,
            memberEnds: Object.fromEntries(
              nextEnds.map((grant) => [
                grant.principal_id,
                grant.expires_at ?? null,
              ])
            ),
          }
        : current
    )
  }

  // Granting is a put of the whole assignment, so an empty end removes one.
  const setMemberEnd = async (subject: string, end: string | null) => {
    if (!selected) return
    setBusy(true)
    setError("")
    try {
      await api.addRoleMember(selected.id, subject, end)
      await refreshMembers()
    } finally {
      setBusy(false)
    }
  }

  const toggleMember = async (subject: string, checked: boolean) => {
    if (!selected) return
    setBusy(true)
    setError("")
    try {
      if (checked) await api.addRoleMember(selected.id, subject)
      else await api.removeRoleMember(selected.id, subject)
      await refreshMembers()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const toggleDirectoryBinding = async (
    assigneeType: RoleDirectoryBinding["assignee_type"],
    assigneeID: string,
    checked: boolean
  ) => {
    if (!selected) return
    setBusy(true)
    setError("")
    try {
      if (checked)
        await iamApi.addRoleDirectoryBinding(
          selected.id,
          assigneeType,
          assigneeID
        )
      else
        await iamApi.removeRoleDirectoryBinding(
          selected.id,
          assigneeType,
          assigneeID
        )
      const nextBindings = await iamApi.roleDirectoryBindings(selected.id)
      setDetails((current) =>
        current.roleID === selected.id
          ? { ...current, bindings: nextBindings }
          : current
      )
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const remove = async () => {
    if (!selected) return
    setBusy(true)
    setError("")
    try {
      await api.deleteRole(selected.id)
      const next = new URLSearchParams(params)
      next.delete("role")
      setParams(next)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const saveDataScope = async (values: Record<string, unknown>) => {
    if (!selected) return
    scopeDraftVersion.current += 1
    setBusy(true)
    setError("")
    try {
      const dataScope = await iamApi.setRoleDataScope(selected.id, {
        scope: values.scope as DataScope,
        department_ids:
          values.scope === "selected_departments"
            ? (values.department_ids as string[])
            : [],
      })
      if (activeRoleID.current !== selected.id) return
      scopeSource.current = dataScope
      scopeDirty.current = false
      setDetails((current) =>
        current.roleID === selected.id ? { ...current, dataScope } : current
      )
      setScopeDraft(dataScope.scope)
      setScopeDepartmentIDs(dataScope.department_ids)
      setScopeFormRevision((value) => value + 1)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-col lg:flex-1" data-role-workspace>
      <PageHeader
        title={t(
          system
            ? "systemRolePermissions"
            : operatingPlatformID()
              ? "platformRolePermissions"
              : "tenantRolePermissions"
        )}
        description={t(
          system
            ? "systemRolesDescription"
            : operatingPlatformID()
              ? "platformRolesDescription"
              : "rolesDescription"
        )}
        action={
          allowed("role_create") && (
            <Button
              onClick={() => {
                setEditing(null)
                setOpen(true)
              }}
            >
              <Plus />
              {t("createRole")}
            </Button>
          )
        }
      />
      {error && <Alert>{error}</Alert>}
      {loadError && (
        <Alert>
          <p>{t.has(loadError) ? t(loadError) : loadError}</p>
          <Button variant="outline" onClick={() => void refresh()}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      <div className="grid min-h-0 overflow-hidden rounded-md border bg-card lg:flex-1 lg:grid-cols-[280px_minmax(0,1fr)]">
        <aside
          className="min-h-0 border-b lg:overflow-y-auto lg:overscroll-contain lg:border-r lg:border-b-0"
          tabIndex={0}
          aria-label={t("roleList")}
          data-role-navigation
        >
          <header className="sticky top-0 z-10 flex h-12 items-center justify-between border-b bg-muted px-4 text-sm font-semibold">
            {t("roleList")}
            <span className="text-xs font-normal text-muted-foreground">
              {roles.length}
            </span>
          </header>
          {roles.map((role) => (
            <button
              key={role.id}
              data-role-id={role.id}
              className={`grid min-h-16 w-full grid-cols-[34px_minmax(0,1fr)] items-center gap-3 border-b px-3 text-left hover:bg-muted/40 ${selected?.id === role.id ? "bg-primary/5 shadow-[inset_3px_0_var(--primary)]" : ""}`}
              onClick={() => selectRole(role)}
              aria-current={selected?.id === role.id ? "true" : undefined}
            >
              <span className="grid size-8 place-items-center rounded-md bg-primary/10 text-primary">
                <KeyRound size={16} />
              </span>
              <span className="min-w-0">
                <strong className="block text-sm break-words">
                  {system
                    ? systemRoleLabel(
                        role,
                        t("fullSystemAdministrator"),
                        t("importedSystemPermissions")
                      )
                    : role.name}
                </strong>
                <small className="mt-1 block text-xs break-words text-muted-foreground">
                  {role.builtin
                    ? t("protectedSuperadminAuthority")
                    : role.description || role.id}
                </small>
              </span>
            </button>
          ))}
        </aside>
        <section
          className="min-h-0 min-w-0 lg:flex lg:flex-col lg:overflow-hidden"
          aria-label={t("roleWorkspaceSections")}
          data-role-content
        >
          {selected ? (
            <>
              <header className="flex min-h-20 shrink-0 flex-wrap items-center justify-between gap-4 border-b px-5 py-3">
                <div className="min-w-0">
                  <h2 className="font-semibold break-words">
                    {system
                      ? systemRoleLabel(
                          selected,
                          t("fullSystemAdministrator"),
                          t("importedSystemPermissions")
                        )
                      : selected.name}
                  </h2>
                  <p className="mt-1 text-xs break-words text-muted-foreground">
                    {selected.builtin
                      ? t("protectedSuperadminAuthority")
                      : selected.description || t("noDescription")}
                  </p>
                </div>
                <div className="flex gap-2">
                  {!selected.builtin && allowed("role_update") && (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={busy}
                      onClick={() => {
                        setEditing(selected)
                        setOpen(true)
                      }}
                    >
                      <Pencil />
                      {t("edit")}
                    </Button>
                  )}
                  {!selected.builtin && allowed("role_delete") && (
                    <Button
                      variant="destructive"
                      size="sm"
                      disabled={busy}
                      onClick={() => void remove()}
                    >
                      <Trash2 />
                      {t("deleteRole")}
                    </Button>
                  )}
                </div>
              </header>
              {selected.builtin && (
                <p className="shrink-0 border-b px-5 py-3 text-sm text-muted-foreground">
                  {t("protectedSuperadminAuthority")}
                </p>
              )}
              <Tabs
                value={view}
                onValueChange={selectView}
                className="min-h-0 lg:flex lg:flex-1 lg:flex-col lg:overflow-hidden"
              >
                <TabsList
                  variant="line"
                  className="flex w-full shrink-0 justify-start gap-2 overflow-x-auto border-b px-3 sm:gap-5 sm:px-5"
                  aria-label={t("roleWorkspaceSections")}
                  data-role-workspace-tabs
                >
                  <TabsTrigger
                    variant="line"
                    value="permissions"
                    className="min-w-0 gap-1.5"
                    data-role-view="permissions"
                  >
                    <KeyRound className="size-4" />
                    {t("permissionContent")}
                    <span
                      className="min-w-5 text-center text-xs text-muted-foreground tabular-nums"
                      data-role-count="permissions"
                    >
                      {loadingDetails ? "..." : grants.length}
                    </span>
                  </TabsTrigger>
                  <TabsTrigger
                    variant="line"
                    value="assignments"
                    className="min-w-0 gap-1.5"
                    data-role-view="assignments"
                  >
                    <UsersRound className="size-4" />
                    {t("assignedObjects")}
                    <span
                      className="min-w-5 text-center text-xs text-muted-foreground tabular-nums"
                      data-role-count="assignments"
                    >
                      {loadingDetails ? "..." : assignmentCount}
                    </span>
                  </TabsTrigger>
                  {!system && (
                    <TabsTrigger
                      variant="line"
                      value="scope"
                      className="min-w-0 gap-1.5"
                      data-role-view="scope"
                    >
                      <Database className="size-4" />
                      {t("dataScope")}
                    </TabsTrigger>
                  )}
                </TabsList>
                <TabsContent
                  value="scope"
                  className="m-0 min-h-0 p-5 lg:flex-1 lg:overflow-y-auto lg:overscroll-contain"
                  data-role-panel="scope"
                >
                  <section data-role-data-scope>
                    <h3 className="mb-3 flex items-center gap-2 border-b pb-2 text-xs font-semibold text-muted-foreground uppercase">
                      <Database size={15} />
                      {t("dataScope")}
                    </h3>
                    {loadingDetails ? (
                      <p className="text-sm text-muted-foreground">
                        {t("loadingDataScope")}
                      </p>
                    ) : (
                      <div className="grid gap-4">
                        {scopeContract.failed && allowed("role_update") && (
                          <Alert>
                            <Button
                              variant="outline"
                              onClick={scopeContract.retry}
                            >
                              {t("retryNow")}
                            </Button>
                          </Alert>
                        )}
                        <AutoForm
                          key={`${selected.id}:${scopeFormRevision}`}
                          id="role-data-scope-form"
                          mode="edit"
                          readonly={!allowed("role_update")}
                          contract={scopeContract.contract}
                          initialData={{
                            scope: scopeDraft,
                            department_ids: scopeDepartmentIDs,
                          }}
                          onChange={onScopeChange}
                          onSubmit={saveDataScope}
                          fields={{
                            scope: {
                              name: t("scope"),
                              type: "select",
                              value: "all",
                              disabled: busy,
                              config: {
                                options: {
                                  all: t("allData"),
                                  self: t("ownData"),
                                  department: t("ownDepartment"),
                                  department_and_descendants: t(
                                    "departmentDescendants"
                                  ),
                                  selected_departments: t(
                                    "selectedDepartments"
                                  ),
                                },
                              },
                            },
                            department_ids: {
                              name: t("selectedDepartments"),
                              type: "custom",
                              component: ScopeDepartmentsField,
                              value: [],
                              col: 2,
                              disabled: busy,
                              show: (values) =>
                                values?.scope === "selected_departments",
                              config: { departments },
                            },
                          }}
                        />
                        {allowed("role_update") && (
                          <div>
                            <Button
                              size="sm"
                              type="submit"
                              form="role-data-scope-form"
                              disabled={busy || !scopeContract.contract}
                              data-save-data-scope
                            >
                              {t("saveDataScope")}
                            </Button>
                          </div>
                        )}
                      </div>
                    )}
                  </section>
                </TabsContent>
                <TabsContent
                  value="assignments"
                  className="m-0 min-h-0 p-5 lg:flex-1 lg:overflow-y-auto lg:overscroll-contain"
                  data-role-panel="assignments"
                >
                  <section className="mb-7">
                    <div className="mb-3 flex min-h-9 flex-wrap items-center justify-between gap-3 border-b pb-2">
                      <h3 className="flex items-center gap-2 text-xs font-semibold text-muted-foreground uppercase">
                        <UsersRound size={15} />
                        {t("roleMembers")}
                      </h3>
                      <Button variant="ghost" size="sm" asChild>
                        <Link
                          to={system ? "/system/accounts" : "../users"}
                          data-role-directory-link="account"
                        >
                          <ExternalLink />
                          {t("openAccountDirectory")}
                        </Link>
                      </Button>
                    </div>
                    {loadingDetails ? (
                      <p className="text-sm text-muted-foreground">
                        {t("loadingMembers")}
                      </p>
                    ) : members.length === 0 ? (
                      <p className="text-sm text-muted-foreground">
                        {t("noAssignableMembers")}
                      </p>
                    ) : (
                      <AutoTable
                        key={selected.id}
                        data={members}
                        rowKey="subject"
                        searchable={(member) =>
                          `${member.display_name} ${member.email} ${member.subject}`
                        }
                        loading={refreshingDetails}
                        onRefresh={() => {
                          setRefreshingDetails(true)
                          setDetailsRevision((value) => value + 1)
                        }}
                        sticky={{ maxHeight: "24rem" }}
                        columns={[
                          {
                            field: "assignment",
                            type: "custom",
                            name: t("roleMembers"),
                            width: "100px",
                            render: (_value, member) => (
                              <label className="inline-flex min-h-11 min-w-11 items-center justify-center">
                                <Checkbox
                                  aria-label={`${t("roleMembers")} ${member.display_name}`}
                                  data-role-member-id={member.subject}
                                  checked={boundMembers.includes(
                                    member.subject
                                  )}
                                  disabled={
                                    busy ||
                                    !allowed("role_manage_member") ||
                                    (system &&
                                      (selected.builtin || member.protected)) ||
                                    member.status !== "active"
                                  }
                                  onCheckedChange={(value) =>
                                    void toggleMember(
                                      member.subject,
                                      value === true
                                    )
                                  }
                                />
                              </label>
                            ),
                          },
                          {
                            field: "display_name",
                            type: "custom",
                            name: t("displayName"),
                            sortable: true,
                            render: (_value, member) => (
                              <div className="min-w-40 break-words whitespace-normal">
                                <strong className="block text-sm">
                                  {member.display_name}
                                </strong>
                                <small className="mt-1 block text-xs text-muted-foreground">
                                  {member.email || member.subject}
                                </small>
                                {!system &&
                                  boundMembers.includes(member.subject) && (
                                    <RoleMemberEnd
                                      key={`${selected.id}:${member.subject}`}
                                      end={memberEnds[member.subject] ?? null}
                                      roleID={selected.id}
                                      principalID={member.subject}
                                      busy={busy}
                                      readonly={!allowed("role_manage_member")}
                                      onChange={(value) =>
                                        setMemberEnd(member.subject, value)
                                      }
                                    />
                                  )}
                              </div>
                            ),
                          },
                          {
                            field: "status",
                            type: "custom",
                            name: t("status"),
                            render: (_value, member) => (
                              <Badge
                                variant={
                                  member.status === "active"
                                    ? "secondary"
                                    : "destructive"
                                }
                              >
                                {member.status === "active"
                                  ? t("active")
                                  : t("disable")}
                              </Badge>
                            ),
                          },
                        ]}
                      />
                    )}
                  </section>
                  {!system && (
                    <DirectoryBindingsSection
                      title={t("groups")}
                      href="../groups"
                      linkLabel={t("openGroupDirectory")}
                      kind="group"
                      icon={<UsersRound size={15} />}
                      loading={loadingDetails || refreshingDetails}
                      busy={busy || !allowed("role_manage_assignee")}
                      items={directoryGroups.map((group) => ({
                        id: group.id,
                        name: group.name,
                        detail: group.description || t("staticGroup"),
                        status: group.status,
                      }))}
                      boundIDs={directoryBindings
                        .filter((binding) => binding.assignee_type === "group")
                        .map((binding) => binding.assignee_id)}
                      onToggle={toggleDirectoryBinding}
                      onRefresh={() => {
                        setRefreshingDetails(true)
                        setDetailsRevision((value) => value + 1)
                      }}
                    />
                  )}
                </TabsContent>
                <TabsContent
                  value="permissions"
                  className="m-0 min-h-0 p-5 lg:flex-1 lg:overflow-y-auto lg:overscroll-contain"
                  data-role-panel="permissions"
                >
                  <section>
                    <h3 className="mb-3 border-b pb-2 text-xs font-semibold text-muted-foreground uppercase">
                      {t("permissionGrants")}
                    </h3>
                    {Object.entries(permissionGroups).map(
                      ([resource, permissions]) => (
                        <section key={resource} className="mb-6">
                          <h4 className="mb-3 text-xs font-semibold text-muted-foreground uppercase">
                            {t.has(`permissionResource_${resource}`)
                              ? t(`permissionResource_${resource}`)
                              : resource}
                          </h4>
                          <div className="grid gap-2 md:grid-cols-2">
                            {permissions.map((permission) => {
                              const grant = grants.find(
                                (item) =>
                                  item.permission_code === permission.code
                              )
                              return (
                                <div
                                  key={permission.code}
                                  className="grid min-h-14 grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 gap-y-2 rounded-md border p-3 hover:bg-muted/30 sm:grid-cols-[auto_minmax(0,1fr)_auto]"
                                  data-permission-code={permission.code}
                                >
                                  <Checkbox
                                    aria-label={t("grantNamed", {
                                      permission:
                                        permission.code === "tenant_administer"
                                          ? t("tenantAdministration")
                                          : permission.summary,
                                    })}
                                    checked={Boolean(grant)}
                                    disabled={
                                      busy ||
                                      selected.builtin ||
                                      loadingDetails ||
                                      !allowed("role_grant_permission")
                                    }
                                    onCheckedChange={(value) =>
                                      void togglePermission(
                                        permission.code,
                                        value === true
                                      )
                                    }
                                  />
                                  <span className="min-w-0 flex-1">
                                    <strong className="block text-sm">
                                      {permission.code === "tenant_administer"
                                        ? t("tenantAdministration")
                                        : permission.summary}
                                    </strong>
                                    <small className="mt-1 block font-mono text-xs text-muted-foreground">
                                      {permission.code}
                                    </small>
                                    {grant?.condition && (
                                      <small
                                        className="mt-1 block text-xs break-words text-muted-foreground"
                                        data-permission-condition-summary
                                      >
                                        {trustedContextConditionSummary(
                                          grant.condition,
                                          t("unrestricted")
                                        )}
                                      </small>
                                    )}
                                  </span>
                                  {!system &&
                                    grant &&
                                    allowed("role_grant_permission") && (
                                      <Button
                                        type="button"
                                        variant="ghost"
                                        size="sm"
                                        className="col-start-2 justify-self-start sm:col-start-3 sm:row-start-1 sm:justify-self-end"
                                        disabled={busy}
                                        onClick={() =>
                                          openPermissionCondition(permission)
                                        }
                                        data-edit-permission-condition={
                                          permission.code
                                        }
                                      >
                                        <SlidersHorizontal />
                                        {t("condition")}
                                      </Button>
                                    )}
                                </div>
                              )
                            })}
                          </div>
                        </section>
                      )
                    )}
                  </section>
                </TabsContent>
              </Tabs>
            </>
          ) : (
            <div className="grid min-h-96 place-items-center text-sm text-muted-foreground">
              {t("selectOrCreateRole")}
            </div>
          )}
        </section>
      </div>
      <Dialog
        open={Boolean(conditionPermission)}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setConditionPermission(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("permissionConditions")}</DialogTitle>
            <DialogDescription>
              {conditionPermission
                ? `${conditionPermission.code === "tenant_administer" ? t("tenantAdministration") : conditionPermission.summary} (${conditionPermission.code})`
                : t("permissionConditionsDescription")}
              {conditionGrant?.condition && (
                <span className="mt-1 block" data-current-condition-summary>
                  {t("currentCondition")}
                  {trustedContextConditionSummary(
                    conditionGrant.condition,
                    t("unrestricted")
                  )}
                </span>
              )}
            </DialogDescription>
          </DialogHeader>
          <AutoForm
            key={conditionPermission?.code}
            id="role-permission-condition-form"
            className="grid gap-4 md:grid-cols-2"
            onSubmit={savePermissionCondition}
            mode="edit"
            initialData={{ ...conditionDraft }}
            onChange={(values) =>
              setConditionDraft((current) =>
                Object.keys(current).every(
                  (key) => current[key as keyof typeof current] === values[key]
                )
                  ? current
                  : {
                      minimumAcr: String(values.minimumAcr ?? ""),
                      clientID: String(values.clientID ?? ""),
                      networkZone: String(values.networkZone ?? ""),
                      timeStart: String(values.timeStart ?? ""),
                      timeEnd: String(values.timeEnd ?? ""),
                      timezone: String(values.timezone ?? ""),
                    }
              )
            }
            fields={{
              minimumAcr: {
                name: t("minimumAcr"),
                type: "select",
                config: {
                  id: "permission-minimum-acr",
                  options: [
                    { value: "", label: t("unrestricted") },
                    { value: "1", label: t("basicAuthentication") },
                    { value: "2", label: t("strongAuthentication") },
                    ...(conditionDraft.minimumAcr &&
                    !["1", "2"].includes(conditionDraft.minimumAcr)
                      ? [
                          {
                            value: conditionDraft.minimumAcr,
                            label: t("authenticationLevel", {
                              level: conditionDraft.minimumAcr,
                            }),
                          },
                        ]
                      : []),
                  ],
                },
              },
              clientID: {
                name: t("oauthClientIdentifier"),
                type: "text",
                config: {
                  id: "permission-client-id",
                  placeholder: t("unrestricted"),
                  maxLength: 255,
                },
              },
              networkZone: {
                name: t("trustedNetworkZone"),
                type: "text",
                config: {
                  id: "permission-network-zone",
                  placeholder: t("unrestricted"),
                  maxLength: 255,
                },
              },
              timezone: {
                name: t("ianaTimezone"),
                type: "text",
                disabled: (values) => !values?.timeStart && !values?.timeEnd,
                required: (values) =>
                  Boolean(values?.timeStart || values?.timeEnd),
                config: { id: "permission-timezone", maxLength: 64 },
              },
              timeStart: {
                name: t("dailyStart"),
                type: "text",
                required: (values) => Boolean(values?.timeEnd),
                config: { id: "permission-time-start", type: "time" },
              },
              timeEnd: {
                name: t("dailyEnd"),
                type: "text",
                required: (values) => Boolean(values?.timeStart),
                config: { id: "permission-time-end", type: "time" },
              },
            }}
          >
            <div
              className="space-y-2 border-t pt-3 text-sm md:col-span-2"
              data-permission-condition-effect
            >
              <strong>{t("conditionEffect")}</strong>
              {condition ? (
                <ul className="list-inside list-disc space-y-1">
                  {conditionDraft.minimumAcr && (
                    <li>
                      {t("requiresAuthenticationLevel", {
                        level: conditionDraft.minimumAcr,
                      })}
                    </li>
                  )}
                  {conditionDraft.clientID.trim() && (
                    <li>
                      {t("requiresOAuthClient", {
                        client: conditionDraft.clientID.trim(),
                      })}
                    </li>
                  )}
                  {conditionDraft.networkZone.trim() && (
                    <li>
                      {t("requiresNetworkZone", {
                        zone: conditionDraft.networkZone.trim(),
                      })}
                    </li>
                  )}
                  {conditionDraft.timeStart && conditionDraft.timeEnd && (
                    <li>
                      {t("requiresTimeWindow", {
                        start: conditionDraft.timeStart,
                        end: conditionDraft.timeEnd,
                        timezone: conditionDraft.timezone,
                      })}
                    </li>
                  )}
                </ul>
              ) : (
                <p>{t("noAdditionalConditions")}</p>
              )}
              <p className="text-muted-foreground">
                {t("conditionGrantBoundary")}
              </p>
            </div>
          </AutoForm>
          <DialogFooter>
            {conditionGrant?.condition && (
              <Button
                variant="destructive"
                disabled={busy}
                onClick={() => void clearPermissionCondition()}
                data-clear-permission-condition
              >
                {t("clearCondition")}
              </Button>
            )}
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setConditionPermission(null)}
            >
              {t("cancel")}
            </Button>
            <Button
              type="submit"
              form="role-permission-condition-form"
              disabled={busy || !condition}
              data-save-permission-condition
            >
              {busy ? t("saving") : t("saveCondition")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
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
              {editing ? t("editRole") : t("createTenantRole")}
            </DialogTitle>
            <DialogDescription>
              {editing ? t("editRoleDescription") : t("createRoleDescription")}
            </DialogDescription>
          </DialogHeader>
          {roleContract.failed && (
            <Alert>
              <Button variant="outline" onClick={roleContract.retry}>
                {t("retryNow")}
              </Button>
            </Alert>
          )}
          <AutoForm
            key={editing?.id ?? "create"}
            id="role-form"
            mode={editing ? "edit" : "add"}
            initialData={
              editing
                ? { name: editing.name, description: editing.description }
                : undefined
            }
            fields={{
              name: { name: t("roleName"), type: "text", value: "" },
              description: { name: t("description"), type: "text", value: "" },
            }}
            contract={roleContract.contract}
            readonly={!allowed(editing ? "role_update" : "role_create")}
            onSubmit={submit}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {t("cancel")}
            </Button>
            <Button
              type="submit"
              form="role-form"
              disabled={
                busy ||
                !roleContract.contract ||
                !allowed(editing ? "role_update" : "role_create")
              }
            >
              {busy ? t("saving") : editing ? t("save") : t("create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

type RoleWorkspaceView = "permissions" | "assignments" | "scope"

function ScopeDepartmentsField({
  value,
  onChange,
  disabled,
  readonly,
  config,
  labelId,
  error,
  errorId,
}: FieldRendererProps) {
  const t = useTranslations("iam")
  const selected = Array.isArray(value) ? (value as string[]) : []
  const departments = config.departments as Department[]
  return (
    <div
      role="group"
      aria-labelledby={labelId}
      aria-describedby={error ? errorId : undefined}
    >
      <div className="grid gap-2 md:grid-cols-2" data-scope-departments>
        {departments.map((department) => {
          const checked = selected.includes(department.id)
          return (
            <label
              key={department.id}
              className="flex min-h-12 items-center gap-3 rounded-md border p-3 hover:bg-muted/30"
            >
              <Checkbox
                data-scope-department-id={department.id}
                checked={checked}
                disabled={
                  disabled ||
                  readonly ||
                  (department.status !== "active" && !checked)
                }
                onCheckedChange={(next) =>
                  onChange(
                    next === true
                      ? [...new Set([...selected, department.id])]
                      : selected.filter((id) => id !== department.id)
                  )
                }
              />
              <span className="min-w-0 flex-1 text-sm break-words">
                {department.name}
              </span>
              {department.status !== "active" && (
                <Badge variant="destructive">{t("disable")}</Badge>
              )}
            </label>
          )
        })}
      </div>
      {error && (
        <p id={errorId} className="mt-1 text-xs text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}

interface DirectoryOption {
  id: string
  name: string
  detail: string
  status: "active" | "disabled"
}

function localTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"
}

function roleWorkspaceView(value: string | null): RoleWorkspaceView {
  return value === "assignments" || value === "scope" ? value : "permissions"
}

function RoleExpiryField({
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
        type="datetime-local"
        className="h-8 w-auto text-xs"
        aria-labelledby={labelId}
        aria-describedby={error ? errorId : undefined}
        aria-invalid={Boolean(error)}
        value={typeof value === "string" ? localInputValue(value) : ""}
        disabled={disabled || readonly}
        onChange={(event) => {
          const local = event.target.value
          const instant = new Date(local)
          onChange(
            !local
              ? undefined
              : Number.isNaN(instant.getTime())
                ? local
                : instant.toISOString()
          )
        }}
      />
      {error && (
        <p id={errorId} className="mt-1 text-xs text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}

function RoleMemberEnd({
  end,
  roleID,
  principalID,
  busy,
  readonly,
  onChange,
}: {
  end: string | null
  roleID: string
  principalID: string
  busy: boolean
  readonly: boolean
  onChange: (value: string | null) => Promise<void>
}) {
  const t = useTranslations("iam")
  const [editing, setEditing] = useState(false)
  const route = useMemo(
    () =>
      editing && !readonly
        ? apiRoute("put", "/roles/{role_id}/members/{principal_id}", {
            path: { role_id: roleID, principal_id: principalID },
          })
        : null,
    [editing, readonly, roleID, principalID]
  )
  const metadata = useRequestContract(route)
  if (!editing)
    return (
      <button
        type="button"
        className="mt-1.5 inline-flex items-center gap-1.5 text-xs text-muted-foreground underline-offset-2 hover:underline"
        data-role-member-end={end ?? ""}
        disabled={busy || readonly}
        onClick={() => setEditing(true)}
      >
        <CalendarClock size={13} />
        {end
          ? `${t("roleMemberUntil")} ${new Date(end).toLocaleString()}`
          : t("roleMemberNoEnd")}
      </button>
    )
  return (
    <div className="mt-2" data-role-member-end-editor>
      {metadata.failed && (
        <Button variant="outline" onClick={metadata.retry}>
          {t("retryNow")}
        </Button>
      )}
      <AutoForm
        mode="edit"
        readonly={readonly}
        contract={metadata.contract}
        initialData={{ expires_at: end ?? undefined }}
        fields={{
          expires_at: {
            name: t("roleMemberUntil"),
            type: "custom",
            component: RoleExpiryField,
            disabled: busy,
            col: 2,
          },
        }}
        onSubmit={async (values) => {
          await onChange((values.expires_at as string | undefined) ?? null)
          setEditing(false)
        }}
      >
        <div className="flex flex-wrap gap-2 md:col-span-2">
          <Button
            type="submit"
            size="sm"
            variant="secondary"
            disabled={busy || readonly || !metadata.contract}
          >
            {t("roleMemberEndSave")}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={busy}
            onClick={() => setEditing(false)}
          >
            {t("cancel")}
          </Button>
        </div>
      </AutoForm>
    </div>
  )
}

// localInputValue renders an instant for a datetime-local control, which reads
// and writes local wall-clock time with no zone of its own.
function localInputValue(iso: string | null): string {
  if (!iso) return ""
  const moment = new Date(iso)
  if (Number.isNaN(moment.getTime())) return ""
  const pad = (value: number) => String(value).padStart(2, "0")
  return (
    `${moment.getFullYear()}-${pad(moment.getMonth() + 1)}-${pad(moment.getDate())}` +
    `T${pad(moment.getHours())}:${pad(moment.getMinutes())}`
  )
}

function DirectoryBindingsSection({
  title,
  href,
  linkLabel,
  kind,
  icon,
  items,
  boundIDs,
  loading,
  busy,
  onToggle,
  onRefresh,
}: {
  title: string
  href: string
  linkLabel: string
  kind: RoleDirectoryBinding["assignee_type"]
  icon: ReactNode
  items: DirectoryOption[]
  boundIDs: string[]
  loading: boolean
  busy: boolean
  onRefresh: () => void
  onToggle: (
    kind: RoleDirectoryBinding["assignee_type"],
    id: string,
    checked: boolean
  ) => void
}) {
  const t = useTranslations("iam")

  return (
    <section className="mb-7" data-directory-section={kind}>
      <div className="mb-3 flex min-h-9 flex-wrap items-center justify-between gap-3 border-b pb-2">
        <h3 className="flex items-center gap-2 text-xs font-semibold text-muted-foreground uppercase">
          {icon}
          {title}
        </h3>
        <Button variant="ghost" size="sm" asChild>
          <Link to={href} data-role-directory-link={kind}>
            <ExternalLink />
            {linkLabel}
          </Link>
        </Button>
      </div>
      {loading ? (
        <p className="text-sm text-muted-foreground">{t("loadingShort")}</p>
      ) : items.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {t("noAssignableItems")}
        </p>
      ) : (
        <AutoTable
          data={items}
          rowKey="id"
          searchable={(item) => `${item.name} ${item.detail}`}
          onRefresh={onRefresh}
          sticky={{ maxHeight: "24rem" }}
          columns={[
            {
              field: "assignment",
              type: "custom",
              name: title,
              width: "100px",
              render: (_value, item) => (
                <label className="inline-flex min-h-11 min-w-11 items-center justify-center">
                  <Checkbox
                    data-assignee-type={kind}
                    data-assignee-id={item.id}
                    aria-label={`${title} ${item.name}`}
                    checked={boundIDs.includes(item.id)}
                    disabled={
                      busy ||
                      (item.status !== "active" && !boundIDs.includes(item.id))
                    }
                    onCheckedChange={(value) =>
                      onToggle(kind, item.id, value === true)
                    }
                  />
                </label>
              ),
            },
            {
              field: "name",
              type: "custom",
              name: t("name"),
              sortable: true,
              render: (_value, item) => (
                <div className="min-w-40 break-words whitespace-normal">
                  <strong className="block text-sm">{item.name}</strong>
                  <small className="mt-1 block text-xs text-muted-foreground">
                    {item.detail}
                  </small>
                </div>
              ),
            },
            {
              field: "status",
              type: "custom",
              name: t("status"),
              render: (_value, item) => (
                <Badge
                  variant={
                    item.status === "active" ? "secondary" : "destructive"
                  }
                >
                  {item.status === "active" ? t("active") : t("disable")}
                </Badge>
              ),
            },
          ]}
        />
      )}
    </section>
  )
}
