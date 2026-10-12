import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { fileURLToPath } from 'node:url';
import { runCommand } from '../lib/process.mjs';
import { readJSON, writeJSON, writeText, treeHashes } from '../lib/files.mjs';
import { checkChain } from '../lib/events.mjs';
import { screenshotConfig } from '../lib/qa.mjs';

const cli = fileURLToPath(new URL('../myr.mjs', import.meta.url));
async function fixture(t, overrides = {}, acceptanceFail = false) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'myr-test-'));
  const taskDir = path.join(root, 'input');
  await writeText(path.join(taskDir, 'requirement.md'), '# 迷你任务\nAC1: 输出 42\nAC2: 写测试\n');
  const task = { name: 'mini', requirement: 'requirement.md', acceptance: { dir: 'acceptance', command: ['node', '--test', 'acceptance/*.test.mjs'] },
    verify: [{ name: 'unit', command: ['node', '--test', 'tests/*.test.mjs'] }], maxAttempts: 2, maxWorkItems: 2, ...overrides };
  await writeJSON(path.join(taskDir, 'task.json'), task);
  await writeText(path.join(taskDir, task.acceptance.dir, 'client.test.mjs'),
    `import { test } from 'node:test';\nimport assert from 'node:assert/strict';\nimport { value } from '../src/value.mjs';\ntest('CLIENT_CASE', () => assert.equal(value, ${acceptanceFail ? 43 : 42}));\n`);
  const baseline = await treeHashes(taskDir);
  t.after(async () => {
    try { assert.deepEqual(await treeHashes(taskDir), baseline, '甲方输入必须保持原样'); }
    finally { await fs.rm(root, { recursive: true, force: true, maxRetries: 3 }); }
  });
  const dir = path.join(root, 'runs', 'case');
  const call = (...args) => runCommand(['node', cli, ...args], { cwd: root, timeoutMs: 30_000 });
  const run = (auto = true) => call('run', taskDir, '--executor', 'mock', '--run-id', 'case', ...(auto ? ['--auto-approve-if-verified'] : []));
  const state = () => readJSON(path.join(dir, 'state.json'));
  const events = async () => (await fs.readFile(path.join(dir, 'events.jsonl'), 'utf8')).trim().split('\n').map(JSON.parse);
  return { root, taskDir, dir, call, run, state, events };
}
function exit(result, code) { assert.equal(result.exitCode, code, result.stdout + '\n' + result.stderr); }

test('正常流水线：自动 L3、独立复验、甲方 L4 与完整事件链', async t => {
  const f = await fixture(t);
  exit(await f.run(), 0);
  const state = await f.state();
  assert.equal(state.status, 'AWAITING_CLIENT');
  assert.equal(state.workItems.length, 2);
  assert.ok(state.workItems.every(x => x.status === 'ACCEPTED' && x.authoritySource === 'POLICY:auto_verified'));
  assert.ok(state.workItems.every(x => /^[a-f0-9]{40}$/.test(x.attempts[0].commit)));
  const events = await f.events();
  assert.ok(events.some(e => e.type === 'DECISION_MADE' && e.actor.kind === 'POLICY' && e.data.authority_source === 'POLICY:auto_verified'));
  assert.ok(!events.some(e => e.type === 'CLIENT_ACCEPTED'));
  exit(await f.call('verify', f.dir), 0);
  exit(await f.call('accept', f.dir, '--by', 'client'), 0);
  assert.equal((await f.state()).status, 'COMPLETED');
  assert.ok((await f.events()).some(e => e.type === 'RUN_COMPLETED'));
  assert.equal((await checkChain(f.dir)).ok, true);
  assert.match(await fs.readFile(path.join(f.dir, 'report.md'), 'utf8'), /L4 甲方：已确认/);
});

test('验证失败带结构化反馈返工，第二次成功', async t => {
  const f = await fixture(t, { mock: { failFirst: true } });
  exit(await f.run(), 0);
  const item = (await f.state()).workItems[0];
  assert.equal(item.attempts.length, 2);
  assert.equal(item.attempts[0].verification[0].exitCode, 1);
  assert.equal(item.attempts[1].feedback.category, 'FUNCTIONAL_DEFECT');
  assert.match(item.attempts[1].feedback.detail, /value is 42/);
  const context = await readJSON(path.join(f.dir, 'W1.attempt2.context.json'));
  assert.match(context.prompt, /FUNCTIONAL_DEFECT/);
  assert.ok((await f.events()).some(e => e.type === 'REWORK_SCHEDULED'));
});

