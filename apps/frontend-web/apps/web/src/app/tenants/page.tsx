import { PaginationPages } from "@workspace/ui/components/pagination"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useState } from "react"
import { Landmark, Pencil, Plus, Trash2 } from "lucide-react"
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
import { AccountViewDialog } from "../../components/account-view-dialog"
import { Alert } from "../../components/alert"
import { useAuth } from "../../components/auth"
import { PageHeader } from "../../components/page-header"

// One screen's worth. The endpoint allows up to 200, and a page nobody scrolls
// is a page nobody needed fetched.
const tenantPageSize = 50
import {
  iamApi,
  type Principal,
  type Tenant,
  type TenantUpdate,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { principalLabel } from "../../lib/principal-label"
import { selfProfileApi } from "../security/profile-api"

const statusLabels: Record<Tenant["status"], string> = {
  active: "active",
  suspended: "disable",
  deleted: "deleted",
}

export default function TenantsPage() {
  const locale = useLocale()

  const t = useTranslations("iam")
  const allowed = useAllowed()
  const { session } = useAuth()

  const [tenants, setTenants] = useState<Tenant[]>([])
  const [includeDeleted, setIncludeDeleted] = useState(false)
  const [search, setSearch] = useState("")
  const [offset, setOffset] = useState(0)
  const [total, setTotal] = useState(0)
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Tenant | null>(null)
  const [deleting, setDeleting] = useState<Tenant | null>(null)
  const [status, setStatus] = useState<TenantUpdate["status"]>("active")
  const [principals, setPrincipals] = useState<
    Pick<Principal, "id" | "display_name" | "login_name">[]
  >([])
  const [ownerID, setOwnerID] = useState("")
  const [ownerSearch, setOwnerSearch] = useState("")
  const [ownerOffset, setOwnerOffset] = useState(0)
  const [ownerTotal, setOwnerTotal] = useState(0)
  const [ownersLoading, setOwnersLoading] = useState(false)
  const canListOwners = allowed("system_account_view")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [loadError, setLoadError] = useState("")

  // One page, narrowed by the service. This listing is the only screen that
  // shows every tenant in the deployment, so it must not read all of them to
  // show any of them: the endpoint caps a page at 200 deliberately, and walking
  // that cap page by page just moves the whole table into the browser.
  const refresh = useCallback(
    () =>
      iamApi
        .tenantPage({ includeDeleted, search, offset, limit: tenantPageSize })
        .then((next) => {
          setTenants(next.items)
          setTotal(next.total)
          setLoadError("")
        })
        .catch((cause: Error) => {
          setTenants([])
          setTotal(0)
          setLoadError(cause.message)
        })
        .finally(() => setLoading(false)),
    [includeDeleted, search, offset]
  )

  useEffect(() => {
    void refresh()
  }, [refresh])

  const openCreate = () => {
    setEditing(null)
    setStatus("active")
    const owner = session
      ? {
          id: session.subject,
          login_name: session.preferred_username,
        }
      : null
    setPrincipals(owner ? [owner] : [])
    setOwnerID(owner?.id ?? "")
    setOwnerSearch("")
    setOwnerOffset(0)
    setOwnerTotal(0)
    setOwnersLoading(canListOwners)
    setError("")
    setOpen(true)
  }

  useEffect(() => {
    if (!open || editing || !session?.subject) return
    const subject = session.subject
    const controller = new AbortController()
    void selfProfileApi.get(subject, controller.signal).then(
      (profile) => {
        if (controller.signal.aborted) return
        setPrincipals((current) => {
          const existing = current.find((item) => item.id === subject)
          return [
            { ...existing, id: subject, display_name: profile.display_name },
            ...current.filter((item) => item.id !== subject),
          ]
        })
      },
      (cause: Error) => { if (!controller.signal.aborted) setError(cause.message) }
    )
    return () => controller.abort()
  }, [open, editing, session?.subject])

  useEffect(() => {
    if (!open || editing || !canListOwners) return
    let active = true
    void iamApi.systemPrincipals(ownerSearch, ownerOffset, { category: "user", status: "active" }).then(
      (page) => {
        if (!active) return
        setPrincipals((current) => [
          ...new Map([
            ...current.filter((item) => item.id === ownerID || item.id === session?.subject),
            ...page.items,
          ].map((item) => [item.id, item])).values(),
        ])
        setOwnerTotal(page.total)
        setOwnersLoading(false)
      },
      (cause: Error) => {
        if (!active) return
        setError(cause.message)
        setOwnersLoading(false)
      }
    )
    return () => { active = false }
  }, [open, editing, canListOwners, ownerSearch, ownerOffset, ownerID, session?.subject])

  const openEdit = (tenant: Tenant) => {
    setEditing(tenant)
    setStatus(tenant.status === "suspended" ? "suspended" : "active")
    setOpen(true)
  }

  const submit = async (data: Record<string, unknown>) => {
    setBusy(true)
    setError("")
    try {
      if (editing) {
        await iamApi.updateTenant(editing.id, {
          name: String(data.name),
          status,
          version: editing.version,
        })
      } else {
        await iamApi.createTenant({
          slug: String(data.slug),
          name: String(data.name),
          owner_principal_id: ownerID,
        })
      }
      setOpen(false)
      setEditing(null)
      await refresh()
      window.dispatchEvent(new Event("tenant-catalog-change"))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const remove = async () => {
    if (!deleting) return
    setBusy(true)
    setError("")
    try {
      await iamApi.deleteTenant(deleting.id, deleting.version)
      setDeleting(null)
      await refresh()
      window.dispatchEvent(new Event("tenant-catalog-change"))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      <PageHeader
        title={t("tenantManagement")}
        description={t("tenantLifecycle")}
      />
      {error && <Alert>{error}</Alert>}
      {loadError && !loading && (
        <Alert>
          <p>{t.has(loadError) ? t(loadError) : loadError}</p>
          <Button
            variant="outline"
            onClick={() => {
              setLoading(true)
              void refresh()
            }}
          >
            {t("retryNow")}
          </Button>
        </Alert>
      )}

      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-md border bg-card">
        <AutoTable
          fill
          loading={loading}
          onRefresh={refresh}
          rowOffset={offset}
          toolbar={
            <div className="flex min-w-0 flex-1 flex-wrap items-end gap-3">
              {allowed("tenant_create") && (
                <Button onClick={openCreate}>
                  <Plus />
                  {t("createTenant")}
                </Button>
              )}
              <AutoForm
                className="min-w-64 flex-1"
                fields={{
                  search: {
                    name: t("searchTenants"),
                    type: "text",
                    value: search,
                  },
                  include_deleted: {
                    name: t("showDeletedTenants"),
                    type: "checkbox",
                    value: includeDeleted,
                    col: 1,
                    config: { id: "include-deleted-tenants" },
                  },
                }}
                onChange={(values) => {
                  const nextSearch = String(values.search)
                  const nextIncludeDeleted = values.include_deleted === true
                  if (
                    nextSearch !== search ||
                    nextIncludeDeleted !== includeDeleted
                  ) {
                    setError("")
                    setLoading(true)
                    setSearch(nextSearch)
                    setIncludeDeleted(nextIncludeDeleted)
                    setOffset(0)
                  }
                }}
              />
            </div>
          }
          footer={
            <div className="flex w-full flex-wrap items-center justify-between gap-3">
              <span className="text-sm text-muted-foreground">
                {Math.min(offset + tenantPageSize, total)} / {total}
              </span>
              <PaginationPages
                page={Math.floor(offset / tenantPageSize) + 1}
                totalPages={Math.max(1, Math.ceil(total / tenantPageSize))}
                disabled={loading}
                onPageChange={(page) => {
                  setLoading(true)
                  setOffset((page - 1) * tenantPageSize)
                }}
              />
            </div>
          }
          className="[&_table]:min-w-[900px]"
          emptyContent={
            !loading &&
            !loadError &&
            tenants.length === 0 && (
              <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
                <span className="text-center">
                  <Landmark className="mx-auto mb-2 size-7" />
                  {t("noTenants")}
                </span>
              </div>
            )
          }
          pagination={false}
          data={loading || loadError ? [] : tenants}
          rowKey={(tenant) => tenant.id}
          columns={[
            {
              field: "tenant",
              name: t("tenant"),
              type: "custom",
              render: (_value, tenant) => (
                <div>
                  <div className="flex min-w-0 items-center gap-2">
                    <Landmark className="size-4 shrink-0 text-primary" />
                    <strong className="truncate text-sm">{tenant.name}</strong>
                  </div>
                </div>
              ),
            },
            {
              field: "identifier",
              name: t("identifier"),
              type: "custom",
              render: (_value, tenant) => (
                <div className="font-mono text-xs">{tenant.slug}</div>
              ),
            },
            {
              field: "id",
              name: t("id"),
              type: "custom",
              render: (_value, tenant) => (
                <div className="max-w-64 truncate font-mono text-xs text-muted-foreground">
                  {tenant.id}
                </div>
              ),
            },
            {
              field: "status",
              name: t("status"),
              type: "custom",
              render: (_value, tenant) => (
                <div>
                  <Badge
                    variant={
                      tenant.status === "active"
                        ? "secondary"
                        : tenant.status === "suspended"
                          ? "destructive"
                          : "outline"
                    }
                  >
                    {t(statusLabels[tenant.status])}
                  </Badge>
                </div>
              ),
            },
            {
              field: "version",
              name: t("version"),
              type: "custom",
              render: (_value, tenant) => (
                <div className="font-mono text-xs">v{tenant.version}</div>
              ),
            },
            {
              field: "updatedAt",
              name: t("updatedAt"),
              type: "custom",
              render: (_value, tenant) => (
                <div className="text-sm text-muted-foreground">
                  {new Date(tenant.updated_at).toLocaleString(locale, {
                    hour12: false,
                  })}
                </div>
              ),
            },
            {
              field: "actions",
              name: t("actions"),
              type: "actions",
              render: (_value, tenant) => (
                <div className="text-right">
                  {tenant.status !== "deleted" && (
                    <div className="flex justify-end gap-1">
                      {allowed("account_view_create") && (
                        <AccountViewDialog
                          label={tenant.name}
                          organization={{ kind: "tenant", id: tenant.id }}
                          disabled={busy || tenant.status !== "active"}
                        />
                      )}
                      {allowed("tenant_update") && (
                        <Button
                          variant="ghost"
                          size="icon"
                          disabled={busy}
                          onClick={() => openEdit(tenant)}
                          aria-label={t("editNamed", { name: tenant.name })}
                          title={t("editTenant")}
                        >
                          <Pencil />
                        </Button>
                      )}
                      {allowed("tenant_delete") && (
                        <Button
                          variant="ghost"
                          size="icon"
                          className="text-destructive"
                          disabled={busy}
                          onClick={() => setDeleting(tenant)}
                          aria-label={t("deleteNamed", { name: tenant.name })}
                          title={t("deleteTenant")}
                        >
                          <Trash2 />
                        </Button>
                      )}
                    </div>
                  )}
                </div>
              ),
            },
          ]}
        />

        {loading && (
          <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
            {t("loading")}
          </div>
        )}
      </div>

      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next)
          if (!next) setEditing(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editing ? t("editTenant") : t("createTenant")}
            </DialogTitle>
            <DialogDescription>
              {editing ? t("tenantSlugImmutable") : t("tenantSlugDescription")}
            </DialogDescription>
          </DialogHeader>
          <AutoForm
            key={editing?.id ?? "create"}
            mode={editing ? "edit" : "add"}
            id="tenant-form"
            fields={{
              name: {
                name: t("name"),
                type: "text",
                value: editing?.name ?? "",
                required: true,
                config: { id: "tenant-name", maxLength: 128 },
              },
              slug: {
                name: t("identifier"),
                type: "text",
                value: editing?.slug ?? "",
                required: !editing,
                disabled: Boolean(editing),
                config: {
                  id: "tenant-slug",
                  minLength: 3,
                  maxLength: 63,
                  pattern: "[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?",
                },
              },
              owner_principal_id: {
                name: t("administrator"),
                type: "select",
                value: ownerID,
                show: !editing,
                config: {
                  id: "tenant-owner",
                  options: principals.map((principal) => ({
                    value: principal.id,
                    label: principalLabel(principal, t("accountUnavailable"), t("accountAnonymized")),
                  })),
                  placeholder: t("selectTenantAdmin"),
                  suffix: t("tenantAdminDescription"),
                },
              },
              status: {
                name: t("status"),
                type: "select",
                value: status,
                show: Boolean(editing),
                config: {
                  id: "tenant-status",
                  options: { active: t("active"), suspended: t("disable") },
                },
              },
            }}
            onSubmit={submit}
            onChange={(values) => {
              setStatus(values.status as TenantUpdate["status"])
              setOwnerID(String(values.owner_principal_id ?? ""))
            }}
          ></AutoForm>
          {!editing && canListOwners && (
            <div className="grid gap-2">
              <AutoForm
                fields={{ search: { name: t("loginNameOrEmail"), type: "text", value: ownerSearch } }}
                onChange={(values) => {
                  const next = String(values.search ?? "")
                  if (next === ownerSearch) return
                  setOwnersLoading(true)
                  setOwnerSearch(next)
                  setOwnerOffset(0)
                }}
              />
              <PaginationPages
                page={Math.floor(ownerOffset / 50) + 1}
                totalPages={Math.max(1, Math.ceil(ownerTotal / 50))}
                disabled={ownersLoading}
                onPageChange={(page) => { setOwnersLoading(true); setOwnerOffset((page - 1) * 50) }}
              />
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {t("cancel")}
            </Button>
            <Button
              type="submit"
              form="tenant-form"
              disabled={busy || (!editing && !ownerID)}
            >
              {busy ? t("saving") : editing ? t("save") : t("create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={Boolean(deleting)}
        onOpenChange={(next) => !next && setDeleting(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("deleteTenant")}</DialogTitle>
            <DialogDescription>
              {t("deleteTenantConfirm", { name: deleting?.name ?? "" })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleting(null)}>
              {t("cancel")}
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={() => void remove()}
            >
              {busy ? t("deleting") : t("confirmDelete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
