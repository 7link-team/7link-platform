import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { Eye, Plus, GitCompareArrows, Bookmark } from "lucide-react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { PaginationPages } from "@workspace/ui/components/pagination"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { apiRoute } from "../../lib/api-route"
import {
  iamApi,
  type Constitution,
  type ConstitutionDetail,
  type ConstitutionDecision,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { useRequestContract } from "../../lib/use-request-contract"
import { constitutionDiff } from "./constitution-diff"

const states = ["DRAFT", "IN_REVIEW", "ACTIVE", "SUPERSEDED", "REJECTED"]

export function ProjectConstitutions({
  entityID,
  readOnly,
  onChanged,
}: {
  entityID: string
  readOnly: boolean
  onChanged: () => void
}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const allowed = useAllowed()
  const [search, setSearch] = useSearchParams()
  const rawOffset = Number(search.get("constitutionOffset") ?? 0)
  const offset =
    Number.isSafeInteger(rawOffset) && rawOffset >= 0 ? rawOffset : 0
  const rawLimit = Number(search.get("constitutionLimit") ?? 20)
  const limit = [10, 20, 50, 100].includes(rawLimit) ? rawLimit : 20
  const state = search.get("constitutionState") ?? "all"
  const summary = search.get("constitutionSummary") ?? ""
  const [rows, setRows] = useState<Constitution[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [reading, setReading] = useState(false)
  const [error, setError] = useState("")
  const [creating, setCreating] = useState(false)
  const [detail, setDetail] = useState<ConstitutionDetail | null>(null)
  const [baseline, setBaseline] = useState<{
    entity: string
    row: Constitution
  } | null>(null)
  const [comparison, setComparison] = useState<{
    entity: string
    before: Constitution
    after: Constitution
  } | null>(null)
  const selectedBaseline = baseline?.entity === entityID ? baseline.row : null
  const [subject, setSubject] = useState("")
  const [identityFailed, setIdentityFailed] = useState(false)
  const [identityAttempt, setIdentityAttempt] = useState(0)
  const pending = useRef<AbortController | null>(null)
  const detailPending = useRef<AbortController | null>(null)
  const load = useCallback(async () => {
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setLoading(true)
    setError("")
    try {
      const page = await iamApi.constitutions(
        entityID,
        {
          offset,
          limit,
          sort: "-version_no",
          state: state === "all" ? undefined : `eq:${state}`,
          change_summary: summary ? `like:${summary}` : undefined,
        },
        controller.signal
      )
      if (controller.signal.aborted) return
      if (offset > 0 && offset >= page.total) {
        setSearch(
          (previous) => {
            const next = new URLSearchParams(previous)
            next.set(
              "constitutionOffset",
              String(Math.max(0, Math.ceil(page.total / limit) - 1) * limit)
            )
            return next
          },
          { replace: true }
        )
        return
      }
      setRows(page.items)
      setTotal(page.total)
    } catch (cause) {
      if (!controller.signal.aborted) {
        setRows([])
        setTotal(0)
        setError(cause instanceof Error ? cause.message : t("workUnavailable"))
      }
    } finally {
      if (!controller.signal.aborted) setLoading(false)
    }
  }, [entityID, offset, limit, state, summary, setSearch, t])
  useEffect(() => {
    let active = true
    queueMicrotask(() => {
      if (active) void load()
    })
    return () => {
      active = false
      pending.current?.abort()
    }
  }, [load])
  useEffect(() => {
    let active = true
    void iamApi.session().then(
      (session) => {
        if (active) {
          setSubject(session.subject)
          setIdentityFailed(false)
        }
      },
      () => {
        if (active) setIdentityFailed(true)
      }
    )
    return () => {
      active = false
    }
  }, [identityAttempt])
  useEffect(() => () => detailPending.current?.abort(), [])
  const open = async (row: Constitution) => {
    if (!row.id) return
    detailPending.current?.abort()
    const controller = new AbortController()
    detailPending.current = controller
    setReading(true)
    setError("")
    try {
      const result = await iamApi.constitution(row.id, controller.signal)
      if (!controller.signal.aborted) setDetail(result)
    } catch (cause) {
      if (!controller.signal.aborted)
        setError(cause instanceof Error ? cause.message : t("workUnavailable"))
    } finally {
      if (!controller.signal.aborted) setReading(false)
    }
  }
  return (
    <>
      {error && <Alert>{error}</Alert>}
      {identityFailed && (
        <Alert>
          {t("constitutionIdentityUnavailable")}{" "}
          <Button
            size="sm"
            variant="outline"
            onClick={() => setIdentityAttempt((value) => value + 1)}
          >
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      {reading && <p role="status">{t("loading")}</p>}
      {selectedBaseline && (
        <div
          className="flex flex-wrap items-center gap-3 text-sm"
          role="status"
        >
          <span>
            {t("constitutionComparisonBase", {
              version: selectedBaseline.version_no,
            })}
          </span>
          <Button size="sm" variant="outline" onClick={() => setBaseline(null)}>
            {t("constitutionClearBase")}
          </Button>
        </div>
      )}
      <AutoTable
        fill
        data={loading ? [] : rows}
        rowKey="id"
        rowOffset={offset}
        loading={loading}
        pagination={false}
        onRefresh={load}
        exportable={!error}
        emptyContent={t("constitutionEmpty")}
        toolbar={
          <>
            <AutoForm
              key={JSON.stringify({ summary, state })}
              mode="edit"
              initialData={{ summary, state }}
              className="flex-1 md:grid-cols-2"
              fields={{
                summary: {
                  name: t("constitutionSummary"),
                  type: "text",
                  config: { type: "search" },
                },
                state: {
                  name: t("status"),
                  type: "select",
                  config: {
                    options: [
                      { value: "all", label: t("all") },
                      ...states.map((value) => ({
                        value,
                        label: t(`constitutionState_${value}`),
                      })),
                    ],
                  },
                },
              }}
              onSubmit={(values) =>
                setSearch((previous) => {
                  const next = new URLSearchParams(previous)
                  next.set(
                    "constitutionSummary",
                    String(values.summary ?? "").trim()
                  )
                  next.set("constitutionState", String(values.state ?? "all"))
                  next.delete("constitutionOffset")
                  return next
                })
              }
            >
              <Button type="submit" size="sm">
                {t("search")}
              </Button>
            </AutoForm>
            {!readOnly && allowed("project_read") && (
              <Button onClick={() => setCreating(true)}>
                <Plus />
                {t("constitutionPropose")}
              </Button>
            )}
          </>
        }
        columns={[
          { field: "version_no", name: t("projectVersion"), width: "110px" },
          {
            field: "state",
            name: t("status"),
            width: "150px",
            format: (_, row) => t(`constitutionState_${row.state}`),
          },
          {
            field: "change_summary",
            name: t("constitutionSummary"),
            width: "350px",
          },
          {
            field: "proposed_by",
            name: t("constitutionProposer"),
            width: "200px",
          },
          {
            field: "reviewed_by",
            name: t("constitutionReviewer"),
            width: "200px",
          },
          {
            field: "created_at",
            name: t("createdAt"),
            width: "210px",
            format: (_, row) => new Date(row.created_at).toLocaleString(locale),
          },
        ]}
        actions={[
          {
            name: t("constitutionDetails"),
            icon: Eye,
            onclick: open,
            disabled: () => reading,
          },
          {
            name: t("constitutionSelectBase"),
            icon: Bookmark,
            onclick: (row) => setBaseline({ entity: entityID, row }),
            disabled: (row) => row.id === selectedBaseline?.id,
          },
          {
            name: t("constitutionCompare"),
            icon: GitCompareArrows,
            onclick: (row) => {
              if (selectedBaseline)
                setComparison({
                  entity: entityID,
                  before: selectedBaseline,
                  after: row,
                })
            },
            disabled: (row) =>
              !selectedBaseline || row.id === selectedBaseline.id,
          },
        ]}
        footer={
          <div className="flex w-full flex-wrap items-center justify-between gap-3">
            <span className="text-sm text-muted-foreground">
              {t("projectPageSummary", {
                start: rows.length && !loading ? offset + 1 : 0,
                end: rows.length && !loading ? offset + rows.length : 0,
                total,
              })}
            </span>
            <label className="flex items-center gap-2 text-sm">
              {t("projectPageSize")}
              <select
                className="rounded-md border bg-background p-1"
                value={limit}
                disabled={loading}
                onChange={(event) =>
                  setSearch((previous) => {
                    const next = new URLSearchParams(previous)
                    next.set("constitutionLimit", event.target.value)
                    next.delete("constitutionOffset")
                    return next
                  })
                }
              >
                {[10, 20, 50, 100].map((size) => (
                  <option key={size} value={size}>
                    {size}
                  </option>
                ))}
              </select>
            </label>
            <PaginationPages
              page={Math.floor(offset / limit) + 1}
              totalPages={Math.max(1, Math.ceil(total / limit))}
              disabled={loading}
              onPageChange={(page) =>
                setSearch((previous) => {
                  const next = new URLSearchParams(previous)
                  next.set("constitutionOffset", String((page - 1) * limit))
                  return next
                })
              }
            />
          </div>
        }
      />
      {creating && (
        <ConstitutionProposalDialog
          entityID={entityID}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false)
            void load()
          }}
        />
      )}
      {detail && (
        <ConstitutionReviewDialog
          key={detail.id}
          detail={detail}
          canSubmit={
            !readOnly &&
            !!subject &&
            detail.proposed_by === subject &&
            allowed("project_read")
          }
          canReview={!readOnly && allowed("project_manage")}
          onClose={() => setDetail(null)}
          onChanged={() => {
            void load()
            onChanged()
          }}
        />
      )}
      {comparison?.entity === entityID && (
        <ConstitutionComparisonDialog
          key={`${comparison.before.id}:${comparison.after.id}`}
          before={comparison.before}
          after={comparison.after}
          onClose={() => setComparison(null)}
        />
      )}
    </>
  )
}

function ConstitutionComparisonDialog({
  before,
  after,
  onClose,
}: {
  before: Constitution
  after: Constitution
  onClose: () => void
}) {
  const t = useTranslations("iam")
  const [documents, setDocuments] = useState<
    [ConstitutionDetail, ConstitutionDetail] | null
  >(null)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    void Promise.all([
      iamApi.constitution(before.id!, controller.signal),
      iamApi.constitution(after.id!, controller.signal),
    ]).then(
      (result) => {
        if (!controller.signal.aborted) {
          setDocuments(result)
          setLoading(false)
        }
      },
      (cause) => {
        if (!controller.signal.aborted) {
          setError(
            cause instanceof Error ? cause.message : t("workUnavailable")
          )
          setLoading(false)
        }
      }
    )
    return () => controller.abort()
  }, [before.id, after.id, attempt, t])
  const rows = useMemo(
    () =>
      documents ? constitutionDiff(documents[0].body, documents[1].body) : [],
    [documents]
  )
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent className="flex h-[85dvh] min-h-0 flex-col overflow-hidden sm:max-w-5xl">
        <DialogHeader>
          <DialogTitle>{t("constitutionCompare")}</DialogTitle>
          <DialogDescription>
            {t("constitutionComparisonDirection", {
              before: before.version_no,
              after: after.version_no,
            })}
          </DialogDescription>
        </DialogHeader>
        {error && (
          <Alert>
            {error}{" "}
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                setError("")
                setLoading(true)
                setAttempt((value) => value + 1)
              }}
            >
              {t("retryNow")}
            </Button>
          </Alert>
        )}
        {documents && documents[0].body_sha256 === documents[1].body_sha256 && (
          <p role="status">{t("constitutionIdentical")}</p>
        )}
        <AutoTable
          fill
          data={rows}
          rowKey="id"
          loading={loading}
          pageSize={50}
          exportable={false}
          emptyContent={error ? t("workUnavailable") : t("constitutionEmpty")}
          columns={[
            {
              field: "before",
              name: t("constitutionBeforeLine"),
              width: "110px",
            },
            {
              field: "after",
              name: t("constitutionAfterLine"),
              width: "110px",
            },
            {
              field: "kind",
              name: t("status"),
              width: "130px",
              format: (_, row) => t(`constitutionDiff_${row.kind}`),
            },
            {
              field: "text",
              name: t("constitutionBody"),
              width: "600px",
              format: (_, row) => (
                <pre
                  className={`font-mono text-sm break-all whitespace-pre-wrap ${row.kind === "removed" ? "text-destructive" : row.kind === "added" ? "font-semibold" : "text-muted-foreground"}`}
                >
                  {row.kind === "removed"
                    ? "− "
                    : row.kind === "added"
                      ? "+ "
                      : "  "}
                  {row.text || "\u00a0"}
                </pre>
              ),
            },
          ]}
        />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("close")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ConstitutionProposalDialog({
  entityID,
  onClose,
  onCreated,
}: {
  entityID: string
  onClose: () => void
  onCreated: () => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const route = useMemo(
    () =>
      apiRoute("post", "/entities/{entity_id}/project/constitution-versions", {
        path: { entity_id: entityID },
      }),
    [entityID]
  )
  const metadata = useRequestContract(route)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const pending = useRef(false)
  const mounted = useRef(true)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending.current) onClose()
      }}
    >
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("constitutionPropose")}</DialogTitle>
          <DialogDescription>{t("constitutionFixedHint")}</DialogDescription>
        </DialogHeader>
        {error && <Alert>{error}</Alert>}
        {metadata.failed && (
          <Alert>
            {t("projectContractUnavailable")}{" "}
            <Button size="sm" variant="outline" onClick={metadata.retry}>
              {t("retryNow")}
            </Button>
          </Alert>
        )}
        <AutoForm
          id="constitution-proposal-form"
          contract={metadata.contract}
          fields={{
            change_summary: {
              name: t("constitutionSummary"),
              type: "textarea",
              disabled: busy,
            },
            body: {
              name: t("constitutionBody"),
              type: "textarea",
              disabled: busy,
              config: { rows: 16 },
            },
          }}
          onSubmit={async (values) => {
            if (
              pending.current ||
              !metadata.contract ||
              !allowed("project_read")
            )
              return
            const body = {
              change_summary: String(values.change_summary ?? ""),
              body: String(values.body ?? ""),
            }
            const identity = JSON.stringify(body)
            const key =
              attempt.current?.identity === identity
                ? attempt.current.key
                : crypto.randomUUID()
            attempt.current = { identity, key }
            pending.current = true
            setBusy(true)
            setError("")
            try {
              await iamApi.proposeConstitution(entityID, body, key)
              if (mounted.current) onCreated()
            } catch (cause) {
              if (mounted.current)
                setError(
                  cause instanceof Error ? cause.message : t("workUnavailable")
                )
            } finally {
              pending.current = false
              if (mounted.current) setBusy(false)
            }
          }}
        />
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            type="submit"
            form="constitution-proposal-form"
            disabled={busy || !metadata.contract}
          >
            {busy ? t("saving") : t("constitutionPropose")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ConstitutionReviewDialog({
  detail,
  canSubmit,
  canReview,
  onClose,
  onChanged,
}: {
  detail: ConstitutionDetail
  canSubmit: boolean
  canReview: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const allowed = useAllowed()
  const [current, setCurrent] = useState(detail)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const pending = useRef(false)
  const mounted = useRef(true)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  const route = useMemo(
    () =>
      current.id
        ? apiRoute("post", "/constitution-versions/{version_id}/decision", {
            path: { version_id: current.id },
          })
        : null,
    [current.id]
  )
  const metadata = useRequestContract(
    canReview && current.state === "IN_REVIEW" ? route : null
  )
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const execute = async (decision?: ConstitutionDecision) => {
    if (
      !current.id ||
      pending.current ||
      (decision
        ? !canReview || !allowed("project_manage") || !metadata.contract
        : !canSubmit || !allowed("project_read"))
    )
      return
    const identity = JSON.stringify({
      id: current.id,
      version: current.version,
      decision,
    })
    const key =
      attempt.current?.identity === identity
        ? attempt.current.key
        : crypto.randomUUID()
    attempt.current = { identity, key }
    pending.current = true
    setBusy(true)
    setError("")
    try {
      const receipt = decision
        ? await iamApi.decideConstitution(
            current.id,
            current.version,
            decision,
            key
          )
        : await iamApi.submitConstitution(current.id, current.version, key)
      if (mounted.current) {
        setCurrent((previous) => ({ ...previous, ...receipt }))
        attempt.current = null
        onChanged()
      }
    } catch (cause) {
      if (mounted.current)
        setError(cause instanceof Error ? cause.message : t("workUnavailable"))
    } finally {
      pending.current = false
      if (mounted.current) setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending.current) onClose()
      }}
    >
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>
            {t("constitutionVersion", { version: current.version_no })}
          </DialogTitle>
          <DialogDescription>{t("constitutionFixedHint")}</DialogDescription>
        </DialogHeader>
        {error && (
          <Alert>
            {error}{" "}
            <Button
              size="sm"
              variant="outline"
              disabled={busy}
              onClick={async () => {
                if (!current.id || pending.current) return
                pending.current = true
                setBusy(true)
                try {
                  const next = await iamApi.constitution(current.id)
                  if (mounted.current) {
                    setCurrent(next)
                    attempt.current = null
                    setError("")
                  }
                } catch (cause) {
                  if (mounted.current)
                    setError(
                      cause instanceof Error
                        ? cause.message
                        : t("workUnavailable")
                    )
                } finally {
                  pending.current = false
                  if (mounted.current) setBusy(false)
                }
              }}
            >
              {t("refresh")}
            </Button>
          </Alert>
        )}
        <AutoForm
          key={current.version}
          mode="detail"
          initialData={{
            ...current,
            state: t(`constitutionState_${current.state}`),
            reviewed_at: current.reviewed_at
              ? new Date(current.reviewed_at).toLocaleString(locale)
              : "",
          }}
          fields={{
            state: { name: t("status"), type: "text" },
            change_summary: {
              name: t("constitutionSummary"),
              type: "textarea",
            },
            body: {
              name: t("constitutionBody"),
              type: "textarea",
              config: { rows: 16 },
            },
            body_sha256: { name: "SHA-256", type: "textarea" },
            proposed_by: { name: t("constitutionProposer"), type: "text" },
            reviewed_by: {
              name: t("constitutionReviewer"),
              type: "text",
              show: !!current.reviewed_by,
            },
            reviewed_at: {
              name: t("constitutionReviewedAt"),
              type: "text",
              show: !!current.reviewed_at,
            },
            review_comment: {
              name: t("constitutionReviewComment"),
              type: "textarea",
              show: current.review_comment !== null,
            },
          }}
        />
        {current.state === "IN_REVIEW" && canReview && (
          <>
            {metadata.failed && (
              <Alert>
                {t("projectContractUnavailable")}{" "}
                <Button size="sm" variant="outline" onClick={metadata.retry}>
                  {t("retryNow")}
                </Button>
              </Alert>
            )}
            <p className="text-sm text-muted-foreground">
              {t("constitutionReviewHint")}
            </p>
            <AutoForm
              id="constitution-decision-form"
              contract={metadata.contract}
              fields={{
                result: {
                  name: t("constitutionDecision"),
                  type: "select",
                  value: "APPROVE",
                  disabled: busy,
                  config: {
                    options: [
                      { value: "APPROVE", label: t("constitutionApprove") },
                      { value: "REJECT", label: t("constitutionReject") },
                    ],
                  },
                },
                comment: {
                  name: t("constitutionReviewComment"),
                  type: "textarea",
                  disabled: busy,
                },
              }}
              onSubmit={(values) =>
                execute({
                  result: values.result as ConstitutionDecision["result"],
                  comment: String(values.comment ?? ""),
                })
              }
            />
          </>
        )}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {t("close")}
          </Button>
          {current.state === "DRAFT" && canSubmit && (
            <Button disabled={busy} onClick={() => void execute()}>
              {t("constitutionSubmit")}
            </Button>
          )}
          {current.state === "IN_REVIEW" && canReview && (
            <Button
              type="submit"
              form="constitution-decision-form"
              disabled={busy || !metadata.contract}
            >
              {t("constitutionRecordDecision")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
