import { iamApi } from "../../lib/iam-api"
export type ReviewPage<T> = {
  items: T[]
  has_more: boolean
  next_cursor?: string
}
export type ReviewReceipt = Pick<Sheet, "id" | "version" | "state">
export type OpinionRecord = {
  id: string
  supersedes_id?: string
  opinion: Opinion
}
export type ReviewTypeSettings = {
  name: string
  applies_to: { requirement: boolean; work_item_types: string[] }
  review_nodes: string[]
  default_reviewer_ids: string[]
  min_reviewers: number
  content_fields: string[]
  per_ac_required: boolean
  propagate: boolean
}
export type ReviewType = {
  id: string
  version: number
  settings: ReviewTypeSettings
}
export type Opinion = {
  actor_kind?: "HUMAN" | "AGENT"
  reviewer_id: string
  decision: "PASS" | "REJECT" | "SUGGEST"
  comment: string
  criteria: Record<string, string>
  updated_at: string
}
export type Sheet = {
  id: string
  requirement_id: string
  work_item_id?: string
  initiator_id: string
  state: string
  version: number
  document: {
    decision?: {
      action: "APPROVE" | "REQUEST_CHANGES"
      requirement_version: number
      version_no: number
      suite_hash?: string
      authority_source: string
      decided_at: string
    }
    target_kind?: "REQUIREMENT" | "WORK_ITEM"
    work_item_id?: string
    work_item_version?: number
    description?: string
    review_node?: string
    review_type_snapshot?: ReviewType
    title: string
    review_type: string
    requirement_version: number
    baseline_version: number
    reviewers: string[]
    minimum: number
    due_at?: string
    criteria: { id: string; code: string; text: string }[]
    opinions: Opinion[]
    rework_ids: string[]
    reason?: string
  }
}
export function reviewCreateBody(values: Record<string, unknown>) {
  const reviewers = String(values.reviewers ?? "")
    .split(/[,\uFF0C\s]+/)
    .filter(Boolean)
  const minimum = Number(values.minimum),
    version = Number(values.target_version ?? values.requirement_version),
    requirement = String(values.target_id ?? values.requirement_id ?? ""),
    workItem = values.target_kind === "WORK_ITEM"
  if (
    !/^[1-9][0-9]{0,18}$/.test(requirement) ||
    !reviewers.length ||
    reviewers.length > 30 ||
    reviewers.some((id) => !/^[1-9][0-9]{0,18}$/.test(id)) ||
    new Set(reviewers).size !== reviewers.length ||
    !Number.isSafeInteger(minimum) ||
    minimum < 1 ||
    minimum > reviewers.length ||
    !Number.isSafeInteger(version) ||
    version < 1
  )
    throw new Error("Invalid review participants or version")
  return {
    ...(workItem
      ? { work_item_id: requirement, work_item_version: version }
      : { requirement_id: requirement, requirement_version: version }),
    reviewers,
    minimum,
    review_type: String(values.review_type ?? ""),
    ...(values.due_at
      ? { due_at: new Date(String(values.due_at)).toISOString() }
      : {}),
  }
}
export function reviewOpinionBody(
  sheet: Sheet,
  values: Record<string, unknown>
) {
  const decision = String(values.decision),
    criteria: Record<string, string> = {}
  for (const criterion of sheet.document.criteria) {
    const value = String(values[`criterion_${criterion.id}`] ?? "").trim()
    if (value) criteria[criterion.id] = value
  }
  if (
    !["PASS", "REJECT", "SUGGEST"].includes(decision) ||
    (decision === "REJECT" &&
      !Object.keys(criteria).length &&
      sheet.document.review_type_snapshot?.settings.per_ac_required !== false)
  )
    throw new Error("Rejected criteria require feedback")
  return {
    version: sheet.version,
    action: "OPINION",
    opinion: { decision, comment: String(values.comment ?? ""), criteria },
  }
}
export const reviewSheetApi = (
  api: Pick<
    typeof iamApi,
    "deliveryResultRequest" | "deliveryResultPage"
  > = iamApi
) => ({
  get: (entity: string, sheet: string, signal?: AbortSignal) =>
    api.deliveryResultRequest<Sheet>(
      {
        method: "GET",
        url: `/entities/${encodeURIComponent(entity)}/review-sheets/${encodeURIComponent(sheet)}`,
      },
      { signal }
    ),
  types: (
    tenant: string,
    cursor: string,
    signal?: AbortSignal,
    project?: string
  ) =>
    api.deliveryResultRequest<ReviewPage<ReviewType>>(
      {
        method: "GET",
        url: `/tenants/${encodeURIComponent(tenant)}/review-types?cursor=${encodeURIComponent(cursor)}&limit=50${project ? `&project_id=${encodeURIComponent(project)}` : ""}`,
      },
      { signal }
    ),
  saveType: (
    tenant: string,
    current: ReviewType | null,
    settings: ReviewTypeSettings,
    key: string,
    project?: string
  ) =>
    api.deliveryResultRequest<ReviewType>(
      {
        method: current ? "PUT" : "POST",
        url: `/tenants/${encodeURIComponent(tenant)}/review-types${current ? `/${encodeURIComponent(current.id)}` : ""}${project && current ? `?project_id=${encodeURIComponent(project)}` : ""}`,
      },
      {
        headers: { "Idempotency-Key": key },
        body: JSON.stringify(
          current ? { version: current.version, settings } : settings
        ),
      }
    ),
  history: (
    entity: string,
    sheet: string,
    cursor: string,
    signal?: AbortSignal
  ) =>
    api.deliveryResultRequest<ReviewPage<OpinionRecord>>(
      {
        method: "GET",
        url: `/entities/${encodeURIComponent(entity)}/review-sheets/${encodeURIComponent(sheet)}/opinions?cursor=${encodeURIComponent(cursor)}&limit=50`,
      },
      { signal }
    ),
  list: (entity: string, cursor: string, signal?: AbortSignal) =>
    api.deliveryResultRequest<ReviewPage<Sheet>>(
      {
        method: "GET",
        url: `/entities/${encodeURIComponent(entity)}/review-sheets?cursor=${encodeURIComponent(cursor)}&limit=50`,
      },
      { signal }
    ),
  create: (
    entity: string,
    body: ReturnType<typeof reviewCreateBody>,
    key: string
  ) =>
    api.deliveryResultRequest<ReviewReceipt>(
      {
        method: "POST",
        url: `/entities/${encodeURIComponent(entity)}/review-sheets`,
      },
      { headers: { "Idempotency-Key": key }, body: JSON.stringify(body) }
    ),
  action: (
    entity: string,
    id: string,
    body: { version: number; [key: string]: unknown },
    key: string
  ) =>
    api.deliveryResultRequest<ReviewReceipt>(
      {
        method: "POST",
        url: `/entities/${encodeURIComponent(entity)}/review-sheets/${encodeURIComponent(id)}/actions`,
      },
      {
        headers: { "Idempotency-Key": key, "If-Match": `"${body.version}"` },
        body: JSON.stringify(body),
      }
    ),
})
