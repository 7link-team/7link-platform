import { PaginationPages } from "@workspace/ui/components/pagination"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useEffect, useState } from "react"
import { useSearchParams } from "react-router"
import { CheckCircle2, Eye, FileClock, Search, ShieldAlert } from "lucide-react"
import { Badge } from "@workspace/ui/components/badge"
import { Button, buttonVariants } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Label } from "@workspace/ui/components/label"
import { AutoTable } from "@workspace/ui/components/auto-table"
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@workspace/ui/components/tooltip"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi, type AuditEvent, type AuditIntegrity } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

const pageSize = 50

interface AuditFilters {
  projectID: string
  module: string
  actorKind: string
  authoritySource: string
  eventType: string
  outcome: string
  principalID: string
  targetType: string
  targetID: string
  from: string
  to: string
}

const emptyFilters: AuditFilters = {
  projectID: "",
  module: "",
  actorKind: "",
  authoritySource: "",
  eventType: "",
  outcome: "",
  principalID: "",
  targetType: "",
  targetID: "",
  from: "",
  to: "",
}

const outcomeOptions = [
  { value: "all", label: "allOutcomes" },
  { value: "success", label: "success" },
  { value: "denied", label: "deny" },
  { value: "failure", label: "failure" },
]

export function SystemAuditEventsPage() {
  return <AuditEventsPage scope="system" />
}

