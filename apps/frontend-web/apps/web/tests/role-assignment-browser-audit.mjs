// Real tenant role assignment UI, using an isolated authenticated English session.
// IAM_BROWSER_SESSION=... IAM_ROLES_URL=... bun apps/web/tests/role-assignment-browser-audit.mjs
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
      { input: script, encoding: "utf8", windowsHide: true, timeout: 60000 }
    )
  )
const wait = (predicate) => run("wait", "--fn", predicate)
const api = (expression) =>
  evaluate(
    `(async()=>{const {iamApi:api}=await import('/src/lib/iam-api.ts');return ${expression}})()`
  )
let role, group
try {
  run("open", url)
  run("wait", "[data-role-content]")
  const suffix = Date.now()
  role = api(
    `api.createRole({name:'Assignment acceptance ${suffix}',description:'Temporary test role without permissions'})`
  )
  group = api(
    `api.createGroup({name:'Assignment acceptance ${suffix}',type:'static',description:'Temporary test group',status:'active',sort_order:0})`
  )
  run("open", `${url}?role=${role.id}&view=assignments`)
  run("wait", "[data-role-member-id]")
  wait(
    "document.querySelectorAll('[data-role-panel=assignments] table').length===2"
  )
  const memberID = evaluate(
    "[...document.querySelectorAll('[data-role-member-id]')].find(e=>!e.disabled)?.dataset.roleMemberId"
  )
  assert(memberID, "No assignable member fixture")
  const memberSelector = `[data-role-member-id="${memberID}"]`
  run("click", memberSelector)
  wait(
    `document.querySelector('${memberSelector}')?.getAttribute('aria-checked')==='true'`
  )
  assert(
    api(`api.roleMembers('${role.id}')`).includes(memberID),
    "Member grant not persisted"
  )
  evaluate(
    `document.querySelector('${memberSelector}').closest('tr').querySelector('[data-role-member-end]').click();true`
  )
  evaluate(
    "(() => { const input=document.querySelector('input[type=datetime-local]'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'2030-12-31T12:00'); input.dispatchEvent(new Event('input',{bubbles:true})); input.dispatchEvent(new Event('change',{bubbles:true})); return true })()"
  )
  wait(
    "!document.querySelector('[data-role-member-end-editor] button[type=submit]')?.disabled"
  )
  evaluate(
    "document.querySelector('[data-role-member-end-editor] form').requestSubmit();true"
  )
  wait(
    "!!document.querySelector('[data-role-member-end]')?.getAttribute('data-role-member-end')"
  )
  const grant = api(`api.roleMemberGrants('${role.id}')`).find(
    (item) => item.principal_id === memberID
  )
  assert.equal(
    new Date(grant?.expires_at).getTime(),
    evaluate("new Date('2030-12-31T12:00').getTime()"),
    "Member end not persisted"
  )
  evaluate(
    `document.querySelector('${memberSelector}').closest('tr').querySelector('[data-role-member-end]').click();true`
  )
  wait(
    "!document.querySelector('[data-role-member-end-editor] button[type=submit]')?.disabled"
  )
  evaluate(`(() => {
    const original = window.fetch;
    window.fetch = async (...args) => {
      if (String(args[0]).includes('/roles/${role.id}/members/${memberID}') && args[1]?.method?.toUpperCase() === 'PUT') {
        window.fetch = original;
        throw new TypeError('Expiry acceptance simulated network failure');
      }
      return original(...args);
    };
    document.querySelector('[data-role-member-end-editor] form').requestSubmit();
    return true;
  })()`)
  wait("!!document.querySelector('[data-role-member-end-editor] [role=alert]')")
  assert.equal(
    evaluate("document.querySelector('input[type=datetime-local]').value"),
    "2030-12-31T12:00",
    "Failed save lost date draft"
  )
  run("screenshot", "/tmp/role-expiry-failed-save.png")
  evaluate(
    "document.querySelector('[data-role-member-end-editor] form').requestSubmit();true"
  )
  wait("!document.querySelector('[data-role-member-end-editor]')")
  evaluate(
    `document.querySelector('${memberSelector}').closest('tr').querySelector('[data-role-member-end]').click();true`
  )
  wait(
    "!document.querySelector('[data-role-member-end-editor] button[type=submit]')?.disabled"
  )
  evaluate(
    "(() => {const input=document.querySelector('input[type=datetime-local]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'');input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));return true})()"
  )
  evaluate(
    "document.querySelector('[data-role-member-end-editor] form').requestSubmit();true"
  )
  wait("!document.querySelector('[data-role-member-end-editor]')")
  const unlimitedGrant = api(`api.roleMemberGrants('${role.id}')`).find(
    (item) => item.principal_id === memberID
  )
  assert(unlimitedGrant, "Clearing expiry removed the member grant")
  // RoleMemberGrant omits expires_at when the persisted end is NULL.
  assert.equal(
    unlimitedGrant.expires_at,
    undefined,
    "Clearing expiry did not persist unlimited access"
  )
  run("click", memberSelector)
  wait(
    `document.querySelector('${memberSelector}')?.getAttribute('aria-checked')==='false'`
  )
  assert(
    !api(`api.roleMembers('${role.id}')`).includes(memberID),
    "Member revoke not persisted"
  )
  const groupSelector = `[data-assignee-id="${group.id}"]`
  // Page the local group table if pre-existing directory data fills its first page.
  evaluate(
    "(()=>{const select=document.querySelector('[data-directory-section=group] select');select.value='100';select.dispatchEvent(new Event('change',{bubbles:true}));return true})()"
  )
  run("wait", groupSelector)
  run("click", groupSelector)
  wait(
    `document.querySelector('${groupSelector}')?.getAttribute('aria-checked')==='true'`
  )
  assert(
    api(`api.roleDirectoryBindings('${role.id}')`).some(
      (item) => item.assignee_id === group.id
    ),
    "Group binding not persisted"
  )
  run("reload")
  run("wait", groupSelector)
  wait(
    `document.querySelector('${groupSelector}')?.getAttribute('aria-checked')==='true'`
  )
  run("click", groupSelector)
  wait(
    `document.querySelector('${groupSelector}')?.getAttribute('aria-checked')==='false'`
  )
  assert(
    !api(`api.roleDirectoryBindings('${role.id}')`).some(
      (item) => item.assignee_id === group.id
    ),
    "Group revoke not persisted"
  )
  const chooseScope = (label) => {
    run("click", "#role-data-scope-form [role=combobox]")
    run("find", "role", "option", "click", "--name", label, "--exact")
  }
  const saveScope = (scope) => {
    run("click", "[data-save-data-scope]")
    wait("!document.querySelector('[data-save-data-scope]').disabled")
    assert.equal(api(`api.roleDataScope('${role.id}')`).scope, scope)
  }
  run("click", "[data-role-view=scope]")
  wait(
    "!!document.querySelector('[data-save-data-scope]') && !document.querySelector('[data-save-data-scope]').disabled"
  )
  chooseScope("Own data")
  run("click", "[data-role-view=assignments]")
  run("click", "[data-role-view=scope]")
  wait(
    "document.querySelector('#role-data-scope-form [role=combobox]')?.textContent==='Own data'"
  )
  saveScope("self")
  chooseScope("Selected departments")
  const departmentID = evaluate(
    "[...document.querySelectorAll('[data-scope-department-id]')].find(e=>!e.disabled)?.dataset.scopeDepartmentId"
  )
  assert(departmentID, "No eligible department fixture")
  run("click", `[data-scope-department-id="${departmentID}"]`)
  saveScope("selected_departments")
  assert.deepEqual(api(`api.roleDataScope('${role.id}')`).department_ids, [
    departmentID,
  ])
  run("reload")
  wait(
    `document.querySelector('[data-scope-department-id="${departmentID}"]')?.getAttribute('aria-checked')==='true'`
  )
  chooseScope("All data")
  saveScope("all")
  assert.deepEqual(api(`api.roleDataScope('${role.id}')`).department_ids, [])
  run("click", "[data-role-view=assignments]")
  run("set", "viewport", "375", "812")
  const mobile = evaluate(
    "({width:innerWidth,bodyWidth:document.body.scrollWidth,tables:[...document.querySelectorAll('[data-role-panel=assignments] table')].map(t=>({viewport:t.parentElement.clientWidth,content:t.parentElement.scrollWidth}))})"
  )
  assert(
    mobile.bodyWidth <= mobile.width,
    "Assignment tables overflow the page"
  )
  evaluate(
    "document.querySelector('[data-role-panel=assignments]').scrollIntoView({block:'start'});true"
  )
  run("screenshot", "/tmp/role-assignment-mobile.png")
  run("set", "viewport", "1440", "900")
  evaluate(
    "document.querySelector('[data-role-panel=assignments]').scrollTop=0;document.querySelector('main').scrollTop=0;true"
  )
  run("screenshot", "/tmp/role-assignment-desktop.png")
  console.log(
    JSON.stringify({
      pass: true,
      url,
      mobile,
      checks: [
        "member grant/revoke persisted",
        "expiry persisted, failed save retained draft, retry succeeded, clear persisted null",
        "group grant/reload/revoke persisted",
        "two AutoTables",
        "data scope save/reload/department clearing and draft retained across tabs",
        "mobile page bounds",
      ],
    })
  )
} finally {
  run("open", url)
  run("wait", "[data-role-content]")
  if (role) api(`api.deleteRole('${role.id}')`)
  if (group) api(`api.deleteGroup('${group.id}',${group.version})`)
}
