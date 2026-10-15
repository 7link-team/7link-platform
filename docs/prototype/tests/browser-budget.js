// Isolated owner session with seed launch project. All fixture tasks and drafts
// are created through the real UI; no store command or export handler is mocked.
(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 90));
  const check = (value, message) => { if (!value) throw Error(message); };
  const until = async (predicate, message) => {
    const end = Date.now() + 10000;
    while (!predicate()) { if (Date.now() > end) throw Error(message); await wait(); }
  };
  const state = () => JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
  const params = () => new URLSearchParams(location.hash.split('?')[1] || '');
  const path = () => location.hash.split('?')[0];
  const modal = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  const list = () => document.querySelector('main .resource-data-list[aria-label="任务列表"]');
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(element => element.textContent.trim() === text);
  const click = async (text, root = document) => {
    const target = button(text, root); check(target && !target.disabled, `Missing/enabled button: ${text}`);
    target.click(); await wait();
  };
  const fill = async (label, value, root = document) => {
    const input = root.querySelector(`[name="${CSS.escape(label)}"]`); check(input, `Missing field: ${label}`);
    const proto = input.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(input, value);
    input.dispatchEvent(new Event('input', {bubbles:true})); await wait();
  };
  const choose = async (label, text, root = document) => {
    const combo = root.querySelector(`[role="combobox"][aria-label="${CSS.escape(label)}"]`);
    check(combo && !combo.disabled, `Missing/enabled choice: ${label}`); combo.click(); await wait();
    const option = [...document.querySelectorAll('[role="option"]')].find(element => element.textContent.trim() === text);
    check(option, `Missing option: ${label}/${text}`); option.click(); await wait();
  };

  location.hash='/cost?scope=launch';await until(()=>document.querySelector('main h1')?.textContent==='用量与预算','Cost page absent');
  const checks=[];
  await click('预算规则');check(params().get('state.cost.tab')==='"policies"','Budget tab has no URL position');
  await click('新增预算规则');await fill('费用上限（USD）','3',modal());await fill('最大并发调用数','1',modal());await fill('创建或修订理由','确认本地有限预算',modal());await click('保存预算规则',modal());await until(()=>!modal(),'Policy did not close');
  check(state().accounting.policies.length===1,'Policy not persisted');checks.push('explicit budget policy save');
  await click('调用明细');await click('模拟预留调用');await fill('唯一调用编号','budget-ui-one',modal());await fill('用途（含失败尝试或候选生成）','验证失败费用计入',modal());await fill('最大预留费用（USD）','2',modal());await click('模拟预留调用',modal());await until(()=>!modal(),'Reservation did not close');
  check(state().accounting.calls.length===1&&state().accounting.calls[0].state==='RESERVED','Reservation missing');
  await click('模拟预留调用');await fill('唯一调用编号','budget-ui-two',modal());await fill('用途（含失败尝试或候选生成）','验证超限不产生新调用',modal());await fill('最大预留费用（USD）','2',modal());await click('模拟预留调用',modal());check(modal()&&state().accounting.calls.length===1,'Hard limit created over-budget call');checks.push('hard budget/concurrency reject atomically');
  // Discard the rejected proposal via the actual unsaved guard.
  modal().querySelector('button[aria-label="关闭弹窗"]').click();await wait();await click('放弃修改');await until(()=>!modal(),'Guard did not discard');
  await click('模拟结束调用');await choose('调用结果','失败（费用仍计入）',modal());await click('记录结束回执',modal());await until(()=>!modal(),'Close receipt absent');
  const c=state().accounting.calls[0];check(c.state==='PENDING'&&c.actual===null&&c.outcome==='FAILED','Unknown failed call became zero cost');checks.push('unknown charge held after failure');
  await click('追加对账');await fill('供应商实际费用（USD）','4',modal());await fill('对账依据与差异理由','供应商晚到用量，保留超额尾差',modal());await click('追加对账',modal());await until(()=>!modal(),'Reconcile did not close');
  const fixed=state().accounting.calls[0];check(fixed.actual===null&&fixed.corrections[0].actual===4&&fixed.corrections[0].delta===2,'Late reconciliation overwrote original or lost difference');checks.push('append-only late correction');
  await click('详情');check(params().get('state.cost.dialog')?.includes(fixed.id),'Detail URL lost exact record');check(modal().textContent.includes('供应商晚到用量'),'Detail has no reconciliation evidence');
  modal().querySelector('button[aria-label="关闭弹窗"]').click();await until(()=>!modal(),'Detail not closed');
  check(document.documentElement.scrollWidth<=innerWidth+2,'Cost page overflows viewport');
  return {pass:true,width:innerWidth,checks};
})()
