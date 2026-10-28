import { PaginationPages } from "@workspace/ui/components/pagination"
import { AutoForm } from "@workspace/ui/components/auto-form"
import {
  AutoTable,
  TableFilterBar,
  type FilterOption,
} from "@workspace/ui/components/auto-table"
import { useCallback, useEffect, useRef, useState } from "react"
import { Plus, Pencil, Loader2, UserCog, ExternalLink } from "lucide-react"
import { Link } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@workspace/ui/components/dialog"
import { PageHeader } from "../../components/page-header"
import { Alert } from "../../components/alert"
import { AccountViewDialog } from "../../components/account-view-dialog"
import { iamApi, type Principal } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { getAccountView } from "../../lib/account-view"
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
  const save = async (data: Record<string, unknown>) => {
    setBusy(true)
    setError("")
    try {
      const profile = {
        display_name: String(data.display_name),
        email: String(data.email),
      }
      if (editing) await iamApi.updateSystemPrincipal(editing.id, profile)
      else
        await iamApi.createSystemPrincipal({
          ...profile,
          login_name: String(data.login_name),
          password: String(data.password),
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
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
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
      />
      {error && <Alert>{error}</Alert>}

      <AutoTable
        loading={loading}
        fill
        onRefresh={refresh}
        rowOffset={offset}
        toolbarActions={
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
        toolbar={
          <div className="flex min-w-0 flex-1 flex-wrap items-end gap-3">
            <TableFilterBar
              key={`${category}:${search}:${owner}:${status}`}
              className="min-w-0 flex-1 border-0 p-0"
              defaultValue={{ search, owner, status }}
              filters={[
                {
                  field: "search",
                  name: t("searchPrincipals"),
                  type: "string",
                  conditions: [],
                },
                ...(category === "platform" || category === "tenant"
                  ? ([
                      {
                        field: "owner",
                        name: t(
                          category === "platform"
                            ? "allPlatforms"
                            : "allTenants"
                        ),
                        type: "select",
                        conditions: [],
                        options: [
                          {
                            value: "",
                            label: t(
                              category === "platform"
                                ? "allPlatforms"
                                : "allTenants"
                            ),
                          },
                          ...owners.map((item) => ({
                            value: item.id,
                            label: item.name,
                          })),
                        ],
                      },
                    ] satisfies FilterOption[])
                  : []),
                {
                  field: "status",
                  name: t("status"),
                  type: "select",
                  conditions: [],
                  options: [
                    { value: "", label: t("allStatuses") },
                    { value: "active", label: t("active") },
                    { value: "disabled", label: t("disable") },
                  ],
                },
              ]}
              onSearch={(values) => {
                setSearch(values.search ?? "")
                setOwner(values.owner ?? "")
                setStatus((values.status ?? "") as typeof status)
                setOffset(0)
              }}
              onReset={() => {
                setSearch("")
                setOwner("")
                setStatus("")
                setOffset(0)
              }}
            />
          </div>
        }
        footer={
          <div className="flex w-full flex-wrap items-center justify-between gap-3">
            <span className="text-sm text-muted-foreground">
              {Math.min(offset + 50, total)} / {total}
            </span>
            <PaginationPages
              page={Math.floor(offset / 50) + 1}
              totalPages={Math.max(1, Math.ceil(total / 50))}
              disabled={loading}
              onPageChange={(page) => {
                setOffset((page - 1) * 50)
              }}
            />
          </div>
        }
        emptyContent={null}
        rowProps={(item) => ({ "data-account-id": item.id })}
        pagination={false}
        data={items}
        rowKey={(item) => item.id}
        columns={[
          {
            field: "loginName",
            name: t("loginName"),
            type: "custom",
            render: (_value, item) => <div>{item.login_name ?? item.id}</div>,
          },
          {
            field: "display_name",
            name: t("displayName"),
            type: "text",
          },
          {
            field: "email",
            name: t("email"),
            type: "text",
          },
          {
            field: "status",
            name: t("status"),
            type: "custom",
            render: (_value, item) => (
              <div>{t.has(item.status) ? t(item.status) : item.status}</div>
            ),
          },
          {
            field: "department",
            name: t("department"),
            type: "custom",
            render: (_value, item) => (
              <div>
                {departments.find(
                  (department) =>
                    department.id ===
                    members.find((member) => member.subject === item.id)
                      ?.department_id
                )?.name ?? "-"}
              </div>
            ),
          },
          {
            field: "assignedRoles",
            name: t("assignedRoles"),
            type: "custom",
            render: (_value, item) => (
              <div className="max-w-sm break-words">
                {members.find((member) => member.subject === item.id)?.protected
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
              </div>
            ),
          },
          {
            field: "actions",
            name: t("actions"),
            type: "actions",
            render: (_value, item) => (
              <div>
                <div className="flex items-center gap-1">
                  {category === "user" && allowed("system_account_update") && (
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
                          name: item.display_name || item.login_name || item.id,
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
                      preferredScope={
                        category === "user" ? undefined : category
                      }
                      preferredOwner={owner || undefined}
                    />
                  )}
                </div>
              </div>
            ),
          },
        ]}
      />

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t(editing ? "editAccount" : "createAccount")}
            </DialogTitle>
          </DialogHeader>
          <AutoForm
            key={editing?.id ?? "create"}
            mode={editing ? "edit" : "add"}
            id="system-accounts-form"
            fields={{
              login_name: {
                name: t("loginName"),
                type: "text",
                value: "",
                required: true,
                show: !editing,
                config: { id: "system-account-login" },
              },
              password: {
                name: t("password"),
                type: "password",
                value: "",
                required: true,
                show: !editing,
                config: {
                  id: "system-account-password",
                  autoComplete: "new-password",
                },
              },
              display_name: {
                name: t("displayName"),
                type: "text",
                value: editing?.display_name ?? "",
                config: { id: "system-account-name" },
              },
              email: {
                name: t("email"),
                type: "email",
                value: editing?.email ?? "",
                config: { id: "system-account-email" },
              },
            }}
            onSubmit={save}
          >
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
          </AutoForm>
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
    </div>
  )
}
