import { useCallback, useEffect, useRef, useState, type FormEvent } from "react"
import {
  Plus,
  Search,
  Pencil,
  Loader2,
  UserCog,
  ExternalLink,
  ChevronLeft,
  ChevronRight,
} from "lucide-react"
import { Link } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@workspace/ui/components/dialog"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
import { PageHeader } from "../../components/page-header"
import { Alert } from "../../components/alert"
import { AccountViewDialog } from "../../components/account-view-dialog"
import { iamApi, type Principal } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { getAccountView } from "../../lib/account-view"
import { SimpleSelect } from "@workspace/ui/components/select"
import { SystemMembershipDialog } from "../../components/system-membership-dialog"
import { systemRoleLabel } from "../../lib/membership-presentation"
import type {
  SystemAccountOwner,
  SystemMember,
  Department,
  Role,
} from "../../lib/iam-api"

type AccountCategory = "system" | "platform" | "tenant" | "user"
export function SystemMemberAccountsPage() {
  return <SystemAccountsPage category="system" />
}
export function PlatformAccountsPage() {
  return <SystemAccountsPage category="platform" />
}
export function TenantAccountsPage() {
  return <SystemAccountsPage category="tenant" />
}

export default function SystemAccountsPage({
  category = "user",
}: {
  category?: AccountCategory
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [items, setItems] = useState<Principal[]>([])
  const [search, setSearch] = useState("")
  const [offset, setOffset] = useState(0)
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Principal | null>(null)
  const [busy, setBusy] = useState(false)
  const [owners, setOwners] = useState<SystemAccountOwner[]>([])
  const [owner, setOwner] = useState("")
  const [status, setStatus] = useState<"active" | "disabled" | "">("")
  const [accessOpen, setAccessOpen] = useState(false)
  const [accessAccount, setAccessAccount] = useState<Principal | null>(null)
  const [members, setMembers] = useState<SystemMember[]>([])
  const [departments, setDepartments] = useState<Department[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const ownerBase = owner
    ? category === "platform"
      ? `/system/accounts/platform/${encodeURIComponent(owner)}`
      : `/system/accounts/tenant/${encodeURIComponent(owner)}`
    : ""
  useEffect(() => {
    let active = true
    const pending =
      category === "platform" || category === "tenant"
        ? iamApi.systemAccountOwners(category)
        : Promise.resolve([])
    void pending.then(
      (next) => {
        if (active) setOwners(next)
      },
      (cause: Error) => {
        if (active) setError(cause.message)
      }
    )
    return () => {
      active = false
    }
  }, [category, allowed])
  const refreshAccess = useCallback(async () => {
    if (category !== "system" || !allowed("system_member_view")) return
    try {
      const [nextMembers, nextDepartments, nextRoles] = await Promise.all([
        iamApi.systemMembers(),
        allowed("system_department_view")
          ? iamApi.systemDepartments()
          : Promise.resolve([]),
        allowed("system_role_view")
          ? iamApi.systemRoles()
          : Promise.resolve([]),
      ])
      setMembers(nextMembers)
      setDepartments(nextDepartments)
      setRoles(nextRoles)
    } catch (cause) {
      setError((cause as Error).message)
    }
  }, [category, allowed])
  useEffect(() => {
    let active = true
    void Promise.resolve().then(() => {
      if (active) void refreshAccess()
    })
    return () => {
      active = false
    }
  }, [refreshAccess])
  const generation = useRef(0)
  const invalidate = useCallback(() => {
    generation.current++
  }, [])
  const refresh = useCallback(async () => {
    const request = ++generation.current
    setLoading(true)
    setError("")
    try {
      const next = await iamApi.systemPrincipals(search, offset, {
        category,
        owner_id: owner || undefined,
        status: status || undefined,
      })
      if (request !== generation.current) return
      setItems(next.items)
      setTotal(next.total)
    } catch (cause) {
      if (request !== generation.current) return
      setError((cause as Error).message)
      setItems([])
    } finally {
      if (request === generation.current) setLoading(false)
    }
  }, [search, offset, category, owner, status])
  useEffect(() => {
    let active = true
    void Promise.resolve().then(() => {
      if (active) void refresh()
    })
    return () => {
      active = false
      invalidate()
    }
  }, [refresh, invalidate])
  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setBusy(true)
    setError("")
    try {
      const profile = {
        display_name: String(form.get("display_name")),
        email: String(form.get("email")),
      }
      if (editing) await iamApi.updateSystemPrincipal(editing.id, profile)
      else
        await iamApi.createSystemPrincipal({
          ...profile,
          login_name: String(form.get("login_name")),
          password: String(form.get("password")),
        })
      setOpen(false)
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
        title={t(
          category === "system"
            ? "systemMemberAccounts"
            : category === "platform"
              ? "platformAccounts"
              : category === "tenant"
                ? "tenantAccounts"
                : "userAccounts"
        )}
        action={
          category === "user" && allowed("system_account_create") ? (
            <Button
              onClick={() => {
                setEditing(null)
                setOpen(true)
              }}
            >
              <Plus className="size-4" />
              {t("createAccount")}
            </Button>
          ) : category === "system" && allowed("system_member_create") ? (
            <Button
              onClick={() => {
                setAccessAccount(null)
                setAccessOpen(true)
              }}
            >
              <Plus />
              {t("addSystemAccount")}
            </Button>
          ) : ownerBase ? (
            <Button asChild variant="outline">
              <Link to={ownerBase}>
                <ExternalLink />
                {t("manageMemberships")}
              </Link>
            </Button>
          ) : undefined
        }
      />
      {error && <Alert>{error}</Alert>}
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <Search className="size-4 text-muted-foreground" />
        <Input
          aria-label={t("searchPrincipals")}
          value={search}
          onChange={(event) => {
            setSearch(event.target.value)
            setOffset(0)
          }}
          className="max-w-sm"
        />
        {(category === "platform" || category === "tenant") && (
          <SimpleSelect
            id="account-owner"
            value={owner || "__all__"}
            onValueChange={(value) => {
              setOwner(value === "__all__" ? "" : value)
              setOffset(0)
            }}
            options={[
              {
                value: "__all__",
                label: t(
                  category === "platform" ? "allPlatforms" : "allTenants"
                ),
              },
              ...owners.map((item) => ({ value: item.id, label: item.name })),
            ]}
          />
        )}
        <SimpleSelect
          id="account-status"
          value={status || "__all__"}
          onValueChange={(value) => {
            setStatus(value === "__all__" ? "" : (value as typeof status))
            setOffset(0)
          }}
          options={{
            __all__: t("allStatuses"),
            active: t("active"),
            disabled: t("disable"),
          }}
        />
      </div>
      {loading ? (
        <Loader2 className="size-5 animate-spin" />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("loginName")}</TableHead>
              <TableHead>{t("displayName")}</TableHead>
              <TableHead>{t("email")}</TableHead>
              <TableHead>{t("status")}</TableHead>
              {category === "system" && (
                <>
                  <TableHead>{t("department")}</TableHead>
                  <TableHead>{t("assignedRoles")}</TableHead>
                </>
              )}
              <TableHead>{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <TableRow key={item.id} data-account-id={item.id}>
                <TableCell>{item.login_name ?? item.id}</TableCell>
                <TableCell>{item.display_name}</TableCell>
                <TableCell>{item.email}</TableCell>
                <TableCell>
                  {t.has(item.status) ? t(item.status) : item.status}
                </TableCell>
                {category === "system" && (
                  <>
                    <TableCell>
                      {departments.find(
                        (department) =>
                          department.id ===
                          members.find((member) => member.subject === item.id)
                            ?.department_id
                      )?.name ?? "-"}
                    </TableCell>
                    <TableCell className="max-w-sm break-words">
                      {members.find((member) => member.subject === item.id)
                        ?.protected
                        ? t("fullSystemAdministrator")
                        : (
                            members.find((member) => member.subject === item.id)
                              ?.role_ids ?? []
                          )
                            .map((id) => {
                              const role = roles.find((role) => role.id === id)
                              return role
                                ? systemRoleLabel(
                                    role,
                                    t("fullSystemAdministrator"),
                                    t("importedSystemPermissions")
                                  )
                                : id
                            })
                            .join(", ") || "-"}
                    </TableCell>
                  </>
                )}
                <TableCell>
                  <div className="flex items-center gap-1">
                    {category === "user" &&
                      allowed("system_account_update") && (
                        <Button
                          size="icon"
                          variant="ghost"
                          title={t("edit")}
                          aria-label={t("edit")}
                          onClick={() => {
                            setEditing(item)
                            setOpen(true)
                          }}
                        >
                          <Pencil className="size-4" />
                        </Button>
                      )}
                    {(category === "system" || category === "user") &&
                      allowed("system_member_view") && (
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={t("systemAccessNamed", {
                            name:
                              item.display_name || item.login_name || item.id,
                          })}
                          title={t("systemAccountAccess")}
                          onClick={() => {
                            setAccessAccount(item)
                            setAccessOpen(true)
                          }}
                        >
                          <UserCog />
                        </Button>
                      )}
                    {(category === "platform" || category === "tenant") &&
                      ownerBase && (
                        <Button
                          asChild
                          variant="ghost"
                          size="icon"
                          title={t("manageMemberships")}
                        >
                          <Link
                            to={ownerBase}
                            aria-label={t("manageMembershipsNamed", {
                              name:
                                item.display_name || item.login_name || item.id,
                            })}
                          >
                            <ExternalLink />
                          </Link>
                        </Button>
                      )}
                    {allowed("system_account_view") && !getAccountView() && (
                      <AccountViewDialog
                        principalID={item.id}
                        label={item.display_name || item.login_name || item.id}
                        preferredScope={category === "user" ? undefined : category}
                        preferredOwner={owner || undefined}
                      />
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <div className="mt-4 flex items-center justify-end gap-2">
        <span className="text-sm text-muted-foreground">
          {Math.min(offset + items.length, total)} / {total}
        </span>
        <Button
          size="icon"
          aria-label={t("previous")}
          variant="outline"
          disabled={offset === 0 || loading}
          onClick={() => setOffset(Math.max(0, offset - 50))}
        >
          <ChevronLeft />
        </Button>
        <Button
          size="icon"
          aria-label={t("next")}
          variant="outline"
          disabled={offset + 50 >= total || loading}
          onClick={() => setOffset(offset + 50)}
        >
          <ChevronRight />
        </Button>
      </div>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t(editing ? "editAccount" : "createAccount")}
            </DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(event) => {
              void save(event)
            }}
            className="space-y-4"
          >
            {!editing && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="system-account-login">{t("loginName")}</Label>
                  <Input id="system-account-login" name="login_name" required />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="system-account-password">
                    {t("password")}
                  </Label>
                  <Input
                    id="system-account-password"
                    type="password"
                    name="password"
                    autoComplete="new-password"
                    required
                  />
                </div>
              </>
            )}
            <div className="space-y-2">
              <Label htmlFor="system-account-name">{t("displayName")}</Label>
              <Input
                id="system-account-name"
                name="display_name"
                defaultValue={editing?.display_name ?? ""}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="system-account-email">{t("email")}</Label>
              <Input
                id="system-account-email"
                name="email"
                type="email"
                defaultValue={editing?.email ?? ""}
              />
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setOpen(false)}
              >
                {t("cancel")}
              </Button>
              <Button type="submit" disabled={busy}>
                {busy && <Loader2 className="size-4 animate-spin" />}
                {t("save")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <SystemMembershipDialog
        key={accessAccount?.id ?? "new"}
        open={accessOpen}
        account={accessAccount}
        onClose={() => setAccessOpen(false)}
        onChanged={() => {
          void refresh()
          void refreshAccess()
        }}
      />
    </>
  )
}
