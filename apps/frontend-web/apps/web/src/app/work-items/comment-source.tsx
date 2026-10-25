import { useEffect, useState } from "react"
import { Link, useParams } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { ApiError, iamApi } from "../../lib/iam-api"
import { FrozenArtifactAnchor, type ArtifactAnchor } from "../executions/artifact-comments"

export type CommentSource = { source_comment_thread_id: string; execution_id: string; source_first_body: string; found_in_work_item_id: string; source_anchor_snapshot: ArtifactAnchor; steps: string; expected: string; actual: string; found_stage: string }
export function WorkCommentSource({ entityID, workID }: { entityID: string; workID: string }) {
  const { tenantID = "" } = useParams()
  const [source, setSource] = useState<CommentSource | null>(null)
  const [error, setError] = useState("")
  useEffect(() => {
    const controller = new AbortController()
    void iamApi.deliveryResultRequest<CommentSource>({ method: "GET", url: `/entities/${encodeURIComponent(entityID)}/work-items/${encodeURIComponent(workID)}/comment-source` }, { signal: controller.signal })
      .then(value => { if (!controller.signal.aborted) setSource(value) })
      .catch(cause => { if (!controller.signal.aborted && !(cause instanceof ApiError && cause.status === 404)) setError(cause instanceof Error ? cause.message : String(cause)) })
    return () => controller.abort()
  }, [entityID, workID])
  if (error) return <Alert>{error}</Alert>
  if (!source) return null
  return <WorkCommentSourceSnapshot source={source} tenantID={tenantID} entityID={entityID} />
}
export function WorkCommentSourceSnapshot({ source, tenantID, entityID }: { source: CommentSource; tenantID: string; entityID: string }) {
  const t = useTranslations("iam")
  return <section className="grid min-w-0 gap-3 border-t pt-4" aria-label={t("artifactCommentProvenance")}>
    <h3 className="font-semibold">{t("artifactCommentProvenance")}</h3><FrozenArtifactAnchor anchor={source.source_anchor_snapshot} />
    <dl className="grid gap-3 text-sm"><div><dt className="font-medium">{t("artifactCommentBody")}</dt><dd className="whitespace-pre-wrap break-words">{source.source_first_body}</dd></div><div><dt className="font-medium">{t("artifactCommentExpected")}</dt><dd className="whitespace-pre-wrap break-words">{source.expected}</dd></div></dl>
    <Link className="min-h-11 py-2 text-sm underline" to={`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(entityID)}/executions/${encodeURIComponent(source.execution_id)}#artifact-comment-${encodeURIComponent(source.source_comment_thread_id)}`}>{t("artifactCommentOpenSource")}</Link>
    <Link className="min-h-11 py-2 text-sm underline" to={`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(entityID)}/work-items?work=${encodeURIComponent(source.found_in_work_item_id)}`}>{t("artifactCommentSourceWork")}</Link>
    <p className="text-sm text-muted-foreground">{t("artifactCommentFrozenHint")}</p>
  </section>
}
