if (typeof window.qaCompleteValidation !== "function") throw Error("Preload tests/qa-browser-helpers.js before this suite.");
// Run on a fresh prototype in an isolated agent-browser session.
(async () => {
  const wait = () => new Promise((resolve) => setTimeout(resolve, 180));
  const check = (value, message) => { if (!value) throw Error(message); };
  const click = async (label, scope = document) => {
    const element = [...scope.querySelectorAll('button')].find((e) => e.textContent.trim() === label);
    check(element, `Missing button ${label}`); element.click(); await wait();
  };
  const fill = async (label, value) => {
    const field = [...document.querySelectorAll('.field')].find((e) => e.querySelector('label')?.textContent.replace(' *', '').trim() === label);
    const input = field?.querySelector('input:not([type=hidden]),textarea'); check(input, `Missing field ${label}`);
    Object.getOwnPropertyDescriptor(input.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value').set.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true })); await wait();
  };
  const state = () => JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
  location.hash = '/p/launch/work-items/W-103?tab=review'; await wait();
  await window.qaCompleteValidation('W-103');
  await click('接受当前版本'); await click('批准正式交付'); await click('确认', document.querySelector('dialog,[role=dialog]'));
  const delivered = state().resources.deliveries[0];
  check(delivered.items.length >= 2, 'No fixed item references');
  location.hash = `/deliveries/${delivered.id}`; await wait();
  check(![...document.querySelectorAll('button')].some((e) => e.textContent.trim() === '编辑'), 'Fixed package remained editable');
  await click('逐项部分接受');
  const select = document.querySelector('[role=combobox][aria-label="第 2 项回执"]'); check(select, 'Item receipt selector missing');
  select.click(); await wait(); await click('已拒收', document.querySelector('[role=listbox]'));
  await fill('第 2 项位置', '检查报告第一条'); await fill('第 2 项期望', '保留完整检查证据'); await fill('第 2 项实际', '定位信息不足');
  await fill('依据与说明', '接受成果，补充证据'); await click('确认操作', document.querySelector('dialog,[role=dialog]'));
  const receipt = state().resources.deliveries[0];
  check(receipt.status === '部分接受' && receipt.items[0].status === '已接受' && receipt.items[1].status === '已拒收', 'Item decisions were not retained');
  check(document.querySelector('main').textContent.includes('检查报告第一条'), 'Structured receipt not visible');
  for (const label of ['记录接收', '逐项部分接受', '拒收并退回']) { const button=[...document.querySelectorAll('main button')].find(button=>button.textContent.trim()===label); check(button?.disabled, 'Fixed receipt still offers rewriting: '+label); }
  check(document.querySelector('main').textContent.includes('此包已有固定回执，结果与拒收依据保留'), 'Fixed receipt explanation missing');
  location.hash = '/p/launch/work-items/W-104?tab=review'; await wait(); await click('退回修订');
  await fill('成果位置', '移动端导航'); await fill('期望结果', '不遮挡内容'); await fill('实际结果', '导航遮挡首屏'); await fill('理由与交接说明', '修复布局');
  await click('确认', document.querySelector('dialog,[role=dialog]'));
  const returned = state().work.find((w) => w.id === 'W-104').returnFeedback.at(-1);
  check(returned.version === 3 && returned.position === '移动端导航' && returned.structured, 'Work return lost its fixed context');
  location.hash = '/p/launch/work-items/W-104?tab=activity'; await wait();
  check(document.querySelector('main').textContent.includes('导航遮挡首屏'), 'Structured return not visible in history');
  return { pass: true, checks: ['fixed delivery item references', 'immutable package editor', 'partial item receipt', 'structured version-bound work return', 'visible feedback history'] };
})()
