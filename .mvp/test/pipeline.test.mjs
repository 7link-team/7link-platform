import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { fileURLToPath } from 'node:url';
import { runCommand } from '../lib/process.mjs';
import { readJSON, writeJSON, writeText, treeHashes } from '../lib/files.mjs';
import { checkChain } from '../lib/events.mjs';

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
