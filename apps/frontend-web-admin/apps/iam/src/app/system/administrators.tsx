import { useCallback, useEffect, useState, type FormEvent } from "react"
import { Plus, Pencil, Trash2, Loader2 } from "lucide-react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Checkbox } from "@workspace/ui/components/checkbox"
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
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { iamApi, type Permission } from "../../lib/iam-api"

type Administrator = Awaited<
  ReturnType<typeof iamApi.systemAdministrators>
>[number]
export default function SystemAdministratorsPage() {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [items, setItems] = useState<Administrator[]>([])
  const [catalog, setCatalog] = useState<Permission[]>([])
  const [open, setOpen] = useState(false)
  const [principal, setPrincipal] = useState("")
  const [full, setFull] = useState(false)
  const [codes, setCodes] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [deleting, setDeleting] = useState<Administrator | null>(null)
  const refresh = useCallback(async () => {
    try {
      const [next, permissions] = await Promise.all([
        iamApi.systemAdministrators(),
        iamApi.systemPermissionCatalog(),
      ])
      setItems(next)
      setCatalog(permissions)
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
  const edit = (item?: Administrator) => {
    setPrincipal(item?.principal_id ?? "")
    setFull(item?.full_administrator ?? false)
    setCodes(item?.permissions ?? [])
    setOpen(true)
  }
  const save = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    try {
      await iamApi.setSystemAdministrator(principal, full, full ? [] : codes)
      setOpen(false)
      await refresh()
      window.dispatchEvent(new Event("menu-catalog-change"))
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
      await iamApi.revokeSystemAdministrator(deleting.principal_id)
      setDeleting(null)
      await refresh()
      window.dispatchEvent(new Event("menu-catalog-change"))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <PageHeader
        title={t("systemAdministrators")}
        action={
          allowed("platform_administer") && (
            <Button onClick={() => edit()}>
              <Plus className="size-4" />
              {t("addSystemAdministrator")}
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
              <TableHead>{t("principalID")}</TableHead>
              <TableHead>{t("permissions")}</TableHead>
              <TableHead>{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <TableRow key={item.principal_id}>
                <TableCell>{item.principal_id}</TableCell>
                <TableCell className="max-w-lg break-words">
                  {item.full_administrator
                    ? t("fullSystemAdministrator")
                    : item.permissions?.join(", ")}
                </TableCell>
                <TableCell>
                  {allowed("platform_administer") && (
                    <div className="flex items-center gap-1">
                      <Button
                        size="icon"
                        variant="ghost"
                        title={t("edit")}
                        aria-label={t("edit")}
                        onClick={() => edit(item)}
                      >
                        <Pencil className="size-4" />
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        title={t("delete")}
                        aria-label={t("delete")}
                        onClick={() => setDeleting(item)}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </div>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("systemAdministrators")}</DialogTitle>
          </DialogHeader>
          <form
            className="space-y-4"
            onSubmit={(event) => {
              void save(event)
            }}
          >
            <div className="space-y-2">
              <Label htmlFor="system-administrator-id">
                {t("principalID")}
              </Label>
              <Input
                id="system-administrator-id"
                value={principal}
                onChange={(event) => setPrincipal(event.target.value)}
                required
              />
            </div>
            <label className="flex items-center gap-2">
              <Checkbox
                checked={full}
                onCheckedChange={(value) => setFull(value === true)}
              />
              {t("fullSystemAdministrator")}
            </label>
            {!full && (
              <div className="max-h-72 space-y-2 overflow-y-auto">
                {catalog.map((item) => (
                  <label
                    key={item.code}
                    className="flex items-start gap-2 text-sm"
                  >
                    <Checkbox
                      checked={codes.includes(item.code)}
                      onCheckedChange={(value) =>
                        setCodes((previous) =>
                          value === true
                            ? [...previous, item.code]
                            : previous.filter((code) => code !== item.code)
                        )
                      }
                    />
                    <span className="min-w-0 break-words">
                      {item.code}
                      <span className="block text-muted-foreground">
                        {item.summary}
                      </span>
                    </span>
                  </label>
                ))}
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
              <Button
                type="submit"
                disabled={busy || !principal || (!full && codes.length === 0)}
              >
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
              {t("revokeSystemAdministrator")} · {deleting?.principal_id}
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
