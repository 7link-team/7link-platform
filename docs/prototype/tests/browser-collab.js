// Run in an isolated agent-browser session at desktop and mobile widths.
(async () => {
  const wait = () => new Promise((resolve) => setTimeout(resolve, 180));
  const check = (condition, message) => {
    if (!condition) throw Error(message);
  };
  const click = async (text, scope = document) => {
    const target = [...scope.querySelectorAll("button, a")].find(
      (el) => el.textContent.trim() === text,
    );
    check(target, `Missing control: ${text}`);
    target.click();
    await wait();
  };
  const fill = async (selector, value) => {
    const input = document.querySelector(selector);
    check(input, `Missing input: ${selector}`);
    const proto =
      input.tagName === "TEXTAREA"
        ? HTMLTextAreaElement.prototype
        : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, "value").set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await wait();
  };
  const go = async (path) => {
    location.hash = path;
    await wait();
  };
  const state = () =>
    JSON.parse(localStorage.getItem("collaboration-prototype-v1"));
  const compact = matchMedia("(max-width: 1250px)").matches;
  const openContext = async () => {
    if (compact) {
      const trigger = document.querySelector('[aria-label="切换会话上下文"]');
      trigger.focus();
      trigger.click();
      await wait();
      check(
        document.querySelector("dialog:modal"),
        "Context must be a native modal",
      );
      check(
        document.querySelector("dialog").contains(document.activeElement),
        "Context focus was not moved inside",
      );
    }
    return document.querySelector(compact ? "dialog" : ".chat-context");
  };
  const closeContext = async () => {
    if (compact) {
      document.querySelector('dialog [aria-label="关闭弹窗"]').click();
      await wait();
      check(
        document.activeElement?.getAttribute("aria-label") === "切换会话上下文",
        "Context focus not restored",
      );
    }
  };

  const execution = state().executions.find(item => item.workId && item.id !== item.workId);
  check(execution, "Need a saved execution with an independent identity");
  await go(`/chat?execution_id=${encodeURIComponent(execution.id)}`);
  check(document.querySelector("main").textContent.includes(execution.id), "Exact execution was not displayed");
  check(!document.querySelector(".message-actions") &&
    ![...document.querySelectorAll(".composer button")].some(button => button.textContent.trim() === "发送"),
    "Execution exposes ordinary mutable chat or free message sending");
  await go("/chat?execution_id=W-104");
  check(document.querySelector("main").textContent.includes("任务编号不能作为执行编号") && !document.querySelector(".composer"),
    "A work ID was accepted as an execution ID");
  await go("/chat?channel_id=launch");
  const marker = `channel regression ${Date.now()}`;
  await fill(".composer textarea", marker);
  const sentAfter = Date.now();
  await click("发送", document.querySelector(".composer"));
  const sentBefore = Date.now();
  const savedMessage = state().messages.find(item => item.text === marker);
  check(/^\d{4}-\d{2}-\d{2}T.*Z$/.test(savedMessage?.time) &&
    Date.parse(savedMessage.time) >= sentAfter && Date.parse(savedMessage.time) <= sentBefore,
    "Message lost its absolute send date/time");
  const message = [...document.querySelectorAll(".message")].find((el) =>
    el.textContent.includes(marker),
  );
  await click("回复线程", message);
  check(
    new URLSearchParams(location.hash.split("?")[1]).get("channel_id") === "launch" &&
      new URLSearchParams(location.hash.split("?")[1]).get("thread_id") === savedMessage.id,
    "Channel/root identity lost on thread entry",
  );
  check(
    document.querySelector(".messages").textContent.includes(marker),
    "Thread lost its root message",
  );
  await fill(".composer textarea", `${marker} reply`);
  await click("发送", document.querySelector(".composer"));
  const reply = state().messages.find(
    (item) => item.text === `${marker} reply`,
  );
  check(
    reply?.channel === "launch" && reply.thread === savedMessage.id,
    "Reply stored outside the selected channel thread",
  );
  await click("返回频道");
  check(
    new URLSearchParams(location.hash.split("?")[1]).get("channel_id") === "launch" &&
      !new URLSearchParams(location.hash.split("?")[1]).has("thread_id"),
    "Thread exit lost channel context",
  );

  await go("/chat?channel_id=launch");
  const linked = [...document.querySelectorAll(".message")].find((el) =>
    el.querySelector(".message-work"),
  );
  check(linked, "Need a message with an internal work reference");
  await click("共享", linked);
  await click("确认共享选定消息", document.querySelector("dialog"));
  const shared = state().messages.at(-1);
  check(
    !("work" in shared) &&
      !("file" in shared) &&
      !("thread" in shared) &&
      !("history" in shared),
    "Sharing copied unselected metadata",
  );

  await go("/chat?channel_id=private");
  let context = await openContext();
  check(context.textContent.includes("私人会话"), "Private context missing");
  check(
    !context.querySelector('a[href*="/p/launch/"]') &&
      !context.querySelector('[role="progressbar"]'),
    "Private context includes launch project",
  );
  await closeContext();

  if (matchMedia("(max-width: 1023px)").matches) {
    await go("/collaboration");
    const shell = document.querySelector(".app-shell");
    document.querySelector('[aria-label="展开导航"]').click();
    await wait();
    await click("目标", document.querySelector(".sidebar"));
    check(
      !document.querySelector(".sidebar.open") &&
        !document.querySelector(".nav-scrim"),
      "Drawer remains after selection",
    );
    check(
      document.querySelector(".app-shell") === shell && location.hash.split("?")[0] === "#/goals",
      "Menu must retain the shell and record the selected page in its URL",
    );
  }

  document.querySelector(".tenant-trigger").click();
  await wait();
  await click("创建租户");
  await fill('input[name="租户名称"]', `会话回归 ${Date.now()}`);
  await click("创建并进入");
  await go("/chat?channel_id=launch");
  check(
    document.querySelector("main").textContent.includes("此会话不在可访问范围") && !document.querySelector(".composer"),
    "Empty tenant exposed a nonexistent project channel",
  );
  await go("/chat?channel_id=team");
  context = await openContext();
  check(
    context.textContent.includes("暂无关联工作"),
    "Empty tenant lacks context empty state",
  );
  check(
    !context.querySelector('[role="progressbar"]') &&
      !context.querySelector('a[href*="/p/launch/"]'),
    "Empty tenant inherited project context",
  );
  await closeContext();
  document.querySelector(".tenant-trigger").click();
  await wait();
  const studio = [
    ...document.querySelectorAll(".tenant-popover > button"),
  ].find((el) => el.textContent.includes("Northstar Studio"));
  check(studio, "Original tenant missing");
  studio.click();
  await wait();
  return {
    pass: true,
    compact,
    checks: [
      "exact execution identity and no ordinary chat controls",
      "channel absolute timestamp and thread enter/reply/exit",
      "text-only sharing",
      "private scope",
      "empty tenant scope",
      "mobile drawer and modal focus",
    ],
  };
})();
