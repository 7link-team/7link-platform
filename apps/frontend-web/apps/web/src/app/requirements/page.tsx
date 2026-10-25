import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { Eye, Plus, Trash2 } from "lucide-react"
import {
  AutoForm,
  type FieldRendererProps,
} from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Textarea } from "@workspace/ui/components/textarea"
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
import { PageHeader } from "../../components/page-header"
import { apiRoute } from "../../lib/api-route"
import {
  iamApi,
  type Requirement,
  type RequirementDetail,
  type RequirementInput,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { useRequestContract } from "../../lib/use-request-contract"
import { EditRequirement, type DraftEdit } from "./edit"
import { RequirementChanges } from "./changes"
import { WorkCodeLinks } from "../work-items/code-links"
import { RequirementReviewFacts } from "../review-sheet/page"
import { reviewSheetMessages } from "../review-sheet/messages"

type Criterion = { text: string; required: boolean }
function CriteriaField({
  value,
  onChange,
  disabled,
  readonly,
  labelId,
  describedBy,
  error,
  errorId,
  onEdit,
}: FieldRendererProps & { onEdit?: (index: number) => void }) {
  const t = useTranslations("iam")
  const rows = (Array.isArray(value) ? value : []) as Criterion[]
  return (
    <div
      role="group"
      aria-labelledby={labelId}
      aria-describedby={describedBy}
      className="space-y-3"
    >
      {rows.map((row, index) => (
        <fieldset
          key={index}
          disabled={disabled && !onEdit}
          className="space-y-2 rounded-md border p-3"
        >
          <legend className="px-1 text-sm">
            {t("workClauseNumber", { number: index + 1 })}
          </legend>
          <label className="flex flex-col gap-1 text-sm">
            {t("workCriterionText")}
            <Textarea
              value={row.text}
              readOnly={readonly}
              required={!readonly}
              aria-invalid={Boolean(error)}
              aria-describedby={describedBy}
              onChange={(e) =>
                onChange(
                  rows.map((item, i) =>
                    i === index ? { ...item, text: e.target.value } : item
                  )
                )
              }
            />
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={row.required}
              disabled={readonly || disabled}
              onChange={(e) =>
                onChange(
                  rows.map((item, i) =>
                    i === index ? { ...item, required: e.target.checked } : item
                  )
                )
              }
            />
            {t("requirementRequired")}
          </label>
          {onEdit && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onEdit(index)}
            >
              {t("requirementEdit")}
            </Button>
          )}
          {!readonly && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => onChange(rows.filter((_, i) => i !== index))}
            >
              <Trash2 />
              {t("workRemoveClause")}
            </Button>
          )}
        </fieldset>
      ))}
      {error && (
        <p id={errorId} role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      {!readonly && (
        <Button
          type="button"
          variant="outline"
          disabled={disabled || rows.length >= 50}
          onClick={() => onChange([...rows, { text: "", required: true }])}
        >
          <Plus />
          {t("workAddClause")}
        </Button>
      )}
    </div>
  )
}

