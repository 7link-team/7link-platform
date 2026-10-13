import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useEffect, useRef, useState } from "react"
import {
  Archive,
  ChevronLeft,
  ChevronRight,
  Download,
  FileSearch,
  LoaderCircle,
  RotateCcw,
  ShieldCheck,
} from "lucide-react"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
import { Alert } from "../../components/alert"
import type {
  AuditArchiveJob,
  AuditArchiveObject,
  AuditArchiveStatus,
  AuditScope,
  IamApi,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

const pageSize = 20
const stateLabels = {
  queued: "queued",
  running: "running",
  failed: "failure",
  complete: "completed",
}
const failureLabels: Record<string, string> = {
  storage_unavailable: "storageUnavailable",
  integrity_failed: "integrityFailed",
  interrupted: "executionInterrupted",
}

export function ArchiveJobs({
  api,
  scope,
  status,
  onChanged,
}: {
  api: IamApi
  scope: AuditScope
  status: AuditArchiveStatus
  onChanged: () => Promise<void>
}) {
  const locale = useLocale()

  const t = useTranslations("iam")
  const allowed = useAllowed()
  // The platform scope of this screen reaches /system/... routes, which
  // are guarded by platform_administer rather than the tenant audit verbs.
  const scopedCode = (tenantCode: string) =>
    scope === "platform" ? "platform_administer" : tenantCode

  const active = useRef(true)
  const [jobs, setJobs] = useState<AuditArchiveJob[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState("")
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [selected, setSelected] = useState<AuditArchiveJob | null>(null)
  const [verified, setVerified] = useState<AuditArchiveObject | null>(null)

  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
    }
  }, [])

  useEffect(() => {
    let current = true
    void api.auditArchives(offset, pageSize, scope).then(
      (result) => {
        if (!current) return
        setJobs(result.items)
        setTotal(result.total)
        setLoading(false)
        setError("")
      },
      (cause: Error) => {
        if (!current) return
        setJobs([])
        setTotal(0)
        setLoading(false)
        setError(cause.message)
      }
    )
    return () => {
      current = false
    }
  }, [api, scope, offset, status])

  const run = async (key: string, action: () => Promise<void>) => {
    setBusy(key)
    setError("")
    setNotice("")
    try {
      await action()
      if (active.current) await onChanged()
    } catch (cause) {
      if (active.current) setError((cause as Error).message)
    } finally {
      if (active.current) setBusy("")
    }
  }

  const requestArchive = () =>
    run("request", async () => {
      const job = await api.requestAuditArchive(scope)
      if (!active.current) return
      setNotice(
        t("jobStatus", { id: job.id, state: t(stateLabels[job.state]) })
      )
      setOffset(0)
    })

  const verify = (job: AuditArchiveJob) =>
    run(`verify:${job.id}`, async () => {
      const object = await api.verifyAuditArchive(job.id, scope)
      if (!active.current) return
      setVerified(object)
      setSelected(job)
      setNotice(t("jobVerified", { id: job.id }))
    })

  const download = (job: AuditArchiveJob) =>
    run(`download:${job.id}`, async () => {
      const result = await api.downloadAuditArchive(job.id, scope)
      if (!active.current) return
      const url = URL.createObjectURL(result.blob)
      const anchor = document.createElement("a")
      anchor.href = url
      anchor.download = result.filename
      anchor.hidden = true
      document.body.append(anchor)
      anchor.click()
      anchor.remove()
      window.setTimeout(() => URL.revokeObjectURL(url), 0)
    })

  const pending = status.queued_jobs + status.running_jobs + status.failed_jobs
  const object = verified ?? selected?.object

  return (
    <section className="min-w-0 border-t pt-4" aria-label={t("archiveJobs")}>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Archive className="size-4" />
          <h2 className="text-sm font-medium">{t("archiveJobs")}</h2>
          <Badge variant={status.enabled ? "default" : "outline"}>
            {status.enabled ? t("enabled") : t("notEnabled")}
          </Badge>
        </div>
        {allowed(scopedCode("audit_event_export")) && (
          <Button
            size="sm"
            disabled={
              !status.enabled ||
              status.pending_events === 0 ||
              pending > 0 ||
              !!busy ||
              loading
            }
            onClick={() => void requestArchive()}
            title={
              !status.enabled
                ? t("archiveStorageDisabled")
                : status.pending_events === 0
                  ? t("noEligibleArchiveEvents")
                  : undefined
            }
          >
            {busy === "request" ? (
              <LoaderCircle className="animate-spin" />
            ) : (
              <Archive />
            )}
            {t("createArchiveJob")}
          </Button>
        )}
      </div>
      <dl className="mb-4 grid grid-cols-2 gap-4 border-y py-3 sm:grid-cols-4">
        <Metric label={t("archivedEvents")} value={status.archived_events} />
        <Metric
          label={t("pendingArchiveEvents")}
          value={status.pending_events}
        />
        <Metric
          label={t("runningJobs")}
          value={status.queued_jobs + status.running_jobs}
        />
        <Metric label={t("failedJobs")} value={status.failed_jobs} />
      </dl>
      {error && <Alert>{error}</Alert>}
      {notice && (
        <p role="status" className="mb-3 text-sm break-all">
          {notice}
        </p>
      )}
      {loading ? (
        <p className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
          <LoaderCircle className="size-4 animate-spin" />
          {t("loadingArchiveJobs")}
        </p>
      ) : jobs.length === 0 ? (
        <p className="py-6 text-sm text-muted-foreground">
          {!status.enabled
            ? t("archiveStorageDisabled")
            : status.eligible_events === 0
              ? t("noEligibleArchiveEvents")
              : t("noArchiveJobs")}
        </p>
      ) : (
        <Table className="min-w-[880px]">
          <TableHeader>
            <TableRow>
              <TableHead>{t("jobCreatedAt")}</TableHead>
              <TableHead>{t("eventSequence")}</TableHead>
              <TableHead>{t("status")}</TableHead>
              <TableHead>{t("attemptNextRetry")}</TableHead>
              <TableHead>{t("retainUntil")}</TableHead>
              <TableHead className="w-40 text-right">{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {jobs.map((job) => (
              <TableRow key={job.id}>
                <TableCell>
                  <div className="font-mono text-xs">{job.id}</div>
                  <div className="mt-1 text-xs text-muted-foreground">
                    {date(job.created_at, locale)}
                  </div>
                </TableCell>
                <TableCell className="tabular-nums">
                  {job.after_sequence + 1} - {job.head_sequence}
                </TableCell>
                <TableCell>
                  <Badge
                    variant={
                      job.state === "failed"
                        ? "destructive"
                        : job.state === "complete"
                          ? "default"
                          : "outline"
                    }
                  >
                    {t(stateLabels[job.state])}
                  </Badge>
                  {job.error_code && (
                    <div className="mt-1 max-w-40 text-xs break-words text-destructive">
                      {failureLabels[job.error_code]
                        ? t(failureLabels[job.error_code])
                        : job.error_code}
                    </div>
                  )}
                </TableCell>
                <TableCell className="text-xs">
                  <div>
                    {job.attempts} {t("attemptsSuffix")}
                  </div>
                  <div className="mt-1 text-muted-foreground">
                    {job.next_attempt > 0
                      ? date(job.next_attempt, locale)
                      : "-"}
                  </div>
                </TableCell>
                <TableCell className="text-xs">
                  {date(job.retain_until, locale)}
                </TableCell>
                <TableCell>
                  <div className="flex justify-end gap-1">
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={t("archiveDetailsNamed", { id: job.id })}
                      title={t("archiveDetails")}
                      onClick={() => {
                        setSelected(job)
                        setVerified(null)
                      }}
                    >
                      <FileSearch />
                    </Button>
                    {(job.state === "failed" || job.state === "running") &&
                      allowed(scopedCode("audit_event_export")) && (
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          disabled={!!busy || !status.enabled}
                          aria-label={t("retryArchiveNamed", { id: job.id })}
                          title={
                            job.state === "running"
                              ? t("retryExpiredJob")
                              : t("retryNow")
                          }
                          onClick={() =>
                            void run(`retry:${job.id}`, async () => {
                              await api.retryAuditArchive(job.id, scope)
                            })
                          }
                        >
                          {busy === `retry:${job.id}` ? (
                            <LoaderCircle className="animate-spin" />
                          ) : (
                            <RotateCcw />
                          )}
                        </Button>
                      )}
                    {job.state === "complete" && (
                      <>
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          disabled={!!busy || !status.enabled}
                          aria-label={t("verifyArchiveNamed", { id: job.id })}
                          title={t("verifyArchive")}
                          onClick={() => void verify(job)}
                        >
                          {busy === `verify:${job.id}` ? (
                            <LoaderCircle className="animate-spin" />
                          ) : (
                            <ShieldCheck />
                          )}
                        </Button>
                        {allowed(scopedCode("audit_event_export")) && (
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            disabled={!!busy || !status.enabled}
                            aria-label={t("downloadArchiveNamed", {
                              id: job.id,
                            })}
                            title={t("downloadArchive")}
                            onClick={() => void download(job)}
                          >
                            {busy === `download:${job.id}` ? (
                              <LoaderCircle className="animate-spin" />
                            ) : (
                              <Download />
                            )}
                          </Button>
                        )}
                      </>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <div className="mt-3 flex items-center justify-between gap-3 text-sm">
        <span className="text-muted-foreground">
          {t("totalPrefix")}
          {total} {t("jobsSuffix")}
        </span>
        <div className="flex items-center gap-2">
          <Button
            size="icon-sm"
            variant="outline"
            title={t("previousPage")}
            aria-label={t("previousArchives")}
            disabled={loading || offset === 0 || !!busy}
            onClick={() => {
              setLoading(true)
              setOffset(Math.max(0, offset - pageSize))
            }}
          >
            <ChevronLeft />
          </Button>
          <span className="min-w-16 text-center tabular-nums">
            {Math.floor(offset / pageSize) + 1} /{" "}
            {Math.max(1, Math.ceil(total / pageSize))}
          </span>
          <Button
            size="icon-sm"
            variant="outline"
            title={t("nextPage")}
            aria-label={t("nextArchives")}
            disabled={loading || offset + pageSize >= total || !!busy}
            onClick={() => {
              setLoading(true)
              setOffset(offset + pageSize)
            }}
          >
            <ChevronRight />
          </Button>
        </div>
      </div>
      <Dialog
        open={!!selected}
        onOpenChange={(open) => {
          if (!open) {
            setSelected(null)
            setVerified(null)
          }
        }}
      >
        <DialogContent className="max-h-[85dvh] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{t("archiveDetails")}</DialogTitle>
          </DialogHeader>
          {selected && (
            <dl className="grid min-w-0 gap-3 text-sm">
              <Detail label={t("jobId")} value={selected.id} />
              <Detail
                label={t("status")}
                value={t(stateLabels[selected.state])}
              />
              <Detail
                label={t("eventCount")}
                value={String(selected.head_sequence - selected.after_sequence)}
              />
              <Detail
                label={t("minimumRetention")}
                value={date(selected.retain_until, locale)}
              />
              {object && (
                <>
                  <Detail label={t("bucket")} value={object.bucket} />
                  <Detail label={t("objectKey")} value={object.key} />
                  <Detail
                    label={t("immutableVersion")}
                    value={object.version_id}
                  />
                  <Detail label="SHA-256" value={object.sha256} />
                  <Detail
                    label={t("byteCount")}
                    value={object.size.toLocaleString(locale)}
                  />
                  <Detail
                    label={t("objectLockedUntil")}
                    value={date(object.retain_until, locale)}
                  />
                </>
              )}
              {verified && (
                <p role="status" className="flex items-center gap-2">
                  <ShieldCheck className="size-4" />
                  {t("archiveVerified")}
                </p>
              )}
            </dl>
          )}
        </DialogContent>
      </Dialog>
    </section>
  )
}

function date(value: string | number, locale: string) {
  return new Date(value).toLocaleString(locale)
}
function Metric({ label, value }: { label: string; value: number }) {
  const locale = useLocale()

  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-lg font-medium tabular-nums">
        {value.toLocaleString(locale)}
      </dd>
    </div>
  )
}
function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 font-mono text-xs break-all">{value}</dd>
    </div>
  )
}
