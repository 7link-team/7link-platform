// Preseeded complete new-format member drafts; aggregate branches explicitly exercise imported legacy drafts.
(async () => {
  const pause = () => new Promise(resolve => setTimeout(resolve, 180));
  const check = (value, message) => { if (!value) throw Error(message); };
  const state = () => JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
  const button = (label, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent.trim() === label);
  const click = async (label, root = document) => { const item = button(label, root); check(item && !item.disabled, `Unavailable button ${label}`); item.click(); await pause(); };
  const fill = async (name, value) => {
    const input = document.querySelector(`dialog [name="${name}"]`); check(input, `Missing field ${name}`);
    Object.getOwnPropertyDescriptor(input.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value').set.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true })); await pause();
  };
  const choose = async (label, text) => {
    document.querySelector(`[role=combobox][aria-label="${label}"]`).click(); await pause();
    const option = [...document.querySelectorAll('[role=option]')].find(item => item.textContent.trim() === text); check(option, `Missing option ${text}`); option.click(); await pause();
  };
  const generate = async subject => {
    location.hash = '/reports'; await pause(); await click('生成 / 打开周期报告');
    if (subject) await choose('报告归属', subject);
    await choose('报告周期', '日报'); await fill('周期内日期', '2026-10-14'); await click('生成 / 打开', document.querySelector('dialog'));
    return location.hash.split('/').at(-1);
  };
  const act = async label => { await click(label); await fill('依据与说明', '核对固定正文与周期事实，保留未知说明'); await click('确认操作', document.querySelector('dialog')); };
  const ownId = await generate(); await act('本人确认并上报');
  check(state().resources.reports.find(item => item.id === ownId).status === '已提交', 'Own submission did not remain pending');
  for (const label of ['审核通过', '退回修改']) check(button(label)?.disabled && button(label).title.includes('本人'), 'Own review not blocked with explanation');
  check(document.querySelector('main').textContent.includes('固定提交'), 'Submitted content unavailable for review');
  const agentName = state().agents.find(agent => agent.id === 'atlas').name;
  const agentId = await generate(`${agentName} · Agent`);
  await click('编辑'); await fill('人工校准正文', 'Agent 周期运行说明 · 本地建议'); await click('保存', document.querySelector('dialog'));
  await act('模拟 Agent 上报');
  const submitted = structuredClone(state().resources.reports.find(item => item.id === agentId).submission);
  check(submitted.actorKind === 'Agent' && submitted.recordedBy === 'H-lin' && submitted.source.includes('模拟'), 'Agent simulation misattributed to a Human');
  check(document.querySelector('main').textContent.includes('Agent 周期运行说明'), 'Fixed submitted body not visible');
  await act('审核通过');
  let agentReport = state().resources.reports.find(item => item.id === agentId);
  check(agentReport.status === '已接受' && agentReport.approval.actorKind === 'Human', 'Agent report did not receive Human confirmation');
  check(JSON.stringify(agentReport.approval.snapshot) === JSON.stringify(submitted), 'Approval lost fixed Agent submission');
  check(button('编辑')?.disabled && button('编辑').title.includes('撤销确认'), 'Accepted report remains editable');
  await act('撤销确认');
  agentReport = state().resources.reports.find(item => item.id === agentId);
  check(agentReport.status === '草稿' && agentReport.history.at(-1).action === 'REOPEN' && agentReport.history.at(-1).actorId === 'H-lin', 'Explicit REOPEN audit missing');
  check(JSON.stringify(agentReport.history.at(-1).snapshot.approval.snapshot) === JSON.stringify(submitted), 'REOPEN lost fixed approval');
  await click('编辑'); await fill('人工校准正文', '修订后的 Agent 建议'); await click('保存', document.querySelector('dialog'));
  agentReport = state().resources.reports.find(item => item.id === agentId);
  check(agentReport.status === '草稿' && !agentReport.approval && agentReport.history.some(item => item.action === 'REOPEN' && item.snapshot?.approval?.snapshot?.content?.summary === 'Agent 周期运行说明 · 本地建议'), 'Revision lost REOPEN approval snapshot or retained current approval');
  const tenantName = state().tenants.find(item => item.id === state().activeTenant).name;
  const teamId = await generate(`${tenantName} · 租户`); await act('本人确认并上报');
  const team = state().resources.reports.find(item => item.id === teamId);
  check(team.status === '已接受' && team.approval.actorId === 'SYSTEM:team_top', 'TEAM documented system confirmation missing');
  const node = state().organization.nodes.find(item => item.id === 'ORG-engineering');
  const nodeId = await generate(`${node.name} · 团队`); await act('本人确认并上报');
  const nodeReport = state().resources.reports.find(item => item.id === nodeId);
  check(nodeReport.status === '已提交' && !nodeReport.approval, 'Organization node incorrectly used tenant system confirmation');
  check(button('审核通过')?.disabled && button('审核通过').title.includes('本人'), 'Organization node allowed self-review');
  check(!document.querySelector('main').textContent.includes('[object Object]'), 'Raw metadata leaked into report UI');
  check(document.documentElement.scrollWidth <= innerWidth + 1, 'Report authority layout overflows');
  return { pass: true, checks: ['Human submission separate from review', 'self-review disabled with reason', 'fixed content visible', 'explicit Agent simulation', 'Human confirmation', 'historical approval retained on revision', 'TEAM system exception', 'organization node exception denied', 'responsive layout'] };
})()
