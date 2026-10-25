import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { ExecutionMerge, MergeFacts } from "./merge"
import { mergeLabels, type MergeFact } from "./merge-data"

const fact: MergeFact = {id: "9007199254740993", execution_id: "9007199254740994", attempt: 2, state: "CONFLICT", source_commit: "a".repeat(40), target_branch: "release/stable", push: true, authority_source: "permission:code_merge", reason: "Integrate accepted work", observation: {state: "CONFLICT", pushed: false, reason: "Target advanced", conflict_files: ["src/critical.ts", "src/<unsafe>.ts"]}}

for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) it(`renders measured merge outcomes and permission guards in ${locale}`, async () => {
  const messages = await loadMessages(locale), l = mergeLabels[locale]
  const render = (content: React.ReactNode, permissions: string[] = []) => renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter><Routes><Route element={<Outlet context={{permissions: new Set(permissions), permissionsResolved: true}}/>}><Route index element={content}/></Route></Routes></MemoryRouter></IntlAppProvider>)
  const conflict = render(<MergeFacts rows={[fact]}/>)
  expect(conflict).toContain(fact.id)
  expect(conflict).toContain(fact.source_commit)
  expect(conflict).toContain(fact.target_branch)
  expect(conflict).toContain(fact.authority_source)
  expect(conflict).toContain(l.states.CONFLICT)
  expect(conflict).toContain(l.unpushed)
  expect(conflict).not.toContain(l.pushed)
  expect(conflict).toContain("src/&lt;unsafe&gt;.ts")
  expect(conflict).not.toContain("src/<unsafe>.ts")
  const merged = render(<MergeFacts rows={[{...fact, state: "MERGED", observation: {state: "MERGED", merge_commit: "b".repeat(40), pushed: true, reason: "Remote confirmed"}}]}/>)
  expect(merged).toContain(l.pushed)
  expect(merged).toContain("b".repeat(40))
  expect(merged).not.toContain(l.unpushed)
  const owner = render(<ExecutionMerge executionID={fact.execution_id} state="SUCCEEDED"/>, ["code_merge"])
  expect(owner).toContain(l.request)
  expect(owner).toContain(l.branch)
  expect(owner).toContain(l.reason)
  expect(owner).toContain(l.hint)
  for (const [state, permissions] of [["SUCCEEDED", []], ["RUNNING", ["code_merge"]], ["FAILED", ["code_merge"]]] as const) {
    const guarded = render(<ExecutionMerge executionID={fact.execution_id} state={state}/>, [...permissions])
    expect(guarded).not.toContain("<form")
    expect(guarded).toContain(l.title)
  }
  expect(render(<MergeFacts rows={[]}/>)).toContain(l.empty)
})

