import { expect, it } from "bun:test"
import { fileURLToPath } from "node:url"
import { existsSync, readFileSync } from "node:fs"
import { resolve, dirname } from "node:path"
import { spawn } from "node:child_process"

it("reconciles another client's action through real SSE and polls the same URL context every ten seconds after denial", async () => {
  const absolute = (path: string) => JSON.stringify(fileURLToPath(new URL(path, import.meta.url)))
  const built = await Bun.build({entrypoints: ["governance-inbox-browser"], target: "browser", plugins: [{name: "inbox-transport-fixture", setup(build) {
    // Bun on Windows can retain a stale file handle across browser fixture builds.
    build.onLoad({filter: /\.[cm]?js$/, namespace: "file"}, ({path}) => ({contents: readFileSync(path, "utf8"), loader: "js", resolveDir: dirname(path)}))
    build.onResolve({filter: /^governance-inbox-browser$/}, () => ({path: "entry", namespace: "fixture"}))
    build.onResolve({filter: /iam-api$/}, () => ({path: "api", namespace: "fixture"}))
    build.onLoad({filter: /.*/, namespace: "fixture"}, ({path}) => ({loader: "tsx", resolveDir: import.meta.dir, contents: path === "api" ? `
      export class ApiError extends Error {}; export const businessCode={captchaInvalid:1}; export const iamApi = {workItem: async()=>{throw new Error("Source details unavailable")},deliveryResultRequest: async (route, init) => {const response=await fetch(route.url,init); if(!response.ok)throw new Error('Read denied');return response.json()}};
    ` : `
      import React from 'react';import {createRoot} from 'react-dom/client';
      import {MemoryRouter,Route,Routes} from 'react-router';
      import {IntlAppProvider} from '@workspace/ui/i18n/intl-provider';
      import {RealtimeProvider} from '@workspace/ui/hooks/use-realtime';
      import {createRealtimeClient} from '@workspace/ui/lib/realtime';
      import ui from ${absolute("../../../../../packages/ui/src/i18n/messages/en-US.json")};
      import en from ${absolute("../../messages/en-US.json")};
      import Page from ${absolute("./page.tsx")};
      const client=createRealtimeClient({url:(tenant,topics)=>'/event-stream?'+new URLSearchParams(topics.map(topic=>['topic',topic])),request:()=>({})});
      window.client=client;
      createRoot(document.getElementById('root')).render(<IntlAppProvider initialLocale="en-US" initialMessages={{...ui,iam:en}}><RealtimeProvider client={client} tenantID="7"><MemoryRouter initialEntries={['/admin/7/governance?kind=WORK_ITEM_REVIEW&project_id=9&cursor=large-string-cursor']}><Routes><Route path="/admin/:tenantID/governance" element={<Page/>}/></Routes></MemoryRouter></RealtimeProvider></IntlAppProvider>);
    `}))
  }}]})
  expect(built.success).toBe(true)
  if (!built.success) throw new Error(built.logs.map(String).join("\n"))
  const script = await built.outputs[0]!.text()
  let pending = true, denied = false, sequence = 0
  const enc = new TextEncoder()
  const streams = new Set<ReadableStreamDefaultController<Uint8Array>>()
  const reads: {time: number; query: string}[] = []
  const server = Bun.serve({hostname: "127.0.0.1", port: 0, idleTimeout: 60, fetch: request => {
    const url = new URL(request.url)
    if (url.pathname === "/fixture.js") return new Response(script, {headers: {"content-type": "text/javascript"}})
    if (url.pathname === "/event-stream") {
      expect(url.searchParams.get("topic")).toBe("tenant/7/governance_inbox")
      return new Response(new ReadableStream<Uint8Array>({start(controller) { streams.add(controller); controller.enqueue(enc.encode(`event: ready\ndata: ${JSON.stringify({accepted: denied ? [] : ["tenant/7/governance_inbox"], denied: denied ? [{topic: "tenant/7/governance_inbox", reason: "forbidden"}] : []})}\n\n`)) }, cancel() { /* Closed when the server is stopped. */ }}), {headers: {"content-type": "text/event-stream"}})
    }
    if (url.pathname.endsWith("/inbox-counts")) return Response.json({total:pending?1:0,by_kind:{WORK_ITEM_REVIEW:pending?1:0},by_priority:{HIGH:pending?1:0},revision:"current",capabilities:[]})
    if (url.pathname.includes("/inbox-items/")) return Response.json({id:"9007199254740993",kind:"WORK_ITEM_REVIEW",priority:"HIGH",created_at:"2026-10-23T12:00:00Z",project_id:"9",title:"Current pending decision",object_type:"work_item",object_id:"9007199254740994"})
    if (url.pathname.endsWith("/inbox-items")) {
      reads.push({time: Date.now(), query: url.search})
      return Response.json({items: pending ? [{id:"9007199254740993",kind:"WORK_ITEM_REVIEW",priority:"HIGH",created_at:"2026-10-23T12:00:00Z",project_id:"9",title:"Current pending decision",object_type:"work_item",object_id:"9007199254740994"}] : [],has_more:false,capabilities:[]})
    }
    if (url.pathname === "/act" && request.method === "POST") {
      pending = false
      const frame=enc.encode(`id: ${++sequence}\nevent: governance.inbox_changed\ndata: {"type":"governance.inbox_changed","topics":["tenant/7/governance_inbox"]}\n\n`)
      for (const controller of streams) {try{controller.enqueue(frame)}catch{streams.delete(controller)}}
      return Response.json({committed: Date.now()})
    }
    if (url.pathname === "/deny" && request.method === "POST") {
      denied = true; pending = true
      for (const controller of streams) {try{controller.enqueue(enc.encode('event: topic_revoked\ndata: {}\n\n'))}catch{streams.delete(controller)}}
      return new Response(null,{status:204})
    }
    if (url.pathname === "/silent-action") {pending=false;return new Response(null,{status:204})}
    if (url.pathname === "/reads") return Response.json(reads)
    return new Response('<html><body><div id="root"></div><script type="module" src="/fixture.js"></script></body></html>', {headers: {"content-type": "text/html"}})
  }})
  const candidates = process.platform === "win32" ? [resolve(process.env["PROGRAMFILES(X86)"] ?? "C:/Program Files (x86)", "Microsoft/Edge/Application/msedge.exe"),resolve(process.env.PROGRAMFILES ?? "C:/Program Files", "Google/Chrome/Application/chrome.exe")] : []
  const check = `const assert=require('node:assert/strict');const {chromium}=require(${JSON.stringify(fileURLToPath(import.meta.resolve("playwright")))});
    (async()=>{const browser=await chromium.launch({headless:true,executablePath:${JSON.stringify(candidates.find(existsSync)) ?? "undefined"}});try{
      const a=await browser.newPage(),b=await browser.newPage();
      await Promise.all([a.goto(${JSON.stringify(server.url.href)}),b.goto(${JSON.stringify(server.url.href)})]);
      await Promise.all([a.getByText('Current pending decision',{exact:true}).waitFor(),b.getByText('Current pending decision',{exact:true}).waitFor()]);
      await Promise.all([a.waitForFunction(()=>window.client.getSnapshot().status==='live'),b.waitForFunction(()=>window.client.getSnapshot().status==='live')]);
      const at=Date.now();await a.evaluate(()=>fetch('/act',{method:'POST'}));
      await b.getByText('Current pending decision',{exact:true}).waitFor({state:'detached'});
      assert(Date.now()-at<1000,'other client must remove resolved source within one second');
      await a.evaluate(()=>fetch('/deny',{method:'POST'}));
      await b.getByText('Current pending decision',{exact:true}).waitFor();
      await b.waitForFunction(()=>window.client.getSnapshot().status==='polling');
      const pollStart=Date.now();await a.evaluate(()=>fetch('/silent-action'));
      await b.getByText('Current pending decision',{exact:true}).waitFor({state:'detached',timeout:12000});
      const delay=Date.now()-pollStart;assert(delay>=8500&&delay<=11500,'disconnected reconciliation uses ten second interval: '+delay);
      const reads=await b.evaluate(()=>fetch('/reads').then(r=>r.json()));assert(reads.length>=6);
      for(const read of reads){const q=new URLSearchParams(read.query);assert.equal(q.get('kind'),'WORK_ITEM_REVIEW');assert.equal(q.get('project_id'),'9');assert.equal(q.get('cursor'),'large-string-cursor');assert.equal(q.get('limit'),'50')}
      console.log('two browser clients: SSE source removal <1s; denied-stream fallback '+delay+'ms; URL context preserved');
    }finally{await browser.close()}})().catch(e=>{console.error(e);process.exitCode=1});`
  try { await new Promise<void>((done,reject)=>{const child=spawn("node",["-e",check],{windowsHide:true,stdio:["ignore","pipe","pipe"]});let output="";child.stdout.on("data",chunk=>{output+=String(chunk)});child.stderr.on("data",chunk=>{output+=String(chunk)});child.on("error",reject);child.on("exit",code=>code===0?done():reject(new Error(output||`Browser exit ${code}`)))}) }
  finally { for(const controller of streams) {try{controller.close()}catch{ /* Already canceled. */ }} await server.stop(true) }
}, 45_000)
