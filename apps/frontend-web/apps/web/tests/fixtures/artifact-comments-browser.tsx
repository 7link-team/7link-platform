// Explicit transport fixture; real authorization, pinning and transactions are
// exercised by the backend tests. This renders the production components.
import {createRoot} from "react-dom/client"
import {MemoryRouter,Outlet,Route,Routes} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {AuthContext,type AuthContextValue} from "../../src/components/auth"
import {loadMessages} from "../../src/lib/i18n"
import {ArtifactCommentThreads,type ArtifactThread} from "../../src/app/executions/artifact-comments"
import "../../src/index.css"
const source="first line\nRejected code line\nlast line\n"
const sources: Record<string,{name:string;text:string}>={"10":{name:"source.go",text:source},"12":{name:"source.go",text:source},"20":{name:"report.csv",text:"name,result\nBuild,failed\n"},"21":{name:"document.txt",text:"Source paragraph\n\nSecond paragraph"},"22":{name:"preview.html",text:"<html><body><h1>Source HTML region</h1><script>window.fixtureUnsafeScriptRan=true</script></body></html>"}}
const anchor={kind:"CODE_LINES",path:"source.go",version_ref:"file-version:71 sha256:abc",file_id:"70",version_id:"71",sha256:"abc",start_line:2,end_line:2,excerpt:"Rejected code line"}
const threads:ArtifactThread[]=[{id:"90",execution_id:"7",work_item_id:"3",evidence_id:"10",visibility:"INTERNAL",state:"OPEN",last_seq:1,created_by:"11",executor_id:"22",anchor,first_body:"Frozen original comment",anchor_status:"CURRENT"}]
const messages=[{id:"91",thread_id:"90",seq:1,body:"Frozen original comment" as string|null,created_by:"11",created_at:new Date().toISOString(),edit_count:0}]
const writes:unknown[]=[]
Object.assign(window,{fixtureWrites:writes,fixtureSourceReads:[],fixtureUnsafeScriptRan:false})
const nativeFetch=window.fetch.bind(window)
window.fetch=async(input,init)=>{
 const url=new URL(String(input),location.origin)
 if(!url.pathname.includes("/api/"))return nativeFetch(input,init)
 const method=init?.method??"GET"
 const ok=(data:unknown,status=200)=>Response.json({code:0,data},{status})
 if(url.pathname.endsWith("artifact-source")){
  ;(window as unknown as {fixtureSourceReads:string[]}).fixtureSourceReads.push(url.searchParams.get("evidence_id")??"")
  if(url.searchParams.get("evidence_id")==="12")await new Promise(resolve=>setTimeout(resolve,800))
  return ok({...sources[url.searchParams.get("evidence_id")??"10"],version_id:"71",sha256:"abc"})
 }
 if(method==="GET"&&url.pathname.endsWith("artifact-comments"))return ok({items:threads,has_more:false})
 if(method==="GET"&&url.pathname.endsWith("/comments"))return ok({items:messages,last_seq:threads[0].last_seq,has_more:false})
 const body=JSON.parse(String(init?.body??"{}")),headers=new Headers(init?.headers)
 writes.push({method,path:url.pathname,body,key:headers.get("Idempotency-Key"),match:headers.get("If-Match")})
 if(method==="POST"&&url.pathname.endsWith("artifact-comments")){threads.push({...threads[0],id:String(100+threads.length),first_body:body.body,first_retracted:false,anchor:{...anchor,kind:body.anchor_kind,path:sources[body.evidence_id].name}});return ok(threads.at(-1),201)}
 threads[0].last_seq++
 if(method==="POST"&&url.pathname.endsWith("/comments"))messages.push({...messages[0],id:"92",seq:threads[0].last_seq,body:body.body})
 if(method==="PATCH"&&url.pathname.endsWith("/comments/91")){messages[0].body=body.body;messages[0].edit_count++;threads[0].first_body=body.body}
 if(method==="DELETE"){messages[0].body=null;threads[0].first_retracted=true}
 if(url.pathname.endsWith("/comment-threads/90"))threads[0].state=body.state
 if(url.pathname.endsWith("/reanchor")){threads[0].anchor_status="RELOCATED";threads[0].relocated_anchor={...anchor,version_id:"72",version_ref:"file-version:72 sha256:def",start_line:3,end_line:3}}
 return ok({thread_id:"90",last_seq:threads[0].last_seq})
}
const locale=new URLSearchParams(location.search).get("locale")??"en-US",messagesCatalog=await loadMessages(locale)
const readonly=new URLSearchParams(location.search).has("readonly")
const auth={session:{subject:readonly?"33":"11"},status:"authenticated"} as unknown as AuthContextValue
createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={locale} initialMessages={messagesCatalog}><AuthContext.Provider value={auth}><MemoryRouter><Routes><Route element={<Outlet context={{permissions:new Set(readonly?["execution_read"]:["execution_read","artifact_comment_write","work_item_write"]),permissionsResolved:true}}/>}><Route index element={<main className="mx-auto max-w-4xl p-4"><ArtifactCommentThreads tenantID="1" projectID="99" executionID="7" evidence={Object.entries(sources).map(([id,item])=>({id,kind:"FILE",title:item.name+" "+id}))}/></main>}/></Route></Routes></MemoryRouter></AuthContext.Provider></IntlAppProvider>)
