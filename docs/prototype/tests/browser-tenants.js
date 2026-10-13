// Reset local demo state before running; repeat at desktop and 375px.
(async () => {
  const pause = () => new Promise(r => setTimeout(r, 160));
  const check = (condition, message) => { if (!condition) throw Error(message); };
  const state = () => JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
  const click = async (text, scope=document) => {
    const el = [...scope.querySelectorAll('button')].find(el => el.getClientRects().length && (el.textContent.trim() === text || el.getAttribute('aria-label') === text));
    check(el, `Missing ${text}`); el.click(); await pause();
  };
  const openInbox = async () => { document.querySelector('.tenant-trigger').click(); await pause(); await click('待处理邀请 · 1'); };
  await openInbox();
  check(document.querySelector('dialog').textContent.includes('合作空间'), 'Pending tenant missing');
  await click('接受并进入');
  check(state().activeTenant === 'partner' && state().tenants.length === 2, 'Acceptance did not join once');
  check(state().work.length === 0 && state().agents.length === 0 && state().organization.members.length === 1, 'Tenant data leaked');
  check(!document.querySelector('dialog'), 'Inbox did not close');
  check(document.activeElement === document.querySelector('.tenant-trigger'), 'Acceptance focus lost');
  document.querySelector('.tenant-trigger').click(); await pause();
  const studio = [...document.querySelectorAll('.tenant-popover button')].find(el => el.textContent.includes('Northstar Studio'));
  studio.click(); await pause();
  check(state().activeTenant === 'studio' && state().work.length > 0, 'Original tenant facts lost');
  document.querySelector('.tenant-trigger').click(); await pause(); await click('加入租户');
  const field = document.querySelector('dialog input');
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(field, 'DEMO-JOIN');
  field.dispatchEvent(new Event('input', {bubbles:true})); await pause();
  await click('验证邀请并加入');
  check(state().tenants.filter(t => t.id === 'partner').length === 1, 'Repeated join duplicated membership');
  check(document.documentElement.scrollWidth <= innerWidth + 1, 'Tenant UI overflow');
  return {pass:true, checks:['account invitation inbox','accept and focus','empty tenant isolation','restore tenant facts','idempotent join','responsive width']};
})();
