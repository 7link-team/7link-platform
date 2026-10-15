(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 180));
  const check = (value, message) => { if (!value) throw Error(message); };
  const state = () => JSON.parse(localStorage.getItem("collaboration-prototype-v1"));
  const fixture = state().testPrivateKnowledge;
  check(fixture, "Missing genuine private knowledge fixture");
  const originalOther = JSON.stringify(state().resources.knowledge.find(record => record.id === fixture.otherId));
  const go = async path => { location.hash = path; await wait(); };
  const click = async (label, scope = document) => {
    const control = [...scope.querySelectorAll("button")].find(control => control.textContent.trim() === label);
    check(control, "Missing " + label); control.click(); await wait();
  };
  const fill = async (label, value) => {
    const input = document.querySelector(`[aria-label="${label}"]`) || document.querySelector(`[name="${label}"]`);
    check(input, "Missing " + label);
    Object.getOwnPropertyDescriptor(input.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value").set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true })); await wait();
  };
  const chooseRole = async role => {
    await go("/settings"); const combo = document.querySelector('[role="combobox"][aria-label="角色视角"]');
    check(combo, "Missing real role selector"); combo.click(); await wait();
    const option = [...document.querySelectorAll('[role="option"]')].find(option => option.textContent.trim() === role);
    check(option, "Missing role option " + role); option.click(); await wait(); check(state().role === role, "Role switch not persisted");
  };
  const privateScope = async () => {const combo=document.querySelector('[role="combobox"][aria-label="资料范围"]');check(combo,"Missing library scope");combo.click();await wait();const option=[...document.querySelectorAll('[role="option"]')].find(option=>option.textContent.trim()==="我的私人资料");check(option,"Missing private scope");option.click();await wait();};
  const close = async () => { const button = document.querySelector('dialog[open] [aria-label="关闭弹窗"]'); check(button, "Missing modal close"); button.click(); await wait(); };
  const roles = [];
  for (const role of ["owner", "executor", "external"]) {
    await chooseRole(role); await go("/knowledge"); if(role!=="external") await privateScope();
    check(!document.querySelector("main").textContent.includes(fixture.otherName), "Private title leaked in list for " + role);
    check(!document.querySelector(`main a[href="#/knowledge/${fixture.otherId}"]`), "Private detail link leaked");
    check(document.querySelector("main").textContent.includes(fixture.ownName) === (role !== "external"), "Own private visibility incorrect");
    await go(`/knowledge/${fixture.otherId}`);
    check(!document.querySelector("main").textContent.includes(fixture.otherContent) && !document.querySelector("main").textContent.includes(fixture.otherName), "Private detail leaked for " + role);
    check(![...document.querySelectorAll("main button")].some(button => ["编辑", "导出", "确认 / 发布", "禁用资料"].includes(button.textContent.trim())), "Denied private view retained mutation/export controls");
    document.querySelector('[aria-label="全局搜索"]').click(); await wait();
    await fill("全局搜索关键词", fixture.otherName);
    check(!document.querySelector("dialog[open] .search-results").textContent.includes(fixture.otherName), "Private title leaked in global search");
    await fill("全局搜索关键词", fixture.ownName);
    check(document.querySelector("dialog[open] .search-results").textContent.includes(fixture.ownName) === (role !== "external"), "Own global search inconsistent");
    await close();
    check(document.documentElement.scrollWidth <= innerWidth + 2, "Private permission view overflows");
    roles.push({ role, privateListHidden: true, privateDetailHidden: true, privateSearchHidden: true });
  }
  await chooseRole("owner"); await go(`/knowledge/${fixture.ownId}`);
  check(document.querySelector("main").textContent.includes("本人修订正文"), "Own detail missing genuine current content");
  const blobs = [], downloads = [], nativeCreate = URL.createObjectURL, nativeClick = HTMLAnchorElement.prototype.click;
  URL.createObjectURL = function(blob) { blobs.push(blob); return nativeCreate.call(URL, blob); };
  HTMLAnchorElement.prototype.click = function() { downloads.push(this.download); };
  let listExportCount;
  try {
    await click("导出"); check(blobs.length === 1 && downloads[0] === "knowledge.json", "Actual own export click missing");
    check(JSON.stringify(JSON.parse(await blobs[0].text())) === JSON.stringify(state().resources.knowledge.find(record => record.id === fixture.ownId)), "Own export lost content or history");
    await go("/knowledge"); await privateScope(); await click("导出当前查询结果");
    const records = JSON.parse(await blobs[1].text()).records;
    check(records.every(record => record.id !== fixture.otherId) && !JSON.stringify(records).includes(fixture.otherContent), "List export leaked other private original/history");
    listExportCount = records.length;
  } finally { URL.createObjectURL = nativeCreate; HTMLAnchorElement.prototype.click = nativeClick; }
  await go("/overview");
  check(!document.querySelector("main").textContent.includes(fixture.otherName), "Dashboard activity leaked private title");
  await go("/governance/audit");
  check(!document.querySelector("main").textContent.includes(fixture.otherName), "Audit activity leaked private title");
  check(document.querySelector("main").textContent.includes(fixture.ownName), "Authorized own activity disappeared");
  try {
    URL.createObjectURL = function(blob) { blobs.push(blob); return nativeCreate.call(URL, blob); };
    HTMLAnchorElement.prototype.click = function() { downloads.push(this.download); };
    await click("导出当前查询结果");
    const events = JSON.parse(await blobs.at(-1).text()).records;
    check(events.some(event => event.target === `/knowledge/${fixture.ownId}`), "Audit export lost authorized source");
    check(!JSON.stringify(events).includes(fixture.otherName) && !events.some(event => fixture.legacyEvents.some(old => old.id === event.id)), "Audit Blob leaked private history");
  } finally { URL.createObjectURL = nativeCreate; HTMLAnchorElement.prototype.click = nativeClick; }
  for (const event of fixture.legacyEvents)
    check(JSON.stringify(state().events.find(saved => saved.id === event.id)) === JSON.stringify(event), "Activity filtering rewrote original history");
  await go("/knowledge");
  await privateScope(); await click("新建知识");
  check(document.querySelector('dialog[open] [name="正文 / 本地预览内容"]').value === "", "New document silently copied an existing document body");
  check(document.querySelector('dialog[open] [aria-label="明确归属范围"]')?.textContent.includes("私人"), "Private create lost scope");
  await close();
  await go(`/knowledge/${fixture.ownId}`); await click("编辑");
  await fill("适用 / 共享范围", "租户"); await click("保存", document.querySelector("dialog[open]"));
  check(document.querySelector('dialog[open] [role="alert"]')?.textContent.includes("共享"), "Ordinary editor widened private sharing");
  for (let attempt = 0; attempt < 2; attempt++) {
    if (attempt) {
      const submit = document.querySelector('dialog[open] button[type="submit"]');
      submit.focus(); await click("保存", document.querySelector("dialog[open]"));
    }
    const error = document.querySelector('dialog[open] [role="alert"]'), rect = error.getBoundingClientRect();
    const dialog = document.querySelector('dialog[open]'), dialogRect = dialog.getBoundingClientRect();
    const headingBottom = dialog.querySelector(".modal-heading").getBoundingClientRect().bottom;
    check(document.activeElement === error && rect.top >= headingBottom && rect.bottom <= Math.min(dialogRect.bottom, innerHeight), "Rejected action left focus hidden or obscured in modal");
  }
  check(state().resources.knowledge.find(record => record.id === fixture.ownId).scope === "私人", "Denied sharing changed private scope");
  await fill("适用 / 共享范围", "私人"); await fill("内容", "本人浏览器修订正文"); await click("保存", document.querySelector("dialog[open]"));
  check(!document.querySelector("dialog[open]"), "Own legitimate edit did not save");
  check(state().resources.knowledge.find(record => record.id === fixture.ownId).history.some(snapshot => snapshot.content === "本人修订正文"), "Own edit lost original history");
  await go("/chat?channel_id=private");
  const marker = `private-memory-${Date.now()}`; const composer = document.querySelector(".composer textarea");
  check(composer, "Missing private composer");
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(composer, marker);
  composer.dispatchEvent(new Event("input", { bubbles: true })); await wait(); await click("发送", document.querySelector(".composer"));
  const message = [...document.querySelectorAll(".message")].find(message => message.querySelector("p")?.textContent === marker);
  check(message, "Actual private message missing"); await click("更多", message); await click("保存为记忆候选", document.querySelector("dialog[open]"));
  const candidate = state().resources.knowledge.find(record => record.content === marker);
  check(candidate?.scope === "私人" && candidate.ownerId === "H-lin" && candidate.ownerKind === "Human" && candidate.tenantId === state().activeTenant && candidate.status === "待确认", "Private message candidate bypassed owner/scope establishment or became published");
  check(JSON.stringify(state().resources.knowledge.find(record => record.id === fixture.otherId)) === originalOther, "Private original mutated through UI tests");
  return { pass: true, width: innerWidth, roles, listExportCount, checks: ["real three-role list/detail/global search privacy", "actual Blob/download preservation", "no private history leakage", "blank create defaults", "sharing edit atomic refusal", "legitimate own edit/history", "actual private message candidate with stable owner/tenant", "other original unchanged"] };
})()
