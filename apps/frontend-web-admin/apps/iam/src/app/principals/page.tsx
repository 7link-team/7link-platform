import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type FormEvent,
} from "react"
import { Pencil, Plus, Search, ShieldAlert, UserRoundCheck } from "lucide-react"
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
import { Alert } from "../../components/alert"
import { PrincipalLifecycleDialog } from "./lifecycle-dialog"
import { PageHeader } from "../../components/page-header"
import {
  createIamApi,
  getTenant,
  type Department,
  type FieldConstraint,
  type Member,
  type Principal,
} from "../../lib/iam-api"

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
  const [fields, setFields] = useState<FieldConstraint["fields"]>({})
  const [items, setItems] = useState<Principal[]>([])
  const [members, setMembers] = useState<Member[]>([])
  const [departments, setDepartments] = useState<Department[]>([])
  const [search, setSearch] = useState("")
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Principal | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [departmentID, setDepartmentID] = useState("")
  const refresh = useCallback(
    () =>
      Promise.all([
        iamApi.principals(search),
        iamApi.members(),
        iamApi.departments(),
        iamApi.effectiveFields("user_profile"),
      ])
        .then(([result, nextMembers, nextDepartments, nextFields]) => {
          setItems(result.items)
          setMembers(nextMembers)
          setDepartments(nextDepartments)
          setFields(nextFields.fields)
        })
        .catch((cause: Error) => {
          setItems([])
          setMembers([])
          setFields({})
          throw cause
        }),
    [search, iamApi]
  )
  useEffect(() => {
    void refresh().catch((cause: Error) => setError(cause.message))
  }, [refresh])

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    try {
      if (editing) {
        const changes: { display_name?: string; email?: string } = {}
        for (const field of ["display_name", "email"] as const) {
          if (!data.has(field) || !fields[field]?.update) continue
          const value = String(data.get(field))
          const original =
            fields[field]?.read === "allow" ? (editing[field] ?? "") : ""
          if (value !== original) changes[field] = value
        }
        if (Object.keys(changes).length)
          await iamApi.updatePrincipal(editing.id, changes)
        const membership = members.find(
          (member) => member.subject === editing.id
        )
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
          {items.length} {t("items")}
        </span>
      </div>
      {error && <Alert>{error}</Alert>}
      <div className="overflow-hidden rounded-md border bg-card">
        <Table className="min-w-[900px] whitespace-nowrap">
          <TableHeader>
            <TableRow>
              <TableHead>{t("principal")}</TableHead>
              <TableHead>{t("loginIdentifier")}</TableHead>
              <TableHead>{t("status")}</TableHead>
              <TableHead>{t("primaryDepartment")}</TableHead>
              <TableHead>{t("updatedAt")}</TableHead>
              <TableHead className="w-44 text-right">{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((principal) => (
              <TableRow key={principal.id}>
                <TableCell>
                  <div className="flex items-center gap-3">
                    <span className="grid size-9 place-items-center rounded-full bg-primary/10 font-semibold text-primary">
                      {(principal.display_name ||
                        principal.login_name ||
                        principal.id)[0]?.toUpperCase()}
                    </span>
                    <div>
                      <strong className="block text-sm">
                        {principal.display_name ||
                          principal.login_name ||
                          principal.id}
                      </strong>
                      <span className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                        {principal.email || principal.id}
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
                  )?.name ?? t("unassigned")}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {new Date(principal.updated_at).toLocaleString(locale)}
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex justify-end gap-1">
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
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {items.length === 0 && (
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
                ? t("editPrincipalDescription")
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
                  ? editing?.display_name
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
              label={t("email")}
              name="email"
              type="email"
              defaultValue={
                fields.email?.read === "allow" ? editing?.email : undefined
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