test('预算耗尽升级；禁止直接 approve，review reject 开新有界预算后 resume', async t => {
  const f = await fixture(t, { maxAttempts: 1, mock: { failFirst: true } });
  exit(await f.run(), 3);
  assert.equal((await f.state()).status, 'NEEDS_HUMAN');
  exit(await f.call('resume', f.dir), 3);
  exit(await f.call('review', f.dir, 'W1', 'approve'), 1);
  exit(await f.call('review', f.dir, 'W1', 'reject', '--category', 'FUNCTIONAL_DEFECT', '--detail', '修复 42', '--by', 'lead'), 0);
  exit(await f.call('resume', f.dir), 0);
  const state = await f.state();
  assert.equal(state.status, 'AWAITING_CLIENT');
  assert.equal(state.workItems[0].attempts.length, 2);
  assert.ok((await f.events()).some(e => e.type === 'DECISION_MADE' && e.data.authority_source === 'HUMAN:lead'));
});

test('默认每项暂停等待 review，approve + resume 后才运行验收', async t => {
  const f = await fixture(t);
  exit(await f.run(false), 2);
  assert.equal((await f.state()).workItems[0].status, 'VERIFIED');
  assert.equal((await f.state()).acceptance, undefined);
  exit(await f.call('review', f.dir, 'W1', 'approve', '--by', 'lead'), 0);
  exit(await f.call('resume', f.dir), 2);
  exit(await f.call('review', f.dir, 'W2', 'approve', '--by', 'lead'), 0);
  exit(await f.call('resume', f.dir), 0);
  assert.equal((await f.state()).status, 'AWAITING_CLIENT');
});

test('人工拒绝反馈注入下一次执行；reject 必須有 category/detail', async t => {
  const f = await fixture(t);
  exit(await f.run(false), 2);
  exit(await f.call('review', f.dir, 'W1', 'reject'), 1);
  exit(await f.call('review', f.dir, 'W1', 'reject', '--category', 'DESIGN', '--detail', '增加边界检查', '--by', 'lead'), 0);
  exit(await f.call('resume', f.dir), 2);
  const context = await readJSON(path.join(f.dir, 'W1.attempt2.context.json'));
  assert.deepEqual(context.feedback, { category: 'DESIGN', detail: '增加边界检查' });
});

test('甲方自动验收失败，报告列出失败用例，不能人工接受', async t => {
  const f = await fixture(t, {}, true);
  exit(await f.run(), 1);
  assert.equal((await f.state()).status, 'ACCEPTANCE_FAILED');
  assert.match(await fs.readFile(path.join(f.dir, 'report.md'), 'utf8'), /CLIENT_CASE/);
  exit(await f.call('accept', f.dir, '--by', 'client'), 1);
});

test('隐藏验收不会出现在执行工作区，仅验收副本持有原指纹', async t => {
  const f = await fixture(t);
  exit(await f.run(), 0);
  await assert.rejects(fs.access(path.join(f.dir, 'workspace', 'acceptance')), { code: 'ENOENT' });
  assert.deepEqual(await treeHashes(path.join(f.dir, 'acceptance-copy', 'acceptance')), await treeHashes(path.join(f.taskDir, 'acceptance')));
  await assert.rejects(fs.access(path.join(f.dir, 'workspace', 'claim.json')), { code: 'ENOENT' });
  const context = await readJSON(path.join(f.dir, 'W1.attempt1.context.json'));
  assert.ok(!context.prompt.includes('CLIENT_CASE'));
});

test('事件行被篡改时 verify 返回失败且拒绝追加', async t => {
  const f = await fixture(t);
  exit(await f.run(), 0);
  const file = path.join(f.dir, 'events.jsonl');
  const events = await f.events();
  events[1].data.method = 'tampered';
  const content = events.map(e => JSON.stringify(e)).join('\n') + '\n';
  await fs.writeFile(file, content, 'utf8');
  exit(await f.call('verify', f.dir), 1);
  assert.equal((await checkChain(f.dir)).ok, false);
  assert.equal(await fs.readFile(file, 'utf8'), content);
});

