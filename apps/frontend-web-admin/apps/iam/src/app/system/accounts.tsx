import { useCallback, useEffect, useRef, useState, type FormEvent } from "react"
import { Plus, Search, Pencil, Loader2 } from "lucide-react"
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

export default function SystemAccountsPage() {
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
  const generation = useRef(0)
  const invalidate = useCallback(() => {
    generation.current++
  }, [])
  const refresh = useCallback(async () => {
    const request = ++generation.current
    setLoading(true)
    setError("")
    try {
      const next = await iamApi.systemPrincipals(search, offset)
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
  }, [search, offset])
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
        title={t("systemAccounts")}
        action={
          allowed("system_account_create") && (
            <Button
              onClick={() => {
                setEditing(null)
                setOpen(true)
              }}
            >
              <Plus className="size-4" />
              {t("createAccount")}
            </Button>
          )
        }
      />
      {error && <Alert>{error}</Alert>}
      <div className="mb-4 flex items-center gap-2">
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
              <TableHead>{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <TableRow key={item.id}>
                <TableCell>{item.login_name ?? item.id}</TableCell>
                <TableCell>{item.display_name}</TableCell>
                <TableCell>{item.email}</TableCell>
                <TableCell>{item.status}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-1">
                    {allowed("system_account_update") && (
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
                    {allowed("system_account_view") && !getAccountView() && (
                      <AccountViewDialog
                        principalID={item.id}
                        label={item.display_name || item.login_name || item.id}
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
          variant="outline"
          disabled={offset === 0 || loading}
          onClick={() => setOffset(Math.max(0, offset - 50))}
        >
          {t("previous")}
        </Button>
        <Button
          variant="outline"
          disabled={offset + 50 >= total || loading}
          onClick={() => setOffset(offset + 50)}
        >
          {t("next")}
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
    </>
  )
}
