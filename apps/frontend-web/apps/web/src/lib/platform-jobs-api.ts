import { iamApi } from "./iam-api"
import type { components } from "./api-schema"

export type PlatformJob = components["schemas"]["AdminJobView"]
export type JobSummary = components["schemas"]["AdminJobSummary"]
export type PlatformSchedule = components["schemas"]["AdminScheduleView"]
export type CorrelatedOperation = components["schemas"]["OperationSummary"]
export function jobFailures(
  history: unknown
): { attempt: number; at: string; code: string; message: string }[] {
  if (!Array.isArray(history)) return []
  return history
    .filter(
      (
        entry
      ): entry is {
        attempt: number
        at: string
        code: string
        message: string
      } =>
        !!entry &&
        typeof entry === "object" &&
        Number.isInteger(entry.attempt) &&
        typeof entry.at === "string" &&
        typeof entry.code === "string" &&
        typeof entry.message === "string"
    )
    .slice(-5)
    .reverse()
}
export type JobFilters = {
  state?: string
  queue?: string
  kind?: string
  tenant_id?: string
  created_at?: string
  priority?: string
}
function resourceID(id: string | null): string {
  if (!id || !/^[1-9][0-9]*$/.test(id))
    throw new Error("Invalid resource identifier")
  return encodeURIComponent(id)
}
export const platformJobsApi = {
  operations: (correlation: string, offset = 0) => {
    if (!/^[A-Za-z0-9:.-]{1,64}$/.test(correlation)) throw new Error("Invalid correlation identifier")
    return iamApi.deliveryResultRequest<CorrelatedOperation[]>({
      method: "GET",
      url: `/system/operation-logs?${new URLSearchParams({ correlation_id: correlation, offset: String(offset), limit: "50" })}`,
    })
  },
  list: (filters: JobFilters, offset: number) =>
    iamApi.deliveryResultRequest<components["schemas"]["AdminJobPage"]>({
      method: "GET",
      url: `/platform/jobs?${new URLSearchParams({ ...Object.fromEntries(Object.entries(filters).filter(([, value]) => value)), offset: String(offset), limit: "50" })}`,
    }),
  get: (id: string | null) =>
    iamApi.deliveryResultRequest<PlatformJob>({
      method: "GET",
      url: `/platform/jobs/${resourceID(id)}`,
    }),
  retry: (job: PlatformJob, reason: string) =>
    iamApi.deliveryResultRequest<PlatformJob>(
      { method: "POST", url: `/platform/jobs/${resourceID(job.id)}/attempts` },
      {
        headers: { "If-Match": String(job.version) },
        body: JSON.stringify({ reason }),
      }
    ),
  cancel: (job: PlatformJob, reason: string) =>
    iamApi.deliveryResultRequest<PlatformJob>(
      { method: "PATCH", url: `/platform/jobs/${resourceID(job.id)}` },
      {
        headers: { "If-Match": String(job.version) },
        body: JSON.stringify({ state: "CANCELLED", reason }),
      }
    ),
  retryBatch: (ids: string[], reason: string) => {
    if (ids.length < 1 || ids.length > 100) throw new Error("Select 1–100 jobs")
    ids.forEach(resourceID)
    return iamApi.deliveryResultRequest<
      components["schemas"]["AdminBatchResult"]
    >(
      { method: "POST", url: "/platform/job-attempts" },
      { body: JSON.stringify({ job_ids: ids, reason }) }
    )
  },
  schedules: () =>
    iamApi.deliveryResultRequest<PlatformSchedule[]>({
      method: "GET",
      url: "/platform/job-schedules",
    }),
  enable: (schedule: PlatformSchedule, enabled: boolean) =>
    iamApi.deliveryResultRequest<PlatformSchedule>(
      {
        method: "PATCH",
        url: `/platform/job-schedules/${resourceID(schedule.id)}`,
      },
      {
        headers: { "If-Match": String(schedule.version) },
        body: JSON.stringify({ enabled }),
      }
    ),
  run: (schedule: PlatformSchedule, key: string) =>
    iamApi.deliveryResultRequest<{ job_id: string }>(
      {
        method: "POST",
        url: `/platform/job-schedules/${resourceID(schedule.id)}/runs`,
      },
      { headers: { "Idempotency-Key": key } }
    ),
}

export function jobCanRetry(job: PlatformJob): boolean {
  return job.state === "DEAD" && job.has_handler
}
export function jobCanCancel(job: PlatformJob): boolean {
  return job.state === "PENDING" || job.state === "DEAD"
}