test('执行器故障独立重试一次；持续故障升级而非 FUNCTIONAL_DEFECT', async t => {
  const f = await fixture(t, { mock: { executorFailure: true } });
  exit(await f.run(), 3);
  const item = (await f.state()).workItems[0];
  assert.equal(item.attempts[0].invocations.length, 2);
  assert.equal(item.feedback.category, 'EXECUTION_FAILURE');
  assert.equal(item.attempts[0].verification, undefined);
});

test('执行器首次故障后重试成功，不消耗第二个质量尝试', async t => {
  const f = await fixture(t, { mock: { executorFailOnce: true } });
  exit(await f.run(), 0);
  const item = (await f.state()).workItems[0];
  assert.equal(item.attempts.length, 1);
  assert.equal(item.attempts[0].invocations.length, 2);
});

test('甲方人工拒绝保留 L4 未完成及人工来源', async t => {
  const f = await fixture(t);
  exit(await f.run(), 0);
  exit(await f.call('accept', f.dir, '--by', 'client', '--reject'), 1);
  exit(await f.call('accept', f.dir, '--by', 'client', '--reject', '--detail', '体验不符'), 0);
  assert.equal((await f.state()).status, 'CLIENT_REJECTED');
  const event = (await f.events()).find(e => e.type === 'CLIENT_REJECTED');
  assert.equal(event.data.authority_source, 'HUMAN:client');
});

test('验证后工作区变动阻止人工 approve，独立 verify 也检出', async t => {
  const f = await fixture(t);
  exit(await f.run(false), 2);
  await writeText(path.join(f.dir, 'workspace', 'src', 'value.mjs'), 'export const value = 0;\n');
  exit(await f.call('review', f.dir, 'W1', 'approve'), 1);
  exit(await f.call('verify', f.dir), 1);
});

test('任务验收指纹被更换时独立复验失败', async t => {
  const f = await fixture(t);
  exit(await f.run(), 0);
  const file = path.join(f.taskDir, 'acceptance', 'client.test.mjs');
  const original = await fs.readFile(file, 'utf8');
  try {
    await writeText(file, original + '// replaced\n');
    exit(await f.call('verify', f.dir), 1);
    assert.equal((await f.state()).independentVerification.acceptance.integrity, false);
  } finally { await fs.writeFile(file, original, 'utf8'); }
});

test('进程捕获完整流指纹、4KB 尾部、退出码与超时', async () => {
  const result = await runCommand(['node', '-e', "process.stdout.write('a'.repeat(9000)); process.stderr.write('b'.repeat(8000)); process.exitCode=7"]);
  assert.equal(result.exitCode, 7);
  assert.equal(Buffer.byteLength(result.stdout), 4096);
  assert.equal(Buffer.byteLength(result.stderr), 4096);
  assert.match(result.outputSha256, /^[a-f0-9]{64}$/);
  const timeout = await runCommand(['node', '-e', 'setInterval(()=>{}, 1000)'], { timeoutMs: 100 });
  assert.equal(timeout.timedOut, true);
});

test('acceptance.dir 自定义路径在独立副本中按配置运行', async t => {
  const f = await fixture(t, { acceptance: { dir: 'client-tests', command: ['node', '--test', 'client-tests/*.test.mjs'] } });
  exit(await f.run(), 0);
  await assert.rejects(fs.access(path.join(f.dir, 'workspace', 'client-tests')), { code: 'ENOENT' });
  assert.ok((await f.state()).acceptance.ok);
});

test('自报高置信度但没有有效测试更新，不能通过交付契约', async t => {
  const f = await fixture(t, { maxAttempts: 1, mock: { files: { 'tests/value.test.mjs': '' } } });
  // W1 creates the empty file; W2 has no test update despite reporting full confidence.
  exit(await f.run(), 3);
  const state = await f.state();
  assert.equal(state.workItems[1].status, 'NEEDS_HUMAN');
  assert.equal(state.workItems[1].attempts[0].claim.selfConfidence, 1);
  assert.ok(state.workItems[1].attempts[0].verification.some(x => x.name === 'delivery-contract' && x.exitCode === 1));
});

test('maxWorkItems 限制计划，runId 不得覆盖或越界', async t => {
  const f = await fixture(t, { maxWorkItems: 1 });
  exit(await f.run(), 3);
  assert.equal((await f.state()).workItems.length, 0);
  exit(await f.run(), 1);
  exit(await f.call('run', f.taskDir, '--executor', 'mock', '--run-id', '../escape'), 1);
});

