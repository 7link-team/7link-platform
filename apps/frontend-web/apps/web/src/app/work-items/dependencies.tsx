import { WorkUpstreamArtifacts } from "./upstream"
import { useCallback, useEffect, useRef, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { PaginationPages } from "@workspace/ui/components/pagination"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import {
  iamApi,
  type WorkDependency,
  type WorkItem,
  type WorkItemDetail,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

export function WorkDependencies({
  entityID,
  detail,
  onUpdated,
}: {
  entityID: string
  detail: WorkItemDetail
  onUpdated: (value: WorkItemDetail) => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [rows, setRows] = useState<WorkDependency[]>([])
  const [choices, setChoices] = useState<WorkItem[]>([])
  const [search, setSearch] = useState("")
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)
  const [selecting, setSelecting] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [choicesLoading, setChoicesLoading] = useState(false)
  const [error, setError] = useState("")
  const [confirm, setConfirm] = useState<{
    id: string
    title: string
    remove: boolean
  } | null>(null)
  const pending = useRef(false)
  const active = useRef(true)
  const canEdit =
    allowed("work_item_write") &&
    !detail.is_container &&
    ["DRAFT", "READY", "BLOCKED", "CLAIMED", "IN_PROGRESS", "REWORK"].includes(
      detail.state
    )
  const load = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true)
      setError("")
      try {
        const [dependencies, current] = await Promise.all([
          iamApi.workDependencies(entityID, detail.id!, signal),
          iamApi.workItem(entityID, detail.id!, signal),
        ])
        if (signal?.aborted || !active.current) return
        setRows(dependencies.items)
        if (current.version !== detail.version) onUpdated(current)
      } catch (cause) {
        if (!signal?.aborted && active.current)
          setError(
            cause instanceof Error ? cause.message : t("workUnavailable")
          )
      } finally {
        if (!signal?.aborted && active.current) setLoading(false)
      }
    },
    [entityID, detail.id, detail.version, onUpdated, t]
  )
  useEffect(() => {
    active.current = true
    const c = new AbortController()
    queueMicrotask(() => {
      if (!c.signal.aborted) void load(c.signal)
    })
    return () => {
      active.current = false
      c.abort()
    }
  }, [load])
  useEffect(() => {
    if (!selecting) return
    const c = new AbortController()
    queueMicrotask(() => {
      if (!c.signal.aborted) setChoicesLoading(true)
    })
    void iamApi
      .workItems(
        entityID,
        {
          offset: (page - 1) * 20,
          limit: 20,
          sort: "number",
          ...(search ? { title: `like:${search}` } : {}),
        },
        c.signal
      )
      .then(
        (result) => {
          if (!c.signal.aborted) {
            setChoices(result.items)
            setTotal(result.total)
          }
        },
        (cause) => {
          if (!c.signal.aborted)
            setError(
              cause instanceof Error ? cause.message : t("workUnavailable")
            )
        }
      )
      .finally(() => {
        if (!c.signal.aborted) setChoicesLoading(false)
      })
    return () => c.abort()
  }, [entityID, page, search, selecting, t])
  return (
    <section
      className="grid gap-3 border-t pt-4"
      aria-label={t("workDependencies")}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="font-medium">{t("workDependencies")}</h2>
        <Button
          size="sm"
          variant="outline"
          disabled={busy || loading}
          onClick={() => void load()}
        >
          {t("refresh")}
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">{t("workDependencyHint")}</p>
 <WorkUpstreamArtifacts entityID={entityID} detail={detail} />
      {error && <Alert>{error}</Alert>}
      <AutoTable
        data={rows}
        loading={loading}
        rowKey="id"
        pagination={false}
        exportable={false}
        columns={[
          { field: "number", name: t("workNumber") },
          { field: "title", name: t("workTitle") },
          {
            field: "state",
            name: t("status"),
            format: (_, row) => t(`workState_${row.state}`),
          },
        ]}
        actions={
          canEdit
            ? [
                {
                  name: t("workDependencyRemove"),
                  onclick: (row) => {
                    if (row.id)
                      setConfirm({ id: row.id, title: row.title, remove: true })
                  },
                  disabled: () => busy,
                },
              ]
            : []
        }
      />
      {canEdit && !confirm && (
        <Button
          variant="outline"
          disabled={busy}
          onClick={() => setSelecting((value) => !value)}
        >
          {selecting ? t("cancel") : t("workDependencyAdd")}
        </Button>
      )}
      {selecting && canEdit && !confirm && (
        <div className="grid gap-3">
          <AutoForm
            mode="edit"
            fields={{ title: { name: t("workTitle"), type: "text" } }}
            onSubmit={(values) => {
              setSearch(String(values.title ?? "").trim())
              setPage(1)
            }}
          >
            <Button type="submit" size="sm">
              {t("search")}
            </Button>
          </AutoForm>
          <AutoTable
            data={choices}
            loading={choicesLoading}
            rowKey="id"
            pagination={false}
            exportable={false}
            columns={[
              { field: "number", name: t("workNumber") },
              { field: "title", name: t("workTitle") },
            ]}
            actions={[
              {
                name: t("workDependencyAdd"),
                onclick: (row) => {
                  if (row.id)
                    setConfirm({ id: row.id, title: row.title, remove: false })
                },
                disabled: (row) =>
                  choicesLoading ||
                  busy ||
                  row.id === detail.id ||
                  rows.some((value) => value.id === row.id),
              },
            ]}
          />
          <PaginationPages
            page={page}
            totalPages={Math.max(1, Math.ceil(total / 20))}
            onPageChange={setPage}
          />
        </div>
      )}
      {confirm && (
        <AutoForm
          key={`${confirm.id}:${confirm.remove}:${detail.version}`}
          mode="edit"
          initialData={{ title: confirm.title }}
          fields={{
            title: { name: t("workTitle"), type: "text", disabled: true },
          }}
          onSubmit={async () => {
            if (pending.current || !canEdit) return
            pending.current = true
            setBusy(true)
            setError("")
            try {
              if (confirm.remove)
                await iamApi.removeWorkDependency(
                  entityID,
                  detail.id!,
                  confirm.id
                )
              else
                await iamApi.addWorkDependency(entityID, detail.id!, confirm.id)
              const current = await iamApi.workItem(entityID, detail.id!)
              if (active.current) {
                const dependencies = await iamApi.workDependencies(
                  entityID,
                  detail.id!
                )
                if (active.current) {
                  setRows(dependencies.items)
                  setConfirm(null)
                  setSelecting(false)
                  onUpdated(current)
                }
              }
            } catch (cause) {
              if (active.current)
                setError(
                  cause instanceof Error ? cause.message : t("workUnavailable")
                )
            } finally {
              pending.current = false
              if (active.current) setBusy(false)
            }
          }}
        >
          <div className="flex gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={() => setConfirm(null)}
            >
              {t("cancel")}
            </Button>
            <Button type="submit" disabled={busy}>
              {busy
                ? t("saving")
                : t(
                    confirm.remove
                      ? "workDependencyRemove"
                      : "workDependencyAdd"
                  )}
            </Button>
          </div>
        </AutoForm>
      )}
    </section>
  )
}
