import { useEffect, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { AutoForm, type FieldConfig } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAuth } from "../../components/auth"
import { useAllowed } from "../../lib/permissions"
import { iamApi, type RequirementDetail } from "../../lib/iam-api"
import { reviewSheetMessages } from "./messages"
import {
  reviewCreateBody,
  reviewOpinionBody,
  reviewSheetApi,
  type Sheet,
  type ReviewReceipt,
  type OpinionRecord,
  type ReviewType,
  type ReviewPage,
} from "./api"
import { ReviewTypes } from "./review-types"
const api = reviewSheetApi()
export default function ReviewSheetPage() {
  const { entityID = "", tenantID = "" } = useParams()
  return (
    <Reviews
      key={`${tenantID}:${entityID}`}
      entity={entityID}
      tenant={tenantID}
    />
  )
}
function Reviews({ entity, tenant }: { entity: string; tenant: string }) {
  const locale = useLocale(),
    l =
      reviewSheetMessages[locale as keyof typeof reviewSheetMessages] ??
      reviewSheetMessages["en-US"],
    allowed = useAllowed(),
    { session } = useAuth()
  const [params, setParams] = useSearchParams(),
    cursor = params.get("cursor") ?? "",
    trail = params.get("trail")?.split(",") ?? [],
    selectedID = params.get("sheet") ?? "",
    state = params.get("state") ?? ""
  const [page, setPage] = useState<ReviewPage<Sheet> | null>(null),
    [loaded, setLoaded] = useState(""),
    [revision, setRevision] = useState(0),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [creating, setCreating] = useState(params.get("create") === "1")
  const [reviewTypes, setReviewTypes] = useState<ReviewType[]>([]),
    [typeID, setTypeID] = useState("")
  const detailIdentity = `${entity}:${selectedID}:${revision}`
  const [detail, setDetail] = useState<{
    identity: string
    sheet?: Sheet
    error?: string
  } | null>(null)
  const configuredType = reviewTypes.find((t) => t.id === typeID)
  const pending = useRef<{ signature: string; key: string } | null>(null),
    identity = `${entity}:${cursor}:${revision}`,
    read = allowed("requirement_read"),
    loading = loaded !== identity
  useEffect(() => {
    if (!read) return
    const c = new AbortController()
    void api
      .list(entity, cursor, c.signal)
      .then((v) => {
        if (!c.signal.aborted) {
          setPage(v)
          setLoaded(identity)
          setError("")
        }
      })
      .catch((e) => {
        if (!c.signal.aborted) {
          setPage(null)
          setLoaded(identity)
          setError(e instanceof Error ? e.message : l.error)
        }
      })
    return () => c.abort()
  }, [entity, cursor, revision, identity, read, l.error])
  useEffect(() => {
    if (!read || !selectedID) return
    const controller = new AbortController()
    void api.get(entity, selectedID, controller.signal).then(
      (sheet) => {
        if (!controller.signal.aborted)
          setDetail({ identity: detailIdentity, sheet })
      },
      (failure) => {
        if (!controller.signal.aborted)
          setDetail({
            identity: detailIdentity,
            error: failure instanceof Error ? failure.message : l.error,
          })
      }
    )
    return () => controller.abort()
  }, [entity, selectedID, revision, detailIdentity, read, l.error])
  const selected =
    read && selectedID && detail?.identity === detailIdentity
      ? detail.sheet
      : undefined
  const change = (key: string, value: string) =>
    setParams((old) => {
      const next = new URLSearchParams(old)
      if (value) next.set(key, value)
      else next.delete(key)
      return next
    })
  const run = async (
    signature: string,
    fn: (key: string) => Promise<ReviewReceipt>
  ) => {
    if (busy) return
    setBusy(true)
    setError("")
    if (pending.current?.signature !== signature)
      pending.current = { signature, key: crypto.randomUUID() }
    try {
      const value = await fn(pending.current.key)
      pending.current = null
      change("sheet", value.id)
      setRevision((v) => v + 1)
      setCreating(false)
    } catch (e) {
      setError(e instanceof Error ? e.message : l.error)
      throw e
    } finally {
      setBusy(false)
    }
  }
  const action = (body: { version: number; [key: string]: unknown }) =>
    selected &&
    run(JSON.stringify([selected.id, body]), (key) =>
      api.action(entity, selected.id, body, key)
    )
  const fields: Record<string, FieldConfig> = {
    target_kind: {
      name: l.targetKind,
      type: "select",
      value: "REQUIREMENT",
      required: true,
      config: {
        options: [
          { value: "REQUIREMENT", label: l.requirement },
          { value: "WORK_ITEM", label: l.workItem },
        ],
      },
    },
    target_id: {
      value: params.get("requirement") ?? "",
      name: l.targetID,
      type: "text",
      required: true,
      config: { inputMode: "numeric", pattern: "[1-9][0-9]{0,18}" },
    },
    target_version: {
      value: Number(params.get("version")) || 1,
      name: l.targetVersion,
      type: "number",
      required: true,
    },
    review_type: {
      name: l.reviewType,
      type: "text",
      required: true,
      config: { maxLength: 100 },
    },
    reviewers: { name: l.reviewers, type: "textarea", required: true },
    minimum: { name: l.minimum, type: "number", required: true },
    due_at: { name: l.due, type: "datetime" },
  }
  if (configuredType) {
    delete fields.review_type
    fields.reviewers = {
      name: l.reviewers,
      type: "textarea",
      required: true,
      value: configuredType.settings.default_reviewer_ids.join(", "),
    }
    delete fields.minimum
  }
  const opinionFields: Record<string, FieldConfig> = {
    decision: {
      name: l.decision,
      type: "select",
      required: true,
      value: "PASS",
      config: {
        options: [
          { value: "PASS", label: l.pass },
          { value: "REJECT", label: l.reject },
          { value: "SUGGEST", label: l.suggest },
        ],
      },
    },
    comment: { name: l.comment, type: "textarea", config: { maxLength: 2000 } },
  }
  for (const c of selected?.document.criteria ?? [])
    opinionFields[`criterion_${c.id}`] = {
      name: `${c.code}: ${c.text}`,
      type: "textarea",
      config: { maxLength: 2000 },
    }
  return (
    <main className="min-w-0 space-y-5">
      <PageHeader title={l.title} description={l.description} />
      <p className="text-sm text-muted-foreground" role="status">
        {l.unavailable}
      </p>
      {!read ? (
        <Alert>{l.denied}</Alert>
      ) : (
        <>
          {error && <Alert>{error}</Alert>}
          <div className="flex flex-wrap items-end gap-3">
            <label className="grid gap-1 text-sm">
              {l.state}
              <select
                className="min-h-11 rounded-md border bg-background px-3"
                value={state}
                onChange={(e) => change("state", e.target.value)}
              >
                <option value="">{l.all}</option>
                {["OPEN", "PASSED", "REJECTED", "CANCELLED"].map((v) => (
                  <option key={v}>{v}</option>
                ))}
              </select>
            </label>
            <Button
              variant="outline"
              disabled={loading || busy}
              onClick={() => setRevision((v) => v + 1)}
            >
              {l.refresh}
            </Button>
            {allowed("review_initiate") && (
              <Button disabled={busy} onClick={() => setCreating((v) => !v)}>
                {l.create}
              </Button>
            )}
          </div>
          {creating && (
            <section className="space-y-3 rounded-lg border p-4">
              <h2 className="font-semibold">{l.create}</h2>
              <label className="grid gap-1 text-sm">
                {l.reviewType}
                <select
                  className="min-h-11 rounded-md border bg-background px-3"
                  value={typeID}
                  onChange={(e) => setTypeID(e.target.value)}
                >
                  <option value="">{l.legacyType}</option>
                  {reviewTypes
                    .filter(
                      (t) =>
                        t.settings.applies_to.requirement ||
                        t.settings.applies_to.work_item_types.length > 0
                    )
                    .map((t) => (
                      <option key={t.id} value={t.id}>
                        {t.settings.name}
                      </option>
                    ))}
                </select>
              </label>
              {!reviewTypes.length && (
                <p className="text-sm text-muted-foreground">{l.noTypes}</p>
              )}
              <AutoForm
                key={configuredType?.id ?? "legacy"}
                fields={fields}
                onSubmit={async (values) => {
                  const body = {
                    ...reviewCreateBody({
                      ...values,
                      minimum:
                        configuredType?.settings.min_reviewers ??
                        values.minimum,
                    }),
                    ...(configuredType
                      ? { review_type_id: configuredType.id }
                      : {}),
                  }
                  await run(JSON.stringify(body), (key) =>
                    api.create(entity, body, key)
                  )
                }}
                onError={(e) =>
                  setError(e instanceof Error ? e.message : l.error)
                }
              >
                <Button type="submit" disabled={busy}>
                  {l.create}
                </Button>
              </AutoForm>
            </section>
          )}
          <AutoTable
            data={(loading ? [] : (page?.items ?? [])).filter(
              (v) => !state || v.state === state
            )}
            loading={loading}
            rowKey="id"
            pagination={false}
            exportable={false}
            emptyContent={error ? l.error : l.empty}
            columns={[
              {
                field: "requirement_id",
                name: l.targetID,
                render: (_, row) =>
                  `${row.document.target_kind ?? "REQUIREMENT"}: ${row.work_item_id ?? row.requirement_id}`,
              },
              {
                field: "document",
                name: l.reviewType,
                render: (_, row) => row.document.review_type,
              },
              { field: "state", name: l.state },
              { field: "initiator_id", name: l.initiator },
            ]}
            actions={[
              { name: l.view, onclick: (row) => change("sheet", row.id) },
            ]}
          />
          <div className="flex justify-end gap-2">
            <Button
              variant="outline"
              disabled={loading || !cursor}
              onClick={() =>
                setParams((old) => {
                  const next = new URLSearchParams(old)
                  const previous = trail.at(-1) ?? ""
                  if (previous) next.set("cursor", previous)
                  else next.delete("cursor")
                  if (trail.length > 1)
                    next.set("trail", trail.slice(0, -1).join(","))
                  else next.delete("trail")
                  return next
                })
              }
            >
              {l.previous}
            </Button>
            <Button
              variant="outline"
              disabled={loading || !page?.has_more || !page.next_cursor}
              onClick={() =>
                setParams((old) => {
                  const next = new URLSearchParams(old)
                  next.set("cursor", page!.next_cursor!)
                  next.set("trail", [...trail, cursor].join(","))
                  return next
                })
              }
            >
              {l.next}
            </Button>
          </div>
          {selectedID && detail?.identity !== detailIdentity && (
            <p role="status">{l.loading}</p>
          )}
          {detail?.identity === detailIdentity && detail.error && (
            <Alert>{detail.error}</Alert>
          )}
          {selected && (
            <section className="min-w-0 space-y-4 border-t pt-5">
              <h2 className="text-lg font-semibold break-words">
                {selected.document.title} / {selected.document.review_type}
              </h2>
              {selected.document.description && (
                <p className="break-words whitespace-pre-wrap">
                  {selected.document.description}
                </p>
              )}
              <dl className="grid gap-3 text-sm sm:grid-cols-2">
                {[
                  [l.state, selected.state],
                  [l.reviewNode, selected.document.review_node ?? "-"],
                  [
                    l.typeVersion,
                    selected.document.review_type_snapshot?.version ?? "-",
                  ],
                  [l.initiator, selected.initiator_id],
                  [
                    l.targetVersion,
                    selected.document.work_item_version ??
                      selected.document.requirement_version,
                  ],
                  [
                    l.count,
                    `${selected.document.opinions.filter((op) => op.actor_kind !== "AGENT").length} / ${selected.document.minimum}`,
                  ],
                  [l.due, selected.document.due_at ?? "-"],
                ].map(([name, value]) => (
                  <div key={name}>
                    <dt className="text-muted-foreground">{name}</dt>
                    <dd className="break-all">{value}</dd>
                  </div>
                ))}
              </dl>
              {!selected.work_item_id && (
                <RequirementSummary
                  key={`${selected.id}:${selected.version}:${revision}`}
                  entity={entity}
                  requirement={selected.requirement_id}
                  frozenVersion={selected.document.requirement_version}
                />
              )}
              <h3 className="font-medium">{l.criteria}</h3>
              {selected.document.criteria.map((c) => (
                <article key={c.id} className="space-y-2 border-l-2 pl-3">
                  <p className="font-medium break-words">
                    {c.code} / {c.text}
                  </p>
                  {selected.document.opinions
                    .filter((op) => op.criteria?.[c.id])
                    .map((op) => (
                      <p
                        key={op.reviewer_id}
                        className="text-sm break-words whitespace-pre-wrap"
                      >
                        {op.reviewer_id} / {op.actor_kind} / {op.decision}:{" "}
                        {op.criteria?.[c.id]}
                      </p>
                    ))}
                </article>
              ))}
              <h3 className="font-medium">{l.opinions}</h3>
              {!selected.document.opinions.filter(
                (op) => op.actor_kind !== "AGENT"
              ).length ? (
                <p className="text-sm text-muted-foreground">{l.noOpinions}</p>
              ) : (
                selected.document.opinions.map((op) => (
                  <div
                    key={op.reviewer_id}
                    className="space-y-1 border-b pb-2 text-sm"
                  >
                    <p>
                      {op.reviewer_id} / {op.actor_kind} / {op.decision} /{" "}
                      {op.updated_at}
                    </p>
                    <p className="break-words whitespace-pre-wrap">
                      {op.comment}
                    </p>
                  </div>
                ))
              )}
              <OpinionHistory
                key={`${selected.id}:${selected.version}`}
                entity={entity}
                sheet={selected.id}
              />
              {selected.state === "OPEN" ? (
                <>
                  {allowed("review_participate") &&
                    selected.document.reviewers.includes(
                      session?.subject ?? ""
                    ) && (
                      <div className="space-y-3">
                        <h3 className="font-medium">{l.submit}</h3>
                        <p className="text-sm text-muted-foreground">
                          {l.perCriterion}
                        </p>
                        <AutoForm
                          key={`${selected.id}:${selected.version}`}
                          fields={opinionFields}
                          onSubmit={async (v) => {
                            await action(reviewOpinionBody(selected, v))
                          }}
                          onError={(e) =>
                            setError(e instanceof Error ? e.message : l.error)
                          }
                        >
                          <Button type="submit" disabled={busy}>
                            {l.submit}
                          </Button>
                        </AutoForm>
                      </div>
                    )}
                  {allowed("review_initiate") &&
                    selected.initiator_id === session?.subject && (
                      <div className="space-y-3">
                        <p className="text-sm text-muted-foreground">
                          {l.pending}
                        </p>
                        <Button
                          disabled={
                            busy ||
                            selected.document.opinions.filter(
                              (op) => op.actor_kind !== "AGENT"
                            ).length < selected.document.minimum
                          }
                          onClick={() => {
                            void action({
                              version: selected.version,
                              action: "CLOSE",
                            })?.catch(() => undefined)
                          }}
                        >
                          {l.close}
                        </Button>
                        <AutoForm
                          fields={{
                            reason: {
                              name: l.reason,
                              type: "textarea",
                              required: true,
                              config: { maxLength: 2000 },
                            },
                          }}
                          onSubmit={async (v) => {
                            await action({
                              version: selected.version,
                              action: "CANCEL",
                              reason: v.reason,
                            })
                          }}
                          onError={() => undefined}
                        >
                          <Button
                            variant="destructive"
                            type="submit"
                            disabled={busy}
                          >
                            {l.cancel}
                          </Button>
                        </AutoForm>
                      </div>
                    )}
                </>
              ) : (
                <p role="status">
                  {l.readOnly} {selected.document.reason}
                  {selected.document.decision && (
                    <span className="block break-all">
                      {selected.document.decision.action} ?{" "}
                      {selected.document.decision.authority_source} ?{" "}
                      {l.frozenSuite}:{" "}
                      {selected.document.decision.suite_hash ?? "-"}
                    </span>
                  )}
                </p>
              )}
              {!!selected.document.rework_ids.length && (
                <div className="space-y-2">
                  <h3 className="font-medium">{l.rework}</h3>
                  {selected.document.rework_ids.map((id) => (
                    <Link
                      key={id}
                      className="mr-4 inline-block py-2 underline"
                      to={`/admin/${tenant}/entities/${entity}/work-items/${id}`}
                    >
                      {id}
                    </Link>
                  ))}
                </div>
              )}
            </section>
          )}
          <ReviewTypes
            tenant={tenant}
            project={entity}
            manage={allowed("workflow_manage")}
            onTypes={setReviewTypes}
          />
        </>
      )}
    </main>
  )
}