for (const method of ['testAgent', 'acceptAgent']) {
  test(`${method} 首轮失败自动返工，内部通过后才裁决，不暂停人`, async t => {
    const f = await fixture(t, { mock: { [method]: { failFirst: true } } });
    exit(await f.run(), 0);
    const state = await f.state(), item = state.workItems[0], events = await f.events();
    assert.equal(item.attempts.length, 2);
    assert.equal(item.attempts[0].internalQA.failedStage, method === 'testAgent' ? 'AGENT_TEST' : 'AGENT_ACCEPTANCE');
    assert.equal(item.attempts[1].feedback.category, 'INTERNAL_QA_FAILURE');
    assert.match((await readJSON(path.join(f.dir, 'W1.attempt2.context.json'))).prompt, /mock QA failure/);
    assert.ok(!events.some(e => ['RUN_PAUSED', 'ESCALATED_TO_HUMAN'].includes(e.type)));
    for (const accepted of state.workItems) {
      const sequence = events.filter(e => e.workItemId === accepted.id);
      assert.ok(sequence.findLastIndex(e => e.type === 'INTERNAL_QA_PASSED') < sequence.findIndex(e => e.type === 'DECISION_MADE'));
      assert.equal(accepted.attempts.at(-1).internalQA.AGENT_ACCEPTANCE.ok, true);
    }
    const report = await fs.readFile(path.join(f.dir, 'report.md'), 'utf8');
    assert.match(report, /内部一次通过率：50.0%/);
    assert.match(report, /平均内部轮次：1.50/);
    assert.match(report, /内部拦截的问题数（未打扰人）：1/);
  });
}

test('内部轮次优先于旧预算，用尽才升级且 reason 固定', async t => {
  const f = await fixture(t, { maxAttempts: 1, maxInternalRounds: 2, mock: { acceptAgent: { alwaysFail: true } } });
  exit(await f.run(), 3);
  assert.equal((await f.state()).workItems[0].attempts.length, 2);
  const events = await f.events();
  assert.equal(events.filter(e => e.type === 'RUN_PAUSED').length, 1);
  assert.deepEqual(events.filter(e => e.type === 'ESCALATED_TO_HUMAN').map(e => e.data.reason), ['INTERNAL_ROUNDS_EXHAUSTED']);
  assert.equal(events.filter(e => e.type === 'INTERNAL_QA_FAILED').length, 2);
  assert.ok(!events.some(e => e.type === 'DECISION_MADE'));
});

test('非自动 L3 也只在内部返工全部通过后暂停一次', async t => {
  const f = await fixture(t, { mock: { testAgent: { failFirst: true } } });
  exit(await f.run(false), 2);
  assert.equal((await f.state()).workItems[0].attempts.length, 2);
  const events = await f.events();
  assert.equal(events.filter(e => e.type === 'RUN_PAUSED').length, 1);
  assert.ok(!events.some(e => e.type === 'ESCALATED_TO_HUMAN'));
  assert.ok(events.findIndex(e => e.type === 'INTERNAL_QA_PASSED') < events.findIndex(e => e.type === 'RUN_PAUSED'));
});

test('测试 Agent 越界改动恢复 tracked 文件并删除 untracked 文件，判失败', async t => {
  const f = await fixture(t, { maxInternalRounds: 1, mock: { testAgent: { scopeViolation: true } } });
  exit(await f.run(), 3);
  assert.equal(await fs.readFile(path.join(f.dir, 'workspace/src/value.mjs'), 'utf8'), 'export const value = 42;\n');
  await assert.rejects(fs.access(path.join(f.dir, 'workspace/outside.txt')), { code: 'ENOENT' });
  const item = (await f.state()).workItems[0];
  assert.equal(item.attempts[0].internalQA.AGENT_TEST.ok, false);
  assert.deepEqual(new Set(item.attempts[0].internalQA.AGENT_TEST.violations), new Set(['src/value.mjs', 'outside.txt']));
  assert.ok((await f.events()).some(e => e.type === 'QA_SCOPE_VIOLATION' && e.data.rolledBack));
});

