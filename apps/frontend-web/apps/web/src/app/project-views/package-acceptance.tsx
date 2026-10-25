import { RubricScores } from "../templates/rubric-scores"
import { useEffect, useRef, useState } from "react"
import { Link, useParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import type { FormConfig } from "@workspace/ui/components/auto-form"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { Alert } from "../../components/alert"
import type { components } from "../../lib/api-schema"
import { clientworkApi, packageFeedbackCategories, packageItemDecision, packageReworkMatches, type ClientRecords } from "../../lib/clientwork-api"
import { useAllowed } from "../../lib/permissions"
import { MutationForm } from "./client"
import { PackageComments, RefusalAnchors } from "./package-comments"
import type { ClientRefusalAnchor } from "../../lib/clientwork-api"

type Package = ClientRecords["packages"]
type Source = components["schemas"]["ReworkSourceView"]
type Mutate = (body: unknown, key: string, action: string) => Promise<void>
export function PackageAcceptance({ project, row, access, mutate }: { project: string; row: Package; access: { external: boolean; principal_id: string; can_accept?: boolean } | null; mutate: Mutate }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
 const [offline,setOffline]=useState<{body:Record<string,unknown>;key:string}|null>(null)
 const [evidenceError,setEvidenceError]=useState("")
 const [downloading,setDownloading]=useState(false)
  const fields: FormConfig = {}
  for (const [index, item] of (row.items ?? []).entries()) {
    const label = `${index + 1}. ${item.artifact.name}`
    const result = `result_${item.item_id}`
    fields[result] = { name: `${label} · ${t("packageItemResult")}`, type: "select", required: true, config: { options: ["ACCEPTED", "REJECTED"].map(value => ({ value, label: t(`packageItem_${value}`) })) } }
    fields[`category_${item.item_id}`] = { name: `${label} · ${t("packageFeedbackCategory")}`, type: "select", required: true, show: values => values?.[result] === "REJECTED", config: { options: packageFeedbackCategories.map(value => ({ value, label: t(`packageFeedback_${value}`) })) } }
    fields[`threads_${item.item_id}`] = { name: `${label} ? ${t("packageReferencedComments")}`, type: "custom", show: values => values?.[result] === "REJECTED", component: props => <PackageComments {...props} project={project} delivery={row.id!} item={item.item_id} artifact={item.artifact} external={Boolean(access?.external)} published={row.state === "PUBLISHED"}/> }
    fields[`comment_${item.item_id}`] = { name: `${label} · ${t("packageRejectionComment")}`, type: "textarea", required: true, show: values => values?.[result] === "REJECTED", config: { rules: [value => { const length = Array.from(String(value ?? "").trim()).length; return length < 10 || length > 2000 ? t("packageItemInvalid") : undefined }] } }
  }
  fields.reason = { name: t("clientReason"), type: "textarea", required: true, config: { rules: [value => { const length = Array.from(String(value ?? "").trim()).length; return length < 1 || length > 2000 ? t("packageItemInvalid") : undefined }] } }
  fields.signer_name = { name: t("packageSignerName"), type: "text", required: true, config: { rules: [value => { const length = Array.from(String(value ?? "").trim()).length; return length < 1 || length > 200 ? t("packageItemInvalid") : undefined }] } }
  fields.confirmed = { name: t("packageConfirmed"), type: "checkbox", required: true, config: { rules: [value => value === true ? undefined : t("packageItemInvalid")] } }
  const outcomes = row.decision?.items ?? []
  return <section className="grid min-w-0 gap-3 border-t pt-4" aria-label={t("packageAcceptance")}>
    <h3 className="font-medium">{t("packageAcceptance")}</h3>
    {row.items?.map(item => <details key={item.item_id}><summary className="cursor-pointer py-3">{item.artifact.name} ? {t("packageClientComments")}</summary><PackageComments project={project} delivery={row.id!} item={item.item_id} artifact={item.artifact} external={Boolean(access?.external)} published={row.state === "PUBLISHED"}/></details>)}
    {row.state !== "DRAFT" && row.items?.map(item => <details key={`rubric:${item.item_id}`}><summary className="cursor-pointer py-3">{t("rubricTitle")}: {item.artifact.name}</summary><RubricScores project={project} packageID={row.id!} itemID={item.item_id}/></details>)}
    {row.decision && <>
      <p className="text-sm text-muted-foreground">{t("packageImmutableReceipt")}</p>
      <dl className="grid gap-2 text-sm sm:grid-cols-2">
        <div><dt>{t("clientDecision")}</dt><dd>{row.decision.decision}</dd></div>
        <div><dt>{t("clientReason")}</dt><dd className="whitespace-pre-wrap break-words">{row.decision.reason}</dd></div>
        <div className="sm:col-span-2"><dt>{t("packageReceiptHash")}</dt><dd className="break-all">{row.decision.content_hash}</dd></div>
        {row.signature && <><div><dt>{t("packageSignerName")}</dt><dd>{row.signature.signer_name}</dd></div><div className="sm:col-span-2"><dt>{t("packageSignatureHash")}</dt><dd className="break-all">{row.signature.receipt_sha256}</dd></div></>}
      </dl>
      {row.signature?.source === "RECORDED" && <section className="grid gap-2 text-sm" aria-label={t("packageOfflineReceipt")}><p>{t("packageRecordedBy",{recorder:row.signature.recorded_by ?? ""})}</p><p className="break-all">{row.signature.evidence?.name} / {row.signature.evidence?.sha256}</p>{evidenceError && <Alert>{evidenceError}</Alert>}<Button variant="outline" className="min-h-11 justify-self-start" disabled={downloading} onClick={async()=>{setDownloading(true);setEvidenceError("");try{const blob=await clientworkApi.receiptEvidence(project,row.id!);const url=URL.createObjectURL(blob);const a=document.createElement("a");a.href=url;a.download=row.signature?.evidence?.name ?? "acceptance-evidence";a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}catch(cause){setEvidenceError(cause instanceof Error?cause.message:t("clientLoadFailed"))}finally{setDownloading(false)}}}>{t("packageOfflineEvidence")}</Button></section>}
      {outcomes.length ? <ol className="grid gap-3">{outcomes.map(outcome => <li key={outcome.item_id} className="grid gap-1 border-l-2 pl-3 text-sm">
        <strong className="break-words">{row.items?.find(item => item.item_id === outcome.item_id)?.artifact.name ?? outcome.item_id}</strong>
        <p className="break-all font-mono text-xs">{outcome.item_id}</p>
        <p>{t(`packageItem_${outcome.result}`)}</p>
        {outcome.feedback_category && <p>{t(`packageFeedback_${outcome.feedback_category}`)}</p>}
        {outcome.comment && <p className="whitespace-pre-wrap break-words">{outcome.comment}</p>}
        <RefusalAnchors anchors={(outcome as typeof outcome & {anchors?:ClientRefusalAnchor[]}).anchors ?? []}/>
      </li>)}</ol> : <p role="status">{t("packageLegacyReceipt")}</p>}
    </>}
    {Boolean(row.dispositions?.length) && <section aria-label={t("packageRework")} className="grid gap-2"><h4>{t("packageRework")}</h4>{row.dispositions?.map((item,index)=><p key={`${item.item_id}:${index}`} className="whitespace-pre-wrap break-words text-sm">{item.client_message}</p>)}</section>}
    {access?.external && access.can_accept !== false && row.state === "PUBLISHED" && Boolean(row.items?.length) && <details>
      <summary className="cursor-pointer py-3">{t("packageDecideItems")}</summary>
      <p className="mb-3 text-sm text-muted-foreground">{t("packageItemNotice")}</p>
      <MutationForm key={`${row.id}:${row.version}:${row.content_hash}`} fields={fields} label={t("packageSubmitItems")} submit={(values, key) => mutate(packageItemDecision(row, values, t("packageItemInvalid")), key, "receipts")}/>
    </details>}
    {access && !access.external && allowed("delivery_send") && row.state === "PUBLISHED" && Boolean(row.items?.length) && <details><summary className="cursor-pointer py-3">{t("packageRecordOffline")}</summary><p className="mb-3 text-sm text-muted-foreground">{t("packageOfflineNotice")}</p><MutationForm key={`offline:${row.id}:${row.version}`} fields={{...fields,evidence_file_id:{name:t("packageOfflineFileID"),type:"text",required:true,config:{rules:[value=>/^[1-9][0-9]{0,18}$/.test(String(value??""))?undefined:t("packageItemInvalid")]}}}} label={t("packageRecordOffline")} submit={async(values,key)=>{const body=packageItemDecision(row,values,t("packageItemInvalid"));setOffline({body:{...body,evidence_file_id:String(values.evidence_file_id)},key})}}/></details>}
    {offline && <ReauthenticationDialog purpose="acceptance_record" target={`package:${row.id}`} onClose={()=>setOffline(null)} onVerified={async proof=>{await mutate({...offline.body,reauth_proof:proof},offline.key,"recorded-receipts");setOffline(null)}}/>}
    {access && !access.external && allowed("scope_change") && ["REJECTED", "PARTIALLY_ACCEPTED"].includes(row.state) && <PackageRework key={`${project}:${row.id}:${row.version}:${row.content_hash}`} project={project} row={row} mutate={mutate}/>}
  </section>
}

function PackageRework({ project, row, mutate }: { project: string; row: Package; mutate: Mutate }) {
  const { tenantID = "" } = useParams()
  const t = useTranslations("iam")
  const [source, setSource] = useState<Source | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const pending = useRef<AbortController | null>(null)
  useEffect(() => () => pending.current?.abort(), [])
  return <section className="grid min-w-0 gap-3 border-t pt-4" aria-label={t("packageRework")}>
    <h4 className="font-medium">{t("packageRework")}</h4>
    <p className="text-sm text-muted-foreground">{t("packageReworkNotice")}</p>
    {row.rework_intake_id && <p className="break-all">{t("packageSourceID")}: <Link className="underline underline-offset-4" to={`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(project)}/intake?${new URLSearchParams({ intake: row.rework_intake_id })}`}>{row.rework_intake_id}</Link></p>}
    {row.rework_unavailable && <p role="status">{t("packageReworkUnavailable")}: <span className="break-words">{row.rework_unavailable_reason || t("clientEmpty")}</span></p>}
    {error && <Alert>{error}</Alert>}
    <Button variant="outline" className="justify-self-start" disabled={busy} onClick={async () => {
      pending.current?.abort()
      const controller = new AbortController()
      pending.current = controller
      setBusy(true); setError(""); setSource(null)
      try {
        const value = await clientworkApi.packageRework(project, row.id!, controller.signal)
        if (controller.signal.aborted) return
        if (!packageReworkMatches(value, project, row)) throw new Error(t("packageReworkMismatch"))
        setSource(value)
      } catch (cause) { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("clientLoadFailed")) }
      finally { if (!controller.signal.aborted) setBusy(false) }
    }}>{busy ? t("loading") : t("packageReadRework")}</Button>
    {!row.rework_intake_id && row.rework_unavailable && <MutationForm fields={{}} label={t("packageRetryRework")} submit={(_, key) => mutate({}, key, "rework")}/>}
    {source && <dl className="grid gap-3 text-sm sm:grid-cols-2">{[
      ["packageSourceID", source.id], ["status", source.state], ["clientVersion", source.version], ["packageSourceReceipt", source.receipt_id], ["packageReceiptHash", source.content_hash],
    ].map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{t(String(label))}</dt><dd className="break-all">{value}</dd></div>)}
      <div className="sm:col-span-2"><dt>{t("packageRejectedSources")}</dt><dd><ol className="grid gap-3">{source.rejection.items?.map(item => <li key={item.item_id} className="grid gap-1 border-l-2 pl-3">
        <p className="break-all">{item.item_id}</p>
        {item.feedback_category && <p>{t.has(`packageFeedback_${item.feedback_category}`) ? t(`packageFeedback_${item.feedback_category}`) : item.feedback_category}</p>}
        <p className="whitespace-pre-wrap break-words">{item.comment}</p><RefusalAnchors anchors={(item as typeof item & {anchors?:ClientRefusalAnchor[]}).anchors ?? []}/>
        <dl className="grid gap-1">{[["clientRequirements", item.requirement_id], ["clientWorkItem", item.work_item_id], ["packageResult", item.result_id], ["packageEvidence", item.evidence_id], ["intakeOriginalID", item.source_intake_id]].map(([label, value]) => <div key={label}><dt>{t(String(label))}</dt><dd className="break-all">{value ?? "—"}</dd></div>)}</dl>
      </li>)}</ol></dd></div>
    </dl>}
  </section>
}
