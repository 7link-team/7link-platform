import { useEffect, useState } from "react"
import { Link, useParams } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAllowed } from "../../lib/permissions"
import { iamApi } from "../../lib/iam-api"
import {readUsedKnowledge, type UsedKnowledge} from "./source-api"


export function UsedKnowledgePanel({executionID = "", resultID = "", compact = false}: {executionID?: string; resultID?: string; compact?: boolean}) {
  const {tenantID = ""} = useParams()
  const allowed = useAllowed(), t = useTranslations("iam")
  const [state, setState] = useState<{execution: string; data?: UsedKnowledge; error?: string}>({execution: ""})
  const [retry, setRetry] = useState(0)
  const canRead = allowed("knowledge_read")
  const identity = resultID ? `${tenantID}:result:${resultID}` : `${tenantID}:execution:${executionID}`
  useEffect(() => {
    if (!canRead || (!executionID && !resultID) || !tenantID) return
    const controller = new AbortController()
    const request = resultID ? iamApi.deliveryResultRequest<UsedKnowledge>({method: "GET", url: `/tenants/${encodeURIComponent(tenantID)}/knowledge/results/${encodeURIComponent(resultID)}`}, {signal: controller.signal, cache: "no-store"}) : readUsedKnowledge(tenantID, executionID, controller.signal)
    void request.then((data) => {
      if (!controller.signal.aborted) setState({execution: identity, data})
    }).catch((error: unknown) => {if (!controller.signal.aborted) setState({execution: identity, error: error instanceof Error ? error.message : t("usedKnowledgeUnavailable")})})
    return () => controller.abort()
  }, [tenantID, executionID, resultID, identity, canRead, t, retry])
  if (!canRead) return null
  if (state.execution !== identity) return <p role="status">{t("loading")}</p>
  if (state.error) return <Alert>{state.error}<button className="ml-3 min-h-11 underline" onClick={() => setRetry((value) => value + 1)}>{t("refresh")}</button></Alert>
  return <UsedKnowledgeContent data={state.data ?? {available: false}} compact={compact}/>
}

export function UsedKnowledgeContent({data, compact = false}: {data: UsedKnowledge; compact?: boolean}) {
  const t = useTranslations("iam"), {tenantID = ""} = useParams()
  const [showAll, setShowAll] = useState(false)
  if (!data.available || !data.retrieval) return null
  const retrieval = data.retrieval, feedback = data.feedback ?? [], all = [...retrieval.rules, ...retrieval.items]
  const cited = all.filter((item) => item.cited).length + feedback.filter((item) => item.cited).length
  return <details className="rounded-lg border p-4">
    <summary className="min-h-11 cursor-pointer py-2 font-medium">{t("usedKnowledgeTitle")} · {all.length + feedback.length} · {t("usedKnowledgeCited")} {cited}</summary>
    <div className="grid gap-4 pt-3">
      {data.execution_budget && <p className="text-sm text-muted-foreground">{t("usedKnowledgeExecutionBudget")}: {data.execution_budget}</p>}
      {data.scores_available === false && <p role="status" className="text-sm text-muted-foreground">{t("usedKnowledgeScoresUnavailable")}</p>}
      {retrieval.mode === "K2_FALLBACK" && <p role="status" className="text-sm text-muted-foreground">{t("usedKnowledgeFallback")}</p>}
      <p className="text-sm text-muted-foreground">{t("usedKnowledgeBudget")}: {retrieval.token_budget} + 1500 · {t("usedKnowledgeTokens")}: {retrieval.tokens_total}</p>
      {retrieval.rules_omitted > 0 && <Alert>{t("usedKnowledgeRulesOmitted")}: {retrieval.rules_omitted}</Alert>}
      {feedback.length > 0 && <section className="grid gap-3"><h3 className="font-medium">{t("usedKnowledgeFeedback")}</h3>
        {feedback.slice(0, compact && !showAll ? 5 : undefined).map((item) => <article className="grid min-w-0 gap-2 border-t pt-3" key={item.label}>
          <strong>{item.label} {item.category}</strong><p className="text-sm">{item.cited ? t("usedKnowledgeCited") : t("usedKnowledgeNotCited")}</p>
          {(!compact || showAll) && <><p className="break-words text-sm">{item.location}</p><p className="break-words text-sm">{item.expected}</p><p className="break-words text-sm">{item.detail}</p><p className="break-words text-xs text-muted-foreground">{t("usedKnowledgeDecision")}: {item.source_decision_id}{item.actor_id ? ` / ${item.actor_id}` : ""}{item.decided_at ? ` / ${item.decided_at}` : ""}</p></>}
        </article>)}
      </section>}
      {([{name: t("usedKnowledgeRules"), items: retrieval.rules}, {name: t("usedKnowledgeReferences"), items: retrieval.items}]).map((group) => <section className="grid gap-3" key={group.name}>
        <h3 className="font-medium">{group.name}</h3>
        {group.items.filter((item) => !compact || showAll || all.indexOf(item) + feedback.length < 5).map((item) => <article className="grid min-w-0 gap-2 border-t pt-3" key={item.label}>
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-2">
            <strong className="break-words">{item.label} {item.title}</strong>
            {item.source_id && <Link className="min-h-11 py-2 text-sm underline" to={`/admin/${encodeURIComponent(tenantID)}/knowledge/sources/${encodeURIComponent(item.source_id)}`}>{t("usedKnowledgeSource")}</Link>}
          </div>
          <p className="break-words text-sm text-muted-foreground">{item.source_kind} · {item.scope} · {item.tokens} {t("usedKnowledgeTokens")} · {t(`usedKnowledgeInclusion_${item.inclusion}`)}{item.label.startsWith("[K") && data.scores_available !== false ? ` · ${Math.round(item.score_ppm / 10000)}%` : ""}</p>
          <p className="text-sm">{item.cited ? t("usedKnowledgeCited") : t("usedKnowledgeNotCited")}{item.citation_outcome ? ` · ${t(`usedKnowledgeOutcome_${item.citation_outcome}`)}` : ""}</p>
          {item.truncated && <p className="text-sm text-muted-foreground">{t("usedKnowledgeTruncated")}</p>}
          {item.source_changed && <p className="text-sm text-muted-foreground">{t("usedKnowledgeSourceChanged")}</p>}
          {(!compact || showAll) && <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words text-sm">{item.body}</pre>}
          <code className="break-all text-xs text-muted-foreground">sha256:{item.content_hash}</code>
        </article>)}
      </section>)}
      {compact && !showAll && all.length + feedback.length > 5 && <button className="min-h-11 w-fit underline" onClick={() => setShowAll(true)}>{t("usedKnowledgeShowAll")}</button>}
    </div>
  </details>
}
