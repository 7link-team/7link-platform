import { useEffect, useRef, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import {
  AutoTable,
  TableFilterBar,
  type FilterOption,
} from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { ActivityLabel } from "../../components/activity-label"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { useAllowed } from "../../lib/permissions"
import {
  platformEventsApi,
  replayRange,
  type DomainEvent,
  type EventFilters,
  type EventSubscriber,
  type ParkedEvent,
  type ReplayRange,
} from "../../lib/platform-events-api"

type Tab = "subscribers" | "parked" | "events"
type Mutation =
  | { subscriber: EventSubscriber; kind: "pause" | "replay" }
  | { row: ParkedEvent; kind: "retry" | "discard" }
export default function PlatformDomainEventsPage() {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const view = allowed("system_event_view"),
    manage = allowed("system_event_manage")
  const [tab, setTab] = useState<Tab>("subscribers")
  const [subscribers, setSubscribers] = useState<EventSubscriber[]>([])
  const [subscriber, setSubscriber] = useState("")
  const [events, setEvents] = useState<DomainEvent[]>([])
  const [parked, setParked] = useState<ParkedEvent[]>([])
  const [filters, setFilters] = useState<Record<string, string>>({})
  const [offset, setOffset] = useState(0),
    [total, setTotal] = useState(0)
  const [refresh, setRefresh] = useState(0)
  const [loading, setLoading] = useState(true),
    [busy, setBusy] = useState(false)
  const [error, setError] = useState(""),
    [notice, setNotice] = useState("")
  const [detail, setDetail] = useState<DomainEvent | null>(null)
  const [mutation, setMutation] = useState<Mutation | null>(null)
  const [replay, setReplay] = useState<{
    name: string
    range: ReplayRange
  } | null>(null)
  const replayKey = useRef("")
  useEffect(() => {
    let current = true
    if (!view) return
    void platformEventsApi.subscribers().then(
      (rows) => {
        if (!current) return
        setSubscribers(rows ?? [])
        setLoading(false)
      },
      (cause: Error) => {
        if (current) {
          setError(cause.message)
          setLoading(false)
        }
      }
    )
    return () => {
      current = false
    }
  }, [view, refresh])
  useEffect(() => {
    let current = true
    if (!view || tab === "subscribers" || (tab === "parked" && !subscriber))
      return
    const load =
      tab === "events"
        ? platformEventsApi.events(filters as EventFilters, offset)
        : platformEventsApi.parked(subscriber, offset, filters)
    void load.then(
      (page) => {
        if (!current) return
        if (tab === "events") setEvents(page.items as DomainEvent[])
        else setParked(page.items as ParkedEvent[])
        setTotal(page.total)
        setLoading(false)
        setError("")
      },
      (cause: Error) => {
        if (current) {
          setError(cause.message)
          setLoading(false)
          setEvents([])
          setParked([])
        }
      }
    )
    return () => {
      current = false
    }
  }, [view, tab, subscriber, filters, offset, refresh])
  const inspect = async (id: string | null) => {
    setBusy(true)
    setError("")
    try {
      setDetail(await platformEventsApi.event(id))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  if (!view) return <Alert>{t("eventsNoAccess")}</Alert>
  return (
    <section className="space-y-4">
      <PageHeader
        title={t("eventsTitle")}
        description={t("eventsDescription")}
        action={
          <Button
            variant="outline"
            disabled={busy}
            onClick={() => {
              setLoading(true)
              setRefresh((n) => n + 1)
            }}
          >
            {t("refresh")}
          </Button>
        }
      />
      {error && <Alert>{error}</Alert>}
      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}
      <div
        role="tablist"
        aria-label={t("eventsTitle")}
        className="flex flex-wrap gap-2"
      >
        {(["subscribers", "parked", "events"] as const).map((value) => (
          <Button
            key={value}
            role="tab"
            aria-selected={tab === value}
            variant={tab === value ? "default" : "outline"}
            onClick={() => {
              setTab(value)
              setRefresh((n) => n + 1)
              setFilters({})
              setOffset(0)
              setLoading(value === "parked" ? !!subscriber : true)
              setError("")
            }}
          >
            {t(`eventsTab${value}`)}
          </Button>
        ))}
      </div>
      {tab === "subscribers" && (
        <AutoTable
          data={subscribers}
          loading={loading}
          rowKey="name"
          pagination={false}
          emptyContent={t("eventsEmpty")}
          columns={[
            {
              field: "name",
              name: t("name"),
              render: (_, row) => <ActivityLabel {...row} code={row.name} />,
            },
            {
              field: "event_types",
              name: t("activityTriggers"),
              render: (_, row) => (row.event_types ?? []).join(", "),
            },
            { field: "mode", name: t("eventsMode") },
            { field: "partitions", name: t("eventsPartitions") },
            {
              field: "cursors",
              name: t("eventsCursors"),
              render: (_, row) =>
                (row.cursors ?? [])
                  .map(
                    (cursor) =>
                      `${cursor.p}: ${cursor.last_seq} (${t("eventsLag")}: ${cursor.lag})`
                  )
                  .join("; "),
            },
            {
              field: "parked",
              name: t("eventsTabparked"),
              render: (_, row) => (
                <Button
                  variant="link"
                  onClick={() => {
                    setSubscriber(row.name)
                    setTab("parked")
                    setFilters({})
                    setOffset(0)
                    setLoading(true)
                  }}
                >
                  {row.parked}
                </Button>
              ),
            },
            { field: "blocked", name: t("eventsBlocked") },
            {
              field: "paused",
              name: t("jobsState"),
              render: (_, row) =>
                t(row.paused ? "eventsPaused" : "eventsActive"),
            },
            {
              field: "actions",
              name: t("actions"),
              render: (_, row) =>
                manage && (
                  <div className="flex gap-2">
                    <Button
                      variant="outline"
                      disabled={busy}
                      onClick={() =>
                        setMutation({ subscriber: row, kind: "pause" })
                      }
                    >
                      {t(row.paused ? "eventsResume" : "eventsPause")}
                    </Button>
                    <Button
                      disabled={busy}
                      onClick={() => {
                        replayKey.current = ""
                        setMutation({ subscriber: row, kind: "replay" })
                      }}
                    >
                      {t("eventsReplay")}
                    </Button>
                  </div>
                ),
            },
          ]}
        />
      )}
      {tab !== "subscribers" && (
        <>
          <TableFilterBar
            key={`${tab}:${subscriber}:${JSON.stringify(filters)}`}
            defaultValue={{ ...filters, subscriber }}
            filters={[
              ...(tab === "parked"
                ? ([
                    {
                      field: "subscriber",
                      name: t("eventsSubscriber"),
                      type: "select",
                      conditions: [],
                      options: subscribers.map((row) => ({
                        value: row.name,
                        label: row.display_name
                          ? `${row.display_name} (${row.name})`
                          : row.name,
                      })),
                    },
                    {
                      field: "state",
                      name: t("jobsState"),
                      type: "select",
                      conditions: [],
                      options: [
                        "",
                        "PARKED",
                        "REDELIVERING",
                        "DELIVERED",
                        "DISCARDED",
                      ].map((value) => ({
                        value,
                        label: value ? t(`eventsState${value}`) : t("all"),
                      })),
                    },
                    {
                      field: "reason",
                      name: t("eventsReason"),
                      type: "select",
                      conditions: [],
                      options: ["", "FAILED", "BLOCKED"].map((value) => ({
                        value,
                        label: value ? t(`eventsReason${value}`) : t("all"),
                      })),
                    },
                  ] satisfies FilterOption[])
                : ([
                    {
                      field: "event_type",
                      name: t("eventsType"),
                      type: "string",
                      conditions: ["eq"],
                    },
                    {
                      field: "tenant_id",
                      name: t("jobsTenantFilter"),
                      type: "string",
                      conditions: ["eq"],
                    },
                    {
                      field: "subject_id",
                      name: t("eventsSubjectID"),
                      type: "string",
                      conditions: ["eq"],
                    },
                    {
                      field: "stream_seq",
                      name: t("eventsSequenceFilter"),
                      type: "number",
                      conditions: ["gt", "lt"],
                    },
                    {
                      field: "occurred_at",
                      name: t("eventsOccurredFilter"),
                      type: "datetime",
                      conditions: ["gte", "lte", "gt", "lt"],
                    },
                  ] satisfies FilterOption[])),
              {
                field: "subject_kind",
                name: t("eventsSubjectKind"),
                type: "string",
                conditions: [],
              },
            ]}
            onSearch={(values) => {
              const next = { ...values }
              if (tab === "parked") {
                setSubscriber(next.subscriber ?? "")
                delete next.subscriber
              }
              setFilters(next)
              setOffset(0)
              setLoading(tab === "events" || !!values.subscriber)
            }}
            onReset={() => {
              setFilters({})
              setSubscriber("")
              setOffset(0)
              setLoading(tab === "events")
            }}
          />
          {tab === "events" ? (
            <AutoTable
              data={events}
              rowKey="event_id"
              loading={loading}
              pagination={false}
              emptyContent={t("eventsEmpty")}
              columns={[
                {
                  field: "event_id",
                  name: t("id"),
                  render: (_, row) => (
                    <Button
                      variant="link"
                      disabled={busy}
                      onClick={() => void inspect(row.event_id)}
                    >
                      {row.event_id}
                    </Button>
                  ),
                },
                { field: "stream_seq", name: t("eventsSequence") },
                {
                  field: "event_type",
                  name: t("eventsType"),
                  render: (_, row) => (
                    <ActivityLabel {...row} code={row.event_type} />
                  ),
                },
                { field: "tenant_id", name: t("jobsTenant") },
                {
                  field: "subject",
                  name: t("eventsSubjectKind"),
                  render: (_, row) => `${row.subject.kind}/${row.subject.id}`,
                },
                { field: "occurred_at", name: t("eventsOccurred") },
              ]}
            />
          ) : (
            <AutoTable
              data={parked}
              rowKey="id"
              loading={loading && !!subscriber}
              pagination={false}
              emptyContent={t(
                subscriber ? "eventsEmpty" : "eventsChooseSubscriber"
              )}
              columns={[
                { field: "id", name: t("id") },
                { field: "stream_seq", name: t("eventsSequence") },
                {
                  field: "subject_kind",
                  name: t("eventsSubjectKind"),
                  render: (_, row) => `${row.subject_kind}/${row.subject_id}`,
                },
                { field: "reason", name: t("eventsReason") },
                { field: "state", name: t("jobsState") },
                { field: "attempts", name: t("jobsAttempts") },
                { field: "last_error_code", name: t("jobsLastError") },
                {
                  field: "actions",
                  name: t("actions"),
                  render: (_, row) =>
                    manage && (
                      <div className="flex gap-2">
                        <Button
                          disabled={busy || row.state !== "PARKED"}
                          onClick={() => setMutation({ row, kind: "retry" })}
                        >
                          {t("eventsRetry")}
                        </Button>
                        <Button
                          variant="destructive"
                          disabled={busy || row.state !== "PARKED"}
                          onClick={() => setMutation({ row, kind: "discard" })}
                        >
                          {t("eventsDiscard")}
                        </Button>
                      </div>
                    ),
                },
              ]}
            />
          )}
          <div className="flex justify-end gap-2">
            <Button
              variant="outline"
              disabled={loading || offset === 0}
              onClick={() => {
                setOffset((n) => Math.max(0, n - 50))
                setLoading(true)
              }}
            >
              {t("previous")}
            </Button>
            <Button
              variant="outline"
              disabled={loading || offset + 50 >= total}
              onClick={() => {
                setOffset((n) => n + 50)
                setLoading(true)
              }}
            >
              {t("next")}
            </Button>
          </div>
        </>
      )}
      <Dialog
        open={!!detail}
        onOpenChange={(open) => {
          if (!open) setDetail(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("eventsDetail")}</DialogTitle>
            <DialogDescription>{t("eventsReadAudited")}</DialogDescription>
          </DialogHeader>
          {detail && <ActivityLabel {...detail} code={detail.event_type} />}
          <pre className="max-h-96 overflow-auto text-xs break-all whitespace-pre-wrap">
            {JSON.stringify(detail, null, 2)}
          </pre>
        </DialogContent>
      </Dialog>
      <Dialog
        open={!!mutation}
        onOpenChange={(open) => {
          if (!open && !busy) setMutation(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t(
                mutation?.kind === "replay" ? "eventsReplay" : "eventsConfirm"
              )}
            </DialogTitle>
            <DialogDescription>
              {mutation &&
                ("subscriber" in mutation
                  ? mutation.subscriber.name
                  : mutation.row.id)}
            </DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          {mutation?.kind === "pause" ? (
            <Button
              disabled={busy}
              onClick={async () => {
                if (!manage || busy) return
                setBusy(true)
                setError("")
                try {
                  await platformEventsApi.pause(
                    mutation.subscriber,
                    !mutation.subscriber.paused
                  )
                  setMutation(null)
                  setRefresh((n) => n + 1)
                } catch (cause) {
                  setError((cause as Error).message)
                } finally {
                  setBusy(false)
                }
              }}
            >
              {t("confirm")}
            </Button>
          ) : (
            mutation && (
              <AutoForm
                key={mutation.kind}
                fields={
                  mutation.kind === "replay"
                    ? {
                        from_seq: {
                          name: t("eventsFromSeq"),
                          type: "text",
                          required: true,
                        },
                        to_seq: { name: t("eventsToSeq"), type: "text" },
                        ignore_receipts: {
                          name: t("eventsIgnoreReceipts"),
                          type: "checkbox",
                        },
                      }
                    : {
                        reason: {
                          name: t("jobsReason"),
                          type: "textarea",
                          required: true,
                          config: { maxLength: 2000 },
                        },
                      }
                }
                onSubmit={async (values) => {
                  if (!manage || busy) return
                  if (mutation.kind === "replay") {
                    const range = replayRange(values)
                    if (!range) {
                      setError(t("eventsInvalidRange"))
                      return
                    }
                    replayKey.current ||= crypto.randomUUID()
                    setReplay({ name: mutation.subscriber.name, range })
                    setMutation(null)
                    setError("")
                    return
                  }
                  if (mutation.kind !== "retry" && mutation.kind !== "discard")
                    return
                  setBusy(true)
                  setError("")
                  try {
                    await platformEventsApi.mutateParked(
                      mutation.row,
                      mutation.kind === "retry",
                      String(values.reason)
                    )
                    setMutation(null)
                    setRefresh((n) => n + 1)
                  } catch (cause) {
                    setError((cause as Error).message)
                  } finally {
                    setBusy(false)
                  }
                }}
              >
                <Button type="submit" disabled={busy}>
                  {t("confirm")}
                </Button>
              </AutoForm>
            )
          )}
        </DialogContent>
      </Dialog>
      {replay && (
        <ReauthenticationDialog
          purpose="system_event_replay"
          target={replay.name}
          onClose={() => setReplay(null)}
          onVerified={async (proof) => {
            if (!manage) throw new Error(t("eventsNoAccess"))
            const result = await platformEventsApi.replay(
              replay.name,
              replay.range,
              proof,
              replayKey.current
            )
            setNotice(
              `${t("eventsReplayCreated")}: ${result.replay_cursor} (${result.from_seq}–${result.to_seq})`
            )
            setReplay(null)
            replayKey.current = ""
            setRefresh((n) => n + 1)
          }}
        />
      )}
    </section>
  )
}
