import { AutoForm } from "@workspace/ui/components/auto-form"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useRef, useState } from "react"
import {
  Ban,
  Check,
  CircleCheckBig,
  Eye,
  LoaderCircle,
  Plus,
  X,
} from "lucide-react"
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
import { Alert } from "../../components/alert"
import { useAuth } from "../../components/auth"
import { PageHeader } from "../../components/page-header"
import {
  iamApi,
  type AccessReview,
  type AccessReviewDecision,
  type AccessReviewItem,
  type AccessReviewStatus,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

type PendingAction =
  | {
      kind: "decide"
      review: AccessReview
      item: AccessReviewItem
      decision: Exclude<AccessReviewDecision, "pending">
    }
  | { kind: "complete"; review: AccessReview }
  | { kind: "cancel"; review: AccessReview }

export default function AccessReviewsPage() {
  const locale = useLocale()

  const t = useTranslations("iam")
  const allowed = useAllowed()

  const { session } = useAuth()
  const [reviews, setReviews] = useState<AccessReview[]>([])
  const [selected, setSelected] = useState<AccessReview | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [dueAt, setDueAt] = useState("")
  const [action, setAction] = useState<PendingAction | null>(null)
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState("")
  const loadGeneration = useRef(0)
  const [error, setError] = useState("")

  const refresh = useCallback(async (reviewID?: string) => {
    const generation = ++loadGeneration.current
    setLoaded(false)
    setLoadError("")
    try {
      const [items, detail] = await Promise.all([
        iamApi.accessReviews(),
        reviewID ? iamApi.accessReview(reviewID) : Promise.resolve(null),
      ])
      if (generation !== loadGeneration.current) return
      setReviews(items)
      if (reviewID) setSelected(detail)
    } catch (cause) {
      if (generation !== loadGeneration.current) return
      setReviews([])
      setSelected(null)
      setLoadError((cause as Error).message)
    } finally {
      if (generation === loadGeneration.current) setLoaded(true)
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

  const updateDueAt = useCallback(
    (values: Record<string, unknown>) => setDueAt(String(values.due_at ?? "")),
    []
  )

  const openCreate = () => {
    setDueAt(toLocalInputValue(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000)))
    setCreateOpen(true)
  }

  const createReview = async (data: Record<string, unknown>) => {
    setBusy(true)
    setError("")
    try {
      const created = await iamApi.createAccessReview({
        name: String(data.name ?? "").trim(),
        due_at: new Date(String(data.due_at)).toISOString(),
      })
      setCreateOpen(false)
      setSelected(created)
      await refresh(created.id)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const applyAction = async (data: Record<string, unknown>) => {
    if (!action) return
    setBusy(true)
    setError("")
    try {
      if (action.kind === "decide") {
        await iamApi.decideAccessReviewItem(
          action.review.id,
          action.item.id,
          action.decision,
          String(data.note ?? "").trim()
        )
      } else if (action.kind === "complete") {
        await iamApi.completeAccessReview(action.review.id)
      } else {
        await iamApi.cancelAccessReview(action.review.id)
      }
      const reviewID = action.review.id
      setAction(null)
      await refresh(reviewID)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <PageHeader title={t("accessReviews")} />
      {error && <Alert>{error}</Alert>}
      {loadError && (
        <Alert>
          <p>{t.has(loadError) ? t(loadError) : loadError}</p>
          <Button variant="outline" onClick={() => void refresh(selected?.id)}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}

      <section
        className="flex min-h-0 flex-1 flex-col"
        aria-labelledby="review-campaigns-heading"
      >
        <h2
          id="review-campaigns-heading"
          className="mb-3 text-sm font-semibold"
        >
          {t("reviewCampaigns")}
        </h2>
        {!loadError && (
          <CampaignList
            toolbar={
              allowed("access_review_create") ? (
                <Button disabled={busy} onClick={openCreate}>
                  <Plus />
                  {t("newReview")}
                </Button>
              ) : undefined
            }
            onRefresh={() => refresh(selected?.id).catch(showError(setError))}
            items={reviews}
            loaded={loaded}
            selectedID={selected?.id}
            busy={busy}
            onSelect={(review) => {
              setBusy(true)
              setError("")
              void iamApi
                .accessReview(review.id)
                .then(setSelected)
                .catch(showError(setError))
                .finally(() => setBusy(false))
            }}
          />
        )}
      </section>

      {selected && (
        <section
          className="flex min-h-0 flex-1 flex-col border-t pt-4"
          aria-labelledby="review-detail-heading"
        >
          <div className="mb-4 flex flex-wrap items-start justify-between gap-4">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h2
                  id="review-detail-heading"
                  className="text-base font-semibold"
                >
                  {selected.name}
                </h2>
                <ReviewStatus status={selected.status} />
              </div>
              <p className="mt-1 font-mono text-xs text-muted-foreground">
                {selected.id}
              </p>
            </div>
            {selected.status === "open" && (
              <div className="flex flex-wrap gap-2">
                {allowed("access_review_manage") && (
                  <Button
                    variant="outline"
                    disabled={busy}
                    onClick={() =>
                      setAction({ kind: "cancel", review: selected })
                    }
                  >
                    <Ban />
                    {t("cancelReview")}
                  </Button>
                )}
                {allowed("access_review_manage") && (
                  <Button
                    disabled={busy || selected.pending > 0}
                    onClick={() =>
                      setAction({ kind: "complete", review: selected })
                    }
                  >
                    <CircleCheckBig />
                    {t("completeReview")}
                  </Button>
                )}
              </div>
            )}
          </div>
          <dl className="mb-4 grid grid-cols-2 gap-3 text-sm sm:grid-cols-5">
            <ReviewMetric label={t("total")} value={selected.total} />
            <ReviewMetric label={t("pending")} value={selected.pending} />
            <ReviewMetric label={t("retain")} value={selected.kept} />
            <ReviewMetric label={t("revoke")} value={selected.revoked} />
            <div>
              <dt className="text-xs text-muted-foreground">{t("deadline")}</dt>
              <dd className="mt-1 text-xs">
                {formatTime(selected.due_at, locale)}
              </dd>
            </div>
          </dl>
          <ReviewItems
            review={selected}
            currentSubject={session?.subject ?? ""}
            busy={busy}
            onRefresh={() => refresh(selected.id).catch(showError(setError))}
            onAction={setAction}
          />
        </section>
      )}

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("newAccessReview")}</DialogTitle>
            <DialogDescription>
              {t("accessReviewFormDescription")}
            </DialogDescription>
          </DialogHeader>
          <AutoForm
            id="access-review-create"
            fields={{
              name: {
                name: t("name"),
                type: "text",
                required: true,
                config: {
                  id: "access-review-name",
                  minLength: 3,
                  maxLength: 128,
                  autoFocus: true,
                },
              },
              due_at: {
                name: t("deadline"),
                type: "text",
                value: dueAt,
                required: true,
                config: {
                  id: "access-review-due-at",
                  type: "datetime-local",
                  min: toLocalInputValue(new Date()),
                },
              },
            }}
            onSubmit={createReview}
            onChange={updateDueAt}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              {t("cancel")}
            </Button>
            <Button
              type="submit"
              form="access-review-create"
              disabled={busy || !dueAt}
            >
              {busy && <LoaderCircle className="animate-spin" />}
              {t("create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(action)} onOpenChange={() => setAction(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{action && t(actionTitle(action))}</DialogTitle>
            <DialogDescription>
              {action &&
                (action.kind === "complete"
                  ? t("completeReviewConfirm", { name: action.review.name })
                  : action.kind === "cancel"
                    ? t("cancelReviewConfirm", { name: action.review.name })
                    : `${action.item.principal_name} · ${action.item.role_name}`)}
            </DialogDescription>
          </DialogHeader>
          <AutoForm
            id="access-review-action"
            fields={{
              note: {
                name: t("reviewNote"),
                type: "textarea",
                show: action?.kind === "decide",
                config: {
                  id: "access-review-note",
                  maxLength: 500,
                  autoFocus: true,
                },
              },
            }}
            onSubmit={applyAction}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setAction(null)}>
              {t("back")}
            </Button>
            <Button
              type="submit"
              form="access-review-action"
              variant={
                action?.kind === "cancel" ||
                (action?.kind === "decide" && action.decision === "revoke")
                  ? "destructive"
                  : "default"
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

function CampaignList({
  items,
  loaded,
  selectedID,
  busy,
  onSelect,
  toolbar,
  onRefresh,
}: {
  toolbar?: React.ReactNode
  onRefresh: () => Promise<void>
  items: AccessReview[]
  loaded: boolean
  selectedID?: string
  busy: boolean
  onSelect: (review: AccessReview) => void
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
          emptyContent={t("noReviews")}
          rowKey="id"
          searchable
          rowProps={(review) => ({
            "data-review-id": review.id,
            "data-state": selectedID === review.id ? "selected" : undefined,
          })}
          columns={[
            {
              field: "name",
              name: t("name"),
              render: (_, review) => (
                <>
                  <strong className="block text-sm">{review.name}</strong>
                  <span className="font-mono text-xs text-muted-foreground">
                    {review.id}
                  </span>
                </>
              ),
            },
            {
              field: "status",
              name: t("status"),
              render: (_, review) => (
                <>
                  <ReviewStatus status={review.status} />
                </>
              ),
            },
            {
              field: "progress",
              name: t("progress"),
              render: (_, review) => (
                <>
                  {review.total - review.pending}/{review.total}
                </>
              ),
            },
            {
              field: "due_at",
              name: t("deadline"),
              render: (_, review) => (
                <>
                  <div className="text-xs">
                    {formatTime(review.due_at, locale)}
                  </div>
                </>
              ),
            },
            {
              field: "owner_id",
              name: t("reviewOwner"),
              render: (_, review) => (
                <>
                  <div className="max-w-48 truncate text-sm">
                    {review.owner_id}
                  </div>
                </>
              ),
            },
            {
              field: "actions",
              type: "actions",
              name: t("actions"),
              render: (_, review) => (
                <>
                  <div className="text-right">
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={busy}
                      onClick={() => onSelect(review)}
                    >
                      <Eye />
                      {t("view")}
                    </Button>
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

function ReviewItems({
  review,
  currentSubject,
  busy,
  onAction,
  onRefresh,
}: {
  review: AccessReview
  currentSubject: string
  busy: boolean
  onRefresh: () => Promise<void>
  onAction: (action: PendingAction) => void
}) {
  const locale = useLocale()

  const t = useTranslations("iam")
  const allowed = useAllowed()

  const actions = (item: AccessReviewItem) =>
    item.decision === "pending" &&
    review.status === "open" &&
    allowed("access_review_decide") ? (
      <div className="flex justify-end gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={busy || item.principal_id === currentSubject}
          onClick={() =>
            onAction({ kind: "decide", review, item, decision: "keep" })
          }
        >
          <Check />
          {t("retain")}
        </Button>
        <Button
          size="sm"
          variant="destructive"
          disabled={busy || item.principal_id === currentSubject}
          onClick={() =>
            onAction({ kind: "decide", review, item, decision: "revoke" })
          }
        >
          <X />
          {t("revoke")}
        </Button>
      </div>
    ) : (
      <DecisionBadge decision={item.decision} />
    )

  return (
    <>
      <div className="flex min-h-0 flex-1 flex-col">
        <AutoTable
          fill
          onRefresh={onRefresh}
          data={review.items}
          rowKey="id"
          searchable
          rowProps={(item) => ({ "data-review-item-id": item.id })}
          columns={[
            {
              field: "principal_name",
              name: t("principal"),
              render: (_, item) => (
                <>
                  <strong className="block text-sm">
                    {item.principal_name}
                  </strong>
                  <span className="font-mono text-xs text-muted-foreground">
                    {item.principal_id}
                  </span>
                </>
              ),
            },
            {
              field: "role_name",
              name: t("role"),
              render: (_, item) => (
                <>
                  <strong className="block text-sm">{item.role_name}</strong>
                  <span className="font-mono text-xs text-muted-foreground">
                    {item.role_id}
                  </span>
                </>
              ),
            },
            {
              field: "grant_type",
              name: t("type"),
              render: (_, item) => (
                <>
                  <Badge variant="outline">
                    {item.grant_type === "permanent"
                      ? t("permanent")
                      : t("temporary")}
                  </Badge>
                </>
              ),
            },
            {
              field: "grant_created_at",
              name: t("grantedAt"),
              render: (_, item) => (
                <>
                  <div className="text-xs">
                    {formatTime(item.grant_created_at, locale)}
                  </div>
                </>
              ),
            },
            {
              field: "grant_expires_at",
              name: t("grantUntil"),
              render: (_, item) => (
                <>
                  <div className="text-xs">
                    {item.grant_expires_at
                      ? formatTime(item.grant_expires_at, locale)
                      : t("noExpiry")}
                  </div>
                </>
              ),
            },
            {
              field: "decided_by",
              name: t("decidedBy"),
              render: (_, item) => (
                <>
                  <div className="max-w-40 truncate text-sm">
                    {item.decided_by || "-"}
                  </div>
                </>
              ),
            },
            {
              field: "actions",
              type: "actions",
              name: t("reviewDecision"),
              render: (_, item) => (
                <>
                  <div className="text-right">{actions(item)}</div>
                </>
              ),
            },
          ]}
        />
      </div>
    </>
  )
}

function ReviewMetric({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 font-semibold tabular-nums">{value}</dd>
    </div>
  )
}

function ReviewStatus({ status }: { status: AccessReviewStatus }) {
  const t = useTranslations("iam")
  const value = reviewStatusLabels[status]
  return (
    <Badge className="shrink-0 whitespace-nowrap" variant={value.variant}>
      {t(value.label)}
    </Badge>
  )
}

function DecisionBadge({ decision }: { decision: AccessReviewDecision }) {
  const t = useTranslations("iam")
  const value = decisionLabels[decision]
  return (
    <Badge className="whitespace-nowrap" variant={value.variant}>
      {t(value.label)}
    </Badge>
  )
}

const reviewStatusLabels: Record<
  AccessReviewStatus,
  {
    label: string
    variant: "default" | "secondary" | "destructive" | "outline"
  }
> = {
  open: { label: "inProgress", variant: "default" },
  completed: { label: "completed", variant: "secondary" },
  cancelled: { label: "cancelled", variant: "outline" },
  expired: { label: "expired", variant: "destructive" },
}

const decisionLabels: Record<
  AccessReviewDecision,
  {
    label: string
    variant: "default" | "secondary" | "destructive" | "outline"
  }
> = {
  pending: { label: "pending", variant: "outline" },
  keep: { label: "retain", variant: "secondary" },
  revoke: { label: "revoked", variant: "destructive" },
}

function actionTitle(action: PendingAction): string {
  if (action.kind === "complete") return "completeAccessReview"
  if (action.kind === "cancel") return "cancelAccessReview"
  return action.decision === "keep" ? "retainGrant" : "revokeGrant"
}

function toLocalInputValue(value: Date): string {
  return new Date(value.getTime() - value.getTimezoneOffset() * 60_000)
    .toISOString()
    .slice(0, 16)
}

function formatTime(value: string, locale: string): string {
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value))
}

function showError(setError: (message: string) => void) {
  return (cause: unknown) => setError((cause as Error).message)
}
