import { useEffect, useMemo, useState } from "react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import { Checkbox } from "@workspace/ui/components/checkbox"
import { SimpleSelect } from "@workspace/ui/components/select"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@workspace/ui/components/dialog"
import { Alert } from "./alert"
import {
  ApiError,
  iamApi,
  type Principal,
  type Member,
  type Department,
  type Role,
  type SystemOwnerScope,
} from "../lib/iam-api"
import { useAllowed } from "../lib/permissions"
import { systemRoleLabel } from "../lib/membership-presentation"

export function SystemMembershipDialog({
  open,
  account,
  onClose,
  onChanged,
  scope,
}: {
  open: boolean
  account: Principal | null
  onClose: () => void
  onChanged: () => void
  scope?: SystemOwnerScope
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const memberAllowed = allowed(
    scope ? "platform_administer" : "system_member_update"
  )
  const createAllowed = allowed(
    scope ? "platform_administer" : "system_member_create"
  )
  const roleAllowed = allowed(
    scope ? "platform_administer" : "system_role_manage_member"
  )
  const departmentViewAllowed = allowed(
    scope ? "system_account_view" : "system_department_view"
  )
  const roleViewAllowed = allowed(
    scope ? "system_account_view" : "system_role_view"
  )
  const assignmentViewAllowed = allowed(
    scope ? "system_account_view" : "system_member_view"
  )
  const api = useMemo(
    () => ({
      member: (id: string) =>
        scope ? iamApi.systemOwnerMember(scope, id) : iamApi.systemMember(id),
      departments: () =>
        scope
          ? iamApi.systemOwnerDepartments(scope)
          : iamApi.systemDepartments(),
      roles: () =>
        scope ? iamApi.systemOwnerRoles(scope) : iamApi.systemRoles(),
      assigned: async (id: string) =>
        scope
          ? iamApi.systemOwnerMemberRoles(scope, id)
          : iamApi.systemMemberRoles(id),
    }),
    [scope]
  )
  const [search, setSearch] = useState("")
  const [candidates, setCandidates] = useState<Principal[]>([])
  const [selected, setSelected] = useState<Principal | null>(account)
  const [member, setMember] = useState<
    (Member & { protected?: boolean }) | null
  >(null)
  const [departments, setDepartments] = useState<Department[]>([])
  const [roles, setRoles] = useState<(Role & { builtin?: boolean })[]>([])
  const [assigned, setAssigned] = useState<string[]>([])
  const [department, setDepartment] = useState("")
  const [status, setStatus] = useState<Member["status"]>("active")
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  useEffect(() => {
    if (!open) return
    let active = true
    void Promise.resolve().then(() => {
      if (!active) return
      setSelected(account)
      setError("")
      setSearch("")
    })
    return () => {
      active = false
    }
  }, [open, account])
  useEffect(() => {
    if (!open || account) return
    let active = true
    void iamApi.systemPrincipals(search).then(
      (result) => {
        if (active) setCandidates(result.items)
      },
      (cause: Error) => {
        if (active) setError(cause.message)
      }
    )
    return () => {
      active = false
    }
  }, [open, account, search])
  useEffect(() => {
    if (!open) return
    let active = true
    void Promise.resolve()
      .then(() => {
        if (active) {
          setLoading(true)
          setMember(null)
          setAssigned([])
        }
        return Promise.all([
          selected
            ? api.member(selected.id).catch((cause: unknown) => {
                if (cause instanceof ApiError && cause.status === 404)
                  return null
                throw cause
              })
            : Promise.resolve(null),
          departmentViewAllowed ? api.departments() : Promise.resolve([]),
          roleViewAllowed ? api.roles() : Promise.resolve([]),
          selected && assignmentViewAllowed
            ? api.assigned(selected.id).catch((cause: unknown) => {
                if (cause instanceof ApiError && cause.status === 404) return []
                throw cause
              })
            : Promise.resolve([]),
        ])
      })
      .then(
        ([nextMember, nextDepartments, nextRoles, nextAssigned]) => {
          if (!active) return
          setMember(nextMember)
          setDepartments(nextDepartments)
          setRoles(nextRoles)
          setAssigned(nextAssigned)
          setDepartment(nextMember?.department_id ?? "")
          setStatus(nextMember?.status ?? "active")
        },
        (cause: Error) => {
          if (active) setError(cause.message)
        }
      )
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [
    open,
    selected,
    api,
    departmentViewAllowed,
    roleViewAllowed,
    assignmentViewAllowed,
  ])
  const save = async () => {
    if (!selected) return
    setBusy(true)
    setError("")
    try {
      const next = scope
        ? member
          ? await iamApi.updateSystemOwnerMember(scope, selected.id, {
              department_id: department,
              status,
            })
          : await iamApi.createSystemOwnerMember(scope, {
              subject: selected.id,
              display_name:
                selected.display_name || selected.login_name || selected.id,
              email: selected.email,
              department_id: department,
              status,
            })
        : member
          ? await iamApi.updateSystemMember(selected.id, {
              department_id: department,
              ...(member.protected ? {} : { status }),
            })
          : await iamApi.createSystemMember({
              principal_id: selected.id,
              display_name:
                selected.display_name || selected.login_name || selected.id,
              email: selected.email,
              department_id: department,
              status,
            })
      setMember(next)
      onChanged()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const toggleRole = async (
    role: { id: string; builtin?: boolean },
    checked: boolean
  ) => {
    if (!selected || !member || role.builtin || member.protected) return
    setBusy(true)
    setError("")
    try {
      if (scope) {
        if (checked)
          await iamApi.addSystemOwnerRoleMember(scope, role.id, selected.id)
        else
          await iamApi.removeSystemOwnerRoleMember(scope, role.id, selected.id)
      } else if (checked) await iamApi.addSystemRoleMember(role.id, selected.id)
      else await iamApi.removeSystemRoleMember(role.id, selected.id)
      setAssigned((current) =>
        checked
          ? [...new Set([...current, role.id])]
          : current.filter((id) => id !== role.id)
      )
      window.dispatchEvent(new Event("menu-catalog-change"))
      onChanged()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (!value) onClose()
      }}
    >
      <DialogContent className="max-h-[85dvh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>
            {t(scope ? "manageMemberships" : "systemAccountAccess")}
          </DialogTitle>
          <DialogDescription>
            {selected
              ? `${selected.display_name || selected.login_name} · ${scope?.name ?? t("systemAdministration")}`
              : (scope?.name ?? t("systemAdministration"))}
          </DialogDescription>
        </DialogHeader>
        {error && <Alert>{error}</Alert>}
        {!account && (
          <div className="grid gap-2">
            <Label htmlFor="system-member-search">
              {t("searchPrincipals")}
            </Label>
            <Input
              id="system-member-search"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
            <SimpleSelect
              id="system-member-principal"
              value={selected?.id ?? ""}
              onValueChange={(id) => {
                setLoading(true)
                setSelected(candidates.find((item) => item.id === id) ?? null)
              }}
              options={candidates.map((item) => ({
                value: item.id,
                label: `${item.display_name || item.login_name || item.id} (${item.login_name || item.id})`,
              }))}
              placeholder={t("selectAccount")}
            />
          </div>
        )}
        {loading ? (
          <p role="status">{t("loading")}</p>
        ) : (
          selected && (
            <>
              {member?.protected && (
                <p className="text-sm text-muted-foreground">
                  {t("protectedSuperadminAuthority")}
                </p>
              )}
              <div className="grid gap-2">
                <Label htmlFor="system-member-department">
                  {t("department")}
                </Label>
                <SimpleSelect
                  id="system-member-department"
                  value={department || "__none__"}
                  onValueChange={(id) =>
                    setDepartment(id === "__none__" ? "" : id)
                  }
                  options={[
                    { value: "__none__", label: t("unassignedDepartment") },
                    ...departments.map((item) => ({
                      value: item.id,
                      label: item.name,
                    })),
                  ]}
                  disabled={busy || !(member ? memberAllowed : createAllowed)}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="system-member-status">{t("status")}</Label>
                <SimpleSelect
                  id="system-member-status"
                  value={status}
                  onValueChange={(value) =>
                    setStatus(value as Member["status"])
                  }
                  options={{ active: t("active"), disabled: t("disable") }}
                  disabled={
                    busy ||
                    member?.protected ||
                    !(member ? memberAllowed : createAllowed)
                  }
                />
              </div>
              {member && (
                <section className="grid gap-2">
                  <h3 className="text-sm font-semibold">
                    {t("assignedRoles")}
                  </h3>
                  {roles.map((role) => (
                    <label
                      key={role.id}
                      className="flex items-start gap-3 rounded-md border p-3"
                    >
                      <Checkbox
                        data-system-member-role={role.id}
                        checked={assigned.includes(role.id)}
                        disabled={
                          busy ||
                          role.builtin ||
                          member.protected ||
                          !roleAllowed
                        }
                        onCheckedChange={(value) =>
                          void toggleRole(role, value === true)
                        }
                      />
                      <span className="min-w-0">
                        <strong className="block text-sm break-words">
                          {scope
                            ? role.name
                            : systemRoleLabel(
                                role,
                                t("fullSystemAdministrator"),
                                t("importedSystemPermissions")
                              )}
                        </strong>
                        <span className="text-xs text-muted-foreground">
                          {role.builtin
                            ? t("protectedSuperadminAuthority")
                            : role.description}
                        </span>
                      </span>
                    </label>
                  ))}
                </section>
              )}
            </>
          )
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("close")}
          </Button>
          {selected && (member ? memberAllowed : createAllowed) && (
            <Button disabled={busy || loading} onClick={() => void save()}>
              {t(member ? "save" : scope ? "create" : "addSystemAccount")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
