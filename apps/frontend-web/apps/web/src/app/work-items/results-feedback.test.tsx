import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "../../../../../packages/ui/src/i18n/messages/en-US.json"
import en from "../../messages/en-US.json"
import { ResultFeedbackPanel } from "./results-feedback"
import { fileURLToPath } from "node:url"
import { existsSync, readFileSync } from "node:fs"
import { resolve, dirname } from "node:path"
import { spawn } from "node:child_process"

function render(permissions: string[], resolved = true) {
  return renderToStaticMarkup(
    <IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: en }}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route
            element={
              <Outlet
                context={{
                  permissions: new Set(permissions),
                  permissionsResolved: resolved,
                }}
              />
            }
          >
            <Route
              path="/"
              element={
                <ResultFeedbackPanel
                  entityID="3"
                  workID="7"
                  resultID="11"
                  sequence={2}
                />
              }
            />
          </Route>
        </Routes>
      </MemoryRouter>
    </IntlAppProvider>
  )
}

describe("result feedback permissions", () => {
  it("shows scoped discussion and its exact version without offering a composer to a reader", () => {
    const html = render(["work_item_read"])
    expect(html).toContain("Team comments and feedback")
    expect(html).toContain("This action targets version 2:")
    expect(html).not.toContain("Add comment or feedback")
  })
  it("offers composition only with the actual artifact comment permission and work visibility", () => {
    expect(render(["work_item_read", "artifact_comment_write"])).toContain(
      "Add comment or feedback"
    )
    expect(render(["artifact_comment_write"])).not.toContain(
      "Team comments and feedback"
    )
    expect(
      render(["work_item_read", "artifact_comment_write"], false)
    ).not.toContain("Team comments and feedback")
  })
})

