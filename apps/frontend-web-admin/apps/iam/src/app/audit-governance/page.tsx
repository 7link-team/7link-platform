import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react"
import { useSearchParams } from "react-router"
import {
  CheckCircle2,
  FileClock,
  Fingerprint,
  LoaderCircle,
  Pencil,
  RefreshCw,
  ShieldCheck,
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
import { Tabs, TabsList, TabsTrigger } from "@workspace/ui/components/tabs"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import {
  createIamApi,
  getTenant,
  type AuditAnchor,
  type AuditArchiveStatus,
  type AuditGovernance,
  type AuditScope,
} from "../../lib/iam-api"
import { ArchiveJobs } from "./archives"

export default function AuditGovernancePage() {
  const [tenant, setTenantValue] = useState(getTenant)
  const [params, setParams] = useSearchParams()
  const scope: AuditScope =
    params.get("scope") === "platform" ? "platform" : "tenant"
  useEffect(() => {
    const sync = () => setTenantValue(getTenant())
    window.addEventListener("tenant-change", sync)
    return () => window.removeEventListener("tenant-change", sync)
  }, [])
  return (
    <>
      <Tabs
        value={scope}
        onValueChange={(value) =>
          setParams((previous) => {
            const next = new URLSearchParams(previous)
            next.set("scope", value)
            return next
          })
        }
      >
        <TabsList variant="line" className="mb-4" aria-label="审计范围">
          <TabsTrigger variant="line" value="tenant">
            当前租户
          </TabsTrigger>
          <TabsTrigger variant="line" value="platform">
            平台
          </TabsTrigger>
        </TabsList>
      </Tabs>
      <ScopedAuditGovernance
        key={`${tenant}:${scope}`}
        tenant={tenant}
        scope={scope}
      />
    </>
  )
}

function ScopedAuditGovernance({
  tenant,
  scope,
}: {
  tenant: string
  scope: AuditScope
}) {
  const iamApi = useMemo(() => createIamApi(undefined, () => tenant), [tenant])
  const active = useRef(true)
  const refreshID = useRef(0)
  const [governance, setGovernance] = useState<AuditGovernance | null>(null)
  const [archiveStatus, setArchiveStatus] = useState<AuditArchiveStatus | null>(
    null
  )
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [dialogOpen, setDialogOpen] = useState(false)
  const [minDays, setMinDays] = useState("365")
  const [archiveDays, setArchiveDays] = useState("730")

  const load = useCallback(
    () =>
      Promise.all([
        scope === "tenant" ? iamApi.auditGovernance() : Promise.resolve(null),
        iamApi.auditArchiveStatus(scope),
      ]),
    [iamApi, scope]
  )

  const refresh = useCallback(
    async (showLoading = true) => {
      if (!active.current) return
      const requestID = ++refreshID.current
      if (showLoading) setLoading(true)
      setError("")
      try {
        const [report, status] = await load()
        if (!active.current || requestID !== refreshID.current) return
        setGovernance(report)
        setArchiveStatus(status)
      } catch (cause) {
        if (!active.current || requestID !== refreshID.current) return
        setGovernance(null)
        setArchiveStatus(null)
        setError((cause as Error).message)
      } finally {
        if (active.current && requestID === refreshID.current) setLoading(false)
      }
    },
    [load]
  )

  useEffect(() => {
    active.current = true
    let current = true
    void load().then(
      ([report, status]) => {
        if (!current) return
        setGovernance(report)
        setArchiveStatus(status)
        setMinDays(String(status.policy.min_days))
        setArchiveDays(String(status.policy.archive_after_days))
        setError("")
        setLoading(false)
      },
      (cause: Error) => {
        if (!current) return
        setError(cause.message)
        setLoading(false)
      }
    )
    return () => {
      current = false
      active.current = false
    }
  }, [load])

  const hasPendingJobs =
    !!archiveStatus &&
    archiveStatus.queued_jobs +
      archiveStatus.running_jobs +
      archiveStatus.failed_jobs >
      0
  useEffect(() => {
    if (!hasPendingJobs || dialogOpen) return
    const timer = window.setInterval(() => {
      if (!document.hidden) void refresh(false)
    }, 5000)
    return () => window.clearInterval(timer)
  }, [hasPendingJobs, dialogOpen, refresh])

  const saveRetention = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    try {
      await iamApi.setAuditRetention(
        {
          min_days: Number(minDays),
          archive_after_days: Number(archiveDays),
        },
        scope
      )
      if (!active.current) return
      setDialogOpen(false)
      await refresh()
    } catch (cause) {
      if (active.current) setError((cause as Error).message)
    } finally {
      if (active.current) setBusy(false)
    }
  }

  const signRoot = async () => {
    setBusy(true)
    setError("")
    try {
      await iamApi.auditSignRoot()
      await refresh()
    } catch (cause) {
      if (active.current) setError((cause as Error).message)
    } finally {
      if (active.current) setBusy(false)
    }
  }

  const policy = archiveStatus?.policy
  const integrity = governance?.integrity
  const anchor = integrity?.anchor

  return (
    <>
      <PageHeader
        title={scope === "platform" ? "平台审计治理" : "审计治理"}
        action={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => void refresh()}
              disabled={loading}
            >
              <RefreshCw className={loading ? "animate-spin" : ""} />
              刷新
            </Button>
            <Button
              type="button"
              size="sm"
              disabled={!archiveStatus || busy}
              onClick={() => {
                setMinDays(String(policy?.min_days ?? 365))
                setArchiveDays(String(policy?.archive_after_days ?? 730))
                setDialogOpen(true)
              }}
            >
              <Pencil />
              更新保留策略
            </Button>
            {scope === "tenant" && (
              <Button
                type="button"
                size="sm"
                variant="secondary"
                onClick={() => void signRoot()}
                disabled={busy || integrity?.valid !== true}
              >
                {busy ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <Fingerprint />
                )}
                签名当前根
              </Button>
            )}
          </div>
        }
      />
      {error && <Alert>{error}</Alert>}
      {loading && !archiveStatus ? (
        <div className="flex items-center gap-2 text-muted-foreground">
          <LoaderCircle className="animate-spin" />
          加载中
        </div>
      ) : (
        archiveStatus && (
          <div className="grid gap-4">
            <section className="grid gap-4 lg:grid-cols-3">
              <PolicyCard
                minDays={policy?.min_days}
                archiveAfterDays={policy?.archive_after_days}
                updatedAt={policy?.updated_at}
              />
              {scope === "tenant" && (
                <IntegrityCard
                  valid={integrity?.valid}
                  verified={integrity?.verified_events}
                  headSequence={integrity?.head_sequence}
                  anchor={anchor}
                />
              )}
              {scope === "tenant" && (
                <StatsCard
                  total={governance?.total_events}
                  archiveReady={governance?.archive_ready_events}
                  anchored={governance?.anchored_events}
                />
              )}
            </section>
            <ArchiveJobs
              api={iamApi}
              scope={scope}
              status={archiveStatus}
              onChanged={refresh}
            />
            {scope === "tenant" && (
              <AnchorsTable anchors={governance?.anchors ?? []} />
            )}
          </div>
        )
      )}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>配置保留策略</DialogTitle>
            <DialogDescription>
              设置事件的最小保留期与归档就绪期，归档不会删除事件。
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={(event) => void saveRetention(event)}>
            <div className="grid gap-4">
              <div className="grid gap-2">
                <Label htmlFor="min-days">最短保留天数</Label>
                <Input
                  id="min-days"
                  type="number"
                  min={1}
                  max={36500}
                  required
                  value={minDays}
                  onChange={(event) => setMinDays(event.target.value)}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="archive-days">归档就绪天数</Label>
                <Input
                  id="archive-days"
                  type="number"
                  min={Number(minDays) || 1}
                  max={36500}
                  required
                  value={archiveDays}
                  onChange={(event) => setArchiveDays(event.target.value)}
                />
              </div>
            </div>
            <DialogFooter className="mt-6">
              <Button
                type="button"
                variant="outline"
                onClick={() => setDialogOpen(false)}
              >
                取消
              </Button>
              <Button type="submit" disabled={busy}>
                {busy && <LoaderCircle className="animate-spin" />}
                保存
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}