export default function RequirementsPage() {
  const { tenantID = "", entityID = "" } = useParams()
  return <Requirements key={`${tenantID}:${entityID}`} entityID={entityID} />
}
function Requirements({ entityID }: { entityID: string }) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const allowed = useAllowed()
  const [search, setSearch] = useSearchParams()
  const rawOffset = Number(search.get("offset") ?? 0)
  const offset =
    Number.isSafeInteger(rawOffset) && rawOffset >= 0 ? rawOffset : 0
  const rawLimit = Number(search.get("limit") ?? 20)
  const limit = [10, 20, 50, 100].includes(rawLimit) ? rawLimit : 20
  const title = search.get("title") ?? ""
  const status = search.get("status") ?? "all"
  const sort = search.get("sort") ?? "priority,number"
  const [rows, setRows] = useState<Requirement[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [creating, setCreating] = useState(false)
  const [detail, setDetail] = useState<RequirementDetail | null>(null)
  const [editing, setEditing] = useState<DraftEdit | null>(null)
  const [reading, setReading] = useState(false)
  const [baselining, setBaselining] = useState(false)
  const [baselineError, setBaselineError] = useState("")
  const pending = useRef<AbortController | null>(null)
  const detailPending = useRef<AbortController | null>(null)
  const baselinePending = useRef(false)
  const baselineKey = useRef<{
    id: string
    version: number
    key: string
  } | null>(null)
  const mounted = useRef(true)
  const load = useCallback(async () => {
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setLoading(true)
    setError("")
    try {
      const page = await iamApi.requirements(
        entityID,
        {
          offset,
          limit,
          sort,
          title: title ? `like:${title}` : undefined,
          status: status === "all" ? undefined : `eq:${status}`,
        },
        controller.signal
      )
      if (controller.signal.aborted) return
      if (offset > 0 && offset >= page.total) {
        setSearch(
          (previous) => {
            const next = new URLSearchParams(previous)
            next.set(
              "offset",
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
  }, [entityID, offset, limit, sort, title, status, setSearch, t])
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
    mounted.current = true
    return () => {
      mounted.current = false
      detailPending.current?.abort()
    }
  }, [])
  const open = async (row: Requirement) => {
    if (!row.id) return
    detailPending.current?.abort()
    const controller = new AbortController()
    detailPending.current = controller
    setReading(true)
    setError("")
    setBaselineError("")
    try {
      const result = await iamApi.requirement(
        entityID,
        row.id,
        controller.signal
      )
      if (!controller.signal.aborted) setDetail(result)
    } catch (cause) {
      if (!controller.signal.aborted)
        setError(cause instanceof Error ? cause.message : t("workUnavailable"))
    } finally {
      if (!controller.signal.aborted) setReading(false)
    }
  }
  const baseline = async () => {
    if (!detail?.id || baselinePending.current || !allowed("requirement_write"))
      return
    const current = detail
    const id = detail.id
    if (
      baselineKey.current?.id !== current.id ||
      baselineKey.current.version !== current.version
    )
      baselineKey.current = {
        id,
        version: current.version,
        key: crypto.randomUUID(),
      }
    baselinePending.current = true
    setBaselining(true)
    setBaselineError("")
    try {
      await iamApi.baselineRequirement(
        entityID,
        id,
        current.version,
        baselineKey.current!.key
      )
      if (mounted.current) {
        await open(current)
        void load()
      }
    } catch (cause) {
      if (mounted.current)
        setBaselineError(
          cause instanceof Error ? cause.message : t("workUnavailable")
        )
    } finally {
      baselinePending.current = false
      if (mounted.current) setBaselining(false)
    }
  }
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
      <PageHeader
        title={t("requirements")}
        action={
          allowed("requirement_write") ? (
            <Button onClick={() => setCreating(true)}>
              <Plus />
              {t("requirementCreate")}
            </Button>
          ) : undefined
        }
      />
      {error && <Alert>{error}</Alert>}
      {reading && (
        <p role="status" className="text-sm">
          {t("loading")}
        </p>
      )}
      <AutoTable
        fill
        data={loading ? [] : rows}
        loading={loading}
        rowKey="id"
        rowOffset={offset}
        pagination={false}
        onRefresh={load}
        exportable={!error}
        emptyContent={t("requirementEmpty")}
        toolbar={
          <AutoForm
            key={JSON.stringify({ title, status, sort })}
            mode="edit"
            initialData={{ title, status, sort }}
            className="flex-1 md:grid-cols-3"
            fields={{
              title: {
                name: t("workTitle"),
                type: "text",
                config: { type: "search" },
              },
              status: {
                name: t("status"),
                type: "select",
                config: {
                  options: [
                    { value: "all", label: t("all") },
                    { value: "DRAFT", label: t("workDraft") },
                    { value: "BASELINED", label: t("requirementBaselined") },
                  ],
                },
              },
              sort: {
                name: t("projectSort"),
                type: "select",
                config: {
                  options: [
                    {
                      value: "priority,number",
                      label: t("requirementPriority"),
                    },
                    { value: "-number", label: t("projectNewest") },
                    { value: "number", label: t("workNumber") },
                    { value: "-updated_at", label: t("updatedAt") },
                  ],
                },
              },
            }}
            onSubmit={(values) => {
              const next = new URLSearchParams()
              next.set("limit", String(limit))
              for (const key of ["title", "status", "sort"]) {
                const value = String(values[key] ?? "").trim()
                if (value) next.set(key, value)
              }
              setSearch(next)
            }}
          >
            <Button type="submit" size="sm">
              {t("search")}
            </Button>
          </AutoForm>
        }
        columns={[
          { field: "number", name: t("workNumber"), width: "110px" },
          { field: "title", name: t("workTitle"), width: "360px" },
          {
            field: "priority",
            name: t("requirementPriority"),
            width: "140px",
            format: (_, row) => `P${row.priority}`,
          },
          {
            field: "status",
            name: t("status"),
            width: "180px",
            format: (_, row) =>
              row.status === "BASELINED"
                ? t("requirementBaselineVersion", {
                    version: row.current_version_no,
                  })
                : t("workDraft"),
          },
          {
            field: "current_version_no",
            name: t("requirementBaseline"),
            width: "170px",
          },
          {
            field: "updated_at",
            name: t("updatedAt"),
            width: "210px",
            format: (_, row) => new Date(row.updated_at).toLocaleString(locale),
          },
        ]}
        actions={[{ name: t("workDetails"), icon: Eye, onclick: open }]}
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
                onChange={(e) =>
                  setSearch((previous) => {
                    const next = new URLSearchParams(previous)
                    next.set("limit", e.target.value)
                    next.delete("offset")
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
                  next.set("offset", String((page - 1) * limit))
                  return next
                })
              }
            />
          </div>
        }
      />
      {creating && (
        <CreateRequirement
          entityID={entityID}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false)
            void load()
          }}
        />
      )}
      {detail && (
        <Dialog
          open
          onOpenChange={(isOpen) => {
            if (!isOpen && !baselinePending.current) setDetail(null)
          }}
        >
          <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-2xl">
            <DialogHeader>
              <DialogTitle>{detail.title}</DialogTitle>
              <DialogDescription>
                {detail.status === "DRAFT"
                  ? t("requirementBaselineHint")
                  : t("requirementFrozen")}
              </DialogDescription>
            </DialogHeader>
            {baselineError && (
              <Alert>
                {baselineError}{" "}
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => void open(detail)}
                >
                  {t("refresh")}
                </Button>
              </Alert>
            )}
            <WorkCodeLinks key={`requirement-code:${detail.id}`} entityID={entityID} detail={detail} targetKind="requirement" />
            <RequirementReviewFacts
              requirement={detail}
              frozenVersion={detail.version}
            />
            <AutoForm
              key={`${detail.id}:${detail.version}`}
              mode="detail"
              initialData={{
                description: detail.description,
                criteria: detail.criteria,
              }}
              fields={{
                description: { name: t("description"), type: "textarea" },
                criteria: {
                  name: t("workCriteria"),
                  type: "custom",
                  component: (props) => (
                    <CriteriaField
                      {...props}
                      onEdit={
                        detail.status === "DRAFT" &&
                        allowed("requirement_write")
                          ? (index) => setEditing({ kind: "criterion", index })
                          : undefined
                      }
                    />
                  ),
                },
              }}
            />
            {(detail.versions ?? []).map((version) => (
              <section
                key={version.version_no}
                className="space-y-2 border-t pt-3"
              >
                <h3 className="text-sm font-medium">
                  {t("requirementBaselineVersion", {
                    version: version.version_no,
                  })}
                </h3>
                <p className="text-xs break-all text-muted-foreground">
                  SHA-256 {version.content_hash}
                  <span className="block">
                    {!version.suite_hash && (
                      <span className="block">
                        {
                          (
                            reviewSheetMessages[
                              locale as keyof typeof reviewSheetMessages
                            ] ?? reviewSheetMessages["en-US"]
                          ).legacySuite
                        }
                      </span>
                    )}
                    {
                      (
                        reviewSheetMessages[
                          locale as keyof typeof reviewSheetMessages
                        ] ?? reviewSheetMessages["en-US"]
                      ).frozenSuite
                    }
                    : {version.suite_hash || "-"} (
                    {version.suite_case_count ?? 0})
                  </span>
                </p>
                <p className="text-xs text-muted-foreground">
                  {new Date(version.created_at).toLocaleString(locale)}
                </p>
              </section>
            ))}
            {!!detail.decisions?.length && (
              <section className="space-y-2 border-t pt-3">
                <h3 className="text-sm font-medium">
                  {
                    (
                      reviewSheetMessages[
                        locale as keyof typeof reviewSheetMessages
                      ] ?? reviewSheetMessages["en-US"]
                    ).decisionHistory
                  }
                </h3>
                {detail.decisions.map((decision) => (
                  <p key={decision.sheet_id} className="text-xs break-all">
                    {decision.action} ? v{decision.version_no} ?{" "}
                    {decision.authority_source} ?{" "}
                    {new Date(decision.decided_at).toLocaleString(locale)}
                  </p>
                ))}
              </section>
            )}
            <DialogFooter>
              {allowed("requirement_read") && (
                <Button variant="outline" asChild>
                  <Link
                    to={`../review-sheet?${detail.review?.state === "OPEN" ? `sheet=${detail.review.sheet_id}` : `create=1&requirement=${detail.id}&version=${detail.version}`}`}
                  >
                    {
                      (
                        reviewSheetMessages[
                          locale as keyof typeof reviewSheetMessages
                        ] ?? reviewSheetMessages["en-US"]
                      ).review
                    }
                  </Link>
                </Button>
              )}
              {detail.status === "DRAFT" && allowed("requirement_write") && (
                <>
                  <Button
                    variant="outline"
                    disabled={baselining}
                    onClick={() => setEditing({ kind: "metadata" })}
                  >
                    {t("requirementEdit")}
                  </Button>
                  <Button
                    variant="outline"
                    disabled={
                      baselining || (detail.criteria?.length ?? 0) >= 50
                    }
                    onClick={() => setEditing({ kind: "new" })}
                  >
                    {t("workAddClause")}
                  </Button>
                </>
              )}
              {detail.status === "DRAFT" && allowed("requirement_write") && (
                <Button
                  disabled={
                    baselining ||
                    !(detail.criteria ?? []).some(
                      (criterion) => criterion.required
                    )
                  }
                  onClick={baseline}
                >
                  {baselining ? t("saving") : t("requirementConfirmBaseline")}
                </Button>
              )}
            </DialogFooter>
            <RequirementChanges
              key={`${detail.id}:${detail.version}`}
              entityID={entityID}
              detail={detail}
              onSaved={() => {
                void open(detail)
                void load()
              }}
            />
          </DialogContent>
        </Dialog>
      )}
      {detail && editing && (
        <EditRequirement
          entityID={entityID}
          detail={detail}
          edit={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            void open(detail)
            void load()
          }}
        />
      )}
    </div>
  )
}

