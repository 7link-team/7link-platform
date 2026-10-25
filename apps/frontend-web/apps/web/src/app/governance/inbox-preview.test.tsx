import { expect, it, spyOn } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { clarificationApi, type Clarification } from "../project-views/clarification-api"
import { ClarificationSummary } from "./inbox-preview"
import { loadInboxClarification } from "./inbox-source-data"
import type { InboxItem } from "./api"
import en from "../../messages/en-US.json"
import { fileURLToPath } from "node:url"
import { existsSync, readFileSync } from "node:fs"
import { resolve, dirname } from "node:path"
import { spawn } from "node:child_process"

it("loads the exact clarification round across pages and renders canonical readonly questions and answers",async()=>{
 const item:InboxItem={id:"101",object_id:"102",project_id:"9",kind:"CLARIFICATION_REPLY",priority:"HIGH",title:"Reply",created_at:"2026-10-23T00:00:00Z",object_type:"intake"}
 const round:Clarification={id:"101",intake_id:"102",version:3,state:"ANSWERED",created_at:item.created_at,recipient_id:"8",available_via:"IN_APP",timing_available:true,reminders_sent:0,questions:[{number:1,state:"ANSWERED",text:"Which agreed scope?",answer:"Only the signed scope.\nKeep the exact wording.",answered_at:item.created_at}]}
 const list=spyOn(clarificationApi,"list").mockResolvedValueOnce({items:[{...round,id:"100"}],has_more:true,next_cursor:"page-two"}).mockResolvedValueOnce({items:[round],has_more:false})
 try{
  const signal=new AbortController().signal
  expect(await loadInboxClarification(item,signal)).toEqual(round)
  expect(list.mock.calls).toEqual([["9","",signal],["9","page-two",signal]])
  const html=renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={{iam:en}}><ClarificationSummary round={round}/></IntlAppProvider>)
  expect(html).toContain("Which agreed scope?");expect(html).toContain("Only the signed scope.\nKeep the exact wording.");expect(html).not.toContain("<form");expect(html).not.toContain("<button")
  list.mockReset().mockResolvedValue({items:[{...round,intake_id:"999"}],has_more:false})
  await expect(loadInboxClarification(item,signal)).rejects.toThrow("no longer available")
  list.mockReset().mockRejectedValue(new Error("permission revoked"))
  await expect(loadInboxClarification(item,signal)).rejects.toThrow("permission revoked")
 }finally{list.mockRestore()}
})

