// The fixture uses real local commands. Rendering/exporting must never alter them.
(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 180));
  const state = () => JSON.parse(localStorage.getItem("collaboration-prototype-v1"));
  const fixture = state().testResourcePresentation;
  const check = (value, message) => { if (!value) throw Error(message); };
  check(fixture, "Missing actual resource presentation fixture");
  const failures = [], results = [];
  const verify = (value, message) => { if (!value) failures.push(message); };
  const section = title => [...document.querySelectorAll("main section")].find(element => (() => {const heading=element.querySelector("h2")?.cloneNode(true);heading?.querySelectorAll(".section-anchor").forEach(anchor=>anchor.remove());return heading?.textContent.trim()===title;})());
  const go = async path => { location.hash = path; await wait(); };
  const original = localStorage.getItem("collaboration-prototype-v1");
  if (fixture.variant) {
    const variant = fixture.variant;
    if (variant.startsWith("requirements")) {
      await go("/tests/" + fixture.testId);
      const term = [...(section("基本信息")?.querySelectorAll("dt") || [])].find(term => term.textContent === "关联要求");
      verify(term?.nextElementSibling.textContent.includes("条款记录格式无效，请核对原始记录"), "Malformed requirement field is silently hidden or serialized as JSON");
      verify(!term?.nextElementSibling.textContent.includes("internal-token"), "Raw imported requirement object leaked into UI");
      if (variant === "requirementsItem") verify(term?.nextElementSibling.textContent.includes("保留有效条款"), "Valid requirement disappeared alongside invalid element");
      verify(localStorage.getItem("collaboration-prototype-v1") === original, "Rendering malformed requirements changed their original value");
      return { pass: failures.length === 0, width: innerWidth, failures, variant, checks: ["actual native reload of malformed requirements", "explicit error, no raw JSON", "original record retained"] };
    }
    await go("/deliveries/" + fixture.deliveries[0].id);
    const source = section("固定发送记录");
    verify(Boolean(source), "Imported source view crashed: " + variant);
    if (variant === "legacy") verify(source?.textContent.includes("不会从当前负责人或工作状态补造历史"), "Legacy package invents send history");
    if (variant === "missing" || variant === "malformed") verify(source?.textContent.includes("未记录") && !source.textContent.includes("H-lin"), "Missing provenance replaced by current owner");
    if (variant === "unknown") verify(source?.textContent.includes("未提供可核验的发送来源") && !source.textContent.includes("本地模拟发送记录"), "Unknown imported source falsely certified as local simulation");
    verify(document.documentElement.scrollWidth <= innerWidth + 2, "Imported source view overflows: " + variant);
    verify(localStorage.getItem("collaboration-prototype-v1") === original, "Reading imported source rewrote its history");
    return { pass: failures.length === 0, width: innerWidth, failures, variant, checks: ["actual native reload of imported source", "no invented sender/version", "honest missing or unknown source", "viewport fit", "no history mutation"] };
  }
  const exports = [];
  const create = URL.createObjectURL, revoke = URL.revokeObjectURL, anchorClick = HTMLAnchorElement.prototype.click;
  // Capture the actual Blob and download click, rather than replacing exportData.
  URL.createObjectURL = blob => { exports.push({ blob }); return "blob:local-presentation-export"; };
  URL.revokeObjectURL = url => { if (url !== "blob:local-presentation-export") revoke.call(URL, url); };
  HTMLAnchorElement.prototype.click = function() {
    if (this.download) { exports.at(-1).download = this.download; return; }
    return anchorClick.call(this);
  };
  const exportRecord = async (kind, record) => {
    const button = [...document.querySelectorAll("main button")].find(button => button.textContent.trim() === "导出");
    check(button, "Actual export button missing");
    button.click(); await wait();
    const exported = exports.at(-1);
    verify(exported?.download === kind + ".json" && await exported.blob.text() === JSON.stringify(record, null, 2), "Export changed or lost original " + kind + " record");
  };
  try {
    for (const entry of fixture.deliveries) {
      await go("/deliveries/" + entry.id);
      const record = state().resources.deliveries.find(item => item.id === entry.id);
      verify(!section("基本信息")?.textContent.includes("sendFact"), "Delivery basic information leaked raw sendFact");
      const source = section("固定发送记录");
      verify(source?.textContent.includes("本地模拟发送记录，不代表真实远程发布或客户接收"), "Delivery source has no honest local simulation label");
      const pairs = [...(source?.querySelectorAll("dt") || [])].map(term => [term.textContent, term.nextElementSibling?.textContent]);
      const fields = Object.fromEntries(pairs);
      verify(fields["记录主体"]?.includes(record.sendFact.actor.id), "Original sender is missing from the dedicated source view");
      verify(fields["固定交付包"]?.includes(record.sendFact.deliverySnapshot.id), "Original fixed package identity missing from the source view");
      verify(fields["关联工作 / 固定版本"]?.includes(record.sendFact.deliverySnapshot.workId) && fields["关联工作 / 固定版本"]?.includes("v" + record.sendFact.deliverySnapshot.version), "Original fixed work/version missing from the source view");
      verify(section("固定交付清单与逐项回执")?.textContent.includes(record.items[0].name), "Structured package items disappeared");
      verify(document.documentElement.scrollWidth <= innerWidth + 2, "Delivery presentation overflows viewport");
      await exportRecord("deliveries", record);
      results.push({ kind: "deliveries", status: entry.status, width: innerWidth });
    }
    await go("/reports/" + fixture.reportId);
    const report = state().resources.reports.find(item => item.id === fixture.reportId);
    verify(!section("基本信息")?.textContent.includes("currentPlanBinding"), "Report duplicated its internal Plan binding as JSON");
    verify(section("本期计划固定引用")?.textContent.includes("测试场景：调研仍待完成"), "Report fixed Plan commitment disappeared");
    await exportRecord("reports", report);
    await go("/models/" + fixture.modelId);
    const model = state().resources.models.find(item => item.id === fixture.modelId);
    verify(!section("基本信息")?.textContent.includes("nodeResults"), "Model duplicated internal node results as JSON");
    verify(section("下发与能力")?.textContent.includes("节点下发结果仅为本地模拟"), "Model application results falsely imply real node delivery");
    verify(model.nodeResults?.length === 3, "Actual model command did not produce three node results");
    verify(section("下发与能力")?.querySelectorAll(".node-status-list > div").length === model.nodeResults.length, "Structured node results disappeared");
    for (const result of model.nodeResults) verify(section("下发与能力")?.textContent.includes(result.status), "Actual node application status missing");
    await exportRecord("models", model);
    await go("/tests/" + fixture.testId);
    const test = state().resources.tests.find(item => item.id === fixture.testId);
    const term = [...section("基本信息").querySelectorAll("dt")].find(term => term.textContent === "关联要求");
    verify(term?.nextElementSibling.querySelectorAll("li").length === test.requirementRefs.length, "Business requirement list missing or incomplete");
    for (const requirement of test.requirementRefs) verify(term?.nextElementSibling.textContent.includes(requirement), "Original requirement text missing from its semantic list");
    await exportRecord("tests", test);
    verify(localStorage.getItem("collaboration-prototype-v1") === original, "Reading/exporting changed original business records");
    return { pass: failures.length === 0, width: innerWidth, failures, results, exportedRecords: exports.length,
      checks: ["all four actual delivery states", "no raw technical duplicates", "fixed sender/package provenance", "honest simulation/missing source", "fixed Plan and node results retained", "business requirement list", "actual Blob export unchanged", "no read/export mutations", "legacy and malformed imported views"] };
  } finally {
    URL.createObjectURL = create; URL.revokeObjectURL = revoke; HTMLAnchorElement.prototype.click = anchorClick;
  }
})()
