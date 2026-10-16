// Authenticated isolated session; real menu records, read-only navigation.
import { execFileSync } from "node:child_process"
import assert from "node:assert/strict"
const session = process.env.IAM_BROWSER_SESSION
const url = process.env.IAM_MENUS_URL
if (!session || !url)
  throw Error("IAM_BROWSER_SESSION and IAM_MENUS_URL required")
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
const settled = () => evaluate("new Promise(r=>setTimeout(()=>r(true),250))")
const measure = () =>
  evaluate(`(() => {
 const d=e=>{const r=e.getBoundingClientRect();return {top:r.top,bottom:r.bottom,h:e.clientHeight,sh:e.scrollHeight,y:e.scrollTop,w:e.clientWidth,sw:e.scrollWidth}};
 const section=document.querySelector('[data-menu-content]');
 return {main:d(document.querySelector('main')),tree:d(document.querySelector('[data-menu-navigation] [role=tree]')),section:d(section),table:d(section.querySelector('table').parentElement),body:document.body.scrollWidth};
})()`)
run("open", url)
run("wait", "[data-menu-row-id]")
run("set", "viewport", "1440", "450")
settled()
const before = measure()
assert(before.main.sh <= before.main.h + 1, "Outer main overflows")
assert(
  before.tree.bottom <= 450 && before.section.bottom <= 450,
  "Panels exceed viewport"
)
assert(before.table.sh > before.table.h, "Needs real overflowing table")
run(
  "scroll",
  "down",
  "400",
  "--selector",
  "[data-menu-content] div:has(> table)"
)
settled()
const right = measure()
assert(right.table.y > 0, "Table did not scroll")
assert.equal(right.tree.y, before.tree.y)
assert.equal(right.tree.top, before.tree.top)
assert.equal(right.main.y, 0)
const leftOverflow = before.tree.sh > before.tree.h
if (leftOverflow) {
  run(
    "scroll",
    "down",
    "100",
    "--selector",
    "[data-menu-navigation] [role=tree]"
  )
  settled()
  const left = measure()
  assert(left.tree.y > 0)
  assert.equal(left.table.y, right.table.y)
  assert.equal(left.main.y, 0)
}
const horizontal = evaluate(`(() => {
 const s=document.querySelector('[data-menu-content] table').parentElement;
 s.scrollTop=0;s.scrollLeft=0;
 const cell=s.querySelector('tbody tr td:last-child'),sticky=getComputedStyle(cell).position==='sticky',x=cell.getBoundingClientRect().right;
 s.scrollLeft=s.scrollWidth;
 return {overflow:s.scrollWidth>s.clientWidth,x:s.scrollLeft,sticky,delta:cell.getBoundingClientRect().right-x};
})()`)
assert(horizontal.overflow && horizontal.x > 0)
if (horizontal.sticky)
  assert(Math.abs(horizontal.delta) < 2, "Sticky action column moved")
evaluate(
  "document.querySelector('[data-menu-navigation] [role=tree]').scrollTop=0;document.querySelector('[data-menu-navigation] [role=tree]').focus();true"
)
run("press", "PageDown")
settled()
if (leftOverflow) assert(measure().tree.y > 0, "Keyboard tree scrolling failed")
const hasBranches = evaluate(
  "Boolean(document.querySelector('[data-menu-tree-id]'))"
)
if (process.env.REQUIRE_MENU_TREE === "1")
  assert(hasBranches, "Requires a real menu branch")
if (hasBranches) {
  evaluate(
    "document.querySelector('[data-menu-tree-id] > button:last-child').click();true"
  )
  settled()
  assert(
    evaluate(
      "Boolean(document.querySelector('[role=treeitem][aria-selected=true]'))"
    ),
    "Branch selection failed"
  )
}
run("click", "[data-menu-root]")
settled()
run("set", "viewport", "375", "812")
settled()
const mobile = measure()
assert(
  mobile.body <= 375 && mobile.main.sw <= mobile.main.w + 1,
  "Mobile page overflow"
)
assert(mobile.section.top >= mobile.tree.bottom, "Mobile panes not stacked")
assert(
  evaluate(
    `(() => {const rows=document.querySelectorAll('[data-menu-row-id]');const row=rows[rows.length-1];row.scrollIntoView({block:'center'});const r=row.getBoundingClientRect();return r.top>=0&&r.bottom<=innerHeight})()`
  ),
  "Final row unreachable"
)
console.log(
  JSON.stringify(
    {
      pass: true,
      route: new URL(url).pathname,
      before,
      right,
      leftOverflow,
      hasBranches,
      horizontal,
      mobile,
    },
    null,
    2
  )
)
