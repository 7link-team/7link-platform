import { useEffect, useRef, useState } from "react"
import { AutoForm, type FieldConfig } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import {
  reviewSheetApi,
  type ReviewType,
  type ReviewTypeSettings,
  type ReviewPage,
} from "./api"
import { reviewSheetMessages } from "./messages"

const api = reviewSheetApi()
export function ReviewTypes({
  tenant,
  project,
  manage,
  onTypes,
}: {
  tenant: string
  project?: string
  manage: boolean
  onTypes: (types: ReviewType[]) => void
}) {
  const locale = useLocale(),
    l =
      reviewSheetMessages[locale as keyof typeof reviewSheetMessages] ??
      reviewSheetMessages["en-US"]
  const [localCopy, setLocalCopy] = useState(false)
  const [page, setPage] = useState<ReviewPage<ReviewType> | null>(null),
    [cursors, setCursors] = useState([""]),
    cursor = cursors[cursors.length - 1]!,
    [revision, setRevision] = useState(0),
    [error, setError] = useState(""),
    [editing, setEditing] = useState<ReviewType | null | undefined>(undefined),
    [busy, setBusy] = useState(false),
    [loaded, setLoaded] = useState("")
  const pending = useRef<{ signature: string; key: string } | null>(null),
    identity = `${tenant}:${cursor}:${revision}:${localCopy}:${project}`,
    loading = loaded !== identity
  useEffect(() => {
    const c = new AbortController()
    void api
      .types(tenant, cursor, c.signal, localCopy ? project : undefined)
      .then((v) => {
        if (!c.signal.aborted) {
          setPage(v)
          if (!project) onTypes(v.items)
          else {
            void api
              .types(tenant, cursor, c.signal, project)
              .then((resolved) => {
                if (!c.signal.aborted) onTypes(resolved.items)
              })
              .catch((e) => {
                if (!c.signal.aborted) {
                  onTypes([])
                  setError(e instanceof Error ? e.message : l.error)
                }
              })
          }
          setError("")
          setLoaded(identity)
        }
      })
      .catch((e) => {
        if (!c.signal.aborted) {
          setPage(null)
          onTypes([])
          setError(e instanceof Error ? e.message : l.error)
          setLoaded(identity)
        }
      })
    return () => c.abort()
  }, [tenant, cursor, revision, onTypes, identity, l.error, localCopy, project])
  const fields: Record<string, FieldConfig> = {
    propagate: { name: l.propagate, type: "switch", value: true },
    work_item_types: { name: l.workItemTypes, type: "text" },
    work_nodes: { name: l.workNodes, type: "text" },
    name: {
      name: l.reviewType,
      type: "text",
      required: true,
      config: { maxLength: 60 },
    },
    draft: { name: l.draftNode, type: "switch", value: true },
    baselined: { name: l.baselinedNode, type: "switch", value: true },
    reviewers: { name: l.defaultReviewers, type: "textarea" },
    minimum: { name: l.minimum, type: "number", value: 1, required: true },
    description: { name: l.includeDescription, type: "switch", value: true },
    per_ac_required: { name: l.requireCriterion, type: "switch", value: true },
  }
  if (localCopy) delete fields.propagate
  return (
    <section className="min-w-0 space-y-3 border-t pt-5">
      <h2 className="font-semibold">{l.typeSettings}</h2>
      <p className="text-sm text-muted-foreground">{l.typePolicy}</p>
      {project && (
        <label className="flex min-h-11 items-center gap-3 text-sm">
          <input
            type="checkbox"
            checked={localCopy}
            onChange={(e) => {
              setLocalCopy(e.target.checked)
              setEditing(undefined)
              setCursors([""])
            }}
          />
          {l.projectCopy}
        </label>
      )}
      {error && <Alert>{error}</Alert>}
      <div className="flex gap-2">
        <Button
          variant="outline"
          disabled={loading || busy}
          onClick={() => setRevision((v) => v + 1)}
        >
          {l.refresh}
        </Button>
        {manage && !localCopy && (
          <Button disabled={loading || busy} onClick={() => setEditing(null)}>
            {l.addType}
          </Button>
        )}
      </div>
      <AutoTable
        data={loading ? [] : (page?.items ?? [])}
        loading={loading}
        rowKey="id"
        pagination={false}
        exportable={false}
        emptyContent={l.noTypes}
        columns={[
          { field: "id", name: "ID" },
          {
            field: "settings",
            name: l.reviewType,
            render: (_, row) => row.settings.name,
          },
          { field: "version", name: l.typeVersion },
        ]}
        actions={
          manage
            ? [{ name: l.editType, onclick: (row) => setEditing(row) }]
            : []
        }
      />
      <div className="flex justify-end gap-2">
        <Button
          variant="outline"
          disabled={loading || cursors.length === 1}
          onClick={() => setCursors((v) => v.slice(0, -1))}
        >
          {l.previous}
        </Button>
        <Button
          variant="outline"
          disabled={loading || !page?.has_more || !page.next_cursor}
          onClick={() => setCursors((v) => [...v, page!.next_cursor!])}
        >
          {l.next}
        </Button>
      </div>
      {manage && editing !== undefined && (
        <AutoForm
          key={editing ? `${editing.id}:${editing.version}` : "new"}
          fields={fields}
          mode="edit"
          initialData={
            editing
              ? {
                  name: editing.settings.name,
                  propagate: editing.settings.propagate,
                  work_item_types:
                    editing.settings.applies_to.work_item_types.join(", "),
                  work_nodes: editing.settings.review_nodes
                    .filter((n) => !["DRAFT", "BASELINED"].includes(n))
                    .join(", "),
                  draft: editing.settings.review_nodes.includes("DRAFT"),
                  baselined:
                    editing.settings.review_nodes.includes("BASELINED"),
                  reviewers: editing.settings.default_reviewer_ids.join(", "),
                  minimum: editing.settings.min_reviewers,
                  description:
                    editing.settings.content_fields.includes("description"),
                  per_ac_required: editing.settings.per_ac_required,
                }
              : undefined
          }
          onSubmit={async (values) => {
            if (busy) return
            const reviewers = String(values.reviewers ?? "")
                .split(/[,\s]+/)
                .filter(Boolean),
              minimum = Number(values.minimum)
            if (
              reviewers.length > 30 ||
              reviewers.some((id) => !/^[1-9][0-9]{0,18}$/.test(id)) ||
              new Set(reviewers).size !== reviewers.length ||
              !Number.isInteger(minimum) ||
              minimum < 1 ||
              minimum > 10
            )
              throw new Error(l.invalid)
            const settings: ReviewTypeSettings = {
              name: String(values.name ?? ""),
              applies_to: {
                requirement: editing?.settings.applies_to.requirement ?? true,
                work_item_types: String(values.work_item_types ?? "")
                  .split(/[,\s]+/)
                  .filter(Boolean),
              },
              review_nodes: [
                ...(values.draft ? ["DRAFT"] : []),
                ...(values.baselined ? ["BASELINED"] : []),
                ...String(values.work_nodes ?? "")
                  .split(/[,\s]+/)
                  .filter(Boolean),
              ],
              default_reviewer_ids: reviewers,
              min_reviewers: minimum,
              content_fields: [
                ...(editing?.settings.content_fields.filter(
                  (field) => field !== "description"
                ) ?? ["title", "criteria"]),
                ...(values.description ? ["description"] : []),
              ],
              per_ac_required: values.per_ac_required === true,
              propagate: values.propagate === true,
            }
            const signature = JSON.stringify([
              localCopy ? project : "team",
              editing?.id,
              editing?.version,
              settings,
            ])
            if (pending.current?.signature !== signature)
              pending.current = { signature, key: crypto.randomUUID() }
            setBusy(true)
            setError("")
            try {
              await api.saveType(
                tenant,
                editing ?? null,
                settings,
                pending.current.key,
                localCopy ? project : undefined
              )
              pending.current = null
              setEditing(undefined)
              setRevision((v) => v + 1)
            } catch (e) {
              setError(e instanceof Error ? e.message : l.error)
              throw e
            } finally {
              setBusy(false)
            }
          }}
          onError={(e) => setError(e instanceof Error ? e.message : l.error)}
        >
          <div className="flex gap-2">
            <Button type="submit" disabled={busy}>
              {l.save}
            </Button>
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={() => setEditing(undefined)}
            >
              {l.cancel}
            </Button>
          </div>
        </AutoForm>
      )}
    </section>
  )
}
