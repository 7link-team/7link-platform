import { useCallback, useEffect, useState, type FormEvent } from "react"
import { ArrowRight, Pencil, Plus, Trash2, Loader2 } from "lucide-react"
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
import { SimpleSelect } from "@workspace/ui/components/select"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
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
  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    try {
      if (editing)
        await iamApi.updateOperatingPlatform(editing.id, {
          name: String(data.get("name")),
          status,
          version: editing.version,
        })
      else
        await iamApi.createOperatingPlatform({
          slug: String(data.get("slug")),
          name: String(data.get("name")),
          owner_principal_id: String(data.get("owner_principal_id")),
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
    <>
      <PageHeader
        title={t("operatingPlatforms")}
        action={
          allowed("operating_platform_create") && (
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
          )
        }
      />
      {error && <Alert>{error}</Alert>}
      {loading ? (
        <Loader2 className="size-5 animate-spin" />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("name")}</TableHead>
              <TableHead>{t("identifier")}</TableHead>
              <TableHead>{t("status")}</TableHead>
              <TableHead>{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <TableRow key={item.id}>
                <TableCell>{item.name}</TableCell>
                <TableCell>{item.slug}</TableCell>
                <TableCell>{item.status}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-1">
                    <Link
                      to={`/operating-platforms/${encodeURIComponent(item.id)}`}
                      title={t("enterOperatingPlatform")}
                      aria-label={`${t("enterOperatingPlatform")} ${item.name}`}
                      className="grid size-9 place-items-center"
                    >
                      <ArrowRight className="size-4" />
                    </Link>
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
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t(editing ? "editOperatingPlatform" : "createOperatingPlatform")}
            </DialogTitle>
          </DialogHeader>
          <form
            className="space-y-4"
            onSubmit={(event) => {
              void save(event)
            }}
          >
            {!editing && (
              <div className="space-y-2">
                <Label htmlFor="operating-slug">{t("identifier")}</Label>
                <Input
                  id="operating-slug"
                  name="slug"
                  required
                  minLength={3}
                  maxLength={63}
                />
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="operating-name">{t("name")}</Label>
              <Input
                id="operating-name"
                name="name"
                defaultValue={editing?.name ?? ""}
                required
                maxLength={128}
              />
            </div>
            {!editing ? (
              <div className="space-y-2">
                <Label htmlFor="operating-owner">{t("ownerPrincipalID")}</Label>
                <Input
                  id="operating-owner"
                  name="owner_principal_id"
                  defaultValue={session?.subject ?? ""}
                  required
                />
              </div>
            ) : (
              <div className="space-y-2">
                <Label htmlFor="operating-status">{t("status")}</Label>
                <SimpleSelect
                  id="operating-status"
                  value={status}
                  onValueChange={(value) => setStatus(value as typeof status)}
                  options={[
                    { value: "active", label: t("active") },
                    { value: "suspended", label: t("disable") },
                  ]}
                />
              </div>
            )}
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
          </form>
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
    </>
  )
}