test('新增 QA 测试即使原有命令未覆盖也必跑，失败不能进入验收 Agent', async t => {
  const f = await fixture(t, { maxInternalRounds: 1, mock: { testAgent: { files: {
    'tests/qa/boundary.test.mjs': "import { test } from 'node:test'; test('QA_BOUNDARY', () => { throw new Error('boundary failed'); });\n",
  } } } });
  exit(await f.run(), 3);
  const qa = (await f.state()).workItems[0].attempts[0].internalQA;
  assert.equal(qa.AGENT_TEST.verification.find(v => v.name === 'qa-tests').exitCode, 1);
  assert.equal(qa.AGENT_ACCEPTANCE, undefined);
});

test('通过的 QA 测试纳入最终 Git 证据，旧 review 和独立复验仍可用', async t => {
  const f = await fixture(t, { mock: { testAgent: { files: {
    'tests/qa/中文回归.test.mjs': "import { test } from 'node:test'; test('regression', () => {});\n",
  } } } });
  exit(await f.run(false), 2);
  const attempt = (await f.state()).workItems[0].attempts[0];
  assert.ok(attempt.filesChanged.includes('tests/qa/中文回归.test.mjs'));
  assert.equal(attempt.internalQA.AGENT_TEST.verification.find(v => v.name === 'qa-tests').exitCode, 0);
  exit(await f.call('review', f.dir, 'W1', 'approve', '--by', 'lead'), 0);
  exit(await f.call('resume', f.dir), 2);
  exit(await f.call('review', f.dir, 'W2', 'approve', '--by', 'lead'), 0);
  exit(await f.call('resume', f.dir), 0);
  exit(await f.call('verify', f.dir), 0);
});

test('截图失败记录 UNVERIFIED，仍调用验收 Agent；CLI 覆盖配置不改输入', async t => {
  const f = await fixture(t, { qa: { screenshot: { page: 'other.html', viewports: [[100, 100]] } } });
  const original = process.env.MYR_BROWSER;
  process.env.MYR_BROWSER = path.join(f.root, 'missing-browser.exe');
  try {
    exit(await f.call('run', f.taskDir, '--executor', 'mock', '--run-id', 'case', '--auto-approve-if-verified',
      '--qa-screenshot', 'src/value.mjs', '--qa-viewports', '900x900,420x800', '--acceptor-model', 'different-model'), 0);
  } finally { if (original === undefined) delete process.env.MYR_BROWSER; else process.env.MYR_BROWSER = original; }
  const state = await f.state();
  assert.deepEqual(state.qaScreenshot, { page: 'src/value.mjs', viewports: [[900, 900], [420, 800]] });
  assert.equal(state.acceptorModel, 'different-model');
  for (const item of state.workItems) {
    const qa = item.attempts[0].internalQA;
    assert.ok(qa.screenshots.unverified.some(e => e.includes('未能截图')));
    assert.ok(qa.AGENT_ACCEPTANCE.conclusion.criteria.some(c => c.verdict === 'UNVERIFIED'));
  }
  assert.match(await fs.readFile(path.join(f.dir, 'report.md'), 'utf8'), /未能截图/);
});

test('工作项不交付被截图页面时不截图，也不因此记 UNVERIFIED', async t => {
  const f = await fixture(t);
  const original = process.env.MYR_BROWSER;
  process.env.MYR_BROWSER = path.join(f.root, 'missing-browser.exe');
  try {
    exit(await f.call('run', f.taskDir, '--executor', 'mock', '--run-id', 'case', '--auto-approve-if-verified', '--qa-screenshot', 'index.html'), 0);
  } finally { if (original === undefined) delete process.env.MYR_BROWSER; else process.env.MYR_BROWSER = original; }
  for (const item of (await f.state()).workItems) {
    const qa = item.attempts[0].internalQA;
    assert.deepEqual([qa.screenshots.files, qa.screenshots.unverified, qa.screenshots.invocations], [[], [], []]);
    assert.ok(!qa.AGENT_ACCEPTANCE.conclusion.criteria.some(c => c.ref === 'SCREENSHOT'));
  }
});

