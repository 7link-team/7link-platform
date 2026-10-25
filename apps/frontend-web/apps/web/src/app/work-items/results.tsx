import { RubricScores } from "../templates/rubric-scores"
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react"
import {
  AutoForm,
  type FieldRendererProps,
} from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { PaginationPages } from "@workspace/ui/components/pagination"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { ReviewAttention } from "./attention"
import { UsedKnowledgePanel } from "../knowledge/used-knowledge"
import { WorkSkillDistillation } from "../knowledge/distillation"
import { iamApi, type WorkItemDetail } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { ResultFeedbackPanel } from "./results-feedback"
import { WorkRecovery } from "./recovery"
import {
  isAcceptedResult,
  resultsApi,
  type ResultEvidenceInput,
  type WorkResult,
} from "./results-api"

const EvidenceScope = createContext<{
  entityID: string
  workID: string
  criteria: { id: string | null; text: string }[]
}>({ entityID: "", workID: "", criteria: [] })

function EvidenceField({
  value,
  onChange,
  disabled,
  labelId,
  describedBy,
  error,
  errorId,
}: FieldRendererProps) {
  const t = useTranslations("iam")
  const scope = useContext(EvidenceScope)
  const [resolving, setResolving] = useState(false)
  const [failure, setFailure] = useState("")
  const rows = (Array.isArray(value) ? value : []) as ResultEvidenceInput[]
  const change = (index: number, patch: Partial<ResultEvidenceInput>) =>
    onChange(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)))
  return (
    <div
      role="group"
      aria-labelledby={labelId}
      aria-describedby={describedBy}
      className="grid gap-3"
    >
      <p className="text-sm text-muted-foreground">{t("resultEvidenceHint")}</p>
      {rows.map((row, i) => (
        <fieldset
          key={i}
          disabled={disabled || resolving}
          className="grid gap-2 rounded-md border p-3"
        >
          <legend className="px-1 text-sm">
            {t("resultEvidenceNumber", { number: i + 1 })}
          </legend>
          <label className="grid gap-1 text-sm">
            {t("resultEvidence_file_id")}
            <Input
              required
              value={row.file_id}
              pattern="[1-9][0-9]*"
              onChange={(e) =>
                change(i, {
                  file_id: e.target.value,
                  space_id: "",
                  version_id: "",
                  file_version: 0,
                })
              }
            />
          </label>
          <Button
            type="button"
            variant="outline"
            disabled={!row.file_id || resolving}
            onClick={async () => {
              setResolving(true)
              setFailure("")
              try {
                const pinned = await resultsApi.candidate(
                  scope.entityID,
                  scope.workID,
                  row.file_id
                )
                change(i, {
                  space_id: pinned.space_id,
                  file_id: pinned.file_id,
                  version_id: pinned.version_id,
                  file_version: pinned.file_version,
                })
              } catch (cause) {
                setFailure(
                  cause instanceof Error ? cause.message : t("workUnavailable")
                )
              } finally {
                setResolving(false)
              }
            }}
          >
            {resolving ? t("loading") : t("resultResolveEvidence")}
          </Button>
          {row.version_id && (
            <p className="text-sm break-all">
              {t("resultEvidence_space_id")}: {row.space_id} ·{" "}
              {t("resultEvidence_version_id")}: {row.version_id} ·{" "}
              {t("resultEvidence_file_version")}: {row.file_version}
            </p>
          )}
          <label className="grid gap-1 text-sm">
            {t("resultEvidence_criterion_id")}
            <select
              className="rounded-md border bg-background p-2"
              value={row.criterion_id ?? ""}
              onChange={(e) =>
                change(i, { criterion_id: e.target.value || undefined })
              }
            >
              <option value="">{t("resultGeneralEvidence")}</option>
              {scope.criteria
                .filter((c) => c.id)
                .map((c) => (
                  <option key={c.id} value={c.id!}>
                    {c.text}
                  </option>
                ))}
            </select>
          </label>
          <label className="grid gap-1 text-sm">
            {t("resultCoverage")}
            <select
              className="rounded-md border bg-background p-2"
              value={row.coverage}
              onChange={(e) =>
                change(i, {
                  coverage: e.target.value as ResultEvidenceInput["coverage"],
                })
              }
            >
              {["COVERED", "PARTIAL", "NOT_APPLICABLE"].map((coverage) => (
                <option key={coverage} value={coverage}>
                  {t(`resultCoverage_${coverage}`)}
                </option>
              ))}
            </select>
          </label>
          <label className="grid gap-1 text-sm">
            {t("resultEvidence_note")}
            <Input
              required={row.coverage === "NOT_APPLICABLE"}
              maxLength={2000}
              value={row.note ?? ""}
              onChange={(e) => change(i, { note: e.target.value || undefined })}
            />
          </label>
          <Button
            type="button"
            variant="outline"
            disabled={disabled || rows.length <= 1}
            onClick={() => onChange(rows.filter((_, n) => n !== i))}
          >
            {t("resultRemoveEvidence")}
          </Button>
        </fieldset>
      ))}
      {error && (
        <p role="alert" id={errorId} className="text-sm text-destructive">
          {error}
        </p>
      )}
      {failure && <Alert>{failure}</Alert>}
      <Button
        type="button"
        variant="outline"
        disabled={disabled || resolving || rows.length >= 50}
        onClick={() =>
          onChange([
            ...rows,
            {
              space_id: "",
              file_id: "",
              version_id: "",
              file_version: 0,
              coverage: "COVERED",
            },
          ])
        }
      >
        {t("resultAddEvidence")}
      </Button>
    </div>
  )
}

