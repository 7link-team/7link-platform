import type { components } from "./api-schema"
import { iamApi } from "./iam-api"
export type PackageSendRecord = {id:string;package_version:number;content_hash:string;recipient_id:string;channel_id:string;attempt_no:number;state:"FAILED"|"SUCCEEDED";message_id:string|null;failure_code:string|null;actor_id:string;created_at:string}
export type DeliveryConfirmation = components["schemas"]["DeliveryConfirmationFact"]
export type ClientRecords = {
  shares: components["schemas"]["PreviewShareView"]
  sheets: components["schemas"]["SheetView"]
  changes: components["schemas"]["ChangeView"]
  issues: components["schemas"]["IssueView"]
  packages: components["schemas"]["PackageView"]
  previews: components["schemas"]["PreviewView"]
  comments: components["schemas"]["AnnotationView"]
  cases: components["schemas"]["TestCaseView"]
  plans: components["schemas"]["TestPlanView"]
  runs: components["schemas"]["TestRunView"]
}
export type ClientRefusalAnchor = {thread_id:string;anchor_kind:string;anchor_excerpt:string;version_ref:string;artifact_ref:Record<string,unknown>;anchor?:Record<string,unknown>}
export type ClientPackageCommentInput = {body:string;anchor_kind?:"CODE_LINES"|"TABLE_ROW"|"TEXT_PARAGRAPH"|"HTML_REGION";start_line?:number;end_line?:number;row_index?:number;paragraph_index?:number;text_quote?:string;rect?:{x:number;y:number;w:number;h:number};viewport?:{w:number;h:number;dpr:number;device:"DESKTOP"|"TABLET"|"PHONE"};scroll?:{x:number;y:number}}
export type ClientPackageThread = ClientRefusalAnchor & {body:string;converted_target_id?:string}
function packageCommentPath(project:string,id:string,item:string){
 if(!/^[a-f0-9]{64}$/.test(item))throw new Error("Invalid delivery item")
 return `${collaborationPath(project,"packages",id)}/items/${item}/comments`
}
export type ClientCollection = keyof ClientRecords
export type ClientPage<T> = { items: T[]; has_more: boolean; next_cursor?: string }
export const clientCollections = {
  shares:"preview-shares", sheets: "requirement-sheets", changes: "client-changes", issues: "client-issues",
  packages: "delivery-packages", previews: "artifact-previews", comments: "artifact-comments",
  cases: "test-cases", plans: "test-plans", runs: "test-runs",
} as const
export function collaborationID(value: string | null | undefined): string {
  if (!value || !/^[1-9][0-9]{0,18}$/.test(value)) throw new Error("Invalid resource identifier")
  return value
}
export function collaborationPath(project: string, collection: ClientCollection, id?: string) {
  return `/entities/${collaborationID(project)}/${clientCollections[collection]}${id ? `/${collaborationID(id)}` : ""}`
}
export type PackageBundleState = {state: "NONE"|"BUILDING"|"BUILT"|"FAILED"|"STALE";fingerprint?:string;current_fingerprint?:string;bundle_sha256?:string;manifest_sha256?:string;error_code?:string;size_bytes:number}
export const clientworkApi = {
 packageBundle:(project:string,id:string,signal?:AbortSignal)=>iamApi.deliveryResultRequest<PackageBundleState>({method:"GET",url:`${collaborationPath(project,"packages",id)}/bundle`},{signal}),
 packageBundleContent:(project:string,id:string)=>iamApi.deliveryResultDownload({method:"GET",url:`${collaborationPath(project,"packages",id)}/bundle/content`},{maxBytes:512*1024*1024}),
 packageComments:(project:string,id:string,item:string,cursor:string,signal?:AbortSignal)=>iamApi.deliveryResultRequest<ClientPage<ClientPackageThread>>({method:"GET",url:`${packageCommentPath(project,id,item)}?${new URLSearchParams({limit:"20",...(cursor?{cursor}:{})})}`},{signal}),
 createPackageComment:(project:string,id:string,item:string,body:ClientPackageCommentInput,key:string)=>iamApi.deliveryResultRequest<components["schemas"]["Receipt"]>({method:"POST",url:packageCommentPath(project,id,item)},{headers:{"Idempotency-Key":key},body:JSON.stringify(body)}),
 packageCommentSource:(project:string,id:string,version:string,signal?:AbortSignal)=>iamApi.deliveryResultDownload({method:"GET",url:`${collaborationPath(project,"packages",id)}/artifacts/${collaborationID(version)}/content`},{maxBytes:4*1024*1024,signal}),
 receiptEvidence: (project:string,id:string) => iamApi.deliveryResultDownload({method:"GET",url:`${collaborationPath(project,"packages",id)}/receipt-evidence`}),
  list: <K extends ClientCollection>(project: string, collection: K, cursor?: string, signal?: AbortSignal) =>
    iamApi.deliveryResultRequest<ClientPage<ClientRecords[K]>>({method: "GET", url: `${collaborationPath(project, collection)}?${new URLSearchParams({limit: "20", ...(cursor ? {cursor} : {})})}`}, {signal}),
  get: <K extends ClientCollection>(project: string, collection: K, id: string, signal?: AbortSignal, revision?: number, replyCursor?: string) =>
    iamApi.deliveryResultRequest<ClientRecords[K]>({method: "GET", url: `${collaborationPath(project, collection, id)}${collection === "sheets" && revision ? `?revision=${revision}` : collection === "issues" && replyCursor ? `?reply_cursor=${encodeURIComponent(replyCursor)}` : ""}`}, {signal}),
  command: (project: string, collection: ClientCollection, body: unknown, key: string, id?: string, action?: string, version?: number, patch = false) => {
    if (action && !["versions", "publish", "signoffs", "actions", "receipts", "runs", "applications", "decisions", "rework", "recorded-receipts", "sends"].includes(action)) throw new Error("Invalid action")
    return iamApi.deliveryResultRequest<components["schemas"]["Receipt"]>({method: patch ? "PATCH" : "POST", url: `${collaborationPath(project, collection, id)}${action ? `/${action}` : ""}`}, {
      headers: {"Idempotency-Key": key, ...(version ? {"If-Match": `"${version}"`} : {})}, body: JSON.stringify(body),
    })
  },
  packageSends:(project:string,id:string,cursor:string,signal?:AbortSignal)=>iamApi.deliveryResultRequest<ClientPage<PackageSendRecord>>({method:"GET",url:`${collaborationPath(project,"packages",id)}/sends?${new URLSearchParams({limit:"20",...(cursor?{cursor}:{})})}`},{signal}),
  requestDeliveryConfirmation:(project:string,id:string,version:number,key:string)=>iamApi.deliveryResultRequest<DeliveryConfirmation>({method:"POST",url:`/entities/${collaborationID(project)}/project/delivery-packages/${collaborationID(id)}/confirmation-requests`},{headers:{"Idempotency-Key":key},body:JSON.stringify({source_version:version})}),
  withdrawDeliveryConfirmation:(project:string,id:string,key:string)=>iamApi.deliveryResultRequest<DeliveryConfirmation>({method:"PUT",url:`/entities/${collaborationID(project)}/project/delivery-confirmations/${collaborationID(id)}/withdrawal`},{headers:{"Idempotency-Key":key}}),
  access: (project: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<{external:boolean;principal_id:string}>({method:"GET",url:`/entities/${collaborationID(project)}/client-access`},{signal}),
  packageRework: (project: string, id: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<components["schemas"]["ReworkSourceView"]>({method:"GET",url:`${collaborationPath(project,"packages",id)}/rework`},{signal}),
  download: (project:string,collection:"previews"|"packages"|"shares",id:string,versionID?:string) => iamApi.deliveryResultDownload({method:"GET",url:`${collaborationPath(project,collection,id)}${collection==="packages"?`/artifacts/${collaborationID(versionID)}`:""}/content`}),
  timeline: (project:string,id:string,cursor:string,signal?:AbortSignal)=>iamApi.deliveryResultRequest<ClientPage<components["schemas"]["PackageTimelineView"]>>({method:"GET",url:`${collaborationPath(project,"packages",id)}/timeline?${new URLSearchParams({limit:"20",...(cursor?{cursor}:{})})}`},{signal}),
  settings: (project: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<components["schemas"]["ClientSettings"]>({method: "GET", url: `/entities/${collaborationID(project)}/project/settings`}, {signal}),
  patchSettings: (project: string, body: components["schemas"]["ClientSettingsPatch"], version: number, key: string) => iamApi.deliveryResultRequest<components["schemas"]["Receipt"]>({method: "PATCH", url: `/entities/${collaborationID(project)}/project/settings`}, {headers: {"Idempotency-Key": key, "If-Match": `"${version}"`}, body: JSON.stringify(body)}),
}
export function splitReferenceIDs(value: unknown): string[] {
  const ids = String(value ?? "").split(/[\s,]+/).filter(Boolean)
  if (!ids.length || ids.length > 100 || new Set(ids).size !== ids.length) throw new Error("Invalid resource references")
  return ids.map(collaborationID)
}
export function textLines(value: unknown): string[] { return String(value ?? "").split(/\r?\n/).map(v => v.trim()).filter(Boolean) }

// Refuse lossy JS numbers: an edit must never round a stored micro-amount.
function whole(value: unknown, message: string, fallback?: number): number | null {
 if (value === "" || value === undefined || value === null) return fallback ?? null
 const n = Number(value)
 if (!Number.isSafeInteger(n) || n < 0) throw new Error(message)
 return n
}
export function toleranceBudget(values: Record<string, unknown>, message = "Invalid tolerance commitment"): components["schemas"]["ToleranceBudget"] {
 const pct = (name: string, fallback: number) => { const n = whole(values[name], message, fallback)!; if (n > 100) throw new Error(message); return n }
 const date = new Date(String(values.appetite_end_at))
 if (!Number.isFinite(date.getTime()) || date.getUTCFullYear() < 1970 || date.getUTCFullYear() > 9999) throw new Error(message)
 return { appetite_end_at: date.toISOString(), appetite_cost_micros: whole(values.appetite_cost_micros, message), appetite_scope_criteria: whole(values.appetite_scope_criteria, message), tolerance_time_pct: pct("tolerance_time_pct", 20), tolerance_cost_pct: pct("tolerance_cost_pct", 15), tolerance_scope_pct: pct("tolerance_scope_pct", 10) }
}
export function toleranceForecast(values: Record<string, unknown>, message: string): components["schemas"]["ToleranceForecast"] {
 const date = new Date(String(values.end_at))
 if (!Number.isFinite(date.getTime()) || date.getUTCFullYear() < 1970 || date.getUTCFullYear() > 9999) throw new Error(message)
 return { end_at: date.toISOString(), cost_micros: whole(values.cost_micros, message), scope_criteria: whole(values.scope_criteria, message) }
}
export function approvedChangeDelta(values: Record<string, unknown>): components["schemas"]["ApprovedChangeDelta"] {
 const signed = (name: string) => { const n = values[name] === "" || values[name] == null ? 0 : Number(values[name]); if (!Number.isSafeInteger(n)) throw new Error("Invalid approved change delta"); return n }
 const days = signed("schedule_delta_days")
 if (Math.abs(days) > 36500) throw new Error("Invalid approved change schedule")
 return { schedule_delta_days: days, cost_delta_micros: signed("cost_delta_micros"), scope_delta_criteria: signed("scope_delta_criteria") }
}

export const packageFeedbackCategories = ["FUNCTIONAL_DEFECT", "REQUIREMENT_MISMATCH", "STYLE", "PERFORMANCE", "SECURITY", "OTHER"] as const satisfies readonly NonNullable<components["schemas"]["PackageItemDecision"]["feedback_category"]>[]

export function packageItemDecision(row: ClientRecords["packages"], values: Record<string, unknown>, message: string): components["schemas"]["PackageDecision"] {
  const reason = String(values.reason ?? "").trim()
  const signer_name = String(values.signer_name ?? "").trim()
  if (!signer_name || Array.from(signer_name).length > 200 || values.confirmed !== true) throw new Error(message)
  if (!reason || Array.from(reason).length > 2000 || !/^[a-f0-9]{64}$/.test(row.content_hash) || !row.items?.length || row.items.length > 100) throw new Error(message)
  const seen = new Set<string>()
  const items = row.items.map<components["schemas"]["PackageItemDecision"]>(item => {
    if (!/^[a-f0-9]{64}$/.test(item.item_id) || seen.has(item.item_id)) throw new Error(message)
    seen.add(item.item_id)
    const references = values[`threads_${item.item_id}`]
    if (references !== undefined && !Array.isArray(references)) throw new Error(message)
    const threadIDs = Array.isArray(references) ? [...new Set(references.map(value => collaborationID(String(value))))] : []
    if (threadIDs.length > 20) throw new Error(message)
    const result = values[`result_${item.item_id}`]
    if (result !== "ACCEPTED" && result !== "REJECTED") throw new Error(message)
    if (result === "ACCEPTED") return { item_id: item.item_id, result }
    const category = values[`category_${item.item_id}`]
    const comment = String(values[`comment_${item.item_id}`] ?? "").trim()
    if (!packageFeedbackCategories.some(value => value === category) || Array.from(comment).length < 10 || Array.from(comment).length > 2000) throw new Error(message)
    return { item_id: item.item_id, result, feedback_category: category as NonNullable<components["schemas"]["PackageItemDecision"]["feedback_category"]>, comment, ...(threadIDs.length ? {comment_thread_ids:threadIDs} : {}) }
  })
  return { content_hash: row.content_hash, signer_name, confirmed: true, reason, items }
}

export function packageReworkMatches(source: components["schemas"]["ReworkSourceView"], project: string, row: ClientRecords["packages"]): boolean {
  return Boolean(source.id && source.project_id === project && source.package_id === row.id && source.content_hash === row.content_hash && (!row.rework_intake_id || source.id === row.rework_intake_id) && source.rejection.package_id === row.id && source.rejection.content_hash === row.content_hash && source.receipt_id && source.rejection.receipt_id === source.receipt_id)
}


// CSV parsing matches source rows (header excluded), including quoted delimiters,
// escaped quotes and newlines. Malformed/unsupported data remains unavailable.
export function packageSourceRows(text:string,name:string):unknown[] {
  if(/\.json$/i.test(name)){
    if(!Array.isArray(JSON.parse(text)))throw new Error("Invalid table")
    const source=text.trim(),rows:string[]=[];let start=1,depth=0,quoted=false,escaped=false
    for(let i=1;i<source.length-1;i++){const char=source[i]!
      if(quoted){if(escaped)escaped=false;else if(char==="\\")escaped=true;else if(char==='"')quoted=false;continue}
      if(char==='"')quoted=true;else if(char==="["||char==="{")depth++;else if(char==="]"||char==="}")depth--;else if(char===","&&depth===0){rows.push(source.slice(start,i).trim());start=i+1}
    }
    const last=source.slice(start,-1).trim();if(last)rows.push(last);return rows
  }
  if(!/\.(csv|tsv)$/i.test(name))throw new Error("Unsupported table")
  const delimiter=/\.tsv$/i.test(name)?"\t":",",rows:string[][]=[]
  let row:string[]=[],cell="",quoted=false,ended=false
  for(let i=0;i<text.length;i++){const c=text[i]!
    if(c==='"'){if(quoted&&text[i+1]==='"'){cell+='"';i++}else if(quoted){quoted=false;ended=true}else if(cell===""&&!ended)quoted=true;else throw new Error("Invalid table")}
    else if(!quoted&&c===delimiter){row.push(cell);cell="";ended=false}
    else if(!quoted&&c==="\n"){const nonempty=cell!==""||row.length>0||ended;row.push(cell);if(nonempty)rows.push(row);row=[];cell="";ended=false}
    else {if(ended)throw new Error("Invalid table");cell+=c}
  }
  if(quoted)throw new Error("Invalid table")
  if(cell!==""||row.length||ended){row.push(cell);rows.push(row)}
  if(rows.length<2)throw new Error("Invalid table")
  const width=rows[0]!.length;if(rows.some(value=>value.length!==width))throw new Error("Invalid table")
  return rows.slice(1)
}
