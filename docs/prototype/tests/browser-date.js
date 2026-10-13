// Shared date Field regression: run at desktop and 375 px in an isolated browser.
(async () => {
  const wait = () => new Promise((resolve) => setTimeout(resolve, 150));
  const check = (value, message) => {
    if (!value) throw Error(message);
  };
  const click = async (label) => {
    const node = [...document.querySelectorAll("button")].find(
      (node) =>
        node.textContent.trim() === label ||
        node.getAttribute("aria-label") === label,
    );
    check(node, `Missing ${label}`);
    node.click();
    await wait();
  };
  location.hash = "/reports";
  await wait();
  await click("生成 / 打开周期报告");
  const modal = document.querySelector("dialog[open]"),
    input = modal.querySelector('[name="周期内日期"]');
  check(input?.type === "text", "Native date input still present");
  const trigger = modal.querySelector(".date-trigger");
  const fill = async (value) => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    ).set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new Event("change", { bubbles: true }));
    await wait();
  };
  await fill("2024-02-29");
  check(
    input.value === "2024-02-29" && input.checkValidity(),
    "Leap day rejected",
  );
  trigger.click();
  await wait();
  let panel = document.querySelector(".date-popover");
  check(
    panel && panel.closest("dialog") === modal,
    "Calendar not mounted inside modal",
  );
  check(
    document.activeElement.dataset.date === "2024-02-29",
    "Opening focus wrong",
  );
  const key = async (key) => {
    document.activeElement.dispatchEvent(
      new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }),
    );
    await wait();
  };
  await key("ArrowRight");
  check(
    document.activeElement.dataset.date === "2024-03-01",
    "Day boundary navigation failed",
  );
  await key("PageUp");
  check(
    document.activeElement.dataset.date === "2024-02-01",
    "Month navigation failed",
  );
  await key("End");
  check(
    document.activeElement.dataset.date === "2024-02-04",
    "Week end navigation failed",
  );
  document.activeElement.click();
  await wait();
  check(
    input.value === "2024-02-04" && !document.querySelector(".date-popover"),
    "Selection not saved",
  );
  check(document.activeElement === trigger, "Selection focus not restored");
  trigger.click();
  await wait();
  await key("Escape");
  check(
    !document.querySelector(".date-popover") &&
      modal.open &&
      document.activeElement === trigger,
    "Escape closed parent or lost focus",
  );
  for (const value of [
    "2025-02-29",
    "2024-04-31",
    "0000-01-01",
    "2024-13-01",
    "2024-2-1",
  ]) {
    await fill(value);
    check(!input.checkValidity(), `Accepted invalid date ${value}`);
  }
  input.blur();
  await wait();
  check(
    modal.querySelector(".field-error")?.textContent.includes("有效日期"),
    "Missing custom date error",
  );
  await fill("");
  check(!input.checkValidity(), "Required date accepted empty");
  await fill("2024-02-29");
  check(input.checkValidity(), "Valid date did not clear custom validity");
  trigger.click();
  await wait();
  panel = document.querySelector(".date-popover");
  const rect = panel.getBoundingClientRect();
  check(
    rect.left >= 0 &&
      rect.right <= innerWidth &&
      rect.top >= 0 &&
      rect.bottom <= innerHeight,
    "Calendar exceeds viewport",
  );
  const inputName = input.name;
  check(
    new FormData(input.closest("form")).get(inputName) === "2024-02-29",
    "FormData compatibility lost",
  );
  return {
    pass: true,
    width: innerWidth,
    nativeDateInputs: document.querySelectorAll('input[type="date"]').length,
    invalidDates: 5,
    calendarBounds: {
      left: rect.left,
      right: rect.right,
      top: rect.top,
      bottom: rect.bottom,
    },
    parentModalOpen: modal.open,
  };
})();
