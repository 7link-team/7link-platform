// Run via agent-browser eval --stdin in a fresh local prototype session.
// REQ-PREVIEW-013 / EXT-25: state is scoped to tenant/task/tab, not component lifetime.
(async () => {
  const wait = () => new Promise((r) => setTimeout(r, 140));
  const check = (v, message) => {
    if (!v) throw Error(message);
  };
  check(document.visibilityState === "visible", "Preview keyboard tests require a visible browser tab (animation frames pause in background tabs)");
  const openPreview = async () => {
    if (matchMedia("(max-width: 1250px)").matches && !document.querySelector("dialog:modal")) {
      document.querySelector('[aria-label="切换会话上下文"]').click();
      await wait();
    }
  };
  const panel = () =>
    document.querySelector(".task-previews [role=tabpanel]:not([hidden])");
  const click = async (text, scope = document) => {
    const el = [...scope.querySelectorAll("button")].find(
      (e) => e.textContent.trim() === text,
    );
    check(el, `Missing button ${text}`);
    el.click();
    await wait();
  };
  const fill = async (selector, value, scope = document) => {
    const el = scope.querySelector(selector);
    check(el, `Missing input ${selector}`);
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    ).set.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
    await wait();
  };
  location.hash = "/chat?execution_id=W-104";
  await wait();
  await openPreview();
  check(!document.querySelector('[aria-label="添加预览标签"]'), "Preview tabs must not be manually added");
  check(!document.querySelector('.task-previews [aria-label^="关闭 "]'), "Preview tabs must not be manually removed");
  // Navigation belongs to each preview, not the application's location/history.
  const applicationHash = location.hash;
  await fill('[aria-label="预览路径"]', '/first', panel());
  panel().querySelector('[aria-label="打开预览路径"]').click();await wait();
  await fill('[aria-label="预览路径"]', '/second', panel());
  panel().querySelector('[aria-label="打开预览路径"]').click();await wait();
  panel().querySelector('[aria-label="预览后退"]').click();await wait();
  check(panel().querySelector('[aria-label="预览路径"]').value === '/first', 'Preview back history');
  panel().querySelector('[aria-label="预览前进"]').click();await wait();
  check(panel().querySelector('[aria-label="预览路径"]').value === '/second', 'Preview forward history');
  panel().querySelector('[aria-label="刷新预览"]').click();await wait();
  check(panel().textContent.includes('示例已刷新'), 'Preview refresh has observable result');
  check(location.hash === applicationHash, 'Preview navigation changed application route');
  for (const [name, path] of [
    ["Web · 主预览", "/store"],
    ["Web · 管理端", "/admin"],
  ]) {
    await click(name);
    await fill('[aria-label="预览路径"]', path, panel());
  }
  await click("API");
  await fill("input:not([type=hidden])", "/healthz", panel());
  await click("模拟请求", panel());
  await click("终端");
  await fill('[aria-label="任务终端命令"]', "pwd", panel());
  panel().querySelector('[aria-label="执行模拟命令"]').click();
  await wait();
  await fill('[aria-label="任务终端命令"]', "status", panel());
  for (let i = 0; i < 55; i++) {
    await fill('[aria-label="任务终端命令"]', "help", panel());
    panel().querySelector('[aria-label="执行模拟命令"]').click(); await wait();
  }
  await fill('[aria-label="任务终端命令"]', "status", panel());
  panel().querySelector('pre').scrollTop = 80; await wait();
  check(panel().querySelector('pre').scrollTop === 80, "Terminal fixture must actually scroll");
  await click("模拟断线", panel()); await click("重新连接", panel());
  check(panel().querySelector('pre').scrollTop === 80, "Reconnect retains terminal reading position");
  await click("仓库代码");
  await click("acceptance.md", panel());
  for (const [name, expected] of [
    ["Web · 主预览", "/store"],
    ["Web · 管理端", "/admin"],
    ["API", "/healthz"],
  ]) {
    await click(name);
    check(
      panel().querySelector("input:not([type=hidden])").value === expected,
      `Independent state: ${name}`,
    );
    await click("模拟断线", panel());
    check(
      panel().textContent.includes("预览连接已断开"),
      "Disconnect feedback",
    );
    await click("重新连接", panel());
    check(
      panel().querySelector("input:not([type=hidden])").value === expected,
      `Reconnect state: ${name}`,
    );
  }
  location.hash = "/chat?execution_id=W-103";
  await wait();
  await openPreview();
  check(
    !document.querySelector(".preview-tablist").textContent.includes("Web · 管理端"),
    "Task isolation",
  );
  location.hash = "/chat?execution_id=W-104";
  await wait();
  await openPreview();
  const barBounds = document.querySelector('.preview-tablist').getBoundingClientRect();
  const activeBounds = document.querySelector('.preview-tablist [aria-selected=true]').getBoundingClientRect();
  check(activeBounds.left >= barBounds.left - 1 && activeBounds.right <= barBounds.right + 1, "Restored active tab remains visible");
  await click("Web · 主预览");
  check(
    panel().querySelector("input").value === "/store",
    "Remount retains Web path",
  );
  await click("API");
  check(
    panel().querySelector("pre").textContent.includes("/healthz"),
    "Remount retains API result",
  );
  await click("终端");
  check(panel().querySelector("pre").scrollTop === 80, "Remount retains terminal reading position");
  check(
    panel().querySelector("input").value === "status",
    "Remount retains terminal draft",
  );
  check(
    panel().querySelector("pre").textContent.includes("/workspace/W-104"),
    "Remount retains terminal history",
  );
  await click("仓库代码");
  check(
    panel().querySelector(".repository-tree .active").textContent ===
      "acceptance.md",
    "Remount retains file selection",
  );
  await click("Web · 管理端");
  document.activeElement.dispatchEvent(
    new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }),
  );
  await wait();
  check(document.activeElement.textContent === "API", "Keyboard next tab");
  document.activeElement.dispatchEvent(
    new KeyboardEvent("keydown", { key: "Delete", bubbles: true }),
  );
  await wait();
  check(
    document.activeElement.textContent === "API",
    "Delete does not remove system-provided tabs",
  );
  // UI is read-only/offline-capable even while service operations remain blocked.
  location.hash = "/settings";
  await wait();
  const offline = [...document.querySelectorAll("input[type=checkbox]")].find(
    (e) => e.closest("label")?.textContent.includes("离线"),
  );
  if (offline) {
    offline.click();
    await wait();
  } else throw Error("Offline setting not found");
  location.hash = "/chat?execution_id=W-104";
  await wait();
  await openPreview();
  await click("Web · 主预览");
  check(
    panel().querySelector("input").value === "/store",
    "Offline tab switching",
  );
  check(document.querySelectorAll('.preview-tablist [role=tab]').length === 6, "System preview resources stay present offline");
  location.hash = "/settings";
  await wait();
  [...document.querySelectorAll("input[type=checkbox]")]
    .find((e) => e.closest("label")?.textContent.includes("离线"))
    .click();
  await wait();
  location.hash = "/chat?execution_id=W-104";
  await wait();
  await openPreview();
  check(document.querySelectorAll(".preview-tablist [role=tab]").length === 6, "System tabs survive reconnect");
  return {
    pass: true,
    checks: [
      "same-kind independent state",
      "task isolation and remount state",
      "disconnect/reconnect",
      "keyboard focus and no manual deletion",
      "offline navigation",
      "system resource tabs persist",
    ],
  };
})();