it("two-pane preview supports J/K, A/R then Enter, Escape, reason validation, failed same-key retry and source removal",async()=>{
 const absolute=(path:string)=>JSON.stringify(fileURLToPath(new URL(path,import.meta.url)))
 const built=await Bun.build({entrypoints:["inbox-preview-browser"],target:"browser",plugins:[{name:"current-source-transport",setup(build){
    // Read dependencies explicitly across repeated Windows browser fixture builds.
    build.onLoad({filter: /\.[cm]?js$/, namespace: "file"}, ({path}) => ({contents: readFileSync(path, "utf8"), loader: "js", resolveDir: dirname(path)}))
  build.onResolve({filter:/^inbox-preview-browser$/},()=>({path:"entry",namespace:"fixture"}))
  build.onResolve({filter:/iam-api$/},()=>({path:"api",namespace:"fixture"}))
  build.onLoad({filter:/.*/,namespace:"fixture"},({path})=>({loader:"tsx",resolveDir:import.meta.dir,contents:path==="api"?`
   export class ApiError extends Error {}; export const businessCode={captchaInvalid:1}; export const iamApi={deliveryResultRequest:async(route,init)=>{const r=await fetch(route.url,{...init,method:route.method});if(!r.ok)throw new Error('Current source temporarily unavailable');return r.json()}};
  `:`
   import React from 'react';import {createRoot} from 'react-dom/client';import {MemoryRouter,Routes,Route} from 'react-router';
   import {IntlAppProvider} from '@workspace/ui/i18n/intl-provider';import Page from ${absolute("./page.tsx")};
   import ui from ${absolute("../../../../../packages/ui/src/i18n/messages/en-US.json")};import en from ${absolute("../../messages/en-US.json")};
   createRoot(document.getElementById('root')).render(<IntlAppProvider initialLocale="en-US" initialMessages={{...ui,iam:en}}><MemoryRouter initialEntries={['/admin/7/governance']}><Routes><Route path="/admin/:tenantID/governance" element={<Page/>}/></Routes></MemoryRouter></IntlAppProvider>);
  `}))
 }}]})
 expect(built.success).toBe(true);if(!built.success)throw new Error(built.logs.map(String).join("\n"))
 const script=await built.outputs[0]!.text(),calls:{key:string|null;body:unknown}[]=[]
 let removed=false
 const items=()=>[...(removed?[]:[{id:"101",object_id:"101",title:"First pending rule"}]),{id:"102",object_id:"102",title:"Second pending rule"}].map(row=>({...row,kind:"MEMORY_RULE_APPROVAL",priority:"NORMAL",project_id:"9",created_at:"2026-10-23T00:00:00Z",object_type:"memory"}))
 const server=Bun.serve({hostname:"127.0.0.1",port:0,async fetch(request){const url=new URL(request.url)
  if(url.pathname==="/fixture.js")return new Response(script,{headers:{"content-type":"text/javascript"}})
  if(url.pathname.endsWith("/inbox-items"))return Response.json({items:items(),has_more:false,capabilities:[]})
  if(url.pathname.endsWith("/inbox-counts"))return Response.json({total:items().length,by_kind:{MEMORY_RULE_APPROVAL:items().length},by_priority:{NORMAL:items().length},revision:"observed",capabilities:[]})
  if(url.pathname.includes("/inbox-items/"))return Response.json(items().find(row=>decodeURIComponent(url.pathname).endsWith(":"+row.id)))
  if(url.pathname.endsWith("/reviews")&&request.method==="POST"){calls.push({key:request.headers.get("Idempotency-Key"),body:await request.json()});if(calls.length===1)return new Response(null,{status:503});removed=true;return Response.json({id:"101",version:5,state:"APPROVED"})}
  if(url.pathname.includes("/knowledge/"))return Response.json({id:url.pathname.split("/").at(-1),version:4,state:"PENDING_REVIEW",title:"Frozen source rule",body:"Exact current rule content "+url.pathname.split("/").at(-1),project_id:"9",conflict_memory_ids:["999"]})
  return new Response('<html><body><div id="root"></div><script type="module" src="/fixture.js"></script></body></html>',{headers:{"content-type":"text/html"}})
 }})
 const candidates=process.platform==="win32"?[resolve(process.env["PROGRAMFILES(X86)"]??"C:/Program Files (x86)","Microsoft/Edge/Application/msedge.exe"),resolve(process.env.PROGRAMFILES??"C:/Program Files","Google/Chrome/Application/chrome.exe")]:[]
 const check=`const assert=require('node:assert/strict');const {chromium}=require(${JSON.stringify(fileURLToPath(import.meta.resolve("playwright")))});
 (async()=>{const browser=await chromium.launch({headless:true,executablePath:${JSON.stringify(candidates.find(existsSync))??"undefined"}});try{const page=await browser.newPage({viewport:{width:390,height:844}});await page.goto(${JSON.stringify(server.url.href)});
 await page.getByText('Exact current rule content 101',{exact:true}).waitFor();const root=page.locator('div[tabindex="0"]').first();
 await root.focus();await page.keyboard.press('j');await page.getByText('Exact current rule content 102',{exact:true}).waitFor();
 await page.keyboard.press('k');await page.getByText('Exact current rule content 101',{exact:true}).waitFor();
 await root.focus();await page.keyboard.press('a');assert.equal(await page.locator('[role="alert"]').count(),1);await page.keyboard.press('Escape');assert.equal(await page.locator('[role="alert"]').count(),0);
 await page.keyboard.press('a');await page.keyboard.press('Enter');assert.equal(await page.locator('[data-inbox-primary]').first().isDisabled(),true);
 await page.getByRole('textbox',{name:'Reason',exact:true}).fill('Reviewed the precise frozen rule');assert.equal(await page.locator('[data-inbox-primary]').first().isDisabled(),true);const conflict=page.getByRole('checkbox',{name:'I reviewed the conflicting memories',exact:true});assert.equal(await conflict.isChecked(),false);await conflict.check();assert.equal(await page.locator('[data-inbox-primary]').first().isEnabled(),true);await root.focus();await page.keyboard.press('r');await page.keyboard.press('Escape');await page.keyboard.press('a');await page.keyboard.press('Enter');
 await page.getByText('Current source temporarily unavailable',{exact:true}).waitFor();await root.focus();await page.keyboard.press('a');await page.keyboard.press('Enter');
 await page.getByText('First pending rule',{exact:true}).waitFor({state:'detached'});await page.getByText('Second pending rule',{exact:true}).waitFor();
 console.log('keyboard two-step, reason validation, same-key transport retry, current-source removal PASS');
 }finally{await browser.close()}})().catch(e=>{console.error(e);process.exitCode=1});`
 try{await new Promise<void>((done,reject)=>{const child=spawn("node",["-e",check],{windowsHide:true,stdio:["ignore","pipe","pipe"]});let output="";child.stdout.on("data",v=>output+=String(v));child.stderr.on("data",v=>output+=String(v));child.on("error",reject);child.on("exit",code=>code===0?done():reject(new Error(output||`Browser exit ${code}`)))})
  expect(calls).toHaveLength(2);expect(calls[0]!.key).toBeTruthy();expect(calls[1]!.key).toBe(calls[0]!.key);expect(calls[0]!.body).toEqual({version:4,action:"APPROVED",reason:"Reviewed the precise frozen rule",conflicts_acknowledged:true});expect(calls[1]!.body).toEqual(calls[0]!.body)
 }finally{await server.stop(true)}
},45000)
