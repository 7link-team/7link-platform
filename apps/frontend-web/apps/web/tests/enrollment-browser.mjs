import assert from "node:assert/strict"
import {readFileSync} from "node:fs"
import {spawnSync} from "node:child_process"

const base = process.env.ENROLLMENT_FIXTURE_URL, binary = process.env.AGENT_BROWSER_BINARY
assert(base && ["localhost", "127.0.0.1"].includes(new URL(base).hostname), "isolated local fixture URL required")
assert(binary, "installed agent-browser native binary required")
const session = `machine-enrollment-${process.pid}`
const call = (...args) => {
  const result = spawnSync(binary, ["--session", session, ...args], {encoding: "utf8", windowsHide: true, timeout: 30000})
  assert.equal(result.status, 0, `${args[0]}: ${result.stderr} ${result.stdout}`)
  return result.stdout
}
const check = expression => assert.match(call("eval", `Boolean(${expression})`), /true/, expression)
const click = label => {call("find", "role", "button", "click", "--name", label, "--exact")}
const open = query => {
  call("open", `${base}/tests/fixtures/enrollment-browser.html?${query}`)
  call("wait", "--fn", "document.querySelector('summary') !== null")
  call("click", "summary")
  call("snapshot", "-i")
}
try {
  call("set", "viewport", "390", "844")
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const messages = JSON.parse(readFileSync(new URL(`../src/messages/${locale}.json`, import.meta.url), "utf8"))
    open(`locale=${locale}&user_code=AAAA-CCCC`)
    check("document.querySelector('form input').value === 'AAAA-CCCC'")
    click(messages.fleetInspectEnrollment)
    call("wait", "--text", "observed-build-host")
    check("document.body.innerText.includes('CODEX') && document.body.innerText.includes('1.2.3')")
    check(`document.body.innerText.includes(${JSON.stringify(messages.fleetEnrollmentRuntimeMissing)})`)
    check(`document.body.innerText.includes(${JSON.stringify(messages.fleetEnrollmentReported)})`)
    call("fill", "input[maxlength='100']", "build-01")
    click(messages.fleetApprove)
    call("wait", "--text", messages.fleetEnrollmentConfirmed)
    check("window.enrollmentWrites.length === 1 && window.enrollmentWrites[0].body.name === 'build-01' && window.enrollmentWrites[0].body.user_code === 'AAAA-CCCC' && window.enrollmentWrites[0].body.approve === true")
    check("window.enrollmentWrites[0].path.endsWith('/9007199254740993/decision')")
    call("eval", "Object.assign(window.enrollmentStatus,{state:'COMPLETE',machine_state:'ONLINE'})")
    call("wait", "--text", messages.fleetEnrollmentOnline)
    check("document.querySelector('a')?.getAttribute('href') === '/admin/7/machines/9007199254740993'")
    check("document.documentElement.scrollWidth <= window.innerWidth")
    check("!document.body.innerText.includes('????')")
  }
  const messages = JSON.parse(readFileSync(new URL("../src/messages/en-US.json", import.meta.url), "utf8"))
  open("user_code=XXXX-XXXX")
  click(messages.fleetInspectEnrollment)
  call("wait", "--text", "Enrollment not found")
  check("window.enrollmentWrites.length === 0 && !document.querySelector('input[maxlength=\"100\"]')")
  open("user_code=AAAA-CCCC&decisionFailure")
  click(messages.fleetInspectEnrollment)
  call("wait", "--text", "observed-build-host")
  click(messages.fleetApprove)
  call("wait", "--text", "Decision temporarily unavailable")
  call("eval", "window.enrollmentStatus.state='CANCELLED'")
  call("wait", "--text", messages.fleetEnrollment_CANCELLED)
  check("window.enrollmentWrites.length === 1 && !document.querySelector('input[maxlength=\"100\"]')")
  open("user_code=AAAA-CCCC")
  click(messages.fleetInspectEnrollment)
  call("wait", "--text", "observed-build-host")
  click(messages.fleetDenyEnrollment)
  call("wait", "--text", messages.fleetEnrollment_DENIED)
  check("window.enrollmentWrites[0].body.approve === false && !document.querySelector('input[maxlength=\"100\"]')")
  open("user_code=AAAA-CCCC&expiry")
  click(messages.fleetInspectEnrollment)
  call("wait", "--text", messages.fleetEnrollment_EXPIRED)
  check("window.enrollmentWrites.length === 0 && !document.querySelector('input[maxlength=\"100\"]')")
  open("user_code=AAAA-CCCC")
  click(messages.fleetInspectEnrollment)
  call("wait", "--text", "observed-build-host")
  call("eval", "window.enrollmentStatus.state='CANCELLED'")
  call("wait", "--text", messages.fleetEnrollment_CANCELLED)
  check("window.enrollmentWrites.length === 0 && !document.querySelector('input[maxlength=\"100\"]')")
  console.log("PASS: production code-only lookup, frozen reported runtimes, rename/approval, exact large ID, confirmed/connected/online progress, canonical details link, three locales at390px, unknown code, denial, expiry and machine cancellation; explicit transport fixtures")
} finally {call("close")}
