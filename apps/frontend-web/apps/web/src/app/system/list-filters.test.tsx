import { expect, it } from "bun:test"
import { existsSync, readFileSync } from "node:fs"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { spawn } from "node:child_process"

it("keeps exact instants and IDs while operators, time entry and reset work in both filter modes", async () => {
  const absolute = (path: string) =>
    JSON.stringify(fileURLToPath(new URL(path, import.meta.url)))
  const built = await Bun.build({
    entrypoints: ["list-filter-fixture"],
    target: "browser",
    plugins: [
      {
        name: "list-filter-fixture",
        setup(build) {
          build.onLoad(
            { filter: /\.[cm]?js$/, namespace: "file" },
            ({ path }) => ({
              contents: readFileSync(path, "utf8"),
              loader: "js",
              resolveDir: dirname(path),
            })
          )
          build.onResolve({ filter: /^list-filter-fixture$/ }, () => ({
            path: "entry",
            namespace: "list-filter-fixture",
          }))
          build.onLoad(
            { filter: /.*/, namespace: "list-filter-fixture" },
            () => ({
              loader: "tsx",
              resolveDir: import.meta.dir,
              contents: `
        import React,{useState} from 'react';import {createRoot} from 'react-dom/client';
        import {IntlAppProvider} from '@workspace/ui/i18n/intl-provider';import en from ${absolute("../../../../../packages/ui/src/i18n/messages/en-US.json")};
        import {TableFilterBar} from '@workspace/ui/components/auto-table';
        const seed={owner:'eq:9007199254740993',created:'gte:2026-11-01T09:30:00.123Z',queue:'system:jobs'};
        const filters=[{field:'owner',name:'Owner',type:'string',conditions:['eq']},{field:'created',name:'Created',type:'datetime',conditions:['gte','lt']},{field:'queue',name:'Queue',type:'string',conditions:[]}];
        window.searches=[];
        function Fixture(){const [value,setValue]=useState(seed);return <IntlAppProvider initialLocale="en-US" initialMessages={en}><TableFilterBar filters={filters} defaultValue={seed} value={location.search?value:undefined} onChange={location.search?setValue:undefined} onSearch={value=>window.searches.push(value)}/></IntlAppProvider>}
        createRoot(document.getElementById('root')).render(<Fixture/>);
      `,
            })
          )
        },
      },
    ],
  })
  expect(built.success).toBe(true)
  if (!built.success) throw new Error(built.logs.map(String).join("\n"))
  const script = await built.outputs[0]!.text()
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch: (request) =>
      new URL(request.url).pathname === "/fixture.js"
        ? new Response(script, {
            headers: { "content-type": "text/javascript" },
          })
        : new Response(
            '<html><body><div id="root"></div><script type="module" src="/fixture.js"></script></body></html>',
            { headers: { "content-type": "text/html" } }
          ),
  })
  const candidates =
    process.platform === "win32"
      ? [
          resolve(
            process.env["PROGRAMFILES(X86)"] ?? "C:/Program Files (x86)",
            "Microsoft/Edge/Application/msedge.exe"
          ),
          resolve(
            process.env.PROGRAMFILES ?? "C:/Program Files",
            "Google/Chrome/Application/chrome.exe"
          ),
        ]
      : []
  const check = `const assert=require('node:assert/strict');const {chromium}=require(${JSON.stringify(fileURLToPath(import.meta.resolve("playwright")))});
    (async()=>{const browser=await chromium.launch({headless:true,executablePath:${JSON.stringify(candidates.find(existsSync)) ?? "undefined"}});try{
      for(const mode of ['', '?controlled']){
        const context=await browser.newContext({timezoneId:'America/Los_Angeles'});const page=await context.newPage();
        await page.goto(${JSON.stringify(server.url.href)}+mode);
        const search=page.getByRole('button',{name:'Search',exact:true});await search.waitFor();
        assert.equal(await page.getByRole('textbox',{name:'Owner Value',exact:true}).inputValue(),'9007199254740993');
        await search.click();
        assert.deepEqual(await page.evaluate(()=>window.searches.at(-1)),{owner:'eq:9007199254740993',created:'gte:2026-11-01T09:30:00.123Z',queue:'system:jobs'});
        await page.getByRole('combobox',{name:'Created Condition',exact:true}).click();
        await page.getByRole('option',{name:'Less than / before',exact:true}).click();await search.click();
        assert.equal(await page.evaluate(()=>window.searches.at(-1).created),'lt:2026-11-01T09:30:00.123Z');
        await page.getByRole('button',{name:'Created Value',exact:true}).click();
        await page.locator('input[type=time]').fill('10:31:27');await page.keyboard.press('Escape');await search.click();
        assert.equal(await page.evaluate(()=>window.searches.at(-1).created),'lt:2026-11-01T18:31:27.000Z');
        await page.getByRole('button',{name:'Reset',exact:true}).click();await search.click();
        assert.deepEqual(await page.evaluate(()=>window.searches.at(-1)),{});
        await context.close();
      }
    }finally{await browser.close();}})().catch(e=>{console.error(e);process.exitCode=1});`
  try {
    await new Promise<void>((done, reject) => {
      const child = spawn("node", ["-e", check], {
        windowsHide: true,
        stdio: ["ignore", "pipe", "pipe"],
      })
      let output = ""
      child.stdout.on("data", (chunk) => {
        output += String(chunk)
      })
      child.stderr.on("data", (chunk) => {
        output += String(chunk)
      })
      child.on("error", reject)
      child.on("exit", (code) =>
        code === 0
          ? done()
          : reject(new Error(output || `Browser exit ${code}`))
      )
    })
  } finally {
    await server.stop(true)
  }
}, 60_000)