function PolicyCard({
  minDays,
  archiveAfterDays,
  updatedAt,
}: {
  minDays?: number
  archiveAfterDays?: number
  updatedAt?: string
}) {
  return (
    <Card icon={<ShieldCheck />} title="保留策略">
      <Stat label="最短保留天数" value={minDays ?? "-"} />
      <Stat label="归档就绪天数" value={archiveAfterDays ?? "-"} />
      <Stat
        label="更新时间"
        value={
          updatedAt && new Date(updatedAt).getUTCFullYear() > 1
            ? new Date(updatedAt).toLocaleString()
            : "平台默认"
        }
      />
    </Card>
  )
}

function IntegrityCard({
  valid,
  verified,
  headSequence,
  anchor,
}: {
  valid?: boolean
  verified?: number
  headSequence?: number
  anchor?: AuditGovernance["integrity"]["anchor"]
}) {
  return (
    <Card icon={<CheckCircle2 />} title="链完整性">
      <Stat
        label="链完整性"
        value={
          <Badge variant={valid ? "default" : "destructive"}>
            {valid ? "有效" : "无效"}
          </Badge>
        }
      />
      <Stat label="已校验事件" value={verified ?? 0} />
      <Stat label="链头序号" value={headSequence ?? 0} />
      <Stat
        label="锚定状态"
        value={
          <Badge variant={anchor?.enabled ? "default" : "outline"}>
            {anchor?.enabled ? "已启用" : "未启用"}
          </Badge>
        }
      />
      <Stat
        label="签名状态"
        value={
          <Badge
            variant={
              anchor?.signed
                ? anchor.signature_valid
                  ? "default"
                  : "destructive"
                : "outline"
            }
          >
            {anchor?.signed
              ? anchor.signature_valid
                ? "已签名"
                : "签名无效"
              : "未签名"}
          </Badge>
        }
      />
    </Card>
  )
}

