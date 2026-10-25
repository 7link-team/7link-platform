import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { readFileSync } from "node:fs"

// Run only against a disposable copied API database. Credentials stay file-backed.
assert.equal(process.env.WORKSPACE_ACCEPTANCE_DISPOSABLE, "true")
const input = JSON.parse(readFileSync(process.env.WORKSPACE_ACCEPTANCE_INPUT, "utf8"))
const origin = new URL(input.origin)
assert(["127.0.0.1", "localhost"].includes(origin.hostname))
assert(!["4317", "8087", "4303", "8080", "8091", "8092"].includes(origin.port))
const session = `workspace-selection-${process.pid}`
const command = (...args) => {
  try {
    return execFileSync(process.env.AGENT_BROWSER_BINARY || "bun", process.env.AGENT_BROWSER_BINARY
      ? ["--session", session, ...args]
      : ["x", "agent-browser", "--session", session, ...args], {encoding: "utf8", windowsHide: true, timeout: 180_000, stdio: args[0] === "open" ? "ignore" : "pipe"})
  } catch {
    // CLI errors can contain fill arguments. Keep file-backed credentials out of logs.
    throw new Error(`agent-browser ${args[0]} failed`)
  }
}
const evaluate = source => JSON.parse(command("eval", source))
const eventually = async (source, description) => {
  for (let attempt = 0; attempt < 120; attempt++) {
    if (evaluate(source)) return
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  throw new Error(`Timed out: ${description}`)
}
try {
  command("open", `${origin.origin}/admin/login`)
  command("set", "viewport", "1440", "1000")
  await eventually("Boolean(document.querySelector('#login-name'))", "sign-in form")
  // Verify the development entrance tabs are enabled, so their later absence is meaningful.
  assert(evaluate("Boolean(document.querySelector('a[href=\"/system/login\"]'))"))
  if (input.prefilled) {
    assert(evaluate(`document.querySelector('#login-name').value===${JSON.stringify(input.login)} && document.querySelector('#password').value.length>0`), "real demo account must already be prefilled")
  } else {
    command("fill", "#login-name", input.login)
    command("fill", "#password", input.password)
  }
  command("click", 'button[type="submit"]')
  await eventually("!location.pathname.endsWith('/login')", "real form sign-in")
  // Exercise the original copied memberships before any data mutation. Creating
  // a tenant first can make this test select a fresh, unrelated data shape.
  await eventually("document.querySelectorAll('main a[href^=\"/admin/\"]').length>=2", "workspace choices")
  assert.equal(evaluate("document.querySelectorAll('a[href$=\"/login\"]').length"), 0)
  const choices = evaluate("Array.from(document.querySelectorAll('main a[href^=\"/admin/\"]'),link=>link.getAttribute('href'))")
  const destination = input.tenantID ? `/admin/${input.tenantID}` : choices[0]
  assert(choices.includes(destination), "copied demo tenant must be offered")
  const other = choices.find(path => path !== destination)
  assert(other, "copied database needs a second original membership")
  evaluate(`(()=>{
    window.workspaceRequests=[];
    const original=window.fetch;
    window.fetch=async(...args)=>{const start=performance.now();const response=await original(...args);window.workspaceRequests.push({url:String(args[0]),status:response.status,elapsed:performance.now()-start});return response};
    return true;
  })()`)
  // Exactly one ordinary user click; the same tab must reach the tenant.
  const selectedAt = Date.now()
  command("click", `main a[href="${destination}"]`)
  await eventually(`location.pathname===${JSON.stringify(destination)}`, "destination URL commits before permission discovery")
  const navigationMs = Date.now() - selectedAt
  assert(navigationMs < 3000, "a slow permissions response must not pin the chooser URL")
  await eventually(`location.pathname===${JSON.stringify(destination)} && document.querySelector('main h1')!==null`, "one-click tenant dashboard content")
  await new Promise(resolve => setTimeout(resolve, 1500))
  assert.equal(evaluate("location.pathname"), destination)
  const requests = evaluate("window.workspaceRequests")
  const entityRequests = requests.filter(row => /\/principals\/[^/]+\/entities\?/.test(row.url))
  assert(entityRequests.every(row => !row.url.includes("?permission=")), "workspace discovery must use the batch")
  assert(requests.every(row => row.status !== 503), "workspace entry must not return transient503")
  const deployment = evaluate("fetch('/api/deployment').then(async r=>({status:r.status,code:(await r.json()).code}))")
  assert.equal(deployment.status, 200)
  assert.equal(deployment.code, 0)
  const batch = evaluate(`(async()=>{
    const me=(await(await fetch('/api/me')).json()).data;
    const path='/api/tenants/'+${JSON.stringify(destination.split("/")[2])}+'/principals/'+me.subject+'/entities';
    const result=[];
    for(const query of ['permissions=file_view,project_read','permission=file_view','permission=project_read']){
      const r=await fetch(path+'?'+query);const body=await r.json();result.push({status:r.status,ids:(body.data??[]).map(row=>row.id).sort()});
    }
    return result;
  })()`)
  assert(batch.every(row => row.status === 200))
  assert.deepEqual(batch[0].ids, [...new Set([...batch[1].ids, ...batch[2].ids])].sort())
  // Change to a different tenant as well, so a remembered selection cannot hide a switch race.
  command("open", `${origin.origin}/admin/workspaces`)
  await eventually(`Boolean(document.querySelector('main a[href="${other}"]'))`, "second tenant choice")
  command("click", `main a[href="${other}"]`)
  await eventually(`location.pathname===${JSON.stringify(other)} && document.querySelector('main h1')!==null`, "different tenant dashboard content")
  console.log(JSON.stringify({oneClick:true,differentTenant:true,prefilled:Boolean(input.prefilled),navigationMs,pickerEntranceTabs:0,deployment:200,entityRequests:entityRequests.length,batchUnion:true,transient503:0,destination}))
} finally {
  command("close")
}
