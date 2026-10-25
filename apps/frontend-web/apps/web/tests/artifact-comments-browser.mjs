import assert from "node:assert/strict"
import {spawnSync} from "node:child_process"
const base=process.env.ARTIFACT_COMMENT_FIXTURE_URL,binary=process.env.AGENT_BROWSER_BINARY
assert(base&&["localhost","127.0.0.1"].includes(new URL(base).hostname),"isolated local fixture URL required")
assert(binary,"installed agent-browser native binary required")
const session=`artifact-comments-${process.pid}`
const call=(...args)=>{const result=spawnSync(binary,["--session",session,...args],{encoding:"utf8",windowsHide:true,timeout:30000});assert.equal(result.status,0,`${args[0]}: ${result.stderr} ${result.stdout}`);return result.stdout}
const click=selector=>{call("scrollintoview",selector);call("click",selector)}
const check=expression=>{const result=call("eval",`Boolean(${expression})`);assert.match(result,/true/,expression)}
try{
 call("open",`${base}/tests/fixtures/artifact-comments-browser.html`)
 call("wait","--text","Frozen original comment")
 call("snapshot","-i")
 click("summary")
 call("wait","--text","first line")
 check("window.fixtureSourceReads.includes('10')")
 call("find","role","button","click","--name","Thread conversation","--exact")
 call("wait","--text","Authors can edit")
 call("find","role","button","click","--name","Edit","--exact")
 call("fill","li textarea","Edited original comment")
 call("find","role","button","click","--name","Save","--exact")
 call("wait","--text","Edited original comment")
 call("fill","li textarea","A reply from the browser")
 call("find","role","button","click","--name","Reply","--exact")
 call("wait","--text","A reply from the browser")
 call("find","role","button","click","--name","Resolve thread","--exact")
 call("wait","--text","Reopen thread")
 call("find","role","button","click","--name","Reopen thread","--exact")
 call("wait","--text","Resolve thread")
 call("select","li select","12")
 click("li form:last-child button[type=submit]")
 call("wait","--text","file-version:72")
 click("li ol li:first-child button:last-child")
 click("[role=alertdialog] button:first-child")
 call("wait","--text","Comment withdrawn")
 check("window.fixtureWrites.some(r=>r.method==='DELETE'&&r.match)")
 check("window.fixtureWrites.every(r=>r.key)")
 check("document.querySelector('a[href*=comment_thread]')?.getAttribute('href').includes('comment_thread=90')")
 // An in-flight source switch must clear the old picker and disable submit.
 call("select","section > form > label:first-of-type select","10")
 call("wait","--fn","document.querySelector('[data-source-line]') !== null")
 call("select","section > form > label:first-of-type select","12")
 check("document.querySelector('section > form button[type=submit]').disabled")

 call("wait","--fn","document.querySelector('[data-source-line]') !== null")
 const submit=body=>{call("fill","section > form textarea",body);click("section > form button[type=submit]");call("wait","--text",body)}
 click("[data-source-line='2']")
 submit("New code anchored comment")
 for(const [id,kind,pick] of [["20","TABLE_ROW","[data-source-row='0']"],["21","TEXT_PARAGRAPH","[data-source-paragraph='0']"],["22","HTML_REGION","[data-source-region]"]]){
  call("select","section > form > label:first-of-type select",id)
  call("select","section > form > label:nth-of-type(2) select",kind)
  call("wait","--fn",`document.querySelector(${JSON.stringify(pick)}) !== null`)
  if(kind==="HTML_REGION"){
   call("wait","--fn","document.querySelector('iframe')?.contentDocument?.body?.innerText.includes('Source HTML region')")
   call("focus",pick);call("press","Enter")
  }else click(pick)
  submit(`New ${kind} anchored comment`)
 }
 check("JSON.stringify(window.fixtureWrites.filter(r=>r.path.endsWith('artifact-comments')).map(r=>r.body.anchor_kind)) === JSON.stringify(['CODE_LINES','TABLE_ROW','TEXT_PARAGRAPH','HTML_REGION'])")
 check("window.fixtureWrites.find(r=>r.body.anchor_kind==='TABLE_ROW').body.row_index===0")
 check("window.fixtureWrites.find(r=>r.body.anchor_kind==='TEXT_PARAGRAPH').body.paragraph_index===0")
 check("window.fixtureWrites.find(r=>r.body.anchor_kind==='HTML_REGION').body.rect.w>0")
 check("window.fixtureUnsafeScriptRan===false")
 call("set","viewport","390","844")
 check("document.documentElement.scrollWidth<=window.innerWidth")
 for(const locale of ["en-US","zh-CN","ms-MY"]){
  call("open",`${base}/tests/fixtures/artifact-comments-browser.html?readonly&locale=${locale}`)
  call("wait","--fn","document.querySelector('section ul li') !== null")
  check("!document.querySelector('section > form')")
  check("!document.querySelector('a[href*=comment_thread]')")
 }
 console.log("PASS: four native anchor submissions, production conversation edit/reply/resolve/reopen/reanchor/withdraw, original source, feedback link, source-switch fence, three-locale readonly and mobile containment")
}catch(error){console.error(call("snapshot"));console.error(call("eval","JSON.stringify({writes:window.fixtureWrites,body:document.body.innerText})"));throw error}finally{call("close")}
