import { useCallback, useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { FrozenArtifactAnchor } from "../executions/artifact-comments"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { PaginationPages } from "@workspace/ui/components/pagination"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAllowed } from "../../lib/permissions"
import {
  resultsApi,
  type ResultFeedback,
  type ResultFeedbackInput,
} from "./results-api"

export function ResultFeedbackPanel({
  entityID,
  workID,
  resultID,
  sequence,
  inboxSecondary = false,
}: {
  entityID: string
  workID: string
  resultID: string
  sequence: number
  inboxSecondary?: boolean
}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const [searchParams] = useSearchParams()
  const sourceThread = searchParams.get("comment_thread") ?? ""
  const allowed = useAllowed()
  const canRead = allowed("work_item_read")
  const canComment = allowed("artifact_comment_write")
  const [rows, setRows] = useState<ResultFeedback[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [composing, setComposing] = useState(() => /^[1-9][0-9]*$/.test(sourceThread))
  const active = useRef(true)
  const pending = useRef(false)
  const controller = useRef<AbortController | null>(null)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  const load = useCallback(async () => {
    if (!canRead) return
    controller.current?.abort()
    const c = new AbortController()
    controller.current = c
    setLoading(true)
    setError("")
    try {
      const page = await resultsApi.feedback(
        entityID,
        workID,
        resultID,
        offset,
        c.signal
      )
      if (c.signal.aborted || !active.current) return
      setRows(page.items)
      setTotal(page.total)
    } catch (cause) {
      if (!c.signal.aborted && active.current) {
        setRows([])
        setError(cause instanceof Error ? cause.message : t("workUnavailable"))
      }
    } finally {
      if (!c.signal.aborted && active.current) setLoading(false)
    }
  }, [canRead, entityID, workID, resultID, offset, t])
  useEffect(() => {
    active.current = true
    let live = true
    queueMicrotask(() => {
      if (live) void load()
    })
    return () => {
      live = false
      active.current = false
      controller.current?.abort()
    }
  }, [load])
  if (!canRead) return null
  return (
    <section
      className="grid gap-3 border-t pt-4"
      aria-label={t("resultFeedbackTitle")}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-medium">{t("resultFeedbackTitle")}</h3>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={loading || busy}
          onClick={() => void load()}
        >
          {t("refresh")}
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">{t("resultFeedbackHint")}</p>
      <p className="text-sm break-all">
        {t("resultExactVersion", { number: sequence })} {resultID}
      </p>
      {error && <Alert>{error}</Alert>}
      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}
      <AutoTable
        data={rows}
        loading={loading}
        rowKey="id"
        rowOffset={offset}
        pagination={false}
        exportable={false}
        emptyContent={t("resultFeedbackEmpty")}
        columns={[
          {
            field: "kind",
            name: t("resultFeedbackKind"),
            format: (_, row) => t(`resultFeedbackKind_${row.kind}`),
          },
          {
            field: "category",
            name: t("resultFeedbackCategory"),
            format: (_, row) =>
              row.category ? t(`resultFeedbackCategory_${row.category}`) : "—",
          },
          { field: "detail", name: t("resultFeedbackDetail"), format: (_, row) => <div className="grid gap-2"><p className="whitespace-pre-wrap">{row.detail}</p>{row.anchor_snapshot && <FrozenArtifactAnchor anchor={row.anchor_snapshot} />}{row.comment_thread_id && <p>{t("artifactCommentSourceThread")}: {row.comment_thread_id}</p>}</div> },
          { field: "actor_id", name: t("resultActor") },
          {
            field: "created_at",
            name: t("createdAt"),
            format: (_, row) => new Date(row.created_at).toLocaleString(locale),
          },
        ]}
      />
      <PaginationPages
        page={Math.floor(offset / 20) + 1}
        totalPages={Math.max(1, Math.ceil(total / 20))}
        disabled={loading || busy}
        onPageChange={(page) => setOffset((page - 1) * 20)}
      />
      {!composing && canComment && (
        <Button
          type="button"
          variant="outline"
          disabled={busy || loading}
          data-inbox-secondary={inboxSecondary || undefined}
          onClick={() => {
            setComposing(true)
            setNotice("")
          }}
        >
          {t("resultFeedbackAdd")}
        </Button>
      )}
      {composing && canComment && (
        <AutoForm
          mode="edit"
          initialData={{
            kind: sourceThread || inboxSecondary ? "FEEDBACK" : "COMMENT",
            category: "QUALITY_REJECTION",
            detail: "",
            comment_thread_id: sourceThread,
            location: "",
            expected: "",
          }}
          fields={{
            comment_thread_id: { name: t("artifactCommentSourceThread"), type: "text", show: values => values?.kind === "FEEDBACK" },
            location: { name: t("artifactCommentSourceLocation"), type: "text", show: values => values?.kind === "FEEDBACK" },
            expected: { name: t("artifactCommentExpected"), type: "textarea", show: values => values?.kind === "FEEDBACK", config: { maxLength: 2000 } },
            kind: {
              name: t("resultFeedbackKind"),
              type: "select",
              required: true,
              config: {
                options: ["COMMENT", "FEEDBACK"].map((value) => ({
                  value,
                  label: t(`resultFeedbackKind_${value}`),
                })),
              },
            },
            category: {
              name: t("resultFeedbackCategory"),
              type: "select",
              show: (values) => values?.kind === "FEEDBACK",
              required: true,
              config: {
                options: [
                  "QUALITY_REJECTION",
                  "STRATEGY_CHANGE",
                  "SCOPE_CHANGE",
                ].map((value) => ({
                  value,
                  label: t(`resultFeedbackCategory_${value}`),
                })),
              },
            },
            detail: {
              name: t("resultFeedbackDetail"),
              type: "textarea",
              required: values => !values?.comment_thread_id,
              config: { maxLength: 2000 },
            },
          }}
          onSubmit={async (values) => {
            if (pending.current || !allowed("artifact_comment_write")) return
            const body: ResultFeedbackInput = {
              kind: values.kind as ResultFeedbackInput["kind"],
              detail: String(values.detail ?? "").trim(),
              ...(values.kind === "FEEDBACK"
                ? {
                    category:
                      values.category as ResultFeedbackInput["category"],
                    ...(values.comment_thread_id ? { comment_thread_id: String(values.comment_thread_id) } : {}),
                    ...(values.location ? { location: String(values.location) } : {}),
                    ...(values.expected ? { expected: String(values.expected) } : {}),
                  }
                : {}),
            }
            const identity = JSON.stringify({ resultID, body })
            const key =
              attempt.current?.identity === identity
                ? attempt.current.key
                : crypto.randomUUID()
            attempt.current = { identity, key }
            pending.current = true
            setBusy(true)
            setError("")
            try {
              await resultsApi.postFeedback(
                entityID,
                workID,
                resultID,
                key,
                body
              )
              attempt.current = null
              if (active.current) {
                setComposing(false)
                setNotice(t("resultSaved"))
                if (offset !== Math.floor(total / 20) * 20)
                  setOffset(Math.floor(total / 20) * 20)
                else await load()
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
              onClick={() => setComposing(false)}
            >
              {t("cancel")}
            </Button>
            <Button type="submit" disabled={busy}>
              {busy ? t("saving") : t("resultFeedbackPost")}
            </Button>
          </div>
        </AutoForm>
      )}
    </section>
  )
}
