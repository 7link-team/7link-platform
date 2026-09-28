import { useCallback, useEffect, useState, type FormEvent } from "react"
import { useParams } from "react-router"
import { Plus, Loader2, UserRoundX, UserRoundCheck } from "lucide-react"
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
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { iamApi, type Member } from "../../lib/iam-api"

export default function PlatformMembersPage() {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const { platformID = "" } = useParams<{ platformID: string }>()
  const [items, setItems] = useState<Member[]>([])
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const refresh = useCallback(async () => {
    try {
      setItems(await iamApi.members())
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
    const data = new FormData(event.currentTarget)
    try {
      await iamApi.addOperatingMember(platformID, {
        subject: String(data.get("principal_id")),
        display_name: String(data.get("display_name")),
        email: String(data.get("email")),
      })
      setOpen(false)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const toggle = async (item: Member) => {
    setBusy(true)
    try {
      await iamApi.updateMember(item.subject, {
        status: item.status === "active" ? "disabled" : "active",
      })
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
        title={t("platformMembers")}
        action={
          allowed("platform_member_create") && (
            <Button onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              {t("addPlatformMember")}
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
              <TableHead>{t("displayName")}</TableHead>
              <TableHead>{t("email")}</TableHead>
              <TableHead>{t("status")}</TableHead>
              <TableHead>{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <TableRow key={item.subject}>
                <TableCell>{item.display_name || item.subject}</TableCell>
                <TableCell>{item.email}</TableCell>
                <TableCell>{item.status}</TableCell>
                <TableCell>
                  {allowed("platform_member_update") && (
                    <Button
                      size="icon"
                      variant="ghost"
                      title={t(item.status === "active" ? "disable" : "enable")}
                      aria-label={t(
                        item.status === "active" ? "disable" : "enable"
                      )}
                      disabled={busy}
                      onClick={() => {
                        void toggle(item)
                      }}
                    >
                      {item.status === "active" ? (
                        <UserRoundX className="size-4" />
                      ) : (
                        <UserRoundCheck className="size-4" />
                      )}
                    </Button>
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
            <DialogTitle>{t("addPlatformMember")}</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(event) => {
              void save(event)
            }}
            className="space-y-4"
          >
            <div className="space-y-2">
              <Label htmlFor="platform-member-id">{t("principalID")}</Label>
              <Input id="platform-member-id" name="principal_id" required />
            </div>
            <div className="space-y-2">
              <Label htmlFor="platform-member-name">{t("displayName")}</Label>
              <Input id="platform-member-name" name="display_name" />
            </div>
            <div className="space-y-2">
              <Label htmlFor="platform-member-email">{t("email")}</Label>
              <Input id="platform-member-email" type="email" name="email" />
            </div>
            <DialogFooter>
              <Button
                variant="outline"
                type="button"
                onClick={() => setOpen(false)}
              >
                {t("cancel")}
              </Button>
              <Button type="submit" disabled={busy}>
                {t("save")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}
