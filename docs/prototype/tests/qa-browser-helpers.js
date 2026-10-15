// Preload this helper with agent-browser eval --stdin before browser regression scripts.
window.qaCompleteValidation = async function(workId) {
  const wait = () => new Promise(resolve => setTimeout(resolve, 120));
  const click = async text => { const button = [...document.querySelectorAll('button')].find(button => button.textContent.trim() === text); if (!button) throw Error('Missing QA button ' + text); button.click(); await wait(); };
  const fill = async (label, value) => { const input = document.querySelector(`[name="${label}"]`); if (!input) throw Error('Missing QA field ' + label); Object.getOwnPropertyDescriptor(input.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value').set.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })); await wait(); };
  const select = async (label, text) => { document.querySelector(`[role=combobox][aria-label="${label}"]`).click(); await wait(); const option = [...document.querySelectorAll('[role=option]')].find(item => item.textContent.trim() === text || item.textContent.trim().startsWith(text + ' ·')); if (!option) throw Error('Missing QA option ' + text); option.click(); await wait(); };
  const state = () => JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
  const work = state()?.work.find(work => work.id === workId) || { project: workId === 'W-201' ? 'film' : workId === 'W-301' ? 'novel' : 'launch' };
  const href = `/p/${work.project}/work-items/${workId}?tab=review`;
  location.hash = href; await wait(); await click('开始独立验证');
  if (!location.hash.startsWith('#/testplans/')) throw Error('Verification did not open fixed plan');
  const plan = state().resources.testplans.find(plan => plan.id === state().work.find(work => work.id === workId).qaPlanId);
  let batch = plan.batches.find(batch => batch.id === plan.currentBatchId);
  if (batch.items.some(item => item.runId)) {
    await click('形成固定测试报告');document.querySelector('dialog[open] [aria-label="关闭弹窗"]')?.click();await wait(); location.hash = `/testplans/${plan.id}`; await wait(); await click('开始新执行批次');
    const latest = state().resources.testplans.find(item => item.id === plan.id); batch = latest.batches.find(batch => batch.id === latest.currentBatchId);
  }
  for (const ref of batch.caseRefs) {
    await click(`记录结果 · ${ref.caseId}`); await select('执行结论', '通过'); await select('验证来源', '平台独立验证'); await select('运行主体', 'Verifier');
    await fill('实际结果 / 跳过或阻塞原因', `逐项核对：${ref.snapshot.expected}`); await fill('证据引用', `mock-browser/${batch.id}/${ref.caseId}`); await fill('工具版本（手测填写不适用）', 'browser-mock/1');
    await click('保存逐项结果'); if (document.querySelector('dialog[open]')) throw Error('QA result did not save');
  }
  await click('形成固定测试报告');document.querySelector('dialog[open] [aria-label="关闭弹窗"]')?.click();await wait();
  location.hash = href; await wait();
};
window.qaConfigureRework = async function(workId) {
  const wait = () => new Promise(resolve => setTimeout(resolve, 100));
  const state = JSON.parse(localStorage.getItem('collaboration-prototype-v1')); const work = state.work.find(work => work.id === workId);
  location.hash = `/p/${work.project}/work-items/${workId}?tab=review`; await wait();
  const summary = [...document.querySelectorAll('summary')].find(summary => summary.textContent === '配置本工作返工上限'); if (!summary) throw Error('Missing rework configuration'); summary.click(); await wait();
  for (const [label, value] of [['内部修复上限', '5'], ['正式返工升级阈值', '3'], ['返工处理依据', '浏览器验证显式设置本专业返工阈值']]) {
    const input = document.querySelector(`[name="${label}"]`); if (!input) throw Error('Missing ' + label); Object.getOwnPropertyDescriptor(input.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value').set.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })); await wait();
  }
  const button = [...document.querySelectorAll('button')].find(button => button.textContent.trim() === '保存返工上限'); button.click(); await wait();
};