test('缺省内部预算为五轮；截图参数校验拒绝越界和无效视口', async t => {
  const f = await fixture(t, { maxAttempts: undefined });
  exit(await f.run(), 0);
  assert.equal((await f.state()).task.maxInternalRounds, 5);
  assert.throws(() => screenshotConfig({}, { qaScreenshot: '../index.html' }), /越界/);
  for (const viewport of ['0x800', '900', '900xNaN', '900x800,']) {
    assert.throws(() => screenshotConfig({}, { qaScreenshot: 'index.html', qaViewports: viewport }));
  }
});

test('ignored 越界文件和 Git 元数据也会恢复，不能躲过范围检查', async t => {
  const f = await fixture(t, { maxInternalRounds: 1, mock: { files: { '.gitignore': 'ignored.txt\n' }, testAgent: { files: {
    'ignored.txt': 'forbidden\n', '.git/config': '[broken]\n',
  } } } });
  exit(await f.run(), 3);
  await assert.rejects(fs.access(path.join(f.dir, 'workspace/ignored.txt')), { code: 'ENOENT' });
  assert.match(await fs.readFile(path.join(f.dir, 'workspace/.git/config'), 'utf8'), /autocrlf = false/);
  assert.ok((await f.state()).workItems[0].attempts[0].internalQA.AGENT_TEST.violations.includes('.git/config'));
});

test('只读验收 Agent 越界写入也恢复并返工，不能凭 pass 声明穿透', async t => {
  const f = await fixture(t, { maxInternalRounds: 1, mock: { acceptAgent: { files: { 'src/value.mjs': 'export const value = 99;\n' } } } });
  exit(await f.run(), 3);
  assert.equal(await fs.readFile(path.join(f.dir, 'workspace/src/value.mjs'), 'utf8'), 'export const value = 42;\n');
  assert.equal((await f.state()).workItems[0].attempts[0].internalQA.failedStage, 'AGENT_ACCEPTANCE');
});

test('即使实现者忽略 QA 路径，通过的新测试仍进入最终 commit', async t => {
  const f = await fixture(t, { mock: { files: { '.gitignore': 'tests/qa/\n' }, testAgent: { files: {
    'tests/qa/ignored.test.mjs': "import { test } from 'node:test'; test('QA_IGNORED', () => {});\n",
  } } } });
  exit(await f.run(), 0);
  assert.ok((await f.state()).workItems[0].attempts[0].filesChanged.includes('tests/qa/ignored.test.mjs'));
  exit(await f.call('verify', f.dir), 0);
});

test('测试 Agent 持续调用故障作为内部失败，完整消耗预算后才升级', async t => {
  const f = await fixture(t, { maxInternalRounds: 2, mock: { testAgent: { executorFailure: true } } });
  exit(await f.run(), 3);
  const item = (await f.state()).workItems[0];
  assert.equal(item.attempts.length, 2);
  for (const attempt of item.attempts) {
    assert.equal(attempt.internalQA.failedStage, 'AGENT_TEST');
    assert.equal(attempt.internalQA.AGENT_TEST.invocations.length, 2);
  }
  assert.equal((await f.events()).filter(e => e.type === 'RUN_PAUSED').length, 1);
});

for (const conclusion of [
  { pass: true, criteria: [{ ref: 'AC1', verdict: 'FAIL', evidence: 'missing' }], uxFindings: [] },
  { pass: true, criteria: [], uxFindings: [{ severity: 'BLOCKER', title: 'unusable', evidence: 'cannot play', expected: 'playable' }] },
  { pass: true },
  { pass: true, criteria: [null], uxFindings: [] },
]) {
  test(`产品验收 fail/严重体验问题/无效契约均不可穿透：${JSON.stringify(conclusion)}`, async t => {
    const f = await fixture(t, { maxInternalRounds: 1, mock: { acceptAgent: { conclusion } } });
    exit(await f.run(), 3);
    assert.equal((await f.state()).workItems[0].attempts[0].internalQA.failedStage, 'AGENT_ACCEPTANCE');
  });
}

test('产品验收只按当前工作项的验收标准判定，其他工作项的 FAIL 不阻塞', async t => {
  const conclusion = { pass: true, criteria: [{ ref: 'AC99', verdict: 'FAIL', evidence: 'belongs to another item' }], uxFindings: [] };
  const f = await fixture(t, { maxInternalRounds: 1, mock: { acceptAgent: { conclusion } } });
  exit(await f.run(), 0);
  assert.ok((await f.state()).workItems.every(item => item.attempts[0].internalQA.AGENT_ACCEPTANCE.ok));
});