it("retries a lost merge response exactly and waits for recorded merge outcomes", async () => {
  const {fileURLToPath} = await import("node:url")
  const {resolve, dirname} = await import("node:path")
  const {existsSync, readFileSync} = await import("node:fs")
  const {spawn} = await import("node:child_process")
  const absolute = (relative: string) => JSON.stringify(fileURLToPath(new URL(relative, import.meta.url)))
  const pending = {...fact, state: "PENDING", observation: undefined}
  const built = await Bun.build({entrypoints: ["merge-browser-entry"], target: "browser", plugins: [{name: "explicit-merge-fixture", setup(build) {
    // Read dependencies explicitly across repeated Windows browser fixture builds.
    build.onLoad({filter: /\.[cm]?js$/, namespace: "file"}, ({path}) => ({contents: readFileSync(path, "utf8"), loader: "js", resolveDir: dirname(path)}))
    build.onResolve({filter: /^merge-browser-entry$/}, () => ({path: "entry", namespace: "merge-fixture"}))
    build.onResolve({filter: /iam-api$/}, () => ({path: "api", namespace: "merge-fixture"}))
    build.onLoad({filter: /.*/, namespace: "merge-fixture"}, ({path}) => ({loader: "tsx", resolveDir: import.meta.dir, contents: path === "api" ? `
      export class ApiError extends Error {}
      window.mergeCalls=[];window.mergeRows=[];
      export const iamApi={deliveryResultRequest:async(route,init)=>{
        if(route.method==='POST') {window.mergeCalls.push({route,body:init.body,headers:init.headers});if(window.mergeCalls.length===1)throw new Error('Lost merge response');window.mergeRows=[${JSON.stringify(pending)}];return window.mergeRows[0];}
        return window.mergeRows;
      }};
    ` : `
      import React from 'react';import {createRoot} from 'react-dom/client';
      import {MemoryRouter,Routes,Route,Outlet} from 'react-router';
      import {IntlAppProvider} from '@workspace/ui/i18n/intl-provider';
      import ui from ${absolute("../../../../../packages/ui/src/i18n/messages/en-US.json")};
      import en from ${absolute("../../messages/en-US.json")};
      import {ExecutionMerge} from ${absolute("./merge.tsx")};
      createRoot(document.getElementById('root')).render(<IntlAppProvider initialLocale="en-US" initialMessages={{...ui,iam:en}}><MemoryRouter><Routes><Route element={<Outlet context={{permissions:new Set(['code_merge']),permissionsResolved:true}}/>}><Route index element={<ExecutionMerge executionID="9007199254740994" state="SUCCEEDED"/>}/></Route></Routes></MemoryRouter></IntlAppProvider>);
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
      await page.getByRole('textbox',{name:'Target branch (blank uses project default)',exact:true}).fill('release/stable');
      await page.getByRole('textbox',{name:'Merge reason',exact:true}).fill('Integrate accepted evidence');
      await page.getByRole('button',{name:'Request merge',exact:true}).click();
      await page.getByText('Lost merge response',{exact:true}).waitFor();
      assert.equal(await page.getByRole('textbox',{name:'Merge reason',exact:true}).count(),0);
      await page.getByRole('button',{name:'Retry exact request',exact:true}).click();
      await page.getByRole('status').filter({hasText:'Merge request recorded'}).waitFor();
      await page.waitForFunction(()=>document.querySelector('ol').textContent.includes('Pending'));
      const calls=await page.evaluate(()=>window.mergeCalls);assert.equal(calls.length,2);assert.deepEqual(calls[0],calls[1]);
      assert.equal(calls[0].route.url,'/executions/9007199254740994/merges');assert.ok(calls[0].headers['Idempotency-Key']);
      assert.deepEqual(JSON.parse(calls[0].body),{target_branch:'release/stable',reason:'Integrate accepted evidence',push:false});
      assert.equal(await page.getByText('Push confirmed',{exact:true}).count(),0);
      assert.equal(await page.getByRole('button',{name:'Request merge',exact:true}).count(),0);
      await page.evaluate(()=>window.mergeRows=[${JSON.stringify(fact)}]);
      await page.getByRole('button',{name:'Refresh merge outcomes',exact:true}).click();
      await page.getByText('Push not confirmed',{exact:false}).waitFor();
      await page.getByText('Target advanced',{exact:false}).waitFor();
      assert.equal(await page.getByRole('button',{name:'Request merge',exact:true}).count(),1);
    }finally{await browser.close();}})().catch(e=>{console.error(e);process.exitCode=1});`
  try {await new Promise<void>((done, reject) => {const child = spawn("node", ["-e", check], {windowsHide: true, stdio: ["ignore", "pipe", "pipe"]}); let output = ""; child.stdout.on("data", chunk => {output += String(chunk)}); child.stderr.on("data", chunk => {output += String(chunk)}); child.on("error", reject); child.on("exit", code => code === 0 ? done() : reject(new Error(output || `Browser exit ${code}`)))})}
  finally {await server.stop(true)}
}, 60_000)
