import assert from "node:assert/strict"
import {chromium} from "playwright"

const baseURL=process.env.PACKAGE_COMMENT_FIXTURE_URL
assert(baseURL&&["localhost","127.0.0.1"].includes(new URL(baseURL).hostname),"An isolated local Vite URL is required")
const browser=await chromium.launch({channel:"chrome",headless:true})
try{
 const page=await browser.newPage({viewport:{width:1280,height:900}}),errors=[]
 page.on("pageerror",error=>errors.push(error.message))
 await page.goto(`${baseURL}/tests/fixtures/package-comments-browser.html`)
 const submit=async(index,body)=>{const article=page.locator(`[data-fixture-item="${index}"]`);await article.locator("textarea").fill(body);await article.locator('button[type="submit"]').click();await page.waitForFunction(count=>window.fixtureWrites.length===count,index)}
 const open=async(index,kind)=>{const article=page.locator(`[data-fixture-item="${index}"]`);await article.locator("summary").click();await article.locator("select").first().selectOption(kind);return article}
 const code=await open(1,"CODE_LINES"),first=code.locator('[data-source-line="1"]'),second=code.locator('[data-source-line="2"]')
 await first.waitFor();await first.scrollIntoViewIfNeeded();const a=await first.boundingBox(),b=await second.boundingBox();assert(a&&b)
 await page.mouse.move(a.x+80,a.y+a.height/2);await page.mouse.down();await page.mouse.move(b.x+80,b.y+b.height/2,{steps:8});await page.mouse.up();await submit(1,"Frozen code refusal anchor")
 const table=await open(2,"TABLE_ROW");await table.locator('[data-source-row="0"]').click();await submit(2,"Frozen table refusal anchor")
 const paragraph=await open(3,"TEXT_PARAGRAPH");await paragraph.locator('[data-source-paragraph="0"]').focus();await page.keyboard.press("Enter");await submit(3,"Frozen paragraph refusal anchor")
 const html=await open(4,"HTML_REGION"),overlay=html.locator("[data-source-region]");await overlay.waitFor();await html.locator("iframe").waitFor();await page.waitForFunction(()=>document.querySelector('iframe')?.contentDocument?.body?.innerText.includes("Rejected HTML region"))
 await overlay.scrollIntoViewIfNeeded();const region=await overlay.boundingBox();assert(region)
 await page.mouse.move(region.x+1,region.y+1);await page.mouse.down();await page.mouse.move(region.x+450,region.y+140,{steps:10});await page.mouse.up();await submit(4,"Frozen HTML refusal anchor")
 const writes=await page.evaluate(()=>window.fixtureWrites)
 assert.deepEqual(writes.map(row=>row.anchor_kind),["CODE_LINES","TABLE_ROW","TEXT_PARAGRAPH","HTML_REGION"])
 assert.equal(writes[0].start_line,1);assert.equal(writes[0].end_line,2);assert.equal(writes[1].row_index,0);assert.equal(writes[2].paragraph_index,0);assert.equal(writes[2].text_quote,"Rejected source paragraph")
 assert(writes[3].text_quote.includes("Rejected HTML region"));assert(writes[3].rect.w>0&&writes[3].rect.h>0);assert.equal(writes[3].viewport.device,"DESKTOP");assert(writes[3].viewport.w>=writes[3].rect.w);assert.equal(writes[3].scroll.x,0)
 const frozen=await page.locator("[data-fixture-frozen]").innerText()
 for(const value of ["CODE_LINES","TABLE_ROW","TEXT_PARAGRAPH","HTML_REGION","Rejected code line",'Build',"Rejected source paragraph","Rejected HTML region","file-version:1","file-version:4"])assert(frozen.includes(value),`missing frozen source ${value}`)
 assert.equal(await page.evaluate(()=>window.fixtureUnsafeScriptRan),false);assert.equal(await page.locator('[data-source-line]').count(),4,"rerenders preserve one source viewer")
 await overlay.focus();await page.keyboard.press("Enter");assert(await html.getByRole("status").filter({hasText:"Source location selected"}).count())
 await page.setViewportSize({width:390,height:844});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),"mobile source overflow stays contained")
 assert.deepEqual(errors,[]);console.log("PASS: four actual source picks, immutable refusal anchors, keyboard HTML region, script isolation and mobile containment")
}finally{await browser.close()}
