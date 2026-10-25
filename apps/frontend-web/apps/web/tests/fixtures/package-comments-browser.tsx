// Explicit browser contract fixtures; production IAM/Files/receipt persistence
// is tested separately by the composed real-provider backend test.
import {useEffect,useState} from "react"
import {createRoot} from "react-dom/client"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../src/lib/i18n"
import {PackageComments,RefusalAnchors} from "../../src/app/project-views/package-comments"
import type {ClientPackageThread,ClientRefusalAnchor} from "../../src/lib/clientwork-api"
import "../../src/index.css"

const files=[{name:"source.go",text:"first line\nRejected code line\nlast line\n"},{name:"report.csv",text:'name,result\nBuild,failed\nTests,passed\n'},{name:"document.txt",text:"Rejected source paragraph\n\nA second paragraph"},{name:"preview.html",text:'<!doctype html><html><body><h1>Rejected HTML region</h1><p>Read only source preview</p><script>window.fixtureUnsafeScriptRan=true</script></body></html>'}]
const artifacts=await Promise.all(files.map(async(file,index)=>({...file,version_id:String(index+1),item:String.fromCharCode(97+index).repeat(64),sha256:Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256",new TextEncoder().encode(file.text))),b=>b.toString(16).padStart(2,"0")).join("")})))
const threads=new Map<string,ClientPackageThread[]>(),writes:unknown[]=[]
Object.assign(window,{fixtureWrites:writes,fixtureUnsafeScriptRan:false})
const nativeFetch=window.fetch.bind(window)
window.fetch=async(input,init)=>{
 const url=new URL(String(input),location.origin);if(!url.pathname.includes("/api/"))return nativeFetch(input,init)
 const artifact=artifacts.find(row=>url.pathname.includes(`/artifacts/${row.version_id}/content`))
 if(artifact)return new Response(artifact.text,{headers:{"content-type":artifact.name.endsWith(".html")?"text/html":"text/plain","cache-control":"private, no-store"}})
 const item=url.pathname.match(/\/items\/([a-f0-9]{64})\/comments$/)?.[1]
 if(!item)return Response.json({code:127404,message:"Unconfigured fixture"},{status:404})
 if((init?.method??"GET")==="POST"){
  const body=JSON.parse(String(init?.body));writes.push(body);const source=artifacts.find(row=>row.item===item)!
  const excerpt=body.anchor_kind==="CODE_LINES"?source.text.split("\n").slice(body.start_line-1,body.end_line).join("\n"):body.anchor_kind==="TABLE_ROW"?'["Build","failed"]':body.text_quote
  const row:ClientPackageThread={thread_id:String(90+writes.length),anchor_kind:body.anchor_kind,anchor_excerpt:excerpt,version_ref:`file-version:${source.version_id} sha256:${source.sha256}`,artifact_ref:{version_id:source.version_id,sha256:source.sha256},anchor:body,body:body.body}
  threads.set(item,[...(threads.get(item)??[]),row]);window.dispatchEvent(new Event("fixture-anchor-written"));return Response.json({code:0,data:{id:row.thread_id,version:1,state:"OPEN"}},{status:201})
 }
 return Response.json({code:0,data:{items:threads.get(item)??[],has_more:false}})
}
export function Fixture(){const [anchors,setAnchors]=useState<ClientRefusalAnchor[]>([]);useEffect(()=>{const update=()=>setAnchors([...threads.values()].flat());window.addEventListener("fixture-anchor-written",update);return()=>window.removeEventListener("fixture-anchor-written",update)},[])
 return <main className="mx-auto grid max-w-5xl gap-8 p-4">{artifacts.map(artifact=><article key={artifact.item} data-fixture-item={artifact.version_id} className="grid min-w-0 gap-3 border p-4"><h2>{artifact.name}</h2><PackageComments project="3" delivery="8" item={artifact.item} artifact={artifact} external published/></article>)}<section data-fixture-frozen><RefusalAnchors anchors={anchors}/></section></main>}
const locale=new URLSearchParams(location.search).get("locale")??"en-US",messages=await loadMessages(locale)
createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={locale} initialMessages={messages}><Fixture/></IntlAppProvider>)
