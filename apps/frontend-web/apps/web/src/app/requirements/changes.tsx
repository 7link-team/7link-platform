import { useEffect, useRef, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { PaginationPages } from "@workspace/ui/components/pagination"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { iamApi, type RequirementDetail } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { scopeBody, submitScopeChange, type ScopeValues } from "./changes-api"

type Change = {
  id: string
  from_version_no: number
  to_version_no: number
  summary: string
  actor_id: string
  created_at: string
}
type Impact = { work_item_id: string; impact: string; version_before: number }
export function RequirementChanges({
  entityID,
  detail,
  onSaved,
}: {
  entityID: string
  detail: RequirementDetail
  onSaved: () => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const canWrite =
    Boolean(detail.id) &&
    allowed("scope_change") &&
    allowed("requirement_write") &&
    detail.status === "BASELINED"
  const path = `/entities/${encodeURIComponent(entityID)}/requirements/${encodeURIComponent(detail.id ?? "")}/change-records`
  const [offset, setOffset] = useState(0)
  const [rows, setRows] = useState<Change[]>([])
  const [total, setTotal] = useState(0)
  const [selected, setSelected] = useState("")
  const [impactOffset, setImpactOffset] = useState(0)
  const [impacts, setImpacts] = useState<Impact[]>([])
  const [impactTotal, setImpactTotal] = useState(0)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<ReturnType<typeof scopeBody> | null>(null)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const pending = useRef(false)
  const attempt = useRef<{ key: string; body: object } | null>(null)
  const active = useRef(true)
  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
    }
  }, [])
  useEffect(() => {
    const controller = new AbortController()
    let live = true
    queueMicrotask(() => {
      if (live) {
        setLoading(true)
        setError("")
      }
    })
    iamApi
      .deliveryResultPage<Change>(
        { method: "GET", url: `${path}?offset=${offset}&limit=20` },
        controller.signal
      )
      .then((page) => {
        if (live) {
          setRows(page.items)
          setTotal(page.total)
        }
      })
      .catch((cause) => {
        if (live)
          setError(
            cause instanceof Error ? cause.message : t("workUnavailable")
          )
      })
      .finally(() => {
        if (live) setLoading(false)
      })
    return () => {
      live = false
      controller.abort()
    }
  }, [path, offset, revision, t])
  useEffect(() => {
    if (!selected) return
    const controller = new AbortController()
    let live = true
    iamApi
      .deliveryResultPage<Impact>(
        {
          method: "GET",
          url: `${path}/${encodeURIComponent(selected)}/impacts?offset=${impactOffset}&limit=20`,
        },
        controller.signal
      )
      .then((page) => {
        if (live) {
          setImpacts(page.items)
          setImpactTotal(page.total)
        }
      })
      .catch((cause) => {
        if (live) {
          setImpacts([])
          setError(
            cause instanceof Error ? cause.message : t("workUnavailable")
          )
        }
      })
    return () => {
      live = false
      controller.abort()
    }
  }, [path, selected, impactOffset, t])
  return (
    <section
      className="grid gap-3 border-t pt-4"
      aria-label={t("scopeHistory")}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-medium">{t("scopeHistory")}</h3>
        <Button
          size="sm"
          variant="outline"
          disabled={busy || loading}
          onClick={() => setRevision((v) => v + 1)}
        >
          {t("refresh")}
        </Button>
        {canWrite && (
          <Button
            size="sm"
            variant="outline"
            disabled={busy}
            onClick={() => setEditing((v) => !v)}
          >
            {t("scopeChange")}
          </Button>
        )}
      </div>
      {error && <Alert>{error}</Alert>}
      <AutoTable
        data={rows}
        loading={loading}
        rowKey="id"
        rowOffset={offset}
        pagination={false}
        exportable={false}
        columns={[
          { field: "to_version_no", name: t("scopeVersion") },
          { field: "summary", name: t("scopeReason") },
          { field: "actor_id", name: t("resultActor") },
          {
            field: "id",
            name: t("scopeImpacts"),
            format: (_, row) => (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setSelected(row.id)
                  setImpactOffset(0)
                  setImpacts([])
                  setImpactTotal(0)
                }}
              >
                {t("details")}
              </Button>
            ),
          },
        ]}
      />
      <PaginationPages
        page={Math.floor(offset / 20) + 1}
        totalPages={Math.max(1, Math.ceil(total / 20))}
        disabled={loading || busy}
        onPageChange={(page) => setOffset((page - 1) * 20)}
      />
      {selected && (
        <>
          <h4 className="text-sm font-medium">{t("scopeImpacts")}</h4>
          <AutoTable
            data={impacts}
            rowKey="work_item_id"
            rowOffset={impactOffset}
            pagination={false}
            exportable={false}
            columns={[
              { field: "work_item_id", name: t("scopeWork") },
              { field: "version_before", name: t("scopePriorVersion") },
              {
                field: "impact",
                name: t("scopeImpact"),
                format: () => t("scopeStale"),
              },
            ]}
          />
          <PaginationPages
            page={Math.floor(impactOffset / 20) + 1}
            totalPages={Math.max(1, Math.ceil(impactTotal / 20))}
            disabled={busy}
            onPageChange={(page) => setImpactOffset((page - 1) * 20)}
          />
        </>
      )}
      {editing && canWrite && (
        <>
          <p className="text-sm text-muted-foreground">
            {t("scopeChangeHint")}
          </p>
          <AutoForm
            id="requirement-scope-form"
            mode="edit"
            initialData={{
              title: detail.title,
              description: detail.description,
              priority: String(detail.priority),
              summary: "",
              action: "NONE",
              criterion_id: detail.criteria?.[0]?.id ?? "",
              text: "",
              required: true,
            }}
            fields={{
              title: { name: t("workTitle"), type: "text", required: true },
              description: { name: t("description"), type: "textarea" },
              priority: {
                name: t("workPriority"),
                type: "select",
                config: {
                  options: [0, 1, 2, 3].map((value) => ({
                    value: String(value),
                    label: `P${value}`,
                  })),
                },
              },
              summary: {
                name: t("scopeReason"),
                type: "textarea",
                required: true,
              },
              action: {
                name: t("scopeCriterionAction"),
                type: "select",
                config: {
                  options: ["NONE", "ADD", "RETIRE", "SUPERSEDE"].map(
                    (value) => ({ value, label: t(`scopeAction_${value}`) })
                  ),
                },
              },
              criterion_id: {
                name: t("scopeCriterion"),
                type: "select",
                required: true,
                show: (v) =>
                  v?.action === "RETIRE" || v?.action === "SUPERSEDE",
                config: {
                  options: (detail.criteria ?? [])
                    .filter((c) => c.id)
                    .map((c) => ({
                      value: c.id!,
                      label: `${c.code}: ${c.text}`,
                    })),
                },
              },
              text: {
                name: t("workCriterionText"),
                type: "textarea",
                required: true,
                show: (v) => v?.action === "ADD" || v?.action === "SUPERSEDE",
              },
              required: {
                name: t("requirementRequired"),
                type: "checkbox",
                show: (v) => v?.action === "ADD" || v?.action === "SUPERSEDE",
              },
            }}
            onSubmit={async (values) => {
              if (!canWrite || pending.current) return
              setError("")
              attempt.current = null
              setDraft(scopeBody(detail, values as ScopeValues))
            }}
            onError={(cause) =>
              setError(
                cause instanceof Error ? cause.message : t("workUnavailable")
              )
            }
          />
          <Button
            type="submit"
            form="requirement-scope-form"
            disabled={busy || !canWrite}
          >
            {t("scopeChange")}
          </Button>
        </>
      )}
      {draft && canWrite && (
        <ReauthenticationDialog
          purpose="scope_change"
          target={`requirement:${detail.id}`}
          onClose={() => {
            if (!pending.current) setDraft(null)
          }}
          onVerified={async (proof) => {
            if (!canWrite || pending.current) return
            pending.current = true
            setBusy(true)
            const request = attempt.current ?? {
              key: crypto.randomUUID(),
              body: { ...draft, reauth_proof: proof },
            }
            attempt.current = request
            try {
              await submitScopeChange(
                path,
                detail.version,
                request.key,
                request.body
              )
              if (active.current) {
                setDraft(null)
                setEditing(false)
                attempt.current = null
                setRevision((v) => v + 1)
                onSaved()
              }
            } finally {
              pending.current = false
              if (active.current) setBusy(false)
            }
          }}
        />
      )}
    </section>
  )
}