export default function AuditEventsPage({
  scope = "tenant",
}: {
  scope?: "system" | "tenant"
}) {
  const locale = useLocale()

  const t = useTranslations("iam")
  const allowed = useAllowed()
  const system = scope === "system"
  const [params, setParams] = useSearchParams()
  const detailID = params.get("id") ?? ""
  const canView = allowed(system ? "system_audit_event_view" : "audit_event_view")

  const [events, setEvents] = useState<AuditEvent[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [query, setQuery] = useState<Parameters<typeof iamApi.auditExport>[0]>(
    {}
  )
  const [filterReset, setFilterReset] = useState(0)
  const [integrity, setIntegrity] = useState<AuditIntegrity | null>(null)
  const [selected, setSelected] = useState<AuditEvent | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState("")
  const [exporting, setExporting] = useState(false)
  const [error, setError] = useState("")
  const detailBusy = Boolean(detailID && selected?.id !== detailID && !error)
  const [refreshKey, setRefreshKey] = useState(0)

  useEffect(() => {
    let active = true
    void Promise.all([
      system
        ? iamApi.systemAuditEvents({ ...query, offset, limit: pageSize })
        : iamApi.auditEvents({ ...query, offset, limit: pageSize }),
      system ? iamApi.systemAuditIntegrity() : iamApi.auditIntegrity(),
    ]).then(
      ([page, chain]) => {
        if (!active) return
        setEvents(page.items)
        setTotal(page.total)
        setIntegrity(chain)
        setLoadError("")
        setLoading(false)
      },
      (cause: Error) => {
        if (!active) return
        setLoadError(cause.message)
        setEvents([])
        setIntegrity(null)
        setLoading(false)
      }
    )
    return () => {
      active = false
    }
  }, [offset, query, refreshKey, system])

  const submit = (values: Record<string, unknown>) => {
    const filters = Object.fromEntries(
      Object.keys(emptyFilters).map((key) => [key, String(values[key] ?? "")])
    ) as unknown as AuditFilters
    setLoading(true)
    setOffset(0)
    setQuery({
      ...(!system ? {
        project_id: filters.projectID.trim() || undefined,
        module: filters.module.trim() || undefined,
        actor_kind: (["HUMAN", "AGENT", "MACHINE", "CLIENT", "SYSTEM"] as const).find(kind => kind === filters.actorKind),
        authority_source: filters.authoritySource.trim() || undefined,
      } : {}),
      event_type: filters.eventType.trim() || undefined,
      outcome:
        filters.outcome === "success" ||
        filters.outcome === "denied" ||
        filters.outcome === "failure"
          ? filters.outcome
          : undefined,
      principal_id: filters.principalID.trim() || undefined,
      target_type: filters.targetType.trim() || undefined,
      target_id: filters.targetID.trim() || undefined,
      from: asRFC3339(filters.from) || undefined,
      to: asRFC3339(filters.to) || undefined,
    })
  }

  const reset = () => {
    setLoading(true)
    setFilterReset((value) => value + 1)
    setOffset(0)
    setQuery({})
  }

  useEffect(() => {
    if (!detailID || !canView) return
    let active = true
    void (system ? iamApi.systemAuditEvent(detailID) : iamApi.auditEvent(detailID)).then(
      event => { if (active) { setSelected(event); setError("") } },
      cause => { if (active) { setSelected(null); setError((cause as Error).message) } }
    )
    return () => { active = false }
  }, [detailID, system, canView])

  const openDetail = (event: AuditEvent) => {
    setError("")
    setParams(current => { const next = new URLSearchParams(current); next.set("id", event.id); return next })
  }

  const exportEvents = async () => {
    setExporting(true)
    setError("")
    try {
      const download = await (system
        ? iamApi.systemAuditExport(query)
        : iamApi.auditExport(query))
      const url = URL.createObjectURL(download.blob)
      const anchor = document.createElement("a")
      anchor.href = url
      anchor.download = download.filename
      anchor.hidden = true
      document.body.append(anchor)
      anchor.click()
      anchor.remove()
      window.setTimeout(() => URL.revokeObjectURL(url), 0)
      setLoading(true)
      setRefreshKey((value) => value + 1)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setExporting(false)
    }
  }

  const first = total === 0 ? 0 : offset + 1
  const last = Math.min(offset + events.length, total)

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      <PageHeader
        title={t(system ? "systemAuditEvents" : "auditLog")}
        description={t(system ? "systemAuditDescription" : "auditDescription")}
      />
      {error && <Alert>{error}</Alert>}
      {loadError && !loading && (
        <Alert>
          <p>{t.has(loadError) ? t(loadError) : loadError}</p>
          <Button
            variant="outline"
            onClick={() => {
              setLoading(true)
              setRefreshKey((value) => value + 1)
            }}
          >
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      <IntegrityStatus value={integrity} loading={loading} />

      <AutoTable
        fill
        toolbar={
          <details className="w-full">
            <summary className="cursor-pointer py-2 text-sm font-medium">
              {t("query")}
            </summary>
            <AutoForm
              key={filterReset}
              className="max-h-40 w-full items-end gap-3 overflow-y-auto md:grid-cols-4"
              onSubmit={submit}
              fields={{
                ...(!system ? {
                  projectID: {
                    name: t("auditProjectID"), type: "text" as const, value: "",
                    config: { id: "audit-project", maxLength: 19 },
                  },
                  module: {
                    name: t("auditModule"), type: "text" as const, value: "",
                    config: { id: "audit-module", maxLength: 64, placeholder: "delivery" },
                  },
                  actorKind: {
                    name: t("auditActorKind"), type: "select" as const, value: "all",
                    config: { id: "audit-actor-kind", options: [
                      { value: "all", label: t("auditAllActors") },
                      ...(["HUMAN", "AGENT", "MACHINE", "CLIENT", "SYSTEM"] as const).map(kind => ({value: kind, label: t(`auditActor_${kind}`)})),
                    ] },
                  },
                  authoritySource: {
                    name: t("auditAuthoritySource"), type: "text" as const, value: "",
                    config: { id: "audit-authority-source", maxLength: 91, placeholder: "ROLE:engineer" },
                  },
                } : {}),
                eventType: {
                  name: t("eventType"),
                  type: "text",
                  value: "",
                  config: {
                    id: "audit-event-type",
                    maxLength: 128,
                    placeholder: "oauth_client_created",
                  },
                },
                outcome: {
                  name: t("outcome"),
                  type: "select",
                  value: "all",
                  config: {
                    id: "audit-outcome",
                    options: outcomeOptions.map((option) => ({
                      ...option,
                      label: t(option.label),
                    })),
                  },
                },
                principalID: {
                  name: t("actorId"),
                  type: "text",
                  value: "",
                  config: { id: "audit-principal", maxLength: 128 },
                },
                targetType: {
                  name: t("targetType"),
                  type: "text",
                  value: "",
                  config: {
                    id: "audit-target-type",
                    maxLength: 128,
                    placeholder: "oauth_client",
                  },
                },
                targetID: {
                  name: t("targetId"),
                  type: "text",
                  value: "",
                  config: { id: "audit-target-id", maxLength: 128 },
                },
                from: {
                  name: t("startTime"),
                  type: "text",
                  value: "",
                  config: {
                    id: "audit-from",
                    maxLength: 128,
                    type: "datetime-local",
                  },
                },
                to: {
                  name: t("endTime"),
                  type: "text",
                  value: "",
                  config: {
                    id: "audit-to",
                    maxLength: 128,
                    type: "datetime-local",
                  },
                },
              }}
            >
              <div className="flex items-end gap-2">
                <Button type="submit" className="flex-1">
                  <Search />
                  {t("query")}
                </Button>
                <Button type="button" variant="outline" onClick={reset}>
                  {t("reset")}
                </Button>
              </div>
            </AutoForm>
          </details>
        }
        title={
          <span>
            {t("securityEvents")} {first}-{last} / {total}
          </span>
        }
        rowOffset={offset}
        footer={
          <div className="flex w-full flex-wrap items-center justify-between gap-3">
            <span className="text-sm text-muted-foreground">
              {Math.min(offset + pageSize, total)} / {total}
            </span>
            <PaginationPages
              page={Math.floor(offset / pageSize) + 1}
              totalPages={Math.max(1, Math.ceil(total / pageSize))}
              disabled={loading}
              onPageChange={(page) => {
                setLoading(true)
                setOffset((page - 1) * pageSize)
              }}
            />
          </div>
        }
        onRefresh={() => {
          setLoading(true)
          setRefreshKey((value) => value + 1)
        }}
        exportable={
          allowed(
            system ? "system_audit_event_export" : "audit_event_export"
          ) &&
          integrity?.valid === true &&
          !exporting
        }
        onExport={exportEvents}
        emptyContent={<EmptyEvents />}
        loading={loading}
        data={loading || loadError ? [] : events}
        rowKey="id"
        pagination={false}
        columns={[
          {
            field: "created_at",
            name: t("occurredAt"),
            render: (_, event) => (
              <>
                <div className="text-xs">
                  {formatTime(event.created_at, locale)}
                </div>
              </>
            ),
          },
          {
            field: "event_type",
            name: t("event"),
            render: (_, event) => (
              <>
                <code className="font-mono text-xs">{event.event_type}</code>
              </>
            ),
          },
          {
            field: "principal_id",
            name: t("auditActor"),
            render: (_, event) => (
              <>
                <code className="block max-w-48 truncate font-mono text-xs text-muted-foreground">
                  {event.principal_id || "system"}
                </code>
              </>
            ),
          },
          {
            field: "target_type",
            name: t("target"),
            render: (_, event) => (
              <>
                <span className="block text-xs">
                  {event.target_type || "-"}
                </span>
                <code className="mt-1 block max-w-56 truncate font-mono text-xs text-muted-foreground">
                  {event.target_id || "-"}
                </code>
              </>
            ),
          },
          {
            field: "outcome",
            name: t("outcome"),
            render: (_, event) => (
              <>
                <OutcomeBadge outcome={event.outcome} />
              </>
            ),
          },
          {
            field: "sequence",
            name: t("chainSequence"),
            render: (_, event) => (
              <>
                <div className="text-xs">
                  {event.sequence > 0 ? `#${event.sequence}` : "legacy"}
                </div>
              </>
            ),
          },
          {
            field: "actions",
            type: "actions",
            name: t("details"),
            render: (_, event) => (
              <>
                <div className="text-right">
                  <DetailButton
                    event={event}
                    disabled={detailBusy}
                    onClick={() => void openDetail(event)}
                  />
                </div>
              </>
            ),
          },
        ]}
      />
      <EventDetail value={canView && selected?.id === detailID ? selected : null} onClose={() => {
        setParams(current => { const next = new URLSearchParams(current); next.delete("id"); return next })
      }} />
    </div>
  )
}

function IntegrityStatus({
  value,
  loading,
}: {
  value: AuditIntegrity | null
  loading: boolean
}) {
  const t = useTranslations("iam")

  const valid = value?.valid === true
  return (
    <section
      className={`flex min-h-16 shrink-0 items-center gap-3 rounded-md border px-4 ${valid ? "border-success/30 bg-success/5" : value ? "border-destructive/30 bg-destructive/5" : "bg-muted/20"}`}
      aria-live="polite"
    >
      {valid ? (
        <CheckCircle2 className="size-5 shrink-0 text-success" />
      ) : (
        <ShieldAlert className="size-5 shrink-0 text-muted-foreground" />
      )}
      <div className="min-w-0">
        <strong className="block text-sm">
          {loading
            ? t("verifyingAuditChain")
            : valid
              ? t("auditChainIntact")
              : value
                ? t("auditChainInvalid")
                : t("notYetVerified")}
        </strong>
        <p className="mt-1 truncate text-xs text-muted-foreground">
          {value
            ? t("auditVerifiedCount", {
                count: value.verified_events,
                head: value.head_sequence,
              })
            : t("awaitTenantIntegrity")}
        </p>
      </div>
    </section>
  )
}

function OutcomeBadge({ outcome }: { outcome: AuditEvent["outcome"] }) {
  const t = useTranslations("iam")

  const label =
    outcome === "success"
      ? t("success")
      : outcome === "denied"
        ? t("deny")
        : t("failure")
  return (
    <Badge
      variant={
        outcome === "success"
          ? "default"
          : outcome === "denied"
            ? "outline"
            : "destructive"
      }
      className="shrink-0 whitespace-nowrap"
    >
      {label}
    </Badge>
  )
}

function DetailButton({
  event,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { event: AuditEvent }) {
  const t = useTranslations("iam")

  const label = t("eventDetailsNamed", { event: event.event_type })
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger
          {...props}
          aria-label={label}
          className={buttonVariants({
            size: "icon",
            variant: "ghost",
            className: "size-10",
          })}
        >
          <Eye />
        </TooltipTrigger>
        <TooltipContent>{label}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

function EventDetail({
  value,
  onClose,
}: {
  value: AuditEvent | null
  onClose: () => void
}) {
  const locale = useLocale()

  const t = useTranslations("iam")

  return (
    <Dialog open={value !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("auditEventDetails")}</DialogTitle>
          <DialogDescription>
            {value
              ? `${value.event_type} · ${formatTime(value.created_at, locale)}`
              : ""}
          </DialogDescription>
        </DialogHeader>
        {value && (
          <div className="grid gap-4">
            <DetailGrid event={value} />
            <DetailCode
              label={t("eventData")}
              value={JSON.stringify(value.detail, null, 2)}
            />
            <DetailCode
              label={t("previousHash")}
              value={value.previous_hash || "-"}
            />
            <DetailCode
              label={t("eventHash")}
              value={value.event_hash || "legacy event"}
            />
          </div>
        )}
        <DialogFooter>
          <Button type="button" onClick={onClose}>
            {t("close")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function DetailGrid({ event }: { event: AuditEvent }) {
  const t = useTranslations("iam")

  const items = [
    [t("eventId"), event.id],
    [t("tenant"), event.tenant_id],
    [t("auditActor"), event.principal_id || "system"],
    // principal_id is the acting operator; an account view records the
    // account it looked at separately.
    ...(typeof event.detail?.viewed_principal_id === "string"
      ? [[t("viewedAccount"), event.detail.viewed_principal_id]]
      : []),
    [t("target"), `${event.target_type || "-"}:${event.target_id || "-"}`],
    [t("outcome"), event.outcome],
    [t("sequence"), event.sequence > 0 ? String(event.sequence) : "legacy"],
    ["IP", event.ip_address || "-"],
    [t("userAgent"), event.user_agent || "-"],
  ]
  return (
    <dl className="grid gap-x-4 gap-y-3 rounded-md border p-4 sm:grid-cols-2">
      {items.map(([label, value]) => (
        <div key={label} className="min-w-0">
          <dt className="text-xs text-muted-foreground">{label}</dt>
          <dd className="mt-1 font-mono text-xs break-words">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function DetailCode({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid gap-2">
      <Label>{label}</Label>
      <pre className="max-h-48 overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-xs break-all whitespace-pre-wrap">
        {value}
      </pre>
    </div>
  )
}

function EmptyEvents() {
  const t = useTranslations("iam")

  return (
    <div>
      <FileClock className="mx-auto mb-2 size-7 text-muted-foreground" />
      <p className="text-sm font-medium">{t("noAuditEvents")}</p>
    </div>
  )
}

function asRFC3339(value: string): string {
  return value ? new Date(value).toISOString() : ""
}

function formatTime(value: string, locale: string): string {
  return new Date(value).toLocaleString(locale)
}
