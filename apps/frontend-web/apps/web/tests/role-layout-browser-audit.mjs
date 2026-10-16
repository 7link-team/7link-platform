// Run in an isolated, authenticated agent-browser session against an actual role page.
// IAM_BROWSER_SESSION=roles-layout IAM_ROLES_URL=http://127.0.0.1:4303/admin/<id>/roles bun apps/web/tests/role-layout-browser-audit.mjs
import { execFileSync } from "node:child_process"
import assert from "node:assert/strict"

const session = process.env.IAM_BROWSER_SESSION
const url = process.env.IAM_ROLES_URL
if (!session || !url)
  throw Error("IAM_BROWSER_SESSION and IAM_ROLES_URL are required")
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
const measure = () =>
  evaluate(`(() => {
  const describe=e=>{const r=e.getBoundingClientRect();return {top:r.top,bottom:r.bottom,height:e.clientHeight,scrollHeight:e.scrollHeight,scrollTop:e.scrollTop,width:e.clientWidth,scrollWidth:e.scrollWidth,overflow:getComputedStyle(e).overflowY}};
  const main=document.querySelector('main'), nav=document.querySelector('[data-role-navigation]'), content=document.querySelector('[data-role-content]');
  if(!nav||!content)throw Error('Role workspace missing: authenticate the supplied session');
  return {viewport:[innerWidth,innerHeight],bodyWidth:document.body.scrollWidth,main:describe(main),nav:describe(nav),content:describe(content),panel:describe(document.querySelector('[data-role-panel=permissions]')),tabsTop:document.querySelector('[data-role-workspace-tabs]').getBoundingClientRect().top,headerTop:content.querySelector('header').getBoundingClientRect().top,firstRoleTop:nav.querySelector('button').getBoundingClientRect().top};
})()`)
const settled = () =>
  evaluate("new Promise(resolve=>setTimeout(()=>resolve(true),250))")
run("open", url)
run("wait", "[data-role-content] [role=checkbox]")
run("set", "viewport", "1440", "900")
settled()
evaluate(
  "document.querySelector('[data-role-panel=permissions]').scrollTop=0;document.querySelector('[data-role-navigation]').scrollTop=0;true"
)
const desktop = measure()
assert(
  desktop.main.scrollHeight <= desktop.main.height + 1,
  "Desktop outer main still scrolls"
)
assert(
  desktop.content.bottom <= 901 && desktop.nav.bottom <= 901,
  "Panels exceed viewport"
)
assert(
  desktop.panel.scrollHeight > desktop.panel.height,
  "Fixture needs an overflowing permission panel"
)
run("scroll", "down", "700", "--selector", "[data-role-panel=permissions]")
settled()
const rightScrolled = measure()
assert(rightScrolled.panel.scrollTop > 0, "Right panel did not scroll")
assert.equal(rightScrolled.nav.scrollTop, desktop.nav.scrollTop)
assert.equal(rightScrolled.firstRoleTop, desktop.firstRoleTop)
assert.equal(rightScrolled.main.scrollTop, 0)
assert.equal(rightScrolled.tabsTop, desktop.tabsTop, "Tab titles scrolled away")
assert.equal(
  rightScrolled.headerTop,
  desktop.headerTop,
  "Role header scrolled away"
)
run("set", "viewport", "1440", "450")
settled()
const short = measure()
assert(
  short.main.scrollHeight <= short.main.height + 1,
  "Short viewport outer main overflows"
)
const leftOverflow = short.nav.scrollHeight > short.nav.height + 1
if (process.env.REQUIRE_ROLE_NAV_OVERFLOW === "1")
  assert(leftOverflow, "Fixture needs an overflowing role list")
if (leftOverflow) {
  run("scroll", "down", "180", "--selector", "[data-role-navigation]")
  settled()
  const leftScrolled = measure()
  assert(
    leftScrolled.nav.scrollTop > short.nav.scrollTop,
    "Left panel did not scroll"
  )
  assert.equal(leftScrolled.panel.scrollTop, short.panel.scrollTop)
  assert.equal(leftScrolled.main.scrollTop, 0)
}
evaluate(
  "document.querySelector('[data-role-panel=permissions]').scrollTop=0;document.querySelector('[data-role-panel=permissions]').focus();true"
)
const beforeKeyboard = measure()
run("press", "PageDown")
settled()
const keyboard = measure()
assert(keyboard.panel.scrollTop > 0, "Keyboard cannot scroll the focused panel")
assert.equal(keyboard.nav.scrollTop, beforeKeyboard.nav.scrollTop)
// Every available tab must keep its navigation outside the scrolling panel.
const views = evaluate(
  "Array.from(document.querySelectorAll('[data-role-view]'), e=>e.dataset.roleView)"
)
for (const view of views) {
  run("click", `[data-role-view="${view}"]`)
  settled()
  const panel = evaluate(`(() => {
    const e=document.querySelector('[data-role-panel="${view}"]');
    const tabs=document.querySelector('[data-role-workspace-tabs]');
    const before=tabs.getBoundingClientRect().top;
    e.scrollTop=400;
    return {height:e.clientHeight,bottom:e.getBoundingClientRect().bottom,tabsBefore:before,tabsAfter:tabs.getBoundingClientRect().top,outerScroll:document.querySelector('[data-role-content]').scrollTop};
  })()`)
  assert(
    panel.height > 0 && panel.bottom <= 451,
    `${view} panel has no usable bounded viewport`
  )
  assert.equal(
    panel.tabsAfter,
    panel.tabsBefore,
    `${view} tab navigation moved`
  )
  assert.equal(panel.outerScroll, 0, `${view} scrolled the whole section`)
}
run("click", '[data-role-view="permissions"]')
settled()
run("set", "viewport", "375", "812")
settled()
const mobile = measure()
assert(
  mobile.bodyWidth <= 375 && mobile.main.scrollWidth <= mobile.main.width + 1,
  "Mobile horizontal page overflow"
)
assert(
  mobile.content.top >= mobile.nav.bottom - 1,
  "Mobile panels are not stacked"
)
evaluate(
  "document.querySelector('[data-role-content]').scrollIntoView({block:'start'});true"
)
settled()
const mobileContent = measure()
assert(
  mobileContent.content.top < 812 &&
    mobileContent.content.top >= mobileContent.main.top - 1,
  "Mobile content unreachable"
)
const lastVisible = evaluate(
  `(() => {const items=document.querySelectorAll('[data-role-content] [role=checkbox]');const last=items[items.length-1];last.scrollIntoView({block:'center'});const r=last.getBoundingClientRect();return r.top>=0&&r.bottom<=innerHeight})()`
)
assert(lastVisible, "Final permission is unreachable on mobile")
console.log(
  JSON.stringify(
    {
      pass: true,
      route: new URL(url).pathname,
      desktop,
      short,
      leftOverflow,
      mobile,
      checks: [
        "bounded desktop panels",
        "right scroll independent",
        "role header and tab titles stay visible",
        "all available tabs have bounded content",
        ...(leftOverflow ? ["left scroll independent"] : []),
        "keyboard panel scroll",
        "mobile stacking and width",
        "mobile content and final permission reachable",
      ],
    },
    null,
    2
  )
)
