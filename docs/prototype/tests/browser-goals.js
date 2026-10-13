// Run in a dedicated session at desktop and 375 px; uses only UI writes.
(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 180));
  const check = (value, message) => { if (!value) throw Error(message); };
  const state = () => JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
  const goal = () => state().resources.goals.find(item => item.id === 'G-01');
  const go = async path => { location.hash = path; await wait(); };
  const click = async (label, scope = document) => {
    const button = [...scope.querySelectorAll('button')].find(item => item.textContent.trim() === label || item.getAttribute('aria-label') === label);
    check(button, `Missing ${label}`); button.click(); await wait();
  };
  const fill = async (label, value) => {
    const input = document.querySelector(`dialog [name="${label}"]`);
    check(input, `Missing input ${label}`);
    const proto = input.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true })); await wait();
  };
  if (document.querySelector('dialog')) await click('关闭弹窗');
  await go('/goals/G-01');
  await click('编辑');
  const actual = document.querySelector('dialog [name="实际结果"]').value;
  await fill('实际结果', actual === '5 天' ? '6.2 天' : '5 天');
  await click('保存', document.querySelector('dialog'));
  check(document.querySelector('main').textContent.includes('进度待校准'), 'Changed metric retained displayed progress');
  check(!document.querySelector('main [role="progressbar"]'), 'Stale progress bar remains');
  check(goal().progress === null && !goal().progressCalibration, 'Old progress not invalidated');
  await go('/goals');
  check(document.querySelector('main').textContent.includes('进度待校准'), 'List did not show calibration required');
  await go('/goals/G-01');
  const before = structuredClone(goal()), works = JSON.stringify(state().work);
  await click('手工校准进度');
  await fill('手工进度（%）', '100');
  await click('保存校准', document.querySelector('dialog'));
  check(document.querySelector('dialog') && goal().revision === before.revision, 'Blank explanation accepted');
  await fill('校准依据', '浏览器验收：逐项核对当前指标证据后人工校准');
  await click('保存校准', document.querySelector('dialog'));
  check(!document.querySelector('dialog'), 'Calibration modal did not close');
  check(goal().progressCalibration.metricRevision === goal().metricRevision && goal().progress === 100, 'Calibration not bound');
  check(goal().status === before.status && JSON.stringify(state().work) === works, 'Calibration altered lifecycle');
  check(document.querySelector('main').textContent.includes('手工进度 100%') && document.querySelector('main').textContent.includes('浏览器验收：'), 'Bound progress evidence not shown');
  await click('编辑');
  await fill('名称', `浏览器验收目标 ${innerWidth}`);
  await click('保存', document.querySelector('dialog'));
  check(goal().progress === 100 && goal().metricRevision === before.metricRevision, 'Name edit invalidated calibration');
  await click('编辑');
  await fill('基线', `${document.querySelector('dialog [name="基线"]').value} 修订`);
  await click('保存', document.querySelector('dialog'));
  check(goal().progress === null && goal().history.some(item => item.progressCalibration?.progress === 100), 'Metric revision lost audit or retained progress');
  check(document.querySelector('main').textContent.includes('进度待校准'), 'Second metric change not reflected');
  const checks = ['metric edits invalidate progress in detail/list', 'required manual evidence bound to revision', 'calibration preserves goal/work status', 'nonmetric edits preserve progress', 'old calibration retained in history'];
  for (const [id, label] of [['K-01', '禁用资料'], ['K-02', '禁用记忆']]) {
    await go(`/knowledge/${id}`);
    check([...document.querySelectorAll('button')].some(item => item.textContent.trim() === label), `${id} wrong action label`);
    check(document.documentElement.scrollWidth <= innerWidth + 1, `${id} overflow`);
  }
  await go('/goals/G-01');
  await click('手工校准进度');
  check(document.documentElement.scrollWidth <= innerWidth + 1, 'Calibration dialog overflow');
  await click('关闭弹窗');
  check(document.activeElement.textContent.includes('手工校准进度'), 'Modal focus not restored');
  const button = label => [...document.querySelectorAll('button')].find(item => item.textContent.trim() === label);
  check(button('批准目标').disabled, 'Draft exposes direct approval');
  const decide = async label => { await click(label); await fill('依据与说明', '审核当前固定修订的要求、指标与责任范围'); await click('确认操作', document.querySelector('dialog')); check(!document.querySelector('dialog'), label + ' failed to close'); };
  await decide('提交审核');
  const submitted=structuredClone(goal().submission);
  check(goal().status==='待审核' && submitted.revision===goal().revision, 'Submission did not pin revision');
  check(button('提交审核').disabled && !button('批准目标').disabled, 'Review actions do not match state');
  await decide('退回修改');check(goal().status==='草稿' && !goal().submission, 'Return did not clear active submission');
  await decide('提交审核');await decide('批准目标');
  check(goal().status==='进行中' && goal().approval.snapshot.name===goal().name, 'Approval missing fixed content');
  check(document.querySelector('main').textContent.includes('已批准修订'), 'Approval evidence is not shown');
  await click('编辑');await fill('目标值','新的目标版本');await click('保存',document.querySelector('dialog'));
  check(goal().status==='草稿' && !goal().approval && button('批准目标').disabled,'Definition edit kept old approval');
  check(goal().history.some(entry=>entry.approval), 'Previous approval history lost');
  checks.push('kind-specific knowledge actions', 'responsive layout and modal focus','submit-return-resubmit-approve fixed revision','edit invalidates approval and preserves history');
  return { result: 'PASS', width: innerWidth, checks };
})()
