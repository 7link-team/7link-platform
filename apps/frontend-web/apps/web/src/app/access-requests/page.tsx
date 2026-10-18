import { AutoForm } from "@workspace/ui/components/auto-form"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useState } from "react"
import { Ban, Check, LoaderCircle, Plus, Undo2, X } from "lucide-react"
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
import { AutoTable } from "@workspace/ui/components/auto-table"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@workspace/ui/components/tabs"
import { useOutletContext } from "react-router"
import { Alert } from "../../components/alert"
import { useAuth } from "../../components/auth"
import { PageHeader } from "../../components/page-header"
import {
  iamApi,
  type AccessRequest,
  type AccessRequestStatus,
  type EffectiveMenu,
  type RequestableRole,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { effectiveMenuPaths } from "../../lib/navigation"

type DecisionAction = "approve" | "reject" | "revoke" | "withdraw"

interface Decision {
  action: DecisionAction
  request: AccessRequest
}

export default function AccessRequestsPage() {
  const t = useTranslations("iam")

  const { session } = useAuth()
  const { menus } = useOutletContext<{ menus: EffectiveMenu[] | null }>()
  const canApprove = Boolean(
    menus && effectiveMenuPaths(menus).has("/access-requests")
  )
  const [mine, setMine] = useState<AccessRequest[]>([])
  const [queue, setQueue] = useState<AccessRequest[]>([])
  const [roles, setRoles] = useState<RequestableRole[]>([])
  const [tab, setTab] = useState("mine")
  const [createOpen, setCreateOpen] = useState(false)
  const [roleID, setRoleID] = useState("")
  const [expiresAt, setExpiresAt] = useState("")
  const [decision, setDecision] = useState<Decision | null>(null)
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState("")

  const refresh = useCallback(
    () =>
      Promise.all([
        iamApi.myAccessRequests(),
        iamApi.requestableRoles(),
        canApprove ? iamApi.accessRequests() : Promise.resolve([]),
      ]).then(([myRequests, availableRoles, approvalQueue]) => {
        setMine(myRequests)
        setRoles(availableRoles)
        setQueue(approvalQueue)
        setLoaded(true)
      }),
    [canApprove]
  )

  useEffect(() => {
    void refresh().catch((cause: Error) => {
      setError(cause.message)
      setLoaded(true)
    })
  }, [refresh])

  const updateRequestValues = useCallback((values: Record<string, unknown>) => {
    setRoleID(String(values.role_id ?? ""))
    setExpiresAt(String(values.access_expires_at ?? ""))
  }, [])

  const openCreate = () => {
    setRoleID(roles[0]?.id ?? "")
    setExpiresAt(toLocalInputValue(new Date(Date.now() + 8 * 60 * 60 * 1000)))
    setCreateOpen(true)
  }

  const submitRequest = async (data: Record<string, unknown>) => {
    setBusy(true)
    setError("")
    try {
      await iamApi.createAccessRequest({
        role_id: String(data.role_id ?? ""),
        reason: String(data.reason ?? "").trim(),
        access_expires_at: new Date(
          String(data.access_expires_at)
        ).toISOString(),
      })
      setCreateOpen(false)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const applyDecision = async (data: Record<string, unknown>) => {
    if (!decision) return
    const note = String(data.note ?? "").trim()
    setBusy(true)
    setError("")
    try {
      switch (decision.action) {
        case "approve":
          await iamApi.approveAccessRequest(decision.request.id, note)
          break
        case "reject":
          await iamApi.rejectAccessRequest(decision.request.id, note)
          break
        case "revoke":
          await iamApi.revokeAccessRequest(decision.request.id, note)
          break
        case "withdraw":
          await iamApi.withdrawAccessRequest(decision.request.id, note)
          break
      }
      setDecision(null)
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
        title={t("accessRequests")}
        description={t("accessRequestsDescription")}
      />
      {error && <Alert>{error}</Alert>}
      <Tabs
        className="flex min-h-0 flex-1 flex-col"
        value={canApprove ? tab : "mine"}
        onValueChange={setTab}
      >
        <TabsList className="mb-4">
          <TabsTrigger value="mine">{t("myRequests")}</TabsTrigger>
          {canApprove && (
            <TabsTrigger value="queue">{t("approvalQueue")}</TabsTrigger>
          )}
        </TabsList>
        <TabsContent className="flex min-h-0 flex-1 flex-col" value="mine">
          <AccessRequestTable
            items={mine}
            loaded={loaded}
            mode="mine"
            currentSubject={session?.subject ?? ""}
            busy={busy}
            onRefresh={() => refresh().catch(showError(setError))}
            toolbar={
              <Button
                disabled={roles.length === 0 || busy}
                onClick={openCreate}
              >
                <Plus />
                {t("newRequest")}
              </Button>
            }
            onDecision={setDecision}
          />
        </TabsContent>
        {canApprove && (
          <TabsContent className="flex min-h-0 flex-1 flex-col" value="queue">
            <AccessRequestTable
              items={queue}
              loaded={loaded}
              mode="queue"
              currentSubject={session?.subject ?? ""}
              busy={busy}
              onRefresh={() => refresh().catch(showError(setError))}
              toolbar={
                <Button
                  disabled={roles.length === 0 || busy}
                  onClick={openCreate}
                >
                  <Plus />
                  {t("newRequest")}
                </Button>
              }
              onDecision={setDecision}
            />
          </TabsContent>
        )}
      </Tabs>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("newAccessRequest")}</DialogTitle>
            <DialogDescription>
              {t("accessRequestFormDescription")}
            </DialogDescription>
          </DialogHeader>
          <AutoForm
            id="access-request-form"
            fields={{
              role_id: {
                name: t("role"),
                type: "select",
                value: roleID,
                config: {
                  id: "access-request-role",
                  options: roles.map((role) => ({
                    value: role.id,
                    label: role.name,
                  })),
                  placeholder: t("selectRole"),
                },
              },
              access_expires_at: {
                name: t("accessDeadline"),
                type: "text",
                value: expiresAt,
                required: true,
                config: {
                  id: "access-request-expiry",
                  type: "datetime-local",
                  min: toLocalInputValue(new Date()),
                },
              },
              reason: {
                name: t("requestReason"),
                type: "textarea",
                required: true,
                config: {
                  id: "access-request-reason",
                  minLength: 3,
                  maxLength: 500,
                },
              },
            }}
            onSubmit={submitRequest}
            onChange={updateRequestValues}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              {t("cancel")}
            </Button>
            <Button
              type="submit"
              form="access-request-form"
              disabled={busy || !roleID || !expiresAt}
            >
              {busy && <LoaderCircle className="animate-spin" />}
              {t("submitRequest")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(decision)} onOpenChange={() => setDecision(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {decision && t(decisionTitle[decision.action])}
            </DialogTitle>
            <DialogDescription>
              {decision?.request.requester_id} · {decision?.request.role_name}
            </DialogDescription>
          </DialogHeader>
          <AutoForm
            id="access-decision-form"
            fields={{
              note: {
                name:
                  decision?.action === "approve"
                    ? t("approvalNote")
                    : t("reason"),
                type: "textarea",
                config: {
                  id: "access-decision-note",
                  maxLength: 500,
                  autoFocus: true,
                },
              },
            }}
            onSubmit={applyDecision}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setDecision(null)}>
              {t("cancel")}
            </Button>
            <Button
              type="submit"
              form="access-decision-form"
              variant={
                decision?.action === "approve" ? "default" : "destructive"
              }
              disabled={busy}
            >
              {busy && <LoaderCircle className="animate-spin" />}
              {t("confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function AccessRequestTable({
  items,
  loaded,
  mode,
  currentSubject,
  busy,
  onDecision,
  onRefresh,
  toolbar,
}: {
  toolbar: React.ReactNode
  onRefresh: () => Promise<void>
  items: AccessRequest[]
  loaded: boolean
  mode: "mine" | "queue"
  currentSubject: string
  busy: boolean
  onDecision: (decision: Decision) => void
}) {
  const locale = useLocale()

  const t = useTranslations("iam")

  return (
    <>
      <div className="flex min-h-0 flex-1 flex-col">
        <AutoTable
          fill
          toolbar={toolbar}
          onRefresh={onRefresh}
          data={items}
          loading={!loaded}
          emptyContent={t("noAccessRequests")}
          rowKey="id"
          searchable
          columns={[
            {
              field: "requester_id",
              name: t("requester"),
              render: (_, request) => (
                <>
                  <strong className="block text-sm">
                    {request.requester_id}
                  </strong>
                  <span className="font-mono text-xs text-muted-foreground">
                    {request.id}
                  </span>
                </>
              ),
            },
            {
              field: "role_name",
              name: t("role"),
              render: (_, request) => (
                <>
                  <strong className="block text-sm">{request.role_name}</strong>
                  <span className="font-mono text-xs text-muted-foreground">
                    {request.role_id}
                  </span>
                </>
              ),
            },
            {
              field: "reason",
              name: t("reason"),
              render: (_, request) => (
                <>
                  <div className="max-w-72 text-sm whitespace-normal">
                    {request.reason}
                  </div>
                </>
              ),
            },
            {
              field: "status",
              name: t("status"),
              render: (_, request) => (
                <>
                  <RequestStatus status={request.status} />
                </>
              ),
            },
            {
              field: "access_expires_at",
              name: t("accessUntil"),
              render: (_, request) => (
                <>
                  <div className="text-xs text-muted-foreground">
                    {formatTime(request.access_expires_at, locale)}
                  </div>
                </>
              ),
            },
            {
              field: "decided_by",
              name: t("approver"),
              render: (_, request) => (
                <>
                  <div className="text-sm">{request.decided_by || "-"}</div>
                </>
              ),
            },
            {
              field: "actions",
              type: "actions",
              name: t("actions"),
              render: (_, request) => (
                <>
                  <div className="text-right">
                    <RequestActions
                      request={request}
                      mode={mode}
                      currentSubject={currentSubject}
                      busy={busy}
                      onDecision={onDecision}
                    />
                  </div>
                </>
              ),
            },
          ]}
        />
      </div>
    </>
  )
}

function RequestActions({
  request,
  mode,
  currentSubject,
  busy,
  onDecision,
}: {
  request: AccessRequest
  mode: "mine" | "queue"
  currentSubject: string
  busy: boolean
  onDecision: (decision: Decision) => void
}) {
  const t = useTranslations("iam")
  // Reaching the queue is access_request_view; deciding on it is
  // access_request_approve, and the two are granted separately.
  const allowed = useAllowed()

  if (mode === "mine") {
    if (request.status !== "pending" && request.status !== "approved")
      return null
    return (
      <IconAction
        label={
          request.status === "pending"
            ? t("withdrawRequest")
            : t("relinquishAccess")
        }
        disabled={busy}
        onClick={() => onDecision({ action: "withdraw", request })}
      >
        <Undo2 />
      </IconAction>
    )
  }
  if (request.requester_id === currentSubject && request.status === "pending")
    return (
      <span className="text-xs text-muted-foreground">
        {t("otherApprovalRequired")}
      </span>
    )
  if (!allowed("access_request_approve")) return null
  if (request.status === "pending")
    return (
      <div className="flex justify-end gap-1">
        <IconAction
          label={t("approveRequest")}
          disabled={busy}
          onClick={() => onDecision({ action: "approve", request })}
        >
          <Check />
        </IconAction>
        <IconAction
          label={t("denyRequest")}
          disabled={busy}
          onClick={() => onDecision({ action: "reject", request })}
        >
          <X />
        </IconAction>
      </div>
    )
  if (request.status === "approved")
    return (
      <IconAction
        label={t("revokeAccess")}
        disabled={busy}
        onClick={() => onDecision({ action: "revoke", request })}
      >
        <Ban />
      </IconAction>
    )
  return null
}

function IconAction({
  label,
  children,
  ...props
}: React.ComponentProps<typeof Button> & { label: string }) {
  return (
    <Button
      type="button"
      size="icon"
      variant="ghost"
      aria-label={label}
      title={label}
      {...props}
    >
      {children}
    </Button>
  )
}

function RequestStatus({ status }: { status: AccessRequestStatus }) {
  const t = useTranslations("iam")
  const value = statusLabels[status]
  return (
    <Badge className="shrink-0 whitespace-nowrap" variant={value.variant}>
      {t(value.label)}
    </Badge>
  )
}

const statusLabels: Record<
  AccessRequestStatus,
  {
    label: string
    variant: "default" | "secondary" | "destructive" | "outline"
  }
> = {
  pending: { label: "pendingApproval", variant: "outline" },
  approved: { label: "approved", variant: "secondary" },
  rejected: { label: "denied", variant: "destructive" },
  cancelled: { label: "withdrawn", variant: "outline" },
  revoked: { label: "revoked", variant: "destructive" },
  expired: { label: "expired", variant: "outline" },
}

const decisionTitle: Record<DecisionAction, string> = {
  approve: "approveAccessRequest",
  reject: "denyAccessRequest",
  revoke: "revokeAccess",
  withdraw: "withdrawOrRelinquish",
}

function toLocalInputValue(value: Date): string {
  const offset = value.getTimezoneOffset() * 60_000
  return new Date(value.getTime() - offset).toISOString().slice(0, 16)
}

function formatTime(value: string, locale: string): string {
  return new Date(value).toLocaleString(locale)
}

function showError(setError: (message: string) => void) {
  return (cause: unknown) => setError((cause as Error).message)
}
