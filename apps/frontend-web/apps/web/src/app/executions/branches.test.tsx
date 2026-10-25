import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { BranchHistoryFacts, ExecutionBranches } from "./branches"
import { branchMessages, type Branch } from "./branches-data"
const source = {id: "9007199254740993", project_id: "9", work_item_id: "3", version: 4, branch_reason: "QUALITY", parent_execution_id: "9007199254740992", state: "SUCCEEDED", plan: {objective: "Source objective", base_commit: "a".repeat(40)}} as Branch
for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) it(`renders truthful paged ancestry, URL comparison and guarded strategy in ${locale}`, async () => {
  const messages = await loadMessages(locale), l = branchMessages[locale]
  const render = (content: React.ReactNode, permissions: string[]) => renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter initialEntries={["/?tab=branches&compare_branch=9007199254740994"]}><Routes><Route element={<Outlet context={{permissions: new Set(permissions), permissionsResolved: true}}/>}><Route index element={content}/></Route></Routes></MemoryRouter></IntlAppProvider>)
  const child = {...source, id: "9007199254740994", parent_execution_id: source.id!, branch_reason: "STRATEGY"}
  const facts = render(<BranchHistoryFacts page={{items: [source, child], has_more: true, next_cursor: child.id}} tenantID="7" source={source}/>, [])
  expect(facts).toContain(source.parent_execution_id!); expect(facts).toContain(l.parent); expect(facts).toContain('aria-pressed="true"'); expect(facts).toContain("STRATEGY"); expect(facts).not.toContain(l.root); expect(facts).toContain("9007199254740994"); expect(facts).toContain("/admin/7/entities/9/executions/")
  const owner = render(<ExecutionBranches tenantID="7" source={source}/>, ["execution_start"])
  expect(owner).toContain(l.create); expect(owner).toContain(l.baseline); expect(owner).toContain(l.reason); expect(owner).toContain(l.objective)
  const reader = render(<ExecutionBranches tenantID="7" source={source}/>, [])
  expect(reader).not.toContain(l.create); expect(reader).not.toContain("<form"); expect(reader).toContain(l.title)
})

