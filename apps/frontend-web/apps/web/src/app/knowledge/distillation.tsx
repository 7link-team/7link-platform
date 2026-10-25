import { useRef, useState } from "react"
import { Link, useParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAllowed } from "../../lib/permissions"
import { knowledgeSkills, type SkillDistillation } from "./skills-api"

export function WorkSkillDistillation({work, state}: {work: string; state: string}) {
  const {tenantID = ""} = useParams()
  const allowed = useAllowed()
  if (state !== "ACCEPTED" || !allowed("knowledge_write")) return null
  return <DistillationForm key={`${tenantID}:${work}`} tenant={tenantID} work={work} />
}

function DistillationForm({tenant, work}: {tenant: string; work: string}) {
  const t = useTranslations("iam")
  const [agent, setAgent] = useState("")
  const [request, setRequest] = useState<SkillDistillation | null>(null)
  const [reviewSkill, setReviewSkill] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const attempt = useRef<{agent: string; key: string} | null>(null)
  const apply = async (value: SkillDistillation) => {
    setRequest(value)
    if (value.skill_version_id) {
      const version = await knowledgeSkills.version(tenant, value.skill_version_id)
      setReviewSkill(version.skill_id ?? "")
    }
  }
  const start = async () => {
    setBusy(true); setError("")
    const retry = attempt.current?.agent === agent ? attempt.current : {agent, key: crypto.randomUUID()}
    attempt.current = retry
    try { await apply(await knowledgeSkills.distill(tenant, work, agent, retry.key)) }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("fleetUnavailable")) }
    finally { setBusy(false) }
  }
  const refresh = async () => {
    if (!request?.distillation_id) return
    setBusy(true); setError("")
    try { await apply(await knowledgeSkills.distillation(tenant, request.distillation_id)) }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("fleetUnavailable")) }
    finally { setBusy(false) }
  }
  return <details className="rounded border p-4"><summary className="cursor-pointer py-2 font-medium">{t("knowledgeSkillDistill")}</summary>
    <form className="mt-3 grid gap-3" onSubmit={event => {event.preventDefault(); void start()}}>
      <p className="text-sm text-muted-foreground">{t("knowledgeSkillDistillHint")}</p>
      <label className="grid gap-2 text-sm">{t("knowledgeSkillDistillAgent")}<Input required pattern="[1-9][0-9]{0,18}" inputMode="numeric" value={agent} disabled={busy || !!request} onChange={event => setAgent(event.target.value)} /></label>
      {error && <Alert>{error}</Alert>}
      {!request && <Button type="submit" className="justify-self-start" disabled={busy}>{t(busy ? "loading" : "knowledgeSkillDistill")}</Button>}
      {request && <div className="grid gap-3" role="status"><p>{t(`knowledgeSkillDistillState_${request.state}`)}</p>{reviewSkill && <Link className="inline-flex min-h-11 items-center underline" to={`/admin/${encodeURIComponent(tenant)}/knowledge/skills?skill=${encodeURIComponent(reviewSkill)}`}>{t("knowledgeSkillDistillReview")}</Link>}{request.state === "RUNNING" && <Button type="button" variant="outline" className="justify-self-start" disabled={busy} onClick={() => void refresh()}>{t(busy ? "loading" : "refresh")}</Button>}</div>}
    </form>
  </details>
}
