// Explicit API fixtures: verifies browser interaction, not live authentication.
import assert from "node:assert/strict"
import {spawnSync} from "node:child_process"

const origin=process.env.AGENT_KNOWLEDGE_BROWSER_ORIGIN
const binary=process.env.AGENT_BROWSER_BINARY
assert(origin && binary)
const session=`agent-knowledge-details-${process.pid}`
function browser(args,input){
  const r=spawnSync(binary,["--session",session,"--json",...args],{encoding:"utf8",input,windowsHide:true,timeout:45000})
  assert.equal(r.status,0,r.stderr||r.stdout)
  const result=JSON.parse(r.stdout.trim());assert(result.success,JSON.stringify(result.error));return result.data
}
const evaluate=code=>browser(["eval","--stdin"],code).result
async function waitFor(code){
  for(let i=0;i<40;i++){if(evaluate(code))return;await new Promise(r=>setTimeout(r,100))}
  assert.fail(`Browser predicate did not become true: ${code}\n${JSON.stringify(evaluate("({requests:window.fixtureRequests,inputs:Array.from(document.querySelectorAll('input')).map(i=>({type:i.type,value:i.value}))})"))}`)
}
async function open(page,locale="en-US",denied=false){
  const url=new URL("/tests/fixtures/agent-knowledge-browser.html",origin)
  url.search=new URLSearchParams({page,locale,...(denied?{denied:"1"}:{})})
  browser(["open",url.href]);await waitFor("Boolean(document.querySelector('h1') || document.querySelector('[role=alert]'))")
  browser(["snapshot","-i"])
}
const passed=[]
try{
  await open("workflow")
  await waitFor("document.body.innerText.includes('Ready')")
  assert(evaluate("Boolean(document.querySelector('details')) && document.querySelectorAll('table tbody tr').length===2"))
  browser(["click","table tbody tr:first-child td:nth-of-type(2) button"])
  browser(["click","aside input[type=checkbox]"])
  browser(["fill","aside textarea",'[{"kind":"ACTOR_SCOPE","actor_kinds":["HUMAN"]}]'])
  browser(["find","role","button","click","--name","Save","--exact"])
  await waitFor("window.fixtureRequests.some(r=>r.method==='PUT')")
  assert(evaluate("window.fixtureRequests.find(r=>r.method==='PUT').body.transitions.some(t=>t.from==='draft'&&t.to==='ready'&&!t.manual_allowed&&t.conditions[0]?.kind==='ACTOR_SCOPE')"))
  passed.push("workflow matrix saves manual guard and selected-cell conditions")
  await open("quality")
  await waitFor("Boolean(document.querySelector('fieldset'))")
  browser(["click","fieldset input[type=checkbox]"])
  browser(["find","label","Rule title","fill","Human corrective rule"])
  browser(["find","label","Corrective rule","fill","Require a real independent acceptance receipt."])
  browser(["find","role","button","click","--name","Confirm learning","--exact"])
  await waitFor("window.fixtureRequests.some(r=>r.method==='PUT'&&r.url.endsWith('/learning'))")
  assert(evaluate("window.fixtureRequests.find(r=>r.method==='PUT').body.rule_draft.title==='Human corrective rule'"))
  await waitFor("Boolean(document.querySelector('a[href$=\"/governance/memory-review\"]'))")
  passed.push("quality Human draft submits to existing knowledge review destination")
  await open("worklogs")
  await waitFor("document.body.innerText.includes('Actual work fixture')")
  browser(["find","label","Work item","fill","77"])
  // Native date controls use locale-dependent segmented keyboard editing.
  // Set their ISO DOM values and dispatch the same bubbling input/change events.
  for(const [index,value] of [[0,"2026-10-01"],[1,"2026-11-01"]]){
    evaluate(`(()=>{const input=document.querySelectorAll('input[type=date]')[${index}];Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'${value}');input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));})()`)
  }
  await waitFor("['/worklogs?','/worklog-summary?'].every(p=>window.fixtureRequests.some(r=>r.url.includes(p)&&r.url.includes('work_item_id=77')&&r.url.includes('from=2026-10-01')&&r.url.includes('until=2026-11-01')))")
  passed.push("worklog list and approved summary share work-item and half-open period")
  for(const page of ["workflow","quality","worklogs"]){
    await open(page,"en-US",true)
    assert(evaluate("window.fixtureRequests.length===0"))
  }
  passed.push("denied owned pages do not read sources")
  browser(["set","viewport","390","844"])
  for(const page of ["workflow","quality","worklogs"]){
    for(const locale of ["en-US","zh-CN","ms-MY"]){
      await open(page,locale)
      await waitFor("window.fixtureRequests.length>0")
      assert(evaluate("!document.body.innerText.includes('????')"))
      assert(evaluate("document.documentElement.scrollWidth<=window.innerWidth"),`${page}/${locale}: mobile document overflow`)
    }
  }
  passed.push("three locales at mobile width preserve readable document bounds")
}finally{browser(["close"])}
console.log(passed.map(v=>`PASS ${v}`).join("\n"))
