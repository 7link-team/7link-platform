// Run with agent-browser eval --stdin in an isolated desktop session.
// Real keyboard regression (synthetic key events cannot exercise native Tab):
// open /#/settings; click '[aria-label="主题"]'; press Tab
// expect focus on checkbox 深色模式 and no [role=listbox].
// click '[aria-label="主题"]'; press Shift+Tab
// expect focus on .connection and no [role=listbox].
(async () => {
  const check = (condition, message) => {
    if (!condition) throw Error(message);
  };
  const until = async (fn) => {
    const deadline = Date.now() + 10000;
    while (!fn()) {
      if (Date.now() > deadline) throw Error("UI condition timed out");
      await new Promise((r) => setTimeout(r, 50));
    }
  };
  location.hash = "/settings";
  await until(() => document.querySelector('[aria-label="主题"]'));
  check(
    !document.querySelector("dialog[open]"),
    "Start without an open dialog",
  );
  document.querySelector(".tenant-trigger").click();
  await until(() => document.querySelector(".tenant-popover"));
  [...document.querySelectorAll(".tenant-popover button")]
    .find((el) => el.textContent.trim() === "创建租户")
    .click();
  await until(() => document.querySelector('dialog input[name="租户名称"]'));
  document.querySelector('dialog button[type="submit"]').click();
  await until(() => document.querySelector(".field-error"));
  check(
    document.querySelector("form").noValidate,
    "Native validation popup enabled",
  );
  check(
    document.activeElement === document.querySelector("dialog input"),
    "First invalid input not focused",
  );
  const results = [];
  const freeze = document.createElement("style");
  freeze.textContent = "*,*::before,*::after{transition:none!important}";
  document.head.append(freeze);
  const rgb = (c) => {
    const a = c.match(/[\d.]+/g).map(Number);
    return c.startsWith("color(") ? a.map((v, i) => (i < 3 ? v * 255 : v)) : a;
  };
  const blend = (c, b) =>
    c.slice(0, 3).map((v, i) => v * (c[3] ?? 1) + b[i] * (1 - (c[3] ?? 1)));
  const lum = (c) =>
    c
      .slice(0, 3)
      .map((v) => v / 255)
      .map((v) => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
      .reduce((a, v, i) => a + v * [0.2126, 0.7152, 0.0722][i], 0);
  const ratio = (a, b) => {
    const x = lum(a),
      y = lum(b);
    return +((Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)).toFixed(2);
  };
  const root = document.documentElement,
    originalTheme = root.dataset.theme,
    originalDark = root.classList.contains("dark");
  for (const theme of [
    "aurora",
    "modern",
    "business",
    "eyeCare",
    "pinkKawaii",
    "neoBrutalism",
  ]) {
    root.dataset.theme = theme;
    for (const dark of [false, true]) {
      root.classList.toggle("dark", dark);
      const dialog = document.querySelector("dialog"),
        card = rgb(getComputedStyle(dialog).backgroundColor),
        button = dialog.querySelector(".btn.primary"),
        error = dialog.querySelector(".field-error"),
        input = dialog.querySelector("input");
      const style = getComputedStyle(button),
        err = getComputedStyle(error),
        field = getComputedStyle(input);
      results.push({
        theme,
        dark,
        primary: ratio(rgb(style.color), rgb(style.backgroundColor)),
        error: ratio(rgb(err.color), card),
        input: ratio(rgb(field.color), blend(rgb(field.backgroundColor), card)),
        scrollbar: getComputedStyle(root).scrollbarColor,
        colorScheme: getComputedStyle(root).colorScheme,
      });
    }
  }
  root.dataset.theme = originalTheme;
  root.classList.toggle("dark", originalDark);
  freeze.remove();
  for (const item of results) {
    check(
      item.primary >= 4.5,
      `${item.theme}/${item.dark}: primary contrast ${item.primary}`,
    );
    check(
      item.error >= 4.5,
      `${item.theme}/${item.dark}: error contrast ${item.error}`,
    );
    check(
      item.input >= 4.5,
      `${item.theme}/${item.dark}: input contrast ${item.input}`,
    );
    check(
      item.colorScheme === (item.dark ? "dark" : "light"),
      "Wrong native color scheme",
    );
    check(item.scrollbar !== "auto", "Scrollbar escaped custom theme");
  }
  document.querySelector('dialog [aria-label="关闭弹窗"]').click();
  await until(() => !document.querySelector('dialog[open]'));
  await until(() => document.activeElement === document.querySelector('.tenant-trigger'));
  document.querySelector(".tenant-trigger").click();
  await until(() => document.querySelector(".tenant-popover"));
  [...document.querySelectorAll(".tenant-popover button")]
    .find((el) => el.textContent.trim() === "加入租户")
    .click();
  await until(() => document.querySelector("dialog[open]"));
  document.querySelector('dialog [aria-label="关闭弹窗"]').click();
  await until(() => !document.querySelector("dialog[open]"));
  await until(() => document.activeElement === document.querySelector(".tenant-trigger"));
  return { pass: true, fixtures: results, tenantFocusRestored: true, joinFocusRestored: true };
})();
