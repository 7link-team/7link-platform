import * as fs from 'node:fs/promises';
import path from 'node:path';
import { inside, readJSON, writeText, saveState, treeHashes, copyTree } from './files.mjs';
import { runCommand, passed } from './process.mjs';
import { emit, checkChain } from './events.mjs';
import { report } from './report.mjs';

const workspaceOf = dir => path.join(dir, 'workspace');
const executorOf = name => import(`./executors/${name}.mjs`);
async function git(dir, args) {
  const result = await runCommand(['git', ...args], { cwd: workspaceOf(dir) });
  if (!passed(result)) throw new Error(`git ${args[0]} 失败: ${result.stderr || result.error}`);
  return result.stdout.trim();
}
async function commit(dir, message) {
  await git(dir, ['add', '-A']);
  await git(dir, ['-c', 'user.name=myr', '-c', 'user.email=myr@localhost', 'commit', '--allow-empty', '-m', message]);
  return git(dir, ['rev-parse', 'HEAD']);
}
async function persist(dir, state) {
  await saveState(dir, state);
  await report(dir, state);
}
async function pause(dir, state, status, item, reason) {
  state.status = status;
  if (status === 'NEEDS_HUMAN') await emit(dir, 'ESCALATED_TO_HUMAN', { reason }, item?.id);
  await emit(dir, 'RUN_PAUSED', { status, reason }, item?.id);
  await persist(dir, state);
  return status === 'NEEDS_HUMAN_REVIEW' ? 2 : 3;
}
function validateTask(task, taskDir) {
  if (!task.name || !Number.isInteger(task.maxAttempts) || task.maxAttempts < 1 || !Number.isInteger(task.maxWorkItems) || task.maxWorkItems < 1) throw new Error('任务名称/尝试上限/工作项上限无效');
  inside(taskDir, task.requirement);
  inside(taskDir, task.acceptance?.dir);
  if (!Array.isArray(task.verify) || !task.verify.length) throw new Error('至少需要一条 verify 命令');
  for (const command of [task.acceptance.command, ...task.verify.map(v => v.command)]) {
    if (!Array.isArray(command) || !command.length || command.some(x => typeof x !== 'string' || !x)) throw new Error('command 必须是非空字符串数组');
  }
}
function validatePlan(plan, task, workspace) {
  if (!Array.isArray(plan?.workItems) || !plan.workItems.length || plan.workItems.length > task.maxWorkItems) throw new Error('计划工作项数量无效');
  const ids = new Set();
  for (const item of plan.workItems) {
    if (!/^W[1-9]\d*$/.test(item.id) || ids.has(item.id) || typeof item.title !== 'string' || !item.title || typeof item.description !== 'string' || !Array.isArray(item.acceptanceRefs) || item.acceptanceRefs.some(x => typeof x !== 'string') || !Array.isArray(item.files)) throw new Error('计划工作项格式无效');
    ids.add(item.id);
    for (const file of item.files) {
      inside(workspace, file);
      const normalized = file.replace(/\\/g, '/');
      const acceptanceDir = task.acceptance.dir.replace(/\\/g, '/');
      if (/^(acceptance|\.git)(\/|$)/.test(normalized) || normalized === acceptanceDir || normalized.startsWith(acceptanceDir + '/')) throw new Error('计划包含禁止路径');
    }
  }
}
async function invoke(dir, state, method, context, item, attempt) {
  const executor = await executorOf(state.executor);
  const invocations = attempt ? attempt.invocations : (state.planInvocations ??= []);
  // One infrastructure retry per planning/implementation attempt, distinct from quality rework.
  for (let call = 1; call <= 2; call++) {
    let result;
    try { result = await executor[method]({ ...context, call }); }
    catch (err) { result = { invocation: { exitCode: null, error: err.message, durationMs: 0, outputFile: null } }; }
    invocations.push(result.invocation);
    await emit(dir, 'EXECUTOR_COMPLETED', { method, call, ...result.invocation }, item?.id, { kind: 'AGENT', id: state.executor });
    await saveState(dir, state);
    if (passed(result.invocation)) return result;
  }
  return null;
}
export async function start(taskDir, options = {}) {
  taskDir = path.resolve(taskDir);
  const task = await readJSON(path.join(taskDir, 'task.json'));
  validateTask(task, taskDir);
  const runId = options.runId ?? new Date().toISOString().replace(/[:.]/g, '-');
  if (!/^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(runId)) throw new Error('runId 只能包含字母、数字、下划线、短横线');
  const dir = path.resolve('runs', runId);
  const taskRelative = path.relative(taskDir, dir);
  if (!taskRelative.startsWith('..') && !path.isAbsolute(taskRelative)) throw new Error('run 目录不得位于任务输入中');
  const executor = options.executor ?? 'codex';
  if (!['codex', 'mock'].includes(executor)) throw new Error('executor 必须是 codex 或 mock');
  const requirement = await fs.readFile(inside(taskDir, task.requirement), 'utf8');
  const acceptanceHashes = await treeHashes(inside(taskDir, task.acceptance.dir));
  if (!Object.keys(acceptanceHashes).length) throw new Error('验收目录不能为空');
  await fs.mkdir(path.dirname(dir), { recursive: true });
  await fs.mkdir(dir); // Exclusive: never overwrite an existing run.
  const lock = path.join(dir, '.myr.lock');
  await fs.mkdir(lock);
  try {
    await fs.mkdir(workspaceOf(dir));
    const state = { version: 1, runId, taskDir, task, executor, autoApprove: !!options.autoApprove,
      startedAt: new Date().toISOString(), status: 'PLANNING', requirement, acceptanceHashes, workItems: [] };
    await emit(dir, 'RUN_STARTED', { task, taskDir, executor, autoApprove: state.autoApprove, acceptanceHashes });
    await writeText(path.join(workspaceOf(dir), 'requirement.md'), requirement);
    await git(dir, ['init']);
    await git(dir, ['config', 'core.autocrlf', 'false']);
    state.initialCommit = await commit(dir, 'Initial requirement');
    await persist(dir, state);
    return { dir, code: await continueRun(dir, state) };
  } finally { await fs.rm(lock, { recursive: true, force: true }); }
}
async function verifyCommands(dir, state) {
  const results = [];
  for (const spec of state.task.verify) results.push({ name: spec.name, ...await runCommand(spec.command, { cwd: workspaceOf(dir) }) });
  return results;
}
function feedbackFor(results) {
  return { category: 'FUNCTIONAL_DEFECT', detail: results.filter(x => !passed(x)).map(x => `${x.name}: exit=${x.exitCode}, timeout=${x.timedOut}\n${x.stdout ?? ''}\n${x.stderr ?? ''}\n${x.error ?? ''}`).join('\n'),
    failures: results.filter(x => !passed(x)) };
}
async function readClaim(dir, item) {
  const file = path.join(workspaceOf(dir), 'claim.json');
  try {
    const claim = await readJSON(file);
    if (claim.workItemId !== item.id || typeof claim.summary !== 'string' || !Array.isArray(claim.filesChanged) || !Number.isFinite(claim.selfConfidence) || claim.selfConfidence < 0 || claim.selfConfidence > 1) throw new Error('claim.json 格式无效');
    for (const changed of claim.filesChanged) inside(workspaceOf(dir), changed);
    return { claim };
  } catch (err) { return { claimError: err.message }; }
  finally { await fs.rm(file, { force: true }); }
}
async function approveItem(dir, state, item, source, actor) {
  item.status = 'ACCEPTED';
  item.authoritySource = source;
  item.acceptedCommit = item.attempts.at(-1).commit;
  await emit(dir, 'DECISION_MADE', { decision: 'approve', authority_source: source, commit: item.acceptedCommit }, item.id, actor);
  state.status = 'RUNNING';
  await persist(dir, state);
}
export async function continueRun(dir, state) {
  if (state.status === 'NEEDS_HUMAN_REVIEW') return 2;
  if (state.status === 'NEEDS_HUMAN') return 3;
  if (['AWAITING_CLIENT', 'COMPLETED', 'CLIENT_REJECTED', 'ACCEPTANCE_FAILED'].includes(state.status)) return state.status.endsWith('FAILED') ? 1 : 0;
  if (!state.workItems.length) {
    const prompt = `你是乙方主管。仅规划，不修改文件。返回计划 JSON；最多 ${state.task.maxWorkItems} 个顺序工作项，id 为 W1 等，每项包含 title、description、acceptanceRefs、files。不要访问隐藏验收。需求全文：\n${state.requirement}`;
    const result = await invoke(dir, state, 'plan', { runDir: dir, workspace: workspaceOf(dir), task: state.task, prompt });
    if (!result) return pause(dir, state, 'NEEDS_HUMAN', null, '规划执行故障，已重试一次');
    try { validatePlan(result.plan, state.task, workspaceOf(dir)); }
    catch (err) { return pause(dir, state, 'NEEDS_HUMAN', null, err.message); }
    state.workItems = result.plan.workItems.map(item => ({ ...item, status: 'PENDING', attempts: [], attemptLimit: state.task.maxAttempts }));
    await emit(dir, 'PLAN_CREATED', result.plan, undefined, { kind: 'AGENT', id: state.executor });
    state.status = 'RUNNING';
    await persist(dir, state);
  }
  for (const item of state.workItems) {
    if (item.status === 'ACCEPTED') continue;
    while (item.status !== 'VERIFIED' && item.attempts.length < item.attemptLimit) {
      const attempt = { number: item.attempts.length + 1, feedback: item.feedback ?? null, invocations: [] };
      item.attempts.push(attempt);
      item.status = 'EXECUTING';
      state.status = 'RUNNING';
      await fs.rm(path.join(workspaceOf(dir), 'claim.json'), { force: true });
      await emit(dir, 'ATTEMPT_STARTED', { attempt: attempt.number, feedback: attempt.feedback }, item.id);
      await persist(dir, state);
      const completed = state.workItems.filter(x => x.status === 'ACCEPTED').map(x => ({ id: x.id, summary: x.attempts.at(-1)?.claim?.summary }));
      const prompt = `你是乙方执行 Agent。仅在当前工作区内修改文件；不要访问外部任务或隐藏验收。必须为自己的改动写/更新 tests/*.test.mjs。不得修改 requirement.md、.git 或创建 acceptance/。完成后在工作区根写 claim.json，格式 {workItemId,summary,filesChanged:[相对路径],selfConfidence:0..1}。声明不能代替验证。\n需求全文：\n${state.requirement}\n当前工作项：${JSON.stringify(item)}\n已完成工作项摘要：${JSON.stringify(completed)}\n上次失败结构化反馈：${JSON.stringify(attempt.feedback)}`;
      const result = await invoke(dir, state, 'execute', { runDir: dir, workspace: workspaceOf(dir), task: state.task,
        workItem: item, attempt: attempt.number, feedback: attempt.feedback, completed, prompt }, item, attempt);
      if (!result) {
        item.status = 'NEEDS_HUMAN';
        item.feedback = { category: 'EXECUTION_FAILURE', detail: JSON.stringify(attempt.invocations) };
        return pause(dir, state, 'NEEDS_HUMAN', item, '执行故障，已重试一次（非质量问题）');
      }
      Object.assign(attempt, await readClaim(dir, item));
      await emit(dir, 'CLAIM_SUBMITTED', { claim: attempt.claim ?? null, error: attempt.claimError ?? null }, item.id, { kind: 'AGENT', id: state.executor });
      // Evidence comes from Git, never from filesChanged in the claim.
      const previous = await git(dir, ['rev-parse', 'HEAD']);
      attempt.commit = await commit(dir, `${item.id} attempt ${attempt.number}`);
      attempt.filesChanged = (await git(dir, ['diff', '--name-only', previous, attempt.commit])).split('\n').filter(Boolean);
      await emit(dir, 'EVIDENCE_COMMITTED', { attempt: attempt.number, commit: attempt.commit, filesChanged: attempt.filesChanged }, item.id);
      attempt.verification = await verifyCommands(dir, state);
      const hasTests = attempt.filesChanged.some(file => /^tests\/[^/]+\.test\.mjs$/.test(file));
      if (!attempt.claim || !hasTests) attempt.verification.push({ name: 'delivery-contract', exitCode: 1, error: attempt.claimError ?? '本次提交没有写/更新 tests/*.test.mjs' });
      const unchanged = await fs.readFile(path.join(workspaceOf(dir), 'requirement.md'), 'utf8').then(x => x === state.requirement.replace(/\r\n?/g, '\n')).catch(() => false);
      let acceptanceAbsent = true;
      for (const relative of new Set(['acceptance', state.task.acceptance.dir])) {
        acceptanceAbsent &&= await fs.lstat(inside(workspaceOf(dir), relative)).then(() => false, err => err.code === 'ENOENT');
      }
      if (!unchanged || !acceptanceAbsent) attempt.verification.push({ name: 'workspace-contract', exitCode: 1, error: '需求被修改或工作区出现 acceptance/' });
      if (await git(dir, ['status', '--porcelain'])) attempt.verification.push({ name: 'evidence-integrity', exitCode: 1, error: '验证命令修改了提交后的工作区' });
      const ok = attempt.verification.every(passed);
      await emit(dir, 'VERIFICATION_COMPLETED', { attempt: attempt.number, ok, results: attempt.verification }, item.id);
      if (ok) { item.status = 'VERIFIED'; item.feedback = null; }
      else {
        item.status = 'REWORK'; item.feedback = feedbackFor(attempt.verification);
        await emit(dir, 'REWORK_SCHEDULED', { feedback: item.feedback, bounded: true, attemptLimit: item.attemptLimit }, item.id);
      }
      await persist(dir, state);
    }
    if (item.status !== 'VERIFIED') {
      item.status = 'NEEDS_HUMAN';
      return pause(dir, state, 'NEEDS_HUMAN', item, '尝试预算耗尽');
    }
    if (!state.autoApprove) return pause(dir, state, 'NEEDS_HUMAN_REVIEW', item, 'L2 通过，等待 L3 人工裁决');
    await approveItem(dir, state, item, 'POLICY:auto_verified', { kind: 'POLICY', id: 'auto_verified' });
  }
  try { await ensureEvidence(dir, state.workItems.at(-1)); }
  catch (err) {
    const item = state.workItems.at(-1);
    item.status = 'NEEDS_HUMAN';
    item.feedback = { category: 'EVIDENCE_MISMATCH', detail: err.message };
    return pause(dir, state, 'NEEDS_HUMAN', item, err.message);
  }
  state.acceptance = await acceptanceRun(dir, state);
  state.status = state.acceptance.ok ? 'AWAITING_CLIENT' : 'ACCEPTANCE_FAILED';
  await persist(dir, state);
  return state.acceptance.ok ? 0 : 1;
}
async function acceptanceRun(dir, state) {
  const source = inside(state.taskDir, state.task.acceptance.dir);
  const hashes = await treeHashes(source);
  const copy = path.join(dir, 'acceptance-copy');
  await fs.rm(copy, { recursive: true, force: true });
  await copyTree(workspaceOf(dir), copy, true);
  const destination = inside(copy, state.task.acceptance.dir);
  await fs.rm(destination, { recursive: true, force: true });
  await copyTree(source, destination);
  const before = await treeHashes(destination);
  const commitHash = await git(dir, ['rev-parse', 'HEAD']);
  const result = await runCommand(state.task.acceptance.command, { cwd: copy });
  const after = await treeHashes(destination);
  const sourceAfter = await treeHashes(source);
  const integrity = [hashes, before, after, sourceAfter].every(x => JSON.stringify(x) === JSON.stringify(state.acceptanceHashes));
  const acceptance = { ok: passed(result) && integrity, integrity, hashes, result, commit: commitHash, ts: new Date().toISOString() };
  await emit(dir, 'ACCEPTANCE_RUN', acceptance);
  return acceptance;
}
export async function load(dir) {
  dir = path.resolve(dir);
  const chain = await checkChain(dir);
  if (!chain.ok) throw new Error(`事件链损坏: ${chain.error}`);
  return readJSON(path.join(dir, 'state.json'));
}
async function ensureEvidence(dir, item) {
  if (await git(dir, ['rev-parse', 'HEAD']) !== item.attempts.at(-1)?.commit || await git(dir, ['status', '--porcelain'])) throw new Error('工作区与最近验证的 commit 不一致；先 reject 返工');
}
export async function review(dir, id, decision, options = {}) {
  const state = await load(dir), item = state.workItems.find(x => x.id === id);
  if (!item || !['NEEDS_HUMAN_REVIEW', 'NEEDS_HUMAN'].includes(state.status) || !['VERIFIED', 'NEEDS_HUMAN'].includes(item.status)) throw new Error('当前工作项不在待裁决状态');
  const by = options.by ?? 'operator', source = `HUMAN:${by}`, actor = { kind: 'HUMAN', id: by };
  if (decision === 'approve') {
    if (item.status !== 'VERIFIED') throw new Error('只有程序验证通过的工作项可以 approve；否则 reject 指定反馈后 resume');
    await ensureEvidence(dir, item);
    await approveItem(dir, state, item, source, actor);
  } else if (decision === 'reject') {
    if (!options.category?.trim() || !options.detail?.trim()) throw new Error('reject 必须带 --category 和 --detail');
    item.feedback = { category: options.category, detail: options.detail };
    item.status = 'REWORK'; item.authoritySource = source;
    // A human rejection explicitly opens one new bounded rework budget.
    item.attemptLimit = item.attempts.length + state.task.maxAttempts;
    state.status = 'RUNNING';
    await emit(dir, 'DECISION_MADE', { decision, authority_source: source, feedback: item.feedback, attemptLimit: item.attemptLimit }, id, actor);
    await emit(dir, 'REWORK_SCHEDULED', { feedback: item.feedback, attemptLimit: item.attemptLimit }, id);
    await persist(dir, state);
  } else throw new Error('裁决必须是 approve 或 reject');
  return state;
}
export async function resume(dir) {
  const state = await load(dir);
  if (state.workItems.some(x => x.status === 'EXECUTING')) {
    const item = state.workItems.find(x => x.status === 'EXECUTING');
    item.status = 'NEEDS_HUMAN';
    return pause(dir, state, 'NEEDS_HUMAN', item, '上次执行被中断；需 review reject 后开始新的有界预算');
  }
  if (state.status === 'NEEDS_HUMAN' && !state.workItems.length) {
    state.status = 'PLANNING'; // An explicit resume retries planning, never loops indefinitely.
    await emit(dir, 'RUN_RESUMED', { reason: '人工发起规划重试' });
  }
  return continueRun(dir, state);
}
export async function accept(dir, options) {
  const state = await load(dir);
  if (state.status !== 'AWAITING_CLIENT' || !state.acceptance?.ok) throw new Error('只有自动验收通过且等待甲方的 run 可以裁决');
  if (!options.by?.trim()) throw new Error('甲方裁决必须带 --by');
  if (options.reject && !options.detail?.trim()) throw new Error('甲方拒绝必须带 --detail');
  await ensureEvidence(dir, state.workItems.at(-1));
  state.client = { by: options.by, decision: options.reject ? 'reject' : 'accept', detail: options.detail ?? '', authority_source: `HUMAN:${options.by}` };
  await emit(dir, options.reject ? 'CLIENT_REJECTED' : 'CLIENT_ACCEPTED', state.client, undefined, { kind: 'HUMAN', id: options.by });
  state.status = options.reject ? 'CLIENT_REJECTED' : 'COMPLETED';
  if (!options.reject) await emit(dir, 'RUN_COMPLETED', { authority_source: state.client.authority_source });
  await persist(dir, state);
  return state;
}
export async function independentVerify(dir) {
  const chain = await checkChain(dir);
  if (!chain.ok) {
    await report(dir, await readJSON(path.join(dir, 'state.json')));
    return { ok: false, chain }; // Never append to a corrupted log.
  }
  const state = await load(dir);
  const results = await verifyCommands(dir, state);
  await emit(dir, 'VERIFICATION_COMPLETED', { independent: true, results, ok: results.every(passed) });
  const acceptance = await acceptanceRun(dir, state);
  const evidence = { head: await git(dir, ['rev-parse', 'HEAD']), clean: !(await git(dir, ['status', '--porcelain'])) };
  const expected = state.workItems.at(-1)?.attempts.at(-1)?.commit;
  state.independentVerification = { ok: results.every(passed) && acceptance.ok && evidence.clean && evidence.head === expected,
    ts: new Date().toISOString(), results, acceptance, evidence, chain: await checkChain(dir) };
  await persist(dir, state);
  return state.independentVerification;
}
