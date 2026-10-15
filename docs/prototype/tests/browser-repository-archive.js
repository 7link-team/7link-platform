(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 180));
  const check = (value, message) => { if (!value) throw Error(message); };
  const state = () => JSON.parse(localStorage.getItem("collaboration-prototype-v1"));
  const dialog = () => document.querySelector("dialog[open]");
  const go = async path => { location.hash = path; await wait(); };
  const click = async (text, scope = document) => {
    const button = [...scope.querySelectorAll("button")].find(button => !button.disabled && (button.textContent.trim() === text || button.getAttribute("aria-label") === text));
    check(button, `Missing enabled ${text}`); button.click(); await wait();
  };
  const fill = async (label, value) => {
    const field = (dialog() || document).querySelector(`[name="${label}"]`) || (dialog() || document).querySelector(`[aria-label="${label}"]`);
    check(field, `Missing field ${label}`);
    Object.getOwnPropertyDescriptor(field.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value").set.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true })); await wait();
  };
  const choose = async (label, text) => {
    const field = document.querySelector(`[role="combobox"][aria-label="${label}"]`); check(field, `Missing select ${label}`);
    field.click(); await wait();
    const option = [...document.querySelectorAll('[role="option"]')].find(option => option.textContent.trim() === text); check(option, `Missing option ${text}`);
    option.click(); await wait();
  };
  const original = structuredClone(state().resources.repositories[0]);
  await go(`/repositories/${original.id}`);
  check(document.querySelector("main").textContent.includes("所属项目未记录"), "Legacy repository silently inferred project");
  check([...document.querySelectorAll("main button")].find(button => button.textContent.trim() === "归档仓库")?.disabled, "Unbound legacy repository archive available before scope confirmation");
  await click("编辑连接配置");
  await choose("所属项目", state().projects.find(project => project.id === "launch").name);
  await click("保存", dialog());
  let repository = state().resources.repositories.find(repository => repository.id === original.id);
  check(repository.projectId === "launch" && repository.history.length >= 1, "Explicit binding not recorded");
  const commit = repository.commit, messages = JSON.stringify(state().messages), work = JSON.stringify(state().work);
  await click("归档仓库"); await fill("归档理由", "本项目仓库停止登记新工作目录"); await click("确认归档仓库", dialog());
  repository = state().resources.repositories.find(repository => repository.id === original.id);
  check(repository.archivedAt && repository.archiveReason === "本项目仓库停止登记新工作目录" && repository.id === original.id && repository.commit === commit, "Archive destroyed stable history or omitted reason");
  check(JSON.stringify(state().messages) === messages && JSON.stringify(state().work) === work, "Repository archive cancelled work or changed unrelated facts");
  check(![...document.querySelectorAll("main button")].some(button => ["编辑连接配置", "模拟增量同步", "暂停同步", "归档仓库"].includes(button.textContent.trim())), "Archived repository retains write actions");
  check(document.querySelector("main").textContent.includes("已归档 · 历史只读"), "Archived detail lacks readonly status");
  await go("/repositories");
  check(!document.querySelector(`a[href^="#/repositories/${original.id}?"]`), "Archived record appears in active filter");
  await choose("归档状态", "已归档");
  check(document.querySelector(`a[href^="#/repositories/${original.id}?"]`), "Archived filter omits historical link");
  await fill("搜索当前授权列表", "no-matching-repository");
  check(document.querySelector("main").textContent.includes("当前条件下没有仓库"), "Archive search empty state missing");
  await go(`/repositories/${original.id}`);
  check(document.querySelector("main").textContent.includes("已归档 · 历史只读"), "Deep link lost archived record");
  check(document.documentElement.scrollWidth <= innerWidth, "Archive flow overflowed viewport");
  return { pass: true, width: innerWidth, checks: ["legacy scope explicit binding", "archive reason and fixed history", "no cancellation side effects", "readonly detail", "archive filter and search", "historical deep link"] };
})();
