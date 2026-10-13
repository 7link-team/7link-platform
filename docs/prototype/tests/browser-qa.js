// Run in an isolated fresh prototype browser session (owner role, seed records).
(async () => {
  const wait = () => new Promise((resolve) => setTimeout(resolve, 160));
  const check = (condition, message) => {
    if (!condition) throw Error(message);
  };
  const until = async (fn) => {
    const end = Date.now() + 10000;
    while (!fn()) {
      if (Date.now() > end) throw Error("UI timed out");
      await wait();
    }
  };
  const state = () =>
    JSON.parse(localStorage.getItem("collaboration-prototype-v1"));
  const testcase = () =>
    state().resources.tests.find((item) => item.id === "TC-01");
  const defect = () =>
    (() => { const work = state().work.find(item => item.id === "BUG-01"); return { ...work.defect, status: work.defect.stage === "CLOSED" ? "已关闭" : work.status === "待验收" && work.defect.repairVersion ? "待验证" : "待修复" }; })();
  const go = async (path) => {
    location.hash = path;
    await wait();
  };
  const click = async (label, root = document) => {
    const button = [...root.querySelectorAll("button")].find(
      (item) => item.textContent.trim() === label,
    );
    check(button, `Missing button ${label}`);
    button.click();
    await wait();
  };
  const fill = async (label, value) => {
    const input = document.querySelector(`[name="${label}"]`);
    check(input, `Missing field ${label}`);
    const prototype =
      input.tagName === "TEXTAREA"
        ? HTMLTextAreaElement.prototype
        : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, "value").set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await wait();
  };
  const action = async (label, runId) => {
    await click(label);
    await until(() => document.querySelector("dialog[open]"));
    if (runId) {
      document.querySelector('[aria-label="独立验证运行"]').click();
      await until(() => document.querySelector('[role="listbox"]'));
      const option = [...document.querySelectorAll('[role="option"]')].find(
        (item) => item.textContent.includes(runId),
      );
      check(option, `Missing eligible run ${runId}`);
      option.click();
      await wait();
    }
    await fill("依据与说明", `浏览器验收：${label}`);
    await click("确认操作", document.querySelector("dialog"));
  };
  await go("/defects/BUG-01");
  await action("验证通过");
  check(
    document.querySelector("dialog").textContent.includes("请先提交修复"),
    "Draft defect closed without repair",
  );
  await click("取消", document.querySelector("dialog"));
  check(document.querySelectorAll("dialog[open]").length === 2, "Rejected defect action lost discard guard");
  await click("放弃修改", [...document.querySelectorAll("dialog[open]")].at(-1));
  await action("提交修复");
  check(defect().status === "待验证", "Repair not submitted");
  await go("/tests/TC-01");
  await action("记录失败");
  const failed = structuredClone(testcase().runs.at(-1));
  const baseline = structuredClone(testcase().runs[0]);
  check(
    baseline.evidence === "run-104 / mobile-375" &&
      baseline.actual === "导航遮挡内容",
    "Old failure facts were overwritten",
  );
  check(
    failed.source === "mock" && failed.evidence !== baseline.evidence,
    "New failure reused old evidence",
  );
  check(
    document.querySelector("main").textContent.includes("固定运行记录"),
    "Run snapshots absent from UI",
  );
  await go("/defects/BUG-01");
  await action("验证失败并重开", failed.id);
  check(
    defect().status === "待修复" && defect().verification.id === failed.id,
    "Failed verification did not reopen",
  );
  await action("提交修复");
  await go("/tests/TC-01");
  await action("模拟重验通过");
  const passed = structuredClone(testcase().runs.at(-1));
  check(
    passed.sourceKind === "平台独立验证" && passed.defectRepairRevision === 2,
    "Run lost independent source/repair revision",
  );
  await go("/defects/BUG-01");
  await action("验证通过", passed.id);
  check(
    defect().status === "已关闭" && defect().verification.id === passed.id,
    "Passing verification did not close",
  );
  check(
    defect().history.some((item) => item.verification?.id === failed.id),
    "Prior failed verification lost",
  );
  await go("/tests/TC-01");
  const runs = JSON.stringify(testcase().runs);
  await click("编辑");
  await fill("输入", "已修改的下一轮输入");
  await click("保存", document.querySelector("dialog"));
  check(
    testcase().status === "待执行" && !testcase().currentRun,
    "Case edit retained valid execution status",
  );
  check(
    JSON.stringify(testcase().runs) === runs,
    "Case edit rewrote past run snapshots",
  );
  check(
    defect().verification.id === passed.id,
    "Case edit changed historic closure evidence",
  );
  return {
    pass: true,
    checks: [
      "repair guard",
      "full old failure snapshot",
      "new attempt evidence",
      "failed verification reopens",
      "independent current repair closes",
      "case edit preserves runs",
    ],
  };
})();
