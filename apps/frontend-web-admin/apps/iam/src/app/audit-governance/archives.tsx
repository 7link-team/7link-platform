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

const pageSize = 20
const stateLabels = {
  queued: "排队中",
  running: "执行中",
  failed: "失败",
  complete: "已完成",
}
const failureLabels: Record<string, string> = {
  storage_unavailable: "存储不可用",
  integrity_failed: "完整性校验失败",
  interrupted: "执行中断",
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
      setNotice(`任务 ${job.id}：${stateLabels[job.state]}`)
      setOffset(0)
    })

  const verify = (job: AuditArchiveJob) =>
    run(`verify:${job.id}`, async () => {
      const object = await api.verifyAuditArchive(job.id, scope)
      if (!active.current) return
      setVerified(object)
      setSelected(job)
      setNotice(`任务 ${job.id} 校验通过`)
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
    <section className="min-w-0 border-t pt-4" aria-label="归档任务">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Archive className="size-4" />
          <h2 className="text-sm font-medium">归档任务</h2>
          <Badge variant={status.enabled ? "default" : "outline"}>
            {status.enabled ? "已启用" : "未启用"}
          </Badge>
        </div>
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
        >
          {busy === "request" ? (
            <LoaderCircle className="animate-spin" />
          ) : (
            <Archive />
          )}
          创建归档任务
        </Button>
      </div>
      <dl className="mb-4 grid grid-cols-2 gap-4 border-y py-3 sm:grid-cols-4">
        <Metric label="已归档事件" value={status.archived_events} />
        <Metric label="待归档事件" value={status.pending_events} />
        <Metric
          label="执行中任务"
          value={status.queued_jobs + status.running_jobs}
        />
        <Metric label="失败任务" value={status.failed_jobs} />
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
          加载归档任务
        </p>
      ) : jobs.length === 0 ? (
        <p className="py-6 text-sm text-muted-foreground">暂无归档任务</p>
      ) : (
        <Table className="min-w-[880px]">
          <TableHeader>
            <TableRow>
              <TableHead>任务 / 创建时间</TableHead>
              <TableHead>事件序号</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>尝试 / 下次重试</TableHead>
              <TableHead>保留至</TableHead>
              <TableHead className="w-40 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {jobs.map((job) => (
              <TableRow key={job.id}>
                <TableCell>
                  <div className="font-mono text-xs">{job.id}</div>
                  <div className="mt-1 text-xs text-muted-foreground">
                    {date(job.created_at)}
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
                    {stateLabels[job.state]}
                  </Badge>
                  {job.error_code && (
                    <div className="mt-1 max-w-40 text-xs break-words text-destructive">
                      {failureLabels[job.error_code] ?? job.error_code}
                    </div>
                  )}
                </TableCell>
                <TableCell className="text-xs">
                  <div>{job.attempts} 次</div>
                  <div className="mt-1 text-muted-foreground">
                    {job.next_attempt > 0 ? date(job.next_attempt) : "-"}
                  </div>
                </TableCell>
                <TableCell className="text-xs">
                  {date(job.retain_until)}
                </TableCell>
                <TableCell>
                  <div className="flex justify-end gap-1">
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`归档详情 ${job.id}`}
                      title="归档详情"
                      onClick={() => {
                        setSelected(job)
                        setVerified(null)
                      }}
                    >
                      <FileSearch />
                    </Button>
                    {(job.state === "failed" || job.state === "running") && (
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        disabled={!!busy || !status.enabled}
                        aria-label={`重试归档 ${job.id}`}
                        title={
                          job.state === "running"
                            ? "重试已过期任务"
                            : "立即重试"
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
                          aria-label={`校验归档 ${job.id}`}
                          title="校验归档"
                          onClick={() => void verify(job)}
                        >
                          {busy === `verify:${job.id}` ? (
                            <LoaderCircle className="animate-spin" />
                          ) : (
                            <ShieldCheck />
                          )}
                        </Button>
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          disabled={!!busy || !status.enabled}
                          aria-label={`下载归档 ${job.id}`}
                          title="下载归档"
                          onClick={() => void download(job)}
                        >
                          {busy === `download:${job.id}` ? (
                            <LoaderCircle className="animate-spin" />
                          ) : (
                            <Download />
                          )}
                        </Button>
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
        <span className="text-muted-foreground">共 {total} 个任务</span>
        <div className="flex items-center gap-2">
          <Button
            size="icon-sm"
            variant="outline"
            title="上一页"
            aria-label="上一页归档"
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
            title="下一页"
            aria-label="下一页归档"
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
            <DialogTitle>归档详情</DialogTitle>
          </DialogHeader>
          {selected && (
            <dl className="grid min-w-0 gap-3 text-sm">
              <Detail label="任务 ID" value={selected.id} />
              <Detail label="状态" value={stateLabels[selected.state]} />
              <Detail
                label="事件数量"
                value={String(selected.head_sequence - selected.after_sequence)}
              />
              <Detail
                label="最低保留期限"
                value={date(selected.retain_until)}
              />
              {object && (
                <>
                  <Detail label="存储桶" value={object.bucket} />
                  <Detail label="对象键" value={object.key} />
                  <Detail label="不可变版本" value={object.version_id} />
                  <Detail label="SHA-256" value={object.sha256} />
                  <Detail label="字节数" value={object.size.toLocaleString()} />
                  <Detail
                    label="对象锁定至"
                    value={date(object.retain_until)}
                  />
                </>
              )}
              {verified && (
                <p role="status" className="flex items-center gap-2">
                  <ShieldCheck className="size-4" />
                  版本、内容和保留锁校验通过
                </p>
              )}
            </dl>
          )}
        </DialogContent>
      </Dialog>
    </section>
  )
}

function date(value: string | number) {
  return new Date(value).toLocaleString()
}
function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-lg font-medium tabular-nums">
        {value.toLocaleString()}
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
