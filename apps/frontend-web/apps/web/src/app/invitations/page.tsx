import { AutoTable } from "@workspace/ui/components/auto-table/auto-table"
import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useRef, useState } from "react"
import { Ban, Copy, MailPlus, RefreshCw } from "lucide-react"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
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

  const submit = async (data: Record<string, unknown>) => {
    setBusy(true)
    setError("")
    try {
      const result = await iamApi.createInvitation({
        email: String(data.email ?? "").trim(),
        department_id: String(data.department_id || "") || undefined,
        role_ids: data.role_ids as string[],
        expires_in_hours: Number(data.expires_in_hours),
      })
      setOpen(false)
      setIssued(result)
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
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <PageHeader
        title={t("memberInvitations")}
        description={t("invitationManagementDescription")}
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
      <AutoTable
        fill
        toolbar={
          <>
            {allowed("invitation_create") && (
              <Button onClick={() => setOpen(true)}>
                <MailPlus />
                {t("createInvitation")}
              </Button>
            )}
          </>
        }
        onRefresh={refresh}
        searchable
        loading={!loaded}

        emptyContent={loadError ? null : t("noInvitations")}
        data={!loaded || loadError ? [] : items}
        rowKey="id"
        columns={[
          {
            field: "email",
            name: <>{t("email")}</>,
            render: (_, invitation) => (
              <>
                <strong className="block text-sm">{invitation.email}</strong>
                <span className="font-mono text-xs text-muted-foreground">
                  {invitation.id}
                </span>
              </>
            ),
          },
          {
            field: "status",
            name: <>{t("status")}</>,
            render: (_, invitation) => (
              <>
                <InvitationStatus status={invitation.status} />
              </>
            ),
          },
          {
            field: "department_id",
            name: <>{t("defaultDepartment")}</>,
            render: (_, invitation) => (
              <>
                {departments.find(
                  (department) => department.id === invitation.department_id
                )?.name ??
                  invitation.department_id ??
                  t("unassigned")}
              </>
            ),
          },
          {
            field: "role_ids",
            name: <>{t("defaultRole")}</>,
            render: (_, invitation) => (
              <>
                {invitation.role_ids.length
                  ? invitation.role_ids
                      .map(
                        (id) => roles.find((role) => role.id === id)?.name ?? id
                      )
                      .join("、")
                  : t("unassigned")}
              </>
            ),
          },
          {
            field: "expires_at",
            name: <>{t("expiresAt")}</>,
            render: (_, invitation) => (
              <>{new Date(invitation.expires_at).toLocaleString(locale)}</>
            ),
          },
          {
            field: "actions",
            type: "actions",
            name: <>{t("actions")}</>,
            render: (_, invitation) => (
              <>
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
              </>
            ),
          },
        ]}
      />

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("createMemberInvitation")}</DialogTitle>
            <DialogDescription>
              {t("invitationCreateDescription")}
            </DialogDescription>
          </DialogHeader>
          <AutoForm
            id="invitation-form"
            mode="edit"
            initialData={{ ...{} }}
            fields={{
              email: {
                name: t("email"),
                type: "email",
                required: true,
                disabled: busy,
                col: 2,
                config: {
                  id: "invitation-email",
                  type: "email",
                  maxLength: 320,
                  autoComplete: "email",
                },
              },
              expires_in_hours: {
                name: t("validHours"),
                type: "number",
                required: true,
                value: 72,
                disabled: busy,
                col: 2,
                config: { id: "invitation-expiry", min: 1, max: 720 },
              },
              department_id: {
                name: t("defaultDepartment"),
                type: "select",
                value: "",
                disabled: busy,
                col: 2,
                config: {
                  id: "invitation-department",
                  options: [
                    { value: "", label: t("unassigned") },
                    ...departments
                      .filter((department) => department.status === "active")
                      .map((department) => ({
                        value: department.id,
                        label: department.name,
                      })),
                  ],
                },
              },
              role_ids: {
                name: t("defaultRole"),
                type: "checkbox-group",
                value: [],
                disabled: busy,
                col: 2,
                config: {
                  options: roles.map((role) => ({
                    value: role.id,
                    label: role.name,
                  })),
                },
              },
            }}
            onSubmit={submit}
          >
            {roles.length === 0 && (
              <p className="text-sm text-muted-foreground md:col-span-2">
                {t("noRoles")}
              </p>
            )}
          </AutoForm>
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
    </div>
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
