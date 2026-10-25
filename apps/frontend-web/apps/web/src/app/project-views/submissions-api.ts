import type { components } from "../../lib/api-schema"
import { apiRoute } from "../../lib/api-route"
import { collaborationID } from "../../lib/clientwork-api"
import { iamApi } from "../../lib/iam-api"

export type SubmissionDetail = components["schemas"]["SubmissionDetail"]
export type SubmissionPage = components["schemas"]["PageSubmissionSummary"]
export type SubmissionQuestion = components["schemas"]["SubmissionQuestion"]
export type AnswerAttachment = components["schemas"]["AnswerAttachment"]

export const createSubmissionsApi = (api: Pick<typeof iamApi, "deliveryResultRequest" | "deliveryResultDownload" | "files" | "uploadFile">) => ({
  download: (tenant: string, id: string, question: SubmissionQuestion, attachment: string) => {
    collaborationID(tenant); collaborationID(id); collaborationID(question.round_id); collaborationID(attachment)
    if (!Number.isSafeInteger(question.number) || question.number < 1 || question.number > 10) throw new Error("Invalid submission question")
    return api.deliveryResultDownload(apiRoute("get", "/tenants/{tenant_id}/intake-submissions/{id}/clarification-rounds/{round_id}/answers/{question_no}/attachments/{attachment_id}/content", { path: { tenant_id: tenant, id, round_id: question.round_id!, question_no: question.number, attachment_id: attachment } }))
  },
  // Native Files completes verification before returning this file-node ID.
  upload: async (project: string, file: File, unavailableMessage: string) => {
    collaborationID(project)
    const roots = await api.files(project)
    const root = roots.items?.find(entry => entry.kind === "directory" && entry.id)
    if (!root?.id) throw new Error(unavailableMessage)
    const uploaded = await api.uploadFile(project, root.id, file)
    collaborationID(uploaded.id)
    return uploaded.id!
  },
  list: async (tenant: string, project: string, cursor: string, signal?: AbortSignal) => {
    collaborationID(tenant); collaborationID(project)
    const page = await api.deliveryResultRequest<SubmissionPage>(apiRoute("get", "/tenants/{tenant_id}/intake-submissions", { path: { tenant_id: tenant }, query: { project_id: project, cursor: cursor || undefined, limit: 50 } }), { signal })
    for (const row of page.items ?? []) if (!row.id || row.project_id !== project) throw new Error("Invalid submission project")
    return page
  },
  get: async (tenant: string, project: string, id: string, signal?: AbortSignal) => {
    collaborationID(tenant); collaborationID(project); collaborationID(id)
    const result = await api.deliveryResultRequest<SubmissionDetail>(apiRoute("get", "/tenants/{tenant_id}/intake-submissions/{id}", { path: { tenant_id: tenant, id } }), { signal })
    if (result.id !== id || result.project_id !== project) throw new Error("Invalid submission project")
    return result
  },
  answer: (tenant: string, id: string, question: SubmissionQuestion, answer: string, key: string, signal?: AbortSignal, uploadIDs: string[] = []) => {
    collaborationID(tenant); collaborationID(id); collaborationID(question.round_id)
    if (!Number.isSafeInteger(question.round_version) || question.round_version < 1 || !Number.isSafeInteger(question.number) || question.number < 1 || question.number > 10 || !["SENT", "EXPIRED"].includes(question.state)) throw new Error("Invalid submission question")
    if (uploadIDs.length > 5 || new Set(uploadIDs).size !== uploadIDs.length) throw new Error("Invalid submission attachments")
    for (const upload of uploadIDs) collaborationID(upload)
    return api.deliveryResultRequest<components["schemas"]["Receipt"]>(apiRoute("put", "/tenants/{tenant_id}/intake-submissions/{id}/clarification-rounds/{round_id}/answers/{question_no}", { path: { tenant_id: tenant, id, round_id: question.round_id!, question_no: question.number } }), { headers: { "Idempotency-Key": key, "If-Match": `"${question.round_version}"` }, body: JSON.stringify({ answer_text: answer, ...(uploadIDs.length ? { upload_ids: uploadIDs } : {}) }), signal })
  },
})

export const submissionsApi = createSubmissionsApi(iamApi)
