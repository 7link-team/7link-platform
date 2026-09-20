import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react"
import { useLocation, useNavigate, useParams } from "react-router"
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
import { useAllowed } from "../../lib/permissions"
import { ArchiveJobs } from "./archives"

export default function AuditGovernancePage() {
  const t = useTranslations("iam")

  const location = useLocation()
  const navigate = useNavigate()
  const { tenantID = "" } = useParams<{ tenantID?: string }>()
  const [tenant, setTenantValue] = useState(() => tenantID || getTenant())
  const scope: AuditScope = location.pathname.startsWith("/platform/")
    ? "platform"
    : "tenant"
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
          navigate(
            value === "platform"
              ? "/platform/audit-governance"
              : `/tenants/${encodeURIComponent(tenant)}/audit-governance`
          )
        }
      >
        <TabsList variant="line" className="mb-4" aria-label={t("auditScope")}>
          <TabsTrigger variant="line" value="tenant">
            {t("currentTenant")}
          </TabsTrigger>
          <TabsTrigger variant="line" value="platform">
            {t("platform")}
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
  const t = useTranslations("iam")
  const allowed = useAllowed()
  // The platform tab reaches /platform/... routes, which are guarded by
  // platform_administer instead of the tenant audit verbs.
  const scopedCode = (tenantCode: string) =>
    scope === "platform" ? "platform_administer" : tenantCode

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
        title={
          scope === "platform"
            ? t("platformAuditGovernance")
            : t("auditGovernance")
        }
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
              {t("refresh")}
            </Button>
            {allowed(scopedCode("audit_event_manage")) && (
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
                {t("updateRetentionPolicy")}
              </Button>
            )}
            {scope === "tenant" && allowed("audit_event_manage") && (
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
                {t("signCurrentRoot")}
              </Button>
            )}
          </div>
        }
      />
      {error && <Alert>{error}</Alert>}
      {loading && !archiveStatus ? (
        <div className="flex items-center gap-2 text-muted-foreground">
          <LoaderCircle className="animate-spin" />
          {t("loadingShort")}
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
            <DialogTitle>{t("configureRetention")}</DialogTitle>
            <DialogDescription>
              {t("retentionPolicyDescription")}
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={(event) => void saveRetention(event)}>
            <div className="grid gap-4">
              <div className="grid gap-2">
                <Label htmlFor="min-days">{t("minimumRetentionDays")}</Label>
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
                <Label htmlFor="archive-days">{t("archiveReadyDays")}</Label>
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
                {t("cancel")}
              </Button>
              <Button type="submit" disabled={busy}>
                {busy && <LoaderCircle className="animate-spin" />}
                {t("save")}
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
  const locale = useLocale()

  const t = useTranslations("iam")

  return (
    <Card icon={<ShieldCheck />} title={t("retentionPolicy")}>
      <Stat label={t("minimumRetentionDays")} value={minDays ?? "-"} />
      <Stat label={t("archiveReadyDays")} value={archiveAfterDays ?? "-"} />
      <Stat
        label={t("updatedAt")}
        value={
          updatedAt && new Date(updatedAt).getUTCFullYear() > 1
            ? new Date(updatedAt).toLocaleString(locale)
            : t("platformDefault")
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
  const t = useTranslations("iam")

  return (
    <Card icon={<CheckCircle2 />} title={t("chainIntegrity")}>
      <Stat
        label={t("chainIntegrity")}
        value={
          <Badge variant={valid ? "default" : "destructive"}>
            {valid ? t("valid") : t("invalid")}
          </Badge>
        }
      />
      <Stat label={t("verifiedEvents")} value={verified ?? 0} />
      <Stat label={t("chainHeadSequence")} value={headSequence ?? 0} />
      <Stat
        label={t("anchorStatus")}
        value={
          <Badge variant={anchor?.enabled ? "default" : "outline"}>
            {anchor?.enabled ? t("enabled") : t("notEnabled")}
          </Badge>
        }
      />
      <Stat
        label={t("signatureStatus")}
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
                ? t("signed")
                : t("invalidSignature")
              : t("unsigned")}
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
  const t = useTranslations("iam")

  return (
    <Card icon={<FileClock />} title={t("statistics")}>
      <Stat label={t("totalEvents")} value={total ?? 0} />
      <Stat label={t("archiveEligible")} value={archiveReady ?? 0} />
      <Stat label={t("anchoredEvents")} value={anchored ?? 0} />
    </Card>
  )
}

function AnchorsTable({ anchors }: { anchors: AuditAnchor[] }) {
  const locale = useLocale()

  const t = useTranslations("iam")

  return (
    <section className="rounded-md border">
      <div className="flex items-center gap-2 border-b px-4 py-3">
        <FileClock className="size-4 text-muted-foreground" />
        <h2 className="text-sm font-medium">{t("rootAnchorChain")}</h2>
      </div>
      {anchors.length === 0 ? (
        <p className="px-4 py-6 text-sm text-muted-foreground">
          {t("noAnchors")}
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("sequence")}</TableHead>
              <TableHead>{t("chainHeadHash")}</TableHead>
              <TableHead>{t("anchoredAt")}</TableHead>
              <TableHead>{t("anchorHash")}</TableHead>
              <TableHead>{t("signatureStatus")}</TableHead>
              <TableHead>{t("signingKeyId")}</TableHead>
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
                  {new Date(item.anchored_at).toLocaleString(locale)}
                </TableCell>
                <TableCell className="max-w-52 truncate font-mono text-xs">
                  {item.anchor_hash}
                </TableCell>
                <TableCell>
                  <Badge variant={item.root_signature ? "default" : "outline"}>
                    {item.root_signature ? t("signed") : t("unsigned")}
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
