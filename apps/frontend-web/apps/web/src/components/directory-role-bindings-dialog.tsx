import { AutoTable } from "@workspace/ui/components/auto-table"
import { useEffect, useState } from "react"
import { ExternalLink, ShieldCheck } from "lucide-react"
import { Link } from "react-router"
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
  const [reload, setReload] = useState(0)

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
  }, [assigneeID, assigneeType, reload])

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
        <div className="flex h-[min(32rem,60dvh)] min-h-0 flex-col">
          {loading ? (
            <p className="text-sm text-muted-foreground">{t("loadingShort")}</p>
          ) : roles.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("noRoles")}</p>
          ) : (
            <AutoTable
              fill
              onRefresh={() => {
                setLoading(true)
                setReload((value) => value + 1)
              }}
              data={roles}
              rowKey="id"
              searchable
              columns={[
                {
                  field: "binding",
                  name: t("roleBindings"),
                  render: (_, role) => (
                    <Checkbox
                      id={`directory-role-${assigneeType}-${role.id}`}
                      data-directory-role-id={role.id}
                      aria-label={t("manageRoleBindings", { name: role.name })}
                      checked={boundRoleIDs.includes(role.id)}
                      disabled={Boolean(busyRoleID)}
                      onCheckedChange={(value) =>
                        void toggleRole(role, value === true)
                      }
                    />
                  ),
                },
                {
                  field: "name",
                  name: t("role"),
                  render: (_, role) => (
                    <label
                      htmlFor={`directory-role-${assigneeType}-${role.id}`}
                    >
                      <strong className="block text-sm break-words">
                        {role.name}
                      </strong>
                      <small className="mt-1 block text-xs break-words text-muted-foreground">
                        {role.description || role.id}
                      </small>
                    </label>
                  ),
                },
                {
                  field: "actions",
                  type: "actions",
                  name: t("actions"),
                  render: (_, role) => (
                    <Button variant="ghost" size="icon" asChild>
                      <Link
                        to={`/roles?role=${encodeURIComponent(role.id)}&view=assignments`}
                        aria-label={t("openNamedRole", { name: role.name })}
                        title={t("openNamedRole", { name: role.name })}
                        data-open-role-id={role.id}
                      >
                        <ExternalLink />
                      </Link>
                    </Button>
                  ),
                },
              ]}
            />
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
