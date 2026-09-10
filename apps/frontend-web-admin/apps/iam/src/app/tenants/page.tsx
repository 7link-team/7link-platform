import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { ArrowRight, Landmark, Pencil, Plus, Trash2 } from "lucide-react"
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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
import { useNavigate } from "react-router"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import {
  iamApi,
  setTenant,
  type Principal,
  type Tenant,
  type TenantUpdate,
} from "../../lib/iam-api"

const statusLabels: Record<Tenant["status"], string> = {
  active: "active",
  suspended: "disable",
  deleted: "deleted",
}

export default function TenantsPage() {
  const locale = useLocale()

  const t = useTranslations("iam")

  const [tenants, setTenants] = useState<Tenant[]>([])
  const [includeDeleted, setIncludeDeleted] = useState(false)
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Tenant | null>(null)
  const [deleting, setDeleting] = useState<Tenant | null>(null)
  const [status, setStatus] = useState<TenantUpdate["status"]>("active")
  const [principals, setPrincipals] = useState<Principal[]>([])
  const [ownerID, setOwnerID] = useState("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [loadError, setLoadError] = useState("")
  const navigate = useNavigate()

  const refresh = useCallback(
    () =>
      iamApi
        .tenants(includeDeleted)
        .then((next) => {
          setTenants(next)
          setLoadError("")
        })
        .catch((cause: Error) => {
          setTenants([])
          setLoadError(cause.message)
        })
        .finally(() => setLoading(false)),
    [includeDeleted]
  )

  useEffect(() => {
    void refresh()
  }, [refresh])

  const openCreate = () => {
    setEditing(null)
    setStatus("active")
    setOwnerID("")
    setOpen(true)
    iamApi
      .principals()
      .then((list) =>
        setPrincipals(list.items.filter((item) => item.status === "active"))
      )
      .catch((cause: Error) => setError(cause.message))
  }

  const openEdit = (tenant: Tenant) => {
    setEditing(tenant)
    setStatus(tenant.status === "suspended" ? "suspended" : "active")
    setOpen(true)
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    try {
      if (editing) {
        await iamApi.updateTenant(editing.id, {
          name: String(data.get("name")),
          status,
          version: editing.version,
        })
      } else {
        await iamApi.createTenant({
          slug: String(data.get("slug")),
          name: String(data.get("name")),
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

  const enterTenant = (tenant: Tenant) => {
    setTenant(tenant.id)
    navigate("/")
  }

  return (
    <>
      <PageHeader
        title={t("tenantManagement")}
        description={t("tenantLifecycle")}
        action={
          <Button onClick={openCreate}>
            <Plus />
            {t("createTenant")}
          </Button>
        }
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
      <div className="mb-3 flex min-h-9 items-center justify-end">
        <label
          htmlFor="include-deleted-tenants"
          className="flex min-h-9 cursor-pointer items-center gap-2 text-sm"
        >
          <Checkbox
            id="include-deleted-tenants"
            checked={includeDeleted}
            onCheckedChange={(checked) => {
              setError("")
              setLoading(true)
              setIncludeDeleted(checked === true)
            }}
          />
          {t("showDeletedTenants")}
        </label>
      </div>
      <div className="overflow-x-auto rounded-md border bg-card">
        <Table className="min-w-[900px]">
          <TableHeader>
            <TableRow>
              <TableHead>{t("tenant")}</TableHead>
              <TableHead>{t("identifier")}</TableHead>
              <TableHead>ID</TableHead>
              <TableHead>{t("status")}</TableHead>
              <TableHead>{t("version")}</TableHead>
              <TableHead>{t("updatedAt")}</TableHead>
              <TableHead className="w-36 text-right">{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(loading || loadError ? [] : tenants).map((tenant) => (
              <TableRow key={tenant.id}>
                <TableCell>
                  <div className="flex min-w-0 items-center gap-2">
                    <Landmark className="size-4 shrink-0 text-primary" />
                    <strong className="truncate text-sm">{tenant.name}</strong>
                  </div>
                </TableCell>
                <TableCell className="font-mono text-xs">
                  {tenant.slug}
                </TableCell>
                <TableCell className="max-w-64 truncate font-mono text-xs text-muted-foreground">
                  {tenant.id}
                </TableCell>
                <TableCell>
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
                </TableCell>
                <TableCell className="font-mono text-xs">
                  v{tenant.version}
                </TableCell>
                <TableCell className="text-sm text-muted-foreground">
                  {new Date(tenant.updated_at).toLocaleString(locale, {
                    hour12: false,
                  })}
                </TableCell>
                <TableCell className="text-right">
                  {tenant.status !== "deleted" && (
                    <div className="flex justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="icon"
                        disabled={busy || tenant.status !== "active"}
                        onClick={() => enterTenant(tenant)}
                        aria-label={t("enterTenantNamed", {
                          name: tenant.name,
                        })}
                        title={t("enterTenant")}
                      >
                        <ArrowRight />
                      </Button>
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
                    </div>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {!loading && !loadError && tenants.length === 0 && (
          <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
            <span className="text-center">
              <Landmark className="mx-auto mb-2 size-7" />
              {t("noTenants")}
            </span>
          </div>
        )}
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
          <form
            key={editing?.id ?? "create"}
            id="tenant-form"
            className="grid gap-4"
            onSubmit={submit}
          >
            <div className="grid gap-2">
              <Label htmlFor="tenant-name">{t("name")}</Label>
              <Input
                id="tenant-name"
                name="name"
                defaultValue={editing?.name}
                maxLength={128}
                required
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="tenant-slug">{t("identifier")}</Label>
              <Input
                id="tenant-slug"
                name="slug"
                defaultValue={editing?.slug}
                minLength={3}
                maxLength={63}
                pattern="[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?"
                disabled={Boolean(editing)}
                required={!editing}
              />
            </div>
            {!editing && (
              <div className="grid gap-2">
                <Label htmlFor="tenant-owner">{t("administrator")}</Label>
                <SimpleSelect
                  id="tenant-owner"
                  options={principals.map((principal) => ({
                    value: principal.id,
                    label: `${principal.display_name}（${principal.login_name}）`,
                  }))}
                  value={ownerID}
                  onValueChange={setOwnerID}
                  placeholder={t("selectTenantAdmin")}
                />
                <p className="text-xs text-muted-foreground">
                  {t("tenantAdminDescription")}
                </p>
              </div>
            )}
            {editing && (
              <div className="grid gap-2">
                <Label htmlFor="tenant-status">{t("status")}</Label>
                <SimpleSelect
                  id="tenant-status"
                  options={{ active: t("active"), suspended: t("disable") }}
                  value={status}
                  onValueChange={(value) =>
                    setStatus(value as TenantUpdate["status"])
                  }
                />
              </div>
            )}
          </form>
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
    </>
  )
}
