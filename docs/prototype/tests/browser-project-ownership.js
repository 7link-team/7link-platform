// Each suite starts with fresh state; create a tenant through the visible account UI.
(async () => {
  const pause = () => new Promise(resolve => setTimeout(resolve, 180));
  const check = (condition, message) => { if (!condition) throw Error(message); };
  const click = async (label, root = document) => {
    const button = [...root.querySelectorAll('button')].find(item => item.textContent.trim() === label && item.getClientRects().length);
    check(button, `Missing button ${label}`); button.click(); await pause();
  };
  const fill = async (name, value) => {
    const element = document.querySelector(`[name="${name}"]`); check(element, `Missing input ${name}`);
    Object.getOwnPropertyDescriptor(element.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value').set.call(element, value);
    element.dispatchEvent(new Event('input', { bubbles: true })); await pause();
  };
  document.querySelector('.tenant-trigger').click(); await pause();
  await click('创建租户'); await fill('租户名称', '项目责任范围验收'); await click('创建并进入');
  location.hash = '/projects'; await pause();
  await click('创建项目');
  await fill('项目名称', '当前租户项目'); await fill('目标与预期结果', '负责人必须属于当前租户');
  document.querySelector('[role=combobox][aria-label="Human 负责人"]').click(); await pause();
  const options = [...document.querySelectorAll('[role=option]')];
  check(!options.some(option => /周宁|许言/.test(option.textContent)), 'Project offers foreign-tenant owners');
  const self = options.find(option => option.textContent.includes('林予')); check(self, 'Current Human unavailable'); self.click(); await pause();
  await click('创建项目', document.querySelector('dialog[open]'));
  const state = JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
  const project = state.projects.find(item => item.name === '当前租户项目');
  check(project && project.ownerId === 'H-lin', 'Project lost stable Human identity');
  check(state.organization.members.some(person => person.id === project.ownerId && person.kind === 'Human'), 'Saved owner is not a tenant Human');
  check(!document.querySelector('dialog[open]'), 'Successful save was treated as dirty dismissal');
  check(location.hash.split("?")[0] === `#/p/${project.id}`, 'Created project detail did not open');
  check(document.documentElement.scrollWidth <= innerWidth + 1, 'Project page overflow');
  return { pass: true, checks: ['real tenant creation', 'tenant-scoped Human choices', 'stable owner identity', 'successful dismissal', 'project detail', 'viewport fit'] };
})()
