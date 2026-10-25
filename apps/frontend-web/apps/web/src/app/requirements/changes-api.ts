import { iamApi, type RequirementDetail } from "../../lib/iam-api"

export type ScopeValues = {
  title: string
  description: string
  priority: number
  summary: string
  action: string
  criterion_id: string
  text: string
  required: boolean
}

export function scopeBody(
  detail: Pick<RequirementDetail, "current_version_no">,
  values: ScopeValues
) {
  const criteria =
    values.action === "ADD"
      ? { add: [{ text: values.text, required: values.required }] }
      : values.action === "RETIRE"
        ? { retire: [values.criterion_id] }
        : values.action === "SUPERSEDE"
          ? {
              supersede: [
                {
                  criterion_id: values.criterion_id,
                  text: values.text,
                  required: values.required,
                },
              ],
            }
          : {}
  return {
    base_version_no: detail.current_version_no,
    title: values.title,
    description: values.description,
    priority: Number(values.priority),
    summary: values.summary,
    criteria,
  }
}

export function submitScopeChange(
  path: string,
  version: number,
  key: string,
  body: object
) {
  return iamApi.deliveryResultRequest(
    { method: "POST", url: path },
    {
      headers: { "If-Match": `"${version}"`, "Idempotency-Key": key },
      body: JSON.stringify(body),
    }
  )
}