type RequirementReviewFactsValue = Pick<
  RequirementDetail,
  "status" | "version" | "review"
> & { review_state?: "NEEDS_WORK" | "REVIEW_PENDING" | "APPROVED" }
export function RequirementReviewFacts({
  requirement,
  frozenVersion,
}: {
  requirement: RequirementReviewFactsValue
  frozenVersion: number
}) {
  const locale = useLocale(),
    l =
      reviewSheetMessages[locale as keyof typeof reviewSheetMessages] ??
      reviewSheetMessages["en-US"]
  return (
    <section
      className="min-w-0 space-y-3 border-y py-4"
      aria-label={l.currentRequirement}
    >
      <h3 className="font-medium">{l.currentRequirement}</h3>
      <dl className="grid gap-3 text-sm sm:grid-cols-2">
        {[
          [l.businessStatus, requirement.status],
          [
            l.lifecycle,
            requirement.review_state === "APPROVED"
              ? l.approved
              : requirement.review_state === "REVIEW_PENDING"
                ? l.reviewPending
                : l.needsWork,
          ],
          [l.currentVersion, requirement.version],
          [l.currentVerdict, requirement.review?.state ?? l.noCurrentReview],
          [l.currentReviewSheet, requirement.review?.sheet_id ?? "-"],
        ].map(([label, value]) => (
          <div key={label}>
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="break-words">{value}</dd>
          </div>
        ))}
      </dl>
      <p className="text-sm text-muted-foreground">{l.verdictStatusPolicy}</p>
      {requirement.version !== frozenVersion &&
        requirement.review?.state !== "PASSED" &&
        requirement.review?.state !== "REJECTED" && (
          <p role="status" className="text-sm">
            {l.outdatedSnapshot}
          </p>
        )}
    </section>
  )
}