// Exercise the production component in Chromium. Transport facts are explicit
// fixtures; the composed Go test owns real server/IAM acceptance.
it("keeps exact retry identity after a lost response and restores comparison in the URL", async () => {
  const {fileURLToPath} = await import("node:url")
  const {resolve} = await import("node:path")
  const {existsSync} = await import("node:fs")
  const {spawn} = await import("node:child_process")
  const absolute = (relative: string) => JSON.stringify(fileURLToPath(new URL(relative, import.meta.url)))
  const child = {...source, id: "9007199254740994", parent_execution_id: source.id!, branch_reason: "STRATEGY"}
  const built = await Bun.build({entrypoints: ["branches-browser-entry"], target: "browser", plugins: [{name: "explicit-branch-fixture", setup(build) {
    build.onResolve({filter: /^branches-browser-entry$/}, () => ({path: "entry", namespace: "branch-fixture"}))
    build.onResolve({filter: /iam-api$/}, () => ({path: "api", namespace: "branch-fixture"}))
    build.onLoad({filter: /.*/, namespace: "branch-fixture"}, ({path}) => ({loader: "tsx", resolveDir: import.meta.dir, contents: path === "api" ? `
      export class ApiError extends Error {}
      const source=${JSON.stringify(source)}, child=${JSON.stringify(child)};
      window.branchCalls=[];
      export const iamApi={execution:async()=>({execution:child}),deliveryResultRequest:async(route,init)=>{
        if(route.method==='POST') {window.branchCalls.push({route,body:init.body,headers:init.headers});if(window.branchCalls.length===1)throw new Error('Lost response');return child;}
        if(route.url.includes('evidence-provenance'))return {items:[],has_more:false,verifiers:[],comparison_truncated:false,verifiers_truncated:false,limitations:[]};
        return {items:[source,child],has_more:false};
      }};
    ` : `
      import React from 'react';import {createRoot} from 'react-dom/client';
      import {MemoryRouter,Routes,Route,Outlet,useLocation} from 'react-router';
      import {IntlAppProvider} from '@workspace/ui/i18n/intl-provider';
      import ui from ${absolute("../../../../../packages/ui/src/i18n/messages/en-US.json")};
      import en from ${absolute("../../messages/en-US.json")};
      import {ExecutionBranches} from ${absolute("./branches.tsx")};
      function Shell(){return <Outlet context={{permissions:new Set(['execution_start']),permissionsResolved:true}}/>}
      function Page(){const location=useLocation();return <><output id="current-url">{location.search}</output><ExecutionBranches tenantID="7" source={${JSON.stringify(source)}}/></>}
      createRoot(document.getElementById('root')).render(<IntlAppProvider initialLocale="en-US" initialMessages={{...ui,iam:en}}><MemoryRouter initialEntries={['/?tab=branches']}><Routes><Route element={<Shell/>}><Route index element={<Page/>}/></Route></Routes></MemoryRouter></IntlAppProvider>);
    `}))
  }}]})
  expect(built.success).toBe(true)
  if (!built.success) throw new Error(built.logs.map(String).join("\n"))
  const script = await built.outputs[0]!.text()
  const server = Bun.serve({hostname: "127.0.0.1", port: 0, fetch: request => new URL(request.url).pathname === "/fixture.js" ? new Response(script, {headers: {"content-type": "text/javascript"}}) : new Response('<html><body><div id="root"></div><script type="module" src="/fixture.js"></script></body></html>', {headers: {"content-type": "text/html"}})})
  const candidates = process.platform === "win32" ? [resolve(process.env["PROGRAMFILES(X86)"] ?? "C:/Program Files (x86)", "Microsoft/Edge/Application/msedge.exe"), resolve(process.env.PROGRAMFILES ?? "C:/Program Files", "Google/Chrome/Application/chrome.exe")] : []
  const check = `const assert=require('node:assert/strict');const {chromium}=require(${JSON.stringify(fileURLToPath(import.meta.resolve("playwright")))});
    (async()=>{const browser=await chromium.launch({headless:true,executablePath:${JSON.stringify(candidates.find(existsSync)) ?? "undefined"}});try{
      const page=await browser.newPage({viewport:{width:390,height:844}});await page.goto(${JSON.stringify(server.url.href)});
      await page.getByRole('button',{name:'Compare evidence',exact:true}).click();
      await page.waitForFunction(()=>document.querySelector('#current-url').textContent.includes('compare_branch=9007199254740994'));
      await page.getByRole('heading',{name:'Alternative execution: 9007199254740994',exact:true}).waitFor();
      await page.getByRole('textbox',{name:'Reason',exact:true}).fill('Try a different algorithm');
      await page.getByRole('textbox',{name:'Alternative objective',exact:true}).fill('Preserve the frozen baseline');
      await page.getByRole('button',{name:'Create strategy branch',exact:true}).click();
      await page.getByText('Lost response',{exact:true}).waitFor();
      assert.equal(await page.getByRole('textbox',{name:'Reason',exact:true}).count(),0);
      await page.getByRole('button',{name:'Retry exact request',exact:true}).click();
      await page.getByRole('status').filter({hasText:'Strategy branch created'}).waitFor();
      const calls=await page.evaluate(()=>window.branchCalls);assert.equal(calls.length,2);assert.deepEqual(calls[0],calls[1]);
      assert.equal(calls[0].route.url,'/executions/9007199254740993/branches');assert.equal(calls[0].headers['If-Match'],'"4"');
      assert.deepEqual(JSON.parse(calls[0].body),{reason:'Try a different algorithm',objective:'Preserve the frozen baseline'});
    }finally{await browser.close();}})().catch(e=>{console.error(e);process.exitCode=1});`
  try {await new Promise<void>((done, reject) => {const child = spawn("node", ["-e", check], {windowsHide: true, stdio: ["ignore", "pipe", "pipe"]}); let output = ""; child.stdout.on("data", chunk => {output += String(chunk)}); child.stderr.on("data", chunk => {output += String(chunk)}); child.on("error", reject); child.on("exit", code => code === 0 ? done() : reject(new Error(output || `Browser exit ${code}`)))})}
  finally {await server.stop(true)}
}, 60_000)
