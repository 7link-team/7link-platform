// Fresh named browser session; all mutations below use actual UI controls.
(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 160));
  const check = (value, message) => { if (!value) throw Error(message); };
  const state = () => JSON.parse(localStorage.getItem("collaboration-prototype-v1"));
  const go = async path => { location.hash = path; await wait(); };
  const click = async (text, root = document) => { const button = [...root.querySelectorAll("button")].find(item => item.textContent.trim() === text); check(button && !button.disabled, `Missing or disabled ${text}`); button.click(); await wait(); };
  const fill = async (label, value) => { const input = document.querySelector(`[name="${label}"]`); check(input, `Missing ${label}`); Object.getOwnPropertyDescriptor(input.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value").set.call(input, value); input.dispatchEvent(new Event("input", { bubbles: true })); await wait(); };
  const choose = async (label, text) => { const combo = document.querySelector(`[role=combobox][aria-label="${label}"]`); check(combo, `Missing choice ${label}`); combo.click(); await wait(); const option = [...document.querySelectorAll('[role=option]')].find(item => item.textContent.trim() === text); check(option, `Missing option ${text}`); option.click(); await wait(); };
  const today = new Date().toISOString().slice(0, 10);
  await go("/goals"); await click("新建目标"); await fill("目标名称", "周期计划浏览器目标"); await choose("目标归属", "Agent · Atlas"); await choose("目标负责人", "Agent · Atlas");
  await choose("目标周期", "季度"); await fill("周期内日期", today); await fill("目标时区", "UTC");
  await fill("KR 1 标题", "形成研究记录"); await fill("KR 1 单位", "份"); await fill("KR 1 基线", "0"); await fill("KR 1 目标值", "1"); await fill("KR 1 权重", "1");
  await click("保存目标草稿", document.querySelector("dialog")); check(!document.querySelector("dialog"), "Goal setup did not save");
  await go("/plans/me"); await choose("计划主体", "Atlas · Agent"); await fill("周期日期", today); await click("生成 / 打开 Agent 模拟计划");
  const planId = location.hash.split("/").at(-1), plan = () => state().resources.plans.find(item => item.id === planId);
  check(plan()?.status === "草稿", "Missing draft"); await click("编辑计划项"); await click("添加计划项");
  await fill("标题 1", "研究计划演示"); await choose("目标 1", "周期计划浏览器目标"); await choose("KR 1", "形成研究记录"); await fill("完成标准 1", "提供可核对的研究记录"); await fill("承诺日期 1", today);
  await click("保存", document.querySelector("dialog")); check(!document.querySelector("dialog"), "Plan editor failed to save");
  await click("模拟 Agent 提交计划"); check(plan().status === "已提交", "Plan did not submit");
  await click("确认固定计划"); await fill("确认或退回说明", "Human 核对本期范围"); await click("确认操作", document.querySelector("dialog"));
  check(plan().status === "已确认" && plan().confirmedSnapshot.actor.kind === "Human", "Plan did not freeze Human approval");
  const baseline = structuredClone(plan().confirmedSnapshot);
  await click("记录完成证据 · 研究计划演示"); await fill("完成证据或说明", "本地研究记录：逐项核对完成"); await click("确认操作", document.querySelector("dialog"));
  check(document.querySelector("main").textContent.includes("完成比例 100%"), "Evidence did not drive actual completion");
  await click("编辑计划项"); await fill("标题 1", "研究计划新范围"); await fill("变更原因", "新增明确范围，需重新确认"); await click("保存", document.querySelector("dialog"));
  check(plan().status === "已提交", "Replan did not require confirmation"); check(JSON.stringify(plan().confirmedSnapshot) === JSON.stringify(baseline), "Replan rewrote confirmed baseline");
  check(document.querySelector("main").textContent.includes("研究计划演示") && document.querySelector("main").textContent.includes("研究计划新范围"), "Before/after diff absent");
  await click("退回计划"); await fill("确认或退回说明", "请核对新增范围"); await click("确认操作", document.querySelector("dialog")); check(plan().status === "已退回", "Human return failed");
  await go("/plans/me"); await choose("计划主体", "Atlas · Agent"); await fill("周期日期", today); await fill("计划时区", "UTC"); await click("生成 / 打开 Agent 模拟计划"); check(location.hash.endsWith(planId), "Timezone created duplicate period plan");
  await go("/plans/me"); await click("生成 / 打开本人计划"); check(document.querySelector("main").textContent.includes("不能本人自审"), "Missing-superior guard invisible");
  check(![...document.querySelectorAll("button")].some(button => button.textContent === "确认固定计划"), "Own draft exposed approval");
  check(document.documentElement.scrollWidth <= innerWidth + 2, "Plan UI overflows viewport");
  return { pass: true, width: innerWidth, checks: ["typed goal association", "Agent simulation provenance", "fixed Human approval", "FREE_TASK evidence completion", "semantic replan diff and old baseline", "Human return", "period uniqueness across timezone", "missing superior and self-review guard", "viewport fit"] };
})()
