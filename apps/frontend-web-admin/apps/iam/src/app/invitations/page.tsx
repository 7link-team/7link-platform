import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react"
import { Ban, Copy, MailPlus, RefreshCw } from "lucide-react"
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
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import {
  iamApi,
  type Department,
  type Invitation,
  type IssuedInvitation,
  type Role,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

export default function InvitationsPage() {
  const locale = useLocale()

  const t = useTranslations("iam")
  const allowed = useAllowed()

  const [items, setItems] = useState<Invitation[]>([])
  const [departments, setDepartments] = useState<Department[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const [open, setOpen] = useState(false)
  const [issued, setIssued] = useState<IssuedInvitation | null>(null)
  const [departmentID, setDepartmentID] = useState("")
  const [roleIDs, setRoleIDs] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState("")
  const [loadError, setLoadError] = useState("")
  const loadGeneration = useRef(0)

  const refresh = useCallback(async () => {
    const generation = ++loadGeneration.current
    setLoaded(false)
    setLoadError("")
    try {
      const next = await iamApi.invitations()
      if (generation === loadGeneration.current) setItems(next)
    } catch (cause) {
      if (generation === loadGeneration.current) {
        setItems([])
        setLoadError((cause as Error).message)
      }
    } finally {
      if (generation === loadGeneration.current) setLoaded(true)
    }
  }, [])

  useEffect(() => {
    let active = true
    void Promise.resolve().then(() => {
      if (active) void refresh()
    })
    // Both lists belong to other permissions; an invitation manager without
    // them still gets the page, with those selectors empty.
    void Promise.allSettled([
      allowed("dept_view") ? iamApi.departments() : Promise.resolve([]),
      allowed("role_view") ? iamApi.roles() : Promise.resolve([]),
    ]).then(([departmentResult, roleResult]) => {
      if (!active) return
      if (departmentResult.status === "fulfilled")
        setDepartments(departmentResult.value)
      if (roleResult.status === "fulfilled") setRoles(roleResult.value)
    })
    return () => {
      active = false
    }
  }, [refresh, allowed])

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    try {
      const result = await iamApi.createInvitation({
        email: String(data.get("email") ?? "").trim(),
        department_id: departmentID || undefined,
        role_ids: roleIDs,
        expires_in_hours: Number(data.get("expires_in_hours")),
      })
      setOpen(false)
      setIssued(result)
      setDepartmentID("")
      setRoleIDs([])
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const resend = async (invitation: Invitation) => {
    if (!window.confirm(t("resendInvitationConfirm"))) return
    setBusy(true)
    setError("")
    try {
      setIssued(await iamApi.resendInvitation(invitation.id))
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const revoke = async (invitation: Invitation) => {
    if (
      !window.confirm(t("revokeInvitationConfirm", { email: invitation.email }))
    )
      return
    setBusy(true)
    setError("")
    try {
      await iamApi.revokeInvitation(invitation.id)
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
        title={t("memberInvitations")}
        description={t("invitationManagementDescription")}
        action={
          allowed("invitation_create") && (
            <Button onClick={() => setOpen(true)}>
              <MailPlus />
              {t("createInvitation")}
            </Button>
          )
        }
      />
      {error && <Alert>{error}</Alert>}
      {loadError && (
        <Alert>
          <p>{t.has(loadError) ? t(loadError) : loadError}</p>
          <Button variant="outline" onClick={() => void refresh()}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      {!loaded && <p role="status">{t("loading")}</p>}
      <div className="overflow-x-auto rounded-md border bg-card">
        <Table className="min-w-[860px]">
          <TableHeader>
            <TableRow>
              <TableHead>{t("email")}</TableHead>
              <TableHead>{t("status")}</TableHead>
              <TableHead>{t("defaultDepartment")}</TableHead>
              <TableHead>{t("defaultRole")}</TableHead>
              <TableHead>{t("expiresAt")}</TableHead>
              <TableHead className="w-28 text-right">{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(!loaded || loadError ? [] : items).map((invitation) => (
              <TableRow key={invitation.id}>
                <TableCell>
                  <strong className="block text-sm">{invitation.email}</strong>
                  <span className="font-mono text-xs text-muted-foreground">
                    {invitation.id}
                  </span>
                </TableCell>
                <TableCell>
                  <InvitationStatus status={invitation.status} />
                </TableCell>
                <TableCell className="text-sm">
                  {departments.find(
                    (department) => department.id === invitation.department_id
                  )?.name ??
                    invitation.department_id ??
                    t("unassigned")}
                </TableCell>
                <TableCell className="text-sm">
                  {invitation.role_ids.length
                    ? invitation.role_ids
                        .map(
                          (id) =>
                            roles.find((role) => role.id === id)?.name ?? id
                        )
                        .join("、")
                    : t("unassigned")}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {new Date(invitation.expires_at).toLocaleString(locale)}
                </TableCell>
                <TableCell className="text-right">
                  {invitation.status === "pending" && (
                    <div className="flex justify-end gap-1">
                      {allowed("invitation_resend") && (
                        <Button
                          size="icon"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => void resend(invitation)}
                          aria-label={t("resendNamed", {
                            name: invitation.email,
                          })}
                          title={t("resendRotate")}
                        >
                          <RefreshCw />
                        </Button>
                      )}
                      {allowed("invitation_revoke") && (
                        <Button
                          size="icon"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => void revoke(invitation)}
                          aria-label={t("revokeNamed", {
                            name: invitation.email,
                          })}
                          title={t("revokeInvitation")}
                        >
                          <Ban />
                        </Button>
                      )}
                    </div>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {loaded && !loadError && items.length === 0 && (
          <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
            <MailPlus className="mb-2 size-7" />
            {t("noInvitations")}
          </div>
        )}
      </div>

      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next)
          if (!next) {
            setDepartmentID("")
            setRoleIDs([])
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("createMemberInvitation")}</DialogTitle>
            <DialogDescription>
              {t("invitationCreateDescription")}
            </DialogDescription>
          </DialogHeader>
          <form id="invitation-form" className="grid gap-4" onSubmit={submit}>
            <div className="grid gap-2">
              <Label htmlFor="invitation-email">{t("email")}</Label>
              <Input
                id="invitation-email"
                name="email"
                type="email"
                autoComplete="email"
                required
                maxLength={320}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="invitation-expiry">{t("validHours")}</Label>
              <Input
                id="invitation-expiry"
                name="expires_in_hours"
                type="number"
                min={1}
                max={720}
                defaultValue={72}
                required
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="invitation-department">
                {t("defaultDepartment")}
              </Label>
              <SimpleSelect
                id="invitation-department"
                value={departmentID || "__none__"}
                onValueChange={(value) =>
                  setDepartmentID(value === "__none__" ? "" : value)
                }
                options={[
                  { value: "__none__", label: t("unassigned") },
                  ...departments
                    .filter((department) => department.status === "active")
                    .map((department) => ({
                      value: department.id,
                      label: department.name,
                    })),
                ]}
              />
            </div>
            <fieldset className="grid gap-2">
              <legend className="text-sm font-medium">
                {t("defaultRole")}
              </legend>
              <div className="max-h-36 space-y-2 overflow-y-auto rounded-md border p-3">
                {roles.length ? (
                  roles.map((role) => (
                    <label
                      key={role.id}
                      className="flex min-h-7 items-center gap-2 text-sm"
                    >
                      <Checkbox
                        checked={roleIDs.includes(role.id)}
                        onCheckedChange={(checked) =>
                          setRoleIDs((current) =>
                            checked
                              ? [...current, role.id]
                              : current.filter((id) => id !== role.id)
                          )
                        }
                      />
                      {role.name}
                    </label>
                  ))
                ) : (
                  <span className="text-sm text-muted-foreground">
                    {t("noRoles")}
                  </span>
                )}
              </div>
            </fieldset>
          </form>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {t("cancel")}
            </Button>
            <Button type="submit" form="invitation-form" disabled={busy}>
              {busy ? t("issuing") : t("issueInvitation")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(issued)} onOpenChange={() => setIssued(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("oneTimeInvitation")}</DialogTitle>
            <DialogDescription>{t("invitationSecretOnce")}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="issued-invitation-token">
              {t("invitationCredential")}
            </Label>
            <div className="flex gap-2">
              <Input
                id="issued-invitation-token"
                className="font-mono text-xs"
                value={issued?.token ?? ""}
                readOnly
              />
              <Button
                size="icon"
                variant="outline"
                onClick={() =>
                  void navigator.clipboard
                    .writeText(issued?.token ?? "")
                    .catch(() => setError(t("clipboardRejected")))
                }
                aria-label={t("copyInvitation")}
                title={t("copyInvitation")}
              >
                <Copy />
              </Button>
            </div>
          </div>
          <DialogFooter>
            <Button onClick={() => setIssued(null)}>{t("done")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function InvitationStatus({ status }: { status: Invitation["status"] }) {
  const t = useTranslations("iam")

  const label = {
    pending: t("pendingAcceptance"),
    accepted: t("accepted"),
    revoked: t("revoked"),
    expired: t("expired"),
  }[status]
  return (
    <Badge
      variant={
        status === "pending"
          ? "secondary"
          : status === "accepted"
            ? "outline"
            : "destructive"
      }
    >
      {label}
    </Badge>
  )
}
