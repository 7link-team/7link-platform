import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { useCallback, useEffect, useState } from "react"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@workspace/ui/components/dialog"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { AccountViewDialog } from "../../components/account-view-dialog"
import { useAuth } from "../../components/auth"
import { useAllowed } from "../../lib/permissions"
import { iamApi, type Tenant } from "../../lib/iam-api"

export default function OperatingPlatformsPage() {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const { session } = useAuth()
  const [items, setItems] = useState<Tenant[]>([])
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Tenant | null>(null)
  const [deleting, setDeleting] = useState<Tenant | null>(null)
  const [status, setStatus] = useState<"active" | "suspended">("active")
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const refresh = useCallback(async () => {
    try {
      setItems(await iamApi.operatingPlatforms())
      setError("")
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])
  useEffect(() => {
    let active = true
    void Promise.resolve().then(() => {
      if (active) void refresh()
    })
    return () => {
      active = false
    }
  }, [refresh])
  const save = async (data: Record<string, unknown>) => {
    setBusy(true)
    setError("")
    try {
      if (editing)
        await iamApi.updateOperatingPlatform(editing.id, {
          name: String(data.name),
          status,
          version: editing.version,
        })
      else
        await iamApi.createOperatingPlatform({
          slug: String(data.slug),
          name: String(data.name),
          owner_principal_id: String(data.owner_principal_id),
        })
      setOpen(false)
      await refresh()
      window.dispatchEvent(new Event("operating-platform-catalog-change"))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const remove = async () => {
    if (!deleting) return
    setBusy(true)
    try {
      await iamApi.deleteOperatingPlatform(deleting.id, deleting.version)
      setDeleting(null)
      await refresh()
      window.dispatchEvent(new Event("operating-platform-catalog-change"))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      <PageHeader title={t("operatingPlatforms")} />
      {error && <Alert>{error}</Alert>}
      <AutoTable
        loading={loading}
        fill
        onRefresh={refresh}
        searchable
        toolbar={
          <div className="flex min-w-0 flex-1 flex-wrap items-end gap-3">
            {allowed("operating_platform_create") && (
              <Button
                onClick={() => {
                  setEditing(null)
                  setStatus("active")
                  setOpen(true)
                }}
              >
                <Plus className="size-4" />
                {t("createOperatingPlatform")}
              </Button>
            )}
          </div>
        }
        emptyContent={null}
        data={items}
        rowKey={(item) => item.id}
        columns={[
          {
            field: "name",
            name: t("name"),
            type: "text",
          },
          {
            field: "slug",
            name: t("identifier"),
            type: "text",
          },
          {
            field: "status",
            name: t("status"),
            type: "text",
          },
          {
            field: "actions",
            name: t("actions"),
            type: "actions",
            render: (_value, item) => (
              <div>
                <div className="flex items-center gap-1">
                  {allowed("account_view_create") && (
                    <AccountViewDialog
                      label={item.name}
                      organization={{ kind: "platform", id: item.id }}
                      disabled={item.status !== "active"}
                    />
                  )}
                  {allowed("operating_platform_update") && (
                    <Button
                      size="icon"
                      variant="ghost"
                      title={t("edit")}
                      aria-label={t("edit")}
                      onClick={() => {
                        setEditing(item)
                        setStatus(
                          item.status === "suspended" ? "suspended" : "active"
                        )
                        setOpen(true)
                      }}
                    >
                      <Pencil className="size-4" />
                    </Button>
                  )}
                  {allowed("operating_platform_delete") && (
                    <Button
                      size="icon"
                      variant="ghost"
                      title={t("delete")}
                      aria-label={t("delete")}
                      onClick={() => setDeleting(item)}
                    >
                      <Trash2 className="size-4" />
                    </Button>
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
              {t(editing ? "editOperatingPlatform" : "createOperatingPlatform")}
            </DialogTitle>
          </DialogHeader>
          <AutoForm
            key={editing?.id ?? "create"}
            mode={editing ? "edit" : "add"}
            id="system-operating-platforms-form"
            fields={{
              slug: {
                name: t("identifier"),
                type: "text",
                value: "",
                required: true,
                show: !editing,
                config: { id: "operating-slug", minLength: 3, maxLength: 63 },
              },
              name: {
                name: t("name"),
                type: "text",
                value: editing?.name ?? "",
                required: true,
                config: { id: "operating-name", maxLength: 128 },
              },
              owner_principal_id: {
                name: t("ownerPrincipalID"),
                type: "text",
                value: session?.subject ?? "",
                required: true,
                show: !editing,
                config: { id: "operating-owner" },
              },
              status: {
                name: t("status"),
                type: "select",
                value: status,
                show: Boolean(editing),
                config: {
                  id: "operating-status",
                  options: { active: t("active"), suspended: t("disable") },
                },
              },
            }}
            onSubmit={save}
            onChange={(values) => {
              setStatus(values.status as typeof status)
            }}
          >
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setOpen(false)}
              >
                {t("cancel")}
              </Button>
              <Button disabled={busy} type="submit">
                {t("save")}
              </Button>
            </DialogFooter>
          </AutoForm>
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(deleting)}
        onOpenChange={(value) => {
          if (!value) setDeleting(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t("deleteOperatingPlatform")} · {deleting?.name}
            </DialogTitle>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleting(null)}>
              {t("cancel")}
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={() => {
                void remove()
              }}
            >
              {t("delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
