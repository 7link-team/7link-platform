import { useEffect, useState } from "react"
import { ShieldCheck } from "lucide-react"
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
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "./alert"
import { iamApi, type Role, type RoleDirectoryBinding } from "../lib/iam-api"

interface DirectoryRoleBindingsDialogProps {
  open: boolean
  itemName: string
  assigneeType: RoleDirectoryBinding["assignee_type"]
  assigneeID: string
  onClose: () => void
}

export function DirectoryRoleBindingsDialog({
  open,
  itemName,
  assigneeType,
  assigneeID,
  onClose,
}: DirectoryRoleBindingsDialogProps) {
  const t = useTranslations("iam")
  const [roles, setRoles] = useState<Role[]>([])
  const [boundRoleIDs, setBoundRoleIDs] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [busyRoleID, setBusyRoleID] = useState("")
  const [error, setError] = useState("")

  useEffect(() => {
    let current = true
    void iamApi
      .roles()
      .then(async (nextRoles) => {
        const bindingLists = await Promise.all(
          nextRoles.map((role) => iamApi.roleDirectoryBindings(role.id))
        )
        if (!current) return
        setRoles(nextRoles)
        setBoundRoleIDs(
          nextRoles
            .filter((_, index) =>
              bindingLists[index].some(
                (binding) =>
                  binding.assignee_type === assigneeType &&
                  binding.assignee_id === assigneeID
              )
            )
            .map((role) => role.id)
        )
      })
      .catch((cause: Error) => {
        if (current) setError(cause.message)
      })
      .finally(() => {
        if (current) setLoading(false)
      })
    return () => {
      current = false
    }
  }, [assigneeID, assigneeType])

  const toggleRole = async (role: Role, checked: boolean) => {
    setBusyRoleID(role.id)
    setError("")
    try {
      if (checked)
        await iamApi.addRoleDirectoryBinding(role.id, assigneeType, assigneeID)
      else
        await iamApi.removeRoleDirectoryBinding(
          role.id,
          assigneeType,
          assigneeID
        )
      const bindings = await iamApi.roleDirectoryBindings(role.id)
      const bound = bindings.some(
        (binding) =>
          binding.assignee_type === assigneeType &&
          binding.assignee_id === assigneeID
      )
      setBoundRoleIDs((current) =>
        bound
          ? [...new Set([...current, role.id])]
          : current.filter((id) => id !== role.id)
      )
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusyRoleID("")
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent
        className="max-h-[calc(100dvh-2rem)] max-w-2xl overflow-y-auto"
        data-directory-role-dialog={assigneeType}
      >
        <DialogHeader>
          <DialogTitle className="flex items-start gap-2 pr-8">
            <ShieldCheck className="mt-0.5 size-5 shrink-0" />
            <span className="min-w-0 break-words">
              {itemName} · {t("roleBindings")}
            </span>
          </DialogTitle>
          <DialogDescription>
            {t("directoryRolesDescription")}
          </DialogDescription>
        </DialogHeader>
        {error && <Alert>{error}</Alert>}
        <div className="grid max-h-[min(32rem,60dvh)] gap-2 overflow-y-auto pr-1 sm:grid-cols-2">
          {loading ? (
            <p className="text-sm text-muted-foreground">{t("loadingShort")}</p>
          ) : roles.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("noRoles")}</p>
          ) : (
            roles.map((role) => {
              const checked = boundRoleIDs.includes(role.id)
              return (
                <label
                  key={role.id}
                  className="flex min-h-14 items-center gap-3 rounded-md border p-3 hover:bg-muted/30"
                >
                  <Checkbox
                    data-directory-role-id={role.id}
                    aria-label={t("manageRoleBindings", { name: role.name })}
                    checked={checked}
                    disabled={Boolean(busyRoleID)}
                    onCheckedChange={(value) =>
                      void toggleRole(role, value === true)
                    }
                  />
                  <span className="min-w-0 flex-1">
                    <strong className="block text-sm break-words">
                      {role.name}
                    </strong>
                    <small className="mt-1 block text-xs break-words text-muted-foreground">
                      {role.description || role.id}
                    </small>
                  </span>
                </label>
              )
            })
          )}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            disabled={Boolean(busyRoleID)}
            onClick={onClose}
            data-close-directory-roles
          >
            {t("close")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