function RequirementSummary({
  entity,
  requirement,
  frozenVersion,
}: {
  entity: string
  requirement: string
  frozenVersion: number
}) {
  const locale = useLocale(),
    l =
      reviewSheetMessages[locale as keyof typeof reviewSheetMessages] ??
      reviewSheetMessages["en-US"]
  const [value, setValue] = useState<RequirementDetail | null>(null),
    [error, setError] = useState("")
  useEffect(() => {
    const controller = new AbortController()
    void iamApi
      .requirement(entity, requirement, controller.signal)
      .then((detail) => {
        if (!controller.signal.aborted) setValue(detail)
      })
      .catch((cause) => {
        if (!controller.signal.aborted)
          setError(cause instanceof Error ? cause.message : l.error)
      })
    return () => controller.abort()
  }, [entity, requirement, l.error])
  if (error) return <Alert>{error}</Alert>
  if (!value) return <p role="status">{l.loading}</p>
  return (
    <RequirementReviewFacts requirement={value} frozenVersion={frozenVersion} />
  )
}

function OpinionHistory({ entity, sheet }: { entity: string; sheet: string }) {
  const locale = useLocale(),
    l =
      reviewSheetMessages[locale as keyof typeof reviewSheetMessages] ??
      reviewSheetMessages["en-US"]
  const [cursors, setCursors] = useState([""]),
    cursor = cursors[cursors.length - 1]!,
    [revision, setRevision] = useState(0)
  const [page, setPage] = useState<ReviewPage<OpinionRecord> | null>(null),
    [error, setError] = useState("")
  const [loaded, setLoaded] = useState("")
  const identity = `${entity}:${sheet}:${cursor}:${revision}`,
    loading = loaded !== identity
  useEffect(() => {
    const controller = new AbortController()
    void api
      .history(entity, sheet, cursor, controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) {
          setPage(value)
          setError("")
          setLoaded(identity)
        }
      })
      .catch((e) => {
        if (!controller.signal.aborted) {
          setPage(null)
          setError(e instanceof Error ? e.message : l.error)
          setLoaded(identity)
        }
      })
    return () => controller.abort()
  }, [entity, sheet, cursor, revision, identity, l.error])
  return (
    <section className="min-w-0 space-y-3">
      <h3 className="font-medium">{l.history}</h3>
      {error && <Alert>{error}</Alert>}
      <Button
        variant="outline"
        disabled={loading}
        onClick={() => setRevision((v) => v + 1)}
      >
        {l.refresh}
      </Button>
      {loading ? (
        <p role="status">{l.loading}</p>
      ) : (
        <AutoTable
          data={
            page?.items.map((v) => ({
              id: v.id,
              reviewer: v.opinion.reviewer_id,
              actor: v.opinion.actor_kind,
              decision: v.opinion.decision,
              comment: v.opinion.comment,
              criteria: Object.entries(v.opinion.criteria ?? {})
                .map(([id, text]) => `${id}: ${text}`)
                .join("; "),
              supersedes: v.supersedes_id ?? "-",
              date: v.opinion.updated_at,
            })) ?? []
          }
          rowKey="id"
          pagination={false}
          exportable={false}
          columns={[
            { field: "id", name: "ID" },
            { field: "reviewer", name: l.reviewers },
            { field: "actor", name: l.actorKind },
            { field: "decision", name: l.decision },
            { field: "comment", name: l.comment },
            { field: "criteria", name: l.criteria },
            { field: "supersedes", name: l.supersedes },
            { field: "date", name: l.submittedAt },
          ]}
        />
      )}
      {!loading && !page?.items.length && <p>{l.noOpinions}</p>}
      <div className="flex justify-end gap-2">
        <Button
          variant="outline"
          disabled={loading || cursors.length === 1}
          onClick={() => setCursors((v) => v.slice(0, -1))}
        >
          {l.previous}
        </Button>
        <Button
          variant="outline"
          disabled={loading || !page?.has_more || !page.next_cursor}
          onClick={() => setCursors((v) => [...v, page!.next_cursor!])}
        >
          {l.next}
        </Button>
      </div>
    </section>
  )
}
