import { useCallback, useEffect, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@workspace/ui/components/tabs"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi } from "../../lib/iam-api"
import { useRealtime } from "@workspace/ui/hooks/use-realtime"
import { useAllowed } from "../../lib/permissions"
import type { components } from "../../lib/api-schema"
import { ExecutionMerge } from "./merge"
import { ExecutionProgress } from "./progress"
import { ExecutionBranches } from "./branches"
import { EvidenceProvenancePanel } from "./provenance"
import { ArtifactCommentThreads } from "./artifact-comments"
import { UsedKnowledgePanel } from "../knowledge/used-knowledge"

export default function ExecutionDetailPage() {
  const {tenantID = "", entityID = "", executionID = ""} = useParams()
  return <ExecutionDetail key={`${tenantID}:${entityID}:${executionID}`} tenantID={tenantID} projectID={entityID} executionID={executionID} />
}
function ExecutionDetail({tenantID, projectID, executionID}: {tenantID: string; projectID: string; executionID: string}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [search, setSearch] = useSearchParams()
  const tab = ["plan", "logs", "evidence", "branches"].includes(search.get("tab") ?? "") ? search.get("tab")! : "plan"
  const [detail, setDetail] = useState<components["schemas"]["ExecutionDetail"] | null>(null)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [acknowledge, setAcknowledge] = useState(false)
  const [permissionReasons, setPermissionReasons] = useState<Record<string,string>>({})
  const [permissionSaved,setPermissionSaved] = useState(false)
  const pending = useRef<AbortController | null>(null)
  const load = useCallback(async (background = false) => {
    pending.current?.abort(); const controller = new AbortController(); pending.current = controller
    if (!background) setLoading(true); setError("")
    try {const row = await iamApi.execution(executionID, controller.signal); if (!controller.signal.aborted) {if (row.execution.project_id !== projectID) throw new Error(t("executionWrongProject")); setDetail(row)}}
    catch (cause) {if (!controller.signal.aborted) {if (!background) setDetail(null); setError(cause instanceof Error ? cause.message : t("executionUnavailable"))}}
    finally {if (!controller.signal.aborted) setLoading(false)}
  }, [executionID, projectID, t])
  useEffect(() => {let active = true; queueMicrotask(() => {if (active) void load()}); return () => {active = false; pending.current?.abort()}}, [load])
  useRealtime(`tenant/${tenantID}/execution/${executionID}`, {onEvent: () => {void load(true)}, onResync: () => {void load(true)}, poll: () => load(true), pollIntervalMs: 5000})
  const execution = detail?.execution
  const command = async (recover: boolean) => {
    if (!execution || busy) return
    setBusy(true); setError("")
    try {const row = recover ? await iamApi.recoverExecution(executionID, execution.version, acknowledge) : await iamApi.cancelExecution(executionID, execution.version); setDetail((old) => old ? {...old, execution: row} : null); setAcknowledge(false)}
    catch (cause) {setError(cause instanceof Error ? cause.message : t("executionUnavailable"))}
    finally {setBusy(false)}
  }
  return <div className="flex min-w-0 flex-1 flex-col gap-4 overflow-auto">
    <Link className="text-sm underline" to={`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(projectID)}/executions`}>{t("executions")}</Link>
    <PageHeader title={execution ? `${t("executionID")} ${execution.id}` : t("executions")} action={<Button variant="outline" disabled={loading || busy} onClick={() => void load()}>{t("refresh")}</Button>} />
    {error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
    {execution && detail && !loading && <>
      <Link className="inline-flex min-h-11 items-center underline" to={`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(projectID)}/workspace/${encodeURIComponent(executionID)}`}>{t("runtimeWorkspace")}</Link>
      <div className="flex flex-wrap items-center gap-3"><span className="rounded-full border px-3 py-1 text-sm">{t(`executionState_${execution.state}`)}</span><span className="text-sm text-muted-foreground">{t(`executionResult_${execution.result_state}`)}</span>
        {execution.cancel_requested_at && <p role="status" className="text-sm">{t("executionCancelPending")}</p>}
        {allowed("execution_stop") && ["CREATED", "RUNNING", "PAUSED"].includes(execution.state) && <Button variant="outline" disabled={busy || Boolean(execution.cancel_requested_at)} onClick={() => void command(false)}>{t("executionCancel")}</Button>}
      </div>
      <ExecutionProgress state={execution.state} progress={execution.progress} policy={execution.plan.progress_policy} templateVersion={execution.plan.progress_template_version_id ?? undefined}/>
      {allowed("interaction_respond") && (detail.approvals ?? []).filter((approval): approval is components["schemas"]["ApprovalView"] & {id: string} => typeof approval.id === "string" && approval.id.length > 0).map((approval) => <section key={approval.id} className="grid gap-3 rounded-lg border p-4" aria-label={t("executionPermissionTitle")}>
        <h2 className="font-semibold">{t("executionPermissionTitle")}: {approval.tool}</h2>
        <pre aria-label={t("executionPermissionInput")} className="max-h-64 overflow-auto whitespace-pre-wrap break-words text-sm">{JSON.stringify(approval.input, null, 2)}</pre>
        <label className="grid gap-2 text-sm">{t("executionPermissionReason")}<textarea className="min-h-24 rounded-md border bg-background p-3" maxLength={2000} value={permissionReasons[approval.id] ?? ""} disabled={busy} onChange={(event) => setPermissionReasons((old) => ({...old,[approval.id]:event.target.value}))} /></label>
        <div className="flex flex-wrap gap-2">{(["ALLOW","DENY"] as const).map((state) => <Button key={state} variant={state === "DENY" ? "outline" : "default"} className="min-h-11" disabled={busy || !(permissionReasons[approval.id] ?? "").trim()} onClick={async () => {
          if (busy) return
          setBusy(true); setError(""); setPermissionSaved(false)
          try {await iamApi.decideExecutionPermission(executionID, approval.id, state, permissionReasons[approval.id] ?? ""); setPermissionSaved(true); await load(true)}
          catch (cause) {setError(cause instanceof Error ? cause.message : t("executionUnavailable"))}
          finally {setBusy(false)}
        }}>{t(state === "ALLOW" ? "executionPermissionAllow" : "executionPermissionDeny")}</Button>)}</div>
      </section>)}
      {permissionSaved && <p role="status">{t("executionPermissionSaved")}</p>}
      {execution.result_state === "UNKNOWN" && <Alert>{t("executionUnknownWarning")}</Alert>}
      {allowed("execution_stop") && execution.state === "INTERRUPTED" && <section className="grid gap-3 rounded-lg border p-4"><label className="flex min-h-11 items-start gap-3 text-sm"><input className="mt-1 size-4" type="checkbox" checked={acknowledge} disabled={busy} onChange={(event) => setAcknowledge(event.target.checked)} />{t("executionRecoveryAcknowledgement")}</label><Button className="w-fit" disabled={busy || !acknowledge} onClick={() => void command(true)}>{t("executionRecover")}</Button></section>}
      <Tabs value={tab} onValueChange={(value) => setSearch((previous) => {const next = new URLSearchParams(previous); next.set("tab", value); return next})}><TabsList className="h-auto max-w-full flex-wrap" aria-label={t("executions")}>{["plan", "logs", "evidence", "branches"].map((tab) => <TabsTrigger className="min-h-11" key={tab} value={tab}>{t(tab === "branches" ? "executionTab_branches" : `executionTab_${tab}`)}</TabsTrigger>)}</TabsList>
        <TabsContent value="plan"><AutoForm mode="detail" initialData={{work_item_id: execution.work_item_id, machine_id: execution.machine_id, runtime: execution.plan.runtime, model: execution.plan.model, workdir: execution.plan.workdir, base_commit: execution.plan.base_commit, objective: execution.plan.objective, authorization_mode: execution.plan.authorization_mode, plan_hash: execution.plan_hash, dispatch_epoch: execution.dispatch_epoch, state_reason: execution.state_reason}} fields={{work_item_id: {name: t("executionWorkItem"), type: "text"}, machine_id: {name: t("fleetMachineID"), type: "text"}, runtime: {name: t("fleetRuntime"), type: "text"}, model: {name: t("fleetModel"), type: "text"}, workdir: {name: t("fleetPath"), type: "text"}, base_commit: {name: t("executionBaseCommit"), type: "text"}, objective: {name: t("executionObjective"), type: "textarea"}, authorization_mode: {name: t("executionAuthorization"), type: "text"}, plan_hash: {name: t("executionPlanHash"), type: "text"}, dispatch_epoch: {name: t("executionEpoch"), type: "number"}, state_reason: {name: t("executionReason"), type: "text"}}} /></TabsContent>
        <TabsContent value="logs">{detail.has_more_logs && <Alert>{t("executionLogsTruncated")}</Alert>}<div className="max-h-[60dvh] overflow-auto rounded-lg border bg-muted/30 p-4"><pre className="whitespace-pre-wrap break-words text-xs">{(detail.logs ?? []).map((row) => `[${row.dispatch_epoch}:${row.seq}] ${row.content}`).join("\n") || t("executionNoLogs")}</pre></div></TabsContent>
        <TabsContent value="evidence"><EvidenceProvenancePanel executionID={executionID}/></TabsContent>
        <TabsContent value="branches"><ExecutionBranches key={executionID} tenantID={tenantID} source={execution}/></TabsContent>
      </Tabs>
      <ExecutionMerge key={`merge:${executionID}`} executionID={executionID} state={execution.state}/>
      <ArtifactCommentThreads key={executionID} tenantID={tenantID} projectID={projectID} executionID={executionID} evidence={detail.evidence ?? []} />
      <UsedKnowledgePanel key={`knowledge:${executionID}`} executionID={executionID}/>
    </>}
  </div>
}
