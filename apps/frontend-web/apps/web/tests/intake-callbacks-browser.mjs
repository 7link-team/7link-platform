import assert from "node:assert/strict"
import {spawnSync} from "node:child_process"
const base = process.env.INTAKE_CALLBACK_FIXTURE_URL
const binary = process.env.AGENT_BROWSER_BINARY
assert(base && binary)
const origin = new URL(base)
assert(["127.0.0.1","localhost"].includes(origin.hostname))
assert(!["4317","8087","4303","8080","8091","8092"].includes(origin.port))
const session = `intake-callbacks-${process.pid}`
const call = (...args) => {
  const result=spawnSync(binary,["--session",session,...args],{encoding:"utf8",windowsHide:true,timeout:60000})
  assert.equal(result.status,0,`${args[0]}: ${result.stderr} ${result.stdout}`)
  return result.stdout
}
const evaluate = expression => JSON.parse(call("eval",expression))
const open = query => {
  call("open",`${base}/tests/fixtures/intake-callbacks.html?${query}`)
  call("wait","--fn","document.querySelector('main .grid > section:nth-child(3)')!==null")
  return evaluate("window.callbackLabels")
}
try {
  call("set","viewport","390","844")
  for (const locale of ["en-US","zh-CN","ms-MY"]) {
    const labels=open(`locale=${locale}`)
    assert(evaluate(`document.body.innerText.includes(${JSON.stringify(labels.callbackDisclosure)})`))
    call("click","main .grid > section:nth-child(3) button")
    call("find","label",labels.callback,"fill","9007199254740995")
    call("find","role","button","click","--name",labels.save,"--exact")
    call("wait","--fn","window.callbackWrites.length===1")
    assert.equal(evaluate("window.callbackWrites[0].body.callback_subscription_id"),"9007199254740995")
    assert(evaluate("document.documentElement.scrollWidth<=innerWidth"))
    open(`locale=${locale}&readonly`)
    assert.equal(evaluate("document.querySelectorAll('main .grid button').length"),0)
    assert(evaluate("document.body.innerText.includes('9007199254740993')"))
  }
  const labels=open("locale=en-US&lost")
  call("click","main .grid > section:nth-child(3) button")
  call("find","label",labels.callback,"click")
  call("press","Control+a")
  call("press","Backspace")
  assert.equal(evaluate("document.querySelector('[name=callback_subscription_id]').value"), "")
  call("find","role","button","click","--name",labels.save,"--exact")
  call("wait","--text","Simulated lost response")
  call("find","role","button","click","--name",labels.save,"--exact")
  call("wait","--fn","window.callbackWrites.length===2")
  assert(evaluate("window.callbackWrites.every(w=>!('callback_subscription_id' in w.body))"), JSON.stringify(evaluate("window.callbackWrites")))
  assert(evaluate("window.callbackWrites[0].key===window.callbackWrites[1].key && Boolean(window.callbackWrites[0].key)"))
  console.log("PASS: three locales, current configuration, read-only, narrow viewport and lost-response clear replay")
} finally { call("close") }
