// Uses an authenticated, isolated agent-browser session on the real role page.
// IAM_BROWSER_SESSION=... IAM_ROLES_URL=... bun apps/web/tests/role-form-browser-audit.mjs
import { execFileSync } from "node:child_process"
import assert from "node:assert/strict"

const session = process.env.IAM_BROWSER_SESSION
const url = process.env.IAM_ROLES_URL
if (!session || !url)
  throw Error("IAM_BROWSER_SESSION and IAM_ROLES_URL required")
const run = (...args) =>
  execFileSync("bunx", ["agent-browser", "--session", session, ...args], {
    encoding: "utf8",
    windowsHide: true,
    timeout: 60000,
  })
const evaluate = (script) =>
  JSON.parse(
    execFileSync(
      "bunx",
      ["agent-browser", "--session", session, "eval", "--stdin"],
      {
        input: script,
        encoding: "utf8",
        windowsHide: true,
        timeout: 60000,
      }
    )
  )
const button = (name) =>
  run("find", "role", "button", "click", "--name", name, "--exact")
const name = (value) => {
  if (value === "") {
    // Select the text explicitly, then use a real key event. CLI fill("")
    // can clear the DOM without delivering React's input event.
    evaluate(
      "(() => { const input=document.querySelector('#role-form input[name=name]'); input.focus(); input.select(); return true })()"
    )
    run("press", "Backspace")
  } else
    run(
      "find",
      "role",
      "textbox",
      "fill",
      value,
      "--name",
      "Role name",
      "--exact"
    )
  assert.equal(
    evaluate("document.querySelector('#role-form input[name=name]').value"),
    value
  )
}
const settle = () =>
  evaluate("new Promise(resolve=>setTimeout(()=>resolve(true),300))")
const unique = `Form acceptance ${Date.now()}`
let created = false
let createdID
run("open", url)
run("wait", "[data-role-content]")
evaluate(`(() => {
  window.__roleRequests=[];
  const original=window.fetch;
  window.fetch=async (...args)=>{
    const response=await original(...args);
    const method=String(args[1]?.method??'GET').toUpperCase();
    const path=String(args[0]);
    if(/roles/.test(path)&&['POST','PATCH','DELETE','OPTIONS'].includes(method))
      window.__roleRequests.push({method,status:response.status});
    return response;
  };return true;
})()`)
const requests = () => evaluate("window.__roleRequests")
const invalid = (value, action) => {
  const before = requests().filter((r) => r.method !== "OPTIONS").length
  name(value)
  button(action)
  settle()
  const message = evaluate(`(() => {
    const input=document.querySelector('#role-form input');
    const errorID=input?.getAttribute('aria-describedby');
    return errorID ? document.getElementById(errorID)?.textContent : null;
  })()`)
  assert(message, `Missing field error for ${JSON.stringify(value)}`)
  assert.equal(
    requests().filter((r) => r.method !== "OPTIONS").length,
    before,
    "Invalid form sent a mutation"
  )
  return message
}
try {
  button("Create role")
  run("wait", "#role-form input")
  settle()
  const createErrors = ["", "   ", "汉".repeat(43), "a".repeat(129)].map(
    (value) => invalid(value, "Create")
  )
  name(unique)
  button("Create")
  run(
    "wait",
    "--fn",
    `!document.querySelector('[role=dialog]') && document.querySelector('[data-role-content] h2')?.textContent===${JSON.stringify(unique)}`
  )
  created = true
  const createdURL = run("get", "url").trim()
  createdID = new URL(createdURL).searchParams.get("role")
  assert(createdID, "Created role missing selection ID")
  button("Edit")
  run("wait", "#role-form input")
  settle()
  assert.equal(
    evaluate("document.querySelector('#role-form input').value"),
    unique
  )
  const editErrors = ["", "   ", "汉".repeat(43), "a".repeat(129)].map(
    (value) => invalid(value, "Save")
  )
  assert.deepEqual(
    editErrors,
    createErrors,
    "Create and edit reject the same values differently"
  )
  const boundary = "汉".repeat(42) + "ab"
  name(boundary)
  button("Save")
  run(
    "wait",
    "--fn",
    `!document.querySelector('[role=dialog]') && document.querySelector('[data-role-content] h2')?.textContent===${JSON.stringify(boundary)}`
  )
  run("reload")
  run(
    "wait",
    "--fn",
    `document.querySelector('[data-role-content] h2')?.textContent===${JSON.stringify(boundary)}`
  )
  button("Delete role")
  run(
    "wait",
    "--fn",
    `document.querySelector('[data-role-content] h2')?.textContent!==${JSON.stringify(boundary)} && !document.querySelector('[data-role-navigation]')?.textContent.includes(${JSON.stringify(boundary)})`
  )
  created = false
  console.log(
    JSON.stringify({
      result: "PASS",
      url,
      checks: [
        "POST/PATCH empty, whitespace, 129-byte Chinese, 129-character rejection without mutation",
        "equal field messages",
        "create/read/edit/reload/delete",
        "128-byte Chinese boundary accepted",
      ],
      createErrors,
      editErrors,
    })
  )
} finally {
  if (created) {
    assert.equal(
      new URL(run("get", "url").trim()).searchParams.get("role"),
      createdID,
      "Refusing cleanup of a different role"
    )
    // Only the role created by this run is selected. Never delete a pre-existing role.
    if (evaluate("!!document.querySelector('[role=dialog]')")) button("Cancel")
    button("Delete role")
    settle()
  }
}
