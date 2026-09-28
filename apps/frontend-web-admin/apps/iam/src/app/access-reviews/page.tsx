import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react"
import {
  Ban,
  Check,
  CircleCheckBig,
  Eye,
  ListChecks,
  LoaderCircle,
  Plus,
  RefreshCw,
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
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
import { Textarea } from "@workspace/ui/components/textarea"
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

  const openCreate = () => {
    setDueAt(toLocalInputValue(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000)))
    setCreateOpen(true)
  }

  const createReview = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    setBusy(true)
    setError("")
    try {
      const created = await iamApi.createAccessReview({
        name: String(data.get("name") ?? "").trim(),
        due_at: new Date(dueAt).toISOString(),
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

  const applyAction = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!action) return
    setBusy(true)
    setError("")
    try {
      if (action.kind === "decide") {
        const data = new FormData(event.currentTarget)
        await iamApi.decideAccessReviewItem(
          action.review.id,
          action.item.id,
          action.decision,
          String(data.get("note") ?? "").trim()
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
    <>
      <PageHeader
        title={t("accessReviews")}
        action={
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              disabled={busy}
              onClick={() =>
                void refresh(selected?.id).catch(showError(setError))
              }
            >
              <RefreshCw className={busy ? "animate-spin" : ""} />
              {t("refresh")}
            </Button>
            {allowed("access_review_create") && (
              <Button disabled={busy} onClick={openCreate}>
                <Plus />
                {t("newReview")}
              </Button>
            )}
          </div>
        }
      />
      {error && <Alert>{error}</Alert>}
      {loadError && (
        <Alert>
          <p>{t.has(loadError) ? t(loadError) : loadError}</p>
          <Button variant="outline" onClick={() => void refresh(selected?.id)}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}

      <section aria-labelledby="review-campaigns-heading">
        <h2
          id="review-campaigns-heading"
          className="mb-3 text-sm font-semibold"
        >
          {t("reviewCampaigns")}
        </h2>
        {!loadError && (
          <CampaignList
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
          className="mt-8 border-t pt-6"
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
          <form
            id="access-review-create"
            className="grid gap-4"
            onSubmit={createReview}
          >
            <div className="grid gap-2">
              <Label htmlFor="access-review-name">{t("name")}</Label>
              <Input
                id="access-review-name"
                name="name"
                minLength={3}
                maxLength={128}
                autoFocus
                required
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="access-review-due-at">{t("deadline")}</Label>
              <Input
                id="access-review-due-at"
                type="datetime-local"
                value={dueAt}
                min={toLocalInputValue(new Date())}
                onChange={(event) => setDueAt(event.target.value)}
                required
              />
            </div>
          </form>
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
          <form
            id="access-review-action"
            className="grid gap-2"
            onSubmit={applyAction}
          >
            {action?.kind === "decide" && (
              <>
                <Label htmlFor="access-review-note">{t("reviewNote")}</Label>
                <Textarea
                  id="access-review-note"
                  name="note"
                  maxLength={500}
                  autoFocus
                />
              </>
            )}
          </form>
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
    </>
  )
}

function CampaignList({
  items,
  loaded,
  selectedID,
  busy,
  onSelect,
}: {
  items: AccessReview[]
  loaded: boolean
  selectedID?: string
  busy: boolean
  onSelect: (review: AccessReview) => void
}) {
  const locale = useLocale()

  const t = useTranslations("iam")

  if (!loaded)
    return (
      <div
        className="grid min-h-32 place-items-center"
        aria-label={t("loadingReviews")}
      >
        <LoaderCircle className="animate-spin text-muted-foreground" />
      </div>
    )
  if (items.length === 0)
    return (
      <div className="grid min-h-40 place-items-center rounded-md border bg-card text-sm text-muted-foreground">
        <div className="grid justify-items-center gap-2">
          <ListChecks className="size-7" />
          {t("noReviews")}
        </div>
      </div>
    )
  return (
    <>
      <div className="space-y-2 md:hidden">
        {items.map((review) => (
          <article
            key={review.id}
            className="rounded-md border bg-card p-4"
            data-review-id={review.id}
          >
            <div className="flex items-start justify-between gap-3">
              <strong className="min-w-0 truncate text-sm">
                {review.name}
              </strong>
              <ReviewStatus status={review.status} />
            </div>
            <dl className="mt-3 grid grid-cols-[4rem_1fr] gap-x-3 gap-y-1 text-xs">
              <dt className="text-muted-foreground">{t("progress")}</dt>
              <dd>
                {review.total - review.pending}/{review.total}
              </dd>
              <dt className="text-muted-foreground">{t("due")}</dt>
              <dd>{formatTime(review.due_at, locale)}</dd>
              <dt className="text-muted-foreground">{t("reviewOwner")}</dt>
              <dd className="truncate">{review.owner_id}</dd>
            </dl>
            <Button
              className="mt-3 w-full"
              variant={selectedID === review.id ? "secondary" : "outline"}
              disabled={busy}
              onClick={() => onSelect(review)}
            >
              <Eye />
              {t("view")}
            </Button>
          </article>
        ))}
      </div>
      <div className="hidden overflow-x-auto rounded-md border bg-card md:block">
        <Table className="min-w-[760px]">
          <TableHeader>
            <TableRow>
              <TableHead>{t("name")}</TableHead>
              <TableHead>{t("status")}</TableHead>
              <TableHead>{t("progress")}</TableHead>
              <TableHead>{t("deadline")}</TableHead>
              <TableHead>{t("reviewOwner")}</TableHead>
              <TableHead className="w-24 text-right">{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((review) => (
              <TableRow
                key={review.id}
                data-review-id={review.id}
                data-state={selectedID === review.id ? "selected" : undefined}
              >
                <TableCell>
                  <strong className="block text-sm">{review.name}</strong>
                  <span className="font-mono text-xs text-muted-foreground">
                    {review.id}
                  </span>
                </TableCell>
                <TableCell>
                  <ReviewStatus status={review.status} />
                </TableCell>
                <TableCell>
                  {review.total - review.pending}/{review.total}
                </TableCell>
                <TableCell className="text-xs">
                  {formatTime(review.due_at, locale)}
                </TableCell>
                <TableCell className="max-w-48 truncate text-sm">
                  {review.owner_id}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy}
                    onClick={() => onSelect(review)}
                  >
                    <Eye />
                    {t("view")}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </>
  )
}

function ReviewItems({
  review,
  currentSubject,
  busy,
  onAction,
}: {
  review: AccessReview
  currentSubject: string
  busy: boolean
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
      <div className="space-y-2 md:hidden">
        {review.items.map((item) => (
          <article
            key={item.id}
            className="rounded-md border bg-card p-4"
            data-review-item-id={item.id}
          >
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <strong className="block truncate text-sm">
                  {item.principal_name}
                </strong>
                <span className="block truncate font-mono text-xs text-muted-foreground">
                  {item.principal_id}
                </span>
              </div>
              <Badge variant="outline">
                {item.grant_type === "permanent"
                  ? t("permanent")
                  : t("temporary")}
              </Badge>
            </div>
            <dl className="mt-3 grid grid-cols-[4rem_1fr] gap-x-3 gap-y-1 text-xs">
              <dt className="text-muted-foreground">{t("role")}</dt>
              <dd className="truncate">{item.role_name}</dd>
              <dt className="text-muted-foreground">{t("grantedAt")}</dt>
              <dd>{formatTime(item.grant_created_at, locale)}</dd>
              <dt className="text-muted-foreground">{t("grantUntil")}</dt>
              <dd>
                {item.grant_expires_at
                  ? formatTime(item.grant_expires_at, locale)
                  : t("noExpiry")}
              </dd>
              {item.principal_id === currentSubject &&
                item.decision === "pending" && (
                  <>
                    <dt className="text-muted-foreground">
                      {t("restriction")}
                    </dt>
                    <dd>{t("cannotReviewOwn")}</dd>
                  </>
                )}
            </dl>
            <div className="mt-3 border-t pt-3">{actions(item)}</div>
          </article>
        ))}
      </div>
      <div className="hidden overflow-x-auto rounded-md border bg-card md:block">
        <Table className="min-w-[980px]">
          <TableHeader>
            <TableRow>
              <TableHead>{t("principal")}</TableHead>
              <TableHead>{t("role")}</TableHead>
              <TableHead>{t("type")}</TableHead>
              <TableHead>{t("grantedAt")}</TableHead>
              <TableHead>{t("grantUntil")}</TableHead>
              <TableHead>{t("decidedBy")}</TableHead>
              <TableHead className="w-48 text-right">
                {t("reviewDecision")}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {review.items.map((item) => (
              <TableRow key={item.id} data-review-item-id={item.id}>
                <TableCell>
                  <strong className="block text-sm">
                    {item.principal_name}
                  </strong>
                  <span className="font-mono text-xs text-muted-foreground">
                    {item.principal_id}
                  </span>
                </TableCell>
                <TableCell>
                  <strong className="block text-sm">{item.role_name}</strong>
                  <span className="font-mono text-xs text-muted-foreground">
                    {item.role_id}
                  </span>
                </TableCell>
                <TableCell>
                  <Badge variant="outline">
                    {item.grant_type === "permanent"
                      ? t("permanent")
                      : t("temporary")}
                  </Badge>
                </TableCell>
                <TableCell className="text-xs">
                  {formatTime(item.grant_created_at, locale)}
                </TableCell>
                <TableCell className="text-xs">
                  {item.grant_expires_at
                    ? formatTime(item.grant_expires_at, locale)
                    : t("noExpiry")}
                </TableCell>
                <TableCell className="max-w-40 truncate text-sm">
                  {item.decided_by || "-"}
                </TableCell>
                <TableCell className="text-right">{actions(item)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
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
