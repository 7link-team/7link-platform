import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Link } from "react-router"
import { UserAvatar } from "../../components/user-avatar"
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react"
import {
  Building2,
  GitBranch,
  KeyRound,
  Loader2,
  Pencil,
  Plus,
  Search,
  ShieldAlert,
  ShieldCheck,
  UserRoundCheck,
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
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@workspace/ui/components/sheet"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
import { Alert } from "../../components/alert"
import { PrincipalLifecycleDialog } from "./lifecycle-dialog"
import { PageHeader } from "../../components/page-header"
import {
  createIamApi,
  getTenant,
  type Department,
  type FieldConstraint,
  type Member,
  type MemberAuthorizationSnapshot,
  type MemberAuthorizationSource,
  type Principal,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

export default function PrincipalsPage() {
  const [tenant, setTenantValue] = useState(getTenant)
  useEffect(() => {
    const sync = () => setTenantValue(getTenant())
    window.addEventListener("tenant-change", sync)
    return () => window.removeEventListener("tenant-change", sync)
  }, [])
  return <TenantPrincipals key={tenant} tenant={tenant} />
}

function TenantPrincipals({ tenant }: { tenant: string }) {
  const locale = useLocale()

  const t = useTranslations("iam")

  const iamApi = useMemo(() => createIamApi(undefined, () => tenant), [tenant])
  const allowed = useAllowed()
  const [fields, setFields] = useState<FieldConstraint["fields"]>({})
  const [items, setItems] = useState<Principal[]>([])
  const [members, setMembers] = useState<Member[]>([])
  const membershipByPrincipal = useMemo(
    () => new Map(members.map((member) => [member.subject, member])),
    [members]
  )
  const [departments, setDepartments] = useState<Department[]>([])
  const [search, setSearch] = useState("")
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Principal | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState("")
  const loadGeneration = useRef(0)
  const [departmentID, setDepartmentID] = useState("")
  const refresh = useCallback(async () => {
    const generation = ++loadGeneration.current
    setLoading(true)
    setLoadError("")
    // The department directory answers to a different permission than the
    // principal list, so a member who may read principals but not departments
    // loses one column rather than the whole page.
    return Promise.allSettled([
      iamApi.principals(search),
      iamApi.members(),
      allowed("dept_view") ? iamApi.departments() : Promise.resolve([]),
      iamApi.effectiveFields("user_profile"),
    ])
      .then(([result, nextMembers, nextDepartments, nextFields]) => {
        if (generation !== loadGeneration.current) return
        setItems(result.status === "fulfilled" ? result.value.items : [])
        setMembers(nextMembers.status === "fulfilled" ? nextMembers.value : [])
        setDepartments(
          nextDepartments.status === "fulfilled" ? nextDepartments.value : []
        )
        setFields(
          nextFields.status === "fulfilled" ? nextFields.value.fields : {}
        )
        setLoadError(
          result.status === "rejected"
            ? ((result.reason as Error | undefined)?.message ?? "")
            : ""
        )
      })
      .finally(() => {
        if (generation === loadGeneration.current) setLoading(false)
      })
  }, [search, iamApi, allowed])
  useEffect(() => {
    let active = true
    void Promise.resolve().then(() => {
      if (active) void refresh()
    })
    return () => {
      active = false
    }
  }, [refresh])

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    try {
      if (editing) {
        const membership = members.find(
          (member) => member.subject === editing.id
        )
        if (!membership) throw new Error(t("pageError"))
        const changes: { display_name?: string; email?: string } = {}
        for (const field of ["display_name", "email"] as const) {
          if (!data.has(field) || !fields[field]?.update) continue
          const value = String(data.get(field))
          const original =
            fields[field]?.read === "allow" ? (membership[field] ?? "") : ""
          if (value !== original) changes[field] = value
        }
        if (Object.keys(changes).length)
          await iamApi.updateMember(editing.id, changes)
        if (membership && (membership.department_id ?? "") !== departmentID)
          await iamApi.updateMember(editing.id, { department_id: departmentID })
      } else {
        await iamApi.createPrincipal({
          login_name: String(data.get("login_name")),
          password: String(data.get("password")),
          ...(data.has("display_name")
            ? { display_name: String(data.get("display_name")) }
            : {}),
          ...(data.has("email") ? { email: String(data.get("email")) } : {}),
        })
      }
      setOpen(false)
      setEditing(null)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const [lifecycle, setLifecycle] = useState<Principal | null>(null)
  const [authorizationPrincipal, setAuthorizationPrincipal] =
    useState<Principal | null>(null)
  const [authorization, setAuthorization] =
    useState<MemberAuthorizationSnapshot | null>(null)
  const [authorizationBusy, setAuthorizationBusy] = useState(false)
  const [authorizationError, setAuthorizationError] = useState("")

  const inspectAuthorization = async (principal: Principal) => {
    setAuthorizationPrincipal(principal)
    setAuthorization(null)
    setAuthorizationError("")
    setAuthorizationBusy(true)
    try {
      setAuthorization(await iamApi.memberAuthorizationSources(principal.id))
    } catch (cause) {
      setAuthorizationError((cause as Error).message)
    } finally {
      setAuthorizationBusy(false)
    }
  }
  const editingMembership = editing
    ? members.find((member) => member.subject === editing.id)
    : undefined
  const toggle = async (principal: Principal) => {
    setBusy(true)
    setError("")
    try {
      if (principal.status === "active")
        await iamApi.disablePrincipal(principal.id)
      else await iamApi.restorePrincipal(principal.id)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <PageHeader
        title={t("principalMembers")}
        description={t("principalManagementDescription")}
        action={
          <div className="flex flex-wrap gap-2">
            {allowed("platform_administer") && (
              <Link
                className="inline-flex min-h-11 items-center rounded-md border px-3 text-sm"
                to="/platform/closure-requests"
              >
                {t("closureRequests")}
              </Link>
            )}
            {allowed("user_create") && (
              <Button
                disabled={!fields.login_name?.create}
                onClick={() => {
                  setEditing(null)
                  setDepartmentID("")
                  setOpen(true)
                }}
              >
                <Plus />
                {t("createPrincipal")}
              </Button>
            )}
          </div>
        }
      />
      <div className="mb-4 flex items-center justify-between gap-3">
        <label className="relative w-full max-w-md">
          <Search className="absolute top-2.5 left-3 size-4 text-muted-foreground" />
          <Input
            className="pl-9"
            aria-label={t("searchPrincipals")}
            placeholder={t("loginNameOrEmail")}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </label>
        <span className="text-xs whitespace-nowrap text-muted-foreground">
          {!loading && !loadError && (
            <>
              {items.length} {t("items")}
            </>
          )}
        </span>
      </div>
      {error && <Alert>{error}</Alert>}
      {loadError && (
        <Alert>
          <p>{t.has(loadError) ? t(loadError) : loadError}</p>
          <Button variant="outline" onClick={() => void refresh()}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      {loading && <p role="status">{t("loading")}</p>}
      <div className="overflow-hidden rounded-md border bg-card">
        <Table className="min-w-[900px] whitespace-nowrap">
          <TableHeader>
            <TableRow>
              <TableHead>{t("principal")}</TableHead>
              <TableHead>{t("loginIdentifier")}</TableHead>
              <TableHead>{t("status")}</TableHead>
              <TableHead>{t("primaryDepartment")}</TableHead>
              <TableHead>{t("updatedAt")}</TableHead>
              <TableHead className="w-56 text-right">{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(loading || loadError ? [] : items).map((principal) => (
              <TableRow key={principal.id}>
                <TableCell>
                  <div className="flex items-center gap-3">
                    <UserAvatar id={principal.id} avatar={principal.avatar} />
                    <div>
                      <strong className="block text-sm">
                        {membershipByPrincipal.get(principal.id)
                          ?.display_name ||
                          principal.display_name ||
                          principal.login_name ||
                          principal.id}
                      </strong>
                      <span className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                        {principal.email
                          ? `${t("primaryEmail")}: ${principal.email}`
                          : principal.id}
                        {principal.email &&
                          principal.email_verified !== undefined && (
                            <Badge
                              variant={
                                principal.email_verified
                                  ? "secondary"
                                  : "outline"
                              }
                              className="px-1.5 py-0 text-[10px]"
                            >
                              {principal.email_verified
                                ? t("verified")
                                : t("unverified")}
                            </Badge>
                          )}
                      </span>
                      {membershipByPrincipal.get(principal.id)?.email &&
                        fields.email?.read === "allow" && (
                          <span className="block text-xs text-muted-foreground">
                            {t("tenantContactEmail")}:{" "}
                            {membershipByPrincipal.get(principal.id)?.email}
                          </span>
                        )}
                    </div>
                  </div>
                </TableCell>
                <TableCell className="font-mono text-xs">
                  {principal.login_name}
                </TableCell>
                <TableCell>
                  <Badge
                    variant={
                      principal.status === "active"
                        ? "secondary"
                        : "destructive"
                    }
                  >
                    {principal.status === "active" ? t("active") : t("disable")}
                  </Badge>
                </TableCell>
                <TableCell
                  className="text-sm"
                  data-member-department={principal.id}
                >
                  {departments.find(
                    (department) =>
                      department.id ===
                      members.find((member) => member.subject === principal.id)
                        ?.department_id
                  )?.name ??
                    (members.find((member) => member.subject === principal.id)
                      ?.department_id ||
                      t("unassigned"))}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {new Date(principal.updated_at).toLocaleString(locale)}
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex justify-end gap-1">
                    {allowed("role_view") && (
                      <Button
                        variant="ghost"
                        size="icon"
                        disabled={busy}
                        data-view-authorization-sources={principal.id}
                        onClick={() => void inspectAuthorization(principal)}
                        aria-label={t("viewNamedAuthorizationSources", {
                          name:
                            principal.display_name ||
                            principal.login_name ||
                            principal.id,
                        })}
                        title={t("authorizationSources")}
                      >
                        <ShieldCheck />
                      </Button>
                    )}
                    {allowed("user_update") && (
                      <Button
                        variant="ghost"
                        size="icon"
                        disabled={busy}
                        onClick={() => {
                          setEditing(principal)
                          setDepartmentID(
                            members.find(
                              (member) => member.subject === principal.id
                            )?.department_id ?? ""
                          )
                          setOpen(true)
                        }}
                        aria-label={t("editNamed", {
                          name:
                            principal.display_name ||
                            principal.login_name ||
                            principal.id,
                        })}
                        title={t("editPrincipal")}
                      >
                        <Pencil />
                      </Button>
                    )}
                    {allowed("platform_administer") && (
                      <Button
                        variant="ghost"
                        size="icon"
                        disabled={busy}
                        onClick={() => setLifecycle(principal)}
                        aria-label={t("namedRetentionErasure", {
                          name:
                            principal.display_name ||
                            principal.login_name ||
                            principal.id,
                        })}
                        title={t("retentionErasure")}
                      >
                        <ShieldAlert />
                      </Button>
                    )}
                    {allowed("user_update") && (
                      <Button
                        variant={
                          principal.status === "active"
                            ? "destructive"
                            : "outline"
                        }
                        size="sm"
                        disabled={busy}
                        onClick={() => void toggle(principal)}
                      >
                        {principal.status === "active"
                          ? t("disable")
                          : t("restore")}
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {!loading && !loadError && items.length === 0 && (
          <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
            <UserRoundCheck className="mb-2 size-7" />
            {t("noPrincipals")}
          </div>
        )}
      </div>
      <Dialog
        open={open}
        onOpenChange={(nextOpen) => {
          setOpen(nextOpen)
          if (!nextOpen) {
            setEditing(null)
            setDepartmentID("")
          }
        }}
      >
        <DialogContent className="grid max-h-[calc(100svh-2rem)] gap-4 overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              {editing ? t("editPrincipal") : t("createLocalPrincipal")}
            </DialogTitle>
            <DialogDescription>
              {editing
                ? t("editTenantContactDescription")
                : t("createPrincipalDescription")}
            </DialogDescription>
          </DialogHeader>
          <form
            key={editing?.id ?? "create"}
            id="principal-form"
            className="grid gap-4"
            onSubmit={submit}
          >
            {!editing && (
              <Field label={t("loginIdentifier")} name="login_name" required />
            )}
            <Field
              label={t("displayName")}
              name="display_name"
              defaultValue={
                fields.display_name?.read === "allow"
                  ? editingMembership?.display_name
                  : undefined
              }
              disabled={
                !(editing
                  ? fields.display_name?.update
                  : fields.display_name?.create)
              }
              required={!editing && Boolean(fields.display_name?.create)}
            />
            {editing && editingMembership && (
              <div className="grid gap-2">
                <Label htmlFor="principal-department">
                  {t("primaryDepartment")}
                </Label>
                <SimpleSelect
                  id="principal-department"
                  value={departmentID || "__none__"}
                  onValueChange={(value) =>
                    setDepartmentID(value === "__none__" ? "" : value)
                  }
                  options={[
                    { value: "__none__", label: t("unassigned") },
                    ...departments
                      .filter(
                        (department) =>
                          department.status === "active" ||
                          department.id === departmentID
                      )
                      .map((department) => ({
                        value: department.id,
                        label:
                          department.status === "active"
                            ? department.name
                            : t("disabledNamed", { name: department.name }),
                      })),
                  ]}
                />
              </div>
            )}
            <Field
              label={editing ? t("tenantContactEmail") : t("email")}
              name="email"
              type="email"
              defaultValue={
                fields.email?.read === "allow"
                  ? editingMembership?.email
                  : undefined
              }
              disabled={
                !(editing ? fields.email?.update : fields.email?.create)
              }
            />
            {!editing && (
              <Field
                label={t("initialPassword")}
                name="password"
                type="password"
                minLength={12}
                required
              />
            )}
          </form>
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => setOpen(false)}>
              {t("cancel")}
            </Button>
            <Button type="submit" form="principal-form" disabled={busy}>
              {busy ? t("saving") : editing ? t("save") : t("create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AuthorizationSourcesSheet
        principal={authorizationPrincipal}
        snapshot={authorization}
        departments={departments}
        locale={locale}
        busy={authorizationBusy}
        error={authorizationError}
        onClose={() => setAuthorizationPrincipal(null)}
      />

      <PrincipalLifecycleDialog
        key={lifecycle?.id ?? "none"}
        principal={lifecycle}
        iamApi={iamApi}
        onClose={() => {
          setLifecycle(null)
          void refresh()
        }}
      />
    </>
  )
}

function AuthorizationSourcesSheet({
  principal,
  snapshot,
  departments,
  locale,
  busy,
  error,
  onClose,
}: {
  principal: Principal | null
  snapshot: MemberAuthorizationSnapshot | null
  departments: Department[]
  locale: string
  busy: boolean
  error: string
  onClose: () => void
}) {
  const t = useTranslations("iam")
  const sources = snapshot?.sources ?? []
  const sections = [
    {
      key: "roles",
      title: t("roleAuthorizationSources"),
      icon: KeyRound,
      sources: sources.filter((source) =>
        [
          "permanent_role",
          "temporary_role",
          "static_group",
          "dynamic_group",
        ].includes(source.type)
      ),
    },
    {
      key: "entities",
      title: t("entityAuthorizationSources"),
      icon: Building2,
      sources: sources.filter((source) => source.type === "entity_binding"),
    },
    {
      key: "relationships",
      title: t("relationshipAuthorizationSources"),
      icon: GitBranch,
      sources: sources.filter((source) => source.type === "relationship"),
    },
  ].filter((section) => section.sources.length > 0)
  const departmentByID = new Map(
    departments.map((department) => [department.id, department.name])
  )

  const sourceTypeLabel = (source: MemberAuthorizationSource) =>
    t(
      {
        permanent_role: "directRoleSource",
        temporary_role: "temporaryRoleSource",
        static_group: "staticGroupSource",
        dynamic_group: "dynamicGroupSource",

        entity_binding: "entityRoleSource",
        relationship: "resourceRelationshipSource",
      }[source.type]
    )
  const dataScopeLabel = (source: MemberAuthorizationSource) =>
    source.data_scope
      ? t(
          {
            all: "allData",
            self: "ownData",
            department: "ownDepartment",
            department_and_descendants: "departmentDescendants",
            selected_departments: "selectedDepartments",
          }[source.data_scope]
        )
      : ""

  return (
    <Sheet
      open={Boolean(principal)}
      onOpenChange={(open) => !open && onClose()}
    >
      <SheetContent
        side="right"
        className="w-full overflow-hidden sm:max-w-3xl"
        data-authorization-sources-sheet
      >
        <SheetHeader className="border-b pr-12 pb-5">
          <SheetTitle>{t("authorizationSources")}</SheetTitle>
          <SheetDescription className="break-words">
            {principal
              ? t("authorizationSourcesFor", {
                  name:
                    principal.display_name ||
                    principal.login_name ||
                    principal.id,
                })
              : ""}
          </SheetDescription>
        </SheetHeader>
        <div className="flex-1 overflow-y-auto px-6 py-5">
          {busy && (
            <div className="grid min-h-48 place-items-center text-sm text-muted-foreground">
              <span className="flex items-center gap-2">
                <Loader2 className="size-4 animate-spin" />
                {t("loadingAuthorizationSources")}
              </span>
            </div>
          )}
          {!busy && error && <Alert>{error}</Alert>}
          {!busy && snapshot && (
            <>
              <div className="mb-5 flex flex-wrap gap-x-5 gap-y-1 border-b pb-4 text-xs text-muted-foreground">
                <span>
                  {t("authorizationSourceCount", { count: sources.length })}
                </span>
                <span>
                  {t("policyVersion")} {snapshot.revision}
                </span>
                <span>
                  {t("evaluatedAt")}{" "}
                  {new Date(snapshot.evaluated_at).toLocaleString(locale)}
                </span>
              </div>
              <div className="mb-5 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-sm text-foreground">
                {t("authorizationSourceRemovalCaution")}
              </div>
              {sections.map((section) => {
                const Icon = section.icon
                return (
                  <section
                    key={section.key}
                    className="mb-6"
                    aria-labelledby={`authorization-source-${section.key}`}
                  >
                    <h3
                      id={`authorization-source-${section.key}`}
                      className="mb-2 flex items-center gap-2 text-sm font-semibold"
                    >
                      <Icon className="size-4 text-primary" />
                      {section.title}
                      <Badge variant="outline">{section.sources.length}</Badge>
                    </h3>
                    <div className="divide-y rounded-md border">
                      {section.sources.map((source, index) => (
                        <div
                          key={`${source.type}:${source.role_id ?? "none"}:${source.source_id}:${source.scope_id}:${source.effect}:${index}`}
                          className="grid gap-3 p-4"
                          data-authorization-source={source.type}
                          data-authorization-role={source.role_id ?? ""}
                          data-authorization-source-id={source.source_id}
                          data-authorization-scope-id={source.scope_id}
                          data-authorization-effect={source.effect}
                        >
                          <div className="flex flex-wrap items-start justify-between gap-2">
                            <div className="min-w-0">
                              <strong className="block text-sm break-words">
                                {source.role_name ||
                                  source.scope_name ||
                                  `${source.scope_type}:${source.scope_id}`}
                              </strong>
                              <span className="mt-1 block text-xs break-words text-muted-foreground">
                                {sourceTypeLabel(source)}
                                {source.source_name
                                  ? ` · ${source.source_name}`
                                  : ""}
                              </span>
                            </div>
                            <div className="flex shrink-0 flex-wrap gap-1.5">
                              {source.inherited && (
                                <Badge variant="outline">
                                  {t("inheritedSource")}
                                </Badge>
                              )}
                              <Badge
                                variant={
                                  source.effect === "deny"
                                    ? "destructive"
                                    : "secondary"
                                }
                              >
                                {source.effect === "deny"
                                  ? t("deny")
                                  : t("allow")}
                              </Badge>
                            </div>
                          </div>
                          <dl className="grid gap-2 text-xs sm:grid-cols-2">
                            <div>
                              <dt className="text-muted-foreground">
                                {t("authorizationScope")}
                              </dt>
                              <dd className="mt-0.5 break-all">
                                {source.scope_name || source.scope_type} ·{" "}
                                {source.scope_id}
                              </dd>
                            </div>
                            {source.expires_at && (
                              <div>
                                <dt className="text-muted-foreground">
                                  {t("expiresAt")}
                                </dt>
                                <dd className="mt-0.5">
                                  {new Date(source.expires_at).toLocaleString(
                                    locale
                                  )}
                                </dd>
                              </div>
                            )}
                            {source.data_scope && (
                              <div className="sm:col-span-2">
                                <dt className="text-muted-foreground">
                                  {t("dataScope")}
                                </dt>
                                <dd className="mt-0.5 break-words">
                                  {dataScopeLabel(source)}
                                  {source.department_ids.length > 0
                                    ? ` · ${source.department_ids
                                        .map(
                                          (id) => departmentByID.get(id) ?? id
                                        )
                                        .join(", ")}`
                                    : ""}
                                </dd>
                              </div>
                            )}
                            {source.relationship_path.length > 0 && (
                              <div className="sm:col-span-2">
                                <dt className="text-muted-foreground">
                                  {t("relationshipPath")}
                                </dt>
                                <dd className="mt-0.5 break-all">
                                  {source.relationship_path
                                    .map(
                                      (step) =>
                                        `${step.subject_type}:${step.subject_id}${step.subject_relation ? `#${step.subject_relation}` : ""} -> ${step.relation} -> ${step.resource_type}:${step.resource_id}`
                                    )
                                    .join(" / ")}
                                </dd>
                              </div>
                            )}
                          </dl>
                          <div className="flex flex-wrap gap-1.5">
                            {source.permission_codes.map((permission) => (
                              <Badge
                                key={permission}
                                variant="outline"
                                className="max-w-full font-mono text-[11px] whitespace-normal"
                              >
                                {permission}
                              </Badge>
                            ))}
                          </div>
                        </div>
                      ))}
                    </div>
                  </section>
                )
              })}
              {sources.length === 0 && (
                <div className="grid min-h-48 place-items-center text-center text-sm text-muted-foreground">
                  <div>
                    <ShieldCheck className="mx-auto mb-2 size-7" />
                    {t("noEffectiveAuthorizationSources")}
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function Field({
  label,
  name,
  type = "text",
  required = false,
  minLength,
  defaultValue,
  disabled = false,
}: {
  label: string
  name: string
  type?: string
  required?: boolean
  minLength?: number
  defaultValue?: string
  disabled?: boolean
}) {
  return (
    <div className="grid gap-2">
      <Label htmlFor={name}>{label}</Label>
      <Input
        id={name}
        name={name}
        type={type}
        required={required}
        minLength={minLength}
        defaultValue={defaultValue}
        disabled={disabled}
        maxLength={type === "password" ? 1024 : 320}
        autoComplete={type === "password" ? "new-password" : "off"}
      />
    </div>
  )
}
