// Isolated browser contract fixture. No production API requests or writes.
import { createRoot } from "react-dom/client"
import { createMemoryRouter, RouterProvider, Outlet, Link } from "react-router"
import { IntlAppProvider, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../src/lib/i18n"
import { AuthContext } from "../../src/components/auth"
import { projectSurfaces } from "../../src/lib/project-surfaces"
import { entityNavigation } from "../../src/lib/navigation"
import AuditEvents from "../../src/app/audit-events/page"
import MyWork from "../../src/app/my-work/page"
import AgentsPage from "../../src/app/agents/page"
import { ProfileEditor } from "../../src/app/security/profile"
import { ProfilePreferences } from "../../src/app/security/preferences"
import { ThemeProvider } from "next-themes"
import ConversationHubPage from "../../src/app/conversations/hub"
import ProjectSurface from "../../src/app/project-views/page"
import "../../src/index.css"

const query = new URLSearchParams(location.search)
const requests: string[] = []
const writes: {url:string;body:unknown;headers:unknown}[]=[]
Object.assign(window, { fixtureRequests: requests, fixtureWrites:writes })
let shareState="ACTIVE"
const directMessages = [{id:"9007199254740997",channel_id:"9007199254740993",author_id:"12",seq:1,body:"Private <script>window.directInjected=true</script>",visibility:"CHANNEL",version:1,created_at:"2026-10-21T00:00:00Z"}]
let directRead = 0
let selfProfile = { id: "11", display_name: "Fixture nickname", email: "fixture@example.test", phone: "+12025550123", email_verified: true, phone_verified: true }
let preferences = { tenant_id: "7", type_key: "*", in_app: true, browser_push: false, email: "IMMEDIATE", quiet_enabled: false, quiet_start_min: 1320, quiet_end_min: 480, timezone: "UTC", digest_hour: 9, version: 4 }
let preferenceConflict = query.has("conflict")
let retriageAttempts = 0
let submissionUploads = 0
let submissionAttachmentIDs: string[] = []
let triageVersion = 1
let triageCard: unknown = undefined
Object.assign(window, { fixtureClearPreferenceConflict: () => { preferenceConflict = false; preferences.version = 5 } })
Object.assign(window,{fixtureEndShare:()=>{shareState="REVOKED"}})
const nativeFetch = window.fetch.bind(window)
const base = {
  id: "21", project_id: "7", number: 1, title: "Fixture accountable work",
  accountable_principal_id: "11", current_claim_id: null, archived_at: null,
  type: "BUG", state: "NEEDS_HUMAN_REVIEW", description: "Isolated test fixture, not a real work item.",
  objective: "UI contract verification", version: 1, criteria: [], baseline_stale: false,
  preparation: {}, defect_stage: null, severity: "NORMAL", created_at: "2026-10-20T00:00:00Z", updated_at: "2026-10-20T00:00:00Z",
}
window.fetch = async (input, init) => {
  const url = String(input)
  if (!url.includes("/api/")) return nativeFetch(input, init)
  requests.push(`${init?.method ?? "GET"} ${url}`)
  if (query.has("submission-files") && url.includes("/entities/7/files")) {
    if (init?.method === "POST") {
      submissionUploads++
      const file = (init.body as FormData).get("file") as File
      writes.push({url, body:{upload_name:file?.name}, headers:{}})
      if (query.has("upload-fail") && submissionUploads === 2) return Response.json({code:503,message:"Fixture upload unavailable"},{status:503})
      return Response.json({code:0,data:{id:String(9000+submissionUploads),kind:"file",name:file?.name}})
    }
    return Response.json({code:0,data:{items:[{id:"700",kind:"directory",name:"Project files"}]}})
  }
  if(init?.method && init.method!=="GET" && init.method!=="OPTIONS") writes.push({url,body:JSON.parse(String(init.body||"{}")),headers:Object.fromEntries(new Headers(init.headers).entries())})
  const unavailable = query.has("unavailable") || url.includes("/workflow") || url.includes("/files") || url.includes("/spaces") || url.includes("/roots")
  if (unavailable) return new Response(JSON.stringify({ code: 503, message: "Unavailable: isolated UI fixture" }), { status: 503, headers: { "Content-Type": "application/json" } })
  let data: unknown = []
  if (query.has("submission-only") && url.includes("/intake-submissions")) {
    if (url.includes("/attachments/")) return Response.json({code:403,message:"Fixture attachment access revoked"},{status:403})
    if (init?.method === "PUT") {
      retriageAttempts++
      if (query.has("submission-fail") && retriageAttempts === 1) return Response.json({ code: 503, message: "Fixture answer unavailable" }, { status: 503 })
      triageVersion = 2
      submissionAttachmentIDs = JSON.parse(String(init.body)).upload_ids ?? []
      return Response.json({ code: 0, data: { id: "9007199254740995", version: 2, state: "ANSWERED" } })
    }
    const row = { id: "9007199254740993", project_id: query.has("wrong-intake-project") ? "8" : "7", ref_no: 42, status: triageVersion === 2 ? "TRIAGED" : "CLARIFYING", kind: "NEW", version: triageVersion, received_at: "2026-10-22T00:00:00Z", updated_at: "2026-10-22T00:00:00Z", client_message: "Public <script>window.submissionInjected=true</script>", open_questions_count: triageVersion === 2 ? 0 : 1, questions: [{ round_id: "9007199254740995", round_version: triageVersion, number: 1, text: "Which timezone?", state: triageVersion === 2 ? "ANSWERED" : "SENT", answer: triageVersion === 2 ? "UTC" : "", attachments:submissionAttachmentIDs.map((id,index)=>({id:String(9100+index),file_id:id,version_id:String(9200+index),name:`evidence-${index}.txt`,mime:"text/plain",size:10,sha256:"a".repeat(64)})) }], deliveries: [], downloads_available: false, change_view_available: false }
    return Response.json({ code: 0, data: new URL(url, location.origin).pathname.endsWith("/intake-submissions") ? { items: [row], has_more: false } : row })
  }
  if (url.endsWith("/triage-requests") && init?.method === "POST") {
    retriageAttempts++
    if(query.has("triage-fail") && retriageAttempts===1) return Response.json({code:503,message:"Fixture queue unavailable"},{status:503})
    return Response.json({code:0,data:{id:"9007199254740993",version:2,state:"REQUESTED",requested_at:"2026-10-22T12:00:00Z",coalesced:query.has("triage-fail")||retriageAttempts>1}},{status:query.has("triage-fail")||retriageAttempts>1?202:201})
  }
  if(query.has("delegated")&&url.includes("/delegated-requirement")) {
 if(query.has("delegated-denied"))return Response.json({code:124403,message:"Current delegation denied"},{status:403})
 if(init?.method==="POST"){
 if(query.has("delegated-conflict"))return Response.json({code:124409,message:"Frozen card conflict"},{status:409})
 return Response.json({code:0,data:{id:"9007199254740999",version:1,state:"DRAFT"}})
 }
 return Response.json({code:0,data:{id:"9007199254740995",intake_id:"9007199254740993",project_id:query.has("wrong-intake-project")?"8":"7",actor_id:"9007199254740997",revision:2,version:4,hash:"c".repeat(64),created_at:"2026-10-22T00:00:00Z",draft:{summary:"Frozen <script>window.delegatedInjected=true</script>",requirement_draft:"Exact immutable proposal",criteria:["Every selected row"],risks:[],questions:[],size:"S"}}})
 }
 if (query.has("triage") && url.includes("/intakes")) {
    if (init?.method === "POST") {
      if (query.has("triage-conflict")) return Response.json({code:124409,message:"Stale source version"},{status:409})
      triageVersion++
      triageCard={id:"9007199254740999",intake_id:"9007199254740993",project_id:"7",revision:triageVersion-1,version:triageVersion,hash:"c".repeat(64),actor_id:"11",created_at:"2026-10-21T00:00:00Z",draft:JSON.parse(String(init.body))}
      data={id:"9007199254740993",version:triageVersion,revision:triageVersion-1,state:"TRIAGED"}
    } else {
      const row={id:"9007199254740993",project_id:query.has("wrong-intake-project")?"8":"7",ref_no:42,title:"Fixture NEW Intake",kind:query.has("rework")?"REWORK":"NEW",state:triageVersion===1?"RECEIVED":"TRIAGED",version:triageVersion,card_revision:triageVersion-1,received_at:"2026-10-21T00:00:00Z",raw_content:"Original <script>window.intakeInjected=true</script>",raw_content_format:"PLAIN",raw_content_hash:"a".repeat(64),triage:triageCard}
      data=new URL(url,location.origin).pathname.endsWith("/intakes")?{items:[row],has_more:false}:row
    }
    return Response.json({code:0,data})
  }
  if (url.includes("/notification-preferences/")) {
    if (query.has("private-denied")) return Response.json({ code: 403, message: "Forbidden" }, { status: 403 })
    if (init?.method === "PUT") {
      if (preferenceConflict) return Response.json({ code: 409, message: "Version conflict" }, { status: 409 })
      preferences = { ...preferences, ...JSON.parse(String(init.body)), version: preferences.version + 1 }
    }
    return Response.json({ code: 0, data: { ...preferences, ...(query.has("wrong-workspace") ? { tenant_id: "8" } : {}) } })
  }
  if (url.includes("/direct-channels") || url.includes("/human-contacts") || /\/tenants\/1\/conversations(?:\?|$)/.test(url)) {
    if (query.has("private-denied")) return Response.json({code:403,message:"Private participant denied"},{status:403})
    const channel = {id:"9007199254740993",kind:"DIRECT",name:"Human conversation",visibility:"INTERNAL",unread:Math.max(0,directMessages.length-directRead),mention_unread:0,version:1}
    const messagePath = url.includes("/messages")
    if (init?.method === "OPTIONS") data = {version:"fixture",operation_id:messagePath?"channel.direct.post":"channel.direct.create",method:"POST",path:messagePath?"/tenants/{tenant_id}/direct-channels/{channel_id}/messages":"/tenants/{tenant_id}/direct-channels",locale:query.get("locale")||"en-US",schemas:{},request_body:{content:{"application/json":{schema:{type:"object",properties:messagePath?{body:{type:"string",minLength:1,maxLength:8000}}:{peer_principal_id:{type:"string"}},required:[messagePath?"body":"peer_principal_id"]}}}}}
    else if(url.includes("/read-marker")) { directRead=JSON.parse(String(init?.body)).seq;data=directRead }
    else if(url.includes("/human-contacts")) data={items:[{id:"9007199254740995",display_name:"Alice <script>window.contactInjected=true</script>",avatar:""}],has_more:false}
    else if(messagePath && init?.method==="POST") {const body=JSON.parse(String(init.body));const message={...directMessages[0]!,id:"9007199254740999",seq:2,author_id:"11",body:body.body};directMessages.push(message);data=message}
    else if(messagePath) data={items:[...directMessages].reverse(),has_more:false}
    else if(init?.method==="POST") data={...channel,created:false}
    else data={items:[channel,{id:"92",kind:"CUSTOM",project_id:"7",name:"Authorized project channel",unread:2,mention_unread:0,version:1},{id:"93",kind:"CUSTOM",project_id:"8",name:"Other authorized project",unread:0,mention_unread:0,version:1},{id:"94",kind:"CUSTOM",project_id:"7",name:"Same project second channel",unread:0,mention_unread:0,version:1}],has_more:false}
    return Response.json({code:0,data})
  }
  if (url.includes("/principals/11/profile")) {
    if (init?.method === "OPTIONS") data = { version: "fixture", operation_id: "identity-update-my-profile", method: "PATCH", path: "/principals/{principal_id}/profile", locale: query.get("locale") || "en-US", schemas: {}, request_body: { content: { "application/json": { schema: { type: "object", additionalProperties: false, properties: { display_name: { type: "string", minLength: 1, maxLength: 128 }, email: { type: "string", maxLength: 320 }, phone: { type: "string", maxLength: 32 } } } } } } }
    else { if (init?.method === "PATCH") selfProfile = { ...selfProfile, ...JSON.parse(String(init.body)) }; data = selfProfile }
    return Response.json({ code: 0, data })
  }
  let total: number | undefined
  const fixtures:Record<string,unknown> = {
    "agents": { id: "9007199254740993", principal_id: "9007199254740995", name: "Actual <script>window.agentInjected=true</script>", handle: "actual-agent", state: "ACTIVE", process_status: "STOPPED", runtime: "CODEX", model: "reported-model", owner_id: "11", version: 1 },
    "intakes": { id: "9007199254740993", project_id: query.has("wrong-intake-project") ? "8" : "7", ref_no: 42, title: "Fixture Intake source", kind: "CHANGE", state: "TRIAGED", version: 4, card_revision: 2, received_at: "2026-10-21T00:00:00Z", raw_content: "Original <script>window.intakeInjected=true</script>", raw_content_format: "PLAIN", raw_content_hash: "a".repeat(64), card: { id: "9007199254740993", project_id: "7", requirement_id: "9007199254740995", original_intake_id: "9007199254740997", change_id: "9007199254740999", state: "TRIAGED", version: 4, card_revision: 2, proposal_hash: "b".repeat(64), proposal: '{"summary":"Exact frozen change"}', impact: '{"breached":[]}' } },
    "dispatch-proposals":{id:"92",work_item_id:"21",work_version:1,trigger:"MANUAL_REQUEST",state:"PROPOSED",result:"RECOMMENDED",recommended_id:"12",inputs_digest:"d".repeat(64),version:1,expires_at:"2027-01-01T00:00:00Z",snapshot:{scoring_version:"s1-preview",requires_human:true,guard_reasons:["MANUAL_POLICY"],limitations:["QUALITY_COST_HISTORY_UNAVAILABLE"],work:{},candidates:[{agent_id:"13",principal_id:"12",name:"Fixture dispatch agent",score:850000,active_items:0,max_parallel:2,ineligible_codes:[],history_available:false,components:{skill_bp:10000,fpa_bp:5000,cost_bp:10000,load_bp:10000,diversity_bp:10000}}]}},
    "preview-shares":{id:"93",preview_id:"85",state:shareState,note:"Fixture expiring share",expires_at:"2027-01-01T00:00:00Z",version:2,...(shareState==="ACTIVE"?{preview:{id:"85",title:"Fixture private progress",source_stage:"PROGRESS",file_version_id:"40",content_hash:"a".repeat(64),client_visible:false}}:{})},

    "requirement-sheets":{id:"81",project_id:"7",title:"Fixture confirmation",revision:1,version:2,state:"AWAITING_SIGNOFF",content_hash:"a".repeat(64),signer_id:"11",content:{title:"Fixture confirmation",items:[{id:"10",title:"Fixture requirement",description:"Confirm this exact scope",acceptance_criteria:[{id:"20",text:"Works as specified"}]}],milestones:["First release"],out_of_scope:[],source_refs:[]}},
    "client-changes":{id:"82",sheet_id:"81",base_revision:1,description:"Fixture scope request",state:"AWAITING_CONFIRMATION",impact:"One extra milestone",proposal_hash:"b".repeat(64),version:2,applied:false},
    "client-issues":{id:"83",title:"Fixture client issue",description:"Reported observation",state:"RESPONDED",version:2,replies:[{id:"91",body:"Approved public reply"}],replies_has_more:false},
    "delivery-packages":{id:"84",title:"Fixture release",state:"PUBLISHED",version:2,content_hash:"c".repeat(64),recipient_id:"11",content:{title:"Fixture release",release_notes:"Release notes",artifacts:[{name:"report.txt",version_id:"40",sha256:"a".repeat(64),coverage:"COVERED",verification_overrides:[]}]}},
    "artifact-previews":{id:"85",title:"Fixture preview",file_version_id:"40",content_hash:"a".repeat(64),client_visible:true},
    "artifact-comments":{id:"86",preview_id:"85",file_version_id:"40",content_hash:"a".repeat(64),anchor:{kind:"PAGE",page:1},body:"Fixture anchored comment",client_visible:true},
    "test-cases":{id:"87",title:"Fixture case",steps:"Exercise flow",expected:"Correct result",work_item_id:"21",version:1},
    "test-plans":{id:"88",title:"Fixture plan",state:"FROZEN",version:1,cases:[{id:"87",title:"Fixture frozen case",steps:"Exercise flow",expected:"Correct result",work_item_id:"21",version:1}]},
    "test-runs":{id:"89",plan_id:"88",case_id:"87",case_version:1,result:"FAILED",evidence:"Observed manually",source:"HUMAN_RECORDED"},
  }
  if (query.has("draft")) fixtures["delivery-packages"] = {
    ...fixtures["delivery-packages"] as object, state: "DRAFT", version: 3,
    draft: { title: "Fixture release", release_notes: "Release notes", recipient_id: "11", artifacts: [{work_item_id:"21",result_id:"25",evidence_id:"30"}] },
  }
  if (query.has("requested")) fixtures["client-changes"] = {
    ...fixtures["client-changes"] as object, state: "REQUESTED", version: 1,
  }
  const resource = new URL(url,location.origin).pathname.split("/").filter(Boolean)
  const collection=resource.find(v=>v in fixtures)
  const auditEvent = {id:"501",event_type:"fixture.audit_event",outcome:"success",principal_id:"11",target_type:"fixture",target_id:"21",created_at:"2026-10-20T00:00:00Z",detail:{reason:"Fixture audit detail"},sequence:1,previous_hash:"",event_hash:"a".repeat(64)}
  if(url.includes("/audit/events/")) data=auditEvent
  else if(url.includes("/audit/events")){data=[auditEvent];total=1}
  else if(url.includes("/audit/integrity-verifications"))data={valid:true,verified_events:1,head_sequence:1}
  else if(url.includes("/preview-shares/")&&url.endsWith("/content"))return new Response("Fixture shared content",{headers:{"Content-Type":"text/plain"}})
  if(url.includes("/tolerance-impact")) {
    const before = {id:"95",subject_kind:"REQUIREMENT",subject_id:"10",project_id:"7",appetite_end_at:"2026-11-01T00:00:00Z",appetite_cost_micros:100,appetite_scope_criteria:10,tolerance_time_pct:20,tolerance_cost_pct:15,tolerance_scope_pct:10,baseline_no:1,version:1,state:"OK",forecast_at:null,forecast:{end_at:"0001-01-01T00:00:00Z",cost_micros:null,scope_criteria:null}}
    data={before:[before],subjects:[{...before,state:"EXCEPTION",forecast:{end_at:"2026-11-05T00:00:00Z",cost_micros:130,scope_criteria:10}}],breached:[{subject_kind:"REQUIREMENT",subject_id:"10",dimension:"TIME"},{subject_kind:"REQUIREMENT",subject_id:"10",dimension:"COST"}],low_confidence:true}
  }
  else if(url.includes("/project/tolerances"))data={items:[],has_more:false}
  else if(url.includes("/client-pending"))data={items:[{id:"81",kind:"SIGNOFF",title:"Fixture confirmation",version:2},{id:"82",kind:"CHANGE",title:"Fixture scope request",version:2},{id:"84",kind:"ACCEPTANCE",title:"Fixture release",version:2}],has_more:false}
  else if(url.endsWith("/dispatch-proposals")&&init?.method==="POST")data={id:"92",version:1,state:"PROPOSED"}
  else if(url.includes("/dispatch-candidates"))data={snapshot:{...(fixtures["dispatch-proposals"] as {snapshot:object}).snapshot,work:{work_item_id:"21",work_version:6,state:query.has("redispatch")?"IN_PROGRESS":"READY"}},inputs_digest:"e".repeat(64)}
  else if(url.includes("/timeline"))data={items:[{id:"100",kind:"package_published",state:"PUBLISHED",version:2,content_hash:"c".repeat(64),created_at:"2026-10-20T00:00:00Z"}],has_more:false}
  else if(url.includes("/client-access")) data={external:query.has("client"),principal_id:"11"}
  else if(url.includes("/project/settings")) data={name:"Fixture project",description:"Fixture project brief",target_date:"2027-01-01",version:1}
  else if(collection) {const row=fixtures[collection];data=resource.at(-1)===collection?{items:[row],has_more:false}:row}
  else if (/\/my-work(?:\?|$)/.test(url)) data = { items: [{ ...base, responsibility: "accountable" }], has_more: false }
  else if (/\/projects\?/.test(url)) { data = [{ project_id: "7", name: "Fixture project" }]; total = 1 }
  else if (/\/project(?:\?|$)/.test(url)) data = { project_id: "7", tenant_id: "1", name: "Fixture project", stage: "ACTIVE" }
  else if (/\/work-items\/21(?:\?|$)/.test(url)) data = base
  else if (/\/work-items(?:\?|$)/.test(url)) { data = [base, { ...base, id: "22", title: "Fixture other actor", accountable_principal_id: "12" }]; total = 2 }
  else if (url.includes("/assignments") || url.includes("/results")) { data = []; total = 0 }
  else if (url.includes("/me")) data = { subject: "11", id: "11", kind: "HUMAN" }
  return new Response(JSON.stringify({ code: 0, message: "ok", data, ...(total === undefined ? {} : { meta: { page: { total, offset: 0, limit: 200 } } }) }), { headers: { "Content-Type": "application/json" } })
}
const permissions = new Set(query.has("denied") ? [] : query.has("submission-only") ? ["intake_submit"] : query.has("triage-only") ? ["intake_triage"] : query.has("delegated") ? ["intake_convert"] : ["agent_read", "scope_change", "audit_event_view", "project_read", "work_item_assign", "work_item_read", "requirement_read", "channel_read", "execution_read", "file_view", "project_manage", "requirement_write", "delivery_compose", "delivery_send", "preview_share_approve", "artifact_comment_write", "test_case_manage", "test_plan_manage", "test_run_execute"])
export function Shell() {
  const t = useTranslations("iam")
  return <AuthContext.Provider value={{ session: { subject: "11" }, status: "authenticated", refresh: async () => {} } as never}>
    <main className="flex h-screen min-w-0 flex-col gap-4 p-4">
      <aside className="text-xs">Isolated UI fixture: supplied test responses only.</aside>
      <nav className="flex flex-wrap gap-3" aria-label="Fixture project navigation">{entityNavigation(permissions, "project").filter((link) => link.path === "/my-work" || projectSurfaces.some((entry) => `/${entry.path}` === link.path)).map((link) => <Link className="underline" key={link.path} to={`/admin/1/entities/7${link.path}`}>{t(link.label)}</Link>)}</nav>
      <Outlet context={{ tenantID: "1", entityID: "7", permissions, permissionsResolved: true }} />
    </main>
  </AuthContext.Provider>
}
const router = createMemoryRouter([{ element: <Shell />, children: [
  { path: "/admin/:tenantID/conversations", element: <ConversationHubPage /> },
  { path: "/admin/:tenantID/entities/:entityID/agents", element: <AgentsPage /> },
  { path: "/admin/:tenantID/entities/:entityID/profile", element: <ProfileEditor principal="11" /> },
  { path: "/admin/:tenantID/entities/:entityID/preferences", element: <ThemeProvider attribute="class"><ProfilePreferences principal="11" tenant={query.has("no-workspace") ? "" : "7"} /></ThemeProvider> },
  { path: "/admin/:tenantID/entities/:entityID/audit", element: <AuditEvents /> },
  { path: "/admin/:tenantID/entities/:entityID/my-work", element: <MyWork /> },
  ...projectSurfaces.map((entry) => ({ path: `/admin/:tenantID/entities/:entityID/${entry.path}`, element: <ProjectSurface surface={entry.path} /> })),
] }], { initialEntries: [query.get("page") === "hub" ? "/admin/1/conversations" : `/admin/1/entities/7/${query.get("page") || "my-work"}${query.has("id")||query.has("collection") ? `?${new URLSearchParams({...query.has("id")?{id:query.get("id")!}:{},...query.has("collection")?{collection:query.get("collection")!}:{}})}` : ""}`] })
void loadMessages(query.get("locale") || "en-US").then((messages) => {
  createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={query.get("locale") || "en-US"} initialMessages={messages}><RouterProvider router={router} /></IntlAppProvider>)
})
