// Fixture uses real Human rework/QA/delivery and external receipt actions.
(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 180));
  const check = (value, message) => { if (!value) throw Error(message); };
  const state = () => JSON.parse(localStorage.getItem("collaboration-prototype-v1"));
  const cases = state().testDeliveryReceiptCases;
  check(cases?.length === 4, "Missing four actual delivery receipt cases");
  const original = JSON.stringify({ deliveries: state().resources.deliveries, work: state().work, events: state().events });
  const results = [];
  for (const entry of cases) {
    location.hash = `/deliveries/${entry.id}`; await wait();
    const record = state().resources.deliveries.find(delivery => delivery.id === entry.id);
    check(record.status === entry.status && document.querySelector("main h1")?.textContent === record.name, "Actual fixed delivery detail missing");
    const controls = ["记录接收", "逐项部分接受", "拒收并退回"].map(label => [...document.querySelectorAll("main button")].find(button => button.textContent.trim() === label));
    check(controls.every(Boolean), "Receipt action controls disappeared without explanation");
    check(controls.every(button => button.disabled === (entry.status !== "待回执")), `Incorrect receipt controls for ${entry.status}`);
    if (entry.status !== "待回执") {
      check(document.querySelector("main").textContent.includes("此包已有固定回执，结果与拒收依据保留"), "Fixed receipt guidance missing");
      for (const button of controls) button.click(); await wait();
      check(!document.querySelector("dialog[open]"), "Disabled fixed receipt action opened an editor");
    }
    check(document.documentElement.scrollWidth <= innerWidth + 2, "Fixed receipt detail overflows viewport");
    results.push({ status: entry.status, disabled: controls.map(button => button.disabled), width: innerWidth });
  }
  for (const role of ["executor", "owner", "external"]) {
    location.hash = "/settings"; await wait();
    const combo = document.querySelector('[role="combobox"][aria-label="角色视角"]');
    check(combo, "Missing real simulated role selector"); combo.click(); await wait();
    const option = [...document.querySelectorAll('[role="option"]')].find(option => option.textContent.trim() === role);
    check(option, "Missing role option " + role); option.click(); await wait();
    check(state().role === role, "Actual role change not persisted");
    for (const entry of cases) {
      location.hash = `/deliveries/${entry.id}`; await wait();
      const controls = ["记录接收", "逐项部分接受", "拒收并退回"].map(label => [...document.querySelectorAll("main button")].find(button => button.textContent.trim() === label));
      check(controls.every(Boolean), "Missing scoped receipt buttons for " + role);
      check(controls.every(button => button.disabled === (role === "executor" || entry.status !== "待回执")), "Receipt authority inconsistent for " + role + "/" + entry.status);
      if (role === "executor") {
        check(document.querySelector("main").textContent.includes("当前模拟身份没有回执登记权限"), "No explicit recorder authority explanation");
        for (const button of controls) button.click(); await wait();
        check(!document.querySelector("dialog[open]"), "Unauthorized receipt editor opened");
      }
      check(document.documentElement.scrollWidth <= innerWidth + 2, "Authority view overflows viewport");
      results.push({ role, status: entry.status, disabled: controls.map(button => button.disabled), width: innerWidth });
    }
  }
  check(JSON.stringify({ deliveries: state().resources.deliveries, work: state().work, events: state().events }) === original, "Read-only receipt views changed fixed records");
  return { pass: true, width: innerWidth, results, checks: ["four genuinely generated package states", "pending action availability", "all terminal actions disabled", "visible new-package recovery guidance", "no disabled click side effects", "record immutability", "viewport fit", "real role selector and all 3 roles × 4 package states", "uniform recorder authority", "no unauthorized dialog or side effects"] };
})()