type Action = {
  kind: "submit" | "check" | "accept" | "handoff" | "receipt" | "return"
  resultID?: string
  sequence?: number
  handoffID?: string
}
type ResultWork = WorkItemDetail & {
  current_claim_id?: string | null
  accepted_at?: string | null
}

function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export function WorkResults({
  entityID,
  detail,
  onUpdated,
  surface = "REVIEW_DESK",
}: {
  entityID: string
  detail: ResultWork
  surface?: "REVIEW_DESK" | "INBOX"
  onUpdated: (detail: WorkItemDetail) => void
}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const allowed = useAllowed()
  const [rows, setRows] = useState<WorkResult[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [selectedID, setSelectedID] = useState("")
  const [subject, setSubject] = useState("")
  const [ownAssignment, setOwnAssignment] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [action, setAction] = useState<Action | null>(null)
  const active = useRef(true)
  const pending = useRef(false)
  const controller = useRef<AbortController | null>(null)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  const canRead = allowed("work_item_read")
  const load = useCallback(async () => {
    if (!canRead) return
    controller.current?.abort()
    const c = new AbortController()
    controller.current = c
    setLoading(true)
    setError("")
    try {
      const [page, work, session, assignments] = await Promise.all([
        resultsApi.list(entityID, detail.id!, offset, c.signal),
        iamApi.workItem(entityID, detail.id!, c.signal),
        iamApi.session(),
        iamApi.workAssignments(detail.id!, c.signal),
      ])
      if (c.signal.aborted || !active.current) return
      setRows(page.items)
      setTotal(page.total)
      setSubject(session.subject)
      setOwnAssignment(
        assignments.items.some(
          (a) =>
            a.assignee_kind === "HUMAN" &&
            a.assignee_principal_id === session.subject &&
            !!a.lease_expires_at &&
            new Date(a.lease_expires_at).getTime() > Date.now()
        )
      )
      if (work.version !== detail.version) onUpdated(work)
    } catch (cause) {
      if (!c.signal.aborted && active.current) {
        setRows([])
        setError(cause instanceof Error ? cause.message : t("workUnavailable"))
      }
    } finally {
      if (!c.signal.aborted && active.current) setLoading(false)
    }
  }, [canRead, entityID, detail.id, detail.version, offset, onUpdated, t])
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
  const selected =
    rows.find((r) => r.id === selectedID) ??
    rows.find((r) => r.id === detail.current_claim_id) ??
    rows[0]
  const currentResult = rows.find((r) => r.id === detail.current_claim_id)
  const canSubmit =
    allowed("work_item_execute") &&
    (detail.current_claim_id
      ? currentResult?.actor_id === subject
      : ownAssignment) &&
    !detail.archived_at &&
    !detail.paused_at &&
    !detail.is_container &&
    [
      "CLAIMED",
      "IN_PROGRESS",
      "REWORK",
      "NEEDS_HUMAN_REVIEW",
      "ACCEPTED",
    ].includes(detail.state)
  const permitted = (kind: Action["kind"]) =>
    kind === "submit"
      ? canSubmit
      : kind === "check"
        ? allowed("work_item_write")
        : ["handoff", "receipt"].includes(kind)
          ? allowed("delivery_send")
          : kind === "return"
            ? allowed("decision_request_changes") &&
              !!subject &&
              selected?.actor_id !== subject
            : allowed("decision_approve")
  const current = selected?.id === detail.current_claim_id
  const accepted = selected && isAcceptedResult(selected, detail)
  const act = (kind: Action["kind"], handoffID?: string) => {
    setError("")
    setNotice("")
    setAction({
      kind,
      resultID: selected?.id,
      sequence: selected?.sequence,
      handoffID,
    })
  }
  const download = async (result: WorkResult, evidenceID: string) => {
    if (pending.current) return
    pending.current = true
    setBusy(true)
    setError("")
    try {
      const blob = await resultsApi.evidence(
        entityID,
        detail.id!,
        result.id,
        evidenceID
      )
      if (active.current) saveBlob(blob, `evidence-${evidenceID}`)
    } catch (cause) {
      if (active.current)
        setError(cause instanceof Error ? cause.message : t("workUnavailable"))
    } finally {
      pending.current = false
      if (active.current) setBusy(false)
    }
  }
  if (!canRead) return null
  return (
    <section className="grid gap-3 border-t pt-4" aria-label={t("resultTitle")}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="font-medium">{t("resultTitle")}</h2>
        <Button
          size="sm"
          variant="outline"
          disabled={busy || loading}
          onClick={() => void load()}
        >
          {t("refresh")}
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">{t("resultManualHint")}</p>
      {detail.id && <WorkSkillDistillation key={`distill:${detail.id}`} work={detail.id} state={detail.state ?? ""}/>}
      {current&&selected&&subject&&detail.id&&allowed("work_item_read")&&detail.state==="NEEDS_HUMAN_REVIEW"&&<ReviewAttention key={`${entityID}:${detail.id}:${selected.id}`} project={entityID} work={detail.id} claim={selected.id} surface={surface}/>}
      {current && detail.id && <RubricScores project={entityID} work={detail.id}/>}
      {error && <Alert>{error}</Alert>}
      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}
      {loading && <p role="status">{t("loading")}</p>}
      {!loading && !error && !rows.length && (
        <p className="text-sm text-muted-foreground">{t("resultEmpty")}</p>
      )}
      {!!rows.length && (
        <>
          <label className="grid gap-1 text-sm">
            {t("resultVersion")}
            <select
              className="rounded-md border bg-background p-2"
              disabled={busy || loading || !!action}
              value={selected?.id ?? ""}
              onChange={(e) => setSelectedID(e.target.value)}
            >
              {rows.map((r) => (
                <option key={r.id} value={r.id}>
                  {t("resultVersionNumber", { number: r.sequence })}
                  {r.id === detail.current_claim_id
                    ? ` · ${t("resultCurrent")}`
                    : ""}{" "}
                  · {new Date(r.created_at).toLocaleString(locale)}
                </option>
              ))}
            </select>
          </label>
          <PaginationPages
            page={Math.floor(offset / 50) + 1}
            totalPages={Math.max(1, Math.ceil(total / 50))}
            disabled={busy || loading || !!action}
            onPageChange={(page) => {
              setOffset((page - 1) * 50)
              setSelectedID("")
            }}
          />
        </>
      )}
      {selected && (
        <>
          <AutoForm
            key={`${selected.id}:${detail.version}`}
            mode="detail"
            initialData={{
              id: selected.id,
              summary: selected.summary,
              actor: selected.actor_id,
              previous: selected.previous_result_id ?? "—",
              rework: selected.rework_action_id ?? "—",
              accepted: accepted ? t("resultAccepted") : t("resultNotAccepted"),
            }}
            fields={{
              id: { name: t("resultID"), type: "text" },
              summary: { name: t("resultSummary"), type: "textarea" },
              actor: { name: t("resultActor"), type: "text" },
              previous: { name: t("resultPrevious"), type: "text" },
              rework: { name: t("resultRework"), type: "text" },
              accepted: { name: t("resultAcceptance"), type: "text" },
            }}
          />
          <p className="text-sm text-muted-foreground">
            {t("resultAcceptanceHint")}
          </p>
          <h3 className="text-sm font-medium">{t("resultEvidence")}</h3>
          <AutoTable
            data={selected.evidence ?? []}
            rowKey="id"
            pagination={false}
            exportable={false}
            columns={[
              { field: "file_id", name: t("resultEvidence_file_id") },
              { field: "version_id", name: t("resultEvidence_version_id") },
              {
                field: "criterion_id",
                name: t("resultEvidence_criterion_id"),
                format: (_, row) => row.criterion_id ?? "—",
              },
              {
                field: "coverage",
                name: t("resultCoverage"),
                format: (_, row) => t(`resultCoverage_${row.coverage}`),
              },
              { field: "note", name: t("resultEvidence_note") },
            ]}
            actions={[
              {
                name: t("resultDownloadEvidence"),
                disabled: () => busy,
                onclick: (row) => void download(selected, row.id),
              },
            ]}
          />
          <h3 className="text-sm font-medium">{t("resultActions")}</h3>
          <AutoTable
            data={selected.actions ?? []}
            rowKey="id"
            pagination={false}
            exportable={false}
            columns={[
              {
                field: "kind",
                name: t("resultAction"),
                format: (_, row) => t(`resultKind_${row.kind}`),
              },
              { field: "actor_id", name: t("resultActor") },
              { field: "reason", name: t("resultReason") },
              {
                field: "verdict",
                name: t("resultVerdict"),
                format: (_, row) =>
                  row.verdict ? t(`resultVerdict_${row.verdict}`) : "—",
              },
              {
                field: "created_at",
                name: t("createdAt"),
                format: (_, row) =>
                  new Date(row.created_at).toLocaleString(locale),
              },
            ]}
          />
          {!action && current && !loading && !error && (
            <div className="flex flex-wrap gap-2">
              {permitted("check") &&
                detail.state === "NEEDS_HUMAN_REVIEW" &&
                selected.actor_id !== subject && (
                  <Button
                    variant="outline"
                    disabled={busy}
                    onClick={() => act("check")}
                  >
                    {t("resultDo_check")}
                  </Button>
                )}
              {permitted("accept") &&
                detail.state === "NEEDS_HUMAN_REVIEW" &&
                selected.actor_id !== subject &&
                !accepted && (
                  <Button
                    variant="outline"
                    disabled={busy}
                    data-inbox-primary
                    onClick={() => act("accept")}
                  >
                    {t("resultDo_accept")}
                  </Button>
                )}
              {permitted("handoff") && accepted && (
                <Button
                  variant="outline"
                  disabled={busy}
                  onClick={() => act("handoff")}
                >
                  {t("resultDo_handoff")}
                </Button>
              )}
            </div>
          )}
          <h3 className="text-sm font-medium">{t("resultPackages")}</h3>
          <p className="text-sm text-muted-foreground">
            {t("resultPackageHint")}
          </p>
          {(selected.handoffs ?? []).map((handoff) => (
            <div key={handoff.id} className="grid gap-2 rounded-md border p-3">
              <p className="text-sm break-all">
                {handoff.id} · {handoff.summary}
              </p>
              <p className="text-xs break-all">SHA-256 {handoff.sha256}</p>
              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() =>
                    saveBlob(
                      new Blob([handoff.manifest], {
                        type: "application/json",
                      }),
                      `manifest-${handoff.id}.json`
                    )
                  }
                >
                  {t("resultDownloadManifest")}
                </Button>
                {!action &&
                  current &&
                  accepted &&
                  !(selected.actions ?? []).some(
                    (a) =>
                      a.handoff_id === handoff.id &&
                      ["RECEIPT", "RETURN"].includes(a.kind)
                  ) && (
                    <>
                      {permitted("receipt") && handoff.actor_id !== subject && (
                        <Button
                          size="sm"
                          disabled={busy || loading}
                          onClick={() => act("receipt", handoff.id)}
                        >
                          {t("resultDo_receipt")}
                        </Button>
                      )}
                      {permitted("return") && handoff.actor_id !== subject && (
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={busy || loading}
                          onClick={() => act("return", handoff.id)}
                        >
                          {t("resultDo_return")}
                        </Button>
                      )}
                    </>
                  )}
              </div>
            </div>
          ))}
        </>
      )}
      {!action && canSubmit && !loading && !error && (
        <Button disabled={busy} onClick={() => act("submit")}>
          {t("resultDo_submit")}
        </Button>
      )}
      {action && (
        <div className="grid gap-3 rounded-md border p-3">
          <h3 className="font-medium">{t(`resultDo_${action.kind}`)}</h3>
          {action.kind !== "submit" && (
            <p className="text-sm">
              {t("resultExactVersion", { number: action.sequence ?? 0 })}{" "}
              <span className="break-all">{action.resultID}</span>
            </p>
          )}
          {action.kind === "check" && (
            <p className="text-sm text-muted-foreground">
              {t("resultIndependentHint")}
            </p>
          )}
          <EvidenceScope.Provider
            value={{
              entityID,
              workID: detail.id!,
              criteria: detail.criteria ?? [],
            }}
          >
            <AutoForm
              key={`${action.kind}:${action.resultID}:${action.handoffID}`}
              mode="edit"
              initialData={{
                summary: "",
                reason: "",
                verdict: "INCONCLUSIVE",
                evidence: [
                  {
                    space_id: "",
                    file_id: "",
                    version_id: "",
                    file_version: 0,
                    coverage: "COVERED",
                  },
                ],
              }}
              fields={{
                summary: {
                  name: t("resultSummary"),
                  type: "textarea",
                  show: ["submit", "handoff"].includes(action.kind),
                  required: ["submit", "handoff"].includes(action.kind),
                  config: { maxLength: 2000 },
                },
                evidence: {
                  name: t("resultEvidence"),
                  type: "custom",
                  component: EvidenceField,
                  show: action.kind === "submit",
                  required: action.kind === "submit",
                },
                verdict: {
                  name: t("resultVerdict"),
                  type: "select",
                  show: action.kind === "check",
                  required: action.kind === "check",
                  config: {
                    options: ["PASSED", "FAILED", "INCONCLUSIVE"].map(
                      (value) => ({ value, label: t(`resultVerdict_${value}`) })
                    ),
                  },
                },
                reason: {
                  name: t("resultReason"),
                  type: "textarea",
                  show: !["submit", "handoff"].includes(action.kind),
                  required: !["submit", "handoff"].includes(action.kind),
                  config: { maxLength: 2000 },
                },
              }}
              onSubmit={async (values) => {
                if (pending.current || !permitted(action.kind)) return
                if (
                  action.kind === "submit" &&
                  (!Array.isArray(values.evidence) ||
                    !values.evidence.length ||
                    (values.evidence as ResultEvidenceInput[]).some(
                      (e) =>
                        !e.space_id ||
                        !e.version_id ||
                        !Number.isSafeInteger(e.file_version) ||
                        e.file_version < 1
                    ))
                ) {
                  setError(t("resultResolveRequired"))
                  return
                }
                const suffix =
                  action.kind === "submit"
                    ? ""
                    : `/${encodeURIComponent(action.resultID!)}/${action.kind === "check" ? "checks" : action.kind === "accept" ? "acceptance" : action.kind === "handoff" ? "handoffs" : `handoffs/${encodeURIComponent(action.handoffID!)}/receipt`}`
                const body =
                  action.kind === "submit"
                    ? {
                        summary: String(values.summary).trim(),
                        evidence: values.evidence,
                      }
                    : action.kind === "check"
                      ? {
                          reason: String(values.reason).trim(),
                          verdict: values.verdict,
                        }
                      : action.kind === "handoff"
                        ? { summary: String(values.summary).trim() }
                        : action.kind === "accept"
                          ? { reason: String(values.reason).trim() }
                          : {
                              accepted: action.kind === "receipt",
                              reason: String(values.reason).trim(),
                            }
                const identity = JSON.stringify({
                  suffix,
                  version: detail.version,
                  body,
                })
                const key =
                  attempt.current?.identity === identity
                    ? attempt.current.key
                    : crypto.randomUUID()
                attempt.current = { identity, key }
                pending.current = true
                setBusy(true)
                setError("")
                try {
                  const result = await resultsApi.mutate(
                    entityID,
                    detail.id!,
                    suffix,
                    detail.version,
                    key,
                    body
                  )
                  attempt.current = null
                  if (!active.current) return
                  setAction(null)
                  setSelectedID(result.id)
                  if (action.kind === "submit") setOffset(0)
                  setNotice(t("resultSaved"))
                  const work = await iamApi.workItem(entityID, detail.id!)
                  if (active.current) {
                    onUpdated(work)
                    await load()
                  }
                } catch (cause) {
                  if (active.current)
                    setError(
                      cause instanceof Error
                        ? cause.message
                        : t("workUnavailable")
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
                  onClick={() => setAction(null)}
                >
                  {t("cancel")}
                </Button>
                <Button type="submit" disabled={busy}>
                  {busy ? t("saving") : t(`resultDo_${action.kind}`)}
                </Button>
              </div>
            </AutoForm>
          </EvidenceScope.Provider>
        </div>
      )}
      {selected && (
        <WorkRecovery
          key={`result-recovery:${selected.id}`}
          entityID={entityID}
          detail={detail}
          result={selected}
          subject={subject}
          onUpdated={onUpdated}
        />
      )}
      {selected && (
        <ResultFeedbackPanel
          key={selected.id}
          entityID={entityID}
          workID={detail.id!}
          resultID={selected.id}
          sequence={selected.sequence}
          inboxSecondary={surface === "INBOX" && current && detail.state === "NEEDS_HUMAN_REVIEW"}
        />
      )}
      {selected && <UsedKnowledgePanel key={`knowledge:${selected.id}`} resultID={selected.id} compact/>}
    </section>
  )
}