function CreateRequirement({
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
      apiRoute("post", "/entities/{entity_id}/requirements", {
        path: { entity_id: entityID },
      }),
    [entityID]
  )
  const metadata = useRequestContract(route)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const pending = useRef(false)
  const active = useRef(true)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
    }
  }, [])
  return (
    <Dialog
      open
      onOpenChange={(isOpen) => {
        if (!isOpen && !pending.current) onClose()
      }}
    >
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("requirementCreate")}</DialogTitle>
          <DialogDescription>{t("requirementCreateHint")}</DialogDescription>
        </DialogHeader>
        {error && <Alert>{error}</Alert>}
        {metadata.failed && (
          <Alert>
            {t("projectContractUnavailable")}{" "}
            <Button variant="outline" size="sm" onClick={metadata.retry}>
              {t("retryNow")}
            </Button>
          </Alert>
        )}
        <AutoForm
          id="requirement-create-form"
          contract={metadata.contract}
          fields={{
            title: { name: t("workTitle"), type: "text", disabled: busy },
            description: {
              name: t("description"),
              type: "textarea",
              value: "",
              disabled: busy,
            },
            priority: {
              name: t("requirementPriority"),
              type: "select",
              value: "1",
              disabled: busy,
              config: {
                options: [0, 1, 2, 3].map((value) => ({
                  value: String(value),
                  label: `P${value}`,
                })),
              },
            },
            criteria: {
              name: t("workCriteria"),
              type: "custom",
              component: CriteriaField,
              value: [{ text: "", required: true }],
              disabled: busy,
            },
          }}
          onSubmit={async (values) => {
            if (
              pending.current ||
              !metadata.contract ||
              !allowed("requirement_write")
            )
              return
            const body: RequirementInput = {
              title: String(values.title),
              description: String(values.description ?? ""),
              priority: Number(values.priority),
              criteria: values.criteria as Criterion[],
            }
            const identity = JSON.stringify(body)
            const key =
              attempt.current?.body === identity
                ? attempt.current.key
                : crypto.randomUUID()
            attempt.current = { body: identity, key }
            pending.current = true
            setBusy(true)
            setError("")
            try {
              await iamApi.createRequirement(entityID, body, key)
              if (active.current) onCreated()
            } catch (cause) {
              if (active.current)
                setError(
                  cause instanceof Error ? cause.message : t("workUnavailable")
                )
            } finally {
              pending.current = false
              if (active.current) setBusy(false)
            }
          }}
          onError={(cause) =>
            setError(
              cause instanceof Error ? cause.message : t("workUnavailable")
            )
          }
        />
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            type="submit"
            form="requirement-create-form"
            disabled={
              busy || !metadata.contract || !allowed("requirement_write")
            }
          >
            {busy ? t("saving") : t("requirementCreate")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