function StatsCard({
  total,
  archiveReady,
  anchored,
}: {
  total?: number
  archiveReady?: number
  anchored?: number
}) {
  return (
    <Card icon={<FileClock />} title="统计数据">
      <Stat label="事件总数" value={total ?? 0} />
      <Stat label="已达归档期限" value={archiveReady ?? 0} />
      <Stat label="已锚定事件" value={anchored ?? 0} />
    </Card>
  )
}

function AnchorsTable({ anchors }: { anchors: AuditAnchor[] }) {
  return (
    <section className="rounded-md border">
      <div className="flex items-center gap-2 border-b px-4 py-3">
        <FileClock className="size-4 text-muted-foreground" />
        <h2 className="text-sm font-medium">根锚点链</h2>
      </div>
      {anchors.length === 0 ? (
        <p className="px-4 py-6 text-sm text-muted-foreground">无锚点记录</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>序号</TableHead>
              <TableHead>链头哈希</TableHead>
              <TableHead>锚定时间</TableHead>
              <TableHead>锚点哈希</TableHead>
              <TableHead>签名状态</TableHead>
              <TableHead>签名键 ID</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {anchors.map((item) => (
              <TableRow key={item.head_sequence}>
                <TableCell className="font-mono text-xs">
                  {item.head_sequence}
                </TableCell>
                <TableCell className="max-w-52 truncate font-mono text-xs">
                  {item.head_hash}
                </TableCell>
                <TableCell className="text-xs">
                  {new Date(item.anchored_at).toLocaleString()}
                </TableCell>
                <TableCell className="max-w-52 truncate font-mono text-xs">
                  {item.anchor_hash}
                </TableCell>
                <TableCell>
                  <Badge variant={item.root_signature ? "default" : "outline"}>
                    {item.root_signature ? "已签名" : "未签名"}
                  </Badge>
                </TableCell>
                <TableCell className="font-mono text-xs">
                  {item.signing_key_id ?? "-"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  )
}

function Card({
  icon,
  title,
  children,
}: {
  icon: React.ReactNode
  title: string
  children: React.ReactNode
}) {
  return (
    <section className="rounded-md border p-4">
      <div className="mb-3 flex items-center gap-2">
        {icon}
        <h2 className="text-sm font-medium">{title}</h2>
      </div>
      <dl className="grid gap-3">{children}</dl>
    </section>
  )
}

function Stat({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-sm font-medium">{value}</dd>
    </div>
  )
}
