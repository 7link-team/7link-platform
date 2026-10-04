import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { systemRoleLabel } from "../../lib/membership-presentation"
import { Link, useSearchParams } from "react-router"
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
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
import { Label } from "@workspace/ui/components/label"
import { SimpleSelect } from "@workspace/ui/components/select"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@workspace/ui/components/tabs"
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
  const [error, setError] = useState("")
  const [loadError, setLoadError] = useState("")
  const [scopeDraft, setScopeDraft] = useState<DataScope>("all")
  const [scopeDepartmentIDs, setScopeDepartmentIDs] = useState<string[]>([])
  const scopeDraftVersion = useRef(0)
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
    ]).then(
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
          members: nextMembers.status === "fulfilled" ? nextMembers.value : [],
          bindings: nextBindings.value,
          groups: nextGroups.status === "fulfilled" ? nextGroups.value : [],

          dataScope: nextDataScope.value,
          departments:
            nextDepartments.status === "fulfilled" ? nextDepartments.value : [],
        })
        // A read started before an edit must not reset the newer scope draft.
        if (scopeVersion === scopeDraftVersion.current) {
          setScopeDraft(nextDataScope.value.scope)
          setScopeDepartmentIDs(nextDataScope.value.department_ids)
        }
      }
    )
    return () => {
      current = false
    }
  }, [selected, allowed, api, system])

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

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const body = {
      name: String(data.get("name")),
      description: String(data.get("description")),
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
    } catch (cause) {
      setError((cause as Error).message)
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

  const savePermissionCondition = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
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
    const nextEnds = system
      ? []
      : await iamApi.roleMemberGrants(selected.id)
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
    } catch (cause) {
      setError((cause as Error).message)
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

  const saveDataScope = async () => {
    if (!selected) return
    scopeDraftVersion.current += 1
    setBusy(true)
    setError("")
    try {
      const dataScope = await iamApi.setRoleDataScope(selected.id, {
        scope: scopeDraft,
        department_ids:
          scopeDraft === "selected_departments" ? scopeDepartmentIDs : [],
      })
      setDetails((current) =>
        current.roleID === selected.id ? { ...current, dataScope } : current
      )
      setScopeDepartmentIDs(dataScope.department_ids)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
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
      <div className="grid min-h-[620px] overflow-hidden rounded-md border bg-card lg:grid-cols-[280px_minmax(0,1fr)]">
        <aside className="border-b lg:border-r lg:border-b-0">
          <header className="flex h-12 items-center justify-between border-b bg-muted/30 px-4 text-sm font-semibold">
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
        <section className="min-w-0">
          {selected ? (
            <>
              <header className="flex min-h-20 flex-wrap items-center justify-between gap-4 border-b px-5 py-3">
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
                <p className="border-b px-5 py-3 text-sm text-muted-foreground">
                  {t("protectedSuperadminAuthority")}
                </p>
              )}
              <Tabs value={view} onValueChange={selectView}>
                <TabsList
                  variant="line"
                  className="flex w-full justify-start gap-2 overflow-x-auto border-b px-3 sm:gap-5 sm:px-5"
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
                  className="m-0 p-5"
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
                        <div className="grid max-w-md gap-2">
                          <Label htmlFor="role-data-scope">{t("scope")}</Label>
                          <SimpleSelect
                            id="role-data-scope"
                            value={scopeDraft}
                            disabled={busy || !allowed("role_update")}
                            onValueChange={(value) => {
                              scopeDraftVersion.current += 1
                              setScopeDraft(value as DataScope)
                            }}
                            options={{
                              all: t("allData"),
                              self: t("ownData"),
                              department: t("ownDepartment"),
                              department_and_descendants: t(
                                "departmentDescendants"
                              ),
                              selected_departments: t("selectedDepartments"),
                            }}
                          />
                        </div>
                        {scopeDraft === "selected_departments" && (
                          <div
                            className="grid gap-2 md:grid-cols-2"
                            data-scope-departments
                          >
                            {departments.map((department) => {
                              const checked = scopeDepartmentIDs.includes(
                                department.id
                              )
                              return (
                                <label
                                  key={department.id}
                                  className="flex min-h-12 items-center gap-3 rounded-md border p-3 hover:bg-muted/30"
                                >
                                  <Checkbox
                                    data-scope-department-id={department.id}
                                    checked={checked}
                                    disabled={
                                      busy ||
                                      !allowed("role_update") ||
                                      (department.status !== "active" &&
                                        !checked)
                                    }
                                    onCheckedChange={(value) => {
                                      scopeDraftVersion.current += 1
                                      setScopeDepartmentIDs((current) =>
                                        value === true
                                          ? [
                                              ...new Set([
                                                ...current,
                                                department.id,
                                              ]),
                                            ]
                                          : current.filter(
                                              (id) => id !== department.id
                                            )
                                      )
                                    }}
                                  />
                                  <span className="min-w-0 flex-1 text-sm break-words">
                                    {department.name}
                                  </span>
                                  {department.status !== "active" && (
                                    <Badge variant="destructive">
                                      {t("disable")}
                                    </Badge>
                                  )}
                                </label>
                              )
                            })}
                          </div>
                        )}
                        <div>
                          {allowed("role_update") && (
                            <Button
                              size="sm"
                              disabled={busy}
                              onClick={() => void saveDataScope()}
                              data-save-data-scope
                            >
                              {t("saveDataScope")}
                            </Button>
                          )}
                        </div>
                      </div>
                    )}
                  </section>
                </TabsContent>
                <TabsContent
                  value="assignments"
                  className="m-0 p-5"
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
                      <div className="grid gap-2 md:grid-cols-2">
                        {members.map((member) => (
                          <label
                            key={member.subject}
                            className="flex min-h-14 items-center gap-3 rounded-md border p-3 hover:bg-muted/30"
                          >
                            <Checkbox
                              data-role-member-id={member.subject}
                              checked={boundMembers.includes(member.subject)}
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
                            <span className="min-w-0 flex-1">
                              <strong className="block text-sm break-words">
                                {member.display_name}
                              </strong>
                              <small className="mt-1 block text-xs break-words text-muted-foreground">
                                {member.email || member.subject}
                              </small>
                              {!system &&
                                boundMembers.includes(member.subject) && (
                                  <RoleMemberEnd
                                    end={memberEnds[member.subject] ?? null}
                                    disabled={
                                      busy || !allowed("role_manage_member")
                                    }
                                    onChange={(value) =>
                                      void setMemberEnd(member.subject, value)
                                    }
                                  />
                                )}
                            </span>
                            {member.status !== "active" && (
                              <Badge variant="destructive">
                                {t("disable")}
                              </Badge>
                            )}
                          </label>
                        ))}
                      </div>
                    )}
                  </section>
                  {!system && (
                    <DirectoryBindingsSection
                      title={t("groups")}
                      href="../groups"
                      linkLabel={t("openGroupDirectory")}
                      kind="group"
                      icon={<UsersRound size={15} />}
                      loading={loadingDetails}
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
                    />
                  )}
                </TabsContent>
                <TabsContent
                  value="permissions"
                  className="m-0 p-5"
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
          <form
            id="role-permission-condition-form"
            className="grid gap-4 md:grid-cols-2"
            onSubmit={savePermissionCondition}
          >
            <div className="grid gap-2">
              <Label htmlFor="permission-minimum-acr">{t("minimumAcr")}</Label>
              <SimpleSelect
                id="permission-minimum-acr"
                value={conditionDraft.minimumAcr}
                onValueChange={(value) =>
                  setConditionDraft((current) => ({
                    ...current,
                    minimumAcr: value,
                  }))
                }
                options={[
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
                ]}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="permission-client-id">
                {t("oauthClientIdentifier")}
              </Label>
              <Input
                id="permission-client-id"
                value={conditionDraft.clientID}
                onChange={(event) =>
                  setConditionDraft((current) => ({
                    ...current,
                    clientID: event.target.value,
                  }))
                }
                placeholder={t("unrestricted")}
                maxLength={255}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="permission-network-zone">
                {t("trustedNetworkZone")}
              </Label>
              <Input
                id="permission-network-zone"
                value={conditionDraft.networkZone}
                onChange={(event) =>
                  setConditionDraft((current) => ({
                    ...current,
                    networkZone: event.target.value,
                  }))
                }
                placeholder={t("unrestricted")}
                maxLength={255}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="permission-timezone">{t("ianaTimezone")}</Label>
              <Input
                id="permission-timezone"
                value={conditionDraft.timezone}
                disabled={!conditionDraft.timeStart && !conditionDraft.timeEnd}
                onChange={(event) =>
                  setConditionDraft((current) => ({
                    ...current,
                    timezone: event.target.value,
                  }))
                }
                required={Boolean(
                  conditionDraft.timeStart || conditionDraft.timeEnd
                )}
                maxLength={64}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="permission-time-start">{t("dailyStart")}</Label>
              <Input
                id="permission-time-start"
                type="time"
                value={conditionDraft.timeStart}
                onChange={(event) =>
                  setConditionDraft((current) => ({
                    ...current,
                    timeStart: event.target.value,
                  }))
                }
                required={Boolean(conditionDraft.timeEnd)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="permission-time-end">{t("dailyEnd")}</Label>
              <Input
                id="permission-time-end"
                type="time"
                value={conditionDraft.timeEnd}
                onChange={(event) =>
                  setConditionDraft((current) => ({
                    ...current,
                    timeEnd: event.target.value,
                  }))
                }
                required={Boolean(conditionDraft.timeStart)}
              />
            </div>
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
          </form>
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
          <form
            key={editing?.id ?? "create"}
            id="role-form"
            className="grid gap-4"
            onSubmit={submit}
          >
            <div className="grid gap-2">
              <Label htmlFor="role-name">{t("roleName")}</Label>
              <Input
                id="role-name"
                name="name"
                defaultValue={editing?.name}
                required
                maxLength={128}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="role-description">{t("description")}</Label>
              <Input
                id="role-description"
                name="description"
                defaultValue={editing?.description}
                maxLength={4096}
              />
            </div>
          </form>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {t("cancel")}
            </Button>
            <Button type="submit" form="role-form" disabled={busy}>
              {busy ? t("saving") : editing ? t("save") : t("create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

type RoleWorkspaceView = "permissions" | "assignments" | "scope"

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

function RoleMemberEnd({
  end,
  disabled,
  onChange,
}: {
  end: string | null
  disabled: boolean
  onChange: (value: string | null) => void
}) {
  const t = useTranslations("iam")
  const [draft, setDraft] = useState(() => localInputValue(end))
  const [editing, setEditing] = useState(false)

  if (!editing)
    return (
      <button
        type="button"
        className="mt-1.5 inline-flex items-center gap-1.5 text-xs text-muted-foreground underline-offset-2 hover:underline"
        data-role-member-end={end ?? ""}
        disabled={disabled}
        onClick={(event) => {
          event.preventDefault()
          // The draft is taken when the editor opens, which is the only moment it
          // can disagree with what the service holds.
          setDraft(localInputValue(end))
          setEditing(true)
        }}
      >
        <CalendarClock size={13} />
        {end
          ? `${t("roleMemberUntil")} ${new Date(end).toLocaleString()}`
          : t("roleMemberNoEnd")}
      </button>
    )

  return (
    <span
      className="mt-2 flex flex-wrap items-center gap-2"
      onClick={(event) => event.preventDefault()}
    >
      <Input
        type="datetime-local"
        className="h-8 w-auto text-xs"
        value={draft}
        disabled={disabled}
        onChange={(event) => setDraft(event.target.value)}
      />
      <Button
        type="button"
        size="sm"
        variant="secondary"
        disabled={disabled}
        onClick={() => {
          setEditing(false)
          onChange(draft ? new Date(draft).toISOString() : null)
        }}
      >
        {t("roleMemberEndSave")}
      </Button>
      <Button
        type="button"
        size="sm"
        variant="ghost"
        onClick={() => {
          setDraft(localInputValue(end))
          setEditing(false)
        }}
      >
        {t("cancel")}
      </Button>
    </span>
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
        <div className="grid gap-2 md:grid-cols-2">
          {items.map((item) => {
            const checked = boundIDs.includes(item.id)
            return (
              <label
                key={item.id}
                className="flex min-h-14 items-center gap-3 rounded-md border p-3 hover:bg-muted/30"
              >
                <Checkbox
                  data-assignee-type={kind}
                  data-assignee-id={item.id}
                  aria-label={`${title} ${item.name}`}
                  checked={checked}
                  disabled={busy || (item.status !== "active" && !checked)}
                  onCheckedChange={(value) =>
                    onToggle(kind, item.id, value === true)
                  }
                />
                <span className="min-w-0 flex-1">
                  <strong className="block text-sm break-words">
                    {item.name}
                  </strong>
                  <small className="mt-1 block text-xs break-words text-muted-foreground">
                    {item.detail}
                  </small>
                </span>
                {item.status !== "active" && (
                  <Badge variant="destructive">{t("disable")}</Badge>
                )}
              </label>
            )
          })}
        </div>
      )}
    </section>
  )
}
