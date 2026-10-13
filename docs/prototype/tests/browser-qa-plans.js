// Fresh isolated browser session; all mutations below use rendered controls.
(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 140));
  const check = (ok, message) => { if (!ok) throw Error(message); };
  const state = () => JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
  const go = async path => { location.hash = path; await wait(); };
  const click = async (text, root = document) => { const button = [...root.querySelectorAll('button')].find(button => button.textContent.trim() === text); check(button, `Missing button ${text}`); button.click(); await wait(); };
  const fill = async (label, value) => { const field = document.querySelector(`[name="${label}"]`); check(field, `Missing field ${label}`); Object.getOwnPropertyDescriptor(field.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value').set.call(field, value); field.dispatchEvent(new Event('input', { bubbles: true })); await wait(); };
  const choose = async (label, text) => { const field = document.querySelector(`[role=combobox][aria-label="${label}"]`); check(field, `Missing select ${label}`); field.click(); await wait(); const option = [...document.querySelectorAll('[role=option]')].find(item => item.textContent.trim() === text || item.textContent.trim().startsWith(text + ' ·')); check(option, `Missing option ${text}`); option.click(); await wait(); };
  const plan = () => state().resources.testplans.find(item => item.id === state().work.find(item => item.id === 'W-104').qaPlanId);
  const batch = () => plan().batches.find(item => item.id === plan().currentBatchId);
  const record = async (caseId, status = '通过', source = '平台独立验证', owner = 'Verifier') => {
    await click(`记录结果 · ${caseId}`); await choose('执行结论', status); await choose('验证来源', source); await choose('运行主体', owner);
    await fill('实际结果 / 跳过或阻塞原因', status === '失败' ? '导航仍遮挡按钮' : '逐条核对固定要求，记录本次实际结果');
    await fill('证据引用', `mock-browser/${batch().id}/${caseId}/${status}`); await fill('工具版本（手测填写不适用）', 'browser-mock/1');
    await fill('日志引用（选填）', 'mock-log/browser'); await click('保存逐项结果');
    check(!document.querySelector('dialog[open]'), 'Result did not save');
  };
  await go('/p/launch/work-items/W-104?tab=review'); await click('开始独立验证');
  check(location.hash.startsWith('#/testplans/'), 'Work verification did not navigate to fixed plan');
  check(batch().items.every(item => item.status === '未执行'), 'Starting batch silently passed cases');
  const originalPlanId = plan().id;
  const firstCase = batch().caseRefs[0].caseId;
  await record(firstCase, '失败');
  check(document.querySelector('[data-qa-counts]').textContent.includes('失败 1'), 'Failure count missing');
  check(document.querySelector('[data-qa-counts]').textContent.includes('未执行 1'), 'Not-executed count missing');
  await click('形成固定测试报告');
  check(location.hash.startsWith('#/testreports/'), 'Report lacks deep link');
  const reportId = location.hash.split('/').at(-1); const originalReport = JSON.stringify(state().resources.testreports.find(item => item.id === reportId));
  check(![...document.querySelectorAll('button')].some(button => button.textContent.trim() === '编辑'), 'Frozen report exposes edit');
  await go(`/tests/${firstCase}`); await click('编辑'); await fill('执行步骤', '改版：先检查导航，再核对键盘焦点'); await click('保存');
  await go(`/testplans/${originalPlanId}`); await click('开始新执行批次');
  check(document.body.innerText.includes('计划用例或工作已改版'), 'Stale case revision started');
  await click('修订测试计划'); await fill('变更影响与重验范围', '用例步骤改版，所有选中用例重新验证'); await click('固定计划修订'); await click('开始新执行批次');
  check(plan().revision === 2 && batch().items.every(item => item.status === '未执行'), 'Plan revision did not explicitly reset retest');
  for (const ref of batch().caseRefs) await record(ref.caseId);
  check(state().work.find(item => item.id === 'W-104').qa === false, 'Unfinished batch bypassed report gate');
  await click('形成固定测试报告'); check(state().work.find(item => item.id === 'W-104').qa === true, 'Fixed independent report did not pass'); await go(`/testplans/${originalPlanId}`); await click('开始新执行批次');
  await record(firstCase, '通过', 'Agent 自测', 'Atlas');
  check(state().work.find(item => item.id === 'W-104').qa === false, 'Agent self-test bypassed independent gate');
  await click('形成固定测试报告'); await go(`/testplans/${originalPlanId}`); await click('开始新执行批次');
  for (const ref of batch().caseRefs) await record(ref.caseId);
  await click('形成固定测试报告');
  check(JSON.stringify(state().resources.testreports.find(item => item.id === reportId)) === originalReport, 'Old failed report changed after revision');
  check(state().resources.testreports.length === 4, 'New batch did not create distinct report');
  check(document.documentElement.scrollWidth <= innerWidth + 1, 'QA report overflows viewport');
  const production = state().work.find(item => item.id === 'W-104').production;
  check(production?.version === 3 && production.executorIds.includes('agent:atlas'), 'Fixed producer provenance missing');
  check(JSON.stringify(batch().workContexts.find(item => item.workId === 'W-104').production) === JSON.stringify(production), 'Batch did not retain producer snapshot');
  await go('/p/launch/work-items/W-104?tab=review');
  await choose('完成与接收方式', '内部使用，无外部接收方');
  await fill('L4 不适用理由', '内部验证成果'); await click('确认接收契约'); await click('接受当前版本');
  check(state().work.find(item => item.id === 'W-104').status === '已完成', 'Internal acceptance did not complete work');
  const acceptedSnapshot = JSON.stringify(state());
  await go(`/testplans/${originalPlanId}`); await click('开始新执行批次');
  check(document.body.innerText.includes('当前版本已接受'), 'Accepted version reopened through existing plan');
  check(JSON.stringify(state()) === acceptedSnapshot, 'Rejected reopening mutated state or historical evidence');
  await click('修订测试计划'); await fill('变更影响与重验范围', '尝试改写已接受的版本'); await click('固定计划修订');
  check(document.body.innerText.includes('当前版本已接受'), 'Plan revision bypassed accepted-version protection');
  check(JSON.stringify(state()) === acceptedSnapshot, 'Rejected plan revision changed accepted evidence');


  return { pass: true, checks: ['check starts pending batch', 'failure/not-executed counts', 'immutable deep-linked report', 'case revision invalidates plan', 'explicit retest revision', 'independent source gate', 'history retained', 'viewport containment', 'fixed producer snapshot', 'accepted version cannot reopen QA', 'plan revision cannot invalidate acceptance'] };
})()
