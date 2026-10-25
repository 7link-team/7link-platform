import { useEffect, useMemo, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { PageHeader } from "../../components/page-header"
import { Alert } from "../../components/alert"
import { useAllowed } from "../../lib/permissions"
import { iamApi } from "../../lib/iam-api"

type Escape = {
  id: string
  project_id: string
  work_item_id: string
  stage: string
  category: string
  summary: string
  learning_state: string
  version: number
  passed_by: {
    stage: string
    principal_id: string
    model_family: string
    verifier_version: string
  }[]
  memory_ids: string[]
  eval_case_ref?: string
  decision_reason?: string
  learning_draft?: { title: string; body: string }
}
type EscapeSummary = {
  window_start: string
  window_end: string
  escaped_problems: number
  entered_human_review: number
  rate: number | null
}
export default function QualityEscapesPage() {
  const { tenantID = "", entityID = "" } = useParams()
  return (
    <QualityEscapes
      key={`${tenantID}:${entityID}`}
      tenantID={tenantID}
      entityID={entityID}
    />
  )
}
function QualityEscapes({
  tenantID,
  entityID,
}: {
  tenantID: string
  entityID: string
}) {
  const t = useTranslations("iam"),
    allowed = useAllowed()
  const [search, setSearch] = useSearchParams()
  const root = `/tenants/${encodeURIComponent(tenantID)}/quality-escapes`
  const [page, setPage] = useState<{ items: Escape[]; total: number } | null>(
    null
  )
  const [selected, setSelected] = useState<Escape | null>(null)
  const [summary, setSummary] = useState<EscapeSummary | null>(null)
  const [summaryError, setSummaryError] = useState("")
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [reason, setReason] = useState(""),
    [revision, setRevision] = useState(0)
  const [ruleDraft, setRuleDraft] = useState(false),
    [ruleTitle, setRuleTitle] = useState(""),
    [ruleBody, setRuleBody] = useState("")
  const read = allowed("audit_read"),
    manage = allowed("policy_manage")
  const id = search.get("escape_id") ?? "",
    state = search.get("learning_state") ?? "",
    project = entityID || search.get("project_id") || ""
  const offset = Math.max(0, Number(search.get("offset") ?? 0) || 0)
  const query = useMemo(() => {
    const q = new URLSearchParams({ limit: "50", offset: String(offset) })
    if (state) q.set("learning_state", state)
    if (project) q.set("project_id", project)
    return q.toString()
  }, [state, project, offset])
  useEffect(() => {
    if (!read) return
    const c = new AbortController()
    iamApi
      .deliveryResultPage<Escape>(
        { method: "GET", url: `${root}?${query}` },
        c.signal
      )
      .then((value) => {
        setPage(value)
        setError("")
      })
      .catch((e) => {
        if (!c.signal.aborted) setError(e.message)
      })
    return () => c.abort()
  }, [read, root, query, revision])
  useEffect(() => {
    if (!read || !project) return
    const c = new AbortController()
    iamApi.deliveryResultRequest<EscapeSummary>(
      {method: "GET", url: `/tenants/${encodeURIComponent(tenantID)}/quality-escape-summary?project_id=${encodeURIComponent(project)}`},
      {signal: c.signal}
    ).then(value => {setSummary(value); setSummaryError("")}).catch(e => {
      if (!c.signal.aborted) {setSummary(null); setSummaryError(e.message)}
    })
    return () => c.abort()
  }, [read, project, tenantID, revision])
  useEffect(() => {
    if (!read || !id) return
    const c = new AbortController()
    iamApi
      .deliveryResultRequest<Escape>(
        { method: "GET", url: `${root}/${encodeURIComponent(id)}` },
        { signal: c.signal }
      )
      .then(setSelected)
      .catch((e) => {
        if (!c.signal.aborted) setError(e.message)
      })
    return () => c.abort()
  }, [read, id, root, revision])
  const choose = (value: string) => {
    setSelected(null)
    setReason("")
    setRuleDraft(false)
    setRuleTitle("")
    setRuleBody("")
    const q = new URLSearchParams(search)
    if (value) q.set("escape_id", value)
    else q.delete("escape_id")
    setSearch(q)
  }
  const decide = async (state: string) => {
    if (!selected || busy) return
    setBusy(true)
    setError("")
    try {
      await iamApi.deliveryResultRequest<Escape>(
        { method: "PUT", url: `${root}/${selected.id}/learning` },
        {
          headers: {
            "If-Match": `"${selected.version}"`,
            "Idempotency-Key": crypto.randomUUID(),
          },
          body: JSON.stringify({
            state,
            reason,
            ...(state === "RECORDED" && ruleDraft
              ? { rule_draft: { title: ruleTitle, body: ruleBody } }
              : {}),
          }),
        }
      )
      setPage(null)
      setSelected(null)
      setRevision((v) => v + 1)
    } catch (e) {
      setError(e instanceof Error ? e.message : t("govUnavailable"))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="space-y-6">
      <PageHeader
        title={t("qualityEscapesTitle")}
        description={t("qualityEscapesHint")}
      />
      {error && <Alert>{error}</Alert>}
      {!read ? (
        <Alert>{t("entranceAccessDenied")}</Alert>
      ) : (
        <>
          <label className="flex flex-wrap items-center gap-3">
            {t("qualityLearningState")}
            <select
              className="rounded-md border bg-background p-2"
              value={state}
              onChange={(e) => {
                setPage(null)
                const q = new URLSearchParams(search)
                q.delete("offset")
                if (e.target.value) q.set("learning_state", e.target.value)
                else q.delete("learning_state")
                setSearch(q)
              }}
            >
              <option value="">{t("all")}</option>
              {["PENDING", "RECORDED", "DISMISSED"].map((s) => (
                <option key={s}>{s}</option>
              ))}
            </select>
          </label>
          <div className="grid min-w-0 gap-6 lg:grid-cols-12">
          <section className="min-w-0 space-y-4 rounded-xl border bg-card p-5 lg:col-span-4">
            <h2 className="font-semibold">{t("qualityRateTitle")}</h2>
            {!project ? <p className="text-sm text-muted-foreground">{t("qualityRateProject")}</p> : summaryError ? <Alert>{summaryError}</Alert> : !summary ? <p role="status">{t("loading")}</p> : <>
              <dl className="space-y-4 text-sm">
                <div><dt className="text-muted-foreground">{t("qualityRateCount")}</dt><dd className="mt-1 text-2xl font-semibold tabular-nums">{summary.escaped_problems} / {summary.entered_human_review}</dd></div>
                <div><dt className="text-muted-foreground">{t("qualityRateLabel")}</dt><dd className="mt-1 text-3xl font-semibold tabular-nums">{summary.rate === null ? "—" : `${(summary.rate * 100).toFixed(1)}%`}</dd></div>
              </dl>
              <p className="text-xs text-muted-foreground">{summary.window_start.slice(0,10)} – {summary.window_end.slice(0,10)} UTC</p>
              <p className="text-sm text-muted-foreground">{t("qualityRateHint")}</p>
              {summary.rate === null && <p className="text-sm text-muted-foreground">{t("qualityRateNoReview")}</p>}
            </>}
          </section>
          <section className="min-w-0 space-y-4 rounded-xl border bg-card p-5 lg:col-span-8">
          <h2 className="font-semibold">{t("qualityEscapesTitle")}</h2>
          {!page ? (
            !error && <p role="status">{t("loading")}</p>
          ) : (
            <>
              <AutoTable
                data={page.items}
                pagination={false}
                exportable={false}
                columns={[
                  { field: "project_id", name: t("delegationProject") },
                  { field: "work_item_id", name: t("qualityWorkItem") },
                  { field: "stage", name: t("qualityStage") },
                  { field: "category", name: t("qualityCategory") },
                  { field: "learning_state", name: t("qualityLearningState") },
                  { field: "summary", name: t("govReason") },
                ]}
                rowKey="id"
                loading={false}
                actions={[
                  { name: t("view"), onclick: (row) => choose(row.id) },
                ]}
              />
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  disabled={offset === 0}
                  onClick={() => {
                    setPage(null)
                    const q = new URLSearchParams(search)
                    q.set("offset", String(Math.max(0, offset - 50)))
                    setSearch(q)
                  }}
                >
                  {t("previous")}
                </Button>
                <Button
                  variant="outline"
                  disabled={offset + 50 >= page.total}
                  onClick={() => {
                    setPage(null)
                    const q = new URLSearchParams(search)
                    q.set("offset", String(offset + 50))
                    setSearch(q)
                  }}
                >
                  {t("next")}
                </Button>
              </div>
            </>
          )}
          </section>
          <section className="min-w-0 space-y-3 rounded-xl border bg-card p-5 lg:col-span-12">
            <h2 className="font-semibold">{t("qualityLearningLoop")}</h2>
            <p className="text-sm text-muted-foreground">{t("qualityLearningLoopHint")}</p>
          </section>
          </div>
          {selected && selected.id === id && (
            <section className="space-y-4 border-t pt-6">
              <div className="flex gap-3">
                <h2 className="grow font-semibold">{selected.summary}</h2>
                <Button variant="outline" onClick={() => choose("")}>
                  {t("close")}
                </Button>
              </div>
              <p className="text-sm">
                {selected.work_item_id} / {selected.stage} / {selected.category}{" "}
                / {selected.learning_state}
              </p>
              <h3 className="font-medium">{t("qualityPassedBy")}</h3>
              {selected.passed_by.map((gate, i) => (
                <p key={i} className="text-sm break-words">
                  {gate.stage} / {gate.principal_id} / {gate.model_family} /{" "}
                  {gate.verifier_version}
                </p>
              ))}
              <h3 className="font-medium">{t("qualityLearningDrafts")}</h3>
              {selected.learning_draft && (
                <div className="space-y-2">
                  <p className="font-medium">{selected.learning_draft.title}</p>
                  <p className="whitespace-pre-wrap break-words text-sm">{selected.learning_draft.body}</p>
                  <p className="text-sm text-muted-foreground">{t("qualityRuleHint")}</p>
                </div>
              )}
              <p className="text-sm break-words">
                {selected.memory_ids.join(", ") || t("qualityDraftsPending")}
              </p>
              {allowed("memory_approve") && selected.memory_ids.map(entryID => (
                <Link
                  key={entryID}
                  className="text-sm underline"
                  to={`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(selected.project_id)}/governance/memory-review?entry_id=${encodeURIComponent(entryID)}`}
                >
                  {t("qualityReviewRules")} {entryID}
                </Link>
              ))}
              {selected.eval_case_ref && (
                <p className="text-sm break-words">{selected.eval_case_ref}</p>
              )}
              {selected.decision_reason && (
                <p className="whitespace-pre-wrap">
                  {selected.decision_reason}
                </p>
              )}
              {manage && selected.learning_state === "PENDING" && (
                <div className="space-y-3">
                  <label className="block">
                    {t("govReason")}
                    <textarea
                      className="mt-2 block min-h-24 w-full rounded-md border bg-background p-3"
                      value={reason}
                      maxLength={2000}
                      onChange={(e) => setReason(e.target.value)}
                    />
                  </label>
                  {allowed("knowledge_write") && (
                    <fieldset className="space-y-3 rounded-md border p-4">
                      <label className="flex items-center gap-2">
                        <input
                          type="checkbox"
                          checked={ruleDraft}
                          onChange={(e) => {
                            setRuleDraft(e.target.checked)
                            if (e.target.checked && selected.learning_draft) {
                              setRuleTitle(selected.learning_draft.title)
                              setRuleBody(selected.learning_draft.body)
                            }
                          }}
                        />
                        {t("qualityProposeRule")}
                      </label>
                      <p className="text-sm text-muted-foreground">
                        {t("qualityRuleHint")}
                      </p>
                      {ruleDraft && (
                        <>
                          <label className="block">
                            {t("qualityRuleTitle")}
                            <input
                              className="mt-1 block w-full rounded-md border bg-background p-2"
                              value={ruleTitle}
                              maxLength={200}
                              onChange={(e) => setRuleTitle(e.target.value)}
                            />
                          </label>
                          <label className="block">
                            {t("qualityRuleBody")}
                            <textarea
                              className="mt-1 block min-h-32 w-full rounded-md border bg-background p-2"
                              value={ruleBody}
                              maxLength={8000}
                              onChange={(e) => setRuleBody(e.target.value)}
                            />
                          </label>
                        </>
                      )}
                    </fieldset>
                  )}
                  <div className="flex flex-wrap gap-2">
                    <Button
                      disabled={
                        busy ||
                        (ruleDraft && (!ruleTitle.trim() || !ruleBody.trim()))
                      }
                      onClick={() => decide("RECORDED")}
                    >
                      {t("qualityRecordLearning")}
                    </Button>
                    <Button
                      variant="destructive"
                      disabled={busy || !reason.trim()}
                      onClick={() => decide("DISMISSED")}
                    >
                      {t("qualityDismissLearning")}
                    </Button>
                  </div>
                </div>
              )}
            </section>
          )}
        </>
      )}
    </div>
  )
}