it("routes the Inbox secondary action to categorized feedback, preserving the independent check command",async()=>{
 const absolute=(path:string)=>JSON.stringify(fileURLToPath(new URL(path,import.meta.url)))
 const work={id:"7",version:3,current_claim_id:"11",state:"NEEDS_HUMAN_REVIEW",criteria:[]}
 const built=await Bun.build({entrypoints:["inbox-result-feedback-browser"],target:"browser",plugins:[{name:"authorized-source-fixture",setup(build){
    // Bun on Windows can retain a stale file handle across browser fixture builds.
    build.onLoad({filter: /\.[cm]?js$/, namespace: "file"}, ({path}) => ({contents: readFileSync(path, "utf8"), loader: "js", resolveDir: dirname(path)}))
  build.onResolve({filter:/^inbox-result-feedback-browser$/},()=>({path:"entry",namespace:"fixture"}))
  build.onResolve({filter:/iam-api$/},()=>({path:"api",namespace:"fixture"}))
  build.onResolve({filter:/\/(attention|used-knowledge|distillation|recovery|rubric-scores)$/},()=>({path:"unrelated-panels",namespace:"fixture"}))
  build.onLoad({filter:/.*/,namespace:"fixture"},({path})=>({loader:"tsx",resolveDir:import.meta.dir,contents:path==="api"?`
   export class ApiError extends Error {}; export const businessCode={captchaInvalid:1};
   const request=async(route,init)=>{const r=await fetch(route.url,{...init,method:route.method});if(!r.ok)throw new Error('Denied');return r.json()};
   export const iamApi={workItem:async()=>(${JSON.stringify(work)}),session:async()=>({subject:'20'}),workAssignments:async()=>({items:[]}),deliveryResultRequest:request,deliveryResultPage:(route,signal)=>request(route,{signal})};
  `:path==="unrelated-panels"?`export const ReviewAttention=()=>null,UsedKnowledgePanel=()=>null,WorkSkillDistillation=()=>null,WorkRecovery=()=>null,RubricScores=()=>null;`:`
   import React from 'react';import {createRoot} from 'react-dom/client';import {MemoryRouter,Routes,Route,Outlet} from 'react-router';
   import {IntlAppProvider} from '@workspace/ui/i18n/intl-provider';import {WorkResults} from ${absolute("./results.tsx")};
   import ui from ${absolute("../../../../../packages/ui/src/i18n/messages/en-US.json")};import en from ${absolute("../../messages/en-US.json")};
   createRoot(document.getElementById('root')).render(<IntlAppProvider initialLocale="en-US" initialMessages={{...ui,iam:en}}><MemoryRouter><Routes><Route element={<Outlet context={{permissions:new Set(['work_item_read','work_item_write','decision_approve','artifact_comment_write']),permissionsResolved:true}}/>}><Route path="/" element={<WorkResults entityID="9" detail={${JSON.stringify(work)}} surface="INBOX" onUpdated={()=>{}}/>}/></Route></Routes></MemoryRouter></IntlAppProvider>);
  `}))
 }}]})
 expect(built.success).toBe(true);if(!built.success)throw new Error(built.logs.map(String).join("\n"))
 const script=await built.outputs[0]!.text(),calls:{url:string;key:string|null;body:unknown}[]=[]
 const server=Bun.serve({hostname:"127.0.0.1",port:0,async fetch(request){const url=new URL(request.url)
  if(url.pathname==="/fixture.js")return new Response(script,{headers:{"content-type":"text/javascript"}})
  if(request.method==="POST"){calls.push({url:url.pathname,key:request.headers.get("Idempotency-Key"),body:await request.json()});return Response.json({id:"91",result_version_id:"11",actor_id:"20",created_at:"2026-10-23T00:00:00Z"})}
  if(url.pathname.endsWith("/feedback"))return Response.json({items:[],total:0})
  if(url.pathname.endsWith("/results"))return Response.json({items:[{id:"11",work_item_id:"7",project_id:"9",sequence:2,summary:"Current immutable result",actor_id:"19",created_at:"2026-10-23T00:00:00Z",evidence:[],actions:[],handoffs:[]}],total:1})
  return new Response('<html><body><div id="root"></div><script type="module" src="/fixture.js"></script></body></html>',{headers:{"content-type":"text/html"}})
 }})
 const candidates=process.platform==="win32"?[resolve(process.env["PROGRAMFILES(X86)"]??"C:/Program Files (x86)","Microsoft/Edge/Application/msedge.exe"),resolve(process.env.PROGRAMFILES??"C:/Program Files","Google/Chrome/Application/chrome.exe")]:[]
 const check=`const assert=require('node:assert/strict');const {chromium}=require(${JSON.stringify(fileURLToPath(import.meta.resolve("playwright")))});
 (async()=>{const browser=await chromium.launch({headless:true,executablePath:${JSON.stringify(candidates.find(existsSync))??"undefined"}});try{const page=await browser.newPage();await page.goto(${JSON.stringify(server.url.href)});
 const secondary=page.locator('[data-inbox-secondary]');await secondary.waitFor();assert.equal(await secondary.count(),1);assert.equal(await secondary.textContent(),${JSON.stringify(en.resultFeedbackAdd)});await secondary.click();
 await page.getByRole('combobox',{name:${JSON.stringify(en.resultFeedbackCategory)},exact:true}).waitFor();assert.equal(await page.getByText(${JSON.stringify(en.resultDo_check)},{exact:true}).count(),1);
 await page.getByRole('textbox',{name:${JSON.stringify(en.resultFeedbackDetail)},exact:true}).fill('The evidence misses the agreed acceptance case.');await page.getByRole('button',{name:${JSON.stringify(en.resultFeedbackPost)},exact:true}).click();
 await page.getByText(${JSON.stringify(en.resultSaved)},{exact:true}).waitFor();
 }finally{await browser.close()}})().catch(e=>{console.error(e);process.exitCode=1});`
 try{
  await new Promise<void>((done,reject)=>{const child=spawn("node",["-e",check],{windowsHide:true,stdio:["ignore","pipe","pipe"]});let output="";child.stdout.on("data",v=>output+=String(v));child.stderr.on("data",v=>output+=String(v));child.on("error",reject);child.on("exit",code=>code===0?done():reject(new Error(output||`Browser exit ${code}`)))})
  expect(calls).toHaveLength(1);expect(calls[0]!.url).toBe("/entities/9/work-items/7/results/11/feedback");expect(calls[0]!.key).toBeTruthy();expect(calls[0]!.body).toEqual({kind:"FEEDBACK",category:"QUALITY_REJECTION",detail:"The evidence misses the agreed acceptance case."})
 }finally{await server.stop(true)}
},45000)
