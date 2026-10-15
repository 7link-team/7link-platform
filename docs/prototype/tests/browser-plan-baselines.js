// Deliberately corrupted local persistence fixtures are injected by run-browser.py.
// They test honest diagnostic rendering, not successful approval or execution.
(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 180));
  const check = (value, message) => { if (!value) throw Error(message); };
  const storage = () => localStorage.getItem("collaboration-prototype-v1");
  const before = storage(), fixture = JSON.parse(before);
  const go = async path => { location.hash = path; await wait(); };
  const corrupt = fixture.resources.plans.filter(p => p.id.startsWith("BASELINE-CORRUPT-"));
  check(corrupt.length === 24, "Corruption fixtures missing");
  for (const plan of corrupt) {
    await go(`/plans/${plan.id}`);
    const main = document.querySelector("main");
    check(main?.textContent.includes("周期计划基线无法核对"), `Missing diagnosis ${plan.id}`);
    check(main.textContent.includes("原始计划与历史仍保留"), `Missing preservation explanation ${plan.id}`);
    check(![...main.querySelectorAll("button")].some(b => /编辑计划项|提交计划|确认固定计划|记录完成证据/.test(b.textContent)), `Invalid baseline exposes write ${plan.id}`);
    check(!main.textContent.includes("完成比例 0%"), `Unknown baseline presented as zero ${plan.id}`);
    const original = main.querySelector("details");
    check(original?.querySelector("summary")?.textContent.includes("查看原始基线与历史"), `Original record inaccessible ${plan.id}`);
    original.querySelector("summary").click(); await wait();
    const evidence = JSON.parse(original.querySelector("pre").textContent);
    check(JSON.stringify(evidence) === JSON.stringify({ confirmedSnapshot: plan.confirmedSnapshot ?? null, history: plan.history ?? null }), `Raw historical evidence changed ${plan.id}`);
    check(document.documentElement.scrollWidth <= innerWidth + 2, `Overflow ${plan.id}`);
    check(storage() === before, `Reading mutated raw fixture ${plan.id}`);
  }
  const liveCorrupt = fixture.resources.plans.filter(p => p.id.startsWith("LIVE-CORRUPT-"));
  check(liveCorrupt.length === 18, "Current-field fixtures missing");
  for (const plan of liveCorrupt) {
    await go(`/plans/${plan.id}`);
    const main = document.querySelector("main");
    check(!main.textContent.includes("页面暂时无法显示"), `Current field crashed ${plan.id}`);
    check(main.textContent.includes("当前修订计划项") && main.textContent.includes("损坏"), `Current diagnosis missing ${plan.id}`);
    check(main.textContent.includes("完成比例 0%"), `Intact baseline hidden ${plan.id}`);
    const raw = [...main.querySelectorAll("details")].find(d => d.querySelector("summary")?.textContent === "查看原始当前修订计划项");
    check(raw && JSON.stringify(JSON.parse(raw.querySelector("pre").textContent)) === JSON.stringify(plan.items), `Original current data lost ${plan.id}`);
    check([...main.querySelectorAll("button")].filter(b => /编辑计划项|提交计划|确认固定计划/.test(b.textContent)).every(b => b.disabled), `Damaged current write enabled ${plan.id}`);
    check(document.documentElement.scrollWidth <= innerWidth + 2, `Current diagnosis overflow ${plan.id}`);
    check(storage() === before, `Current diagnosis changed storage ${plan.id}`);
  }
  await go("/plans/BASELINE-FOREIGN");
  check(document.querySelector("main")?.textContent.includes("计划不存在或无权访问"), "Foreign tenant Plan is readable");
  await go("/plans/me");
  check(!document.querySelector('a[href="#/plans/BASELINE-FOREIGN"]'), "Foreign tenant Plan leaked into list");
  await go("/reports/BASELINE-REPORT");
  check(document.querySelector("main")?.textContent.includes("本期存在重复计划"), "Report arbitrarily merges conflicting baselines");
  const edit=[...document.querySelectorAll("main button")].find(button=>button.textContent.trim()==="编辑");
  check(edit&&!edit.disabled,"Report editor missing");edit.click();await wait();
  const dialog=document.querySelector("dialog[open]"), adopt=[...dialog.querySelectorAll("button")].find(button=>button.textContent.trim()==="采用下期计划当前版本");
  check(adopt&&!adopt.disabled,"Report Plan adoption entry missing");adopt.click();await wait();
  check(dialog.querySelector('[role="alert"]')?.textContent.includes("已确认计划"),"Corrupt next-Plan adoption did not show actionable rejection");
  check(storage()===before,"Rejected adoption mutated report or original Plan");
  const cancel=[...dialog.querySelectorAll("button")].find(button=>button.textContent.trim()==="取消");check(cancel,"Cancel missing");cancel.click();await wait();check(!document.querySelector("dialog[open]"),"Rejected adoption dirtied report editor");
  await go("/plans/BASELINE-VALID");
  check(document.querySelector("main")?.textContent.includes("完成比例 0%"), "Valid uncompleted baseline was hidden");
  for (const [kind, message] of [["MISSING", "N2：本期尚无"], ["DRAFT", "不能使用草稿承诺"], ["PENDING", "原已确认基线"], ["FROZEN", "N2：本期尚无"]]) {
    await go(`/reports/CURRENT-${kind}`);
    const main = document.querySelector("main");
    check(main?.textContent.includes(message), `Missing current Plan diagnostic ${kind}`);
    if (kind === "FROZEN") check(!main.textContent.includes("不能使用草稿承诺"), "Fixed history read the replacement live draft");
    check(document.documentElement.scrollWidth <= innerWidth + 2, `Current Plan diagnostic overflow ${kind}`);
    check(storage() === before, `Diagnostic read changed raw evidence ${kind}`);
  }
  check(storage() === before, "Navigation changed persistent facts");
  return { pass: true, width: innerWidth, checks: ["24 corrupted baseline diagnostics", "no unauthorized writes or zero progress", "cross-tenant detail and list isolation", "report explicit unavailability", "valid baseline compatibility", "raw persistent facts unchanged", "viewport fit"] };
})()
